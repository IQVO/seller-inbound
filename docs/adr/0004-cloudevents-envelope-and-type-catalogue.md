# 4. CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-24).

## Context

Every Kafka message of the retail network is a CloudEvents 1.0 event in structured content mode. The wire shapes are in `apis/asyncapi.yaml`; this ADR records the catalogue and the routing.

## Decision

- Envelope: `specversion` 1.0; `id` (UUID minted once per domain event and stored with the outbox row, so a retry carries the same id); `source` `/retailnetwork/seller-inbound`; `type` `com.retailnetwork.partner.seller-inbound.<entity>.<EventName>`; `subject` the aggregate root id; `time` the domain instant in UTC; `datacontenttype` `application/json`; `dataschema` `urn:retailnetwork:seller-inbound:<stream>:<EventName>:v1`. Kafka header `content-type: application/cloudevents+json; charset=UTF-8`; key = subject; `traceparent` (and `tracestate`/`baggage` when present) stay in headers.
- `data` is camelCase, ids are strings, timestamps RFC 3339 UTC, quantities always `qtyEaches`; optional fields are omitted when absent, arrays are always present.
- Only `internal/adapters/outbound/kafka/encoder.go` builds an envelope (through `internal/adapters/kafka/cloudevents`); golden envelopes in `encoder_test.go` pin every field.

| Event | Entity | Subject | Topic |
| --- | --- | --- | --- |
| `InboundShipmentLabelled` | `inboundshipment` | `shipmentId` | `retailnetwork.seller-inbound.events` |
| `InboundShipmentShipped` | `inboundshipment` | `shipmentId` | `retailnetwork.seller-inbound.events` |
| `InboundShipmentReceived` | `inboundshipment` | `shipmentId` | `retailnetwork.seller-inbound.events` |
| `SellerOnboarded`, `SellerSuspended`, `SellerReinstated` | `seller` | `sellerId` | `retailnetwork.seller-inbound.analytics` |
| `InboundPlanConfirmed` | `inboundplan` | `planId` | `retailnetwork.seller-inbound.analytics` |
| `InboundDiscrepancyRecorded` | `inboundshipment` | `shipmentId` | `retailnetwork.seller-inbound.analytics` |
| `SellerComplianceViolationRecorded` | `seller` | `sellerId` | `retailnetwork.seller-inbound.analytics` |

```text
type: com.retailnetwork.partner.seller-inbound.seller.SellerOnboarded stream: analytics
type: com.retailnetwork.partner.seller-inbound.seller.SellerSuspended stream: analytics
type: com.retailnetwork.partner.seller-inbound.seller.SellerReinstated stream: analytics
type: com.retailnetwork.partner.seller-inbound.inboundplan.InboundPlanConfirmed stream: analytics
type: com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentLabelled stream: events
type: com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentShipped stream: events
type: com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentReceived stream: events
type: com.retailnetwork.partner.seller-inbound.inboundshipment.InboundDiscrepancyRecorded stream: analytics
type: com.retailnetwork.partner.seller-inbound.seller.SellerComplianceViolationRecorded stream: analytics
```

Delivery is a transactional outbox: the aggregate row, the idempotency row and the encoded events commit in ONE Postgres transaction and an always-on relay drains `outbox_events` to Kafka, at-least-once, in id order, with an attempt cap (`OUTBOX_MAX_ATTEMPTS`) after which a poison row is excluded and left for inspection.

## Consequences

- A consumer dedupes on `id` and dispatches on the full `type`.
- Adding an event type needs: the domain event, an encoder case with a golden test, a row here and in the AsyncAPI, and a decision which topic it belongs to.
