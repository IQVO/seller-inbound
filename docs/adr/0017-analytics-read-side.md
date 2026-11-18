# ADR 0017: analytics read side

## Status

Accepted (2026-11-17). Additive: no published payload, topic, OLTP table or REST route of the api changes. Adds two
binaries (`cmd/si-projector`, `cmd/si-reports`), one analytical-database migration set (`analytics/migrations`), two
read-only report routes, two default-off chart components and a second, separate database.

## Context

seller-inbound owns the inbound-from-sellers facts, but nothing answers them as a trend: the facts leave as
CloudEvents and the OLTP tables hold the current state, with no history in a shape a report can aggregate. The
shipping-and-tracking pilot (its ADR 0012) already built the shape: a projector that folds the context's own topic into
a SEPARATE analytical Postgres, and a read-only reports process over it. This ADR applies that shape here, with the
differences this context forces:

- the facts live on **two** own topics: `retailnetwork.seller-inbound.events` (`PlacementPlanSuggested`,
  `InboundShipmentReceived`) and `retailnetwork.seller-inbound.analytics` (`InboundPlanConfirmed`), so one consumer
  subscribes to both (`GroupTopics`);
- every fact the reports need is already published, so nothing is fanned out, the outbox and the encoder are unchanged
  and no OLTP migration is needed;
- the REST contract of this context is camelCase (ADR 0010), but the new report is snake_case by decision of the
  requester (see "Open decisions").

## Decision

### 1. What is projected

The projector consumes ONLY this context's own two topics. It never reads a sibling's topic and never calls a sibling
over REST or MCP. Three types are projected, dispatched on the FULL CloudEvents `type`
(`com.retailnetwork.partner.seller-inbound.<entity>.<EventName>`, the prefix of `apis/asyncapi.yaml`):

| type | topic | payload fields read | fact |
| --- | --- | --- | --- |
| `com.retailnetwork.partner.seller-inbound.inboundplan.InboundPlanConfirmed` | analytics | `planId` (= subject), `sellerId`, `confirmedAt` | `plan_confirmations` |
| `com.retailnetwork.partner.seller-inbound.placementplan.PlacementPlanSuggested` | events | `planId` (= subject), `sellerId`, `catalogItemId`, `createdAt` | `placement_advisories` |
| `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentReceived` | events | `shipmentId` (= subject), `sellerId`, `receivedAt`, `received[]` (`gtin`, `catalogItemId`, `qtyEaches`) | `shipment_receipts` + `received_stock_lines` |

The other seven published types (`InboundShipmentLabelled`, `InboundShipmentShipped`, `InboundDiscrepancyRecorded`,
`SellerOnboarded`, `SellerSuspended`, `SellerReinstated`, `SellerComplianceViolationRecorded`) are acknowledged and not
projected: no report needs them. Facts keyed on different aggregates sit on different partitions and may arrive in any
order, so the model stores facts and the reports join them at query time.

Tables (`analytics/migrations/0001_seller_inbound_facts`): `analytics_processed_events` (the dedupe set; `occurred_at`
is the CloudEvents `time`, the basis of the freshness lag), `plan_confirmations`, `placement_advisories`,
`shipment_receipts`, `received_stock_lines`. A receipt published before ADR 0014 has no `received` and so no line rows.

### 2. The report: `GET /reports/seller-inbound-performance?from=&to=`

Days are UTC calendar days of the **business instant in the payload** (`confirmedAt`, `createdAt`, `receivedAt`), not
of the CloudEvents `time`. One row per day that has any fact; a day without facts has no row and an empty range is an
empty `days` array. Fields (snake_case):

| field | definition |
| --- | --- |
| `inbound_plans_confirmed` | `InboundPlanConfirmed` events of the day |
| `placement_advisories_issued` | `PlacementPlanSuggested` events of the day |
| `shipments_received` | `InboundShipmentReceived` events of the day |
| `received_eaches`, `received_item_count` | the eaches and the distinct GTINs over EVERY `received` line of the day |
| `received_by_item[]` | per item: `gtin`, `catalog_item_id` (when a line of that day resolved it), `publications` (receipts that listed the GTIN), `qty_eaches`; at most 10, most eaches first, ties by GTIN |
| `advisory_to_receipt_samples`, `advisory_to_receipt_p50_seconds`, `advisory_to_receipt_p95_seconds` | see below; the percentiles are `null` without a sample |

