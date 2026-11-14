package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
)

// closingEvent returns the InboundShipmentReceived the receipt appended.
func closingEvent(t *testing.T, e *env, before int) inboundshipment.InboundShipmentReceived {
	t.Helper()
	for _, ev := range e.store.Outbox().Events()[before:] {
		if r, ok := ev.(inboundshipment.InboundShipmentReceived); ok {
			return r
		}
	}
	t.Fatal("no InboundShipmentReceived appended")
	return inboundshipment.InboundShipmentReceived{}
}

func receiveAll(t *testing.T, e *env, shp, carton string, items ...requests.ReceiptItem) inboundshipment.InboundShipmentReceived {
	t.Helper()
	before := len(e.store.Outbox().Events())
	in := requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", ReceivedAt: t0.Add(time24h()),
		Cartons: []requests.ReceiptCarton{{SSCC: carton, Status: "RECEIVED", Items: items}}}
	if err := e.svc.ApplyInboundReceipt(context.Background(), "rcpt-1", in); err != nil {
		t.Fatal(err)
	}
	return closingEvent(t, e, before)
}

func declareEach(t *testing.T, e *env, eachGTIN string) {
	t.Helper()
	if err := e.svc.ApplyPackagingDeclared(context.Background(), "ev-pk-each", requests.PackagingDeclared{CatalogItemID: "item-1",
		Level: "EACH", GTIN: eachGTIN, UnitsPerPack: 1, DeclaredAt: t0}); err != nil {
		t.Fatal(err)
	}
}

// TestReceiptPublishesTheCaseInEachesOfItsEachGtin: a CASE carton of 12
// eaches closes as one received line of 12 eaches under the EACH GTIN the
// local CatalogPackagingView resolves the case GTIN to (ADR 0014).
func TestReceiptPublishesTheCaseInEachesOfItsEachGtin(t *testing.T) {
	e := newEnv(t)
	shp, carton, caseGTIN := shippedShipment(t, e)
	each := gtin(t, "0614141000012")
	declareEach(t, e, each)
	got := receiveAll(t, e, shp, carton)
	want := []inboundshipment.ReceivedLine{{CatalogItemID: "item-1", GTIN: each, QtyEaches: 12}}
	if !reflect.DeepEqual(got.Received, want) {
		t.Fatalf("received %+v, want %+v (case %s)", got.Received, want, caseGTIN)
	}
}

// TestReceiptCountedShortCartonPublishesTheCount: the vendor's count wins
// over the manifest for a discrepant RECEIVED carton.
func TestReceiptCountedShortCartonPublishesTheCount(t *testing.T) {
	e := newEnv(t)
	shp, carton, caseGTIN := shippedShipment(t, e)
	each := gtin(t, "0614141000012")
	declareEach(t, e, each)
	got := receiveAll(t, e, shp, carton, requests.ReceiptItem{GTIN: caseGTIN, QtyEaches: 10})
	want := []inboundshipment.ReceivedLine{{CatalogItemID: "item-1", GTIN: each, QtyEaches: 10}}
	if got.Status != inboundshipment.ClosedWithDiscrepancy || !reflect.DeepEqual(got.Received, want) {
		t.Fatalf("%s received %+v, want %+v", got.Status, got.Received, want)
	}
}

// TestReceiptWithoutAnEachLevelKeepsTheCaseGtin: the view knows the case GTIN
// but holds no EACH row for its child, so the case GTIN is published with the
// item.
func TestReceiptWithoutAnEachLevelKeepsTheCaseGtin(t *testing.T) {
	e := newEnv(t)
	shp, carton, caseGTIN := shippedShipment(t, e)
	got := receiveAll(t, e, shp, carton)
	want := []inboundshipment.ReceivedLine{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 12}}
	if !reflect.DeepEqual(got.Received, want) {
		t.Fatalf("received %+v, want %+v", got.Received, want)
	}
}

// TestReceiptMissingCartonPublishesNoLines: a MISSING carton does not count;
// received is an empty, non-nil list.
func TestReceiptMissingCartonPublishesNoLines(t *testing.T) {
	e := newEnv(t)
	shp, carton, _ := shippedShipment(t, e)
	before := len(e.store.Outbox().Events())
	in := requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", ReceivedAt: t0.Add(time24h()),
		Cartons: []requests.ReceiptCarton{{SSCC: carton, Status: "MISSING"}}}
	if err := e.svc.ApplyInboundReceipt(context.Background(), "rcpt-1", in); err != nil {
		t.Fatal(err)
	}
	if got := closingEvent(t, e, before).Received; got == nil || len(got) != 0 {
		t.Fatalf("received %#v", got)
	}
}

// failingPackaging fails ListByItem once armed.
type failingPackaging struct {
	ports.PackagingViewRepository
	armed bool
}

var errViewDown = errors.New("packaging view down")

func (f *failingPackaging) ListByItem(ctx context.Context, item string) ([]network.PackLevel, error) {
	if f.armed {
		return nil, errViewDown
	}
	return f.PackagingViewRepository.ListByItem(ctx, item)
}

// TestReceiptFailsTheUnitWhenThePackagingViewCannotBeRead: a read error of
// the local view rolls the whole receipt back (nothing saved or published),
// so the redelivery can succeed.
func TestReceiptFailsTheUnitWhenThePackagingViewCannotBeRead(t *testing.T) {
	store := memory.NewStore()
	view := &failingPackaging{PackagingViewRepository: store.Packaging()}
	issuer := &memory.SsccIssuer{}
	e := &env{store: store, clock: memory.NewClock(t0), sscc: issuer}
	e.svc = usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Sellers: store.Sellers(), Plans: store.Plans(), Shipments: store.Shipments(), Nodes: store.Nodes(), Scorecard: store.Scorecard(), Capacity: store.Capacity(),
		Packaging: view, DemandHeat: store.DemandHeat(), Listings: store.Listings(), PlacementPlans: store.PlacementPlans(),
		Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: e.clock, IDs: &memory.SequentialIDs{}, SSCC: issuer,
	}, usecases.Config{})
	shp, carton, _ := shippedShipment(t, e)
	before := len(store.Outbox().Events())
	view.armed = true
	in := requests.InboundReceipt{ShipmentID: shp, NodeID: "n-1", ReceivedAt: t0.Add(time24h()),
		Cartons: []requests.ReceiptCarton{{SSCC: carton, Status: "RECEIVED"}}}
	if err := e.svc.ApplyInboundReceipt(context.Background(), "rcpt-1", in); !errors.Is(err, errViewDown) {
		t.Fatalf("err %v", err)
	}
	if n := len(store.Outbox().Events()) - before; n != 0 {
		t.Fatalf("published %d events", n)
	}
	if s, _ := e.svc.GetInboundShipment(context.Background(), shp); s.State != "SHIPPED" {
		t.Fatalf("state %s", s.State)
	}
	view.armed = false
	if err := e.svc.ApplyInboundReceipt(context.Background(), "rcpt-1", in); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if got := closingEvent(t, e, before).Received; len(got) != 1 || got[0].QtyEaches != 12 {
		t.Fatalf("received %+v", got)
	}
}
