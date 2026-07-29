# S4 Spaces Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `splatter push`/`splatter pull` sync manifest-referenced image blobs with a DigitalOcean Spaces bucket (flat `blobs/<sha256>` keys), plus honest sync reporting in `splatter status`.

**Architecture:** New `internal/spaces` package: hand-rolled SigV4 signing over `net/http` with three operations (HEAD/PUT/GET) — zero new dependencies. `internal/workspace` gains `BlobRefs`, the manifest scanner both commands share. Thin `push`/`pull` cobra commands orchestrate; an `httptest`-backed fake Spaces server (`internal/spaces/spacestest`) makes everything testable offline.

**Tech Stack:** Go 1.26 stdlib only (crypto/hmac, crypto/sha256, net/http, net/http/httptest), cobra (existing).

**Spec:** `docs/superpowers/specs/2026-07-29-s4-spaces-sync-design.md` — its locked decisions govern.

## Global Constraints

- No new module dependencies. `go.mod` gains nothing.
- All file writes go through `internal/fsio` primitives (`ReplaceFile` for the downloaded blobs). Nothing else writes files.
- All paths through `path/filepath`; manifest-stored paths are relative and slash-normalized (`filepath.FromSlash` to touch disk, `filepath.ToSlash` to store).
- Exit codes: 0 success, 1 runtime error, 2 usage error. Missing/invalid `SPLATTER_S3_*` config is a **runtime** error (exit 1), matching S2's missing-API-key behavior. Unknown flags/args → usage (2) via the existing `usageArgs`/`usageErr` machinery.
- Every command supports `--json` via the existing `emit` helper: result object on stdout, logs/errors on stderr.
- Nothing deletes, locally or remotely (spec §7 GC policy).
- No new `runtime.GOOS` branches (the browser-open helper stays the only one).
- Commit style: `feat:`/`docs:`/`test:` prefixes, no attribution trailers.
- After every task: `go build ./... && go test ./...` green before commit.

---

### Task 1: SigV4 request signing (`internal/spaces/sigv4.go`)

**Files:**
- Create: `internal/spaces/sigv4.go`
- Test: `internal/spaces/sigv4_test.go`

**Interfaces:**
- Consumes: nothing (stdlib only).
- Produces: `Sign(req *http.Request, accessKey, secretKey, region, payloadSHA256 string, now time.Time)` and `const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"` — Task 3's client calls both.

- [ ] **Step 1: Write the failing test**

The vector is AWS's published worked example ("Authenticating Requests (AWS Signature Version 4) → Signature Calculations: Examples Using GET"): GET `test.txt` from `examplebucket`, fixed date 2013-05-24, known credentials, known final Authorization header. If this test ever fails, the constant is authoritative — fix the implementation, never the vector.

```go
package spaces

import (
	"net/http"
	"testing"
	"time"
)

// AWS's published S3 SigV4 GET-object example: known inputs, known
// Authorization header. The vector is authoritative.
func TestSignMatchesAWSGetObjectExample(t *testing.T) {
	req, err := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	Sign(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"us-east-1", emptyPayloadSHA256, now)

	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization mismatch:\n got: %s\nwant: %s", got, want)
	}
	if got := req.Header.Get("x-amz-date"); got != "20130524T000000Z" {
		t.Fatalf("x-amz-date: %q", got)
	}
	if got := req.Header.Get("x-amz-content-sha256"); got != emptyPayloadSHA256 {
		t.Fatalf("x-amz-content-sha256: %q", got)
	}
}

// Signing must be deterministic: same inputs, same signature.
func TestSignDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sig := func() string {
		req, _ := http.NewRequest("PUT", "https://nyc3.digitaloceanspaces.com/bkt/blobs/abc123", nil)
		req.Header.Set("Content-Type", "image/png")
		Sign(req, "AK", "SK", "nyc3", "deadbeef", now)
		return req.Header.Get("Authorization")
	}
	if a, b := sig(), sig(); a != b {
		t.Fatalf("not deterministic:\n%s\n%s", a, b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spaces/ -run TestSign -v`
Expected: FAIL — compile error, `Sign` and `emptyPayloadSHA256` undefined.

- [ ] **Step 3: Write the implementation**

```go
// Package spaces is a minimal S3-compatible client for DigitalOcean
// Spaces blob sync: SigV4 signing plus HEAD/PUT/GET on flat keys. It
// knows nothing about the filesystem or the workspace.
package spaces

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// emptyPayloadSHA256 is sha256("") — the payload hash for bodyless
// requests (GET, HEAD).
const emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// Sign adds x-amz-date, x-amz-content-sha256, and Authorization SigV4
// headers to req, signing host plus every header already set. The URL
// path must not need URI encoding beyond EscapedPath (splatter keys are
// hex, so this always holds); the query string must already be in
// canonical form (splatter sends none).
func Sign(req *http.Request, accessKey, secretKey, region, payloadSHA256 string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadSHA256)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}

	names := []string{"host"}
	for name := range req.Header {
		names = append(names, strings.ToLower(name))
	}
	sort.Strings(names)

	var canonHeaders strings.Builder
	for _, n := range names {
		v := host
		if n != "host" {
			v = strings.TrimSpace(req.Header.Get(n))
		}
		canonHeaders.WriteString(n)
		canonHeaders.WriteByte(':')
		canonHeaders.WriteString(v)
		canonHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonHeaders.String(),
		signedHeaders,
		payloadSHA256,
	}, "\n")

	scope := dateStamp + "/" + region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hexSHA256([]byte(canonical)),
	}, "\n")

	key := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	key = hmacSHA256(key, []byte(region))
	key = hmacSHA256(key, []byte("s3"))
	key = hmacSHA256(key, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))

	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+
			", SignedHeaders="+signedHeaders+
			", Signature="+signature)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
```

Note the join structure: `canonHeaders.String()` ends with `\n`, so joining with `\n` produces the required blank line between the header block and the signed-headers list.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spaces/ -run TestSign -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/spaces/sigv4.go internal/spaces/sigv4_test.go
git commit -m "feat: SigV4 request signing for spaces sync"
```

---

### Task 2: Config from environment (`internal/spaces/config.go`)

**Files:**
- Create: `internal/spaces/config.go`
- Test: `internal/spaces/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Config struct { Endpoint, Bucket, AccessKey, SecretKey, Region string }` and `FromEnv() (Config, error)` — used by Task 3's `New(cfg Config)`, Task 5/6 commands, and Task 7's status.

- [ ] **Step 1: Write the failing test**

```go
package spaces

import (
	"strings"
	"testing"
)

func setAllEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SPLATTER_S3_ENDPOINT", "https://nyc3.digitaloceanspaces.com")
	t.Setenv("SPLATTER_S3_BUCKET", "splats")
	t.Setenv("SPLATTER_S3_ACCESS_KEY", "AK")
	t.Setenv("SPLATTER_S3_SECRET_KEY", "SK")
	t.Setenv("SPLATTER_S3_REGION", "")
}

func TestFromEnvComplete(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_REGION", "ams3")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Endpoint: "https://nyc3.digitaloceanspaces.com", Bucket: "splats",
		AccessKey: "AK", SecretKey: "SK", Region: "ams3"}
	if cfg != want {
		t.Fatalf("got %+v want %+v", cfg, want)
	}
}

func TestFromEnvDerivesRegionFromEndpoint(t *testing.T) {
	setAllEnv(t)
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "nyc3" {
		t.Fatalf("region: got %q want nyc3", cfg.Region)
	}
}

func TestFromEnvRegionFallback(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_ENDPOINT", "https://localhost:9000")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("region: got %q want us-east-1", cfg.Region)
	}
}

// The error must name every missing var at once, not just the first.
func TestFromEnvNamesAllMissingVars(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_BUCKET", "")
	t.Setenv("SPLATTER_S3_SECRET_KEY", "")
	_, err := FromEnv()
	if err == nil {
		t.Fatal("want error")
	}
	for _, name := range []string{"SPLATTER_S3_BUCKET", "SPLATTER_S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error %q should name %s", err, name)
		}
	}
	if strings.Contains(err.Error(), "SPLATTER_S3_ENDPOINT") {
		t.Fatalf("error %q names a var that is set", err)
	}
}

