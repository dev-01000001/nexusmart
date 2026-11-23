package middleware

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type memRevoker struct {
	revoked map[string]bool
}

func (m *memRevoker) IsJTIRevoked(_ context.Context, jti string) (bool, error) {
	return m.revoked[jti], nil
}

type memRateLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newMemRateLimiter() *memRateLimiter {
	return &memRateLimiter{counts: make(map[string]int)}
}

func (m *memRateLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (RateLimitResult, error) {
	if limit <= 0 {
		return RateLimitResult{Allowed: true}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts[key] >= limit {
		return RateLimitResult{
			Allowed:    false,
			Remaining:  0,
			RetryAfter: 12 * time.Second,
		}, nil
	}
	m.counts[key]++
	return RateLimitResult{
		Allowed:   true,
		Remaining: limit - m.counts[key],
	}, nil
}

func TestRateLimitByIPAndDisableWhenNonPositive(t *testing.T) {
	log := logger.New("gw-test", logger.WithWriter(io.Discard))
	lim := newMemRateLimiter()

	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Limit of 2 per min -> 1st & 2nd pass, 3rd returns 429 with Retry-After.
	h := RateLimitByIP(lim, 2, log, nil)(okHandler)
	for i := 1; i <= 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "10.0.0.5:12345"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("req %d expected 200, got %d", i, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("req 3 expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429")
	}
	var env httpx.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "RATE_LIMITED" {
		t.Fatalf("expected RATE_LIMITED error envelope, got %s", rec.Body.String())
	}

	// Disabled when limit <= 0.
	disabled := RateLimitByIP(lim, 0, log, nil)(okHandler)
	for i := 0; i < 5; i++ {
		r2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req2.RemoteAddr = "10.0.0.5:12345"
		disabled.ServeHTTP(r2, req2)
		if r2.Code != http.StatusOK {
			t.Fatalf("disabled limiter expected 200, got %d", r2.Code)
		}
	}
}

func TestAuthAndRBACMiddleware(t *testing.T) {
	authMgr, err := auth.NewManager("gw-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	buyerTok, buyerJTI, _, _ := authMgr.SignAccessToken("user-buyer", "buyer", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("user-admin", "admin", "")
	revoker := &memRevoker{revoked: map[string]bool{}}
	log := logger.New("gw-test", logger.WithWriter(io.Discard))

	adminRoute := Auth(authMgr, revoker, log, true)(RequireRoles("admin", "super_admin")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// Missing Authorization -> 401
	rec := httptest.NewRecorder()
	adminRoute.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	// Buyer token on admin route -> 403
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	adminRoute.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}

	// Admin token on admin route -> 200
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	adminRoute.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Revoked JWT JTI -> 401 UNAUTHENTICATED globally at gateway.
	revoker.revoked[buyerJTI] = true
	anyAuthedRoute := Auth(authMgr, revoker, log, true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/cart", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	anyAuthedRoute.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for revoked JTI, got %d", rec.Code)
	}
	var env httpx.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "UNAUTHENTICATED" {
		t.Fatalf("expected UNAUTHENTICATED envelope on revoked JTI, got %s", rec.Body.String())
	}
}

func TestRecovererReturnsCanonicalJSONEnvelope(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gw-panic-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	log := logger.New("gw-panic-test", logger.WithWriter(io.Discard))
	h := telemetry.HTTPMiddleware("gw-panic-test")(Recoverer(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	var env httpx.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("expected valid JSON error envelope, got %q (%v)", rec.Body.String(), err)
	}
	if env.Error.Code != "INTERNAL_ERROR" || env.Error.TraceID == "" {
		t.Fatalf("unexpected panic error envelope: %+v", env.Error)
	}
}

func TestClientIPExtraction(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.10:5555"
	if got := ClientIP(req); got != "192.168.1.10" {
		t.Fatalf("got %q, want 192.168.1.10", got)
	}
	// Without trusted proxies, forwarded headers must be ignored.
	req.Header.Set("X-Real-IP", "203.0.113.9")
	if got := ClientIP(req); got != "192.168.1.10" {
		t.Fatalf("untrusted X-Real-IP: got %q, want 192.168.1.10", got)
	}
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
	if got := ClientIP(req); got != "192.168.1.10" {
		t.Fatalf("untrusted XFF: got %q, want 192.168.1.10", got)
	}

	// With trusted proxy CIDR, scan XFF right-to-left and pick nearest untrusted IP.
	tp := NewTrustedProxies("192.168.1.0/24", nil)
	if got := tp.ClientIP(req); got != "203.0.113.9" {
		t.Fatalf("trusted XFF right-to-left: got %q, want 203.0.113.9", got)
	}
	req.Header.Del("X-Forwarded-For")
	if got := tp.ClientIP(req); got != "203.0.113.9" {
		t.Fatalf("trusted X-Real-IP: got %q, want 203.0.113.9", got)
	}
}
