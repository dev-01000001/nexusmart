package router

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type w3aProductClient struct {
	productv1.ProductServiceClient
	mu              sync.Mutex
	categories      []*productv1.Category
	products        map[string]*productv1.Product
	skus            map[string]*productv1.SKU
	lastListCatReq  *productv1.ListCategoriesRequest
	lastListCatMD   metadata.MD
	lastListProdReq *productv1.ListProductsRequest
	listProdCalls   int
	lastGetSKUsReq  *productv1.GetSKUsBatchRequest
}

func (p *w3aProductClient) ListCategories(ctx context.Context, in *productv1.ListCategoriesRequest, _ ...grpc.CallOption) (*productv1.ListCategoriesResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastListCatReq = in
	p.lastListCatMD, _ = metadata.FromOutgoingContext(ctx)
	var out []*productv1.Category
	for _, c := range p.categories {
		if in.GetParentId() != "" && c.GetParentId() != in.GetParentId() {
			continue
		}
		out = append(out, c)
	}
	return &productv1.ListCategoriesResponse{Categories: out}, nil
}

func (p *w3aProductClient) CreateCategory(_ context.Context, in *productv1.CreateCategoryRequest, _ ...grpc.CallOption) (*productv1.CreateCategoryResponse, error) {
	return &productv1.CreateCategoryResponse{
		Category: &productv1.Category{
			Id:                "cat-created",
			Name:              in.GetName(),
			Slug:              in.GetSlug(),
			ParentId:          in.GetParentId(),
			CommissionRateBps: in.GetCommissionRateBps(),
			SortOrder:         in.GetSortOrder(),
		},
	}, nil
}

func (p *w3aProductClient) UpdateCategory(_ context.Context, in *productv1.UpdateCategoryRequest, _ ...grpc.CallOption) (*productv1.UpdateCategoryResponse, error) {
	return &productv1.UpdateCategoryResponse{
		Category: &productv1.Category{
			Id:                in.GetId(),
			Name:              in.GetName(),
			Slug:              in.GetSlug(),
			ParentId:          in.GetParentId(),
			CommissionRateBps: in.GetCommissionRateBps(),
			SortOrder:         in.GetSortOrder(),
		},
	}, nil
}

func (p *w3aProductClient) ListProducts(_ context.Context, in *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listProdCalls++
	p.lastListProdReq = in
	skuFilter := make(map[string]struct{}, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		skuFilter[id] = struct{}{}
	}
	var out []*productv1.Product
	for _, prod := range p.products {
		if len(skuFilter) > 0 {
			matched := false
			for _, sk := range prod.GetSkus() {
				if _, ok := skuFilter[sk.GetId()]; ok {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if in.GetShopId() != "" && prod.GetShopId() != in.GetShopId() {
			continue
		}
		out = append(out, prod)
	}
	return &productv1.ListProductsResponse{Products: out, Total: int64(len(out))}, nil
}

func (p *w3aProductClient) GetProduct(_ context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, prod := range p.products {
		if (in.GetId() != "" && prod.GetId() == in.GetId()) ||
			(in.GetSlug() != "" && prod.GetSlug() == in.GetSlug()) {
			return &productv1.GetProductResponse{Product: prod}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "product not found")
}

func (p *w3aProductClient) CreateProduct(_ context.Context, in *productv1.CreateProductRequest, _ ...grpc.CallOption) (*productv1.CreateProductResponse, error) {
	return &productv1.CreateProductResponse{
		Product: &productv1.Product{
			Id:         "prod-new",
			ShopId:     in.GetShopId(),
			CategoryId: in.GetCategoryId(),
			Name:       in.GetName(),
			Status:     productv1.ProductStatus_PRODUCT_STATUS_PENDING,
			Skus: []*productv1.SKU{
				{Id: "sku-new", ShopId: in.GetShopId(), PriceVnd: 150000, CommissionRateBps: 550, IsActive: true},
			},
		},
	}, nil
}

func (p *w3aProductClient) UpdateProduct(_ context.Context, in *productv1.UpdateProductRequest, _ ...grpc.CallOption) (*productv1.UpdateProductResponse, error) {
	return &productv1.UpdateProductResponse{
		Product: &productv1.Product{
			Id:     in.GetId(),
			ShopId: in.GetShopId(),
			Name:   in.GetName(),
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			Skus: []*productv1.SKU{
				{Id: "sku-a-active", ShopId: in.GetShopId(), PriceVnd: 200000, CommissionRateBps: 550, IsActive: true},
			},
		},
	}, nil
}

func (p *w3aProductClient) UpdateStock(_ context.Context, in *productv1.UpdateStockRequest, _ ...grpc.CallOption) (*productv1.UpdateStockResponse, error) {
	return &productv1.UpdateStockResponse{
		Sku: &productv1.SKU{
			Id:                in.GetSkuId(),
			ShopId:            in.GetShopId(),
			Stock:             in.GetNewStock(),
			CommissionRateBps: 550,
			IsActive:          true,
		},
	}, nil
}

func (p *w3aProductClient) AdminReviewProduct(_ context.Context, in *productv1.AdminReviewProductRequest, _ ...grpc.CallOption) (*productv1.AdminReviewProductResponse, error) {
	return &productv1.AdminReviewProductResponse{
		Product: &productv1.Product{
			Id:     in.GetProductId(),
			ShopId: "shop-a",
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			Skus: []*productv1.SKU{
				{Id: "sku-a-active", ShopId: "shop-a", PriceVnd: 200000, CommissionRateBps: 550, IsActive: true},
			},
		},
	}, nil
}

func (p *w3aProductClient) GetSKUsBatch(_ context.Context, in *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastGetSKUsReq = in
	out := make([]*productv1.SKU, 0, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		if sk, ok := p.skus[id]; ok {
			out = append(out, sk)
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

type w3aUserClient struct {
	userv1.UserServiceClient
	mu                sync.Mutex
	lastReviewShopReq *userv1.AdminReviewShopRequest
	reviewShopErr     error
	lastReviewBankReq *userv1.AdminReviewShopBankAccountRequest
	reviewBankCalls   int
	reviewBankErr     error
	addBankCalls      int
	verifyOTPCalls    int
}

func (u *w3aUserClient) ListPublicShops(_ context.Context, _ *userv1.ListPublicShopsRequest, _ ...grpc.CallOption) (*userv1.ListPublicShopsResponse, error) {
	return &userv1.ListPublicShopsResponse{
		Shops: []*userv1.Shop{{Id: "shop-a", Name: "Shop A", Slug: "shop-a", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE}},
		Total: 1,
	}, nil
}

func (u *w3aUserClient) GetShop(_ context.Context, _ *userv1.GetShopRequest, _ ...grpc.CallOption) (*userv1.GetShopResponse, error) {
	return &userv1.GetShopResponse{
		Shop: &userv1.Shop{Id: "shop-a", Name: "Shop A", Slug: "shop-a", Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE},
	}, nil
}

func (u *w3aUserClient) AdminReviewShop(_ context.Context, in *userv1.AdminReviewShopRequest, _ ...grpc.CallOption) (*userv1.AdminReviewShopResponse, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastReviewShopReq = in
	if u.reviewShopErr != nil {
		return nil, u.reviewShopErr
	}
	return &userv1.AdminReviewShopResponse{
		Shop: &userv1.Shop{Id: in.GetShopId(), Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE},
	}, nil
}

func (u *w3aUserClient) AdminReviewShopBankAccount(_ context.Context, in *userv1.AdminReviewShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.AdminReviewShopBankAccountResponse, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.reviewBankCalls++
	u.lastReviewBankReq = in
	if u.reviewBankErr != nil {
		return nil, u.reviewBankErr
	}
	return &userv1.AdminReviewShopBankAccountResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  in.GetBankAccountId(),
			ShopId:              in.GetShopId(),
			BankCode:            "VCB",
			AccountNumberMasked: "****7890",
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED,
		},
	}, nil
}

func (u *w3aUserClient) GetShopBankAccount(_ context.Context, in *userv1.GetShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.GetShopBankAccountResponse, error) {
	return &userv1.GetShopBankAccountResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  "ba-1",
			ShopId:              in.GetShopId(),
			BankCode:            "VCB",
			AccountNumberMasked: "****7890",
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP,
		},
	}, nil
}

func (u *w3aUserClient) AddShopBankAccount(_ context.Context, in *userv1.AddShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.AddShopBankAccountResponse, error) {
	u.mu.Lock()
	u.addBankCalls++
	u.mu.Unlock()
	return &userv1.AddShopBankAccountResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  "ba-1",
			ShopId:              in.GetShopId(),
			BankCode:            in.GetBankCode(),
			AccountNumberMasked: "****7890",
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP,
		},
		OtpSent: true,
	}, nil
}

func (u *w3aUserClient) VerifyShopBankAccountOTP(_ context.Context, in *userv1.VerifyShopBankAccountOTPRequest, _ ...grpc.CallOption) (*userv1.VerifyShopBankAccountOTPResponse, error) {
	u.mu.Lock()
	u.verifyOTPCalls++
	u.mu.Unlock()
	return &userv1.VerifyShopBankAccountOTPResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  "ba-1",
			ShopId:              in.GetShopId(),
			BankCode:            "VCB",
			AccountNumberMasked: "****7890",
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED,
		},
	}, nil
}

