package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

func TestReadyz_HardDepsOnly_NotFailingWhenMeiliDown(t *testing.T) {
	okPing := func(context.Context) error { return nil }
	failPing := func(context.Context) error { return errors.New("connection refused") }

	t.Run("200 when postgres, redis, nats healthy even without Meilisearch", func(t *testing.T) {
		readyFn := buildReadyCheck(okPing, okPing, func() bool { return true })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /readyz status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("503 when search-postgres fails", func(t *testing.T) {
		readyFn := buildReadyCheck(failPing, okPing, func() bool { return true })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /readyz status = %d, want 503", rec.Code)
		}
	})

	t.Run("503 when redis fails", func(t *testing.T) {
		readyFn := buildReadyCheck(okPing, failPing, func() bool { return true })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /readyz status = %d, want 503", rec.Code)
		}
	})

	t.Run("503 when nats disconnected", func(t *testing.T) {
		readyFn := buildReadyCheck(okPing, okPing, func() bool { return false })
		h := metrics.NewOpsHandler("search", readyFn, prometheus.NewRegistry())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET /readyz status = %d, want 503", rec.Code)
		}
	})
}

func startFakeAuthRedis(t *testing.T, wantPassword string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake redis: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeAuthRedisConn(conn, wantPassword)
		}
	}()
	return ln.Addr().String()
}

func serveFakeAuthRedisConn(conn net.Conn, wantPassword string) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	authed := false
	for {
		args, err := readRESPCommand(r)
		if err != nil || len(args) == 0 {
			return
		}
		cmd := strings.ToUpper(args[0])
		switch cmd {
		case "HELLO":
			_, _ = conn.Write([]byte("-ERR unknown command 'HELLO'\r\n"))
		case "AUTH":
			if len(args) >= 2 && args[len(args)-1] == wantPassword {
				authed = true
				_, _ = conn.Write([]byte("+OK\r\n"))
			} else {
				_, _ = conn.Write([]byte("-WRONGPASS invalid username-password pair\r\n"))
			}
		case "PING":
			if !authed {
				_, _ = conn.Write([]byte("-NOAUTH Authentication required.\r\n"))
			} else {
				_, _ = conn.Write([]byte("+PONG\r\n"))
			}
		default:
			if !authed {
				_, _ = conn.Write([]byte("-NOAUTH Authentication required.\r\n"))
			} else {
				_, _ = conn.Write([]byte("+OK\r\n"))
			}
		}
	}
}

func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "*") {
		return strings.Fields(line), nil
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 0 {
		return nil, errors.New("invalid resp array length")
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		hdr, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		hdr = strings.TrimRight(hdr, "\r\n")
		if !strings.HasPrefix(hdr, "$") {
			return nil, errors.New("expected bulk string")
		}
		size, err := strconv.Atoi(hdr[1:])
		if err != nil || size < 0 {
			return nil, errors.New("invalid bulk string length")
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return args, nil
}

func TestConnectRedisWithRetry_AuthenticatesWithPassword(t *testing.T) {
	const wantPassword = "search-redis-secret-pass"
	addr := startFakeAuthRedis(t, wantPassword)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("connects when password matches", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		rdb, err := connectRedisWithRetry(ctx, addr, wantPassword, log)
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

		rdb, err := connectRedisWithRetry(ctx, addr, "", log)
		if err == nil {
			_ = rdb.Close()
			t.Fatalf("expected error connecting without password to auth-protected redis")
		}
	})
}
