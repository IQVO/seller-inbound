package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// Consumed topics: the producers' integration topics (their contracts are
// restated in apis/asyncapi.yaml, never imported).
const (
	NodeTopology       = "retailnetwork.node-topology.events"
	OfferAvailability  = "retailnetwork.offer-availability.events"
	VendorConnectivity = "retailnetwork.vendor-connectivity.events"
)

// Consumed types, byte-identical to the producers' AsyncAPI. Note the tier
// segment of each producer: node-topology is `logistics`, offer-availability
// `commerce`, vendor-connectivity `partner`.
const (
	TypeNodeRegistered  = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	TypeProfileDeclared = "com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared"
	TypeNodeActivated   = "com.retailnetwork.logistics.node-topology.node.NodeActivated"
	TypeNodeSuspended   = "com.retailnetwork.logistics.node-topology.node.NodeSuspended"
	TypeNodeRetired     = "com.retailnetwork.logistics.node-topology.node.NodeRetired"

	TypePackagingDeclared = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemPackagingDeclared"
	TypePackLevelRetired  = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemPackLevelRetired"

	TypeReceiptSubmitted = "com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted"
)

// Wire payloads: a TOLERANT local copy of only the fields this context uses.
// Unknown fields are ignored (decodeData uses encoding/json); optional ones
// default to their zero value.

type nodeRegisteredData struct {
	NodeID   string `json:"nodeId"`
	NodeType string `json:"nodeType"`
}

type nodeIDData struct {
	NodeID string `json:"nodeId"`
}

type wireInboundLimits struct {
	MaxCartonWeightG int `json:"maxCartonWeightG"`
	MaxCartonDimsMm  *struct {
		L int `json:"l"`
		W int `json:"w"`
		H int `json:"h"`
	} `json:"maxCartonDimsMm"`
	AcceptsPallets *bool `json:"acceptsPallets"`
	RequiresSscc   *bool `json:"requiresSscc"`
}

type profileDeclaredData struct {
	NodeID          string             `json:"nodeId"`
	ProfileRevision int                `json:"profileRevision"`
	Inbound         *wireInboundLimits `json:"inbound"`
}

type packagingDeclaredData struct {
	CatalogItemID string    `json:"catalogItemId"`
	Level         string    `json:"level"`
	GTIN          string    `json:"gtin"`
	ChildGTIN     string    `json:"childGtin"`
	UnitsPerPack  int       `json:"unitsPerPack"`
	DeclaredAt    time.Time `json:"declaredAt"`
}

type packLevelRetiredData struct {
	CatalogItemID string    `json:"catalogItemId"`
	Level         string    `json:"level"`
	GTIN          string    `json:"gtin"`
	RetiredAt     time.Time `json:"retiredAt"`
}

type receiptItemData struct {
	GTIN      string `json:"gtin"`
	QtyEaches int    `json:"qtyEaches"`
}

type receiptCartonData struct {
	SSCC            string            `json:"sscc"`
	Status          string            `json:"status"`
	Items           []receiptItemData `json:"items"`
	ComplianceCodes []string          `json:"complianceCodes"`
}

type receiptSubmittedData struct {
	ShipmentID string              `json:"shipmentId"`
	NodeID     string              `json:"nodeId"`
	ReceivedAt time.Time           `json:"receivedAt"`
	Cartons    []receiptCartonData `json:"cartons"`
}

// permanent marks an error that retrying can never cure (a value the domain
// refuses, a transition it forbids, a shipment that does not exist): the
// consumer dead-letters the event at once instead of retrying it. Anything else
// (a database error, a lost race) stays transient.
func permanent(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, shared.ErrValidation) || errors.Is(err, shared.ErrIllegalTransition) || errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("%w: %v", errBadPayload, err)
	}
	return err
}

