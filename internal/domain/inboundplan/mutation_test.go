package inboundplan

import (
	"strings"
	"testing"
)

// TestUnitVolumeAcceptsTheCeiling: MaxUnitVolumeDm3 itself is a legal volume.
func TestUnitVolumeAcceptsTheCeiling(t *testing.T) {
	p, err := newPlan(t, func(in *NewPlan) { in.Lines[0].UnitVolumeDm3 = MaxUnitVolumeDm3 })
	if err != nil {
		t.Fatalf("unitVolumeDm3 = MaxUnitVolumeDm3 must be accepted: %v", err)
	}
	if got := p.Lines()[0].UnitVolumeDm3; got != MaxUnitVolumeDm3 {
		t.Fatalf("unit volume = %d, want %d", got, MaxUnitVolumeDm3)
	}
	if _, err := newPlan(t, func(in *NewPlan) { in.Lines[0].UnitVolumeDm3 = 0 }); err != nil {
		t.Fatalf("an undeclared (0) unit volume must be accepted: %v", err)
	}
}

// TestRedistributionShareBoundaries: a share may be exactly one unit and may
// be exactly the whole line (a single recipient).
func TestRedistributionShareBoundaries(t *testing.T) {
	whole, err := splitPlan(t, func(in *NewPlan) {
		in.Redistribution = []RedistributionLine{{NodeID: "fc-a", LineNo: 1, QtyEaches: 90}}
		in.PlacementFeeBasis.NodeIDs = []string{"fc-a"}
	})
	if err != nil {
		t.Fatalf("one recipient taking the whole line must be accepted: %v", err)
	}
	if got := whole.Redistribution(); len(got) != 1 || got[0].QtyEaches != 90 {
		t.Fatalf("redistribution = %+v, want the single whole share", got)
	}

	units, err := splitPlan(t, func(in *NewPlan) {
		in.Lines[0].QtyEaches = 2
		in.Redistribution = []RedistributionLine{{NodeID: "fc-a", LineNo: 1, QtyEaches: 1}, {NodeID: "fc-b", LineNo: 1, QtyEaches: 1}}
		in.PlacementFeeBasis.Dm3 = 4 // 2 eaches x 2 dm3
	})
	if err != nil {
		t.Fatalf("shares of exactly one unit must be accepted: %v", err)
	}
	if got := units.Redistribution(); len(got) != 2 || got[0].QtyEaches != 1 || got[1].QtyEaches != 1 {
		t.Fatalf("redistribution = %+v, want two single-unit shares", got)
	}

	for _, qty := range []int{0, 91} {
		if _, err := splitPlan(t, func(in *NewPlan) {
			in.Redistribution = []RedistributionLine{{NodeID: "fc-a", LineNo: 1, QtyEaches: qty}}
			in.PlacementFeeBasis.NodeIDs = []string{"fc-a"}
		}); err == nil {
			t.Fatalf("a share of %d on a 90-each line must be refused", qty)
		}
	}
}

// TestRedistributionCoverageErrorNamesTheLine: the refusal says which line is
// short and by how much (line numbers are 1-based).
func TestRedistributionCoverageErrorNamesTheLine(t *testing.T) {
	_, err := splitPlan(t, func(in *NewPlan) {
		in.Lines = append(in.Lines, Line{CatalogItemID: "item-2", GTIN: gtin(t, "0614141000029"), QtyEaches: 10, UnitVolumeDm3: 3})
		in.Redistribution = []RedistributionLine{
			{NodeID: "fc-a", LineNo: 1, QtyEaches: 90},
			{NodeID: "fc-a", LineNo: 2, QtyEaches: 7},
		}
		in.PlacementFeeBasis.NodeIDs = []string{"fc-a"}
		in.PlacementFeeBasis.Dm3 = 90*2 + 10*3
	})
	if err == nil {
		t.Fatal("a redistribution covering 7 of 10 eaches of line 2 must be refused")
	}
	const want = "does not cover line 2 exactly (7 of 10 eaches)"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to contain %q", err.Error(), want)
	}
}
