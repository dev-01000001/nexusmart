package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad_DefaultsAndOverrides(t *testing.T) {
	keys := []string{
		"SERVICE_NAME",
		"GRPC_PORT",
		"HTTP_PORT",
		"DATABASE_URL",
		"REDIS_ADDR",
		"REDIS_PASSWORD",
		"NATS_URL",
		"NATS_STREAM",
		"PRODUCT_GRPC_ADDR",
		"PROMOTION_GRPC_TIMEOUT",
		"FLASH_SALE_SWEEP_INTERVAL",
		"PROMOTION_VOUCHER_EXPIRY_NOTICE_HOURS",
		"PROMOTION_VOUCHER_EXPIRY_SCAN_INTERVAL",
		"PROMOTION_REDIS_RECONCILE_INTERVAL",
		"JWT_SECRET",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_INSECURE",
		"LOG_LEVEL",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}

	def := Load()
	if def.ServiceName != "promotion" || def.GRPCPort != "50054" || def.HTTPPort != "8084" {
		t.Fatalf("unexpected defaults: %+v", def)
	}
	if def.RedisAddr != "localhost:6379" || def.NATSURL != "nats://localhost:4222" || def.NATSStream != "NEXUSMART" || def.ProductGRPCAddr != "localhost:50052" {
		t.Fatalf("unexpected default infra endpoints: %+v", def)
	}
	if def.GRPCCallTimeout != 5*time.Second || def.FlashSaleSweepInterval != 2*time.Second {
		t.Fatalf("unexpected default grpc/sweep durations: grpc=%v sweep=%v", def.GRPCCallTimeout, def.FlashSaleSweepInterval)
	}
	if def.VoucherExpiryNoticeHours != 24 || def.VoucherExpiryScanInterval != time.Minute || def.RedisReconcileInterval != 5*time.Second {
		t.Fatalf("unexpected timer defaults: hours=%d expiry=%v reconcile=%v", def.VoucherExpiryNoticeHours, def.VoucherExpiryScanInterval, def.RedisReconcileInterval)
	}
	if !def.OTLPInsecure || def.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected default telemetry/log settings: insecure=%v level=%v", def.OTLPInsecure, def.LogLevel)
	}

	t.Setenv("SERVICE_NAME", "promo-custom")
	t.Setenv("GRPC_PORT", "60054")
	t.Setenv("HTTP_PORT", "9084")
	t.Setenv("DATABASE_URL", "postgres://user:pass@db:5432/promo")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "secret-pass")
	t.Setenv("NATS_URL", "nats://nats:4222")
	t.Setenv("NATS_STREAM", "CUSTOM_STREAM")
	t.Setenv("PRODUCT_GRPC_ADDR", "product:50052")
	t.Setenv("PROMOTION_GRPC_TIMEOUT", "12s")
	t.Setenv("FLASH_SALE_SWEEP_INTERVAL", "15s")
	t.Setenv("PROMOTION_VOUCHER_EXPIRY_NOTICE_HOURS", "12")
	t.Setenv("PROMOTION_VOUCHER_EXPIRY_SCAN_INTERVAL", "30s")
	t.Setenv("PROMOTION_REDIS_RECONCILE_INTERVAL", "15s")
	t.Setenv("JWT_SECRET", "custom-jwt-secret")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	t.Setenv("LOG_LEVEL", "debug")

	ovr := Load()
	if ovr.ServiceName != "promo-custom" || ovr.GRPCPort != "60054" || ovr.HTTPPort != "9084" {
		t.Fatalf("unexpected service/port overrides: %+v", ovr)
	}
	if ovr.DatabaseURL != "postgres://user:pass@db:5432/promo" || ovr.RedisAddr != "redis:6379" || ovr.RedisPass != "secret-pass" {
		t.Fatalf("unexpected db/redis overrides: %+v", ovr)
	}
	if ovr.NATSURL != "nats://nats:4222" || ovr.NATSStream != "CUSTOM_STREAM" || ovr.ProductGRPCAddr != "product:50052" {
		t.Fatalf("unexpected nats/product overrides: %+v", ovr)
	}
	if ovr.GRPCCallTimeout != 12*time.Second || ovr.FlashSaleSweepInterval != 15*time.Second {
		t.Fatalf("unexpected duration overrides: %+v", ovr)
	}
	if ovr.VoucherExpiryNoticeHours != 12 || ovr.VoucherExpiryScanInterval != 30*time.Second || ovr.RedisReconcileInterval != 15*time.Second {
		t.Fatalf("unexpected timer overrides: %+v", ovr)
	}
	if ovr.JWTSecret != "custom-jwt-secret" || ovr.OTLPEndpoint != "otel:4317" || ovr.OTLPInsecure || ovr.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected jwt/otel/log overrides: %+v", ovr)
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
		"debug":     slog.LevelDebug,
		"DEBUG":     slog.LevelDebug,
		"WARN":      slog.LevelWarn,
		" WARNING ": slog.LevelWarn,
		"error":     slog.LevelError,
		"info":      slog.LevelInfo,
		"other":     slog.LevelInfo,
	} {
		if got := parseLogLevel(raw); got != want {
			t.Fatalf("parseLogLevel(%q) = %v, want %v", raw, got, want)
		}
	}
}
