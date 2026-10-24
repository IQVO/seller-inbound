package features_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

const (
	typeNodeRegistered  = "com.retailnetwork.logistics.node-topology.node.NodeRegistered"
	typeProfileDeclared = "com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared"
	typeNodeActivated   = "com.retailnetwork.logistics.node-topology.node.NodeActivated"
	typeNodeSuspended   = "com.retailnetwork.logistics.node-topology.node.NodeSuspended"
	typePackaging       = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemPackagingDeclared"
	typeReceipt         = "com.retailnetwork.partner.vendor-connectivity.inboundreceipt.InboundReceiptSubmitted"
)

func registerGiven(sc *godog.ScenarioContext, w *world) {
	registerGivenNetwork(sc, w)
	registerGivenSeller(sc, w)
	registerGivenShipment(sc, w)
}

func registerGivenSeller(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^seller "([^"]*)" is onboarded$`, func(id string) error {
		if err := w.write(http.MethodPost, "/sellers", fmt.Sprintf(`{"sellerId":%q,"name":"Seller %s"}`, id, id)); err != nil || w.status != http.StatusCreated {
			return fmt.Errorf("onboard: %v %d %s", err, w.status, w.body)
		}
		return nil
	})
	sc.Step(`^seller "([^"]*)" has a confirmed (case-packed|individual) plan for (\d+) eaches of "([^"]*)" with GTIN "([^"]*)" to node "([^"]*)"$`,
		func(seller, packing string, qty int, item, gtin, node string) error {
			opt := map[string]string{"case-packed": "CASE_PACKED", "individual": "INDIVIDUAL"}[packing]
			body := fmt.Sprintf(`{"sellerId":%q,"destinationNodeId":%q,"packingOption":%q,"lines":[{"catalogItemId":%q,"gtin":%q,"qtyEaches":%d}]}`, seller, node, opt, item, gtin, qty)
			if err := w.write(http.MethodPost, "/inbound-plans", body); err != nil || w.status != http.StatusCreated {
				return fmt.Errorf("create plan: %v %d %s", err, w.status, w.body)
			}
			if err := w.write(http.MethodPost, "/inbound-plans/{plan}:confirm", ""); err != nil || w.status != http.StatusOK {
				return fmt.Errorf("confirm plan: %v %d %s", err, w.status, w.body)
			}
			return nil
		})
	sc.Step(`^seller "([^"]*)" is suspended$`, func(id string) error {
		if err := w.write(http.MethodPost, "/sellers/"+id+":suspend", ""); err != nil || w.status != http.StatusOK {
			return fmt.Errorf("suspend: %v %d %s", err, w.status, w.body)
		}
		return nil
	})
}

func registerGivenNetwork(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^node "([^"]*)" is an active fulfillment center$`, func(node string) error {
		return w.activeNode(node, "")
	})
	sc.Step(`^node "([^"]*)" is an active fulfillment center that accepts cartons up to (\d+) grams$`, func(node string, grams int) error {
		return w.activeNode(node, fmt.Sprintf(`,"inbound":{"maxCartonWeightG":%d}`, grams))
	})
	sc.Step(`^node "([^"]*)" is an active sortation center$`, func(node string) error {
		reg := fmt.Sprintf(`{"nodeId":%q,"nodeType":"SORTATION_CENTER"}`, node)
		for _, step := range []struct{ typ, data string }{{typeNodeRegistered, reg}, {typeNodeActivated, fmt.Sprintf(`{"nodeId":%q}`, node)}} {
			if err := w.feed(w.nodes, step.typ, node, step.data); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^node "([^"]*)" is suspended by node-topology$`, func(node string) error {
		return w.feed(w.nodes, typeNodeSuspended, node, fmt.Sprintf(`{"nodeId":%q}`, node))
	})
	sc.Step(`^item "([^"]*)" has a case of (\d+) eaches with GTIN "([^"]*)" and each GTIN "([^"]*)"$`, func(item string, units int, gtin, each string) error {
		data := fmt.Sprintf(`{"catalogItemId":%q,"level":"CASE","gtin":%q,"childGtin":%q,"unitsPerPack":%d,"declaredAt":"2026-10-24T12:00:00Z"}`, item, gtin, each, units)
		return w.feed(w.catalg, typePackaging, item, data)
	})
}

