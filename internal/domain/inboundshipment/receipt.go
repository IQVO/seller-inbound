package inboundshipment

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

// The discrepancy kinds this context derives from the vendor's carton status
// and counts. They are the fleet's carton discrepancy kind strings, passed
// through unchanged on InboundDiscrepancyRecorded (ADR 0009 of the model).
const (
	KindMissingCarton    = "MissingCarton"
	KindUnexpectedCarton = "UnexpectedCarton"
	KindDamaged          = "Damaged"
	KindShort            = "Short"
	KindOver             = "Over"
)

// CodeUnmanifestedCarton is the compliance code the context itself raises for a
// carton the vendor received that the manifest does not list.
const CodeUnmanifestedCarton = "UNMANIFESTED_CARTON"

// ReceiptItem is one counted GTIN of a received carton, in eaches.
type ReceiptItem struct {
	GTIN      string
	QtyEaches int
}

// ReceiptCarton is the vendor's answer for one carton.
type ReceiptCarton struct {
	SSCC            string
	Status          ReceiptStatus
	Items           []ReceiptItem
	ComplianceCodes []string
}

// Receipt is the vendor's carton-level answer (InboundReceiptSubmitted).
type Receipt struct {
	NodeID     string
	ReceivedAt time.Time
	Cartons    []ReceiptCarton
}

// RecordReceipt applies a vendor receipt. The first answer for a carton is
// final (a repeated one is ignored), a carton the manifest does not list is
// recorded as UNEXPECTED, and the shipment closes once every manifest carton
// has an answer: CLOSED when nothing differed, CLOSED_WITH_DISCREPANCY
// otherwise. Until then it is RECEIVING. A receipt for a closed shipment is a
// no-op, so a redelivered event changes nothing.
func (s *Shipment) RecordReceipt(r Receipt, now time.Time) ([]shared.Event, error) {
	if s.state == Closed || s.state == ClosedWithDiscrepancy {
		return nil, nil
	}
	if s.state != Shipped && s.state != Receiving {
		return nil, fmt.Errorf("%w: shipment is %s, a receipt needs a SHIPPED shipment", shared.ErrIllegalTransition, s.state)
	}
	if r.NodeID != s.nodeID {
		return nil, shared.Invalid("receipt nodeId %q is not the shipment's node", r.NodeID)
	}
	norm, err := normaliseReceipt(r.Cartons)
	if err != nil {
		return nil, err
	}
	at := now.UTC()
	var events []shared.Event
	for _, rc := range norm {
		events = append(events, s.apply(rc, at)...)
	}
	s.version++
	if !s.allAnswered() {
		s.state = Receiving
		return events, nil
	}
	s.state, s.receivedAt = Closed, r.ReceivedAt.UTC()
	if s.discrepant {
		s.state = ClosedWithDiscrepancy
	}
	return append(events, s.receivedEvent(at)), nil
}

func normaliseReceipt(in []ReceiptCarton) ([]ReceiptCarton, error) {
	out := make([]ReceiptCarton, 0, len(in))
	seen := map[string]bool{}
	for i, rc := range in {
		if _, err := gs1.ParseSSCC(rc.SSCC); err != nil {
			return nil, shared.Invalid("cartons[%d].sscc: %v", i, err)
		}
		if !rc.Status.Valid() {
			return nil, shared.Invalid("cartons[%d].status must be RECEIVED, MISSING, UNEXPECTED or DAMAGED", i)
		}
		if seen[rc.SSCC] {
			return nil, shared.Invalid("cartons[%d].sscc appears twice in the receipt", i)
		}
		seen[rc.SSCC] = true
		items := make([]ReceiptItem, len(rc.Items))
		for j, it := range rc.Items {
			g, err := gs1.ParseGTIN(it.GTIN)
			if err != nil || it.QtyEaches < 0 {
				return nil, shared.Invalid("cartons[%d].items[%d] is not a valid gtin and quantity", i, j)
			}
			items[j] = ReceiptItem{GTIN: g.String(), QtyEaches: it.QtyEaches}
		}
		out = append(out, ReceiptCarton{SSCC: rc.SSCC, Status: rc.Status, Items: items, ComplianceCodes: rc.ComplianceCodes})
	}
	return out, nil
}

// apply records one receipt carton and returns the facts it raises.
func (s *Shipment) apply(rc ReceiptCarton, at time.Time) []shared.Event {
	i := slices.IndexFunc(s.cartons, func(c Carton) bool { return c.SSCC == rc.SSCC })
	if i < 0 {
		return s.applyUnlisted(rc, at)
	}
	if s.cartons[i].Receipt != "" {
		return nil
	}
	s.cartons[i].Receipt = rc.Status
	events := s.statusDiscrepancy(s.cartons[i], at)
	if rc.Status == StatusReceived || rc.Status == StatusDamaged {
		events = append(events, s.quantityDiscrepancies(s.cartons[i], rc, at)...)
	}
	return append(events, s.violations(rc.SSCC, rc.ComplianceCodes, at)...)
}

