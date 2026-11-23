package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
)

func TestClientIPDefaultIgnoresSpoofedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
	req.RemoteAddr = "192.168.1.10:5555"
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 203.0.113.9")
	req.Header.Set("X-Real-IP", "203.0.113.9")

	if got := ClientIP(req); got != "192.168.1.10" {
		t.Fatalf("default ClientIP must ignore XFF/X-Real-IP without trusted proxies: got %q, want %q", got, "192.168.1.10")
	}
}

func TestTrustedProxiesClientIPAndMultiHopXFF(t *testing.T) {
	emptyTP := NewTrustedProxies("", nil)
	reqEmpty := httptest.NewRequest(http.MethodGet, "/", nil)
	reqEmpty.RemoteAddr = "172.20.0.5:4321"
	reqEmpty.Header.Set("X-Forwarded-For", "198.51.100.1")
	if got := emptyTP.ClientIP(reqEmpty); got != "172.20.0.5" {
		t.Fatalf("empty TRUSTED_PROXIES must return RemoteAddr: got %q", got)
	}

	tp := NewTrustedProxies("10.0.0.0/8, 172.20.0.9", nil)

	// Untrusted RemoteAddr cannot spoof XFF or X-Real-IP.
	reqUntrusted := httptest.NewRequest(http.MethodGet, "/", nil)
	reqUntrusted.RemoteAddr = "198.51.100.50:9999"
	reqUntrusted.Header.Set("X-Forwarded-For", "203.0.113.1")
	reqUntrusted.Header.Set("X-Real-IP", "203.0.113.2")
	if got := tp.ClientIP(reqUntrusted); got != "198.51.100.50" {
		t.Fatalf("untrusted peer must ignore forwarded headers: got %q, want 198.51.100.50", got)
	}

	// Trusted peer + multi-hop XFF with spoofed leftmost hop and trusted inner hop:
	// "1.1.1.1 (spoofed), 203.0.113.42 (real client), 10.0.0.2 (trusted edge)" -> must pick 203.0.113.42!
	reqMulti := httptest.NewRequest(http.MethodGet, "/", nil)
	reqMulti.RemoteAddr = "172.20.0.9:51234"
	reqMulti.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.42, 10.0.0.2")
	if got := tp.ClientIP(reqMulti); got != "203.0.113.42" {
		t.Fatalf("multi-hop XFF right-to-left: got %q, want 203.0.113.42", got)
	}

	// Multiple X-Forwarded-For header lines are evaluated in order right-to-left.
	reqSplitHdr := httptest.NewRequest(http.MethodGet, "/", nil)
	reqSplitHdr.RemoteAddr = "172.20.0.9:51234"
	reqSplitHdr.Header.Add("X-Forwarded-For", "1.1.1.1, 198.51.100.88")
	reqSplitHdr.Header.Add("X-Forwarded-For", "10.0.0.7")
	if got := tp.ClientIP(reqSplitHdr); got != "198.51.100.88" {
		t.Fatalf("split XFF headers right-to-left: got %q, want 198.51.100.88", got)
	}

	// Malformed rightmost hop stops traversal safely and falls back to peer IP.
	reqBadHop := httptest.NewRequest(http.MethodGet, "/", nil)
	reqBadHop.RemoteAddr = "172.20.0.9:51234"
	reqBadHop.Header.Set("X-Forwarded-For", "203.0.113.1, not-an-ip")
	if got := tp.ClientIP(reqBadHop); got != "172.20.0.9" {
		t.Fatalf("malformed XFF hop must fall back to peer IP: got %q, want 172.20.0.9", got)
	}

	// Trusted peer with X-Real-IP only (no XFF).
	reqRealIP := httptest.NewRequest(http.MethodGet, "/", nil)
	reqRealIP.RemoteAddr = "10.0.0.5:8080"
	reqRealIP.Header.Set("X-Real-IP", "203.0.113.77")
	if got := tp.ClientIP(reqRealIP); got != "203.0.113.77" {
		t.Fatalf("trusted peer X-Real-IP: got %q, want 203.0.113.77", got)
	}

	// IPv4-mapped IPv6 peer matches IPv4 trusted proxy.
	reqMapped := httptest.NewRequest(http.MethodGet, "/", nil)
	reqMapped.RemoteAddr = "[::ffff:172.20.0.9]:8080"
	reqMapped.Header.Set("X-Forwarded-For", "198.51.100.99")
	if got := tp.ClientIP(reqMapped); got != "198.51.100.99" {
		t.Fatalf("ipv4-mapped ipv6 peer: got %q, want 198.51.100.99", got)
	}
}

