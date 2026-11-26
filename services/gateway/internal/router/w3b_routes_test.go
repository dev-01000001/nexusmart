package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	cartv1 "github.com/nexusmart/core/gen/go/nexusmart/cart/v1"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type w3bProductClient struct {
	productv1.ProductServiceClient
	mu                  sync.Mutex
	categories          []*productv1.Category
	products            map[string]*productv1.Product
	discontinuedBySlug  map[string]*productv1.GetProductResponse
	skus                map[string]*productv1.SKU
	lastCreateCatReq    *productv1.CreateCategoryRequest
	lastUpdateCatReq    *productv1.UpdateCategoryRequest
	lastGetProductReq   *productv1.GetProductRequest
	lastListReviewsReq  *productv1.ListProductReviewsRequest
	lastListReviewsMD   metadata.MD
	lastCreateReviewReq *productv1.CreateReviewRequest
	lastCreateReviewMD  metadata.MD
	createReviewErr     error
	lastOrderReviewsReq *productv1.ListOrderReviewsRequest
	lastListKWReq       *productv1.ListBannedKeywordsRequest
	lastCreateKWReq     *productv1.CreateBannedKeywordRequest
	lastUpdateKWReq     *productv1.UpdateBannedKeywordRequest
	lastDeleteKWReq     *productv1.DeleteBannedKeywordRequest
	lastPreviewKWReq    *productv1.PreviewBannedKeywordRequest
	lastRescanReq       *productv1.RescanProductsForBannedContentRequest
	rescanRespOverride  *productv1.RescanProductsForBannedContentResponse
}

func (p *w3bProductClient) ListCategories(_ context.Context, _ *productv1.ListCategoriesRequest, _ ...grpc.CallOption) (*productv1.ListCategoriesResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return &productv1.ListCategoriesResponse{Categories: p.categories}, nil
}

func (p *w3bProductClient) CreateCategory(_ context.Context, in *productv1.CreateCategoryRequest, _ ...grpc.CallOption) (*productv1.CreateCategoryResponse, error) {
	p.mu.Lock()
	p.lastCreateCatReq = in
	p.mu.Unlock()
	return &productv1.CreateCategoryResponse{
		Category: &productv1.Category{
			Id:                "cat-new",
			Name:              in.GetName(),
			Slug:              in.GetSlug(),
			ParentId:          in.GetParentId(),
			CommissionRateBps: in.GetCommissionRateBps(),
			SortOrder:         in.GetSortOrder(),
			IsProhibited:      in.GetIsProhibited(),
		},
	}, nil
}

func (p *w3bProductClient) UpdateCategory(_ context.Context, in *productv1.UpdateCategoryRequest, _ ...grpc.CallOption) (*productv1.UpdateCategoryResponse, error) {
	p.mu.Lock()
	p.lastUpdateCatReq = in
	p.mu.Unlock()
	return &productv1.UpdateCategoryResponse{
		Category: &productv1.Category{
			Id:                in.GetId(),
			Name:              in.GetName(),
			Slug:              in.GetSlug(),
			CommissionRateBps: in.GetCommissionRateBps(),
			IsProhibited:      in.GetIsProhibited(),
		},
	}, nil
}

func (p *w3bProductClient) ListProducts(_ context.Context, _ *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*productv1.Product, 0, len(p.products))
	for _, prod := range p.products {
		out = append(out, prod)
	}
	return &productv1.ListProductsResponse{Products: out, Total: int64(len(out))}, nil
}

