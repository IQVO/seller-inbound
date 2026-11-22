package report_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

func TestRulesArePinned(t *testing.T) {
	if report.ReceivedItemsLimit() != 10 {
		t.Errorf("ReceivedItemsLimit = %d, want 10", report.ReceivedItemsLimit())
	}
	if report.LatencyP50() != 0.5 || report.LatencyP95() != 0.95 {
		t.Errorf("percentile ranks = %v, %v, want 0.5, 0.95", report.LatencyP50(), report.LatencyP95())
	}
}

var at = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func base(kind report.Kind) report.Event {
	return report.Event{Kind: kind, EventID: "ev-1", At: at, SubjectID: "sub-1", SellerID: "seller-1", OccurredAt: at}
}

func TestValidateAcceptsEachKind(t *testing.T) {
	adv := base(report.KindPlacementPlanSuggested)
	adv.CatalogItemID = "item-1"
	rec := base(report.KindInboundShipmentReceived)
	rec.Lines = []report.ReceivedLine{{GTIN: "00614141000036", QtyEaches: 1}, {GTIN: "00614141000012", CatalogItemID: "item-2", QtyEaches: 9}}
	empty := base(report.KindInboundShipmentReceived) // a receipt without `received` (before ADR 0014) is valid
	for name, e := range map[string]report.Event{
		"plan confirmed": base(report.KindInboundPlanConfirmed), "advisory": adv, "receipt": rec, "receipt without lines": empty,
	} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: Validate = %v, want nil", name, err)
		}
	}
}

func TestValidateRejectsWhatCanNeverBeProjected(t *testing.T) {
	with := func(kind report.Kind, mutate func(*report.Event)) report.Event {
		e := base(kind)
		mutate(&e)
		return e
	}
	cases := []struct {
		name string
		e    report.Event
		want string
	}{
		{"no id", with(report.KindInboundPlanConfirmed, func(e *report.Event) { e.EventID = "" }), "id is required"},
		{"no time", with(report.KindInboundPlanConfirmed, func(e *report.Event) { e.At = time.Time{} }), "time is required"},
		{"no aggregate id", with(report.KindInboundPlanConfirmed, func(e *report.Event) { e.SubjectID = "" }), "the aggregate id is required"},
		{"no seller", with(report.KindInboundPlanConfirmed, func(e *report.Event) { e.SellerID = "" }), "sellerId is required"},
		{"no instant", with(report.KindInboundPlanConfirmed, func(e *report.Event) { e.OccurredAt = time.Time{} }), "the business instant is required"},
		{"advisory without item", with(report.KindPlacementPlanSuggested, func(*report.Event) {}), "catalogItemId is required"},
		{"line without gtin", with(report.KindInboundShipmentReceived, func(e *report.Event) { e.Lines = []report.ReceivedLine{{QtyEaches: 2}} }), "received[].gtin is required"},
		{"line of zero eaches", with(report.KindInboundShipmentReceived, func(e *report.Event) {
			e.Lines = []report.ReceivedLine{{GTIN: "g", QtyEaches: 1}, {GTIN: "h"}}
		}), "received[].qtyEaches > 0 is required"},
		{"unknown kind", with("SomethingElse", func(*report.Event) {}), "a known kind is required"},
	}
	for _, c := range cases {
		err := c.e.Validate()
		if !errors.Is(err, report.ErrInvalidEvent) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Validate = %v, want ErrInvalidEvent containing %q", c.name, err, c.want)
		}
	}
}

func TestValidateNamesTheFirstMissingFieldInOrder(t *testing.T) {
	err := report.Event{Kind: report.KindInboundPlanConfirmed}.Validate()
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("an empty event must name the id first, got %v", err)
	}
}
