package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/seller-inbound/internal/application/views"
)

// Reads is the read-only slice of the application service: EXACTLY the four
// read use cases the REST adapter exposes (GET /sellers/{sellerId}/scorecard,
// GET /inbound-plans/{planId}, GET /inbound-shipments/{shipmentId}/labels and
// GET /inbound-shipments/{shipmentId}); *usecases.Service implements it. The
// adapter holds this and nothing that can write (docs/adr/0006): the
// composition root (cmd/mcp) wires no write seam at all.
type Reads interface {
	GetSellerScorecard(ctx context.Context, sellerID, window string) (views.SellerScorecard, error)
	GetInboundPlan(ctx context.Context, planID string) (views.Plan, error)
	GetLabels(ctx context.Context, shipmentID string) (views.Labels, error)
	GetInboundShipment(ctx context.Context, shipmentID string) (views.Shipment, error)
}

// Deps is everything the MCP tools need, injected by the composition root.
type Deps struct {
	Reads Reads
}

// --- inputs -------------------------------------------------------------------

// scorecardInput mirrors GET /sellers/{sellerId}/scorecard: the path parameter
// and the optional window query parameter.
type scorecardInput struct {
	SellerID string `json:"sellerId" jsonschema:"the seller id whose compliance scorecard is answered, e.g. seller-77"`
	Window   string `json:"window,omitempty" jsonschema:"optional rolling window that ends now: 7d or 30d; omitted means 30d"`
}

type planIDInput struct {
	PlanID string `json:"planId" jsonschema:"the inbound plan id, e.g. pln-0f3a91"`
}

type shipmentIDInput struct {
	ShipmentID string `json:"shipmentId" jsonschema:"the inbound shipment id, e.g. shp-0f3a91"`
}

// --- outputs (the REST bodies, camelCase like the REST contract) --------------

// scorecardView is the wire shape of the seller scorecard. A rate whose
// denominator is zero is omitted, never null or NaN; the kind map is always
// present.
type scorecardView struct {
	SellerID                   string         `json:"sellerId"`
	Window                     string         `json:"window"`
	WindowStart                time.Time      `json:"windowStart"`
	WindowEnd                  time.Time      `json:"windowEnd"`
	ShipmentsReceived          int            `json:"shipmentsReceived"`
	CartonsReported            int            `json:"cartonsReported"`
	CartonsReceived            int            `json:"cartonsReceived"`
	ReceiptAccuracyRate        *float64       `json:"receiptAccuracyRate,omitempty"`
	DiscrepancyRatePerShipment *float64       `json:"discrepancyRatePerShipment,omitempty"`
	DiscrepanciesByKind        map[string]int `json:"discrepanciesByKind"`
	ComplianceViolations       int            `json:"complianceViolations"`
	ComplianceTier             string         `json:"complianceTier"`
}

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
		return scorecardView{}, mapError(err)
	}
	return toScorecard(v), nil
}

func (d Deps) getInboundPlan(ctx context.Context, in planIDInput) (planView, error) {
	v, err := d.Reads.GetInboundPlan(ctx, in.PlanID)
	if err != nil {
		return planView{}, mapError(err)
	}
	return toPlan(v), nil
}

func (d Deps) getShipmentLabels(ctx context.Context, in shipmentIDInput) (labelsView, error) {
	v, err := d.Reads.GetLabels(ctx, in.ShipmentID)
	if err != nil {
		return labelsView{}, mapError(err)
	}
	return toLabels(v), nil
}

func (d Deps) getInboundShipment(ctx context.Context, in shipmentIDInput) (shipmentView, error) {
	v, err := d.Reads.GetInboundShipment(ctx, in.ShipmentID)
	if err != nil {
		return shipmentView{}, mapError(err)
	}
	return toShipment(v), nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the four read-only tools, one per read GET of
// apis/openapi.yaml. Every tool is annotated read-only, idempotent and
// closed-world; TestToolSurface pins the set and fails the build on any
// write-verb name (docs/adr/0006).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

	addTool(server, &mcp.Tool{
		Name: "get_seller_scorecard",
		Description: "Answer a seller's rolling-window compliance scorecard from the facts this service recorded (ADR 0005): shipments " +
			"received in the window, cartons reported and how many were RECEIVED, receiptAccuracyRate (omitted when nothing was reported), " +
			"discrepancies by kind, discrepancyRatePerShipment (omitted when no shipment closed), compliance violations and the derived " +
			"complianceTier (GOOD, WATCH or AT_RISK), which is informational only. window is 7d or 30d and defaults to 30d. Read-only; an " +
			"unknown seller fails with not-found, a bad window with invalid-window.",
		Annotations: readOnly,
	}, d.getSellerScorecard)

	addTool(server, &mcp.Tool{
		Name: "get_inbound_plan",
		Description: "Read one inbound plan: the seller, the destination node, the packing option (INDIVIDUAL or CASE_PACKED), the state " +
			"(DRAFT, CONFIRMED or CANCELLED) and the declared lines in eaches (catalog item, GTIN, quantity), with the created, confirmed " +
			"and cancelled instants. Read-only; an unknown id fails with not-found.",
		Annotations: readOnly,
	}, d.getInboundPlan)

	addTool(server, &mcp.Tool{
		Name: "get_shipment_labels",
		Description: "Read the label data of a labelled inbound shipment, one entry per carton: the SSCC, the GS1-128 element string " +
			"(00)<sscc>, the human readable line, the contents in eaches with lot and expiry when declared, the content summary, the " +
			"total eaches and the destination node. Read-only; an unknown id fails with not-found.",
		Annotations: readOnly,
	}, d.getShipmentLabels)

	addTool(server, &mcp.Tool{
		Name: "get_inbound_shipment",
		Description: "Read one inbound shipment: its plan, seller and destination node, the state (PLANNED, PACKING, LABELLED, SHIPPED or " +
			"RECEIVED) and every carton packed so far (SSCC, optional parent SSCC, kind, contents in eaches, gross weight, dimensions, " +
			"seal and receipt status). Read-only; an unknown id fails with not-found.",
		Annotations: readOnly,
	}, d.getInboundShipment)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
