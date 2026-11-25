package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/nexusmart/core/pkg/common/env"
)

const (
	EmbeddingModeMock   = "mock"
	EmbeddingModeOpenAI = "openai"

	DefaultEmbeddingDim        = 1536
	DefaultVectorMinSimilarity = 0.25
)

// Config holds runtime configuration for the search service.
type Config struct {
	ServiceName         string
	GRPCPort            string
	HTTPPort            string
	DatabaseURL         string
	RedisAddr           string
	NATSURL             string
	NATSStream          string
	NATSDLQStream       string
	MeilisearchURL      string
	MeiliMasterKey      string
	ProductGRPCAddr     string
	UserGRPCAddr        string
	EmbeddingMode       string
	EmbeddingModel      string
	EmbeddingDim        int
	EmbeddingAPIKey     string
	EmbeddingBaseURL    string
	RRFK                int
	MeiliTimeoutMS      int
	VectorTimeoutMS     int
	VectorMinSimilarity float64
	JWTSecret           string
	ServiceJWTSecret    string
	OTLPEndpoint        string
	OTLPInsecure        bool
	LogLevel            slog.Level
}

// Load reads environment variables with safe dev/sandbox defaults (G12/G13/S6).
func Load() Config {
	return Config{
		ServiceName:         env.EnvString("SERVICE_NAME", "search"),
		GRPCPort:            env.EnvString("GRPC_PORT", "50059"),
		HTTPPort:            env.EnvString("HTTP_PORT", "8089"),
		DatabaseURL:         env.EnvString("DATABASE_URL", "postgres://nexusmart:nexusmart_dev_password@localhost:5432/search_db?sslmode=disable"),
		RedisAddr:           env.EnvString("REDIS_ADDR", "localhost:6379"),
		NATSURL:             env.EnvString("NATS_URL", "nats://localhost:4222"),
		NATSStream:          env.EnvString("NATS_STREAM", "NEXUSMART"),
		NATSDLQStream:       env.EnvString("NATS_DLQ_STREAM", "NEXUSMART_DLQ"),
		MeilisearchURL:      envFirstString("http://meilisearch:7700", "MEILISEARCH_URL", "MEILI_URL"),
		MeiliMasterKey:      env.EnvString("MEILI_MASTER_KEY", "dev-only-meilisearch-master-key-change-me"),
		ProductGRPCAddr:     env.EnvString("PRODUCT_GRPC_ADDR", "product:50052"),
		UserGRPCAddr:        env.EnvString("USER_GRPC_ADDR", "user:50051"),
		EmbeddingMode:       strings.ToLower(strings.TrimSpace(env.EnvString("EMBEDDING_MODE", EmbeddingModeMock))),
		EmbeddingModel:      env.EnvString("EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingDim:        env.EnvInt("EMBEDDING_DIM", DefaultEmbeddingDim),
		EmbeddingAPIKey:     env.EnvString("EMBEDDING_API_KEY", ""),
		EmbeddingBaseURL:    env.EnvString("EMBEDDING_BASE_URL", "https://api.openai.com/v1"),
		RRFK:                env.EnvInt("SEARCH_RRF_K", 60),
		MeiliTimeoutMS:      env.EnvInt("SEARCH_MEILI_TIMEOUT_MS", 1500),
		VectorTimeoutMS:     env.EnvInt("SEARCH_VECTOR_TIMEOUT_MS", 800),
		VectorMinSimilarity: parseEnvFloat("SEARCH_VECTOR_MIN_SIMILARITY", DefaultVectorMinSimilarity),
		JWTSecret:           env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		ServiceJWTSecret:    env.EnvString("SERVICE_JWT_SECRET", "dev-only-nexusmart-service-hs256-secret-change-me"),
		OTLPEndpoint:        env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:        env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:            parseLogLevel(env.EnvString("LOG_LEVEL", "info")),
	}
}

// Validate checks hard startup invariants (G12: refuse startup if openai mode lacks API key).
func (c Config) Validate() error {
	mode := strings.ToLower(strings.TrimSpace(c.EmbeddingMode))
	if mode == "" {
		mode = EmbeddingModeMock
	}
	switch mode {
	case EmbeddingModeMock:
	case EmbeddingModeOpenAI:
		if strings.TrimSpace(c.EmbeddingAPIKey) == "" {
			return errors.New("EMBEDDING_API_KEY is required when EMBEDDING_MODE=openai")
		}
	default:
		return fmt.Errorf("unsupported EMBEDDING_MODE %q (allowed: mock, openai)", c.EmbeddingMode)
	}
	if c.EmbeddingDim != DefaultEmbeddingDim {
		return fmt.Errorf("EMBEDDING_DIM must be %d to match schema vector(1536), got %d", DefaultEmbeddingDim, c.EmbeddingDim)
	}
	if c.RRFK <= 0 {
		return fmt.Errorf("SEARCH_RRF_K must be positive, got %d", c.RRFK)
	}
	if c.MeiliTimeoutMS <= 0 {
		return fmt.Errorf("SEARCH_MEILI_TIMEOUT_MS must be positive, got %d", c.MeiliTimeoutMS)
	}
	if c.VectorTimeoutMS <= 0 {
		return fmt.Errorf("SEARCH_VECTOR_TIMEOUT_MS must be positive, got %d", c.VectorTimeoutMS)
	}
	if c.VectorMinSimilarity < 0 || c.VectorMinSimilarity > 1 {
		return fmt.Errorf("SEARCH_VECTOR_MIN_SIMILARITY must be in [0, 1], got %v", c.VectorMinSimilarity)
	}
	return nil
}

func envFirstString(defaultVal string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(env.EnvString(k, "")); v != "" {
			return v
		}
	}
	return defaultVal
}

func parseEnvFloat(key string, defaultVal float64) float64 {
	raw := strings.TrimSpace(env.EnvString(key, ""))
	if raw == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return defaultVal
	}
	return v
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
