package main

import (
	"github.com/spf13/cobra"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

// jsonOut is set by the persistent --json flag.
var jsonOut bool

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "splatter",
		Short:         "Concept-refinement instrument for T-shirt designs",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "emit machine-readable result object on stdout")
	// Flag parse failures are usage errors (exit 2).
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageErr{err}
	})
	return root
}

// usageArgs wraps a cobra positional-args validator so violations become
// usage errors (exit 2) instead of runtime errors. Every subcommand uses
// it: Args: usageArgs(cobra.NoArgs), etc.
func usageArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return usageErr{err}
		}
		return nil
	}
}
