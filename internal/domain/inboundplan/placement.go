package inboundplan

// The placement half of ADR 0015: who splits the inbound across nodes, the
// recorded NETWORK_SPLIT redistribution, the fee-basis FACT and their
// snapshot checks. Split from plan.go at the placement seam (file-length
// gate).

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// PlacementOption says who splits the inbound across nodes (ADR 0015):
// SELLER_SPLIT, the default, sends everything to the destination the seller
// picked; NETWORK_SPLIT records a redistribution of the cross-dock's lines
// across the fulfilment centres of its region.
type PlacementOption string

// The two placement options.
const (
	// SellerSplit is the only behaviour before ADR 0015.
	SellerSplit PlacementOption = "SELLER_SPLIT"
	// NetworkSplit destinations an ACTIVE INBOUND_CROSS_DOCK and carries a
	// recorded redistribution. Nothing downstream executes it.
	NetworkSplit PlacementOption = "NETWORK_SPLIT"
)

// Valid reports whether p is one of the two options.
func (p PlacementOption) Valid() bool { return p == SellerSplit || p == NetworkSplit }

// RedistributionLine is one (node, line) share of a NETWORK_SPLIT plan's
// recorded redistribution (capacity-split-v1, ADR 0015). LineNo is the line's
// 1-based position in the plan.
type RedistributionLine struct {
	NodeID    string
	LineNo    int
	QtyEaches int
}

// FeeBasis is the recorded basis of a placement fee: a FACT for a later
// vendor-settlement context, never a charge (ADR 0015). It is set exactly
// when the placement option is NETWORK_SPLIT.
type FeeBasis struct {
	Basis   string
	Month   string
	Dm3     int64
	NodeIDs []string
}

func copyFeeBasis(f *FeeBasis) *FeeBasis {
	if f == nil {
		return nil
	}
	out := *f
	out.NodeIDs = append([]string(nil), f.NodeIDs...)
	return &out
}

// plan's own volume.
func checkSplit(s Snapshot) error {
	if s.PlacementOption == SellerSplit {
		if len(s.Redistribution) > 0 || s.PlacementFeeBasis != nil {
			return errors.New("a SELLER_SPLIT plan carries no redistribution and no placement fee basis")
		}
		return nil
	}
	if err := checkRedistribution(s); err != nil {
		return err
	}
	return checkFeeBasis(s)
}

// checkRedistribution pins the recorded shares: one entry per (node, line),
// every line covered exactly.
func checkRedistribution(s Snapshot) error {
	if len(s.Redistribution) == 0 {
		return errors.New("a NETWORK_SPLIT plan carries its recorded redistribution")
	}
	qtyByLine := map[int]int{}
	seen := map[RedistributionLine]bool{}
	for i, r := range s.Redistribution {
		if !shared.ValidID(r.NodeID) {
			return fmt.Errorf("redistribution[%d].nodeId is not a valid id", i)
		}
		if r.LineNo < 1 || r.LineNo > len(s.Lines) {
			return fmt.Errorf("redistribution[%d].lineNo %d is not a line of this plan", i, r.LineNo)
		}
		if r.QtyEaches < 1 || r.QtyEaches > s.Lines[r.LineNo-1].QtyEaches {
			return fmt.Errorf("redistribution[%d].qtyEaches must be between 1 and the line's quantity", i)
		}
		if seen[r] {
			return fmt.Errorf("redistribution[%d] appears twice", i)
		}
		seen[r] = true
		qtyByLine[r.LineNo] += r.QtyEaches
	}
	for i, l := range s.Lines {
		if qtyByLine[i+1] != l.QtyEaches {
			return fmt.Errorf("the redistribution does not cover line %d exactly (%d of %d eaches)", i+1, qtyByLine[i+1], l.QtyEaches)
		}
	}
	return nil
}

// checkFeeBasis pins the recorded basis: the NETWORK_SPLIT basis, a calendar
// month key, the plan's own volume, and the redistribution's recipients,
// unique and sorted.
func checkFeeBasis(s Snapshot) error {
	f := s.PlacementFeeBasis
	if f == nil {
		return errors.New("a NETWORK_SPLIT plan carries its placement fee basis")
	}
	if f.Basis != string(NetworkSplit) {
		return fmt.Errorf("the placement fee basis is %q, not %q", f.Basis, NetworkSplit)
	}
	if !shared.ValidMonth(f.Month) {
		return errors.New("the placement fee basis month must be a YYYY-MM key")
	}
	if f.Dm3 != volumeDm3(s.Lines) {
		return fmt.Errorf("the placement fee basis dm3 %d is not the plan's volume %d", f.Dm3, volumeDm3(s.Lines))
	}
	nodes := map[string]bool{}
	for _, r := range s.Redistribution {
		nodes[r.NodeID] = true
	}
	if len(f.NodeIDs) != len(nodes) {
		return fmt.Errorf("the placement fee basis names %d nodes but the redistribution covers %d", len(f.NodeIDs), len(nodes))
	}
	sorted := append([]string(nil), f.NodeIDs...)
	sort.Strings(sorted)
	for _, id := range sorted {
		if !nodes[id] {
			return fmt.Errorf("the placement fee basis names node %q the redistribution does not cover", id)
		}
	}
	if !slices.Equal(sorted, f.NodeIDs) {
		return errors.New("the placement fee basis nodeIds must be unique and sorted")
	}
	return nil
}

// volumeDm3 is Σ qty x unitVolumeDm3, and 0 when ANY line is undeclared: a
// partial sum is not a fact (ADR 0015).
func volumeDm3(lines []Line) int64 {
	var total int64
	for _, l := range lines {
		if l.UnitVolumeDm3 <= 0 {
			return 0
		}
		total += int64(l.QtyEaches) * int64(l.UnitVolumeDm3)
	}
	return total
}

// Redistribution returns a copy of the recorded NETWORK_SPLIT redistribution.
func (p *Plan) Redistribution() []RedistributionLine {
	return append([]RedistributionLine(nil), p.redistribution...)
}

// SplitLimitKnown reports whether the redistribution shares came from declared
// monthly limits (false is the degraded even-split mode, ADR 0015).
func (p *Plan) SplitLimitKnown() bool { return p.splitLimitKnown }

// PlacementFeeBasis returns a copy of the recorded fee basis, nil for
// SELLER_SPLIT.
func (p *Plan) PlacementFeeBasis() *FeeBasis { return copyFeeBasis(p.placementFeeBasis) }

// CapacityEnforced reports whether the monthly capacity gate was actually
// applied to this plan (both the node limit and the volume known, ADR 0015).
func (p *Plan) CapacityEnforced() bool { return p.capacityEnforced }

// VolumeDm3 is the plan's total declared volume, Σ qty x unitVolumeDm3, and 0
// when any line's unit volume is undeclared (ADR 0015).
func (p *Plan) VolumeDm3() int64 { return volumeDm3(p.lines) }

// State returns the lifecycle state.
func (p *Plan) State() State { return p.state }

// Version returns the optimistic-concurrency version.
func (p *Plan) Version() int64 { return p.version }

// PlannedByItem returns the planned eaches per catalogItemId.
func (p *Plan) PlannedByItem() map[string]int {
	out := make(map[string]int, len(p.lines))
	for _, l := range p.lines {
		out[l.CatalogItemID] = l.QtyEaches
	}
	return out
}
