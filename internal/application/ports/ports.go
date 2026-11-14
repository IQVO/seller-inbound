// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// sentinels of errors.go.
//
// Optimistic concurrency: every Save of an aggregate expects the stored version
// to be aggregate.Version()-1 (ADR 0002); a create (Version() 1) expects that
// the id is not stored. Any mismatch, including an update of a row that no
// longer exists, is ErrConcurrentUpdate. Inside a UnitOfWork every repository
// joins the transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/capacity"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// SellerRepository persists SellerAccount aggregates.
type SellerRepository interface {
	// Get returns the account, or ErrNotFound.
	Get(ctx context.Context, id seller.ID) (*seller.Account, error)
	// Save persists the account under the concurrency rule of the package comment.
	Save(ctx context.Context, a *seller.Account) error
}

// PlanRepository persists InboundPlan aggregates.
type PlanRepository interface {
	// Get returns the plan, or ErrNotFound.
	Get(ctx context.Context, id inboundplan.ID) (*inboundplan.Plan, error)
	// Save persists the plan under the concurrency rule of the package comment.
	Save(ctx context.Context, p *inboundplan.Plan) error
}

// ShipmentRepository persists InboundShipment aggregates with their cartons.
type ShipmentRepository interface {
	// Get returns the shipment, or ErrNotFound.
	Get(ctx context.Context, id inboundshipment.ID) (*inboundshipment.Shipment, error)
	// ListByPlan returns every shipment of the plan, oldest first.
	ListByPlan(ctx context.Context, planID inboundplan.ID) ([]*inboundshipment.Shipment, error)
	// Save persists the shipment under the concurrency rule of the package comment
	// and registers its carton SSCCs network-wide; an SSCC held by another shipment
	// is ErrSsccTaken and nothing is saved.
	Save(ctx context.Context, s *inboundshipment.Shipment) error
}

// NodeViewRepository stores the NodeInboundView read model (upsert, no version).
type NodeViewRepository interface {
	// Get returns the node, or ErrNotFound.
	Get(ctx context.Context, nodeID string) (network.Node, error)
	// Save inserts or replaces the node row.
	Save(ctx context.Context, n network.Node) error
	// VendorFulfillmentCenters returns every ACTIVE vendor FULFILLMENT_CENTER
	// the view holds (ADR 0012), ordered by node id. Placement suggestions
	// are split across these only.
	VendorFulfillmentCenters(ctx context.Context) ([]network.Node, error)
	// RegionFulfillmentCenters returns every ACTIVE FULFILLMENT_CENTER of the
	// region, ordered by node id (ADR 0015): the candidates a NETWORK_SPLIT
	// plan's redistribution is computed from.
	RegionFulfillmentCenters(ctx context.Context, regionID string) ([]network.Node, error)
}

// PackagingViewRepository stores the CatalogPackagingView read model (upsert).
type PackagingViewRepository interface {
	// Get returns the (item, level, gtin) row, or ErrNotFound.
	Get(ctx context.Context, catalogItemID string, level network.Level, gtin string) (network.PackLevel, error)
	// Save inserts or replaces the row.
	Save(ctx context.Context, p network.PackLevel) error
	// ListByItem returns every row of the catalogue item, active or not.
	ListByItem(ctx context.Context, catalogItemID string) ([]network.PackLevel, error)
}

// DemandHeatView stores the demand facts of the placement advisory's demand
// view (ADR 0012): one row per live released line of an assignment group,
// replaced wholesale by the group's next release and removed by its
// cancellation, so re-releases (reassignments) never double-count.
type DemandHeatView interface {
	// RecordDemand upserts the release: the group's previous lines are
	// replaced by this release's lines. Called with the ctx handed to
	// UnitOfWork.Do it joins the SAME transaction as the inbox claim.
	RecordDemand(ctx context.Context, release network.DemandRelease) error
	// CancelDemand removes every line of the assignment group (the demand is
	// gone). Cancelling an unknown group is a no-op: the cancel may arrive
	// before the release in a replay.
	CancelDemand(ctx context.Context, assignmentID string, groupNo int) error
	// RegionHeat answers one item's per-region demand over the window:
	// the sum of the live lines at each region's nodes (a line whose node the
	// NodeInboundView has not seen yet waits until node-topology catches up).
	// Regions come back ordered by region id; a heat with zero demand is
	// never returned.
	RegionHeat(ctx context.Context, catalogItemID string, windowStart time.Time) ([]network.RegionHeat, error)
}

// ListingView stores the declared-availability half of the demand view
// (ADR 0012): the latest ListingAvailabilityChanged per (item, node).
type ListingView interface {
	// SaveAvailability upserts the (item, node) availability row.
	SaveAvailability(ctx context.Context, row network.NodeAvailability) error
	// Availability answers every node's declared availability of the item.
	Availability(ctx context.Context, catalogItemID string) ([]network.NodeAvailability, error)
}

