package http_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/claudioed/seller-inbound/internal/application/requests"
)

// declareVolume gives node n-1 a declared monthly inbound volume (ADR 0015).
func declareVolume(t *testing.T, a *api, monthlyVolumeDm3 int) {
	t.Helper()
	if err := a.svc.ApplyNodeProfileDeclared(context.Background(), "ev-prof-cap",
		requests.NodeProfileDeclared{NodeID: "n-1", ProfileRevision: 1,
			Inbound: &requests.NodeInbound{MonthlyVolumeDm3: monthlyVolumeDm3}}); err != nil {
		t.Fatal(err)
	}
}

// volBody is one INDIVIDUAL line of qty x unitVolumeDm3.
func volBody(qty, unitVolumeDm3 int) string {
	return `{"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"` +
		gtinEach + `","qtyEaches":` + itoa(qty) + `,"unitVolumeDm3":` + itoa(unitVolumeDm3) + `}]}`
}

// TestCreatePlanCarriesTheCapacityFacts pins the wire of ADR 0015: the created
// plan echoes the placement facts and the reservation read answers the volume.
func TestCreatePlanCarriesTheCapacityFacts(t *testing.T) {
	a := newAPI(t)
	a.seller()
	declareVolume(t, a, 1000)
	v := a.mustCode(http.MethodPost, "/inbound-plans", volBody(3, 100), 201)
	if v["placementOption"] != "SELLER_SPLIT" || v["capacityEnforced"] != true {
		t.Fatalf("plan = %v", v)
	}
	allocs := a.mustCode(http.MethodGet, "/sellers/S-1/capacity-allocations?month=2026-10", "", 200)
	items := allocs["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["nodeId"] != "n-1" || items[0].(map[string]any)["reservedDm3"] != float64(300) {
		t.Fatalf("allocations = %v", allocs)
	}
	// an unknown seller is 404, a malformed month is 422
	a.do(http.MethodGet, "/sellers/nobody/capacity-allocations", "").problem(t, 404, "not-found")
	a.do(http.MethodGet, "/sellers/S-1/capacity-allocations?month=2026-13", "").problem(t, 422, "validation-failed")
}

// TestCreatePlanOverTheCapacityLimit: one dm3 past the tier-factored monthly
// limit is 422 inbound-capacity-exceeded; the boundary lands.
func TestCreatePlanOverTheCapacityLimit(t *testing.T) {
	a := newAPI(t)
	a.seller()
	declareVolume(t, a, 1000)
	a.mustCode(http.MethodPost, "/inbound-plans", volBody(5, 200), 201) // exactly 1000: the boundary lands
	a.do(http.MethodPost, "/inbound-plans", volBody(1, 1)).problem(t, 422, "inbound-capacity-exceeded")
}

// TestCreatePlanUnenforcedGate: an undeclared volume (or node limit) never
// blocks the plan; capacityEnforced says so.
func TestCreatePlanUnenforcedGate(t *testing.T) {
	a := newAPI(t)
	a.seller()
	v := a.mustCode(http.MethodPost, "/inbound-plans", volBody(3, 100), 201) // no limit declared
	if v["capacityEnforced"] != false {
		t.Fatalf("plan = %v", v)
	}
	declareVolume(t, a, 1000)
	v = a.mustCode(http.MethodPost, "/inbound-plans",
		`{"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-1","gtin":"`+gtinEach+`","qtyEaches":5000}]}`, 201)
	if v["capacityEnforced"] != false { // volume unknown: a huge plan passes
		t.Fatalf("plan = %v", v)
	}
}
