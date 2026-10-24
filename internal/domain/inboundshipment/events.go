package inboundshipment

import (
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// Entity segments of the CloudEvents type: the shipment's own events use
// "inboundshipment"; the compliance violation is a fact about the seller.
const (
	Entity       = "inboundshipment"
	SellerEntity = "seller"
)

// InboundShipmentLabelled is raised by Label. Cartons is the full manifest.
type InboundShipmentLabelled struct {
	shared.Header
	ShipmentID ID
	PlanID     string
	SellerID   string
	NodeID     string
	Cartons    []Carton
}

// EventName returns "InboundShipmentLabelled".
func (InboundShipmentLabelled) EventName() string { return "InboundShipmentLabelled" }

// Entity returns "inboundshipment".
func (InboundShipmentLabelled) Entity() string { return Entity }

// SubjectID returns the shipmentId.
func (e InboundShipmentLabelled) SubjectID() string { return string(e.ShipmentID) }

// InboundShipmentShipped is raised by Ship.
type InboundShipmentShipped struct {
	shared.Header
	ShipmentID ID
	PlanID     string
	SellerID   string
	NodeID     string
}

// EventName returns "InboundShipmentShipped".
func (InboundShipmentShipped) EventName() string { return "InboundShipmentShipped" }

// Entity returns "inboundshipment".
func (InboundShipmentShipped) Entity() string { return Entity }

// SubjectID returns the shipmentId.
func (e InboundShipmentShipped) SubjectID() string { return string(e.ShipmentID) }

// ReceivedCarton is one carton of the closing event.
type ReceivedCarton struct {
	SSCC   string
	Status ReceiptStatus
}

// InboundShipmentReceived is raised by RecordReceipt when the shipment closes.
type InboundShipmentReceived struct {
	shared.Header
	ShipmentID ID
	SellerID   string
	NodeID     string
	Status     State
	Cartons    []ReceivedCarton
	ReceivedAt time.Time
}

// EventName returns "InboundShipmentReceived".
func (InboundShipmentReceived) EventName() string { return "InboundShipmentReceived" }

// Entity returns "inboundshipment".
func (InboundShipmentReceived) Entity() string { return Entity }

// SubjectID returns the shipmentId.
func (e InboundShipmentReceived) SubjectID() string { return string(e.ShipmentID) }

// InboundDiscrepancyRecorded is raised per difference between the manifest and
// the vendor's receipt. Kind is the fleet's carton discrepancy kind string.
// SSCC, Expected and Actual are optional (empty = omitted).
type InboundDiscrepancyRecorded struct {
	shared.Header
	ShipmentID ID
	SellerID   string
	NodeID     string
	SSCC       string
	Kind       string
	Expected   string
	Actual     string
}

// EventName returns "InboundDiscrepancyRecorded".
func (InboundDiscrepancyRecorded) EventName() string { return "InboundDiscrepancyRecorded" }

// Entity returns "inboundshipment".
func (InboundDiscrepancyRecorded) Entity() string { return Entity }

// SubjectID returns the shipmentId.
func (e InboundDiscrepancyRecorded) SubjectID() string { return string(e.ShipmentID) }

// SellerComplianceViolationRecorded is raised per compliance code of a receipt.
// It is a fact about the seller: its subject is the sellerId. SSCC is the carton
// the code was reported on; the wire payload does not carry it.
type SellerComplianceViolationRecorded struct {
	shared.Header
	SellerID   string
	ShipmentID ID
	SSCC       string
	Code       string
}

// EventName returns "SellerComplianceViolationRecorded".
func (SellerComplianceViolationRecorded) EventName() string {
	return "SellerComplianceViolationRecorded"
}

// Entity returns "seller".
func (SellerComplianceViolationRecorded) Entity() string { return SellerEntity }

// SubjectID returns the sellerId.
func (e SellerComplianceViolationRecorded) SubjectID() string { return e.SellerID }

var (
	_ shared.Event = InboundShipmentLabelled{}
	_ shared.Event = InboundShipmentShipped{}
	_ shared.Event = InboundShipmentReceived{}
	_ shared.Event = InboundDiscrepancyRecorded{}
	_ shared.Event = SellerComplianceViolationRecorded{}
)