func registerGivenShipment(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the plan has an open shipment$`, func() error {
		if err := w.write(http.MethodPost, "/inbound-plans/{plan}/shipments", ""); err != nil || w.status != http.StatusCreated {
			return fmt.Errorf("open shipment: %v %d %s", err, w.status, w.body)
		}
		return nil
	})
	sc.Step(`^the shipment has (\d+) case cartons? of (\d+) eaches of "([^"]*)" with GTIN "([^"]*)"$`, func(n, qty int, item, gtin string) error {
		for range n {
			if err := w.write(http.MethodPost, "/inbound-shipments/{shipment}/cartons", caseCarton(item, gtin, qty)); err != nil || w.status != http.StatusCreated {
				return fmt.Errorf("add carton: %v %d %s", err, w.status, w.body)
			}
		}
		return nil
	})
	sc.Step(`^the shipment is labelled and shipped$`, func() error {
		for _, path := range []string{"/inbound-shipments/{shipment}:label", "/inbound-shipments/{shipment}:ship"} {
			if err := w.write(http.MethodPost, path, ""); err != nil || w.status != http.StatusOK {
				return fmt.Errorf("%s: %v %d %s", path, err, w.status, w.body)
			}
		}
		return nil
	})
}

func caseCarton(item, gtin string, qty int) string {
	return fmt.Sprintf(`{"kind":"HOMOGENEOUS_CASE","contents":[{"catalogItemId":%q,"gtin":%q,"qtyEaches":%d}],"grossWeightG":5200,"dimsMm":{"l":400,"w":300,"h":200}}`, item, gtin, qty)
}

func (w *world) activeNode(node, inbound string) error {
	reg := fmt.Sprintf(`{"nodeId":%q,"nodeType":"FULFILLMENT_CENTER"}`, node)
	prof := fmt.Sprintf(`{"nodeId":%q,"profileRevision":1%s}`, node, inbound)
	for _, step := range []struct{ typ, data string }{{typeNodeRegistered, reg}, {typeProfileDeclared, prof}, {typeNodeActivated, fmt.Sprintf(`{"nodeId":%q}`, node)}} {
		if err := w.feed(w.nodes, step.typ, node, step.data); err != nil {
			return err
		}
	}
	return nil
}

func registerWhen(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I send GET "([^"]*)"$`, func(path string) error { return w.request(http.MethodGet, w.expand(path), "") })
	sc.Step(`^I send POST "([^"]*)"$`, func(path string) error { return w.write(http.MethodPost, path, "") })
	sc.Step(`^I send POST "([^"]*)" with:$`, func(path string, body *godog.DocString) error {
		return w.write(http.MethodPost, path, body.Content)
	})
	sc.Step(`^the vendor reports this receipt of the shipment:$`, func(doc *godog.DocString) error {
		w.feedErr = w.feed(w.receip, typeReceipt, w.shipmentID, w.expand(doc.Content))
		return nil
	})
	sc.Step(`^the same receipt event is delivered twice:$`, func(doc *godog.DocString) error {
		msg := cloudEvent("evt-dup", typeReceipt, w.shipmentID, w.expand(doc.Content))
		for range 2 {
			if err := w.receip.HandleMessage(context.Background(), msg); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the service clock advances (\d+) days?$`, func(days int) error {
		w.clock.Advance(time.Duration(days) * 24 * time.Hour)
		return nil
	})
	sc.Step(`^the vendor reports a receipt for the unknown shipment "([^"]*)"$`, func(id string) error {
		data := fmt.Sprintf(`{"shipmentId":%q,"nodeId":"n-1","receivedAt":"2026-10-25T09:30:00Z","cartons":[]}`, id)
		w.feedErr = w.feed(w.receip, typeReceipt, id, data)
		return nil
	})
}

func registerThen(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the response status is (\d+)$`, func(code int) error {
		if w.status != code {
			return fmt.Errorf("status = %d, want %d: %s", w.status, code, w.body)
		}
		return nil
	})
	sc.Step(`^the problem type is "([^"]*)"$`, func(slug string) error {
		v, ok, err := w.field("type")
		if err != nil || !ok || render(v) != slug {
			return fmt.Errorf("problem type = %v (%v), want %s: %s", v, err, slug, w.body)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" is "([^"]*)"$`, func(path, want string) error {
		v, ok, err := w.field(path)
		want = w.expand(want)
		if err != nil || !ok || render(v) != want {
			return fmt.Errorf("%s = %v (found %v, %v), want %s: %s", path, v, ok, err, want, w.body)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" is absent$`, func(path string) error {
		if _, ok, _ := w.field(path); ok {
			return fmt.Errorf("%s is present: %s", path, w.body)
		}
		return nil
	})
	sc.Step(`^the response lists (\d+) items in "([^"]*)"$`, func(n int, path string) error {
		v, ok, err := w.field(path)
		list, isList := v.([]any)
		if err != nil || !ok || !isList || len(list) != n {
			return fmt.Errorf("%s = %v, want %d items: %s", path, v, n, w.body)
		}
		return nil
	})
	sc.Step(`^the outbox event types are "([^"]*)"$`, func(want string) error {
		if got := strings.Join(w.eventNames(), ", "); got != want {
			return fmt.Errorf("outbox events = %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^the receipt is dead-lettered as undeliverable$`, func() error {
		if w.feedErr == nil {
			return fmt.Errorf("the consumer accepted a receipt for a shipment it does not know")
		}
		return nil
	})
}
