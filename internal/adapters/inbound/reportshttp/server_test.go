package reportshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// fakeReader answers fixed rows and records the range it was asked for.
type fakeReader struct {
	counts    []report.DayCounts
	items     []report.DayItem
	latencies []report.DayLatency
	last      *time.Time
	err       error
	failOn    string // "", "counts", "items" or "latency": which call fails (all when err is set and failOn is empty)
	asked     report.Range
}

func (f *fakeReader) fail(call string) error {
	if f.failOn == "" || f.failOn == call {
		return f.err
	}
	return nil
}

func (f *fakeReader) InboundCounts(_ context.Context, r report.Range) ([]report.DayCounts, error) {
	f.asked = r
	return f.counts, f.fail("counts")
}

func (f *fakeReader) ReceivedItems(context.Context, report.Range) ([]report.DayItem, error) {
	return f.items, f.fail("items")
}

func (f *fakeReader) AdvisoryToReceipt(context.Context, report.Range) ([]report.DayLatency, error) {
	return f.latencies, f.fail("latency")
}

func (f *fakeReader) LastEventAt(context.Context) (*time.Time, error) { return f.last, f.err }

type fakePing struct{ err error }

func (p fakePing) Ping(context.Context) error { return p.err }

var (
	now  = time.Date(2026, 11, 8, 18, 30, 0, 0, time.UTC)
	nov3 = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
)

func serve(t *testing.T, s *reportshttp.Server, method, target string) (int, string, map[string]any) {
	t.Helper()
	if s.Now == nil {
		s.Now = func() time.Time { return now }
	}
	h := reportshttp.NewRouter(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON: %v", method, target, rec.Body.String(), err)
	}
	return rec.Code, rec.Header().Get("Content-Type"), body
}

func TestSellerInboundPerformanceReport(t *testing.T) {
	r := &fakeReader{
		counts:    []report.DayCounts{{Day: nov3, PlansConfirmed: 2, AdvisoriesIssued: 3, ShipmentsReceived: 1}},
		items:     []report.DayItem{{Day: nov3, GTIN: "00614141000012", CatalogItemID: "item-1", Publications: 2, QtyEaches: 15}, {Day: nov3, GTIN: "00614141000036", Publications: 1, QtyEaches: 4}},
		latencies: []report.DayLatency{{Day: nov3, Samples: 2, P50: 9000, P95: 13860}},
	}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/seller-inbound-performance?from=2026-11-01&to=2026-11-07")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("status %d content-type %s", code, ct)
	}
	want := map[string]any{"from": "2026-11-01", "to": "2026-11-07", "days": []any{map[string]any{
		"day": "2026-11-03", "inbound_plans_confirmed": 2.0, "placement_advisories_issued": 3.0, "shipments_received": 1.0,
		"received_eaches": 19.0, "received_item_count": 2.0,
		"received_by_item": []any{
			map[string]any{"gtin": "00614141000012", "catalog_item_id": "item-1", "publications": 2.0, "qty_eaches": 15.0},
			map[string]any{"gtin": "00614141000036", "publications": 1.0, "qty_eaches": 4.0},
		},
		"advisory_to_receipt_samples": 2.0, "advisory_to_receipt_p50_seconds": 9000.0, "advisory_to_receipt_p95_seconds": 13860.0,
	}}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
	if !r.asked.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !r.asked.To.Equal(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("asked range = %+v, want [11-01, 11-08)", r.asked)
	}
}

