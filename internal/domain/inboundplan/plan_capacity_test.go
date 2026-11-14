package inboundplan

import (
	"reflect"
	"testing"
)

var volLines = []Line{
	{CatalogItemID: "item-1", GTIN: "0614141000012", QtyEaches: 120, UnitVolumeDm3: 2},
	{CatalogItemID: "item-2", GTIN: "0614141000029", QtyEaches: 50, UnitVolumeDm3: 5},
}

func splitPlan(t *testing.T, mut func(*NewPlan)) (*Plan, error) {
	t.Helper()
	in := NewPlan{ID: "pln-2", SellerID: "seller-1", DestinationID: "node-1", Packing: CasePacked,
		Lines:           []Line{{CatalogItemID: "item-1", GTIN: gtin(t, "0614141000012"), QtyEaches: 90, UnitVolumeDm3: 2}},
		PlacementOption: NetworkSplit,
		Redistribution: []RedistributionLine{
			{NodeID: "fc-a", LineNo: 1, QtyEaches: 60},
			{NodeID: "fc-b", LineNo: 1, QtyEaches: 30},
		},
		SplitLimitKnown:   true,
		PlacementFeeBasis: &FeeBasis{Basis: "NETWORK_SPLIT", Month: "2026-11", Dm3: 180, NodeIDs: []string{"fc-a", "fc-b"}},
		CapacityEnforced:  true}
	if mut != nil {
		mut(&in)
	}
	return Create(in, t0)
}

