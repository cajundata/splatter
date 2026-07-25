// Package workspace knows the splats layout: marker-based root discovery,
// scaffolding for init, and the walks behind validate and status.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var ErrNoWorkspace = errors.New("not inside a splatter workspace")

var ErrInvalidName = errors.New("invalid project name")

var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

type ScaffoldResult struct {
	Created []string
	Existed []string
}

const gitattributes = `# Evidence files cross PS7/macOS through git; LF always.
* text=auto eol=lf
*.png binary
`

const gitignore = `projects/*/runs/*/images/
`

const providersStub = `version: 1
profiles:
  gemini-baseline:
    provider: gemini
    model: gemini-2.5-flash-image
    native: { }
  openai-baseline:
    provider: openai
    model: gpt-image-1
    native: { }
profile_sets:
  baseline: [gemini-baseline, openai-baseline]
`

const pricingStub = `version: "2026-07-25.0"
# Per-image prices used when the API response reports no cost.
# Bump version on every edit; it lands in cost.source as table:<version>.
models:
  gemini-2.5-flash-image:
    usd_per_image: 0.039
  gpt-image-1:
    usd_per_image: 0.042
`

// isRoot reports whether dir carries the workspace marker.
func isRoot(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, "providers.yaml")); err != nil || fi.IsDir() {
		return false
	}
	fi, err := os.Stat(filepath.Join(dir, "projects"))
	return err == nil && fi.IsDir()
}

// FindRoot walks up from start looking for the workspace marker.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if isRoot(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoWorkspace
		}
		dir = parent
	}
}

// ScaffoldWorkspace creates the workspace skeleton in dir. Re-running in
// an existing workspace root is idempotent (files reported as existed,
// missing ones recreated); nesting under another workspace is refused.
// Existing files are never overwritten.
func ScaffoldWorkspace(dir string) (*ScaffoldResult, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if root, err := FindRoot(filepath.Dir(abs)); err == nil {
		return nil, fmt.Errorf("already inside workspace at %s", root)
	}
	dir = abs
	res := &ScaffoldResult{}
	files := []struct{ name, content string }{
		{".gitattributes", gitattributes},
		{".gitignore", gitignore},
		{"providers.yaml", providersStub},
		{"pricing.yaml", pricingStub},
	}
	for _, f := range files {
		if err := createIfAbsent(filepath.Join(dir, f.name), []byte(f.content), f.name, res); err != nil {
			return nil, err
		}
	}
	proj := filepath.Join(dir, "projects")
	if _, err := os.Stat(proj); err == nil {
		res.Existed = append(res.Existed, "projects")
	} else if err := os.Mkdir(proj, 0o755); err != nil {
		return nil, err
	} else {
		res.Created = append(res.Created, "projects")
	}
	sort.Strings(res.Created)
	sort.Strings(res.Existed)
	return res, nil
}

// ScaffoldProject creates projects/<name>/{briefs,critiques,packages}/
// with .gitkeep files. runs/ and verdicts.jsonl appear on first use.
func ScaffoldProject(root, name string) (*ScaffoldResult, error) {
	if !isRoot(root) {
		return nil, fmt.Errorf("%s: %w", root, ErrNoWorkspace)
	}
	if !projectNameRe.MatchString(name) {
		return nil, fmt.Errorf("%q: %w (want lowercase letters, digits, hyphens)", name, ErrInvalidName)
	}
	res := &ScaffoldResult{}
	for _, sub := range []string{"briefs", "critiques", "packages"} {
		d := filepath.Join(root, "projects", name, sub)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
		rel := filepath.ToSlash(filepath.Join("projects", name, sub, ".gitkeep"))
		if err := createIfAbsent(filepath.Join(d, ".gitkeep"), nil, rel, res); err != nil {
			return nil, err
		}
	}
	sort.Strings(res.Created)
	sort.Strings(res.Existed)
	return res, nil
}

// createIfAbsent writes content to path only if nothing exists there,
// recording the outcome under rel in res.
func createIfAbsent(path string, content []byte, rel string, res *ScaffoldResult) error {
	if _, err := os.Stat(path); err == nil {
		res.Existed = append(res.Existed, rel)
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	res.Created = append(res.Created, rel)
	return nil
}
