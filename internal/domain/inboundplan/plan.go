// Package inboundplan is the InboundPlan aggregate: a seller's declaration of
// what it intends to send to one vendor node, in eaches, and how it will pack
// it (ADR 0002).
//
// States: DRAFT (initial), CONFIRMED and CANCELLED. ConfirmInboundPlan and
// CancelInboundPlan both leave DRAFT; CONFIRMED and CANCELLED are terminal.
package inboundplan

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

const (
	// MaxLines is the most lines one plan declares.
	MaxLines = 500
	// MaxQtyEaches is the largest quantity of one line (the Postgres integer ceiling).
	MaxQtyEaches = shared.MaxInt32
	// MaxUnitVolumeDm3 is the largest seller-declared volume of one each, in
	// cubic decimetres (ADR 0015). 0 means undeclared.
	MaxUnitVolumeDm3 = 100000
)

// ID identifies a plan (planId).
type ID string

// State is the lifecycle state of a plan.
type State string

// The three states of a plan.
const (
	Draft     State = "DRAFT"
	Confirmed State = "CONFIRMED"
	Cancelled State = "CANCELLED"
)

// PackingOption says how the seller packs the plan.
type PackingOption string

// The two packing options.
const (
	// Individual packs eaches freely: cartons are logistic units with no pack GTIN.
	Individual PackingOption = "INDIVIDUAL"
	// CasePacked packs master cartons: one item at the quantity of its CASE level.
	CasePacked PackingOption = "CASE_PACKED"
)

// Valid reports whether p is one of the two options.
func (p PackingOption) Valid() bool { return p == Individual || p == CasePacked }

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

// Line is one planned catalogue item, in eaches. GTIN is the 14-digit form.
// UnitVolumeDm3 is the seller-declared volume of one each in cubic decimetres:
// 0 = undeclared, else 1..MaxUnitVolumeDm3 (ADR 0015).
type Line struct {
	CatalogItemID string
	GTIN          string
	QtyEaches     int
	UnitVolumeDm3 int
}

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

// Plan is the aggregate root.
type Plan struct {
	id                ID
	sellerID          string
	destination       string
	packing           PackingOption
	lines             []Line
	placementOption   PlacementOption
	redistribution    []RedistributionLine
	splitLimitKnown   bool
	placementFeeBasis *FeeBasis
	capacityEnforced  bool
	state             State
	createdAt         time.Time
	confirmedAt       time.Time
	cancelledAt       time.Time
	version           int64
}

// Snapshot is the persisted form of a plan. Restore validates it.
type Snapshot struct {
	ID                ID
	SellerID          string
	DestinationID     string
	Packing           PackingOption
	Lines             []Line
	PlacementOption   PlacementOption
	Redistribution    []RedistributionLine
	SplitLimitKnown   bool
	PlacementFeeBasis *FeeBasis
	CapacityEnforced  bool
	State             State
	CreatedAt         time.Time
	ConfirmedAt       time.Time
	CancelledAt       time.Time
	Version           int64
}

// NewPlan is the input of Create. Lines carry the GTIN as the client sent it
// (8, 12, 13 or 14 digits); Create normalises it to 14 digits. The
// redistribution, the fee basis and capacityEnforced are the facts the use
// case computed under ADR 0015; an empty PlacementOption defaults to
// SELLER_SPLIT.
type NewPlan struct {
	ID                ID
	SellerID          string
	DestinationID     string
	Packing           PackingOption
	Lines             []Line
	PlacementOption   PlacementOption
	Redistribution    []RedistributionLine
	SplitLimitKnown   bool
	PlacementFeeBasis *FeeBasis
	CapacityEnforced  bool
}

