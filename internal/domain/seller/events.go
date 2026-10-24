package seller

import "github.com/claudioed/seller-inbound/internal/domain/shared"

// Entity is the CloudEvents entity segment of the SellerAccount's events.
const Entity = "seller"

// SellerOnboarded is raised by Onboard.
type SellerOnboarded struct {
	shared.Header
	SellerID ID
	Name     string
}

// EventName returns "SellerOnboarded".
func (SellerOnboarded) EventName() string { return "SellerOnboarded" }

// Entity returns "seller".
func (SellerOnboarded) Entity() string { return Entity }

// SubjectID returns the sellerId.
func (e SellerOnboarded) SubjectID() string { return string(e.SellerID) }

// SellerSuspended is raised by Suspend.
type SellerSuspended struct {
	shared.Header
	SellerID ID
	Reason   string
}

// EventName returns "SellerSuspended".
func (SellerSuspended) EventName() string { return "SellerSuspended" }

// Entity returns "seller".
func (SellerSuspended) Entity() string { return Entity }

// SubjectID returns the sellerId.
func (e SellerSuspended) SubjectID() string { return string(e.SellerID) }

// SellerReinstated is raised by Reinstate.
type SellerReinstated struct {
	shared.Header
	SellerID ID
}

// EventName returns "SellerReinstated".
func (SellerReinstated) EventName() string { return "SellerReinstated" }

// Entity returns "seller".
func (SellerReinstated) Entity() string { return Entity }

// SubjectID returns the sellerId.
func (e SellerReinstated) SubjectID() string { return string(e.SellerID) }

var (
	_ shared.Event = SellerOnboarded{}
	_ shared.Event = SellerSuspended{}
	_ shared.Event = SellerReinstated{}
)
