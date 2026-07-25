package workspace

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cajundata/splatter/internal/schema"
)

type Finding struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ErrProjectNotFound is wrapped into the error returned by Validate/
// projectNames when a named --project doesn't exist under projects/.
var ErrProjectNotFound = errors.New("project not found")

// Validate walks every project (or just the named one) and returns all
// findings. The error return is reserved for I/O failures.
func Validate(root, project string) ([]Finding, error) {
	names, err := projectNames(root, project)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, name := range names {
		pf, err := validateProject(root, name)
		if err != nil {
			return nil, err
		}
		findings = append(findings, pf...)
	}
	return findings, nil
}

func projectNames(root, only string) ([]string, error) {
	if only != "" {
		if fi, err := os.Stat(filepath.Join(root, "projects", only)); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("project %q: %w", only, ErrProjectNotFound)
		}
		return []string{only}, nil
	}
	entries, err := os.ReadDir(filepath.Join(root, "projects"))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func validateProject(root, name string) ([]Finding, error) {
	proj := filepath.Join(root, "projects", name)
	rel := func(abs string) string {
		r, err := filepath.Rel(root, abs)
		if err != nil {
			return abs
		}
		return filepath.ToSlash(r)
	}
	var findings []Finding
	add := func(abs, msg string) { findings = append(findings, Finding{Path: rel(abs), Message: msg}) }

	// briefs/*.md parse
	briefPaths, _ := filepath.Glob(filepath.Join(proj, "briefs", "*.md"))
	for _, bp := range briefPaths {
		data, err := os.ReadFile(bp)
		if err != nil {
			return nil, err
		}
		if _, _, err := schema.ParseBrief(data); err != nil {
			add(bp, err.Error())
		}
	}

	// runs/*/manifest.jsonl: decode lines, collect headers and image IDs
	runImages := map[string]map[string]bool{} // run id -> image id set
	runDirs, _ := filepath.Glob(filepath.Join(proj, "runs", "r_*"))
	for _, rd := range runDirs {
		mp := filepath.Join(rd, "manifest.jsonl")
		f, err := os.Open(mp)
		if os.IsNotExist(err) {
			add(rd, "run directory without manifest.jsonl")
			continue
		}
		if err != nil {
			return nil, err
		}
		lineNo := 0
		var header *schema.RunHeader
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for sc.Scan() {
			lineNo++
			decoded, err := schema.DecodeManifestLine(sc.Bytes())
			if err != nil {
				add(mp, fmt.Sprintf("line %d: %v", lineNo, err))
				continue
			}
			switch r := decoded.(type) {
			case *schema.RunHeader:
				if lineNo != 1 {
					add(mp, fmt.Sprintf("line %d: run header must be first line", lineNo))
				} else {
					header = r
				}
			case *schema.CallRecord:
				if lineNo == 1 {
					add(mp, "line 1: manifest must start with run header")
				}
				set, ok := runImages[r.Run]
				if !ok {
					set = map[string]bool{}
					runImages[r.Run] = set
				}
				for _, img := range r.Images {
					set[img.ID] = true
					checkImageHash(rd, img, add)
				}
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
		if header != nil {
			if _, ok := runImages[header.Run]; !ok {
				runImages[header.Run] = map[string]bool{}
			}
			checkBriefHash(proj, header, add)
		} else {
			add(mp, "manifest has no run header")
		}
	}

	// verdicts.jsonl (optional until first verdict)
	vp := filepath.Join(proj, "verdicts.jsonl")
	if f, err := os.Open(vp); err == nil {
		lineNo := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
		for sc.Scan() {
			lineNo++
			if _, err := schema.DecodeVerdictLine(sc.Bytes()); err != nil {
				add(vp, fmt.Sprintf("line %d: %v", lineNo, err))
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// critiques/*.json
	critPaths, _ := filepath.Glob(filepath.Join(proj, "critiques", "*.json"))
	for _, cp := range critPaths {
		data, err := os.ReadFile(cp)
		if err != nil {
			return nil, err
		}
		var crit schema.Critique
		if err := json.Unmarshal(data, &crit); err != nil {
			add(cp, fmt.Sprintf("malformed JSON: %v", err))
			continue
		}
		if err := crit.Validate(); err != nil {
			add(cp, err.Error())
			continue
		}
		known, ok := runImages[crit.Run]
		if !ok {
			add(cp, fmt.Sprintf("references unknown run %s", crit.Run))
			continue
		}
		for _, item := range crit.Items {
			if !known[item.Image] {
				add(cp, fmt.Sprintf("references unknown image %s", item.Image))
			}
		}
	}
	return findings, nil
}

// checkBriefHash recomputes the brief hash referenced by a run header.
func checkBriefHash(proj string, h *schema.RunHeader, add func(string, string)) {
	bp := filepath.Join(proj, filepath.FromSlash(h.Brief))
	data, err := os.ReadFile(bp)
	if err != nil {
		add(bp, fmt.Sprintf("brief referenced by run %s not readable: %v", h.Run, err))
		return
	}
	if got := schema.BriefSHA256(data); got != h.BriefSHA256 {
		add(bp, fmt.Sprintf("brief_sha256 mismatch for run %s: manifest %s, file %s",
			h.Run, short(h.BriefSHA256), short(got)))
	}
}

// checkImageHash verifies a locally-present image file against its
// manifest sha256. Absent files are fine — they may live only in Spaces.
func checkImageHash(runDir string, img schema.ImageRef, add func(string, string)) {
	ip := filepath.Join(runDir, filepath.FromSlash(img.File))
	data, err := os.ReadFile(ip)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		add(ip, fmt.Sprintf("unreadable: %v", err))
		return
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != img.SHA256 {
		add(ip, fmt.Sprintf("image sha256 mismatch: manifest %s, file %s",
			short(img.SHA256), short(got)))
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}
