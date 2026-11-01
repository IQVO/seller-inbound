package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	outboxrelay "github.com/claudioed/seller-inbound/internal/adapters/outbound/outbox"
	"github.com/claudioed/seller-inbound/internal/adapters/outbound/sscc"
)

// Boot errors for the variables the context cannot run without. Each message
// names the variable.
var (
	errMissingDatabaseURL = errors.New("DATABASE_URL is required: this context runs on real Postgres, there is no in-memory mode")
	errMissingBrokers     = errors.New("KAFKA_BROKERS is required: this context runs on real Kafka (outbox relay, consumers, /readyz)")
	errMissingSsccPrefix  = errors.New("SSCC_COMPANY_PREFIX is required: the network issues SSCCs from its own GS1 company prefix")
)

// consumerGroups are the mandatory consumer group variables.
var consumerGroups = []string{"NODE_CONSUMER_GROUP", "CATALOG_CONSUMER_GROUP", "RECEIPT_CONSUMER_GROUP"}

// runtimeEnv is the validated environment: the brokers, every consumer group
// and the SSCC issuing configuration.
type runtimeEnv struct {
	brokers       []string
	groups        map[string]string
	ssccPrefix    string
	ssccExtension int
}

// loadRuntime validates the environment before anything is opened or bound.
// Every error names the variable.
func loadRuntime(env func(string) string) (runtimeEnv, error) {
	if env("DATABASE_URL") == "" {
		return runtimeEnv{}, errMissingDatabaseURL
	}
	rt := runtimeEnv{brokers: splitBrokers(env("KAFKA_BROKERS")), groups: map[string]string{}}
	if len(rt.brokers) == 0 {
		return runtimeEnv{}, errMissingBrokers
	}
	if err := parsePublisherMode(env("EVENT_PUBLISHER")); err != nil {
		return runtimeEnv{}, err
	}
	for _, name := range consumerGroups {
		if env(name) == "" {
			return runtimeEnv{}, fmt.Errorf("%s is required: the consumer always runs on real Kafka", name)
		}
		rt.groups[name] = env(name)
	}
	rt.ssccPrefix = strings.TrimSpace(env("SSCC_COMPANY_PREFIX"))
	if rt.ssccPrefix == "" {
		return runtimeEnv{}, errMissingSsccPrefix
	}
	rt.ssccExtension = sscc.DefaultExtensionDigit
	if raw := strings.TrimSpace(env("SSCC_EXTENSION_DIGIT")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return runtimeEnv{}, fmt.Errorf("%w: SSCC_EXTENSION_DIGIT %q is not a digit", sscc.ErrInvalidConfig, raw)
		}
		rt.ssccExtension = n
	}
	// Validate prefix and extension now, before anything is opened.
	if _, err := sscc.NewIssuer(rt.ssccPrefix, rt.ssccExtension, nil); err != nil {
		return runtimeEnv{}, err
	}
	return rt, nil
}

// splitBrokers splits KAFKA_BROKERS on commas, trimming blanks.
func splitBrokers(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// parsePublisherMode validates EVENT_PUBLISHER: unset or "kafka". The log sink
// does not exist; the variable stays because deployment manifests set it.
func parsePublisherMode(raw string) error {
	switch raw {
	case "", "kafka":
		return nil
	case "log":
		return errors.New("EVENT_PUBLISHER=log is not supported: this context publishes to real Kafka")
	default:
		return fmt.Errorf("EVENT_PUBLISHER: unknown value %q (want kafka)", raw)
	}
}

// parseRelayInterval reads OUTBOX_RELAY_INTERVAL (Go duration or plain
// seconds), defaulting when unset, malformed or non-positive.
func parseRelayInterval(raw string, logger *slog.Logger) time.Duration {
	if raw == "" {
		return outboxrelay.DefaultInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		if secs, serr := strconv.ParseFloat(raw, 64); serr == nil {
			d, err = time.Duration(secs*float64(time.Second)), nil
		}
	}
	if err != nil || d <= 0 {
		logger.Warn("invalid OUTBOX_RELAY_INTERVAL; using the default", "value", raw, "default", outboxrelay.DefaultInterval)
		return outboxrelay.DefaultInterval
	}
	return d
}

// outboxMaxAttempts reads OUTBOX_MAX_ATTEMPTS: how many times an outbox row may
// fail before the relay excludes it from claims. Unset = the fleet default
// (20); 0 = unlimited; negative or unparsable values log a warning and use the
// default, so a typo never silently removes the cap.
func outboxMaxAttempts(raw string, logger *slog.Logger) int {
	if raw == "" {
		return outboxrelay.DefaultMaxAttempts
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		logger.Warn("invalid OUTBOX_MAX_ATTEMPTS; using the default", "value", raw, "default", outboxrelay.DefaultMaxAttempts)
		return outboxrelay.DefaultMaxAttempts
	}
	return n
}

// shutdownDrainDelay reads SHUTDOWN_DRAIN_DELAY ("0" disables; unset, negative
// or unparsable = the default).
func shutdownDrainDelay(logger *slog.Logger) time.Duration {
	raw := os.Getenv("SHUTDOWN_DRAIN_DELAY")
	if raw == "" {
		return DefaultShutdownDrainDelay
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		logger.Warn("ignoring invalid SHUTDOWN_DRAIN_DELAY", "value", raw, "default", DefaultShutdownDrainDelay.String())
		return DefaultShutdownDrainDelay
	}
	return d
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
