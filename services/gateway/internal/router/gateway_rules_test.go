package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ruleTestProductClient struct {
	productv1.ProductServiceClient
	mu             sync.Mutex
	products       map[string]*productv1.Product
	lastListMD     metadata.MD
	lastGetMD      metadata.MD
	lastListReq    *productv1.ListProductsRequest
	reviewCalls    int
	createCatCalls int
}

func (f *ruleTestProductClient) ListProducts(ctx context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastListMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastListReq = in
	hasAuth := len(f.lastListMD.Get(auth.MDAuthorization)) > 0

	var out []*productv1.Product
	for _, p := range f.products {
		if !hasAuth && p.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED &&
			p.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK {
			continue
		}
		if in.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED && p.GetStatus() != in.GetStatus() {
			continue
		}
		out = append(out, p)
	}
	return &productv1.ListProductsResponse{Products: out, Total: int64(len(out))}, nil
}

func (f *ruleTestProductClient) GetProduct(ctx context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastGetMD, _ = metadata.FromOutgoingContext(ctx)
	hasAuth := len(f.lastGetMD.Get(auth.MDAuthorization)) > 0

	for _, p := range f.products {
		if (in.GetId() != "" && p.GetId() == in.GetId()) || (in.GetSlug() != "" && p.GetSlug() == in.GetSlug()) {
			if !hasAuth && p.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_APPROVED &&
				p.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK {
				return nil, status.Error(codes.NotFound, "product not found")
			}
			return &productv1.GetProductResponse{Product: p}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "product not found")
}

func (f *ruleTestProductClient) AdminReviewProduct(_ context.Context, in *productv1.AdminReviewProductRequest, _ ...grpc.CallOption) (*productv1.AdminReviewProductResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviewCalls++
	return &productv1.AdminReviewProductResponse{
		Product: &productv1.Product{
			Id:     in.GetProductId(),
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
		},
	}, nil
}

func (f *ruleTestProductClient) CreateCategory(_ context.Context, in *productv1.CreateCategoryRequest, _ ...grpc.CallOption) (*productv1.CreateCategoryResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCatCalls++
	return &productv1.CreateCategoryResponse{
		Category: &productv1.Category{
			Id:   "cat-new-1",
			Name: in.GetName(),
			Slug: in.GetSlug(),
		},
	}, nil
}

type ruleTestUserClient struct {
	userv1.UserServiceClient
	mu            sync.Mutex
	auditReqs     []*userv1.WriteAuditLogRequest
	auditMDs      []metadata.MD
	lastStatusMD  metadata.MD
	lastRoleMD    metadata.MD
	lastShopRevMD metadata.MD
	auditFailN    int
	auditErr      error
	auditAttempt  int
}

func (f *ruleTestUserClient) WriteAuditLog(ctx context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditAttempt++
	md, _ := metadata.FromOutgoingContext(ctx)
	f.auditMDs = append(f.auditMDs, md)
	f.auditReqs = append(f.auditReqs, in)
	if f.auditFailN > 0 && f.auditAttempt <= f.auditFailN {
		err := f.auditErr
		if err == nil {
			err = status.Error(codes.Unavailable, "user service down")
		}
		return nil, err
	}
	return &userv1.WriteAuditLogResponse{
		AuditLog: &userv1.AuditLog{
			Id:         "audit-1",
			ActorId:    in.GetActorId(),
			ActorRole:  in.GetActorRole(),
			Action:     in.GetAction(),
			TargetType: in.GetTargetType(),
			TargetId:   in.GetTargetId(),
			IpAddress:  in.GetIpAddress(),
		},
	}, nil
}

func (f *ruleTestUserClient) AdminUpdateUserStatus(ctx context.Context, in *userv1.AdminUpdateUserStatusRequest, _ ...grpc.CallOption) (*userv1.AdminUpdateUserStatusResponse, error) {
	f.mu.Lock()
	f.lastStatusMD, _ = metadata.FromOutgoingContext(ctx)
	f.mu.Unlock()
	return &userv1.AdminUpdateUserStatusResponse{
		User: &userv1.User{Id: in.GetTargetUserId(), Status: in.GetStatus()},
	}, nil
}

func (f *ruleTestUserClient) AdminUpdateUserRole(ctx context.Context, in *userv1.AdminUpdateUserRoleRequest, _ ...grpc.CallOption) (*userv1.AdminUpdateUserRoleResponse, error) {
	f.mu.Lock()
	f.lastRoleMD, _ = metadata.FromOutgoingContext(ctx)
	f.mu.Unlock()
	return &userv1.AdminUpdateUserRoleResponse{
		User: &userv1.User{Id: in.GetTargetUserId(), Role: in.GetRole()},
	}, nil
}

func (f *ruleTestUserClient) AdminReviewShop(ctx context.Context, in *userv1.AdminReviewShopRequest, _ ...grpc.CallOption) (*userv1.AdminReviewShopResponse, error) {
	f.mu.Lock()
	f.lastShopRevMD, _ = metadata.FromOutgoingContext(ctx)
	f.mu.Unlock()
	return &userv1.AdminReviewShopResponse{
		Shop: &userv1.Shop{Id: in.GetShopId(), Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE},
	}, nil
}

type ruleTestOrderClient struct {
	orderv1.OrderServiceClient
	lastRetryReq *orderv1.RetryPaymentRequest
	lastRetryMD  metadata.MD
	retryResp    *orderv1.RetryPaymentResponse
	retryErr     error
}

func (f *ruleTestOrderClient) RetryPayment(ctx context.Context, in *orderv1.RetryPaymentRequest, _ ...grpc.CallOption) (*orderv1.RetryPaymentResponse, error) {
	f.lastRetryMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastRetryReq = in
	if f.retryErr != nil {
		return nil, f.retryErr
	}
	return f.retryResp, nil
}

type ruleTestPaymentClient struct {
	paymentv1.PaymentServiceClient
	payments    map[string]*paymentv1.Payment
	createCalls int
	lastGetReq  *paymentv1.GetPaymentRequest
	lastGetMD   metadata.MD
}

func (f *ruleTestPaymentClient) CreatePayment(_ context.Context, _ *paymentv1.CreatePaymentRequest, _ ...grpc.CallOption) (*paymentv1.CreatePaymentResponse, error) {
	f.createCalls++
	return &paymentv1.CreatePaymentResponse{
		Payment: &paymentv1.Payment{PaymentId: "should-not-be-called"},
	}, nil
}

func (f *ruleTestPaymentClient) GetPayment(ctx context.Context, in *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	f.lastGetMD, _ = metadata.FromOutgoingContext(ctx)
	f.lastGetReq = in
	p, ok := f.payments[in.GetPaymentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "payment not found")
	}
	return &paymentv1.GetPaymentResponse{Payment: p}, nil
}

