package main

import (
	"errors"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// The mandatory configuration refuses to boot, naming the variable: the chart
// tests grep these "<VAR> is required" strings to prove the Deployment renders
// every mandatory variable, so their phrasing is a contract.
func TestValidateBootConfigRefusals(t *testing.T) {
	const db = "postgres://u:p@db:5432/si"
	for name, tc := range map[string]struct {
		env     map[string]string
		wantErr string
	}{
		"no database":              {map[string]string{"A2A_AGENT_URL": "http://a:8093/"}, "DATABASE_URL is required"},
		"no agent url":             {map[string]string{"DATABASE_URL": db}, "A2A_AGENT_URL is required"},
		"blank agent url":          {map[string]string{"DATABASE_URL": db, "A2A_AGENT_URL": "   "}, "A2A_AGENT_URL is required"},
		"relative agent url":       {map[string]string{"DATABASE_URL": db, "A2A_AGENT_URL": "/a2a"}, "A2A_AGENT_URL is required"},
		"scheme-less agent url":    {map[string]string{"DATABASE_URL": db, "A2A_AGENT_URL": "seller-inbound-a2a:8093"}, "A2A_AGENT_URL is required"},
		"non-http agent url":       {map[string]string{"DATABASE_URL": db, "A2A_AGENT_URL": "ftp://a:8093/"}, "A2A_AGENT_URL is required"},
		"hostless agent url":       {map[string]string{"DATABASE_URL": db, "A2A_AGENT_URL": "http:///a2a"}, "A2A_AGENT_URL is required"},
		"unparsable agent url":     {map[string]string{"DATABASE_URL": db, "A2A_AGENT_URL": "http://[::1"}, "A2A_AGENT_URL is required"},
		"kafka alone is no config": {map[string]string{"KAFKA_BROKERS": "k:9092"}, "DATABASE_URL is required"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := validateBootConfig(env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateBootConfig = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateBootConfigAcceptsAbsoluteHTTPURLs(t *testing.T) {
	for _, u := range []string{
		"http://seller-inbound-a2a.warehouse-systems.svc.cluster.local:8093/",
		"https://agents.example.test/a2a",
		" http://a:8093/ ",
	} {
		got, err := validateBootConfig(env(map[string]string{"DATABASE_URL": "postgres://u@db/si", "A2A_AGENT_URL": u}))
		if err != nil || got != strings.TrimSpace(u) {
			t.Errorf("validateBootConfig(%q) = %q, %v", u, got, err)
		}
	}
}

// No Kafka variable is a boot requirement: this binary never publishes, so a
// pod without KAFKA_BROKERS or EVENT_PUBLISHER must still validate.
func TestValidateBootConfigNeedsNoKafka(t *testing.T) {
	if _, err := validateBootConfig(env(map[string]string{"DATABASE_URL": "postgres://u@db/si", "A2A_AGENT_URL": "http://a:8093/"})); err != nil {
		t.Fatalf("a read-only agent must not require Kafka: %v", err)
	}
}

// run() refuses a bad A2A_AGENT_URL BEFORE it dials anything or binds a
// listener: with an unreachable DATABASE_URL it would otherwise spend the
// whole retry budget (and then fail on the database, not the URL).
func TestRunFailsFastOnAMissingAgentURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u@127.0.0.1:1/db")
	t.Setenv("A2A_AGENT_URL", "")
	t.Setenv("A2A_ADDR", "127.0.0.1:0")
	err := run()
	if err == nil || !errors.Is(err, errBadAgentURL) {
		t.Fatalf("run() = %v, want errBadAgentURL", err)
	}
}