func TestTrustedProxiesHostnamePeriodicResolution(t *testing.T) {
	var (
		mu       sync.Mutex
		now      = time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
		resolved []netip.Addr
		dnsErr   = errors.New("nxdomain: web not up yet")
		lookups  int
	)

	tp := &TrustedProxies{
		hosts: []string{"web"},
		lookupFn: func(_ context.Context, host string) ([]netip.Addr, error) {
			mu.Lock()
			defer mu.Unlock()
			lookups++
			if host != "web" {
				return nil, errors.New("unexpected host")
			}
			if dnsErr != nil {
				return nil, dnsErr
			}
			return append([]netip.Addr(nil), resolved...), nil
		},
		nowFn: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		refreshTTL: 30 * time.Second,
		retryTTL:   2 * time.Second,
		missTTL:    5 * time.Second,
		resolved:   make(map[netip.Addr]struct{}),
	}
	tp.refresh(false)

	// 1. Before web boots, peer 172.20.0.10 is not trusted yet.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.20.0.10:40000"
	req.Header.Set("X-Forwarded-For", "198.51.100.21")
	if got := tp.ClientIP(req); got != "172.20.0.10" {
		t.Fatalf("before DNS resolves web: got %q, want 172.20.0.10", got)
	}

	// 2. Web boots at 172.20.0.10; after retryTTL (2s), next check resolves and trusts it.
	mu.Lock()
	dnsErr = nil
	resolved = []netip.Addr{netip.MustParseAddr("172.20.0.10")}
	now = now.Add(3 * time.Second)
	mu.Unlock()

	if got := tp.ClientIP(req); got != "198.51.100.21" {
		t.Fatalf("after web resolves to 172.20.0.10: got %q, want 198.51.100.21", got)
	}

	// 3. Web recreates at new IP 172.20.0.11; after refreshTTL (30s), old IP is no longer trusted and new IP is trusted.
	mu.Lock()
	resolved = []netip.Addr{netip.MustParseAddr("172.20.0.11")}
	now = now.Add(31 * time.Second)
	mu.Unlock()

	if got := tp.ClientIP(req); got != "172.20.0.10" {
		t.Fatalf("old web IP after rotation must not be trusted: got %q, want 172.20.0.10", got)
	}

	reqNew := httptest.NewRequest(http.MethodGet, "/", nil)
	reqNew.RemoteAddr = "172.20.0.11:40001"
	reqNew.Header.Set("X-Forwarded-For", "198.51.100.22")
	if got := tp.ClientIP(reqNew); got != "198.51.100.22" {
		t.Fatalf("new web IP after rotation must be trusted: got %q, want 198.51.100.22", got)
	}
}

func TestRateLimitByIPBlocksSpoofedXFFFromUntrustedPeer(t *testing.T) {
	log := logger.New("gw-test", logger.WithWriter(io.Discard))
	lim := newMemRateLimiter()
	tp := NewTrustedProxies("10.0.0.1", log)

	h := RateLimitByIP(lim, 2, log, tp)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Untrusted attacker varies XFF on every request -> still hits the same RemoteAddr bucket and gets 429 on 3rd.
	for i, fakeIP := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
		req.RemoteAddr = "203.0.113.5:12345"
		req.Header.Set("X-Forwarded-For", fakeIP)
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 2 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("req %d (spoofed XFF %s): got %d, want %d", i+1, fakeIP, rec.Code, want)
		}
	}
}

func TestRequestLoggerRecordsUserIDWhenAuthRunsInside(t *testing.T) {
	authMgr, err := auth.NewManager("logger-uid-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	tok, _, _, err := authMgr.SignAccessToken("user-42", "buyer", "")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}

	var buf bytes.Buffer
	log := logger.New("gateway-test", logger.WithWriter(&buf))

	// Mirrors production router order: RequestLogger wraps Auth.
	h := RequestLogger(log, nil)(Auth(authMgr, nil, log, true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("decode log json %q: %v", buf.String(), err)
	}
	if entry["user_id"] != "user-42" {
		t.Fatalf("expected RequestLogger to record user_id=user-42, got log=%s", buf.String())
	}
}
