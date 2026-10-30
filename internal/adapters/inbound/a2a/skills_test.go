package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// world is the seeded seller-inbound every skill test reads: one ACTIVE
// seller, one ACTIVE fulfillment centre, a CONFIRMED CASE_PACKED plan of 24
// eaches and one LABELLED shipment of two HOMOGENEOUS_CASE cartons.
type world struct {
	sellerID, planID, shipmentID string
	cartonSSCCs                  []string
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func gtin(t *testing.T, payload string) string {
	t.Helper()
	g, err := gs1.NewGTIN(payload)
	must(t, err)
	return g.String()
}

// seededService wires the real application service over the in-memory
// repositories and seeds the world through the WRITE use cases the way the
// REST adapter would. The write use cases exist ONLY here: the A2A adapter's
// Deps carry reads alone.
func seededService(t *testing.T) (*usecases.Service, world) {
	t.Helper()
	ctx := context.Background()
	store := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Sellers: store.Sellers(), Plans: store.Plans(), Shipments: store.Shipments(),
		Nodes: store.Nodes(), Scorecard: store.Scorecard(), Packaging: store.Packaging(),
		Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: memory.NewClock(now), IDs: &memory.SequentialIDs{}, SSCC: &memory.SsccIssuer{},
	}, usecases.Config{})
	caseGTIN, eachGTIN := gtin(t, "0614141000036"), gtin(t, "0614141000012")

	_, err := svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "seller-77", Name: "Acme Goods"})
	must(t, err)
	must(t, svc.ApplyNodeRegistered(ctx, "ev-reg-1", requests.NodeRegistered{NodeID: "node-1", NodeType: "FULFILLMENT_CENTER"}))
	must(t, svc.ApplyNodeActivated(ctx, "ev-act-1", requests.NodeLifecycle{NodeID: "node-1"}))
	must(t, svc.ApplyPackagingDeclared(ctx, "ev-pk-1", requests.PackagingDeclared{
		CatalogItemID: "item-1", Level: "CASE", GTIN: caseGTIN, ChildGTIN: eachGTIN, UnitsPerPack: 12, DeclaredAt: now,
	}))
	plan, err := svc.CreateInboundPlan(ctx, requests.CreateInboundPlan{
		SellerID: "seller-77", DestinationNodeID: "node-1", PackingOption: "CASE_PACKED",
		Lines: []requests.PlanLine{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 24}},
	})
	must(t, err)
	_, err = svc.ConfirmInboundPlan(ctx, plan.PlanID)
	must(t, err)
	shipment, err := svc.CreateInboundShipment(ctx, plan.PlanID)
	must(t, err)
	var ssccs []string
	for range 2 {
		added, err := svc.AddCarton(ctx, requests.AddCarton{
			ShipmentID: shipment.ShipmentID, Kind: "HOMOGENEOUS_CASE",
			Contents:     []requests.Content{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 12}},
			GrossWeightG: 5200, DimsL: 400, DimsW: 300, DimsH: 200,
		})
		must(t, err)
		ssccs = append(ssccs, added.Carton.SSCC)
	}
	_, err = svc.LabelShipment(ctx, shipment.ShipmentID)
	must(t, err)
	return svc, world{sellerID: "seller-77", planID: plan.PlanID, shipmentID: shipment.ShipmentID, cartonSSCCs: ssccs}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireProblem and wireEnvelope are a TEST-LOCAL copy of the answer contract.
