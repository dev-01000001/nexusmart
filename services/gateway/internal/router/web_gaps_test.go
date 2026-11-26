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

type fakeGapUserClient struct {
	userv1.UserServiceClient
	shops           map[string]*userv1.Shop
	lastRegisterReq *userv1.RegisterShopRequest
	lastGetShopReq  *userv1.GetShopRequest
}

func (f *fakeGapUserClient) RegisterShop(_ context.Context, in *userv1.RegisterShopRequest, _ ...grpc.CallOption) (*userv1.RegisterShopResponse, error) {
	f.lastRegisterReq = in
	return &userv1.RegisterShopResponse{
		Shop: &userv1.Shop{
			Id:                 "shop-new",
			OwnerUserId:        in.GetUserId(),
			Name:               in.GetName(),
			Slug:               in.GetSlug(),
			Description:        in.GetDescription(),
			ContactPhone:       in.GetContactPhone(),
			BusinessLicenseDoc: in.GetBusinessLicenseDoc(),
			Status:             userv1.ShopStatus_SHOP_STATUS_PENDING,
		},
	}, nil
}

func (f *fakeGapUserClient) GetShop(_ context.Context, in *userv1.GetShopRequest, _ ...grpc.CallOption) (*userv1.GetShopResponse, error) {
	f.lastGetShopReq = in
	for _, s := range f.shops {
		if in.GetShopId() != "" && s.GetId() == in.GetShopId() {
			return &userv1.GetShopResponse{Shop: s}, nil
		}
		if in.GetSlug() != "" && strings.EqualFold(s.GetSlug(), in.GetSlug()) {
			return &userv1.GetShopResponse{Shop: s}, nil
		}
		if in.GetOwnerUserId() != "" && s.GetOwnerUserId() == in.GetOwnerUserId() {
			return &userv1.GetShopResponse{Shop: s}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "shop not found")
}

func (f *fakeGapUserClient) ListPublicShops(_ context.Context, _ *userv1.ListPublicShopsRequest, _ ...grpc.CallOption) (*userv1.ListPublicShopsResponse, error) {
	var out []*userv1.Shop
	for _, s := range f.shops {
		if s.GetStatus() == userv1.ShopStatus_SHOP_STATUS_ACTIVE {
			out = append(out, s)
		}
	}
	return &userv1.ListPublicShopsResponse{Shops: out, Total: int64(len(out))}, nil
}

type fakeGapProductClient struct {
	productv1.ProductServiceClient
	categories    []*productv1.Category
	products      map[string]*productv1.Product
	skus          map[string]*productv1.SKU
	lastListReq   *productv1.ListProductsRequest
	lastGetReq    *productv1.GetProductRequest
	lastCreateReq *productv1.CreateProductRequest
	lastUpdateReq *productv1.UpdateProductRequest
	lastSKUsReq   *productv1.GetSKUsBatchRequest
}

func (f *fakeGapProductClient) ListCategories(_ context.Context, _ *productv1.ListCategoriesRequest, _ ...grpc.CallOption) (*productv1.ListCategoriesResponse, error) {
	return &productv1.ListCategoriesResponse{Categories: f.categories}, nil
}

func (f *fakeGapProductClient) ListProducts(_ context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	f.lastListReq = in
	var out []*productv1.Product
	for _, p := range f.products {
		if in.GetShopId() != "" && p.GetShopId() != in.GetShopId() {
			continue
		}
		if in.GetCategoryId() != "" && p.GetCategoryId() != in.GetCategoryId() {
			continue
		}
		if in.GetStatus() != productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED && p.GetStatus() != in.GetStatus() {
			continue
		}
		out = append(out, p)
	}
	return &productv1.ListProductsResponse{Products: out, Total: int64(len(out))}, nil
}

func (f *fakeGapProductClient) GetProduct(_ context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	f.lastGetReq = in
	for _, p := range f.products {
		if in.GetId() != "" && p.GetId() == in.GetId() {
			return &productv1.GetProductResponse{Product: p}, nil
		}
		if in.GetSlug() != "" && strings.EqualFold(p.GetSlug(), in.GetSlug()) {
			return &productv1.GetProductResponse{Product: p}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "product not found")
}

func (f *fakeGapProductClient) CreateProduct(_ context.Context, in *productv1.CreateProductRequest, _ ...grpc.CallOption) (*productv1.CreateProductResponse, error) {
	f.lastCreateReq = in
	return &productv1.CreateProductResponse{
		Product: &productv1.Product{
			Id:         "prod-created",
			ShopId:     in.GetShopId(),
			CategoryId: in.GetCategoryId(),
			Name:       in.GetName(),
			Slug:       in.GetSlug(),
			Status:     productv1.ProductStatus_PRODUCT_STATUS_PENDING,
		},
	}, nil
}

func (f *fakeGapProductClient) UpdateProduct(_ context.Context, in *productv1.UpdateProductRequest, _ ...grpc.CallOption) (*productv1.UpdateProductResponse, error) {
	f.lastUpdateReq = in
	return &productv1.UpdateProductResponse{
		Product: &productv1.Product{
			Id:         in.GetId(),
			ShopId:     in.GetShopId(),
			CategoryId: in.GetCategoryId(),
			Name:       in.GetName(),
			Slug:       in.GetSlug(),
			Status:     in.GetStatus(),
		},
	}, nil
}

func (f *fakeGapProductClient) GetSKUsBatch(_ context.Context, in *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	f.lastSKUsReq = in
	out := make([]*productv1.SKU, 0, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		if sk, ok := f.skus[id]; ok {
			out = append(out, sk)
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

type fakeGapOrderClient struct {
	orderv1.OrderServiceClient
	orders map[string]*orderv1.Order
}

func (f *fakeGapOrderClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	ord, ok := f.orders[in.GetOrderId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return &orderv1.GetOrderResponse{Order: ord}, nil
}

type fakeGapPromotionClient struct {
	promotionv1.PromotionServiceClient
	vouchers                 []*promotionv1.Voucher
	lastListReq              *promotionv1.ListAvailableVouchersRequest
	lastRegisterFlashItemReq *promotionv1.RegisterFlashSaleItemRequest
	registerFlashItemCalls   int
}

func (f *fakeGapPromotionClient) RegisterFlashSaleItem(_ context.Context, in *promotionv1.RegisterFlashSaleItemRequest, _ ...grpc.CallOption) (*promotionv1.RegisterFlashSaleItemResponse, error) {
	f.registerFlashItemCalls++
	f.lastRegisterFlashItemReq = in
	return &promotionv1.RegisterFlashSaleItemResponse{
		Item: &promotionv1.FlashSaleItem{
			Id:             "fsi-1",
			FlashSaleId:    in.GetFlashSaleId(),
			ShopId:         in.GetShopId(),
			SkuId:          in.GetSkuId(),
			FlashPriceVnd:  in.GetFlashPriceVnd(),
			TotalSlots:     in.GetTotalSlots(),
			RemainingSlots: in.GetTotalSlots(),
			PerUserLimit:   in.GetPerUserLimit(),
			Status:         promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING,
		},
	}, nil
}

func (f *fakeGapPromotionClient) ListAvailableVouchers(_ context.Context, in *promotionv1.ListAvailableVouchersRequest, _ ...grpc.CallOption) (*promotionv1.ListAvailableVouchersResponse, error) {
	f.lastListReq = in
	var out []*promotionv1.Voucher
	for _, v := range f.vouchers {
		if in.GetShopId() != "" && v.GetShopId() != in.GetShopId() {
			continue
		}
		if in.GetScope() != promotionv1.VoucherScope_VOUCHER_SCOPE_UNSPECIFIED && v.GetScope() != in.GetScope() {
			continue
		}
		out = append(out, v)
	}
	return &promotionv1.ListAvailableVouchersResponse{Vouchers: out}, nil
}

func assertNoPrivateShopFields(t *testing.T, raw map[string]any) {
	t.Helper()
	for _, secretKey := range []string{"owner_user_id", "pickup_address_id", "contact_phone", "business_license_doc", "rejection_reason"} {
		if _, exists := raw[secretKey]; exists {
			t.Fatalf("public shop response leaked private field %q: %+v", secretKey, raw)
		}
	}
}

func TestWebGapEndpointsRBACIsolationAndPrivacy(t *testing.T) {
	authMgr, err := auth.NewManager("web-gap-test-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	sellerBTok, _, _, _ := authMgr.SignAccessToken("seller-b", "seller", "shop-b")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	userMock := &fakeGapUserClient{
		shops: map[string]*userv1.Shop{
			"shop-a": {
				Id:                 "shop-a",
				OwnerUserId:        "seller-a",
				Name:               "Xưởng Dệt Bạch Đằng",
				Slug:               "xuong-det-bach-dang",
				Description:        "Cotton 220gsm",
				PickupAddressId:    "addr-pickup-a",
				ContactPhone:       "0901111111",
				BusinessLicenseDoc: "GPKD-A-SECRET.pdf",
				Status:             userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			},
			"shop-pending": {
				Id:                 "shop-pending",
				OwnerUserId:        "buyer-1",
				Name:               "Xưởng Chờ Duyệt",
				Slug:               "xuong-cho-duyet",
				Description:        "Chờ duyệt",
				PickupAddressId:    "addr-pickup-p",
				ContactPhone:       "0902222222",
				BusinessLicenseDoc: "GPKD-P-SECRET.pdf",
				Status:             userv1.ShopStatus_SHOP_STATUS_PENDING,
				RejectionReason:    "internal note",
			},
		},
	}

	productMock := &fakeGapProductClient{
		categories: []*productv1.Category{
			{Id: "cat-1", Name: "Áo thun", Slug: "ao-thun"},
		},
		products: map[string]*productv1.Product{
			"prod-a-approved": {
				Id:         "prod-a-approved",
				ShopId:     "shop-a",
				CategoryId: "cat-1",
				Name:       "Áo thun Compact",
				Slug:       "ao-thun-compact",
				Status:     productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			},
			"prod-a-pending": {
				Id:         "prod-a-pending",
				ShopId:     "shop-a",
				CategoryId: "cat-1",
				Name:       "Áo thun Mới Chờ Duyệt",
				Slug:       "ao-thun-moi-cho-duyet",
				Status:     productv1.ProductStatus_PRODUCT_STATUS_PENDING,
			},
			"prod-b-approved": {
				Id:         "prod-b-approved",
				ShopId:     "shop-b",
				CategoryId: "cat-1",
				Name:       "Áo Polo Shop B",
				Slug:       "ao-polo-shop-b",
				Status:     productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			},
		},
	}

	orderMock := &fakeGapOrderClient{
		orders: map[string]*orderv1.Order{
			"ord-a": {Id: "ord-a", OrderCode: "NM-A", BuyerId: "buyer-1", ShopId: "shop-a", Status: orderv1.OrderStatus_ORDER_STATUS_CONFIRMED},
			"ord-b": {Id: "ord-b", OrderCode: "NM-B", BuyerId: "buyer-1", ShopId: "shop-b", Status: orderv1.OrderStatus_ORDER_STATUS_CONFIRMED},
		},
	}

	promoMock := &fakeGapPromotionClient{
		vouchers: []*promotionv1.Voucher{
			{Id: "v-a", Code: "SHOPA10", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-a"},
			{Id: "v-b", Code: "SHOPB10", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-b"},
			{Id: "v-plat", Code: "NEXUS50", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM, ShopId: ""},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-web-gap-test",
		Clients: &clients.Clients{
			User:      userMock,
			Product:   productMock,
			Order:     orderMock,
			Promotion: promoMock,
			Timeout:   5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-web-gap-test", logger.WithWriter(io.Discard)),
	})

	doReq := func(method, path, token, body string) *httptest.ResponseRecorder {
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
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// --- 1. Public shop endpoints: ACTIVE only & zero private field leaks ---
	rec := doReq(http.MethodGet, "/api/v1/shops", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /shops: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var shopsList struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &shopsList); err != nil {
		t.Fatalf("decode /shops: %v", err)
	}
	if len(shopsList.Items) != 1 || shopsList.Total != 1 || shopsList.Items[0]["slug"] != "xuong-det-bach-dang" {
		t.Fatalf("unexpected /shops response: %+v", shopsList)
	}
	assertNoPrivateShopFields(t, shopsList.Items[0])

	// GET /shops/{id} active -> 200 without private fields; pending -> 404.
	rec = doReq(http.MethodGet, "/api/v1/shops/shop-a", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /shops/shop-a: want 200, got %d", rec.Code)
	}
	var shopWrap struct {
		Shop map[string]any `json:"shop"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &shopWrap)
	if shopWrap.Shop["slug"] != "xuong-det-bach-dang" {
		t.Fatalf("expected slug on /shops/shop-a, got %+v", shopWrap.Shop)
	}
	assertNoPrivateShopFields(t, shopWrap.Shop)

	if rec = doReq(http.MethodGet, "/api/v1/shops/shop-pending", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /shops/shop-pending: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}

	// GET /shops/slug/{slug} active -> 200 without private fields; pending -> 404.
	rec = doReq(http.MethodGet, "/api/v1/shops/slug/xuong-det-bach-dang", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /shops/slug/xuong-det-bach-dang: want 200, got %d", rec.Code)
	}
	shopWrap.Shop = nil
	_ = json.Unmarshal(rec.Body.Bytes(), &shopWrap)
	if shopWrap.Shop["id"] != "shop-a" {
		t.Fatalf("expected shop-a, got %+v", shopWrap.Shop)
	}
	assertNoPrivateShopFields(t, shopWrap.Shop)

	if rec = doReq(http.MethodGet, "/api/v1/shops/slug/xuong-cho-duyet", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /shops/slug/xuong-cho-duyet: want 404, got %d", rec.Code)
	}

	// --- 2. Authenticated GET /shops/me ---
	if rec = doReq(http.MethodGet, "/api/v1/shops/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /shops/me without token: want 401, got %d", rec.Code)
	}
	rec = doReq(http.MethodGet, "/api/v1/shops/me", buyerTok, "")
	if rec.Code != http.StatusOK || userMock.lastGetShopReq.GetOwnerUserId() != "buyer-1" {
		t.Fatalf("GET /shops/me buyer: code=%d req=%+v", rec.Code, userMock.lastGetShopReq)
	}
	if !strings.Contains(rec.Body.String(), `"status":"PENDING"`) || !strings.Contains(rec.Body.String(), `"contact_phone":"0902222222"`) {
		t.Fatalf("GET /shops/me should return owner's own full shop details, got %s", rec.Body.String())
	}

	// POST /shops/register forwards optional slug.
	rec = doReq(http.MethodPost, "/api/v1/shops/register", buyerTok, `{"name":"Shop Moi","slug":"shop-moi","contact_phone":"0903333333"}`)
	if rec.Code != http.StatusCreated || userMock.lastRegisterReq.GetSlug() != "shop-moi" {
		t.Fatalf("POST /shops/register slug: code=%d req=%+v", rec.Code, userMock.lastRegisterReq)
	}

	// --- 3. Public GET /products/slug/{slug} and query filters (category_slug, shop_slug, sort_by) ---
	rec = doReq(http.MethodGet, "/api/v1/products/slug/ao-thun-compact", "", "")
	if rec.Code != http.StatusOK || productMock.lastGetReq.GetSlug() != "ao-thun-compact" {
		t.Fatalf("GET /products/slug/ao-thun-compact: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"slug":"ao-thun-compact"`) {
		t.Fatalf("expected slug in product response: %s", rec.Body.String())
	}
	if rec = doReq(http.MethodGet, "/api/v1/products/slug/missing-slug", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /products/slug/missing-slug: want 404, got %d", rec.Code)
	}

	rec = doReq(http.MethodGet, "/api/v1/products?category_slug=ao-thun&shop_slug=xuong-det-bach-dang&sort_by=price-asc", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /products with slug filters: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if productMock.lastListReq.GetCategoryId() != "cat-1" || productMock.lastListReq.GetShopId() != "shop-a" || productMock.lastListReq.GetSortBy() != "price_asc" {
		t.Fatalf("unexpected resolved ListProductsRequest: %+v", productMock.lastListReq)
	}

	// Unknown category_slug or pending shop_slug returns empty 200 without leaking pending shop.
	rec = doReq(http.MethodGet, "/api/v1/products?category_slug=khong-co", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("GET /products unknown category_slug: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(http.MethodGet, "/api/v1/products?shop_slug=xuong-cho-duyet", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("GET /products pending shop_slug: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = doReq(http.MethodGet, "/api/v1/products?shop_slug=khong-ton-tai", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("GET /products unknown/not-found shop_slug: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// --- 4. Seller endpoints: 401 no token, 403 wrong role, strict shop_id isolation (seller A != shop B) ---
	sellerRoutes := []string{
		"/api/v1/seller/shop",
		"/api/v1/seller/products",
		"/api/v1/seller/products/prod-a-approved",
		"/api/v1/seller/orders/ord-a",
		"/api/v1/seller/vouchers",
	}
	for _, p := range sellerRoutes {
		if rec = doReq(http.MethodGet, p, "", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s without token: want 401, got %d", p, rec.Code)
		}
		if rec = doReq(http.MethodGet, p, buyerTok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s as buyer: want 403, got %d", p, rec.Code)
		}
		if rec = doReq(http.MethodGet, p, adminTok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s as admin: want 403, got %d", p, rec.Code)
		}
	}

	// GET /seller/shop reads verified shop_id from JWT.
	rec = doReq(http.MethodGet, "/api/v1/seller/shop", sellerATok, "")
	if rec.Code != http.StatusOK || userMock.lastGetShopReq.GetShopId() != "shop-a" {
		t.Fatalf("GET /seller/shop: code=%d req=%+v", rec.Code, userMock.lastGetShopReq)
	}

	// GET /seller/products ignores client ?shop_id=shop-b and forces shop-a from JWT.
	rec = doReq(http.MethodGet, "/api/v1/seller/products?shop_id=shop-b", sellerATok, "")
	if rec.Code != http.StatusOK || productMock.lastListReq.GetShopId() != "shop-a" {
		t.Fatalf("GET /seller/products must ignore client shop_id: code=%d req=%+v", rec.Code, productMock.lastListReq)
	}
	if strings.Contains(rec.Body.String(), "prod-b-approved") || !strings.Contains(rec.Body.String(), "prod-a-pending") {
		t.Fatalf("GET /seller/products leaked shop B or missed pending product: %s", rec.Body.String())
	}

	// GET /seller/products/{id}: seller A can read own product, blocked (403) from shop B's product!
	if rec = doReq(http.MethodGet, "/api/v1/seller/products/prod-a-pending", sellerATok, ""); rec.Code != http.StatusOK {
		t.Fatalf("seller A reading own pending product: want 200, got %d", rec.Code)
	}
	if rec = doReq(http.MethodGet, "/api/v1/seller/products/prod-b-approved", sellerATok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("seller A reading shop B product: want 403, got %d (%s)", rec.Code, rec.Body.String())
	}

	// GET /seller/orders/{id}: seller A can read own order, blocked (403) from shop B's order!
	if rec = doReq(http.MethodGet, "/api/v1/seller/orders/ord-a", sellerATok, ""); rec.Code != http.StatusOK {
		t.Fatalf("seller A reading own order: want 200, got %d", rec.Code)
	}
	if rec = doReq(http.MethodGet, "/api/v1/seller/orders/ord-b", sellerATok, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("seller A reading shop B order: want 403, got %d (%s)", rec.Code, rec.Body.String())
	}

	// GET /seller/vouchers: strictly scoped to seller A's shop_id & SHOP scope.
	rec = doReq(http.MethodGet, "/api/v1/seller/vouchers?shop_id=shop-b", sellerATok, "")
	if rec.Code != http.StatusOK || promoMock.lastListReq.GetShopId() != "shop-a" || promoMock.lastListReq.GetScope() != promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP {
		t.Fatalf("GET /seller/vouchers: code=%d req=%+v", rec.Code, promoMock.lastListReq)
	}
	if strings.Contains(rec.Body.String(), "SHOPB10") || strings.Contains(rec.Body.String(), "NEXUS50") || !strings.Contains(rec.Body.String(), "SHOPA10") {
		t.Fatalf("GET /seller/vouchers returned wrong vouchers: %s", rec.Body.String())
	}
	// Seller B sees only SHOPB10, not SHOPA10.
	rec = doReq(http.MethodGet, "/api/v1/seller/vouchers", sellerBTok, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "SHOPA10") || !strings.Contains(rec.Body.String(), "SHOPB10") {
		t.Fatalf("seller B /seller/vouchers mismatch: %s", rec.Body.String())
	}

	// POST & PUT /seller/products forward optional slug.
	rec = doReq(http.MethodPost, "/api/v1/seller/products", sellerATok, `{"category_id":"cat-1","name":"Ao Moi","slug":"ao-moi"}`)
	if rec.Code != http.StatusCreated || productMock.lastCreateReq.GetSlug() != "ao-moi" {
		t.Fatalf("POST /seller/products slug: code=%d req=%+v", rec.Code, productMock.lastCreateReq)
	}
	rec = doReq(http.MethodPut, "/api/v1/seller/products/prod-a-approved", sellerATok, `{"name":"Ao Sua","slug":"ao-sua"}`)
	if rec.Code != http.StatusOK || productMock.lastUpdateReq.GetSlug() != "ao-sua" {
		t.Fatalf("PUT /seller/products/{id} slug: code=%d req=%+v", rec.Code, productMock.lastUpdateReq)
	}

	// --- 5. Admin product endpoints: 401 no token, 403 buyer/seller, 200 admin ---
	for _, p := range []string{"/api/v1/admin/products?status=PENDING", "/api/v1/admin/products/prod-a-pending"} {
		if rec = doReq(http.MethodGet, p, "", ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s without token: want 401, got %d", p, rec.Code)
		}
		if rec = doReq(http.MethodGet, p, buyerTok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s as buyer: want 403, got %d", p, rec.Code)
		}
		if rec = doReq(http.MethodGet, p, sellerATok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("GET %s as seller: want 403, got %d", p, rec.Code)
		}
		if rec = doReq(http.MethodGet, p, adminTok, ""); rec.Code != http.StatusOK {
			t.Fatalf("GET %s as admin: want 200, got %d (%s)", p, rec.Code, rec.Body.String())
		}
	}
}

func TestSellerRegisterFlashSaleItemSKUOwnership(t *testing.T) {
	authMgr, err := auth.NewManager("flash-sale-sku-ownership-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")

	productMock := &fakeGapProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a-1": {Id: "sku-a-1", ProductId: "prod-a", ShopId: "shop-a", PriceVnd: 200000, IsActive: true},
			"sku-b-1": {Id: "sku-b-1", ProductId: "prod-b", ShopId: "shop-b", PriceVnd: 250000, IsActive: true},
		},
	}
	promoMock := &fakeGapPromotionClient{}

	h := New(Dependencies{
		ServiceName: "gateway-flash-sale-test",
		Clients: &clients.Clients{
			Product:   productMock,
			Promotion: promoMock,
			Timeout:   5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-flash-sale-test", logger.WithWriter(io.Discard)),
	})

	doPost := func(token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/seller/flash-sales/fs-1/items", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Auth / RBAC sanity checks.
	if rec := doPost("", `{"sku_id":"sku-a-1","flash_price_vnd":150000,"total_slots":10,"per_user_limit":1}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
	if rec := doPost(buyerTok, `{"sku_id":"sku-a-1","flash_price_vnd":150000,"total_slots":10,"per_user_limit":1}`); rec.Code != http.StatusForbidden {
		t.Fatalf("buyer role: want 403, got %d", rec.Code)
	}
	if rec := doPost(sellerATok, `{"sku_id":"  ","flash_price_vnd":150000,"total_slots":10,"per_user_limit":1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank sku_id: want 400, got %d", rec.Code)
	}

	// Case 1: SKU does not exist -> 404 NOT_FOUND, promotion RPC NOT called.
	rec := doPost(sellerATok, `{"sku_id":"sku-does-not-exist","flash_price_vnd":150000,"total_slots":10,"per_user_limit":1}`)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"NOT_FOUND"`) {
		t.Fatalf("missing SKU: want 404 NOT_FOUND, got %d (%s)", rec.Code, rec.Body.String())
	}
	if promoMock.registerFlashItemCalls != 0 {
		t.Fatalf("missing SKU must not call Promotion.RegisterFlashSaleItem, got %d calls", promoMock.registerFlashItemCalls)
	}

	// Case 2: SKU belongs to another shop (shop-b) -> 403 PERMISSION_DENIED, promotion RPC NOT called.
	rec = doPost(sellerATok, `{"sku_id":"sku-b-1","flash_price_vnd":100000,"total_slots":10,"per_user_limit":1}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"PERMISSION_DENIED"`) {
		t.Fatalf("cross-shop SKU: want 403 PERMISSION_DENIED, got %d (%s)", rec.Code, rec.Body.String())
	}
	if promoMock.registerFlashItemCalls != 0 {
		t.Fatalf("cross-shop SKU must not call Promotion.RegisterFlashSaleItem, got %d calls", promoMock.registerFlashItemCalls)
	}

	// Case 3: SKU belongs to caller's shop (shop-a) -> 201 Created, forwarded with JWT shop_id.
	rec = doPost(sellerATok, `{"sku_id":"sku-a-1","flash_price_vnd":150000,"total_slots":10,"per_user_limit":1}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("own SKU: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if promoMock.registerFlashItemCalls != 1 {
		t.Fatalf("expected exactly 1 RegisterFlashSaleItem call, got %d", promoMock.registerFlashItemCalls)
	}
	if promoMock.lastRegisterFlashItemReq.GetShopId() != "shop-a" || promoMock.lastRegisterFlashItemReq.GetSkuId() != "sku-a-1" || promoMock.lastRegisterFlashItemReq.GetFlashSaleId() != "fs-1" {
		t.Fatalf("unexpected RegisterFlashSaleItemRequest: %+v", promoMock.lastRegisterFlashItemReq)
	}
}
