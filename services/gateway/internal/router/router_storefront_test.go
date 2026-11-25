package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeUserClient struct {
	userv1.UserServiceClient
	shops       map[string]*userv1.Shop
	lastGetShop *userv1.GetShopRequest
}

func (f *fakeUserClient) GetShop(_ context.Context, in *userv1.GetShopRequest, _ ...grpc.CallOption) (*userv1.GetShopResponse, error) {
	f.lastGetShop = in
	s, ok := f.shops[in.GetShopId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "shop not found")
	}
	return &userv1.GetShopResponse{Shop: s}, nil
}

func (f *fakeUserClient) ListPublicShops(_ context.Context, _ *userv1.ListPublicShopsRequest, _ ...grpc.CallOption) (*userv1.ListPublicShopsResponse, error) {
	out := []*userv1.Shop{}
	for _, s := range f.shops {
		if s.GetStatus() == userv1.ShopStatus_SHOP_STATUS_ACTIVE {
			out = append(out, s)
		}
	}
	return &userv1.ListPublicShopsResponse{Shops: out, Total: int64(len(out))}, nil
}

type fakeListProductClient struct {
	productv1.ProductServiceClient
	lastList *productv1.ListProductsRequest
}

func (f *fakeListProductClient) ListProducts(_ context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	f.lastList = in
	return &productv1.ListProductsResponse{Products: []*productv1.Product{{Id: "p1", ShopId: in.GetShopId()}}, Total: 1}, nil
}

type fakeSellerOrderClient struct {
	orderv1.OrderServiceClient
	orders map[string]*orderv1.Order
}

