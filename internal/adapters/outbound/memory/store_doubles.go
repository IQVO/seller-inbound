package memory

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

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
