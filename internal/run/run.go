package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cajundata/splatter/internal/config"
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

// Gen runs one generation call end to end by fanning a single profile.
// A config-stage provider failure is caught by pre-flight and returned
// as a plain error with no evidence written at all — no run allocated,
// nothing on disk. A request- or decode-stage failure reached the wire,
// so it produces a failed-call record (Failed=true) instead of an error.
func Gen(ctx context.Context, p GenParams) (*GenResult, error) {
	fr, err := Fan(ctx, FanParams{
		Root: p.Root, BriefPath: p.BriefPath,
		Profiles: []ResolvedProfile{{ID: p.ProfileID, Profile: p.Profile, Provider: p.Provider}},
		N:        p.N, Harness: p.Harness, Pricing: p.Pricing,
	})
	if err != nil {
		return nil, err
	}
	c := fr.Calls[0]
	return &GenResult{
		Project: fr.Project, Run: fr.Run, Call: c.Call,
		Images: c.Images, Cost: c.Cost, LatencyMS: c.LatencyMS,
		Failed: c.Failed, Error: c.Error,
	}, nil
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
