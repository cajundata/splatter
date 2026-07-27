// Package config loads and validates providers.yaml and pricing.yaml.
// Profiles are evidence: both files are git-tracked in the workspace.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Profile struct {
	Provider string         `yaml:"provider"`
	Model    string         `yaml:"model"`
	Native   map[string]any `yaml:"native"`
}

type Providers struct {
	Version     int                 `yaml:"version"`
	Profiles    map[string]Profile  `yaml:"profiles"`
	ProfileSets map[string][]string `yaml:"profile_sets"`
	path        string              // providers.yaml location, carried into resolution errors
}

type ModelPrice struct {
	USDPerImage float64 `yaml:"usd_per_image"`
}

type Pricing struct {
	Version string                `yaml:"version"`
	Models  map[string]ModelPrice `yaml:"models"`
}

var knownProviders = map[string]bool{"gemini": true, "openai": true}

func LoadProviders(path string) (*Providers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Providers
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: yaml: %w", path, err)
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("%s: version: want 1, got %d", path, p.Version)
	}
	p.path = path
	for id, prof := range p.Profiles {
		if !knownProviders[prof.Provider] {
			return nil, fmt.Errorf("%s: profile %s: unknown provider %q", path, id, prof.Provider)
		}
		if prof.Model == "" {
			return nil, fmt.Errorf("%s: profile %s: model required", path, id)
		}
	}
	for set, members := range p.ProfileSets {
		for _, m := range members {
			if _, ok := p.Profiles[m]; !ok {
				return nil, fmt.Errorf("%s: profile_set %s: unknown profile %q", path, set, m)
			}
		}
	}
	return &p, nil
}

func LoadPricing(path string) (*Pricing, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Pricing
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("%s: yaml: %w", path, err)
	}
	if p.Version == "" {
		return nil, fmt.Errorf("%s: version required (bumped on every edit)", path)
	}
	return &p, nil
}

// source names the file resolution errors point at; struct literals
// (tests) fall back to the conventional filename.
func (p *Providers) source() string {
	if p.path == "" {
		return "providers.yaml"
	}
	return p.path
}

func (p *Providers) Resolve(profileID string) (Profile, error) {
	prof, ok := p.Profiles[profileID]
	if !ok {
		ids := make([]string, 0, len(p.Profiles))
		for id := range p.Profiles {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return Profile{}, fmt.Errorf("%s: unknown profile %q (available: %s)", p.source(), profileID, strings.Join(ids, ", "))
	}
	return prof, nil
}
