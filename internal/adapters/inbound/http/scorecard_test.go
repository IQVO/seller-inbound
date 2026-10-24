package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/claudioed/seller-inbound/internal/application/requests"
)

func TestScorecardRoute(t *testing.T) {
	a := newAPI(t)
	a.seller()

	empty := a.mustCode(http.MethodGet, "/sellers/S-1/scorecard", "", 200)
	if empty["window"] != "30d" || empty["complianceTier"] != "GOOD" || empty["shipmentsReceived"] != float64(0) ||
		empty["windowEnd"] != "2026-10-24T12:00:00Z" || empty["windowStart"] != "2026-09-24T12:00:00Z" {
		t.Fatalf("%v", empty)
	}
	if _, has := empty["receiptAccuracyRate"]; has {
		t.Fatalf("a zero-denominator rate must be omitted, not null or NaN: %v", empty)
	}
	if _, has := empty["discrepancyRatePerShipment"]; has {
		t.Fatalf("%v", empty)
	}
	if byKind, ok := empty["discrepanciesByKind"].(map[string]any); !ok || len(byKind) != 0 {
		t.Fatalf("discrepanciesByKind must be an empty object: %v", empty)
	}

	for window, start := range map[string]string{"7d": "2026-10-17T12:00:00Z", "30d": "2026-09-24T12:00:00Z", "": "2026-09-24T12:00:00Z"} {
		got := a.mustCode(http.MethodGet, "/sellers/S-1/scorecard?window="+window, "", 200)
		if got["windowStart"] != start {
			t.Errorf("window %q: windowStart %v, want %s", window, got["windowStart"], start)
		}
	}
}

func TestScorecardProblems(t *testing.T) {
	a := newAPI(t)
	a.seller()
	for _, q := range []string{"?window=90d", "?window=1d", "?window=7D", "?window=7", "?window=%20", "?window=7d%20"} {
		a.do(http.MethodGet, "/sellers/S-1/scorecard"+q, "").problem(t, 400, "invalid-window")
	}
	a.do(http.MethodGet, "/sellers/ghost/scorecard", "").problem(t, 404, "not-found")
	a.do(http.MethodGet, "/sellers/ghost/scorecard?window=90d", "").problem(t, 400, "invalid-window")
	// The scorecard is a read: there is no write on it.
	if r := a.do(http.MethodPost, "/sellers/S-1/scorecard", ""); r.code != http.StatusNotFound && r.code != http.StatusMethodNotAllowed {
		t.Fatalf("POST on the scorecard: %d %s", r.code, r.body)
	}
}

// TestScorecardAnswersTheRecordedFacts drives a shipment through the REST API,
// closes it with a vendor receipt and reads the scorecard back.
func TestScorecardAnswersTheRecordedFacts(t *testing.T) {
	a := newAPI(t)
	a.seller()
	plan := a.confirmedPlan("CASE_PACKED", gtinCase, 12)
	id := a.mustCode(http.MethodPost, "/inbound-plans/"+plan+"/shipments", "", 201)["shipmentId"].(string)
	carton := a.mustCode(http.MethodPost, "/inbound-shipments/"+id+"/cartons", cartonBody("", 12, ""), 201)["carton"].(map[string]any)["sscc"].(string)
	a.mustCode(http.MethodPost, "/inbound-shipments/"+id+":label", "", 200)
	a.mustCode(http.MethodPost, "/inbound-shipments/"+id+":ship", "", 200)
	err := a.svc.ApplyInboundReceipt(context.Background(), "rcpt-1", requests.InboundReceipt{ShipmentID: id, NodeID: "n-1", ReceivedAt: t0,
		Cartons: []requests.ReceiptCarton{{SSCC: carton, Status: "DAMAGED", Items: []requests.ReceiptItem{{GTIN: gtinCase, QtyEaches: 10}}, ComplianceCodes: []string{"CARTON_CRUSHED"}}}})
	if err != nil {
		t.Fatal(err)
	}
	got := a.mustCode(http.MethodGet, "/sellers/S-1/scorecard?window=7d", "", 200)
	byKind, _ := got["discrepanciesByKind"].(map[string]any)
	if got["shipmentsReceived"] != float64(1) || got["cartonsReported"] != float64(1) || got["cartonsReceived"] != float64(0) ||
		got["receiptAccuracyRate"] != float64(0) || got["discrepancyRatePerShipment"] != float64(2) || got["complianceViolations"] != float64(1) ||
		got["complianceTier"] != "AT_RISK" || len(byKind) != 2 || byKind["Damaged"] != float64(1) || byKind["Short"] != float64(1) {
		t.Fatalf("%v", got)
	}
	// Informational: the seller can still be read and is still ACTIVE.
	if s := a.mustCode(http.MethodPost, "/sellers/S-1:reinstate", "", 200); s["state"] != "ACTIVE" {
		t.Fatalf("%v", s)
	}
	checkAgainstContract(t, got)
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkAgainstContract holds the wire shape to apis/openapi.yaml: a live answer
// with every field present has exactly the schema's properties, the required
// ones are always present, and the documented examples have those same fields.
func checkAgainstContract(t *testing.T, live map[string]any) {
	t.Helper()
	raw, err := os.ReadFile("../../../../apis/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schema := dig(t, spec, "components", "schemas", "SellerScorecard")
	props := keysOf(dig(t, schema, "properties"))
	if got := keysOf(live); !slices.Equal(got, props) {
		t.Errorf("live fields %v differ from the schema properties %v", got, props)
	}
	examples := dig(t, spec, "paths", "/sellers/{sellerId}/scorecard", "get", "responses", "200", "content", "application/json", "examples")
	if len(examples) != 2 {
		t.Fatalf("expected the two documented examples, got %d", len(examples))
	}
	for name, ex := range examples {
		value := dig(t, ex.(map[string]any), "value")
		body, _ := json.Marshal(value)
		var back map[string]any
		if err := json.Unmarshal(body, &back); err != nil {
			t.Fatal(err)
		}
		for _, req := range schema["required"].([]any) {
			if _, ok := back[req.(string)]; !ok {
				t.Errorf("example %s lacks the required field %s", name, req)
			}
		}
		for k := range back {
			if !slices.Contains(props, k) {
				t.Errorf("example %s has the undocumented field %s", name, k)
			}
		}
	}
}

func dig(t *testing.T, m map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := m
	for _, p := range path {
		next, ok := cur[p].(map[string]any)
		if !ok {
			t.Fatalf("openapi.yaml has no %v", path)
		}
		cur = next
	}
	return cur
}
