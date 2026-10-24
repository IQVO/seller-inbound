package inboundshipment

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// shippedShipment returns a SHIPPED shipment of two cartons: A (12 eaches of g1)
// and B (6 of g1 and 3 of g2).
func shippedShipment(t *testing.T) (*Shipment, string, string, string, string) {
	t.Helper()
	g1, g2 := mustGTIN(t, "0614141000012"), mustGTIN(t, "0614141000029")
	s := newShipment(t)
	rules := looseRules()
	rules.PlanQty = map[string]int{"item-1": 18, "item-2": 3}
	if _, err := s.AddCarton(carton(t, 1, "item-1", g1, 12), rules); err != nil {
		t.Fatal(err)
	}
	b := carton(t, 2, "item-1", g1, 6)
	b.Contents = append(b.Contents, Content{CatalogItemID: "item-2", GTIN: g2, QtyEaches: 3})
	if _, err := s.AddCarton(b, rules); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Label(PlanRules{PlanQty: rules.PlanQty, SingleShipment: true}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ship(t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return s, mustSSCC(t, 1), mustSSCC(t, 2), g1, g2
}

func at(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

func names(evs []shared.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventName()
	}
	return out
}

func TestReceiptAllReceivedClosesClean(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{
		{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{GTIN: g1, QtyEaches: 12}}},
		{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{GTIN: g1, QtyEaches: 6}, {GTIN: g2, QtyEaches: 3}}},
	}}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(evs); !reflect.DeepEqual(got, []string{"InboundShipmentReceived"}) {
		t.Fatalf("events %v", got)
	}
	e := evs[0].(InboundShipmentReceived)
	if e.Status != Closed || s.State() != Closed || !e.ReceivedAt.Equal(at(5)) || len(e.Cartons) != 2 || e.Cartons[0].Status != StatusReceived ||
		e.AggregateVersion() != s.Version() || e.Entity() != Entity || e.SubjectID() != "shp-1" || !e.OccurredAt().Equal(at(6)) {
		t.Fatalf("closing event %+v state %s", e, s.State())
	}
	again, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(7), Cartons: []ReceiptCarton{{SSCC: a, Status: StatusMissing}}}, at(8))
	if err != nil || again != nil {
		t.Fatalf("a receipt for a closed shipment must be a no-op: %v %v", err, again)
	}
}

func TestReceiptDiscrepancies(t *testing.T) {
	g1, g2 := mustGTIN(t, "0614141000012"), mustGTIN(t, "0614141000029")
	tests := []struct {
		name       string
		a, b       func(sscc string) ReceiptCarton
		wantEvents []string
		kinds      []string
		state      State
	}{
		{"missing carton",
			func(s string) ReceiptCarton { return ReceiptCarton{SSCC: s, Status: StatusMissing} },
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}}
			},
			[]string{"InboundDiscrepancyRecorded", "InboundShipmentReceived"}, []string{KindMissingCarton}, ClosedWithDiscrepancy},
		{"damaged carton",
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusDamaged, Items: []ReceiptItem{{g1, 12}}}
			},
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}}
			},
			[]string{"InboundDiscrepancyRecorded", "InboundShipmentReceived"}, []string{KindDamaged}, ClosedWithDiscrepancy},
		{"short and over counts",
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 10}}}
			},
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 7}, {g2, 3}}}
			},
			[]string{"InboundDiscrepancyRecorded", "InboundDiscrepancyRecorded", "InboundShipmentReceived"}, []string{KindShort, KindOver}, ClosedWithDiscrepancy},
		{"a counted gtin that is not in the manifest",
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 12}, {g2, 2}}}
			},
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}}
			},
			[]string{"InboundDiscrepancyRecorded", "InboundShipmentReceived"}, []string{KindOver}, ClosedWithDiscrepancy},
		{"a manifest gtin counted as zero",
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 12}}}
			},
			func(s string) ReceiptCarton {
				return ReceiptCarton{SSCC: s, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}}}
			},
			[]string{"InboundDiscrepancyRecorded", "InboundShipmentReceived"}, []string{KindShort}, ClosedWithDiscrepancy},
		{"no count reported is not compared",
			func(s string) ReceiptCarton { return ReceiptCarton{SSCC: s, Status: StatusReceived} },
			func(s string) ReceiptCarton { return ReceiptCarton{SSCC: s, Status: StatusReceived} },
			[]string{"InboundShipmentReceived"}, nil, Closed},
		{"status unexpected on a manifest carton",
			func(s string) ReceiptCarton { return ReceiptCarton{SSCC: s, Status: StatusUnexpected} },
			func(s string) ReceiptCarton { return ReceiptCarton{SSCC: s, Status: StatusReceived} },
			[]string{"InboundDiscrepancyRecorded", "InboundShipmentReceived"}, []string{KindUnexpectedCarton}, ClosedWithDiscrepancy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { runDiscrepancyCase(t, tc.a, tc.b, tc.wantEvents, tc.kinds, tc.state) })
	}
}

