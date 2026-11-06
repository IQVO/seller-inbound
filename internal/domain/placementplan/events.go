package placementplan

import (
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// Entity is the CloudEvents entity segment of the PlacementPlan's events.
const Entity = "placementplan"

// PlacementPlanSuggested is raised by Compute: the advisory was recorded, for
// analytics to read (no context consumes it in v1; ADR 0012). Its fields are
// the model's data list.
type PlacementPlanSuggested struct {
	shared.Header
	PlanID        ID
	SellerID      string
	CatalogItemID string
	Basis         string
	Suggestions   []Suggestion
	CreatedAt     time.Time
}

// EventName returns "PlacementPlanSuggested".
func (PlacementPlanSuggested) EventName() string { return "PlacementPlanSuggested" }

// Entity returns "placementplan".
func (PlacementPlanSuggested) Entity() string { return Entity }

// SubjectID returns the planId.
func (e PlacementPlanSuggested) SubjectID() string { return string(e.PlanID) }

var _ shared.Event = PlacementPlanSuggested{}
