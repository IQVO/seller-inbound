package usecases

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
)

func cartonView(c inboundshipment.Carton) views.Carton {
	return views.Carton{SSCC: c.SSCC, ParentSSCC: c.ParentSSCC, Kind: string(c.Kind), Contents: contentViews(c.Contents),
		GrossWeightG: c.GrossWeightG, DimsL: c.Dims.L, DimsW: c.Dims.W, DimsH: c.Dims.H, Sealed: c.Sealed, ReceiptStatus: string(c.Receipt)}
}

func contentViews(in []inboundshipment.Content) []views.Content {
	out := make([]views.Content, len(in))
	for i, ct := range in {
		out[i] = views.Content{CatalogItemID: ct.CatalogItemID, GTIN: ct.GTIN, QtyEaches: ct.QtyEaches, Lot: ct.Lot, Expiry: ct.Expiry}
	}
	return out
}

func shipmentView(s *inboundshipment.Shipment) views.Shipment {
	sn := s.Snapshot()
	cartons := make([]views.Carton, len(sn.Cartons))
	for i, c := range sn.Cartons {
		cartons[i] = cartonView(c)
	}
	return views.Shipment{ShipmentID: string(sn.ID), PlanID: sn.PlanID, SellerID: sn.SellerID, NodeID: sn.NodeID, State: string(sn.State),
		Cartons: cartons, CreatedAt: sn.CreatedAt, LabelledAt: sn.LabelledAt, ShippedAt: sn.ShippedAt, ReceivedAt: sn.ReceivedAt}
}

// mapSave turns the repository's SSCC conflict into the domain error.
func mapSave(err error) error {
	if errors.Is(err, ports.ErrSsccTaken) {
		return fmt.Errorf("%w: the sscc is registered to another shipment", inboundshipment.ErrSsccNotUnique)
	}
	return err
}

