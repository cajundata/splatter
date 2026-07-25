package run

import (
	"github.com/cajundata/splatter/internal/config"
	"github.com/cajundata/splatter/internal/provider"
	"github.com/cajundata/splatter/internal/schema"
)

// ResolveCost is the only constructor of manifest Cost values. Locked
// precedence: "reported" → "table:<pricing_version>" → unavailable.
// A failed call (nImages 0) is unavailable unless the adapter reported
// dollars — a table price for zero delivered images would assert knowledge
// the evidence doesn't contain.
func ResolveCost(reported provider.Cost, modelReturned, modelRequested string, nImages int, pricing *config.Pricing) schema.Cost {
	if reported.Source == "reported" && reported.USD != nil {
		return schema.Cost{USD: reported.USD, Source: "reported"}
	}
	if nImages > 0 && pricing != nil {
		model := modelReturned
		price, ok := pricing.Models[model]
		if !ok {
			model = modelRequested
			price, ok = pricing.Models[model]
		}
		if ok {
			usd := price.USDPerImage * float64(nImages)
			return schema.Cost{USD: &usd, Source: "table:" + pricing.Version}
		}
	}
	return schema.Cost{USD: nil, Source: "unavailable"}
}
