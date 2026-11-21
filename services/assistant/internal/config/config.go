package config

import (
	"log/slog"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/env"
)

// Config holds runtime configuration for the AI assistant service.
type Config struct {
	ServiceName string
	HTTPPort    string
	GRPCPort    string
	LogLevel    slog.Level

	DatabaseURL   string
	RedisAddr     string
	RedisPassword string
	NATSURL       string
	NATSStream    string
	NATSDLQStream string

	JWTSecret string

	// Upstream service endpoints (trusted config only — never user-supplied).
	GatewayURL      string
	SearchGRPCAddr  string
	ProductGRPCAddr string
	ChatGRPCAddr    string

	// LLM settings (LLM_MODE=mock|anthropic).
	LLMMode            string
	LLMModel           string
	LLMFallbackModel   string
	AnthropicAPIKey    string
	AnthropicBaseURL   string
	LLMTimeout         time.Duration
	InputCostVNDPer1K  int64
	OutputCostVNDPer1K int64

	// Embedding settings (EMBED_MODE=mock|voyage) for policy RAG.
	EmbedMode   string
	VoyageKey   string
	VoyageModel string

	// Quotas, cost cap, caching, and abuse protection.
	DailyUserTokenLimit int64
	DailyCostLimitVND   int64
	CacheTTL            time.Duration
	ReviewSummaryTTL    time.Duration
	MinReviewsSummary   int
	AbuseStrikeLimit    int
	AbuseLockoutWindow  time.Duration

	// Business hours (Asia/Ho_Chi_Minh, 24h) for handoff estimation.
	BusinessHourStart int
	BusinessHourEnd   int

	// PoliciesDir overrides embedded policies if non-empty.
	PoliciesDir string

	OTLPEndpoint string
	OTLPInsecure bool
}

// Load parses assistant configuration from environment variables with safe dev defaults.
func Load() Config {
	return Config{
		ServiceName: env.EnvString("SERVICE_NAME", "assistant"),
		HTTPPort:    env.EnvString("HTTP_PORT", "8091"),
		GRPCPort:    env.EnvString("GRPC_PORT", "50061"),
		LogLevel:    parseLogLevel(env.EnvString("LOG_LEVEL", "info")),

		DatabaseURL:   env.EnvString("DATABASE_URL", ""),
		RedisAddr:     env.EnvString("REDIS_ADDR", "redis:6379"),
		RedisPassword: env.EnvString("REDIS_PASSWORD", ""),
		NATSURL:       env.EnvString("NATS_URL", ""),
		NATSStream:    env.EnvString("NATS_STREAM", "NEXUSMART"),
		NATSDLQStream: env.EnvString("NATS_DLQ_STREAM", "NEXUSMART_DLQ"),

		JWTSecret: env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),

		GatewayURL:      strings.TrimRight(env.EnvString("GATEWAY_URL", "http://gateway:8080"), "/"),
		SearchGRPCAddr:  env.EnvString("SEARCH_GRPC_ADDR", "search:50059"),
		ProductGRPCAddr: env.EnvString("PRODUCT_GRPC_ADDR", "product:50052"),
		ChatGRPCAddr:    env.EnvString("CHAT_GRPC_ADDR", "chat:50060"),

		LLMMode:            strings.ToLower(strings.TrimSpace(env.EnvString("LLM_MODE", "mock"))),
		LLMModel:           env.EnvString("LLM_MODEL", "claude-opus-5-5"),
		LLMFallbackModel:   env.EnvString("LLM_FALLBACK_MODEL", ""),
		AnthropicAPIKey:    env.EnvString("ANTHROPIC_API_KEY", ""),
		AnthropicBaseURL:   strings.TrimRight(env.EnvString("ANTHROPIC_BASE_URL", "https://api.anthropic.com"), "/"),
		LLMTimeout:         env.EnvDuration("LLM_TIMEOUT", 45*time.Second),
		InputCostVNDPer1K:  int64(env.EnvInt("LLM_INPUT_COST_VND_PER_1K", 380)),
		OutputCostVNDPer1K: int64(env.EnvInt("LLM_OUTPUT_COST_VND_PER_1K", 1900)),

		EmbedMode:   strings.ToLower(strings.TrimSpace(env.EnvString("EMBED_MODE", "mock"))),
		VoyageKey:   env.EnvString("VOYAGE_API_KEY", ""),
		VoyageModel: env.EnvString("VOYAGE_MODEL", "voyage-3-lite"),

		DailyUserTokenLimit: int64(env.EnvInt("ASSISTANT_DAILY_USER_TOKEN_LIMIT", 50000)),
		DailyCostLimitVND:   int64(env.EnvInt("ASSISTANT_DAILY_COST_LIMIT_VND", 500000)),
		CacheTTL:            env.EnvDuration("ASSISTANT_CACHE_TTL", 10*time.Minute),
		ReviewSummaryTTL:    env.EnvDuration("ASSISTANT_REVIEW_SUMMARY_TTL", time.Hour),
		MinReviewsSummary:   env.EnvInt("ASSISTANT_MIN_REVIEWS_SUMMARY", 10),
		AbuseStrikeLimit:    env.EnvInt("ASSISTANT_ABUSE_STRIKE_LIMIT", 5),
		AbuseLockoutWindow:  env.EnvDuration("ASSISTANT_ABUSE_LOCKOUT_WINDOW", 15*time.Minute),

		BusinessHourStart: env.EnvInt("ASSISTANT_BUSINESS_HOUR_START", 8),
		BusinessHourEnd:   env.EnvInt("ASSISTANT_BUSINESS_HOUR_END", 22),

		PoliciesDir: env.EnvString("ASSISTANT_POLICIES_DIR", ""),

		OTLPEndpoint: env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure: env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
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
