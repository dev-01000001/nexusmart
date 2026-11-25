package config

import (
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/env"
)

// Config holds runtime configuration for the shipping service.
type Config struct {
	ServiceName           string
	GRPCPort              string
	HTTPPort              string
	DatabaseURL           string
	RedisAddr             string
	RedisPassword         string
	UserGRPCAddr          string
	NATSURL               string
	NATSStream            string
	NATSDLQStream         string
	JWTSecret             string
	WebhookSecret         string
	MaxRedelivery         int32
	ConfigKey             string
	LabelBaseURL          string
	GHNMode               string
	GHNBaseURL            string
	GHNToken              string
	GHNShopID             int
	GHTKMode              string
	GHTKBaseURL           string
	GHTKToken             string
	QuoteCacheTTL         time.Duration
	CarrierTimeout        time.Duration
	CarrierRetries        int
	CarrierBackoff        time.Duration
	CBMaxFailures         uint32
	CBOpenTimeout         time.Duration
	OutboxInterval        time.Duration
	PollInterval          time.Duration
	ConsumerMaxTries      uint64
	CreateRetryMax        int32
	CreateRetryBase       time.Duration
	CreateRetryMaxBackoff time.Duration
	CreateWorkerInterval  time.Duration
	TestFaultInjection    bool
	TestFailCreateCount   int
	OTLPEndpoint          string
	OTLPInsecure          bool
	LogLevel              slog.Level
}

// Load reads configuration from environment variables with sandbox/dev defaults.
func Load() Config {
	quoteCacheTTL := 60 * time.Second
	if strings.TrimSpace(os.Getenv("SHIPPING_FEE_CACHE_TTL_SECONDS")) != "" {
		quoteCacheTTL = time.Duration(maxInt(env.EnvInt("SHIPPING_FEE_CACHE_TTL_SECONDS", 60), 1)) * time.Second
	} else if strings.TrimSpace(os.Getenv("QUOTE_CACHE_TTL")) != "" {
		quoteCacheTTL = env.EnvDuration("QUOTE_CACHE_TTL", 60*time.Second)
	}

	return Config{
		ServiceName:           env.EnvString("SERVICE_NAME", "shipping"),
		GRPCPort:              env.EnvString("GRPC_PORT", "50057"),
		HTTPPort:              env.EnvString("HTTP_PORT", "8087"),
		DatabaseURL:           env.EnvString("DATABASE_URL", "postgres://nexusmart:nexusmart_dev_password@localhost:5432/shipping_db?sslmode=disable"),
		RedisAddr:             env.EnvString("REDIS_ADDR", "localhost:6379"),
		RedisPassword:         env.EnvString("REDIS_PASSWORD", ""),
		UserGRPCAddr:          env.EnvString("USER_GRPC_ADDR", "localhost:50051"),
		NATSURL:               env.EnvString("NATS_URL", "nats://localhost:4222"),
		NATSStream:            env.EnvString("NATS_STREAM", "NEXUSMART"),
		NATSDLQStream:         env.EnvString("NATS_DLQ_STREAM", "NEXUSMART_DLQ"),
		JWTSecret:             env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		WebhookSecret:         env.EnvString("SHIPPING_WEBHOOK_SECRET", "dev-only-shipping-webhook-hmac-secret"),
		MaxRedelivery:         int32(maxInt(env.EnvInt("SHIPPING_MAX_REDELIVERY", 3), 1)),
		ConfigKey:             env.EnvString("SHIPPING_CONFIG_KEY", "ZGV2LW9ubHktc2hpcHBpbmctY29uZmlnLWtleS0zMmI="),
		LabelBaseURL:          strings.TrimRight(env.EnvString("SHIPPING_LABEL_BASE_URL", "http://localhost:8080/api/v1/shipping/labels"), "/"),
		GHNMode:               strings.ToLower(env.EnvString("GHN_MODE", "mock")),
		GHNBaseURL:            env.EnvString("GHN_BASE_URL", "https://dev-online-gateway.ghn.vn/shiip/public-api"),
		GHNToken:              env.EnvString("GHN_TOKEN", "sandbox-ghn-token"),
		GHNShopID:             env.EnvInt("GHN_SHOP_ID", 0),
		GHTKMode:              strings.ToLower(env.EnvString("GHTK_MODE", "mock")),
		GHTKBaseURL:           env.EnvString("GHTK_BASE_URL", "https://services-staging.ghtklab.com"),
		GHTKToken:             env.EnvString("GHTK_TOKEN", "sandbox-ghtk-token"),
		QuoteCacheTTL:         quoteCacheTTL,
		CarrierTimeout:        env.EnvDuration("CARRIER_TIMEOUT", 3*time.Second),
		CarrierRetries:        env.EnvInt("CARRIER_MAX_RETRIES", 3),
		CarrierBackoff:        env.EnvDuration("CARRIER_RETRY_BACKOFF", 100*time.Millisecond),
		CBMaxFailures:         uint32(maxInt(env.EnvInt("CARRIER_CB_MAX_FAILURES", 3), 1)),
		CBOpenTimeout:         env.EnvDuration("CARRIER_CB_OPEN_TIMEOUT", 30*time.Second),
		OutboxInterval:        env.EnvDuration("OUTBOX_POLL_INTERVAL", 250*time.Millisecond),
		PollInterval:          env.EnvDuration("SHIPMENT_POLL_INTERVAL", 60*time.Second),
		ConsumerMaxTries:      uint64(maxInt(env.EnvInt("NATS_MAX_DELIVER", 5), 1)),
		CreateRetryMax:        int32(maxInt(env.EnvInt("SHIPPING_CREATE_RETRY_MAX", 8), 1)),
		CreateRetryBase:       time.Duration(maxInt(env.EnvInt("SHIPPING_CREATE_RETRY_BASE_MS", 2000), 10)) * time.Millisecond,
		CreateRetryMaxBackoff: time.Duration(maxInt(env.EnvInt("SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS", 300000), 100)) * time.Millisecond,
		CreateWorkerInterval:  time.Duration(maxInt(env.EnvInt("SHIPPING_CREATE_WORKER_INTERVAL_MS", 2000), 10)) * time.Millisecond,
		TestFaultInjection:    env.EnvBool("SHIPPING_TEST_FAULT_INJECTION", false),
		TestFailCreateCount:   maxInt(env.EnvInt("SHIPPING_TEST_FAIL_CREATE_COUNT", 0), 0),
		OTLPEndpoint:          env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:          env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:              parseLogLevel(env.EnvString("LOG_LEVEL", "info")),
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
