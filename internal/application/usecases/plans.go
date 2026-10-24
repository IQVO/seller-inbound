package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/network"
)

func planView(p *inboundplan.Plan) views.Plan {
	sn := p.Snapshot()
	lines := make([]views.PlanLine, len(sn.Lines))
	for i, l := range sn.Lines {
		lines[i] = views.PlanLine{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches}
	}
	return views.Plan{PlanID: string(sn.ID), SellerID: sn.SellerID, DestinationNodeID: sn.DestinationID, PackingOption: string(sn.Packing),
		State: string(sn.State), Lines: lines, CreatedAt: sn.CreatedAt, ConfirmedAt: sn.ConfirmedAt, CancelledAt: sn.CancelledAt}
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
func (s *Service) CreateInboundPlan(ctx context.Context, in requests.CreateInboundPlan) (views.Plan, error) {
	var out views.Plan
	err := s.atomically(ctx, func(ctx context.Context) error {
		if err := s.requireActiveSeller(ctx, in.SellerID); err != nil {
			return err
		}
		if _, err := s.requireNode(ctx, in.DestinationNodeID); err != nil {
			return err
		}
		lines := make([]inboundplan.Line, len(in.Lines))
		for i, l := range in.Lines {
			lines[i] = inboundplan.Line{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches}
		}
		p, err := inboundplan.Create(inboundplan.NewPlan{
			ID: inboundplan.ID(s.d.IDs.NewID("pln")), SellerID: in.SellerID, DestinationID: in.DestinationNodeID,
			Packing: inboundplan.PackingOption(in.PackingOption), Lines: lines,
		}, s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.d.Plans.Save(ctx, p); err != nil {
			return err
		}
		out = planView(p)
		return nil
	})
	return out, err
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

// CancelInboundPlan cancels a DRAFT plan.
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
