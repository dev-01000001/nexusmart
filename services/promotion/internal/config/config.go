package config

import (
	"log/slog"
	"strings"

	"github.com/nexusmart/core/pkg/common/env"
)

// Config holds runtime configuration for the promotion service.
type Config struct {
	ServiceName  string
	GRPCPort     string
	HTTPPort     string
	DatabaseURL  string
	RedisAddr    string
	RedisPass    string
	NATSURL      string
	NATSStream   string
	JWTSecret    string
	OTLPEndpoint string
	OTLPInsecure bool
	LogLevel     slog.Level
}

// Load reads service configuration from environment variables with local dev defaults.
func Load() Config {
	return Config{
		ServiceName:  env.EnvString("SERVICE_NAME", "promotion"),
		GRPCPort:     env.EnvString("GRPC_PORT", "50054"),
		HTTPPort:     env.EnvString("HTTP_PORT", "8084"),
		DatabaseURL:  env.EnvString("DATABASE_URL", "postgres://nexusmart:nexusmart_dev_password@localhost:5432/promotion_db?sslmode=disable"),
		RedisAddr:    env.EnvString("REDIS_ADDR", "localhost:6379"),
		RedisPass:    env.EnvString("REDIS_PASSWORD", ""),
		NATSURL:      env.EnvString("NATS_URL", "nats://localhost:4222"),
		NATSStream:   env.EnvString("NATS_STREAM", "NEXUSMART"),
		JWTSecret:    env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		OTLPEndpoint: env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure: env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:     parseLogLevel(env.EnvString("LOG_LEVEL", "info")),
	}
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
