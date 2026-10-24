package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
)

// uniqueViolation is the SQLSTATE of a duplicate key.
const uniqueViolation = "23505"

// saveErr maps a failed INSERT: a duplicate key is a lost create race.
func saveErr(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == uniqueViolation {
		return ports.ErrConcurrentUpdate
	}
	return err
}

func updated(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrConcurrentUpdate
	}
	return nil
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func zeroIfNil(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.UTC()
}

// SellerRepo is the Postgres ports.SellerRepository.
type SellerRepo struct{ pool *pgxpool.Pool }

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
func NewNodeViewRepo(pool *pgxpool.Pool) *NodeViewRepo { return &NodeViewRepo{pool: pool} }

var _ ports.NodeViewRepository = (*NodeViewRepo)(nil)

type dimsJSON struct {
	L int `json:"l"`
	W int `json:"w"`
	H int `json:"h"`
}

type limitsJSON struct {
	MaxCartonWeightG int       `json:"maxCartonWeightG,omitempty"`
	MaxCartonDimsMm  *dimsJSON `json:"maxCartonDimsMm,omitempty"`
	AcceptsPallets   *bool     `json:"acceptsPallets,omitempty"`
	RequiresSscc     *bool     `json:"requiresSscc,omitempty"`
}

// Get returns the node or ports.ErrNotFound.
func (r *NodeViewRepo) Get(ctx context.Context, nodeID string) (network.Node, error) {
	var (
		n      network.Node
		status string
		raw    []byte
	)
	err := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT node_id, node_type, status, profile_revision, inbound_limits FROM node_inbound_views WHERE node_id = $1`, nodeID).
		Scan(&n.ID, &n.Type, &status, &n.ProfileRevision, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return network.Node{}, ports.ErrNotFound
	}
	if err != nil {
		return network.Node{}, err
	}
	n.Status = network.NodeStatus(status)
	if raw != nil {
		var l limitsJSON
		if err := json.Unmarshal(raw, &l); err != nil {
			return network.Node{}, fmt.Errorf("decode node %s limits: %w", nodeID, err)
		}
		n.Limits = &network.InboundLimits{MaxCartonWeightG: l.MaxCartonWeightG, AcceptsPallets: l.AcceptsPallets, RequiresSscc: l.RequiresSscc}
		if l.MaxCartonDimsMm != nil {
			n.Limits.MaxCartonDims = &network.Dims{L: l.MaxCartonDimsMm.L, W: l.MaxCartonDimsMm.W, H: l.MaxCartonDimsMm.H}
		}
	}
	return n, nil
}

// Save inserts or replaces the node row.
func (r *NodeViewRepo) Save(ctx context.Context, n network.Node) error {
	var raw []byte
	if n.Limits != nil {
		l := limitsJSON{MaxCartonWeightG: n.Limits.MaxCartonWeightG, AcceptsPallets: n.Limits.AcceptsPallets, RequiresSscc: n.Limits.RequiresSscc}
		if d := n.Limits.MaxCartonDims; d != nil {
			l.MaxCartonDimsMm = &dimsJSON{L: d.L, W: d.W, H: d.H}
		}
		var err error
		if raw, err = json.Marshal(l); err != nil {
			return err
		}
	}
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO node_inbound_views (node_id, node_type, status, profile_revision, inbound_limits) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (node_id) DO UPDATE SET node_type = EXCLUDED.node_type, status = EXCLUDED.status,
			profile_revision = EXCLUDED.profile_revision, inbound_limits = EXCLUDED.inbound_limits`,
		n.ID, n.Type, string(n.Status), n.ProfileRevision, raw)
	return err
}

// PackagingViewRepo is the Postgres ports.PackagingViewRepository.
type PackagingViewRepo struct{ pool *pgxpool.Pool }

// NewPackagingViewRepo constructs a PackagingViewRepo over pool.
func NewPackagingViewRepo(pool *pgxpool.Pool) *PackagingViewRepo {
	return &PackagingViewRepo{pool: pool}
}

