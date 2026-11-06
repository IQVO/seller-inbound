//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/postgres"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
	"github.com/claudioed/seller-inbound/internal/testing/pgtest"
)

func vendorFC(id, region string) network.Node {
	n, err := network.Node{}.Registered(id, network.TypeFulfillmentCenter, network.OperatorVendor, region)
	if err != nil {
		panic(err)
	}
	return n.Activated(id)
}

// TestNodeViewKeepsOperatorAndRegion: the node row round-trips the operator
// and region NodeRegistered declared, and VendorFulfillmentCenters selects
// exactly the ACTIVE vendor fulfillment centers.
func TestNodeViewKeepsOperatorAndRegion(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	nodes := postgres.NewNodeViewRepo(pool)
	for _, n := range []network.Node{
		vendorFC("fc-a", "reg-east"),
		vendorFC("fc-b", "reg-east"),
		vendorFC("fc-susp", "reg-west"),
		{ID: "fc-n", Type: network.TypeFulfillmentCenter, Status: network.NodeActive, Operator: network.OperatorNetwork, RegionID: "reg-east"},
	} {
		if "fc-susp" == n.ID {
			n = n.Suspended(n.ID)
		}
		if err := nodes.Save(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := nodes.Get(ctx, "fc-a")
	if err != nil || got.Operator != network.OperatorVendor || got.RegionID != "reg-east" {
		t.Fatalf("get = %v %+v", err, got)
	}
	candidates, err := nodes.VendorFulfillmentCenters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].ID != "fc-a" || candidates[1].ID != "fc-b" {
		t.Fatalf("candidates = %+v", candidates)
	}
}

// TestDemandHeatAndListingRepos: releases upsert by group (a re-release
// replaces), cancels remove, the window and the region join bound the heat,
// and listing availability upserts per (item, node).
func TestDemandHeatAndListingRepos(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	nodes := postgres.NewNodeViewRepo(pool)
	heat := postgres.NewDemandHeatRepo(pool)
	listings := postgres.NewListingRepo(pool)
	if err := nodes.Save(ctx, vendorFC("fc-a", "reg-east")); err != nil {
		t.Fatal(err)
	}
	if err := nodes.Save(ctx, vendorFC("fc-b", "reg-west")); err != nil {
		t.Fatal(err)
	}
	rel := network.DemandRelease{AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-a", ReleasedAt: t0.Add(-24 * time.Hour),
		Lines: []network.ReleaseLine{{LineNo: 1, CatalogItemID: "itm-1", Quantity: 100}, {LineNo: 2, CatalogItemID: "itm-1", Quantity: 40}}}
	if err := heat.RecordDemand(ctx, rel); err != nil {
		t.Fatal(err)
	}
	if err := heat.RecordDemand(ctx, network.DemandRelease{AssignmentID: "asg-2", GroupNo: 1, NodeID: "fc-b", ReleasedAt: t0.Add(-48 * time.Hour),
		Lines: []network.ReleaseLine{{LineNo: 1, CatalogItemID: "itm-1", Quantity: 7}}}); err != nil {
		t.Fatal(err)
	}
	// A release older than the window: invisible to the heat.
	if err := heat.RecordDemand(ctx, network.DemandRelease{AssignmentID: "asg-old", GroupNo: 1, NodeID: "fc-a", ReleasedAt: t0.Add(-30 * 24 * time.Hour),
		Lines: []network.ReleaseLine{{LineNo: 1, CatalogItemID: "itm-1", Quantity: 500}}}); err != nil {
		t.Fatal(err)
	}
	got, err := heat.RegionHeat(ctx, "itm-1", t0.Add(-28*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("heat = %+v", got)
	}
	if got[0].RegionID != "reg-east" || got[0].DemandUnits != 140 || !got[0].LastReleasedAt.Equal(t0.Add(-24*time.Hour)) {
		t.Fatalf("east = %+v", got[0])
	}
	if got[1].RegionID != "reg-west" || got[1].DemandUnits != 7 {
		t.Fatalf("west = %+v", got[1])
	}
	// A re-release of asg-1 to fc-b replaces its lines (reassignment moves
	// the demand: reg-east empties, reg-west gains the 20 units).
	if err := heat.RecordDemand(ctx, network.DemandRelease{AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-b", ReleasedAt: t0.Add(-time.Hour),
		Lines: []network.ReleaseLine{{LineNo: 1, CatalogItemID: "itm-1", Quantity: 20}}}); err != nil {
		t.Fatal(err)
	}
	got, _ = heat.RegionHeat(ctx, "itm-1", t0.Add(-28*24*time.Hour))
	if len(got) != 1 || got[0].RegionID != "reg-west" || got[0].DemandUnits != 27 {
		t.Fatalf("heat after reassignment = %+v", got)
	}
	// A cancel removes the group's demand; an unknown cancel is a no-op.
	if err := heat.CancelDemand(ctx, "asg-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := heat.CancelDemand(ctx, "asg-none", 9); err != nil {
		t.Fatal(err)
	}
	got, _ = heat.RegionHeat(ctx, "itm-1", t0.Add(-28*24*time.Hour))
	if len(got) != 1 || got[0].RegionID != "reg-west" {
		t.Fatalf("heat after cancel = %+v", got)
	}

	// Listing availability: upsert per (item, node), newest wins.
	row := network.NodeAvailability{ListingID: "lst-1", CatalogItemID: "itm-1", NodeID: "fc-a", AvailableQty: 30, AsOf: t0}
	if err := listings.SaveAvailability(ctx, row); err != nil {
		t.Fatal(err)
	}
	row.AvailableQty, row.ListingID, row.AsOf = 25, "lst-1", t0.Add(time.Minute)
	if err := listings.SaveAvailability(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := listings.SaveAvailability(ctx, network.NodeAvailability{ListingID: "lst-2", CatalogItemID: "itm-1", NodeID: "fc-b", AvailableQty: 20, AsOf: t0}); err != nil {
		t.Fatal(err)
	}
	avail, err := listings.Availability(ctx, "itm-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(avail) != 2 || avail[0].NodeID != "fc-a" || avail[0].AvailableQty != 25 || avail[1].NodeID != "fc-b" || avail[1].AvailableQty != 20 {
		t.Fatalf("availability = %+v", avail)
	}
}

// TestPlacementPlanRepoRoundTrip: every persisted field survives the round
// trip (Snapshot struct, builder, Restore and the JSONB suggestions), the
// optimistic-concurrency rule holds, LatestOf answers the newest plan and
// List pages newest first.
func TestPlacementPlanRepoRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	repo := postgres.NewPlacementPlanRepo(pool)

	mk := func(id, createdAt string, cover int) *placementplan.Plan {
		p, _, err := placementplan.Compute(placementplan.NewPlan{
			ID: placementplan.ID(id), SellerID: "S-1", CatalogItemID: "itm-1", CoverDays: cover, WindowDays: 28,
			Suggestions: []placementplan.Suggestion{
				{NodeID: "fc-a", RegionID: "reg-east", SuggestedUnits: 12,
					Evidence: placementplan.Evidence{RegionDemandUnits: 140, NodeAvailabilityUnits: 30, CoverDays: cover}},
				{NodeID: "fc-b", RegionID: "reg-east", SuggestedUnits: 8,
					Evidence: placementplan.Evidence{RegionDemandUnits: 140, NodeAvailabilityUnits: 20, CoverDays: cover}},
			},
		}, time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC).Add(0))
		if err != nil {
			t.Fatal(err)
		}
		_ = createdAt
		return p
	}
	first := mk("plp-1", "", 14)
	if err := repo.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "plp-1")
	if err != nil {
		t.Fatal(err)
	}
	sn, have := first.Snapshot(), got.Snapshot()
	if have.SellerID != sn.SellerID || have.CatalogItemID != sn.CatalogItemID || have.CoverDays != sn.CoverDays ||
		have.WindowDays != sn.WindowDays || have.Basis != placementplan.Basis || have.State != placementplan.Suggested ||
		len(have.Suggestions) != 2 || have.Suggestions[1].SuggestedUnits != 8 ||
		have.Suggestions[1].Evidence.RegionDemandUnits != 140 || have.Suggestions[1].Evidence.NodeAvailabilityUnits != 20 ||
		have.Suggestions[1].Evidence.CoverDays != 14 || have.Version != 1 {
		t.Fatalf("round trip lost a persisted field:\nwant %+v\nhave %+v", sn, have)
	}
	// Accept, supersede and an EMPTY plan: every state column round-trips.
	if err := got.Accept(t0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	accepted, _ := repo.Get(ctx, "plp-1")
	if accepted.State() != placementplan.Accepted || accepted.Snapshot().AcceptedAt.IsZero() {
		t.Fatalf("accepted = %+v", accepted.Snapshot())
	}
	empty := mk("plp-2", "", 14)
	emptySnap := empty.Snapshot()
	emptySnap.Suggestions = nil
	emptyRestored, err := placementplan.Restore(emptySnap)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, emptyRestored); err != nil {
		t.Fatal(err)
	}
	emptyGot, _ := repo.Get(ctx, "plp-2")
	if len(emptyGot.Suggestions()) != 0 {
		t.Fatalf("empty plan suggestions = %+v", emptyGot.Suggestions())
	}
	// Stale save loses the race.
	stale := mk("plp-1", "", 14)
	if err := repo.Save(ctx, stale); !errors.Is(err, ports.ErrConcurrentUpdate) {
		t.Fatalf("stale save = %v", err)
	}
	// LatestOf and the keyset list.
	if latest, err := repo.LatestOf(ctx, "S-1", "itm-1"); err != nil || latest.ID() != "plp-2" {
		t.Fatalf("latest = %v %+v", err, latest)
	}
	if _, err := repo.LatestOf(ctx, "S-9", "itm-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown seller latest = %v", err)
	}
	page, err := repo.List(ctx, "S-1", "", time.Time{}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID() != "plp-2" {
		t.Fatalf("list = %+v", page)
	}
	next, err := repo.List(ctx, "S-1", "", page[0].Snapshot().CreatedAt, string(page[0].ID()), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].ID() != "plp-1" {
		t.Fatalf("next page = %+v", next)
	}
	filtered, err := repo.List(ctx, "S-1", "itm-9", time.Time{}, "", 10)
	if err != nil || len(filtered) != 0 {
		t.Fatalf("filtered = %v %+v", err, filtered)
	}
	if _, err := repo.Get(ctx, "plp-nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("get unknown = %v", err)
	}
}
