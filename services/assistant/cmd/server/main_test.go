package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nexusmart/core/services/assistant/internal/httpserver"
	"github.com/nexusmart/core/services/assistant/internal/quota"
	"github.com/nexusmart/core/services/assistant/internal/store"
	"github.com/redis/go-redis/v9"
)

func reserveClosedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen temp addr: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func TestInitQuotaAndRevocationAndReadyCheck(t *testing.T) {
	origAttempts := redisWaitAttempts
	origInterval := redisWaitInterval
	redisWaitAttempts = 1
	redisWaitInterval = time.Millisecond
	t.Cleanup(func() {
		redisWaitAttempts = origAttempts
		redisWaitInterval = origInterval
	})

	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("REDIS_ADDR set while Redis is down keeps RedisManager, fails readyz, and self-heals", func(t *testing.T) {
		addr := reserveClosedAddr(t)
		quotaMgr, rdb, revoker := initQuotaAndRevocation(context.Background(), addr, "", quota.Config{}, discardLog)
		if rdb == nil {
			t.Fatalf("expected non-nil redis client when REDIS_ADDR is configured")
		}
		defer func() { _ = rdb.Close() }()
		if revoker == nil {
			t.Fatalf("expected non-nil revocation checker when REDIS_ADDR is configured")
		}
		if _, ok := quotaMgr.(*quota.RedisManager); !ok {
			t.Fatalf("expected *quota.RedisManager when REDIS_ADDR is configured, got %T", quotaMgr)
		}

		readyFn := buildReadyCheck(store.NewMemoryStore(), quotaMgr, rdb)
		srv := httptest.NewServer(httpserver.New(httpserver.Config{
			ServiceName: "assistant",
			ReadyCheck:  readyFn,
			Logger:      discardLog,
		}))
		defer srv.Close()

		// While Redis is unreachable, /readyz MUST report 503.
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatalf("GET /readyz while redis down: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("expected /readyz 503 while Redis is down, got %d", resp.StatusCode)
		}

		// Bring Redis up on the same address: /readyz and RedisManager self-heal without restart.
		mr := miniredis.NewMiniRedis()
		if err := mr.StartAddr(addr); err != nil {
			t.Fatalf("start miniredis on %s: %v", addr, err)
		}
		defer mr.Close()

		resp2, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatalf("GET /readyz after redis recovery: %v", err)
		}
		_ = resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			t.Fatalf("expected /readyz 200 after Redis recovers, got %d", resp2.StatusCode)
		}

		if err := quotaMgr.SetCache(context.Background(), "self-heal", []byte("ok"), time.Minute); err != nil {
			t.Fatalf("SetCache after Redis recovery failed: %v", err)
		}
		if !mr.Exists("assistant:cache:self-heal") {
			t.Fatalf("expected quota manager to persist into recovered Redis")
		}
	})

	t.Run("buildReadyCheck pings rdb directly even if quotaMgr is MemoryManager", func(t *testing.T) {
		deadAddr := reserveClosedAddr(t)
		rdb := redis.NewClient(&redis.Options{Addr: deadAddr})
		defer func() { _ = rdb.Close() }()

		readyFn := buildReadyCheck(store.NewMemoryStore(), quota.NewMemoryManager(quota.Config{}), rdb)
		if err := readyFn(context.Background()); err == nil {
			t.Fatalf("expected readyFn to fail when rdb is unreachable")
		}
	})

	t.Run("empty REDIS_ADDR keeps MemoryManager and readyz returns 200", func(t *testing.T) {
		quotaMgr, rdb, revoker := initQuotaAndRevocation(context.Background(), "", "", quota.Config{}, discardLog)
		if rdb != nil {
			t.Fatalf("expected nil redis client when REDIS_ADDR is empty")
		}
		if revoker != nil {
			t.Fatalf("expected nil revocation checker when REDIS_ADDR is empty")
		}
		if _, ok := quotaMgr.(*quota.MemoryManager); !ok {
			t.Fatalf("expected *quota.MemoryManager when REDIS_ADDR is empty, got %T", quotaMgr)
		}

		readyFn := buildReadyCheck(store.NewMemoryStore(), quotaMgr, rdb)
		if err := readyFn(context.Background()); err != nil {
			t.Fatalf("expected readyFn to succeed without REDIS_ADDR, got %v", err)
		}
	})
}
