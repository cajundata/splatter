package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type initResult struct {
	Mode    string   `json:"mode"` // "workspace" | "project"
	Root    string   `json:"root"`
	Created []string `json:"created"`
	Existed []string `json:"existed"`
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [project]",
		Short: "Scaffold a workspace (no args) or a project (with name)",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			var res *workspace.ScaffoldResult
			result := initResult{Mode: "workspace", Root: cwd}
			if len(args) == 0 {
				res, err = workspace.ScaffoldWorkspace(cwd)
			} else {
				result.Mode = "project"
				var root string
				root, err = workspace.FindRoot(cwd)
				if err != nil {
					if errors.Is(err, workspace.ErrNoWorkspace) {
						return usageErr{fmt.Errorf("init %s: %w (run splatter init first)", args[0], err)}
					}
					return err
				}
				result.Root = root
				res, err = workspace.ScaffoldProject(root, args[0])
			}
			if err != nil {
				if errors.Is(err, workspace.ErrInvalidName) {
					return usageErr{err}
				}
				return err
			}
			result.Created = res.Created
			result.Existed = res.Existed
			return emit(cmd, result, humanScaffold(result))
		},
	}
}

func humanScaffold(r initResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s at %s\n", r.Mode, r.Root)
	for _, p := range r.Created {
		fmt.Fprintf(&b, "  created %s\n", p)
	}
	for _, p := range r.Existed {
		fmt.Fprintf(&b, "  exists  %s\n", p)
	}
	if len(r.Created) == 0 {
		b.WriteString("nothing to do\n")
	}
	return b.String()
}
