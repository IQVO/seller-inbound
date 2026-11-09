# ADR 0014: Received stock is published per item

## Status

Accepted (2026-11-09).

## Context

Network gap G51: received seller stock never becomes a listing.
offer-availability is ready to create owner-scoped listings when it sees
received stock, but
`com.retailnetwork.partner.seller-inbound.inboundshipment.InboundShipmentReceived`
carries only `{shipmentId, sellerId, nodeId, status, cartons:[{sscc, status}],
receivedAt}`: no item, GTIN or quantity. This context knows the manifest
content of every carton (`Content`: catalogItemId, gtin, qtyEaches, lot,
expiry) from labelling, the vendor's counted items per carton from
`InboundReceiptSubmitted`, and the catalogue packaging (GTIN to
catalogItemId, child GTIN, `unitsPerPack`) in its `CatalogPackagingView`
(ADR 0003).

Alternatives weighed:

- offer-availability joins `InboundShipmentLabelled` (the manifest) with
  `InboundShipmentReceived` itself. Rejected: it would have to keep a
  manifest read model of a context it does not own, could not see the
  vendor's counted quantities (they never leave this context), and would
  repeat the receipt-status rules of ADR 0002 (A8).
- A new event (`InboundStockReceived`). Rejected: the fact is the same
  closing fact; an additive optional field of the existing v1 payload is the
  smaller change and old consumers ignore it.

## Decision

The contract, as fixed by the orchestrator (verbatim):

1. InboundShipmentReceived.data gains `received`: an array of {catalogItemId?, gtin, qtyEaches}, aggregated per (gtin) over the cartons whose receipt counts as received (define it from the as-built carton receipt status vocabulary, e.g. RECEIVED/RECEIVED_WITH_DISCREPANCY count and MISSING does not).
   - qtyEaches is in eaches: a case GTIN's qty x unitsPerPack, resolved from CatalogPackagingView. Use the receipt's ACTUAL counted quantities where the receipt reports them; otherwise use the manifest quantities. State the rule in the ADR.
   - The `gtin` is the EACH-level GTIN when the packaging view resolves the case GTIN to its child each. Otherwise it is the carton's GTIN.
   - catalogItemId is present when the packaging view resolves it, and omitted otherwise.
   - Entries with qtyEaches 0 are omitted. Lines are sorted by gtin.
2. The existing fields are unchanged. `received` is always present on new events (an empty array is legal). Old consumers ignore it.
3. Update apis/asyncapi.yaml: payload schema, example, and description ('Consumers: offer-availability uses `received` to create owner-scoped listings, gap G51'). If the catalogue ADR lists payload fields, update it there too. ADR (expected 0014): 'Received stock is published per item'.
4. Tests:
   - domain/use-case tests for the aggregation: case vs each, a discrepancy carton, a missing carton, unresolved packaging;
   - an encoder golden;
   - the e2e/integration expectations for the event payload.
5. Do not change the topic, type or subject.

How this context meets it, against the as-built model:

1. **Which cartons count.** The as-built receipt vocabulary is `RECEIVED`,
   `MISSING`, `UNEXPECTED`, `DAMAGED` (ADR 0002, A8); there is no
   `RECEIVED_WITH_DISCREPANCY` status. A carton with a quantity discrepancy
   is a `RECEIVED` carton whose counts differ from its manifest (`Short` /
   `Over`). Only manifest cartons answered `RECEIVED` count, discrepant or
   not. `MISSING` does not count (nothing arrived). `DAMAGED` does not count:
   the stock is not sellable, and a listing on it would promise what cannot
   ship; the damage is already published as `InboundDiscrepancyRecorded`
   (`Damaged`). `UNEXPECTED` does not count: a carton the manifest does not
   list has no known owner-of-record contents (only its SSCC is kept).
2. **Quantity rule.** For a counted carton, when the vendor's receipt
   reported counted items for it (a non-empty item list), the counted
   quantities are used, GTIN by GTIN, including a GTIN the manifest did not
   list; otherwise the carton's manifest contents are used. Both are already
   in eaches: a manifest `Content.QtyEaches` is eaches by construction (a
   `HOMOGENEOUS_CASE` carton's `qtyEaches` must equal the `unitsPerPack` of
   its active CASE level, `ErrCaseQtyMismatchesGtin`), and the receipt's
   counted `qtyEaches` is eaches on the wire. So the case GTIN's
   `qty x unitsPerPack` IS the stored `qtyEaches`; it is summed as is, never
   multiplied again.
3. **GTIN resolution.** A line's GTIN is looked up in the
   `CatalogPackagingView` rows of the line's `catalogItemId`: the manifest
   content's `catalogItemId`; for a counted GTIN, the `catalogItemId` of the
   carton's manifest line with that GTIN, else of any manifest line of the
   shipment with that GTIN, else none (unresolved). From the row of that
   GTIN, the `childGtin` chain is walked down inside the item's rows until a
   row of level `EACH` is reached; its GTIN is the published `gtin`. When the
   chain breaks (no row for the GTIN, no child, a child the view does not
   hold) the carton's GTIN is published. Retired rows still resolve:
   retirement stops new packing at a level, it does not change which item a
   GTIN identifies. `network.ResolveEach` holds the rule.
4. **catalogItemId.** Present when the view holds a row of
   (`catalogItemId`, carton GTIN) at any level; omitted otherwise. When two
   contributions of one published `gtin` differ, the first resolved one (in
   manifest carton order) wins.
5. **Aggregation.** Quantities are summed per published `gtin`; entries
   with `qtyEaches` 0 are omitted; lines are sorted by `gtin`. The array is
   always present (`[]` when nothing counts, for example every carton
   `MISSING`).
6. **Where it is computed.** `Shipment.RecordReceipt` builds `received` when
   the shipment closes (`InboundShipmentReceived.Received`); the use case
   `ApplyInboundReceipt` hands it a resolver over the local
   `CatalogPackagingView` (read only when the shipment closes, in the same
   transaction; no synchronous call to another context). Each `Carton` keeps
   the vendor's counted items (`Counted`) from its first answer, in the
   `cartons` JSONB column, so a shipment received over several receipts
   still publishes the actual counts. No migration: the column is JSONB and
   the new key is omitted when empty; a carton answered before this change
   has no stored counts and falls back to its manifest.
7. Topic, `type`, `subject`, `dataschema` (`:v1`) and every existing field
   are unchanged. ADR 0004 lists no payload fields, so it is not edited.

## Consequences

- offer-availability can create owner-scoped listings from the closing
  fact alone (`sellerId`, `nodeId`, `received`), gap G51.
- A shipment closed before this change was published without `received`;
  consumers must treat a missing `received` as "unknown", not as "nothing
  received". Replays of those old events carry no lines.
- Packaging declared after a shipment closed does not change the published
  lines; the event is a fact of the closing instant.
- Stock answered `DAMAGED` never becomes a listing through this event; if
  damaged stock is later reworked into sellable stock, that is a new fact
  of the receiving node, not of this context.
- Model gap G65 (plan lines may name a discontinued item) is out of scope.
