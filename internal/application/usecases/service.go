// Package usecases holds the application services: one method per command and
// query of the model and per REST operation, plus the consumers' policies.
// They orchestrate domain aggregates through ports; they perform no I/O of
// their own. Every command runs in ONE unit of work that saves the aggregates
// and appends their events to the outbox (and, for a consumer, claims the
// CloudEvents id in the inbox).
package usecases

import (
	"context"
	"errors"
	"log/slog"

	"github.com/claudioed/seller-inbound/internal/application/ports"
)

// Config tunes the service.
type Config struct {
	Logger *slog.Logger
	// SsccIssueAttempts is how many times an issued SSCC that collides with one a
	// seller supplied is re-issued before giving up. Default 5.
	SsccIssueAttempts int
}

// DefaultSsccIssueAttempts is the default of Config.SsccIssueAttempts.
const DefaultSsccIssueAttempts = 5

// Deps are the out ports the service needs.
type Deps struct {
	UoW       ports.UnitOfWork
	Sellers   ports.SellerRepository
	Plans     ports.PlanRepository
	Shipments ports.ShipmentRepository
	Nodes     ports.NodeViewRepository
	Scorecard ports.ScorecardFacts
	Packaging ports.PackagingViewRepository
	Outbox    ports.EventOutbox
	Inbox     ports.ProcessedEvents
	Clock     ports.Clock
	IDs       ports.IDGenerator
	SSCC      ports.SsccIssuer
}

// Service is the application service of seller-inbound.
type Service struct {
	d   Deps
	cfg Config
}

// New builds the service, filling the Config defaults.
func New(d Deps, cfg Config) *Service {
	if cfg.SsccIssueAttempts <= 0 {
		cfg.SsccIssueAttempts = DefaultSsccIssueAttempts
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{d: d, cfg: cfg}
}

// atomically runs fn in one unit of work and re-runs the whole unit once when
// a save lost an optimistic-concurrency race (the caller re-reads and retries).
func (s *Service) atomically(ctx context.Context, fn func(ctx context.Context) error) error {
	err := s.d.UoW.Do(ctx, fn)
	if errors.Is(err, ports.ErrConcurrentUpdate) {
		err = s.d.UoW.Do(ctx, fn)
	}
	return err
}

// consume is the inbox wrapper of every consumer policy: claim the event id,
// run fn, commit both together. A duplicate id is skipped.
func (s *Service) consume(ctx context.Context, eventID, eventType string, fn func(ctx context.Context) error) error {
	return s.atomically(ctx, func(ctx context.Context) error {
		first, err := s.d.Inbox.Claim(ctx, eventID, eventType, s.d.Clock.Now())
		if err != nil {
			return err
		}
		if !first {
			return nil
		}
		return fn(ctx)
	})
}
