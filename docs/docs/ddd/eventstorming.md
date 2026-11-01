---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
sidebar_position: 6
description: "Process-level EventStorming boards for Seller Inbound, from a seller's plan to a closed shipment, with hotspots."
---

# EventStorming

Process-level boards in the ddd-crew [EventStorming glossary & cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet)
notation. Every sticky is a command, aggregate, event, policy or read model that exists in the code; the hotspots are the
open questions of the network gap register (IQVO/warehouse-docs) and of this repository's ADRs. The network-wide board is
[EventStorming big picture](https://iqvo.github.io/warehouse-docs/network/strategic-design/eventstorming-big-picture).

| Sticky | Colour | Meaning here |
| --- | --- | --- |
| Command | blue `#a5d8ff` | `CreateInboundPlan`, `AddCarton`, `LabelShipment`... |
| Aggregate | yellow `#ffec99` | `InboundPlan`, `InboundShipment` |
| Domain Event | orange `#ffc078` | an event published or consumed |
| Policy | lilac `#d0bfff` | a rule applied by a use case |
| Read Model | green `#b2f2bb` | `NodeInboundView`, `CatalogPackagingView` |
| External System | pink `#fcc2d7` | another context or an actor |
| Hotspot | red `#ff8787` | an open question |

## 1. Plan and confirm

```mermaid
flowchart TB
    S["seller"]:::external
    C1["CreateInboundPlan"]:::command
    R1["NodeInboundView"]:::readmodel
    R2["CatalogPackagingView"]:::readmodel
    G1["InboundPlan"]:::aggregate
    E1["InboundPlanConfirmed"]:::event
    S --> C1 --> G1 --> E1
    R1 --- G1
    R2 --- G1
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/plans.go`. `NodeInboundView` is read at creation and confirmation (`requireNode`);
`CatalogPackagingView` is read later, on `AddCarton`. The event appears on confirmation (`ConfirmInboundPlan`), not on
creation.

## 2. Pack, label, ship

```mermaid
flowchart TB
    S["seller"]:::external
    C2["AddCarton"]:::command
    P1["Policy: SsccIssuance"]:::policy
    G2["InboundShipment<br/>Carton"]:::aggregate
    C3["LabelShipment"]:::command
    E2["InboundShipmentLabelled"]:::event
    C4["ShipInboundShipment"]:::command
    E3["InboundShipmentShipped"]:::event
    H1["Hotspot: who creates<br/>owner-scoped listings?"]:::hotspot
    S --> C2 --> G2
    P1 --- C2
    S --> C3 --> G2 --> E2
    S --> C4 --> G2 --> E3
    E3 --- H1
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/shipments.go`, `internal/domain/inboundshipment/{shipment,carton}.go`.

## 3. Receipt and reconciliation

```mermaid
flowchart TB
    V["vendor"]:::external
    E1["InboundReceiptSubmitted"]:::event
    P1["Policy: ReceiveOnVendorReceipt"]:::policy
    C1["RecordReceipt"]:::command
    G1["InboundShipment"]:::aggregate
    P2["Policy: RecordDiscrepancies"]:::policy
    E2["InboundShipmentReceived"]:::event
    E3["InboundDiscrepancyRecorded"]:::event
    E4["SellerComplianceViolationRecorded"]:::event
    H1["Hotspot: shipment never<br/>received?"]:::hotspot
    H2["Hotspot: violations feed only an<br/>informational scorecard"]:::hotspot
    V --> E1 --> P1 --> C1 --> G1
    P2 --- G1
    G1 --> E2
    G1 --> E3
    G1 --> E4
    G1 --- H1
    E4 --- H2
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/consumers.go` (`ApplyInboundReceipt`), `internal/domain/inboundshipment/receipt.go`.

## Hotspots

| Hotspot | Where it comes from |
| --- | --- |
| Who creates owner-scoped listings from received stock | Network gap register G51 (IQVO/warehouse-docs): nothing in this service creates a listing |
| A shipped shipment that is never received | No timeout exists: a `SHIPPED` shipment stays so until a receipt arrives (see [Troubleshooting](/docs/operations/troubleshooting)) |
| Violations are recorded and nothing acts on them | The scorecard is informational ([ADR 0005](/docs/adr/0005-seller-compliance-scorecard)); no consumer of the analytics topic exists in the organisation |
