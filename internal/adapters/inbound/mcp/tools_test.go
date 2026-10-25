package mcp_test

import (
	"testing"
)

// The scorecard tool answers the window the REST GET answers: the default
// (omitted window) is 30d, an explicit 7d is honoured, a seller with no
// closed shipment has omitted rates, an always-present kind map and the GOOD
// tier.
func TestGetSellerScorecardAnswersTheWindow(t *testing.T) {
	h, w := withWorld(t)

	got := h.ok(t, "get_seller_scorecard", map[string]any{"sellerId": w.sellerID})
	assertScorecard(t, got, w.sellerID, "30d")

	got7d := h.ok(t, "get_seller_scorecard", map[string]any{"sellerId": w.sellerID, "window": "7d"})
	assertScorecard(t, got7d, w.sellerID, "7d")
}

// assertScorecard checks one scorecard result of a seller with no receipt
// facts recorded.
func assertScorecard(t *testing.T, got map[string]any, sellerID, window string) {
	t.Helper()
	if got["sellerId"] != sellerID || got["window"] != window {
		t.Fatalf("scorecard identity = %v %v, want %s %s", got["sellerId"], got["window"], sellerID, window)
	}
	if got["complianceTier"] != "GOOD" || got["shipmentsReceived"] != 0.0 || got["cartonsReported"] != 0.0 {
		t.Fatalf("scorecard counts = %v", got)
	}
	if _, hasRate := got["receiptAccuracyRate"]; hasRate {
		t.Fatalf("receiptAccuracyRate must be omitted when nothing was reported: %v", got)
	}
	if _, hasRate := got["discrepancyRatePerShipment"]; hasRate {
		t.Fatalf("discrepancyRatePerShipment must be omitted when no shipment closed: %v", got)
	}
	kind, isMap := got["discrepanciesByKind"].(map[string]any)
	if !isMap || len(kind) != 0 {
		t.Fatalf("discrepanciesByKind = %#v, want an always-present empty map", got["discrepanciesByKind"])
	}
	if _, ok := got["windowStart"].(string); !ok {
		t.Fatalf("windowStart = %v, want an RFC 3339 instant", got["windowStart"])
	}
}

func TestGetSellerScorecardErrors(t *testing.T) {
	h, w := withWorld(t)
	failWith(t, h.session, "get_seller_scorecard", map[string]any{"sellerId": "ghost"}, "not-found")
	failWith(t, h.session, "get_seller_scorecard", map[string]any{"sellerId": w.sellerID, "window": "90d"}, "invalid-window")
}

func TestGetInboundPlanReturnsPlanAndLines(t *testing.T) {
	h, w := withWorld(t)
	got := h.ok(t, "get_inbound_plan", map[string]any{"planId": w.planID})
	if got["planId"] != w.planID || got["sellerId"] != w.sellerID || got["destinationNodeId"] != "node-1" ||
		got["packingOption"] != "CASE_PACKED" || got["state"] != "CONFIRMED" {
		t.Fatalf("get_inbound_plan = %v", got)
	}
	lines, _ := got["lines"].([]any)
	if len(lines) != 1 {
		t.Fatalf("lines = %v, want the one declared line", got["lines"])
	}
	line, _ := lines[0].(map[string]any)
	if line["catalogItemId"] != "item-1" || line["qtyEaches"] != 24.0 {
		t.Fatalf("line = %v", line)
	}
	if _, ok := got["confirmedAt"].(string); !ok {
		t.Fatalf("confirmedAt = %v, want an RFC 3339 instant", got["confirmedAt"])
	}
	if _, has := got["cancelledAt"]; has {
		t.Fatalf("cancelledAt must be omitted on a CONFIRMED plan: %v", got)
	}
}

func TestGetInboundPlanErrors(t *testing.T) {
	h, _ := withWorld(t)
	failWith(t, h.session, "get_inbound_plan", map[string]any{"planId": "pln-nope"}, "not-found")
}

func TestGetInboundShipmentReturnsCartons(t *testing.T) {
	h, w := withWorld(t)
	got := h.ok(t, "get_inbound_shipment", map[string]any{"shipmentId": w.shipmentID})
	assertShipmentIdentity(t, got, w)
	assertShipmentCartons(t, got, w)
}

