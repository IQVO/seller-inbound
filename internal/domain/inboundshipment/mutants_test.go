package inboundshipment

import (
	"fmt"
	"testing"
	"time"
)

// Boundary tests: the value just inside every bound is accepted.

func TestCartonValueBoundariesAreInclusive(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	for name, mut := range map[string]func(*NewCarton){
		"weight 1":         func(c *NewCarton) { c.GrossWeightG = 1 },
		"weight ceiling":   func(c *NewCarton) { c.GrossWeightG = MaxGrossWeightG },
		"edges 1":          func(c *NewCarton) { c.Dims = Dims{L: 1, W: 1, H: 1} },
		"edges ceiling":    func(c *NewCarton) { c.Dims = Dims{L: MaxDimMm, W: MaxDimMm, H: MaxDimMm} },
		"quantity ceiling": func(c *NewCarton) { c.Contents[0].QtyEaches = MaxQtyEaches },
		"quantity 1":       func(c *NewCarton) { c.Contents[0].QtyEaches = 1 },
	} {
		s := newShipment(t)
		in := carton(t, 1, "item-1", g, 5)
		mut(&in)
		r := looseRules()
		r.PlanQty = map[string]int{"item-1": MaxQtyEaches}
		if _, err := s.AddCarton(in, r); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCartonWithMaxContentsLinesIsAccepted(t *testing.T) {
	s := newShipment(t)
	in := NewCarton{SSCC: mustSSCC(t, 1), GrossWeightG: 5000, Dims: Dims{L: 400, W: 300, H: 200}}
	plan := map[string]int{}
	for i := range MaxContents {
		item := fmt.Sprintf("item-%d", i)
		in.Contents = append(in.Contents, Content{CatalogItemID: item, GTIN: mustGTIN(t, fmt.Sprintf("0614141%06d", i)), QtyEaches: 1})
		plan[item] = 1
	}
	r := looseRules()
	r.PlanQty = plan
	if _, err := s.AddCarton(in, r); err != nil {
		t.Fatalf("MaxContents lines are allowed: %v", err)
	}
	in.SSCC = mustSSCC(t, 2)
	in.Contents = append(in.Contents, Content{CatalogItemID: "one-more", GTIN: mustGTIN(t, "0614141999999"), QtyEaches: 1})
	r.PlanQty["one-more"] = 1
	if _, err := s.AddCarton(in, r); err == nil {
		t.Fatal("MaxContents+1 lines must be refused")
	}
}

func TestRestoreAcceptsTheBoundaryValues(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	r := looseRules()
	r.PlanQty = map[string]int{"item-1": MaxCartons * 2}
	for i := 1; i <= MaxCartons; i++ {
		if _, err := s.AddCarton(carton(t, i, "item-1", g, 1), r); err != nil {
			t.Fatal(err)
		}
	}
	snap := s.Snapshot()
	snap.Version = 1
	if _, err := Restore(snap); err != nil {
		t.Fatalf("version 1 and MaxCartons cartons are valid: %v", err)
	}
}

func TestReceiptVersionAndEventVersionsFollowTheAggregate(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	before := s.Version()
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{
		{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 10}}, ComplianceCodes: []string{"LABEL_TORN"}},
		{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}},
	}}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	if s.Version() != before+1 {
		t.Fatalf("version %d, want %d", s.Version(), before+1)
	}
	if len(evs) != 3 {
		t.Fatalf("events %v", names(evs))
	}
	for _, e := range evs {
		if e.AggregateVersion() != s.Version() {
			t.Errorf("%s carries version %d, want %d", e.EventName(), e.AggregateVersion(), s.Version())
		}
	}
}

func TestDamagedCartonWithAShortCountRecordsBothFacts(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{
		{SSCC: a, Status: StatusDamaged, Items: []ReceiptItem{{g1, 9}}},
		{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}},
	}}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(names(evs)); got != "[InboundDiscrepancyRecorded InboundDiscrepancyRecorded InboundShipmentReceived]" {
		t.Fatalf("events %s", got)
	}
	if evs[0].(InboundDiscrepancyRecorded).Kind != KindDamaged || evs[1].(InboundDiscrepancyRecorded).Kind != KindShort {
		t.Fatalf("kinds %+v %+v", evs[0], evs[1])
	}
}

func TestAnItemCountedAsZeroIsNotAnOverage(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	extra := mustGTIN(t, "0614141000036")
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{
		{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 12}, {extra, 0}}},
		{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}},
	}}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(names(evs)); got != "[InboundShipmentReceived]" {
		t.Fatalf("a zero count is not a discrepancy: %s", got)
	}
}

func TestUnexpectedCartonsAreCappedAtTheShipmentLimit(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	var cartons []ReceiptCarton
	for i := 1; i <= maxUnexpected; i++ {
		cartons = append(cartons, ReceiptCarton{SSCC: mustSSCC(t, 100000+i), Status: StatusUnexpected})
	}
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: cartons}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	recorded := 0
	for _, e := range evs {
		if e.EventName() == "InboundDiscrepancyRecorded" {
			recorded++
		}
	}
	more, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{
		{SSCC: mustSSCC(t, 999999), Status: StatusUnexpected},
		{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 12}}},
		{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}},
	}}, at(7))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range more {
		if e.EventName() == "InboundDiscrepancyRecorded" {
			t.Fatalf("carton %d beyond the cap was recorded after %d", maxUnexpected+1, recorded)
		}
	}
	if recorded != maxUnexpected {
		t.Fatalf("recorded %d unexpected cartons, want %d", recorded, maxUnexpected)
	}
}

func TestRestoreChecksCartonsAgainstTheState(t *testing.T) {
	empty := newShipment(t).Snapshot()
	if _, err := Restore(empty); err != nil {
		t.Fatalf("a DRAFT shipment without cartons is valid: %v", err)
	}
	for _, st := range []State{Labelled, Shipped, Receiving, Closed, ClosedWithDiscrepancy} {
		sn := empty
		sn.State, sn.LabelledAt, sn.ShippedAt, sn.ReceivedAt = st, t0, t0, t0
		switch st {
		case Labelled:
			sn.ShippedAt, sn.ReceivedAt = emptyTime(), emptyTime()
		case Shipped, Receiving:
			sn.ReceivedAt = emptyTime()
		}
		if _, err := Restore(sn); err == nil {
			t.Errorf("%s without a carton must be refused", st)
		}
	}
}

func emptyTime() (z time.Time) { return z }
