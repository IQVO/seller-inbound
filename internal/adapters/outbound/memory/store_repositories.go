package memory

import (
	"context"
	"sort"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
)

// SellerRepository is the in-memory ports.SellerRepository.
type SellerRepository struct{ s *Store }

// Get returns the account or ports.ErrNotFound.
func (r *SellerRepository) Get(ctx context.Context, id seller.ID) (*seller.Account, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.sellers[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return seller.Restore(snap)
}

// Save stores the account under the concurrency rule.
func (r *SellerRepository) Save(ctx context.Context, a *seller.Account) error {
	defer r.s.enter(ctx)()
	old, ok := r.s.sellers[a.ID()]
	if !versionOK(old.Version, ok, a.Version()) {
		return ports.ErrConcurrentUpdate
	}
	r.s.sellers[a.ID()] = a.Snapshot()
	return nil
}

// PlanRepository is the in-memory ports.PlanRepository.
type PlanRepository struct{ s *Store }

// Get returns the plan or ports.ErrNotFound.
func (r *PlanRepository) Get(ctx context.Context, id inboundplan.ID) (*inboundplan.Plan, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.plans[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return inboundplan.Restore(snap)
}

// Save stores the plan under the concurrency rule.
func (r *PlanRepository) Save(ctx context.Context, p *inboundplan.Plan) error {
	defer r.s.enter(ctx)()
	old, ok := r.s.plans[p.ID()]
	if !versionOK(old.Version, ok, p.Version()) {
		return ports.ErrConcurrentUpdate
	}
	r.s.plans[p.ID()] = p.Snapshot()
	return nil
}

// ShipmentRepository is the in-memory ports.ShipmentRepository.
type ShipmentRepository struct{ s *Store }

// Get returns the shipment or ports.ErrNotFound.
func (r *ShipmentRepository) Get(ctx context.Context, id inboundshipment.ID) (*inboundshipment.Shipment, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.shipments[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return inboundshipment.Restore(snap)
}

// ListByPlan returns the plan's shipments ordered by (createdAt, id).
func (r *ShipmentRepository) ListByPlan(ctx context.Context, planID inboundplan.ID) ([]*inboundshipment.Shipment, error) {
	defer r.s.enter(ctx)()
	var snaps []inboundshipment.Snapshot
	for _, snap := range r.s.shipments {
		if snap.PlanID == string(planID) {
			snaps = append(snaps, snap)
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].CreatedAt.Equal(snaps[j].CreatedAt) {
			return snaps[i].CreatedAt.Before(snaps[j].CreatedAt)
		}
		return snaps[i].ID < snaps[j].ID
	})
	out := make([]*inboundshipment.Shipment, 0, len(snaps))
	for _, snap := range snaps {
		sh, err := inboundshipment.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, nil
}

// Save registers the SSCCs and stores the shipment under the concurrency rule.
func (r *ShipmentRepository) Save(ctx context.Context, sh *inboundshipment.Shipment) error {
	defer r.s.enter(ctx)()
	snap := sh.Snapshot()
	for _, c := range snap.Cartons {
		if owner, held := r.s.sscc[c.SSCC]; held && owner != snap.ID {
			return ports.ErrSsccTaken
		}
	}
	old, ok := r.s.shipments[snap.ID]
	if !versionOK(old.Version, ok, snap.Version) {
		return ports.ErrConcurrentUpdate
	}
	for _, c := range snap.Cartons {
		r.s.sscc[c.SSCC] = snap.ID
	}
	r.s.shipments[snap.ID] = snap
	return nil
}

// NodeViewRepository is the in-memory ports.NodeViewRepository.
type NodeViewRepository struct{ s *Store }

// Get returns the node or ports.ErrNotFound.
func (r *NodeViewRepository) Get(ctx context.Context, nodeID string) (network.Node, error) {
	defer r.s.enter(ctx)()
	n, ok := r.s.nodes[nodeID]
	if !ok {
		return network.Node{}, ports.ErrNotFound
	}
	return n, nil
}

// Save inserts or replaces the node row.
func (r *NodeViewRepository) Save(ctx context.Context, n network.Node) error {
	defer r.s.enter(ctx)()
	r.s.nodes[n.ID] = n
	return nil
}

// PackagingViewRepository is the in-memory ports.PackagingViewRepository.
type PackagingViewRepository struct{ s *Store }

// Get returns the row or ports.ErrNotFound.
func (r *PackagingViewRepository) Get(ctx context.Context, item string, level network.Level, gtin string) (network.PackLevel, error) {
	defer r.s.enter(ctx)()
	p, ok := r.s.packaging[packKey{item, level, gtin}]
	if !ok {
		return network.PackLevel{}, ports.ErrNotFound
	}
	return p, nil
}

// Save inserts or replaces the row.
func (r *PackagingViewRepository) Save(ctx context.Context, p network.PackLevel) error {
	defer r.s.enter(ctx)()
	r.s.packaging[packKey{p.CatalogItemID, p.Level, p.GTIN}] = p
	return nil
}

// ListByItem returns the rows of the item ordered by (level, gtin).
func (r *PackagingViewRepository) ListByItem(ctx context.Context, item string) ([]network.PackLevel, error) {
	defer r.s.enter(ctx)()
	var out []network.PackLevel
	for k, p := range r.s.packaging {
		if k.item == item {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level < out[j].Level
		}
		return out[i].GTIN < out[j].GTIN
	})
	return out, nil
}
