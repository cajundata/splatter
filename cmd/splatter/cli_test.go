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
