---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
sidebar_position: 2
description: "Why seller-inbound is a Supporting subdomain of the retail network, and how its neighbours are classified."
---

# Subdomain classification

**Seller Inbound is a Supporting subdomain**: bounded context `seller-inbound`, CloudEvents tier `partner`, business
model *supply enabler*, evolution *custom-built*. Its place on the Core Domain Chart is business differentiation 0.34,
model complexity 0.48 ([Core Domain Chart](/docs/ddd/core-domain-chart)).

## Reasoning

- **Low differentiation.** The rules are the common practice of case-packed receiving: identify every carton, check it
  against the plan, count what arrives. Nothing in this context decides a promise or a route, which is what sets the
  network apart. It enables supply; it does not differentiate.
- **Moderate complexity.** Three aggregates (one with a child entity and a six-state machine), eight consumed event types
  from three producers, and invariants that depend on two local read models. The hard part is validating a carton set
  against a plan and a packaging hierarchy, not the lifecycle.
- **Not Generic.** Buying a seller-onboarding product would still leave the carton, SSCC and receipt-reconciliation
  rules to write, and they are specific to this network's model (`ownerRef`, the node's inbound limits).
- **Why a context of its own.** It is the seller's contract, not the vendor's (vendor-connectivity is the vendor
  protocol); it owns facts nobody else should (SSCC uniqueness network-wide, plan-to-carton reconciliation); and it keeps
  the seam narrow: sellers reach this context, vendors reach vendor-connectivity, and the two meet only through events.
- **What would move it.** If seller compliance became a scored, enforced programme, the context would grow an Enforcer
  role and move up. Today the scorecard is informational ([ADR 0005](/docs/adr/0005-seller-compliance-scorecard)).

## Neighbours

Copied from the retail network's classification
([Subdomain Classification](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification),
`origin/main` of IQVO/warehouse-docs), with the relation to this context:

| Bounded context | Classification | Tier | Relation to seller-inbound |
| --- | --- | --- | --- |
| `node-topology` | Supporting | `logistics` | Upstream: node status, type and inbound limits (Conformist). |
| `offer-availability` | Supporting | `commerce` | Upstream: packaging hierarchy and GTINs (Conformist). |
| `vendor-connectivity` | Supporting | `partner` | Upstream for the receipt, downstream for the manifest and closing status (Customer/Supplier both ways, events only). |
| `node-performance` | Supporting | `partner` | Downstream: consumes `InboundShipmentShipped`. |
| `seller-inbound` | **Supporting** | `partner` | This context. |
| `delivery-promise`, `order-sourcing` | Core | `commerce` | No edge (Separate Ways). |
| `linehaul-transportation`, `last-mile-delivery` | Supporting | `logistics` | No edge. |
| `customer-ordering` | Generic | `commerce` | No edge. |
| `shipping-and-tracking` | Generic | `logistics` | No edge. |

The network has two Core, seven Supporting and two Generic contexts. In the problem space the subdomain is *Seller
Inbound*: letting sellers plan, label and ship stock to vendor nodes and reconciling what arrives.

The warehouse-systems fleet is not a neighbour: it reaches the network only through the Vendor API of
vendor-connectivity ([Integration](/docs/ecosystem/integration#edges-that-do-not-exist)). The fleet's own classification
of its contexts is on the [warehouse-docs strategic design](https://iqvo.github.io/warehouse-docs/strategic-design/subdomain-classification)
site.
