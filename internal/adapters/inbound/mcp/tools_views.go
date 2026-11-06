package mcp

import (
	"context"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/views"
)

type planLineView struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
}

type planView struct {
	PlanID            string         `json:"planId"`
	SellerID          string         `json:"sellerId"`
	DestinationNodeID string         `json:"destinationNodeId"`
	PackingOption     string         `json:"packingOption"`
	State             string         `json:"state"`
	Lines             []planLineView `json:"lines"`
	CreatedAt         time.Time      `json:"createdAt"`
	ConfirmedAt       *time.Time     `json:"confirmedAt,omitempty"`
	CancelledAt       *time.Time     `json:"cancelledAt,omitempty"`
}

type contentView struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
	Lot           string `json:"lot,omitempty"`
	Expiry        string `json:"expiry,omitempty"`
}

type dimsView struct {
	L int `json:"l"`
	W int `json:"w"`
	H int `json:"h"`
}

type cartonView struct {
	SSCC          string        `json:"sscc"`
	ParentSSCC    string        `json:"parentSscc,omitempty"`
	Kind          string        `json:"kind"`
	Contents      []contentView `json:"contents"`
	GrossWeightG  int           `json:"grossWeightG"`
	DimsMm        dimsView      `json:"dimsMm"`
	Sealed        bool          `json:"sealed"`
	ReceiptStatus string        `json:"receiptStatus,omitempty"`
}

type shipmentView struct {
	ShipmentID string       `json:"shipmentId"`
	PlanID     string       `json:"planId"`
	SellerID   string       `json:"sellerId"`
	NodeID     string       `json:"nodeId"`
	State      string       `json:"state"`
	Cartons    []cartonView `json:"cartons"`
	CreatedAt  time.Time    `json:"createdAt"`
	LabelledAt *time.Time   `json:"labelledAt,omitempty"`
	ShippedAt  *time.Time   `json:"shippedAt,omitempty"`
	ReceivedAt *time.Time   `json:"receivedAt,omitempty"`
}

type labelView struct {
	LabelRef          string        `json:"labelRef"`
	SSCC              string        `json:"sscc"`
	GS1128            string        `json:"gs1128"`
	HumanReadable     string        `json:"humanReadable"`
	ParentSSCC        string        `json:"parentSscc,omitempty"`
	Kind              string        `json:"kind"`
	Contents          []contentView `json:"contents"`
	ContentSummary    string        `json:"contentSummary"`
	TotalEaches       int           `json:"totalEaches"`
	DestinationNodeID string        `json:"destinationNodeId"`
}

type labelsView struct {
	ShipmentID        string      `json:"shipmentId"`
	State             string      `json:"state"`
	DestinationNodeID string      `json:"destinationNodeId"`
	Labels            []labelView `json:"labels"`
}

// --- mapping ------------------------------------------------------------------

// instant formats an optional instant; the zero time is nil (omitted), like
// the REST adapter's tptr.
func instant(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toScorecard(v views.SellerScorecard) scorecardView {
	kind := v.DiscrepanciesByKind
	if kind == nil {
		kind = map[string]int{}
	}
	return scorecardView{
		SellerID: v.SellerID, Window: v.Window, WindowStart: v.WindowStart.UTC(), WindowEnd: v.WindowEnd.UTC(),
		ShipmentsReceived: v.ShipmentsReceived, CartonsReported: v.CartonsReported, CartonsReceived: v.CartonsReceived,
		ReceiptAccuracyRate: v.ReceiptAccuracyRate, DiscrepancyRatePerShipment: v.DiscrepancyRatePerShipment,
		DiscrepanciesByKind: kind, ComplianceViolations: v.ComplianceViolations, ComplianceTier: v.ComplianceTier,
	}
}

func toPlan(v views.Plan) planView {
	lines := make([]planLineView, len(v.Lines))
	for i, l := range v.Lines {
		lines[i] = planLineView{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches}
	}
	return planView{
		PlanID: v.PlanID, SellerID: v.SellerID, DestinationNodeID: v.DestinationNodeID, PackingOption: v.PackingOption,
		State: v.State, Lines: lines, CreatedAt: v.CreatedAt.UTC(), ConfirmedAt: instant(v.ConfirmedAt), CancelledAt: instant(v.CancelledAt),
	}
}

func toContents(in []views.Content) []contentView {
	out := make([]contentView, len(in))
	for i, c := range in {
		out[i] = contentView{CatalogItemID: c.CatalogItemID, GTIN: c.GTIN, QtyEaches: c.QtyEaches, Lot: c.Lot, Expiry: c.Expiry}
	}
	return out
}

func toShipment(v views.Shipment) shipmentView {
	cartons := make([]cartonView, len(v.Cartons))
	for i, c := range v.Cartons {
		cartons[i] = cartonView{
			SSCC: c.SSCC, ParentSSCC: c.ParentSSCC, Kind: c.Kind, Contents: toContents(c.Contents),
			GrossWeightG: c.GrossWeightG, DimsMm: dimsView{L: c.DimsL, W: c.DimsW, H: c.DimsH}, Sealed: c.Sealed, ReceiptStatus: c.ReceiptStatus,
		}
	}
	return shipmentView{
		ShipmentID: v.ShipmentID, PlanID: v.PlanID, SellerID: v.SellerID, NodeID: v.NodeID, State: v.State, Cartons: cartons,
		CreatedAt: v.CreatedAt.UTC(), LabelledAt: instant(v.LabelledAt), ShippedAt: instant(v.ShippedAt), ReceivedAt: instant(v.ReceivedAt),
	}
}

func toLabels(v views.Labels) labelsView {
	labels := make([]labelView, len(v.Labels))
	for i, l := range v.Labels {
		labels[i] = labelView{
			LabelRef: l.LabelRef, SSCC: l.SSCC, GS1128: l.GS1128, HumanReadable: l.HumanReadable, ParentSSCC: l.ParentSSCC,
			Kind: l.Kind, Contents: toContents(l.Contents), ContentSummary: l.ContentSummary, TotalEaches: l.TotalEaches,
			DestinationNodeID: l.DestinationNodeID,
		}
	}
	return labelsView{ShipmentID: v.ShipmentID, State: v.State, DestinationNodeID: v.DestinationNodeID, Labels: labels}
}

// --- handlers -----------------------------------------------------------------

func (d Deps) getSellerScorecard(ctx context.Context, in scorecardInput) (scorecardView, error) {
	v, err := d.Reads.GetSellerScorecard(ctx, in.SellerID, in.Window)
	if err != nil {
		return scorecardView{}, mapError(ctx, err)
	}
	return toScorecard(v), nil
}

func (d Deps) getInboundPlan(ctx context.Context, in planIDInput) (planView, error) {
	v, err := d.Reads.GetInboundPlan(ctx, in.PlanID)
	if err != nil {
		return planView{}, mapError(ctx, err)
	}
	return toPlan(v), nil
}

func (d Deps) getShipmentLabels(ctx context.Context, in shipmentIDInput) (labelsView, error) {
	v, err := d.Reads.GetLabels(ctx, in.ShipmentID)
	if err != nil {
		return labelsView{}, mapError(ctx, err)
	}
	return toLabels(v), nil
}

func (d Deps) getInboundShipment(ctx context.Context, in shipmentIDInput) (shipmentView, error) {
	v, err := d.Reads.GetInboundShipment(ctx, in.ShipmentID)
	if err != nil {
		return shipmentView{}, mapError(ctx, err)
	}
	return toShipment(v), nil
}
