//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka with nothing but the environment a deployment sets, and drives it only
// through REST and Kafka. Node and catalogue events go in through the input
// topics, a seller plans, packs, labels and ships through the REST API, and
// the effects are read from the produced topics (events and analytics) and
// Postgres. The vendor's receipt goes in through Kafka and closes the
// shipment. /readyz then follows Kafka going away. Every Kafka read is
// bounded by a deadline; run it with -timeout 300s.

const (
	topicNodes     = "retailnetwork.node-topology.events"
	topicOffers    = "retailnetwork.offer-availability.events"
	topicVendor    = "retailnetwork.vendor-connectivity.events"
	topicEvents    = "retailnetwork.seller-inbound.events"
	topicAnalytics = "retailnetwork.seller-inbound.analytics"

	source     = "/retailnetwork/seller-inbound"
	typePrefix = "com.retailnetwork.partner.seller-inbound."
)

// containerStartTimeout bounds a testcontainers start.
const containerStartTimeout = 120 * time.Second

func startPostgres(t *testing.T) (*tcpostgres.PostgresContainer, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("seller_inbound_e2e"), tcpostgres.WithUsername("e2e"), tcpostgres.WithPassword("e2e"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return c, url
}

func startKafka(t *testing.T) (*tckafka.KafkaContainer, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("seller-inbound-e2e"))
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	return c, brokers
}

// createTopic creates a one-partition topic and waits for its leader.
func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// produce writes already-encoded messages to topic with RequireAll acks.
func produce(t *testing.T, brokers []string, topic string, msgs ...kafkago.Message) {
	t.Helper()
	transport := &kafkago.Transport{}
	defer transport.CloseIdleConnections()
	w := &kafkago.Writer{Addr: kafkago.TCP(brokers...), Topic: topic, Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll, BatchTimeout: 10 * time.Millisecond, Transport: transport}
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.WriteMessages(ctx, msgs...); err != nil {
		t.Fatalf("produce to %s: %v", topic, err)
	}
}

// inputEvent builds a CloudEvent as a sibling context publishes it.
func inputEvent(t *testing.T, id, src, typ, subject, data string) []byte {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource(src)
	e.SetType(typ)
	e.SetSubject(subject)
	e.SetTime(time.Now().UTC())
	e.SetDataSchema("urn:warehouse:e2e:events:X:v1")
	if err := e.SetData("application/json", json.RawMessage(data)); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// consume reads exactly n messages of the one-partition topic from the start,
// bounded by a deadline.
func consume(t *testing.T, brokers []string, topic string, n int) []kafkago.Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	var out []kafkago.Message
	for len(out) < n {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read %s: got %d of %d messages: %v", topic, len(out), n, err)
		}
		out = append(out, m)
	}
	return out
}

// noMoreMessages fails if the topic holds more than want messages after a
// settling period (nothing was duplicated).
func noMoreMessages(t *testing.T, brokers []string, topic string, want int) {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := 0
	for {
		if _, err := r.ReadMessage(ctx); err != nil {
			break
		}
		got++
	}
	if got != want {
		t.Fatalf("%s holds %d messages, want exactly %d", topic, got, want)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// service is one running instance of the real entry point.
type service struct {
	t      *testing.T
	base   string
	cancel context.CancelFunc
	done   chan error
}

// env is the environment a deployment sets.
type env struct {
	dbURL   string
	brokers []string
	addr    string
}

func (e env) apply(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", e.dbURL)
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", strings.Join(e.brokers, ","))
	t.Setenv("EVENT_PUBLISHER", "kafka")
	t.Setenv("NODE_CONSUMER_GROUP", "seller-inbound-node-e2e")
	t.Setenv("CATALOG_CONSUMER_GROUP", "seller-inbound-catalog-e2e")
	t.Setenv("RECEIPT_CONSUMER_GROUP", "seller-inbound-receipt-e2e")
	t.Setenv("SSCC_COMPANY_PREFIX", "0614141")
	t.Setenv("HTTP_ADDR", e.addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "200ms")
	t.Setenv("LOG_LEVEL", "warn")
}

// start boots run in-process and waits until the listener answers /healthz.
func (e env) start(t *testing.T) *service {
	t.Helper()
	e.apply(t)
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{t: t, base: "http://" + e.addr, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(s.stopQuietly)
	waitFor(t, 90*time.Second, "the service to listen on "+e.addr, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("run returned while booting: %v", err)
		default:
		}
		status, _ := s.try(http.MethodGet, "/healthz", "", "")
		return status == http.StatusOK
	})
	return s
}

func (s *service) stopQuietly() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(60 * time.Second):
	}
}

