package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	assistantv1 "github.com/nexusmart/core/gen/go/nexusmart/assistant/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeAssistantClient struct {
	assistantv1.AssistantServiceClient
	lastMD           metadata.MD
	lastHandoffReq   *assistantv1.HandoffRequest
	lastReportReq    *assistantv1.SellerReportRequest
	lastDescribeReq  *assistantv1.DescribeProductRequest
	lastSuggestReq   *assistantv1.SuggestReplyRequest
	lastReviewProdID string
}

func (f *fakeAssistantClient) Handoff(ctx context.Context, in *assistantv1.HandoffRequest, _ ...grpc.CallOption) (*assistantv1.HandoffResponse, error) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastHandoffReq = in
	return &assistantv1.HandoffResponse{
		TicketId:            "tkt-1",
		ConversationId:      "chat-conv-1",
		ShopId:              in.GetShopId(),
		Summary:             "Tóm tắt yêu cầu hỗ trợ",
		Status:              "transferred",
		WithinBusinessHours: true,
		EstimatedResponse:   "5-15 phút",
	}, nil
}

func (f *fakeAssistantClient) SellerReport(ctx context.Context, in *assistantv1.SellerReportRequest, _ ...grpc.CallOption) (*assistantv1.SellerReportResponse, error) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastReportReq = in
	return &assistantv1.SellerReportResponse{
		ShopId: "shop-42",
		Period: in.GetPeriod(),
		Answer: "Doanh thu tuần này đạt 4.500.000đ.",
		Metrics: &assistantv1.SellerReportMetrics{
			TotalOrders:         12,
			CompletedOrders:     9,
			CancelledOrders:     1,
			CancelRatePct:       8.33,
			GrossRevenueVnd:     4500000,
			AvailableBalanceVnd: 3200000,
			PendingBalanceVnd:   1100000,
			TopProducts: []*assistantv1.TopProductStat{
				{ProductId: "prod-1", ProductName: "Áo thun Cotton", UnitsSold: 5, RevenueVnd: 1500000},
			},
		},
		Usage: &assistantv1.UsageMetrics{InputTokens: 100, OutputTokens: 80, TotalTokens: 180, CostVnd: 190},
	}, nil
}

func (f *fakeAssistantClient) DescribeProduct(ctx context.Context, in *assistantv1.DescribeProductRequest, _ ...grpc.CallOption) (*assistantv1.DescribeProductResponse, error) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastDescribeReq = in
	return &assistantv1.DescribeProductResponse{
		TitleSuggestion: in.GetName(),
		Description:     "Mô tả sản phẩm chuẩn tiếng Việt.",
		Highlights:      []string{"Cotton 220gsm", "Thoáng mát"},
		AiGenerated:     true,
		Usage:           &assistantv1.UsageMetrics{InputTokens: 80, OutputTokens: 120, TotalTokens: 200, CostVnd: 259},
	}, nil
}

func (f *fakeAssistantClient) SuggestReply(ctx context.Context, in *assistantv1.SuggestReplyRequest, _ ...grpc.CallOption) (*assistantv1.SuggestReplyResponse, error) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastSuggestReq = in
	return &assistantv1.SuggestReplyResponse{
		Suggestions: []*assistantv1.ReplySuggestion{
			{Id: "s1", Text: "Dạ cao 1m72 nặng 68kg mặc size L vừa đẹp ạ.", Tone: "than_thien"},
		},
		NeedsMoreInfo:     false,
		InjectionDetected: false,
		AiGenerated:       true,
		Usage:             &assistantv1.UsageMetrics{InputTokens: 90, OutputTokens: 60, TotalTokens: 150, CostVnd: 149},
	}, nil
}

func (f *fakeAssistantClient) GetReviewSummary(_ context.Context, in *assistantv1.GetReviewSummaryRequest, _ ...grpc.CallOption) (*assistantv1.GetReviewSummaryResponse, error) {
	f.lastReviewProdID = in.GetProductId()
	return &assistantv1.GetReviewSummaryResponse{
		ProductId:          in.GetProductId(),
		Available:          true,
		ReviewCount:        15,
		MinReviewsRequired: 10,
		AverageRating:      4.6,
		Summary:            "Khách hàng đánh giá cao chất vải cotton dày dặn.",
		Pros:               []string{"Vải mát", "Giao nhanh"},
		Cons:               []string{"Form hơi rộng"},
		AiGenerated:        true,
		UpdatedAt:          timestamppb.New(time.Date(2026, 11, 21, 7, 0, 0, 0, time.UTC)),
	}, nil
}

func (f *fakeAssistantClient) SubmitFeedback(ctx context.Context, _ *assistantv1.SubmitFeedbackRequest, _ ...grpc.CallOption) (*assistantv1.SubmitFeedbackResponse, error) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
	return &assistantv1.SubmitFeedbackResponse{Ok: true}, nil
}

