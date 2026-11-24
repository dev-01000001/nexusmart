package e2e

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds endpoints, credentials, and webhook secrets for the E2E suite.
// Defaults align with docker-compose.yml and .env.example so `make e2e` runs with zero extra env vars.
type Config struct {
	GatewayURL          string
	UserHTTPURL         string
	ProductHTTPURL      string
	CartHTTPURL         string
	PromotionHTTPURL    string
	OrderHTTPURL        string
	PaymentHTTPURL      string
	ShippingHTTPURL     string
	NotificationHTTPURL string

	UserGRPCAddr      string
	PromotionGRPCAddr string
	ProductGRPCAddr   string
	OrderGRPCAddr     string
	PaymentGRPCAddr   string
	ShippingGRPCAddr  string

	NATSURL   string
	RedisAddr string

	MailpitURL string

	AdminEmail            string
	AdminPassword         string
	PaymentWebhookSecret  string
	ShippingWebhookSecret string
	// JWTSecret is the dev user-token key, used only to mint buyer tokens for the direct-gRPC flash-sale burst.
	JWTSecret        string
	ServiceJWTSecret string

	ShippingTestFaultInjection      bool
	ShippingCreateRetryMax          int
	ShippingCreateRetryBaseMS       int
	ShippingCreateRetryMaxBackoffMS int
	ShippingCreateWorkerIntervalMS  int
	CarrierMaxRetries               int
	CarrierCBMaxFailures            int
	CarrierCBOpenTimeout            time.Duration

	HTTPTimeout time.Duration
	PollTimeout time.Duration
}

// ServiceEndpoint describes a service's HTTP ops base URL.
type ServiceEndpoint struct {
	Name    string
	BaseURL string
}

// LoadConfig constructs Config from environment variables with local Docker Compose defaults.
func LoadConfig() Config {
	return Config{
		GatewayURL:          envOr("GATEWAY_URL", "http://localhost:8080"),
		UserHTTPURL:         envOr("USER_HTTP_URL", "http://localhost:8081"),
		ProductHTTPURL:      envOr("PRODUCT_HTTP_URL", "http://localhost:8082"),
		CartHTTPURL:         envOr("CART_HTTP_URL", "http://localhost:8083"),
		PromotionHTTPURL:    envOr("PROMOTION_HTTP_URL", "http://localhost:8084"),
		OrderHTTPURL:        envOr("ORDER_HTTP_URL", "http://localhost:8085"),
		PaymentHTTPURL:      envOr("PAYMENT_HTTP_URL", "http://localhost:8086"),
		ShippingHTTPURL:     envOr("SHIPPING_HTTP_URL", "http://localhost:8087"),
		NotificationHTTPURL: envOr("NOTIFICATION_HTTP_URL", "http://localhost:8088"),

		UserGRPCAddr:      envOr("USER_GRPC_ADDR", "localhost:50051"),
		PromotionGRPCAddr: envOr("PROMOTION_GRPC_ADDR", "localhost:50054"),
		ProductGRPCAddr:   envOr("PRODUCT_GRPC_ADDR", "localhost:50052"),
		OrderGRPCAddr:     envOr("ORDER_GRPC_ADDR", "localhost:50055"),
		PaymentGRPCAddr:   envOr("PAYMENT_GRPC_ADDR", "localhost:50056"),
		ShippingGRPCAddr:  envOr("SHIPPING_GRPC_ADDR", "localhost:50057"),

		NATSURL:   envOr("NATS_URL", "nats://localhost:4222"),
		RedisAddr: envOr("REDIS_ADDR", "localhost:6379"),

		MailpitURL: envOr("MAILPIT_URL", "http://localhost:8025"),

		AdminEmail:            envOrMulti([]string{"ADMIN_EMAIL", "ADMIN_BOOTSTRAP_EMAIL"}, "admin@nexusmart.local"),
		AdminPassword:         envOrMulti([]string{"ADMIN_PASSWORD", "ADMIN_BOOTSTRAP_PASSWORD"}, "AdminDevPassword123!"),
		PaymentWebhookSecret:  envOr("PAYMENT_WEBHOOK_SECRET", "dev-only-payment-webhook-hmac-secret"),
		ShippingWebhookSecret: envOr("SHIPPING_WEBHOOK_SECRET", "dev-only-shipping-webhook-hmac-secret"),
		JWTSecret:             envOr("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		ServiceJWTSecret:      envOr("SERVICE_JWT_SECRET", "dev-only-nexusmart-service-hs256-secret-change-me"),

		ShippingTestFaultInjection:      envBool("SHIPPING_TEST_FAULT_INJECTION", false),
		ShippingCreateRetryMax:          envInt("SHIPPING_CREATE_RETRY_MAX", 8),
		ShippingCreateRetryBaseMS:       envInt("SHIPPING_CREATE_RETRY_BASE_MS", 2000),
		ShippingCreateRetryMaxBackoffMS: envInt("SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS", 300000),
		ShippingCreateWorkerIntervalMS:  envInt("SHIPPING_CREATE_WORKER_INTERVAL_MS", 2000),
		CarrierMaxRetries:               envInt("CARRIER_MAX_RETRIES", 3),
		CarrierCBMaxFailures:            envInt("CARRIER_CB_MAX_FAILURES", 3),
		CarrierCBOpenTimeout:            envDuration("CARRIER_CB_OPEN_TIMEOUT", 30*time.Second),

		HTTPTimeout: 15 * time.Second,
		PollTimeout: 25 * time.Second,
	}
}

// AllServices returns the 9 core services and their HTTP ops ports.
func (c Config) AllServices() []ServiceEndpoint {
	return []ServiceEndpoint{
		{Name: "gateway", BaseURL: c.GatewayURL},
		{Name: "user", BaseURL: c.UserHTTPURL},
		{Name: "product", BaseURL: c.ProductHTTPURL},
		{Name: "cart", BaseURL: c.CartHTTPURL},
		{Name: "promotion", BaseURL: c.PromotionHTTPURL},
		{Name: "order", BaseURL: c.OrderHTTPURL},
		{Name: "payment", BaseURL: c.PaymentHTTPURL},
		{Name: "shipping", BaseURL: c.ShippingHTTPURL},
		{Name: "notification", BaseURL: c.NotificationHTTPURL},
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return strings.TrimRight(v, "/")
	}
	return strings.TrimRight(fallback, "/")
}

func envOrMulti(keys []string, fallback string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}
