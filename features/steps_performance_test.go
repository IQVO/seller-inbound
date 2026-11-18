package features_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"time"

	"github.com/cucumber/godog"
	kafkago "github.com/segmentio/kafka-go"

	inboundkafka "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/seller-inbound/internal/adapters/outbound/kafka"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// reportsWorld is the state of the analytics scenarios: the REAL analytics
// consumer folding events (shaped by the REAL encoder) into the in-memory twin
// of the analytical store, and the REAL reports router over it.
type reportsWorld struct {
	store    *analyticsstore.MemoryStore
	consumer *inboundkafka.AnalyticsConsumer
	lastMsg  []byte
	status   int
	body     map[string]any
	seq      int
}

// reportsNow is the clock of the reports in the scenarios.
var reportsNow = time.Date(2026, 11, 8, 18, 30, 0, 0, time.UTC)

func newReportsWorld() *reportsWorld {
	store := analyticsstore.NewMemoryStore()
	quiet := slog.New(slog.DiscardHandler)
	return &reportsWorld{
		store:    store,
		consumer: &inboundkafka.AnalyticsConsumer{Projection: store, Topic: inboundkafka.OwnEventsTopic, DLQTopic: inboundkafka.AnalyticsDLQTopic, Logger: quiet},
	}
}

func parseInstant(s string) (time.Time, error) { return time.Parse(time.RFC3339, s) }

// feed encodes one domain event with the real encoder and hands it to the real
// consumer, as the Kafka loop would.
func (r *reportsWorld) feed(ev shared.Event) error {
	r.seq++
	enc := &outboundkafka.Encoder{NewID: func() string { return fmt.Sprintf("bdd-report-%04d", r.seq) }}
	msgs, err := enc.Encode(ev)
	if err != nil {
		return err
	}
	r.lastMsg = msgs[0].Value
	return r.consumer.HandleMessage(context.Background(), kafkago.Message{Topic: msgs[0].Topic, Value: r.lastMsg})
}

func (r *reportsWorld) get(path string) error {
	srv := &reportshttp.Server{Reader: r.store, Ping: r.store, Now: func() time.Time { return reportsNow }}
	h := reportshttp.NewRouter(srv, slog.New(slog.DiscardHandler), "bdd")
	ts := httptest.NewServer(h)
	defer ts.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	r.status = resp.StatusCode
	r.body = map[string]any{}
	if err := json.Unmarshal(raw, &r.body); err != nil {
		return fmt.Errorf("the report body is not JSON: %s", raw)
	}
	return nil
}

// day is the row of one UTC day of the last report.
func (r *reportsWorld) day(date string) (map[string]any, error) {
	days, _ := r.body["days"].([]any)
	for _, d := range days {
		if row, ok := d.(map[string]any); ok && row["day"] == date {
			return row, nil
		}
	}
	return nil, fmt.Errorf("the report has no row for %s: %v", date, r.body)
}

func (r *reportsWorld) planConfirmed(plan, seller, at string) error {
	t, err := parseInstant(at)
	if err != nil {
		return err
	}
	return r.feed(inboundplan.InboundPlanConfirmed{Header: shared.Header{Version: 2, At: t}, PlanID: inboundplan.ID(plan), SellerID: seller,
		DestinationNodeID: "n-1", Packing: inboundplan.Individual, PlacementOption: inboundplan.SellerSplit})
}

func (r *reportsWorld) advisory(seller, item, at string) error {
	t, err := parseInstant(at)
	if err != nil {
		return err
	}
	r.seq++
	return r.feed(placementplan.PlacementPlanSuggested{Header: shared.Header{Version: 1, At: t}, PlanID: placementplan.ID(fmt.Sprintf("plp-%d", r.seq)),
		SellerID: seller, CatalogItemID: item, Basis: "demand-heat-v1", CreatedAt: t})
}

func (r *reportsWorld) received(shipment, seller, at string, table *godog.Table) error {
	t, err := parseInstant(at)
	if err != nil {
		return err
	}
	lines := []inboundshipment.ReceivedLine{}
	if table != nil {
		for _, row := range tableRows(table) {
			var qty int
			if _, err := fmt.Sscan(row[2], &qty); err != nil {
				return fmt.Errorf("qtyEaches %q: %w", row[2], err)
			}
			lines = append(lines, inboundshipment.ReceivedLine{GTIN: row[0], CatalogItemID: row[1], QtyEaches: qty})
		}
	}
	return r.feed(inboundshipment.InboundShipmentReceived{Header: shared.Header{Version: 3, At: t}, ShipmentID: inboundshipment.ID(shipment),
		SellerID: seller, NodeID: "n-1", Status: inboundshipment.Closed, ReceivedAt: t, Received: lines})
}

func registerPerformanceSteps(sc *godog.ScenarioContext) {
	r := newReportsWorld()
	registerPerformanceGivens(sc, r)
	registerPerformanceWhens(sc, r)
	registerPerformanceThens(sc, r)
}

