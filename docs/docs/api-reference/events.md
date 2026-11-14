---
id: events
title: Event catalogue
sidebar_label: Events
sidebar_position: 2
description: "The CloudEvents seller-inbound publishes and consumes: types, topics, keys, trigger, consumers and delivery rules."
---

# Event catalogue

The contract is `apis/asyncapi.yaml` (AsyncAPI 3.0.0). Every message is CloudEvents 1.0 in structured mode, built and
decoded only in `internal/adapters/kafka/cloudevents`. Field-level payloads are on [Domain events](/docs/ddd/domain-events).

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID minted once, when the event is encoded into the outbox; a relay retry republishes the same id |
| `source` | `/retailnetwork/seller-inbound` |
| `type` | `com.retailnetwork.partner.seller-inbound.<entity>.<EventName>`; entity `seller`, `inboundplan`, `inboundshipment` or `placementplan` |
| `subject` | the aggregate root id: `sellerId`, `planId` or `shipmentId` (for `SellerComplianceViolationRecorded`, the `sellerId`) |
| `time` | the instant the fact became true |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:seller-inbound:<stream>:<EventName>:v1`, stream `events` or `analytics` |
| Kafka key | the `subject`, so one aggregate's events stay ordered on one partition |
| Kafka header | `content-type: application/cloudevents+json; charset=UTF-8` |

Source: `internal/adapters/kafka/cloudevents/cloudevents.go`, `internal/adapters/outbound/kafka/encoder.go`.

## Published

Each event is appended to `outbox_events` in the same Postgres transaction as the change that raised it, then relayed by
the in-process outbox relay. Delivery is at-least-once; consumers dedupe on `id`.

| Event | Full `type` | Topic | Raised by | Consumers |
| --- | --- | --- | --- | --- |
| `SellerOnboarded` | `com.retailnetwork.partner.seller-inbound.seller.SellerOnboarded` | `retailnetwork.seller-inbound.analytics` | `OnboardSeller` | none in the organisation |
| `SellerSuspended` | `com.retailnetwork.partner.seller-inbound.seller.SellerSuspended` | `retailnetwork.seller-inbound.events` | `SuspendSeller` (not when already suspended) | offer-availability (suppresses the seller's listings, ADR 0013) |
| `SellerReinstated` | `com.retailnetwork.partner.seller-inbound.seller.SellerReinstated` | `retailnetwork.seller-inbound.events` | `ReinstateSeller` (not when already active) | offer-availability (restores the seller's listings, ADR 0013) |
| `InboundPlanConfirmed` | `com.retailnetwork.partner.seller-inbound.inboundplan.InboundPlanConfirmed` | `retailnetwork.seller-inbound.analytics` | `ConfirmInboundPlan` | none |
| `InboundShipmentLabelled` | `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentLabelled` | `retailnetwork.seller-inbound.events` | `LabelShipment` | vendor-connectivity |
| `InboundShipmentShipped` | `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped` | `retailnetwork.seller-inbound.events` | `ShipInboundShipment` | vendor-connectivity, node-performance |
| `InboundShipmentReceived` | `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentReceived` | `retailnetwork.seller-inbound.events` | `ApplyInboundReceipt`, once, when the shipment closes | vendor-connectivity; offer-availability (`received`: creates owner-scoped listings, gap G51, ADR 0014) |
| `InboundDiscrepancyRecorded` | `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundDiscrepancyRecorded` | `retailnetwork.seller-inbound.analytics` | `ApplyInboundReceipt`, per discrepancy, also while the shipment is `RECEIVING` | none |
| `SellerComplianceViolationRecorded` | `com.retailnetwork.partner.seller-inbound.seller.SellerComplianceViolationRecorded` | `retailnetwork.seller-inbound.analytics` | `ApplyInboundReceipt`, per distinct compliance code of a carton | none |
| `PlacementPlanSuggested` | `com.retailnetwork.partner.seller-inbound.placementplan.PlacementPlanSuggested` | `retailnetwork.seller-inbound.events` | placement advisory compute ([ADR 0012](/docs/adr/0012-the-placement-advisory)) | none evidenced (analytics-only in v1) |

The routing is `streamFor` in `encoder.go`: the three shipment events, the two seller status events (ADR 0013)
other contexts consume, and `PlacementPlanSuggested` (kept on the events topic so a consumer can subscribe later without a topic change) go to the events topic; every other fact goes to the analytics topic. Creating or cancelling a plan, creating a shipment and adding cartons
publish nothing: a draft is private to the seller and the cartons travel with `InboundShipmentLabelled`.

Value sets: the shipment `status` of `InboundShipmentReceived` is `CLOSED` or `CLOSED_WITH_DISCREPANCY`; the carton
`status` is `RECEIVED`, `MISSING`, `UNEXPECTED` or `DAMAGED`; the discrepancy `kind` is one of `MissingCarton`,
`UnexpectedCarton`, `Damaged`, `Short`, `Over` (`internal/domain/inboundshipment/receipt.go`); the compliance `code` is
passed through from the vendor's receipt, plus the context's own `UNMANIFESTED_CARTON`.

`received` of `InboundShipmentReceived` (ADR 0014) lists `{catalogItemId?, gtin, qtyEaches}` per `gtin`, sorted by
`gtin`, over the manifest cartons answered `RECEIVED` (`MISSING`, `DAMAGED` and `UNEXPECTED` cartons do not count), from
the vendor's counted quantities where the receipt reported them and from the manifest otherwise. `gtin` is the
EACH-level GTIN when `CatalogPackagingView` resolves the carton GTIN to it, otherwise the carton GTIN; `catalogItemId`
is omitted when the view does not resolve it; zero lines are omitted; the array is always present.

## Consumed

One consumer per topic, each under a mandatory consumer group; dispatch is on the full `type`; effects are idempotent on
the CloudEvents `id` (`processed_events`).

| Producer | Topic | Full `type` | Effect |
| --- | --- | --- | --- |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | Creates the node in `node_inbound_views`. |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared` | Replaces the inbound limits if the revision is newer. |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeActivated` | Status `ACTIVE`. |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeSuspended` | Status `SUSPENDED`. |
| node-topology | `retailnetwork.node-topology.events` | `com.retailnetwork.logistics.node-topology.node.NodeRetired` | Status `RETIRED` (terminal). |
| offer-availability | `retailnetwork.offer-availability.events` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemPackagingDeclared` | Upserts the pack level. |
| offer-availability | `retailnetwork.offer-availability.events` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemPackLevelRetired` | Marks the level inactive. |
| vendor-connectivity | `retailnetwork.vendor-connectivity.events` | `com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted` | Records the receipt on the shipment. |
| offer-availability | `retailnetwork.offer-availability.events` | `com.retailnetwork.commerce.offer-availability.listing.ListingAvailabilityChanged` | Upserts the latest availability per item and node (`listing_availability`, availability half of the placement `DemandHeatView`, ADR 0012). |
| order-sourcing | `retailnetwork.order-sourcing.events` | `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentReleasedToNode` | Handler defined (`SourcingHandlers`), **no consumer started by `cmd/api`**: would fold the released lines into `demand_heat_lines` (ADR 0012). |
| order-sourcing | `retailnetwork.order-sourcing.events` | `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentCancelled` | Handler defined, **no consumer started by `cmd/api`**: would delete the assignment group's `demand_heat_lines`. |

The contract lists eleven consumed types; the first nine rows above are read by the three consumers `cmd/api` starts, the
last two are not (see [Integration](/docs/ecosystem/integration#discrepancies-found)).

Failure handling (retry, dead letters, poison messages) is on [Integration](/docs/ecosystem/integration#upstream-what-seller-inbound-consumes).

## Rules for changing the catalogue

Per `CLAUDE.md`: a new event is a domain struct, an entry in `apis/asyncapi.yaml` and the `type:` line in
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue), in the same commit. The fleet catalogue test
(`TestEventCatalogueMatchesContract`) matches `com.warehouse.(wms|wes)` and `com.retailnetwork` types, so it fails the build
if one of this repository's published `com.retailnetwork.partner.seller-inbound` types is declared in the AsyncAPI file but
missing from ADR 0004. CI `api-lint` runs Spectral on the AsyncAPI file.
