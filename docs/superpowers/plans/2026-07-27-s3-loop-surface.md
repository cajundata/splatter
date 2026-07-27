# S3 Loop Surface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The critique loop's surface: `splatter fan` (one brief, one run, one call per profile), `splatter sheet` (self-contained static HTML review surface in the run directory), `splatter verdict` (validated append to `verdicts.jsonl`), plus the S2 polish backlog.

**Architecture:** Orchestration unifies in `internal/run`: per-call execution extracts from `Gen` into `executeCall`, a new `Fan` runs sequential calls across resolved profiles, and `Gen` becomes a thin one-profile wrapper over `Fan`. Concrete adapters gain an optional `Preflight` method (discovered by type assertion — the spec-frozen `Provider` interface is untouched) so a fan aborts with zero disk trace before any wire call. Run location (`FindRun`) and manifest reading (`ReadManifest`) live in `internal/workspace`, shared by `verdict` and `sheet`. The sheet is a derived file: one embedded `html/template`, inline CSS, no JavaScript, written via `fsio.ReplaceFile`.

**Tech Stack:** Go, cobra, `html/template` + `go:embed`. No new dependencies.

**Design doc:** `docs/superpowers/specs/2026-07-27-s3-loop-surface-design.md`. The splatter spec (`docs/splatter_spec.md`) §4.3, §5, §10 govern.

## Global Constraints

