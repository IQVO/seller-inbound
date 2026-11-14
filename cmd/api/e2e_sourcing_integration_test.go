//go:build integration

package main

import (
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

const (
	topicSourcing = "retailnetwork.order-sourcing.events"

	typeReleased  = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode"
	typeCancelled = "com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelled"
)

func sourcingEvent(t *testing.T, id, typ, assignment, data string) kafkago.Message {
	t.Helper()
	return kafkago.Message{Key: []byte(assignment), Value: inputEvent(t, id, "/retailnetwork/order-sourcing", typ, assignment, data)}
}

// mergedRelease is order-sourcing's merged-release golden (its ADR 0015): the
// extra `members`, per-line `assignmentId` and unknown fields must be ignored,
// and its two lines of one item land as two rows.
const mergedRelease = `{"assignmentId":"asg-7f3c2d10","releaseNo":1,"orderId":"ord-1","groupNo":1,"nodeId":"node-1",` +
	`"pickupDepartureId":"dep-1","requiredShipBy":"2026-10-22T14:10:00Z","promisedDeliverByLatest":"2026-10-22T14:10:00Z","tier":"STANDARD",` +
	`"lines":[{"catalogItemId":"item-1","listingId":"lst-1","quantity":2,"assignmentId":"asg-7f3c2d10"},` +
	`{"catalogItemId":"item-1","listingId":"lst-1","quantity":3,"assignmentId":"asg-2c9d41aa"}],"estimatedCostCents":1065,` +
	`"members":[{"assignmentId":"asg-7f3c2d10","orderId":"ord-1","groupNo":1,"promiseId":"prm-1"},` +
	`{"assignmentId":"asg-2c9d41aa","orderId":"ord-2","groupNo":1,"promiseId":"prm-2"}]}`

// plainRelease is the golden non-merged release of a second assignment.
const plainRelease = `{"assignmentId":"asg-plain","releaseNo":1,"orderId":"ord-3","groupNo":1,"nodeId":"node-1","tier":"TWO_DAY",` +
	`"lines":[{"catalogItemId":"item-1","listingId":"lst-1","quantity":4}],"estimatedCostCents":900,"policyVersion":2,"effectiveCostCents":1000}`

func demandRows(w *world, assignment string) int {
	return w.db.count(`SELECT count(*) FROM demand_heat_lines WHERE assignment_id=$1 AND group_no=1`, assignment)
}

func demandUnits(w *world, assignment string) int {
	return w.db.count(`SELECT coalesce(sum(quantity),0)::int FROM demand_heat_lines WHERE assignment_id=$1 AND group_no=1`, assignment)
}

func processed(w *world, id string) int {
	return w.db.count(`SELECT count(*) FROM processed_events WHERE event_id=$1`, id)
}

// Phase 5c: order-sourcing's release and cancel events, read from its topic by
// the fourth consumer, feed and empty demand_heat_lines (ADR 0012, ADR 0015).
// Kafka orders one partition, so an event is known to have been handled once a
// LATER event of the same topic has been.
func phaseSourcing(t *testing.T, w *world) {
	t.Helper()
	merged := sourcingEvent(t, "evt-s1", typeReleased, "asg-7f3c2d10", mergedRelease)
	produce(t, w.brokers, topicSourcing, merged)
	waitFor(t, 60*time.Second, "the merged release to land in demand_heat_lines", func() bool { return demandRows(w, "asg-7f3c2d10") == 2 })
	if got := demandUnits(w, "asg-7f3c2d10"); got != 5 {
		t.Fatalf("merged release holds %d units, want 5 (2+3)", got)
	}
	if n := w.db.count(`SELECT count(*) FROM demand_heat_lines WHERE assignment_id='asg-7f3c2d10' AND node_id='node-1' AND catalog_item_id='item-1'`); n != 2 {
		t.Fatalf("%d rows carry the release's node and item, want 2", n)
	}
	if n := processed(w, "evt-s1"); n != 1 {
		t.Fatalf("processed_events holds %d rows for the release, want 1", n)
	}

	// A replay of the same event id is a no-op; the plain release behind it proves the replay was handled.
	produce(t, w.brokers, topicSourcing, merged, sourcingEvent(t, "evt-s2", typeReleased, "asg-plain", plainRelease))
	waitFor(t, 60*time.Second, "the plain release behind the replay", func() bool { return demandRows(w, "asg-plain") == 1 })
	if demandRows(w, "asg-7f3c2d10") != 2 || demandUnits(w, "asg-7f3c2d10") != 5 || processed(w, "evt-s1") != 1 {
		t.Fatal("a replayed release changed demand_heat_lines or processed_events")
	}

	// The cancel removes that group's rows only.
	cancel := sourcingEvent(t, "evt-s3", typeCancelled, "asg-7f3c2d10", `{"assignmentId":"asg-7f3c2d10","orderId":"ord-1","groupNo":1,"reason":"CUSTOMER_REQUEST"}`)
	produce(t, w.brokers, topicSourcing, cancel)
	waitFor(t, 60*time.Second, "the cancel to remove the merged release's rows", func() bool { return demandRows(w, "asg-7f3c2d10") == 0 })
	if demandRows(w, "asg-plain") != 1 {
		t.Fatal("the cancel removed another assignment's demand")
	}

	// Replaying the release after its cancel must not bring the demand back; the second cancel behind it marks the end.
	produce(t, w.brokers, topicSourcing, merged, cancel,
		sourcingEvent(t, "evt-s4", typeCancelled, "asg-plain", `{"assignmentId":"asg-plain","orderId":"ord-3","groupNo":1}`))
	waitFor(t, 60*time.Second, "the closing cancel", func() bool { return demandRows(w, "asg-plain") == 0 })
	if demandRows(w, "asg-7f3c2d10") != 0 {
		t.Fatal("a replayed release resurrected cancelled demand")
	}
	for _, id := range []string{"evt-s1", "evt-s2", "evt-s3", "evt-s4"} {
		if n := processed(w, id); n != 1 {
			t.Fatalf("processed_events holds %d rows for %s, want 1", n, id)
		}
	}
	if n := w.db.count(`SELECT count(*) FROM processed_events WHERE event_id LIKE 'evt-s%'`); n != 4 {
		t.Fatalf("%d sourcing event ids were claimed, want 4", n)
	}
}
