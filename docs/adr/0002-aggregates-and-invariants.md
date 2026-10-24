# 2. SellerAccount, InboundPlan and InboundShipment: aggregates, invariants and the assumptions the model left open

## Status

Accepted (2026-10-24).

## Context

The model (`network/model/model.yaml`, context `seller-inbound`) names three aggregates, their invariants, errors, events and policies. It does not say how an id is minted, what a repeated command answers, how a network-issued SSCC is built, or which bounds apply. This ADR records what the code does and the assumptions it rests on. The assumptions are numbered; code and docs cite them as `ADR 0002 A<n>`.

## Decision

### Aggregates (all in `internal/domain`, no import outside `internal/domain`)

- **SellerAccount** (`seller`): `ACTIVE` <-> `SUSPENDED`. A suspended seller creates no plan, shipment or carton and ships nothing (`ErrSellerSuspended`). Events `SellerOnboarded`, `SellerSuspended`, `SellerReinstated`.
- **InboundPlan** (`inboundplan`): `DRAFT` -> `CONFIRMED` | `CANCELLED`. Lines are `{catalogItemId, gtin, qtyEaches}`, 1 to 500, one line per catalogue item; `packingOption` is `INDIVIDUAL` or `CASE_PACKED`. Event `InboundPlanConfirmed`.
- **InboundShipment** with **Carton** entities (`inboundshipment`): `DRAFT` -> `LABELLED` -> `SHIPPED` -> `RECEIVING` -> `CLOSED` | `CLOSED_WITH_DISCREPANCY`. Events `InboundShipmentLabelled`, `InboundShipmentShipped`, `InboundShipmentReceived`, `InboundDiscrepancyRecorded`, `SellerComplianceViolationRecorded`.
- `network` holds the two read-model value types (`Node`, `PackLevel`) and `ErrNodeCannotReceive`; `shared` holds `Event`, `ErrValidation`, `ErrIllegalTransition` and the GS1 value objects (`shared/gs1`: GTIN, SSCC, mod-10 check digit).

Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` and an optimistic `Version()`; `now` and ids are passed in. `ErrNotFound` and `ErrConcurrentUpdate` are port errors (`internal/application/ports/errors.go`).

### Invariants and their errors

| Error | Rule | Problem slug |
| --- | --- | --- |
| `ErrCasePackNotUniform` | A `HOMOGENEOUS_CASE` carton holds exactly one item, one GTIN and one quantity | `case-pack-not-uniform` 422 |
| `ErrCaseQtyMismatchesGtin` | The quantity of a case carton equals the `unitsPerPack` of the active CASE level of the item | `case-qty-mismatches-gtin` 422 |
| `ErrCartonOverweight` | Gross weight at most the node's `maxCartonWeightG` (when declared) | `carton-overweight` 422 |
| `ErrCartonOversize` | Every edge fits the node's `maxCartonDimsMm` (when declared), sorted longest first | `carton-oversize` 422 |
| `ErrSsccNotUnique` | An SSCC is unique network-wide (the `sscc_registry` primary key) | `sscc-not-unique` 409 |
| `ErrInvalidSscc` | An SSCC has 18 digits and a valid GS1 mod-10 check digit | `invalid-sscc` 422 |
| `ErrCartonContentsMismatchPlan` | At labelling, the eaches per item and GTIN over all cartons equal the confirmed plan | `carton-contents-mismatch-plan` 422 |
| `ErrNodeCannotReceive` | The node is known, `ACTIVE`, has the INBOUND capability (fulfillment centre or inbound cross-dock) and accepts pallets when a carton has a parent SSCC | `node-cannot-receive` 409 |
| `ErrSellerSuspended` | See SellerAccount | `seller-suspended` 409 |
| `ErrShipmentNotLabelled` | A shipment ships only when `LABELLED` | `shipment-not-labelled` 409 |

The shared slugs are `not-found` 404, `illegal-transition` 409, `concurrent-update` 409 and `validation-failed` 422. The table lives in `problemCatalogue` (`internal/adapters/inbound/http/errors.go`); an unmapped error is an opaque 500 `internal-error`.

### Assumptions

- **A1 Ids.** `sellerId` is chosen by the caller (`shared.ValidID`: 1 to 64 characters, alphanumeric first, then `.`, `_`, `:`, `-`). `planId` and `shipmentId` are minted by the service as `pln-<uuid>` and `shp-<uuid>`. Cartons are identified by their SSCC.
- **A2 Eaches.** Every quantity (plan line, carton content, receipt item, event payload) is in eaches. Case counts are derived from the catalogue's `unitsPerPack`, never stored.
- **A3 Repeated commands.** Only the seller account is idempotent by state: suspending a suspended seller or reinstating an active one answers the account unchanged with `200` and publishes no event. Every other command is strict: a transition the state forbids is `illegal-transition` 409 (confirm of a confirmed or cancelled plan, cancel of a confirmed plan, label of a labelled shipment, ship of a shipped one, a carton added to a labelled shipment), so a retry of a half-completed flow is told what state it is in; the REST `Idempotency-Key` replays the first response of a request that did succeed. Onboarding an existing `sellerId` is `seller-exists` 409.
- **A4 Order-tolerant views.** The local read models are rebuilt from events that may arrive twice or out of order: a profile for an unregistered node creates the row with its limits and neither type nor status (so it cannot receive yet); a later registration fills the type and keeps a status already seen; only a profile revision newer than the one held replaces the limits; `RETIRED` is terminal and a later activation does not revive it. `CatalogPackagingView` keys on item, level and GTIN; a retire before a declare stores a retired row.
- **A5 SSCC issuance.** When a carton carries no SSCC the network issues one: extension digit, then the company prefix (`SSCC_COMPANY_PREFIX`, mandatory, 6 to 11 digits, validated at boot), then the serial from the Postgres sequence `sscc_serial_seq` zero-padded to fill 16 digits, then the check digit. The extension digit is `SSCC_EXTENSION_DIGIT`, default `0`. A seller-supplied SSCC is accepted only when valid and unique; an issued SSCC that collides with a seller-supplied one is re-issued (up to 5 attempts, then an error). The response says which (`ssccIssued`).
- **A6 Bounds.** At most 500 plan lines, 1000 cartons per shipment, 100 content lines per carton, lot at most 20 characters (GS1 AI 10), expiry an ISO date; weight, dimensions and quantities at most the Postgres integer ceiling.
- **A7 Integer ceiling.** `shared.MaxInt32` (2147483647) bounds every integer the domain accepts, so no value reaches Postgres as an overflow.
- **A8 Receipt policy.** The vendor's receipt (`InboundReceiptSubmitted`) is applied per carton, first answer final. Statuses `RECEIVED`, `MISSING`, `UNEXPECTED`, `DAMAGED`. For each manifest carton: `MISSING` records `MissingCarton`; `DAMAGED` records `Damaged`; a counted quantity below the manifest records `Short`, above records `Over`. A carton the manifest does not list is recorded as `UnexpectedCarton` and raises the compliance code `UNMANIFESTED_CARTON`. Every compliance code the vendor reports on a carton raises one `SellerComplianceViolationRecorded` per distinct code. The manifest is never edited. The shipment is `RECEIVING` after a partial receipt and closes when every manifest carton has an answer: `CLOSED` when nothing differed, otherwise `CLOSED_WITH_DISCREPANCY`, publishing `InboundShipmentReceived` once. A receipt for a closed shipment, or the same event twice, changes nothing.
- **A9 Absent limits.** A node that declared no inbound limits receives any carton; a limit is checked only when declared. `requiresSscc` does not change anything: every carton always has an SSCC.
- **A10 Plan fit at labelling.** A plan may be filled by several shipments over time; the plan check at labelling compares the cartons of THIS shipment with the plan lines (every line exactly, no extra item).
- **A11 Event routing.** The three events other contexts consume (`InboundShipmentLabelled`, `InboundShipmentShipped`, `InboundShipmentReceived`) go to the events topic; the other six, which no context consumes, go to the analytics topic (ADR 0004).
- **A12 Retention.** Idempotency keys older than 24 h are purged by a daily sweep. Aggregates are kept.

## Consequences

- Handlers hold no business logic; a new rule lands in the aggregate with a table test and a boundary test, a new error in `problemCatalogue`, and a changed number here is a new ADR that supersedes this one.
- The label data (SSCC, GS1-128 element string `(00)<sscc>`, human readable line, contents in eaches, destination node) is derived on read and never stored.