// applyUnlisted records a carton the manifest does not list, once.
func (s *Shipment) applyUnlisted(rc ReceiptCarton, at time.Time) []shared.Event {
	if rc.Status == StatusMissing || slices.Contains(s.unexpected, rc.SSCC) || len(s.unexpected) >= maxUnexpected {
		return nil
	}
	s.unexpected = append(s.unexpected, rc.SSCC)
	s.discrepant = true
	events := []shared.Event{s.discrepancy(rc.SSCC, KindUnexpectedCarton, "", "", at)}
	codes := append([]string{CodeUnmanifestedCarton}, rc.ComplianceCodes...)
	return append(events, s.violations(rc.SSCC, codes, at)...)
}

const maxUnexpected = MaxCartons

func (s *Shipment) statusDiscrepancy(c Carton, at time.Time) []shared.Event {
	kind := ""
	switch c.Receipt {
	case StatusMissing:
		kind = KindMissingCarton
	case StatusUnexpected:
		kind = KindUnexpectedCarton
	case StatusDamaged:
		kind = KindDamaged
	case StatusReceived:
	}
	if kind == "" {
		return nil
	}
	s.discrepant = true
	return []shared.Event{s.discrepancy(c.SSCC, kind, "", "", at)}
}

// quantityDiscrepancies compares the counted GTINs with the manifest. A carton
// the vendor reported without any count is not compared.
func (s *Shipment) quantityDiscrepancies(c Carton, rc ReceiptCarton, at time.Time) []shared.Event {
	if len(rc.Items) == 0 {
		return nil
	}
	got := map[string]int{}
	for _, it := range rc.Items {
		got[it.GTIN] += it.QtyEaches
	}
	var events []shared.Event
	for _, ct := range c.Contents {
		if got[ct.GTIN] != ct.QtyEaches {
			events = append(events, s.quantityEvent(c.SSCC, ct.GTIN, ct.QtyEaches, got[ct.GTIN], at))
		}
		delete(got, ct.GTIN)
	}
	extra := make([]string, 0, len(got))
	for g := range got {
		extra = append(extra, g)
	}
	slices.Sort(extra)
	for _, g := range extra {
		if got[g] > 0 {
			events = append(events, s.quantityEvent(c.SSCC, g, 0, got[g], at))
		}
	}
	return events
}

func (s *Shipment) quantityEvent(sscc, gtin string, expected, actual int, at time.Time) shared.Event {
	s.discrepant = true
	kind := KindShort
	if actual > expected {
		kind = KindOver
	}
	return s.discrepancy(sscc, kind, gtin+":"+strconv.Itoa(expected), gtin+":"+strconv.Itoa(actual), at)
}

func (s *Shipment) discrepancy(sscc, kind, expected, actual string, at time.Time) shared.Event {
	return InboundDiscrepancyRecorded{
		Header: shared.Header{Version: s.version + 1, At: at}, ShipmentID: s.id, SellerID: s.sellerID, NodeID: s.nodeID,
		SSCC: sscc, Kind: kind, Expected: expected, Actual: actual,
	}
}

// violations raises one SellerComplianceViolationRecorded per distinct code.
func (s *Shipment) violations(sscc string, codes []string, at time.Time) []shared.Event {
	var events []shared.Event
	var done []string
	for _, code := range codes {
		if code == "" || slices.Contains(done, code) {
			continue
		}
		done = append(done, code)
		events = append(events, SellerComplianceViolationRecorded{
			Header: shared.Header{Version: s.version + 1, At: at}, SellerID: s.sellerID, ShipmentID: s.id, SSCC: sscc, Code: code,
		})
	}
	return events
}

func (s *Shipment) allAnswered() bool {
	return !slices.ContainsFunc(s.cartons, func(c Carton) bool { return c.Receipt == "" })
}

func (s *Shipment) receivedEvent(at time.Time) shared.Event {
	cartons := make([]ReceivedCarton, 0, len(s.cartons)+len(s.unexpected))
	for _, c := range s.cartons {
		cartons = append(cartons, ReceivedCarton{SSCC: c.SSCC, Status: c.Receipt})
	}
	for _, u := range s.unexpected {
		cartons = append(cartons, ReceivedCarton{SSCC: u, Status: StatusUnexpected})
	}
	return InboundShipmentReceived{
		Header: shared.Header{Version: s.version, At: at}, ShipmentID: s.id, SellerID: s.sellerID, NodeID: s.nodeID,
		Status: s.state, Cartons: cartons, ReceivedAt: s.receivedAt,
	}
}
