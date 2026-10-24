package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
)

// Wire shapes. Optional fields are omitted when empty; instants are RFC 3339 UTC.

type onboardSellerBody struct {
	SellerID string `json:"sellerId"`
	Name     string `json:"name"`
}

type suspendSellerBody struct {
	Reason string `json:"reason"`
}

type planLineBody struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
}

type createPlanBody struct {
	SellerID          string         `json:"sellerId"`
	DestinationNodeID string         `json:"destinationNodeId"`
	PackingOption     string         `json:"packingOption"`
	Lines             []planLineBody `json:"lines"`
}

type contentBody struct {
	CatalogItemID string `json:"catalogItemId"`
	GTIN          string `json:"gtin"`
	QtyEaches     int    `json:"qtyEaches"`
	Lot           string `json:"lot,omitempty"`
	Expiry        string `json:"expiry,omitempty"`
}

type dimsBody struct {
	L int `json:"l"`
	W int `json:"w"`
	H int `json:"h"`
}

type addCartonBody struct {
	SSCC         string        `json:"sscc"`
	ParentSSCC   string        `json:"parentSscc"`
	Kind         string        `json:"kind"`
	Contents     []contentBody `json:"contents"`
	GrossWeightG int           `json:"grossWeightG"`
	DimsMm       dimsBody      `json:"dimsMm"`
}

type sellerResponse struct {
	SellerID      string     `json:"sellerId"`
	Name          string     `json:"name"`
	State         string     `json:"state"`
	OnboardedAt   time.Time  `json:"onboardedAt"`
	SuspendedAt   *time.Time `json:"suspendedAt,omitempty"`
	SuspendReason string     `json:"suspendReason,omitempty"`
}

type planResponse struct {
	PlanID            string         `json:"planId"`
	SellerID          string         `json:"sellerId"`
	DestinationNodeID string         `json:"destinationNodeId"`
	PackingOption     string         `json:"packingOption"`
	State             string         `json:"state"`
	Lines             []planLineBody `json:"lines"`
	CreatedAt         time.Time      `json:"createdAt"`
	ConfirmedAt       *time.Time     `json:"confirmedAt,omitempty"`
	CancelledAt       *time.Time     `json:"cancelledAt,omitempty"`
}

type cartonResponse struct {
	SSCC          string        `json:"sscc"`
	ParentSSCC    string        `json:"parentSscc,omitempty"`
	Kind          string        `json:"kind"`
	Contents      []contentBody `json:"contents"`
	GrossWeightG  int           `json:"grossWeightG"`
	DimsMm        dimsBody      `json:"dimsMm"`
	Sealed        bool          `json:"sealed"`
	ReceiptStatus string        `json:"receiptStatus,omitempty"`
}

type shipmentResponse struct {
	ShipmentID string           `json:"shipmentId"`
	PlanID     string           `json:"planId"`
	SellerID   string           `json:"sellerId"`
	NodeID     string           `json:"nodeId"`
	State      string           `json:"state"`
	Cartons    []cartonResponse `json:"cartons"`
	CreatedAt  time.Time        `json:"createdAt"`
	LabelledAt *time.Time       `json:"labelledAt,omitempty"`
	ShippedAt  *time.Time       `json:"shippedAt,omitempty"`
	ReceivedAt *time.Time       `json:"receivedAt,omitempty"`
}

type labelResponse struct {
	LabelRef          string        `json:"labelRef"`
	SSCC              string        `json:"sscc"`
	GS1128            string        `json:"gs1128"`
	HumanReadable     string        `json:"humanReadable"`
	ParentSSCC        string        `json:"parentSscc,omitempty"`
	Kind              string        `json:"kind"`
	Contents          []contentBody `json:"contents"`
	ContentSummary    string        `json:"contentSummary"`
	TotalEaches       int           `json:"totalEaches"`
	DestinationNodeID string        `json:"destinationNodeId"`
}

