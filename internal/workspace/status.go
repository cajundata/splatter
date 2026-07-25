package workspace

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
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
		s.Runs = len(runs)
		crits, _ := filepath.Glob(filepath.Join(proj, "critiques", "*.json"))
		s.Critiques = len(crits)
		if f, err := os.Open(filepath.Join(proj, "verdicts.jsonl")); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				s.Verdicts++
			}
			f.Close()
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
