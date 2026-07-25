package run

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
)

const genBrief = `---
id: b_001
project: gradient-descent
concept: one-line concept statement
aspect: square
---
Creative direction prose.
`

// fakeProvider implements provider.Provider for orchestration tests.
type fakeProvider struct {
	caps   provider.Capabilities
	result provider.Result
	err    error
	gotReq provider.Request
}

func (f *fakeProvider) Name() string                        { return "fake" }
func (f *fakeProvider) Capabilities() provider.Capabilities { return f.caps }
func (f *fakeProvider) Generate(ctx context.Context, req provider.Request) (provider.Result, error) {
	f.gotReq = req
	return f.result, f.err
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func setupWorkspace(t *testing.T) (root, briefPath string) {
	t.Helper()
	root = t.TempDir()
	if _, err := workspace.ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.ScaffoldProject(root, "gradient-descent"); err != nil {
		t.Fatal(err)
	}
	briefPath = filepath.Join(root, "projects", "gradient-descent", "briefs", "b_001.md")
	if err := os.WriteFile(briefPath, []byte(genBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, briefPath
}

func successProvider(t *testing.T) *fakeProvider {
	img := pngBytes(t)
	rawBody, _ := json.Marshal(map[string]any{
		"data": base64.StdEncoding.EncodeToString(make([]byte, 5000)),
		"note": "wire response",
	})
	return &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4, AspectModes: []string{"1:1"}},
		result: provider.Result{
			Images:       []provider.Image{{Bytes: img, W: 2, H: 2}},
			Latency:      123 * time.Millisecond,
			Meta:         provider.CallMeta{ModelReturned: "fake-model-9", HTTPStatus: 200, ProviderRequestID: "req_f"},
			Raw:          rawBody,
			AspectActual: "1:1",
		},
	}
}

func genParams(root, briefPath string, p provider.Provider) GenParams {
	return GenParams{
		Root: root, BriefPath: briefPath, ProfileID: "test-profile",
		Profile: config.Profile{Provider: "fake", Model: "fake-model", Native: map[string]any{"k": "v"}},
		N:       1, Harness: "splatter test",
		Pricing: &config.Pricing{Version: "2026-07-25.0",
			Models: map[string]config.ModelPrice{"fake-model-9": {USDPerImage: 0.05}}},
		Provider: p,
	}
}

func TestGenWritesCompleteEvidence(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := successProvider(t)
	res, err := Gen(context.Background(), genParams(root, briefPath, fake))
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed || res.Run != "r_0001" || res.Call != "c_01" || res.Project != "gradient-descent" {
		t.Fatalf("result: %+v", res)
	}
	runDir := filepath.Join(root, "projects", "gradient-descent", "runs", "r_0001")

	// image file written and hash matches record
	imgPath := filepath.Join(runDir, "images", "c_01_0.png")
	imgData, err := os.ReadFile(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(imgData)
	if res.Images[0].SHA256 != hex.EncodeToString(sum[:]) || res.Images[0].File != "images/c_01_0.png" {
		t.Fatalf("image ref: %#v", res.Images[0])
	}

	// raw sidecar exists and is redacted
	rawData, err := os.ReadFile(filepath.Join(runDir, "raw", "c_01.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawData), "$blob") || strings.Contains(string(rawData), base64.StdEncoding.EncodeToString(make([]byte, 100))) {
		t.Fatalf("sidecar not redacted: %s", rawData)
	}

	// manifest: header line then call line, both schema-valid
	mf, err := os.ReadFile(filepath.Join(runDir, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(mf), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("want 2 manifest lines, got %d", len(lines))
	}
	h, err := schema.DecodeManifestLine(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	header := h.(*schema.RunHeader)
	if header.Run != "r_0001" || header.Project != "gradient-descent" ||
		header.BriefSHA256 != schema.BriefSHA256([]byte(genBrief)) ||
		header.Brief != "briefs/b_001.md" || header.Harness != "splatter test" {
		t.Fatalf("header: %+v", header)
	}
	c, err := schema.DecodeManifestLine(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	call := c.(*schema.CallRecord)
	if call.Provider != "fake" || call.ModelRequested != "fake-model" || call.ModelReturned != "fake-model-9" ||
		call.Profile != "test-profile" || call.Operation != "generate" ||
		call.Response.LatencyMS != 123 || call.Response.HTTPStatus != 200 ||
		call.Cost.Source != "table:2026-07-25.0" || *call.Cost.USD != 0.05 ||
		call.Raw != "raw/c_01.json" || call.Request.Native["k"] != "v" {
		t.Fatalf("call: %+v", call)
	}
	// prompt passed through from brief body via request
	if fake.gotReq.Prompt == "" || fake.gotReq.Aspect != "square" || fake.gotReq.N != 1 {
		t.Fatalf("provider request: %+v", fake.gotReq)
	}
}

func TestGenSecondRunAllocatesNextID(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	for i := 0; i < 2; i++ {
		if _, err := Gen(context.Background(), genParams(root, briefPath, successProvider(t))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "projects", "gradient-descent", "runs", "r_0002", "manifest.jsonl")); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func TestGenProviderFailureWritesFailedRecord(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4},
		result: provider.Result{Latency: 55 * time.Millisecond, Raw: []byte(`{"error":"x"}`),
			Meta: provider.CallMeta{HTTPStatus: 429}},
		err: &provider.Error{Stage: "request", HTTPStatus: 429, Message: "rate limited"},
	}
	res, err := Gen(context.Background(), genParams(root, briefPath, fake))
	if err != nil {
		t.Fatalf("provider failure must not be a Gen error: %v", err)
	}
	if !res.Failed || res.Error == nil || res.Error.Stage != "request" || res.Error.HTTPStatus != 429 {
		t.Fatalf("result: %+v", res)
	}
	runDir := filepath.Join(root, "projects", "gradient-descent", "runs", "r_0001")
	mf, _ := os.ReadFile(filepath.Join(runDir, "manifest.jsonl"))
	lines := bytes.Split(bytes.TrimSpace(mf), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("failed call still gets a record; lines=%d", len(lines))
	}
	call, err := schema.DecodeManifestLine(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	cr := call.(*schema.CallRecord)
	if len(cr.Images) != 0 || cr.Error == nil || cr.Error.Message != "rate limited" ||
		cr.Response.LatencyMS != 55 || cr.Cost.Source != "unavailable" {
		t.Fatalf("failed record: %+v", cr)
	}
	// failed exchange's raw body still becomes a sidecar
	if _, err := os.Stat(filepath.Join(runDir, "raw", "c_01.json")); err != nil {
		t.Fatalf("failure sidecar: %v", err)
	}
	if !bytes.Contains(lines[1], []byte(`"images":[]`)) {
		t.Fatalf("failed record must serialize images as [], not null: %s", lines[1])
	}
}

func TestGenConfigStageFailureLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4},
		err:  &provider.Error{Stage: "config", Message: "missing FAKE_API_KEY"},
	}
	_, err := Gen(context.Background(), genParams(root, briefPath, fake))
	if err == nil || !strings.Contains(err.Error(), "missing FAKE_API_KEY") {
		t.Fatalf("config-stage failure must be a Gen error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("config-stage failure must leave no run dirs: %v", entries)
	}
}

func TestGenCapabilityViolationLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	fake := &fakeProvider{caps: provider.Capabilities{MaxBatch: 1}}
	p := genParams(root, briefPath, fake)
	p.N = 4
	_, err := Gen(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "batch") {
		t.Fatalf("want capability error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("capability violation must not create a run: %v", entries)
	}
}

func TestGenUnscaffoldedProjectErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := workspace.ScaffoldWorkspace(root); err != nil {
		t.Fatal(err)
	}
	briefPath := filepath.Join(root, "brief.md")
	if err := os.WriteFile(briefPath, []byte(genBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Gen(context.Background(), genParams(root, briefPath, successProvider(t)))
	if err == nil || !strings.Contains(err.Error(), "splatter init gradient-descent") {
		t.Fatalf("want unscaffolded-project error, got %v", err)
	}
}

func TestGenRelativeBriefPath(t *testing.T) {
	root, _ := setupWorkspace(t)
	t.Chdir(root)
	p := genParams(root, filepath.Join("projects", "gradient-descent", "briefs", "b_001.md"), successProvider(t))
	res, err := Gen(context.Background(), p)
	if err != nil {
		t.Fatalf("cwd-relative brief path must work: %v", err)
	}
	if res.Run != "r_0001" {
		t.Fatalf("result: %+v", res)
	}
}

func TestGenBriefOutsideProjectLeavesNoTrace(t *testing.T) {
	root, _ := setupWorkspace(t)
	outside := filepath.Join(root, "stray-brief.md")
	if err := os.WriteFile(outside, []byte(genBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Gen(context.Background(), genParams(root, outside, successProvider(t)))
	if err == nil || !strings.Contains(err.Error(), "must live under") {
		t.Fatalf("want containment error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("rejected brief must leave no run dirs: %v", entries)
	}
}
