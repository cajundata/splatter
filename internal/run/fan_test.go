package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/provider/gemini"
	"github.com/cajundata/splatter/internal/provider/openai"
	"github.com/cajundata/splatter/internal/workspace"
)

// The pre-flight seam is discovered by type assertion at runtime; these
// assertions fail the build if either real adapter drifts off it.
var (
	_ preflighter = (*gemini.Adapter)(nil)
	_ preflighter = (*openai.Adapter)(nil)
)

// preflightFake is a fakeProvider with a controllable Preflight result.
type preflightFake struct {
	fakeProvider
	preflightErr error
}

func (f *preflightFake) Preflight(req provider.Request) error { return f.preflightErr }

func fanParams(root, briefPath string, profiles ...ResolvedProfile) FanParams {
	return FanParams{
		Root: root, BriefPath: briefPath, Profiles: profiles,
		N: 1, Harness: "splatter test",
	}
}

func resolved(id string, p provider.Provider) ResolvedProfile {
	return ResolvedProfile{ID: id, Profile: testProfile(), Provider: p}
}

func TestFanMixedSuccessAndFailure(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	good := successProvider(t)
	bad := &fakeProvider{
		caps: provider.Capabilities{MaxBatch: 4},
		result: provider.Result{Latency: 55000000, Raw: []byte(`{"error":"x"}`),
			Meta: provider.CallMeta{HTTPStatus: 429}},
		err: &provider.Error{Stage: "request", HTTPStatus: 429, Message: "rate limited"},
	}
	res, err := Fan(context.Background(), fanParams(root, briefPath,
		resolved("prof-a", good), resolved("prof-b", bad)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Run != "r_0001" || res.Succeeded != 1 || res.Failed != 1 || len(res.Calls) != 2 {
		t.Fatalf("result: %+v", res)
	}
	// sequential call IDs in profile order
	if res.Calls[0].Call != "c_01" || res.Calls[1].Call != "c_02" {
		t.Fatalf("call ids: %s, %s", res.Calls[0].Call, res.Calls[1].Call)
	}
	if res.Calls[0].Failed || !res.Calls[1].Failed || res.Calls[1].Error.HTTPStatus != 429 {
		t.Fatalf("outcomes: %+v", res.Calls)
	}
	if res.Calls[0].Profile != "prof-a" || res.Calls[1].Profile != "prof-b" {
		t.Fatalf("profiles: %+v", res.Calls)
	}
	// one run, header + two call records; failure never destroys the sibling
	runDir := filepath.Join(root, "projects", "gradient-descent", "runs", "r_0001")
	if _, err := os.Stat(filepath.Join(runDir, "images", "c_01_0.png")); err != nil {
		t.Fatalf("success images missing: %v", err)
	}
	findings, err := workspace.Validate(root, "gradient-descent")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("fan evidence must validate cleanly: %+v", findings)
	}
}

func TestFanAllFailedIsNotAFanError(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	bad := func() *fakeProvider {
		return &fakeProvider{
			caps: provider.Capabilities{MaxBatch: 4},
			result: provider.Result{Latency: 1000000, Raw: []byte(`{"error":"x"}`),
				Meta: provider.CallMeta{HTTPStatus: 500}},
			err: &provider.Error{Stage: "request", HTTPStatus: 500, Message: "boom"},
		}
	}
	res, err := Fan(context.Background(), fanParams(root, briefPath,
		resolved("prof-a", bad()), resolved("prof-b", bad())))
	if err != nil {
		t.Fatalf("all-failed fan still returns a result (exit mapping is the command's job): %v", err)
	}
	if res.Succeeded != 0 || res.Failed != 2 {
		t.Fatalf("result: %+v", res)
	}
}

func TestFanPreflightFailureLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	good := successProvider(t)
	bad := &preflightFake{preflightErr: &provider.Error{Stage: "config", Message: "missing FAKE_API_KEY"}}
	bad.caps = provider.Capabilities{MaxBatch: 4}
	_, err := Fan(context.Background(), fanParams(root, briefPath,
		resolved("prof-a", good), resolved("prof-b", bad)))
	if err == nil || !strings.Contains(err.Error(), "missing FAKE_API_KEY") ||
		!strings.Contains(err.Error(), "prof-b") {
		t.Fatalf("want pre-flight error naming the profile, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("pre-flight failure must leave no run dirs: %v", entries)
	}
	if len(good.gotReq.Prompt) != 0 {
		t.Fatal("pre-flight failure must abort before any provider call")
	}
}

func TestFanCapabilityViolationLeavesNoTrace(t *testing.T) {
	root, briefPath := setupWorkspace(t)
	small := &fakeProvider{caps: provider.Capabilities{MaxBatch: 1}}
	p := fanParams(root, briefPath, resolved("prof-a", small))
	p.N = 4
	_, err := Fan(context.Background(), p)
	if err == nil || !strings.Contains(err.Error(), "batch") {
		t.Fatalf("want capability error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "projects", "gradient-descent", "runs"))
	if len(entries) != 0 {
		t.Fatalf("capability violation must not create a run: %v", entries)
	}
}

func TestFanConfigErrorAfterPassingPreflightIsFailedCall(t *testing.T) {
	// Should be impossible with real adapters (Preflight shares checks with
	// Generate); if it happens anyway the header is already written, so the
	// call is recorded as failed — evidence preserved, never destroyed.
	root, briefPath := setupWorkspace(t)
	liar := &preflightFake{}
	liar.caps = provider.Capabilities{MaxBatch: 4}
	liar.err = &provider.Error{Stage: "config", Message: "surprise config failure"}
	res, err := Fan(context.Background(), fanParams(root, briefPath, resolved("prof-a", liar)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Calls[0].Error == nil || res.Calls[0].Error.Stage != "config" {
		t.Fatalf("result: %+v", res)
	}
}
