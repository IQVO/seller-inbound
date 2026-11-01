---
id: class-diagram
title: Class Diagram
sidebar_label: Class Diagram
sidebar_position: 9
description: "UML class diagrams of the Seller Inbound domain types and its hexagonal layout, with the as-built package names."
---

# Class Diagram

UML class diagrams of the domain model. Fields are unexported (`-`), methods exported (`+`); only methods that exist in
the code are listed. The packages are `internal/domain/seller` (`Account`), `internal/domain/inboundplan` (`Plan`),
`internal/domain/inboundshipment` (`Shipment`, `Carton`, `Receipt`), `internal/domain/network` (the read-model value
types), `internal/domain/scorecard` (the informational scorecard) and `internal/domain/shared/gs1` (`GTIN`, `SSCC`).
The terms are mapped on [Ubiquitous language](/docs/ddd/ubiquitous-language).

## 1. InboundShipment aggregate

```mermaid
classDiagram
    direction TB
    class Shipment {
        <<AggregateRoot>>
        -id ID
        -planID string
        -sellerID string
        -nodeID string
        -state State
        -cartons Carton[]
        -version int64
        +AddCarton(in NewCarton, r CartonRules) Carton, error
        +Label(rules PlanRules, now Time) Event[], error
        +Ship(now Time) Event[], error
        +RecordReceipt(r Receipt, now Time) Event[], error
        +Snapshot() Snapshot
    }
    class Carton {
        <<Entity>>
        +SSCC string
        +ParentSSCC string
        +Kind Kind
        +Contents Content[]
        +GrossWeightG int
        +Dims Dims
        +Sealed bool
        +Receipt ReceiptStatus
    }
    class Content {
        <<ValueObject>>
        +CatalogItemID string
        +GTIN string
        +QtyEaches int
        +Lot string
        +Expiry string
    }
    Shipment "1" *-- "0..*" Carton
    Carton "1" *-- "1..*" Content
```

Source: `internal/domain/inboundshipment/{shipment,carton,receipt}.go`. `Restore(Snapshot)` rebuilds the aggregate and
re-validates it. A new shipment starts empty and `DRAFT`; cartons are added one at a time, so the diagram says `0..*`.

## 2. Plan, seller and identifiers

```mermaid
classDiagram
    direction LR
    class Account {
        <<AggregateRoot>>
        -id ID
        -name string
        -state State
        -version int64
        +Suspend(reason string, now Time) Event[], error
        +Reinstate(now Time) Event[], error
        +EnsureActive() error
        +OwnerRef() string
    }
    class Plan {
        <<AggregateRoot>>
        -id ID
        -sellerID string
        -destination string
        -packing PackingOption
        -lines Line[]
        -state State
        -version int64
        +Confirm(now Time) Event[], error
        +Cancel(now Time) error
        +EnsureConfirmed() error
        +PlannedByItem() map
    }
    class Line {
        <<ValueObject>>
        +CatalogItemID string
        +GTIN string
        +QtyEaches int
    }
    class GTIN {
        <<ValueObject>>
        -digits14 string
    }
    class SSCC {
        <<ValueObject>>
        -digits18 string
    }
    Plan "1" *-- "1..*" Line
    Line --> GTIN
    Plan ..> Account : sellerID
```

Source: `internal/domain/seller/seller.go`, `internal/domain/inboundplan/plan.go`, `internal/domain/shared/gs1`. The GTIN
parser accepts the short GS1 forms, checks the mod-10 digit and returns the 14-digit form; the SSCC parser needs exactly
18 digits with a valid mod-10 digit. A client-supplied check digit is never trusted.

## 3. Hexagonal layout

```mermaid
flowchart TB
    subgraph IN["inbound adapters"]
        HTTP["http seller API"]
        CONS["kafka consumers"]
        MCP["mcp and a2a reads"]
    end
    subgraph APP["application"]
        UC["use cases and ports"]
    end
    subgraph DOM["domain"]
        AGG["seller, inboundplan,<br/>inboundshipment, scorecard"]
    end
    subgraph OUT["outbound adapters"]
        PG["postgres repos<br/>outbox, inbox"]
        RELAY["outbox relay to kafka"]
        SS["sscc issuer, ids, clock"]
    end
    HTTP --> UC
    CONS --> UC
    MCP --> UC
    UC --> AGG
    UC --> PG
    UC --> SS
    PG --> RELAY
```

Source: `internal/adapters/{inbound,outbound}`, `internal/application`; the dependency rule is enforced by
`TestHexagonalArchitecture` (see [Testing](/docs/development/testing) and [Architecture](/docs/overview/architecture)).
