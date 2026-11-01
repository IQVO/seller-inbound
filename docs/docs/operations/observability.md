---
id: observability
title: Observability
sidebar_label: Observability
sidebar_position: 3
description: "OpenTelemetry metrics, traces and structured logs of seller-inbound, the exporter variables, and the dashboard and alerts that exist in warehouse-infra."
---

# Observability

All three binaries (`cmd/api`, `cmd/mcp`, `cmd/a2a`) follow the same fleet observability standard
([ADR 0008](/docs/adr/0008-fleet-observability-standard)): OpenTelemetry traces and metrics pushed over OTLP/gRPC to a
Collector, JSON logs on stdout that carry `trace_id` and `span_id`, and no Prometheus scrape endpoint. Export never
blocks boot: the gRPC exporters connect lazily, so an absent Collector costs telemetry, never availability.

Source: `internal/adapters/outbound/telemetry/telemetry.go` (`Setup`), `cmd/api/telemetry.go`,
`cmd/mcp/telemetry.go`, `cmd/a2a/telemetry.go`.

## Exporter configuration

| Variable | Default | Effect |
| --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC endpoint (`host:port` or URL). Used for traces and metrics. |
| `OTEL_SERVICE_NAME` | `seller-inbound` (`cmd/api`), `seller-inbound-mcp`, `seller-inbound-a2a` | `service.name` resource attribute and the otelchi server name. |
| `SERVICE_VERSION` | `dev` | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | `deployment.environment.name` resource attribute. |
| `LOG_LEVEL` | `info` | Log threshold; the OTel SDK's own diagnostics go through the same slog handler (exporter errors at `debug`). |

Metrics are pushed every 15 s by a periodic reader. Traces use a batch span processor. The W3C `traceparent` and
`baggage` propagators are installed globally. Full tables per binary: [Configuration](/docs/operations/configuration).

## Metrics

| Instrument | Type | Unit | Attributes | Emitted by | Source |
| --- | --- | --- | --- | --- | --- |
| `seller_inbound.shipment.shipped` | counter (Int64) | `{shipment}` | `outcome` = `shipped` or `rejected`; `reason` (only when rejected) = the problem slug, for example `shipment-not-labelled`, `seller-suspended`, `node-cannot-receive`, `internal-error` | `cmd/api` | `internal/adapters/outbound/telemetry/shipment_metrics.go`, `internal/application/usecases/shipment_metrics.go` |
| `http.server.request.duration` | histogram (Float64) | `s` | `http.method`, `http.scheme`, `http.route` (chi route pattern, never the raw path); no status code | all three binaries | otelchi `metric` package, wired in `internal/adapters/inbound/http/server.go`, `cmd/mcp/router.go`, `cmd/a2a/router.go` |
| Go runtime metrics (`go.goroutine.count`, `go.memory.used`, GC and heap series) | gauges / counters | per the OTel Go runtime instrumentation | runtime-defined | all three binaries | `runtime.Start` in `telemetry.Setup` (`go.opentelemetry.io/contrib/instrumentation/runtime`) |

Explicit histogram buckets of `http.server.request.duration` (seconds): 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25,
0.5, 0.75, 1, 2.5, 5, 7.5, 10.

`seller_inbound.shipment.shipped` counts every attempt to ship an inbound shipment. A falling `shipped` share with a
rising `shipment-not-labelled` means sellers reach the ship step before labelling every carton; a rising
`node-cannot-receive` means destination nodes refuse stock; a rising `internal-error` means the service itself fails.

**What is not instrumented:** consumer lag, outbox backlog and age, dead-letter counts, scorecard computations. The
service has no custom instrument for them; the [Runbook](/docs/operations/runbook) and
[Troubleshooting](/docs/operations/troubleshooting) give the SQL and Kafka reads that replace them.

## Traces

| Span | Created by | Notes |
| --- | --- | --- |
| Server span per HTTP request, named after the chi route pattern | `otelchi.Middleware` in the three routers | One span name per route, not per id. Mounted before the logger so log lines carry its ids. |
| `<topic> process` (kind `CONSUMER`) | `internal/adapters/inbound/kafka/tracing.go` | Attributes `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`. Parented on the `traceparent` header of the message, a new trace when absent. An error from the handler records the error on the span. |
| `a2a.skill <skill>` | `internal/adapters/inbound/a2a/skills.go` | Attribute `a2a.skill`. Status `Error` when the skill answers a problem. |

