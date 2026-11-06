package memory

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
)

// The placement doubles implement the out ports.
var (
	_ ports.DemandHeatView          = (*DemandHeatView)(nil)
	_ ports.ListingView             = (*ListingView)(nil)
	_ ports.PlacementPlanRepository = (*PlacementPlanRepository)(nil)
)

// DemandHeatView is the in-memory ports.DemandHeatView: one slice of lines per
// assignment group, replaced wholesale by each release.
type DemandHeatView struct{ s *Store }

// RecordDemand replaces the group's lines with the release's lines.
func (r *DemandHeatView) RecordDemand(ctx context.Context, release network.DemandRelease) error {
	defer r.s.enter(ctx)()
	r.s.demand[demandKey{release.AssignmentID, release.GroupNo}] = release.LinesOf()
	return nil
}

// CancelDemand removes the group's lines; an unknown group is a no-op.
func (r *DemandHeatView) CancelDemand(ctx context.Context, assignmentID string, groupNo int) error {
	defer r.s.enter(ctx)()
	delete(r.s.demand, demandKey{assignmentID, groupNo})
	return nil
}

// RegionHeat sums the item's live lines per region of the node view, ordered
// by region id. A line whose node has no region yet (the NodeInboundView has
// not seen it) contributes nothing until node-topology catches up.
func (r *DemandHeatView) RegionHeat(ctx context.Context, catalogItemID string, windowStart time.Time) ([]network.RegionHeat, error) {
	defer r.s.enter(ctx)()
	byRegion := map[string]*network.RegionHeat{}
	for _, lines := range r.s.demand {
		for _, l := range lines {
			if l.CatalogItemID != catalogItemID || l.ReleasedAt.Before(windowStart) {
				continue
			}
			region := r.s.nodes[l.NodeID].RegionID
			if region == "" {
				continue
			}
			h, ok := byRegion[region]
			if !ok {
				h = &network.RegionHeat{RegionID: region}
				byRegion[region] = h
			}
			h.DemandUnits += l.Quantity
			if l.ReleasedAt.After(h.LastReleasedAt) {
				h.LastReleasedAt = l.ReleasedAt
			}
		}
	}
	out := make([]network.RegionHeat, 0, len(byRegion))
	for _, h := range byRegion {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RegionID < out[j].RegionID })
	return out, nil
}

// ListingView is the in-memory ports.ListingView.
type ListingView struct{ s *Store }

// SaveAvailability upserts the (item, node) row.
func (r *ListingView) SaveAvailability(ctx context.Context, row network.NodeAvailability) error {
	defer r.s.enter(ctx)()
	r.s.listings[availKey{row.CatalogItemID, row.NodeID}] = row
	return nil
}

// Availability returns every node's declared availability of the item,
// ordered by node id.
func (r *ListingView) Availability(ctx context.Context, catalogItemID string) ([]network.NodeAvailability, error) {
	defer r.s.enter(ctx)()
	var out []network.NodeAvailability
	for k, row := range r.s.listings {
		if k.item == catalogItemID {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

// PlacementPlanRepository is the in-memory ports.PlacementPlanRepository.
type PlacementPlanRepository struct{ s *Store }

// Get returns the plan or ports.ErrNotFound.
func (r *PlacementPlanRepository) Get(ctx context.Context, id placementplan.ID) (*placementplan.Plan, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.placementPlans[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return placementplan.Restore(snap)
}

// Save stores the plan under the concurrency rule.
func (r *PlacementPlanRepository) Save(ctx context.Context, p *placementplan.Plan) error {
	defer r.s.enter(ctx)()
	old, ok := r.s.placementPlans[p.ID()]
	if !versionOK(old.Version, ok, p.Version()) {
		return ports.ErrConcurrentUpdate
	}
	r.s.placementPlans[p.ID()] = p.Snapshot()
	return nil
}

// LatestOf returns the newest plan of the seller and item, or ErrNotFound.
func (r *PlacementPlanRepository) LatestOf(ctx context.Context, sellerID, catalogItemID string) (*placementplan.Plan, error) {
	defer r.s.enter(ctx)()
	var latest placementplan.Snapshot
	found := false
	for _, snap := range r.s.placementPlans {
		if snap.SellerID != sellerID || snap.CatalogItemID != catalogItemID {
			continue
		}
		if !found || snap.CreatedAt.After(latest.CreatedAt) ||
			(snap.CreatedAt.Equal(latest.CreatedAt) && snap.ID > latest.ID) {
			latest, found = snap, true
		}
	}
	if !found {
		return nil, ports.ErrNotFound
	}
	return placementplan.Restore(latest)
}

// List returns the seller's plans (of one item when set), newest first,
// keyset-paged after (afterCreatedAt, afterPlanID).
func (r *PlacementPlanRepository) List(ctx context.Context, sellerID, catalogItemID string, afterCreatedAt time.Time, afterPlanID string, limit int) ([]*placementplan.Plan, error) {
	defer r.s.enter(ctx)()
	var snaps []placementplan.Snapshot
	for _, snap := range r.s.placementPlans {
		if snap.SellerID != sellerID {
			continue
		}
		if catalogItemID != "" && snap.CatalogItemID != catalogItemID {
			continue
		}
		snaps = append(snaps, snap)
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].CreatedAt.Equal(snaps[j].CreatedAt) {
			return snaps[i].CreatedAt.After(snaps[j].CreatedAt) // newest first
		}
		return snaps[i].ID > snaps[j].ID
	})
	if !afterCreatedAt.IsZero() {
		kept := snaps[:0]
		for _, snap := range snaps {
			if snap.CreatedAt.Before(afterCreatedAt) || (snap.CreatedAt.Equal(afterCreatedAt) && string(snap.ID) < afterPlanID) {
				kept = append(kept, snap)
			}
		}
		snaps = kept
	}
	if len(snaps) > limit {
		snaps = snaps[:limit]
	}
	out := make([]*placementplan.Plan, 0, len(snaps))
	for _, snap := range snaps {
		p, err := placementplan.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
