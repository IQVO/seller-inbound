package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// store is what both twins implement: the writer and the reader port.
type store interface {
	report.Projection
	report.Reader
}

// runContract is THE contract of the analytical store (ADR 0017). It runs
// against MemoryStore (memory_test.go) and against real Postgres
// (postgres_integration_test.go); newStore returns an EMPTY store.
func runContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Run("an empty store answers empty, non-nil reads", func(t *testing.T) { contractEmpty(t, newStore(t)) })
	t.Run("a replayed id is a no-op", func(t *testing.T) { contractReplay(t, newStore(t)) })
	t.Run("counts per day", func(t *testing.T) { contractCounts(t, newStore(t)) })
	t.Run("received items per day and GTIN", func(t *testing.T) { contractItems(t, newStore(t)) })
	t.Run("advisory-to-receipt per day", func(t *testing.T) { contractLatency(t, newStore(t)) })
	t.Run("the freshness basis is the newest CloudEvents time", func(t *testing.T) { contractLastEvent(t, newStore(t)) })
	t.Run("an invalid fact is rejected and leaves nothing", func(t *testing.T) { contractRejected(t, newStore(t)) })
}

var (
	ctx       = context.Background()
	nov2      = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	nov3      = nov2.Add(24 * time.Hour)
	nov4      = nov3.Add(24 * time.Hour)
	fullRange = report.Range{From: nov3, To: nov3.Add(48 * time.Hour)}
)

// seeder writes events with fresh ids; At (the CloudEvents time) is a minute
// after the business instant.
type seeder struct {
	t   *testing.T
	s   store
	seq int
}

func (d *seeder) apply(e report.Event) {
	d.t.Helper()
	d.seq++
	e.EventID = fmt.Sprintf("evt-%d", d.seq)
	e.SubjectID = fmt.Sprintf("subject-%d", d.seq)
	e.At = e.OccurredAt.Add(time.Minute)
	applied, err := d.s.Apply(ctx, e)
	if err != nil || !applied {
		d.t.Fatalf("apply %s %s: applied=%v err=%v", e.Kind, e.EventID, applied, err)
	}
}

func (d *seeder) plan(seller string, at time.Time) {
	d.apply(report.Event{Kind: report.KindInboundPlanConfirmed, SellerID: seller, OccurredAt: at})
}

func (d *seeder) advisory(seller, item string, at time.Time) {
	d.apply(report.Event{Kind: report.KindPlacementPlanSuggested, SellerID: seller, CatalogItemID: item, OccurredAt: at})
}

func (d *seeder) receipt(seller string, at time.Time, lines ...report.ReceivedLine) {
	d.apply(report.Event{Kind: report.KindInboundShipmentReceived, SellerID: seller, OccurredAt: at, Lines: lines})
}

func line(gtin, item string, qty int) report.ReceivedLine {
	return report.ReceivedLine{GTIN: gtin, CatalogItemID: item, QtyEaches: qty}
}

// seedScenario writes the fixture the report contracts read (ADR 0017's
// definitions). Range [nov3, nov5).
func seedScenario(t *testing.T, s store) {
	t.Helper()
	d := &seeder{t: t, s: s}
	// Plans: two on nov3 (the second in its last second), one on nov4; one a
	// second before the range and one exactly at its exclusive end.
	d.plan("s1", nov3.Add(9*time.Hour))
	d.plan("s2", nov4.Add(-time.Second))
	d.plan("s1", nov4.Add(time.Hour))
	d.plan("s1", nov3.Add(-time.Second))
	d.plan("s1", fullRange.To)
	// Advisories. The one of nov2 is before the range but still the latest
	// before nothing: a later one for the same seller and item wins.
	d.advisory("s1", "item-1", nov2.Add(12*time.Hour))
	d.advisory("s1", "item-1", nov3.Add(6*time.Hour))  // the latest before R1: 4h
	d.advisory("s2", "item-2", nov3.Add(8*time.Hour))  // before R2: 1h
	d.advisory("s1", "item-1", nov3.Add(11*time.Hour)) // AFTER R1: never paired with it
	d.advisory("s1", "item-9", nov4)                   // before R7: 15h
	d.advisory("s2", "item-5", nov4.Add(10*time.Hour)) // at the very instant of R9: 0 seconds
	d.advisory("s1", "item-1", fullRange.To)           // after the range
	// Receipts.
	d.receipt("s1", nov3.Add(10*time.Hour), line("g-1", "item-1", 10), line("g-2", "", 4)) // R1
	d.receipt("s2", nov3.Add(9*time.Hour), line("g-3", "item-2", 6))                       // R2
	d.receipt("s1", nov3.Add(13*time.Hour), line("g-1", "", 5))                            // R3: same GTIN, unresolved here
	d.receipt("s1", nov4.Add(12*time.Hour))                                                // R4: published before ADR 0014, no lines
	d.receipt("s1", nov4.Add(15*time.Hour), line("g-1", "item-9", 2))                      // R7
	d.receipt("s2", nov4.Add(10*time.Hour), line("g-9", "item-1", 1))                      // R8: only s1 has an advisory for item-1
	d.receipt("s2", nov4.Add(10*time.Hour), line("g-5", "item-5", 3))                      // R9
	d.receipt("s1", nov3.Add(-time.Second), line("g-1", "item-1", 100))                    // out: before the range
	d.receipt("s1", fullRange.To, line("g-1", "item-1", 100))                              // out: exactly at the exclusive end
}

