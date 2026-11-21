package config

import (
	"time"

	"github.com/nexusmart/core/pkg/common/env"
)

// Config holds runtime configuration for the API gateway.
type Config struct {
	ServiceName          string
	HTTPPort             string
	RedisAddr            string
	RedisPassword        string
	JWTSecret            string
	RateLimitIPPerMin    int
	RateLimitUserPerMin  int
	RateLimitFlashPerMin int
	TrustedProxyCIDRs    string
	UserGRPCAddr         string
	ProductGRPCAddr      string
	CartGRPCAddr         string
	PromotionGRPCAddr    string
	OrderGRPCAddr        string
	PaymentGRPCAddr      string
	PaymentHTTPURL       string
	ShippingGRPCAddr     string
	ShippingHTTPURL      string
	NotificationGRPCAddr string
	SearchGRPCAddr       string
	GRPCCallTimeout      time.Duration
	OTLPEndpoint         string
	OTLPInsecure         bool
	LogLevel             string
}

// Load reads gateway configuration from environment variables.
func Load() Config {
	return Config{
		ServiceName:          env.EnvString("SERVICE_NAME", "gateway"),
		HTTPPort:             env.EnvString("HTTP_PORT", "8080"),
		RedisAddr:            env.EnvString("REDIS_ADDR", "localhost:6379"),
		RedisPassword:        env.EnvString("REDIS_PASSWORD", ""),
		JWTSecret:            env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		RateLimitIPPerMin:    env.EnvInt("RATE_LIMIT_IP_PER_MIN", 300),
		RateLimitUserPerMin:  env.EnvInt("RATE_LIMIT_USER_PER_MIN", 180),
		RateLimitFlashPerMin: env.EnvInt("RATE_LIMIT_FLASH_SALE_PER_MIN", 60),
		TrustedProxyCIDRs:    env.EnvString("TRUSTED_PROXY_CIDRS", ""),
		UserGRPCAddr:         env.EnvString("USER_GRPC_ADDR", "localhost:50051"),
		ProductGRPCAddr:      env.EnvString("PRODUCT_GRPC_ADDR", "localhost:50052"),
		CartGRPCAddr:         env.EnvString("CART_GRPC_ADDR", "localhost:50053"),
		PromotionGRPCAddr:    env.EnvString("PROMOTION_GRPC_ADDR", "localhost:50054"),
		OrderGRPCAddr:        env.EnvString("ORDER_GRPC_ADDR", "localhost:50055"),
		PaymentGRPCAddr:      env.EnvString("PAYMENT_GRPC_ADDR", "localhost:50056"),
		PaymentHTTPURL:       env.EnvString("PAYMENT_HTTP_URL", "http://localhost:8086"),
		ShippingGRPCAddr:     env.EnvString("SHIPPING_GRPC_ADDR", "localhost:50057"),
		ShippingHTTPURL:      env.EnvString("SHIPPING_HTTP_URL", "http://localhost:8087"),
		NotificationGRPCAddr: env.EnvString("NOTIFICATION_GRPC_ADDR", "localhost:50058"),
		SearchGRPCAddr:       env.EnvString("SEARCH_GRPC_ADDR", "localhost:50059"),
		GRPCCallTimeout:      env.EnvDuration("GRPC_CALL_TIMEOUT", 10*time.Second),
		OTLPEndpoint:         env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:         env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:             env.EnvString("LOG_LEVEL", "info"),
	}
}
