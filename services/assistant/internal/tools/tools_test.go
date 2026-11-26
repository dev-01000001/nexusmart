package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/policy"
	"github.com/nexusmart/core/services/assistant/internal/resilience"
	"github.com/nexusmart/core/services/assistant/internal/retriever"
	"github.com/nexusmart/core/services/assistant/internal/tools"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type testGuardrail struct{}

func (testGuardrail) CheckUserInput(text string) guardrail.CheckResult {
	return guardrail.CheckResult{Allowed: true, SanitizedText: text}
}

func (testGuardrail) SanitizeUntrustedData(_, text string) guardrail.CheckResult {
	cleaned := strings.ReplaceAll(text, "INJECT_0VND", "[lọc]")
	return guardrail.CheckResult{
		Allowed:        true,
		InjectionFound: cleaned != text,
		SanitizedText:  cleaned,
	}
}

func (testGuardrail) FilterOutput(text string) (string, bool) { return text, false }
func (testGuardrail) VerifyGrounding(string, string, []guardrail.GroundedProduct) (bool, []string) {
	return false, nil
}

type mockGateway struct {
	lastBearer            string
	lastOrderStatus       string
	getOrderErr           error
	addCartCalls          int
	addCartResp           map[string]any
	listSellerOrdersCalls int
	chatErr               error
	sellerOrders          []map[string]any
	sellerBalance         map[string]any
	buyerOrders           []map[string]any
	singleOrder           map[string]any
	chatConvCreated       map[string]any
}

type stubOrderReportClient struct {
	lastBearer string
	lastReq    *orderv1.GetSellerReportRequest
	calls      int
	resp       *orderv1.GetSellerReportResponse
	err        error
}

