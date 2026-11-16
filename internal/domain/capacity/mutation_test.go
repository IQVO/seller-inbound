package capacity

import (
	"reflect"
	"testing"
)

// TestRankedKnownNeedsADeclaredLimit pins the contract of rankedKnown: only a
// candidate that DECLARED a limit and still has capacity remaining is ranked.
// A candidate with an undeclared limit (0) is out even if its reserved figure
// were negative, which would otherwise read as positive remaining capacity.
func TestRankedKnownNeedsADeclaredLimit(t *testing.T) {
	candidates := []Candidate{
		{NodeID: "fc-a", LimitDm3: 0},
		{NodeID: "fc-b", LimitDm3: 100},
		{NodeID: "fc-c", LimitDm3: 100},
	}
	reserved := map[string]int64{"fc-a": -50, "fc-b": 40, "fc-c": 100}
	got := rankedKnown(candidates, TierFactorGoodBps, reserved)
	want := []ranked{{id: "fc-b", remaining: 60}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rankedKnown = %+v, want %+v (undeclared fc-a and full fc-c are out)", got, want)
	}
}

// TestRankedKnownOrdersLargestFirstThenLowestID pins the ordering contract.
func TestRankedKnownOrdersLargestFirstThenLowestID(t *testing.T) {
	candidates := []Candidate{
		{NodeID: "fc-z", LimitDm3: 100},
		{NodeID: "fc-b", LimitDm3: 300},
		{NodeID: "fc-y", LimitDm3: 100},
		{NodeID: "fc-a", LimitDm3: 100},
	}
	got := rankedKnown(candidates, TierFactorGoodBps, nil)
	want := []ranked{
		{id: "fc-b", remaining: 300},
		{id: "fc-a", remaining: 100},
		{id: "fc-y", remaining: 100},
		{id: "fc-z", remaining: 100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rankedKnown = %+v, want %+v", got, want)
	}
}
