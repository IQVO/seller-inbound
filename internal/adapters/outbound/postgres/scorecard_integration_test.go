//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/postgres"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/testing/pgtest"
)

func scFacts(seller, shipment string, at time.Time) scorecard.Facts {
	return scorecard.Facts{
		Receipts: []scorecard.Receipt{{SellerID: seller, ShipmentID: shipment, CartonsReported: 5, CartonsReceived: 3, At: at}},
		Occurrences: []scorecard.Occurrence{
			{SellerID: seller, ShipmentID: shipment, Type: scorecard.Discrepancy, Code: "Short", At: at},
			{SellerID: seller, ShipmentID: shipment, Type: scorecard.Discrepancy, Code: "Short", At: at},
			{SellerID: seller, ShipmentID: shipment, Type: scorecard.Discrepancy, Code: "MissingCarton", At: at},
			{SellerID: seller, ShipmentID: shipment, Type: scorecard.Violation, Code: "LABEL_TORN", At: at},
		},
	}
}

func TestScorecardFactsRoundTripWindowAndSellers(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewScorecardFacts(pgtest.NewPool(t))
	if err := repo.Record(ctx, scorecard.Facts{}); err != nil {
		t.Fatalf("recording nothing: %v", err)
	}
	zero, err := repo.Counts(ctx, "S-1", t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || zero.ShipmentsReceived != 0 || zero.Violations != 0 || zero.Discrepancies == nil || len(zero.Discrepancies) != 0 {
		t.Fatalf("a seller with no facts: %+v %v", zero, err)
	}
	if err := repo.Record(ctx, scFacts("S-1", "shp-1", t0)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Record(ctx, scFacts("S-2", "shp-2", t0)); err != nil {
		t.Fatal(err)
	}
	// The closing of a shipment is kept once; a second closing row is ignored.
	again := scFacts("S-1", "shp-1", t0)
	again.Receipts[0].CartonsReceived = 0
	again.Occurrences = nil
	if err := repo.Record(ctx, again); err != nil {
		t.Fatal(err)
	}
	want := scorecard.Counts{ShipmentsReceived: 1, CartonsReported: 5, CartonsReceived: 3, Violations: 1,
		Discrepancies: map[string]int{"Short": 2, "MissingCarton": 1}}
	got, err := repo.Counts(ctx, "S-1", t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("counts = %+v (%v), want %+v", got, err, want)
	}
	// Both ends of the window are inclusive; a second outside either end is out.
	for name, c := range map[string]struct {
		since, until time.Time
		in           bool
	}{
		"since is the fact":         {t0, t0.Add(time.Hour), true},
		"until is the fact":         {t0.Add(-time.Hour), t0, true},
		"since just after the fact": {t0.Add(time.Microsecond), t0.Add(time.Hour), false},
		"until just before":         {t0.Add(-time.Hour), t0.Add(-time.Microsecond), false},
	} {
		got, err := repo.Counts(ctx, "S-1", c.since, c.until)
		if err != nil || (got.ShipmentsReceived == 1) != c.in || (got.Violations == 1) != c.in {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	other, _ := repo.Counts(ctx, "S-2", t0.Add(-time.Hour), t0.Add(time.Hour))
	if other.ShipmentsReceived != 1 || other.Violations != 1 {
		t.Fatalf("S-2: %+v", other)
	}
	none, _ := repo.Counts(ctx, "S-3", t0.Add(-time.Hour), t0.Add(time.Hour))
	if none.ShipmentsReceived != 0 || none.Violations != 0 || len(none.Discrepancies) != 0 {
		t.Fatalf("S-3: %+v", none)
	}
}

func TestScorecardFactsAreParameterised(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewScorecardFacts(pgtest.NewPool(t))
	hostile := "S-1' OR '1'='1"
	if err := repo.Record(ctx, scFacts("S-1", "shp-1", t0)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Counts(ctx, hostile, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || got.ShipmentsReceived != 0 {
		t.Fatalf("a hostile seller id matched rows: %+v %v", got, err)
	}
	// A code with quotes is stored verbatim.
	f := scorecard.Facts{Occurrences: []scorecard.Occurrence{{SellerID: "S-1", ShipmentID: "shp-1", Type: scorecard.Violation, Code: "x'); DROP TABLE sellers; --", At: t0}}}
	if err := repo.Record(ctx, f); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Counts(ctx, "S-1", t0.Add(-time.Hour), t0.Add(time.Hour)); err != nil || got.Violations != 2 {
		t.Fatalf("%+v %v", got, err)
	}
}

// TestScorecardFactsJoinTheUnitOfWork: the facts commit or roll back with the
// outbox rows of the same receipt.
func TestScorecardFactsJoinTheUnitOfWork(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	uow := postgres.NewUnitOfWork(pool)
	repo := postgres.NewScorecardFacts(pool)
	boom := errors.New("boom")
	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := repo.Record(ctx, scFacts("S-1", "shp-1", t0)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if got, _ := repo.Counts(ctx, "S-1", t0.Add(-time.Hour), t0.Add(time.Hour)); got.ShipmentsReceived != 0 || got.Violations != 0 {
		t.Fatalf("a rolled-back unit left facts: %+v", got)
	}
	if err := uow.Do(ctx, func(ctx context.Context) error { return repo.Record(ctx, scFacts("S-1", "shp-1", t0)) }); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Counts(ctx, "S-1", t0.Add(-time.Hour), t0.Add(time.Hour)); got.ShipmentsReceived != 1 {
		t.Fatalf("a committed unit lost its facts: %+v", got)
	}
	var _ ports.ScorecardFacts = repo
}

// TestScorecardBackfillFromThePublishedEvents proves migration 0002 gives an
// existing deployment the scorecard it would have had: the receipt, discrepancy
// and violation events already in the outbox (written by the real encoder) are
// restated as facts, and the result equals the facts recorded live.
func TestScorecardBackfillFromThePublishedEvents(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	hdr := shared.Header{Version: 2, At: time.Now().UTC()}
	events := []shared.Event{
		inboundshipment.InboundDiscrepancyRecorded{Header: hdr, ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", SSCC: "x", Kind: "Short"},
		inboundshipment.InboundDiscrepancyRecorded{Header: hdr, ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", Kind: "MissingCarton"},
		inboundshipment.SellerComplianceViolationRecorded{Header: hdr, SellerID: "S-1", ShipmentID: "shp-1", Code: "LABEL_TORN"},
		inboundshipment.InboundShipmentReceived{Header: hdr, ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", Status: inboundshipment.ClosedWithDiscrepancy,
			ReceivedAt: hdr.At, Cartons: []inboundshipment.ReceivedCarton{
				{SSCC: "a", Status: inboundshipment.StatusReceived}, {SSCC: "b", Status: inboundshipment.StatusMissing}, {SSCC: "c", Status: inboundshipment.StatusUnexpected}}},
		// another seller's closing, and an event that is no scorecard fact
		inboundshipment.InboundShipmentReceived{Header: hdr, ShipmentID: "shp-9", SellerID: "S-9", NodeID: "n-1", Status: inboundshipment.Closed,
			ReceivedAt: hdr.At, Cartons: []inboundshipment.ReceivedCarton{{SSCC: "z", Status: inboundshipment.StatusReceived}}},
		inboundshipment.InboundShipmentShipped{Header: hdr, ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1"},
	}
	ob := postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), kafka.NewEncoder())
	if err := ob.Append(ctx, events...); err != nil {
		t.Fatal(err)
	}
	// Return to the state before the migration ran: no scorecard tables.
	if _, err := pool.Exec(ctx, `DROP TABLE seller_scorecard_occurrences; DROP TABLE seller_scorecard_receipts`); err != nil {
		t.Fatal(err)
	}
	up, err := os.ReadFile("migrations/0002_seller_scorecard.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("migration 0002 on a database that already published events: %v", err)
	}
	repo := postgres.NewScorecardFacts(pool)
	since, until := hdr.At.Add(-time.Hour), time.Now().Add(time.Hour)
	got, err := repo.Counts(ctx, "S-1", since, until)
	if err != nil {
		t.Fatal(err)
	}
	// The same facts recorded live: the oracle of the backfill.
	live := postgres.NewScorecardFacts(pgtest.NewPool(t))
	if err := live.Record(ctx, scorecard.FactsFrom(events)); err != nil {
		t.Fatal(err)
	}
	want, err := live.Counts(ctx, "S-1", since, until)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || got.ShipmentsReceived != 1 || got.CartonsReported != 3 || got.CartonsReceived != 1 || got.Violations != 1 || got.Discrepancies["Short"] != 1 {
		t.Fatalf("backfill = %+v, live = %+v", got, want)
	}
	if other, _ := repo.Counts(ctx, "S-9", since, until); other.ShipmentsReceived != 1 || other.CartonsReceived != 1 {
		t.Fatalf("S-9: %+v", other)
	}
}
