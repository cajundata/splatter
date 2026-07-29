package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
)

// runCLI executes the CLI in-process with dir as working directory,
// returning captured stdout. Reused by init, validate, and status tests.
func runCLI(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(dir)
	// reset persistent flag state between runs
	jsonOut = false
	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err := root.Execute()
	return outBuf.String(), err
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

func TestInitInvalidProjectNameIsUsageError(t *testing.T) {
	dir := t.TempDir()
	if _, err := runCLI(t, dir, "init"); err != nil {
		t.Fatal(err)
	}
	_, err := runCLI(t, dir, "init", "Bad Name")
	if err == nil {
		t.Fatal("want error")
	}
	var u usageErr
	if !errors.As(err, &u) {
		t.Fatalf("want usageErr (exit 2), got %T: %v", err, err)
	}
}

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

type cliFakeProvider struct {
	fail         bool
	preflightErr error
}

func (f *cliFakeProvider) Preflight(req provider.Request) error { return f.preflightErr }

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
		{"fan", "--brief", brief}, // neither
		{"fan", "--brief", brief, "--set", "baseline", "--profiles", "a"},                   // both
		{"fan", "--brief", brief, "--set", "nope"},                                          // unknown set
		{"fan", "--brief", brief, "--profiles", "nope"},                                     // unknown profile
		{"fan", "--set", "baseline"},                                                        // missing brief
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
		{"sheet"},                    // missing --run
		{"sheet", "--run", "r_0099"}, // unknown run
	} {
		_, err := runCLI(t, dir, args...)
		var u usageErr
		if !errors.As(err, &u) {
			t.Fatalf("%v: want usageErr, got %T: %v", args, err, err)
		}
	}
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
		{"verdict", "--run", "r_0001", "--keep", "c_99_9"},                     // unknown keep id
		{"verdict", "--run", "r_0001", "--cull", "c_99_9"},                     // unknown cull id
		{"verdict", "--run", "r_0001", "--note", "c_99_9: unknown image note"}, // unknown note id
		{"verdict", "--run", "r_0001", "--keep", "c_01_0", "--cull", "c_01_0"}, // overlap
		{"verdict", "--run", "r_0001"},                                         // empty verdict
		{"verdict", "--run", "r_0099", "--keep", "c_01_0"},                     // unknown run
		{"verdict", "--keep", "c_01_0"},                                        // missing --run
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
