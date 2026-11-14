// Package capacity is the inbound capacity allocation policy of ADR 0015:
// the per-(seller, month, node) reservation key, the tier-factored monthly
// gate (record-and-allow) and the deterministic redistribution formula
// capacity-split-v1. Everything here is a pure function of its inputs; the
// use cases maintain the reservations transactionally and record the outcome
// on the plan.
package capacity

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// The tier factors of the monthly allocation, in basis points of the node's
// declared monthly dm3 (ADR 0015; GOOD 100% / WATCH 80% / AT_RISK 50%). A
// PROVISIONAL scorecard (no observations) derives GOOD and so pays the GOOD
// factor: the factor is a function of scorecard.Tier only.
const (
	TierFactorGoodBps   = 10000
	TierFactorWatchBps  = 8000
	TierFactorAtRiskBps = 5000
)

// NetworkSplitMaxNodes is how many fulfilment centres one NETWORK_SPLIT plan
// redistributes across at most.
const NetworkSplitMaxNodes = 3

// ErrInboundCapacityExceeded: the plan's volume would push the seller's
// reserved dm3 for the node past the tier-factored monthly limit (problem type
// inbound-capacity-exceeded, 422).
var ErrInboundCapacityExceeded = errors.New("inbound capacity exceeded")

// ErrNetworkSplitDestination: a NETWORK_SPLIT plan must destination an ACTIVE
// INBOUND_CROSS_DOCK (problem type network-split-destination, 422).
var ErrNetworkSplitDestination = errors.New("network split needs an inbound cross-dock destination")

// ErrNetworkSplitUnavailable: the destination's region holds no ACTIVE
// FULFILLMENT_CENTER the view has seen, so no redistribution can be recorded
// (problem type network-split-unavailable, 422).
var ErrNetworkSplitUnavailable = errors.New("network split has no fulfilment centre to redistribute to")

// Allocation is one row of the reservation table: the dm3 a seller's DRAFT
// and CONFIRMED plans of the month promise to one node (ADR 0015).
type Allocation struct {
	SellerID    string
	Month       string
	NodeID      string
	ReservedDm3 int64
	UpdatedAt   time.Time
}

// TierFactorBps answers the allocation factor of the tier. An unknown tier
// never happens (scorecard.TierFor is total); an empty one is GOOD, matching
// the PROVISIONAL path.
func TierFactorBps(t scorecard.Tier) int {
	switch t {
	case scorecard.TierWatch:
		return TierFactorWatchBps
	case scorecard.TierAtRisk:
		return TierFactorAtRiskBps
	default:
		return TierFactorGoodBps
	}
}

// MonthOf is the calendar month key of the allocation table: see
// shared.MonthOf (kept there: inboundplan and capacity share it without a
// cycle). A reservation lives in the month of the plan's createdAt.
func MonthOf(t time.Time) string { return shared.MonthOf(t) }

// LimitDm3 is the seller's tier-factored share of a node's declared monthly
// dm3: monthlyVolumeDm3 x factorBps / 10000, integer division. A zero or
// negative monthlyVolumeDm3 (undeclared) stays zero: the gate is off, not
// zero-tight.
func LimitDm3(monthlyVolumeDm3 int, factorBps int) int64 {
	if monthlyVolumeDm3 <= 0 {
		return 0
	}
	return int64(monthlyVolumeDm3) * int64(factorBps) / 10000
}

// Enforced reports whether the gate was actually applied: both sides known
// (the node declared a limit and the plan's volume is complete).
func Enforced(limitDm3, volumeDm3 int64) bool { return limitDm3 > 0 && volumeDm3 > 0 }

// Check is the gate. It refuses ErrInboundCapacityExceeded only when BOTH
// sides are known and the reservation would exceed the limit; either side
// unknown passes (record-and-allow: a legacy plan is never blocked, ADR 0015).
func Check(limitDm3, reservedDm3, volumeDm3 int64) error {
	if Enforced(limitDm3, volumeDm3) && reservedDm3+volumeDm3 > limitDm3 {
		return fmt.Errorf("%w: reserved %d + plan %d dm3 exceeds the monthly %d dm3", ErrInboundCapacityExceeded, reservedDm3, volumeDm3, limitDm3)
	}
	return nil
}

// SplitLine is one plan line the split distributes. LineNo is the line's
// 1-based position in the plan.
type SplitLine struct {
	LineNo    int
	QtyEaches int
}

// Candidate is one ACTIVE FULFILLMENT_CENTER of the destination cross-dock's
// region. LimitDm3 is the node's declared monthly dm3; 0 = undeclared.
type Candidate struct {
	NodeID   string
	LimitDm3 int
}

// Target is one (node, line) share of the recorded redistribution.
type Target struct {
	NodeID    string
	LineNo    int
	QtyEaches int
}

