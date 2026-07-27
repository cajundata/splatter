package run

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
)

type GenParams struct {
	// Root is the workspace root and is expected to be an absolute path
	// (as returned by workspace.FindRoot). BriefPath may be cwd-relative.
	Root      string
	BriefPath string
	ProfileID string
	Profile   config.Profile
	N         int
	Harness   string
	Pricing   *config.Pricing
	Provider  provider.Provider
}

type GenResult struct {
	Project   string            `json:"project"`
	Run       string            `json:"run"`
	Call      string            `json:"call"`
	Images    []schema.ImageRef `json:"images"`
	Cost      schema.Cost       `json:"cost"`
	LatencyMS int64             `json:"latency_ms"`
	Failed    bool              `json:"failed"`
	Error     *schema.CallError `json:"error,omitempty"`
}

// Gen runs one generation call end to end and writes all evidence. A
// config-stage provider error (missing API key, unsupported native key,
// etc.) never reaches the wire, so Gen returns it as a plain error with no
// evidence written at all — no run allocated, nothing on disk. A request-
// or decode-stage failure did reach the wire, so it produces a
// failed-call record (Failed=true) instead of a Gen error.
func Gen(ctx context.Context, p GenParams) (*GenResult, error) {
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

	// Brief-under-project containment check. Absolutize p.BriefPath first
	// (it may be cwd-relative) and resolve symlinks on both sides so
	// containment survives symlinked roots (e.g. macOS /tmp).
	absBrief, err := filepath.Abs(p.BriefPath)
	if err != nil {
		return nil, err
	}
	if r, err := filepath.EvalSymlinks(absBrief); err == nil {
		absBrief = r
	}
	resolvedProj := projDir
	if r, err := filepath.EvalSymlinks(projDir); err == nil {
		resolvedProj = r
	}
	briefRel, err := filepath.Rel(resolvedProj, absBrief)
	if err != nil || briefRel == ".." || strings.HasPrefix(briefRel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("brief %s must live under project dir %s", p.BriefPath, projDir)
	}

	// Capability checks: planning time, before any wire call or disk write.
	caps := p.Provider.Capabilities()
	if p.N < 1 {
		return nil, fmt.Errorf("n must be >= 1")
	}
	if p.N > caps.MaxBatch {
		return nil, fmt.Errorf("n=%d exceeds provider %s max batch %d", p.N, p.Provider.Name(), caps.MaxBatch)
	}

	// All pre-checks are complete; call the provider before touching disk.
	// A config-stage failure never reaches the wire, so nothing has been
	// allocated yet and there is nothing on disk to clean up on that path.
	callID := "c_01"
	req := provider.Request{
		Model:  p.Profile.Model,
		Prompt: strings.TrimSpace(meta.Concept + "\n\n" + body),
		N:      p.N,
		Aspect: meta.Aspect,
		Native: p.Profile.Native,
	}
	res, genErr := p.Provider.Generate(ctx, req)
	var pe *provider.Error
	if errors.As(genErr, &pe) && pe.Stage == "config" {
		return nil, genErr
	}

	// Everything from here on is disk mutation: the call has either
	// succeeded or failed at request/decode stage, both of which reached
	// the wire and must leave evidence behind.
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
	manifest := filepath.Join(runDir, "manifest.jsonl")
	if err := fsio.AppendRecord(manifest, header); err != nil {
		return nil, err
	}

	// Sidecar: written for success AND wire failures (evidence either way).
	rawRel := ""
	if len(res.Raw) > 0 {
		rawRel = "raw/" + callID + ".json"
		if err := writeNew(filepath.Join(runDir, "raw", callID+".json"), RedactRaw(res.Raw)); err != nil {
			return nil, err
		}
	}

	record := schema.CallRecord{
		V: 1, Type: "call", Run: runID, Call: callID,
		TS:       time.Now().UTC().Truncate(time.Second),
		Provider: p.Provider.Name(), ModelRequested: p.Profile.Model,
		ModelReturned: res.Meta.ModelReturned, Profile: p.ProfileID, Operation: "generate",
		Request: schema.CallRequest{Prompt: req.Prompt, N: p.N, Aspect: meta.Aspect, Native: p.Profile.Native},
		Response: schema.CallResponse{
			LatencyMS:         res.Latency.Milliseconds(),
			HTTPStatus:        res.Meta.HTTPStatus,
			ProviderRequestID: res.Meta.ProviderRequestID,
		},
		Raw: rawRel,
	}

	result := &GenResult{Project: meta.Project, Run: runID, Call: callID, LatencyMS: res.Latency.Milliseconds()}
	if genErr != nil {
		record.Error = callErrorFrom(genErr)
		record.Images = []schema.ImageRef{}
		record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, p.Profile.Model, 0, p.Pricing)
		result.Failed, result.Error, result.Cost = true, record.Error, record.Cost
		if err := fsio.AppendRecord(manifest, record); err != nil {
			return nil, err
		}
		return result, nil
	}

	for i, img := range res.Images {
		id := fmt.Sprintf("%s_%d", callID, i)
		rel := "images/" + id + ".png"
		if err := writeNew(filepath.Join(runDir, "images", id+".png"), img.Bytes); err != nil {
			return nil, err
		}
		sum := sha256.Sum256(img.Bytes)
		record.Images = append(record.Images, schema.ImageRef{
			ID: id, File: rel, SHA256: hex.EncodeToString(sum[:]),
			W: img.W, H: img.H, AspectActual: res.AspectActual,
		})
	}
	record.Cost = ResolveCost(res.Cost, res.Meta.ModelReturned, p.Profile.Model, len(res.Images), p.Pricing)
	if err := fsio.AppendRecord(manifest, record); err != nil {
		return nil, err
	}
	result.Images, result.Cost = record.Images, record.Cost
	return result, nil
}

// allocateRun returns the next sequential run ID and creates its directory.
func allocateRun(projDir string) (string, string, error) {
	runsDir := filepath.Join(projDir, "runs")
	if err := os.MkdirAll(runsDir, 0o755); err != nil {
		return "", "", err
	}
	max := 0
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "r_")); err == nil && n > max {
			max = n
		}
	}
	id := fmt.Sprintf("r_%04d", max+1)
	dir := filepath.Join(runsDir, id)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", "", err
	}
	return id, dir, nil
}

// writeNew creates a file exclusively — evidence is never overwritten.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func callErrorFrom(err error) *schema.CallError {
	var pe *provider.Error
	if errors.As(err, &pe) {
		return &schema.CallError{Stage: pe.Stage, HTTPStatus: pe.HTTPStatus, Message: pe.Message}
	}
	return &schema.CallError{Stage: "request", Message: err.Error()}
}