func (s *stubOrderReportClient) GetSellerReport(_ context.Context, rawBearer string, req *orderv1.GetSellerReportRequest) (*orderv1.GetSellerReportResponse, error) {
	s.calls++
	s.lastBearer = rawBearer
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func (m *mockGateway) ListBuyerOrders(_ context.Context, rawBearer, _, status string) ([]map[string]any, error) {
	m.lastBearer = rawBearer
	m.lastOrderStatus = status
	return m.buyerOrders, nil
}

func (m *mockGateway) GetOrder(_ context.Context, rawBearer, _ string) (map[string]any, error) {
	m.lastBearer = rawBearer
	if m.getOrderErr != nil {
		return nil, m.getOrderErr
	}
	return m.singleOrder, nil
}

func (m *mockGateway) AddCartItem(_ context.Context, rawBearer, skuID string, qty int32) (map[string]any, error) {
	m.lastBearer = rawBearer
	m.addCartCalls++
	if m.addCartResp != nil {
		return m.addCartResp, nil
	}
	return map[string]any{
		"cart":             map[string]any{"total_items": qty, "sku_id": skuID},
		"clamped_to_stock": false,
	}, nil
}

func (m *mockGateway) ListSellerOrders(_ context.Context, rawBearer string) ([]map[string]any, error) {
	m.listSellerOrdersCalls++
	m.lastBearer = rawBearer
	return m.sellerOrders, nil
}

func (m *mockGateway) GetSellerBalance(_ context.Context, rawBearer string) (map[string]any, error) {
	m.lastBearer = rawBearer
	return m.sellerBalance, nil
}

func (m *mockGateway) ListSellerProducts(_ context.Context, rawBearer string) ([]map[string]any, error) {
	m.lastBearer = rawBearer
	return nil, nil
}

func (m *mockGateway) CreateChatConversation(_ context.Context, rawBearer, shopID string) (map[string]any, error) {
	m.lastBearer = rawBearer
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	if m.chatConvCreated != nil {
		return m.chatConvCreated, nil
	}
	return map[string]any{"id": "conv-123", "shop_id": shopID}, nil
}

func TestAllowedToolsRBACAndNoDangerousTools(t *testing.T) {
	t.Parallel()

	reg := tools.NewRegistry(nil, nil, nil, nil, nil)

	hasTool := func(role, name string) bool {
		for _, td := range reg.AllowedTools(role) {
			if td.Name == name {
				return true
			}
		}
		return false
	}

	for _, nonSeller := range []string{"", "buyer", "admin"} {
		if hasTool(nonSeller, tools.ToolGetSellerReport) {
			t.Errorf("role %q must NOT be exposed get_seller_report", nonSeller)
		}
		if !hasTool(nonSeller, tools.ToolSearchProducts) || !hasTool(nonSeller, tools.ToolSearchPolicies) {
			t.Errorf("role %q missing public search tools", nonSeller)
		}
	}
	if !hasTool("seller", tools.ToolGetSellerReport) {
		t.Errorf("seller role MUST be exposed get_seller_report")
	}

	// Forbidden tools must NEVER be exposed to any role.
	for _, role := range []string{"", "buyer", "seller", "admin"} {
		for _, forbidden := range []string{"checkout", "pay", "payment", "refund", "execute_sql", "sql"} {
			if hasTool(role, forbidden) {
				t.Errorf("forbidden tool %q exposed for role %q", forbidden, role)
			}
		}
	}
}

func TestRegistryAuthConfirmationRBACAndJWTForwarding(t *testing.T) {
	t.Parallel()

	memRet := retriever.NewInMemoryRetriever([]retriever.ProductHit{
		{
			ID: "p-1", ShopID: "shop-a", Name: "Áo thun A", Description: "Đẹp INJECT_0VND", MinPriceVND: 200000, InStock: true,
			SKUs: []retriever.SKUHit{{ID: "sku-1", Name: "M", PriceVND: 200000, AvailableStock: 10}},
		},
		{
			ID: "p-2", ShopID: "shop-b", Name: "Áo thun B", Description: "Shop khác", MinPriceVND: 200000, InStock: true,
			SKUs: []retriever.SKUHit{{ID: "sku-b", Name: "L", PriceVND: 200000, AvailableStock: 5}},
		},
	}, testGuardrail{})
	polRAG, err := policy.NewRAG("", "mock")
	if err != nil {
		t.Fatalf("NewRAG: %v", err)
	}
	gw := &mockGateway{
		singleOrder: map[string]any{
			"id":                 "ord-1",
			"status":             "SHIPPING",
			"shop_id":            "shop-a",
			"sales_channel":      "STOREFRONT",
			"storefront_shop_id": "shop-a",
		},
		sellerOrders: []map[string]any{
			{
				"id": "o1", "status": "COMPLETED", "total_payable_vnd": float64(500000),
				"items": []any{
					map[string]any{"product_id": "p-1", "product_name": "Áo thun A", "quantity": float64(2), "line_total_vnd": float64(400000)},
				},
			},
			{
				"id": "o2", "status": "CONFIRMED", "total_payable_vnd": float64(300000),
				"items": []any{
					map[string]any{"product_id": "p-1", "product_name": "Áo thun A", "quantity": float64(1), "line_total_vnd": float64(300000)},
				},
			},
			{
				"id": "o3", "status": "CANCELLED", "total_payable_vnd": float64(200000),
			},
		},
		sellerBalance: map[string]any{
			"available_vnd": float64(450000),
			"pending_vnd":   float64(285000),
		},
	}
	orderRepStub := &stubOrderReportClient{
		resp: &orderv1.GetSellerReportResponse{
			ShopId:          "shop-a",
			FromDate:        "2026-10-28",
			ToDate:          "2026-11-26",
			GmvVnd:          925000,
			NetRevenueVnd:   810000,
			TotalOrders:     4,
			CompletedOrders: 2,
			CancelledOrders: 1,
			CancelRateBps:   2500,
			TopProducts: []*orderv1.TopProductStat{
				{ProductId: "p-1", ProductName: "Áo thun A", UnitsSold: 3, RevenueVnd: 700000, OrderCount: 2},
			},
		},
	}
	reg := tools.NewRegistry(memRet, polRAG, gw, testGuardrail{}, nil, tools.WithOrderReportClient(orderRepStub))
	ctx := context.Background()

	// 1. search_products ALWAYS overrides LLM shop_id with cc.ShopID.
	res := reg.Execute(ctx, tools.CallContext{ShopID: "shop-a"}, tools.ToolSearchProducts, map[string]any{
		"query":   "ao thun",
		"shop_id": "shop-b", // attacker tries to query shop-b!
	})
	if !res.OK {
		t.Fatalf("search_products failed: %+v", res)
	}
	prods, _ := res.Data["products"].([]retriever.ProductHit)
	if len(prods) != 1 || prods[0].ShopID != "shop-a" {
		t.Fatalf("expected only shop-a product, got %+v", prods)
	}
	if strings.Contains(prods[0].Description, "INJECT_0VND") {
		t.Errorf("product description not sanitized: %q", prods[0].Description)
	}

	// 2. Anonymous calls to private tools MUST return RequiresAuth: true.
	for _, privTool := range []string{tools.ToolGetOrderStatus, tools.ToolAddToCart, tools.ToolHandoffToAgent} {
		r := reg.Execute(ctx, tools.CallContext{}, privTool, map[string]any{"sku_id": "sku-1", "quantity": 1})
		if r.OK || !r.RequiresAuth || !r.Private {
			t.Errorf("anonymous %s: expected OK=false RequiresAuth=true Private=true, got %+v", privTool, r)
		}
	}

	// 3. Buyer blocked from get_seller_report.
	buyerCC := tools.CallContext{UserID: "u-buyer", UserRole: "buyer", RawBearer: "eyJ.buyer.jwt", ShopID: "shop-a"}
	rSellerDenied := reg.Execute(ctx, buyerCC, tools.ToolGetSellerReport, nil)
	if rSellerDenied.OK {
		t.Fatalf("buyer should be blocked from get_seller_report, got %+v", rSellerDenied)
	}

	// 4. get_order_status forwards buyer JWT verbatim and maps 403/404 safely without leaking.
	rOrder := reg.Execute(ctx, buyerCC, tools.ToolGetOrderStatus, map[string]any{"order_id": "ord-1"})
	if !rOrder.OK || !rOrder.Private {
		t.Fatalf("get_order_status failed: %+v", rOrder)
	}
	if gw.lastBearer != "eyJ.buyer.jwt" {
		t.Errorf("JWT was not forwarded verbatim: got %q", gw.lastBearer)
	}

	// 4b. get_order_status normalizes legacy phase-0b return statuses onto canonical main statuses.
	for _, tc := range []struct {
		in   string
		want string
	}{
		{in: "RETURN_APPROVED", want: "RETURN_REQUESTED"},
		{in: "RETURN_REJECTED", want: "RETURN_REQUESTED"},
		{in: "RETURNING", want: "RETURN_REQUESTED"},
		{in: "REFUNDED", want: "RETURNED"},
		{in: "RETURN_REQUESTED", want: "RETURN_REQUESTED"},
		{in: "RETURNED", want: "RETURNED"},
	} {
		rList := reg.Execute(ctx, buyerCC, tools.ToolGetOrderStatus, map[string]any{"status": tc.in})
		if !rList.OK || gw.lastOrderStatus != tc.want {
			t.Errorf("status %q: expected normalized %q, got ok=%v status=%q", tc.in, tc.want, rList.OK, gw.lastOrderStatus)
		}
	}

	gw.getOrderErr = &resilience.HTTPStatusError{StatusCode: http.StatusForbidden, Body: "secret internal detail"}
	rForbidden := reg.Execute(ctx, buyerCC, tools.ToolGetOrderStatus, map[string]any{"order_id": "ord-other"})
	if rForbidden.OK || strings.Contains(rForbidden.Error, "secret internal detail") {
		t.Errorf("403 error leaked upstream details or succeeded: %+v", rForbidden)
	}
	gw.getOrderErr = nil

	// 5. add_to_cart requires confirmation first, then calls gateway with exact JWT once confirmed.
	rNeedConfirm := reg.Execute(ctx, buyerCC, tools.ToolAddToCart, map[string]any{"sku_id": "sku-1", "quantity": 2})
	if !rNeedConfirm.OK || !rNeedConfirm.RequiresConfirmation || gw.addCartCalls != 0 {
		t.Fatalf("expected confirmation prompt without mutating cart, got %+v calls=%d", rNeedConfirm, gw.addCartCalls)
	}

	confirmedCC := buyerCC
	confirmedCC.Confirmed = true
	rAdded := reg.Execute(ctx, confirmedCC, tools.ToolAddToCart, map[string]any{"sku_id": "sku-1", "quantity": 2})
	if !rAdded.OK || rAdded.RequiresConfirmation || gw.addCartCalls != 1 {
		t.Fatalf("expected confirmed add_to_cart to execute once, got %+v calls=%d", rAdded, gw.addCartCalls)
	}

	// 6. Seller report delegates to OrderService.GetSellerReport (never self-computes from ListSellerOrders).
	sellerCC := tools.CallContext{
		UserID:       "u-seller",
		UserRole:     "seller",
		SellerShopID: "shop-a",
		RawBearer:    "eyJ.seller.jwt",
	}
	rReport := reg.Execute(ctx, sellerCC, tools.ToolGetSellerReport, map[string]any{"period": "30d"})
	if !rReport.OK || !rReport.Private {
		t.Fatalf("get_seller_report failed: %+v", rReport)
	}
	if orderRepStub.calls != 1 || orderRepStub.lastBearer != "eyJ.seller.jwt" || gw.lastBearer != "eyJ.seller.jwt" {
		t.Errorf("seller JWT not forwarded verbatim to OrderService.GetSellerReport and GetSellerBalance: repCalls=%d repBearer=%q gwBearer=%q",
			orderRepStub.calls, orderRepStub.lastBearer, gw.lastBearer)
	}
	if gw.listSellerOrdersCalls != 0 {
		t.Fatalf("get_seller_report must NOT call ListSellerOrders, got %d calls", gw.listSellerOrdersCalls)
	}
	if rReport.Data["total_orders"] != 4 ||
		rReport.Data["completed_orders"] != 2 ||
		rReport.Data["cancelled_orders"] != 1 ||
		rReport.Data["cancel_rate_pct"] != 25.0 ||
		rReport.Data["gross_revenue_vnd"] != int64(925000) ||
		rReport.Data["available_balance_vnd"] != int64(450000) ||
		rReport.Data["pending_balance_vnd"] != int64(285000) {
		t.Errorf("unexpected seller metrics: %+v", rReport.Data)
	}

	// 7. handoff_to_agent degrades gracefully when chat service is unavailable.
	gw.chatErr = &resilience.HTTPStatusError{StatusCode: http.StatusServiceUnavailable}
	rHandoff := reg.Execute(ctx, buyerCC, tools.ToolHandoffToAgent, map[string]any{"reason": "cần gặp nhân viên"})
	if !rHandoff.OK || rHandoff.Data["status"] != "queued_offline" {
		t.Errorf("expected graceful queued_offline degradation, got %+v", rHandoff)
	}
}

func TestHTTPGatewayClientSSRFJWTAndNoRetryOnPOST(t *testing.T) {
	t.Parallel()

	// SSRF validation.
	for _, badURL := range []string{"", "file:///etc/passwd", "gopher://127.0.0.1:6379", "http://", "http://user:pass@gateway:8080"} {
		if _, err := tools.NewHTTPGatewayClient(badURL, time.Second, nil); err == nil {
			t.Errorf("expected SSRF error for baseURL %q", badURL)
		}
	}

	var getAttempts atomic.Int32
	var postAttempts atomic.Int32
	var sawAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/v1/orders":
			if getAttempts.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"id": "ord-99", "status": "CONFIRMED"}},
			})
		case "/api/v1/cart/items":
			postAttempts.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, err := tools.NewHTTPGatewayClient(srv.URL, 2*time.Second, resilience.NewCircuitBreaker(5, time.Second))
	if err != nil {
		t.Fatalf("NewHTTPGatewayClient: %v", err)
	}

	// Missing JWT MUST fail locally without sending any HTTP request.
	if _, err := client.ListBuyerOrders(context.Background(), "", "", ""); !errors.Is(err, tools.ErrMissingBearerToken) {
		t.Fatalf("expected ErrMissingBearerToken, got %v", err)
	}

	// Idempotent GET retries on 503 and forwards exact Bearer JWT.
	orders, err := client.ListBuyerOrders(context.Background(), "user.jwt.token", "shop-1", "")
	if err != nil {
		t.Fatalf("ListBuyerOrders failed: %v", err)
	}
	if len(orders) != 1 || getAttempts.Load() != 2 {
		t.Fatalf("expected 1 order after 2 GET attempts, got orders=%d attempts=%d", len(orders), getAttempts.Load())
	}
	if sawAuth != "Bearer user.jwt.token" {
		t.Fatalf("expected exact Authorization header 'Bearer user.jwt.token', got %q", sawAuth)
	}

	// Non-idempotent POST MUST NOT retry on 503!
	_, err = client.AddCartItem(context.Background(), "user.jwt.token", "sku-1", 1)
	if err == nil {
		t.Fatalf("expected 503 error from AddCartItem")
	}
	if postAttempts.Load() != 1 {
		t.Fatalf("POST /cart/items MUST NOT retry: got %d attempts", postAttempts.Load())
	}
}

