package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

// Event types, used as the inbox's event_type column.
const (
	evNodeRegistered   = "NodeRegistered"
	evNodeProfile      = "NodeOperationalProfileDeclared"
	evNodeActivated    = "NodeActivated"
	evNodeSuspended    = "NodeSuspended"
	evNodeRetired      = "NodeRetired"
	evPackaging        = "CatalogItemPackagingDeclared"
	evPackLevelRetired = "CatalogItemPackLevelRetired"
	evReceipt          = "InboundReceiptSubmitted"
)

// nodeOrNew returns the node row, or an empty one when the view has not seen it.
func (s *Service) nodeOrNew(ctx context.Context, nodeID string) (network.Node, error) {
	n, err := s.d.Nodes.Get(ctx, nodeID)
	if errors.Is(err, ports.ErrNotFound) {
		return network.Node{ID: nodeID}, nil
	}
	return n, err
}

func (s *Service) foldNode(ctx context.Context, eventID, eventType, nodeID string, fold func(network.Node) (network.Node, error)) error {
	return s.consume(ctx, eventID, eventType, func(ctx context.Context) error {
		n, err := s.nodeOrNew(ctx, nodeID)
		if err != nil {
			return err
		}
		next, err := fold(n)
		if err != nil {
			return err
		}
		return s.d.Nodes.Save(ctx, next)
	})
}

// ApplyNodeRegistered is policy NodeMustBeAbleToReceive on NodeRegistered.
func (s *Service) ApplyNodeRegistered(ctx context.Context, eventID string, in requests.NodeRegistered) error {
	return s.foldNode(ctx, eventID, evNodeRegistered, in.NodeID, func(n network.Node) (network.Node, error) {
		return n.Registered(in.NodeID, in.NodeType)
	})
}

// ApplyNodeProfileDeclared records the inbound limits of a node profile revision.
func (s *Service) ApplyNodeProfileDeclared(ctx context.Context, eventID string, in requests.NodeProfileDeclared) error {
	return s.foldNode(ctx, eventID, evNodeProfile, in.NodeID, func(n network.Node) (network.Node, error) {
		return n.ProfileDeclared(in.NodeID, in.ProfileRevision, limitsFromRequest(in.Inbound)), nil
	})
}

func limitsFromRequest(in *requests.NodeInbound) *network.InboundLimits {
	if in == nil {
		return nil
	}
	out := &network.InboundLimits{MaxCartonWeightG: in.MaxCartonWeightG, AcceptsPallets: in.AcceptsPallets, RequiresSscc: in.RequiresSscc}
	if d := in.MaxCartonDims; d != nil {
		out.MaxCartonDims = &network.Dims{L: d.L, W: d.W, H: d.H}
	}
	return out
}

// ApplyNodeActivated marks the node ACTIVE.
func (s *Service) ApplyNodeActivated(ctx context.Context, eventID string, in requests.NodeLifecycle) error {
	return s.foldNode(ctx, eventID, evNodeActivated, in.NodeID, func(n network.Node) (network.Node, error) {
		return n.Activated(in.NodeID), nil
	})
}

// ApplyNodeSuspended marks the node SUSPENDED.
func (s *Service) ApplyNodeSuspended(ctx context.Context, eventID string, in requests.NodeLifecycle) error {
	return s.foldNode(ctx, eventID, evNodeSuspended, in.NodeID, func(n network.Node) (network.Node, error) {
		return n.Suspended(in.NodeID), nil
	})
}

// ApplyNodeRetired marks the node RETIRED.
func (s *Service) ApplyNodeRetired(ctx context.Context, eventID string, in requests.NodeLifecycle) error {
	return s.foldNode(ctx, eventID, evNodeRetired, in.NodeID, func(n network.Node) (network.Node, error) {
		return n.Retired(in.NodeID), nil
	})
}

