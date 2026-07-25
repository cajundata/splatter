package workspace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

type ProjectStatus struct {
	Name      string `json:"name"`
	Briefs    int    `json:"briefs"`
	Runs      int    `json:"runs"`
	Verdicts  int    `json:"verdicts"`
	Critiques int    `json:"critiques"`
}

// Status reports per-project evidence counts, sorted by project name.
func Status(root string) ([]ProjectStatus, error) {
	names, err := projectNames(root, "")
	if err != nil {
		return nil, err
	}
	var out []ProjectStatus
	for _, name := range names {
		proj := filepath.Join(root, "projects", name)
		s := ProjectStatus{Name: name}
		briefs, _ := filepath.Glob(filepath.Join(proj, "briefs", "*.md"))
		s.Briefs = len(briefs)
		runs, _ := filepath.Glob(filepath.Join(proj, "runs", "r_*"))
		for _, r := range runs {
			if fi, err := os.Stat(r); err == nil && fi.IsDir() {
				s.Runs++
			}
		}
		crits, _ := filepath.Glob(filepath.Join(proj, "critiques", "*.json"))
		s.Critiques = len(crits)
		if f, err := os.Open(filepath.Join(proj, "verdicts.jsonl")); err == nil {
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
			for sc.Scan() {
				s.Verdicts++
			}
			serr := sc.Err()
			f.Close()
			if serr != nil {
				return nil, fmt.Errorf("verdicts.jsonl for %s: %w", name, serr)
			}
		}
		out = append(out, s)
	}
	return out, nil
}