type fakeOrderReportServer struct {
	orderv1.UnimplementedOrderServiceServer
	mu         sync.Mutex
	calls      int
	lastClaims *auth.Claims
	lastRaw    string
	lastReq    *orderv1.GetSellerReportRequest
	resp       *orderv1.GetSellerReportResponse
	err        error
}

func (s *fakeOrderReportServer) GetSellerReport(ctx context.Context, req *orderv1.GetSellerReportRequest) (*orderv1.GetSellerReportResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastClaims, _ = auth.ClaimsFromContext(ctx)
	s.lastRaw = auth.RawTokenFromContext(ctx)
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func TestSellerReportUsesOrderServiceGetSellerReportAndMatchesFields(t *testing.T) {
	t.Parallel()

	userMgr, err := auth.NewManager("order-report-user-secret", auth.DefaultAccessTTL)
	if err != nil {
		t.Fatalf("user auth manager: %v", err)
	}
	svcMgr, err := auth.NewManager("order-report-svc-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("svc auth manager: %v", err)
	}
	sellerTok, _, _, err := userMgr.SignAccessToken("seller-u1", auth.RoleSeller, "shop-alpha")
	if err != nil {
		t.Fatalf("SignAccessToken: %v", err)
	}

	fakeOrderSrv := &fakeOrderReportServer{
		resp: &orderv1.GetSellerReportResponse{
			ShopId:          "shop-alpha",
			FromDate:        "2026-11-20",
			ToDate:          "2026-11-26",
			GmvVnd:          15450000,
			NetRevenueVnd:   13900000,
			CommissionVnd:   1100000,
			PaymentFeeVnd:   450000,
			TotalOrders:     18,
			CompletedOrders: 14,
			CancelledOrders: 3,
			ReturnedOrders:  1,
			CancelRateBps:   1666, // 16.66%
			ReturnRateBps:   555,
			TopProducts: []*orderv1.TopProductStat{
				{ProductId: "prod-1", ProductName: "Áo Linen", UnitsSold: 12, RevenueVnd: 8400000, OrderCount: 9},
				{ProductId: "prod-2", ProductName: "Quần Kaki", UnitsSold: 7, RevenueVnd: 5600000, OrderCount: 5},
			},
		},
	}

	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(auth.GRPCUnaryServerInterceptor(userMgr, svcMgr)),
	)
	orderv1.RegisterOrderServiceServer(grpcSrv, fakeOrderSrv)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	orderReportClient := tools.NewGRPCOrderReportClientWithClient(orderv1.NewOrderServiceClient(conn), 3*time.Second, resilience.NewCircuitBreaker(5, time.Second))

	// Put deliberately conflicting data into ListSellerOrders to prove it is never used.
	gw := &mockGateway{
		sellerOrders: []map[string]any{
			{"id": "wrong-1", "status": "COMPLETED", "total_payable_vnd": float64(1)},
		},
		sellerBalance: map[string]any{
			"available_vnd": float64(9800000),
			"pending_vnd":   float64(2300000),
		},
	}
	// Pin clock to 2026-11-26 10:00 Asia/Ho_Chi_Minh (03:00 UTC).
	pinnedNow := func() time.Time { return time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC) }

	reg := tools.NewRegistry(
		nil,
		nil,
		gw,
		testGuardrail{},
		nil,
		tools.WithOrderReportClient(orderReportClient),
		tools.WithNow(pinnedNow),
	)

	sellerCC := tools.CallContext{
		UserID:       "seller-u1",
		UserRole:     auth.RoleSeller,
		SellerShopID: "shop-alpha",
		RawBearer:    "Bearer " + sellerTok,
	}

	// Verify Asia/Ho_Chi_Minh date ranges for today, 7d, 30d, 90d, and unknown fallback to 7d.
	for _, tc := range []struct {
		period         string
		wantNormalized string
		wantFrom       string
		wantTo         string
	}{
		{"today", "today", "2026-11-26", "2026-11-26"},
		{"7d", "7d", "2026-11-20", "2026-11-26"},
		{"30d", "30d", "2026-10-28", "2026-11-26"},
		{"90d", "90d", "2026-08-29", "2026-11-26"},
		{"180d", "7d", "2026-11-20", "2026-11-26"},
	} {
		// Inject a decoy outgoing authorization header to prove GRPCOrderReportClient replaces it strictly with seller's JWT.
		ctxWithDecoy := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(auth.MDAuthorization, "Bearer decoy-token"))
		res := reg.Execute(ctxWithDecoy, sellerCC, tools.ToolGetSellerReport, map[string]any{"period": tc.period})
		if !res.OK || !res.Private {
			t.Fatalf("period=%s failed: %+v", tc.period, res)
		}
		if res.Data["period"] != tc.wantNormalized {
			t.Fatalf("period=%s: want normalized period %q, got %v", tc.period, tc.wantNormalized, res.Data["period"])
		}
		fakeOrderSrv.mu.Lock()
		gotClaims := fakeOrderSrv.lastClaims
		gotReq := fakeOrderSrv.lastReq
		gotRaw := fakeOrderSrv.lastRaw
		fakeOrderSrv.mu.Unlock()

		if gotClaims == nil || gotClaims.Service != "" || gotClaims.UserID() != "seller-u1" || gotClaims.ShopID != "shop-alpha" {
			t.Fatalf("period=%s: OrderService.GetSellerReport must receive seller end-user JWT (no service token), got claims=%+v", tc.period, gotClaims)
		}
		if gotRaw != "Bearer "+sellerTok {
			t.Fatalf("period=%s: expected seller JWT verbatim, got %q", tc.period, gotRaw)
		}
		if gotReq.GetShopId() != "shop-alpha" || gotReq.GetFromDate() != tc.wantFrom || gotReq.GetToDate() != tc.wantTo || gotReq.GetTopLimit() != 5 {
			t.Fatalf("period=%s: unexpected GetSellerReportRequest: %+v (want %s..%s limit 5)", tc.period, gotReq, tc.wantFrom, tc.wantTo)
		}

		// Exact field parity with OrderService.GetSellerReportResponse + GetSellerBalance.
		if res.Data["total_orders"] != 18 ||
			res.Data["completed_orders"] != 14 ||
			res.Data["cancelled_orders"] != 3 ||
			res.Data["cancel_rate_pct"] != 16.66 ||
			res.Data["gross_revenue_vnd"] != int64(15450000) ||
			res.Data["available_balance_vnd"] != int64(9800000) ||
			res.Data["pending_balance_vnd"] != int64(2300000) {
			t.Fatalf("period=%s: metrics mismatch: %+v", tc.period, res.Data)
		}
		topProds, ok := res.Data["top_products"].([]map[string]any)
		if !ok || len(topProds) != 2 ||
			topProds[0]["product_id"] != "prod-1" ||
			topProds[0]["product_name"] != "Áo Linen" ||
			topProds[0]["units_sold"] != int32(12) ||
			topProds[0]["revenue_vnd"] != int64(8400000) ||
			topProds[1]["product_id"] != "prod-2" ||
			topProds[1]["units_sold"] != int32(7) ||
			topProds[1]["revenue_vnd"] != int64(5600000) {
			t.Fatalf("period=%s: top_products mismatch: %+v", tc.period, res.Data["top_products"])
		}
	}

	if gw.listSellerOrdersCalls != 0 {
		t.Fatalf("ListSellerOrders must NEVER be called when using OrderService.GetSellerReport, got %d calls", gw.listSellerOrdersCalls)
	}

	// Verify gRPC auth/permission/transient error mapping without falling back to ListSellerOrders.
	fakeOrderSrv.mu.Lock()
	fakeOrderSrv.err = status.Error(codes.PermissionDenied, "wrong shop")
	fakeOrderSrv.mu.Unlock()
	deniedRes := reg.Execute(context.Background(), sellerCC, tools.ToolGetSellerReport, map[string]any{"period": "7d"})
	if deniedRes.OK || deniedRes.RequiresAuth {
		t.Fatalf("PermissionDenied: want OK=false RequiresAuth=false, got %+v", deniedRes)
	}

	fakeOrderSrv.mu.Lock()
	fakeOrderSrv.err = status.Error(codes.Unauthenticated, "expired")
	fakeOrderSrv.mu.Unlock()
	unauthRes := reg.Execute(context.Background(), sellerCC, tools.ToolGetSellerReport, map[string]any{"period": "7d"})
	if unauthRes.OK || !unauthRes.RequiresAuth {
		t.Fatalf("Unauthenticated: want OK=false RequiresAuth=true, got %+v", unauthRes)
	}

	if gw.listSellerOrdersCalls != 0 {
		t.Fatalf("ListSellerOrders must NEVER be called even on OrderService errors, got %d calls", gw.listSellerOrdersCalls)
	}
}

