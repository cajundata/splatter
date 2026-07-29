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
