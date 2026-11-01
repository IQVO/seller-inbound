---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
description: "The four read-only MCP tools of seller-inbound: inputs, outputs and annotations, from the pinned tool registry."
---

# MCP tools

`cmd/mcp` serves a read-only [Model Context Protocol](https://modelcontextprotocol.io) server over **Streamable HTTP
only** ([ADR 0006](/docs/adr/0006-read-only-mcp-server)). It is unauthenticated like the rest of the fleet: the
in-cluster ClusterIP boundary is the access control.

| | |
| --- | --- |
| Listen address | `MCP_ADDR`, default `:8090` |
| Endpoint | `/mcp` (also mounted at `/`) |
| Probe | `GET /healthz` returns `200 {"status":"ok"}` |
| Needs | `DATABASE_URL` only; it dials no Kafka and registers no write seam |
| Gateway | `/api/mcp/seller-inbound` in `warehouse-infra` (`helm-values/seller-inbound.yaml`, `mcp.route.path`); the ops agent calls `http://seller-inbound-mcp.warehouse-systems.svc.cluster.local:8090/mcp` |
| Deployed by | the chart behind `mcp.enabled` (default off, on in `warehouse-infra`) |

Source: `cmd/mcp/main.go`, `cmd/mcp/router.go`, `internal/adapters/inbound/mcp/{server,tools,errors}.go`.

**How this page was produced.** The list below comes from the pinned tool registry
`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`, which `TestToolRegistryGolden` compares with the
server's `tools/list` on every test run; it was not captured from a running server. Run `tools/list` against a live
`cmd/mcp` to see the same four tools.

## Annotations

All four tools carry `readOnlyHint: true`, `idempotentHint: true` and `openWorldHint: false`. There is no write tool:
the governance tests in `internal/adapters/inbound/mcp/governance_test.go` (`TestToolSurface` with its write-verb and
write-seam sensors) fail on a write-named tool or a write seam, and `TestMCPAdapterDependencyRule` limits the adapter
to the application and domain layers. A tool call adds no `outbox_events` row.

| Tool | Required input | Optional input | Annotation |
| --- | --- | --- | --- |
| `get_inbound_plan` | `planId` | none | read-only |
| `get_inbound_shipment` | `shipmentId` | none | read-only |
| `get_shipment_labels` | `shipmentId` | none | read-only |
| `get_seller_scorecard` | `sellerId` | `window` | read-only |

Every input schema is `additionalProperties: false`. The tools are bound to the same use cases as the REST `GET`
routes. A failure is an `isError` tool result whose text is `<slug>: <detail>` with the REST slug (`not-found`,
`invalid-window`, `validation-failed`); an untyped error is logged (`mcp tool failed with an unexpected error`) and
answered as `internal-error: an unexpected internal error occurred`.

## `get_inbound_plan`

Reads one inbound plan.

- **Input:** `planId` (string, required, for example `pln-0f3a91`).
- **Output:** `planId`, `sellerId`, `destinationNodeId`, `packingOption` (`INDIVIDUAL` or `CASE_PACKED`), `state`
  (`DRAFT`, `CONFIRMED`, `CANCELLED`), `lines` (each `catalogItemId`, `gtin`, `qtyEaches`), `createdAt`, and
  `confirmedAt` and `cancelledAt` (string or null).
- **Errors:** an unknown id is `not-found`.

## `get_inbound_shipment`

Reads one inbound shipment with its cartons.

- **Input:** `shipmentId` (string, required, for example `shp-0f3a91`).
- **Output:** `shipmentId`, `planId`, `sellerId`, `nodeId`, `state`, `createdAt`, `labelledAt`, `shippedAt`,
  `receivedAt` (string or null) and `cartons`: each with `sscc`, `parentSscc`, `kind`, `contents` (`catalogItemId`,
  `gtin`, `qtyEaches`, optional `lot` and `expiry`), `grossWeightG`, `dimsMm` (`l`, `w`, `h`), `sealed` and
  `receiptStatus`.
- **Errors:** `not-found`.

## `get_shipment_labels`

Reads the label data of a shipment, one entry per carton.

- **Input:** `shipmentId` (required).
- **Output:** `shipmentId`, `state`, `destinationNodeId` and `labels`: each with `labelRef` (`LBL-<sscc>`), `sscc`,
  `gs1128` (the element string `(00)<sscc>`), `humanReadable`, `parentSscc`, `kind`, `contents`, `contentSummary`,
  `totalEaches` and `destinationNodeId`.
- **Errors:** `not-found`. The tool works in any shipment state, including `DRAFT`.

## `get_seller_scorecard`

Answers a seller's informational compliance scorecard ([ADR 0005](/docs/adr/0005-seller-compliance-scorecard)).

- **Input:** `sellerId` (required, for example `seller-77`); `window` (optional, `7d` or `30d`, default `30d`).
- **Output:** `sellerId`, `window`, `windowStart`, `windowEnd`, `shipmentsReceived`, `cartonsReported`,
  `cartonsReceived`, `receiptAccuracyRate` (number or null: omitted when nothing was reported),
  `discrepancyRatePerShipment` (number or null: omitted when no shipment closed), `discrepanciesByKind` (kind to
  count), `complianceViolations` and `complianceTier` (`GOOD`, `WATCH`, `AT_RISK`).
- **Errors:** an unknown seller is `not-found`; any other window is `invalid-window`. A suspended seller has a
  scorecard.

## Known inconsistencies

- The descriptions of `get_inbound_shipment` (and the A2A `inbound_status` skill, which reuses the wording) list the
  shipment states `PLANNED, PACKING, LABELLED, SHIPPED or RECEIVED`. The aggregate's real states are `DRAFT`,
  `LABELLED`, `SHIPPED`, `RECEIVING`, `CLOSED` and `CLOSED_WITH_DISCREPANCY`
  (`internal/domain/inboundshipment/shipment.go`), and those are what the `state` field returns. The descriptions are
  pinned by the golden file, so correcting them is a reviewed code change.
- `cmd/mcp` logs `environment` as `dev` when `ENVIRONMENT` is unset, while the telemetry resource says `local`
  ([Configuration](/docs/operations/configuration)).
