// Package placementplan is the PlacementPlan aggregate: the network's advisory
// of where a seller's forward stock of one catalogue item belongs (ADR 0012).
//
// The plan is computed deterministically from the DemandHeatView facts the
// service already projects (per-region released demand over the observation
// window, per-node declared listing availability); it is ADVICE, never an
// obligation: acceptance changes no state outside this aggregate and no
// downstream effect exists in v1.
//
// States: SUGGESTED (computed), ACCEPTED (the seller took the advice) and
// SUPERSEDED (a newer plan for the same seller and item was computed). A plan
// with no suggestions is legal: an item with no demand anywhere yields an
// EMPTY plan, still recorded and still published.
package placementplan

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// Basis is the formula version a plan was computed under. It changes only
// when the formula itself changes; the REST body and the event carry it so a
// reader can interpret the numbers without reading code.
const Basis = "demand-heat-v1"

// CoverDays bounds of the compute command: at least one day of cover, at most
// a quarter (the advisory is forward stock placement, not a season buy).
const (
	MinCoverDays = 1
	MaxCoverDays = 90
)

// ID identifies a placement plan (planId).
type ID string

// State is the lifecycle state of a plan.
type State string

// The three states of a placement plan.
const (
	Suggested  State = "SUGGESTED"
	Accepted   State = "ACCEPTED"
	Superseded State = "SUPERSEDED"
)

// Evidence is why one suggestion says what it says: the demand the region
// locked in over the window, the availability the node declared, and the
// cover horizon the plan was computed for.
type Evidence struct {
	RegionDemandUnits     int
	NodeAvailabilityUnits int
	CoverDays             int
}

// Suggestion is one node's share of the region target.
type Suggestion struct {
	NodeID         string
	RegionID       string
	SuggestedUnits int
	Evidence       Evidence
}

// Snapshot is the persisted form of a plan. Restore validates it.
type Snapshot struct {
	ID            ID
	SellerID      string
	CatalogItemID string
	CoverDays     int
	WindowDays    int
	Basis         string
	Suggestions   []Suggestion
	State         State
	CreatedAt     time.Time
	AcceptedAt    time.Time
	SupersededAt  time.Time
	Version       int64
}

// Plan is the aggregate root.
type Plan struct {
	id            ID
	sellerID      string
	catalogItemID string
	coverDays     int
	windowDays    int
	basis         string
	suggestions   []Suggestion
	state         State
	createdAt     time.Time
	acceptedAt    time.Time
	supersededAt  time.Time
	version       int64
}

// NewPlan is the input of Compute: the command's validated facts and the
// suggestions the formula produced (empty is legal).
type NewPlan struct {
	ID            ID
	SellerID      string
	CatalogItemID string
	CoverDays     int
	WindowDays    int
	Suggestions   []Suggestion
}

// Compute builds a SUGGESTED plan at version 1 and raises
// PlacementPlanSuggested. It records the plan even when the formula produced
// no suggestion: an empty plan is a legal answer (no demand, nothing to
// place), not an error.
func Compute(in NewPlan, now time.Time) (*Plan, []shared.Event, error) {
	p, err := build(Snapshot{ID: in.ID, SellerID: in.SellerID, CatalogItemID: in.CatalogItemID,
		CoverDays: in.CoverDays, WindowDays: in.WindowDays, Basis: Basis,
		Suggestions: in.Suggestions, State: Suggested, CreatedAt: now.UTC(), Version: 1})
	if err != nil {
		return nil, nil, shared.Invalid("placement plan: %v", err)
	}
	return p, []shared.Event{PlacementPlanSuggested{
		Header: shared.Header{Version: p.version, At: p.createdAt},
		PlanID: p.id, SellerID: p.sellerID, CatalogItemID: p.catalogItemID, Basis: p.basis,
		Suggestions: p.Suggestions(), CreatedAt: p.createdAt,
	}}, nil
}

// Restore rebuilds a persisted plan, validating every invariant.
func Restore(s Snapshot) (*Plan, error) {
	p, err := build(s)
	if err != nil {
		return nil, shared.Corrupt("placement plan %q: %v", s.ID, err)
	}
	return p, nil
}

func build(s Snapshot) (*Plan, error) {
	if err := checkValues(s); err != nil {
		return nil, err
	}
	return &Plan{
		id: s.ID, sellerID: s.SellerID, catalogItemID: s.CatalogItemID, coverDays: s.CoverDays,
		windowDays: s.WindowDays, basis: s.Basis, suggestions: append([]Suggestion(nil), s.Suggestions...),
		state: s.State, createdAt: s.CreatedAt.UTC(), acceptedAt: shared.UTC(s.AcceptedAt),
		supersededAt: shared.UTC(s.SupersededAt), version: s.Version,
	}, nil
}

