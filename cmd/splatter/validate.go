package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

type validateResult struct {
	OK       bool                `json:"ok"`
	Root     string              `json:"root"`
	Findings []workspace.Finding `json:"findings"`
}

func newValidateCmd() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Check schemas, hashes, and critique files across the workspace",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := workspace.FindRoot(cwd)
			if err != nil {
				return usageErr{err}
			}
			findings, err := workspace.Validate(root, project)
			if err != nil {
				if strings.Contains(err.Error(), "not found") {
					return usageErr{err}
				}
				return err
			}
			res := validateResult{OK: len(findings) == 0, Root: root, Findings: findings}
			if res.Findings == nil {
				res.Findings = []workspace.Finding{}
			}
			var b strings.Builder
			if res.OK {
				fmt.Fprintf(&b, "ok: %s\n", root)
			} else {
				fmt.Fprintf(&b, "%d finding(s) in %s\n", len(findings), root)
				for _, f := range findings {
					fmt.Fprintf(&b, "  %s: %s\n", f.Path, f.Message)
				}
			}
			if err := emit(cmd, res, b.String()); err != nil {
				return err
			}
			if !res.OK {
				return validationErr{fmt.Errorf("%d validation finding(s)", len(findings))}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "validate a single project")
	return cmd
}
