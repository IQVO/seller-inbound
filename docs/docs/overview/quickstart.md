---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: "Run seller-inbound locally against Postgres and Kafka and walk a shipment from onboarding to ship."
---

# Quickstart

This page gets `cmd/api` running on your machine and walks one shipment through the seller API. The commands and
payloads come from the code and `apis/openapi.yaml`; **no run output is pasted here** because this page was written
from the source, not from a captured session. Where a response is shown it is the documented example from the OpenAPI
contract.

## Prerequisites

- Go (version in `go.mod`), `make`, `curl`.
- A reachable PostgreSQL. The service owns its own database and runs its embedded migrations at boot.
- A reachable Kafka broker. There is **one** broker platform-wide; from the kind cluster it is reachable at
  `localhost:9092`. The docker-compose Kafka is retired, do not start another.
- Docker only for `make integration` (testcontainers boots its own Postgres and Kafka there).
- `golangci-lint` (pinned `v2.14.0`) for `make lint`; `lefthook install` once per clone wires the git hooks.

There is no in-memory mode: `cmd/api` refuses to boot without `DATABASE_URL`, `KAFKA_BROKERS`, the three consumer
groups and `SSCC_COMPANY_PREFIX` ([Configuration](/docs/operations/configuration)).

## Make targets

| Target | What it runs |
| --- | --- |
| `make build` | `go build ./...` |
| `make test` | `go test ./... -race` (unit, handler and godog scenarios, no database) |
| `make check` | `fmt-check vet build lint test` (the pre-push hook) |
| `make check-all` | `check` plus `coverage arch-test bdd` |
| `make coverage` | tests with the 90% gate over `internal/domain` and `internal/application` |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | `go test ./internal/architecture/... -v` |
| `make integration` | `go test -tags=integration ./... -race -count=1` (Docker) |
| `make mutation` / `make mutation-full` | gremlins on `internal/domain/inboundshipment` / on `internal/domain` |
| `make vuln` | `govulncheck ./...` |

See [Testing](/docs/development/testing) for the full pyramid.

## Start the API

```sh
export DATABASE_URL="postgres://user:password@localhost:5432/seller_inbound?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export NODE_CONSUMER_GROUP=seller-inbound-node
export CATALOG_CONSUMER_GROUP=seller-inbound-catalog
export RECEIPT_CONSUMER_GROUP=seller-inbound-receipt
export SSCC_COMPANY_PREFIX=0614141        # the network's GS1 company prefix, 6 to 11 digits
go run ./cmd/api                          # migrations run at boot; REST on :8080
```

Check the probes:

```sh
curl -s localhost:8080/healthz    # liveness
curl -s localhost:8080/readyz     # 200 only while Postgres and a Kafka broker answer
```

The read-only siblings need only the database:

```sh
DATABASE_URL="$DATABASE_URL" go run ./cmd/mcp    # MCP on :8090, path /mcp
DATABASE_URL="$DATABASE_URL" A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a   # A2A on :8093
```

## Seed a destination node

A plan can only target a node that the local `node_inbound_views` read model knows as `ACTIVE` with an INBOUND type
(`FULFILLMENT_CENTER` or `INBOUND_CROSS_DOCK`, `internal/domain/network/network.go`). In a real environment the
node consumer fills that table from node-topology's `NodeRegistered` and `NodeActivated` events. For a local walk
without node-topology, insert the row yourself:

```sh
psql "$DATABASE_URL" -c "INSERT INTO node_inbound_views (node_id, node_type, status) VALUES ('node-0f3a91', 'FULFILLMENT_CENTER', 'ACTIVE')"
```

## Walk a shipment

Every `POST` needs an `Idempotency-Key` header (1 to 128 characters). All calls below go to the service directly
(`localhost:8080`, routes at the root). The OpenAPI server URL is `http://localhost:8000/api/retail-network/seller-inbound`,
but the route in `warehouse-infra` (`helm-values/seller-inbound.yaml`) is `/api/seller-inbound` with the prefix stripped,
so through Kong on `:8000` the same calls go to `http://localhost:8000/api/seller-inbound/...`.

1. Onboard a seller:

   ```sh
   curl -s -X POST localhost:8080/sellers -H 'Content-Type: application/json' -H 'Idempotency-Key: k-1' \
     -d '{"sellerId":"seller-77","name":"Acme Goods"}'
   ```

   Documented response (`201`): `{"sellerId":"seller-77","name":"Acme Goods","state":"ACTIVE","onboardedAt":"..."}`.

2. Declare an individually packed plan and confirm it:

   ```sh
   curl -s -X POST localhost:8080/inbound-plans -H 'Content-Type: application/json' -H 'Idempotency-Key: k-2' \
     -d '{"sellerId":"seller-77","destinationNodeId":"node-0f3a91","packingOption":"INDIVIDUAL","lines":[{"catalogItemId":"item-004512","gtin":"00614141000036","qtyEaches":12}]}'
   curl -s -X POST 'localhost:8080/inbound-plans/<planId>:confirm' -H 'Idempotency-Key: k-3'
   ```

   Replace `<planId>` with the `planId` of the first response (`pln-...`).

3. Open a shipment, add a carton that matches the plan exactly, label and ship:

   ```sh
   curl -s -X POST localhost:8080/inbound-plans/<planId>/shipments -H 'Idempotency-Key: k-4'
   curl -s -X POST localhost:8080/inbound-shipments/<shipmentId>/cartons -H 'Content-Type: application/json' -H 'Idempotency-Key: k-5' \
     -d '{"kind":"MIXED","contents":[{"catalogItemId":"item-004512","gtin":"00614141000036","qtyEaches":12}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}'
   curl -s -X POST 'localhost:8080/inbound-shipments/<shipmentId>:label' -H 'Idempotency-Key: k-6'
   curl -s localhost:8080/inbound-shipments/<shipmentId>/labels
   curl -s -X POST 'localhost:8080/inbound-shipments/<shipmentId>:ship' -H 'Idempotency-Key: k-7'
   ```

   The carton response carries the SSCC the network issued (`ssccIssued: true`) and its label data. For a single-shipment
   plan, `:label` fails with `carton-contents-mismatch-plan` unless the cartons add up to the plan's eaches.

4. Read the scorecard (a pure read):

   ```sh
   curl -s 'localhost:8080/sellers/seller-77/scorecard?window=30d'
   ```

   With no closed shipment the rates are omitted and `complianceTier` is `GOOD`.

`:ship` appends `InboundShipmentShipped` to the outbox; the relay publishes it to `retailnetwork.seller-inbound.events`
within `OUTBOX_RELAY_INTERVAL`. The shipment closes only when vendor-connectivity publishes `InboundReceiptSubmitted`
for it ([Integration](/docs/ecosystem/integration)).

## Where to go next

- [Architecture](/docs/overview/architecture) for the layout of `cmd/` and `internal/`.
- [Runbook](/docs/operations/runbook) and [Troubleshooting](/docs/operations/troubleshooting) when something does not boot.
- [API reference](/docs/api-reference) for every route and event.
