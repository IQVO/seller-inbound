package kafka

import (
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/seller-inbound/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// The CloudEvents entity segments of this context's aggregates.
const (
	entityInboundPlan     = "inboundplan"
	entityInboundShipment = "inboundshipment"
	entityPlacementPlan   = "placementplan"
	entitySeller          = "seller"
)

// projectedKinds maps the FULL CloudEvents type of each projected event
// (ADR 0017) to its report.Kind.
func projectedKinds() map[string]report.Kind {
	return map[string]report.Kind{
		cloudevents.Type(entityInboundPlan, string(report.KindInboundPlanConfirmed)):        report.KindInboundPlanConfirmed,
		cloudevents.Type(entityPlacementPlan, string(report.KindPlacementPlanSuggested)):    report.KindPlacementPlanSuggested,
		cloudevents.Type(entityInboundShipment, string(report.KindInboundShipmentReceived)): report.KindInboundShipmentReceived,
	}
}

// acknowledgedTypes are this context's other published types: committed past
// without a warning, because no report needs them (ADR 0017).
func acknowledgedTypes() map[string]bool {
	return map[string]bool{
		cloudevents.Type(entityInboundShipment, "InboundShipmentLabelled"):    true,
		cloudevents.Type(entityInboundShipment, "InboundShipmentShipped"):     true,
		cloudevents.Type(entityInboundShipment, "InboundDiscrepancyRecorded"): true,
		cloudevents.Type(entitySeller, "SellerOnboarded"):                     true,
		cloudevents.Type(entitySeller, "SellerSuspended"):                     true,
		cloudevents.Type(entitySeller, "SellerReinstated"):                    true,
		cloudevents.Type(entitySeller, "SellerComplianceViolationRecorded"):   true,
	}
}

// receivedLineData is one item of InboundShipmentReceived.received.
type receivedLineData struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
}

// analyticsData is the union of the payload fields the projection reads,
// hand-mirrored from apis/asyncapi.yaml (never imported from the encoder).
// Unknown fields are ignored; an absent timestamp stays zero.
type analyticsData struct {
	PlanID        string             `json:"planId"`
	ShipmentID    string             `json:"shipmentId"`
	SellerID      string             `json:"sellerId"`
	CatalogItemID string             `json:"catalogItemId"`
	ConfirmedAt   time.Time          `json:"confirmedAt"`
	CreatedAt     time.Time          `json:"createdAt"`
	ReceivedAt    time.Time          `json:"receivedAt"`
	Received      []receivedLineData `json:"received"`
}

// toEvent decodes and validates a CloudEvent of a projected kind. Every error
// is deterministic: the same bytes can never pass.
func toEvent(kind report.Kind, evt ce.Event) (report.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return report.Event{}, fmt.Errorf("decode %s %s data: %w", kind, evt.ID(), err)
	}
	subjectOf, occurredAt := d.aggregate(kind)
	if subjectOf == "" || subjectOf != evt.Subject() {
		return report.Event{}, fmt.Errorf("%s %s: the aggregate id %q does not match the subject %q", kind, evt.ID(), subjectOf, evt.Subject())
	}
	e := report.Event{
		Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), SubjectID: subjectOf, SellerID: d.SellerID,
		OccurredAt: occurredAt.UTC(), CatalogItemID: d.CatalogItemID,
	}
	for _, l := range d.Received {
		e.Lines = append(e.Lines, report.ReceivedLine{GTIN: l.GTIN, CatalogItemID: l.CatalogItemID, QtyEaches: l.QtyEaches})
	}
	if err := e.Validate(); err != nil {
		return report.Event{}, err
	}
	return e, nil
}

// aggregate is the aggregate id a kind's event is about and its business
// instant: the plan and `confirmedAt`, the advisory's plan and `createdAt`, or
// the shipment and `receivedAt`.
func (d analyticsData) aggregate(kind report.Kind) (id string, at time.Time) {
	switch kind {
	case report.KindInboundPlanConfirmed:
		return d.PlanID, d.ConfirmedAt
	case report.KindPlacementPlanSuggested:
		return d.PlanID, d.CreatedAt
	default:
		return d.ShipmentID, d.ReceivedAt
	}
}
