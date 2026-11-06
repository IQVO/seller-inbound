package placementplan

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func now() time.Time { return time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC) }

// fixture is the ADR 0012 worked example: one region, two vendor FCs.
func fixture() NewPlan {
	return NewPlan{
		ID: "plp-1", SellerID: "sel-1", CatalogItemID: "itm-1", CoverDays: 14, WindowDays: 28,
		Suggestions: []Suggestion{
			{NodeID: "fc-a", RegionID: "reg-east", SuggestedUnits: 12,
				Evidence: Evidence{RegionDemandUnits: 140, NodeAvailabilityUnits: 30, CoverDays: 14}},
			{NodeID: "fc-b", RegionID: "reg-east", SuggestedUnits: 8,
				Evidence: Evidence{RegionDemandUnits: 140, NodeAvailabilityUnits: 20, CoverDays: 14}},
		},
	}
}

func TestComputeBuildsSuggestedPlanAndRaisesTheEvent(t *testing.T) {
	p, evs, err := Compute(fixture(), now())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	checkHeader(t, p)
	if len(p.Suggestions()) != 2 {
		t.Fatalf("suggestions = %v", p.Suggestions())
	}
	checkEvent(t, evs)
}

func checkHeader(t *testing.T, p *Plan) {
	t.Helper()
	if p.State() != Suggested || p.Version() != 1 {
		t.Fatalf("state/version = %s/%d", p.State(), p.Version())
	}
	if p.Basis() != Basis || p.WindowDays() != 28 || p.CoverDays() != 14 {
		t.Fatalf("basis/window/cover = %s/%d/%d", p.Basis(), p.WindowDays(), p.CoverDays())
	}
}

func checkEvent(t *testing.T, evs []shared.Event) {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("events = %v", evs)
	}
	ev, ok := evs[0].(PlacementPlanSuggested)
	if !ok {
		t.Fatalf("event = %+v", evs[0])
	}
	if ev.PlanID != "plp-1" || ev.SellerID != "sel-1" || ev.CatalogItemID != "itm-1" ||
		ev.Basis != Basis || len(ev.Suggestions) != 2 || !ev.CreatedAt.Equal(now()) {
		t.Fatalf("event = %+v", ev)
	}
	if ev.SubjectID() != "plp-1" || ev.Entity() != "placementplan" || ev.EventName() != "PlacementPlanSuggested" {
		t.Fatalf("event envelope fields = %+v", ev)
	}
}

func TestEmptyPlanIsLegal(t *testing.T) {
	in := fixture()
	in.Suggestions = nil
	p, evs, err := Compute(in, now())
	if err != nil {
		t.Fatalf("an item with no demand yields an EMPTY plan: %v", err)
	}
	if len(p.Suggestions()) != 0 {
		t.Fatalf("suggestions = %v", p.Suggestions())
	}
	if len(evs) != 1 {
		t.Fatalf("the empty plan is still published: %v", evs)
	}
}

func TestComputeRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*NewPlan)
	}{
		{"bad plan id", func(in *NewPlan) { in.ID = "?" }},
		{"bad seller", func(in *NewPlan) { in.SellerID = "" }},
		{"bad item", func(in *NewPlan) { in.CatalogItemID = "x!" }},
		{"cover too small", func(in *NewPlan) { in.CoverDays = 0 }},
		{"cover too large", func(in *NewPlan) { in.CoverDays = 91 }},
		{"window missing", func(in *NewPlan) { in.WindowDays = 0 }},
		{"zero-unit suggestion", func(in *NewPlan) { in.Suggestions[0].SuggestedUnits = 0 }},
		{"suggestion without demand", func(in *NewPlan) { in.Suggestions[0].Evidence.RegionDemandUnits = 0 }},
		{"negative availability", func(in *NewPlan) { in.Suggestions[0].Evidence.NodeAvailabilityUnits = -1 }},
		{"duplicate node", func(in *NewPlan) { in.Suggestions[1].NodeID = in.Suggestions[0].NodeID }},
		{"bad region", func(in *NewPlan) { in.Suggestions[0].RegionID = "" }},
	}
	for _, tc := range cases {
		in := fixture()
		tc.mut(&in)
		if _, _, err := Compute(in, now()); !errors.Is(err, shared.ErrValidation) {
			t.Fatalf("%s: want validation error, got %v", tc.name, err)
		}
	}
}

