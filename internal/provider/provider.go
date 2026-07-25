package provider

import (
	"context"
	"fmt"
	"time"
)

type Provider interface {
	Name() string
	Capabilities() Capabilities
	Generate(ctx context.Context, req Request) (Result, error)
}

type Capabilities struct {
	Img2Img, Edit, Seed bool
	MaxBatch            int
	AspectModes         []string // provider-native vocabulary
}

type Request struct {
	Model  string
	Prompt string
	N      int
	Aspect string // harness vocabulary: square, portrait_4_5, portrait_2_3, landscape_4_3
	Seed   *int64
	Native map[string]any // provider-specific, recorded verbatim in manifest
}

type Image struct {
	Bytes []byte
	W, H  int
}

// Cost as reported by the adapter. Source is "reported" only when a dollar
// figure was parsed from the wire response; otherwise leave zero-valued and
// internal/run's resolver applies table/unavailable per the locked precedence.
type Cost struct {
	USD    *float64
	Source string
}

type CallMeta struct {
	ModelReturned     string
	ProviderRequestID string
	HTTPStatus        int
}

// Result is the adapter's return value. On a "request"-stage error the
// adapter returns a PARTIAL Result alongside the error — Latency, Raw, and
// Meta.HTTPStatus from the failed exchange — because the spec requires
// failed-call records to preserve latency and status when the call reached
// the wire. Config-stage errors return a zero Result.
type Result struct {
	Images       []Image
	Latency      time.Duration // from the instrumented transport
	Cost         Cost
	Meta         CallMeta
	Raw          []byte // captured wire body (redacted later by internal/run)
	AspectActual string // provider-native mode actually requested
}

// Error is the normalized per-adapter error. Stage: "config" (before any
// wire call), "request" (transport/HTTP), "decode" (unusable response).
type Error struct {
	Stage      string
	HTTPStatus int
	Message    string
}

func (e *Error) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("%s: http %d: %s", e.Stage, e.HTTPStatus, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Stage, e.Message)
}
