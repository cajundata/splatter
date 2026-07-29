// Package spacestest provides an in-memory S3-compatible fake for
// exercising spaces.Client and the push/pull commands offline.
package spacestest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type Server struct {
	URL       string
	Bucket    string
	AccessKey string

	mu    sync.Mutex
	blobs map[string][]byte
}

// New starts a fake Spaces server; it is closed via t.Cleanup.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{Bucket: "test-bucket", AccessKey: "TESTKEY", blobs: map[string][]byte{}}
	ts := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(ts.Close)
	s.URL = ts.URL
	return s
}

// Put seeds (or corrupts) a stored blob directly.
func (s *Server) Put(key string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[key] = data
}

// Get returns a stored blob.
func (s *Server) Get(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[key]
	return b, ok
}

// Len reports how many blobs are stored.
func (s *Server) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blobs)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+s.AccessKey+"/") ||
		!strings.Contains(auth, "SignedHeaders=") ||
		!strings.Contains(auth, "host") ||
		!strings.Contains(auth, "x-amz-content-sha256") ||
		!strings.Contains(auth, "x-amz-date") {
		http.Error(w, "SignatureDoesNotMatch", http.StatusForbidden)
		return
	}
	// Path-style: /<bucket>/<key...>
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 || parts[0] != s.Bucket || parts[1] == "" {
		http.Error(w, "NoSuchBucket", http.StatusNotFound)
		return
	}
	key := parts[1]
	switch r.Method {
	case http.MethodHead:
		if _, ok := s.Get(key); !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		b, ok := s.Get(key)
		if !ok {
			http.Error(w, "NoSuchKey", http.StatusNotFound)
			return
		}
		w.Write(b)
	case http.MethodPut:
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.Put(key, b)
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "MethodNotAllowed", http.StatusMethodNotAllowed)
	}
}
