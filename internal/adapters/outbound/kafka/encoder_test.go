package kafka

import (
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

const goldenID = "1b0c9a4e-2f7d-4a63-9d1e-5a7c3e2b9f10"

var t0 = time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)

func hdr() shared.Header { return shared.Header{Version: 2, At: t0} }

func encoder() *Encoder { return &Encoder{NewID: func() string { return goldenID }} }

// goldenEnvelope renders the exact CloudEvents 1.0 structured JSON the encoder
// must produce for one event; only the data object differs per event.
func goldenEnvelope(entity, event, subject, stream, data string) string {
	return fmt.Sprintf(`{"specversion":"1.0","id":%q,"source":"/retailnetwork/seller-inbound",`+
		`"type":"com.retailnetwork.partner.seller-inbound.%s.%s","subject":%q,`+
		`"datacontenttype":"application/json","dataschema":"urn:retailnetwork:seller-inbound:%s:%s:v1",`+
		`"time":"2026-10-24T13:00:00Z","data":%s}`, goldenID, entity, event, subject, stream, event, data)
}

const (
	sscc1 = "006141410000000012"
	gtinC = "00614141000036"
)

func labelledCartons() []inboundshipment.Carton {
	return []inboundshipment.Carton{{SSCC: sscc1, ParentSSCC: "006141410000000104", Kind: inboundshipment.HomogeneousCase, GrossWeightG: 5200,
		Dims:     inboundshipment.Dims{L: 400, W: 300, H: 200},
		Contents: []inboundshipment.Content{{CatalogItemID: "item-1", GTIN: gtinC, QtyEaches: 12, Lot: "L7", Expiry: "2027-10-24"}}}}
}

