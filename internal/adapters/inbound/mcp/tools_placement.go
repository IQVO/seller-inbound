package mcp

import (
	"context"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/views"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- placement advisory (ADR 0012): inputs, outputs and handlers --------------

type placementPlanIDInput struct {
	PlanID string `json:"planId" jsonschema:"the placement plan id, e.g. pl-0f3a91"`
}

type listPlacementPlansInput struct {
	SellerID      string `json:"sellerId" jsonschema:"the seller id whose placement plans are listed, e.g. seller-77"`
	CatalogItemID string `json:"catalogItemId" jsonschema:"the catalogue item id, e.g. item-1"`
	Limit         int    `json:"limit,omitempty" jsonschema:"optional page size 1..100; omitted means 50"`
}

// placementPlanView is the wire shape of a placement plan (the REST body).
type placementPlanView struct {
	PlanID        string                    `json:"planId"`
	SellerID      string                    `json:"sellerId"`
	CatalogItemID string                    `json:"catalogItemId"`
	CoverDays     int                       `json:"coverDays"`
	WindowDays    int                       `json:"windowDays"`
	Basis         string                    `json:"basis"`
	State         string                    `json:"state"`
	Suggestions   []placementSuggestionView `json:"suggestions"`
	CreatedAt     time.Time                 `json:"createdAt"`
	AcceptedAt    *time.Time                `json:"acceptedAt,omitempty"`
	SupersededAt  *time.Time                `json:"supersededAt,omitempty"`
}

type placementSuggestionView struct {
	NodeID                string `json:"nodeId"`
	RegionID              string `json:"regionId"`
	SuggestedUnits        int    `json:"suggestedUnits"`
	RegionDemandUnits     int    `json:"regionDemandUnits"`
	NodeAvailabilityUnits int    `json:"nodeAvailabilityUnits"`
	CoverDays             int    `json:"coverDays"`
}

type placementPlanPageView struct {
	Items []placementPlanView `json:"items"`
}

func toPlacementPlan(p views.PlacementPlan) placementPlanView {
	out := placementPlanView{
		PlanID: p.PlanID, SellerID: p.SellerID, CatalogItemID: p.CatalogItemID, CoverDays: p.CoverDays,
		WindowDays: p.WindowDays, Basis: p.Basis, State: p.State, CreatedAt: p.CreatedAt.UTC(),
		Suggestions: make([]placementSuggestionView, 0, len(p.Suggestions)),
	}
	for _, s := range p.Suggestions {
		out.Suggestions = append(out.Suggestions, placementSuggestionView{
			NodeID: s.NodeID, RegionID: s.RegionID, SuggestedUnits: s.SuggestedUnits,
			RegionDemandUnits: s.RegionDemandUnits, NodeAvailabilityUnits: s.NodeAvailabilityUnits, CoverDays: s.CoverDays,
		})
	}
	if !p.AcceptedAt.IsZero() {
		t := p.AcceptedAt.UTC()
		out.AcceptedAt = &t
	}
	if !p.SupersededAt.IsZero() {
		t := p.SupersededAt.UTC()
		out.SupersededAt = &t
	}
	return out
}
func (d Deps) listPlacementPlans(ctx context.Context, in listPlacementPlansInput) (placementPlanPageView, error) {
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	items, err := d.Reads.ListPlacementPlans(ctx, in.SellerID, in.CatalogItemID, time.Time{}, "", limit)
	if err != nil {
		return placementPlanPageView{}, mapError(ctx, err)
	}
	out := make([]placementPlanView, 0, len(items))
	for _, p := range items {
		out = append(out, toPlacementPlan(p))
	}
	return placementPlanPageView{Items: out}, nil
}

func (d Deps) getPlacementPlan(ctx context.Context, in placementPlanIDInput) (placementPlanView, error) {
	v, err := d.Reads.GetPlacementPlan(ctx, in.PlanID)
	if err != nil {
		return placementPlanView{}, mapError(ctx, err)
	}
	return toPlacementPlan(v), nil
}

// registerPlacementTools registers the two placement read tools (ADR 0012).
func (d Deps) registerPlacementTools(server *mcp.Server) {
	closedWorld := false
	addTool(server, &mcp.Tool{
		Name: "get_placement_plan",
		Description: "Read one placement plan (ADR 0012): the seller, the catalogue item, coverDays and the observation windowDays, " +
			"the basis (demand-heat-v1), the state (SUGGESTED, ACCEPTED or SUPERSEDED), the createdAt/acceptedAt/supersededAt " +
			"instant(s) and the per-node suggestions (nodeId, regionId, suggestedUnits with the evidence: regionDemandUnits, " +
			"nodeAvailabilityUnits, coverDays). Read-only; an unknown plan fails with not-found.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getPlacementPlan)

	addTool(server, &mcp.Tool{
		Name: "list_placement_plans",
		Description: "List the placement plans of one (sellerId, catalogItemId), newest first (ADR 0012). Each item is the same " +
			"shape as get_placement_plan; the body is {items}. Read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listPlacementPlans)
}
