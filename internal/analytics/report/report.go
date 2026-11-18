// Package report is the self-contained read-model region of the analytics read
// side (ADR 0017): the facts the projector folds in, the shape of the
// seller-inbound performance report, the report rules (the bounded per-item
// list, the latency percentile ranks, the unit conversion), the date-range
// rules, the freshness lag, and the writer/reader ports the analytical store
// implements. It imports no other internal package (arch test): nothing here is
// imported from the domain, so the OLTP model can never leak into the
// projection.
package report

import (
	"context"
	"errors"
	"time"
)

// Kind is which of the three projected events a fact came from. The names are
// the CloudEvents event names (the last segment of `type`).
type Kind string

// The three projected events of this context's two topics.
const (
	// KindInboundPlanConfirmed is on retailnetwork.seller-inbound.analytics.
	KindInboundPlanConfirmed Kind = "InboundPlanConfirmed"
	// KindPlacementPlanSuggested is on retailnetwork.seller-inbound.events.
	KindPlacementPlanSuggested Kind = "PlacementPlanSuggested"
	// KindInboundShipmentReceived is on retailnetwork.seller-inbound.events.
	KindInboundShipmentReceived Kind = "InboundShipmentReceived"
)

// ReceivedLine is one item of an InboundShipmentReceived `received` array: the
// stock received of one GTIN, in eaches. CatalogItemID is empty when the
// receipt did not resolve it (the event omits it then).
type ReceivedLine struct {
	GTIN          string
	CatalogItemID string
	QtyEaches     int
}

// Event is one decoded analytics fact ready to project. It is the union of the
// three payloads; only the fields of its Kind are set.
type Event struct {
	Kind    Kind
	EventID string    // CloudEvents id: the idempotency key
	At      time.Time // CloudEvents time: when the use case raised it (freshness)

	// SubjectID is the aggregate the fact is about: the plan (InboundPlanConfirmed,
	// PlacementPlanSuggested) or the shipment (InboundShipmentReceived).
	SubjectID string
	SellerID  string
	// OccurredAt is the business instant of the fact: `confirmedAt`, `createdAt`
	// or `receivedAt`. The reports bucket days on it.
	OccurredAt time.Time

	// PlacementPlanSuggested.
	CatalogItemID string

	// InboundShipmentReceived: the stock received per item. Empty for a receipt
	// published before ADR 0014 (it has no `received`) or one where nothing counts.
	Lines []ReceivedLine
}

// Projection is the WRITER port: Apply records the event id and projects the
// fact in ONE transaction. applied is false when the id was already recorded
// (a replay): nothing changed. An error wrapping ErrRejected is deterministic
// (the store can never accept this event: retry is pointless); any other error
// is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("report: event rejected by the analytical store")

// Reader is the READER port. Every method answers for the half-open instant
// range [r.From, r.To) of the business instants, bucketed by UTC day. Results
// are ordered by day and never nil.
type Reader interface {
	// InboundCounts counts, per day, the plans confirmed, the placement
	// advisories issued and the shipment receipts published.
	InboundCounts(ctx context.Context, r Range) ([]DayCounts, error)
	// ReceivedItems aggregates, per day and GTIN, the received-stock lines.
	// It is NOT bounded: PerformanceDays keeps the top ReceivedItemsLimit.
	ReceivedItems(ctx context.Context, r Range) ([]DayItem, error)
	// AdvisoryToReceipt aggregates, per day of the receipt, the seconds from
	// the latest preceding placement advisory of the same seller and item to
	// a received line of that item.
	AdvisoryToReceipt(ctx context.Context, r Range) ([]DayLatency, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// applied (nil while it applied none): the basis of the freshness lag.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// DayCounts is what the store counts for one UTC day.
type DayCounts struct {
	Day               time.Time
	PlansConfirmed    int
	AdvisoriesIssued  int
	ShipmentsReceived int
}

// DayItem is what the store aggregates for one UTC day of receipt and one GTIN:
// the received-stock lines (publications) and their eaches. CatalogItemID is
// empty when no line of that day resolved it.
type DayItem struct {
	Day           time.Time
	GTIN          string
	CatalogItemID string
	Publications  int
	QtyEaches     int
}

// DayLatency is what the store aggregates for one UTC day of receipt: the
// samples (received lines with a preceding advisory) and the continuous
// percentiles of the seconds between the two instants, at LatencyP50 and
// LatencyP95.
type DayLatency struct {
	Day     time.Time
	Samples int
	P50     float64
	P95     float64
}
