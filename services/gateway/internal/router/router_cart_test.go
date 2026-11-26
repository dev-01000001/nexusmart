package router

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeCartClientForAddItem struct {
	cartv1.CartServiceClient
	addCalls int
	lastReq  *cartv1.AddItemRequest
	addErr   error
}

func (f *fakeCartClientForAddItem) AddItem(_ context.Context, in *cartv1.AddItemRequest, _ ...grpc.CallOption) (*cartv1.AddItemResponse, error) {
	f.addCalls++
	f.lastReq = in
	if f.addErr != nil {
		return nil, f.addErr
	}
	return &cartv1.AddItemResponse{
		Cart: &cartv1.Cart{
			UserId: in.GetUserId(),
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-a",
					Items: []*cartv1.CartItem{
						{
							SkuId:    in.GetSkuId(),
							ShopId:   "shop-a",
							Quantity: in.GetQuantity(),
							PriceVnd: 150000,
						},
					},
					SubtotalVnd: 150000 * int64(in.GetQuantity()),
				},
			},
			TotalItems: in.GetQuantity(),
			TotalVnd:   150000 * int64(in.GetQuantity()),
		},
		ClampedToStock: false,
	}, nil
}

type fakeProductBatchClientForCart struct {
	productv1.ProductServiceClient
	batchCalls int
	lastSKUIDs []string
	skus       map[string]*productv1.SKU
	batchErr   error
}

func (f *fakeProductBatchClientForCart) GetSKUsBatch(_ context.Context, in *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	f.batchCalls++
	f.lastSKUIDs = append([]string(nil), in.GetSkuIds()...)
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	out := make([]*productv1.SKU, 0, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		if sk, ok := f.skus[id]; ok {
			out = append(out, sk)
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

func TestGatewayAddCartItemStorefrontShopIsolation(t *testing.T) {
	authMgr, err := auth.NewManager("cart-storefront-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", auth.RoleBuyer, "")

	cartMock := &fakeCartClientForAddItem{}
	prodMock := &fakeProductBatchClientForCart{
		skus: map[string]*productv1.SKU{
			"sku-a1":         {Id: "sku-a1", ShopId: "shop-a", IsActive: true, PriceVnd: 150000},
			"sku-a-inactive": {Id: "sku-a-inactive", ShopId: "shop-a", IsActive: false},
			"sku-b1":         {Id: "sku-b1", ShopId: "shop-b", IsActive: true, PriceVnd: 200000},
			"sku-b-hidden":   {Id: "sku-b-hidden", ShopId: "shop-b", IsActive: false},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-cart-storefront-test",
		Clients: &clients.Clients{
			Cart:    cartMock,
			Product: prodMock,
			Timeout: 5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-cart-storefront-test", logger.WithWriter(io.Discard)),
	})

	doPost := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/cart/items", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. Foreign shop active SKU -> 404 NOT_FOUND, CartService.AddItem must NOT be called.
	cartMock.addCalls = 0
	prodMock.batchCalls = 0
	rec := doPost(`{"sku_id":"sku-b1","quantity":1,"storefront_shop_id":"shop-a"}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"NOT_FOUND"`) {
		t.Fatalf("foreign active SKU: want 404 NOT_FOUND, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cartMock.addCalls != 0 || prodMock.batchCalls != 1 {
		t.Fatalf("foreign active SKU: want Cart=0 Product=1, got Cart=%d Product=%d", cartMock.addCalls, prodMock.batchCalls)
	}

	// 2. Foreign shop inactive/hidden SKU -> identical 404 NOT_FOUND (no SKU_NOT_ACTIVE oracle).
	cartMock.addCalls = 0
	prodMock.batchCalls = 0
	rec = doPost(`{"sku_id":"sku-b-hidden","quantity":1,"storefront_shop_id":"shop-a"}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"NOT_FOUND"`) {
		t.Fatalf("foreign inactive SKU: want 404 NOT_FOUND, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cartMock.addCalls != 0 || prodMock.batchCalls != 1 {
		t.Fatalf("foreign inactive SKU: want Cart=0 Product=1, got Cart=%d Product=%d", cartMock.addCalls, prodMock.batchCalls)
	}

	// 3. Non-existent SKU with storefront_shop_id -> identical 404 NOT_FOUND.
	cartMock.addCalls = 0
	prodMock.batchCalls = 0
	rec = doPost(`{"sku_id":"sku-missing","quantity":1,"storefront_shop_id":"shop-a"}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"NOT_FOUND"`) {
		t.Fatalf("missing SKU: want 404 NOT_FOUND, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cartMock.addCalls != 0 || prodMock.batchCalls != 1 {
		t.Fatalf("missing SKU: want Cart=0 Product=1, got Cart=%d Product=%d", cartMock.addCalls, prodMock.batchCalls)
	}

	// 4. Matching shop active SKU -> 200 and calls CartService.AddItem.
	cartMock.addCalls = 0
	prodMock.batchCalls = 0
	rec = doPost(`{"sku_id":"sku-a1","quantity":2,"storefront_shop_id":"shop-a"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("matching shop SKU: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if prodMock.batchCalls != 1 || cartMock.addCalls != 1 || cartMock.lastReq.GetSkuId() != "sku-a1" || cartMock.lastReq.GetQuantity() != 2 {
		t.Fatalf("matching shop SKU: unexpected calls Product=%d Cart=%d req=%+v", prodMock.batchCalls, cartMock.addCalls, cartMock.lastReq)
	}

	// 5. Same-shop inactive SKU passes shop check and lets CartService return 409 SKU_NOT_ACTIVE.
	cartMock.addCalls = 0
	prodMock.batchCalls = 0
	cartMock.addErr = status.Error(codes.FailedPrecondition, "SKU_NOT_ACTIVE: sku sku-a-inactive is not active")
	rec = doPost(`{"sku_id":"sku-a-inactive","quantity":1,"storefront_shop_id":"shop-a"}`)
	cartMock.addErr = nil
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "SKU_NOT_ACTIVE") {
		t.Fatalf("same-shop inactive SKU: want 409 SKU_NOT_ACTIVE, got %d (%s)", rec.Code, rec.Body.String())
	}
	if prodMock.batchCalls != 1 || cartMock.addCalls != 1 {
		t.Fatalf("same-shop inactive SKU: want Product=1 Cart=1, got Product=%d Cart=%d", prodMock.batchCalls, cartMock.addCalls)
	}

	// 6. Transient ProductService error propagates (503 UNAVAILABLE, never 404) and blocks Cart.AddItem.
	cartMock.addCalls = 0
	prodMock.batchCalls = 0
	prodMock.batchErr = status.Error(codes.Unavailable, "product db down")
	rec = doPost(`{"sku_id":"sku-a1","quantity":1,"storefront_shop_id":"shop-a"}`)
	prodMock.batchErr = nil
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("transient product error: want 503, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cartMock.addCalls != 0 {
		t.Fatalf("transient product error: must not call CartService.AddItem, got %d", cartMock.addCalls)
	}

	// 7. Omitting storefront_shop_id skips GetSKUsBatch and calls CartService.AddItem directly.
	cartMock.addCalls = 0
	prodMock.batchCalls = 0
	rec = doPost(`{"sku_id":"sku-b1","quantity":1}`)
	if rec.Code != http.StatusOK || prodMock.batchCalls != 0 || cartMock.addCalls != 1 {
		t.Fatalf("no storefront_shop_id: want 200 Product=0 Cart=1, got code=%d Product=%d Cart=%d", rec.Code, prodMock.batchCalls, cartMock.addCalls)
	}
}
