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

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/env"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/cart/internal/server"
	"github.com/nexusmart/core/services/cart/internal/store"
)

func main() {
	serviceName := env.EnvString("SERVICE_NAME", "cart")
	log := logger.New(serviceName, logger.WithLevel(parseLogLevel(env.EnvString("LOG_LEVEL", "info"))))
	if err := run(serviceName, log); err != nil {
		log.Error("cart service exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(serviceName string, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	grpcPort := env.EnvString("GRPC_PORT", "50053")
	httpPort := env.EnvString("HTTP_PORT", "8083")
	redisAddr := env.EnvString("REDIS_ADDR", "redis:6379")
	productAddr := env.EnvString("PRODUCT_GRPC_ADDR", "product:50052")
	userAddr := env.EnvString("USER_GRPC_ADDR", "")
	jwtSecret := env.EnvString("JWT_SECRET", "dev-only-nexusmart-hs256-secret-change-me")
	otelEndpoint := env.EnvString("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	otelInsecure := env.EnvBool("OTEL_EXPORTER_OTLP_INSECURE", true)

	shutdownTelemetry, err := telemetry.Init(ctx, telemetry.Config{
		ServiceName:  serviceName,
		OTLPEndpoint: otelEndpoint,
		Insecure:     otelInsecure,
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

	rdb, err := connectRedisWithRetry(ctx, redisAddr, log)
	if err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	prodConn, err := grpc.NewClient(
		productAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(serviceName)),
	)
	if err != nil {
		return fmt.Errorf("create product grpc client: %w", err)
	}
	defer func() { _ = prodConn.Close() }()
	productClient := productv1.NewProductServiceClient(prodConn)

	var userClient server.UserClient
	if userAddr != "" {
		userConn, dialErr := grpc.NewClient(
			userAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithChainUnaryInterceptor(telemetry.GRPCUnaryClientInterceptor(serviceName)),
		)
		if dialErr != nil {
			log.Warn("failed to initialize optional user grpc client", slog.String("error", dialErr.Error()))
		} else {
			defer func() { _ = userConn.Close() }()
			userClient = userv1.NewUserServiceClient(userConn)
		}
	}

	cartStore := store.NewRedisStore(rdb)
	red := metrics.NewRED(serviceName, nil)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(serviceName),
			red.GRPCUnaryServerInterceptor(),
			logger.GRPCUnaryServerInterceptor(log),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	cartSrv := server.New(cartStore, productClient, userClient, log)
	cartv1.RegisterCartServiceServer(grpcServer, cartSrv)
	reflection.Register(grpcServer)

	grpcLis, err := net.Listen("tcp", ":"+grpcPort)
	if err != nil {
		return fmt.Errorf("listen grpc :%s: %w", grpcPort, err)
	}

	opsHandler := metrics.NewOpsHandler(serviceName, func(rctx context.Context) error {
		pingCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		return cartStore.Ping(pingCtx)
	}, nil)
	httpHandler := telemetry.HTTPMiddleware(serviceName)(red.HTTPMiddleware()(opsHandler))

	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Info("starting cart gRPC server", slog.String("addr", ":"+grpcPort))
		if err := grpcServer.Serve(grpcLis); err != nil {
			errCh <- fmt.Errorf("grpc serve: %w", err)
		}
	}()
	go func() {
		log.Info("starting cart HTTP ops server", slog.String("addr", ":"+httpPort))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down cart service")
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

func connectRedisWithRetry(ctx context.Context, addr string, log *slog.Logger) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
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
	return nil, fmt.Errorf("redis unavailable after retries: %w", lastErr)
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
