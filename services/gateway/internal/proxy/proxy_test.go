package proxy

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
)

func TestWebhookProxyPreservesRawBodyAndHeaders(t *testing.T) {
	const secret = "dev-only-payment-webhook-hmac-secret"
	// Deliberately non-canonical whitespace to verify exact raw byte preservation.
	rawBody := []byte(`{  "provider": "vietqr_mock",   "amount_vnd": 250000, "provider_tx_id": "TXN-001" }`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	const expectedTS = "1763600000"
	const expectedTP = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	var gotPath, gotSig, gotTS, gotTP string
	var gotBody []byte

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSig = r.Header.Get("X-Signature")
		gotTS = r.Header.Get("X-Timestamp")
		gotTP = r.Header.Get("traceparent")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	log := logger.New("proxy-test", logger.WithWriter(io.Discard))
	wp, err := NewWebhookProxy(upstream.URL, log)
	if err != nil {
		t.Fatalf("NewWebhookProxy failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/webhooks/payment", bytes.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", expectedSig)
	req.Header.Set("X-Timestamp", expectedTS)
	req.Header.Set("traceparent", expectedTP)
	rec := httptest.NewRecorder()

	wp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if gotPath != "/webhooks/payment" {
		t.Fatalf("expected path /webhooks/payment, got %q", gotPath)
	}
	if !bytes.Equal(gotBody, rawBody) {
		t.Fatalf("raw body mutated: got %q want %q", gotBody, rawBody)
	}
	if gotSig != expectedSig || gotTS != expectedTS || gotTP != expectedTP {
		t.Fatalf("headers mismatch: sig=%q ts=%q tp=%q", gotSig, gotTS, gotTP)
	}
}

func TestSSEProxyRewritesPathAndFlushes(t *testing.T) {
	var gotPath string
	var gotHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("event: payment.succeeded\ndata: {\"payment_id\":\"pay-1\"}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	log := logger.New("proxy-test", logger.WithWriter(io.Discard))
	sp, err := NewSSEProxy(upstream.URL, log)
	if err != nil {
		t.Fatalf("NewSSEProxy failed: %v", err)
	}

	claims := &auth.Claims{Role: "seller", ShopID: "shop-42"}
	claims.Subject = "seller-1"
	const expectedAuth = "Bearer test-access-token"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay-1/events", nil)
	ctx := auth.WithClaims(req.Context(), claims)
	ctx = auth.WithRawToken(ctx, expectedAuth)
	req = req.WithContext(ctx)
	req.Header.Set("X-User-Id", "spoofed-user")
	req.Header.Set("X-User-Role", "admin")
	req.Header.Set("X-Shop-Id", "spoofed-shop")

	rec := httptest.NewRecorder()
	sp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if gotPath != "/payments/pay-1/events" {
		t.Fatalf("expected rewritten path /payments/pay-1/events, got %q", gotPath)
	}
	if !strings.Contains(rec.Body.String(), "event: payment.succeeded") {
		t.Fatalf("expected SSE payload, got %q", rec.Body.String())
	}
	for _, hdr := range []string{"X-User-Id", "X-User-Role", "X-Shop-Id"} {
		if vals := gotHeaders.Values(hdr); len(vals) != 0 {
			t.Fatalf("expected %s to be stripped from upstream request, got %v", hdr, vals)
		}
	}
	if gotAuth := gotHeaders.Get("Authorization"); gotAuth != expectedAuth {
		t.Fatalf("expected Authorization %q forwarded, got %q", expectedAuth, gotAuth)
	}
}

func TestShippingLabelProxyForwardsAuthAndStripsIdentityHeaders(t *testing.T) {
	var gotPath string
	var gotHeaders http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>Label GHN-123</body></html>"))
	}))
	defer upstream.Close()

	log := logger.New("proxy-test", logger.WithWriter(io.Discard))
	lp, err := NewShippingLabelProxy(upstream.URL, log)
	if err != nil {
		t.Fatalf("NewShippingLabelProxy failed: %v", err)
	}

	const expectedAuth = "Bearer test-seller-token"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shipping/labels/GHN-123", nil)
	ctx := auth.WithRawToken(req.Context(), expectedAuth)
	req = req.WithContext(ctx)
	req.Header.Set("X-User-Id", "spoofed")
	req.Header.Set("X-User-Role", "admin")
	req.Header.Set("X-Shop-Id", "spoofed-shop")

	rec := httptest.NewRecorder()
	lp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if gotPath != "/api/v1/shipping/labels/GHN-123" {
		t.Fatalf("expected path /api/v1/shipping/labels/GHN-123, got %q", gotPath)
	}
	if !strings.Contains(rec.Body.String(), "Label GHN-123") {
		t.Fatalf("expected label HTML body, got %q", rec.Body.String())
	}
	for _, hdr := range []string{"X-User-Id", "X-User-Role", "X-Shop-Id"} {
		if vals := gotHeaders.Values(hdr); len(vals) != 0 {
			t.Fatalf("expected %s to be stripped, got %v", hdr, vals)
		}
	}
	if gotAuth := gotHeaders.Get("Authorization"); gotAuth != expectedAuth {
		t.Fatalf("expected Authorization %q forwarded, got %q", expectedAuth, gotAuth)
	}
	const wantLabelCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'"
	if gotCSP := rec.Header().Values("Content-Security-Policy"); len(gotCSP) != 1 || gotCSP[0] != wantLabelCSP {
		t.Fatalf("Content-Security-Policy values = %q, want single [%q]", gotCSP, wantLabelCSP)
	}
	if gotNosniff := rec.Header().Values("X-Content-Type-Options"); len(gotNosniff) != 1 || gotNosniff[0] != "nosniff" {
		t.Fatalf("X-Content-Type-Options values = %q, want single [nosniff]", gotNosniff)
	}
}
