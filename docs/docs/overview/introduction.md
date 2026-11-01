---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What seller-inbound owns and does not own, its Supporting classification in the Network area, its main capabilities and where to read next."
---

# Seller Inbound

**Seller Inbound** (`seller-inbound`) is the front door for stock that **sellers** send to the retail network. A seller
declares an **inbound plan** in eaches, packs it into **cartons** that each carry an **SSCC** and a printed label,
and ships it to a vendor node. Cartons may be **case-packed** (a master carton holding one item at the quantity of its
declared case level) or mixed. The context checks each plan against the destination node's declared inbound limits and
the item's packaging hierarchy, publishes the carton manifest for the Vendor API, and closes the shipment from the
vendor's carton-level receipt, recording every discrepancy instead of correcting it. Stock stays owned by its seller
(`ownerRef` equals the `sellerId`).

| | |
| --- | --- |
| Area | Network (retail-network), tier `partner` |
| Subdomain class | **Supporting**, custom-built ([Classification](/docs/ddd/subdomain-classification)) |
| Repository | [`IQVO/seller-inbound`](https://github.com/IQVO/seller-inbound), Go, hexagonal |
| Binaries | `cmd/api` (REST, consumers, outbox relay), `cmd/mcp` (read-only MCP), `cmd/a2a` (read-only A2A); `web/` is the console remote |
| Store and bus | Postgres (owned by the context), Kafka (the fleet's single broker) |
| Design source | `network/model/model.yaml` in IQVO/warehouse-docs; this repository's [ADRs](/docs/adr) record what the service does |

This repository is a service of the retail network, not of the warehouse-systems fleet. The fleet reaches the network
only through the Vendor API of `vendor-connectivity`.

## What it owns

- **`SellerAccount`**: onboarding, suspension and reinstatement of a seller; the gate every plan, shipment and carton
  passes ([Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas)).
- **`InboundPlan`**: what a seller will send, to which node, `INDIVIDUAL` or `CASE_PACKED`, in eaches.
- **`InboundShipment` with its `Carton` entities**: one physical consignment, the carton manifest, the SSCCs and label
  data, and the reconciliation against the vendor's receipt.
- **SSCC uniqueness network-wide** (`sscc_registry`) and **SSCC issuance** from the network's own GS1 company prefix when
  the seller supplies none.
- **The seller compliance scorecard** ([ADR 0005](/docs/adr/0005-seller-compliance-scorecard)): an informational rolling
  7-day or 30-day read over the discrepancies and violations this service recorded. It suspends and blocks nothing.
- **Two local read models**, `NodeInboundView` and `CatalogPackagingView`, folded from neighbours' events
  ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models)).

## What it does not own

- **The node registry and inbound limits** belong to node-topology; **the packaging hierarchy and GTINs** to
  offer-availability. They are copied in as events, never looked up.
- **The vendor's building.** Receiving, stowing, breaking a case and picking happen in the vendor's systems; the
  vendor's carton-level answer arrives as `InboundReceiptSubmitted` through vendor-connectivity. A vendor never calls
  this service.
- **Commingling, FEFO and lot allocation.** Lot and expiry are captured on cartons and carried on events; nothing
  allocates by them.
- **Scoring that acts.** The scorecard is read-only information; suspending a seller stays the manual
  `POST /sellers/{id}:suspend`. No chargebacks or money movement (payments are a future, Generic context).
- **Inbound freight and placement advice.** The seller delivers to the node it names; which node should hold which item
  is not modelled.
- **EDI.** The published language is JSON over REST and CloudEvents on Kafka.
- **Authentication.** REST, MCP and A2A are unauthenticated; the in-cluster boundary is the access control.

## Main capabilities

1. **Plan** an inbound against a node that can receive, with lines validated as GS1 GTINs and quantities in eaches.
2. **Pack**: add cartons whose contents, weight and size are checked against the plan, the packaging hierarchy and the
   node's limits, with an SSCC supplied or issued.
3. **Label**: freeze the manifest once the cartons match the plan, and serve the label data (GS1-128 `(00)` element
   string, human-readable line, content summary) for each carton.
4. **Ship**, which publishes the manifest-bearing events for vendor-connectivity and node-performance.
5. **Reconcile** the vendor's receipt: close the shipment `CLOSED` or `CLOSED_WITH_DISCREPANCY`, publish each
   discrepancy and compliance violation, and keep the scorecard facts.
6. **Report** the scorecard, and read plans, shipments and labels over REST, MCP and A2A.

## Business rules in one place

- Quantities are always **eaches**; a bare quantity never changes meaning.
- A `CASE_PACKED` carton is **one item at one quantity**, the `unitsPerPack` of the item's active CASE level.
- A supplied SSCC is accepted only if valid (18 digits, mod-10) and unique network-wide.
- Carton weight and size are the **node's** declared limits.
- No shipment ships without a label on every carton.
- Discrepancies are **recorded, never corrected**: the manifest is not edited to match the dock.
- Every write takes an `Idempotency-Key`.

## Where to read next

| You want to | Read |
| --- | --- |
| Run it | [Quickstart](/docs/overview/quickstart), [Configuration](/docs/operations/configuration) |
| Understand the structure | [Architecture](/docs/overview/architecture), [Runtime](/docs/overview/runtime) |
| Operate and debug it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Troubleshooting](/docs/operations/troubleshooting) |
| Test it | [Testing](/docs/development/testing) |
| Learn the domain | [Ubiquitous language](/docs/ddd/ubiquitous-language), [Use cases](/docs/ddd/use-cases), [DDD artifacts](/docs/ddd/ddd-artifacts) |
| Integrate with it | [API reference](/docs/api-reference), [Events](/docs/api-reference/events), [Integration](/docs/ecosystem/integration), [MCP tools](/docs/mcp/tools) |
| See why | [Architecture decision records](/docs/adr) |

The network-wide view of this context (the other ten contexts, the model and the gap register) is on the
[retail network documentation](https://iqvo.github.io/warehouse-docs/network/contexts/seller-inbound).
