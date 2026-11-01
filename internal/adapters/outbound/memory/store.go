// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit and BDD tests only: cmd/** never imports this package
// (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so a
// loaded aggregate never aliases stored state and the doubles keep the
// persistence contract of the real adapters: a miss is ports.ErrNotFound, a
// stale or duplicate save is ports.ErrConcurrentUpdate, an SSCC held by another
// shipment is ports.ErrSsccTaken.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store for
// the whole unit and restores the state taken at the start when the unit fails
// or panics, outbox and inbox included.
package memory

import (
	"context"
	"slices"
	"sync"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/network"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/seller"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// The doubles implement the out ports.
var (
	_ ports.SellerRepository        = (*SellerRepository)(nil)
	_ ports.PlanRepository          = (*PlanRepository)(nil)
	_ ports.ShipmentRepository      = (*ShipmentRepository)(nil)
	_ ports.NodeViewRepository      = (*NodeViewRepository)(nil)
	_ ports.PackagingViewRepository = (*PackagingViewRepository)(nil)
	_ ports.ProcessedEvents         = (*Inbox)(nil)
	_ ports.ScorecardFacts          = (*ScorecardFacts)(nil)
	_ ports.EventOutbox             = (*Outbox)(nil)
	_ ports.UnitOfWork              = (*UnitOfWork)(nil)
	_ ports.Clock                   = (*Clock)(nil)
	_ ports.IDGenerator             = (*SequentialIDs)(nil)
	_ ports.SsccIssuer              = (*SsccIssuer)(nil)
)

type packKey struct {
	item  string
	level network.Level
	gtin  string
}

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu        sync.Mutex
	sellers   map[seller.ID]seller.Snapshot
	plans     map[inboundplan.ID]inboundplan.Snapshot
	shipments map[inboundshipment.ID]inboundshipment.Snapshot
	sscc      map[string]inboundshipment.ID
	nodes     map[string]network.Node
	packaging map[packKey]network.PackLevel
	inbox     map[string]string
	outbox    []shared.Event
	receipts  map[string]scorecard.Receipt
	occurs    []scorecard.Occurrence
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{
		sellers: map[seller.ID]seller.Snapshot{}, plans: map[inboundplan.ID]inboundplan.Snapshot{},
		shipments: map[inboundshipment.ID]inboundshipment.Snapshot{}, sscc: map[string]inboundshipment.ID{},
		nodes: map[string]network.Node{}, packaging: map[packKey]network.PackLevel{}, inbox: map[string]string{},
		receipts: map[string]scorecard.Receipt{},
	}
}

// state is a copy of everything a unit of work can change. Stored snapshots are
// replaced, never edited in place, so copying the maps is enough.
type state struct {
	sellers   map[seller.ID]seller.Snapshot
	plans     map[inboundplan.ID]inboundplan.Snapshot
	shipments map[inboundshipment.ID]inboundshipment.Snapshot
	sscc      map[string]inboundshipment.ID
	nodes     map[string]network.Node
	packaging map[packKey]network.PackLevel
	inbox     map[string]string
	outbox    []shared.Event
	receipts  map[string]scorecard.Receipt
	occurs    []scorecard.Occurrence
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) capture() state {
	return state{sellers: cloneMap(s.sellers), plans: cloneMap(s.plans), shipments: cloneMap(s.shipments), sscc: cloneMap(s.sscc),
		nodes: cloneMap(s.nodes), packaging: cloneMap(s.packaging), inbox: cloneMap(s.inbox), outbox: slices.Clone(s.outbox),
		receipts: cloneMap(s.receipts), occurs: slices.Clone(s.occurs)}
}

func (s *Store) restore(st state) {
	s.sellers, s.plans, s.shipments, s.sscc = st.sellers, st.plans, st.shipments, st.sscc
	s.nodes, s.packaging, s.inbox, s.outbox = st.nodes, st.packaging, st.inbox, st.outbox
	s.receipts, s.occurs = st.receipts, st.occurs
}

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Counts is how many rows each table of the store holds.
type Counts struct {
	Sellers, Plans, Shipments, Nodes, Packaging, Inbox, OutboxEvents, Ssccs int
	// ScorecardReceipts and ScorecardOccurrences are the scorecard fact rows.
	ScorecardReceipts, ScorecardOccurrences int
	// VersionSum is the sum of every aggregate version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate versions,
// so a test can prove that a failed use case stored and changed nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Counts{Sellers: len(s.sellers), Plans: len(s.plans), Shipments: len(s.shipments), Nodes: len(s.nodes),
		Packaging: len(s.packaging), Inbox: len(s.inbox), OutboxEvents: len(s.outbox), Ssccs: len(s.sscc),
		ScorecardReceipts: len(s.receipts), ScorecardOccurrences: len(s.occurs)}
	for _, v := range s.sellers {
		c.VersionSum += v.Version
	}
	for _, v := range s.plans {
		c.VersionSum += v.Version
	}
	for _, v := range s.shipments {
		c.VersionSum += v.Version
	}
	return c
}

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Sellers returns the SellerRepository double.
func (s *Store) Sellers() *SellerRepository { return &SellerRepository{s} }

// Plans returns the PlanRepository double.
func (s *Store) Plans() *PlanRepository { return &PlanRepository{s} }

// Shipments returns the ShipmentRepository double.
func (s *Store) Shipments() *ShipmentRepository { return &ShipmentRepository{s} }

// Nodes returns the NodeViewRepository double.
func (s *Store) Nodes() *NodeViewRepository { return &NodeViewRepository{s} }

// Packaging returns the PackagingViewRepository double.
func (s *Store) Packaging() *PackagingViewRepository { return &PackagingViewRepository{s} }

// Inbox returns the ProcessedEvents double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// versionOK applies the optimistic-concurrency rule of the ports package:
// stored version == version-1, and a create (version 1) expects no row.
func versionOK(stored int64, exists bool, version int64) bool {
	if version == 1 {
		return !exists
	}
	return exists && stored == version-1
}

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn atomically: the state taken at the start comes back when fn fails or panics.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		return fn(ctx)
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.capture()
	committed := false
	defer func() {
		if !committed {
			u.s.restore(before)
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, struct{}{}))
	committed = err == nil
	return err
}
