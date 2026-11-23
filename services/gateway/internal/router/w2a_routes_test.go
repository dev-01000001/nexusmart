package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"github.com/nexusmart/core/services/gateway/internal/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type w2aRevokerStub struct {
	mu            sync.Mutex
	revokedJTI    map[string]bool
	revokedBefore map[string]int64
	err           error
}

func (r *w2aRevokerStub) IsTokenRevoked(_ context.Context, claims *auth.Claims) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return false, r.err
	}
	if claims == nil {
		return false, nil
	}
	if r.revokedJTI[claims.JTI()] {
		return true, nil
	}
	if cutoff, ok := r.revokedBefore[claims.UserID()]; ok {
		if claims.IssuedAt == nil || claims.IssuedAt.Unix() <= cutoff {
			return true, nil
		}
	}
	return false, nil
}

type w2aRateLimiter struct {
	mu     sync.Mutex
	counts map[string]int
}

func newW2ARateLimiter() *w2aRateLimiter {
	return &w2aRateLimiter{counts: make(map[string]int)}
}

func (l *w2aRateLimiter) Allow(_ context.Context, key string, limit int, _ time.Duration) (middleware.RateLimitResult, error) {
	if limit <= 0 {
		return middleware.RateLimitResult{Allowed: true}, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[key] >= limit {
		return middleware.RateLimitResult{Allowed: false, Remaining: 0, RetryAfter: 10 * time.Second}, nil
	}
	l.counts[key]++
	return middleware.RateLimitResult{Allowed: true, Remaining: limit - l.counts[key]}, nil
}

type w2aUserClient struct {
	userv1.UserServiceClient
	mu            sync.Mutex
	lastForgotReq *userv1.ForgotPasswordRequest
	lastResetReq  *userv1.ResetPasswordRequest
	auditReqs     []*userv1.WriteAuditLogRequest
	profileUser   *userv1.User
}

func (u *w2aUserClient) ForgotPassword(_ context.Context, in *userv1.ForgotPasswordRequest, _ ...grpc.CallOption) (*userv1.ForgotPasswordResponse, error) {
	u.mu.Lock()
	u.lastForgotReq = in
	u.mu.Unlock()
	return &userv1.ForgotPasswordResponse{Sent: true}, nil
}

func (u *w2aUserClient) ResetPassword(_ context.Context, in *userv1.ResetPasswordRequest, _ ...grpc.CallOption) (*userv1.ResetPasswordResponse, error) {
	u.mu.Lock()
	u.lastResetReq = in
	u.mu.Unlock()
	return &userv1.ResetPasswordResponse{Ok: true}, nil
}

func (u *w2aUserClient) GetProfile(_ context.Context, _ *userv1.GetProfileRequest, _ ...grpc.CallOption) (*userv1.GetProfileResponse, error) {
	return &userv1.GetProfileResponse{User: u.profileUser}, nil
}

func (u *w2aUserClient) WriteAuditLog(_ context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	u.mu.Lock()
	u.auditReqs = append(u.auditReqs, in)
	u.mu.Unlock()
	return &userv1.WriteAuditLogResponse{AuditLog: &userv1.AuditLog{Id: "audit-w2a"}}, nil
}

type w2aOrderClient struct {
	orderv1.OrderServiceClient
	mu                     sync.Mutex
	orders                 map[string]*orderv1.Order
	returns                map[string]*orderv1.ReturnRequest
	lastGetReturnReq       *orderv1.GetReturnRequestRequest
	lastListReturnsReq     *orderv1.ListReturnRequestsRequest
	lastRespondReq         *orderv1.RespondReturnRequest
	lastEscalateReq        *orderv1.EscalateReturnRequest
	lastEvidenceReq        *orderv1.AddReturnEvidenceRequest
	lastConfirmReceivedReq *orderv1.ConfirmReturnReceivedRequest
	lastDisputeReq         *orderv1.DisputeReturnGoodsRequest
	lastAdminResolveReq    *orderv1.AdminResolveReturnRequest
}

func (o *w2aOrderClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ord, ok := o.orders[in.GetOrderId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return &orderv1.GetOrderResponse{Order: ord}, nil
}

func (o *w2aOrderClient) GetReturnRequest(_ context.Context, in *orderv1.GetReturnRequestRequest, _ ...grpc.CallOption) (*orderv1.GetReturnRequestResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastGetReturnReq = in
	for _, rr := range o.returns {
		if (in.GetReturnRequestId() != "" && rr.GetId() == in.GetReturnRequestId()) ||
			(in.GetOrderId() != "" && rr.GetOrderId() == in.GetOrderId()) {
			return &orderv1.GetReturnRequestResponse{ReturnRequest: rr}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "return request not found")
}

func (o *w2aOrderClient) ListReturnRequests(_ context.Context, in *orderv1.ListReturnRequestsRequest, _ ...grpc.CallOption) (*orderv1.ListReturnRequestsResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastListReturnsReq = in
	out := make([]*orderv1.ReturnRequest, 0, len(o.returns))
	for _, rr := range o.returns {
		out = append(out, rr)
	}
	return &orderv1.ListReturnRequestsResponse{ReturnRequests: out, Total: int64(len(out))}, nil
}

func (o *w2aOrderClient) RespondReturn(_ context.Context, in *orderv1.RespondReturnRequest, _ ...grpc.CallOption) (*orderv1.RespondReturnResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastRespondReq = in
	return &orderv1.RespondReturnResponse{ReturnRequest: o.returns[in.GetReturnRequestId()]}, nil
}

func (o *w2aOrderClient) EscalateReturn(_ context.Context, in *orderv1.EscalateReturnRequest, _ ...grpc.CallOption) (*orderv1.EscalateReturnResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastEscalateReq = in
	return &orderv1.EscalateReturnResponse{ReturnRequest: o.returns[in.GetReturnRequestId()]}, nil
}

func (o *w2aOrderClient) AddReturnEvidence(_ context.Context, in *orderv1.AddReturnEvidenceRequest, _ ...grpc.CallOption) (*orderv1.AddReturnEvidenceResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastEvidenceReq = in
	return &orderv1.AddReturnEvidenceResponse{ReturnRequest: o.returns[in.GetReturnRequestId()]}, nil
}

func (o *w2aOrderClient) ConfirmReturnReceived(_ context.Context, in *orderv1.ConfirmReturnReceivedRequest, _ ...grpc.CallOption) (*orderv1.ConfirmReturnReceivedResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastConfirmReceivedReq = in
	return &orderv1.ConfirmReturnReceivedResponse{ReturnRequest: o.returns[in.GetReturnRequestId()]}, nil
}

func (o *w2aOrderClient) DisputeReturnGoods(_ context.Context, in *orderv1.DisputeReturnGoodsRequest, _ ...grpc.CallOption) (*orderv1.DisputeReturnGoodsResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastDisputeReq = in
	return &orderv1.DisputeReturnGoodsResponse{ReturnRequest: o.returns[in.GetReturnRequestId()]}, nil
}

func (o *w2aOrderClient) AdminResolveReturn(_ context.Context, in *orderv1.AdminResolveReturnRequest, _ ...grpc.CallOption) (*orderv1.AdminResolveReturnResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastAdminResolveReq = in
	return &orderv1.AdminResolveReturnResponse{ReturnRequest: o.returns[in.GetReturnRequestId()]}, nil
}

type w2aPaymentClient struct {
	paymentv1.PaymentServiceClient
	mu                  sync.Mutex
	payments            map[string]*paymentv1.Payment
	refunds             []*paymentv1.Refund
	queueItems          []*paymentv1.ReconciliationQueueItem
	lastListRefundsReq  *paymentv1.ListRefundsRequest
	lastSubmitBankReq   *paymentv1.SubmitRefundBankAccountRequest
	lastSettleRefundReq *paymentv1.SettleRefundRequest
	lastListQueueReq    *paymentv1.ListReconciliationQueueRequest
	lastResolveQueueReq *paymentv1.ResolveReconciliationItemRequest
}

func (p *w2aPaymentClient) GetPayment(_ context.Context, in *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pay, ok := p.payments[in.GetPaymentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "payment not found")
	}
	return &paymentv1.GetPaymentResponse{Payment: pay}, nil
}

func (p *w2aPaymentClient) ListRefunds(_ context.Context, in *paymentv1.ListRefundsRequest, _ ...grpc.CallOption) (*paymentv1.ListRefundsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastListRefundsReq = in
	return &paymentv1.ListRefundsResponse{Refunds: p.refunds, Total: int64(len(p.refunds))}, nil
}

func (p *w2aPaymentClient) SubmitRefundBankAccount(_ context.Context, in *paymentv1.SubmitRefundBankAccountRequest, _ ...grpc.CallOption) (*paymentv1.SubmitRefundBankAccountResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastSubmitBankReq = in
	return &paymentv1.SubmitRefundBankAccountResponse{Refund: p.refunds[0]}, nil
}

func (p *w2aPaymentClient) SettleRefund(_ context.Context, in *paymentv1.SettleRefundRequest, _ ...grpc.CallOption) (*paymentv1.SettleRefundResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastSettleRefundReq = in
	return &paymentv1.SettleRefundResponse{Refund: p.refunds[0], AlreadySettled: false}, nil
}

func (p *w2aPaymentClient) ListReconciliationQueue(_ context.Context, in *paymentv1.ListReconciliationQueueRequest, _ ...grpc.CallOption) (*paymentv1.ListReconciliationQueueResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastListQueueReq = in
	return &paymentv1.ListReconciliationQueueResponse{Items: p.queueItems, Total: int64(len(p.queueItems))}, nil
}

func (p *w2aPaymentClient) ResolveReconciliationItem(_ context.Context, in *paymentv1.ResolveReconciliationItemRequest, _ ...grpc.CallOption) (*paymentv1.ResolveReconciliationItemResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastResolveQueueReq = in
	return &paymentv1.ResolveReconciliationItemResponse{
		Item:            p.queueItems[0],
		Refund:          p.refunds[0],
		AlreadyResolved: false,
	}, nil
}

type w2aShippingClient struct {
	shippingv1.ShippingServiceClient
	mu                    sync.Mutex
	lastGetShipmentReq    *shippingv1.GetShipmentRequest
	lastRedeliveryReq     *shippingv1.RequestRedeliveryRequest
	lastGetSettingsReq    *shippingv1.GetShopShippingSettingsRequest
	lastUpdateSettingsReq *shippingv1.UpdateShopShippingSettingsRequest
	lastUpdateCarrierReq  *shippingv1.AdminUpdateCarrierRequest
	lastTestCarrierReq    *shippingv1.AdminTestCarrierRequest
}

func (s *w2aShippingClient) GetShipment(_ context.Context, in *shippingv1.GetShipmentRequest, _ ...grpc.CallOption) (*shippingv1.GetShipmentResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastGetShipmentReq = in
	dir := in.GetDirection()
	if dir == shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_UNSPECIFIED {
		dir = shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD
	}
	return &shippingv1.GetShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:      "ship-1",
			OrderId:         in.GetOrderId(),
			ShopId:          "shop-a",
			CarrierCode:     "ghn",
			TrackingCode:    "GHN-RET-1",
			Status:          shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING,
			Direction:       dir,
			ReturnRequestId: "ret-a",
			RedeliveryCount: 2,
		},
	}, nil
}