func runDiscrepancyCase(t *testing.T, fa, fb func(string) ReceiptCarton, wantEvents, wantKinds []string, wantState State) {
	t.Helper()
	s, a, b, _, _ := shippedShipment(t)
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{fa(a), fb(b)}}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(evs); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("events %v, want %v", got, wantEvents)
	}
	var kinds []string
	for _, e := range evs {
		if d, ok := e.(InboundDiscrepancyRecorded); ok {
			kinds = append(kinds, d.Kind)
			if d.NodeID != "node-1" || d.SellerID != "seller-1" || d.ShipmentID != "shp-1" || d.SSCC == "" || d.Entity() != Entity || d.SubjectID() != "shp-1" {
				t.Errorf("discrepancy %+v", d)
			}
		}
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds %v, want %v", kinds, wantKinds)
	}
	if s.State() != wantState {
		t.Fatalf("state %s, want %s", s.State(), wantState)
	}
}

func TestQuantityDiscrepancyCarriesExpectedAndActual(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{
		{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 10}}},
		{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}},
	}}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	d := evs[0].(InboundDiscrepancyRecorded)
	if d.Kind != KindShort || d.Expected != g1+":12" || d.Actual != g1+":10" || d.SSCC != a {
		t.Fatalf("%+v", d)
	}
}

func TestPartialReceiptThenCompletion(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 12}}}}}, at(5))
	if err != nil || len(evs) != 0 || s.State() != Receiving {
		t.Fatalf("partial: %v %v %s", err, evs, s.State())
	}
	// A repeated answer for A is ignored: the first one is final.
	evs, err = s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(6), Cartons: []ReceiptCarton{{SSCC: a, Status: StatusMissing}}}, at(6))
	if err != nil || len(evs) != 0 || s.State() != Receiving {
		t.Fatalf("repeat: %v %v %s", err, evs, s.State())
	}
	evs, err = s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(7), Cartons: []ReceiptCarton{{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}}}}, at(7))
	if err != nil || s.State() != Closed || len(evs) != 1 {
		t.Fatalf("complete: %v %v %s", err, evs, s.State())
	}
	if got := evs[0].(InboundShipmentReceived).ReceivedAt; !got.Equal(at(7)) {
		t.Fatalf("receivedAt %v", got)
	}
}

func TestUnexpectedCartonAndComplianceCodes(t *testing.T) {
	s, a, b, g1, g2 := shippedShipment(t)
	stray := mustSSCC(t, 77)
	evs, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{
		{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 12}}, ComplianceCodes: []string{"LABEL_SKEW", "LABEL_SKEW", "", "NO_LOT"}},
		{SSCC: b, Status: StatusReceived, Items: []ReceiptItem{{g1, 6}, {g2, 3}}},
		{SSCC: stray, Status: StatusUnexpected, ComplianceCodes: []string{"NO_LOT"}},
		{SSCC: mustSSCC(t, 78), Status: StatusMissing},
	}}, at(6))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SellerComplianceViolationRecorded", "SellerComplianceViolationRecorded", "InboundDiscrepancyRecorded",
		"SellerComplianceViolationRecorded", "SellerComplianceViolationRecorded", "InboundShipmentReceived"}
	if got := names(evs); !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v\nwant %v", got, want)
	}
	v := evs[0].(SellerComplianceViolationRecorded)
	if v.Entity() != SellerEntity || v.SubjectID() != "seller-1" || v.Code != "LABEL_SKEW" || v.ShipmentID != "shp-1" || v.SSCC != a {
		t.Fatalf("%+v", v)
	}
	if c := evs[3].(SellerComplianceViolationRecorded).Code; c != CodeUnmanifestedCarton {
		t.Fatalf("derived code %q", c)
	}
	closing := evs[5].(InboundShipmentReceived)
	if closing.Status != ClosedWithDiscrepancy || len(closing.Cartons) != 3 || closing.Cartons[2].SSCC != stray || closing.Cartons[2].Status != StatusUnexpected {
		t.Fatalf("closing %+v", closing)
	}
	// The same stray carton in a later receipt is not recorded twice.
	if evs, _ := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(9), Cartons: []ReceiptCarton{{SSCC: stray, Status: StatusUnexpected}}}, at(9)); evs != nil {
		t.Fatalf("closed shipment: %v", evs)
	}
}

