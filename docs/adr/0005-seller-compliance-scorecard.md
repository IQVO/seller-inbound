# 5. Seller compliance scorecard (informational)

## Status

Accepted (2026-10-24).

## Context

Contract P11-7 asks for a per-seller scorecard of rolling-window rates derived from what the context already records when a vendor receipt is applied: how accurate the receipts are, how often and in which way a shipment differs from its manifest, how many compliance violations were raised, and a compliance tier. The contract adds "informational unless the model says it grades" and mentions an additive event.

What the model (`network/model/model.yaml`, context `seller-inbound`) says today:

- `SellerAccount` is `ACTIVE | SUSPENDED`; the only transitions are the commands `SuspendSeller` and `ReinstateSeller`, which the model gives to *network partner managers (human)* (`POST /sellers/{sellerId}:suspend`, `:reinstate`). No policy suspends a seller from observed facts.
- The policy `RecordDiscrepancies` says a discrepancy or compliance code is **recorded as a fact**, "the manifest is never edited". No policy grades, scores or enforces on those facts.
- No scorecard read model, REST route or event exists for this context. (node-performance has a `NodeScorecard`, where only the inbound metrics are explicitly "informational ... never graded": the same stance is taken here.)

So nothing in the model licenses grading, blocking or publishing. This ADR adds a read side only.

## Decision

A `GET /sellers/{sellerId}/scorecard?window=7d|30d` read computes a scorecard from this service's **own facts**, with no new consumer and no new event. It is **informational**: it never suspends, blocks, throttles or otherwise acts on the seller.

### A1 Facts come from two own tables, written with the receipt

The facts the scorecard needs (discrepancies by kind, violations) are published as events but were not stored in a queryable form: the shipment row keeps only per-carton status, not the `Short`/`Over` quantity discrepancies or the compliance codes. Migration `0002_seller_scorecard` adds two append-only tables:

| Table | One row per | Columns that matter |
| --- | --- | --- |
| `seller_scorecard_receipts` | shipment that closed (`InboundShipmentReceived`) | `shipment_id` PK, `seller_id`, `cartons_reported`, `cartons_received`, `recorded_at` |
| `seller_scorecard_occurrences` | `InboundDiscrepancyRecorded` (`DISCREPANCY`, code = kind) or `SellerComplianceViolationRecorded` (`VIOLATION`, code = compliance code) | `seller_id`, `shipment_id`, `occurrence_type`, `code`, `recorded_at` |

Both have an index `(seller_id, recorded_at)`. `ApplyInboundReceipt` records the facts in the **same unit of work** as the shipment save, the outbox append and the inbox claim, and derives them with `scorecard.FactsFrom` from the very events it appends, so the scorecard and the published stream cannot disagree and a redelivered receipt (inbox) or a late one for a closed shipment (no state change) records nothing twice. The closing row is also idempotent by `shipment_id`.

The migration **backfills** from `outbox_events` (the encoded CloudEvents already published, with their write instant), so a deployment that has already closed shipments starts with the scorecard it would have had. An integration test proves the backfilled counts equal the live-recorded ones.

Rejected: reading the outbox at query time (couples a read to the encoded wire format and to outbox retention, and scans a table sized for delivery, not for queries); deriving from the shipment's JSONB cartons (loses `Short`/`Over` and the violations); a consumer of the service's own topic (a Kafka round trip to learn what a local transaction already knows).

### A2 Time basis and window

- A fact is dated by `recorded_at`, the instant **this service** applied the receipt (its clock), **not** the `receivedAt` the vendor reports: a vendor claim can be wrong or in the future, the window rests on our own clock.
- The window is rolling and ends **now**: `[now - N*24h, now]`, **inclusive on both ends**, `N` = 7 or 30. A fixed duration, not calendar days.
- `window` defaults to `30d`; any other value is `400 invalid-window` (`scorecard.ErrInvalidWindow`). The window is validated before the seller is looked up, so an invalid window for an unknown seller is the 400.
- An unknown seller is `404 not-found`. A **suspended** seller has a scorecard like any other.

### A3 Metrics and the derived tier

For one seller in the window:

| Field | Definition | Omitted when |
| --- | --- | --- |
| `shipmentsReceived` | shipments that closed (`CLOSED` or `CLOSED_WITH_DISCREPANCY`) | never (0) |
| `cartonsReported` | cartons those closings reported, any status (manifest cartons plus unexpected ones) | never (0) |
| `cartonsReceived` | of those, status `RECEIVED` | never (0) |
| `receiptAccuracyRate` | `cartonsReceived / cartonsReported`, as node-performance defines inbound accuracy ("share of reported cartons with status RECEIVED") | `cartonsReported` = 0 |
| `discrepancyRatePerShipment` | discrepancies recorded in the window / `shipmentsReceived` | `shipmentsReceived` = 0 |
| `discrepanciesByKind` | count per kind string, passed through (an open vocabulary: `MissingCarton`, `UnexpectedCarton`, `Damaged`, `Short`, `Over` today) | never (an empty object) |
| `complianceViolations` | violations recorded | never (0) |
| `complianceTier` | `GOOD`, `WATCH` or `AT_RISK`, below | never |

