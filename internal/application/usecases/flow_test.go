package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func (e *env) plan(t *testing.T, packing string, qty int, caseGTIN, eachGTIN string) views.Plan {
	t.Helper()
	g := eachGTIN
	if packing == "CASE_PACKED" {
		g = caseGTIN
	}
	p, err := e.svc.CreateInboundPlan(context.Background(), requests.CreateInboundPlan{SellerID: "S-1", DestinationNodeID: "n-1", PackingOption: packing,
		Lines: []requests.PlanLine{{CatalogItemID: "item-1", GTIN: g, QtyEaches: qty}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func addCarton(shipmentID, sscc, gtin string, qty int) requests.AddCarton {
	return requests.AddCarton{ShipmentID: shipmentID, SSCC: sscc, Contents: []requests.Content{{CatalogItemID: "item-1", GTIN: gtin, QtyEaches: qty}},
		GrossWeightG: 5000, DimsL: 400, DimsW: 300, DimsH: 200}
}

func TestSellerLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "S-1", Name: "Acme"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "S-1", Name: "Acme"}); !errors.Is(err, seller.ErrSellerExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := e.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "bad id", Name: "Acme"}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("invalid: %v", err)
	}
	s, err := e.svc.SuspendSeller(ctx, requests.SuspendSeller{SellerID: "S-1", Reason: "late"})
	if err != nil || s.State != "SUSPENDED" || s.SuspendReason != "late" {
		t.Fatalf("%v %+v", err, s)
	}
	if s, err = e.svc.ReinstateSeller(ctx, "S-1"); err != nil || s.State != "ACTIVE" {
		t.Fatalf("%v %+v", err, s)
	}
	if _, err := e.svc.SuspendSeller(ctx, requests.SuspendSeller{SellerID: "nope"}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if got := eventNames(e.store.Outbox().Events()); !reflect.DeepEqual(got, []string{"SellerOnboarded", "SellerSuspended", "SellerReinstated"}) {
		t.Fatalf("events %v", got)
	}
	// a second suspend/reinstate pair is a no-op without event
	_, _ = e.svc.ReinstateSeller(ctx, "S-1")
	if len(e.store.Outbox().Events()) != 3 {
		t.Fatal("reinstating an active seller must publish nothing")
	}
}

func planGuardEnv(t *testing.T) (*env, requests.CreateInboundPlan) {
	t.Helper()
	e := newEnv(t)
	caseGTIN, _ := e.seed(t)
	return e, requests.CreateInboundPlan{SellerID: "S-1", DestinationNodeID: "n-1", PackingOption: "CASE_PACKED",
		Lines: []requests.PlanLine{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 120}}}
}

func TestPlanRefusals(t *testing.T) {
	ctx := context.Background()
	e, in := planGuardEnv(t)
	e.node(t, "n-2", "DELIVERY_STATION", true)
	cases := []struct {
		name string
		mut  func(*requests.CreateInboundPlan)
		want error
	}{
		{"unknown seller", func(p *requests.CreateInboundPlan) { p.SellerID = "ghost" }, ports.ErrNotFound},
		{"unknown node cannot receive", func(p *requests.CreateInboundPlan) { p.DestinationNodeID = "ghost" }, network.ErrNodeCannotReceive},
		{"node without INBOUND capability", func(p *requests.CreateInboundPlan) { p.DestinationNodeID = "n-2" }, network.ErrNodeCannotReceive},
		{"invalid line", func(p *requests.CreateInboundPlan) {
			p.Lines = []requests.PlanLine{{CatalogItemID: "item-1", GTIN: "123", QtyEaches: 1}}
		}, shared.ErrValidation},
	}
	for _, tc := range cases {
		bad := in
		tc.mut(&bad)
		if _, err := e.svc.CreateInboundPlan(ctx, bad); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	_, _ = e.svc.SuspendSeller(ctx, requests.SuspendSeller{SellerID: "S-1"})
	if _, err := e.svc.CreateInboundPlan(ctx, in); !errors.Is(err, seller.ErrSellerSuspended) {
		t.Errorf("suspended seller: %v", err)
	}
}

func TestPlanConfirmCancelAndRead(t *testing.T) {
	ctx := context.Background()
	e, in := planGuardEnv(t)
	p, err := e.svc.CreateInboundPlan(ctx, in)
	if err != nil || p.State != "DRAFT" || p.Lines[0].GTIN != in.Lines[0].GTIN {
		t.Fatalf("%v %+v", err, p)
	}
	if c, err := e.svc.ConfirmInboundPlan(ctx, p.PlanID); err != nil || c.State != "CONFIRMED" {
		t.Fatal(err)
	}
	if _, err := e.svc.CancelInboundPlan(ctx, p.PlanID); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("cancel confirmed: %v", err)
	}
	q, _ := e.svc.CreateInboundPlan(ctx, in)
	if x, err := e.svc.CancelInboundPlan(ctx, q.PlanID); err != nil || x.State != "CANCELLED" {
		t.Fatal(err)
	}
	if got, err := e.svc.GetInboundPlan(ctx, p.PlanID); err != nil || got.State != "CONFIRMED" {
		t.Fatal(err)
	}
	for _, f := range []func(context.Context, string) (views.Plan, error){e.svc.GetInboundPlan, e.svc.ConfirmInboundPlan, e.svc.CancelInboundPlan} {
		if _, err := f(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
			t.Fatal(err)
		}
	}
}

