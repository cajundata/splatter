package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/splatter/internal/spaces"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type statusResult struct {
	Root     string                    `json:"root"`
	Sync     string                    `json:"sync"`
	Projects []workspace.ProjectStatus `json:"projects"`
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Workspace state and pending sync",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			projects, err := workspace.Status(root)
			if err != nil {
				return err
			}
			if projects == nil {
				projects = []workspace.ProjectStatus{}
			}
			sync := "configured"
			if _, err := spaces.FromEnv(); err != nil {
				sync = "not configured"
			}
			res := statusResult{Root: root, Sync: sync, Projects: projects}
			var b strings.Builder
			fmt.Fprintf(&b, "workspace: %s\nsync: %s\n", res.Root, res.Sync)
			for _, p := range res.Projects {
				fmt.Fprintf(&b, "  %-24s briefs:%d runs:%d verdicts:%d critiques:%d missing:%d\n",
					p.Name, p.Briefs, p.Runs, p.Verdicts, p.Critiques, p.MissingImages)
			}
			if len(res.Projects) == 0 {
				b.WriteString("  no projects\n")
			}
			return emit(cmd, res, b.String())
		},
	}
}