// If the adapter's envelope drifts, this suite fails instead of silently
// following it.
type wireProblem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		Scorecard *struct {
			SellerID            string         `json:"sellerId"`
			Window              string         `json:"window"`
			ShipmentsReceived   int            `json:"shipmentsReceived"`
			DiscrepanciesByKind map[string]int `json:"discrepanciesByKind"`
			ComplianceTier      string         `json:"complianceTier"`
		} `json:"scorecard"`
		Plan *struct {
			PlanID            string `json:"planId"`
			SellerID          string `json:"sellerId"`
			DestinationNodeID string `json:"destinationNodeId"`
			PackingOption     string `json:"packingOption"`
			State             string `json:"state"`
			Lines             []struct {
				QtyEaches int `json:"qtyEaches"`
			} `json:"lines"`
		} `json:"plan"`
		Shipment *struct {
			ShipmentID string `json:"shipmentId"`
			PlanID     string `json:"planId"`
			State      string `json:"state"`
			Cartons    []struct {
				SSCC string `json:"sscc"`
				Kind string `json:"kind"`
			} `json:"cartons"`
		} `json:"shipment"`
	} `json:"data"`
	Problem *wireProblem `json:"problem"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	must(t, err)
	return &wire{client: client}
}

// call sends one message part and returns the decoded envelope, the one-line
// text summary and the raw JSON of the data part.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string, string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text), string(raw)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	env, text, _ := w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
	return env, text
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d %s problem, got data: %+v", status, slug, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func seededWire(t *testing.T) (*wire, world) {
	t.Helper()
	svc, w := seededService(t)
	return newWire(t, Deps{Reads: svc}), w
}

func TestWireSellerComplianceAnswersTheScorecard(t *testing.T) {
	wr, w := seededWire(t)
	for name, tc := range map[string]struct {
		args   map[string]any
		window string
	}{
		"window omitted defaults to 30d": {map[string]any{"sellerId": w.sellerID}, "30d"},
		"window 7d":                      {map[string]any{"sellerId": w.sellerID, "window": "7d"}, "7d"},
		"window 30d":                     {map[string]any{"sellerId": w.sellerID, "window": "30d"}, "30d"},
	} {
		t.Run(name, func(t *testing.T) {
			env, text := wr.skill(t, skillSellerCompliance, tc.args)
			s := env.Data.Scorecard
			if env.Problem != nil || s == nil || env.Skill != skillSellerCompliance {
				t.Fatalf("envelope = %+v", env)
			}
			if s.SellerID != w.sellerID || s.Window != tc.window || s.ComplianceTier != "GOOD" || s.ShipmentsReceived != 0 || s.DiscrepanciesByKind == nil {
				t.Fatalf("scorecard = %+v", s)
			}
			want := "seller-77: GOOD compliance tier over " + tc.window + " (0 shipments received, 0 compliance violations)"
			if text != want {
				t.Fatalf("summary = %q, want %q", text, want)
			}
		})
	}
}

func TestWireInboundStatusReadsAPlan(t *testing.T) {
	wr, w := seededWire(t)
	env, text := wr.skill(t, skillInboundStatus, map[string]any{"planId": w.planID})
	p := env.Data.Plan
	if env.Problem != nil || p == nil || env.Data.Shipment != nil {
		t.Fatalf("envelope = %+v", env)
	}
	if p.PlanID != w.planID || p.SellerID != w.sellerID || p.DestinationNodeID != "node-1" || p.PackingOption != "CASE_PACKED" ||
		p.State != "CONFIRMED" || len(p.Lines) != 1 || p.Lines[0].QtyEaches != 24 {
		t.Fatalf("plan = %+v", p)
	}
	if want := w.planID + ": CONFIRMED plan of seller seller-77 to node node-1, CASE_PACKED, 1 lines (24 eaches)"; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

func TestWireInboundStatusReadsAShipment(t *testing.T) {
	wr, w := seededWire(t)
	env, text := wr.skill(t, skillInboundStatus, map[string]any{"shipmentId": w.shipmentID})
	s := env.Data.Shipment
	if env.Problem != nil || s == nil || env.Data.Plan != nil {
		t.Fatalf("envelope = %+v", env)
	}
	if s.ShipmentID != w.shipmentID || s.PlanID != w.planID || s.State != "LABELLED" || len(s.Cartons) != 2 ||
		s.Cartons[0].SSCC != w.cartonSSCCs[0] || s.Cartons[0].Kind != "HOMOGENEOUS_CASE" {
		t.Fatalf("shipment = %+v", s)
	}
	if want := w.shipmentID + ": LABELLED shipment of plan " + w.planID + " to node node-1, 2 cartons"; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

// A skill request may also arrive as a Text part holding the same JSON, and
// the reply is always the data part + one-line text part.
func TestWireAcceptsATextPartHoldingTheSkillJSON(t *testing.T) {
	wr, w := seededWire(t)
	env, _, _ := wr.call(t, a2a.NewTextPart(`{"skill":"inbound_status","arguments":{"planId":"`+w.planID+`"}}`))
	if env.Problem != nil || env.Data.Plan == nil || env.Data.Plan.PlanID != w.planID {
		t.Fatalf("envelope = %+v", env)
	}
}

// ---- argument validation and in-band problems -------------------------------

func TestWireArgumentValidationIsAnsweredInBand(t *testing.T) {
	wr, w := seededWire(t)
	for name, tc := range map[string]struct {
		skill  string
		args   map[string]any
		status int
		slug   string
	}{
		"seller_compliance: sellerId missing":           {skillSellerCompliance, map[string]any{}, 400, "invalid-argument"},
		"seller_compliance: sellerId blank":             {skillSellerCompliance, map[string]any{"sellerId": "  "}, 400, "invalid-argument"},
		"seller_compliance: sellerId wrong type":        {skillSellerCompliance, map[string]any{"sellerId": 7}, 400, "invalid-argument"},
		"seller_compliance: misspelled window":          {skillSellerCompliance, map[string]any{"sellerId": w.sellerID, "windows": "7d"}, 400, "invalid-argument"},
		"seller_compliance: unknown argument":           {skillSellerCompliance, map[string]any{"sellerId": w.sellerID, "planId": w.planID}, 400, "invalid-argument"},
		"seller_compliance: window wrong type":          {skillSellerCompliance, map[string]any{"sellerId": w.sellerID, "window": 7}, 400, "invalid-argument"},
		"seller_compliance: unsupported window":         {skillSellerCompliance, map[string]any{"sellerId": w.sellerID, "window": "90d"}, 400, "invalid-window"},
		"seller_compliance: unknown seller":             {skillSellerCompliance, map[string]any{"sellerId": "seller-404"}, 404, "not-found"},
		"inbound_status: no id":                         {skillInboundStatus, map[string]any{}, 400, "invalid-argument"},
		"inbound_status: blank ids":                     {skillInboundStatus, map[string]any{"planId": " ", "shipmentId": ""}, 400, "invalid-argument"},
		"inbound_status: both ids":                      {skillInboundStatus, map[string]any{"planId": w.planID, "shipmentId": w.shipmentID}, 400, "invalid-argument"},
		"inbound_status: unknown argument":              {skillInboundStatus, map[string]any{"planId": w.planID, "id": "x"}, 400, "invalid-argument"},
		"inbound_status: planId wrong type":             {skillInboundStatus, map[string]any{"planId": []string{"a"}}, 400, "invalid-argument"},
		"inbound_status: unknown plan":                  {skillInboundStatus, map[string]any{"planId": "pln-404"}, 404, "not-found"},
		"inbound_status: unknown shipment":              {skillInboundStatus, map[string]any{"shipmentId": "shp-404"}, 404, "not-found"},
		"unknown skill (a write on the REST API)":       {"confirm_inbound_plan", map[string]any{"planId": w.planID}, 400, "unknown-skill"},
		"unknown skill (a sibling MCP tool is not one)": {"get_shipment_labels", map[string]any{"shipmentId": w.shipmentID}, 400, "unknown-skill"},
	} {
		t.Run(name, func(t *testing.T) {
			env, text := wr.skill(t, tc.skill, tc.args)
			wantProblem(t, env, tc.status, tc.slug)
			if env.Skill != tc.skill {
				t.Errorf("envelope skill = %q, want %q", env.Skill, tc.skill)
			}
			if !strings.Contains(text, env.Problem.Title) {
				t.Errorf("summary %q does not carry the problem title %q", text, env.Problem.Title)
			}
		})
	}
}

func TestWireMalformedRequestsAreProblemsNotRPCErrors(t *testing.T) {
	wr, _ := seededWire(t)
	for name, part := range map[string]*a2a.Part{
		"text part that is not JSON":        a2a.NewTextPart("how compliant is seller-77?"),
		"text part with trailing data":      a2a.NewTextPart(`{"skill":"seller_compliance","arguments":{"sellerId":"seller-77"}} {"x":1}`),
		"no skill field":                    a2a.NewDataPart(map[string]any{"arguments": map[string]any{"sellerId": "seller-77"}}),
		"unknown request field":             a2a.NewDataPart(map[string]any{"skill": "seller_compliance", "args": map[string]any{}}),
		"raw (non-JSON) part":               a2a.NewRawPart([]byte{0x01, 0x02}),
		"data part that is not an object":   a2a.NewDataPart([]string{"seller_compliance"}),
		"text part with a string as skills": a2a.NewTextPart(`"seller_compliance"`),
	} {
		t.Run(name, func(t *testing.T) {
			env, _, _ := wr.call(t, part)
			wantProblem(t, env, 400, "malformed-skill-request")
		})
	}
}

// stubReads fails every read with err, to prove the 500 classification and
// that infrastructure detail never reaches the agent client.
type stubReads struct{ err error }

func (s stubReads) GetSellerScorecard(context.Context, string, string) (views.SellerScorecard, error) {
	return views.SellerScorecard{}, s.err
}

func (s stubReads) GetInboundPlan(context.Context, string) (views.Plan, error) {
	return views.Plan{}, s.err
}

func (s stubReads) GetInboundShipment(context.Context, string) (views.Shipment, error) {
	return views.Shipment{}, s.err
}

func TestWireUnexpectedFailuresAreAnswered500WithoutLeakingDetail(t *testing.T) {
	wr := newWire(t, Deps{Reads: stubReads{err: errors.New("dial tcp 10.0.0.9:5432: password authentication failed for user svc_si")}})
	for skill, args := range map[string]map[string]any{
		skillSellerCompliance: {"sellerId": "seller-77"},
		skillInboundStatus:    {"planId": "pln-1"},
	} {
		env, text, raw := wr.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
		wantProblem(t, env, 500, "internal-error")
		for _, leak := range []string{"10.0.0.9", "password", "svc_si"} {
			if strings.Contains(raw+text, leak) {
				t.Errorf("%s: the answer leaks %q: %s / %s", skill, leak, raw, text)
			}
		}
	}
}

// The problem detail is adapter-owned text: the shared domain error prefix
// ("validation failed: window: ...") must not reach the client.
func TestWireProblemDetailIsAdapterOwned(t *testing.T) {
	wr, w := seededWire(t)
	env, text := wr.skill(t, skillSellerCompliance, map[string]any{"sellerId": w.sellerID, "window": "90d"})
	wantProblem(t, env, 400, "invalid-window")
	if strings.Contains(env.Problem.Detail+text, "validation failed") {
		t.Errorf("shared error text leaked into the answer: %q / %q", env.Problem.Detail, text)
	}
	if env.Problem.Detail != `window "90d" is not supported; use 7d or 30d (omit it for 30d)` {
		t.Errorf("detail = %q", env.Problem.Detail)
	}
}

// Each skill run is one OTel span named "a2a.skill <id>" carrying the
// a2a.skill attribute, like the MCP adapter's mcp.tool spans.
func TestSkillRunEmitsASpanWithTheSkillAttribute(t *testing.T) {
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))

	wr, w := seededWire(t)
	wr.skill(t, skillSellerCompliance, map[string]any{"sellerId": w.sellerID})

	var found bool
	for _, span := range recorder.Ended() {
		if span.Name() != "a2a.skill "+skillSellerCompliance {
			continue
		}
		found = true
		for _, attr := range span.Attributes() {
			if string(attr.Key) == "a2a.skill" && attr.Value.AsString() == skillSellerCompliance {
				return
			}
		}
		t.Fatalf("span %q lacks the a2a.skill attribute: %v", span.Name(), span.Attributes())
	}
	if !found {
		t.Fatal("no a2a.skill span recorded")
	}
}
