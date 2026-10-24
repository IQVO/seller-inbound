package inboundplan

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
	"github.com/claudioed/seller-inbound/internal/domain/shared/gs1"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func gtin(t *testing.T, payload string) string {
	t.Helper()
	g, err := gs1.NewGTIN(payload)
	if err != nil {
		t.Fatal(err)
	}
	return g.String()
}

func newPlan(t *testing.T, mut func(*NewPlan)) (*Plan, error) {
	in := NewPlan{ID: "pln-1", SellerID: "seller-1", DestinationID: "node-1", Packing: CasePacked,
		Lines: []Line{{CatalogItemID: "item-1", GTIN: gtin(t, "0614141000012"), QtyEaches: 120}, {CatalogItemID: "item-2", GTIN: gtin(t, "0614141000029"), QtyEaches: 50}}}
	if mut != nil {
		mut(&in)
	}
	return Create(in, t0)
}

func TestCreate(t *testing.T) {
	p, err := newPlan(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.State() != Draft || p.Version() != 1 || p.ID() != "pln-1" || p.SellerID() != "seller-1" || p.DestinationNodeID() != "node-1" || p.Packing() != CasePacked {
		t.Fatalf("%+v", p.Snapshot())
	}
	if got := p.PlannedByItem(); got["item-1"] != 120 || got["item-2"] != 50 || len(got) != 2 {
		t.Fatalf("%v", got)
	}
	// the returned lines are a copy
	p.Lines()[0].QtyEaches = 1
	if p.Lines()[0].QtyEaches != 120 {
		t.Fatal("Lines must return a copy")
	}
}

func TestCreateNormalisesGTIN(t *testing.T) {
	g13 := gtin(t, "0614141000012")[1:] // 13 digits with the same check digit
	p, err := newPlan(t, func(in *NewPlan) { in.Lines[0].GTIN = g13 })
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Lines()[0].GTIN; len(got) != 14 || got[0] != '0' {
		t.Fatalf("gtin %q", got)
	}
}

func TestCreateValidation(t *testing.T) {
	many := make([]Line, MaxLines+1)
	for i := range many {
		many[i] = Line{CatalogItemID: "i" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676)), GTIN: gtin(t, "0614141000012"), QtyEaches: 1}
	}
	bad := map[string]func(*NewPlan){
		"empty id":          func(in *NewPlan) { in.ID = "" },
		"bad seller":        func(in *NewPlan) { in.SellerID = "a b" },
		"bad node":          func(in *NewPlan) { in.DestinationID = "" },
		"bad packing":       func(in *NewPlan) { in.Packing = "LOOSE" },
		"no lines":          func(in *NewPlan) { in.Lines = nil },
		"too many lines":    func(in *NewPlan) { in.Lines = many },
		"bad item id":       func(in *NewPlan) { in.Lines[0].CatalogItemID = "" },
		"duplicate item":    func(in *NewPlan) { in.Lines[1].CatalogItemID = "item-1" },
		"bad gtin":          func(in *NewPlan) { in.Lines[0].GTIN = "12345678901234" },
		"variable measure":  func(in *NewPlan) { in.Lines[0].GTIN = gtin(t, "9614141000012") },
		"zero qty":          func(in *NewPlan) { in.Lines[0].QtyEaches = 0 },
		"qty above ceiling": func(in *NewPlan) { in.Lines[0].QtyEaches = MaxQtyEaches + 1 },
	}
	for name, f := range bad {
		if _, err := newPlan(t, f); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	ok := func(in *NewPlan) { in.Lines[0].QtyEaches = MaxQtyEaches; in.Packing = Individual }
	if _, err := newPlan(t, ok); err != nil {
		t.Errorf("ceiling is inclusive: %v", err)
	}
	exactly := func(in *NewPlan) { in.Lines = many[:MaxLines] }
	if _, err := newPlan(t, exactly); err != nil {
		t.Errorf("MaxLines lines are allowed: %v", err)
	}
	one := func(in *NewPlan) {
		in.Lines = []Line{{CatalogItemID: "item-1", GTIN: gtin(t, "0614141000012"), QtyEaches: 1}}
	}
	if _, err := newPlan(t, one); err != nil {
		t.Errorf("one line of one each is allowed: %v", err)
	}
	if !Individual.Valid() || !CasePacked.Valid() || PackingOption("").Valid() {
		t.Error("PackingOption.Valid")
	}
}

func TestConfirm(t *testing.T) {
	p, _ := newPlan(t, nil)
	if err := p.EnsureConfirmed(); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("draft: %v", err)
	}
	evs, err := p.Confirm(t0.Add(time.Hour))
	if err != nil || len(evs) != 1 || p.State() != Confirmed || p.Version() != 2 {
		t.Fatal(err)
	}
	checkConfirmedEvent(t, evs[0].(InboundPlanConfirmed))
	if err := p.EnsureConfirmed(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Confirm(t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("confirm twice: %v", err)
	}
}

func TestCancelConfirmedPlan(t *testing.T) {
	p, _ := newPlan(t, nil)
	_, _ = p.Confirm(t0)
	if err := p.Cancel(t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("cancel confirmed: %v", err)
	}
}

func TestCancel(t *testing.T) {
	c, _ := newPlan(t, nil)
	if err := c.Cancel(t0.Add(time.Hour)); err != nil || c.State() != Cancelled || c.Version() != 2 {
		t.Fatal(err)
	}
	if _, err := c.Confirm(t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("confirm cancelled: %v", err)
	}
	if err := c.Cancel(t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("cancel twice: %v", err)
	}
	if err := c.EnsureConfirmed(); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestRestore(t *testing.T) {
	p, _ := newPlan(t, nil)
	_, _ = p.Confirm(t0.Add(time.Hour))
	got, err := Restore(p.Snapshot())
	if err != nil || got.State() != Confirmed {
		t.Fatal(err)
	}
	broken := []func(*Snapshot){
		func(s *Snapshot) { s.State = "X" },
		func(s *Snapshot) { s.ConfirmedAt = time.Time{} },
		func(s *Snapshot) { s.CancelledAt = t0 },
		func(s *Snapshot) { s.State, s.ConfirmedAt, s.CancelledAt = Draft, t0, time.Time{} },
		func(s *Snapshot) { s.State, s.ConfirmedAt, s.CancelledAt = Cancelled, t0, t0 },
		func(s *Snapshot) { s.State, s.ConfirmedAt, s.CancelledAt = Cancelled, time.Time{}, time.Time{} },
		func(s *Snapshot) { s.Version = 0 },
		func(s *Snapshot) { s.CreatedAt = time.Time{} },
		func(s *Snapshot) { s.Lines[0].GTIN = gtin(t, "0614141000012")[1:] },
		func(s *Snapshot) { s.Lines = nil },
	}
	for i, f := range broken {
		s := got.Snapshot()
		f(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func checkConfirmedEvent(t *testing.T, e InboundPlanConfirmed) {
	t.Helper()
	if e.EventName() != "InboundPlanConfirmed" || e.Entity() != "inboundplan" || e.SubjectID() != "pln-1" || e.AggregateVersion() != 2 {
		t.Fatalf("%+v", e)
	}
	if !e.OccurredAt().Equal(t0.Add(time.Hour)) || e.SellerID != "seller-1" || e.DestinationNodeID != "node-1" || e.Packing != CasePacked || len(e.Lines) != 2 {
		t.Fatalf("%+v", e)
	}
}
