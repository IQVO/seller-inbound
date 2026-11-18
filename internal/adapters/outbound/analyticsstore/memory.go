package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// MemoryStore is the in-memory twin of Projection and Reader, for unit tests
// of the consumer and the reports. contract_test.go runs the SAME suite
// against it and against Postgres, so its behaviour cannot drift from the real
// store. The shipped binaries never construct it.
type MemoryStore struct {
	mu         sync.Mutex
	processed  map[string]time.Time
	plans      []memPlan
	advisories []memAdvisory
	receipts   []memReceipt
	// Fail, when set, is returned by the next Apply (and cleared), to drive
	// the consumer's transient and rejection paths.
	Fail error
}

type memPlan struct{ at time.Time }

type memAdvisory struct {
	seller, item string
	at           time.Time
}

type memReceipt struct {
	seller string
	at     time.Time
	lines  []report.ReceivedLine
}

// NewMemoryStore returns an empty twin.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{processed: map[string]time.Time{}}
}

var (
	_ report.Projection = (*MemoryStore)(nil)
	_ report.Reader     = (*MemoryStore)(nil)
)

// Apply implements report.Projection with the Postgres store's semantics: an
// event the schema's CHECKs would refuse is rejected before anything is
// recorded (so is a repeated GTIN inside one receipt: the table's primary key),
// a replay is a no-op.
func (m *MemoryStore) Apply(_ context.Context, e report.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		err := m.Fail
		m.Fail = nil
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	if _, seen := m.processed[e.EventID]; seen {
		return false, nil
	}
	if duplicateGTIN(e.Lines) {
		return false, fmt.Errorf("%w: %s %s: a GTIN appears twice in received", report.ErrRejected, e.Kind, e.EventID)
	}
	m.processed[e.EventID] = e.At.UTC()
	at := e.OccurredAt.UTC()
	switch e.Kind {
	case report.KindInboundPlanConfirmed:
		m.plans = append(m.plans, memPlan{at: at})
	case report.KindPlacementPlanSuggested:
		m.advisories = append(m.advisories, memAdvisory{seller: e.SellerID, item: e.CatalogItemID, at: at})
	default: // Validate admitted only the three kinds: this is the receipt
		m.receipts = append(m.receipts, memReceipt{seller: e.SellerID, at: at, lines: append([]report.ReceivedLine(nil), e.Lines...)})
	}
	return true, nil
}

func duplicateGTIN(lines []report.ReceivedLine) bool {
	seen := map[string]bool{}
	for _, l := range lines {
		if seen[l.GTIN] {
			return true
		}
		seen[l.GTIN] = true
	}
	return false
}

func utcDay(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

func inRange(r report.Range, at time.Time) bool { return at.Compare(r.From) >= 0 && at.Before(r.To) }

// InboundCounts implements report.Reader.
func (m *MemoryStore) InboundCounts(_ context.Context, r report.Range) ([]report.DayCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	days := map[time.Time]*report.DayCounts{}
	day := func(at time.Time) *report.DayCounts {
		d := utcDay(at)
		if days[d] == nil {
			days[d] = &report.DayCounts{Day: d}
		}
		return days[d]
	}
	for _, p := range m.plans {
		if inRange(r, p.at) {
			day(p.at).PlansConfirmed++
		}
	}
	for _, a := range m.advisories {
		if inRange(r, a.at) {
			day(a.at).AdvisoriesIssued++
		}
	}
	for _, rc := range m.receipts {
		if inRange(r, rc.at) {
			day(rc.at).ShipmentsReceived++
		}
	}
	out := make([]report.DayCounts, 0, len(days))
	for _, d := range days {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// ReceivedItems implements report.Reader.
func (m *MemoryStore) ReceivedItems(_ context.Context, r report.Range) ([]report.DayItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type key struct {
		day  time.Time
		gtin string
	}
	items := map[key]*report.DayItem{}
	for _, rc := range m.receipts {
		if !inRange(r, rc.at) {
			continue
		}
		for _, l := range rc.lines {
			k := key{utcDay(rc.at), l.GTIN}
			if items[k] == nil {
				items[k] = &report.DayItem{Day: k.day, GTIN: l.GTIN}
			}
			it := items[k]
			it.Publications++
			it.QtyEaches += l.QtyEaches
			it.CatalogItemID = max(it.CatalogItemID, l.CatalogItemID) // the store's max(): '' sorts first
		}
	}
	out := make([]report.DayItem, 0, len(items))
	for _, it := range items {
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.Before(out[j].Day)
		}
		return out[i].GTIN < out[j].GTIN
	})
	return out, nil
}

// AdvisoryToReceipt implements report.Reader: each received line that resolved
// its catalog item is paired with the LATEST advisory of the same seller and
// item at or before the receipt; a line without one is not a sample.
func (m *MemoryStore) AdvisoryToReceipt(_ context.Context, r report.Range) ([]report.DayLatency, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	perDay := map[time.Time][]float64{}
	for _, rc := range m.receipts {
		if !inRange(r, rc.at) {
			continue
		}
		for _, l := range rc.lines {
			if l.CatalogItemID == "" {
				continue
			}
			if adv, ok := m.latestAdvisory(rc.seller, l.CatalogItemID, rc.at); ok {
				perDay[utcDay(rc.at)] = append(perDay[utcDay(rc.at)], rc.at.Sub(adv).Seconds())
			}
		}
	}
	out := make([]report.DayLatency, 0, len(perDay))
	for day, secs := range perDay {
		out = append(out, report.DayLatency{Day: day, Samples: len(secs),
			P50: report.Percentile(secs, report.LatencyP50()), P95: report.Percentile(secs, report.LatencyP95())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out, nil
}

// latestAdvisory is the newest advisory instant of (seller, item) at or before at.
func (m *MemoryStore) latestAdvisory(seller, item string, at time.Time) (time.Time, bool) {
	var latest time.Time
	found := false
	for _, a := range m.advisories {
		if a.seller == seller && a.item == item && a.at.Compare(at) <= 0 && (!found || a.at.After(latest)) {
			latest, found = a.at, true
		}
	}
	return latest, found
}

// LastEventAt implements report.Reader.
func (m *MemoryStore) LastEventAt(context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *time.Time
	for _, at := range m.processed {
		if last == nil || at.After(*last) {
			a := at
			last = &a
		}
	}
	return last, nil
}

// Ping is the twin's readiness check: always ready.
func (m *MemoryStore) Ping(context.Context) error { return nil }
