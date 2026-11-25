package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	assistantv1 "github.com/nexusmart/core/gen/go/nexusmart/assistant/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/assistant/internal/config"
	"github.com/nexusmart/core/services/assistant/internal/consumer"
	"github.com/nexusmart/core/services/assistant/internal/grpcserver"
	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/httpserver"
	"github.com/nexusmart/core/services/assistant/internal/llm"
	"github.com/nexusmart/core/services/assistant/internal/policy"
	"github.com/nexusmart/core/services/assistant/internal/quota"
	"github.com/nexusmart/core/services/assistant/internal/resilience"
	"github.com/nexusmart/core/services/assistant/internal/retriever"
	"github.com/nexusmart/core/services/assistant/internal/service"
	"github.com/nexusmart/core/services/assistant/internal/store"
	"github.com/nexusmart/core/services/assistant/internal/tools"
	"github.com/nexusmart/core/services/assistant/migrations"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var validDBNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

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

	// 1. Storage: PostgreSQL (assistant_db with safe auto-create) or in-memory standalone fallback.
	var (
		dataStore store.Store
		pgPool    *pgxpool.Pool
	)
	if strings.TrimSpace(cfg.DatabaseURL) != "" {
		pgPool, err = connectPostgres(ctx, cfg.DatabaseURL, log)
		if err != nil {
			log.Error("failed to connect postgres", slog.String("error", err.Error()))
			return err
		}
		defer pgPool.Close()

		if err := migrations.Apply(ctx, pgPool); err != nil {
			log.Error("failed to apply assistant_db migrations", slog.String("error", err.Error()))
			return err
		}
		log.Info("assistant_db migrated cleanly")
		dataStore = store.NewPGStore(pgPool)
	} else {
		log.Warn("DATABASE_URL not set; running assistant with in-memory store")
		dataStore = store.NewMemoryStore()
	}

	// 2. Quota, cost cap, abuse lockout, and cache manager: Redis primary with memory fallback.
	quotaCfg := quota.Config{
		DailyUserTokenLimit: cfg.DailyUserTokenLimit,
		DailyCostLimitVND:   cfg.DailyCostLimitVND,
		AbuseStrikeLimit:    cfg.AbuseStrikeLimit,
		AbuseLockoutWindow:  cfg.AbuseLockoutWindow,
	}
	var (
		quotaMgr quota.Manager
		rdb      *redis.Client
	)
	if strings.TrimSpace(cfg.RedisAddr) != "" {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.RedisAddr,
			Password: cfg.RedisPassword,
		})
		if pingErr := waitForRedis(ctx, rdb, log); pingErr == nil {
			quotaMgr = quota.NewRedisManager(rdb, quotaCfg)
			defer func() { _ = rdb.Close() }()
		} else {
			_ = rdb.Close()
			rdb = nil
			log.Warn("redis unavailable; falling back to in-memory quota/cache manager", slog.String("error", pingErr.Error()))
			quotaMgr = quota.NewMemoryManager(quotaCfg)
		}
	} else {
		log.Warn("REDIS_ADDR not set; using in-memory quota/cache manager")
		quotaMgr = quota.NewMemoryManager(quotaCfg)
	}

	// 3. Guardrail, LLM provider (mock | anthropic), Policy RAG, Product retriever, and Tools.
	guard := guardrail.New()

	primaryLLM, fallbackLLM, err := buildLLMProviders(cfg, log)
	if err != nil {
		log.Error("failed to init LLM provider", slog.String("error", err.Error()))
		return err
	}

	polRet, err := policy.NewRAG(cfg.PoliciesDir, cfg.EmbedMode)
	if err != nil {
		log.Error("failed to init policy RAG", slog.String("error", err.Error()))
		return err
	}

	prodRet, err := retriever.NewChainedRetriever(retriever.ChainedConfig{
		SearchGRPCAddr:  cfg.SearchGRPCAddr,
		ProductGRPCAddr: cfg.ProductGRPCAddr,
		GatewayURL:      cfg.GatewayURL,
		ServiceName:     cfg.ServiceName,
		Timeout:         5 * time.Second,
		Guardrail:       guard,
		Logger:          log,
	})
	if err != nil {
		log.Error("failed to init product retriever", slog.String("error", err.Error()))
		return err
	}
	defer func() { _ = prodRet.Close() }()

	gwClient, err := tools.NewHTTPGatewayClient(cfg.GatewayURL, 8*time.Second, resilience.NewCircuitBreaker(5, 15*time.Second))
	if err != nil {
		log.Error("failed to init gateway HTTP client", slog.String("error", err.Error()))
		return err
	}

	toolReg := tools.NewRegistry(prodRet, polRet, gwClient, guard, log)

	red := metrics.NewRED(cfg.ServiceName, prometheus.DefaultRegisterer)
	assistantMetrics := service.NewMetrics(prometheus.DefaultRegisterer)

	svc := service.New(service.Deps{
		Config:      cfg,
		LLM:         primaryLLM,
		FallbackLLM: fallbackLLM,
		Guardrail:   guard,
		Tools:       toolReg,
		Products:    prodRet,
		Policies:    polRet,
		Quota:       quotaMgr,
		Store:       dataStore,
		Metrics:     assistantMetrics,
		Logger:      log,
	})

	// 4. Optional NATS JetStream consumer for asynchronous review summarization.
	var (
		natsConn     *nats.Conn
		natsConsumer *consumer.Consumer
	)
	if strings.TrimSpace(cfg.NATSURL) != "" {
		nc, js, natsErr := connectNATS(ctx, cfg.NATSURL, cfg.NATSStream, cfg.NATSDLQStream, log)
		if natsErr != nil {
			log.Warn("NATS JetStream unavailable; review event consumer disabled", slog.String("error", natsErr.Error()))
		} else {
			natsConn = nc
			defer natsConn.Close()
			natsConsumer = consumer.NewConsumer(js, svc, dataStore, cfg.NATSStream, 5, log)
			if startErr := natsConsumer.Start(ctx); startErr != nil {
				log.Warn("failed to start NATS review consumer", slog.String("error", startErr.Error()))
			} else {
				log.Info("assistant NATS review summarizer consumer started")
				defer func() { _ = natsConsumer.Close() }()
			}
		}
	}

	// 5. JWT managers (end-user JWT verified for all buyer/seller calls; optional service token verifier only).
	jwtMgr, err := auth.NewManager(cfg.JWTSecret, auth.DefaultAccessTTL)
	if err != nil {
		log.Error("failed to init JWT manager", slog.String("error", err.Error()))
		return err
	}
	var svcMgr *auth.Manager
	if rawSvcSecret := strings.TrimSpace(os.Getenv("SERVICE_JWT_SECRET")); rawSvcSecret != "" {
		if m, mErr := auth.NewManager(rawSvcSecret, auth.ServiceTokenTTL); mErr == nil {
			svcMgr = m
		}
	}

	// 6. Start gRPC server on :50061.
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			telemetry.GRPCUnaryServerInterceptor(cfg.ServiceName),
			red.GRPCUnaryServerInterceptor(),
			auth.GRPCUnaryServerInterceptor(jwtMgr, svcMgr),
		),
	)
	assistantv1.RegisterAssistantServiceServer(grpcSrv, grpcserver.New(svc))
	reflection.Register(grpcSrv)

	grpcLis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		log.Error("failed to listen gRPC", slog.String("port", cfg.GRPCPort), slog.String("error", err.Error()))
		return err
	}
	go func() {
		log.Info("assistant gRPC server listening", slog.String("port", cfg.GRPCPort))
		if serveErr := grpcSrv.Serve(grpcLis); serveErr != nil {
			log.Error("assistant gRPC serve error", slog.String("error", serveErr.Error()))
		}
	}()

	// 7. Start HTTP SSE + ops server on :8091 (WriteTimeout: 0 to allow long SSE streams).
	readyFn := func(rctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(rctx, 2*time.Second)
		defer cancel()
		if err := dataStore.Ping(checkCtx); err != nil {
			return fmt.Errorf("store: %w", err)
		}
		if err := quotaMgr.Ping(checkCtx); err != nil {
			return fmt.Errorf("quota: %w", err)
		}
		return nil
	}
	httpHandler := httpserver.New(httpserver.Config{
		ServiceName: cfg.ServiceName,
		Service:     svc,
		AuthManager: jwtMgr,
		RED:         red,
		Gatherer:    prometheus.DefaultGatherer,
		ReadyCheck:  readyFn,
		Logger:      log,
	})

	httpSrv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           httpHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0, // SSE streaming requires no global write timeout.
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		log.Info("assistant HTTP SSE server listening", slog.String("port", cfg.HTTPPort), slog.String("llm_mode", cfg.LLMMode))
		if serveErr := httpSrv.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Error("assistant HTTP serve error", slog.String("error", serveErr.Error()))
		}
	}()

	<-ctx.Done()
	log.Info("shutting down assistant service")

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)
	grpcSrv.GracefulStop()
	return nil
}

