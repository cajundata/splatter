# S2 Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Live image generation with evidence: Provider interface, instrumented transport, Gemini and OpenAI adapters, config loading, and `splatter gen` writing complete manifest call records.

**Architecture:** Package-per-adapter under `internal/provider/` (per-adapter error normalization enforced by package boundaries); one instrumented `Recorder` RoundTripper per call in `internal/transport`; config in `internal/config`; all evidence writing (run IDs, manifest records, image files, redacted sidecars, cost resolution) only in `internal/run`; `cmd/splatter/gen.go` stays thin. Design: `docs/superpowers/specs/2026-07-24-s2-providers-design.md`; product spec §4–§6: `docs/splatter_spec.md`.

**Tech Stack:** Go, `google.golang.org/genai` (Gemini), `github.com/openai/openai-go/v3` (OpenAI), existing internal packages (fsio, schema, workspace).

## Global Constraints

- **No commit may carry a `Co-Authored-By: Claude` trailer or any AI-attribution line.**
- Evidence files are CLI-written only; every manifest write goes through `fsio.AppendRecord`; derived files through `fsio.ReplaceFile`.
- Cost source precedence, locked: `"reported"` → `"table:<pricing_version>"` → `{"usd":null,"source":"unavailable"}`. Untagged estimates prohibited. Only `internal/run`'s resolver constructs manifest `Cost` values.
- Capability checks fail before any wire call. Unsupported operation = error, never a silent downgrade. Aspect maps to nearest provider-native mode; `aspect_actual` records what was actually requested.
- Error normalization is per-adapter, inside the adapter's package. No shared normalizer importing SDK types.
- The transport never stores request headers (no credentials on disk, ever). API keys via env (`GEMINI_API_KEY`, `OPENAI_API_KEY`) injected as constructor parameters.
- Raw sidecars: wire response verbatim except base64 payloads >4KB, each replaced by `{"$blob":"<sha256-of-decoded-bytes>","bytes":N}`.
- Zero `runtime.GOOS` branches; all paths through `path/filepath`; manifest-stored paths relative and slash-normalized.
- Exit codes: 0 success, 1 runtime/provider error, 2 usage error, 3 validation failure. Every command supports `--json`; logs to stderr.
- No live API calls in `go test` — all network tests use `httptest`. The live exit criterion is operator-run.
- Models: `gemini-baseline` → `gemini-2.5-flash-image`; `openai-baseline` → `gpt-image-1`.
- TDD for all behavior; commit after every green cycle.

## File Structure

```
internal/
├── provider/
│   ├── provider.go        # Provider iface, Request/Result/Capabilities,
│   │                      #   Image, Cost, CallMeta, Error (spec §6.1)
│   ├── provider_test.go
│   ├── gemini/
│   │   ├── gemini.go      # adapter + aspect map + error normalizer
│   │   └── gemini_test.go # httptest fixtures
│   └── openai/
│       ├── openai.go
│       └── openai_test.go
├── transport/
│   ├── transport.go       # Recorder RoundTripper
│   └── transport_test.go
├── config/
│   ├── config.go          # providers.yaml + pricing.yaml
│   └── config_test.go
└── run/
    ├── redact.go          # sidecar redaction (pure)
    ├── redact_test.go
    ├── cost.go            # cost resolver (pure)
    ├── cost_test.go
    ├── run.go             # orchestration: IDs, evidence writes
    └── run_test.go
cmd/splatter/gen.go        # thin command
docs/live-check.md         # operator's exit-criterion script
```

Existing files modified: `internal/workspace/{validate,status,workspace}.go` + tests (polish, config stubs), `internal/schema/{records,brief}.go` + tests (polish), `cmd/splatter/{root.go,main_test.go}` (registration, exit-code table test).

---

### Task 1: Substrate polish (S1 final-review backlog)

**Files:**
- Modify: `internal/workspace/validate.go`, `internal/workspace/status.go`, `internal/schema/brief.go`, `internal/schema/records.go`
- Test: `internal/workspace/validate_test.go`, `internal/workspace/status_test.go`, `internal/schema/brief_test.go`, `internal/schema/records_test.go`, create `cmd/splatter/main_test.go`

**Interfaces:**
- Consumes: existing S1 code.
- Produces: no new exported API. Behavior fixes only; later tasks rely on `CallRecord.Validate()` accepting only images with `W,H ≥ 1` and non-empty `AspectActual` on successful calls.

- [ ] **Step 1: Write the failing tests (all five files)**

Append to `internal/workspace/validate_test.go`:

```go
func TestValidateRunIdentityCrossChecks(t *testing.T) {
	// header.Run must match the run directory name
	root := buildFixture(t)
	proj := filepath.Join(root, "projects", "gradient-descent")
	// rename the run dir so header says r_0001 but dir is r_0002
	if err := os.Rename(filepath.Join(proj, "runs", "r_0001"),
		filepath.Join(proj, "runs", "r_0002")); err != nil {
		t.Fatal(err)
	}
	findings, err := Validate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	mustFindingContaining(t, findings, "r_0002")

	// critique filename must match its run field
	root = buildFixture(t)
	proj = filepath.Join(root, "projects", "gradient-descent")
	old := filepath.Join(proj, "critiques", "r_0001.json")
	if err := os.Rename(old, filepath.Join(proj, "critiques", "r_0099.json")); err != nil {
		t.Fatal(err)
	}
	findings, err = Validate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	mustFindingContaining(t, findings, "r_0099")
}
```

Append to `internal/schema/brief_test.go`:

```go
func TestParseBriefClosingFenceAtEOF(t *testing.T) {
	// closing fence with no trailing newline is still closed
	doc := "---\nid: b_001\nproject: p\nconcept: c\naspect: square\n---"
	meta, body, err := ParseBrief([]byte(doc))
	if err != nil {
		t.Fatalf("fence at EOF must parse: %v", err)
	}
	if meta.ID != "b_001" || body != "" {
		t.Fatalf("got meta=%#v body=%q", meta, body)
	}
}

func TestParseBriefMissingProjectAndConcept(t *testing.T) {
	cases := map[string]string{
		"missing project": "---\nid: b_001\nconcept: c\naspect: square\n---\nbody\n",
		"missing concept": "---\nid: b_001\nproject: p\naspect: square\n---\nbody\n",
	}
	for name, doc := range cases {
		if _, _, err := ParseBrief([]byte(doc)); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

func TestParseBriefAllValidAspects(t *testing.T) {
	for _, a := range []string{"square", "portrait_4_5", "portrait_2_3", "landscape_4_3"} {
		doc := "---\nid: b_001\nproject: p\nconcept: c\naspect: " + a + "\n---\nbody\n"
		if _, _, err := ParseBrief([]byte(doc)); err != nil {
			t.Fatalf("aspect %s must be valid: %v", a, err)
		}
	}
}
```

Append to `internal/schema/records_test.go`:

```go
func TestCallRecordImageRefValidation(t *testing.T) {
	c := validCallRecord()
	c.Images[0].SHA256 = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "images[0]") {
		t.Fatalf("want images[0] error, got %v", err)
	}
	c = validCallRecord()
	c.Images[0].W = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "images[0]") {
		t.Fatalf("want images[0] dimension error, got %v", err)
	}
	c = validCallRecord()
	c.Images[0].AspectActual = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "images[0]") {
		t.Fatalf("want images[0] aspect_actual error, got %v", err)
	}
}
```

Append to `internal/workspace/status_test.go`:

```go
func TestStatusIgnoresNonDirRunEntries(t *testing.T) {
	root := buildFixture(t)
	proj := filepath.Join(root, "projects", "gradient-descent")
	// a stray file matching r_* must not count as a run
	if err := os.WriteFile(filepath.Join(proj, "runs", "r_stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].Runs != 1 {
		t.Fatalf("want 1 run, got %d", statuses[0].Runs)
	}
}
```

(add `"os"` and `"path/filepath"` to status_test.go imports)

Create `cmd/splatter/main_test.go`:

