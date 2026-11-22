//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inboundkafka "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/seller-inbound/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/seller-inbound/internal/adapters/outbound/kafka"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/testing/kafkatest"
)

// The whole analytics read side (ADR 0017) against real infrastructure, all
// started with testcontainers (never skip-gated, never a DSN from env): the
// REAL encoder shapes the CloudEvents the service publishes, they are written
// to a real Kafka (the events and the analytics topics), the projector's
// consumer projects BOTH topics into a SEPARATE analytical Postgres, and the
// reports answer from it over a read-only pool -- while a legacy message, an
// unknown type, a poison message and a duplicate share the topics. The DLQ is
// read back, and a later marker proves the duplicate was consumed, not stuck.

func e2eAnalyticalPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("seller_inbound_analytics"), tcpostgres.WithUsername("itest"), tcpostgres.WithPassword("itest"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		t.Fatalf("start the analytical postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

func e2eAt(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }

// routed is one encoded message and the test topic it belongs on.
type routed struct {
	topic string
	msg   kafkatest.Message
}

// published encodes events with the real encoder and returns, per message, the
// topic the service would write it to (mapped onto the test's own topics).
func published(t *testing.T, topics map[string]string, events ...shared.Event) []routed {
	t.Helper()
	msgs, err := outboundkafka.NewEncoder().Encode(events...)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]routed, len(msgs))
	for i, m := range msgs {
		if topics[m.Topic] == "" {
			t.Fatalf("the encoder routed to %s, which the test has no topic for", m.Topic)
		}
		out[i] = routed{topic: topics[m.Topic], msg: kafkatest.Message{Key: string(m.Key), Value: m.Value}}
	}
	return out
}

func produceAll(t *testing.T, msgs ...routed) {
	t.Helper()
	for _, m := range msgs {
		kafkatest.Produce(t, m.topic, m.msg)
	}
}

