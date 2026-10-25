//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/seller-inbound/internal/adapters/inbound/mcp"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/clock"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/ids"
	outboundkafka "github.com/claudioed/seller-inbound/internal/adapters/outbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/postgres"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/sscc"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
	"github.com/claudioed/seller-inbound/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases
// are already migrated, a second run must be a no-op), then reads through
// every MCP tool exactly what the write use cases persisted, and its reads
// add no outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	countOutbox := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return n
	}

	w := seedThroughWriteUseCases(t, url)
	before := countOutbox()
	if before < 3 {
		t.Fatalf("outbox rows after seeding = %d, want the seller onboarded, plan created and shipment created events", before)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertSeededReads(t, session, w)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
	}
}

// seeded is the shape of the world the write use cases persist.
type seeded struct {
	sellerID, planID, shipmentID, cartonSSCC string
}

// seedThroughWriteUseCases onboards a seller, folds the node and packaging
// events, plans and confirms a CASE_PACKED inbound, opens a shipment, packs
// two cartons and labels it, the way cmd/api does (same use cases, Postgres
// adapters, transactional outbox and inbox).
func seedThroughWriteUseCases(t *testing.T, url string) seeded {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ob := postgres.NewOutboxRepo(pool)
	issuer, err := sscc.NewIssuer("0614141", 0, postgres.NewSsccSequence(pool))
	if err != nil {
		t.Fatal(err)
	}
	svc := usecases.New(usecases.Deps{
		UoW: postgres.NewUnitOfWork(pool), Sellers: postgres.NewSellerRepo(pool), Plans: postgres.NewPlanRepo(pool),
		Shipments: postgres.NewShipmentRepo(pool), Nodes: postgres.NewNodeViewRepo(pool), Scorecard: postgres.NewScorecardFacts(pool),
		Packaging: postgres.NewPackagingViewRepo(pool), Outbox: postgres.NewEventOutbox(ob, outboundkafka.NewEncoder()),
		Inbox: postgres.NewProcessedEventRepo(pool), Clock: clock.System{}, IDs: ids.UUID{}, SSCC: issuer,
	}, usecases.Config{Logger: quietLogger()})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	gtinOf := func(payload string) string {
		t.Helper()
		g, err := gs1.NewGTIN(payload)
		if err != nil {
			t.Fatal(err)
		}
		return g.String()
	}
	caseGTIN, eachGTIN := gtinOf("0614141000036"), gtinOf("0614141000012")

	_, err = svc.OnboardSeller(ctx, requests.OnboardSeller{SellerID: "seller-77", Name: "Acme Goods"})
	must(err)
	must(svc.ApplyNodeRegistered(ctx, "ev-reg-1", requests.NodeRegistered{NodeID: "node-1", NodeType: "FULFILLMENT_CENTER"}))
	must(svc.ApplyNodeActivated(ctx, "ev-act-1", requests.NodeLifecycle{NodeID: "node-1"}))
	must(svc.ApplyPackagingDeclared(ctx, "ev-pk-1", requests.PackagingDeclared{
		CatalogItemID: "item-1", Level: "CASE", GTIN: caseGTIN, ChildGTIN: eachGTIN, UnitsPerPack: 12,
		DeclaredAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC),
	}))

	plan, err := svc.CreateInboundPlan(ctx, requests.CreateInboundPlan{
		SellerID: "seller-77", DestinationNodeID: "node-1", PackingOption: "CASE_PACKED",
		Lines: []requests.PlanLine{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 24}},
	})
	must(err)
	_, err = svc.ConfirmInboundPlan(ctx, plan.PlanID)
	must(err)
	shipment, err := svc.CreateInboundShipment(ctx, plan.PlanID)
	must(err)
	added, err := svc.AddCarton(ctx, requests.AddCarton{
		ShipmentID: shipment.ShipmentID, Kind: "HOMOGENEOUS_CASE",
		Contents:     []requests.Content{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 12}},
		GrossWeightG: 5200,
		DimsL:        400, DimsW: 300, DimsH: 200,
	})
	must(err)
	_, err = svc.AddCarton(ctx, requests.AddCarton{
		ShipmentID: shipment.ShipmentID, Kind: "HOMOGENEOUS_CASE",
		Contents:     []requests.Content{{CatalogItemID: "item-1", GTIN: caseGTIN, QtyEaches: 12}},
		GrossWeightG: 5200,
		DimsL:        400, DimsW: 300, DimsH: 200,
	})
	must(err)
	_, err = svc.LabelShipment(ctx, shipment.ShipmentID)
	must(err)
	return seeded{sellerID: "seller-77", planID: plan.PlanID, shipmentID: shipment.ShipmentID, cartonSSCC: added.Carton.SSCC}
}

// assertSeededReads drives every tool and checks the answer against the
// seeded world.
func assertSeededReads(t *testing.T, session *sdk.ClientSession, w seeded) {
	t.Helper()
	ctx := context.Background()
	call := func(name string, args map[string]any) *sdk.CallToolResult {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: transport error: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned a tool error", name)
		}
		return res
	}
	sc := func(res *sdk.CallToolResult) map[string]any {
		t.Helper()
		m, ok := res.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("structured content = %#v, want an object", res.StructuredContent)
		}
		return m
	}

	scorecard := sc(call("get_seller_scorecard", map[string]any{"sellerId": w.sellerID}))
	if scorecard["sellerId"] != w.sellerID || scorecard["complianceTier"] != "GOOD" {
		t.Fatalf("get_seller_scorecard = %v", scorecard)
	}
	plan := sc(call("get_inbound_plan", map[string]any{"planId": w.planID}))
	if plan["planId"] != w.planID || plan["state"] != "CONFIRMED" {
		t.Fatalf("get_inbound_plan = %v", plan)
	}
	shipment := sc(call("get_inbound_shipment", map[string]any{"shipmentId": w.shipmentID}))
	if shipment["shipmentId"] != w.shipmentID || shipment["state"] != "LABELLED" {
		t.Fatalf("get_inbound_shipment = %v", shipment)
	}
	labels := sc(call("get_shipment_labels", map[string]any{"shipmentId": w.shipmentID}))
	if labels["shipmentId"] != w.shipmentID || labels["state"] != "LABELLED" {
		t.Fatalf("get_shipment_labels = %v", labels)
	}
	entries, _ := labels["labels"].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["sscc"] != w.cartonSSCC {
		t.Fatalf("labels = %v, want the two packed cartons with %s first", labels["labels"], w.cartonSSCC)
	}
}
