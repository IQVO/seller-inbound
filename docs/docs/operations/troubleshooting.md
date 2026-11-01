---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
sidebar_position: 4
description: "Symptom, cause, check and fix for seller-inbound: boot errors, readiness, outbox, consumers and dead letters, SSCC, and every REST problem type."
---

# Troubleshooting

Each entry is symptom, cause, how to check, how to fix. Every cause comes from the code named in the entry. For the
signals themselves see [Observability](/docs/operations/observability); for routine procedures the
[Runbook](/docs/operations/runbook).

## Boot and readiness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Process exits at once with `DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode` | The variable is empty. There is no in-memory runtime. | The error line `service exited with error` names the variable (`cmd/api/config.go` `loadRuntime`). | Set `DATABASE_URL` ([Configuration](/docs/operations/configuration)). |
| Exit with `KAFKA_BROKERS is required`, `NODE_CONSUMER_GROUP is required: the consumer always runs on real Kafka` (or the catalog or receipt group), or `SSCC_COMPANY_PREFIX is required` | A mandatory `cmd/api` variable is missing. | Same log line. | Set it. The chart's `config.nodeConsumerGroup`, `config.catalogConsumerGroup`, `config.receiptConsumerGroup`, `config.ssccCompanyPrefix` and `kafka.brokers` must all be non-empty. |
| Exit with `EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka` or `EVENT_PUBLISHER: unknown value` | Only `kafka` (or unset) is accepted. | `cmd/api/config.go` `parsePublisherMode`. | Unset the variable or set `kafka`. |
| Exit with an `invalid SSCC configuration` error | `SSCC_COMPANY_PREFIX` is not 6 to 11 digits, or `SSCC_EXTENSION_DIGIT` is not one digit 0..9. | `internal/adapters/outbound/sscc/sscc.go` (`ErrInvalidConfig`). | Correct the value. The prefix must be the network's real GS1 company prefix. |
| `cmd/a2a` exits naming `A2A_AGENT_URL` | The Agent Card URL is missing, relative or not `http(s)`. | `cmd/a2a/main.go` `validateBootConfig`. | Set an absolute URL, for example `http://seller-inbound-a2a.warehouse-systems.svc.cluster.local:8093/`. |
| Crash-loop after `retrying` lines (`op=run migrations` or `ping postgres`), then `service exited with error` | Postgres unreachable or the migration failed; the boot retry (`internal/bootretry`, 5 attempts, backoff 1+2+4+8 s) is exhausted and the process refuses to boot. | The last `err` on the `retrying` lines; `kubectl logs --previous`. | Fix the DSN, the Secret `seller-inbound-db` or PgBouncer; the pod restarts into a healthy database. |
| Migration hangs or fails behind PgBouncer | golang-migrate holds a session advisory lock that does not survive a transaction-pooling proxy. | The migration step never finishes. | Set `MIGRATIONS_DATABASE_URL` to a direct DSN (chart `database.migrationsExistingSecretKey`). |
| `/readyz` answers 503 `{"status":"not_ready"}` | The pod is draining: SIGTERM arrived and the drain gate flipped. | Pod phase `Terminating`; the log line `shutdown: readiness flipped to not-ready`. | Nothing; it is the graceful stop. |
| `/readyz` answers 503 `dependency-unavailable`, detail `not ready: kafka` (or `postgres`) | The ping or the topic-less Kafka metadata request failed in the last 3 s round. | WARN `readiness: dependency is not ready` with `dependency` and `error`. | Restore Postgres or the broker (`KAFKA_BROKERS`). Writes keep working while only Kafka is down: events queue in `outbox_events`. |
| Pod restarts after Kafka was down | A consumer or the relay returned on its own after a persistent broker failure (`maxTransientFetchFailures` = 10); `watchWorkers` exits the process so the orchestrator restarts it. | ERROR `consumer stopped` or `outbox relay stopped`. | Restore the broker; the restart is by design. |
| Shutdown takes longer than the pod's grace period | `SHUTDOWN_DRAIN_DELAY` (5 s) + HTTP drain (10 s) + worker drain (10 s each) exceed `terminationGracePeriodSeconds`. | WARN `worker did not stop before the shutdown drain deadline`. | Keep `terminationGracePeriodSeconds` above the drain delay + 30 s. |

