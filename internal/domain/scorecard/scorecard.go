// Package scorecard is the seller compliance scorecard (ADR 0005): a rolling
// window read model over the facts this service records when it applies a
// vendor receipt. Everything here is a pure function of its inputs.
//
// The scorecard is INFORMATIONAL. The complianceTier it derives suspends,
// blocks and limits nothing: suspension stays the human SuspendSeller command
// of the SellerAccount aggregate (model.yaml, context seller-inbound). No
// function of this package takes or returns a seller.Account.
package scorecard

import (
	"fmt"
	"maps"
	"math"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// Window is a rolling look-back period.
type Window string

// The windows the API accepts.
const (
	Window7d  Window = "7d"
	Window30d Window = "30d"
)

// DefaultWindow answers a request that names no window.
const DefaultWindow = Window30d

// hoursPerDay is the length of a rolling day: windows are fixed durations, not
// calendar days, so a daylight-saving change never moves a boundary.
const hoursPerDay = 24

// ParseWindow reads the window query value. An empty value is DefaultWindow;
// anything but "7d" and "30d" is ErrInvalidWindow.
func ParseWindow(s string) (Window, error) {
	switch Window(s) {
	case "":
		return DefaultWindow, nil
	case Window7d, Window30d:
		return Window(s), nil
	}
	return "", fmt.Errorf("%w: %q (use 7d or 30d)", ErrInvalidWindow, s)
}

// Days returns the length of the window in days.
func (w Window) Days() int {
	if w == Window7d {
		return 7
	}
	return 30
}

// Since returns the start of the window that ends at now. The start itself is
// inside the window.
func (w Window) Since(now time.Time) time.Time {
	return now.UTC().Add(-time.Duration(w.Days()) * hoursPerDay * time.Hour)
}

// Tier is the derived, informational compliance tier.
type Tier string

// The tiers, best to worst.
const (
	TierGood   Tier = "GOOD"
	TierWatch  Tier = "WATCH"
	TierAtRisk Tier = "AT_RISK"
)

// The declared thresholds of the tier (ADR 0005 A3). The tier is the worst of
// three independent signals; every comparison is on the exact, unrounded
// value. Changing a number here is a new ADR that supersedes 0005.
const (
	// WatchAccuracyBelow: receiptAccuracyRate strictly below this is WATCH.
	WatchAccuracyBelow = 0.98
	// AtRiskAccuracyBelow: receiptAccuracyRate strictly below this is AT_RISK.
	AtRiskAccuracyBelow = 0.90
	// WatchDiscrepancyRate: discrepancyRatePerShipment at or above this is WATCH.
	WatchDiscrepancyRate = 0.5
	// AtRiskDiscrepancyRate: discrepancyRatePerShipment at or above this is AT_RISK.
	AtRiskDiscrepancyRate = 2.0
	// WatchViolations: this many compliance violations or more is WATCH.
	WatchViolations = 1
	// AtRiskViolations: this many compliance violations or more is AT_RISK.
	AtRiskViolations = 5
)

// ratePrecision is the number of decimals a published rate keeps.
const ratePrecision = 1e4

// Counts are the sums of the facts recorded in a window for one seller.
type Counts struct {
	// ShipmentsReceived is the number of shipments that closed (CLOSED or
	// CLOSED_WITH_DISCREPANCY) in the window.
	ShipmentsReceived int
	// CartonsReported is the number of cartons those closing events reported.
	CartonsReported int
	// CartonsReceived is how many of them have the status RECEIVED.
	CartonsReceived int
	// Discrepancies counts InboundDiscrepancyRecorded per kind. The kind is the
	// fleet's carton discrepancy kind string, an open vocabulary.
	Discrepancies map[string]int
	// Violations counts SellerComplianceViolationRecorded.
	Violations int
}

// Card is the scorecard of one seller over one window.
type Card struct {
	Window Window
	// Since and Until bound the window: facts with Since <= recordedAt <= Until.
	Since, Until time.Time

	ShipmentsReceived int
	CartonsReported   int
	CartonsReceived   int
	// ReceiptAccuracyRate is CartonsReceived / CartonsReported (as
	// node-performance defines inbound accuracy); nil when nothing was reported.
	ReceiptAccuracyRate *float64
	// DiscrepancyRatePerShipment is the discrepancies recorded / ShipmentsReceived;
	// nil when no shipment closed.
	DiscrepancyRatePerShipment *float64
	// DiscrepanciesByKind is never nil.
	DiscrepanciesByKind  map[string]int
	ComplianceViolations int
	Tier                 Tier
}

// Compute derives the scorecard of the window that ends at now. A rate whose
// denominator is zero is nil (omitted on the wire), never NaN.
func Compute(w Window, now time.Time, c Counts) Card {
	byKind := map[string]int{}
	maps.Copy(byKind, c.Discrepancies)
	total := 0
	for _, n := range byKind {
		total += n
	}
	accuracy := ratio(c.CartonsReceived, c.CartonsReported)
	perShipment := ratio(total, c.ShipmentsReceived)
	return Card{
		Window: w, Since: w.Since(now), Until: now.UTC(),
		ShipmentsReceived: c.ShipmentsReceived, CartonsReported: c.CartonsReported, CartonsReceived: c.CartonsReceived,
		ReceiptAccuracyRate: rounded(accuracy), DiscrepancyRatePerShipment: rounded(perShipment),
		DiscrepanciesByKind: byKind, ComplianceViolations: c.Violations,
		Tier: TierFor(accuracy, perShipment, c.Violations),
	}
}

// ratio is num/den, or nil when den is not positive.
func ratio(num, den int) *float64 {
	if den <= 0 {
		return nil
	}
	r := float64(num) / float64(den)
	return &r
}

// rounded returns r rounded to ratePrecision, or nil.
func rounded(r *float64) *float64 {
	if r == nil {
		return nil
	}
	v := math.Round(*r*ratePrecision) / ratePrecision
	return &v
}

// TierFor is the worst of the three signals. A nil rate (nothing observed) is
// no signal. It is a pure classifier: it reads numbers and returns a label.
func TierFor(accuracy, perShipment *float64, violations int) Tier {
	if atRisk(accuracy, perShipment, violations) {
		return TierAtRisk
	}
	if watch(accuracy, perShipment, violations) {
		return TierWatch
	}
	return TierGood
}

func atRisk(accuracy, perShipment *float64, violations int) bool {
	if accuracy != nil && *accuracy < AtRiskAccuracyBelow {
		return true
	}
	if perShipment != nil && *perShipment >= AtRiskDiscrepancyRate {
		return true
	}
	return violations >= AtRiskViolations
}

func watch(accuracy, perShipment *float64, violations int) bool {
	if accuracy != nil && *accuracy < WatchAccuracyBelow {
		return true
	}
	if perShipment != nil && *perShipment >= WatchDiscrepancyRate {
		return true
	}
	return violations >= WatchViolations
}

// ErrInvalidWindow marks a window other than 7d or 30d (problem type
// invalid-window, 400). It wraps shared.ErrValidation so a caller that only
// knows the shared category still treats it as a client error.
var ErrInvalidWindow = fmt.Errorf("%w: window", shared.ErrValidation)
