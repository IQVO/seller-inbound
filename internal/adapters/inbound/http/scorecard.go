package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/seller-inbound/internal/application/views"
)

// scorecardResponse is the wire shape of GET /sellers/{sellerId}/scorecard. A
// rate whose denominator is zero is omitted, never null or NaN; the kind map is
// always present.
type scorecardResponse struct {
	SellerID                   string         `json:"sellerId"`
	Window                     string         `json:"window"`
	WindowStart                time.Time      `json:"windowStart"`
	WindowEnd                  time.Time      `json:"windowEnd"`
	ShipmentsReceived          int            `json:"shipmentsReceived"`
	CartonsReported            int            `json:"cartonsReported"`
	CartonsReceived            int            `json:"cartonsReceived"`
	ReceiptAccuracyRate        *float64       `json:"receiptAccuracyRate,omitempty"`
	DiscrepancyRatePerShipment *float64       `json:"discrepancyRatePerShipment,omitempty"`
	DiscrepanciesByKind        map[string]int `json:"discrepanciesByKind"`
	ComplianceViolations       int            `json:"complianceViolations"`
	ComplianceTier             string         `json:"complianceTier"`
}

func scorecardOut(v views.SellerScorecard) scorecardResponse {
	return scorecardResponse(v)
}

func (s *Server) handleGetScorecard(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.GetSellerScorecard(r.Context(), chi.URLParam(r, "sellerId"), r.URL.Query().Get("window"))
	respond(w, r, scorecardOut(v), err)
}
