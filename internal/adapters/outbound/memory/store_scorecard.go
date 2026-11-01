package memory

import (
	"context"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
)

// Scorecard returns the ScorecardFacts double.
func (s *Store) Scorecard() *ScorecardFacts { return &ScorecardFacts{s} }

// ScorecardFacts is the in-memory ports.ScorecardFacts. It follows the SQL
// contract of the Postgres adapter: a closed shipment is kept once, a window
// is inclusive on both ends.
type ScorecardFacts struct{ s *Store }

// Record appends the facts.
func (f *ScorecardFacts) Record(ctx context.Context, facts scorecard.Facts) error {
	defer f.s.enter(ctx)()
	for _, r := range facts.Receipts {
		if _, kept := f.s.receipts[r.ShipmentID]; !kept {
			f.s.receipts[r.ShipmentID] = r
		}
	}
	f.s.occurs = append(f.s.occurs, facts.Occurrences...)
	return nil
}

// Counts sums the seller's facts recorded in [since, until].
func (f *ScorecardFacts) Counts(ctx context.Context, sellerID string, since, until time.Time) (scorecard.Counts, error) {
	defer f.s.enter(ctx)()
	in := func(at time.Time) bool { return !at.Before(since) && !at.After(until) }
	out := scorecard.Counts{Discrepancies: map[string]int{}}
	for _, r := range f.s.receipts {
		if r.SellerID == sellerID && in(r.At) {
			out.ShipmentsReceived++
			out.CartonsReported += r.CartonsReported
			out.CartonsReceived += r.CartonsReceived
		}
	}
	for _, o := range f.s.occurs {
		if o.SellerID != sellerID || !in(o.At) {
			continue
		}
		if o.Type == scorecard.Discrepancy {
			out.Discrepancies[o.Code]++
		} else {
			out.Violations++
		}
	}
	return out, nil
}