// expectExit waits for run to return on its own (nobody cancelled it) and
// returns its error.
func (s *service) expectExit() error {
	s.t.Helper()
	select {
	case err := <-s.done:
		s.done <- nil // let the cleanup find it stopped
		return err
	case <-time.After(90 * time.Second):
		s.t.Fatal("run did not return although Kafka is gone")
		return nil
	}
}

// try performs a request and returns -1 when the connection fails. A non-empty
// key is sent as the Idempotency-Key.
func (s *service) try(method, path, body, key string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		return -1, err.Error()
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return -1, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (s *service) call(method, path, body, key string, want int) string {
	s.t.Helper()
	status, raw := s.try(method, path, body, key)
	if status != want {
		s.t.Fatalf("%s %s = %d %s, want %d", method, path, status, raw, want)
	}
	return raw
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// pgView reads the service's own database.
type pgView struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func newPGView(t *testing.T, url string) pgView {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pgView{t: t, pool: pool}
}

func (v pgView) count(query string, args ...any) int {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := v.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		v.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// The CloudEvents context attributes of a published message, plus its data.
type envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            string          `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	DataSchema      string          `json:"dataschema"`
	Data            json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, m kafkago.Message) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(m.Value, &e); err != nil {
		t.Fatalf("not a CloudEvent: %v: %s", err, m.Value)
	}
	return e
}

const (
	gtinCase = "00614141000036"
	gtinEach = "00614141000012"
	ssccOwn  = "006141420000000011" // supplied by the seller: valid and unique
)

func nodeEvent(t *testing.T, id, name, node, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(node), Value: inputEvent(t, id, "/retailnetwork/node-topology", "com.retailnetwork.logistics.node-topology.node."+name, node, data)}
}

func catalogEvent(t *testing.T, id, name, item, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(item), Value: inputEvent(t, id, "/retailnetwork/offer-availability",
		"com.retailnetwork.commerce.offer-availability.catalogitem."+name, item, data)}
}

func receiptEvent(t *testing.T, id, shipment, node, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(shipment), Value: inputEvent(t, id, "/retailnetwork/vendor-connectivity",
		"com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted", shipment, data)}
}

func jsonField(t *testing.T, raw, key string) string {
	t.Helper()
	var m map[string]any
	mustDecode(t, raw, &m)
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("%s has no string field %q", raw, key)
	}
	return v
}

func mustDecode(t *testing.T, raw string, dest any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

func assertProblem(t *testing.T, body, slug string) {
	t.Helper()
	if !strings.Contains(body, `"type":"`+slug+`"`) {
		t.Fatalf("body = %s, want problem type %s", body, slug)
	}
}

func readyz(s *service) (int, string) { return s.try(http.MethodGet, "/readyz", "", "") }

func waitReadyzStatus(t *testing.T, s *service, want int, what string) string {
	t.Helper()
	var body string
	waitFor(t, 60*time.Second, what, func() bool {
		var status int
		status, body = readyz(s)
		return status == want
	})
	return body
}

func entityOf(event string) string {
	switch event {
	case "SellerOnboarded", "SellerSuspended", "SellerReinstated", "SellerComplianceViolationRecorded":
		return "seller"
	case "InboundPlanConfirmed":
		return "inboundplan"
	default:
		return "inboundshipment"
	}
}

// assertEnvelope checks every context attribute and the Kafka key/header of a message of this service.
func assertEnvelope(t *testing.T, m kafkago.Message, name, subject, stream string) envelope {
	t.Helper()
	e := decodeEnvelope(t, m)
	wantType := typePrefix + entityOf(name) + "." + name
	schema := fmt.Sprintf("urn:retailnetwork:seller-inbound:%s:%s:v1", stream, name)
	if e.SpecVersion != "1.0" || e.Type != wantType || e.Source != source || e.DataSchema != schema ||
		e.DataContentType != "application/json" || e.Subject != subject || e.ID == "" {
		t.Fatalf("envelope = %+v, want 1.0 %s from %s (%s) about %s", e, wantType, source, schema, subject)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", e.Time, err)
	}
	if string(m.Key) != subject {
		t.Fatalf("key = %q, want %q", m.Key, subject)
	}
	ct := ""
	for _, h := range m.Headers {
		if h.Key == "content-type" {
			ct = string(h.Value)
		}
	}
	if ct != "application/cloudevents+json; charset=UTF-8" {
		t.Fatalf("content-type header = %q", ct)
	}
	return e
}

func dataOf(t *testing.T, e envelope) map[string]any {
	t.Helper()
	var d map[string]any
	mustDecode(t, string(e.Data), &d)
	return d
}

// Phase 1: onboard a seller, feed the node and catalogue views through Kafka.
func phaseSetup(t *testing.T, s *service, w *world) {
	t.Helper()
	body := s.call(http.MethodPost, "/sellers", `{"sellerId":"seller-77","name":"Acme Goods"}`, "onboard-1", http.StatusCreated)
	if !strings.Contains(body, `"state":"ACTIVE"`) {
		t.Fatalf("onboard = %s", body)
	}
	if got := s.call(http.MethodPost, "/sellers", `{"sellerId":"seller-77","name":"Acme Goods"}`, "onboard-1", http.StatusCreated); got != body {
		t.Fatalf("a replayed key answers the first response: %s vs %s", got, body)
	}
	assertProblem(t, s.call(http.MethodPost, "/sellers", `{"sellerId":"seller-77","name":"Acme Goods"}`, "onboard-2", http.StatusConflict), "seller-exists")
	assertProblem(t, s.call(http.MethodPost, "/sellers", `{"sellerId":"seller-78","name":"Other"}`, "", http.StatusUnprocessableEntity), "idempotency-key-required")

	produce(t, w.brokers, topicNodes,
		nodeEvent(t, "evt-n1", "NodeRegistered", "node-1", `{"nodeId":"node-1","nodeType":"FULFILLMENT_CENTER","operator":"VENDOR","unknownField":1}`),
		nodeEvent(t, "evt-n2", "NodeOperationalProfileDeclared", "node-1", `{"nodeId":"node-1","profileRevision":1,"inbound":{"maxCartonWeightG":25000,"maxCartonDimsMm":{"l":600,"w":400,"h":400},"acceptsPallets":true}}`),
		nodeEvent(t, "evt-n3", "NodeActivated", "node-1", `{"nodeId":"node-1"}`))
	produce(t, w.brokers, topicOffers,
		catalogEvent(t, "evt-c1", "CatalogItemPackagingDeclared", "item-1",
			fmt.Sprintf(`{"catalogItemId":"item-1","level":"CASE","gtin":%q,"childGtin":%q,"unitsPerPack":12,"declaredAt":"2026-10-24T12:00:00Z"}`, gtinCase, gtinEach)))
	waitFor(t, 60*time.Second, "the node and catalogue views to be fed", func() bool {
		return w.db.count(`SELECT count(*) FROM node_inbound_views WHERE node_id='node-1' AND status='ACTIVE' AND profile_revision=1`) == 1 &&
			w.db.count(`SELECT count(*) FROM catalog_pack_levels WHERE catalog_item_id='item-1' AND level='CASE' AND active`) == 1
	})
	w.analytics = append(w.analytics, "SellerOnboarded")
}

// Phase 2: plan, confirm, open a shipment, pack two cartons, label, ship.
func phaseWorkflow(t *testing.T, s *service, w *world) {
	t.Helper()
	plan := fmt.Sprintf(`{"sellerId":"seller-77","destinationNodeId":"node-1","packingOption":"CASE_PACKED","lines":[{"catalogItemId":"item-1","gtin":%q,"qtyEaches":24}]}`, gtinCase)
	planID := jsonField(t, s.call(http.MethodPost, "/inbound-plans", plan, "plan-1", http.StatusCreated), "planId")
	assertProblem(t, s.call(http.MethodPost, "/inbound-plans", strings.Replace(plan, "node-1", "node-404", 1), "plan-bad", http.StatusConflict), "node-cannot-receive")
	s.call(http.MethodPost, "/inbound-plans/"+planID+":confirm", "", "confirm-1", http.StatusOK)
	w.analytics = append(w.analytics, "InboundPlanConfirmed")
	w.planID = planID

	w.shipmentID = jsonField(t, s.call(http.MethodPost, "/inbound-plans/"+planID+"/shipments", "", "ship-open-1", http.StatusCreated), "shipmentId")
	url := "/inbound-shipments/" + w.shipmentID
	carton := func(extra string, qty int) string {
		return fmt.Sprintf(`{%s"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":"item-1","gtin":%q,"qtyEaches":%d}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}`, extra, gtinCase, qty)
	}
	assertProblem(t, s.call(http.MethodPost, url+"/cartons", carton("", 10), "c-bad", http.StatusUnprocessableEntity), "case-qty-mismatches-gtin")
	issued := s.call(http.MethodPost, url+"/cartons", carton("", 12), "c-1", http.StatusCreated)
	if !strings.Contains(issued, `"ssccIssued":true`) || !strings.Contains(issued, `"gs1128":"(00)0`) || !strings.Contains(issued, `"destinationNodeId":"node-1"`) {
		t.Fatalf("issued carton = %s", issued)
	}
	var added struct {
		Carton struct {
			SSCC string `json:"sscc"`
		} `json:"carton"`
	}
	mustDecode(t, issued, &added)
	w.ssccIssued = added.Carton.SSCC
	if !strings.HasPrefix(w.ssccIssued, "00614141") || len(w.ssccIssued) != 18 {
		t.Fatalf("network SSCC %q does not come from the configured prefix", w.ssccIssued)
	}
	s.call(http.MethodPost, url+"/cartons", carton(`"sscc":"`+ssccOwn+`",`, 12), "c-2", http.StatusCreated)
	assertProblem(t, s.call(http.MethodPost, url+"/cartons", carton(`"sscc":"`+ssccOwn+`",`, 12), "c-3", http.StatusConflict), "sscc-not-unique")
	assertProblem(t, s.call(http.MethodPost, url+":ship", "", "ship-early", http.StatusConflict), "shipment-not-labelled")

	s.call(http.MethodPost, url+":label", "", "label-1", http.StatusOK)
	labels := s.call(http.MethodGet, url+"/labels", "", "", http.StatusOK)
	if !strings.Contains(labels, `"gs1128":"(00)`+w.ssccIssued+`"`) || !strings.Contains(labels, `"gs1128":"(00)`+ssccOwn+`"`) || !strings.Contains(labels, `"totalEaches":12`) {
		t.Fatalf("labels = %s", labels)
	}
	s.call(http.MethodPost, url+":ship", "", "ship-1", http.StatusOK)
	if got := s.call(http.MethodGet, url, "", "", http.StatusOK); !strings.Contains(got, `"state":"SHIPPED"`) {
		t.Fatalf("shipment = %s", got)
	}
	w.events = append(w.events, "InboundShipmentLabelled", "InboundShipmentShipped")
}

