package scorecard

import (
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// OccurrenceType tells which published fact an Occurrence restates.
type OccurrenceType string

// The two occurrence types.
const (
	// Discrepancy restates InboundDiscrepancyRecorded; Code is its kind.
	Discrepancy OccurrenceType = "DISCREPANCY"
	// Violation restates SellerComplianceViolationRecorded; Code is its code.
	Violation OccurrenceType = "VIOLATION"
)

// Receipt restates InboundShipmentReceived: one closed shipment. At is the
// instant this service recorded the closing (the event's occurrence), not the
// receivedAt the vendor claims, so the window rests on our own clock.
type Receipt struct {
	SellerID        string
	ShipmentID      string
	CartonsReported int
	CartonsReceived int
	At              time.Time
}

// Occurrence is one discrepancy or one compliance violation of a seller.
type Occurrence struct {
	SellerID   string
	ShipmentID string
	Type       OccurrenceType
	Code       string
	At         time.Time
}

// Facts are the scorecard facts raised by one command.
type Facts struct {
	Receipts    []Receipt
	Occurrences []Occurrence
}

// Empty reports whether there is nothing to record.
func (f Facts) Empty() bool { return len(f.Receipts) == 0 && len(f.Occurrences) == 0 }

// FactsFrom extracts the scorecard facts from the events a receipt raised, in
// order. Events that are not scorecard facts are ignored. The facts are the
// very events the outbox publishes, so the scorecard and the stream cannot
// disagree.
func FactsFrom(events []shared.Event) Facts {
	var f Facts
	for _, ev := range events {
		switch e := ev.(type) {
		case inboundshipment.InboundShipmentReceived:
			f.Receipts = append(f.Receipts, receiptOf(e))
		case inboundshipment.InboundDiscrepancyRecorded:
			f.Occurrences = append(f.Occurrences, Occurrence{
				SellerID: e.SellerID, ShipmentID: string(e.ShipmentID), Type: Discrepancy, Code: e.Kind, At: e.OccurredAt(),
			})
		case inboundshipment.SellerComplianceViolationRecorded:
			f.Occurrences = append(f.Occurrences, Occurrence{
				SellerID: e.SellerID, ShipmentID: string(e.ShipmentID), Type: Violation, Code: e.Code, At: e.OccurredAt(),
			})
		}
	}
	return f
}

func receiptOf(e inboundshipment.InboundShipmentReceived) Receipt {
	received := 0
	for _, c := range e.Cartons {
		if c.Status == inboundshipment.StatusReceived {
			received++
		}
	}
	return Receipt{
		SellerID: e.SellerID, ShipmentID: string(e.ShipmentID),
		CartonsReported: len(e.Cartons), CartonsReceived: received, At: e.OccurredAt(),
	}
}