- Exit codes (spec §5): 0 success, 1 runtime/provider error, 2 usage error, 3 validation failure. `fan` exits 0 if at least one call succeeded, 1 only if all calls failed.
- Every command accepts `--json` for a machine-readable result object on stdout; logs go to stderr. Use the existing `emit()` helper in `cmd/splatter/root.go`.
- Usage errors inside `RunE` are returned as `usageErr{err}`; validation failures as `validationErr{err}` (see `cmd/splatter/main.go`).
- Exactly ONE `runtime.GOOS` branch is permitted in the whole codebase: the browser-open helper (spec §8). Nothing else may branch on GOOS.
- All paths through `path/filepath`; all manifest-stored paths relative and slash-normalized (`filepath.ToSlash`).
- Evidence files are written only via `fsio.AppendRecord`; derived regenerable files only via `fsio.ReplaceFile`; new evidence files (images, sidecars) via `internal/run`'s `writeNew` (O_EXCL — evidence is never overwritten).
- Every record carries `"v": 1`. Empty `keep`/`cull` in verdict records serialize as `[]`, never `null`.
- The `Provider` interface in `internal/provider/provider.go` is spec-frozen (§6.1). Do not add methods to it. `Preflight` goes on the concrete adapter structs only.
- Timestamps in records: `time.Now().UTC().Truncate(time.Second)`.
- Commit messages: existing repo style (`feat:`, `fix:`, `test:`, `refactor:`, `docs:` prefixes, lowercase summary). NEVER add Co-Authored-By or any AI-attribution trailers.
- After each task: `go test ./...` green before committing.
- Out of scope (design doc): Spaces push/pull, export, report, critique authoring, refinement lineage creation (`parent_run` stays null in fan-created headers; the sheet's parent-link *rendering* is in scope), sheet visual polish beyond the spec's content list, concurrency in fan.

---

### Task 1: S2 backlog — run: `errors.As` config-stage detection + `Root` doc note

**Files:**
- Modify: `internal/run/run.go`
- Test: `internal/run/run_test.go`

**Interfaces:**
- Consumes: `provider.Error` (`internal/provider/provider.go`), existing `Gen`.
- Produces: no signature changes. Behavior change: a config-stage `*provider.Error` wrapped by `fmt.Errorf("...: %w", err)` is now detected (previously only a bare `*provider.Error` was).

- [ ] **Step 1: Write the failing test**

Add to `internal/run/run_test.go` (add `"fmt"` to the test file's imports):

```go
func TestGenWrappedConfigErrorLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4},
		err:  fmt.Errorf("adapter: %w", &provider.Error{Stage: "config", Message: "missing FAKE_API_KEY"}),
	}
	_, err := Gen(context.Background(), genParams(root, briefPath, fake))
	if err == nil || !strings.Contains(err.Error(), "missing FAKE_API_KEY") {
		t.Fatalf("wrapped config-stage failure must be a Gen error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("config-stage failure must leave no run dirs: %v", entries)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/run/ -run TestGenWrappedConfigErrorLeavesNoTrace -v`
Expected: FAIL — the wrapped error is not recognized as config-stage, so Gen writes a failed-call record and returns no error (`wrapped config-stage failure must be a Gen error, got <nil>`).

- [ ] **Step 3: Switch detection to `errors.As`**

In `internal/run/run.go`, add `"errors"` to imports. Replace:

```go
	res, genErr := p.Provider.Generate(ctx, req)
	if pe, ok := genErr.(*provider.Error); ok && pe.Stage == "config" {
		return nil, genErr
	}
```

with:

```go
	res, genErr := p.Provider.Generate(ctx, req)
	var pe *provider.Error
	if errors.As(genErr, &pe) && pe.Stage == "config" {
		return nil, genErr
	}
```

Also replace the body of `callErrorFrom` (same class of bug — a wrapped `*provider.Error` would be mis-recorded as stage `"request"` with no HTTP status):

```go
func callErrorFrom(err error) *schema.CallError {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return &schema.CallError{Stage: pe.Stage, HTTPStatus: pe.HTTPStatus, Message: pe.Message}
	}
	return &schema.CallError{Stage: "request", Message: err.Error()}
}
```

And add the backlog doc note on `GenParams`:

```go
type GenParams struct {
	// Root is the workspace root and is expected to be an absolute path
	// (as returned by workspace.FindRoot). BriefPath may be cwd-relative.
	Root      string
	BriefPath string
	ProfileID string
	Profile   config.Profile
	N         int
	Harness   string
	Pricing   *config.Pricing
	Provider  provider.Provider
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/run/ -v`
Expected: all PASS, including the new test and all existing `TestGen*` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/run/run.go internal/run/run_test.go
git commit -m "fix: detect wrapped config-stage provider errors via errors.As"
```

---

### Task 2: S2 backlog — openai: partial-evidence decode-branch tests

Test-only task. The two decode branches in `internal/provider/openai/openai.go` (bad base64 at line ~124, undecodable image at ~128) return a partial `Result` carrying Latency/Raw/Meta alongside the error; only the empty-`data` branch is currently tested.

**Files:**
- Test: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: existing test helpers `serve`, `tinyPNG` and adapter `New(apiKey, baseURL)` in that file.
- Produces: nothing — regression coverage only.

- [ ] **Step 1: Write the two tests**

Add to `internal/provider/openai/openai_test.go` (file already imports `base64`, `fmt`, `errors`, `strings`):

```go
func TestGenerateBadBase64IsDecodeErrorWithEvidence(t *testing.T) {
	srv := serve(t, 200, `{"created":1753500000,"data":[{"b64_json":"%%%not-base64%%%"}]}`, nil)
	defer srv.Close()
	a := New("k", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "decode" || !strings.Contains(pe.Message, "base64") {
		t.Fatalf("want decode error mentioning base64, got %v", err)
	}
	if res.Latency <= 0 || res.Meta.HTTPStatus != 200 || len(res.Raw) == 0 {
		t.Fatalf("partial result lost evidence: %+v", res)
	}
	if res.Meta.ModelReturned != "m" || res.Meta.ProviderRequestID != "req_oai_1" {
		t.Fatalf("partial result lost meta: %#v", res.Meta)
	}
}

func TestGenerateUndecodableImageIsDecodeErrorWithEvidence(t *testing.T) {
	notAnImage := base64.StdEncoding.EncodeToString([]byte("plainly not a png"))
	srv := serve(t, 200, fmt.Sprintf(`{"created":1753500000,"data":[{"b64_json":"%s"}]}`, notAnImage), nil)
	defer srv.Close()
	a := New("k", srv.URL)
	res, err := a.Generate(context.Background(), provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"})
	var pe *provider.Error
	if !errors.As(err, &pe) || pe.Stage != "decode" || !strings.Contains(pe.Message, "undecodable") {
		t.Fatalf("want decode error mentioning undecodable, got %v", err)
	}
	if res.Latency <= 0 || res.Meta.HTTPStatus != 200 || len(res.Raw) == 0 {
		t.Fatalf("partial result lost evidence: %+v", res)
	}
	if res.Meta.ModelReturned != "m" || res.Meta.ProviderRequestID != "req_oai_1" {
		t.Fatalf("partial result lost meta: %#v", res.Meta)
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/provider/openai/ -run TestGenerate -v`
Expected: PASS immediately (the implementation already behaves this way; these are the backlog's missing regression tests). If either FAILS, that is a real S2 bug — stop and fix the adapter branch to return `decodePartial()` before proceeding.

- [ ] **Step 3: Commit**

```bash
git add internal/provider/openai/openai_test.go
git commit -m "test: partial-evidence coverage for openai decode branches"
```

---

### Task 3: S2 backlog — config: `Resolve` errors carry the providers.yaml path

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `LoadProviders(path)`, `(*Providers).Resolve(profileID)`.
- Produces: `Providers` gains an unexported `path` field set by `LoadProviders`; unexported helper `(*Providers).source() string` (returns the loaded path, `"providers.yaml"` fallback for literals). Task 9 reuses `source()` in `ResolveSet`.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestResolveErrorCarriesProvidersPath(t *testing.T) {
	path := writeFile(t, "providers.yaml", goodProviders)
	p, err := LoadProviders(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("Resolve error must carry the providers.yaml path %q, got %v", path, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/config/ -run TestResolveErrorCarriesProvidersPath -v`
Expected: FAIL — error message has no path.

- [ ] **Step 3: Thread the path through**

In `internal/config/config.go`:

```go
type Providers struct {
	Version     int                 `yaml:"version"`
	Profiles    map[string]Profile  `yaml:"profiles"`
	ProfileSets map[string][]string `yaml:"profile_sets"`
	path        string              // providers.yaml location, carried into resolution errors
}
```

In `LoadProviders`, after the version check passes, add:

```go
	p.path = path
```

Add the helper and update `Resolve`'s error line:

```go
// source names the file resolution errors point at; struct literals
// (tests) fall back to the conventional filename.
func (p *Providers) source() string {
	if p.path == "" {
		return "providers.yaml"
	}
	return p.path
}
```

```go
		return Profile{}, fmt.Errorf("%s: unknown profile %q (available: %s)", p.source(), profileID, strings.Join(ids, ", "))
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config/ -v`
Expected: all PASS (`TestResolveUnknownProfileListsAvailable` still passes — it only asserts the available-profile listing).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "fix: profile resolution errors name the providers.yaml path"
```

---

### Task 4: S2 backlog — gen: nonexistent `--brief` file is a usage error

**Files:**
- Modify: `cmd/splatter/gen.go`
- Test: `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `run.Gen` (returns the raw `*os.PathError` from `os.ReadFile` when the brief doesn't exist), `usageErr`, test helpers `withFakeProvider`, `setupGenWorkspace`, `runCLI`.
- Produces: `gen` with a missing brief file now exits 2 instead of 1.

- [ ] **Step 1: Write the failing test**

Add to `cmd/splatter/cli_test.go`:

```go
func TestGenMissingBriefFileIsUsageError(t *testing.T) {
	withFakeProvider(t, &cliFakeProvider{})
	dir := setupGenWorkspace(t)
	_, err := runCLI(t, dir, "gen", "--brief", "projects/gradient-descent/briefs/nope.md",
		"--profile", "gemini-baseline")
	var u usageErr
	if !errors.As(err, &u) {
		t.Fatalf("nonexistent brief: want usageErr (exit 2), got %T: %v", err, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/splatter/ -run TestGenMissingBriefFileIsUsageError -v`
Expected: FAIL — plain error (exit 1), not `usageErr`.

- [ ] **Step 3: Map the error in gen.go**

In `cmd/splatter/gen.go`, replace the error handling after `run.Gen`:

```go
			res, err := run.Gen(cmd.Context(), run.GenParams{
				Root: root, BriefPath: briefPath, ProfileID: profileID, Profile: prof,
				N: n, Harness: "splatter " + version, Pricing: pricing, Provider: prov,
			})
			if err != nil {
				if os.IsNotExist(err) {
					return usageErr{err}
				}
				return err
			}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/splatter/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/splatter/gen.go cmd/splatter/cli_test.go
git commit -m "fix: gen maps nonexistent brief file to usage error"
```

---

### Task 5: Adapters: extract `validateRequest`, add `Preflight`

Both adapters get a `Preflight(provider.Request) error` running exactly their existing config-stage checks (key presence, aspect mappable, n range, seed, native-key allowlist) with zero network and zero filesystem. The checks are extracted into a shared `validateRequest` so Generate and Preflight cannot drift. Generate's own checks remain (defense in depth) because Generate now calls the same function.

**Files:**
- Modify: `internal/provider/gemini/gemini.go`
- Modify: `internal/provider/openai/openai.go`
- Test: `internal/provider/gemini/gemini_test.go`
- Test: `internal/provider/openai/openai_test.go`

**Interfaces:**
- Consumes: `provider.Request`, `provider.Error`, each adapter's existing `aspectMap` and `configErr`.
- Produces: `func (a *Adapter) Preflight(req provider.Request) error` on BOTH `gemini.Adapter` and `openai.Adapter`. Task 7's `run` package discovers this method via type assertion against `interface{ Preflight(provider.Request) error }`.
- Internal: gemini `func (a *Adapter) validateRequest(req provider.Request) (native string, err error)`; openai `func (a *Adapter) validateRequest(req provider.Request) (size, quality string, err error)`.

- [ ] **Step 1: Write the failing gemini test**

Add to `internal/provider/gemini/gemini_test.go` (the file already has `ptr` and imports `errors`, `strings`):

```go
func TestPreflightMirrorsConfigChecks(t *testing.T) {
	a := New("k", "")
	bad := []struct {
		name string
		req  provider.Request
		want string
	}{
		{"bad aspect", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "wide"}, "aspect"},
		{"n over max", provider.Request{Model: "m", Prompt: "p", N: 2, Aspect: "square"}, "batch"},
		{"n under min", provider.Request{Model: "m", Prompt: "p", N: 0, Aspect: "square"}, "batch"},
		{"seed", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square", Seed: ptr(int64(7))}, "seed"},
		{"native key", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
			Native: map[string]any{"style": "x"}}, "native"},
	}
	for _, tc := range bad {
		err := a.Preflight(tc.req)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Stage != "config" || !strings.Contains(pe.Message, tc.want) {
			t.Fatalf("%s: want config error containing %q, got %v", tc.name, tc.want, err)
		}
	}
	if err := a.Preflight(provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"}); err != nil {
		t.Fatalf("valid request must preflight clean: %v", err)
	}
	if err := New("", "").Preflight(provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"}); err == nil ||
		!strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("missing key must fail preflight, got %v", err)
	}
}
```

- [ ] **Step 2: Write the failing openai test**

Add to `internal/provider/openai/openai_test.go`:

```go
func TestPreflightMirrorsConfigChecks(t *testing.T) {
	a := New("k", "")
	bad := []struct {
		name string
		req  provider.Request
		want string
	}{
		{"bad aspect", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "wide"}, "aspect"},
		{"n over max", provider.Request{Model: "m", Prompt: "p", N: 11, Aspect: "square"}, "batch"},
		{"n under min", provider.Request{Model: "m", Prompt: "p", N: 0, Aspect: "square"}, "batch"},
		{"seed", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square", Seed: ptr(int64(1))}, "seed"},
		{"unknown native", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
			Native: map[string]any{"style": "vivid"}}, "native"},
		{"non-string quality", provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
			Native: map[string]any{"quality": 7}}, "quality"},
	}
	for _, tc := range bad {
		err := a.Preflight(tc.req)
		var pe *provider.Error
		if !errors.As(err, &pe) || pe.Stage != "config" || !strings.Contains(pe.Message, tc.want) {
			t.Fatalf("%s: want config error containing %q, got %v", tc.name, tc.want, err)
		}
	}
	if err := a.Preflight(provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square",
		Native: map[string]any{"quality": "high"}}); err != nil {
		t.Fatalf("valid request must preflight clean: %v", err)
	}
	if err := New("", "").Preflight(provider.Request{Model: "m", Prompt: "p", N: 1, Aspect: "square"}); err == nil ||
		!strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("missing key must fail preflight, got %v", err)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./internal/provider/... -run TestPreflight -v`
Expected: FAIL to compile — `a.Preflight undefined`.

- [ ] **Step 4: Extract gemini's validateRequest and add Preflight**

In `internal/provider/gemini/gemini.go`, add after `configErr`:

```go
// validateRequest runs every config-stage check with zero network and
// zero filesystem. Shared by Generate and Preflight so a passing
// pre-flight means Generate cannot fail at config stage (client
// construction aside).
func (a *Adapter) validateRequest(req provider.Request) (string, error) {
	if a.apiKey == "" {
		return "", configErr("missing GEMINI_API_KEY")
	}
	native, ok := aspectMap[req.Aspect]
	if !ok {
		return "", configErr("unsupported aspect %q", req.Aspect)
	}
	if req.N < 1 || req.N > 1 {
		return "", configErr("n=%d outside batch range 1..1", req.N)
	}
	if req.Seed != nil {
		return "", configErr("seed not supported")
	}
	// v1 allowlist is empty: any native key is unsupported, never ignored.
	for k := range req.Native {
		return "", configErr("unsupported native key %q", k)
	}
	return native, nil
}

// Preflight runs the adapter's config-stage checks without touching the
// network. Discovered by internal/run via type assertion; the frozen
// Provider interface is untouched.
func (a *Adapter) Preflight(req provider.Request) error {
	_, err := a.validateRequest(req)
	return err
}
```

Replace the opening of `Generate` (everything from `if a.apiKey == ""` through the `req.Native` loop) with:

```go
	var zero provider.Result
	native, err := a.validateRequest(req)
	if err != nil {
		return zero, err
	}
```

- [ ] **Step 5: Extract openai's validateRequest and add Preflight**

In `internal/provider/openai/openai.go`, add after `configErr`:

```go
// validateRequest runs every config-stage check with zero network and
// zero filesystem, returning the mapped native size and quality. Shared
// by Generate and Preflight so a passing pre-flight means Generate
// cannot fail at config stage.
func (a *Adapter) validateRequest(req provider.Request) (size, quality string, err error) {
	if a.apiKey == "" {
		return "", "", configErr("missing OPENAI_API_KEY")
	}
	native, ok := aspectMap[req.Aspect]
	if !ok {
		return "", "", configErr("unsupported aspect %q", req.Aspect)
	}
	if req.N < 1 || req.N > 10 {
		return "", "", configErr("n=%d outside batch range 1..10", req.N)
	}
	if req.Seed != nil {
		return "", "", configErr("seed not supported")
	}
	for k, v := range req.Native {
		switch k {
		case "quality":
			s, ok := v.(string)
			if !ok {
				return "", "", configErr("native quality must be a string, got %T", v)
			}
			quality = s
		default:
			return "", "", configErr("unsupported native key %q", k)
		}
	}
	return native, quality, nil
}

// Preflight runs the adapter's config-stage checks without touching the
// network. Discovered by internal/run via type assertion; the frozen
// Provider interface is untouched.
func (a *Adapter) Preflight(req provider.Request) error {
	_, _, err := a.validateRequest(req)
	return err
}
```

Replace the opening of `Generate` (from `if a.apiKey == ""` through the `req.Native` loop, INCLUDING the `params` construction it straddles) with:

```go
	var zero provider.Result
	native, quality, err := a.validateRequest(req)
	if err != nil {
		return zero, err
	}
	params := oai.ImageGenerateParams{
		Prompt: req.Prompt,
		Model:  oai.ImageModel(req.Model),
		N:      oai.Int(int64(req.N)),
		Size:   oai.ImageGenerateParamsSize(native),
	}
	if quality != "" {
		params.Quality = oai.ImageGenerateParamsQuality(quality)
	}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/provider/... -v`
Expected: all PASS — new Preflight tests and every existing `TestGenerateConfigErrors` table (Generate still fails identically; defense in depth intact).

- [ ] **Step 7: Commit**

```bash
git add internal/provider/gemini/gemini.go internal/provider/gemini/gemini_test.go \
        internal/provider/openai/openai.go internal/provider/openai/openai_test.go
git commit -m "feat: adapter pre-flight seam sharing config-stage checks with generate"
```

---

### Task 6: workspace: `FindRun` and `ReadManifest`

**Files:**
- Create: `internal/workspace/findrun.go`
- Test: `internal/workspace/findrun_test.go`

**Interfaces:**
- Consumes: `schema.DecodeManifestLine`, `schema.RunHeader`, `schema.CallRecord`, the package's existing `maxLineBytes` const (in `validate.go`).
- Produces (used by Tasks 10, 11, 13):
  - `var ErrRunNotFound = errors.New("run not found")`
  - `type AmbiguousRunError struct { Run string; Projects []string }` implementing `error`
  - `func FindRun(root, runID string) (project string, err error)` — zero matches wraps `ErrRunNotFound`; multi-match returns `*AmbiguousRunError` naming the candidate projects. Commands surface both as usage errors.
  - `func ReadManifest(runDir string) (*schema.RunHeader, []schema.CallRecord, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/workspace/findrun_test.go`:

```go
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/schema"
)

// scaffoldRun creates projects/<proj>/runs/<runID> under root, scaffolding
// the workspace and project on first use.
func scaffoldRun(t *testing.T, root, proj, runID string) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "providers.yaml")); err != nil {
		if _, err := ScaffoldWorkspace(root); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ScaffoldProject(root, proj); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "projects", proj, "runs", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFindRunSingleMatch(t *testing.T) {
	root := t.TempDir()
	scaffoldRun(t, root, "p1", "r_0001")
	scaffoldRun(t, root, "p2", "r_0002")
	proj, err := FindRun(root, "r_0002")
	if err != nil || proj != "p2" {
		t.Fatalf("want p2, got %q, %v", proj, err)
	}
}

func TestFindRunZeroMatches(t *testing.T) {
	root := t.TempDir()
	scaffoldRun(t, root, "p1", "r_0001")
	_, err := FindRun(root, "r_0099")
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("want ErrRunNotFound, got %v", err)
	}
}

func TestFindRunAmbiguousNamesCandidates(t *testing.T) {
	root := t.TempDir()
	scaffoldRun(t, root, "p1", "r_0001")
	scaffoldRun(t, root, "p2", "r_0001")
	_, err := FindRun(root, "r_0001")
	var amb *AmbiguousRunError
	if !errors.As(err, &amb) {
		t.Fatalf("want AmbiguousRunError, got %v", err)
	}
	if !strings.Contains(err.Error(), "p1") || !strings.Contains(err.Error(), "p2") {
		t.Fatalf("error must name candidate projects: %v", err)
	}
}

func manifestFixtures(t *testing.T, runDir string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	header := schema.RunHeader{
		V: 1, Type: "run", Run: "r_0001", Project: "p1", Brief: "briefs/b_001.md",
		BriefSHA256: "abc123", Iteration: 1, Created: now, Harness: "splatter test",
	}
	call := schema.CallRecord{
		V: 1, Type: "call", Run: "r_0001", Call: "c_01", TS: now,
		Provider: "gemini", ModelRequested: "m", ModelReturned: "m",
		Profile: "gemini-baseline", Operation: "generate",
		Request:  schema.CallRequest{Prompt: "p", N: 1, Aspect: "square"},
		Response: schema.CallResponse{LatencyMS: 5, HTTPStatus: 200},
		Cost:     schema.Cost{Source: "unavailable"},
		Images: []schema.ImageRef{{ID: "c_01_0", File: "images/c_01_0.png",
			SHA256: "deadbeef", W: 2, H: 2, AspectActual: "1:1"}},
		Raw: "raw/c_01.json",
	}
	mp := filepath.Join(runDir, "manifest.jsonl")
	for _, rec := range []any{header, call} {
		if err := fsio.AppendRecord(mp, rec); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadManifest(t *testing.T) {
	root := t.TempDir()
	runDir := scaffoldRun(t, root, "p1", "r_0001")
	manifestFixtures(t, runDir)
	header, calls, err := ReadManifest(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if header.Run != "r_0001" || header.Project != "p1" {
		t.Fatalf("header: %+v", header)
	}
	if len(calls) != 1 || calls[0].Call != "c_01" || calls[0].Images[0].ID != "c_01_0" {
		t.Fatalf("calls: %+v", calls)
	}
}

func TestReadManifestNoHeaderErrors(t *testing.T) {
	root := t.TempDir()
	runDir := scaffoldRun(t, root, "p1", "r_0001")
	if err := os.WriteFile(filepath.Join(runDir, "manifest.jsonl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadManifest(runDir); err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("want no-header error, got %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/workspace/ -run 'TestFindRun|TestReadManifest' -v`
Expected: FAIL to compile — `FindRun`, `ErrRunNotFound`, `AmbiguousRunError`, `ReadManifest` undefined.

- [ ] **Step 3: Implement findrun.go**

Create `internal/workspace/findrun.go`:

```go
package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/schema"
)

var ErrRunNotFound = errors.New("run not found")

// AmbiguousRunError reports a run ID present in more than one project.
type AmbiguousRunError struct {
	Run      string
	Projects []string
}

func (e *AmbiguousRunError) Error() string {
	return fmt.Sprintf("run %s found in multiple projects: %s (disambiguation not supported; rename or remove one)",
		e.Run, strings.Join(e.Projects, ", "))
}

// FindRun scans projects/*/runs/<runID> for the owning project. Zero
// matches wraps ErrRunNotFound; more than one returns *AmbiguousRunError
// naming the candidates. Commands surface both as usage errors.
func FindRun(root, runID string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		return "", err
	}
	var owners []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fi, err := os.Stat(filepath.Join(root, "projects", e.Name(), "runs", runID))
		if err == nil && fi.IsDir() {
			owners = append(owners, e.Name())
		}
	}
	switch len(owners) {
	case 0:
		return "", fmt.Errorf("run %q: %w", runID, ErrRunNotFound)
	case 1:
		return owners[0], nil
	default:
		return "", &AmbiguousRunError{Run: runID, Projects: owners}
	}
}

// ReadManifest decodes a run's manifest.jsonl into its header and call
// records, validating every line. Shared by verdict (image-ID checks)
// and sheet (card data).
func ReadManifest(runDir string) (*schema.RunHeader, []schema.CallRecord, error) {
	mp := filepath.Join(runDir, "manifest.jsonl")
	f, err := os.Open(mp)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var header *schema.RunHeader
	var calls []schema.CallRecord
	lineNo := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
	for sc.Scan() {
		lineNo++
		decoded, err := schema.DecodeManifestLine(sc.Bytes())
		if err != nil {
			return nil, nil, fmt.Errorf("%s line %d: %w", mp, lineNo, err)
		}
		switch r := decoded.(type) {
		case *schema.RunHeader:
			header = r
		case *schema.CallRecord:
			calls = append(calls, *r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", mp, err)
	}
	if header == nil {
		return nil, nil, fmt.Errorf("%s: manifest has no run header", mp)
	}
	return header, calls, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/workspace/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/workspace/findrun.go internal/workspace/findrun_test.go
git commit -m "feat: run location and manifest reading in workspace"
```

---

### Task 7: run: `executeCall` + `Fan`

New file `internal/run/fan.go`. `Gen` stays untouched this task (its containment code is briefly duplicated by `containedBriefRel`; Task 8 deletes the duplication when Gen becomes a wrapper).

**Files:**
- Create: `internal/run/fan.go`
- Test: `internal/run/fan_test.go`

**Interfaces:**
- Consumes: `allocateRun`, `writeNew`, `callErrorFrom`, `RedactRaw`, `ResolveCost` (all existing in `internal/run`); `fsio.AppendRecord`; `schema.ParseBrief`/`BriefSHA256`; adapter `Preflight` from Task 5 via type assertion.
- Produces (used by Tasks 8, 9):

```go
type ResolvedProfile struct {
	ID       string
	Profile  config.Profile
	Provider provider.Provider
}

type FanParams struct {
	Root      string // workspace root, absolute (from workspace.FindRoot)
	BriefPath string
	Profiles  []ResolvedProfile // one call per entry, in order
	N         int
	Harness   string
	Pricing   *config.Pricing
}

type CallOutcome struct {
	Call      string            `json:"call"`
	Profile   string            `json:"profile"`
	Provider  string            `json:"provider"`
	Images    []schema.ImageRef `json:"images"`
	Cost      schema.Cost       `json:"cost"`
	LatencyMS int64             `json:"latency_ms"`
	Failed    bool              `json:"failed"`
	Error     *schema.CallError `json:"error,omitempty"`
}

type FanResult struct {
	Project   string        `json:"project"`
	Run       string        `json:"run"`
	Calls     []CallOutcome `json:"calls"`
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
}

func Fan(ctx context.Context, p FanParams) (*FanResult, error)
```

- Internal: `executeCall(ctx context.Context, runDir, callID, profileID string, prof config.Profile, prov provider.Provider, req provider.Request, pricing *config.Pricing) (schema.CallRecord, error)` — wire call plus evidence writes (sidecar, images, record appended to the manifest). Error return is for I/O failures only; provider failures land inside the record. `containedBriefRel(briefPath, projDir string) (string, error)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/run/fan_test.go`:

```go
package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/workspace"
)

// preflightFake is a fakeProvider with a controllable Preflight result.
type preflightFake struct {
	fakeProvider
	preflightErr error
}

func (f *preflightFake) Preflight(req provider.Request) error { return f.preflightErr }

func fanParams(root, briefPath string, profiles ...ResolvedProfile) FanParams {
	return FanParams{
		Root: root, BriefPath: briefPath, Profiles: profiles,
		N: 1, Harness: "splatter test",
	}
}

func resolved(id string, p provider.Provider) ResolvedProfile {
	return ResolvedProfile{ID: id, Profile: testProfile(), Provider: p}
}

func TestFanMixedSuccessAndFailure(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	good := successProvider(t)
	bad := &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4},
		result: provider.Result{Latency: 55000000, Raw: []byte(`{"error":"x"}`),
			Meta: provider.CallMeta{HTTPStatus: 429}},
		err: &provider.Error{Stage: "request", HTTPStatus: 429, Message: "rate limited"},
	}
	res, err := Fan(context.Background(), fanParams(root, briefPath,
		resolved("prof-a", good), resolved("prof-b", bad)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Run != "r_0001" || res.Succeeded != 1 || res.Failed != 1 || len(res.Calls) != 2 {
		t.Fatalf("result: %+v", res)
	}
	// sequential call IDs in profile order
	if res.Calls[0].Call != "c_01" || res.Calls[1].Call != "c_02" {
		t.Fatalf("call ids: %s, %s", res.Calls[0].Call, res.Calls[1].Call)
	}
	if res.Calls[0].Failed || !res.Calls[1].Failed || res.Calls[1].Error.HTTPStatus != 429 {
		t.Fatalf("outcomes: %+v", res.Calls)
	}
	if res.Calls[0].Profile != "prof-a" || res.Calls[1].Profile != "prof-b" {
		t.Fatalf("profiles: %+v", res.Calls)
	}
	// one run, header + two call records; failure never destroys the sibling
	runDir := filepath.Join(root, "projects", "gradient-descent", "runs", "r_0001")
	if _, err := os.Stat(filepath.Join(runDir, "images", "c_01_0.png")); err != nil {
		t.Fatalf("success images missing: %v", err)
	}
	findings, err := workspace.Validate(root, "gradient-descent")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("fan evidence must validate cleanly: %+v", findings)
	}
}

func TestFanAllFailedIsNotAFanError(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	bad := func() *fakeProvider {
		return &fakeProvider{
			caps: provider.Capabilities{MaxBatch: 4},
			result: provider.Result{Latency: 1000000, Raw: []byte(`{"error":"x"}`),
				Meta: provider.CallMeta{HTTPStatus: 500}},
			err: &provider.Error{Stage: "request", HTTPStatus: 500, Message: "boom"},
		}
	}
	res, err := Fan(context.Background(), fanParams(root, briefPath,
		resolved("prof-a", bad()), resolved("prof-b", bad())))
	if err != nil {
		t.Fatalf("all-failed fan still returns a result (exit mapping is the command's job): %v", err)
	}
	if res.Succeeded != 0 || res.Failed != 2 {
		t.Fatalf("result: %+v", res)
	}
}

func TestFanPreflightFailureLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	good := successProvider(t)
	bad := &preflightFake{preflightErr: &provider.Error{Stage: "config", Message: "missing FAKE_API_KEY"}}
	bad.caps = provider.Capabilities{MaxBatch: 4}
	_, err := Fan(context.Background(), fanParams(root, briefPath,
		resolved("prof-a", good), resolved("prof-b", bad)))
	if err == nil || !strings.Contains(err.Error(), "missing FAKE_API_KEY") ||
		!strings.Contains(err.Error(), "prof-b") {
		t.Fatalf("want pre-flight error naming the profile, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("pre-flight failure must leave no run dirs: %v", entries)
	}
	if len(good.gotReq.Prompt) != 0 {
		t.Fatal("pre-flight failure must abort before any provider call")
	}
}

func TestFanCapabilityViolationLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	small := &fakeProvider{caps: provider.Capabilities{MaxBatch: 1}}
	p := fanParams(root, briefPath, resolved("prof-a", small))
	p.N = 4
	_, err := Fan(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "batch") {
		t.Fatalf("want capability error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("capability violation must not create a run: %v", entries)
	}
}

func TestFanConfigErrorAfterPassingPreflightIsFailedCall(t *testing.T) {
	// Should be impossible with real adapters (Preflight shares checks with
	// Generate); if it happens anyway the header is already written, so the
	// call is recorded as failed — evidence preserved, never destroyed.
	root, briefPath := setupWorkspace(t)
	liar := &preflightFake{}
	liar.caps = provider.Capabilities{MaxBatch: 4}
	liar.err = &provider.Error{Stage: "config", Message: "surprise config failure"}
	res, err := Fan(context.Background(), fanParams(root, briefPath, resolved("prof-a", liar)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Calls[0].Error == nil || res.Calls[0].Error.Stage != "config" {
		t.Fatalf("result: %+v", res)
	}
}
```

Also add the shared profile helper to `internal/run/run_test.go` (refactor `genParams` to use it so both files agree):

```go
func testProfile() config.Profile {
	return config.Profile{Provider: "fake", Model: "fake-model", Native: map[string]any{"k": "v"}}
}
```

and in `genParams` replace the `Profile:` line with `Profile: testProfile(),`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/run/ -run TestFan -v`
Expected: FAIL to compile — `Fan`, `FanParams`, `ResolvedProfile` undefined.

- [ ] **Step 3: Implement fan.go**

Create `internal/run/fan.go`:

```go
package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
)

// ResolvedProfile pairs a providers.yaml profile with its constructed
// adapter, ready to call.
type ResolvedProfile struct {
	ID       string
	Profile  config.Profile
	Provider provider.Provider
}

type FanParams struct {
	// Root is the workspace root and is expected to be an absolute path
	// (as returned by workspace.FindRoot). BriefPath may be cwd-relative.
	Root      string
	BriefPath string
	Profiles  []ResolvedProfile // one call per entry, in order
	N         int
	Harness   string
	Pricing   *config.Pricing
}

type CallOutcome struct {
	Call      string            `json:"call"`
	Profile   string            `json:"profile"`
	Provider  string            `json:"provider"`
	Images    []schema.ImageRef `json:"images"`
	Cost      schema.Cost       `json:"cost"`
	LatencyMS int64             `json:"latency_ms"`
	Failed    bool              `json:"failed"`
	Error     *schema.CallError `json:"error,omitempty"`
}

type FanResult struct {
	Project   string        `json:"project"`
	Run       string        `json:"run"`
	Calls     []CallOutcome `json:"calls"`
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
}

// preflighter is the optional pre-flight seam on concrete adapters,
// discovered by type assertion so the spec-frozen Provider interface
// (§6.1) stays untouched. Preflight runs the adapter's config-stage
// checks with zero network.
type preflighter interface {
	Preflight(provider.Request) error
}

// Fan runs one brief across every resolved profile as sequential calls
// in a single run. Every profile is pre-flighted before any disk write:
// a pre-flight or capability failure aborts the whole fan with an error
// and zero disk trace. After the run header is written, per-call
// failures land in failed-call records and never abort the fan; the
// error return from that point on is for I/O failures only.
func Fan(ctx context.Context, p FanParams) (*FanResult, error) {
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
	briefRel, err := containedBriefRel(p.BriefPath, projDir)
	if err != nil {
		return nil, err
	}
	if len(p.Profiles) == 0 {
		return nil, fmt.Errorf("no profiles to fan across")
	}

	// Pre-flight every profile: capability checks plus the adapter's own
	// config-stage checks when it implements Preflight.
	prompt := strings.TrimSpace(meta.Concept + "\n\n" + body)
	reqs := make([]provider.Request, len(p.Profiles))
	for i, rp := range p.Profiles {
		if p.N < 1 {
			return nil, fmt.Errorf("n must be >= 1")
		}
		caps := rp.Provider.Capabilities()
		if p.N > caps.MaxBatch {
			return nil, fmt.Errorf("profile %s: n=%d exceeds provider %s max batch %d",
				rp.ID, p.N, rp.Provider.Name(), caps.MaxBatch)
		}
		req := provider.Request{
			Model: rp.Profile.Model, Prompt: prompt, N: p.N,
			Aspect: meta.Aspect, Native: rp.Profile.Native,
		}
		if req.Seed != nil && !caps.Seed {
			return nil, fmt.Errorf("profile %s: seed not supported by provider %s", rp.ID, rp.Provider.Name())
		}
		if pf, ok := rp.Provider.(preflighter); ok {
			if err := pf.Preflight(req); err != nil {
				return nil, fmt.Errorf("profile %s: %w", rp.ID, err)
			}
		}
		reqs[i] = req
	}

	// All pre-checks pass; everything from here on is disk mutation.
	runID, runDir, err := allocateRun(projDir)
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"images", "raw"} {
		if err := os.MkdirAll(filepath.Join(runDir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	header := schema.RunHeader{
		V: 1, Type: "run", Run: runID, Project: meta.Project,
		Brief: filepath.ToSlash(briefRel), BriefSHA256: schema.BriefSHA256(briefBytes),
		Iteration: 1, Created: time.Now().UTC().Truncate(time.Second), Harness: p.Harness,
	}
	if err := fsio.AppendRecord(filepath.Join(runDir, "manifest.jsonl"), header); err != nil {
		return nil, err
	}

	result := &FanResult{Project: meta.Project, Run: runID, Calls: []CallOutcome{}}
	for i, rp := range p.Profiles {
		callID := fmt.Sprintf("c_%02d", i+1)
		record, err := executeCall(ctx, runDir, callID, rp.ID, rp.Profile, rp.Provider, reqs[i], p.Pricing)
		if err != nil {
			return nil, err
		}
		outcome := CallOutcome{
			Call: callID, Profile: rp.ID, Provider: rp.Provider.Name(),
			Images: record.Images, Cost: record.Cost,
			LatencyMS: record.Response.LatencyMS,
			Failed:    record.Error != nil, Error: record.Error,
		}
		if outcome.Images == nil {
			outcome.Images = []schema.ImageRef{}
		}
		if outcome.Failed {
			result.Failed++
		} else {
			result.Succeeded++
		}
		result.Calls = append(result.Calls, outcome)
	}
	return result, nil
}

// executeCall performs one provider invocation and writes its evidence
// under runDir: redacted raw sidecar, image files, and the call record
// appended to manifest.jsonl. The error return is for I/O failures
// only; provider failures (any stage — a config-stage error after a
// passing pre-flight should be impossible, but the header is already
// written, so evidence is preserved rather than destroyed) land inside
// the returned record's Error.
func executeCall(ctx context.Context, runDir, callID, profileID string, prof config.Profile,
	prov provider.Provider, req provider.Request, pricing *config.Pricing) (schema.CallRecord, error) {
	res, genErr := prov.Generate(ctx, req)

	manifest := filepath.Join(runDir, "manifest.jsonl")
	// Sidecar: written for success AND wire failures (evidence either way).
	rawRel := ""
	if len(res.Raw) > 0 {
		rawRel = "raw/" + callID + ".json"
		if err := writeNew(filepath.Join(runDir, "raw", callID+".json"), RedactRaw(res.Raw)); err != nil {
			return schema.CallRecord{}, err
		}
	}

	record := schema.CallRecord{
		V: 1, Type: "call", Run: filepath.Base(runDir), Call: callID,
		TS:       time.Now().UTC().Truncate(time.Second),
		Provider: prov.Name(), ModelRequested: prof.Model,
		ModelReturned: res.Meta.ModelReturned, Profile: profileID, Operation: "generate",
		Request: schema.CallRequest{Prompt: req.Prompt, N: req.N, Aspect: req.Aspect,
			Seed: req.Seed, Native: prof.Native},
		Response: schema.CallResponse{
			LatencyMS:         res.Latency.Milliseconds(),
			HTTPStatus:        res.Meta.HTTPStatus,
			ProviderRequestID: res.Meta.ProviderRequestID,
		},
		Raw: rawRel,
	}

	if genErr != nil {
		record.Error = callErrorFrom(genErr)
		record.Images = []schema.ImageRef{}
		record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, prof.Model, 0, pricing)
		return record, fsio.AppendRecord(manifest, record)
	}

	for i, img := range res.Images {
		id := fmt.Sprintf("%s_%d", callID, i)
		rel := "images/" + id + ".png"
		if err := writeNew(filepath.Join(runDir, "images", id+".png"), img.Bytes); err != nil {
			return schema.CallRecord{}, err
		}
		sum := sha256.Sum256(img.Bytes)
		record.Images = append(record.Images, schema.ImageRef{
			ID: id, File: rel, SHA256: hex.EncodeToString(sum[:]),
			W: img.W, H: img.H, AspectActual: res.AspectActual,
		})
	}
	record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, prof.Model, len(res.Images), pricing)
	return record, fsio.AppendRecord(manifest, record)
}

// containedBriefRel returns briefPath relative to projDir, erroring when
// the brief lives outside the project. briefPath is absolutized first
// (it may be cwd-relative) and symlinks are resolved on both sides so
// containment survives symlinked roots (e.g. macOS /tmp).
func containedBriefRel(briefPath, projDir string) (string, error) {
	absBrief, err := filepath.Abs(briefPath)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(absBrief); err == nil {
		absBrief = r
	}
	resolvedProj := projDir
	if r, err := filepath.EvalSymlinks(projDir); err == nil {
		resolvedProj = r
	}
	rel, err := filepath.Rel(resolvedProj, absBrief)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("brief %s must live under project dir %s", briefPath, projDir)
	}
	return rel, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/run/ -v`
Expected: all PASS — new `TestFan*` tests plus every existing `TestGen*` test (Gen is untouched).

- [ ] **Step 5: Commit**

```bash
git add internal/run/fan.go internal/run/fan_test.go internal/run/run_test.go
git commit -m "feat: fan orchestration with per-profile pre-flight and sequential calls"
```

---

### Task 8: run: `Gen` becomes a thin wrapper over `Fan`

**Files:**
- Modify: `internal/run/run.go`
- Modify: `internal/run/run_test.go`

**Interfaces:**
- Consumes: `Fan`, `ResolvedProfile` from Task 7.
- Produces: `Gen(ctx, GenParams) (*GenResult, error)` — signature and `GenResult` shape preserved for the `gen` command. Semantics preserved because real adapters implement `Preflight` with the same checks `Generate` would fail: config-stage failure → plain error, zero disk trace; wire failure → failed-call record.
- The test fake `fakeProvider` gains a `Preflight` that mirrors real adapters (returns its configured error when it is config-stage), which is what keeps the existing no-trace tests passing unchanged.

- [ ] **Step 1: Give fakeProvider an adapter-faithful Preflight**

In `internal/run/run_test.go`, add `"errors"` to imports and add below `Generate`:

```go
// Preflight mirrors the real adapters: the config-stage failures that
// Generate would return are reported before any wire call.
func (f *fakeProvider) Preflight(req provider.Request) error {
	var pe *provider.Error
	if errors.As(f.err, &pe) && pe.Stage == "config" {
		return f.err
	}
	return nil
}
```

(`preflightFake` in `fan_test.go` overrides this — unchanged.)

- [ ] **Step 2: Rewrite Gen as a wrapper**

In `internal/run/run.go`, replace the entire body of `Gen` (keep `GenParams`, `GenResult`, `allocateRun`, `writeNew`, `callErrorFrom`; delete the now-unused inline brief/containment/capability/evidence code):

```go
// Gen runs one generation call end to end by fanning a single profile.
// A config-stage provider failure is caught by pre-flight and returned
// as a plain error with no evidence written at all — no run allocated,
// nothing on disk. A request- or decode-stage failure reached the wire,
// so it produces a failed-call record (Failed=true) instead of an error.
func Gen(ctx context.Context, p GenParams) (*GenResult, error) {
	fr, err := Fan(ctx, FanParams{
		Root: p.Root, BriefPath: p.BriefPath,
		Profiles: []ResolvedProfile{{ID: p.ProfileID, Profile: p.Profile, Provider: p.Provider}},
		N: p.N, Harness: p.Harness, Pricing: p.Pricing,
	})
	if err != nil {
		return nil, err
	}
	c := fr.Calls[0]
	return &GenResult{
		Project: fr.Project, Run: fr.Run, Call: c.Call,
		Images: c.Images, Cost: c.Cost, LatencyMS: c.LatencyMS,
		Failed: c.Failed, Error: c.Error,
	}, nil
}
```

Remove imports that become unused in `run.go` (likely `crypto/sha256`, `encoding/hex`, `os`, `time`, `errors`, `strings`, `fsio`, `schema`, `provider` — keep whatever `allocateRun`/`writeNew`/`callErrorFrom` still need: `fmt`, `os`, `path/filepath`, `strconv`, `strings`, `provider`, `schema`). Let the compiler tell you; `gofmt` and `go vet` must be clean.

Known intentional behavior deltas (verify no test asserts otherwise; none currently does):
- A provider whose `Generate` returns a config-stage error but which implements NO `Preflight` now yields a failed-call record instead of a no-trace error. Both real adapters implement `Preflight`, so CLI behavior is unchanged.
- Failed `GenResult.Images` is now `[]` instead of `nil` (`--json` shows `"images":[]`).
- Capability-violation error messages gain a `profile <id>: ` prefix (existing test asserts only the `"batch"` substring).

- [ ] **Step 3: Run the full run-package suite**

Run: `go test ./internal/run/ -v`
Expected: ALL existing `TestGen*` tests pass unchanged (this is the design doc's regression gate), plus `TestFan*` and Task 1's wrapped-error test.

- [ ] **Step 4: Run the whole module**

Run: `go test ./...`
Expected: all PASS (the `gen` CLI tests exercise the wrapper through cobra).

- [ ] **Step 5: Commit**

```bash
git add internal/run/run.go internal/run/run_test.go
git commit -m "refactor: gen is a one-profile fan"
```

---

### Task 9: `splatter fan` command

**Files:**
- Modify: `internal/config/config.go` (add `ResolveSet`)
- Test: `internal/config/config_test.go`
- Create: `cmd/splatter/fan.go`
- Modify: `cmd/splatter/root.go` (register command)
- Test: `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `run.Fan`, `run.ResolvedProfile`, `buildProvider` seam, `costString`, `emit`, `usageErr`, `workspace.FindRoot`, `config.LoadProviders/LoadPricing/Resolve`, `source()` from Task 3.
- Produces: `func (p *Providers) ResolveSet(set string) ([]string, error)`; `splatter fan --brief <path> [--set <set> | --profiles a,b] [-n N] [--json]`. Exit 0 if ≥1 call succeeded; 1 if all failed or pre-flight failed; 2 for flag misuse (both/neither of `--set`/`--profiles`, unknown set/profile, missing/nonexistent brief, outside workspace). `--json` emits `run.FanResult`.

- [ ] **Step 1: Write the failing ResolveSet test**

Add to `internal/config/config_test.go`:

```go
func TestResolveSet(t *testing.T) {
	path := writeFile(t, "providers.yaml", goodProviders)
	p, err := LoadProviders(path)
	if err != nil {
		t.Fatal(err)
	}
	members, err := p.ResolveSet("baseline")
	if err != nil || len(members) != 2 {
		t.Fatalf("baseline: %v, %v", members, err)
	}
	_, err = p.ResolveSet("nope")
	if err == nil || !strings.Contains(err.Error(), "baseline") || !strings.Contains(err.Error(), path) {
		t.Fatalf("unknown set must list available sets and carry the path, got %v", err)
	}
}
```

Run: `go test ./internal/config/ -run TestResolveSet -v` — expected: compile FAIL (`ResolveSet` undefined).

- [ ] **Step 2: Implement ResolveSet**

Add to `internal/config/config.go`:

```go
// ResolveSet returns the member profile IDs of a profile set, in
// providers.yaml order.
func (p *Providers) ResolveSet(set string) ([]string, error) {
	members, ok := p.ProfileSets[set]
	if !ok {
		names := make([]string, 0, len(p.ProfileSets))
		for n := range p.ProfileSets {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%s: unknown profile set %q (available: %s)",
			p.source(), set, strings.Join(names, ", "))
	}
	return members, nil
}
```

Run: `go test ./internal/config/ -v` — expected: all PASS. Commit:

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: profile set resolution"
```

- [ ] **Step 3: Write the failing CLI tests**

In `cmd/splatter/cli_test.go`, extend `cliFakeProvider` with a pre-flight seam and add a per-provider fake installer:

```go
type cliFakeProvider struct {
	fail         bool
	preflightErr error
}
```

```go
func (f *cliFakeProvider) Preflight(req provider.Request) error { return f.preflightErr }
```

```go
// withFakeProvidersByName routes buildProvider through fakes keyed by
// the profile's provider name, so a fan can mix outcomes per profile.
func withFakeProvidersByName(t *testing.T, fakes map[string]provider.Provider) {
	t.Helper()
	orig := buildProvider
	buildProvider = func(prof config.Profile) (provider.Provider, error) {
		if f, ok := fakes[prof.Provider]; ok {
			return f, nil
		}
		return nil, fmt.Errorf("no fake for provider %q", prof.Provider)
	}
	t.Cleanup(func() { buildProvider = orig })
}
```

(add `"fmt"` to the test file's imports if absent). Then the tests:

```go
func TestFanSetSuccessJSON(t *testing.T) {
	withFakeProvidersByName(t, map[string]provider.Provider{
		"gemini": &cliFakeProvider{}, "openai": &cliFakeProvider{},
	})
	dir := setupGenWorkspace(t)
	out, err := runCLI(t, dir, "fan", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--set", "baseline", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Run   string `json:"run"`
		Calls []struct {
			Call    string `json:"call"`
			Profile string `json:"profile"`
			Failed  bool   `json:"failed"`
		} `json:"calls"`
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, out)
	}
	if res.Run != "r_0001" || res.Succeeded != 2 || res.Failed != 0 || len(res.Calls) != 2 {
		t.Fatalf("result: %+v", res)
	}
	if res.Calls[0].Call != "c_01" || res.Calls[1].Call != "c_02" ||
		res.Calls[0].Profile != "gemini-baseline" || res.Calls[1].Profile != "openai-baseline" {
		t.Fatalf("calls out of order: %+v", res.Calls)
	}
	if _, err := runCLI(t, dir, "validate"); err != nil {
		t.Fatalf("workspace must validate after fan: %v", err)
	}
}

func TestFanMixedFailureExitsZero(t *testing.T) {
	withFakeProvidersByName(t, map[string]provider.Provider{
		"gemini": &cliFakeProvider{}, "openai": &cliFakeProvider{fail: true},
	})
	dir := setupGenWorkspace(t)
	out, err := runCLI(t, dir, "fan", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--set", "baseline", "--json")
	if err != nil {
		t.Fatalf("one success means exit 0: %v", err)
	}
	var res struct {
		Succeeded int `json:"succeeded"`
		Failed    int `json:"failed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Succeeded != 1 || res.Failed != 1 {
		t.Fatalf("result: %+v", res)
	}
}

func TestFanAllFailedExitsOne(t *testing.T) {
	withFakeProvidersByName(t, map[string]provider.Provider{
		"gemini": &cliFakeProvider{fail: true}, "openai": &cliFakeProvider{fail: true},
	})
	dir := setupGenWorkspace(t)
	_, err := runCLI(t, dir, "fan", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--set", "baseline")
	if err == nil {
		t.Fatal("all-failed fan must exit nonzero")
	}
	var u usageErr
	var v validationErr
	if errors.As(err, &u) || errors.As(err, &v) {
		t.Fatalf("all-failed is a runtime error (exit 1), got %T", err)
	}
}

func TestFanPreflightFailureExitsOneWithNoTrace(t *testing.T) {
	withFakeProvidersByName(t, map[string]provider.Provider{
		"gemini": &cliFakeProvider{},
		"openai": &cliFakeProvider{preflightErr: &provider.Error{Stage: "config", Message: "missing OPENAI_API_KEY"}},
	})
	dir := setupGenWorkspace(t)
	_, err := runCLI(t, dir, "fan", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--set", "baseline")
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("want pre-flight error, got %v", err)
	}
	var u usageErr
	if errors.As(err, &u) {
		t.Fatalf("pre-flight failure is a runtime error (exit 1), got usageErr")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("pre-flight failure must leave no run dirs: %v", entries)
	}
}

func TestFanFlagMisuseIsUsageError(t *testing.T) {
	withFakeProvider(t, &cliFakeProvider{})
	dir := setupGenWorkspace(t)
	brief := "projects/gradient-descent/briefs/b_001.md"
	cases := [][]string{
		{"fan", "--brief", brief},                                              // neither
		{"fan", "--brief", brief, "--set", "baseline", "--profiles", "a"},      // both
		{"fan", "--brief", brief, "--set", "nope"},                             // unknown set
		{"fan", "--brief", brief, "--profiles", "nope"},                        // unknown profile
		{"fan", "--set", "baseline"},                                           // missing brief
		{"fan", "--brief", "projects/gradient-descent/briefs/nope.md", "--set", "baseline"}, // nonexistent brief
	}
	for _, args := range cases {
		_, err := runCLI(t, dir, args...)
		var u usageErr
		if !errors.As(err, &u) {
			t.Fatalf("%v: want usageErr, got %T: %v", args, err, err)
		}
	}
}
```

- [ ] **Step 4: Run them to verify they fail**

Run: `go test ./cmd/splatter/ -run TestFan -v`
Expected: FAIL — cobra reports `unknown command "fan"` (surfaced as an error from `runCLI`), or compile failure until the fakes are extended.

- [ ] **Step 5: Implement fan.go**

Create `cmd/splatter/fan.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/run"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

func newFanCmd() *cobra.Command {
	var briefPath, set, profilesCSV string
	var n int
	cmd := &cobra.Command{
		Use:   "fan",
		Short: "Fan one brief across multiple provider profiles in a single run",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if briefPath == "" {
				return usageErr{fmt.Errorf("required flag(s) \"brief\" not set")}
			}
			if (set == "") == (profilesCSV == "") {
				return usageErr{fmt.Errorf("exactly one of --set or --profiles required")}
			}
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
			var ids []string
			if set != "" {
				ids, err = providers.ResolveSet(set)
				if err != nil {
					return usageErr{err}
				}
			} else {
				for _, id := range strings.Split(profilesCSV, ",") {
					if id = strings.TrimSpace(id); id != "" {
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					return usageErr{fmt.Errorf("--profiles: no profile ids given")}
				}
			}
			resolved := make([]run.ResolvedProfile, 0, len(ids))
			for _, id := range ids {
				prof, err := providers.Resolve(id)
				if err != nil {
					return usageErr{err}
				}
				prov, err := buildProvider(prof)
				if err != nil {
					return err
				}
				resolved = append(resolved, run.ResolvedProfile{ID: id, Profile: prof, Provider: prov})
			}
			res, err := run.Fan(cmd.Context(), run.FanParams{
				Root: root, BriefPath: briefPath, Profiles: resolved,
				N: n, Harness: "splatter " + version, Pricing: pricing,
			})
			if err != nil {
				if os.IsNotExist(err) {
					return usageErr{err}
				}
				return err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s: %d succeeded, %d failed\n", res.Run, res.Succeeded, res.Failed)
			for _, c := range res.Calls {
				if c.Failed {
					fmt.Fprintf(&b, "  %s %s FAILED: %s (latency %dms)\n",
						c.Call, c.Profile, c.Error.Message, c.LatencyMS)
				} else {
					fmt.Fprintf(&b, "  %s %s: %d image(s), %s, latency %dms\n",
						c.Call, c.Profile, len(c.Images), costString(c.Cost), c.LatencyMS)
				}
			}
			if err := emit(cmd, res, b.String()); err != nil {
				return err
			}
			// Spec §5: exit 0 if at least one call succeeded, 1 only if all failed.
			if res.Succeeded == 0 {
				return fmt.Errorf("all %d call(s) failed", res.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&briefPath, "brief", "", "path to the brief markdown file (required)")
	cmd.Flags().StringVar(&set, "set", "", "profile set from providers.yaml")
	cmd.Flags().StringVar(&profilesCSV, "profiles", "", "comma-separated profile ids")
	cmd.Flags().IntVarP(&n, "n", "n", 1, "number of images to request per call")
	return cmd
}
```

In `cmd/splatter/root.go`, after `root.AddCommand(newGenCmd())` add:

```go
	root.AddCommand(newFanCmd())
```

- [ ] **Step 6: Run the tests**

Run: `go test ./cmd/splatter/ -v`
Expected: all PASS (fan tests plus every pre-existing gen/init/validate/status test).

- [ ] **Step 7: Commit**

```bash
git add cmd/splatter/fan.go cmd/splatter/root.go cmd/splatter/cli_test.go
git commit -m "feat: fan command with set/profile selection and spec exit semantics"
```

---

### Task 10: `splatter verdict` command

**Files:**
- Create: `cmd/splatter/verdict.go`
- Modify: `cmd/splatter/root.go` (register command)
- Test: `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `workspace.FindRun`, `workspace.ErrRunNotFound`, `workspace.AmbiguousRunError`, `workspace.ReadManifest` (Task 6), `schema.Verdict`, `schema.VerdictNote`, `fsio.AppendRecord`.
- Produces: `splatter verdict --run <id> [--keep ids] [--cull ids] [--note "<text>"]... [--session <label>] [--json]`. Appends one validated `schema.Verdict` to the owning project's `verdicts.jsonl`; `--json` emits the appended record.
- Note convention (spec-locked): a note matching `^([a-z0-9_]+):\s` is an image note for the captured ID (which must exist in the run's manifest, else usage error); any other note is run-level (`image: null`). Run-level notes therefore must not start with a `word:` prefix.
- Validation, all usage errors, nothing appended on any violation: every ID in keep/cull/notes exists in the run's manifest images; keep ∩ cull empty; at least one of keep/cull/note present.

- [ ] **Step 1: Write the failing tests**

Add to `cmd/splatter/cli_test.go`:

```go
// setupRunWorkspace fabricates a workspace with one completed run
// (r_0001, image c_01_0) via the real gen path.
func setupRunWorkspace(t *testing.T) string {
	t.Helper()
	withFakeProvider(t, &cliFakeProvider{})
	dir := setupGenWorkspace(t)
	if _, err := runCLI(t, dir, "gen", "--brief", "projects/gradient-descent/briefs/b_001.md",
		"--profile", "gemini-baseline"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerdictAppendsAndValidates(t *testing.T) {
	dir := setupRunWorkspace(t)
	out, err := runCLI(t, dir, "verdict", "--run", "r_0001",
		"--keep", "c_01_0",
		"--note", "c_01_0: tighten the glyph",
		"--note", "solid direction overall",
		"--session", "s3-check", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		V       int      `json:"v"`
		Type    string   `json:"type"`
		Run     string   `json:"run"`
		Session string   `json:"session"`
		Keep    []string `json:"keep"`
		Cull    []string `json:"cull"`
		Notes   []struct {
			Image *string `json:"image"`
			Text  string  `json:"text"`
		} `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, out)
	}
	if rec.V != 1 || rec.Type != "verdict" || rec.Run != "r_0001" || rec.Session != "s3-check" {
		t.Fatalf("record: %+v", rec)
	}
	if len(rec.Keep) != 1 || rec.Keep[0] != "c_01_0" || len(rec.Cull) != 0 {
		t.Fatalf("keep/cull: %+v", rec)
	}
	if len(rec.Notes) != 2 || rec.Notes[0].Image == nil || *rec.Notes[0].Image != "c_01_0" ||
		rec.Notes[0].Text != "tighten the glyph" || rec.Notes[1].Image != nil {
		t.Fatalf("notes: %+v", rec.Notes)
	}
	// empty cull serializes as [], never null
	raw, err := os.ReadFile(filepath.Join(dir, "projects", "gradient-descent", "verdicts.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"cull":[]`) {
		t.Fatalf("empty cull must serialize as []: %s", raw)
	}
	// the appended line decodes through the schema and validate passes
	if _, err := runCLI(t, dir, "validate"); err != nil {
		t.Fatalf("validate must pass after verdict: %v", err)
	}
}

func TestVerdictSessionDefaultsToLocalDate(t *testing.T) {
	dir := setupRunWorkspace(t)
	out, err := runCLI(t, dir, "verdict", "--run", "r_0001", "--keep", "c_01_0", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatal(err)
	}
	if want := time.Now().Format("2006-01-02"); rec.Session != want {
		t.Fatalf("session default: want %s, got %s", want, rec.Session)
	}
}

func TestVerdictRejectionsAppendNothing(t *testing.T) {
	dir := setupRunWorkspace(t)
	cases := [][]string{
		{"verdict", "--run", "r_0001", "--keep", "c_99_9"},                       // unknown keep id
		{"verdict", "--run", "r_0001", "--cull", "c_99_9"},                       // unknown cull id
		{"verdict", "--run", "r_0001", "--note", "c_99_9: unknown image note"},   // unknown note id
		{"verdict", "--run", "r_0001", "--keep", "c_01_0", "--cull", "c_01_0"},   // overlap
		{"verdict", "--run", "r_0001"},                                           // empty verdict
		{"verdict", "--run", "r_0099", "--keep", "c_01_0"},                       // unknown run
		{"verdict", "--keep", "c_01_0"},                                          // missing --run
	}
	for _, args := range cases {
		_, err := runCLI(t, dir, args...)
		var u usageErr
		if !errors.As(err, &u) {
			t.Fatalf("%v: want usageErr, got %T: %v", args, err, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "gradient-descent", "verdicts.jsonl")); !os.IsNotExist(err) {
		t.Fatal("rejected verdicts must append nothing")
	}
}

func TestVerdictAmbiguousRunIsUsageError(t *testing.T) {
	dir := setupRunWorkspace(t)
	if _, err := runCLI(t, dir, "init", "other"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "projects", "other", "runs", "r_0001"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := runCLI(t, dir, "verdict", "--run", "r_0001", "--keep", "c_01_0")
	var u usageErr
	if !errors.As(err, &u) || !strings.Contains(err.Error(), "gradient-descent") ||
		!strings.Contains(err.Error(), "other") {
		t.Fatalf("ambiguous run: want usageErr naming both projects, got %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/splatter/ -run TestVerdict -v`
Expected: FAIL — `unknown command "verdict"`.

- [ ] **Step 3: Implement verdict.go**

Create `cmd/splatter/verdict.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

// imageNoteRe is the spec-locked convention: a leading "<id>: " makes an
// image note; a note without the prefix is run-level (image: null).
var imageNoteRe = regexp.MustCompile(`^([a-z0-9_]+):\s`)

func parseNote(raw string) schema.VerdictNote {
	m := imageNoteRe.FindStringSubmatch(raw)
	if m == nil {
		return schema.VerdictNote{Image: nil, Text: raw}
	}
	id := m[1]
	return schema.VerdictNote{Image: &id, Text: strings.TrimSpace(raw[len(m[0]):])}
}

func splitIDs(csv string) []string {
	var ids []string
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			ids = append(ids, s)
		}
	}
	return ids
}

// locateRun maps FindRun's distinguishable failures to usage errors.
func locateRun(root, runID string) (string, error) {
	project, err := workspace.FindRun(root, runID)
	if err != nil {
		var amb *workspace.AmbiguousRunError
		if errors.Is(err, workspace.ErrRunNotFound) || errors.As(err, &amb) {
			return "", usageErr{err}
		}
		return "", err
	}
	return project, nil
}

func newVerdictCmd() *cobra.Command {
	var runID, keepCSV, cullCSV, session string
	var noteArgs []string
	cmd := &cobra.Command{
		Use:   "verdict",
		Short: "Record keep/cull decisions and notes for a run",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runID == "" {
				return usageErr{fmt.Errorf("required flag(s) \"run\" not set")}
			}
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
			project, err := locateRun(root, runID)
			if err != nil {
				return err
			}
			runDir := filepath.Join(root, "projects", project, "runs", runID)
			_, calls, err := workspace.ReadManifest(runDir)
			if err != nil {
				return err
			}
			known := map[string]bool{}
			for _, c := range calls {
				for _, img := range c.Images {
					known[img.ID] = true
				}
			}

			keep, cull := splitIDs(keepCSV), splitIDs(cullCSV)
			notes := make([]schema.VerdictNote, 0, len(noteArgs))
			for _, raw := range noteArgs {
				notes = append(notes, parseNote(raw))
			}

			// Validation before append: any violation is a usage error and
			// nothing is appended.
			if len(keep)+len(cull)+len(notes) == 0 {
				return usageErr{fmt.Errorf("empty verdict: at least one of --keep, --cull, --note required")}
			}
			inCull := map[string]bool{}
			for _, id := range cull {
				inCull[id] = true
			}
			for _, id := range keep {
				if inCull[id] {
					return usageErr{fmt.Errorf("image %s in both --keep and --cull", id)}
				}
			}
			checkKnown := func(id string) error {
				if !known[id] {
					return usageErr{fmt.Errorf("image %s not in run %s manifest", id, runID)}
				}
				return nil
			}
			for _, id := range keep {
				if err := checkKnown(id); err != nil {
					return err
				}
			}
			for _, id := range cull {
				if err := checkKnown(id); err != nil {
					return err
				}
			}
			for _, n := range notes {
				if n.Image != nil {
					if err := checkKnown(*n.Image); err != nil {
						return err
					}
				}
			}

			if session == "" {
				session = time.Now().Format("2006-01-02") // local calendar date, spec §4.3
			}
			if keep == nil {
				keep = []string{}
			}
			if cull == nil {
				cull = []string{}
			}
			rec := schema.Verdict{
				V: 1, Type: "verdict", Run: runID,
				TS:      time.Now().UTC().Truncate(time.Second),
				Session: session, Keep: keep, Cull: cull, Notes: notes,
			}
			if err := fsio.AppendRecord(filepath.Join(root, "projects", project, "verdicts.jsonl"), rec); err != nil {
				return err
			}
			human := fmt.Sprintf("verdict appended to projects/%s/verdicts.jsonl: keep %d, cull %d, note(s) %d\n",
				project, len(rec.Keep), len(rec.Cull), len(rec.Notes))
			return emit(cmd, rec, human)
		},
	}
	cmd.Flags().StringVar(&runID, "run", "", "run id, e.g. r_0001 (required)")
	cmd.Flags().StringVar(&keepCSV, "keep", "", "comma-separated image ids to keep")
	cmd.Flags().StringVar(&cullCSV, "cull", "", "comma-separated image ids to cull")
	cmd.Flags().StringArrayVar(&noteArgs, "note", nil,
		`note; "<image_id>: text" attaches to an image, plain text is run-level`)
	cmd.Flags().StringVar(&session, "session", "", "session label (default: local calendar date)")
	return cmd
}
```

In `cmd/splatter/root.go` add:

```go
	root.AddCommand(newVerdictCmd())
```

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/splatter/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/splatter/verdict.go cmd/splatter/root.go cmd/splatter/cli_test.go
git commit -m "feat: verdict command with pre-append validation"
```

---

### Task 11: internal/sheet — `Build` (data assembly)

**Files:**
- Create: `internal/sheet/sheet.go`
- Test: `internal/sheet/sheet_test.go`

**Interfaces:**
- Consumes: `workspace.ReadManifest` (Task 6), `schema.ParseBrief`, `schema.DecodeVerdictLine`, `schema.Critique`.
- Produces (used by Task 12's template and Task 13's command):

```go
type Score struct{ Name string; Value int }

type CritiqueInfo struct {
	Verdict string
	Scores  []Score // sorted by name for deterministic render
	Reason  string
}

type CardImage struct {
	ID       string
	File     string // relative: images/c_01_0.png
	W, H     int
	Status   string // "keep" | "cull" | "" — from verdicts, later records win
	Critique *CritiqueInfo
}

type Card struct {
	Call          string
	Model         string // model_returned, falling back to model_requested
	Profile       string
	N             int
	AspectText    string // "square → 1:1"; failed calls show the request aspect only
	SeedText      string // "—" when null
	LatencyMS     int64
	CostText      string // "$0.0500 (table:2026-07-25.0)" | "cost unavailable"
	Failed        bool
	ErrStage      string
	ErrHTTPStatus int
	ErrMessage    string
	Images        []CardImage
}

type Group struct {
	Provider string
	Cards    []Card
}

type Footer struct {
	ImageCount int
	CostText   string // summed non-null USD, "(N call(s) cost unavailable)" appended when N > 0
	LatencyMS  int64
	ParentLink string // "../<parent_run>/sheet.html" when parent_run set, else ""
}

type Data struct {
	Project    string
	Run        string
	Iteration  int
	BriefID    string
	Concept    string
	BriefNote  string // set instead of BriefID/Concept when the brief is unreadable: hash + note
	Groups     []Group // provider groups in first-appearance order
	Footer     Footer
	VerdictCmd string  // "splatter verdict --run r_0001 --keep c_01_0,c_02_0"
}

func Build(root, project, runID string) (*Data, error)
```

- [ ] **Step 1: Write the failing tests**

Create `internal/sheet/sheet_test.go`:

```go
package sheet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
)

const sheetBrief = `---
id: b_001
project: p1
concept: contour-line mountain range
aspect: square
---
Sparse and geometric.
`

func usd(v float64) *float64 { return &v }

// fixtureRun builds projects/p1/runs/r_0002 with one successful gemini
// call (two images), one failed openai call, a parent run reference,
// verdicts (with a later record overriding an earlier one), and a
// critique for one image.
func fixtureRun(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	if _, err := workspace.ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ScaffoldProject(root, "p1"); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "projects", "p1")
	if err := os.WriteFile(filepath.Join(proj, "briefs", "b_001.md"), []byte(sheetBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(proj, "runs", "r_0002")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	parent, rel := "r_0001", "textual-refinement-of"
	header := schema.RunHeader{
		V: 1, Type: "run", Run: "r_0002", Project: "p1", Brief: "briefs/b_001.md",
		BriefSHA256: schema.BriefSHA256([]byte(sheetBrief)), Iteration: 2,
		ParentRun: &parent, Relationship: &rel, Created: now, Harness: "splatter test",
	}
	success := schema.CallRecord{
		V: 1, Type: "call", Run: "r_0002", Call: "c_01", TS: now,
		Provider: "gemini", ModelRequested: "g-req", ModelReturned: "g-ret",
		Profile: "gemini-baseline", Operation: "generate",
		Request:  schema.CallRequest{Prompt: "p", N: 2, Aspect: "square"},
		Response: schema.CallResponse{LatencyMS: 100, HTTPStatus: 200},
		Cost:     schema.Cost{USD: usd(0.078), Source: "table:2026-07-25.0"},
		Images: []schema.ImageRef{
			{ID: "c_01_0", File: "images/c_01_0.png", SHA256: "aa", W: 4, H: 4, AspectActual: "1:1"},
			{ID: "c_01_1", File: "images/c_01_1.png", SHA256: "bb", W: 4, H: 4, AspectActual: "1:1"},
		},
		Raw: "raw/c_01.json",
	}
	failed := schema.CallRecord{
		V: 1, Type: "call", Run: "r_0002", Call: "c_02", TS: now,
		Provider: "openai", ModelRequested: "o-req", Profile: "openai-baseline", Operation: "generate",
		Request:  schema.CallRequest{Prompt: "p", N: 2, Aspect: "square"},
		Response: schema.CallResponse{LatencyMS: 40, HTTPStatus: 429},
		Cost:     schema.Cost{Source: "unavailable"},
		Images:   []schema.ImageRef{},
		Error:    &schema.CallError{Stage: "request", HTTPStatus: 429, Message: "rate limited"},
	}
	mp := filepath.Join(runDir, "manifest.jsonl")
	for _, rec := range []any{header, success, failed} {
		if err := fsio.AppendRecord(mp, rec); err != nil {
			t.Fatal(err)
		}
	}
	// two verdicts: the later one flips c_01_1 from keep to cull
	vp := filepath.Join(proj, "verdicts.jsonl")
	verdicts := []schema.Verdict{
		{V: 1, Type: "verdict", Run: "r_0002", TS: now, Session: "d1",
			Keep: []string{"c_01_0", "c_01_1"}, Cull: []string{}, Notes: []schema.VerdictNote{}},
		{V: 1, Type: "verdict", Run: "r_0002", TS: now, Session: "d2",
			Keep: []string{}, Cull: []string{"c_01_1"}, Notes: []schema.VerdictNote{}},
	}
	for _, v := range verdicts {
		if err := fsio.AppendRecord(vp, v); err != nil {
			t.Fatal(err)
		}
	}
	critique := `{"v":1,"run":"r_0002","rubric":"rubric_v1","items":[{"image":"c_01_0","verdict":"keep","scores":{"concept_fit":4,"novelty":3},"reason":"strong lines"}]}`
	if err := os.WriteFile(filepath.Join(proj, "critiques", "r_0002.json"), []byte(critique), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBuildAssemblesRenderReadyData(t *testing.T) {
	root := fixtureRun(t)
	d, err := Build(root, "p1", "r_0002")
	if err != nil {
		t.Fatal(err)
	}
	if d.Project != "p1" || d.Run != "r_0002" || d.Iteration != 2 {
		t.Fatalf("header: %+v", d)
	}
	if d.BriefID != "b_001" || d.Concept != "contour-line mountain range" || d.BriefNote != "" {
		t.Fatalf("brief: %+v", d)
	}
	// provider groups in first-appearance order
	if len(d.Groups) != 2 || d.Groups[0].Provider != "gemini" || d.Groups[1].Provider != "openai" {
		t.Fatalf("groups: %+v", d.Groups)
	}
	card := d.Groups[0].Cards[0]
	if card.Model != "g-ret" || card.AspectText != "square → 1:1" || card.SeedText != "—" ||
		!strings.Contains(card.CostText, "table:2026-07-25.0") {
		t.Fatalf("success card: %+v", card)
	}
	// per-image status: later verdict record wins
	if card.Images[0].Status != "keep" || card.Images[1].Status != "cull" {
		t.Fatalf("statuses: %+v", card.Images)
	}
	// critique joined per image, scores sorted by name
	crit := card.Images[0].Critique
	if crit == nil || crit.Verdict != "keep" || len(crit.Scores) != 2 ||
		crit.Scores[0].Name != "concept_fit" || crit.Scores[1].Name != "novelty" {
		t.Fatalf("critique: %+v", crit)
	}
	if card.Images[1].Critique != nil {
		t.Fatal("uncritiqued image must have nil critique")
	}
	// failed call: model badge falls back to model_requested
	fc := d.Groups[1].Cards[0]
	if !fc.Failed || fc.Model != "o-req" || fc.ErrStage != "request" ||
		fc.ErrHTTPStatus != 429 || fc.ErrMessage != "rate limited" {
		t.Fatalf("failed card: %+v", fc)
	}
	// footer: totals over both calls, unavailable count noted, parent link
	if d.Footer.ImageCount != 2 || d.Footer.LatencyMS != 140 {
		t.Fatalf("footer: %+v", d.Footer)
	}
	if !strings.Contains(d.Footer.CostText, "$0.0780") || !strings.Contains(d.Footer.CostText, "1 call(s) cost unavailable") {
		t.Fatalf("footer cost: %q", d.Footer.CostText)
	}
	if d.Footer.ParentLink != "../r_0001/sheet.html" {
		t.Fatalf("parent link: %q", d.Footer.ParentLink)
	}
	if d.VerdictCmd != "splatter verdict --run r_0002 --keep c_01_0,c_01_1" {
		t.Fatalf("verdict cmd: %q", d.VerdictCmd)
	}
}

func TestBuildMissingBriefRendersHashNote(t *testing.T) {
	root := fixtureRun(t)
	if err := os.Remove(filepath.Join(root, "projects", "p1", "briefs", "b_001.md")); err != nil {
		t.Fatal(err)
	}
	d, err := Build(root, "p1", "r_0002")
	if err != nil {
		t.Fatal(err)
	}
	if d.BriefID != "" || d.BriefNote == "" ||
		!strings.Contains(d.BriefNote, schema.BriefSHA256([]byte(sheetBrief))) {
		t.Fatalf("missing brief must produce a hash note, got %+v", d)
	}
}

func TestBuildNoVerdictsNoCritique(t *testing.T) {
	root := fixtureRun(t)
	if err := os.Remove(filepath.Join(root, "projects", "p1", "verdicts.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "projects", "p1", "critiques", "r_0002.json")); err != nil {
		t.Fatal(err)
	}
	d, err := Build(root, "p1", "r_0002")
	if err != nil {
		t.Fatal(err)
	}
	img := d.Groups[0].Cards[0].Images[0]
	if img.Status != "" || img.Critique != nil {
		t.Fatalf("absent files must yield empty status and nil critique: %+v", img)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/sheet/ -v`
Expected: FAIL to compile — package `sheet` doesn't exist yet. (Create `internal/sheet/` as part of Step 1.)

- [ ] **Step 3: Implement Build**

Create `internal/sheet/sheet.go` (Render and the template land in Task 12; this file compiles without them):

```go
// Package sheet builds the static review surface for a run: one
// self-contained HTML file in the run directory. Inline CSS, no
// JavaScript, no external assets; all image references relative. The
// sheet is a derived file, regenerable at any time.
package sheet

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
)

type Score struct {
	Name  string
	Value int
}

type CritiqueInfo struct {
	Verdict string
	Scores  []Score // sorted by name for deterministic render
	Reason  string
}

type CardImage struct {
	ID       string
	File     string // relative: images/c_01_0.png
	W, H     int
	Status   string // "keep" | "cull" | "" — from verdicts, later records win
	Critique *CritiqueInfo
}

type Card struct {
	Call          string
	Model         string // model_returned, falling back to model_requested
	Profile       string
	N             int
	AspectText    string
	SeedText      string
	LatencyMS     int64
	CostText      string
	Failed        bool
	ErrStage      string
	ErrHTTPStatus int
	ErrMessage    string
	Images        []CardImage
}

type Group struct {
	Provider string
	Cards    []Card
}

type Footer struct {
	ImageCount int
	CostText   string
	LatencyMS  int64
	ParentLink string
}

type Data struct {
	Project    string
	Run        string
	Iteration  int
	BriefID    string
	Concept    string
	BriefNote  string
	Groups     []Group
	Footer     Footer
	VerdictCmd string
}

const maxLineBytes = 1024 * 1024

// Build reads a run's manifest, all verdict records for the run, and its
// critique file if present, assembling a render-ready Data.
func Build(root, project, runID string) (*Data, error) {
	projDir := filepath.Join(root, "projects", project)
	runDir := filepath.Join(projDir, "runs", runID)
	header, calls, err := workspace.ReadManifest(runDir)
	if err != nil {
		return nil, err
	}
	status, err := imageStatuses(filepath.Join(projDir, "verdicts.jsonl"), runID)
	if err != nil {
		return nil, err
	}
	critiques, err := imageCritiques(filepath.Join(projDir, "critiques", runID+".json"))
	if err != nil {
		return nil, err
	}

	d := &Data{Project: header.Project, Run: header.Run, Iteration: header.Iteration}
	if briefBytes, err := os.ReadFile(filepath.Join(projDir, filepath.FromSlash(header.Brief))); err == nil {
		if meta, _, perr := schema.ParseBrief(briefBytes); perr == nil {
			d.BriefID, d.Concept = meta.ID, meta.Concept
		}
	}
	if d.BriefID == "" {
		d.BriefNote = fmt.Sprintf("brief %s not available locally (sha256 %s)", header.Brief, header.BriefSHA256)
	}

	groupIdx := map[string]int{}
	var totalUSD float64
	var unavailable int
	var totalLatency int64
	var allIDs []string
	for _, c := range calls {
		card := Card{
			Call: c.Call, Model: c.ModelReturned, Profile: c.Profile,
			N: c.Request.N, AspectText: c.Request.Aspect, SeedText: "—",
			LatencyMS: c.Response.LatencyMS, CostText: costText(c.Cost),
		}
		if card.Model == "" {
			card.Model = c.ModelRequested
		}
		if c.Request.Seed != nil {
			card.SeedText = fmt.Sprintf("%d", *c.Request.Seed)
		}
		if len(c.Images) > 0 {
			card.AspectText = c.Request.Aspect + " → " + c.Images[0].AspectActual
		}
		if c.Error != nil {
			card.Failed = true
			card.ErrStage, card.ErrHTTPStatus, card.ErrMessage = c.Error.Stage, c.Error.HTTPStatus, c.Error.Message
		}
		for _, img := range c.Images {
			ci := CardImage{ID: img.ID, File: img.File, W: img.W, H: img.H, Status: status[img.ID]}
			if info, ok := critiques[img.ID]; ok {
				ci.Critique = info
			}
			card.Images = append(card.Images, ci)
			allIDs = append(allIDs, img.ID)
		}
		if c.Cost.USD != nil {
			totalUSD += *c.Cost.USD
		} else {
			unavailable++
		}
		totalLatency += c.Response.LatencyMS

		idx, ok := groupIdx[c.Provider]
		if !ok {
			idx = len(d.Groups)
			groupIdx[c.Provider] = idx
			d.Groups = append(d.Groups, Group{Provider: c.Provider})
		}
		d.Groups[idx].Cards = append(d.Groups[idx].Cards, card)
	}

	d.Footer = Footer{ImageCount: len(allIDs), LatencyMS: totalLatency}
	d.Footer.CostText = fmt.Sprintf("$%.4f", totalUSD)
	if unavailable > 0 {
		d.Footer.CostText += fmt.Sprintf(" (%d call(s) cost unavailable)", unavailable)
	}
	if header.ParentRun != nil {
		d.Footer.ParentLink = "../" + *header.ParentRun + "/sheet.html"
	}
	d.VerdictCmd = fmt.Sprintf("splatter verdict --run %s --keep %s", header.Run, strings.Join(allIDs, ","))
	return d, nil
}

// imageStatuses folds every verdict record for the run; later records
// win per image. An absent verdicts.jsonl is an empty map, not an error.
func imageStatuses(path, runID string) (map[string]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	status := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
	for sc.Scan() {
		v, err := schema.DecodeVerdictLine(sc.Bytes())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if v.Run != runID {
			continue
		}
		for _, id := range v.Keep {
			status[id] = "keep"
		}
		for _, id := range v.Cull {
			status[id] = "cull"
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return status, nil
}

// imageCritiques loads critiques/<run>.json when present; scores sort by
// name so rendering is deterministic.
func imageCritiques(path string) (map[string]*CritiqueInfo, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]*CritiqueInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var crit schema.Critique
	if err := json.Unmarshal(data, &crit); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]*CritiqueInfo{}
	for _, it := range crit.Items {
		info := &CritiqueInfo{Verdict: it.Verdict, Reason: it.Reason}
		names := make([]string, 0, len(it.Scores))
		for n := range it.Scores {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			info.Scores = append(info.Scores, Score{Name: n, Value: it.Scores[n]})
		}
		out[it.Image] = info
	}
	return out, nil
}

func costText(c schema.Cost) string {
	if c.USD == nil {
		return "cost unavailable"
	}
	return fmt.Sprintf("$%.4f (%s)", *c.USD, c.Source)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/sheet/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sheet/sheet.go internal/sheet/sheet_test.go
git commit -m "feat: sheet data assembly from manifest, verdicts, and critique"
```

---

### Task 12: internal/sheet — embedded template + `Render`

**Files:**
- Create: `internal/sheet/sheet.tmpl.html`
- Modify: `internal/sheet/sheet.go` (embed + Render)
- Test: `internal/sheet/render_test.go`

**Interfaces:**
- Consumes: `Data` and friends from Task 11.
- Produces: `func Render(w io.Writer, d *Data) error` — one embedded `html/template`, inline CSS, no JavaScript, no external assets. Thumbnails are CSS-scaled full-res `<img>` tags wrapped in links to the same file; all hrefs relative.

- [ ] **Step 1: Write the failing golden-substring tests**

Create `internal/sheet/render_test.go`:

```go
package sheet

import (
	"bytes"
	"strings"
	"testing"
)

func renderFixture(t *testing.T) string {
	t.Helper()
	root := fixtureRun(t)
	d, err := Build(root, "p1", "r_0002")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Render(&buf, d); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestRenderGoldenSubstrings(t *testing.T) {
	html := renderFixture(t)
	for _, want := range []string{
		// header: project, run, iteration, brief id + concept
		"p1 / r_0002",
		"iteration 2",
		"b_001",
		"contour-line mountain range",
		// provider grouping
		"<h2>gemini</h2>",
		"<h2>openai</h2>",
		// thumbnail: full-res img hyperlinked to itself, relative path
		`<a href="images/c_01_0.png"><img src="images/c_01_0.png"`,
		// model badge and params
		"g-ret",
		"square → 1:1",
		"seed —",
		// cost with source tag
		"table:2026-07-25.0",
		// per-image keep/cull status from verdicts (later record wins)
		`<span class="status-keep">keep</span>`,
		`<span class="status-cull">cull</span>`,
		// critique verdict + scores
		"critique: keep",
		"concept_fit 4",
		"strong lines",
		// failed call renders as an error card inside its provider group
		"FAILED at request (http 429): rate limited",
		// footer totals and parent link
		"2 image(s)",
		"$0.0780",
		"1 call(s) cost unavailable",
		`<a href="../r_0001/sheet.html">`,
		// copyable verdict command block
		"<pre>splatter verdict --run r_0002 --keep c_01_0,c_01_1</pre>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered sheet missing %q", want)
		}
	}
}

func TestRenderIsSelfContained(t *testing.T) {
	html := renderFixture(t)
	for _, banned := range []string{"<script", "http://", "https://", "src=\"/", "href=\"/"} {
		if strings.Contains(html, banned) {
			t.Errorf("sheet must be self-contained; found %q", banned)
		}
	}
}

func TestRenderDeterministic(t *testing.T) {
	if renderFixture(t) != renderFixture(t) {
		t.Fatal("rendering the same run twice must produce identical bytes")
	}
}

func TestRenderMissingBriefNote(t *testing.T) {
	root := fixtureRun(t)
	d, err := Build(root, "p1", "r_0002")
	if err != nil {
		t.Fatal(err)
	}
	d.BriefID, d.Concept = "", ""
	d.BriefNote = "brief briefs/b_001.md not available locally (sha256 deadbeef)"
	var buf bytes.Buffer
	if err := Render(&buf, d); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "sha256 deadbeef") {
		t.Fatal("missing brief must render the hash note")
	}
}
```

Note: `TestRenderDeterministic` calls `renderFixture` twice against two separate fixture roots — identical output also proves nothing machine-specific (paths, timestamps) leaks into the sheet.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/sheet/ -run TestRender -v`
Expected: FAIL to compile — `Render` undefined.

- [ ] **Step 3: Write the template**

Create `internal/sheet/sheet.tmpl.html`:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Project}} {{.Run}} — splatter sheet</title>
<style>
  body { font-family: -apple-system, "Segoe UI", Helvetica, Arial, sans-serif;
         margin: 2rem; background: #fafafa; color: #222; }
  h1 { margin-bottom: .25rem; }
  .meta { color: #555; margin-top: 0; }
  h2 { border-bottom: 1px solid #ddd; padding-bottom: .25rem; margin-top: 2rem; }
  .cards { display: flex; flex-wrap: wrap; gap: 1rem; align-items: flex-start; }
  .card { border: 1px solid #ddd; border-radius: 4px; background: #fff;
          padding: .75rem; width: 340px; }
  .card img { max-width: 100%; height: auto; display: block; border: 1px solid #eee; }
  .badge { font-weight: 600; margin: 0 0 .5rem; }
  .params, .evidence { color: #555; font-size: .85rem; margin: .25rem 0; }
  .imgmeta { font-size: .85rem; margin: .25rem 0 .75rem; }
  .critique { font-size: .85rem; color: #444; margin: 0 0 .75rem; }
  .error { color: #a00; font-weight: 600; }
  .status-keep { color: #070; font-weight: 700; }
  .status-cull { color: #a00; font-weight: 700; }
  pre { background: #f0f0f0; padding: .75rem; overflow-x: auto; }
  footer { margin-top: 2.5rem; border-top: 1px solid #ddd; padding-top: 1rem; }
</style>
</head>
<body>
<h1>{{.Project}} / {{.Run}}</h1>
<p class="meta">iteration {{.Iteration}}{{if .BriefID}} · brief {{.BriefID}} — {{.Concept}}{{else}} · {{.BriefNote}}{{end}}</p>
{{range .Groups}}
<h2>{{.Provider}}</h2>
<div class="cards">
{{range .Cards}}
  <div class="card">
    <p class="badge">{{.Model}} <small>({{.Profile}}, {{.Call}})</small></p>
{{if .Failed}}
    <p class="error">FAILED at {{.ErrStage}}{{if .ErrHTTPStatus}} (http {{.ErrHTTPStatus}}){{end}}: {{.ErrMessage}}</p>
    <p class="evidence">latency {{.LatencyMS}} ms</p>
{{else}}
{{range .Images}}
    <a href="{{.File}}"><img src="{{.File}}" alt="{{.ID}}"></a>
    <p class="imgmeta">{{.ID}} · {{.W}}×{{.H}}{{if .Status}} · <span class="status-{{.Status}}">{{.Status}}</span>{{end}}</p>
{{with .Critique}}
    <p class="critique">critique: {{.Verdict}}{{range .Scores}} · {{.Name}} {{.Value}}{{end}}<br>{{.Reason}}</p>
{{end}}
{{end}}
    <p class="params">n={{.N}} · aspect {{.AspectText}} · seed {{.SeedText}}</p>
    <p class="evidence">latency {{.LatencyMS}} ms · {{.CostText}}</p>
{{end}}
  </div>
{{end}}
</div>
{{end}}
<footer>
  <p>{{.Footer.ImageCount}} image(s) · total {{.Footer.CostText}} · total latency {{.Footer.LatencyMS}} ms</p>
{{if .Footer.ParentLink}}
  <p><a href="{{.Footer.ParentLink}}">parent run</a></p>
{{end}}
  <p>Record a verdict (edit IDs into --cull as needed):</p>
  <pre>{{.VerdictCmd}}</pre>
</footer>
</body>
</html>
```

- [ ] **Step 4: Add embed + Render to sheet.go**

In `internal/sheet/sheet.go`, add three imports — `"html/template"`, `"io"`, and the blank import `_ "embed"` (the directive needs the package linked in; nothing calls its API) — then the embed directive at package level, after the import block:

```go
//go:embed sheet.tmpl.html
var tmplSrc string

var tmpl = template.Must(template.New("sheet").Parse(tmplSrc))
```

Then:

```go
// Render executes the embedded template. The output is self-contained:
// inline CSS, no JavaScript, relative image references only.
func Render(w io.Writer, d *Data) error {
	return tmpl.Execute(w, d)
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/sheet/ -v`
Expected: all PASS. If a golden substring mismatches on whitespace, fix the TEMPLATE (not the test) unless the test's expectation contradicts the Data contract above.

- [ ] **Step 6: Commit**

```bash
git add internal/sheet/sheet.tmpl.html internal/sheet/sheet.go internal/sheet/render_test.go
git commit -m "feat: self-contained sheet template and renderer"
```

---

### Task 13: `splatter sheet` command + browser-open helper

**Files:**
- Create: `cmd/splatter/sheet.go`
- Create: `cmd/splatter/open.go`
- Modify: `cmd/splatter/root.go` (register command)
- Test: `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `sheet.Build`, `sheet.Render`, `fsio.ReplaceFile`, `locateRun` (Task 10), `emit`, `usageErr`.
- Produces: `splatter sheet --run <id> [--open] [--json]` — writes `sheet.html` into the run directory, prints the path (`--json`: `{"run":..., "path":...}`). `openInBrowser(path string) error` in `open.go` is the codebase's single permitted `runtime.GOOS` branch. `--open` is excluded from automated tests; verified live (macOS) and via the Windows checklist.

- [ ] **Step 1: Write the failing tests**

Add to `cmd/splatter/cli_test.go`:

```go
func TestSheetWritesHTMLIntoRunDir(t *testing.T) {
	dir := setupRunWorkspace(t)
	out, err := runCLI(t, dir, "sheet", "--run", "r_0001", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Run  string `json:"run"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, out)
	}
	want := filepath.Join(dir, "projects", "gradient-descent", "runs", "r_0001", "sheet.html")
	if res.Run != "r_0001" || res.Path != want {
		t.Fatalf("result: %+v (want path %s)", res, want)
	}
	html, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `href="images/c_01_0.png"`) {
		t.Fatalf("sheet must reference images relatively:\n%s", html)
	}
	// sheet is a derived file: regeneration is idempotent
	if _, err := runCLI(t, dir, "sheet", "--run", "r_0001"); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(html, again) {
		t.Fatal("regenerating an unchanged run must reproduce identical bytes")
	}
	// a written sheet never breaks validate
	if _, err := runCLI(t, dir, "validate"); err != nil {
		t.Fatalf("validate must pass with sheet.html present: %v", err)
	}
}

func TestSheetReflectsVerdicts(t *testing.T) {
	dir := setupRunWorkspace(t)
	if _, err := runCLI(t, dir, "verdict", "--run", "r_0001", "--keep", "c_01_0"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "sheet", "--run", "r_0001"); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(dir, "projects", "gradient-descent", "runs", "r_0001", "sheet.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `<span class="status-keep">keep</span>`) {
		t.Fatal("sheet must show per-image keep status after a verdict")
	}
}

func TestSheetUsageErrors(t *testing.T) {
	dir := setupRunWorkspace(t)
	for _, args := range [][]string{
		{"sheet"},                          // missing --run
		{"sheet", "--run", "r_0099"},       // unknown run
	} {
		_, err := runCLI(t, dir, args...)
		var u usageErr
		if !errors.As(err, &u) {
			t.Fatalf("%v: want usageErr, got %T: %v", args, err, err)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./cmd/splatter/ -run TestSheet -v`
Expected: FAIL — `unknown command "sheet"`.

- [ ] **Step 3: Implement open.go**

Create `cmd/splatter/open.go`:

```go
package main

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openInBrowser launches the OS default browser on path. This is the
// codebase's single permitted runtime.GOOS branch (spec §8); no other
// GOOS conditional may exist anywhere.
func openInBrowser(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	default:
		return fmt.Errorf("browser-open not supported on %s", runtime.GOOS)
	}
}
```

- [ ] **Step 4: Implement sheet.go**

Create `cmd/splatter/sheet.go`:

```go
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/sheet"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type sheetResult struct {
	Run  string `json:"run"`
	Path string `json:"path"`
}

func newSheetCmd() *cobra.Command {
	var runID string
	var open bool
	cmd := &cobra.Command{
		Use:   "sheet",
		Short: "Generate the static HTML review sheet for a run",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runID == "" {
				return usageErr{fmt.Errorf("required flag(s) \"run\" not set")}
			}
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
			project, err := locateRun(root, runID)
			if err != nil {
				return err
			}
			d, err := sheet.Build(root, project, runID)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := sheet.Render(&buf, d); err != nil {
				return err
			}
			path := filepath.Join(root, "projects", project, "runs", runID, "sheet.html")
			if err := fsio.ReplaceFile(path, buf.Bytes()); err != nil {
				return err
			}
			if err := emit(cmd, sheetResult{Run: runID, Path: path}, path+"\n"); err != nil {
				return err
			}
			if open {
				return openInBrowser(path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run", "", "run id, e.g. r_0001 (required)")
	cmd.Flags().BoolVar(&open, "open", false, "open the sheet in the default browser")
	return cmd
}
```

In `cmd/splatter/root.go` add:

```go
	root.AddCommand(newSheetCmd())
```

- [ ] **Step 5: Run the tests and the GOOS invariant check**

Run: `go test ./cmd/splatter/ -v`
Expected: all PASS.

Run: `grep -rn "runtime.GOOS" --include="*.go" . | grep -v _test.go`
Expected: exactly one hit — `cmd/splatter/open.go`.

- [ ] **Step 6: Commit**

```bash
git add cmd/splatter/sheet.go cmd/splatter/open.go cmd/splatter/root.go cmd/splatter/cli_test.go
git commit -m "feat: sheet command with isolated browser-open helper"
```

---

### Task 14: Checklists, live-check doc, full verification, cross-compile

**Files:**
- Modify: `docs/windows-checklist.md`
- Modify: `docs/live-check.md` (has uncommitted local edits — READ it first and append without disturbing them)

**Interfaces:**
- Consumes: everything above.
- Produces: the operator-facing halves of the S3 exit criterion (macOS live check; Windows standing checklist), plus a fully verified, cross-compiled tree.

- [ ] **Step 1: Extend the Windows checklist**

Append to `docs/windows-checklist.md`:

```markdown

# S3 Windows Verification Checklist

Same setup as S1 (PS7, `splatter.exe` on PATH, workspace `ws` with project
`gradient-descent` and brief `b_001.md`). Set `GEMINI_API_KEY` and
`OPENAI_API_KEY` in the session for the fan step.

- [ ] `splatter fan --brief projects/gradient-descent/briefs/b_001.md --set baseline`
      — per-call lines print; exit 0 with at least one success (`$LASTEXITCODE`)
- [ ] `splatter fan --brief projects/gradient-descent/briefs/b_001.md` — usage
      error (neither --set nor --profiles), exit 2
- [ ] `splatter sheet --run <run id>` — prints the sheet path, exit 0
- [ ] Double-click `sheet.html` in Explorer — opens in the default browser
      from the filesystem; thumbnails render and click through to full-res
- [ ] `splatter sheet --run <run id> --open` — browser opens (`cmd /c start`)
- [ ] `splatter verdict --run <run id> --keep <image id> --note "solid direction"`
      — exit 0; `splatter verdict --run <run id> --keep nope_id` — exit 2
- [ ] `splatter validate` — exit 0
- [ ] `git add -A; git commit -m s3; git ls-files --eol` — `verdicts.jsonl`
      and `manifest.jsonl` show `i/lf` (no CRLF drift from verdict appends)
```

- [ ] **Step 2: Append the S3 live check (macOS half of the exit criterion)**

Read `docs/live-check.md` first (it has uncommitted local edits; keep them). Append:

```markdown

# S3 Live Exit-Criterion Check (operator-run, macOS)

Cost: one Gemini call + one OpenAI call (~$0.08). Same key setup and demo
workspace as the S2 check above.

```shell
splatter fan --brief projects/demo/briefs/b_001.md --set baseline
splatter sheet --run <run id> --open
splatter verdict --run <run id> --keep <an image id> --note "<image id>: crisp" --note "good round"
splatter validate
```

Confirm:
- [ ] fan exits 0 with one call per profile recorded in one manifest
- [ ] the sheet opens from the filesystem: provider groups, thumbnails
      linking to full-res, cost with source tag, copyable verdict block
- [ ] the verdict appends to projects/demo/verdicts.jsonl and validate exits 0
- [ ] rebuilding the sheet after the verdict shows keep status on the image

Then unset the keys: `unset GEMINI_API_KEY OPENAI_API_KEY`
```

(Watch the nesting when appending — the shell block above is fenced inside this plan's markdown; reproduce it as a plain fenced block in the doc.)

- [ ] **Step 3: Full verification**

Run each and confirm:

```bash
gofmt -l .          # expected: no output
go vet ./...        # expected: no output
go test ./...       # expected: ok for every package
make build-all      # expected: bin/darwin-arm64/splatter and bin/windows-amd64/splatter.exe
```

- [ ] **Step 4: Commit**

```bash
git add docs/windows-checklist.md docs/live-check.md
git commit -m "docs: S3 windows checklist and live exit-criterion check"
```

- [ ] **Step 5: Report the operator steps**

The S3 exit criterion is not fully met by automation. Tell the operator what remains:
1. Run the S3 section of `docs/live-check.md` on macOS with live keys (~$0.08).
2. Run the S3 section of `docs/windows-checklist.md` on the Windows machine.

---

## Self-Review

Checked against the design doc and spec:

- **S2 backlog** (design §"Substrate polish pre-task"): all four items → Tasks 1–4.
- **Fan shape** (§"Run refactor and fan"): `executeCall` extraction, `ResolvedProfile`/`FanParams`/`CallOutcome`/`FanResult` copied verbatim from the design doc (`FanParams.Profiles`' `Provider` field folded in as designed), sequential calls, `Gen` as one-profile wrapper with `GenResult` shape preserved → Tasks 7–8. Config-error-after-preflight recorded as failed call → `TestFanConfigErrorAfterPassingPreflightIsFailedCall`.
- **Pre-flight seam** (§"Decisions"): optional method on concrete adapters, type-assertion discovery, frozen interface untouched, zero network → Tasks 5, 7.
- **fan CLI** (§"cmd/splatter fan.go"): `--set` XOR `--profiles`, set membership via providers.yaml, exit 0/1/2 semantics per spec §5, `--json` emits `FanResult`, `buildProvider` seam reused → Task 9.
- **Verdict** (§"Verdict", spec §4.3): `FindRun` in workspace with distinguishable zero/multi-match errors → Task 6; note regex `^([a-z0-9_]+):\s`, run-level default, pre-append validation (unknown IDs, keep∩cull, empty verdict → usage errors, nothing appended), session default local date with `--session` override, `[]` not null, `fsio.AppendRecord` → Task 10.
- **Sheet** (§"Sheet", spec §10 S3 contents): `Build`/`Render` split, embedded template, inline CSS, no JS, relative hrefs, CSS-scaled self-linked thumbnails, provider grouping, model badge fallback, params/latency/cost-with-source, critique verdict + scores, per-image status with later-records-win, error cards, header brief excerpt with missing-brief hash fallback, footer totals + unavailable note + parent link, `<pre>` verdict command block, `ReplaceFile` regeneration → Tasks 11–13. `--open` via the single GOOS branch, excluded from automated tests → Task 13.
- **Testing section of the design doc**: every listed test appears in Tasks 2, 5, 7–13; Gen-regression gate is Task 8 Step 3; Windows checklist extension is Task 14.
- **Type consistency**: `run.Fan`/`run.ResolvedProfile`/`run.FanResult` names match between Tasks 7, 8, 9; `workspace.FindRun`/`ReadManifest`/`ErrRunNotFound`/`AmbiguousRunError` match between Tasks 6, 10, 13; `sheet.Build`/`Render`/`Data` fields match between Tasks 11, 12, 13; `locateRun` defined in Task 10, reused in Task 13 (Task 10 therefore must land before Task 13).
- **Known intentional deltas** called out in Task 8 (failed `GenResult.Images` `[]` vs `nil`; profile-prefixed capability errors; Preflight-less fakes) with the reasoning recorded.
