package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/capacity"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
)

// volumeNode onboards S-1 (PROVISIONAL scorecard: GOOD factor) and activates a
// node with the declared monthly volume (0 = undeclared).
func volumeNode(t *testing.T, e *env, id, typ, region string, monthlyVolumeDm3 int) {
	t.Helper()
	ctx := context.Background()
	if _, err := e.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "S-1", Name: "Goods"}); err != nil &&
		!errors.Is(err, seller.ErrSellerExists) { // a shared fixture may have onboarded S-1 already
		t.Fatal(err)
	}
	if err := e.svc.ApplyNodeRegistered(ctx, "ev-reg-"+id, requests.NodeRegistered{NodeID: id, NodeType: typ, Operator: "VENDOR", RegionID: region}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyNodeProfileDeclared(ctx, "ev-prof-"+id, requests.NodeProfileDeclared{NodeID: id, ProfileRevision: 1,
		Inbound: &requests.NodeInbound{MonthlyVolumeDm3: monthlyVolumeDm3}}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyNodeActivated(ctx, "ev-act-"+id, requests.NodeLifecycle{NodeID: id}); err != nil {
		t.Fatal(err)
	}
}

// volPlan creates an INDIVIDUAL plan of one line qty x unitVolumeDm3 at node.
func volPlan(t *testing.T, e *env, node string, qty, unitVolumeDm3 int) (views.Plan, error) {
	t.Helper()
	return e.svc.CreateInboundPlan(context.Background(), requests.CreateInboundPlan{
		SellerID: "S-1", DestinationNodeID: node, PackingOption: "INDIVIDUAL",
		Lines: []requests.PlanLine{{CatalogItemID: "item-1", GTIN: gtin(t, "0614141000012"), QtyEaches: qty, UnitVolumeDm3: unitVolumeDm3}},
	})
}

// TestCapacityGateMatrix is the ADR 0015 matrix end to end through the use
// case: the gate refuses only when the node limit AND the volume are known.
func TestCapacityGateMatrix(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name             string
		monthlyVolumeDm3 int
		unitVolumeDm3    int
		wantErr          error
		wantEnforced     bool
	}{
		{"both known within", 2000, 500, nil, true},
		{"both known over", 2000, 2001, capacity.ErrInboundCapacityExceeded, true},
		{"node limit unknown", 0, 500, nil, false},
		{"volume unknown", 2000, 0, nil, false},
		{"both unknown", 0, 0, nil, false},
	} {
		e := newEnv(t)
		volumeNode(t, e, "n-cap", "FULFILLMENT_CENTER", "reg-1", c.monthlyVolumeDm3)
		v, err := volPlan(t, e, "n-cap", 1, c.unitVolumeDm3)
		if (err != nil) != (c.wantErr != nil) || (c.wantErr != nil && !errors.Is(err, c.wantErr)) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.wantErr)
		}
		if err != nil {
			continue
		}
		if v.CapacityEnforced != c.wantEnforced {
			t.Fatalf("%s: capacityEnforced = %v, want %v", c.name, v.CapacityEnforced, c.wantEnforced)
		}
		if v.PlacementOption != "SELLER_SPLIT" || len(v.Redistribution) != 0 || v.PlacementFeeBasis != nil {
			t.Fatalf("%s: legacy plan carries split facts: %+v", c.name, v)
		}
	}
	// the passed gate reserved the volume
	e := newEnv(t)
	volumeNode(t, e, "n-cap", "FULFILLMENT_CENTER", "reg-1", 2000)
	if _, err := volPlan(t, e, "n-cap", 3, 100); err != nil {
		t.Fatal(err)
	}
	allocs, err := e.svc.GetSellerCapacityAllocations(ctx, "S-1", "2026-10")
	if err != nil || len(allocs.Items) != 1 || allocs.Items[0].NodeID != "n-cap" || allocs.Items[0].ReservedDm3 != 300 {
		t.Fatalf("allocations = %+v %v", allocs, err)
	}
}

