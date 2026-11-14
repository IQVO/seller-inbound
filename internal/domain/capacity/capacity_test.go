package capacity

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
)

var jan = time.Date(2026, 11, 30, 23, 30, 0, 0, time.FixedZone("-03", -3*3600))

func TestMonthOfIsUTC(t *testing.T) {
	if got := MonthOf(jan); got != "2026-12" {
		t.Fatalf("MonthOf = %q, want 2026-12 (UTC rolls the month)", got)
	}
}

func TestTierFactorBps(t *testing.T) {
	for _, c := range []struct {
		tier scorecard.Tier
		want int
	}{
		{scorecard.TierGood, TierFactorGoodBps},
		{scorecard.TierWatch, TierFactorWatchBps},
		{scorecard.TierAtRisk, TierFactorAtRiskBps},
		{"", TierFactorGoodBps}, // PROVISIONAL derives GOOD
	} {
		if got := TierFactorBps(c.tier); got != c.want {
			t.Fatalf("TierFactorBps(%q) = %d, want %d", c.tier, got, c.want)
		}
	}
}

func TestLimitDm3(t *testing.T) {
	for _, c := range []struct {
		monthly int
		bps     int
		want    int64
	}{
		{0, TierFactorGoodBps, 0},        // undeclared: gate off, not zero-tight
		{1000, TierFactorGoodBps, 1000},  // GOOD 100%
		{1000, TierFactorWatchBps, 800},  // WATCH 80%
		{1000, TierFactorAtRiskBps, 500}, // AT_RISK 50%
		{333, TierFactorWatchBps, 266},   // integer division
	} {
		if got := LimitDm3(c.monthly, c.bps); got != c.want {
			t.Fatalf("LimitDm3(%d, %d) = %d, want %d", c.monthly, c.bps, got, c.want)
		}
	}
}

// TestCheckGateMatrix is the ADR 0015 gate matrix: the plan is refused only
// when BOTH sides are known and the reservation exceeds the limit.
func TestCheckGateMatrix(t *testing.T) {
	for _, c := range []struct {
		name          string
		limit, volume int64
		reserved      int64
		wantErr       bool
		wantEnforced  bool
	}{
		{"both known within", 1000, 300, 700, false, true},
		{"both known boundary exactly at limit", 1000, 300, 700, false, true}, // reserved+volume == limit passes
		{"both known over", 1000, 301, 700, true, true},
		{"limit unknown", 0, 300, 700, false, false},
		{"volume unknown", 1000, 0, 700, false, false},
		{"both unknown", 0, 0, 700, false, false},
	} {
		err := Check(c.limit, c.reserved, c.volume)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s: err = %v, wantErr %v", c.name, err, c.wantErr)
		}
		if Enforced(c.limit, c.volume) != c.wantEnforced {
			t.Fatalf("%s: Enforced mismatch", c.name)
		}
	}
}

// TestCheckBoundaryAtExactlyLimit pins the inclusive boundary: a plan that
// lands exactly on the limit is allowed.
func TestCheckBoundaryAtExactlyLimit(t *testing.T) {
	if err := Check(1000, 700, 300); err != nil {
		t.Fatalf("boundary plan refused: %v", err)
	}
	if err := Check(1000, 700, 301); err == nil {
		t.Fatal("one dm3 over the limit must be refused")
	}
}

func TestSplitSingleCandidateTakesEverything(t *testing.T) {
	lines := []SplitLine{{LineNo: 1, QtyEaches: 90}, {LineNo: 2, QtyEaches: 7}}
	for _, limit := range []int{0, 5000} {
		got, known := Split(lines, []Candidate{{NodeID: "fc-1", LimitDm3: limit}}, TierFactorGoodBps, nil)
		want := []Target{{NodeID: "fc-1", LineNo: 1, QtyEaches: 90}, {NodeID: "fc-1", LineNo: 2, QtyEaches: 7}}
		if !reflect.DeepEqual(got, want) || known != (limit > 0) {
			t.Fatalf("limit %d: got %v known=%v", limit, got, known)
		}
	}
}

