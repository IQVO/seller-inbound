package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// seedPlacement builds the ADR 0012 worked example: region reg-east has two
// ACTIVE vendor fulfillment centers fc-a and fc-b (and one NETWORK-operated
// FC fc-n the split must ignore), item-1 locked 140 units at fc-a inside the
// 28-day window, and the listings declared fc-a 30 and fc-b 20 available.
func seedPlacement(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	for _, n := range []struct{ id, operator, region string }{
		{"fc-a", network.OperatorVendor, "reg-east"},
		{"fc-b", network.OperatorVendor, "reg-east"},
		{"fc-n", network.OperatorNetwork, "reg-east"},
		{"fc-w", network.OperatorVendor, "reg-west"},
	} {
		if err := e.svc.ApplyNodeRegistered(ctx, "ev-n-"+n.id, requests.NodeRegistered{
			NodeID: n.id, NodeType: network.TypeFulfillmentCenter, Operator: n.operator, RegionID: n.region,
		}); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.ApplyNodeActivated(ctx, "ev-na-"+n.id, requests.NodeLifecycle{NodeID: n.id}); err != nil {
			t.Fatal(err)
		}
	}
	release(t, e, "asg-1", 1, "fc-a", "item-1", 140, t0.Add(-24*time.Hour))
	availability(t, e, "lst-a", "item-1", "fc-a", 30, t0.Add(-time.Hour))
	availability(t, e, "lst-b", "item-1", "fc-b", 20, t0.Add(-time.Hour))
	if _, err := e.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "S-1", Name: "Acme Goods"}); err != nil {
		t.Fatal(err)
	}
}

func release(t *testing.T, e *env, assignment string, group int, node, item string, qty int, at time.Time) {
	t.Helper()
	err := e.svc.ApplyFulfillmentReleased(context.Background(), "ev-rel-"+assignment+"-"+at.Format("150405.000000000"),
		requests.FulfillmentReleased{AssignmentID: assignment, GroupNo: group, NodeID: node, ReleasedAt: at,
			Lines: []requests.ReleaseLine{{LineNo: 1, CatalogItemID: item, Quantity: qty}}})
	if err != nil {
		t.Fatal(err)
	}
}

var availabilitySeq int

func availability(t *testing.T, e *env, listing, item, node string, qty int, at time.Time) {
	t.Helper()
	availabilitySeq++
	err := e.svc.ApplyListingAvailabilityChanged(context.Background(),
		"ev-av-"+listing+"-"+time.Duration(availabilitySeq).String(),
		requests.ListingAvailabilityChanged{ListingID: listing, CatalogItemID: item, NodeID: node,
			AvailableQty: qty, AsOf: at})
	if err != nil {
		t.Fatal(err)
	}
}

