// Package inboundshipment is the InboundShipment aggregate with its Carton
// entities: one physical consignment of a plan to one node. Each carton is a
// logistic unit identified by an SSCC and holding the contents it declares
// (ADR 0002).
//
// States: DRAFT (initial, cartons are added), LABELLED (every carton has a
// label, contents are frozen), SHIPPED, RECEIVING (a partial vendor receipt),
// CLOSED and CLOSED_WITH_DISCREPANCY (terminal). The shipment is never edited to
// match the dock: a difference is a recorded fact (ADR 0009 of the model).
package inboundshipment

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

const (
	// MaxCartons is the most cartons one shipment holds.
	MaxCartons = 1000
	// MaxContents is the most content lines one carton holds.
	MaxContents = 100
	// MaxLot is the longest lot (GS1 AI 10), in characters.
	MaxLot = 20
	// MaxQtyEaches is the largest quantity of one content line (Postgres integer ceiling).
	MaxQtyEaches = shared.MaxInt32
	// MaxGrossWeightG is the largest carton weight in grams.
	MaxGrossWeightG = shared.MaxInt32
	// MaxDimMm is the longest carton edge in millimetres.
	MaxDimMm = shared.MaxInt32
)

// ID identifies a shipment (shipmentId).
type ID string

// State is the lifecycle state of a shipment.
type State string

// The six states of a shipment.
const (
	Draft                 State = "DRAFT"
	Labelled              State = "LABELLED"
	Shipped               State = "SHIPPED"
	Receiving             State = "RECEIVING"
	Closed                State = "CLOSED"
	ClosedWithDiscrepancy State = "CLOSED_WITH_DISCREPANCY"
)

const isoDateLayout = "2006-01-02"

// Shipment is the aggregate root.
type Shipment struct {
	id         ID
	planID     string
	sellerID   string
	nodeID     string
	state      State
	cartons    []Carton
	unexpected []string
	discrepant bool
	createdAt  time.Time
	labelledAt time.Time
	shippedAt  time.Time
	receivedAt time.Time
	version    int64
}

// Snapshot is the persisted form of a shipment. Restore validates it.
type Snapshot struct {
	ID         ID
	PlanID     string
	SellerID   string
	NodeID     string
	State      State
	Cartons    []Carton
	Unexpected []string
	Discrepant bool
	CreatedAt  time.Time
	LabelledAt time.Time
	ShippedAt  time.Time
	ReceivedAt time.Time
	Version    int64
}

// NewShipment is the input of Create.
type NewShipment struct {
	ID       ID
	PlanID   string
	SellerID string
	NodeID   string
}

// Create builds an empty DRAFT shipment at version 1. It raises no event: the
// model publishes a shipment when it is labelled.
func Create(in NewShipment, now time.Time) (*Shipment, error) {
	s, err := build(Snapshot{ID: in.ID, PlanID: in.PlanID, SellerID: in.SellerID, NodeID: in.NodeID,
		State: Draft, CreatedAt: now, Version: 1})
	if err != nil {
		return nil, shared.Invalid("%v", err)
	}
	return s, nil
}

// Restore rebuilds a persisted shipment, validating every invariant.
func Restore(s Snapshot) (*Shipment, error) {
	sh, err := build(s)
	if err != nil {
		return nil, shared.Corrupt("shipment %q: %v", s.ID, err)
	}
	return sh, nil
}

func build(s Snapshot) (*Shipment, error) {
	if err := checkValues(s); err != nil {
		return nil, err
	}
	return &Shipment{
		id: s.ID, planID: s.PlanID, sellerID: s.SellerID, nodeID: s.NodeID, state: s.State,
		cartons: cloneCartons(s.Cartons), unexpected: append([]string(nil), s.Unexpected...), discrepant: s.Discrepant,
		createdAt: s.CreatedAt.UTC(), labelledAt: shared.UTC(s.LabelledAt), shippedAt: shared.UTC(s.ShippedAt),
		receivedAt: shared.UTC(s.ReceivedAt), version: s.Version,
	}, nil
}

func checkValues(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) || !shared.ValidID(s.PlanID) || !shared.ValidID(s.NodeID) {
		return errors.New("shipmentId, planId and nodeId must be valid ids")
	}
	if !shared.ValidOwnerRef(s.SellerID) {
		return errors.New("sellerId is not a valid id")
	}
	if s.CreatedAt.IsZero() {
		return errors.New("createdAt is required")
	}
	if s.Version < 1 {
		return errors.New("version must be at least 1")
	}
	if len(s.Cartons) > MaxCartons {
		return fmt.Errorf("a shipment holds at most %d cartons", MaxCartons)
	}
	return checkState(s)
}

func checkState(s Snapshot) error {
	if err := checkInstants(s); err != nil {
		return err
	}
	sealed := s.State != Draft
	for i, c := range s.Cartons {
		if c.Sealed != sealed {
			return fmt.Errorf("cartons[%d].sealed must be %t in state %s", i, sealed, s.State)
		}
		if err := c.check(); err != nil {
			return fmt.Errorf("cartons[%d]: %w", i, err)
		}
	}
	return nil
}

