package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
)

// ResolvedProfile pairs a providers.yaml profile with its constructed
// adapter, ready to call.
type ResolvedProfile struct {
	ID       string
	Profile  config.Profile
	Provider provider.Provider
}

type FanParams struct {
	// Root is the workspace root and is expected to be an absolute path
	// (as returned by workspace.FindRoot). BriefPath may be cwd-relative.
	Root      string
	BriefPath string
	Profiles  []ResolvedProfile // one call per entry, in order
	N         int
	Harness   string
	Pricing   *config.Pricing
}

type CallOutcome struct {
	Call      string            `json:"call"`
	Profile   string            `json:"profile"`
	Provider  string            `json:"provider"`
	Images    []schema.ImageRef `json:"images"`
	Cost      schema.Cost       `json:"cost"`
	LatencyMS int64             `json:"latency_ms"`
	Failed    bool              `json:"failed"`
	Error     *schema.CallError `json:"error,omitempty"`
}

type FanResult struct {
	Project   string        `json:"project"`
	Run       string        `json:"run"`
	Calls     []CallOutcome `json:"calls"`
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
}

// preflighter is the optional pre-flight seam on concrete adapters,
// discovered by type assertion so the spec-frozen Provider interface
// (§6.1) stays untouched. Preflight runs the adapter's config-stage
// checks with zero network.
type preflighter interface {
	Preflight(provider.Request) error
}

