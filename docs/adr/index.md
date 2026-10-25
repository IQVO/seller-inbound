# Architecture decision records

The decisions of `seller-inbound`, in the order they were taken. The ADR bodies
are immutable; a changed decision gets a new ADR that supersedes the old one.
The *Status* column is copied from each ADR's own `## Status` section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-seller-inbound-bounded-context.md) | Seller Inbound is a bounded context of the retail network | Accepted (bootstrap) |
| [0002](./0002-aggregates-and-invariants.md) | SellerAccount, InboundPlan and InboundShipment: aggregates, invariants and the assumptions the model left open | Accepted (2026-10-24) |
| [0003](./0003-consumed-contracts-and-local-read-models.md) | Consumed contracts and local read models | Accepted (2026-10-24) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-24) |
| [0005](./0005-seller-compliance-scorecard.md) | Seller compliance scorecard (informational) | Accepted (2026-10-24) |
| [0006](./0006-read-only-mcp-server.md) | A read-only MCP server | Accepted (2026-10-25) |
| [0007](./0007-console-web-remote.md) | A web console remote | Accepted (2026-10-25) |
