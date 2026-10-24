package inboundshipment

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
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
	return checkContents(c.Contents)
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

// Limits are the destination node's declared inbound limits. Zero values mean
// "not declared": the limit is unchecked.
type Limits struct {
	MaxCartonWeightG int
	MaxCartonDims    *Dims
	// AcceptsPallets is nil when the node declares nothing about pallets.
	AcceptsPallets *bool
}

// PlanRules are the plan facts a shipment cannot know by itself; the use case
// reads them from the plan, its other shipments and the local views.
type PlanRules struct {
	Packing inboundplan.PackingOption
	// PlanQty is the planned eaches per catalogItemId.
	PlanQty map[string]int
	// OtherQty is the eaches the plan's OTHER shipments already carry per catalogItemId.
	OtherQty map[string]int
	// SingleShipment is true when this is the only shipment of the plan: its cartons
	// must then add up to the plan lines exactly when it is labelled.
	SingleShipment bool
}

// CartonRules adds the node limits and the packaging view to PlanRules for AddCarton.
type CartonRules struct {
	PlanRules
	Limits Limits
	// CaseUnits returns unitsPerPack of the ACTIVE CASE level of the catalogue item
	// whose gtin is given, and whether there is one.
	CaseUnits func(catalogItemID, gtin string) (int, bool)
}

// NewCarton is the input of AddCarton. SSCC is always set: the use case issues
// one from the network's prefix when the seller supplied none. Kind may be empty
// (derived from the plan's packing option). Contents carry the GTIN as sent.
type NewCarton struct {
	SSCC         string
	ParentSSCC   string
	Kind         Kind
	Contents     []Content
	GrossWeightG int
	Dims         Dims
}

// AddCarton adds a carton to a DRAFT shipment, enforcing the carton invariants.
// It raises no event; the cartons are published when the shipment is labelled.
func (s *Shipment) AddCarton(in NewCarton, r CartonRules) (Carton, error) {
	if s.state != Draft {
		return Carton{}, fmt.Errorf("%w: shipment is %s, cartons are added to a DRAFT shipment", shared.ErrIllegalTransition, s.state)
	}
	if len(s.cartons) >= MaxCartons {
		return Carton{}, shared.Invalid("a shipment holds at most %d cartons", MaxCartons)
	}
	c, err := s.buildCarton(in, r)
	if err != nil {
		return Carton{}, err
	}
	if err := s.checkAgainstPlan(c, r); err != nil {
		return Carton{}, err
	}
	s.cartons = append(s.cartons, c)
	s.version++
	return cloneCartons([]Carton{c})[0], nil
}

func (s *Shipment) buildCarton(in NewCarton, r CartonRules) (Carton, error) {
	if err := s.checkSSCCs(in, r.Limits); err != nil {
		return Carton{}, err
	}
	contents, err := normaliseContents(in.Contents)
	if err != nil {
		return Carton{}, err
	}
	kind, err := resolveKind(in.Kind, r.Packing, len(contents))
	if err != nil {
		return Carton{}, err
	}
	if in.GrossWeightG < 1 || in.GrossWeightG > MaxGrossWeightG {
		return Carton{}, shared.Invalid("grossWeightG must be between 1 and %d", MaxGrossWeightG)
	}
	if err := checkDims(in.Dims); err != nil {
		return Carton{}, err
	}
	if err := checkLimits(in, r.Limits); err != nil {
		return Carton{}, err
	}
	return Carton{SSCC: in.SSCC, ParentSSCC: in.ParentSSCC, Kind: kind, Contents: contents,
		GrossWeightG: in.GrossWeightG, Dims: in.Dims}, nil
}

func (s *Shipment) checkSSCCs(in NewCarton, lim Limits) error {
	if _, err := gs1.ParseSSCC(in.SSCC); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSscc, err)
	}
	for _, c := range s.cartons {
		if c.SSCC == in.SSCC {
			return fmt.Errorf("%w: %s is already in this shipment", ErrSsccNotUnique, in.SSCC)
		}
	}
	if in.ParentSSCC == "" {
		return nil
	}
	if _, err := gs1.ParseSSCC(in.ParentSSCC); err != nil {
		return fmt.Errorf("%w: parentSscc: %v", ErrInvalidSscc, err)
	}
	if in.ParentSSCC == in.SSCC {
		return shared.Invalid("parentSscc must differ from sscc")
	}
	if lim.AcceptsPallets != nil && !*lim.AcceptsPallets {
		return fmt.Errorf("%w: the node does not accept pallets", ErrNodeCannotReceive)
	}
	return nil
}

