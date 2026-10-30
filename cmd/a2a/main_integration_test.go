//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/claudioed/seller-inbound/internal/adapters/inbound/a2a"
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

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then answers both skills
// over the real JSON-RPC wire from exactly what the write use cases persisted,
// and its reads add no outbox row: the agent is read-only, so nothing it does
// can leave an event behind.
func TestA2AAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
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

	srv := httptest.NewServer(newAgent(deps, testAgentURL, "test", quietLogger(), "seller-inbound-a2a"))
	defer srv.Close()
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "test"})
	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}

	assertSeededReads(t, client, w)

	if after := countOutbox(); after != before {
		t.Fatalf("outbox rows %d -> %d: the A2A binary wrote an event", before, after)
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

// ask sends one skill request and returns the decoded data part of the reply.
func ask(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) map[string]any {
	t.Helper()
	part := a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args})
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("%s: transport error: %v", skill, err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) != 2 {
		t.Fatalf("%s: reply = %T, want a message with a data and a text part", skill, resp)
	}
	raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	if env["problem"] != nil {
		t.Fatalf("%s answered a problem: %s", skill, raw)
	}
	data, _ := env["data"].(map[string]any)
	return data
}

// assertSeededReads drives every skill and checks the answer against the
// seeded world.
func assertSeededReads(t *testing.T, client *a2aclient.Client, w seeded) {
	t.Helper()
	field := func(data map[string]any, key string) map[string]any {
		t.Helper()
		m, ok := data[key].(map[string]any)
		if !ok {
			t.Fatalf("data has no %q object: %v", key, data)
		}
		return m
	}

	scorecard := field(ask(t, client, "seller_compliance", map[string]any{"sellerId": w.sellerID}), "scorecard")
	if scorecard["sellerId"] != w.sellerID || scorecard["complianceTier"] != "GOOD" || scorecard["window"] != "30d" {
		t.Fatalf("seller_compliance = %v", scorecard)
	}
	plan := field(ask(t, client, "inbound_status", map[string]any{"planId": w.planID}), "plan")
	if plan["planId"] != w.planID || plan["state"] != "CONFIRMED" {
		t.Fatalf("inbound_status(planId) = %v", plan)
	}
	shipment := field(ask(t, client, "inbound_status", map[string]any{"shipmentId": w.shipmentID}), "shipment")
	if shipment["shipmentId"] != w.shipmentID || shipment["state"] != "LABELLED" {
		t.Fatalf("inbound_status(shipmentId) = %v", shipment)
	}
	cartons, _ := shipment["cartons"].([]any)
	if len(cartons) != 2 || cartons[0].(map[string]any)["sscc"] != w.cartonSSCC {
		t.Fatalf("cartons = %v, want the two packed cartons with %s first", shipment["cartons"], w.cartonSSCC)
	}
}
