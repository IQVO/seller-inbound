# ADR 0009: A2A inbound adapter

## Status

Accepted (2026-10-30).

## Context

The fleet separates three transports with no overlap: Kafka with CloudEvents
carries facts (ADR 0004), MCP carries one agent's tools (ADR 0006), and A2A
(Agent2Agent) carries the delegation of a goal between agents. seller-inbound
already has REST and a read-only MCP server over the same use cases. A
delegating agent that wants "how compliant is this seller" or "where does this
inbound stand" should be able to ask this context directly, in goal terms.

## Decision

- **A third driving adapter, a separate binary.** `internal/adapters/inbound/a2a`
  is built on the official A2A Go SDK (`github.com/a2aproject/a2a-go/v2`,
  pinned v2.7.0) and served by `cmd/a2a` over JSON-RPC 2.0 on HTTP, mounted at
  both `/` and `/a2a`, with the Agent Card at `/.well-known/agent-card.json` and
  an open `GET /healthz`. It is a message-only agent: both skills are
  transactional request/response reads, so there is no Task lifecycle.
- **READ-ONLY, because the MCP charter is read-only (ADR 0006).** An A2A skill
  may change state only if the context's MCP surface already offers that write.
  It offers none, so the agent never writes and never widens what this context
  exposes. Onboarding, suspending and reinstating sellers, the plan lifecycle,
  packing cartons, labelling and shipping stay REST-only, with the
  `Idempotency-Key` middleware and the transactional outbox.
- **Two curated, goal-shaped skills, derived from the MCP tools.**
  `seller_compliance(sellerId, window?)` answers the rolling-window compliance
  scorecard (from `get_seller_scorecard`); `inbound_status(planId | shipmentId)`
  answers the state of one inbound plan or one inbound shipment (from
  `get_inbound_plan` and `get_inbound_shipment`). `get_shipment_labels` is not
  offered: label data is a tool for an agent working on a shipment, not a
  delegation goal. The skills are not one per endpoint.
- **Input contract.** The first message part is a Data part (or a Text part with
  the same JSON) `{"skill": "<id>", "arguments": {...}}`. It is decoded
  strictly (`DisallowUnknownFields`, trailing data refused), so a misspelled
  argument is refused instead of silently dropped (a typo of `window` must not
  fall back to 30d).
- **Rejections are answered in-band.** Domain rejections and bad requests come
  back as an RFC 7807-shaped `problem` inside the message envelope
  (`{"skill", "problem": {type, title, detail, status}}`), never as JSON-RPC
  errors. The classification is the REST adapter's for the errors a read can
  raise: unknown seller, plan or shipment is 404 `not-found`, a window other
  than 7d or 30d is 400 `invalid-window`, other value validation is 422
  `validation-failed` and anything else is 500 `internal-error` (the internal
  error is logged, never sent). Request-shape problems (unknown skill, missing
  or unknown argument, malformed part) are 400. REST's 409 classes belong to
  writes this agent does not offer, and its 503 is the readiness probe's.
  Detail texts are adapter-owned.
- **Answer shape.** One agent Message carrying a Data part (the envelope with
  DTOs in the REST/MCP lowerCamelCase shapes; a rate with a zero denominator is
  omitted) and a Text part (a one-line summary), bound to the caller's
  contextId. One OTel span per skill, `a2a.skill <id>` with the attribute
  `a2a.skill`, mirroring `mcp.tool`.
- **Read-only is structural.** The adapter holds `Reads`, an interface of three
  read use cases, and nothing that can write. `cmd/a2a` is composed like
  `cmd/mcp`: only the read repositories, no unit of work, outbox, inbox or SSCC
  issuer, no outbox relay, no Kafka (no `KAFKA_BROKERS`, `EVENT_PUBLISHER` or
  consumer groups). `internal/architecture/a2a_fitness_test.go` pins the
  dependency rule (application and domain only, nothing depends on the
  adapter), the absence of an outbound A2A client and of write use cases.
- **No auth, no sibling calls.** The Agent Card declares no security schemes: A2A
  is unauthenticated like REST and MCP (the fleet-wide revert of 2026-09-11),
  and access control is the in-cluster ClusterIP boundary. The agent reads this
  context's own tables only and makes no REST, MCP or A2A call to a sibling
  context.
- **Boot and deployment.** `DATABASE_URL` and `A2A_AGENT_URL` are mandatory (a
  boot error naming the variable; the URL must be an absolute http(s) URL, since
  the card advertises it). `A2A_ADDR` defaults to `:8093`. The binary runs the
  idempotent embedded migrations on start, like `cmd/mcp`. The chart gets an
  a2a Deployment, Service and optional HTTPRoute (`/api/a2a/seller-inbound`,
  rewritten to `/`) behind `a2a.enabled` (default off); `A2A_AGENT_URL` defaults
  to the in-cluster Service DNS name and is overridden by `a2a.agentUrl`.

## Consequences

- A delegating agent can discover this context from its Agent Card and ask the
  two goal questions without REST or MCP glue, and sees the same problem slugs
  and status classes a REST caller would.
- A2A never becomes a data-sync channel: facts still leave only as CloudEvents
  through the transactional outbox, and this agent adds no outbox row.
- Adding a skill, and above all a write skill, is a reviewed act: the MCP
  surface must offer the same write first, and this ADR is amended.
- One more Deployment per environment when `a2a.enabled` is on.