## Outbox and events

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Downstream contexts do not see `InboundShipmentShipped` (or any event) | Kafka down, or the relay cannot publish a row. | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id;` and ERROR `outbox relay pass failed` (`error` starts `outbox row <id> (<type>):`). | Restore Kafka; the backlog drains within `OUTBOX_RELAY_INTERVAL` passes. |
| A row stays unpublished while later rows publish | The row reached `OUTBOX_MAX_ATTEMPTS` (default 20) and is excluded from claims; ERROR `outbox relay: row reached the attempt cap...` logged once. | The same SELECT: `attempts >= 20`, `last_error` says why. | Fix the cause, then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. |
| Events published but the CloudEvent is rejected by a consumer | The envelope is the repo's CloudEvents encoder (`internal/adapters/outbound/kafka/encoder.go`). Consumers dead-letter anything they cannot decode. | The consumer's `.dlq` message headers `x-dlq-error`. | Compare the payload with [Events](/docs/api-reference/events). |
| No trace links across services | A message without a `traceparent` header starts a new trace; rows written outside a span carry none. | Kafka headers on the message. | Expected for events written outside a request span. |

## Consumers and dead letters

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Consumer logs `kafka reader error; retrying` or no `event processed` for a topic | The producer has not written yet, so the topic does not exist (the service never creates topics). | The topic list in your broker. | Benign: it recovers once the producer writes. |
| `handling failed; retrying the same message` repeating for one `partition`/`offset` | A transient failure (database error, lost race): the same message is retried with capped exponential backoff (200 ms doubling to 5 s), up to 5 attempts. | The `error` field. | Fix the database; after the 5th attempt the message is dead-lettered. |
| Messages appear on `<topic>.dlq` | Poison message: undecodable payload, a value the domain refuses (`validation failed`), a transition it forbids (`illegal transition`), or a receipt for a shipment this service does not know (`not found`). Or 5 transient attempts failed. | Headers `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error`, `x-dlq-failed-at`. | Fix the producer or the data. A replay re-applies safely only if the original was never committed: check `processed_events` (idempotent on the CloudEvents id) first. |
| `dead-letter publish failed; retrying the same message` | The DLQ write itself fails (broker down). The consumer never drops a message it could not dead-letter. | ERROR line, `attempt`. | Restore the broker. |
| A vendor receipt is dead-lettered with `receipt nodeId ... is not the shipment's node` | The receipt names a different node than the shipment's. | `x-dlq-error` header. | Fix vendor-connectivity's event; this service never edits a shipment to match the dock. |
| A shipment stays `SHIPPED` | The receipt consumer has not received `InboundReceiptSubmitted`, or only a partial receipt arrived (state `RECEIVING`). | `SELECT state FROM inbound_shipments WHERE shipment_id = ...`; consumer lag of `RECEIPT_CONSUMER_GROUP`. | Wait for the vendor; the shipment closes once every manifest carton has an answer. |
| Plans cannot target a node that exists | The local view `node_inbound_views` is fed by node-topology events; a node never seen is treated as unable to receive. | `SELECT * FROM node_inbound_views WHERE node_id = ...`: status must be `ACTIVE`, type `FULFILLMENT_CENTER` or `INBOUND_CROSS_DOCK`. | Check the node consumer lag; the node must have been `NodeRegistered` and `NodeActivated`. |
| `case-qty-mismatches-gtin` for a correct case | The pack level comes from offer-availability events (`catalog_pack_levels`). | `SELECT * FROM catalog_pack_levels WHERE catalog_item_id = ...`: an active CASE row with this GTIN and `units_per_pack >= 2`. | Check the catalog consumer lag; a retired GTIN is never reactivated. |

## SSCC

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Carton creation fails with `sscc serial range exhausted` (a 500) | The sequence `sscc_serial_seq` passed the serial range of the configured prefix (`16 - len(prefix)` digits). | `ErrSerialExhausted` in `internal/adapters/outbound/sscc/sscc.go`; ERROR `request failed`. | Configure a shorter company prefix. **Never reset the sequence**: issued SSCCs are recorded in `sscc_registry` and must stay unique. |
| `sscc-not-unique` (409) on a carton the seller supplied | The SSCC is already in this shipment or registered network-wide (`sscc_registry`). | The 409 detail names the case. | Use another SSCC, or omit it so the network issues one. |
| `invalid-sscc` (422) | Not 18 digits or a wrong mod-10 check digit; also applies to `parentSscc`. | The detail shows the GS1 error. | Correct the number. |

