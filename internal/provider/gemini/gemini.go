// Package gemini adapts the Gemini API (google.golang.org/genai) to
// splatter's Provider interface. Image mode per the Starshp reference:
// responseModalities TEXT+IMAGE, no tools (the API rejects tools alongside
// image output), images arrive as InlineData parts. Synchronous only.
package gemini

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/png"
	"strings"

	"google.golang.org/genai"

	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/transport"
)

type Adapter struct {
	apiKey  string
	baseURL string // empty = production endpoint; tests inject httptest URL
}

func New(apiKey, baseURL string) *Adapter {
	return &Adapter{apiKey: apiKey, baseURL: baseURL}
}

func (a *Adapter) Name() string { return "gemini" }

// aspectMap: harness vocabulary -> nearest genai-native aspectRatio.
// The API supports 1:1, 2:3, 3:2, 3:4, 4:3, 9:16, 16:9, 21:9 — no 4:5,
// so portrait_4_5 maps to 3:4 (nearest) and aspect_actual records that.
var aspectMap = map[string]string{
	"square":        "1:1",
	"portrait_4_5":  "3:4",
	"portrait_2_3":  "2:3",
	"landscape_4_3": "4:3",
}

func (a *Adapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		// One image per call for this model; -n above 1 fails the
		// capability check rather than silently looping.
		MaxBatch:    1,
		AspectModes: []string{"1:1", "3:4", "2:3", "4:3"},
	}
}

func configErr(format string, args ...any) *provider.Error {
	return &provider.Error{Stage: "config", Message: fmt.Sprintf(format, args...)}
}

// validateRequest runs every config-stage check with zero network and
// zero filesystem. Shared by Generate and Preflight so a passing
// pre-flight means Generate cannot fail at config stage (client
// construction aside).
func (a *Adapter) validateRequest(req provider.Request) (string, error) {
	if a.apiKey == "" {
		return "", configErr("missing GEMINI_API_KEY")
	}
	native, ok := aspectMap[req.Aspect]
	if !ok {
		return "", configErr("unsupported aspect %q", req.Aspect)
	}
	if req.N < 1 || req.N > 1 {
		return "", configErr("n=%d outside batch range 1..1", req.N)
	}
	if req.Seed != nil {
		return "", configErr("seed not supported")
	}
	// v1 allowlist is empty: any native key is unsupported, never ignored.
	for k := range req.Native {
		return "", configErr("unsupported native key %q", k)
	}
	return native, nil
}

// Preflight runs the adapter's config-stage checks without touching the
// network. Discovered by internal/run via type assertion; the frozen
// Provider interface is untouched.
func (a *Adapter) Preflight(req provider.Request) error {
	_, err := a.validateRequest(req)
	return err
}

func (a *Adapter) Generate(ctx context.Context, req provider.Request) (provider.Result, error) {
	var zero provider.Result
	native, err := a.validateRequest(req)
	if err != nil {
		return zero, err
	}

	rec, client := transport.NewRecorder("")
	cc := &genai.ClientConfig{APIKey: a.apiKey, Backend: genai.BackendGeminiAPI, HTTPClient: client}
	if a.baseURL != "" {
		cc.HTTPOptions.BaseURL = a.baseURL
	}
	cl, err := genai.NewClient(ctx, cc)
	if err != nil {
		return zero, configErr("client: %v", err)
	}

	cfg := &genai.GenerateContentConfig{
		ResponseModalities: []string{"TEXT", "IMAGE"},
		ImageConfig:        &genai.ImageConfig{AspectRatio: native},
	}
	contents := []*genai.Content{genai.NewContentFromText(req.Prompt, genai.RoleUser)}
	resp, err := cl.Models.GenerateContent(ctx, req.Model, contents, cfg)
	if err != nil {
		// Partial result: the failed exchange's evidence survives into the
		// manifest's failed-call record.
		partial := provider.Result{Latency: rec.Latency(), Raw: rec.Body(),
			Meta: provider.CallMeta{HTTPStatus: rec.HTTPStatus()}}
		return partial, normalize(err, rec.HTTPStatus())
	}

	// Decode-stage failures still reached the wire: preserve the exchange's
	// evidence (Latency/Raw/Meta.HTTPStatus) in the partial Result alongside
	// the error, same as request-stage failures.
	decodePartial := func() provider.Result {
		return provider.Result{Latency: rec.Latency(), Raw: rec.Body(),
			Meta: provider.CallMeta{ModelReturned: resp.ModelVersion, HTTPStatus: rec.HTTPStatus()}}
	}

	var images []provider.Image
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.InlineData == nil || !strings.HasPrefix(part.InlineData.MIMEType, "image/") {
				continue
			}
			data := part.InlineData.Data
			cfgImg, _, derr := image.DecodeConfig(bytes.NewReader(data))
			if derr != nil {
				return decodePartial(), &provider.Error{Stage: "decode",
					Message: fmt.Sprintf("undecodable %s payload: %v", part.InlineData.MIMEType, derr)}
			}
			images = append(images, provider.Image{Bytes: data, W: cfgImg.Width, H: cfgImg.Height})
		}
	}
	if len(images) == 0 {
		return decodePartial(), &provider.Error{Stage: "decode", Message: "response contained no image parts"}
	}

	return provider.Result{
		Images:  images,
		Latency: rec.Latency(),
		// Gemini reports no dollar figure; leave Cost zero for the
		// resolver's table/unavailable precedence.
		Meta: provider.CallMeta{
			ModelReturned: resp.ModelVersion,
			HTTPStatus:    rec.HTTPStatus(),
		},
		Raw:          rec.Body(),
		AspectActual: native,
	}, nil
}

// normalize maps a genai SDK error to the per-adapter normalized form.
func normalize(err error, status int) *provider.Error {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return &provider.Error{Stage: "request", HTTPStatus: apiErr.Code, Message: apiErr.Message}
	}
	return &provider.Error{Stage: "request", HTTPStatus: status, Message: err.Error()}
}
