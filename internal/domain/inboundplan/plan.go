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

// Line is one planned catalogue item, in eaches. GTIN is the 14-digit form.
// UnitVolumeDm3 is the seller-declared volume of one each in cubic decimetres:
// 0 = undeclared, else 1..MaxUnitVolumeDm3 (ADR 0015).
type Line struct {
	CatalogItemID string
	GTIN          string
	QtyEaches     int
	UnitVolumeDm3 int
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
