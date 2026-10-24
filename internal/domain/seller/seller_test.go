package seller

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func onboarded(t *testing.T) *Account {
	t.Helper()
	a, evs, err := Onboard(NewAccount{ID: "seller-1", Name: "Acme Goods"}, t0)
	if err != nil || len(evs) != 1 {
		t.Fatal(err)
	}
	e := evs[0].(SellerOnboarded)
	if e.EventName() != "SellerOnboarded" || e.Entity() != "seller" || e.SubjectID() != "seller-1" || e.AggregateVersion() != 1 || !e.OccurredAt().Equal(t0) || e.Name != "Acme Goods" {
		t.Fatalf("%+v", e)
	}
	return a
}

func TestOnboardValidation(t *testing.T) {
	bad := []NewAccount{{ID: "", Name: "x"}, {ID: "a b", Name: "x"}, {ID: "a:b", Name: "x"}, {ID: ID(strings.Repeat("a", 65)), Name: "x"},
		{ID: "ok", Name: ""}, {ID: "ok", Name: " padded "}, {ID: "ok", Name: strings.Repeat("n", MaxName+1)}}
	for _, in := range bad {
		if _, _, err := Onboard(in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	if _, _, err := Onboard(NewAccount{ID: ID(strings.Repeat("a", 64)), Name: strings.Repeat("n", MaxName)}, t0); err != nil {
		t.Errorf("limits are inclusive: %v", err)
	}
}

func TestSuspendAndReinstate(t *testing.T) {
	a := onboarded(t)
	if err := a.EnsureActive(); err != nil {
		t.Fatal(err)
	}
	evs, err := a.Suspend("late shipments", t0.Add(time.Hour))
	if err != nil || len(evs) != 1 || a.State() != Suspended || a.Version() != 2 {
		t.Fatalf("%v %v", err, evs)
	}
	e := evs[0].(SellerSuspended)
	if e.EventName() != "SellerSuspended" || e.Entity() != "seller" || e.SubjectID() != "seller-1" || e.Reason != "late shipments" || e.AggregateVersion() != 2 {
		t.Fatalf("%+v", e)
	}
	if err := a.EnsureActive(); !errors.Is(err, ErrSellerSuspended) {
		t.Fatalf("suspended seller: %v", err)
	}
	if evs, err := a.Suspend("again", t0); err != nil || evs != nil || a.Version() != 2 {
		t.Fatalf("suspending twice is a no-op: %v %v", err, evs)
	}
}

func TestReinstate(t *testing.T) {
	a := onboarded(t)
	_, _ = a.Suspend("x", t0.Add(time.Hour))
	evs, err := a.Reinstate(t0.Add(2 * time.Hour))
	if err != nil || len(evs) != 1 || a.State() != Active || a.Version() != 3 {
		t.Fatalf("%v %v", err, evs)
	}
	r := evs[0].(SellerReinstated)
	if r.EventName() != "SellerReinstated" || r.Entity() != "seller" || r.SubjectID() != "seller-1" || !r.OccurredAt().Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("%+v", r)
	}
	if evs, err := a.Reinstate(t0); err != nil || evs != nil {
		t.Fatalf("reinstating twice is a no-op: %v %v", err, evs)
	}
	if a.ID() != "seller-1" || a.OwnerRef() != "seller-1" || a.Name() != "Acme Goods" {
		t.Fatal("accessors")
	}
}

func TestSuspendReasonValidation(t *testing.T) {
	for _, reason := range []string{" lead", strings.Repeat("r", MaxReason+1)} {
		if _, err := onboarded(t).Suspend(reason, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%q: %v", reason, err)
		}
	}
	if _, err := onboarded(t).Suspend("", t0); err != nil {
		t.Errorf("reason is optional: %v", err)
	}
	if _, err := onboarded(t).Suspend(strings.Repeat("r", MaxReason), t0); err != nil {
		t.Errorf("limit inclusive: %v", err)
	}
}

func TestRestore(t *testing.T) {
	a := onboarded(t)
	_, _ = a.Suspend("x", t0.Add(time.Hour))
	got, err := Restore(a.Snapshot())
	if err != nil || got.State() != Suspended || got.Version() != 2 {
		t.Fatal(err)
	}
	broken := []func(*Snapshot){
		func(s *Snapshot) { s.State = "X" },
		func(s *Snapshot) { s.SuspendedAt = time.Time{} },
		func(s *Snapshot) { s.Version = 0 },
		func(s *Snapshot) { s.OnboardedAt = time.Time{} },
		func(s *Snapshot) { s.ID = "" },
		func(s *Snapshot) { s.Name = "" },
		func(s *Snapshot) { s.SuspendReason = " x" },
		func(s *Snapshot) { s.State, s.SuspendedAt = Active, t0 },
		func(s *Snapshot) { s.State, s.SuspendedAt, s.SuspendReason = Active, time.Time{}, "r" },
	}
	for i, f := range broken {
		s := got.Snapshot()
		f(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}