// PlacementPlanRepository persists PlacementPlan aggregates.
type PlacementPlanRepository interface {
	// Get returns the plan, or ErrNotFound.
	Get(ctx context.Context, id placementplan.ID) (*placementplan.Plan, error)
	// Save persists the plan under the concurrency rule of the package comment.
	Save(ctx context.Context, p *placementplan.Plan) error
	// LatestOf returns the seller's newest plan of the item, or ErrNotFound.
	LatestOf(ctx context.Context, sellerID, catalogItemID string) (*placementplan.Plan, error)
	// List returns the seller's plans, newest first (keyset-paged after the
	// (afterCreatedAt, afterPlanID) row the caller last saw -- a zero
	// afterCreatedAt is page one), at most limit rows. An empty catalogItemID
	// means every item. The handler passes one row more than the client's
	// limit so the page can tell whether a next page exists.
	List(ctx context.Context, sellerID, catalogItemID string, afterCreatedAt time.Time, afterPlanID string, limit int) ([]*placementplan.Plan, error)
}

// ScorecardFacts stores and sums the facts of the seller compliance scorecard
// (ADR 0005): one row per closed shipment and one per discrepancy or compliance
// violation, written from the events a receipt raised.
type ScorecardFacts interface {
	// Record appends the facts. Called with the ctx handed to UnitOfWork.Do it
	// joins the SAME transaction as the shipment save and the outbox append.
	// Recording a closed shipment twice keeps the first row. No facts is a no-op.
	Record(ctx context.Context, f scorecard.Facts) error
	// Counts sums the seller's facts recorded in [since, until], both inclusive.
	// A seller with no facts answers zero counts and a non-nil empty map.
	Counts(ctx context.Context, sellerID string, since, until time.Time) (scorecard.Counts, error)
}

// CapacityAllocations stores the inbound capacity reservations of ADR 0015:
// one row per (seller, YYYY-MM month, node) holding the dm3 the seller's
// DRAFT and CONFIRMED plans promise. Maintained transactionally by the use
// cases (CreateInboundPlan reserves, CancelInboundPlan releases, confirm
// keeps); write-side bookkeeping, not an aggregate and not an event stream.
type CapacityAllocations interface {
	// Reserved answers the seller's reserved dm3 for the month and node.
	// An unseen key answers 0.
	Reserved(ctx context.Context, sellerID, month, nodeID string) (int64, error)
	// Reserve adds dm3 to the key's reservation (0 is a no-op).
	Reserve(ctx context.Context, sellerID, month, nodeID string, dm3 int64, at time.Time) error
	// Release subtracts dm3 from the key's reservation, never below 0
	// (0 is a no-op).
	Release(ctx context.Context, sellerID, month, nodeID string, dm3 int64) error
	// Allocations answers the seller's reservations of the month, ordered by
	// node id (the operator-facing honesty surface, ADR 0015).
	Allocations(ctx context.Context, sellerID, month string) ([]capacity.Allocation, error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls, so the aggregates and their raised events commit or roll back
// together. The adapter encodes each event into its CloudEvents envelope (the
// id is minted once, at append) and stores one row per event in order.
// Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// ProcessedEvents is the consumers' inbox (table processed_events).
type ProcessedEvents interface {
	// Claim records the CloudEvents id and reports whether it was new. A false
	// answer means the event was processed before and must be skipped. Inside a
	// UnitOfWork the claim commits or rolls back with the effect.
	Claim(ctx context.Context, eventID, eventType string, at time.Time) (bool, error)
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or
// rolls back together when fn returns an error (or panics).
//
//   - fn MUST use the ctx passed to it for every call that should participate.
//   - Do is re-entrant: when ctx already carries an open unit, Do joins it and
//     the outermost Do owns commit and rollback.
//   - The error fn returns is returned from Do unchanged (errors.Is keeps
//     working); a commit failure is returned wrapped.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock is the service's notion of "now".
type Clock interface {
	Now() time.Time
}

// IDGenerator mints aggregate ids.
type IDGenerator interface {
	// NewID returns prefix + "-" + a random UUID, e.g. "pln-3f6c...".
	NewID(prefix string) string
}

// SsccIssuer issues SSCCs from the network's own GS1 company prefix (policy
// SsccIssuance). The serial comes from a Postgres sequence, so two instances
// never issue the same SSCC.
type SsccIssuer interface {
	// Next returns a fresh 18-digit SSCC with a valid check digit.
	Next(ctx context.Context) (string, error)
}

// ShipmentMetrics is the Tier 2 business counter of the context (ADR 0008):
// every attempt to ship an inbound shipment, counted once by outcome. It is
// fire-and-forget; the zero Deps value gets a no-op (usecases.Deps.Metrics).
type ShipmentMetrics interface {
	// ShipmentShipped counts a shipment that moved to SHIPPED.
	ShipmentShipped(ctx context.Context)
	// ShipmentRejected counts a ship attempt that shipped nothing; reason is
	// the problem slug of apis/openapi.yaml (internal-error for any other).
	ShipmentRejected(ctx context.Context, reason string)
}