// Split computes the redistribution of a NETWORK_SPLIT plan
// (capacity-split-v1, ADR 0015):
//
//   - exactly one candidate takes every line whole;
//   - when every candidate declares a limit and at least one has capacity
//     remaining (limit x sellerFactor - reserved), the recipients are the
//     NetworkSplitMaxNodes largest by remaining capacity (ties to the lowest
//     nodeId) and every line is split proportionally to that remaining
//     capacity: floor shares, at least 1 unit to each recipient that still has
//     units to take, the leftover units to the largest;
//   - otherwise (a candidate declares no limit, or none has capacity left) the
//     recipients share each line evenly -- the NetworkSplitMaxNodes lowest
//     nodeIds, floor shares, leftovers in ascending nodeId order -- and the
//     answer says limitKnown false so the plan can record the degraded mode.
//
// The answer carries every non-zero share ordered by (lineNo, nodeId); it sums
// to the lines exactly. reservedByNode is the seller's reserved dm3 per node
// of the same month; nil means none.
func Split(lines []SplitLine, candidates []Candidate, factorBps int, reservedByNode map[string]int64) ([]Target, bool) {
	if len(candidates) == 0 || len(lines) == 0 {
		return nil, false
	}
	sharesByLine, limitKnown := shares(lines, candidates, factorBps, reservedByNode)
	var out []Target
	for _, l := range lines {
		for _, id := range sortedIDs(candidates) {
			if q := sharesByLine[l.LineNo][id]; q > 0 {
				out = append(out, Target{NodeID: id, LineNo: l.LineNo, QtyEaches: q})
			}
		}
	}
	return out, limitKnown
}

// shares answers the per-line node shares and whether they came from declared
// limits.
func shares(lines []SplitLine, candidates []Candidate, factorBps int, reservedByNode map[string]int64) (map[int]map[string]int, bool) {
	out := make(map[int]map[string]int, len(lines))
	if len(candidates) == 1 {
		for _, l := range lines {
			out[l.LineNo] = map[string]int{candidates[0].NodeID: l.QtyEaches}
		}
		return out, candidates[0].LimitDm3 > 0
	}
	known := rankedKnown(candidates, factorBps, reservedByNode)
	if degraded(candidates, known) {
		ids := evenIDs(candidates)
		for _, l := range lines {
			out[l.LineNo] = evenShare(l.QtyEaches, ids)
		}
		return out, false
	}
	if len(known) > NetworkSplitMaxNodes {
		known = known[:NetworkSplitMaxNodes]
	}
	for _, l := range lines {
		out[l.LineNo] = proportionalShare(l.QtyEaches, known)
	}
	return out, true
}

// degraded reports whether the split must fall back to the even mode: a
// candidate declares no limit, or none of them has capacity remaining.
func degraded(candidates []Candidate, known []ranked) bool {
	if len(known) == 0 {
		return true
	}
	for _, c := range candidates {
		if c.LimitDm3 <= 0 {
			return true
		}
	}
	return false
}

// evenIDs answers the NetworkSplitMaxNodes lowest candidate node ids.
func evenIDs(candidates []Candidate) []string {
	ids := sortedIDs(candidates)
	if len(ids) > NetworkSplitMaxNodes {
		ids = ids[:NetworkSplitMaxNodes]
	}
	return ids
}

// proportionalShare splits qty across the ranked recipients proportionally to
// their remaining capacity: floor shares, at least 1 unit to each recipient
// that still has units to take (largest first), the leftover to the largest.
func proportionalShare(qty int, known []ranked) map[string]int {
	m := make(map[string]int, len(known))
	var total int64
	for _, k := range known {
		total += k.remaining
	}
	assigned := 0
	for _, k := range known {
		s := int(int64(qty) * k.remaining / total)
		m[k.id] = s
		assigned += s
	}
	for _, k := range known {
		if assigned >= qty {
			break
		}
		if m[k.id] == 0 {
			m[k.id] = 1
			assigned++
		}
	}
	if assigned < qty {
		m[known[0].id] += qty - assigned
	}
	return m
}

// ranked answers the candidates with a declared limit and capacity remaining,
// largest first, ties to the lowest nodeId.
type ranked struct {
	id        string
	remaining int64
}

func rankedKnown(candidates []Candidate, factorBps int, reservedByNode map[string]int64) []ranked {
	var out []ranked
	for _, c := range candidates {
		rem := LimitDm3(c.LimitDm3, factorBps) - reservedByNode[c.NodeID]
		if c.LimitDm3 > 0 && rem > 0 {
			out = append(out, ranked{c.NodeID, rem})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].remaining != out[j].remaining {
			return out[i].remaining > out[j].remaining
		}
		return out[i].id < out[j].id
	})
	return out
}

func sortedIDs(candidates []Candidate) []string {
	ids := make([]string, len(candidates))
	for i, c := range candidates {
		ids[i] = c.NodeID
	}
	sort.Strings(ids)
	return ids
}

// evenShare splits qty evenly across ids: floor shares, the leftovers one
// each in ascending nodeId order.
func evenShare(qty int, ids []string) map[string]int {
	m := make(map[string]int, len(ids))
	assigned := 0
	for _, id := range ids {
		m[id] = qty / len(ids)
		assigned += m[id]
	}
	for _, id := range ids {
		if assigned >= qty {
			break
		}
		m[id]++
		assigned++
	}
	return m
}