// CreateInboundShipment creates an empty DRAFT shipment for a CONFIRMED plan.
func (s *Service) CreateInboundShipment(ctx context.Context, planID string) (views.Shipment, error) {
	var out views.Shipment
	err := s.atomically(ctx, func(ctx context.Context) error {
		p, err := s.d.Plans.Get(ctx, inboundplan.ID(planID))
		if err != nil {
			return err
		}
		if err := s.requireActiveSeller(ctx, p.SellerID()); err != nil {
			return err
		}
		if err := p.EnsureConfirmed(); err != nil {
			return err
		}
		if _, err := s.requireNode(ctx, p.DestinationNodeID()); err != nil {
			return err
		}
		sh, err := inboundshipment.Create(inboundshipment.NewShipment{
			ID: inboundshipment.ID(s.d.IDs.NewID("shp")), PlanID: string(p.ID()), SellerID: p.SellerID(), NodeID: p.DestinationNodeID(),
		}, s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.d.Shipments.Save(ctx, sh); err != nil {
			return err
		}
		out = shipmentView(sh)
		return nil
	})
	return out, err
}

// shipmentContext is what a carton or label command needs next to the shipment.
type shipmentContext struct {
	sh    *inboundshipment.Shipment
	rules inboundshipment.PlanRules
}

// loadShipment loads the shipment and its plan, checks the seller is ACTIVE and
// builds the plan rules from the plan and the plan's other shipments.
func (s *Service) loadShipment(ctx context.Context, shipmentID string) (shipmentContext, error) {
	sh, err := s.d.Shipments.Get(ctx, inboundshipment.ID(shipmentID))
	if err != nil {
		return shipmentContext{}, err
	}
	if err := s.requireActiveSeller(ctx, sh.SellerID()); err != nil {
		return shipmentContext{}, err
	}
	p, err := s.d.Plans.Get(ctx, inboundplan.ID(sh.PlanID()))
	if err != nil {
		return shipmentContext{}, err
	}
	all, err := s.d.Shipments.ListByPlan(ctx, p.ID())
	if err != nil {
		return shipmentContext{}, err
	}
	other := map[string]int{}
	for _, o := range all {
		if o.ID() == sh.ID() {
			continue
		}
		for item, qty := range o.QtyByItem() {
			other[item] += qty
		}
	}
	return shipmentContext{sh: sh, rules: inboundshipment.PlanRules{
		Packing: p.Packing(), PlanQty: p.PlannedByItem(), OtherQty: other, SingleShipment: len(all) == 1,
	}}, nil
}

func limitsOf(n network.Node) inboundshipment.Limits {
	if n.Limits == nil {
		return inboundshipment.Limits{}
	}
	out := inboundshipment.Limits{MaxCartonWeightG: n.Limits.MaxCartonWeightG, AcceptsPallets: n.Limits.AcceptsPallets}
	if d := n.Limits.MaxCartonDims; d != nil {
		out.MaxCartonDims = &inboundshipment.Dims{L: d.L, W: d.W, H: d.H}
	}
	return out
}

// caseUnits preloads the packaging view rows of the items a carton names and
// returns the lookup the shipment aggregate needs.
func (s *Service) caseUnits(ctx context.Context, contents []requests.Content) (func(string, string) (int, bool), error) {
	rows := map[string][]network.PackLevel{}
	for _, ct := range contents {
		if _, done := rows[ct.CatalogItemID]; done {
			continue
		}
		r, err := s.d.Packaging.ListByItem(ctx, ct.CatalogItemID)
		if err != nil {
			return nil, err
		}
		rows[ct.CatalogItemID] = r
	}
	return func(item, gtin string) (int, bool) { return network.ActiveCaseUnits(rows[item], gtin) }, nil
}

// AddCarton adds a carton to a DRAFT shipment. A carton without an SSCC gets
// one issued from the network's own prefix; a supplied one is accepted only if
// valid and unique network-wide (policy SsccIssuance).
func (s *Service) AddCarton(ctx context.Context, in requests.AddCarton) (views.AddedCarton, error) {
	var out views.AddedCarton
	err := s.atomically(ctx, func(ctx context.Context) error {
		sc, err := s.loadShipment(ctx, in.ShipmentID)
		if err != nil {
			return err
		}
		node, err := s.requireNode(ctx, sc.sh.NodeID())
		if err != nil {
			return err
		}
		units, err := s.caseUnits(ctx, in.Contents)
		if err != nil {
			return err
		}
		rules := inboundshipment.CartonRules{PlanRules: sc.rules, Limits: limitsOf(node), CaseUnits: units}
		carton, issued, err := s.addWithSSCC(ctx, sc.sh, in, rules)
		if err != nil {
			return err
		}
		out = views.AddedCarton{ShipmentID: in.ShipmentID, Carton: cartonView(carton), Issued: issued,
			Label: labelView(carton, sc.sh.NodeID())}
		return nil
	})
	return out, err
}

// addWithSSCC adds the carton and saves the shipment. An issued SSCC that
// collides with one a seller supplied is re-issued; a supplied one that collides
// is the seller's error.
func (s *Service) addWithSSCC(ctx context.Context, sh *inboundshipment.Shipment, in requests.AddCarton, rules inboundshipment.CartonRules) (inboundshipment.Carton, bool, error) {
	issued := in.SSCC == ""
	attempts := 1
	if issued {
		attempts = s.cfg.SsccIssueAttempts
	}
	var lastErr error
	for range attempts {
		sscc := in.SSCC
		if issued {
			var err error
			if sscc, err = s.d.SSCC.Next(ctx); err != nil {
				return inboundshipment.Carton{}, false, err
			}
		}
		before := sh.Snapshot()
		carton, err := sh.AddCarton(newCarton(in, sscc), rules)
		if err != nil {
			return inboundshipment.Carton{}, false, err
		}
		// A refused SSCC leaves the transaction usable: the repository checks the
		// registry before it writes anything.
		err = s.d.Shipments.Save(ctx, sh)
		if err == nil {
			return carton, issued, nil
		}
		restored, rerr := inboundshipment.Restore(before)
		if rerr != nil {
			return inboundshipment.Carton{}, false, rerr
		}
		*sh = *restored
		lastErr = mapSave(err)
		if !errors.Is(lastErr, inboundshipment.ErrSsccNotUnique) {
			return inboundshipment.Carton{}, false, lastErr
		}
	}
	return inboundshipment.Carton{}, false, lastErr
}

func newCarton(in requests.AddCarton, sscc string) inboundshipment.NewCarton {
	contents := make([]inboundshipment.Content, len(in.Contents))
	for i, ct := range in.Contents {
		contents[i] = inboundshipment.Content{CatalogItemID: ct.CatalogItemID, GTIN: ct.GTIN, QtyEaches: ct.QtyEaches, Lot: ct.Lot, Expiry: ct.Expiry}
	}
	return inboundshipment.NewCarton{SSCC: sscc, ParentSSCC: in.ParentSSCC, Kind: inboundshipment.Kind(in.Kind), Contents: contents,
		GrossWeightG: in.GrossWeightG, Dims: inboundshipment.Dims{L: in.DimsL, W: in.DimsW, H: in.DimsH}}
}

// LabelShipment moves a DRAFT shipment to LABELLED and publishes InboundShipmentLabelled.
func (s *Service) LabelShipment(ctx context.Context, shipmentID string) (views.Shipment, error) {
	var out views.Shipment
	err := s.atomically(ctx, func(ctx context.Context) error {
		sc, err := s.loadShipment(ctx, shipmentID)
		if err != nil {
			return err
		}
		if _, err := s.requireNode(ctx, sc.sh.NodeID()); err != nil {
			return err
		}
		evs, err := sc.sh.Label(sc.rules, s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.d.Shipments.Save(ctx, sc.sh); err != nil {
			return mapSave(err)
		}
		if err := s.d.Outbox.Append(ctx, evs...); err != nil {
			return err
		}
		out = shipmentView(sc.sh)
		return nil
	})
	return out, err
}

// ShipInboundShipment moves a LABELLED shipment to SHIPPED and publishes
// InboundShipmentShipped. The seller must be ACTIVE and the node able to receive.
func (s *Service) ShipInboundShipment(ctx context.Context, shipmentID string) (views.Shipment, error) {
	var out views.Shipment
	err := s.atomically(ctx, func(ctx context.Context) error {
		sh, err := s.d.Shipments.Get(ctx, inboundshipment.ID(shipmentID))
		if err != nil {
			return err
		}
		if err := s.requireActiveSeller(ctx, sh.SellerID()); err != nil {
			return err
		}
		if _, err := s.requireNode(ctx, sh.NodeID()); err != nil {
			return err
		}
		evs, err := sh.Ship(s.d.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.d.Shipments.Save(ctx, sh); err != nil {
			return mapSave(err)
		}
		if err := s.d.Outbox.Append(ctx, evs...); err != nil {
			return err
		}
		out = shipmentView(sh)
		return nil
	})
	return out, err
}

// GetInboundShipment returns the shipment with its cartons.
func (s *Service) GetInboundShipment(ctx context.Context, shipmentID string) (views.Shipment, error) {
	sh, err := s.d.Shipments.Get(ctx, inboundshipment.ID(shipmentID))
	if err != nil {
		return views.Shipment{}, err
	}
	return shipmentView(sh), nil
}

// GetLabels returns the label data of every carton of the shipment.
func (s *Service) GetLabels(ctx context.Context, shipmentID string) (views.Labels, error) {
	sh, err := s.d.Shipments.Get(ctx, inboundshipment.ID(shipmentID))
	if err != nil {
		return views.Labels{}, err
	}
	cartons := sh.Cartons()
	labels := make([]views.Label, len(cartons))
	for i, c := range cartons {
		labels[i] = labelView(c, sh.NodeID())
	}
	return views.Labels{ShipmentID: string(sh.ID()), State: string(sh.State()), DestinationNodeID: sh.NodeID(), Labels: labels}, nil
}

// labelView renders the label data of a carton: the GS1-128 element string for
// AI 00, the human readable line, and the contents summary in eaches.
func labelView(c inboundshipment.Carton, nodeID string) views.Label {
	parts := make([]string, len(c.Contents))
	for i, ct := range c.Contents {
		parts[i] = fmt.Sprintf("%s x%d", ct.CatalogItemID, ct.QtyEaches)
	}
	return views.Label{
		LabelRef: "LBL-" + c.SSCC, SSCC: c.SSCC, GS1128: "(00)" + c.SSCC, HumanReadable: humanSSCC(c.SSCC),
		ParentSSCC: c.ParentSSCC, Kind: string(c.Kind), Contents: contentViews(c.Contents),
		ContentSummary: fmt.Sprintf("%d eaches: %s", c.TotalEaches(), strings.Join(parts, ", ")),
		TotalEaches:    c.TotalEaches(), DestinationNodeID: nodeID,
	}
}

// humanSSCC groups an 18-digit SSCC for the human readable line:
// "(00) E PPPP PPPP SSSS SSSS C" (extension digit, sixteen digits in fours, check digit).
func humanSSCC(s string) string {
	if len(s) != 18 {
		return "(00) " + s
	}
	return fmt.Sprintf("(00) %s %s %s %s %s %s", s[0:1], s[1:5], s[5:9], s[9:13], s[13:17], s[17:18])
}