func (p *w3bProductClient) GetProduct(_ context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastGetProductReq = in
	if in.GetSlug() != "" {
		if stub, ok := p.discontinuedBySlug[in.GetSlug()]; ok {
			if !in.GetIncludeDiscontinuedStub() {
				return nil, status.Error(codes.NotFound, "product not found")
			}
			return stub, nil
		}
	}
	for _, prod := range p.products {
		if (in.GetId() != "" && prod.GetId() == in.GetId()) ||
			(in.GetSlug() != "" && prod.GetSlug() == in.GetSlug()) {
			return &productv1.GetProductResponse{Product: prod}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "product not found")
}

func (p *w3bProductClient) GetSKUsBatch(_ context.Context, in *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*productv1.SKU, 0, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		if sk, ok := p.skus[id]; ok {
			out = append(out, sk)
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

func (p *w3bProductClient) ListProductReviews(ctx context.Context, in *productv1.ListProductReviewsRequest, _ ...grpc.CallOption) (*productv1.ListProductReviewsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastListReviewsReq = in
	p.lastListReviewsMD, _ = metadata.FromOutgoingContext(ctx)
	now := timestamppb.Now()
	// Intentionally populate internal fields to verify gateway strips them on public JSON (E6-bis).
	return &productv1.ListProductReviewsResponse{
		Reviews: []*productv1.ProductReview{
			{
				Id:          "rev-1",
				ProductId:   in.GetProductId(),
				ShopId:      "shop-a",
				OrderId:     "ord-secret-1",
				OrderItemId: "oi-secret-1",
				SkuId:       "sku-secret-1",
				SkuName:     "Size M / Trắng",
				BuyerId:     "buyer-secret-1",
				BuyerName:   "N. Linh",
				Rating:      5,
				Comment:     "Vải rất đẹp",
				ImageUrls:   []string{"https://cdn.example.com/r1.jpg"},
				Status:      productv1.ReviewStatus_REVIEW_STATUS_PUBLISHED,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
		Total:         1,
		RatingAvgX100: 500,
		ReviewCount:   1,
		RatingCounts:  map[int32]int32{5: 1},
	}, nil
}

func (p *w3bProductClient) CreateReview(ctx context.Context, in *productv1.CreateReviewRequest, _ ...grpc.CallOption) (*productv1.CreateReviewResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastCreateReviewReq = in
	p.lastCreateReviewMD, _ = metadata.FromOutgoingContext(ctx)
	if p.createReviewErr != nil {
		return nil, p.createReviewErr
	}
	now := timestamppb.Now()
	return &productv1.CreateReviewResponse{
		Review: &productv1.ProductReview{
			Id:          "rev-new",
			ProductId:   "prod-a",
			ShopId:      "shop-a",
			OrderId:     in.GetOrderId(),
			OrderItemId: in.GetOrderItemId(),
			SkuId:       "sku-a",
			SkuName:     "Size M",
			BuyerId:     "buyer-1",
			BuyerName:   "N. Linh",
			Rating:      in.GetRating(),
			Comment:     in.GetComment(),
			ImageUrls:   in.GetImageUrls(),
			Status:      productv1.ReviewStatus_REVIEW_STATUS_PUBLISHED,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}, nil
}

func (p *w3bProductClient) ListOrderReviews(_ context.Context, in *productv1.ListOrderReviewsRequest, _ ...grpc.CallOption) (*productv1.ListOrderReviewsResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastOrderReviewsReq = in
	now := timestamppb.Now()
	return &productv1.ListOrderReviewsResponse{
		Reviews: []*productv1.ProductReview{
			{
				Id:          "rev-1",
				ProductId:   "prod-a",
				ShopId:      "shop-a",
				OrderId:     in.GetOrderId(),
				OrderItemId: "oi-1",
				SkuId:       "sku-a",
				SkuName:     "Size M",
				BuyerId:     "buyer-1",
				BuyerName:   "N. Linh",
				Rating:      5,
				Comment:     "Đúng mô tả",
				ImageUrls:   []string{},
				Status:      productv1.ReviewStatus_REVIEW_STATUS_PUBLISHED,
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		},
	}, nil
}

func (p *w3bProductClient) ListBannedKeywords(_ context.Context, in *productv1.ListBannedKeywordsRequest, _ ...grpc.CallOption) (*productv1.ListBannedKeywordsResponse, error) {
	p.mu.Lock()
	p.lastListKWReq = in
	p.mu.Unlock()
	now := timestamppb.Now()
	return &productv1.ListBannedKeywordsResponse{
		Keywords: []*productv1.BannedKeyword{
			{
				Id:             "kw-1",
				Term:           "hàng giả",
				NormalizedTerm: "hang gia",
				Category:       "COUNTERFEIT",
				Action:         productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
				IsActive:       true,
				CreatedBy:      "admin-1",
				CreatedAt:      now,
				UpdatedAt:      now,
			},
		},
		Total: 1,
	}, nil
}

func (p *w3bProductClient) CreateBannedKeyword(_ context.Context, in *productv1.CreateBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.CreateBannedKeywordResponse, error) {
	p.mu.Lock()
	p.lastCreateKWReq = in
	p.mu.Unlock()
	return &productv1.CreateBannedKeywordResponse{
		Keyword: &productv1.BannedKeyword{
			Id:             "kw-new",
			Term:           in.GetTerm(),
			NormalizedTerm: strings.ToLower(in.GetTerm()),
			Category:       in.GetCategory(),
			Action:         in.GetAction(),
			IsActive:       true,
			CreatedBy:      "admin-1",
		},
	}, nil
}

func (p *w3bProductClient) UpdateBannedKeyword(_ context.Context, in *productv1.UpdateBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.UpdateBannedKeywordResponse, error) {
	p.mu.Lock()
	p.lastUpdateKWReq = in
	p.mu.Unlock()
	return &productv1.UpdateBannedKeywordResponse{
		Keyword: &productv1.BannedKeyword{
			Id:       in.GetId(),
			Term:     in.GetTerm(),
			Category: in.GetCategory(),
			Action:   in.GetAction(),
			IsActive: in.GetIsActive(),
		},
	}, nil
}

func (p *w3bProductClient) DeleteBannedKeyword(_ context.Context, in *productv1.DeleteBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.DeleteBannedKeywordResponse, error) {
	p.mu.Lock()
	p.lastDeleteKWReq = in
	p.mu.Unlock()
	return &productv1.DeleteBannedKeywordResponse{Ok: true}, nil
}

func (p *w3bProductClient) PreviewBannedKeyword(_ context.Context, in *productv1.PreviewBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.PreviewBannedKeywordResponse, error) {
	p.mu.Lock()
	p.lastPreviewKWReq = in
	p.mu.Unlock()
	return &productv1.PreviewBannedKeywordResponse{
		AffectedCount: 2,
		Samples: []*productv1.BannedContentProductSample{
			{
				ProductId:     "prod-a",
				ShopId:        "shop-a",
				Name:          "Mẫu thử",
				Slug:          "mau-thu",
				Status:        productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				MatchedTerms:  []string{in.GetTerm()},
				HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
			},
		},
	}, nil
}

func (p *w3bProductClient) RescanProductsForBannedContent(_ context.Context, in *productv1.RescanProductsForBannedContentRequest, _ ...grpc.CallOption) (*productv1.RescanProductsForBannedContentResponse, error) {
	p.mu.Lock()
	p.lastRescanReq = in
	override := p.rescanRespOverride
	p.mu.Unlock()
	if override != nil {
		return override, nil
	}
	return &productv1.RescanProductsForBannedContentResponse{
		ScannedCount: 120,
		BlockedCount: 1,
		FlaggedCount: 2,
		Samples: []*productv1.BannedContentProductSample{
			{
				ProductId:     "prod-a",
				ShopId:        "shop-a",
				Name:          "Mẫu thử",
				Slug:          "mau-thu",
				Status:        productv1.ProductStatus_PRODUCT_STATUS_REJECTED,
				MatchedTerms:  []string{"hàng giả"},
				HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
			},
		},
		HasMore:    true,
		NextCursor: "cursor-2",
	}, nil
}

type w3bCartClient struct {
	cartv1.CartServiceClient
}

func (c *w3bCartClient) GetCart(_ context.Context, in *cartv1.GetCartRequest, _ ...grpc.CallOption) (*cartv1.GetCartResponse, error) {
	return &cartv1.GetCartResponse{
		Cart: &cartv1.Cart{
			UserId: in.GetUserId(),
			ShopGroups: []*cartv1.CartShopGroup{
				{
					ShopId:      "shop-a",
					ShopName:    "Shop A",
					SubtotalVnd: 200000,
					Items: []*cartv1.CartItem{
						{
							SkuId:             "sku-ok",
							ProductId:         "prod-a",
							ShopId:            "shop-a",
							ProductName:       "Áo thun",
							PriceVnd:          200000,
							Quantity:          1,
							Selected:          true,
							Unavailable:       false,
							UnavailableReason: "",
						},
						{
							SkuId:             "sku-hidden",
							ProductId:         "prod-old",
							ShopId:            "shop-a",
							ProductName:       "Áo cũ",
							PriceVnd:          0,
							Quantity:          2,
							Selected:          true,
							Unavailable:       true,
							UnavailableReason: "SKU_INACTIVE",
						},
					},
				},
			},
			TotalItems: 1,
			TotalVnd:   200000,
		},
	}, nil
}

type w3bShippingClient struct {
	shippingv1.ShippingServiceClient
	mu               sync.Mutex
	getSettingsCalls int
	lastCalcFeeReq   *shippingv1.CalculateFeeRequest
	lastCalcFeeMD    metadata.MD
}

func (s *w3bShippingClient) ListWards(_ context.Context, in *shippingv1.ListWardsRequest, _ ...grpc.CallOption) (*shippingv1.ListWardsResponse, error) {
	return &shippingv1.ListWardsResponse{
		Wards: []*shippingv1.Ward{
			{Code: "00001", ProvinceCode: in.GetProvinceCode(), Name: "Phường A", CodSupported: true},
			{Code: "20335", ProvinceCode: in.GetProvinceCode(), Name: "Đặc khu Hoàng Sa", CodSupported: false},
		},
	}, nil
}

func (s *w3bShippingClient) GetShopShippingSettings(_ context.Context, in *shippingv1.GetShopShippingSettingsRequest, _ ...grpc.CallOption) (*shippingv1.GetShopShippingSettingsResponse, error) {
	s.mu.Lock()
	s.getSettingsCalls++
	s.mu.Unlock()
	return &shippingv1.GetShopShippingSettingsResponse{
		Settings: &shippingv1.ShopShippingSettings{ShopId: in.GetShopId(), DefaultWeightGrams: 600},
	}, nil
}

func (s *w3bShippingClient) CalculateFee(ctx context.Context, in *shippingv1.CalculateFeeRequest, _ ...grpc.CallOption) (*shippingv1.CalculateFeeResponse, error) {
	s.mu.Lock()
	s.lastCalcFeeReq = in
	s.lastCalcFeeMD, _ = metadata.FromOutgoingContext(ctx)
	s.mu.Unlock()
	return &shippingv1.CalculateFeeResponse{
		IsEstimated: strings.TrimSpace(in.GetToWardCode()) == "",
		Quotes: []*shippingv1.CarrierQuote{
			{
				CarrierCode:   "ghn",
				CarrierName:   "GHN",
				ServiceCode:   "STD",
				ServiceName:   "Chuẩn",
				FeeVnd:        25000,
				EstimatedDays: 2,
				IsFallback:    false,
				CodSupported:  in.GetToWardCode() != "20335",
			},
		},
	}, nil
}

func (s *w3bShippingClient) GetShipment(_ context.Context, in *shippingv1.GetShipmentRequest, _ ...grpc.CallOption) (*shippingv1.GetShipmentResponse, error) {
	return &shippingv1.GetShipmentResponse{
		Shipment: &shippingv1.Shipment{
			ShipmentId: "ship-q1",
			OrderId:    in.GetOrderId(),
			ShopId:     "shop-a",
			Status:     shippingv1.ShipmentStatus_SHIPMENT_STATUS_QUEUED,
			Direction:  shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD,
		},
	}, nil
}

type w3bOrderClient struct {
	orderv1.OrderServiceClient
	mu          sync.Mutex
	checkoutErr error
}

func (o *w3bOrderClient) CreateCheckout(_ context.Context, _ *orderv1.CreateCheckoutRequest, _ ...grpc.CallOption) (*orderv1.CreateCheckoutResponse, error) {
	o.mu.Lock()
	err := o.checkoutErr
	o.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &orderv1.CreateCheckoutResponse{CheckoutGroupId: "grp-1"}, nil
}

func (o *w3bOrderClient) SellerShipOrder(_ context.Context, in *orderv1.SellerShipOrderRequest, _ ...grpc.CallOption) (*orderv1.SellerShipOrderResponse, error) {
	return &orderv1.SellerShipOrderResponse{
		Order: &orderv1.Order{
			Id:                in.GetOrderId(),
			ShopId:            in.GetShopId(),
			Status:            orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
			ShipmentJobStatus: "QUEUED",
		},
		Queued:        true,
		ShipmentJobId: "job-ship-1",
	}, nil
}

func (o *w3bOrderClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	return &orderv1.GetOrderResponse{
		Order: &orderv1.Order{
			Id:                in.GetOrderId(),
			BuyerId:           "buyer-1",
			ShopId:            "shop-a",
			Status:            orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
			ShipmentJobStatus: "FAILED",
		},
	}, nil
}

func (o *w3bOrderClient) ListOrders(_ context.Context, _ *orderv1.ListOrdersRequest, _ ...grpc.CallOption) (*orderv1.ListOrdersResponse, error) {
	return &orderv1.ListOrdersResponse{
		Orders: []*orderv1.Order{
			{
				Id:                "ord-1",
				BuyerId:           "buyer-1",
				ShopId:            "shop-a",
				Status:            orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
				ShipmentJobStatus: "FAILED",
			},
		},
		Total: 1,
	}, nil
}

type w3bUserClient struct {
	userv1.UserServiceClient
	mu                 sync.Mutex
	phoneOTPReqCalls   int
	phoneOTPVerCalls   int
	lastPhoneOTPReq    *userv1.RequestPhoneOTPRequest
	lastPhoneOTPVerReq *userv1.VerifyPhoneOTPRequest
	auditReqs          []*userv1.WriteAuditLogRequest
}

func (u *w3bUserClient) RequestPhoneOTP(_ context.Context, in *userv1.RequestPhoneOTPRequest, _ ...grpc.CallOption) (*userv1.RequestPhoneOTPResponse, error) {
	u.mu.Lock()
	u.phoneOTPReqCalls++
	u.lastPhoneOTPReq = in
	u.mu.Unlock()
	return &userv1.RequestPhoneOTPResponse{
		Sent:              true,
		ExpiresInSeconds:  300,
		RetryAfterSeconds: 60,
		PhoneMasked:       "090****789",
	}, nil
}

func (u *w3bUserClient) VerifyPhoneOTP(_ context.Context, in *userv1.VerifyPhoneOTPRequest, _ ...grpc.CallOption) (*userv1.VerifyPhoneOTPResponse, error) {
	u.mu.Lock()
	u.phoneOTPVerCalls++
	u.lastPhoneOTPVerReq = in
	u.mu.Unlock()
	return &userv1.VerifyPhoneOTPResponse{
		Verified: true,
		User: &userv1.User{
			Id:            "buyer-1",
			FullName:      "Buyer One",
			Phone:         in.GetPhone(),
			PhoneVerified: true,
			Role:          userv1.UserRole_USER_ROLE_BUYER,
			Status:        userv1.UserStatus_USER_STATUS_ACTIVE,
		},
	}, nil
}

func (u *w3bUserClient) WriteAuditLog(_ context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	u.mu.Lock()
	u.auditReqs = append(u.auditReqs, in)
	u.mu.Unlock()
	return &userv1.WriteAuditLogResponse{AuditLog: &userv1.AuditLog{Id: "audit-w3b"}}, nil
}

type w3bPaymentClient struct {
	paymentv1.PaymentServiceClient
	mu                 sync.Mutex
	importCalls        int
	importDelay        time.Duration
	listRunsDelay      time.Duration
	importRespOverride *paymentv1.ImportCodReconciliationResponse
	lastImportReq      *paymentv1.ImportCodReconciliationRequest
	lastImportMD       metadata.MD
	lastImportDeadline time.Time
	lastListRunsReq    *paymentv1.ListReconciliationRunsRequest
	lastListQueueReq   *paymentv1.ListReconciliationQueueRequest
}

func (p *w3bPaymentClient) ImportCodReconciliation(ctx context.Context, in *paymentv1.ImportCodReconciliationRequest, _ ...grpc.CallOption) (*paymentv1.ImportCodReconciliationResponse, error) {
	if _, err := proto.Marshal(in); err != nil {
		return nil, status.Errorf(codes.Internal, "grpc: error while marshaling: %v", err)
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	deadline, _ := ctx.Deadline()
	p.mu.Lock()
	p.importCalls++
	p.lastImportReq = in
	p.lastImportMD = md.Copy()
	p.lastImportDeadline = deadline
	delay := p.importDelay
	override := p.importRespOverride
	p.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if override != nil {
		return override, nil
	}
	now := timestamppb.Now()
	return &paymentv1.ImportCodReconciliationResponse{
		Run: &paymentv1.ReconciliationRunSummary{
			RunId:                  "run-cod-1",
			RunType:                "cod_file",
			Provider:               in.GetCarrierCode(),
			RunDate:                "2026-11-24",
			FileName:               in.GetFileName(),
			TotalRows:              int32(len(in.GetRows())),
			MatchedCount:           1,
			AlreadyReconciledCount: 1,
			NotDeliveredCount:      1,
			TotalMatchedVnd:        250000,
			Status:                 "completed",
			CreatedBy:              "admin-1",
			CreatedAt:              now,
		},
		Items: []*paymentv1.ReconciliationItem{
			{
				PaymentId:         "pay-1",
				LocalAmountVnd:    250000,
				ProviderAmountVnd: 250000,
				Status:            paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_MATCHED,
				TrackingCode:      "GHN-001",
				OrderId:           "ord-1",
				LineNumber:        2,
			},
			{
				PaymentId:         "pay-2",
				LocalAmountVnd:    100000,
				ProviderAmountVnd: 100000,
				Status:            paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_ALREADY_RECONCILED,
				TrackingCode:      "GHN-002",
				OrderId:           "ord-2",
				LineNumber:        3,
			},
			{
				ProviderAmountVnd: 150000,
				Status:            paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_NOT_DELIVERED,
				TrackingCode:      "GHN-003",
				OrderId:           "ord-3",
				LineNumber:        4,
			},
		},
	}, nil
}

func (p *w3bPaymentClient) ListReconciliationRuns(ctx context.Context, in *paymentv1.ListReconciliationRunsRequest, _ ...grpc.CallOption) (*paymentv1.ListReconciliationRunsResponse, error) {
	p.mu.Lock()
	p.lastListRunsReq = in
	delay := p.listRunsDelay
	p.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	return &paymentv1.ListReconciliationRunsResponse{
		Runs: []*paymentv1.ReconciliationRunSummary{
			{
				RunId:        "run-cod-1",
				RunType:      "cod_file",
				Provider:     "ghn",
				FileName:     "cod.csv",
				TotalRows:    3,
				MatchedCount: 1,
				Status:       "completed",
			},
		},
		Total: 1,
	}, nil
}

func (p *w3bPaymentClient) ListReconciliationQueue(_ context.Context, in *paymentv1.ListReconciliationQueueRequest, _ ...grpc.CallOption) (*paymentv1.ListReconciliationQueueResponse, error) {
	p.mu.Lock()
	p.lastListQueueReq = in
	p.mu.Unlock()
	return &paymentv1.ListReconciliationQueueResponse{
		Items: []*paymentv1.ReconciliationQueueItem{
			{
				Id:               "rq-cod-1",
				Provider:         "ghn",
				Reason:           "amount_mismatch",
				ResolutionStatus: "pending",
				SourceType:       "cod_file",
				TrackingCode:     "GHN-999",
			},
		},
		Total: 1,
	}, nil
}

type w3bHarness struct {
	handler  http.Handler
	authMgr  *auth.Manager
	user     *w3bUserClient
	product  *w3bProductClient
	cart     *w3bCartClient
	shipping *w3bShippingClient
	order    *w3bOrderClient
	payment  *w3bPaymentClient
}

func newW3BHarness(t *testing.T) *w3bHarness {
	t.Helper()
	authMgr, err := auth.NewManager("w3b-gateway-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	userMock := &w3bUserClient{}
	prodMock := &w3bProductClient{
		categories: []*productv1.Category{
			{Id: "cat-ok", Name: "Áo", Slug: "ao", CommissionRateBps: 500, IsProhibited: false},
			{Id: "cat-ban", Name: "Hàng cấm", Slug: "hang-cam", CommissionRateBps: 500, IsProhibited: true},
		},
		products: map[string]*productv1.Product{
			"prod-a": {
				Id:              "prod-a",
				ShopId:          "shop-a",
				CategoryId:      "cat-ok",
				Name:            "Áo Thun A",
				Slug:            "ao-thun-a",
				Status:          productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
				RejectionReason: "internal-old-note",
				MinPriceVnd:     200000,
				MaxPriceVnd:     250000,
				RatingAvgX100:   475,
				ReviewCount:     12,
				FlaggedKeywords: []string{"cam kết 100%"},
				Skus: []*productv1.SKU{
					{Id: "sku-a-w", ProductId: "prod-a", ShopId: "shop-a", PriceVnd: 200000, WeightGrams: 300, CommissionRateBps: 500, IsActive: true},
				},
			},
		},
		discontinuedBySlug: map[string]*productv1.GetProductResponse{
			"ao-hidden": {
				Discontinued:           true,
				DiscontinuedCategoryId: "cat-ok",
				DiscontinuedName:       "Áo Đã Ẩn",
				DiscontinuedShopId:     "shop-a",
				// Deliberately attach product to ensure gateway never leaks it on discontinued stub.
				Product: &productv1.Product{
					Id:              "prod-hid",
					ShopId:          "LEAK_SHOP",
					Name:            "LEAK_NAME",
					Description:     "LEAK_DESC",
					FlaggedKeywords: []string{"LEAK_KW"},
					Skus:            []*productv1.SKU{{Id: "sku-leak", PriceVnd: 999999}},
				},
			},
			"ao-rejected": {
				Discontinued:           true,
				DiscontinuedCategoryId: "cat-ok",
				DiscontinuedName:       "",
				DiscontinuedShopId:     "shop-b",
			},
			"ao-empty-shop": {
				Discontinued:           true,
				DiscontinuedCategoryId: "cat-ok",
				DiscontinuedName:       "",
				DiscontinuedShopId:     "",
				Product: &productv1.Product{
					Id:     "prod-empty-shop",
					ShopId: "LEAK_EMPTY_SHOP",
				},
			},
		},
		skus: map[string]*productv1.SKU{
			"sku-a-w": {Id: "sku-a-w", ProductId: "prod-a", ShopId: "shop-a", PriceVnd: 200000, WeightGrams: 300, IsActive: true},
			"sku-a-0": {Id: "sku-a-0", ProductId: "prod-a", ShopId: "shop-a", PriceVnd: 150000, WeightGrams: 0, IsActive: true},
		},
	}
	cartMock := &w3bCartClient{}
	shipMock := &w3bShippingClient{}
	ordMock := &w3bOrderClient{}
	payMock := &w3bPaymentClient{}

	h := New(Dependencies{
		ServiceName: "gateway-w3b-test",
		Clients: &clients.Clients{
			User:     userMock,
			Product:  prodMock,
			Cart:     cartMock,
			Shipping: shipMock,
			Order:    ordMock,
			Payment:  payMock,
			Timeout:  5 * time.Second,
		},
		AuthManager: authMgr,
		Logger:      logger.New("gateway-w3b-test", logger.WithWriter(io.Discard)),
	})
	return &w3bHarness{
		handler:  h,
		authMgr:  authMgr,
		user:     userMock,
		product:  prodMock,
		cart:     cartMock,
		shipping: shipMock,
		order:    ordMock,
		payment:  payMock,
	}
}

func (hw *w3bHarness) do(method, target, token, contentType, body string) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	hw.handler.ServeHTTP(rec, req)
	return rec
}

func TestW3B_A4_ReviewsModerationDiscontinuedAndCart(t *testing.T) {
	hw := newW3BHarness(t)
	buyerTok, _, _, _ := hw.authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := hw.authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	adminTok, _, _, _ := hw.authMgr.SignAccessToken("admin-1", "admin", "")

	// 1. Public product responses expose rating_avg_x100 & review_count and OMIT flagged_keywords & commission_rate_bps.
	for _, path := range []string{"/api/v1/products", "/api/v1/products/prod-a", "/api/v1/products/slug/ao-thun-a"} {
		rec := hw.do(http.MethodGet, path, adminTok, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: want 200, got %d (%s)", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"rating_avg_x100":475`) || !strings.Contains(body, `"review_count":12`) {
			t.Fatalf("GET %s missing rating fields: %s", path, body)
		}
		if strings.Contains(body, "flagged_keywords") || strings.Contains(body, "commission_rate_bps") || strings.Contains(body, "internal-old-note") {
			t.Fatalf("GET %s leaked internal product fields: %s", path, body)
		}
	}

	// Seller & Admin product routes retain flagged_keywords.
	for label, pair := range map[string][2]string{
		"seller": {sellerTok, "/api/v1/seller/products/prod-a"},
		"admin":  {adminTok, "/api/v1/admin/products/prod-a"},
	} {
		rec := hw.do(http.MethodGet, pair[1], pair[0], "", "")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"flagged_keywords":["cam kết 100%"]`) {
			t.Fatalf("%s GET %s must retain flagged_keywords: code=%d body=%s", label, pair[1], rec.Code, rec.Body.String())
		}
	}

	// 2. Categories expose is_prohibited; POST/PUT /admin/categories forward is_prohibited & has_is_prohibited.
	recCats := hw.do(http.MethodGet, "/api/v1/categories", "", "", "")
	if recCats.Code != http.StatusOK || !strings.Contains(recCats.Body.String(), `"is_prohibited":true`) {
		t.Fatalf("GET /categories missing is_prohibited: code=%d body=%s", recCats.Code, recCats.Body.String())
	}
	recCatCreate := hw.do(http.MethodPost, "/api/v1/admin/categories", adminTok, "", `{"name":"Ban","slug":"ban","is_prohibited":true}`)
	if recCatCreate.Code != http.StatusCreated || !hw.product.lastCreateCatReq.GetIsProhibited() {
		t.Fatalf("POST /admin/categories is_prohibited failed: code=%d req=%+v", recCatCreate.Code, hw.product.lastCreateCatReq)
	}
	recCatUpdOmit := hw.do(http.MethodPut, "/api/v1/admin/categories/cat-ban", adminTok, "", `{"name":"Ban2","slug":"ban2"}`)
	if recCatUpdOmit.Code != http.StatusOK || hw.product.lastUpdateCatReq.GetHasIsProhibited() {
		t.Fatalf("PUT /admin/categories omitted is_prohibited must leave HasIsProhibited=false: code=%d req=%+v", recCatUpdOmit.Code, hw.product.lastUpdateCatReq)
	}
	recCatUpdSet := hw.do(http.MethodPut, "/api/v1/admin/categories/cat-ban", adminTok, "", `{"name":"Ban2","slug":"ban2","is_prohibited":true}`)
	if recCatUpdSet.Code != http.StatusOK || !hw.product.lastUpdateCatReq.GetHasIsProhibited() || !hw.product.lastUpdateCatReq.GetIsProhibited() {
		t.Fatalf("PUT /admin/categories explicit is_prohibited failed: code=%d req=%+v", recCatUpdSet.Code, hw.product.lastUpdateCatReq)
	}

	// 3. Discontinued slug stub (E13): HIDDEN has name + shop_id, REJECTED omits name + has shop_id, neither leaks skus/description/flagged_keywords.
	recHid := hw.do(http.MethodGet, "/api/v1/products/slug/ao-hidden", "", "", "")
	if recHid.Code != http.StatusOK {
		t.Fatalf("GET /products/slug/ao-hidden: want 200, got %d (%s)", recHid.Code, recHid.Body.String())
	}
	var hidMap map[string]any
	if err := json.Unmarshal(recHid.Body.Bytes(), &hidMap); err != nil {
		t.Fatalf("decode discontinued hidden: %v", err)
	}
	if hidMap["discontinued"] != true || hidMap["category_id"] != "cat-ok" || hidMap["shop_id"] != "shop-a" || hidMap["name"] != "Áo Đã Ẩn" {
		t.Fatalf("unexpected discontinued hidden payload: %+v", hidMap)
	}
	if _, hasProd := hidMap["product"]; hasProd || strings.Contains(recHid.Body.String(), "LEAK_") {
		t.Fatalf("discontinued hidden leaked product/skus: %s", recHid.Body.String())
	}

	recRej := hw.do(http.MethodGet, "/api/v1/products/slug/ao-rejected", "", "", "")
	if recRej.Code != http.StatusOK {
		t.Fatalf("GET /products/slug/ao-rejected: want 200, got %d (%s)", recRej.Code, recRej.Body.String())
	}
	var rejMap map[string]any
	_ = json.Unmarshal(recRej.Body.Bytes(), &rejMap)
	if rejMap["discontinued"] != true || rejMap["category_id"] != "cat-ok" || rejMap["shop_id"] != "shop-b" {
		t.Fatalf("unexpected discontinued rejected payload: %+v", rejMap)
	}
	if _, hasName := rejMap["name"]; hasName {
		t.Fatalf("discontinued rejected MUST omit name: %s", recRej.Body.String())
	}

	// Empty discontinued_shop_id must omit shop_id and never fall back to Product.ShopId.
	recEmptyShop := hw.do(http.MethodGet, "/api/v1/products/slug/ao-empty-shop", "", "", "")
	if recEmptyShop.Code != http.StatusOK {
		t.Fatalf("GET /products/slug/ao-empty-shop: want 200, got %d (%s)", recEmptyShop.Code, recEmptyShop.Body.String())
	}
	var emptyShopMap map[string]any
	if err := json.Unmarshal(recEmptyShop.Body.Bytes(), &emptyShopMap); err != nil {
		t.Fatalf("decode discontinued empty shop: %v", err)
	}
	if _, hasShop := emptyShopMap["shop_id"]; hasShop || strings.Contains(recEmptyShop.Body.String(), "LEAK_") {
		t.Fatalf("discontinued stub with empty discontinued_shop_id must omit shop_id and not leak product: %s", recEmptyShop.Body.String())
	}

	// GET /products/{id} must NOT request a discontinued stub and returns 404 for unknown/discontinued IDs.
	recByID := hw.do(http.MethodGet, "/api/v1/products/prod-hid", "", "", "")
	if recByID.Code != http.StatusNotFound {
		t.Fatalf("GET /products/prod-hid: want 404, got %d (%s)", recByID.Code, recByID.Body.String())
	}
	if hw.product.lastGetProductReq.GetIncludeDiscontinuedStub() {
		t.Fatalf("GET /products/{id} must not set IncludeDiscontinuedStub=true")
	}

	// Unknown slug still returns 404.
	if rec404 := hw.do(http.MethodGet, "/api/v1/products/slug/never-existed", "", "", ""); rec404.Code != http.StatusNotFound {
		t.Fatalf("unknown slug want 404, got %d", rec404.Code)
	}

	// 4. Reviews: public GET /products/{id}/reviews strips buyer_id/order_id/order_item_id/sku_id (E6-bis), strips auth header.
	recRevPub := hw.do(http.MethodGet, "/api/v1/products/prod-a/reviews?rating=5&has_images=true", adminTok, "", "")
	if recRevPub.Code != http.StatusOK {
		t.Fatalf("GET /products/prod-a/reviews: want 200, got %d (%s)", recRevPub.Code, recRevPub.Body.String())
	}
	if vals := hw.product.lastListReviewsMD.Get(auth.MDAuthorization); len(vals) != 0 {
		t.Fatalf("public GET /products/{id}/reviews must strip Authorization metadata, got %v", vals)
	}
	if hw.product.lastListReviewsReq.GetRatingFilter() != 5 || !hw.product.lastListReviewsReq.GetHasImages() {
		t.Fatalf("review filters not forwarded: %+v", hw.product.lastListReviewsReq)
	}
	pubRevBody := recRevPub.Body.String()
	for _, forbidden := range []string{"buyer_id", "order_id", "order_item_id", "sku_id", "buyer-secret-1", "ord-secret-1", "oi-secret-1", "sku-secret-1"} {
		if strings.Contains(pubRevBody, forbidden) {
			t.Fatalf("public GET /products/{id}/reviews leaked %q: %s", forbidden, pubRevBody)
		}
	}
	if !strings.Contains(pubRevBody, `"rating_avg_x100":500`) || !strings.Contains(pubRevBody, `"N. Linh"`) || !strings.Contains(pubRevBody, `"1":0`) || !strings.Contains(pubRevBody, `"5":1`) {
		t.Fatalf("public review response missing expected fields: %s", pubRevBody)
	}
	// Invalid rating query param -> 400.
	if recBadRating := hw.do(http.MethodGet, "/api/v1/products/prod-a/reviews?rating=9", "", "", ""); recBadRating.Code != http.StatusBadRequest {
		t.Fatalf("invalid rating filter want 400, got %d", recBadRating.Code)
	}

	// Buyer order reviews: requires auth; POST creates 201; E7 image validation rejects javascript: & >5 URLs; E8 BANNED_CONTENT -> 400 BANNED_CONTENT.
	if recAnon := hw.do(http.MethodPost, "/api/v1/orders/ord-1/reviews", "", "", `{"order_item_id":"oi-1","rating":5}`); recAnon.Code != http.StatusUnauthorized {
		t.Fatalf("anon POST /orders/{id}/reviews want 401, got %d", recAnon.Code)
	}
	if recAnon := hw.do(http.MethodGet, "/api/v1/orders/ord-1/reviews", "", "", ""); recAnon.Code != http.StatusUnauthorized {
		t.Fatalf("anon GET /orders/{id}/reviews want 401, got %d", recAnon.Code)
	}
	if recXSS := hw.do(http.MethodPost, "/api/v1/orders/ord-1/reviews", buyerTok, "", `{"order_item_id":"oi-1","rating":5,"image_urls":["javascript:alert(1)"]}`); recXSS.Code != http.StatusBadRequest {
		t.Fatalf("javascript: image_url want 400, got %d (%s)", recXSS.Code, recXSS.Body.String())
	}
	if recTooMany := hw.do(http.MethodPost, "/api/v1/orders/ord-1/reviews", buyerTok, "", `{"order_item_id":"oi-1","rating":5,"image_urls":["https://a/1","https://a/2","https://a/3","https://a/4","https://a/5","https://a/6"]}`); recTooMany.Code != http.StatusBadRequest {
		t.Fatalf(">5 image_urls want 400, got %d", recTooMany.Code)
	}
	recCreateRev := hw.do(http.MethodPost, "/api/v1/orders/ord-1/reviews", buyerTok, "", `{"order_item_id":"oi-1","rating":5,"comment":"Tốt","image_urls":["https://cdn.example.com/1.jpg","/products/2.jpg"]}`)
	if recCreateRev.Code != http.StatusCreated {
		t.Fatalf("POST /orders/ord-1/reviews want 201, got %d (%s)", recCreateRev.Code, recCreateRev.Body.String())
	}
	if !strings.Contains(recCreateRev.Body.String(), `"order_item_id":"oi-1"`) {
		t.Fatalf("POST review missing order_item_id: %s", recCreateRev.Body.String())
	}
	// BANNED_CONTENT error code propagated as 400 BANNED_CONTENT.
	hw.product.mu.Lock()
	hw.product.createReviewErr = status.Error(codes.InvalidArgument, "BANNED_CONTENT: matched hang gia")
	hw.product.mu.Unlock()
	recBanRev := hw.do(http.MethodPost, "/api/v1/orders/ord-1/reviews", buyerTok, "", `{"order_item_id":"oi-1","rating":5,"comment":"hang gia"}`)
	hw.product.mu.Lock()
	hw.product.createReviewErr = nil
	hw.product.mu.Unlock()
	if recBanRev.Code != http.StatusBadRequest || !strings.Contains(recBanRev.Body.String(), `"code":"BANNED_CONTENT"`) {
		t.Fatalf("BANNED_CONTENT review want 400 BANNED_CONTENT, got %d (%s)", recBanRev.Code, recBanRev.Body.String())
	}

	// GET /orders/{id}/reviews returns full fields including order_item_id & sku_id (E6-bis).
	recOrdRevs := hw.do(http.MethodGet, "/api/v1/orders/ord-1/reviews", buyerTok, "", "")
	if recOrdRevs.Code != http.StatusOK || !strings.Contains(recOrdRevs.Body.String(), `"order_item_id":"oi-1"`) || !strings.Contains(recOrdRevs.Body.String(), `"sku_id":"sku-a"`) {
		t.Fatalf("GET /orders/ord-1/reviews mismatch: code=%d body=%s", recOrdRevs.Code, recOrdRevs.Body.String())
	}

	// 5. Admin banned keywords CRUD + preview + rescan RBAC (401 anon, 403 buyer/seller, 200/201 admin).
	kwRoutes := []struct {
		method, path, body string
		wantAdminStatus    int
	}{
		{http.MethodGet, "/api/v1/admin/banned-keywords?action=BLOCK&include_inactive=true", "", http.StatusOK},
		{http.MethodPost, "/api/v1/admin/banned-keywords", `{"term":"hang gia","category":"COUNTERFEIT","action":"BLOCK"}`, http.StatusCreated},
		{http.MethodPut, "/api/v1/admin/banned-keywords/kw-1", `{"term":"hang gia","action":"REVIEW","is_active":false}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/admin/banned-keywords/kw-1", "", http.StatusOK},
		{http.MethodPost, "/api/v1/admin/banned-keywords/preview", `{"term":"hang gia"}`, http.StatusOK},
		{http.MethodPost, "/api/v1/admin/banned-keywords/rescan", `{"dry_run":true,"limit":999}`, http.StatusOK},
	}
	for _, rt := range kwRoutes {
		if rec := hw.do(rt.method, rt.path, "", "", rt.body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s want 401, got %d", rt.method, rt.path, rec.Code)
		}
		for _, tok := range []string{buyerTok, sellerTok} {
			if rec := hw.do(rt.method, rt.path, tok, "", rt.body); rec.Code != http.StatusForbidden {
				t.Fatalf("non-admin %s %s want 403, got %d", rt.method, rt.path, rec.Code)
			}
		}
		rec := hw.do(rt.method, rt.path, adminTok, "", rt.body)
		if rec.Code != rt.wantAdminStatus {
			t.Fatalf("admin %s %s want %d, got %d (%s)", rt.method, rt.path, rt.wantAdminStatus, rec.Code, rec.Body.String())
		}
	}
	// Verify UpdateBannedKeyword presence flag (explicit is_active=false set above, omitted preserves HasIsActive=false).
	if !hw.product.lastUpdateKWReq.GetHasIsActive() || hw.product.lastUpdateKWReq.GetIsActive() {
		t.Fatalf("PUT banned-keyword explicit is_active=false mismatch: %+v", hw.product.lastUpdateKWReq)
	}
	if rec := hw.do(http.MethodPut, "/api/v1/admin/banned-keywords/kw-1", adminTok, "", `{"term":"hang gia"}`); rec.Code != http.StatusOK || hw.product.lastUpdateKWReq.GetHasIsActive() {
		t.Fatalf("PUT banned-keyword omitted is_active must leave HasIsActive=false: code=%d req=%+v", rec.Code, hw.product.lastUpdateKWReq)
	}
	// Verify rescan caps limit at 500 per E11.
	if hw.product.lastRescanReq.GetLimit() != 500 {
		t.Fatalf("rescan limit >500 must be capped at 500, got %d", hw.product.lastRescanReq.GetLimit())
	}

	// 6. Cart unavailable fields (E3).
	recCart := hw.do(http.MethodGet, "/api/v1/cart", buyerTok, "", "")
	if recCart.Code != http.StatusOK {
		t.Fatalf("GET /cart want 200, got %d (%s)", recCart.Code, recCart.Body.String())
	}
	if !strings.Contains(recCart.Body.String(), `"unavailable":true`) || !strings.Contains(recCart.Body.String(), `"unavailable_reason":"SKU_INACTIVE"`) {
		t.Fatalf("GET /cart missing unavailable fields: %s", recCart.Body.String())
	}
}

func TestW3B_A5_ShippingOrderPhoneOTPAndCODImport(t *testing.T) {
	hw := newW3BHarness(t)
	buyerTok, _, _, _ := hw.authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := hw.authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	adminTok, _, _, _ := hw.authMgr.SignAccessToken("admin-1", "admin", "")

	// 1. Wards expose cod_supported (E16).
	recWards := hw.do(http.MethodGet, "/api/v1/shipping/provinces/48/wards", "", "", "")
	if recWards.Code != http.StatusOK || !strings.Contains(recWards.Body.String(), `"cod_supported":false`) || !strings.Contains(recWards.Body.String(), `"cod_supported":true`) {
		t.Fatalf("GET /shipping/provinces/48/wards missing cod_supported: code=%d body=%s", recWards.Code, recWards.Body.String())
	}

	// 2. Public POST /shipping/calculate-fee works anonymously WITHOUT calling GetShopShippingSettings,
	// uses fallback_weight_item_qty for 0-weight SKUs, supports empty to_ward_code (is_estimated=true) & returns cod_supported (E17).
	recAnonFee := hw.do(http.MethodPost, "/api/v1/shipping/calculate-fee", "", "", `{
		"shop_id": "shop-a",
		"to_province_code": "79",
		"payment_method": "COD",
		"items": [
			{"sku_id": "sku-a-w", "quantity": 2},
			{"sku_id": "sku-a-0", "quantity": 3}
		]
	}`)
	if recAnonFee.Code != http.StatusOK {
		t.Fatalf("anon POST /shipping/calculate-fee want 200, got %d (%s)", recAnonFee.Code, recAnonFee.Body.String())
	}
	if hw.shipping.getSettingsCalls != 0 {
		t.Fatalf("anonymous calculate-fee must NOT call GetShopShippingSettings, got %d calls", hw.shipping.getSettingsCalls)
	}
	reqFee := hw.shipping.lastCalcFeeReq
	if reqFee.GetWeightGrams() != 600 || reqFee.GetFallbackWeightItemQty() != 3 || reqFee.GetCodAmountVnd() != 850000 {
		t.Fatalf("anonymous calculate-fee req mismatch: weight=%d fallback_qty=%d cod=%d",
			reqFee.GetWeightGrams(), reqFee.GetFallbackWeightItemQty(), reqFee.GetCodAmountVnd())
	}
	if !strings.Contains(recAnonFee.Body.String(), `"is_estimated":true`) || !strings.Contains(recAnonFee.Body.String(), `"cod_supported":true`) {
		t.Fatalf("calculate-fee response missing is_estimated/cod_supported: %s", recAnonFee.Body.String())
	}

	// Authenticated calculate-fee still works and loads shop settings.
	recAuthFee := hw.do(http.MethodPost, "/api/v1/shipping/calculate-fee", buyerTok, "", `{
		"shop_id": "shop-a",
		"to_province_code": "48",
		"to_ward_code": "20335",
		"items": [{"sku_id": "sku-a-0", "quantity": 2}]
	}`)
	if recAuthFee.Code != http.StatusOK || hw.shipping.getSettingsCalls != 1 || hw.shipping.lastCalcFeeReq.GetWeightGrams() != 1200 {
		t.Fatalf("auth calculate-fee mismatch: code=%d settingsCalls=%d req=%+v", recAuthFee.Code, hw.shipping.getSettingsCalls, hw.shipping.lastCalcFeeReq)
	}
	if !strings.Contains(recAuthFee.Body.String(), `"is_estimated":false`) || !strings.Contains(recAuthFee.Body.String(), `"cod_supported":false`) {
		t.Fatalf("auth calculate-fee ward 20335 response mismatch: %s", recAuthFee.Body.String())
	}

	// 3. Order shipment_job_status & SellerShipOrder queued/shipment_job_id + Shipment QUEUED status (E18).
	recShip := hw.do(http.MethodPost, "/api/v1/seller/orders/ord-1/ship", sellerTok, "", `{"carrier_code":"ghn"}`)
	if recShip.Code != http.StatusOK || !strings.Contains(recShip.Body.String(), `"queued":true`) || !strings.Contains(recShip.Body.String(), `"shipment_job_id":"job-ship-1"`) || !strings.Contains(recShip.Body.String(), `"shipment_job_status":"QUEUED"`) {
		t.Fatalf("POST /seller/orders/ord-1/ship queued fields missing: code=%d body=%s", recShip.Code, recShip.Body.String())
	}
	recOrdBuyer := hw.do(http.MethodGet, "/api/v1/orders/ord-1", buyerTok, "", "")
	if recOrdBuyer.Code != http.StatusOK || strings.Contains(recOrdBuyer.Body.String(), "shipment_job_status") {
		t.Fatalf("GET /orders/ord-1 as buyer must omit shipment_job_status: code=%d body=%s", recOrdBuyer.Code, recOrdBuyer.Body.String())
	}
	recOrdSeller := hw.do(http.MethodGet, "/api/v1/orders/ord-1", sellerTok, "", "")
	if recOrdSeller.Code != http.StatusOK || !strings.Contains(recOrdSeller.Body.String(), `"shipment_job_status":"FAILED"`) {
		t.Fatalf("GET /orders/ord-1 as seller owner shipment_job_status missing: code=%d body=%s", recOrdSeller.Code, recOrdSeller.Body.String())
	}
	recShipment := hw.do(http.MethodGet, "/api/v1/shipping/shipments/ord-1", buyerTok, "", "")
	if recShipment.Code != http.StatusOK || !strings.Contains(recShipment.Body.String(), `"status":"QUEUED"`) {
		t.Fatalf("GET /shipping/shipments/ord-1 QUEUED status missing: code=%d body=%s", recShipment.Code, recShipment.Body.String())
	}

	// 4. Phone OTP routes (E19): require auth, support optional body on /otp, forward phone+otp on /verify.
	if rec := hw.do(http.MethodPost, "/api/v1/users/me/phone/otp", "", "", `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon POST /users/me/phone/otp want 401, got %d", rec.Code)
	}
	if rec := hw.do(http.MethodPost, "/api/v1/users/me/phone/verify", "", "", `{"otp":"123456"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon POST /users/me/phone/verify want 401, got %d", rec.Code)
	}
	recOTP := hw.do(http.MethodPost, "/api/v1/users/me/phone/otp", buyerTok, "", "")
	if recOTP.Code != http.StatusOK || !strings.Contains(recOTP.Body.String(), `"phone_masked":"090****789"`) {
		t.Fatalf("POST /users/me/phone/otp empty body want 200, got %d (%s)", recOTP.Code, recOTP.Body.String())
	}
	recVer := hw.do(http.MethodPost, "/api/v1/users/me/phone/verify", buyerTok, "", `{"phone":"0901234789","otp":"123456"}`)
	if recVer.Code != http.StatusOK || !strings.Contains(recVer.Body.String(), `"verified":true`) || !strings.Contains(recVer.Body.String(), `"phone_verified":true`) {
		t.Fatalf("POST /users/me/phone/verify want 200, got %d (%s)", recVer.Code, recVer.Body.String())
	}

	// 5. COD Reconciliation CSV Import & Runs & Queue (E20 / E20-bis):
	// RBAC: anon 401, buyer/seller 403.
	validCSV := "\xEF\xBB\xBFtracking_code,cod_amount_vnd,delivered_at,order_code\nGHN-001,250000,2026-11-24T10:00:00Z,NM-001\nGHN-002,100000,2026-11-24,\n"
	for _, rt := range []struct{ method, path, ct, body string }{
		{http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", "text/csv", validCSV},
		{http.MethodGet, "/api/v1/admin/reconciliation/runs?run_type=cod_file", "", ""},
	} {
		if rec := hw.do(rt.method, rt.path, "", rt.ct, rt.body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s want 401, got %d", rt.method, rt.path, rec.Code)
		}
		for _, tok := range []string{buyerTok, sellerTok} {
			if rec := hw.do(rt.method, rt.path, tok, rt.ct, rt.body); rec.Code != http.StatusForbidden {
				t.Fatalf("non-admin %s %s want 403, got %d", rt.method, rt.path, rec.Code)
			}
		}
	}

	// Valid text/csv with UTF-8 BOM -> 200, exact SHA256, rows parsed with line numbers 2 & 3.
	recImp := hw.do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn&file_name=ghn-nov.csv", adminTok, "text/csv", validCSV)
	if recImp.Code != http.StatusOK {
		t.Fatalf("POST cod/import text/csv want 200, got %d (%s)", recImp.Code, recImp.Body.String())
	}
	wantSum := sha256.Sum256([]byte(validCSV))
	gotImpReq := hw.payment.lastImportReq
	if gotImpReq == nil || gotImpReq.GetCarrierCode() != "ghn" || gotImpReq.GetFileName() != "ghn-nov.csv" ||
		gotImpReq.GetFileSha256() != hex.EncodeToString(wantSum[:]) || len(gotImpReq.GetRows()) != 2 {
		t.Fatalf("ImportCodReconciliationRequest mismatch: %+v", gotImpReq)
	}
	if gotImpReq.GetRows()[0].GetLineNumber() != 2 || gotImpReq.GetRows()[0].GetCodAmountVnd() != 250000 || gotImpReq.GetRows()[0].GetOrderCode() != "NM-001" {
		t.Fatalf("row[0] mismatch: %+v", gotImpReq.GetRows()[0])
	}
	if !strings.Contains(recImp.Body.String(), `"ALREADY_RECONCILED"`) || !strings.Contains(recImp.Body.String(), `"NOT_DELIVERED"`) || !strings.Contains(recImp.Body.String(), `"not_delivered_count":1`) {
		t.Fatalf("cod/import response missing new statuses/counts: %s", recImp.Body.String())
	}

	// Valid multipart/form-data upload -> 200.
	var mpBuf bytes.Buffer
	mpw := multipart.NewWriter(&mpBuf)
	_ = mpw.WriteField("carrier_code", "mock_express")
	_ = mpw.WriteField("run_date", "2026-11-24")
	fw, _ := mpw.CreateFormFile("file", "carrier_export.csv")
	_, _ = fw.Write([]byte("tracking_code,cod_amount_vnd,delivered_at\nMOCK-1,500000,2026-11-24\n"))
	_ = mpw.Close()
	recMP := hw.do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import", adminTok, mpw.FormDataContentType(), mpBuf.String())
	if recMP.Code != http.StatusOK || hw.payment.lastImportReq.GetCarrierCode() != "mock_express" || hw.payment.lastImportReq.GetFileName() != "carrier_export.csv" {
		t.Fatalf("multipart cod/import mismatch: code=%d req=%+v body=%s", recMP.Code, hw.payment.lastImportReq, recMP.Body.String())
	}

	// Reject invalid CSVs (non-digit amount, formula injection, >5000 rows, invalid UTF-8, >2MiB).
	callsBefore := hw.payment.importCalls
	badCSVs := map[string]string{
		"negative_amount":   "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,-1000,2026-11-24\n",
		"decimal_amount":    "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,1000.50,2026-11-24\n",
		"comma_amount":      "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,\"100,000\",2026-11-24\n",
		"plus_amount":       "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,+1000,2026-11-24\n",
		"sci_amount":        "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,1e5,2026-11-24\n",
		"formula_tracking":  "tracking_code,cod_amount_vnd,delivered_at\n=CMD|' /C calc'!A0,100000,2026-11-24\n",
		"formula_ordercode": "tracking_code,cod_amount_vnd,delivered_at,order_code\nTRK-1,100000,2026-11-24,  @SUM(A1:A2)\n",
		"formula_delivered": "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,100000,+2026-11-24\n",
		"missing_header":    "tracking_code,cod_amount_vnd\nTRK-1,100000\n",
		"empty_rows":        "tracking_code,cod_amount_vnd,delivered_at\n",
		"invalid_utf8":      "tracking_code,cod_amount_vnd,delivered_at\nTRK-\xff,100000,2026-11-24\n",
	}
	for name, payload := range badCSVs {
		rec := hw.do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", adminTok, "text/csv", payload)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad CSV %s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// > 5000 rows -> 400.
	var sb strings.Builder
	sb.WriteString("tracking_code,cod_amount_vnd,delivered_at\n")
	for i := 0; i < 5001; i++ {
		fmt.Fprintf(&sb, "TRK-%05d,10000,2026-11-24\n", i)
	}
	if rec5001 := hw.do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", adminTok, "text/csv", sb.String()); rec5001.Code != http.StatusBadRequest {
		t.Fatalf("5001 rows CSV: want 400, got %d (%s)", rec5001.Code, rec5001.Body.String())
	}

	// > 2 MiB -> 413 PAYLOAD_TOO_LARGE.
	hugeCSV := "tracking_code,cod_amount_vnd,delivered_at\n" + strings.Repeat("TRK-1234567890,100000,2026-11-24\n", (httpx.MaxBodyBytes/33)+10)
	if recHuge := hw.do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", adminTok, "text/csv", hugeCSV); recHuge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(">2MiB CSV: want 413, got %d (%s)", recHuge.Code, recHuge.Body.String())
	}
	if hw.payment.importCalls != callsBefore {
		t.Fatalf("invalid COD CSVs must not call PaymentService, got %d extra calls", hw.payment.importCalls-callsBefore)
	}

	// GET /admin/reconciliation/runs & queue expose source_type/tracking_code.
	recRuns := hw.do(http.MethodGet, "/api/v1/admin/reconciliation/runs?run_type=cod_file", adminTok, "", "")
	if recRuns.Code != http.StatusOK || !strings.Contains(recRuns.Body.String(), `"run_id":"run-cod-1"`) {
		t.Fatalf("GET /admin/reconciliation/runs failed: code=%d body=%s", recRuns.Code, recRuns.Body.String())
	}
	recQueue := hw.do(http.MethodGet, "/api/v1/admin/reconciliation/queue", adminTok, "", "")
	if recQueue.Code != http.StatusOK || !strings.Contains(recQueue.Body.String(), `"source_type":"cod_file"`) || !strings.Contains(recQueue.Body.String(), `"tracking_code":"GHN-999"`) {
		t.Fatalf("GET /admin/reconciliation/queue missing source_type/tracking_code: code=%d body=%s", recQueue.Code, recQueue.Body.String())
	}

	// Extra edge cases: exact 5000 rows allowed; invalid Content-Type, run_date, file_name formula, carrier formula, run_type rejected.
	var sb5000 strings.Builder
	sb5000.WriteString("tracking_code,cod_amount_vnd,delivered_at\n")
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&sb5000, "T%05d,1000,2026-11-24\n", i)
	}
	if rec5000 := hw.do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", adminTok, "text/csv", sb5000.String()); rec5000.Code != http.StatusOK || len(hw.payment.lastImportReq.GetRows()) != 5000 {
		t.Fatalf("exact 5000 rows CSV: want 200 & 5000 rows, got %d (rows=%d, body=%s)", rec5000.Code, len(hw.payment.lastImportReq.GetRows()), rec5000.Body.String())
	}
	for label, tc := range map[string]struct{ method, path, ct, body string }{
		"json_content_type":  {http.MethodPost, "/api/v1/admin/reconciliation/cod/import", "application/json", `{"rows":[]}`},
		"bad_run_date":       {http.MethodPost, "/api/v1/admin/reconciliation/cod/import?run_date=2026-99-99", "text/csv", validCSV},
		"formula_file_name":  {http.MethodPost, "/api/v1/admin/reconciliation/cod/import?file_name=%3Devil.csv", "text/csv", validCSV},
		"formula_carrier":    {http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=%2Bghn", "text/csv", validCSV},
		"tab_formula_cell":   {http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", "text/csv", "tracking_code,cod_amount_vnd,delivered_at\n\tTRK-1,100000,2026-11-24\n"},
		"invalid_run_type":   {http.MethodGet, "/api/v1/admin/reconciliation/runs?run_type=crypto", "", ""},
		"invalid_kw_action":  {http.MethodGet, "/api/v1/admin/banned-keywords?action=DROP", "", ""},
		"negative_rescan":    {http.MethodPost, "/api/v1/admin/banned-keywords/rescan", "application/json", `{"limit":-1}`},
		"missing_verify_otp": {http.MethodPost, "/api/v1/users/me/phone/verify", "application/json", `{"phone":"0901234567","otp":"   "}`},
	} {
		rec := hw.do(tc.method, tc.path, adminTok, tc.ct, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("edge case %s (%s %s): want 400, got %d (%s)", label, tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}

	// Verify super_admin RBAC on all new admin routes + AdminAudit recorded banned_keyword and reconciliation_run.
	superAdminTok, _, _, _ := hw.authMgr.SignAccessToken("super-1", "super_admin", "")
	if rec := hw.do(http.MethodPost, "/api/v1/admin/banned-keywords", superAdminTok, "", `{"term":"lừa đảo","category":"FRAUD","action":"BLOCK"}`); rec.Code != http.StatusCreated {
		t.Fatalf("super_admin POST /admin/banned-keywords want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := hw.do(http.MethodGet, "/api/v1/admin/reconciliation/runs", superAdminTok, "", ""); rec.Code != http.StatusOK {
		t.Fatalf("super_admin GET /admin/reconciliation/runs want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	hw.user.mu.Lock()
	auditReqs := append([]*userv1.WriteAuditLogRequest(nil), hw.user.auditReqs...)
	hw.user.mu.Unlock()
	foundKWAudit, foundCodAudit := false, false
	for _, a := range auditReqs {
		if a.GetAction() == "POST /api/v1/admin/banned-keywords" && a.GetTargetType() == "banned_keyword" && a.GetTargetId() == "kw-new" {
			foundKWAudit = true
		}
		if a.GetAction() == "POST /api/v1/admin/reconciliation/cod/import" && a.GetTargetType() == "reconciliation_run" && a.GetTargetId() == "run-cod-1" {
			foundCodAudit = true
		}
	}
	if !foundKWAudit || !foundCodAudit {
		t.Fatalf("expected AdminAudit for banned_keyword (%v) and reconciliation_run (%v), got %+v", foundKWAudit, foundCodAudit, auditReqs)
	}

	// Checkout canonical COD & cart unavailable error codes pass through with exact error.code (E4, E19).
	for _, code := range []string{"COD_PHONE_NOT_VERIFIED", "COD_AREA_NOT_SUPPORTED", "COD_REJECTED_LIMIT_EXCEEDED", "SKU_INACTIVE"} {
		hw.order.mu.Lock()
		hw.order.checkoutErr = status.Error(codes.FailedPrecondition, code+": blocked")
		hw.order.mu.Unlock()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", strings.NewReader(`{"address_id":"a1","from_cart":true,"payment_method":"COD","sales_channel":"MARKETPLACE"}`))
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		req.Header.Set("Idempotency-Key", "idem-"+code)
		rec := httptest.NewRecorder()
		hw.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"`+code+`"`) {
			t.Fatalf("checkout %s: want 409 with code=%s, got %d (%s)", code, code, rec.Code, rec.Body.String())
		}
	}
	hw.order.mu.Lock()
	hw.order.checkoutErr = nil
	hw.order.mu.Unlock()
}

func TestW3B_RouterRateLimits_PhoneOTPAndShippingFeeAndAuthIsolation(t *testing.T) {
	authMgr, err := auth.NewManager("w3b-rl-router-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	buyer1Tok, _, _, _ := authMgr.SignAccessToken("buyer-rl-1", "buyer", "")
	buyer2Tok, _, _, _ := authMgr.SignAccessToken("buyer-rl-2", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-rl-1", "seller", "shop-a")

	userMock := &w3bUserClient{}
	prodMock := &w3bProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a-w": {Id: "sku-a-w", ProductId: "prod-a", ShopId: "shop-a", PriceVnd: 100000, WeightGrams: 200, IsActive: true},
		},
	}
	shipMock := &w3bShippingClient{}
	rl := &w3aMemRateLimiter{}
	revoker := &w2aRevokerStub{
		revokedJTI:    map[string]bool{},
		revokedBefore: map[string]int64{},
	}

	h := New(Dependencies{
		ServiceName: "gateway-w3b-rl",
		Clients: &clients.Clients{
			User:     userMock,
			Product:  prodMock,
			Shipping: shipMock,
			Timeout:  5 * time.Second,
		},
		AuthManager:                authMgr,
		RevocationChecker:          revoker,
		RateLimiter:                rl,
		RateLimitIPPerMin:          300,
		RateLimitUserPerMin:        180,
		RateLimitAuthPerMin:        2,
		RateLimitBankOTPPerMin:     2,
		RateLimitPhoneOTPPerMin:    3,
		RateLimitShippingFeePerMin: 2,
		Logger:                     logger.New("gw-w3b-rl", logger.WithWriter(io.Discard)),
	})

	do := func(method, path, ip, tok, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = ip + ":44321"
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. Phone OTP rate limit: 3 calls across /otp + /verify succeed, 4th -> 429 RATE_LIMITED without calling UserService.
	for i := 1; i <= 3; i++ {
		p := "/api/v1/users/me/phone/otp"
		b := `{}`
		if i == 3 {
			p = "/api/v1/users/me/phone/verify"
			b = `{"phone":"0901234567","otp":"123456"}`
		}
		rec := do(http.MethodPost, p, "198.51.100.20", buyer1Tok, b)
		if rec.Code != http.StatusOK {
			t.Fatalf("phone otp call %d: want 200, got %d (%s)", i, rec.Code, rec.Body.String())
		}
	}
	userMock.mu.Lock()
	reqBefore, verBefore := userMock.phoneOTPReqCalls, userMock.phoneOTPVerCalls
	userMock.mu.Unlock()

	rec429 := do(http.MethodPost, "/api/v1/users/me/phone/otp", "198.51.100.20", buyer1Tok, `{}`)
	if rec429.Code != http.StatusTooManyRequests || rec429.Header().Get("Retry-After") == "" {
		t.Fatalf("4th phone otp call want 429 + Retry-After, got %d (%s)", rec429.Code, rec429.Body.String())
	}
	userMock.mu.Lock()
	if userMock.phoneOTPReqCalls != reqBefore || userMock.phoneOTPVerCalls != verBefore {
		t.Fatalf("429 phone otp must short-circuit before UserService")
	}
	userMock.mu.Unlock()

	// Buyer 2 unaffected by Buyer 1's exhausted phone OTP bucket; seller's bank-OTP bucket unaffected too.
	if rec := do(http.MethodPost, "/api/v1/users/me/phone/otp", "198.51.100.20", buyer2Tok, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("buyer2 phone otp must stay 200, got %d", rec.Code)
	}
	// Auth tight bucket (forgot-password) stays separated from phone OTP and shipping fee.
	if rec := do(http.MethodPost, "/api/v1/users/me/phone/otp", "198.51.100.20", sellerTok, `{}`); rec.Code != http.StatusOK {
		t.Fatalf("seller phone otp must stay 200, got %d", rec.Code)
	}

	// 2. Shipping calculate-fee E17-bis: anon limited by IP (2/min), authenticated users behind SAME IP get independent per-user buckets!
	feeBody := `{"shop_id":"shop-a","to_province_code":"79","items":[{"sku_id":"sku-a-w","quantity":1}]}`
	for i := 1; i <= 3; i++ {
		rec := do(http.MethodPost, "/api/v1/shipping/calculate-fee", "203.0.113.7", "", feeBody)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("anon calculate-fee call %d: want %d, got %d (%s)", i, want, rec.Code, rec.Body.String())
		}
	}
	// Authenticated buyer-1 & buyer-2 behind the SAME exhausted IP 203.0.113.7 still succeed (2 each)!
	for _, tok := range []string{buyer1Tok, buyer2Tok} {
		for i := 1; i <= 3; i++ {
			rec := do(http.MethodPost, "/api/v1/shipping/calculate-fee", "203.0.113.7", tok, feeBody)
			want := http.StatusOK
			if i == 3 {
				want = http.StatusTooManyRequests
			}
			if rec.Code != want {
				t.Fatalf("auth calculate-fee call %d on shared IP: want %d, got %d (%s)", i, want, rec.Code, rec.Body.String())
			}
		}
	}

	// Invalid/revoked JWT on optional-auth calculate-fee returns 401; Redis revocation error returns 503.
	if rec := do(http.MethodPost, "/api/v1/shipping/calculate-fee", "203.0.113.8", "invalid.jwt.token", feeBody); rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid JWT on optional-auth calculate-fee: want 401, got %d", rec.Code)
	}
	revoker.mu.Lock()
	revoker.err = status.Error(codes.Unavailable, "redis down")
	revoker.mu.Unlock()
	if rec := do(http.MethodPost, "/api/v1/shipping/calculate-fee", "203.0.113.8", buyer1Tok, feeBody); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("revoker error on optional-auth calculate-fee: want 503, got %d", rec.Code)
	}
	revoker.mu.Lock()
	revoker.err = nil
	revoker.mu.Unlock()

	// Ensure rl uses separate keys for ip vs user vs phone_otp.
	rl.mu.Lock()
	_, hasIPKey := rl.counts["rl:shipping_fee:ip:203.0.113.7"]
	_, hasUser1Key := rl.counts["rl:shipping_fee:user:buyer-rl-1"]
	_, hasPhoneKey := rl.counts["rl:phone_otp:user:buyer-rl-1"]
	rl.mu.Unlock()
	if !hasIPKey || !hasUser1Key || !hasPhoneKey {
		t.Fatalf("missing expected isolated Redis keys: ip=%v user=%v phone=%v", hasIPKey, hasUser1Key, hasPhoneKey)
	}
	_ = middleware.RateLimitPhoneOTP
}

func TestW3B_CodImportLongTimeoutAndWriteDeadlineThroughMiddleware(t *testing.T) {
	authMgr, err := auth.NewManager("w3b-cod-timeout-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-timeout", "admin", "")

	userMock := &w3bUserClient{}
	payMock := &w3bPaymentClient{
		importDelay:   350 * time.Millisecond,
		listRunsDelay: 350 * time.Millisecond,
	}
	reg := prometheus.NewRegistry()
	red := metrics.NewRED("gateway-w3b-timeout-test", reg)

	h := New(Dependencies{
		ServiceName: "gateway-w3b-timeout-test",
		Clients: &clients.Clients{
			User:    userMock,
			Payment: payMock,
			Timeout: 80 * time.Millisecond,
		},
		AuthManager: authMgr,
		RED:         red,
		Logger:      logger.New("gateway-w3b-timeout-test", logger.WithWriter(io.Discard)),
	})

	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Config.WriteTimeout = 200 * time.Millisecond
	srv.Start()
	defer srv.Close()

	httpClient := &http.Client{Timeout: 5 * time.Second}

	// 1. COD CSV import takes 350ms (> 80ms Clients.Timeout and > 200ms Server.ReadTimeout/WriteTimeout):
	//    must override gRPC timeout to 110s, extend HTTP write/read deadlines via ResponseController through
	//    all middleware wrappers (SecurityHeaders, telemetry, RED, RequestLogger, AdminAudit),
	//    forward Authorization & x-client-ip metadata, and still return 200 OK.
	csvBody := "tracking_code,cod_amount_vnd,delivered_at\nGHN-001,250000,2026-11-24\n"
	reqImport, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", strings.NewReader(csvBody))
	if err != nil {
		t.Fatalf("new import request: %v", err)
	}
	reqImport.Header.Set("Authorization", "Bearer "+adminTok)
	reqImport.Header.Set("Content-Type", "text/csv")

	start := time.Now()
	respImport, err := httpClient.Do(reqImport)
	if err != nil {
		t.Fatalf("POST /admin/reconciliation/cod/import failed (write deadline not extended?): %v", err)
	}
	defer respImport.Body.Close()
	importBody, err := io.ReadAll(respImport.Body)
	if err != nil {
		t.Fatalf("read import response body: %v", err)
	}
	if respImport.StatusCode != http.StatusOK {
		t.Fatalf("POST /admin/reconciliation/cod/import: want 200, got %d (%s)", respImport.StatusCode, string(importBody))
	}
	if !strings.Contains(string(importBody), `"run_id":"run-cod-1"`) {
		t.Fatalf("unexpected import body: %s", string(importBody))
	}

	payMock.mu.Lock()
	gotMD := payMock.lastImportMD
	gotDeadline := payMock.lastImportDeadline
	payMock.mu.Unlock()
	if vals := gotMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+adminTok {
		t.Fatalf("import must forward admin Bearer token, got %v", vals)
	}
	if vals := gotMD.Get(clients.MDClientIP); len(vals) == 0 || vals[0] == "" {
		t.Fatalf("import must forward x-client-ip metadata, got %v", vals)
	}
	if remaining := gotDeadline.Sub(start); remaining < 100*time.Second || remaining > 120*time.Second {
		t.Fatalf("import gRPC deadline = %v from start, want ~110s", remaining)
	}

	// 2. Another route on the same server still obeys Clients.Timeout (100ms < 300ms delay) -> 504 DEADLINE_EXCEEDED.
	reqRuns, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/admin/reconciliation/runs", nil)
	if err != nil {
		t.Fatalf("new runs request: %v", err)
	}
	reqRuns.Header.Set("Authorization", "Bearer "+adminTok)
	respRuns, err := httpClient.Do(reqRuns)
	if err != nil {
		t.Fatalf("GET /admin/reconciliation/runs request error: %v", err)
	}
	defer respRuns.Body.Close()
	runsBody, _ := io.ReadAll(respRuns.Body)
	if respRuns.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("GET /admin/reconciliation/runs must still timeout with 504, got %d (%s)", respRuns.StatusCode, string(runsBody))
	}
	if !strings.Contains(string(runsBody), `"code":"DEADLINE_EXCEEDED"`) {
		t.Fatalf("GET /admin/reconciliation/runs want DEADLINE_EXCEEDED, got %s", string(runsBody))
	}
}

func buildExactByteValidCODCSV(t *testing.T, targetBytes int) []byte {
	t.Helper()
	const header = "tracking_code,cod_amount_vnd,delivered_at,order_code\n"
	const baseRow = "GHN-0001,250000,2026-11-24,NM-0001"
	const rowCount = 4500 // <= maxCODCSVRows (5000)
	remaining := targetBytes - len(header)
	if remaining < rowCount*(len(baseRow)+1) {
		t.Fatalf("targetBytes %d too small for %d rows", targetBytes, rowCount)
	}
	perRow := remaining / rowCount
	extra := remaining % rowCount
	var buf bytes.Buffer
	buf.Grow(targetBytes)
	buf.WriteString(header)
	for i := 0; i < rowCount; i++ {
		pad := perRow - len(baseRow) - 1
		if i == 0 {
			pad += extra
		}
		buf.WriteString(baseRow)
		if pad > 0 {
			buf.Write(bytes.Repeat([]byte(" "), pad))
		}
		buf.WriteByte('\n')
	}
	if buf.Len() != targetBytes {
		t.Fatalf("generated CSV len=%d, want %d", buf.Len(), targetBytes)
	}
	return buf.Bytes()
}

func buildMultipartCODPayload(t *testing.T, carrierCode, fileName string, csvData []byte) (contentType string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	mpw := multipart.NewWriter(&buf)
	if carrierCode != "" {
		if err := mpw.WriteField("carrier_code", carrierCode); err != nil {
			t.Fatalf("WriteField carrier_code: %v", err)
		}
	}
	if err := mpw.WriteField("run_date", "2026-11-24"); err != nil {
		t.Fatalf("WriteField run_date: %v", err)
	}
	fw, err := mpw.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(csvData); err != nil {
		t.Fatalf("write CSV part: %v", err)
	}
	if err := mpw.Close(); err != nil {
		t.Fatalf("Close multipart writer: %v", err)
	}
	return mpw.FormDataContentType(), buf.Bytes()
}

func TestRevC_CodImportMultipartOverheadAnd2MiBCsvCap(t *testing.T) {
	hw := newW3BHarness(t)
	adminTok, _, _, _ := hw.authMgr.SignAccessToken("admin-1", "admin", "")

	// 1. Multipart upload containing a valid 2,097,000-byte CSV (< 2 MiB), whose total multipart body
	//    exceeds 2 MiB due to boundary/form fields (< 2 MiB + 64 KiB): must pass the cap -> 200 OK.
	csv2097000 := buildExactByteValidCODCSV(t, 2097000)
	ctOK, bodyOK := buildMultipartCODPayload(t, "ghn", "near-2mib.csv", csv2097000)
	if int64(len(bodyOK)) <= httpx.MaxBodyBytes {
		t.Fatalf("test setup error: expected multipart body > 2 MiB, got %d", len(bodyOK))
	}
	reqOK := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reconciliation/cod/import", bytes.NewReader(bodyOK))
	reqOK.Header.Set("Authorization", "Bearer "+adminTok)
	reqOK.Header.Set("Content-Type", ctOK)
	recOK := httptest.NewRecorder()
	hw.handler.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("multipart CSV 2,097,000 bytes (total body=%d): want 200, got %d (%s)", len(bodyOK), recOK.Code, recOK.Body.String())
	}

	// Chunked (ContentLength = -1) multipart with 2,097,000-byte CSV must also succeed.
	reqChunkedOK := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reconciliation/cod/import", io.NopCloser(bytes.NewReader(bodyOK)))
	reqChunkedOK.ContentLength = -1
	reqChunkedOK.Header.Set("Authorization", "Bearer "+adminTok)
	reqChunkedOK.Header.Set("Content-Type", ctOK)
	recChunkedOK := httptest.NewRecorder()
	hw.handler.ServeHTTP(recChunkedOK, reqChunkedOK)
	if recChunkedOK.Code != http.StatusOK {
		t.Fatalf("chunked multipart CSV 2,097,000 bytes: want 200, got %d (%s)", recChunkedOK.Code, recChunkedOK.Body.String())
	}

	callsBeforeReject := hw.payment.importCalls

	// 2. Multipart upload containing a 2 MiB + 1 byte CSV (total body still < 2 MiB + 64 KiB):
	//    must still be rejected with 413 PAYLOAD_TOO_LARGE on the CSV content cap.
	csvOver := buildExactByteValidCODCSV(t, httpx.MaxBodyBytes+1)
	ctOver, bodyOver := buildMultipartCODPayload(t, "ghn", "over-2mib.csv", csvOver)
	reqOver := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reconciliation/cod/import", bytes.NewReader(bodyOver))
	reqOver.Header.Set("Authorization", "Bearer "+adminTok)
	reqOver.Header.Set("Content-Type", ctOver)
	recOver := httptest.NewRecorder()
	hw.handler.ServeHTTP(recOver, reqOver)
	if recOver.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("multipart CSV 2 MiB + 1 byte: want 413, got %d (%s)", recOver.Code, recOver.Body.String())
	}

	// 3. Non-multipart text/csv of 2 MiB + 1 byte on cod/import stays strictly capped at 2 MiB -> 413.
	reqRawOver := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", bytes.NewReader(csvOver))
	reqRawOver.Header.Set("Authorization", "Bearer "+adminTok)
	reqRawOver.Header.Set("Content-Type", "text/csv")
	recRawOver := httptest.NewRecorder()
	hw.handler.ServeHTTP(recRawOver, reqRawOver)
	if recRawOver.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("text/csv 2 MiB + 1 byte: want 413, got %d (%s)", recRawOver.Code, recRawOver.Body.String())
	}

	// 4. Another route with 2 MiB + 1 byte body (both JSON and multipart/form-data) must stay capped at 2 MiB -> 413.
	otherJSON := `{"term":"` + strings.Repeat("a", httpx.MaxBodyBytes) + `","action":"BLOCK"}`
	reqOtherJSON := httptest.NewRequest(http.MethodPost, "/api/v1/admin/banned-keywords", strings.NewReader(otherJSON))
	reqOtherJSON.Header.Set("Authorization", "Bearer "+adminTok)
	reqOtherJSON.Header.Set("Content-Type", "application/json")
	recOtherJSON := httptest.NewRecorder()
	hw.handler.ServeHTTP(recOtherJSON, reqOtherJSON)
	if recOtherJSON.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("other route JSON > 2 MiB: want 413, got %d (%s)", recOtherJSON.Code, recOtherJSON.Body.String())
	}

	reqOtherMP := httptest.NewRequest(http.MethodPost, "/api/v1/admin/banned-keywords", bytes.NewReader(bodyOK))
	reqOtherMP.Header.Set("Authorization", "Bearer "+adminTok)
	reqOtherMP.Header.Set("Content-Type", ctOK)
	recOtherMP := httptest.NewRecorder()
	hw.handler.ServeHTTP(recOtherMP, reqOtherMP)
	if recOtherMP.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("other route multipart > 2 MiB: want 413, got %d (%s)", recOtherMP.Code, recOtherMP.Body.String())
	}

	if hw.payment.importCalls != callsBeforeReject {
		t.Fatalf("413 rejected uploads must not call PaymentService, got %d extra calls", hw.payment.importCalls-callsBeforeReject)
	}
}

func TestRevC_AdminAuditLargeResponsePreservesTargetIDAndSummary(t *testing.T) {
	hw := newW3BHarness(t)
	adminTok, _, _, _ := hw.authMgr.SignAccessToken("admin-1", "admin", "")

	// 1. COD import returning > 4 KiB JSON (60 items).
	largeItems := make([]*paymentv1.ReconciliationItem, 0, 60)
	for i := 0; i < 60; i++ {
		largeItems = append(largeItems, &paymentv1.ReconciliationItem{
			PaymentId:         fmt.Sprintf("pay-large-%03d", i),
			LocalAmountVnd:    250000,
			ProviderAmountVnd: 250000,
			Status:            paymentv1.ReconciliationStatus_RECONCILIATION_STATUS_MATCHED,
			TrackingCode:      fmt.Sprintf("GHN-TRACK-LARGE-%05d", i),
			OrderId:           fmt.Sprintf("ord-large-%03d", i),
			LineNumber:        int32(i + 2),
		})
	}
	hw.payment.mu.Lock()
	hw.payment.importRespOverride = &paymentv1.ImportCodReconciliationResponse{
		Run: &paymentv1.ReconciliationRunSummary{
			RunId:                  "run-cod-large-1",
			RunType:                "cod_file",
			Provider:               "ghn",
			RunDate:                "2026-11-24",
			FileName:               "customer-pii-export.csv",
			TotalRows:              60,
			MatchedCount:           50,
			AlreadyReconciledCount: 3,
			MissingLocalCount:      2,
			MissingProviderCount:   1,
			AmountMismatchCount:    2,
			NotDeliveredCount:      2,
			TotalMatchedVnd:        12500000,
			Status:                 "completed",
			CreatedBy:              "admin-1",
			CreatedAt:              timestamppb.Now(),
		},
		Items: largeItems,
	}
	hw.payment.mu.Unlock()

	csvPayload := "tracking_code,cod_amount_vnd,delivered_at\nGHN-PII-RAW-999,250000,2026-11-24\n"
	recCod := hw.do(http.MethodPost, "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn&file_name=customer-pii-export.csv", adminTok, "text/csv", csvPayload)
	if recCod.Code != http.StatusOK {
		t.Fatalf("large COD import: want 200, got %d (%s)", recCod.Code, recCod.Body.String())
	}
	if len(recCod.Body.Bytes()) <= 4096 {
		t.Fatalf("test setup error: expected COD import response > 4096 bytes, got %d", len(recCod.Body.Bytes()))
	}

	hw.user.mu.Lock()
	codAudits := append([]*userv1.WriteAuditLogRequest(nil), hw.user.auditReqs...)
	hw.user.auditReqs = nil
	hw.user.mu.Unlock()
	if len(codAudits) != 1 {
		t.Fatalf("expected 1 COD import audit record, got %d", len(codAudits))
	}
	codAudit := codAudits[0]
	if codAudit.GetTargetType() != "reconciliation_run" || codAudit.GetTargetId() != "run-cod-large-1" {
		t.Fatalf("COD import audit target mismatch: type=%q id=%q, want (reconciliation_run, run-cod-large-1)", codAudit.GetTargetType(), codAudit.GetTargetId())
	}
	var codAfter map[string]any
	if err := json.Unmarshal([]byte(codAudit.GetAfterJson()), &codAfter); err != nil {
		t.Fatalf("COD import audit after_json invalid JSON: %v (%s)", err, codAudit.GetAfterJson())
	}
	if codAfter["run_id"] != "run-cod-large-1" ||
		codAfter["total_rows"] != float64(60) ||
		codAfter["matched_count"] != float64(50) ||
		codAfter["already_reconciled_count"] != float64(3) ||
		codAfter["missing_local_count"] != float64(2) ||
		codAfter["missing_provider_count"] != float64(1) ||
		codAfter["amount_mismatch_count"] != float64(2) ||
		codAfter["not_delivered_count"] != float64(2) ||
		codAfter["total_matched_vnd"] != float64(12500000) {
		t.Fatalf("COD import audit after_json missing run summary counters/total: %s", codAudit.GetAfterJson())
	}
	if strings.Contains(codAudit.GetAfterJson(), "GHN-PII-RAW-999") || strings.Contains(codAudit.GetAfterJson(), "GHN-TRACK-LARGE-") || strings.Contains(codAudit.GetAfterJson(), "customer-pii-export.csv") {
		t.Fatalf("COD import audit after_json must not leak CSV contents, items, or file_name: %s", codAudit.GetAfterJson())
	}

	// 2. Banned-keywords rescan returning > 4 KiB JSON (40 product samples).
	largeSamples := make([]*productv1.BannedContentProductSample, 0, 40)
	for i := 0; i < 40; i++ {
		largeSamples = append(largeSamples, &productv1.BannedContentProductSample{
			ProductId:     fmt.Sprintf("prod-rescan-%03d", i),
			ShopId:        "shop-a",
			Name:          fmt.Sprintf("Sản phẩm vi phạm mẫu dài số %03d chứa từ khoá cấm", i),
			Slug:          fmt.Sprintf("san-pham-vi-pham-mau-dai-so-%03d", i),
			Status:        productv1.ProductStatus_PRODUCT_STATUS_REJECTED,
			MatchedTerms:  []string{"hàng giả", "cấm bán"},
			HighestAction: productv1.BannedKeywordAction_BANNED_KEYWORD_ACTION_BLOCK,
		})
	}
	hw.product.mu.Lock()
	hw.product.rescanRespOverride = &productv1.RescanProductsForBannedContentResponse{
		ScannedCount: 500,
		BlockedCount: 12,
		FlaggedCount: 28,
		Samples:      largeSamples,
		HasMore:      true,
		NextCursor:   "cur-next-42",
	}
	hw.product.mu.Unlock()

	recRescan := hw.do(http.MethodPost, "/api/v1/admin/banned-keywords/rescan", adminTok, "application/json", `{"keyword_ids":["kw-1","kw-2"],"dry_run":true,"cursor":"cur-1","limit":500}`)
	if recRescan.Code != http.StatusOK {
		t.Fatalf("large banned-keywords rescan: want 200, got %d (%s)", recRescan.Code, recRescan.Body.String())
	}
	if len(recRescan.Body.Bytes()) <= 4096 {
		t.Fatalf("test setup error: expected rescan response > 4096 bytes, got %d", len(recRescan.Body.Bytes()))
	}

	hw.user.mu.Lock()
	rescanAudits := append([]*userv1.WriteAuditLogRequest(nil), hw.user.auditReqs...)
	hw.user.mu.Unlock()
	if len(rescanAudits) != 1 {
		t.Fatalf("expected 1 rescan audit record, got %d", len(rescanAudits))
	}
	rescanAudit := rescanAudits[0]
	var rescanAfter map[string]any
	if err := json.Unmarshal([]byte(rescanAudit.GetAfterJson()), &rescanAfter); err != nil {
		t.Fatalf("rescan audit after_json invalid JSON: %v (%s)", err, rescanAudit.GetAfterJson())
	}
	if rescanAfter["dry_run"] != true ||
		rescanAfter["cursor"] != "cur-1" ||
		rescanAfter["limit"] != float64(500) ||
		rescanAfter["scanned_count"] != float64(500) ||
		rescanAfter["blocked_count"] != float64(12) ||
		rescanAfter["flagged_count"] != float64(28) ||
		rescanAfter["has_more"] != true ||
		rescanAfter["next_cursor"] != "cur-next-42" {
		t.Fatalf("rescan audit after_json missing params/counters: %s", rescanAudit.GetAfterJson())
	}
	if strings.Contains(rescanAudit.GetAfterJson(), "prod-rescan-000") {
		t.Fatalf("rescan audit after_json must not dump full samples array: %s", rescanAudit.GetAfterJson())
	}
}

func TestRevC_CodImportInvalidUTF8MetadataReturns400(t *testing.T) {
	hw := newW3BHarness(t)
	adminTok, _, _, _ := hw.authMgr.SignAccessToken("admin-1", "admin", "")
	validCSV := "tracking_code,cod_amount_vnd,delivered_at\nGHN-001,250000,2026-11-24\n"
	callsBefore := hw.payment.importCalls

	// 1. Invalid UTF-8 in query params (?carrier_code=%ff and ?file_name=%ff.csv).
	for name, target := range map[string]string{
		"query_carrier_code":  "/api/v1/admin/reconciliation/cod/import?carrier_code=%ff",
		"query_file_name":     "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn&file_name=%ff.csv",
		"query_path_dir_utf8": "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn&file_name=%ff/cod.csv",
	} {
		rec := hw.do(http.MethodPost, target, adminTok, "text/csv", validCSV)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid UTF-8 %s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	// 2. Invalid UTF-8 in multipart form fields (carrier_code and file_name).
	for name, tc := range map[string]struct{ carrier, fileName string }{
		"multipart_carrier_code": {carrier: "ghn-\xff", fileName: "ok.csv"},
		"multipart_file_name":    {carrier: "ghn", fileName: "bad-\xff.csv"},
	} {
		ct, body := buildMultipartCODPayload(t, tc.carrier, tc.fileName, []byte(validCSV))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/reconciliation/cod/import", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminTok)
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()
		hw.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid UTF-8 %s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}

	if hw.payment.importCalls != callsBefore {
		t.Fatalf("invalid UTF-8 metadata must not reach PaymentService, got %d extra calls", hw.payment.importCalls-callsBefore)
	}
}

func TestRevC_ReviewImageURLHostnameAndUserinfo(t *testing.T) {
	hw := newW3BHarness(t)
	buyerTok, _, _, _ := hw.authMgr.SignAccessToken("buyer-1", "buyer", "")

	badURLs := []string{
		"https://:80/a.png",
		"https://:443/a.png",
		"https://[]:443/a.png",
		"https://user:pass@cdn.example.com/a.png",
		"https://user@cdn.example.com/a.png",
		"https://@cdn.example.com/a.png",
	}
	for _, u := range badURLs {
		payload, _ := json.Marshal(map[string]any{
			"order_item_id": "oi-1",
			"rating":        5,
			"comment":       "Tốt",
			"image_urls":    []string{u},
		})
		rec := hw.do(http.MethodPost, "/api/v1/orders/ord-1/reviews", buyerTok, "application/json", string(payload))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid review image_url %q: want 400, got %d (%s)", u, rec.Code, rec.Body.String())
		}
	}

	goodPayload, _ := json.Marshal(map[string]any{
		"order_item_id": "oi-1",
		"rating":        5,
		"comment":       "Tốt",
		"image_urls":    []string{"https://cdn.example.com:8443/a.png", "/products/local.png"},
	})
	recGood := hw.do(http.MethodPost, "/api/v1/orders/ord-1/reviews", buyerTok, "application/json", string(goodPayload))
	if recGood.Code != http.StatusCreated {
		t.Fatalf("valid review image_urls: want 201, got %d (%s)", recGood.Code, recGood.Body.String())
	}
}

func TestRevC_ShipmentJobStatusHiddenFromBuyerVisibleToSellerOwnerAndAdmin(t *testing.T) {
	hw := newW3BHarness(t)
	buyerTok, _, _, _ := hw.authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerOwnerTok, _, _, _ := hw.authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	otherSellerTok, _, _, _ := hw.authMgr.SignAccessToken("seller-b", "seller", "shop-b")
	adminTok, _, _, _ := hw.authMgr.SignAccessToken("admin-1", "admin", "")

	// Buyer (and seller purchasing from a different shop) must NOT receive shipment_job_status.
	for name, tc := range map[string]struct{ path, tok string }{
		"buyer_get_order":        {"/api/v1/orders/ord-1", buyerTok},
		"buyer_list_orders":      {"/api/v1/orders", buyerTok},
		"other_seller_get_order": {"/api/v1/orders/ord-1", otherSellerTok},
	} {
		rec := hw.do(http.MethodGet, tc.path, tc.tok, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d (%s)", name, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "shipment_job_status") || strings.Contains(rec.Body.String(), `"FAILED"`) {
			t.Fatalf("%s leaked shipment_job_status to non-owner/buyer: %s", name, rec.Body.String())
		}
	}

	// Selling shop owner and admin MUST still receive shipment_job_status.
	for name, tc := range map[string]struct{ path, tok string }{
		"seller_owner_get_order": {"/api/v1/orders/ord-1", sellerOwnerTok},
		"seller_list_orders":     {"/api/v1/seller/orders", sellerOwnerTok},
		"admin_get_order":        {"/api/v1/orders/ord-1", adminTok},
		"admin_list_orders":      {"/api/v1/admin/orders", adminTok},
	} {
		rec := hw.do(http.MethodGet, tc.path, tc.tok, "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d (%s)", name, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"shipment_job_status":"FAILED"`) {
			t.Fatalf("%s missing shipment_job_status=FAILED: %s", name, rec.Body.String())
		}
	}
}
