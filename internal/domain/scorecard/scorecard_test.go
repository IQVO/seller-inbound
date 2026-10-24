package scorecard

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func f(v float64) *float64 { return &v }

func TestParseWindow(t *testing.T) {
	for in, want := range map[string]Window{"": Window30d, "7d": Window7d, "30d": Window30d} {
		got, err := ParseWindow(in)
		if err != nil || got != want {
			t.Errorf("ParseWindow(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"1d", "7D", "30", "7d ", " 7d", "90d", "0d", "-7d", "7days", "week"} {
		got, err := ParseWindow(in)
		if !errors.Is(err, ErrInvalidWindow) || !errors.Is(err, shared.ErrValidation) || got != "" {
			t.Errorf("ParseWindow(%q) = %q, %v; want ErrInvalidWindow", in, got, err)
		}
	}
}

func TestWindowDaysAndSince(t *testing.T) {
	if Window7d.Days() != 7 || Window30d.Days() != 30 {
		t.Fatalf("days: %d %d", Window7d.Days(), Window30d.Days())
	}
	if got, want := Window7d.Since(now), now.Add(-7*24*time.Hour); !got.Equal(want) {
		t.Errorf("7d since = %v, want %v", got, want)
	}
	if got, want := Window30d.Since(now), time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("30d since = %v, want %v", got, want)
	}
	// An instant in another zone gives the same UTC boundary.
	local := now.In(time.FixedZone("x", -3*3600))
	if got := Window7d.Since(local); got.Location() != time.UTC || !got.Equal(Window7d.Since(now)) {
		t.Errorf("since must be UTC and zone independent: %v", got)
	}
}

func TestComputeZeroShipmentsOmitsRates(t *testing.T) {
	c := Compute(Window7d, now, Counts{})
	if c.ReceiptAccuracyRate != nil || c.DiscrepancyRatePerShipment != nil {
		t.Fatalf("rates must be nil with nothing observed: %v %v", c.ReceiptAccuracyRate, c.DiscrepancyRatePerShipment)
	}
	if c.DiscrepanciesByKind == nil || len(c.DiscrepanciesByKind) != 0 {
		t.Fatalf("byKind must be a non-nil empty map: %#v", c.DiscrepanciesByKind)
	}
	if c.Tier != TierGood || c.ComplianceViolations != 0 || c.ShipmentsReceived != 0 {
		t.Fatalf("%+v", c)
	}
	if c.Window != Window7d || !c.Since.Equal(Window7d.Since(now)) || !c.Until.Equal(now) {
		t.Fatalf("bounds: %+v", c)
	}
}

func TestComputeRatesNeverNaN(t *testing.T) {
	// Discrepancies with no closed shipment, cartons received without a report.
	c := Compute(Window30d, now, Counts{Discrepancies: map[string]int{"Short": 3}, Violations: 2})
	if c.DiscrepancyRatePerShipment != nil || c.ReceiptAccuracyRate != nil {
		t.Fatalf("a zero denominator must omit the rate: %+v", c)
	}
	if c.DiscrepanciesByKind["Short"] != 3 || c.ComplianceViolations != 2 {
		t.Fatalf("%+v", c)
	}
	// A shipment that reported no carton has a rate per shipment but no accuracy.
	c = Compute(Window30d, now, Counts{ShipmentsReceived: 1})
	if c.ReceiptAccuracyRate != nil || c.DiscrepancyRatePerShipment == nil || *c.DiscrepancyRatePerShipment != 0 {
		t.Fatalf("%+v", c)
	}
	for _, r := range []*float64{c.ReceiptAccuracyRate, c.DiscrepancyRatePerShipment} {
		if r != nil && (math.IsNaN(*r) || math.IsInf(*r, 0)) {
			t.Fatalf("non-finite rate %v", *r)
		}
	}
}

func TestComputeValuesAndRounding(t *testing.T) {
	c := Compute(Window30d, now, Counts{
		ShipmentsReceived: 3, CartonsReported: 3, CartonsReceived: 2,
		Discrepancies: map[string]int{"MissingCarton": 1, "Short": 1}, Violations: 0,
	})
	if c.ReceiptAccuracyRate == nil || *c.ReceiptAccuracyRate != 0.6667 {
		t.Errorf("accuracy 2/3 rounds to 0.6667: %v", c.ReceiptAccuracyRate)
	}
	if c.DiscrepancyRatePerShipment == nil || *c.DiscrepancyRatePerShipment != 0.6667 {
		t.Errorf("per shipment 2/3 rounds to 0.6667: %v", c.DiscrepancyRatePerShipment)
	}
	c = Compute(Window30d, now, Counts{ShipmentsReceived: 3, CartonsReported: 3, CartonsReceived: 1, Discrepancies: map[string]int{"Short": 1}})
	if *c.ReceiptAccuracyRate != 0.3333 || *c.DiscrepancyRatePerShipment != 0.3333 {
		t.Errorf("1/3 rounds to 0.3333: %v %v", *c.ReceiptAccuracyRate, *c.DiscrepancyRatePerShipment)
	}
	c = Compute(Window30d, now, Counts{ShipmentsReceived: 4, CartonsReported: 10, CartonsReceived: 10, Discrepancies: map[string]int{"Damaged": 2, "Over": 3}})
	if *c.ReceiptAccuracyRate != 1 || *c.DiscrepancyRatePerShipment != 1.25 {
		t.Errorf("%v %v", *c.ReceiptAccuracyRate, *c.DiscrepancyRatePerShipment)
	}
	if c.CartonsReported != 10 || c.CartonsReceived != 10 || c.ShipmentsReceived != 4 {
		t.Errorf("%+v", c)
	}
}

func TestComputeCopiesTheKindMap(t *testing.T) {
	in := map[string]int{"Short": 1}
	c := Compute(Window7d, now, Counts{ShipmentsReceived: 1, Discrepancies: in})
	in["Short"] = 99
	in["Over"] = 1
	if len(c.DiscrepanciesByKind) != 1 || c.DiscrepanciesByKind["Short"] != 1 {
		t.Fatalf("the card must not alias the input: %#v", c.DiscrepanciesByKind)
	}
}

func TestTierThresholdsAreDeclared(t *testing.T) {
	// The numbers ADR 0005 A3 documents; a change is a new ADR.
	if WatchAccuracyBelow != 0.98 || AtRiskAccuracyBelow != 0.90 || WatchDiscrepancyRate != 0.5 ||
		AtRiskDiscrepancyRate != 2.0 || WatchViolations != 1 || AtRiskViolations != 5 {
		t.Fatal("thresholds drifted from ADR 0005 A3")
	}
}

func TestTierAccuracyEdges(t *testing.T) {
	cases := []struct {
		acc  float64
		want Tier
	}{
		{1, TierGood},
		{0.98, TierGood}, // exactly the watch threshold is not below it
		{0.9799, TierWatch},
		{0.90, TierWatch}, // exactly the at-risk threshold is not below it
		{0.8999, TierAtRisk},
		{0, TierAtRisk},
	}
	for _, c := range cases {
		if got := TierFor(f(c.acc), nil, 0); got != c.want {
			t.Errorf("accuracy %v: %s, want %s", c.acc, got, c.want)
		}
	}
}

func TestTierDiscrepancyRateEdges(t *testing.T) {
	cases := []struct {
		rate float64
		want Tier
	}{
		{0, TierGood},
		{0.4999, TierGood},
		{0.5, TierWatch}, // at the watch threshold counts
		{1.9999, TierWatch},
		{2.0, TierAtRisk}, // at the at-risk threshold counts
		{10, TierAtRisk},
	}
	for _, c := range cases {
		if got := TierFor(nil, f(c.rate), 0); got != c.want {
			t.Errorf("rate %v: %s, want %s", c.rate, got, c.want)
		}
	}
}

func TestTierViolationEdges(t *testing.T) {
	cases := []struct {
		n    int
		want Tier
	}{{0, TierGood}, {1, TierWatch}, {4, TierWatch}, {5, TierAtRisk}, {6, TierAtRisk}}
	for _, c := range cases {
		if got := TierFor(nil, nil, c.n); got != c.want {
			t.Errorf("%d violations: %s, want %s", c.n, got, c.want)
		}
	}
}

func TestTierIsTheWorstSignal(t *testing.T) {
	// Each signal alone drives its tier even when the other two are clean.
	if got := TierFor(f(1), f(0), 0); got != TierGood {
		t.Errorf("clean: %s", got)
	}
	if got := TierFor(f(0.5), f(0), 0); got != TierAtRisk {
		t.Errorf("accuracy alone: %s", got)
	}
	if got := TierFor(f(1), f(3), 0); got != TierAtRisk {
		t.Errorf("rate alone: %s", got)
	}
	if got := TierFor(f(1), f(0), 9); got != TierAtRisk {
		t.Errorf("violations alone: %s", got)
	}
	// A WATCH signal never lowers an AT_RISK one, in any position.
	if got := TierFor(f(0.95), f(0.1), 5); got != TierAtRisk {
		t.Errorf("watch accuracy + at-risk violations: %s", got)
	}
	if got := TierFor(f(0.95), f(2), 0); got != TierAtRisk {
		t.Errorf("watch accuracy + at-risk rate: %s", got)
	}
	if got := TierFor(f(0.5), f(0.6), 1); got != TierAtRisk {
		t.Errorf("at-risk accuracy + watch others: %s", got)
	}
	if got := TierFor(f(1), f(0.6), 0); got != TierWatch {
		t.Errorf("watch rate alone: %s", got)
	}
	if got := TierFor(f(1), f(0), 1); got != TierWatch {
		t.Errorf("watch violations alone: %s", got)
	}
	if got := TierFor(f(0.97), f(0), 0); got != TierWatch {
		t.Errorf("watch accuracy alone: %s", got)
	}
}

func TestTierUsesTheUnroundedRate(t *testing.T) {
	// 4899/5000 = 0.9798 -> WATCH; 98/100 = 0.98 -> GOOD. Rounding to 4 decimals
	// must never move a seller across a threshold: 49999/50000 rounds to 1.
	c := Compute(Window30d, now, Counts{ShipmentsReceived: 1, CartonsReported: 100000, CartonsReceived: 97999})
	if c.Tier != TierWatch {
		t.Errorf("0.97999 is below 0.98 even though it rounds to 0.98: %s (%v)", c.Tier, *c.ReceiptAccuracyRate)
	}
	c = Compute(Window30d, now, Counts{ShipmentsReceived: 1, CartonsReported: 50, CartonsReceived: 49})
	if c.Tier != TierGood {
		t.Errorf("49/50 is exactly 0.98: %s", c.Tier)
	}
	c = Compute(Window30d, now, Counts{ShipmentsReceived: 100000, Discrepancies: map[string]int{"Short": 49999}})
	if c.Tier != TierGood {
		t.Errorf("0.49999 per shipment rounds to 0.5 but is below it: %s (%v)", c.Tier, *c.DiscrepancyRatePerShipment)
	}
}

func TestComputeDerivesTheTierFromTheCounts(t *testing.T) {
	cases := []struct {
		name string
		in   Counts
		want Tier
	}{
		{"nothing", Counts{}, TierGood},
		{"clean shipment", Counts{ShipmentsReceived: 1, CartonsReported: 4, CartonsReceived: 4}, TierGood},
		{"one violation only", Counts{Violations: 1}, TierWatch},
		{"five violations only", Counts{Violations: 5}, TierAtRisk},
		{"half the shipments differ", Counts{ShipmentsReceived: 2, CartonsReported: 50, CartonsReceived: 50, Discrepancies: map[string]int{"Short": 1}}, TierWatch},
		{"two a shipment", Counts{ShipmentsReceived: 2, CartonsReported: 50, CartonsReceived: 50, Discrepancies: map[string]int{"Short": 3, "Over": 1}}, TierAtRisk},
		{"one carton in ten missing", Counts{ShipmentsReceived: 1, CartonsReported: 10, CartonsReceived: 9}, TierWatch},
		{"two in ten missing", Counts{ShipmentsReceived: 1, CartonsReported: 10, CartonsReceived: 8}, TierAtRisk},
	}
	for _, c := range cases {
		if got := Compute(Window30d, now, c.in).Tier; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
