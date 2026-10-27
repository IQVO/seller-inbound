package usecases_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
)

// recordingMetrics counts the ports.ShipmentMetrics calls of a use case.
type recordingMetrics struct {
	mu       sync.Mutex
	shipped  int
	rejected []string
}

var _ ports.ShipmentMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) ShipmentShipped(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shipped++
}

func (m *recordingMetrics) ShipmentRejected(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejected = append(m.rejected, reason)
}

// newMeteredEnv is newEnv with the business counter injected.
func newMeteredEnv(t *testing.T) (*env, *recordingMetrics) {
	t.Helper()
	m := &recordingMetrics{}
	store := memory.NewStore()
	clock := memory.NewClock(t0)
	issuer := &memory.SsccIssuer{}
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Sellers: store.Sellers(), Plans: store.Plans(), Shipments: store.Shipments(), Nodes: store.Nodes(), Scorecard: store.Scorecard(),
		Packaging: store.Packaging(), Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: clock, IDs: &memory.SequentialIDs{}, SSCC: issuer,
		Metrics: m,
	}, usecases.Config{})
	return &env{svc: svc, store: store, clock: clock, sscc: issuer}, m
}

func TestShipInboundShipmentCountsEachAttemptOnceByOutcome(t *testing.T) {
	e, m := newMeteredEnv(t)
	ctx := context.Background()
	shp, _ := twoCartonShipment(t, e)

	if _, err := e.svc.ShipInboundShipment(ctx, shp); err == nil {
		t.Fatal("shipping before labelling must be refused")
	}
	if m.shipped != 0 || len(m.rejected) != 1 || m.rejected[0] != "shipment-not-labelled" {
		t.Fatalf("after a refusal: shipped=%d rejected=%v, want 0 / [shipment-not-labelled]", m.shipped, m.rejected)
	}

	if _, err := e.svc.LabelShipment(ctx, shp); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ShipInboundShipment(ctx, shp); err != nil {
		t.Fatal(err)
	}
	if m.shipped != 1 || len(m.rejected) != 1 {
		t.Fatalf("after a shipment: shipped=%d rejected=%v, want 1 / one earlier refusal", m.shipped, m.rejected)
	}
}

func TestShipInboundShipmentRejectionReasonIsTheProblemSlug(t *testing.T) {
	e, m := newMeteredEnv(t)
	if _, err := e.svc.ShipInboundShipment(context.Background(), "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("ship an unknown shipment: %v", err)
	}
	if len(m.rejected) != 1 || m.rejected[0] != "not-found" || m.shipped != 0 {
		t.Fatalf("shipped=%d rejected=%v, want one not-found", m.shipped, m.rejected)
	}
}

// A service built without Metrics (every other test) must not panic.
func TestShipInboundShipmentWithoutMetricsIsANoOp(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.ShipInboundShipment(context.Background(), "missing"); err == nil {
		t.Fatal("want an error")
	}
}
