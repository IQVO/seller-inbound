---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Route -> use case, kept in sync with `apis/openapi.yaml`; routes are registered in
`internal/adapters/inbound/http/server.go` (`NewRouter`), handlers are in `handlers.go`, and
`cmd/api/main.go` wires the `Server`. The seller is named in the request body and checked
against its account (no authentication, consistent with the fleet):

- `POST /sellers` -> `OnboardSeller` (201)
- `POST /sellers/{id}:suspend` -> `SuspendSeller`; `POST /sellers/{id}:reinstate` -> `ReinstateSeller`
- `POST /inbound-plans` -> `CreateInboundPlan` (201); `GET /inbound-plans/{planId}` -> `GetInboundPlan`
- `POST /inbound-plans/{planId}:confirm` -> `ConfirmInboundPlan`; `:cancel` -> `CancelInboundPlan`
- `POST /inbound-plans/{planId}/shipments` -> `CreateInboundShipment` (201)
- `POST /inbound-shipments/{id}/cartons` -> `AddCarton` (201, carton + label data + `ssccIssued`)
- `POST /inbound-shipments/{id}:label` -> `LabelShipment`; `:ship` -> `ShipInboundShipment`
- `GET /inbound-shipments/{id}` -> `GetInboundShipment`; `GET /inbound-shipments/{id}/labels` -> `GetLabels`
  (chi cannot route a `:verb` suffix inside one segment, so the actions of a resource share
  `/sellers/{sellerAction}`, `/inbound-plans/{planAction}` and `/inbound-shipments/{shipmentAction}`
  and the handler splits the suffix)
- `GET /healthz` (liveness), `GET /readyz` (drain gate, Postgres ping, Kafka metadata): not business operations.

## Conventions

- **Errors** are RFC 9457 `application/problem+json`: `type` is a kebab-case slug from
  `problemCatalogue` in `internal/adapters/inbound/http/errors.go` (shared: `not-found` 404,
  `illegal-transition` 409, `concurrent-update` 409, `validation-failed` 422; one slug per model
  `Err...`, e.g. `case-qty-mismatches-gtin`, `sscc-not-unique`, `seller-suspended`), plus `title`,
  `status`, `detail`, `instance`. An unmapped error is an opaque 500 `internal-error`. A new
  `Err` needs a catalogue row.
- **Idempotency-Key** is required on every write (`idempotency.go`): same key + same request
  replays the first response, same key + other request is 422 `idempotency-key-reused`, a missing
  key is 422 `idempotency-key-required`; keys live 24 h in `idempotency_keys`. A new write
  registers through the `write(...)` wrapper in `NewRouter`, never bare `r.Post`.
- **NUL** (U+0000) is rejected before anything else (`nul.go`): 404 in the path, 400
  `malformed-request` in the query or body.
- Bodies are decoded strictly (`decodeJSON`, `response.go`): unknown fields are refused
  (`validation-failed`).
- Every GTIN and SSCC in `apis/openapi.yaml` and `apis/asyncapi.yaml` examples must carry a correct
  GS1 check digit.
- **Handlers hold no business logic**: decode, call a use case, encode the view.
- **Auth**: none. REST is unauthenticated, as in the rest of the network
  (`internal/architecture/fitness_test.go` `TestNoAuthMiddlewareReintroduced` keeps it so).
- Tests: `handlers_test.go` runs the real router over the memory doubles; the real middleware and
  Postgres are proven by `cmd/api/e2e_integration_test.go`; behaviour by `features/*.feature`.
