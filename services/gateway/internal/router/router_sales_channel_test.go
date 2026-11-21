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

	"github.com/go-chi/chi/v5"
	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type salesChannelOrderMock struct {
	orderv1.OrderServiceClient
	checkoutCalls int
	lastCheckout  *orderv1.CreateCheckoutRequest
	lastList      *orderv1.ListOrdersRequest
	getCalls      int
	orders        map[string]*orderv1.Order
}

func (m *salesChannelOrderMock) CreateCheckout(_ context.Context, in *orderv1.CreateCheckoutRequest, _ ...grpc.CallOption) (*orderv1.CreateCheckoutResponse, error) {
	m.checkoutCalls++
	m.lastCheckout = in
	return &orderv1.CreateCheckoutResponse{
		CheckoutGroupId:  "grp-sc-1",
		PaymentId:        "pay-sc-1",
		PaymentMethod:    in.GetPaymentMethod(),
		PaymentStatus:    "PENDING",
		TotalPayableVnd:  150000,
		SalesChannel:     in.GetSalesChannel(),
		StorefrontShopId: in.GetStorefrontShopId(),
		Orders: []*orderv1.Order{
			{
				Id:               "ord-sc-1",
				OrderCode:        "NM-SC-001",
				BuyerId:          in.GetBuyerId(),
				ShopId:           "shop-a",
				Status:           orderv1.OrderStatus_ORDER_STATUS_PENDING,
				PaymentMethod:    in.GetPaymentMethod(),
				TotalPayableVnd:  150000,
				SalesChannel:     in.GetSalesChannel(),
				StorefrontShopId: in.GetStorefrontShopId(),
			},
		},
	}, nil
}

func (m *salesChannelOrderMock) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	m.getCalls++
	o, ok := m.orders[in.GetOrderId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return &orderv1.GetOrderResponse{Order: o}, nil
}

func (m *salesChannelOrderMock) ListOrders(_ context.Context, in *orderv1.ListOrdersRequest, _ ...grpc.CallOption) (*orderv1.ListOrdersResponse, error) {
	m.lastList = in
	out := make([]*orderv1.Order, 0, len(m.orders))
	for _, o := range m.orders {
		if in.GetSalesChannel() != "" && normalizeSalesChannelOutput(o.GetSalesChannel()) != in.GetSalesChannel() {
			continue
		}
		out = append(out, o)
	}
	return &orderv1.ListOrdersResponse{Orders: out, Total: int64(len(out))}, nil
}

type salesChannelCartMock struct {
	cartv1.CartServiceClient
	lastGet   *cartv1.GetCartRequest
	lastClear *cartv1.ClearItemsRequest
}

func (m *salesChannelCartMock) GetCart(_ context.Context, in *cartv1.GetCartRequest, _ ...grpc.CallOption) (*cartv1.GetCartResponse, error) {
	m.lastGet = in
	return &cartv1.GetCartResponse{
		Cart: &cartv1.Cart{
			UserId: in.GetUserId(),
			ShopGroups: []*cartv1.CartShopGroup{
				{ShopId: in.GetShopId(), SubtotalVnd: 100000},
			},
			TotalItems: 1,
			TotalVnd:   100000,
		},
	}, nil
}

func (m *salesChannelCartMock) ClearItems(_ context.Context, in *cartv1.ClearItemsRequest, _ ...grpc.CallOption) (*cartv1.ClearItemsResponse, error) {
	m.lastClear = in
	return &cartv1.ClearItemsResponse{Ok: true}, nil
}

type salesChannelPaymentMock struct {
	paymentv1.PaymentServiceClient
	payments      map[string]*paymentv1.Payment
	lastCreate    *paymentv1.CreatePaymentRequest
	lastLedger    *paymentv1.GetLedgerSummaryRequest
	lastReconcile *paymentv1.RunReconciliationRequest
}

func (m *salesChannelPaymentMock) GetPayment(_ context.Context, in *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	p, ok := m.payments[in.GetPaymentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "payment not found")
	}
	return &paymentv1.GetPaymentResponse{Payment: p}, nil
}

