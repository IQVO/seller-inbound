package http

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/requests"
	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/go-chi/chi/v5"
)

// placementPlanBody is the compute command of the advisory (ADR 0012).
type placementPlanBody struct {
	SellerID      string `json:"sellerId"`
	CatalogItemID string `json:"catalogItemId"`
	CoverDays     *int   `json:"coverDays,omitempty"`
}

type placementSuggestionOut struct {
	NodeID                string `json:"nodeId"`
	RegionID              string `json:"regionId"`
	SuggestedUnits        int    `json:"suggestedUnits"`
	RegionDemandUnits     int    `json:"regionDemandUnits"`
	NodeAvailabilityUnits int    `json:"nodeAvailabilityUnits"`
	CoverDays             int    `json:"coverDays"`
}

type placementPlanOut struct {
	PlanID        string                   `json:"planId"`
	SellerID      string                   `json:"sellerId"`
	CatalogItemID string                   `json:"catalogItemId"`
	CoverDays     int                      `json:"coverDays"`
	WindowDays    int                      `json:"windowDays"`
	Basis         string                   `json:"basis"`
	State         string                   `json:"state"`
	Suggestions   []placementSuggestionOut `json:"suggestions"`
	CreatedAt     string                   `json:"createdAt"`
	AcceptedAt    *string                  `json:"acceptedAt,omitempty"`
	SupersededAt  *string                  `json:"supersededAt,omitempty"`
}

func placementOut(p views.PlacementPlan) placementPlanOut {
	out := placementPlanOut{
		PlanID: p.PlanID, SellerID: p.SellerID, CatalogItemID: p.CatalogItemID,
		CoverDays: p.CoverDays, WindowDays: p.WindowDays, Basis: p.Basis, State: p.State,
		Suggestions: make([]placementSuggestionOut, 0, len(p.Suggestions)),
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, s := range p.Suggestions {
		out.Suggestions = append(out.Suggestions, placementSuggestionOut{
			NodeID: s.NodeID, RegionID: s.RegionID, SuggestedUnits: s.SuggestedUnits,
			RegionDemandUnits: s.RegionDemandUnits, NodeAvailabilityUnits: s.NodeAvailabilityUnits, CoverDays: s.CoverDays,
		})
	}
	if !p.AcceptedAt.IsZero() {
		t := p.AcceptedAt.UTC().Format(time.RFC3339)
		out.AcceptedAt = &t
	}
	if !p.SupersededAt.IsZero() {
		t := p.SupersededAt.UTC().Format(time.RFC3339)
		out.SupersededAt = &t
	}
	return out
}

func (s *Server) handleComputePlacementPlan(w http.ResponseWriter, r *http.Request) {
	var in placementPlanBody
	if err := decodeJSON(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	req := requests.ComputePlacementPlan{SellerID: in.SellerID, CatalogItemID: in.CatalogItemID}
	if in.CoverDays != nil {
		req.CoverDays = *in.CoverDays
	}
	v, err := s.Service.ComputePlacementPlan(r.Context(), req)
	created(w, r, placementOut(v), err)
}

func (s *Server) handleAcceptPlacementPlan(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.AcceptPlacementPlan(r.Context(), chi.URLParam(r, "planId"))
	respond(w, r, placementOut(v), err)
}

func (s *Server) handleGetPlacementPlan(w http.ResponseWriter, r *http.Request) {
	v, err := s.Service.GetPlacementPlan(r.Context(), chi.URLParam(r, "planId"))
	respond(w, r, placementOut(v), err)
}

// handleListPlacementPlans pages the plans of one (seller, item) newest first
// with the fleet keyset convention {items, nextCursor}; the cursor is
// "<createdAt RFC3339>|<planId>".
func (s *Server) handleListPlacementPlans(w http.ResponseWriter, r *http.Request) {
	sellerID := r.URL.Query().Get("sellerId")
	itemID := r.URL.Query().Get("catalogItemId")
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, r, fmt.Errorf("%w: limit must be 1..100", errMalformedRequest))
			return
		}
		limit = n
	}
	var afterCreatedAt time.Time
	var afterPlanID string
	if cur := r.URL.Query().Get("cursor"); cur != "" {
		parts := strings.SplitN(cur, "|", 2)
		if len(parts) != 2 {
			writeError(w, r, fmt.Errorf("%w: cursor must be \"<createdAt>|<planId>\"", errMalformedRequest))
			return
		}
		t, err := time.Parse(time.RFC3339, parts[0])
		if err != nil {
			writeError(w, r, fmt.Errorf("%w: cursor createdAt is not RFC 3339", errMalformedRequest))
			return
		}
		afterCreatedAt, afterPlanID = t.UTC(), parts[1]
	}
	items, err := s.Service.ListPlacementPlans(r.Context(), sellerID, itemID, afterCreatedAt, afterPlanID, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]placementPlanOut, 0, len(items))
	for _, p := range items {
		out = append(out, placementOut(p))
	}
	body := struct {
		Items      []placementPlanOut `json:"items"`
		NextCursor string             `json:"nextCursor"`
	}{Items: out}
	if len(items) == limit {
		last := items[len(items)-1]
		body.NextCursor = fmt.Sprintf("%s|%s", last.CreatedAt.UTC().Format(time.RFC3339), last.PlanID)
	}
	writeJSON(w, http.StatusOK, body)
}
