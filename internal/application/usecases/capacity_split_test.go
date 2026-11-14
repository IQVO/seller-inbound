package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/capacity"
)

// splitEnv onboards S-1 and seeds a region: an inbound cross-dock with a
// declared monthly volume and two fulfilment centres of declared limits.
func splitEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	volumeNode(t, e, "ixd-1", "INBOUND_CROSS_DOCK", "reg-1", 1000)
	for _, fc := range []struct {
		id    string
		limit int
	}{{"fc-a", 1000}, {"fc-b", 3000}} {
		volumeNode(t, e, fc.id, "FULFILLMENT_CENTER", "reg-1", fc.limit)
	}
	return e
}

func splitPlan(t *testing.T, e *env, destination string) (views.Plan, error) {
	t.Helper()
	return e.svc.CreateInboundPlan(context.Background(), requests.CreateInboundPlan{
		SellerID: "S-1", DestinationNodeID: destination, PackingOption: "INDIVIDUAL", PlacementOption: "NETWORK_SPLIT",
		Lines: []requests.PlanLine{{CatalogItemID: "item-1", GTIN: gtin(t, "0614141000012"), QtyEaches: 80, UnitVolumeDm3: 2}},
	})
}

// wantRedistribution asserts the golden 20/60 split across fc-a/fc-b.
func wantRedistribution(t *testing.T, v views.Plan) {
	t.Helper()
	if len(v.Redistribution) != 2 || v.Redistribution[0].LineNo != 1 {
		t.Fatalf("redistribution = %+v", v.Redistribution)
	}
	if v.Redistribution[0].NodeID != "fc-a" || v.Redistribution[0].QtyEaches != 20 {
		t.Fatalf("fc-a share = %+v", v.Redistribution[0])
	}
	if v.Redistribution[1].NodeID != "fc-b" || v.Redistribution[1].QtyEaches != 60 {
		t.Fatalf("fc-b share = %+v", v.Redistribution[1])
	}
}

// wantFeeBasis asserts the recorded fact: NETWORK_SPLIT, the month, the
// plan's own volume, both recipients sorted.
func wantFeeBasis(t *testing.T, v views.Plan) {
	t.Helper()
	f := v.PlacementFeeBasis
	if f == nil {
		t.Fatal("no fee basis recorded")
	}
	if f.Basis != "NETWORK_SPLIT" || f.Month != "2026-10" || f.Dm3 != 160 {
		t.Fatalf("fee basis = %+v", f)
	}
	if len(f.NodeIDs) != 2 || f.NodeIDs[0] != "fc-a" || f.NodeIDs[1] != "fc-b" {
		t.Fatalf("fee basis node ids = %v", f.NodeIDs)
	}
}

// TestNetworkSplitRecordsRedistributionAndFeeBasis: fc-b holds three times
// fc-a's remaining capacity, so capacity-split-v1 (GOOD factor) gives the
// 80 eaches 20/60, and the fee basis names both nodes and the plan's own
// volume.
func TestNetworkSplitRecordsRedistributionAndFeeBasis(t *testing.T) {
	e := splitEnv(t)
	v, err := splitPlan(t, e, "ixd-1")
	if err != nil {
		t.Fatal(err)
	}
	wantRedistribution(t, v)
	wantFeeBasis(t, v)
	if !v.CapacityEnforced {
		t.Fatal("both sides were known: the gate must be enforced")
	}
}

// TestNetworkSplitReservesAtTheCrossDock: the reservation sits at the
// CROSS-DOCK (the plan's destination), never at the fulfilment centres.
func TestNetworkSplitReservesAtTheCrossDock(t *testing.T) {
	ctx := context.Background()
	e := splitEnv(t)
	if _, err := splitPlan(t, e, "ixd-1"); err != nil {
		t.Fatal(err)
	}
	allocs, err := e.svc.GetSellerCapacityAllocations(ctx, "S-1", "2026-10")
	if err != nil || len(allocs.Items) != 1 || allocs.Items[0].NodeID != "ixd-1" || allocs.Items[0].ReservedDm3 != 160 {
		t.Fatalf("allocations = %+v %v", allocs, err)
	}
}

// TestNetworkSplitDestinationMustBeACrossDock: a NETWORK_SPLIT plan at a
// FULFILLMENT_CENTER is refused (422 network-split-destination).
func TestNetworkSplitDestinationMustBeACrossDock(t *testing.T) {
	e := splitEnv(t)
	if _, err := splitPlan(t, e, "fc-a"); !errors.Is(err, capacity.ErrNetworkSplitDestination) {
		t.Fatalf("err = %v", err)
	}
}

// TestNetworkSplitNeedsACentreInTheRegion: a cross-dock whose region holds no
// ACTIVE fulfilment centre is refused (422 network-split-unavailable): a
// redistribution with no destination is not a recordable fact.
func TestNetworkSplitNeedsACentreInTheRegion(t *testing.T) {
	e := newEnv(t)
	volumeNode(t, e, "ixd-lonely", "INBOUND_CROSS_DOCK", "reg-empty", 1000)
	if _, err := splitPlan(t, e, "ixd-lonely"); !errors.Is(err, capacity.ErrNetworkSplitUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