var _ ports.PackagingViewRepository = (*PackagingViewRepo)(nil)

const packagingColumns = `catalog_item_id, level, gtin, child_gtin, units_per_pack, active, declared_at, retired_at`

func scanPackLevel(row pgx.Row) (network.PackLevel, error) {
	var (
		p                 network.PackLevel
		level             string
		declared, retired *time.Time
	)
	err := row.Scan(&p.CatalogItemID, &level, &p.GTIN, &p.ChildGTIN, &p.UnitsPerPack, &p.Active, &declared, &retired)
	if errors.Is(err, pgx.ErrNoRows) {
		return network.PackLevel{}, ports.ErrNotFound
	}
	p.Level, p.DeclaredAt, p.RetiredAt = network.Level(level), zeroIfNil(declared), zeroIfNil(retired)
	return p, err
}

// Get returns the (item, level, gtin) row or ports.ErrNotFound.
func (r *PackagingViewRepo) Get(ctx context.Context, item string, level network.Level, gtin string) (network.PackLevel, error) {
	return scanPackLevel(queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT `+packagingColumns+` FROM catalog_pack_levels WHERE catalog_item_id = $1 AND level = $2 AND gtin = $3`, item, string(level), gtin))
}

// Save inserts or replaces the row.
func (r *PackagingViewRepo) Save(ctx context.Context, p network.PackLevel) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO catalog_pack_levels (`+packagingColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (catalog_item_id, level, gtin) DO UPDATE SET child_gtin = EXCLUDED.child_gtin, units_per_pack = EXCLUDED.units_per_pack,
			active = EXCLUDED.active, declared_at = EXCLUDED.declared_at, retired_at = EXCLUDED.retired_at`,
		p.CatalogItemID, string(p.Level), p.GTIN, p.ChildGTIN, p.UnitsPerPack, p.Active, nullTime(p.DeclaredAt), nullTime(p.RetiredAt))
	return err
}

// ListByItem returns every row of the catalogue item.
func (r *PackagingViewRepo) ListByItem(ctx context.Context, item string) ([]network.PackLevel, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+packagingColumns+` FROM catalog_pack_levels WHERE catalog_item_id = $1 ORDER BY level, gtin`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []network.PackLevel
	for rows.Next() {
		p, err := scanPackLevel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProcessedEventRepo is the Postgres ports.ProcessedEvents (the inbox).
type ProcessedEventRepo struct{ pool *pgxpool.Pool }

// NewProcessedEventRepo constructs a ProcessedEventRepo over pool.
func NewProcessedEventRepo(pool *pgxpool.Pool) *ProcessedEventRepo {
	return &ProcessedEventRepo{pool: pool}
}

var _ ports.ProcessedEvents = (*ProcessedEventRepo)(nil)

// Claim records the CloudEvents id and reports whether it was new. Inside a
// unit of work the INSERT shares the transaction of the event's effects, so a
// rollback un-claims it. A concurrent claim of the same id blocks on the primary
// key until the other transaction ends.
func (r *ProcessedEventRepo) Claim(ctx context.Context, eventID, eventType string, at time.Time) (bool, error) {
	tag, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO processed_events (event_id, event_type, processed_at) VALUES ($1, $2, $3) ON CONFLICT (event_id) DO NOTHING`, eventID, eventType, at)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// SsccSequence is the Postgres sequence the network's SSCC serial references come from.
type SsccSequence struct{ pool *pgxpool.Pool }

// NewSsccSequence constructs the sequence over pool.
func NewSsccSequence(pool *pgxpool.Pool) *SsccSequence { return &SsccSequence{pool: pool} }

// Next returns the next serial reference. Sequences are not rolled back: a
// number may be skipped, never reused.
func (s *SsccSequence) Next(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT nextval('sscc_serial_seq')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("next sscc serial: %w", err)
	}
	return n, nil
}
