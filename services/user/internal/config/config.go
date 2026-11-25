package config

import (
	"strings"
	"time"

	"github.com/nexusmart/core/pkg/common/env"
)

// Config holds runtime configuration for the user service.
type Config struct {
	ServiceName            string
	GRPCPort               string
	HTTPPort               string
	DatabaseURL            string
	RedisAddr              string
	RedisPassword          string
	NATSURL                string
	NATSStream             string
	JWTSecret              string
	JWTAccessTTL           time.Duration
	RefreshTokenTTL        time.Duration
	OTPTTL                 time.Duration
	OTPMaxResends          int
	LoginMaxFailedAttempts int
	LoginLockoutDuration   time.Duration
	BankPayoutLockDuration time.Duration
	BankAccountKey         string
	PhoneOTPTTL            time.Duration
	PhoneOTPCooldown       time.Duration
	PhoneOTPMaxPerHour     int
	AdminBootstrapEmail    string
	AdminBootstrapPassword string
	ShippingGRPCAddr       string
	SiteRootDomain         string
	CFMode                 string
	CFAPIToken             string
	CFZoneID               string
	CFOriginHost           string
	CFCNAMETarget          string
	CFPollInterval         time.Duration
	OTLPEndpoint           string
	OTLPInsecure           bool
	LogLevel               string
}

// Load reads configuration from environment variables with dev/sandbox defaults.
func Load() Config {
	rootDomain := strings.Trim(strings.ToLower(strings.TrimSpace(env.EnvString("SITE_ROOT_DOMAIN", "nexusmart.localhost"))), ".")
	if rootDomain == "" {
		rootDomain = "nexusmart.localhost"
	}
	cfMode := strings.ToLower(strings.TrimSpace(env.EnvString("CF_MODE", "mock")))
	if cfMode == "" {
		cfMode = "mock"
	}
	cfPollInterval := env.EnvDuration("CF_POLL_INTERVAL", 5*time.Minute)
	if cfPollInterval <= 0 {
		cfPollInterval = 5 * time.Minute
	}
	return Config{
		ServiceName:            env.EnvString("SERVICE_NAME", "user"),
		GRPCPort:               env.EnvString("GRPC_PORT", "50051"),
		HTTPPort:               env.EnvString("HTTP_PORT", "8081"),
		DatabaseURL:            env.EnvString("DATABASE_URL", "postgres://nexusmart:nexusmart_dev_password@localhost:5432/user_db?sslmode=disable"),
		RedisAddr:              env.EnvString("REDIS_ADDR", "localhost:6379"),
		RedisPassword:          env.EnvString("REDIS_PASSWORD", ""),
		NATSURL:                env.EnvString("NATS_URL", "nats://localhost:4222"),
		NATSStream:             env.EnvString("NATS_STREAM", "NEXUSMART"),
		JWTSecret:              env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me"),
		JWTAccessTTL:           env.EnvDuration("JWT_ACCESS_TTL", 15*time.Minute),
		RefreshTokenTTL:        env.EnvDuration("REFRESH_TOKEN_TTL", 168*time.Hour),
		OTPTTL:                 env.EnvDuration("OTP_TTL", 10*time.Minute),
		OTPMaxResends:          env.EnvInt("OTP_MAX_RESENDS", 3),
		LoginMaxFailedAttempts: env.EnvInt("LOGIN_MAX_FAILED_ATTEMPTS", 5),
		LoginLockoutDuration:   env.EnvDuration("LOGIN_LOCKOUT_DURATION", 15*time.Minute),
		BankPayoutLockDuration: time.Duration(env.EnvInt("USER_BANK_PAYOUT_LOCK_HOURS", 24)) * time.Hour,
		BankAccountKey:         env.EnvString("BANK_ACCOUNT_KEY", ""),
		PhoneOTPTTL:            time.Duration(env.EnvInt("USER_PHONE_OTP_TTL_SECONDS", 300)) * time.Second,
		PhoneOTPCooldown:       time.Duration(env.EnvInt("USER_PHONE_OTP_COOLDOWN_SECONDS", 60)) * time.Second,
		PhoneOTPMaxPerHour:     env.EnvInt("USER_PHONE_OTP_MAX_PER_HOUR", 5),
		AdminBootstrapEmail:    env.EnvString("ADMIN_BOOTSTRAP_EMAIL", "admin@nexusmart.local"),
		AdminBootstrapPassword: env.EnvString("ADMIN_BOOTSTRAP_PASSWORD", "AdminDevPassword123!"),
		ShippingGRPCAddr:       env.EnvString("SHIPPING_GRPC_ADDR", "localhost:50057"),
		SiteRootDomain:         rootDomain,
		CFMode:                 cfMode,
		CFAPIToken:             env.EnvString("CF_API_TOKEN", ""),
		CFZoneID:               env.EnvString("CF_ZONE_ID", ""),
		CFOriginHost:           env.EnvString("CF_ORIGIN_HOST", "origin."+rootDomain),
		CFCNAMETarget:          env.EnvString("CF_CNAME_TARGET", "shops."+rootDomain),
		CFPollInterval:         cfPollInterval,
		OTLPEndpoint:           env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:           env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:               env.EnvString("LOG_LEVEL", "info"),
	}
}
