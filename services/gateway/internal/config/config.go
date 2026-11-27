package config

import (
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/env"
	"github.com/nexusmart/core/pkg/common/storage"
)

// Config holds runtime configuration for the API gateway.
type Config struct {
	ServiceName                      string
	HTTPPort                         string
	RedisAddr                        string
	RedisPassword                    string
	JWTSecret                        string
	RateLimitIPPerMin                int
	RateLimitBFFPerMin               int
	RateLimitUserPerMin              int
	RateLimitFlashPerMin             int
	RateLimitAuthPerMin              int
	RateLimitBankOTPPerMin           int
	RateLimitPhoneOTPPerMin          int
	RateLimitShippingFeePerMin       int
	RateLimitOrderLookupPerMin       int
	RateLimitOrderLookupCodePer10Min int
	RateLimitWebhookPerMin           int
	ProductImportMaxRows             int
	ProductImportMaxBytes            int64
	TrustedProxies                   string
	UserGRPCAddr                     string
	ProductGRPCAddr                  string
	CartGRPCAddr                     string
	PromotionGRPCAddr                string
	OrderGRPCAddr                    string
	PaymentGRPCAddr                  string
	PaymentHTTPURL                   string
	ShippingGRPCAddr                 string
	ShippingHTTPURL                  string
	NotificationGRPCAddr             string
	SearchGRPCAddr                   string
	SearchFallbackTimeout            time.Duration
	GRPCCallTimeout                  time.Duration
	S3                               storage.Config
	OTLPEndpoint                     string
	OTLPInsecure                     bool
	LogLevel                         string
}

// Load reads gateway configuration from environment variables.
func Load() Config {
	searchFallbackMS := env.EnvInt("SEARCH_FALLBACK_TIMEOUT_MS", 1500)
	if searchFallbackMS <= 0 {
		searchFallbackMS = 1500
	}
	importMaxRows := env.EnvInt("PRODUCT_IMPORT_MAX_ROWS", 1000)
	if importMaxRows <= 0 {
		importMaxRows = 1000
	}
	importMaxBytes := env.EnvInt("PRODUCT_IMPORT_MAX_BYTES", 1048576)
	if importMaxBytes <= 0 {
		importMaxBytes = 1048576
	}
	trustedProxies := strings.TrimSpace(env.EnvString("TRUSTED_PROXIES", ""))
	if trustedProxies == "" {
		trustedProxies = strings.TrimSpace(env.EnvString("TRUSTED_PROXY_CIDRS", ""))
	}
	return Config{
		ServiceName:                      env.EnvString("SERVICE_NAME", "gateway"),
		HTTPPort:                         env.EnvString("HTTP_PORT", "8080"),
		RedisAddr:                        env.EnvString("REDIS_ADDR", "localhost:6379"),
		RedisPassword:                    env.EnvString("REDIS_PASSWORD", ""),
		JWTSecret:                        env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		RateLimitIPPerMin:                env.EnvInt("RATE_LIMIT_IP_PER_MIN", 300),
		RateLimitBFFPerMin:               env.EnvInt("RATE_LIMIT_BFF_PER_MIN", 6000),
		RateLimitUserPerMin:              env.EnvInt("RATE_LIMIT_USER_PER_MIN", 180),
		RateLimitFlashPerMin:             env.EnvInt("RATE_LIMIT_FLASH_SALE_PER_MIN", 60),
		RateLimitAuthPerMin:              env.EnvInt("RATE_LIMIT_AUTH_PER_MIN", 10),
		RateLimitBankOTPPerMin:           env.EnvInt("RATE_LIMIT_BANK_OTP_PER_MIN", 5),
		RateLimitPhoneOTPPerMin:          env.EnvInt("RATE_LIMIT_PHONE_OTP_PER_MIN", 5),
		RateLimitShippingFeePerMin:       env.EnvInt("RATE_LIMIT_SHIPPING_FEE_PER_MIN", 30),
		RateLimitOrderLookupPerMin:       env.EnvInt("RATE_LIMIT_ORDER_LOOKUP_PER_MIN", 10),
		RateLimitOrderLookupCodePer10Min: env.EnvInt("RATE_LIMIT_ORDER_LOOKUP_CODE_PER_10MIN", 5),
		RateLimitWebhookPerMin:           env.EnvInt("RATE_LIMIT_WEBHOOK_PER_MIN", 600),
		ProductImportMaxRows:             importMaxRows,
		ProductImportMaxBytes:            int64(importMaxBytes),
		TrustedProxies:                   trustedProxies,
		UserGRPCAddr:                     env.EnvString("USER_GRPC_ADDR", "localhost:50051"),
		ProductGRPCAddr:                  env.EnvString("PRODUCT_GRPC_ADDR", "localhost:50052"),
		CartGRPCAddr:                     env.EnvString("CART_GRPC_ADDR", "localhost:50053"),
		PromotionGRPCAddr:                env.EnvString("PROMOTION_GRPC_ADDR", "localhost:50054"),
		OrderGRPCAddr:                    env.EnvString("ORDER_GRPC_ADDR", "localhost:50055"),
		PaymentGRPCAddr:                  env.EnvString("PAYMENT_GRPC_ADDR", "localhost:50056"),
		PaymentHTTPURL:                   env.EnvString("PAYMENT_HTTP_URL", "http://localhost:8086"),
		ShippingGRPCAddr:                 env.EnvString("SHIPPING_GRPC_ADDR", "localhost:50057"),
		ShippingHTTPURL:                  env.EnvString("SHIPPING_HTTP_URL", "http://localhost:8087"),
		NotificationGRPCAddr:             env.EnvString("NOTIFICATION_GRPC_ADDR", "localhost:50058"),
		SearchGRPCAddr:                   env.EnvString("SEARCH_GRPC_ADDR", "search:50059"),
		SearchFallbackTimeout:            time.Duration(searchFallbackMS) * time.Millisecond,
		GRPCCallTimeout:                  env.EnvDuration("GRPC_CALL_TIMEOUT", 10*time.Second),
		S3:                               storage.LoadConfigFromEnv(),
		OTLPEndpoint:                     env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:                     env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:                         env.EnvString("LOG_LEVEL", "info"),
	}
}