func TestPlanConfirmNeedsActiveSellerAndNode(t *testing.T) {
	ctx := context.Background()
	e, in := planGuardEnv(t)
	p, _ := e.svc.CreateInboundPlan(ctx, in)
	_, _ = e.svc.SuspendSeller(ctx, requests.SuspendSeller{SellerID: "S-1"})
	if _, err := e.svc.ConfirmInboundPlan(ctx, p.PlanID); !errors.Is(err, seller.ErrSellerSuspended) {
		t.Fatal(err)
	}
	_, _ = e.svc.ReinstateSeller(ctx, "S-1")
	_ = e.svc.ApplyNodeSuspended(ctx, "ev-susp", requests.NodeLifecycle{NodeID: "n-1"})
	if _, err := e.svc.ConfirmInboundPlan(ctx, p.PlanID); !errors.Is(err, network.ErrNodeCannotReceive) {
		t.Fatal(err)
	}
}

// twoCartonShipment builds a DRAFT shipment of two case cartons: one with an
// issued SSCC, one with a seller-supplied SSCC.
func twoCartonShipment(t *testing.T, e *env) (shipmentID, caseGTIN string) {
	t.Helper()
	ctx := context.Background()
	caseGTIN, eachGTIN := e.seed(t)
	p := e.plan(t, "CASE_PACKED", 24, caseGTIN, eachGTIN)
	if _, err := e.svc.CreateInboundShipment(ctx, p.PlanID); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("a DRAFT plan has no shipments: %v", err)
	}
	if _, err := e.svc.ConfirmInboundPlan(ctx, p.PlanID); err != nil {
		t.Fatal(err)
	}
	sh, err := e.svc.CreateInboundShipment(ctx, p.PlanID)
	if err != nil || sh.State != "DRAFT" || sh.NodeID != "n-1" || sh.SellerID != "S-1" {
		t.Fatalf("%v %+v", err, sh)
	}
	c1, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", caseGTIN, 12))
	if err != nil || !c1.Issued || len(c1.Carton.SSCC) != 18 || c1.Carton.Kind != "HOMOGENEOUS_CASE" {
		t.Fatalf("%v %+v", err, c1)
	}
	assertLabel(t, c1)
	mine := sscc(t, "9999")
	c2, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, mine, caseGTIN, 12))
	if err != nil || c2.Issued || c2.Carton.SSCC != mine {
		t.Fatalf("%v %+v", err, c2)
	}
	assertSsccRefused(t, e, sh.ShipmentID, caseGTIN, mine)
	return sh.ShipmentID, caseGTIN
}

func TestCasePackedHappyPathToShipped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	shp, _ := twoCartonShipment(t, e)
	if _, err := e.svc.ShipInboundShipment(ctx, shp); !errors.Is(err, inboundshipment.ErrShipmentNotLabelled) {
		t.Fatalf("ship before label: %v", err)
	}
	labelled, err := e.svc.LabelShipment(ctx, shp)
	if err != nil || labelled.State != "LABELLED" || !labelled.Cartons[0].Sealed {
		t.Fatalf("%v %+v", err, labelled)
	}
	if labels, err := e.svc.GetLabels(ctx, shp); err != nil || len(labels.Labels) != 2 || labels.State != "LABELLED" || labels.DestinationNodeID != "n-1" {
		t.Fatalf("%v %+v", err, labels)
	}
	if shipped, err := e.svc.ShipInboundShipment(ctx, shp); err != nil || shipped.State != "SHIPPED" {
		t.Fatalf("%v %+v", err, shipped)
	}
	if got, err := e.svc.GetInboundShipment(ctx, shp); err != nil || got.State != "SHIPPED" || len(got.Cartons) != 2 || got.ShippedAt.IsZero() {
		t.Fatalf("%v %+v", err, got)
	}
	assertEvents(t, e, "SellerOnboarded", "InboundPlanConfirmed", "InboundShipmentLabelled", "InboundShipmentShipped")
}

