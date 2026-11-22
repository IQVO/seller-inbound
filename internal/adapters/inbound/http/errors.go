package http

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/capacity"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type, a malformed query value).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title, class) of one error category; the
// detail comes from the error at write time. class is the replay class the
// Idempotency-Key middleware reads (replay_class.go, ADR 0018): every problem
// literal of this package lives in this file and declares one.
type problem struct {
	status int
	slug   string
	title  string
	class  replayClass
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error", classRetryable}

// Problems the middlewares write themselves, before any use case runs. The
// three Idempotency-Key answers are a function of the request alone and are
// written outside the store, so nothing is ever kept under their key.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", classFinal}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid", classFinal}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", classFinal}
	// notFoundResource is the 404 of a request path that can never name a resource
	// (a NUL in the path, nul.go); it shares its slug, so its class, with the
	// not-found of the catalogue, and it is written before the idempotency
	// middleware, so nothing is stored under any key.
	notFoundResource = problem{http.StatusNotFound, "not-found", "Not found", classRetryable}
	// dependencyUnavailable is the 503 of GET /readyz (readiness.go).
	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable", classRetryable}
)

// problemCatalogue maps every typed error to its problem, in match order: the
// single REST error table of ADR 0002. The slug is the kebab-case of the Err
// name (the shared types are not-found, illegal-transition, concurrent-update
// and validation-failed).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{scorecard.ErrInvalidWindow, problem{http.StatusBadRequest, "invalid-window", "The window must be 7d or 30d", classFinal}},
	{ports.ErrInvalidCoverDays, problem{http.StatusBadRequest, "invalid-cover-days",
		fmt.Sprintf("The coverDays must be between %d and %d", placementplan.MinCoverDays, placementplan.MaxCoverDays), classFinal}},
	{errMalformedRequest, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed", classRetryable}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed", classRetryable}},
	{inboundshipment.ErrCasePackNotUniform, problem{http.StatusUnprocessableEntity, "case-pack-not-uniform", "A case-packed carton holds one item at one quantity", classRetryable}},
	{inboundshipment.ErrCaseQtyMismatchesGtin, problem{http.StatusUnprocessableEntity, "case-qty-mismatches-gtin", "The carton quantity differs from the quantity the case GTIN declares", classRetryable}},
	{inboundshipment.ErrCartonOverweight, problem{http.StatusUnprocessableEntity, "carton-overweight", "The carton is heavier than the destination node accepts", classRetryable}},
	{inboundshipment.ErrCartonOversize, problem{http.StatusUnprocessableEntity, "carton-oversize", "The carton is larger than the destination node accepts", classRetryable}},
	{inboundshipment.ErrInvalidSscc, problem{http.StatusUnprocessableEntity, "invalid-sscc", "The SSCC must be 18 digits with a valid check digit", classFinal}},
	{inboundshipment.ErrCartonContentsMismatchPlan, problem{http.StatusUnprocessableEntity, "carton-contents-mismatch-plan", "The carton contents do not fit the plan", classRetryable}},
	{capacity.ErrInboundCapacityExceeded, problem{http.StatusUnprocessableEntity, "inbound-capacity-exceeded", "The plan would exceed the seller's monthly inbound capacity at this node", classRetryable}},
	{capacity.ErrNetworkSplitDestination, problem{http.StatusUnprocessableEntity, "network-split-destination", "A network-split plan destinations an inbound cross-dock", classRetryable}},
	{capacity.ErrNetworkSplitUnavailable, problem{http.StatusUnprocessableEntity, "network-split-unavailable", "The destination region holds no fulfilment centre to redistribute to", classRetryable}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found", classRetryable}},
	{seller.ErrSellerSuspended, problem{http.StatusConflict, "seller-suspended", "The seller is suspended", classRetryable}},
	{seller.ErrSellerExists, problem{http.StatusConflict, "seller-exists", "The seller is already onboarded", classRetryable}},
	{network.ErrNodeCannotReceive, problem{http.StatusConflict, "node-cannot-receive", "The destination node cannot receive inbound shipments", classRetryable}},
	{inboundshipment.ErrSsccNotUnique, problem{http.StatusConflict, "sscc-not-unique", "The SSCC is already used", classRetryable}},
	{inboundshipment.ErrShipmentNotLabelled, problem{http.StatusConflict, "shipment-not-labelled", "A shipment ships only after every carton is labelled", classRetryable}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "Illegal transition", classRetryable}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch it and retry", classRetryable}},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