func buildLLMProviders(cfg config.Config, log *slog.Logger) (llm.Provider, llm.Provider, error) {
	switch cfg.LLMMode {
	case "anthropic":
		if strings.TrimSpace(cfg.AnthropicAPIKey) == "" {
			log.Warn("LLM_MODE=anthropic requested without ANTHROPIC_API_KEY; falling back to mock provider")
			return llm.NewMockProvider(cfg.InputCostVNDPer1K, cfg.OutputCostVNDPer1K), nil, nil
		}
		primary, err := llm.NewAnthropicProvider(llm.AnthropicConfig{
			BaseURL:            cfg.AnthropicBaseURL,
			APIKey:             cfg.AnthropicAPIKey,
			Model:              cfg.LLMModel,
			Timeout:            cfg.LLMTimeout,
			InputCostVNDPer1K:  cfg.InputCostVNDPer1K,
			OutputCostVNDPer1K: cfg.OutputCostVNDPer1K,
		})
		if err != nil {
			return nil, nil, err
		}
		var fallback llm.Provider
		if strings.TrimSpace(cfg.LLMFallbackModel) != "" {
			fb, fbErr := llm.NewAnthropicProvider(llm.AnthropicConfig{
				BaseURL:            cfg.AnthropicBaseURL,
				APIKey:             cfg.AnthropicAPIKey,
				Model:              cfg.LLMFallbackModel,
				Timeout:            cfg.LLMTimeout,
				InputCostVNDPer1K:  cfg.InputCostVNDPer1K,
				OutputCostVNDPer1K: cfg.OutputCostVNDPer1K,
			})
			if fbErr == nil {
				fallback = fb
			}
		}
		return primary, fallback, nil
	default:
		return llm.NewMockProvider(cfg.InputCostVNDPer1K, cfg.OutputCostVNDPer1K), nil, nil
	}
}

