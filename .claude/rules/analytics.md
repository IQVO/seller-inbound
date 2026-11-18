---
paths:
  - "cmd/si-projector/**"
  - "cmd/si-reports/**"
  - "analytics/**"
  - "internal/analytics/**"
  - "internal/adapters/outbound/analyticsstore/**"
  - "internal/adapters/inbound/kafka/analytics_*.go"
  - "internal/adapters/inbound/reportshttp/**"
---

# Analytics read side (projector + reports)

Record: `docs/adr/0017-analytics-read-side.md`. The read side consumes the facts this context already publishes; do not
change the encoder, the outbox or the two topic contracts from here.

- Source: ONLY this context's own two topics, `retailnetwork.seller-inbound.events` and
  `retailnetwork.seller-inbound.analytics` (never a sibling's, never a REST/MCP call to one). Three projected types,
  dispatched on the FULL CloudEvents type (`internal/adapters/inbound/kafka/analytics_decode.go`); the other seven own
  types are acknowledged silently; anything else (and non-CloudEvents) is skipped with a sampled WARN.
- `cmd/si-projector` is the ONLY writer of the SEPARATE analytical database (`ANALYTICS_DATABASE_URL`, `KAFKA_BROKERS`,
  `ANALYTICS_CONSUMER_GROUP` default `seller-inbound-analytics`, `ADMIN_ADDR` `:8091`). It never opens the OLTP
  database. `cmd/si-reports` (`HTTP_ADDR` `:8092`) reads through a read-only pool (`ANALYTICS_READER_DATABASE_URL`,
  falling back to `ANALYTICS_DATABASE_URL`).
- Dedupe on the CloudEvents `id` and the fact write (a receipt with all its lines) are ONE analytical-database
  transaction (`Projection.Apply`); test atomicity with a trigger that RAISEs (`postgres_integration_test.go`).
- Delivery policy: dead-letter ONLY deterministic poison (bad payload, subject mismatch, missing field, Postgres class
  22/23) to `retailnetwork.seller-inbound.analytics.dlq`; retry transient failures forever with capped backoff.
- `internal/analytics/report` is pure and imports no other internal package (arch test). SQL only counts, sums and
  `percentile_cont`; the per-day assembly, the bounded per-item list, ranks, range rules and the freshness lag live
  there, in `make mutation` and in the coverage scope. One contract suite (`analyticsstore/contract_test.go`) runs
  against `MemoryStore` and Postgres: change both twins together.
- `analytics/migrations` is embedded (`analytics/migrations.go`), additive, with down files; never edit an applied
  migration, add the next number.
- Reports are unauthenticated, read-only GETs under `/reports/...` (plus `/healthz`, `/readyz`), snake_case, UTC days,
  `from`/`to` inclusive dates. Any route change edits `apis/openapi.yaml` (tag `Reports`) and regenerates the REST
  reference in the same commit. Capacity granted/refused is NOT here: no event carries it (ADR 0017, section 3).
- Chart: the `analytics:` block is opt-in; keep `tests/render_test.sh` and `tests/test_service_selectors.py` green.
