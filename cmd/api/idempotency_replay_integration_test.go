//go:build integration

// The replay-class rule (docs/adr/0018-every-problem-type-has-a-replay-class.md) through the REAL
// chi router, the real use cases and a real Postgres 16: a refusal that nothing committed and that
// depends on state that can change (RETRYABLE) is NOT stored, so a retry under the same
// Idempotency-Key executes; a refusal that is a function of the request alone (FINAL) is stored
// and replayed.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	inboundhttp "github.com/claudioed/seller-inbound/internal/adapters/inbound/http"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/postgres"
	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
)

// racingSellers stands for a competing writer: while armed, every Save of an existing seller is
// preceded by a COMMITTED version bump on a second connection, so the request's own save finds
// zero rows (ports.ErrConcurrentUpdate), exactly as a lost optimistic-lock race.
type racingSellers struct {
	ports.SellerRepository
	pool      *pgxpool.Pool
	competing atomic.Int32 // how many upcoming saves lose the race
}

func (r *racingSellers) Save(ctx context.Context, a *seller.Account) error {
	if a.Snapshot().Version > 1 && r.competing.Load() > 0 {
		r.competing.Add(-1)
		if _, err := r.pool.Exec(context.Background(),
			`UPDATE sellers SET version = version + 1 WHERE seller_id = $1`, string(a.Snapshot().ID)); err != nil {
			return err
		}
	}
	return r.SellerRepository.Save(ctx, a)
}

// replayFixture is the shipped wiring (wire_adapters.go) over a fresh Postgres, with only the
// seller repository decorated to lose a race on demand.
type replayFixture struct {
	pool   *pgxpool.Pool
	router http.Handler
	races  *racingSellers
}

func newReplayFixture(t *testing.T) replayFixture {
	t.Helper()
	_, url := startPostgres(t)
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	pool, err := postgres.NewPool(context.Background(), url)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	ad := postgresAdapters(pool, nil)
	races := &racingSellers{SellerRepository: ad.deps.Sellers, pool: pool}
	ad.deps.Sellers = races
	svc := buildService(ad, nil)
	router := inboundhttp.NewRouter(&inboundhttp.Server{Service: svc, Idempotency: ad.idempotency}, nil, "")
	return replayFixture{pool: pool, router: router, races: races}
}

func (f replayFixture) post(path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f replayFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (f replayFixture) keyRows(t *testing.T, key string) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

func problemSlug(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem document: %q: %v", rec.Body.String(), err)
	}
	return p.Type
}

func notReplayed(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Get(inboundhttp.IdempotencyReplayedHeader); got != "" {
		t.Fatalf("a fresh execution carries %s: %q", inboundhttp.IdempotencyReplayedHeader, got)
	}
}

func replayOf(t *testing.T, first, again *httptest.ResponseRecorder) {
	t.Helper()
	if again.Code != first.Code || again.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %s, want the stored %d %s", again.Code, again.Body.String(), first.Code, first.Body.String())
	}
	if got := again.Header().Get(inboundhttp.IdempotencyReplayedHeader); got != "true" {
		t.Fatalf("replay %s = %q, want true", inboundhttp.IdempotencyReplayedHeader, got)
	}
}

