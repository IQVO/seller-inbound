//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/postgres"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
	"github.com/claudioed/seller-inbound/internal/testing/pgtest"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func mustSSCC(t *testing.T, n string) string {
	t.Helper()
	s, err := gs1.NewSSCC("0061414100000" + n)
	if err != nil {
		t.Fatal(err)
	}
	return s.String()
}

func mustGTIN(t *testing.T) string {
	t.Helper()
	g, err := gs1.NewGTIN("0614141000012")
	if err != nil {
		t.Fatal(err)
	}
	return g.String()
}

func TestSellerRepoRoundTripAndVersioning(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewSellerRepo(pgtest.NewPool(t))
	a, _, err := seller.Onboard(seller.NewAccount{ID: "S-1", Name: "Acme"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, a); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("duplicate create: %v", err)
	}
	loaded, _ := repo.Get(ctx, "S-1")
	stale, _ := repo.Get(ctx, "S-1")
	_, _ = loaded.Suspend("late", t0.Add(time.Hour))
	if err := repo.Save(ctx, loaded); err != nil {
		t.Fatal(err)
	}
	_, _ = stale.Suspend("late", t0.Add(time.Hour))
	if err := repo.Save(ctx, stale); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("stale save: %v", err)
	}
	got, err := repo.Get(ctx, "S-1")
	if err != nil || got.State() != seller.Suspended || got.Version() != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestPlanRepoRoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewPlanRepo(pgtest.NewPool(t))
	p, err := inboundplan.Create(inboundplan.NewPlan{ID: "pln-1", SellerID: "S-1", DestinationID: "n-1", Packing: inboundplan.CasePacked,
		Lines: []inboundplan.Line{{CatalogItemID: "item-1", GTIN: mustGTIN(t), QtyEaches: 120}}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	_, _ = p.Confirm(t0.Add(time.Hour))
	if err := repo.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "pln-1")
	if err != nil || got.State() != inboundplan.Confirmed || got.Lines()[0].QtyEaches != 120 || got.Version() != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	if err := repo.Save(ctx, p); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("saving the same version again: %v", err)
	}
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
}

