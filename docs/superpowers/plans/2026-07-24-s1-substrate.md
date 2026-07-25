# S1 Substrate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build splatter's substrate: Go module scaffold, frozen v1 schema types with JSONL round-trip, Windows-safe write helpers, and the `init`, `status`, and `validate` commands, cross-compiled for both targets.

**Architecture:** Thin cobra command layer in `cmd/splatter/` over three domain packages: `internal/schema` (v1 record types, JSONL codec, brief hashing), `internal/workspace` (root discovery, scaffold, validate/status walks), `internal/fsio` (append-fsync and replace-via-rename write primitives). Spec: `docs/splatter_spec.md`; design: `docs/superpowers/specs/2026-07-24-s1-substrate-design.md`.

**Tech Stack:** Go (latest stable), `github.com/spf13/cobra`, `gopkg.in/yaml.v3`. No other dependencies.

## Global Constraints

- Module path: `github.com/cajundata/splatter`.
- Build targets: `windows/amd64` and `darwin/arm64` only. No linux target, no WSL branches.
- **Zero** `runtime.GOOS` branches in S1 (the spec permits exactly one, browser-open, which arrives in S3).
- All filesystem paths through `path/filepath`; any path stored in an evidence file is relative and slash-normalized (`filepath.ToSlash`).
- Append-only JSONL writes: `O_APPEND|O_CREATE|O_WRONLY`, exactly one `Write` call per record (line + `\n`), then fsync. Never rename.
- Derived/regenerable files: write temp in destination dir, fsync, remove destination, rename — via the one shared helper `fsio.ReplaceFile`.
- Every record carries `"v":1`. Readers tolerate unknown fields but reject missing required ones.
- Exit codes: 0 success, 1 runtime error, 2 usage error, 3 validation failure.
- Every command supports `--json` (machine-readable result object on stdout); logs go to stderr.
- Evidence files (`manifest.jsonl`, `verdicts.jsonl`) are CLI-written only — nothing in S1 writes them except tests exercising the schema/fsio layers.
- Commit after every green test cycle. TDD for all behavior.

## File Structure

```
splatter/
├── cmd/splatter/
│   ├── main.go            # entrypoint: run root, map errors to exit codes
│   ├── root.go            # root cobra command, --json persistent flag, version
│   ├── init.go            # splatter init [project]
│   ├── status.go          # splatter status
│   └── validate.go        # splatter validate [--project p]
├── internal/
│   ├── fsio/
│   │   ├── fsio.go        # AppendRecord, ReplaceFile
│   │   └── fsio_test.go
│   ├── schema/
│   │   ├── records.go     # RunHeader, CallRecord, Verdict, Critique + Validate()
│   │   ├── records_test.go
│   │   ├── jsonl.go       # line codec: DecodeManifestLine, DecodeVerdictLine
│   │   ├── jsonl_test.go
│   │   ├── brief.go       # front-matter parse, LF normalization, sha256
│   │   └── brief_test.go
│   └── workspace/
│       ├── workspace.go   # FindRoot, ScaffoldWorkspace, ScaffoldProject
│       ├── workspace_test.go
│       ├── validate.go    # Validate walk → []Finding
│       ├── validate_test.go
│       ├── status.go      # Status walk → []ProjectStatus
│       └── status_test.go
├── docs/windows-checklist.md
├── Makefile
├── go.mod
└── go.sum
```

---

### Task 1: Module scaffold, root command, Makefile

**Files:**
- Create: `go.mod`, `Makefile`, `cmd/splatter/main.go`, `cmd/splatter/root.go`

**Interfaces:**
- Produces: `newRootCmd() *cobra.Command` (root.go); package-level `var version = "dev"` overridden by `-ldflags`; `jsonOut bool` persistent flag readable by subcommands via `cmd.Flags()`/closure; `exitCode(err error) int` in main.go mapping errors to 0/1/2/3; sentinel error type `validationErr` (Task 7 wraps findings in it to force exit 3).

- [ ] **Step 1: Initialize module and dependencies**

```bash
cd /Users/weldon/projects/splatter
go mod init github.com/cajundata/splatter
go get github.com/spf13/cobra@latest
go get gopkg.in/yaml.v3@latest
```

- [ ] **Step 2: Write root.go and main.go**

`cmd/splatter/root.go`:

```go
package main

import (
	"github.com/spf13/cobra"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// jsonOut is set by the persistent --json flag.
var jsonOut bool

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "splatter",
		Short:         "Concept-refinement instrument for T-shirt designs",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit machine-readable result object on stdout")
	// Flag parse failures are usage errors (exit 2).
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageErr{err}
	})
	return root
}

// usageArgs wraps a cobra positional-args validator so violations become
// usage errors (exit 2) instead of runtime errors. Every subcommand uses
// it: Args: usageArgs(cobra.NoArgs), etc.
func usageArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return usageErr{err}
		}
		return nil
	}
}
```

`cmd/splatter/main.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// validationErr forces exit code 3. Command implementations wrap
// validation failures in it; runtime errors pass through as plain errors.
type validationErr struct{ err error }

func (v validationErr) Error() string { return v.err.Error() }
func (v validationErr) Unwrap() error { return v.err }

// usageErr forces exit code 2 for argument errors detected inside RunE.
type usageErr struct{ err error }

func (u usageErr) Error() string { return u.err.Error() }
func (u usageErr) Unwrap() error { return u.err }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var v validationErr
	if errors.As(err, &v) {
		return 3
	}
	var u usageErr
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "splatter:", err)
		code := exitCode(err)
		// Unknown subcommand: cobra returns a plain error from Execute
		// before any RunE runs; Find failing identifies it as usage.
		if code == 1 {
			if _, _, findErr := root.Find(os.Args[1:]); findErr != nil {
				code = 2
			}
		}
		os.Exit(code)
	}
}
```

The three usage-error paths each have an owner: bad flags → `SetFlagErrorFunc` wraps in `usageErr`; bad positional args → `usageArgs` wrapper on each subcommand; unknown subcommand → the `Find` check in `main`. Step 4's manual checks are the acceptance test for all three.

- [ ] **Step 3: Write Makefile**

```makefile
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -ldflags "-X main.version=$(VERSION)"

.PHONY: build test build-all clean

build:
	go build $(LDFLAGS) -o bin/splatter ./cmd/splatter

test:
	go test ./...

build-all:
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o bin/darwin-arm64/splatter      ./cmd/splatter
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o bin/windows-amd64/splatter.exe ./cmd/splatter

clean:
	rm -rf bin
```

- [ ] **Step 4: Verify build, version, and exit codes**

```bash
make build
./bin/splatter --version        # expect: splatter version <git describe or dev>
./bin/splatter nonsense; echo $?  # expect error on stderr, exit 2
./bin/splatter --badflag; echo $? # expect error on stderr, exit 2
./bin/splatter --help; echo $?    # expect usage on stdout, exit 0
```

If `nonsense` or `--badflag` exits 1 instead of 2, fix the mapping in `main()` until these manual checks pass; they are the acceptance test for this task.

- [ ] **Step 5: Add bin/ to .gitignore and commit**

```bash
printf 'bin/\n' >> .gitignore
git add -A
git commit -m "feat: scaffold module, root command, Makefile"
```

---

### Task 2: fsio write primitives

**Files:**
- Create: `internal/fsio/fsio.go`
- Test: `internal/fsio/fsio_test.go`

**Interfaces:**
- Produces: `fsio.AppendRecord(path string, v any) error` — marshals `v` to one JSON line, appends atomically with fsync. `fsio.ReplaceFile(path string, data []byte) error` — temp-write/remove/rename for derived files. Both create parent directories? **No** — callers ensure directories exist; these helpers fail if the parent is missing (keeps them predictable).

- [ ] **Step 1: Write the failing tests**

`internal/fsio/fsio_test.go`:

```go
package fsio

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendRecordProducesOneParseableLinePerCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	for i := 0; i < 3; i++ {
		if err := AppendRecord(path, map[string]int{"n": i}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]int
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %d not valid JSON: %v", lines, err)
		}
		if m["n"] != lines {
			t.Fatalf("line %d: got n=%d", lines, m["n"])
		}
		lines++
	}
	if lines != 3 {
		t.Fatalf("want 3 lines, got %d", lines)
	}
}

func TestAppendRecordRejectsUnmarshalableValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.jsonl")
	if err := AppendRecord(path, make(chan int)); err == nil {
		t.Fatal("want error for unmarshalable value")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file must not be created when marshal fails")
	}
}

func TestReplaceFileOverExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.html")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("got %q", got)
	}
}

func TestReplaceFileLeavesNoTempLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.html")
	if err := ReplaceFile(path, []byte("content")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("want only sheet.html, got %s", strings.Join(names, ", "))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/fsio/ -v`
