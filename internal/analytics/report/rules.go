package report

import (
	"errors"
	"fmt"
)

// The report rules of ADR 0017. They are functions, not package variables or
// constants, so a caller cannot mutate them and mutation testing reaches (and
// the tests pin) them.

// ReceivedItemsLimit is the most items a day's `received_by_item` lists: the
// ones with the most eaches received, ties broken by GTIN.
func ReceivedItemsLimit() int { return 10 }

// LatencyP50 and LatencyP95 are the continuous-percentile ranks of the
// advisory-to-receipt seconds.
func LatencyP50() float64 { return 0.5 }

// LatencyP95 is the 95th-percentile rank.
func LatencyP95() float64 { return 0.95 }

// ErrInvalidEvent marks a fact that can never be projected: a required field is
// missing or a value is out of range. It is deterministic, so the projector
// dead-letters it.
var ErrInvalidEvent = errors.New("report: invalid analytics event")

// Validate checks the fields every fact needs and the fields its Kind needs.
func (e Event) Validate() error {
	missing := firstMissing(
		required{"id", e.EventID != ""},
		required{"time", !e.At.IsZero()},
		required{"the aggregate id", e.SubjectID != ""},
		required{"sellerId", e.SellerID != ""},
		required{"the business instant", !e.OccurredAt.IsZero()},
	)
	if missing == "" {
		missing = e.kindMissing()
	}
	if missing != "" {
		return fmt.Errorf("%w: %s %s: %s is required", ErrInvalidEvent, e.Kind, e.EventID, missing)
	}
	return nil
}

// kindMissing names the first missing or invalid field of the Kind's own
// payload, or rejects a Kind the projection does not know.
func (e Event) kindMissing() string {
	switch e.Kind {
	case KindInboundPlanConfirmed:
		return ""
	case KindPlacementPlanSuggested:
		return firstMissing(required{"catalogItemId", e.CatalogItemID != ""})
	case KindInboundShipmentReceived:
		return e.linesMissing()
	default:
		return "a known kind"
	}
}

// linesMissing checks every received line: a GTIN and a positive quantity (the
// contract omits lines of zero eaches).
func (e Event) linesMissing() string {
	for _, l := range e.Lines {
		if missing := firstMissing(required{"received[].gtin", l.GTIN != ""}, required{"received[].qtyEaches > 0", l.QtyEaches > 0}); missing != "" {
			return missing
		}
	}
	return ""
}

// required is one field check: name and whether it is present.
type required struct {
	name    string
	present bool
}

// firstMissing names the first absent field, or "" when every one is present.
func firstMissing(fields ...required) string {
	for _, f := range fields {
		if !f.present {
			return f.name
		}
	}
	return ""
}