func (u *w3aUserClient) WriteAuditLog(_ context.Context, _ *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	return &userv1.WriteAuditLogResponse{AuditLog: &userv1.AuditLog{Id: "audit-w3a"}}, nil
}

type w3aPromotionClient struct {
	promotionv1.PromotionServiceClient
	mu               sync.Mutex
	previewCalls     int
	previewErr       error
	registerCalls    int
	registerErr      error
	lastRegisterReq  *promotionv1.RegisterFlashSaleItemRequest
	lastPreviewInput *promotionv1.PreviewDiscountsRequest
}

func (p *w3aPromotionClient) ListActiveFlashSales(_ context.Context, _ *promotionv1.ListActiveFlashSalesRequest, _ ...grpc.CallOption) (*promotionv1.ListActiveFlashSalesResponse, error) {
	now := timestamppb.Now()
	return &promotionv1.ListActiveFlashSalesResponse{
		FlashSales: []*promotionv1.FlashSale{
			{
				Id: "fs-1", Name: "Flash", StartsAt: now, EndsAt: now,
				Items: []*promotionv1.FlashSaleItem{
					{Id: "fsi-1", FlashSaleId: "fs-1", ShopId: "shop-a", SkuId: "sku-a-active", FlashPriceVnd: 150000, OriginalPriceVnd: 200000, TotalSlots: 10, RemainingSlots: 10},
				},
			},
		},
	}, nil
}

func (p *w3aPromotionClient) ListActiveShopDiscounts(_ context.Context, _ *promotionv1.ListActiveShopDiscountsRequest, _ ...grpc.CallOption) (*promotionv1.ListActiveShopDiscountsResponse, error) {
	return &promotionv1.ListActiveShopDiscountsResponse{
		Discounts: []*promotionv1.ActiveShopDiscount{
			{ItemId: "sdpi-1", ProgramId: "sdp-1", ShopId: "shop-a", SkuId: "sku-a-active", OriginalPriceVnd: 200000, DiscountedPriceVnd: 180000},
		},
	}, nil
}

func (p *w3aPromotionClient) PreviewDiscounts(_ context.Context, in *promotionv1.PreviewDiscountsRequest, _ ...grpc.CallOption) (*promotionv1.PreviewDiscountsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.previewCalls++
	p.lastPreviewInput = in
	if p.previewErr != nil {
		return nil, p.previewErr
	}
	return &promotionv1.PreviewDiscountsResponse{
		Breakdown: &promotionv1.DiscountBreakdown{
			TotalSubtotalVnd: 200000,
			FinalPayableVnd:  200000,
		},
	}, nil
}

func (p *w3aPromotionClient) RegisterFlashSaleItem(_ context.Context, in *promotionv1.RegisterFlashSaleItemRequest, _ ...grpc.CallOption) (*promotionv1.RegisterFlashSaleItemResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.registerCalls++
	p.lastRegisterReq = in
	if p.registerErr != nil {
		return nil, p.registerErr
	}
	return &promotionv1.RegisterFlashSaleItemResponse{
		Item: &promotionv1.FlashSaleItem{
			Id:            "fsi-new",
			FlashSaleId:   in.GetFlashSaleId(),
			ShopId:        in.GetShopId(),
			SkuId:         in.GetSkuId(),
			FlashPriceVnd: in.GetFlashPriceVnd(),
			TotalSlots:    in.GetTotalSlots(),
		},
	}, nil
}

type w3aShippingClient struct {
	shippingv1.ShippingServiceClient
	mu              sync.Mutex
	getSettingsCall int
	calcFeeCalls    int
}

func (s *w3aShippingClient) GetShopShippingSettings(_ context.Context, in *shippingv1.GetShopShippingSettingsRequest, _ ...grpc.CallOption) (*shippingv1.GetShopShippingSettingsResponse, error) {
	s.mu.Lock()
	s.getSettingsCall++
	s.mu.Unlock()
	return &shippingv1.GetShopShippingSettingsResponse{
		Settings: &shippingv1.ShopShippingSettings{ShopId: in.GetShopId(), DefaultWeightGrams: 500},
	}, nil
}

func (s *w3aShippingClient) CalculateFee(_ context.Context, _ *shippingv1.CalculateFeeRequest, _ ...grpc.CallOption) (*shippingv1.CalculateFeeResponse, error) {
	s.mu.Lock()
	s.calcFeeCalls++
	s.mu.Unlock()
	return &shippingv1.CalculateFeeResponse{
		Quotes: []*shippingv1.CarrierQuote{{CarrierCode: "ghn", ServiceCode: "STANDARD", FeeVnd: 25000}},
	}, nil
}

type w3aCartClient struct {
	cartv1.CartServiceClient
}

func (c *w3aCartClient) GetCart(_ context.Context, in *cartv1.GetCartRequest, _ ...grpc.CallOption) (*cartv1.GetCartResponse, error) {
	return &cartv1.GetCartResponse{
		Cart: &cartv1.Cart{
			UserId: in.GetUserId(),
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId: "shop-a",
					Items: []*cartv1.CartItem{
						{SkuId: "sku-a-active", ProductId: "prod-a", ShopId: "shop-a", ProductName: "Shirt", PriceVnd: 200000, Quantity: 1},
					},
				},
			},
			TotalItems: 1,
			TotalVnd:   200000,
		},
	}, nil
}

