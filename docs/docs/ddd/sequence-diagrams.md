---
id: sequence-diagrams
title: Sequence Diagrams
sidebar_label: Sequence Diagrams
sidebar_position: 8
description: "One sequence per key Seller Inbound use case, from inbound adapter through aggregate, repository and outbox."
---

# Sequence Diagrams

Shared conventions, from the code:

- Every use case runs inside `Service.atomically`, one Postgres transaction (`UnitOfWork`). The repository saves with
  `WHERE version = :expected`; a lost race is `concurrent-update` (409).
- Outbox rows are written in the same transaction as the aggregate (`EventOutbox.Append`); the in-process relay produces
  them to Kafka later (`retailnetwork.seller-inbound.events`; the six analytics-only events to
  `retailnetwork.seller-inbound.analytics`).
- Write routes pass the HTTP idempotency middleware first (`Idempotency-Key`); it is omitted from the diagrams.
- Consumers claim the CloudEvents `id` in `processed_events` in the same transaction as their effect.
- Event names are short; full types are on the [Event catalogue](/docs/api-reference/events).

## 1. AddCarton

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http POST cartons
    participant UC as AddCarton
    participant V as read models
    participant I as SSCC issuer
    participant A as InboundShipment
    participant R as ShipmentRepo
    H->>UC: AddCarton(shipmentId, carton)
    UC->>R: Get(shipmentId), plan and sibling shipments
    UC->>V: node limits and CASE levels of the items
    opt no sscc supplied
        UC->>I: Next()
    end
    UC->>A: AddCarton(carton, rules)
    alt rule broken
        A-->>UC: case-pack-not-uniform, case-qty-mismatches-gtin,<br/>carton-overweight, carton-oversize, invalid-sscc
        UC-->>H: 4xx problem+json
    else accepted
        A-->>UC: carton
        UC->>R: Save(expected version)
        alt supplied SSCC already registered
            R-->>UC: sscc-not-unique
            UC-->>H: 409 problem+json
        else saved
            UC-->>H: 201 carton, ssccIssued and label
        end
    end
```

Source: `internal/application/usecases/shipments.go` (`AddCarton`, `addWithSSCC`). An issued SSCC that collides is
re-issued up to `SsccIssueAttempts` times; a supplied one that collides is the seller's error. No event is written.

## 2. ShipInboundShipment

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http POST ship
    participant UC as ShipInboundShipment
    participant A as InboundShipment
    participant R as ShipmentRepo
    participant OB as Outbox
    participant K as Relay + Kafka
    H->>UC: ShipInboundShipment(shipmentId)
    UC->>R: Get(shipmentId)
    UC->>UC: seller ACTIVE and node able to receive
    UC->>A: Ship(now)
    alt not LABELLED
        A-->>UC: shipment-not-labelled
        UC-->>H: 409 shipment-not-labelled
    else LABELLED
        A-->>UC: SHIPPED and InboundShipmentShipped
        UC->>R: Save(expected version)
        UC->>OB: Append(event), same transaction
        UC-->>H: 200 shipment
        OB->>K: relay publishes later
    end
```

Source: `shipments.go` (`ShipInboundShipment`). The outcome is counted on `seller_inbound.shipment.shipped`
(`outcome` `shipped` or `rejected`, `reason` the problem slug); see [Observability](/docs/operations/observability).

## 3. ApplyInboundReceipt (consumer)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant K as Kafka
    participant C as consumer
    participant IB as processed_events
    participant UC as ApplyInboundReceipt
    participant A as InboundShipment
    participant OB as Outbox
    K->>C: InboundReceiptSubmitted
    C->>IB: claim event id
    alt already processed
        IB-->>C: duplicate
        C-->>K: commit and skip
    else new
        C->>UC: Execute(receipt)
        UC->>A: RecordReceipt(receipt)
        A-->>UC: statuses, discrepancies,<br/>RECEIVING or closed
        UC->>OB: discrepancy and violation events
        UC->>OB: InboundShipmentReceived when closed
        C-->>K: commit after the transaction
    end
```

Source: `internal/adapters/inbound/kafka/consumer.go`, `internal/application/usecases/consumers.go`,
`internal/domain/inboundshipment/receipt.go`. A receipt for a shipment that is already closed is a no-op; the scorecard
facts are written in the same transaction as the receipt.

## 4. Confirm a plan

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http POST confirm
    participant UC as ConfirmInboundPlan
    participant S as SellerRepo
    participant V as NodeViews
    participant P as InboundPlan
    participant R as PlanRepo
    participant OB as Outbox
    H->>UC: ConfirmInboundPlan(planId)
    UC->>R: Get(planId)
    UC->>S: seller ACTIVE?
    UC->>V: node able to receive?
    UC->>P: Confirm(now)
    P-->>UC: CONFIRMED and InboundPlanConfirmed
    UC->>R: Save(expected version)
    UC->>OB: Append(event)
    UC-->>H: 200 plan
```

Source: `internal/application/usecases/plans.go` (`ConfirmInboundPlan`, `requireNode`). A refusal maps to
`seller-suspended`, `node-cannot-receive` or `illegal-transition`.
