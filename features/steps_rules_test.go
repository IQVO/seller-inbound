package features_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/seller-inbound/internal/domain/inboundplan"
	"github.com/claudioed/seller-inbound/internal/domain/inboundshipment"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// steps_rules_test.go is the vocabulary of the *_rules.feature files, the
// scenarios derived from the context documentation: facts delivered by a
// neighbour under a chosen CloudEvents id, plans and cartons of a chosen
// shape, scorecard facts at a chosen instant, and typed assertions on the
// events the service published (their kind, expected and counted values).

const (
	nodeTypePrefix    = "com.retailnetwork.logistics.node-topology.node."
	catalogTypePrefix = "com.retailnetwork.commerce.offer-availability.catalogitem."

	// cartonRef names a carton of the scenario by its position ("carton 2") or by a literal SSCC.
	cartonRef = `((?:carton \d+)|(?:SSCC "[^"]*"))`
)

func registerRuleSteps(sc *godog.ScenarioContext, w *world) {
	registerRuleGivenNetwork(sc, w)
	registerRuleGivenPlans(sc, w)
	registerRuleGivenScorecard(sc, w)
	registerRuleWhens(sc, w)
	registerRuleThenCounts(sc, w)
	registerRuleThenPayloads(sc, w)
	registerRuleThenReceipt(sc, w)
	registerRuleThenClosing(sc, w)
}

// --- helpers -----------------------------------------------------------------

// mod10 is the GS1 check digit of a payload.
func mod10(payload string) int {
	sum := 0
	for i := len(payload) - 1; i >= 0; i-- {
		d := int(payload[i] - '0')
		if (len(payload)-1-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return (10 - sum%10) % 10
}

// itemID and gtinOf generate the i-th distinct catalogue item and its valid GTIN-13.
func itemID(i int) string { return fmt.Sprintf("item-%03d", i) }

func gtinOf(i int) string {
	payload := fmt.Sprintf("0614%08d", i)
	return payload + strconv.Itoa(mod10(payload))
}

func line(item, gtin string, qty int) string {
	return fmt.Sprintf(`{"catalogItemId":%q,"gtin":%q,"qtyEaches":%d}`, item, gtin, qty)
}

func cartonBody(contents ...string) string {
	return `{"contents":[` + strings.Join(contents, ",") + `],"grossWeightG":3000,"dimsMm":{"l":400,"w":300,"h":200}}`
}

func packingOption(s string) string {
	return map[string]string{"case-packed": "CASE_PACKED", "individual": "INDIVIDUAL"}[s]
}

func distinctLines(n, qty int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = line(itemID(i+1), gtinOf(i+1), qty)
	}
	return lines
}

// tableRows returns the data rows of a table (its header row dropped) as plain strings.
func tableRows(t *godog.Table) [][]string {
	var rows [][]string
	for _, r := range t.Rows[1:] {
		cells := make([]string, len(r.Cells))
		for i, c := range r.Cells {
			cells[i] = c.Value
		}
		rows = append(rows, cells)
	}
	return rows
}

// deliver hands one CloudEvent to a consumer under a chosen id; "" mints a fresh one.
func (w *world) deliver(c interface {
	HandleMessage(context.Context, []byte) error
}, typ, id, data string) error {
	if id == "" {
		id = w.eventID()
	}
	return c.HandleMessage(context.Background(), cloudEvent(id, typ, "subject", w.expand(data)))
}

func (w *world) createPlan(seller, packing, node string, lines []string) error {
	body := fmt.Sprintf(`{"sellerId":%q,"destinationNodeId":%q,"packingOption":%q,"lines":[%s]}`,
		seller, node, packingOption(packing), strings.Join(lines, ","))
	return w.write(http.MethodPost, "/inbound-plans", body)
}

// confirmedPlan creates and confirms a plan, failing unless both succeed.
func (w *world) confirmedPlan(seller, packing, node string, lines []string) error {
	if err := w.createPlan(seller, packing, node, lines); err != nil || w.status != http.StatusCreated {
		return fmt.Errorf("create plan: %w %d %s", err, w.status, w.body)
	}
	if err := w.write(http.MethodPost, "/inbound-plans/{plan}/confirmations", ""); err != nil || w.status != http.StatusOK {
		return fmt.Errorf("confirm plan: %w %d %s", err, w.status, w.body)
	}
	return nil
}

// ssccOf resolves a carton reference: "carton N" is the N-th carton added through the API.
func (w *world) ssccOf(ref string) (string, error) {
	if lit, ok := strings.CutPrefix(ref, `SSCC "`); ok {
		return strings.TrimSuffix(lit, `"`), nil
	}
	n, err := strconv.Atoi(strings.TrimPrefix(ref, "carton "))
	if err != nil || n < 1 || n > len(w.sscc) {
		return "", fmt.Errorf("%s: only %d cartons were added through the API", ref, len(w.sscc))
	}
	return w.sscc[n-1], nil
}

// --- network facts --------------------------------------------------------------

func registerRuleGivenNetwork(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^node "([^"]*)" is an active inbound cross-dock$`, func(node string) error {
		reg := fmt.Sprintf(`{"nodeId":%q,"nodeType":"INBOUND_CROSS_DOCK"}`, node)
		if err := w.feed(w.nodes, typeNodeRegistered, node, reg); err != nil {
			return err
		}
		return w.feed(w.nodes, typeNodeActivated, node, fmt.Sprintf(`{"nodeId":%q}`, node))
	})
	sc.Step(`^node "([^"]*)" is an active fulfillment center with the inbound limits:$`, func(node string, doc *godog.DocString) error {
		return w.activeNode(node, `,"inbound":`+doc.Content)
	})
	sc.Step(`^node-topology delivers (\w+) as event "([^"]*)" with:$`, func(name, id string, doc *godog.DocString) error {
		return w.deliver(w.nodes, nodeTypePrefix+name, id, doc.Content)
	})
	sc.Step(`^node-topology delivers (\w+) with:$`, func(name string, doc *godog.DocString) error {
		return w.deliver(w.nodes, nodeTypePrefix+name, "", doc.Content)
	})
	sc.Step(`^offer-availability delivers (\w+) with:$`, func(name string, doc *godog.DocString) error {
		return w.deliver(w.catalg, catalogTypePrefix+name, "", doc.Content)
	})
}

