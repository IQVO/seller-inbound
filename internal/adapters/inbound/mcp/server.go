// Package mcp is the inbound Model Context Protocol adapter: it exposes this
// bounded context to the AI ecosystem as a second driving adapter over the
// same application-layer read use cases the REST adapter uses. It is built on
// the official MCP Go SDK and served over Streamable HTTP only.
//
// Per docs/adr/0006-read-only-mcp-server.md the surface is READ-ONLY: every
// tool reads seller-inbound data (get_seller_scorecard, get_inbound_plan,
// get_shipment_labels, get_inbound_shipment); onboarding a seller, the plan
// lifecycle, packing cartons, labelling and shipping stay on REST and on the
// outbox, and governance_test.go fails the build on a write-verb tool name
// and on a write seam referenced from this package. This package depends
// inward on the application layer and the domain only, never on an outbound
// adapter or the REST adapter. The composition root (cmd/mcp) wires concrete
// repositories into the use cases. There is no auth of any kind (fleet-wide
// revert 2026-09-11): access control is the in-cluster ClusterIP boundary.
package mcp

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverName and serverVersion identify this server in the MCP initialize
// handshake.
const (
	serverName    = "seller-inbound-mcp"
	serverVersion = "1.0.0"
)

// instructions is what an MCP client is told about this server at initialize.
const instructions = "Seller Inbound of the retail network (read-only): the seller-facing front door for stock coming into the network. " +
	"Answer a seller's rolling-window compliance scorecard with get_seller_scorecard (window 7d or 30d, default 30d: shipments closed, " +
	"receipt accuracy, discrepancies by kind, compliance violations and the derived GOOD / WATCH / AT_RISK tier, which is informational " +
	"only). Read one inbound plan with its lines and state (DRAFT, CONFIRMED, CANCELLED) with get_inbound_plan, read one inbound shipment " +
	"with its cartons (each identified by an SSCC) and its state (PLANNED, PACKING, LABELLED, SHIPPED, RECEIVED) with get_inbound_shipment, " +
	"and read the label data of a labelled shipment's cartons (SSCC, GS1-128 element string, contents in eaches, destination node) with " +
	"get_shipment_labels. Nothing here changes data: onboarding and suspending sellers, planning, confirming, cancelling, packing cartons, " +
	"labelling and shipping are REST operations of seller-inbound."

// NewServer builds the MCP server for this bounded context with every tool
// registered.
func NewServer(deps Deps) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		&mcp.ServerOptions{Instructions: instructions},
	)
	deps.registerTools(server)
	return server
}

// Handler returns the Streamable HTTP handler for the MCP server.
func Handler(server *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
}
