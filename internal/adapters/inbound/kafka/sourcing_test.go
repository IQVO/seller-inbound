package kafka_test

import (
	"context"
	"testing"

	kafkaconsumer "github.com/claudioed/seller-inbound/internal/adapters/inbound/kafka"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/memory"
	"github.com/claudioed/seller-inbound/internal/application/usecases"
	"github.com/claudioed/seller-inbound/internal/domain/network"
)

// sourcingFixture is a Service with DemandHeatView wired, the sourcing
// handlers over it, and a node whose region the heat is summed under.
func sourcingFixture(t *testing.T) (*memory.Store, *kafkaconsumer.Consumer) {
	t.Helper()
	s := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: s.UnitOfWork(), Sellers: s.Sellers(), Plans: s.Plans(), Shipments: s.Shipments(), Nodes: s.Nodes(), Scorecard: s.Scorecard(), Packaging: s.Packaging(),
		Outbox: s.Outbox(), Inbox: s.Inbox(), Clock: memory.NewClock(t0), IDs: &memory.SequentialIDs{}, SSCC: &memory.SsccIssuer{},
		DemandHeat: s.DemandHeat(), Listings: s.Listings(),
	}, usecases.Config{Logger: testLogger()})
	f := &fixture{store: s, svc: svc, dlq: &fakeDLQWriter{}}
	if err := f.nodes().HandleMessage(context.Background(),
		event("n1", kafkaconsumer.TypeNodeRegistered, "n1", m{"nodeId": "n1", "nodeType": "FULFILLMENT_CENTER", "operator": "VENDOR", "regionId": "r-south"})); err != nil {
		t.Fatal(err)
	}
	return s, f.consumer(kafkaconsumer.SourcingHandlers(svc))
}

func regionUnits(t *testing.T, s *memory.Store, item string) int {
	t.Helper()
	heat, err := s.DemandHeat().RegionHeat(context.Background(), item, t0.Add(-network.ObservationWindow))
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, h := range heat {
		total += h.DemandUnits
	}
	return total
}

// releasedPlain is order-sourcing's golden FulfillmentReleasedToNode shape.
func releasedPlain() m {
	return m{"assignmentId": "asg-7f3c2d10", "releaseNo": 1, "orderId": "ord-1", "groupNo": 1, "nodeId": "n1",
		"pickupDepartureId": "dep-1", "requiredShipBy": "2026-10-22T14:10:00Z", "promisedDeliverByLatest": "2026-10-22T14:10:00Z",
		"tier": "TWO_DAY", "estimatedCostCents": 1065, "policyVersion": 2, "effectiveCostCents": 1315,
		"lines": []m{{"catalogItemId": "cat-1", "listingId": "lst-1", "quantity": 2}}}
}

// releasedMerged is the merged-release golden of order-sourcing (ADR 0015):
// extra optional `members`, a per-line `assignmentId` and two lines of one item.
func releasedMerged() m {
	d := releasedPlain()
	d["tier"] = "STANDARD"
	d["lines"] = []m{
		{"catalogItemId": "cat-1", "listingId": "lst-1", "quantity": 2, "assignmentId": "asg-7f3c2d10"},
		{"catalogItemId": "cat-1", "listingId": "lst-1", "quantity": 3, "assignmentId": "asg-2c9d41aa"},
	}
	d["members"] = []m{
		{"assignmentId": "asg-7f3c2d10", "orderId": "ord-1", "groupNo": 1, "promiseId": "prm-1"},
		{"assignmentId": "asg-2c9d41aa", "orderId": "ord-2", "groupNo": 1, "promiseId": "prm-2"},
	}
	d["ownerRef"] = m{"kind": "SELLER", "id": "seller-77"}
	return d
}

func TestSourcingHandlersTolerateTheMergedReleaseShape(t *testing.T) {
	s, c := sourcingFixture(t)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		data m
		want int
	}{"plain release": {releasedPlain(), 2}, "merged release with members, per-line assignmentId and ownerRef": {releasedMerged(), 5}} {
		if err := c.HandleMessage(ctx, event("rel-"+name, kafkaconsumer.TypeReleasedToNode, "asg-7f3c2d10", tc.data)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := regionUnits(t, s, "cat-1"); got != tc.want {
			t.Fatalf("%s: region demand = %d, want %d (a re-release of the same group replaces its lines)", name, got, tc.want)
		}
	}
}

func TestSourcingCancelRemovesTheDemandAndAReplayIsANoOp(t *testing.T) {
	s, c := sourcingFixture(t)
	ctx := context.Background()
	release := event("rel-1", kafkaconsumer.TypeReleasedToNode, "asg-7f3c2d10", releasedMerged())
	for i := 0; i < 2; i++ { // the second delivery is a replay of the same event id
		if err := c.HandleMessage(ctx, release); err != nil {
			t.Fatal(err)
		}
	}
	if got := regionUnits(t, s, "cat-1"); got != 5 {
		t.Fatalf("demand = %d, want 5", got)
	}
	cancel := event("can-1", kafkaconsumer.TypeFulfilCancelled, "asg-7f3c2d10",
		m{"assignmentId": "asg-7f3c2d10", "orderId": "ord-1", "groupNo": 1, "reason": "CUSTOMER_REQUEST"})
	if err := c.HandleMessage(ctx, cancel); err != nil {
		t.Fatal(err)
	}
	if got := regionUnits(t, s, "cat-1"); got != 0 {
		t.Fatalf("demand after cancel = %d, want 0", got)
	}
	// Replaying the release (same id) must not resurrect the demand.
	if err := c.HandleMessage(ctx, release); err != nil {
		t.Fatal(err)
	}
	if got := regionUnits(t, s, "cat-1"); got != 0 {
		t.Fatalf("a replayed release brought the demand back: %d", got)
	}
}
