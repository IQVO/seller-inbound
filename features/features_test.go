// Package features_test hosts the godog (Gherkin) acceptance suite. It drives
// the REAL chi router over HTTP and feeds the local views and the vendor
// receipts through the REAL consumers' HandleMessage with raw CloudEvents (the
// producers' own contracts), over the in-memory doubles wired the way cmd/api
// wires the real adapters. Every scenario in features/*.feature is a black-box
// test of the service. The runner lives in features/, never at the repo root.
package features_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	kafkago "github.com/segmentio/kafka-go"

	inboundhttp "github.com/claudioed/seller-inbound/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
)

// TestFeatures runs every Gherkin feature in this directory.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "progress",
			Paths:    []string{"."},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// start is the service clock at the beginning of every scenario.
var start = time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)

// world is the per-scenario state.
type world struct {
	server *httptest.Server
	store  *memory.Store
	clock  *memory.Clock
	svc    *usecases.Service
	nodes  *inboundkafka.Consumer
	catalg *inboundkafka.Consumer
	receip *inboundkafka.Consumer

	status int
	body   []byte

	planID, shipmentID string
	sscc               []string // SSCCs of the cartons added through the API, in order
	lastKey            int
	eventSeq           int
	feedErr            error
}

func newWorld() *world {
	w := &world{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	w.store = memory.NewStore()
	w.clock = memory.NewClock(start)
	s := w.store
	w.svc = usecases.New(usecases.Deps{
		UoW: s.UnitOfWork(), Sellers: s.Sellers(), Plans: s.Plans(), Shipments: s.Shipments(), Nodes: s.Nodes(), Scorecard: s.Scorecard(), Packaging: s.Packaging(),
		Outbox: s.Outbox(), Inbox: s.Inbox(), Clock: w.clock, IDs: &memory.SequentialIDs{}, SSCC: &memory.SsccIssuer{},
	}, usecases.Config{Logger: logger})
	w.nodes = &inboundkafka.Consumer{Name: "node", Topic: "nodes", Handlers: inboundkafka.NodeHandlers(w.svc), Logger: logger}
	w.catalg = &inboundkafka.Consumer{Name: "catalog", Topic: "catalog", Handlers: inboundkafka.CatalogHandlers(w.svc), Logger: logger}
	w.receip = &inboundkafka.Consumer{Name: "receipt", Topic: "receipts", Handlers: inboundkafka.ReceiptHandlers(w.svc, logger), Logger: logger, DLQ: &recordingDLQ{}}
	w.server = httptest.NewServer(inboundhttp.NewRouter(&inboundhttp.Server{Service: w.svc}))
	return w
}

// InitializeScenario wires the steps over a fresh world per scenario.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.server.Close()
		return ctx, nil
	})
	registerGiven(sc, w)
	registerWhen(sc, w)
	registerThen(sc, w)
}

// --- helpers -----------------------------------------------------------------

func (w *world) eventID() string {
	w.eventSeq++
	return fmt.Sprintf("evt-%04d", w.eventSeq)
}

func cloudEvent(id, typ, subject, data string) []byte {
	return fmt.Appendf(nil, `{"specversion":"1.0","id":%q,"source":"/retailnetwork/test","type":%q,"subject":%q,"time":"2026-10-24T13:00:00Z","datacontenttype":"application/json","dataschema":"urn:test","data":%s}`,
		id, typ, subject, data)
}

// feed hands one raw CloudEvent to a consumer, as the Kafka loop would.
func (w *world) feed(c *inboundkafka.Consumer, typ, subject, data string) error {
	return c.HandleMessage(context.Background(), cloudEvent(w.eventID(), typ, subject, data))
}

// expand replaces {plan}, {shipment} and {sscc:N} (the N-th carton added, from 1).
func (w *world) expand(s string) string {
	s = strings.ReplaceAll(s, "{plan}", w.planID)
	s = strings.ReplaceAll(s, "{shipment}", w.shipmentID)
	for i, v := range w.sscc {
		s = strings.ReplaceAll(s, "{sscc:"+strconv.Itoa(i+1)+"}", v)
	}
	return s
}

func (w *world) request(method, path, body string, headers ...string) error {
	var rd io.Reader = http.NoBody
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, w.server.URL+path, rd)
	if err != nil {
		return err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	w.status = resp.StatusCode
	if w.body, err = io.ReadAll(resp.Body); err != nil {
		return err
	}
	w.remember()
	return nil
}

// remember keeps the ids and SSCCs a response carries for later placeholders.
func (w *world) remember() {
	var doc struct {
		PlanID     string `json:"planId"`
		ShipmentID string `json:"shipmentId"`
		Carton     struct {
			SSCC string `json:"sscc"`
		} `json:"carton"`
	}
	if json.Unmarshal(w.body, &doc) != nil || w.status >= 300 {
		return
	}
	if doc.PlanID != "" && doc.ShipmentID == "" {
		w.planID = doc.PlanID
	}
	if doc.ShipmentID != "" {
		w.shipmentID = doc.ShipmentID
	}
	if doc.Carton.SSCC != "" {
		w.sscc = append(w.sscc, doc.Carton.SSCC)
	}
}

// write sends a write with a fresh Idempotency-Key header (the key is enforced
// by the Postgres middleware; the doubles run without it).
func (w *world) write(method, path, body string) error {
	w.lastKey++
	return w.request(method, w.expand(path), w.expand(body), "Idempotency-Key", "bdd-"+strconv.Itoa(w.lastKey))
}

// field walks a dotted path ("labels.0.gs1128") through the last response.
func (w *world) field(path string) (any, bool, error) {
	var doc any
	if err := json.Unmarshal(w.body, &doc); err != nil {
		return nil, false, fmt.Errorf("response is not JSON: %s", w.body)
	}
	cur := doc
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, false, nil
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false, nil
			}
			cur = node[i]
		default:
			return nil, false, nil
		}
	}
	return cur, true, nil
}

func render(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func (w *world) eventNames() []string {
	var names []string
	for _, e := range w.store.Outbox().Events() {
		names = append(names, e.EventName())
	}
	return names
}

// recordingDLQ stands in for the dead-letter writer: its presence makes a
// payload that can never be applied surface as a poison error, which the Kafka
// loop dead-letters.
type recordingDLQ struct{}

func (*recordingDLQ) WriteMessages(context.Context, ...kafkago.Message) error { return nil }
func (*recordingDLQ) Close() error                                            { return nil }
