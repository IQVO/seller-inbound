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
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

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

// SellerRepository is the in-memory ports.SellerRepository.
type SellerRepository struct{ s *Store }

// Get returns the account or ports.ErrNotFound.
func (r *SellerRepository) Get(ctx context.Context, id seller.ID) (*seller.Account, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.sellers[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return seller.Restore(snap)
}

// Save stores the account under the concurrency rule.
func (r *SellerRepository) Save(ctx context.Context, a *seller.Account) error {
	defer r.s.enter(ctx)()
	old, ok := r.s.sellers[a.ID()]
	if !versionOK(old.Version, ok, a.Version()) {
		return ports.ErrConcurrentUpdate
	}
	r.s.sellers[a.ID()] = a.Snapshot()
	return nil
}

// PlanRepository is the in-memory ports.PlanRepository.
type PlanRepository struct{ s *Store }

// Get returns the plan or ports.ErrNotFound.
func (r *PlanRepository) Get(ctx context.Context, id inboundplan.ID) (*inboundplan.Plan, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.plans[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return inboundplan.Restore(snap)
}

// Save stores the plan under the concurrency rule.
func (r *PlanRepository) Save(ctx context.Context, p *inboundplan.Plan) error {
	defer r.s.enter(ctx)()
	old, ok := r.s.plans[p.ID()]
	if !versionOK(old.Version, ok, p.Version()) {
		return ports.ErrConcurrentUpdate
	}
	r.s.plans[p.ID()] = p.Snapshot()
	return nil
}

// ShipmentRepository is the in-memory ports.ShipmentRepository.
type ShipmentRepository struct{ s *Store }

// Get returns the shipment or ports.ErrNotFound.
func (r *ShipmentRepository) Get(ctx context.Context, id inboundshipment.ID) (*inboundshipment.Shipment, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.shipments[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return inboundshipment.Restore(snap)
}

// ListByPlan returns the plan's shipments ordered by (createdAt, id).
func (r *ShipmentRepository) ListByPlan(ctx context.Context, planID inboundplan.ID) ([]*inboundshipment.Shipment, error) {
	defer r.s.enter(ctx)()
	var snaps []inboundshipment.Snapshot
	for _, snap := range r.s.shipments {
		if snap.PlanID == string(planID) {
			snaps = append(snaps, snap)
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].CreatedAt.Equal(snaps[j].CreatedAt) {
			return snaps[i].CreatedAt.Before(snaps[j].CreatedAt)
		}
		return snaps[i].ID < snaps[j].ID
	})
	out := make([]*inboundshipment.Shipment, 0, len(snaps))
	for _, snap := range snaps {
		sh, err := inboundshipment.Restore(snap)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, nil
}

// Save registers the SSCCs and stores the shipment under the concurrency rule.
func (r *ShipmentRepository) Save(ctx context.Context, sh *inboundshipment.Shipment) error {
	defer r.s.enter(ctx)()
	snap := sh.Snapshot()
	for _, c := range snap.Cartons {
		if owner, held := r.s.sscc[c.SSCC]; held && owner != snap.ID {
			return ports.ErrSsccTaken
		}
	}
	old, ok := r.s.shipments[snap.ID]
	if !versionOK(old.Version, ok, snap.Version) {
		return ports.ErrConcurrentUpdate
	}
	for _, c := range snap.Cartons {
		r.s.sscc[c.SSCC] = snap.ID
	}
	r.s.shipments[snap.ID] = snap
	return nil
}

// NodeViewRepository is the in-memory ports.NodeViewRepository.
type NodeViewRepository struct{ s *Store }

// Get returns the node or ports.ErrNotFound.
func (r *NodeViewRepository) Get(ctx context.Context, nodeID string) (network.Node, error) {
	defer r.s.enter(ctx)()
	n, ok := r.s.nodes[nodeID]
	if !ok {
		return network.Node{}, ports.ErrNotFound
	}
	return n, nil
}

// Save inserts or replaces the node row.
func (r *NodeViewRepository) Save(ctx context.Context, n network.Node) error {
	defer r.s.enter(ctx)()
	r.s.nodes[n.ID] = n
	return nil
}

// PackagingViewRepository is the in-memory ports.PackagingViewRepository.
type PackagingViewRepository struct{ s *Store }

// Get returns the row or ports.ErrNotFound.
func (r *PackagingViewRepository) Get(ctx context.Context, item string, level network.Level, gtin string) (network.PackLevel, error) {
	defer r.s.enter(ctx)()
	p, ok := r.s.packaging[packKey{item, level, gtin}]
	if !ok {
		return network.PackLevel{}, ports.ErrNotFound
	}
	return p, nil
}

// Save inserts or replaces the row.
func (r *PackagingViewRepository) Save(ctx context.Context, p network.PackLevel) error {
	defer r.s.enter(ctx)()
	r.s.packaging[packKey{p.CatalogItemID, p.Level, p.GTIN}] = p
	return nil
}

// ListByItem returns the rows of the item ordered by (level, gtin).
func (r *PackagingViewRepository) ListByItem(ctx context.Context, item string) ([]network.PackLevel, error) {
	defer r.s.enter(ctx)()
	var out []network.PackLevel
	for k, p := range r.s.packaging {
		if k.item == item {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level < out[j].Level
		}
		return out[i].GTIN < out[j].GTIN
	})
	return out, nil
}

// Inbox is the in-memory ports.ProcessedEvents.
type Inbox struct{ s *Store }

// Claim records the id and reports whether it was new.
func (i *Inbox) Claim(ctx context.Context, eventID, eventType string, _ time.Time) (bool, error) {
	defer i.s.enter(ctx)()
	if _, seen := i.s.inbox[eventID]; seen {
		return false, nil
	}
	i.s.inbox[eventID] = eventType
	return true, nil
}

// Outbox is the in-memory ports.EventOutbox.
type Outbox struct{ s *Store }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.outbox = append(o.s.outbox, events...)
	return nil
}

// Events returns every appended event in order.
func (o *Outbox) Events() []shared.Event {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return slices.Clone(o.s.outbox)
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

// Clock is a settable ports.Clock.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock reading t.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now returns the current reading.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set moves the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// SequentialIDs is a ports.IDGenerator minting pln-1, shp-2, ...
type SequentialIDs struct {
	mu sync.Mutex
	n  int
}

// NewID returns the next id.
func (g *SequentialIDs) NewID(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return fmt.Sprintf("%s-%d", prefix, g.n)
}

// SsccIssuer is a ports.SsccIssuer minting valid SSCCs 000000000000000011 ... by serial.
type SsccIssuer struct {
	mu     sync.Mutex
	n      int64
	Fail   error
	Prefix string
}

// Next returns the next SSCC under the configured prefix (default "0614141").
func (i *SsccIssuer) Next(context.Context) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.Fail != nil {
		return "", i.Fail
	}
	i.n++
	prefix := i.Prefix
	if prefix == "" {
		prefix = "0614141"
	}
	serial := fmt.Sprintf("%0*d", 16-len(prefix), i.n)
	body := "0" + prefix + serial
	cd := checkDigit(body)
	return body + fmt.Sprint(cd), nil
}

// checkDigit is the GS1 mod-10 check digit of a 17-digit payload.
func checkDigit(payload string) int {
	sum := 0
	for i := len(payload) - 1; i >= 0; i-- {
		d := int(payload[i] - '0')
		if (len(payload)-1-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return (10 - sum%10) % 10
}

// Scorecard returns the ScorecardFacts double.
func (s *Store) Scorecard() *ScorecardFacts { return &ScorecardFacts{s} }

// ScorecardFacts is the in-memory ports.ScorecardFacts. It follows the SQL
// contract of the Postgres adapter: a closed shipment is kept once, a window
// is inclusive on both ends.
type ScorecardFacts struct{ s *Store }

// Record appends the facts.
func (f *ScorecardFacts) Record(ctx context.Context, facts scorecard.Facts) error {
	defer f.s.enter(ctx)()
	for _, r := range facts.Receipts {
		if _, kept := f.s.receipts[r.ShipmentID]; !kept {
			f.s.receipts[r.ShipmentID] = r
		}
	}
	f.s.occurs = append(f.s.occurs, facts.Occurrences...)
	return nil
}

// Counts sums the seller's facts recorded in [since, until].
func (f *ScorecardFacts) Counts(ctx context.Context, sellerID string, since, until time.Time) (scorecard.Counts, error) {
	defer f.s.enter(ctx)()
	in := func(at time.Time) bool { return !at.Before(since) && !at.After(until) }
	out := scorecard.Counts{Discrepancies: map[string]int{}}
	for _, r := range f.s.receipts {
		if r.SellerID == sellerID && in(r.At) {
			out.ShipmentsReceived++
			out.CartonsReported += r.CartonsReported
			out.CartonsReceived += r.CartonsReceived
		}
	}
	for _, o := range f.s.occurs {
		if o.SellerID != sellerID || !in(o.At) {
			continue
		}
		if o.Type == scorecard.Discrepancy {
			out.Discrepancies[o.Code]++
		} else {
			out.Violations++
		}
	}
	return out, nil
}
