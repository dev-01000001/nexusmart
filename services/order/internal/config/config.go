package config

import (
	"log/slog"
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/env"
)

// Config holds runtime configuration for the order service.
type Config struct {
	ServiceName string
	GRPCPort    string
	HTTPPort    string
	DatabaseURL string
	NATSURL     string
	NATSStream  string

	UserGRPCAddr      string
	ProductGRPCAddr   string
	CartGRPCAddr      string
	PromotionGRPCAddr string
	PaymentGRPCAddr   string
	ShippingGRPCAddr  string

	CODMaxRejectedCount       int32
	CODRequirePhoneVerified   bool
	CODMinOrdersForRate       int32
	CODMaxRejectRateBPS       int32
	FlashPaymentExpiryMinutes int
	RewardCoinsBPS            int32
	AutoCompleteDays          int
	SellerConfirmHours        int
	ReturnSellerResponseDays  int
	ReturnShipDays            int
	ReturnInspectDays         int
	BatchShipMaxItems         int
	GRPCCallTimeout           time.Duration
	OutboxPollInterval        time.Duration
	ExpiryWatchdogTimeout     time.Duration
	ExpiryWatchdogPeriod      time.Duration
	StaleCheckoutAfter        time.Duration

	JWTSecret    string
	OTLPEndpoint string
	OTLPInsecure bool
	LogLevel     slog.Level
}

// Load reads order service configuration from environment variables with safe dev defaults.
func Load() Config {
	return Config{
		ServiceName: env.EnvString("SERVICE_NAME", "order"),
		GRPCPort:    env.EnvString("GRPC_PORT", "50055"),
		HTTPPort:    env.EnvString("HTTP_PORT", "8085"),
		DatabaseURL: env.EnvString("DATABASE_URL", "postgres://nexusmart:nexusmart_dev_password@localhost:5432/order_db?sslmode=disable"),
		NATSURL:     env.EnvString("NATS_URL", "nats://localhost:4222"),
		NATSStream:  env.EnvString("NATS_STREAM", "NEXUSMART"),

		UserGRPCAddr:      env.EnvString("USER_GRPC_ADDR", "localhost:50051"),
		ProductGRPCAddr:   env.EnvString("PRODUCT_GRPC_ADDR", "localhost:50052"),
		CartGRPCAddr:      env.EnvString("CART_GRPC_ADDR", "localhost:50053"),
		PromotionGRPCAddr: env.EnvString("PROMOTION_GRPC_ADDR", "localhost:50054"),
		PaymentGRPCAddr:   env.EnvString("PAYMENT_GRPC_ADDR", "localhost:50056"),
		ShippingGRPCAddr:  env.EnvString("SHIPPING_GRPC_ADDR", "localhost:50057"),

		CODMaxRejectedCount:       int32(env.EnvInt("ORDER_COD_MAX_REJECTED_COUNT", 3)),
		CODRequirePhoneVerified:   env.EnvBool("ORDER_COD_REQUIRE_PHONE_VERIFIED", true),
		CODMinOrdersForRate:       int32(env.EnvInt("ORDER_COD_MIN_ORDERS_FOR_RATE", 3)),
		CODMaxRejectRateBPS:       int32(env.EnvInt("ORDER_COD_MAX_REJECT_RATE_BPS", 3000)),
		FlashPaymentExpiryMinutes: env.EnvInt("ORDER_FLASH_PAYMENT_EXPIRY_MINUTES", 5),
		RewardCoinsBPS:            int32(env.EnvInt("ORDER_REWARD_COINS_BPS", 100)),
		AutoCompleteDays:          env.EnvInt("ORDER_AUTO_COMPLETE_DAYS", 7),
		SellerConfirmHours:        env.EnvInt("ORDER_SELLER_CONFIRM_HOURS", 48),
		ReturnSellerResponseDays:  env.EnvInt("ORDER_RETURN_SELLER_RESPONSE_DAYS", 3),
		ReturnShipDays:            env.EnvInt("ORDER_RETURN_SHIP_DAYS", 5),
		ReturnInspectDays:         env.EnvInt("ORDER_RETURN_INSPECT_DAYS", 3),
		BatchShipMaxItems:         env.EnvInt("ORDER_BATCH_SHIP_MAX_ITEMS", 50),
		GRPCCallTimeout:           env.EnvDuration("ORDER_GRPC_TIMEOUT", 10*time.Second),
		OutboxPollInterval:        env.EnvDuration("ORDER_OUTBOX_POLL_INTERVAL", 250*time.Millisecond),
		ExpiryWatchdogPeriod:      env.EnvDuration("ORDER_EXPIRY_WATCHDOG_INTERVAL", 10*time.Second),
		ExpiryWatchdogTimeout:     env.EnvDuration("ORDER_EXPIRY_WATCHDOG_TIMEOUT", 15*time.Second),
		StaleCheckoutAfter:        env.EnvDuration("ORDER_STALE_CHECKOUT_AFTER", 5*time.Minute),

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
