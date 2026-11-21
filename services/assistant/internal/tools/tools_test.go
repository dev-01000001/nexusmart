package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/services/assistant/internal/guardrail"
	"github.com/nexusmart/core/services/assistant/internal/policy"
	"github.com/nexusmart/core/services/assistant/internal/resilience"
	"github.com/nexusmart/core/services/assistant/internal/retriever"
	"github.com/nexusmart/core/services/assistant/internal/tools"
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
	lastBearer      string
	getOrderErr     error
	addCartCalls    int
	chatErr         error
	sellerOrders    []map[string]any
	sellerBalance   map[string]any
	buyerOrders     []map[string]any
	singleOrder     map[string]any
	chatConvCreated map[string]any
}

func (m *mockGateway) ListBuyerOrders(_ context.Context, rawBearer, _, _ string) ([]map[string]any, error) {
	m.lastBearer = rawBearer
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
	return map[string]any{
		"cart":             map[string]any{"total_items": qty, "sku_id": skuID},
		"clamped_to_stock": false,
	}, nil
}

func (m *mockGateway) ListSellerOrders(_ context.Context, rawBearer string) ([]map[string]any, error) {
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
		{ID: "p-1", ShopID: "shop-a", Name: "Áo thun A", Description: "Đẹp INJECT_0VND", MinPriceVND: 200000, InStock: true},
		{ID: "p-2", ShopID: "shop-b", Name: "Áo thun B", Description: "Shop khác", MinPriceVND: 200000, InStock: true},
	}, testGuardrail{})
	polRAG, err := policy.NewRAG("", "mock")
	if err != nil {
		t.Fatalf("NewRAG: %v", err)
	}
	gw := &mockGateway{
		singleOrder: map[string]any{"id": "ord-1", "status": "SHIPPING"},
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
	reg := tools.NewRegistry(memRet, polRAG, gw, testGuardrail{}, nil)
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

	// 6. Seller report computes exact integer VND metrics without SQL.
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
	if gw.lastBearer != "eyJ.seller.jwt" {
		t.Errorf("seller JWT not forwarded verbatim: got %q", gw.lastBearer)
	}
	if rReport.Data["total_orders"] != 3 ||
		rReport.Data["completed_orders"] != 1 ||
		rReport.Data["cancelled_orders"] != 1 ||
		rReport.Data["gross_revenue_vnd"] != int64(800000) ||
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