// Phase 3: the exact CloudEvents on the output topics.
func phasePublished(t *testing.T, w *world) {
	t.Helper()
	msgs := consume(t, w.brokers, topicEvents, 2)
	for i, name := range w.events {
		e := assertEnvelope(t, msgs[i], name, w.shipmentID, "events")
		d := dataOf(t, e)
		if d["shipmentId"] != w.shipmentID || d["planId"] != w.planID || d["sellerId"] != "seller-77" || d["nodeId"] != "node-1" {
			t.Fatalf("%s data = %v", name, d)
		}
		if name == "InboundShipmentLabelled" {
			cartons, _ := d["cartons"].([]any)
			if len(cartons) != 2 {
				t.Fatalf("manifest = %v", d["cartons"])
			}
			c0 := cartons[0].(map[string]any)
			if c0["sscc"] != w.ssccIssued || c0["kind"] != "HOMOGENEOUS_CASE" || c0["grossWeightG"] != float64(5200) {
				t.Fatalf("manifest carton = %v", c0)
			}
		}
	}
	an := consume(t, w.brokers, topicAnalytics, 2)
	assertEnvelope(t, an[0], "SellerOnboarded", "seller-77", "analytics")
	e := assertEnvelope(t, an[1], "InboundPlanConfirmed", w.planID, "analytics")
	if d := dataOf(t, e); d["packingOption"] != "CASE_PACKED" || d["destinationNodeId"] != "node-1" {
		t.Fatalf("InboundPlanConfirmed data = %v", d)
	}
}

