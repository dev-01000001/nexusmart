package router

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatv1 "github.com/nexusmart/core/gen/go/nexusmart/chat/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"github.com/nexusmart/core/services/gateway/internal/proxy"
	"google.golang.org/grpc"
)

func TestMerge_MiddlewareCompletenessWebhooksSSELabelSearchChatAssistant(t *testing.T) {
	authMgr, err := auth.NewManager("merge-mw-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-mw-1", auth.RoleBuyer, "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-mw-1", auth.RoleSeller, "shop-mw-1")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-mw-1", auth.RoleAdmin, "")

	// Assistant SSE upstream intentionally sends duplicate security headers to verify gateway strips them.
	assistSSEUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Content-Type-Options", "upstream-nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: token\ndata: {\"text\":\"hi\"}\n\n"))
	}))
	defer assistSSEUpstream.Close()

	assistantMock := &fakeAssistantClient{}
	restoreAssist := setAssistantTestUpstream(assistantMock, assistSSEUpstream.URL)
	t.Cleanup(restoreAssist)

	chatMock := &fakeChatClient{
		tickets: map[string]*chatv1.ExchangeWSTicketResponse{},
	}
	restoreChat := setChatTestUpstream(chatMock, assistSSEUpstream.URL)
	t.Cleanup(restoreChat)

	webhookUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer webhookUpstream.Close()

	paySSEUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: ping\ndata: {}\n\n"))
	}))
	defer paySSEUpstream.Close()

	labelUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>label</html>"))
	}))
	defer labelUpstream.Close()

	log := logger.New("merge-mw-test", logger.WithWriter(io.Discard))
	wp, _ := proxy.NewWebhookProxy(webhookUpstream.URL, log)
	sp, _ := proxy.NewSSEProxy(paySSEUpstream.URL, log)
	lp, _ := proxy.NewShippingLabelProxy(labelUpstream.URL, log)

	userMock := &w3b2UserClient{}
	promoMock := &w3b2PromotionClient{}
	payMock := &w3b2PaymentClient{
		payments: map[string]*paymentv1.Payment{
			"pay-mw-1": {PaymentId: "pay-mw-1", UserId: "buyer-mw-1"},
		},
	}
	searchMock := &fakeShopScopedSearchClient{
		searchResp: nil,
	}

	revoker := &fakeRevocationChecker{
		revoked:       map[string]bool{},
		revokedBefore: map[string]time.Time{},
	}
	rl := newW2ARateLimiter()
	tp, err := middleware.NewTrustedProxiesValidated("172.31.250.0/28", log)
	if err != nil {
		t.Fatalf("NewTrustedProxiesValidated: %v", err)
	}

	h := New(Dependencies{
		ServiceName:            "gateway-merge-mw-test",
		Clients:                &clients.Clients{User: userMock, Promotion: promoMock, Payment: payMock, Search: searchMock, Timeout: 5 * time.Second},
		AuthManager:            authMgr,
		RevocationChecker:      revoker,
		RateLimiter:            rl,
		RateLimitIPPerMin:      2,
		RateLimitUserPerMin:    100,
		RateLimitWebhookPerMin: 2,
		TrustedProxies:         tp,
		PaymentWebhookProxy:    wp,
		ShippingWebhookProxy:   wp,
		ShippingLabelProxy:     lp,
		PaymentSSEProxy:        sp,
		Logger:                 log,
	})

	// 1. Oversized (> 2 MiB) body MUST be rejected with 413 PAYLOAD_TOO_LARGE on webhook,
	//    assistant SSE, assistant REST, chat REST, and shipping batch label.
	oversized := bytes.Repeat([]byte("a"), int(httpx.MaxBodyBytes)+1024)
	for i, tc := range []struct {
		name string
		path string
		tok  string
	}{
		{"webhook_zalopay", "/webhooks/zalopay", ""},
		{"assistant_sse", "/api/v1/assistant/chat", buyerTok},
		{"assistant_rest", "/api/v1/assistant/feedback", buyerTok},
		{"chat_rest", "/api/v1/chat/conversations", buyerTok},
		{"shipping_batch_label", "/api/v1/shipping/labels/batch", sellerTok},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(oversized))
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:40000", 1+i)
		req.Header.Set("Content-Type", "application/json")
		if tc.tok != "" {
			req.Header.Set("Authorization", "Bearer "+tc.tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), `"PAYLOAD_TOO_LARGE"`) {
			t.Fatalf("%s oversized body: want 413 PAYLOAD_TOO_LARGE, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
	}

	// 2. Assistant SSE strips duplicate upstream security headers and sets single canonical SecurityHeaders.
	{
		req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/chat", strings.NewReader(`{"message":"hello"}`))
		req.RemoteAddr = "198.51.100.50:40000"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("assistant SSE want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		if vals := rec.Header().Values("X-Content-Type-Options"); len(vals) != 1 || vals[0] != "nosniff" {
			t.Fatalf("assistant SSE X-Content-Type-Options = %v, want single [nosniff]", vals)
		}
		if vals := rec.Header().Values("X-Frame-Options"); len(vals) != 1 || vals[0] != "DENY" {
			t.Fatalf("assistant SSE X-Frame-Options = %v, want single [DENY]", vals)
		}
	}

	// 3. Rate limiting (limit=2/min) enforced on /webhooks/zalopay, payment SSE, assistant SSE, search, and chat REST.
	for i, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		tok    string
	}{
		{"webhook", http.MethodPost, "/webhooks/zalopay", `{"data":"{}"}`, ""},
		{"payment_sse", http.MethodGet, "/api/v1/payments/pay-mw-1/events", "", buyerTok},
		{"assistant_sse", http.MethodPost, "/api/v1/assistant/chat", `{"message":"hi"}`, ""},
		{"search", http.MethodGet, "/api/v1/search?q=ao", "", ""},
		{"chat_rest", http.MethodGet, "/api/v1/chat/conversations", "", buyerTok},
	} {
		ip := fmt.Sprintf("203.0.113.%d:50000", 1+i)
		for attempt := 1; attempt <= 3; attempt++ {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.RemoteAddr = ip
			if tc.tok != "" {
				req.Header.Set("Authorization", "Bearer "+tc.tok)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if attempt == 3 && rec.Code != http.StatusTooManyRequests {
				t.Fatalf("%s rate limit req 3: want 429, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
			if attempt < 3 && rec.Code == http.StatusTooManyRequests {
				t.Fatalf("%s rate limit req %d unexpectedly hit 429", tc.name, attempt)
			}
		}
	}

	// 4. user:revoked_before revocation MUST reject chat WS (Bearer, Cookie, Ticket) and chat REST.
	revokedBuyerTok, _, _, _ := authMgr.SignAccessToken("buyer-revoked-before", auth.RoleBuyer, "")
	wsTicketTok, wsTicketJTI, _, _ := authMgr.SignAccessToken("buyer-revoked-before", auth.RoleBuyer, "")
	chatMock.mu.Lock()
	chatMock.tickets["revoked-before-ticket"] = &chatv1.ExchangeWSTicketResponse{
		UserId:  "buyer-revoked-before",
		Role:    auth.RoleBuyer,
		Jti:     wsTicketJTI,
		WsToken: wsTicketTok,
	}
	chatMock.mu.Unlock()
	revoker.mu.Lock()
	revoker.revokedBefore["buyer-revoked-before"] = time.Now().Add(time.Minute)
	revoker.mu.Unlock()

	for i, tc := range []struct {
		name string
		path string
		hdr  map[string]string
	}{
		{"chat_rest_revoked_before", "/api/v1/chat/conversations", map[string]string{"Authorization": "Bearer " + revokedBuyerTok}},
		{"chat_ws_bearer_revoked_before", "/api/v1/chat/ws", map[string]string{"Authorization": "Bearer " + revokedBuyerTok}},
		{"chat_ws_cookie_revoked_before", "/api/v1/chat/ws", map[string]string{"Cookie": "nm_at=" + revokedBuyerTok}},
		{"chat_ws_ticket_revoked_before", "/api/v1/chat/ws?ticket=revoked-before-ticket", nil},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:45000", 60+i)
		for k, v := range tc.hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"UNAUTHENTICATED"`) {
			t.Fatalf("%s: want 401 UNAUTHENTICATED, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
	}

	// 5. AdminAudit skips self-audited site/domain mutations (no duplicate audit rows)
	//    while auditing non-user mutations (POST /admin/flash-sales/{id}/end).
	userMock.mu.Lock()
	userMock.auditReqs = nil
	userMock.mu.Unlock()

	for i, tc := range []struct {
		method string
		path   string
		body   string
		want   int
	}{
		{http.MethodPost, "/api/v1/admin/sites/site-1/suspend", `{"suspend":true,"reason":"abuse"}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/admin/sites/shop-1/domains/dom-1", "", http.StatusNoContent},
	} {
		var rdr io.Reader
		if tc.body != "" {
			rdr = strings.NewReader(tc.body)
		}
		req := httptest.NewRequest(tc.method, tc.path, rdr)
		req.RemoteAddr = fmt.Sprintf("198.51.100.%d:41000", 80+i)
		req.Header.Set("Authorization", "Bearer "+adminTok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s: want %d, got %d (%s)", tc.method, tc.path, tc.want, rec.Code, rec.Body.String())
		}
	}
	userMock.mu.Lock()
	selfAuditCount := len(userMock.auditReqs)
	userMock.mu.Unlock()
	if selfAuditCount != 0 {
		t.Fatalf("self-audited admin site/domain mutations must not trigger duplicate gateway WriteAuditLog, got %d", selfAuditCount)
	}

	reqEndFS := httptest.NewRequest(http.MethodPost, "/api/v1/admin/flash-sales/fs-1/end", nil)
	reqEndFS.RemoteAddr = "198.51.100.99:41000"
	reqEndFS.Header.Set("Authorization", "Bearer "+adminTok)
	recEndFS := httptest.NewRecorder()
	h.ServeHTTP(recEndFS, reqEndFS)
	if recEndFS.Code != http.StatusOK {
		t.Fatalf("POST /admin/flash-sales/fs-1/end: want 200, got %d (%s)", recEndFS.Code, recEndFS.Body.String())
	}
	userMock.mu.Lock()
	audits := append([]*userv1.WriteAuditLogRequest(nil), userMock.auditReqs...)
	userMock.mu.Unlock()
	if len(audits) != 1 || audits[0].GetAction() != "POST /api/v1/admin/flash-sales/{id}/end" || audits[0].GetTargetId() != "fs-1" {
		t.Fatalf("expected 1 gateway audit log for flash-sale end, got %+v", audits)
	}
}

func TestMerge_AdminListReconciliationRunsForwardsSalesChannel(t *testing.T) {
	authMgr, err := auth.NewManager("merge-recon-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", auth.RoleAdmin, "")

	var gotReq *paymentv1.ListReconciliationRunsRequest
	payMock := &reconRunsMock{
		onList: func(in *paymentv1.ListReconciliationRunsRequest) { gotReq = in },
	}
	h := New(Dependencies{
		ServiceName: "gateway-recon-test",
		Clients:     &clients.Clients{Payment: payMock, Timeout: 5 * time.Second},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-recon-test", logger.WithWriter(io.Discard)),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/reconciliation/runs?sales_channel=STOREFRONT", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotReq == nil || gotReq.GetSalesChannel() != "STOREFRONT" {
		t.Fatalf("GET /admin/reconciliation/runs?sales_channel=STOREFRONT failed: code=%d req=%+v body=%s", rec.Code, gotReq, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"sales_channel":"STOREFRONT"`) || !strings.Contains(rec.Body.String(), `"auto_healed_count":2`) {
		t.Fatalf("reconciliation run summary missing merged fields: %s", rec.Body.String())
	}

	reqBad := httptest.NewRequest(http.MethodGet, "/api/v1/admin/reconciliation/runs?sales_channel=INVALID", nil)
	reqBad.Header.Set("Authorization", "Bearer "+adminTok)
	recBad := httptest.NewRecorder()
	h.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("invalid sales_channel on reconciliation runs: want 400, got %d", recBad.Code)
	}
}

type reconRunsMock struct {
	paymentv1.PaymentServiceClient
	onList func(*paymentv1.ListReconciliationRunsRequest)
}

func (m *reconRunsMock) ListReconciliationRuns(_ context.Context, in *paymentv1.ListReconciliationRunsRequest, _ ...grpc.CallOption) (*paymentv1.ListReconciliationRunsResponse, error) {
	if m.onList != nil {
		m.onList(in)
	}
	return &paymentv1.ListReconciliationRunsResponse{
		Runs: []*paymentv1.ReconciliationRunSummary{
			{
				RunId:           "run-sf-1",
				RunType:         "vietqr",
				SalesChannel:    in.GetSalesChannel(),
				AutoHealedCount: 2,
			},
		},
		Total: 1,
	}, nil
}

type flashRLPromoMock struct {
	promotionv1.PromotionServiceClient
}

func (m *flashRLPromoMock) ListActiveFlashSales(_ context.Context, _ *promotionv1.ListActiveFlashSalesRequest, _ ...grpc.CallOption) (*promotionv1.ListActiveFlashSalesResponse, error) {
	return &promotionv1.ListActiveFlashSalesResponse{}, nil
}

func TestRouterFlashSaleRateLimit_PublicBFFVsSellerStrict(t *testing.T) {
	log := logger.New("gateway-flash-rl-test", logger.WithWriter(io.Discard))
	authMgr, err := auth.NewManager("gateway-flash-rl-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	sellerTok, _, _, err := authMgr.SignAccessToken("seller-flash-1", auth.RoleSeller, "shop-flash-1")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}
	tp, err := middleware.NewTrustedProxiesValidated("172.31.250.0/28", log)
	if err != nil {
		t.Fatalf("NewTrustedProxiesValidated: %v", err)
	}
	rl := newW2ARateLimiter()

	h := New(Dependencies{
		ServiceName:          "gateway-flash-rl-test",
		Clients:              &clients.Clients{Promotion: &flashRLPromoMock{}, Timeout: 5 * time.Second},
		AuthManager:          authMgr,
		RateLimiter:          rl,
		RateLimitIPPerMin:    100,
		RateLimitBFFPerMin:   5,
		RateLimitUserPerMin:  100,
		RateLimitFlashPerMin: 2,
		TrustedProxies:       tp,
		Logger:               log,
	})

	// 1. Public GET /api/v1/flash-sales from trusted peer without XFF/X-Real-IP -> uses rl:flash:bff:<peer> (limit=5).
	for i := 1; i <= 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales?include_upcoming=false", nil)
		req.RemoteAddr = "172.31.250.3:50000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 6 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("public BFF GET /flash-sales req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if rl.counts["rl:flash:bff:172.31.250.3"] != 5 {
		t.Fatalf("expected rl:flash:bff:172.31.250.3 count=5, got %v", rl.counts)
	}

	// 2. Public GET /api/v1/flash-sales from trusted peer WITH XFF -> uses rl:flash:ip:<client> (limit=2).
	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/flash-sales", nil)
		req.RemoteAddr = "172.31.250.3:50001"
		req.Header.Set("X-Forwarded-For", "203.0.113.20")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("public XFF GET /flash-sales req %d: got %d, want %d", i, rec.Code, want)
		}
	}
	if rl.counts["rl:flash:ip:203.0.113.20"] != 2 {
		t.Fatalf("expected rl:flash:ip:203.0.113.20 count=2, got %v", rl.counts)
	}

	// 3. Seller POST /api/v1/seller/flash-sales/{id}/items from trusted peer without XFF -> still rl:flash:user:<uid> (limit=2).
	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/flash-sales/fs-1/items", strings.NewReader(`{}`))
		req.RemoteAddr = "172.31.250.4:50002"
		req.Header.Set("Authorization", "Bearer "+sellerTok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if i < 3 && rec.Code == http.StatusTooManyRequests {
			t.Fatalf("seller POST flash-sale item req %d unexpectedly hit 429", i)
		}
		if i == 3 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("seller POST flash-sale item req 3: got %d, want 429", rec.Code)
		}
	}
	if rl.counts["rl:flash:user:seller-flash-1"] != 2 || rl.counts["rl:flash:bff:172.31.250.4"] != 0 {
		t.Fatalf("seller POST flash-sale item must stay on rl:flash:user bucket, got %v", rl.counts)
	}
}
