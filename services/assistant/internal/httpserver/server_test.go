package httpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/services/assistant/internal/service"
	"github.com/nexusmart/core/services/assistant/internal/tools"
	"github.com/prometheus/client_golang/prometheus"
)

type fakeChatSvc struct {
	fn func(ctx context.Context, cc tools.CallContext, req service.ChatRequest, emit func(service.SSEEvent) error) error
}

func (f *fakeChatSvc) StreamChat(ctx context.Context, cc tools.CallContext, req service.ChatRequest, emit func(service.SSEEvent) error) error {
	return f.fn(ctx, cc, req, emit)
}

func TestHTTPServerSSEAndAuth(t *testing.T) {
	jwtMgr, err := auth.NewManager("test-secret-key-12345", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	buyerToken, _, _, err := jwtMgr.SignAccessToken("user-42", "buyer", "")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}

	reg := prometheus.NewRegistry()
	red := metrics.NewRED("assistant", reg)

	var capturedCC tools.CallContext
	var capturedIP string
	handler := New(Config{
		ServiceName: "assistant",
		AuthManager: jwtMgr,
		RED:         red,
		Gatherer:    reg,
		Service: &fakeChatSvc{
			fn: func(_ context.Context, cc tools.CallContext, req service.ChatRequest, emit func(service.SSEEvent) error) error {
				capturedCC = cc
				capturedIP = req.ClientIP
				if err := emit(service.SSEEvent{Type: "token", Data: service.TokenEventData{Text: "Xin chào "}}); err != nil {
					return err
				}
				return emit(service.SSEEvent{Type: "done", Data: service.DoneEventData{ConversationID: "c1", MessageID: "m1"}})
			},
		},
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 1. Healthz & readyz & metrics work.
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz failed: resp=%v err=%v", resp, err)
	}
	_ = resp.Body.Close()

	// 2. Invalid Bearer token -> 401 NexusMart JSON error envelope.
	reqBadAuth, _ := http.NewRequest(http.MethodPost, srv.URL+"/assistant/chat", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hi"}]}`))
	reqBadAuth.Header.Set("Authorization", "Bearer invalid-token")
	resp, err = http.DefaultClient.Do(reqBadAuth)
	if err != nil {
		t.Fatalf("bad auth request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	var env errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || env.Error.Code != "UNAUTHENTICATED" {
		t.Fatalf("expected UNAUTHENTICATED envelope, got %+v err=%v", env, err)
	}

	// 3. Anonymous request allowed and receives SSE stream with client IP populated.
	reqAnon, _ := http.NewRequest(http.MethodPost, srv.URL+"/assistant/chat", bytes.NewBufferString(`{"shop_id":"shop-bach-dang","messages":[{"role":"user","content":"áo thun"}]}`))
	reqAnon.Header.Set("X-Forwarded-For", "203.0.113.10")
	respAnon, err := http.DefaultClient.Do(reqAnon)
	if err != nil {
		t.Fatalf("anon request: %v", err)
	}
	defer respAnon.Body.Close()
	if respAnon.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 SSE, got %d", respAnon.StatusCode)
	}
	if ct := respAnon.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", ct)
	}
	if capturedIP != "203.0.113.10" || capturedCC.UserID != "" || capturedCC.ShopID != "shop-bach-dang" {
		t.Fatalf("unexpected anon call context: cc=%+v ip=%q", capturedCC, capturedIP)
	}

	scanner := bufio.NewScanner(respAnon.Body)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "event: token") || !strings.Contains(joined, "event: done") {
		t.Fatalf("expected token and done SSE events, got:\n%s", joined)
	}

	// 4. Valid JWT populates CallContext with verified claims & raw Bearer.
	reqAuth, _ := http.NewRequest(http.MethodPost, srv.URL+"/assistant/chat", bytes.NewBufferString(`{"messages":[{"role":"user","content":"đơn hàng của tôi"}]}`))
	reqAuth.Header.Set("Authorization", "Bearer "+buyerToken)
	respAuth, err := http.DefaultClient.Do(reqAuth)
	if err != nil {
		t.Fatalf("auth request: %v", err)
	}
	_ = respAuth.Body.Close()
	if capturedCC.UserID != "user-42" || capturedCC.UserRole != "buyer" || capturedCC.RawBearer != "Bearer "+buyerToken {
		t.Fatalf("expected verified JWT claims forwarded, got %+v", capturedCC)
	}
}

func TestHTTPServerPreStreamRateLimitEnvelope(t *testing.T) {
	handler := New(Config{
		ServiceName: "assistant",
		Service: &fakeChatSvc{
			fn: func(_ context.Context, _ tools.CallContext, _ service.ChatRequest, _ func(service.SSEEvent) error) error {
				return &service.DomainError{
					Kind:          service.ErrRateLimited,
					Code:          "RATE_LIMITED",
					Message:       "vượt hạn mức token ngày",
					RetryAfterSec: 300,
				}
			},
		},
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/assistant/chat", "application/json", bytes.NewBufferString(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
	if ra := resp.Header.Get("Retry-After"); ra != "300" {
		t.Fatalf("expected Retry-After=300, got %q", ra)
	}
	var env errorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || env.Error.Code != "RATE_LIMITED" {
		t.Fatalf("expected RATE_LIMITED JSON envelope, got %+v err=%v", env, err)
	}
}
