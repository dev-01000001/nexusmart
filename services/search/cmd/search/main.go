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
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	searchcfg "github.com/nexusmart/core/services/search/config"
	"github.com/nexusmart/core/services/search/internal/config"
	"github.com/nexusmart/core/services/search/internal/embedder"
	"github.com/nexusmart/core/services/search/internal/events"
	"github.com/nexusmart/core/services/search/internal/grpcserver"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/service"
	"github.com/nexusmart/core/services/search/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "search service exited with error: %v\n", err)
		os.Exit(1)
	}
}

// buildReadyCheck pings hard dependencies ONLY (search-postgres, Redis, NATS).
// It intentionally does NOT fail /readyz when Meilisearch is down so fallback stays reachable (G13).
func buildReadyCheck(pgPing, redisPing func(context.Context) error, natsConnected func() bool) func(context.Context) error {
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		if pgPing != nil {
			if err := pgPing(checkCtx); err != nil {
				return fmt.Errorf("search-postgres not ready: %w", err)
			}
		}
		if redisPing != nil {
			if err := redisPing(checkCtx); err != nil {
				return fmt.Errorf("redis not ready: %w", err)
			}
		}
		if natsConnected != nil && !natsConnected() {
			return errors.New("nats not connected")
		}
		return nil
	}
}

func run() error {
	cfg := config.Load()
	log := logger.New(cfg.ServiceName, logger.WithLevel(cfg.LogLevel))

	if err := cfg.Validate(); err != nil {
		log.Error("invalid search configuration", slog.String("error", err.Error()))
		return err
	}

	synonyms, err := searchcfg.LoadSynonyms()
	if err != nil {
		log.Error("failed to load synonyms.json", slog.String("error", err.Error()))
		return err
	}

	emb, err := embedder.New(embedder.Config{
		Mode:     cfg.EmbeddingMode,
		Model:    cfg.EmbeddingModel,
		Dim:      cfg.EmbeddingDim,
		APIKey:   cfg.EmbeddingAPIKey,
		BaseURL:  cfg.EmbeddingBaseURL,
		Timeout:  time.Duration(cfg.VectorTimeoutMS) * time.Millisecond,
		Synonyms: synonyms,
		Logger:   log,
	})
	if err != nil {
		log.Error("failed to initialize embedder", slog.String("error", err.Error()))
		return err
	}

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

	jwtMgr, err := auth.NewManager(cfg.JWTSecret, auth.DefaultAccessTTL)
	if err != nil {
		log.Error("failed to init JWT manager", slog.String("error", err.Error()))
		return err
	}
	svcMgr, err := auth.NewManager(cfg.ServiceJWTSecret, auth.ServiceTokenTTL)
	if err != nil {
		log.Error("failed to init service token manager", slog.String("error", err.Error()))
		return err
	}

	pool, err := connectPostgresWithRetry(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Error("failed to connect search-postgres", slog.String("error", err.Error()))
		return err
	}
	defer pool.Close()
	log.Info("connected to search-postgres and applied migrations")

	rdb, err := connectRedisWithRetry(ctx, cfg.RedisAddr, cfg.RedisPassword, log)
	if err != nil {
		log.Error("failed to connect redis", slog.String("error", err.Error()))
		return err
	}
	defer func() { _ = rdb.Close() }()
	log.Info("connected to redis")

	nc, js, err := connectNATSWithRetry(ctx, cfg.NATSURL, cfg.NATSStream, cfg.NATSDLQStream, log)
	if err != nil {
		log.Error("failed to connect nats", slog.String("error", err.Error()))
		return err
	}
	defer nc.Close()
	log.Info("connected to NATS JetStream", slog.String("stream", cfg.NATSStream))

	meiliClient := meili.NewClient(meili.Options{
		URL:      cfg.MeilisearchURL,
		APIKey:   cfg.MeiliMasterKey,
		Timeout:  time.Duration(cfg.MeiliTimeoutMS) * time.Millisecond,
		Synonyms: synonyms,
		Logger:   log,
	})
	initMeiliCtx, cancelMeili := context.WithTimeout(ctx, 2*time.Second)
	if mErr := meiliClient.EnsureConfigured(initMeiliCtx); mErr != nil {
		// G13: Do NOT block startup if Meilisearch is down; auto-reconnect loop configures index once online.
		log.Warn("meilisearch unavailable at startup; service will stay up and auto-configure on reconnect",
			slog.String("error", mErr.Error()),
		)
	}
	cancelMeili()

	var productClient indexer.ProductIndexClient
	if cfg.ProductGRPCAddr != "" {
		prodConn, dialErr := grpc.NewClient(
			cfg.ProductGRPCAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(cfg.ServiceName)),
		)
		if dialErr != nil {
			log.Warn("failed to init lazy product grpc client", slog.String("error", dialErr.Error()))
		} else {
			defer func() { _ = prodConn.Close() }()
			productClient = productv1.NewProductServiceClient(prodConn)
		}
	}

	var userClient indexer.UserIndexClient
	if cfg.UserGRPCAddr != "" {
		userConn, dialErr := grpc.NewClient(
			cfg.UserGRPCAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(cfg.ServiceName)),
		)
		if dialErr != nil {
			log.Warn("failed to init lazy user grpc client", slog.String("error", dialErr.Error()))
		} else {
			defer func() { _ = userConn.Close() }()
			userClient = userv1.NewUserServiceClient(userConn)
		}
	}

	searchStore := store.New(pool)
	trendingStore := store.NewTrendingStore(rdb)
	idx := indexer.New(ctx, searchStore, meiliClient, emb, productClient, userClient, svcMgr, log)
	consumer := events.NewConsumer(js, js, searchStore, idx, log, cfg.NATSStream)
	if err := consumer.Start(ctx); err != nil {
		log.Error("failed to start NATS consumer", slog.String("error", err.Error()))
		return err
	}

	var bgWG sync.WaitGroup
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		meiliClient.StartReconnectLoop(ctx)
	}()

	searchSvc := service.New(searchStore, trendingStore, meiliClient, emb, idx, service.Options{
		RRFK:                cfg.RRFK,
		MeiliTimeout:        time.Duration(cfg.MeiliTimeoutMS) * time.Millisecond,
		VectorTimeout:       time.Duration(cfg.VectorTimeoutMS) * time.Millisecond,
		VectorMinSimilarity: cfg.VectorMinSimilarity,
		Synonyms:            synonyms,
		Logger:              log,
	})

	red := metrics.NewRED(cfg.ServiceName, prometheus.DefaultRegisterer)
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			logger.GRPCUnaryServerInterceptor(log),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	searchv1.RegisterSearchServiceServer(grpcSrv, grpcserver.New(searchSvc))
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Error("failed to listen gRPC", slog.String("port", cfg.GRPCPort), slog.String("error", err.Error()))
		return err
	}

	readyFn := buildReadyCheck(
		searchStore.Ping,
		func(rctx context.Context) error { return rdb.Ping(rctx).Err() },
		nc.IsConnected,
	)
	opsHandler := telemetry.HTTPMiddleware(cfg.ServiceName)(
		red.HTTPMiddleware()(
			metrics.NewOpsHandler(cfg.ServiceName, readyFn, prometheus.DefaultGatherer),
		),
	)

	httpSrv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           opsHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("search gRPC server listening", slog.String("port", cfg.GRPCPort))
		if serveErr := grpcSrv.Serve(grpcLis); serveErr != nil {
			errCh <- fmt.Errorf("grpc serve: %w", serveErr)
		}
	}()
	go func() {
		log.Info("search HTTP ops server listening", slog.String("port", cfg.HTTPPort))
		if serveErr := httpSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", serveErr)
		}
	}()

	var exitErr error
	select {
	case <-ctx.Done():
		log.Info("shutting down search service")
	case serveErr := <-errCh:
		exitErr = serveErr
		stop()
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	grpcSrv.GracefulStop()
	_ = consumer.Stop()
	idx.Stop()
	bgWG.Wait()
	return exitErr
}

