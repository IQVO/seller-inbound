// Package network holds the local read models of seller-inbound, fed by the
// events of its neighbours (ADR 0003, ADR 0012): NodeInboundView
// (node-topology: type, status, region, operator, inbound limits),
// CatalogPackagingView (offer-availability pack levels) and DemandHeatView
// (order-sourcing released demand plus offer-availability listing
// availability, the facts of the placement advisory). All are projections,
// not aggregates: they are rebuilt from events that may arrive twice or out
// of order, so every transition below is order-tolerant and idempotent.
package network

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

// ErrNodeCannotReceive: the destination node is unknown, not ACTIVE, lacks the
// INBOUND capability or does not accept pallets (problem type node-cannot-receive, 409).
var ErrNodeCannotReceive = errors.New("node cannot receive")

// NodeStatus is the lifecycle status of a node as node-topology reports it.
type NodeStatus string

// The four node statuses.
const (
	NodeProposed  NodeStatus = "PROPOSED"
	NodeActive    NodeStatus = "ACTIVE"
	NodeSuspended NodeStatus = "SUSPENDED"
	NodeRetired   NodeStatus = "RETIRED"
)

// Node types that carry the INBOUND capability (ADR 0009 of the model, decision 9).
const (
	TypeFulfillmentCenter = "FULFILLMENT_CENTER"
	TypeInboundCrossDock  = "INBOUND_CROSS_DOCK"
)

// Dims is the largest carton a node accepts, in millimetres.
type Dims struct{ L, W, H int }

// InboundLimits are the inbound limits a node declared. Pointers and zero
// values mean "not declared".
type InboundLimits struct {
	MaxCartonWeightG int
	MaxCartonDims    *Dims
	AcceptsPallets   *bool
	RequiresSscc     *bool
}

// Node is one row of NodeInboundView.
type Node struct {
	ID     string
	Type   string
	Status NodeStatus
	// Operator is who runs the building (NETWORK or VENDOR), and RegionID the
	// node-topology region the building sits in: both declared once at
	// registration and kept for the placement advisory (ADR 0012).
	Operator        string
	RegionID        string
	Limits          *InboundLimits
	ProfileRevision int
}

// CanReceive returns ErrNodeCannotReceive unless the node is ACTIVE and its type
// has the INBOUND capability. A node with no declared limits receives cartons
// with the limits unchecked.
func (n Node) CanReceive() error {
	if n.Status != NodeActive {
		return fmt.Errorf("%w: node %s is %s", ErrNodeCannotReceive, n.ID, n.Status)
	}
	if n.Type != TypeFulfillmentCenter && n.Type != TypeInboundCrossDock {
		return fmt.Errorf("%w: node %s of type %q has no INBOUND capability", ErrNodeCannotReceive, n.ID, n.Type)
	}
	return nil
}

// Registered folds NodeRegistered into the row: the type, operator and region
// are set, and a status already seen (an out-of-order activation) is kept.
// The registration is the only carrier of the region and the operator; a
// re-registration of a known node refreshes them.
func (n Node) Registered(id, nodeType, operator, regionID string) (Node, error) {
	if !shared.ValidID(id) {
		return Node{}, shared.Invalid("nodeId must be a valid id")
	}
	if operator != "" && operator != OperatorNetwork && operator != OperatorVendor {
		return Node{}, shared.Invalid("operator must be NETWORK or VENDOR")
	}
	if regionID != "" && !shared.ValidID(regionID) {
		return Node{}, shared.Invalid("regionId must be a valid id")
	}
	out := n
	out.ID, out.Type = id, nodeType
	if operator != "" {
		out.Operator = operator
	}
	if regionID != "" {
		out.RegionID = regionID
	}
	if out.Status == "" {
		out.Status = NodeProposed
	}
	return out, nil
}

// Activated folds NodeActivated; a RETIRED node stays retired.
func (n Node) Activated(id string) Node { return n.moveTo(id, NodeActive) }

// Suspended folds NodeSuspended; a RETIRED node stays retired.
func (n Node) Suspended(id string) Node { return n.moveTo(id, NodeSuspended) }

// Retired folds NodeRetired.
func (n Node) Retired(id string) Node { return n.moveTo(id, NodeRetired) }

func (n Node) moveTo(id string, to NodeStatus) Node {
	out := n
	out.ID = id
	if out.Status != NodeRetired {
		out.Status = to
	}
	return out
}

