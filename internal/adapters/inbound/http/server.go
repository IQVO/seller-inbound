// Package http is the REST inbound adapter: the seller API of
// apis/openapi.yaml on a chi router, RFC 9457 problem documents for every
// error, and the liveness/readiness endpoints. It has no auth middleware
// (fleet decision; internal/architecture's TestNoAuthMiddlewareReintroduced):
// the seller is named in the request body and the aggregates check it is ACTIVE.
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/seller-inbound/internal/application/usecases"
)

// Server holds the application service the handlers call.
type Server struct {
	Service *usecases.Service

	// Idempotency wraps every write (the fleet Idempotency-Key middleware,
	// built by cmd/ over the Postgres pool); the shipped binary always sets it,
	// nil (handler unit tests) leaves the routes unwrapped.
	Idempotency func(http.Handler) http.Handler
	// Readiness backs GET /readyz: the drain gate plus the Postgres and Kafka
	// checks (NewReadiness). The shipped binary always builds it with
	// NewReadiness; nil (handler unit tests) means always ready.
	Readiness *Readiness
}

// NewRouter builds the chi router for every operation of apis/openapi.yaml.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	// A NUL (U+0000) in the path, the query or a body is a uniform client
	// error here, never a Postgres 22021 -> 500; it runs before the
	// idempotency middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	write := func(h http.HandlerFunc) http.Handler {
		if s.Idempotency == nil {
			return h
		}
		return s.Idempotency(h)
	}

	r.Method(http.MethodPost, "/sellers", write(s.handleOnboardSeller))
	// chi cannot route a ":verb" suffix inside one segment, so the actions of a
	// resource share a pattern and are dispatched on the suffix.
	r.Method(http.MethodPost, "/sellers/{sellerAction}", write(s.handleSellerAction))
	r.Method(http.MethodPost, "/inbound-plans", write(s.handleCreatePlan))
	r.Method(http.MethodPost, "/inbound-plans/{planAction}", write(s.handlePlanAction))
	r.Get("/inbound-plans/{planId}", s.handleGetPlan)
	r.Method(http.MethodPost, "/inbound-plans/{planId}/shipments", write(s.handleCreateShipment))
	r.Method(http.MethodPost, "/inbound-shipments/{shipmentId}/cartons", write(s.handleAddCarton))
	r.Method(http.MethodPost, "/inbound-shipments/{shipmentAction}", write(s.handleShipmentAction))
	r.Get("/inbound-shipments/{shipmentId}/labels", s.handleGetLabels)
	r.Get("/inbound-shipments/{shipmentId}", s.handleGetShipment)
	return r
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}
