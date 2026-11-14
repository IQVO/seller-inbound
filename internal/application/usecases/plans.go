package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func planView(p *inboundplan.Plan) views.Plan {
	sn := p.Snapshot()
	lines := make([]views.PlanLine, len(sn.Lines))
	for i, l := range sn.Lines {
		lines[i] = views.PlanLine{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches, UnitVolumeDm3: l.UnitVolumeDm3}
	}
	redistribution := make([]views.RedistributionLine, len(sn.Redistribution))
	for i, r := range sn.Redistribution {
		redistribution[i] = views.RedistributionLine{NodeID: r.NodeID, LineNo: r.LineNo, QtyEaches: r.QtyEaches}
	}
	var feeBasis *views.FeeBasis
	if sn.PlacementFeeBasis != nil {
		feeBasis = &views.FeeBasis{Basis: sn.PlacementFeeBasis.Basis, Month: sn.PlacementFeeBasis.Month,
			Dm3: sn.PlacementFeeBasis.Dm3, NodeIDs: sn.PlacementFeeBasis.NodeIDs}
	}
	return views.Plan{PlanID: string(sn.ID), SellerID: sn.SellerID, DestinationNodeID: sn.DestinationID, PackingOption: string(sn.Packing),
		PlacementOption: string(sn.PlacementOption), Lines: lines, Redistribution: redistribution, PlacementFeeBasis: feeBasis,
		CapacityEnforced: sn.CapacityEnforced,
		State:            string(sn.State), CreatedAt: sn.CreatedAt, ConfirmedAt: sn.ConfirmedAt, CancelledAt: sn.CancelledAt}
}

// requireNode reads the destination node from the local view and refuses a node
// that cannot receive. A node the view has never seen cannot receive either.
func (s *Service) requireNode(ctx context.Context, nodeID string) (network.Node, error) {
	n, err := s.d.Nodes.Get(ctx, nodeID)
	if err != nil {
		if isNotFound(err) {
			return network.Node{}, network.ErrNodeCannotReceive
		}
		return network.Node{}, err
	}
	return n, n.CanReceive()
}

func isNotFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }

// CreateInboundPlan creates a DRAFT plan for an ACTIVE seller and a node that can receive.
// Under ADR 0015 the plan's declared volume is checked against the node's
// tier-factored monthly limit (record-and-allow) and reserved; a NETWORK_SPLIT
// plan also records its capacity-split-v1 redistribution and fee basis.
func (s *Service) CreateInboundPlan(ctx context.Context, in requests.CreateInboundPlan) (views.Plan, error) {
	var out views.Plan
	err := s.atomically(ctx, func(ctx context.Context) error {
		if err := s.requireActiveSeller(ctx, in.SellerID); err != nil {
			return err
		}
		node, err := s.requireNode(ctx, in.DestinationNodeID)
		if err != nil {
			return err
		}
		now := s.d.Clock.Now()
		month := shared.MonthOf(now)
		lines := make([]inboundplan.Line, len(in.Lines))
		for i, l := range in.Lines {
			lines[i] = inboundplan.Line{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches, UnitVolumeDm3: l.UnitVolumeDm3}
		}
		facts, err := s.planFacts(ctx, in.SellerID, node, lines, inboundplan.PlacementOption(in.PlacementOption), month, now)
		if err != nil {
			return err
		}
		p, err := inboundplan.Create(inboundplan.NewPlan{
			ID: inboundplan.ID(s.d.IDs.NewID("pln")), SellerID: in.SellerID, DestinationID: in.DestinationNodeID,
			Packing: inboundplan.PackingOption(in.PackingOption), Lines: lines,
			PlacementOption: facts.placementOption, Redistribution: facts.redistribution,
			SplitLimitKnown: facts.splitLimitKnown, PlacementFeeBasis: facts.placementFeeBasis,
			CapacityEnforced: facts.capacityEnforced,
		}, now)
		if err != nil {
			return err
		}
		if err := s.d.Plans.Save(ctx, p); err != nil {
			return err
		}
		if err := s.d.Capacity.Reserve(ctx, in.SellerID, month, in.DestinationNodeID, p.VolumeDm3(), now); err != nil {
			return err
		}
		out = planView(p)
		return nil
	})
	return out, err
}

