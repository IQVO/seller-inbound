package usecases

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/capacity"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// planFacts are the ADR 0015 facts computed at plan create: the gate's
// enforcement, and the NETWORK_SPLIT redistribution and fee basis.
type planFacts struct {
	placementOption   inboundplan.PlacementOption
	redistribution    []inboundplan.RedistributionLine
	splitLimitKnown   bool
	placementFeeBasis *inboundplan.FeeBasis
	capacityEnforced  bool
}

// tierFactorBps answers the seller's allocation factor from THIS context's own
// scorecard (ADR 0005 read path): a PROVISIONAL scorecard -- no observations
// in the default window -- derives GOOD and so pays the GOOD factor.
func (s *Service) tierFactorBps(ctx context.Context, sellerID string, now time.Time) (int, error) {
	counts, err := s.d.Scorecard.Counts(ctx, sellerID, scorecard.DefaultWindow.Since(now), now)
	if err != nil {
		return 0, err
	}
	return capacity.TierFactorBps(scorecard.Compute(scorecard.DefaultWindow, now, counts).Tier), nil
}

// checkCapacity is the ADR 0015 gate, applied in CreateInboundPlan after
// CanReceive: limit = the node's declared monthly dm3 x the seller's tier
// factor; the plan is refused only when BOTH sides are known and the
// reservation would exceed the limit (record-and-allow otherwise).
func (s *Service) checkCapacity(ctx context.Context, sellerID string, node network.Node, volumeDm3 int64, month string, now time.Time) (bool, error) {
	monthly := 0
	if node.Limits != nil {
		monthly = node.Limits.MonthlyVolumeDm3
	}
	factorBps, err := s.tierFactorBps(ctx, sellerID, now)
	if err != nil {
		return false, err
	}
	limit := capacity.LimitDm3(monthly, factorBps)
	reserved, err := s.d.Capacity.Reserved(ctx, sellerID, month, node.ID)
	if err != nil {
		return false, err
	}
	if err := capacity.Check(limit, reserved, volumeDm3); err != nil {
		return false, err
	}
	return capacity.Enforced(limit, volumeDm3), nil
}

// networkSplitFacts computes the capacity-split-v1 redistribution and the fee
// basis of a NETWORK_SPLIT plan (ADR 0015). The destination must be an ACTIVE
// INBOUND_CROSS_DOCK and its region must hold an ACTIVE FULFILLMENT_CENTER
// the view has seen; the redistribution is a recorded fact only.
func (s *Service) networkSplitFacts(ctx context.Context, sellerID string, node network.Node, lines []inboundplan.Line, month string, now time.Time) ([]inboundplan.RedistributionLine, bool, *inboundplan.FeeBasis, error) {
	if node.Type != network.TypeInboundCrossDock {
		return nil, false, nil, fmt.Errorf("%w: node %s is a %s", capacity.ErrNetworkSplitDestination, node.ID, node.Type)
	}
	factorBps, err := s.tierFactorBps(ctx, sellerID, now)
	if err != nil {
		return nil, false, nil, err
	}
	fcs, err := s.d.Nodes.RegionFulfillmentCenters(ctx, node.RegionID)
	if err != nil {
		return nil, false, nil, err
	}
	if len(fcs) == 0 {
		return nil, false, nil, fmt.Errorf("%w: region %q of node %s", capacity.ErrNetworkSplitUnavailable, node.RegionID, node.ID)
	}
	splitLines := make([]capacity.SplitLine, len(lines))
	for i, l := range lines {
		splitLines[i] = capacity.SplitLine{LineNo: i + 1, QtyEaches: l.QtyEaches}
	}
	candidates := make([]capacity.Candidate, len(fcs))
	for i, fc := range fcs {
		limitDm3 := 0
		if fc.Limits != nil {
			limitDm3 = fc.Limits.MonthlyVolumeDm3
		}
		candidates[i] = capacity.Candidate{NodeID: fc.ID, LimitDm3: limitDm3}
	}
	reservedByNode := make(map[string]int64, len(fcs))
	for _, c := range candidates {
		reserved, err := s.d.Capacity.Reserved(ctx, sellerID, month, c.NodeID)
		if err != nil {
			return nil, false, nil, err
		}
		reservedByNode[c.NodeID] = reserved
	}
	targets, limitKnown := capacity.Split(splitLines, candidates, factorBps, reservedByNode)
	redistribution := make([]inboundplan.RedistributionLine, len(targets))
	seen := map[string]bool{}
	var nodeIDs []string
	for i, t := range targets {
		redistribution[i] = inboundplan.RedistributionLine{NodeID: t.NodeID, LineNo: t.LineNo, QtyEaches: t.QtyEaches}
		if !seen[t.NodeID] {
			seen[t.NodeID] = true
			nodeIDs = append(nodeIDs, t.NodeID)
		}
	}
	var volume int64
	for _, l := range lines {
		if l.UnitVolumeDm3 > 0 {
			volume += int64(l.QtyEaches) * int64(l.UnitVolumeDm3)
		} else {
			volume = 0
			break
		}
	}
	sort.Strings(nodeIDs) // the aggregate requires the fee basis node ids sorted
	feeBasis := &inboundplan.FeeBasis{Basis: string(inboundplan.NetworkSplit), Month: month, Dm3: volume, NodeIDs: nodeIDs}
	return redistribution, limitKnown, feeBasis, nil
}

// GetSellerCapacityAllocations answers a seller's inbound capacity
// reservations of one calendar month (ADR 0015; month defaults to the current
// month). An unknown seller is ports.ErrNotFound; an invalid month is a
// validation error.
func (s *Service) GetSellerCapacityAllocations(ctx context.Context, sellerID, month string) (views.CapacityAllocations, error) {
	if month == "" {
		month = shared.MonthOf(s.d.Clock.Now())
	}
	if !shared.ValidMonth(month) {
		return views.CapacityAllocations{}, shared.Invalid("month must be a YYYY-MM key")
	}
	if _, err := s.d.Sellers.Get(ctx, seller.ID(sellerID)); err != nil {
		return views.CapacityAllocations{}, err
	}
	rows, err := s.d.Capacity.Allocations(ctx, sellerID, month)
	if err != nil {
		return views.CapacityAllocations{}, err
	}
	items := make([]views.CapacityAllocation, len(rows))
	for i, r := range rows {
		items[i] = views.CapacityAllocation{NodeID: r.NodeID, ReservedDm3: r.ReservedDm3, UpdatedAt: r.UpdatedAt}
	}
	return views.CapacityAllocations{SellerID: sellerID, Month: month, Items: items}, nil
}