func normaliseContents(in []Content) ([]Content, error) {
	out := make([]Content, len(in))
	for i, ct := range in {
		g, err := gs1.ParseGTIN(ct.GTIN)
		if err == nil {
			err = gs1.RejectVariableMeasure(g)
		}
		if err != nil {
			return nil, shared.Invalid("contents[%d].gtin: %v", i, err)
		}
		out[i] = ct
		out[i].GTIN = g.String()
	}
	if err := checkContents(out); err != nil {
		return nil, shared.Invalid("%v", err)
	}
	return out, nil
}

// resolveKind derives the carton kind from the plan's packing option: a
// CASE_PACKED plan holds one item per carton, an INDIVIDUAL plan packs freely.
func resolveKind(asked Kind, packing inboundplan.PackingOption, items int) (Kind, error) {
	if packing == inboundplan.CasePacked {
		if asked == Mixed || items > 1 {
			return "", fmt.Errorf("%w: a CASE_PACKED carton holds one item only", ErrCasePackNotUniform)
		}
		return HomogeneousCase, nil
	}
	if asked != "" && asked != Mixed {
		return "", shared.Invalid("kind %s needs a CASE_PACKED plan", asked)
	}
	return Mixed, nil
}

func checkDims(d Dims) error {
	for _, e := range []int{d.L, d.W, d.H} {
		if e < 1 || e > MaxDimMm {
			return shared.Invalid("dimsMm l, w and h must be between 1 and %d", MaxDimMm)
		}
	}
	return nil
}

func checkLimits(in NewCarton, lim Limits) error {
	if lim.MaxCartonWeightG > 0 && in.GrossWeightG > lim.MaxCartonWeightG {
		return fmt.Errorf("%w: %d g is above the node limit of %d g", ErrCartonOverweight, in.GrossWeightG, lim.MaxCartonWeightG)
	}
	if lim.MaxCartonDims != nil && !fits(in.Dims, *lim.MaxCartonDims) {
		return fmt.Errorf("%w: %dx%dx%d mm does not fit %dx%dx%d mm", ErrCartonOversize,
			in.Dims.L, in.Dims.W, in.Dims.H, lim.MaxCartonDims.L, lim.MaxCartonDims.W, lim.MaxCartonDims.H)
	}
	return nil
}

// fits reports whether a carton fits the limit box in some orientation: the
// sorted edges are compared pairwise.
func fits(c, limit Dims) bool {
	ce, le := []int{c.L, c.W, c.H}, []int{limit.L, limit.W, limit.H}
	slices.Sort(ce)
	slices.Sort(le)
	return ce[0] <= le[0] && ce[1] <= le[1] && ce[2] <= le[2]
}

func (s *Shipment) checkAgainstPlan(c Carton, r CartonRules) error {
	have := s.qtyByItem()
	for _, ct := range c.Contents {
		planned, onPlan := r.PlanQty[ct.CatalogItemID]
		if !onPlan {
			return fmt.Errorf("%w: item %q is not on the plan", ErrCartonContentsMismatchPlan, ct.CatalogItemID)
		}
		if have[ct.CatalogItemID]+r.OtherQty[ct.CatalogItemID]+ct.QtyEaches > planned {
			return fmt.Errorf("%w: item %q would exceed the plan line of %d eaches", ErrCartonContentsMismatchPlan, ct.CatalogItemID, planned)
		}
	}
	if r.Packing != inboundplan.CasePacked {
		return nil
	}
	ct := c.Contents[0]
	if err := s.checkUniform(ct); err != nil {
		return err
	}
	units, ok := r.CaseUnits(ct.CatalogItemID, ct.GTIN)
	if !ok {
		return fmt.Errorf("%w: gtin %s is not an active CASE level of item %q", ErrCaseQtyMismatchesGtin, ct.GTIN, ct.CatalogItemID)
	}
	if units != ct.QtyEaches {
		return fmt.Errorf("%w: carton holds %d eaches, the case gtin %s declares %d", ErrCaseQtyMismatchesGtin, ct.QtyEaches, ct.GTIN, units)
	}
	return nil
}

func (s *Shipment) checkUniform(ct Content) error {
	for _, c := range s.cartons {
		for _, o := range c.Contents {
			if o.CatalogItemID == ct.CatalogItemID && o.QtyEaches != ct.QtyEaches {
				return fmt.Errorf("%w: item %q is already packed at %d eaches per carton, not %d",
					ErrCasePackNotUniform, ct.CatalogItemID, o.QtyEaches, ct.QtyEaches)
			}
		}
	}
	return nil
}
