package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad_DefaultsAndEnvOverrides(t *testing.T) {
	// Verify defaults when env vars are unset.
	cfg := Load()
	if cfg.ServiceName != "order" {
		t.Fatalf("ServiceName = %q, want order", cfg.ServiceName)
	}
	if cfg.GRPCPort != "50055" || cfg.HTTPPort != "8085" {
		t.Fatalf("ports = (%q, %q), want (50055, 8085)", cfg.GRPCPort, cfg.HTTPPort)
	}
	if cfg.CODMaxVND != 5000000 || cfg.CODMaxRejectedCount != 3 {
		t.Fatalf("COD defaults = (%d, %d)", cfg.CODMaxVND, cfg.CODMaxRejectedCount)
	}
	if cfg.PaymentExpiryMinutes != 15 || cfg.DefaultCommissionBPS != 500 || cfg.RewardCoinsBPS != 100 {
		t.Fatalf("numeric defaults unexpected: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %v, want info", cfg.LogLevel)
	}

	// Verify env overrides.
	t.Setenv("SERVICE_NAME", "order-custom")
	t.Setenv("GRPC_PORT", "60055")
	t.Setenv("HTTP_PORT", "9085")
	t.Setenv("ORDER_COD_MAX_VND", "8000000")
	t.Setenv("ORDER_COD_MAX_REJECTED_COUNT", "5")
	t.Setenv("ORDER_PAYMENT_EXPIRY_MINUTES", "20")
	t.Setenv("ORDER_DEFAULT_COMMISSION_BPS", "750")
	t.Setenv("ORDER_REWARD_COINS_BPS", "200")
	t.Setenv("ORDER_GRPC_TIMEOUT", "7s")
	t.Setenv("ORDER_OUTBOX_POLL_INTERVAL", "500ms")
	t.Setenv("ORDER_EXPIRY_WATCHDOG_INTERVAL", "30s")
	t.Setenv("ORDER_EXPIRY_WATCHDOG_TIMEOUT", "25s")
	t.Setenv("LOG_LEVEL", "debug")

	custom := Load()
	if custom.ServiceName != "order-custom" || custom.GRPCPort != "60055" || custom.HTTPPort != "9085" {
		t.Fatalf("unexpected custom basic fields: %+v", custom)
	}
	if custom.CODMaxVND != 8000000 || custom.CODMaxRejectedCount != 5 {
		t.Fatalf("unexpected custom COD config: %+v", custom)
	}
	if custom.GRPCCallTimeout != 7*time.Second || custom.OutboxPollInterval != 500*time.Millisecond {
		t.Fatalf("unexpected custom durations: %+v", custom)
	}
	if custom.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %v, want debug", custom.LogLevel)
	}

	// Verify invalid int64 falls back to default.
	t.Setenv("ORDER_COD_MAX_VND", "not-a-number")
	if got := envInt64("ORDER_COD_MAX_VND", 12345); got != 12345 {
		t.Fatalf("envInt64 fallback = %d, want 12345", got)
	}

	// Verify all parseLogLevel branches.
	for in, want := range map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"WARN":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"info":    slog.LevelInfo,
		"other":   slog.LevelInfo,
	} {
		if got := parseLogLevel(in); got != want {
			t.Fatalf("parseLogLevel(%q) = %v, want %v", in, got, want)
		}
	}
}
