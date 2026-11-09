package inboundshipment

import (
	"errors"
	"reflect"
	"testing"

	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// GTINs of the received-lines tests (ADR 0014): item-1 has an EACH, an INNER
// and a CASE level; item-2 an EACH and a CASE level.
const (
	rEach1  = "00614141000012"
	rCase1  = "00614141000036"
	rInner1 = "00614141000043"
	rEach2  = "00614141000050"
	rCase2  = "00614141000067"
)

// packagingView is a CatalogPackagingView fixture resolved with the real rule.
func packagingView(t *testing.T) ItemPackaging {
	t.Helper()
	declare := func(item string, l network.Level, gtin, child string, units int) network.PackLevel {
		p, err := network.Declare(nil, item, l, gtin, child, units, t0)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	rows := map[string][]network.PackLevel{
		"item-1": {declare("item-1", network.Case, rCase1, rInner1, 12), declare("item-1", network.Inner, rInner1, rEach1, 4),
			declare("item-1", network.Each, rEach1, "", 1)},
		"item-2": {declare("item-2", network.Case, rCase2, rEach2, 6), declare("item-2", network.Each, rEach2, "", 1)},
	}
	return func(item, gtin string) (string, bool) { return network.ResolveEach(rows[item], gtin) }
}

// shipped labels and ships a shipment of the given cartons; the plan is
// exactly what the cartons carry.
func shipped(t *testing.T, packing inboundplan.PackingOption, cartons ...NewCarton) *Shipment {
	t.Helper()
	plan := map[string]int{}
	for _, c := range cartons {
		for _, ct := range c.Contents {
			plan[ct.CatalogItemID] += ct.QtyEaches
		}
	}
	rules := CartonRules{PlanRules: PlanRules{Packing: packing, PlanQty: plan, SingleShipment: true},
		CaseUnits: func(_, g string) (int, bool) {
			return map[string]int{rCase1: 12, rCase2: 6}[g], g == rCase1 || g == rCase2
		}}
	s := newShipment(t)
	for _, c := range cartons {
		if _, err := s.AddCarton(c, rules); err != nil {
			t.Fatalf("add %s: %v", c.SSCC, err)
		}
	}
	if _, err := s.Label(rules.PlanRules, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ship(at(1)); err != nil {
		t.Fatal(err)
	}
	return s
}

func mixedCarton(t *testing.T, serial int, contents ...Content) NewCarton {
	t.Helper()
	return NewCarton{SSCC: mustSSCC(t, serial), Contents: contents, GrossWeightG: 5000, Dims: Dims{L: 400, W: 300, H: 200}}
}

// closeWith records one receipt that closes the shipment and returns its
// InboundShipmentReceived.
func closeWith(t *testing.T, s *Shipment, pack ItemPackaging, cartons ...ReceiptCarton) InboundShipmentReceived {
	t.Helper()
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: cartons, Packaging: pack}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if r, ok := e.(InboundShipmentReceived); ok {
			return r
		}
	}
	t.Fatalf("no InboundShipmentReceived in %v", names(evs))
	return InboundShipmentReceived{}
}

func wantLines(t *testing.T, got InboundShipmentReceived, want ...ReceivedLine) {
	t.Helper()
	if want == nil {
		want = []ReceivedLine{}
	}
	if got.Received == nil || !reflect.DeepEqual(got.Received, want) {
		t.Fatalf("received = %#v, want %#v", got.Received, want)
	}
}

// TestReceivedCaseCartonsArePublishedInEachesOfTheEachGtin: two CASE cartons
// of 12 eaches publish one line of 24 eaches under the EACH GTIN their case
// GTIN resolves to (CASE -> INNER -> EACH); the stored qtyEaches is already
// eaches and is never multiplied again.
func TestReceivedCaseCartonsArePublishedInEachesOfTheEachGtin(t *testing.T) {
	s := shipped(t, inboundplan.CasePacked,
		carton(t, 1, "item-1", rCase1, 12), carton(t, 2, "item-1", rCase1, 12), carton(t, 3, "item-2", rCase2, 6))
	e := closeWith(t, s, packagingView(t),
		ReceiptCarton{SSCC: mustSSCC(t, 1), Status: StatusReceived}, ReceiptCarton{SSCC: mustSSCC(t, 2), Status: StatusReceived},
		ReceiptCarton{SSCC: mustSSCC(t, 3), Status: StatusReceived})
	if e.Status != Closed {
		t.Fatalf("status %s", e.Status)
	}
	wantLines(t, e, ReceivedLine{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 24}, ReceivedLine{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 6})
}

// TestReceivedCaseAndEachLinesOfOneItemAggregate: a case line and an each
// line of the same item land on one EACH GTIN line.
func TestReceivedCaseAndEachLinesOfOneItemAggregate(t *testing.T) {
	s := shipped(t, inboundplan.Individual,
		mixedCarton(t, 1, Content{CatalogItemID: "item-1", GTIN: rCase1, QtyEaches: 12}),
		mixedCarton(t, 2, Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 5}, Content{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 3}))
	e := closeWith(t, s, packagingView(t),
		ReceiptCarton{SSCC: mustSSCC(t, 1), Status: StatusReceived}, ReceiptCarton{SSCC: mustSSCC(t, 2), Status: StatusReceived})
	wantLines(t, e, ReceivedLine{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 17}, ReceivedLine{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 3})
}

