package placementplan

import (
	"errors"
	"testing"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// TestComputeAcceptsEveryBoundaryValue: the value just inside each bound is
// accepted, the value just outside is refused (validation error).
func TestComputeAcceptsEveryBoundaryValue(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*NewPlan)
		ok   bool
	}{
		{"cover days at the minimum", func(in *NewPlan) { in.CoverDays = MinCoverDays }, true},
		{"cover days at the maximum", func(in *NewPlan) { in.CoverDays = MaxCoverDays }, true},
		{"cover days below the minimum", func(in *NewPlan) { in.CoverDays = MinCoverDays - 1 }, false},
		{"cover days above the maximum", func(in *NewPlan) { in.CoverDays = MaxCoverDays + 1 }, false},
		{"window of one day", func(in *NewPlan) { in.WindowDays = 1 }, true},
		{"window of zero days", func(in *NewPlan) { in.WindowDays = 0 }, false},
		{"suggested units of one", func(in *NewPlan) { in.Suggestions[0].SuggestedUnits = 1 }, true},
		{"suggested units of zero", func(in *NewPlan) { in.Suggestions[0].SuggestedUnits = 0 }, false},
		{"region demand of one unit", func(in *NewPlan) { in.Suggestions[0].Evidence.RegionDemandUnits = 1 }, true},
		{"region demand of zero units", func(in *NewPlan) { in.Suggestions[0].Evidence.RegionDemandUnits = 0 }, false},
		{"node availability of zero units", func(in *NewPlan) { in.Suggestions[0].Evidence.NodeAvailabilityUnits = 0 }, true},
		{"node availability below zero", func(in *NewPlan) { in.Suggestions[0].Evidence.NodeAvailabilityUnits = -1 }, false},
		{"evidence cover days at the minimum", func(in *NewPlan) { in.Suggestions[0].Evidence.CoverDays = MinCoverDays }, true},
		{"evidence cover days at the maximum", func(in *NewPlan) { in.Suggestions[0].Evidence.CoverDays = MaxCoverDays }, true},
		{"evidence cover days below the minimum", func(in *NewPlan) { in.Suggestions[0].Evidence.CoverDays = MinCoverDays - 1 }, false},
		{"evidence cover days above the maximum", func(in *NewPlan) { in.Suggestions[0].Evidence.CoverDays = MaxCoverDays + 1 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture()
			c.mut(&in)
			_, _, err := Compute(in, now())
			if c.ok && err != nil {
				t.Fatalf("Compute refused a boundary value: %v", err)
			}
			if !c.ok && !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("Compute err = %v, want a validation error", err)
			}
		})
	}
}
