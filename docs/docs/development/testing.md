---
id: testing
title: Testing
sidebar_label: Testing
sidebar_position: 1
description: "The seller-inbound test pyramid: unit, handler and use-case tests, godog scenarios, integration with testcontainers, contract, mutation and architecture fitness tests, and the CI jobs."
---

# Testing

The pyramid as it stands on `develop`. Everything except `make integration` runs without Docker, Postgres or Kafka.

| Layer | Where | How many | Runs with |
| --- | --- | --- | --- |
| Unit (domain, application, adapters) | `internal/domain/**`, `internal/application/**`, `internal/adapters/**`, `internal/bootretry`, `cmd/*/main_test.go` | about 89 `_test.go` files, 12 of them `*_integration_test.go` | `make test` |
| HTTP handler tests (no database) | `internal/adapters/inbound/http/*_test.go` | readiness, NUL rejection, idempotency capture, scorecard, telemetry, handlers | `make test` |
| BDD acceptance (godog) | `features/*.feature` | **6 feature files, 47 scenarios** | `make bdd` (also part of `make test`) |
| Integration (testcontainers) | files tagged `integration`: `cmd/api/e2e_integration_test.go`, `cmd/mcp`, `cmd/a2a`, `internal/adapters/outbound/{postgres,outbox,kafka}`, `internal/adapters/inbound/kafka` | 12 `*_integration_test.go` files | `make integration` |
| Contract | event catalogue vs `apis/asyncapi.yaml`, MCP tool registry golden file, CloudEvents encoder/decoder, Spectral lint of both specs | see below | `make arch-test`, `make test`, CI `api-lint` |
| Mutation | `internal/domain/**` with gremlins | thresholds in `.gremlins.yaml` | `make mutation`, `make mutation-full` |
| Architecture fitness | `internal/architecture/` | 12 test files, 31 `Test*` functions | `make arch-test` |

## Unit and use-case tests

The domain packages (`inboundplan`, `inboundshipment`, `seller`, `scorecard`, `network`, `shared`, `shared/gs1`) have
table tests for every invariant; `internal/application/usecases` has `flow_test.go` (a shipment from onboarding to
receipt on in-memory test doubles), `consumers_test.go`, `scorecard_test.go` and `shipment_metrics_test.go`. The
in-memory store under `internal/adapters/outbound/memory` exists for tests only: a fitness test
(`TestRuntimeBinaryHasNoInMemoryAdapters`) fails if any `cmd/**` binary imports it.

`make coverage` runs the whole suite with the race detector and enforces a **90 % gate** over
`./internal/domain/...` and `./internal/application/...` (`COVERAGE_PKGS` and `COVERAGE_THRESHOLD` in the `Makefile`;
CI runs the same target, so the scope cannot drift).

## godog features

| File | Scenarios |
| --- | --- |
| `features/sellers.feature` | 9 |
| `features/inbound_plans.feature` | 9 |
| `features/shipments_and_labels.feature` | 13 |
| `features/receipts.feature` | 6 |
| `features/scorecard.feature` | 8 |
| `features/node_limits.feature` | 2 |
| **Total** | **47** |

The steps (`features/steps_test.go`, `features/features_test.go`) drive the real use cases and the real REST router over
in-memory adapters, and feed the consumers with CloudEvents built from the producers' contracts (node-topology,
offer-availability, vendor-connectivity). Run them alone with `make bdd` (`go test ./... -run TestFeatures -v`).

## Integration tests

`make integration` runs `go test -tags=integration ./... -race -count=1`. It needs only Docker: every test boots its
own Postgres and Kafka with testcontainers, so there is no `DATABASE_URL`, no docker compose and no skip gate. Two
fitness tests enforce this (`TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers`).

- `cmd/api/e2e_integration_test.go` starts the real `run` entry point against the containers and walks the REST API,
  the three consumers and the relay end to end.
- `cmd/mcp` and `cmd/a2a` have their own `main_integration_test.go`.
- `internal/adapters/outbound/postgres` covers the repositories, the scorecard tables, and that an outbox row written
  under a span stores its `traceparent`.
- `internal/adapters/outbound/outbox` covers the relay and the attempt cap; `outbound/kafka` the sink and writers;
  `inbound/kafka` the consumers and dead-lettering.

## Contract tests

- `TestEventCatalogueMatchesContract` (`internal/architecture/catalogue_fitness_test.go`) compares the event types the
  code emits and consumes with `apis/asyncapi.yaml`.
- `TestToolRegistryGolden` and `TestToolSurface` (`internal/adapters/inbound/mcp/governance_test.go`) pin the MCP tools
  to `internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`; refresh it with `go test ./internal/adapters/inbound/mcp -update`
  and review the diff. The same file checks that no tool name is a write verb.
- `internal/adapters/kafka/cloudevents/cloudevents_test.go` and `internal/adapters/outbound/kafka/encoder_test.go`
  pin the CloudEvents envelope.
- CI `api-lint` runs Spectral on `apis/openapi.yaml` (`.spectral.yaml`) and `apis/asyncapi.yaml`
  (`.spectral.asyncapi.yaml`) with `--fail-severity=warn`.

## Mutation testing