Expected: FAIL — `undefined: AppendRecord`, `undefined: ReplaceFile` (compile error).

- [ ] **Step 3: Write the implementation**

`internal/fsio/fsio.go`:

```go
// Package fsio provides splatter's two write primitives. Evidence files
// (append-only JSONL) go through AppendRecord; derived, regenerable files
// (sheets, exports) go through ReplaceFile. Nothing else writes files.
package fsio

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AppendRecord marshals v to a single JSON line and appends it to path
// with one Write call followed by fsync. O_APPEND guarantees no partial
// interleaved lines; marshal errors happen before the file is touched.
func AppendRecord(path string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// ReplaceFile atomically replaces path with data: write temp in the same
// directory, fsync, remove any existing destination, rename. The
// unconditional remove-before-rename works identically on darwin and
// windows, so no GOOS branch is needed.
func ReplaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fsio/ -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/fsio/
git commit -m "feat: fsio append and replace write primitives"
```

---

### Task 3: Schema record types and JSONL codec

**Files:**
- Create: `internal/schema/records.go`, `internal/schema/jsonl.go`
- Test: `internal/schema/records_test.go`, `internal/schema/jsonl_test.go`

**Interfaces:**
- Produces (types used by every later task):

```go
type RunHeader struct {
	V            int       `json:"v"`
	Type         string    `json:"type"` // "run"
	Run          string    `json:"run"`
	Project      string    `json:"project"`
	Brief        string    `json:"brief"`
	BriefSHA256  string    `json:"brief_sha256"`
	Iteration    int       `json:"iteration"`
	ParentRun    *string   `json:"parent_run"`
	Relationship *string   `json:"relationship"` // "textual-refinement-of" | "regeneration-of"
	Created      time.Time `json:"created"`
	Harness      string    `json:"harness"`
}

type CallRequest struct {
	Prompt string         `json:"prompt"`
	N      int            `json:"n"`
	Aspect string         `json:"aspect"`
	Seed   *int64         `json:"seed"`
	Native map[string]any `json:"native,omitempty"`
}

type CallResponse struct {
	LatencyMS         int64  `json:"latency_ms"`
	HTTPStatus        int    `json:"http_status"`
	ProviderRequestID string `json:"provider_request_id,omitempty"`
}

type Cost struct {
	USD    *float64 `json:"usd"` // null when source == "unavailable"
	Source string   `json:"source"` // "reported" | "table:<version>" | "unavailable"
}

type ImageRef struct {
	ID           string `json:"id"`
	File         string `json:"file"` // relative, slash-normalized
	SHA256       string `json:"sha256"`
	W            int    `json:"w"`
	H            int    `json:"h"`
	AspectActual string `json:"aspect_actual"`
}

type CallError struct {
	Stage      string `json:"stage"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Message    string `json:"message"`
}

type CallRecord struct {
	V              int          `json:"v"`
	Type           string       `json:"type"` // "call"
	Run            string       `json:"run"`
	Call           string       `json:"call"`
	TS             time.Time    `json:"ts"`
	Provider       string       `json:"provider"`
	ModelRequested string       `json:"model_requested"`
	ModelReturned  string       `json:"model_returned"`
	Profile        string       `json:"profile"`
	Operation      string       `json:"operation"` // "generate" | "edit"
	Request        CallRequest  `json:"request"`
	Response       CallResponse `json:"response"`
	Cost           Cost         `json:"cost"`
	Images         []ImageRef   `json:"images"`
	Raw            string       `json:"raw"`
	Error          *CallError   `json:"error"`
}

type VerdictNote struct {
	Image *string `json:"image"` // nil = run-level note
	Text  string  `json:"text"`
}

type Verdict struct {
	V       int           `json:"v"`
	Type    string        `json:"type"` // "verdict"
	Run     string        `json:"run"`
	TS      time.Time     `json:"ts"`
	Session string        `json:"session"`
	Keep    []string      `json:"keep"`
	Cull    []string      `json:"cull"`
	Notes   []VerdictNote `json:"notes"`
}

type CritiqueItem struct {
	Image   string         `json:"image"`
	Verdict string         `json:"verdict"` // "keep" | "cull"
	Scores  map[string]int `json:"scores"`
	Reason  string         `json:"reason"`
}

type Critique struct {
	V      int            `json:"v"`
	Run    string         `json:"run"`
	Rubric string         `json:"rubric"`
	Items  []CritiqueItem `json:"items"`
}
```

- Each of `RunHeader`, `CallRecord`, `Verdict`, `Critique` has `Validate() error` returning the **first** missing/invalid required field as `"missing required field: <json name>"` or `"invalid <name>: <detail>"`.
- `jsonl.go` produces: `DecodeManifestLine(line []byte) (any, error)` returning `*RunHeader` or `*CallRecord` by dispatching on the `"type"` field (unknown type = error); `DecodeVerdictLine(line []byte) (*Verdict, error)`. Both call `Validate()` before returning.

- [ ] **Step 1: Write the failing record tests**

`internal/schema/records_test.go`:

```go
package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validRunHeader() RunHeader {
	return RunHeader{
		V: 1, Type: "run", Run: "r_0001", Project: "gradient-descent",
		Brief: "briefs/b_001.md", BriefSHA256: "9f2c", Iteration: 1,
		Created: time.Date(2026, 7, 24, 14, 31, 8, 0, time.UTC),
		Harness: "splatter v0.1.0",
	}
}

func validCallRecord() CallRecord {
	usd := 0.27
	return CallRecord{
		V: 1, Type: "call", Run: "r_0001", Call: "c_01",
		TS:       time.Date(2026, 7, 24, 14, 31, 26, 0, time.UTC),
		Provider: "gemini", ModelRequested: "m", ModelReturned: "m",
		Profile: "gemini-baseline", Operation: "generate",
		Request:  CallRequest{Prompt: "p", N: 4, Aspect: "square"},
		Response: CallResponse{LatencyMS: 18240, HTTPStatus: 200},
		Cost:     Cost{USD: &usd, Source: "reported"},
		Images: []ImageRef{{ID: "c_01_0", File: "images/c_01_0.png",
			SHA256: "ab", W: 1024, H: 1024, AspectActual: "square"}},
		Raw: "raw/c_01.json",
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []any{validRunHeader(), validCallRecord(),
		Verdict{V: 1, Type: "verdict", Run: "r_0001",
			TS: time.Date(2026, 7, 24, 15, 2, 11, 0, time.UTC), Session: "2026-07-24",
			Keep: []string{"c_01_0"}, Cull: []string{"c_01_1"},
			Notes: []VerdictNote{{Image: nil, Text: "run-level"}}},
		Critique{V: 1, Run: "r_0001", Rubric: "rubric_v1",
			Items: []CritiqueItem{{Image: "c_01_0", Verdict: "keep",
				Scores: map[string]int{"concept_fit": 4}, Reason: "r"}}},
	}
	for _, in := range cases {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("%T marshal: %v", in, err)
		}
		out := reflect.New(reflect.TypeOf(in))
		if err := json.Unmarshal(b, out.Interface()); err != nil {
			t.Fatalf("%T unmarshal: %v", in, err)
		}
		if !reflect.DeepEqual(in, out.Elem().Interface()) {
			t.Fatalf("%T round-trip mismatch:\n in: %#v\nout: %#v", in, in, out.Elem().Interface())
		}
	}
}

func TestUnknownFieldsTolerated(t *testing.T) {
	b, _ := json.Marshal(validRunHeader())
	withExtra := strings.Replace(string(b), `{`, `{"future_field":42,`, 1)
	var h RunHeader
	if err := json.Unmarshal([]byte(withExtra), &h); err != nil {
		t.Fatalf("unknown field must be tolerated: %v", err)
	}
	if err := h.Validate(); err != nil {
		t.Fatalf("record with unknown field must validate: %v", err)
	}
}

func TestValidateCatchesMissingRequired(t *testing.T) {
	h := validRunHeader()
	h.BriefSHA256 = ""
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "brief_sha256") {
		t.Fatalf("want brief_sha256 error, got %v", err)
	}
	h = validRunHeader()
	h.V = 0
	if err := h.Validate(); err == nil || !strings.Contains(err.Error(), "v") {
		t.Fatalf("want v error, got %v", err)
	}
	c := validCallRecord()
	c.Cost.Source = ""
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "cost.source") {
		t.Fatalf("want cost.source error, got %v", err)
	}
	c = validCallRecord()
	c.Operation = "remix"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "operation") {
		t.Fatalf("want operation error, got %v", err)
	}
	v := Verdict{V: 1, Type: "verdict", Run: "r_0001", TS: time.Now().UTC(), Session: ""}
	if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("want session error, got %v", err)
	}
	q := Critique{V: 1, Run: "", Rubric: "rubric_v1"}
	if err := q.Validate(); err == nil || !strings.Contains(err.Error(), "run") {
		t.Fatalf("want run error, got %v", err)
	}
}

