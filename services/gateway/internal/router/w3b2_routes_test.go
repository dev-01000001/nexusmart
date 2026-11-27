package router

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type w3b2UserClient struct {
	userv1.UserServiceClient
	mu                     sync.Mutex
	auditReqs              []*userv1.WriteAuditLogRequest
	lastCreateStaffReq     *userv1.AdminCreateStaffUserRequest
	lastActivitiesReq      *userv1.AdminListUserActivitiesRequest
	lastSuspendReq         *userv1.AdminRequestShopSuspensionRequest
	lastUnsuspendReq       *userv1.AdminUnsuspendShopRequest
	lastDecideShopReq      *userv1.AdminDecideShopSuspensionRequest
	lastListShopApprReq    *userv1.AdminListShopApprovalsRequest
	listShopApprovalsCalls int
	listUsersCalls         int
	updateRoleCalls        int
	decideShopErr          error
}

func (u *w3b2UserClient) WriteAuditLog(_ context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	u.mu.Lock()
	u.auditReqs = append(u.auditReqs, in)
	u.mu.Unlock()
	return &userv1.WriteAuditLogResponse{AuditLog: &userv1.AuditLog{Id: "audit-w3b2"}}, nil
}

func (u *w3b2UserClient) AdminListUsers(_ context.Context, _ *userv1.AdminListUsersRequest, _ ...grpc.CallOption) (*userv1.AdminListUsersResponse, error) {
	u.mu.Lock()
	u.listUsersCalls++
	u.mu.Unlock()
	return &userv1.AdminListUsersResponse{Users: []*userv1.User{}, Total: 0}, nil
}

func (u *w3b2UserClient) AdminUpdateUserStatus(_ context.Context, in *userv1.AdminUpdateUserStatusRequest, _ ...grpc.CallOption) (*userv1.AdminUpdateUserStatusResponse, error) {
	return &userv1.AdminUpdateUserStatusResponse{User: &userv1.User{Id: in.GetTargetUserId(), Status: in.GetStatus()}}, nil
}

func (u *w3b2UserClient) AdminUpdateUserRole(_ context.Context, in *userv1.AdminUpdateUserRoleRequest, _ ...grpc.CallOption) (*userv1.AdminUpdateUserRoleResponse, error) {
	u.mu.Lock()
	u.updateRoleCalls++
	u.mu.Unlock()
	return &userv1.AdminUpdateUserRoleResponse{User: &userv1.User{Id: in.GetTargetUserId(), Role: in.GetRole()}}, nil
}

func (u *w3b2UserClient) AdminCreateStaffUser(_ context.Context, in *userv1.AdminCreateStaffUserRequest, _ ...grpc.CallOption) (*userv1.AdminCreateStaffUserResponse, error) {
	u.mu.Lock()
	u.lastCreateStaffReq = in
	u.mu.Unlock()
	return &userv1.AdminCreateStaffUserResponse{
		User: &userv1.User{
			Id:       "staff-new-1",
			FullName: in.GetFullName(),
			Email:    in.GetEmail(),
			Phone:    in.GetPhone(),
			Role:     in.GetRole(),
			Status:   userv1.UserStatus_USER_STATUS_ACTIVE,
		},
		PasswordResetSent: true,
	}, nil
}

func (u *w3b2UserClient) AdminListUserActivities(_ context.Context, in *userv1.AdminListUserActivitiesRequest, _ ...grpc.CallOption) (*userv1.AdminListUserActivitiesResponse, error) {
	u.mu.Lock()
	u.lastActivitiesReq = in
	u.mu.Unlock()
	return &userv1.AdminListUserActivitiesResponse{
		Items: []*userv1.UserActivityEntry{
			{
				Id:           "act-1",
				UserId:       in.GetUserId(),
				EventType:    "auth.login",
				IpAddress:    "203.0.113.xxx",
				MetadataJson: `{"method":"password"}`,
				CreatedAt:    timestamppb.Now(),
			},
		},
		Total:            1,
		CodRejectedCount: 2,
	}, nil
}

func (u *w3b2UserClient) AdminListShops(_ context.Context, _ *userv1.AdminListShopsRequest, _ ...grpc.CallOption) (*userv1.AdminListShopsResponse, error) {
	return &userv1.AdminListShopsResponse{
		Shops: []*userv1.Shop{
			{
				Id:              "shop-1",
				Name:            "Shop One",
				Status:          userv1.ShopStatus_SHOP_STATUS_SUSPENDED,
				SuspendedReason: "counterfeit reports",
				SuspendedAt:     timestamppb.Now(),
				StatusVersion:   3,
			},
		},
		Total: 1,
	}, nil
}

func (u *w3b2UserClient) AdminReviewShop(_ context.Context, in *userv1.AdminReviewShopRequest, _ ...grpc.CallOption) (*userv1.AdminReviewShopResponse, error) {
	return &userv1.AdminReviewShopResponse{Shop: &userv1.Shop{Id: in.GetShopId(), Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE}}, nil
}

func (u *w3b2UserClient) AdminRequestShopSuspension(_ context.Context, in *userv1.AdminRequestShopSuspensionRequest, _ ...grpc.CallOption) (*userv1.AdminRequestShopSuspensionResponse, error) {
	u.mu.Lock()
	u.lastSuspendReq = in
	u.mu.Unlock()
	return &userv1.AdminRequestShopSuspensionResponse{
		Approval: &userv1.ShopSuspensionApproval{
			Id:            "shop-appr-1",
			ShopId:        in.GetShopId(),
			ShopName:      "Shop One",
			Status:        userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING,
			Reason:        in.GetReason(),
			RequestedBy:   "ops-1",
			RequestedRole: "ops",
			CreatedAt:     timestamppb.Now(),
		},
	}, nil
}

func (u *w3b2UserClient) AdminListShopApprovals(_ context.Context, in *userv1.AdminListShopApprovalsRequest, _ ...grpc.CallOption) (*userv1.AdminListShopApprovalsResponse, error) {
	u.mu.Lock()
	u.listShopApprovalsCalls++
	u.lastListShopApprReq = in
	u.mu.Unlock()
	return &userv1.AdminListShopApprovalsResponse{
		Items: []*userv1.ShopSuspensionApproval{
			{
				Id:            "shop-appr-1",
				ShopId:        "shop-1",
				ShopName:      "Shop One",
				Status:        userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_PENDING,
				Reason:        "policy breach",
				RequestedBy:   "mod-1",
				RequestedRole: "moderator",
				CreatedAt:     timestamppb.Now(),
			},
		},
		Total: 1,
	}, nil
}

func (u *w3b2UserClient) AdminDecideShopSuspension(_ context.Context, in *userv1.AdminDecideShopSuspensionRequest, _ ...grpc.CallOption) (*userv1.AdminDecideShopSuspensionResponse, error) {
	u.mu.Lock()
	u.lastDecideShopReq = in
	err := u.decideShopErr
	u.mu.Unlock()
	if err != nil {
		return nil, err
	}
	now := timestamppb.Now()
	return &userv1.AdminDecideShopSuspensionResponse{
		Approval: &userv1.ShopSuspensionApproval{
			Id:            in.GetApprovalId(),
			ShopId:        "shop-1",
			ShopName:      "Shop One",
			Status:        userv1.ShopApprovalStatus_SHOP_APPROVAL_STATUS_APPROVED,
			Reason:        "policy breach",
			RequestedBy:   "mod-1",
			RequestedRole: "moderator",
			DecidedBy:     "ops-2",
			DecidedNote:   in.GetNote(),
			CreatedAt:     now,
			DecidedAt:     now,
		},
		Shop: &userv1.Shop{
			Id:              "shop-1",
			Name:            "Shop One",
			Status:          userv1.ShopStatus_SHOP_STATUS_SUSPENDED,
			SuspendedReason: "policy breach",
			SuspendedAt:     now,
			StatusVersion:   2,
		},
	}, nil
}

func (u *w3b2UserClient) AdminUnsuspendShop(_ context.Context, in *userv1.AdminUnsuspendShopRequest, _ ...grpc.CallOption) (*userv1.AdminUnsuspendShopResponse, error) {
	u.mu.Lock()
	u.lastUnsuspendReq = in
	u.mu.Unlock()
	return &userv1.AdminUnsuspendShopResponse{
		Shop: &userv1.Shop{
			Id:            in.GetShopId(),
			Name:          "Shop One",
			Status:        userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			StatusVersion: 3,
		},
	}, nil
}

func (u *w3b2UserClient) GetShopBankAccount(_ context.Context, in *userv1.GetShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.GetShopBankAccountResponse, error) {
	return &userv1.GetShopBankAccountResponse{BankAccount: &userv1.ShopBankAccount{Id: "ba-1", ShopId: in.GetShopId()}}, nil
}

func (u *w3b2UserClient) AdminReviewShopBankAccount(_ context.Context, in *userv1.AdminReviewShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.AdminReviewShopBankAccountResponse, error) {
	return &userv1.AdminReviewShopBankAccountResponse{BankAccount: &userv1.ShopBankAccount{Id: in.GetBankAccountId(), ShopId: in.GetShopId()}}, nil
}

func (u *w3b2UserClient) ListAuditLogs(_ context.Context, _ *userv1.ListAuditLogsRequest, _ ...grpc.CallOption) (*userv1.ListAuditLogsResponse, error) {
	return &userv1.ListAuditLogsResponse{Logs: []*userv1.AuditLog{}, Total: 0}, nil
}

func (u *w3b2UserClient) GetPlatformUserStats(_ context.Context, in *userv1.GetPlatformUserStatsRequest, _ ...grpc.CallOption) (*userv1.GetPlatformUserStatsResponse, error) {
	return &userv1.GetPlatformUserStatsResponse{FromDate: in.GetFromDate(), ToDate: in.GetToDate()}, nil
}

func (u *w3b2UserClient) ListSites(_ context.Context, _ *userv1.ListSitesRequest, _ ...grpc.CallOption) (*userv1.ListSitesResponse, error) {
	return &userv1.ListSitesResponse{Sites: []*userv1.Site{}, Total: 0}, nil
}

func (u *w3b2UserClient) GetSiteDraft(_ context.Context, in *userv1.GetSiteDraftRequest, _ ...grpc.CallOption) (*userv1.GetSiteDraftResponse, error) {
	return &userv1.GetSiteDraftResponse{Site: &userv1.Site{Id: "site-1", ShopId: in.GetShopId()}}, nil
}

func (u *w3b2UserClient) ListSiteDomains(_ context.Context, _ *userv1.ListSiteDomainsRequest, _ ...grpc.CallOption) (*userv1.ListSiteDomainsResponse, error) {
	return &userv1.ListSiteDomainsResponse{Domains: []*userv1.Domain{}}, nil
}

func (u *w3b2UserClient) RemoveCustomDomain(_ context.Context, _ *userv1.RemoveCustomDomainRequest, _ ...grpc.CallOption) (*userv1.RemoveCustomDomainResponse, error) {
	return &userv1.RemoveCustomDomainResponse{}, nil
}

func (u *w3b2UserClient) SuspendSite(_ context.Context, in *userv1.SuspendSiteRequest, _ ...grpc.CallOption) (*userv1.SuspendSiteResponse, error) {
	return &userv1.SuspendSiteResponse{Site: &userv1.Site{Id: in.GetSiteId(), Status: userv1.SiteStatus_SITE_STATUS_SUSPENDED}}, nil
}

type w3b2ProductClient struct {
	productv1.ProductServiceClient
}