// NodeHandlers maps node-topology's events to the NodeInboundView updates.
func NodeHandlers(svc *usecases.Service) map[string]HandlerFunc {
	lifecycle := func(apply func(context.Context, string, requests.NodeLifecycle) error) HandlerFunc {
		return func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[nodeIDData](e)
			if err != nil {
				return err
			}
			return permanent(apply(ctx, e.ID(), requests.NodeLifecycle{NodeID: d.NodeID}))
		}
	}
	return map[string]HandlerFunc{
		TypeNodeRegistered: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[nodeRegisteredData](e)
			if err != nil {
				return err
			}
			return permanent(svc.ApplyNodeRegistered(ctx, e.ID(), requests.NodeRegistered(d)))
		},
		TypeProfileDeclared: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[profileDeclaredData](e)
			if err != nil {
				return err
			}
			return permanent(svc.ApplyNodeProfileDeclared(ctx, e.ID(),
				requests.NodeProfileDeclared{NodeID: d.NodeID, ProfileRevision: d.ProfileRevision, Inbound: inboundRequest(d.Inbound)}))
		},
		TypeNodeActivated: lifecycle(svc.ApplyNodeActivated),
		TypeNodeSuspended: lifecycle(svc.ApplyNodeSuspended),
		TypeNodeRetired:   lifecycle(svc.ApplyNodeRetired),
	}
}

func inboundRequest(w *wireInboundLimits) *requests.NodeInbound {
	if w == nil {
		return nil
	}
	out := &requests.NodeInbound{MaxCartonWeightG: w.MaxCartonWeightG, AcceptsPallets: w.AcceptsPallets, RequiresSscc: w.RequiresSscc}
	if d := w.MaxCartonDimsMm; d != nil {
		out.MaxCartonDims = &requests.Dims{L: d.L, W: d.W, H: d.H}
	}
	return out
}

// CatalogHandlers maps offer-availability's packaging events to the CatalogPackagingView.
func CatalogHandlers(svc *usecases.Service) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypePackagingDeclared: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[packagingDeclaredData](e)
			if err != nil {
				return err
			}
			return permanent(svc.ApplyPackagingDeclared(ctx, e.ID(), requests.PackagingDeclared(d)))
		},
		TypePackLevelRetired: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[packLevelRetiredData](e)
			if err != nil {
				return err
			}
			return permanent(svc.ApplyPackLevelRetired(ctx, e.ID(), requests.PackLevelRetired(d)))
		},
	}
}

// ReceiptHandlers maps vendor-connectivity's InboundReceiptSubmitted to
// RecordInboundReceipt (policy ReceiveOnVendorReceipt).
func ReceiptHandlers(svc *usecases.Service, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeReceiptSubmitted: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[receiptSubmittedData](e)
			if err != nil {
				return err
			}
			in := requests.InboundReceipt{ShipmentID: d.ShipmentID, NodeID: d.NodeID, ReceivedAt: d.ReceivedAt}
			for _, c := range d.Cartons {
				rc := requests.ReceiptCarton{SSCC: c.SSCC, Status: c.Status, ComplianceCodes: c.ComplianceCodes}
				for _, it := range c.Items {
					rc.Items = append(rc.Items, requests.ReceiptItem(it))
				}
				in.Cartons = append(in.Cartons, rc)
			}
			if err := permanent(svc.ApplyInboundReceipt(ctx, e.ID(), in)); err != nil {
				return err
			}
			logger.InfoContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "subject", e.Subject())
			return nil
		},
	}
}

// NewNodeConsumer reads node-topology's topic under groupID (NODE_CONSUMER_GROUP).
func NewNodeConsumer(brokers []string, groupID string, svc *usecases.Service, logger *slog.Logger) *Consumer {
	return newConsumer("node consumer", brokers, NodeTopology, groupID, NodeHandlers(svc), logger)
}

// NewCatalogConsumer reads offer-availability's topic under groupID (CATALOG_CONSUMER_GROUP).
func NewCatalogConsumer(brokers []string, groupID string, svc *usecases.Service, logger *slog.Logger) *Consumer {
	return newConsumer("catalog consumer", brokers, OfferAvailability, groupID, CatalogHandlers(svc), logger)
}

// NewReceiptConsumer reads vendor-connectivity's topic under groupID (RECEIPT_CONSUMER_GROUP).
func NewReceiptConsumer(brokers []string, groupID string, svc *usecases.Service, logger *slog.Logger) *Consumer {
	return newConsumer("receipt consumer", brokers, VendorConnectivity, groupID, ReceiptHandlers(svc, logger), logger)
}