func (f *fakeSellerOrderClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	o, ok := f.orders[in.GetOrderId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return &orderv1.GetOrderResponse{Order: o}, nil
}

type fakeSellerPromotionClient struct {
	promotionv1.PromotionServiceClient
	lastList *promotionv1.ListAvailableVouchersRequest
}

func (f *fakeSellerPromotionClient) ListAvailableVouchers(_ context.Context, in *promotionv1.ListAvailableVouchersRequest, _ ...grpc.CallOption) (*promotionv1.ListAvailableVouchersResponse, error) {
	f.lastList = in
	return &promotionv1.ListAvailableVouchersResponse{
		Vouchers: []*promotionv1.Voucher{{Id: "v1", Code: "SHOP10", ShopId: in.GetShopId(), Scope: in.GetScope()}},
	}, nil
}

func TestStorefrontShopVisibilityAndSellerScopes(t *testing.T) {
	authMgr, err := auth.NewManager("router-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("owner-1", "seller", "shop-active")
	pendingOwnerTok, _, _, _ := authMgr.SignAccessToken("owner-2", "buyer", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	users := &fakeUserClient{shops: map[string]*userv1.Shop{
		"shop-active": {
			Id: "shop-active", OwnerUserId: "owner-1", Name: "Bach Dang", ContactPhone: "0900000001",
			BusinessLicenseDoc: "doc", PickupAddressId: "addr-1", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE,
		},
		"shop-pending": {
			Id: "shop-pending", OwnerUserId: "owner-2", Name: "Pending", ContactPhone: "0900000002",
			Status: userv1.ShopStatus_SHOP_STATUS_PENDING,
		},
	}}
	products := &fakeListProductClient{}
	orders := &fakeSellerOrderClient{orders: map[string]*orderv1.Order{
		"ord-own":   {Id: "ord-own", BuyerId: "buyer-1", ShopId: "shop-active"},
		"ord-other": {Id: "ord-other", BuyerId: "owner-1", ShopId: "shop-pending"},
	}}
	promos := &fakeSellerPromotionClient{}
	h := New(Dependencies{
		ServiceName: "gateway-router-test",
		Clients:     &clients.Clients{User: users, Product: products, Order: orders, Promotion: promos, Timeout: 5 * time.Second},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-router-test", logger.WithWriter(io.Discard)),
	})

	do := func(method, path, tok string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}
	shopOf := func(body map[string]any) map[string]any {
		s, _ := body["shop"].(map[string]any)
		return s
	}

	// Anonymous / other buyer: ACTIVE shop, public fields only.
	for _, tok := range []string{"", buyerTok} {
		code, body := do(http.MethodGet, "/api/v1/shops/shop-active", tok)
		if code != http.StatusOK {
			t.Fatalf("expected 200 for active shop, got %d", code)
		}
		s := shopOf(body)
		for _, k := range []string{"contact_phone", "business_license_doc", "owner_user_id", "pickup_address_id"} {
			if _, ok := s[k]; ok {
				t.Fatalf("public shop view leaked %q: %+v", k, s)
			}
		}
		if s["name"] != "Bach Dang" {
			t.Fatalf("unexpected public shop: %+v", s)
		}
	}

	// Non-ACTIVE shop hidden from the public, visible to its owner and admins.
	if code, _ := do(http.MethodGet, "/api/v1/shops/shop-pending", ""); code != http.StatusNotFound {
		t.Fatalf("expected 404 for pending shop anonymously, got %d", code)
	}
	if code, _ := do(http.MethodGet, "/api/v1/shops/shop-pending", buyerTok); code != http.StatusNotFound {
		t.Fatalf("expected 404 for pending shop to other buyer, got %d", code)
	}
	for _, tok := range []string{pendingOwnerTok, adminTok} {
		code, body := do(http.MethodGet, "/api/v1/shops/shop-pending", tok)
		if code != http.StatusOK || shopOf(body)["contact_phone"] != "0900000002" {
			t.Fatalf("expected full shop for owner/admin, got %d %+v", code, body)
		}
	}

	// Public list: ACTIVE shops only, public fields only.
	code, body := do(http.MethodGet, "/api/v1/shops", "")
	items, _ := body["items"].([]any)
	if code != http.StatusOK || len(items) != 1 {
		t.Fatalf("expected 1 active shop in list, got %d %+v", code, body)
	}
	if first, _ := items[0].(map[string]any); first["contact_phone"] != nil || first["id"] != "shop-active" {
		t.Fatalf("unexpected public list item: %+v", first)
	}

	// Seller: own shop + products scoped by JWT shop_id, ignoring any shop_id in the query.
	code, body = do(http.MethodGet, "/api/v1/seller/shop", sellerTok)
	if code != http.StatusOK || users.lastGetShop.GetShopId() != "shop-active" || shopOf(body)["contact_phone"] != "0900000001" {
		t.Fatalf("seller/shop: code=%d req=%+v body=%+v", code, users.lastGetShop, body)
	}
	code, _ = do(http.MethodGet, "/api/v1/seller/products?shop_id=shop-pending&status=PENDING", sellerTok)
	if code != http.StatusOK || products.lastList.GetShopId() != "shop-active" ||
		products.lastList.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING {
		t.Fatalf("seller/products: code=%d req=%+v", code, products.lastList)
	}
	if code, _ := do(http.MethodGet, "/api/v1/seller/products", buyerTok); code != http.StatusForbidden {
		t.Fatalf("expected 403 for buyer on seller/products, got %d", code)
	}
	if code, _ := do(http.MethodGet, "/api/v1/seller/shop", ""); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token on seller/shop, got %d", code)
	}

	// Seller order detail: allows own shop's order, blocks another shop's order even if seller is buyer.
	if code, body := do(http.MethodGet, "/api/v1/seller/orders/ord-own", sellerTok); code != http.StatusOK || body["order"] == nil {
		t.Fatalf("expected 200 for seller own order, got %d %+v", code, body)
	}
	if code, _ := do(http.MethodGet, "/api/v1/seller/orders/ord-other", sellerTok); code != http.StatusForbidden {
		t.Fatalf("expected 403 for seller accessing another shop's order, got %d", code)
	}
	if code, _ := do(http.MethodGet, "/api/v1/seller/orders/ord-own", buyerTok); code != http.StatusForbidden {
		t.Fatalf("expected 403 for buyer on seller/orders/{id}, got %d", code)
	}

	// Seller vouchers: scoped to JWT shop_id & SHOP scope, ignoring query shop_id.
	if code, _ := do(http.MethodGet, "/api/v1/seller/vouchers?shop_id=shop-pending", sellerTok); code != http.StatusOK ||
		promos.lastList.GetShopId() != "shop-active" ||
		promos.lastList.GetScope() != promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP {
		t.Fatalf("seller/vouchers: code=%d req=%+v", code, promos.lastList)
	}
	if code, _ := do(http.MethodGet, "/api/v1/seller/vouchers", buyerTok); code != http.StatusForbidden {
		t.Fatalf("expected 403 for buyer on seller/vouchers, got %d", code)
	}

	// Admin products listing: RBAC strictly enforced at gateway.
	if code, _ := do(http.MethodGet, "/api/v1/admin/products?status=PENDING", ""); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token on admin/products, got %d", code)
	}
	if code, _ := do(http.MethodGet, "/api/v1/admin/products?status=PENDING", buyerTok); code != http.StatusForbidden {
		t.Fatalf("expected 403 for buyer on admin/products, got %d", code)
	}
	if code, _ := do(http.MethodGet, "/api/v1/admin/products?status=PENDING", sellerTok); code != http.StatusForbidden {
		t.Fatalf("expected 403 for seller on admin/products, got %d", code)
	}
	if code, _ := do(http.MethodGet, "/api/v1/admin/products?status=PENDING&shop_id=shop-pending", adminTok); code != http.StatusOK ||
		products.lastList.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_PENDING ||
		products.lastList.GetShopId() != "shop-pending" {
		t.Fatalf("admin/products: code=%d req=%+v", code, products.lastList)
	}
}
