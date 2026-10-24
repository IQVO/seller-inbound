# Seller Inbound runbook

**seller-inbound** is the retail network's front door for stock coming in from sellers:
a seller is onboarded, declares an inbound plan in eaches, packs cartons identified by an
SSCC, has the label data produced and ships; the vendor's carton-level receipt closes the
shipment and every discrepancy and compliance violation is recorded, never corrected.
Sources: `README.md`, `docs/adr/0002` (aggregates), `0003` (consumed contracts),
`0004` (event catalogue), `docs/runtime.md` (probes, shutdown, outbox).

## Runtime shape

One component, one process: the `api` Deployment runs `cmd/api` (chart
`charts/seller-inbound`, `replicaCount: 1`, HPA off by default; no MCP, frontend,
projector or reports component). Inside that process (`cmd/api/main.go`):

- REST seller API on `:8080` (`internal/adapters/inbound/http/server.go`): sellers, inbound
  plans, inbound shipments and cartons, label data; every write behind an
  `Idempotency-Key` header (24h replay).
- Transactional outbox relay draining `outbox_events` to Kafka
  (`internal/adapters/outbound/outbox/relay.go`).
- Three Kafka consumers: node (`retailnetwork.node-topology.events`), catalog
  (`retailnetwork.offer-availability.events`), receipt
  (`retailnetwork.vendor-connectivity.events`) - `internal/adapters/inbound/kafka/consumers.go`.
- One loop: a 24h retention sweeper for expired idempotency keys (`startBackground` in
  `cmd/api/main.go`).

If the relay, a consumer or the loop returns on its own, the process exits non-zero so
the orchestrator restarts it (`watchWorkers`); it never sits "alive" with a dead worker.

## Dependencies

- **Postgres** - `DATABASE_URL` (pooled DSN, PgBouncer in-cluster) and
  `MIGRATIONS_DATABASE_URL` (direct DSN for the boot migrations only: golang-migrate
  holds a session advisory lock, which breaks behind a transaction-pooling proxy).
  Both from the Secret `seller-inbound-db` (chart `database.existingSecret`; the
  migrations key is `optional: true`, the binary falls back to `DATABASE_URL`).
  Embedded migrations run at boot with backoff (`internal/bootretry`). The SSCC serial is
  the Postgres sequence `sscc_serial_seq`.
- **Kafka** - `KAFKA_BROKERS` (required): relay sink, the three consumers, `/readyz`.
  Topics are auto-created by the first writer (`AllowAutoTopicCreation: true`,
  `internal/adapters/outbound/kafka/writer.go`). Publishes
  `retailnetwork.seller-inbound.events` and `retailnetwork.seller-inbound.analytics`
  (`.../outbound/kafka/encoder.go`); dead letters go to `<topic>.dlq`
  (`.../inbound/kafka/deadletter.go`).
- **No sibling REST service**: nothing is called synchronously (ADR 0003).

## Health

`internal/adapters/inbound/http/readiness.go`, wired in `cmd/api/main.go`:

- `GET /healthz` - liveness only: `200 {"status":"ok"}` while the process serves, even
  while draining or with every dependency down.
- `GET /readyz` - `200 {"status":"ready"}` only while the drain gate is open, Postgres
  answers `PING` and a Kafka broker answers a topic-less metadata request (probing
  never creates topics). Verdict cached 3s, one round bounded 2s. Draining: `503
  {"status":"not_ready"}`. Dependency down: `503 application/problem+json`
  `dependency-unavailable` naming `postgres` and/or `kafka`.

Writes with a dependency down, per code:

- **Postgres down** - writes fail: aggregate, idempotency row and outbox rows commit in
  ONE Postgres transaction, so nothing durable can happen (`errors.go` falls through
  to the internal-error problem). `/readyz` 503 pulls the pod from Kong first.
- **Kafka down** - writes are STILL accepted: events are committed to `outbox_events`
  and the relay retries every `OUTBOX_RELAY_INTERVAL`; only `/readyz` goes 503, and the
  process exits when a consumer notices (`watchWorkers`) so it restarts into a healthy broker.

## Configuration

Chart `charts/seller-inbound` - each env var below maps 1:1 to a `values.yaml`
`config.*` key rendered into the ConfigMap/container env (checksum-annotated so a
change rolls the pods); secret keys come from the `seller-inbound-db` Secret.

| Env var | Chart value | Default / meaning |
| --- | --- | --- |
| `DATABASE_URL` | `database.existingSecret` | required, pooled Postgres DSN |
| `MIGRATIONS_DATABASE_URL` | `database.migrationsExistingSecretKey` | optional, direct DSN for boot migrations |
| `KAFKA_BROKERS` | `kafka.brokers` | required, comma-separated `host:port` |
| `NODE_CONSUMER_GROUP` | `config.nodeConsumerGroup` | required, stable group id (node-topology) |
| `CATALOG_CONSUMER_GROUP` | `config.catalogConsumerGroup` | required (offer-availability packaging) |
| `RECEIPT_CONSUMER_GROUP` | `config.receiptConsumerGroup` | required (vendor-connectivity receipts) |
| `SSCC_COMPANY_PREFIX` | `config.ssccCompanyPrefix` | required, the network's GS1 company prefix, 6 to 11 digits |
| `SSCC_EXTENSION_DIGIT` | `config.ssccExtensionDigit` | `0`; a single digit 0..9 (ADR 0002 A5) |
| `EVENT_PUBLISHER` | `config.eventPublisher` | `kafka` only; anything else is a boot error |
| `HTTP_ADDR` | `config.httpAddr` | `:8080` |
| `LOG_LEVEL` | `config.logLevel` | `info` (`debug|info|warn|error`) |
| `OUTBOX_RELAY_INTERVAL` | `config.outboxRelayInterval` | `1s` |
| `OUTBOX_MAX_ATTEMPTS` | `config.outboxMaxAttempts` | `20`; `0` = unlimited |
| `SHUTDOWN_DRAIN_DELAY` | `config.shutdownDrainDelay` | `5s`; `0` disables |