func TestPublicProductRoutesAlwaysPublicOnly(t *testing.T) {
	authMgr, err := auth.NewManager("public-products-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-1")

	prodMock := &ruleTestProductClient{
		products: map[string]*productv1.Product{
			"p-approved": {
				Id:     "p-approved",
				ShopId: "shop-1",
				Name:   "Public Shirt",
				Slug:   "public-shirt",
				Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			},
			"p-pending": {
				Id:     "p-pending",
				ShopId: "shop-1",
				Name:   "Pending Shirt",
				Slug:   "pending-shirt",
				Status: productv1.ProductStatus_PRODUCT_STATUS_PENDING,
			},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-test",
		Clients: &clients.Clients{
			Product: prodMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-test", logger.WithWriter(io.Discard)),
	})

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"admin", adminTok},
		{"seller", sellerTok},
	} {
		t.Run("ListProducts_"+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/products?shop_id=shop-1", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "p-pending") {
				t.Fatalf("public GET /products with %s token leaked PENDING product: %s", tc.name, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "p-approved") {
				t.Fatalf("public GET /products missing approved product: %s", rec.Body.String())
			}
			if vals := prodMock.lastListMD.Get(auth.MDAuthorization); len(vals) != 0 {
				t.Fatalf("public GET /products must not forward Authorization metadata, got %v", vals)
			}
		})

		t.Run("GetProductByID_Pending_"+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/products/p-pending", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("public GET /products/p-pending with %s token must return 404, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
		})

		t.Run("GetProductBySlug_Pending_"+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/products/slug/pending-shirt", nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("public GET /products/slug/pending-shirt with %s token must return 404, got %d (%s)", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRetryPaymentCallsOrderServiceAndFetchesNewPayment(t *testing.T) {
	authMgr, err := auth.NewManager("retry-payment-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")

	newExp := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Second)
	orderMock := &ruleTestOrderClient{
		retryResp: &orderv1.RetryPaymentResponse{
			CheckoutGroupId:  "grp-1",
			PaymentId:        "pay-new-2",
			PaymentExpiresAt: timestamppb.New(newExp),
		},
	}
	paymentMock := &ruleTestPaymentClient{
		payments: map[string]*paymentv1.Payment{
			"pay-old-1": {
				PaymentId:    "pay-old-1",
				OrderGroupId: "grp-1",
				UserId:       "buyer-1",
				AmountVnd:    250000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
			},
			"pay-new-2": {
				PaymentId:    "pay-new-2",
				OrderGroupId: "grp-1",
				UserId:       "buyer-1",
				AmountVnd:    250000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				QrUrl:        "https://qr.example/pay-new-2",
				QrCode:       "VIETQR-NEW-2",
				ExpiresAt:    timestamppb.New(newExp),
			},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-test",
		Clients: &clients.Clients{
			Order:   orderMock,
			Payment: paymentMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-test", logger.WithWriter(io.Discard)),
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay-old-1/retry", strings.NewReader(`{"method":"VIETQR"}`))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Idempotency-Key", "idem-retry-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if paymentMock.createCalls != 0 {
		t.Fatalf("handler must NOT call PaymentService.CreatePayment directly, got %d calls", paymentMock.createCalls)
	}
	if orderMock.lastRetryReq == nil {
		t.Fatalf("expected OrderService.RetryPayment to be called")
	}
	if orderMock.lastRetryReq.GetPaymentId() != "pay-old-1" ||
		orderMock.lastRetryReq.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR ||
		orderMock.lastRetryReq.GetIdempotencyKey() != "idem-retry-1" {
		t.Fatalf("unexpected RetryPaymentRequest: %+v", orderMock.lastRetryReq)
	}
	if vals := orderMock.lastRetryMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+buyerTok {
		t.Fatalf("expected buyer JWT forwarded to RetryPayment, got %v", vals)
	}
	if paymentMock.lastGetReq == nil || paymentMock.lastGetReq.GetPaymentId() != "pay-new-2" {
		t.Fatalf("expected GetPayment(pay-new-2), got %+v", paymentMock.lastGetReq)
	}

	var body struct {
		Payment paymentDTO `json:"payment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if body.Payment.PaymentID != "pay-new-2" || body.Payment.QRCode != "VIETQR-NEW-2" || body.Payment.AmountVND != 250000 {
		t.Fatalf("unexpected retry JSON shape: %+v", body.Payment)
	}
}

func TestMapPaymentAndEndpointsIncludePlatformDiscountVND(t *testing.T) {
	// Direct mapPayment check: non-zero and zero platform_discount_vnd must both serialize in JSON.
	protoPayment := &paymentv1.Payment{
		PaymentId:    "pay-alloc-1",
		OrderGroupId: "grp-alloc-1",
		UserId:       "buyer-1",
		AmountVnd:    195000,
		Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
		ShopAllocations: []*paymentv1.ShopAllocation{
			{
				OrderId:             "ord-1",
				ShopId:              "shop-1",
				SubtotalVnd:         200000,
				CommissionVnd:       10000,
				ShippingFeeVnd:      30000,
				PlatformDiscountVnd: 35000,
			},
			{
				OrderId:             "ord-2",
				ShopId:              "shop-2",
				SubtotalVnd:         100000,
				CommissionVnd:       5000,
				ShippingFeeVnd:      20000,
				PlatformDiscountVnd: 0,
			},
		},
	}

	dto := mapPayment(protoPayment)
	if len(dto.ShopAllocations) != 2 || dto.ShopAllocations[0].PlatformDiscountVND != 35000 || dto.ShopAllocations[1].PlatformDiscountVND != 0 {
		t.Fatalf("mapPayment allocations mismatch: %+v", dto.ShopAllocations)
	}
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal paymentDTO: %v", err)
	}
	var decoded struct {
		ShopAllocations []map[string]any `json:"shop_allocations"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal paymentDTO: %v", err)
	}
	if got, ok := decoded.ShopAllocations[0]["platform_discount_vnd"]; !ok || got != float64(35000) {
		t.Fatalf("expected shop_allocations[0].platform_discount_vnd=35000, got ok=%v val=%v (json=%s)", ok, got, string(raw))
	}
	if got, ok := decoded.ShopAllocations[1]["platform_discount_vnd"]; !ok || got != float64(0) {
		t.Fatalf("expected shop_allocations[1].platform_discount_vnd=0 key present, got ok=%v val=%v (json=%s)", ok, got, string(raw))
	}

	// Verify both GET /api/v1/payments/{id} and POST /api/v1/payments/{id}/retry expose platform_discount_vnd.
	authMgr, err := auth.NewManager("alloc-endpoint-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")

	retried := &paymentv1.Payment{
		PaymentId:       "pay-alloc-2",
		OrderGroupId:    "grp-alloc-1",
		UserId:          "buyer-1",
		AmountVnd:       195000,
		Method:          paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		Status:          paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
		ShopAllocations: protoPayment.ShopAllocations,
	}
	orderMock := &ruleTestOrderClient{
		retryResp: &orderv1.RetryPaymentResponse{
			CheckoutGroupId: "grp-alloc-1",
			PaymentId:       "pay-alloc-2",
		},
	}
	paymentMock := &ruleTestPaymentClient{
		payments: map[string]*paymentv1.Payment{
			"pay-alloc-1": protoPayment,
			"pay-alloc-2": retried,
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-test",
		Clients: &clients.Clients{
			Order:   orderMock,
			Payment: paymentMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-test", logger.WithWriter(io.Discard)),
	})

	checkAllocJSON := func(t *testing.T, body []byte) {
		t.Helper()
		var wrap struct {
			Payment struct {
				ShopAllocations []map[string]any `json:"shop_allocations"`
			} `json:"payment"`
		}
		if err := json.Unmarshal(body, &wrap); err != nil {
			t.Fatalf("decode response: %v (body=%s)", err, string(body))
		}
		if len(wrap.Payment.ShopAllocations) != 2 {
			t.Fatalf("expected 2 allocations, got %d (body=%s)", len(wrap.Payment.ShopAllocations), string(body))
		}
		if v, ok := wrap.Payment.ShopAllocations[0]["platform_discount_vnd"]; !ok || v != float64(35000) {
			t.Fatalf("allocation[0] platform_discount_vnd mismatch: ok=%v v=%v", ok, v)
		}
		if v, ok := wrap.Payment.ShopAllocations[1]["platform_discount_vnd"]; !ok || v != float64(0) {
			t.Fatalf("allocation[1] platform_discount_vnd mismatch: ok=%v v=%v", ok, v)
		}
	}

	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/payments/pay-alloc-1", nil)
	reqGet.Header.Set("Authorization", "Bearer "+buyerTok)
	recGet := httptest.NewRecorder()
	h.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("GET /payments/pay-alloc-1 want 200, got %d (%s)", recGet.Code, recGet.Body.String())
	}
	checkAllocJSON(t, recGet.Body.Bytes())

	reqRetry := httptest.NewRequest(http.MethodPost, "/api/v1/payments/pay-alloc-1/retry", strings.NewReader(`{"method":"VIETQR"}`))
	reqRetry.Header.Set("Authorization", "Bearer "+buyerTok)
	recRetry := httptest.NewRecorder()
	h.ServeHTTP(recRetry, reqRetry)
	if recRetry.Code != http.StatusOK {
		t.Fatalf("POST /payments/pay-alloc-1/retry want 200, got %d (%s)", recRetry.Code, recRetry.Body.String())
	}
	checkAllocJSON(t, recRetry.Body.Bytes())
}

func TestAdminAuditMiddlewareCallsWriteAuditLogAndSkipsUserAndGET(t *testing.T) {
	authMgr, err := auth.NewManager("admin-audit-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-99", "super_admin", "")

	userMock := &ruleTestUserClient{}
	prodMock := &ruleTestProductClient{
		products: map[string]*productv1.Product{
			"prod-123": {Id: "prod-123", Status: productv1.ProductStatus_PRODUCT_STATUS_PENDING},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-test",
		Clients: &clients.Clients{
			User:    userMock,
			Product: prodMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-test", logger.WithWriter(io.Discard)),
	})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		var rBody io.Reader
		if body != "" {
			rBody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rBody)
		req.RemoteAddr = "198.51.100.44:34567"
		req.Header.Set("Authorization", "Bearer "+adminTok)
		req.Header.Set("X-Forwarded-For", "1.2.3.4") // Untrusted -> must be ignored!
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. Admin GET must NOT write audit log.
	if rec := do(http.MethodGet, "/api/v1/admin/products", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/products want 200, got %d", rec.Code)
	}

	// 2. Routes self-audited by user service must NOT duplicate audit log.
	if rec := do(http.MethodPatch, "/api/v1/admin/users/u-1/status", `{"status":"LOCKED","reason":"spam"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH /admin/users/u-1/status want 200, got %d", rec.Code)
	}
	if rec := do(http.MethodPatch, "/api/v1/admin/users/u-1/role", `{"role":"buyer","reason":"demote"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH /admin/users/u-1/role want 200, got %d", rec.Code)
	}
	if rec := do(http.MethodPost, "/api/v1/admin/shops/s-1/review", `{"decision":"APPROVE","reason":"ok"}`); rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/shops/s-1/review want 200, got %d", rec.Code)
	}
	if len(userMock.auditReqs) != 0 {
		t.Fatalf("GET and user-service self-audited routes must not trigger gateway WriteAuditLog, got %d calls: %+v", len(userMock.auditReqs), userMock.auditReqs)
	}

	// 3. Admin review product (2xx) -> MUST call WriteAuditLog with verified claims, route pattern, target id, and ClientIP.
	if rec := do(http.MethodPost, "/api/v1/admin/products/prod-123/review", `{"decision":"APPROVE","reason":"looks good"}`); rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/products/prod-123/review want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(userMock.auditReqs) != 1 {
		t.Fatalf("expected 1 WriteAuditLog call after product review, got %d", len(userMock.auditReqs))
	}
	got := userMock.auditReqs[0]
	if got.GetActorId() != "admin-99" || got.GetActorRole() != "super_admin" {
		t.Fatalf("unexpected actor: %+v", got)
	}
	if got.GetAction() != "POST /api/v1/admin/products/{id}/review" {
		t.Fatalf("unexpected action: got %q", got.GetAction())
	}
	if got.GetTargetId() != "prod-123" || got.GetTargetType() == "" {
		t.Fatalf("unexpected target: type=%q id=%q", got.GetTargetType(), got.GetTargetId())
	}
	if got.GetIpAddress() != "198.51.100.44" {
		t.Fatalf("expected ClientIP 198.51.100.44 (ignoring spoofed XFF), got %q", got.GetIpAddress())
	}

	// 4. Admin create category (no route param, 201 Created) -> MUST call WriteAuditLog with non-empty target_id.
	if rec := do(http.MethodPost, "/api/v1/admin/categories", `{"name":"Ao","slug":"ao"}`); rec.Code != http.StatusCreated {
		t.Fatalf("POST /admin/categories want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(userMock.auditReqs) != 2 {
		t.Fatalf("expected 2 WriteAuditLog calls after create category, got %d", len(userMock.auditReqs))
	}
	catAudit := userMock.auditReqs[1]
	if catAudit.GetAction() != "POST /api/v1/admin/categories" || catAudit.GetTargetId() != "cat-new-1" {
		t.Fatalf("unexpected category audit: %+v", catAudit)
	}
}

type unfilteredProductClient struct {
	productv1.ProductServiceClient
	products []*productv1.Product
}

func (f *unfilteredProductClient) ListProducts(_ context.Context, _ *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	return &productv1.ListProductsResponse{
		Products: f.products,
		Total:    int64(len(f.products)),
	}, nil
}

func (f *unfilteredProductClient) GetProduct(_ context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	for _, p := range f.products {
		if (in.GetId() != "" && p.GetId() == in.GetId()) || (in.GetSlug() != "" && p.GetSlug() == in.GetSlug()) {
			return &productv1.GetProductResponse{Product: p}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "product not found")
}

func TestPublicProductRoutesFailClosedOnUnspecifiedAndSyncTotal(t *testing.T) {
	prodMock := &unfilteredProductClient{
		products: []*productv1.Product{
			{Id: "p-approved", Name: "Approved", Slug: "approved-shirt", Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED},
			{Id: "p-unspecified", Name: "Unspecified", Slug: "unspecified-shirt", Status: productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED},
			{Id: "p-pending", Name: "Pending", Slug: "pending-shirt", Status: productv1.ProductStatus_PRODUCT_STATUS_PENDING},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-test",
		Clients: &clients.Clients{
			Product: prodMock,
			Timeout: 5 * time.Second,
		},
		Logger: logger.New("gateway-test", logger.WithWriter(io.Discard)),
	})

	// ListProducts must fail-closed on UNSPECIFIED and PENDING, and total must match filtered count (1, not 3).
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/products", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /products want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var listBody struct {
		Items []productDTO `json:"items"`
		Total int64        `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode /products: %v", err)
	}
	if len(listBody.Items) != 1 || listBody.Items[0].ID != "p-approved" {
		t.Fatalf("expected only p-approved in public list (fail-closed on UNSPECIFIED/PENDING), got %+v", listBody.Items)
	}
	if listBody.Total != 1 {
		t.Fatalf("expected total=1 matching filtered items, got %d (body=%s)", listBody.Total, rec.Body.String())
	}

	// GetProduct by ID and by Slug must fail-closed (404) on UNSPECIFIED.
	for _, path := range []string{"/api/v1/products/p-unspecified", "/api/v1/products/slug/unspecified-shirt"} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s with UNSPECIFIED status must return 404, got %d (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

func TestGatewayForwardsClientIPMetadataToDownstreamGRPC(t *testing.T) {
	authMgr, err := auth.NewManager("client-ip-md-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "super_admin", "")
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")

	userMock := &ruleTestUserClient{}
	orderMock := &fakeOrderClient{}
	tp := middleware.NewTrustedProxies("10.0.0.0/8", nil)

	h := New(Dependencies{
		ServiceName: "gateway-test",
		Clients: &clients.Clients{
			User:    userMock,
			Order:   orderMock,
			Timeout: 5 * time.Second,
		},
		AuthManager:    authMgr,
		TrustedProxies: tp,
		Logger:         logger.New("gateway-test", logger.WithWriter(io.Discard)),
	})

	// 1. Untrusted peer with spoofed XFF on all 3 user self-audited admin routes -> must forward peer IP, never spoofed XFF.
	for _, tc := range []struct {
		method string
		path   string
		body   string
		getMD  func() metadata.MD
	}{
		{http.MethodPatch, "/api/v1/admin/users/u-1/status", `{"status":"LOCKED","reason":"r"}`, func() metadata.MD { return userMock.lastStatusMD }},
		{http.MethodPatch, "/api/v1/admin/users/u-1/role", `{"role":"buyer","reason":"r"}`, func() metadata.MD { return userMock.lastRoleMD }},
		{http.MethodPost, "/api/v1/admin/shops/s-1/review", `{"decision":"APPROVE","reason":"r"}`, func() metadata.MD { return userMock.lastShopRevMD }},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.RemoteAddr = "198.51.100.44:54321"
		req.Header.Set("Authorization", "Bearer "+adminTok)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s want 200, got %d (%s)", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		if vals := tc.getMD().Get("x-client-ip"); len(vals) != 1 || vals[0] != "198.51.100.44" {
			t.Fatalf("%s %s expected x-client-ip=[198.51.100.44], got %v", tc.method, tc.path, vals)
		}
	}

	// 2. Trusted proxy peer with multi-hop XFF on admin review shop & buyer checkout -> must forward resolved right-to-left ClientIP.
	reqShop := httptest.NewRequest(http.MethodPost, "/api/v1/admin/shops/s-2/review", strings.NewReader(`{"decision":"APPROVE","reason":"ok"}`))
	reqShop.RemoteAddr = "10.0.0.9:43210"
	reqShop.Header.Set("Authorization", "Bearer "+adminTok)
	reqShop.Header.Set("X-Forwarded-For", "1.1.1.1, 203.0.113.88, 10.0.0.2")
	recShop := httptest.NewRecorder()
	h.ServeHTTP(recShop, reqShop)
	if recShop.Code != http.StatusOK {
		t.Fatalf("trusted review shop want 200, got %d", recShop.Code)
	}
	if vals := userMock.lastShopRevMD.Get("x-client-ip"); len(vals) != 1 || vals[0] != "203.0.113.88" {
		t.Fatalf("trusted review shop expected x-client-ip=[203.0.113.88], got %v", vals)
	}

	reqCheckout := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", strings.NewReader(`{"address_id":"a1","from_cart":true,"payment_method":"VIETQR"}`))
	reqCheckout.RemoteAddr = "10.0.0.9:43211"
	reqCheckout.Header.Set("Authorization", "Bearer "+buyerTok)
	reqCheckout.Header.Set("Idempotency-Key", "idem-ip-1")
	reqCheckout.Header.Set("X-Forwarded-For", "203.0.113.99")
	recCheckout := httptest.NewRecorder()
	h.ServeHTTP(recCheckout, reqCheckout)
	if recCheckout.Code != http.StatusCreated {
		t.Fatalf("checkout want 201, got %d (%s)", recCheckout.Code, recCheckout.Body.String())
	}
	if vals := orderMock.lastMD.Get("x-client-ip"); len(vals) != 1 || vals[0] != "203.0.113.99" {
		t.Fatalf("checkout expected x-client-ip=[203.0.113.99], got %v", vals)
	}
}
