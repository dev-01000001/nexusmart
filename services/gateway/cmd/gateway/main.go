package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/storage"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/config"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"github.com/nexusmart/core/services/gateway/internal/proxy"
	"github.com/nexusmart/core/services/gateway/internal/router"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg := config.Load()
	log := logger.New(cfg.ServiceName, logger.WithLevel(parseLogLevel(cfg.LogLevel)))
	if err := run(cfg, log); err != nil {
		log.Error("gateway exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  cfg.ServiceName,
		OTLPEndpoint: cfg.OTLPEndpoint,
		Insecure:     cfg.OTLPInsecure,
	})
	if err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(shutCtx)
	}()

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}

	authMgr, err := auth.NewManager(cfg.JWTSecret, auth.DefaultAccessTTL)
	if err != nil {
		return fmt.Errorf("init auth manager: %w", err)
	}

	grpcClients, err := clients.New(cfg)
	if err != nil {
		return fmt.Errorf("init grpc clients: %w", err)
	}
	defer grpcClients.Close()

	paymentWebhookProxy, err := proxy.NewWebhookProxy(cfg.PaymentHTTPURL, log)
	if err != nil {
		return fmt.Errorf("init payment webhook proxy: %w", err)
	}
	shippingWebhookProxy, err := proxy.NewWebhookProxy(cfg.ShippingHTTPURL, log)
	if err != nil {
		return fmt.Errorf("init shipping webhook proxy: %w", err)
	}
	shippingLabelProxy, err := proxy.NewShippingLabelProxy(cfg.ShippingHTTPURL, log)
	if err != nil {
		return fmt.Errorf("init shipping label proxy: %w", err)
	}
	paymentSSEProxy, err := proxy.NewSSEProxy(cfg.PaymentHTTPURL, log)
	if err != nil {
		return fmt.Errorf("init payment SSE proxy: %w", err)
	}

	var mediaStore storage.Store
	if cfg.S3.Enabled() {
		s3Client, s3Err := storage.New(cfg.S3)
		if s3Err != nil {
			return fmt.Errorf("init media storage: %w", s3Err)
		}
		if ensureErr := ensureMediaBucketWithRetry(ctx, s3Client, log); ensureErr != nil {
			return fmt.Errorf("ensure media bucket: %w", ensureErr)
		}
		mediaStore = s3Client
		log.Info("media object storage ready", slog.String("bucket", s3Client.Bucket()))
	}

	reg := newMetricsRegistry()
	red := metrics.NewRED(cfg.ServiceName, reg)
	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := rdb.Ping(checkCtx).Err(); err != nil {
			return fmt.Errorf("redis not ready: %w", err)
		}
		return nil
	}
	opsHandler := metrics.NewOpsHandler(cfg.ServiceName, readyFn, reg)

	trustedProxies, err := middleware.NewTrustedProxiesValidated(cfg.TrustedProxies, log)
	if err != nil {
		return fmt.Errorf("parse trusted proxies: %w", err)
	}

	rateLimiter := middleware.NewRedisRateLimiter(rdb)
	revocationChecker := middleware.NewRedisRevocationChecker(rdb)

	handler := router.New(router.Dependencies{
		ServiceName:                      cfg.ServiceName,
		Clients:                          grpcClients,
		AuthManager:                      authMgr,
		RevocationChecker:                revocationChecker,
		RateLimiter:                      rateLimiter,
		RateLimitIPPerMin:                cfg.RateLimitIPPerMin,
		RateLimitBFFPerMin:               cfg.RateLimitBFFPerMin,
		RateLimitUserPerMin:              cfg.RateLimitUserPerMin,
		RateLimitFlashPerMin:             cfg.RateLimitFlashPerMin,
		RateLimitAuthPerMin:              cfg.RateLimitAuthPerMin,
		RateLimitBankOTPPerMin:           cfg.RateLimitBankOTPPerMin,
		RateLimitPhoneOTPPerMin:          cfg.RateLimitPhoneOTPPerMin,
		RateLimitShippingFeePerMin:       cfg.RateLimitShippingFeePerMin,
		RateLimitOrderLookupPerMin:       cfg.RateLimitOrderLookupPerMin,
		RateLimitOrderLookupCodePer10Min: cfg.RateLimitOrderLookupCodePer10Min,
		RateLimitWebhookPerMin:           cfg.RateLimitWebhookPerMin,
		ProductImportMaxRows:             cfg.ProductImportMaxRows,
		ProductImportMaxBytes:            cfg.ProductImportMaxBytes,
		SearchFallbackTimeout:            cfg.SearchFallbackTimeout,
		TrustedProxies:                   trustedProxies,
		RED:                              red,
		OpsHandler:                       opsHandler,
		PaymentWebhookProxy:              paymentWebhookProxy,
		ShippingWebhookProxy:             shippingWebhookProxy,
		ShippingLabelProxy:               shippingLabelProxy,
		PaymentSSEProxy:                  paymentSSEProxy,
		MediaStore:                       mediaStore,
		Logger:                           log,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("starting gateway HTTP server", slog.String("port", cfg.HTTPPort))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down gateway")
	case err := <-errCh:
		return err
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutCtx)
}

func newMetricsRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
	)
	return reg
}

func parseLogLevel(lvl string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(lvl)) {
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

func ensureMediaBucketWithRetry(ctx context.Context, store storage.Store, log *slog.Logger) error {
	const maxAttempts = 5
	backoff := 250 * time.Millisecond
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := store.EnsureBucket(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == maxAttempts || ctx.Err() != nil {
			break
		}
		log.Warn("retrying media bucket ensure", slog.Int("attempt", attempt), slog.String("error", err.Error()))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return lastErr
}