```go
package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain", errors.New("boom"), 1},
		{"usage", usageErr{errors.New("bad")}, 2},
		{"validation", validationErr{errors.New("findings")}, 3},
		{"wrapped usage", fmt.Errorf("ctx: %w", usageErr{errors.New("bad")}), 2},
		{"wrapped validation", fmt.Errorf("ctx: %w", validationErr{errors.New("f")}), 3},
	}
	for _, tc := range cases {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, got)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/... ./cmd/... 2>&1 | grep -E 'FAIL|ok'`
Expected: FAIL in schema (fence at EOF, ImageRef dims), workspace (identity cross-checks, status stray file). `TestExitCodeMapping` passes already (it pins existing behavior — that is fine; it exists to catch regressions). `TestParseBriefMissingProjectAndConcept` and `TestParseBriefAllValidAspects` also already pass (coverage-only).

- [ ] **Step 3: Implement the fixes**

`internal/schema/brief.go` — replace the fence-search block in `ParseBrief`:

```go
	rest := norm[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	fenceLen := len("\n---\n")
	if end < 0 {
		// closing fence at EOF without trailing newline
		if bytes.HasSuffix(rest, []byte("\n---")) {
			end = len(rest) - len("\n---")
			fenceLen = len("\n---")
		} else {
			return nil, "", fmt.Errorf("brief front matter not closed")
		}
	}
```

and change the body extraction line to:

```go
	body := string(rest[end+fenceLen:])
```

`internal/schema/records.go` — replace the images loop in `CallRecord.Validate`:

```go
	for i, img := range c.Images {
		if img.ID == "" || img.File == "" || img.SHA256 == "" {
			return fmt.Errorf("invalid images[%d]: id, file, sha256 all required", i)
		}
		if img.W < 1 || img.H < 1 {
			return fmt.Errorf("invalid images[%d]: w and h must be >= 1", i)
		}
		if img.AspectActual == "" {
			return fmt.Errorf("invalid images[%d]: aspect_actual required", i)
		}
	}
```

`internal/workspace/validate.go`:

1. Add package-level constant and use it at both scanner sites (replacing the two `1024*1024` literals):

```go
// maxLineBytes bounds a single JSONL line in evidence files.
const maxLineBytes = 1024 * 1024
```

2. Wrap both `sc.Err()` returns with path context:

```go
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("%s: %w", mp, err)
		}
```

(and `vp` for the verdicts scanner)

3. In the run-directory loop, derive the directory's run ID and cross-check the header (inside the `if header != nil` branch, before `checkBriefHash`):

```go
		if header != nil {
			if dirRun := filepath.Base(rd); header.Run != dirRun {
				add(mp, fmt.Sprintf("run header %s does not match directory %s", header.Run, dirRun))
			}
			...
		}
```

4. In the critiques loop, after `crit.Validate()` succeeds, cross-check the filename:

```go
		wantName := crit.Run + ".json"
		if got := filepath.Base(cp); got != wantName {
			add(cp, fmt.Sprintf("critique filename %s does not match run %s", got, crit.Run))
		}
```

Place this before the known-run lookup so a renamed critique yields the filename finding even when the run exists.

`internal/workspace/status.go` — filter runs by IsDir, check scanner error, drop the redundant sort, align the scanner buffer:

```go
		runs, _ := filepath.Glob(filepath.Join(proj, "runs", "r_*"))
		for _, r := range runs {
			if fi, err := os.Stat(r); err == nil && fi.IsDir() {
				s.Runs++
			}
		}
```

```go
		if f, err := os.Open(filepath.Join(proj, "verdicts.jsonl")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
			for sc.Scan() {
				s.Verdicts++
			}
			serr := sc.Err()
			f.Close()
			if serr != nil {
				return nil, fmt.Errorf("verdicts.jsonl for %s: %w", name, serr)
			}
		}
```

Delete the trailing `sort.Slice(...)` line (projectNames already returns sorted names) and remove the now-unused `"sort"` import. Add `"fmt"` to imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 5: Commit**

```bash
git add internal/ cmd/
git commit -m "fix: substrate polish from S1 final review"
```

---

### Task 2: Provider interface and shared types

**Files:**
- Create: `internal/provider/provider.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces (spec §6.1 verbatim plus the normalized error; every later task uses these):

```go
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
```

Note `Result.AspectActual`: spec §6.1's Result doesn't list it, but spec §4.2 requires `aspect_actual` per image in the manifest and §6.1 requires recording "what the provider was actually asked for" — the adapter is the only component that knows the native mode, so it reports it here. Record this as a deliberate, spec-driven addition.

- [ ] **Step 1: Write the failing test**

`internal/provider/provider_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -v`
Expected: FAIL — package does not exist / `Error` undefined.

- [ ] **Step 3: Write provider.go**

The complete file from the Interfaces block above, verbatim.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/provider/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/
git commit -m "feat: provider interface and shared types"
```

---

### Task 3: Instrumented transport

**Files:**
- Create: `internal/transport/transport.go`
- Test: `internal/transport/transport_test.go`

**Interfaces:**
- Consumes: nothing new (net/http only).
- Produces:

```go
// NewRecorder returns a Recorder and an *http.Client using it. requestIDHeader
// names the provider's request-id response header ("" = don't capture).
func NewRecorder(requestIDHeader string) (*Recorder, *http.Client)

type Recorder struct { /* unexported fields */ }

func (r *Recorder) Latency() time.Duration // wall-clock around the last RoundTrip
func (r *Recorder) Body() []byte           // captured response body of the last exchange
func (r *Recorder) HTTPStatus() int
func (r *Recorder) RequestID() string
```

One Recorder per Generate call. If the SDK makes multiple HTTP exchanges through one client (retries), the recorder keeps the LAST exchange — the one that produced the returned response.

- [ ] **Step 1: Write the failing tests**

`internal/transport/transport_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/transport/ -v`
Expected: FAIL — `NewRecorder` undefined.

- [ ] **Step 3: Write transport.go**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/transport/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/transport/
git commit -m "feat: instrumented transport recorder"
```

---

### Task 4: Config loading and workspace stubs

**Files:**
- Create: `internal/config/config.go`
- Modify: `internal/workspace/workspace.go` (providersStub, pricingStub constants)
- Test: `internal/config/config_test.go`, `internal/workspace/workspace_test.go` (stub assertions)

**Interfaces:**
- Consumes: `gopkg.in/yaml.v3`.
- Produces:

```go
package config

type Profile struct {
	Provider string         `yaml:"provider"`
	Model    string         `yaml:"model"`
	Native   map[string]any `yaml:"native"`
}

type Providers struct {
	Version     int                  `yaml:"version"`
	Profiles    map[string]Profile   `yaml:"profiles"`
	ProfileSets map[string][]string  `yaml:"profile_sets"`
}

type ModelPrice struct {
	USDPerImage float64 `yaml:"usd_per_image"`
}

type Pricing struct {
	Version string                `yaml:"version"`
	Models  map[string]ModelPrice `yaml:"models"`
}

func LoadProviders(path string) (*Providers, error) // parse + validate
func LoadPricing(path string) (*Pricing, error)     // parse + validate

// Resolve returns the named profile or an error listing available IDs.
func (p *Providers) Resolve(profileID string) (Profile, error)
```

- Validation: `version` must be 1 (providers) / non-empty string (pricing); every profile needs non-empty `provider` (must be `"gemini"` or `"openai"`) and `model`; every profile-set member must reference an existing profile. Errors carry file path and field context.

- [ ] **Step 1: Write the failing tests**

`internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodProviders = `version: 1
profiles:
  gemini-baseline:
    provider: gemini
    model: gemini-2.5-flash-image
    native: { }
  openai-baseline:
    provider: openai
    model: gpt-image-1
    native: { quality: high }
profile_sets:
  baseline: [gemini-baseline, openai-baseline]
`

func TestLoadProviders(t *testing.T) {
	p, err := LoadProviders(writeFile(t, "providers.yaml", goodProviders))
	if err != nil {
		t.Fatal(err)
	}
	prof, err := p.Resolve("openai-baseline")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Provider != "openai" || prof.Model != "gpt-image-1" || prof.Native["quality"] != "high" {
		t.Fatalf("bad profile: %#v", prof)
	}
	if len(p.ProfileSets["baseline"]) != 2 {
		t.Fatalf("bad sets: %#v", p.ProfileSets)
	}
}

