package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
		{"empty manifest", func(t *testing.T, root, proj string) {
			p := filepath.Join(proj, "runs", "r_0001", "manifest.jsonl")
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}, "no run header"},
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
	_, err := Validate(root, "no-such-project")
	if err == nil {
		t.Fatal("want error for unknown project")
	}
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
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