// TestCapacityBoundaryAtExactlyLimit pins the inclusive boundary: a plan that
// lands exactly on the tier-factored limit is allowed, one dm3 more is not.
func TestCapacityBoundaryAtExactlyLimit(t *testing.T) {
	e := newEnv(t)
	volumeNode(t, e, "n-cap", "FULFILLMENT_CENTER", "reg-1", 1000)
	if _, err := volPlan(t, e, "n-cap", 500, 2); err != nil { // exactly 1000 dm3, GOOD factor
		t.Fatalf("boundary plan refused: %v", err)
	}
	_, err := volPlan(t, e, "n-cap", 500, 2)
	if !errors.Is(err, capacity.ErrInboundCapacityExceeded) {
		t.Fatalf("second boundary plan err = %v", err)
	}
}

// TestCapacityTierFactor watches the seller (one compliance violation =>
// WATCH => 80% of the declared monthly dm3) and proves the factor bites.
func TestCapacityTierFactor(t *testing.T) {
	e := newEnv(t)
	volumeNode(t, e, "n-cap", "FULFILLMENT_CENTER", "reg-1", 1000)
	if err := e.store.Scorecard().Record(context.Background(), scorecard.Facts{Occurrences: []scorecard.Occurrence{
		{SellerID: "S-1", ShipmentID: "shp-x", Type: scorecard.Violation, Code: "LATE", At: t0},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := volPlan(t, e, "n-cap", 400, 2); err != nil { // 800 dm3 = WATCH limit exactly
		t.Fatalf("WATCH boundary plan refused: %v", err)
	}
	if _, err := volPlan(t, e, "n-cap", 1, 1); !errors.Is(err, capacity.ErrInboundCapacityExceeded) {
		t.Fatal("a dm3 past the WATCH factor must be refused")
	}
}

// TestCancelReleasesCapacity: cancelling a DRAFT plan gives the volume back.
func TestCancelReleasesCapacity(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	volumeNode(t, e, "n-cap", "FULFILLMENT_CENTER", "reg-1", 1000)
	v, err := volPlan(t, e, "n-cap", 500, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CancelInboundPlan(ctx, v.PlanID); err != nil {
		t.Fatal(err)
	}
	allocs, err := e.svc.GetSellerCapacityAllocations(ctx, "S-1", "2026-10")
	if err != nil || len(allocs.Items) != 1 || allocs.Items[0].ReservedDm3 != 0 {
		t.Fatalf("allocations after cancel = %+v %v", allocs, err)
	}
	if _, err := volPlan(t, e, "n-cap", 500, 2); err != nil { // the released capacity is bookable again
		t.Fatalf("released capacity not bookable: %v", err)
	}
}

// TestCapacityMonthsAreKeyedSeparately: a reservation lives in the calendar
// month of the plan's creation; December does not see November's reservation.
func TestCapacityMonthsAreKeyedSeparately(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	volumeNode(t, e, "n-cap", "FULFILLMENT_CENTER", "reg-1", 1000)
	if _, err := volPlan(t, e, "n-cap", 500, 2); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(time.Date(2026, 12, 1, 0, 30, 0, 0, time.UTC))
	if _, err := volPlan(t, e, "n-cap", 500, 2); err != nil { // same volume, a later month: no reservation there
		t.Fatalf("month rollover blocked the plan: %v", err)
	}
	oct, err := e.svc.GetSellerCapacityAllocations(ctx, "S-1", "2026-10")
	if err != nil || len(oct.Items) != 1 || oct.Items[0].ReservedDm3 != 1000 {
		t.Fatalf("october = %+v %v", oct, err)
	}
	all, err := e.svc.GetSellerCapacityAllocations(ctx, "S-1", "") // defaults to the current month
	if err != nil || all.Month != "2026-12" {
		t.Fatalf("default month = %+v %v", all, err)
	}
}

// TestCapacityAllocationsRead guards the read: unknown seller 404, malformed
// month 422.
func TestCapacityAllocationsRead(t *testing.T) {
	e := newEnv(t)
	volumeNode(t, e, "n-cap", "FULFILLMENT_CENTER", "reg-1", 1000)
	if _, err := e.svc.GetSellerCapacityAllocations(context.Background(), "nobody", "2026-10"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown seller err = %v", err)
	}
	if _, err := e.svc.GetSellerCapacityAllocations(context.Background(), "S-1", "2026-13"); err == nil {
		t.Fatal("malformed month accepted")
	}
}
