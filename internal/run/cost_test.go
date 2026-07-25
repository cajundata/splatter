package run

import (
	"testing"

	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
)

func testPricing() *config.Pricing {
	return &config.Pricing{
		Version: "2026-07-25.0",
		Models:  map[string]config.ModelPrice{"gpt-image-1": {USDPerImage: 0.04}},
	}
}

func TestResolveCostReportedWins(t *testing.T) {
	usd := 0.27
	got := ResolveCost(provider.Cost{USD: &usd, Source: "reported"}, "gpt-image-1", "gpt-image-1", 2, testPricing())
	if got.Source != "reported" || got.USD == nil || *got.USD != 0.27 {
		t.Fatalf("got %#v", got)
	}
}

func TestResolveCostTable(t *testing.T) {
	got := ResolveCost(provider.Cost{}, "gpt-image-1", "gpt-image-1", 2, testPricing())
	if got.Source != "table:2026-07-25.0" || got.USD == nil || *got.USD != 0.08 {
		t.Fatalf("got %#v", got)
	}
	// model_returned unknown, falls back to model_requested
	got = ResolveCost(provider.Cost{}, "gpt-image-1-2027", "gpt-image-1", 1, testPricing())
	if got.Source != "table:2026-07-25.0" || *got.USD != 0.04 {
		t.Fatalf("fallback: %#v", got)
	}
}

func TestResolveCostUnavailable(t *testing.T) {
	got := ResolveCost(provider.Cost{}, "mystery", "mystery", 1, testPricing())
	if got.Source != "unavailable" || got.USD != nil {
		t.Fatalf("got %#v", got)
	}
	// failed call: zero images, nothing reported
	got = ResolveCost(provider.Cost{}, "gpt-image-1", "gpt-image-1", 0, testPricing())
	if got.Source != "unavailable" || got.USD != nil {
		t.Fatalf("failed call must be unavailable, got %#v", got)
	}
}
