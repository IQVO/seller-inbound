# ADR 0016: Start the order-sourcing consumer

## Status

Accepted (2026-11-14). Amends [ADR 0003](./0003-consumed-contracts-and-local-read-models.md) (its list of consumer
group variables) and completes [ADR 0012](./0012-the-placement-advisory.md) (the demand half of `DemandHeatView`).

## Context

ADR 0012 has this context read order-sourcing's events to feed the demand half of `DemandHeatView`
(`demand_heat_lines`). The pieces were built (`SourcingHandlers`, `NewSourcingConsumer`, the use cases
`ApplyFulfillmentReleased` and `ApplyFulfillmentCancelled`, the topic constant `OrderSourcing`, the two types in
`apis/asyncapi.yaml`) but `startBackground` in `cmd/api/wire_background.go` built only the node, catalog and receipt
consumers, `cmd/api/config.go` listed only three group variables and the chart rendered three. The result: nothing
read `retailnetwork.order-sourcing.events`, `demand_heat_lines` was never written and every placement plan was empty,
with no failing test, log line or alert. The README and the docs said so from 2026-11-14.

ADR 0012 names `FulfillmentLocked` as the observed demand. As built, the demand is the release
(`FulfillmentReleasedToNode`, one row per released line, the release instant being the CloudEvents `time`) and its
withdrawal (`FulfillmentCancelled`); this ADR records that as the as-built contract and does not change it.

## Decision

1. `cmd/api` starts a fourth consumer, the sourcing consumer, on `retailnetwork.order-sourcing.events`
   (`NewSourcingConsumer`, started in `startBackground` next to the other three).
2. Its group comes from the mandatory variable `SOURCING_CONSUMER_GROUP`, exactly like the other three (ADR 0003): no
   default and no literal group id in code; a missing value is a boot error naming the variable. ADR 0003's list of
   group variables is therefore `NODE_CONSUMER_GROUP`, `CATALOG_CONSUMER_GROUP`, `RECEIPT_CONSUMER_GROUP` and
   `SOURCING_CONSUMER_GROUP`.
3. The chart value is `config.sourcingConsumerGroup`. It has no default and an empty value refuses to render naming
   the variable. The cluster value, `seller-inbound-order-sourcing`, lives in `warehouse-infra`
   `helm-values/seller-inbound.yaml`.
4. The wire contract is unchanged and decoded tolerantly. A merged release (order-sourcing ADR 0015) carries extra
   optional fields (`members`, per-line `assignmentId`, `ownerRef`) that the decoders ignore; each line of a merged
   release lands as its own row. A test feeds the golden merged payload shape.
5. A sensor keeps this from recurring: `TestEveryConsumerConstructorIsStartedFromCmdApi`
   (`internal/architecture/consumer_wired_fitness_test.go`) fails when a `New*Consumer` constructor declared in
   `internal/adapters/inbound/kafka` is not called from `cmd/api`. It failed on the tree before this change, naming
   `NewSourcingConsumer`.

## Consequences

- `demand_heat_lines` is fed: a release replaces its assignment group's rows, a cancel removes them, a replayed event
  id is a no-op through `processed_events`. Placement plans have demand once order-sourcing has released stock
  (`cmd/api/e2e_sourcing_integration_test.go`).
- A deployment without `SOURCING_CONSUMER_GROUP` no longer boots. The chart value must be merged in `warehouse-infra`
  before the chart version that renders it is deployed; the infra key was merged first and is ignored by a chart that
  does not know it.
- A `kafka-go` group consumer that starts before its topic exists never picks it up later (fleet gap row 45). The pod
  must be restarted after `retailnetwork.order-sourcing.events` exists; this is not solved here.
- Placement plans computed before the consumer ran stay empty; a new `POST /placement-plans` supersedes them.
