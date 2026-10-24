# 1. Seller Inbound is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: IQVO/warehouse-docs `network/model/model.yaml`, context `seller-inbound`, and its network ADRs.

## Context

The retail network is modelled as ten bounded contexts, each its own service and database. PROPOSED. The seller-facing front door for stock coming into the network: a seller is onboarded, plans an inbound shipment to a vendor node in eaches (loose or case-packed), packs it into cartons each identified by an SSCC and a printed label, and ships it. The context checks the plan against the destination node's declared inbound limits and the catalogue item's packaging hierarchy, hands the vendor the shipment as a carton manifest through the Vendor API, and closes the shipment from the vendor's carton-level receipt, recording every discrepancy and compliance violation instead of correcting it. It absorbs the idea reserved earlier as the inventory-placement context; automated placement advice is not modelled (see the gap register).

## Decision

- This repo owns the aggregates SellerAccount, InboundPlan, InboundShipment; the invariants and error names are those of the model.
- Business decisions of the context:
- Eaches are the ledger unit: every quantity on a plan, a carton and a receipt is in eaches; pack fields are additive and named cases, unitsPerPack, qtyOfChild.
- A CASE_PACKED carton holds one catalogue item at the quantity of its declared CASE level; a mixed carton is a logistic unit only and has no pack GTIN.
- SSCC rule: a seller-supplied SSCC is accepted only if it is a valid 18-digit code and unique network-wide; otherwise the network issues one from its own GS1 company prefix and returns the label.
- A shipment cannot ship until every carton has a label, and cannot be labelled outside the destination node's declared carton weight and size limits.
- Stock a seller sends is owned by that seller (ownerRef = sellerId) and stays segregated from every other owner's; commingling is not designed.
- A receipt discrepancy or compliance violation is recorded as a fact; the context never edits the manifest to match what arrived.
- Events use the network CloudEvents namespace `com.retailnetwork.partner.seller-inbound.<entity>.<Event>` on `retailnetwork.seller-inbound.events`
  (analytics-only events on `.analytics`). Published: SellerOnboarded, SellerSuspended, SellerReinstated, InboundPlanConfirmed, InboundShipmentLabelled, InboundShipmentShipped, InboundShipmentReceived, InboundDiscrepancyRecorded, SellerComplianceViolationRecorded. No `warehouse.*` topic is consumed or produced.
- No live REST lookup into sibling contexts except the calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet. PII exists only in customer-ordering and shipping-and-tracking.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
