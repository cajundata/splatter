// Package openai adapts the OpenAI Images API (openai-go/v3,
// client.Images.Generate, synchronous) to splatter's Provider interface.
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/png"

	oai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/transport"
)

type Adapter struct {
	apiKey  string
	baseURL string
}

func New(apiKey, baseURL string) *Adapter {
	return &Adapter{apiKey: apiKey, baseURL: baseURL}
}

func (a *Adapter) Name() string { return "openai" }

// aspectMap: harness vocabulary -> the API's three native sizes. Both
// portrait aspects share 1024x1536 (nearest native); aspect_actual records it.
var aspectMap = map[string]string{
	"square":        "1024x1024",
	"portrait_4_5":  "1024x1536",
	"portrait_2_3":  "1024x1536",
	"landscape_4_3": "1536x1024",
}

func (a *Adapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		MaxBatch:    10, // images API documented n limit
		AspectModes: []string{"1024x1024", "1024x1536", "1536x1024"},
	}
}

func configErr(format string, args ...any) *provider.Error {
	return &provider.Error{Stage: "config", Message: fmt.Sprintf(format, args...)}
}

func (a *Adapter) Generate(ctx context.Context, req provider.Request) (provider.Result, error) {
	var zero provider.Result
	if a.apiKey == "" {
		return zero, configErr("missing OPENAI_API_KEY")
	}
	native, ok := aspectMap[req.Aspect]
	if !ok {
		return zero, configErr("unsupported aspect %q", req.Aspect)
	}
	if req.N < 1 || req.N > 10 {
		return zero, configErr("n=%d outside batch range 1..10", req.N)
	}
	if req.Seed != nil {
		return zero, configErr("seed not supported")
	}
	params := oai.ImageGenerateParams{
		Prompt: req.Prompt,
		Model:  oai.ImageModel(req.Model),
		N:      oai.Int(int64(req.N)),
		Size:   oai.ImageGenerateParamsSize(native),
	}
	for k, v := range req.Native {
		switch k {
		case "quality":
			s, ok := v.(string)
			if !ok {
				return zero, configErr("native quality must be a string, got %T", v)
			}
			params.Quality = oai.ImageGenerateParamsQuality(s)
		default:
			return zero, configErr("unsupported native key %q", k)
		}
	}

	rec, client := transport.NewRecorder("X-Request-Id")
	opts := []option.RequestOption{
		option.WithAPIKey(a.apiKey),
		option.WithHTTPClient(client),
		// One call = one HTTP exchange = one evidence record. Retrying is
		// the operator's decision, not the adapter's.
		option.WithMaxRetries(0),
	}
	if a.baseURL != "" {
		opts = append(opts, option.WithBaseURL(a.baseURL))
	}
	cl := oai.NewClient(opts...)

	resp, err := cl.Images.Generate(ctx, params)
	if err != nil {
		// Partial result: preserve the failed exchange's evidence.
		partial := provider.Result{Latency: rec.Latency(), Raw: rec.Body(),
			Meta: provider.CallMeta{HTTPStatus: rec.HTTPStatus()}}
		return partial, normalize(err, rec.HTTPStatus())
	}

	// Decode-stage failures still reached the wire: preserve the exchange's
	// evidence (Latency/Raw/Meta.HTTPStatus) in the partial Result alongside
	// the error, same as request-stage failures.
	decodePartial := func() provider.Result {
		return provider.Result{Latency: rec.Latency(), Raw: rec.Body(),
			Meta: provider.CallMeta{
				ModelReturned:     req.Model,
				ProviderRequestID: rec.RequestID(),
				HTTPStatus:        rec.HTTPStatus(),
			}}
	}

	if len(resp.Data) == 0 {
		return decodePartial(), &provider.Error{Stage: "decode", Message: "response contained no images"}
	}
	images := make([]provider.Image, 0, len(resp.Data))
	for i, d := range resp.Data {
		raw, derr := base64.StdEncoding.DecodeString(d.B64JSON)
		if derr != nil {
			return decodePartial(), &provider.Error{Stage: "decode", Message: fmt.Sprintf("data[%d]: bad base64: %v", i, derr)}
		}
		cfgImg, _, derr := image.DecodeConfig(bytes.NewReader(raw))
		if derr != nil {
			return decodePartial(), &provider.Error{Stage: "decode", Message: fmt.Sprintf("data[%d]: undecodable image: %v", i, derr)}
		}
		images = append(images, provider.Image{Bytes: raw, W: cfgImg.Width, H: cfgImg.Height})
	}

	return provider.Result{
		Images:  images,
		Latency: rec.Latency(),
		// The images response reports token usage, not dollars; the
		// resolver applies the pricing table.
		Meta: provider.CallMeta{
			// The images response does not echo the model; record what
			// was requested (model_requested == model_returned here).
			ModelReturned:     req.Model,
			ProviderRequestID: rec.RequestID(),
			HTTPStatus:        rec.HTTPStatus(),
		},
		Raw:          rec.Body(),
		AspectActual: native,
	}, nil
}

func normalize(err error, status int) *provider.Error {
	var apiErr *oai.Error
	if errors.As(err, &apiErr) {
		return &provider.Error{Stage: "request", HTTPStatus: apiErr.StatusCode, Message: apiErr.Message}
	}
	return &provider.Error{Stage: "request", HTTPStatus: status, Message: err.Error()}
}
