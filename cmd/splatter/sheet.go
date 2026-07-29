package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cajundata/splatter/internal/fsio"
	"github.com/cajundata/splatter/internal/sheet"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type sheetResult struct {
	Run  string `json:"run"`
	Path string `json:"path"`
}

func newSheetCmd() *cobra.Command {
	var runID string
	var open bool
	cmd := &cobra.Command{
		Use:   "sheet",
		Short: "Generate the static HTML review sheet for a run",
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
			d, err := sheet.Build(root, project, runID)
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := sheet.Render(&buf, d); err != nil {
				return err
			}
			path := filepath.Join(root, "projects", project, "runs", runID, "sheet.html")
			if err := fsio.ReplaceFile(path, buf.Bytes()); err != nil {
				return err
			}
			if err := emit(cmd, sheetResult{Run: runID, Path: path}, path+"\n"); err != nil {
				return err
			}
			if open {
				return openInBrowser(path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run", "", "run id, e.g. r_0001 (required)")
	cmd.Flags().BoolVar(&open, "open", false, "open the sheet in the default browser")
	return cmd
}
