package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// rejectionReasons maps a ship failure to the problem slug the REST adapter
// answers with (same vocabulary as apis/openapi.yaml), in match order.
var rejectionReasons = []struct {
	err    error
	reason string
}{
	{inboundshipment.ErrShipmentNotLabelled, "shipment-not-labelled"},
	{seller.ErrSellerSuspended, "seller-suspended"},
	{network.ErrNodeCannotReceive, "node-cannot-receive"},
	{ports.ErrNotFound, "not-found"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
	{shared.ErrIllegalTransition, "illegal-transition"},
	{shared.ErrValidation, "validation-failed"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// recordShipOutcome counts one ship attempt: shipped when err is nil, else
// rejected with the reason of err. It runs once per call, after the unit of
// work (and its single concurrency retry) finished.
func (s *Service) recordShipOutcome(ctx context.Context, err error) {
	m := s.d.Metrics
	if m == nil {
		return
	}
	if err == nil {
		m.ShipmentShipped(ctx)
		return
	}
	m.ShipmentRejected(ctx, rejectionReason(err))
}

func rejectionReason(err error) string {
	for _, r := range rejectionReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