// Fan runs one brief across every resolved profile as sequential calls
// in a single run. Every profile is pre-flighted before any disk write:
// a pre-flight or capability failure aborts the whole fan with an error
// and zero disk trace. After the run header is written, per-call
// failures land in failed-call records and never abort the fan; the
// error return from that point on is for I/O failures only.
func Fan(ctx context.Context, p FanParams) (*FanResult, error) {
	briefBytes, err := os.ReadFile(p.BriefPath)
	if err != nil {
		return nil, err
	}
	meta, body, err := schema.ParseBrief(briefBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.BriefPath, err)
	}
	projDir := filepath.Join(p.Root, "projects", meta.Project)
	if fi, err := os.Stat(projDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("project %q not scaffolded (run: splatter init %s)", meta.Project, meta.Project)
	}
	briefRel, err := containedBriefRel(p.BriefPath, projDir)
	if err != nil {
		return nil, err
	}
	if len(p.Profiles) == 0 {
		return nil, fmt.Errorf("no profiles to fan across")
	}

	// Pre-flight every profile: capability checks plus the adapter's own
	// config-stage checks when it implements Preflight.
	prompt := strings.TrimSpace(meta.Concept + "\n\n" + body)
	reqs := make([]provider.Request, len(p.Profiles))
	for i, rp := range p.Profiles {
		if p.N < 1 {
			return nil, fmt.Errorf("n must be >= 1")
		}
		caps := rp.Provider.Capabilities()
		if p.N > caps.MaxBatch {
			return nil, fmt.Errorf("profile %s: n=%d exceeds provider %s max batch %d",
				rp.ID, p.N, rp.Provider.Name(), caps.MaxBatch)
		}
		req := provider.Request{
			Model: rp.Profile.Model, Prompt: prompt, N: p.N,
			Aspect: meta.Aspect, Native: rp.Profile.Native,
		}
		if req.Seed != nil && !caps.Seed {
			return nil, fmt.Errorf("profile %s: seed not supported by provider %s", rp.ID, rp.Provider.Name())
		}
		if pf, ok := rp.Provider.(preflighter); ok {
			if err := pf.Preflight(req); err != nil {
				return nil, fmt.Errorf("profile %s: %w", rp.ID, err)
			}
		}
		reqs[i] = req
	}

	// All pre-checks pass; everything from here on is disk mutation.
	runID, runDir, err := allocateRun(projDir)
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"images", "raw"} {
		if err := os.MkdirAll(filepath.Join(runDir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	header := schema.RunHeader{
		V: 1, Type: "run", Run: runID, Project: meta.Project,
		Brief: filepath.ToSlash(briefRel), BriefSHA256: schema.BriefSHA256(briefBytes),
		Iteration: 1, Created: time.Now().UTC().Truncate(time.Second), Harness: p.Harness,
	}
	if err := fsio.AppendRecord(filepath.Join(runDir, "manifest.jsonl"), header); err != nil {
		return nil, err
	}

	result := &FanResult{Project: meta.Project, Run: runID, Calls: []CallOutcome{}}
	for i, rp := range p.Profiles {
		callID := fmt.Sprintf("c_%02d", i+1)
		record, err := executeCall(ctx, runDir, callID, rp.ID, rp.Profile, rp.Provider, reqs[i], p.Pricing)
		if err != nil {
			return nil, err
		}
		outcome := CallOutcome{
			Call: callID, Profile: rp.ID, Provider: rp.Provider.Name(),
			Images: record.Images, Cost: record.Cost,
			LatencyMS: record.Response.LatencyMS,
			Failed:    record.Error != nil, Error: record.Error,
		}
		if outcome.Images == nil {
			outcome.Images = []schema.ImageRef{}
		}
		if outcome.Failed {
			result.Failed++
		} else {
			result.Succeeded++
		}
		result.Calls = append(result.Calls, outcome)
	}
	return result, nil
}

// executeCall performs one provider invocation and writes its evidence
// under runDir: redacted raw sidecar, image files, and the call record
// appended to manifest.jsonl. The error return is for I/O failures
// only; provider failures (any stage — a config-stage error after a
// passing pre-flight should be impossible, but the header is already
// written, so evidence is preserved rather than destroyed) land inside
// the returned record's Error.
func executeCall(ctx context.Context, runDir, callID, profileID string, prof config.Profile,
	prov provider.Provider, req provider.Request, pricing *config.Pricing) (schema.CallRecord, error) {
	res, genErr := prov.Generate(ctx, req)

	manifest := filepath.Join(runDir, "manifest.jsonl")
	// Sidecar: written for success AND wire failures (evidence either way).
	rawRel := ""
	if len(res.Raw) > 0 {
		rawRel = "raw/" + callID + ".json"
		if err := writeNew(filepath.Join(runDir, "raw", callID+".json"), RedactRaw(res.Raw)); err != nil {
			return schema.CallRecord{}, err
		}
	}

	record := schema.CallRecord{
		V: 1, Type: "call", Run: filepath.Base(runDir), Call: callID,
		TS:       time.Now().UTC().Truncate(time.Second),
		Provider: prov.Name(), ModelRequested: prof.Model,
		ModelReturned: res.Meta.ModelReturned, Profile: profileID, Operation: "generate",
		Request: schema.CallRequest{Prompt: req.Prompt, N: req.N, Aspect: req.Aspect,
			Seed: req.Seed, Native: prof.Native},
		Response: schema.CallResponse{
			LatencyMS:         res.Latency.Milliseconds(),
			HTTPStatus:        res.Meta.HTTPStatus,
			ProviderRequestID: res.Meta.ProviderRequestID,
		},
		Raw: rawRel,
	}

	if genErr != nil {
		record.Error = callErrorFrom(genErr)
		record.Images = []schema.ImageRef{}
		record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, prof.Model, 0, pricing)
		return record, fsio.AppendRecord(manifest, record)
	}

	for i, img := range res.Images {
		id := fmt.Sprintf("%s_%d", callID, i)
		rel := "images/" + id + ".png"
		if err := writeNew(filepath.Join(runDir, "images", id+".png"), img.Bytes); err != nil {
			return schema.CallRecord{}, err
		}
		sum := sha256.Sum256(img.Bytes)
		record.Images = append(record.Images, schema.ImageRef{
			ID: id, File: rel, SHA256: hex.EncodeToString(sum[:]),
			W: img.W, H: img.H, AspectActual: res.AspectActual,
		})
	}
	record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, prof.Model, len(res.Images), pricing)
	return record, fsio.AppendRecord(manifest, record)
}

// containedBriefRel returns briefPath relative to projDir, erroring when
// the brief lives outside the project. briefPath is absolutized first
// (it may be cwd-relative) and symlinks are resolved on both sides so
// containment survives symlinked roots (e.g. macOS /tmp).
func containedBriefRel(briefPath, projDir string) (string, error) {
	absBrief, err := filepath.Abs(briefPath)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(absBrief); err == nil {
		absBrief = r
	}
	resolvedProj := projDir
	if r, err := filepath.EvalSymlinks(projDir); err == nil {
		resolvedProj = r
	}
	rel, err := filepath.Rel(resolvedProj, absBrief)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("brief %s must live under project dir %s", briefPath, projDir)
	}
	return rel, nil
}
