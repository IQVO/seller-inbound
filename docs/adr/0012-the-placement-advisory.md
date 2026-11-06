# ADR 0012: the placement advisory (PlacementPlan)

## Status

Accepted (2026-11-06).

## Context

The retail network holds every fact needed to answer "where should forward
stock sit" and answers nothing: sellers choose destination nodes blindly, and
the network's cost-to-serve suffers exactly where demand concentrates far
from inventory. Public fulfillment-network practice names inbound placement
the top remaining cost lever, and its monetised placement programmes all rest
on the same primitive the network already has: per-(item, region) demand
observed at fulfilment time, and per-node availability declared by listings.
seller-inbound already consumes both event streams (order-sourcing's
`FulfillmentLocked`, offer-availability's listing events) — the advisory needs
no new subscription and no synchronous call.

## Decision

- **DemandHeatView** (local read model, migration 0004): per
  (catalogItemId, regionId), the units of `FulfillmentLocked` lines over a
  28-day observation window (the region resolved from the locked node's
  region, kept from node-topology events); per (catalogItemId, nodeId), the
  current declared listing availability.
- **PlacementPlan** aggregate with the deterministic formula `demand-heat-v1`:
  for each region with window demand, `suggestedUnits = max(0,
  ceil(regionDemandUnits x coverDays / windowDays) - regionAvailability)`,
  split across the region's ACTIVE vendor fulfilment centres proportionally
  to their availability (evenly when all are zero); regions without demand
  produce no suggestion; **an item with no demand anywhere yields an EMPTY
  plan, which is legal and recorded** — absence of advice is a fact, not an
  error. `coverDays` is 1..90, default 14. A new plan for the same
  (sellerId, catalogItemId) SUPERSEDES the previous one.
- **REST**: `POST /placement-plans` (the compute command, idempotent by key),
  `GET /placement-plans?sellerId=&catalogItemId=` (keyset `{items,nextCursor}`,
  newest first) and `GET /placement-plans/{planId}`;
  `POST /placement-plans/{planId}/acceptances` moves SUGGESTED → ACCEPTED
  (illegal-transition afterwards).
- **PlacementPlanSuggested** is an integration event (the advisory is a fact
  about the network's state) with an analytics-only consumer in v1.
- **Worked example** (the fixture test): a region with window demand 140
  units over 28 days, availability 40 at fc-1 and 0 at fc-2, coverDays 14 →
  region needs ceil(140 x 14 / 28) = 70, shortfall 30; fc-1 suggests 30
  (all of it: proportional to availability, fc-2 holds none), fc-2 suggests 0.

## Consequences

- Advisory-only in v1: ACCEPTED drives nothing downstream; the seller ships
  through the existing inbound-plan flow and the linkage is manual.
  Auto-replenishment agreements (the network placing stock on the seller's
  behalf) are explicitly out of scope.
- The formula is deliberately planner-free: deterministic, auditable, and
  correctable by publishing a new basis version — optimisation belongs in a
  later ADR once the advisory has consumers.
- Demand is observed at LOCK time, not delivery time: it measures where the
  network actually fulfilled, which is the signal placement should feed.
