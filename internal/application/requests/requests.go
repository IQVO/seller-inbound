// Package requests holds the inputs of the use cases: what a REST body or a
// consumed event carries once the adapter has decoded it. They are plain data;
// the domain validates the values.
package requests

import "time"

// OnboardSeller is command OnboardSeller.
type OnboardSeller struct {
	SellerID string
	Name     string
}

// SuspendSeller is command SuspendSeller; Reason is optional.
type SuspendSeller struct {
	SellerID string
	Reason   string
}

// PlanLine is one line of an inbound plan, in eaches. GTIN is as sent.
type PlanLine struct {
	CatalogItemID string
	GTIN          string
	QtyEaches     int
}

// CreateInboundPlan is command CreateInboundPlan.
type CreateInboundPlan struct {
	SellerID          string
	DestinationNodeID string
	PackingOption     string
	Lines             []PlanLine
}

// Content is one content line of a carton, in eaches.
type Content struct {
	CatalogItemID string
	GTIN          string
	QtyEaches     int
	Lot           string
	Expiry        string
}

// AddCarton is command AddCarton. SSCC and ParentSSCC are optional; Kind is
// optional (derived from the plan's packing option).
type AddCarton struct {
	ShipmentID   string
	SSCC         string
	ParentSSCC   string
	Kind         string
	Contents     []Content
	GrossWeightG int
	DimsL        int
	DimsW        int
	DimsH        int
}

// Dims is a size in millimetres.
type Dims struct{ L, W, H int }

// NodeInbound is the optional inbound block of a node profile. Zero values and
// nil pointers mean "not declared".
type NodeInbound struct {
	MaxCartonWeightG int
	MaxCartonDims    *Dims
	AcceptsPallets   *bool
	RequiresSscc     *bool
}

// NodeRegistered is the consumed node-topology NodeRegistered.
type NodeRegistered struct {
	NodeID   string
	NodeType string
}

// NodeProfileDeclared is the consumed NodeOperationalProfileDeclared. Inbound is
// nil when the revision declares no inbound limits.
type NodeProfileDeclared struct {
	NodeID          string
	ProfileRevision int
	Inbound         *NodeInbound
}

// NodeLifecycle is a consumed NodeActivated, NodeSuspended or NodeRetired.
type NodeLifecycle struct {
	NodeID string
}

// PackagingDeclared is the consumed CatalogItemPackagingDeclared.
type PackagingDeclared struct {
	CatalogItemID string
	Level         string
	GTIN          string
	ChildGTIN     string
	UnitsPerPack  int
	DeclaredAt    time.Time
}

// PackLevelRetired is the consumed CatalogItemPackLevelRetired.
type PackLevelRetired struct {
	CatalogItemID string
	Level         string
	GTIN          string
	RetiredAt     time.Time
}

// ReceiptItem is one counted GTIN of a received carton, in eaches.
type ReceiptItem struct {
	GTIN      string
	QtyEaches int
}

// ReceiptCarton is the vendor's answer for one carton.
type ReceiptCarton struct {
	SSCC            string
	Status          string
	Items           []ReceiptItem
	ComplianceCodes []string
}

// InboundReceipt is the consumed InboundReceiptSubmitted.
type InboundReceipt struct {
	ShipmentID string
	NodeID     string
	ReceivedAt time.Time
	Cartons    []ReceiptCarton
}