// --- plans, shipments and cartons -------------------------------------------------

func registerRuleGivenPlans(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^seller "([^"]*)" has a confirmed (individual|case-packed) plan to node "([^"]*)" with lines:$`,
		func(seller, packing, node string, t *godog.Table) error {
			var lines []string
			for _, r := range tableRows(t) {
				qty, err := strconv.Atoi(r[2])
				if err != nil {
					return err
				}
				lines = append(lines, line(r[0], r[1], qty))
			}
			return w.confirmedPlan(seller, packing, node, lines)
		})
	sc.Step(`^seller "([^"]*)" has a draft (individual|case-packed) plan for (\d+) eaches of "([^"]*)" with GTIN "([^"]*)" to node "([^"]*)"$`,
		func(seller, packing string, qty int, item, gtin, node string) error {
			if err := w.createPlan(seller, packing, node, []string{line(item, gtin, qty)}); err != nil || w.status != http.StatusCreated {
				return fmt.Errorf("create plan: %w %d %s", err, w.status, w.body)
			}
			return nil
		})
	sc.Step(`^seller "([^"]*)" has a confirmed individual plan of (\d+) distinct items of (\d+) eaches each to node "([^"]*)"$`,
		func(seller string, n, qty int, node string) error {
			return w.confirmedPlan(seller, "individual", node, distinctLines(n, qty))
		})
	sc.Step(`^the shipment has an individual carton of (\d+) eaches of "([^"]*)" with GTIN "([^"]*)"$`, func(qty int, item, gtin string) error {
		return w.addCarton(cartonBody(line(item, gtin, qty)))
	})
	sc.Step(`^the shipment has (\d+) individual cartons of 1 each of "([^"]*)" with GTIN "([^"]*)"$`, func(n int, item, gtin string) error {
		for range n {
			if err := w.addCarton(cartonBody(line(item, gtin, 1))); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the shipment is labelled$`, func() error {
		if err := w.write(http.MethodPost, "/inbound-shipments/{shipment}/labels", ""); err != nil || w.status != http.StatusOK {
			return fmt.Errorf("label: %w %d %s", err, w.status, w.body)
		}
		return nil
	})
}