// A day with no receipt that had a preceding advisory has explicit nulls, not zeros.
func TestADayWithoutLatencySamplesHasNullPercentiles(t *testing.T) {
	r := &fakeReader{counts: []report.DayCounts{{Day: nov3, PlansConfirmed: 1}}}
	_, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/seller-inbound-performance")
	day := body["days"].([]any)[0].(map[string]any)
	if v, ok := day["advisory_to_receipt_p50_seconds"]; !ok || v != nil {
		t.Fatalf("p50 = %v (present %v), want an explicit null", v, ok)
	}
	if v, ok := day["advisory_to_receipt_p95_seconds"]; !ok || v != nil || day["advisory_to_receipt_samples"] != 0.0 {
		t.Fatalf("day = %v", day)
	}
	if items, ok := day["received_by_item"].([]any); !ok || len(items) != 0 {
		t.Fatalf("received_by_item = %#v, want []", day["received_by_item"])
	}
	if body["from"] != "2026-10-10" || body["to"] != "2026-11-08" {
		t.Fatalf("the default range is the 30 days ending today: %v %v", body["from"], body["to"])
	}
}

func TestEmptyReportIsAnEmptyArray(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/seller-inbound-performance")
	if days, ok := body["days"].([]any); !ok || len(days) != 0 {
		t.Errorf("days = %#v, want []", body["days"])
	}
}

func TestFreshness(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/freshness")
	if v, ok := body["as_of"]; !ok || v != nil || body["lag_seconds"] != nil {
		t.Fatalf("before the first event = %v, want explicit nulls", body)
	}
	asOf := now.Add(-90 * time.Second)
	_, _, body = serve(t, &reportshttp.Server{Reader: &fakeReader{last: &asOf}}, http.MethodGet, "/reports/freshness")
	if body["as_of"] != "2026-11-08T18:28:30Z" || body["lag_seconds"] != 90.0 {
		t.Fatalf("freshness = %v", body)
	}
}

func assertProblem(t *testing.T, code int, ct string, body map[string]any, wantCode int, wantType string) {
	t.Helper()
	if code != wantCode || ct != "application/problem+json" || body["type"] != wantType || body["status"] != float64(wantCode) {
		t.Fatalf("got %d %s %v, want %d problem %s", code, ct, body, wantCode, wantType)
	}
}

func TestInvalidRangeIsAProblem(t *testing.T) {
	for _, q := range []string{"?from=2026-11-05&to=2026-11-04", "?from=nope", "?to=2026-13-01", "?from=2025-01-01&to=2026-11-01"} {
		code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/seller-inbound-performance"+q)
		assertProblem(t, code, ct, body, http.StatusBadRequest, "invalid-report-range")
	}
}

func TestReaderFailureIsAnOpaque500(t *testing.T) {
	cause := errors.New("pq: password authentication failed for secret-role")
	cases := []struct{ path, failOn string }{
		{"/reports/seller-inbound-performance", "counts"}, {"/reports/seller-inbound-performance", "items"},
		{"/reports/seller-inbound-performance", "latency"}, {"/reports/freshness", ""},
	}
	for _, c := range cases {
		r := &fakeReader{err: cause, failOn: c.failOn}
		code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, c.path)
		assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
		if strings.Contains(body["detail"].(string), "secret") {
			t.Fatalf("%s (%s) leaked the cause: %v", c.path, c.failOn, body)
		}
	}
}

func TestUnknownPathAndWriteMethodsAreProblems(t *testing.T) {
	code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/nope")
	assertProblem(t, code, ct, body, http.StatusNotFound, "not-found")
	code, ct, body = serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodPost, "/reports/freshness")
	assertProblem(t, code, ct, body, http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestHealthAndReadiness(t *testing.T) {
	if code, _, body := serve(t, &reportshttp.Server{}, http.MethodGet, "/healthz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz = %d %v", code, body)
	}
	if code, _, body := serve(t, &reportshttp.Server{Ping: fakePing{}}, http.MethodGet, "/readyz"); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v", code, body)
	}
	code, ct, body := serve(t, &reportshttp.Server{Ping: fakePing{err: errors.New("down")}}, http.MethodGet, "/readyz")
	assertProblem(t, code, ct, body, http.StatusServiceUnavailable, "dependency-unavailable")
	draining := &reportshttp.Server{}
	draining.SetNotReady()
	if code, _, body := serve(t, draining, http.MethodGet, "/readyz"); code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("draining readyz = %d %v", code, body)
	}
}