func newW3ATestHarness(t *testing.T) (http.Handler, *auth.Manager, *w3aUserClient, *w3aProductClient, *w3aPromotionClient, *w3aShippingClient) {
	t.Helper()
	authMgr, err := auth.NewManager("w3a-gateway-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}

	userMock := &w3aUserClient{}
	prodMock := &w3aProductClient{
		categories: []*productv1.Category{
			{Id: "cat-1", Name: "Áo thun", Slug: "ao-thun", ParentId: "", CommissionRateBps: 550, SortOrder: 1},
			{Id: "cat-0", Name: "Khuyến mãi 0%", Slug: "promo-zero", ParentId: "cat-1", CommissionRateBps: 0, SortOrder: 2},
		},
		products: map[string]*productv1.Product{
			"prod-a": {
				Id:          "prod-a",
				ShopId:      "shop-a",
				CategoryId:  "cat-1",
				Name:        "Áo Thun A",
				Slug:        "ao-thun-a",
				Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				MinPriceVnd: 200000,
				MaxPriceVnd: 200000,
				Skus: []*productv1.SKU{
					{
						Id:                "sku-a-active",
						ProductId:         "prod-a",
						ShopId:            "shop-a",
						ProductName:       "Áo Thun A",
						SkuCode:           "A-M",
						Name:              "Size M",
						PriceVnd:          200000,
						MinPrice_30DVnd:   200000,
						Stock:             20,
						AvailableStock:    20,
						WeightGrams:       300,
						CategoryId:        "cat-1",
						CommissionRateBps: 550,
						IsActive:          true,
					},
					{
						Id:                "sku-a-zero-bps",
						ProductId:         "prod-a",
						ShopId:            "shop-a",
						ProductName:       "Áo Thun A",
						SkuCode:           "A-L",
						Name:              "Size L",
						PriceVnd:          200000,
						MinPrice_30DVnd:   200000,
						Stock:             10,
						AvailableStock:    10,
						WeightGrams:       300,
						CategoryId:        "cat-0",
						CommissionRateBps: 0,
						IsActive:          true,
					},
				},
			},
			"prod-b": {
				Id:          "prod-b",
				ShopId:      "shop-b",
				CategoryId:  "cat-1",
				Name:        "Áo Polo B",
				Slug:        "ao-polo-b",
				Status:      productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				MinPriceVnd: 250000,
				MaxPriceVnd: 250000,
				Skus: []*productv1.SKU{
					{
						Id:                "sku-b-active",
						ProductId:         "prod-b",
						ShopId:            "shop-b",
						ProductName:       "Áo Polo B",
						SkuCode:           "B-M",
						Name:              "Size M",
						PriceVnd:          250000,
						MinPrice_30DVnd:   250000,
						Stock:             15,
						AvailableStock:    15,
						WeightGrams:       350,
						CategoryId:        "cat-1",
						CommissionRateBps: 600,
						IsActive:          true,
					},
				},
			},
		},
		skus: map[string]*productv1.SKU{
			"sku-a-active": {
				Id:                "sku-a-active",
				ProductId:         "prod-a",
				ShopId:            "shop-a",
				ProductName:       "Áo Thun A",
				SkuCode:           "A-M",
				Name:              "Size M",
				PriceVnd:          200000,
				WeightGrams:       300,
				CategoryId:        "cat-1",
				CommissionRateBps: 550,
				IsActive:          true,
			},
			"sku-b-active": {
				Id:                "sku-b-active",
				ProductId:         "prod-b",
				ShopId:            "shop-b",
				ProductName:       "Áo Polo B",
				SkuCode:           "B-M",
				Name:              "Size M",
				PriceVnd:          250000,
				WeightGrams:       350,
				CategoryId:        "cat-1",
				CommissionRateBps: 600,
				IsActive:          true,
			},
			// Exact D2 redacted shape for hidden/inactive SKUs: only id, product_id, shop_id, product_name, name, image_url, is_active=false.
			"sku-a-hidden": {
				Id:          "sku-a-hidden",
				ProductId:   "prod-a-hid",
				ShopId:      "shop-a",
				ProductName: "Hidden Product A",
				Name:        "Hidden M",
				ImageUrl:    "/img/hidden-a.png",
				IsActive:    false,
			},
			"sku-b-hidden": {
				Id:          "sku-b-hidden",
				ProductId:   "prod-b-hid",
				ShopId:      "shop-b",
				ProductName: "Hidden Product B",
				Name:        "Hidden L",
				ImageUrl:    "/img/hidden-b.png",
				IsActive:    false,
			},
		},
	}
	promoMock := &w3aPromotionClient{}
	shipMock := &w3aShippingClient{}
	cartMock := &w3aCartClient{}

	h := New(Dependencies{
		ServiceName: "gateway-w3a-test",
		Clients: &clients.Clients{
			User:      userMock,
			Product:   prodMock,
			Promotion: promoMock,
			Shipping:  shipMock,
			Cart:      cartMock,
			Timeout:   5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-w3a-test", logger.WithWriter(io.Discard)),
	})
	return h, authMgr, userMock, prodMock, promoMock, shipMock
}

func w3aDoRequest(h http.Handler, method, target, token, body string) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
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

// TestW3A_D1_CommissionStrippedPublicAndKeptAdminSeller verifies D1:
// - Public routes strip commission_rate_bps completely from JSON (even when called with admin/seller token).
// - New GET /api/v1/admin/categories enforces admin/super_admin RBAC and retains commission_rate_bps (including 0).
// - Seller and admin product/category routes keep commission_rate_bps.
func TestW3A_D1_CommissionStrippedPublicAndKeptAdminSeller(t *testing.T) {
	h, authMgr, _, prodMock, _, _ := newW3ATestHarness(t)

	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")
	superAdminTok, _, _, _ := authMgr.SignAccessToken("super-1", "super_admin", "")

	// 1. Public routes MUST omit commission_rate_bps completely (tested anon, buyer, seller, admin).
	publicCases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/categories", ""},
		{http.MethodGet, "/api/v1/products", ""},
		{http.MethodGet, "/api/v1/products/prod-a", ""},
		{http.MethodGet, "/api/v1/products/slug/ao-thun-a", ""},
		{http.MethodGet, "/api/v1/shops", ""},
		{http.MethodGet, "/api/v1/shops/shop-a", ""},
		{http.MethodGet, "/api/v1/flash-sales", ""},
		{http.MethodGet, "/api/v1/shop-discounts/active?shop_id=shop-a", ""},
	}
	for _, tc := range publicCases {
		for label, tok := range map[string]string{
			"anon":   "",
			"buyer":  buyerTok,
			"seller": sellerTok,
			"admin":  adminTok,
		} {
			rec := w3aDoRequest(h, tc.method, tc.path, tok, tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s %s (%s): want 200, got %d (%s)", tc.method, tc.path, label, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "commission_rate_bps") {
				t.Fatalf("%s %s (%s) leaked commission_rate_bps in public JSON: %s", tc.method, tc.path, label, rec.Body.String())
			}
		}
	}

	// Buyer-only routes (cart & preview) also MUST NOT expose commission_rate_bps.
	if rec := w3aDoRequest(h, http.MethodGet, "/api/v1/cart", buyerTok, ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "commission_rate_bps") {
		t.Fatalf("GET /cart: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := w3aDoRequest(h, http.MethodPost, "/api/v1/promotions/preview", buyerTok, `{"sales_channel":"MARKETPLACE","shop_items":[{"shop_id":"shop-a","shipping_fee_vnd":20000,"items":[{"sku_id":"sku-a-active","quantity":1}]}]}`); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "commission_rate_bps") {
		t.Fatalf("POST /promotions/preview: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 2. GET /api/v1/admin/categories: RBAC (401 anon, 403 buyer/seller, 200 admin/super_admin) + keeps commission_rate_bps (including 0) + forwards parent_id.
	if rec := w3aDoRequest(h, http.MethodGet, "/api/v1/admin/categories", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon GET /admin/categories: want 401, got %d", rec.Code)
	}
	for _, tok := range []string{buyerTok, sellerTok} {
		if rec := w3aDoRequest(h, http.MethodGet, "/api/v1/admin/categories", tok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin GET /admin/categories: want 403, got %d", rec.Code)
		}
	}
	for label, tok := range map[string]string{"admin": adminTok, "super_admin": superAdminTok} {
		rec := w3aDoRequest(h, http.MethodGet, "/api/v1/admin/categories", tok, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s GET /admin/categories: want 200, got %d (%s)", label, rec.Code, rec.Body.String())
		}
		var out struct {
			Categories []map[string]any `json:"categories"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode /admin/categories: %v (%s)", err, rec.Body.String())
		}
		if len(out.Categories) != 2 {
			t.Fatalf("expected 2 categories, got %d (%s)", len(out.Categories), rec.Body.String())
		}
		bpsBySlug := make(map[string]any, 2)
		for _, c := range out.Categories {
			v, exists := c["commission_rate_bps"]
			if !exists {
				t.Fatalf("%s GET /admin/categories omitted commission_rate_bps on %+v", label, c)
			}
			bpsBySlug[fmt.Sprint(c["slug"])] = v
		}
		if bpsBySlug["ao-thun"] != float64(550) || bpsBySlug["promo-zero"] != float64(0) {
			t.Fatalf("%s GET /admin/categories bps mismatch: %+v", label, bpsBySlug)
		}
	}
	// parent_id filter forwarded on /admin/categories.
	recChild := w3aDoRequest(h, http.MethodGet, "/api/v1/admin/categories?parent_id=cat-1", adminTok, "")
	if recChild.Code != http.StatusOK || prodMock.lastListCatReq.GetParentId() != "cat-1" {
		t.Fatalf("GET /admin/categories?parent_id=cat-1 mismatch: code=%d req=%+v", recChild.Code, prodMock.lastListCatReq)
	}

	// 2b. GET /api/v1/seller/categories (D1 supplement): RBAC all 4 roles + anon, same JSON shape as /admin/categories.
	if rec := w3aDoRequest(h, http.MethodGet, "/api/v1/seller/categories", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon GET /seller/categories: want 401, got %d (%s)", rec.Code, rec.Body.String())
	}
	for role, tok := range map[string]string{
		"buyer":       buyerTok,
		"admin":       adminTok,
		"super_admin": superAdminTok,
	} {
		if rec := w3aDoRequest(h, http.MethodGet, "/api/v1/seller/categories", tok, ""); rec.Code != http.StatusForbidden {
			t.Fatalf("%s GET /seller/categories: want 403, got %d (%s)", role, rec.Code, rec.Body.String())
		}
	}
	recSellerCats := w3aDoRequest(h, http.MethodGet, "/api/v1/seller/categories", sellerTok, "")
	if recSellerCats.Code != http.StatusOK {
		t.Fatalf("seller GET /seller/categories: want 200, got %d (%s)", recSellerCats.Code, recSellerCats.Body.String())
	}
	recAdminCats := w3aDoRequest(h, http.MethodGet, "/api/v1/admin/categories", adminTok, "")
	if recSellerCats.Body.String() != recAdminCats.Body.String() {
		t.Fatalf("GET /seller/categories JSON shape differs from /admin/categories:\nseller=%s\nadmin=%s",
			recSellerCats.Body.String(), recAdminCats.Body.String())
	}
	if !strings.Contains(recSellerCats.Body.String(), `"commission_rate_bps":550`) ||
		!strings.Contains(recSellerCats.Body.String(), `"commission_rate_bps":0`) {
		t.Fatalf("seller GET /seller/categories must retain commission_rate_bps including 0: %s", recSellerCats.Body.String())
	}
	recSellerCatsChild := w3aDoRequest(h, http.MethodGet, "/api/v1/seller/categories?parent_id=cat-1", sellerTok, "")
	if recSellerCatsChild.Code != http.StatusOK || prodMock.lastListCatReq.GetParentId() != "cat-1" {
		t.Fatalf("GET /seller/categories?parent_id=cat-1 mismatch: code=%d req=%+v", recSellerCatsChild.Code, prodMock.lastListCatReq)
	}

	// 3. Seller product & stock routes retain commission_rate_bps (including explicit 0).
	sellerKeepCases := []struct {
		method, path, body string
		wantStatus         int
	}{
		{http.MethodGet, "/api/v1/seller/products", "", http.StatusOK},
		{http.MethodGet, "/api/v1/seller/products/prod-a", "", http.StatusOK},
		{http.MethodPost, "/api/v1/seller/products", `{"category_id":"cat-1","name":"New"}`, http.StatusCreated},
		{http.MethodPut, "/api/v1/seller/products/prod-a", `{"name":"Upd"}`, http.StatusOK},
		{http.MethodPatch, "/api/v1/seller/skus/sku-a-active/stock", `{"new_stock":10,"expected_version":1}`, http.StatusOK},
	}
	for _, tc := range sellerKeepCases {
		rec := w3aDoRequest(h, tc.method, tc.path, sellerTok, tc.body)
		if rec.Code != tc.wantStatus {
			t.Fatalf("seller %s %s: want %d, got %d (%s)", tc.method, tc.path, tc.wantStatus, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"commission_rate_bps":550`) {
			t.Fatalf("seller %s %s must retain commission_rate_bps:550, got %s", tc.method, tc.path, rec.Body.String())
		}
	}
	// Verify seller GET /seller/products/prod-a also preserves explicit 0 commission_rate_bps on sku-a-zero-bps.
	if rec := w3aDoRequest(h, http.MethodGet, "/api/v1/seller/products/prod-a", sellerTok, ""); !strings.Contains(rec.Body.String(), `"commission_rate_bps":0`) {
		t.Fatalf("seller GET /seller/products/prod-a must serialize explicit 0 commission_rate_bps: %s", rec.Body.String())
	}

	// 4. Admin product & category mutation routes retain commission_rate_bps.
	adminKeepCases := []struct {
		method, path, body string
		wantStatus         int
	}{
		{http.MethodGet, "/api/v1/admin/products", "", http.StatusOK},
		{http.MethodGet, "/api/v1/admin/products/prod-a", "", http.StatusOK},
		{http.MethodPost, "/api/v1/admin/products/prod-a/review", `{"decision":"APPROVE"}`, http.StatusOK},
		{http.MethodPost, "/api/v1/admin/categories", `{"name":"New","slug":"new","commission_rate_bps":550}`, http.StatusCreated},
		{http.MethodPut, "/api/v1/admin/categories/cat-1", `{"name":"Upd","commission_rate_bps":550}`, http.StatusOK},
	}
	for _, tc := range adminKeepCases {
		rec := w3aDoRequest(h, tc.method, tc.path, adminTok, tc.body)
		if rec.Code != tc.wantStatus {
			t.Fatalf("admin %s %s: want %d, got %d (%s)", tc.method, tc.path, tc.wantStatus, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"commission_rate_bps":550`) {
			t.Fatalf("admin %s %s must retain commission_rate_bps:550, got %s", tc.method, tc.path, rec.Body.String())
		}
	}
}

// TestW3A_D2_HiddenSKUHandling verifies D2 across preview, calculate-fee, and flash sale registration:
//   - preview & calculate-fee reject hidden/inactive SKU with 409 SKU_INACTIVE (never compute with price/weight 0).
//   - calculate-fee still returns uniform 400 for cross-shop hidden SKU (no existence oracle).
//   - flash sale registration checks shop ownership first: another shop's hidden SKU -> 403 PERMISSION_DENIED;
//     owning seller's hidden SKU forwards to PromotionService and keeps existing error code (409 FAILED_PRECONDITION).
func TestW3A_D2_HiddenSKUHandling(t *testing.T) {
	h, authMgr, _, _, promoMock, shipMock := newW3ATestHarness(t)

	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")

	decodeErr := func(t *testing.T, rec *httptest.ResponseRecorder) httpx.ErrorEnvelope {
		t.Helper()
		var env httpx.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
		}
		return env
	}

	// 1. POST /api/v1/promotions/preview with hidden SKU -> 409 SKU_INACTIVE, never calls shipping or promotion.
	t.Run("PreviewHiddenSKU_Returns409_SKU_INACTIVE", func(t *testing.T) {
		promoMock.mu.Lock()
		promoMock.previewCalls = 0
		promoMock.mu.Unlock()
		shipMock.mu.Lock()
		shipMock.getSettingsCall = 0
		shipMock.mu.Unlock()

		rec := w3aDoRequest(h, http.MethodPost, "/api/v1/promotions/preview", buyerTok, `{
			"sales_channel": "MARKETPLACE",
			"shop_items": [{
				"shop_id": "shop-a",
				"shipping_fee_vnd": 25000,
				"items": [{"sku_id": "sku-a-hidden", "quantity": 2}]
			}]
		}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("preview hidden SKU: want 409, got %d (%s)", rec.Code, rec.Body.String())
		}
		env := decodeErr(t, rec)
		if env.Error.Code != "SKU_INACTIVE" {
			t.Fatalf("preview hidden SKU error code: want SKU_INACTIVE, got %q (body=%s)", env.Error.Code, rec.Body.String())
		}
		if promoMock.previewCalls != 0 || shipMock.getSettingsCall != 0 {
			t.Fatalf("preview hidden SKU must not call upstream shipping/promotion: promo=%d ship=%d", promoMock.previewCalls, shipMock.getSettingsCall)
		}

		// P2-3: Unknown SKU, cross-shop active SKU, cross-shop hidden SKU, and missing shop_id + hidden SKU
		// MUST return the exact same 400 INVALID_ARGUMENT code AND message (no existence/ownership oracle).
		oraclePayloads := map[string]string{
			"unknown_sku":              `{"sales_channel":"MARKETPLACE","shop_items":[{"shop_id":"shop-a","shipping_fee_vnd":25000,"items":[{"sku_id":"sku-does-not-exist","quantity":1}]}]}`,
			"cross_shop_active_sku":    `{"sales_channel":"MARKETPLACE","shop_items":[{"shop_id":"shop-a","shipping_fee_vnd":25000,"items":[{"sku_id":"sku-b-active","quantity":1}]}]}`,
			"cross_shop_hidden_sku":    `{"sales_channel":"MARKETPLACE","shop_items":[{"shop_id":"shop-a","shipping_fee_vnd":25000,"items":[{"sku_id":"sku-b-hidden","quantity":1}]}]}`,
			"missing_shop_hidden_sku":  `{"sales_channel":"MARKETPLACE","shop_items":[{"shop_id":"","shipping_fee_vnd":25000,"items":[{"sku_id":"sku-a-hidden","quantity":1}]}]}`,
			"missing_shop_unknown_sku": `{"sales_channel":"MARKETPLACE","shop_items":[{"shop_id":"","shipping_fee_vnd":25000,"items":[{"sku_id":"sku-does-not-exist","quantity":1}]}]}`,
		}
		var baselineMsg string
		for name, payload := range oraclePayloads {
			recO := w3aDoRequest(h, http.MethodPost, "/api/v1/promotions/preview", buyerTok, payload)
			if recO.Code != http.StatusBadRequest {
				t.Fatalf("preview %s: want 400, got %d (%s)", name, recO.Code, recO.Body.String())
			}
			envO := decodeErr(t, recO)
			if envO.Error.Code != "INVALID_ARGUMENT" {
				t.Fatalf("preview %s: want INVALID_ARGUMENT, got %q (%s)", name, envO.Error.Code, recO.Body.String())
			}
			if baselineMsg == "" {
				baselineMsg = envO.Error.Message
			} else if envO.Error.Message != baselineMsg {
				t.Fatalf("preview %s message %q differs from baseline %q (oracle leak!)", name, envO.Error.Message, baselineMsg)
			}
		}
		if promoMock.previewCalls != 0 || shipMock.getSettingsCall != 0 {
			t.Fatalf("preview oracle cases must not call upstream shipping/promotion: promo=%d ship=%d", promoMock.previewCalls, shipMock.getSettingsCall)
		}

		// Active SKU with empty shop_id (backward-compatible) still resolves shop_id and succeeds;
		// under D4, gateway omits redundant subtotal_vnd/unit_price_vnd/category_id (promotion resolves them).
		recEmptyShopActive := w3aDoRequest(h, http.MethodPost, "/api/v1/promotions/preview", buyerTok, `{
			"sales_channel": "MARKETPLACE",
			"shop_items": [{
				"shop_id": "",
				"shipping_fee_vnd": 20000,
				"items": [{"sku_id": "sku-a-active", "quantity": 2, "flash_sale_item_id": "fsi-1"}]
			}]
		}`)
		if recEmptyShopActive.Code != http.StatusOK {
			t.Fatalf("preview active SKU with empty shop_id: want 200, got %d (%s)", recEmptyShopActive.Code, recEmptyShopActive.Body.String())
		}
		if gotIn := promoMock.lastPreviewInput; gotIn == nil || len(gotIn.GetShopItems()) != 1 || gotIn.GetShopItems()[0].GetShopId() != "shop-a" {
			t.Fatalf("preview active SKU with empty shop_id did not resolve shop-a: %+v", gotIn)
		} else {
			si := gotIn.GetShopItems()[0]
			if si.GetSubtotalVnd() != 0 || len(si.GetItems()) != 1 ||
				si.GetItems()[0].GetUnitPriceVnd() != 0 || si.GetItems()[0].GetCategoryId() != "" ||
				si.GetItems()[0].GetSkuId() != "sku-a-active" || si.GetItems()[0].GetQuantity() != 2 ||
				si.GetItems()[0].GetFlashSaleItemId() != "fsi-1" {
				t.Fatalf("D4 ponytail preview request must omit redundant price/subtotal/category and forward sku/qty/flash: %+v", si)
			}
		}

		// Race: SKU active at gateway GetSKUsBatch, becomes inactive at PromotionService.PreviewDiscounts -> still 409 SKU_INACTIVE.
		promoMock.mu.Lock()
		promoMock.previewErr = status.Error(codes.FailedPrecondition, "SKU_INACTIVE: sku sku-a-active is not active")
		promoMock.mu.Unlock()
		defer func() {
			promoMock.mu.Lock()
			promoMock.previewErr = nil
			promoMock.mu.Unlock()
		}()
		recRace := w3aDoRequest(h, http.MethodPost, "/api/v1/promotions/preview", buyerTok, `{
			"sales_channel": "MARKETPLACE",
			"shop_items": [{
				"shop_id": "shop-a",
				"shipping_fee_vnd": 25000,
				"items": [{"sku_id": "sku-a-active", "quantity": 1}]
			}]
		}`)
		if recRace.Code != http.StatusConflict {
			t.Fatalf("preview TOCTOU SKU_INACTIVE: want 409, got %d (%s)", recRace.Code, recRace.Body.String())
		}
		if envRace := decodeErr(t, recRace); envRace.Error.Code != "SKU_INACTIVE" {
			t.Fatalf("preview TOCTOU error code: want SKU_INACTIVE, got %q", envRace.Error.Code)
		}
	})

	// 2. POST /api/v1/shipping/calculate-fee with hidden SKU -> 409 SKU_INACTIVE, never calls shipping.
	t.Run("CalculateFeeHiddenSKU_Returns409_SKU_INACTIVE", func(t *testing.T) {
		shipMock.mu.Lock()
		shipMock.getSettingsCall = 0
		shipMock.calcFeeCalls = 0
		shipMock.mu.Unlock()

		for _, pm := range []string{"VIETQR", "COD"} {
			rec := w3aDoRequest(h, http.MethodPost, "/api/v1/shipping/calculate-fee", buyerTok, fmt.Sprintf(`{
				"shop_id": "shop-a",
				"to_province_code": "79",
				"to_ward_code": "79001",
				"payment_method": %q,
				"items": [{"sku_id": "sku-a-hidden", "quantity": 2}]
			}`, pm))
			if rec.Code != http.StatusConflict {
				t.Fatalf("calculate-fee (%s) hidden SKU: want 409, got %d (%s)", pm, rec.Code, rec.Body.String())
			}
			env := decodeErr(t, rec)
			if env.Error.Code != "SKU_INACTIVE" {
				t.Fatalf("calculate-fee (%s) hidden SKU error code: want SKU_INACTIVE, got %q", pm, env.Error.Code)
			}
		}
		if shipMock.getSettingsCall != 0 || shipMock.calcFeeCalls != 0 {
			t.Fatalf("calculate-fee hidden SKU must not call shipping service: settings=%d calc=%d", shipMock.getSettingsCall, shipMock.calcFeeCalls)
		}

		// Cross-shop hidden SKU still returns uniform 400 INVALID_ARGUMENT (indistinguishable from unknown SKU).
		recOther := w3aDoRequest(h, http.MethodPost, "/api/v1/shipping/calculate-fee", buyerTok, `{
			"shop_id": "shop-a",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"payment_method": "VIETQR",
			"items": [{"sku_id": "sku-b-hidden", "quantity": 1}]
		}`)
		recUnknown := w3aDoRequest(h, http.MethodPost, "/api/v1/shipping/calculate-fee", buyerTok, `{
			"shop_id": "shop-a",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"payment_method": "VIETQR",
			"items": [{"sku_id": "sku-nonexistent", "quantity": 1}]
		}`)
		if recOther.Code != http.StatusBadRequest || recUnknown.Code != http.StatusBadRequest {
			t.Fatalf("cross-shop hidden vs unknown SKU: want both 400, got %d and %d", recOther.Code, recUnknown.Code)
		}
		eOther := decodeErr(t, recOther)
		eUnknown := decodeErr(t, recUnknown)
		if eOther.Error.Code != eUnknown.Error.Code || eOther.Error.Message != eUnknown.Error.Message {
			t.Fatalf("cross-shop hidden SKU must match unknown SKU error: %+v vs %+v", eOther.Error, eUnknown.Error)
		}
	})

	// 3. POST /api/v1/seller/flash-sales/{id}/items with hidden SKU:
	// - Another shop's hidden SKU -> 403 PERMISSION_DENIED (shop checked FIRST; Promotion NOT called).
	// - Own shop's hidden SKU -> forwarded to PromotionService (keeps existing error code, e.g. 409 FAILED_PRECONDITION).
	t.Run("RegisterFlashSaleHiddenSKU_OtherShop403_OwnerKeepsOldCode", func(t *testing.T) {
		promoMock.mu.Lock()
		promoMock.registerCalls = 0
		promoMock.registerErr = nil
		promoMock.mu.Unlock()

		// Other shop's hidden SKU -> 403 PERMISSION_DENIED.
		recOther := w3aDoRequest(h, http.MethodPost, "/api/v1/seller/flash-sales/fs-1/items", sellerATok, `{
			"sku_id": "sku-b-hidden",
			"flash_price_vnd": 100000,
			"total_slots": 5,
			"per_user_limit": 1
		}`)
		if recOther.Code != http.StatusForbidden {
			t.Fatalf("other shop hidden SKU on flash register: want 403, got %d (%s)", recOther.Code, recOther.Body.String())
		}
		if env := decodeErr(t, recOther); env.Error.Code != "PERMISSION_DENIED" {
			t.Fatalf("other shop hidden SKU error code: want PERMISSION_DENIED, got %q", env.Error.Code)
		}
		if promoMock.registerCalls != 0 {
			t.Fatalf("other shop hidden SKU must not call Promotion.RegisterFlashSaleItem, got %d", promoMock.registerCalls)
		}

		// Own shop's hidden SKU -> forwarded to PromotionService, keeping existing error code (FAILED_PRECONDITION -> 409).
		promoMock.mu.Lock()
		promoMock.registerErr = status.Error(codes.FailedPrecondition, "sku sku-a-hidden is not active")
		promoMock.mu.Unlock()
		defer func() {
			promoMock.mu.Lock()
			promoMock.registerErr = nil
			promoMock.mu.Unlock()
		}()

		recOwn := w3aDoRequest(h, http.MethodPost, "/api/v1/seller/flash-sales/fs-1/items", sellerATok, `{
			"sku_id": "sku-a-hidden",
			"flash_price_vnd": 100000,
			"total_slots": 5,
			"per_user_limit": 1
		}`)
		if promoMock.registerCalls != 1 {
			t.Fatalf("own shop hidden SKU must reach PromotionService to keep existing error codes, got %d calls", promoMock.registerCalls)
		}
		if recOwn.Code != http.StatusConflict {
			t.Fatalf("own shop hidden SKU: want 409, got %d (%s)", recOwn.Code, recOwn.Body.String())
		}
		if env := decodeErr(t, recOwn); env.Error.Code != "FAILED_PRECONDITION" {
			t.Fatalf("own shop hidden SKU must preserve FAILED_PRECONDITION code, got %q", env.Error.Code)
		}
	})
}

// TestW3A_D6_BankAccountReviewAndShopExpectedOwnerName verifies D6:
//   - POST /admin/shops/{id}/bank-account/review requires non-empty bank_account_id (missing/blank -> 400 before RPC);
//     stale/non-pending row from upstream returns 409.
//   - POST /admin/shops/{id}/review forwards optional expected_owner_name; OWNER_NAME_CHANGED from upstream returns
//     409 with error.code == "OWNER_NAME_CHANGED".
func TestW3A_D6_BankAccountReviewAndShopExpectedOwnerName(t *testing.T) {
	h, authMgr, userMock, _, _, _ := newW3ATestHarness(t)
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	decodeErr := func(t *testing.T, rec *httptest.ResponseRecorder) httpx.ErrorEnvelope {
		t.Helper()
		var env httpx.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode error envelope: %v (body=%s)", err, rec.Body.String())
		}
		return env
	}

	// 1. Bank account review: missing or whitespace-only bank_account_id -> 400 INVALID_ARGUMENT before calling UserService.
	for _, badBody := range []string{
		`{"decision":"APPROVE","reason":"ok"}`,
		`{"bank_account_id":"","decision":"APPROVE","reason":"ok"}`,
		`{"bank_account_id":"   ","decision":"REJECT","reason":"mismatch"}`,
	} {
		rec := w3aDoRequest(h, http.MethodPost, "/api/v1/admin/shops/shop-a/bank-account/review", adminTok, badBody)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bank-account review %s: want 400, got %d (%s)", badBody, rec.Code, rec.Body.String())
		}
		if env := decodeErr(t, rec); env.Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("bank-account review %s: want INVALID_ARGUMENT, got %q", badBody, env.Error.Code)
		}
	}
	if userMock.reviewBankCalls != 0 {
		t.Fatalf("missing bank_account_id must fail at gateway without calling UserService, got %d calls", userMock.reviewBankCalls)
	}

	// Valid bank_account_id forwarded (trimmed) -> 200 OK.
	recOK := w3aDoRequest(h, http.MethodPost, "/api/v1/admin/shops/shop-a/bank-account/review", adminTok, `{"bank_account_id":" ba-42 ","decision":"APPROVE","reason":"verified"}`)
	if recOK.Code != http.StatusOK || userMock.lastReviewBankReq.GetBankAccountId() != "ba-42" {
		t.Fatalf("valid bank-account review: code=%d req=%+v body=%s", recOK.Code, userMock.lastReviewBankReq, recOK.Body.String())
	}

	// Non-pending/stale bank account row -> 409 FAILED_PRECONDITION.
	userMock.mu.Lock()
	userMock.reviewBankErr = status.Error(codes.FailedPrecondition, "only PENDING_ADMIN_REVIEW bank accounts can be reviewed")
	userMock.mu.Unlock()
	recStale := w3aDoRequest(h, http.MethodPost, "/api/v1/admin/shops/shop-a/bank-account/review", adminTok, `{"bank_account_id":"ba-old","decision":"APPROVE","reason":"stale"}`)
	userMock.mu.Lock()
	userMock.reviewBankErr = nil
	userMock.mu.Unlock()
	if recStale.Code != http.StatusConflict {
		t.Fatalf("stale bank-account review: want 409, got %d (%s)", recStale.Code, recStale.Body.String())
	}

	// 2. AdminReviewShop: forwards expected_owner_name; OWNER_NAME_CHANGED -> 409 with error.code == "OWNER_NAME_CHANGED".
	recShopOK := w3aDoRequest(h, http.MethodPost, "/api/v1/admin/shops/shop-a/review", adminTok, `{"decision":"APPROVE","reason":"ok","expected_owner_name":"Nguyễn Văn A"}`)
	if recShopOK.Code != http.StatusOK {
		t.Fatalf("shop review with expected_owner_name: want 200, got %d (%s)", recShopOK.Code, recShopOK.Body.String())
	}
	if userMock.lastReviewShopReq == nil || userMock.lastReviewShopReq.GetExpectedOwnerName() != "Nguyễn Văn A" {
		t.Fatalf("AdminReviewShopRequest did not forward expected_owner_name: %+v", userMock.lastReviewShopReq)
	}

	// Mismatched expected_owner_name -> 409 OWNER_NAME_CHANGED.
	userMock.mu.Lock()
	userMock.reviewShopErr = status.Error(codes.FailedPrecondition, "OWNER_NAME_CHANGED: shop owner full_name has changed since review started")
	userMock.mu.Unlock()
	recMismatch := w3aDoRequest(h, http.MethodPost, "/api/v1/admin/shops/shop-a/review", adminTok, `{"decision":"APPROVE","expected_owner_name":"Nguyễn Văn Cũ"}`)
	userMock.mu.Lock()
	userMock.reviewShopErr = nil
	userMock.mu.Unlock()
	if recMismatch.Code != http.StatusConflict {
		t.Fatalf("shop review OWNER_NAME_CHANGED: want 409, got %d (%s)", recMismatch.Code, recMismatch.Body.String())
	}
	envMismatch := decodeErr(t, recMismatch)
	if envMismatch.Error.Code != "OWNER_NAME_CHANGED" || !strings.Contains(envMismatch.Error.Message, "OWNER_NAME_CHANGED") {
		t.Fatalf("shop review OWNER_NAME_CHANGED envelope mismatch: %+v", envMismatch.Error)
	}

	// Other FailedPrecondition on shop review keeps FAILED_PRECONDITION.
	userMock.mu.Lock()
	userMock.reviewShopErr = status.Error(codes.FailedPrecondition, "only PENDING shops can be reviewed")
	userMock.mu.Unlock()
	recNotPending := w3aDoRequest(h, http.MethodPost, "/api/v1/admin/shops/shop-a/review", adminTok, `{"decision":"APPROVE"}`)
	userMock.mu.Lock()
	userMock.reviewShopErr = nil
	userMock.mu.Unlock()
	if recNotPending.Code != http.StatusConflict {
		t.Fatalf("shop review non-pending: want 409, got %d (%s)", recNotPending.Code, recNotPending.Body.String())
	}
	if envNP := decodeErr(t, recNotPending); envNP.Error.Code != "FAILED_PRECONDITION" {
		t.Fatalf("shop review non-pending code: want FAILED_PRECONDITION, got %q", envNP.Error.Code)
	}
}

// TestW3A_D8_ListProductsSKUIDs verifies D8:
// - GET /api/v1/products?sku_ids=a,b parses, deduplicates, forwards to Product.ListProducts, omits commission_rate_bps.
// - Accepts up to 200 SKU IDs; rejects >200 (even with duplicates) and rejects invalid/empty elements with 400.
func TestW3A_D8_ListProductsSKUIDs(t *testing.T) {
	h, _, _, prodMock, _, _ := newW3ATestHarness(t)

	// 1. Valid comma-separated sku_ids (with duplicates) -> deduplicated & forwarded, returns matching public products without commission_rate_bps.
	rec := w3aDoRequest(h, http.MethodGet, "/api/v1/products?sku_ids=sku-a-active,sku-b-active,sku-a-active", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /products?sku_ids valid: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "commission_rate_bps") {
		t.Fatalf("GET /products?sku_ids leaked commission_rate_bps: %s", rec.Body.String())
	}
	gotIDs := prodMock.lastListProdReq.GetSkuIds()
	if len(gotIDs) != 2 || gotIDs[0] != "sku-a-active" || gotIDs[1] != "sku-b-active" {
		t.Fatalf("forwarded SkuIds = %v, want [sku-a-active sku-b-active]", gotIDs)
	}

	// Repeated query param (?sku_ids=a&sku_ids=b) also supported.
	recRep := w3aDoRequest(h, http.MethodGet, "/api/v1/products?sku_ids=sku-a-active&sku_ids=sku-b-active", "", "")
	if recRep.Code != http.StatusOK {
		t.Fatalf("GET /products repeated sku_ids: want 200, got %d (%s)", recRep.Code, recRep.Body.String())
	}
	if ids := prodMock.lastListProdReq.GetSkuIds(); len(ids) != 2 || ids[0] != "sku-a-active" || ids[1] != "sku-b-active" {
		t.Fatalf("repeated sku_ids forwarded = %v", ids)
	}

	// Add a PENDING product in mock to verify gateway filters non-public products even when queried via sku_ids (P3-4).
	prodMock.mu.Lock()
	prodMock.products["prod-pending"] = &productv1.Product{
		Id:          "prod-pending",
		ShopId:      "shop-a",
		CategoryId:  "cat-1",
		Name:        "Áo Chờ Duyệt",
		Slug:        "ao-cho-duyet",
		Status:      productv1.ProductStatus_PRODUCT_STATUS_PENDING,
		MinPriceVnd: 180000,
		MaxPriceVnd: 180000,
		Skus: []*productv1.SKU{
			{
				Id:                "sku-pending",
				ProductId:         "prod-pending",
				ShopId:            "shop-a",
				PriceVnd:          180000,
				CommissionRateBps: 550,
				IsActive:          false,
			},
		},
	}
	prodMock.mu.Unlock()

	recPendOnly := w3aDoRequest(h, http.MethodGet, "/api/v1/products?sku_ids=sku-pending", "", "")
	if recPendOnly.Code != http.StatusOK {
		t.Fatalf("GET /products?sku_ids=sku-pending: want 200, got %d (%s)", recPendOnly.Code, recPendOnly.Body.String())
	}
	var pendOut struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(recPendOnly.Body.Bytes(), &pendOut); err != nil {
		t.Fatalf("decode pending sku_ids response: %v", err)
	}
	if len(pendOut.Items) != 0 || pendOut.Total != 0 {
		t.Fatalf("GET /products?sku_ids=sku-pending must return 0 items & total=0, got items=%d total=%d (%s)",
			len(pendOut.Items), pendOut.Total, recPendOnly.Body.String())
	}

	recMixedPend := w3aDoRequest(h, http.MethodGet, "/api/v1/products?sku_ids=sku-a-active,sku-pending", "", "")
	if recMixedPend.Code != http.StatusOK {
		t.Fatalf("GET /products?sku_ids=sku-a-active,sku-pending: want 200, got %d (%s)", recMixedPend.Code, recMixedPend.Body.String())
	}
	var mixedOut struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(recMixedPend.Body.Bytes(), &mixedOut); err != nil {
		t.Fatalf("decode mixed pending sku_ids response: %v", err)
	}
	if len(mixedOut.Items) != 1 || mixedOut.Items[0].ID != "prod-a" || mixedOut.Total != 1 {
		t.Fatalf("GET /products?sku_ids=sku-a-active,sku-pending must return only prod-a (total=1), got %+v (%s)",
			mixedOut, recMixedPend.Body.String())
	}

	// Exact 200 valid SKU IDs succeeds.
	exact200 := make([]string, 200)
	for i := range exact200 {
		exact200[i] = fmt.Sprintf("sku-%03d", i+1)
	}
	rec200 := w3aDoRequest(h, http.MethodGet, "/api/v1/products?sku_ids="+strings.Join(exact200, ","), "", "")
	if rec200.Code != http.StatusOK || len(prodMock.lastListProdReq.GetSkuIds()) != 200 {
		t.Fatalf("GET /products 200 sku_ids: code=%d forwarded=%d", rec200.Code, len(prodMock.lastListProdReq.GetSkuIds()))
	}

	// 2. Invalid & >200 cases -> ALL return 400 INVALID_ARGUMENT before calling ProductService.
	tooManyUnique := make([]string, 201)
	tooManyDup := make([]string, 201)
	for i := range tooManyUnique {
		tooManyUnique[i] = fmt.Sprintf("sku-%03d", i+1)
		tooManyDup[i] = "sku-a-active"
	}
	tooLongID := strings.Repeat("a", 129)

	badQueries := []struct {
		name  string
		query string
	}{
		{"empty_value", "sku_ids="},
		{"only_commas", "sku_ids=,,"},
		{"empty_middle", "sku_ids=sku-a-active,,sku-b-active"},
		{"trailing_comma", "sku_ids=sku-a-active,"},
		{"leading_comma", "sku_ids=,sku-a-active"},
		{"whitespace_element", "sku_ids=sku-a-active,%20%20,sku-b-active"},
		{"embedded_space", "sku_ids=" + url.QueryEscape("sku bad")},
		{"invalid_char_semicolon", "sku_ids=" + url.QueryEscape("sku;drop")},
		{"invalid_char_slash", "sku_ids=" + url.QueryEscape("sku/1")},
		{"too_long_element", "sku_ids=" + tooLongID},
		{"over_200_unique", "sku_ids=" + strings.Join(tooManyUnique, ",")},
		{"over_200_duplicates", "sku_ids=" + strings.Join(tooManyDup, ",")},
		{"invalid_even_with_non_public_status", "sku_ids=,,&status=PENDING"},
	}
	for _, tc := range badQueries {
		t.Run("Reject_"+tc.name, func(t *testing.T) {
			prodMock.mu.Lock()
			callsBefore := prodMock.listProdCalls
			prodMock.mu.Unlock()

			recBad := w3aDoRequest(h, http.MethodGet, "/api/v1/products?"+tc.query, "", "")
			if recBad.Code != http.StatusBadRequest {
				t.Fatalf("GET /products?%s: want 400, got %d (%s)", tc.query, recBad.Code, recBad.Body.String())
			}
			var env httpx.ErrorEnvelope
			if err := json.Unmarshal(recBad.Body.Bytes(), &env); err != nil || env.Error.Code != "INVALID_ARGUMENT" {
				t.Fatalf("GET /products?%s: want INVALID_ARGUMENT envelope, got %s", tc.query, recBad.Body.String())
			}
			prodMock.mu.Lock()
			callsAfter := prodMock.listProdCalls
			prodMock.mu.Unlock()
			if callsAfter != callsBefore {
				t.Fatalf("GET /products?%s must not call Product.ListProducts on validation error", tc.query)
			}
		})
	}

	// 3. Round 4 [P2]: /shop-discounts/active treats completely empty sku_ids= as omitted (backward-compatible),
	// while still rejecting embedded/trailing empty elements with 400 (and /products?sku_ids= stays 400 above).
	t.Run("ShopDiscountsActiveEmptySKUIDsCompat", func(t *testing.T) {
		for _, okPath := range []string{
			"/api/v1/shop-discounts/active?shop_id=shop-a&sku_ids=",
			"/api/v1/shop-discounts/active?shop_id=shop-a&sku_ids=%20%20",
			"/api/v1/shop-discounts/active?shop_id=shop-a&sku_ids=&sku_ids=",
		} {
			recOK := w3aDoRequest(h, http.MethodGet, okPath, "", "")
			if recOK.Code != http.StatusOK {
				t.Fatalf("GET %s: want 200 (empty sku_ids treated as omitted), got %d (%s)", okPath, recOK.Code, recOK.Body.String())
			}
		}
		for _, badPath := range []string{
			"/api/v1/shop-discounts/active?sku_ids=",
			"/api/v1/shop-discounts/active?shop_id=shop-a&sku_ids=,,",
			"/api/v1/shop-discounts/active?shop_id=shop-a&sku_ids=sku-a-active,,sku-b-active",
			"/api/v1/shop-discounts/active?shop_id=shop-a&sku_ids=sku-a-active,",
			"/api/v1/shop-discounts/active?shop_id=shop-a&sku_ids=,sku-a-active",
		} {
			recBad := w3aDoRequest(h, http.MethodGet, badPath, "", "")
			if recBad.Code != http.StatusBadRequest {
				t.Fatalf("GET %s: want 400, got %d (%s)", badPath, recBad.Code, recBad.Body.String())
			}
		}
	})
}

type w3aMemRateLimiter struct {
	mu      sync.Mutex
	counts  map[string]int
	failAll bool
}

func (m *w3aMemRateLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (middleware.RateLimitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAll {
		return middleware.RateLimitResult{}, status.Error(codes.Unavailable, "redis down")
	}
	if m.counts == nil {
		m.counts = make(map[string]int)
	}
	if m.counts[key] >= limit {
		return middleware.RateLimitResult{Allowed: false, Remaining: 0, RetryAfter: window}, nil
	}
	m.counts[key]++
	return middleware.RateLimitResult{Allowed: true, Remaining: limit - m.counts[key]}, nil
}

// TestW3A_D7_BankOTPRateLimitOnRouter verifies router wiring for D7:
// POST /seller/bank-account and POST /seller/bank-account/verify-otp share a dedicated
// per-user bucket (default 5/min), GET /seller/bank-account and other routes stay unaffected,
// another seller stays unaffected, and Redis errors fail open matching existing limiters.
func TestW3A_D7_BankOTPRateLimitOnRouter(t *testing.T) {
	authMgr, err := auth.NewManager("w3a-d7-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-d7-a", "seller", "shop-a")
	sellerBTok, _, _, _ := authMgr.SignAccessToken("seller-d7-b", "seller", "shop-b")

	userMock := &w3aUserClient{}
	rl := &w3aMemRateLimiter{}
	h := New(Dependencies{
		ServiceName: "gateway-w3a-d7",
		Clients: &clients.Clients{
			User:    userMock,
			Timeout: 5 * time.Second,
		},
		AuthManager:            authMgr,
		RateLimiter:            rl,
		RateLimitIPPerMin:      300,
		RateLimitUserPerMin:    180,
		RateLimitBankOTPPerMin: 5,
		Logger:                 logger.New("gw-w3a-d7", logger.WithWriter(io.Discard)),
	})

	// 1. Seller A uses 5 calls across add bank account + verify OTP -> all succeed.
	addBody := `{"bank_code":"VCB","bank_name":"Vietcombank","account_number":"1234567890","account_holder_name":"NGUYEN VAN A"}`
	rec1 := w3aDoRequest(h, http.MethodPost, "/api/v1/seller/bank-account", sellerATok, addBody)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("call 1 POST /seller/bank-account: want 201, got %d (%s)", rec1.Code, rec1.Body.String())
	}
	for i := 2; i <= 5; i++ {
		rec := w3aDoRequest(h, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", sellerATok, `{"otp":"123456"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d POST /seller/bank-account/verify-otp: want 200, got %d (%s)", i, rec.Code, rec.Body.String())
		}
	}

	userMock.mu.Lock()
	addBefore := userMock.addBankCalls
	verifyBefore := userMock.verifyOTPCalls
	userMock.mu.Unlock()

	// 2. 6th call (verify-otp) and 7th call (add bank-account) for Seller A -> 429 RATE_LIMITED + Retry-After, no upstream RPC.
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/v1/seller/bank-account/verify-otp", `{"otp":"123456"}`},
		{"/api/v1/seller/bank-account", addBody},
	} {
		rec := w3aDoRequest(h, http.MethodPost, tc.path, sellerATok, tc.body)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("6th+ call POST %s: want 429, got %d (%s)", tc.path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatalf("POST %s 429 missing Retry-After header", tc.path)
		}
		var env httpx.ErrorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "RATE_LIMITED" {
			t.Fatalf("POST %s 429 want RATE_LIMITED envelope, got %s", tc.path, rec.Body.String())
		}
	}
	userMock.mu.Lock()
	if userMock.addBankCalls != addBefore || userMock.verifyOTPCalls != verifyBefore {
		t.Fatalf("429 must short-circuit before calling UserService (add=%d/%d verify=%d/%d)",
			userMock.addBankCalls, addBefore, userMock.verifyOTPCalls, verifyBefore)
	}
	userMock.mu.Unlock()

	// 3. GET /seller/bank-account and GET /seller/shop for Seller A are NOT blocked by bank OTP bucket.
	for _, p := range []string{"/api/v1/seller/bank-account", "/api/v1/seller/shop"} {
		recGet := w3aDoRequest(h, http.MethodGet, p, sellerATok, "")
		if recGet.Code != http.StatusOK {
			t.Fatalf("GET %s must not be blocked by bank OTP bucket: got %d (%s)", p, recGet.Code, recGet.Body.String())
		}
	}

	// 4. Seller B is unaffected.
	recB := w3aDoRequest(h, http.MethodPost, "/api/v1/seller/bank-account", sellerBTok, addBody)
	if recB.Code != http.StatusCreated {
		t.Fatalf("Seller B POST /seller/bank-account must stay 201, got %d (%s)", recB.Code, recB.Body.String())
	}

	// 5. Redis error fails open on POST /seller/bank-account/verify-otp.
	rl.mu.Lock()
	rl.failAll = true
	rl.mu.Unlock()
	recFailOpen := w3aDoRequest(h, http.MethodPost, "/api/v1/seller/bank-account/verify-otp", sellerATok, `{"otp":"123456"}`)
	if recFailOpen.Code != http.StatusOK {
		t.Fatalf("Redis error should fail open (200), got %d (%s)", recFailOpen.Code, recFailOpen.Body.String())
	}
}