func assertEvents(t *testing.T, e *env, want ...string) {
	t.Helper()
	if names := eventNames(e.store.Outbox().Events()); !reflect.DeepEqual(names, want) {
		t.Fatalf("events %v, want %v", names, want)
	}
}

func TestCartonRulesAtUseCaseLevel(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	caseGTIN, eachGTIN := e.seed(t)
	p := e.plan(t, "CASE_PACKED", 120, caseGTIN, eachGTIN)
	_, _ = e.svc.ConfirmInboundPlan(ctx, p.PlanID)
	sh, _ := e.svc.CreateInboundShipment(ctx, p.PlanID)
	if _, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", caseGTIN, 10)); !errors.Is(err, inboundshipment.ErrCaseQtyMismatchesGtin) {
		t.Fatalf("qty: %v", err)
	}
	if _, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", eachGTIN, 12)); !errors.Is(err, inboundshipment.ErrCaseQtyMismatchesGtin) {
		t.Fatalf("not a case gtin: %v", err)
	}
	// a node limit
	if err := e.svc.ApplyNodeProfileDeclared(ctx, "ev-prof", requests.NodeProfileDeclared{NodeID: "n-1", ProfileRevision: 1,
		Inbound: &requests.NodeInbound{MaxCartonWeightG: 4000, MaxCartonDims: &requests.Dims{L: 400, W: 300, H: 200}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", caseGTIN, 12)); !errors.Is(err, inboundshipment.ErrCartonOverweight) {
		t.Fatalf("overweight: %v", err)
	}
	c := addCarton(sh.ShipmentID, "", caseGTIN, 12)
	c.GrossWeightG, c.DimsH = 3000, 201
	if _, err := e.svc.AddCarton(ctx, c); !errors.Is(err, inboundshipment.ErrCartonOversize) {
		t.Fatalf("oversize: %v", err)
	}
	// a retired case level no longer validates
	if err := e.svc.ApplyPackLevelRetired(ctx, "ev-ret", requests.PackLevelRetired{CatalogItemID: "item-1", Level: "CASE", GTIN: caseGTIN, RetiredAt: t0}); err != nil {
		t.Fatal(err)
	}
	c.DimsH = 200
	if _, err := e.svc.AddCarton(ctx, c); !errors.Is(err, inboundshipment.ErrCaseQtyMismatchesGtin) {
		t.Fatalf("retired level: %v", err)
	}
	if got := e.store.Counts(); got.Ssccs != 0 {
		t.Fatalf("refused cartons must register nothing: %+v", got)
	}
	// the label must cover the plan exactly for a single shipment
	if err := e.svc.ApplyPackagingDeclared(ctx, "ev-pk-2", requests.PackagingDeclared{CatalogItemID: "item-1", Level: "CASE", GTIN: gtin(t, "0614141000043"), UnitsPerPack: 12, DeclaredAt: t0}); err != nil {
		t.Fatal(err)
	}
	ok := addCarton(sh.ShipmentID, "", gtin(t, "0614141000043"), 12)
	ok.GrossWeightG = 3000
	if _, err := e.svc.AddCarton(ctx, ok); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.LabelShipment(ctx, sh.ShipmentID); !errors.Is(err, inboundshipment.ErrCartonContentsMismatchPlan) {
		t.Fatalf("label short of the plan: %v", err)
	}
}

func TestSuspendedSellerAndNodeStopShipments(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	caseGTIN, eachGTIN := e.seed(t)
	p := e.plan(t, "INDIVIDUAL", 5, caseGTIN, eachGTIN)
	_, _ = e.svc.ConfirmInboundPlan(ctx, p.PlanID)
	sh, _ := e.svc.CreateInboundShipment(ctx, p.PlanID)
	if _, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", eachGTIN, 5)); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.LabelShipment(ctx, sh.ShipmentID)
	_, _ = e.svc.SuspendSeller(ctx, requests.SuspendSeller{SellerID: "S-1"})
	if _, err := e.svc.ShipInboundShipment(ctx, sh.ShipmentID); !errors.Is(err, seller.ErrSellerSuspended) {
		t.Fatalf("ship while suspended: %v", err)
	}
	if _, err := e.svc.CreateInboundShipment(ctx, p.PlanID); !errors.Is(err, seller.ErrSellerSuspended) {
		t.Fatalf("create while suspended: %v", err)
	}
	if _, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", eachGTIN, 1)); !errors.Is(err, seller.ErrSellerSuspended) {
		t.Fatalf("carton while suspended: %v", err)
	}
	if _, err := e.svc.LabelShipment(ctx, sh.ShipmentID); !errors.Is(err, seller.ErrSellerSuspended) {
		t.Fatalf("label while suspended: %v", err)
	}
	_, _ = e.svc.ReinstateSeller(ctx, "S-1")
	_ = e.svc.ApplyNodeRetired(ctx, "ev-ret-n1", requests.NodeLifecycle{NodeID: "n-1"})
	if _, err := e.svc.ShipInboundShipment(ctx, sh.ShipmentID); !errors.Is(err, network.ErrNodeCannotReceive) {
		t.Fatalf("ship to a retired node: %v", err)
	}
	if _, err := e.svc.GetInboundShipment(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := e.svc.GetLabels(ctx, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestIssuedSsccCollisionIsReissued(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	caseGTIN, eachGTIN := e.seed(t)
	p := e.plan(t, "INDIVIDUAL", 10, caseGTIN, eachGTIN)
	_, _ = e.svc.ConfirmInboundPlan(ctx, p.PlanID)
	sh, _ := e.svc.CreateInboundShipment(ctx, p.PlanID)
	// a seller took the SSCC the network is about to issue (serial 1 under the network prefix)
	next, _ := (&memory.SsccIssuer{}).Next(ctx)
	other, _ := e.svc.CreateInboundShipment(ctx, p.PlanID)
	if _, err := e.svc.AddCarton(ctx, addCarton(other.ShipmentID, next, eachGTIN, 1)); err != nil {
		t.Fatal(err)
	}
	c, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", eachGTIN, 1))
	if err != nil || !c.Issued || c.Carton.SSCC == next {
		t.Fatalf("collision must be re-issued: %v %+v", err, c)
	}
	e.sscc.Fail = errors.New("sequence down")
	if _, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", eachGTIN, 1)); err == nil {
		t.Fatal("an issuer failure surfaces")
	}
}

func TestEveryRejectionLeavesNoTrace(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	caseGTIN, eachGTIN := e.seed(t)
	p := e.plan(t, "INDIVIDUAL", 5, caseGTIN, eachGTIN)
	_, _ = e.svc.ConfirmInboundPlan(ctx, p.PlanID)
	sh, _ := e.svc.CreateInboundShipment(ctx, p.PlanID)
	before := e.store.Counts()
	_, err := e.svc.AddCarton(ctx, addCarton(sh.ShipmentID, "", eachGTIN, 6)) // exceeds the plan line
	if !errors.Is(err, inboundshipment.ErrCartonContentsMismatchPlan) {
		t.Fatal(err)
	}
	if after := e.store.Counts(); after != before {
		t.Fatalf("a rejected command changed the store: %+v -> %+v", before, after)
	}
}

func assertSsccRefused(t *testing.T, e *env, shipmentID, gtin, taken string) {
	t.Helper()
	for sscc, want := range map[string]error{taken: inboundshipment.ErrSsccNotUnique, "123": inboundshipment.ErrInvalidSscc} {
		if _, err := e.svc.AddCarton(context.Background(), addCarton(shipmentID, sscc, gtin, 12)); !errors.Is(err, want) {
			t.Fatalf("%s: %v", sscc, err)
		}
	}
}

func assertLabel(t *testing.T, c views.AddedCarton) {
	t.Helper()
	l := c.Label
	if l.GS1128 != "(00)"+c.Carton.SSCC || l.DestinationNodeID != "n-1" || l.TotalEaches != 12 || l.LabelRef != "LBL-"+c.Carton.SSCC {
		t.Fatalf("label %+v", l)
	}
	if !strings.Contains(l.ContentSummary, "12 eaches") || !strings.HasPrefix(l.HumanReadable, "(00) 0 ") {
		t.Fatalf("label text %+v", l)
	}
}
