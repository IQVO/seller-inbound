package http_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sihttp "github.com/claudioed/seller-inbound/internal/adapters/inbound/http"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

const (
	gtinCase = "00614141000036"
	gtinEach = "00614141000012"
	ssccA    = "006141420000000011"
	ssccB    = "006141420000000028"
)

// api is the router over the in-memory doubles, with node n-1 active, a CASE level
// of 12 eaches declared for item-1 and a seller onboarded through the API.
type api struct {
	t   *testing.T
	h   http.Handler
	svc *usecases.Service
}

func newAPI(t *testing.T) *api {
	t.Helper()
	s := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: s.UnitOfWork(), Sellers: s.Sellers(), Plans: s.Plans(), Shipments: s.Shipments(), Nodes: s.Nodes(), Scorecard: s.Scorecard(), Packaging: s.Packaging(),
		Outbox: s.Outbox(), Inbox: s.Inbox(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{}, SSCC: &memory.SsccIssuer{},
	}, usecases.Config{})
	ctx := context.Background()
	for _, err := range []error{
		svc.ApplyNodeRegistered(ctx, "e1", requests.NodeRegistered{NodeID: "n-1", NodeType: "FULFILLMENT_CENTER"}),
		svc.ApplyNodeActivated(ctx, "e2", requests.NodeLifecycle{NodeID: "n-1"}),
		svc.ApplyPackagingDeclared(ctx, "e3", requests.PackagingDeclared{CatalogItemID: "item-1", Level: "CASE", GTIN: gtinCase, ChildGTIN: gtinEach, UnitsPerPack: 12, DeclaredAt: t0}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return &api{t: t, svc: svc, h: sihttp.NewRouter(&sihttp.Server{Service: svc})}
}

type result struct {
	code int
	body string
	hdr  http.Header
}

func (a *api) do(method, target, body string) result {
	a.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, httptest.NewRequest(method, target, rd))
	return result{rec.Code, rec.Body.String(), rec.Header()}
}

func (r result) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("not JSON: %q", r.body)
	}
	return m
}

func (r result) problem(t *testing.T, code int, slug string) {
	t.Helper()
	if r.code != code || r.hdr.Get("Content-Type") != "application/problem+json" || r.json(t)["type"] != slug {
		t.Fatalf("got %d %s %s, want %d %s", r.code, r.hdr.Get("Content-Type"), r.body, code, slug)
	}
}

func (a *api) seller() {
	a.t.Helper()
	a.mustCode(http.MethodPost, "/sellers", `{"sellerId":"S-1","name":"Acme Goods"}`, 201)
}

func (a *api) mustCode(method, target, body string, code int) map[string]any {
	a.t.Helper()
	r := a.do(method, target, body)
	if r.code != code {
		a.t.Fatalf("%s %s = %d %s, want %d", method, target, r.code, r.body, code)
	}
	return r.json(a.t)
}

