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
	"github.com/claudioed/seller-inbound/internal/domain/network"
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
		SELECT node_id, node_type, status, operator, region_id, profile_revision, inbound_limits FROM node_inbound_views WHERE node_id = $1`, nodeID).
		Scan(&n.ID, &n.Type, &status, &n.Operator, &n.RegionID, &n.ProfileRevision, &raw)
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
		INSERT INTO node_inbound_views (node_id, node_type, status, operator, region_id, profile_revision, inbound_limits) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (node_id) DO UPDATE SET node_type = EXCLUDED.node_type, status = EXCLUDED.status,
			operator = EXCLUDED.operator, region_id = EXCLUDED.region_id,
			profile_revision = EXCLUDED.profile_revision, inbound_limits = EXCLUDED.inbound_limits`,
		n.ID, n.Type, string(n.Status), n.Operator, n.RegionID, n.ProfileRevision, raw)
	return err
}

// VendorFulfillmentCenters returns every ACTIVE vendor FULFILLMENT_CENTER,
// ordered by node id.
func (r *NodeViewRepo) VendorFulfillmentCenters(ctx context.Context) ([]network.Node, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT node_id, node_type, status, operator, region_id, profile_revision, inbound_limits
		FROM node_inbound_views
		WHERE node_type = $1 AND status = $2 AND operator = $3
		ORDER BY node_id`, network.TypeFulfillmentCenter, network.NodeActive, network.OperatorVendor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []network.Node
	for rows.Next() {
		var (
			n      network.Node
			status string
			raw    []byte
		)
		if err := rows.Scan(&n.ID, &n.Type, &status, &n.Operator, &n.RegionID, &n.ProfileRevision, &raw); err != nil {
			return nil, err
		}
		n.Status = network.NodeStatus(status)
		if raw != nil {
			var l limitsJSON
			if err := json.Unmarshal(raw, &l); err != nil {
				return nil, fmt.Errorf("decode node %s limits: %w", n.ID, err)
			}
			n.Limits = &network.InboundLimits{MaxCartonWeightG: l.MaxCartonWeightG, AcceptsPallets: l.AcceptsPallets, RequiresSscc: l.RequiresSscc}
			if l.MaxCartonDimsMm != nil {
				n.Limits.MaxCartonDims = &network.Dims{L: l.MaxCartonDimsMm.L, W: l.MaxCartonDimsMm.W, H: l.MaxCartonDimsMm.H}
			}
		}
		out = append(out, n)
	}
	return out, rows.Err()
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
