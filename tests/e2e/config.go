package e2e

import (
	"os"
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

	UserGRPCAddr         string
	ProductGRPCAddr      string
	CartGRPCAddr         string
	PromotionGRPCAddr    string
	OrderGRPCAddr        string
	PaymentGRPCAddr      string
	ShippingGRPCAddr     string
	NotificationGRPCAddr string

	RedisAddr  string
	MailpitURL string

	AdminEmail            string
	AdminPassword         string
	PaymentWebhookSecret  string
	ShippingWebhookSecret string
	// JWTSecret is the dev user-token key, used only to mint buyer tokens for the direct-gRPC flash-sale burst.
	JWTSecret string

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

		UserGRPCAddr:         envOr("USER_GRPC_ADDR", "localhost:50051"),
		ProductGRPCAddr:      envOr("PRODUCT_GRPC_ADDR", "localhost:50052"),
		CartGRPCAddr:         envOr("CART_GRPC_ADDR", "localhost:50053"),
		PromotionGRPCAddr:    envOr("PROMOTION_GRPC_ADDR", "localhost:50054"),
		OrderGRPCAddr:        envOr("ORDER_GRPC_ADDR", "localhost:50055"),
		PaymentGRPCAddr:      envOr("PAYMENT_GRPC_ADDR", "localhost:50056"),
		ShippingGRPCAddr:     envOr("SHIPPING_GRPC_ADDR", "localhost:50057"),
		NotificationGRPCAddr: envOr("NOTIFICATION_GRPC_ADDR", "localhost:50058"),

		RedisAddr:  defaultRedisAddr(envOr("GATEWAY_URL", "http://localhost:8080")),
		MailpitURL: envOr("MAILPIT_URL", "http://localhost:8025"),

		AdminEmail:            envOrMulti([]string{"ADMIN_EMAIL", "ADMIN_BOOTSTRAP_EMAIL"}, "admin@nexusmart.local"),
		AdminPassword:         envOrMulti([]string{"ADMIN_PASSWORD", "ADMIN_BOOTSTRAP_PASSWORD"}, "AdminDevPassword123!"),
		PaymentWebhookSecret:  envOr("PAYMENT_WEBHOOK_SECRET", "dev-only-payment-webhook-hmac-secret"),
		ShippingWebhookSecret: envOr("SHIPPING_WEBHOOK_SECRET", "dev-only-shipping-webhook-hmac-secret"),
		JWTSecret:             envOr("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),

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

func defaultRedisAddr(gatewayURL string) string {
	if v := strings.TrimSpace(os.Getenv("REDIS_ADDR")); v != "" {
		return v
	}
	if strings.Contains(gatewayURL, "://gateway:") {
		return "redis:6379"
	}
	return ""
}