// ProfileDeclared folds NodeOperationalProfileDeclared: only a revision newer
// than the one held replaces the limits (nil limits = none declared).
func (n Node) ProfileDeclared(id string, revision int, limits *InboundLimits) Node {
	out := n
	out.ID = id
	if revision > out.ProfileRevision {
		out.ProfileRevision, out.Limits = revision, limits
	}
	return out
}

// Level is a packaging level.
type Level string

// The four packaging levels, lowest first.
const (
	Each   Level = "EACH"
	Inner  Level = "INNER"
	Case   Level = "CASE"
	Pallet Level = "PALLET"
)

// Valid reports whether l is one of the four levels.
func (l Level) Valid() bool { return l == Each || l == Inner || l == Case || l == Pallet }

// PackLevel is one row of CatalogPackagingView: a level of a catalogue item.
type PackLevel struct {
	CatalogItemID string
	Level         Level
	GTIN          string
	ChildGTIN     string
	UnitsPerPack  int
	Active        bool
	DeclaredAt    time.Time
	RetiredAt     time.Time
}

func checkKey(item string, level Level, gtin string) (string, error) {
	g, err := gs1.ParseGTIN(gtin)
	if err != nil {
		return "", shared.Invalid("gtin: %v", err)
	}
	if !shared.ValidID(item) || !level.Valid() {
		return "", shared.Invalid("catalogItemId and level must be valid")
	}
	return g.String(), nil
}

// Declare folds CatalogItemPackagingDeclared into the existing row (nil when
// none). A GTIN that was retired is never reactivated: a retired GTIN is never reused.
func Declare(existing *PackLevel, item string, level Level, gtin, child string, unitsPerPack int, at time.Time) (PackLevel, error) {
	g, err := checkKey(item, level, gtin)
	if err != nil {
		return PackLevel{}, err
	}
	if unitsPerPack < 1 {
		return PackLevel{}, shared.Invalid("unitsPerPack must be at least 1")
	}
	out := PackLevel{CatalogItemID: item, Level: level, GTIN: g, ChildGTIN: child, UnitsPerPack: unitsPerPack, Active: true, DeclaredAt: at.UTC()}
	if existing != nil && !existing.Active {
		out.Active, out.RetiredAt = false, existing.RetiredAt
	}
	return out, nil
}

// Retire folds CatalogItemPackLevelRetired into the existing row (nil when none).
func Retire(existing *PackLevel, item string, level Level, gtin string, at time.Time) (PackLevel, error) {
	g, err := checkKey(item, level, gtin)
	if err != nil {
		return PackLevel{}, err
	}
	out := PackLevel{CatalogItemID: item, Level: level, GTIN: g}
	if existing != nil {
		out = *existing
	}
	out.Active, out.RetiredAt = false, at.UTC()
	return out, nil
}

// ResolveEach answers, among one catalogue item's CatalogPackagingView rows,
// whether the view holds the 14-digit gtin at all (known) and the EACH-level
// GTIN its childGtin chain resolves to (each, "" when the chain breaks: no
// child, or a child the view does not hold). Retired rows resolve too:
// retirement stops new packing at a level, not the identity of a GTIN
// (ADR 0014).
func ResolveEach(rows []PackLevel, gtin string) (each string, known bool) {
	row, ok := rowOf(rows, gtin)
	if !ok {
		return "", false
	}
	for range rows {
		if row.Level == Each {
			return row.GTIN, true
		}
		child, err := gs1.ParseGTIN(row.ChildGTIN)
		if err != nil {
			return "", true
		}
		if row, ok = rowOf(rows, child.String()); !ok {
			return "", true
		}
	}
	return "", true // a childGtin cycle never reaches EACH
}

// rowOf returns the row of the gtin, preferring an active one.
func rowOf(rows []PackLevel, gtin string) (PackLevel, bool) {
	var found *PackLevel
	for i := range rows {
		if rows[i].GTIN != gtin {
			continue
		}
		if rows[i].Active {
			return rows[i], true
		}
		if found == nil {
			found = &rows[i]
		}
	}
	if found == nil {
		return PackLevel{}, false
	}
	return *found, true
}

// ActiveCaseUnits returns unitsPerPack of the ACTIVE CASE level with the given
// 14-digit gtin among the item's rows.
func ActiveCaseUnits(rows []PackLevel, gtin string) (int, bool) {
	for _, r := range rows {
		if r.Level == Case && r.Active && r.GTIN == gtin && r.UnitsPerPack >= 2 {
			return r.UnitsPerPack, true
		}
	}
	return 0, false
}
