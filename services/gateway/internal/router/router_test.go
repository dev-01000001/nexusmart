package router

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/proxy"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type fakeOrderClient struct {
	orderv1.OrderServiceClient
	lastMD           metadata.MD
	lastCheckoutReq  *orderv1.CreateCheckoutRequest
	lastRetryReq     *orderv1.RetryPaymentRequest
	idempotentReplay bool
}

func (f *fakeOrderClient) RetryPayment(ctx context.Context, in *orderv1.RetryPaymentRequest, _ ...grpc.CallOption) (*orderv1.RetryPaymentResponse, error) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastRetryReq = in
	switch in.GetPaymentId() {
	case "pay-cancelled", "pay-expired", "pay-succeeded":
		return nil, status.Error(codes.FailedPrecondition, "order group cannot be retried")
	}
	return &orderv1.RetryPaymentResponse{
		CheckoutGroupId: "grp-1",
		PaymentId:       "pay-retried",
	}, nil
}

func (f *fakeOrderClient) CreateCheckout(ctx context.Context, in *orderv1.CreateCheckoutRequest, _ ...grpc.CallOption) (*orderv1.CreateCheckoutResponse, error) {
	f.lastMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastCheckoutReq = in
	return &orderv1.CreateCheckoutResponse{
		CheckoutGroupId:  "grp-1",
		PaymentId:        "pay-1",
		PaymentMethod:    in.GetPaymentMethod(),
		PaymentStatus:    "PENDING",
		TotalPayableVnd:  250000,
		IdempotentReplay: f.idempotentReplay,
		Orders: []*orderv1.Order{
			{
				Id:              "ord-1",
				OrderCode:       "NM-001",
				BuyerId:         in.GetBuyerId(),
				ShopId:          "shop-1",
				Status:          orderv1.OrderStatus_ORDER_STATUS_PENDING,
				PaymentMethod:   in.GetPaymentMethod(),
				TotalPayableVnd: 250000,
			},
		},
	}, nil
}

type fakeProductClient struct {
	productv1.ProductServiceClient
	lastCreateReq *productv1.CreateProductRequest
}

func (f *fakeProductClient) CreateProduct(_ context.Context, in *productv1.CreateProductRequest, _ ...grpc.CallOption) (*productv1.CreateProductResponse, error) {
	f.lastCreateReq = in
	return &productv1.CreateProductResponse{
		Product: &productv1.Product{
			Id:         "prod-1",
			ShopId:     in.GetShopId(),
			CategoryId: in.GetCategoryId(),
			Name:       in.GetName(),
			Status:     productv1.ProductStatus_PRODUCT_STATUS_PENDING,
		},
	}, nil
}

type fakePaymentClient struct {
	paymentv1.PaymentServiceClient
	payments      map[string]*paymentv1.Payment
	lastCreateReq *paymentv1.CreatePaymentRequest
}

func (f *fakePaymentClient) CreatePayment(_ context.Context, in *paymentv1.CreatePaymentRequest, _ ...grpc.CallOption) (*paymentv1.CreatePaymentResponse, error) {
	f.lastCreateReq = in
	return &paymentv1.CreatePaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId:    "pay-retried",
			OrderGroupId: in.GetOrderGroupId(),
			UserId:       in.GetUserId(),
			AmountVnd:    in.GetAmountVnd(),
			Method:       in.GetMethod(),
			Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
		},
	}, nil
}

func (f *fakePaymentClient) GetPayment(_ context.Context, in *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	p, ok := f.payments[in.GetPaymentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "payment not found")
	}
	return &paymentv1.GetPaymentResponse{Payment: p}, nil
}

func (f *fakePaymentClient) GetLedgerSummary(_ context.Context, _ *paymentv1.GetLedgerSummaryRequest, _ ...grpc.CallOption) (*paymentv1.GetLedgerSummaryResponse, error) {
	return &paymentv1.GetLedgerSummaryResponse{
		TotalDebitsVnd:  500000,
		TotalCreditsVnd: 500000,
		IsBalanced:      true,
		Accounts: []*paymentv1.AccountBalance{
			{AccountCode: "ESCROW", AccountType: "LIABILITY", BalanceVnd: 500000},
		},
	}, nil
}

