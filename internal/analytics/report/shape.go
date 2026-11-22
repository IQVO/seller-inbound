package report

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// ReceivedItem is one item of a day's bounded `received_by_item` list.
type ReceivedItem struct {
	GTIN          string
	CatalogItemID string
	Publications  int
	QtyEaches     int
}

// PerformanceRow is one UTC day of the seller-inbound performance report.
// ReceivedEaches and ReceivedItemCount cover EVERY item of the day;
// ReceivedByItem is the top ReceivedItemsLimit of them. LatencySamples counts
// the received lines that had a preceding advisory; the two percentiles are nil
// when there is none.
type PerformanceRow struct {
	Day               time.Time
	PlansConfirmed    int
	AdvisoriesIssued  int
	ShipmentsReceived int
	ReceivedEaches    int
	ReceivedItemCount int
	ReceivedByItem    []ReceivedItem
	LatencySamples    int
	LatencyP50        *float64
	LatencyP95        *float64
}

// PerformanceDays merges the three per-day aggregates into one row per UTC day
// that has any fact, ordered by day. A day appears when any source has it;
// ReceivedByItem is never nil. A nil input yields an empty, non-nil slice.
func PerformanceDays(counts []DayCounts, items []DayItem, latencies []DayLatency) []PerformanceRow {
	byDay := map[time.Time]*PerformanceRow{}
	row := func(day time.Time) *PerformanceRow {
		if r, ok := byDay[day]; ok {
			return r
		}
		r := &PerformanceRow{Day: day, ReceivedByItem: []ReceivedItem{}}
		byDay[day] = r
		return r
	}
	for _, c := range counts {
		r := row(c.Day)
		r.PlansConfirmed, r.AdvisoriesIssued, r.ShipmentsReceived = c.PlansConfirmed, c.AdvisoriesIssued, c.ShipmentsReceived
	}
	perDay := map[time.Time][]ReceivedItem{}
	for _, it := range items {
		r := row(it.Day)
		r.ReceivedEaches += it.QtyEaches
		r.ReceivedItemCount++
		perDay[it.Day] = append(perDay[it.Day], ReceivedItem{GTIN: it.GTIN, CatalogItemID: it.CatalogItemID, Publications: it.Publications, QtyEaches: it.QtyEaches})
	}
	for day, list := range perDay {
		byDay[day].ReceivedByItem = TopReceivedItems(list, ReceivedItemsLimit())
	}
	for _, l := range latencies {
		r := row(l.Day)
		p50, p95 := l.P50, l.P95
		r.LatencySamples, r.LatencyP50, r.LatencyP95 = l.Samples, &p50, &p95
	}
	out := make([]PerformanceRow, 0, len(byDay))
	for _, r := range byDay {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}

// TopReceivedItems orders items by eaches received, most first, ties by GTIN
// ascending, and keeps at most limit of them. The input is not modified; the
// result is never nil.
func TopReceivedItems(items []ReceivedItem, limit int) []ReceivedItem {
	sorted := slices.Clone(items)
	slices.SortFunc(sorted, func(a, b ReceivedItem) int {
		if a.QtyEaches != b.QtyEaches {
			return b.QtyEaches - a.QtyEaches
		}
		return strings.Compare(a.GTIN, b.GTIN)
	})
	// min/max, not `if len > limit`: both give the same slice at the boundary,
	// which would leave an equivalent `>` -> `>=` mutant.
	return append([]ReceivedItem{}, sorted[:max(0, min(len(sorted), limit))]...)
}

// Percentile is the continuous percentile (linear interpolation between
// closest ranks, p in [0,1]) of values, the same definition as Postgres'
// percentile_cont. values need not be sorted; it is not modified. An empty
// input yields 0.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := p * float64(len(sorted)-1)
	lo := math.Floor(rank)
	hi := math.Ceil(rank)
	return sorted[int(lo)] + (sorted[int(hi)]-sorted[int(lo)])*(rank-lo)
}

// Freshness is the projection's freshness: AsOf is the CloudEvents time of the
// newest event applied and LagSeconds is now - AsOf. Both are nil while the
// projection has applied nothing.
type Freshness struct {
	AsOf       *time.Time
	LagSeconds *float64
}

// ComputeFreshness derives the lag from the newest applied event time. A clock
// that is behind the event (skew) reads as zero lag, never negative.
func ComputeFreshness(asOf *time.Time, now time.Time) Freshness {
	if asOf == nil {
		return Freshness{}
	}
	// math.Max, not `if lag < 0`: both yield 0 at the boundary, which would
	// leave an equivalent `<` -> `<=` mutant.
	lag := math.Max(0, now.Sub(*asOf).Seconds())
	utc := asOf.UTC()
	return Freshness{AsOf: &utc, LagSeconds: &lag}
}
