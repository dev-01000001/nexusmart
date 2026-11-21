package main

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func TestConnectHelpersAndDialProductService(t *testing.T) {
	log := logger.New("promotion-main-test", logger.WithWriter(io.Discard))

	// connectRedis succeeds against miniredis
	mr := miniredis.RunT(t)
	rdb, err := connectRedis(context.Background(), mr.Addr(), "", log)
	if err != nil {
		t.Fatalf("connectRedis to miniredis failed: %v", err)
	}
	_ = rdb.Close()

	// connectRedis respects canceled context on unreachable address
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connectRedis(canceledCtx, "127.0.0.1:1", "", log); err == nil {
		t.Fatal("expected connectRedis error with canceled ctx")
	}

	// connectPostgres rejects invalid DSN immediately & respects canceled ctx
	if _, err := connectPostgres(context.Background(), "://invalid-dsn", log); err == nil {
		t.Fatal("expected connectPostgres parse error")
	}
	if _, err := connectPostgres(canceledCtx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable", log); err == nil {
		t.Fatal("expected connectPostgres error with canceled ctx")
	}

	// connectNATS respects canceled context on unreachable broker
	if _, _, err := connectNATS(canceledCtx, "nats://127.0.0.1:1", "NEXUSMART", store.NewMemoryStore(), nil, log); err == nil {
		t.Fatal("expected connectNATS error with canceled ctx")
	}

	// dialProductService creates a valid gRPC client connection
	svcMgr, err := auth.NewManager("test-svc-secret-key", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	conn, err := dialProductService("127.0.0.1:50052", "promotion", 2*time.Second, svcMgr)
	if err != nil {
		t.Fatalf("dialProductService: %v", err)
	}
	_ = conn.Close()
}
