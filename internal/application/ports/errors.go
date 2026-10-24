package ports

import "errors"

var (
	// ErrNotFound is what every repository returns for a Get that matches
	// nothing (problem type not-found, 404). An adapter wraps its own miss into
	// this sentinel, so callers test errors.Is(err, ports.ErrNotFound).
	ErrNotFound = errors.New("not found")
	// ErrConcurrentUpdate is what Save returns when the stored version is not
	// the one the aggregate was loaded at (problem type concurrent-update, 409).
	ErrConcurrentUpdate = errors.New("concurrent update")
	// ErrSsccTaken is what ShipmentRepository.Save returns when a carton SSCC is
	// already registered to another shipment: SSCCs are unique network-wide. The
	// use case turns it into inboundshipment.ErrSsccNotUnique.
	ErrSsccTaken = errors.New("sscc already registered")
)