// checkInstants checks the cartons and instants each state requires.
func checkInstants(s Snapshot) error {
	has, lab, shp, rec := len(s.Cartons) > 0, !s.LabelledAt.IsZero(), !s.ShippedAt.IsZero(), !s.ReceivedAt.IsZero()
	var ok bool
	switch s.State {
	case Draft:
		ok = all(!lab, !shp, !rec)
	case Labelled:
		ok = all(has, lab, !shp, !rec)
	case Shipped, Receiving:
		ok = all(has, lab, shp, !rec)
	case Closed, ClosedWithDiscrepancy:
		ok = all(has, shp, rec)
	default:
		return errors.New("unknown shipment state")
	}
	if !ok {
		return fmt.Errorf("the cartons and instants do not fit state %s", s.State)
	}
	return nil
}

func all(conds ...bool) bool {
	for _, c := range conds {
		if !c {
			return false
		}
	}
	return true
}

// Label freezes the shipment: DRAFT to LABELLED, every carton sealed, and
// InboundShipmentLabelled raised. rules supply the plan facts the shipment
// cannot know by itself.
func (s *Shipment) Label(rules PlanRules, now time.Time) ([]shared.Event, error) {
	if s.state != Draft {
		return nil, fmt.Errorf("%w: shipment is %s, only a DRAFT shipment can be labelled", shared.ErrIllegalTransition, s.state)
	}
	if len(s.cartons) == 0 {
		return nil, shared.Invalid("a shipment needs at least one carton before it is labelled")
	}
	if rules.SingleShipment {
		if err := s.matchesPlanExactly(rules.PlanQty); err != nil {
			return nil, err
		}
	}
	for i := range s.cartons {
		s.cartons[i].Sealed = true
	}
	s.state, s.labelledAt = Labelled, now.UTC()
	s.version++
	ev := InboundShipmentLabelled{
		Header: shared.Header{Version: s.version, At: s.labelledAt}, ShipmentID: s.id, PlanID: s.planID,
		SellerID: s.sellerID, NodeID: s.nodeID, Cartons: cloneCartons(s.cartons),
	}
	return []shared.Event{ev}, nil
}

// matchesPlanExactly: for a plan with a single shipment, the cartons carry
// exactly the planned eaches of every line.
func (s *Shipment) matchesPlanExactly(planQty map[string]int) error {
	have := s.qtyByItem()
	for item, want := range planQty {
		if have[item] != want {
			return fmt.Errorf("%w: item %q carries %d eaches, the plan line has %d",
				ErrCartonContentsMismatchPlan, item, have[item], want)
		}
	}
	return nil
}

// Ship moves LABELLED to SHIPPED and raises InboundShipmentShipped. A shipment
// that is not labelled cannot ship (ErrShipmentNotLabelled).
func (s *Shipment) Ship(now time.Time) ([]shared.Event, error) {
	switch s.state {
	case Draft:
		return nil, ErrShipmentNotLabelled
	case Labelled:
	default:
		return nil, fmt.Errorf("%w: shipment is %s, only a LABELLED shipment can ship", shared.ErrIllegalTransition, s.state)
	}
	for _, c := range s.cartons {
		if !c.Sealed {
			return nil, ErrShipmentNotLabelled
		}
	}
	s.state, s.shippedAt = Shipped, now.UTC()
	s.version++
	ev := InboundShipmentShipped{
		Header: shared.Header{Version: s.version, At: s.shippedAt}, ShipmentID: s.id, PlanID: s.planID,
		SellerID: s.sellerID, NodeID: s.nodeID,
	}
	return []shared.Event{ev}, nil
}

func (s *Shipment) qtyByItem() map[string]int {
	out := map[string]int{}
	for _, c := range s.cartons {
		for _, ct := range c.Contents {
			out[ct.CatalogItemID] += ct.QtyEaches
		}
	}
	return out
}

// QtyByItem returns the eaches this shipment carries per catalogItemId.
func (s *Shipment) QtyByItem() map[string]int { return s.qtyByItem() }

// Snapshot returns the persisted form.
func (s *Shipment) Snapshot() Snapshot {
	return Snapshot{ID: s.id, PlanID: s.planID, SellerID: s.sellerID, NodeID: s.nodeID, State: s.state,
		Cartons: cloneCartons(s.cartons), Unexpected: append([]string(nil), s.unexpected...), Discrepant: s.discrepant,
		CreatedAt: s.createdAt, LabelledAt: s.labelledAt, ShippedAt: s.shippedAt, ReceivedAt: s.receivedAt, Version: s.version}
}

// ID returns the shipmentId.
func (s *Shipment) ID() ID { return s.id }

// PlanID returns the plan the shipment belongs to.
func (s *Shipment) PlanID() string { return s.planID }

// SellerID returns the owner of the stock.
func (s *Shipment) SellerID() string { return s.sellerID }

// NodeID returns the destination node.
func (s *Shipment) NodeID() string { return s.nodeID }

// State returns the lifecycle state.
func (s *Shipment) State() State { return s.state }

// Cartons returns a copy of the cartons.
func (s *Shipment) Cartons() []Carton { return cloneCartons(s.cartons) }

// Version returns the optimistic-concurrency version.
func (s *Shipment) Version() int64 { return s.version }