func (m *salesChannelPaymentMock) CreatePayment(_ context.Context, in *paymentv1.CreatePaymentRequest, _ ...grpc.CallOption) (*paymentv1.CreatePaymentResponse, error) {
	m.lastCreate = in
	return &paymentv1.CreatePaymentResponse{
		Payment: &paymentv1.Payment{
			PaymentId:        "pay-retry-sf",
			OrderGroupId:     in.GetOrderGroupId(),
			UserId:           in.GetUserId(),
			AmountVnd:        in.GetAmountVnd(),
			Method:           in.GetMethod(),
			Status:           paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
			ShopAllocations:  in.GetShopAllocations(),
			SalesChannel:     in.GetSalesChannel(),
			StorefrontShopId: in.GetStorefrontShopId(),
		},
	}, nil
}

func (m *salesChannelPaymentMock) GetLedgerSummary(_ context.Context, in *paymentv1.GetLedgerSummaryRequest, _ ...grpc.CallOption) (*paymentv1.GetLedgerSummaryResponse, error) {
	m.lastLedger = in
	return &paymentv1.GetLedgerSummaryResponse{
		TotalDebitsVnd:  300000,
		TotalCreditsVnd: 300000,
		IsBalanced:      true,
		SalesChannel:    in.GetSalesChannel(),
	}, nil
}

func (m *salesChannelPaymentMock) RunReconciliation(_ context.Context, in *paymentv1.RunReconciliationRequest, _ ...grpc.CallOption) (*paymentv1.RunReconciliationResponse, error) {
	m.lastReconcile = in
	ch := in.GetSalesChannel()
	if ch == "" {
		ch = "STOREFRONT"
	}
	return &paymentv1.RunReconciliationResponse{
		RunId:        "rec-1",
		MatchedCount: 1,
		SalesChannel: in.GetSalesChannel(),
		Items: []*paymentv1.ReconciliationItem{
			{
				PaymentId:         "pay-sf-1",
				ProviderTxId:      "tx-1",
				LocalAmountVnd:    150000,
				ProviderAmountVnd: 150000,
				Status:            paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_MATCHED,
				SalesChannel:      ch,
				StorefrontShopId:  "shop-a",
			},
		},
	}, nil
}

type salesChannelPromoMock struct {
	promotionv1.PromotionServiceClient
	lastList    *promotionv1.ListAvailableVouchersRequest
	lastPreview *promotionv1.PreviewDiscountsRequest
}

func (m *salesChannelPromoMock) ListAvailableVouchers(_ context.Context, in *promotionv1.ListAvailableVouchersRequest, _ ...grpc.CallOption) (*promotionv1.ListAvailableVouchersResponse, error) {
	m.lastList = in
	return &promotionv1.ListAvailableVouchersResponse{}, nil
}

func (m *salesChannelPromoMock) PreviewDiscounts(_ context.Context, in *promotionv1.PreviewDiscountsRequest, _ ...grpc.CallOption) (*promotionv1.PreviewDiscountsResponse, error) {
	m.lastPreview = in
	return &promotionv1.PreviewDiscountsResponse{Breakdown: &promotionv1.DiscountBreakdown{}}, nil
}

