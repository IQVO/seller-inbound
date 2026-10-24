package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type, a malformed query value).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title) of one error category; the
// detail comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the middlewares write themselves, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusUnprocessableEntity, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyReused   = problem{http.StatusUnprocessableEntity, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
	unknownAction          = problem{http.StatusNotFound, "not-found", "Not found"}
)

// errUnknownAction is a ":verb" suffix the resource does not have.
var errUnknownAction = errors.New("unknown action")

// problemCatalogue maps every typed error to its problem, in match order: the
// single REST error table of ADR 0002. The slug is the kebab-case of the Err
// name (the shared types are not-found, illegal-transition, concurrent-update
// and validation-failed).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{scorecard.ErrInvalidWindow, problem{http.StatusBadRequest, "invalid-window", "The window must be 7d or 30d"}},
	{errMalformedRequest, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
	{inboundshipment.ErrCasePackNotUniform, problem{http.StatusUnprocessableEntity, "case-pack-not-uniform", "A case-packed carton holds one item at one quantity"}},
	{inboundshipment.ErrCaseQtyMismatchesGtin, problem{http.StatusUnprocessableEntity, "case-qty-mismatches-gtin", "The carton quantity differs from the quantity the case GTIN declares"}},
	{inboundshipment.ErrCartonOverweight, problem{http.StatusUnprocessableEntity, "carton-overweight", "The carton is heavier than the destination node accepts"}},
	{inboundshipment.ErrCartonOversize, problem{http.StatusUnprocessableEntity, "carton-oversize", "The carton is larger than the destination node accepts"}},
	{inboundshipment.ErrInvalidSscc, problem{http.StatusUnprocessableEntity, "invalid-sscc", "The SSCC must be 18 digits with a valid check digit"}},
	{inboundshipment.ErrCartonContentsMismatchPlan, problem{http.StatusUnprocessableEntity, "carton-contents-mismatch-plan", "The carton contents do not fit the plan"}},
	{ports.ErrNotFound, problem{http.StatusNotFound, "not-found", "Not found"}},
	{errUnknownAction, unknownAction},
	{seller.ErrSellerSuspended, problem{http.StatusConflict, "seller-suspended", "The seller is suspended"}},
	{seller.ErrSellerExists, problem{http.StatusConflict, "seller-exists", "The seller is already onboarded"}},
	{network.ErrNodeCannotReceive, problem{http.StatusConflict, "node-cannot-receive", "The destination node cannot receive inbound shipments"}},
	{inboundshipment.ErrSsccNotUnique, problem{http.StatusConflict, "sscc-not-unique", "The SSCC is already used"}},
	{inboundshipment.ErrShipmentNotLabelled, problem{http.StatusConflict, "shipment-not-labelled", "A shipment ships only after every carton is labelled"}},
	{shared.ErrIllegalTransition, problem{http.StatusConflict, "illegal-transition", "Illegal transition"}},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch it and retry"}},
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