func TestAccept(t *testing.T) {
	p, _, err := Compute(fixture(), now())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	later := now().Add(time.Hour)
	if err := p.Accept(later); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if p.State() != Accepted || p.Version() != 2 {
		t.Fatalf("state/version = %s/%d", p.State(), p.Version())
	}
	if !p.Snapshot().AcceptedAt.Equal(later) {
		t.Fatalf("acceptedAt = %v", p.Snapshot().AcceptedAt)
	}
	if err := p.Accept(later); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("accepting twice: want illegal transition, got %v", err)
	}
}

func TestSupersede(t *testing.T) {
	p, _, err := Compute(fixture(), now())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	later := now().Add(2 * time.Hour)
	if err := p.Supersede(later); err != nil {
		t.Fatalf("supersede: %v", err)
	}
	if p.State() != Superseded || p.Version() != 2 {
		t.Fatalf("state/version = %s/%d", p.State(), p.Version())
	}
	if err := p.Supersede(later); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("superseding twice: want illegal transition, got %v", err)
	}
}

func TestAcceptedPlanCanBeSuperseded(t *testing.T) {
	p, _, err := Compute(fixture(), now())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if err := p.Accept(now().Add(time.Hour)); err != nil {
		t.Fatalf("accept: %v", err)
	}
	later := now().Add(2 * time.Hour)
	if err := p.Supersede(later); err != nil {
		t.Fatalf("a newer compute supersedes even an ACCEPTED plan: %v", err)
	}
	if p.State() != Superseded || p.Version() != 3 {
		t.Fatalf("state/version = %s/%d", p.State(), p.Version())
	}
}

func TestSnapshotRoundTripsEveryPersistedField(t *testing.T) {
	p := acceptedFixture(t)
	got, err := Restore(p.Snapshot())
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	want, have := p.Snapshot(), got.Snapshot()
	if len(want.Suggestions) != len(have.Suggestions) {
		t.Fatalf("round trip lost suggestions:\nwant %+v\nhave %+v", want, have)
	}
	for i := range want.Suggestions {
		if want.Suggestions[i] != have.Suggestions[i] {
			t.Fatalf("suggestion %d: want %+v have %+v", i, want.Suggestions[i], have.Suggestions[i])
		}
	}
	want.Suggestions, have.Suggestions = nil, nil
	if !reflect.DeepEqual(want, have) {
		t.Fatalf("round trip lost a persisted field:\nwant %+v\nhave %+v", want, have)
	}
}

func acceptedFixture(t *testing.T) *Plan {
	t.Helper()
	p, _, err := Compute(fixture(), now())
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if err := p.Accept(now().Add(time.Hour)); err != nil {
		t.Fatalf("accept: %v", err)
	}
	return p
}

func TestRestoreRefusesCorruptSnapshots(t *testing.T) {
	s := fixtureSnapshot()
	s.State = "MYSTERY"
	if _, err := Restore(s); err == nil {
		t.Fatal("an unknown state is corrupt")
	}
	s2 := fixtureSnapshot()
	s2.AcceptedAt = now()
	if _, err := Restore(s2); err == nil {
		t.Fatal("a SUGGESTED plan with acceptedAt is corrupt")
	}
	s3 := fixtureSnapshot()
	s3.Suggestions[0].SuggestedUnits = 0
	if _, err := Restore(s3); err == nil {
		t.Fatal("a zero-unit suggestion is corrupt")
	}
}

func fixtureSnapshot() Snapshot {
	in := fixture()
	return Snapshot{ID: in.ID, SellerID: in.SellerID, CatalogItemID: in.CatalogItemID, CoverDays: in.CoverDays,
		WindowDays: in.WindowDays, Basis: Basis, Suggestions: in.Suggestions, State: Suggested,
		CreatedAt: now(), Version: 1}
}
