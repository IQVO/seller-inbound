# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: warehouse-docs `network/model/model.yaml` (context `seller-inbound`) and
ADR 0009 of the network. Invariants, transitions and every assumption the model left open
are in `docs/adr/0002-aggregates-and-invariants.md` (assumptions A1-A12).

## Ubiquitous Language (use these exact names)

- **Seller / SellerAccount** - a company that sends stock into the network; identified by
  `sellerId`. `ACTIVE` or `SUSPENDED`. The seller is the owner of record of the stock it sends.
- **Inbound plan** - what a seller will send to ONE destination node, in eaches: lines of
  `{catalogItemId, gtin, qtyEaches}` and a `packingOption` (`INDIVIDUAL` or `CASE_PACKED`).
  `DRAFT`, `CONFIRMED`, `CANCELLED`.
- **Inbound shipment** - one physical consignment of a confirmed plan. `DRAFT`, `LABELLED`,
  `SHIPPED`, `RECEIVING`, `CLOSED`, `CLOSED_WITH_DISCREPANCY`.
- **Carton** - a logistic unit of a shipment identified by an SSCC (`HOMOGENEOUS_CASE` or
  `MIXED`), with contents in eaches, a gross weight and outer dimensions.
- **SSCC / GTIN** - GS1 identifiers (18 and 14 digits, mod-10 check digit); value objects in
  `internal/domain/shared/gs1`. The seller may supply an SSCC; otherwise the network issues one
  from its own GS1 company prefix.
- **Label data** - SSCC, the GS1-128 element string `(00)<sscc>`, the human readable line, the
  contents summary in eaches and the destination node; derived on read, never stored.
- **Manifest** - the cartons of a LABELLED shipment; frozen, never edited to match the dock.
- **Receipt / discrepancy / compliance violation** - the vendor's carton-level answer, and the
  facts recorded from it (`MissingCarton`, `UnexpectedCarton`, `Damaged`, `Short`, `Over`;
  compliance codes such as `UNMANIFESTED_CARTON`).
- **NodeInboundView / CatalogPackagingView** - local read models fed by node-topology and
  offer-availability events (never read live).

## Aggregates

- **SellerAccount** (`internal/domain/seller`): `ACTIVE` <-> `SUSPENDED`; a suspended seller
  creates no plan, shipment or carton and ships nothing (`ErrSellerSuspended`).
- **InboundPlan** (`internal/domain/inboundplan`): `DRAFT` -> `CONFIRMED` | `CANCELLED`; 1 to 500
  lines, one per item and GTIN.
- **InboundShipment** + **Carton** (`internal/domain/inboundshipment`, the mutation focus): all
  the carton invariants (`ErrCasePackNotUniform`, `ErrCaseQtyMismatchesGtin`, `ErrCartonOverweight`,
  `ErrCartonOversize`, `ErrSsccNotUnique`, `ErrInvalidSscc`, `ErrCartonContentsMismatchPlan`,
  `ErrShipmentNotLabelled`) and `RecordReceipt`.
- `internal/domain/network`: the `Node` and `PackLevel` value types of the read models and
  `ErrNodeCannotReceive`. `internal/domain/shared`: `Event`, `ErrValidation`,
  `ErrIllegalTransition`, `MaxInt32`.

Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` and an optimistic
`Version()`; `now` and ids are passed in. `ErrNotFound` and `ErrConcurrentUpdate` are port
errors (`internal/application/ports/errors.go`). The domain imports nothing outside
`internal/domain`.

## Domain events

Nine events; wire shape in `apis/asyncapi.yaml`. `InboundShipmentLabelled`,
`InboundShipmentShipped` and `InboundShipmentReceived` go to `retailnetwork.seller-inbound.events`,
the other six to `retailnetwork.seller-inbound.analytics` (ADR 0004):

- `SellerOnboarded`, `SellerSuspended`, `SellerReinstated` (`internal/domain/seller/events.go`).
- `InboundPlanConfirmed` (`internal/domain/inboundplan/events.go`).
- `InboundShipmentLabelled`, `InboundShipmentShipped`, `InboundShipmentReceived`,
  `InboundDiscrepancyRecorded`, `SellerComplianceViolationRecorded`
  (`internal/domain/inboundshipment/events.go`).

## Key use cases (`internal/application/usecases`)

One method of `Service` per command and query; commands run in ONE `ports.UnitOfWork` (load,
domain call, save, outbox append).

- Seller commands (`sellers.go`): `OnboardSeller`, `SuspendSeller`, `ReinstateSeller`.
- Plans (`plans.go`): `CreateInboundPlan`, `ConfirmInboundPlan`, `CancelInboundPlan`, `GetInboundPlan`.
- Shipments (`shipments.go`): `CreateInboundShipment`, `AddCarton`, `LabelShipment`,
  `ShipInboundShipment`, `GetInboundShipment`, `GetLabels`.
- Consumer policies (`consumers.go`): `ApplyNodeRegistered`, `ApplyNodeProfileDeclared`,
  `ApplyNodeActivated`/`Suspended`/`Retired`, `ApplyPackagingDeclared`, `ApplyPackLevelRetired`,
  `ApplyInboundReceipt` (policy `ReceiveOnVendorReceipt`). Read DTOs: `internal/application/views`.
- Ports (`internal/application/ports/ports.go`) are interfaces only; the memory test doubles in
  `internal/adapters/outbound/memory` back the unit tests and the godog scenarios, the Postgres
  adapters are proven by `internal/adapters/outbound/postgres/repositories_integration_test.go`.
