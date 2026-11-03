package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mimeTypeJSON is the MIME type of every resource this server serves.
const mimeTypeJSON = "application/json"

// scorecardURITemplate is the ONE resource this server offers (Agentic
// Standard v2, AS-3): a scoped read model this context owns, not a dump of a
// table. It answers "how compliant is this seller over the default rolling
// window?" for ONE seller, built from the same GetSellerScorecard read use
// case the get_seller_scorecard tool and GET /sellers/{sellerId}/scorecard
// use (the default 30d window; the 7d variant stays a tool argument, a URI
// cannot carry it). Bounded by construction: one seller, the scorecard's
// fixed fields, JSON text.
const scorecardURITemplate = "warehouse://seller-inbound/scorecards/{sellerId}"

// registerResources adds the resource surface (read-only by nature, L1; the
// charter keeps resources context data an agent can load without a tool
// call). No resource ever writes: Deps holds read use cases only (ADR 0006).
func (d Deps) registerResources(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: scorecardURITemplate,
		Name:        "seller compliance scorecard",
		Description: "The default-window (30d) compliance scorecard of one seller: shipments received, cartons reported and received, receipt accuracy, discrepancies by kind and the derived informational tier (GOOD, WATCH or AT_RISK). An unknown seller is an error.",
		MIMEType:    mimeTypeJSON,
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		sellerID, err := parseScorecardURI(req.Params.URI)
		if err != nil {
			return nil, err
		}
		v, err := d.Reads.GetSellerScorecard(ctx, sellerID, "")
		if err != nil {
			return nil, mapError(ctx, err)
		}
		return resourceResult(req.Params.URI, toScorecard(v))
	})
}

// resourceResult marshals one read model into the resource's text contents.
func resourceResult(uri string, view any) (*mcp.ReadResourceResult, error) {
	body, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: mimeTypeJSON,
			Text:     string(body),
		}},
	}, nil
}

// parseScorecardURI extracts the sellerId from a
// warehouse://seller-inbound/scorecards/{sellerId} URI. The id is
// model-supplied and therefore untrusted: a malformed URI or an empty id is
// rejected rather than silently defaulted.
func parseScorecardURI(uri string) (string, error) {
	const scheme = "warehouse://seller-inbound/scorecards/"
	rest, ok := strings.CutPrefix(uri, scheme)
	if !ok {
		return "", fmt.Errorf("resource uri %q must start with %q", uri, scheme)
	}
	if rest == "" || strings.Contains(rest, "/") {
		return "", fmt.Errorf("resource uri %q must carry exactly one non-empty sellerId after %q", uri, scheme)
	}
	return rest, nil
}
