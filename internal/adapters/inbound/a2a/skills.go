package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/claudioed/seller-inbound/internal/application/ports"
	"github.com/claudioed/seller-inbound/internal/domain/scorecard"
	"github.com/claudioed/seller-inbound/internal/domain/shared"
)

// tracerName is the OTel instrumentation scope for A2A skill spans, mirroring
// the MCP adapter's mcp.tool spans.
const tracerName = "github.com/claudioed/seller-inbound/internal/adapters/inbound/a2a"

// Skill ids advertised on the Agent Card (see Card). They are goal-shaped, not
// endpoint-shaped: seller_compliance answers get_seller_scorecard, and
// inbound_status answers get_inbound_plan or get_inbound_shipment by the id
// it is given (ADR 0009).
const (
	skillSellerCompliance = "seller_compliance"
	skillInboundStatus    = "inbound_status"
)

// skillRequest is the wire contract of every skill call: the first message
// part carries (as a Data part, or a Text part holding the same JSON) the
// skill id and its arguments. Strict on purpose: an agent client is
// machine-to-machine, so unknown fields are rejected, never ignored, and a
// misspelled name like "windows" must not silently fall back to the 30d
// default. (Go's encoding/json matches field names case-insensitively, so
// "sellerid" still reaches sellerId; only genuinely unknown names are
// refused.)
type skillRequest struct {
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

// sellerComplianceArgs and inboundStatusArgs are the per-skill argument
// shapes; the keys are the REST/MCP names.
type sellerComplianceArgs struct {
	SellerID string `json:"sellerId"`
	Window   string `json:"window"`
}

type inboundStatusArgs struct {
	PlanID     string `json:"planId"`
	ShipmentID string `json:"shipmentId"`
}

// answer maps one incoming message to a skill and returns the agent's reply
// Message. It is the whole executor body: this is a message-only agent, so a
// skill run is exactly one request mapped to one answer (ADR 0009).
func answer(ctx context.Context, deps Deps, ec *a2asrv.ExecutorContext) a2a.Event {
	req, problem := parseSkillRequest(ec)
	if problem != nil {
		return reply(ec, envelope{Skill: "", Problem: problem}, problem.summary())
	}

	ctx, span := otel.Tracer(tracerName).Start(ctx, "a2a.skill "+req.Skill)
	defer span.End()
	span.SetAttributes(attribute.String("a2a.skill", req.Skill))

	dto, problem := run(ctx, deps, req)
	if problem != nil {
		span.SetStatus(codes.Error, problem.Detail)
		return reply(ec, envelope{Skill: req.Skill, Problem: problem}, problem.summary())
	}
	return reply(ec, envelope{Skill: req.Skill, Data: dto}, dto.Summary())
}

// run dispatches one validated request to its skill.
func run(ctx context.Context, deps Deps, req skillRequest) (skillDTO, *problem) {
	switch req.Skill {
	case skillSellerCompliance:
		return deps.sellerCompliance(ctx, req.Arguments)
	case skillInboundStatus:
		return deps.inboundStatus(ctx, req.Arguments)
	default:
		return skillDTO{}, problemUnknownSkill(req.Skill)
	}
}

// reply builds the agent Message for one skill run: a Data part carrying the
// structured envelope and a Text part carrying the one-line summary, bound to
// the caller's context (and task, when the client referenced one).
func reply(ec *a2asrv.ExecutorContext, env envelope, summary string) a2a.Event {
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, ec, a2a.NewDataPart(env), a2a.NewTextPart(summary))
}

// parseSkillRequest extracts the skillRequest from the message's first part.
// A Data part (or a Text part holding the same JSON object) is accepted;
// anything else is a request-shape problem, answered, not raised, because a
// malformed delegation is a business answer for the calling agent, not a
// transport failure.
func parseSkillRequest(ec *a2asrv.ExecutorContext) (skillRequest, *problem) {
	msg := ec.Message
	if msg == nil || len(msg.Parts) == 0 {
		return skillRequest{}, problemMalformedRequest("the message carries no parts")
	}
	raw, err := partJSON(msg.Parts[0])
	if err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the first message part is not a skill request: %v", err))
	}
	var req skillRequest
	if err := decodeStrict(raw, &req); err != nil {
		return skillRequest{}, problemMalformedRequest(fmt.Sprintf("the skill request is not valid: %v", err))
	}
	if req.Skill == "" {
		return skillRequest{}, problemMalformedRequest(`the skill request carries no "skill" field`)
	}
	return req, nil
}

// partJSON renders one part's content as the JSON bytes of a skill request.
func partJSON(part *a2a.Part) ([]byte, error) {
	switch content := part.Content.(type) {
	case a2a.Data:
		return json.Marshal(content.Value)
	case a2a.Text:
		return []byte(string(content)), nil
	default:
		return nil, errors.New("expected a data part (or a text part holding the skill JSON)")
	}
}