// TestSplitProportionalGolden: three equal candidates, GOOD factor, no
// reservations: an equal three-way split of every line.
func TestSplitProportionalGolden(t *testing.T) {
	got, known := Split(
		[]SplitLine{{LineNo: 1, QtyEaches: 90}, {LineNo: 2, QtyEaches: 10}},
		[]Candidate{{NodeID: "fc-a", LimitDm3: 1000}, {NodeID: "fc-b", LimitDm3: 1000}, {NodeID: "fc-c", LimitDm3: 1000}},
		TierFactorGoodBps, nil)
	want := []Target{
		{NodeID: "fc-a", LineNo: 1, QtyEaches: 30},
		{NodeID: "fc-b", LineNo: 1, QtyEaches: 30},
		{NodeID: "fc-c", LineNo: 1, QtyEaches: 30},
		{NodeID: "fc-a", LineNo: 2, QtyEaches: 4}, // floor(10/3)=3 each, leftover 1 to the largest
		{NodeID: "fc-b", LineNo: 2, QtyEaches: 3},
		{NodeID: "fc-c", LineNo: 2, QtyEaches: 3},
	}
	if !known || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v known=%v", got, known)
	}
}

// TestSplitProportionalByRemaining: the shares follow REMAINING capacity
// (limit x factor - reserved), not the bare limit: fc-b is smaller but empty,
// fc-a is bigger but nearly full.
func TestSplitProportionalByRemaining(t *testing.T) {
	got, known := Split(
		[]SplitLine{{LineNo: 1, QtyEaches: 60}},
		[]Candidate{{NodeID: "fc-a", LimitDm3: 100}, {NodeID: "fc-b", LimitDm3: 50}},
		TierFactorGoodBps, map[string]int64{"fc-a": 90})
	want := []Target{{NodeID: "fc-a", LineNo: 1, QtyEaches: 10}, {NodeID: "fc-b", LineNo: 1, QtyEaches: 50}}
	if !known || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v known=%v", got, known)
	}
}

