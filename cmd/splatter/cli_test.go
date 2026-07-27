package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
