package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
)

// ScorecardFactsRepo is the Postgres ports.ScorecardFacts over
// seller_scorecard_receipts and seller_scorecard_occurrences.
type ScorecardFactsRepo struct{ pool *pgxpool.Pool }

// NewScorecardFacts constructs a ScorecardFactsRepo over pool.
func NewScorecardFacts(pool *pgxpool.Pool) *ScorecardFactsRepo {
	return &ScorecardFactsRepo{pool: pool}
}

var _ ports.ScorecardFacts = (*ScorecardFactsRepo)(nil)

// Record inserts the facts on the transaction carried by ctx. A shipment that
// already has its closing row keeps it.
func (r *ScorecardFactsRepo) Record(ctx context.Context, f scorecard.Facts) error {
	q := queryFor(ctx, r.pool)
	for _, rc := range f.Receipts {
		if _, err := q.Exec(ctx, `
			INSERT INTO seller_scorecard_receipts (shipment_id, seller_id, cartons_reported, cartons_received, recorded_at)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT (shipment_id) DO NOTHING`,
			rc.ShipmentID, rc.SellerID, rc.CartonsReported, rc.CartonsReceived, rc.At.UTC()); err != nil {
			return err
		}
	}
	for _, o := range f.Occurrences {
		if _, err := q.Exec(ctx, `
			INSERT INTO seller_scorecard_occurrences (seller_id, shipment_id, occurrence_type, code, recorded_at)
			VALUES ($1, $2, $3, $4, $5)`,
			o.SellerID, o.ShipmentID, string(o.Type), o.Code, o.At.UTC()); err != nil {
			return err
		}
	}
	return nil
}

// Counts sums the seller's facts recorded in [since, until]. The receipts and
// the occurrences are read by one statement, so they come from one snapshot.
func (r *ScorecardFactsRepo) Counts(ctx context.Context, sellerID string, since, until time.Time) (scorecard.Counts, error) {
	q := queryFor(ctx, r.pool)
	out := scorecard.Counts{Discrepancies: map[string]int{}}
	rows, err := q.Query(ctx, `
		SELECT 'RECEIPT' AS fact, '' AS code, count(*)::int AS n, COALESCE(sum(cartons_reported), 0)::int AS reported, COALESCE(sum(cartons_received), 0)::int AS received
		FROM seller_scorecard_receipts
		WHERE seller_id = $1 AND recorded_at >= $2 AND recorded_at <= $3
		UNION ALL
		SELECT occurrence_type, code, count(*)::int, 0, 0
		FROM seller_scorecard_occurrences
		WHERE seller_id = $1 AND recorded_at >= $2 AND recorded_at <= $3
		GROUP BY occurrence_type, code`, sellerID, since.UTC(), until.UTC())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var fact, code string
		var n, reported, received int
		if err := rows.Scan(&fact, &code, &n, &reported, &received); err != nil {
			return out, err
		}
		switch fact {
		case "RECEIPT":
			out.ShipmentsReceived, out.CartonsReported, out.CartonsReceived = n, reported, received
		case string(scorecard.Discrepancy):
			out.Discrepancies[code] += n
		case string(scorecard.Violation):
			out.Violations += n
		}
	}
	return out, rows.Err()
}
