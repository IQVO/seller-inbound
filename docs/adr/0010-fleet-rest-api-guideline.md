# ADR 0010: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01).

## Context

The fleet REST API guideline (warehouse-docs, "Warehouse REST API Guideline")
is mandatory for every `apis/openapi.yaml`: URLs name things and never actions,
every `POST` and `PATCH` is idempotent, key reuse is `409`, and every
collection is a keyset-paged `{items, nextCursor}` envelope. It is enforced by
the `rest-guideline` Spectral rule (R1-R8) in the `api-lint` job. seller-inbound
still had `:verb` custom methods, answered `Idempotency-Key` problems with
`422`, and scoped the key to its route.

## Decision

- **Noun routes (R1, R2).** The six `:verb` commands become transition
  resources (guideline 1.3d / 1.4); `operationId` is unchanged so MCP and A2A
  mappings do not move, and status codes and bodies do not change:

  | Old | New |
  | --- | --- |
  | `POST /sellers/{sellerId}:suspend` | `POST /sellers/{sellerId}/suspensions` |
  | `POST /sellers/{sellerId}:reinstate` | `POST /sellers/{sellerId}/reinstatements` |
  | `POST /inbound-plans/{planId}:confirm` | `POST /inbound-plans/{planId}/confirmations` |
  | `POST /inbound-plans/{planId}:cancel` | `POST /inbound-plans/{planId}/cancellations` |
  | `POST /inbound-shipments/{shipmentId}:label` | `POST /inbound-shipments/{shipmentId}/labels` |
  | `POST /inbound-shipments/{shipmentId}:ship` | `POST /inbound-shipments/{shipmentId}/dispatches` |

  The old routes are gone (no alias); `TestRetiredRoutesAreGone` pins 404/405.
  The `{sellerAction}` / `{planAction}` / `{shipmentAction}` dispatch on a
  `:verb` suffix is deleted; each route has its own handler.
- **Idempotency (R3-R6, R8).** Every `POST` was already behind
  `RequireIdempotencyKey`, whose key row commits in the SAME transaction as the
  aggregate and its outbox rows (the use case joins it through `TxBinder`). The
  change is the contract: the key is **global** (migration `0003` narrows the
  primary key from `(route, key)` to `(key)`, keeping the earliest row of any
  key stored under two routes); the fingerprint is method + path (the `route`
  column) + SHA-256 of the body; a different fingerprint is
  `409 idempotency-key-reused` (was `422`); a missing key is
  `400 idempotency-key-required`, a key that is not 1 to 128 printable ASCII
  characters is `400 idempotency-key-invalid`; a replay carries
  `Idempotency-Replayed: true` (the first execution never does). 2xx and 4xx are
  stored and replayed, a 5xx or panic rolls back and is never stored.
- **Pagination (R7).** seller-inbound has no list endpoint; the one flagged
  GET is exempt (below). No `page` package or `unknown-query-parameter` check is
  needed because no collection takes `limit`/`cursor`.

## Exemptions

- `GET /inbound-shipments/{shipmentId}/labels` is `x-pagination-exempt: view`:
  it is one composite resource, the label sheet of a single shipment (header
  `shipmentId`, `state`, `destinationNodeId` plus the labels derived from the
  frozen manifest), printed as one unit and bounded by the manifest of one
  shipment. It is not a collection of independently addressable rows.

## Consumers

Inside this repo nothing calls the renamed routes over HTTP (the console remote
is read-only; MCP and A2A call use cases directly). Tests, BDD features,
README and the docs pages use the new routes. Outside: the `e2e-tests` repo
(`seller_inbound_test.go`) changes in its own PR.

## Consequences

The cutover is breaking by design and deploys together with the fleet REST
cutover set. Clients must send `Idempotency-Key` on every `POST` (already
required here), treat `409 idempotency-key-reused` as a client bug, and read
`Idempotency-Replayed`.
