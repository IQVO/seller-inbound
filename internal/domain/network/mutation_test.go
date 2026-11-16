package network

import (
	"strings"
	"testing"
	"time"
)

// TestDemandReleaseAcceptsAQuantityOfOne: the smallest legal line is one unit.
func TestDemandReleaseAcceptsAQuantityOfOne(t *testing.T) {
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	r := DemandRelease{AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-a", ReleasedAt: at,
		Lines: []ReleaseLine{{LineNo: 1, CatalogItemID: "itm-1", Quantity: 1}}}
	if err := r.Validate(); err != nil {
		t.Fatalf("a quantity of 1 is valid: %v", err)
	}
	r.Lines[0].Quantity = 0
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "quantity must be at least 1") {
		t.Fatalf("a quantity of 0 err = %v, want the quantity message", err)
	}
}

// TestNodeAvailabilityAcceptsZeroUnits: a listing declaring zero units is a
// legal (sold out) availability, only a negative quantity is refused.
func TestNodeAvailabilityAcceptsZeroUnits(t *testing.T) {
	n := NodeAvailability{ListingID: "lst-1", CatalogItemID: "itm-1", NodeID: "fc-a", AvailableQty: 0,
		AsOf: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}
	if err := n.Validate(); err != nil {
		t.Fatalf("zero available units is valid: %v", err)
	}
	n.AvailableQty = -1
	if err := n.Validate(); err == nil || !strings.Contains(err.Error(), "availableQty must not be negative") {
		t.Fatalf("negative units err = %v, want the availableQty message", err)
	}
}