// assertShipmentIdentity checks the shipment's own scalars and instants.
func assertShipmentIdentity(t *testing.T, got map[string]any, w world) {
	t.Helper()
	for k, v := range map[string]any{
		"shipmentId": w.shipmentID, "planId": w.planID, "sellerId": w.sellerID, "nodeId": "node-1", "state": "LABELLED",
	} {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["labelledAt"].(string); !ok {
		t.Errorf("labelledAt = %v, want an RFC 3339 instant", got["labelledAt"])
	}
}

// assertShipmentCartons checks the two packed cartons and their optional
// fields.
func assertShipmentCartons(t *testing.T, got map[string]any, w world) {
	t.Helper()
	cartons, _ := got["cartons"].([]any)
	if len(cartons) != 2 {
		t.Fatalf("cartons = %v, want the two packed cartons", got["cartons"])
	}
	first, _ := cartons[0].(map[string]any)
	if first["sscc"] != w.cartonSSCCs[0] || first["kind"] != "HOMOGENEOUS_CASE" || first["grossWeightG"] != 5200.0 ||
		first["sealed"] != true {
		t.Fatalf("carton = %v", first)
	}
	dims, _ := first["dimsMm"].(map[string]any)
	if dims["l"] != 400.0 || dims["w"] != 300.0 || dims["h"] != 200.0 {
		t.Fatalf("dimsMm = %v", dims)
	}
	if _, has := first["parentSscc"]; has {
		t.Fatalf("parentSscc must be omitted on a carton without one: %v", first)
	}
}

func TestGetInboundShipmentErrors(t *testing.T) {
	h, _ := withWorld(t)
	failWith(t, h.session, "get_inbound_shipment", map[string]any{"shipmentId": "shp-nope"}, "not-found")
}

func TestGetShipmentLabelsReturnsLabelData(t *testing.T) {
	h, w := withWorld(t)
	got := h.ok(t, "get_shipment_labels", map[string]any{"shipmentId": w.shipmentID})
	if got["shipmentId"] != w.shipmentID || got["state"] != "LABELLED" || got["destinationNodeId"] != "node-1" {
		t.Fatalf("get_shipment_labels = %v", got)
	}
	labels, _ := got["labels"].([]any)
	if len(labels) != 2 {
		t.Fatalf("labels = %v, want one per carton", got["labels"])
	}
	first, _ := labels[0].(map[string]any)
	if first["labelRef"] != "LBL-"+w.cartonSSCCs[0] || first["sscc"] != w.cartonSSCCs[0] ||
		first["gs1128"] != "(00)"+w.cartonSSCCs[0] || first["kind"] != "HOMOGENEOUS_CASE" ||
		first["totalEaches"] != 12.0 || first["destinationNodeId"] != "node-1" {
		t.Fatalf("label = %v", first)
	}
	contents, _ := first["contents"].([]any)
	if len(contents) != 1 || contents[0].(map[string]any)["catalogItemId"] != "item-1" ||
		contents[0].(map[string]any)["qtyEaches"] != 12.0 {
		t.Fatalf("label contents = %v", contents)
	}
}

func TestGetShipmentLabelsErrors(t *testing.T) {
	h, _ := withWorld(t)
	failWith(t, h.session, "get_shipment_labels", map[string]any{"shipmentId": "shp-nope"}, "not-found")
}

// Reading through every tool emits nothing: the MCP surface cannot append an
// outbox event.
func TestReadsEmitNothing(t *testing.T) {
	h, w := withWorld(t)
	before := len(h.store.Outbox().Events())
	h.ok(t, "get_seller_scorecard", map[string]any{"sellerId": w.sellerID})
	h.ok(t, "get_inbound_plan", map[string]any{"planId": w.planID})
	h.ok(t, "get_shipment_labels", map[string]any{"shipmentId": w.shipmentID})
	h.ok(t, "get_inbound_shipment", map[string]any{"shipmentId": w.shipmentID})
	if after := len(h.store.Outbox().Events()); after != before {
		t.Fatalf("events %d -> %d: a read tool emitted an event", before, after)
	}
}