// newShipment saves an empty DRAFT shipment (version 1) and returns it with one
// carton added in memory (version 2), ready to be saved.
func newShipment(t *testing.T, repo *postgres.ShipmentRepo, id, sscc string) *inboundshipment.Shipment {
	t.Helper()
	s, err := inboundshipment.Create(inboundshipment.NewShipment{ID: inboundshipment.ID(id), PlanID: "pln-1", SellerID: "S-1", NodeID: "n-1"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	rules := inboundshipment.CartonRules{PlanRules: inboundshipment.PlanRules{PlanQty: map[string]int{"item-1": 100}, Packing: inboundplan.Individual},
		CaseUnits: func(string, string) (int, bool) { return 0, false }}
	_, err = s.AddCarton(inboundshipment.NewCarton{SSCC: sscc, Contents: []inboundshipment.Content{{CatalogItemID: "item-1", GTIN: mustGTIN(t), QtyEaches: 5, Lot: "L1", Expiry: "2027-01-31"}},
		GrossWeightG: 1000, Dims: inboundshipment.Dims{L: 10, W: 10, H: 10}}, rules)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestShipmentRepoRoundTripRegistryAndConcurrency(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewShipmentRepo(pgtest.NewPool(t))
	a := newShipment(t, repo, "shp-1", mustSSCC(t, "0001"))
	if err := repo.Save(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "shp-1")
	if err != nil || len(got.Cartons()) != 1 || got.Cartons()[0].Contents[0].Lot != "L1" || got.Cartons()[0].Dims.L != 10 {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestShipmentRepoRefusesATakenSsccAndKeepsLabelledState(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewShipmentRepo(pgtest.NewPool(t))
	got := newShipment(t, repo, "shp-1", mustSSCC(t, "0001"))
	if err := repo.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	// another shipment cannot register an SSCC that is taken
	b := newShipment(t, repo, "shp-2", mustSSCC(t, "0001"))
	if err := repo.Save(ctx, b); !errors.Is(err, ports.ErrSsccTaken) {
		t.Fatalf("taken sscc: %v", err)
	}
	if stored, err := repo.Get(ctx, "shp-2"); err != nil || len(stored.Cartons()) != 0 {
		t.Fatalf("a refused save must store nothing: %v", err)
	}
	// the owner may save its own SSCCs again (an update)
	if _, err := got.Label(inboundshipment.PlanRules{}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := repo.ListByPlan(ctx, "pln-1")
	if err != nil || len(list) != 2 || list[0].State() != inboundshipment.Labelled || list[1].State() != inboundshipment.Draft {
		t.Fatalf("%v %+v", err, list)
	}
}

func TestShipmentRepoRejectsStaleWriters(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewShipmentRepo(pgtest.NewPool(t))
	first := newShipment(t, repo, "shp-1", mustSSCC(t, "0001"))
	if err := repo.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	first, err := repo.Get(ctx, "shp-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Label(inboundshipment.PlanRules{}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	stale, _ := repo.Get(ctx, "shp-1")
	fresh, _ := repo.Get(ctx, "shp-1")
	_, _ = fresh.Ship(t0.Add(2 * time.Hour))
	_, _ = stale.Ship(t0.Add(2 * time.Hour))
	if err := repo.Save(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, stale); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("stale: %v", err)
	}
	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestShipmentSsccRegistryHasExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewShipmentRepo(pgtest.NewPool(t))
	const writers = 6
	shipments := make([]*inboundshipment.Shipment, writers)
	for i := range shipments {
		shipments[i] = newShipment(t, repo, "shp-"+string(rune('a'+i)), mustSSCC(t, "0042"))
	}
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for _, s := range shipments {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- repo.Save(ctx, s)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ports.ErrSsccTaken) && !errors.Is(err, ports.ErrConcurrentUpdate) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d shipments claimed one SSCC, want exactly 1", wins)
	}
}

func TestViewReposAndInbox(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	nodes := postgres.NewNodeViewRepo(pool)
	yes := true
	n := network.Node{ID: "n-1", Type: network.TypeFulfillmentCenter, Status: network.NodeActive, ProfileRevision: 3,
		Limits: &network.InboundLimits{MaxCartonWeightG: 2000, MaxCartonDims: &network.Dims{L: 1, W: 2, H: 3}, AcceptsPallets: &yes}}
	if err := nodes.Save(ctx, n); err != nil {
		t.Fatal(err)
	}
	got, err := nodes.Get(ctx, "n-1")
	if err != nil || got.Limits == nil || got.Limits.MaxCartonDims.H != 3 || !*got.Limits.AcceptsPallets || got.ProfileRevision != 3 {
		t.Fatalf("%v %+v", err, got)
	}
	n.Limits = nil
	_ = nodes.Save(ctx, n)
	if got, _ = nodes.Get(ctx, "n-1"); got.Limits != nil {
		t.Fatal("limits must clear")
	}
	if _, err := nodes.Get(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}

}

func TestPackagingViewRepoAndInbox(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	pk := postgres.NewPackagingViewRepo(pool)
	row := network.PackLevel{CatalogItemID: "item-1", Level: network.Case, GTIN: "00614141000036", ChildGTIN: "00614141000012", UnitsPerPack: 12, Active: true, DeclaredAt: t0}
	_ = pk.Save(ctx, row)
	row.Active, row.RetiredAt = false, t0.Add(time.Hour)
	_ = pk.Save(ctx, row)
	r, err := pk.Get(ctx, "item-1", network.Case, "00614141000036")
	if err != nil || r.Active || r.RetiredAt.IsZero() || r.UnitsPerPack != 12 {
		t.Fatalf("%v %+v", err, r)
	}
	if rows, _ := pk.ListByItem(ctx, "item-1"); len(rows) != 1 {
		t.Fatal(rows)
	}
	if _, err := pk.Get(ctx, "item-1", network.Each, "00614141000012"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}

	inbox := postgres.NewProcessedEventRepo(pool)
	if ok, err := inbox.Claim(ctx, "e1", "X", t0); err != nil || !ok {
		t.Fatal(err)
	}
	if ok, err := inbox.Claim(ctx, "e1", "X", t0); err != nil || ok {
		t.Fatalf("second claim: %v %v", ok, err)
	}
}

func TestSsccSequenceIsMonotonic(t *testing.T) {
	ctx := context.Background()
	seq := postgres.NewSsccSequence(pgtest.NewPool(t))
	a, err := seq.Next(ctx)
	b, _ := seq.Next(ctx)
	if err != nil || a != 1 || b != 2 {
		t.Fatalf("%d %d %v", a, b, err)
	}
}

func TestUnitOfWorkRollsBackAggregateOutboxAndClaimTogether(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	uow := postgres.NewUnitOfWork(pool)
	sellers := postgres.NewSellerRepo(pool)
	inbox := postgres.NewProcessedEventRepo(pool)
	ob := postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), kafka.NewEncoder())
	boom := errors.New("boom")
	err := uow.Do(ctx, func(ctx context.Context) error {
		a, evs, _ := seller.Onboard(seller.NewAccount{ID: "S-9", Name: "Nine"}, t0)
		if err := sellers.Save(ctx, a); err != nil {
			return err
		}
		if err := ob.Append(ctx, evs...); err != nil {
			return err
		}
		if _, err := inbox.Claim(ctx, "evt-1", "X", t0); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := sellers.Get(ctx, "S-9"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("the seller must be rolled back: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM outbox_events) + (SELECT count(*) FROM processed_events)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("outbox and inbox must roll back too: %d %v", n, err)
	}
	if ok, _ := inbox.Claim(ctx, "evt-1", "X", t0); !ok {
		t.Fatal("the claim must have been rolled back")
	}
}

func TestOutboxEventsAreCloudEventsOnTheRightTopic(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	ob := postgres.NewEventOutbox(postgres.NewOutboxRepo(pool), kafka.NewEncoder())
	_, evs, err := seller.Onboard(seller.NewAccount{ID: "S-1", Name: "Acme"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := ob.Append(ctx, evs...); err != nil {
		t.Fatal(err)
	}
	var topic, typ, subject, key string
	if err := pool.QueryRow(ctx, `SELECT topic, event_type, subject, convert_from(key, 'UTF8') FROM outbox_events`).Scan(&topic, &typ, &subject, &key); err != nil {
		t.Fatal(err)
	}
	if topic != kafka.TopicAnalytics || typ != "com.retailnetwork.partner.seller-inbound.seller.SellerOnboarded" || subject != "S-1" || key != "S-1" {
		t.Fatalf("row = %s %s %s %s", topic, typ, subject, key)
	}
}

func TestMigrationsRunTwice(t *testing.T) {
	url := pgtest.URL(t)
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("migrations are idempotent: %v", err)
	}
}
