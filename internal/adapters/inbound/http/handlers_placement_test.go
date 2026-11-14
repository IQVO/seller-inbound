package http_test

import "testing"

// TestPlacementPlanCoverDaysProblem pins the problem of ADR 0012: a coverDays
// outside 1..90 is a 400 invalid-cover-days, not the internal-error problem
// that an unmapped typed error becomes. The in-range and default (0 -> 14)
// cases are use-case tests (placement_test.go).
func TestPlacementPlanCoverDaysProblem(t *testing.T) {
	a := newAPI(t)
	for _, bad := range []string{"-1", "91", "1000"} {
		a.do("POST", "/placement-plans", `{"sellerId":"S-1","catalogItemId":"item-1","coverDays":`+bad+`}`).
			problem(t, 400, "invalid-cover-days")
	}
}
