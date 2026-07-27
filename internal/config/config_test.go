package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodProviders = `version: 1
profiles:
  gemini-baseline:
    provider: gemini
    model: gemini-2.5-flash-image
    native: { }
  openai-baseline:
    provider: openai
    model: gpt-image-1
    native: { quality: high }
profile_sets:
  baseline: [gemini-baseline, openai-baseline]
`

func TestLoadProviders(t *testing.T) {
	p, err := LoadProviders(writeFile(t, "providers.yaml", goodProviders))
	if err != nil {
		t.Fatal(err)
	}
	prof, err := p.Resolve("openai-baseline")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Provider != "openai" || prof.Model != "gpt-image-1" || prof.Native["quality"] != "high" {
		t.Fatalf("bad profile: %#v", prof)
	}
	if len(p.ProfileSets["baseline"]) != 2 {
		t.Fatalf("bad sets: %#v", p.ProfileSets)
	}
}

func TestLoadProvidersRejectsBadConfig(t *testing.T) {
	cases := map[string]struct{ content, wantSubstr string }{
		"bad version":      {"version: 2\nprofiles: { }\n", "version"},
		"unknown provider": {"version: 1\nprofiles:\n  x:\n    provider: dalle\n    model: m\n", "dalle"},
		"missing model":    {"version: 1\nprofiles:\n  x:\n    provider: gemini\n", "model"},
		"dangling set":     {"version: 1\nprofiles: { }\nprofile_sets:\n  s: [nope]\n", "nope"},
		"malformed yaml":   {"version: [1\n", "yaml"},
	}
	for name, tc := range cases {
		_, err := LoadProviders(writeFile(t, "providers.yaml", tc.content))
		if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
			t.Fatalf("%s: want error containing %q, got %v", name, tc.wantSubstr, err)
		}
	}
}

func TestResolveUnknownProfileListsAvailable(t *testing.T) {
	p, err := LoadProviders(writeFile(t, "providers.yaml", goodProviders))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), "gemini-baseline") {
		t.Fatalf("error must list available profiles, got %v", err)
	}
}

func TestResolveErrorCarriesProvidersPath(t *testing.T) {
	path := writeFile(t, "providers.yaml", goodProviders)
	p, err := LoadProviders(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("Resolve error must carry the providers.yaml path %q, got %v", path, err)
	}
}

func TestResolveSet(t *testing.T) {
	path := writeFile(t, "providers.yaml", goodProviders)
	p, err := LoadProviders(path)
	if err != nil {
		t.Fatal(err)
	}
	members, err := p.ResolveSet("baseline")
	if err != nil || len(members) != 2 {
		t.Fatalf("baseline: %v, %v", members, err)
	}
	_, err = p.ResolveSet("nope")
	if err == nil || !strings.Contains(err.Error(), "baseline") || !strings.Contains(err.Error(), path) {
		t.Fatalf("unknown set must list available sets and carry the path, got %v", err)
	}
}

func TestLoadPricing(t *testing.T) {
	good := "version: \"2026-07-25.0\"\nmodels:\n  gpt-image-1:\n    usd_per_image: 0.04\n"
	pr, err := LoadPricing(writeFile(t, "pricing.yaml", good))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Version != "2026-07-25.0" || pr.Models["gpt-image-1"].USDPerImage != 0.04 {
		t.Fatalf("bad pricing: %#v", pr)
	}
	if _, err := LoadPricing(writeFile(t, "pricing.yaml", "models: { }\n")); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatalf("missing version must error, got %v", err)
	}
}
