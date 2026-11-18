package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// Reader is the Postgres READER (report.Reader). Every query filters the
// business instant with `>= from AND < to`, and days are UTC calendar days.
// SQL only counts, sums and takes percentile_cont; the bounded per-item list,
// the percentile ranks and the per-day assembly come from
// internal/analytics/report.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ report.Reader = (*Reader)(nil)

// countsSQL: the plans confirmed, the advisories issued and the receipts
// published, per UTC day of their business instant.
//
//	$1 from, $2 to
const countsSQL = `
	SELECT day,
	       count(*) FILTER (WHERE fact = 'plan'),
	       count(*) FILTER (WHERE fact = 'advisory'),
	       count(*) FILTER (WHERE fact = 'receipt')
	FROM (
		SELECT (confirmed_at AT TIME ZONE 'UTC')::date AS day, 'plan' AS fact
		  FROM plan_confirmations WHERE confirmed_at >= $1 AND confirmed_at < $2
		UNION ALL
		SELECT (created_at AT TIME ZONE 'UTC')::date, 'advisory'
		  FROM placement_advisories WHERE created_at >= $1 AND created_at < $2
		UNION ALL
		SELECT (received_at AT TIME ZONE 'UTC')::date, 'receipt'
		  FROM shipment_receipts WHERE received_at >= $1 AND received_at < $2
	) facts
	GROUP BY day
	ORDER BY day`

// InboundCounts implements report.Reader.
func (r *Reader) InboundCounts(ctx context.Context, rg report.Range) ([]report.DayCounts, error) {
	return collect(ctx, r.pool, countsSQL, func(rows pgx.Rows) (report.DayCounts, error) {
		var d report.DayCounts
		err := rows.Scan(&d.Day, &d.PlansConfirmed, &d.AdvisoriesIssued, &d.ShipmentsReceived)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To)
}

// itemsSQL: the received-stock lines per UTC day of receipt and GTIN. The
// catalog item is the one any line of that day resolved (” when none did).
//
//	$1 from, $2 to
const itemsSQL = `
	SELECT (received_at AT TIME ZONE 'UTC')::date AS day, gtin,
	       COALESCE(max(NULLIF(catalog_item_id, '')), ''),
	       count(*), COALESCE(sum(qty_eaches), 0)::bigint
	FROM received_stock_lines
	WHERE received_at >= $1 AND received_at < $2
	GROUP BY day, gtin
	ORDER BY day, gtin`

// ReceivedItems implements report.Reader.
func (r *Reader) ReceivedItems(ctx context.Context, rg report.Range) ([]report.DayItem, error) {
	return collect(ctx, r.pool, itemsSQL, func(rows pgx.Rows) (report.DayItem, error) {
		var d report.DayItem
		err := rows.Scan(&d.Day, &d.GTIN, &d.CatalogItemID, &d.Publications, &d.QtyEaches)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To)
}

// latencySQL: every received line of the range that resolved its catalog item,
// with the LATEST placement advisory of the same seller and item at or before
// the receipt (a line without one is not a sample), then per UTC day of
// receipt the samples and the two percentiles of the seconds between the two.
//
//	$1 from, $2 to, $3 p50 rank, $4 p95 rank
const latencySQL = `
	WITH samples AS (
		SELECT (l.received_at AT TIME ZONE 'UTC')::date AS day,
		       EXTRACT(EPOCH FROM (l.received_at - a.created_at))::float8 AS seconds
		FROM received_stock_lines l
		CROSS JOIN LATERAL (
			SELECT p.created_at FROM placement_advisories p
			 WHERE p.seller_id = l.seller_id AND p.catalog_item_id = l.catalog_item_id AND p.created_at <= l.received_at
			 ORDER BY p.created_at DESC
			 LIMIT 1
		) a
		WHERE l.catalog_item_id <> '' AND l.received_at >= $1 AND l.received_at < $2
	)
	SELECT day, count(*),
	       percentile_cont($3::float8) WITHIN GROUP (ORDER BY seconds),
	       percentile_cont($4::float8) WITHIN GROUP (ORDER BY seconds)
	FROM samples
	GROUP BY day
	ORDER BY day`

// AdvisoryToReceipt implements report.Reader.
func (r *Reader) AdvisoryToReceipt(ctx context.Context, rg report.Range) ([]report.DayLatency, error) {
	return collect(ctx, r.pool, latencySQL, func(rows pgx.Rows) (report.DayLatency, error) {
		var d report.DayLatency
		err := rows.Scan(&d.Day, &d.Samples, &d.P50, &d.P95)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.LatencyP50(), report.LatencyP95())
}

// LastEventAt implements report.Reader: the newest CloudEvents time among the
// applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

// Ping checks the analytical database answers (the reports' /readyz).
func (r *Reader) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// collect runs query with args and scans every row; the result is a non-nil
// slice even when empty.
func collect[T any](ctx context.Context, pool *pgxpool.Pool, query string, scan func(pgx.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: query: %w", err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("analyticsstore: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: rows: %w", err)
	}
	return out, nil
}
