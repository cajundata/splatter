package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

// imageNoteRe is the spec-locked convention: a leading "<id>: " makes an
// image note; a note without the prefix is run-level (image: null).
var imageNoteRe = regexp.MustCompile(`^([a-z0-9_]+):\s`)

func parseNote(raw string) schema.VerdictNote {
	m := imageNoteRe.FindStringSubmatch(raw)
	if m == nil {
		return schema.VerdictNote{Image: nil, Text: raw}
	}
	id := m[1]
	return schema.VerdictNote{Image: &id, Text: strings.TrimSpace(raw[len(m[0]):])}
}

func splitIDs(csv string) []string {
	var ids []string
	for _, s := range strings.Split(csv, ",") {
		if s = strings.TrimSpace(s); s != "" {
			ids = append(ids, s)
		}
	}
	return ids
}

// locateRun maps FindRun's distinguishable failures to usage errors.
func locateRun(root, runID string) (string, error) {
	project, err := workspace.FindRun(root, runID)
	if err != nil {
		var amb *workspace.AmbiguousRunError
		if errors.Is(err, workspace.ErrRunNotFound) || errors.As(err, &amb) {
			return "", usageErr{err}
		}
		return "", err
	}
	return project, nil
}

func newVerdictCmd() *cobra.Command {
	var runID, keepCSV, cullCSV, session string
	var noteArgs []string
	cmd := &cobra.Command{
		Use:   "verdict",
		Short: "Record keep/cull decisions and notes for a run",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if runID == "" {
				return usageErr{fmt.Errorf("required flag(s) \"run\" not set")}
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := workspace.FindRoot(cwd)
			if err != nil {
				if errors.Is(err, workspace.ErrNoWorkspace) {
					return usageErr{err}
				}
				return err
			}
			project, err := locateRun(root, runID)
			if err != nil {
				return err
			}
			runDir := filepath.Join(root, "projects", project, "runs", runID)
			_, calls, err := workspace.ReadManifest(runDir)
			if err != nil {
				return err
			}
			known := map[string]bool{}
			for _, c := range calls {
				for _, img := range c.Images {
					known[img.ID] = true
				}
			}

			keep, cull := splitIDs(keepCSV), splitIDs(cullCSV)
			notes := make([]schema.VerdictNote, 0, len(noteArgs))
			for _, raw := range noteArgs {
				notes = append(notes, parseNote(raw))
			}

			// Validation before append: any violation is a usage error and
			// nothing is appended.
			if len(keep)+len(cull)+len(notes) == 0 {
				return usageErr{fmt.Errorf("empty verdict: at least one of --keep, --cull, --note required")}
			}
			inCull := map[string]bool{}
			for _, id := range cull {
				inCull[id] = true
			}
			for _, id := range keep {
				if inCull[id] {
					return usageErr{fmt.Errorf("image %s in both --keep and --cull", id)}
				}
			}
			checkKnown := func(id string) error {
				if !known[id] {
					return usageErr{fmt.Errorf("image %s not in run %s manifest", id, runID)}
				}
				return nil
			}
			for _, id := range keep {
				if err := checkKnown(id); err != nil {
					return err
				}
			}
			for _, id := range cull {
				if err := checkKnown(id); err != nil {
					return err
				}
			}
			for _, n := range notes {
				if n.Image != nil {
					if err := checkKnown(*n.Image); err != nil {
						return err
					}
				}
			}

			if session == "" {
				session = time.Now().Format("2006-01-02") // local calendar date, spec §4.3
			}
			if keep == nil {
				keep = []string{}
			}
			if cull == nil {
				cull = []string{}
			}
			rec := schema.Verdict{
				V: 1, Type: "verdict", Run: runID,
				TS:      time.Now().UTC().Truncate(time.Second),
				Session: session, Keep: keep, Cull: cull, Notes: notes,
			}
			if err := fsio.AppendRecord(filepath.Join(root, "projects", project, "verdicts.jsonl"), rec); err != nil {
				return err
			}
			human := fmt.Sprintf("verdict appended to projects/%s/verdicts.jsonl: keep %d, cull %d, note(s) %d\n",
				project, len(rec.Keep), len(rec.Cull), len(rec.Notes))
			return emit(cmd, rec, human)
		},
	}
	cmd.Flags().StringVar(&runID, "run", "", "run id, e.g. r_0001 (required)")
	cmd.Flags().StringVar(&keepCSV, "keep", "", "comma-separated image ids to keep")
	cmd.Flags().StringVar(&cullCSV, "cull", "", "comma-separated image ids to cull")
	cmd.Flags().StringArrayVar(&noteArgs, "note", nil,
		`note; "<image_id>: text" attaches to an image, plain text is run-level`)
	cmd.Flags().StringVar(&session, "session", "", "session label (default: local calendar date)")
	return cmd
}
