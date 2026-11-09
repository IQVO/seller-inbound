package network

import (
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// ObservationWindow is the demand window of the placement advisory (ADR 0012):
// the trailing period over which DemandHeatView sums released units. A window
// long enough to smooth a busy day, short enough to react to a trend: 28 days.
const ObservationWindow = 28 * 24 * time.Hour

// Node operators, as node-topology declares them (NodeRegistered.operator).
const (
	OperatorNetwork = "NETWORK"
	OperatorVendor  = "VENDOR"
)

// DemandLine is one order line of one released assignment group: the network
// committed `Quantity` units of `CatalogItemID` to ship from `NodeID`. Lines
// are keyed by (AssignmentID, GroupNo, LineNo): a re-release of the same
// group (a reassignment) REPLACES its lines, so demand lands on exactly the
// node the network last released it to, never twice.
type DemandLine struct {
	AssignmentID  string
	GroupNo       int
	LineNo        int
	NodeID        string
	CatalogItemID string
	Quantity      int
	ReleasedAt    time.Time
}

// DemandRelease is one FulfillmentReleasedToNode event folded into
// DemandHeatView: every line of the group, released to one node at one
// instant. An empty line set is a no-op fold (the event carries none).
type DemandRelease struct {
	AssignmentID string
	GroupNo      int
	NodeID       string
	ReleasedAt   time.Time
	Lines        []ReleaseLine
}

// ReleaseLine is one line of a DemandRelease.
type ReleaseLine struct {
	LineNo        int
	CatalogItemID string
	Quantity      int
}

// Validate checks the release before it is recorded. A release with no lines
// is valid: the projection keeps it as a cancel-by-replacement of an earlier
// release of the same group.
func (r DemandRelease) Validate() error {
	if !shared.ValidID(r.AssignmentID) {
		return shared.Invalid("assignmentId must be a valid id")
	}
	if r.GroupNo < 1 {
		return shared.Invalid("groupNo must be at least 1")
	}
	if !shared.ValidID(r.NodeID) {
		return shared.Invalid("nodeId must be a valid id")
	}
	if r.ReleasedAt.IsZero() {
		return shared.Invalid("releasedAt is required")
	}
	for i, l := range r.Lines {
		if l.LineNo < 1 {
			return shared.Invalid("lines[%d].lineNo must be at least 1", i)
		}
		if !shared.ValidID(l.CatalogItemID) {
			return shared.Invalid("lines[%d].catalogItemId must be a valid id", i)
		}
		if l.Quantity < 1 {
			return shared.Invalid("lines[%d].quantity must be at least 1", i)
		}
	}
	return nil
}

// LinesOf expands the release into its stored demand lines.
func (r DemandRelease) LinesOf() []DemandLine {
	out := make([]DemandLine, 0, len(r.Lines))
	for _, l := range r.Lines {
		out = append(out, DemandLine{AssignmentID: r.AssignmentID, GroupNo: r.GroupNo, LineNo: l.LineNo,
			NodeID: r.NodeID, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity, ReleasedAt: r.ReleasedAt})
	}
	return out
}

// RegionHeat is one region's demand for one item over the observation
// window: the sum of the live demand lines at the region's nodes, and the
// newest release instant among them.
type RegionHeat struct {
	RegionID       string
	DemandUnits    int
	LastReleasedAt time.Time
}

// NodeAvailability is one node's current declared availability of one item,
// from offer-availability's ListingAvailabilityChanged (ADR 0012). AvailableQty
// is what the listing declared, not what the network counted.
type NodeAvailability struct {
	ListingID     string
	CatalogItemID string
	NodeID        string
	AvailableQty  int
	AsOf          time.Time
}

// Validate checks the availability row before it is stored.
func (n NodeAvailability) Validate() error {
	if !shared.ValidID(n.ListingID) {
		return shared.Invalid("listingId must be a valid id")
	}
	if !shared.ValidID(n.CatalogItemID) || !shared.ValidID(n.NodeID) {
		return shared.Invalid("catalogItemId and nodeId must be valid ids")
	}
	if n.AvailableQty < 0 {
		return shared.Invalid("availableQty must not be negative")
	}
	if n.AsOf.IsZero() {
		return shared.Invalid("asOf is required")
	}
	return nil
}

// IsPlacementCandidate reports whether the node is a placement target of the
// advisory: an ACTIVE, vendor-operated fulfillment center (ADR 0012). Seller
// stock sits at vendor nodes; network-operated buildings and non-FC types are
// not where a seller's forward inbound belongs.
func (n Node) IsPlacementCandidate() bool {
	return n.Type == TypeFulfillmentCenter && n.Status == NodeActive && n.Operator == OperatorVendor
}