func (a *api) confirmedPlan(packing, gtin string, qty int) string {
	a.t.Helper()
	body := `{"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"` + packing + `","lines":[{"catalogItemId":"item-1","gtin":"` + gtin + `","qtyEaches":` + itoa(qty) + `}]}`
	plan := a.mustCode(http.MethodPost, "/inbound-plans", body, 201)
	id := plan["planId"].(string)
	a.mustCode(http.MethodPost, "/inbound-plans/"+id+":confirm", "", 200)
	return id
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestSellerRoutes(t *testing.T) {
	a := newAPI(t)
	a.seller()
	a.do(http.MethodPost, "/sellers", `{"sellerId":"S-1","name":"Acme Goods"}`).problem(t, 409, "seller-exists")
	a.do(http.MethodPost, "/sellers", `{"sellerId":"bad id","name":"x"}`).problem(t, 422, "validation-failed")
	a.do(http.MethodPost, "/sellers", `{"sellerId":"S-2","name":"x","extra":1}`).problem(t, 422, "validation-failed")
	a.do(http.MethodPost, "/sellers", ``).problem(t, 422, "validation-failed")
	s := a.mustCode(http.MethodPost, "/sellers/S-1:suspend", `{"reason":"late"}`, 200)
	if s["state"] != "SUSPENDED" || s["suspendReason"] != "late" || s["suspendedAt"] == nil {
		t.Fatalf("%v", s)
	}
	if s = a.mustCode(http.MethodPost, "/sellers/S-1:reinstate", "", 200); s["state"] != "ACTIVE" || s["suspendedAt"] != nil {
		t.Fatalf("%v", s)
	}
	a.mustCode(http.MethodPost, "/sellers/S-1:suspend", "", 200)
	a.do(http.MethodPost, "/sellers/ghost:suspend", "").problem(t, 404, "not-found")
	a.do(http.MethodPost, "/sellers/S-1:explode", "").problem(t, 404, "not-found")
	a.do(http.MethodPost, "/sellers/S-1", "").problem(t, 404, "not-found")
}

func TestPlanRoutes(t *testing.T) {
	a := newAPI(t)
	a.seller()
	body := `{"sellerId":"S-1","destinationNodeId":"n-1","packingOption":"CASE_PACKED","lines":[{"catalogItemId":"item-1","gtin":"` + gtinCase + `","qtyEaches":24}]}`
	p := a.mustCode(http.MethodPost, "/inbound-plans", body, 201)
	if p["state"] != "DRAFT" || p["packingOption"] != "CASE_PACKED" || p["confirmedAt"] != nil || p["lines"].([]any)[0].(map[string]any)["qtyEaches"] != float64(24) {
		t.Fatalf("%v", p)
	}
	id := p["planId"].(string)
	if got := a.mustCode(http.MethodGet, "/inbound-plans/"+id, "", 200); got["planId"] != id {
		t.Fatal(got)
	}
	if c := a.mustCode(http.MethodPost, "/inbound-plans/"+id+":confirm", "", 200); c["state"] != "CONFIRMED" || c["confirmedAt"] == nil {
		t.Fatal(c)
	}
	a.do(http.MethodPost, "/inbound-plans/"+id+":cancel", "").problem(t, 409, "illegal-transition")
	q := a.mustCode(http.MethodPost, "/inbound-plans", body, 201)["planId"].(string)
	if c := a.mustCode(http.MethodPost, "/inbound-plans/"+q+":cancel", "", 200); c["state"] != "CANCELLED" || c["cancelledAt"] == nil {
		t.Fatal(c)
	}
	a.do(http.MethodGet, "/inbound-plans/ghost", "").problem(t, 404, "not-found")
	a.do(http.MethodPost, "/inbound-plans/"+id+":nope", "").problem(t, 404, "not-found")
	a.do(http.MethodPost, "/inbound-plans", strings.Replace(body, `"n-1"`, `"ghost"`, 1)).problem(t, 409, "node-cannot-receive")
	a.do(http.MethodPost, "/inbound-plans", strings.Replace(body, `"S-1"`, `"ghost"`, 1)).problem(t, 404, "not-found")
	a.do(http.MethodPost, "/inbound-plans", strings.Replace(body, gtinCase, "123", 1)).problem(t, 422, "validation-failed")
	a.mustCode(http.MethodPost, "/sellers/S-1:suspend", "", 200)
	a.do(http.MethodPost, "/inbound-plans", body).problem(t, 409, "seller-suspended")
}

func cartonBody(sscc string, qty int, extra string) string {
	s := ""
	if sscc != "" {
		s = `"sscc":"` + sscc + `",`
	}
	return `{` + s + extra + `"contents":[{"catalogItemId":"item-1","gtin":"` + gtinCase + `","qtyEaches":` + itoa(qty) + `}],"grossWeightG":5000,"dimsMm":{"l":400,"w":300,"h":200}}`
}

func TestShipmentRoutesEndToEnd(t *testing.T) {
	a := newAPI(t)
	a.seller()
	plan := a.confirmedPlan("CASE_PACKED", gtinCase, 24)
	sh := a.mustCode(http.MethodPost, "/inbound-plans/"+plan+"/shipments", "", 201)
	id := sh["shipmentId"].(string)
	if sh["state"] != "DRAFT" || sh["nodeId"] != "n-1" || len(sh["cartons"].([]any)) != 0 {
		t.Fatalf("%v", sh)
	}
	assertIssuedCarton(t, a.mustCode(http.MethodPost, "/inbound-shipments/"+id+"/cartons", cartonBody("", 12, ""), 201))
	if c2 := a.mustCode(http.MethodPost, "/inbound-shipments/"+id+"/cartons", cartonBody(ssccA, 12, `"parentSscc":"`+ssccB+`",`), 201); c2["ssccIssued"] != false {
		t.Fatalf("%v", c2)
	}
	a.do(http.MethodPost, "/inbound-shipments/"+id+":ship", "").problem(t, 409, "shipment-not-labelled")
	if l := a.mustCode(http.MethodPost, "/inbound-shipments/"+id+":label", "", 200); l["state"] != "LABELLED" || l["labelledAt"] == nil {
		t.Fatalf("%v", l)
	}
	labels := a.mustCode(http.MethodGet, "/inbound-shipments/"+id+"/labels", "", 200)
	if ls := labels["labels"].([]any); len(ls) != 2 || labels["destinationNodeId"] != "n-1" || ls[1].(map[string]any)["parentSscc"] != ssccB {
		t.Fatalf("%v", labels)
	}
	if s := a.mustCode(http.MethodPost, "/inbound-shipments/"+id+":ship", "", 200); s["state"] != "SHIPPED" || s["shippedAt"] == nil {
		t.Fatalf("%v", s)
	}
	if got := a.mustCode(http.MethodGet, "/inbound-shipments/"+id, "", 200); got["state"] != "SHIPPED" || len(got["cartons"].([]any)) != 2 {
		t.Fatalf("%v", got)
	}
}

func assertIssuedCarton(t *testing.T, c map[string]any) {
	t.Helper()
	label := c["label"].(map[string]any)
	sscc := c["carton"].(map[string]any)["sscc"].(string)
	if c["ssccIssued"] != true || label["gs1128"] != "(00)"+sscc || label["destinationNodeId"] != "n-1" || label["totalEaches"] != float64(12) || label["contentSummary"] == "" {
		t.Fatalf("%v", c)
	}
}

func TestCartonErrorsHaveStableSlugs(t *testing.T) {
	a := newAPI(t)
	a.seller()
	plan := a.confirmedPlan("CASE_PACKED", gtinCase, 120)
	id := a.mustCode(http.MethodPost, "/inbound-plans/"+plan+"/shipments", "", 201)["shipmentId"].(string)
	url := "/inbound-shipments/" + id + "/cartons"
	a.do(http.MethodPost, url, cartonBody("", 10, "")).problem(t, 422, "case-qty-mismatches-gtin")
	a.do(http.MethodPost, url, cartonBody("123", 12, "")).problem(t, 422, "invalid-sscc")
	a.do(http.MethodPost, url, strings.Replace(cartonBody("", 12, ""), `"qtyEaches":12}`, `"qtyEaches":12},{"catalogItemId":"item-2","gtin":"`+gtinEach+`","qtyEaches":1}`, 1)).problem(t, 422, "case-pack-not-uniform")
	a.do(http.MethodPost, url, cartonBody("", 12, `"unknown":1,`)).problem(t, 422, "validation-failed")
	a.mustCode(http.MethodPost, url, cartonBody(ssccA, 12, ""), 201)
	a.do(http.MethodPost, url, cartonBody(ssccA, 12, "")).problem(t, 409, "sscc-not-unique")
	a.do(http.MethodPost, "/inbound-shipments/ghost/cartons", cartonBody("", 12, "")).problem(t, 404, "not-found")
	a.do(http.MethodGet, "/inbound-shipments/ghost/labels", "").problem(t, 404, "not-found")
	a.do(http.MethodGet, "/inbound-shipments/ghost", "").problem(t, 404, "not-found")
	a.do(http.MethodPost, "/inbound-shipments/"+id+":label", "").problem(t, 422, "carton-contents-mismatch-plan")
	a.do(http.MethodPost, "/inbound-shipments/"+id+":nope", "").problem(t, 404, "not-found")
	a.do(http.MethodPost, "/inbound-plans/ghost/shipments", "").problem(t, 404, "not-found")
}

func TestNodeLimitsMapToProblems(t *testing.T) {
	a := newAPI(t)
	a.seller()
	if err := a.svc.ApplyNodeProfileDeclared(context.Background(), "e9", requests.NodeProfileDeclared{NodeID: "n-1", ProfileRevision: 1,
		Inbound: &requests.NodeInbound{MaxCartonWeightG: 4000, MaxCartonDims: &requests.Dims{L: 400, W: 300, H: 100}}}); err != nil {
		t.Fatal(err)
	}
	plan := a.confirmedPlan("CASE_PACKED", gtinCase, 24)
	id := a.mustCode(http.MethodPost, "/inbound-plans/"+plan+"/shipments", "", 201)["shipmentId"].(string)
	url := "/inbound-shipments/" + id + "/cartons"
	a.do(http.MethodPost, url, cartonBody("", 12, "")).problem(t, 422, "carton-overweight")
	light := strings.Replace(cartonBody("", 12, ""), `"grossWeightG":5000`, `"grossWeightG":3000`, 1)
	a.do(http.MethodPost, url, light).problem(t, 422, "carton-oversize")
	_ = a.svc.ApplyNodeSuspended(context.Background(), "e10", requests.NodeLifecycle{NodeID: "n-1"})
	a.do(http.MethodPost, url, cartonBody("", 12, "")).problem(t, 409, "node-cannot-receive")
}

func TestSuspendedSellerCannotShip(t *testing.T) {
	a := newAPI(t)
	a.seller()
	plan := a.confirmedPlan("CASE_PACKED", gtinCase, 12)
	id := a.mustCode(http.MethodPost, "/inbound-plans/"+plan+"/shipments", "", 201)["shipmentId"].(string)
	a.mustCode(http.MethodPost, "/inbound-shipments/"+id+"/cartons", cartonBody("", 12, ""), 201)
	a.mustCode(http.MethodPost, "/inbound-shipments/"+id+":label", "", 200)
	a.mustCode(http.MethodPost, "/sellers/S-1:suspend", "", 200)
	a.do(http.MethodPost, "/inbound-shipments/"+id+":ship", "").problem(t, 409, "seller-suspended")
	a.do(http.MethodPost, "/inbound-plans/"+plan+"/shipments", "").problem(t, 409, "seller-suspended")
}

func TestHealthAndUnknownRoutes(t *testing.T) {
	a := newAPI(t)
	if r := a.do(http.MethodGet, "/healthz", ""); r.code != 200 || r.json(t)["status"] != "ok" {
		t.Fatal(r)
	}
	if r := a.do(http.MethodGet, "/readyz", ""); r.code != 200 || r.json(t)["status"] != "ready" {
		t.Fatal(r)
	}
	if r := a.do(http.MethodGet, "/nope", ""); r.code != 404 {
		t.Fatal(r)
	}
}