func (s *w2aShippingClient) RequestRedelivery(_ context.Context, in *shippingv1.RequestRedeliveryRequest, _ ...grpc.CallOption) (*shippingv1.RequestRedeliveryResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRedeliveryReq = in
	return &shippingv1.RequestRedeliveryResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId:      "ship-1",
			OrderId:         in.GetOrderId(),
			Status:          shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING,
			Direction:       shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD,
			RedeliveryCount: 1,
		},
	}, nil
}

func (s *w2aShippingClient) GetShopShippingSettings(_ context.Context, in *shippingv1.GetShopShippingSettingsRequest, _ ...grpc.CallOption) (*shippingv1.GetShopShippingSettingsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastGetSettingsReq = in
	return &shippingv1.GetShopShippingSettingsResponse{
		Settings: &shippingv1.ShopShippingSettings{
			ShopId:              in.GetShopId(),
			EnabledCarrierCodes: []string{"ghn", "mock_express"},
			DefaultWeightGrams:  350,
			DefaultLengthCm:     25,
			DefaultWidthCm:      20,
			DefaultHeightCm:     10,
		},
	}, nil
}

func (s *w2aShippingClient) UpdateShopShippingSettings(_ context.Context, in *shippingv1.UpdateShopShippingSettingsRequest, _ ...grpc.CallOption) (*shippingv1.UpdateShopShippingSettingsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUpdateSettingsReq = in
	return &shippingv1.UpdateShopShippingSettingsResponse{
		Settings: &shippingv1.ShopShippingSettings{
			ShopId:              in.GetShopId(),
			EnabledCarrierCodes: in.GetEnabledCarrierCodes(),
			DefaultWeightGrams:  in.GetDefaultWeightGrams(),
			DefaultLengthCm:     in.GetDefaultLengthCm(),
			DefaultWidthCm:      in.GetDefaultWidthCm(),
			DefaultHeightCm:     in.GetDefaultHeightCm(),
		},
	}, nil
}

