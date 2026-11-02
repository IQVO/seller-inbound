//go:build integration

package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type rawAnswer struct {
	status   int
	body     string
	replayed string
}

// send performs one request with the given Idempotency-Key and reports the
// status, body and Idempotency-Replayed header.
func (s *service) send(t *testing.T, method, path, body, key string) rawAnswer {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return rawAnswer{resp.StatusCode, string(raw), resp.Header.Get("Idempotency-Replayed")}
}

// phaseIdempotency proves the fleet REST guideline section 2 on the real router
// and the real Postgres store: global key, byte-identical replay with
// Idempotency-Replayed, 409 reuse (other body and other route), 400 for a
// missing or malformed key, a cached 4xx, and one effect under concurrency.
func phaseIdempotency(t *testing.T, s *service, w *world) {
	t.Helper()
	const onboard = `{"sellerId":"seller-idem","name":"Idem Goods"}`
	first := s.send(t, http.MethodPost, "/sellers", onboard, "idem-1")
	if first.status != http.StatusCreated || first.replayed != "" {
		t.Fatalf("first = %d replayed=%q %s", first.status, first.replayed, first.body)
	}
	again := s.send(t, http.MethodPost, "/sellers", onboard, "idem-1")
	if again.status != first.status || again.body != first.body || again.replayed != "true" {
		t.Fatalf("replay = %d replayed=%q, want the first answer byte for byte with Idempotency-Replayed: true", again.status, again.replayed)
	}
	if n := w.db.count(`SELECT count(*) FROM sellers WHERE seller_id='seller-idem'`); n != 1 {
		t.Fatalf("sellers after a replay = %d, want 1", n)
	}
	// Same key, different body: 409 idempotency-key-reused.
	diff := s.send(t, http.MethodPost, "/sellers", `{"sellerId":"seller-idem2","name":"Other"}`, "idem-1")
	if diff.status != http.StatusConflict || diff.replayed != "" {
		t.Fatalf("different body = %d %s, want 409", diff.status, diff.body)
	}
	assertProblem(t, diff.body, "idempotency-key-reused")
	// The key is global: the same key on another route is a reuse, not a new key.
	other := s.send(t, http.MethodPost, "/sellers/seller-idem/suspensions", "", "idem-1")
	if other.status != http.StatusConflict {
		t.Fatalf("same key on another route = %d %s, want 409", other.status, other.body)
	}
	assertProblem(t, other.body, "idempotency-key-reused")
	// Missing and malformed keys are 400.
	assertProblem(t, s.send(t, http.MethodPost, "/sellers", onboard, "").body, "idempotency-key-required")
	if got := s.send(t, http.MethodPost, "/sellers", onboard, ""); got.status != http.StatusBadRequest {
		t.Fatalf("missing key = %d", got.status)
	}
	for _, key := range []string{strings.Repeat("k", 129), "café"} {
		got := s.send(t, http.MethodPost, "/sellers", onboard, key)
		if got.status != http.StatusBadRequest {
			t.Fatalf("key %q = %d, want 400", key, got.status)
		}
		assertProblem(t, got.body, "idempotency-key-invalid")
	}
	// A 4xx is cached and replayed.
	bad := s.send(t, http.MethodPost, "/sellers/ghost-seller/suspensions", "", "idem-404")
	if bad.status != http.StatusNotFound {
		t.Fatalf("ghost suspend = %d", bad.status)
	}
	if rep := s.send(t, http.MethodPost, "/sellers/ghost-seller/suspensions", "", "idem-404"); rep.status != http.StatusNotFound || rep.replayed != "true" || rep.body != bad.body {
		t.Fatalf("cached 4xx replay = %d replayed=%q", rep.status, rep.replayed)
	}
	// N concurrent requests with one key: exactly one effect, the rest replay.
	const n = 8
	const conc = `{"sellerId":"seller-conc","name":"Concurrent Goods"}`
	var wg sync.WaitGroup
	answers := make([]rawAnswer, n)
	for i := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			answers[i] = s.send(t, http.MethodPost, "/sellers", conc, "idem-conc")
		}()
	}
	wg.Wait()
	replays := 0
	for _, a := range answers {
		if a.status != http.StatusCreated || a.body != answers[0].body {
			t.Fatalf("concurrent answer = %d %s", a.status, a.body)
		}
		if a.replayed == "true" {
			replays++
		}
	}
	if replays != n-1 {
		t.Fatalf("replays = %d, want %d (exactly one execution)", replays, n-1)
	}
	if c := w.db.count(`SELECT count(*) FROM sellers WHERE seller_id='seller-conc'`); c != 1 {
		t.Fatalf("sellers after %d concurrent requests = %d, want 1", n, c)
	}
	if c := w.db.count(`SELECT count(*) FROM idempotency_keys WHERE key='idem-conc'`); c != 1 {
		t.Fatalf("idempotency rows for one key = %d, want 1", c)
	}
	// The retired routes are gone.
	for _, p := range []string{"/sellers/seller-idem:suspend", "/sellers/seller-idem:reinstate", "/inbound-plans/x:confirm",
		"/inbound-plans/x:cancel", "/inbound-shipments/x:label", "/inbound-shipments/x:ship"} {
		if got := s.send(t, http.MethodPost, p, "", "retired-"+p); got.status != http.StatusNotFound && got.status != http.StatusMethodNotAllowed {
			t.Fatalf("retired route %s = %d, want 404 or 405", p, got.status)
		}
	}
}