// Phase 4: the vendor's receipt, read from the vendor-connectivity topic, closes the shipment.
func phaseReceipt(t *testing.T, s *service, w *world) {
	t.Helper()
	data := fmt.Sprintf(`{"transactionId":"tx-1","vendorId":"V-1","nodeId":"node-1","shipmentId":%q,"receivedAt":"2026-10-25T09:30:00Z","extra":"ignored","cartons":[`+
		`{"sscc":%q,"status":"RECEIVED","items":[{"gtin":%q,"qtyEaches":12}],"complianceCodes":[]},`+
		`{"sscc":%q,"status":"DAMAGED","items":[{"gtin":%q,"qtyEaches":10}],"complianceCodes":["CARTON_CRUSHED"]}]}`,
		w.shipmentID, w.ssccIssued, gtinCase, ssccOwn, gtinCase)
	produce(t, w.brokers, topicVendor, receiptEvent(t, "evt-r1", w.shipmentID, "node-1", data))
	// The same event again: the inbox swallows it.
	produce(t, w.brokers, topicVendor, receiptEvent(t, "evt-r1", w.shipmentID, "node-1", data))
	waitFor(t, 60*time.Second, "the shipment to close", func() bool {
		_, body := s.try(http.MethodGet, "/inbound-shipments/"+w.shipmentID, "", "")
		return strings.Contains(body, `"state":"CLOSED_WITH_DISCREPANCY"`)
	})
	received := consume(t, w.brokers, topicEvents, 3)[2]
	e := assertEnvelope(t, received, "InboundShipmentReceived", w.shipmentID, "events")
	d := dataOf(t, e)
	if d["status"] != "CLOSED_WITH_DISCREPANCY" || d["receivedAt"] != "2026-10-25T09:30:00Z" || d["nodeId"] != "node-1" {
		t.Fatalf("InboundShipmentReceived data = %v", d)
	}
	noMoreMessages(t, w.brokers, topicEvents, 3)
}

