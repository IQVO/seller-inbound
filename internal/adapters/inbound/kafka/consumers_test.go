package kafka_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	kafkaconsumer "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/domain/network"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

const (
	gtinCase = "00614141000036"
	gtinEach = "00614141000012"
)

// fixture is a Service over the memory doubles with the three consumers wired over it.
type fixture struct {
	store *memory.Store
	svc   *usecases.Service
	dlq   *fakeDLQWriter
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: s.UnitOfWork(), Sellers: s.Sellers(), Plans: s.Plans(), Shipments: s.Shipments(), Nodes: s.Nodes(), Scorecard: s.Scorecard(), Packaging: s.Packaging(),
		Outbox: s.Outbox(), Inbox: s.Inbox(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{}, SSCC: &memory.SsccIssuer{},
	}, usecases.Config{Logger: testLogger()})
	return &fixture{store: s, svc: svc, dlq: &fakeDLQWriter{}}
}

func (f *fixture) consumer(handlers map[string]kafkaconsumer.HandlerFunc) *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{Name: "test consumer", Topic: "src", Handlers: handlers, Logger: testLogger(), Retry: fastRetry, DLQ: f.dlq}
}

func (f *fixture) nodes() *kafkaconsumer.Consumer {
	return f.consumer(kafkaconsumer.NodeHandlers(f.svc))
}
func (f *fixture) catalog() *kafkaconsumer.Consumer {
	return f.consumer(kafkaconsumer.CatalogHandlers(f.svc))
}
func (f *fixture) receipts() *kafkaconsumer.Consumer {
	return f.consumer(kafkaconsumer.ReceiptHandlers(f.svc, testLogger()))
}

// event builds a structured-mode CloudEvents message of a neighbour's type.
func event(id, typ, subject string, data any) []byte {
	d, _ := json.Marshal(data)
	return fmt.Appendf(nil, `{"specversion":"1.0","id":%q,"source":"/retailnetwork/x","type":%q,"subject":%q,"time":"2026-10-24T13:00:00Z",`+
		`"datacontenttype":"application/json","dataschema":"urn:x","data":%s}`, id, typ, subject, d)
}

type m = map[string]any

