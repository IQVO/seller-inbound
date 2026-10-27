# seller-inbound

Retail network, bounded context **Seller Inbound** (Supporting, partner tier).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

PROPOSED. The seller-facing front door for stock coming into the network: a seller is onboarded, plans an inbound shipment to a vendor node in eaches (loose or case-packed), packs it into cartons each identified by an SSCC and a printed label, and ships it. The context checks the plan against the destination node's declared inbound limits and the catalogue item's packaging hierarchy, hands the vendor the shipment as a carton manifest through the Vendor API, and closes the shipment from the vendor's carton-level receipt, recording every discrepancy and compliance violation instead of correcting it. It absorbs the idea reserved earlier as the inventory-placement context; automated placement advice is not modelled (see the gap register).

Status: implemented, merged and deployed (the packaging checklist is `docs/before-first-release.md`). The design is proposed in
[retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound); `network/model/model.yaml` in IQVO/warehouse-docs is the single source of truth.
This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.partner.seller-inbound.<entity>.<Event>`), transactional outbox.

## What it does

- **Sellers** are onboarded, suspended and reinstated (`POST /sellers`, `/sellers/{id}:suspend|:reinstate`).
- An **inbound plan** (`POST /inbound-plans`, `:confirm`, `:cancel`) declares, in eaches, what a seller will send to one destination node (`INDIVIDUAL` or `CASE_PACKED`).
- An **inbound shipment** (`POST /inbound-plans/{planId}/shipments`) takes **cartons** (`POST /inbound-shipments/{id}/cartons`), each identified by an SSCC: a seller-supplied one is accepted only if valid (18 digits, GS1 mod-10) and unique network-wide, otherwise the network issues one from its own GS1 company prefix.
- `:label` freezes the manifest once the cartons add up to the plan; `GET /inbound-shipments/{id}/labels` returns the label data per carton (SSCC, GS1-128 element string `(00)<sscc>`, human readable line, contents in eaches, destination node); `:ship` ships.
- The vendor's carton-level receipt (an event from vendor-connectivity) closes the shipment; every discrepancy and compliance violation is recorded, never corrected.
- **`GET /sellers/{id}/scorecard?window=7d|30d`** answers a seller's rolling-window scorecard from the facts the service recorded (shipments closed, receipt accuracy, discrepancies by kind, compliance violations) and a derived `complianceTier` (`GOOD`, `WATCH`, `AT_RISK`). It is **informational**: it suspends and blocks nothing, suspension stays a human command, and it publishes no event (ADR 0005).

Contracts: `apis/openapi.yaml` (REST), `apis/asyncapi.yaml` (nine published and eight consumed events). Decisions: `docs/adr/` (index in `docs/adr/index.md`). Operations: `docs/runtime.md`, `docs/operations/runbook.md`.

## Run

`cmd/api` needs Postgres and Kafka; there is no in-memory mode. Mandatory environment: `DATABASE_URL`, `KAFKA_BROKERS`, `NODE_CONSUMER_GROUP`, `CATALOG_CONSUMER_GROUP`, `RECEIPT_CONSUMER_GROUP`, `SSCC_COMPANY_PREFIX` (6 to 11 digits). A missing one is a boot error naming it. Deploy with `charts/seller-inbound`. `cmd/mcp` ([ADR 0006](docs/adr/0006-read-only-mcp-server.md)) is the optional second binary: the read-only MCP server (`get_seller_scorecard`, `get_inbound_plan`, `get_shipment_labels`, `get_inbound_shipment`) over the same reads as the REST GETs, Postgres only, `mcp.enabled` in the chart (default off). `frontend.enabled=true` adds the web remote's nginx Deployment + ClusterIP Service; it needs no database and no Kafka, and this chart never routes it (the web gateway owns `/mfes/seller-inbound/`).

## Web remote

[ADR 0007](docs/adr/0007-console-web-remote.md). `web/` is the `seller_inbound_mfe` Module Federation remote the warehouse-console shell mounts under `/seller-inbound/*` (federation name `seller_inbound_mfe`, exposes `./App`, dev/preview port 4022, build base `/mfes/seller-inbound/`). Two read-only lookup screens — the seller compliance scorecard (7d/30d window, tier, discrepancies by kind) and the inbound plan browser (state, facts, lines) — over this context's own GET routes; `apiOrigin` comes from the shell's `/config.json` at runtime. The image is the two-stage uikit named-context build (`docker build --build-context uikit=../../warehouse-ui-kit -t ghcr.io/iqvo/seller-inbound-frontend:local .` from `web/`).

```sh
cd web && npm ci && npm run lint && npx tsc -b && npm test && npm run build
```

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0008](docs/adr/0008-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id`
and a `request_id`; `cmd/api` and `cmd/mcp` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`;
export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter
`seller_inbound.shipment.shipped` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by
the consumers. The chart's `otel.*` values render the env.

## Develop

`make check` (format, vet, lint, unit, handler and godog scenarios), `make coverage`, `make mutation`, `make integration` (testcontainers Postgres and Kafka, needs Docker).

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