type addedCartonResponse struct {
	ShipmentID string         `json:"shipmentId"`
	Carton     cartonResponse `json:"carton"`
	SSCCIssued bool           `json:"ssccIssued"`
	Label      labelResponse  `json:"label"`
}

type labelsResponse struct {
	ShipmentID        string          `json:"shipmentId"`
	State             string          `json:"state"`
	DestinationNodeID string          `json:"destinationNodeId"`
	Labels            []labelResponse `json:"labels"`
}

func tptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func contentBodies(in []views.Content) []contentBody {
	out := make([]contentBody, len(in))
	for i, c := range in {
		out[i] = contentBody(c)
	}
	return out
}

func sellerOut(v views.Seller) sellerResponse {
	return sellerResponse{SellerID: v.SellerID, Name: v.Name, State: v.State, OnboardedAt: v.OnboardedAt.UTC(),
		SuspendedAt: tptr(v.SuspendedAt), SuspendReason: v.SuspendReason}
}

func planOut(v views.Plan) planResponse {
	lines := make([]planLineBody, len(v.Lines))
	for i, l := range v.Lines {
		lines[i] = planLineBody(l)
	}
	return planResponse{PlanID: v.PlanID, SellerID: v.SellerID, DestinationNodeID: v.DestinationNodeID, PackingOption: v.PackingOption,
		State: v.State, Lines: lines, CreatedAt: v.CreatedAt.UTC(), ConfirmedAt: tptr(v.ConfirmedAt), CancelledAt: tptr(v.CancelledAt)}
}

func cartonOut(c views.Carton) cartonResponse {
	return cartonResponse{SSCC: c.SSCC, ParentSSCC: c.ParentSSCC, Kind: c.Kind, Contents: contentBodies(c.Contents), GrossWeightG: c.GrossWeightG,
		DimsMm: dimsBody{L: c.DimsL, W: c.DimsW, H: c.DimsH}, Sealed: c.Sealed, ReceiptStatus: c.ReceiptStatus}
}

func shipmentOut(v views.Shipment) shipmentResponse {
	cartons := make([]cartonResponse, len(v.Cartons))
	for i, c := range v.Cartons {
		cartons[i] = cartonOut(c)
	}
	return shipmentResponse{ShipmentID: v.ShipmentID, PlanID: v.PlanID, SellerID: v.SellerID, NodeID: v.NodeID, State: v.State, Cartons: cartons,
		CreatedAt: v.CreatedAt.UTC(), LabelledAt: tptr(v.LabelledAt), ShippedAt: tptr(v.ShippedAt), ReceivedAt: tptr(v.ReceivedAt)}
}

func labelOut(l views.Label) labelResponse {
	return labelResponse{LabelRef: l.LabelRef, SSCC: l.SSCC, GS1128: l.GS1128, HumanReadable: l.HumanReadable, ParentSSCC: l.ParentSSCC, Kind: l.Kind,
		Contents: contentBodies(l.Contents), ContentSummary: l.ContentSummary, TotalEaches: l.TotalEaches, DestinationNodeID: l.DestinationNodeID}
}

