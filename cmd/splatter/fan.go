package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/run"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

func newFanCmd() *cobra.Command {
	var briefPath, set, profilesCSV string
	var n int
	cmd := &cobra.Command{
		Use:   "fan",
		Short: "Fan one brief across multiple provider profiles in a single run",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			if briefPath == "" {
				return usageErr{fmt.Errorf("required flag(s) \"brief\" not set")}
			}
			if (set == "") == (profilesCSV == "") {
				return usageErr{fmt.Errorf("exactly one of --set or --profiles required")}
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
			providers, err := config.LoadProviders(filepath.Join(root, "providers.yaml"))
			if err != nil {
				return err
			}
			pricing, err := config.LoadPricing(filepath.Join(root, "pricing.yaml"))
			if err != nil {
				return err
			}
			var ids []string
			if set != "" {
				ids, err = providers.ResolveSet(set)
				if err != nil {
					return usageErr{err}
				}
			} else {
				for _, id := range strings.Split(profilesCSV, ",") {
					if id = strings.TrimSpace(id); id != "" {
						ids = append(ids, id)
					}
				}
				if len(ids) == 0 {
					return usageErr{fmt.Errorf("--profiles: no profile ids given")}
				}
			}
			resolved := make([]run.ResolvedProfile, 0, len(ids))
			for _, id := range ids {
				prof, err := providers.Resolve(id)
				if err != nil {
					return usageErr{err}
				}
				prov, err := buildProvider(prof)
				if err != nil {
					return err
				}
				resolved = append(resolved, run.ResolvedProfile{ID: id, Profile: prof, Provider: prov})
			}
			res, err := run.Fan(cmd.Context(), run.FanParams{
				Root: root, BriefPath: briefPath, Profiles: resolved,
				N: n, Harness: "splatter " + version, Pricing: pricing,
			})
			if err != nil {
				if os.IsNotExist(err) {
					return usageErr{err}
				}
				return err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s: %d succeeded, %d failed\n", res.Run, res.Succeeded, res.Failed)
			for _, c := range res.Calls {
				if c.Failed {
					fmt.Fprintf(&b, "  %s %s FAILED: %s (latency %dms)\n",
						c.Call, c.Profile, c.Error.Message, c.LatencyMS)
				} else {
					fmt.Fprintf(&b, "  %s %s: %d image(s), %s, latency %dms\n",
						c.Call, c.Profile, len(c.Images), costString(c.Cost), c.LatencyMS)
				}
			}
			if err := emit(cmd, res, b.String()); err != nil {
				return err
			}
			// Spec §5: exit 0 if at least one call succeeded, 1 only if all failed.
			if res.Succeeded == 0 {
				return fmt.Errorf("all %d call(s) failed", res.Failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&briefPath, "brief", "", "path to the brief markdown file (required)")
	cmd.Flags().StringVar(&set, "set", "", "profile set from providers.yaml")
	cmd.Flags().StringVar(&profilesCSV, "profiles", "", "comma-separated profile ids")
	cmd.Flags().IntVarP(&n, "n", "n", 1, "number of images to request per call")
	return cmd
}
