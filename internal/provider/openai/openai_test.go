package openai

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

func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func successBody(t *testing.T, pngs ...[]byte) string {
	t.Helper()
	data := make([]any, 0, len(pngs))
	for _, p := range pngs {
		data = append(data, map[string]any{"b64_json": base64.StdEncoding.EncodeToString(p)})
	}
	b, err := json.Marshal(map[string]any{"created": 1753500000, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func serve(t *testing.T, status int, body string, sawBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sawBody != nil {
			buf := new(bytes.Buffer)
			buf.ReadFrom(r.Body)
			*sawBody = buf.Bytes()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req_oai_1")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
}

func TestGenerateSuccessBatch(t *testing.T) {
	png1, png2 := tinyPNG(t, 4, 3), tinyPNG(t, 4, 3)
	var reqBody []byte
	srv := serve(t, 200, successBody(t, png1, png2), &reqBody)
	defer srv.Close()

	a := New("test-key", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{
		Model: "gpt-image-1", Prompt: "a shirt", N: 2, Aspect: "landscape_4_3",
		Native: map[string]any{"quality": "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 2 || res.Images[0].W != 4 || res.Images[0].H != 3 {
		t.Fatalf("images: %d", len(res.Images))
	}
	if res.AspectActual != "1536x1024" {
		t.Fatalf("aspect_actual: %q", res.AspectActual)
	}
	if res.Meta.HTTPStatus != 200 || res.Meta.ProviderRequestID != "req_oai_1" {
		t.Fatalf("meta: %#v", res.Meta)
	}
	if res.Meta.ModelReturned != "gpt-image-1" {
		t.Fatalf("model_returned: %q", res.Meta.ModelReturned)
	}
	if res.Latency <= 0 || len(res.Raw) == 0 {
		t.Fatalf("latency %v raw %d", res.Latency, len(res.Raw))
	}
	req := string(reqBody)
	for _, want := range []string{`"n":2`, `"size":"1536x1024"`, `"quality":"high"`, `"model":"gpt-image-1"`} {
		if !strings.Contains(req, want) {
			t.Fatalf("request missing %s:\n%s", want, req)
		}
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
		{"n over max", provider.Request{Model: "m", Prompt: "p", N: 11, Aspect: "square"}, "batch"},
		{"seed", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square", Seed: ptr(int64(1))}, "seed"},
		{"unknown native", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
			Native: map[string]any{"style": "vivid"}}, "native"},
		{"non-string quality", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
			Native: map[string]any{"quality": 7}}, "quality"},
	}
	for _, tc := range cases {
		_, err := a.Generate(context.Background(), tc.req)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Stage != "config" || !strings.Contains(pe.Message, tc.want) {
			t.Fatalf("%s: want config error containing %q, got %v", tc.name, tc.want, err)
		}
	}
	a = New("", "http://unused.invalid")
	_, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "config" || !strings.Contains(pe.Message, "OPENAI_API_KEY") {
		t.Fatalf("want missing-key error, got %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestGenerateAPIErrorNormalized(t *testing.T) {
	srv := serve(t, 429, `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`, nil)
	defer srv.Close()
	a := New("k", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("want *provider.Error, got %T: %v", err, err)
	}
	if pe.Stage != "request" || pe.HTTPStatus != 429 || !strings.Contains(pe.Message, "Rate limit") {
		t.Fatalf("bad normalization: %#v", pe)
	}
	if res.Latency <= 0 || res.Meta.HTTPStatus != 429 || len(res.Raw) == 0 {
		t.Fatalf("partial result lost evidence: %+v", res)
	}
}

func TestGenerateEmptyDataIsDecodeError(t *testing.T) {
	srv := serve(t, 200, `{"created":1753500000,"data":[]}`, nil)
	defer srv.Close()
	a := New("k", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "decode" {
		t.Fatalf("want decode error, got %v", err)
	}
	if res.Latency <= 0 || res.Meta.HTTPStatus != 200 || len(res.Raw) == 0 {
		t.Fatalf("partial result lost evidence: %+v", res)
	}
}

func TestCapabilities(t *testing.T) {
	c := New("k", "").Capabilities()
	if c.MaxBatch != 10 || c.Seed || c.Img2Img || c.Edit {
		t.Fatalf("capabilities: %#v", c)
	}
}
