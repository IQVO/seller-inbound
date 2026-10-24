# 3. Consumed contracts and local read models

## Status

Accepted (2026-10-24).

## Context

`seller-inbound` needs three facts it does not own: whether a node can receive inbound cartons and what limits it declared, what a catalogue item's pack levels are, and what the vendor received. The fleet rule is: no synchronous call to a sibling, no live read of sibling data.

## Decision

The context consumes eight event types from three producers into local state. Each producer's contract is restated in `apis/asyncapi.yaml` and decoded **tolerantly** (unknown fields are ignored, optional fields default); the producers' packages are never imported.

| Producer | Topic | Types consumed | Feeds |
| --- | --- | --- | --- |
| node-topology | `retailnetwork.node-topology.events` | `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired` | `NodeInboundView` (`node_inbound_views`): type, status, profile revision, inbound limits |
| offer-availability | `retailnetwork.offer-availability.events` | `CatalogItemPackagingDeclared`, `CatalogItemPackLevelRetired` | `CatalogPackagingView` (`catalog_pack_levels`): pack levels with GTIN, child GTIN, `unitsPerPack`, active flag |
| vendor-connectivity | `retailnetwork.vendor-connectivity.events` | `InboundReceiptSubmitted` | policy `ReceiveOnVendorReceipt` -> `RecordInboundReceipt` |

Type strings carry each producer's own tier segment and must match byte for byte: node-topology `com.retailnetwork.logistics.node-topology.node.<Name>`, offer-availability `com.retailnetwork.commerce.offer-availability.catalogitem.<Name>`, vendor-connectivity `com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted`.

### Consumer mechanics (`internal/adapters/inbound/kafka`)

- One consumer per topic, each under its own group from a **mandatory** variable: `NODE_CONSUMER_GROUP`, `CATALOG_CONSUMER_GROUP`, `RECEIPT_CONSUMER_GROUP`. No default and no literal group id anywhere.
- Dispatch on the full `type`; an unknown type or a message that is not a CloudEvent is ignored and committed.
- Idempotent on the CloudEvents `id`: the handler claims the id in `processed_events` in the same transaction as its writes; a redelivery is a no-op.
- A transient error (database, lost optimistic race, transient fetch error) retries the same message with capped exponential backoff, then dead-letters it to `<topic>.dlq`. A payload that can never be applied (undecodable, a value the domain refuses, a receipt for a shipment that does not exist) is dead-lettered at once. The fetch loop classifies transient fetch errors and keeps going; the readers use `MaxWait` 500 ms.
- If a consumer returns on its own the process exits non-zero (`watchWorkers`).

### Relationships

- node-topology and offer-availability: Conformist. The context takes their events as published.
- vendor-connectivity: Customer/Supplier. vendor-connectivity serves `InboundShipmentShipped`/`InboundShipmentLabelled` to vendors as the carton manifest and sends back the receipt.
- No synchronous call to any sibling. The service has no outbound HTTP client.

## Consequences

- A destination node or a pack level the views have not seen yet is refused as `node-cannot-receive` or `case-qty-mismatches-gtin`; the caller retries after the events arrive.
- A change in a producer's payload that removes a field this context relies on fails loudly in its dead-letter topic, not silently.
