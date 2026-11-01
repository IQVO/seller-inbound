---
id: domain-message-flow
title: Domain Message Flow
sidebar_label: Domain Message Flow
sidebar_position: 5
description: "Domain Message Flow diagrams for Seller Inbound: a case-packed shipment end to end, a receipt with a discrepancy, and SSCC issuance."
---

# Domain Message Flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling). Every arrow
is a message of this context, prefixed `cmd:`, `evt:` or `qry:`. Quantities and identifiers in the notes are illustrative.
Full event types are on [Domain events](/docs/ddd/domain-events); the runtime view with errors is on
[Sequence diagrams](/docs/ddd/sequence-diagrams). The vendor-side steps (the vendor polling and submitting through the
Vendor API) belong to vendor-connectivity and are shown only as far as they reach this context.

## 1. A case-packed shipment, plan to close

### 1a. Plan and pack

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as seller
    participant OA as offer-<br/>availability
    participant NT as node-<br/>topology
    participant SI as seller-inbound
    OA->>SI: evt: CatalogItemPackagingDeclared<br/>CASE level holds 12 eaches
    NT->>SI: evt: NodeOperationalProfileDeclared<br/>inbound limits
    S->>SI: cmd: CreateInboundPlan<br/>CASE_PACKED, 36 eaches
    S->>SI: cmd: ConfirmInboundPlan
    SI-->>S: InboundPlanConfirmed
    S->>SI: cmd: CreateInboundShipment
    S->>SI: cmd: AddCarton x3<br/>12 eaches each
    SI-->>S: SSCC and label per carton
    S->>SI: cmd: LabelShipment
```

Source: `internal/adapters/inbound/kafka/consumers.go` (the two views), `internal/application/usecases/{plans,shipments}.go`.

### 1b. Ship and receive

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as seller
    participant SI as seller-inbound
    participant VC as vendor-<br/>connectivity
    participant V as vendor
    participant NP as node-<br/>performance
    S->>SI: cmd: ShipInboundShipment
    SI->>VC: evt: InboundShipmentLabelled<br/>carton manifest
    SI->>VC: evt: InboundShipmentShipped
    SI->>NP: evt: InboundShipmentShipped
    V->>VC: submits the carton-level receipt<br/>3 cartons RECEIVED
    VC->>SI: evt: InboundReceiptSubmitted
    Note over SI: RecordReceipt<br/>nothing differs
    Note over SI: InboundShipmentReceived<br/>status CLOSED
    SI->>VC: evt: InboundShipmentReceived
```

Source: `internal/application/usecases/shipments.go` (`ShipInboundShipment`), `consumers.go` (`ApplyInboundReceipt`),
`internal/adapters/outbound/kafka/encoder.go` (topic routing).

## 2. A receipt with a discrepancy

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant V as vendor
    participant VC as vendor-<br/>connectivity
    participant SI as seller-inbound
    V->>VC: submits the receipt<br/>1 MISSING, 1 DAMAGED,<br/>complianceCodes on one
    VC->>SI: evt: InboundReceiptSubmitted
    Note over SI: RecordReceipt<br/>the manifest is not edited
    Note over SI: InboundDiscrepancyRecorded<br/>per carton and per count difference
    Note over SI: SellerComplianceViolation-<br/>Recorded per distinct code
    Note over SI: InboundShipmentReceived<br/>CLOSED_WITH_DISCREPANCY
```

Source: `internal/domain/inboundshipment/receipt.go` (`RecordReceipt`, `statusDiscrepancy`, `quantityDiscrepancies`,
`violations`).

## 3. SSCC issuance

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as seller
    participant SI as seller-inbound
    S->>SI: cmd: AddCarton with sscc
    alt valid and unique
        SI-->>S: accepted, label returned
    else invalid or already used
        SI-->>S: invalid-sscc or sscc-not-unique
    end
    S->>SI: cmd: AddCarton without sscc
    SI-->>S: SSCC issued from the<br/>network GS1 prefix, label returned
```

Source: `internal/application/usecases/shipments.go` (`addWithSSCC`), `internal/adapters/outbound/sscc/sscc.go`,
`internal/domain/inboundshipment/carton.go` (`checkSSCCs`).