func TestNodeEventsFeedTheNodeView(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c := f.nodes()
	for _, msg := range [][]byte{
		event("e1", kafkaconsumer.TypeNodeRegistered, "n-1", m{"nodeId": "n-1", "nodeType": "FULFILLMENT_CENTER", "subtype": "SORTABLE", "operator": "VENDOR", "somethingNew": true}),
		event("e2", kafkaconsumer.TypeProfileDeclared, "n-1", m{"nodeId": "n-1", "profileRevision": 2, "handlingTimeMinutes": 45, "timezone": "UTC",
			"inbound": m{"maxCartonWeightG": 25000, "maxCartonDimsMm": m{"l": 600, "w": 400, "h": 400}, "acceptsPallets": true, "requiresSscc": false}}),
		event("e3", kafkaconsumer.TypeNodeActivated, "n-1", m{"nodeId": "n-1", "activatedAt": "2026-10-24T13:00:00Z"}),
	} {
		if err := c.HandleMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	n, err := f.store.Nodes().Get(ctx, "n-1")
	if err != nil || n.Status != network.NodeActive || n.Type != "FULFILLMENT_CENTER" || n.ProfileRevision != 2 || n.Limits == nil ||
		n.Limits.MaxCartonWeightG != 25000 || n.Limits.MaxCartonDims.W != 400 || !*n.Limits.AcceptsPallets || *n.Limits.RequiresSscc {
		t.Fatalf("node = %+v %v", n, err)
	}
	// Ordered on purpose: RETIRED is terminal, so SUSPENDED must be applied first.
	for _, step := range []struct {
		typ  string
		want network.NodeStatus
	}{{kafkaconsumer.TypeNodeSuspended, network.NodeSuspended}, {kafkaconsumer.TypeNodeRetired, network.NodeRetired}} {
		if err := c.HandleMessage(ctx, event("s-"+string(step.want), step.typ, "n-1", m{"nodeId": "n-1"})); err != nil {
			t.Fatal(err)
		}
		if n, _ := f.store.Nodes().Get(ctx, "n-1"); n.Status != step.want {
			t.Fatalf("after %s status = %s", step.typ, n.Status)
		}
	}
}

func TestPackagingEventsFeedThePackagingView(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c := f.catalog()
	if err := c.HandleMessage(ctx, event("p1", kafkaconsumer.TypePackagingDeclared, "item-1", m{"catalogItemId": "item-1", "level": "CASE", "gtin": gtinCase,
		"childGtin": gtinEach, "unitsPerPack": 12, "declaredAt": "2026-10-24T13:00:00Z", "unknown": 1})); err != nil {
		t.Fatal(err)
	}
	rows, _ := f.store.Packaging().ListByItem(ctx, "item-1")
	if len(rows) != 1 || !rows[0].Active || rows[0].UnitsPerPack != 12 || rows[0].ChildGTIN != gtinEach {
		t.Fatalf("%+v", rows)
	}
	if err := c.HandleMessage(ctx, event("p2", kafkaconsumer.TypePackLevelRetired, "item-1", m{"catalogItemId": "item-1", "level": "CASE", "gtin": gtinCase, "retiredAt": "2026-10-24T14:00:00Z"})); err != nil {
		t.Fatal(err)
	}
	if rows, _ = f.store.Packaging().ListByItem(ctx, "item-1"); rows[0].Active {
		t.Fatalf("%+v", rows)
	}
}

func TestUnknownTypesAndForeignMessagesAreIgnored(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.store.Counts()
	for _, msg := range [][]byte{
		event("x1", "com.retailnetwork.logistics.node-topology.lane.LaneOpened", "l-1", m{"laneId": "l-1"}),
		[]byte(`{"event_id":"flat","event_type":"x"}`),
		[]byte(`not json`),
	} {
		if err := f.nodes().HandleMessage(ctx, msg); err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
	}
	if f.store.Counts() != before {
		t.Fatal("ignored messages changed the store")
	}
}

func TestMalformedAndDeterministicallyBadEventsAreDeadLettered(t *testing.T) {
	f := newFixture(t)
	bad := [][]byte{
		event("b1", kafkaconsumer.TypeNodeRegistered, "n-1", m{"nodeId": 7}),                                 // undecodable data
		event("b2", kafkaconsumer.TypeNodeRegistered, "", m{"nodeId": "", "nodeType": "FULFILLMENT_CENTER"}), // domain validation
		event("b3", kafkaconsumer.TypePackagingDeclared, "i", m{"catalogItemId": "i", "level": "BOX", "gtin": gtinCase, "unitsPerPack": 2}),
	}
	for i, msg := range bad {
		var c *kafkaconsumer.Consumer
		if i < 2 {
			c = f.nodes()
		} else {
			c = f.catalog()
		}
		reader := newFakeReader(msg)
		if err := runUntilCommits(t, c, reader, 1); err != nil && !isCancel(err) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	if got := len(f.dlq.published()); got != len(bad) {
		t.Fatalf("dead-lettered %d, want %d", got, len(bad))
	}
}

func receipt(id, shipment, node string, cartons ...m) []byte {
	return event(id, kafkaconsumer.TypeReceiptSubmitted, shipment, m{"transactionId": "tx-1", "vendorId": "V-1", "nodeId": node, "shipmentId": shipment,
		"receivedAt": "2026-10-25T09:30:00Z", "cartons": cartons, "extra": "ignored"})
}

func TestReceiptForAnUnknownShipmentIsDeadLettered(t *testing.T) {
	f := newFixture(t)
	reader := newFakeReader(receipt("r1", "ghost", "n-1", m{"sscc": "006141410000000012", "status": "RECEIVED", "items": []m{}, "complianceCodes": []string{}}))
	if err := runUntilCommits(t, f.receipts(), reader, 1); err != nil && !isCancel(err) {
		t.Fatal(err)
	}
	if got := len(f.dlq.published()); got != 1 {
		t.Fatalf("dead-lettered %d", got)
	}
}

func TestReceiptHandlersMapTheWirePayload(t *testing.T) {
	f := newFixture(t)
	h := kafkaconsumer.ReceiptHandlers(f.svc, testLogger())
	if _, ok := h[kafkaconsumer.TypeReceiptSubmitted]; !ok || len(h) != 1 {
		t.Fatalf("handlers: %v", reflect.ValueOf(h).MapKeys())
	}
	// A receipt reaches the use case: the unknown shipment surfaces as the permanent error.
	err := f.receipts().HandleMessage(context.Background(), receipt("r1", "ghost", "n-1", m{"sscc": "006141410000000012", "status": "DAMAGED",
		"items": []m{{"gtin": gtinCase, "qtyEaches": 3}}, "complianceCodes": []string{"X"}}))
	if err == nil {
		t.Fatal("an unknown shipment must not be silently acknowledged")
	}
}
