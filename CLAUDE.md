# seller-inbound

harness-template: v4

Retail network, bounded context Seller Inbound (Supporting, partner tier). Owns Seller, InboundPlan, InboundShipment and the SellerComplianceScorecard; consumes offer-availability and node-topology events into local read models (ADR 0003) and publishes seller lifecycle events.
Design source of truth: IQVO/warehouse-docs `network/model/model.yaml` (context seller-inbound). This service belongs to the retail network, not the warehouse-systems fleet.
This project is a domain-driven design study, not associated with any company: never name a real company, brand or product in code, comments, ADRs, specs, tests or fixtures (README `## Disclaimer`).

## Commands
`make check` (fmt, vet, build, lint, unit + handler + BDD; the pre-push gate), `make coverage` (scope + threshold live only in the Makefile), `make mutation` (gremlins per internal/domain/<pkg>; thresholds measured, never guessed), `make arch-test`, `make integration` (testcontainers Postgres + Kafka, needs Docker; build tag `integration`), `make vuln`. Lint: `golangci-lint run --allow-parallel-runners ./...`.
Guides: `python3 scripts/harness/repo_lint.py`, `python3 scripts/harness/guide_lint.py` must stay clean. npm runs ONLY inside web/.

## Layout (hexagonal)
`internal/domain/{shared,gs1,seller,inboundplan,inboundshipment,scorecard,network}` pure Go; `internal/application/{ports,requests,views,usecases,outbox}`;
`internal/adapters/inbound/{http (chi),kafka,mcp (read-only)}`; `internal/adapters/outbound/{postgres,kafka,outbox,sscc,clock,ids,memory}`;
`cmd/api` (composition root) and `cmd/mcp` (read-only MCP server, :8090); `features/` (godog, never a .go file at the repo root); migrations embedded in the postgres adapter.
`web/` is the console MFE remote (`seller_inbound_mfe`, vite + module federation, strictPort dev 4022, PUBLIC_BASE /mfes/seller-inbound/).

## Runtime rule
Real PostgreSQL and Kafka only. `DATABASE_URL` and `KAFKA_BROKERS` are mandatory (boot error naming the variable). `/healthz` = liveness; `/readyz` = drain gate + dependency checks. No authentication on REST or MCP (the in-cluster ClusterIP boundary is the access control). See docs/docs/overview/runtime.md.

## REST and MCP
chi router implementing every operation of apis/openapi.yaml (14 operations) plus /healthz, /readyz. Every state-changing route takes `Idempotency-Key`. Errors are RFC 9457 problem+json with kebab-case `type` slugs.
The MCP server (cmd/mcp, ADR 0006) exposes four read-only tools — one per REST GET (get_seller_scorecard with an optional window, get_inbound_plan, get_shipment_labels, get_inbound_shipment) — bound to the same read use cases; reads add zero outbox rows (testcontainers-proven), and the adapter touches no write seam (UnitOfWork/EventOutbox/Inbox).

## Events: CloudEvents 1.0 is MANDATORY
Envelope only via internal/adapters/kafka/cloudevents. Type catalogue in ADR 0004; subject and Kafka key = aggregate id. Transactional outbox: aggregates + outbox rows in ONE Postgres transaction. A new event: domain struct + apis/asyncapi.yaml + ADR 0004 `type:` line in the SAME commit.

## Domain
Aggregates, invariants and assumptions in docs/adr/0002; consumed contracts in 0003; the compliance scorecard in 0005. GS1 carton/SSCC identification via internal/domain/shared/gs1.

## Workflow
GitFlow: feature/* -> PR into develop, CI green before merge, never push develop or main. lefthook: pre-commit fmt/vet/lint, pre-push `make check`. Commit and push after each step.
Helm chart: charts/seller-inbound (mcp and frontend both default OFF — ADRs 0006/0007; ci/all-components-values.yaml renders everything on; tests in charts/seller-inbound/tests: render_test.sh + test_service_selectors.py + the python suites, run by the chart-tests job). The web remote ships its own image (ghcr.io/iqvo/seller-inbound-frontend), not the api Dockerfile.

A2A: `cmd/a2a` (port 8093) is this context's read-only Agent2Agent agent, skills `seller_compliance` (read), `inbound_status` (read), decided in `docs/adr/0009-a2a-inbound-adapter.md`; delegation only (facts stay CloudEvents on Kafka), unauthenticated like REST and MCP; rules in `.claude/rules/fleet/a2a.md`.
