package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type windowRecordingLimiter struct {
	mu      sync.Mutex
	counts  map[string]int
	calls   []rateLimitCall
	failErr error
}

type rateLimitCall struct {
	key    string
	limit  int
	window time.Duration
}

func newWindowRecordingLimiter() *windowRecordingLimiter {
	return &windowRecordingLimiter{counts: make(map[string]int)}
}

func (l *windowRecordingLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (RateLimitResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, rateLimitCall{key: key, limit: limit, window: window})
	if l.failErr != nil {
		return RateLimitResult{}, l.failErr
	}
	if l.counts[key] >= limit {
		return RateLimitResult{Allowed: false, Remaining: 0, RetryAfter: 45 * time.Second}, nil
	}
	l.counts[key]++
	return RateLimitResult{Allowed: true, Remaining: limit - l.counts[key]}, nil
}

func TestW3B3RateLimitOrderLookup_DualLimitsG7(t *testing.T) {
	limiter := newWindowRecordingLimiter()
	var logBuf syncBuffer
	log := logger.New("gw-order-lookup-rl-test", logger.WithWriter(&logBuf))

	var lastCode string
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OrderCode string `json:"order_code"`
			Phone     string `json:"phone"`
		}
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.WriteDecodeError(w, r, err)
			return
		}
		if !AllowOrderLookupCode(w, r, limiter, 5, body.OrderCode, log) {
			return
		}
		lastCode = body.OrderCode
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	chain := RateLimitOrderLookupIP(limiter, 10, log, nil)(okHandler)

	doLookup := func(ip, orderCode, phone string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"order_code":%q,"phone":%q}`, orderCode, phone)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(body))
		req.RemoteAddr = ip + ":12345"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		return rec
	}

	// 1. Attack G7-a: 10 requests from same IP with different order codes pass; 11th returns 429 RATE_LIMIT_EXCEEDED + Retry-After.
	for i := 1; i <= 10; i++ {
		code := fmt.Sprintf("NM-IP-%02d", i)
		rec := doLookup("198.51.100.10", code, "0901234567")
		if rec.Code != http.StatusOK {
			t.Fatalf("IP call %d: want 200, got %d (%s)", i, rec.Code, rec.Body.String())
		}
		if lastCode != code {
			t.Fatalf("IP call %d: decoded order_code = %q, want %q", i, lastCode, code)
		}
	}
	rec11 := doLookup("198.51.100.10", "NM-IP-11", "0901234567")
	if rec11.Code != http.StatusTooManyRequests {
		t.Fatalf("IP call 11: want 429, got %d (%s)", rec11.Code, rec11.Body.String())
	}
	if sec, err := strconv.Atoi(rec11.Header().Get("Retry-After")); err != nil || sec < 1 {
		t.Fatalf("IP call 11 Retry-After = %q, want integer >= 1", rec11.Header().Get("Retry-After"))
	}
	var env11 httpx.ErrorEnvelope
	if err := json.Unmarshal(rec11.Body.Bytes(), &env11); err != nil || env11.Error.Code != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("IP call 11 error envelope = %s, want RATE_LIMIT_EXCEEDED", rec11.Body.String())
	}

	// 2. Attack G7-b: 5 requests for the SAME normalized order_code from DIFFERENT IPs pass; 6th from a 6th IP returns 429 RATE_LIMIT_EXCEEDED + Retry-After.
	codeVariants := []string{"NM-TARGET-01", "nm-target-01", "  NM-TARGET-01 ", "Nm-Target-01", "nm-target-01  "}
	for i, variant := range codeVariants {
		ip := fmt.Sprintf("203.0.113.%d", i+1)
		rec := doLookup(ip, variant, "0909998888")
		if rec.Code != http.StatusOK {
			t.Fatalf("code call %d (%s from %s): want 200, got %d (%s)", i+1, variant, ip, rec.Code, rec.Body.String())
		}
	}
	recCode6 := doLookup("203.0.113.6", "nm-target-01", "0909998888")
	if recCode6.Code != http.StatusTooManyRequests {
		t.Fatalf("code call 6 from new IP: want 429, got %d (%s)", recCode6.Code, recCode6.Body.String())
	}
	if sec, err := strconv.Atoi(recCode6.Header().Get("Retry-After")); err != nil || sec < 1 {
		t.Fatalf("code call 6 Retry-After = %q, want integer >= 1", recCode6.Header().Get("Retry-After"))
	}
	var envCode6 httpx.ErrorEnvelope
	if err := json.Unmarshal(recCode6.Body.Bytes(), &envCode6); err != nil || envCode6.Error.Code != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("code call 6 error envelope = %s, want RATE_LIMIT_EXCEEDED", recCode6.Body.String())
	}

	// 3. Verify exact Redis keys and windows (1m for IP, 10m for normalized order_code).
	var sawIPKey, sawCodeKey bool
	limiter.mu.Lock()
	for _, c := range limiter.calls {
		if c.key == "rl:order_lookup:ip:198.51.100.10" && c.limit == 10 && c.window == time.Minute {
			sawIPKey = true
		}
		if c.key == "rl:order_lookup:code:NM-TARGET-01" && c.limit == 5 && c.window == 10*time.Minute {
			sawCodeKey = true
		}
	}
	limiter.mu.Unlock()
	if !sawIPKey || !sawCodeKey {
		t.Fatalf("expected keys/windows rl:order_lookup:ip:<ip> (10/1m) and rl:order_lookup:code:NM-TARGET-01 (5/10m), got calls=%+v", limiter.calls)
	}

	// 4. Redis error fails open without logging phone or order_code.
	limiter.mu.Lock()
	limiter.failErr = errors.New("redis down")
	limiter.mu.Unlock()
	recFail := doLookup("198.51.100.99", "NM-SECRET-99", "0911222333")
	limiter.mu.Lock()
	limiter.failErr = nil
	limiter.mu.Unlock()
	if recFail.Code != http.StatusOK {
		t.Fatalf("redis error should fail open (200), got %d", recFail.Code)
	}
	if strings.Contains(logBuf.String(), "0911222333") || strings.Contains(logBuf.String(), "NM-SECRET-99") {
		t.Fatalf("rate limiter warning leaked PII/code: %s", logBuf.String())
	}
}

func TestW3B3RateLimitOrderLookup_ConcurrentRace(t *testing.T) {
	limiter := newWindowRecordingLimiter()
	log := logger.New("gw-order-lookup-race", logger.WithWriter(io.Discard))
	chain := RateLimitOrderLookupIP(limiter, 100, log, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OrderCode string `json:"order_code"`
		}
		if err := httpx.DecodeJSON(r, &body); err != nil {
			httpx.WriteDecodeError(w, r, err)
			return
		}
		if !AllowOrderLookupCode(w, r, limiter, 5, body.OrderCode, log) {
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	var allowed, blocked atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/lookup", strings.NewReader(`{"order_code":"NM-RACE-01","phone":"0901234567"}`))
			req.RemoteAddr = fmt.Sprintf("198.51.100.%d:5000", idx+1)
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, req)
			switch rec.Code {
			case http.StatusOK:
				allowed.Add(1)
			case http.StatusTooManyRequests:
				blocked.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if allowed.Load() != 5 || blocked.Load() != 7 {
		t.Fatalf("concurrent lookup on same code: allowed=%d blocked=%d, want 5/7", allowed.Load(), blocked.Load())
	}
}
