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
	notificationv1 "github.com/nexusmart/core/gen/go/nexusmart/notification/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/notification/internal/config"
	"github.com/nexusmart/core/services/notification/internal/consumer"
	"github.com/nexusmart/core/services/notification/internal/db"
	"github.com/nexusmart/core/services/notification/internal/grpcserver"
	"github.com/nexusmart/core/services/notification/internal/mailer"
	"github.com/nexusmart/core/services/notification/internal/service"
	"github.com/nexusmart/core/services/notification/internal/store"
	"github.com/nexusmart/core/services/notification/internal/userclient"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
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

	if err := db.Migrate(ctx, pool); err != nil {
		log.Error("failed to run notification_db migrations", slog.String("error", err.Error()))
		return err
	}
	log.Info("notification_db migrated cleanly")

	nc, js, err := connectNATS(ctx, cfg.NATSURL, cfg.NATSStream, cfg.NATSDLQStream, log)
	if err != nil {
		log.Error("failed to connect NATS JetStream", slog.String("error", err.Error()))
		return err
	}
	defer nc.Close()

	svcMgr, err := auth.NewManager(os.Getenv("SERVICE_JWT_SECRET"), auth.ServiceTokenTTL)
	if err != nil {
		log.Error("failed to init service token manager (SERVICE_JWT_SECRET)", slog.String("error", err.Error()))
		return err
	}

	var userResolver userclient.Resolver
	if uc, ucErr := userclient.New(cfg.UserGRPCAddr, cfg.ServiceName, svcMgr); ucErr == nil {
		userResolver = uc
		defer func() { _ = uc.Close() }()
	} else {
		log.Warn("user gRPC client unavailable; relying on event payload emails", slog.String("error", ucErr.Error()))
	}

	pgStore := store.NewPGStore(pool)
	smtpMailer := mailer.NewSMTPMailer(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom)
	svc := service.NewNotificationService(pgStore, smtpMailer, userResolver, log)

	natsConsumer := consumer.NewConsumer(js, svc, pgStore, cfg.NATSStream, cfg.ConsumerMaxTries, log)
	if err := natsConsumer.Start(ctx); err != nil {
		log.Error("failed to start notification JetStream consumer", slog.String("error", err.Error()))
		return err
	}
	defer func() { _ = natsConsumer.Close() }()

	red := metrics.NewRED(cfg.ServiceName, prometheus.DefaultRegisterer)
	jwtMgr, err := auth.NewManager(cfg.JWTSecret, auth.DefaultAccessTTL)
	if err != nil {
		log.Error("failed to init JWT manager", slog.String("error", err.Error()))
		return err
	}

	// Start gRPC server on :50058.
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	notificationv1.RegisterNotificationServiceServer(grpcSrv, grpcserver.New(svc))
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Error("failed to listen gRPC", slog.String("port", cfg.GRPCPort), slog.String("error", err.Error()))
		return err
	}
	go func() {
		log.Info("notification gRPC server listening", slog.String("port", cfg.GRPCPort))
		if serveErr := grpcSrv.Serve(grpcLis); serveErr != nil {
			log.Error("notification gRPC serve error", slog.String("error", serveErr.Error()))
		}
	}()

	// Start HTTP ops server on :8088 (/healthz, /readyz, /metrics).
	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := pool.Ping(checkCtx); err != nil {
			return fmt.Errorf("postgres: %w", err)
		}
		if !nc.IsConnected() {
			return errors.New("nats: disconnected")
		}
		return nil
	}
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
	go func() {
		log.Info("notification HTTP ops server listening", slog.String("port", cfg.HTTPPort))
		if serveErr := httpSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("notification HTTP serve error", slog.String("error", serveErr.Error()))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down notification service")

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
		log.Warn("waiting for NATS JetStream...", slog.Int("attempt", i+1), slog.String("error", lastErr.Error()))
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, nil, lastErr
}