func (p *w3b2ProductClient) ListCategories(_ context.Context, _ *productv1.ListCategoriesRequest, _ ...grpc.CallOption) (*productv1.ListCategoriesResponse, error) {
	return &productv1.ListCategoriesResponse{Categories: []*productv1.Category{{Id: "cat-1", Name: "Ao"}}}, nil
}
func (p *w3b2ProductClient) CreateCategory(_ context.Context, in *productv1.CreateCategoryRequest, _ ...grpc.CallOption) (*productv1.CreateCategoryResponse, error) {
	return &productv1.CreateCategoryResponse{Category: &productv1.Category{Id: "cat-1", Name: in.GetName()}}, nil
}
func (p *w3b2ProductClient) UpdateCategory(_ context.Context, in *productv1.UpdateCategoryRequest, _ ...grpc.CallOption) (*productv1.UpdateCategoryResponse, error) {
	return &productv1.UpdateCategoryResponse{Category: &productv1.Category{Id: in.GetId(), Name: in.GetName()}}, nil
}
func (p *w3b2ProductClient) DeleteCategory(_ context.Context, _ *productv1.DeleteCategoryRequest, _ ...grpc.CallOption) (*productv1.DeleteCategoryResponse, error) {
	return &productv1.DeleteCategoryResponse{Ok: true}, nil
}
func (p *w3b2ProductClient) ListCategoryFeeSchedules(_ context.Context, _ *productv1.ListCategoryFeeSchedulesRequest, _ ...grpc.CallOption) (*productv1.ListCategoryFeeSchedulesResponse, error) {
	return &productv1.ListCategoryFeeSchedulesResponse{Schedules: []*productv1.CategoryFeeSchedule{}, Total: 0}, nil
}
func (p *w3b2ProductClient) SetCategoryFeeSchedule(_ context.Context, in *productv1.SetCategoryFeeScheduleRequest, _ ...grpc.CallOption) (*productv1.SetCategoryFeeScheduleResponse, error) {
	return &productv1.SetCategoryFeeScheduleResponse{Schedule: &productv1.CategoryFeeSchedule{Id: "cf-1", CategoryId: in.GetCategoryId()}}, nil
}
func (p *w3b2ProductClient) ListProducts(_ context.Context, _ *productv1.ListProductsRequest, _ ...grpc.CallOption) (*productv1.ListProductsResponse, error) {
	return &productv1.ListProductsResponse{Products: []*productv1.Product{}, Total: 0}, nil
}
func (p *w3b2ProductClient) GetProduct(_ context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	return &productv1.GetProductResponse{Product: &productv1.Product{Id: in.GetId(), Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED}}, nil
}
func (p *w3b2ProductClient) AdminReviewProduct(_ context.Context, in *productv1.AdminReviewProductRequest, _ ...grpc.CallOption) (*productv1.AdminReviewProductResponse, error) {
	return &productv1.AdminReviewProductResponse{Product: &productv1.Product{Id: in.GetProductId()}}, nil
}
func (p *w3b2ProductClient) ListBannedKeywords(_ context.Context, _ *productv1.ListBannedKeywordsRequest, _ ...grpc.CallOption) (*productv1.ListBannedKeywordsResponse, error) {
	return &productv1.ListBannedKeywordsResponse{Keywords: []*productv1.BannedKeyword{}, Total: 0}, nil
}
func (p *w3b2ProductClient) CreateBannedKeyword(_ context.Context, in *productv1.CreateBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.CreateBannedKeywordResponse, error) {
	return &productv1.CreateBannedKeywordResponse{Keyword: &productv1.BannedKeyword{Id: "kw-1", Term: in.GetTerm()}}, nil
}
func (p *w3b2ProductClient) UpdateBannedKeyword(_ context.Context, in *productv1.UpdateBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.UpdateBannedKeywordResponse, error) {
	return &productv1.UpdateBannedKeywordResponse{Keyword: &productv1.BannedKeyword{Id: in.GetId()}}, nil
}
func (p *w3b2ProductClient) DeleteBannedKeyword(_ context.Context, _ *productv1.DeleteBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.DeleteBannedKeywordResponse, error) {
	return &productv1.DeleteBannedKeywordResponse{Ok: true}, nil
}
func (p *w3b2ProductClient) PreviewBannedKeyword(_ context.Context, _ *productv1.PreviewBannedKeywordRequest, _ ...grpc.CallOption) (*productv1.PreviewBannedKeywordResponse, error) {
	return &productv1.PreviewBannedKeywordResponse{AffectedCount: 0}, nil
}
func (p *w3b2ProductClient) RescanProductsForBannedContent(_ context.Context, _ *productv1.RescanProductsForBannedContentRequest, _ ...grpc.CallOption) (*productv1.RescanProductsForBannedContentResponse, error) {
	return &productv1.RescanProductsForBannedContentResponse{ScannedCount: 0}, nil
}
func (p *w3b2ProductClient) GetSKUsBatch(_ context.Context, in *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	out := make([]*productv1.SKU, 0, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		out = append(out, &productv1.SKU{Id: id, ShopId: "shop-1", PriceVnd: 100000, WeightGrams: 500, IsActive: true})
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

type w3b2PromotionClient struct {
	promotionv1.PromotionServiceClient
}

func (p *w3b2PromotionClient) ListAvailableVouchers(_ context.Context, _ *promotionv1.ListAvailableVouchersRequest, _ ...grpc.CallOption) (*promotionv1.ListAvailableVouchersResponse, error) {
	return &promotionv1.ListAvailableVouchersResponse{Vouchers: []*promotionv1.Voucher{}}, nil
}
func (p *w3b2PromotionClient) CreateVoucher(_ context.Context, in *promotionv1.CreateVoucherRequest, _ ...grpc.CallOption) (*promotionv1.CreateVoucherResponse, error) {
	return &promotionv1.CreateVoucherResponse{Voucher: &promotionv1.Voucher{Id: "v-1", Code: in.GetCode()}}, nil
}
func (p *w3b2PromotionClient) UpdateVoucher(_ context.Context, in *promotionv1.UpdateVoucherRequest, _ ...grpc.CallOption) (*promotionv1.UpdateVoucherResponse, error) {
	return &promotionv1.UpdateVoucherResponse{Voucher: &promotionv1.Voucher{Id: in.GetVoucherId()}}, nil
}
func (p *w3b2PromotionClient) SetVoucherStatus(_ context.Context, in *promotionv1.SetVoucherStatusRequest, _ ...grpc.CallOption) (*promotionv1.SetVoucherStatusResponse, error) {
	return &promotionv1.SetVoucherStatusResponse{Voucher: &promotionv1.Voucher{Id: in.GetVoucherId(), Status: in.GetStatus()}}, nil
}
func (p *w3b2PromotionClient) DeleteVoucher(_ context.Context, _ *promotionv1.DeleteVoucherRequest, _ ...grpc.CallOption) (*promotionv1.DeleteVoucherResponse, error) {
	return &promotionv1.DeleteVoucherResponse{Ok: true}, nil
}
func (p *w3b2PromotionClient) ListAllFlashSales(_ context.Context, _ *promotionv1.ListAllFlashSalesRequest, _ ...grpc.CallOption) (*promotionv1.ListAllFlashSalesResponse, error) {
	return &promotionv1.ListAllFlashSalesResponse{FlashSales: []*promotionv1.FlashSale{}, Total: 0}, nil
}
func (p *w3b2PromotionClient) CreateFlashSale(_ context.Context, in *promotionv1.CreateFlashSaleRequest, _ ...grpc.CallOption) (*promotionv1.CreateFlashSaleResponse, error) {
	return &promotionv1.CreateFlashSaleResponse{FlashSale: &promotionv1.FlashSale{Id: "fs-1", Name: in.GetName()}}, nil
}
func (p *w3b2PromotionClient) EndFlashSaleWindow(_ context.Context, in *promotionv1.EndFlashSaleWindowRequest, _ ...grpc.CallOption) (*promotionv1.EndFlashSaleWindowResponse, error) {
	return &promotionv1.EndFlashSaleWindowResponse{Window: &promotionv1.FlashSale{Id: in.GetWindowId(), Status: "ENDED"}}, nil
}
func (p *w3b2PromotionClient) ListFlashSaleItems(_ context.Context, _ *promotionv1.ListFlashSaleItemsRequest, _ ...grpc.CallOption) (*promotionv1.ListFlashSaleItemsResponse, error) {
	return &promotionv1.ListFlashSaleItemsResponse{Items: []*promotionv1.FlashSaleItem{}, Total: 0}, nil
}
func (p *w3b2PromotionClient) ApproveFlashSaleItem(_ context.Context, in *promotionv1.ApproveFlashSaleItemRequest, _ ...grpc.CallOption) (*promotionv1.ApproveFlashSaleItemResponse, error) {
	return &promotionv1.ApproveFlashSaleItemResponse{Item: &promotionv1.FlashSaleItem{Id: in.GetItemId()}}, nil
}
func (p *w3b2PromotionClient) CreditCoins(_ context.Context, in *promotionv1.CreditCoinsRequest, _ ...grpc.CallOption) (*promotionv1.CreditCoinsResponse, error) {
	return &promotionv1.CreditCoinsResponse{UserId: in.GetUserId(), Balance: in.GetAmount()}, nil
}

type w3b2OrderClient struct {
	orderv1.OrderServiceClient
	mu               sync.Mutex
	listOrdersCalls  int
	lastListOrders   *orderv1.ListOrdersRequest
	lastListOrdersMD metadata.MD
	getOrderCalls    int
	lastCheckoutReq  *orderv1.CreateCheckoutRequest
	checkoutErr      error
	lastRetryReq     *orderv1.RetryPaymentRequest
	retryErr         error
}

func (o *w3b2OrderClient) CreateCheckout(_ context.Context, in *orderv1.CreateCheckoutRequest, _ ...grpc.CallOption) (*orderv1.CreateCheckoutResponse, error) {
	o.mu.Lock()
	o.lastCheckoutReq = in
	err := o.checkoutErr
	o.mu.Unlock()
	if err != nil {
		return nil, err
	}
	payURL := ""
	if in.GetPaymentMethod() == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY {
		payURL = "https://sbgateway.zalopay.vn/openinapp?order=mock123"
	}
	return &orderv1.CreateCheckoutResponse{
		CheckoutGroupId: "grp-zp-1",
		PaymentId:       "pay-zp-1",
		PaymentMethod:   in.GetPaymentMethod(),
		PaymentStatus:   "PENDING",
		PayUrl:          payURL,
		TotalPayableVnd: 250000,
		Orders: []*orderv1.Order{
			{
				Id:              "ord-zp-1",
				OrderCode:       "NM-ZP-1",
				BuyerId:         in.GetBuyerId(),
				ShopId:          "shop-1",
				Status:          orderv1.OrderStatus_ORDER_STATUS_PENDING,
				PaymentMethod:   in.GetPaymentMethod(),
				TotalPayableVnd: 250000,
			},
		},
	}, nil
}

func (o *w3b2OrderClient) RetryPayment(_ context.Context, in *orderv1.RetryPaymentRequest, _ ...grpc.CallOption) (*orderv1.RetryPaymentResponse, error) {
	o.mu.Lock()
	o.lastRetryReq = in
	err := o.retryErr
	o.mu.Unlock()
	if err != nil {
		return nil, err
	}
	switch in.GetPaymentMethod() {
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY:
		return &orderv1.RetryPaymentResponse{
			CheckoutGroupId: "grp-1",
			PaymentId:       "pay-zp-new",
			PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
			SwitchedToCod:   false,
			PayUrl:          "https://sbgateway.zalopay.vn/openinapp?order=retry999",
			Orders: []*orderv1.Order{
				{
					Id:              "ord-1",
					BuyerId:         "buyer-1",
					ShopId:          "shop-1",
					Status:          orderv1.OrderStatus_ORDER_STATUS_PENDING,
					PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
					PaymentFeeVnd:   5000,
					PaymentFeeBps:   200,
					TotalPayableVnd: 250000,
				},
			},
		}, nil
	case orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD:
		return &orderv1.RetryPaymentResponse{
			CheckoutGroupId: "grp-1",
			PaymentId:       "pay-cod-new",
			PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SwitchedToCod:   true,
			Orders: []*orderv1.Order{
				{
					Id:              "ord-1",
					BuyerId:         "buyer-1",
					ShopId:          "shop-1",
					Status:          orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
					PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
					PaymentFeeVnd:   0,
					PaymentFeeBps:   0,
					TotalPayableVnd: 250000,
				},
			},
		}, nil
	default:
		return &orderv1.RetryPaymentResponse{
			CheckoutGroupId: "grp-1",
			PaymentId:       "pay-vqr-new",
			PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
			SwitchedToCod:   false,
		}, nil
	}
}

func (o *w3b2OrderClient) ListOrders(ctx context.Context, in *orderv1.ListOrdersRequest, _ ...grpc.CallOption) (*orderv1.ListOrdersResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	o.mu.Lock()
	o.listOrdersCalls++
	o.lastListOrders = in
	o.lastListOrdersMD = md.Copy()
	o.mu.Unlock()
	return &orderv1.ListOrdersResponse{
		Orders: []*orderv1.Order{
			{
				Id:              "ord-recent-1",
				OrderCode:       "NM-REC-1",
				BuyerId:         in.GetBuyerId(),
				ShopId:          "shop-1",
				Status:          orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
				PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
				TotalPayableVnd: 320000,
				CommissionVnd:   16000,
			},
		},
		Total: 1,
	}, nil
}

func (o *w3b2OrderClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	o.mu.Lock()
	o.getOrderCalls++
	o.mu.Unlock()
	return &orderv1.GetOrderResponse{
		Order: &orderv1.Order{
			Id:                in.GetOrderId(),
			BuyerId:           "buyer-1",
			ShopId:            "shop-1",
			Status:            orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
			CommissionVnd:     15000,
			PaymentFeeVnd:     5000,
			PaymentFeeBps:     200,
			NeedsManualReview: true,
			ShipmentJobStatus: "QUEUED",
		},
	}, nil
}

func (o *w3b2OrderClient) GetOrderTimeline(_ context.Context, in *orderv1.GetOrderTimelineRequest, _ ...grpc.CallOption) (*orderv1.GetOrderTimelineResponse, error) {
	return &orderv1.GetOrderTimelineResponse{
		Events: []*orderv1.OrderTimelineEvent{
			{Id: "ev-1", OrderId: in.GetOrderId(), ToStatus: orderv1.OrderStatus_ORDER_STATUS_CONFIRMED},
		},
	}, nil
}

func (o *w3b2OrderClient) ListOrdersNeedingReview(_ context.Context, _ *orderv1.ListOrdersNeedingReviewRequest, _ ...grpc.CallOption) (*orderv1.ListOrdersNeedingReviewResponse, error) {
	return &orderv1.ListOrdersNeedingReviewResponse{Orders: []*orderv1.Order{}, Total: 0}, nil
}

func (o *w3b2OrderClient) ResolveOrderReview(_ context.Context, in *orderv1.ResolveOrderReviewRequest, _ ...grpc.CallOption) (*orderv1.ResolveOrderReviewResponse, error) {
	return &orderv1.ResolveOrderReviewResponse{Order: &orderv1.Order{Id: in.GetOrderId()}}, nil
}

func (o *w3b2OrderClient) GetPlatformReport(_ context.Context, in *orderv1.GetPlatformReportRequest, _ ...grpc.CallOption) (*orderv1.GetPlatformReportResponse, error) {
	return &orderv1.GetPlatformReportResponse{FromDate: in.GetFromDate(), ToDate: in.GetToDate()}, nil
}

func (o *w3b2OrderClient) ListReturnRequests(_ context.Context, _ *orderv1.ListReturnRequestsRequest, _ ...grpc.CallOption) (*orderv1.ListReturnRequestsResponse, error) {
	return &orderv1.ListReturnRequestsResponse{ReturnRequests: []*orderv1.ReturnRequest{}, Total: 0}, nil
}

func (o *w3b2OrderClient) GetReturnRequest(_ context.Context, in *orderv1.GetReturnRequestRequest, _ ...grpc.CallOption) (*orderv1.GetReturnRequestResponse, error) {
	return &orderv1.GetReturnRequestResponse{ReturnRequest: &orderv1.ReturnRequest{Id: in.GetReturnRequestId()}}, nil
}

func (o *w3b2OrderClient) AdminResolveReturn(_ context.Context, in *orderv1.AdminResolveReturnRequest, _ ...grpc.CallOption) (*orderv1.AdminResolveReturnResponse, error) {
	return &orderv1.AdminResolveReturnResponse{ReturnRequest: &orderv1.ReturnRequest{Id: in.GetReturnRequestId()}}, nil
}

type w3b2PaymentClient struct {
	paymentv1.PaymentServiceClient
	mu                  sync.Mutex
	methodConfigs       []*paymentv1.PaymentMethodConfig
	lastGetMethodsMD    metadata.MD
	lastUpdateMethodReq *paymentv1.UpdatePaymentMethodConfigRequest
	updateMethodErr     error
	lastListRefundsReq  *paymentv1.ListRefundsRequest
	listRefundsCalls    int
	lastDecideRefundReq *paymentv1.DecideRefundApprovalRequest
	decideRefundErr     error
	lastGetFeeReq       *paymentv1.GetFeeConfigRequest
	lastSetFeeReq       *paymentv1.SetFeeConfigRequest
	payments            map[string]*paymentv1.Payment
}

func (p *w3b2PaymentClient) GetPaymentMethodConfigs(ctx context.Context, in *paymentv1.GetPaymentMethodConfigsRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentMethodConfigsResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastGetMethodsMD = md.Copy()
	out := make([]*paymentv1.PaymentMethodConfig, 0, len(p.methodConfigs))
	for _, c := range p.methodConfigs {
		if in.GetMethod() != paymentv1.PaymentMethod_PAYMENT_METHOD_UNSPECIFIED && c.GetMethod() != in.GetMethod() {
			continue
		}
		out = append(out, c)
	}
	return &paymentv1.GetPaymentMethodConfigsResponse{Configs: out}, nil
}

func (p *w3b2PaymentClient) UpdatePaymentMethodConfig(_ context.Context, in *paymentv1.UpdatePaymentMethodConfigRequest, _ ...grpc.CallOption) (*paymentv1.UpdatePaymentMethodConfigResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastUpdateMethodReq = in
	if p.updateMethodErr != nil {
		return nil, p.updateMethodErr
	}
	updated := &paymentv1.PaymentMethodConfig{
		Method:                in.GetMethod(),
		Enabled:               in.GetEnabled(),
		MinOrderVnd:           in.GetMinOrderVnd(),
		MaxOrderVnd:           in.GetMaxOrderVnd(),
		ExpiryMinutes:         in.GetExpiryMinutes(),
		CredentialsConfigured: true,
		CredentialPreview:     "***5555",
		UpdatedBy:             "admin-1",
		UpdatedAt:             timestamppb.Now(),
		Note:                  in.GetNote(),
		Version:               in.GetExpectedVersion() + 1,
	}
	return &paymentv1.UpdatePaymentMethodConfigResponse{Config: updated}, nil
}

func (p *w3b2PaymentClient) GetPayment(_ context.Context, in *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pay, ok := p.payments[in.GetPaymentId()]; ok {
		return &paymentv1.GetPaymentResponse{Payment: pay}, nil
	}
	return nil, status.Error(codes.NotFound, "payment not found")
}

func (p *w3b2PaymentClient) GetFeeConfig(_ context.Context, in *paymentv1.GetFeeConfigRequest, _ ...grpc.CallOption) (*paymentv1.GetFeeConfigResponse, error) {
	p.mu.Lock()
	p.lastGetFeeReq = in
	p.mu.Unlock()
	return &paymentv1.GetFeeConfigResponse{
		ActiveConfigs: []*paymentv1.PaymentFeeConfigItem{
			{Id: "fc-zp", PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY, FeeBps: 180},
		},
	}, nil
}

func (p *w3b2PaymentClient) SetFeeConfig(_ context.Context, in *paymentv1.SetFeeConfigRequest, _ ...grpc.CallOption) (*paymentv1.SetFeeConfigResponse, error) {
	p.mu.Lock()
	p.lastSetFeeReq = in
	p.mu.Unlock()
	return &paymentv1.SetFeeConfigResponse{
		Config: &paymentv1.PaymentFeeConfigItem{Id: "fc-new", PaymentMethod: in.GetPaymentMethod(), FeeBps: in.GetFeeBps()},
	}, nil
}

func (p *w3b2PaymentClient) GetLedgerSummary(_ context.Context, _ *paymentv1.GetLedgerSummaryRequest, _ ...grpc.CallOption) (*paymentv1.GetLedgerSummaryResponse, error) {
	return &paymentv1.GetLedgerSummaryResponse{IsBalanced: true}, nil
}

func (p *w3b2PaymentClient) CreateRefund(_ context.Context, in *paymentv1.CreateRefundRequest, _ ...grpc.CallOption) (*paymentv1.CreateRefundResponse, error) {
	return &paymentv1.CreateRefundResponse{
		Refund: &paymentv1.Refund{
			RefundId:          "ref-large-1",
			PaymentId:         in.GetPaymentId(),
			OrderId:           in.GetOrderId(),
			AmountVnd:         in.GetAmountVnd(),
			Status:            paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
			Reason:            in.GetReason(),
			IdempotencyKey:    in.GetIdempotencyKey(),
			RequestedBy:       "cskh-1",
			ProviderMRefundId: "261124_2553_0001",
			ProviderStatus:    "processing",
		},
	}, nil
}

func (p *w3b2PaymentClient) ListRefunds(_ context.Context, in *paymentv1.ListRefundsRequest, _ ...grpc.CallOption) (*paymentv1.ListRefundsResponse, error) {
	p.mu.Lock()
	p.listRefundsCalls++
	p.lastListRefundsReq = in
	p.mu.Unlock()
	now := timestamppb.Now()
	return &paymentv1.ListRefundsResponse{
		Refunds: []*paymentv1.Refund{
			{
				RefundId:          "ref-appr-1",
				PaymentId:         "pay-zp-1",
				OrderId:           "ord-1",
				AmountVnd:         6000000,
				Status:            paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL,
				Reason:            "large dispute refund",
				RequestedBy:       "cskh-1",
				ApprovedBy:        "",
				ApprovalNote:      "",
				ProviderMRefundId: "261124_2553_0001",
				ProviderStatus:    "pending",
				CreatedAt:         now,
			},
		},
		Total: 1,
	}, nil
}

func (p *w3b2PaymentClient) DecideRefundApproval(_ context.Context, in *paymentv1.DecideRefundApprovalRequest, _ ...grpc.CallOption) (*paymentv1.DecideRefundApprovalResponse, error) {
	p.mu.Lock()
	p.lastDecideRefundReq = in
	err := p.decideRefundErr
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	st := paymentv1.RefundStatus_REFUND_STATUS_REJECTED
	if in.GetApprove() {
		st = paymentv1.RefundStatus_REFUND_STATUS_PENDING
	}
	return &paymentv1.DecideRefundApprovalResponse{
		Refund: &paymentv1.Refund{
			RefundId:          in.GetRefundId(),
			PaymentId:         "pay-zp-1",
			OrderId:           "ord-1",
			AmountVnd:         6000000,
			Status:            st,
			RequestedBy:       "cskh-1",
			ApprovedBy:        "acc-1",
			ApprovedAt:        timestamppb.Now(),
			ApprovalNote:      in.GetNote(),
			ProviderMRefundId: "261124_2553_0002",
			ProviderStatus:    "processing",
		},
	}, nil
}

func (p *w3b2PaymentClient) SettleRefund(_ context.Context, in *paymentv1.SettleRefundRequest, _ ...grpc.CallOption) (*paymentv1.SettleRefundResponse, error) {
	return &paymentv1.SettleRefundResponse{Refund: &paymentv1.Refund{RefundId: in.GetRefundId(), Status: paymentv1.RefundStatus_REFUND_STATUS_DONE}}, nil
}

func (p *w3b2PaymentClient) ListSellerPayouts(_ context.Context, in *paymentv1.ListSellerPayoutsRequest, _ ...grpc.CallOption) (*paymentv1.ListSellerPayoutsResponse, error) {
	return &paymentv1.ListSellerPayoutsResponse{
		Items:    []*paymentv1.SellerPayout{},
		Page:     in.GetPage(),
		PageSize: in.GetPageSize(),
		Total:    0,
	}, nil
}

func (p *w3b2PaymentClient) DecideSellerPayout(_ context.Context, in *paymentv1.DecideSellerPayoutRequest, _ ...grpc.CallOption) (*paymentv1.DecideSellerPayoutResponse, error) {
	now := timestamppb.Now()
	st := "COMPLETED"
	ref := "MOCKBANK-test"
	if strings.EqualFold(in.GetDecision(), "REJECT") {
		st = "REJECTED"
		ref = ""
	}
	return &paymentv1.DecideSellerPayoutResponse{
		Payout: &paymentv1.SellerPayout{
			Id:                in.GetPayoutId(),
			ShopId:            "shop-1",
			AmountVnd:         100000,
			Status:            st,
			BankName:          "Vietcombank",
			BankAccountMasked: "****0123",
			AccountHolderName: "NGUYEN VAN A",
			RejectReason:      in.GetReason(),
			TransferRef:       ref,
			RequestedBy:       "seller-1",
			DecidedBy:         in.GetDecidedByUserId(),
			CreatedAt:         now,
			DecidedAt:         now,
		},
	}, nil
}

func (p *w3b2PaymentClient) RunReconciliation(_ context.Context, _ *paymentv1.RunReconciliationRequest, _ ...grpc.CallOption) (*paymentv1.RunReconciliationResponse, error) {
	return &paymentv1.RunReconciliationResponse{RunId: "run-1"}, nil
}

func (p *w3b2PaymentClient) ImportCodReconciliation(_ context.Context, _ *paymentv1.ImportCodReconciliationRequest, _ ...grpc.CallOption) (*paymentv1.ImportCodReconciliationResponse, error) {
	return &paymentv1.ImportCodReconciliationResponse{Run: &paymentv1.ReconciliationRunSummary{RunId: "run-cod-1"}}, nil
}

func (p *w3b2PaymentClient) ListReconciliationRuns(_ context.Context, _ *paymentv1.ListReconciliationRunsRequest, _ ...grpc.CallOption) (*paymentv1.ListReconciliationRunsResponse, error) {
	return &paymentv1.ListReconciliationRunsResponse{Runs: []*paymentv1.ReconciliationRunSummary{}, Total: 0}, nil
}

func (p *w3b2PaymentClient) ListReconciliationQueue(_ context.Context, _ *paymentv1.ListReconciliationQueueRequest, _ ...grpc.CallOption) (*paymentv1.ListReconciliationQueueResponse, error) {
	return &paymentv1.ListReconciliationQueueResponse{Items: []*paymentv1.ReconciliationQueueItem{}, Total: 0}, nil
}

func (p *w3b2PaymentClient) ResolveReconciliationItem(_ context.Context, in *paymentv1.ResolveReconciliationItemRequest, _ ...grpc.CallOption) (*paymentv1.ResolveReconciliationItemResponse, error) {
	return &paymentv1.ResolveReconciliationItemResponse{Item: &paymentv1.ReconciliationQueueItem{Id: in.GetItemId()}}, nil
}

type w3b2ShippingClient struct {
	shippingv1.ShippingServiceClient
	mu               sync.Mutex
	getShipmentCalls int
}

func (s *w3b2ShippingClient) GetShipment(_ context.Context, in *shippingv1.GetShipmentRequest, _ ...grpc.CallOption) (*shippingv1.GetShipmentResponse, error) {
	s.mu.Lock()
	s.getShipmentCalls++
	s.mu.Unlock()
	return &shippingv1.GetShipmentResponse{
		Shipment: &shippingv1.Shipment{ShipmentId: "ship-1", OrderId: in.GetOrderId(), ShopId: "shop-1"},
	}, nil
}
func (s *w3b2ShippingClient) GetShopShippingSettings(_ context.Context, in *shippingv1.GetShopShippingSettingsRequest, _ ...grpc.CallOption) (*shippingv1.GetShopShippingSettingsResponse, error) {
	return &shippingv1.GetShopShippingSettingsResponse{Settings: &shippingv1.ShopShippingSettings{ShopId: in.GetShopId(), DefaultWeightGrams: 500}}, nil
}
func (s *w3b2ShippingClient) CalculateFee(_ context.Context, _ *shippingv1.CalculateFeeRequest, _ ...grpc.CallOption) (*shippingv1.CalculateFeeResponse, error) {
	return &shippingv1.CalculateFeeResponse{Quotes: []*shippingv1.CarrierQuote{{CarrierCode: "ghn", FeeVnd: 20000}}}, nil
}
func (s *w3b2ShippingClient) AdminListCarriers(_ context.Context, _ *shippingv1.AdminListCarriersRequest, _ ...grpc.CallOption) (*shippingv1.AdminListCarriersResponse, error) {
	return &shippingv1.AdminListCarriersResponse{Carriers: []*shippingv1.CarrierConfig{}}, nil
}
func (s *w3b2ShippingClient) AdminUpdateCarrier(_ context.Context, in *shippingv1.AdminUpdateCarrierRequest, _ ...grpc.CallOption) (*shippingv1.AdminUpdateCarrierResponse, error) {
	return &shippingv1.AdminUpdateCarrierResponse{Carrier: &shippingv1.CarrierConfig{CarrierCode: in.GetCarrierCode(), Enabled: in.GetEnabled()}}, nil
}
func (s *w3b2ShippingClient) AdminTestCarrier(_ context.Context, _ *shippingv1.AdminTestCarrierRequest, _ ...grpc.CallOption) (*shippingv1.AdminTestCarrierResponse, error) {
	return &shippingv1.AdminTestCarrierResponse{Ok: true}, nil
}

type w3b2SearchClient struct {
	searchv1.SearchServiceClient
}

func (s *w3b2SearchClient) ReindexAll(_ context.Context, _ *searchv1.ReindexAllRequest, _ ...grpc.CallOption) (*searchv1.ReindexAllResponse, error) {
	return &searchv1.ReindexAllResponse{JobId: "reindex-1", Status: searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_RUNNING}, nil
}

func (s *w3b2SearchClient) GetReindexStatus(_ context.Context, in *searchv1.GetReindexStatusRequest, _ ...grpc.CallOption) (*searchv1.GetReindexStatusResponse, error) {
	return &searchv1.GetReindexStatusResponse{JobId: in.GetJobId(), Status: searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED}, nil
}

type w3b2Harness struct {
	handler    http.Handler
	authMgr    *auth.Manager
	user       *w3b2UserClient
	product    *w3b2ProductClient
	promotion  *w3b2PromotionClient
	order      *w3b2OrderClient
	payment    *w3b2PaymentClient
	shipping   *w3b2ShippingClient
	search     *w3b2SearchClient
	webhookSrv *httptest.Server
	labelSrv   *httptest.Server
	lastWHPath string
	lastWHBody []byte
	lastWHTP   string
	lastLblHdr http.Header
	whMu       sync.Mutex
}

func newW3B2Harness(t *testing.T, webhookLimit int) *w3b2Harness {
	t.Helper()
	authMgr, err := auth.NewManager("w3b2-gateway-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}
	now := timestamppb.Now()
	hw := &w3b2Harness{
		authMgr:   authMgr,
		user:      &w3b2UserClient{},
		product:   &w3b2ProductClient{},
		promotion: &w3b2PromotionClient{},
		order:     &w3b2OrderClient{},
		payment: &w3b2PaymentClient{
			methodConfigs: []*paymentv1.PaymentMethodConfig{
				{
					Method:                paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
					Enabled:               true,
					MinOrderVnd:           0,
					MaxOrderVnd:           5000000,
					ExpiryMinutes:         0,
					CredentialsConfigured: false,
					CredentialPreview:     "",
					UpdatedBy:             "seed",
					UpdatedAt:             now,
					Note:                  "internal cod note",
					Version:               1,
				},
				{
					Method:                paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
					Enabled:               true,
					MinOrderVnd:           10000,
					MaxOrderVnd:           0,
					ExpiryMinutes:         15,
					CredentialsConfigured: true,
					CredentialPreview:     "***8888",
					UpdatedBy:             "admin-1",
					UpdatedAt:             now,
					Note:                  "internal vietqr note",
					Version:               2,
				},
				{
					Method:                paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY,
					Enabled:               true,
					MinOrderVnd:           10000,
					MaxOrderVnd:           50000000,
					ExpiryMinutes:         15,
					CredentialsConfigured: true,
					CredentialPreview:     "***2553",
					UpdatedBy:             "ops-1",
					UpdatedAt:             now,
					Note:                  "internal zalopay note",
					Version:               3,
				},
			},
			payments: map[string]*paymentv1.Payment{
				"pay-zp-1": {
					PaymentId:    "pay-zp-1",
					OrderGroupId: "grp-1",
					UserId:       "buyer-1",
					AmountVnd:    250000,
					Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY,
					Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
					PayUrl:       "https://sbgateway.zalopay.vn/openinapp?order=pay1",
					Provider:     "zalopay_mock",
					ProviderRef:  "261124_pay1",
					ShopAllocations: []*paymentv1.ShopAllocation{
						{OrderId: "ord-1", ShopId: "shop-1", SubtotalVnd: 230000, CommissionVnd: 12000, ShippingFeeVnd: 20000},
						{OrderId: "ord-2", ShopId: "shop-2", SubtotalVnd: 100000, CommissionVnd: 5000, ShippingFeeVnd: 10000},
					},
				},
				"pay-zp-new": {
					PaymentId:    "pay-zp-new",
					OrderGroupId: "grp-1",
					UserId:       "buyer-1",
					AmountVnd:    250000,
					Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY,
					Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
					PayUrl:       "https://sbgateway.zalopay.vn/openinapp?order=retry999",
					ShopAllocations: []*paymentv1.ShopAllocation{
						{OrderId: "ord-1", ShopId: "shop-1", SubtotalVnd: 230000, CommissionVnd: 15000, ShippingFeeVnd: 20000},
					},
				},
				"pay-cod-new": {
					PaymentId:    "pay-cod-new",
					OrderGroupId: "grp-1",
					UserId:       "buyer-1",
					AmountVnd:    250000,
					Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_COD,
					Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
					ShopAllocations: []*paymentv1.ShopAllocation{
						{OrderId: "ord-1", ShopId: "shop-1", SubtotalVnd: 230000, CommissionVnd: 10000, ShippingFeeVnd: 20000},
					},
				},
			},
		},
		shipping: &w3b2ShippingClient{},
		search:   &w3b2SearchClient{},
	}

	hw.webhookSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/payments/") && strings.HasSuffix(r.URL.Path, "/events") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: payment.pending\ndata: {}\n\n"))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		hw.whMu.Lock()
		hw.lastWHPath = r.URL.Path
		hw.lastWHBody = append([]byte(nil), raw...)
		hw.lastWHTP = r.Header.Get("traceparent")
		hw.whMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"return_code":1,"return_message":"success"}`))
	}))
	t.Cleanup(hw.webhookSrv.Close)

	hw.labelSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hw.whMu.Lock()
		hw.lastLblHdr = r.Header.Clone()
		hw.whMu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>label</html>"))
	}))
	t.Cleanup(hw.labelSrv.Close)

	log := logger.New("w3b2-test", logger.WithWriter(io.Discard))
	wp, _ := proxy.NewWebhookProxy(hw.webhookSrv.URL, log)
	sp, _ := proxy.NewSSEProxy(hw.webhookSrv.URL, log)
	lp, _ := proxy.NewShippingLabelProxy(hw.labelSrv.URL, log)

	var lim *w2aRateLimiter
	if webhookLimit > 0 {
		lim = newW2ARateLimiter()
	}

	hw.handler = New(Dependencies{
		ServiceName: "gateway-w3b2-test",
		Clients: &clients.Clients{
			User:      hw.user,
			Product:   hw.product,
			Promotion: hw.promotion,
			Order:     hw.order,
			Payment:   hw.payment,
			Shipping:  hw.shipping,
			Search:    hw.search,
			Timeout:   5 * time.Second,
		},
		AuthManager:            authMgr,
		RateLimiter:            lim,
		RateLimitWebhookPerMin: webhookLimit,
		PaymentWebhookProxy:    wp,
		ShippingWebhookProxy:   wp,
		ShippingLabelProxy:     lp,
		PaymentSSEProxy:        sp,
		Logger:                 log,
	})
	return hw
}

func (hw *w3b2Harness) token(role string) string {
	shopID := ""
	if role == auth.RoleSeller {
		shopID = "shop-1"
	}
	tok, _, _, _ := hw.authMgr.SignAccessToken("user-"+role, role, shopID)
	return tok
}

func (hw *w3b2Harness) do(method, target, token, contentType, body string, headers map[string]string) *httptest.ResponseRecorder {
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
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	hw.handler.ServeHTTP(rec, req)
	return rec
}

func TestW3B2_AdminRBACMatrixExhaustiveAllRoutesAndRoles(t *testing.T) {
	hw := newW3B2Harness(t, 0)

	type adminRouteCase struct {
		method      string
		pattern     string
		samplePath  string
		capability  string
		contentType string
		body        string
		headers     map[string]string
	}

	idemHdr := map[string]string{"Idempotency-Key": "idem-rbac-1"}
	validCODCSV := "tracking_code,cod_amount_vnd,delivered_at\nTRK-1,100000,2026-11-24\n"

	cases := []adminRouteCase{
		// Section 4.1 User / Staff / Shop / Approvals / Audit
		{http.MethodGet, "/api/v1/admin/users", "/api/v1/admin/users", auth.CapUsersRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/staff", "/api/v1/admin/staff", auth.CapStaffCreate, "", `{"full_name":"Staff A","email":"staff@example.com","phone":"0901234567","role":"ops"}`, nil},
		{http.MethodGet, "/api/v1/admin/users/{id}/activities", "/api/v1/admin/users/u-1/activities", auth.CapUsersRead, "", "", nil},
		{http.MethodPatch, "/api/v1/admin/users/{id}/status", "/api/v1/admin/users/u-1/status", auth.CapUsersStatusWrite, "", `{"status":"LOCKED","reason":"abuse"}`, nil},
		{http.MethodPatch, "/api/v1/admin/users/{id}/role", "/api/v1/admin/users/u-1/role", auth.CapUsersRoleWrite, "", `{"role":"moderator","reason":"promote"}`, nil},
		{http.MethodGet, "/api/v1/admin/shops", "/api/v1/admin/shops", auth.CapShopsRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/shops/{id}/review", "/api/v1/admin/shops/s-1/review", auth.CapShopsReview, "", `{"decision":"APPROVE","reason":"ok"}`, nil},
		{http.MethodPost, "/api/v1/admin/shops/{id}/suspend", "/api/v1/admin/shops/s-1/suspend", auth.CapShopsSuspendRequest, "", `{"reason":"policy violation"}`, nil},
		{http.MethodPost, "/api/v1/admin/shops/{id}/unsuspend", "/api/v1/admin/shops/s-1/unsuspend", auth.CapShopsUnsuspend, "", `{"reason":"remediated"}`, nil},
		{http.MethodGet, "/api/v1/admin/shops/{id}/bank-account", "/api/v1/admin/shops/s-1/bank-account", auth.CapShopsBankRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/shops/{id}/bank-account/review", "/api/v1/admin/shops/s-1/bank-account/review", auth.CapShopsBankReview, "", `{"bank_account_id":"ba-1","decision":"APPROVE","reason":"ok"}`, nil},
		{http.MethodGet, "/api/v1/admin/sites", "/api/v1/admin/sites", auth.CapSitesRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/sites/{shop_id}", "/api/v1/admin/sites/s-1", auth.CapSitesRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/sites/{shop_id}/domains", "/api/v1/admin/sites/s-1/domains", auth.CapSitesRead, "", "", nil},
		{http.MethodDelete, "/api/v1/admin/sites/{shop_id}/domains/{id}", "/api/v1/admin/sites/s-1/domains/dom-1", auth.CapSitesDomainsWrite, "", "", nil},
		{http.MethodPost, "/api/v1/admin/sites/{site_id}/suspend", "/api/v1/admin/sites/site-1/suspend", auth.CapSitesSuspend, "", `{"suspend":true,"reason":"policy"}`, nil},
		{http.MethodGet, "/api/v1/admin/approvals", "/api/v1/admin/approvals", auth.CapApprovalsRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/approvals/shop/{id}/decide", "/api/v1/admin/approvals/shop/appr-1/decide", auth.CapShopsSuspendDecide, "", `{"decision":"APPROVE","note":"verified"}`, nil},
		{http.MethodPost, "/api/v1/admin/approvals/refund/{id}/decide", "/api/v1/admin/approvals/refund/ref-1/decide", auth.CapRefundsApprove, "", `{"approve":true,"note":"verified"}`, nil},
		{http.MethodGet, "/api/v1/admin/audit-logs", "/api/v1/admin/audit-logs", auth.CapAuditLogsRead, "", "", nil},

		// Section 4.2 Categories / Fees / Products / Banned Keywords
		{http.MethodGet, "/api/v1/admin/categories", "/api/v1/admin/categories", auth.CapCategoriesRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/categories", "/api/v1/admin/categories", auth.CapCategoriesWrite, "", `{"name":"Ao","slug":"ao"}`, nil},
		{http.MethodPut, "/api/v1/admin/categories/{id}", "/api/v1/admin/categories/cat-1", auth.CapCategoriesWrite, "", `{"name":"Ao"}`, nil},
		{http.MethodDelete, "/api/v1/admin/categories/{id}", "/api/v1/admin/categories/cat-1", auth.CapCategoriesWrite, "", "", nil},
		{http.MethodGet, "/api/v1/admin/fees/categories", "/api/v1/admin/fees/categories", auth.CapFeesCategoryRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/fees/categories/{id}", "/api/v1/admin/fees/categories/cat-1", auth.CapFeesCategoryWrite, "", `{"commission_rate_bps":500}`, nil},
		{http.MethodGet, "/api/v1/admin/products", "/api/v1/admin/products", auth.CapProductsRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/products/{id}", "/api/v1/admin/products/p-1", auth.CapProductsRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/products/{id}/review", "/api/v1/admin/products/p-1/review", auth.CapProductsReview, "", `{"decision":"APPROVE"}`, nil},
		{http.MethodGet, "/api/v1/admin/banned-keywords", "/api/v1/admin/banned-keywords", auth.CapBannedKeywordsRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/banned-keywords", "/api/v1/admin/banned-keywords", auth.CapBannedKeywordsWrite, "", `{"term":"hang gia","action":"BLOCK"}`, nil},
		{http.MethodPut, "/api/v1/admin/banned-keywords/{id}", "/api/v1/admin/banned-keywords/kw-1", auth.CapBannedKeywordsWrite, "", `{"action":"REVIEW"}`, nil},
		{http.MethodDelete, "/api/v1/admin/banned-keywords/{id}", "/api/v1/admin/banned-keywords/kw-1", auth.CapBannedKeywordsWrite, "", "", nil},
		{http.MethodPost, "/api/v1/admin/banned-keywords/preview", "/api/v1/admin/banned-keywords/preview", auth.CapBannedKeywordsRead, "", `{"term":"hang gia"}`, nil},
		{http.MethodPost, "/api/v1/admin/banned-keywords/rescan", "/api/v1/admin/banned-keywords/rescan", auth.CapBannedKeywordsWrite, "", `{"dry_run":true}`, nil},

		// Section 4.4 Vouchers / Flash Sales / Coins
		{http.MethodGet, "/api/v1/admin/vouchers", "/api/v1/admin/vouchers", auth.CapVouchersRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/vouchers", "/api/v1/admin/vouchers", auth.CapVouchersWrite, "", `{"code":"V1","name":"V","discount_type":"FIXED","value_vnd":10000,"quota":10}`, nil},
		{http.MethodPut, "/api/v1/admin/vouchers/{id}", "/api/v1/admin/vouchers/v-1", auth.CapVouchersWrite, "", `{"name":"V2"}`, nil},
		{http.MethodPatch, "/api/v1/admin/vouchers/{id}/status", "/api/v1/admin/vouchers/v-1/status", auth.CapVouchersWrite, "", `{"status":"PAUSED"}`, nil},
		{http.MethodDelete, "/api/v1/admin/vouchers/{id}", "/api/v1/admin/vouchers/v-1", auth.CapVouchersWrite, "", "", nil},
		{http.MethodGet, "/api/v1/admin/flash-sales", "/api/v1/admin/flash-sales", auth.CapFlashSalesRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/flash-sales", "/api/v1/admin/flash-sales", auth.CapFlashSalesWrite, "", `{"name":"FS"}`, nil},
		{http.MethodPost, "/api/v1/admin/flash-sales/{id}/end", "/api/v1/admin/flash-sales/fs-1/end", auth.CapFlashSalesWrite, "", "", nil},
		{http.MethodGet, "/api/v1/admin/flash-sale-items", "/api/v1/admin/flash-sale-items", auth.CapFlashSalesRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/flash-sale-items/{item_id}/approve", "/api/v1/admin/flash-sale-items/fsi-1/approve", auth.CapFlashSalesItemApprove, "", `{"approve":true}`, nil},
		{http.MethodPost, "/api/v1/admin/coins/credit", "/api/v1/admin/coins/credit", auth.CapCoinsCredit, "", `{"user_id":"u-1","amount":1000,"reason":"bonus"}`, nil},

		// Section 4.5 Orders / Reports / Returns
		{http.MethodGet, "/api/v1/admin/orders", "/api/v1/admin/orders", auth.CapOrdersRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/orders/needs-review", "/api/v1/admin/orders/needs-review", auth.CapOrdersRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/orders/{id}/resolve-review", "/api/v1/admin/orders/ord-1/resolve-review", auth.CapOrdersReviewResolve, "", `{"note":"ok"}`, nil},
		{http.MethodGet, "/api/v1/admin/reports", "/api/v1/admin/reports", auth.CapReportsRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/reports/export", "/api/v1/admin/reports/export", auth.CapReportsRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/returns", "/api/v1/admin/returns", auth.CapReturnsRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/returns/{id}", "/api/v1/admin/returns/ret-1", auth.CapReturnsRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/returns/{id}/resolve", "/api/v1/admin/returns/ret-1/resolve", auth.CapReturnsResolve, "", `{"decision":"REFUND","return_fee_bearer":"SELLER"}`, nil},

		// Section 4.6 Payment Methods / Fees / Ledger / Refunds / Reconciliation
		{http.MethodGet, "/api/v1/admin/payment-methods", "/api/v1/admin/payment-methods", auth.CapPaymentMethodsRead, "", "", nil},
		{http.MethodPut, "/api/v1/admin/payment-methods/{method}", "/api/v1/admin/payment-methods/ZALOPAY", auth.CapPaymentMethodsWrite, "", `{"enabled":true,"min_order_vnd":10000,"max_order_vnd":50000000,"expiry_minutes":15,"expected_version":3}`, nil},
		{http.MethodGet, "/api/v1/admin/fees/payment", "/api/v1/admin/fees/payment", auth.CapFeesPaymentRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/fees/payment", "/api/v1/admin/fees/payment", auth.CapFeesPaymentWrite, "", `{"payment_method":"ZALOPAY","fee_bps":180}`, nil},
		{http.MethodGet, "/api/v1/admin/ledger/summary", "/api/v1/admin/ledger/summary", auth.CapLedgerRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/payments/{id}/refund", "/api/v1/admin/payments/pay-1/refund", auth.CapRefundsCreate, "", `{"order_id":"ord-1","amount_vnd":100000,"reason":"refund"}`, idemHdr},
		{http.MethodGet, "/api/v1/admin/refunds", "/api/v1/admin/refunds", auth.CapRefundsRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/refunds/{id}/settle", "/api/v1/admin/refunds/ref-1/settle", auth.CapRefundsSettle, "", `{"provider_refund_id":"pr-1"}`, idemHdr},
		{http.MethodGet, "/api/v1/admin/payouts", "/api/v1/admin/payouts", auth.CapPayoutsRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/payouts/{id}/decision", "/api/v1/admin/payouts/po-1/decision", auth.CapPayoutsDecide, "", `{"decision":"APPROVE","reason":""}`, nil},
		{http.MethodPost, "/api/v1/admin/payments/reconcile", "/api/v1/admin/payments/reconcile", auth.CapReconciliationWrite, "", `{"date":"2026-11-24"}`, nil},
		{http.MethodPost, "/api/v1/admin/reconciliation/cod/import", "/api/v1/admin/reconciliation/cod/import?carrier_code=ghn", auth.CapReconciliationWrite, "text/csv", validCODCSV, nil},
		{http.MethodGet, "/api/v1/admin/reconciliation/runs", "/api/v1/admin/reconciliation/runs", auth.CapLedgerRead, "", "", nil},
		{http.MethodGet, "/api/v1/admin/reconciliation/queue", "/api/v1/admin/reconciliation/queue", auth.CapLedgerRead, "", "", nil},
		{http.MethodPost, "/api/v1/admin/reconciliation/queue/{id}/resolve", "/api/v1/admin/reconciliation/queue/rq-1/resolve", auth.CapReconciliationWrite, "", `{"action":"SUSPEND"}`, idemHdr},

		// Section 4.7 Carriers
		{http.MethodGet, "/api/v1/admin/carriers", "/api/v1/admin/carriers", auth.CapCarriersRead, "", "", nil},
		{http.MethodPut, "/api/v1/admin/carriers/{code}", "/api/v1/admin/carriers/ghn", auth.CapCarriersWrite, "", `{"enabled":true}`, nil},
		{http.MethodPost, "/api/v1/admin/carriers/{code}/test", "/api/v1/admin/carriers/ghn/test", auth.CapCarriersWrite, "", "", nil},

		// Section 4.8a Search Reindex (B1 / G11)
		{http.MethodPost, "/api/v1/admin/search/reindex", "/api/v1/admin/search/reindex", auth.CapSearchReindex, "", `{"batch_size":200}`, nil},
		{http.MethodGet, "/api/v1/admin/search/reindex/{job_id}", "/api/v1/admin/search/reindex/reindex-1", auth.CapSearchReindex, "", "", nil},
	}

	// 1. Verify EVERY /api/v1/admin/* route registered in chi router is present in the test matrix.
	covered := make(map[string]bool, len(cases))
	for _, tc := range cases {
		covered[tc.method+" "+tc.pattern] = true
	}
	chiRoutes, ok := hw.handler.(chi.Routes)
	if !ok {
		t.Fatalf("router handler does not implement chi.Routes")
	}
	if err := chi.Walk(chiRoutes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/v1/admin/") {
			key := method + " " + route
			if !covered[key] {
				t.Errorf("registered admin route %q is missing from RBAC matrix test", key)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	// 2. Test EVERY admin route × 6 staff roles + buyer + seller + anonymous.
	allRoles := []string{
		auth.RoleSuperAdmin,
		auth.RoleAdmin,
		auth.RoleModerator,
		auth.RoleCSKH,
		auth.RoleAccountant,
		auth.RoleOps,
		auth.RoleBuyer,
		auth.RoleSeller,
	}
	tokens := make(map[string]string, len(allRoles))
	for _, r := range allRoles {
		tokens[r] = hw.token(r)
	}

	for _, tc := range cases {
		t.Run(tc.method+"_"+tc.pattern, func(t *testing.T) {
			// Anonymous must be 401.
			recAnon := hw.do(tc.method, tc.samplePath, "", tc.contentType, tc.body, tc.headers)
			if recAnon.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous %s %s: want 401, got %d (%s)", tc.method, tc.samplePath, recAnon.Code, recAnon.Body.String())
			}

			for _, role := range allRoles {
				wantAllowed := auth.RoleHasCapability(role, tc.capability)
				rec := hw.do(tc.method, tc.samplePath, tokens[role], tc.contentType, tc.body, tc.headers)
				if wantAllowed {
					if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
						t.Errorf("role=%s (allowed by %s) %s %s: got %d (%s), expected allowed 2xx",
							role, tc.capability, tc.method, tc.samplePath, rec.Code, rec.Body.String())
					}
				} else {
					if rec.Code != http.StatusForbidden {
						t.Errorf("role=%s (denied by %s) %s %s: want 403, got %d (%s)",
							role, tc.capability, tc.method, tc.samplePath, rec.Code, rec.Body.String())
					}
				}
			}
		})
	}
}

func TestW3B2_A6_ZaloPayWebhookPaymentMethodsRetryAndFees(t *testing.T) {
	hw := newW3B2Harness(t, 2)
	buyerTok := hw.token(auth.RoleBuyer)
	sellerTok := hw.token(auth.RoleSeller)
	adminTok := hw.token(auth.RoleAdmin)
	opsTok := hw.token(auth.RoleOps)

	// 1. POST /webhooks/zalopay: public, preserves exact raw bytes & traceparent, enforces webhook rate limit.
	rawPayload := []byte(`{  "data" : "{\"app_id\":2553,\"app_trans_id\":\"261124_abc\",\"amount\":250000}",  "mac":"deadbeef", "type":1 }`)
	const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/zalopay", bytes.NewReader(rawPayload))
		req.RemoteAddr = "198.51.100.77:12345"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("traceparent", tp)
		rec := httptest.NewRecorder()
		hw.handler.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("POST /webhooks/zalopay req %d: want %d, got %d (%s)", i, want, rec.Code, rec.Body.String())
		}
	}
	hw.whMu.Lock()
	gotPath, gotBody, gotTP := hw.lastWHPath, hw.lastWHBody, hw.lastWHTP
	hw.whMu.Unlock()
	if gotPath != "/webhooks/zalopay" || !bytes.Equal(gotBody, rawPayload) || gotTP != tp {
		t.Fatalf("zalopay webhook proxy mismatch: path=%q tp=%q body=%q", gotPath, gotTP, string(gotBody))
	}

	// 2. GET /api/v1/payment-methods (public & optional-auth with admin token): NEVER leaks internal admin fields.
	for _, tok := range []string{"", adminTok} {
		rec := hw.do(http.MethodGet, "/api/v1/payment-methods", tok, "", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/payment-methods (tok=%v): want 200, got %d (%s)", tok != "", rec.Code, rec.Body.String())
		}
		bodyStr := rec.Body.String()
		for _, forbidden := range []string{"credentials_configured", "credential_preview", "updated_by", "updated_at", `"note"`, "***2553", "internal zalopay note"} {
			if strings.Contains(bodyStr, forbidden) {
				t.Fatalf("GET /api/v1/payment-methods leaked %q: %s", forbidden, bodyStr)
			}
		}
		var wrap struct {
			Methods []map[string]any `json:"methods"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil || len(wrap.Methods) != 3 {
			t.Fatalf("GET /api/v1/payment-methods expected 3 methods, got err=%v body=%s", err, bodyStr)
		}
		if vals := hw.payment.lastGetMethodsMD.Get(auth.MDAuthorization); len(vals) != 0 {
			t.Fatalf("GET /api/v1/payment-methods must not forward Authorization metadata, got %v", vals)
		}
	}

	// 3. GET /api/v1/admin/payment-methods & PUT /api/v1/admin/payment-methods/{method} (F7 optimistic lock + audit before/after).
	recAdminPM := hw.do(http.MethodGet, "/api/v1/admin/payment-methods", opsTok, "", "", nil)
	if recAdminPM.Code != http.StatusOK || !strings.Contains(recAdminPM.Body.String(), `"credential_preview":"***2553"`) || !strings.Contains(recAdminPM.Body.String(), `"version":3`) {
		t.Fatalf("GET /api/v1/admin/payment-methods mismatch: code=%d body=%s", recAdminPM.Code, recAdminPM.Body.String())
	}

	// Missing expected_version -> 400.
	recNoVer := hw.do(http.MethodPut, "/api/v1/admin/payment-methods/ZALOPAY", opsTok, "", `{"enabled":true,"min_order_vnd":10000,"max_order_vnd":50000000,"expiry_minutes":15}`, nil)
	if recNoVer.Code != http.StatusBadRequest {
		t.Fatalf("PUT /admin/payment-methods missing expected_version: want 400, got %d (%s)", recNoVer.Code, recNoVer.Body.String())
	}

	// Version conflict from payment -> 409 VERSION_CONFLICT.
	hw.payment.mu.Lock()
	hw.payment.updateMethodErr = status.Error(codes.FailedPrecondition, "VERSION_CONFLICT: stale version")
	hw.payment.mu.Unlock()
	recConflict := hw.do(http.MethodPut, "/api/v1/admin/payment-methods/ZALOPAY", opsTok, "", `{"enabled":true,"min_order_vnd":10000,"max_order_vnd":50000000,"expiry_minutes":15,"expected_version":1}`, nil)
	if recConflict.Code != http.StatusConflict || !strings.Contains(recConflict.Body.String(), `"code":"VERSION_CONFLICT"`) {
		t.Fatalf("PUT /admin/payment-methods version conflict: want 409 VERSION_CONFLICT, got %d (%s)", recConflict.Code, recConflict.Body.String())
	}
	hw.payment.mu.Lock()
	hw.payment.updateMethodErr = nil
	hw.payment.mu.Unlock()

	// Valid update -> 200 + audit record contains before_json AND after_json.
	hw.user.mu.Lock()
	hw.user.auditReqs = nil
	hw.user.mu.Unlock()
	recUpd := hw.do(http.MethodPut, "/api/v1/admin/payment-methods/ZALOPAY", opsTok, "", `{"enabled":false,"min_order_vnd":20000,"max_order_vnd":20000000,"expiry_minutes":10,"note":"maintenance","expected_version":3}`, nil)
	if recUpd.Code != http.StatusOK || !strings.Contains(recUpd.Body.String(), `"version":4`) {
		t.Fatalf("PUT /admin/payment-methods/ZALOPAY: want 200 version=4, got %d (%s)", recUpd.Code, recUpd.Body.String())
	}
	hw.user.mu.Lock()
	audits := append([]*userv1.WriteAuditLogRequest(nil), hw.user.auditReqs...)
	hw.user.mu.Unlock()
	if len(audits) != 1 {
		t.Fatalf("expected 1 audit log for PUT /admin/payment-methods/ZALOPAY, got %d", len(audits))
	}
	if !strings.Contains(audits[0].GetBeforeJson(), `"version":3`) || !strings.Contains(audits[0].GetBeforeJson(), `"enabled":true`) {
		t.Fatalf("expected before_json with previous config (version 3, enabled true), got %s", audits[0].GetBeforeJson())
	}
	if !strings.Contains(audits[0].GetAfterJson(), `"version":4`) || !strings.Contains(audits[0].GetAfterJson(), `"enabled":false`) {
		t.Fatalf("expected after_json with updated config (version 4, enabled false), got %s", audits[0].GetAfterJson())
	}

	// 4. POST /api/v1/checkout with ZALOPAY returns pay_url; domain 409 error codes pass through.
	recCheckout := hw.do(http.MethodPost, "/api/v1/checkout", buyerTok, "", `{"address_id":"addr-1","from_cart":true,"payment_method":"ZALOPAY","sales_channel":"MARKETPLACE"}`, map[string]string{"Idempotency-Key": "idem-zp-1"})
	if recCheckout.Code != http.StatusCreated ||
		!strings.Contains(recCheckout.Body.String(), `"payment_method":"ZALOPAY"`) ||
		!strings.Contains(recCheckout.Body.String(), `"pay_url":"https://sbgateway.zalopay.vn/openinapp?order=mock123"`) {
		t.Fatalf("POST /checkout ZALOPAY mismatch: code=%d body=%s", recCheckout.Code, recCheckout.Body.String())
	}
	for _, errCode := range []string{"SHOP_SUSPENDED", "PAYMENT_METHOD_DISABLED", "ORDER_AMOUNT_OUT_OF_RANGE"} {
		hw.order.mu.Lock()
		hw.order.checkoutErr = status.Error(codes.FailedPrecondition, errCode+": blocked")
		hw.order.mu.Unlock()
		recErr := hw.do(http.MethodPost, "/api/v1/checkout", buyerTok, "", `{"address_id":"addr-1","payment_method":"ZALOPAY","sales_channel":"MARKETPLACE"}`, map[string]string{"Idempotency-Key": "idem-err-" + errCode})
		if recErr.Code != http.StatusConflict || !strings.Contains(recErr.Body.String(), `"code":"`+errCode+`"`) {
			t.Fatalf("checkout %s: want 409 exact code, got %d (%s)", errCode, recErr.Code, recErr.Body.String())
		}
	}
	hw.order.mu.Lock()
	hw.order.checkoutErr = nil
	hw.order.mu.Unlock()

	// 5. POST /api/v1/payments/{id}/retry: Idempotency-Key mandatory; ZALOPAY & COD switching work and return pay_url, switched_to_cod, orders.
	recRetryNoIdem := hw.do(http.MethodPost, "/api/v1/payments/pay-zp-1/retry", buyerTok, "", `{"method":"ZALOPAY"}`, nil)
	if recRetryNoIdem.Code != http.StatusBadRequest || !strings.Contains(recRetryNoIdem.Body.String(), `"code":"MISSING_IDEMPOTENCY_KEY"`) {
		t.Fatalf("POST /payments/{id}/retry without Idempotency-Key: want 400 MISSING_IDEMPOTENCY_KEY, got %d (%s)", recRetryNoIdem.Code, recRetryNoIdem.Body.String())
	}

	recRetryZP := hw.do(http.MethodPost, "/api/v1/payments/pay-zp-1/retry", buyerTok, "", `{"method":"ZALOPAY"}`, map[string]string{"Idempotency-Key": "idem-retry-zp"})
	if recRetryZP.Code != http.StatusOK {
		t.Fatalf("POST /payments/{id}/retry ZALOPAY: want 200, got %d (%s)", recRetryZP.Code, recRetryZP.Body.String())
	}
	var retryZPResp struct {
		Payment       paymentDTO  `json:"payment"`
		PayURL        string      `json:"pay_url"`
		SwitchedToCOD bool        `json:"switched_to_cod"`
		Orders        []*orderDTO `json:"orders"`
	}
	if err := json.Unmarshal(recRetryZP.Body.Bytes(), &retryZPResp); err != nil {
		t.Fatalf("decode retry ZP response: %v", err)
	}
	if retryZPResp.PayURL != "https://sbgateway.zalopay.vn/openinapp?order=retry999" ||
		retryZPResp.Payment.PayURL != "https://sbgateway.zalopay.vn/openinapp?order=retry999" ||
		retryZPResp.SwitchedToCOD || len(retryZPResp.Orders) != 1 || retryZPResp.Orders[0].PaymentMethod != "ZALOPAY" {
		t.Fatalf("unexpected retry ZALOPAY response: %+v", retryZPResp)
	}

	recRetryCOD := hw.do(http.MethodPost, "/api/v1/payments/pay-zp-1/retry", buyerTok, "", `{"method":"COD"}`, map[string]string{"Idempotency-Key": "idem-retry-cod"})
	if recRetryCOD.Code != http.StatusOK {
		t.Fatalf("POST /payments/{id}/retry COD switch: want 200, got %d (%s)", recRetryCOD.Code, recRetryCOD.Body.String())
	}
	var retryCODResp struct {
		Payment       paymentDTO  `json:"payment"`
		SwitchedToCOD bool        `json:"switched_to_cod"`
		Orders        []*orderDTO `json:"orders"`
	}
	if err := json.Unmarshal(recRetryCOD.Body.Bytes(), &retryCODResp); err != nil {
		t.Fatalf("decode retry COD response: %v", err)
	}
	if !retryCODResp.SwitchedToCOD || retryCODResp.Payment.Method != "COD" || len(retryCODResp.Orders) != 1 || retryCODResp.Orders[0].Status != "CONFIRMED" {
		t.Fatalf("unexpected retry COD switch response: %+v", retryCODResp)
	}

	// 6. Seller GET /api/v1/payments/{id} redacts pay_url alongside qr_url/qr_code/provider_ref.
	recSellerPay := hw.do(http.MethodGet, "/api/v1/payments/pay-zp-1", sellerTok, "", "", nil)
	if recSellerPay.Code != http.StatusOK {
		t.Fatalf("seller GET /payments/pay-zp-1: want 200, got %d (%s)", recSellerPay.Code, recSellerPay.Body.String())
	}
	if strings.Contains(recSellerPay.Body.String(), "sbgateway.zalopay.vn") {
		t.Fatalf("seller GET /payments/{id} leaked pay_url: %s", recSellerPay.Body.String())
	}

	// 7. Admin fees/payment supports ZALOPAY, and shipping/calculate-fee supports ZALOPAY.
	recGetFee := hw.do(http.MethodGet, "/api/v1/admin/fees/payment?payment_method=ZALOPAY", adminTok, "", "", nil)
	if recGetFee.Code != http.StatusOK || hw.payment.lastGetFeeReq.GetPaymentMethod() != paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY {
		t.Fatalf("GET /admin/fees/payment?payment_method=ZALOPAY failed: code=%d req=%+v", recGetFee.Code, hw.payment.lastGetFeeReq)
	}
	recSetFee := hw.do(http.MethodPost, "/api/v1/admin/fees/payment", adminTok, "", `{"payment_method":"ZALOPAY","fee_bps":190}`, nil)
	if recSetFee.Code != http.StatusCreated || hw.payment.lastSetFeeReq.GetPaymentMethod() != paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY {
		t.Fatalf("POST /admin/fees/payment ZALOPAY failed: code=%d req=%+v", recSetFee.Code, hw.payment.lastSetFeeReq)
	}
	recCalcFee := hw.do(http.MethodPost, "/api/v1/shipping/calculate-fee", buyerTok, "", `{"shop_id":"shop-1","to_province_code":"01","items":[{"sku_id":"sku-1","quantity":1}],"payment_method":"ZALOPAY"}`, nil)
	if recCalcFee.Code != http.StatusOK {
		t.Fatalf("POST /shipping/calculate-fee ZALOPAY failed: code=%d body=%s", recCalcFee.Code, recCalcFee.Body.String())
	}

	// 8. Refund DTO carries approval & ZaloPay fields + PENDING_APPROVAL/REJECTED/MANUAL_REVIEW status.
	recRefunds := hw.do(http.MethodGet, "/api/v1/admin/refunds?status=PENDING_APPROVAL", adminTok, "", "", nil)
	if recRefunds.Code != http.StatusOK || hw.payment.lastListRefundsReq.GetStatus() != paymentv1.RefundStatus_REFUND_STATUS_PENDING_APPROVAL {
		t.Fatalf("GET /admin/refunds?status=PENDING_APPROVAL failed: code=%d req=%+v", recRefunds.Code, hw.payment.lastListRefundsReq)
	}
	for _, field := range []string{`"status":"PENDING_APPROVAL"`, `"requested_by":"cskh-1"`, `"provider_m_refund_id":"261124_2553_0001"`, `"provider_status":"pending"`} {
		if !strings.Contains(recRefunds.Body.String(), field) {
			t.Fatalf("GET /admin/refunds missing %s in %s", field, recRefunds.Body.String())
		}
	}
	for _, stStr := range []string{"REJECTED", "MANUAL_REVIEW"} {
		recSt := hw.do(http.MethodGet, "/api/v1/admin/refunds?status="+stStr, adminTok, "", "", nil)
		if recSt.Code != http.StatusOK || hw.payment.lastListRefundsReq.GetStatus() == paymentv1.RefundStatus_REFUND_STATUS_UNSPECIFIED {
			t.Fatalf("GET /admin/refunds?status=%s failed to parse status: code=%d got=%v", stStr, recSt.Code, hw.payment.lastListRefundsReq.GetStatus())
		}
	}
}

