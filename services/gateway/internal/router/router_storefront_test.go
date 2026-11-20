package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
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

func (f *fakeUserClient) ListActiveShops(_ context.Context, _ *userv1.ListActiveShopsRequest, _ ...grpc.CallOption) (*userv1.ListActiveShopsResponse, error) {
	out := []*userv1.Shop{}
	for _, s := range f.shops {
		if s.GetStatus() == userv1.ShopStatus_SHOP_STATUS_ACTIVE {
			out = append(out, s)
		}
	}
	return &userv1.ListActiveShopsResponse{Shops: out, Total: int64(len(out))}, nil
}

type fakeListProductClient struct {
	productv1.ProductServiceClient
	lastList *productv1.ListProductsRequest
}

func (f *fakeListProductClient) ListProducts(_ context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	f.lastList = in
	return &productv1.ListProductsResponse{Products: []*productv1.Product{{Id: "p1", ShopId: in.GetShopId()}}, Total: 1}, nil
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
	h := New(Dependencies{
		ServiceName: "gateway-router-test",
		Clients:     &clients.Clients{User: users, Product: products, Timeout: 5 * time.Second},
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
}
