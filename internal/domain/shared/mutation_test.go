package shared

import (
	"testing"
	"time"
)

func TestMonthOfIsUTCYearMonth(t *testing.T) {
	brt := time.FixedZone("-03", -3*3600)
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), "2026-11"},
		{time.Date(2026, 11, 30, 23, 30, 0, 0, brt), "2026-12"}, // 02:30 UTC next day: the month rolls
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "2026-01"},
		{time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), "2026-12"},
	}
	for _, c := range cases {
		if got := MonthOf(c.in); got != c.want {
			t.Fatalf("MonthOf(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestValidMonthEveryMonthNumber: only 01..12 are months.
func TestValidMonthEveryMonthNumber(t *testing.T) {
	for m := 0; m <= 99; m++ {
		s := "2026-" + string([]byte{byte('0' + m/10), byte('0' + m%10)})
		want := m >= 1 && m <= 12
		if got := ValidMonth(s); got != want {
			t.Fatalf("ValidMonth(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestValidMonthShape(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"2026-1", false},
		{"2026-011", false},
		{"2026-11-", false},
		{"20261-1", false},
		{"2026/11", false},
		{"2026 11", false},
		{"202611-", false},
		{"0000-01", true},
		{"9999-12", true},
		{"x026-11", false},
		{"2x26-11", false},
		{"20x6-11", false},
		{"202x-11", false},
		{"2026-x1", false},
		{"2026-1x", false},
		{"/026-11", false}, // '/' is just below '0'
		{":026-11", false}, // ':' is just above '9'
		{"202/-11", false},
		{"202:-11", false},
		{"2026-/1", false},
		{"2026-:1", false},
		{"2026-1/", false},
		{"2026-1:", false},
	}
	for _, c := range cases {
		if got := ValidMonth(c.in); got != c.want {
			t.Fatalf("ValidMonth(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