func hand(t *testing.T, id, typ, subject string, data map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/seller-inbound", "type": typ, "subject": subject,
		"datacontenttype": "application/json", "time": e2eAt(3, 13).Format(time.RFC3339), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

type e2eDay struct {
	Day               string  `json:"day"`
	PlansConfirmed    int     `json:"inbound_plans_confirmed"`
	AdvisoriesIssued  int     `json:"placement_advisories_issued"`
	ShipmentsReceived int     `json:"shipments_received"`
	ReceivedEaches    int     `json:"received_eaches"`
	ReceivedItemCount int     `json:"received_item_count"`
	LatencySamples    int     `json:"advisory_to_receipt_samples"`
	LatencyP50        float64 `json:"advisory_to_receipt_p50_seconds"`
	LatencyP95        float64 `json:"advisory_to_receipt_p95_seconds"`
	ReceivedByItem    []struct {
		GTIN string `json:"gtin"`
	} `json:"received_by_item"`
}

func TestAnalyticsReadSide_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	brokers := kafkatest.Brokers(t)
	analyticalURL := e2eAnalyticalPostgres(t)
	if err := analyticsstore.RunMigrations(analyticalURL); err != nil {
		t.Fatal(err)
	}
	eventsTopic := kafkatest.Topic(t, inboundkafka.OwnEventsTopic)
	analyticsTopic := kafkatest.Topic(t, inboundkafka.OwnAnalyticsTopic)
	dlqTopic := kafkatest.Topic(t, inboundkafka.AnalyticsDLQTopic)
	topics := map[string]string{outboundkafka.TopicEvents: eventsTopic, outboundkafka.TopicAnalytics: analyticsTopic}

	// ---- the facts, shaped by the real encoder ----
	plan := inboundplan.InboundPlanConfirmed{Header: shared.Header{Version: 2, At: e2eAt(3, 8)}, PlanID: "pln-1", SellerID: "seller-1",
		DestinationNodeID: "node-1", Packing: inboundplan.Individual, PlacementOption: inboundplan.SellerSplit}
	advisory := placementplan.PlacementPlanSuggested{Header: shared.Header{Version: 1, At: e2eAt(3, 6)}, PlanID: "plp-1", SellerID: "seller-1",
		CatalogItemID: "item-1", Basis: "demand-heat-v1", CreatedAt: e2eAt(3, 6)}
	receipt := inboundshipment.InboundShipmentReceived{Header: shared.Header{Version: 4, At: e2eAt(3, 10)}, ShipmentID: "shp-1", SellerID: "seller-1",
		NodeID: "node-1", Status: inboundshipment.Closed, ReceivedAt: e2eAt(3, 10), Received: []inboundshipment.ReceivedLine{
			{CatalogItemID: "item-1", GTIN: "00614141000012", QtyEaches: 10}, {GTIN: "00614141000029", QtyEaches: 4}}}
	facts := published(t, topics, plan, advisory, receipt)

	// ---- the topics also carry messages the projector must survive ----
	legacy := []byte(`{"event_id":"e-1","event_type":"InboundShipmentReceived","payload":{"shipmentId":"x"}}`)
	unknown := hand(t, "evt-unknown", cloudevents.Type("inboundshipment", "InboundShipmentTeleported"), "shp-u", map[string]any{"shipmentId": "shp-u"})
	poison := hand(t, "evt-poison", cloudevents.Type("inboundshipment", "InboundShipmentReceived"), "shp-p", // subject != aggregate id
		map[string]any{"shipmentId": "shp-OTHER", "sellerId": "seller-1", "receivedAt": e2eAt(3, 13).Format(time.RFC3339)})
	kafkatest.Produce(t, eventsTopic, kafkatest.Message{Key: "x", Value: legacy}, kafkatest.Message{Key: "shp-u", Value: unknown},
		kafkatest.Message{Key: "shp-p", Value: poison})
	produceAll(t, facts...)

	// ---- the projector: ONE consumer over BOTH own topics + the writer pool ----
	writer, err := analyticsstore.NewPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(writer.Close)
	consumer := inboundkafka.NewAnalyticsConsumer(brokers, []string{eventsTopic, analyticsTopic}, dlqTopic,
		fmt.Sprintf("si-projector-e2e-%d", time.Now().UnixNano()), analyticsstore.NewProjection(writer), quiet)
	consumer.Retry = inboundkafka.RetryPolicy{Initial: 50 * time.Millisecond, Max: 200 * time.Millisecond}
	consumerDone := make(chan struct{})
	go func() { defer close(consumerDone); _ = consumer.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-consumerDone; _ = consumer.Close() })

	// ---- the reports over a READ-ONLY pool ----
	readOnly, err := analyticsstore.NewReadOnlyPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnly.Close)
	reader := analyticsstore.NewReader(readOnly)
	srv := httptest.NewServer(reportshttp.NewRouter(&reportshttp.Server{Reader: reader, Ping: reader, Now: func() time.Time { return e2eAt(6, 0) }}, quiet, "e2e"))
	t.Cleanup(srv.Close)
	report := func() []e2eDay {
		var out struct {
			Days []e2eDay `json:"days"`
		}
		getJSON(t, srv.URL+"/reports/seller-inbound-performance?from=2026-11-03&to=2026-11-05", &out)
		return out.Days
	}
	want3 := e2eDay{Day: "2026-11-03", PlansConfirmed: 1, AdvisoriesIssued: 1, ShipmentsReceived: 1, ReceivedEaches: 14, ReceivedItemCount: 2,
		LatencySamples: 1, LatencyP50: 4 * 3600, LatencyP95: 4 * 3600, ReceivedByItem: []struct {
			GTIN string `json:"gtin"`
		}{{"00614141000012"}, {"00614141000029"}}}
	waitUntil(t, "the three facts projected", func() bool { d := report(); return len(d) == 1 && reflect.DeepEqual(d[0], want3) })
	assertOnlyThePoisonWasDeadLettered(t, brokers, dlqTopic, poison)

	// ---- a redelivered receipt (same id) changes nothing; a later marker on the same topic proves it was consumed ----
	marker := placementplan.PlacementPlanSuggested{Header: shared.Header{Version: 1, At: e2eAt(4, 9)}, PlanID: "plp-2", SellerID: "seller-2",
		CatalogItemID: "item-9", Basis: "demand-heat-v1", CreatedAt: e2eAt(4, 9)}
	kafkatest.Produce(t, eventsTopic, facts[2].msg)
	produceAll(t, published(t, topics, marker)...)
	waitUntil(t, "the marker projected", func() bool { d := report(); return len(d) == 2 && d[1].AdvisoriesIssued == 1 })
	if d := report(); !reflect.DeepEqual(d[0], want3) {
		t.Errorf("after a redelivered receipt day 3 = %+v, want it unchanged %+v (idempotent on the CloudEvents id)", d[0], want3)
	}
	var fresh struct {
		AsOf *time.Time `json:"as_of"`
	}
	getJSON(t, srv.URL+"/reports/freshness", &fresh)
	if fresh.AsOf == nil || !fresh.AsOf.Equal(e2eAt(4, 9)) {
		t.Errorf("freshness as_of = %v, want the newest event time %v", fresh.AsOf, e2eAt(4, 9))
	}
}

// assertOnlyThePoisonWasDeadLettered reads the DLQ: exactly the poison bytes,
// nothing for the legacy or unknown-type messages.
func assertOnlyThePoisonWasDeadLettered(t *testing.T, brokers []string, dlqTopic string, poison []byte) {
	t.Helper()
	dlq := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: dlqTopic, Partition: 0, MinBytes: 1, MaxBytes: 10e6})
	defer func() { _ = dlq.Close() }()
	readCtx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	m, err := dlq.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("nothing reached the DLQ: %v", err)
	}
	if !bytes.Equal(m.Value, poison) {
		t.Errorf("DLQ value differs from the poison message:\n%s", m.Value)
	}
	quietCtx, quietStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer quietStop()
	if extra, err := dlq.ReadMessage(quietCtx); err == nil {
		t.Errorf("a second message reached the DLQ (legacy and unknown types are skipped, not dead-lettered): %s", extra.Value)
	}
}