func (s *Service) existingPackLevel(ctx context.Context, item string, level network.Level, gtin string) (*network.PackLevel, error) {
	row, err := s.d.Packaging.Get(ctx, item, level, gtin)
	if errors.Is(err, ports.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// ApplyPackagingDeclared records a declared pack level in CatalogPackagingView.
func (s *Service) ApplyPackagingDeclared(ctx context.Context, eventID string, in requests.PackagingDeclared) error {
	return s.consume(ctx, eventID, evPackaging, func(ctx context.Context) error {
		gtin, err := normGTIN(in.GTIN)
		if err != nil {
			return err
		}
		existing, err := s.existingPackLevel(ctx, in.CatalogItemID, network.Level(in.Level), gtin)
		if err != nil {
			return err
		}
		row, err := network.Declare(existing, in.CatalogItemID, network.Level(in.Level), in.GTIN, in.ChildGTIN, in.UnitsPerPack, in.DeclaredAt)
		if err != nil {
			return err
		}
		return s.d.Packaging.Save(ctx, row)
	})
}

// ApplyPackLevelRetired marks a pack level retired in CatalogPackagingView.
func (s *Service) ApplyPackLevelRetired(ctx context.Context, eventID string, in requests.PackLevelRetired) error {
	return s.consume(ctx, eventID, evPackLevelRetired, func(ctx context.Context) error {
		gtin, err := normGTIN(in.GTIN)
		if err != nil {
			return err
		}
		existing, err := s.existingPackLevel(ctx, in.CatalogItemID, network.Level(in.Level), gtin)
		if err != nil {
			return err
		}
		row, err := network.Retire(existing, in.CatalogItemID, network.Level(in.Level), in.GTIN, in.RetiredAt)
		if err != nil {
			return err
		}
		return s.d.Packaging.Save(ctx, row)
	})
}

// ApplyInboundReceipt is policy ReceiveOnVendorReceipt: run RecordInboundReceipt
// for the shipment, publishing InboundDiscrepancyRecorded,
// SellerComplianceViolationRecorded and, when the shipment closes,
// InboundShipmentReceived. An unknown shipment is ports.ErrNotFound (poison).
func (s *Service) ApplyInboundReceipt(ctx context.Context, eventID string, in requests.InboundReceipt) error {
	return s.consume(ctx, eventID, evReceipt, func(ctx context.Context) error {
		sh, err := s.d.Shipments.Get(ctx, inboundshipment.ID(in.ShipmentID))
		if err != nil {
			return fmt.Errorf("receipt for shipment %q: %w", in.ShipmentID, err)
		}
		cartons := make([]inboundshipment.ReceiptCarton, len(in.Cartons))
		for i, c := range in.Cartons {
			items := make([]inboundshipment.ReceiptItem, len(c.Items))
			for j, it := range c.Items {
				items[j] = inboundshipment.ReceiptItem{GTIN: it.GTIN, QtyEaches: it.QtyEaches}
			}
			cartons[i] = inboundshipment.ReceiptCarton{SSCC: c.SSCC, Status: inboundshipment.ReceiptStatus(c.Status), Items: items, ComplianceCodes: c.ComplianceCodes}
		}
		before := sh.Version()
		evs, err := sh.RecordReceipt(inboundshipment.Receipt{NodeID: in.NodeID, ReceivedAt: in.ReceivedAt, Cartons: cartons}, s.d.Clock.Now())
		if err != nil {
			return err
		}
		if sh.Version() == before {
			return nil // a redelivered or late receipt for a closed shipment changes nothing
		}
		if err := s.d.Shipments.Save(ctx, sh); err != nil {
			return mapSave(err)
		}
		return s.d.Outbox.Append(ctx, evs...)
	})
}

func normGTIN(s string) (string, error) {
	g, err := gs1.ParseGTIN(s)
	if err != nil {
		return "", shared.Invalid("gtin: %v", err)
	}
	return g.String(), nil
}
