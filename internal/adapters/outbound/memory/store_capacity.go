package memory

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/capacity"
)

// allocKey is the reservation key of ADR 0015.
type allocKey struct {
	seller, month, node string
}

// CapacityAllocations is the in-memory ports.CapacityAllocations.
type CapacityAllocations struct{ s *Store }

var _ ports.CapacityAllocations = (*CapacityAllocations)(nil)

// Reserved answers the seller's reserved dm3 for the month and node.
func (r *CapacityAllocations) Reserved(ctx context.Context, sellerID, month, nodeID string) (int64, error) {
	defer r.s.enter(ctx)()
	return r.s.capacity[allocKey{sellerID, month, nodeID}].ReservedDm3, nil
}

// Reserve adds dm3 to the key's reservation (0 is a no-op).
func (r *CapacityAllocations) Reserve(ctx context.Context, sellerID, month, nodeID string, dm3 int64, at time.Time) error {
	defer r.s.enter(ctx)()
	if dm3 == 0 {
		return nil
	}
	k := allocKey{sellerID, month, nodeID}
	cur, ok := r.s.capacity[k]
	if !ok {
		cur = capacity.Allocation{SellerID: sellerID, Month: month, NodeID: nodeID}
	}
	cur.ReservedDm3 += dm3
	cur.UpdatedAt = at.UTC()
	r.s.capacity[k] = cur
	return nil
}

// Release subtracts dm3 from the key's reservation, never below 0 (0 is a
// no-op).
func (r *CapacityAllocations) Release(ctx context.Context, sellerID, month, nodeID string, dm3 int64) error {
	defer r.s.enter(ctx)()
	if dm3 == 0 {
		return nil
	}
	k := allocKey{sellerID, month, nodeID}
	cur, ok := r.s.capacity[k]
	if !ok {
		return nil
	}
	cur.ReservedDm3 -= dm3
	if cur.ReservedDm3 < 0 {
		cur.ReservedDm3 = 0
	}
	r.s.capacity[k] = cur
	return nil
}

// Allocations answers the seller's reservations of the month, ordered by node
// id.
func (r *CapacityAllocations) Allocations(ctx context.Context, sellerID, month string) ([]capacity.Allocation, error) {
	defer r.s.enter(ctx)()
	var out []capacity.Allocation
	for k, a := range r.s.capacity {
		if k.seller == sellerID && k.month == month {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}