// Create builds a DRAFT plan at version 1. It raises no event: the model
// publishes the plan only when it is confirmed.
func Create(in NewPlan, now time.Time) (*Plan, error) {
	lines := make([]Line, len(in.Lines))
	for i, l := range in.Lines {
		g, err := gs1.ParseGTIN(l.GTIN)
		if err == nil {
			err = gs1.RejectVariableMeasure(g)
		}
		if err != nil {
			return nil, shared.Invalid("lines[%d].gtin: %v", i, err)
		}
		lines[i] = Line{CatalogItemID: l.CatalogItemID, GTIN: g.String(), QtyEaches: l.QtyEaches, UnitVolumeDm3: l.UnitVolumeDm3}
	}
	option := in.PlacementOption
	if option == "" {
		option = SellerSplit
	}
	p, err := build(Snapshot{ID: in.ID, SellerID: in.SellerID, DestinationID: in.DestinationID, Packing: in.Packing,
		Lines: lines, PlacementOption: option, Redistribution: in.Redistribution, SplitLimitKnown: in.SplitLimitKnown,
		PlacementFeeBasis: in.PlacementFeeBasis, CapacityEnforced: in.CapacityEnforced,
		State: Draft, CreatedAt: now, Version: 1})
	if err != nil {
		return nil, shared.Invalid("%v", err)
	}
	return p, nil
}

// Restore rebuilds a persisted plan, validating every invariant.
func Restore(s Snapshot) (*Plan, error) {
	p, err := build(s)
	if err != nil {
		return nil, shared.Corrupt("plan %q: %v", s.ID, err)
	}
	return p, nil
}

func build(s Snapshot) (*Plan, error) {
	if err := checkValues(s); err != nil {
		return nil, err
	}
	return &Plan{
		id: s.ID, sellerID: s.SellerID, destination: s.DestinationID, packing: s.Packing,
		lines: append([]Line(nil), s.Lines...), placementOption: s.PlacementOption,
		redistribution:    append([]RedistributionLine(nil), s.Redistribution...),
		splitLimitKnown:   s.SplitLimitKnown,
		placementFeeBasis: copyFeeBasis(s.PlacementFeeBasis), capacityEnforced: s.CapacityEnforced,
		state: s.State, createdAt: s.CreatedAt.UTC(),
		confirmedAt: shared.UTC(s.ConfirmedAt), cancelledAt: shared.UTC(s.CancelledAt), version: s.Version,
	}, nil
}

func copyFeeBasis(f *FeeBasis) *FeeBasis {
	if f == nil {
		return nil
	}
	out := *f
	out.NodeIDs = append([]string(nil), f.NodeIDs...)
	return &out
}

func checkValues(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) {
		return errors.New("planId is not a valid id")
	}
	if !shared.ValidOwnerRef(s.SellerID) {
		return errors.New("sellerId is not a valid id")
	}
	if !shared.ValidID(s.DestinationID) {
		return errors.New("destinationNodeId is not a valid id")
	}
	if !s.Packing.Valid() {
		return errors.New("packingOption must be INDIVIDUAL or CASE_PACKED")
	}
	if !s.PlacementOption.Valid() {
		return errors.New("placementOption must be SELLER_SPLIT or NETWORK_SPLIT")
	}
	if s.CreatedAt.IsZero() {
		return errors.New("createdAt is required")
	}
	if s.Version < 1 {
		return errors.New("version must be at least 1")
	}
	if err := checkLines(s.Lines); err != nil {
		return err
	}
	if err := checkSplit(s); err != nil {
		return err
	}
	return checkState(s)
}

func checkLines(lines []Line) error {
	if len(lines) == 0 || len(lines) > MaxLines {
		return fmt.Errorf("a plan has between 1 and %d lines", MaxLines)
	}
	seen := map[string]bool{}
	for i, l := range lines {
		if !shared.ValidID(l.CatalogItemID) {
			return fmt.Errorf("lines[%d].catalogItemId is not a valid id", i)
		}
		if seen[l.CatalogItemID] {
			return fmt.Errorf("lines[%d].catalogItemId %q appears twice", i, l.CatalogItemID)
		}
		seen[l.CatalogItemID] = true
		if _, err := gs1.ParseGTIN(l.GTIN); err != nil || len(l.GTIN) != 14 {
			return fmt.Errorf("lines[%d].gtin is not a valid 14-digit GTIN", i)
		}
		if l.QtyEaches < 1 || l.QtyEaches > MaxQtyEaches {
			return fmt.Errorf("lines[%d].qtyEaches must be between 1 and %d", i, MaxQtyEaches)
		}
		if l.UnitVolumeDm3 < 0 || l.UnitVolumeDm3 > MaxUnitVolumeDm3 {
			return fmt.Errorf("lines[%d].unitVolumeDm3 must be between 0 (undeclared) and %d", i, MaxUnitVolumeDm3)
		}
	}
	return nil
}

