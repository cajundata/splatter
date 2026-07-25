// Package transport provides the instrumented RoundTripper that measures
// wall-clock latency around real HTTP exchanges and captures raw response
// bodies. It has no field for request headers: credentials structurally
// cannot be persisted from here.
package transport

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

type Recorder struct {
	requestIDHeader string
	latency         time.Duration
	body            []byte
	status          int
	requestID       string
}

// NewRecorder returns a Recorder and an *http.Client whose transport
// records through it. One Recorder per provider call; on multiple
// exchanges (SDK retries) the last one wins.
func NewRecorder(requestIDHeader string) (*Recorder, *http.Client) {
	r := &Recorder{requestIDHeader: requestIDHeader}
	return r, &http.Client{Transport: roundTripper{rec: r}}
}

func (r *Recorder) Latency() time.Duration { return r.latency }
func (r *Recorder) Body() []byte           { return r.body }
func (r *Recorder) HTTPStatus() int        { return r.status }
func (r *Recorder) RequestID() string      { return r.requestID }

type roundTripper struct{ rec *Recorder }

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := http.DefaultTransport.RoundTrip(req)
	rt.rec.latency = time.Since(start)
	rt.rec.body, rt.rec.status, rt.rec.requestID = nil, 0, ""
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	rt.rec.body = body
	rt.rec.status = resp.StatusCode
	if rt.rec.requestIDHeader != "" {
		rt.rec.requestID = resp.Header.Get(rt.rec.requestIDHeader)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}
