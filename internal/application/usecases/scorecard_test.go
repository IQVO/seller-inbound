package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
)

// closeWithDiscrepancy closes the one-carton shipment with a damaged, short
// carton that carries one compliance code: two discrepancies, one violation.
func closeWithDiscrepancy(t *testing.T, e *env) (shipmentID string) {
	t.Helper()
	shp, carton, g := shippedShipment(t, e)
	in := requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", ReceivedAt: t0, Cartons: []requests.ReceiptCarton{
		{SSCC: carton, Status: "DAMAGED", Items: []requests.ReceiptItem{{GTIN: g, QtyEaches: 10}}, ComplianceCodes: []string{"CARTON_CRUSHED"}}}}
	if err := e.svc.ApplyInboundReceipt(context.Background(), "rcpt-1", in); err != nil {
		t.Fatal(err)
	}
	return shp
}

func TestScorecardOfASellerWithNoFactsOmitsRates(t *testing.T) {
	e := newEnv(t)
	e.seed(t)
	sc, err := e.svc.GetSellerScorecard(context.Background(), "S-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Window != "30d" || sc.ShipmentsReceived != 0 || sc.ReceiptAccuracyRate != nil || sc.DiscrepancyRatePerShipment != nil ||
		sc.DiscrepanciesByKind == nil || len(sc.DiscrepanciesByKind) != 0 || sc.ComplianceTier != "GOOD" || sc.SellerID != "S-1" {
		t.Fatalf("%+v", sc)
	}
	if !sc.WindowEnd.Equal(t0) || !sc.WindowStart.Equal(t0.Add(-30*24*time.Hour)) {
		t.Fatalf("window %v .. %v", sc.WindowStart, sc.WindowEnd)
	}
}

func TestScorecardFromAReceiptThatClosedWithDiscrepancy(t *testing.T) {
	e := newEnv(t)
	closeWithDiscrepancy(t, e)
	sc, err := e.svc.GetSellerScorecard(context.Background(), "S-1", "7d")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Window != "7d" || sc.ShipmentsReceived != 1 || sc.CartonsReported != 1 || sc.CartonsReceived != 0 || sc.ComplianceViolations != 1 {
		t.Fatalf("%+v", sc)
	}
	if sc.ReceiptAccuracyRate == nil || *sc.ReceiptAccuracyRate != 0 || sc.DiscrepancyRatePerShipment == nil || *sc.DiscrepancyRatePerShipment != 2 {
		t.Fatalf("rates %v %v", sc.ReceiptAccuracyRate, sc.DiscrepancyRatePerShipment)
	}
	if len(sc.DiscrepanciesByKind) != 2 || sc.DiscrepanciesByKind["Damaged"] != 1 || sc.DiscrepanciesByKind["Short"] != 1 {
		t.Fatalf("%v", sc.DiscrepanciesByKind)
	}
	if sc.ComplianceTier != string(scorecard.TierAtRisk) {
		t.Fatalf("tier %s", sc.ComplianceTier)
	}
}

func TestScorecardWindowBoundaries(t *testing.T) {
	e := newEnv(t)
	closeWithDiscrepancy(t, e) // recorded at t0
	ctx := context.Background()
	shipments := func(window string) int {
		sc, err := e.svc.GetSellerScorecard(ctx, "S-1", window)
		if err != nil {
			t.Fatal(err)
		}
		return sc.ShipmentsReceived
	}
	e.clock.Set(t0.Add(7 * 24 * time.Hour))
	if shipments("7d") != 1 {
		t.Error("a fact exactly 7 days old is inside the 7d window")
	}
	e.clock.Set(t0.Add(7*24*time.Hour + time.Nanosecond))
	if shipments("7d") != 0 || shipments("30d") != 1 {
		t.Error("a fact one nanosecond older than 7 days leaves the 7d window but not the 30d one")
	}
	e.clock.Set(t0.Add(30 * 24 * time.Hour))
	if shipments("30d") != 1 {
		t.Error("a fact exactly 30 days old is inside the 30d window")
	}
	e.clock.Set(t0.Add(30*24*time.Hour + time.Nanosecond))
	if shipments("30d") != 0 {
		t.Error("a fact older than 30 days is outside the 30d window")
	}
	// A fact recorded after the instant asked about is not in it either.
	e.clock.Set(t0.Add(-time.Nanosecond))
	if shipments("30d") != 0 {
		t.Error("a fact in the future of the window end is not counted")
	}
}

func TestScorecardRefusals(t *testing.T) {
	e := newEnv(t)
	e.seed(t)
	ctx := context.Background()
	if _, err := e.svc.GetSellerScorecard(ctx, "ghost", "7d"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("unknown seller: %v", err)
	}
	for _, w := range []string{"1d", "90d", "7D", " 7d"} {
		if _, err := e.svc.GetSellerScorecard(ctx, "S-1", w); !errors.Is(err, scorecard.ErrInvalidWindow) {
			t.Errorf("window %q: %v", w, err)
		}
	}
	// The window is validated before the seller is looked up.
	if _, err := e.svc.GetSellerScorecard(ctx, "ghost", "90d"); !errors.Is(err, scorecard.ErrInvalidWindow) {
		t.Errorf("invalid window for an unknown seller: %v", err)
	}
}

// TestScorecardIsInformational proves the tier changes nothing: reading an
// AT_RISK scorecard publishes no event, stores nothing and leaves the seller
// ACTIVE; a suspended seller keeps its scorecard.
func TestScorecardIsInformational(t *testing.T) {
	e := newEnv(t)
	closeWithDiscrepancy(t, e)
	ctx := context.Background()
	before := e.store.Counts()
	events := len(e.store.Outbox().Events())
	sc, err := e.svc.GetSellerScorecard(ctx, "S-1", "30d")
	if err != nil || sc.ComplianceTier != "AT_RISK" {
		t.Fatalf("%+v %v", sc, err)
	}
	if after := e.store.Counts(); after != before || len(e.store.Outbox().Events()) != events {
		t.Fatalf("a read changed the store: %+v -> %+v", before, after)
	}
	acc, _ := e.store.Sellers().Get(ctx, "S-1")
	if acc.Snapshot().State != "ACTIVE" {
		t.Fatalf("the seller state moved: %s", acc.Snapshot().State)
	}
	if _, err := e.svc.SuspendSeller(ctx, requests.SuspendSeller{SellerID: "S-1", Reason: "partner manager decision"}); err != nil {
		t.Fatal(err)
	}
	if sc2, err := e.svc.GetSellerScorecard(ctx, "S-1", "30d"); err != nil || sc2.ComplianceTier != "AT_RISK" {
		t.Fatalf("a suspended seller keeps its scorecard: %+v %v", sc2, err)
	}
}

func TestScorecardCountsEachFactOnceAndPerSeller(t *testing.T) {
	e := newEnv(t)
	shp := closeWithDiscrepancy(t, e)
	ctx := context.Background()
	if _, err := e.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "S-2", Name: "Other Goods"}); err != nil {
		t.Fatal(err)
	}
	// The same receipt event redelivered, and a late receipt under a new id.
	in := requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", ReceivedAt: t0}
	for _, id := range []string{"rcpt-1", "rcpt-late"} {
		if err := e.svc.ApplyInboundReceipt(ctx, id, in); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	sc, _ := e.svc.GetSellerScorecard(ctx, "S-1", "30d")
	if sc.ShipmentsReceived != 1 || sc.ComplianceViolations != 1 || sc.DiscrepanciesByKind["Damaged"] != 1 || sc.DiscrepanciesByKind["Short"] != 1 {
		t.Fatalf("a redelivery counted twice: %+v", sc)
	}
	other, _ := e.svc.GetSellerScorecard(ctx, "S-2", "30d")
	if other.ShipmentsReceived != 0 || other.ComplianceViolations != 0 || len(other.DiscrepanciesByKind) != 0 {
		t.Fatalf("another seller's facts leaked: %+v", other)
	}
}

// TestPartialReceiptCountsFactsBeforeTheShipmentCloses: an unlisted carton is
// recorded at once (a discrepancy and a violation) while the manifest carton is
// still unanswered, so no shipment has closed and no rate exists yet.
func TestPartialReceiptCountsFactsBeforeTheShipmentCloses(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	shp, _, _ := shippedShipment(t, e)
	in := requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", ReceivedAt: t0, Cartons: []requests.ReceiptCarton{
		{SSCC: sscc(t, "9999"), Status: "UNEXPECTED"}}}
	if err := e.svc.ApplyInboundReceipt(ctx, "rcpt-partial", in); err != nil {
		t.Fatal(err)
	}
	sc, err := e.svc.GetSellerScorecard(ctx, "S-1", "30d")
	if err != nil {
		t.Fatal(err)
	}
	if sc.ShipmentsReceived != 0 || sc.ReceiptAccuracyRate != nil || sc.DiscrepancyRatePerShipment != nil {
		t.Fatalf("%+v", sc)
	}
	if sc.DiscrepanciesByKind["UnexpectedCarton"] != 1 || sc.ComplianceViolations != 1 || sc.ComplianceTier != "WATCH" {
		t.Fatalf("%+v", sc)
	}
}
