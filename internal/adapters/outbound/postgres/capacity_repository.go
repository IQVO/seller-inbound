package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/capacity"
)

// CapacityRepo is the Postgres ports.CapacityAllocations (ADR 0015).
type CapacityRepo struct{ pool *pgxpool.Pool }

// NewCapacityRepo constructs a CapacityRepo over pool.
func NewCapacityRepo(pool *pgxpool.Pool) *CapacityRepo { return &CapacityRepo{pool: pool} }

var _ ports.CapacityAllocations = (*CapacityRepo)(nil)

// isNoRows reports a miss of the reservation table.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// Reserved answers the seller's reserved dm3 for the month and node; an
// unseen key answers 0. Inside a unit of work it reads the SAME transaction.
func (r *CapacityRepo) Reserved(ctx context.Context, sellerID, month, nodeID string) (int64, error) {
	var reserved int64
	err := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT reserved_dm3 FROM inbound_capacity_allocations WHERE seller_id = $1 AND month = $2 AND node_id = $3`,
		sellerID, month, nodeID).Scan(&reserved)
	if err != nil {
		if isNoRows(err) {
			return 0, nil
		}
		return 0, err
	}
	return reserved, nil
}

// Reserve adds dm3 to the key's reservation (0 is a no-op).
func (r *CapacityRepo) Reserve(ctx context.Context, sellerID, month, nodeID string, dm3 int64, at time.Time) error {
	if dm3 == 0 {
		return nil
	}
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO inbound_capacity_allocations (seller_id, month, node_id, reserved_dm3, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (seller_id, month, node_id)
		DO UPDATE SET reserved_dm3 = inbound_capacity_allocations.reserved_dm3 + EXCLUDED.reserved_dm3,
			updated_at = EXCLUDED.updated_at`,
		sellerID, month, nodeID, dm3, at)
	return err
}

// Release subtracts dm3 from the key's reservation, never below 0 (0 is a
// no-op).
func (r *CapacityRepo) Release(ctx context.Context, sellerID, month, nodeID string, dm3 int64) error {
	if dm3 == 0 {
		return nil
	}
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		UPDATE inbound_capacity_allocations
		SET reserved_dm3 = GREATEST(reserved_dm3 - $4, 0), updated_at = now()
		WHERE seller_id = $1 AND month = $2 AND node_id = $3`,
		sellerID, month, nodeID, dm3)
	return err
}

// Allocations answers the seller's reservations of the month, ordered by node
// id.
func (r *CapacityRepo) Allocations(ctx context.Context, sellerID, month string) ([]capacity.Allocation, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT seller_id, month, node_id, reserved_dm3, updated_at
		FROM inbound_capacity_allocations
		WHERE seller_id = $1 AND month = $2
		ORDER BY node_id`, sellerID, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []capacity.Allocation
	for rows.Next() {
		var a capacity.Allocation
		if err := rows.Scan(&a.SellerID, &a.Month, &a.NodeID, &a.ReservedDm3, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
