package usecases

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/placementplan"
)

// DefaultCoverDays is the cover horizon of the compute command when the
// client omits it (ADR 0012).
const DefaultCoverDays = 14

func placementView(p *placementplan.Plan) views.PlacementPlan {
	sn := p.Snapshot()
	suggestions := make([]views.PlacementSuggestion, len(sn.Suggestions))
	for i, s := range sn.Suggestions {
		suggestions[i] = views.PlacementSuggestion{NodeID: s.NodeID, RegionID: s.RegionID, SuggestedUnits: s.SuggestedUnits,
			RegionDemandUnits: s.Evidence.RegionDemandUnits, NodeAvailabilityUnits: s.Evidence.NodeAvailabilityUnits,
			CoverDays: s.Evidence.CoverDays}
	}
	return views.PlacementPlan{PlanID: string(sn.ID), SellerID: sn.SellerID, CatalogItemID: sn.CatalogItemID,
		CoverDays: sn.CoverDays, WindowDays: sn.WindowDays, Basis: sn.Basis, State: string(sn.State),
		Suggestions: suggestions, CreatedAt: sn.CreatedAt, AcceptedAt: sn.AcceptedAt, SupersededAt: sn.SupersededAt}
}

// candidate is one placement target with the availability the formula splits by.
type candidate struct {
	node         network.Node
	availability int
}

// ceilDiv returns ceil(a/b) for positive a and b.
func ceilDiv(a, b int) int { return (a + b - 1) / b }

// splitTarget divides target among the candidates proportional to their
// availability (an even split when every availability is zero), largest
// remainder first, ties by node id, so the shares always sum to target
// exactly and the same inputs always produce the same split (ADR 0012).
func splitTarget(target int, candidates []candidate) []int {
	shares := make([]int, len(candidates))
	total := 0
	for _, c := range candidates {
		total += c.availability
	}
	if total == 0 {
		base, rem := target/len(candidates), target%len(candidates)
		for i := range shares {
			shares[i] = base
		}
		for i := 0; i < rem; i++ {
			shares[i]++ // candidates are ordered by node id: the remainder is deterministic
		}
		return shares
	}
	given := 0
	for i, c := range candidates {
		shares[i] = target * c.availability / total
		given += shares[i]
	}
	// Largest remainder: the leftover units go to the biggest fractional share
	// first, ties broken by the earlier candidate (node id order).
	left := target - given
	type frac struct{ idx, rest int }
	rests := make([]frac, 0, len(candidates))
	for i, c := range candidates {
		rests = append(rests, frac{i, target * c.availability % total})
	}
	sort.SliceStable(rests, func(a, b int) bool {
		if rests[a].rest != rests[b].rest {
			return rests[a].rest > rests[b].rest
		}
		return rests[a].idx < rests[b].idx
	})
	for i := 0; i < left && i < len(rests); i++ {
		shares[rests[i].idx]++
	}
	return shares
}

// buildSuggestions is THE formula of ADR 0012, on the facts the three read
// models answered. For every region with demand in the observation window:
//
//	target = max(0, ceil(demandUnits * coverDays / windowDays) - regionAvailability)
//
// split across the region's ACTIVE vendor FULFILLMENT_CENTER nodes
// proportional to their declared availability (even split when all zero).
// A region whose availability already covers the target, a region with no
// candidate node, and an item with no demand anywhere produce no suggestion;
// an all-round empty result is a legal plan.
func buildSuggestions(heat []network.RegionHeat, nodes []network.Node, availability []network.NodeAvailability, coverDays int) []placementplan.Suggestion {
	windowDays := int(network.ObservationWindow / (24 * time.Hour))
	declared := make(map[string]int, len(availability))
	for _, a := range availability {
		declared[a.NodeID] = a.AvailableQty
	}
	byRegion := make(map[string][]candidate)
	for _, n := range nodes {
		if n.IsPlacementCandidate() && n.RegionID != "" {
			byRegion[n.RegionID] = append(byRegion[n.RegionID], candidate{node: n, availability: declared[n.ID]})
		}
	}
	var out []placementplan.Suggestion
	for _, r := range heat { // heat is ordered by region id
		out = append(out, regionSuggestions(r, byRegion[r.RegionID], coverDays, windowDays)...)
	}
	return out
}

// regionSuggestions applies the formula to one region.
func regionSuggestions(r network.RegionHeat, cs []candidate, coverDays, windowDays int) []placementplan.Suggestion {
	if r.DemandUnits <= 0 || len(cs) == 0 {
		return nil // no demand to place, or a region the advisory cannot place (no vendor FC): residual of ADR 0012
	}
	sort.Slice(cs, func(a, b int) bool { return cs[a].node.ID < cs[b].node.ID })
	regionAvailability := 0
	for _, c := range cs {
		regionAvailability += c.availability
	}
	target := ceilDiv(r.DemandUnits*coverDays, windowDays) - regionAvailability
	if target <= 0 {
		return nil // the region's declared stock already covers the horizon
	}
	var out []placementplan.Suggestion
	for i, share := range splitTarget(target, cs) {
		if share <= 0 {
			continue // a zero share is not a suggestion
		}
		out = append(out, placementplan.Suggestion{
			NodeID: cs[i].node.ID, RegionID: r.RegionID, SuggestedUnits: share,
			Evidence: placementplan.Evidence{RegionDemandUnits: r.DemandUnits,
				NodeAvailabilityUnits: cs[i].availability, CoverDays: coverDays},
		})
	}
	return out
}