// checkSplit pins the placement facts of ADR 0015: a redistribution and a fee
// basis exist exactly when the option is NETWORK_SPLIT, the redistribution
// covers every line exactly, and the fee basis names the recipients and the
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

func checkState(s Snapshot) error {
	switch s.State {
	case Draft:
		if !s.ConfirmedAt.IsZero() || !s.CancelledAt.IsZero() {
			return errors.New("a draft plan is neither confirmed nor cancelled")
		}
	case Confirmed:
		if s.ConfirmedAt.IsZero() || !s.CancelledAt.IsZero() {
			return errors.New("a confirmed plan needs confirmedAt and no cancelledAt")
		}
	case Cancelled:
		if s.CancelledAt.IsZero() || !s.ConfirmedAt.IsZero() {
			return errors.New("a cancelled plan needs cancelledAt and no confirmedAt")
		}
	default:
		return errors.New("state must be DRAFT, CONFIRMED or CANCELLED")
	}
	return nil
}

// Confirm moves DRAFT to CONFIRMED and raises InboundPlanConfirmed.
func (p *Plan) Confirm(now time.Time) ([]shared.Event, error) {
	if p.state != Draft {
		return nil, fmt.Errorf("%w: plan is %s, only a DRAFT plan can be confirmed", shared.ErrIllegalTransition, p.state)
	}
	p.state, p.confirmedAt = Confirmed, now.UTC()
	p.version++
	ev := InboundPlanConfirmed{
		Header: shared.Header{Version: p.version, At: p.confirmedAt}, PlanID: p.id, SellerID: p.sellerID,
		DestinationNodeID: p.destination, Packing: p.packing, Lines: p.Lines(),
		PlacementOption: p.placementOption, Redistribution: p.Redistribution(), PlacementFeeBasis: p.PlacementFeeBasis(),
	}
	return []shared.Event{ev}, nil
}

// Cancel moves DRAFT to CANCELLED. The model publishes no event for it.
func (p *Plan) Cancel(now time.Time) error {
	if p.state != Draft {
		return fmt.Errorf("%w: plan is %s, only a DRAFT plan can be cancelled", shared.ErrIllegalTransition, p.state)
	}
	p.state, p.cancelledAt = Cancelled, now.UTC()
	p.version++
	return nil
}

// EnsureConfirmed returns an illegal-transition error unless the plan is CONFIRMED
// (a shipment is only created for a confirmed plan).
func (p *Plan) EnsureConfirmed() error {
	if p.state != Confirmed {
		return fmt.Errorf("%w: plan is %s, shipments need a CONFIRMED plan", shared.ErrIllegalTransition, p.state)
	}
	return nil
}

// Snapshot returns the persisted form.
func (p *Plan) Snapshot() Snapshot {
	return Snapshot{ID: p.id, SellerID: p.sellerID, DestinationID: p.destination, Packing: p.packing, Lines: p.Lines(),
		PlacementOption: p.placementOption, Redistribution: p.Redistribution(), SplitLimitKnown: p.splitLimitKnown,
		PlacementFeeBasis: p.PlacementFeeBasis(), CapacityEnforced: p.capacityEnforced,
		State: p.state, CreatedAt: p.createdAt, ConfirmedAt: p.confirmedAt, CancelledAt: p.cancelledAt, Version: p.version}
}

// ID returns the planId.
func (p *Plan) ID() ID { return p.id }

// SellerID returns the owner of the planned stock.
func (p *Plan) SellerID() string { return p.sellerID }

// DestinationNodeID returns the node the stock is sent to.
func (p *Plan) DestinationNodeID() string { return p.destination }

// Packing returns the packing option.
func (p *Plan) Packing() PackingOption { return p.packing }

// Lines returns a copy of the planned lines.
func (p *Plan) Lines() []Line { return append([]Line(nil), p.lines...) }

// PlacementOption returns who splits the inbound (ADR 0015).
func (p *Plan) PlacementOption() PlacementOption { return p.placementOption }

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