func TestAddToCartSecuritySchemaShopIsolationAndCartURL(t *testing.T) {
	t.Parallel()

	memRet := retriever.NewInMemoryRetriever([]retriever.ProductHit{
		{
			ID: "p-a", ShopID: "shop-a", Name: "Áo A", MinPriceVND: 200000, InStock: true,
			SKUs: []retriever.SKUHit{
				{ID: "sku-a-active", Name: "Đen / M", PriceVND: 200000, AvailableStock: 8},
				{ID: "sku-a-inactive", Name: "Đen / S", PriceVND: 200000, AvailableStock: 5, Inactive: true},
			},
		},
		{
			ID: "p-b", ShopID: "shop-b", Name: "Áo B", MinPriceVND: 250000, InStock: true,
			SKUs: []retriever.SKUHit{
				{ID: "sku-b-active", Name: "Trắng / L", PriceVND: 250000, AvailableStock: 4},
			},
		},
	}, testGuardrail{})
	gw := &mockGateway{}
	reg := tools.NewRegistry(memRet, nil, gw, testGuardrail{}, nil)
	ctx := context.Background()

	// 1. add_to_cart schema exposed to LLM MUST NOT contain "confirmed".
	for _, td := range reg.AllowedTools("buyer") {
		if td.Name != tools.ToolAddToCart {
			continue
		}
		props, _ := td.InputSchema["properties"].(map[string]any)
		if _, hasConfirmed := props["confirmed"]; hasConfirmed {
			t.Fatalf("ToolAddToCart InputSchema MUST NOT expose confirmed to LLM: %+v", props)
		}
	}

	// 2. LLM passing args["confirmed"]=true MUST NOT bypass cc.Confirmed=false.
	buyerCC := tools.CallContext{UserID: "u-buyer", UserRole: "buyer", RawBearer: "eyJ.buyer.jwt", ShopID: "shop-a", Confirmed: false}
	rBypass := reg.Execute(ctx, buyerCC, tools.ToolAddToCart, map[string]any{
		"sku_id":    "sku-a-active",
		"quantity":  1,
		"confirmed": true,
	})
	if !rBypass.OK || !rBypass.RequiresConfirmation || gw.addCartCalls != 0 {
		t.Fatalf("LLM args[confirmed]=true must require confirmation and never call AddCartItem: res=%+v calls=%d", rBypass, gw.addCartCalls)
	}

	// 3. Cross-shop SKU MUST be rejected even when cc.Confirmed=true.
	confirmedCC := buyerCC
	confirmedCC.Confirmed = true
	rCrossShop := reg.Execute(ctx, confirmedCC, tools.ToolAddToCart, map[string]any{
		"sku_id":   "sku-b-active",
		"quantity": 1,
	})
	if rCrossShop.OK || rCrossShop.RequiresConfirmation || gw.addCartCalls != 0 {
		t.Fatalf("cross-shop SKU must be rejected before AddCartItem: res=%+v calls=%d", rCrossShop, gw.addCartCalls)
	}

	// 4. Nonexistent or inactive (not on sale) SKU on scoped storefront MUST be rejected.
	rMissing := reg.Execute(ctx, confirmedCC, tools.ToolAddToCart, map[string]any{
		"sku_id":   "sku-does-not-exist",
		"quantity": 1,
	})
	if rMissing.OK || rMissing.RequiresConfirmation || gw.addCartCalls != 0 {
		t.Fatalf("unknown SKU on scoped storefront must be rejected: res=%+v calls=%d", rMissing, gw.addCartCalls)
	}
	rInactive := reg.Execute(ctx, confirmedCC, tools.ToolAddToCart, map[string]any{
		"sku_id":   "sku-a-inactive",
		"quantity": 1,
	})
	if rInactive.OK || rInactive.RequiresConfirmation || gw.addCartCalls != 0 {
		t.Fatalf("inactive own-shop SKU must be rejected before AddCartItem: res=%+v calls=%d", rInactive, gw.addCartCalls)
	}
	// Fail-closed when product retriever is missing on scoped storefront.
	regNoRet := tools.NewRegistry(nil, nil, gw, testGuardrail{}, nil)
	rNoRet := regNoRet.Execute(ctx, confirmedCC, tools.ToolAddToCart, map[string]any{
		"sku_id":   "sku-a-active",
		"quantity": 1,
	})
	if rNoRet.OK || gw.addCartCalls != 0 {
		t.Fatalf("missing SKU lookup on scoped storefront must fail closed: res=%+v calls=%d", rNoRet, gw.addCartCalls)
	}

	// 5. Valid own-shop active SKU with cc.Confirmed=true succeeds and returns cart_url "/gio-hang".
	rOK := reg.Execute(ctx, confirmedCC, tools.ToolAddToCart, map[string]any{
		"sku_id":   "sku-a-active",
		"quantity": 2,
	})
	if !rOK.OK || rOK.RequiresConfirmation || gw.addCartCalls != 1 {
		t.Fatalf("expected valid own-shop add_to_cart to succeed once: res=%+v calls=%d", rOK, gw.addCartCalls)
	}
	if rOK.Data["cart_url"] != "/gio-hang" {
		t.Fatalf("expected cart_url=/gio-hang, got %v", rOK.Data["cart_url"])
	}

	// 6. handoff_to_agent ignores LLM shop_id when cc.ShopID is set.
	rHandoff := reg.Execute(ctx, buyerCC, tools.ToolHandoffToAgent, map[string]any{
		"shop_id": "shop-b",
		"reason":  "cần hỗ trợ",
	})
	if !rHandoff.OK || rHandoff.Data["shop_id"] != "shop-a" {
		t.Fatalf("handoff_to_agent must scope to cc.ShopID=shop-a, got %+v", rHandoff)
	}
}