## REST problem types

Errors are `application/problem+json` with `type` = the slug, `title`, `status`, `detail` and `instance` (the
request path). The table is `problemCatalogue` in `internal/adapters/inbound/http/errors.go`.

| Status | `type` | Raised when |
| --- | --- | --- |
| 400 | `invalid-window` | `window` of the scorecard is not `7d` or `30d`. |
| 404 | `not-found` | Unknown seller, plan or shipment, or an unknown `:action` suffix. |
| 409 | `seller-suspended` | A suspended seller creates a plan, shipment or carton, confirms a plan, or ships. |
| 409 | `seller-exists` | `POST /sellers` for a `sellerId` already onboarded. |
| 409 | `node-cannot-receive` | The destination node is unknown, not `ACTIVE`, has no INBOUND capability, or (for a `parentSscc` carton) does not accept pallets. |
| 409 | `sscc-not-unique` | The SSCC is already used. |
| 409 | `shipment-not-labelled` | `:ship` on a shipment that is not `LABELLED`. |
| 409 | `illegal-transition` | A state change the aggregate forbids: confirm or cancel a non-`DRAFT` plan, a shipment for a non-`CONFIRMED` plan, a carton added to a non-`DRAFT` shipment, labelling a non-`DRAFT` shipment, shipping a shipment that is past `LABELLED`. |
| 409 | `concurrent-update` | Another request modified the resource (optimistic version check); re-fetch and retry. |
| 422 | `validation-failed` | Bad JSON, an unknown field, a value outside its bounds, a malformed GTIN, a plan line repeated, a shipment labelled with no carton. |
| 422 | `idempotency-key-required` | A `POST` without an `Idempotency-Key` header of 1 to 128 characters. |
| 422 | `idempotency-key-reused` | The same key and route with a different body. A key replays the stored answer for 24 h. |
| 422 | `case-pack-not-uniform` | A `CASE_PACKED` carton with more than one item, `MIXED` kind, or an item packed at two different quantities. |
| 422 | `case-qty-mismatches-gtin` | The carton quantity differs from the `unitsPerPack` of the item's active CASE level, or the GTIN is not an active CASE level. |
| 422 | `carton-overweight` / `carton-oversize` | Above the node's declared `maxCartonWeightG` or `maxCartonDimsMm` (edges compared sorted, so orientation does not matter). |
| 422 | `invalid-sscc` | See above. |
| 422 | `carton-contents-mismatch-plan` | An item not on the plan, a total above the plan line across the plan's shipments, or a single-shipment plan labelled with totals different from the plan. |
| 503 | `dependency-unavailable` | Only `/readyz`. |
| 500 | `internal-error` | An unmapped error. The detail is always `an unexpected error occurred`; the real error is in the ERROR log line `request failed`. |

Other points: a NUL character (U+0000) in the path, query or body is a uniform client error and never records the
idempotency key; a 5xx is never cached, so the same key can retry; a 4xx is cached and replays.

## Seller scorecard

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| A rate is missing from the scorecard | By design: no shipment closed in the window (`discrepancyRatePerShipment`) or no carton reported (`receiptAccuracyRate`); never `0` or `NaN`. | `GET /sellers/{id}/scorecard?window=30d`. | None. |
| Counts are empty after an upgrade | Migration `0002` backfills from the published events in `outbox_events`; facts deleted before the upgrade are lost. | `SELECT count(*) FROM seller_scorecard_receipts`. | None; nothing else rebuilds them. |
| Scorecard tier looks too harsh or too lenient | Tier thresholds are constants in `internal/domain/scorecard/scorecard.go`. | [ADR 0005](/docs/adr/0005-seller-compliance-scorecard). | A change needs a new ADR, not a config change. |

## MCP and A2A

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| MCP tool returns an error with `unexpected error` | An unmapped error inside a tool; logged as ERROR `mcp tool failed with an unexpected error`. | The log line. | Same causes as `internal-error` above. |
| MCP or A2A shows stale data | They read the database `cmd/api` owns and never write; lag is the data's, not theirs. | Compare with `GET` on the REST API. | Check `cmd/api`. |
| A2A skill answers a problem envelope | The delegated message was malformed or the skill is unknown; it is answered, not raised. | Span `a2a.skill <skill>` status `Error`. | See [API reference](/docs/api-reference). |
