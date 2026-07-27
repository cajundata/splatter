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
