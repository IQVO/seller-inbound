//go:build integration

package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/seller-inbound/internal/analytics/report"
)

// One analytical Postgres per test binary (testcontainers, never skipped, never
// an env DSN); every test gets its own database.
var (
	pgOnce     sync.Once
	pgC        *tcpostgres.PostgresContainer
	errPGStart error
	dbSeq      atomic.Int64
)

func adminURL(t *testing.T) string {
	t.Helper()
	pgOnce.Do(func() {
		pgC, errPGStart = tcpostgres.Run(context.Background(), "postgres:16-alpine",
			tcpostgres.WithDatabase("postgres"), tcpostgres.WithUsername("analytics"), tcpostgres.WithPassword("analytics"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	})
	if errPGStart != nil {
		t.Fatalf("start postgres: %v", errPGStart)
	}
	url, err := pgC.ConnectionString(context.Background(), "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// freshURL creates an empty database and returns its DSN.
func freshURL(t *testing.T) string {
	t.Helper()
	admin := adminURL(t)
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	name := fmt.Sprintf("analytics_%d", dbSeq.Add(1))
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// migratedPool returns a writer pool on a fresh, migrated database. The
// migrations run twice: every projector boot runs them, the second is a no-op.
func migratedPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url := freshURL(t)
	for range 2 {
		if err := analyticsstore.RunMigrations(url); err != nil {
			t.Fatalf("migrations: %v", err)
		}
	}
	pool, err := analyticsstore.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool, url
}

type pgStore struct {
	*analyticsstore.Projection
	*analyticsstore.Reader
}

func TestPostgresStoreContract(t *testing.T) {
	runContract(t, func(t *testing.T) store {
		pool, _ := migratedPool(t)
		return pgStore{analyticsstore.NewProjection(pool), analyticsstore.NewReader(pool)}
	})
}

func TestAnalyticalMigrationsGoUpDownAndUp(t *testing.T) {
	_, url := migratedPool(t)
	if err := analyticsstore.RollbackMigrations(url); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := analyticsstore.RunMigrations(url); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// receiptEvent is a receipt with one line of gtin.
func receiptEvent(id, gtin string) report.Event {
	return report.Event{Kind: report.KindInboundShipmentReceived, EventID: id, SubjectID: "shp-1", SellerID: "s1", At: nov3, OccurredAt: nov3,
		Lines: []report.ReceivedLine{{GTIN: gtin, CatalogItemID: "item-1", QtyEaches: 3}}}
}

// The dedupe mark and the projection are ONE transaction: when the LAST fact
// insert of a receipt fails, the claim and the receipt row are rolled back
// too, so the redelivery applies it.
func TestDedupeMarkAndProjectionAreOneTransaction(t *testing.T) {
	pool, _ := migratedPool(t)
	for _, ddl := range []string{
		`CREATE FUNCTION refuse_line() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'line refused by test'; END $$`,
		`CREATE TRIGGER refuse_line BEFORE INSERT ON received_stock_lines FOR EACH ROW EXECUTE FUNCTION refuse_line()`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	p := analyticsstore.NewProjection(pool)
	e := receiptEvent("evt-atomic", "00614141000036")
	applied, err := p.Apply(ctx, e)
	if err == nil || applied || errors.Is(err, report.ErrRejected) {
		t.Fatalf("a raised exception must be a transient failure: applied=%v err=%v", applied, err)
	}
	if countRows(t, pool, "analytics_processed_events") != 0 || countRows(t, pool, "shipment_receipts") != 0 {
		t.Fatal("the claim or the receipt survived a failed projection")
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_line ON received_stock_lines`); err != nil {
		t.Fatal(err)
	}
	if applied, err := p.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("the redelivery must apply: applied=%v err=%v", applied, err)
	}
	if countRows(t, pool, "analytics_processed_events") != 1 || countRows(t, pool, "shipment_receipts") != 1 || countRows(t, pool, "received_stock_lines") != 1 {
		t.Fatal("the redelivery must leave exactly one mark, one receipt and one line")
	}
}

// A store refusal in SQLSTATE class 23 is deterministic (ErrRejected, the
// consumer dead-letters it) and leaves no mark.
func TestIntegrityViolationIsRejected(t *testing.T) {
	pool, _ := migratedPool(t)
	if _, err := pool.Exec(ctx, `ALTER TABLE received_stock_lines ADD CONSTRAINT test_no_bogus CHECK (gtin <> 'BOGUS')`); err != nil {
		t.Fatal(err)
	}
	applied, err := analyticsstore.NewProjection(pool).Apply(ctx, receiptEvent("evt-bogus", "BOGUS"))
	if applied || !errors.Is(err, report.ErrRejected) {
		t.Fatalf("applied=%v err=%v; want ErrRejected", applied, err)
	}
	if countRows(t, pool, "analytics_processed_events") != 0 || countRows(t, pool, "shipment_receipts") != 0 {
		t.Fatal("a rejected fact left a mark or a receipt")
	}
}

func TestReadOnlyPoolRefusesWrites(t *testing.T) {
	_, url := migratedPool(t)
	ro, err := analyticsstore.NewReadOnlyPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.Exec(ctx, `INSERT INTO analytics_processed_events (event_id, event_type, occurred_at) VALUES ('x', 'y', now())`); err == nil {
		t.Fatal("the reports pool must refuse a write")
	}
	if err := analyticsstore.NewReader(ro).Ping(ctx); err != nil {
		t.Fatalf("the reports pool must still read: %v", err)
	}
}
