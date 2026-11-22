package kafka

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/seller-inbound/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// fakeProjection records the applied events; errs are returned one per Apply
// call first (nil entries succeed).
type fakeProjection struct {
	mu     sync.Mutex
	events []report.Event
	errs   []error
}

func (f *fakeProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return false, err
		}
	}
	f.events = append(f.events, e)
	return true, nil
}

var (
	eventTime   = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	businessAt  = time.Date(2026, 11, 3, 11, 30, 0, 0, time.UTC)
	businessRFC = "2026-11-03T11:30:00Z"
)

// ownEvent encodes one of this context's events through the shared envelope
// builder (the one the outbox encoder uses).
func ownEvent(t *testing.T, entity, name, stream, subject string, data map[string]any) []byte {
	t.Helper()
	b, err := cloudevents.New(cloudevents.Spec{ID: "evt-" + name, Entity: entity, EventName: name, Subject: subject,
		Time: eventTime, Stream: stream, Version: 1, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// receiptEvent is an InboundShipmentReceived carrying one line.
func receiptEvent(t *testing.T, subject string, data map[string]any) []byte {
	t.Helper()
	return ownEvent(t, entityInboundShipment, "InboundShipmentReceived", cloudevents.StreamEvents, subject, data)
}

func receiptData() map[string]any {
	return map[string]any{"shipmentId": "shp-1", "sellerId": "seller-1", "nodeId": "node-1", "status": "CLOSED", "receivedAt": businessRFC,
		"cartons":  []map[string]any{{"sscc": "006141410000000012", "status": "RECEIVED"}},
		"received": []map[string]any{{"catalogItemId": "item-1", "gtin": "00614141000012", "qtyEaches": 12}, {"gtin": "00614141000036", "qtyEaches": 3}},
	}
}

func analyticsConsumer(p report.Projection, dlq DeadLetterWriter, logger *slog.Logger) *AnalyticsConsumer {
	if logger == nil {
		logger = quietLogger()
	}
	return &AnalyticsConsumer{Projection: p, DLQ: dlq, Topic: OwnEventsTopic, DLQTopic: AnalyticsDLQTopic, Logger: logger}
}

func TestAnalyticsConsumerProjectsTheThreeTypes(t *testing.T) {
	cases := []struct {
		name  string
		value []byte
		want  report.Event
	}{
		{"InboundPlanConfirmed", ownEvent(t, entityInboundPlan, "InboundPlanConfirmed", cloudevents.StreamAnalytics, "pln-1",
			map[string]any{"planId": "pln-1", "sellerId": "seller-1", "destinationNodeId": "node-1", "packingOption": "CASE_PACKED", "confirmedAt": businessRFC, "lines": []any{}}),
			report.Event{Kind: report.KindInboundPlanConfirmed, SubjectID: "pln-1", SellerID: "seller-1"}},
		{"PlacementPlanSuggested", ownEvent(t, entityPlacementPlan, "PlacementPlanSuggested", cloudevents.StreamEvents, "plp-1",
			map[string]any{"planId": "plp-1", "sellerId": "seller-1", "catalogItemId": "item-1", "basis": "demand-heat-v1", "createdAt": businessRFC, "suggestions": []any{}}),
			report.Event{Kind: report.KindPlacementPlanSuggested, SubjectID: "plp-1", SellerID: "seller-1", CatalogItemID: "item-1"}},
		{"InboundShipmentReceived", receiptEvent(t, "shp-1", receiptData()),
			report.Event{Kind: report.KindInboundShipmentReceived, SubjectID: "shp-1", SellerID: "seller-1", Lines: []report.ReceivedLine{
				{GTIN: "00614141000012", CatalogItemID: "item-1", QtyEaches: 12}, {GTIN: "00614141000036", QtyEaches: 3}}}},
	}
	for _, c := range cases {
		p, dlq := &fakeProjection{}, &runDLQ{}
		err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), kafkago.Message{Value: c.value})
		if err != nil || len(dlq.msgs) != 0 || len(p.events) != 1 {
			t.Fatalf("%s: err=%v dlq=%d applied=%d", c.name, err, len(dlq.msgs), len(p.events))
		}
		want := c.want
		want.EventID, want.At, want.OccurredAt = "evt-"+c.name, eventTime, businessAt
		if !reflect.DeepEqual(p.events[0], want) {
			t.Errorf("%s projected %+v\nwant %+v", c.name, p.events[0], want)
		}
	}
}

func TestAnalyticsConsumerProjectsAReceiptWithoutReceivedLines(t *testing.T) {
	data := receiptData()
	delete(data, "received") // a receipt published before ADR 0014
	p, dlq := &fakeProjection{}, &runDLQ{}
	err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), kafkago.Message{Value: receiptEvent(t, "shp-1", data)})
	if err != nil || len(dlq.msgs) != 0 || len(p.events) != 1 || len(p.events[0].Lines) != 0 {
		t.Fatalf("err=%v dlq=%d applied=%+v", err, len(dlq.msgs), p.events)
	}
}

