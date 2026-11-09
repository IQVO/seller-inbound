package network

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

func TestRegisteredKeepsOperatorAndRegion(t *testing.T) {
	n, err := Node{}.Registered("fc-a", TypeFulfillmentCenter, OperatorVendor, "reg-east")
	if err != nil {
		t.Fatalf("registered: %v", err)
	}
	if n.Operator != OperatorVendor || n.RegionID != "reg-east" {
		t.Fatalf("operator/region = %s/%s", n.Operator, n.RegionID)
	}
	// An out-of-order activation survives; a re-registration refreshes the region.
	activated := n.Activated("fc-a")
	refreshed, err := activated.Registered("fc-a", TypeFulfillmentCenter, OperatorVendor, "reg-west")
	if err != nil {
		t.Fatalf("re-registered: %v", err)
	}
	if refreshed.Status != NodeActive || refreshed.RegionID != "reg-west" {
		t.Fatalf("status/region after re-registration = %s/%s", refreshed.Status, refreshed.RegionID)
	}
	// A legacy registration without operator or region still folds (tolerant decode).
	legacy, err := Node{}.Registered("n-old", TypeInboundCrossDock, "", "")
	if err != nil {
		t.Fatalf("legacy registered: %v", err)
	}
	if legacy.Operator != "" || legacy.RegionID != "" || legacy.Status != NodeProposed {
		t.Fatalf("legacy fold = %+v", legacy)
	}
}

func TestRegisteredRejectsBadOperatorAndRegion(t *testing.T) {
	if _, err := (Node{}).Registered("fc-a", TypeFulfillmentCenter, "ALIEN", "reg-east"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("operator: want validation error, got %v", err)
	}
	if _, err := (Node{}).Registered("fc-a", TypeFulfillmentCenter, OperatorVendor, "reg east!"); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("region: want validation error, got %v", err)
	}
}

func TestIsPlacementCandidate(t *testing.T) {
	base, err := Node{}.Registered("fc-a", TypeFulfillmentCenter, OperatorVendor, "reg-east")
	if err != nil {
		t.Fatalf("registered: %v", err)
	}
	base = base.Activated("fc-a")
	if !base.IsPlacementCandidate() {
		t.Fatal("an ACTIVE vendor FC is a candidate")
	}
	if suspended := base.Suspended("fc-a"); suspended.IsPlacementCandidate() {
		t.Fatal("a SUSPENDED node is not a candidate")
	}
	networkRun, err := Node{}.Registered("fc-n", TypeFulfillmentCenter, OperatorNetwork, "reg-east")
	if err != nil {
		t.Fatalf("registered: %v", err)
	}
	if networkRun.IsPlacementCandidate() {
		t.Fatal("a NETWORK-operated FC is not a candidate")
	}
	dock, err := Node{}.Registered("xd-1", TypeInboundCrossDock, OperatorVendor, "reg-east")
	if err != nil {
		t.Fatalf("registered: %v", err)
	}
	if dock.IsPlacementCandidate() {
		t.Fatal("an INBOUND_CROSS_DOCK is not a candidate")
	}
}

func TestDemandReleaseValidateAndLinesOf(t *testing.T) {
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	r := DemandRelease{AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-a", ReleasedAt: at,
		Lines: []ReleaseLine{{LineNo: 1, CatalogItemID: "itm-1", Quantity: 3}, {LineNo: 2, CatalogItemID: "itm-2", Quantity: 2}}}
	if err := r.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	lines := r.LinesOf()
	if len(lines) != 2 || lines[0].NodeID != "fc-a" || lines[1].ReleasedAt != at.UTC() {
		t.Fatalf("lines = %+v", lines)
	}
	bad := []DemandRelease{
		{AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-a", ReleasedAt: at, Lines: []ReleaseLine{{LineNo: 1, CatalogItemID: "itm-1", Quantity: 0}}},
		{AssignmentID: "", GroupNo: 1, NodeID: "fc-a", ReleasedAt: at},
		{AssignmentID: "asg-1", GroupNo: 0, NodeID: "fc-a", ReleasedAt: at},
		{AssignmentID: "asg-1", GroupNo: 1, NodeID: "?", ReleasedAt: at},
		{AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-a"},
		{AssignmentID: "asg-1", GroupNo: 1, NodeID: "fc-a", ReleasedAt: at, Lines: []ReleaseLine{{LineNo: 0, CatalogItemID: "itm-1", Quantity: 1}}},
	}
	for i, b := range bad {
		if err := b.Validate(); !errors.Is(err, shared.ErrValidation) {
			t.Fatalf("case %d: want validation error, got %v", i, err)
		}
	}
	empty := DemandRelease{AssignmentID: "asg-2", GroupNo: 3, NodeID: "fc-b", ReleasedAt: at}
	if err := empty.Validate(); err != nil {
		t.Fatalf("a release with no lines is a valid no-op fold: %v", err)
	}
	if len(empty.LinesOf()) != 0 {
		t.Fatal("an empty release expands to no lines")
	}
}

func TestNodeAvailabilityValidate(t *testing.T) {
	ok := NodeAvailability{ListingID: "lst-1", CatalogItemID: "itm-1", NodeID: "fc-a", AvailableQty: 20, AsOf: time.Now()}
	if err := ok.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	bad := NodeAvailability{ListingID: "lst-1", CatalogItemID: "itm-1", NodeID: "fc-a", AvailableQty: -1, AsOf: time.Now()}
	if err := bad.Validate(); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("negative qty: want validation error, got %v", err)
	}
	noAsOf := ok
	noAsOf.AsOf = time.Time{}
	if err := noAsOf.Validate(); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("missing asOf: want validation error, got %v", err)
	}
}
