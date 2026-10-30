package a2a

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

const testAgentURL = "http://seller-inbound-a2a.warehouse-systems.svc.cluster.local:8093/"

func testCard() *a2a.AgentCard {
	return Card(Config{AgentURL: testAgentURL, Version: "1.0.0"})
}

// TestCardShape pins the identity, interface and capability surface of the
// Agent Card: the discovery contract other agents program against.
func TestCardShape(t *testing.T) {
	card := testCard()

	if card.Name != "seller-inbound" {
		t.Errorf("card name = %q, want seller-inbound", card.Name)
	}
	if len(card.SupportedInterfaces) != 1 {
		t.Fatalf("card has %d interfaces, want 1", len(card.SupportedInterfaces))
	}
	iface := card.SupportedInterfaces[0]
	if iface.URL != testAgentURL || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("interface = %+v, want JSONRPC at the configured agent URL", iface)
	}
	if card.Capabilities.Streaming {
		t.Error("streaming must not be advertised: this is a message-only agent")
	}
	if card.SecuritySchemes != nil {
		t.Error("card must declare no security schemes: the surface is unauthenticated fleet-wide")
	}
	if card.Provider == nil || card.Provider.Org != "IQVO" {
		t.Errorf("provider = %+v, want IQVO", card.Provider)
	}
}

// TestCardSkillsAreCuratedAndTruthful pins the two-skill, READ-ONLY surface
// and proves the card never lies about its own contract: every advertised
// example must parse under the SAME strict decoder the executor uses, name its
// own skill, and carry arguments that skill accepts.
func TestCardSkillsAreCuratedAndTruthful(t *testing.T) {
	want := map[string]bool{skillSellerCompliance: true, skillInboundStatus: true}
	card := testCard()
	if len(card.Skills) != len(want) {
		t.Fatalf("card has %d skills, want exactly %d (curated, goal-shaped, not one per endpoint)", len(card.Skills), len(want))
	}
	for _, s := range card.Skills {
		if !want[s.ID] {
			t.Errorf("unexpected skill %q on the card", s.ID)
			continue
		}
		if s.Name == "" || s.Description == "" || len(s.Examples) == 0 {
			t.Errorf("skill %q lacks a name, description or example", s.ID)
		}
		if !containsTag(s.Tags, "read") || containsTag(s.Tags, "write") {
			t.Errorf("skill %q must be tagged read and never write (this context's MCP surface is read-only), tags = %v", s.ID, s.Tags)
		}
		for _, ex := range s.Examples {
			assertExampleAccepted(t, s.ID, ex)
		}
	}
}

func assertExampleAccepted(t *testing.T, id, example string) {
	t.Helper()
	var req skillRequest
	if err := decodeStrict([]byte(example), &req); err != nil {
		t.Errorf("skill %q example is not valid under the strict decoder: %v", id, err)
		return
	}
	if req.Skill != id {
		t.Errorf("skill %q example names skill %q", id, req.Skill)
		return
	}
	var err error
	switch id {
	case skillSellerCompliance:
		err = decodeStrict(req.Arguments, &sellerComplianceArgs{})
	case skillInboundStatus:
		err = decodeStrict(req.Arguments, &inboundStatusArgs{})
	}
	if err != nil {
		t.Errorf("skill %q example arguments are rejected by its own strict decoder: %v", id, err)
	}
}

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

// TestCardNeverClaimsToWrite keeps the card honest about the read-only charter:
// no skill id may start with a write verb of this service, and the card says
// it is read-only.
func TestCardNeverClaimsToWrite(t *testing.T) {
	writeVerbs := []string{
		"onboard_", "suspend_", "reinstate_", "create_", "confirm_", "cancel_", "add_", "label_", "ship_",
		"update_", "delete_", "set_", "apply_", "record_",
	}
	card := testCard()
	for _, s := range card.Skills {
		for _, verb := range writeVerbs {
			if strings.HasPrefix(s.ID, verb) {
				t.Errorf("skill %q looks like a write; this agent is read-only", s.ID)
			}
		}
	}
	if !strings.Contains(card.Description, "read-only") {
		t.Errorf("the card description must say the agent is read-only: %q", card.Description)
	}
}

// TestCardSerializable proves the card marshals to JSON a client can parse
// back, with the discovery fields intact.
func TestCardSerializable(t *testing.T) {
	raw, err := json.Marshal(testCard())
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var back a2a.AgentCard
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("card JSON does not round-trip: %v", err)
	}
	if back.Name != "seller-inbound" || len(back.Skills) != 2 {
		t.Errorf("round-tripped card = %q with %d skills", back.Name, len(back.Skills))
	}
}