func connectPostgresWithRetry(ctx context.Context, databaseURL string, log *slog.Logger) (*pgxpool.Pool, error) {
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		pool, err := store.OpenPool(ctx, databaseURL)
		if err == nil {
			return pool, nil
		}
		lastErr = err
		log.Warn("waiting for search-postgres", slog.Int("attempt", attempt), slog.String("error", err.Error()))
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("connect search-postgres after retries: %w", lastErr)
}

func connectRedisWithRetry(ctx context.Context, addr, password string, log *slog.Logger) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := rdb.Ping(pingCtx).Err()
		cancel()
		if err == nil {
			return rdb, nil
		}
		lastErr = err
		log.Warn("waiting for redis", slog.Int("attempt", attempt), slog.String("error", err.Error()))
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = rdb.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	_ = rdb.Close()
	return nil, fmt.Errorf("connect redis after retries: %w", lastErr)
}

func connectNATSWithRetry(ctx context.Context, natsURL, streamName, dlqStream string, log *slog.Logger) (*nats.Conn, nats.JetStreamContext, error) {
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		nc, err := nats.Connect(natsURL,
			nats.Name("search-service"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(time.Second),
		)
		if err == nil {
			js, jsErr := nc.JetStream()
			if jsErr == nil {
				if ensureErr := jetstream.EnsureStreams(js, streamName, dlqStream); ensureErr == nil {
					return nc, js, nil
				} else {
					lastErr = ensureErr
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
	return nil, nil, fmt.Errorf("connect NATS after retries: %w", lastErr)
}
