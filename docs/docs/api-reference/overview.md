---
id: overview
title: API overview
slug: /api-reference
sidebar_position: 1
description: "The interfaces of seller-inbound (REST, events, MCP, A2A), the endpoint table and the conventions of the contracts."
---

# API overview

:::info Implemented by cmd/api
`cmd/api` serves every route below (`internal/adapters/inbound/http`), on `HTTP_ADDR` (default `:8080`). The contracts
are `apis/openapi.yaml` and `apis/asyncapi.yaml`. There is no route in the code that the spec does not list: the router
registers exactly the 14 API operations plus `/healthz` and `/readyz`. See the [Quickstart](/docs/overview/quickstart)
for a walk through the seller API.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 1.0.0) | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. The spec names the server `http://localhost:8000/api/retail-network/seller-inbound`; the gateway route in `warehouse-infra` is `/api/seller-inbound` (prefix stripped), and `cmd/api` itself serves the routes at the root of `:8080`. |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Nine published and eight consumed event types; summarised on the [event catalogue](/docs/api-reference/events), every field on [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0006](/docs/adr/0006-read-only-mcp-server), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` (port 8090; through Kong `/api/mcp/seller-inbound`). Four read-only tools, no write tool: `get_inbound_plan`, `get_inbound_shipment`, `get_shipment_labels`, `get_seller_scorecard`. See [MCP tools](/docs/mcp/tools). No auth, by fleet decision. |
| A2A | [ADR 0009](/docs/adr/0009-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/seller-inbound`), the Agent Card at `/.well-known/agent-card.json`. Two read-only skills, no write skill: `seller_compliance` (`sellerId`, optional `window` `7d` or `30d`) and `inbound_status` (exactly one of `planId` or `shipmentId`). No auth, by fleet decision. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| Sellers | `POST /sellers` | `onboardSeller` | `201`, the account, `ACTIVE` |
| Sellers | `POST /sellers/{sellerId}:suspend` | `suspendSeller` | `200`, the account (a repeat is a no-op, no event) |
| Sellers | `POST /sellers/{sellerId}:reinstate` | `reinstateSeller` | `200`, the account (a repeat is a no-op, no event) |
| Sellers | `GET /sellers/{sellerId}/scorecard` | `getSellerScorecard` | `200`, the scorecard; `window` `7d` or `30d` (default `30d`) |
| Inbound Plans | `POST /inbound-plans` | `createInboundPlan` | `201`, the plan, `DRAFT` |
| Inbound Plans | `GET /inbound-plans/{planId}` | `getInboundPlan` | `200`, the plan with its lines |
| Inbound Plans | `POST /inbound-plans/{planId}:confirm` | `confirmInboundPlan` | `200`, `CONFIRMED` |
| Inbound Plans | `POST /inbound-plans/{planId}:cancel` | `cancelInboundPlan` | `200`, `CANCELLED` |
| Inbound Plans | `POST /inbound-plans/{planId}/shipments` | `createInboundShipment` | `201`, an empty `DRAFT` shipment |
| Inbound Shipments | `POST /inbound-shipments/{shipmentId}/cartons` | `addCarton` | `201`, the carton, `ssccIssued`, label data |
| Inbound Shipments | `POST /inbound-shipments/{shipmentId}:label` | `labelInboundShipment` | `200`, `LABELLED` |
| Inbound Shipments | `GET /inbound-shipments/{shipmentId}/labels` | `getShipmentLabels` | `200`, one label per carton |
| Inbound Shipments | `POST /inbound-shipments/{shipmentId}:ship` | `shipInboundShipment` | `200`, `SHIPPED` |
| Inbound Shipments | `GET /inbound-shipments/{shipmentId}` | `getInboundShipment` | `200`, the shipment with its cartons |
| Health | `GET /healthz`, `GET /readyz` | none (not in the spec) | `200`; `503` while draining or when Postgres or Kafka is unreachable |

The `:verb` actions share a chi pattern per resource (`/sellers/{sellerAction}`, `/inbound-plans/{planAction}`,
`/inbound-shipments/{shipmentAction}`) and are dispatched on the suffix; an unknown suffix is `404 not-found`
(`internal/adapters/inbound/http/server.go`).

## Conventions in the contract

- **Eaches.** Every quantity is `qtyEaches`; a bare quantity never changes meaning. GTINs are 14 digits (shorter forms
  are normalised), SSCCs 18, both with a GS1 mod-10 check digit.
- **`Idempotency-Key`.** Required on every `POST` (1 to 128 characters), implemented by
  `internal/adapters/inbound/http/idempotency.go`. The same key and route with an equal body replays the stored answer
  for 24 h (a 4xx replays too); a different body is `422 idempotency-key-reused`; a missing or too long key is
  `422 idempotency-key-required`. A 5xx is never stored.
- **Errors** are RFC 9457 `application/problem+json` with `type` = the bare slug (not a URL), `title`, `status`,
  `detail` and `instance`. The slugs are `not-found`, `illegal-transition`, `concurrent-update`, `validation-failed`,
  `invalid-window`, `seller-exists`, `seller-suspended`, `node-cannot-receive`, `sscc-not-unique`, `invalid-sscc`,
  `case-pack-not-uniform`, `case-qty-mismatches-gtin`, `carton-overweight`, `carton-oversize`,
  `carton-contents-mismatch-plan`, `shipment-not-labelled`, `idempotency-key-required`, `idempotency-key-reused`,
  `dependency-unavailable` and `internal-error`. The full status table is on
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).
- **Optimistic concurrency.** Aggregates carry a `version` that is not exposed on the wire; a lost race is
  `409 concurrent-update`, which the client resolves by re-fetching and retrying.
- **Omitted rates.** In the scorecard, a rate whose denominator is zero is omitted, never `0` or `NaN`.
- **Request bodies are strict.** Unknown fields, trailing data and an empty body are `422 validation-failed`; a body is
  limited to 4 MiB; a NUL character anywhere is a uniform client error.
- **Auth**: none. The fleet reverted REST and MCP auth, and `TestNoAuthMiddlewareReintroduced` fails CI if auth
  middleware returns under `internal/adapters/inbound`.
- **CloudEvents.** Every Kafka message is CloudEvents 1.0 in structured mode with `content-type:
  application/cloudevents+json; charset=UTF-8`; the Kafka key is the subject.

## A2A skill requests

A2A is the delegation channel ([ADR 0009](/docs/adr/0009-a2a-inbound-adapter)); facts still travel only as CloudEvents on
Kafka. A client sends a message whose first part is a data part (or a text part holding the same JSON):

```json
{"skill": "seller_compliance", "arguments": {"sellerId": "seller-77", "window": "7d"}}
```

```json
{"skill": "inbound_status", "arguments": {"shipmentId": "shp-0f3a91"}}
```

The reply is one agent message carrying a data part `{"skill", "data" | "problem"}` and a one-line text summary. Domain
and request problems are answered inside the envelope (`problem` with `type`, `title`, `detail`, `status`), never as a
JSON-RPC error: `malformed-skill-request`, `unknown-skill`, `invalid-argument` (all 400), `invalid-window` (400),
`not-found` (404), `validation-failed` (422) and `internal-error` (500). Unknown request fields are rejected rather than
ignored. The agent is message-only: no Task lifecycle, no streaming, and the Agent Card declares no security scheme.

Source: `internal/adapters/inbound/a2a/{server,skills,mapping}.go`, `cmd/a2a/router.go`.
