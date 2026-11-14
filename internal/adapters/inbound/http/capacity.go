package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/seller-inbound/internal/application/views"
)

// capacityAllocationResponse is one row of GET
// /sellers/{sellerId}/capacity-allocations: the dm3 a seller's DRAFT and
// CONFIRMED plans of the month promise to one node (ADR 0015).
type capacityAllocationResponse struct {
	NodeID      string    `json:"nodeId"`
	ReservedDm3 int64     `json:"reservedDm3"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// capacityAllocationsResponse is the wire shape of the reservation read: the
// operator-facing honesty surface of the ADR 0015 gate.
type capacityAllocationsResponse struct {
	SellerID string                       `json:"sellerId"`
	Month    string                       `json:"month"`
	Items    []capacityAllocationResponse `json:"items"`
}

func capacityAllocationsOut(v views.CapacityAllocations) capacityAllocationsResponse {
	items := make([]capacityAllocationResponse, len(v.Items))
	for i, a := range v.Items {
		items[i] = capacityAllocationResponse{NodeID: a.NodeID, ReservedDm3: a.ReservedDm3, UpdatedAt: a.UpdatedAt.UTC()}
	}
	return capacityAllocationsResponse{SellerID: v.SellerID, Month: v.Month, Items: items}
}

func (s *Server) handleCapacityAllocations(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.GetSellerCapacityAllocations(r.Context(), chi.URLParam(r, "sellerId"), r.URL.Query().Get("month"))
	respond(w, r, capacityAllocationsOut(v), err)
}