`.gremlins.yaml` sets one worker, timeout coefficient 30, and thresholds **efficacy 99** and **mutant-coverage 99**.
The file records the last measurement: every domain package except `inboundshipment` at 100 %; `inboundshipment` at
99.23 % with one equivalent surviving mutant (`receipt.go:195`, the receipt normalisation already drops zero-quantity
items). `make mutation` runs gremlins on `internal/domain/inboundshipment` (the fast subset); `make mutation-full` on all of `internal/domain`.
gremlins is pinned to v0.6.0.

## Architecture fitness tests

`make arch-test` runs `go test ./internal/architecture/... -v`. Each rule has a "sensor bites" companion that proves the
rule fails on a bad fixture.

| Rule | Test |
| --- | --- |
| Hexagonal dependency direction (`domain` imports nothing outward, `application` only domain, adapters do not import each other) | `TestHexagonalArchitecture` |
| MCP and A2A adapters are read-only and import no REST adapter or outbound client | `TestMCPAdapterDependencyRule`, `TestA2AAdapterDependencyRule`, `TestA2AAgentIsReadOnlyAndHasNoOutboundClient` |
| No authentication middleware reintroduced | `TestNoAuthMiddlewareReintroduced` |
| Runtime binaries never import the in-memory adapters | `TestRuntimeBinaryHasNoInMemoryAdapters` |
| No Go file at the repository root | `TestNoGoFilesAtRepoRoot` |
| CloudEvents only, no flat envelope or toggle; replay consumers set a commit interval | `TestNoEventEnvelopeToggleOrFlatEnvelope`, `TestCloudEventsOnly`, `TestReplayConsumersSetCommitInterval` |
| Event catalogue equals the AsyncAPI contract | `TestEventCatalogueMatchesContract` |
| Consumer group ids come from configuration, never a literal | `TestKafkaConsumerGroupNeverHardcodedInline` |
| Kafka writers set `RequiredAcks`; a writer and its message never both set a topic; dead-letter calls guard cancellation | `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice`, `TestDeadLetterCallsGuardCancellation` |
| Every outbox relay passes the attempt cap | `TestOutboxRelaysPassAttemptCap` |
| MCP optional inputs carry `omitempty` | `TestMCPOptionalToolInputsCarryOmitempty` |
| Integration tests use testcontainers | `TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers` |

Known discrepancy: `internal/architecture/architecture_test.go:23` sets `modulePath = "github.com/IQVO/seller-inbound"`
while `go.mod` declares `module github.com/claudioed/seller-inbound`. Whether the arch-go rules of
`TestHexagonalArchitecture` resolve packages under the `go.mod` path was not verified for this page.

## Make targets

| Target | What it runs |
| --- | --- |
| `make build` / `vet` / `fmt` / `fmt-check` | `go build ./...`, `go vet ./...`, `gofmt -w .`, the gofmt check |
| `make lint` | `golangci-lint run --allow-parallel-runners ./...` (pinned v2.14.0) |
| `make test` | `go test ./... -race` |
| `make coverage` | tests plus the 90 % gate |
| `make bdd` | `go test ./... -run TestFeatures -v` |
| `make arch-test` | the fitness tests |
| `make integration` | testcontainers suite (Docker) |
| `make mutation` / `make mutation-full` | gremlins |
| `make vuln` | `govulncheck ./...` |
| `make check` | `fmt-check vet build lint test`; the lefthook pre-push hook runs it |
| `make check-all` | `check` plus `coverage arch-test bdd` |
| `make check-fast`, `make guide-lint`, `make harness-test` | agent-harness helpers (changed packages, guide linter, hook tests) |

## CI jobs (`.github/workflows/ci.yml`)

Triggers: push and pull request on `main` and `develop`, a weekly Monday schedule, and manual dispatch.

| Job | Runs | Gate |
| --- | --- | --- |
| `lint` | golangci-lint v2.14.0 | blocking |
| `guide-lint` | agent-guide sensor and hook tests | advisory (`continue-on-error`) |
| `complexity` | gocyclo, gocognit, cyclop, funlen, nestif plus an informational gocyclo report | blocking |
| `test` | build, vet, `make coverage` | blocking |
| `bdd` | `go test ./... -run TestFeatures -v` | blocking |
| `integration` | `go test -tags=integration ./... -race -count=1` | blocking |
| `mutation-fast` | gremlins on every package under `internal/domain` | blocking |
| `mutation`, `drift` | exhaustive mutation, deadcode, `go mod tidy -diff`, knip, coverage-quality | scheduled and manual only; open a `harness:red` issue on a scheduled failure |
| `api-lint` | Spectral on both specs | blocking |
| `vuln` | govulncheck | blocking |
| `helm-lint` | `ct lint` and the Service selector test | pull requests only |
| `chart-tests` | every `charts/seller-inbound/tests/*.sh` and `test_*.py` | blocking |
| `arch-test` | fitness tests | blocking |
| `web` | the Module Federation remote: lint, `tsc -b`, vitest, build | blocking |
| `docker-publish`, `release` | image, cosign signature, SBOM, Helm chart, tag | push to `main` only, after the jobs above |

`docs.yml` builds this site on pull requests and deploys it on a push to `main`. The CI has no trivy scan and no
generated-docs drift job.
