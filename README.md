# seller-inbound

[![CI](https://github.com/IQVO/seller-inbound/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/IQVO/seller-inbound/actions/workflows/ci.yml?query=branch%3Adevelop) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/seller-inbound/blob/develop/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/seller-inbound/blob/develop/go.mod) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/seller-inbound/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/seller-inbound/blob/develop/LICENSE)

Retail network, bounded context **Seller Inbound** (Supporting, partner tier).

The seller-facing front door for stock coming into the network: a seller is onboarded, plans an inbound shipment to a vendor node in eaches (loose or case-packed), packs it into cartons each identified by an SSCC and a printed label, and ships it. The context checks the plan against the destination node's declared inbound limits and the catalogue item's packaging hierarchy, hands the vendor the shipment as a carton manifest through the Vendor API, and closes the shipment from the vendor's carton-level receipt, recording every discrepancy and compliance violation instead of correcting it. It absorbs the idea reserved earlier as the inventory-placement context; automated placement advice is not modelled (see the gap register).

It owns the `Seller`, `InboundPlan` and `InboundShipment` (with cartons and labels) aggregates and the seller compliance scorecard. It does **not** own the nodes or their inbound limits (node-topology's, consumed), the catalogue packaging hierarchy (offer-availability's, consumed) or the vendor receipt itself (vendor-connectivity hands it over as an event). It is a downstream conformist of those three Open Host Services.

Status: implemented, merged and deployed (the packaging checklist is `docs/before-first-release.md`). The design is proposed in [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound); `network/model/model.yaml` in IQVO/warehouse-docs is the single source of truth. This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message (`com.retailnetwork.partner.seller-inbound.<entity>.<Event>`), transactional outbox.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

This repository has no docs site of its own. Its design documentation lives on the warehouse-docs retail-network pages:

| Area | Pages |
|---|---|
| Retail network | [Network overview](https://iqvo.github.io/warehouse-docs/network) |
| This context | [Index / service facts](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound), [Business context](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/business-context), [Domain events](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/domain-events), [Use cases](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/use-cases) |
| DDD artifact pack | [Ubiquitous language](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/ubiquitous-language), [Context map](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/context-map), [Core domain chart](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/bounded-context-canvas), [Aggregate design canvas](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/aggregate-design-canvas), [EventStorming](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/eventstorming), [Class diagram](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/class-diagram), [Sequence diagrams](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/sequence-diagrams), [ER diagram](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/entity-relationship), [Domain message flow](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound/domain-message-flow) |
| Decisions | [Network ADRs](https://iqvo.github.io/warehouse-docs/network/adr), incl. [ADR 0009: seller inbound and case-packed shipments](https://iqvo.github.io/warehouse-docs/network/adr/0009-seller-inbound-and-case-packed-shipments) |

In-tree: [docs/adr/](docs/adr/index.md) (decisions, index), [docs/runtime.md](docs/runtime.md) and [docs/operations/runbook.md](docs/operations/runbook.md) (operations), `docs/before-first-release.md` (packaging checklist).

## Architecture

Hexagonal / ports & adapters. Dependencies point inward only:

```
cmd/api/                     composition root: seller REST API + outbox relay + the three
                             Kafka consumers + the retention loop, on real Postgres and
                             real Kafka (no in-memory mode, no log sink, no consumer off)
cmd/mcp/                     read-only MCP server (Streamable HTTP on /mcp)
cmd/a2a/                     read-only A2A agent (JSON-RPC over HTTP, Agent Card at
                             /.well-known/agent-card.json)
internal/
  domain/                    seller, inboundplan, inboundshipment, network, scorecard,
                             shared — pure business logic
  application/
    ports/                   OUT interfaces only (repos, EventPublisher, Clock)
    usecases/                one struct per use case
    requests/ outbox/        request handling; outbox orchestration
    views/                   the read models
  adapters/
    inbound/http/            chi handlers, RFC 9457 problem mapping, idempotency middleware
    inbound/kafka/           node-topology, offer-availability, vendor-connectivity consumers
    inbound/mcp/             the four read-only MCP tools
    inbound/a2a/             the two read-only A2A skills
    inbound/requestlog/      the request log line
    outbound/postgres/       pgxpool repos, embedded golang-migrate, UnitOfWork
    outbound/outbox/         transactional outbox writer + relay
    outbound/kafka/          the topic publisher behind the relay
    outbound/sscc/           GS1 SSCC issuing (mod-10, the network's company prefix)
    outbound/telemetry/      OTel wiring + the business counter
    outbound/clock/ ids/     wall clock, id source
    outbound/memory/         test doubles only, never linked into cmd/** (fitness-tested)
  architecture/              the fitness tests below
  bootretry/ testing/        boot retry, shared test helpers
apis/                        openapi.yaml (REST) + asyncapi.yaml (events)
docs/adr/                    9 ADRs + index
features/                    8 godog feature files
charts/seller-inbound/       the Helm chart
web/                         the operator micro-frontend remote
```

The layering is an executable test, not a convention: `internal/architecture/architecture_test.go` (`TestHexagonalArchitecture`) fails the build if the domain ever imports an outer layer or an adapter reaches sideways into a sibling. The same package pins CloudEvents-only envelopes and the event catalogue (`catalogue_fitness_test.go`, `events_fitness_test.go`), the Kafka DLQ/acks/topic rules (`kafka_*_fitness_test.go`), and the read-only MCP/A2A surfaces (`mcp_optional_inputs_test.go`, `a2a_fitness_test.go`).

## Domain events

All facts leave as CloudEvents 1.0 (structured content mode, `application/cloudevents+json`); the envelope and type catalogue are [ADR 0004](docs/adr/0004-cloudevents-envelope-and-type-catalogue.md). The type convention is `com.retailnetwork.partner.seller-inbound.<entity>.<Event>`, for example:

```
com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped
com.retailnetwork.partner.seller-inbound.seller.SellerOnboarded
```

Specified in `apis/asyncapi.yaml` — nine published and eight consumed events:

| Topic | Direction | Messages |
|---|---|---|
| `retailnetwork.seller-inbound.events` | publishes (via the transactional outbox) | `InboundShipmentLabelled`, `InboundShipmentShipped`, `InboundShipmentReceived` |
| `retailnetwork.seller-inbound.analytics` | publishes (analytics-only, via the outbox) | `SellerOnboarded`, `SellerSuspended`, `SellerReinstated`, `InboundPlanConfirmed`, `InboundDiscrepancyRecorded`, `SellerComplianceViolationRecorded` |
| `retailnetwork.node-topology.events` | consumes | `NodeRegistered`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `NodeOperationalProfileDeclared` |
| `retailnetwork.offer-availability.events` | consumes | `CatalogItemPackagingDeclared`, `CatalogItemPackLevelRetired` |
| `retailnetwork.vendor-connectivity.events` | consumes | `InboundReceiptSubmitted` (the carton-level receipt that closes a shipment) |

The vendor's receipt (an event from vendor-connectivity) closes the shipment; every discrepancy and compliance violation is recorded, never corrected.

## Running it

`cmd/api` needs Postgres and Kafka; there is no in-memory mode (`EVENT_PUBLISHER=log` is a boot error). A missing mandatory variable is a boot error naming it. `GET /readyz` is 200 only while Postgres answers a ping AND a Kafka broker answers a metadata request; the process exits non-zero when the relay, a consumer or a loop returns on its own.

```sh
export DATABASE_URL="postgres://user:pass@localhost:5432/seller_inbound?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export NODE_CONSUMER_GROUP=seller-inbound-node
export CATALOG_CONSUMER_GROUP=seller-inbound-catalog
export RECEIPT_CONSUMER_GROUP=seller-inbound-receipt
export SSCC_COMPANY_PREFIX=1234567          # the network's GS1 company prefix, 6-11 digits
go run ./cmd/api                            # migrations run at boot; REST on :8080
```

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Listen address |
| `DATABASE_URL` | **required** | Postgres DSN |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers (relay, consumers, `/readyz`) |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; `log` is a boot error |
| `NODE_CONSUMER_GROUP` | **required** | Stable group id of the node-topology consumer |
| `CATALOG_CONSUMER_GROUP` | **required** | Stable group id of the offer-availability consumer |
| `RECEIPT_CONSUMER_GROUP` | **required** | Stable group id of the vendor-connectivity consumer |
| `SSCC_COMPANY_PREFIX` | **required** | The network's GS1 company prefix, 6 to 11 digits |
| `SSCC_EXTENSION_DIGIT` | `0` | SSCC extension digit 0..9 (ADR 0002 A5) |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `seller-inbound` | `service.name` of spans and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | telemetry default | Collector OTLP/gRPC endpoint; export never blocks |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

`cmd/mcp` (read-only: Postgres only, no Kafka, no writes): `MCP_ADDR` (default `:8090`), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL` (default `DATABASE_URL`), `LOG_LEVEL` (`info`), `OTEL_SERVICE_NAME` (`seller-inbound-mcp`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION` (`dev`), `ENVIRONMENT`.

`cmd/a2a` (read-only, same rules as `cmd/mcp`): `A2A_ADDR` (default `:8093`), `A2A_AGENT_URL` (**required**, absolute http(s) URL advertised on the Agent Card), `DATABASE_URL` (**required**), `MIGRATIONS_DATABASE_URL`, `LOG_LEVEL`, `OTEL_SERVICE_NAME` (`seller-inbound-a2a`), `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0008](docs/adr/0008-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id` and a `request_id`; `cmd/api`, `cmd/mcp` and `cmd/a2a` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`; export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter `seller_inbound.shipment.shipped` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the consumers. The chart's `otel.*` values render the env.

## API

REST contract: [`apis/openapi.yaml`](apis/openapi.yaml). Every state-changing `POST` takes `Idempotency-Key` (1 to 128 characters); a repeat replays the first response, a different body under the same key is `idempotency-key-reused`. Errors are RFC 9457 `application/problem+json`; the shared problem types are `not-found` 404, `illegal-transition` 409, `concurrent-update` 409 and `validation-failed` 422, plus one slug per model `Err...` name. Suspending a suspended seller and reinstating an active one answer `200` with the account and publish no event (ADR 0002 A3). No authentication.

What it does, route by route:

- **Sellers** are onboarded, suspended and reinstated: `POST /sellers`, `POST /sellers/{sellerId}:suspend`, `POST /sellers/{sellerId}:reinstate`, `GET /sellers/{sellerId}`.
- An **inbound plan** (`POST /inbound-plans`, `:confirm`, `:cancel`, `GET /inbound-plans/{planId}`) declares, in eaches, what a seller will send to one destination node (`INDIVIDUAL` or `CASE_PACKED`).
- An **inbound shipment** (`POST /inbound-plans/{planId}/shipments`) takes **cartons** (`POST /inbound-shipments/{shipmentId}/cartons`), each identified by an SSCC: a seller-supplied one is accepted only if valid (18 digits, GS1 mod-10) and unique network-wide, otherwise the network issues one from its own GS1 company prefix.
- `POST /inbound-shipments/{shipmentId}:label` freezes the manifest once the cartons add up to the plan; `GET /inbound-shipments/{shipmentId}/labels` returns the label data per carton (SSCC, GS1-128 element string `(00)<sscc>`, human readable line, contents in eaches, destination node); `POST /inbound-shipments/{shipmentId}:ship` ships.
- **`GET /sellers/{sellerId}/scorecard?window=7d|30d`** answers a seller's rolling-window scorecard from the facts the service recorded (shipments closed, receipt accuracy, discrepancies by kind, compliance violations) and a derived `complianceTier` (`GOOD`, `WATCH`, `AT_RISK`). It is **informational**: it suspends and blocks nothing, suspension stays a human command, and it publishes no event ([ADR 0005](docs/adr/0005-seller-compliance-scorecard.md)).

`GET /healthz` (liveness) and `GET /readyz` (Postgres + Kafka gate) serve the probes.

## Helm chart

Deploy with `charts/seller-inbound`. Values that matter: `config.*` (the env above, incl. the three consumer groups and `SSCC_COMPANY_PREFIX`), `database.url` (or `existingSecret`), `kafka.brokers`, `otel.enabled` (on by default; `false` renders no `OTEL_*` env at all) and `environment`. `mcp.enabled=true` (default off) adds the MCP Deployment + ClusterIP Service; `a2a.enabled=true` (default off) adds the A2A agent's; `frontend.enabled=true` adds the web remote's nginx Deployment + ClusterIP Service — it needs no database and no Kafka, and this chart never routes it (the web gateway owns `/mfes/seller-inbound/`). Linted and rendered in CI by the `helm-lint` (`ct lint`) and `chart-tests` jobs.

### Running the MCP server in Kubernetes

`cmd/mcp` ([ADR 0006](docs/adr/0006-read-only-mcp-server.md)) is the optional second binary: the read-only MCP server (`get_seller_scorecard`, `get_inbound_plan`, `get_shipment_labels`, `get_inbound_shipment`) over the same reads as the REST GETs, Postgres only, Streamable HTTP on `/mcp` (`MCP_ADDR`, default `:8090`), `GET /healthz` for the probes, unauthenticated like REST (access control is the in-cluster ClusterIP boundary), `mcp.enabled` in the chart (default off).

### Running the A2A agent in Kubernetes

`cmd/a2a` ([ADR 0009](docs/adr/0009-a2a-inbound-adapter.md)) is the optional third binary: a read-only A2A agent (Agent2Agent, JSON-RPC 2.0, Agent Card at `/.well-known/agent-card.json`, port 8093) with two goal-shaped skills, `seller_compliance` and `inbound_status`, over the same reads; Postgres only, `A2A_AGENT_URL` is mandatory (absolute http(s) URL the card advertises), `a2a.enabled` in the chart (default off), unauthenticated like REST and MCP.

## Operator micro-frontend (`web/`)

[ADR 0007](docs/adr/0007-console-web-remote.md). `web/` is the `seller_inbound_mfe` Module Federation remote the warehouse-console shell mounts under `/seller-inbound/*` (federation name `seller_inbound_mfe`, exposes `./App`, dev/preview port 4022, build base `/mfes/seller-inbound/`). Two read-only lookup screens — the seller compliance scorecard (7d/30d window, tier, discrepancies by kind) and the inbound plan browser (state, facts, lines) — over this context's own GET routes; `apiOrigin` comes from the shell's `/config.json` at runtime (`SELLER_INBOUND_API_BASE` = `<apiOrigin>/api/seller-inbound`; standalone dev uses `http://localhost:8000`). The image is the two-stage uikit named-context build:

```sh
cd web && npm ci && npm run lint && npx tsc -b && npm test && npm run build
docker build --build-context uikit=../../warehouse-ui-kit -t ghcr.io/iqvo/seller-inbound-frontend:local .
```

Deployed, the remote is served by its own nginx pod behind the web gateway at `http://localhost/mfes/seller-inbound/`, while Kong serves the API at `http://localhost:8000/api/seller-inbound/`; neither proxies to the other (`frontend.enabled=true` in the chart).

## Local development / quality gate

`make check` (format, vet, lint, unit, handler and godog scenarios), `make check-fast`, `make check-all`, `make coverage`, `make mutation`, `make mutation-full`, `make integration` (testcontainers Postgres and Kafka, needs Docker), `make arch-test`, `make vuln`, `make guide-lint`, `make harness-test`; `make help` lists everything. lefthook wires the same targets as hooks (`lefthook install` once per clone): pre-commit runs `make fmt-check`, `make vet`, `make lint`; pre-push runs `make check`. `integration` and `mutation` are deliberately excluded from the hooks so committing and pushing stay fast and offline-friendly.

## Tests

- **Unit**: `make test` — domain, use cases, handlers, adapters.
- **BDD**: `make bdd` — godog scenarios in `features/` (8 feature files).
- **Integration**: `make integration` — testcontainers Postgres + Kafka.
- **Mutation**: `make mutation` (blocking subset) / `make mutation-full` — gremlins, config in `.gremlins.yaml`.
- **Architecture fitness**: `make arch-test` — `internal/architecture/` (hexagonal rule, CloudEvents-only, catalogue, Kafka rules, read-only MCP/A2A).
- **Web**: lint/typecheck/test/build of `web/` against `warehouse-ui-kit`.

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`, `test`, `bdd`, `integration`, `mutation-fast`, `api-lint`, `vuln`, `helm-lint`, `chart-tests`, `arch-test` and `web` on every push and PR; the exhaustive `mutation` job and the advisory `drift` sensor (dead code, `go mod tidy -diff`, unused frontend exports, mutation-vs-coverage quality) run on schedule; `docker-publish` pushes the image and `release` tags and publishes the chart.

## Architecture Decision Records

[docs/adr/](docs/adr/index.md) — index plus 9 ADRs (0001–0009: bounded context, aggregates and invariants, consumed contracts and local read models, CloudEvents envelope and type catalogue, seller compliance scorecard, read-only MCP server, console web remote, fleet observability standard, A2A inbound adapter).

## License

[MIT](LICENSE).