func (s *w2aShippingClient) AdminListCarriers(_ context.Context, _ *shippingv1.AdminListCarriersRequest, _ ...grpc.CallOption) (*shippingv1.AdminListCarriersResponse, error) {
	return &shippingv1.AdminListCarriersResponse{
		Carriers: []*shippingv1.CarrierConfig{
			{
				CarrierCode:     "ghn",
				Name:            "Giao Hàng Nhanh",
				Enabled:         true,
				TokenConfigured: true,
				FallbackRates: []*shippingv1.FallbackRate{
					{
						Region:              shippingv1.ShippingRegion_SHIPPING_REGION_SAME_PROVINCE,
						BaseFeeVnd:          22000,
						BaseWeightGrams:     500,
						ExtraFeeVndPer_500G: 5000,
					},
				},
			},
		},
	}, nil
}

func (s *w2aShippingClient) AdminUpdateCarrier(_ context.Context, in *shippingv1.AdminUpdateCarrierRequest, _ ...grpc.CallOption) (*shippingv1.AdminUpdateCarrierResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUpdateCarrierReq = in
	return &shippingv1.AdminUpdateCarrierResponse{
		Carrier: &shippingv1.CarrierConfig{
			CarrierCode:     in.GetCarrierCode(),
			Name:            "Giao Hàng Nhanh",
			Enabled:         in.GetEnabled(),
			TokenConfigured: in.GetApiToken() != "",
			FallbackRates:   in.GetFallbackRates(),
		},
	}, nil
}

func (s *w2aShippingClient) AdminTestCarrier(_ context.Context, in *shippingv1.AdminTestCarrierRequest, _ ...grpc.CallOption) (*shippingv1.AdminTestCarrierResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTestCarrierReq = in
	return &shippingv1.AdminTestCarrierResponse{Ok: true, Message: "connection ok"}, nil
}

type w2aPromotionClient struct {
	promotionv1.PromotionServiceClient
}

func (p *w2aPromotionClient) ListAvailableVouchers(_ context.Context, _ *promotionv1.ListAvailableVouchersRequest, _ ...grpc.CallOption) (*promotionv1.ListAvailableVouchersResponse, error) {
	return &promotionv1.ListAvailableVouchersResponse{Vouchers: []*promotionv1.Voucher{}}, nil
}

