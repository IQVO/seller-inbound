package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
)

// ShipmentRepo is the Postgres ports.ShipmentRepository.
type ShipmentRepo struct{ pool *pgxpool.Pool }

// NewShipmentRepo constructs a ShipmentRepo over pool.
func NewShipmentRepo(pool *pgxpool.Pool) *ShipmentRepo { return &ShipmentRepo{pool: pool} }

var _ ports.ShipmentRepository = (*ShipmentRepo)(nil)

type contentJSON struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
	Lot           string `json:"lot,omitempty"`
	Expiry        string `json:"expiry,omitempty"`
}

type cartonJSON struct {
	SSCC         string        `json:"sscc"`
	ParentSSCC   string        `json:"parentSscc,omitempty"`
	Kind         string        `json:"kind"`
	Contents     []contentJSON `json:"contents"`
	GrossWeightG int           `json:"grossWeightG"`
	Dims         dimsJSON      `json:"dimsMm"`
	Sealed       bool          `json:"sealed"`
	Receipt      string        `json:"receipt,omitempty"`
}

func encodeCartons(in []inboundshipment.Carton) ([]byte, error) {
	out := make([]cartonJSON, len(in))
	for i, c := range in {
		contents := make([]contentJSON, len(c.Contents))
		for j, ct := range c.Contents {
			contents[j] = contentJSON{CatalogItemID: ct.CatalogItemID, GTIN: ct.GTIN, QtyEaches: ct.QtyEaches, Lot: ct.Lot, Expiry: ct.Expiry}
		}
		out[i] = cartonJSON{SSCC: c.SSCC, ParentSSCC: c.ParentSSCC, Kind: string(c.Kind), Contents: contents, GrossWeightG: c.GrossWeightG,
			Dims: dimsJSON{L: c.Dims.L, W: c.Dims.W, H: c.Dims.H}, Sealed: c.Sealed, Receipt: string(c.Receipt)}
	}
	return json.Marshal(out)
}

func decodeCartons(raw []byte) ([]inboundshipment.Carton, error) {
	var in []cartonJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := make([]inboundshipment.Carton, len(in))
	for i, c := range in {
		contents := make([]inboundshipment.Content, len(c.Contents))
		for j, ct := range c.Contents {
			contents[j] = inboundshipment.Content{CatalogItemID: ct.CatalogItemID, GTIN: ct.GTIN, QtyEaches: ct.QtyEaches, Lot: ct.Lot, Expiry: ct.Expiry}
		}
		out[i] = inboundshipment.Carton{SSCC: c.SSCC, ParentSSCC: c.ParentSSCC, Kind: inboundshipment.Kind(c.Kind), Contents: contents,
			GrossWeightG: c.GrossWeightG, Dims: inboundshipment.Dims{L: c.Dims.L, W: c.Dims.W, H: c.Dims.H}, Sealed: c.Sealed,
			Receipt: inboundshipment.ReceiptStatus(c.Receipt)}
	}
	return out, nil
}

const shipmentColumns = `shipment_id, plan_id, seller_id, node_id, state, cartons, unexpected, discrepant, created_at, labelled_at, shipped_at, received_at, version`