func TestSellerGetOrderRejectsEmptyShopIDEvenIfOrderShopIDEmpty(t *testing.T) {
	authMgr, err := auth.NewManager("b3-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	sellerEmptyShopTok, _, _, _ := authMgr.SignAccessToken("seller-empty", "seller", "")
	sellerValidTok, _, _, _ := authMgr.SignAccessToken("seller-ok", "seller", "shop-a")

	orderMock := &salesChannelOrderMock{
		orders: map[string]*orderv1.Order{
			"ord-empty-shop": {Id: "ord-empty-shop", BuyerId: "b1", ShopId: ""},
			"ord-shop-a":     {Id: "ord-shop-a", BuyerId: "b1", ShopId: "shop-a", SalesChannel: "STOREFRONT", StorefrontShopId: "shop-a"},
		},
	}
	c := &clients.Clients{Order: orderMock, Timeout: 5 * time.Second}
	log := logger.New("b3-test", logger.WithWriter(io.Discard))

	// 1. Through full router: seller JWT with empty shop_id -> 403, even if order's shop_id is "".
	r := New(Dependencies{
		ServiceName: "b3-test",
		Clients:     c,
		AuthManager: authMgr,
		Logger:      log,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/seller/orders/ord-empty-shop", nil)
	req.Header.Set("Authorization", "Bearer "+sellerEmptyShopTok)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("router seller/orders with empty JWT shop_id: got %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if orderMock.getCalls != 0 {
		t.Fatalf("expected 0 GetOrder RPC calls when seller JWT shop_id is empty, got %d", orderMock.getCalls)
	}

	// 2. Direct handler invocation (bypassing RequireSellerShop middleware) with empty JWT shop_id
	//    against an order whose shop_id is ALSO empty -> MUST still reject 403 (fixes "" == "" bypass).
	h := &handler{clients: c, log: log}
	emptyClaims := &auth.Claims{Role: "seller", ShopID: ""}
	reqDirect := httptest.NewRequest(http.MethodGet, "/api/v1/seller/orders/ord-empty-shop", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "ord-empty-shop")
	ctxDirect := context.WithValue(auth.WithClaims(reqDirect.Context(), emptyClaims), chi.RouteCtxKey, rctx)
	recDirect := httptest.NewRecorder()
	h.handleSellerGetOrder(recDirect, reqDirect.WithContext(ctxDirect))
	if recDirect.Code != http.StatusForbidden {
		t.Fatalf("direct handleSellerGetOrder with empty shop_id: got %d, want 403 (%s)", recDirect.Code, recDirect.Body.String())
	}
	if orderMock.getCalls != 0 {
		t.Fatalf("direct handleSellerGetOrder must fail before RPC when shop_id is empty; calls=%d", orderMock.getCalls)
	}

	// 3. Valid seller accessing an order with empty shop_id -> 403.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/seller/orders/ord-empty-shop", nil)
	req.Header.Set("Authorization", "Bearer "+sellerValidTok)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("valid seller accessing order with empty shop_id: got %d, want 403", rec.Code)
	}

	// 4. Sibling seller handlers directly invoked with empty shop_id also reject 403.
	for name, fn := range map[string]http.HandlerFunc{
		"handleSellerListOrders":   h.handleSellerListOrders,
		"handleSellerConfirmOrder": h.handleSellerConfirmOrder,
		"handleSellerShipOrder":    h.handleSellerShipOrder,
		"handleSellerBalance":      h.handleSellerBalance,
	} {
		recSib := httptest.NewRecorder()
		reqSib := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(auth.WithClaims(context.Background(), emptyClaims))
		fn(recSib, reqSib)
		if recSib.Code != http.StatusForbidden {
			t.Fatalf("direct %s with empty shop_id: got %d, want 403", name, recSib.Code)
		}
	}
}

func TestSalesChannelGatewayContractAndValidation(t *testing.T) {
	authMgr, err := auth.NewManager("sc-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-1", "seller", "shop-a")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	orderMock := &salesChannelOrderMock{
		orders: map[string]*orderv1.Order{
			"ord-legacy": {Id: "ord-legacy", BuyerId: "buyer-1", ShopId: "shop-a", SalesChannel: "", StorefrontShopId: ""},
			"ord-sf":     {Id: "ord-sf", BuyerId: "buyer-1", ShopId: "shop-a", SalesChannel: "STOREFRONT", StorefrontShopId: "shop-a"},
		},
	}
	cartMock := &salesChannelCartMock{}
	paymentMock := &salesChannelPaymentMock{
		payments: map[string]*paymentv1.Payment{
			"pay-sf": {
				PaymentId:        "pay-sf",
				OrderGroupId:     "grp-sf",
				UserId:           "buyer-1",
				AmountVnd:        150000,
				Method:           paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:           paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				SalesChannel:     "STOREFRONT",
				StorefrontShopId: "shop-a",
				ShopAllocations: []*paymentv1.ShopAllocation{
					{OrderId: "ord-sf", ShopId: "shop-a", SubtotalVnd: 150000, SalesChannel: "STOREFRONT", StorefrontShopId: "shop-a"},
				},
			},
		},
	}
	promoMock := &salesChannelPromoMock{}

	h := New(Dependencies{
		ServiceName: "gateway-sc-test",
		Clients: &clients.Clients{
			Order:     orderMock,
			Cart:      cartMock,
			Payment:   paymentMock,
			Promotion: promoMock,
			Timeout:   5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-sc-test", logger.WithWriter(io.Discard)),
	})

	call := func(method, path, body, tok string, extraHeaders map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var rBody io.Reader
		if body != "" {
			rBody = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, path, rBody)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// A4 Checkout: omitted sales_channel defaults to MARKETPLACE.
	rec := call(http.MethodPost, "/api/v1/checkout", `{"address_id":"a1","payment_method":"COD"}`, buyerTok, map[string]string{"Idempotency-Key": "idem-1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("default checkout: got %d (%s)", rec.Code, rec.Body.String())
	}
	if orderMock.lastCheckout.GetSalesChannel() != "MARKETPLACE" || orderMock.lastCheckout.GetStorefrontShopId() != "" {
		t.Fatalf("default checkout forwarded wrong channel: %+v", orderMock.lastCheckout)
	}
	var chkResp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &chkResp)
	if chkResp["sales_channel"] != "MARKETPLACE" || chkResp["storefront_shop_id"] != "" {
		t.Fatalf("unexpected default checkout response: %+v", chkResp)
	}

	// A4 Checkout: valid STOREFRONT with storefront_shop_id.
	rec = call(http.MethodPost, "/api/v1/checkout", `{"address_id":"a1","payment_method":"VIETQR","sales_channel":"STOREFRONT","storefront_shop_id":"shop-a"}`, buyerTok, map[string]string{"Idempotency-Key": "idem-2"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("storefront checkout: got %d (%s)", rec.Code, rec.Body.String())
	}
	if orderMock.lastCheckout.GetSalesChannel() != "STOREFRONT" || orderMock.lastCheckout.GetStorefrontShopId() != "shop-a" {
		t.Fatalf("storefront checkout forwarded wrong channel: %+v", orderMock.lastCheckout)
	}
	chkResp = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &chkResp)
	ordersArr, _ := chkResp["orders"].([]any)
	firstOrder, _ := ordersArr[0].(map[string]any)
	if chkResp["sales_channel"] != "STOREFRONT" || chkResp["storefront_shop_id"] != "shop-a" ||
		firstOrder["sales_channel"] != "STOREFRONT" || firstOrder["storefront_shop_id"] != "shop-a" {
		t.Fatalf("storefront checkout DTO missing channel fields: %+v", chkResp)
	}

	// A4 Checkout validation failures -> 400 INVALID_ARGUMENT (RPC must not be called).
	prevCalls := orderMock.checkoutCalls
	for _, payload := range []string{
		`{"address_id":"a1","payment_method":"COD","sales_channel":"WHOLESALE"}`,
		`{"address_id":"a1","payment_method":"COD","sales_channel":"STOREFRONT"}`,
		`{"address_id":"a1","payment_method":"COD","sales_channel":"MARKETPLACE","storefront_shop_id":"shop-a"}`,
		`{"address_id":"a1","payment_method":"COD","storefront_shop_id":"shop-a"}`,
	} {
		rec = call(http.MethodPost, "/api/v1/checkout", payload, buyerTok, map[string]string{"Idempotency-Key": "idem-bad"})
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"INVALID_ARGUMENT"`) {
			t.Fatalf("expected 400 INVALID_ARGUMENT for %s, got %d %s", payload, rec.Code, rec.Body.String())
		}
	}
	if orderMock.checkoutCalls != prevCalls {
		t.Fatalf("invalid checkout payloads must not call OrderService.CreateCheckout")
	}

	// A3 Cart: GET /api/v1/cart?shop_id=... & DELETE /api/v1/cart?shop_id=...
	rec = call(http.MethodGet, "/api/v1/cart?shop_id=shop-a", "", buyerTok, nil)
	if rec.Code != http.StatusOK || cartMock.lastGet == nil || cartMock.lastGet.GetShopId() != "shop-a" {
		t.Fatalf("GET /api/v1/cart?shop_id=shop-a failed: code=%d req=%+v", rec.Code, cartMock.lastGet)
	}
	rec = call(http.MethodDelete, "/api/v1/cart", `{"shop_id":"shop-a"}`, buyerTok, nil)
	if rec.Code != http.StatusOK || cartMock.lastClear == nil || cartMock.lastClear.GetShopId() != "shop-a" {
		t.Fatalf("DELETE /api/v1/cart with shop_id failed: code=%d req=%+v", rec.Code, cartMock.lastClear)
	}

	// A5 Orders (buyer, seller, admin — get & list) return sales_channel & storefront_shop_id, legacy -> MARKETPLACE.
	for _, tc := range []struct {
		path string
		tok  string
	}{
		{"/api/v1/orders/ord-legacy", buyerTok},
		{"/api/v1/orders/ord-sf", buyerTok},
		{"/api/v1/seller/orders/ord-sf", sellerTok},
	} {
		rec = call(http.MethodGet, tc.path, "", tc.tok, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s failed: %d %s", tc.path, rec.Code, rec.Body.String())
		}
		var resp map[string]map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		wantCh := "STOREFRONT"
		if strings.HasSuffix(tc.path, "ord-legacy") {
			wantCh = "MARKETPLACE"
		}
		if resp["order"]["sales_channel"] != wantCh {
			t.Fatalf("GET %s sales_channel=%v, want %s", tc.path, resp["order"]["sales_channel"], wantCh)
		}
		if wantCh == "STOREFRONT" && resp["order"]["storefront_shop_id"] != "shop-a" {
			t.Fatalf("GET %s storefront_shop_id=%v, want shop-a", tc.path, resp["order"]["storefront_shop_id"])
		}
	}

	for _, tc := range []struct {
		path string
		tok  string
	}{
		{"/api/v1/orders?sales_channel=STOREFRONT", buyerTok},
		{"/api/v1/seller/orders?sales_channel=STOREFRONT", sellerTok},
		{"/api/v1/admin/orders?sales_channel=STOREFRONT", adminTok},
	} {
		rec = call(http.MethodGet, tc.path, "", tc.tok, nil)
		if rec.Code != http.StatusOK || orderMock.lastList.GetSalesChannel() != "STOREFRONT" {
			t.Fatalf("GET %s failed: code=%d lastList=%+v", tc.path, rec.Code, orderMock.lastList)
		}
	}
	const sfShopUUID = "11111111-1111-4111-8111-111111111111"
	rec = call(http.MethodGet, "/api/v1/orders?storefront_shop_id="+sfShopUUID+"&page=2&page_size=5", "", buyerTok, nil)
	if rec.Code != http.StatusOK || orderMock.lastList.GetStorefrontShopId() != sfShopUUID ||
		orderMock.lastList.GetPage() != 2 || orderMock.lastList.GetPageSize() != 5 {
		t.Fatalf("buyer storefront_shop_id filter forward failed: code=%d req=%+v", rec.Code, orderMock.lastList)
	}
	orderMock.lastList = nil
	if rec = call(http.MethodGet, "/api/v1/orders?storefront_shop_id=not-a-uuid", "", buyerTok, nil); rec.Code != http.StatusBadRequest || orderMock.lastList != nil {
		t.Fatalf("invalid buyer storefront_shop_id must return 400 without RPC call, got %d req=%+v", rec.Code, orderMock.lastList)
	}
	if rec = call(http.MethodGet, "/api/v1/admin/orders?sales_channel=BAD", "", adminTok, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid admin orders sales_channel filter, got %d", rec.Code)
	}

	// A5 Admin ledger summary & reconciliation channel filters.
	rec = call(http.MethodGet, "/api/v1/admin/ledger/summary?sales_channel=STOREFRONT", "", adminTok, nil)
	if rec.Code != http.StatusOK || paymentMock.lastLedger.GetSalesChannel() != "STOREFRONT" {
		t.Fatalf("ledger summary filter failed: code=%d req=%+v", rec.Code, paymentMock.lastLedger)
	}
	if !strings.Contains(rec.Body.String(), `"sales_channel":"STOREFRONT"`) {
		t.Fatalf("ledger summary response missing sales_channel: %s", rec.Body.String())
	}
	if rec = call(http.MethodGet, "/api/v1/admin/ledger/summary?sales_channel=INVALID", "", adminTok, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid ledger sales_channel, got %d", rec.Code)
	}

	rec = call(http.MethodPost, "/api/v1/admin/payments/reconcile", `{"date":"2026-11-20","sales_channel":"STOREFRONT"}`, adminTok, nil)
	if rec.Code != http.StatusOK || paymentMock.lastReconcile.GetSalesChannel() != "STOREFRONT" {
		t.Fatalf("reconcile body filter failed: code=%d req=%+v", rec.Code, paymentMock.lastReconcile)
	}
	if !strings.Contains(rec.Body.String(), `"storefront_shop_id":"shop-a"`) {
		t.Fatalf("reconcile response missing item storefront_shop_id: %s", rec.Body.String())
	}
	rec = call(http.MethodPost, "/api/v1/admin/payments/reconcile?sales_channel=MARKETPLACE", ``, adminTok, nil)
	if rec.Code != http.StatusOK || paymentMock.lastReconcile.GetSalesChannel() != "MARKETPLACE" {
		t.Fatalf("reconcile query filter failed: code=%d req=%+v", rec.Code, paymentMock.lastReconcile)
	}
	if rec = call(http.MethodPost, "/api/v1/admin/payments/reconcile", `{"sales_channel":"BOGUS"}`, adminTok, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid reconcile sales_channel, got %d", rec.Code)
	}

	// Payment retry preserves STOREFRONT sales_channel and storefront_shop_id.
	rec = call(http.MethodPost, "/api/v1/payments/pay-sf/retry", `{"method":"VIETQR"}`, buyerTok, nil)
	if rec.Code != http.StatusOK || paymentMock.lastCreate == nil ||
		paymentMock.lastCreate.GetSalesChannel() != "STOREFRONT" ||
		paymentMock.lastCreate.GetStorefrontShopId() != "shop-a" {
		t.Fatalf("retry payment must preserve sales_channel/storefront_shop_id: code=%d req=%+v", rec.Code, paymentMock.lastCreate)
	}

	// Promotion preview & vouchers forward and validate sales_channel.
	rec = call(http.MethodPost, "/api/v1/promotions/preview", `{"sales_channel":"STOREFRONT","storefront_shop_id":"shop-a","shop_items":[]}`, buyerTok, nil)
	if rec.Code != http.StatusOK || promoMock.lastPreview.GetSalesChannel() != "STOREFRONT" || promoMock.lastPreview.GetStorefrontShopId() != "shop-a" {
		t.Fatalf("promotions preview forward failed: code=%d req=%+v", rec.Code, promoMock.lastPreview)
	}
	if rec = call(http.MethodPost, "/api/v1/promotions/preview", `{"sales_channel":"STOREFRONT"}`, buyerTok, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on preview STOREFRONT without shop id, got %d", rec.Code)
	}
	rec = call(http.MethodGet, "/api/v1/vouchers?sales_channel=STOREFRONT&storefront_shop_id=shop-a", "", "", nil)
	if rec.Code != http.StatusOK || promoMock.lastList.GetSalesChannel() != "STOREFRONT" || promoMock.lastList.GetStorefrontShopId() != "shop-a" {
		t.Fatalf("vouchers forward failed: code=%d req=%+v", rec.Code, promoMock.lastList)
	}

	// Zero CORS headers invariant.
	rec = call(http.MethodGet, "/api/v1/vouchers", "", "", map[string]string{"Origin": "https://evil.example"})
	for hdr := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(hdr), "access-control-") {
			t.Fatalf("gateway must emit 0 CORS headers, found %s", hdr)
		}
	}
}
