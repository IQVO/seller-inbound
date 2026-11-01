---
id: use-cases
title: Use cases
sidebar_label: Use cases
sidebar_position: 1
description: "Every application use case of seller-inbound: trigger, inputs, invariants checked, errors and the events raised."
---

# Use cases

One method of `usecases.Service` (`internal/application/usecases`) per command, query or consumed event. Each command
runs in one Postgres transaction (`atomically`): the aggregate change, its outbox rows and, for consumers, the
processed-event claim commit or roll back together. Nothing leaves the context synchronously: the node and packaging
facts are read from local tables fed by Kafka ([Integration](/docs/ecosystem/integration)).

## Conventions

- **Errors** are `application/problem+json`; the status and slug of every error are in
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).
- **Idempotency-Key** is required on every `POST` (1 to 128 characters, kept 24 h); a repeat with the same body
  replays the first answer, a different body is `422 idempotency-key-reused`.
- **Seller must be `ACTIVE`** for every plan, shipment and carton command and for confirm and ship
  (`requireActiveSeller`, `internal/application/usecases/sellers.go`). Cancelling a plan and the receipt consumer do not
  check it.
- **Destination node must be able to receive** (`requireNode`): known to `node_inbound_views`, `ACTIVE`, type
  `FULFILLMENT_CENTER` or `INBOUND_CROSS_DOCK`. An unknown node is `node-cannot-receive`.
- Quantities are always eaches; GTINs are normalised to 14 digits; variable-measure GTINs (indicator digit 9) are
  refused.

Source: `internal/application/usecases/{sellers,plans,shipments,scorecard,consumers}.go`, domain rules in
`internal/domain/{seller,inboundplan,inboundshipment,network}`.

## Summary

| Use case | Trigger | Raises |
| --- | --- | --- |
| `OnboardSeller` | `POST /sellers` | `SellerOnboarded` |
| `SuspendSeller` | `POST /sellers/{sellerId}:suspend` | `SellerSuspended` |
| `ReinstateSeller` | `POST /sellers/{sellerId}:reinstate` | `SellerReinstated` |
| `CreateInboundPlan` | `POST /inbound-plans` | none |
| `ConfirmInboundPlan` | `POST /inbound-plans/{planId}:confirm` | `InboundPlanConfirmed` |
| `CancelInboundPlan` | `POST /inbound-plans/{planId}:cancel` | none |
| `CreateInboundShipment` | `POST /inbound-plans/{planId}/shipments` | none |
| `AddCarton` | `POST /inbound-shipments/{shipmentId}/cartons` | none |
| `LabelShipment` | `POST /inbound-shipments/{shipmentId}:label` | `InboundShipmentLabelled` |
| `ShipInboundShipment` | `POST /inbound-shipments/{shipmentId}:ship` | `InboundShipmentShipped` |
| `ApplyInboundReceipt` | `InboundReceiptSubmitted` on Kafka | `InboundDiscrepancyRecorded`, `SellerComplianceViolationRecorded`, `InboundShipmentReceived` |
| `ApplyNodeRegistered`, `ApplyNodeProfileDeclared`, `ApplyNodeActivated`, `ApplyNodeSuspended`, `ApplyNodeRetired` | node-topology events | none (updates `node_inbound_views`) |
| `ApplyPackagingDeclared`, `ApplyPackLevelRetired` | offer-availability events | none (updates `catalog_pack_levels`) |
| `GetInboundPlan`, `GetInboundShipment`, `GetLabels`, `GetSellerScorecard` | `GET` routes, MCP tools, A2A skills | none |

## Seller commands

### OnboardSeller

- **Inputs:** `sellerId` (1 to 64 characters of `A-Z a-z 0-9 . _ -`, it is also the `ownerRef` of the stock), `name`
  (non-blank, trimmed, at most 120 characters).
- **Invariants:** the id is not onboarded yet (`seller-exists`).
- **Result:** an `ACTIVE` seller, `SellerOnboarded` appended to the outbox.