func TestLoadProvidersRejectsBadConfig(t *testing.T) {
	cases := map[string]struct{ content, wantSubstr string }{
		"bad version":     {"version: 2\nprofiles: { }\n", "version"},
		"unknown provider": {"version: 1\nprofiles:\n  x:\n    provider: dalle\n    model: m\n", "dalle"},
		"missing model":   {"version: 1\nprofiles:\n  x:\n    provider: gemini\n", "model"},
		"dangling set":    {"version: 1\nprofiles: { }\nprofile_sets:\n  s: [nope]\n", "nope"},
		"malformed yaml":  {"version: [1\n", "yaml"},
	}
	for name, tc := range cases {
		_, err := LoadProviders(writeFile(t, "providers.yaml", tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
			t.Fatalf("%s: want error containing %q, got %v", name, tc.wantSubstr, err)
		}
	}
}

func TestResolveUnknownProfileListsAvailable(t *testing.T) {
	p, err := LoadProviders(writeFile(t, "providers.yaml", goodProviders))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), "gemini-baseline") {
		t.Fatalf("error must list available profiles, got %v", err)
	}
}

func TestLoadPricing(t *testing.T) {
	good := "version: \"2026-07-25.0\"\nmodels:\n  gpt-image-1:\n    usd_per_image: 0.04\n"
	pr, err := LoadPricing(writeFile(t, "pricing.yaml", good))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Version != "2026-07-25.0" || pr.Models["gpt-image-1"].USDPerImage != 0.04 {
		t.Fatalf("bad pricing: %#v", pr)
	}
	if _, err := LoadPricing(writeFile(t, "pricing.yaml", "models: { }\n")); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatalf("missing version must error, got %v", err)
	}
}
```

Append to `internal/workspace/workspace_test.go`:

```go
func TestScaffoldStubsContainBaselines(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	py, _ := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	for _, want := range []string{"gemini-baseline", "gemini-2.5-flash-image", "openai-baseline", "gpt-image-1", "baseline:"} {
		if !strings.Contains(string(py), want) {
			t.Fatalf("providers.yaml stub missing %q:\n%s", want, py)
		}
	}
	pr, _ := os.ReadFile(filepath.Join(dir, "pricing.yaml"))
	for _, want := range []string{"gemini-2.5-flash-image", "gpt-image-1", "usd_per_image"} {
		if !strings.Contains(string(pr), want) {
			t.Fatalf("pricing.yaml stub missing %q:\n%s", want, pr)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ ./internal/workspace/ -run 'TestLoad|TestResolve|TestScaffoldStubs' -v`
Expected: FAIL — config package missing; stub assertions fail.

- [ ] **Step 3: Write config.go and update the stubs**

`internal/config/config.go`:

```go
// Package config loads and validates providers.yaml and pricing.yaml.
// Profiles are evidence: both files are git-tracked in the workspace.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Profile struct {
	Provider string         `yaml:"provider"`
	Model    string         `yaml:"model"`
	Native   map[string]any `yaml:"native"`
}

type Providers struct {
	Version     int                 `yaml:"version"`
	Profiles    map[string]Profile  `yaml:"profiles"`
	ProfileSets map[string][]string `yaml:"profile_sets"`
}

type ModelPrice struct {
	USDPerImage float64 `yaml:"usd_per_image"`
}

type Pricing struct {
	Version string                `yaml:"version"`
	Models  map[string]ModelPrice `yaml:"models"`
}

var knownProviders = map[string]bool{"gemini": true, "openai": true}

func LoadProviders(path string) (*Providers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Providers
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: yaml: %w", path, err)
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("%s: version: want 1, got %d", path, p.Version)
	}
	for id, prof := range p.Profiles {
		if !knownProviders[prof.Provider] {
			return nil, fmt.Errorf("%s: profile %s: unknown provider %q", path, id, prof.Provider)
		}
		if prof.Model == "" {
			return nil, fmt.Errorf("%s: profile %s: model required", path, id)
		}
	}
	for set, members := range p.ProfileSets {
		for _, m := range members {
			if _, ok := p.Profiles[m]; !ok {
				return nil, fmt.Errorf("%s: profile_set %s: unknown profile %q", path, set, m)
			}
		}
	}
	return &p, nil
}

func LoadPricing(path string) (*Pricing, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Pricing
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: yaml: %w", path, err)
	}
	if p.Version == "" {
		return nil, fmt.Errorf("%s: version required (bumped on every edit)", path)
	}
	return &p, nil
}

