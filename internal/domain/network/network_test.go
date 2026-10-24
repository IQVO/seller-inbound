package network

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func TestNodeCanReceive(t *testing.T) {
	tests := []struct {
		typ    string
		status NodeStatus
		ok     bool
	}{
		{TypeFulfillmentCenter, NodeActive, true},
		{TypeInboundCrossDock, NodeActive, true},
		{"RETURNS_CENTER", NodeActive, false},
		{"DELIVERY_STATION", NodeActive, false},
		{"", NodeActive, false},
		{TypeFulfillmentCenter, NodeProposed, false},
		{TypeFulfillmentCenter, NodeSuspended, false},
		{TypeFulfillmentCenter, NodeRetired, false},
	}
	for _, tc := range tests {
		err := Node{ID: "n", Type: tc.typ, Status: tc.status}.CanReceive()
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrNodeCannotReceive)) {
			t.Errorf("%s/%s: %v", tc.typ, tc.status, err)
		}
	}
}

func TestNodeFolds(t *testing.T) {
	n, err := Node{}.Registered("n1", TypeFulfillmentCenter)
	if err != nil || n.Status != NodeProposed || n.Type != TypeFulfillmentCenter {
		t.Fatalf("%v %+v", err, n)
	}
	if _, err := (Node{}).Registered("", "X"); !errors.Is(err, shared.ErrValidation) {
		t.Fatal(err)
	}
	n = n.Activated("n1")
	if n.Status != NodeActive {
		t.Fatal(n)
	}
	// an out-of-order NodeRegistered keeps the status already seen
	again, _ := n.Registered("n1", TypeFulfillmentCenter)
	if again.Status != NodeActive {
		t.Fatalf("re-registered %+v", again)
	}
	if got := n.Suspended("n1"); got.Status != NodeSuspended {
		t.Fatal(got)
	}
	r := n.Retired("n1")
	if r.Status != NodeRetired || r.Activated("n1").Status != NodeRetired || r.Suspended("n1").Status != NodeRetired {
		t.Fatal("a retired node stays retired")
	}
	// an activation seen before the registration creates the row
	early := Node{}.Activated("n2")
	if early.ID != "n2" || early.Status != NodeActive {
		t.Fatal(early)
	}
}

func TestProfileDeclaredKeepsTheNewestRevision(t *testing.T) {
	yes := true
	l2 := &InboundLimits{MaxCartonWeightG: 2000, MaxCartonDims: &Dims{1, 2, 3}, AcceptsPallets: &yes}
	n := Node{ID: "n"}.ProfileDeclared("n", 2, l2)
	if n.ProfileRevision != 2 || n.Limits != l2 {
		t.Fatalf("%+v", n)
	}
	if old := n.ProfileDeclared("n", 1, nil); old.Limits != l2 || old.ProfileRevision != 2 {
		t.Fatal("an older revision must be ignored")
	}
	if same := n.ProfileDeclared("n", 2, nil); same.Limits != l2 {
		t.Fatal("the same revision must be ignored")
	}
	if cleared := n.ProfileDeclared("n", 3, nil); cleared.Limits != nil || cleared.ProfileRevision != 3 {
		t.Fatal("a newer revision without inbound clears the limits")
	}
}

func TestPackagingFolds(t *testing.T) {
	const g, child = "00614141000036", "00614141000012"
	d, err := Declare(nil, "item-1", Case, "614141000036", child, 12, t0)
	if err != nil || !d.Active || d.GTIN != g || d.UnitsPerPack != 12 || d.ChildGTIN != child {
		t.Fatalf("%v %+v", err, d)
	}
	rows := []PackLevel{d}
	if u, ok := ActiveCaseUnits(rows, g); !ok || u != 12 {
		t.Fatal("active case level")
	}
	if _, ok := ActiveCaseUnits(rows, "00614141000012"); ok {
		t.Fatal("another gtin")
	}
}

func TestPackagingRetiredIsNeverReactivated(t *testing.T) {
	const g, child = "00614141000036", "00614141000012"
	d, _ := Declare(nil, "item-1", Case, g, child, 12, t0)
	r, err := Retire(&d, "item-1", Case, g, t0.Add(time.Hour))
	if err != nil || r.Active || r.UnitsPerPack != 12 || !r.RetiredAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("%v %+v", err, r)
	}
	if _, ok := ActiveCaseUnits([]PackLevel{r}, g); ok {
		t.Fatal("a retired level is not active")
	}
	// a retired GTIN is never reactivated by a late declaration
	late, err := Declare(&r, "item-1", Case, g, child, 12, t0)
	if err != nil || late.Active || !late.RetiredAt.Equal(r.RetiredAt) {
		t.Fatalf("%v %+v", err, late)
	}
}

func TestPackagingRetireAndLevels(t *testing.T) {
	const g = "00614141000036"
	// retire before declare creates the retired row
	early, err := Retire(nil, "item-1", Case, g, t0)
	if err != nil || early.Active || early.CatalogItemID != "item-1" || early.Level != Case {
		t.Fatalf("%v %+v", err, early)
	}
	// only CASE levels with at least two eaches count
	each, _ := Declare(nil, "item-1", Each, g, "", 1, t0)
	if _, ok := ActiveCaseUnits([]PackLevel{each}, g); ok {
		t.Fatal("an EACH level is not a case")
	}
	one, _ := Declare(nil, "item-1", Case, g, "", 1, t0)
	if _, ok := ActiveCaseUnits([]PackLevel{one}, g); ok {
		t.Fatal("a case of one each is not a case")
	}
}

func TestPackagingValidation(t *testing.T) {
	if _, err := Declare(nil, "item-1", Case, "123", "", 2, t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatal(err)
	}
	if _, err := Declare(nil, "", Case, "00614141000036", "", 2, t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatal(err)
	}
	if _, err := Declare(nil, "item-1", "BOX", "00614141000036", "", 2, t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatal(err)
	}
	if _, err := Declare(nil, "item-1", Case, "00614141000036", "", 0, t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatal(err)
	}
	if _, err := Retire(nil, "item-1", Case, "5", t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatal(err)
	}
	for _, l := range []Level{Each, Inner, Case, Pallet} {
		if !l.Valid() {
			t.Error(l)
		}
	}
	if Level("").Valid() {
		t.Error("empty level")
	}
}

func TestPackLevelBoundaries(t *testing.T) {
	if _, err := Declare(nil, "item-1", Case, "00614141000036", "00614141000012", 1, t0); err != nil {
		t.Fatalf("one unit per pack is a valid declaration: %v", err)
	}
	if _, err := Declare(nil, "item-1", Case, "00614141000036", "00614141000012", 0, t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("zero units: %v", err)
	}
	for units, want := range map[int]bool{1: false, 2: true, 12: true} {
		rows := []PackLevel{{CatalogItemID: "item-1", Level: Case, GTIN: "00614141000036", UnitsPerPack: units, Active: true}}
		if got, ok := ActiveCaseUnits(rows, "00614141000036"); ok != want || (ok && got != units) {
			t.Errorf("units %d: ActiveCaseUnits = %d, %v", units, got, ok)
		}
	}
}