// addCarton adds a carton to the scenario's shipment, failing unless it is accepted.
func (w *world) addCarton(body string) error {
	if err := w.write(http.MethodPost, "/inbound-shipments/{shipment}/cartons", body); err != nil || w.status != http.StatusCreated {
		return fmt.Errorf("add carton: %w %d %s", err, w.status, w.body)
	}
	return nil
}

// --- scorecard facts ----------------------------------------------------------------

// seedFacts records scorecard facts straight in the facts store, at an instant
// relative to the service clock: the thresholds of ADR 0005 are on counts that
// no sane receipt flow produces one by one.
func (w *world) seedFacts(f scorecard.Facts) error {
	return w.store.Scorecard().Record(context.Background(), f)
}

func (w *world) seedReceipts(seller string, shipments, reported, received int) error {
	var f scorecard.Facts
	for i := range shipments {
		r := scorecard.Receipt{SellerID: seller, ShipmentID: w.eventID() + "-" + strconv.Itoa(i), At: w.clock.Now()}
		if i == 0 {
			r.CartonsReported, r.CartonsReceived = reported, received
		}
		f.Receipts = append(f.Receipts, r)
	}
	return w.seedFacts(f)
}

func (w *world) seedOccurrences(seller string, typ scorecard.OccurrenceType, code string, n int, ago time.Duration) error {
	var f scorecard.Facts
	for range n {
		f.Occurrences = append(f.Occurrences, scorecard.Occurrence{
			SellerID: seller, ShipmentID: "seeded", Type: typ, Code: code, At: w.clock.Now().Add(-ago),
		})
	}
	return w.seedFacts(f)
}

func registerRuleGivenScorecard(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^seller "([^"]*)" has (\d+) closed shipments reporting (\d+) cartons of which (\d+) arrived$`, w.seedReceipts)
	sc.Step(`^seller "([^"]*)" has (\d+) discrepancies of kind "([^"]*)" recorded now$`, func(seller string, n int, kind string) error {
		return w.seedOccurrences(seller, scorecard.Discrepancy, kind, n, 0)
	})
	sc.Step(`^seller "([^"]*)" has (\d+) compliance violations? recorded now$`, func(seller string, n int) error {
		return w.seedOccurrences(seller, scorecard.Violation, "LABEL_UNREADABLE", n, 0)
	})
	sc.Step(`^seller "([^"]*)" has a compliance violation recorded exactly (\d+) days ago$`, func(seller string, days int) error {
		return w.seedOccurrences(seller, scorecard.Violation, "LABEL_UNREADABLE", 1, time.Duration(days)*24*time.Hour)
	})
	sc.Step(`^seller "([^"]*)" has a compliance violation recorded (\d+) days and one second ago$`, func(seller string, days int) error {
		return w.seedOccurrences(seller, scorecard.Violation, "LABEL_UNREADABLE", 1, time.Duration(days)*24*time.Hour+time.Second)
	})
}

// --- whens --------------------------------------------------------------------------

func registerRuleWhens(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^seller "([^"]*)" submits an individual plan of (\d+) distinct items to node "([^"]*)"$`, func(seller string, n int, node string) error {
		return w.createPlan(seller, "individual", node, distinctLines(n, 1))
	})
	sc.Step(`^I add a mixed carton holding the first (\d+) items of the plan$`, func(n int) error {
		contents := make([]string, n)
		for i := range contents {
			contents[i] = line(itemID(i+1), gtinOf(i+1), 1)
		}
		return w.write(http.MethodPost, "/inbound-shipments/{shipment}/cartons", cartonBody(contents...))
	})
	sc.Step(`^the vendor reports this receipt of the shipment as event "([^"]*)":$`, func(id string, doc *godog.DocString) error {
		w.feedErr = w.receip.HandleMessage(context.Background(), cloudEvent(id, typeReceipt, w.shipmentID, w.expand(doc.Content)))
		return nil
	})
}

// --- thens: published events ----------------------------------------------------------

func (w *world) events() []shared.Event { return w.store.Outbox().Events() }

func (w *world) countEvents(want int, name string) error {
	n := 0
	for _, e := range w.events() {
		if e.EventName() == name {
			n++
		}
	}
	if n != want {
		return fmt.Errorf("outbox holds %d %s events, want %d (all: %s)", n, name, want, strings.Join(w.eventNames(), ", "))
	}
	return nil
}

