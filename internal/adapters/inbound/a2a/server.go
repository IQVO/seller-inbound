// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the agent ecosystem as a third driving adapter over the same
// application-layer read use cases the REST and MCP adapters use. It is built
// on the official A2A Go SDK (a2aproject/a2a-go/v2) and speaks JSON-RPC 2.0
// over HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0009-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between agents: a client sends a goal-shaped skill request as a structured
// message part and this agent answers from its own state through its own use
// cases. It is not a data-sync channel: facts still travel exclusively as
// CloudEvents on Kafka (ADR 0004), published through the transactional outbox.
//
// This agent is READ-ONLY, exactly like the MCP adapter (ADR 0006): both
// skills read seller-inbound data. Onboarding a seller, the plan lifecycle,
// packing cartons, labelling and shipping stay on REST and the outbox. This
// package never writes and contains no outbound A2A client.
//
// Like the MCP adapter, this package depends inward on the application layer
// and the domain only, never on an outbound adapter or the REST adapter. The
// composition root (cmd/a2a) wires the concrete repositories into the use
// cases. Domain structs and application views never leak across the message
// boundary.
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/seller-inbound/internal/application/views"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL agent clients can actually dial
// (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Reads is the read-only slice of the application service the A2A skills
// need: three of the four read use cases the REST and MCP adapters expose
// (GetSellerScorecard, GetInboundPlan, GetInboundShipment); *usecases.Service
// implements it. The adapter holds this and nothing that can write
// (docs/adr/0009): the composition root wires no write seam at all.
type Reads interface {
	GetSellerScorecard(ctx context.Context, sellerID, window string) (views.SellerScorecard, error)
	GetInboundPlan(ctx context.Context, planID string) (views.Plan, error)
	GetInboundShipment(ctx context.Context, shipmentID string) (views.Shipment, error)
}

// Deps is everything the A2A skills need, injected by the composition root.
type Deps struct {
	Reads Reads
}

// Card builds this agent's Agent Card (ADR 0009). The surface is two curated,
// goal-shaped READ skills derived from the MCP tools get_seller_scorecard,
// get_inbound_plan and get_inbound_shipment (ADR 0006): this context's MCP
// charter is entirely read-only, so the agent is read-only too and never
// widens what the context exposes. The card declares no security schemes on
// purpose: this surface is unauthenticated by the same fleet-wide decision as
// REST and MCP; schemes are added here, not in a redesign, if that decision is
// ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "seller-inbound",
		Description: "Seller Inbound agent of the retail network (read-only): the seller-facing front door for stock coming into the network. It answers how compliant a seller is (the rolling-window compliance scorecard and its informational GOOD / WATCH / AT_RISK tier) and where an inbound stands (an inbound plan with its declared lines, or an inbound shipment with its cartons). It never changes data: onboarding or suspending sellers, planning, packing, labelling and shipping stay on the REST API.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/seller-inbound",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillSellerCompliance,
				Name:        "Assess a seller's compliance",
				Description: "Answer one seller's rolling-window compliance scorecard from the facts this service recorded: shipments received in the window, cartons reported and received, receiptAccuracyRate (omitted when nothing was reported), discrepancies by kind, discrepancyRatePerShipment (omitted when no shipment closed), compliance violations and the derived complianceTier (GOOD, WATCH or AT_RISK), which is informational only. Arguments: sellerId (required) and window (optional: 7d or 30d, default 30d). Answers a 404 problem for an unknown seller and a 400 problem for another window. Persists nothing.",
				Tags:        []string{"seller", "compliance", "scorecard", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"seller_compliance","arguments":{"sellerId":"seller-77"}}`,
					`{"skill":"seller_compliance","arguments":{"sellerId":"seller-77","window":"7d"}}`,
				},
			},
			{
				ID:          skillInboundStatus,
				Name:        "Check where an inbound stands",
				Description: "Read the state of one inbound: pass planId for an inbound plan (seller, destination node, packing option, state DRAFT / CONFIRMED / CANCELLED and the declared lines in eaches) or shipmentId for an inbound shipment (plan, seller, destination node, state PLANNED / PACKING / LABELLED / SHIPPED / RECEIVED and every carton packed so far with its SSCC, contents and receipt status). Exactly one of the two is required. Answers a 404 problem when the id is unknown. Persists nothing.",
				Tags:        []string{"inbound", "plan", "shipment", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"inbound_status","arguments":{"planId":"pln-0f3a91"}}`,
					`{"skill":"inbound_status","arguments":{"shipmentId":"shp-0f3a91"}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response reads, so
// there is no long-running Task lifecycle to track in this first A2A surface.
// The response carries the caller's contextId (and taskId when the client
// referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler, the only transport this
// agent ships (ADR 0009), mirroring ADR 0006's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