func registerPerformanceGivens(sc *godog.ScenarioContext, r *reportsWorld) {
	sc.Step(`^the plan "([^"]*)" of seller "([^"]*)" was confirmed at "([^"]*)"$`, r.planConfirmed)
	sc.Step(`^seller "([^"]*)" got a placement advisory for item "([^"]*)" at "([^"]*)"$`, r.advisory)
	sc.Step(`^the shipment "([^"]*)" of seller "([^"]*)" was received at "([^"]*)" with these items:$`, r.received)
	sc.Step(`^the shipment "([^"]*)" of seller "([^"]*)" was received at "([^"]*)" without item lines$`, func(shipment, seller, at string) error {
		return r.received(shipment, seller, at, nil)
	})
	sc.Step(`^the last event is delivered again$`, func() error {
		return r.consumer.HandleMessage(context.Background(), kafkago.Message{Topic: inboundkafka.OwnAnalyticsTopic, Value: r.lastMsg})
	})
	sc.Step(`^a message that is not a CloudEvent is consumed$`, func() error {
		return r.consumer.HandleMessage(context.Background(), kafkago.Message{Topic: inboundkafka.OwnEventsTopic, Value: []byte(`{"event_id":"legacy-1"}`)})
	})
}

func registerPerformanceWhens(sc *godog.ScenarioContext, r *reportsWorld) {
	sc.Step(`^I request the seller-inbound performance report from "([^"]*)" to "([^"]*)"$`, func(from, to string) error {
		return r.get("/reports/seller-inbound-performance?from=" + from + "&to=" + to)
	})
	sc.Step(`^I request the report freshness$`, func() error { return r.get("/reports/freshness") })
}

func registerPerformanceThens(sc *godog.ScenarioContext, r *reportsWorld) {
	sc.Step(`^the report status is (\d+)$`, func(code int) error {
		if r.status != code {
			return fmt.Errorf("status = %d, want %d: %v", r.status, code, r.body)
		}
		return nil
	})
	sc.Step(`^the report problem type is "([^"]*)"$`, func(slug string) error {
		if r.body["type"] != slug {
			return fmt.Errorf("problem = %v, want type %s", r.body, slug)
		}
		return nil
	})
	sc.Step(`^the report covers "([^"]*)" to "([^"]*)" with (\d+) days$`, func(from, to string, n int) error {
		days, ok := r.body["days"].([]any)
		if r.body["from"] != from || r.body["to"] != to || !ok || len(days) != n {
			return fmt.Errorf("report = %v, want %s..%s with %d days (an empty array, never null)", r.body, from, to, n)
		}
		return nil
	})
	sc.Step(`^the report has the days "([^"]*)"$`, func(want string) error {
		var got []string
		for _, d := range r.body["days"].([]any) {
			got = append(got, d.(map[string]any)["day"].(string))
		}
		if strings.Join(got, ", ") != want {
			return fmt.Errorf("days = %v, want %s", got, want)
		}
		return nil
	})
	registerDayThens(sc, r)
	sc.Step(`^the freshness has no event yet$`, func() error {
		if v, ok := r.body["as_of"]; !ok || v != nil {
			return fmt.Errorf("as_of = %v (present %v), want an explicit null", v, ok)
		}
		if v, ok := r.body["lag_seconds"]; !ok || v != nil {
			return fmt.Errorf("lag_seconds = %v (present %v), want an explicit null", v, ok)
		}
		return nil
	})
	sc.Step(`^the freshness as of is "([^"]*)"$`, func(want string) error {
		if r.body["as_of"] != want {
			return fmt.Errorf("freshness = %v, want as_of %s", r.body, want)
		}
		return nil
	})
}

func registerDayThens(sc *godog.ScenarioContext, r *reportsWorld) {
	sc.Step(`^the day "([^"]*)" has "([^"]*)" (\d+)$`, func(date, field string, want int) error {
		row, err := r.day(date)
		if err != nil {
			return err
		}
		if row[field] != float64(want) {
			return fmt.Errorf("%s %s = %v, want %d", date, field, row[field], want)
		}
		return nil
	})
	sc.Step(`^the day "([^"]*)" has "([^"]*)" null$`, func(date, field string) error {
		row, err := r.day(date)
		if err != nil {
			return err
		}
		if v, ok := row[field]; !ok || v != nil {
			return fmt.Errorf("%s %s = %v (present %v), want an explicit null", date, field, v, ok)
		}
		return nil
	})
	sc.Step(`^the day "([^"]*)" lists no received items$`, func(date string) error {
		row, err := r.day(date)
		if err != nil {
			return err
		}
		if items, ok := row["received_by_item"].([]any); !ok || len(items) != 0 {
			return fmt.Errorf("%s received_by_item = %v, want []", date, row["received_by_item"])
		}
		return nil
	})
	sc.Step(`^the day "([^"]*)" lists the received GTINs "([^"]*)"$`, func(date, want string) error {
		row, err := r.day(date)
		if err != nil {
			return err
		}
		var got []string
		for _, it := range row["received_by_item"].([]any) {
			got = append(got, it.(map[string]any)["gtin"].(string))
		}
		if !reflect.DeepEqual(got, strings.Split(want, ", ")) {
			return fmt.Errorf("%s received GTINs = %v, want %s", date, got, want)
		}
		return nil
	})
}