// TestComputePlacementPlanFormulaOnTheWorkedExample pins the ADR 0012 worked
// example: demand 140 over 28 days, cover 14 -> ceil(140*14/28) = 70; the
// region declares 30+20 = 50 -> target 20, split proportional to 30:20 ->
// fc-a 12, fc-b 8.
func TestComputePlacementPlanFormulaOnTheWorkedExample(t *testing.T) {
	e := newEnv(t)
	seedPlacement(t, e)
	ctx := context.Background()

	got, err := e.svc.ComputePlacementPlan(ctx, requests.ComputePlacementPlan{SellerID: "S-1", CatalogItemID: "item-1", CoverDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	checkWorkedExamplePlan(t, got)
	checkWorkedExampleEvent(t, e)
}

func checkWorkedExamplePlan(t *testing.T, got views.PlacementPlan) {
	t.Helper()
	if got.State != "SUGGESTED" || got.Basis != placementplan.Basis || got.CoverDays != 14 || got.WindowDays != 28 {
		t.Fatalf("plan header = %+v", got)
	}
	if len(got.Suggestions) != 2 {
		t.Fatalf("suggestions = %+v", got.Suggestions)
	}
	want := []views.PlacementSuggestion{
		{NodeID: "fc-a", RegionID: "reg-east", SuggestedUnits: 12, RegionDemandUnits: 140, NodeAvailabilityUnits: 30, CoverDays: 14},
		{NodeID: "fc-b", RegionID: "reg-east", SuggestedUnits: 8, RegionDemandUnits: 140, NodeAvailabilityUnits: 20, CoverDays: 14},
	}
	for i, w := range want {
		if got.Suggestions[i] != w {
			t.Fatalf("suggestion %d = %+v, want %+v", i, got.Suggestions[i], w)
		}
	}
	if got.Suggestions[0].SuggestedUnits+got.Suggestions[1].SuggestedUnits != 20 {
		t.Fatalf("the split must sum to the region target 20: %+v", got.Suggestions)
	}
}

func checkWorkedExampleEvent(t *testing.T, e *env) {
	t.Helper()
	// The compute is a command: the plan was recorded with its event (the
	// seed's SellerOnboarded is the only other event).
	evs := e.store.Outbox().Events()
	if len(evs) != 2 {
		t.Fatalf("outbox = %v", evs)
	}
	if ev, ok := evs[1].(placementplan.PlacementPlanSuggested); !ok || ev.SellerID != "S-1" || len(ev.Suggestions) != 2 {
		t.Fatalf("event = %+v", evs[1])
	}
}

// TestComputePlacementPlanDefaultCoverAndValidation: omitted coverDays means
// 14; out-of-range coverDays is ErrInvalidCoverDays.
func TestComputePlacementPlanDefaultCoverAndValidation(t *testing.T) {
	e := newEnv(t)
	seedPlacement(t, e)
	ctx := context.Background()

	got, err := e.svc.ComputePlacementPlan(ctx, requests.ComputePlacementPlan{SellerID: "S-1", CatalogItemID: "item-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.CoverDays != usecases.DefaultCoverDays {
		t.Fatalf("coverDays = %d, want the default %d", got.CoverDays, usecases.DefaultCoverDays)
	}
	for _, bad := range []int{-1, 91, 909} {
		if _, err := e.svc.ComputePlacementPlan(ctx, requests.ComputePlacementPlan{SellerID: "S-1", CatalogItemID: "item-1", CoverDays: bad}); !errors.Is(err, ports.ErrInvalidCoverDays) {
			t.Fatalf("coverDays %d: want ErrInvalidCoverDays, got %v", bad, err)
		}
	}
}

// TestComputePlacementPlanEmptyIsLegal: an item with no demand anywhere
// yields an EMPTY plan, recorded and published.
func TestComputePlacementPlanEmptyIsLegal(t *testing.T) {
	e := newEnv(t)
	seedPlacement(t, e)
	ctx := context.Background()

	got, err := e.svc.ComputePlacementPlan(ctx, requests.ComputePlacementPlan{SellerID: "S-1", CatalogItemID: "item-quiet"})
	if err != nil {
		t.Fatalf("no demand is not an error: %v", err)
	}
	if len(got.Suggestions) != 0 {
		t.Fatalf("suggestions = %+v", got.Suggestions)
	}
	if len(e.store.Outbox().Events()) != 2 {
		t.Fatal("the empty plan is still published")
	}
}

// TestComputePlacementPlanCoveredRegionAndNoCandidates: a region whose
// declared availability covers the horizon, and a region with demand but no
// vendor FC, produce no suggestion.
func TestComputePlacementPlanCoveredRegionAndNoCandidates(t *testing.T) {
	e := newEnv(t)
	seedPlacement(t, e)
	// reg-west has a vendor FC but no demand; fc-n is NETWORK-run: put 100
	// demand units on it -- the region heat counts them, but no candidate
	// exists to split across, so no suggestion may name fc-n.
	release(t, e, "asg-2", 1, "fc-n", "item-1", 100, t0.Add(-2*time.Hour))
	// Cover the east: raise availability to 70+.
	availability(t, e, "lst-a", "item-1", "fc-a", 60, t0.Add(-time.Minute))
	availability(t, e, "lst-b", "item-1", "fc-b", 60, t0.Add(-time.Minute))
	ctx := context.Background()

	got, err := e.svc.ComputePlacementPlan(ctx, requests.ComputePlacementPlan{SellerID: "S-1", CatalogItemID: "item-1", CoverDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Suggestions) != 0 {
		t.Fatalf("a covered region and a region without candidates suggest nothing: %+v", got.Suggestions)
	}
}

// TestComputePlacementPlanEvenSplitWhenNoAvailability: with no listing row at
// all the split is even (largest remainder by node id).
func TestComputePlacementPlanEvenSplitWhenNoAvailability(t *testing.T) {
	store := memory.NewStore()
	clock := memory.NewClock(t0)
	svc := usecases.New(usecases.Deps{UoW: store.UnitOfWork(), Sellers: store.Sellers(), Nodes: store.Nodes(),
		DemandHeat: store.DemandHeat(), Listings: store.Listings(), PlacementPlans: store.PlacementPlans(),
		Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: clock, IDs: &memory.SequentialIDs{}}, usecases.Config{})
	ctx := context.Background()
	for _, n := range []string{"fc-a", "fc-b", "fc-c"} {
		if err := svc.ApplyNodeRegistered(ctx, "ev-"+n, requests.NodeRegistered{NodeID: n, NodeType: network.TypeFulfillmentCenter, Operator: network.OperatorVendor, RegionID: "reg-east"}); err != nil {
			t.Fatal(err)
		}
		if err := svc.ApplyNodeActivated(ctx, "ev-act-"+n, requests.NodeLifecycle{NodeID: n}); err != nil {
			t.Fatal(err)
		}
	}
	// 8 units/day, cover 14: ceil(8*14/28)=4, no availability -> target 4.
	release(t, &env{svc: svc, store: store, clock: clock}, "asg-3", 1, "fc-a", "item-1", 8, t0.Add(-time.Hour))
	if _, err := svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "S-1", Name: "Acme"}); err != nil {
		t.Fatal(err)
	}
	got, err := svc.ComputePlacementPlan(ctx, requests.ComputePlacementPlan{SellerID: "S-1", CatalogItemID: "item-1", CoverDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	// 4/3 = 1 remainder 1: the first node (id order) takes the extra unit.
	if len(got.Suggestions) != 3 || got.Suggestions[0].SuggestedUnits != 2 ||
		got.Suggestions[1].SuggestedUnits != 1 || got.Suggestions[2].SuggestedUnits != 1 {
		t.Fatalf("even split of 4 over three nodes = 2/1/1 by node id: %+v", got.Suggestions)
	}
	for _, s := range got.Suggestions {
		if s.NodeAvailabilityUnits != 0 {
			t.Fatalf("availability evidence = %+v", s)
		}
	}
}

// TestPlacementSupersedeAcceptAndTransitions drives the lifecycle through
// REST-shaped commands.
func TestPlacementSupersedeOnEveryCompute(t *testing.T) {
	e := newEnv(t)
	seedPlacement(t, e)
	ctx := context.Background()

	first := mustCompute(t, e, ctx, "item-1")
	e.clock.Set(t0.Add(time.Minute))
	second := mustCompute(t, e, ctx, "item-1")
	if first.PlanID == second.PlanID {
		t.Fatal("every compute is a new plan")
	}
	old, err := e.svc.GetPlacementPlan(ctx, first.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if old.State != "SUPERSEDED" || old.SupersededAt.IsZero() {
		t.Fatalf("the previous plan is SUPERSEDED with supersededAt, got %+v", old)
	}
}

func TestPlacementAcceptLifecycle(t *testing.T) {
	e := newEnv(t)
	seedPlacement(t, e)
	ctx := context.Background()

	second := mustCompute(t, e, ctx, "item-1")
	accepted, err := e.svc.AcceptPlacementPlan(ctx, second.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.State != "ACCEPTED" || accepted.AcceptedAt.IsZero() {
		t.Fatalf("accepted = %+v", accepted)
	}
	if _, err := e.svc.AcceptPlacementPlan(ctx, second.PlanID); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("accepting twice: want illegal transition, got %v", err)
	}
	// A newer compute supersedes even an ACCEPTED plan.
	e.clock.Set(t0.Add(2 * time.Minute))
	third := mustCompute(t, e, ctx, "item-1")
	mid, _ := e.svc.GetPlacementPlan(ctx, second.PlanID)
	if mid.State != "SUPERSEDED" {
		t.Fatalf("an accepted plan is superseded by a newer compute, got %s", mid.State)
	}
	if _, err := e.svc.AcceptPlacementPlan(ctx, second.PlanID); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("accepting a SUPERSEDED plan: want illegal transition, got %v", err)
	}
	// The list answers newest first.
	list, err := e.svc.ListPlacementPlans(ctx, "S-1", "", time.Time{}, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].PlanID != third.PlanID || list[1].PlanID != second.PlanID {
		t.Fatalf("list = %+v", list)
	}
}

func mustCompute(t *testing.T, e *env, ctx context.Context, item string) views.PlacementPlan {
	t.Helper()
	got, err := e.svc.ComputePlacementPlan(ctx, requests.ComputePlacementPlan{SellerID: "S-1", CatalogItemID: item})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestDemandHeatWindowReReleaseAndCancel: releases outside the window do not
// count; a re-release replaces (a reassignment moves demand); a cancel
// removes it.
func TestDemandHeatWindowReReleaseAndCancel(t *testing.T) {
	e := newEnv(t)
	seedPlacement(t, e)
	ctx := context.Background()

	// Outside the 28-day window: no heat.
	release(t, e, "asg-old", 1, "fc-a", "item-1", 500, t0.Add(-29*24*time.Hour))
	heat, err := e.store.DemandHeat().RegionHeat(ctx, "item-1", t0.Add(-network.ObservationWindow))
	if err != nil {
		t.Fatal(err)
	}
	if len(heat) != 1 || heat[0].DemandUnits != 140 {
		t.Fatalf("heat = %+v", heat)
	}

	// A re-release of asg-1 to fc-b moves the demand (never doubles).
	err = e.svc.ApplyFulfillmentReleased(ctx, "ev-rel-asg-1-r2", requests.FulfillmentReleased{
		AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-b", ReleasedAt: t0.Add(-time.Hour),
		Lines: []requests.ReleaseLine{{LineNo: 1, CatalogItemID: "item-1", Quantity: 140}}})
	if err != nil {
		t.Fatal(err)
	}
	heat, _ = e.store.DemandHeat().RegionHeat(ctx, "item-1", t0.Add(-network.ObservationWindow))
	if len(heat) != 1 || heat[0].DemandUnits != 140 {
		t.Fatalf("after re-release heat = %+v", heat)
	}

	// A cancel removes the group's demand entirely.
	if err := e.svc.ApplyFulfillmentCancelled(ctx, "ev-rel-asg-1-c", requests.FulfillmentCancelled{AssignmentID: "asg-1", GroupNo: 1}); err != nil {
		t.Fatal(err)
	}
	heat, _ = e.store.DemandHeat().RegionHeat(ctx, "item-1", t0.Add(-network.ObservationWindow))
	if len(heat) != 0 {
		t.Fatalf("after cancel heat = %+v", heat)
	}
}