func (w *world) discrepancyHeld(kind, ref, expected, actual string) error {
	sscc, err := w.ssccOf(ref)
	if err != nil {
		return err
	}
	var seen []string
	for _, e := range w.events() {
		d, ok := e.(inboundshipment.InboundDiscrepancyRecorded)
		if !ok {
			continue
		}
		seen = append(seen, fmt.Sprintf("%s/%s/%s/%s", d.Kind, d.SSCC, d.Expected, d.Actual))
		if d.Kind == kind && d.SSCC == sscc && d.Expected == expected && d.Actual == actual {
			return nil
		}
	}
	return fmt.Errorf("no discrepancy %s/%s/%s/%s among %v", kind, sscc, expected, actual, seen)
}

func (w *world) violationHeld(code, ref string) error {
	sscc, err := w.ssccOf(ref)
	if err != nil {
		return err
	}
	for _, e := range w.events() {
		if v, ok := e.(inboundshipment.SellerComplianceViolationRecorded); ok && v.Code == code && v.SSCC == sscc {
			return nil
		}
	}
	return fmt.Errorf("no violation %s on %s among %v", code, sscc, w.eventNames())
}

func (w *world) envelopeOf(name, entity, subject string) error {
	for _, e := range w.events() {
		if e.EventName() != name {
			continue
		}
		if e.Entity() != entity || e.SubjectID() != w.expand(subject) {
			return fmt.Errorf("%s has entity %q subject %q, want %q %q", name, e.Entity(), e.SubjectID(), entity, w.expand(subject))
		}
		return nil
	}
	return fmt.Errorf("no %s event among %v", name, w.eventNames())
}

func registerRuleThenCounts(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the outbox holds (\d+) "([^"]*)" events?$`, w.countEvents)
	sc.Step(`^the outbox holds an InboundDiscrepancyRecorded of kind "([^"]*)" for `+cartonRef+`(?: expecting "([^"]*)" and counting "([^"]*)")?$`,
		w.discrepancyHeld)
	sc.Step(`^the outbox holds a SellerComplianceViolationRecorded with code "([^"]*)" for `+cartonRef+`$`, w.violationHeld)
	sc.Step(`^the first "([^"]*)" event has entity "([^"]*)" and subject "([^"]*)"$`, w.envelopeOf)
}

func (w *world) planConfirmedCarries(seller, node, packing string, t *godog.Table) error {
	var want []inboundplan.Line
	for _, r := range tableRows(t) {
		qty, _ := strconv.Atoi(r[2])
		want = append(want, inboundplan.Line{CatalogItemID: r[0], GTIN: r[1], QtyEaches: qty})
	}
	for _, e := range w.events() {
		p, ok := e.(inboundplan.InboundPlanConfirmed)
		if !ok {
			continue
		}
		if p.SellerID != seller || p.DestinationNodeID != node || string(p.Packing) != packing || !slices.Equal(p.Lines, want) {
			return fmt.Errorf("InboundPlanConfirmed = %+v, want seller %s node %s packing %s lines %v", p, seller, node, packing, want)
		}
		return nil
	}
	return fmt.Errorf("no InboundPlanConfirmed among %v", w.eventNames())
}

func (w *world) labelled() (inboundshipment.InboundShipmentLabelled, error) {
	for _, e := range w.events() {
		if l, ok := e.(inboundshipment.InboundShipmentLabelled); ok {
			return l, nil
		}
	}
	return inboundshipment.InboundShipmentLabelled{}, fmt.Errorf("no InboundShipmentLabelled among %v", w.eventNames())
}

func (w *world) labelledCarries(seller, node string, n int) error {
	e, err := w.labelled()
	if err != nil {
		return err
	}
	if e.SellerID != seller || e.NodeID != node || string(e.ShipmentID) != w.shipmentID || e.PlanID != w.planID || len(e.Cartons) != n {
		return fmt.Errorf("InboundShipmentLabelled = %+v, want seller %s node %s shipment %s plan %s and %d cartons", e, seller, node, w.shipmentID, w.planID, n)
	}
	return nil
}

