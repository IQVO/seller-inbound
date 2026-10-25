# ADR 0006: a read-only MCP server

## Status

Accepted (2026-10-25).

## Context

Every other bounded context of the fleet that exposes an HTTP API also ships an
MCP server so that agents (warehouse-ops-agent first) can read its data without
a bespoke REST client. seller-inbound had none — the WMS parity surface every
other network context already has. The service already has the read queries
behind its REST adapter (`GetSellerScorecard`, `GetInboundPlan`, `GetLabels`,
`GetInboundShipment` — the four GET operations of apis/openapi.yaml), so an MCP
adapter is another driving adapter over them, not new domain.

## Decision

- **A second driving adapter, a separate binary.** `internal/adapters/inbound/mcp`
  is built on the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`
  v1.8.0) and served over Streamable HTTP only (no stdio, no SSE) by `cmd/mcp`,
  mounted at `/mcp` and `/` with an open `/healthz`. It is a second deployable
  beside `cmd/api`; the Helm chart gets an mcp Deployment and Service (default
  off) and an optional gateway HTTPRoute at `/api/mcp/seller-inbound`.
- **Four read tools over the existing queries, nothing else — one per read GET
  of apis/openapi.yaml.** `get_seller_scorecard(sellerId, window?)`,
  `get_inbound_plan(planId)`, `get_shipment_labels(shipmentId)` and
  `get_inbound_shipment(shipmentId)`. Arguments and results are lowerCamelCase
  like the REST contract; typed errors carry the REST problem slug
  (`not-found`, `invalid-window`, `validation-failed`) as the prefix of the
  tool error. Onboarding, suspending and reinstating sellers, the plan
  lifecycle, packing cartons, labelling and shipping stay REST-only.
- **Read-only is structural.** The adapter holds `Reads`, an interface of
  exactly the four read use cases the REST handlers call, and nothing that can
  write; `cmd/mcp` wires only the read repositories into the use cases — no
  unit of work, no outbox, no inbox, no SSCC issuer — and never dials Kafka
  (no KAFKA_BROKERS, no consumer groups): the relay and the three consumers
  run in `cmd/api` only. `TestToolSurface` pins the exact tool set (budget 4),
  the read-only annotations and the absence of write verbs in tool names;
  `TestWriteSeamSensorFailsOnWriteSeams` fails the build if the adapter ever
  references a write use case or a transactional seam by name (importing
  `ports` stays legal: the read error sentinel lives there);
  `TestToolRegistryGolden` pins the whole advertised registry; the
  integration test proves the binary reads what cmd/api's use cases wrote in
  a real Postgres and adds no outbox row. The optional `window` input carries
  `,omitempty` so the SDK publishes it as optional
  (`internal/architecture/mcp_optional_inputs_test.go`).
- **No sibling calls, no auth.** The server reads this context's own tables
  only, calls no other context over REST or MCP, and has no auth layer (the
  fleet-wide revert of 2026-09-11). Access control is the in-cluster ClusterIP
  boundary.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode); the binary runs
  the idempotent embedded migrations on start (with `MIGRATIONS_DATABASE_URL`
  for a direct DSN), wrapped in the same `bootretry` budget as `cmd/api`, so
  it can start against a fresh database before `cmd/api` has. `MCP_ADDR`
  (default `:8090`), `LOG_LEVEL`, `SERVICE_VERSION` and `ENVIRONMENT` complete
  the environment.

## Consequences

- Agents can answer "what is this seller's compliance tier", "what does this
  plan declare", "which cartons is this shipment carrying and what does each
  label say" without REST glue.
- Adding a tool is a reviewed act: the tool budget, the curated list and the
  golden file all change together (an amendment of this ADR).
- Writes stay REST operations with the `Idempotency-Key` middleware and the
  transactional outbox; the MCP surface cannot reach them at all.
