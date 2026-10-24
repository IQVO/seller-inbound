package scorecard

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func hdr(at time.Time) shared.Header { return shared.Header{Version: 3, At: at} }

func TestFactsFromReceiptRaisedEvents(t *testing.T) {
	at := time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)
	later := at.Add(time.Minute)
	evs := []shared.Event{
		inboundshipment.InboundDiscrepancyRecorded{Header: hdr(at), ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", SSCC: "s1", Kind: inboundshipment.KindShort},
		inboundshipment.SellerComplianceViolationRecorded{Header: hdr(at), SellerID: "S-1", ShipmentID: "shp-1", SSCC: "s1", Code: "LABEL_TORN"},
		inboundshipment.InboundShipmentLabelled{Header: hdr(at), ShipmentID: "shp-1", SellerID: "S-1"},
		inboundshipment.InboundShipmentReceived{
			Header: hdr(later), ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", Status: inboundshipment.ClosedWithDiscrepancy,
			Cartons: []inboundshipment.ReceivedCarton{
				{SSCC: "s1", Status: inboundshipment.StatusReceived},
				{SSCC: "s2", Status: inboundshipment.StatusDamaged},
				{SSCC: "s3", Status: inboundshipment.StatusMissing},
				{SSCC: "s4", Status: inboundshipment.StatusReceived},
				{SSCC: "s5", Status: inboundshipment.StatusUnexpected},
			},
			ReceivedAt: at.Add(-48 * time.Hour), // the vendor's claim is not the window instant
		},
	}
	want := Facts{
		Receipts: []Receipt{{SellerID: "S-1", ShipmentID: "shp-1", CartonsReported: 5, CartonsReceived: 2, At: later}},
		Occurrences: []Occurrence{
			{SellerID: "S-1", ShipmentID: "shp-1", Type: Discrepancy, Code: "Short", At: at},
			{SellerID: "S-1", ShipmentID: "shp-1", Type: Violation, Code: "LABEL_TORN", At: at},
		},
	}
	got := FactsFrom(evs)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if got.Empty() {
		t.Fatal("facts are not empty")
	}
}

func TestFactsFromNothingIsEmpty(t *testing.T) {
	for _, evs := range [][]shared.Event{nil, {}, {inboundshipment.InboundShipmentShipped{Header: hdr(now), ShipmentID: "shp-1", SellerID: "S-1"}}} {
		f := FactsFrom(evs)
		if !f.Empty() || f.Receipts != nil || f.Occurrences != nil {
			t.Errorf("%+v", f)
		}
	}
	if (Facts{Receipts: []Receipt{{}}}).Empty() || (Facts{Occurrences: []Occurrence{{}}}).Empty() {
		t.Error("either list makes the facts non-empty")
	}
}

func TestFactsFromAReceiptWithoutCartons(t *testing.T) {
	f := FactsFrom([]shared.Event{inboundshipment.InboundShipmentReceived{Header: hdr(now), ShipmentID: "shp-2", SellerID: "S-2", Status: inboundshipment.Closed}})
	if len(f.Receipts) != 1 || f.Receipts[0].CartonsReported != 0 || f.Receipts[0].CartonsReceived != 0 || f.Receipts[0].SellerID != "S-2" {
		t.Fatalf("%+v", f)
	}
}
