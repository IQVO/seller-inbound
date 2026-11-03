// The MCP surface beyond tools (Agentic Standard v2, AS-3): ONE resource —
// the scoped seller-compliance scorecard read model — and ONE prompt — this
// context's operator SOP. Pinned through the same in-memory client the tool
// tests use.
package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The resource template is advertised with the fleet URI shape and reads the
// same read model as the get_seller_scorecard tool (default 30d window).
func TestSurface_ResourceTemplateListedAndRead(t *testing.T) {
	h, w := withWorld(t)
	ctx := t.Context()

	listed, err := h.session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	found := false
	for _, rt := range listed.ResourceTemplates {
		if rt.URITemplate == "warehouse://seller-inbound/scorecards/{sellerId}" {
			found = true
			if rt.MIMEType != "application/json" {
				t.Errorf("template MIMEType = %q, want application/json", rt.MIMEType)
			}
		}
	}
	if !found {
		t.Fatalf("scorecard resource template not advertised: %+v", listed.ResourceTemplates)
	}

	res, err := h.session.ReadResource(ctx, &sdk.ReadResourceParams{
		URI: "warehouse://seller-inbound/scorecards/" + w.sellerID,
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d entries, want 1", len(res.Contents))
	}
	c := res.Contents[0]
	if c.URI != "warehouse://seller-inbound/scorecards/"+w.sellerID {
		t.Errorf("content URI = %q", c.URI)
	}
	var scorecard struct {
		SellerID         string         `json:"sellerId"`
		Window           string         `json:"window"`
		ComplianceTier   string         `json:"complianceTier"`
		DiscrepanciesMap map[string]int `json:"discrepanciesByKind"`
	}
	if err := json.Unmarshal([]byte(c.Text), &scorecard); err != nil {
		t.Fatalf("resource text is not the scorecard JSON: %v (%s)", err, c.Text)
	}
	if scorecard.SellerID != w.sellerID || scorecard.Window != "30d" || scorecard.ComplianceTier != "GOOD" || len(scorecard.DiscrepanciesMap) != 0 {
		t.Errorf("scorecard = %+v, want seller %s, window 30d, tier GOOD, no discrepancies", scorecard, w.sellerID)
	}
}

// A malformed URI and an unknown seller are errors, never silent defaults.
func TestSurface_ResourceRejectsMalformedAndUnknown(t *testing.T) {
	h, _ := withWorld(t)
	for _, uri := range []string{
		"staffing://seller-inbound/scorecards/seller-77",        // wrong scheme
		"warehouse://seller-inbound/shipments/seller-77",        // wrong model
		"warehouse://seller-inbound/scorecards/",                // empty sellerId
		"warehouse://seller-inbound/scorecards/seller-77/extra", // trailing segment
		"warehouse://seller-inbound/scorecards/seller-nobody",   // unknown seller
	} {
		if _, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read resource %q: expected an error", uri)
		}
	}
}

// The prompt is advertised with its typed argument and returns the SOP text.
func TestSurface_PromptListedAndGet(t *testing.T) {
	h, w := withWorld(t)
	ctx := t.Context()

	listed, err := h.session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(listed.Prompts) != 1 || listed.Prompts[0].Name != "assess_seller_compliance" {
		t.Fatalf("prompts = %+v, want exactly assess_seller_compliance", listed.Prompts)
	}
	if len(listed.Prompts[0].Arguments) != 1 || listed.Prompts[0].Arguments[0].Name != "sellerId" || !listed.Prompts[0].Arguments[0].Required {
		t.Fatalf("prompt arguments = %+v, want one required sellerId", listed.Prompts[0].Arguments)
	}

	res, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{
		Name:      "assess_seller_compliance",
		Arguments: map[string]string{"sellerId": w.sellerID},
	})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(res.Messages))
	}
	text := res.Messages[0].Content.(*sdk.TextContent).Text
	for _, needle := range []string{"get_seller_scorecard", "get_inbound_plan", "get_inbound_shipment", w.sellerID} {
		if !strings.Contains(text, needle) {
			t.Errorf("SOP text does not mention %q", needle)
		}
	}

	// A missing argument degrades to the placeholder, never to a hidden call.
	noArg, err := h.session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "assess_seller_compliance"})
	if err != nil {
		t.Fatalf("get prompt without arguments: %v", err)
	}
	if plain := noArg.Messages[0].Content.(*sdk.TextContent).Text; !strings.Contains(plain, "<sellerId>") {
		t.Errorf("SOP without an argument should carry the <sellerId> placeholder")
	}
}