### SuspendSeller and ReinstateSeller

- **Inputs:** `sellerId`; for suspend an optional `reason` (trimmed, at most 200 characters).
- **Invariants:** the seller exists (`not-found`). Suspending a suspended seller, or reinstating an active one, is a
  no-op that raises no event and returns the seller (it is not an `illegal-transition`).
- **Effect while suspended:** plan creation and confirmation, shipment creation, carton adding, labelling and shipping
  are refused (`seller-suspended`). A shipment already `SHIPPED` still closes on the vendor's receipt.
- **Events:** `SellerSuspended`, `SellerReinstated`.

## Inbound plan commands

### CreateInboundPlan

- **Inputs:** `sellerId`, `destinationNodeId`, `packingOption` (`INDIVIDUAL` or `CASE_PACKED`), `lines` (1 to 500;
  each `catalogItemId` once, a valid GTIN of 8, 12, 13 or 14 digits, `qtyEaches` from 1 to 2147483647).
- **Invariants:** seller `ACTIVE`; node able to receive; the lines above.
- **Result:** a `DRAFT` plan at version 1. No event: a draft is private to the seller.
- **Note:** the plan is not checked against the packaging view. Whether a `CASE_PACKED` item has an active CASE level is
  enforced on `AddCarton`, not here.

### ConfirmInboundPlan

- **Invariants:** plan `DRAFT` (`illegal-transition` otherwise); seller still `ACTIVE`; node still able to receive.
- **Events:** `InboundPlanConfirmed` (seller, node, packing option and lines).

### CancelInboundPlan

- **Invariants:** plan `DRAFT`. `CONFIRMED` and `CANCELLED` are terminal. No event is published for a cancellation.

## Shipment commands

### CreateInboundShipment

- **Inputs:** `planId`.
- **Invariants:** plan `CONFIRMED`; seller `ACTIVE`; node able to receive. A confirmed plan may have several shipments.
- **Result:** an empty `DRAFT` shipment (`shp-...`) for the plan's destination node. No event.

### AddCarton

- **Inputs:** `contents` (1 to 100 lines of `catalogItemId`, `gtin`, `qtyEaches`, optional `lot` up to 20 characters and
  ISO `expiry`), `grossWeightG` (at least 1), `dimsMm` (`l`, `w`, `h` at least 1), optional `kind`, optional `sscc`,
  optional `parentSscc`.
- **Invariants (`internal/domain/inboundshipment/carton.go`):**
  - shipment `DRAFT` and fewer than 1000 cartons;
  - SSCC: a supplied one is 18 digits with a valid mod-10 check digit (`invalid-sscc`) and unique in the shipment and in
    `sscc_registry` (`sscc-not-unique`); without one the network issues it from `SSCC_COMPANY_PREFIX` and
    `SSCC_EXTENSION_DIGIT` (re-issued up to `SsccIssueAttempts` times if it collides);
  - `parentSscc` differs from the SSCC, and the node must not forbid pallets (`acceptsPallets=false` gives
    `node-cannot-receive`);
  - kind: a `CASE_PACKED` plan makes every carton `HOMOGENEOUS_CASE` with exactly one item (`case-pack-not-uniform` if
    asked `MIXED` or more than one item); an `INDIVIDUAL` plan makes it `MIXED`;
  - node limits: weight at most `maxCartonWeightG` (`carton-overweight`), size fits `maxCartonDimsMm` in some
    orientation (`carton-oversize`); a limit the node never declared is not checked;
  - plan fit: each item is on the plan and the total over this shipment and the plan's other shipments does not exceed
    the plan line (`carton-contents-mismatch-plan`);
  - case-packed: the item is packed at one quantity per carton (`case-pack-not-uniform`), and that quantity equals the
    `unitsPerPack` of an active CASE level with this GTIN in `catalog_pack_levels` (`case-qty-mismatches-gtin`).
- **Result:** the carton, `ssccIssued`, and its label data. No event: cartons are published with the shipment when it is
  labelled.

### LabelShipment

