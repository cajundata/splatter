package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/schema"
)

var ErrRunNotFound = errors.New("run not found")

// AmbiguousRunError reports a run ID present in more than one project.
type AmbiguousRunError struct {
	Run      string
	Projects []string
}

func (e *AmbiguousRunError) Error() string {
	return fmt.Sprintf("run %s found in multiple projects: %s (disambiguation not supported; rename or remove one)",
		e.Run, strings.Join(e.Projects, ", "))
}

// FindRun scans projects/*/runs/<runID> for the owning project. Zero
// matches wraps ErrRunNotFound; more than one returns *AmbiguousRunError
// naming the candidates. Commands surface both as usage errors.
func FindRun(root, runID string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		return "", err
	}
	var owners []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fi, err := os.Stat(filepath.Join(root, "projects", e.Name(), "runs", runID))
		if err == nil && fi.IsDir() {
			owners = append(owners, e.Name())
		}
	}
	switch len(owners) {
	case 0:
		return "", fmt.Errorf("run %q: %w", runID, ErrRunNotFound)
	case 1:
		return owners[0], nil
	default:
		return "", &AmbiguousRunError{Run: runID, Projects: owners}
	}
}

// ReadManifest decodes a run's manifest.jsonl into its header and call
// records, validating every line. Shared by verdict (image-ID checks)
// and sheet (card data).
func ReadManifest(runDir string) (*schema.RunHeader, []schema.CallRecord, error) {
	mp := filepath.Join(runDir, "manifest.jsonl")
	f, err := os.Open(mp)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	var header *schema.RunHeader
	var calls []schema.CallRecord
	lineNo := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
	for sc.Scan() {
		lineNo++
		decoded, err := schema.DecodeManifestLine(sc.Bytes())
		if err != nil {
			return nil, nil, fmt.Errorf("%s line %d: %w", mp, lineNo, err)
		}
		switch r := decoded.(type) {
		case *schema.RunHeader:
			header = r
		case *schema.CallRecord:
			calls = append(calls, *r)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", mp, err)
	}
	if header == nil {
		return nil, nil, fmt.Errorf("%s: manifest has no run header", mp)
	}
	return header, calls, nil
}
