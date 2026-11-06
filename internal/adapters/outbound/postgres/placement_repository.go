package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
)

// DemandHeatRepo is the Postgres ports.DemandHeatView: the demand half of the
// advisory's read model (ADR 0012, table demand_heat_lines).
type DemandHeatRepo struct{ pool *pgxpool.Pool }

// NewDemandHeatRepo constructs a DemandHeatRepo over pool.
func NewDemandHeatRepo(pool *pgxpool.Pool) *DemandHeatRepo { return &DemandHeatRepo{pool: pool} }

var _ ports.DemandHeatView = (*DemandHeatRepo)(nil)

// RecordDemand replaces the assignment group's lines with the release's
// lines, in one transaction-bounded sweep.
func (r *DemandHeatRepo) RecordDemand(ctx context.Context, release network.DemandRelease) error {
	q := queryFor(ctx, r.pool)
	if _, err := q.Exec(ctx, `DELETE FROM demand_heat_lines WHERE assignment_id = $1 AND group_no = $2`,
		release.AssignmentID, release.GroupNo); err != nil {
		return err
	}
	for _, l := range release.LinesOf() {
		if _, err := q.Exec(ctx, `
			INSERT INTO demand_heat_lines (assignment_id, group_no, line_no, node_id, catalog_item_id, quantity, released_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			l.AssignmentID, l.GroupNo, l.LineNo, l.NodeID, l.CatalogItemID, l.Quantity, l.ReleasedAt); err != nil {
			return err
		}
	}
	return nil
}

// CancelDemand removes the group's lines; an unknown group is a no-op.
func (r *DemandHeatRepo) CancelDemand(ctx context.Context, assignmentID string, groupNo int) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx,
		`DELETE FROM demand_heat_lines WHERE assignment_id = $1 AND group_no = $2`, assignmentID, groupNo)
	return err
}

// RegionHeat sums the item's live lines per region of the node view, ordered
// by region id. A line whose node has no region yet contributes nothing until
// node-topology catches up.
func (r *DemandHeatRepo) RegionHeat(ctx context.Context, catalogItemID string, windowStart time.Time) ([]network.RegionHeat, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT v.region_id, SUM(d.quantity), MAX(d.released_at)
		FROM demand_heat_lines d
		JOIN node_inbound_views v ON v.node_id = d.node_id
		WHERE d.catalog_item_id = $1 AND d.released_at >= $2 AND v.region_id <> ''
		GROUP BY v.region_id
		HAVING SUM(d.quantity) > 0
		ORDER BY v.region_id`, catalogItemID, windowStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []network.RegionHeat
	for rows.Next() {
		var h network.RegionHeat
		if err := rows.Scan(&h.RegionID, &h.DemandUnits, &h.LastReleasedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListingRepo is the Postgres ports.ListingView: the availability half of the
// advisory's read model (ADR 0012, table listing_availability).
type ListingRepo struct{ pool *pgxpool.Pool }

// NewListingRepo constructs a ListingRepo over pool.
func NewListingRepo(pool *pgxpool.Pool) *ListingRepo { return &ListingRepo{pool: pool} }

var _ ports.ListingView = (*ListingRepo)(nil)

// SaveAvailability upserts the (item, node) row.
func (r *ListingRepo) SaveAvailability(ctx context.Context, row network.NodeAvailability) error {
	_, err := queryFor(ctx, r.pool).Exec(ctx, `
		INSERT INTO listing_availability (catalog_item_id, node_id, listing_id, available_qty, as_of)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (catalog_item_id, node_id) DO UPDATE SET
			listing_id = EXCLUDED.listing_id, available_qty = EXCLUDED.available_qty, as_of = EXCLUDED.as_of`,
		row.CatalogItemID, row.NodeID, row.ListingID, row.AvailableQty, row.AsOf)
	return err
}

// Availability returns every node's declared availability of the item,
// ordered by node id.
func (r *ListingRepo) Availability(ctx context.Context, catalogItemID string) ([]network.NodeAvailability, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `
		SELECT listing_id, catalog_item_id, node_id, available_qty, as_of
		FROM listing_availability WHERE catalog_item_id = $1 ORDER BY node_id`, catalogItemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []network.NodeAvailability
	for rows.Next() {
		var a network.NodeAvailability
		if err := rows.Scan(&a.ListingID, &a.CatalogItemID, &a.NodeID, &a.AvailableQty, &a.AsOf); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PlacementPlanRepo is the Postgres ports.PlacementPlanRepository.
type PlacementPlanRepo struct{ pool *pgxpool.Pool }

// NewPlacementPlanRepo constructs a PlacementPlanRepo over pool.
func NewPlacementPlanRepo(pool *pgxpool.Pool) *PlacementPlanRepo {
	return &PlacementPlanRepo{pool: pool}
}

var _ ports.PlacementPlanRepository = (*PlacementPlanRepo)(nil)

type suggestionJSON struct {
	NodeID           string `json:"nodeId"`
	RegionID         string `json:"regionId"`
	SuggestedUnits   int    `json:"suggestedUnits"`
	RegionDemand     int    `json:"regionDemandUnits"`
	NodeAvailability int    `json:"nodeAvailabilityUnits"`
	CoverDays        int    `json:"coverDays"`
}

func suggestionsToJSON(in []placementplan.Suggestion) ([]byte, error) {
	out := make([]suggestionJSON, len(in))
	for i, s := range in {
		out[i] = suggestionJSON{NodeID: s.NodeID, RegionID: s.RegionID, SuggestedUnits: s.SuggestedUnits,
			RegionDemand: s.Evidence.RegionDemandUnits, NodeAvailability: s.Evidence.NodeAvailabilityUnits,
			CoverDays: s.Evidence.CoverDays}
	}
	return json.Marshal(out)
}

func suggestionsFromJSON(raw []byte) ([]placementplan.Suggestion, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rows []suggestionJSON
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	out := make([]placementplan.Suggestion, len(rows))
	for i, s := range rows {
		out[i] = placementplan.Suggestion{NodeID: s.NodeID, RegionID: s.RegionID, SuggestedUnits: s.SuggestedUnits,
			Evidence: placementplan.Evidence{RegionDemandUnits: s.RegionDemand, NodeAvailabilityUnits: s.NodeAvailability,
				CoverDays: s.CoverDays}}
	}
	return out, nil
}

const placementPlanColumns = `plan_id, seller_id, catalog_item_id, cover_days, window_days, basis, state, suggestions, created_at, accepted_at, superseded_at, version`

func scanPlacementPlan(row pgx.Row) (*placementplan.Plan, error) {
	var (
		s          placementplan.Snapshot
		state      string
		raw        []byte
		accepted   *time.Time
		superseded *time.Time
	)
	if err := row.Scan(&s.ID, &s.SellerID, &s.CatalogItemID, &s.CoverDays, &s.WindowDays, &s.Basis, &state, &raw,
		&s.CreatedAt, &accepted, &superseded, &s.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ports.ErrNotFound
		}
		return nil, err
	}
	s.State = placementplan.State(state)
	s.AcceptedAt, s.SupersededAt = zeroIfNil(accepted), zeroIfNil(superseded)
	var err error
	if s.Suggestions, err = suggestionsFromJSON(raw); err != nil {
		return nil, err
	}
	return placementplan.Restore(s)
}

// Get returns the plan or ports.ErrNotFound.
func (r *PlacementPlanRepo) Get(ctx context.Context, id placementplan.ID) (*placementplan.Plan, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx,
		`SELECT `+placementPlanColumns+` FROM placement_plans WHERE plan_id = $1`, string(id))
	return scanPlacementPlan(row)
}

// Save persists the plan under the concurrency rule of ports' package comment.
func (r *PlacementPlanRepo) Save(ctx context.Context, p *placementplan.Plan) error {
	s := p.Snapshot()
	raw, err := suggestionsToJSON(s.Suggestions)
	if err != nil {
		return err
	}
	q := queryFor(ctx, r.pool)
	if s.Version == 1 {
		_, err := q.Exec(ctx, `
			INSERT INTO placement_plans (plan_id, seller_id, catalog_item_id, cover_days, window_days, basis, state, suggestions, created_at, accepted_at, superseded_at, version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			string(s.ID), s.SellerID, s.CatalogItemID, s.CoverDays, s.WindowDays, s.Basis, string(s.State), raw,
			s.CreatedAt, nullTime(s.AcceptedAt), nullTime(s.SupersededAt), s.Version)
		return saveErr(err)
	}
	tag, err := q.Exec(ctx, `
		UPDATE placement_plans SET cover_days = $2, window_days = $3, basis = $4, state = $5, suggestions = $6,
			accepted_at = $7, superseded_at = $8, version = $9
		WHERE plan_id = $1 AND version = $10`,
		string(s.ID), s.CoverDays, s.WindowDays, s.Basis, string(s.State), raw,
		nullTime(s.AcceptedAt), nullTime(s.SupersededAt), s.Version, s.Version-1)
	return updated(tag, err)
}

// LatestOf returns the seller's newest plan of the item, or ErrNotFound.
func (r *PlacementPlanRepo) LatestOf(ctx context.Context, sellerID, catalogItemID string) (*placementplan.Plan, error) {
	row := queryFor(ctx, r.pool).QueryRow(ctx, `
		SELECT `+placementPlanColumns+` FROM placement_plans
		WHERE seller_id = $1 AND catalog_item_id = $2
		ORDER BY created_at DESC, plan_id DESC LIMIT 1`, sellerID, catalogItemID)
	return scanPlacementPlan(row)
}

// List returns the seller's plans (of one item when set), newest first,
// keyset-paged after (afterCreatedAt, afterPlanID).
func (r *PlacementPlanRepo) List(ctx context.Context, sellerID, catalogItemID string, afterCreatedAt time.Time, afterPlanID string, limit int) ([]*placementplan.Plan, error) {
	sql := `SELECT ` + placementPlanColumns + ` FROM placement_plans WHERE seller_id = $1`
	args := []any{sellerID}
	if catalogItemID != "" {
		args = append(args, catalogItemID)
		sql += ` AND catalog_item_id = $` + strconv.Itoa(len(args))
	}
	if !afterCreatedAt.IsZero() {
		args = append(args, afterCreatedAt, afterPlanID)
		n := strconv.Itoa(len(args) - 1)
		sql += ` AND (created_at, plan_id) < ($` + n + `, $` + strconv.Itoa(len(args)) + `)`
	}
	args = append(args, limit)
	sql += ` ORDER BY created_at DESC, plan_id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := queryFor(ctx, r.pool).Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*placementplan.Plan
	for rows.Next() {
		p, err := scanPlacementPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