A rate with a zero denominator is **omitted**, never `null`, `0` or `NaN`. Rates are rounded to four decimals; the tier is computed from the exact, unrounded rates (so rounding never moves a seller across a threshold).

`complianceTier` is the **worst of three independent signals**; a missing rate is no signal. The thresholds are declared constants in `internal/domain/scorecard/scorecard.go`:

| Signal | `WATCH` | `AT_RISK` |
| --- | --- | --- |
| `receiptAccuracyRate` | strictly below `WatchAccuracyBelow` = 0.98 | strictly below `AtRiskAccuracyBelow` = 0.90 |
| `discrepancyRatePerShipment` | at or above `WatchDiscrepancyRate` = 0.5 | at or above `AtRiskDiscrepancyRate` = 2.0 |
| `complianceViolations` | at least `WatchViolations` = 1 | at least `AtRiskViolations` = 5 |

Otherwise `GOOD`; a seller with nothing observed is `GOOD`. Rationale: 0.98 tolerates about two cartons in a hundred not arriving intact before the first flag, and 0.90 is one in ten, where the inbound flow is no longer reliable; a shipment that differs from its manifest in at least one place per two shipments is worth looking at, two differences per shipment is a pattern; every violation code is a flag worth a look and five in a window is a pattern, not an incident. The accuracy edge is *strictly below* (the threshold value itself is acceptable), the other two edges are *at or above* (the threshold value itself is flagged). These are declared starting points, not values calibrated on traffic. Changing one is a new ADR that supersedes this one; `TestTierThresholdsAreDeclared` pins them to this table.

### A4 Informational: no action, no event

- The tier is a label for people and portals. Nothing reads it to suspend, reject a plan, refuse a carton or change the seller: suspension remains the human `SuspendSeller` command (model policy above). No code path takes a `seller.Account` and a `scorecard.Tier` together; reading the scorecard is a pure query (no outbox row, no state change, proven by a use case test, a godog scenario and the e2e test).
- **No event.** The contract allows an additive `SellerScorecardRecomputed` "only if the model says so": the model has no such event and no consumer for one (every event of this context besides the three vendor-facing ones is "analytics only"). Publishing a derived, recomputed value on every receipt would add a wire contract nobody asked for. When a consumer exists (for example seller-aware sourcing, which contract P2 left out of the first phases) a new ADR adds the event, computed from the same function.

### A5 REST contract

`GET /sellers/{sellerId}/scorecard?window=7d|30d` in `apis/openapi.yaml`: additive (a new path, a new schema `SellerScorecard`, a new parameter and a new `400` response; nothing existing changes), documented with two valid examples (one with data, one with no shipment and the rates omitted) that a test holds to the schema. Wire format camelCase, instants RFC 3339 UTC, errors RFC 9457 `application/problem+json`: `400 invalid-window`, `404 not-found`. No authentication, like the rest of the API; no `Idempotency-Key` (a read).

## Consequences

- The numerator and denominator of `discrepancyRatePerShipment` are windowed independently: a discrepancy is counted when recorded, a shipment when it closes. A partial receipt's discrepancies therefore appear before the shipment closes, and a window can hold discrepancies of a shipment that closes in the next one. Accepted: it keeps every fact in the window it was recorded in, and the rate converges once shipments close.
- A small sample is noisy: one damaged carton of two is already `AT_RISK`. `shipmentsReceived` and `cartonsReported` are on the answer so a reader sees the sample size; a minimum-observations rule (node-performance has one for its grade) is a follow-up if the tier is ever consumed by a machine.
- The facts tables grow by one row per closed shipment and one per discrepancy or violation, and are retained like the aggregates (ADR 0002 A12). The window query is one indexed statement per seller.
- Out of scope, separate work: the network model in warehouse-docs (the route, a read-model entry and the rationale above) is updated by the docs close-out, not by this service; the web remote and an MCP tool for the scorecard (the service has neither today).
- New code: `internal/domain/scorecard` (pure; the mutation gate loops over it), `ports.ScorecardFacts` with a Postgres adapter and an in-memory double, `usecases.GetSellerScorecard`, `GET /sellers/{sellerId}/scorecard`, migration `0002`. No new environment variable, consumer, topic or chart value.
