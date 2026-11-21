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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/env"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/product/internal/events"
	"github.com/nexusmart/core/services/product/internal/server"
	"github.com/nexusmart/core/services/product/internal/store"
	"github.com/nexusmart/core/services/product/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "product service exited with error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serviceName := env.EnvString("SERVICE_NAME", "product")
	grpcPort := env.EnvString("GRPC_PORT", "50052")
	httpPort := env.EnvString("HTTP_PORT", "8082")
	databaseURL := env.EnvString("DATABASE_URL", "")
	natsURL := strings.TrimSpace(env.EnvString("NATS_URL", ""))
	natsStream := env.EnvString("NATS_STREAM", events.DefaultStreamName)
	outboxPollInterval := env.EnvDuration("OUTBOX_POLL_INTERVAL", events.DefaultPollInterval)
	jwtSecret := env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me")
	otelEndpoint := env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	sweepInterval := env.EnvDuration("FLASH_SALE_SWEEP_INTERVAL", 2*time.Second)
	logLevel := parseLogLevel(env.EnvString("LOG_LEVEL", "info"))

	log := logger.New(serviceName, logger.WithLevel(logLevel))

	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	shutdownTelemetry, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  serviceName,
		OTLPEndpoint: otelEndpoint,
		Insecure:     true,
	})
	if err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(shutCtx)
	}()

	jwtMgr, err := auth.NewManager(jwtSecret, auth.DefaultAccessTTL)
	if err != nil {
		return fmt.Errorf("init jwt manager: %w", err)
	}

	svcMgr, err := auth.NewManager(os.Getenv("SERVICE_JWT_SECRET"), auth.ServiceTokenTTL)
	if err != nil {
		return fmt.Errorf("init service token manager (SERVICE_JWT_SECRET): %w", err)
	}

	pool, err := connectPostgresWithRetry(ctx, databaseURL, log)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if err := migrations.Apply(ctx, pool); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	log.Info("database migrations applied successfully")

	pgStore := store.NewPgStore(pool)
	red := metrics.NewRED(serviceName, nil)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(serviceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	productSrv := server.New(pgStore, log)
	productSrv.StartFlashSaleSweeper(ctx, sweepInterval)

	var (
		nc   *nats.Conn
		bgWG sync.WaitGroup
	)
	if natsURL != "" {
		var js nats.JetStreamContext
		nc, js, err = connectNATSWithRetry(ctx, natsURL, natsStream, log)
		if err != nil {
			return fmt.Errorf("connect nats: %w", err)
		}
		defer nc.Close()
		log.Info("connected to NATS JetStream", slog.String("stream", natsStream))

		triggerCh := make(chan struct{}, 1)
		productSrv.SetOutboxNotifier(func() {
			select {
			case triggerCh <- struct{}{}:
			default:
			}
		})

		outboxRelay := events.NewOutboxRelay(pgStore, js, log, outboxPollInterval, triggerCh)
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			outboxRelay.Run(ctx)
		}()
		defer func() {
			stop()
			bgWG.Wait()
		}()
	} else {
		log.Info("NATS_URL not set; outbox relay disabled")
	}

	productv1.RegisterProductServiceServer(grpcServer, productSrv)
	reflection.Register(grpcServer)

	grpcLis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		return fmt.Errorf("listen grpc :%s: %w", grpcPort, err)
	}

	opsHandler := metrics.NewOpsHandler(serviceName, func(rctx context.Context) error {
		pingCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := pgStore.Ping(pingCtx); err != nil {
			return fmt.Errorf("postgres not ready: %w", err)
		}
		if nc != nil && !nc.IsConnected() {
			return errors.New("nats not connected")
		}
		return nil
	}, nil)
	httpHandler := telemetry.HTTPMiddleware(serviceName)(red.HTTPMiddleware()(opsHandler))

	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("starting product gRPC server", slog.String("addr", ":"+grpcPort))
		if err := grpcServer.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	go func() {
		log.Info("starting product HTTP ops server", slog.String("addr", ":"+httpPort))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down product service")
	case serveErr := <-errCh:
		stop()
		return serveErr
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutCtx)
	grpcServer.GracefulStop()
	return nil
}

func connectNATSWithRetry(ctx context.Context, natsURL, streamName string, log *slog.Logger) (*nats.Conn, nats.JetStreamContext, error) {
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		nc, err := nats.Connect(
			natsURL,
			nats.Name("nexusmart-product"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		)
		if err == nil {
			js, jsErr := nc.JetStream()
			if jsErr == nil {
				ensureErr := events.EnsureStreams(js, streamName)
				if ensureErr == nil {
					return nc, js, nil
				}
				lastErr = ensureErr
			} else {
				lastErr = jsErr
			}
			nc.Close()
		} else {
			lastErr = err
		}

		log.Warn("waiting for nats jetstream", slog.Int("attempt", attempt), slog.String("error", lastErr.Error()))
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, nil, fmt.Errorf("nats unavailable after retries: %w", lastErr)
}

func connectPostgresWithRetry(ctx context.Context, databaseURL string, log *slog.Logger) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 25
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute

	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			pingErr := pool.Ping(pingCtx)
			cancel()
			if pingErr == nil {
				return pool, nil
			}
			pool.Close()
			lastErr = pingErr
		} else {
			lastErr = err
		}
		log.Warn("waiting for postgres", slog.Int("attempt", attempt), slog.String("error", lastErr.Error()))
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("postgres unavailable after retries: %w", lastErr)
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
