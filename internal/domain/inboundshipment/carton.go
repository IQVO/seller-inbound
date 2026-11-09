package inboundshipment

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

// Sentinel errors of the shipment invariants (model section 4). Each has a
// stable problem slug in the HTTP adapter.
var (
	// ErrCasePackNotUniform: in a CASE_PACKED plan a carton holds only one item, and every
	// carton of that item holds the same quantity (problem type case-pack-not-uniform, 422).
	ErrCasePackNotUniform = errors.New("case pack not uniform")
	// ErrCaseQtyMismatchesGtin: a HOMOGENEOUS_CASE carton's quantity differs from the
	// unitsPerPack of the active CASE level of its gtin (case-qty-mismatches-gtin, 422).
	ErrCaseQtyMismatchesGtin = errors.New("case quantity mismatches the case gtin")
	// ErrCartonOverweight: gross weight above the node's maxCartonWeightG (carton-overweight, 422).
	ErrCartonOverweight = errors.New("carton overweight")
	// ErrCartonOversize: dimensions above the node's maxCartonDimsMm (carton-oversize, 422).
	ErrCartonOversize = errors.New("carton oversize")
	// ErrSsccNotUnique: the SSCC is already used network-wide (sscc-not-unique, 409).
	ErrSsccNotUnique = errors.New("sscc not unique")
	// ErrInvalidSscc: not 18 digits or a wrong mod-10 check digit (invalid-sscc, 422).
	ErrInvalidSscc = errors.New("invalid sscc")
	// ErrCartonContentsMismatchPlan: carton contents do not fit the plan line (carton-contents-mismatch-plan, 422).
	ErrCartonContentsMismatchPlan = errors.New("carton contents mismatch the plan")
	// ErrShipmentNotLabelled: a shipment that is not labelled cannot ship (shipment-not-labelled, 409).
	ErrShipmentNotLabelled = errors.New("shipment not labelled")
	// ErrNodeCannotReceive: the destination node is not ACTIVE, lacks the INBOUND capability,
	// or does not accept pallets (node-cannot-receive, 409). It is the network view's sentinel.
	ErrNodeCannotReceive = network.ErrNodeCannotReceive
)

// Kind says what a carton holds.
type Kind string

// The two kinds of carton.
const (
	// HomogeneousCase is a master carton: one item at the quantity of its CASE level.
	HomogeneousCase Kind = "HOMOGENEOUS_CASE"
	// Mixed is a logistic unit with no pack GTIN, identified by its SSCC and manifest only.
	Mixed Kind = "MIXED"
)

// ReceiptStatus is the vendor's per-carton answer. The zero value is "not answered yet".
type ReceiptStatus string

// The four receipt statuses of the vendor's carton-level answer.
const (
	StatusReceived   ReceiptStatus = "RECEIVED"
	StatusMissing    ReceiptStatus = "MISSING"
	StatusUnexpected ReceiptStatus = "UNEXPECTED"
	StatusDamaged    ReceiptStatus = "DAMAGED"
)

// Valid reports whether r is one of the four statuses.
func (r ReceiptStatus) Valid() bool {
	return r == StatusReceived || r == StatusMissing || r == StatusUnexpected || r == StatusDamaged
}

// Dims is the outer size of a carton in millimetres.
type Dims struct{ L, W, H int }

// Content is one item line of a carton, in eaches. GTIN is the 14-digit form.
type Content struct {
	CatalogItemID string
	GTIN          string
	QtyEaches     int
	Lot           string
	// Expiry is an ISO date (YYYY-MM-DD) or empty.
	Expiry string
}

// Carton is one logistic unit of a shipment.
type Carton struct {
	SSCC         string
	ParentSSCC   string
	Kind         Kind
	Contents     []Content
	GrossWeightG int
	Dims         Dims
	Sealed       bool
	// Receipt is the vendor's answer for this carton; empty until answered.
	Receipt ReceiptStatus
	// Counted is the vendor's counted items of its first answer, in eaches;
	// empty when the vendor reported no count (ADR 0014).
	Counted []ReceiptItem
}

// TotalEaches is the sum of the contents.
func (c Carton) TotalEaches() int {
	n := 0
	for _, ct := range c.Contents {
		n += ct.QtyEaches
	}
	return n
}

func cloneCartons(in []Carton) []Carton {
	out := make([]Carton, len(in))
	for i, c := range in {
		out[i] = c
		out[i].Contents = append([]Content(nil), c.Contents...)
		out[i].Counted = append([]ReceiptItem(nil), c.Counted...)
	}
	return out
}

// check validates a persisted carton (Restore).
func (c Carton) check() error {
	if _, err := gs1.ParseSSCC(c.SSCC); err != nil {
		return errors.New("sscc is invalid")
	}
	if c.Kind != HomogeneousCase && c.Kind != Mixed {
		return errors.New("kind must be HOMOGENEOUS_CASE or MIXED")
	}
	if c.Receipt != "" && !c.Receipt.Valid() {
		return errors.New("receipt status is invalid")
	}
	if err := checkCounted(c); err != nil {
		return err
	}
	return checkContents(c.Contents)
}

func checkCounted(c Carton) error {
	if len(c.Counted) > 0 && c.Receipt == "" {
		return errors.New("counted items need a receipt status")
	}
	for i, it := range c.Counted {
		if g, err := gs1.ParseGTIN(it.GTIN); err != nil || g.String() != it.GTIN || it.QtyEaches < 0 || it.QtyEaches > MaxQtyEaches {
			return fmt.Errorf("counted[%d] is not a valid 14-digit gtin and quantity", i)
		}
	}
	return nil
}

func checkContents(contents []Content) error {
	if len(contents) == 0 || len(contents) > MaxContents {
		return fmt.Errorf("a carton has between 1 and %d content lines", MaxContents)
	}
	seen := map[string]bool{}
	for i, ct := range contents {
		if seen[ct.CatalogItemID] {
			return fmt.Errorf("contents[%d].catalogItemId appears twice", i)
		}
		seen[ct.CatalogItemID] = true
		if err := ct.check(); err != nil {
			return fmt.Errorf("contents[%d].%w", i, err)
		}
	}
	return nil
}

func (ct Content) check() error {
	if !shared.ValidID(ct.CatalogItemID) {
		return errors.New("catalogItemId is not a valid id")
	}
	if g, err := gs1.ParseGTIN(ct.GTIN); err != nil || g.String() != ct.GTIN {
		return errors.New("gtin is not a valid 14-digit GTIN")
	}
	if ct.QtyEaches < 1 || ct.QtyEaches > MaxQtyEaches {
		return fmt.Errorf("qtyEaches must be between 1 and %d", MaxQtyEaches)
	}
	if ct.Lot != "" && !shared.ValidTextN(ct.Lot, MaxLot) {
		return fmt.Errorf("lot must be trimmed and at most %d characters", MaxLot)
	}
	if ct.Expiry != "" {
		if _, err := time.Parse(isoDateLayout, ct.Expiry); err != nil {
			return errors.New("expiry must be an ISO date")
		}
	}
	return nil
}
