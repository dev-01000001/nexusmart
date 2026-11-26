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

func (m *memRevoker) IsTokenRevoked(_ context.Context, claims *auth.Claims) (bool, error) {
	if claims == nil {
		return false, nil
	}
	return m.revoked[claims.JTI()], nil
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
	// Default (empty TRUSTED_PROXY_CIDRS / nil TrustedProxies): MUST ignore XFF and X-Real-IP.
	emptyTrusted, err := ParseTrustedProxies("")
	if err != nil {
		t.Fatalf("ParseTrustedProxies empty: %v", err)
	}
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
	if got := emptyTrusted.ClientIP(req); got != "192.168.1.10" {
		t.Fatalf("emptyTrusted.ClientIP got %q, want 192.168.1.10", got)
	}

	// With trusted proxy CIDR via NewTrustedProxies, scan XFF right-to-left and pick nearest untrusted IP.
	tp := NewTrustedProxies("192.168.1.0/24", nil)
	if got := tp.ClientIP(req); got != "203.0.113.9" {
		t.Fatalf("trusted XFF right-to-left: got %q, want 203.0.113.9", got)
	}
	req.Header.Del("X-Forwarded-For")
	if got := tp.ClientIP(req); got != "203.0.113.9" {
		t.Fatalf("trusted X-Real-IP: got %q, want 203.0.113.9", got)
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
	for _, bad := range []string{"10.0.0.0/99", "172.31.250.0/28, bad/cidr", "bad host"} {
		if _, err := NewTrustedProxiesValidated(bad, nil); err == nil {
			t.Fatalf("expected error for invalid trusted proxy entry %q, got nil", bad)
		}
	}
	if tp, err := NewTrustedProxiesValidated("172.31.250.0/28, 10.0.0.1, web", nil); err != nil || tp == nil {
		t.Fatalf("expected valid CIDR/IP/hostname list to parse, got err=%v", err)
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
	h := RateLimitByIP(lim, 2, log, trusted)(okHandler)
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
	h2 := RateLimitByIP(lim2, 2, log, trusted)(okHandler)
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

func TestRateLimitByIP_BFFBucketAndSecurityBoundaries(t *testing.T) {
	log := logger.New("gw-test", logger.WithWriter(io.Discard))
	trusted, err := ParseTrustedProxies("172.31.250.0/28")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	lim := newMemRateLimiter()
	h := RateLimitByIP(lim, 2, log, trusted, 5)(okHandler)

	// 1. Trusted peer WITHOUT X-Forwarded-For or X-Real-IP -> dedicated rl:bff:<peer> bucket with bffLimit=5.
	for i := 1; i <= 6; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sites/by-host?host=a.nexusmart.localhost", nil)
		req.RemoteAddr = "172.31.250.3:50000"
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 6 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted BFF req %d: got %d, want %d", i, rec.Code, want)
		}
		if gotLimit := rec.Header().Get("X-RateLimit-Limit"); gotLimit != "5" {
			t.Fatalf("trusted BFF req %d: X-RateLimit-Limit = %q, want 5", i, gotLimit)
		}
	}
	if lim.counts["rl:bff:172.31.250.3"] != 5 {
		t.Fatalf("expected rl:bff:172.31.250.3 count=5, got %d (all=%v)", lim.counts["rl:bff:172.31.250.3"], lim.counts)
	}

	// 2. Trusted IPv4-mapped IPv6 peer without forwarded headers -> normalized rl:bff:<ipv4> for GET/HEAD.
	recV6 := httptest.NewRecorder()
	reqV6 := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	reqV6.RemoteAddr = "[::ffff:172.31.250.4]:50001"
	h.ServeHTTP(recV6, reqV6)
	recHead := httptest.NewRecorder()
	reqHead := httptest.NewRequest(http.MethodHead, "/api/v1/products", nil)
	reqHead.RemoteAddr = "172.31.250.4:50001"
	h.ServeHTTP(recHead, reqHead)
	if recV6.Code != http.StatusOK || recHead.Code != http.StatusOK || lim.counts["rl:bff:172.31.250.4"] != 2 {
		t.Fatalf("trusted BFF GET/HEAD: codeV6=%d codeHead=%d counts=%v", recV6.Code, recHead.Code, lim.counts)
	}
	if gotLimit := recHead.Header().Get("X-RateLimit-Limit"); gotLimit != "5" {
		t.Fatalf("trusted BFF HEAD: X-RateLimit-Limit = %q, want 5", gotLimit)
	}

	// 2b. Trusted peer WITHOUT XFF/X-Real-IP on non-GET/HEAD methods (POST, PUT, PATCH, DELETE)
	//     MUST stay on rl:ip:<peer> with ipLimit=2 (fail-closed; never elevated to rl:bff).
	for _, tc := range []struct {
		method string
		path   string
		peer   string
		want   string
	}{
		{http.MethodPost, "/api/v1/auth/login", "172.31.250.8:50010", "172.31.250.8"},
		{http.MethodPut, "/api/v1/users/me/addresses/1", "172.31.250.9:50011", "172.31.250.9"},
		{http.MethodPatch, "/api/v1/users/me", "172.31.250.10:50012", "172.31.250.10"},
		{http.MethodDelete, "/api/v1/cart/items/sku-1", "172.31.250.11:50013", "172.31.250.11"},
	} {
		for i := 1; i <= 3; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.RemoteAddr = tc.peer
			h.ServeHTTP(rec, req)
			wantCode := http.StatusOK
			if i == 3 {
				wantCode = http.StatusTooManyRequests
			}
			if rec.Code != wantCode {
				t.Fatalf("trusted peer %s without XFF req %d: got %d, want %d", tc.method, i, rec.Code, wantCode)
			}
			if gotLimit := rec.Header().Get("X-RateLimit-Limit"); gotLimit != "2" {
				t.Fatalf("trusted peer %s without XFF req %d: X-RateLimit-Limit = %q, want 2", tc.method, i, gotLimit)
			}
		}
		if lim.counts["rl:ip:"+tc.want] != 2 || lim.counts["rl:bff:"+tc.want] != 0 {
			t.Fatalf("trusted peer %s without XFF must use rl:ip:%s, got counts=%v", tc.method, tc.want, lim.counts)
		}
	}

	// 3. Trusted peer WITH X-Forwarded-For -> still counted per visitor IP with ipLimit=2, never BFF bucket.
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "172.31.250.3:50002"
		req.Header.Set("X-Forwarded-For", "203.0.113.10")
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted peer with XFF req %d: got %d, want %d", i, rec.Code, want)
		}
		if gotLimit := rec.Header().Get("X-RateLimit-Limit"); gotLimit != "2" {
			t.Fatalf("trusted peer with XFF req %d: X-RateLimit-Limit = %q, want 2", i, gotLimit)
		}
	}
	if lim.counts["rl:ip:203.0.113.10"] != 2 {
		t.Fatalf("expected rl:ip:203.0.113.10 count=2, got %d", lim.counts["rl:ip:203.0.113.10"])
	}

	// 4. Trusted peer WITH X-Real-IP only -> counted per visitor IP with ipLimit=2, never BFF bucket.
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "172.31.250.3:50003"
		req.Header.Set("X-Real-IP", "203.0.113.11")
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted peer with X-Real-IP req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if lim.counts["rl:ip:203.0.113.11"] != 2 {
		t.Fatalf("expected rl:ip:203.0.113.11 count=2, got %d", lim.counts["rl:ip:203.0.113.11"])
	}

	// 5. Trusted peer WITH malformed X-Forwarded-For -> MUST NOT enter BFF bucket (fail closed to ipLimit=2).
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "172.31.250.5:50004"
		req.Header.Set("X-Forwarded-For", "not-an-ip")
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted peer with malformed XFF req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if lim.counts["rl:bff:172.31.250.5"] != 0 || lim.counts["rl:ip:172.31.250.5"] != 2 {
		t.Fatalf("malformed XFF must stay on rl:ip bucket, got counts=%v", lim.counts)
	}

	// 6. Untrusted peer WITHOUT headers -> regular rl:ip bucket with ipLimit=2 (never BFF bucket).
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "198.51.100.5:40000"
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("untrusted peer no headers req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if lim.counts["rl:bff:198.51.100.5"] != 0 || lim.counts["rl:ip:198.51.100.5"] != 2 {
		t.Fatalf("untrusted peer must never enter BFF bucket, got counts=%v", lim.counts)
	}

	// 7. Untrusted client spoofing X-Forwarded-For (even with a trusted proxy IP) -> stays on rl:ip:<remoteAddr> with ipLimit=2.
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
		req.RemoteAddr = "198.51.100.6:40001"
		req.Header.Set("X-Forwarded-For", "172.31.250.3")
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("untrusted spoofed XFF req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if lim.counts["rl:ip:198.51.100.6"] != 2 {
		t.Fatalf("untrusted spoofed XFF must stay on rl:ip:198.51.100.6, got counts=%v", lim.counts)
	}

	// 8. Dedicated strict limiters (auth, order-lookup, shipping-fee, flash-sale, webhook)
	//    MUST NOT use the BFF bucket even for trusted peers without XFF/X-Real-IP.
	for _, tc := range []struct {
		name    string
		wantKey string
	}{
		{"auth", "rl:auth:ip:172.31.250.7"},
		{"order_lookup", "rl:order_lookup:ip:172.31.250.7"},
		{"shipping_fee", "rl:shipping_fee:ip:172.31.250.7"},
		{"flash_sale", "rl:flash:ip:172.31.250.7"},
		{"webhook", "rl:webhook:ip:172.31.250.7"},
	} {
		t.Run("strict_"+tc.name, func(t *testing.T) {
			strictLim := newMemRateLimiter()
			var wrapped http.Handler
			switch tc.name {
			case "auth":
				wrapped = RateLimitAuth(strictLim, 2, log, trusted)(okHandler)
			case "order_lookup":
				wrapped = RateLimitOrderLookupIP(strictLim, 2, log, trusted)(okHandler)
			case "shipping_fee":
				wrapped = RateLimitShippingFee(strictLim, 2, log, trusted)(okHandler)
			case "flash_sale":
				wrapped = RateLimitFlashSale(strictLim, 2, log, trusted)(okHandler)
			case "webhook":
				wrapped = RateLimitWebhook(strictLim, 2, log, trusted)(okHandler)
			}
			for i := 1; i <= 3; i++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/strict", nil)
				req.RemoteAddr = "172.31.250.7:55555"
				wrapped.ServeHTTP(rec, req)
				want := http.StatusOK
				if i == 3 {
					want = http.StatusTooManyRequests
				}
				if rec.Code != want {
					t.Fatalf("%s req %d: got %d, want %d", tc.name, i, rec.Code, want)
				}
			}
			if strictLim.counts[tc.wantKey] != 2 || strictLim.counts["rl:bff:172.31.250.7"] != 0 {
				t.Fatalf("%s unexpected keys: %v", tc.name, strictLim.counts)
			}
		})
	}
}

