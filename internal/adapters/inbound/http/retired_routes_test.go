package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRetiredRoutesAreGone pins the REST guideline cutover: the ":verb" routes
// are removed outright (no alias), so each answers 404 or 405.
func TestRetiredRoutesAreGone(t *testing.T) {
	a := newAPI(t)
	for _, path := range []string{
		"/sellers/S-1:suspend",
		"/sellers/S-1:reinstate",
		"/inbound-plans/p-1:confirm",
		"/inbound-plans/p-1:cancel",
		"/inbound-shipments/s-1:label",
		"/inbound-shipments/s-1:ship",
	} {
		rec := httptest.NewRecorder()
		a.h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 404 or 405", path, rec.Code)
		}
	}
}