func TestGetOrderStatusStorefrontIsolation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	gw := &mockGateway{}
	reg := tools.NewRegistry(nil, nil, gw, testGuardrail{}, nil)
	buyerCC := tools.CallContext{UserID: "u-buyer", UserRole: "buyer", RawBearer: "eyJ.buyer.jwt", ShopID: "shop-a"}

	// Baseline 404 error from Gateway for exact indistinguishable error comparison.
	gw.getOrderErr = &resilience.HTTPStatusError{StatusCode: http.StatusNotFound}
	notFoundRes := reg.Execute(ctx, buyerCC, tools.ToolGetOrderStatus, map[string]any{"order_id": "ord-missing"})
	gw.getOrderErr = nil

	cases := []struct {
		name   string
		order  map[string]any
		wantOK bool
	}{
		{
			name: "matching storefront order succeeds",
			order: map[string]any{
				"id": "ord-ok", "status": "SHIPPING",
				"shop_id": "shop-a", "sales_channel": "STOREFRONT", "storefront_shop_id": "shop-a",
			},
			wantOK: true,
		},
		{
			name: "other shop storefront order is hidden as not found",
			order: map[string]any{
				"id": "ord-other-shop", "status": "SHIPPING",
				"shop_id": "shop-b", "sales_channel": "STOREFRONT", "storefront_shop_id": "shop-b",
			},
			wantOK: false,
		},
		{
			name: "mismatched shop_id even with matching storefront_shop_id is hidden",
			order: map[string]any{
				"id": "ord-mismatch-shop", "status": "SHIPPING",
				"shop_id": "shop-b", "sales_channel": "STOREFRONT", "storefront_shop_id": "shop-a",
			},
			wantOK: false,
		},
		{
			name: "marketplace order on same shop is hidden as not found",
			order: map[string]any{
				"id": "ord-mkt", "status": "SHIPPING",
				"shop_id": "shop-a", "sales_channel": "MARKETPLACE", "storefront_shop_id": "",
			},
			wantOK: false,
		},
		{
			name: "missing sales_channel is hidden (same law as assertInStorefront)",
			order: map[string]any{
				"id": "ord-no-ch", "status": "SHIPPING",
				"shop_id": "shop-a", "storefront_shop_id": "shop-a",
			},
			wantOK: false,
		},
		{
			name: "missing storefront_shop_id is hidden (same law as assertInStorefront)",
			order: map[string]any{
				"id": "ord-no-sf", "status": "SHIPPING",
				"shop_id": "shop-a", "sales_channel": "STOREFRONT",
			},
			wantOK: false,
		},
		{
			name: "mismatched storefront_shop_id is hidden",
			order: map[string]any{
				"id": "ord-other-sf", "status": "SHIPPING",
				"shop_id": "shop-a", "sales_channel": "STOREFRONT", "storefront_shop_id": "shop-b",
			},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		gw.singleOrder = tc.order
		res := reg.Execute(ctx, buyerCC, tools.ToolGetOrderStatus, map[string]any{"order_id": tc.order["id"]})
		if res.OK != tc.wantOK {
			t.Errorf("%s: want OK=%v, got %+v", tc.name, tc.wantOK, res)
			continue
		}
		if !tc.wantOK {
			if !res.Private || res.Data != nil || res.Error != notFoundRes.Error {
				t.Errorf("%s: must match not-found error %q with nil data, got %+v", tc.name, notFoundRes.Error, res)
			}
		}
	}

	// Unscoped call (empty cc.ShopID) returns order normally.
	gw.singleOrder = map[string]any{
		"id": "ord-mkt", "status": "SHIPPING",
		"shop_id": "shop-b", "sales_channel": "MARKETPLACE", "storefront_shop_id": "",
	}
	unscopedCC := tools.CallContext{UserID: "u-buyer", UserRole: "buyer", RawBearer: "eyJ.buyer.jwt"}
	if res := reg.Execute(ctx, unscopedCC, tools.ToolGetOrderStatus, map[string]any{"order_id": "ord-mkt"}); !res.OK {
		t.Fatalf("unscoped get_order_status should succeed, got %+v", res)
	}
}