func (w *world) manifestLists(lot, expiry string, n int) error {
	e, err := w.labelled()
	if err != nil {
		return err
	}
	if n < 1 || n > len(e.Cartons) || e.Cartons[n-1].Contents[0].Lot != lot || e.Cartons[n-1].Contents[0].Expiry != expiry {
		return fmt.Errorf("manifest = %+v, want lot %s expiry %s on carton %d", e.Cartons, lot, expiry, n)
	}
	return nil
}

func registerRuleThenPayloads(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the InboundPlanConfirmed event carries seller "([^"]*)", node "([^"]*)", packing "([^"]*)" and the lines:$`, w.planConfirmedCarries)
	sc.Step(`^the InboundShipmentLabelled event carries seller "([^"]*)" and node "([^"]*)" for this shipment and (\d+) cartons$`, w.labelledCarries)
	sc.Step(`^the manifest in the InboundShipmentLabelled event lists lot "([^"]*)" expiring "([^"]*)" on carton (\d+)$`, w.manifestLists)
}

// --- thens: the receipt and the closing event --------------------------------------------

func (w *world) received() (inboundshipment.InboundShipmentReceived, error) {
	for _, e := range w.events() {
		if r, ok := e.(inboundshipment.InboundShipmentReceived); ok {
			return r, nil
		}
	}
	return inboundshipment.InboundShipmentReceived{}, fmt.Errorf("no InboundShipmentReceived among %v", w.eventNames())
}

func (w *world) deadLettered(reason string) error {
	if w.feedErr == nil {
		return fmt.Errorf("the consumer accepted a receipt that can never apply")
	}
	msg := w.feedErr.Error()
	if !strings.Contains(msg, "poison message") || !strings.Contains(msg, reason) {
		return fmt.Errorf("receipt error = %q, want a poison message containing %q", msg, reason)
	}
	return nil
}

func registerRuleThenReceipt(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the receipt is accepted$`, func() error {
		if w.feedErr != nil {
			return fmt.Errorf("the receipt consumer refused the event: %w", w.feedErr)
		}
		return nil
	})
	sc.Step(`^the receipt is dead-lettered because "([^"]*)"$`, w.deadLettered)
}

func (w *world) closingStatus(status string) error {
	r, err := w.received()
	if err != nil {
		return err
	}
	if string(r.Status) != status {
		return fmt.Errorf("closing event status = %s, want %s", r.Status, status)
	}
	return nil
}

func (w *world) closingCarton(ref, status string) error {
	sscc, err := w.ssccOf(ref)
	if err != nil {
		return err
	}
	r, err := w.received()
	if err != nil {
		return err
	}
	for _, c := range r.Cartons {
		if c.SSCC != sscc {
			continue
		}
		if string(c.Status) != status {
			return fmt.Errorf("carton %s is %s in the closing event, want %s", sscc, c.Status, status)
		}
		return nil
	}
	return fmt.Errorf("carton %s is not in the closing event %+v", sscc, r.Cartons)
}

func (w *world) receivedLines(t *godog.Table) error {
	r, err := w.received()
	if err != nil {
		return err
	}
	var want []inboundshipment.ReceivedLine
	for _, row := range tableRows(t) {
		qty, _ := strconv.Atoi(row[1])
		want = append(want, inboundshipment.ReceivedLine{GTIN: row[0], QtyEaches: qty, CatalogItemID: row[2]})
	}
	if !slices.Equal(r.Received, want) {
		return fmt.Errorf("received lines = %+v, want %+v", r.Received, want)
	}
	return nil
}

func (w *world) noReceivedLines() error {
	r, err := w.received()
	if err != nil {
		return err
	}
	if r.Received == nil || len(r.Received) != 0 {
		return fmt.Errorf("received = %#v, want an empty, non-nil list", r.Received)
	}
	return nil
}

func registerRuleThenClosing(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the InboundShipmentReceived event has status "([^"]*)"$`, w.closingStatus)
	sc.Step(`^the InboundShipmentReceived event lists `+cartonRef+` as "([^"]*)"$`, w.closingCarton)
	sc.Step(`^the InboundShipmentReceived event reports the received lines:$`, w.receivedLines)
	sc.Step(`^the InboundShipmentReceived event reports no received lines$`, w.noReceivedLines)
}
