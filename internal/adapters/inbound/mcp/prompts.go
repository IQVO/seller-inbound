package mcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// assessSellerComplianceSOP is the operator standard-operating-procedure the
// assess_seller_compliance prompt hands to the model (Agentic Standard v2,
// AS-3). Per the charter, prompts encode how to interpret results, when to
// escalate and what "done" means; this one names tools but NEVER calls them,
// and it cannot write: every tool it names is read-only (ADR 0006) and every
// write of this context is a REST operation.
const assessSellerComplianceSOP = `You are assessing the inbound compliance of one seller of the retail network. Use only the MCP tools of seller-inbound; never assume state — read it.

Procedure:
1. Call get_seller_scorecard(sellerId = SELLER, window = "7d" or "30d"; omitted means 30d). The scorecard is this service's own recorded facts: shipments received in the window, cartons reported and how many were RECEIVED, receiptAccuracyRate (omitted when nothing was reported — never read an omission as 100%), discrepancies by kind, discrepancyRatePerShipment (omitted when no shipment closed), compliance violations and the derived complianceTier.
2. When discrepancies dominate, trace the concrete inbounds: get_inbound_plan(planId) for what was declared (lines in eaches, packing option, state DRAFT / CONFIRMED / CANCELLED) and get_inbound_shipment(shipmentId) for what was packed (every carton by SSCC, with receipt status per carton). get_shipment_labels(shipmentId) gives the label data of a labelled shipment when you need the GS1-128 detail.

Interpretation:
- complianceTier (GOOD, WATCH or AT_RISK) is informational only (ADR 0005): it summarises the window's facts, it does not decide anything and neither do you.
- A discrepancy kind with a high count points at a repeat packaging or labelling problem; a low receiptAccuracyRate with many cartons reported points at handling losses. Distinguish them before reporting.
- An unknown seller or shipment id is a not-found answer, not evidence of non-compliance.

Escalate to the human account manager when: the tier is AT_RISK, or compliance violations repeat across both windows. Suspending or reinstating a seller, correcting plans, repacking or relabelling are human/REST actions — nothing in this server can perform them, so do not promise them.

Done means: you have reported the seller's scorecard for the window asked (counts, rates that exist, discrepancies by kind, tier), traced at least the worst discrepancies to their plan or shipment, and named the escalation when the tier or the repetition warrants it.`

// registerPrompts adds the workflow prompt (the operator SOP of this
// context). Read-only by construction: a prompt returns text.
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "assess_seller_compliance",
		Description: "Standard operating procedure for assessing one seller's inbound compliance: read the rolling-window scorecard, trace discrepancies to their plans and shipments, and know when to escalate to the human account manager.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "sellerId",
			Description: "the seller id whose compliance to assess, e.g. seller-77",
			Required:    true,
		}},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		sellerID := strings.TrimSpace(req.Params.Arguments["sellerId"])
		if sellerID == "" {
			sellerID = "<sellerId>"
		}
		return &mcp.GetPromptResult{
			Description: "How to assess seller compliance: read the scorecard for the window, trace discrepancies to plans and shipments, escalate AT_RISK to the human account manager.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: strings.ReplaceAll(assessSellerComplianceSOP, "SELLER", sellerID)},
			}},
		}, nil
	})
}
