package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/nexusmart/core/services/promotion/internal/config"
)

func TestLoad_Defaults(t *testing.T) {
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
		"JWT_SECRET",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_INSECURE",
		"LOG_LEVEL",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}

	cfg := config.Load()
	if cfg.ServiceName != "promotion" {
		t.Fatalf("expected default ServiceName=promotion, got %q", cfg.ServiceName)
	}
	if cfg.GRPCPort != "50054" || cfg.HTTPPort != "8084" {
		t.Fatalf("unexpected default ports: grpc=%q http=%q", cfg.GRPCPort, cfg.HTTPPort)
	}
	if cfg.RedisAddr != "localhost:6379" || cfg.NATSURL != "nats://localhost:4222" || cfg.NATSStream != "NEXUSMART" {
		t.Fatalf("unexpected default infra endpoints: %+v", cfg)
	}
	if cfg.ProductGRPCAddr != "localhost:50052" {
		t.Fatalf("unexpected default ProductGRPCAddr: %q", cfg.ProductGRPCAddr)
	}
	if cfg.GRPCCallTimeout != 5*time.Second || cfg.FlashSaleSweepInterval != 2*time.Second {
		t.Fatalf("unexpected default durations: grpc=%v sweep=%v", cfg.GRPCCallTimeout, cfg.FlashSaleSweepInterval)
	}
	if !cfg.OTLPInsecure || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected default telemetry/log settings: insecure=%v level=%v", cfg.OTLPInsecure, cfg.LogLevel)
	}
}

func TestLoad_EnvOverridesAndLogLevels(t *testing.T) {
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
	t.Setenv("JWT_SECRET", "custom-jwt-secret")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "otel:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := config.Load()
	if cfg.ServiceName != "promo-custom" || cfg.GRPCPort != "60054" || cfg.HTTPPort != "9084" {
		t.Fatalf("unexpected service/port overrides: %+v", cfg)
	}
	if cfg.DatabaseURL != "postgres://user:pass@db:5432/promo" || cfg.RedisAddr != "redis:6379" || cfg.RedisPass != "secret-pass" {
		t.Fatalf("unexpected db/redis overrides: %+v", cfg)
	}
	if cfg.NATSURL != "nats://nats:4222" || cfg.NATSStream != "CUSTOM_STREAM" || cfg.ProductGRPCAddr != "product:50052" {
		t.Fatalf("unexpected nats/product overrides: %+v", cfg)
	}
	if cfg.GRPCCallTimeout != 12*time.Second || cfg.FlashSaleSweepInterval != 15*time.Second {
		t.Fatalf("unexpected duration overrides: %+v", cfg)
	}
	if cfg.JWTSecret != "custom-jwt-secret" || cfg.OTLPEndpoint != "otel:4317" || cfg.OTLPInsecure {
		t.Fatalf("unexpected jwt/otel overrides: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("expected debug log level, got %v", cfg.LogLevel)
	}

	levelCases := []struct {
		raw  string
		want slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"warn", slog.LevelWarn},
		{" WARNING ", slog.LevelWarn},
		{"error", slog.LevelError},
		{"info", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
	}
	for _, tc := range levelCases {
		t.Setenv("LOG_LEVEL", tc.raw)
		got := config.Load().LogLevel
		if got != tc.want {
			t.Fatalf("LOG_LEVEL=%q: got %v, want %v", tc.raw, got, tc.want)
		}
	}
}