- **Invariants:** shipment `DRAFT` with at least one carton; seller `ACTIVE`; node able to receive. If the plan has only
  this one shipment, its cartons must add up to every plan line exactly (`carton-contents-mismatch-plan`).
- **Effect:** `LABELLED`, every carton sealed, contents frozen.
- **Events:** `InboundShipmentLabelled` carrying the whole carton manifest.

### ShipInboundShipment

- **Invariants:** shipment `LABELLED` (`DRAFT` gives `shipment-not-labelled`, any later state `illegal-transition`);
  seller `ACTIVE`; node still able to receive.
- **Events:** `InboundShipmentShipped`. Every ship attempt, successful or not, increments the counter
  `seller_inbound.shipment.shipped` ([Observability](/docs/operations/observability)).

## Consumed events

### ApplyInboundReceipt (policy ReceiveOnVendorReceipt)

- **Trigger:** `InboundReceiptSubmitted` from vendor-connectivity.
- **Invariants (`Shipment.RecordReceipt`):** the shipment exists (otherwise `not found`, dead-lettered at once); it is
  `SHIPPED` or `RECEIVING` (a `CLOSED` or `CLOSED_WITH_DISCREPANCY` shipment ignores the receipt as a no-op); the
  receipt's `nodeId` is the shipment's node; every carton has a valid SSCC, a status in `RECEIVED`, `MISSING`,
  `UNEXPECTED`, `DAMAGED`, and appears once.
- **Effect:** the first answer for a carton is final. A `MISSING`, `UNEXPECTED` or `DAMAGED` carton and every quantity
  difference per GTIN (`Short`, `Over`) become an `InboundDiscrepancyRecorded`. A carton the manifest does not list is
  recorded once as `UNEXPECTED` with the code `UNMANIFESTED_CARTON`. Each distinct compliance code of a carton becomes a
  `SellerComplianceViolationRecorded`. When every manifest carton has an answer the shipment closes (`CLOSED`, or
  `CLOSED_WITH_DISCREPANCY` when anything differed) and `InboundShipmentReceived` is raised; until then it is
  `RECEIVING`.
- **Same transaction:** the shipment, the outbox rows, the scorecard facts (`seller_scorecard_receipts` and
  `seller_scorecard_occurrences`) and the `processed_events` claim.
- **Errors:** none are returned to a caller. A transient database error is retried; a permanent one (validation,
  illegal transition, unknown shipment) is dead-lettered ([Troubleshooting](/docs/operations/troubleshooting)).

### Node and packaging views

`ApplyNodeRegistered`, `ApplyNodeProfileDeclared`, `ApplyNodeActivated`, `ApplyNodeSuspended`, `ApplyNodeRetired` fold
node-topology events into `node_inbound_views`; `ApplyPackagingDeclared` and `ApplyPackLevelRetired` fold
offer-availability events into `catalog_pack_levels`. They tolerate duplicates and out-of-order delivery: a status
already seen is kept when `NodeRegistered` arrives late, a `RETIRED` node stays retired, only a profile revision newer
than the one held replaces the limits, and a retired GTIN is never reactivated. Each is claimed in `processed_events`
by the CloudEvents id.

## Queries

| Query | Reads | Notes |
| --- | --- | --- |
| `GetInboundPlan` | `inbound_plans` | `not-found` for an unknown plan. |
| `GetInboundShipment` | `inbound_shipments` and cartons | State, cartons, per-carton receipt status. |
| `GetLabels` | the shipment's cartons | One label per carton: `LBL-<sscc>`, GS1-128 element string `(00)<sscc>`, a human-readable line `(00) E PPPP PPPP SSSS SSSS C`, contents summary in eaches. Available in any state. |
| `GetSellerScorecard` | `seller_scorecard_receipts`, `seller_scorecard_occurrences` | `window` `7d` or `30d` (default `30d`, anything else `invalid-window`); unknown seller `not-found`; a suspended seller has a scorecard. A pure read: it raises nothing and never suspends the seller. |