func TestRateLimitFlashSale_BFFBucketAndSecurityBoundaries(t *testing.T) {
	log := logger.New("gw-test", logger.WithWriter(io.Discard))
	trusted, err := ParseTrustedProxies("172.31.250.0/28")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	authMgr, err := auth.NewManager("gw-flash-rl-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, err := authMgr.SignAccessToken("buyer-flash-1", auth.RoleBuyer, "")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	lim := newMemRateLimiter()
	h := Auth(authMgr, nil, log, false)(RateLimitFlashSale(lim, 2, log, trusted, 5)(okHandler))

	// 1. Trusted peer, anonymous, WITHOUT XFF or X-Real-IP -> dedicated rl:flash:bff:<peer> bucket with bffLimit=5.
	for i := 1; i <= 6; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales?include_upcoming=false", nil)
		req.RemoteAddr = "172.31.250.3:50000"
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 6 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted BFF flash req %d: got %d, want %d", i, rec.Code, want)
		}
		if gotLimit := rec.Header().Get("X-RateLimit-Limit"); gotLimit != "5" {
			t.Fatalf("trusted BFF flash req %d: X-RateLimit-Limit = %q, want 5", i, gotLimit)
		}
	}
	if lim.counts["rl:flash:bff:172.31.250.3"] != 5 {
		t.Fatalf("expected rl:flash:bff:172.31.250.3 count=5, got %d (all=%v)", lim.counts["rl:flash:bff:172.31.250.3"], lim.counts)
	}

	// 2. Trusted IPv4-mapped IPv6 peer, anonymous, no forwarded headers -> normalized rl:flash:bff:<ipv4> for GET/HEAD.
	recV6 := httptest.NewRecorder()
	reqV6 := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales", nil)
	reqV6.RemoteAddr = "[::ffff:172.31.250.4]:50001"
	h.ServeHTTP(recV6, reqV6)
	recHead := httptest.NewRecorder()
	reqHead := httptest.NewRequest(http.MethodHead, "/api/v1/flash-sales", nil)
	reqHead.RemoteAddr = "172.31.250.4:50001"
	h.ServeHTTP(recHead, reqHead)
	if recV6.Code != http.StatusOK || recHead.Code != http.StatusOK || lim.counts["rl:flash:bff:172.31.250.4"] != 2 {
		t.Fatalf("trusted BFF flash GET/HEAD: codeV6=%d codeHead=%d counts=%v", recV6.Code, recHead.Code, lim.counts)
	}

	// 2b. Trusted peer, anonymous, WITHOUT XFF/X-Real-IP on POST -> stays on rl:flash:ip:<peer> with limit=2 (fail-closed).
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/flash-sales", nil)
		req.RemoteAddr = "172.31.250.8:50010"
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted peer POST flash without XFF req %d: got %d, want %d", i, rec.Code, want)
		}
		if gotLimit := rec.Header().Get("X-RateLimit-Limit"); gotLimit != "2" {
			t.Fatalf("trusted peer POST flash without XFF req %d: X-RateLimit-Limit = %q, want 2", i, gotLimit)
		}
	}
	if lim.counts["rl:flash:ip:172.31.250.8"] != 2 || lim.counts["rl:flash:bff:172.31.250.8"] != 0 {
		t.Fatalf("trusted peer POST flash without XFF must stay on rl:flash:ip, got counts=%v", lim.counts)
	}

	// 3. Trusted peer, anonymous, WITH X-Forwarded-For -> stays on rl:flash:ip:<client> with limit=2.
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales", nil)
		req.RemoteAddr = "172.31.250.3:50002"
		req.Header.Set("X-Forwarded-For", "203.0.113.10")
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted peer with XFF flash req %d: got %d, want %d", i, rec.Code, want)
		}
		if gotLimit := rec.Header().Get("X-RateLimit-Limit"); gotLimit != "2" {
			t.Fatalf("trusted peer with XFF flash req %d: X-RateLimit-Limit = %q, want 2", i, gotLimit)
		}
	}
	if lim.counts["rl:flash:ip:203.0.113.10"] != 2 {
		t.Fatalf("expected rl:flash:ip:203.0.113.10 count=2, got %d", lim.counts["rl:flash:ip:203.0.113.10"])
	}

	// 4. Trusted peer, anonymous, WITH X-Real-IP -> stays on rl:flash:ip:<client> with limit=2.
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales", nil)
		req.RemoteAddr = "172.31.250.3:50003"
		req.Header.Set("X-Real-IP", "203.0.113.11")
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted peer with X-Real-IP flash req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if lim.counts["rl:flash:ip:203.0.113.11"] != 2 {
		t.Fatalf("expected rl:flash:ip:203.0.113.11 count=2, got %d", lim.counts["rl:flash:ip:203.0.113.11"])
	}

	// 5. Trusted peer, anonymous, WITH malformed XFF -> MUST NOT enter BFF bucket (fail closed to limit=2).
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales", nil)
		req.RemoteAddr = "172.31.250.5:50004"
		req.Header.Set("X-Forwarded-For", "not-an-ip")
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("trusted peer malformed XFF flash req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if lim.counts["rl:flash:bff:172.31.250.5"] != 0 || lim.counts["rl:flash:ip:172.31.250.5"] != 2 {
		t.Fatalf("malformed XFF must stay on rl:flash:ip bucket, got counts=%v", lim.counts)
	}

	// 6. Authenticated user from trusted peer WITHOUT XFF -> stays on rl:flash:user:<uid> with limit=2 (never BFF bucket).
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales", nil)
		req.RemoteAddr = "172.31.250.6:50005"
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("authenticated flash req %d: got %d, want %d", i, rec.Code, want)
		}
		if gotLimit := rec.Header().Get("X-RateLimit-Limit"); gotLimit != "2" {
			t.Fatalf("authenticated flash req %d: X-RateLimit-Limit = %q, want 2", i, gotLimit)
		}
	}
	if lim.counts["rl:flash:user:buyer-flash-1"] != 2 || lim.counts["rl:flash:bff:172.31.250.6"] != 0 {
		t.Fatalf("authenticated request must use rl:flash:user bucket, got counts=%v", lim.counts)
	}

	// 7. Untrusted peer without headers or spoofing XFF with trusted peer IP -> stays on rl:flash:ip:<remoteAddr> with limit=2.
	for _, tc := range []struct {
		name string
		peer string
		xff  string
		want string
	}{
		{"untrusted_no_headers", "198.51.100.5:40000", "", "rl:flash:ip:198.51.100.5"},
		{"untrusted_spoofed_xff", "198.51.100.6:40001", "172.31.250.3", "rl:flash:ip:198.51.100.6"},
	} {
		for i := 1; i <= 3; i++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales", nil)
			req.RemoteAddr = tc.peer
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(rec, req)
			wantCode := http.StatusOK
			if i == 3 {
				wantCode = http.StatusTooManyRequests
			}
			if rec.Code != wantCode {
				t.Fatalf("%s req %d: got %d, want %d", tc.name, i, rec.Code, wantCode)
			}
		}
		if lim.counts[tc.want] != 2 {
			t.Fatalf("%s expected %s count=2, got %v", tc.name, tc.want, lim.counts)
		}
	}
}