// TestSplitLeftoverToTheLargestAndMinOne: equal weights, a quantity that does
// not divide, and a quantity smaller than the recipient count.
func TestSplitLeftoverToTheLargestAndMinOne(t *testing.T) {
	equal := []Candidate{{NodeID: "fc-a", LimitDm3: 1000}, {NodeID: "fc-b", LimitDm3: 1000}, {NodeID: "fc-c", LimitDm3: 1000}}
	got, _ := Split([]SplitLine{{LineNo: 1, QtyEaches: 8}}, equal, TierFactorGoodBps, nil)
	want := []Target{{NodeID: "fc-a", LineNo: 1, QtyEaches: 4}, {NodeID: "fc-b", LineNo: 1, QtyEaches: 2}, {NodeID: "fc-c", LineNo: 1, QtyEaches: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qty 8: got %v", got)
	}
	got, _ = Split([]SplitLine{{LineNo: 1, QtyEaches: 2}}, equal, TierFactorGoodBps, nil)
	want = []Target{{NodeID: "fc-a", LineNo: 1, QtyEaches: 1}, {NodeID: "fc-b", LineNo: 1, QtyEaches: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qty 2: got %v (the third largest takes nothing)", got)
	}
}

// TestSplitTieGoesToTheLowestNodeId: equal remaining keeps only the lowest
// node ids when the quantity cannot reach everyone.
func TestSplitTiesToTheLowestNodeId(t *testing.T) {
	got, _ := Split(
		[]SplitLine{{LineNo: 1, QtyEaches: 2}},
		[]Candidate{{NodeID: "fc-z", LimitDm3: 1000}, {NodeID: "fc-y", LimitDm3: 1000}, {NodeID: "fc-x", LimitDm3: 1000}},
		TierFactorGoodBps, nil)
	want := []Target{{NodeID: "fc-x", LineNo: 1, QtyEaches: 1}, {NodeID: "fc-y", LineNo: 1, QtyEaches: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

// TestSplitCapsAtNetworkSplitMaxNodes: five known candidates, only the three
// largest by remaining capacity receive (the 10-dm3 tie resolves to the
// lowest nodeId, fc-1, which then takes its floor-1 share).
func TestSplitCapsAtNetworkSplitMaxNodes(t *testing.T) {
	cs := []Candidate{{NodeID: "fc-1", LimitDm3: 10}, {NodeID: "fc-2", LimitDm3: 10}, {NodeID: "fc-3", LimitDm3: 10}, {NodeID: "fc-4", LimitDm3: 1000}, {NodeID: "fc-5", LimitDm3: 2000}}
	got, known := Split([]SplitLine{{LineNo: 1, QtyEaches: 33}}, cs, TierFactorGoodBps, nil)
	want := []Target{
		{NodeID: "fc-1", LineNo: 1, QtyEaches: 1},  // floor 0 topped to 1 (the tie winner)
		{NodeID: "fc-4", LineNo: 1, QtyEaches: 10}, // 33 x 1000/3010
		{NodeID: "fc-5", LineNo: 1, QtyEaches: 22}, // 33 x 2000/3010 = 21, + the leftover 1
	}
	if !known || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v known=%v", got, known)
	}
}

// TestSplitDegradedEvenMode: one undeclared candidate among known ones makes
// the split even across the NetworkSplitMaxNodes lowest node ids, recorded as
// limitKnown false (ADR 0015).
func TestSplitDegradedEvenMode(t *testing.T) {
	cs := []Candidate{{NodeID: "fc-d", LimitDm3: 0}, {NodeID: "fc-a", LimitDm3: 1000}, {NodeID: "fc-b", LimitDm3: 1000}, {NodeID: "fc-c", LimitDm3: 1000}}
	got, known := Split([]SplitLine{{LineNo: 1, QtyEaches: 10}}, cs, TierFactorGoodBps, nil)
	want := []Target{{NodeID: "fc-a", LineNo: 1, QtyEaches: 4}, {NodeID: "fc-b", LineNo: 1, QtyEaches: 3}, {NodeID: "fc-c", LineNo: 1, QtyEaches: 3}}
	if known || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v known=%v", got, known)
	}
}

// TestSplitDegradedWhenAllFull: every candidate declared but full (reserved
// eats the tier-factored limit) is also the even, limitKnown-false mode.
func TestSplitDegradedWhenAllFull(t *testing.T) {
	cs := []Candidate{{NodeID: "fc-a", LimitDm3: 1000}, {NodeID: "fc-b", LimitDm3: 1000}}
	got, known := Split([]SplitLine{{LineNo: 1, QtyEaches: 5}}, cs, TierFactorGoodBps,
		map[string]int64{"fc-a": 1000, "fc-b": 1000})
	want := []Target{{NodeID: "fc-a", LineNo: 1, QtyEaches: 3}, {NodeID: "fc-b", LineNo: 1, QtyEaches: 2}}
	if known || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v known=%v", got, known)
	}
}

func TestSplitSumsToTheLines(t *testing.T) {
	lines := []SplitLine{{LineNo: 1, QtyEaches: 97}, {LineNo: 2, QtyEaches: 3}}
	cs := []Candidate{{NodeID: "fc-a", LimitDm3: 700}, {NodeID: "fc-b", LimitDm3: 300}, {NodeID: "fc-c", LimitDm3: 0}}
	for _, bps := range []int{TierFactorGoodBps, TierFactorWatchBps, TierFactorAtRiskBps} {
		got, _ := Split(lines, cs, bps, map[string]int64{"fc-a": 100})
		sum := map[int]int{}
		for _, tg := range got {
			if tg.QtyEaches < 1 {
				t.Fatalf("non-positive share %v", tg)
			}
			sum[tg.LineNo] += tg.QtyEaches
		}
		for _, l := range lines {
			if sum[l.LineNo] != l.QtyEaches {
				t.Fatalf("bps %d: line %d covered %d of %d (%v)", bps, l.LineNo, sum[l.LineNo], l.QtyEaches, got)
			}
		}
	}
}