// created answers a write that made a resource: 201 and the body.
func created[T any](w http.ResponseWriter, r *http.Request, v T, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// respond answers a read or a state change with 200 and v.
func respond[T any](w http.ResponseWriter, r *http.Request, v T, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// splitAction splits "<id>:<verb>" at the last colon. A resource id may itself
// contain a colon (shared.ValidID), the verb never does.
func splitAction(segment string) (id, verb string) {
	i := strings.LastIndexByte(segment, ':')
	if i < 0 {
		return segment, ""
	}
	return segment[:i], segment[i+1:]
}

func (s *Server) handleOnboardSeller(w http.ResponseWriter, r *http.Request) {
	var in onboardSellerBody
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.Service.OnboardSeller(r.Context(), requests.OnboardSeller{SellerID: in.SellerID, Name: in.Name})
	created(w, r, sellerOut(v), err)
}

func (s *Server) handleSellerAction(w http.ResponseWriter, r *http.Request) {
	id, verb := splitAction(chi.URLParam(r, "sellerAction"))
	switch verb {
	case "suspend":
		var in suspendSellerBody
		if err := decodeOptionalJSON(r, &in); err != nil {
			writeError(w, r, err)
			return
		}
		v, err := s.Service.SuspendSeller(r.Context(), requests.SuspendSeller{SellerID: id, Reason: in.Reason})
		respond(w, r, sellerOut(v), err)
	case "reinstate":
		v, err := s.Service.ReinstateSeller(r.Context(), id)
		respond(w, r, sellerOut(v), err)
	default:
		writeError(w, r, errUnknownAction)
	}
}

func (s *Server) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	var in createPlanBody
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	lines := make([]requests.PlanLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = requests.PlanLine(l)
	}
	v, err := s.Service.CreateInboundPlan(r.Context(), requests.CreateInboundPlan{SellerID: in.SellerID,
		DestinationNodeID: in.DestinationNodeID, PackingOption: in.PackingOption, Lines: lines})
	created(w, r, planOut(v), err)
}

func (s *Server) handlePlanAction(w http.ResponseWriter, r *http.Request) {
	id, verb := splitAction(chi.URLParam(r, "planAction"))
	switch verb {
	case "confirm":
		v, err := s.Service.ConfirmInboundPlan(r.Context(), id)
		respond(w, r, planOut(v), err)
	case "cancel":
		v, err := s.Service.CancelInboundPlan(r.Context(), id)
		respond(w, r, planOut(v), err)
	default:
		writeError(w, r, errUnknownAction)
	}
}

func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.GetInboundPlan(r.Context(), chi.URLParam(r, "planId"))
	respond(w, r, planOut(v), err)
}

func (s *Server) handleCreateShipment(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.CreateInboundShipment(r.Context(), chi.URLParam(r, "planId"))
	created(w, r, shipmentOut(v), err)
}

func (s *Server) handleAddCarton(w http.ResponseWriter, r *http.Request) {
	var in addCartonBody
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	contents := make([]requests.Content, len(in.Contents))
	for i, c := range in.Contents {
		contents[i] = requests.Content(c)
	}
	v, err := s.Service.AddCarton(r.Context(), requests.AddCarton{ShipmentID: chi.URLParam(r, "shipmentId"), SSCC: in.SSCC,
		ParentSSCC: in.ParentSSCC, Kind: in.Kind, Contents: contents, GrossWeightG: in.GrossWeightG,
		DimsL: in.DimsMm.L, DimsW: in.DimsMm.W, DimsH: in.DimsMm.H})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, addedCartonResponse{ShipmentID: v.ShipmentID, Carton: cartonOut(v.Carton), SSCCIssued: v.Issued, Label: labelOut(v.Label)})
}

func (s *Server) handleShipmentAction(w http.ResponseWriter, r *http.Request) {
	id, verb := splitAction(chi.URLParam(r, "shipmentAction"))
	switch verb {
	case "label":
		v, err := s.Service.LabelShipment(r.Context(), id)
		respond(w, r, shipmentOut(v), err)
	case "ship":
		v, err := s.Service.ShipInboundShipment(r.Context(), id)
		respond(w, r, shipmentOut(v), err)
	default:
		writeError(w, r, errUnknownAction)
	}
}

func (s *Server) handleGetShipment(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.GetInboundShipment(r.Context(), chi.URLParam(r, "shipmentId"))
	respond(w, r, shipmentOut(v), err)
}

func (s *Server) handleGetLabels(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.GetLabels(r.Context(), chi.URLParam(r, "shipmentId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	labels := make([]labelResponse, len(v.Labels))
	for i, l := range v.Labels {
		labels[i] = labelOut(l)
	}
	writeJSON(w, http.StatusOK, labelsResponse{ShipmentID: v.ShipmentID, State: v.State, DestinationNodeID: v.DestinationNodeID, Labels: labels})
}