func connectPostgres(ctx context.Context, dsn string, log *slog.Logger) (*pgxpool.Pool, error) {
	var lastErr error
	createdTried := false
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
			if !createdTried && isMissingDatabaseError(err) {
				createdTried = true
				if createErr := ensureDatabaseExists(ctx, dsn, log); createErr != nil {
					log.Warn("auto-create assistant_db failed", slog.String("error", createErr.Error()))
				} else {
					continue
				}
			}
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

func isMissingDatabaseError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "3D000" {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "does not exist")
}

func ensureDatabaseExists(ctx context.Context, dsn string, log *slog.Logger) error {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return err
	}
	dbName := strings.TrimPrefix(parsed.Path, "/")
	if !validDBNamePattern.MatchString(dbName) {
		return fmt.Errorf("unsafe database name %q", dbName)
	}
	adminURL := *parsed
	adminURL.Path = "/postgres"

	adminPool, err := pgxpool.New(ctx, adminURL.String())
	if err != nil {
		return err
	}
	defer adminPool.Close()

	createCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = adminPool.Exec(createCtx, fmt.Sprintf(`CREATE DATABASE "%s"`, dbName))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
			return nil
		}
		return err
	}
	log.Info("auto-created postgres database", slog.String("database", dbName))
	return nil
}

func waitForRedis(ctx context.Context, rdb *redis.Client, log *slog.Logger) error {
	var lastErr error
	for i := 0; i < 6; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := rdb.Ping(pingCtx).Err()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		log.Warn("waiting for redis...", slog.Int("attempt", i+1), slog.String("error", err.Error()))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return lastErr
}

func connectNATS(ctx context.Context, rawURL, primaryStream, dlqStream string, log *slog.Logger) (*nats.Conn, nats.JetStreamContext, error) {
	var lastErr error
	for i := 0; i < 10; i++ {
		nc, err := nats.Connect(rawURL,
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