func (p *Providers) Resolve(profileID string) (Profile, error) {
	prof, ok := p.Profiles[profileID]
	if !ok {
		ids := make([]string, 0, len(p.Profiles))
		for id := range p.Profiles {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return Profile{}, fmt.Errorf("unknown profile %q (available: %s)", profileID, strings.Join(ids, ", "))
	}
	return prof, nil
}
```

`internal/workspace/workspace.go` — replace the two stub constants:

```go
const providersStub = `version: 1
profiles:
  gemini-baseline:
    provider: gemini
    model: gemini-2.5-flash-image
    native: { }
  openai-baseline:
    provider: openai
    model: gpt-image-1
    native: { }
profile_sets:
  baseline: [gemini-baseline, openai-baseline]
`

const pricingStub = `version: "2026-07-25.0"
# Per-image prices used when the API response reports no cost.
# Bump version on every edit; it lands in cost.source as table:<version>.
models:
  gemini-2.5-flash-image:
    usd_per_image: 0.039
  gpt-image-1:
    usd_per_image: 0.042
`
```

(The two prices are current published per-image figures for 1024×1024/medium output; the operator maintains them — the table version string is what makes any drift auditable.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/ internal/workspace/
git commit -m "feat: providers and pricing config loading; baseline stubs"
```

---

### Task 5: Gemini adapter

**Files:**
- Create: `internal/provider/gemini/gemini.go`
- Test: `internal/provider/gemini/gemini_test.go`
- Modify: `go.mod` (adds `google.golang.org/genai`)

**Interfaces:**
- Consumes: `provider` types (Task 2), `transport.NewRecorder` (Task 3), `google.golang.org/genai`.
- Produces: `gemini.New(apiKey, baseURL string) *Adapter` implementing `provider.Provider`. `baseURL` empty in production; tests pass an httptest URL.
- Reference material: `~/projects/starshp_app/internal/provider/gemini.go` for image-mode configuration patterns ONLY (responseModalities TEXT+IMAGE, no tools with image output, InlineData parsing, injectable base URL). Do NOT import Starshp code; do NOT copy its streaming shape — splatter is synchronous (`Models.GenerateContent`, not `GenerateContentStream`).
- Native-key policy (both adapters): each adapter has an explicit allowlist of supported `native` keys; an unknown key is a `config`-stage error, never silently ignored (spec's no-silent-downgrade rule applied to config). Gemini's allowlist is empty in v1.

- [ ] **Step 1: Get the dependency**

```bash
go get google.golang.org/genai@latest
```

- [ ] **Step 2: Write the failing tests**

`internal/provider/gemini/gemini_test.go`:

```go
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
	_, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "decode" {
		t.Fatalf("want decode-stage error, got %v", err)
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/provider/gemini/ -v`
Expected: FAIL — `New` undefined (compile error).

- [ ] **Step 4: Write gemini.go**

```go
// Package gemini adapts the Gemini API (google.golang.org/genai) to
// splatter's Provider interface. Image mode per the Starshp reference:
// responseModalities TEXT+IMAGE, no tools (the API rejects tools alongside
// image output), images arrive as InlineData parts. Synchronous only.
package gemini

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png"
	"strings"

	"google.golang.org/genai"

	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/transport"
)

type Adapter struct {
	apiKey  string
	baseURL string // empty = production endpoint; tests inject httptest URL
}

func New(apiKey, baseURL string) *Adapter {
	return &Adapter{apiKey: apiKey, baseURL: baseURL}
}

func (a *Adapter) Name() string { return "gemini" }

// aspectMap: harness vocabulary -> nearest genai-native aspectRatio.
// The API supports 1:1, 2:3, 3:2, 3:4, 4:3, 9:16, 16:9, 21:9 — no 4:5,
// so portrait_4_5 maps to 3:4 (nearest) and aspect_actual records that.
var aspectMap = map[string]string{
	"square":        "1:1",
	"portrait_4_5":  "3:4",
	"portrait_2_3":  "2:3",
	"landscape_4_3": "4:3",
}

func (a *Adapter) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		// One image per call for this model; -n above 1 fails the
		// capability check rather than silently looping.
		MaxBatch:    1,
		AspectModes: []string{"1:1", "3:4", "2:3", "4:3"},
	}
}

func configErr(format string, args ...any) *provider.Error {
	return &provider.Error{Stage: "config", Message: fmt.Sprintf(format, args...)}
}

func (a *Adapter) Generate(ctx context.Context, req provider.Request) (provider.Result, error) {
	var zero provider.Result
	if a.apiKey == "" {
		return zero, configErr("missing GEMINI_API_KEY")
	}
	native, ok := aspectMap[req.Aspect]
	if !ok {
		return zero, configErr("unsupported aspect %q", req.Aspect)
	}
	if req.N > 1 {
		return zero, configErr("n=%d exceeds max batch 1", req.N)
	}
	if req.Seed != nil {
		return zero, configErr("seed not supported")
	}
	// v1 allowlist is empty: any native key is unsupported, never ignored.
	for k := range req.Native {
		return zero, configErr("unsupported native key %q", k)
	}

	rec, client := transport.NewRecorder("")
	cc := &genai.ClientConfig{APIKey: a.apiKey, Backend: genai.BackendGeminiAPI, HTTPClient: client}
	if a.baseURL != "" {
		cc.HTTPOptions.BaseURL = a.baseURL
	}
	cl, err := genai.NewClient(ctx, cc)
	if err != nil {
		return zero, configErr("client: %v", err)
	}

	cfg := &genai.GenerateContentConfig{
		ResponseModalities: []string{"TEXT", "IMAGE"},
		ImageConfig:        &genai.ImageConfig{AspectRatio: native},
	}
	contents := []*genai.Content{genai.NewContentFromText(req.Prompt, genai.RoleUser)}
	resp, err := cl.Models.GenerateContent(ctx, req.Model, contents, cfg)
	if err != nil {
		// Partial result: the failed exchange's evidence survives into the
		// manifest's failed-call record.
		partial := provider.Result{Latency: rec.Latency(), Raw: rec.Body(),
			Meta: provider.CallMeta{HTTPStatus: rec.HTTPStatus()}}
		return partial, normalize(err, rec.HTTPStatus())
	}

	var images []provider.Image
	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.InlineData == nil || !strings.HasPrefix(part.InlineData.MIMEType, "image/") {
				continue
			}
			data := part.InlineData.Data
			cfgImg, _, derr := image.DecodeConfig(bytes.NewReader(data))
			if derr != nil {
				return zero, &provider.Error{Stage: "decode",
					Message: fmt.Sprintf("undecodable %s payload: %v", part.InlineData.MIMEType, derr)}
			}
			images = append(images, provider.Image{Bytes: data, W: cfgImg.Width, H: cfgImg.Height})
		}
	}
	if len(images) == 0 {
		return zero, &provider.Error{Stage: "decode", Message: "response contained no image parts"}
	}

	return provider.Result{
		Images:  images,
		Latency: rec.Latency(),
		// Gemini reports no dollar figure; leave Cost zero for the
		// resolver's table/unavailable precedence.
		Meta: provider.CallMeta{
			ModelReturned: resp.ModelVersion,
			HTTPStatus:    rec.HTTPStatus(),
		},
		Raw:          rec.Body(),
		AspectActual: native,
	}, nil
}

// normalize maps a genai SDK error to the per-adapter normalized form.
func normalize(err error, status int) *provider.Error {
	var apiErr genai.APIError
	if ok := errorsAs(err, &apiErr); ok {
		return &provider.Error{Stage: "request", HTTPStatus: apiErr.Code, Message: apiErr.Message}
	}
	return &provider.Error{Stage: "request", HTTPStatus: status, Message: err.Error()}
}
```

Add this small helper at the bottom of the file (keeps `errors` usage local and testable):

```go
func errorsAs[T error](err error, target *T) bool {
	return errors.As(err, target)
}
```

(add `"errors"` to imports.) Verify with `go doc google.golang.org/genai.APIError` that the type is `APIError` with `Code`/`Message` fields; if the SDK instead exposes `*APIError`, adjust the `errors.As` target mechanically and note it in your report.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/provider/gemini/ -v`
Expected: PASS (5 tests). If `TestGenerateAPIErrorNormalized` fails on the message assertion, print the actual normalized error: the genai SDK may wrap the JSON error body differently — keep Stage/HTTPStatus assertions strict and adjust only the message-substring extraction in `normalize`, not the test.

- [ ] **Step 6: Run the full suite and commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/provider/gemini/ go.mod go.sum
git commit -m "feat: gemini adapter with normalized errors"
```

---

### Task 6: OpenAI adapter

**Files:**
- Create: `internal/provider/openai/openai.go`
- Test: `internal/provider/openai/openai_test.go`
- Modify: `go.mod` (adds `github.com/openai/openai-go/v3`)

**Interfaces:**
- Consumes: `provider` types, `transport.NewRecorder`, `github.com/openai/openai-go/v3` (+ its `option` package).
- Produces: `openai.New(apiKey, baseURL string) *Adapter` implementing `provider.Provider`.
- Native-key allowlist: `quality` (string: passed through as `ImageGenerateParamsQuality`). Anything else = config error.
- SDK retries are disabled (`option.WithMaxRetries(0)`): one call = one HTTP exchange = one evidence record; retrying is the operator's decision, not the adapter's.

- [ ] **Step 1: Get the dependency**

```bash
go get github.com/openai/openai-go/v3@latest
```

- [ ] **Step 2: Write the failing tests**

`internal/provider/openai/openai_test.go`:

```go
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
	_, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "decode" {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestCapabilities(t *testing.T) {
	c := New("k", "").Capabilities()
	if c.MaxBatch != 10 || c.Seed || c.Img2Img || c.Edit {
		t.Fatalf("capabilities: %#v", c)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/provider/openai/ -v`
Expected: FAIL — `New` undefined.

- [ ] **Step 4: Write openai.go**

First confirm the SDK's exact names (adjust the code below mechanically if a constant differs, and note it in your report):

```bash
go doc github.com/openai/openai-go/v3.ImageGenerateParams | head -30
go doc github.com/openai/openai-go/v3.Error | head -20
go doc github.com/openai/openai-go/v3.Int
```

```go
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
	if len(resp.Data) == 0 {
		return zero, &provider.Error{Stage: "decode", Message: "response contained no images"}
	}
	images := make([]provider.Image, 0, len(resp.Data))
	for i, d := range resp.Data {
		raw, derr := base64.StdEncoding.DecodeString(d.B64JSON)
		if derr != nil {
			return zero, &provider.Error{Stage: "decode", Message: fmt.Sprintf("data[%d]: bad base64: %v", i, derr)}
		}
		cfgImg, _, derr := image.DecodeConfig(bytes.NewReader(raw))
		if derr != nil {
			return zero, &provider.Error{Stage: "decode", Message: fmt.Sprintf("data[%d]: undecodable image: %v", i, derr)}
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
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/provider/openai/ -v`
Expected: PASS (6 tests). Same rule as the Gemini task: if the API-error message assertion fails, fix message extraction in `normalize`, keeping the test's Stage/HTTPStatus assertions strict.

- [ ] **Step 6: Run the full suite and commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/provider/openai/ go.mod go.sum
git commit -m "feat: openai adapter with normalized errors"
```

---

### Task 7: Sidecar redaction and cost resolver

**Files:**
- Create: `internal/run/redact.go`, `internal/run/cost.go`
- Test: `internal/run/redact_test.go`, `internal/run/cost_test.go`

**Interfaces:**
- Consumes: `provider.Cost`, `config.Pricing`, `schema.Cost`.
- Produces (used by Task 8):

```go
// RedactRaw returns the wire body with every base64 string payload that
// decodes to more than 4KB replaced by {"$blob":"<sha256>","bytes":N}.
// Non-JSON input is returned verbatim.
func RedactRaw(raw []byte) []byte

// ResolveCost implements the locked precedence: reported → table → unavailable.
// nImages is the number of images actually produced (0 for failed calls —
// a failed call resolves to unavailable unless the adapter reported dollars).
func ResolveCost(reported provider.Cost, modelReturned, modelRequested string, nImages int, pricing *config.Pricing) schema.Cost
```

- [ ] **Step 1: Write the failing tests**

`internal/run/redact_test.go`:

```go
package run

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactRawReplacesLargePayloads(t *testing.T) {
	big := make([]byte, 5000)
	for i := range big {
		big[i] = byte(i % 251)
	}
	sum := sha256.Sum256(big)
	in := map[string]any{
		"note": "small string stays",
		"nested": map[string]any{
			"data": base64.StdEncoding.EncodeToString(big),
		},
		"list": []any{base64.StdEncoding.EncodeToString(big)},
	}
	raw, _ := json.Marshal(in)
	out := RedactRaw(raw)

	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("redacted output must stay valid JSON: %v", err)
	}
	if parsed["note"] != "small string stays" {
		t.Fatal("small strings must survive")
	}
	blob := parsed["nested"].(map[string]any)["data"].(map[string]any)
	if blob["$blob"] != hex.EncodeToString(sum[:]) || blob["bytes"].(float64) != 5000 {
		t.Fatalf("bad blob replacement: %#v", blob)
	}
	if _, ok := parsed["list"].([]any)[0].(map[string]any); !ok {
		t.Fatal("payloads inside arrays must be replaced too")
	}
	if strings.Contains(string(out), base64.StdEncoding.EncodeToString(big[:100])) {
		t.Fatal("payload bytes leaked into redacted output")
	}
}

func TestRedactRawLeavesSmallAndNonBase64(t *testing.T) {
	small := base64.StdEncoding.EncodeToString(make([]byte, 100)) // valid but tiny
	raw, _ := json.Marshal(map[string]any{"a": small, "b": "definitely not base64!!"})
	var parsed map[string]any
	if err := json.Unmarshal(RedactRaw(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["a"] != small || parsed["b"] != "definitely not base64!!" {
		t.Fatalf("small/non-base64 strings must be untouched: %#v", parsed)
	}
}

func TestRedactRawNonJSONVerbatim(t *testing.T) {
	in := []byte("plain text, not json")
	if string(RedactRaw(in)) != string(in) {
		t.Fatal("non-JSON must pass through verbatim")
	}
}
```

`internal/run/cost_test.go`:

```go
package run

import (
	"testing"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
)

func testPricing() *config.Pricing {
	return &config.Pricing{
		Version: "2026-07-25.0",
		Models:  map[string]config.ModelPrice{"gpt-image-1": {USDPerImage: 0.04}},
	}
}

func TestResolveCostReportedWins(t *testing.T) {
	usd := 0.27
	got := ResolveCost(provider.Cost{USD: &usd, Source: "reported"}, "gpt-image-1", "gpt-image-1", 2, testPricing())
	if got.Source != "reported" || got.USD == nil || *got.USD != 0.27 {
		t.Fatalf("got %#v", got)
	}
}

func TestResolveCostTable(t *testing.T) {
	got := ResolveCost(provider.Cost{}, "gpt-image-1", "gpt-image-1", 2, testPricing())
	if got.Source != "table:2026-07-25.0" || got.USD == nil || *got.USD != 0.08 {
		t.Fatalf("got %#v", got)
	}
	// model_returned unknown, falls back to model_requested
	got = ResolveCost(provider.Cost{}, "gpt-image-1-2027", "gpt-image-1", 1, testPricing())
	if got.Source != "table:2026-07-25.0" || *got.USD != 0.04 {
		t.Fatalf("fallback: %#v", got)
	}
}

func TestResolveCostUnavailable(t *testing.T) {
	got := ResolveCost(provider.Cost{}, "mystery", "mystery", 1, testPricing())
	if got.Source != "unavailable" || got.USD != nil {
		t.Fatalf("got %#v", got)
	}
	// failed call: zero images, nothing reported
	got = ResolveCost(provider.Cost{}, "gpt-image-1", "gpt-image-1", 0, testPricing())
	if got.Source != "unavailable" || got.USD != nil {
		t.Fatalf("failed call must be unavailable, got %#v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/run/ -v`
Expected: FAIL — package missing.

- [ ] **Step 3: Write redact.go and cost.go**

`internal/run/redact.go`:

```go
// Package run orchestrates provider calls and owns all evidence writing:
// run IDs, manifest records, image files, redacted raw sidecars, and cost
// resolution. Adapters never touch the filesystem.
package run

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

// blobThreshold: base64 payloads decoding beyond this are replaced in raw
// sidecars, keeping evidence auditable without megabytes of image data.
const blobThreshold = 4096

// RedactRaw replaces large base64 payloads with {"$blob":sha256,"bytes":N}.
// Non-JSON input is returned verbatim (fallback preserves evidence).
func RedactRaw(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return raw
	}
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = redactValue(e)
		}
		return t
	case []any:
		for i, e := range t {
			t[i] = redactValue(e)
		}
		return t
	case string:
		// quick reject: decoded size can't exceed threshold
		if len(t) < blobThreshold*4/3 {
			return t
		}
		decoded, err := base64.StdEncoding.DecodeString(t)
		if err != nil || len(decoded) <= blobThreshold {
			return t
		}
		sum := sha256.Sum256(decoded)
		return map[string]any{"$blob": hex.EncodeToString(sum[:]), "bytes": len(decoded)}
	default:
		return v
	}
}
```

`internal/run/cost.go`:

```go
package run

import (
	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
)

// ResolveCost is the only constructor of manifest Cost values. Locked
// precedence: "reported" → "table:<pricing_version>" → unavailable.
// A failed call (nImages 0) is unavailable unless the adapter reported
// dollars — a table price for zero delivered images would assert knowledge
// the evidence doesn't contain.
func ResolveCost(reported provider.Cost, modelReturned, modelRequested string, nImages int, pricing *config.Pricing) schema.Cost {
	if reported.Source == "reported" && reported.USD != nil {
		return schema.Cost{USD: reported.USD, Source: "reported"}
	}
	if nImages > 0 && pricing != nil {
		model := modelReturned
		price, ok := pricing.Models[model]
		if !ok {
			model = modelRequested
			price, ok = pricing.Models[model]
		}
		if ok {
			usd := price.USDPerImage * float64(nImages)
			return schema.Cost{USD: &usd, Source: "table:" + pricing.Version}
		}
	}
	return schema.Cost{USD: nil, Source: "unavailable"}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/run/ -v`
Expected: PASS (6 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/run/
git commit -m "feat: raw sidecar redaction and cost resolver"
```

---

### Task 8: Gen orchestration

**Files:**
- Create: `internal/run/run.go`
- Test: `internal/run/run_test.go`

**Interfaces:**
- Consumes: `fsio.AppendRecord`, `schema.*` (ParseBrief, BriefSHA256, RunHeader, CallRecord), `provider.Provider`, `config.{Profile,Pricing}`, `RedactRaw`, `ResolveCost` (Task 7).
- Produces (used by Task 9's command):

```go
type GenParams struct {
	Root      string // workspace root
	BriefPath string // as given on the CLI (absolute or cwd-relative)
	ProfileID string
	Profile   config.Profile
	N         int
	Harness   string // e.g. "splatter v0.2.0"
	Pricing   *config.Pricing
	Provider  provider.Provider
}

type GenResult struct {
	Project string            `json:"project"`
	Run     string            `json:"run"`
	Call    string            `json:"call"`
	Images  []schema.ImageRef `json:"images"`
	Cost    schema.Cost       `json:"cost"`
	LatencyMS int64           `json:"latency_ms"`
	Failed  bool              `json:"failed"`
	Error   *schema.CallError `json:"error,omitempty"`
}

// Gen runs one generation call end to end. A provider failure is NOT an
// error return: it produces a failed-call record and GenResult.Failed=true.
// The error return is for everything that prevents evidence from being
// written at all (bad brief, capability violation, unscaffolded project, IO).
func Gen(ctx context.Context, p GenParams) (*GenResult, error)
```

- Semantics: project comes from the brief's front matter; `projects/<project>/` must already be scaffolded (error names `splatter init <project>` if not). Capability checks (n ≥ 1, n ≤ MaxBatch, aspect mapped by the adapter — checked by attempting the call only after N/seed gates) happen before the run directory is created: a capability violation leaves no trace on disk. Run IDs allocate sequentially (`r_0001`…) by scanning existing `r_*` dirs for the max numeric suffix. Evidence files are created with `O_EXCL` (never overwrite). All manifest-stored paths slash-relative to the run dir.

- [ ] **Step 1: Write the failing tests**

`internal/run/run_test.go`:

```go
package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
)

const genBrief = `---
id: b_001
project: gradient-descent
concept: one-line concept statement
aspect: square
---
Creative direction prose.
`

// fakeProvider implements provider.Provider for orchestration tests.
type fakeProvider struct {
	caps   provider.Capabilities
	result provider.Result
	err    error
	gotReq provider.Request
}

func (f *fakeProvider) Name() string                       { return "fake" }
func (f *fakeProvider) Capabilities() provider.Capabilities { return f.caps }
func (f *fakeProvider) Generate(ctx context.Context, req provider.Request) (provider.Result, error) {
	f.gotReq = req
	return f.result, f.err
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setupWorkspace(t *testing.T) (root, briefPath string) {
	t.Helper()
	root = t.TempDir()
	if _, err := workspace.ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ScaffoldProject(root, "gradient-descent"); err != nil {
		t.Fatal(err)
	}
	briefPath = filepath.Join(root, "projects", "gradient-descent", "briefs", "b_001.md")
	if err := os.WriteFile(briefPath, []byte(genBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, briefPath
}

func successProvider(t *testing.T) *fakeProvider {
	img := pngBytes(t)
	rawBody, _ := json.Marshal(map[string]any{
		"data": base64.StdEncoding.EncodeToString(make([]byte, 5000)),
		"note": "wire response",
	})
	return &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4, AspectModes: []string{"1:1"}},
		result: provider.Result{
			Images:       []provider.Image{{Bytes: img, W: 2, H: 2}},
			Latency:      123 * time.Millisecond,
			Meta:         provider.CallMeta{ModelReturned: "fake-model-9", HTTPStatus: 200, ProviderRequestID: "req_f"},
			Raw:          rawBody,
			AspectActual: "1:1",
		},
	}
}

func genParams(root, briefPath string, p provider.Provider) GenParams {
	return GenParams{
		Root: root, BriefPath: briefPath, ProfileID: "test-profile",
		Profile: config.Profile{Provider: "fake", Model: "fake-model", Native: map[string]any{"k": "v"}},
		N:       1, Harness: "splatter test",
		Pricing: &config.Pricing{Version: "2026-07-25.0",
			Models: map[string]config.ModelPrice{"fake-model-9": {USDPerImage: 0.05}}},
		Provider: p,
	}
}

func TestGenWritesCompleteEvidence(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := successProvider(t)
	res, err := Gen(context.Background(), genParams(root, briefPath, fake))
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed || res.Run != "r_0001" || res.Call != "c_01" || res.Project != "gradient-descent" {
		t.Fatalf("result: %+v", res)
	}
	runDir := filepath.Join(root, "projects", "gradient-descent", "runs", "r_0001")

	// image file written and hash matches record
	imgPath := filepath.Join(runDir, "images", "c_01_0.png")
	imgData, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(imgData)
	if res.Images[0].SHA256 != hex.EncodeToString(sum[:]) || res.Images[0].File != "images/c_01_0.png" {
		t.Fatalf("image ref: %#v", res.Images[0])
	}

	// raw sidecar exists and is redacted
	rawData, err := os.ReadFile(filepath.Join(runDir, "raw", "c_01.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawData), "$blob") || strings.Contains(string(rawData), base64.StdEncoding.EncodeToString(make([]byte, 100))) {
		t.Fatalf("sidecar not redacted: %s", rawData)
	}

	// manifest: header line then call line, both schema-valid
	mf, err := os.ReadFile(filepath.Join(runDir, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(mf), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("want 2 manifest lines, got %d", len(lines))
	}
	h, err := schema.DecodeManifestLine(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	header := h.(*schema.RunHeader)
	if header.Run != "r_0001" || header.Project != "gradient-descent" ||
		header.BriefSHA256 != schema.BriefSHA256([]byte(genBrief)) ||
		header.Brief != "briefs/b_001.md" || header.Harness != "splatter test" {
		t.Fatalf("header: %+v", header)
	}
	c, err := schema.DecodeManifestLine(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	call := c.(*schema.CallRecord)
	if call.Provider != "fake" || call.ModelRequested != "fake-model" || call.ModelReturned != "fake-model-9" ||
		call.Profile != "test-profile" || call.Operation != "generate" ||
		call.Response.LatencyMS != 123 || call.Response.HTTPStatus != 200 ||
		call.Cost.Source != "table:2026-07-25.0" || *call.Cost.USD != 0.05 ||
		call.Raw != "raw/c_01.json" || call.Request.Native["k"] != "v" {
		t.Fatalf("call: %+v", call)
	}
	// prompt passed through from brief body via request
	if fake.gotReq.Prompt == "" || fake.gotReq.Aspect != "square" || fake.gotReq.N != 1 {
		t.Fatalf("provider request: %+v", fake.gotReq)
	}
}

func TestGenSecondRunAllocatesNextID(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	for i := 0; i < 2; i++ {
		if _, err := Gen(context.Background(), genParams(root, briefPath, successProvider(t))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "projects", "gradient-descent", "runs", "r_0002", "manifest.jsonl")); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func TestGenProviderFailureWritesFailedRecord(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4},
		result: provider.Result{Latency: 55 * time.Millisecond, Raw: []byte(`{"error":"x"}`),
			Meta: provider.CallMeta{HTTPStatus: 429}},
		err: &provider.Error{Stage: "request", HTTPStatus: 429, Message: "rate limited"},
	}
	res, err := Gen(context.Background(), genParams(root, briefPath, fake))
	if err != nil {
		t.Fatalf("provider failure must not be a Gen error: %v", err)
	}
	if !res.Failed || res.Error == nil || res.Error.Stage != "request" || res.Error.HTTPStatus != 429 {
		t.Fatalf("result: %+v", res)
	}
	runDir := filepath.Join(root, "projects", "gradient-descent", "runs", "r_0001")
	mf, _ := os.ReadFile(filepath.Join(runDir, "manifest.jsonl"))
	lines := bytes.Split(bytes.TrimSpace(mf), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("failed call still gets a record; lines=%d", len(lines))
	}
	call, err := schema.DecodeManifestLine(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	cr := call.(*schema.CallRecord)
	if len(cr.Images) != 0 || cr.Error == nil || cr.Error.Message != "rate limited" ||
		cr.Response.LatencyMS != 55 || cr.Cost.Source != "unavailable" {
		t.Fatalf("failed record: %+v", cr)
	}
	// failed exchange's raw body still becomes a sidecar
	if _, err := os.Stat(filepath.Join(runDir, "raw", "c_01.json")); err != nil {
		t.Fatalf("failure sidecar: %v", err)
	}
}

func TestGenCapabilityViolationLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := &fakeProvider{caps: provider.Capabilities{MaxBatch: 1}}
	p := genParams(root, briefPath, fake)
	p.N = 4
	_, err := Gen(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "batch") {
		t.Fatalf("want capability error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("capability violation must not create a run: %v", entries)
	}
}

func TestGenUnscaffoldedProjectErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(root, "brief.md")
	if err := os.WriteFile(briefPath, []byte(genBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Gen(context.Background(), genParams(root, briefPath, successProvider(t)))
	if err == nil || !strings.Contains(err.Error(), "splatter init gradient-descent") {
		t.Fatalf("want unscaffolded-project error, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/run/ -v`
Expected: FAIL — `Gen`, `GenParams`, `GenResult` undefined.

- [ ] **Step 3: Write run.go**

```go
package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
)

type GenParams struct {
	Root      string
	BriefPath string
	ProfileID string
	Profile   config.Profile
	N         int
	Harness   string
	Pricing   *config.Pricing
	Provider  provider.Provider
}

type GenResult struct {
	Project   string            `json:"project"`
	Run       string            `json:"run"`
	Call      string            `json:"call"`
	Images    []schema.ImageRef `json:"images"`
	Cost      schema.Cost       `json:"cost"`
	LatencyMS int64             `json:"latency_ms"`
	Failed    bool              `json:"failed"`
	Error     *schema.CallError `json:"error,omitempty"`
}

// Gen runs one generation call end to end and writes all evidence. A
// provider failure produces a failed-call record and Failed=true, not an
// error; the error return means no evidence could be written at all.
func Gen(ctx context.Context, p GenParams) (*GenResult, error) {
	briefBytes, err := os.ReadFile(p.BriefPath)
	if err != nil {
		return nil, err
	}
	meta, body, err := schema.ParseBrief(briefBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.BriefPath, err)
	}
	projDir := filepath.Join(p.Root, "projects", meta.Project)
	if fi, err := os.Stat(projDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("project %q not scaffolded (run: splatter init %s)", meta.Project, meta.Project)
	}

	// Capability checks: planning time, before any wire call or disk write.
	caps := p.Provider.Capabilities()
	if p.N < 1 {
		return nil, fmt.Errorf("n must be >= 1")
	}
	if p.N > caps.MaxBatch {
		return nil, fmt.Errorf("n=%d exceeds provider %s max batch %d", p.N, p.Provider.Name(), caps.MaxBatch)
	}

	runID, runDir, err := allocateRun(projDir)
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"images", "raw"} {
		if err := os.MkdirAll(filepath.Join(runDir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	briefRel, err := filepath.Rel(projDir, p.BriefPath)
	if err != nil || strings.HasPrefix(briefRel, "..") {
		// brief outside the project dir: store slash-cleaned absolute-ish
		// fallback is not allowed; require in-project briefs.
		return nil, fmt.Errorf("brief %s must live under %s", p.BriefPath, filepath.Join(projDir, "briefs"))
	}
	header := schema.RunHeader{
		V: 1, Type: "run", Run: runID, Project: meta.Project,
		Brief: filepath.ToSlash(briefRel), BriefSHA256: schema.BriefSHA256(briefBytes),
		Iteration: 1, Created: time.Now().UTC().Truncate(time.Second), Harness: p.Harness,
	}
	manifest := filepath.Join(runDir, "manifest.jsonl")
	if err := fsio.AppendRecord(manifest, header); err != nil {
		return nil, err
	}

	callID := "c_01"
	req := provider.Request{
		Model:  p.Profile.Model,
		Prompt: strings.TrimSpace(meta.Concept + "\n\n" + body),
		N:      p.N,
		Aspect: meta.Aspect,
		Native: p.Profile.Native,
	}
	res, genErr := p.Provider.Generate(ctx, req)

	// Sidecar: written for success AND wire failures (evidence either way).
	rawRel := ""
	if len(res.Raw) > 0 {
		rawRel = "raw/" + callID + ".json"
		if err := writeNew(filepath.Join(runDir, "raw", callID+".json"), RedactRaw(res.Raw)); err != nil {
			return nil, err
		}
	}

	record := schema.CallRecord{
		V: 1, Type: "call", Run: runID, Call: callID,
		TS:       time.Now().UTC().Truncate(time.Second),
		Provider: p.Provider.Name(), ModelRequested: p.Profile.Model,
		ModelReturned: res.Meta.ModelReturned, Profile: p.ProfileID, Operation: "generate",
		Request: schema.CallRequest{Prompt: req.Prompt, N: p.N, Aspect: meta.Aspect, Native: p.Profile.Native},
		Response: schema.CallResponse{
			LatencyMS:         res.Latency.Milliseconds(),
			HTTPStatus:        res.Meta.HTTPStatus,
			ProviderRequestID: res.Meta.ProviderRequestID,
		},
		Raw: rawRel,
	}

	result := &GenResult{Project: meta.Project, Run: runID, Call: callID, LatencyMS: res.Latency.Milliseconds()}
	if genErr != nil {
		record.Error = callErrorFrom(genErr)
		record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, p.Profile.Model, 0, p.Pricing)
		result.Failed, result.Error, result.Cost = true, record.Error, record.Cost
		if err := fsio.AppendRecord(manifest, record); err != nil {
			return nil, err
		}
		return result, nil
	}

	for i, img := range res.Images {
		id := fmt.Sprintf("%s_%d", callID, i)
		rel := "images/" + id + ".png"
		if err := writeNew(filepath.Join(runDir, "images", id+".png"), img.Bytes); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(img.Bytes)
		record.Images = append(record.Images, schema.ImageRef{
			ID: id, File: rel, SHA256: hex.EncodeToString(sum[:]),
			W: img.W, H: img.H, AspectActual: res.AspectActual,
		})
	}
	record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, p.Profile.Model, len(res.Images), p.Pricing)
	if err := fsio.AppendRecord(manifest, record); err != nil {
		return nil, err
	}
	result.Images, result.Cost = record.Images, record.Cost
	return result, nil
}

// allocateRun returns the next sequential run ID and creates its directory.
func allocateRun(projDir string) (string, string, error) {
	runsDir := filepath.Join(projDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return "", "", err
	}
	max := 0
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "r_")); err == nil && n > max {
			max = n
		}
	}
	id := fmt.Sprintf("r_%04d", max+1)
	dir := filepath.Join(runsDir, id)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", "", err
	}
	return id, dir, nil
}

// writeNew creates a file exclusively — evidence is never overwritten.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func callErrorFrom(err error) *schema.CallError {
	if pe, ok := err.(*provider.Error); ok {
		return &schema.CallError{Stage: pe.Stage, HTTPStatus: pe.HTTPStatus, Message: pe.Message}
	}
	return &schema.CallError{Stage: "request", Message: err.Error()}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/run/ -v` then `go test ./...`
Expected: PASS everywhere.

- [ ] **Step 5: Commit**

```bash
git add internal/run/
git commit -m "feat: gen orchestration writes complete evidence"
```

---

### Task 9: gen command and live-check script

**Files:**
- Create: `cmd/splatter/gen.go`, `docs/live-check.md`
- Modify: `cmd/splatter/root.go` (register `newGenCmd()`)
- Test: append to `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `run.Gen`/`GenParams`/`GenResult` (Task 8), `config.LoadProviders`/`LoadPricing`/`Resolve` (Task 4), `gemini.New`/`openai.New` (Tasks 5–6), `workspace.FindRoot`, `emit`, `usageErr`, `runCLI`, package var `version` (root.go).
- Produces: `newGenCmd()`; package var `buildProvider func(config.Profile) (provider.Provider, error)` — the seam CLI tests use to inject a fake provider.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/splatter/cli_test.go`:

```go
type cliFakeProvider struct {
	fail bool
}

func (f *cliFakeProvider) Name() string { return "fake" }
func (f *cliFakeProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{MaxBatch: 4}
}
func (f *cliFakeProvider) Generate(ctx context.Context, req provider.Request) (provider.Result, error) {
	if f.fail {
		return provider.Result{Latency: time.Millisecond, Raw: []byte(`{"error":"boom"}`),
				Meta: provider.CallMeta{HTTPStatus: 500}},
			&provider.Error{Stage: "request", HTTPStatus: 500, Message: "boom"}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		return provider.Result{}, err
	}
	return provider.Result{
		Images:       []provider.Image{{Bytes: buf.Bytes(), W: 2, H: 2}},
		Latency:      5 * time.Millisecond,
		Meta:         provider.CallMeta{ModelReturned: "fake-model", HTTPStatus: 200},
		Raw:          []byte(`{"ok":true}`),
		AspectActual: "1:1",
	}, nil
}

func withFakeProvider(t *testing.T, fake provider.Provider) {
	t.Helper()
	orig := buildProvider
	buildProvider = func(prof config.Profile) (provider.Provider, error) { return fake, nil }
	t.Cleanup(func() { buildProvider = orig })
}

func setupGenWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := runCLI(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "init", "gradient-descent"); err != nil {
		t.Fatal(err)
	}
	brief := "---\nid: b_001\nproject: gradient-descent\nconcept: c\naspect: square\n---\nprose\n"
	p := filepath.Join(dir, "projects", "gradient-descent", "briefs", "b_001.md")
	if err := os.WriteFile(p, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGenSuccessJSON(t *testing.T) {
	withFakeProvider(t, &cliFakeProvider{})
	dir := setupGenWorkspace(t)
	out, err := runCLI(t, dir, "gen", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--profile", "gemini-baseline", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Run    string `json:"run"`
		Failed bool   `json:"failed"`
		Images []struct {
			File string `json:"file"`
		} `json:"images"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, out)
	}
	if res.Run != "r_0001" || res.Failed || len(res.Images) != 1 {
		t.Fatalf("result: %+v", res)
	}
	// validate accepts the evidence gen wrote
	if _, err := runCLI(t, dir, "validate"); err != nil {
		t.Fatalf("workspace must validate after gen: %v", err)
	}
}

func TestGenProviderFailureExitsOne(t *testing.T) {
	withFakeProvider(t, &cliFakeProvider{fail: true})
	dir := setupGenWorkspace(t)
	_, err := runCLI(t, dir, "gen", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--profile", "gemini-baseline")
	if err == nil {
		t.Fatal("failed call must exit nonzero")
	}
	var u usageErr
	var v validationErr
	if errors.As(err, &u) || errors.As(err, &v) {
		t.Fatalf("provider failure is a runtime error (exit 1), got %T", err)
	}
	// evidence still written
	if _, statErr := os.Stat(filepath.Join(dir, "projects", "gradient-descent",
		"runs", "r_0001", "manifest.jsonl")); statErr != nil {
		t.Fatalf("failed call must still leave a manifest: %v", statErr)
	}
}

func TestGenUsageErrors(t *testing.T) {
	withFakeProvider(t, &cliFakeProvider{})
	dir := setupGenWorkspace(t)
	_, err := runCLI(t, dir, "gen", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--profile", "nope")
	var u usageErr
	if !errors.As(err, &u) {
		t.Fatalf("unknown profile: want usageErr, got %v", err)
	}
	_, err = runCLI(t, dir, "gen", "--profile", "gemini-baseline")
	if err == nil {
		t.Fatal("missing --brief must error")
	}
}
```

(add to cli_test.go imports: `"context"`, `"image"`, `"image/png"`, `"time"`, `"github.com/cajundata/splatter/internal/config"`, `"github.com/cajundata/splatter/internal/provider"`; `bytes` is already imported.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/splatter/ -run TestGen -v`
Expected: FAIL — `buildProvider` undefined / `unknown command "gen"`.

- [ ] **Step 3: Write gen.go and register**

Add to `newRootCmd` in `cmd/splatter/root.go`:

```go
	root.AddCommand(newGenCmd())
```

`cmd/splatter/gen.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/provider/gemini"
	"github.com/cajundata/splatter/internal/provider/openai"
	"github.com/cajundata/splatter/internal/run"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

// buildProvider constructs the adapter for a profile. Package var so CLI
// tests can substitute a fake provider under the real command path.
var buildProvider = func(prof config.Profile) (provider.Provider, error) {
	switch prof.Provider {
	case "gemini":
		return gemini.New(os.Getenv("GEMINI_API_KEY"), ""), nil
	case "openai":
		return openai.New(os.Getenv("OPENAI_API_KEY"), ""), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", prof.Provider)
	}
}

func newGenCmd() *cobra.Command {
	var briefPath, profileID string
	var n int
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate image concepts from a brief through one provider profile",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := workspace.FindRoot(cwd)
			if err != nil {
				if errors.Is(err, workspace.ErrNoWorkspace) {
					return usageErr{err}
				}
				return err
			}
			providers, err := config.LoadProviders(filepath.Join(root, "providers.yaml"))
			if err != nil {
				return err
			}
			pricing, err := config.LoadPricing(filepath.Join(root, "pricing.yaml"))
			if err != nil {
				return err
			}
			prof, err := providers.Resolve(profileID)
			if err != nil {
				return usageErr{err}
			}
			prov, err := buildProvider(prof)
			if err != nil {
				return err
			}
			abs := briefPath
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(cwd, briefPath)
			}
			res, err := run.Gen(cmd.Context(), run.GenParams{
				Root: root, BriefPath: abs, ProfileID: profileID, Profile: prof,
				N: n, Harness: "splatter " + version, Pricing: pricing, Provider: prov,
			})
			if err != nil {
				return err
			}
			var b strings.Builder
			if res.Failed {
				fmt.Fprintf(&b, "%s %s FAILED: %s (latency %dms)\n", res.Run, res.Call, res.Error.Message, res.LatencyMS)
			} else {
				fmt.Fprintf(&b, "%s %s: %d image(s), %s, latency %dms\n",
					res.Run, res.Call, len(res.Images), costString(res.Cost), res.LatencyMS)
				for _, img := range res.Images {
					fmt.Fprintf(&b, "  %s (%dx%d)\n", img.File, img.W, img.H)
				}
			}
			if err := emit(cmd, res, b.String()); err != nil {
				return err
			}
			if res.Failed {
				return fmt.Errorf("call failed: %s", res.Error.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&briefPath, "brief", "", "path to the brief markdown file (required)")
	cmd.Flags().StringVar(&profileID, "profile", "", "provider profile id from providers.yaml (required)")
	cmd.Flags().IntVarP(&n, "n", "n", 1, "number of images to request")
	cmd.MarkFlagRequired("brief")
	cmd.MarkFlagRequired("profile")
	return cmd
}

func costString(c schema.Cost) string {
	if c.USD == nil {
		return "cost " + c.Source
	}
	return fmt.Sprintf("cost $%.4f (%s)", *c.USD, c.Source)
}
```

Imports for gen.go additionally need `"errors"` and `"github.com/cajundata/splatter/internal/schema"`. In the RunE body, write the workspace check inline exactly like status.go does (there is no `errorsIsNoWorkspace` helper — use `errors.Is` directly):

```go
			root, err := workspace.FindRoot(cwd)
			if err != nil {
				if errors.Is(err, workspace.ErrNoWorkspace) {
					return usageErr{err}
				}
				return err
			}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/splatter/ -v` then `go test ./...`
Expected: PASS everywhere. Note `cobra.MarkFlagRequired` produces cobra's own error for a missing `--brief`; it surfaces via `Execute()` — the test only asserts non-nil. Confirm manually that its exit code is 2 (it flows through `SetFlagErrorFunc`); if it maps to 1, wrap it explicitly and note the fix.

- [ ] **Step 5: Write docs/live-check.md**

```markdown
# S2 Live Exit-Criterion Check (operator-run)

Automated tests never touch provider APIs. This script is the live half of
S2's exit criterion. Cost: one Gemini call + one OpenAI call (~$0.08).

In a fresh shell (keys never enter files or the repo):

    export GEMINI_API_KEY=...        # from your key store
    export OPENAI_API_KEY=...

    cd <your workspace>              # or: mkdir ws && cd ws && splatter init
    splatter init demo               # if the project doesn't exist yet
    cat > projects/demo/briefs/b_001.md <<'EOF'
    ---
    id: b_001
    project: demo
    concept: minimal contour-line mountain range, single-color print
    aspect: square
    ---
    Sparse, geometric, one accent line. No text.
    EOF

    splatter gen --brief projects/demo/briefs/b_001.md --profile gemini-baseline
    splatter gen --brief projects/demo/briefs/b_001.md --profile openai-baseline
    splatter validate

Confirm for EACH run directory under projects/demo/runs/:
- [ ] images/ contains PNG(s) that open
- [ ] manifest.jsonl call record has latency_ms > 0, http_status 200
- [ ] cost.source is "table:<version>" (or "reported"), never untagged
- [ ] raw/c_01.json exists and contains "$blob" instead of image base64
- [ ] `splatter validate` exits 0

Then unset the keys: `unset GEMINI_API_KEY OPENAI_API_KEY`
```

- [ ] **Step 6: Commit**

```bash
git add cmd/splatter/ docs/live-check.md
git commit -m "feat: gen command and operator live-check script"
```

---

## Exit Criterion Recap

1. `go test ./...` green with zero live API calls — every task.
2. `gen` writes images + call record with transport-measured latency, sourced cost, redacted sidecar — Tasks 8–9 (fake-provider proof), operator's `docs/live-check.md` run (live proof).
3. Evidence validates: `splatter validate` passes on a workspace after `gen` — Task 9 CLI test.