// TestReceivedDiscrepancyCartonUsesTheCountedQuantities: a RECEIVED carton
// counted short, with a GTIN the carton's manifest does not list, publishes
// the counts; a counted zero is omitted.
func TestReceivedDiscrepancyCartonUsesTheCountedQuantities(t *testing.T) {
	s := shipped(t, inboundplan.Individual,
		mixedCarton(t, 1, Content{CatalogItemID: "item-1", GTIN: rCase1, QtyEaches: 12}, Content{CatalogItemID: "item-2", GTIN: rCase2, QtyEaches: 6}),
		mixedCarton(t, 2, Content{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 3}))
	e := closeWith(t, s, packagingView(t),
		ReceiptCarton{SSCC: mustSSCC(t, 1), Status: StatusReceived, Items: []ReceiptItem{
			{GTIN: rCase1, QtyEaches: 10}, {GTIN: rCase2, QtyEaches: 0}, {GTIN: rEach2, QtyEaches: 2}}},
		ReceiptCarton{SSCC: mustSSCC(t, 2), Status: StatusReceived})
	if e.Status != ClosedWithDiscrepancy {
		t.Fatalf("status %s", e.Status)
	}
	// carton 1: 10 of item-1 (counted), 0 of the item-2 case, 2 of the item-2
	// each GTIN that only carton 2's manifest lists; carton 2: 3 (manifest).
	wantLines(t, e, ReceivedLine{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 10}, ReceivedLine{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 5})
}

// TestReceivedMissingDamagedAndUnexpectedCartonsDoNotCount: only RECEIVED
// manifest cartons count; when none does, received is an empty array.
func TestReceivedMissingDamagedAndUnexpectedCartonsDoNotCount(t *testing.T) {
	s := shipped(t, inboundplan.Individual,
		mixedCarton(t, 1, Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4}),
		mixedCarton(t, 2, Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 5}),
		mixedCarton(t, 3, Content{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 6}))
	e := closeWith(t, s, packagingView(t),
		ReceiptCarton{SSCC: mustSSCC(t, 1), Status: StatusReceived},
		ReceiptCarton{SSCC: mustSSCC(t, 2), Status: StatusMissing},
		ReceiptCarton{SSCC: mustSSCC(t, 3), Status: StatusDamaged, Items: []ReceiptItem{{GTIN: rEach2, QtyEaches: 6}}},
		ReceiptCarton{SSCC: mustSSCC(t, 77), Status: StatusUnexpected, Items: []ReceiptItem{{GTIN: rEach2, QtyEaches: 9}}})
	wantLines(t, e, ReceivedLine{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4})

	none := shipped(t, inboundplan.Individual, mixedCarton(t, 1, Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4}))
	wantLines(t, closeWith(t, none, packagingView(t), ReceiptCarton{SSCC: mustSSCC(t, 1), Status: StatusMissing}))
}

// TestReceivedUnresolvedPackagingKeepsTheCartonGtin: with no packaging view
// the carton GTIN is published without an item; a GTIN the view knows but
// cannot walk to EACH keeps the carton GTIN and gains the item. Lines come
// back sorted by gtin.
func TestReceivedUnresolvedPackagingKeepsTheCartonGtin(t *testing.T) {
	cartons := []NewCarton{
		mixedCarton(t, 1, Content{CatalogItemID: "item-2", GTIN: rCase2, QtyEaches: 6}),
		mixedCarton(t, 2, Content{CatalogItemID: "item-1", GTIN: rCase1, QtyEaches: 12}),
	}
	answers := []ReceiptCarton{{SSCC: mustSSCC(t, 1), Status: StatusReceived}, {SSCC: mustSSCC(t, 2), Status: StatusReceived}}
	wantLines(t, closeWith(t, shipped(t, inboundplan.Individual, cartons...), nil, answers...),
		ReceivedLine{GTIN: rCase1, QtyEaches: 12}, ReceivedLine{GTIN: rCase2, QtyEaches: 6})

	noChild := func(item, gtin string) (string, bool) {
		if item == "item-1" && gtin == rCase1 {
			return "", true
		}
		return "", false
	}
	wantLines(t, closeWith(t, shipped(t, inboundplan.Individual, cartons...), noChild, answers...),
		ReceivedLine{CatalogItemID: "item-1", GTIN: rCase1, QtyEaches: 12}, ReceivedLine{GTIN: rCase2, QtyEaches: 6})
}

