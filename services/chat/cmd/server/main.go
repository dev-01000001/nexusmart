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
	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/chat/internal/config"
	"github.com/nexusmart/core/services/chat/internal/db"
	"github.com/nexusmart/core/services/chat/internal/grpcserver"
	"github.com/nexusmart/core/services/chat/internal/hub"
	chatmetrics "github.com/nexusmart/core/services/chat/internal/metrics"
	"github.com/nexusmart/core/services/chat/internal/service"
	"github.com/nexusmart/core/services/chat/internal/store"
	"github.com/nexusmart/core/services/chat/internal/ticket"
	"github.com/nexusmart/core/services/chat/internal/userclient"
	"github.com/nexusmart/core/services/chat/internal/validator"
	"github.com/nexusmart/core/services/chat/internal/ws"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	log := logger.New(cfg.ServiceName, logger.WithLevel(cfg.LogLevel)).With(slog.String("instance_id", cfg.InstanceID))

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
		log.Error("failed to init jwt manager", slog.String("error", err.Error()))
		return err
	}

	var svcMgr *auth.Manager
	if cfg.ServiceJWTSecret != "" {
		svcMgr, err = auth.NewManager(cfg.ServiceJWTSecret, auth.ServiceTokenTTL)
		if err != nil {
			log.Error("failed to init service jwt manager", slog.String("error", err.Error()))
			return err
		}
	}

	pool, err := connectPostgres(ctx, cfg.DatabaseURL, log)
	if err != nil {
		log.Error("failed to connect postgres", slog.String("error", err.Error()))
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Error("failed to migrate chat_db", slog.String("error", err.Error()))
		return err
	}
	log.Info("chat_db migrated cleanly")

	rdb, err := connectRedis(ctx, cfg.RedisAddr, cfg.RedisPassword, log)
	if err != nil {
		log.Error("failed to connect redis", slog.String("error", err.Error()))
		return err
	}
	defer func() { _ = rdb.Close() }()

	ticketMgr, err := ticket.NewManager(rdb, cfg.JWTSecret, cfg.WSTicketTTL)
	if err != nil {
		log.Error("failed to init ws ticket manager", slog.String("error", err.Error()))
		return err
	}

	red := metrics.NewRED(cfg.ServiceName, prometheus.DefaultRegisterer)
	cm := chatmetrics.New(prometheus.DefaultRegisterer)

	chatHub := hub.New(rdb, cfg.InstanceID, cm, log)
	go chatHub.Run(ctx)
	subWaitCtx, subWaitCancel := context.WithTimeout(ctx, 5*time.Second)
	if subErr := chatHub.WaitSubscribed(subWaitCtx); subErr != nil {
		log.Warn("redis pubsub initial subscribe still pending", slog.String("error", subErr.Error()))
	}
	subWaitCancel()

	var svcOpts []service.Option
	svcOpts = append(svcOpts,
		service.WithSiteRootDomain(cfg.SiteRootDomain),
		service.WithMetrics(cm),
	)
	if cfg.UserGRPCAddr != "" {
		uc, ucErr := userclient.New(cfg.UserGRPCAddr, cfg.ServiceName)
		if ucErr != nil {
			log.Warn("user gRPC client unavailable; shop status verification disabled", slog.String("error", ucErr.Error()))
		} else {
			svcOpts = append(svcOpts, service.WithShopChecker(uc))
			defer func() { _ = uc.Close() }()
		}
	}

	imgValidator := validator.NewImageValidator(validator.ImageValidatorConfig{
		Mode:         cfg.ImageValidateMode,
		MaxBytes:     cfg.ImageMaxBytes,
		AllowedHosts: cfg.ImageAllowedHosts,
	})
	pgStore := store.NewPGStore(pool)
	chatSvc := service.NewChatService(pgStore, chatHub, ticketMgr, imgValidator, log, svcOpts...)

	// gRPC server (:50060).
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	chatv1.RegisterChatServiceServer(grpcSrv, grpcserver.New(chatSvc))

	healthSrv := health.NewServer()
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("nexusmart.chat.v1.ChatService", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcSrv, healthSrv)
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Error("failed to listen gRPC", slog.String("port", cfg.GRPCPort), slog.String("error", err.Error()))
		return err
	}

	// HTTP + WebSocket server (:8090): /ws, /healthz, /readyz, /metrics.
	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := pool.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if err := rdb.Ping(checkCtx).Err(); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		return nil
	}
	opsHandler := telemetry.HTTPMiddleware(cfg.ServiceName)(
		red.HTTPMiddleware()(
			metrics.NewOpsHandler(cfg.ServiceName, readyFn, prometheus.DefaultGatherer),
		),
	)
	wsHandler := ws.NewHandler(chatSvc, chatHub, jwtMgr, ticketMgr, ws.Config{
		SiteRootDomain: cfg.SiteRootDomain,
		AllowedOrigins: cfg.WSAllowedOrigins,
		ShutdownCtx:    ctx,
	}, log)

	mux := http.NewServeMux()
	mux.Handle("/ws", wsHandler)
	mux.Handle("/healthz", opsHandler)
	mux.Handle("/readyz", opsHandler)
	mux.Handle("/metrics", opsHandler)

	// Intentionally omit ReadTimeout/WriteTimeout so long-lived WebSocket connections never drop.
	httpSrv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("chat gRPC server listening", slog.String("port", cfg.GRPCPort))
		if serveErr := grpcSrv.Serve(grpcLis); serveErr != nil {
			errCh <- fmt.Errorf("grpc serve: %w", serveErr)
		}
	}()
	go func() {
		log.Info("chat HTTP/WS server listening", slog.String("port", cfg.HTTPPort))
		if serveErr := httpSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", serveErr)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down chat service")
	case serveErr := <-errCh:
		log.Error("chat server error", slog.String("error", serveErr.Error()))
		stop()
	}

	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	grpcSrv.GracefulStop()
	log.Info("chat service stopped cleanly")
	return nil
}

func connectPostgres(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	var lastErr error
	for attempt := 1; attempt <= 20; attempt++ {
		ensureCtx, ensureCancel := context.WithTimeout(ctx, 5*time.Second)
		err := db.EnsureDatabase(ensureCtx, dsn)
		ensureCancel()
		if err == nil {
			poolCfg, parseErr := pgxpool.ParseConfig(dsn)
			if parseErr != nil {
				return nil, fmt.Errorf("parse database url: %w", parseErr)
			}
			poolCfg.MaxConns = 30
			poolCfg.MinConns = 4

			pool, newErr := pgxpool.NewWithConfig(ctx, poolCfg)
			if newErr == nil {
				pingCtx, pingCancel := context.WithTimeout(ctx, 3*time.Second)
				pingErr := pool.Ping(pingCtx)
				pingCancel()
				if pingErr == nil {
					return pool, nil
				}
				pool.Close()
				err = pingErr
			} else {
				err = newErr
			}
		}
		lastErr = err
		log.Warn("waiting for postgres...", slog.Int("attempt", attempt), slog.String("error", err.Error()))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
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
	for attempt := 1; attempt <= 20; attempt++ {
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
		case <-time.After(time.Second):
		}
	}
	_ = rdb.Close()
	return nil, fmt.Errorf("redis unavailable after retries: %w", lastErr)
}
