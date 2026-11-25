package config

import (
	"log/slog"
	"strings"

	"github.com/nexusmart/core/pkg/common/env"
)

// Config holds runtime configuration for the notification service.
type Config struct {
	ServiceName      string
	GRPCPort         string
	HTTPPort         string
	DatabaseURL      string
	NATSURL          string
	NATSStream       string
	NATSDLQStream    string
	ConsumerMaxTries uint64
	SMTPHost         string
	SMTPPort         int
	SMTPFrom         string
	UserGRPCAddr     string
	WebBaseURL       string
	SiteURLScheme    string // scheme of absolute order links to shop sites (https; dev http)
	SiteURLPort      string // optional port appended to site hosts (dev only, e.g. 3300)
	JWTSecret        string
	OTLPEndpoint     string
	OTLPInsecure     bool
	LogLevel         slog.Level
}

// Load parses environment variables with sandbox/dev defaults.
func Load() Config {
	maxTries := env.EnvInt("NATS_MAX_DELIVER", 5)
	if maxTries < 1 {
		maxTries = 5
	}
	return Config{
		ServiceName:      env.EnvString("SERVICE_NAME", "notification"),
		GRPCPort:         env.EnvString("GRPC_PORT", "50058"),
		HTTPPort:         env.EnvString("HTTP_PORT", "8088"),
		DatabaseURL:      env.EnvString("DATABASE_URL", "postgres://nexusmart:nexusmart_dev_password@localhost:5432/notification_db?sslmode=disable"),
		NATSURL:          env.EnvString("NATS_URL", "nats://localhost:4222"),
		NATSStream:       env.EnvString("NATS_STREAM", "NEXUSMART"),
		NATSDLQStream:    env.EnvString("NATS_DLQ_STREAM", "NEXUSMART_DLQ"),
		ConsumerMaxTries: uint64(maxTries),
		SMTPHost:         env.EnvString("SMTP_HOST", "mailpit"),
		SMTPPort:         env.EnvInt("SMTP_PORT", 1025),
		SMTPFrom:         env.EnvString("SMTP_FROM", "no-reply@nexusmart.local"),
		UserGRPCAddr:     env.EnvString("USER_GRPC_ADDR", "user:50051"),
		WebBaseURL:       strings.TrimRight(strings.TrimSpace(env.EnvString("WEB_BASE_URL", "http://localhost:3000")), "/"),
		SiteURLScheme:    env.EnvString("SITE_URL_SCHEME", "https"),
		SiteURLPort:      env.EnvString("SITE_URL_PORT", ""),
		JWTSecret:        env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		OTLPEndpoint:     env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:     env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:         parseLogLevel(env.EnvString("LOG_LEVEL", "info")),
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
