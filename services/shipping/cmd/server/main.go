package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/shipping/internal/cache"
	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/nexusmart/core/services/shipping/internal/config"
	"github.com/nexusmart/core/services/shipping/internal/events"
	"github.com/nexusmart/core/services/shipping/internal/grpcserver"
	"github.com/nexusmart/core/services/shipping/internal/poller"
	"github.com/nexusmart/core/services/shipping/internal/service"
	"github.com/nexusmart/core/services/shipping/internal/store"
	"github.com/nexusmart/core/services/shipping/internal/webhook"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	log := logger.New(cfg.ServiceName, logger.WithLevel(cfg.LogLevel))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTelemetry, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  cfg.ServiceName,
		OTLPEndpoint: cfg.OTLPEndpoint,
		Insecure:     cfg.OTLPInsecure,
		SampleRatio:  1.0,
	})
	if err != nil {
		log.Error("failed to init telemetry", slog.String("error", err.Error()))
		return err
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(shutCtx)
	}()

	pool, err := connectPostgres(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Error("failed to connect postgres", slog.String("error", err.Error()))
		return err
	}
	defer pool.Close()

	pgStore := store.NewPGStore(pool)
	if err := pgStore.MigrateAndSeed(ctx); err != nil {
		log.Error("failed to migrate and seed shipping_db", slog.String("error", err.Error()))
		return err
	}
	log.Info("shipping_db migrated and seeded (34 provinces, wards, 63-province legacy mappings)")

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	defer func() { _ = rdb.Close() }()

	nc, js, err := connectNATS(ctx, cfg.NATSURL, cfg.NATSStream, cfg.NATSDLQStream, log)
	if err != nil {
		log.Error("failed to connect NATS JetStream", slog.String("error", err.Error()))
		return err
	}
	defer nc.Close()

	// Build resilient carrier adapters (timeout + exponential retry/backoff + per-carrier circuit breaker).
	resCfg := carrier.ResilienceConfig{
		AttemptTimeout:  cfg.CarrierTimeout,
		MaxAttempts:     cfg.CarrierRetries,
		BaseBackoff:     cfg.CarrierBackoff,
		MaxBackoff:      2 * time.Second,
		CBMaxFailures:   cfg.CBMaxFailures,
		CBOpenTimeout:   cfg.CBOpenTimeout,
		CBHalfOpenLimit: 2,
		Logger:          log,
	}
	var enabledCarriers []carrier.Carrier
	if cfg.GHNEnabled {
		ghn := carrier.NewGHNAdapter(carrier.GHNConfig{
			Mode:    cfg.GHNMode,
			BaseURL: cfg.GHNBaseURL,
			Token:   cfg.GHNToken,
			ShopID:  cfg.GHNShopID,
		})
		enabledCarriers = append(enabledCarriers, carrier.NewResilientCarrier(ghn, resCfg))
	}
	if cfg.MockCarrierOn {
		mockExp := carrier.NewMockExpressAdapter()
		enabledCarriers = append(enabledCarriers, carrier.NewResilientCarrier(mockExp, resCfg))
	}
	agg := carrier.NewAggregator(log, enabledCarriers...)
	quoteCache := cache.NewRedisQuoteCache(rdb, cfg.QuoteCacheTTL, log)
	outboxRelay := events.NewOutboxRelay(pgStore, js, cfg.OutboxInterval, log)
	svc := service.NewShippingService(pgStore, agg, quoteCache, outboxRelay, log)

	// Start background workers: outbox relay, order.cancelled consumer, fallback status poller.
	go outboxRelay.Run(ctx)

	consumer := events.NewConsumer(js, pgStore, svc, cfg.NATSStream, cfg.ConsumerMaxTries, log)
	if err := consumer.Start(ctx); err != nil {
		log.Error("failed to start NATS consumer", slog.String("error", err.Error()))
		return err
	}
	defer func() { _ = consumer.Close() }()

	statusPoller := poller.New(svc, cfg.PollInterval, log)
	go statusPoller.Run(ctx)

	// Metrics & Auth.
	red := metrics.NewRED(cfg.ServiceName, prometheus.DefaultRegisterer)
	jwtMgr, err := auth.NewManager(cfg.JWTSecret, auth.DefaultAccessTTL)
	if err != nil {
		log.Error("failed to init JWT manager", slog.String("error", err.Error()))
		return err
	}

	svcMgr, err := auth.NewManager(os.Getenv("SERVICE_JWT_SECRET"), auth.ServiceTokenTTL)
	if err != nil {
		log.Error("failed to init service token manager (SERVICE_JWT_SECRET)", slog.String("error", err.Error()))
		return err
	}

	// Start gRPC server on :50057.
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	shippingv1.RegisterShippingServiceServer(grpcSrv, grpcserver.New(svc))
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Error("failed to listen gRPC", slog.String("port", cfg.GRPCPort), slog.String("error", err.Error()))
		return err
	}
	go func() {
		log.Info("shipping gRPC server listening", slog.String("port", cfg.GRPCPort))
		if serveErr := grpcSrv.Serve(grpcLis); serveErr != nil {
			log.Error("shipping gRPC serve error", slog.String("error", serveErr.Error()))
		}
	}()

	// Start HTTP ops + webhook server on :8087.
	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := pool.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if err := rdb.Ping(checkCtx).Err(); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		if !nc.IsConnected() {
			return errors.New("nats: disconnected")
		}
		return nil
	}
	opsHandler := metrics.NewOpsHandler(cfg.ServiceName, readyFn, prometheus.DefaultGatherer)
	whHandler := webhook.NewHandler(cfg.WebhookSecret, svc, log)

	router := chi.NewRouter()
	router.Use(telemetry.HTTPMiddleware(cfg.ServiceName))
	router.Use(red.HTTPMiddleware())
	router.Handle("/healthz", opsHandler)
	router.Handle("/readyz", opsHandler)
	router.Handle("/metrics", opsHandler)
	router.Post("/webhooks/shipping/{carrier}", whHandler.ServeHTTP)

	httpSrv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Info("shipping HTTP server listening", slog.String("port", cfg.HTTPPort))
		if serveErr := httpSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("shipping HTTP serve error", slog.String("error", serveErr.Error()))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down shipping service")

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	grpcSrv.GracefulStop()
	return nil
}

func connectPostgres(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	var lastErr error
	for i := 0; i < 20; i++ {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err = pool.Ping(pingCtx)
			cancel()
			if err == nil {
				return pool, nil
			}
			pool.Close()
		}
		lastErr = err
		log.Warn("waiting for postgres...", slog.Int("attempt", i+1), slog.String("error", err.Error()))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, lastErr
}

func connectNATS(ctx context.Context, url, primaryStream, dlqStream string, log *slog.Logger) (*nats.Conn, nats.JetStreamContext, error) {
	var lastErr error
	for i := 0; i < 20; i++ {
		nc, err := nats.Connect(url,
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		)
		if err == nil {
			js, jsErr := nc.JetStream()
			if jsErr == nil {
				if streamErr := events.EnsureStreams(js, primaryStream, dlqStream); streamErr == nil {
					return nc, js, nil
				} else {
					lastErr = streamErr
				}
			} else {
				lastErr = jsErr
			}
			nc.Close()
		} else {
			lastErr = err
		}
		log.Warn("waiting for NATS JetStream...", slog.Int("attempt", i+1), slog.String("error", lastErr.Error()))
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, nil, lastErr
}
