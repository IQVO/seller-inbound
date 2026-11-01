---
id: domain-events
title: Domain Events
sidebar_label: Domain Events
sidebar_position: 7
description: "The nine events Seller Inbound publishes and the eight it consumes, with payloads, an example envelope and idempotency rules."
---

# Domain Events

Every event this context publishes and consumes. The contract is `apis/asyncapi.yaml`; the payload structs the service
serialises are in `internal/adapters/outbound/kafka/encoder.go`. The envelope, topic routing and consumed-event effects are
on the [Event catalogue](/docs/api-reference/events); the decision record is
[ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue). Quantities are eaches. `sellerId` here is `ownerRef`
on the Vendor API. All timestamps are RFC 3339 UTC strings with nanosecond precision where non-zero.

## Published payloads

| Event | Fields (JSON, from `encoder.go`) |
| --- | --- |
| `SellerOnboarded` | `sellerId`, `name`, `onboardedAt` |
| `SellerSuspended` | `sellerId`, `suspendedAt`, `reason` (omitted when empty) |
| `SellerReinstated` | `sellerId`, `reinstatedAt` |
| `InboundPlanConfirmed` | `planId`, `sellerId`, `destinationNodeId`, `packingOption`, `lines` (`catalogItemId`, `gtin`, `qtyEaches`), `confirmedAt` |
| `InboundShipmentLabelled` | `shipmentId`, `planId`, `sellerId`, `nodeId`, `cartons` (`sscc`, `parentSscc` omitted when none, `kind`, `contents` (`catalogItemId`, `gtin`, `qtyEaches`, `lot`, `expiry`, the last two omitted when empty), `grossWeightG`, `dimsMm` (`l`, `w`, `h`)), `labelledAt` |
| `InboundShipmentShipped` | `shipmentId`, `planId`, `sellerId`, `nodeId`, `shippedAt` |
| `InboundShipmentReceived` | `shipmentId`, `sellerId`, `nodeId`, `status`, `cartons` (`sscc`, `status`), `receivedAt` |
| `InboundDiscrepancyRecorded` | `shipmentId`, `sellerId`, `nodeId`, `sscc` (omitted when none), `kind`, `expected`, `actual` (both omitted when empty), `recordedAt` |
| `SellerComplianceViolationRecorded` | `sellerId`, `shipmentId` (omitted when none), `code`, `recordedAt` |

Value sets:

- Shipment `status` on `InboundShipmentReceived`: `CLOSED` or `CLOSED_WITH_DISCREPANCY`.
- Carton `status`: `RECEIVED`, `MISSING`, `UNEXPECTED` or `DAMAGED`. Carton `kind`: `HOMOGENEOUS_CASE` or `MIXED`.
- Discrepancy `kind`: `MissingCarton`, `UnexpectedCarton`, `Damaged`, `Short` or `Over`, set by the domain
  (`internal/domain/inboundshipment/receipt.go`). For `Short` and `Over`, `expected` and `actual` hold the manifest and
  counted eaches.
- Violation `code`: passed through from the vendor's receipt, plus the context's own `UNMANIFESTED_CARTON`.

`InboundShipmentReceived` is published once, when the shipment closes. `InboundDiscrepancyRecorded` is published for each
discrepancy as receipts are processed, including while the shipment is still `RECEIVING`.

Nothing is published when a plan is created or cancelled, a shipment is created, a carton is added, or a suspend or
reinstate is a repeat.

## Example envelope

`InboundShipmentLabelled` carries the carton manifest. The values below are illustrative, not captured from a running
service. The SSCC and GTIN carry valid mod-10 check digits.

```json
{
  "specversion": "1.0",
  "id": "3f1a7c52-9d0e-4b6a-8c21-5e7f0a9b1c33",
  "source": "/retailnetwork/seller-inbound",
  "type": "com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentLabelled",
  "subject": "ish-01J9Z7K3M4",
  "time": "2026-10-23T13:10:00Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:retailnetwork:seller-inbound:events:InboundShipmentLabelled:v1",
  "data": {
    "shipmentId": "ish-01J9Z7K3M4",
    "planId": "ipl-01J9Z7H8Q2",
    "sellerId": "seller-acme",
    "nodeId": "N-VWS-AUS3",
    "cartons": [
      {
        "sscc": "000123450000000010",
        "kind": "HOMOGENEOUS_CASE",
        "contents": [
          {"catalogItemId": "ci-1001", "gtin": "00012345678905", "qtyEaches": 12, "lot": "L2209", "expiry": "2028-09-30"}
        ],
        "grossWeightG": 7200,
        "dimsMm": {"l": 400, "w": 300, "h": 250}
      }
    ],
    "labelledAt": "2026-10-23T13:10:00Z"
  }
}
```

The ids in the example (`ish-…`, `ipl-…`) are shaped for readability; the service mints ids as `<prefix>-<uuid>`
(`ids.UUID.NewID`, for example `pln-` for a plan), through `ports.IDGenerator`.

## Consumed

Eight event types from three producers, one consumer per topic, dispatch on the full `type`, effects idempotent on the
CloudEvents `id` through `processed_events` in the same transaction. The table of types and effects is on the
[Event catalogue](/docs/api-reference/events#consumed). Failure handling: a transient failure is retried with capped
backoff and then dead-lettered to `<topic>.dlq`; a payload that can never apply is dead-lettered at once
([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-models), [Troubleshooting](/docs/operations/troubleshooting)).
`CatalogItemCreated` and `CatalogItemDiscontinued` are not consumed, so a plan line's catalogue item is not checked for
being live.
