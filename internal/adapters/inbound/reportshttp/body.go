package reportshttp

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// Wire bodies (snake_case, apis/openapi.yaml tag `reports`, ADR 0017).

type receivedItemBody struct {
	GTIN          string `json:"gtin"`
	CatalogItemID string `json:"catalog_item_id,omitempty"`
	Publications  int    `json:"publications"`
	QtyEaches     int    `json:"qty_eaches"`
}

type performanceDayBody struct {
	Day                         string             `json:"day"`
	InboundPlansConfirmed       int                `json:"inbound_plans_confirmed"`
	PlacementAdvisoriesIssued   int                `json:"placement_advisories_issued"`
	ShipmentsReceived           int                `json:"shipments_received"`
	ReceivedEaches              int                `json:"received_eaches"`
	ReceivedItemCount           int                `json:"received_item_count"`
	ReceivedByItem              []receivedItemBody `json:"received_by_item"`
	AdvisoryToReceiptSamples    int                `json:"advisory_to_receipt_samples"`
	AdvisoryToReceiptP50Seconds *float64           `json:"advisory_to_receipt_p50_seconds"`
	AdvisoryToReceiptP95Seconds *float64           `json:"advisory_to_receipt_p95_seconds"`
}

type performanceReportBody struct {
	From string               `json:"from"`
	To   string               `json:"to"`
	Days []performanceDayBody `json:"days"`
}

func performanceBody(rg report.Range, rows []report.PerformanceRow) performanceReportBody {
	out := performanceReportBody{From: rg.FirstDay(), To: rg.LastDay(), Days: make([]performanceDayBody, 0, len(rows))}
	for _, r := range rows {
		items := make([]receivedItemBody, 0, len(r.ReceivedByItem))
		for _, it := range r.ReceivedByItem {
			items = append(items, receivedItemBody{GTIN: it.GTIN, CatalogItemID: it.CatalogItemID, Publications: it.Publications, QtyEaches: it.QtyEaches})
		}
		out.Days = append(out.Days, performanceDayBody{
			Day: report.FormatDay(r.Day), InboundPlansConfirmed: r.PlansConfirmed, PlacementAdvisoriesIssued: r.AdvisoriesIssued,
			ShipmentsReceived: r.ShipmentsReceived, ReceivedEaches: r.ReceivedEaches, ReceivedItemCount: r.ReceivedItemCount, ReceivedByItem: items,
			AdvisoryToReceiptSamples: r.LatencySamples, AdvisoryToReceiptP50Seconds: r.LatencyP50, AdvisoryToReceiptP95Seconds: r.LatencyP95,
		})
	}
	return out
}

type freshnessBody struct {
	AsOf       *time.Time `json:"as_of"`
	LagSeconds *float64   `json:"lag_seconds"`
}

// problem is one RFC 9457 problem type: its status, slug and title.
type problem struct {
	status int
	slug   string
	title  string
}

var (
	invalidRange          = problem{http.StatusBadRequest, "invalid-report-range", "The report range is invalid"}
	notFound              = problem{http.StatusNotFound, "not-found", "Resource not found"}
	methodNotAllowed      = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable"}
	internalError         = problem{http.StatusInternalServerError, "internal-error", "Internal error"}
)

type problemBody struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemBody{Type: p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath()})
}

// logSafe strips line breaks so a logged value cannot forge a log line.
var logSafe = strings.NewReplacer("\n", " ", "\r", " ")

// writeInternal logs err and answers a 500 whose detail never leaks it.
func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "report query failed", "error", logSafe.Replace(err.Error()), "path", logSafe.Replace(r.URL.Path))
	writeProblem(w, r, internalError, "an unexpected error occurred")
}
