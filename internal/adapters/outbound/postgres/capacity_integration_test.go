//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/postgres"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/network"
)

// TestPlanRepoCarriesTheCapacityFacts round-trips a NETWORK_SPLIT plan and a
// legacy plan through real Postgres: every ADR 0015 column survives (four
// places: struct, New, Restore, Snapshot builder).
func TestPlanRepoCarriesTheCapacityFacts(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewPlanRepo(pgtest.NewPool(t))
	p, err := inboundplan.Create(inboundplan.NewPlan{ID: "pln-cap", SellerID: "S-1", DestinationID: "ixd-1", Packing: inboundplan.Individual,
		Lines:           []inboundplan.Line{{CatalogItemID: "item-1", GTIN: mustGTIN(t), QtyEaches: 80, UnitVolumeDm3: 2}},
		PlacementOption: inboundplan.NetworkSplit,
		Redistribution: []inboundplan.RedistributionLine{
			{NodeID: "fc-a", LineNo: 1, QtyEaches: 20}, {NodeID: "fc-b", LineNo: 1, QtyEaches: 60}},
		SplitLimitKnown:   true,
		PlacementFeeBasis: &inboundplan.FeeBasis{Basis: "NETWORK_SPLIT", Month: "2026-10", Dm3: 160, NodeIDs: []string{"fc-a", "fc-b"}},
		CapacityEnforced:  true}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "pln-cap")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlacementOption() != inboundplan.NetworkSplit || !got.SplitLimitKnown() || !got.CapacityEnforced() ||
		got.VolumeDm3() != 160 || len(got.Redistribution()) != 2 || got.PlacementFeeBasis().Dm3 != 160 {
		t.Fatalf("placement facts lost: %+v", got.Snapshot())
	}
	if got.Lines()[0].UnitVolumeDm3 != 2 {
		t.Fatalf("unit volume lost: %+v", got.Lines())
	}
	// a legacy plan round-trips on the defaulted columns
	legacy, err := inboundplan.Create(inboundplan.NewPlan{ID: "pln-old", SellerID: "S-1", DestinationID: "n-1", Packing: inboundplan.CasePacked,
		Lines: []inboundplan.Line{{CatalogItemID: "item-1", GTIN: mustGTIN(t), QtyEaches: 120}}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	old, err := repo.Get(ctx, "pln-old")
	if err != nil || old.PlacementOption() != inboundplan.SellerSplit || old.CapacityEnforced() || old.VolumeDm3() != 0 {
		t.Fatalf("legacy plan drifted: %v %+v", err, old.Snapshot())
	}
}

// TestCapacityRepoReserveReleaseAllocations exercises the reservation table
// against real Postgres: unseen keys read 0, Reserve adds, Release subtracts
// without going negative, Allocations lists by node id.
func TestCapacityRepoReserveReleaseAllocations(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewCapacityRepo(pgtest.NewPool(t))
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	if r, err := repo.Reserved(ctx, "S-1", "2026-11", "n-1"); err != nil || r != 0 {
		t.Fatalf("unseen key: %d %v", r, err)
	}
	if err := repo.Reserve(ctx, "S-1", "2026-11", "n-1", 300, at); err != nil {
		t.Fatal(err)
	}
	if err := repo.Reserve(ctx, "S-1", "2026-11", "n-1", 200, at); err != nil {
		t.Fatal(err)
	}
	if err := repo.Reserve(ctx, "S-1", "2026-11", "n-2", 50, at); err != nil {
		t.Fatal(err)
	}
	if r, err := repo.Reserved(ctx, "S-1", "2026-11", "n-1"); err != nil || r != 500 {
		t.Fatalf("reserved = %d %v", r, err)
	}
	if r, err := repo.Reserved(ctx, "S-1", "2026-12", "n-1"); err != nil || r != 0 {
		t.Fatalf("next month = %d %v", r, err) // the month is part of the key
	}
	rows, err := repo.Allocations(ctx, "S-1", "2026-11")
	if err != nil || len(rows) != 2 || rows[0].NodeID != "n-1" || rows[0].ReservedDm3 != 500 || rows[1].NodeID != "n-2" {
		t.Fatalf("allocations = %+v %v", rows, err)
	}
	if err := repo.Release(ctx, "S-1", "2026-11", "n-1", 600); err != nil { // more than held: clamps at 0
		t.Fatal(err)
	}
	if r, err := repo.Reserved(ctx, "S-1", "2026-11", "n-1"); err != nil || r != 0 {
		t.Fatalf("after release = %d %v", r, err)
	}
}

// TestNodeViewCarriesMonthlyVolume proves the consumed monthly volumetric
// limit survives its column (and only it: the carton limits stay in JSONB).
func TestNodeViewCarriesMonthlyVolume(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewNodeViewRepo(pgtest.NewPool(t))
	withVolume := network.Node{ID: "fc-vol", Type: network.TypeFulfillmentCenter, Status: network.NodeActive, RegionID: "reg-1",
		Limits: &network.InboundLimits{MaxCartonWeightG: 25000, MonthlyVolumeDm3: 4000}}
	if err := repo.Save(ctx, withVolume); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, "fc-vol")
	if err != nil || got.Limits == nil || got.Limits.MonthlyVolumeDm3 != 4000 || got.Limits.MaxCartonWeightG != 25000 {
		t.Fatalf("node = %+v %v", got, err)
	}
	region, err := repo.RegionFulfillmentCenters(ctx, "reg-1")
	if err != nil || len(region) != 1 || region[0].Limits.MonthlyVolumeDm3 != 4000 {
		t.Fatalf("region FCs = %+v %v", region, err)
	}
	if err := repo.Save(ctx, network.Node{ID: "fc-quiet", Type: network.TypeFulfillmentCenter, Status: network.NodeActive,
		RegionID: "reg-2", Limits: &network.InboundLimits{MaxCartonWeightG: 1000}}); err != nil {
		t.Fatal(err)
	}
	quiet, err := repo.Get(ctx, "fc-quiet")
	if err != nil || quiet.Limits.MonthlyVolumeDm3 != 0 {
		t.Fatalf("undeclared volume must read 0: %+v %v", quiet, err)
	}
	other, err := repo.RegionFulfillmentCenters(ctx, "reg-2")
	if err != nil || len(other) != 1 || other[0].ID != "fc-quiet" {
		t.Fatalf("region FCs = %+v %v", other, err)
	}
}
