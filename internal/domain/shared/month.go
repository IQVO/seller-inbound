package shared

import "time"

// MonthOf is the calendar month key of an instant: UTC, "YYYY-MM". The
// inbound capacity reservations of ADR 0015 are keyed by it; a reservation
// lives in the month of the plan's createdAt, so the allocation rolls over
// without any sweeper.
func MonthOf(t time.Time) string { return t.UTC().Format("2006-01") }

// ValidMonth reports whether s is a "YYYY-MM" key.
func ValidMonth(s string) bool {
	if len(s) != 7 || s[4] != '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 4 {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	month := int(s[5]-'0')*10 + int(s[6]-'0')
	return month >= 1 && month <= 12
}