// TestReceivedCountedGtinOfNoManifestLineIsUnresolved: a counted GTIN no
// manifest line lists has no item to resolve and is published as counted.
func TestReceivedCountedGtinOfNoManifestLineIsUnresolved(t *testing.T) {
	s := shipped(t, inboundplan.Individual, mixedCarton(t, 1, Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4}))
	e := closeWith(t, s, packagingView(t), ReceiptCarton{SSCC: mustSSCC(t, 1), Status: StatusReceived, Items: []ReceiptItem{
		{GTIN: rEach1, QtyEaches: 4}, {GTIN: rCase2, QtyEaches: 2}}})
	wantLines(t, e, ReceivedLine{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4}, ReceivedLine{GTIN: rCase2, QtyEaches: 2})
}

// TestReceivedFirstResolvedItemWinsAndZeroTotalsAreOmitted: contributions
// of one published gtin keep the first resolved catalogItemId whatever the
// order; a counted GTIN takes its own carton's item before the shipment's;
// a gtin totalling zero eaches is omitted.
func TestReceivedFirstResolvedItemWinsAndZeroTotalsAreOmitted(t *testing.T) {
	unknown := Content{CatalogItemID: "item-3", GTIN: rEach1, QtyEaches: 2} // item-3 has no packaging rows
	known := Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4}
	answers := []ReceiptCarton{{SSCC: mustSSCC(t, 1), Status: StatusReceived}, {SSCC: mustSSCC(t, 2), Status: StatusReceived,
		Items: []ReceiptItem{{GTIN: rEach1, QtyEaches: 4}, {GTIN: rInner1, QtyEaches: 0}}}}
	for name, order := range map[string][]Content{"unresolved first": {unknown, known}, "resolved first": {known, unknown}} {
		s := shipped(t, inboundplan.Individual, mixedCarton(t, 1, order[0]), mixedCarton(t, 2, order[1]))
		e := closeWith(t, s, packagingView(t), answers...)
		if want := []ReceivedLine{{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: order[0].QtyEaches + 4}}; !reflect.DeepEqual(e.Received, want) {
			t.Errorf("%s: received %+v, want %+v", name, e.Received, want)
		}
	}
}

// TestReceivedCountsSurviveRestoreAcrossPartialReceipts: the counts of an
// earlier receipt are kept on the carton (Snapshot/Restore) and published
// when a later receipt closes the shipment.
func TestReceivedCountsSurviveRestoreAcrossPartialReceipts(t *testing.T) {
	s := shipped(t, inboundplan.Individual,
		mixedCarton(t, 1, Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4}),
		mixedCarton(t, 2, Content{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 3}))
	if _, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(4), Cartons: []ReceiptCarton{
		{SSCC: mustSSCC(t, 1), Status: StatusReceived, Items: []ReceiptItem{{GTIN: rEach1, QtyEaches: 3}}}}}, at(4)); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if got := snap.Cartons[0].Counted; !reflect.DeepEqual(got, []ReceiptItem{{GTIN: rEach1, QtyEaches: 3}}) {
		t.Fatalf("counted %v", got)
	}
	snap.Cartons[0].Counted[0].QtyEaches = 99 // the snapshot is a copy
	back, err := Restore(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	e := closeWith(t, back, packagingView(t), ReceiptCarton{SSCC: mustSSCC(t, 2), Status: StatusReceived})
	wantLines(t, e, ReceivedLine{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 3}, ReceivedLine{CatalogItemID: "item-2", GTIN: rEach2, QtyEaches: 3})
}

// TestRestoreRejectsInvalidCountedItems: counts need an answered carton, a
// 14-digit GTIN and a quantity in range.
func TestRestoreRejectsInvalidCountedItems(t *testing.T) {
	s := shipped(t, inboundplan.Individual, mixedCarton(t, 1, Content{CatalogItemID: "item-1", GTIN: rEach1, QtyEaches: 4}))
	bad := []func(*Carton){
		func(c *Carton) { c.Counted = []ReceiptItem{{GTIN: rEach1, QtyEaches: 1}} },
		func(c *Carton) {
			c.Receipt, c.Counted = StatusReceived, []ReceiptItem{{GTIN: "614141000012", QtyEaches: 1}}
		},
		func(c *Carton) { c.Receipt, c.Counted = StatusReceived, []ReceiptItem{{GTIN: rEach1, QtyEaches: -1}} },
		func(c *Carton) {
			c.Receipt, c.Counted = StatusReceived, []ReceiptItem{{GTIN: rEach1, QtyEaches: MaxQtyEaches + 1}}
		},
	}
	for i, mutate := range bad {
		snap := s.Snapshot()
		mutate(&snap.Cartons[0])
		if _, err := Restore(snap); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("case %d: %v", i, err)
		}
	}
	ok := s.Snapshot()
	ok.Cartons[0].Receipt, ok.Cartons[0].Counted = StatusReceived, []ReceiptItem{{GTIN: rEach1, QtyEaches: 0}, {GTIN: rEach1, QtyEaches: MaxQtyEaches}}
	if _, err := Restore(ok); err != nil {
		t.Fatalf("boundaries: %v", err)
	}
}