func scanShipment(row pgx.Row) (*inboundshipment.Shipment, error) {
	var (
		s                           inboundshipment.Snapshot
		cartons, unexpected         []byte
		labelled, shipped, received *time.Time
	)
	err := row.Scan(&s.ID, &s.PlanID, &s.SellerID, &s.NodeID, &s.State, &cartons, &unexpected, &s.Discrepant, &s.CreatedAt, &labelled, &shipped, &received, &s.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.Cartons, err = decodeCartons(cartons); err != nil {
		return nil, fmt.Errorf("decode shipment %s cartons: %w", s.ID, err)
	}
	if err := json.Unmarshal(unexpected, &s.Unexpected); err != nil {
		return nil, fmt.Errorf("decode shipment %s unexpected: %w", s.ID, err)
	}
	s.LabelledAt, s.ShippedAt, s.ReceivedAt = zeroIfNil(labelled), zeroIfNil(shipped), zeroIfNil(received)
	return inboundshipment.Restore(s)
}

// Get returns the shipment or ports.ErrNotFound.
func (r *ShipmentRepo) Get(ctx context.Context, id inboundshipment.ID) (*inboundshipment.Shipment, error) {
	return scanShipment(queryFor(ctx, r.pool).QueryRow(ctx, `SELECT `+shipmentColumns+` FROM inbound_shipments WHERE shipment_id = $1`, string(id)))
}

// ListByPlan returns the plan's shipments, oldest first.
func (r *ShipmentRepo) ListByPlan(ctx context.Context, planID inboundplan.ID) ([]*inboundshipment.Shipment, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+shipmentColumns+` FROM inbound_shipments WHERE plan_id = $1 ORDER BY created_at, shipment_id`, string(planID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*inboundshipment.Shipment
	for rows.Next() {
		s, err := scanShipment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Save registers the carton SSCCs network-wide, then persists the shipment under
// the optimistic-concurrency rule. A registry row held by ANOTHER shipment makes
// the statement insert fewer rows than there are SSCCs: nothing is written and
// the caller gets ports.ErrSsccTaken (the transaction stays usable).
func (r *ShipmentRepo) Save(ctx context.Context, sh *inboundshipment.Shipment) error {
	s := sh.Snapshot()
	q := queryFor(ctx, r.pool)
	if err := registerSSCCs(ctx, q, string(s.ID), s); err != nil {
		return err
	}
	cartons, err := encodeCartons(s.Cartons)
	if err != nil {
		return err
	}
	unexpected, err := json.Marshal(append([]string{}, s.Unexpected...))
	if err != nil {
		return err
	}
	if s.Version == 1 {
		_, err := q.Exec(ctx, `
			INSERT INTO inbound_shipments (`+shipmentColumns+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			string(s.ID), s.PlanID, s.SellerID, s.NodeID, string(s.State), cartons, unexpected, s.Discrepant, s.CreatedAt,
			nullTime(s.LabelledAt), nullTime(s.ShippedAt), nullTime(s.ReceivedAt), s.Version)
		return saveErr(err)
	}
	tag, err := q.Exec(ctx, `
		UPDATE inbound_shipments SET state = $2, cartons = $3, unexpected = $4, discrepant = $5, labelled_at = $6, shipped_at = $7, received_at = $8, version = $9
		WHERE shipment_id = $1 AND version = $10`,
		string(s.ID), string(s.State), cartons, unexpected, s.Discrepant, nullTime(s.LabelledAt), nullTime(s.ShippedAt), nullTime(s.ReceivedAt), s.Version, s.Version-1)
	return updated(tag, err)
}

// registerSSCCs inserts the shipment's SSCCs into the registry. A row that
// already belongs to this shipment is left alone; a row of another shipment is
// not touched and shows up as a shortfall in RowsAffected.
func registerSSCCs(ctx context.Context, q querier, shipmentID string, s inboundshipment.Snapshot) error {
	if len(s.Cartons) == 0 {
		return nil
	}
	ssccs := make([]string, len(s.Cartons))
	for i, c := range s.Cartons {
		ssccs[i] = c.SSCC
	}
	tag, err := q.Exec(ctx, `
		INSERT INTO sscc_registry (sscc, shipment_id) SELECT unnest($1::text[]), $2
		ON CONFLICT (sscc) DO UPDATE SET shipment_id = sscc_registry.shipment_id WHERE sscc_registry.shipment_id = EXCLUDED.shipment_id`,
		ssccs, shipmentID)
	if err != nil {
		return err
	}
	if int(tag.RowsAffected()) != len(ssccs) {
		return ports.ErrSsccTaken
	}
	return nil
}
