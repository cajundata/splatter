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
