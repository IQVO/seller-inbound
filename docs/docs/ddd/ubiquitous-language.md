---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
sidebar_position: 0
description: "The terms seller-inbound speaks, mapped to the Go identifiers in this repository, plus the neighbour terms it reads."
---

# Ubiquitous language

The terms this context owns, with the Go identifiers the service uses (`package.Type`). Where a word is also a GS1 term,
the definition says what this model means by it. The design source is `network/model/model.yaml` in IQVO/warehouse-docs;
the aggregates are in [Aggregate Design Canvas](/docs/ddd/aggregate-design-canvas).

## This context's terms

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Seller** | An organisation that sends its own stock to the network. | `seller.Account`, `seller.ID` | A **Vendor** (vendor-connectivity), which operates fulfillment nodes. One organisation could be both; the model keeps the two roles apart. |
| **Owner / Seller of Record** | Whose stock it is. `ownerRef` equals the `sellerId`. | `seller.ID` (`shared.ValidOwnerRef`), `Account.OwnerRef()` | The operator of the node that holds the stock. |
| **Seller state** | `ACTIVE` or `SUSPENDED`; only an active seller plans, packs and ships. | `seller.Active`, `seller.Suspended`, `seller.ErrSellerSuspended` | The seller's compliance tier. |
| **Inbound Plan** | A seller's declaration, in eaches, of what it will send to one node, with a packing option. States `DRAFT`, `CONFIRMED`, `CANCELLED`. | `inboundplan.Plan` | A purchase order (vendor-connectivity), which flows the other way. |
| **Packing Option** | `INDIVIDUAL` (cartons are free logistic units) or `CASE_PACKED` (master cartons of one item). | `inboundplan.PackingOption` | A pack level. |
| **Inbound Shipment** | One physical consignment of a plan to a node: cartons, labels, receipt outcome. States `DRAFT`, `LABELLED`, `SHIPPED`, `RECEIVING`, `CLOSED`, `CLOSED_WITH_DISCREPANCY`. | `inboundshipment.Shipment` | A package shipment to a customer (shipping-and-tracking). |
| **Carton** | One outer carton of a shipment, identified by an SSCC; sealed when the shipment is labelled. | `inboundshipment.Carton` | A customer **Package**. |
| **Master Carton** | A carton holding one item at the quantity of its CASE level. | `Carton` with `inboundshipment.HomogeneousCase` | A pallet. |
| **Mixed Carton** | A carton with no pack GTIN, identified by its SSCC and manifest. | `Carton` with `inboundshipment.Mixed` | A master carton with a sticker. |
| **Each** | The lowest packaging level; the unit of every quantity (`qtyEaches`). | `network.Each` | A carton holding one unit. |
| **Inner Pack** | An intermediate package of several eaches. No flow uses it in this version. | `network.Inner` | A case. |
| **Case Pack** | The number of eaches in one case, `unitsPerPack`; at least 2 for a valid CASE level. | `network.PackLevel.UnitsPerPack`, `network.ActiveCaseUnits` | The number of cartons. |
| **GTIN** | The identifier of an item at one packaging level; stored as 14 digits; variable-measure GTINs (indicator 9) are refused. | `gs1.GTIN`, `gs1.RejectVariableMeasure` | A SKU or a `catalogItemId`, which are the network's own ids. |
| **SSCC** | The 18-digit identifier of one logistic unit, unique network-wide, with a mod-10 check digit. | `gs1.SSCC`, `inboundshipment.ErrInvalidSscc`, `ErrSsccNotUnique` | A GTIN: an SSCC identifies one carton, a GTIN a kind of item. |
| **SSCC issuance** | The network builds an SSCC from its own GS1 company prefix when the seller supplies none. | `ports.SsccIssuer`, `sscc.Issuer` | Accepting a seller's SSCC. |
| **Label** | The printable data of a carton: reference `LBL-<sscc>`, GS1-128 element string `(00)<sscc>`, human-readable line, content summary. Rendering is the seller's job. | `views.Label` | The shipment's `LABELLED` state. |
| **Manifest** | The cartons and their contents as frozen at labelling; published on `InboundShipmentLabelled`. | `inboundshipment.InboundShipmentLabelled` | The plan, which is in eaches only. |
| **Inbound Receipt** | The vendor's carton-level answer for a shipment, per carton `RECEIVED`, `MISSING`, `UNEXPECTED` or `DAMAGED` with counted eaches per GTIN. | `inboundshipment.Receipt`, `ReceiptCarton`, `ReceiptStatus` | A goods receipt in the vendor's system. |
| **Discrepancy** | A recorded difference between manifest and receipt, never a correction: kind `MissingCarton`, `UnexpectedCarton`, `Damaged`, `Short`, `Over`. | `inboundshipment.InboundDiscrepancyRecorded` | A compliance violation. |
| **Compliance violation** | A coded breach reported on a carton (plus the context's own `UNMANIFESTED_CARTON`). | `inboundshipment.SellerComplianceViolationRecorded`, `CodeUnmanifestedCarton` | A discrepancy. |
| **Seller scorecard** | An informational rolling-window (7d or 30d) read of a seller's receipt accuracy, discrepancy rate per shipment and violations. | `scorecard.Card`, `scorecard.Compute` | A suspension: it never acts. |
| **Compliance tier** | `GOOD`, `WATCH` or `AT_RISK`: the worst of three signals (accuracy below 0.98 or 0.90; discrepancies per shipment at least 0.5 or 2.0; violations at least 1 or 5). | `scorecard.Tier`, `scorecard.TierFor` | The seller state. |
| **Node able to receive** | A node whose view is `ACTIVE` and of type `FULFILLMENT_CENTER` or `INBOUND_CROSS_DOCK`. | `network.Node.CanReceive`, `ErrNodeCannotReceive` | A node that exists. |
| **Inbound limits** | A node's declared carton `maxCartonWeightG`, `maxCartonDimsMm`, `acceptsPallets`, `requiresSscc`. | `network.InboundLimits` | The plan's quantities. |
| **Break Case** | Opening a sealed case at a vendor node. Not a network event and not modelled here. | none | Any state of `Carton`. |

## Neighbour terms this context reads

| Term | Definition (owner's) | Owner |
| --- | --- | --- |
| **Catalog Item** | The network's own entry for a product, independent of who ships it. | offer-availability |
| **Packaging Hierarchy**, **Pack Level** | The pack levels of one catalogue item (`EACH`, `INNER`, `CASE`, `PALLET`), each with its own GTIN and fixed content quantity. Held locally as `catalog_pack_levels`. | offer-availability |
| **Node**, **Operational Profile** | A building and its declared profile with inbound limits. Held locally as `node_inbound_views`. | node-topology |
| **Vendor**, **Vendor API**, **Inbound Receipt** | An organisation operating nodes, its Open Host Service, and its carton-level report of what arrived. | vendor-connectivity |

## Fleet translation

None at the language level. The warehouse-systems fleet receives the manifest as the Vendor API's published language and
translates it inside its own anti-corruption layer; this repository does not model that side
([Integration](/docs/ecosystem/integration#edges-that-do-not-exist)).

## Terms the model leaves open

| Term | What is missing |
| --- | --- |
| Compliance codes | The vocabulary of codes a vendor may report on a receipt is not enumerated; the service passes them through. |
| Carton `kind` | `HOMOGENEOUS_CASE` and `MIXED` are this model's names. |
