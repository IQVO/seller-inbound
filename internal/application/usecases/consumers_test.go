package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func TestNodeViewFolds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	yes := true
	steps := []func() error{
		func() error {
			return e.svc.ApplyNodeProfileDeclared(ctx, "e1", requests.NodeProfileDeclared{NodeID: "n-9", ProfileRevision: 1,
				Inbound: &requests.NodeInbound{MaxCartonWeightG: 1000, AcceptsPallets: &yes, MaxCartonDims: &requests.Dims{L: 1, W: 2, H: 3}}})
		}, // the profile may arrive before the registration
		func() error {
			return e.svc.ApplyNodeRegistered(ctx, "e2", requests.NodeRegistered{NodeID: "n-9", NodeType: "INBOUND_CROSS_DOCK"})
		},
		func() error { return e.svc.ApplyNodeActivated(ctx, "e3", requests.NodeLifecycle{NodeID: "n-9"}) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	n, err := e.store.Nodes().Get(ctx, "n-9")
	if err != nil || n.Status != network.NodeActive || n.Type != "INBOUND_CROSS_DOCK" || n.Limits == nil || n.Limits.MaxCartonWeightG != 1000 || *n.Limits.AcceptsPallets != true {
		t.Fatalf("%v %+v", err, n)
	}
	nodeViewLifecycle(t, e)
}

func nodeViewLifecycle(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	var n network.Node
	// a redelivered event changes nothing
	if err := e.svc.ApplyNodeSuspended(ctx, "e3", requests.NodeLifecycle{NodeID: "n-9"}); err != nil {
		t.Fatal(err)
	}
	if n, _ = e.store.Nodes().Get(ctx, "n-9"); n.Status != network.NodeActive {
		t.Fatalf("duplicate event id must be skipped: %+v", n)
	}
	_ = e.svc.ApplyNodeSuspended(ctx, "e4", requests.NodeLifecycle{NodeID: "n-9"})
	if n, _ = e.store.Nodes().Get(ctx, "n-9"); n.Status != network.NodeSuspended {
		t.Fatal(n)
	}
	// a later revision without inbound clears the limits
	_ = e.svc.ApplyNodeProfileDeclared(ctx, "e5", requests.NodeProfileDeclared{NodeID: "n-9", ProfileRevision: 2})
	if n, _ = e.store.Nodes().Get(ctx, "n-9"); n.Limits != nil || n.ProfileRevision != 2 {
		t.Fatal(n)
	}
	if err := e.svc.ApplyNodeRegistered(ctx, "e6", requests.NodeRegistered{NodeID: "", NodeType: "X"}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("invalid event: %v", err)
	}
	if got := e.store.Counts(); got.Inbox != 5 {
		t.Fatalf("the rejected event must not stay claimed: %+v", got)
	}
}

func TestPackagingViewFolds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	g := gtin(t, "0614141000036")
	if err := e.svc.ApplyPackLevelRetired(ctx, "p1", requests.PackLevelRetired{CatalogItemID: "item-1", Level: "CASE", GTIN: g, RetiredAt: t0}); err != nil {
		t.Fatal(err)
	}
	// declared after retired: stays retired
	if err := e.svc.ApplyPackagingDeclared(ctx, "p2", requests.PackagingDeclared{CatalogItemID: "item-1", Level: "CASE", GTIN: g, UnitsPerPack: 12, DeclaredAt: t0}); err != nil {
		t.Fatal(err)
	}
	row, err := e.store.Packaging().Get(ctx, "item-1", network.Case, g)
	if err != nil || row.Active || row.UnitsPerPack != 12 {
		t.Fatalf("%v %+v", err, row)
	}
	rows, _ := e.store.Packaging().ListByItem(ctx, "item-1")
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	for name, err := range map[string]error{
		"bad gtin":         e.svc.ApplyPackagingDeclared(ctx, "p3", requests.PackagingDeclared{CatalogItemID: "item-1", Level: "CASE", GTIN: "9", UnitsPerPack: 12}),
		"bad level":        e.svc.ApplyPackagingDeclared(ctx, "p4", requests.PackagingDeclared{CatalogItemID: "item-1", Level: "BOX", GTIN: g, UnitsPerPack: 12}),
		"bad units":        e.svc.ApplyPackagingDeclared(ctx, "p5", requests.PackagingDeclared{CatalogItemID: "item-1", Level: "CASE", GTIN: g, UnitsPerPack: 0}),
		"bad retire":       e.svc.ApplyPackLevelRetired(ctx, "p6", requests.PackLevelRetired{CatalogItemID: "item-1", Level: "CASE", GTIN: "9"}),
		"bad level retire": e.svc.ApplyPackLevelRetired(ctx, "p7", requests.PackLevelRetired{CatalogItemID: "item-1", Level: "BOX", GTIN: g}),
	} {
		if !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// shippedShipment ships a one-carton shipment of 12 eaches and returns its ids.
func shippedShipment(t *testing.T, e *env) (shipmentID, carton, eachGTIN string) {
	t.Helper()
	ctx := context.Background()
	caseGTIN, eachGTIN := e.seed(t)
	p := e.plan(t, "CASE_PACKED", 12, caseGTIN, eachGTIN)
	if _, err := e.svc.ConfirmInboundPlan(ctx, p.PlanID); err != nil {
		t.Fatal(err)
	}
	sh, _ := e.svc.CreateInboundShipment(ctx, p.PlanID)
	c, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", caseGTIN, 12))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LabelShipment(ctx, sh.ShipmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ShipInboundShipment(ctx, sh.ShipmentID); err != nil {
		t.Fatal(err)
	}
	return sh.ShipmentID, c.Carton.SSCC, caseGTIN
}

func TestReceiptClosesTheShipmentAndPublishesFacts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	shp, carton, g := shippedShipment(t, e)
	before := len(e.store.Outbox().Events())
	in := requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", ReceivedAt: t0.Add(time24h()), Cartons: []requests.ReceiptCarton{
		{SSCC: carton, Status: "DAMAGED", Items: []requests.ReceiptItem{{GTIN: g, QtyEaches: 10}}, ComplianceCodes: []string{"CARTON_CRUSHED"}}}}
	if err := e.svc.ApplyInboundReceipt(ctx, "rcpt-1", in); err != nil {
		t.Fatal(err)
	}
	got := eventNames(e.store.Outbox().Events()[before:])
	want := []string{"InboundDiscrepancyRecorded", "InboundDiscrepancyRecorded", "SellerComplianceViolationRecorded", "InboundShipmentReceived"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
	s, _ := e.svc.GetInboundShipment(ctx, shp)
	if s.State != "CLOSED_WITH_DISCREPANCY" || s.Cartons[0].ReceiptStatus != "DAMAGED" || s.ReceivedAt.IsZero() {
		t.Fatalf("%+v", s)
	}
	// redelivery with the same event id, and a late receipt with a new id: nothing more is published
	for _, id := range []string{"rcpt-1", "rcpt-2"} {
		if err := e.svc.ApplyInboundReceipt(ctx, id, in); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if n := len(e.store.Outbox().Events()) - before; n != 4 {
		t.Fatalf("published %d events", n)
	}
}

func TestReceiptPoison(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	shp, carton, _ := shippedShipment(t, e)
	if err := e.svc.ApplyInboundReceipt(ctx, "r1", requests.InboundReceipt{ShipmentID: "ghost", NodeID: "n-1"}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown shipment: %v", err)
	}
	if err := e.svc.ApplyInboundReceipt(ctx, "r2", requests.InboundReceipt{ShipmentID: shp, NodeID: "n-2", Cartons: []requests.ReceiptCarton{{SSCC: carton, Status: "RECEIVED"}}}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("wrong node: %v", err)
	}
	if err := e.svc.ApplyInboundReceipt(ctx, "r3", requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", Cartons: []requests.ReceiptCarton{{SSCC: carton, Status: "LOST"}}}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("bad status: %v", err)
	}
	// a receipt for a shipment that has not shipped
	p := e.plan(t, "INDIVIDUAL", 3, "", gtin(t, "0614141000012"))
	_, _ = e.svc.ConfirmInboundPlan(ctx, p.PlanID)
	draft, _ := e.svc.CreateInboundShipment(ctx, p.PlanID)
	if err := e.svc.ApplyInboundReceipt(ctx, "r4", requests.InboundReceipt{ShipmentID: draft.ShipmentID, NodeID: "n-1"}); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("draft shipment: %v", err)
	}
}

func time24h() time.Duration { return 24 * time.Hour }