**"Inbound announcements registered"** is `InboundPlanConfirmed`: creating a plan publishes no event, so confirmation is
the only announcement fact the model records. The field is named after the event, not after the requirement's word.

**Received stock per item** is the `received` array of ADR 0014 (summed per GTIN over the cartons that count as
received). The item key is the `gtin`: the EACH-level GTIN when the receipt resolved it, otherwise the carton GTIN.

**Advisory-to-receipt time.** Both instants exist in own facts: the advisory's `createdAt` and the receipt's
`receivedAt`. For every received line that resolved its `catalogItemId`, the sample is the seconds from the LATEST
`PlacementPlanSuggested` of the same `sellerId` and `catalogItemId` at or before the receipt (an advisory at the very
instant counts) to the receipt; a line with no such advisory, or without a resolved item, is not a sample. The day is
the receipt's. Nothing in the own facts links an advisory to a plan or a shipment, so this is the time since the
seller's latest advice about the item, **not a causal lead time**. The reports state it so.

`GET /reports/freshness` (the pilot's shape, snake_case): `as_of` = the newest CloudEvents `time` the projection
applied, `lag_seconds` = now - `as_of` (never negative); both `null` until the first event.

Range rules (the pilot's): `from` and `to` are UTC dates, both inclusive; both omitted = the 30 days ending today; only
`to` = the 30 days ending on `to`; only `from` = `from` .. today; `from` after `to` or a range over 366 days is a 400
`invalid-report-range` problem. The routes are collection-shaped reports (`x-pagination-exempt: report`).

SQL only counts, sums and takes `percentile_cont`. The per-day assembly, the bounded per-item list, the percentile
ranks, the range rules and the freshness lag live in `internal/analytics/report`, a pure package that imports no other
internal package (arch test), is in the coverage scope and in `make mutation`. ONE contract suite runs against the
in-memory twin and against real Postgres.

### 3. What the report does NOT contain, and why

- **Inbound capacity allocations granted or refused, and the Pareto of refusal reasons.** The capacity gate (ADR 0015)
  answers `inbound-capacity-exceeded` over REST and publishes **no event**; the allocation read is a view over the OLTP
  tables and "publishes no event". There is no granted/refused fact in `apis/asyncapi.yaml` to project, so neither
  measure can be computed from own events. A confirmed plan only proves the gate passed or was unenforced
  (`capacityEnforced: false`), which the event does not say. Closing this needs a new analytics-only event
  (for example `InboundCapacityDecided` with the outcome and the refusal reason) raised by the use case; that is a
  contract change for its own ADR and is deliberately not made here.
- Nothing is left out for needing another context's fact: every projected measure comes from this context's own
  events.

### 4. Delivery policy (the projector)

As the pilot, except where noted:

- Consumer group `ANALYTICS_CONSUMER_GROUP` (default `seller-inbound-analytics`), fixed and stable, subscribed to both
  own topics; at-least-once, the offset is committed only after the message was handled. A brand-new group starts at
  the earliest offset, so the model rebuilds from retained history.
- **Dedupe and projection in ONE analytical-database transaction**: `INSERT ... ON CONFLICT DO NOTHING` into
  `analytics_processed_events`; the fact (a receipt and all its lines together) is written only if that insert claimed
  the id. A failure leaves neither; a replay of an applied id is a no-op.
- Not a CloudEvents 1.0 message, or a CloudEvent of a type that is neither projected nor one of the seven acknowledged
  types: skipped and committed past with a **sampled WARN**.
- **Dead-letter ONLY deterministic poison**: an undecodable payload, a subject that is not the aggregate id, a missing
  required field or timestamp, a received line without a GTIN or of zero eaches, or a store rejection in Postgres class
  22 or 23 (for example a GTIN repeated inside one `received`). The raw message goes to
  `retailnetwork.seller-inbound.analytics.dlq` with `x-dlq-*` headers, then the offset is committed. A failed DLQ write
  is retried. The DLQ is not `<events>.dlq`: that name belongs to the sibling consumers of the events topic.
- **Transient failures** are retried forever with capped exponential backoff (200ms to 5s) and never dead-lettered.

### 5. The binaries

- `cmd/si-projector`: the ONLY writer of the analytical database. Env: `ANALYTICS_DATABASE_URL` (required, a direct
  DSN: it runs the embedded migrations), `KAFKA_BROKERS` (required), `ANALYTICS_CONSUMER_GROUP`, `ADMIN_ADDR` (default
  `:8091`, `/healthz` and `/readyz`), `LOG_LEVEL`, `OTEL_SERVICE_NAME` (default `seller-inbound-projector`),
  `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`. It never opens the OLTP database.
- `cmd/si-reports`: read-only HTTP on `HTTP_ADDR` (default `:8092`): `GET /reports/seller-inbound-performance`,
  `GET /reports/freshness`, `/healthz`, `/readyz` (analytical-database ping). It reads `ANALYTICS_READER_DATABASE_URL`
  (fallback `ANALYTICS_DATABASE_URL` for local runs) through a pool whose sessions default to read-only transactions
  with a 15s statement timeout. No Kafka, no OLTP database, no call to any sibling, **no auth** (fleet rule). Errors are
  problem+json. The routes are in `apis/openapi.yaml` under the tag `Reports` with a per-path server on `:8092`.
- Both ship in the one image (`/app/si-projector`, `/app/si-reports`).

### 6. Packaging

The chart gains an opt-in `analytics:` block (default `enabled: false`; the default render is byte-identical to the
previous chart): components `analytics-projector` (Deployment, no Service) and `analytics-reports` (Deployment +
ClusterIP Service on :80, an opt-in HTTPRoute `/api/seller-inbound/reports` rewritten to `/reports` carrying
`konghq.com/regex-priority: "10"` so it outranks the API prefix), an `analytics` Secret or
`analytics.database.existingSecret`. Render-time guards refuse `analytics.enabled` without a DSN source, without
`kafka.brokers` or without a consumer group. The projector never gets the OLTP `DATABASE_URL`; the reports pod gets
neither Kafka nor any DSN but the reader's.

### 7. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the topics. Keeping it out of the OLTP database
means a heavy report cannot take OLTP connections, the two schemas evolve independently, the reports role can be
read-only on a database that holds nothing transactional, and the projector holds no OLTP credentials.

## Consequences

- Both own topics gain one service-owned consumer group (`seller-inbound-analytics`); their contracts are unchanged.
  `apis/asyncapi.yaml` states that this context's own projector now reads the analytics topic.
- The model starts at the earliest retained offset; facts older than the retention have no history.
- `received_by_item` is bounded in the report but the store returns every (day, GTIN) aggregate of the range to the
  assembler (at most 366 days, under the 15s statement timeout): a very wide range over a very large item set is the
  first thing to watch.
- A deploy needs a second database, two roles (projector read-write, reports read-only), the DLQ topic and the consumer
  group above (`warehouse-infra`).
- `internal/architecture/consumer_wired_fitness_test.go` required every `New*Consumer` to be called from `cmd/api`;
  `NewAnalyticsConsumer` is started by `cmd/si-projector`, so the sensor now names that one exception, checks the call
  from that binary and fails if the exception outlives its constructor.
- The CI `mutation-fast` job loops over `./internal/domain/*/` only, so `internal/analytics/report` runs in
  `make mutation` (extended here) but not in that job; adding it needs a change to `.github/workflows/ci.yml`.

## Alternatives considered and rejected

- **Project into the OLTP database**: no isolation of load, schema or credentials.
- **Publish a capacity-decision event now**: a new contract and a use-case change for a measure nobody has asked to
  freeze; recorded in section 3 as the follow-up.
- **Fan the facts out to a new topic (`FanoutEncoder`)**: every fact is already on an own topic; it would need an
  outbox identity change for no new information.
- **Two consumers, one per topic**: two groups and two offsets to keep in step for one model.
- **Day by CloudEvents `time`**: that is when the use case ran, not when the plan was confirmed or the stock received.
- **Dense rows (one per day of the range, zeros included)**: the pilot omits days without facts; the report keeps
  that, and an empty range is an empty array.

## Open decisions

- **snake_case on the wire**: the report and the freshness are snake_case as requested, while every other body of this
  context's REST API is camelCase (ADR 0010). The two conventions coexist until a decision aligns them.
- **Capacity measures and the refusal Pareto** need the new event described in section 3.
