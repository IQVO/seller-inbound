---
id: analytics-read-side
title: Analytics read side
sidebar_label: Analytics read side
sidebar_position: 6
description: "The seller-inbound projector and reports: what is projected from the context's own events, the seller-inbound performance report, its fields, the environment, the chart flags and what is deliberately not reported."
---

# Analytics read side

Decision record: [ADR 0017](/docs/adr/0017-analytics-read-side). Two optional binaries, off by default in the chart,
turn this context's **own** events into a per-day report. They read no sibling's topic and call no sibling.

| Binary | Role | Port |
| --- | --- | --- |
| `cmd/si-projector` | the only writer of a separate analytical Postgres; consumes `retailnetwork.seller-inbound.events` and `retailnetwork.seller-inbound.analytics` under `ANALYTICS_CONSUMER_GROUP` (default `seller-inbound-analytics`) | `:8091` (`/healthz`, `/readyz`) |
| `cmd/si-reports` | read-only HTTP over the analytical database, no auth | `:8092` |

## What is projected

| Event (`com.retailnetwork.partner.seller-inbound.<entity>.<Name>`) | Topic | Used for |
| --- | --- | --- |
| `inboundplan.InboundPlanConfirmed` | analytics | `inbound_plans_confirmed` (`confirmedAt`) |
| `placementplan.PlacementPlanSuggested` | events | `placement_advisories_issued` (`createdAt`), the advisory side of the latency |
| `inboundshipment.InboundShipmentReceived` | events | `shipments_received` (`receivedAt`), received stock per item, the receipt side of the latency |

The seven other published types are acknowledged and not projected. A message that is not CloudEvents 1.0, or of an
unknown type, is skipped with a sampled WARN; deterministic poison goes to `retailnetwork.seller-inbound.analytics.dlq`;
a transient failure is retried forever.

## `GET /reports/seller-inbound-performance?from=&to=`

`from` and `to` are UTC dates, both inclusive; neither means the 30 days ending today, and at most 366 days. A bad range
is a `400` `invalid-report-range` problem. The body is snake_case: `from`, `to` and `days`, one row per UTC day that has
any fact (a day without facts has no row, an empty range is `"days": []`):

| Field | Meaning |
| --- | --- |
| `day` | the UTC day of each fact's own business instant |
| `inbound_plans_confirmed` | plans confirmed (creation publishes no event) |
| `placement_advisories_issued` | placement advisories recorded |
| `shipments_received` | shipment receipts published |
| `received_eaches`, `received_item_count` | eaches and distinct GTINs over every received line of the day |
| `received_by_item` | at most 10 items, most eaches first: `gtin`, `catalog_item_id` (when resolved), `publications`, `qty_eaches` |
| `advisory_to_receipt_samples`, `..._p50_seconds`, `..._p95_seconds` | seconds from the seller's latest preceding advisory for the same item to a received line of that item; `null` percentiles without a sample |

The advisory-to-receipt figure is the time since the seller's latest advice about the item, **not** a causal lead time:
no own fact links an advisory to a shipment.

`GET /reports/freshness` answers `{"as_of": ..., "lag_seconds": ...}`, both `null` until the first event. The REST
reference is generated from `apis/openapi.yaml` (tag `Reports`).

## Not reported

Inbound capacity allocations granted or refused, and a Pareto of refusal reasons: the capacity gate answers
`inbound-capacity-exceeded` over REST and publishes no event, so there is no own fact to project. A new analytics-only
event would be needed first (ADR 0017, section 3).

## Chart and environment

`analytics.enabled` (default `false`) renders the `analytics-projector` Deployment (no Service) and the
`analytics-reports` Deployment and ClusterIP Service on port 80; `analytics.reports.route.enabled` adds the
`/api/seller-inbound/reports` route. Enabling it needs `analytics.database.projectorUrl` or
`analytics.database.existingSecret`, `kafka.brokers` and `analytics.projector.consumerGroup`; the chart refuses to render
without them. The projector never gets the OLTP `DATABASE_URL`; the reports pod gets only `ANALYTICS_READER_DATABASE_URL`.

| Variable | Binary | Default |
| --- | --- | --- |
| `ANALYTICS_DATABASE_URL` | projector (required, direct DSN: it runs the migrations) | |
| `KAFKA_BROKERS` | projector (required) | |
| `ANALYTICS_CONSUMER_GROUP` | projector | `seller-inbound-analytics` |
| `ADMIN_ADDR` | projector | `:8091` |
| `ANALYTICS_READER_DATABASE_URL` | reports (falls back to `ANALYTICS_DATABASE_URL` for local runs) | |
| `HTTP_ADDR` | reports | `:8092` |
| `LOG_LEVEL`, `OTEL_SERVICE_NAME` (`seller-inbound-projector` / `seller-inbound-reports`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT` | both | as the other binaries |