func TestGatewayRouterCheckoutRBACOwnershipAndProxies(t *testing.T) {
	shutdown, err := telemetry.Init(context.Background(), telemetry.Config{ServiceName: "gateway-router-test"})
	if err != nil {
		t.Fatalf("telemetry.Init: %v", err)
	}
	defer func() { _ = shutdown(context.Background()) }()

	authMgr, err := auth.NewManager("router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	otherBuyerTok, _, _, _ := authMgr.SignAccessToken("buyer-2", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-42")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	orderMock := &fakeOrderClient{}
	productMock := &fakeProductClient{}
	paymentMock := &fakePaymentClient{
		payments: map[string]*paymentv1.Payment{
			"pay-1": {
				PaymentId:    "pay-1",
				OrderGroupId: "grp-1",
				UserId:       "buyer-1",
				AmountVnd:    250000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
			},
			"pay-cancelled": {
				PaymentId: "pay-cancelled",
				UserId:    "buyer-1",
				AmountVnd: 250000,
				Method:    paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:    paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED,
			},
			"pay-expired": {
				PaymentId: "pay-expired",
				UserId:    "buyer-1",
				AmountVnd: 250000,
				Method:    paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:    paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED,
			},
			"pay-succeeded": {
				PaymentId: "pay-succeeded",
				UserId:    "buyer-1",
				AmountVnd: 250000,
				Method:    paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:    paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED,
			},
			"pay-retried": {
				PaymentId:    "pay-retried",
				OrderGroupId: "grp-1",
				UserId:       "buyer-1",
				AmountVnd:    250000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
			},
		},
	}

	webhookUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/payments/pay-1/events" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: payment.succeeded\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return
		}
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"path":"` + r.URL.Path + `","sig":"` + r.Header.Get("X-Signature") + `","len":` + jsonNumber(len(raw)) + `}`))
	}))
	defer webhookUpstream.Close()

	log := logger.New("gateway-router-test", logger.WithWriter(io.Discard))
	wp, _ := proxy.NewWebhookProxy(webhookUpstream.URL, log)
	sp, _ := proxy.NewSSEProxy(webhookUpstream.URL, log)
	reg := prometheus.NewRegistry()
	red := metrics.NewRED("gateway-router-test", reg)
	ops := metrics.NewOpsHandler("gateway-router-test", nil, reg)

	h := New(Dependencies{
		ServiceName: "gateway-router-test",
		Clients: &clients.Clients{
			Order:   orderMock,
			Product: productMock,
			Payment: paymentMock,
			Timeout: 5 * time.Second,
		},
		AuthManager:          authMgr,
		RED:                  red,
		OpsHandler:           ops,
		PaymentWebhookProxy:  wp,
		ShippingWebhookProxy: wp,
		PaymentSSEProxy:      sp,
		Logger:               log,
	})

	// 1. Ops /healthz works without auth.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on /healthz, got %d", rec.Code)
	}

	// 2. Checkout missing Idempotency-Key -> 400.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", strings.NewReader(`{"address_id":"a1","payment_method":"COD"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without Idempotency-Key, got %d", rec.Code)
	}

	// 3. Fresh checkout -> 201 + forwards Idempotency-Key & auth metadata.
	orderMock.idempotentReplay = false
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/checkout", strings.NewReader(`{"address_id":"a1","from_cart":true,"payment_method":"VIETQR","sales_channel":"MARKETPLACE"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Idempotency-Key", "idem-123")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 on fresh checkout, got %d (%s)", rec.Code, rec.Body.String())
	}
	if orderMock.lastCheckoutReq.GetIdempotencyKey() != "idem-123" || orderMock.lastCheckoutReq.GetBuyerId() != "buyer-1" {
		t.Fatalf("unexpected checkout req forwarded: %+v", orderMock.lastCheckoutReq)
	}
	if vals := orderMock.lastMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+buyerTok {
		t.Fatalf("expected buyer Bearer token forwarded, got %+v", orderMock.lastMD)
	}
	if vals := orderMock.lastMD.Get("x-user-id"); len(vals) != 0 {
		t.Fatalf("plain identity metadata must not be forwarded, got %+v", orderMock.lastMD)
	}

	// 4. Replay checkout (`idempotent_replay=true`) -> 200.
	orderMock.idempotentReplay = true
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/checkout", strings.NewReader(`{"address_id":"a1","from_cart":true,"payment_method":"VIETQR","sales_channel":"MARKETPLACE"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Idempotency-Key", "idem-123")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on idempotent replay checkout, got %d", rec.Code)
	}

	// 5. RBAC: buyer blocked on seller & admin routes; seller creates product with shop_id from JWT.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/products", strings.NewReader(`{"name":"Phone"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for buyer on seller route, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/products", strings.NewReader(`{"name":"Phone","category_id":"cat-1"}`))
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || productMock.lastCreateReq.GetShopId() != "shop-42" {
		t.Fatalf("expected 201 and shop_id=shop-42, got code=%d shop_id=%q", rec.Code, productMock.lastCreateReq.GetShopId())
	}

	// 6. Admin ledger summary requires admin role and returns integer VND + is_balanced.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/ledger/summary", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on admin ledger summary, got %d", rec.Code)
	}
	var ledgerBody map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ledgerBody); err != nil {
		t.Fatalf("decode ledger json: %v", err)
	}
	if ledgerBody["is_balanced"] != true {
		t.Fatalf("expected is_balanced=true, got %+v", ledgerBody)
	}

	// 7. Payment object ownership: buyer-2 gets 403 on buyer-1's payment.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay-1", nil)
	req.Header.Set("Authorization", "Bearer "+otherBuyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner buyer on GetPayment, got %d", rec.Code)
	}

	// 8. Webhook proxy works WITHOUT JWT.
	rawPayload := []byte(`{"provider":"vietqr_mock","amount_vnd":250000}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/webhooks/payment", bytes.NewReader(rawPayload))
	req.Header.Set("X-Signature", "deadbeef")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sig":"deadbeef"`) {
		t.Fatalf("webhook proxy failed: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 9. SSE route requires auth, enforces ownership, sets X-Trace-Id, and streams events.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay-1/events", nil)
	req.Header.Set("Authorization", "Bearer "+otherBuyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner buyer on payment SSE, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay-1/events", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("X-Trace-Id") == "" || !strings.Contains(rec.Body.String(), "payment.succeeded") {
		t.Fatalf("SSE proxy failed: code=%d trace=%q body=%q", rec.Code, rec.Header().Get("X-Trace-Id"), rec.Body.String())
	}

	// 10. Seller token without shop_id blocked on /api/v1/seller/*.
	sellerNoShopTok, _, _, _ := authMgr.SignAccessToken("seller-empty", "seller", "")
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/seller/products", strings.NewReader(`{"name":"Phone"}`))
	req.Header.Set("Authorization", "Bearer "+sellerNoShopTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for seller with empty shop_id, got %d", rec.Code)
	}

	// 11. Payment retry requires Idempotency-Key (F5), rejects terminal statuses (CANCELLED, EXPIRED, SUCCEEDED), and delegates method switching (VIETQR/ZALOPAY/COD) to OrderService.RetryPayment.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay-1/retry", strings.NewReader(`{"method":"VIETQR"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 retrying payment without Idempotency-Key, got %d (%s)", rec.Code, rec.Body.String())
	}

	for _, pid := range []string{"pay-cancelled", "pay-expired", "pay-succeeded"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/api/v1/payments/"+pid+"/retry", strings.NewReader(`{"method":"VIETQR"}`))
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		req.Header.Set("Idempotency-Key", "idem-term-"+pid)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409 retrying terminal payment %s, got %d (%s)", pid, rec.Code, rec.Body.String())
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay-1/retry", strings.NewReader(`{"method":"COD"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Idempotency-Key", "idem-switch-cod")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || orderMock.lastRetryReq == nil || orderMock.lastRetryReq.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD {
		t.Fatalf("expected 200 delegating VIETQR -> COD switch to OrderService.RetryPayment, got code=%d req=%+v (%s)", rec.Code, orderMock.lastRetryReq, rec.Body.String())
	}

	// Valid PENDING VIETQR -> VIETQR retry delegates to OrderService.RetryPayment and never calls Payment.CreatePayment.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay-1/retry", strings.NewReader(`{"method":"VIETQR"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Idempotency-Key", "idem-retry-vqr")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || orderMock.lastRetryReq == nil || orderMock.lastRetryReq.GetPaymentId() != "pay-1" || paymentMock.lastCreateReq != nil {
		t.Fatalf("expected 200 via OrderService.RetryPayment without CreatePayment, got code=%d retryReq=%+v createReq=%+v", rec.Code, orderMock.lastRetryReq, paymentMock.lastCreateReq)
	}
	if !strings.Contains(rec.Body.String(), `"payment_id":"pay-retried"`) {
		t.Fatalf("expected new payment payload in retry response, got %s", rec.Body.String())
	}
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