## Alerts and dashboards

- Grafana dashboard: uid `warehouse-seller-inbound` (fleet convention, configmap
  `grafana-dashboards-warehouse` in namespace `observability`).
- Pod-restart alerting covers every pod in `warehouse-systems`
  (`WarehousePodRestarting` on `kube_pod_container_status_restarts_total`,
  warehouse-infra `terraform/alerting/warehouse-alert-rules.json`).
- Logs are JSON on stdout; Alloy maps the pod label `app.kubernetes.io/name` to the
  Loki label `app`, so query Loki with `{namespace="warehouse-systems",
  app="seller-inbound"}`.
- No business metrics are instrumented: `go.mod` has no Prometheus client and the code
  exports no metrics. Business/event panels stay empty until the service instruments
  itself; alerting today can only see pod restarts, not lag or outbox depth.

## Failure modes

- **Pod crash-loops.** Diagnosis: `kubectl -n warehouse-systems logs deploy/seller-inbound`
  - a missing/invalid env var is a boot error naming the variable (including an
  `SSCC_COMPANY_PREFIX` that is not 6 to 11 digits); migrations and the first ping retry
  with backoff before the process gives up. The pod also restarts by design when the
  relay, a consumer or the loop dies. Action: fix the ConfigMap/Secret (or the
  dependency), then `rollout restart`.
- **Postgres unreachable.** Symptom: `/readyz` 503 naming `postgres`; REST writes fail
  (see Health). Action: check PgBouncer and the `seller-inbound-db` Secret; recovery is
  automatic.
- **Kafka unreachable.** Symptom: `/readyz` 503 naming `kafka`; REST writes are STILL
  accepted and queue in `outbox_events` (`published_at IS NULL`); the relay retries each
  pass. Action: restore brokers; the backlog drains itself.
- **Outbox relay stuck / a row parked at the attempt cap.** A row whose `attempts`
  reached `OUTBOX_MAX_ATTEMPTS` (default 20) is EXCLUDED from future claims
  (`AND attempts < $2` in `internal/adapters/outbound/postgres/outbox_repository.go`)
  and stays in `outbox_events` with `attempts`/`last_error` for inspection; the relay
  logs `outbox relay: row reached the attempt cap and is excluded from future claims;
  re-arm it with attempts = 0 after fixing the cause` once at ERROR (`relay.go`,
  `AttemptCapReachedMsg`). Later rows are NOT blocked. Re-arm after fixing the cause:
  `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`
- **Consumer lag or retry logs.** The three consumed topics belong to their producers;
  if a producer has not written yet, its topic does not exist and the consumer only logs
  reader retries - benign. A transient handler failure retries the SAME message with
  capped exponential backoff up to 5 attempts, then dead-letters it to `<topic>.dlq`; a
  payload that can never be applied (undecodable, refused by the domain, a vendor receipt
  for a shipment this service does not know) is dead-lettered at once
  (`internal/adapters/inbound/kafka/consumer.go`, `deadletter.go`). Effects are idempotent
  on the CloudEvents id (`processed_events`), so replaying a `.dlq` topic re-applies safely
  only if the original was never committed - inspect `processed_events` first.
- **A seller reports `node-cannot-receive` for a node that exists.** The node view is
  fed by node-topology events: check that the node was `NodeActivated` and has a type with
  the INBOUND capability (`node_inbound_views`), and that the node consumer is not behind.
- **`case-qty-mismatches-gtin` for a correct case.** The pack level comes from
  offer-availability events (`catalog_pack_levels`): check the CASE level is `active` with
  the expected `units_per_pack`.
- **SSCC serial range.** The sequence `sscc_serial_seq` must stay below the serial range of the
  configured prefix (`16 - len(prefix)` digits); an exhausted range fails carton creation
  with an internal error naming `sscc serial range exhausted`. Never reset the sequence:
  issued SSCCs are recorded in `sscc_registry` and must stay unique.

## Operational actions

```sh
kubectl -n warehouse-systems rollout restart deploy/seller-inbound
kubectl -n warehouse-systems logs deploy/seller-inbound --follow
```

Logs in Grafana Loki: `{namespace="warehouse-systems", app="seller-inbound"}`
(JSON lines; `level`, `loop`, `consumer`, `error` fields). Restarts are graceful:
`/readyz` flips to 503, `SHUTDOWN_DRAIN_DELAY` (5s) waits, HTTP drains (10s), then
consumers and the loop stop, the relay LAST (so nothing committed is stranded); the chart's
`terminationGracePeriodSeconds` must stay above drain delay + 30s. Any `config.*`
value change rolls the pods automatically (ConfigMap checksum annotation).