func TestW2AOptionalAuthAndSSERevokedBeforeAndRedisError(t *testing.T) {
	authMgr, err := auth.NewManager("w2a-rev-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-rev", "buyer", "")

	revoker := &w2aRevokerStub{
		revokedJTI:    map[string]bool{},
		revokedBefore: map[string]int64{},
	}

	sseUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: payment.pending\ndata: {}\n\n"))
	}))
	defer sseUpstream.Close()

	log := logger.New("w2a-rev-test", logger.WithWriter(io.Discard))
	sseProxy, _ := proxy.NewSSEProxy(sseUpstream.URL, log)

	h := New(Dependencies{
		ServiceName: "w2a-rev-test",
		Clients: &clients.Clients{
			Promotion: &w2aPromotionClient{},
			Payment: &w2aPaymentClient{
				payments: map[string]*paymentv1.Payment{
					"pay-1": {PaymentId: "pay-1", UserId: "buyer-rev"},
				},
			},
			Timeout: 5 * time.Second,
		},
		AuthManager:       authMgr,
		RevocationChecker: revoker,
		PaymentSSEProxy:   sseProxy,
		Logger:            log,
	})

	// 1. Optional auth without token -> 200.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/vouchers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /vouchers anonymous: want 200, got %d", rec.Code)
	}

	// 2. Revoke user via revoked_before = now + 10s -> optional auth & SSE both return 401!
	revoker.mu.Lock()
	revoker.revokedBefore["buyer-rev"] = time.Now().UTC().Unix() + 10
	revoker.mu.Unlock()

	for _, path := range []string{"/api/v1/vouchers", "/api/v1/payments/pay-1/events"} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s with revoked_before token: want 401, got %d (%s)", path, rec.Code, rec.Body.String())
		}
	}

	// 3. Redis error -> optional auth (with token) & SSE both fail closed with 503!
	revoker.mu.Lock()
	revoker.err = errors.New("redis down")
	revoker.mu.Unlock()

	for _, path := range []string{"/api/v1/vouchers", "/api/v1/payments/pay-1/events"} {
		rec = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET %s on Redis error: want 503, got %d (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

func TestW2ASecurityHeaders413AndWebhookRateLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	log := logger.New("w2a-plt-test", logger.WithWriter(io.Discard))
	whProxy, _ := proxy.NewWebhookProxy(upstream.URL, log)
	lim := newW2ARateLimiter()

	h := New(Dependencies{
		ServiceName:            "w2a-plt-test",
		Clients:                &clients.Clients{User: &w2aUserClient{}, Timeout: 5 * time.Second},
		RateLimiter:            lim,
		RateLimitIPPerMin:      10,
		RateLimitAuthPerMin:    2,
		RateLimitWebhookPerMin: 2,
		PaymentWebhookProxy:    whProxy,
		ShippingWebhookProxy:   whProxy,
		Logger:                 log,
	})

	// 1. Security headers on API, webhook, and 404.
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/webhooks/payment", `{"ok":true}`},
		{http.MethodGet, "/api/v1/does-not-exist", ""},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.RemoteAddr = "198.51.100.2:1234"
		h.ServeHTTP(rec, req)
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
			rec.Header().Get("X-Frame-Options") != "DENY" ||
			rec.Header().Get("Referrer-Policy") == "" ||
			!strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
			t.Fatalf("%s %s missing security headers: %+v", tc.method, tc.path, rec.Header())
		}
	}

	// 2. Oversized body (> 2 MiB) -> 413 on API and webhook.
	bigBody := []byte(`{"email":"a@b.c","password":"` + strings.Repeat("x", httpx.MaxBodyBytes+1024) + `"}`)
	for _, path := range []string{"/api/v1/auth/forgot-password", "/webhooks/payment"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(bigBody))
		req.RemoteAddr = "198.51.100.3:1234"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("POST %s >2MiB: want 413, got %d (%s)", path, rec.Code, rec.Body.String())
		}
	}

	// 3. Dedicated webhook rate limit: 2 allowed, 3rd -> 429; API IP bucket unaffected.
	for i := 1; i <= 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/webhooks/shipping/ghn", strings.NewReader(`{}`))
		req.RemoteAddr = "198.51.100.9:1234"
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("webhook req %d: want %d, got %d", i, want, rec.Code)
		}
	}

	// 4. Tight auth rate limit on forgot-password/reset-password: 2 allowed, 3rd -> 429.
	for i := 1; i <= 3; i++ {
		path := "/api/v1/auth/forgot-password"
		body := `{"email":"u@example.com"}`
		if i == 2 {
			path = "/api/v1/auth/reset-password"
			body = `{"token":"abc","new_password":"NewPassword123!"}`
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.RemoteAddr = "198.51.100.9:1235"
		h.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("auth tight req %d (%s): want %d, got %d (%s)", i, path, want, rec.Code, rec.Body.String())
		}
	}
}

