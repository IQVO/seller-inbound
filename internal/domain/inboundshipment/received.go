package inboundshipment

import (
	"slices"
	"strings"
)

// lineQty is one (item, gtin, eaches) contribution of a counted carton.
type lineQty struct {
	item, gtin string
	qty        int
}

// receivedLines builds InboundShipmentReceived.received (ADR 0014): per
// published GTIN, the eaches of the manifest cartons answered RECEIVED, from
// the vendor's counted items where it reported them and from the manifest
// otherwise. MISSING, DAMAGED and UNEXPECTED cartons do not count. Lines of
// zero eaches are omitted and the rest come back sorted by gtin; the result
// is never nil.
func (s *Shipment) receivedLines(pack ItemPackaging) []ReceivedLine {
	itemOf := s.itemByGTIN()
	byGTIN := map[string]*ReceivedLine{}
	var order []string
	for _, c := range s.cartons {
		if c.Receipt != StatusReceived {
			continue
		}
		for _, q := range cartonQuantities(c, itemOf) {
			l := resolveLine(q, pack)
			got, ok := byGTIN[l.GTIN]
			if !ok {
				byGTIN[l.GTIN], order = &l, append(order, l.GTIN)
				continue
			}
			got.QtyEaches += l.QtyEaches
			if got.CatalogItemID == "" {
				got.CatalogItemID = l.CatalogItemID
			}
		}
	}
	out := make([]ReceivedLine, 0, len(order))
	for _, g := range order {
		if byGTIN[g].QtyEaches > 0 {
			out = append(out, *byGTIN[g])
		}
	}
	slices.SortFunc(out, func(a, b ReceivedLine) int { return strings.Compare(a.GTIN, b.GTIN) })
	return out
}

// itemByGTIN maps every manifest GTIN of the shipment to the catalogItemId of
// its first manifest line, in carton order.
func (s *Shipment) itemByGTIN() map[string]string {
	out := map[string]string{}
	for _, c := range s.cartons {
		for _, ct := range c.Contents {
			if _, ok := out[ct.GTIN]; !ok {
				out[ct.GTIN] = ct.CatalogItemID
			}
		}
	}
	return out
}

// cartonQuantities is what one counted carton contributes: the vendor's
// counted items when it reported any, else the manifest contents. A counted
// GTIN takes the catalogItemId of the carton's own manifest line with that
// GTIN, else of the shipment's (shipmentItems), else none.
func cartonQuantities(c Carton, shipmentItems map[string]string) []lineQty {
	out := make([]lineQty, 0, max(len(c.Counted), len(c.Contents)))
	if len(c.Counted) == 0 {
		for _, ct := range c.Contents {
			out = append(out, lineQty{item: ct.CatalogItemID, gtin: ct.GTIN, qty: ct.QtyEaches})
		}
		return out
	}
	for _, it := range c.Counted {
		item, listed := itemOfLine(c, it.GTIN)
		if !listed {
			item = shipmentItems[it.GTIN]
		}
		out = append(out, lineQty{item: item, gtin: it.GTIN, qty: it.QtyEaches})
	}
	return out
}

// itemOfLine returns the catalogItemId of the carton's manifest line with
// the gtin, and whether the carton lists it.
func itemOfLine(c Carton, gtin string) (string, bool) {
	for _, ct := range c.Contents {
		if ct.GTIN == gtin {
			return ct.CatalogItemID, true
		}
	}
	return "", false
}

// resolveLine publishes the EACH-level GTIN when the packaging view resolves
// the carton's GTIN to one, and the catalogItemId when the view knows the
// (item, gtin) pair; otherwise the carton's GTIN without an item.
func resolveLine(q lineQty, pack ItemPackaging) ReceivedLine {
	l := ReceivedLine{GTIN: q.gtin, QtyEaches: q.qty}
	if pack == nil || q.item == "" {
		return l
	}
	each, known := pack(q.item, q.gtin)
	if !known {
		return l
	}
	l.CatalogItemID = q.item
	if each != "" {
		l.GTIN = each
	}
	return l
}