func checkValues(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) {
		return errors.New("planId is not a valid id")
	}
	if !shared.ValidOwnerRef(s.SellerID) {
		return errors.New("sellerId is not a valid id")
	}
	if !shared.ValidID(s.CatalogItemID) {
		return errors.New("catalogItemId is not a valid id")
	}
	if s.CoverDays < MinCoverDays || s.CoverDays > MaxCoverDays {
		return fmt.Errorf("coverDays must be between %d and %d", MinCoverDays, MaxCoverDays)
	}
	if s.WindowDays < 1 {
		return errors.New("windowDays must be at least 1")
	}
	if s.Basis == "" {
		return errors.New("basis is required")
	}
	if s.CreatedAt.IsZero() {
		return errors.New("createdAt is required")
	}
	if s.Version < 1 {
		return errors.New("version must be at least 1")
	}
	if err := checkSuggestions(s.Suggestions); err != nil {
		return err
	}
	return checkState(s)
}

func checkSuggestions(suggestions []Suggestion) error {
	seen := map[string]bool{}
	for i, s := range suggestions {
		if !shared.ValidID(s.NodeID) {
			return fmt.Errorf("suggestions[%d].nodeId is not a valid id", i)
		}
		if !shared.ValidID(s.RegionID) {
			return fmt.Errorf("suggestions[%d].regionId is not a valid id", i)
		}
		if seen[s.NodeID] {
			return fmt.Errorf("suggestions[%d].nodeId %q appears twice", i, s.NodeID)
		}
		seen[s.NodeID] = true
		if s.SuggestedUnits < 1 {
			return fmt.Errorf("suggestions[%d].suggestedUnits must be at least 1: an advisory never suggests zero units", i)
		}
		if s.Evidence.RegionDemandUnits < 1 {
			return fmt.Errorf("suggestions[%d].regionDemandUnits must be at least 1: a suggestion needs demand behind it", i)
		}
		if s.Evidence.NodeAvailabilityUnits < 0 {
			return fmt.Errorf("suggestions[%d].nodeAvailabilityUnits must not be negative", i)
		}
		if s.Evidence.CoverDays < MinCoverDays || s.Evidence.CoverDays > MaxCoverDays {
			return fmt.Errorf("suggestions[%d].coverDays must be between %d and %d", i, MinCoverDays, MaxCoverDays)
		}
	}
	return nil
}

func checkState(s Snapshot) error {
	switch s.State {
	case Suggested:
		if !s.AcceptedAt.IsZero() || !s.SupersededAt.IsZero() {
			return errors.New("a suggested plan is neither accepted nor superseded")
		}
	case Accepted:
		if s.AcceptedAt.IsZero() || !s.SupersededAt.IsZero() {
			return errors.New("an accepted plan needs acceptedAt and no supersededAt")
		}
	case Superseded:
		if s.SupersededAt.IsZero() {
			return errors.New("a superseded plan needs supersededAt")
		}
	default:
		return errors.New("state must be SUGGESTED, ACCEPTED or SUPERSEDED")
	}
	return nil
}

// Accept moves SUGGESTED to ACCEPTED. The acceptance is advisory-only in v1:
// it publishes no event and changes nothing outside this aggregate (the
// seller physically ships through the existing inbound-plan flow).
func (p *Plan) Accept(now time.Time) error {
	if p.state != Suggested {
		return fmt.Errorf("%w: plan is %s, only a SUGGESTED plan can be accepted", shared.ErrIllegalTransition, p.state)
	}
	p.state, p.acceptedAt = Accepted, now.UTC()
	p.version++
	return nil
}

// Supersede moves a SUGGESTED or ACCEPTED plan to SUPERSEDED: a newer plan
// for the same seller and item was computed. It publishes no event; the newer
// plan's PlacementPlanSuggested is the record of the change.
func (p *Plan) Supersede(now time.Time) error {
	if p.state == Superseded {
		return fmt.Errorf("%w: plan is already SUPERSEDED", shared.ErrIllegalTransition)
	}
	p.state, p.supersededAt = Superseded, now.UTC()
	p.version++
	return nil
}

// Snapshot returns the persisted form.
func (p *Plan) Snapshot() Snapshot {
	return Snapshot{ID: p.id, SellerID: p.sellerID, CatalogItemID: p.catalogItemID, CoverDays: p.coverDays,
		WindowDays: p.windowDays, Basis: p.basis, Suggestions: p.Suggestions(), State: p.state,
		CreatedAt: p.createdAt, AcceptedAt: p.acceptedAt, SupersededAt: p.supersededAt, Version: p.version}
}

// ID returns the planId.
func (p *Plan) ID() ID { return p.id }

// SellerID returns the seller the advice is for.
func (p *Plan) SellerID() string { return p.sellerID }

// CatalogItemID returns the item the advice is about.
func (p *Plan) CatalogItemID() string { return p.catalogItemID }

// CoverDays returns the cover horizon the plan was computed for.
func (p *Plan) CoverDays() int { return p.coverDays }

// WindowDays returns the demand observation window, in days.
func (p *Plan) WindowDays() int { return p.windowDays }

// Basis returns the formula version string.
func (p *Plan) Basis() string { return p.basis }

// Suggestions returns a copy of the suggestions.
func (p *Plan) Suggestions() []Suggestion { return append([]Suggestion(nil), p.suggestions...) }

// State returns the lifecycle state.
func (p *Plan) State() State { return p.state }

// Version returns the optimistic-concurrency version.
func (p *Plan) Version() int64 { return p.version }