func TestFromEnvRejectsBadEndpoint(t *testing.T) {
	setAllEnv(t)
	t.Setenv("SPLATTER_S3_ENDPOINT", "nyc3.digitaloceanspaces.com") // no scheme
	if _, err := FromEnv(); err == nil {
		t.Fatal("want error for endpoint without scheme")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spaces/ -run TestFromEnv -v`
Expected: FAIL — compile error, `Config` and `FromEnv` undefined.

- [ ] **Step 3: Write the implementation**

```go
package spaces

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config carries the Spaces connection settings (spec §7 env vars).
type Config struct {
	Endpoint  string // SPLATTER_S3_ENDPOINT, e.g. https://nyc3.digitaloceanspaces.com
	Bucket    string // SPLATTER_S3_BUCKET
	AccessKey string // SPLATTER_S3_ACCESS_KEY
	SecretKey string // SPLATTER_S3_SECRET_KEY
	Region    string // SPLATTER_S3_REGION, optional
}

// FromEnv builds Config from the SPLATTER_S3_* variables. The error
// names every missing variable at once. Region defaults to the first
// hostname label of the endpoint (nyc3.digitaloceanspaces.com → nyc3),
// falling back to us-east-1.
func FromEnv() (Config, error) {
	cfg := Config{
		Endpoint:  os.Getenv("SPLATTER_S3_ENDPOINT"),
		Bucket:    os.Getenv("SPLATTER_S3_BUCKET"),
		AccessKey: os.Getenv("SPLATTER_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("SPLATTER_S3_SECRET_KEY"),
		Region:    os.Getenv("SPLATTER_S3_REGION"),
	}
	var missing []string
	for _, v := range []struct{ name, val string }{
		{"SPLATTER_S3_ENDPOINT", cfg.Endpoint},
		{"SPLATTER_S3_BUCKET", cfg.Bucket},
		{"SPLATTER_S3_ACCESS_KEY", cfg.AccessKey},
		{"SPLATTER_S3_SECRET_KEY", cfg.SecretKey},
	} {
		if v.val == "" {
			missing = append(missing, v.name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("spaces sync not configured: missing %s", strings.Join(missing, ", "))
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return Config{}, fmt.Errorf("SPLATTER_S3_ENDPOINT %q: must be an http(s) URL", cfg.Endpoint)
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
		host := u.Hostname()
		if i := strings.Index(host, "."); i > 0 {
			cfg.Region = host[:i]
		}
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spaces/ -run TestFromEnv -v`
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add internal/spaces/config.go internal/spaces/config_test.go
git commit -m "feat: spaces config from SPLATTER_S3_* env vars"
```

---

### Task 3: Spaces client + fake server (`internal/spaces/client.go`, `internal/spaces/spacestest`)

**Files:**
- Create: `internal/spaces/client.go`
- Create: `internal/spaces/spacestest/fake.go`
- Test: `internal/spaces/client_test.go`

**Interfaces:**
- Consumes: `Sign`, `emptyPayloadSHA256` (Task 1); `Config` (Task 2).
- Produces:
  - `New(cfg Config) *Client`
  - `(*Client) Head(ctx context.Context, key string) (bool, error)`
  - `(*Client) Put(ctx context.Context, key string, body []byte, payloadSHA256 string) error`
  - `(*Client) Get(ctx context.Context, key string) (io.ReadCloser, error)`
  - `var ErrNotFound = errors.New("not found")` (Get wraps it on 404)
  - `spacestest.New(t *testing.T) *spacestest.Server` with fields `URL`, `Bucket` ("test-bucket"), `AccessKey` ("TESTKEY") and methods `Put(key string, data []byte)`, `Get(key string) ([]byte, bool)`, `Len() int` — Tasks 5/6 CLI tests use this.

- [ ] **Step 1: Write the fake server**

The fake is production-shaped enough to catch real mistakes: it rejects requests whose Authorization header doesn't name the configured access key or doesn't sign the minimum header set. Full signature verification stays in Task 1's vector test.

```go
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
```

- [ ] **Step 2: Write the failing client tests**

```go
package spaces

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cajundata/splatter/internal/spaces/spacestest"
)

func testClient(t *testing.T) (*Client, *spacestest.Server) {
	t.Helper()
	srv := spacestest.New(t)
	c := New(Config{Endpoint: srv.URL, Bucket: srv.Bucket,
		AccessKey: srv.AccessKey, SecretKey: "test-secret", Region: "test-1"})
	return c, srv
}

func TestPutHeadGetRoundTrip(t *testing.T) {
	c, srv := testClient(t)
	ctx := context.Background()
	body := []byte("png-bytes")

	exists, err := c.Head(ctx, "blobs/abc")
	if err != nil || exists {
		t.Fatalf("pre-put head: exists=%v err=%v", exists, err)
	}
	if err := c.Put(ctx, "blobs/abc", body, "irrelevant-for-fake"); err != nil {
		t.Fatal(err)
	}
	exists, err = c.Head(ctx, "blobs/abc")
	if err != nil || !exists {
		t.Fatalf("post-put head: exists=%v err=%v", exists, err)
	}
	rc, err := c.Get(ctx, "blobs/abc")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != string(body) {
		t.Fatalf("get: %q", got)
	}
	if stored, _ := srv.Get("blobs/abc"); string(stored) != string(body) {
		t.Fatalf("stored: %q", stored)
	}
}

func TestGetMissingWrapsErrNotFound(t *testing.T) {
	c, _ := testClient(t)
	_, err := c.Get(context.Background(), "blobs/nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Wrong access key → fake returns 403 → Head must surface an error,
// never report absent (403-as-missing would make push re-upload forever).
func TestHeadAuthFailureIsError(t *testing.T) {
	srv := spacestest.New(t)
	c := New(Config{Endpoint: srv.URL, Bucket: srv.Bucket,
		AccessKey: "WRONGKEY", SecretKey: "s", Region: "r"})
	if _, err := c.Head(context.Background(), "blobs/abc"); err == nil {
		t.Fatal("want error on 403")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/spaces/... -run 'TestPutHeadGet|TestGetMissing|TestHeadAuth' -v`
Expected: FAIL — compile error, `Client`/`New`/`ErrNotFound` undefined.

- [ ] **Step 4: Write the client**

```go
package spaces

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound distinguishes a missing remote blob from transport errors.
var ErrNotFound = errors.New("not found")

// Client performs signed HEAD/PUT/GET calls against one bucket using
// path-style addressing: <endpoint>/<bucket>/<key>.
type Client struct {
	cfg  Config
	http *http.Client
	now  func() time.Time
}

func New(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 5 * time.Minute},
		now:  time.Now,
	}
}

func (c *Client) do(ctx context.Context, method, key string, body []byte, payloadSHA256, contentType string) (*http.Response, error) {
	url := strings.TrimSuffix(c.cfg.Endpoint, "/") + "/" + c.cfg.Bucket + "/" + key
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	Sign(req, c.cfg.AccessKey, c.cfg.SecretKey, c.cfg.Region, payloadSHA256, c.now())
	return c.http.Do(req)
}

// Head reports whether key exists. 404 is (false, nil); any other
// non-200 (including 403, which S3 can return for auth problems) is an
// error — treating it as absent would make push re-upload forever.
func (c *Client) Head(ctx context.Context, key string) (bool, error) {
	resp, err := c.do(ctx, http.MethodHead, key, nil, emptyPayloadSHA256, "")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, httpErr("HEAD", key, resp)
	}
}

// Put uploads body under key. payloadSHA256 is body's lowercase hex
// sha256 — for splatter blobs it equals the key's hash segment.
func (c *Client) Put(ctx context.Context, key string, body []byte, payloadSHA256 string) error {
	resp, err := c.do(ctx, http.MethodPut, key, body, payloadSHA256, "image/png")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return httpErr("PUT", key, resp)
	}
	return nil
}

// Get streams key's content. Missing keys wrap ErrNotFound.
func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, err := c.do(ctx, http.MethodGet, key, nil, emptyPayloadSHA256, "")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %w", key, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, httpErr("GET", key, resp)
	}
	return resp.Body, nil
}

// httpErr renders a non-2xx response with a bounded body excerpt.
func httpErr(op, key string, resp *http.Response) error {
	excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(excerpt))
	if msg == "" {
		return fmt.Errorf("%s %s: %s", op, key, resp.Status)
	}
	return fmt.Errorf("%s %s: %s: %s", op, key, resp.Status, msg)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/spaces/... -v`
Expected: PASS (Tasks 1-3 tests all green).

- [ ] **Step 6: Commit**

```bash
git add internal/spaces/client.go internal/spaces/client_test.go internal/spaces/spacestest/fake.go
git commit -m "feat: spaces client with head/put/get and offline fake server"
```

---

### Task 4: Blob inventory (`internal/workspace/blobrefs.go`)

**Files:**
- Create: `internal/workspace/blobrefs.go`
- Test: `internal/workspace/blobrefs_test.go`

**Interfaces:**
- Consumes: existing `projectNames(root, "")`, `maxLineBytes`, `schema.DecodeManifestLine` (all already in package scope).
- Produces:
  - `type BlobRef struct { SHA256 string; Paths []string }` — paths workspace-relative, slash-normalized, sorted; result sorted by SHA256.
  - `BlobRefs(root string) ([]BlobRef, error)` — Tasks 5/6 consume.
  - `projectBlobRefs(root, name string) ([]BlobRef, error)` (unexported) — Task 7's status consumes.

- [ ] **Step 1: Write the failing test**

The test builds a two-project workspace where one sha is referenced from two runs (dedup across manifests) and one manifest line is malformed (hard error — sync must never silently skip evidence).

```go
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/schema"
)

// writeRunFixture writes a valid run: header plus one call whose single
// image has the given id, file bytes untouched on disk (the image file
// itself is NOT written — BlobRefs reads manifests only).
func writeRunFixture(t *testing.T, root, project, run, imageID string, imageSHA string) {
	t.Helper()
	runDir := filepath.Join(root, "projects", project, "runs", run)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(runDir, "manifest.jsonl")
	header := schema.RunHeader{
		V: 1, Type: "run", Run: run, Project: project,
		Brief: "briefs/b_001.md", BriefSHA256: "0000", Iteration: 1,
		Created: time.Now().UTC(), Harness: "test",
	}
	if err := fsio.AppendRecord(manifest, header); err != nil {
		t.Fatal(err)
	}
	usd := 0.1
	call := schema.CallRecord{
		V: 1, Type: "call", Run: run, Call: "c_01", TS: time.Now().UTC(),
		Provider: "gemini", ModelRequested: "m", ModelReturned: "m",
		Profile: "p", Operation: "generate",
		Request:  schema.CallRequest{Prompt: "p", N: 1, Aspect: "square"},
		Response: schema.CallResponse{LatencyMS: 1, HTTPStatus: 200},
		Cost:     schema.Cost{USD: &usd, Source: "reported"},
		Images: []schema.ImageRef{{ID: imageID, File: "images/" + imageID + ".png",
			SHA256: imageSHA, W: 8, H: 8, AspectActual: "square"}},
		Raw: "raw/c_01.json",
	}
	if err := fsio.AppendRecord(manifest, call); err != nil {
		t.Fatal(err)
	}
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestBlobRefsCollectsAndDedupes(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"alpha", "beta"} {
		if _, err := ScaffoldProject(root, p); err != nil {
			t.Fatal(err)
		}
	}
	shared := shaOf("shared")
	only := shaOf("only")
	writeRunFixture(t, root, "alpha", "r_0001", "c_01_0", shared)
	writeRunFixture(t, root, "beta", "r_0001", "c_01_0", shared) // same sha, second path
	writeRunFixture(t, root, "beta", "r_0002", "c_01_0", only)

	refs, err := BlobRefs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("want 2 refs, got %d: %+v", len(refs), refs)
	}
	bySHA := map[string][]string{}
	for _, r := range refs {
		bySHA[r.SHA256] = r.Paths
	}
	wantShared := []string{
		"projects/alpha/runs/r_0001/images/c_01_0.png",
		"projects/beta/runs/r_0001/images/c_01_0.png",
	}
	if got := bySHA[shared]; strings.Join(got, ",") != strings.Join(wantShared, ",") {
		t.Fatalf("shared paths: %v", got)
	}
	if got := bySHA[only]; len(got) != 1 || got[0] != "projects/beta/runs/r_0002/images/c_01_0.png" {
		t.Fatalf("only paths: %v", got)
	}
	// Deterministic order: sorted by sha.
	if !(refs[0].SHA256 < refs[1].SHA256) {
		t.Fatalf("refs not sorted: %s, %s", refs[0].SHA256, refs[1].SHA256)
	}
}

func TestBlobRefsMalformedManifestIsError(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldProject(root, "alpha"); err != nil {
		t.Fatal(err)
	}
	writeRunFixture(t, root, "alpha", "r_0001", "c_01_0", shaOf("x"))
	mp := filepath.Join(root, "projects", "alpha", "runs", "r_0001", "manifest.jsonl")
	f, err := os.OpenFile(mp, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{not json\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := BlobRefs(root); err == nil {
		t.Fatal("want error for malformed manifest line")
	}
}

func TestBlobRefsEmptyWorkspace(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	refs, err := BlobRefs(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("want no refs, got %+v", refs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/workspace/ -run TestBlobRefs -v`
Expected: FAIL — compile error, `BlobRefs` undefined.

- [ ] **Step 3: Write the implementation**

```go
package workspace

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/cajundata/splatter/internal/schema"
)

// BlobRef is one content-addressed image blob and every workspace-
// relative, slash-normalized path that references it. Multiple paths
// occur when several manifests record the same bytes.
type BlobRef struct {
	SHA256 string
	Paths  []string
}

// BlobRefs scans every project's manifests and returns the blob
// inventory sorted by sha (paths sorted within each ref). Malformed
// manifest lines are errors: sync must never silently skip evidence.
func BlobRefs(root string) ([]BlobRef, error) {
	names, err := projectNames(root, "")
	if err != nil {
		return nil, err
	}
	bySHA := map[string]map[string]bool{}
	for _, name := range names {
		if err := collectProjectBlobs(root, name, bySHA); err != nil {
			return nil, err
		}
	}
	return flattenBlobRefs(bySHA), nil
}

// projectBlobRefs is BlobRefs for a single project (status uses it).
func projectBlobRefs(root, name string) ([]BlobRef, error) {
	bySHA := map[string]map[string]bool{}
	if err := collectProjectBlobs(root, name, bySHA); err != nil {
		return nil, err
	}
	return flattenBlobRefs(bySHA), nil
}

func collectProjectBlobs(root, name string, bySHA map[string]map[string]bool) error {
	runDirs, _ := filepath.Glob(filepath.Join(root, "projects", name, "runs", "r_*"))
	for _, rd := range runDirs {
		mp := filepath.Join(rd, "manifest.jsonl")
		f, err := os.Open(mp)
		if os.IsNotExist(err) {
			continue // validate flags this; sync syncs what exists
		}
		if err != nil {
			return err
		}
		relRun, err := filepath.Rel(root, rd)
		if err != nil {
			f.Close()
			return err
		}
		runPrefix := filepath.ToSlash(relRun)
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			decoded, err := schema.DecodeManifestLine(sc.Bytes())
			if err != nil {
				f.Close()
				return fmt.Errorf("%s line %d: %w", mp, lineNo, err)
			}
			rec, ok := decoded.(*schema.CallRecord)
			if !ok {
				continue
			}
			for _, img := range rec.Images {
				p := path.Join(runPrefix, img.File)
				if bySHA[img.SHA256] == nil {
					bySHA[img.SHA256] = map[string]bool{}
				}
				bySHA[img.SHA256][p] = true
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return fmt.Errorf("%s: %w", mp, err)
		}
	}
	return nil
}

func flattenBlobRefs(bySHA map[string]map[string]bool) []BlobRef {
	refs := make([]BlobRef, 0, len(bySHA))
	for sha, paths := range bySHA {
		ref := BlobRef{SHA256: sha}
		for p := range paths {
			ref.Paths = append(ref.Paths, p)
		}
		sort.Strings(ref.Paths)
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].SHA256 < refs[j].SHA256 })
	return refs
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/workspace/ -v`
Expected: PASS (new tests plus all existing workspace tests).

- [ ] **Step 5: Commit**

```bash
git add internal/workspace/blobrefs.go internal/workspace/blobrefs_test.go
git commit -m "feat: manifest blob inventory for spaces sync"
```

---

### Task 5: `splatter push` (`cmd/splatter/push.go`)

**Files:**
- Create: `cmd/splatter/push.go`
- Create: `cmd/splatter/sync_test.go`
- Modify: `cmd/splatter/root.go` (register command)

**Interfaces:**
- Consumes: `spaces.FromEnv`, `spaces.New`, `Client.Head/Put` (Tasks 2-3); `workspace.BlobRefs` (Task 4); existing `emit`, `usageErr`, `usageArgs`, `workspace.FindRoot`; `spacestest` (tests).
- Produces: `syncResult`/`syncError` structs and the `blobKey(sha string) string` helper (`"blobs/" + sha`) in `push.go` — Task 6's pull reuses all three; `setSyncEnv` and `buildSyncWorkspace` test helpers in `sync_test.go` — Task 6's tests reuse them.

- [ ] **Step 1: Write the failing CLI tests**

```go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/spaces/spacestest"
)

func setSyncEnv(t *testing.T, s *spacestest.Server) {
	t.Helper()
	t.Setenv("SPLATTER_S3_ENDPOINT", s.URL)
	t.Setenv("SPLATTER_S3_BUCKET", s.Bucket)
	t.Setenv("SPLATTER_S3_ACCESS_KEY", s.AccessKey)
	t.Setenv("SPLATTER_S3_SECRET_KEY", "test-secret")
	t.Setenv("SPLATTER_S3_REGION", "test-1")
}

const syncBrief = `---
id: b_001
project: demo
concept: test concept
aspect: square
---
Prose.
`

// buildSyncWorkspace scaffolds a workspace with one project, a brief,
// and one run whose manifest references the given images. Each entry
// maps image id to file bytes; nil bytes = referenced in the manifest
// but not written to disk (lives on the "other machine"). The fixture
// passes `splatter validate` whenever every referenced image is present
// with matching bytes.
func buildSyncWorkspace(t *testing.T, images map[string][]byte) (root string, shas map[string]string) {
	t.Helper()
	root = t.TempDir()
	if _, err := runCLI(t, root, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, root, "init", "demo"); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(root, "projects", "demo", "briefs", "b_001.md")
	if err := os.WriteFile(briefPath, []byte(syncBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, "projects", "demo", "runs", "r_0001")
	if err := os.MkdirAll(filepath.Join(runDir, "images"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(runDir, "manifest.jsonl")
	header := schema.RunHeader{
		V: 1, Type: "run", Run: "r_0001", Project: "demo",
		Brief: "briefs/b_001.md", BriefSHA256: schema.BriefSHA256([]byte(syncBrief)),
		Iteration: 1, Created: time.Now().UTC(), Harness: "test",
	}
	if err := fsio.AppendRecord(manifest, header); err != nil {
		t.Fatal(err)
	}
	shas = map[string]string{}
	var refs []schema.ImageRef
	ids := make([]string, 0, len(images))
	for id := range images {
		ids = append(ids, id)
	}
	// map order is random; sort for a stable manifest
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	for _, id := range ids {
		data := images[id]
		if data == nil {
			data = []byte("absent-" + id) // hash of the bytes that exist elsewhere
		} else {
			if err := os.WriteFile(filepath.Join(runDir, "images", id+".png"), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		sum := sha256.Sum256(data)
		shas[id] = hex.EncodeToString(sum[:])
		refs = append(refs, schema.ImageRef{ID: id, File: "images/" + id + ".png",
			SHA256: shas[id], W: 8, H: 8, AspectActual: "square"})
	}
	usd := 0.1
	call := schema.CallRecord{
		V: 1, Type: "call", Run: "r_0001", Call: "c_01", TS: time.Now().UTC(),
		Provider: "gemini", ModelRequested: "m", ModelReturned: "m",
		Profile: "p", Operation: "generate",
		Request:  schema.CallRequest{Prompt: "p", N: len(refs), Aspect: "square"},
		Response: schema.CallResponse{LatencyMS: 1, HTTPStatus: 200},
		Cost:     schema.Cost{USD: &usd, Source: "reported"},
		Images:   refs,
		Raw:      "raw/c_01.json",
	}
	if err := fsio.AppendRecord(manifest, call); err != nil {
		t.Fatal(err)
	}
	return root, shas
}

func decodeSyncResult(t *testing.T, out string) (r struct {
	Uploaded     int `json:"uploaded"`
	Downloaded   int `json:"downloaded"`
	Skipped      int `json:"skipped"`
	MissingLocal int `json:"missing_local"`
	Failed       []struct {
		SHA256  string `json:"sha256"`
		Message string `json:"message"`
	} `json:"failed"`
}) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("stdout not a JSON result: %v\n%q", err, out)
	}
	return r
}

func TestPushUploadsAbsentBlobsOnly(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, shas := buildSyncWorkspace(t, map[string][]byte{
		"c_01_0": []byte("first"),
		"c_01_1": []byte("second"),
	})
	srv.Put("blobs/"+shas["c_01_0"], []byte("first")) // already remote

	out, err := runCLI(t, root, "push", "--json")
	if err != nil {
		t.Fatal(err)
	}
	res := decodeSyncResult(t, out)
	if res.Uploaded != 1 || res.Skipped != 1 || res.MissingLocal != 0 || len(res.Failed) != 0 {
		t.Fatalf("bad result: %+v", res)
	}
	if got, ok := srv.Get("blobs/" + shas["c_01_1"]); !ok || string(got) != "second" {
		t.Fatalf("blob not uploaded correctly: %q ok=%v", got, ok)
	}
}

func TestPushIdempotentRerun(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, _ := buildSyncWorkspace(t, map[string][]byte{"c_01_0": []byte("x")})
	if _, err := runCLI(t, root, "push"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, root, "push", "--json")
	if err != nil {
		t.Fatal(err)
	}
	res := decodeSyncResult(t, out)
	if res.Uploaded != 0 || res.Skipped != 1 {
		t.Fatalf("re-push must upload nothing: %+v", res)
	}
	if srv.Len() != 1 {
		t.Fatalf("blob count: %d", srv.Len())
	}
}

func TestPushMissingLocalReportedNotFailed(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, _ := buildSyncWorkspace(t, map[string][]byte{
		"c_01_0": []byte("here"),
		"c_01_1": nil, // referenced, not on disk
	})
	out, err := runCLI(t, root, "push", "--json")
	if err != nil {
		t.Fatalf("missing-local must not fail push: %v", err)
	}
	res := decodeSyncResult(t, out)
	if res.Uploaded != 1 || res.MissingLocal != 1 || len(res.Failed) != 0 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestPushRefusesCorruptLocalFile(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, shas := buildSyncWorkspace(t, map[string][]byte{"c_01_0": []byte("original")})
	img := filepath.Join(root, "projects", "demo", "runs", "r_0001", "images", "c_01_0.png")
	if err := os.WriteFile(img, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, root, "push", "--json")
	if err == nil {
		t.Fatal("push must exit non-zero when a local file mismatches its manifest sha")
	}
	res := decodeSyncResult(t, out)
	if len(res.Failed) != 1 || res.Failed[0].SHA256 != shas["c_01_0"] {
		t.Fatalf("bad result: %+v", res)
	}
	if srv.Len() != 0 {
		t.Fatal("corrupt bytes must never be uploaded")
	}
}

func TestPushUnconfiguredIsRuntimeError(t *testing.T) {
	root, _ := buildSyncWorkspace(t, map[string][]byte{"c_01_0": []byte("x")})
	t.Setenv("SPLATTER_S3_ENDPOINT", "")
	t.Setenv("SPLATTER_S3_BUCKET", "")
	t.Setenv("SPLATTER_S3_ACCESS_KEY", "")
	t.Setenv("SPLATTER_S3_SECRET_KEY", "")
	_, err := runCLI(t, root, "push")
	if err == nil {
		t.Fatal("want error")
	}
	if code := exitCode(err); code != 1 {
		t.Fatalf("exit code: got %d want 1 (runtime, matching missing-API-key behavior)", code)
	}
	if !strings.Contains(err.Error(), "SPLATTER_S3_ENDPOINT") {
		t.Fatalf("error should name missing vars: %v", err)
	}
}

func TestPushOutsideWorkspaceIsUsageError(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	_, err := runCLI(t, t.TempDir(), "push")
	if err == nil {
		t.Fatal("want error")
	}
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit code: got %d want 2", code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/splatter/ -run TestPush -v`
Expected: FAIL — `unknown command "push" for "splatter"` (or compile error until push.go exists).

- [ ] **Step 3: Write the command**

```go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/spaces"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

// syncError is one blob that could not be synced.
type syncError struct {
	SHA256  string `json:"sha256"`
	Message string `json:"message"`
}

// syncResult is the shared push/pull result object (spec: one struct,
// direction-specific fields zero for the other command).
type syncResult struct {
	Uploaded     int         `json:"uploaded"`
	Downloaded   int         `json:"downloaded"`
	Skipped      int         `json:"skipped"`
	MissingLocal int         `json:"missing_local"`
	Failed       []syncError `json:"failed"`
}

func (r *syncResult) human(verb string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d\n", verb, r.Uploaded+r.Downloaded)
	fmt.Fprintf(&b, "skipped: %d\n", r.Skipped)
	if r.MissingLocal > 0 {
		fmt.Fprintf(&b, "missing locally: %d\n", r.MissingLocal)
	}
	fmt.Fprintf(&b, "failed: %d\n", len(r.Failed))
	return b.String()
}

// blobKey maps an image sha to its flat content-addressed key (spec §7).
func blobKey(sha string) string { return "blobs/" + sha }

// syncSetup is the shared push/pull preamble: workspace root, config,
// blob inventory, client.
func syncSetup() (root string, refs []workspace.BlobRef, client *spaces.Client, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, nil, err
	}
	root, err = workspace.FindRoot(cwd)
	if err != nil {
		if errors.Is(err, workspace.ErrNoWorkspace) {
			return "", nil, nil, usageErr{err}
		}
		return "", nil, nil, err
	}
	cfg, err := spaces.FromEnv()
	if err != nil {
		return "", nil, nil, err // runtime error: matches missing-API-key behavior
	}
	refs, err = workspace.BlobRefs(root)
	if err != nil {
		return "", nil, nil, err
	}
	return root, refs, spaces.New(cfg), nil
}

func newPushCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "push",
		Short: "Upload manifest-referenced image blobs absent from Spaces",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, refs, client, err := syncSetup()
			if err != nil {
				return err
			}
			res := syncResult{Failed: []syncError{}}
			for _, ref := range refs {
				fail := func(err error) {
					res.Failed = append(res.Failed, syncError{SHA256: ref.SHA256, Message: err.Error()})
					fmt.Fprintf(cmd.ErrOrStderr(), "push: %s: %v\n", ref.SHA256[:12], err)
				}
				var local string
				for _, p := range ref.Paths {
					abs := filepath.Join(root, filepath.FromSlash(p))
					if _, err := os.Stat(abs); err == nil {
						local = abs
						break
					}
				}
				if local == "" {
					res.MissingLocal++
					continue
				}
				data, err := os.ReadFile(local)
				if err != nil {
					fail(err)
					continue
				}
				sum := sha256.Sum256(data)
				if got := hex.EncodeToString(sum[:]); got != ref.SHA256 {
					fail(fmt.Errorf("%s: sha256 %s… does not match manifest — refusing to upload", local, got[:12]))
					continue
				}
				exists, err := client.Head(cmd.Context(), blobKey(ref.SHA256))
				if err != nil {
					fail(err)
					continue
				}
				if exists {
					res.Skipped++
					continue
				}
				if err := client.Put(cmd.Context(), blobKey(ref.SHA256), data, ref.SHA256); err != nil {
					fail(err)
					continue
				}
				res.Uploaded++
			}
			if err := emit(cmd, res, res.human("uploaded")); err != nil {
				return err
			}
			if n := len(res.Failed); n > 0 {
				return fmt.Errorf("push: %d blob(s) failed", n)
			}
			return nil
		},
	}
}
```

In `root.go`, add after the sheet command registration:

```go
	root.AddCommand(newPushCmd())
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/splatter/ -run TestPush -v`
Expected: PASS (all six tests).

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go test ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add cmd/splatter/push.go cmd/splatter/sync_test.go cmd/splatter/root.go
git commit -m "feat: splatter push uploads manifest-referenced blobs to spaces"
```

---

### Task 6: `splatter pull` (`cmd/splatter/pull.go`)

**Files:**
- Create: `cmd/splatter/pull.go`
- Modify: `cmd/splatter/sync_test.go` (append tests)
- Modify: `cmd/splatter/root.go` (register command)

**Interfaces:**
- Consumes: `syncResult`, `syncError`, `blobKey`, `syncSetup` (Task 5); `Client.Get` (Task 3); `fsio.ReplaceFile`; test helpers `setSyncEnv`, `buildSyncWorkspace`, `decodeSyncResult` (Task 5).
- Produces: the `pull` command; nothing downstream consumes code from this task.

- [ ] **Step 1: Write the failing CLI tests (append to `sync_test.go`)**

```go
func TestPullMaterializesMissingImages(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, shas := buildSyncWorkspace(t, map[string][]byte{
		"c_01_0": []byte("present"),
		"c_01_1": nil, // referenced, absent locally
	})
	srv.Put("blobs/"+shas["c_01_1"], []byte("absent-c_01_1")) // bytes matching the manifest sha

	out, err := runCLI(t, root, "pull", "--json")
	if err != nil {
		t.Fatal(err)
	}
	res := decodeSyncResult(t, out)
	if res.Downloaded != 1 || res.Skipped != 1 || len(res.Failed) != 0 {
		t.Fatalf("bad result: %+v", res)
	}
	img := filepath.Join(root, "projects", "demo", "runs", "r_0001", "images", "c_01_1.png")
	data, err := os.ReadFile(img)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "absent-c_01_1" {
		t.Fatalf("downloaded bytes: %q", data)
	}
	// validate must pass afterward: the file matches its manifest sha
	if _, err := runCLI(t, root, "validate"); err != nil {
		t.Fatalf("validate after pull: %v", err)
	}
}

func TestPullIdempotentRerun(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, shas := buildSyncWorkspace(t, map[string][]byte{"c_01_0": nil})
	srv.Put("blobs/"+shas["c_01_0"], []byte("absent-c_01_0"))
	if _, err := runCLI(t, root, "pull"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, root, "pull", "--json")
	if err != nil {
		t.Fatal(err)
	}
	res := decodeSyncResult(t, out)
	if res.Downloaded != 0 || res.Skipped != 1 {
		t.Fatalf("re-pull must download nothing: %+v", res)
	}
}

func TestPullRejectsCorruptRemoteBlob(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, shas := buildSyncWorkspace(t, map[string][]byte{"c_01_0": nil})
	srv.Put("blobs/"+shas["c_01_0"], []byte("wrong bytes"))

	out, err := runCLI(t, root, "pull", "--json")
	if err == nil {
		t.Fatal("pull must exit non-zero on hash mismatch")
	}
	res := decodeSyncResult(t, out)
	if len(res.Failed) != 1 || res.Downloaded != 0 {
		t.Fatalf("bad result: %+v", res)
	}
	img := filepath.Join(root, "projects", "demo", "runs", "r_0001", "images", "c_01_0.png")
	if _, err := os.Stat(img); !os.IsNotExist(err) {
		t.Fatal("corrupt bytes must never land at a manifest-referenced path")
	}
}

func TestPullRemoteMissingBlobFails(t *testing.T) {
	srv := spacestest.New(t)
	setSyncEnv(t, srv)
	root, _ := buildSyncWorkspace(t, map[string][]byte{"c_01_0": nil})
	// nothing seeded remotely
	out, err := runCLI(t, root, "pull", "--json")
	if err == nil {
		t.Fatal("want error when a referenced blob is nowhere")
	}
	res := decodeSyncResult(t, out)
	if len(res.Failed) != 1 {
		t.Fatalf("bad result: %+v", res)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/splatter/ -run TestPull -v`
Expected: FAIL — `unknown command "pull" for "splatter"`.

- [ ] **Step 3: Write the command**

Note the run's `images/` directory may not exist after a fresh `git clone` (it is gitignored, and git does not track empty directories), so pull must `MkdirAll` before writing.

```go
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull",
		Short: "Download manifest-referenced image blobs absent locally",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, refs, client, err := syncSetup()
			if err != nil {
				return err
			}
			res := syncResult{Failed: []syncError{}}
			for _, ref := range refs {
				fail := func(err error) {
					res.Failed = append(res.Failed, syncError{SHA256: ref.SHA256, Message: err.Error()})
					fmt.Fprintf(cmd.ErrOrStderr(), "pull: %s: %v\n", ref.SHA256[:12], err)
				}
				var missing []string
				for _, p := range ref.Paths {
					abs := filepath.Join(root, filepath.FromSlash(p))
					if _, err := os.Stat(abs); err == nil {
						res.Skipped++
					} else {
						missing = append(missing, abs)
					}
				}
				if len(missing) == 0 {
					continue
				}
				body, err := client.Get(cmd.Context(), blobKey(ref.SHA256))
				if err != nil {
					fail(err)
					continue
				}
				data, err := io.ReadAll(body)
				body.Close()
				if err != nil {
					fail(err)
					continue
				}
				sum := sha256.Sum256(data)
				if got := hex.EncodeToString(sum[:]); got != ref.SHA256 {
					fail(fmt.Errorf("remote blob corrupt: sha256 %s… does not match key", got[:12]))
					continue
				}
				for _, abs := range missing {
					if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
						fail(err)
						break
					}
					// ReplaceFile writes a temp file and renames: a partial
					// download can never land at a manifest-referenced path.
					if err := fsio.ReplaceFile(abs, data); err != nil {
						fail(err)
						break
					}
					res.Downloaded++
				}
			}
			if err := emit(cmd, res, res.human("downloaded")); err != nil {
				return err
			}
			if n := len(res.Failed); n > 0 {
				return fmt.Errorf("pull: %d blob(s) failed", n)
			}
			return nil
		},
	}
}
```

In `root.go`, add after the push registration:

```go
	root.AddCommand(newPullCmd())
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/splatter/ -run 'TestPush|TestPull' -v`
Expected: PASS (all ten tests).

- [ ] **Step 5: Run the full suite**

Run: `go build ./... && go test ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add cmd/splatter/pull.go cmd/splatter/sync_test.go cmd/splatter/root.go
git commit -m "feat: splatter pull materializes referenced blobs from spaces"
```

---

### Task 7: Honest sync reporting in `splatter status`

**Files:**
- Modify: `internal/workspace/status.go`
- Modify: `cmd/splatter/status.go`
- Test: `internal/workspace/status_test.go` (may exist — extend), `cmd/splatter/sync_test.go` (append one CLI test)

**Interfaces:**
- Consumes: `projectBlobRefs` (Task 4), `spaces.FromEnv` (Task 2).
- Produces: `workspace.ProjectStatus` gains `MissingImages int` with JSON tag `missing_images`; status `sync` field becomes `"configured"`/`"not configured"`.

- [ ] **Step 1: Write the failing tests**

In `internal/workspace` (append to `status_test.go`, or create it with this test if absent — check first: `ls internal/workspace/status_test.go`):

```go
func TestStatusCountsMissingImages(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldProject(root, "alpha"); err != nil {
		t.Fatal(err)
	}
	// one referenced image, not written to disk
	writeRunFixture(t, root, "alpha", "r_0001", "c_01_0", shaOf("gone"))
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].MissingImages != 1 {
		t.Fatalf("bad statuses: %+v", statuses)
	}
}
```

(`writeRunFixture` and `shaOf` come from Task 4's `blobrefs_test.go`, same package.)

In `cmd/splatter/sync_test.go`:

```go
func TestStatusReportsSyncConfiguration(t *testing.T) {
	srv := spacestest.New(t)
	root, _ := buildSyncWorkspace(t, map[string][]byte{"c_01_0": nil})

	// Unconfigured: all vars empty.
	for _, v := range []string{"SPLATTER_S3_ENDPOINT", "SPLATTER_S3_BUCKET",
		"SPLATTER_S3_ACCESS_KEY", "SPLATTER_S3_SECRET_KEY"} {
		t.Setenv(v, "")
	}
	out, err := runCLI(t, root, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Sync     string `json:"sync"`
		Projects []struct {
			Name          string `json:"name"`
			MissingImages int    `json:"missing_images"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Sync != "not configured" {
		t.Fatalf("sync: %q", res.Sync)
	}
	if len(res.Projects) != 1 || res.Projects[0].MissingImages != 1 {
		t.Fatalf("projects: %+v", res.Projects)
	}

	// Configured: vars set.
	setSyncEnv(t, srv)
	out, err = runCLI(t, root, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Sync != "configured" {
		t.Fatalf("sync: %q", res.Sync)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/workspace/ ./cmd/splatter/ -run 'TestStatusCountsMissing|TestStatusReportsSync' -v`
Expected: FAIL — `MissingImages` undefined; CLI sync field stuck at "not configured".

- [ ] **Step 3: Implement**

In `internal/workspace/status.go`, add the field and the count. `ProjectStatus` becomes:

```go
type ProjectStatus struct {
	Name          string `json:"name"`
	Briefs        int    `json:"briefs"`
	Runs          int    `json:"runs"`
	Verdicts      int    `json:"verdicts"`
	Critiques     int    `json:"critiques"`
	MissingImages int    `json:"missing_images"`
}
```

Inside the per-project loop in `Status`, after the critiques count (before the verdicts scan is fine too — keep it adjacent to the other counts):

```go
		refs, err := projectBlobRefs(root, name)
		if err != nil {
			return nil, fmt.Errorf("manifests for %s: %w", name, err)
		}
		for _, ref := range refs {
			for _, p := range ref.Paths {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
					s.MissingImages++
				}
			}
		}
```

In `cmd/splatter/status.go`, replace the stub sync line and extend the human rendering:

```go
			sync := "configured"
			if _, err := spaces.FromEnv(); err != nil {
				sync = "not configured"
			}
			res := statusResult{Root: root, Sync: sync, Projects: projects}
			var b strings.Builder
			fmt.Fprintf(&b, "workspace: %s\nsync: %s\n", res.Root, res.Sync)
			for _, p := range res.Projects {
				fmt.Fprintf(&b, "  %-24s briefs:%d runs:%d verdicts:%d critiques:%d missing:%d\n",
					p.Name, p.Briefs, p.Runs, p.Verdicts, p.Critiques, p.MissingImages)
			}
```

Add `"github.com/cajundata/splatter/internal/spaces"` to the imports and delete the `// Spaces sync arrives in S4; report honestly until then.` comment.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/workspace/ ./cmd/splatter/ -v`
Expected: PASS. If an existing status test asserts the old human line format or `"sync":"not configured"` under set env vars, update that assertion to match the new contract (check `cmd/splatter/cli_test.go` and `docs/windows-checklist.md` reference `"sync":"not configured"` — the checklist is historical record, leave it).

- [ ] **Step 5: Run the full suite and commit**

Run: `go build ./... && go test ./...`
Expected: all green.

```bash
git add internal/workspace/status.go internal/workspace/status_test.go internal/workspace/blobrefs_test.go cmd/splatter/status.go cmd/splatter/sync_test.go
git commit -m "feat: status reports sync configuration and missing images"
```

---

### Task 8: Docs, cross-compile, and final gate

**Files:**
- Modify: `docs/live-check.md` (append S4 section)
- Modify: `docs/windows-checklist.md` (append S4 items)

**Interfaces:**
- Consumes: the finished commands.
- Produces: the operator-facing halves of the S4 exit criterion.

- [ ] **Step 1: Append the S4 section to `docs/live-check.md`**

````markdown
# S4 Live Exit-Criterion Check (operator-run, both machines)

The full M1 exit test: cross-machine handoff. No provider calls — uses an
existing run from the S2/S3 checks (or fan a fresh one on Windows first).

On **Windows** (PowerShell 7), in the workspace:

```shell
$env:SPLATTER_S3_ENDPOINT = "https://<region>.digitaloceanspaces.com"
$env:SPLATTER_S3_BUCKET = "<bucket>"
$env:SPLATTER_S3_ACCESS_KEY = "..."     # from your key store
$env:SPLATTER_S3_SECRET_KEY = "..."

splatter fan --brief projects/demo/briefs/b_001.md --set baseline
splatter push
splatter status                          # sync: configured, missing:0
git add -A; git commit -m "demo run from windows"; git push
```

On **macOS**, in the workspace clone:

```shell
export SPLATTER_S3_ENDPOINT=https://<region>.digitaloceanspaces.com
export SPLATTER_S3_BUCKET=<bucket>
export SPLATTER_S3_ACCESS_KEY=...        # from your key store
export SPLATTER_S3_SECRET_KEY=...

git pull
splatter status                          # missing:<n> before pull
splatter pull
splatter validate
splatter sheet --run <run id> --open
```

Confirm:

- [ ] push exits 0; re-running push reports 0 uploaded (idempotent)
- [ ] bucket contains `blobs/<sha256>` keys, one per distinct image
- [ ] after pull, `splatter status` shows missing:0 and validate exits 0
- [ ] the rebuilt sheet opens with every image rendering from relative paths
- [ ] the pulled manifest.jsonl is byte-identical to the Windows one
      (`git diff` clean; hashes match)

Then clear the env vars in both shells: PS7 `Remove-Item Env:SPLATTER_S3_*`,
zsh `unset SPLATTER_S3_ENDPOINT SPLATTER_S3_BUCKET SPLATTER_S3_ACCESS_KEY SPLATTER_S3_SECRET_KEY`.
````

- [ ] **Step 2: Append S4 items to `docs/windows-checklist.md`**

Match the existing checklist's format (unchecked boxes; the operator checks them during the live run):

```markdown
# S4 Windows Verification Checklist

- [ ] `splatter push` with env vars set — uploads, exit 0, `$LASTEXITCODE` 0
- [ ] `splatter push` again — `uploaded: 0`, all skipped, exit 0
- [ ] `splatter push` with `$env:SPLATTER_S3_SECRET_KEY` removed — error names
      the missing var, exit 1
- [ ] `splatter status --json` — `"sync":"configured"` with vars set,
      `"sync":"not configured"` without
- [ ] `splatter pull` after deleting one local PNG — file restored,
      `splatter validate` exit 0
```

- [ ] **Step 3: Full gate**

Run: `gofmt -l cmd internal` (expected: no output), then `go vet ./...`, then `make test`, then `make build-all`.
Expected: everything clean; both target binaries build.

- [ ] **Step 4: Commit**

```bash
git add docs/live-check.md docs/windows-checklist.md
git commit -m "docs: S4 live-check and windows checklist for spaces sync"
```

---

## Done means

- `go test ./...` green; `make build-all` produces both binaries; `gofmt -l` and `go vet` clean.
- `go.mod` unchanged.
- The S4 sections exist in `docs/live-check.md` and `docs/windows-checklist.md` for the operator-run cross-machine exit test (the exit criterion itself — fan on Windows, push, commit, pull on macOS, identical manifests — is completed by the operator, not by this plan).