func TestFailedCallValidates(t *testing.T) {
	c := validCallRecord()
	c.Images = nil
	c.Error = &CallError{Stage: "request", HTTPStatus: 429, Message: "rate limited"}
	c.Cost = Cost{USD: nil, Source: "unavailable"}
	c.Raw = "" // a call that never reached the wire has no raw sidecar
	if err := c.Validate(); err != nil {
		t.Fatalf("failed-call record must validate: %v", err)
	}
	ok := validCallRecord()
	ok.Raw = ""
	if err := ok.Validate(); err == nil {
		t.Fatal("successful call without raw must fail validation")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/schema/ -v`
Expected: FAIL — compile error, types undefined.

- [ ] **Step 3: Write records.go**

`internal/schema/records.go` — the type declarations exactly as shown in this task's **Interfaces** block above (copy them verbatim), plus:

```go
package schema

import (
	"fmt"
	"time"
)

// (type declarations from the Interfaces block go here)

func missing(name string) error { return fmt.Errorf("missing required field: %s", name) }

func (h RunHeader) Validate() error {
	switch {
	case h.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", h.V)
	case h.Type != "run":
		return fmt.Errorf("invalid type: want \"run\", got %q", h.Type)
	case h.Run == "":
		return missing("run")
	case h.Project == "":
		return missing("project")
	case h.Brief == "":
		return missing("brief")
	case h.BriefSHA256 == "":
		return missing("brief_sha256")
	case h.Iteration < 1:
		return fmt.Errorf("invalid iteration: %d", h.Iteration)
	case h.Created.IsZero():
		return missing("created")
	case h.Harness == "":
		return missing("harness")
	}
	if h.Relationship != nil {
		if h.ParentRun == nil {
			return fmt.Errorf("invalid relationship: set without parent_run")
		}
		if r := *h.Relationship; r != "textual-refinement-of" && r != "regeneration-of" {
			return fmt.Errorf("invalid relationship: %q", r)
		}
	}
	if h.ParentRun != nil && h.Relationship == nil {
		return fmt.Errorf("invalid parent_run: set without relationship")
	}
	return nil
}

func (c CallRecord) Validate() error {
	switch {
	case c.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", c.V)
	case c.Type != "call":
		return fmt.Errorf("invalid type: want \"call\", got %q", c.Type)
	case c.Run == "":
		return missing("run")
	case c.Call == "":
		return missing("call")
	case c.TS.IsZero():
		return missing("ts")
	case c.Provider == "":
		return missing("provider")
	case c.Operation != "generate" && c.Operation != "edit":
		return fmt.Errorf("invalid operation: %q", c.Operation)
	case c.Cost.Source == "":
		return missing("cost.source")
	}
	if len(c.Images) == 0 && c.Error == nil {
		return fmt.Errorf("invalid record: no images and no error")
	}
	// A failed call may never have produced a wire body; raw is required
	// only for successful calls.
	if c.Error == nil && c.Raw == "" {
		return missing("raw")
	}
	for i, img := range c.Images {
		if img.ID == "" || img.File == "" || img.SHA256 == "" {
			return fmt.Errorf("invalid images[%d]: id, file, sha256 all required", i)
		}
	}
	return nil
}

func (v Verdict) Validate() error {
	switch {
	case v.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", v.V)
	case v.Type != "verdict":
		return fmt.Errorf("invalid type: want \"verdict\", got %q", v.Type)
	case v.Run == "":
		return missing("run")
	case v.TS.IsZero():
		return missing("ts")
	case v.Session == "":
		return missing("session")
	}
	for i, n := range v.Notes {
		if n.Text == "" {
			return fmt.Errorf("invalid notes[%d]: text required", i)
		}
	}
	return nil
}

func (q Critique) Validate() error {
	switch {
	case q.V != 1:
		return fmt.Errorf("invalid v: want 1, got %d", q.V)
	case q.Run == "":
		return missing("run")
	case q.Rubric == "":
		return missing("rubric")
	}
	for i, it := range q.Items {
		if it.Image == "" {
			return fmt.Errorf("invalid items[%d]: image required", i)
		}
		if it.Verdict != "keep" && it.Verdict != "cull" {
			return fmt.Errorf("invalid items[%d].verdict: %q", i, it.Verdict)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/schema/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/schema/records.go internal/schema/records_test.go
git commit -m "feat: v1 schema record types with validation"
```

- [ ] **Step 6: Write the failing JSONL codec tests**

`internal/schema/jsonl_test.go`:

```go
package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeManifestLineDispatchesOnType(t *testing.T) {
	hb, _ := json.Marshal(validRunHeader())
	got, err := DecodeManifestLine(hb)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(*RunHeader); !ok {
		t.Fatalf("want *RunHeader, got %T", got)
	}
	cb, _ := json.Marshal(validCallRecord())
	got, err = DecodeManifestLine(cb)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(*CallRecord); !ok {
		t.Fatalf("want *CallRecord, got %T", got)
	}
}

func TestDecodeManifestLineRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"malformed json": `{"v":1,"type":"run"`,
		"unknown type":   `{"v":1,"type":"mystery"}`,
		"invalid record": `{"v":1,"type":"run","run":"r_0001"}`,
	}
	for name, line := range cases {
		if _, err := DecodeManifestLine([]byte(line)); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

func TestDecodeVerdictLine(t *testing.T) {
	line := `{"v":1,"type":"verdict","run":"r_0001","ts":"2026-07-24T15:02:11Z",` +
		`"session":"2026-07-24","keep":["c_01_0"],"cull":[],"notes":[]}`
	v, err := DecodeVerdictLine([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if v.Run != "r_0001" || len(v.Keep) != 1 {
		t.Fatalf("bad decode: %#v", v)
	}
	if _, err := DecodeVerdictLine([]byte(`{"v":1,"type":"verdict"}`)); err == nil ||
		!strings.Contains(err.Error(), "run") {
		t.Fatalf("want run error, got %v", err)
	}
}
```

- [ ] **Step 7: Run tests to verify they fail**

Run: `go test ./internal/schema/ -run TestDecode -v`
Expected: FAIL — `undefined: DecodeManifestLine`, `undefined: DecodeVerdictLine`.

- [ ] **Step 8: Write jsonl.go**

`internal/schema/jsonl.go`:

```go
package schema

import (
	"encoding/json"
	"fmt"
)

// DecodeManifestLine parses one manifest.jsonl line into *RunHeader or
// *CallRecord based on the "type" field, validating before returning.
func DecodeManifestLine(line []byte) (any, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	switch probe.Type {
	case "run":
		var h RunHeader
		if err := json.Unmarshal(line, &h); err != nil {
			return nil, err
		}
		if err := h.Validate(); err != nil {
			return nil, err
		}
		return &h, nil
	case "call":
		var c CallRecord
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
		return &c, nil
	default:
		return nil, fmt.Errorf("unknown record type %q", probe.Type)
	}
}

// DecodeVerdictLine parses and validates one verdicts.jsonl line.
func DecodeVerdictLine(line []byte) (*Verdict, error) {
	var v Verdict
	if err := json.Unmarshal(line, &v); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return &v, nil
}
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `go test ./internal/schema/ -v`
Expected: PASS (all record + codec tests).

- [ ] **Step 10: Commit**

```bash
git add internal/schema/jsonl.go internal/schema/jsonl_test.go
git commit -m "feat: manifest and verdict JSONL line codec"
```

---

### Task 4: Brief front matter and hashing

**Files:**
- Create: `internal/schema/brief.go`
- Test: `internal/schema/brief_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (pure functions).
- Produces:

```go
type BriefMeta struct {
	ID         string   `yaml:"id"`
	Project    string   `yaml:"project"`
	Concept    string   `yaml:"concept"`
	Mood       []string `yaml:"mood"`
	Motifs     []string `yaml:"motifs"`
	Exclusions []string `yaml:"exclusions"`
	Aspect     string   `yaml:"aspect"`
}

func ParseBrief(data []byte) (*BriefMeta, string, error) // meta, body prose, error
func NormalizeLF(b []byte) []byte                        // CRLF and lone CR → LF
func BriefSHA256(fileBytes []byte) string                // hex sha256 of NormalizeLF(fileBytes)
```

- `ParseBrief` requires `id`, `project`, `concept`, and a valid `aspect` (`square`, `portrait_4_5`, `portrait_2_3`, `landscape_4_3` — the harness vocabulary from spec §6.1). Missing front matter delimiters is an error.

- [ ] **Step 1: Write the failing tests**

`internal/schema/brief_test.go`:

```go
package schema

import (
	"strings"
	"testing"
)

const briefDoc = `---
id: b_001
project: gradient-descent
concept: one-line concept statement
mood: [technical, austere]
motifs: [contour lines, sparse grids]
exclusions: [brains, circuit-board cliches]
aspect: square
---
Creative direction prose follows in the body.
`

func TestParseBrief(t *testing.T) {
	meta, body, err := ParseBrief([]byte(briefDoc))
	if err != nil {
		t.Fatal(err)
	}
	if meta.ID != "b_001" || meta.Project != "gradient-descent" || meta.Aspect != "square" {
		t.Fatalf("bad meta: %#v", meta)
	}
	if len(meta.Mood) != 2 || meta.Mood[0] != "technical" {
		t.Fatalf("bad mood: %#v", meta.Mood)
	}
	if !strings.Contains(body, "Creative direction prose") {
		t.Fatalf("bad body: %q", body)
	}
}

func TestParseBriefRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"no front matter": "just prose\n",
		"missing id":      "---\nproject: p\nconcept: c\naspect: square\n---\nbody\n",
		"bad aspect":      "---\nid: b_001\nproject: p\nconcept: c\naspect: wide\n---\nbody\n",
		"unclosed":        "---\nid: b_001\n",
	}
	for name, doc := range cases {
		if _, _, err := ParseBrief([]byte(doc)); err == nil {
			t.Fatalf("%s: want error", name)
		}
	}
}

func TestNormalizeLF(t *testing.T) {
	got := NormalizeLF([]byte("a\r\nb\rc\nd"))
	if string(got) != "a\nb\nc\nd" {
		t.Fatalf("got %q", got)
	}
}

func TestBriefSHA256LineEndingInvariant(t *testing.T) {
	lf := []byte("---\nid: b_001\n---\nbody\n")
	crlf := []byte("---\r\nid: b_001\r\n---\r\nbody\r\n")
	if BriefSHA256(lf) != BriefSHA256(crlf) {
		t.Fatal("CRLF and LF briefs must hash identically")
	}
	if BriefSHA256(lf) == BriefSHA256([]byte("---\nid: b_002\n---\nbody\n")) {
		t.Fatal("different briefs must hash differently")
	}
	if len(BriefSHA256(lf)) != 64 {
		t.Fatal("want 64-char hex digest")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/schema/ -run 'TestParseBrief|TestNormalizeLF|TestBriefSHA256' -v`
Expected: FAIL — compile error, functions undefined.

- [ ] **Step 3: Write brief.go**

`internal/schema/brief.go`:

```go
package schema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"gopkg.in/yaml.v3"
)

type BriefMeta struct {
	ID         string   `yaml:"id"`
	Project    string   `yaml:"project"`
	Concept    string   `yaml:"concept"`
	Mood       []string `yaml:"mood"`
	Motifs     []string `yaml:"motifs"`
	Exclusions []string `yaml:"exclusions"`
	Aspect     string   `yaml:"aspect"`
}

// harness aspect vocabulary, spec §6.1
var validAspects = map[string]bool{
	"square": true, "portrait_4_5": true, "portrait_2_3": true, "landscape_4_3": true,
}

// ParseBrief splits YAML front matter from body prose and validates
// required fields. Input is LF-normalized first so CRLF briefs parse.
func ParseBrief(data []byte) (*BriefMeta, string, error) {
	norm := NormalizeLF(data)
	if !bytes.HasPrefix(norm, []byte("---\n")) {
		return nil, "", fmt.Errorf("brief missing front matter opening ---")
	}
	rest := norm[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		return nil, "", fmt.Errorf("brief front matter not closed")
	}
	var meta BriefMeta
	if err := yaml.Unmarshal(rest[:end], &meta); err != nil {
		return nil, "", fmt.Errorf("brief front matter: %w", err)
	}
	switch {
	case meta.ID == "":
		return nil, "", fmt.Errorf("brief missing required field: id")
	case meta.Project == "":
		return nil, "", fmt.Errorf("brief missing required field: project")
	case meta.Concept == "":
		return nil, "", fmt.Errorf("brief missing required field: concept")
	case !validAspects[meta.Aspect]:
		return nil, "", fmt.Errorf("brief invalid aspect: %q", meta.Aspect)
	}
	body := string(rest[end+len("\n---\n"):])
	return &meta, body, nil
}

// NormalizeLF converts CRLF and lone CR line endings to LF.
func NormalizeLF(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
}

// BriefSHA256 hashes the LF-normalized brief bytes; this is the hash
// frozen into run headers as brief_sha256.
func BriefSHA256(fileBytes []byte) string {
	sum := sha256.Sum256(NormalizeLF(fileBytes))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/schema/ -v`
Expected: PASS (all schema tests).

- [ ] **Step 5: Commit**

```bash
git add internal/schema/brief.go internal/schema/brief_test.go
git commit -m "feat: brief front-matter parsing and LF-normalized hashing"
```

---

### Task 5: Workspace discovery and scaffolding

**Files:**
- Create: `internal/workspace/workspace.go`
- Test: `internal/workspace/workspace_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (os/filepath only).
- Produces:

```go
// ErrNoWorkspace is returned by FindRoot when no marker is found.
var ErrNoWorkspace = errors.New("not inside a splatter workspace")

func FindRoot(start string) (string, error)          // walk up to providers.yaml + projects/
func ScaffoldWorkspace(dir string) (*ScaffoldResult, error)
func ScaffoldProject(root, name string) (*ScaffoldResult, error)

type ScaffoldResult struct {
	Created []string // relative slash paths, sorted
	Existed []string // relative slash paths, sorted
}
```

- Workspace marker: a directory containing both `providers.yaml` and `projects/`.
- `ScaffoldWorkspace` refuses (error) if `dir` is already inside a workspace (its own marker or an ancestor's). Idempotence is per-file: existing files land in `Existed` untouched, missing ones are created.
- `ScaffoldProject` errors if `root` is not a workspace root; creates `projects/<name>/{briefs,critiques,packages}/` each with `.gitkeep`. Project names must match `^[a-z0-9][a-z0-9-]*$`.

- [ ] **Step 1: Write the failing tests**

`internal/workspace/workspace_test.go`:

```go
package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldWorkspaceCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	res, err := ScaffoldWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".gitattributes", ".gitignore", "providers.yaml", "pricing.yaml", "projects"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if len(res.Existed) != 0 || len(res.Created) == 0 {
		t.Fatalf("fresh scaffold: %#v", res)
	}
	ga, _ := os.ReadFile(filepath.Join(dir, ".gitattributes"))
	if !strings.Contains(string(ga), "eol=lf") {
		t.Fatalf(".gitattributes must force LF: %q", ga)
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), "projects/*/runs/*/images/") {
		t.Fatalf(".gitignore must ignore run images: %q", gi)
	}
	py, _ := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	if !strings.Contains(string(py), "version: 1") {
		t.Fatalf("providers.yaml must declare version: %q", py)
	}
}

func TestScaffoldWorkspaceIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	// Re-run in the same dir is idempotent: everything reported existed.
	res, err := ScaffoldWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 0 || len(res.Existed) == 0 {
		t.Fatalf("re-run: %#v", res)
	}
	// Scaffolding nested under an existing workspace is refused.
	nested := filepath.Join(dir, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldWorkspace(nested); err == nil {
		t.Fatal("want refusal nested under existing workspace")
	}
}

func TestScaffoldWorkspaceNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	custom := []byte("version: 1\n# my profiles\n")
	if err := os.WriteFile(filepath.Join(dir, "providers.yaml"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ScaffoldWorkspace(dir) // partial dir: has providers.yaml, no projects/
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "providers.yaml"))
	if string(got) != string(custom) {
		t.Fatal("existing file was overwritten")
	}
	found := false
	for _, p := range res.Existed {
		if p == "providers.yaml" {
			found = true
		}
	}
	if !found {
		t.Fatalf("providers.yaml should be reported as existed: %#v", res)
	}
}

func TestFindRoot(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(dir, "projects", "gradient-descent", "briefs")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := FindRoot(deep)
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir may contain symlinks on darwin; compare resolved paths.
	wantRoot, _ := filepath.EvalSymlinks(dir)
	gotRoot, _ := filepath.EvalSymlinks(root)
	if gotRoot != wantRoot {
		t.Fatalf("want %s, got %s", wantRoot, gotRoot)
	}
	if _, err := FindRoot(t.TempDir()); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("want ErrNoWorkspace, got %v", err)
	}
}

func TestScaffoldProject(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldWorkspace(dir); err != nil {
		t.Fatal(err)
	}
	res, err := ScaffoldProject(dir, "gradient-descent")
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"briefs", "critiques", "packages"} {
		if _, err := os.Stat(filepath.Join(dir, "projects", "gradient-descent", sub, ".gitkeep")); err != nil {
			t.Fatalf("missing %s/.gitkeep: %v", sub, err)
		}
	}
	if len(res.Created) == 0 {
		t.Fatalf("fresh project: %#v", res)
	}
	// Idempotent re-run reports existed, errors nothing.
	res2, err := ScaffoldProject(dir, "gradient-descent")
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Created) != 0 || len(res2.Existed) == 0 {
		t.Fatalf("re-run: %#v", res2)
	}
	// Bad names and non-workspace roots error.
	if _, err := ScaffoldProject(dir, "Bad Name"); err == nil {
		t.Fatal("want error for invalid project name")
	}
	if _, err := ScaffoldProject(t.TempDir(), "ok"); err == nil {
		t.Fatal("want error outside workspace")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/workspace/ -v`
Expected: FAIL — compile error, functions undefined.

- [ ] **Step 3: Write workspace.go**

`internal/workspace/workspace.go`:

```go
// Package workspace knows the splats layout: marker-based root discovery,
// scaffolding for init, and the walks behind validate and status.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var ErrNoWorkspace = errors.New("not inside a splatter workspace")

var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type ScaffoldResult struct {
	Created []string
	Existed []string
}

const gitattributes = `# Evidence files cross PS7/macOS through git; LF always.
* text=auto eol=lf
*.png binary
`

const gitignore = `projects/*/runs/*/images/
`

const providersStub = `version: 1
profiles: { }
# Example:
#   gemini-baseline:
#     provider: gemini
#     model: <model id>
#     native: { }
profile_sets: { }
`

const pricingStub = `version: "2026-07-24.0"
# Per-image prices used when the API response reports no cost.
# Bump version on every edit; it lands in cost.source as table:<version>.
`

// isRoot reports whether dir carries the workspace marker.
func isRoot(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, "providers.yaml")); err != nil || fi.IsDir() {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "projects"))
	return err == nil && fi.IsDir()
}

// FindRoot walks up from start looking for the workspace marker.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if isRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoWorkspace
		}
		dir = parent
	}
}

// ScaffoldWorkspace creates the workspace skeleton in dir. Re-running in
// an existing workspace root is idempotent (files reported as existed,
// missing ones recreated); nesting under another workspace is refused.
// Existing files are never overwritten.
func ScaffoldWorkspace(dir string) (*ScaffoldResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if root, err := FindRoot(filepath.Dir(abs)); err == nil {
		return nil, fmt.Errorf("already inside workspace at %s", root)
	}
	dir = abs
	res := &ScaffoldResult{}
	files := []struct{ name, content string }{
		{".gitattributes", gitattributes},
		{".gitignore", gitignore},
		{"providers.yaml", providersStub},
		{"pricing.yaml", pricingStub},
	}
	for _, f := range files {
		if err := createIfAbsent(filepath.Join(dir, f.name), []byte(f.content), f.name, res); err != nil {
			return nil, err
		}
	}
	proj := filepath.Join(dir, "projects")
	if _, err := os.Stat(proj); err == nil {
		res.Existed = append(res.Existed, "projects")
	} else if err := os.Mkdir(proj, 0o755); err != nil {
		return nil, err
	} else {
		res.Created = append(res.Created, "projects")
	}
	sort.Strings(res.Created)
	sort.Strings(res.Existed)
	return res, nil
}

// ScaffoldProject creates projects/<name>/{briefs,critiques,packages}/
// with .gitkeep files. runs/ and verdicts.jsonl appear on first use.
func ScaffoldProject(root, name string) (*ScaffoldResult, error) {
	if !isRoot(root) {
		return nil, fmt.Errorf("%s: %w", root, ErrNoWorkspace)
	}
	if !projectNameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid project name %q (want lowercase letters, digits, hyphens)", name)
	}
	res := &ScaffoldResult{}
	for _, sub := range []string{"briefs", "critiques", "packages"} {
		d := filepath.Join(root, "projects", name, sub)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
		rel := filepath.ToSlash(filepath.Join("projects", name, sub, ".gitkeep"))
		if err := createIfAbsent(filepath.Join(d, ".gitkeep"), nil, rel, res); err != nil {
			return nil, err
		}
	}
	sort.Strings(res.Created)
	sort.Strings(res.Existed)
	return res, nil
}

// createIfAbsent writes content to path only if nothing exists there,
// recording the outcome under rel in res.
func createIfAbsent(path string, content []byte, rel string, res *ScaffoldResult) error {
	if _, err := os.Stat(path); err == nil {
		res.Existed = append(res.Existed, rel)
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	res.Created = append(res.Created, rel)
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/workspace/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/workspace/workspace.go internal/workspace/workspace_test.go
git commit -m "feat: workspace discovery and scaffolding"
```

---

### Task 6: init command and output plumbing

**Files:**
- Modify: `cmd/splatter/root.go` (add `emit` helper, register subcommand)
- Create: `cmd/splatter/init.go`
- Test: `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `workspace.ScaffoldWorkspace`, `workspace.ScaffoldProject`, `workspace.FindRoot`, `workspace.ErrNoWorkspace` (Task 5); `usageErr` (Task 1).
- Produces: `emit(cmd *cobra.Command, result any, human string) error` in root.go — every command's single output path (JSON object when `--json`, human text otherwise, both to `cmd.OutOrStdout()`). Test helper `runCLI(t, dir, args...) (stdout string, err error)` reused by Tasks 7–8. `newInitCmd()` registered on root.

- [ ] **Step 1: Write the failing tests**

`cmd/splatter/cli_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI executes the CLI in-process with dir as working directory,
// returning captured stdout. Reused by init, validate, and status tests.
func runCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	// reset persistent flag state between runs
	jsonOut = false
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestInitScaffoldsWorkspace(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLI(t, dir, "init")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".gitattributes", ".gitignore", "providers.yaml", "pricing.yaml", "projects"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	if !strings.Contains(out, "created") {
		t.Fatalf("output should list created files: %q", out)
	}
}

func TestInitJSONOutput(t *testing.T) {
	dir := t.TempDir()
	out, err := runCLI(t, dir, "init", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Mode    string   `json:"mode"`
		Created []string `json:"created"`
		Existed []string `json:"existed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout not a JSON object: %v\n%q", err, out)
	}
	if res.Mode != "workspace" || len(res.Created) == 0 {
		t.Fatalf("bad result: %+v", res)
	}
}

func TestInitIdempotentRerun(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, dir, "init", "--json")
	if err != nil {
		t.Fatalf("re-run must succeed: %v", err)
	}
	var res struct {
		Created []string `json:"created"`
		Existed []string `json:"existed"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 0 || len(res.Existed) == 0 {
		t.Fatalf("re-run: %+v", res)
	}
}

func TestInitProject(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "init", "gradient-descent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "gradient-descent", "briefs", ".gitkeep")); err != nil {
		t.Fatal(err)
	}
}

func TestInitProjectOutsideWorkspaceIsUsageError(t *testing.T) {
	_, err := runCLI(t, t.TempDir(), "init", "gradient-descent")
	if err == nil {
		t.Fatal("want error")
	}
	var u usageErr
	if !errors.As(err, &u) {
		t.Fatalf("want usageErr (exit 2), got %T: %v", err, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/splatter/ -v`
Expected: FAIL — the test file compiles (it only references existing symbols), and every test errors with `unknown command "init" for "splatter"`.

- [ ] **Step 3: Implement emit and the init command**

Add to `cmd/splatter/root.go` (inside `newRootCmd`, before `return root`):

```go
	root.AddCommand(newInitCmd())
```

Add to `cmd/splatter/root.go` (file scope):

```go
// emit is every command's single output path: the result object as JSON
// when --json is set, the human rendering otherwise. Logs go to stderr;
// only results go to stdout.
func emit(cmd *cobra.Command, result any, human string) error {
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		return enc.Encode(result)
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), human)
	return err
}
```

(and add `"encoding/json"`, `"fmt"` to root.go imports.)

`cmd/splatter/init.go`:

```go
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type initResult struct {
	Mode    string   `json:"mode"` // "workspace" | "project"
	Root    string   `json:"root"`
	Created []string `json:"created"`
	Existed []string `json:"existed"`
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [project]",
		Short: "Scaffold a workspace (no args) or a project (with name)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			var res *workspace.ScaffoldResult
			result := initResult{Mode: "workspace", Root: cwd}
			if len(args) == 0 {
				res, err = workspace.ScaffoldWorkspace(cwd)
			} else {
				result.Mode = "project"
				var root string
				root, err = workspace.FindRoot(cwd)
				if err != nil {
					return usageErr{fmt.Errorf("init %s: %w (run splatter init first)", args[0], err)}
				}
				result.Root = root
				res, err = workspace.ScaffoldProject(root, args[0])
			}
			if err != nil {
				return err
			}
			result.Created = res.Created
			result.Existed = res.Existed
			return emit(cmd, result, humanScaffold(result))
		},
	}
}

func humanScaffold(r initResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s at %s\n", r.Mode, r.Root)
	for _, p := range r.Created {
		fmt.Fprintf(&b, "  created %s\n", p)
	}
	for _, p := range r.Existed {
		fmt.Fprintf(&b, "  exists  %s\n", p)
	}
	if len(r.Created) == 0 {
		b.WriteString("nothing to do\n")
	}
	return b.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/splatter/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add cmd/splatter/
git commit -m "feat: init command scaffolds workspace and projects"
```

---

### Task 7: validate walk and command

**Files:**
- Create: `internal/workspace/validate.go`, `cmd/splatter/validate.go`
- Modify: `cmd/splatter/root.go` (register `newValidateCmd()`)
- Test: `internal/workspace/validate_test.go`, plus two cases appended to `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `schema.DecodeManifestLine`, `schema.DecodeVerdictLine`, `schema.Critique`, `schema.ParseBrief`, `schema.BriefSHA256` (Tasks 3–4); `fsio.AppendRecord` (Task 2, test fixtures only); `runCLI`, `emit`, `validationErr` (Tasks 1, 6).
- Produces:

```go
type Finding struct {
	Path    string `json:"path"`    // relative slash path from workspace root
	Message string `json:"message"`
}

// Validate walks every project (or just project if non-empty) and
// returns all findings. err is reserved for I/O failures, not findings.
func Validate(root, project string) ([]Finding, error)
```

- Checks, per project: every `briefs/*.md` parses; every `runs/*/manifest.jsonl` line decodes and validates (first line must be the run header, exactly one header per manifest); the header's `brief` path exists and its recomputed `BriefSHA256` matches `brief_sha256`; every image file that exists locally hashes to its record's `sha256` (absent files are **not** findings — they may live only in Spaces); `verdicts.jsonl` lines decode; every `critiques/*.json` parses, validates, references a run that exists, and only image IDs present in that run's manifest.
- Naming a `project` that doesn't exist returns an I/O-level error (the command maps it to `usageErr`).

- [ ] **Step 1: Write the failing tests**

`internal/workspace/validate_test.go`:

```go
package workspace

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
)

const testBrief = `---
id: b_001
project: gradient-descent
concept: one-line concept statement
aspect: square
---
Creative direction prose.
`

// buildFixture scaffolds a workspace with one project, one brief, one
// run (header + one successful call with a real image file), one
// verdict, one critique. Everything validates clean.
func buildFixture(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldProject(root, "gradient-descent"); err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(root, "projects", "gradient-descent")

	briefPath := filepath.Join(proj, "briefs", "b_001.md")
	if err := os.WriteFile(briefPath, []byte(testBrief), 0o644); err != nil {
		t.Fatal(err)
	}

	runDir := filepath.Join(proj, "runs", "r_0001")
	imgDir := filepath.Join(runDir, "images")
	if err := os.MkdirAll(imgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	img := []byte("fake-png-bytes")
	imgSum := sha256.Sum256(img)
	if err := os.WriteFile(filepath.Join(imgDir, "c_01_0.png"), img, 0o644); err != nil {
		t.Fatal(err)
	}

	manifest := filepath.Join(runDir, "manifest.jsonl")
	header := schema.RunHeader{
		V: 1, Type: "run", Run: "r_0001", Project: "gradient-descent",
		Brief: "briefs/b_001.md", BriefSHA256: schema.BriefSHA256([]byte(testBrief)),
		Iteration: 1, Created: time.Now().UTC(), Harness: "splatter test",
	}
	if err := fsio.AppendRecord(manifest, header); err != nil {
		t.Fatal(err)
	}
	usd := 0.27
	call := schema.CallRecord{
		V: 1, Type: "call", Run: "r_0001", Call: "c_01", TS: time.Now().UTC(),
		Provider: "gemini", ModelRequested: "m", ModelReturned: "m",
		Profile: "gemini-baseline", Operation: "generate",
		Request:  schema.CallRequest{Prompt: "p", N: 1, Aspect: "square"},
		Response: schema.CallResponse{LatencyMS: 100, HTTPStatus: 200},
		Cost:     schema.Cost{USD: &usd, Source: "reported"},
		Images: []schema.ImageRef{{ID: "c_01_0", File: "images/c_01_0.png",
			SHA256: hex.EncodeToString(imgSum[:]), W: 8, H: 8, AspectActual: "square"}},
		Raw: "raw/c_01.json",
	}
	if err := fsio.AppendRecord(manifest, call); err != nil {
		t.Fatal(err)
	}

	verdict := schema.Verdict{V: 1, Type: "verdict", Run: "r_0001",
		TS: time.Now().UTC(), Session: "2026-07-24", Keep: []string{"c_01_0"}}
	if err := fsio.AppendRecord(filepath.Join(proj, "verdicts.jsonl"), verdict); err != nil {
		t.Fatal(err)
	}

	crit := schema.Critique{V: 1, Run: "r_0001", Rubric: "rubric_v1",
		Items: []schema.CritiqueItem{{Image: "c_01_0", Verdict: "keep",
			Scores: map[string]int{"concept_fit": 4}, Reason: "fits"}}}
	cb, _ := json.Marshal(crit)
	if err := os.WriteFile(filepath.Join(proj, "critiques", "r_0001.json"), cb, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func mustFindingContaining(t *testing.T, findings []Finding, substr string) {
	t.Helper()
	for _, f := range findings {
		if strings.Contains(f.Message, substr) || strings.Contains(f.Path, substr) {
			return
		}
	}
	t.Fatalf("no finding mentioning %q in %#v", substr, findings)
}

func TestValidateCleanWorkspace(t *testing.T) {
	root := buildFixture(t)
	findings, err := Validate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("clean fixture must have no findings: %#v", findings)
	}
}

func TestValidateFreshScaffoldIsClean(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	findings, err := Validate(root, "")
	if err != nil || len(findings) != 0 {
		t.Fatalf("fresh workspace: findings=%#v err=%v", findings, err)
	}
}

func TestValidateFindsCorruption(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(t *testing.T, root, proj string)
		expect  string
	}{
		{"malformed manifest line", func(t *testing.T, root, proj string) {
			f, err := os.OpenFile(filepath.Join(proj, "runs", "r_0001", "manifest.jsonl"),
				os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString("{not json\n")
			f.Close()
		}, "malformed"},
		{"missing required field", func(t *testing.T, root, proj string) {
			f, err := os.OpenFile(filepath.Join(proj, "runs", "r_0001", "manifest.jsonl"),
				os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString(`{"v":1,"type":"call","run":"r_0001"}` + "\n")
			f.Close()
		}, "missing required field"},
		{"brief hash mismatch", func(t *testing.T, root, proj string) {
			p := filepath.Join(proj, "briefs", "b_001.md")
			if err := os.WriteFile(p, []byte(strings.Replace(testBrief,
				"one-line", "edited", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "brief_sha256"},
		{"image hash mismatch", func(t *testing.T, root, proj string) {
			p := filepath.Join(proj, "runs", "r_0001", "images", "c_01_0.png")
			if err := os.WriteFile(p, []byte("tampered"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "sha256"},
		{"critique unknown image", func(t *testing.T, root, proj string) {
			crit := schema.Critique{V: 1, Run: "r_0001", Rubric: "rubric_v1",
				Items: []schema.CritiqueItem{{Image: "c_99_9", Verdict: "keep", Reason: "?"}}}
			cb, _ := json.Marshal(crit)
			if err := os.WriteFile(filepath.Join(proj, "critiques", "r_0001.json"), cb, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "c_99_9"},
		{"critique unknown run", func(t *testing.T, root, proj string) {
			crit := schema.Critique{V: 1, Run: "r_9999", Rubric: "rubric_v1"}
			cb, _ := json.Marshal(crit)
			if err := os.WriteFile(filepath.Join(proj, "critiques", "r_9999.json"), cb, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "r_9999"},
		{"bad verdict line", func(t *testing.T, root, proj string) {
			f, err := os.OpenFile(filepath.Join(proj, "verdicts.jsonl"),
				os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString(`{"v":1,"type":"verdict"}` + "\n")
			f.Close()
		}, "missing required field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := buildFixture(t)
			proj := filepath.Join(root, "projects", "gradient-descent")
			tc.corrupt(t, root, proj)
			findings, err := Validate(root, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) == 0 {
				t.Fatal("corruption not detected")
			}
			mustFindingContaining(t, findings, tc.expect)
		})
	}
}

func TestValidateMissingImageFileIsNotAFinding(t *testing.T) {
	root := buildFixture(t)
	proj := filepath.Join(root, "projects", "gradient-descent")
	if err := os.Remove(filepath.Join(proj, "runs", "r_0001", "images", "c_01_0.png")); err != nil {
		t.Fatal(err)
	}
	findings, err := Validate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("absent PNG may live only in Spaces; findings: %#v", findings)
	}
}

func TestValidateUnknownProjectErrors(t *testing.T) {
	root := buildFixture(t)
	if _, err := Validate(root, "no-such-project"); err == nil {
		t.Fatal("want error for unknown project")
	}
}

func TestValidateCollectsAllFindings(t *testing.T) {
	root := buildFixture(t)
	proj := filepath.Join(root, "projects", "gradient-descent")
	// two independent corruptions
	os.WriteFile(filepath.Join(proj, "briefs", "b_001.md"), []byte("no front matter"), 0o644)
	f, _ := os.OpenFile(filepath.Join(proj, "verdicts.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{bad\n")
	f.Close()
	findings, err := Validate(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) < 2 {
		t.Fatalf("want all findings reported, got %#v", findings)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/workspace/ -run TestValidate -v`
Expected: FAIL — `undefined: Validate`, `undefined: Finding`.

- [ ] **Step 3: Write validate.go**

`internal/workspace/validate.go`:

```go
package workspace

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cajundata/splatter/internal/schema"
)

type Finding struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Validate walks every project (or just the named one) and returns all
// findings. The error return is reserved for I/O failures.
func Validate(root, project string) ([]Finding, error) {
	names, err := projectNames(root, project)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, name := range names {
		pf, err := validateProject(root, name)
		if err != nil {
			return nil, err
		}
		findings = append(findings, pf...)
	}
	return findings, nil
}

func projectNames(root, only string) ([]string, error) {
	if only != "" {
		if fi, err := os.Stat(filepath.Join(root, "projects", only)); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("project %q not found", only)
		}
		return []string{only}, nil
	}
	entries, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func validateProject(root, name string) ([]Finding, error) {
	proj := filepath.Join(root, "projects", name)
	rel := func(abs string) string {
		r, err := filepath.Rel(root, abs)
		if err != nil {
			return abs
		}
		return filepath.ToSlash(r)
	}
	var findings []Finding
	add := func(abs, msg string) { findings = append(findings, Finding{Path: rel(abs), Message: msg}) }

	// briefs/*.md parse
	briefPaths, _ := filepath.Glob(filepath.Join(proj, "briefs", "*.md"))
	for _, bp := range briefPaths {
		data, err := os.ReadFile(bp)
		if err != nil {
			return nil, err
		}
		if _, _, err := schema.ParseBrief(data); err != nil {
			add(bp, err.Error())
		}
	}

	// runs/*/manifest.jsonl: decode lines, collect headers and image IDs
	runImages := map[string]map[string]bool{} // run id -> image id set
	runDirs, _ := filepath.Glob(filepath.Join(proj, "runs", "r_*"))
	for _, rd := range runDirs {
		mp := filepath.Join(rd, "manifest.jsonl")
		f, err := os.Open(mp)
		if os.IsNotExist(err) {
			add(rd, "run directory without manifest.jsonl")
			continue
		}
		if err != nil {
			return nil, err
		}
		lineNo := 0
		var header *schema.RunHeader
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for sc.Scan() {
			lineNo++
			decoded, err := schema.DecodeManifestLine(sc.Bytes())
			if err != nil {
				add(mp, fmt.Sprintf("line %d: %v", lineNo, err))
				continue
			}
			switch r := decoded.(type) {
			case *schema.RunHeader:
				if lineNo != 1 {
					add(mp, fmt.Sprintf("line %d: run header must be first line", lineNo))
				} else {
					header = r
				}
			case *schema.CallRecord:
				if lineNo == 1 {
					add(mp, "line 1: manifest must start with run header")
				}
				set, ok := runImages[r.Run]
				if !ok {
					set = map[string]bool{}
					runImages[r.Run] = set
				}
				for _, img := range r.Images {
					set[img.ID] = true
					checkImageHash(rd, img, add)
				}
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
		if header != nil {
			if _, ok := runImages[header.Run]; !ok {
				runImages[header.Run] = map[string]bool{}
			}
			checkBriefHash(proj, header, add)
		}
	}

	// verdicts.jsonl (optional until first verdict)
	vp := filepath.Join(proj, "verdicts.jsonl")
	if f, err := os.Open(vp); err == nil {
		lineNo := 0
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			lineNo++
			if _, err := schema.DecodeVerdictLine(sc.Bytes()); err != nil {
				add(vp, fmt.Sprintf("line %d: %v", lineNo, err))
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// critiques/*.json
	critPaths, _ := filepath.Glob(filepath.Join(proj, "critiques", "*.json"))
	for _, cp := range critPaths {
		data, err := os.ReadFile(cp)
		if err != nil {
			return nil, err
		}
		var crit schema.Critique
		if err := json.Unmarshal(data, &crit); err != nil {
			add(cp, fmt.Sprintf("malformed JSON: %v", err))
			continue
		}
		if err := crit.Validate(); err != nil {
			add(cp, err.Error())
			continue
		}
		known, ok := runImages[crit.Run]
		if !ok {
			add(cp, fmt.Sprintf("references unknown run %s", crit.Run))
			continue
		}
		for _, item := range crit.Items {
			if !known[item.Image] {
				add(cp, fmt.Sprintf("references unknown image %s", item.Image))
			}
		}
	}
	return findings, nil
}

// checkBriefHash recomputes the brief hash referenced by a run header.
func checkBriefHash(proj string, h *schema.RunHeader, add func(string, string)) {
	bp := filepath.Join(proj, filepath.FromSlash(h.Brief))
	data, err := os.ReadFile(bp)
	if err != nil {
		add(bp, fmt.Sprintf("brief referenced by run %s not readable: %v", h.Run, err))
		return
	}
	if got := schema.BriefSHA256(data); got != h.BriefSHA256 {
		add(bp, fmt.Sprintf("brief_sha256 mismatch for run %s: manifest %s, file %s",
			h.Run, short(h.BriefSHA256), short(got)))
	}
}

// checkImageHash verifies a locally-present image file against its
// manifest sha256. Absent files are fine — they may live only in Spaces.
func checkImageHash(runDir string, img schema.ImageRef, add func(string, string)) {
	ip := filepath.Join(runDir, filepath.FromSlash(img.File))
	data, err := os.ReadFile(ip)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		add(ip, fmt.Sprintf("unreadable: %v", err))
		return
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != img.SHA256 {
		add(ip, fmt.Sprintf("image sha256 mismatch: manifest %s, file %s",
			short(img.SHA256), short(got)))
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/workspace/ -v`
Expected: PASS (all workspace tests).

- [ ] **Step 5: Commit**

```bash
git add internal/workspace/validate.go internal/workspace/validate_test.go
git commit -m "feat: workspace validation walk"
```

- [ ] **Step 6: Write the failing command tests**

Append to `cmd/splatter/cli_test.go`:

```go
func TestValidateCleanWorkspaceExitsZero(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, dir, "validate", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		OK       bool `json:"ok"`
		Findings []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, out)
	}
	if !res.OK || len(res.Findings) != 0 {
		t.Fatalf("clean workspace: %+v", res)
	}
}

func TestValidateFindingsAreValidationError(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "init", "p1"); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "projects", "p1", "briefs", "b_001.md")
	if err := os.WriteFile(bad, []byte("no front matter"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runCLI(t, dir, "validate")
	if err == nil {
		t.Fatal("want error")
	}
	var v validationErr
	if !errors.As(err, &v) {
		t.Fatalf("want validationErr (exit 3), got %T: %v", err, err)
	}
	_, err = runCLI(t, dir, "validate", "--project", "nope")
	var u usageErr
	if !errors.As(err, &u) {
		t.Fatalf("want usageErr for unknown project, got %T: %v", err, err)
	}
}
```

- [ ] **Step 7: Run tests to verify they fail**

Run: `go test ./cmd/splatter/ -run TestValidate -v`
Expected: FAIL — `unknown command "validate"`.

- [ ] **Step 8: Implement the validate command**

Add to `newRootCmd` in `cmd/splatter/root.go`:

```go
	root.AddCommand(newValidateCmd())
```

`cmd/splatter/validate.go`:

```go
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type validateResult struct {
	OK       bool                `json:"ok"`
	Root     string              `json:"root"`
	Findings []workspace.Finding `json:"findings"`
}

func newValidateCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check schemas, hashes, and critique files across the workspace",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := workspace.FindRoot(cwd)
			if err != nil {
				return usageErr{err}
			}
			findings, err := workspace.Validate(root, project)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					return usageErr{err}
				}
				return err
			}
			res := validateResult{OK: len(findings) == 0, Root: root, Findings: findings}
			if res.Findings == nil {
				res.Findings = []workspace.Finding{}
			}
			var b strings.Builder
			if res.OK {
				fmt.Fprintf(&b, "ok: %s\n", root)
			} else {
				fmt.Fprintf(&b, "%d finding(s) in %s\n", len(findings), root)
				for _, f := range findings {
					fmt.Fprintf(&b, "  %s: %s\n", f.Path, f.Message)
				}
			}
			if err := emit(cmd, res, b.String()); err != nil {
				return err
			}
			if !res.OK {
				return validationErr{fmt.Errorf("%d validation finding(s)", len(findings))}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "validate a single project")
	return cmd
}
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `go test ./... `
Expected: PASS across all packages.

- [ ] **Step 10: Commit**

```bash
git add cmd/splatter/
git commit -m "feat: validate command with exit code 3 on findings"
```

---

### Task 8: status command

**Files:**
- Create: `internal/workspace/status.go`, `cmd/splatter/status.go`
- Modify: `cmd/splatter/root.go` (register `newStatusCmd()`)
- Test: `internal/workspace/status_test.go`, one case appended to `cmd/splatter/cli_test.go`

**Interfaces:**
- Consumes: `FindRoot`, fixture helpers from Task 7's test file (`buildFixture`); `emit`, `runCLI`.
- Produces:

```go
type ProjectStatus struct {
	Name      string `json:"name"`
	Briefs    int    `json:"briefs"`    // briefs/*.md count
	Runs      int    `json:"runs"`      // runs/r_* dir count
	Verdicts  int    `json:"verdicts"`  // verdicts.jsonl line count
	Critiques int    `json:"critiques"` // critiques/*.json count
}

func Status(root string) ([]ProjectStatus, error) // sorted by Name
```

- [ ] **Step 1: Write the failing tests**

`internal/workspace/status_test.go`:

```go
package workspace

import (
	"testing"
)

func TestStatusCounts(t *testing.T) {
	root := buildFixture(t)
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("want 1 project, got %#v", statuses)
	}
	s := statuses[0]
	if s.Name != "gradient-descent" || s.Briefs != 1 || s.Runs != 1 || s.Verdicts != 1 || s.Critiques != 1 {
		t.Fatalf("bad counts: %#v", s)
	}
}

func TestStatusEmptyWorkspace(t *testing.T) {
	root := t.TempDir()
	if _, err := ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	statuses, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 0 {
		t.Fatalf("want no projects, got %#v", statuses)
	}
}
```

Append to `cmd/splatter/cli_test.go`:

```go
func TestStatusJSON(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCLI(t, dir, "init", "p1"); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, dir, "status", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Root     string `json:"root"`
		Sync     string `json:"sync"`
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("stdout not JSON: %v\n%q", err, out)
	}
	if res.Sync != "not configured" || len(res.Projects) != 1 || res.Projects[0].Name != "p1" {
		t.Fatalf("bad result: %+v", res)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/workspace/ ./cmd/splatter/ -run TestStatus -v`
Expected: FAIL — `undefined: Status`, then `unknown command "status"`.

- [ ] **Step 3: Implement Status and the status command**

`internal/workspace/status.go`:

```go
package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
)

type ProjectStatus struct {
	Name      string `json:"name"`
	Briefs    int    `json:"briefs"`
	Runs      int    `json:"runs"`
	Verdicts  int    `json:"verdicts"`
	Critiques int    `json:"critiques"`
}

// Status reports per-project evidence counts, sorted by project name.
func Status(root string) ([]ProjectStatus, error) {
	names, err := projectNames(root, "")
	if err != nil {
		return nil, err
	}
	var out []ProjectStatus
	for _, name := range names {
		proj := filepath.Join(root, "projects", name)
		s := ProjectStatus{Name: name}
		briefs, _ := filepath.Glob(filepath.Join(proj, "briefs", "*.md"))
		s.Briefs = len(briefs)
		runs, _ := filepath.Glob(filepath.Join(proj, "runs", "r_*"))
		s.Runs = len(runs)
		crits, _ := filepath.Glob(filepath.Join(proj, "critiques", "*.json"))
		s.Critiques = len(crits)
		if f, err := os.Open(filepath.Join(proj, "verdicts.jsonl")); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				s.Verdicts++
			}
			f.Close()
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
```

Add to `newRootCmd` in `cmd/splatter/root.go`:

```go
	root.AddCommand(newStatusCmd())
```

`cmd/splatter/status.go`:

```go
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type statusResult struct {
	Root     string                    `json:"root"`
	Sync     string                    `json:"sync"`
	Projects []workspace.ProjectStatus `json:"projects"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Workspace state and pending sync",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := workspace.FindRoot(cwd)
			if err != nil {
				return usageErr{err}
			}
			projects, err := workspace.Status(root)
			if err != nil {
				return err
			}
			if projects == nil {
				projects = []workspace.ProjectStatus{}
			}
			// Spaces sync arrives in S4; report honestly until then.
			res := statusResult{Root: root, Sync: "not configured", Projects: projects}
			var b strings.Builder
			fmt.Fprintf(&b, "workspace: %s\nsync: %s\n", res.Root, res.Sync)
			for _, p := range res.Projects {
				fmt.Fprintf(&b, "  %-24s briefs:%d runs:%d verdicts:%d critiques:%d\n",
					p.Name, p.Briefs, p.Runs, p.Verdicts, p.Critiques)
			}
			if len(res.Projects) == 0 {
				b.WriteString("  no projects\n")
			}
			return emit(cmd, res, b.String())
		},
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 5: Commit**

```bash
git add internal/workspace/status.go internal/workspace/status_test.go cmd/splatter/
git commit -m "feat: status command with per-project evidence counts"
```

---

### Task 9: Cross-compile proof and Windows checklist

**Files:**
- Create: `docs/windows-checklist.md`

**Interfaces:**
- Consumes: `make build-all` (Task 1), the full CLI (Tasks 6–8).

- [ ] **Step 1: Run the full test suite and cross-compile**

```bash
make test       # expected: all packages PASS
make build-all  # expected: bin/darwin-arm64/splatter and bin/windows-amd64/splatter.exe exist
file bin/windows-amd64/splatter.exe   # expected: PE32+ executable, x86-64
file bin/darwin-arm64/splatter        # expected: Mach-O 64-bit arm64
```

- [ ] **Step 2: Run the macOS half of the exit criterion live**

```bash
WS=$(mktemp -d)
cd "$WS"
/Users/weldon/projects/splatter/bin/darwin-arm64/splatter init && \
  /Users/weldon/projects/splatter/bin/darwin-arm64/splatter validate
echo "exit: $?"   # expected: workspace scaffold listing, then "ok: <dir>", exit: 0
/Users/weldon/projects/splatter/bin/darwin-arm64/splatter init gradient-descent
/Users/weldon/projects/splatter/bin/darwin-arm64/splatter status
cd - && rm -rf "$WS"
```

Expected: exit 0 at every step; status lists `gradient-descent` with zero counts.

- [ ] **Step 3: Write the Windows verification checklist**

`docs/windows-checklist.md`:

```markdown
# S1 Windows Verification Checklist

Run in PowerShell 7 on the Windows machine with
`bin/windows-amd64/splatter.exe` copied somewhere on PATH as
`splatter.exe`. Report results back; S1's exit criterion is not fully met
until every box is checked.

- [ ] `mkdir ws; cd ws; splatter init` — scaffold listing, exit code 0
      (`$LASTEXITCODE`)
- [ ] `splatter validate` — prints `ok: <path>`, exit code 0
- [ ] `splatter init` again — reports all files as existing, exit 0
- [ ] `splatter init gradient-descent` — creates project, exit 0
- [ ] `splatter status --json` — one JSON object, `"sync":"not configured"`
- [ ] `splatter nonsense` — error to stderr, exit code 2
- [ ] Open `.gitattributes` in an editor that shows line endings — file
      content contains `eol=lf`
- [ ] `git init; git add -A; git commit -m x` inside ws, then
      `git ls-files --eol` — all text files show `i/lf`
```

- [ ] **Step 4: Commit**

```bash
git add docs/windows-checklist.md
git commit -m "docs: S1 Windows verification checklist"
```

---

## Exit Criterion Recap

S1 is done when:

1. `go test ./...` green (schema round-trip included) — verified in Task 9 Step 1.
2. `splatter init && splatter validate` passes on a fresh workspace on macOS — verified live in Task 9 Step 2.
3. Same on Windows — deferred to the operator via `docs/windows-checklist.md` (decision recorded in the design doc).
4. Both target binaries cross-compile — Task 9 Step 1.
