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
	if cfg.CODMaxRejectedCount != 3 || !cfg.CODRequirePhoneVerified || cfg.CODMinOrdersForRate != 3 || cfg.CODMaxRejectRateBPS != 3000 {
		t.Fatalf("COD defaults unexpected: %+v", cfg)
	}
	if cfg.FlashPaymentExpiryMinutes != 5 || cfg.RewardCoinsBPS != 100 || cfg.AutoCompleteDays != 7 || cfg.SellerConfirmHours != 48 {
		t.Fatalf("numeric defaults unexpected: %+v", cfg)
	}
	if cfg.ReturnSellerResponseDays != 3 || cfg.ReturnShipDays != 5 || cfg.ReturnInspectDays != 3 || cfg.BatchShipMaxItems != 50 {
		t.Fatalf("return/batch defaults unexpected: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %v, want info", cfg.LogLevel)
	}

	// Verify env overrides.
	t.Setenv("SERVICE_NAME", "order-custom")
	t.Setenv("GRPC_PORT", "60055")
	t.Setenv("HTTP_PORT", "9085")
	t.Setenv("ORDER_COD_MAX_REJECTED_COUNT", "5")
	t.Setenv("ORDER_COD_REQUIRE_PHONE_VERIFIED", "false")
	t.Setenv("ORDER_COD_MIN_ORDERS_FOR_RATE", "6")
	t.Setenv("ORDER_COD_MAX_REJECT_RATE_BPS", "2500")
	t.Setenv("ORDER_FLASH_PAYMENT_EXPIRY_MINUTES", "8")
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
	if custom.CODMaxRejectedCount != 5 || custom.CODRequirePhoneVerified || custom.CODMinOrdersForRate != 6 || custom.CODMaxRejectRateBPS != 2500 {
		t.Fatalf("unexpected custom COD config: %+v", custom)
	}
	if custom.FlashPaymentExpiryMinutes != 8 || custom.RewardCoinsBPS != 200 {
		t.Fatalf("unexpected custom flash/reward config: %+v", custom)
	}
	if custom.GRPCCallTimeout != 7*time.Second || custom.OutboxPollInterval != 500*time.Millisecond {
		t.Fatalf("unexpected custom durations: %+v", custom)
	}
	if custom.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %v, want debug", custom.LogLevel)
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
