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

	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
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
