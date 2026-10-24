package inboundplan

import "github.com/claudioed/seller-inbound/internal/domain/shared"

// Entity is the CloudEvents entity segment of the InboundPlan's events.
const Entity = "inboundplan"

// InboundPlanConfirmed is raised by Confirm. Its fields are the model's data list.
type InboundPlanConfirmed struct {
	shared.Header
	PlanID            ID
	SellerID          string
	DestinationNodeID string
	Packing           PackingOption
	Lines             []Line
}

// EventName returns "InboundPlanConfirmed".
func (InboundPlanConfirmed) EventName() string { return "InboundPlanConfirmed" }

// Entity returns "inboundplan".
func (InboundPlanConfirmed) Entity() string { return Entity }

// SubjectID returns the planId.
func (e InboundPlanConfirmed) SubjectID() string { return string(e.PlanID) }

var _ shared.Event = InboundPlanConfirmed{}