// TestEncoder_GoldenWireFormatOfEveryPublishedEvent pins the exact bytes of
// every published event with its topic and key.
func TestEncoder_GoldenWireFormatOfEveryPublishedEvent(t *testing.T) {
	cases := []struct {
		ev                                shared.Event
		entity, event, key, stream, topic string
		data                              string
	}{
		{seller.SellerOnboarded{Header: hdr(), SellerID: "S-1", Name: "Acme Goods"}, "seller", "SellerOnboarded", "S-1", "analytics", TopicAnalytics,
			`{"sellerId":"S-1","name":"Acme Goods","onboardedAt":"2026-10-24T13:00:00Z"}`},
		{seller.SellerSuspended{Header: hdr(), SellerID: "S-1", Reason: "late shipments"}, "seller", "SellerSuspended", "S-1", "analytics", TopicAnalytics,
			`{"sellerId":"S-1","suspendedAt":"2026-10-24T13:00:00Z","reason":"late shipments"}`},
		{seller.SellerSuspended{Header: hdr(), SellerID: "S-1"}, "seller", "SellerSuspended", "S-1", "analytics", TopicAnalytics,
			`{"sellerId":"S-1","suspendedAt":"2026-10-24T13:00:00Z"}`},
		{seller.SellerReinstated{Header: hdr(), SellerID: "S-1"}, "seller", "SellerReinstated", "S-1", "analytics", TopicAnalytics,
			`{"sellerId":"S-1","reinstatedAt":"2026-10-24T13:00:00Z"}`},
		{inboundplan.InboundPlanConfirmed{Header: hdr(), PlanID: "pln-1", SellerID: "S-1", DestinationNodeID: "n-1", Packing: inboundplan.CasePacked,
			Lines: []inboundplan.Line{{CatalogItemID: "item-1", GTIN: gtinC, QtyEaches: 120}}}, "inboundplan", "InboundPlanConfirmed", "pln-1", "analytics", TopicAnalytics,
			`{"planId":"pln-1","sellerId":"S-1","destinationNodeId":"n-1","packingOption":"CASE_PACKED",` +
				`"lines":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":120}],"confirmedAt":"2026-10-24T13:00:00Z"}`},
		{inboundshipment.InboundShipmentLabelled{Header: hdr(), ShipmentID: "shp-1", PlanID: "pln-1", SellerID: "S-1", NodeID: "n-1", Cartons: labelledCartons()},
			"inboundshipment", "InboundShipmentLabelled", "shp-1", "events", TopicEvents,
			`{"shipmentId":"shp-1","planId":"pln-1","sellerId":"S-1","nodeId":"n-1","cartons":[{"sscc":"006141410000000012","parentSscc":"006141410000000104",` +
				`"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":"00614141000036","qtyEaches":12,"lot":"L7","expiry":"2027-10-24"}],` +
				`"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}],"labelledAt":"2026-10-24T13:00:00Z"}`},
		{inboundshipment.InboundShipmentShipped{Header: hdr(), ShipmentID: "shp-1", PlanID: "pln-1", SellerID: "S-1", NodeID: "n-1"},
			"inboundshipment", "InboundShipmentShipped", "shp-1", "events", TopicEvents,
			`{"shipmentId":"shp-1","planId":"pln-1","sellerId":"S-1","nodeId":"n-1","shippedAt":"2026-10-24T13:00:00Z"}`},
		{inboundshipment.InboundShipmentReceived{Header: hdr(), ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", Status: inboundshipment.ClosedWithDiscrepancy,
			Cartons:    []inboundshipment.ReceivedCarton{{SSCC: sscc1, Status: inboundshipment.StatusDamaged}},
			ReceivedAt: t0.Add(-time.Hour)}, "inboundshipment", "InboundShipmentReceived", "shp-1", "events", TopicEvents,
			`{"shipmentId":"shp-1","sellerId":"S-1","nodeId":"n-1","status":"CLOSED_WITH_DISCREPANCY",` +
				`"cartons":[{"sscc":"006141410000000012","status":"DAMAGED"}],"receivedAt":"2026-10-24T12:00:00Z"}`},
		{inboundshipment.InboundDiscrepancyRecorded{Header: hdr(), ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", SSCC: sscc1, Kind: "Short",
			Expected: gtinC + ":12", Actual: gtinC + ":10"}, "inboundshipment", "InboundDiscrepancyRecorded", "shp-1", "analytics", TopicAnalytics,
			`{"shipmentId":"shp-1","sellerId":"S-1","nodeId":"n-1","sscc":"006141410000000012","kind":"Short",` +
				`"expected":"00614141000036:12","actual":"00614141000036:10","recordedAt":"2026-10-24T13:00:00Z"}`},
		{inboundshipment.InboundDiscrepancyRecorded{Header: hdr(), ShipmentID: "shp-1", SellerID: "S-1", NodeID: "n-1", Kind: "MissingCarton"},
			"inboundshipment", "InboundDiscrepancyRecorded", "shp-1", "analytics", TopicAnalytics,
			`{"shipmentId":"shp-1","sellerId":"S-1","nodeId":"n-1","kind":"MissingCarton","recordedAt":"2026-10-24T13:00:00Z"}`},
		{inboundshipment.SellerComplianceViolationRecorded{Header: hdr(), SellerID: "S-1", ShipmentID: "shp-1", SSCC: sscc1, Code: "LABEL_SKEW"},
			"seller", "SellerComplianceViolationRecorded", "S-1", "analytics", TopicAnalytics,
			`{"sellerId":"S-1","shipmentId":"shp-1","code":"LABEL_SKEW","recordedAt":"2026-10-24T13:00:00Z"}`},
	}
	for _, c := range cases {
		msgs, err := encoder().Encode(c.ev)
		if err != nil || len(msgs) != 1 {
			t.Fatalf("%s: Encode: %v %d", c.event, err, len(msgs))
		}
		m := msgs[0]
		if want := goldenEnvelope(c.entity, c.event, c.key, c.stream, c.data); string(m.Value) != want {
			t.Errorf("%s value =\n%s\nwant\n%s", c.event, m.Value, want)
		}
		if m.EventID != goldenID || m.Topic != c.topic || string(m.Key) != c.key || m.Subject != c.key ||
			m.EventType != "com.retailnetwork.partner.seller-inbound."+c.entity+"."+c.event ||
			m.DataSchema != "urn:retailnetwork:seller-inbound:"+c.stream+":"+c.event+":v1" ||
			len(m.Headers) != 1 || m.Headers[0].Key != "content-type" || m.Headers[0].Value != cloudevents.MediaType {
			t.Errorf("%s row = %+v", c.event, m)
		}
	}
}

type unpublished struct{ shared.Header }

func (unpublished) EventName() string { return "Nope" }
func (unpublished) Entity() string    { return "x" }
func (unpublished) SubjectID() string { return "x" }

func TestEncoder_RefusesAnUnpublishedEvent(t *testing.T) {
	if _, err := encoder().Encode(unpublished{hdr()}); err == nil {
		t.Fatal("an event the context does not publish must fail the whole call")
	}
}

func TestEncoder_DefaultsMintDistinctUUIDs(t *testing.T) {
	msgs, err := NewEncoder().Encode(seller.SellerReinstated{Header: hdr(), SellerID: "S-1"}, seller.SellerReinstated{Header: hdr(), SellerID: "S-1"})
	if err != nil || len(msgs) != 2 || msgs[0].EventID == msgs[1].EventID || len(msgs[0].EventID) != 36 {
		t.Fatalf("%v %+v", err, msgs)
	}
	zero := (&Encoder{})
	if m, err := zero.Encode(seller.SellerReinstated{Header: hdr(), SellerID: "S-1"}); err != nil || len(m[0].EventID) != 36 {
		t.Fatalf("nil NewID falls back to uuid: %v", err)
	}
}

func TestEncoder_EmptySubjectIsRefused(t *testing.T) {
	if _, err := encoder().Encode(seller.SellerReinstated{Header: hdr()}); err == nil {
		t.Fatal("empty subject")
	}
}
