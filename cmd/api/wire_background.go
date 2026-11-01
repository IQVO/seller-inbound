package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	inboundhttp "github.com/claudioed/seller-inbound/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
	outboundkafka "github.com/claudioed/seller-inbound/internal/adapters/outbound/kafka"
	outboxrelay "github.com/claudioed/seller-inbound/internal/adapters/outbound/outbox"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/postgres"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
)

// worker is a started background loop and the channel closed once it returned.
type worker struct {
	name string
	done chan struct{}
}

// startBackground starts the three consumers and the retention loop. They are
// always on; the readers dial lazily inside Run, so a broker outage never
// blocks boot.
func startBackground(ad adapters, rt runtimeEnv, svc *usecases.Service, logger *slog.Logger) ([]*worker, func()) {
	ctx, stop := context.WithCancel(context.Background())
	var (
		workers []*worker
		closers []func() error
	)
	consumers := []*inboundkafka.Consumer{
		inboundkafka.NewNodeConsumer(rt.brokers, rt.groups["NODE_CONSUMER_GROUP"], svc, logger),
		inboundkafka.NewCatalogConsumer(rt.brokers, rt.groups["CATALOG_CONSUMER_GROUP"], svc, logger),
		inboundkafka.NewReceiptConsumer(rt.brokers, rt.groups["RECEIPT_CONSUMER_GROUP"], svc, logger),
	}
	for _, c := range consumers {
		w := &worker{name: c.Name, done: make(chan struct{})}
		workers = append(workers, w)
		closers = append(closers, c.Close)
		go func() {
			defer close(w.done)
			logger.Info("consumer running", "consumer", c.Name, "topic", c.Topic, "brokers", rt.brokers)
			if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
				logger.Error("consumer stopped", "consumer", c.Name, "error", err)
			}
		}()
	}
	retentionLoop := &worker{name: "retention sweeper", done: make(chan struct{})}
	workers = append(workers, retentionLoop)
	go func() {
		defer close(retentionLoop.done)
		runLoop(ctx, logger, retentionLoop.name, retentionSweep, func(ctx context.Context) (int, error) {
			return postgres.PurgeIdempotencyKeys(ctx, ad.pool, time.Now().Add(-inboundhttp.IdempotencyTTL))
		})
	}()
	return workers, func() {
		stop()
		for _, closeFn := range closers {
			_ = closeFn()
		}
	}
}

// runLoop calls fn at once and then every interval until ctx ends. A failed
// pass is logged and retried at the next tick: the loops are idempotent.
func runLoop(ctx context.Context, logger *slog.Logger, name string, interval time.Duration, fn func(context.Context) (int, error)) {
	logger.Info("loop running", "loop", name, "interval", interval.String())
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		switch n, err := fn(ctx); {
		case err != nil && ctx.Err() == nil:
			logger.Error("loop pass failed", "loop", name, "error", err)
		case n > 0:
			logger.Info("loop pass done", "loop", name, "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// startOutboxRelay starts the always-on relay goroutine over the Kafka sink.
// Kafka is dialled lazily on the first send, never here.
func startOutboxRelay(store outboxrelay.Store, brokers []string, logger *slog.Logger) (*worker, func(), error) {
	if len(brokers) == 0 {
		return nil, nil, errMissingBrokers
	}
	kafkaSink := outboundkafka.NewRelaySink(brokers)
	closeSink := func() {
		if err := kafkaSink.Close(); err != nil {
			logger.Error("kafka relay sink close failed", "error", err)
		}
	}
	interval := parseRelayInterval(os.Getenv("OUTBOX_RELAY_INTERVAL"), logger)
	maxAttempts := outboxMaxAttempts(os.Getenv("OUTBOX_MAX_ATTEMPTS"), logger)
	relay := outboxrelay.NewRelay(store, kafkaSink, logger, outboxrelay.WithInterval(interval), outboxrelay.WithMaxAttempts(maxAttempts))

	ctx, stop := context.WithCancel(context.Background())
	w := &worker{name: "outbox-relay", done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer closeSink()
		logger.Info("outbox relay running", "publisher", "kafka", "interval", interval, "max_attempts", maxAttempts, "topic", outboundkafka.TopicEvents)
		if err := relay.Run(ctx); !errors.Is(err, context.Canceled) {
			logger.Error("outbox relay stopped", "error", err)
		}
	}()
	return w, stop, nil
}
