package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/seller-inbound/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/seller-inbound"

// shipmentsShippedName is the Tier 2 business metric of the context (ADR 0008,
// the fleet's standard metrics convention): every attempt to ship an inbound
// shipment, by outcome.
const shipmentsShippedName = "seller_inbound.shipment.shipped"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeShipped  = "shipped"
	outcomeRejected = "rejected"
)

// ShipmentMetrics implements ports.ShipmentMetrics against the global
// MeterProvider. Until Setup installs a real provider the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type ShipmentMetrics struct {
	counter metric.Int64Counter
}

var _ ports.ShipmentMetrics = (*ShipmentMetrics)(nil)

// NewShipmentMetrics registers the counter. It only fails if the instrument
// name is invalid, which is a programming error, not a runtime condition. Call
// it after Setup so the instrument binds to the real MeterProvider.
func NewShipmentMetrics() (*ShipmentMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		shipmentsShippedName,
		metric.WithDescription("Attempts to ship an inbound shipment by outcome (shipped or rejected; reason says why a rejection "+
			"happened, as a problem slug such as shipment-not-labelled, seller-suspended or node-cannot-receive). "+
			"A falling shipped share with a rising shipment-not-labelled means sellers reach the ship step before labelling every carton; "+
			"a rising node-cannot-receive means destination nodes are refusing stock; a rising internal-error means the service itself is failing."),
		metric.WithUnit("{shipment}"),
	)
	if err != nil {
		return nil, err
	}
	return &ShipmentMetrics{counter: counter}, nil
}

// ShipmentShipped counts a shipment that was shipped.
func (m *ShipmentMetrics) ShipmentShipped(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeShipped)))
}

// ShipmentRejected counts a ship attempt that shipped nothing, with the reason why.
func (m *ShipmentMetrics) ShipmentRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}