func TestAnalyticsConsumerDeadLettersPoisonOnce(t *testing.T) {
	badData := func(mutate func(map[string]any)) map[string]any {
		d := receiptData()
		mutate(d)
		return d
	}
	cases := []struct {
		name string
		msg  []byte
		errs []error
	}{
		{"undecodable timestamp", receiptEvent(t, "shp-1", badData(func(d map[string]any) { d["receivedAt"] = "yesterday" })), nil},
		{"undecodable lines", receiptEvent(t, "shp-1", badData(func(d map[string]any) { d["received"] = "many" })), nil},
		{"subject is not the aggregate", receiptEvent(t, "shp-9", receiptData()), nil},
		{"a plan whose subject is not the planId", ownEvent(t, entityInboundPlan, "InboundPlanConfirmed", cloudevents.StreamAnalytics, "pln-9",
			map[string]any{"planId": "pln-1", "sellerId": "seller-1", "confirmedAt": businessRFC}), nil},
		{"a required field missing", receiptEvent(t, "shp-1", badData(func(d map[string]any) { delete(d, "sellerId") })), nil},
		{"an advisory without its item", ownEvent(t, entityPlacementPlan, "PlacementPlanSuggested", cloudevents.StreamEvents, "plp-1",
			map[string]any{"planId": "plp-1", "sellerId": "seller-1", "createdAt": businessRFC}), nil},
		{"a line of zero eaches", receiptEvent(t, "shp-1", badData(func(d map[string]any) {
			d["received"] = []map[string]any{{"gtin": "00614141000012", "qtyEaches": 0}}
		})), nil},
		{"the store rejects it", receiptEvent(t, "shp-1", receiptData()), []error{fmt.Errorf("%w: check violation", report.ErrRejected)}},
	}
	for _, c := range cases {
		p, dlq := &fakeProjection{errs: c.errs}, &runDLQ{}
		msg := kafkago.Message{Topic: OwnEventsTopic, Partition: 2, Offset: 7, Key: []byte("shp-1"), Value: c.msg}
		if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: poison must be dead-lettered and committed past, got %v", c.name, err)
		}
		if len(dlq.msgs) != 1 || len(p.events) != 0 {
			t.Fatalf("%s: dlq=%d applied=%d, want exactly one dead letter", c.name, len(dlq.msgs), len(p.events))
		}
		d := dlq.msgs[0]
		if d.Topic != AnalyticsDLQTopic || !bytes.Equal(d.Value, c.msg) || string(d.Key) != "shp-1" || dlqHeader(d, "x-dlq-source-topic") != OwnEventsTopic {
			t.Errorf("%s: dead letter = topic %s key %s headers %v", c.name, d.Topic, d.Key, d.Headers)
		}
		if dlqHeader(d, "x-dlq-error") == "" {
			t.Errorf("%s: the dead letter must carry the cause", c.name)
		}
	}
}

func dlqHeader(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestAnalyticsConsumerTransientFailureIsReturnedNotDeadLettered(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("connection refused")}}, &runDLQ{}
	msg := kafkago.Message{Value: receiptEvent(t, "shp-1", receiptData())}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), msg); err == nil {
		t.Fatal("a transient failure must be returned for the loop to retry")
	}
	if len(dlq.msgs) != 0 {
		t.Fatal("a transient failure must never be dead-lettered")
	}
}

// failingDLQ refuses every write.
type failingDLQ struct{}

func (failingDLQ) WriteMessages(context.Context, ...kafkago.Message) error {
	return errors.New("broker down")
}
func (failingDLQ) Close() error { return nil }

func TestAnalyticsConsumerFailedDeadLetterIsRetried(t *testing.T) {
	msg := kafkago.Message{Value: receiptEvent(t, "shp-9", receiptData())}
	err := analyticsConsumer(&fakeProjection{}, failingDLQ{}, nil).HandleMessage(context.Background(), msg)
	if err == nil || !strings.Contains(err.Error(), AnalyticsDLQTopic) {
		t.Fatalf("a failed dead-letter write must be returned (so the message is retried), got %v", err)
	}
}

func TestAnalyticsConsumerRunRetriesTransientFailuresForever(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("down"), errors.New("down"), errors.New("down"), errors.New("down"),
		errors.New("down"), errors.New("down"), nil}}, &runDLQ{}
	c := analyticsConsumer(p, dlq, nil)
	rec := &loopRecorder{}
	c.Reader = &scriptedReader{rec: rec, msgs: []kafkago.Message{{Offset: 1, Value: receiptEvent(t, "shp-1", receiptData())}}}
	var sleeps []time.Duration
	c.Retry = RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}
	c.sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitFor(t, rec, "commit:1", "the message is committed once the projection succeeds")
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if len(p.events) != 1 || len(dlq.msgs) != 0 {
		t.Fatalf("applied=%d dlq=%d: six transient failures must be retried past the domain cap, never dead-lettered", len(p.events), len(dlq.msgs))
	}
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond}
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff = %v, want the capped sequence %v", sleeps, want)
	}
}

// waitFor blocks until the recorder's log contains want.
func waitFor(t *testing.T, rec *loopRecorder, want, msg string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if strings.Contains(strings.Join(rec.log(), " "), want) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s; events: %v", msg, rec.log())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// runDLQ records every dead letter the analytics consumer publishes.
type runDLQ struct {
	mu   sync.Mutex
	msgs []kafkago.Message
}

func (w *runDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *runDLQ) Close() error { return nil }
