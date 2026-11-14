// Package views holds the answers of the queries and commands: plain data the
// HTTP adapter renders. No domain type leaks to the wire.
package views

import "time"

// Seller is a seller account.
type Seller struct {
	SellerID      string
	Name          string
	State         string
	OnboardedAt   time.Time
	SuspendedAt   time.Time
	SuspendReason string
}

// PlanLine is one line of a plan.
type PlanLine struct {
	CatalogItemID string
	GTIN          string
	QtyEaches     int
	// UnitVolumeDm3 is the seller-declared volume of one each in dm3; 0 =
	// undeclared (ADR 0015).
	UnitVolumeDm3 int
}

// RedistributionLine is one (node, line) share of a NETWORK_SPLIT plan's
// recorded redistribution (ADR 0015).
type RedistributionLine struct {
	NodeID    string
	LineNo    int
	QtyEaches int
}

// FeeBasis is the recorded placement fee basis of a NETWORK_SPLIT plan: a
// fact for a later vendor-settlement context, never a charge (ADR 0015).
type FeeBasis struct {
	Basis   string
	Month   string
	Dm3     int64
	NodeIDs []string
}

// Plan is an inbound plan.
type Plan struct {
	PlanID            string
	SellerID          string
	DestinationNodeID string
	PackingOption     string
	// PlacementOption is SELLER_SPLIT (default) or NETWORK_SPLIT (ADR 0015).
	PlacementOption string
	State           string
	Lines           []PlanLine
	// Redistribution and PlacementFeeBasis are the recorded NETWORK_SPLIT
	// facts; empty/nil for SELLER_SPLIT (ADR 0015).
	Redistribution    []RedistributionLine
	PlacementFeeBasis *FeeBasis
	// CapacityEnforced reports whether the monthly capacity gate was applied
	// (both the node limit and every line volume known, ADR 0015).
	CapacityEnforced bool
	CreatedAt        time.Time
	ConfirmedAt      time.Time
	CancelledAt      time.Time
}

// Content is one content line of a carton.
type Content struct {
	CatalogItemID string
	GTIN          string
	QtyEaches     int
	Lot           string
	Expiry        string
}

// Carton is one carton of a shipment.
type Carton struct {
	SSCC          string
	ParentSSCC    string
	Kind          string
	Contents      []Content
	GrossWeightG  int
	DimsL         int
	DimsW         int
	DimsH         int
	Sealed        bool
	ReceiptStatus string
}

// Shipment is an inbound shipment with its cartons.
type Shipment struct {
	ShipmentID string
	PlanID     string
	SellerID   string
	NodeID     string
	State      string
	Cartons    []Carton
	CreatedAt  time.Time
	LabelledAt time.Time
	ShippedAt  time.Time
	ReceivedAt time.Time
}

// AddedCarton answers AddCarton: the stored carton and whether the network
// issued its SSCC (the label data is the same as GetLabels returns).
type AddedCarton struct {
	ShipmentID string
	Carton     Carton
	Issued     bool
	Label      Label
}

// Label is the printable data of one carton. The service renders no ZPL or PDF:
// the client prints GS1128 as a GS1-128 symbol.
type Label struct {
	LabelRef          string
	SSCC              string
	GS1128            string
	HumanReadable     string
	ParentSSCC        string
	Kind              string
	Contents          []Content
	ContentSummary    string
	TotalEaches       int
	DestinationNodeID string
}

// Labels is the label data of a shipment, one per carton.
type Labels struct {
	ShipmentID        string
	State             string
	DestinationNodeID string
	Labels            []Label
}

// SellerScorecard is the informational compliance scorecard of a seller over a
// rolling window. A rate is nil when its denominator is zero.
type SellerScorecard struct {
	SellerID                   string
	Window                     string
	WindowStart                time.Time
	WindowEnd                  time.Time
	ShipmentsReceived          int
	CartonsReported            int
	CartonsReceived            int
	ReceiptAccuracyRate        *float64
	DiscrepancyRatePerShipment *float64
	DiscrepanciesByKind        map[string]int
	ComplianceViolations       int
	ComplianceTier             string
}

// PlacementSuggestion is one node's share of a placement plan's region target,
// with the evidence that produced it (ADR 0012).
type PlacementSuggestion struct {
	NodeID                string
	RegionID              string
	SuggestedUnits        int
	RegionDemandUnits     int
	NodeAvailabilityUnits int
	CoverDays             int
}

// PlacementPlan is the placement advisory plan of one seller and item.
type PlacementPlan struct {
	PlanID        string
	SellerID      string
	CatalogItemID string
	CoverDays     int
	WindowDays    int
	Basis         string
	State         string
	Suggestions   []PlacementSuggestion
	CreatedAt     time.Time
	AcceptedAt    time.Time
	SupersededAt  time.Time
}

// CapacityAllocation is one row of the reservation table of a seller's month
// (ADR 0015): the dm3 the seller's DRAFT and CONFIRMED plans promise to one
// node.
type CapacityAllocation struct {
	NodeID      string
	ReservedDm3 int64
	UpdatedAt   time.Time
}

// CapacityAllocations answers GET /sellers/{sellerId}/capacity-allocations.
type CapacityAllocations struct {
	SellerID string
	Month    string
	Items    []CapacityAllocation
}
