package report_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

func fp(v float64) *float64 { return &v }

func TestPerformanceDaysMergesTheThreeSourcesPerDay(t *testing.T) {
	d3, d4, d5 := utcDay(2026, 11, 3), utcDay(2026, 11, 4), utcDay(2026, 11, 5)
	got := report.PerformanceDays(
		[]report.DayCounts{
			{Day: d5, PlansConfirmed: 1},
			{Day: d3, PlansConfirmed: 2, AdvisoriesIssued: 3, ShipmentsReceived: 4},
		},
		[]report.DayItem{
			{Day: d3, GTIN: "g-b", CatalogItemID: "item-b", Publications: 1, QtyEaches: 5},
			{Day: d3, GTIN: "g-a", Publications: 2, QtyEaches: 5},
			{Day: d3, GTIN: "g-c", CatalogItemID: "item-c", Publications: 4, QtyEaches: 9},
			{Day: d4, GTIN: "g-a", Publications: 1, QtyEaches: 1},
		},
		[]report.DayLatency{{Day: d3, Samples: 3, P50: 120, P95: 3600}},
	)
	want := []report.PerformanceRow{
		{
			Day: d3, PlansConfirmed: 2, AdvisoriesIssued: 3, ShipmentsReceived: 4, ReceivedEaches: 19, ReceivedItemCount: 3,
			ReceivedByItem: []report.ReceivedItem{
				{GTIN: "g-c", CatalogItemID: "item-c", Publications: 4, QtyEaches: 9},
				{GTIN: "g-a", Publications: 2, QtyEaches: 5},
				{GTIN: "g-b", CatalogItemID: "item-b", Publications: 1, QtyEaches: 5},
			},
			LatencySamples: 3, LatencyP50: fp(120), LatencyP95: fp(3600),
		},
		{Day: d4, ReceivedEaches: 1, ReceivedItemCount: 1, ReceivedByItem: []report.ReceivedItem{{GTIN: "g-a", Publications: 1, QtyEaches: 1}}},
		{Day: d5, PlansConfirmed: 1, ReceivedByItem: []report.ReceivedItem{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows =\n%+v\nwant\n%+v", got, want)
	}
}

func TestPerformanceDaysWithoutFactsIsEmptyAndNonNil(t *testing.T) {
	got := report.PerformanceDays(nil, nil, nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("no facts must yield an empty, non-nil slice, got %#v", got)
	}
}

func TestPerformanceDaysKeepsADayOnlyTheItemsOrTheLatencyKnow(t *testing.T) {
	d := utcDay(2026, 11, 3)
	got := report.PerformanceDays(nil, []report.DayItem{{Day: d, GTIN: "g", Publications: 1, QtyEaches: 2}}, nil)
	if len(got) != 1 || got[0].ReceivedEaches != 2 || got[0].LatencyP50 != nil || got[0].LatencyP95 != nil || got[0].LatencySamples != 0 {
		t.Fatalf("rows = %+v", got)
	}
	got = report.PerformanceDays(nil, nil, []report.DayLatency{{Day: d, Samples: 1, P50: 0, P95: 0}})
	if len(got) != 1 || got[0].LatencyP50 == nil || *got[0].LatencyP50 != 0 || got[0].LatencySamples != 1 {
		t.Fatalf("a day with one zero-second sample keeps its percentiles, rows = %+v", got)
	}
}

func TestPerformanceDaysBoundsTheItemListButCountsEveryItem(t *testing.T) {
	d := utcDay(2026, 11, 3)
	var items []report.DayItem
	for i := 1; i <= report.ReceivedItemsLimit()+3; i++ {
		items = append(items, report.DayItem{Day: d, GTIN: fmt.Sprintf("g-%02d", i), Publications: 1, QtyEaches: i})
	}
	got := report.PerformanceDays(nil, items, nil)
	row := got[0]
	if len(row.ReceivedByItem) != report.ReceivedItemsLimit() || row.ReceivedItemCount != report.ReceivedItemsLimit()+3 {
		t.Fatalf("listed %d, counted %d", len(row.ReceivedByItem), row.ReceivedItemCount)
	}
	if row.ReceivedByItem[0].QtyEaches != 13 || row.ReceivedByItem[9].QtyEaches != 4 {
		t.Fatalf("top of the list = %+v", row.ReceivedByItem)
	}
	total := 0
	for i := 1; i <= 13; i++ {
		total += i
	}
	if row.ReceivedEaches != total {
		t.Fatalf("ReceivedEaches = %d, want the sum over EVERY item %d", row.ReceivedEaches, total)
	}
}

func TestTopReceivedItems(t *testing.T) {
	in := []report.ReceivedItem{{GTIN: "b", QtyEaches: 2}, {GTIN: "a", QtyEaches: 2}, {GTIN: "c", QtyEaches: 7}}
	if got := report.TopReceivedItems(in, 2); !reflect.DeepEqual(got, []report.ReceivedItem{{GTIN: "c", QtyEaches: 7}, {GTIN: "a", QtyEaches: 2}}) {
		t.Errorf("limit 2 = %+v", got)
	}
	if got := report.TopReceivedItems(in, 3); len(got) != 3 || got[1].GTIN != "a" || got[2].GTIN != "b" {
		t.Errorf("limit equal to the length keeps everything, ties by GTIN: %+v", got)
	}
	if got := report.TopReceivedItems(in, 9); len(got) != 3 {
		t.Errorf("limit above the length = %+v", got)
	}
	if got := report.TopReceivedItems(in, 0); got == nil || len(got) != 0 {
		t.Errorf("limit 0 must yield an empty, non-nil slice, got %#v", got)
	}
	if got := report.TopReceivedItems(in, -1); got == nil || len(got) != 0 {
		t.Errorf("a negative limit must yield an empty, non-nil slice, got %#v", got)
	}
	if got := report.TopReceivedItems(nil, 5); got == nil || len(got) != 0 {
		t.Errorf("nil input must yield an empty, non-nil slice, got %#v", got)
	}
	if in[0].GTIN != "b" || in[2].GTIN != "c" {
		t.Errorf("TopReceivedItems modified its input: %+v", in)
	}
}

func TestPercentileMatchesPercentileCont(t *testing.T) {
	values := []float64{7, 1, 3, 5}
	cases := []struct{ p, want float64 }{{0.5, 4}, {0.95, 6.7}, {0, 1}, {1, 7}, {0.25, 2.5}}
	for _, c := range cases {
		if got := report.Percentile(values, c.p); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("Percentile(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	if !reflect.DeepEqual(values, []float64{7, 1, 3, 5}) {
		t.Fatalf("Percentile modified its input: %v", values)
	}
	if got := report.Percentile(nil, 0.5); got != 0 {
		t.Fatalf("empty = %v, want 0", got)
	}
	if got := report.Percentile([]float64{3}, 0.95); got != 3 {
		t.Fatalf("single = %v, want 3", got)
	}
}

func TestComputeFreshness(t *testing.T) {
	if f := report.ComputeFreshness(nil, now); f.AsOf != nil || f.LagSeconds != nil {
		t.Fatalf("nothing applied must give nil/nil, got %+v", f)
	}
	asOf := time.Date(2026, 11, 8, 18, 29, 0, 0, time.UTC) // now is 18:30 UTC
	f := report.ComputeFreshness(&asOf, now)
	if f.AsOf == nil || !f.AsOf.Equal(asOf) || f.AsOf.Location() != time.UTC || f.LagSeconds == nil || *f.LagSeconds != 60 {
		t.Fatalf("freshness = %+v", f)
	}
	future := now.Add(time.Minute)
	if f := report.ComputeFreshness(&future, now); *f.LagSeconds != 0 {
		t.Fatalf("a clock behind the event must read zero lag, got %v", *f.LagSeconds)
	}
}