// placementFacts reads the three read models the formula needs, in one
// transaction.
type placementFacts struct {
	heat         []network.RegionHeat
	nodes        []network.Node
	availability []network.NodeAvailability
}

func (s *Service) readPlacementFacts(ctx context.Context, catalogItemID string, now time.Time) (placementFacts, error) {
	var f placementFacts
	var err error
	if f.heat, err = s.d.DemandHeat.RegionHeat(ctx, catalogItemID, now.Add(-network.ObservationWindow)); err != nil {
		return placementFacts{}, err
	}
	if f.nodes, err = s.d.Nodes.VendorFulfillmentCenters(ctx); err != nil {
		return placementFacts{}, err
	}
	if f.availability, err = s.d.Listings.Availability(ctx, catalogItemID); err != nil {
		return placementFacts{}, err
	}
	return f, nil
}

// supersedePrevious moves the seller's previous plan of the item to
// SUPERSEDED (a newer plan is being computed), saving it when one exists.
func (s *Service) supersedePrevious(ctx context.Context, sellerID, catalogItemID string, now time.Time) error {
	previous, err := s.d.PlacementPlans.LatestOf(ctx, sellerID, catalogItemID)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := previous.Supersede(now); err != nil {
		return err
	}
	return s.d.PlacementPlans.Save(ctx, previous)
}

// ComputePlacementPlan is the compute command of ADR 0012: it reads the
// demand view, builds the suggestions deterministically, supersedes the
// seller's previous plan of the item and records the new one with its
// PlacementPlanSuggested event. An item with no demand yields an EMPTY plan,
// still recorded and still published.
func (s *Service) ComputePlacementPlan(ctx context.Context, in requests.ComputePlacementPlan) (views.PlacementPlan, error) {
	if in.CoverDays == 0 {
		in.CoverDays = DefaultCoverDays
	}
	if in.CoverDays < placementplan.MinCoverDays || in.CoverDays > placementplan.MaxCoverDays {
		return views.PlacementPlan{}, ports.ErrInvalidCoverDays
	}
	var out views.PlacementPlan
	err := s.atomically(ctx, func(ctx context.Context) error {
		if err := s.requireActiveSeller(ctx, in.SellerID); err != nil {
			return err
		}
		now := s.d.Clock.Now()
		facts, err := s.readPlacementFacts(ctx, in.CatalogItemID, now)
		if err != nil {
			return err
		}
		if err := s.supersedePrevious(ctx, in.SellerID, in.CatalogItemID, now); err != nil {
			return err
		}
		windowDays := int(network.ObservationWindow / (24 * time.Hour))
		p, evs, err := placementplan.Compute(placementplan.NewPlan{
			ID: placementplan.ID(s.d.IDs.NewID("plp")), SellerID: in.SellerID, CatalogItemID: in.CatalogItemID,
			CoverDays: in.CoverDays, WindowDays: windowDays,
			Suggestions: buildSuggestions(facts.heat, facts.nodes, facts.availability, in.CoverDays),
		}, now)
		if err != nil {
			return err
		}
		if err := s.d.PlacementPlans.Save(ctx, p); err != nil {
			return err
		}
		if err := s.d.Outbox.Append(ctx, evs...); err != nil {
			return err
		}
		out = placementView(p)
		return nil
	})
	return out, err
}

// AcceptPlacementPlan records the seller's acceptance of a SUGGESTED plan.
// Advisory-only in v1: no event, no downstream effect (ADR 0012).
func (s *Service) AcceptPlacementPlan(ctx context.Context, planID string) (views.PlacementPlan, error) {
	var out views.PlacementPlan
	err := s.atomically(ctx, func(ctx context.Context) error {
		p, err := s.d.PlacementPlans.Get(ctx, placementplan.ID(planID))
		if err != nil {
			return err
		}
		if err := p.Accept(s.d.Clock.Now()); err != nil {
			return err
		}
		if err := s.d.PlacementPlans.Save(ctx, p); err != nil {
			return err
		}
		out = placementView(p)
		return nil
	})
	return out, err
}

// GetPlacementPlan returns the plan.
func (s *Service) GetPlacementPlan(ctx context.Context, planID string) (views.PlacementPlan, error) {
	p, err := s.d.PlacementPlans.Get(ctx, placementplan.ID(planID))
	if err != nil {
		return views.PlacementPlan{}, err
	}
	return placementView(p), nil
}

// ListPlacementPlans answers the keyset-paged plans of one seller (every
// item when catalogItemID is empty), newest first.
func (s *Service) ListPlacementPlans(ctx context.Context, sellerID, catalogItemID string, afterCreatedAt time.Time, afterPlanID string, limit int) ([]views.PlacementPlan, error) {
	plans, err := s.d.PlacementPlans.List(ctx, sellerID, catalogItemID, afterCreatedAt, afterPlanID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]views.PlacementPlan, len(plans))
	for i, p := range plans {
		out[i] = placementView(p)
	}
	return out, nil
}