func contractEmpty(t *testing.T, s store) {
	counts, err := s.InboundCounts(ctx, fullRange)
	if err != nil || counts == nil || len(counts) != 0 {
		t.Fatalf("counts = %#v, %v; want empty non-nil", counts, err)
	}
	items, err := s.ReceivedItems(ctx, fullRange)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("items = %#v, %v; want empty non-nil", items, err)
	}
	lat, err := s.AdvisoryToReceipt(ctx, fullRange)
	if err != nil || lat == nil || len(lat) != 0 {
		t.Fatalf("latency = %#v, %v; want empty non-nil", lat, err)
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("last = %v, %v; want nil", last, err)
	}
}

func contractReplay(t *testing.T, s store) {
	e := report.Event{Kind: report.KindInboundShipmentReceived, EventID: "evt-1", SubjectID: "shp-1", SellerID: "s1", At: nov3.Add(time.Hour),
		OccurredAt: nov3, Lines: []report.ReceivedLine{line("g-1", "item-1", 3)}}
	if applied, err := s.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if applied, err := s.Apply(ctx, e); err != nil || applied {
		t.Fatalf("replay: applied=%v err=%v; want a no-op", applied, err)
	}
	counts, err := s.InboundCounts(ctx, fullRange)
	if err != nil || len(counts) != 1 || counts[0].ShipmentsReceived != 1 {
		t.Fatalf("counts after a replay = %+v, %v; want one receipt", counts, err)
	}
	items, err := s.ReceivedItems(ctx, fullRange)
	if err != nil || len(items) != 1 || items[0].Publications != 1 || items[0].QtyEaches != 3 {
		t.Fatalf("items after a replay = %+v, %v; want one line", items, err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func contractCounts(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.InboundCounts(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	// nov3 advisories: 06:00, 08:00 and 11:00; the one of nov2 and the one at the exclusive end are outside.
	want := []report.DayCounts{
		{Day: nov3, PlansConfirmed: 2, AdvisoriesIssued: 3, ShipmentsReceived: 3},
		{Day: nov4, PlansConfirmed: 1, AdvisoriesIssued: 2, ShipmentsReceived: 4},
	}
	assertDays(t, "counts", got, want, func(c report.DayCounts) time.Time { return c.Day })
	one, err := s.InboundCounts(ctx, report.Range{From: nov4, To: fullRange.To})
	if err != nil || len(one) != 1 || !one[0].Day.Equal(nov4) || one[0].ShipmentsReceived != 4 {
		t.Fatalf("a one-day range = %+v, %v", one, err)
	}
}

// assertDays compares rows whose Day is UTC, ignoring the Day's location.
func assertDays[T any](t *testing.T, what string, got, want []T, day func(T) time.Time) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %+v\nwant %+v", what, got, want)
	}
	for i := range want {
		if !day(got[i]).Equal(day(want[i])) || day(got[i]).Location() != time.UTC {
			t.Errorf("%s row %d day = %v, want %v UTC", what, i, day(got[i]), day(want[i]))
		}
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%s row %d = %+v, want %+v", what, i, got[i], want[i])
		}
	}
}

func contractItems(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.ReceivedItems(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	want := []report.DayItem{
		{Day: nov3, GTIN: "g-1", CatalogItemID: "item-1", Publications: 2, QtyEaches: 15}, // R1 + R3 (unresolved there)
		{Day: nov3, GTIN: "g-2", Publications: 1, QtyEaches: 4},
		{Day: nov3, GTIN: "g-3", CatalogItemID: "item-2", Publications: 1, QtyEaches: 6},
		{Day: nov4, GTIN: "g-1", CatalogItemID: "item-9", Publications: 1, QtyEaches: 2},
		{Day: nov4, GTIN: "g-5", CatalogItemID: "item-5", Publications: 1, QtyEaches: 3},
		{Day: nov4, GTIN: "g-9", CatalogItemID: "item-1", Publications: 1, QtyEaches: 1},
	}
	assertDays(t, "items", got, want, func(i report.DayItem) time.Time { return i.Day })
}

func contractLatency(t *testing.T, s store) {
	seedScenario(t, s)
	got, err := s.AdvisoryToReceipt(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	// nov3: R1 item-1 vs the 06:00 advisory (14400 s), R2 item-2 vs 08:00 (3600 s); the
	// unresolved lines are not samples. nov4: R7 15 h (54000 s), R9 0 s, R8 has no
	// advisory of its seller.
	want := []report.DayLatency{
		{Day: nov3, Samples: 2, P50: 9000, P95: 13860},
		{Day: nov4, Samples: 2, P50: 27000, P95: 51300},
	}
	if len(got) != len(want) {
		t.Fatalf("latency = %+v\nwant %+v", got, want)
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.Day.Equal(w.Day) || g.Day.Location() != time.UTC || g.Samples != w.Samples || !near(g.P50, w.P50) || !near(g.P95, w.P95) {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
}

func contractLastEvent(t *testing.T, s store) {
	d := &seeder{t: t, s: s}
	d.plan("s1", nov3.Add(5*time.Hour))
	d.advisory("s1", "item-1", nov3.Add(2*time.Hour))
	last, err := s.LastEventAt(ctx)
	newest := nov3.Add(5*time.Hour + time.Minute)
	if err != nil || last == nil || !last.Equal(newest) || last.Location() != time.UTC {
		t.Fatalf("last = %v, %v; want %v UTC (the newest, not the last applied)", last, err, newest)
	}
}

func contractRejected(t *testing.T, s store) {
	ev := func(kind report.Kind, mutate func(*report.Event)) report.Event {
		e := report.Event{Kind: kind, EventID: "evt-bad-" + string(kind), SubjectID: "sub", SellerID: "s1", At: nov3, OccurredAt: nov3}
		mutate(&e)
		return e
	}
	bad := []report.Event{
		ev(report.KindInboundPlanConfirmed, func(e *report.Event) { e.SellerID = "" }),
		ev(report.KindPlacementPlanSuggested, func(*report.Event) {}),
		ev(report.KindInboundShipmentReceived, func(e *report.Event) { e.Lines = []report.ReceivedLine{line("g-1", "", 2), line("g-2", "", 0)} }),
		ev("SellerOnboarded", func(*report.Event) {}),
	}
	dup := ev(report.KindInboundShipmentReceived, func(e *report.Event) {
		e.EventID = "evt-dup"
		e.Lines = []report.ReceivedLine{line("g-1", "", 2), line("g-1", "", 3)}
	})
	bad = append(bad, dup)
	for _, e := range bad {
		if applied, err := s.Apply(ctx, e); applied || !errors.Is(err, report.ErrRejected) {
			t.Errorf("%s: applied=%v err=%v; want ErrRejected", e.EventID, applied, err)
		}
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("a rejected fact must leave no mark, last = %v, %v", last, err)
	}
	counts, err := s.InboundCounts(ctx, fullRange)
	if err != nil || len(counts) != 0 {
		t.Fatalf("a rejected fact must leave no row (not even the receipt of the duplicate-GTIN one), counts = %+v, %v", counts, err)
	}
	// The duplicate-GTIN receipt was rolled back whole: its id is free to be applied once corrected.
	dup.Lines = []report.ReceivedLine{line("g-1", "", 5)}
	if applied, err := s.Apply(ctx, dup); err != nil || !applied {
		t.Fatalf("the corrected receipt: applied=%v err=%v", applied, err)
	}
}
