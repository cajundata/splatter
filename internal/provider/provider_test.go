package provider

import "testing"

func TestErrorFormatting(t *testing.T) {
	e := &Error{Stage: "request", HTTPStatus: 429, Message: "rate limited"}
	if got := e.Error(); got != "request: http 429: rate limited" {
		t.Fatalf("got %q", got)
	}
	e = &Error{Stage: "config", Message: "missing GEMINI_API_KEY"}
	if got := e.Error(); got != "config: missing GEMINI_API_KEY" {
		t.Fatalf("got %q", got)
	}
}
