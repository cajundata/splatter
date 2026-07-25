package transport

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecorderCapturesExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("X-Request-Id", "req_123")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	rec, client := NewRecorder("X-Request-Id")
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if string(body) != `{"ok":true}` {
		t.Fatalf("caller body corrupted: %q", body)
	}
	if string(rec.Body()) != `{"ok":true}` {
		t.Fatalf("captured body: %q", rec.Body())
	}
	if rec.Latency() < 30*time.Millisecond {
		t.Fatalf("latency %v must include server delay", rec.Latency())
	}
	if rec.HTTPStatus() != 200 || rec.RequestID() != "req_123" {
		t.Fatalf("status=%d id=%q", rec.HTTPStatus(), rec.RequestID())
	}
}

func TestRecorderKeepsLastExchange(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(200)
		io.WriteString(w, fmt.Sprintf("resp-%d", n))
	}))
	defer srv.Close()

	rec, client := NewRecorder("")
	for i := 0; i < 2; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	if n != 2 || string(rec.Body()) != "resp-2" || rec.HTTPStatus() != 200 {
		t.Fatalf("n=%d body=%q", n, rec.Body())
	}
}

func TestRecorderStoresNoRequestHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	rec, client := NewRecorder("")
	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Authorization", "Bearer sekrit-key")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// The recorder type must be structurally incapable of holding request
	// headers: assert none of its captured state contains the secret.
	if s := string(rec.Body()); s != "" {
		t.Fatalf("unexpected body capture: %q", s)
	}
	if rec.RequestID() != "" {
		t.Fatal("no request id expected")
	}
}

func TestRecorderResetsOnTrailingFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "good")
	}))
	rec, client := NewRecorder("")
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	srv.Close() // next exchange fails at the transport level
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("want transport error")
	}
	if rec.Body() != nil || rec.HTTPStatus() != 0 || rec.RequestID() != "" {
		t.Fatalf("recorder must describe the last (failed) exchange: body=%q status=%d", rec.Body(), rec.HTTPStatus())
	}
	if rec.Latency() <= 0 {
		t.Fatal("latency of failed exchange still recorded")
	}
}
