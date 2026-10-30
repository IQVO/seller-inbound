package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/claudioed/seller-inbound/internal/adapters/inbound/a2a"
)

const testAgentURL = "http://seller-inbound-a2a.warehouse-systems.svc.cluster.local:8093/"

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestServer serves the real router over the real A2A handler. No skill
// can reach a database: the zero Deps is enough for the transport tests
// (an unknown skill is answered before any use case runs).
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newAgent(inbounda2a.Deps{}, testAgentURL, "test", quietLogger(), "seller-inbound-a2a"))
	t.Cleanup(srv.Close)
	return srv
}

func TestRouter_HealthzIsOpenAndServesOK(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %q, want 200 {\"status\":\"ok\"}", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

// The Agent Card is served at the well-known path and advertises the
// configured agent URL, not the listener's.
func TestRouter_ServesTheAgentCardAtTheWellKnownPath(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET agent card = %d, want 200", resp.StatusCode)
	}
	var card a2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatalf("card is not JSON: %v", err)
	}
	if card.Name != "seller-inbound" || len(card.Skills) != 2 || len(card.SupportedInterfaces) != 1 ||
		card.SupportedInterfaces[0].URL != testAgentURL || card.SecuritySchemes != nil {
		t.Fatalf("card = %+v", card)
	}
}

// Both mount points speak JSON-RPC, with no credentials: a real SDK client
// built from the card reaches the executor on "/" and on "/a2a".
func TestJSONRPC_RootAndA2APathsAnswerSkills(t *testing.T) {
	srv := newTestServer(t)
	for _, path := range []string{"/", "/a2a"} {
		card := inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + path, Version: "test"})
		client, err := a2aclient.NewFromCard(context.Background(), card)
		if err != nil {
			t.Fatalf("%s: NewFromCard: %v", path, err)
		}
		part := a2a.NewDataPart(map[string]any{"skill": "no_such_skill", "arguments": map[string]any{}})
		resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
		if err != nil {
			t.Fatalf("%s: SendMessage: %v", path, err)
		}
		msg, ok := resp.(*a2a.Message)
		if !ok || len(msg.Parts) != 2 {
			t.Fatalf("%s: reply = %T", path, resp)
		}
		raw, _ := json.Marshal(msg.Parts[0].Content.(a2a.Data).Value)
		if !bytes.Contains(raw, []byte(`"type":"unknown-skill"`)) || !bytes.Contains(raw, []byte(`"status":400`)) {
			t.Fatalf("%s: answer = %s, want the in-band unknown-skill problem", path, raw)
		}
	}
}

// The binary reads no credential: requests carrying (or lacking) any
// Authorization header behave identically.
func TestNoAuthIsRequired(t *testing.T) {
	srv := newTestServer(t)
	for _, auth := range []string{"", "Bearer nonsense"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/.well-known/agent-card.json", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("agent card with Authorization=%q = %d, want 200", auth, resp.StatusCode)
		}
	}
}

func TestGetenv(t *testing.T) {
	t.Setenv("SI_A2A_TEST_SET", "v")
	t.Setenv("SI_A2A_TEST_EMPTY", "")
	if got := getenv("SI_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("SI_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}

// ---- wiring ------------------------------------------------------------------

// A missing DATABASE_URL is a boot error, never a silent in-memory fallback.
func TestBuildDeps_MissingDatabaseFailsBoot(t *testing.T) {
	_, _, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("buildDeps without a database = %v, want an error naming DATABASE_URL", err)
	}
}

// An unreachable database is a boot error after the bounded retries.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

// The boot migrations dial MIGRATIONS_DATABASE_URL, not DATABASE_URL (a pooled
// DSN cannot honour golang-migrate's session advisory lock). The retry line
// the first failed attempt logs names the host it dialled, so it proves which
// DSN the migration step used. A cancelled context would lose that line, so
// the context is given ~300ms instead.
func TestBuildDeps_MigrationsUseTheMigrationsURL(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err := buildDeps(ctx, logger, "postgres://u@127.0.0.1:2/runtime", "postgres://u@127.0.0.1:1/migrations")
	if err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
	out := logs.String()
	if !strings.Contains(out, "retrying") || !strings.Contains(out, "run migrations") || !strings.Contains(out, "127.0.0.1:1") {
		t.Fatalf("migrations did not dial the migrations URL; log:\n%s", out)
	}
	if strings.Contains(out, "127.0.0.1:2") {
		t.Fatalf("migrations dialled the runtime DSN; log:\n%s", out)
	}
}
