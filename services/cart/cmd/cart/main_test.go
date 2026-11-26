package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestConnectRedisWithRetry_AuthenticatesWithPassword(t *testing.T) {
	const wantPassword = "cart-redis-secret-pass"
	mr := miniredis.RunT(t)
	mr.RequireAuth(wantPassword)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("reads REDIS_PASSWORD from environment", func(t *testing.T) {
		t.Setenv("REDIS_ADDR", mr.Addr())
		t.Setenv("REDIS_PASSWORD", wantPassword)

		addr, pass := loadRedisConfig()
		if addr != mr.Addr() {
			t.Fatalf("REDIS_ADDR = %q, want %q", addr, mr.Addr())
		}
		if pass != wantPassword {
			t.Fatalf("REDIS_PASSWORD = %q, want %q", pass, wantPassword)
		}
	})

	t.Run("connects when password matches", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		rdb, err := connectRedisWithRetry(ctx, mr.Addr(), wantPassword, log)
		if err != nil {
			t.Fatalf("connectRedisWithRetry with valid password failed: %v", err)
		}
		defer func() { _ = rdb.Close() }()

		if err := rdb.Ping(ctx).Err(); err != nil {
			t.Fatalf("authenticated Ping failed: %v", err)
		}
	})

	t.Run("fails when password is empty on auth-protected redis", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()

		rdb, err := connectRedisWithRetry(ctx, mr.Addr(), "", log)
		if err == nil {
			_ = rdb.Close()
			t.Fatalf("expected error connecting without password to auth-protected redis")
		}
		if !strings.Contains(err.Error(), "NOAUTH") && !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("unexpected error without password: %v", err)
		}
	})
}
