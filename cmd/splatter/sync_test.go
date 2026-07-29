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
