package config

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/env"
)

const defaultMaxImageBytes = 5 * 1024 * 1024 // 5 MiB

// Config holds runtime configuration for the chat service.
type Config struct {
	ServiceName       string
	InstanceID        string
	GRPCPort          string
	HTTPPort          string
	DatabaseURL       string
	RedisAddr         string
	RedisPassword     string
	JWTSecret         string
	ServiceJWTSecret  string
	UserGRPCAddr      string
	SiteRootDomain    string
	WSTicketTTL       time.Duration
	WSAllowedOrigins  []string
	ImageMaxBytes     int64
	ImageValidateMode string
	ImageAllowedHosts []string
	OTLPEndpoint      string
	OTLPInsecure      bool
	LogLevel          slog.Level
}

// Load parses environment variables with dev/sandbox defaults.
func Load() Config {
	instanceID := env.EnvString("CHAT_INSTANCE_ID", "")
	if instanceID == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "chat"
		}
		instanceID = host + "-" + uuid.NewString()[:8]
	}

	maxBytes := int64(env.EnvInt("CHAT_IMAGE_MAX_BYTES", defaultMaxImageBytes))
	if maxBytes <= 0 {
		maxBytes = defaultMaxImageBytes
	}

	ticketTTL := env.EnvDuration("CHAT_WS_TICKET_TTL", 60*time.Second)
	if ticketTTL <= 0 {
		ticketTTL = 60 * time.Second
	}

	return Config{
		ServiceName:       env.EnvString("SERVICE_NAME", "chat"),
		InstanceID:        instanceID,
		GRPCPort:          env.EnvString("GRPC_PORT", "50060"),
		HTTPPort:          env.EnvString("HTTP_PORT", "8090"),
		DatabaseURL:       env.EnvString("DATABASE_URL", "postgres://nexusmart:nexusmart_dev_password@localhost:5432/chat_db?sslmode=disable"),
		RedisAddr:         env.EnvString("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     env.EnvString("REDIS_PASSWORD", ""),
		JWTSecret:         env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		ServiceJWTSecret:  env.EnvString("SERVICE_JWT_SECRET", ""),
		UserGRPCAddr:      env.EnvString("USER_GRPC_ADDR", ""),
		SiteRootDomain:    env.EnvString("SITE_ROOT_DOMAIN", "nexusmart.localhost"),
		WSTicketTTL:       ticketTTL,
		WSAllowedOrigins:  splitCSV(env.EnvString("CHAT_WS_ALLOWED_ORIGINS", "")),
		ImageMaxBytes:     maxBytes,
		ImageValidateMode: strings.ToLower(env.EnvString("CHAT_IMAGE_VALIDATE_MODE", "hybrid")),
		ImageAllowedHosts: splitCSV(env.EnvString("CHAT_IMAGE_ALLOWED_HOSTS", "")),
		OTLPEndpoint:      env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:      env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:          parseLogLevel(env.EnvString("LOG_LEVEL", "info")),
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
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