// planFacts computes the ADR 0015 facts of a plan about to be created: the
// capacity gate's enforcement and, for NETWORK_SPLIT, the capacity-split-v1
// redistribution and the fee basis. An empty option defaults to SELLER_SPLIT.
func (s *Service) planFacts(ctx context.Context, sellerID string, node network.Node, lines []inboundplan.Line, option inboundplan.PlacementOption, month string, now time.Time) (planFacts, error) {
	if option == "" {
		option = inboundplan.SellerSplit
	}
	facts := planFacts{placementOption: option}
	var err error
	if facts.capacityEnforced, err = s.checkCapacity(ctx, sellerID, node, volumeOf(lines), month, now); err != nil {
		return planFacts{}, err
	}
	if option != inboundplan.NetworkSplit {
		return facts, nil
	}
	facts.redistribution, facts.splitLimitKnown, facts.placementFeeBasis, err =
		s.networkSplitFacts(ctx, sellerID, node, lines, month, now)
	if err != nil {
		return planFacts{}, err
	}
	return facts, nil
}

// volumeOf is Σ qty x unit over the lines, 0 when any unit volume is
// undeclared (it equals Plan.VolumeDm3 of the plan about to be built).
func volumeOf(lines []inboundplan.Line) int64 {
	var total int64
	for _, l := range lines {
		if l.UnitVolumeDm3 <= 0 {
			return 0
		}
		total += int64(l.QtyEaches) * int64(l.UnitVolumeDm3)
	}
	return total
}

// ConfirmInboundPlan confirms a DRAFT plan and publishes InboundPlanConfirmed.
// The seller must still be ACTIVE and the node able to receive.
func (s *Service) ConfirmInboundPlan(ctx context.Context, planID string) (views.Plan, error) {
	var out views.Plan
	err := s.atomically(ctx, func(ctx context.Context) error {
		p, err := s.d.Plans.Get(ctx, inboundplan.ID(planID))
		if err != nil {
			return err
		}
		if err := s.requireActiveSeller(ctx, p.SellerID()); err != nil {
			return err
		}
		if _, err := s.requireNode(ctx, p.DestinationNodeID()); err != nil {
			return err
		}
		evs, err := p.Confirm(s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.d.Plans.Save(ctx, p); err != nil {
			return err
		}
		if err := s.d.Outbox.Append(ctx, evs...); err != nil {
			return err
		}
		out = planView(p)
		return nil
	})
	return out, err
}

// CancelInboundPlan cancels a DRAFT plan and releases the capacity it held
// (ADR 0015: the reservation of the plan's creation month at its destination).
func (s *Service) CancelInboundPlan(ctx context.Context, planID string) (views.Plan, error) {
	var out views.Plan
	err := s.atomically(ctx, func(ctx context.Context) error {
		p, err := s.d.Plans.Get(ctx, inboundplan.ID(planID))
		if err != nil {
			return err
		}
		if err := p.Cancel(s.d.Clock.Now()); err != nil {
			return err
		}
		if err := s.d.Plans.Save(ctx, p); err != nil {
			return err
		}
		sn := p.Snapshot()
		if err := s.d.Capacity.Release(ctx, sn.SellerID, shared.MonthOf(sn.CreatedAt), sn.DestinationID, p.VolumeDm3()); err != nil {
			return err
		}
		out = planView(p)
		return nil
	})
	return out, err
}

// GetInboundPlan returns the plan.
func (s *Service) GetInboundPlan(ctx context.Context, planID string) (views.Plan, error) {
	p, err := s.d.Plans.Get(ctx, inboundplan.ID(planID))
	if err != nil {
		return views.Plan{}, err
	}
	return planView(p), nil
}
