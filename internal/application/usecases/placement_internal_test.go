package usecases

import (
	"testing"

	"github.com/claudioed/seller-inbound/internal/domain/network"
)

func node(id, region, operator string, active bool) network.Node {
	n, err := network.Node{}.Registered(id, network.TypeFulfillmentCenter, operator, region)
	if err != nil {
		panic(err)
	}
	if active {
		return n.Activated(id)
	}
	return n
}

// TestSplitTargetIsExactAndDeterministic: shares always sum to the target;
// the leftover units go to the largest fractional share, ties by node order.
func TestSplitTargetIsExactAndDeterministic(t *testing.T) {
	// target 20 over weights 30,20,10: exacts 10,6,3 (19 given); the leftover
	// 1 goes to weight 20 (remainder 40 of 60) -> 10,7,3.
	cs := []candidate{{node: node("fc-a", "r", network.OperatorVendor, true), availability: 30},
		{node: node("fc-b", "r", network.OperatorVendor, true), availability: 20},
		{node: node("fc-c", "r", network.OperatorVendor, true), availability: 10}}
	got := splitTarget(20, cs)
	if len(got) != 3 || got[0] != 10 || got[1] != 7 || got[2] != 3 {
		t.Fatalf("split = %v", got)
	}
	// Even split when every weight is zero: base + first remainder.
	zero := []candidate{{node: node("fc-a", "r", network.OperatorVendor, true)},
		{node: node("fc-b", "r", network.OperatorVendor, true)},
		{node: node("fc-c", "r", network.OperatorVendor, true)}}
	got = splitTarget(20, zero)
	if got[0] != 7 || got[1] != 7 || got[2] != 6 {
		t.Fatalf("even split = %v", got)
	}
	// A zero-weight node next to heavier ones gets nothing.
	oneZero := []candidate{{node: node("fc-a", "r", network.OperatorVendor, true), availability: 10},
		{node: node("fc-b", "r", network.OperatorVendor, true)}}
	got = splitTarget(4, oneZero)
	if got[0] != 4 || got[1] != 0 {
		t.Fatalf("zero-weight node = %v", got)
	}
}

// TestBuildSuggestionsSkipsWhatCannotBePlaced pins the formula's skips: a
// covered region, a region without candidates and non-candidate nodes.
func TestBuildSuggestionsSkipsWhatCannotBePlaced(t *testing.T) {
	heat := []network.RegionHeat{{RegionID: "reg-e", DemandUnits: 100}, {RegionID: "reg-w", DemandUnits: 100}}
	nodes := []network.Node{
		node("fc-e", "reg-e", network.OperatorVendor, true),
		node("fc-net", "reg-e", network.OperatorNetwork, true), // not a split target
		node("fc-e-susp", "reg-e", network.OperatorVendor, false),
		// reg-w has no candidate at all.
	}
	// reg-e availability covers the horizon: ceil(100*14/28)=50 <= 60.
	availability := []network.NodeAvailability{{NodeID: "fc-e", AvailableQty: 60}}
	if got := buildSuggestions(heat, nodes, availability, 14); len(got) != 0 {
		t.Fatalf("covered region = %+v", got)
	}
	// One unit short: ceil(100*14/28)=50, availability 49 -> target 1 at fc-e.
	availability = []network.NodeAvailability{{NodeID: "fc-e", AvailableQty: 49}}
	got := buildSuggestions(heat, nodes, availability, 14)
	if len(got) != 1 || got[0].NodeID != "fc-e" || got[0].SuggestedUnits != 1 {
		t.Fatalf("short region = %+v", got)
	}
	if got[0].Evidence.RegionDemandUnits != 100 || got[0].Evidence.NodeAvailabilityUnits != 49 || got[0].Evidence.CoverDays != 14 {
		t.Fatalf("evidence = %+v", got[0].Evidence)
	}
}

// TestBuildSuggestionsRoundsTheHorizonUp pins ceil(demand*cover/window).
func TestBuildSuggestionsRoundsTheHorizonUp(t *testing.T) {
	heat := []network.RegionHeat{{RegionID: "reg-e", DemandUnits: 7}}
	nodes := []network.Node{node("fc-e", "reg-e", network.OperatorVendor, true)}
	// ceil(7*14/28) = ceil(3.5) = 4, availability 0 -> target 4.
	got := buildSuggestions(heat, nodes, nil, 14)
	if len(got) != 1 || got[0].SuggestedUnits != 4 {
		t.Fatalf("rounded horizon = %+v", got)
	}
}
