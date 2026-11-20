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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/promotion/internal/config"
	"github.com/nexusmart/core/services/promotion/internal/events"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func main() {
	cfg := config.Load()
	log := logger.New(cfg.ServiceName, logger.WithLevel(cfg.LogLevel))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	shutdownTracer, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  cfg.ServiceName,
		OTLPEndpoint: cfg.OTLPEndpoint,
		Insecure:     cfg.OTLPInsecure,
		SampleRatio:  1.0,
	})
	if err != nil {
		log.Error("failed to initialize telemetry", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTracer(shCtx)
	}()

	jwtMgr, err := auth.NewManager(cfg.JWTSecret, auth.DefaultAccessTTL)
	if err != nil {
		log.Error("failed to initialize jwt manager", slog.String("error", err.Error()))
		os.Exit(1)
	}

	svcMgr, err := auth.NewManager(os.Getenv("SERVICE_JWT_SECRET"), auth.ServiceTokenTTL)
	if err != nil {
		log.Error("failed to initialize service token manager (SERVICE_JWT_SECRET)", slog.String("error", err.Error()))
		os.Exit(1)
	}

	pool, err := connectPostgres(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Error("failed to connect postgres", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	pgStore := store.NewPostgresStore(pool)
	if err := pgStore.Migrate(ctx); err != nil {
		log.Error("failed to apply database migrations", slog.String("error", err.Error()))
		os.Exit(1)
	}
	log.Info("database migrations applied successfully")

	rdb, err := connectRedis(ctx, cfg.RedisAddr, cfg.RedisPass, log)
	if err != nil {
		log.Error("failed to connect redis", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() { _ = rdb.Close() }()

	luaEngine := lua.NewEngine(rdb)
	promoSvc := service.NewPromotionService(pgStore, luaEngine, log)
	if err := promoSvc.SyncRedisFromStore(ctx); err != nil {
		log.Error("failed to sync redis keys from store", slog.String("error", err.Error()))
		os.Exit(1)
	}

	nc, consumer, err := connectNATS(ctx, cfg.NATSURL, cfg.NATSStream, pgStore, log)
	if err != nil {
		log.Error("failed to start nats consumer", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		if consumer != nil {
			_ = consumer.Stop()
		}
		if nc != nil {
			nc.Close()
		}
	}()

	red := metrics.NewRED(cfg.ServiceName, prometheus.DefaultRegisterer)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	promotionv1.RegisterPromotionServiceServer(grpcServer, promoSvc)

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("nexusmart.promotion.v1.PromotionService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Error("failed to listen on grpc port", slog.String("port", cfg.GRPCPort), slog.String("error", err.Error()))
		os.Exit(1)
	}

	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := pgStore.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres not ready: %w", err)
		}
		if err := rdb.Ping(checkCtx).Err(); err != nil {
			return fmt.Errorf("redis not ready: %w", err)
		}
		if nc != nil && !nc.IsConnected() {
			return errors.New("nats not connected")
		}
		return nil
	}

	opsHandler := telemetry.HTTPMiddleware(cfg.ServiceName)(
		red.HTTPMiddleware()(
			metrics.NewOpsHandler(cfg.ServiceName, readyFn, prometheus.DefaultGatherer),
		),
	)
	httpServer := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           opsHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("starting promotion gRPC server", slog.String("port", cfg.GRPCPort))
		if serveErr := grpcServer.Serve(grpcLis); serveErr != nil {
			errCh <- fmt.Errorf("grpc serve: %w", serveErr)
		}
	}()

	go func() {
		log.Info("starting promotion HTTP ops server", slog.String("port", cfg.HTTPPort))
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", serveErr)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case serveErr := <-errCh:
		log.Error("server error", slog.String("error", serveErr.Error()))
	}

	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	grpcServer.GracefulStop()

	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shCtx)
	log.Info("promotion service stopped cleanly")
}

func connectPostgres(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = 30
	poolCfg.MinConns = 4

	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
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
		log.Warn("waiting for postgres...", slog.Int("attempt", attempt), slog.String("error", lastErr.Error()))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("postgres unavailable after retries: %w", lastErr)
}

func connectRedis(ctx context.Context, addr, password string, log *slog.Logger) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		PoolSize:     100,
		MinIdleConns: 10,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
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
		log.Warn("waiting for redis...", slog.Int("attempt", attempt), slog.String("error", err.Error()))
		select {
		case <-ctx.Done():
			_ = rdb.Close()
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 200 * time.Millisecond):
		}
	}
	_ = rdb.Close()
	return nil, fmt.Errorf("redis unavailable after retries: %w", lastErr)
}

func connectNATS(
	ctx context.Context,
	natsURL, streamName string,
	st store.Store,
	log *slog.Logger,
) (*nats.Conn, *events.OrderCompletedConsumer, error) {
	var nc *nats.Conn
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		conn, err := nats.Connect(natsURL,
			nats.Name("nexusmart-promotion"),
			nats.MaxReconnects(-1),
			nats.ReconnectWait(1*time.Second),
		)
		if err == nil {
			nc = conn
			break
		}
		lastErr = err
		log.Warn("waiting for nats...", slog.Int("attempt", attempt), slog.String("error", err.Error()))
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	if nc == nil {
		return nil, nil, fmt.Errorf("nats unavailable after retries: %w", lastErr)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("init jetstream context: %w", err)
	}

	consumer := events.NewOrderCompletedConsumer(st, js, streamName, log)
	if err := consumer.Start(ctx); err != nil {
		nc.Close()
		return nil, nil, err
	}
	return nc, consumer, nil
}