func TestW3B2_A7_StaffActivitiesApprovalsAndSharedRoutes(t *testing.T) {
	hw := newW3B2Harness(t, 0)
	superTok := hw.token(auth.RoleSuperAdmin)
	adminTok := hw.token(auth.RoleAdmin)
	modTok := hw.token(auth.RoleModerator)
	cskhTok := hw.token(auth.RoleCSKH)
	accTok := hw.token(auth.RoleAccountant)
	opsTok := hw.token(auth.RoleOps)

	// 1. POST /api/v1/admin/staff rejects password field (F14) and succeeds without it (self-audited).
	recWithPwd := hw.do(http.MethodPost, "/api/v1/admin/staff", superTok, "", `{"full_name":"New Ops","email":"ops2@example.com","phone":"0901234567","role":"ops","password":"NotAllowed123!"}`, nil)
	if recWithPwd.Code != http.StatusBadRequest {
		t.Fatalf("POST /admin/staff with password field must fail 400, got %d (%s)", recWithPwd.Code, recWithPwd.Body.String())
	}
	hw.user.mu.Lock()
	hw.user.auditReqs = nil
	hw.user.mu.Unlock()
	recStaffOK := hw.do(http.MethodPost, "/api/v1/admin/staff", superTok, "", `{"full_name":"New Acc","email":"acc2@example.com","phone":"0901234568","role":"accountant"}`, nil)
	if recStaffOK.Code != http.StatusCreated ||
		!strings.Contains(recStaffOK.Body.String(), `"role":"accountant"`) ||
		!strings.Contains(recStaffOK.Body.String(), `"password_reset_sent":true`) {
		t.Fatalf("POST /admin/staff failed: code=%d body=%s", recStaffOK.Code, recStaffOK.Body.String())
	}
	hw.user.mu.Lock()
	staffAudits := len(hw.user.auditReqs)
	hw.user.mu.Unlock()
	if staffAudits != 0 {
		t.Fatalf("POST /admin/staff is user self-audited; gateway must write 0 audit logs, got %d", staffAudits)
	}

	// 2. GET /api/v1/admin/users/{id}/activities:
	//    - moderator (no orders.read) -> 200 with recent_orders: [] and 0 calls to Order.ListOrders!
	hw.order.mu.Lock()
	hw.order.listOrdersCalls = 0
	hw.order.mu.Unlock()
	recModAct := hw.do(http.MethodGet, "/api/v1/admin/users/buyer-9/activities", modTok, "", "", nil)
	if recModAct.Code != http.StatusOK {
		t.Fatalf("moderator GET /admin/users/buyer-9/activities: want 200, got %d (%s)", recModAct.Code, recModAct.Body.String())
	}
	if hw.order.listOrdersCalls != 0 {
		t.Fatalf("moderator must NOT trigger Order.ListOrders, got %d calls", hw.order.listOrdersCalls)
	}
	if !strings.Contains(recModAct.Body.String(), `"recent_orders":[]`) || !strings.Contains(recModAct.Body.String(), `"cod_rejected_count":2`) {
		t.Fatalf("moderator activities unexpected body: %s", recModAct.Body.String())
	}

	//    - cskh (has orders.read) -> calls Order.ListOrders(buyer_id=buyer-9, page=1, page_size=10) with cskh JWT.
	recCskhAct := hw.do(http.MethodGet, "/api/v1/admin/users/buyer-9/activities", cskhTok, "", "", nil)
	if recCskhAct.Code != http.StatusOK {
		t.Fatalf("cskh GET /admin/users/buyer-9/activities: want 200, got %d (%s)", recCskhAct.Code, recCskhAct.Body.String())
	}
	if hw.order.listOrdersCalls != 1 || hw.order.lastListOrders.GetBuyerId() != "buyer-9" || hw.order.lastListOrders.GetPage() != 1 || hw.order.lastListOrders.GetPageSize() != 10 {
		t.Fatalf("cskh activities did not call Order.ListOrders(buyer-9, 1, 10): calls=%d req=%+v", hw.order.listOrdersCalls, hw.order.lastListOrders)
	}
	if vals := hw.order.lastListOrdersMD.Get(auth.MDAuthorization); len(vals) == 0 || vals[0] != "Bearer "+cskhTok {
		t.Fatalf("activities Order.ListOrders must forward viewer JWT, got %v", vals)
	}
	if !strings.Contains(recCskhAct.Body.String(), `"ord-recent-1"`) {
		t.Fatalf("cskh activities missing recent_orders: %s", recCskhAct.Body.String())
	}

	// 3. GET /api/v1/admin/approvals aggregated strictly per role capabilities.
	//    - moderator: ONLY shop approvals (0 calls to payment.ListRefunds)
	hw.user.mu.Lock()
	hw.user.listShopApprovalsCalls = 0
	hw.user.mu.Unlock()
	hw.payment.mu.Lock()
	hw.payment.listRefundsCalls = 0
	hw.payment.mu.Unlock()

	recModAppr := hw.do(http.MethodGet, "/api/v1/admin/approvals", modTok, "", "", nil)
	if recModAppr.Code != http.StatusOK {
		t.Fatalf("moderator GET /admin/approvals: want 200, got %d (%s)", recModAppr.Code, recModAppr.Body.String())
	}
	if hw.user.listShopApprovalsCalls != 1 || hw.payment.listRefundsCalls != 0 {
		t.Fatalf("moderator approvals call counts = (shop=%d, refund=%d), want (1, 0)", hw.user.listShopApprovalsCalls, hw.payment.listRefundsCalls)
	}
	if !strings.Contains(recModAppr.Body.String(), `"shop-appr-1"`) || !strings.Contains(recModAppr.Body.String(), `"refund_approvals":[]`) {
		t.Fatalf("moderator approvals body mismatch: %s", recModAppr.Body.String())
	}

	//    - accountant & cskh: ONLY refund approvals (0 calls to user.AdminListShopApprovals)
	for _, tc := range []struct {
		name string
		tok  string
	}{{"accountant", accTok}, {"cskh", cskhTok}} {
		hw.user.mu.Lock()
		hw.user.listShopApprovalsCalls = 0
		hw.user.mu.Unlock()
		hw.payment.mu.Lock()
		hw.payment.listRefundsCalls = 0
		hw.payment.mu.Unlock()

		rec := hw.do(http.MethodGet, "/api/v1/admin/approvals", tc.tok, "", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s GET /admin/approvals: want 200, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
		if hw.user.listShopApprovalsCalls != 0 || hw.payment.listRefundsCalls != 1 {
			t.Fatalf("%s approvals call counts = (shop=%d, refund=%d), want (0, 1)", tc.name, hw.user.listShopApprovalsCalls, hw.payment.listRefundsCalls)
		}
		if !strings.Contains(rec.Body.String(), `"ref-appr-1"`) || !strings.Contains(rec.Body.String(), `"shop_approvals":[]`) {
			t.Fatalf("%s approvals body mismatch: %s", tc.name, rec.Body.String())
		}
	}

	//    - admin & ops & super_admin: BOTH shop & refund approvals
	for _, tc := range []struct {
		name string
		tok  string
	}{{"admin", adminTok}, {"ops", opsTok}, {"super_admin", superTok}} {
		hw.user.mu.Lock()
		hw.user.listShopApprovalsCalls = 0
		hw.user.mu.Unlock()
		hw.payment.mu.Lock()
		hw.payment.listRefundsCalls = 0
		hw.payment.mu.Unlock()

		rec := hw.do(http.MethodGet, "/api/v1/admin/approvals", tc.tok, "", "", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s GET /admin/approvals: want 200, got %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
		if hw.user.listShopApprovalsCalls != 1 || hw.payment.listRefundsCalls != 1 {
			t.Fatalf("%s approvals call counts = (shop=%d, refund=%d), want (1, 1)", tc.name, hw.user.listShopApprovalsCalls, hw.payment.listRefundsCalls)
		}
		if !strings.Contains(rec.Body.String(), `"shop-appr-1"`) || !strings.Contains(rec.Body.String(), `"ref-appr-1"`) {
			t.Fatalf("%s approvals missing items: %s", tc.name, rec.Body.String())
		}
	}

	// 4. Shop suspension, decision, unsuspend (self-audited + MAKER_CHECKER_CONFLICT -> 403 exact code).
	hw.user.mu.Lock()
	hw.user.auditReqs = nil
	hw.user.mu.Unlock()
	recSusp := hw.do(http.MethodPost, "/api/v1/admin/shops/shop-1/suspend", opsTok, "", `{"reason":"fraud"}`, nil)
	if recSusp.Code != http.StatusCreated || !strings.Contains(recSusp.Body.String(), `"shop-appr-1"`) {
		t.Fatalf("POST /admin/shops/shop-1/suspend: want 201, got %d (%s)", recSusp.Code, recSusp.Body.String())
	}
	hw.user.mu.Lock()
	hw.user.decideShopErr = status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT: cannot approve own request")
	hw.user.mu.Unlock()
	recSelfShop := hw.do(http.MethodPost, "/api/v1/admin/approvals/shop/shop-appr-1/decide", opsTok, "", `{"decision":"APPROVE","note":"self"}`, nil)
	if recSelfShop.Code != http.StatusForbidden || !strings.Contains(recSelfShop.Body.String(), `"code":"MAKER_CHECKER_CONFLICT"`) {
		t.Fatalf("self shop approval: want 403 MAKER_CHECKER_CONFLICT, got %d (%s)", recSelfShop.Code, recSelfShop.Body.String())
	}
	hw.user.mu.Lock()
	hw.user.decideShopErr = nil
	hw.user.mu.Unlock()
	recDecideShop := hw.do(http.MethodPost, "/api/v1/admin/approvals/shop/shop-appr-1/decide", adminTok, "", `{"decision":"APPROVE","note":"confirmed"}`, nil)
	if recDecideShop.Code != http.StatusOK || !strings.Contains(recDecideShop.Body.String(), `"suspended_reason":"policy breach"`) || !strings.Contains(recDecideShop.Body.String(), `"status_version":2`) {
		t.Fatalf("decide shop approval: want 200 with suspension metadata, got %d (%s)", recDecideShop.Code, recDecideShop.Body.String())
	}
	recUnsusp := hw.do(http.MethodPost, "/api/v1/admin/shops/shop-1/unsuspend", opsTok, "", `{"reason":"fixed"}`, nil)
	if recUnsusp.Code != http.StatusOK || !strings.Contains(recUnsusp.Body.String(), `"status":"ACTIVE"`) {
		t.Fatalf("unsuspend shop: want 200 ACTIVE, got %d (%s)", recUnsusp.Code, recUnsusp.Body.String())
	}
	hw.user.mu.Lock()
	shopSelfAudits := len(hw.user.auditReqs)
	hw.user.mu.Unlock()
	if shopSelfAudits != 0 {
		t.Fatalf("user self-audited shop routes must not duplicate gateway WriteAuditLog, got %d", shopSelfAudits)
	}

	// 5. Refund approval decide: MAKER_CHECKER_CONFLICT -> 403 exact code; approve -> 200 + audited.
	hw.payment.mu.Lock()
	hw.payment.decideRefundErr = status.Error(codes.PermissionDenied, "MAKER_CHECKER_CONFLICT: checker equals maker")
	hw.payment.mu.Unlock()
	recSelfRef := hw.do(http.MethodPost, "/api/v1/admin/approvals/refund/ref-appr-1/decide", accTok, "", `{"approve":true,"note":"self"}`, nil)
	if recSelfRef.Code != http.StatusForbidden || !strings.Contains(recSelfRef.Body.String(), `"code":"MAKER_CHECKER_CONFLICT"`) {
		t.Fatalf("self refund approval: want 403 MAKER_CHECKER_CONFLICT, got %d (%s)", recSelfRef.Code, recSelfRef.Body.String())
	}
	hw.payment.mu.Lock()
	hw.payment.decideRefundErr = nil
	hw.payment.mu.Unlock()
	recDecideRef := hw.do(http.MethodPost, "/api/v1/admin/approvals/refund/ref-appr-1/decide", accTok, "", `{"approve":true,"note":"ok"}`, nil)
	if recDecideRef.Code != http.StatusOK || !strings.Contains(recDecideRef.Body.String(), `"approved_by":"acc-1"`) {
		t.Fatalf("decide refund approval: want 200 with approved_by, got %d (%s)", recDecideRef.Code, recDecideRef.Body.String())
	}

	// 6. Shared routes open admin branch for roles with matching read capability:
	//    - GET /orders/{id} & GET /orders/{id}/timeline & GET /orders/{id}/refunds:
	//      cskh (orders.read + refunds.read) sees admin fields and bypasses buyer ownership check; moderator does not.
	recOrdCSKH := hw.do(http.MethodGet, "/api/v1/orders/ord-1", cskhTok, "", "", nil)
	if recOrdCSKH.Code != http.StatusOK || !strings.Contains(recOrdCSKH.Body.String(), `"commission_vnd":15000`) || !strings.Contains(recOrdCSKH.Body.String(), `"needs_manual_review":true`) {
		t.Fatalf("cskh GET /orders/ord-1 should see admin order fields: code=%d body=%s", recOrdCSKH.Code, recOrdCSKH.Body.String())
	}
	recTLCSKH := hw.do(http.MethodGet, "/api/v1/orders/ord-1/timeline", cskhTok, "", "", nil)
	if recTLCSKH.Code != http.StatusOK || !strings.Contains(recTLCSKH.Body.String(), `"ev-1"`) {
		t.Fatalf("cskh GET /orders/ord-1/timeline: want 200, got %d (%s)", recTLCSKH.Code, recTLCSKH.Body.String())
	}
	recRefCSKH := hw.do(http.MethodGet, "/api/v1/orders/ord-1/refunds", cskhTok, "", "", nil)
	if recRefCSKH.Code != http.StatusOK || !strings.Contains(recRefCSKH.Body.String(), `"ref-appr-1"`) {
		t.Fatalf("cskh GET /orders/ord-1/refunds: want 200, got %d (%s)", recRefCSKH.Code, recRefCSKH.Body.String())
	}
	recRefMod := hw.do(http.MethodGet, "/api/v1/orders/ord-1/refunds", modTok, "", "", nil)
	if recRefMod.Code != http.StatusForbidden {
		t.Fatalf("moderator GET /orders/ord-1/refunds (non-owner): want 403, got %d (%s)", recRefMod.Code, recRefMod.Body.String())
	}
	recOrdMod := hw.do(http.MethodGet, "/api/v1/orders/ord-1", modTok, "", "", nil)
	if recOrdMod.Code != http.StatusOK || strings.Contains(recOrdMod.Body.String(), `"commission_vnd":15000`) || strings.Contains(recOrdMod.Body.String(), `"needs_manual_review":true`) {
		t.Fatalf("moderator GET /orders/ord-1 must NOT see admin fee/manual-review fields: code=%d body=%s", recOrdMod.Code, recOrdMod.Body.String())
	}

	//    - Invalid query validation on activities & approvals:
	if recBadTime := hw.do(http.MethodGet, "/api/v1/admin/users/buyer-9/activities?from_time=not-a-timestamp", modTok, "", "", nil); recBadTime.Code != http.StatusBadRequest {
		t.Fatalf("invalid from_time on activities: want 400, got %d", recBadTime.Code)
	}
	if recBadType := hw.do(http.MethodGet, "/api/v1/admin/approvals?type=invalid", adminTok, "", "", nil); recBadType.Code != http.StatusBadRequest {
		t.Fatalf("invalid type on approvals: want 400, got %d", recBadType.Code)
	}
	hw.user.mu.Lock()
	hw.user.listShopApprovalsCalls = 0
	hw.user.mu.Unlock()
	hw.payment.mu.Lock()
	hw.payment.listRefundsCalls = 0
	hw.payment.mu.Unlock()
	if recShopOnly := hw.do(http.MethodGet, "/api/v1/admin/approvals?type=shop", adminTok, "", "", nil); recShopOnly.Code != http.StatusOK || hw.user.listShopApprovalsCalls != 1 || hw.payment.listRefundsCalls != 0 {
		t.Fatalf("approvals?type=shop: code=%d calls=(%d,%d)", recShopOnly.Code, hw.user.listShopApprovalsCalls, hw.payment.listRefundsCalls)
	}
	if recRefOnly := hw.do(http.MethodGet, "/api/v1/admin/approvals?type=refund", adminTok, "", "", nil); recRefOnly.Code != http.StatusOK || hw.user.listShopApprovalsCalls != 1 || hw.payment.listRefundsCalls != 1 {
		t.Fatalf("approvals?type=refund: code=%d calls=(%d,%d)", recRefOnly.Code, hw.user.listShopApprovalsCalls, hw.payment.listRefundsCalls)
	}

	//    - GET /payments/{id} & SSE /payments/{id}/events: cskh/accountant/ops allowed (full allocations + pay_url); moderator denied 403!
	for _, tc := range []struct {
		name string
		tok  string
	}{{"cskh", cskhTok}, {"accountant", accTok}, {"ops", opsTok}} {
		recPay := hw.do(http.MethodGet, "/api/v1/payments/pay-zp-1", tc.tok, "", "", nil)
		if recPay.Code != http.StatusOK || !strings.Contains(recPay.Body.String(), `"commission_vnd":12000`) || !strings.Contains(recPay.Body.String(), `"pay_url":"https://sbgateway.zalopay.vn`) {
			t.Fatalf("%s GET /payments/pay-zp-1 should see full payment: code=%d body=%s", tc.name, recPay.Code, recPay.Body.String())
		}
		recSSE := hw.do(http.MethodGet, "/api/v1/payments/pay-zp-1/events", tc.tok, "", "", nil)
		if recSSE.Code != http.StatusOK {
			t.Fatalf("%s SSE /payments/pay-zp-1/events: want 200, got %d", tc.name, recSSE.Code)
		}
	}
	if recModPay := hw.do(http.MethodGet, "/api/v1/payments/pay-zp-1", modTok, "", "", nil); recModPay.Code != http.StatusForbidden {
		t.Fatalf("moderator GET /payments/pay-zp-1 (non-owner): want 403, got %d", recModPay.Code)
	}
	if recModSSE := hw.do(http.MethodGet, "/api/v1/payments/pay-zp-1/events", modTok, "", "", nil); recModSSE.Code != http.StatusForbidden {
		t.Fatalf("moderator SSE /payments/pay-zp-1/events (non-owner): want 403, got %d", recModSSE.Code)
	}

	//    - GET /shipping/shipments/{order_id}: ops (carriers.read) skips Order.GetOrder ownership check and goes straight to Shipping.GetShipment.
	hw.order.mu.Lock()
	hw.order.getOrderCalls = 0
	hw.order.mu.Unlock()
	recShipOps := hw.do(http.MethodGet, "/api/v1/shipping/shipments/ord-1", opsTok, "", "", nil)
	if recShipOps.Code != http.StatusOK || hw.order.getOrderCalls != 0 {
		t.Fatalf("ops GET /shipping/shipments/ord-1 should bypass buyer Order.GetOrder via carriers.read: code=%d getOrderCalls=%d", recShipOps.Code, hw.order.getOrderCalls)
	}

	//    - GET /shipping/labels/{tracking_code}: ops passes through proxy with Bearer token forwarded and X-User-* stripped.
	recLblOps := hw.do(http.MethodGet, "/api/v1/shipping/labels/TRK-1", opsTok, "", "", map[string]string{"X-User-Role": "spoofed"})
	if recLblOps.Code != http.StatusOK {
		t.Fatalf("ops GET /shipping/labels/TRK-1: want 200, got %d", recLblOps.Code)
	}
	hw.whMu.Lock()
	lblHdr := hw.lastLblHdr
	hw.whMu.Unlock()
	if lblHdr.Get("Authorization") != "Bearer "+opsTok || lblHdr.Get("X-User-Role") != "" {
		t.Fatalf("label proxy headers mismatch: auth=%q x-user-role=%q", lblHdr.Get("Authorization"), lblHdr.Get("X-User-Role"))
	}
}

func TestW3B2_AdminUpdateUserRoleRejectsSuperAdminAndInvalidRoles(t *testing.T) {
	hw := newW3B2Harness(t, 0)
	superTok := hw.token(auth.RoleSuperAdmin)

	rejected := []struct {
		name string
		role string
	}{
		{"super_admin", "super_admin"},
		{"SUPER_ADMIN", "SUPER_ADMIN"},
		{"proto_enum_super_admin", "user_role_super_admin"},
		{"super_admin_with_spaces", "  super_admin  "},
		{"empty", ""},
		{"unknown", "root"},
	}

	for _, tc := range rejected {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			hw.user.mu.Lock()
			hw.user.updateRoleCalls = 0
			hw.user.mu.Unlock()

			rec := hw.do(http.MethodPatch, "/api/v1/admin/users/u-1/role", superTok, "", `{"role":"`+tc.role+`","reason":"test"}`, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("role %q: want 400, got %d (%s)", tc.role, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"INVALID_ARGUMENT"`) {
				t.Fatalf("role %q: want INVALID_ARGUMENT, got %s", tc.role, rec.Body.String())
			}
			hw.user.mu.Lock()
			calls := hw.user.updateRoleCalls
			hw.user.mu.Unlock()
			if calls != 0 {
				t.Fatalf("role %q: gateway must reject before calling UserService.AdminUpdateUserRole, got %d calls", tc.role, calls)
			}
		})
	}

	allowed := []string{"buyer", "seller", "admin", "moderator", "cskh", "accountant", "ops"}
	for _, role := range allowed {
		t.Run("allow_"+role, func(t *testing.T) {
			hw.user.mu.Lock()
			hw.user.updateRoleCalls = 0
			hw.user.mu.Unlock()

			rec := hw.do(http.MethodPatch, "/api/v1/admin/users/u-1/role", superTok, "", `{"role":"`+role+`","reason":"assign"}`, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("role %q: want 200, got %d (%s)", role, rec.Code, rec.Body.String())
			}
			hw.user.mu.Lock()
			calls := hw.user.updateRoleCalls
			hw.user.mu.Unlock()
			if calls != 1 {
				t.Fatalf("role %q: expected 1 AdminUpdateUserRole call, got %d", role, calls)
			}
		})
	}
}

func TestW3B2_PaginationOverflowReturns400InvalidArgument(t *testing.T) {
	hw := newW3B2Harness(t, 0)
	superTok := hw.token(auth.RoleSuperAdmin)

	overflowQueries := []struct {
		name  string
		query string
	}{
		{"int32_negative_wrap_size100", "page=30000000&page_size=100"},
		{"int32_positive_wrap_size100", "page=50000000&page_size=100"},
		{"boundary_plus_one_size20", "page=107374184&page_size=20"},
		{"boundary_plus_one_default_size", "page=107374184"},
		{"boundary_plus_one_size100", "page=21474838&page_size=100"},
		{"page_exceeds_max_int32", "page=2147483648&page_size=1"},
		{"page_exceeds_max_int64", "page=99999999999999999999999&page_size=20"},
		{"page_clamped_size200_overflow", "page=21474838&page_size=200"},
	}

	routes := []string{
		"/api/v1/admin/users",
		"/api/v1/admin/shops",
		"/api/v1/admin/audit-logs",
		"/api/v1/admin/users/u-1/activities",
		"/api/v1/admin/approvals",
		"/api/v1/admin/orders",
		"/api/v1/admin/refunds",
		"/api/v1/admin/reconciliation/runs",
		"/api/v1/admin/banned-keywords",
		"/api/v1/admin/products",
		"/api/v1/admin/flash-sales",
	}

	for _, tc := range overflowQueries {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			hw.user.mu.Lock()
			hw.user.listUsersCalls = 0
			hw.user.mu.Unlock()

			rec := hw.do(http.MethodGet, "/api/v1/admin/users?"+tc.query, superTok, "", "", nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("query %q: want 400, got %d (%s)", tc.query, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"code":"INVALID_ARGUMENT"`) {
				t.Fatalf("query %q: want INVALID_ARGUMENT, got %s", tc.query, rec.Body.String())
			}
			hw.user.mu.Lock()
			calls := hw.user.listUsersCalls
			hw.user.mu.Unlock()
			if calls != 0 {
				t.Fatalf("query %q: gateway must reject before calling upstream gRPC, got %d calls", tc.query, calls)
			}
		})
	}

	for _, route := range routes {
		t.Run("route_"+route, func(t *testing.T) {
			rec := hw.do(http.MethodGet, route+"?page=30000000&page_size=100", superTok, "", "", nil)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"INVALID_ARGUMENT"`) {
				t.Fatalf("GET %s overflow: want 400 INVALID_ARGUMENT, got %d (%s)", route, rec.Code, rec.Body.String())
			}
		})
	}

	validQueries := []struct {
		name         string
		query        string
		wantPage     int32
		wantPageSize int32
	}{
		{"defaults", "", 1, 20},
		{"non_positive_clamped", "page=0&page_size=0", 1, 20},
		{"oversized_page_size_clamped", "page=1&page_size=200", 1, 100},
		{"max_valid_size20", "page=107374183&page_size=20", 107374183, 20},
		{"max_valid_size100", "page=21474837&page_size=100", 21474837, 100},
		{"max_valid_size1", "page=2147483647&page_size=1", 2147483647, 1},
	}

	for _, tc := range validQueries {
		t.Run("valid_"+tc.name, func(t *testing.T) {
			target := "/api/v1/admin/users"
			if tc.query != "" {
				target += "?" + tc.query
			}
			rec := hw.do(http.MethodGet, target, superTok, "", "", nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("query %q: want 200, got %d (%s)", tc.query, rec.Code, rec.Body.String())
			}
			var resp struct {
				Page     int32 `json:"page"`
				PageSize int32 `json:"page_size"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Page != tc.wantPage || resp.PageSize != tc.wantPageSize {
				t.Fatalf("query %q: got (page=%d, page_size=%d), want (%d, %d)",
					tc.query, resp.Page, resp.PageSize, tc.wantPage, tc.wantPageSize)
			}
		})
	}
}
