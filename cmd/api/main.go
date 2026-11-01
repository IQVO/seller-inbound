// Command api is seller-inbound's composition root: it wires env config into
// adapters, adapters into the application service, and the service into the
// seller REST API, the three Kafka consumers (ADR 0003), the outbox relay and
// the retention loop, all on real Postgres and real Kafka: there is no
// in-memory mode, no log sink and no consumer that is off.
//
// Environment:
//
//	HTTP_ADDR                    listen address (default :8080)
//	DATABASE_URL                 Postgres DSN (REQUIRED)
//	MIGRATIONS_DATABASE_URL      direct DSN for the boot migrations (default DATABASE_URL)
//	KAFKA_BROKERS                comma-separated brokers (REQUIRED: relay, consumers, /readyz)
//	EVENT_PUBLISHER              kafka (default kafka); log is a boot error
//	NODE_CONSUMER_GROUP, CATALOG_CONSUMER_GROUP, RECEIPT_CONSUMER_GROUP
//	                             stable group ids (ALL REQUIRED, boot error naming the variable)
//	SSCC_COMPANY_PREFIX          the network's GS1 company prefix, 6 to 11 digits (REQUIRED)
//	SSCC_EXTENSION_DIGIT         SSCC extension digit 0..9 (default 0, ADR 0002 A5)
//	OUTBOX_RELAY_INTERVAL        relay poll interval (Go duration or seconds, default 1s)
//	OUTBOX_MAX_ATTEMPTS          failures before the relay excludes a row (default 20, 0 = unlimited)
//	SHUTDOWN_DRAIN_DELAY         wait after flipping /readyz before closing (default 5s)
//	LOG_LEVEL                    debug|info|warn|error (default info)
//	OTEL_SERVICE_NAME            service.name of spans and metrics (default seller-inbound)
//	OTEL_EXPORTER_OTLP_ENDPOINT  Collector OTLP/gRPC endpoint (default telemetry.DefaultEndpoint); export never blocks
//	SERVICE_VERSION              service.version (default dev)
//	ENVIRONMENT                  deployment.environment.name (default local)
//
// GET /readyz is 200 only while Postgres answers a ping AND a Kafka broker
// answers a metadata request (verdict cached 3s, bounded 2s); /healthz is
// liveness only. The process exits non-zero when the relay, a consumer or a
// loop returns on its own, so the orchestrator restarts it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inboundhttp "github.com/claudioed/seller-inbound/internal/adapters/inbound/http"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/telemetry"
)

// shutdownTimeout bounds the HTTP server's graceful drain.
const shutdownTimeout = 10 * time.Second

// workerDrainTimeout bounds how long shutdown waits for the consumers, the
// loops and the relay to return after their context is cancelled.
const workerDrainTimeout = 10 * time.Second

// DefaultShutdownDrainDelay is how long shutdown waits, after flipping
// /readyz to not-ready, before closing the listener (SHUTDOWN_DRAIN_DELAY
// overrides it; "0" disables it).
const DefaultShutdownDrainDelay = 5 * time.Second

// retentionSweep is how often expired Idempotency-Key rows are purged.
const retentionSweep = 24 * time.Hour

func main() {
	if err := run(context.Background()); err != nil {
		slog.Error("service exited with error", "error", err)
		os.Exit(1)
	}
}

