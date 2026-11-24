package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/config"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/grpcserver"
	"github.com/nexusmart/core/services/order/internal/service"
	"github.com/nexusmart/core/services/order/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "order service exited with error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	log := logger.New(cfg.ServiceName, logger.WithLevel(cfg.LogLevel))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Initialize OpenTelemetry tracing (graceful local fallback when collector unavailable).
	otelShutdown, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  cfg.ServiceName,
		OTLPEndpoint: cfg.OTLPEndpoint,
		Insecure:     cfg.OTLPInsecure,
		SampleRatio:  1.0,
	})
	if err != nil {
		log.Warn("telemetry init failed, continuing without exporter", "error", err)
	} else {
		defer func() {
			sCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = otelShutdown(sCtx)
		}()
	}

	// Initialize PostgreSQL connection pool and auto-apply embedded migrations.
	pool, err := connectPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if err := db.ApplyMigrations(ctx, pool); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	log.Info("database migrations applied")

	// Connect to NATS JetStream and ensure NEXUSMART + NEXUSMART_DLQ streams exist.
	nc, js, err := connectNATS(ctx, cfg.NATSURL, cfg.NATSStream)
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer nc.Close()
	log.Info("connected to NATS JetStream", "stream", cfg.NATSStream)

	svcMgr, err := auth.NewManager(os.Getenv("SERVICE_JWT_SECRET"), auth.ServiceTokenTTL)
	if err != nil {
		return fmt.Errorf("init service token manager (SERVICE_JWT_SECRET): %w", err)
	}

	// Dial downstream gRPC services.
	downstream, err := clients.DialAll(cfg, svcMgr)
	if err != nil {
		return fmt.Errorf("dial downstream grpc clients: %w", err)
	}
	defer func() { _ = downstream.Close() }()

	jwtMgr, err := auth.NewManager(cfg.JWTSecret, auth.DefaultAccessTTL)
	if err != nil {
		return fmt.Errorf("init auth manager: %w", err)
	}

	pgStore := store.NewPostgresStore(pool)
	pgStore.SetLogger(log)
	orderSvc := service.NewOrderService(cfg, pgStore, downstream, log)

	// Start transactional outbox relay, idempotent NATS consumer, and payment expiry watchdog.
	outboxRelay := events.NewOutboxRelay(pgStore, js, log, cfg.OutboxPollInterval, orderSvc.OutboxTrigger())
	consumer := events.NewConsumer(js, js, pgStore, orderSvc, log, cfg.NATSStream)
	if err := consumer.Start(ctx); err != nil {
		return fmt.Errorf("start nats consumer: %w", err)
	}

	var bgWG sync.WaitGroup
	defer func() {
		stop()
		if err := consumer.Stop(); err != nil {
			log.Warn("error stopping nats consumer", "error", err)
		}
		bgWG.Wait()
	}()
	bgWG.Add(2)
	go func() {
		defer bgWG.Done()
		outboxRelay.Run(ctx)
	}()
	go func() {
		defer bgWG.Done()
		orderSvc.RunExpiryWatchdog(ctx)
	}()

	// Prometheus RED metrics and HTTP ops server (/healthz, /readyz, /metrics).
	reg := prometheus.NewRegistry()
	red := metrics.NewRED(cfg.ServiceName, reg)

	readyCheck := func(rCtx context.Context) error {
		if err := pgStore.Ping(rCtx); err != nil {
			return fmt.Errorf("postgres not ready: %w", err)
		}
		if !nc.IsConnected() {
			return errors.New("nats not connected")
		}
		return nil
	}

	opsHandler := telemetry.HTTPMiddleware(cfg.ServiceName)(
		red.HTTPMiddleware()(
			metrics.NewOpsHandler(cfg.ServiceName, readyCheck, reg),
		),
	)
	httpSrv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           opsHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// gRPC server with OTel, RED metrics, structured request logging, and JWT/metadata auth interceptors.
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			logger.GRPCUnaryServerInterceptor(log),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	orderv1.RegisterOrderServiceServer(grpcSrv, grpcserver.New(orderSvc))
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return fmt.Errorf("listen grpc :%s: %w", cfg.GRPCPort, err)
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("starting order HTTP ops server", "port", cfg.HTTPPort)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()
	go func() {
		log.Info("starting order gRPC server", "port", cfg.GRPCPort)
		if err := grpcSrv.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc server: %w", err)
		}
	}()

	var srvErr error
	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case srvErr = <-errCh:
		stop()
	}

	// Graceful shutdown.
	grpcSrv.GracefulStop()

	httpShutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(httpShutCtx); err != nil {
		log.Warn("error shutting down http server", "error", err)
	}

	if srvErr != nil {
		return srvErr
	}
	log.Info("order service stopped cleanly")
	return nil
}

func connectPostgres(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	poolCfg.MaxConns = 25
	poolCfg.MinConns = 2
	poolCfg.MaxConnLifetime = 30 * time.Minute

	var pool *pgxpool.Pool
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		pool, lastErr = pgxpool.NewWithConfig(ctx, poolCfg)
		if lastErr == nil {
			if pingErr := pool.Ping(ctx); pingErr == nil {
				return pool, nil
			} else {
				lastErr = pingErr
				pool.Close()
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, lastErr
}

func connectNATS(ctx context.Context, natsURL, streamName string) (*nats.Conn, nats.JetStreamContext, error) {
	var nc *nats.Conn
	var err error
	for attempt := 1; attempt <= 15; attempt++ {
		nc, err = nats.Connect(
			natsURL,
			nats.Name("nexusmart-order"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if err != nil {
		return nil, nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, nil, err
	}

	if err := events.EnsureStreams(js, streamName); err != nil {
		nc.Close()
		return nil, nil, err
	}
	return nc, js, nil
}
