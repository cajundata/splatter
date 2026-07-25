package gemini

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cajundata/splatter/internal/provider"
)

// tinyPNG returns encoded PNG bytes of a w×h image.
func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// geminiSuccessBody builds a generateContent response carrying one inline PNG.
func geminiSuccessBody(t *testing.T, pngBytes []byte) string {
	t.Helper()
	body := map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{
				"role": "model",
				"parts": []any{
					map[string]any{"text": "here you go"},
					map[string]any{"inlineData": map[string]any{
						"mimeType": "image/png",
						"data":     base64.StdEncoding.EncodeToString(pngBytes),
					}},
				},
			},
			"finishReason": "STOP",
		}},
		"modelVersion": "gemini-2.5-flash-image",
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func serve(t *testing.T, status int, body string, sawPath *string, sawBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sawPath != nil {
			*sawPath = r.URL.Path
		}
		if sawBody != nil {
			buf := new(bytes.Buffer)
			buf.ReadFrom(r.Body)
			*sawBody = buf.Bytes()
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
}

func TestGenerateSuccess(t *testing.T) {
	pngBytes := tinyPNG(t, 3, 4)
	var reqBody []byte
	srv := serve(t, 200, geminiSuccessBody(t, pngBytes), nil, &reqBody)
	defer srv.Close()

	a := New("test-key", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{
		Model: "gemini-2.5-flash-image", Prompt: "a shirt", N: 1, Aspect: "portrait_4_5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].W != 3 || res.Images[0].H != 4 {
		t.Fatalf("images: %d, dims %dx%d", len(res.Images), res.Images[0].W, res.Images[0].H)
	}
	if !bytes.Equal(res.Images[0].Bytes, pngBytes) {
		t.Fatal("image bytes altered")
	}
	if res.AspectActual != "3:4" {
		t.Fatalf("aspect_actual: %q", res.AspectActual)
	}
	if res.Meta.ModelReturned != "gemini-2.5-flash-image" || res.Meta.HTTPStatus != 200 {
		t.Fatalf("meta: %#v", res.Meta)
	}
	if res.Latency <= 0 || len(res.Raw) == 0 {
		t.Fatalf("latency %v, raw %d bytes", res.Latency, len(res.Raw))
	}
	if res.Cost.Source != "" || res.Cost.USD != nil {
		t.Fatalf("gemini never reports cost: %#v", res.Cost)
	}
	// image mode: request must carry responseModalities and the aspect, no tools
	req := string(reqBody)
	for _, want := range []string{"responseModalities", "IMAGE", "3:4"} {
		if !strings.Contains(req, want) {
			t.Fatalf("request missing %q:\n%s", want, req)
		}
	}
	if strings.Contains(req, `"tools"`) {
		t.Fatal("image-mode request must not carry tools")
	}
}

func TestGenerateConfigErrors(t *testing.T) {
	a := New("k", "http://unused.invalid")
	cases := []struct {
		name string
		req  provider.Request
		want string
	}{
		{"bad aspect", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "wide"}, "aspect"},
		{"n over max", provider.Request{Model: "m", Prompt: "p", N: 2, Aspect: "square"}, "batch"},
		{"seed unsupported", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
			Seed: ptr(int64(7))}, "seed"},
		{"unknown native", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
			Native: map[string]any{"quality": "high"}}, "native"},
	}
	for _, tc := range cases {
		_, err := a.Generate(context.Background(), tc.req)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Stage != "config" || !strings.Contains(pe.Message, tc.want) {
			t.Fatalf("%s: want config error containing %q, got %v", tc.name, tc.want, err)
		}
	}
	// missing key
	a = New("", "http://unused.invalid")
	_, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "config" || !strings.Contains(pe.Message, "GEMINI_API_KEY") {
		t.Fatalf("want missing-key config error, got %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestGenerateAPIErrorNormalized(t *testing.T) {
	srv := serve(t, 429, `{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`, nil, nil)
	defer srv.Close()
	a := New("k", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("want *provider.Error, got %T: %v", err, err)
	}
	if pe.Stage != "request" || pe.HTTPStatus != 429 || !strings.Contains(pe.Message, "quota") {
		t.Fatalf("bad normalization: %#v", pe)
	}
	// partial result preserves the failed exchange's evidence
	if res.Latency <= 0 || res.Meta.HTTPStatus != 429 || len(res.Raw) == 0 {
		t.Fatalf("partial result lost evidence: %+v", res)
	}
}

func TestGenerateNoImageIsDecodeError(t *testing.T) {
	body := `{"candidates":[{"content":{"role":"model","parts":[{"text":"cannot help"}]},"finishReason":"STOP"}]}`
	srv := serve(t, 200, body, nil, nil)
	defer srv.Close()
	a := New("k", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "decode" {
		t.Fatalf("want decode-stage error, got %v", err)
	}
	// decode failures still reached the wire: partial evidence must survive
	if res.Latency <= 0 || res.Meta.HTTPStatus != 200 || len(res.Raw) == 0 {
		t.Fatalf("decode failure lost wire evidence: %+v", res)
	}
}

func TestCapabilities(t *testing.T) {
	c := New("k", "").Capabilities()
	if c.MaxBatch != 1 || c.Seed || c.Img2Img || c.Edit {
		t.Fatalf("capabilities: %#v", c)
	}
	want := []string{"1:1", "3:4", "2:3", "4:3"}
	if len(c.AspectModes) != len(want) {
		t.Fatalf("aspect modes: %#v", c.AspectModes)
	}
}
