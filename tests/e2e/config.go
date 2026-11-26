package e2e

import (
	"fmt"
	"net/url"
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
	SearchHTTPURL       string
	ChatHTTPURL         string
	AssistantHTTPURL    string

	UserGRPCAddr         string
	ProductGRPCAddr      string
	CartGRPCAddr         string
	PromotionGRPCAddr    string
	OrderGRPCAddr        string
	PaymentGRPCAddr      string
	ShippingGRPCAddr     string
	NotificationGRPCAddr string
	SearchGRPCAddr       string
	ChatGRPCAddr         string
	AssistantGRPCAddr    string

	MeilisearchURL string
	MeiliMasterKey string
	OTelPromURL    string
	OTelHealthURL  string

	NATSURL         string
	RedisAddr       string
	RedisPassword   string
	UserDatabaseURL string
	MailpitURL      string
	SiteRootDomain  string

	AdminEmail            string
	AdminPassword         string
	PaymentWebhookSecret  string
	ShippingWebhookSecret string
	// ZaloPay mock/sandbox callback verification settings (F1/F2).
	// When env vars are unset in mock mode, payment uses app_id=1 and dev-only-zalopay-mock-key1|key2.
	ZaloPayAppID           int
	ZaloPayKey1            string
	ZaloPayKey2            string
	LargeRefundApprovalVND int64
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
	gatewayURL := envOr("GATEWAY_URL", "http://localhost:8080")
	return Config{
		GatewayURL:          gatewayURL,
		UserHTTPURL:         envOr("USER_HTTP_URL", "http://localhost:8081"),
		ProductHTTPURL:      envOr("PRODUCT_HTTP_URL", "http://localhost:8082"),
		CartHTTPURL:         envOr("CART_HTTP_URL", "http://localhost:8083"),
		PromotionHTTPURL:    envOr("PROMOTION_HTTP_URL", "http://localhost:8084"),
		OrderHTTPURL:        envOr("ORDER_HTTP_URL", "http://localhost:8085"),
		PaymentHTTPURL:      envOr("PAYMENT_HTTP_URL", "http://localhost:8086"),
		ShippingHTTPURL:     envOr("SHIPPING_HTTP_URL", "http://localhost:8087"),
		NotificationHTTPURL: envOr("NOTIFICATION_HTTP_URL", "http://localhost:8088"),
		SearchHTTPURL:       envOr("SEARCH_HTTP_URL", "http://localhost:8089"),
		ChatHTTPURL:         envOr("CHAT_HTTP_URL", "http://localhost:8090"),
		AssistantHTTPURL:    envOr("ASSISTANT_HTTP_URL", "http://localhost:8091"),

		UserGRPCAddr:         envOr("USER_GRPC_ADDR", "localhost:50051"),
		ProductGRPCAddr:      envOr("PRODUCT_GRPC_ADDR", "localhost:50052"),
		CartGRPCAddr:         envOr("CART_GRPC_ADDR", "localhost:50053"),
		PromotionGRPCAddr:    envOr("PROMOTION_GRPC_ADDR", "localhost:50054"),
		OrderGRPCAddr:        envOr("ORDER_GRPC_ADDR", "localhost:50055"),
		PaymentGRPCAddr:      envOr("PAYMENT_GRPC_ADDR", "localhost:50056"),
		ShippingGRPCAddr:     envOr("SHIPPING_GRPC_ADDR", "localhost:50057"),
		NotificationGRPCAddr: envOr("NOTIFICATION_GRPC_ADDR", "localhost:50058"),
		SearchGRPCAddr:       envOr("SEARCH_GRPC_ADDR", "localhost:50059"),
		ChatGRPCAddr:         envOr("CHAT_GRPC_ADDR", "localhost:50060"),
		AssistantGRPCAddr:    envOr("ASSISTANT_GRPC_ADDR", "localhost:50061"),

		MeilisearchURL: envOr("MEILISEARCH_URL", "http://localhost:7700"),
		MeiliMasterKey: envOr("MEILI_MASTER_KEY", "dev-only-meilisearch-master-key-change-me"),
		OTelPromURL:    envOr("OTEL_PROM_URL", "http://localhost:8889/metrics"),
		OTelHealthURL:  envOr("OTEL_HEALTH_URL", "http://localhost:13133/"),

		NATSURL:         envOr("NATS_URL", "nats://localhost:4222"),
		RedisAddr:       defaultRedisAddr(gatewayURL),
		RedisPassword:   strings.TrimSpace(os.Getenv("REDIS_PASSWORD")),
		UserDatabaseURL: defaultUserDatabaseURL(gatewayURL),
		MailpitURL:      envOr("MAILPIT_URL", "http://localhost:8025"),
		SiteRootDomain:  strings.ToLower(envOr("SITE_ROOT_DOMAIN", "nexusmart.localhost")),

		AdminEmail:             envOrMulti([]string{"ADMIN_EMAIL", "ADMIN_BOOTSTRAP_EMAIL"}, "admin@nexusmart.local"),
		AdminPassword:          envOrMulti([]string{"ADMIN_PASSWORD", "ADMIN_BOOTSTRAP_PASSWORD"}, "AdminDevPassword123!"),
		PaymentWebhookSecret:   envOr("PAYMENT_WEBHOOK_SECRET", "dev-only-payment-webhook-hmac-secret"),
		ShippingWebhookSecret:  envOr("SHIPPING_WEBHOOK_SECRET", "dev-only-shipping-webhook-hmac-secret"),
		ZaloPayAppID:           envInt("ZALOPAY_APP_ID", 1),
		ZaloPayKey1:            envOr("ZALOPAY_KEY1", "dev-only-zalopay-mock-key1"),
		ZaloPayKey2:            envOr("ZALOPAY_KEY2", "dev-only-zalopay-mock-key2"),
		LargeRefundApprovalVND: int64(envInt("PAYMENT_LARGE_REFUND_APPROVAL_VND", 5000000)),
		JWTSecret:              envOr("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		ServiceJWTSecret:       envOr("SERVICE_JWT_SECRET", "dev-only-nexusmart-service-hs256-secret-change-me"),

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

func defaultRedisAddr(gatewayURL string) string {
	if v, ok := os.LookupEnv("REDIS_ADDR"); ok {
		return strings.TrimSpace(v)
	}
	if strings.Contains(gatewayURL, "://gateway:") {
		return "redis:6379"
	}
	return "localhost:6379"
}

func defaultUserDatabaseURL(gatewayURL string) string {
	if v := strings.TrimSpace(os.Getenv("USER_DATABASE_URL")); v != "" {
		return v
	}
	host := "localhost:5432"
	if strings.Contains(gatewayURL, "://gateway:") {
		host = "postgres:5432"
	}
	user := envOr("POSTGRES_USER", "nexusmart")
	pass := envOr("POSTGRES_PASSWORD", "nexusmart_dev_password")
	return fmt.Sprintf("postgres://%s:%s@%s/user_db?sslmode=disable", url.QueryEscape(user), url.QueryEscape(pass), host)
}
