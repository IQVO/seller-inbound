---
id: ddd-artifacts
title: DDD artifacts (ddd-crew)
sidebar_label: DDD artifacts index
sidebar_position: 10
description: "Index of the ddd-crew design pack for seller-inbound: charts, canvases, maps, flows, diagrams and the event catalogue."
---

# DDD artifacts (ddd-crew)

The strategic and tactical design of `seller-inbound`, drawn with the [ddd-crew](https://github.com/ddd-crew) tools plus
UML and ER diagrams. Every diagram is Mermaid. The pack started from the retail-network design pack in IQVO/warehouse-docs
(`network/docs/contexts/seller-inbound/`, `origin/main`) and was verified against this repository's code, migrations,
contracts and ADRs on `develop`; each page carries a `Source:` line where it states something the code decides.

| Artifact | ddd-crew tool / notation | What it shows |
| --- | --- | --- |
| [Core domain chart](/docs/ddd/core-domain-chart) | [Core Domain Charts](https://github.com/ddd-crew/core-domain-charts) | Where this context sits on business differentiation vs model complexity, and why it is Supporting. |
| [Bounded context canvas](/docs/ddd/bounded-context-canvas) | [Bounded Context Canvas](https://github.com/ddd-crew/bounded-context-canvas) | Purpose, classification, roles, every inbound and outbound message, business decisions. |
| [Context map](/docs/ddd/context-map) | [Context Mapping](https://github.com/ddd-crew/context-mapping) | This context's slice of the network map: pattern and channel per edge. |
| [Aggregate design canvas](/docs/ddd/aggregate-design-canvas) | [Aggregate Design Canvas v1.1](https://github.com/ddd-crew/aggregate-design-canvas) | `SellerAccount`, `InboundPlan`, `InboundShipment` and `Carton`: states, invariants, commands, events. |
| [Domain message flow](/docs/ddd/domain-message-flow) | [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling) | A case-packed shipment end to end, a receipt with a discrepancy, SSCC issuance. |
| [EventStorming](/docs/ddd/eventstorming) | [EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet) | Process-level boards with sticky colours and hotspots. |
| [Ubiquitous language](/docs/ddd/ubiquitous-language) | Glossary | Every term with the Go identifier that implements it. |
| [Class diagrams](/docs/ddd/class-diagram) | UML class diagram | Domain types and the hexagonal layout. |
| [Entity-relationship diagram](/docs/ddd/entity-relationship) | ER diagram | The Postgres schema of migrations 0001 and 0002: eleven tables and one sequence. |
| [Sequence diagrams](/docs/ddd/sequence-diagrams) | UML sequence diagram | `AddCarton`, `ShipInboundShipment`, the receipt consumer and plan confirmation, with error branches. |
| [Domain events](/docs/ddd/domain-events) | Event catalogue | Nine published events with payloads and an example envelope; the eight consumed. |

Related pages outside the pack: [Use cases](/docs/ddd/use-cases), [Subdomain classification](/docs/ddd/subdomain-classification),
[Integration](/docs/ecosystem/integration) and the [event catalogue](/docs/api-reference/events).

:::note[Sources of truth]
The code wins. Domain rules come from `internal/domain/{seller,inboundplan,inboundshipment,network,scorecard,shared}`,
behaviour from `internal/application/usecases`, the envelope from `internal/adapters/kafka/cloudevents`, the schema from
`internal/adapters/outbound/postgres/migrations`, the REST contract from `apis/openapi.yaml`, the event contract from
`apis/asyncapi.yaml`, and decisions from the ADRs in `docs/adr/`. Where a page and the code disagree, the page is wrong.
:::

:::info Not in this pack
The MCP server ([ADR 0006](/docs/adr/0006-read-only-mcp-server)), the A2A adapter
([ADR 0009](/docs/adr/0009-a2a-inbound-adapter)) and the web console remote ([ADR 0007](/docs/adr/0007-console-web-remote),
`web/`) are built, but they are read surfaces over the model and add no aggregate, entity or event of their own, so the
pack has no pages for them. They are described under [MCP tools](/docs/mcp/tools) and the [API overview](/docs/api-reference).
:::