func TestAddToCartScopesCartToStorefrontShop(t *testing.T) {
	t.Parallel()

	memRet := retriever.NewInMemoryRetriever([]retriever.ProductHit{
		{
			ID: "p-a", ShopID: "shop-a", Name: "Áo A1", MinPriceVND: 200000, InStock: true,
			SKUs: []retriever.SKUHit{{ID: "sku-a-1", Name: "Đen / M", PriceVND: 200000, AvailableStock: 8}},
		},
	}, testGuardrail{})

	// Decode realistic gateway cartDTO JSON so nested slices/numbers match HTTPGatewayClient ([]any, float64).
	const rawRespJSON = `{
		"cart": {
			"user_id": "u-buyer",
			"shop_groups": [
				{
					"shop_id": "shop-a",
					"shop_name": "Shop Áo A",
					"subtotal_vnd": 400000,
					"total_weight_grams": 600,
					"items": [
						{
							"sku_id": "sku-a-1",
							"product_id": "p-a",
							"shop_id": "shop-a",
							"product_name": "Áo A1",
							"sku_name": "Đen / M",
							"price_vnd": 200000,
							"quantity": 2,
							"weight_grams": 300,
							"unavailable": false
						},
						{
							"sku_id": "sku-a-unavail",
							"product_id": "p-a2",
							"shop_id": "shop-a",
							"product_name": "Áo A hết hàng",
							"sku_name": "S",
							"price_vnd": 50000,
							"quantity": 1,
							"weight_grams": 150,
							"unavailable": true
						}
					]
				},
				{
					"shop_id": "shop-b",
					"shop_name": "Shop Đối Thủ B",
					"subtotal_vnd": 900000,
					"total_weight_grams": 1500,
					"items": [
						{
							"sku_id": "sku-b-secret",
							"product_id": "p-b",
							"shop_id": "shop-b",
							"product_name": "Bí mật Shop B",
							"sku_name": "L",
							"price_vnd": 300000,
							"quantity": 3,
							"weight_grams": 500,
							"unavailable": false
						}
					]
				}
			],
			"total_items": 5,
			"total_vnd": 1300000
		},
		"clamped_to_stock": false
	}`
	var decodedResp map[string]any
	if err := json.Unmarshal([]byte(rawRespJSON), &decodedResp); err != nil {
		t.Fatalf("unmarshal test cart fixture: %v", err)
	}

	gw := &mockGateway{addCartResp: decodedResp}
	reg := tools.NewRegistry(memRet, nil, gw, testGuardrail{}, nil)
	cc := tools.CallContext{
		UserID:    "u-buyer",
		UserRole:  "buyer",
		RawBearer: "eyJ.buyer.jwt",
		ShopID:    "shop-a",
		Confirmed: true,
	}

	res := reg.Execute(context.Background(), cc, tools.ToolAddToCart, map[string]any{
		"sku_id":   "sku-a-1",
		"quantity": 2,
	})
	if !res.OK {
		t.Fatalf("add_to_cart failed: %+v", res)
	}

	// Serialized Result (sent to both SSE tool_result and LLM tool context) must NEVER contain foreign shop data.
	serialized, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	payload := string(serialized)
	for _, forbidden := range []string{"shop-b", "Shop Đối Thủ B", "sku-b-secret", "Bí mật Shop B", "1300000", "900000"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("foreign shop cart data %q leaked into add_to_cart result: %s", forbidden, payload)
		}
	}

	cart, ok := res.Data["cart"].(map[string]any)
	if !ok {
		t.Fatalf("expected cart map in Data, got %T: %+v", res.Data["cart"], res.Data["cart"])
	}
	if cart["user_id"] != "u-buyer" {
		t.Fatalf("expected user_id=u-buyer, got %v", cart["user_id"])
	}
	if cart["total_items"] != int32(2) {
		t.Fatalf("expected recomputed total_items=2 (available shop-a items only), got %#v", cart["total_items"])
	}
	if cart["total_vnd"] != int64(400000) {
		t.Fatalf("expected recomputed total_vnd=400000 (shop-a only), got %#v", cart["total_vnd"])
	}
	groups, ok := cart["shop_groups"].([]map[string]any)
	if !ok || len(groups) != 1 {
		t.Fatalf("expected exactly 1 shop_group for shop-a, got %#v", cart["shop_groups"])
	}
	if groups[0]["shop_id"] != "shop-a" || groups[0]["subtotal_vnd"] != int64(400000) || groups[0]["total_weight_grams"] != int32(600) {
		t.Fatalf("unexpected scoped shop_group: %+v", groups[0])
	}
}
