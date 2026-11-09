---
id: entity-relationship
title: Entity Relationship
sidebar_label: Entity Relationship
sidebar_position: 10
description: "The Postgres schema of Seller Inbound's own database, as created by migrations 0001 to 0004."
---

# Entity Relationship

The schema of this context's own Postgres database, as created by
`internal/adapters/outbound/postgres/migrations/0001_seller_inbound_schema.up.sql`, `0002_seller_scorecard.up.sql`,
`0003_idempotency_key_global.up.sql` and `0004_placement_advisory.up.sql`.
There are no foreign keys: each aggregate is one row, and the parts drawn as child entities (plan lines, cartons and
their contents) are stored as `JSONB` on that row. Ids of other aggregates are held by value. The `sscc_registry` table
holds network-wide SSCC uniqueness ([ADR 0002](/docs/adr/0002-aggregates-and-invariants)). Fourteen tables (nine in 0001,
two in 0002, three in 0004; 0003 only narrows the primary key of `idempotency_keys`) and one sequence, `sscc_serial_seq`, which supplies the serial of every SSCC the network issues.

Seller and plan aggregates:

```mermaid
erDiagram
    sellers {
        TEXT seller_id PK "ownerRef"
        TEXT name
        TEXT state "ACTIVE, SUSPENDED"
        TIMESTAMPTZ onboarded_at
        TIMESTAMPTZ suspended_at "nullable"
        TEXT suspend_reason
        BIGINT version
    }
    inbound_plans {
        TEXT plan_id PK
        TEXT seller_id "by value"
        TEXT destination_node_id "by value"
        TEXT packing_option "INDIVIDUAL, CASE_PACKED"
        TEXT state "DRAFT, CONFIRMED, CANCELLED"
        JSONB lines "catalogItemId, gtin, qtyEaches"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ confirmed_at "nullable"
        TIMESTAMPTZ cancelled_at "nullable"
        BIGINT version
    }
```

Shipment aggregate and the SSCC registry:

```mermaid
erDiagram
    inbound_shipments {
        TEXT shipment_id PK
        TEXT plan_id "by value"
        TEXT seller_id "by value"
        TEXT node_id "by value"
        TEXT state "six states"
        JSONB cartons "SSCC cartons with contents and receipt status"
        JSONB unexpected "cartons the manifest did not list"
        BOOLEAN discrepant
        TIMESTAMPTZ created_at
        TIMESTAMPTZ labelled_at "nullable"
        TIMESTAMPTZ shipped_at "nullable"
        TIMESTAMPTZ received_at "nullable"
        BIGINT version
    }
    sscc_registry {
        TEXT sscc PK "unique network-wide"
        TEXT shipment_id
    }
```

Read models fed by consumed events:

```mermaid
erDiagram
    node_inbound_views {
        TEXT node_id PK
        TEXT node_type
        TEXT status
        INTEGER profile_revision
        JSONB inbound_limits "nullable"
        TEXT operator "0004, empty for a pre-0004 registration"
        TEXT region_id "0004, empty for a pre-0004 registration"
    }
    catalog_pack_levels {
        TEXT catalog_item_id PK
        TEXT level PK "EACH, INNER, CASE, PALLET"
        TEXT gtin PK
        TEXT child_gtin
        INTEGER units_per_pack
        BOOLEAN active
        TIMESTAMPTZ declared_at "nullable"
        TIMESTAMPTZ retired_at "nullable"
    }
```

Seller scorecard facts (migration 0002, [ADR 0005](/docs/adr/0005-seller-compliance-scorecard)), written in the same unit
of work as the receipt:

```mermaid
erDiagram
    seller_scorecard_receipts {
        TEXT shipment_id PK
        TEXT seller_id
        INTEGER cartons_reported
        INTEGER cartons_received
        TIMESTAMPTZ recorded_at
    }
    seller_scorecard_occurrences {
        BIGSERIAL id PK
        TEXT seller_id
        TEXT shipment_id
        TEXT occurrence_type "DISCREPANCY, VIOLATION"
        TEXT code
        TIMESTAMPTZ recorded_at
    }
```

Placement advisory (migration 0004, [ADR 0012](/docs/adr/0012-the-placement-advisory)): the two halves of the
`DemandHeatView` read model and the `PlacementPlan` aggregate:

```mermaid
erDiagram
    demand_heat_lines {
        TEXT assignment_id PK "order-sourcing assignment group"
        INTEGER group_no PK "CHECK group_no >= 1"
        INTEGER line_no PK "CHECK line_no >= 1"
        TEXT node_id
        TEXT catalog_item_id "indexed with released_at"
        INTEGER quantity "CHECK quantity >= 1"
        TIMESTAMPTZ released_at
    }
    listing_availability {
        TEXT catalog_item_id PK
        TEXT node_id PK
        TEXT listing_id
        INTEGER available_qty "CHECK available_qty >= 0"
        TIMESTAMPTZ as_of
    }
    placement_plans {
        TEXT plan_id PK
        TEXT seller_id "by value, indexed with catalog_item_id"
        TEXT catalog_item_id
        INTEGER cover_days "CHECK 1..90"
        INTEGER window_days "CHECK >= 1"
        TEXT basis
        TEXT state "SUGGESTED, ACCEPTED, SUPERSEDED"
        JSONB suggestions "per-node suggestion value objects"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ accepted_at "nullable"
        TIMESTAMPTZ superseded_at "nullable"
        BIGINT version "CHECK version >= 1"
    }
```

`demand_heat_lines` holds one row per live released line of an assignment group (a later release of the group replaces
its rows, `FulfillmentCancelled` deletes them) and `listing_availability` the latest `ListingAvailabilityChanged` per
item and node. The `placement_plans` suggestions are value objects stored as `JSONB`. Nothing in the running `cmd/api`
writes `demand_heat_lines` yet; see [Integration](/docs/ecosystem/integration#upstream-what-seller-inbound-consumes).

Outbox, inbox and idempotency (infrastructure tables):

```mermaid
erDiagram
    outbox_events {
        BIGSERIAL id PK
        TEXT event_id "UNIQUE with topic"
        TEXT topic
        TEXT event_type "full CloudEvents type"
        TEXT subject
        BYTEA key "nullable"
        TEXT dataschema
        BYTEA value "encoded CloudEvent"
        JSONB headers
        TIMESTAMPTZ created_at
        TIMESTAMPTZ published_at "nullable"
        INTEGER attempts
        TEXT last_error "nullable"
    }
    processed_events {
        TEXT event_id PK "CloudEvents id"
        TEXT event_type
        TIMESTAMPTZ processed_at
    }
    idempotency_keys {
        TEXT route "method + path of the stored request"
        TEXT key PK "global since 0003, one record per key"
        TEXT request_hash
        INTEGER status_code "nullable"
        BYTEA response_body "nullable"
        JSONB response_headers "nullable"
        TIMESTAMPTZ created_at
        TIMESTAMPTZ completed_at "nullable"
    }
```

Source: the four migrations above. The idempotency rows are purged after 24 hours by the retention sweep in `cmd/api`
(see [Runbook](/docs/operations/runbook)). Check constraints (`state`, `packing_option`, `level`, `occurrence_type`,
`version >= 1`, `cartons_received <= cartons_reported`) are in the migrations. Column details of the outbox and
consumer behaviour are on [Troubleshooting](/docs/operations/troubleshooting).
