---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

seller-inbound PUBLISHES nine events and CONSUMES eight (ADR 0003, ADR 0004). Published:
`retailnetwork.seller-inbound.events` (the three shipment events other contexts consume) and
`retailnetwork.seller-inbound.analytics` (the six analytics-only events). Consumed:
`retailnetwork.node-topology.events`, `retailnetwork.offer-availability.events`,
`retailnetwork.vendor-connectivity.events`. Never produce or consume a `warehouse.*` topic.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes is a CloudEvents 1.0 event in structured
content mode. This is a hard rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write, no dual-read, no
  envelope toggle env var. `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build and validate the envelope with `github.com/cloudevents/sdk-go/v2/event` through ONE helper
  package, `internal/adapters/kafka/cloudevents`. Transport stays `segmentio/kafka-go`; no
  hand-rolled CloudEvent structs. The only producing caller is the encoder
  `internal/adapters/outbound/kafka/encoder.go`.
- Every message carries the Kafka header `content-type: application/cloudevents+json; charset=UTF-8`.
  Message key = aggregate id (`sellerId`, `planId`, `shipmentId`), `kafkago.Hash{}` balancer
  (`internal/adapters/outbound/kafka/writer.go`).
- Attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per event at outbox append and stored in
  `outbox_events.event_id`, so a redelivery carries the same id); `source=/retailnetwork/seller-inbound`;
  `type`; `subject` (the aggregate id, never empty); `time` (the domain occurred-at, UTC);
  `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:seller-inbound:<stream>:<EventName>:v1`. No custom extension
  attributes without an ADR.
- `type` = `com.retailnetwork.partner.seller-inbound.<entity>.<EventName>` with entity
  `seller`, `inboundplan` or `inboundshipment` (lowercase, no separators). A breaking payload
  change is a new `:v2` dataschema, never a mutation of v1; evolution is additive only.
- Payloads follow the wire conventions of contracts-delta: ids are strings, instants RFC 3339 UTC,
  enums UPPER_SNAKE, quantities always `qtyEaches`, optional fields omitted, arrays always
  present.
- Consumers decode with `cloudevents.Decode` (validates), dispatch on the FULL `type` string, ignore
  unknown types and unknown fields, dedupe on `id`, and skip (WARN, commit past) anything that is
  not a valid CloudEvent without claiming the inbox; they never fall back to a legacy shape
  (`TestUnknownTypesAndForeignMessagesAreIgnored`).
- Tests: a golden exact-JSON test per published type
  (`internal/adapters/outbound/kafka/encoder_test.go`), the catalogue-vs-contract sensor
  (`TestEventCatalogueMatchesContract`), consumer tests in
  `internal/adapters/inbound/kafka/consumers_test.go`, and the Kafka proof against a real broker
  (`internal/adapters/outbound/outbox/relay_integration_test.go`, `cmd/api/e2e_integration_test.go`),
  all via testcontainers.

### Published types

`subject` and Kafka key is the aggregate root id.

| `type` | stream | `subject` |
| --- | --- | --- |
| `com.retailnetwork.partner.seller-inbound.seller.SellerOnboarded` | analytics | sellerId |
| `com.retailnetwork.partner.seller-inbound.seller.SellerSuspended` | analytics | sellerId |
| `com.retailnetwork.partner.seller-inbound.seller.SellerReinstated` | analytics | sellerId |
| `com.retailnetwork.partner.seller-inbound.inboundplan.InboundPlanConfirmed` | analytics | planId |
| `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentLabelled` | events | shipmentId |
| `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped` | events | shipmentId |
| `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentReceived` | events | shipmentId |
| `com.retailnetwork.partner.seller-inbound.inboundshipment.InboundDiscrepancyRecorded` | analytics | shipmentId |
| `com.retailnetwork.partner.seller-inbound.seller.SellerComplianceViolationRecorded` | analytics | sellerId |

### Consumed types

Constants and tolerant payload structs: `internal/adapters/inbound/kafka/consumers.go`. Each
consumer's group comes from its mandatory variable (`NODE_CONSUMER_GROUP`,
`CATALOG_CONSUMER_GROUP`, `RECEIPT_CONSUMER_GROUP`).

| `type` | topic | handler (use case) |
| --- | --- | --- |
| `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | node-topology.events | `ApplyNodeRegistered` |
| `com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared` | node-topology.events | `ApplyNodeProfileDeclared` |
| `com.retailnetwork.logistics.node-topology.node.NodeActivated`, `NodeSuspended`, `NodeRetired` | node-topology.events | `ApplyNodeActivated` / `Suspended` / `Retired` |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemPackagingDeclared`, `CatalogItemPackLevelRetired` | offer-availability.events | `ApplyPackagingDeclared` / `ApplyPackLevelRetired` |
| `com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted` | vendor-connectivity.events | `ApplyInboundReceipt` |
