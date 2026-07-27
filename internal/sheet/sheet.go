// Package sheet builds the static review surface for a run: one
// self-contained HTML file in the run directory. Inline CSS, no
// JavaScript, no external assets; all image references relative. The
// sheet is a derived file, regenerable at any time.
package sheet

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
)

//go:embed sheet.tmpl.html
var tmplSrc string

var tmpl = template.Must(template.New("sheet").Parse(tmplSrc))

type Score struct {
	Name  string
	Value int
}

type CritiqueInfo struct {
	Verdict string
	Scores  []Score // sorted by name for deterministic render
	Reason  string
}

type CardImage struct {
	ID       string
	File     string // relative: images/c_01_0.png
	W, H     int
	Status   string // "keep" | "cull" | "" — from verdicts, later records win
	Critique *CritiqueInfo
}

type Card struct {
	Call          string
	Model         string // model_returned, falling back to model_requested
	Profile       string
	N             int
	AspectText    string
	SeedText      string
	LatencyMS     int64
	CostText      string
	Failed        bool
	ErrStage      string
	ErrHTTPStatus int
	ErrMessage    string
	Images        []CardImage
}

type Group struct {
	Provider string
	Cards    []Card
}

type Footer struct {
	ImageCount int
	CostText   string
	LatencyMS  int64
	ParentLink string
}

type Data struct {
	Project    string
	Run        string
	Iteration  int
	BriefID    string
	Concept    string
	BriefNote  string
	Groups     []Group
	Footer     Footer
	VerdictCmd string
}

const maxLineBytes = 1024 * 1024

// Build reads a run's manifest, all verdict records for the run, and its
// critique file if present, assembling a render-ready Data.
func Build(root, project, runID string) (*Data, error) {
	projDir := filepath.Join(root, "projects", project)
	runDir := filepath.Join(projDir, "runs", runID)
	header, calls, err := workspace.ReadManifest(runDir)
	if err != nil {
		return nil, err
	}
	status, err := imageStatuses(filepath.Join(projDir, "verdicts.jsonl"), runID)
	if err != nil {
		return nil, err
	}
	critiques, err := imageCritiques(filepath.Join(projDir, "critiques", runID+".json"))
	if err != nil {
		return nil, err
	}

	d := &Data{Project: header.Project, Run: header.Run, Iteration: header.Iteration}
	if briefBytes, err := os.ReadFile(filepath.Join(projDir, filepath.FromSlash(header.Brief))); err == nil {
		if meta, _, perr := schema.ParseBrief(briefBytes); perr == nil {
			d.BriefID, d.Concept = meta.ID, meta.Concept
		}
	}
	if d.BriefID == "" {
		d.BriefNote = fmt.Sprintf("brief %s not available locally (sha256 %s)", header.Brief, header.BriefSHA256)
	}

	groupIdx := map[string]int{}
	var totalUSD float64
	var unavailable int
	var totalLatency int64
	var allIDs []string
	for _, c := range calls {
		card := Card{
			Call: c.Call, Model: c.ModelReturned, Profile: c.Profile,
			N: c.Request.N, AspectText: c.Request.Aspect, SeedText: "—",
			LatencyMS: c.Response.LatencyMS, CostText: costText(c.Cost),
		}
		if card.Model == "" {
			card.Model = c.ModelRequested
		}
		if c.Request.Seed != nil {
			card.SeedText = fmt.Sprintf("%d", *c.Request.Seed)
		}
		if len(c.Images) > 0 {
			card.AspectText = c.Request.Aspect + " → " + c.Images[0].AspectActual
		}
		if c.Error != nil {
			card.Failed = true
			card.ErrStage, card.ErrHTTPStatus, card.ErrMessage = c.Error.Stage, c.Error.HTTPStatus, c.Error.Message
		}
		for _, img := range c.Images {
			ci := CardImage{ID: img.ID, File: img.File, W: img.W, H: img.H, Status: status[img.ID]}
			if info, ok := critiques[img.ID]; ok {
				ci.Critique = info
			}
			card.Images = append(card.Images, ci)
			allIDs = append(allIDs, img.ID)
		}
		if c.Cost.USD != nil {
			totalUSD += *c.Cost.USD
		} else {
			unavailable++
		}
		totalLatency += c.Response.LatencyMS

		idx, ok := groupIdx[c.Provider]
		if !ok {
			idx = len(d.Groups)
			groupIdx[c.Provider] = idx
			d.Groups = append(d.Groups, Group{Provider: c.Provider})
		}
		d.Groups[idx].Cards = append(d.Groups[idx].Cards, card)
	}

	d.Footer = Footer{ImageCount: len(allIDs), LatencyMS: totalLatency}
	d.Footer.CostText = fmt.Sprintf("$%.4f", totalUSD)
	if unavailable > 0 {
		d.Footer.CostText += fmt.Sprintf(" (%d call(s) cost unavailable)", unavailable)
	}
	if header.ParentRun != nil {
		d.Footer.ParentLink = "../" + *header.ParentRun + "/sheet.html"
	}
	d.VerdictCmd = fmt.Sprintf("splatter verdict --run %s --keep %s", header.Run, strings.Join(allIDs, ","))
	return d, nil
}

// imageStatuses folds every verdict record for the run; later records
// win per image. An absent verdicts.jsonl is an empty map, not an error.
func imageStatuses(path, runID string) (map[string]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	status := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, maxLineBytes), maxLineBytes)
	for sc.Scan() {
		v, err := schema.DecodeVerdictLine(sc.Bytes())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if v.Run != runID {
			continue
		}
		for _, id := range v.Keep {
			status[id] = "keep"
		}
		for _, id := range v.Cull {
			status[id] = "cull"
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return status, nil
}

// imageCritiques loads critiques/<run>.json when present; scores sort by
// name so rendering is deterministic.
func imageCritiques(path string) (map[string]*CritiqueInfo, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]*CritiqueInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	var crit schema.Critique
	if err := json.Unmarshal(data, &crit); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]*CritiqueInfo{}
	for _, it := range crit.Items {
		info := &CritiqueInfo{Verdict: it.Verdict, Reason: it.Reason}
		names := make([]string, 0, len(it.Scores))
		for n := range it.Scores {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			info.Scores = append(info.Scores, Score{Name: n, Value: it.Scores[n]})
		}
		out[it.Image] = info
	}
	return out, nil
}

// Render executes the embedded template. The output is self-contained:
// inline CSS, no JavaScript, relative image references only.
func Render(w io.Writer, d *Data) error {
	return tmpl.Execute(w, d)
}

func costText(c schema.Cost) string {
	if c.USD == nil {
		return "cost unavailable"
	}
	return fmt.Sprintf("$%.4f (%s)", *c.USD, c.Source)
}
