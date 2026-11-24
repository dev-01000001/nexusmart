package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type recordingRateLimiter struct {
	mu      sync.Mutex
	counts  map[string]int
	keys    []string
	failErr error
}

func newRecordingRateLimiter() *recordingRateLimiter {
	return &recordingRateLimiter{counts: make(map[string]int)}
}

func (r *recordingRateLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (RateLimitResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	if r.failErr != nil {
		return RateLimitResult{}, r.failErr
	}
	if r.counts[key] >= limit {
		return RateLimitResult{Allowed: false, Remaining: 0, RetryAfter: 15 * time.Second}, nil
	}
	r.counts[key]++
	return RateLimitResult{Allowed: true, Remaining: limit - r.counts[key]}, nil
}

func (r *recordingRateLimiter) SetError(err error) {
	r.mu.Lock()
	r.failErr = err
	r.mu.Unlock()
}

func (r *recordingRateLimiter) CheckedKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.keys))
	copy(out, r.keys)
	return out
}

// TestW3ARateLimitBankOTP verifies RateLimitBankOTP middleware behavior (key namespace,
// 5/min quota, Retry-After header, per-user & per-bucket isolation, fail-open on Redis error,
// and disable when limit <= 0). Real Redis Lua sliding-window execution is covered in e2e 39.
func TestW3ARateLimitBankOTP(t *testing.T) {
	authMgr, err := auth.NewManager("gw-bank-otp-rl-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	sellerBTok, _, _, _ := authMgr.SignAccessToken("seller-b", "seller", "shop-b")

	limiter := newRecordingRateLimiter()
	var logBuf syncBuffer
	log := logger.New("gw-bank-otp-rl-test", logger.WithWriter(&logBuf))

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	bankRL := Auth(authMgr, nil, log, true)(RateLimitBankOTP(limiter, 5, log)(okHandler))
	userRL := Auth(authMgr, nil, log, true)(RateLimitByUser(limiter, 20, log)(okHandler))

	withToken := func(tok string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/bank-account/verify-otp", strings.NewReader(`{"otp":"123456"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		return req
	}

	// 1. First 5 calls for seller-a succeed with expected rate-limit headers.
	for i := 1; i <= 5; i++ {
		rec := httptest.NewRecorder()
		bankRL.ServeHTTP(rec, withToken(sellerATok))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d for seller-a: want 200, got %d (%s)", i, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != "5" {
			t.Fatalf("call %d X-RateLimit-Limit = %q, want 5", i, got)
		}
		if got := rec.Header().Get("X-RateLimit-Remaining"); got != strconv.Itoa(5-i) {
			t.Fatalf("call %d X-RateLimit-Remaining = %q, want %d", i, got, 5-i)
		}
	}

	// 2. 6th call for seller-a returns 429 RATE_LIMITED with positive Retry-After header.
	rec6 := httptest.NewRecorder()
	bankRL.ServeHTTP(rec6, withToken(sellerATok))
	if rec6.Code != http.StatusTooManyRequests {
		t.Fatalf("call 6 for seller-a: want 429, got %d (%s)", rec6.Code, rec6.Body.String())
	}
	retryAfterSec, err := strconv.Atoi(rec6.Header().Get("Retry-After"))
	if err != nil || retryAfterSec < 1 {
		t.Fatalf("call 6 Retry-After = %q, want integer >= 1", rec6.Header().Get("Retry-After"))
	}
	var errEnv httpx.ErrorEnvelope
	if err := json.Unmarshal(rec6.Body.Bytes(), &errEnv); err != nil || errEnv.Error.Code != "RATE_LIMITED" {
		t.Fatalf("call 6 error envelope = %s, want RATE_LIMITED", rec6.Body.String())
	}

	// 3. Dedicated key namespace ("rl:bank_otp:user:seller-a") disjoint from general user bucket.
	foundBankKey := false
	for _, k := range limiter.CheckedKeys() {
		if k == "rl:bank_otp:user:seller-a" {
			foundBankKey = true
		}
	}
	if !foundBankKey {
		t.Fatalf("expected dedicated key rl:bank_otp:user:seller-a, got keys=%v", limiter.CheckedKeys())
	}

	// 4. General RateLimitByUser bucket for seller-a is NOT affected.
	recGen := httptest.NewRecorder()
	userRL.ServeHTTP(recGen, withToken(sellerATok))
	if recGen.Code != http.StatusOK {
		t.Fatalf("general user bucket for seller-a should stay 200, got %d (%s)", recGen.Code, recGen.Body.String())
	}

	// 5. Another seller (seller-b) is NOT affected by seller-a's exhausted bucket.
	for i := 1; i <= 5; i++ {
		recB := httptest.NewRecorder()
		bankRL.ServeHTTP(recB, withToken(sellerBTok))
		if recB.Code != http.StatusOK {
			t.Fatalf("call %d for seller-b: want 200, got %d (%s)", i, recB.Code, recB.Body.String())
		}
	}

	// 6. Limiter error -> fails open (matches all existing rate limiters) and emits warn log.
	limiter.SetError(errors.New("simulated redis failure"))
	recErr := httptest.NewRecorder()
	bankRL.ServeHTTP(recErr, withToken(sellerATok))
	limiter.SetError(nil)
	if recErr.Code != http.StatusOK {
		t.Fatalf("Redis error should fail open (200) matching existing limiters, got %d (%s)", recErr.Code, recErr.Body.String())
	}
	if !strings.Contains(logBuf.String(), "rate limiter unavailable, failing open") {
		t.Fatalf("expected warn log on Redis failure, got: %s", logBuf.String())
	}

	// 7. Disabled when limitPerMin <= 0 (does not invoke limiter).
	callsBefore := len(limiter.CheckedKeys())
	disabled := Auth(authMgr, nil, log, true)(RateLimitBankOTP(limiter, 0, log)(okHandler))
	recDisabled := httptest.NewRecorder()
	disabled.ServeHTTP(recDisabled, withToken(sellerATok))
	if recDisabled.Code != http.StatusOK {
		t.Fatalf("disabled limiter want 200, got %d", recDisabled.Code)
	}
	if len(limiter.CheckedKeys()) != callsBefore {
		t.Fatalf("disabled limiter should not call limiter.Allow")
	}
}
