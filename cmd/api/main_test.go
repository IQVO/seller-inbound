package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/seller-inbound/internal/adapters/outbound/sscc"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// validEnv is the smallest environment loadRuntime accepts.
func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL": "postgres://u@h/db", "KAFKA_BROKERS": "b1:9092, b2:9092",
		"NODE_CONSUMER_GROUP": "g-node", "CATALOG_CONSUMER_GROUP": "g-catalog", "RECEIPT_CONSUMER_GROUP": "g-receipt",
		"SSCC_COMPANY_PREFIX": "0614141",
	}
}

func lookup(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadRuntimeAcceptsTheMinimalEnvironment(t *testing.T) {
	rt, err := loadRuntime(lookup(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.brokers) != 2 || rt.brokers[1] != "b2:9092" || rt.groups["NODE_CONSUMER_GROUP"] != "g-node" ||
		rt.groups["RECEIPT_CONSUMER_GROUP"] != "g-receipt" || rt.ssccPrefix != "0614141" || rt.ssccExtension != 0 {
		t.Fatalf("rt = %+v", rt)
	}
}

func TestLoadRuntimeReadsTheExtensionDigit(t *testing.T) {
	env := validEnv()
	env["SSCC_EXTENSION_DIGIT"] = "3"
	if rt, err := loadRuntime(lookup(env)); err != nil || rt.ssccExtension != 3 {
		t.Fatalf("rt = %+v, err = %v", rt, err)
	}
}

func TestLoadRuntimeRefusesWithTheVariableName(t *testing.T) {
	tests := map[string]struct {
		set  map[string]string
		want string
	}{
		"no database":       {map[string]string{"DATABASE_URL": ""}, "DATABASE_URL"},
		"no brokers":        {map[string]string{"KAFKA_BROKERS": " , "}, "KAFKA_BROKERS"},
		"publisher log":     {map[string]string{"EVENT_PUBLISHER": "log"}, "EVENT_PUBLISHER=log"},
		"publisher other":   {map[string]string{"EVENT_PUBLISHER": "nats"}, "EVENT_PUBLISHER"},
		"node group":        {map[string]string{"NODE_CONSUMER_GROUP": ""}, "NODE_CONSUMER_GROUP"},
		"catalog group":     {map[string]string{"CATALOG_CONSUMER_GROUP": ""}, "CATALOG_CONSUMER_GROUP"},
		"receipt group":     {map[string]string{"RECEIPT_CONSUMER_GROUP": ""}, "RECEIPT_CONSUMER_GROUP"},
		"no prefix":         {map[string]string{"SSCC_COMPANY_PREFIX": ""}, "SSCC_COMPANY_PREFIX"},
		"short prefix":      {map[string]string{"SSCC_COMPANY_PREFIX": "12345"}, "SSCC_COMPANY_PREFIX"},
		"long prefix":       {map[string]string{"SSCC_COMPANY_PREFIX": "123456789012"}, "SSCC_COMPANY_PREFIX"},
		"non-digit prefix":  {map[string]string{"SSCC_COMPANY_PREFIX": "06141x1"}, "SSCC_COMPANY_PREFIX"},
		"extension garbage": {map[string]string{"SSCC_EXTENSION_DIGIT": "x"}, "SSCC_EXTENSION_DIGIT"},
		"extension range":   {map[string]string{"SSCC_EXTENSION_DIGIT": "10"}, "SSCC_EXTENSION_DIGIT"},
	}
	for name, tc := range tests {
		env := validEnv()
		for k, v := range tc.set {
			env[k] = v
		}
		if _, err := loadRuntime(lookup(env)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to name %s", name, err, tc.want)
		}
	}
	if _, err := loadRuntime(lookup(map[string]string{"SSCC_COMPANY_PREFIX": "0614141"})); err == nil {
		t.Fatal("an empty environment must be refused")
	}
	_ = sscc.ErrInvalidConfig
}

func TestSplitBrokers(t *testing.T) {
	if got := splitBrokers(" a:1,,b:2 ,"); len(got) != 2 || got[0] != "a:1" || got[1] != "b:2" {
		t.Fatalf("got %v", got)
	}
	if got := splitBrokers(""); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestNewReadinessNamesTheFailingDependency(t *testing.T) {
	g := newReadiness(func(context.Context) error { return errors.New("down") }, []string{"127.0.0.1:1"})
	failing := g.Failing(context.Background())
	if len(failing) != 2 || failing[0] != "postgres" || failing[1] != "kafka" {
		t.Fatalf("failing = %v", failing)
	}
}

func TestRelayAndShutdownSettings(t *testing.T) {
	log := quiet()
	for in, want := range map[string]time.Duration{"": time.Second, "250ms": 250 * time.Millisecond, "2": 2 * time.Second, "x": time.Second, "-1s": time.Second, "0": time.Second} {
		if got := parseRelayInterval(in, log); got != want {
			t.Errorf("parseRelayInterval(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]int{"": 20, "5": 5, "0": 0, "-1": 20, "x": 20} {
		if got := outboxMaxAttempts(in, log); got != want {
			t.Errorf("outboxMaxAttempts(%q) = %d, want %d", in, got, want)
		}
	}
	for in, want := range map[string]time.Duration{"": DefaultShutdownDrainDelay, "0": 0, "1s": time.Second, "-1s": DefaultShutdownDrainDelay, "x": DefaultShutdownDrainDelay} {
		t.Setenv("SHUTDOWN_DRAIN_DELAY", in)
		if got := shutdownDrainDelay(log); got != want {
			t.Errorf("shutdownDrainDelay(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPublisherModeAndLogger(t *testing.T) {
	if parsePublisherMode("") != nil || parsePublisherMode("kafka") != nil {
		t.Fatal("kafka is the default")
	}
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "loud": slog.LevelInfo} {
		l := newLogger(in)
		if !l.Enabled(context.Background(), want) || (want > slog.LevelDebug && l.Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q) has the wrong level", in)
		}
	}
	t.Setenv("X_SET", "v")
	if getenv("X_SET", "d") != "v" || getenv("X_UNSET_FOR_SURE", "d") != "d" {
		t.Fatal("getenv")
	}
}

func TestRunLoopCallsAtOnceThenEveryIntervalUntilCancelled(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(ctx, quiet(), "test", 5*time.Millisecond, func(context.Context) (int, error) {
			if calls.Add(1) == 1 {
				return 0, errors.New("first pass fails")
			}
			return 2, nil
		})
	}()
	deadline := time.After(5 * time.Second)
	for calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatal("the loop did not keep ticking after a failed pass")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop")
	}
}

func TestWatchWorkersAndAwait(t *testing.T) {
	a, b := &worker{name: "a", done: make(chan struct{})}, &worker{name: "b", done: make(chan struct{})}
	died := watchWorkers(a, nil, b)
	close(b.done)
	if name := <-died; name != "b" {
		t.Fatalf("died = %s", name)
	}
	await(quiet(), nil)
	await(quiet(), b)
	close(a.done)
	await(quiet(), a)
}

func TestStartOutboxRelayNeedsBrokers(t *testing.T) {
	if _, _, err := startOutboxRelay(nil, nil, quiet()); !errors.Is(err, errMissingBrokers) {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := buildAdapters(context.Background(), "", runtimeEnv{}, quiet()); !errors.Is(err, errMissingDatabaseURL) {
		t.Fatalf("err = %v", err)
	}
}
