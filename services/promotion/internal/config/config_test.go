package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad_DefaultsAndOverrides(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	t.Setenv("PROMOTION_VOUCHER_EXPIRY_NOTICE_HOURS", "")
	t.Setenv("PROMOTION_VOUCHER_EXPIRY_SCAN_INTERVAL", "")
	t.Setenv("PROMOTION_REDIS_RECONCILE_INTERVAL", "")
	t.Setenv("LOG_LEVEL", "")

	def := Load()
	if def.ServiceName != "promotion" || def.GRPCPort != "50054" || def.HTTPPort != "8084" {
		t.Fatalf("unexpected defaults: %+v", def)
	}
	if def.VoucherExpiryNoticeHours != 24 || def.VoucherExpiryScanInterval != time.Minute || def.RedisReconcileInterval != 5*time.Second {
		t.Fatalf("unexpected timer defaults: hours=%d expiry=%v reconcile=%v", def.VoucherExpiryNoticeHours, def.VoucherExpiryScanInterval, def.RedisReconcileInterval)
	}
	if def.LogLevel != slog.LevelInfo {
		t.Fatalf("expected LevelInfo default, got %v", def.LogLevel)
	}

	t.Setenv("PROMOTION_VOUCHER_EXPIRY_NOTICE_HOURS", "12")
	t.Setenv("PROMOTION_VOUCHER_EXPIRY_SCAN_INTERVAL", "30s")
	t.Setenv("PROMOTION_REDIS_RECONCILE_INTERVAL", "15s")
	t.Setenv("PRODUCT_GRPC_ADDR", "product:50052")
	t.Setenv("LOG_LEVEL", "debug")
	ovr := Load()
	if ovr.VoucherExpiryNoticeHours != 12 || ovr.VoucherExpiryScanInterval != 30*time.Second || ovr.RedisReconcileInterval != 15*time.Second || ovr.ProductGRPCAddr != "product:50052" || ovr.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected overrides: %+v", ovr)
	}

	// Non-positive notice hours / intervals fall back to safe defaults.
	t.Setenv("PROMOTION_VOUCHER_EXPIRY_NOTICE_HOURS", "0")
	t.Setenv("PROMOTION_VOUCHER_EXPIRY_SCAN_INTERVAL", "-5s")
	t.Setenv("PROMOTION_REDIS_RECONCILE_INTERVAL", "0s")
	fb := Load()
	if fb.VoucherExpiryNoticeHours != 24 || fb.VoucherExpiryScanInterval != time.Minute || fb.RedisReconcileInterval != 5*time.Second {
		t.Fatalf("expected fallback to 24h / 1m / 5s for non-positive env, got %dh / %v / %v", fb.VoucherExpiryNoticeHours, fb.VoucherExpiryScanInterval, fb.RedisReconcileInterval)
	}

	for raw, want := range map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"WARN":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"info":    slog.LevelInfo,
		"other":   slog.LevelInfo,
	} {
		if got := parseLogLevel(raw); got != want {
			t.Fatalf("parseLogLevel(%q) = %v, want %v", raw, got, want)
		}
	}
}
