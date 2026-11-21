package proxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/telemetry"
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
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay-1/events", nil)
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
}

func TestWebhookProxyPropagatesChildSpanAndTracestate(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-proxy-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	const parentTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const parentSpanID = "00f067aa0ba902b7"
	const incomingTP = "00-" + parentTraceID + "-" + parentSpanID + "-01"
	const incomingTS = "vendor=ghn"

	var gotTP, gotTS string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTP = r.Header.Get("traceparent")
		gotTS = r.Header.Get("tracestate")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	log := logger.New("proxy-test", logger.WithWriter(io.Discard))
	wp, err := NewWebhookProxy(upstream.URL, log)
	if err != nil {
		t.Fatalf("NewWebhookProxy: %v", err)
	}
	h := telemetry.HTTPMiddleware("gateway")(wp)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/shipping/ghn", strings.NewReader(`{"status":"delivered"}`))
	req.Header.Set("traceparent", incomingTP)
	req.Header.Set("tracestate", incomingTS)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	parts := strings.Split(gotTP, "-")
	if len(parts) != 4 {
		t.Fatalf("invalid upstream traceparent %q", gotTP)
	}
	if parts[1] != parentTraceID {
		t.Fatalf("upstream trace_id = %q, want %q", parts[1], parentTraceID)
	}
	if parts[2] == parentSpanID {
		t.Fatalf("upstream traceparent kept external span ID %q instead of gateway child span", gotTP)
	}
	if gotTS != incomingTS {
		t.Fatalf("upstream tracestate = %q, want %q", gotTS, incomingTS)
	}
}