// run boots the service and serves until SIGINT/SIGTERM or until ctx is
// cancelled (the integration test stops the real entry point that way).
func run(ctx context.Context) error {
	logger := newLogger(getenv("LOG_LEVEL", "info"))
	slog.SetDefault(logger)

	// Misconfiguration is refused before anything is opened or bound.
	rt, err := loadRuntime(os.Getenv)
	if err != nil {
		return err
	}

	// Telemetry comes up before the router is built (otelchimetric binds the
	// global MeterProvider at construction) and is flushed LAST: deferred before
	// everything below, so the final spans of the drain still leave the process.
	tel := loadTelemetryEnv(os.Getenv)
	flushTelemetry, err := startTelemetry(logger, tel)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	// closeAdapters (pool.Close) is deferred FIRST so it runs LAST, after the
	// HTTP drain, the consumers, the loops and the relay have all stopped.
	ad, closeAdapters, err := buildAdapters(ctx, os.Getenv("DATABASE_URL"), rt, logger)
	if err != nil {
		return err
	}
	defer closeAdapters()

	// The business counter binds to the MeterProvider telemetry.Setup installed.
	shipmentMetrics, err := telemetry.NewShipmentMetrics()
	if err != nil {
		return err
	}
	ad.deps.Metrics = shipmentMetrics

	svc := buildService(ad, logger)
	readiness := newReadiness(ad.pool.Ping, rt.brokers)
	httpServer := &http.Server{
		Addr: getenv("HTTP_ADDR", ":8080"),
		Handler: inboundhttp.NewRouter(&inboundhttp.Server{
			Service: svc, Idempotency: ad.idempotency, Readiness: readiness,
		}, logger, tel.serviceName),
		ReadHeaderTimeout: 5 * time.Second,
	}

	background, stopBackground := startBackground(ad, rt, svc, logger)
	relay, closeRelay, err := startOutboxRelay(ad.outboxStore, rt.brokers, logger)
	if err != nil {
		stopBackground()
		return err
	}
	return serveUntilSignal(ctx, logger, httpServer, readiness, background, stopBackground, relay, closeRelay)
}

// serveUntilSignal runs httpServer until SIGINT/SIGTERM (or a listen error),
// then shuts down in the fleet order: (1) /readyz flips to 503, (2) wait the
// drain delay, (3) drain HTTP, (4) stop and await the consumers and loops,
// (5) stop and await the relay LAST so nothing committed above is stranded; the
// caller's deferred pool.Close runs after all of it.
func serveUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, readiness *inboundhttp.Readiness,
	background []*worker, stopBackground func(), relay *worker, closeRelay func(),
) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	died := watchWorkers(append([]*worker{relay}, background...)...)
	select {
	case err := <-errCh:
		stopBackground()
		closeRelay()
		return err
	case name := <-died:
		// A consumer, a loop or the relay returned on its own: the service
		// cannot run without it and must not sit "alive" with a dead worker,
		// so exit non-zero and let the orchestrator restart it.
		stopBackground()
		closeRelay()
		return fmt.Errorf("%s stopped unexpectedly: exiting so the orchestrator restarts the service", name)
	case <-sigCtx.Done():
	}

	readiness.SetNotReady()
	if delay := shutdownDrainDelay(logger); delay > 0 {
		logger.Info("shutdown: readiness flipped to not-ready; waiting for traffic to drain", "drain_delay", delay.String())
		time.Sleep(delay)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err := httpServer.Shutdown(shutdownCtx)

	stopBackground()
	for _, w := range background {
		await(logger, w)
	}
	closeRelay()
	await(logger, relay)
	return err
}

// watchWorkers returns a channel that receives the name of every worker that
// returns. Workers only return when stopped or on an unrecoverable failure, so
// a value read while the service is serving means a worker died.
func watchWorkers(workers ...*worker) <-chan string {
	died := make(chan string, len(workers))
	for _, w := range workers {
		if w == nil {
			continue
		}
		go func() {
			<-w.done
			died <- w.name
		}()
	}
	return died
}

// await waits (bounded) for w to return; nil w is a no-op.
func await(logger *slog.Logger, w *worker) {
	if w == nil {
		return
	}
	select {
	case <-w.done:
	case <-time.After(workerDrainTimeout):
		logger.Warn("worker did not stop before the shutdown drain deadline", "worker", w.name)
	}
}

// newLogger builds the JSON logger; LOG_LEVEL debug|info|warn|warning|error
// (case-insensitive; "warning" is accepted like "warn" fleet-wide).
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
}