// decodeStrict decodes one JSON document into dst, rejecting unknown fields
// and trailing data. Absent arguments decode as the empty object so each
// skill's own required-field checks produce the specific problem.
func decodeStrict(raw []byte, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected data after the JSON document")
	}
	return nil
}

// sellerCompliance is the compliance read skill, through the same
// GetSellerScorecard use case the REST and MCP adapters use. window defaults
// to 30d inside the use case.
func (d Deps) sellerCompliance(ctx context.Context, rawArgs json.RawMessage) (skillDTO, *problem) {
	var args sellerComplianceArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	if strings.TrimSpace(args.SellerID) == "" {
		return skillDTO{}, problemInvalidArgument("sellerId is required")
	}
	v, err := d.Reads.GetSellerScorecard(ctx, args.SellerID, args.Window)
	if err != nil {
		return skillDTO{}, problemFromUseCase(err, lookup{what: "seller", idName: "sellerId", id: args.SellerID, window: args.Window})
	}
	dto := toScorecardDTO(v)
	return skillDTO{Scorecard: &dto, summary: summarizeScorecard(dto)}, nil
}

// inboundStatus is the inbound read skill: planId reads an inbound plan,
// shipmentId an inbound shipment, through the same GetInboundPlan and
// GetInboundShipment use cases the REST and MCP adapters use. Exactly one id
// is required.
func (d Deps) inboundStatus(ctx context.Context, rawArgs json.RawMessage) (skillDTO, *problem) {
	var args inboundStatusArgs
	if err := decodeStrict(rawArgs, &args); err != nil {
		return skillDTO{}, problemInvalidArgument(fmt.Sprintf("arguments: %v", err))
	}
	planID, shipmentID := strings.TrimSpace(args.PlanID), strings.TrimSpace(args.ShipmentID)
	switch {
	case planID == "" && shipmentID == "":
		return skillDTO{}, problemInvalidArgument("exactly one of planId or shipmentId is required")
	case planID != "" && shipmentID != "":
		return skillDTO{}, problemInvalidArgument("pass either planId or shipmentId, not both")
	case planID != "":
		return d.planStatus(ctx, args.PlanID)
	default:
		return d.shipmentStatus(ctx, args.ShipmentID)
	}
}

func (d Deps) planStatus(ctx context.Context, planID string) (skillDTO, *problem) {
	v, err := d.Reads.GetInboundPlan(ctx, planID)
	if err != nil {
		return skillDTO{}, problemFromUseCase(err, lookup{what: "inbound plan", idName: "planId", id: planID})
	}
	dto := toPlanDTO(v)
	return skillDTO{Plan: &dto, summary: summarizePlan(dto)}, nil
}

func (d Deps) shipmentStatus(ctx context.Context, shipmentID string) (skillDTO, *problem) {
	v, err := d.Reads.GetInboundShipment(ctx, shipmentID)
	if err != nil {
		return skillDTO{}, problemFromUseCase(err, lookup{what: "inbound shipment", idName: "shipmentId", id: shipmentID})
	}
	dto := toShipmentDTO(v)
	return skillDTO{Shipment: &dto, summary: summarizeShipment(dto)}, nil
}

// lookup names what a skill was asked to read, so a problem can speak about
// it in adapter-owned words.
type lookup struct {
	what, idName, id, window string
}

// problemFromUseCase maps a use-case error to a problem answer, using the SAME
// classification as the REST adapter (internal/adapters/inbound/http
// problemCatalogue) for the errors a read can raise: a bad window is 400
// invalid-window, a missing aggregate is 404 not-found, any other value
// validation failure is 422 validation-failed and anything unrecognised is
// 500 internal-error. (The REST adapter's 409 classes belong to writes this
// read-only agent does not offer; its 503 is the readiness probe's, not a
// use-case outcome.) The detail is adapter-owned text, so shared error text
// never reaches an agent client; the 500 never leaks the internal error.
func problemFromUseCase(err error, l lookup) *problem {
	switch {
	case errors.Is(err, scorecard.ErrInvalidWindow):
		return &problem{Type: "invalid-window", Title: "The window must be 7d or 30d", Status: 400,
			Detail: fmt.Sprintf("window %q is not supported; use 7d or 30d (omit it for 30d)", l.window)}
	case errors.Is(err, ports.ErrNotFound):
		return &problem{Type: "not-found", Title: "Not found", Status: 404,
			Detail: fmt.Sprintf("no %s exists with %s %q", l.what, l.idName, l.id)}
	case errors.Is(err, shared.ErrValidation):
		return &problem{Type: "validation-failed", Title: "Validation failed", Status: 422, Detail: err.Error()}
	default:
		slog.Error("a2a skill failed", "error", err)
		return &problem{Type: "internal-error", Title: "Internal server error", Status: 500,
			Detail: "the skill failed; see server logs (trace id in the response metadata)"}
	}
}
