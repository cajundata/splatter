package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/provider/gemini"
	"github.com/cajundata/splatter/internal/provider/openai"
	"github.com/cajundata/splatter/internal/run"
	"github.com/cajundata/splatter/internal/schema"
	"github.com/cajundata/splatter/internal/workspace"
	"github.com/spf13/cobra"
)

// buildProvider constructs the adapter for a profile. Package var so CLI
// tests can substitute a fake provider under the real command path.
var buildProvider = func(prof config.Profile) (provider.Provider, error) {
	switch prof.Provider {
	case "gemini":
		return gemini.New(os.Getenv("GEMINI_API_KEY"), ""), nil
	case "openai":
		return openai.New(os.Getenv("OPENAI_API_KEY"), ""), nil
	default:
		return nil, fmt.Errorf("unknown provider %q", prof.Provider)
	}
}

func newGenCmd() *cobra.Command {
	var briefPath, profileID string
	var n int
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate image concepts from a brief through one provider profile",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Checked manually (not via MarkFlagRequired) so a missing flag
			// surfaces as usageErr / exit 2 like every other usage failure,
			// rather than cobra's own ValidateRequiredFlags error which
			// bypasses SetFlagErrorFunc and would exit 1.
			if briefPath == "" {
				return usageErr{fmt.Errorf("required flag(s) \"brief\" not set")}
			}
			if profileID == "" {
				return usageErr{fmt.Errorf("required flag(s) \"profile\" not set")}
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
			prof, err := providers.Resolve(profileID)
			if err != nil {
				return usageErr{err}
			}
			prov, err := buildProvider(prof)
			if err != nil {
				return err
			}
			res, err := run.Gen(cmd.Context(), run.GenParams{
				Root: root, BriefPath: briefPath, ProfileID: profileID, Profile: prof,
				N: n, Harness: "splatter " + version, Pricing: pricing, Provider: prov,
			})
			if err != nil {
				if os.IsNotExist(err) {
					return usageErr{err}
				}
				return err
			}
			var b strings.Builder
			if res.Failed {
				fmt.Fprintf(&b, "%s %s FAILED: %s (latency %dms)\n", res.Run, res.Call, res.Error.Message, res.LatencyMS)
			} else {
				fmt.Fprintf(&b, "%s %s: %d image(s), %s, latency %dms\n",
					res.Run, res.Call, len(res.Images), costString(res.Cost), res.LatencyMS)
				for _, img := range res.Images {
					fmt.Fprintf(&b, "  %s (%dx%d)\n", img.File, img.W, img.H)
				}
			}
			if err := emit(cmd, res, b.String()); err != nil {
				return err
			}
			if res.Failed {
				return fmt.Errorf("call failed: %s", res.Error.Message)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&briefPath, "brief", "", "path to the brief markdown file (required)")
	cmd.Flags().StringVar(&profileID, "profile", "", "provider profile id from providers.yaml (required)")
	cmd.Flags().IntVarP(&n, "n", "n", 1, "number of images to request")
	return cmd
}

func costString(c schema.Cost) string {
	if c.USD == nil {
		return "cost " + c.Source
	}
	return fmt.Sprintf("cost $%.4f (%s)", *c.USD, c.Source)
}
