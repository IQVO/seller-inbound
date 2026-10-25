package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/seller-inbound/internal/adapters/inbound/mcp"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

// now is the fixture's fixed "now" (also the scorecard window end).
var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// world is the seeded seller-inbound every tool test reads: one ACTIVE
// seller, one ACTIVE fulfillment centre, a declared CASE pack level of 12
// eaches, a CONFIRMED CASE_PACKED plan of 24 eaches and one LABELLED
// shipment of two HOMOGENEOUS_CASE cartons.
type world struct {
	sellerID, planID, shipmentID string
	cartonSSCCs                  []string
}

// harness is a connected in-memory MCP client over the real server wired to
// the application service over in-memory repositories, exactly as cmd/mcp
// wires them (minus Postgres). The write use cases exist ONLY here, to seed
// data the way the REST adapter would: the MCP surface itself has none.
type harness struct {
	session *sdk.ClientSession
	store   *memory.Store
	svc     *usecases.Service
	clock   *memory.Clock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewClock(now)
	deps := usecases.Deps{
		UoW: store.UnitOfWork(), Sellers: store.Sellers(), Plans: store.Plans(), Shipments: store.Shipments(),
		Nodes: store.Nodes(), Scorecard: store.Scorecard(), Packaging: store.Packaging(),
		Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: clock, IDs: &memory.SequentialIDs{}, SSCC: &memory.SsccIssuer{},
	}
	svc := usecases.New(deps, usecases.Config{})
	return &harness{
		session: connectSession(t, inboundmcp.Deps{Reads: svc}),
		store:   store, svc: svc, clock: clock,
	}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
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
	if err != nil {
		t.Fatal(err)
	}
	return g.String()
}

// seedWorld populates the fixture the way the REST adapter's write use cases
// would (onboard the seller, fold the node and packaging events, plan and
// confirm, open a shipment, pack two cartons, label), and returns the ids.
func (h *harness) seedWorld(t *testing.T) world {
	t.Helper()
	ctx := context.Background()
	caseGTIN, eachGTIN := gtin(t, "0614141000036"), gtin(t, "0614141000012")

	_, err := h.svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "seller-77", Name: "Acme Goods"})
	must(t, err)
	must(t, h.svc.ApplyNodeRegistered(ctx, "ev-reg-1", requests.NodeRegistered{NodeID: "node-1", NodeType: "FULFILLMENT_CENTER"}))
	must(t, h.svc.ApplyNodeActivated(ctx, "ev-act-1", requests.NodeLifecycle{NodeID: "node-1"}))
	must(t, h.svc.ApplyPackagingDeclared(ctx, "ev-pk-1", requests.PackagingDeclared{
		CatalogItemID: "item-1", Level: "CASE", GTIN: caseGTIN, ChildGTIN: eachGTIN, UnitsPerPack: 12, DeclaredAt: now,
	}))

	plan, err := h.svc.CreateInboundPlan(ctx, requests.CreateInboundPlan{
		SellerID: "seller-77", DestinationNodeID: "node-1", PackingOption: "CASE_PACKED",
		Lines: []requests.PlanLine{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 24}},
	})
	must(t, err)
	_, err = h.svc.ConfirmInboundPlan(ctx, plan.PlanID)
	must(t, err)

	shipment, err := h.svc.CreateInboundShipment(ctx, plan.PlanID)
	must(t, err)
	var ssccs []string
	for range 2 {
		added, err := h.svc.AddCarton(ctx, requests.AddCarton{
			ShipmentID: shipment.ShipmentID, Kind: "HOMOGENEOUS_CASE",
			Contents:     []requests.Content{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 12}},
			GrossWeightG: 5200, DimsL: 400, DimsW: 300, DimsH: 200,
		})
		must(t, err)
		ssccs = append(ssccs, added.Carton.SSCC)
	}
	_, err = h.svc.LabelShipment(ctx, shipment.ShipmentID)
	must(t, err)

	return world{sellerID: "seller-77", planID: plan.PlanID, shipmentID: shipment.ShipmentID, cartonSSCCs: ssccs}
}

// withWorld is a harness whose world is already seeded.
func withWorld(t *testing.T) (*harness, world) {
	t.Helper()
	h := newHarness(t)
	return h, h.seedWorld(t)
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
