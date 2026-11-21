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
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/env"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/search/internal/consumer"
	"github.com/nexusmart/core/services/search/internal/embed"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/server"
	"github.com/nexusmart/core/services/search/internal/store"
	"github.com/nexusmart/core/services/search/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "search service exited with error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serviceName := env.EnvString("SERVICE_NAME", "search")
	grpcPort := env.EnvString("GRPC_PORT", "50059")
	httpPort := env.EnvString("HTTP_PORT", "8089")
	databaseURL := env.EnvString("DATABASE_URL", "")
	meiliURL := env.EnvString("MEILI_URL", "http://meilisearch:7700")
	meiliKey := env.EnvString("MEILI_MASTER_KEY", "")
	meiliIndex := env.EnvString("MEILI_INDEX", meili.DefaultIndexName)
	natsURL := env.EnvString("NATS_URL", "")
	natsStream := env.EnvString("NATS_STREAM", consumer.DefaultPrimaryStream)
	natsDLQStream := env.EnvString("NATS_DLQ_STREAM", consumer.DefaultDLQStream)
	productGRPCAddr := env.EnvString("PRODUCT_GRPC_ADDR", "product:50052")
	startupSync := env.EnvBool("SEARCH_STARTUP_SYNC", true)
	jwtSecret := env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me")
	serviceJWTSecret := env.EnvString("SERVICE_JWT_SECRET", "")
	otelEndpoint := env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	logLevel := parseLogLevel(env.EnvString("LOG_LEVEL", "info"))

	log := logger.New(serviceName, logger.WithLevel(logLevel))

	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	shutdownTelemetry, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  serviceName,
		OTLPEndpoint: otelEndpoint,
		Insecure:     true,
		SampleRatio:  1.0,
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

	var svcMgr *auth.Manager
	if serviceJWTSecret != "" {
		svcMgr, err = auth.NewManager(serviceJWTSecret, auth.ServiceTokenTTL)
		if err != nil {
			return fmt.Errorf("init service jwt manager: %w", err)
		}
	}

	embedder, err := embed.NewFromEnv(log)
	if err != nil {
		return fmt.Errorf("init embedder: %w", err)
	}

	pool, err := connectPostgresWithRetry(ctx, databaseURL, log)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if err := migrations.Apply(ctx, pool); err != nil {
		return fmt.Errorf("apply search migrations: %w", err)
	}
	log.Info("search database migrations applied")

	pgStore := store.NewPgStore(pool)

	meiliClient := meili.NewClient(meili.Config{
		BaseURL: meiliURL,
		APIKey:  meiliKey,
		Index:   meiliIndex,
		Logger:  log,
	})
	if err := ensureMeiliWithRetry(ctx, meiliClient, log); err != nil {
		return fmt.Errorf("init meilisearch index: %w", err)
	}
	log.Info("meilisearch index ready", slog.String("index", meiliIndex))

	var (
		prodConn *grpc.ClientConn
		fetcher  indexer.ProductFetcher
	)
	if productGRPCAddr != "" {
		prodConn, err = grpc.NewClient(
			productGRPCAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(serviceName)),
		)
		if err != nil {
			return fmt.Errorf("dial product service %s: %w", productGRPCAddr, err)
		}
		defer func() { _ = prodConn.Close() }()
		fetcher = indexer.NewGRPCProductFetcher(productv1.NewProductServiceClient(prodConn), nil, log)
	}

	idx := indexer.New(pgStore, meiliClient, embedder, fetcher, log)

	var (
		nc           *nats.Conn
		natsConsumer *consumer.Consumer
	)
	if natsURL != "" {
		var js nats.JetStreamContext
		nc, js, err = connectNATSWithRetry(ctx, natsURL, natsStream, natsDLQStream, log)
		if err != nil {
			return fmt.Errorf("connect NATS JetStream: %w", err)
		}
		defer nc.Close()

		natsConsumer = consumer.NewConsumer(js, idx, pgStore, natsStream, 5, log)
		if err := natsConsumer.Start(ctx); err != nil {
			return fmt.Errorf("start search NATS consumer: %w", err)
		}
		defer func() { _ = natsConsumer.Close() }()
		log.Info("search NATS JetStream consumer started", slog.String("stream", natsStream))
	}

	if startupSync && fetcher != nil {
		go runStartupSync(ctx, idx, log)
	}

	red := metrics.NewRED(serviceName, prometheus.DefaultRegisterer)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(serviceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	searchSrv := server.New(meiliClient, pgStore, embedder, log, server.WithCategoryLabels(idx.CategoryLabels))
	searchv1.RegisterSearchServiceServer(grpcServer, searchSrv)
	reflection.Register(grpcServer)

	grpcLis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		return fmt.Errorf("listen grpc :%s: %w", grpcPort, err)
	}

	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := pgStore.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if err := meiliClient.Ping(checkCtx); err != nil {
			return fmt.Errorf("meilisearch: %w", err)
		}
		if nc != nil && !nc.IsConnected() {
			return errors.New("nats: disconnected")
		}
		return nil
	}
	opsHandler := telemetry.HTTPMiddleware(serviceName)(
		red.HTTPMiddleware()(
			metrics.NewOpsHandler(serviceName, readyFn, prometheus.DefaultGatherer),
		),
	)

	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           opsHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("starting search gRPC server", slog.String("addr", ":"+grpcPort))
		if serveErr := grpcServer.Serve(grpcLis); serveErr != nil {
			errCh <- fmt.Errorf("grpc serve: %w", serveErr)
		}
	}()
	go func() {
		log.Info("starting search HTTP ops server", slog.String("addr", ":"+httpPort))
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", serveErr)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down search service")
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

func runStartupSync(ctx context.Context, idx *indexer.Indexer, log *slog.Logger) {
	for attempt := 1; attempt <= 10; attempt++ {
		if ctx.Err() != nil {
			return
		}
		syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, err := idx.SyncAllFromProduct(syncCtx)
		cancel()
		if err == nil {
			return
		}
		log.Warn("startup catalog sync attempt failed; will retry",
			slog.Int("attempt", attempt),
			slog.String("error", err.Error()),
		)
		timer := time.NewTimer(time.Duration(attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
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
	for attempt := 1; attempt <= 20; attempt++ {
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

func ensureMeiliWithRetry(ctx context.Context, client *meili.Client, log *slog.Logger) error {
	var lastErr error
	for attempt := 1; attempt <= 20; attempt++ {
		initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := client.EnsureIndex(initCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		log.Warn("waiting for meilisearch", slog.Int("attempt", attempt), slog.String("error", lastErr.Error()))
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("meilisearch unavailable after retries: %w", lastErr)
}

func connectNATSWithRetry(ctx context.Context, url, primaryStream, dlqStream string, log *slog.Logger) (*nats.Conn, nats.JetStreamContext, error) {
	var lastErr error
	for attempt := 1; attempt <= 20; attempt++ {
		nc, err := nats.Connect(url,
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		)
		if err == nil {
			js, jsErr := nc.JetStream()
			if jsErr == nil {
				if streamErr := consumer.EnsureStreams(js, primaryStream, dlqStream); streamErr == nil {
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
		log.Warn("waiting for NATS JetStream", slog.Int("attempt", attempt), slog.String("error", lastErr.Error()))
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