type fakeAssistantProductClient struct {
	productv1.ProductServiceClient
}

func (f *fakeAssistantProductClient) GetProduct(_ context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	return &productv1.GetProductResponse{
		Product: &productv1.Product{
			Id:     in.GetId(),
			Name:   "Áo thun Cotton",
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
		},
	}, nil
}

func TestGatewayAssistantRoutesSSEAuthRBACAndNoShadowing(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-assistant-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	authMgr, err := auth.NewManager("assistant-router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-42")
	sellerNoShopTok, _, _, _ := authMgr.SignAccessToken("seller-2", "seller", "")

	var seenSSEAuth, seenSSEPath, seenSpoofHeader string
	sseUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSSEAuth = r.Header.Get("Authorization")
		seenSSEPath = r.URL.Path
		seenSpoofHeader = r.Header.Get("X-User-Id")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: token\ndata: {\"text\":\"Xin chào\"}\n\nevent: done\ndata: {\"ttft_ms\":5}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer sseUpstream.Close()

	mockClient := &fakeAssistantClient{}
	restore := setAssistantTestUpstream(mockClient, sseUpstream.URL)
	defer restore()

	log := logger.New("gateway-assistant-test", logger.WithWriter(io.Discard))
	reg := prometheus.NewRegistry()
	h := New(Dependencies{
		ServiceName: "gateway-assistant-test",
		Clients: &clients.Clients{
			Product: &fakeAssistantProductClient{},
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		RED:         metrics.NewRED("gateway-assistant-test", reg),
		Logger:      log,
	})

	// 1. SSE chat works anonymously (strips spoofed X-User-Id, sets X-Accel-Buffering: no).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/chat", strings.NewReader(`{"messages":[{"role":"user","content":"Chào"}],"shop_id":"shop-1"}`))
	req.Header.Set("X-User-Id", "spoofed-admin")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for anonymous SSE chat, got %d (%s)", rec.Code, rec.Body.String())
	}
	if seenSSEPath != "/assistant/chat" || seenSSEAuth != "" || seenSpoofHeader != "" {
		t.Fatalf("unexpected SSE upstream headers: path=%q auth=%q spoof=%q", seenSSEPath, seenSSEAuth, seenSpoofHeader)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" || !strings.Contains(rec.Body.String(), "event: token") {
		t.Fatalf("expected SSE stream headers and token event, got headers=%v body=%q", rec.Header(), rec.Body.String())
	}

	// 2. SSE chat with valid buyer JWT forwards Bearer token verbatim.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assistant/chat", strings.NewReader(`{"messages":[{"role":"user","content":"Đơn của tôi"}]}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || seenSSEAuth != "Bearer "+buyerTok {
		t.Fatalf("expected 200 and forwarded buyer JWT, got code=%d auth=%q", rec.Code, seenSSEAuth)
	}

	// 3. SSE chat with invalid JWT returns 401.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assistant/chat", strings.NewReader(`{"messages":[{"role":"user","content":"Hi"}]}`))
	req.Header.Set("Authorization", "Bearer bad.token.here")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid JWT on SSE chat, got %d", rec.Code)
	}

	// 4. Handoff requires auth and forwards buyer JWT.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assistant/handoff", strings.NewReader(`{"shop_id":"shop-1","reason":"Cần gặp nhân viên"}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthenticated handoff, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assistant/handoff", strings.NewReader(`{"shop_id":"shop-1","reason":"Cần gặp nhân viên"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on authenticated handoff, got %d (%s)", rec.Code, rec.Body.String())
	}
	if vals := mockClient.lastMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+buyerTok {
		t.Fatalf("expected buyer Bearer JWT forwarded to gRPC Handoff, got %+v", mockClient.lastMD)
	}

	// 5. Seller assistant routes enforce RBAC (buyer -> 403, seller without shop -> 403, seller -> 200).
	for _, path := range []string{
		"/api/v1/seller/assistant/report",
		"/api/v1/seller/assistant/describe-product",
		"/api/v1/seller/assistant/suggest-reply",
	} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"question":"doanh thu","name":"Áo","customer_message":"Size gì"}`))
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for buyer on %s, got %d", path, rec.Code)
		}

		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"question":"doanh thu","name":"Áo","customer_message":"Size gì"}`))
		req.Header.Set("Authorization", "Bearer "+sellerNoShopTok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for seller without shop on %s, got %d", path, rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/assistant/report", strings.NewReader(`{"question":"Top sản phẩm tuần này","period":"7d"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for seller report, got %d (%s)", rec.Code, rec.Body.String())
	}
	var reportResp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &reportResp); err != nil {
		t.Fatalf("decode report json: %v", err)
	}
	if reportResp["shop_id"] != "shop-42" {
		t.Fatalf("expected shop_id=shop-42, got %+v", reportResp)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/assistant/describe-product", strings.NewReader(`{"name":"Áo thun Cotton","category":"Áo thun","attributes":{"chat_lieu":"Cotton"}}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || mockClient.lastDescribeReq.GetName() != "Áo thun Cotton" {
		t.Fatalf("expected 200 on describe-product, got %d (%s)", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/assistant/suggest-reply", strings.NewReader(`{"product_id":"prod-1","customer_message":"Cao 1m72 mặc size gì?"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || mockClient.lastSuggestReq.GetProductId() != "prod-1" {
		t.Fatalf("expected 200 on suggest-reply, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 6. Public review-summary works and does NOT shadow existing GET /api/v1/products/{id}.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products/prod-99/review-summary", nil))
	if rec.Code != http.StatusOK || mockClient.lastReviewProdID != "prod-99" {
		t.Fatalf("expected 200 on review-summary, got code=%d prod=%q body=%s", rec.Code, mockClient.lastReviewProdID, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products/prod-99", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Áo thun Cotton") {
		t.Fatalf("existing GET /api/v1/products/{id} must not be shadowed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 7. Feedback route requires auth and returns ok.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/assistant/feedback", strings.NewReader(`{"conversation_id":"c1","message_id":"m1","helpful":true}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("expected 200 on feedback, got code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGatewayAssistantSSETrustedProxyClientIP(t *testing.T) {
	var seenXFF, seenXRI string
	sseUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenXFF = r.Header.Get("X-Forwarded-For")
		seenXRI = r.Header.Get("X-Real-IP")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: done\ndata: {}\n\n"))
	}))
	defer sseUpstream.Close()

	restore := setAssistantTestUpstream(&fakeAssistantClient{}, sseUpstream.URL)
	defer restore()

	trusted, err := middleware.ParseTrustedProxies("172.31.250.0/28")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	authMgr, err := auth.NewManager("assistant-ip-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}

	h := New(Dependencies{
		ServiceName:    "gateway-assistant-ip-test",
		AuthManager:    authMgr,
		TrustedProxies: trusted,
		Logger:         logger.New("gateway-assistant-ip-test", logger.WithWriter(io.Discard)),
	})

	call := func(remoteAddr, xff, xri string) (subject string, gotXFF string, gotXRI string) {
		seenXFF, seenXRI = "", ""
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/assistant/chat", strings.NewReader(`{"messages":[{"role":"user","content":"Chào"}]}`))
		req.RemoteAddr = remoteAddr
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		if xri != "" {
			req.Header.Set("X-Real-IP", xri)
		}
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		return "ip:" + seenXFF, seenXFF, seenXRI
	}

	// 1. Two distinct browser clients behind the same trusted BFF container -> two distinct quota subjects.
	subjA, xffA, xriA := call("172.31.250.5:41001", "203.0.113.10", "203.0.113.10")
	subjB, xffB, xriB := call("172.31.250.5:41002", "203.0.113.20", "")
	if xffA != "203.0.113.10" || xffB != "203.0.113.20" {
		t.Fatalf("expected resolved client IPs via trusted BFF, got A=%q B=%q", xffA, xffB)
	}
	if subjA == subjB {
		t.Fatalf("expected distinct subjects for two clients behind trusted BFF, both got %q", subjA)
	}
	if xriA != "" || xriB != "" {
		t.Fatalf("expected X-Real-IP stripped upstream, got A=%q B=%q", xriA, xriB)
	}

	// 2. Spoofed left-most XFF behind trusted BFF picks right-most untrusted hop.
	_, xffMulti, _ := call("172.31.250.5:41003", "1.2.3.4, 203.0.113.15", "")
	if xffMulti != "203.0.113.15" {
		t.Fatalf("expected right-most untrusted IP 203.0.113.15, got %q", xffMulti)
	}

	// 3. Trusted BFF sending only X-Real-IP resolves and forwards it as X-Forwarded-For.
	_, xffFromXRI, xriCleared := call("172.31.250.5:41004", "", "203.0.113.30")
	if xffFromXRI != "203.0.113.30" || xriCleared != "" {
		t.Fatalf("expected X-Real-IP fallback 203.0.113.30 and cleared X-Real-IP, got xff=%q xri=%q", xffFromXRI, xriCleared)
	}

	// 4. Untrusted direct peer spoofing X-Forwarded-For and X-Real-IP -> spoofed headers discarded.
	_, xffUntrusted, xriUntrusted := call("198.51.100.77:55555", "203.0.113.99", "203.0.113.88")
	if xffUntrusted != "198.51.100.77" || xriUntrusted != "" {
		t.Fatalf("expected untrusted peer IP 198.51.100.77 with spoofed headers ignored, got xff=%q xri=%q", xffUntrusted, xriUntrusted)
	}
}
