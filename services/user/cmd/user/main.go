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
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/user/internal/cloudflare"
	"github.com/nexusmart/core/services/user/internal/config"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/events"
	"github.com/nexusmart/core/services/user/internal/password"
	"github.com/nexusmart/core/services/user/internal/security"
	"github.com/nexusmart/core/services/user/internal/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "user service exited with error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()
	log := logger.New(cfg.ServiceName, logger.WithLevel(parseLogLevel(cfg.LogLevel)))

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

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	if err := db.RunMigrations(ctx, pool); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	defer func() { _ = rdb.Close() }()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}

	nc, err := nats.Connect(cfg.NATSURL, nats.Name(cfg.ServiceName), nats.MaxReconnects(-1))
	if err != nil {
		return fmt.Errorf("connect nats: %w", err)
	}
	defer nc.Close()

	pub, err := events.NewNATSPublisher(nc, cfg.NATSStream)
	if err != nil {
		return fmt.Errorf("init nats publisher: %w", err)
	}

	authMgr, err := auth.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL)
	if err != nil {
		return fmt.Errorf("init auth manager: %w", err)
	}

	svcMgr, err := auth.NewManager(os.Getenv("SERVICE_JWT_SECRET"), auth.ServiceTokenTTL)
	if err != nil {
		return fmt.Errorf("init service token manager (SERVICE_JWT_SECRET): %w", err)
	}

	store := db.NewSQLStore(pool)
	secStore := security.NewRedisStore(rdb)
	hasher := password.NewDefaultHasher()

	if err := service.BootstrapAdmin(ctx, store, hasher, log, cfg.AdminBootstrapEmail, cfg.AdminBootstrapPassword); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if err := service.BootstrapSiteTemplates(ctx, store, log); err != nil {
		return fmt.Errorf("bootstrap site templates: %w", err)
	}

	var cfClient cloudflare.Client
	switch cfg.CFMode {
	case "mock":
		cfClient = cloudflare.NewMock(cfg.CFCNAMETarget)
	case "live":
		cfClient, err = cloudflare.NewLive(cloudflare.LiveConfig{
			APIToken:    cfg.CFAPIToken,
			ZoneID:      cfg.CFZoneID,
			OriginHost:  cfg.CFOriginHost,
			CNAMETarget: cfg.CFCNAMETarget,
		})
		if err != nil {
			return fmt.Errorf("init cloudflare live client: %w", err)
		}
	default:
		return fmt.Errorf("unsupported CF_MODE %q (expected mock or live)", cfg.CFMode)
	}

	reg := prometheus.NewRegistry()
	red := metrics.NewRED(cfg.ServiceName, reg)

	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(authMgr, svcMgr),
		),
	)
	userSrv := service.NewServer(store, secStore, hasher, authMgr, pub, log, service.Options{
		AccessTTL:              cfg.JWTAccessTTL,
		RefreshTTL:             cfg.RefreshTokenTTL,
		OTPTTL:                 cfg.OTPTTL,
		OTPMaxResends:          cfg.OTPMaxResends,
		LoginMaxFailedAttempts: cfg.LoginMaxFailedAttempts,
		LoginLockoutDuration:   cfg.LoginLockoutDuration,
		SiteRootDomain:         cfg.SiteRootDomain,
		Cloudflare:             cfClient,
		CFMode:                 cfg.CFMode,
		CFPollInterval:         cfg.CFPollInterval,
	})
	userv1.RegisterUserServiceServer(grpcSrv, userSrv)
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return fmt.Errorf("listen grpc :%s: %w", cfg.GRPCPort, err)
	}

	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := store.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres not ready: %w", err)
		}
		if err := secStore.Ping(checkCtx); err != nil {
			return fmt.Errorf("redis not ready: %w", err)
		}
		return nil
	}

	opsSrv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           metrics.NewOpsHandler(cfg.ServiceName, readyFn, reg),
		ReadHeaderTimeout: 5 * time.Second,
	}

	pollerCtx, stopPoller := context.WithCancel(ctx)
	defer stopPoller()
	go userSrv.RunDomainPoller(pollerCtx)

	errCh := make(chan error, 2)
	go func() {
		log.Info("starting user gRPC server", slog.String("port", cfg.GRPCPort))
		if err := grpcSrv.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	go func() {
		log.Info("starting user ops HTTP server", slog.String("port", cfg.HTTPPort))
		if err := opsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http ops serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down user service")
	case err := <-errCh:
		return err
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = opsSrv.Shutdown(shutCtx)
	grpcSrv.GracefulStop()
	return nil
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