func TestUnexpectedCartonIsRecordedOnce(t *testing.T) {
	s, a, _, g1, _ := shippedShipment(t)
	stray := mustSSCC(t, 77)
	r := Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{{SSCC: stray, Status: StatusUnexpected}, {SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 12}}}}}
	first, err := s.RecordReceipt(r, at(5))
	if err != nil || len(first) != 2 {
		t.Fatalf("first: %v %v", err, names(first))
	}
	second, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(6), Cartons: []ReceiptCarton{{SSCC: stray, Status: StatusUnexpected}}}, at(6))
	if err != nil || len(second) != 0 {
		t.Fatalf("second: %v %v", err, names(second))
	}
}

func TestReceiptValidation(t *testing.T) {
	s, a, _, g1, _ := shippedShipment(t)
	bad := map[string]Receipt{
		"other node":    {NodeID: "node-2", Cartons: []ReceiptCarton{{SSCC: a, Status: StatusReceived}}},
		"bad sscc":      {NodeID: "node-1", Cartons: []ReceiptCarton{{SSCC: "1", Status: StatusReceived}}},
		"bad status":    {NodeID: "node-1", Cartons: []ReceiptCarton{{SSCC: a, Status: "LOST"}}},
		"repeated sscc": {NodeID: "node-1", Cartons: []ReceiptCarton{{SSCC: a, Status: StatusReceived}, {SSCC: a, Status: StatusReceived}}},
		"bad item gtin": {NodeID: "node-1", Cartons: []ReceiptCarton{{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{"1", 1}}}}},
		"negative qty":  {NodeID: "node-1", Cartons: []ReceiptCarton{{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, -1}}}}},
	}
	for name, r := range bad {
		r.ReceivedAt = at(5)
		before := s.Version()
		if _, err := s.RecordReceipt(r, at(5)); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
		if s.Version() != before {
			t.Errorf("%s: a refused receipt changed the shipment", name)
		}
	}
	zero := Receipt{NodeID: "node-1", ReceivedAt: at(5), Cartons: []ReceiptCarton{{SSCC: a, Status: StatusReceived, Items: []ReceiptItem{{g1, 0}}}}}
	if _, err := s.RecordReceipt(zero, at(5)); err != nil {
		t.Errorf("a zero count is valid: %v", err)
	}
}

func TestReceiptNeedsShippedShipment(t *testing.T) {
	for _, st := range []State{Draft, Labelled} {
		s := newShipment(t)
		if st == Labelled {
			g := mustGTIN(t, "0614141000012")
			_, _ = s.AddCarton(carton(t, 1, "item-1", g, 1), looseRules())
			_, _ = s.Label(PlanRules{}, t0)
		}
		_, err := s.RecordReceipt(Receipt{NodeID: "node-1", ReceivedAt: at(1)}, at(1))
		if !errors.Is(err, shared.ErrIllegalTransition) {
			t.Errorf("%s: %v", st, err)
		}
	}
}

func TestReceiptStatusValid(t *testing.T) {
	for _, st := range []ReceiptStatus{StatusReceived, StatusMissing, StatusUnexpected, StatusDamaged} {
		if !st.Valid() {
			t.Errorf("%s", st)
		}
	}
	for _, st := range []ReceiptStatus{"", "received", "LOST"} {
		if st.Valid() {
			t.Errorf("%q", st)
		}
	}
}
