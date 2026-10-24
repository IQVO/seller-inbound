// Package seller is the SellerAccount aggregate: a seller organisation, the
// owner of record of the stock it sends, and whether it may send more.
//
// States: ACTIVE (initial) and SUSPENDED. SuspendSeller moves ACTIVE to
// SUSPENDED, ReinstateSeller moves SUSPENDED to ACTIVE. Repeating a command
// that is already in effect is a no-op without an event (ADR 0002, A3).
package seller

import (
	"errors"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

var (
	// ErrSellerSuspended is raised when a suspended seller creates a plan,
	// a shipment or a carton, or ships (problem type seller-suspended, 409).
	ErrSellerSuspended = errors.New("seller suspended")
	// ErrSellerExists is raised when the sellerId is already onboarded
	// (problem type seller-exists, 409).
	ErrSellerExists = errors.New("seller already exists")
)

const (
	// MaxName is the longest seller name, in characters.
	MaxName = 120
	// MaxReason is the longest suspension reason, in characters.
	MaxReason = 200
)

// ID identifies a seller (sellerId). On the network it is also the ownerRef of
// the stock the seller sends.
type ID string

// State is the lifecycle state of a seller account.
type State string

// The two states of a seller account.
const (
	Active    State = "ACTIVE"
	Suspended State = "SUSPENDED"
)

// Account is the aggregate root.
type Account struct {
	id          ID
	name        string
	state       State
	onboardedAt time.Time
	suspendedAt time.Time
	reason      string
	version     int64
}

// Snapshot is the persisted form of an account. Restore validates it.
type Snapshot struct {
	ID            ID
	Name          string
	State         State
	OnboardedAt   time.Time
	SuspendedAt   time.Time
	SuspendReason string
	Version       int64
}

// NewAccount is the input of Onboard.
type NewAccount struct {
	ID   ID
	Name string
}

// Onboard creates an ACTIVE account at version 1 and raises SellerOnboarded.
func Onboard(in NewAccount, now time.Time) (*Account, []shared.Event, error) {
	a, err := build(Snapshot{ID: in.ID, Name: in.Name, State: Active, OnboardedAt: now, Version: 1})
	if err != nil {
		return nil, nil, shared.Invalid("%v", err)
	}
	ev := SellerOnboarded{Header: shared.Header{Version: a.version, At: a.onboardedAt}, SellerID: a.id, Name: a.name}
	return a, []shared.Event{ev}, nil
}

// Restore rebuilds a persisted account, validating every invariant.
func Restore(s Snapshot) (*Account, error) {
	a, err := build(s)
	if err != nil {
		return nil, shared.Corrupt("seller %q: %v", s.ID, err)
	}
	return a, nil
}

func build(s Snapshot) (*Account, error) {
	if err := checkValues(s); err != nil {
		return nil, err
	}
	return &Account{
		id: s.ID, name: s.Name, state: s.State, onboardedAt: s.OnboardedAt.UTC(),
		suspendedAt: shared.UTC(s.SuspendedAt), reason: s.SuspendReason, version: s.Version,
	}, nil
}

func checkValues(s Snapshot) error {
	if !shared.ValidOwnerRef(string(s.ID)) {
		return errors.New("sellerId must be 1 to 64 characters of A-Z a-z 0-9 . _ -")
	}
	if !shared.ValidTextN(s.Name, MaxName) {
		return errors.New("name must be non-blank, trimmed and at most 120 characters")
	}
	if s.OnboardedAt.IsZero() {
		return errors.New("onboardedAt is required")
	}
	if s.Version < 1 {
		return errors.New("version must be at least 1")
	}
	return checkState(s)
}

func checkState(s Snapshot) error {
	switch s.State {
	case Active:
		if !s.SuspendedAt.IsZero() || s.SuspendReason != "" {
			return errors.New("an active seller has no suspension")
		}
		return nil
	case Suspended:
		if s.SuspendedAt.IsZero() {
			return errors.New("a suspended seller needs suspendedAt")
		}
		if s.SuspendReason != "" && !shared.ValidTextN(s.SuspendReason, MaxReason) {
			return errors.New("suspend reason must be trimmed and at most 200 characters")
		}
		return nil
	default:
		return errors.New("state must be ACTIVE or SUSPENDED")
	}
}

// Suspend moves ACTIVE to SUSPENDED and raises SellerSuspended. reason is
// optional. Suspending a suspended seller is a no-op.
func (a *Account) Suspend(reason string, now time.Time) ([]shared.Event, error) {
	if a.state == Suspended {
		return nil, nil
	}
	if reason != "" && !shared.ValidTextN(reason, MaxReason) {
		return nil, shared.Invalid("reason must be trimmed and at most %d characters", MaxReason)
	}
	a.state, a.suspendedAt, a.reason = Suspended, now.UTC(), reason
	a.version++
	return []shared.Event{SellerSuspended{Header: shared.Header{Version: a.version, At: a.suspendedAt}, SellerID: a.id, Reason: reason}}, nil
}

// Reinstate moves SUSPENDED to ACTIVE and raises SellerReinstated.
// Reinstating an active seller is a no-op.
func (a *Account) Reinstate(now time.Time) ([]shared.Event, error) {
	if a.state == Active {
		return nil, nil
	}
	a.state, a.suspendedAt, a.reason = Active, time.Time{}, ""
	a.version++
	return []shared.Event{SellerReinstated{Header: shared.Header{Version: a.version, At: now.UTC()}, SellerID: a.id}}, nil
}

// EnsureActive returns ErrSellerSuspended when the seller may not send stock.
func (a *Account) EnsureActive() error {
	if a.state == Suspended {
		return ErrSellerSuspended
	}
	return nil
}

// Snapshot returns the persisted form.
func (a *Account) Snapshot() Snapshot {
	return Snapshot{ID: a.id, Name: a.name, State: a.state, OnboardedAt: a.onboardedAt,
		SuspendedAt: a.suspendedAt, SuspendReason: a.reason, Version: a.version}
}

// ID returns the sellerId.
func (a *Account) ID() ID { return a.id }

// OwnerRef returns the owner of the stock the seller sends: the sellerId.
func (a *Account) OwnerRef() string { return string(a.id) }

// Name returns the seller's name.
func (a *Account) Name() string { return a.name }

// State returns the lifecycle state.
func (a *Account) State() State { return a.state }

// Version returns the optimistic-concurrency version.
func (a *Account) Version() int64 { return a.version }
