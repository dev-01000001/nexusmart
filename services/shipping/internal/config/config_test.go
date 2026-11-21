package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadDefaultsAndOverrides(t *testing.T) {
	// Defaults
	cfg := Load()
	if cfg.ServiceName != "shipping" || cfg.GRPCPort != "50057" || cfg.HTTPPort != "8087" {
		t.Fatalf("unexpected default ports/name: %+v", cfg)
	}
	if !cfg.GHNEnabled || cfg.GHNMode != "mock" {
		t.Fatalf("unexpected GHN defaults: enabled=%v mode=%s", cfg.GHNEnabled, cfg.GHNMode)
	}
	if !cfg.GHTKEnabled || cfg.GHTKMode != "mock" || cfg.GHTKBaseURL != "https://services-staging.ghtklab.com" || cfg.GHTKToken != "sandbox-ghtk-token" {
		t.Fatalf("unexpected GHTK defaults: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("expected default Info log level, got %v", cfg.LogLevel)
	}

	// Overrides
	t.Setenv("GHTK_ENABLED", "false")
	t.Setenv("GHTK_MODE", "SANDBOX")
	t.Setenv("GHTK_BASE_URL", "https://custom.ghtk.test")
	t.Setenv("GHTK_TOKEN", "custom-token")
	t.Setenv("CARRIER_CB_MAX_FAILURES", "0")
	t.Setenv("NATS_MAX_DELIVER", "-3")
	t.Setenv("SHIPMENT_POLL_INTERVAL", "15s")
	t.Setenv("LOG_LEVEL", "debug")

	over := Load()
	if over.GHTKEnabled || over.GHTKMode != "sandbox" || over.GHTKBaseURL != "https://custom.ghtk.test" || over.GHTKToken != "custom-token" {
		t.Fatalf("unexpected GHTK overrides: %+v", over)
	}
	if over.CBMaxFailures != 1 || over.ConsumerMaxTries != 1 {
		t.Fatalf("expected min clamp to 1, got cb=%d tries=%d", over.CBMaxFailures, over.ConsumerMaxTries)
	}
	if over.PollInterval != 15*time.Second || over.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected interval/log level: %v / %v", over.PollInterval, over.LogLevel)
	}

	if parseLogLevel("warn") != slog.LevelWarn || parseLogLevel("warning") != slog.LevelWarn || parseLogLevel("error") != slog.LevelError {
		t.Fatalf("unexpected parseLogLevel values")
	}
}