func TestW2AReturnsRefundsReconciliationShippingRoutesAndRBAC(t *testing.T) {
	authMgr, err := auth.NewManager("w2a-routes-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	otherBuyerTok, _, _, _ := authMgr.SignAccessToken("buyer-2", "buyer", "")
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	sellerBTok, _, _, _ := authMgr.SignAccessToken("seller-b", "seller", "shop-b")
	sellerNoShopTok, _, _, _ := authMgr.SignAccessToken("seller-0", "seller", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	now := time.Now().UTC().Truncate(time.Second)
	userMock := &w2aUserClient{
		profileUser: &userv1.User{
			Id:         "buyer-1",
			Email:      "buyer@example.com",
			Role:       userv1.UserRole_USER_ROLE_BUYER,
			Status:     userv1.UserStatus_USER_STATUS_LOCKED,
			LockReason: "fraud detected",
		},
	}
	orderMock := &w2aOrderClient{
		orders: map[string]*orderv1.Order{
			"ord-a": {
				Id:              "ord-a",
				OrderCode:       "NM-A",
				BuyerId:         "buyer-1",
				ShopId:          "shop-a",
				Status:          orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
				DeliveredAt:     timestamppb.New(now),
				ReturnRequestId: "ret-a",
				ReturnStatus:    orderv1.ReturnStatus_RETURN_STATUS_PENDING,
			},
		},
		returns: map[string]*orderv1.ReturnRequest{
			"ret-a": {
				Id:              "ret-a",
				OrderId:         "ord-a",
				OrderCode:       "NM-A",
				BuyerId:         "buyer-1",
				ShopId:          "shop-a",
				Status:          orderv1.ReturnStatus_RETURN_STATUS_PENDING,
				Reason:          "wrong size",
				RefundAmountVnd: 150000,
				ReturnFeeVnd:    22000,
				ReturnFeeBearer: "SELLER",
				GoodsReceived:   false,
				DeadlineAt:      timestamppb.New(now.Add(72 * time.Hour)),
				CreatedAt:       timestamppb.New(now),
				UpdatedAt:       timestamppb.New(now),
				Events:          []*orderv1.ReturnEvent{{Id: "ev-1", ToStatus: orderv1.ReturnStatus_RETURN_STATUS_PENDING, ActorId: "buyer-1", ActorRole: "buyer"}},
			},
		},
	}
	paymentMock := &w2aPaymentClient{
		refunds: []*paymentv1.Refund{
			{
				RefundId:          "ref-1",
				PaymentId:         "pay-1",
				OrderId:           "ord-a",
				AmountVnd:         150000,
				Status:            paymentv1.RefundStatus_REFUND_STATUS_PENDING,
				BankCode:          "VCB",
				BankAccountMasked: "****7890",
				BankAccountName:   "NGUYEN VAN A",
				BankAccountNumber: "01234567897890",
				IsLatePayment:     false,
			},
		},
		queueItems: []*paymentv1.ReconciliationQueueItem{
			{
				Id:                "rq-1",
				Provider:          "vietqr_mock",
				ProviderTxId:      "TX-1",
				ExpectedPaymentId: "pay-1",
				ExpectedAmountVnd: 150000,
				ActualAmountVnd:   170000,
				Reason:            "amount_mismatch",
				ResolutionStatus:  "pending",
			},
		},
	}
	shippingMock := &w2aShippingClient{}

	h := New(Dependencies{
		ServiceName: "w2a-routes-test",
		Clients: &clients.Clients{
			User:     userMock,
			Order:    orderMock,
			Payment:  paymentMock,
			Shipping: shippingMock,
			Timeout:  5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("w2a-routes-test", logger.WithWriter(io.Discard)),
	})

	do := func(method, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
		var rBody io.Reader
		if body != "" {
			rBody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rBody)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// --- 1. RBAC matrix on all new protected routes ---
	sellerRoutes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/seller/returns", ""},
		{http.MethodGet, "/api/v1/seller/returns/ret-a", ""},
		{http.MethodPost, "/api/v1/seller/returns/ret-a/respond", `{"decision":"ACCEPT_RETURN"}`},
		{http.MethodPost, "/api/v1/seller/returns/ret-a/confirm-received", ""},
		{http.MethodPost, "/api/v1/seller/returns/ret-a/dispute", `{"reason":"damaged"}`},
		{http.MethodGet, "/api/v1/seller/shipping-settings", ""},
		{http.MethodPut, "/api/v1/seller/shipping-settings", `{"enabled_carrier_codes":["ghn"],"default_weight_grams":300}`},
	}
	for _, rt := range sellerRoutes {
		if rec := do(rt.method, rt.path, "", rt.body, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s: want 401, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, buyerTok, rt.body, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("buyer %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, adminTok, rt.body, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("admin %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, sellerNoShopTok, rt.body, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("seller-no-shop %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
	}

	adminRoutes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/admin/returns", ""},
		{http.MethodGet, "/api/v1/admin/returns/ret-a", ""},
		{http.MethodPost, "/api/v1/admin/returns/ret-a/resolve", `{"decision":"REFUND","return_fee_bearer":"SELLER"}`},
		{http.MethodGet, "/api/v1/admin/refunds", ""},
		{http.MethodPost, "/api/v1/admin/refunds/ref-1/settle", `{"provider_refund_id":"TX-1"}`},
		{http.MethodGet, "/api/v1/admin/reconciliation/queue", ""},
		{http.MethodPost, "/api/v1/admin/reconciliation/queue/rq-1/resolve", `{"action":"ASSIGN","payment_id":"pay-1"}`},
		{http.MethodGet, "/api/v1/admin/carriers", ""},
		{http.MethodPut, "/api/v1/admin/carriers/ghn", `{"enabled":true,"api_token":"tok"}`},
		{http.MethodPost, "/api/v1/admin/carriers/ghn/test", ""},
	}
	for _, rt := range adminRoutes {
		if rec := do(rt.method, rt.path, "", rt.body, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s: want 401, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, buyerTok, rt.body, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("buyer %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, sellerATok, rt.body, nil); rec.Code != http.StatusForbidden {
			t.Fatalf("seller %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
	}

	buyerRoutes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/orders/ord-a/return", ""},
		{http.MethodGet, "/api/v1/returns", ""},
		{http.MethodGet, "/api/v1/returns/ret-a", ""},
		{http.MethodPost, "/api/v1/returns/ret-a/escalate", `{"reason":"disagree"}`},
		{http.MethodPost, "/api/v1/returns/ret-a/evidence", `{"note":"photo","evidence_urls":["https://example.com/1.jpg"]}`},
		{http.MethodPost, "/api/v1/orders/ord-a/redelivery", `{"preferred_date":"2026-11-26","note":"afternoon"}`},
		{http.MethodGet, "/api/v1/orders/ord-a/refunds", ""},
		{http.MethodPost, "/api/v1/refunds/ref-1/bank-account", `{"bank_code":"VCB","bank_account_number":"01234567897890","bank_account_name":"NGUYEN VAN A"}`},
	}
	for _, rt := range buyerRoutes {
		if rec := do(rt.method, rt.path, "", rt.body, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s: want 401, got %d", rt.method, rt.path, rec.Code)
		}
	}

	// --- 2. Updated DTO fields: User.lock_reason & Order return/delivered fields & Shipment direction ---
	if rec := do(http.MethodGet, "/api/v1/users/me", buyerTok, "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lock_reason":"fraud detected"`) {
		t.Fatalf("GET /users/me lock_reason missing: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/orders/ord-a", buyerTok, "", nil); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"return_request_id":"ret-a"`) ||
		!strings.Contains(rec.Body.String(), `"return_status":"PENDING"`) ||
		!strings.Contains(rec.Body.String(), `"delivered_at":`) {
		t.Fatalf("GET /orders/ord-a missing new fields: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/shipping/shipments/ord-a?direction=RETURN", buyerTok, "", nil); rec.Code != http.StatusOK ||
		shippingMock.lastGetShipmentReq.GetDirection() != shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN ||
		!strings.Contains(rec.Body.String(), `"direction":"RETURN"`) ||
		!strings.Contains(rec.Body.String(), `"redelivery_count":2`) {
		t.Fatalf("GET /shipping/shipments/ord-a?direction=RETURN mismatch: code=%d req=%+v body=%s", rec.Code, shippingMock.lastGetShipmentReq, rec.Body.String())
	}

	// --- 3. Buyer return/redelivery/refund endpoints + bank_account_number privacy ---
	if rec := do(http.MethodGet, "/api/v1/orders/ord-a/return", buyerTok, "", nil); rec.Code != http.StatusOK || orderMock.lastGetReturnReq.GetOrderId() != "ord-a" {
		t.Fatalf("GET /orders/ord-a/return failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/returns?status=PENDING", buyerTok, "", nil); rec.Code != http.StatusOK || orderMock.lastListReturnsReq.GetRoleScope() != "buyer" {
		t.Fatalf("GET /returns failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/returns/ret-a", buyerTok, "", nil); rec.Code != http.StatusOK || orderMock.lastGetReturnReq.GetReturnRequestId() != "ret-a" {
		t.Fatalf("GET /returns/ret-a failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/v1/returns/ret-a/escalate", buyerTok, `{"reason":"disagree","evidence_urls":["https://example.com/a.jpg"]}`, nil); rec.Code != http.StatusOK || orderMock.lastEscalateReq.GetReason() != "disagree" {
		t.Fatalf("POST /returns/ret-a/escalate failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/v1/returns/ret-a/evidence", buyerTok, `{"note":"more","evidence_urls":["https://example.com/b.jpg"]}`, nil); rec.Code != http.StatusOK || orderMock.lastEvidenceReq.GetNote() != "more" {
		t.Fatalf("POST /returns/ret-a/evidence failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/v1/orders/ord-a/redelivery", buyerTok, `{"preferred_date":"2026-11-26","note":"evening"}`, nil); rec.Code != http.StatusOK || shippingMock.lastRedeliveryReq.GetPreferredDate() != "2026-11-26" {
		t.Fatalf("POST /orders/ord-a/redelivery failed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Non-owner buyer on GET /orders/{id}/refunds -> 403.
	if rec := do(http.MethodGet, "/api/v1/orders/ord-a/refunds", otherBuyerTok, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner GET /orders/ord-a/refunds: want 403, got %d", rec.Code)
	}
	// Owner buyer on GET /orders/{id}/refunds -> 200 with bank_account_masked only (NO full bank_account_number).
	recBuyerRefunds := do(http.MethodGet, "/api/v1/orders/ord-a/refunds", buyerTok, "", nil)
	if recBuyerRefunds.Code != http.StatusOK || strings.Contains(recBuyerRefunds.Body.String(), "01234567897890") || !strings.Contains(recBuyerRefunds.Body.String(), "****7890") {
		t.Fatalf("buyer GET /orders/ord-a/refunds leaked or missed masked bank account: code=%d body=%s", recBuyerRefunds.Code, recBuyerRefunds.Body.String())
	}
	recBank := do(http.MethodPost, "/api/v1/refunds/ref-1/bank-account", buyerTok, `{"bank_code":"VCB","bank_account_number":"01234567897890","bank_account_name":"NGUYEN VAN A"}`, nil)
	if recBank.Code != http.StatusOK || strings.Contains(recBank.Body.String(), "01234567897890") || paymentMock.lastSubmitBankReq.GetBankAccountNumber() != "01234567897890" {
		t.Fatalf("POST /refunds/ref-1/bank-account mismatch: code=%d body=%s", recBank.Code, recBank.Body.String())
	}

	// --- 4. Seller endpoints: shop_id forced from claims + cross-shop 403 ---
	if rec := do(http.MethodGet, "/api/v1/seller/returns", sellerATok, "", nil); rec.Code != http.StatusOK || orderMock.lastListReturnsReq.GetRoleScope() != "seller" {
		t.Fatalf("GET /seller/returns failed: code=%d", rec.Code)
	}
	if rec := do(http.MethodGet, "/api/v1/seller/returns/ret-a", sellerATok, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("seller A GET /seller/returns/ret-a: want 200, got %d", rec.Code)
	}
	if rec := do(http.MethodGet, "/api/v1/seller/returns/ret-a", sellerBTok, "", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("seller B GET /seller/returns/ret-a: want 403, got %d", rec.Code)
	}
	if rec := do(http.MethodPost, "/api/v1/seller/returns/ret-a/respond", sellerATok, `{"decision":"ACCEPT_RETURN","note":"ok"}`, nil); rec.Code != http.StatusOK || orderMock.lastRespondReq.GetDecision() != orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN {
		t.Fatalf("POST /seller/returns/ret-a/respond failed: code=%d req=%+v", rec.Code, orderMock.lastRespondReq)
	}
	if rec := do(http.MethodPost, "/api/v1/seller/returns/ret-a/confirm-received", sellerATok, "", nil); rec.Code != http.StatusOK || orderMock.lastConfirmReceivedReq.GetReturnRequestId() != "ret-a" {
		t.Fatalf("POST /seller/returns/ret-a/confirm-received failed: code=%d", rec.Code)
	}
	if rec := do(http.MethodPost, "/api/v1/seller/returns/ret-a/dispute", sellerATok, `{"reason":"wrong item"}`, nil); rec.Code != http.StatusOK || orderMock.lastDisputeReq.GetReason() != "wrong item" {
		t.Fatalf("POST /seller/returns/ret-a/dispute failed: code=%d", rec.Code)
	}
	if rec := do(http.MethodGet, "/api/v1/seller/shipping-settings", sellerATok, "", nil); rec.Code != http.StatusOK || shippingMock.lastGetSettingsReq.GetShopId() != "shop-a" {
		t.Fatalf("GET /seller/shipping-settings failed: code=%d req=%+v", rec.Code, shippingMock.lastGetSettingsReq)
	}
	if rec := do(http.MethodPut, "/api/v1/seller/shipping-settings", sellerATok, `{"shop_id":"shop-b","enabled_carrier_codes":["ghn"],"default_weight_grams":400}`, nil); rec.Code != http.StatusOK || shippingMock.lastUpdateSettingsReq.GetShopId() != "shop-a" {
		t.Fatalf("PUT /seller/shipping-settings must force JWT shop_id=shop-a: code=%d req=%+v", rec.Code, shippingMock.lastUpdateSettingsReq)
	}

	// --- 5. Admin endpoints + Idempotency-Key + full bank_account_number + audit redaction ---
	if rec := do(http.MethodGet, "/api/v1/admin/returns", adminTok, "", nil); rec.Code != http.StatusOK || orderMock.lastListReturnsReq.GetRoleScope() != "admin" {
		t.Fatalf("GET /admin/returns failed: code=%d", rec.Code)
	}
	if rec := do(http.MethodGet, "/api/v1/admin/returns/ret-a", adminTok, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/returns/ret-a failed: code=%d", rec.Code)
	}
	if rec := do(http.MethodPost, "/api/v1/admin/returns/ret-a/resolve", adminTok, `{"decision":"REFUND","note":"approved","return_fee_bearer":"SELLER"}`, nil); rec.Code != http.StatusOK || orderMock.lastAdminResolveReq.GetDecision() != orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REFUND {
		t.Fatalf("POST /admin/returns/ret-a/resolve failed: code=%d", rec.Code)
	}

	// Admin GET /admin/refunds includes full bank_account_number.
	recAdminRefunds := do(http.MethodGet, "/api/v1/admin/refunds?status=PENDING&order_id=ord-a", adminTok, "", nil)
	if recAdminRefunds.Code != http.StatusOK || !strings.Contains(recAdminRefunds.Body.String(), `"bank_account_number":"01234567897890"`) {
		t.Fatalf("GET /admin/refunds must include full bank_account_number: code=%d body=%s", recAdminRefunds.Code, recAdminRefunds.Body.String())
	}

	// Missing Idempotency-Key on settle & reconciliation resolve -> 400!
	if rec := do(http.MethodPost, "/api/v1/admin/refunds/ref-1/settle", adminTok, `{"provider_refund_id":"TX-1"}`, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "MISSING_IDEMPOTENCY_KEY") {
		t.Fatalf("settle without Idempotency-Key: want 400 MISSING_IDEMPOTENCY_KEY, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/v1/admin/reconciliation/queue/rq-1/resolve", adminTok, `{"action":"ASSIGN","payment_id":"pay-1"}`, nil); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "MISSING_IDEMPOTENCY_KEY") {
		t.Fatalf("reconciliation resolve without Idempotency-Key: want 400 MISSING_IDEMPOTENCY_KEY, got %d (%s)", rec.Code, rec.Body.String())
	}

	// With Idempotency-Key -> 200 and audited without leaking bank_account_number!
	if rec := do(http.MethodPost, "/api/v1/admin/refunds/ref-1/settle", adminTok, `{"provider_refund_id":"TX-1","note":"done"}`, map[string]string{"Idempotency-Key": "idem-settle-1"}); rec.Code != http.StatusOK || paymentMock.lastSettleRefundReq.GetIdempotencyKey() != "idem-settle-1" {
		t.Fatalf("settle with Idempotency-Key failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/admin/reconciliation/queue?status=pending", adminTok, "", nil); rec.Code != http.StatusOK || paymentMock.lastListQueueReq.GetStatus() != "pending" {
		t.Fatalf("GET /admin/reconciliation/queue failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/v1/admin/reconciliation/queue/rq-1/resolve", adminTok, `{"action":"ASSIGN","payment_id":"pay-1","note":"ok"}`, map[string]string{"Idempotency-Key": "idem-rq-1"}); rec.Code != http.StatusOK || paymentMock.lastResolveQueueReq.GetAction() != paymentv1.ReconciliationAction_RECONCILIATION_ACTION_ASSIGN {
		t.Fatalf("POST /admin/reconciliation/queue/rq-1/resolve failed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Admin carriers endpoints + api_token redacted in audit.
	if rec := do(http.MethodGet, "/api/v1/admin/carriers", adminTok, "", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"token_configured":true`) {
		t.Fatalf("GET /admin/carriers failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPut, "/api/v1/admin/carriers/ghn", adminTok, `{"enabled":true,"api_token":"ghn-secret-xyz","fallback_rates":[{"region":"SAME_PROVINCE","base_fee_vnd":22000,"base_weight_grams":500,"extra_fee_vnd_per_500g":5000}]}`, nil); rec.Code != http.StatusOK || shippingMock.lastUpdateCarrierReq.GetApiToken() != "ghn-secret-xyz" {
		t.Fatalf("PUT /admin/carriers/ghn failed: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodPost, "/api/v1/admin/carriers/ghn/test", adminTok, "", nil); rec.Code != http.StatusOK || shippingMock.lastTestCarrierReq.GetCarrierCode() != "ghn" {
		t.Fatalf("POST /admin/carriers/ghn/test failed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// Verify none of the admin audit entries contain raw api_token or bank_account_number.
	userMock.mu.Lock()
	defer userMock.mu.Unlock()
	if len(userMock.auditReqs) < 5 {
		t.Fatalf("expected at least 5 admin audit logs for mutations, got %d", len(userMock.auditReqs))
	}
	for _, a := range userMock.auditReqs {
		for _, leak := range []string{"ghn-secret-xyz", "01234567897890"} {
			if strings.Contains(a.GetBeforeJson(), leak) || strings.Contains(a.GetAfterJson(), leak) {
				t.Fatalf("audit entry %s leaked %q: before=%s after=%s", a.GetAction(), leak, a.GetBeforeJson(), a.GetAfterJson())
			}
		}
	}
}

func TestW2AForgotAndResetPasswordResponseShape(t *testing.T) {
	// Ensure json package is used cleanly via envelope check on forgot/reset password response shape.
	uMock := &w2aUserClient{}
	h := New(Dependencies{
		ServiceName: "w2a-auth-shape",
		Clients:     &clients.Clients{User: uMock, Timeout: 5 * time.Second},
		Logger:      logger.New("w2a-auth-shape", logger.WithWriter(io.Discard)),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", strings.NewReader(`{"email":"test@example.com"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /auth/forgot-password want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var forgotOut map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &forgotOut); err != nil || forgotOut["sent"] != true {
		t.Fatalf("unexpected forgot-password body: %s", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", strings.NewReader(`{"token":"tok-1","new_password":"NewPassword123!"}`)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("POST /auth/reset-password want 200, got %d (%s)", rec2.Code, rec2.Body.String())
	}
	var resetOut map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &resetOut); err != nil || resetOut["ok"] != true {
		t.Fatalf("unexpected reset-password body: %s", rec2.Body.String())
	}
}

func TestW2AShippingLabelProxyAuthAndRevocation(t *testing.T) {
	authMgr, err := auth.NewManager("w2a-label-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-a")
	revoker := &w2aRevokerStub{
		revokedJTI:    map[string]bool{},
		revokedBefore: map[string]int64{},
	}

	var seenAuth string
	labelUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><body>OK " + r.URL.Path + "</body></html>"))
	}))
	defer labelUpstream.Close()

	log := logger.New("w2a-label-test", logger.WithWriter(io.Discard))
	lp, err := proxy.NewShippingLabelProxy(labelUpstream.URL, log)
	if err != nil {
		t.Fatalf("NewShippingLabelProxy: %v", err)
	}

	h := New(Dependencies{
		ServiceName:        "w2a-label-test",
		AuthManager:        authMgr,
		RevocationChecker:  revoker,
		ShippingLabelProxy: lp,
		Logger:             log,
	})

	// 1. Unauthenticated -> 401.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/shipping/labels/GHN-001", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated label GET: want 401, got %d", rec.Code)
	}

	// 2. Authenticated -> 200 proxied with Bearer token.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/shipping/labels/GHN-001", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || seenAuth != "Bearer "+sellerTok || !strings.Contains(rec.Body.String(), "GHN-001") {
		t.Fatalf("authenticated label GET failed: code=%d auth=%q body=%q", rec.Code, seenAuth, rec.Body.String())
	}

	// 3. Revoked token -> 401 before reaching upstream.
	revoker.mu.Lock()
	revoker.revokedBefore["seller-1"] = time.Now().UTC().Unix() + 10
	revoker.mu.Unlock()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/shipping/labels/GHN-001", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked label GET: want 401, got %d", rec.Code)
	}
}

func TestW2ASecurityHeadersSingleValueAcrossAllProxies(t *testing.T) {
	authMgr, err := auth.NewManager("w2a-proxy-hdr-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-a")

	// Upstream intentionally sends all 4 security headers (simulating shipping's nosniff or untrusted CSP)
	// to verify gateway never emits duplicate headers on any proxy route.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "unsafe-url")
		w.Header().Set("Content-Security-Policy", "default-src *")
		switch r.URL.Path {
		case "/api/v1/shipping/labels/GHN-HTML":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<!doctype html><html><body>Label</body></html>"))
		case "/api/v1/shipping/labels/GHN-FORBIDDEN":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden"}`))
		case "/payments/pay-1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: payment.pending\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer upstream.Close()

	log := logger.New("w2a-proxy-hdr-test", logger.WithWriter(io.Discard))
	whProxy, err := proxy.NewWebhookProxy(upstream.URL, log)
	if err != nil {
		t.Fatalf("NewWebhookProxy: %v", err)
	}
	sseProxy, err := proxy.NewSSEProxy(upstream.URL, log)
	if err != nil {
		t.Fatalf("NewSSEProxy: %v", err)
	}
	labelProxy, err := proxy.NewShippingLabelProxy(upstream.URL, log)
	if err != nil {
		t.Fatalf("NewShippingLabelProxy: %v", err)
	}

	h := New(Dependencies{
		ServiceName: "w2a-proxy-hdr-test",
		Clients: &clients.Clients{
			Payment: &w2aPaymentClient{
				payments: map[string]*paymentv1.Payment{
					"pay-1": {PaymentId: "pay-1", UserId: "buyer-1"},
				},
			},
			Timeout: 5 * time.Second,
		},
		AuthManager:          authMgr,
		PaymentWebhookProxy:  whProxy,
		ShippingWebhookProxy: whProxy,
		PaymentSSEProxy:      sseProxy,
		ShippingLabelProxy:   labelProxy,
		Logger:               log,
	})

	const (
		wantAPICSP   = "default-src 'none'; frame-ancestors 'none'"
		wantLabelCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'"
	)

	cases := []struct {
		name       string
		method     string
		path       string
		token      string
		body       string
		wantStatus int
		wantCSP    string
	}{
		{
			name:       "LabelProxyHTML",
			method:     http.MethodGet,
			path:       "/api/v1/shipping/labels/GHN-HTML",
			token:      sellerTok,
			wantStatus: http.StatusOK,
			wantCSP:    wantLabelCSP,
		},
		{
			name:       "LabelProxyNonHTML403",
			method:     http.MethodGet,
			path:       "/api/v1/shipping/labels/GHN-FORBIDDEN",
			token:      sellerTok,
			wantStatus: http.StatusForbidden,
			wantCSP:    wantAPICSP,
		},
		{
			name:       "PaymentWebhookProxy",
			method:     http.MethodPost,
			path:       "/webhooks/payment",
			body:       `{"ok":true}`,
			wantStatus: http.StatusOK,
			wantCSP:    wantAPICSP,
		},
		{
			name:       "ShippingWebhookProxy",
			method:     http.MethodPost,
			path:       "/webhooks/shipping/ghn",
			body:       `{"ok":true}`,
			wantStatus: http.StatusOK,
			wantCSP:    wantAPICSP,
		},
		{
			name:       "PaymentSSEProxy",
			method:     http.MethodGet,
			path:       "/api/v1/payments/pay-1/events",
			token:      buyerTok,
			wantStatus: http.StatusOK,
			wantCSP:    wantAPICSP,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rBody io.Reader
			if tc.body != "" {
				rBody = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, rBody)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			checks := []struct {
				hdr  string
				want string
			}{
				{"Content-Security-Policy", tc.wantCSP},
				{"X-Content-Type-Options", "nosniff"},
				{"X-Frame-Options", "DENY"},
				{"Referrer-Policy", "no-referrer"},
			}
			for _, chk := range checks {
				vals := rec.Header().Values(chk.hdr)
				if len(vals) != 1 || vals[0] != chk.want {
					t.Fatalf("%s values = %q (len=%d), want single [%q]", chk.hdr, vals, len(vals), chk.want)
				}
			}
		})
	}
}