// Phase 5: the discrepancy and the compliance violation of the receipt, on the analytics topic.
func phaseAnalytics(t *testing.T, w *world) {
	t.Helper()
	want := w.analyticsAfterReceipt
	msgs := consume(t, w.brokers, topicAnalytics, 2+len(want))
	for i, name := range want {
		subject := w.shipmentID
		if name == "SellerComplianceViolationRecorded" {
			subject = "seller-77"
		}
		e := assertEnvelope(t, msgs[2+i], name, subject, "analytics")
		d := dataOf(t, e)
		// The damaged carton is reported as Damaged and, because it holds 10 of the 12 eaches, as Short.
		if kind := []string{"Damaged", "Short"}[min(i, 1)]; name == "InboundDiscrepancyRecorded" && (d["sscc"] != ssccOwn || d["kind"] != kind) {
			t.Fatalf("%s data = %v, want kind %s", name, d, kind)
		}
		if name == "SellerComplianceViolationRecorded" && (d["code"] != "CARTON_CRUSHED" || d["shipmentId"] != w.shipmentID) {
			t.Fatalf("%s data = %v", name, d)
		}
	}
	noMoreMessages(t, w.brokers, topicAnalytics, 2+len(want))
	if n := w.db.count(`SELECT count(*) FROM processed_events WHERE event_id='evt-r1'`); n != 1 {
		t.Fatalf("processed_events holds %d rows for the receipt, want 1", n)
	}
	if n := w.db.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`); n != 0 {
		t.Fatalf("%d outbox rows are still unpublished", n)
	}
}

// Phase 6: /readyz follows Kafka going away and the service exits so the orchestrator restarts it.
func phaseKafkaGoesAway(t *testing.T, s *service, w *world) {
	t.Helper()
	if status, body := readyz(s); status != http.StatusOK {
		t.Fatalf("readyz = %d %s", status, body)
	}
	stopTimeout := 10 * time.Second
	if err := w.kc.Stop(context.Background(), &stopTimeout); err != nil {
		t.Fatalf("stop kafka: %v", err)
	}
	body := waitReadyzStatus(t, s, http.StatusServiceUnavailable, "/readyz to turn 503 once Kafka is gone")
	if !strings.Contains(body, "dependency-unavailable") || !strings.Contains(body, "kafka") || strings.Contains(body, "postgres") {
		t.Fatalf("readyz with Kafka down = %s, want dependency-unavailable naming only kafka", body)
	}
	if status, body := s.try(http.MethodGet, "/healthz", "", ""); status != http.StatusOK {
		t.Fatalf("healthz with Kafka down = %d %s, want 200 (liveness only)", status, body)
	}
	err := s.expectExit()
	if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") {
		t.Fatalf("run returned %v after Kafka went away, want a 'stopped unexpectedly' error naming the dead worker", err)
	}
}

// world is what the phases share.
type world struct {
	kc      *tckafka.KafkaContainer
	db      pgView
	brokers []string

	planID, shipmentID, ssccIssued string

	events, analytics     []string
	analyticsAfterReceipt []string
}

func TestRunOnRealPostgresAndKafka(t *testing.T) {
	_, dbURL := startPostgres(t)
	kc, brokers := startKafka(t)
	// The consumed topics exist before the first start, as in the cluster: a group
	// that joins before its topic exists is handed no partition.
	for _, topic := range []string{topicNodes, topicOffers, topicVendor, topicEvents, topicAnalytics} {
		createTopic(t, brokers, topic)
	}
	w := &world{kc: kc, db: newPGView(t, dbURL), brokers: brokers,
		analyticsAfterReceipt: []string{"InboundDiscrepancyRecorded", "InboundDiscrepancyRecorded", "SellerComplianceViolationRecorded"}}
	s := env{dbURL: dbURL, brokers: brokers, addr: freeAddr(t)}.start(t)
	phaseSetup(t, s, w)
	phaseWorkflow(t, s, w)
	phasePublished(t, w)
	phaseReceipt(t, s, w)
	phaseAnalytics(t, w)
	phaseKafkaGoesAway(t, s, w)
}