// RETRYABLE: the save loses an optimistic-lock race twice (the use case re-reads and retries once
// before it answers 409 concurrent-update). Nothing was written, so the key row is gone; the retry
// under the SAME key executes, succeeds, and only then is stored and replayed, with one effect.
func TestReplayClass_ConcurrentUpdateLoserIsReleasedAndItsRetryExecutes(t *testing.T) {
	fx := newReplayFixture(t)
	if rec := fx.post("/sellers", "seed-race", `{"sellerId":"seller-race","name":"Race Goods"}`); rec.Code != http.StatusCreated {
		t.Fatalf("seed = %d %s", rec.Code, rec.Body.String())
	}
	events := fx.count(t, `SELECT count(*) FROM outbox_events`)
	suspend := "/sellers/seller-race/suspensions"
	body := `{"reason":"audit"}`

	fx.races.competing.Store(2)
	loser := fx.post(suspend, "race-loser", body)
	if loser.Code != http.StatusConflict || problemSlug(t, loser) != "concurrent-update" {
		t.Fatalf("loser = %d %s, want 409 concurrent-update", loser.Code, loser.Body.String())
	}
	notReplayed(t, loser)
	if n := fx.keyRows(t, "race-loser"); n != 0 {
		t.Fatalf("key rows of the loser = %d, want 0: a RETRYABLE refusal is not stored", n)
	}
	if n := fx.count(t, `SELECT count(*) FROM outbox_events`); n != events {
		t.Fatalf("outbox rows after the refusal = %d, want %d: nothing committed", n, events)
	}

	retry := fx.post(suspend, "race-loser", body)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry under the same key = %d %s, want 200: the retry must execute", retry.Code, retry.Body.String())
	}
	notReplayed(t, retry)
	if n := fx.keyRows(t, "race-loser"); n != 1 {
		t.Fatalf("key rows after the successful retry = %d, want 1: a 2xx is stored", n)
	}
	replayOf(t, retry, fx.post(suspend, "race-loser", body))
	if n := fx.count(t, `SELECT count(*) FROM sellers WHERE seller_id = 'seller-race' AND state = 'SUSPENDED'`); n != 1 {
		t.Fatalf("suspended sellers = %d, want 1", n)
	}
	if n := fx.count(t, `SELECT count(*) FROM outbox_events`); n != events+1 {
		t.Fatalf("outbox rows after the retry and its replay = %d, want %d (one suspension event, once)", n, events+1)
	}
}

// RETRYABLE: a seller that is not onboarded yet is refused 404 not-found; the fact can arrive
// before the caller retries, so the refusal must not pin the key: the SAME key succeeds once the
// seller exists. A released key is forgotten: another body under it is a new request.
func TestReplayClass_NotFoundIsReleasedAndTheSameKeySucceedsOnceTheTargetExists(t *testing.T) {
	fx := newReplayFixture(t)
	suspend := "/sellers/seller-late/suspensions"

	first := fx.post(suspend, "late-seller", "")
	if first.Code != http.StatusNotFound || problemSlug(t, first) != "not-found" {
		t.Fatalf("first = %d %s, want 404 not-found", first.Code, first.Body.String())
	}
	notReplayed(t, first)
	if n := fx.keyRows(t, "late-seller"); n != 0 {
		t.Fatalf("key rows after a RETRYABLE refusal = %d, want 0: the key must be released", n)
	}

	if rec := fx.post("/sellers", "onboard-late", `{"sellerId":"seller-late","name":"Late Goods"}`); rec.Code != http.StatusCreated {
		t.Fatalf("onboard = %d %s", rec.Code, rec.Body.String())
	}
	retry := fx.post(suspend, "late-seller", "")
	if retry.Code != http.StatusOK {
		t.Fatalf("retry under the same key = %d %s, want 200: the retry must execute", retry.Code, retry.Body.String())
	}
	notReplayed(t, retry)
	replayOf(t, retry, fx.post(suspend, "late-seller", ""))

	// A refusal whose key was released: a different body under it is a NEW request, not a reuse.
	if refused := fx.post("/sellers/seller-ghost/suspensions", "free-key", ""); refused.Code != http.StatusNotFound {
		t.Fatalf("setup: %d %s", refused.Code, refused.Body.String())
	}
	got := fx.post("/sellers", "free-key", `{"sellerId":"seller-free","name":"Free Goods"}`)
	if got.Code != http.StatusCreated {
		t.Fatalf("another request under a released key = %d %s, want a new request (201), not idempotency-key-reused", got.Code, got.Body.String())
	}
}

// FINAL: a coverDays outside the allowed range is a function of the request alone: stored under the
// key and replayed with Idempotency-Replayed, byte for byte, and the key is NOT released.
func TestReplayClass_InvalidCoverDaysIsStoredAndReplayed(t *testing.T) {
	fx := newReplayFixture(t)
	body := `{"sellerId":"seller-cover","catalogItemId":"item-1","coverDays":1000}`

	first := fx.post("/placement-plans", "cover-final", body)
	if first.Code != http.StatusBadRequest || problemSlug(t, first) != "invalid-cover-days" {
		t.Fatalf("first = %d %s, want 400 invalid-cover-days", first.Code, first.Body.String())
	}
	notReplayed(t, first)
	if n := fx.keyRows(t, "cover-final"); n != 1 {
		t.Fatalf("key rows after a FINAL refusal = %d, want 1: it is stored", n)
	}
	replayOf(t, first, fx.post("/placement-plans", "cover-final", body))
}
