# ADR 0015: Inbound capacity allocation and placement options

## Status

Accepted (2026-11-14).

## Context

A seller can promise any volume to any node: an inbound plan carries quantities
in eaches only, and the inbound limits a node declares are carton constraints
(weight, dims, pallets, SSCC) with no volumetric figure. Nothing in the network
answers "is there room this month", and a node's monthly inbound volume is the
one capacity a fulfilment building actually schedules against. The network's
placement programmes need a second lever too: today every plan is seller-split
(the seller picks one destination), while the network's cross-docks exist
precisely to split inbound across the fulfilment centres of their region.

Wave 4 closes both gaps at the model level: node-topology's declared inbound
limits gain an optional `monthlyVolumeDm3` (the dm3 a node accepts per calendar
month, `NodeOperationalProfileDeclared.inbound.monthlyVolumeDm3`), and
seller-inbound — which already folds that profile into NodeInboundView (ADR
0003) — owns the allocation. The seller's standing is this context's own
compliance scorecard (ADR 0005); no sibling is called, synchronously or
otherwise.

## Decision

- **Line volume.** `inboundplan.Line` gains `UnitVolumeDm3 int`: the
  seller-declared volume of one each, 0 = undeclared, else 1..`MaxUnitVolumeDm3`
  (100000 dm3). `Plan.VolumeDm3() = Σ qtyEaches x unitVolumeDm3`, and it is 0
  when ANY line's unit volume is undeclared — a partial sum is not a fact.
- **Reservations.** A table `inbound_capacity_allocations`, keyed
  `(seller_id, month, node_id)` with `month` a `YYYY-MM` calendar key, holds the
  dm3 the seller's DRAFT and CONFIRMED plans promise to the node. Port
  `CapacityAllocations{Reserved, Reserve, Release, Allocations}` (memory twin
  included); it is transactional bookkeeping maintained by the use cases, NOT an
  aggregate and NOT events. `CreateInboundPlan` reserves (a DRAFT holds
  capacity), `CancelInboundPlan` releases, confirmation keeps the hold; the
  month rolls over implicitly in the key (a reservation lives in the calendar
  month of the plan's `createdAt`).
- **The gate (record-and-allow).** In `CreateInboundPlan`, after `CanReceive`:
  `limit = NodeInboundView.MonthlyVolumeDm3 x tierFactorBps / 10000` where the
  factor comes from this context's own scorecard tier with named constants
  `TierFactorGoodBps = 10000`, `TierFactorWatchBps = 8000`,
  `TierFactorAtRiskBps = 5000` (GOOD 100% / WATCH 80% / AT_RISK 50%; a
  PROVISIONAL scorecard — no observations, so `TierFor` says GOOD — uses the
  GOOD factor). When `limit > 0` AND the plan's `VolumeDm3() > 0` AND
  `reserved + volume > limit`, the plan is refused with
  `ErrInboundCapacityExceeded` (422 `inbound-capacity-exceeded`). When EITHER
  side is unknown — the node declared no monthly volume, or any line's unit
  volume is undeclared — the plan is ALLOWED and the unenforced fact is recorded
  on the plan as `capacityEnforced: false`. The gate never blocks a legacy plan.
- **placementOption.** `Plan` gains `PlacementOption`: `SELLER_SPLIT` (the
  default, the only behaviour before this ADR) or `NETWORK_SPLIT`. A
  `NETWORK_SPLIT` plan's destination MUST be an ACTIVE `INBOUND_CROSS_DOCK`
  (else 422 `network-split-destination`), and the destination's region MUST
  hold at least one ACTIVE `FULFILLMENT_CENTER` the view has seen (else 422
  `network-split-unavailable`: a redistribution with no destination is not a
  recordable fact).
- **capacity-split-v1.** At create, `NETWORK_SPLIT` computes the redistribution
  `[]{nodeId, lineNo, qtyEaches}` deterministically (`lineNo` is the 1-based
  index of the plan line):
  1. candidates = the region's ACTIVE FULFILLMENT_CENTERs in NodeInboundView;
     exactly one candidate takes every line whole;
  2. when EVERY candidate declares `monthlyVolumeDm3` and has remaining
     capacity (`limit x sellerTierFactor - reserved > 0`): rank by remaining
     capacity descending, ties to the lowest nodeId, keep the
     `NetworkSplitMaxNodes = 3` largest, and split each line proportionally to
     their remaining capacity — floor shares, at least 1 unit to each recipient
     that still has units to take, the leftover units to the largest;
  3. otherwise (any candidate declares no limit, or none has remaining
     capacity): the recipients share each line EVENLY — the `NetworkSplitMaxNodes`
     lowest nodeIds, floor shares, leftovers distributed in ascending nodeId
     order — and the plan records the degraded mode.
  The split is stored on the plan and carried in `InboundPlanConfirmed`
  (additive; omitted for SELLER_SPLIT). It is a recorded fact only: nothing
  downstream executes it, and redistribution rides the INBOUND_CROSS_DOCK to
  FULFILLMENT_CENTER lanes that already exist (the inbound freight gap closes
  at the MODEL level in this wave).
- **Fee basis.** A `NETWORK_SPLIT` plan carries `PlacementFeeBasis
  {basis: "NETWORK_SPLIT", month, dm3, nodeIds}` — set exactly when
  NETWORK_SPLIT, never otherwise. It is a recorded basis for a future
  vendor-settlement context, NOT a charge: no money moves, no invoice is
  derived here.
- **REST.** `GET /sellers/{sellerId}/capacity-allocations?month=` reads the
  reservation table (the operator-facing honesty surface; `month` defaults to
  the current month). The plan body and view gain `placementOption`,
  `lines[].unitVolumeDm3`, `redistribution?`, `placementFeeBasis?` and
  `capacityEnforced`; the MCP and A2A plan DTOs gain the same optional fields.

## Consequences

- The gate is inert until a node declares `monthlyVolumeDm3` AND a seller sends
  `unitVolumeDm3` — honest silence, never zeros-as-facts; a legacy plan is never
  refused.
- Reservations are bookkeeping, not promises to a sibling: no event is raised
  for them, and the table is rebuilt by nothing (it is write-side state).
- `redistribution` and `placementFeeBasis` are facts for later consumers (the
  linehaul planner books the actual freight; a settlement context invoices from
  history). The vendor-settlement split of the payments gap stays open.
- Additive wire fields only, omitted when absent; consumers decode tolerantly.
