package app

import (
	"strings"
	"testing"
	"time"

	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
)

func TestConfigFromEnvAppliesDefaults(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092, b:9092"})
	cfg := ConfigFromEnv(env)
	if err := env.Err(); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8083" || cfg.DatabaseURL != "postgres://x" || len(cfg.KafkaBrokers) != 2 || cfg.KafkaBrokers[1] != "b:9092" {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.SessionTTL != 720*time.Hour || cfg.OptimizerURL != "http://127.0.0.1:8090" || cfg.OptimizerToken != "" || cfg.SimulatePayments {
		t.Errorf("defaults = %+v: payments must not be simulated unless asked", cfg)
	}
}

func TestConfigFromEnvDefaultsTheCancelTimeoutAndLeavesAIOff(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092"})
	cfg := ConfigFromEnv(env)
	if err := env.Err(); err != nil {
		t.Fatal(err)
	}
	if cfg.CancelTimeout != 2*time.Minute || cfg.AIURL != "" || cfg.AIToken != "" {
		t.Errorf("cancel timeout %v, AI %q/%q: want 2m and no gateway", cfg.CancelTimeout, cfg.AIURL, cfg.AIToken)
	}
	// A Config built by hand (tests, e2e) gets the same defaults.
	if got := (Config{}).withDefaults().CancelTimeout; got != 2*time.Minute {
		t.Errorf("withDefaults cancel timeout = %v", got)
	}
	if got := (Config{CancelTimeout: time.Second}).withDefaults().CancelTimeout; got != time.Second {
		t.Errorf("an explicit cancel timeout became %v", got)
	}
}

// A thorough or reviewed quote may take the optimizer 150s, so the console must not give up on it sooner.
func TestTheConsoleWaitsForADeepQuoteLongerThanTheOptimizerMayTakeForIt(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092"})
	cfg := ConfigFromEnv(env)
	if cfg.OptimizerCallTimeout != 3*time.Minute || cfg.OptimizerCallTimeout <= 150*time.Second {
		t.Errorf("the console waits %v for a quote", cfg.OptimizerCallTimeout)
	}
	if got := (Config{}).withDefaults().OptimizerCallTimeout; got != 3*time.Minute {
		t.Errorf("withDefaults = %v", got)
	}
	read := platform.NewEnvFrom(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "OPTIMIZER_CALL_TIMEOUT": "5m"})
	if got := ConfigFromEnv(read).OptimizerCallTimeout; got != 5*time.Minute {
		t.Errorf("OPTIMIZER_CALL_TIMEOUT=5m gave %v", got)
	}
}

func TestConfigFromEnvReadsTheCancelTimeoutAndTheAIGateway(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{
		"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "CANCEL_TIMEOUT": "45s", "AI_URL": "http://ai:8091", "AI_TOKEN": "s3cret",
	})
	cfg := ConfigFromEnv(env)
	if err := env.Err(); err != nil {
		t.Fatal(err)
	}
	if cfg.CancelTimeout != 45*time.Second || cfg.AIURL != "http://ai:8091" || cfg.AIToken != "s3cret" {
		t.Errorf("cfg = %+v", cfg)
	}
	bad := platform.NewEnvFrom(map[string]string{"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "CANCEL_TIMEOUT": "soon"})
	ConfigFromEnv(bad)
	if err := bad.Err(); err == nil || !strings.Contains(err.Error(), "CANCEL_TIMEOUT") {
		t.Errorf("a bad timeout was not reported: %v", err)
	}
}

func TestSweepIntervalFollowsTheTimeoutWithinBounds(t *testing.T) {
	for timeout, want := range map[time.Duration]time.Duration{
		2 * time.Minute:        5 * time.Second,
		20 * time.Second:       2 * time.Second,
		600 * time.Millisecond: 100 * time.Millisecond,
		time.Millisecond:       100 * time.Millisecond,
	} {
		if got := sweepInterval(timeout); got != want {
			t.Errorf("sweepInterval(%v) = %v, want %v", timeout, got, want)
		}
	}
}

func TestConfigFromEnvReadsTheServiceSettings(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{
		"DATABASE_URL": "postgres://x", "KAFKA_BROKERS": "a:9092", "HTTP_ADDR": ":9999", "SESSION_TTL": "2h",
		"OPTIMIZER_URL": "http://optimizer:8090", "OPTIMIZER_TOKEN": "s3cret", "SIMULATE_PAYMENTS": "true",
	})
	cfg := ConfigFromEnv(env)
	if err := env.Err(); err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9999" || cfg.SessionTTL != 2*time.Hour || cfg.OptimizerURL != "http://optimizer:8090" || cfg.OptimizerToken != "s3cret" || !cfg.SimulatePayments {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestConfigFromEnvReportsEverythingMissing(t *testing.T) {
	env := platform.NewEnvFrom(map[string]string{"SIMULATE_PAYMENTS": "maybe"})
	ConfigFromEnv(env)
	err := env.Err()
	if err == nil {
		t.Fatal("an empty environment was accepted")
	}
	for _, want := range []string{"DATABASE_URL", "KAFKA_BROKERS", "SIMULATE_PAYMENTS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %s: %v", want, err)
		}
	}
}

func TestStartRefusesAnIncompleteConfig(t *testing.T) {
	_, err := Start(t.Context(), Config{}, nil)
	if err == nil {
		t.Fatal("started without a database, brokers or optimizer")
	}
	for _, want := range []string{"DatabaseURL", "KafkaBrokers", "OptimizerURL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %s: %v", want, err)
		}
	}
}
