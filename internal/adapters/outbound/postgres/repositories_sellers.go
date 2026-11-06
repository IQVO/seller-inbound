package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewSellerRepo constructs a SellerRepo over pool.
func NewSellerRepo(pool *pgxpool.Pool) *SellerRepo { return &SellerRepo{pool: pool} }

var _ ports.SellerRepository = (*SellerRepo)(nil)

// Get returns the account or ports.ErrNotFound.
func (r *SellerRepo) Get(ctx context.Context, id seller.ID) (*seller.Account, error) {
	var (
		s         seller.Snapshot
		suspended *time.Time
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT seller_id, name, state, onboarded_at, suspended_at, suspend_reason, version
		FROM sellers WHERE seller_id = $1`, string(id)).
		Scan(&s.ID, &s.Name, &s.State, &s.OnboardedAt, &suspended, &s.SuspendReason, &s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.SuspendedAt = zeroIfNil(suspended)
	return seller.Restore(s)
}

// Save persists the account under the optimistic-concurrency rule.
func (r *SellerRepo) Save(ctx context.Context, a *seller.Account) error {
	s := a.Snapshot()
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `
			INSERT INTO sellers (seller_id, name, state, onboarded_at, suspended_at, suspend_reason, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			string(s.ID), s.Name, string(s.State), s.OnboardedAt, nullTime(s.SuspendedAt), s.SuspendReason, s.Version)
		return saveErr(err)
	}
	tag, err := q.Exec(ctx, `
		UPDATE sellers SET name = $2, state = $3, suspended_at = $4, suspend_reason = $5, version = $6
		WHERE seller_id = $1 AND version = $7`,
		string(s.ID), s.Name, string(s.State), nullTime(s.SuspendedAt), s.SuspendReason, s.Version, s.Version-1)
	return updated(tag, err)
}

// PlanRepo is the Postgres ports.PlanRepository.
type PlanRepo struct{ pool *pgxpool.Pool }

// NewPlanRepo constructs a PlanRepo over pool.
func NewPlanRepo(pool *pgxpool.Pool) *PlanRepo { return &PlanRepo{pool: pool} }

var _ ports.PlanRepository = (*PlanRepo)(nil)

type lineJSON struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
}

// Get returns the plan or ports.ErrNotFound.
func (r *PlanRepo) Get(ctx context.Context, id inboundplan.ID) (*inboundplan.Plan, error) {
	var (
		s                      inboundplan.Snapshot
		lines                  []byte
		confirmed, cancelledAt *time.Time
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT plan_id, seller_id, destination_node_id, packing_option, state, lines, created_at, confirmed_at, cancelled_at, version
		FROM inbound_plans WHERE plan_id = $1`, string(id)).
		Scan(&s.ID, &s.SellerID, &s.DestinationID, &s.Packing, &s.State, &lines, &s.CreatedAt, &confirmed, &cancelledAt, &s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var in []lineJSON
	if err := json.Unmarshal(lines, &in); err != nil {
		return nil, fmt.Errorf("decode plan %s lines: %w", id, err)
	}
	for _, l := range in {
		s.Lines = append(s.Lines, inboundplan.Line{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches})
	}
	s.ConfirmedAt, s.CancelledAt = zeroIfNil(confirmed), zeroIfNil(cancelledAt)
	return inboundplan.Restore(s)
}

// Save persists the plan under the optimistic-concurrency rule.
func (r *PlanRepo) Save(ctx context.Context, p *inboundplan.Plan) error {
	s := p.Snapshot()
	lines := make([]lineJSON, len(s.Lines))
	for i, l := range s.Lines {
		lines[i] = lineJSON{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches}
	}
	raw, err := json.Marshal(lines)
	if err != nil {
		return err
	}
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `
			INSERT INTO inbound_plans (plan_id, seller_id, destination_node_id, packing_option, state, lines, created_at, confirmed_at, cancelled_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			string(s.ID), s.SellerID, s.DestinationID, string(s.Packing), string(s.State), raw, s.CreatedAt,
			nullTime(s.ConfirmedAt), nullTime(s.CancelledAt), s.Version)
		return saveErr(err)
	}
	tag, err := q.Exec(ctx, `
		UPDATE inbound_plans SET state = $2, confirmed_at = $3, cancelled_at = $4, version = $5
		WHERE plan_id = $1 AND version = $6`,
		string(s.ID), string(s.State), nullTime(s.ConfirmedAt), nullTime(s.CancelledAt), s.Version, s.Version-1)
	return updated(tag, err)
}

// NodeViewRepo is the Postgres ports.NodeViewRepository.
type NodeViewRepo struct{ pool *pgxpool.Pool }

// NewNodeViewRepo constructs a NodeViewRepo over pool.
