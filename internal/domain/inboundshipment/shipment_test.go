package inboundshipment

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func mustGTIN(t *testing.T, payload string) string {
	t.Helper()
	g, err := gs1.NewGTIN(payload)
	if err != nil {
		t.Fatal(err)
	}
	return g.String()
}

func mustSSCC(t *testing.T, serial int) string {
	t.Helper()
	p := "0061414100000000"
	n := []byte(p + "0")
	for i := len(n) - 1; serial > 0 && i >= 0; i-- {
		n[i] = byte('0' + serial%10)
		serial /= 10
	}
	s, err := gs1.NewSSCC(string(n))
	if err != nil {
		t.Fatal(err)
	}
	return s.String()
}

func newShipment(t *testing.T) *Shipment {
	t.Helper()
	s, err := Create(NewShipment{ID: "shp-1", PlanID: "pln-1", SellerID: "seller-1", NodeID: "node-1"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func caseRules(units int, gtin string) CartonRules {
	return CartonRules{
		PlanRules: PlanRules{Packing: inboundplan.CasePacked, PlanQty: map[string]int{"item-1": 120, "item-2": 50}, SingleShipment: true},
		CaseUnits: func(item, g string) (int, bool) { return units, item == "item-1" && g == gtin },
	}
}

func looseRules() CartonRules {
	return CartonRules{
		PlanRules: PlanRules{Packing: inboundplan.Individual, PlanQty: map[string]int{"item-1": 120, "item-2": 50}, SingleShipment: true},
		CaseUnits: func(string, string) (int, bool) { return 0, false },
	}
}

func carton(t *testing.T, serial int, item, gtin string, qty int) NewCarton {
	t.Helper()
	return NewCarton{SSCC: mustSSCC(t, serial), Contents: []Content{{CatalogItemID: item, GTIN: gtin, QtyEaches: qty}},
		GrossWeightG: 5000, Dims: Dims{L: 400, W: 300, H: 200}}
}

func TestCreateValidatesIdentity(t *testing.T) {
	bad := []NewShipment{
		{ID: "", PlanID: "p", SellerID: "s", NodeID: "n"},
		{ID: "a", PlanID: "", SellerID: "s", NodeID: "n"},
		{ID: "a", PlanID: "p", SellerID: "s s", NodeID: "n"},
		{ID: "a", PlanID: "p", SellerID: "s", NodeID: ""},
	}
	for _, in := range bad {
		if _, err := Create(in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("Create(%+v) = %v, want ErrValidation", in, err)
		}
	}
	s := newShipment(t)
	if s.State() != Draft || s.Version() != 1 || s.ID() != "shp-1" || s.PlanID() != "pln-1" || s.SellerID() != "seller-1" || s.NodeID() != "node-1" {
		t.Fatalf("unexpected new shipment %+v", s.Snapshot())
	}
}

func TestAddCartonCasePackedHappyPath(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	c, err := s.AddCarton(carton(t, 1, "item-1", g, 12), caseRules(12, g))
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != HomogeneousCase || c.Sealed || c.TotalEaches() != 12 || s.Version() != 2 || len(s.Cartons()) != 1 {
		t.Fatalf("unexpected carton %+v version %d", c, s.Version())
	}
}

func TestAddCartonCaseRules(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	other := mustGTIN(t, "0614141000029")
	tests := []struct {
		name  string
		carts []NewCarton
		rules CartonRules
		want  error
	}{
		{"qty differs from unitsPerPack", []NewCarton{carton(t, 1, "item-1", g, 10)}, caseRules(12, g), ErrCaseQtyMismatchesGtin},
		{"gtin is not a CASE level", []NewCarton{carton(t, 1, "item-1", other, 12)}, caseRules(12, g), ErrCaseQtyMismatchesGtin},
		{"two items in one case-packed carton", []NewCarton{{SSCC: mustSSCC(t, 1), GrossWeightG: 10, Dims: Dims{1, 1, 1},
			Contents: []Content{{CatalogItemID: "item-1", GTIN: g, QtyEaches: 12}, {CatalogItemID: "item-2", GTIN: other, QtyEaches: 12}}}}, caseRules(12, g), ErrCasePackNotUniform},
		{"explicit MIXED kind in a case-packed plan", []NewCarton{func() NewCarton { c := carton(t, 1, "item-1", g, 12); c.Kind = Mixed; return c }()}, caseRules(12, g), ErrCasePackNotUniform},
		{"same item at another quantity", []NewCarton{carton(t, 1, "item-1", g, 12), carton(t, 2, "item-1", g, 24)}, caseRules(12, g), ErrCasePackNotUniform},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newShipment(t)
			var err error
			for _, c := range tc.carts {
				_, err = s.AddCarton(c, tc.rules)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAddCartonSsccRules(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	if _, err := s.AddCarton(carton(t, 1, "item-1", g, 3), looseRules()); err != nil {
		t.Fatal(err)
	}
	dup := carton(t, 1, "item-1", g, 3)
	if _, err := s.AddCarton(dup, looseRules()); !errors.Is(err, ErrSsccNotUnique) {
		t.Fatalf("duplicate: %v", err)
	}
	valid := mustSSCC(t, 9)
	bad := []string{"", "12345", valid[:17], valid[:17] + "x", valid[:17] + string(rune('0'+(int(valid[17]-'0')+1)%10)), valid + "0"}
	for _, sscc := range bad {
		c := carton(t, 2, "item-1", g, 3)
		c.SSCC = sscc
		if _, err := s.AddCarton(c, looseRules()); !errors.Is(err, ErrInvalidSscc) {
			t.Errorf("sscc %q: %v, want ErrInvalidSscc", sscc, err)
		}
	}
	if len(s.Cartons()) != 1 || s.Version() != 2 {
		t.Fatalf("rejected cartons changed the shipment: %d cartons v%d", len(s.Cartons()), s.Version())
	}
}

func TestAddCartonPalletParent(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	no, yes := false, true
	child := func() NewCarton { c := carton(t, 1, "item-1", g, 3); c.ParentSSCC = mustSSCC(t, 100); return c }
	r := looseRules()
	r.Limits.AcceptsPallets = &no
	if _, err := newShipment(t).AddCarton(child(), r); !errors.Is(err, ErrNodeCannotReceive) {
		t.Fatalf("pallet refused: %v", err)
	}
	for name, ok := range map[string]*bool{"accepts": &yes, "undeclared": nil} {
		r.Limits.AcceptsPallets = ok
		if _, err := newShipment(t).AddCarton(child(), r); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := child()
	bad.ParentSSCC = bad.SSCC
	if _, err := newShipment(t).AddCarton(bad, r); !errors.Is(err, shared.ErrValidation) {
		t.Errorf("parent == self: %v", err)
	}
	bad.ParentSSCC = "123"
	if _, err := newShipment(t).AddCarton(bad, r); !errors.Is(err, ErrInvalidSscc) {
		t.Errorf("parent invalid: %v", err)
	}
}

func TestAddCartonNodeLimitBoundaries(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	tests := []struct {
		name   string
		weight int
		dims   Dims
		lim    Limits
		want   error
	}{
		{"weight at the limit", 1000, Dims{10, 10, 10}, Limits{MaxCartonWeightG: 1000}, nil},
		{"weight one over", 1001, Dims{10, 10, 10}, Limits{MaxCartonWeightG: 1000}, ErrCartonOverweight},
		{"no weight limit declared", 999999, Dims{10, 10, 10}, Limits{}, nil},
		{"dims at the limit", 10, Dims{400, 300, 200}, Limits{MaxCartonDims: &Dims{400, 300, 200}}, nil},
		{"dims rotated fit", 10, Dims{200, 400, 300}, Limits{MaxCartonDims: &Dims{400, 300, 200}}, nil},
		{"longest edge one over", 10, Dims{401, 300, 200}, Limits{MaxCartonDims: &Dims{400, 300, 200}}, ErrCartonOversize},
		{"middle edge one over", 10, Dims{400, 301, 200}, Limits{MaxCartonDims: &Dims{400, 300, 200}}, ErrCartonOversize},
		{"shortest edge one over", 10, Dims{400, 300, 201}, Limits{MaxCartonDims: &Dims{400, 300, 200}}, ErrCartonOversize},
		{"no dims limit declared", 10, Dims{9999, 9999, 9999}, Limits{}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := carton(t, 1, "item-1", g, 3)
			c.GrossWeightG, c.Dims = tc.weight, tc.dims
			r := looseRules()
			r.Limits = tc.lim
			_, err := newShipment(t).AddCarton(c, r)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAddCartonValueValidation(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	mut := map[string]func(*NewCarton){
		"no contents":        func(c *NewCarton) { c.Contents = nil },
		"zero qty":           func(c *NewCarton) { c.Contents[0].QtyEaches = 0 },
		"huge qty":           func(c *NewCarton) { c.Contents[0].QtyEaches = MaxQtyEaches + 1 },
		"bad gtin":           func(c *NewCarton) { c.Contents[0].GTIN = "12345678901234" },
		"variable measure":   func(c *NewCarton) { c.Contents[0].GTIN = mustGTIN(t, "9614141000012") },
		"bad item id":        func(c *NewCarton) { c.Contents[0].CatalogItemID = "" },
		"duplicate item":     func(c *NewCarton) { c.Contents = append(c.Contents, c.Contents[0]) },
		"long lot":           func(c *NewCarton) { c.Contents[0].Lot = strings.Repeat("L", MaxLot+1) },
		"bad expiry":         func(c *NewCarton) { c.Contents[0].Expiry = "24/10/2027" },
		"zero weight":        func(c *NewCarton) { c.GrossWeightG = 0 },
		"huge weight":        func(c *NewCarton) { c.GrossWeightG = MaxGrossWeightG + 1 },
		"zero dim":           func(c *NewCarton) { c.Dims.H = 0 },
		"huge dim":           func(c *NewCarton) { c.Dims.L = MaxDimMm + 1 },
		"case kind in loose": func(c *NewCarton) { c.Kind = HomogeneousCase },
	}
	for name, f := range mut {
		t.Run(name, func(t *testing.T) {
			c := carton(t, 1, "item-1", g, 3)
			f(&c)
			if _, err := newShipment(t).AddCarton(c, looseRules()); !errors.Is(err, shared.ErrValidation) {
				t.Fatalf("err = %v, want ErrValidation", err)
			}
		})
	}
	ok := carton(t, 1, "item-1", g, 3)
	ok.Contents[0].Lot, ok.Contents[0].Expiry = strings.Repeat("L", MaxLot), "2027-10-24"
	c, err := newShipment(t).AddCarton(ok, looseRules())
	if err != nil || c.Kind != Mixed {
		t.Fatalf("valid loose carton: %v %+v", err, c)
	}
}

func TestAddCartonNormalisesShortGTIN(t *testing.T) {
	g8, err := gs1.NewGTIN("0614141")
	if err != nil {
		t.Fatal(err)
	}
	c := carton(t, 1, "item-1", g8.String()[6:], 3) // 8 digits
	got, err := newShipment(t).AddCarton(c, looseRules())
	if err != nil || len(got.Contents[0].GTIN) != 14 {
		t.Fatalf("got %v %+v", err, got)
	}
}

func TestAddCartonAgainstPlan(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	r := looseRules()
	t.Run("item not on the plan", func(t *testing.T) {
		if _, err := newShipment(t).AddCarton(carton(t, 1, "item-9", g, 1), r); !errors.Is(err, ErrCartonContentsMismatchPlan) {
			t.Fatal(err)
		}
	})
	t.Run("up to the plan line is fine, one more is not", func(t *testing.T) {
		s := newShipment(t)
		if _, err := s.AddCarton(carton(t, 1, "item-1", g, 120), r); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddCarton(carton(t, 2, "item-1", g, 1), r); !errors.Is(err, ErrCartonContentsMismatchPlan) {
			t.Fatal(err)
		}
	})
	t.Run("other shipments count against the line", func(t *testing.T) {
		rr := r
		rr.OtherQty = map[string]int{"item-1": 100}
		s := newShipment(t)
		if _, err := s.AddCarton(carton(t, 1, "item-1", g, 20), rr); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddCarton(carton(t, 2, "item-1", g, 1), rr); !errors.Is(err, ErrCartonContentsMismatchPlan) {
			t.Fatal(err)
		}
	})
}

func TestAddCartonOnlyWhileDraft(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	if _, err := s.AddCarton(carton(t, 1, "item-1", g, 120), looseRules()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Label(PlanRules{PlanQty: map[string]int{"item-1": 120}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCarton(carton(t, 2, "item-2", g, 1), looseRules()); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("add after label: %v", err)
	}
}

func TestLabel(t *testing.T) {
	t.Run("needs a carton", func(t *testing.T) {
		if _, err := newShipment(t).Label(PlanRules{}, t0); !errors.Is(err, shared.ErrValidation) {
			t.Fatal(err)
		}
	})
}

func TestLabelSingleShipmentMustEqualThePlan(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	_, _ = s.AddCarton(carton(t, 1, "item-1", g, 100), looseRules())
	if _, err := s.Label(PlanRules{PlanQty: map[string]int{"item-1": 120}, SingleShipment: true}, t0); !errors.Is(err, ErrCartonContentsMismatchPlan) {
		t.Fatalf("short: %v", err)
	}
	if s.State() != Draft {
		t.Fatal("a refused label must leave the shipment DRAFT")
	}
	_, _ = s.AddCarton(carton(t, 2, "item-1", g, 20), looseRules())
	if _, err := s.Label(PlanRules{PlanQty: map[string]int{"item-1": 120, "item-2": 50}, SingleShipment: true}, t0); !errors.Is(err, ErrCartonContentsMismatchPlan) {
		t.Fatalf("a plan line with nothing packed: %v", err)
	}
	ev, err := s.Label(PlanRules{PlanQty: map[string]int{"item-1": 120}, SingleShipment: true}, t0.Add(time.Minute))
	if err != nil || len(ev) != 1 {
		t.Fatalf("label: %v %v", err, ev)
	}
	e := ev[0].(InboundShipmentLabelled)
	if e.EventName() != "InboundShipmentLabelled" || e.Entity() != Entity || e.SubjectID() != "shp-1" || e.AggregateVersion() != 4 ||
		!e.OccurredAt().Equal(t0.Add(time.Minute)) || len(e.Cartons) != 2 || !e.Cartons[0].Sealed || e.SellerID != "seller-1" {
		t.Fatalf("event %+v", e)
	}
	if s.State() != Labelled {
		t.Fatal(s.State())
	}
}

func TestLabelMultiShipmentPlanOnlyNeedsToFit(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	_, _ = s.AddCarton(carton(t, 1, "item-1", g, 5), looseRules())
	if _, err := s.Label(PlanRules{PlanQty: map[string]int{"item-1": 120}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Label(PlanRules{}, t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("labelled twice: %v", err)
	}
}

func TestShip(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	_, _ = s.AddCarton(carton(t, 1, "item-1", g, 5), looseRules())
	if _, err := s.Ship(t0); !errors.Is(err, ErrShipmentNotLabelled) {
		t.Fatalf("draft ship: %v", err)
	}
	_, _ = s.Label(PlanRules{}, t0)
	ev, err := s.Ship(t0.Add(time.Hour))
	if err != nil || len(ev) != 1 {
		t.Fatal(err)
	}
	e := ev[0].(InboundShipmentShipped)
	if e.EventName() != "InboundShipmentShipped" || e.Entity() != Entity || e.SubjectID() != "shp-1" || e.AggregateVersion() != 4 || e.NodeID != "node-1" || e.PlanID != "pln-1" {
		t.Fatalf("event %+v", e)
	}
	if _, err := s.Ship(t0); !errors.Is(err, shared.ErrIllegalTransition) || errors.Is(err, ErrShipmentNotLabelled) {
		t.Fatalf("ship twice: %v", err)
	}
	if got := s.QtyByItem()["item-1"]; got != 5 {
		t.Fatalf("QtyByItem = %d", got)
	}
}

func TestShipRefusesAnUnsealedCarton(t *testing.T) {
	snap := Snapshot{ID: "s", PlanID: "p", SellerID: "x", NodeID: "n", State: Labelled, CreatedAt: t0, LabelledAt: t0, Version: 3,
		Cartons: []Carton{{SSCC: mustSSCC(t, 1), Kind: Mixed, Sealed: true, Contents: []Content{{CatalogItemID: "i", GTIN: mustGTIN(t, "0614141000012"), QtyEaches: 1}}}}}
	s, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ship(t0); err != nil {
		t.Fatal(err)
	}
	snap.Cartons[0].Sealed = false
	if _, err := Restore(snap); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("an unsealed carton in a LABELLED shipment must be refused: %v", err)
	}
}

func TestRestoreRoundTripAndInvariants(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	_, _ = s.AddCarton(carton(t, 1, "item-1", g, 5), looseRules())
	snap := s.Snapshot()
	again, err := Restore(snap)
	if err != nil || again.Version() != s.Version() || len(again.Cartons()) != 1 {
		t.Fatal(err)
	}
	broken := []func(*Snapshot){
		func(s *Snapshot) { s.State = "NOPE" },
		func(s *Snapshot) { s.State = Labelled },
		func(s *Snapshot) { s.ShippedAt = t0 },
		func(s *Snapshot) { s.Version = 0 },
		func(s *Snapshot) { s.CreatedAt = time.Time{} },
		func(s *Snapshot) { s.SellerID = "a b" },
		func(s *Snapshot) { s.Cartons[0].SSCC = "1" },
		func(s *Snapshot) { s.Cartons[0].Kind = "X" },
		func(s *Snapshot) { s.Cartons[0].Receipt = "X" },
		func(s *Snapshot) { s.Cartons[0].Contents = nil },
		func(s *Snapshot) { s.State, s.LabelledAt, s.ShippedAt, s.ReceivedAt = Closed, t0, t0, time.Time{} },
	}
	for i, f := range broken {
		sn := s.Snapshot()
		f(&sn)
		if _, err := Restore(sn); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("case %d: %v, want ErrInvalidSnapshot", i, err)
		}
	}
	tooMany := s.Snapshot()
	tooMany.Cartons = make([]Carton, MaxCartons+1)
	if _, err := Restore(tooMany); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Error("too many cartons")
	}
}

func TestMaxCartons(t *testing.T) {
	g := mustGTIN(t, "0614141000012")
	s := newShipment(t)
	r := looseRules()
	r.PlanQty = map[string]int{"item-1": MaxCartons * 2}
	for i := 1; i <= MaxCartons; i++ {
		if _, err := s.AddCarton(carton(t, i, "item-1", g, 1), r); err != nil {
			t.Fatalf("carton %d: %v", i, err)
		}
	}
	if _, err := s.AddCarton(carton(t, MaxCartons+1, "item-1", g, 1), r); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("one carton too many: %v", err)
	}
}
