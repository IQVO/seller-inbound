---
name: how-to-add-a-rest-endpoint
description: Add or change a seller API endpoint in this service in the hexagonal order (domain invariant, use case, handler, apis/openapi.yaml, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the invariant it relies on
validates nothing and gets bypassed.

The worked example is the write `POST /inbound-shipments/{id}/cartons`:
`internal/domain/inboundshipment/carton.go` `AddCarton` ->
`internal/application/usecases/shipments.go` `AddCarton` ->
`internal/adapters/inbound/http/handlers.go` `handleAddCarton`. The read-side example is
`GET /inbound-shipments/{id}/labels` (`shipments.go` `GetLabels` -> `handleGetLabels`).

## 1. Domain first

Does the rule already exist in `internal/domain/<seller|inboundplan|inboundshipment|network>/`? A handler
holds no business logic. A new rule goes into the aggregate with a table-driven test in the same
package and a named `Err...` variable. Pure Go: no I/O, `now` and ids passed in. Then re-run the
mutation gate for that package (`gremlins unleash ./internal/domain/<pkg>`).

## 2. Application: the use case

Add a method to `Service` in `internal/application/usecases/` (`sellers.go`, `plans.go`,
`shipments.go`). A command loads the aggregate, calls the domain, saves it and appends its events
inside ONE `ports.UnitOfWork`; a transition the state forbids is `illegal-transition` (ADR 0002 A3). A seller-scoped write loads the seller and lets the aggregate refuse a
suspended one. The body type goes in `internal/application/requests/requests.go`; the answer is a view
from `internal/application/views`. If a new repository method is needed, add it to
`internal/application/ports/ports.go` (interfaces only), implement it in
`internal/adapters/outbound/postgres/` AND `internal/adapters/outbound/memory/`, and cover the
Postgres side in `repositories_integration_test.go`. Unit-test the use case in
`internal/application/usecases/*_test.go`.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- add the handler to `handlers.go` (decode with `decodeJSON`, call the use case, answer with
  `created(...)` or `respond(...)`);
- register the route in `server.go` `NewRouter`: a write goes through `write(...)` so it sits behind
  the Idempotency-Key middleware, a read uses `r.Get`;
- map a new domain `Err` in `errors.go` `problemCatalogue` (kebab-case slug, the status the OpenAPI
  documents) and add the case to `handlers_test.go`.

## 4. Contract: apis/openapi.yaml

Describe the operation, its `Idempotency-Key` parameter (writes), the success body and every problem it
can return, then lint:
`npx --yes @stoplight/spectral-cli lint apis/openapi.yaml --ruleset .spectral.yaml`
(the `api-lint` CI job runs the same). Keep `.claude/rules/rest-api.md` and the README in step.

## 5. Behaviour

Add the happy path and each refusal to `features/*.feature` (runner `features/features_test.go`, steps
`features/steps_test.go`; the scenarios drive the real router and consumers over the memory
doubles and name refusals by problem slug). The real middleware and Postgres are proven by
`cmd/api/e2e_integration_test.go`.

## 6. Verify before opening the PR

`make check`, `make coverage`, `go test -tags=integration ./cmd/... -race -count=1`, and the spectral
lint above.
