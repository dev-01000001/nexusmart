package config

import (
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
	AdminBootstrapEmail    string
	AdminBootstrapPassword string
	OTLPEndpoint           string
	OTLPInsecure           bool
	LogLevel               string
}

// Load reads configuration from environment variables with dev/sandbox defaults.
func Load() Config {
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
		AdminBootstrapEmail:    env.EnvString("ADMIN_BOOTSTRAP_EMAIL", "admin@nexusmart.local"),
		AdminBootstrapPassword: env.EnvString("ADMIN_BOOTSTRAP_PASSWORD", "AdminDevPassword123!"),
		OTLPEndpoint:           env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		OTLPInsecure:           env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true),
		LogLevel:               env.EnvString("LOG_LEVEL", "info"),
	}
}
