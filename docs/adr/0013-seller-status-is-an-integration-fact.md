# ADR 0013: Seller status is an integration fact

## Status

Accepted (2026-11-08).

## Context

Network gap G48: a suspended seller's listings stay live, because
offer-availability never learns of the suspension. seller-inbound already
raises `SellerSuspended` and `SellerReinstated` (ADR 0002, `SuspendSeller` /
`ReinstateSeller`), but publishes them ONLY on
`retailnetwork.seller-inbound.analytics`, the analytics-only topic (ADR 0004).
No sibling context can rely on an analytics-only fact, so the seller's status
never reaches the context that decides whether its listings are offered.

## Decision

1. Both events are published ONLY on `retailnetwork.seller-inbound.events` from
   now on. The estate rule is that integration events go only to `.events` and
   analytics-only events only to `.analytics`, so they leave the analytics
   topic. Type strings are unchanged:
   `com.retailnetwork.partner.seller-inbound.seller.SellerSuspended` and
   `com.retailnetwork.partner.seller-inbound.seller.SellerReinstated`.
   `dataschema` becomes `urn:retailnetwork:seller-inbound:events:SellerSuspended:v1`
   / `urn:retailnetwork:seller-inbound:events:SellerReinstated:v1`.
   `subject` = Kafka key = `sellerId`.
2. Data fields unchanged. Each payload carries at least `sellerId` plus the
   instant (`suspendedAt` / `reinstatedAt`, RFC 3339). Both instants were
   already required fields, so no field is added. The optional `reason` of
   `SellerSuspended` is kept (omitted when empty). The exact fields:
   - `SellerSuspended`: `sellerId` (required), `suspendedAt` (required,
     RFC 3339 UTC), `reason` (optional, omitted when empty).
   - `SellerReinstated`: `sellerId` (required), `reinstatedAt` (required,
     RFC 3339 UTC).

   `apis/asyncapi.yaml` documents both on the events channel with
   `Consumers: offer-availability` in the message description and lists the
   exact fields.
3. CloudEvents type-catalogue ADR (0004): both rows move to stream `events`
   (table row and `type: ... stream: events` lines).
4. The encoder's routing (`streamFor` in
   `internal/adapters/outbound/kafka/encoder.go`) sends both events to the
   events topic; the golden encoder tests pin the new topic and dataschema.
5. MCP and REST are unaffected: neither exposes stream or topic names.

## Consequences

- offer-availability consumes both events and suppresses and restores the
  seller's listings; that decision, its read model and its inbox are recorded
  in offer-availability's own ADR.
- The analytics stream loses the two events; it keeps `SellerOnboarded`,
  `InboundPlanConfirmed`, `InboundDiscrepancyRecorded` and
  `SellerComplianceViolationRecorded`. Nothing consumes the analytics topic
  today: `git grep -n 'seller-inbound.analytics'` across this repo finds only
  the producer (`TopicAnalytics` in the encoder), the end-to-end test that
  reads back what this service itself produced, the AsyncAPI channel and the
  documentation; no consumer, chart subscription or consumer group names it.
- The events topic now carries seller facts keyed by `sellerId` next to the
  shipment facts keyed by `shipmentId`; consumers already dispatch on the full
  `type` and ignore types they do not act on, so existing consumers of the
  events topic (vendor-connectivity, node-performance) are unaffected.
- `SellerOnboarded` stays analytics-only; a consumer that needs it is a new
  decision.
