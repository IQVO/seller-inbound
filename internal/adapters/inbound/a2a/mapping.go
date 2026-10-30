package a2a

import (
	"fmt"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/views"
)

// Problem `type` values are the bare kebab-case slugs the REST adapter uses
// (internal/adapters/inbound/http/errors.go, apis/openapi.yaml: Problem), so a
// delegating agent sees the identifiers a REST caller would for the outcomes
// both surfaces share. The slugs below belong to the A2A request shape only.

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: 400}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: "unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: 400}
}

func problemInvalidArgument(detail string) *problem {
	return &problem{Type: "invalid-argument", Title: "Invalid skill argument", Detail: detail, Status: 400}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field, keeping the envelope
// flat and versioned as one contract.
type skillDTO struct {
	Scorecard *scorecardDTO `json:"scorecard,omitempty"`
	Plan      *planDTO      `json:"plan,omitempty"`
	Shipment  *shipmentDTO  `json:"shipment,omitempty"`
	summary   string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// scorecardDTO is the message-boundary representation of the seller
// scorecard. A rate whose denominator is zero is omitted, never null or NaN;
// the kind map is always present.
type scorecardDTO struct {
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

type planLineDTO struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
}

type planDTO struct {
	PlanID            string        `json:"planId"`
	SellerID          string        `json:"sellerId"`
	DestinationNodeID string        `json:"destinationNodeId"`
	PackingOption     string        `json:"packingOption"`
	State             string        `json:"state"`
	Lines             []planLineDTO `json:"lines"`
	CreatedAt         time.Time     `json:"createdAt"`
	ConfirmedAt       *time.Time    `json:"confirmedAt,omitempty"`
	CancelledAt       *time.Time    `json:"cancelledAt,omitempty"`
}

type contentDTO struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
	Lot           string `json:"lot,omitempty"`
	Expiry        string `json:"expiry,omitempty"`
}

type dimsDTO struct {
	L int `json:"l"`
	W int `json:"w"`
	H int `json:"h"`
}

type cartonDTO struct {
	SSCC          string       `json:"sscc"`
	ParentSSCC    string       `json:"parentSscc,omitempty"`
	Kind          string       `json:"kind"`
	Contents      []contentDTO `json:"contents"`
	GrossWeightG  int          `json:"grossWeightG"`
	DimsMm        dimsDTO      `json:"dimsMm"`
	Sealed        bool         `json:"sealed"`
	ReceiptStatus string       `json:"receiptStatus,omitempty"`
}

type shipmentDTO struct {
	ShipmentID string      `json:"shipmentId"`
	PlanID     string      `json:"planId"`
	SellerID   string      `json:"sellerId"`
	NodeID     string      `json:"nodeId"`
	State      string      `json:"state"`
	Cartons    []cartonDTO `json:"cartons"`
	CreatedAt  time.Time   `json:"createdAt"`
	LabelledAt *time.Time  `json:"labelledAt,omitempty"`
	ShippedAt  *time.Time  `json:"shippedAt,omitempty"`
	ReceivedAt *time.Time  `json:"receivedAt,omitempty"`
}

// instant formats an optional instant; the zero time is nil (omitted), like
// the REST adapter.
func instant(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toScorecardDTO(v views.SellerScorecard) scorecardDTO {
	kind := v.DiscrepanciesByKind
	if kind == nil {
		kind = map[string]int{}
	}
	return scorecardDTO{
		SellerID: v.SellerID, Window: v.Window, WindowStart: v.WindowStart.UTC(), WindowEnd: v.WindowEnd.UTC(),
		ShipmentsReceived: v.ShipmentsReceived, CartonsReported: v.CartonsReported, CartonsReceived: v.CartonsReceived,
		ReceiptAccuracyRate: v.ReceiptAccuracyRate, DiscrepancyRatePerShipment: v.DiscrepancyRatePerShipment,
		DiscrepanciesByKind: kind, ComplianceViolations: v.ComplianceViolations, ComplianceTier: v.ComplianceTier,
	}
}

func toPlanDTO(v views.Plan) planDTO {
	lines := make([]planLineDTO, len(v.Lines))
	for i, l := range v.Lines {
		lines[i] = planLineDTO{CatalogItemID: l.CatalogItemID, GTIN: l.GTIN, QtyEaches: l.QtyEaches}
	}
	return planDTO{
		PlanID: v.PlanID, SellerID: v.SellerID, DestinationNodeID: v.DestinationNodeID, PackingOption: v.PackingOption,
		State: v.State, Lines: lines, CreatedAt: v.CreatedAt.UTC(), ConfirmedAt: instant(v.ConfirmedAt), CancelledAt: instant(v.CancelledAt),
	}
}

func toContentDTOs(in []views.Content) []contentDTO {
	out := make([]contentDTO, len(in))
	for i, c := range in {
		out[i] = contentDTO{CatalogItemID: c.CatalogItemID, GTIN: c.GTIN, QtyEaches: c.QtyEaches, Lot: c.Lot, Expiry: c.Expiry}
	}
	return out
}

func toShipmentDTO(v views.Shipment) shipmentDTO {
	cartons := make([]cartonDTO, len(v.Cartons))
	for i, c := range v.Cartons {
		cartons[i] = cartonDTO{
			SSCC: c.SSCC, ParentSSCC: c.ParentSSCC, Kind: c.Kind, Contents: toContentDTOs(c.Contents),
			GrossWeightG: c.GrossWeightG, DimsMm: dimsDTO{L: c.DimsL, W: c.DimsW, H: c.DimsH}, Sealed: c.Sealed, ReceiptStatus: c.ReceiptStatus,
		}
	}
	return shipmentDTO{
		ShipmentID: v.ShipmentID, PlanID: v.PlanID, SellerID: v.SellerID, NodeID: v.NodeID, State: v.State, Cartons: cartons,
		CreatedAt: v.CreatedAt.UTC(), LabelledAt: instant(v.LabelledAt), ShippedAt: instant(v.ShippedAt), ReceivedAt: instant(v.ReceivedAt),
	}
}

// summarizeScorecard is the one-line text form of a scorecard.
func summarizeScorecard(s scorecardDTO) string {
	return fmt.Sprintf("%s: %s compliance tier over %s (%d shipments received, %d compliance violations)",
		s.SellerID, s.ComplianceTier, s.Window, s.ShipmentsReceived, s.ComplianceViolations)
}

// summarizePlan is the one-line text form of an inbound plan.
func summarizePlan(p planDTO) string {
	eaches := 0
	for _, l := range p.Lines {
		eaches += l.QtyEaches
	}
	return fmt.Sprintf("%s: %s plan of seller %s to node %s, %s, %d lines (%d eaches)",
		p.PlanID, p.State, p.SellerID, p.DestinationNodeID, p.PackingOption, len(p.Lines), eaches)
}

// summarizeShipment is the one-line text form of an inbound shipment.
func summarizeShipment(s shipmentDTO) string {
	return fmt.Sprintf("%s: %s shipment of plan %s to node %s, %d cartons",
		s.ShipmentID, s.State, s.PlanID, s.NodeID, len(s.Cartons))
}