Trace propagation across the outbox: when a use case appends an event, `internal/adapters/outbound/postgres/outbox_repository.go`
injects the active span's `traceparent` into the row's headers, and the relay puts it on the Kafka message. Consumers of
this service extract it the same way, so a trace follows an event across services.

## Logs

JSON via `log/slog` on stdout; `time`, `level`, `msg` plus the fields below. The handler is wrapped by
`telemetry.WithTraceContext`, which stamps `trace_id` and `span_id` on every record written with a context that holds a
span (the `*Context` slog calls). Boot, shutdown and background-loop lines have none.

| Where | Level | `msg` | Fields |
| --- | --- | --- | --- |
| Request (`cmd/api`, `cmd/mcp`, `cmd/a2a`) | INFO / WARN (4xx) / ERROR (5xx) | `http request` | `method`, `route` (pattern or `unmatched`), `status`, `duration_ms`, `bytes`, `request_id`. `/healthz` and `/readyz` are not logged. |
| REST | ERROR | `request failed` (an unmapped error, answered 500 `internal-error` with the fixed detail `an unexpected error occurred`) | `error`, `method`, `path` |
| MCP | ERROR | `mcp tool failed with an unexpected error` | `error` |
| Boot | INFO | `telemetry configured` | `service_name`, `otlp_endpoint` |
| Boot | INFO | `postgres adapters configured`, `http server listening` | `addr` |
| Boot retry | WARN / INFO | `retrying` / `succeeded after retry` | `op` (`run migrations`, `ping postgres`), `attempt`, `in`, `err` |
| Boot | WARN | `invalid OUTBOX_RELAY_INTERVAL; using the default`, `invalid OUTBOX_MAX_ATTEMPTS; using the default` | `value`, `default` |
| Boot / `cmd/mcp` / `cmd/a2a` | INFO | `mcp server starting`, `a2a server starting` | `version`, `environment` and addresses (`agent_url` for A2A) |
| Boot failure | ERROR | `service exited with error` / `mcp server exited with error` / `a2a server exited with error` (exit 1) | `error` |
| Consumers | INFO | `consumer running` | `consumer` (`node consumer`, `catalog consumer`, `receipt consumer`), `topic`, `brokers` |
| Consumers | INFO | `event processed` (receipt consumer only) | `type`, `event_id`, `subject` |
| Consumers | WARN | `skipping a message that is not a valid CloudEvent` | `consumer`, `topic`, `error` |
| Consumers | WARN | `dead-lettering an event that can never be applied` | `consumer`, `type`, `event_id`, `error` |
| Consumers | WARN | `<consumer> broker connection stalled or lost; fetching again after the backoff` | `error`, `attempt`, `backoff` |
| Consumers | ERROR | `<consumer> handling failed; retrying the same message` | `error`, `partition`, `offset`, `attempt`, `max_attempts`, `retry_in` |
| Consumers | ERROR | `<consumer> handling failed after the maximum attempts; dead-lettering the message`, `<consumer> message can never be applied; dead-lettering it without retry` | `error`, `partition`, `offset` (and `attempts`) |
| Consumers | ERROR | `<consumer> offset commit failed; retrying the same message`, `<consumer> dead-letter publish failed; retrying the same message` | `error`, `partition`, `offset`, `attempt`, `retry_in` |
| Consumers | ERROR | `consumer stopped` (the worker ends; the process then exits) | `consumer`, `error` |
| Relay | INFO | `outbox relay running` | `publisher`, `interval`, `max_attempts`, `topic` |
| Relay | ERROR | `outbox relay pass failed` | `error` (text starts `outbox row <id> (<type>):`) |
| Relay | ERROR | `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `max_attempts`, `error` |
| Relay | ERROR | `outbox relay stopped`, `kafka relay sink close failed` | `error` |
| Retention sweeper | INFO / ERROR | `loop running`, `loop pass done` / `loop pass failed` | `loop` (`retention sweeper`), `interval`, `count` / `error` |
| Readiness | WARN | `readiness: dependency is not ready` (once per check round, cached 3 s) | `dependency` (`postgres` or `kafka`), `error` |
| Shutdown | INFO / WARN | `shutdown: readiness flipped to not-ready; waiting for traffic to drain` / `ignoring invalid SHUTDOWN_DRAIN_DELAY` / `worker did not stop before the shutdown drain deadline` / `telemetry flush failed on shutdown` | `drain_delay` / `value`, `default` / `worker` / `error` |

REST errors are `application/problem+json` documents whose `type` is the bare problem slug; the slug and status table is
in [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).

## Probes

`GET /healthz` (liveness, always 200) and `GET /readyz` on `cmd/api` (200 only while the drain gate is open, Postgres
answers a ping and a Kafka broker answers a metadata request; 503 `not_ready` while draining, 503
`dependency-unavailable` naming the failing dependency otherwise). `cmd/mcp` and `cmd/a2a` serve `/healthz` only.
Details in the [Runbook](/docs/operations/runbook) and [Runtime](/docs/overview/runtime).

## Dashboards and alerts that exist

Read from `warehouse-infra` on `origin/develop`:

- **Grafana dashboard** `warehouse-seller-inbound` (`terraform/dashboards/contexts/seller-inbound.json`, generated by
  `scripts/gen-context-dashboards.py`): Kong request rate, 5xx ratio and p95 latency for the context's routes; service
  RED panels from `http_server_request_duration_seconds_*` filtered `service_name=~"seller-inbound.*"` (request rate by
  route, request duration p95 by process, 5xx per Kong route); the business panel
  `seller_inbound_shipment_shipped_total` by `outcome`; Go goroutines and memory; and Loki panels (live log stream,
  log volume by level, errors and warnings) on `{app="seller-inbound", container!~"istio-proxy|istio-init"}`.
- **Alert rules** (`terraform/alerting/warehouse-alert-rules.json`), groups `warehouse-seller-inbound` and
  `warehouse-seller-inbound-slo`, all from Kong metrics and kube-state: `WarehouseKong5xxRatioHigh` (more than 5 % 5xx
  on a Kong route for 5 minutes, warning), `WarehousePodRestarting` (two or more restarts in 15 minutes, warning),
  `WarehouseApiAvailabilityBurnFast` and `WarehouseApiLatencyBurnFast` (critical), `WarehouseApiAvailabilityBurnSlow`
  and `WarehouseApiLatencyBurnSlow` (warning). The SLO targets are 99 % availability and 95 % of requests under 500 ms
  (measured at the 400 ms Kong bucket).
- **Not covered by any alert or panel:** outbox backlog, consumer lag, dead letters, receipts waiting. There is no
  `outbox` lag gauge in this service.
- Logs reach Loki through Alloy, which maps the pod label `app.kubernetes.io/name` to the Loki label `app`.

The alert annotations in `warehouse-infra` still link `runbook_url` to
`https://github.com/IQVO/seller-inbound/blob/develop/docs/operations/runbook.md`, a path this site's scaffold moved to
`docs/docs/operations/runbook.md`; the link is owned by `warehouse-infra` and not changed here.

## Suggested checks

Not shipped as rules; they are the reads that replace the missing instruments.

| Signal | Read | Worrying when |
| --- | --- | --- |
| Outbox backlog | `SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL` | oldest row older than a few minutes |
| Rows parked at the cap | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20` (use your `OUTBOX_MAX_ATTEMPTS`) | any row; also the ERROR line `row reached the attempt cap` |
| Consumer lag | Kafka consumer-group lag of the three groups | growing, or the same `partition`/`offset` repeating in `handling failed; retrying` |
| Dead letters | depth of `retailnetwork.node-topology.events.dlq`, `retailnetwork.offer-availability.events.dlq`, `retailnetwork.vendor-connectivity.events.dlq` | any message; headers `x-dlq-error`, `x-dlq-source-offset` say why |
| Failed ship attempts | `seller_inbound_shipment_shipped_total{outcome="rejected"}` by `reason` | a rising `internal-error` or `node-cannot-receive` |
| Boot loop | `service exited with error` and the `WarehousePodRestarting` alert | any |
| Table growth | row counts of `outbox_events`, `processed_events` (nothing deletes them) and `idempotency_keys` (purged after 24 h) | capacity planning |
