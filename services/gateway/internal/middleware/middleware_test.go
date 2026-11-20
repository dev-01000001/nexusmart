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
	h := RateLimitByIP(lim, 2, nil, log)(okHandler)
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
	disabled := RateLimitByIP(lim, 0, nil, log)(okHandler)
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
	// Default (empty TRUSTED_PROXY_CIDRS / nil TrustedProxies): MUST ignore XFF and X-Real-IP.
	emptyTrusted, err := ParseTrustedProxies("")
	if err != nil {
		t.Fatalf("ParseTrustedProxies empty: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.10:5555"
	req.Header.Set("X-Real-IP", "203.0.113.9")
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
	if got := ClientIP(req); got != "192.168.1.10" {
		t.Fatalf("ClientIP default got %q, want 192.168.1.10", got)
	}
	if got := emptyTrusted.ClientIP(req); got != "192.168.1.10" {
		t.Fatalf("emptyTrusted.ClientIP got %q, want 192.168.1.10", got)
	}

	// Configured trusted CIDRs: untrusted RemoteAddr still ignores XFF and X-Real-IP.
	trusted, err := ParseTrustedProxies("172.31.250.0/28, 10.10.0.0/16")
	if err != nil {
		t.Fatalf("ParseTrustedProxies valid: %v", err)
	}
	for _, tc := range []struct {
		peer string
		want string
	}{
		{peer: "172.30.13.1:54321", want: "172.30.13.1"},
		{peer: "172.30.13.17:4444", want: "172.30.13.17"},
		{peer: "203.0.113.50:8080", want: "203.0.113.50"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", "198.51.100.7")
		r.Header.Set("X-Real-IP", "203.0.113.9")
		if got := trusted.ClientIP(r); got != tc.want {
			t.Fatalf("untrusted peer %s: got %q, want %q", tc.peer, got, tc.want)
		}
	}

	// Trusted RemoteAddr: single XFF entry.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.31.250.3:50000"
	r.Header.Set("X-Forwarded-For", "203.0.113.10")
	if got := trusted.ClientIP(r); got != "203.0.113.10" {
		t.Fatalf("trusted single XFF got %q, want 203.0.113.10", got)
	}

	// Trusted RemoteAddr: spoofed left-most XFF MUST be ignored; pick right-most untrusted IP.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.31.250.3:50000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 198.51.100.7")
	if got := trusted.ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("trusted spoofed left-most XFF got %q, want 198.51.100.7", got)
	}

	// Multi-hop trusted proxies: skip right-most trusted proxy hops, pick right-most untrusted IP.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.31.250.3:50000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 198.51.100.7, 10.10.1.5, 172.31.250.4")
	if got := trusted.ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("multi-hop XFF got %q, want 198.51.100.7", got)
	}

	// Multiple X-Forwarded-For header lines combined in order.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.31.250.3:50000"
	r.Header.Add("X-Forwarded-For", "1.1.1.1")
	r.Header.Add("X-Forwarded-For", "198.51.100.8, 10.10.2.2")
	if got := trusted.ClientIP(r); got != "198.51.100.8" {
		t.Fatalf("multi-header XFF got %q, want 198.51.100.8", got)
	}

	// Trusted RemoteAddr with X-Real-IP fallback when XFF is absent.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.31.250.3:50000"
	r.Header.Set("X-Real-IP", "203.0.113.9")
	if got := trusted.ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("trusted X-Real-IP got %q, want 203.0.113.9", got)
	}

	// Trusted RemoteAddr with malformed XFF falls back to RemoteAddr (never trusts garbage).
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.31.250.3:50000"
	r.Header.Set("X-Forwarded-For", "198.51.100.7, not-an-ip")
	if got := trusted.ClientIP(r); got != "172.31.250.3" {
		t.Fatalf("malformed XFF got %q, want 172.31.250.3", got)
	}

	// IPv4-mapped IPv6 RemoteAddr matches IPv4 trusted CIDR.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[::ffff:172.31.250.3]:50000"
	r.Header.Set("X-Forwarded-For", "203.0.113.11")
	if got := trusted.ClientIP(r); got != "203.0.113.11" {
		t.Fatalf("ipv4-mapped ipv6 peer got %q, want 203.0.113.11", got)
	}

	// All hops trusted -> returns left-most valid hop.
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.31.250.3:50000"
	r.Header.Set("X-Forwarded-For", "10.10.0.5, 172.31.250.4")
	if got := trusted.ClientIP(r); got != "10.10.0.5" {
		t.Fatalf("all-trusted XFF got %q, want 10.10.0.5", got)
	}
}

func TestParseTrustedProxiesInvalidCIDR(t *testing.T) {
	for _, bad := range []string{"not-a-cidr", "10.0.0.1", "10.0.0.0/99", "172.31.250.0/28, bogus"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Fatalf("expected error for invalid CIDR %q, got nil", bad)
		}
	}
	tp, err := ParseTrustedProxies(" 172.31.250.5/28 , fd00::/8 , ")
	if err != nil {
		t.Fatalf("expected valid CIDRs to parse, got %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "[fd00::1]:443"
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	if got := tp.ClientIP(req); got != "203.0.113.42" {
		t.Fatalf("ipv6 trusted CIDR got %q, want 203.0.113.42", got)
	}
}

func TestRateLimitCannotBeBypassedBySpoofedXFF(t *testing.T) {
	log := logger.New("gw-test", logger.WithWriter(io.Discard))
	trusted, err := ParseTrustedProxies("172.31.250.0/28")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Untrusted caller rotates X-Forwarded-For & X-Real-IP on every request -> still hits 429 on 3rd req.
	lim := newMemRateLimiter()
	h := RateLimitByIP(lim, 2, trusted, log)(okHandler)
	for i, fakeIP := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "172.30.13.1:45000"
		req.Header.Set("X-Forwarded-For", fakeIP)
		req.Header.Set("X-Real-IP", fakeIP)
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("untrusted spoof req %d: got %d, want %d", i+1, rec.Code, want)
		}
	}

	// Trusted proxy with spoofed left-most IP rotating for same real client -> still hits 429 on 3rd req.
	lim2 := newMemRateLimiter()
	h2 := RateLimitByIP(lim2, 2, trusted, log)(okHandler)
	for i, fakeLeft := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "172.31.250.3:45000"
		req.Header.Set("X-Forwarded-For", fakeLeft+", 203.0.113.77")
		h2.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted spoofed-left req %d: got %d, want %d", i+1, rec.Code, want)
		}
	}
}