func TestVolumeDm3(t *testing.T) {
	p, err := newPlan(t, func(in *NewPlan) { in.Lines = volLines })
	if err != nil {
		t.Fatal(err)
	}
	if got := p.VolumeDm3(); got != 120*2+50*5 { // 490
		t.Fatalf("VolumeDm3 = %d, want 490", got)
	}
	// one undeclared line zeroes the total: a partial sum is not a fact
	p, err = newPlan(t, func(in *NewPlan) {
		in.Lines = []Line{volLines[0], {CatalogItemID: "item-2", GTIN: gtin(t, "0614141000029"), QtyEaches: 50}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.VolumeDm3(); got != 0 {
		t.Fatalf("VolumeDm3 with an undeclared line = %d, want 0", got)
	}
}

func TestCreateDefaultsSellerSplit(t *testing.T) {
	p, err := newPlan(t, func(in *NewPlan) { in.Lines[0].UnitVolumeDm3 = 7 })
	if err != nil {
		t.Fatal(err)
	}
	if p.PlacementOption() != SellerSplit || p.CapacityEnforced() || p.SplitLimitKnown() ||
		p.Redistribution() != nil || p.PlacementFeeBasis() != nil {
		t.Fatalf("legacy plan gained placement facts: %+v", p.Snapshot())
	}
	if p.Lines()[0].UnitVolumeDm3 != 7 {
		t.Fatalf("unit volume lost: %+v", p.Lines())
	}
}

func TestUnitVolumeBounds(t *testing.T) {
	for _, v := range []int{-1, MaxUnitVolumeDm3 + 1} {
		if _, err := newPlan(t, func(in *NewPlan) { in.Lines[0].UnitVolumeDm3 = v }); err == nil {
			t.Fatalf("unitVolumeDm3 %d accepted", v)
		}
	}
}

// TestNetworkSplitSnapshotCarriesEveryFact asserts the FOUR places of the
// persisted ADR 0015 fields: NewPlan in, Snapshot out.
func TestNetworkSplitSnapshotCarriesEveryFact(t *testing.T) {
	p, err := splitPlan(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	sn := p.Snapshot()
	if sn.PlacementOption != NetworkSplit || !sn.SplitLimitKnown || !sn.CapacityEnforced ||
		len(sn.Redistribution) != 2 || sn.PlacementFeeBasis == nil || sn.PlacementFeeBasis.Dm3 != 180 {
		t.Fatalf("snapshot lost a placement fact: %+v", sn)
	}
}

// TestNetworkSplitRestoreRoundTripsEveryFact runs the snapshot through
// Restore and back: no placement fact moves.
func TestNetworkSplitRestoreRoundTripsEveryFact(t *testing.T) {
	p, err := splitPlan(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	sn := p.Snapshot()
	back, err := Restore(sn)
	if err != nil {
		t.Fatal(err)
	}
	got := back.Snapshot()
	if got.PlacementOption != sn.PlacementOption || got.SplitLimitKnown != sn.SplitLimitKnown {
		t.Fatalf("round trip changed the placement option facts")
	}
	if got.CapacityEnforced != sn.CapacityEnforced || len(got.Redistribution) != len(sn.Redistribution) {
		t.Fatalf("round trip changed the capacity facts")
	}
	for i := range sn.Redistribution {
		if got.Redistribution[i] != sn.Redistribution[i] {
			t.Fatalf("round trip changed redistribution[%d]", i)
		}
	}
	if !reflect.DeepEqual(*got.PlacementFeeBasis, *sn.PlacementFeeBasis) {
		t.Fatalf("round trip changed the fee basis")
	}
	wantAccessorsBack(t, back)
}

// wantAccessorsBack asserts the restored plan's accessors carry every split
// fact.
func wantAccessorsBack(t *testing.T, back *Plan) {
	t.Helper()
	if back.PlacementOption() != NetworkSplit || !back.CapacityEnforced() || !back.SplitLimitKnown() {
		t.Fatalf("accessors lost a placement fact: %+v", back.Snapshot())
	}
	if len(back.Redistribution()) != 2 || back.PlacementFeeBasis().Month != "2026-11" || back.VolumeDm3() != 180 {
		t.Fatalf("accessors lost a split fact: %+v", back.Snapshot())
	}
}

func TestNetworkSplitInvariants(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*NewPlan)
	}{
		{"bad option", func(in *NewPlan) { in.PlacementOption = "BOTH" }},
		{"seller split carries redistribution", func(in *NewPlan) {
			in.PlacementOption = SellerSplit
		}},
		{"no redistribution", func(in *NewPlan) { in.Redistribution = nil }},
		{"redistribution does not cover the line", func(in *NewPlan) {
			in.Redistribution[1].QtyEaches = 29
		}},
		{"share beyond the line", func(in *NewPlan) { in.Redistribution[0].QtyEaches = 91 }},
		{"line out of range", func(in *NewPlan) { in.Redistribution[0].LineNo = 2 }},
		{"duplicate share", func(in *NewPlan) {
			in.Redistribution = []RedistributionLine{{NodeID: "fc-a", LineNo: 1, QtyEaches: 30}, {NodeID: "fc-a", LineNo: 1, QtyEaches: 60}}
		}},
		{"no fee basis", func(in *NewPlan) { in.PlacementFeeBasis = nil }},
		{"wrong basis", func(in *NewPlan) { in.PlacementFeeBasis.Basis = "SELLER" }},
		{"bad month", func(in *NewPlan) { in.PlacementFeeBasis.Month = "2026-13" }},
		{"fee dm3 is not the volume", func(in *NewPlan) { in.PlacementFeeBasis.Dm3 = 1 }},
		{"unsorted node ids", func(in *NewPlan) { in.PlacementFeeBasis.NodeIDs = []string{"fc-b", "fc-a"} }},
		{"foreign node id", func(in *NewPlan) { in.PlacementFeeBasis.NodeIDs = []string{"fc-a", "fc-z"} }},
	}
	for _, c := range cases {
		if _, err := splitPlan(t, c.mut); err == nil {
			t.Fatalf("%s: accepted", c.name)
		}
	}
}

func TestConfirmCarriesPlacementFacts(t *testing.T) {
	p, err := splitPlan(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	evs, err := p.Confirm(t0)
	if err != nil {
		t.Fatal(err)
	}
	ev := evs[0].(InboundPlanConfirmed)
	if ev.PlacementOption != NetworkSplit || len(ev.Redistribution) != 2 || ev.PlacementFeeBasis == nil ||
		ev.Lines[0].UnitVolumeDm3 != 2 {
		t.Fatalf("event lost a placement fact: %+v", ev)
	}
	// a SELLER_SPLIT plan confirms without the optional placement facts
	p, err = newPlan(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	sellerEv, err := p.Confirm(t0)
	if err != nil {
		t.Fatal(err)
	}
	ev = sellerEv[0].(InboundPlanConfirmed)
	if ev.PlacementOption != SellerSplit || ev.Redistribution != nil || ev.PlacementFeeBasis != nil {
		t.Fatalf("seller-split event carries network-split facts: %+v", ev)
	}
}
