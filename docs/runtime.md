# Runtime: configuration, probes, shutdown and the outbox

`cmd/api` is the only binary. It runs on real PostgreSQL and real Kafka; there is no
alternative runtime and no log sink.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN of the service's pool. A boot error names it when unset. |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy). |
| `KAFKA_BROKERS` | required | Comma-separated `host:port` list (relay, consumers and `/readyz`). A boot error names it when unset or blank. |
| `NODE_CONSUMER_GROUP`, `CATALOG_CONSUMER_GROUP`, `RECEIPT_CONSUMER_GROUP` | required | Stable consumer group ids of the node-topology, offer-availability (packaging) and seller-inbound (receipts) consumers. A boot error names the missing one. |
| `SSCC_COMPANY_PREFIX` | required | The network's GS1 company prefix, 6 to 11 digits; SSCCs the network issues are built from it (ADR 0002 A5). A boot error names it when unset or invalid. |
| `SSCC_EXTENSION_DIGIT` | `0` | Extension digit of issued SSCCs, one digit 0..9. |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; any other value is a boot error. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Poll interval of the relay (Go duration or seconds). |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |

Configuration errors are raised before anything is opened or bound.

## Boot and stop

1. Validate the environment. 2. Run the embedded migrations and ping Postgres, both with
backoff. 3. Start the outbox relay, the three consumers and the daily retention sweeper. 4. Serve.

On SIGINT/SIGTERM: `/readyz` flips to 503 `not_ready`, wait `SHUTDOWN_DRAIN_DELAY`, drain
HTTP (10 s), stop and await the consumers and the loop (10 s), then the relay (10 s), close the
pool. If a consumer, a loop or the relay returns on its own, the process exits non-zero so
the orchestrator restarts it.

## Probes

- `GET /healthz` is liveness: 200 `{"status":"ok"}` while the process serves, even while
  draining or while a dependency is down.
- `GET /readyz` is 200 only if the drain gate is open, Postgres answers a ping and a Kafka
  broker answers a metadata request. The request names no topics (an empty, non-nil list), so
  it never creates a topic; a fresh connection is dialled on every check. While draining it
  answers 503 `not_ready`; while a dependency is down, 503 `application/problem+json` of type
  `dependency-unavailable` naming it. Verdicts are cached 3 s.

## The outbox and the consumers in operation

- Events are written in the same transaction as the aggregates and published later, so a write
  is accepted while Kafka is down; `outbox_events.published_at IS NULL` rows wait and are
  published, in id order, when the broker is back. Delivery is at-least-once with a stable
  CloudEvents `id`.
- A row that failed `OUTBOX_MAX_ATTEMPTS` times is skipped (logged once at ERROR with id,
  topic, type and key) and stays in the table with `attempts` and `last_error`. Fix the cause,
  then re-arm it: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`.
- Consumers claim each CloudEvents id in `processed_events` in the same transaction as their
  writes. A transient failure is retried, then published to `<topic>.dlq`; a payload that can
  never decode goes there at once. A vendor receipt for a shipment this service does not know is dead-lettered at once.
- Idempotency keys older than 24 h are purged by the daily retention sweep (an expired key is also
  replaced on its next use).

## Deployment (Helm chart)

`charts/seller-inbound/` runs `cmd/api` as one Deployment (`component=api`); the relay, the consumers and the loop live in that process. `config.*` values map 1:1 to the variables above and are rendered both into the chart ConfigMap (`envFrom`) and into the container env from one helper; `tests/render_test.sh` fails when `cmd/api` reads a variable the chart does not render, or the chart renders one the binary does not read.

| Value | Variable | Rule |
| --- | --- | --- |
| `database.url` or `database.existingSecret` (+ `existingSecretKey`) | `DATABASE_URL` | `secretKeyRef`, never in the ConfigMap; render fails when neither is set. |
| `database.migrationsExistingSecretKey` | `MIGRATIONS_DATABASE_URL` | `secretKeyRef` with `optional: true`; the binary falls back to `DATABASE_URL`. |
| `kafka.brokers` | `KAFKA_BROKERS` | Required. |
| `config.nodeConsumerGroup`, `config.catalogConsumerGroup`, `config.receiptConsumerGroup` | `NODE_`/`CATALOG_`/`RECEIPT_CONSUMER_GROUP` | Required, stable and service-prefixed (`seller-inbound-node`, ...), never the pod name. |
| `config.ssccCompanyPrefix`, `config.ssccExtensionDigit` | `SSCC_COMPANY_PREFIX`, `SSCC_EXTENSION_DIGIT` | The prefix is required (6 to 11 digits, the render fails otherwise); the extension digit is optional. |
| `config.eventPublisher` | `EVENT_PUBLISHER` | Only `kafka`; `*_MODE` and a non-kafka publisher in `extraEnv` are refused. |
| `config.outboxRelayInterval`, `shutdownDrainDelay` | the relay and drain variables | Optional; not rendered while empty so the binary default applies. `shutdownDrainDelay` takes whole seconds. |
| `config.outboxMaxAttempts` | `OUTBOX_MAX_ATTEMPTS` | Default 20, `0` = unlimited. |
| `terminationGracePeriodSeconds` | n/a | Default 40; refused under 40 and under drain delay + 30 s (drain delay 5 s + HTTP 10 s + consumers and the loop 10 s + relay 10 s = 35 s). |

The pod runs as uid 1000 with a read-only root filesystem, no privilege escalation, all capabilities dropped and the `RuntimeDefault` seccomp profile; liveness is `/healthz`, readiness `/readyz`, plus a startup probe covering the boot retry. The `release` job packages `seller-inbound-<version>.tgz` and pushes it to `oci://ghcr.io/iqvo`.
