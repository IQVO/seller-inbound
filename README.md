# seller-inbound

Retail network, bounded context **Seller Inbound** (Supporting, partner tier).

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

`cmd/api` needs Postgres and Kafka; there is no in-memory mode. Mandatory environment: `DATABASE_URL`, `KAFKA_BROKERS`, `NODE_CONSUMER_GROUP`, `CATALOG_CONSUMER_GROUP`, `RECEIPT_CONSUMER_GROUP`, `SSCC_COMPANY_PREFIX` (6 to 11 digits). A missing one is a boot error naming it. Deploy with `charts/seller-inbound`.

## Develop

`make check` (format, vet, lint, unit, handler and godog scenarios), `make coverage`, `make mutation`, `make integration` (testcontainers Postgres and Kafka, needs Docker).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
