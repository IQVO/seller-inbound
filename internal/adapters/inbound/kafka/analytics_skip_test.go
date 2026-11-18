package kafka

import (
	"context"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/seller-inbound/internal/adapters/kafka/cloudevents"
)

// warnCounter counts the WARN records and keeps the last one's attributes.
type warnCounter struct {
	mu    sync.Mutex
	warns int
	last  map[string]any
}

func (w *warnCounter) Enabled(context.Context, slog.Level) bool { return true }

func (w *warnCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level != slog.LevelWarn {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warns++
	w.last = map[string]any{}
	r.Attrs(func(a slog.Attr) bool { w.last[a.Key] = a.Value.Any(); return true })
	return nil
}

func (w *warnCounter) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w *warnCounter) WithGroup(string) slog.Handler      { return w }

func TestAnalyticsConsumerSkipsNonCloudEventsAndUnknownTypesWithASampledWarn(t *testing.T) {
	warns := &warnCounter{}
	p, dlq := &fakeProjection{}, &runDLQ{}
	c := analyticsConsumer(p, dlq, slog.New(warns))
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return clock }
	skip := []kafkago.Message{
		{Value: []byte(`{"event_id":"e-1","event_type":"InboundShipmentReceived"}`)},
		{Value: []byte("not json")},
		{Value: ownEvent(t, entityInboundShipment, "InboundShipmentTeleported", cloudevents.StreamEvents, "shp-1", map[string]any{"shipmentId": "shp-1"})},
	}
	for _, m := range skip {
		if err := c.HandleMessage(context.Background(), m); err != nil {
			t.Fatalf("a skipped message must be committed past, got %v", err)
		}
	}
	if warns.warns != 1 {
		t.Fatalf("warns = %d, want 1 (sampled)", warns.warns)
	}
	clock = clock.Add(59 * time.Second)
	_ = c.HandleMessage(context.Background(), skip[0])
	if warns.warns != 1 {
		t.Fatalf("a skip inside the interval must stay silent, warns = %d", warns.warns)
	}
	clock = clock.Add(time.Second)
	_ = c.HandleMessage(context.Background(), skip[1])
	if warns.warns != 2 || warns.last["suppressed_since_last_warning"] != int64(3) {
		t.Fatalf("warns = %d last = %v, want a second warning reporting 3 suppressed", warns.warns, warns.last)
	}
	if len(p.events) != 0 || len(dlq.msgs) != 0 {
		t.Fatalf("a skipped message is neither projected nor dead-lettered: applied=%d dlq=%d", len(p.events), len(dlq.msgs))
	}
}

func TestAnalyticsConsumerAcknowledgesTheUnprojectedOwnTypesSilently(t *testing.T) {
	warns := &warnCounter{}
	p, dlq := &fakeProjection{}, &runDLQ{}
	c := analyticsConsumer(p, dlq, slog.New(warns))
	for _, m := range []struct{ entity, name, stream string }{
		{entityInboundShipment, "InboundShipmentLabelled", cloudevents.StreamEvents}, {entityInboundShipment, "InboundShipmentShipped", cloudevents.StreamEvents},
		{entityInboundShipment, "InboundDiscrepancyRecorded", cloudevents.StreamAnalytics}, {entitySeller, "SellerOnboarded", cloudevents.StreamAnalytics},
		{entitySeller, "SellerSuspended", cloudevents.StreamEvents}, {entitySeller, "SellerReinstated", cloudevents.StreamEvents},
		{entitySeller, "SellerComplianceViolationRecorded", cloudevents.StreamAnalytics},
	} {
		msg := kafkago.Message{Value: ownEvent(t, m.entity, m.name, m.stream, "subject-1", map[string]any{"sellerId": "seller-1"})}
		if err := c.HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
	}
	if warns.warns != 0 || len(p.events) != 0 || len(dlq.msgs) != 0 {
		t.Fatalf("warns=%d applied=%d dlq=%d, want all zero", warns.warns, len(p.events), len(dlq.msgs))
	}
}

func TestSkipWarnInterval(t *testing.T) {
	if skipWarnEvery() != time.Minute {
		t.Fatalf("skipWarnEvery = %v", skipWarnEvery())
	}
}

func TestNewAnalyticsConsumerWiring(t *testing.T) {
	c := NewAnalyticsConsumer([]string{"localhost:1"}, OwnTopics(), AnalyticsDLQTopic, "group-x", &fakeProjection{}, nil)
	if c.Topic != OwnEventsTopic || c.DLQTopic != AnalyticsDLQTopic || c.Reader == nil || c.DLQ == nil || c.Logger == nil {
		t.Fatalf("consumer = %+v", c)
	}
	if OwnEventsTopic != "retailnetwork.seller-inbound.events" || OwnAnalyticsTopic != "retailnetwork.seller-inbound.analytics" ||
		AnalyticsDLQTopic != "retailnetwork.seller-inbound.analytics.dlq" {
		t.Fatal("the projector reads its own two topics and dead-letters to the analytics DLQ")
	}
	if !reflect.DeepEqual(OwnTopics(), []string{OwnEventsTopic, OwnAnalyticsTopic}) {
		t.Fatalf("OwnTopics = %v", OwnTopics())
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupReaderConfigSubscribesToEveryTopicUnderOneGroup(t *testing.T) {
	cfg := groupReaderConfig([]string{"b:9092"}, OwnTopics(), "group-x")
	if cfg.GroupID != "group-x" || cfg.Topic != "" || !reflect.DeepEqual(cfg.GroupTopics, OwnTopics()) || cfg.CommitInterval != 0 ||
		cfg.MaxWait != readerMaxWait || !reflect.DeepEqual(cfg.Brokers, []string{"b:9092"}) {
		t.Fatalf("reader config = %+v", cfg)
	}
}
