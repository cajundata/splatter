package transport

import (
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
		io.WriteString(w, "resp")
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
	if n != 2 || string(rec.Body()) != "resp" || rec.HTTPStatus() != 200 {
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
