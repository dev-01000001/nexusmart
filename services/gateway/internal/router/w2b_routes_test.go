package router

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// --- W2B mock gRPC clients ---

type w2bUserClient struct {
	userv1.UserServiceClient
	mu                sync.Mutex
	lastAddBank       *userv1.AddShopBankAccountRequest
	lastVerifyOTP     *userv1.VerifyShopBankAccountOTPRequest
	lastGetBank       *userv1.GetShopBankAccountRequest
	lastReviewBank    *userv1.AdminReviewShopBankAccountRequest
	lastUserStats     *userv1.GetPlatformUserStatsRequest
	lastAdminShopsReq *userv1.AdminListShopsRequest
	auditReqs         []*userv1.WriteAuditLogRequest
}

func (u *w2bUserClient) AdminListShops(_ context.Context, in *userv1.AdminListShopsRequest, _ ...grpc.CallOption) (*userv1.AdminListShopsResponse, error) {
	u.mu.Lock()
	u.lastAdminShopsReq = in
	u.mu.Unlock()
	return &userv1.AdminListShopsResponse{
		Shops: []*userv1.Shop{
			{
				Id:                "shop-a",
				OwnerUserId:       "seller-a",
				Name:              "Shop A",
				Slug:              "shop-a",
				Status:            userv1.ShopStatus_SHOP_STATUS_ACTIVE,
				BankReviewPending: true,
			},
		},
		Total: 1,
	}, nil
}

func (u *w2bUserClient) ListPublicShops(_ context.Context, _ *userv1.ListPublicShopsRequest, _ ...grpc.CallOption) (*userv1.ListPublicShopsResponse, error) {
	return &userv1.ListPublicShopsResponse{
		Shops: []*userv1.Shop{
			{
				Id:                "shop-a",
				OwnerUserId:       "seller-a",
				Name:              "Shop A",
				Slug:              "shop-a",
				Status:            userv1.ShopStatus_SHOP_STATUS_ACTIVE,
				BankReviewPending: true,
			},
		},
		Total: 1,
	}, nil
}

func (u *w2bUserClient) GetShop(_ context.Context, in *userv1.GetShopRequest, _ ...grpc.CallOption) (*userv1.GetShopResponse, error) {
	return &userv1.GetShopResponse{
		Shop: &userv1.Shop{
			Id:                "shop-a",
			OwnerUserId:       "seller-a",
			Name:              "Shop A",
			Slug:              "shop-a",
			Status:            userv1.ShopStatus_SHOP_STATUS_ACTIVE,
			BankReviewPending: true,
		},
	}, nil
}

func (u *w2bUserClient) AddShopBankAccount(_ context.Context, in *userv1.AddShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.AddShopBankAccountResponse, error) {
	u.mu.Lock()
	u.lastAddBank = in
	u.mu.Unlock()
	return &userv1.AddShopBankAccountResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  "ba-1",
			ShopId:              in.GetShopId(),
			BankCode:            in.GetBankCode(),
			BankName:            in.GetBankName(),
			AccountNumberMasked: "****7890",
			AccountNumber:       "01234567897890", // intentionally populated to verify seller handler strips it
			AccountHolderName:   in.GetAccountHolderName(),
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_PENDING_OTP,
		},
		OtpSent: true,
	}, nil
}

func (u *w2bUserClient) VerifyShopBankAccountOTP(_ context.Context, in *userv1.VerifyShopBankAccountOTPRequest, _ ...grpc.CallOption) (*userv1.VerifyShopBankAccountOTPResponse, error) {
	u.mu.Lock()
	u.lastVerifyOTP = in
	u.mu.Unlock()
	now := timestamppb.Now()
	return &userv1.VerifyShopBankAccountOTPResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  "ba-1",
			ShopId:              in.GetShopId(),
			BankCode:            "VCB",
			BankName:            "Vietcombank",
			AccountNumberMasked: "****7890",
			AccountNumber:       "01234567897890",
			AccountHolderName:   "NGUYEN VAN A",
			HolderNameMatched:   true,
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED,
			PayoutLockedUntil:   timestamppb.New(time.Now().UTC().Add(24 * time.Hour)),
			VerifiedAt:          now,
		},
	}, nil
}

func (u *w2bUserClient) GetShopBankAccount(_ context.Context, in *userv1.GetShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.GetShopBankAccountResponse, error) {
	u.mu.Lock()
	u.lastGetBank = in
	u.mu.Unlock()
	return &userv1.GetShopBankAccountResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  "ba-1",
			ShopId:              in.GetShopId(),
			BankCode:            "VCB",
			BankName:            "Vietcombank",
			AccountNumberMasked: "****7890",
			AccountNumber:       "01234567897890",
			AccountHolderName:   "NGUYEN VAN A",
			HolderNameMatched:   true,
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED,
		},
	}, nil
}

func (u *w2bUserClient) AdminReviewShopBankAccount(_ context.Context, in *userv1.AdminReviewShopBankAccountRequest, _ ...grpc.CallOption) (*userv1.AdminReviewShopBankAccountResponse, error) {
	u.mu.Lock()
	u.lastReviewBank = in
	u.mu.Unlock()
	return &userv1.AdminReviewShopBankAccountResponse{
		BankAccount: &userv1.ShopBankAccount{
			Id:                  in.GetBankAccountId(),
			ShopId:              in.GetShopId(),
			BankCode:            "VCB",
			BankName:            "Vietcombank",
			AccountNumberMasked: "****7890",
			AccountNumber:       "01234567897890",
			AccountHolderName:   "CONG TY TNHH ABC",
			Status:              userv1.ShopBankAccountStatus_SHOP_BANK_ACCOUNT_STATUS_VERIFIED,
			ReviewNote:          in.GetReason(),
		},
	}, nil
}

func (u *w2bUserClient) GetPlatformUserStats(_ context.Context, in *userv1.GetPlatformUserStatsRequest, _ ...grpc.CallOption) (*userv1.GetPlatformUserStatsResponse, error) {
	u.mu.Lock()
	u.lastUserStats = in
	u.mu.Unlock()
	return &userv1.GetPlatformUserStatsResponse{
		FromDate:     in.GetFromDate(),
		ToDate:       in.GetToDate(),
		TotalUsers:   120,
		ActiveUsers:  110,
		NewUsers:     15,
		TotalShops:   12,
		ActiveShops:  10,
		PendingShops: 2,
		NewShops:     3,
		Daily: []*userv1.DailyUserShopStat{
			{Date: "2026-11-20", NewUsers: 5, NewShops: 1},
			{Date: "2026-11-21", NewUsers: 10, NewShops: 2},
		},
	}, nil
}

func (u *w2bUserClient) WriteAuditLog(_ context.Context, in *userv1.WriteAuditLogRequest, _ ...grpc.CallOption) (*userv1.WriteAuditLogResponse, error) {
	u.mu.Lock()
	u.auditReqs = append(u.auditReqs, in)
	u.mu.Unlock()
	return &userv1.WriteAuditLogResponse{AuditLog: &userv1.AuditLog{Id: "audit-w2b"}}, nil
}

type w2bProductClient struct {
	productv1.ProductServiceClient
	mu               sync.Mutex
	skus             map[string]*productv1.SKU
	lastUpdateCat    *productv1.UpdateCategoryRequest
	lastListCatFees  *productv1.ListCategoryFeeSchedulesRequest
	lastSetCatFee    *productv1.SetCategoryFeeScheduleRequest
	lastGetSKUsBatch *productv1.GetSKUsBatchRequest
}

func (p *w2bProductClient) UpdateCategory(_ context.Context, in *productv1.UpdateCategoryRequest, _ ...grpc.CallOption) (*productv1.UpdateCategoryResponse, error) {
	p.mu.Lock()
	p.lastUpdateCat = in
	p.mu.Unlock()
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

func (p *w2bProductClient) ListCategoryFeeSchedules(_ context.Context, in *productv1.ListCategoryFeeSchedulesRequest, _ ...grpc.CallOption) (*productv1.ListCategoryFeeSchedulesResponse, error) {
	p.mu.Lock()
	p.lastListCatFees = in
	p.mu.Unlock()
	now := timestamppb.Now()
	return &productv1.ListCategoryFeeSchedulesResponse{
		Schedules: []*productv1.CategoryFeeSchedule{
			{
				Id:                "cfs-1",
				CategoryId:        "cat-1",
				CommissionRateBps: 500,
				EffectiveFrom:     now,
				CreatedBy:         "admin-1",
				Note:              "base rate",
				CreatedAt:         now,
			},
		},
		Total: 1,
	}, nil
}

func (p *w2bProductClient) SetCategoryFeeSchedule(_ context.Context, in *productv1.SetCategoryFeeScheduleRequest, _ ...grpc.CallOption) (*productv1.SetCategoryFeeScheduleResponse, error) {
	p.mu.Lock()
	p.lastSetCatFee = in
	p.mu.Unlock()
	eff := in.GetEffectiveFrom()
	if eff == nil {
		eff = timestamppb.Now()
	}
	return &productv1.SetCategoryFeeScheduleResponse{
		Schedule: &productv1.CategoryFeeSchedule{
			Id:                "cfs-new",
			CategoryId:        in.GetCategoryId(),
			CommissionRateBps: in.GetCommissionRateBps(),
			EffectiveFrom:     eff,
			CreatedBy:         "admin-1",
			Note:              in.GetNote(),
			CreatedAt:         timestamppb.Now(),
		},
	}, nil
}

func (p *w2bProductClient) GetProduct(_ context.Context, in *productv1.GetProductRequest, _ ...grpc.CallOption) (*productv1.GetProductResponse, error) {
	return &productv1.GetProductResponse{
		Product: &productv1.Product{
			Id:     in.GetId(),
			ShopId: "shop-a",
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			Skus: []*productv1.SKU{
				{
					Id:              "sku-1",
					ShopId:          "shop-a",
					CategoryId:      "cat-1",
					PriceVnd:        250000,
					MinPrice_30DVnd: 220000,
					IsActive:        true,
				},
			},
		},
	}, nil
}

func (p *w2bProductClient) GetSKUsBatch(_ context.Context, in *productv1.GetSKUsBatchRequest, _ ...grpc.CallOption) (*productv1.GetSKUsBatchResponse, error) {
	p.mu.Lock()
	p.lastGetSKUsBatch = in
	defer p.mu.Unlock()
	out := make([]*productv1.SKU, 0, len(in.GetSkuIds()))
	for _, id := range in.GetSkuIds() {
		if sk, ok := p.skus[id]; ok {
			out = append(out, sk)
		}
	}
	return &productv1.GetSKUsBatchResponse{Skus: out}, nil
}

type w2bPromotionClient struct {
	promotionv1.PromotionServiceClient
	mu                    sync.Mutex
	activeShopDiscounts   []*promotionv1.ActiveShopDiscount
	lastListVouchers      *promotionv1.ListAvailableVouchersRequest
	lastCreateVoucher     *promotionv1.CreateVoucherRequest
	lastUpdateVoucher     *promotionv1.UpdateVoucherRequest
	lastSetVoucherStatus  *promotionv1.SetVoucherStatusRequest
	lastDeleteVoucher     *promotionv1.DeleteVoucherRequest
	lastCreateFlashSale   *promotionv1.CreateFlashSaleRequest
	lastListAllFlashSales *promotionv1.ListAllFlashSalesRequest
	lastListFlashItems    *promotionv1.ListFlashSaleItemsRequest
	lastApproveFlash      *promotionv1.ApproveFlashSaleItemRequest
	lastCreateProgram     *promotionv1.CreateShopDiscountProgramRequest
	lastUpdateProgram     *promotionv1.UpdateShopDiscountProgramRequest
	lastEndProgram        *promotionv1.EndShopDiscountProgramRequest
	lastListPrograms      *promotionv1.ListShopDiscountProgramsRequest
	lastActiveDiscounts   *promotionv1.ListActiveShopDiscountsRequest
	lastPreview           *promotionv1.PreviewDiscountsRequest
}

func (p *w2bPromotionClient) ListAvailableVouchers(_ context.Context, in *promotionv1.ListAvailableVouchersRequest, _ ...grpc.CallOption) (*promotionv1.ListAvailableVouchersResponse, error) {
	p.mu.Lock()
	p.lastListVouchers = in
	p.mu.Unlock()
	return &promotionv1.ListAvailableVouchersResponse{
		Vouchers: []*promotionv1.Voucher{
			{
				Id:          "v-1",
				Code:        "SAVE10",
				Name:        "Save 10%",
				Scope:       in.GetScope(),
				ShopId:      in.GetShopId(),
				Status:      promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
				CategoryIds: []string{"cat-1"},
			},
		},
	}, nil
}

func (p *w2bPromotionClient) CreateVoucher(_ context.Context, in *promotionv1.CreateVoucherRequest, _ ...grpc.CallOption) (*promotionv1.CreateVoucherResponse, error) {
	p.mu.Lock()
	p.lastCreateVoucher = in
	p.mu.Unlock()
	return &promotionv1.CreateVoucherResponse{
		Voucher: &promotionv1.Voucher{
			Id:          "v-new",
			Code:        in.GetCode(),
			Name:        in.GetName(),
			Scope:       in.GetScope(),
			ShopId:      in.GetShopId(),
			Status:      promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
			CategoryIds: in.GetCategoryIds(),
		},
	}, nil
}

func (p *w2bPromotionClient) UpdateVoucher(_ context.Context, in *promotionv1.UpdateVoucherRequest, _ ...grpc.CallOption) (*promotionv1.UpdateVoucherResponse, error) {
	p.mu.Lock()
	p.lastUpdateVoucher = in
	p.mu.Unlock()
	return &promotionv1.UpdateVoucherResponse{
		Voucher: &promotionv1.Voucher{
			Id:          in.GetVoucherId(),
			Name:        in.GetName(),
			Status:      promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
			CategoryIds: in.GetCategoryIds(),
		},
	}, nil
}

func (p *w2bPromotionClient) SetVoucherStatus(_ context.Context, in *promotionv1.SetVoucherStatusRequest, _ ...grpc.CallOption) (*promotionv1.SetVoucherStatusResponse, error) {
	p.mu.Lock()
	p.lastSetVoucherStatus = in
	p.mu.Unlock()
	return &promotionv1.SetVoucherStatusResponse{
		Voucher: &promotionv1.Voucher{
			Id:     in.GetVoucherId(),
			Status: in.GetStatus(),
		},
	}, nil
}

func (p *w2bPromotionClient) DeleteVoucher(_ context.Context, in *promotionv1.DeleteVoucherRequest, _ ...grpc.CallOption) (*promotionv1.DeleteVoucherResponse, error) {
	p.mu.Lock()
	p.lastDeleteVoucher = in
	p.mu.Unlock()
	return &promotionv1.DeleteVoucherResponse{Ok: true}, nil
}

func (p *w2bPromotionClient) CreateFlashSale(_ context.Context, in *promotionv1.CreateFlashSaleRequest, _ ...grpc.CallOption) (*promotionv1.CreateFlashSaleResponse, error) {
	p.mu.Lock()
	p.lastCreateFlashSale = in
	p.mu.Unlock()
	return &promotionv1.CreateFlashSaleResponse{
		FlashSale: &promotionv1.FlashSale{
			Id:                 "fs-1",
			Name:               in.GetName(),
			StartsAt:           in.GetStartsAt(),
			EndsAt:             in.GetEndsAt(),
			MinDiscountPercent: in.GetMinDiscountPercent(),
			CategoryIds:        in.GetCategoryIds(),
		},
	}, nil
}

func (p *w2bPromotionClient) ListAllFlashSales(_ context.Context, in *promotionv1.ListAllFlashSalesRequest, _ ...grpc.CallOption) (*promotionv1.ListAllFlashSalesResponse, error) {
	p.mu.Lock()
	p.lastListAllFlashSales = in
	p.mu.Unlock()
	return &promotionv1.ListAllFlashSalesResponse{
		FlashSales: []*promotionv1.FlashSale{
			{
				Id:                 "fs-1",
				Name:               "11.11 Flash",
				MinDiscountPercent: 20,
				CategoryIds:        []string{"cat-1"},
			},
		},
		Total: 1,
	}, nil
}

func (p *w2bPromotionClient) ListFlashSaleItems(_ context.Context, in *promotionv1.ListFlashSaleItemsRequest, _ ...grpc.CallOption) (*promotionv1.ListFlashSaleItemsResponse, error) {
	p.mu.Lock()
	p.lastListFlashItems = in
	p.mu.Unlock()
	return &promotionv1.ListFlashSaleItemsResponse{
		Items: []*promotionv1.FlashSaleItem{
			{
				Id:               "fsi-1",
				FlashSaleId:      "fs-1",
				ShopId:           in.GetShopId(),
				SkuId:            "sku-1",
				FlashPriceVnd:    150000,
				OriginalPriceVnd: 250000,
				TotalSlots:       10,
				RemainingSlots:   8,
				PerUserLimit:     1,
				Status:           promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING,
				CreatedAt:        timestamppb.Now(),
			},
		},
		Total: 1,
	}, nil
}

func (p *w2bPromotionClient) ApproveFlashSaleItem(_ context.Context, in *promotionv1.ApproveFlashSaleItemRequest, _ ...grpc.CallOption) (*promotionv1.ApproveFlashSaleItemResponse, error) {
	p.mu.Lock()
	p.lastApproveFlash = in
	p.mu.Unlock()
	st := promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_REJECTED
	if in.GetApprove() {
		st = promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED
	}
	return &promotionv1.ApproveFlashSaleItemResponse{
		Item: &promotionv1.FlashSaleItem{
			Id:     in.GetItemId(),
			Status: st,
		},
	}, nil
}

func (p *w2bPromotionClient) CreateShopDiscountProgram(_ context.Context, in *promotionv1.CreateShopDiscountProgramRequest, _ ...grpc.CallOption) (*promotionv1.CreateShopDiscountProgramResponse, error) {
	p.mu.Lock()
	p.lastCreateProgram = in
	p.mu.Unlock()
	return &promotionv1.CreateShopDiscountProgramResponse{
		Program: &promotionv1.ShopDiscountProgram{
			Id:       "sdp-1",
			ShopId:   in.GetShopId(),
			Name:     in.GetName(),
			StartsAt: in.GetStartsAt(),
			EndsAt:   in.GetEndsAt(),
			Status:   promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE,
			Items: []*promotionv1.ShopDiscountProgramItem{
				{
					Id:                    "sdpi-1",
					ProgramId:             "sdp-1",
					ShopId:                in.GetShopId(),
					SkuId:                 "sku-1",
					OriginalPriceVnd:      250000,
					DiscountedPriceVnd:    200000,
					DiscountPercent:       20,
					PerUserLimit:          2,
					PriceInflationFlagged: true,
					Warning:               "price increased in 30d",
				},
			},
			Warnings: []string{"overlaps approved flash sale"},
		},
		Warnings: []string{"overlaps approved flash sale"},
	}, nil
}

func (p *w2bPromotionClient) UpdateShopDiscountProgram(_ context.Context, in *promotionv1.UpdateShopDiscountProgramRequest, _ ...grpc.CallOption) (*promotionv1.UpdateShopDiscountProgramResponse, error) {
	p.mu.Lock()
	p.lastUpdateProgram = in
	p.mu.Unlock()
	return &promotionv1.UpdateShopDiscountProgramResponse{
		Program: &promotionv1.ShopDiscountProgram{
			Id:     in.GetProgramId(),
			ShopId: "shop-a",
			Name:   in.GetName(),
			Status: promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE,
		},
		Warnings: []string{},
	}, nil
}

func (p *w2bPromotionClient) EndShopDiscountProgram(_ context.Context, in *promotionv1.EndShopDiscountProgramRequest, _ ...grpc.CallOption) (*promotionv1.EndShopDiscountProgramResponse, error) {
	p.mu.Lock()
	p.lastEndProgram = in
	p.mu.Unlock()
	return &promotionv1.EndShopDiscountProgramResponse{
		Program: &promotionv1.ShopDiscountProgram{
			Id:     in.GetProgramId(),
			ShopId: "shop-a",
			Status: promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ENDED,
		},
	}, nil
}

func (p *w2bPromotionClient) ListShopDiscountPrograms(_ context.Context, in *promotionv1.ListShopDiscountProgramsRequest, _ ...grpc.CallOption) (*promotionv1.ListShopDiscountProgramsResponse, error) {
	p.mu.Lock()
	p.lastListPrograms = in
	p.mu.Unlock()
	return &promotionv1.ListShopDiscountProgramsResponse{
		Programs: []*promotionv1.ShopDiscountProgram{
			{Id: "sdp-1", ShopId: in.GetShopId(), Name: "Weekend Deal", Status: promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE},
		},
		Total: 1,
	}, nil
}

func (p *w2bPromotionClient) ListActiveShopDiscounts(_ context.Context, in *promotionv1.ListActiveShopDiscountsRequest, _ ...grpc.CallOption) (*promotionv1.ListActiveShopDiscountsResponse, error) {
	p.mu.Lock()
	p.lastActiveDiscounts = in
	out := p.activeShopDiscounts
	p.mu.Unlock()
	return &promotionv1.ListActiveShopDiscountsResponse{Discounts: out}, nil
}

func (p *w2bPromotionClient) PreviewDiscounts(_ context.Context, in *promotionv1.PreviewDiscountsRequest, _ ...grpc.CallOption) (*promotionv1.PreviewDiscountsResponse, error) {
	p.mu.Lock()
	p.lastPreview = in
	p.mu.Unlock()
	var totalSub int64
	var warnings []string
	shops := make([]*promotionv1.ShopDiscountBreakdown, 0, len(in.GetShopItems()))
	for _, s := range in.GetShopItems() {
		totalSub += s.GetSubtotalVnd()
		var appliedIDs []string
		if s.GetShopId() == "shop-a" {
			appliedIDs = []string{"sdpi-real-1"}
			warnings = []string{"item limit reached for secondary line"}
		}
		shops = append(shops, &promotionv1.ShopDiscountBreakdown{
			ShopId:                    s.GetShopId(),
			SubtotalVnd:               s.GetSubtotalVnd(),
			ShopProgramDiscountVnd:    50000,
			ShopFreeshipDiscountVnd:   15000,
			ShippingFeeVnd:            s.GetShippingFeeVnd(),
			FinalShippingFeeVnd:       s.GetShippingFeeVnd() - 15000,
			FinalPayableVnd:           s.GetSubtotalVnd() - 50000 + s.GetShippingFeeVnd() - 15000,
			AppliedShopProgramItemIds: appliedIDs,
		})
	}
	return &promotionv1.PreviewDiscountsResponse{
		Breakdown: &promotionv1.DiscountBreakdown{
			Shops:                        shops,
			TotalSubtotalVnd:             totalSub,
			TotalShopProgramDiscountVnd:  50000,
			TotalShopFreeshipDiscountVnd: 15000,
			FinalPayableVnd:              totalSub - 50000,
			Warnings:                     warnings,
		},
	}, nil
}

type w2bOrderClient struct {
	orderv1.OrderServiceClient
	mu                 sync.Mutex
	sellerReportResp   *orderv1.GetSellerReportResponse
	platformReportResp *orderv1.GetPlatformReportResponse
	lastSellerReport   *orderv1.GetSellerReportRequest
	lastPlatformReport *orderv1.GetPlatformReportRequest
	lastNeedsReview    *orderv1.ListOrdersNeedingReviewRequest
	lastResolveReview  *orderv1.ResolveOrderReviewRequest
	lastCheckoutReq    *orderv1.CreateCheckoutRequest
	checkoutWarnings   []string
	orderByID          map[string]*orderv1.Order
}

func (o *w2bOrderClient) GetOrder(_ context.Context, in *orderv1.GetOrderRequest, _ ...grpc.CallOption) (*orderv1.GetOrderResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ord, ok := o.orderByID[in.GetOrderId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "order not found")
	}
	return &orderv1.GetOrderResponse{Order: ord}, nil
}

func (o *w2bOrderClient) ListOrders(_ context.Context, _ *orderv1.ListOrdersRequest, _ ...grpc.CallOption) (*orderv1.ListOrdersResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	ord := o.orderByID["ord-flagged"]
	return &orderv1.ListOrdersResponse{Orders: []*orderv1.Order{ord}, Total: 1}, nil
}

func (o *w2bOrderClient) CreateCheckout(_ context.Context, in *orderv1.CreateCheckoutRequest, _ ...grpc.CallOption) (*orderv1.CreateCheckoutResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastCheckoutReq = in
	ord := o.orderByID["ord-flagged"]
	return &orderv1.CreateCheckoutResponse{
		CheckoutGroupId: "cg-1",
		Orders:          []*orderv1.Order{ord},
		PaymentId:       "pay-1",
		PaymentMethod:   orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		TotalPayableVnd: ord.GetTotalPayableVnd(),
		Warnings:        append([]string(nil), o.checkoutWarnings...),
	}, nil
}

func (o *w2bOrderClient) RetryPayment(_ context.Context, _ *orderv1.RetryPaymentRequest, _ ...grpc.CallOption) (*orderv1.RetryPaymentResponse, error) {
	return &orderv1.RetryPaymentResponse{
		CheckoutGroupId: "cg-1",
		PaymentId:       "pay-multi-2",
	}, nil
}

func (o *w2bOrderClient) CancelOrder(_ context.Context, in *orderv1.CancelOrderRequest, _ ...grpc.CallOption) (*orderv1.CancelOrderResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return &orderv1.CancelOrderResponse{Order: o.orderByID[in.GetOrderId()]}, nil
}

func (o *w2bOrderClient) BuyerConfirmReceived(_ context.Context, in *orderv1.BuyerConfirmReceivedRequest, _ ...grpc.CallOption) (*orderv1.BuyerConfirmReceivedResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return &orderv1.BuyerConfirmReceivedResponse{Order: o.orderByID[in.GetOrderId()]}, nil
}

func (o *w2bOrderClient) RequestReturn(_ context.Context, in *orderv1.RequestReturnRequest, _ ...grpc.CallOption) (*orderv1.RequestReturnResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return &orderv1.RequestReturnResponse{Order: o.orderByID[in.GetOrderId()], ReturnRequestId: "rr-1"}, nil
}

func (o *w2bOrderClient) GetSellerReport(_ context.Context, in *orderv1.GetSellerReportRequest, _ ...grpc.CallOption) (*orderv1.GetSellerReportResponse, error) {
	o.mu.Lock()
	o.lastSellerReport = in
	resp := o.sellerReportResp
	o.mu.Unlock()
	return resp, nil
}

func (o *w2bOrderClient) GetPlatformReport(_ context.Context, in *orderv1.GetPlatformReportRequest, _ ...grpc.CallOption) (*orderv1.GetPlatformReportResponse, error) {
	o.mu.Lock()
	o.lastPlatformReport = in
	resp := o.platformReportResp
	o.mu.Unlock()
	return resp, nil
}

func (o *w2bOrderClient) ListOrdersNeedingReview(_ context.Context, in *orderv1.ListOrdersNeedingReviewRequest, _ ...grpc.CallOption) (*orderv1.ListOrdersNeedingReviewResponse, error) {
	o.mu.Lock()
	o.lastNeedsReview = in
	o.mu.Unlock()
	return &orderv1.ListOrdersNeedingReviewResponse{
		Orders: []*orderv1.Order{
			{
				Id:                      "ord-orphan-1",
				OrderCode:               "NM-ORPHAN-1",
				BuyerId:                 "buyer-1",
				ShopId:                  "shop-a",
				Status:                  orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
				ShopProgramDiscountVnd:  20000,
				ShopFreeshipDiscountVnd: 10000,
				PaymentFeeVnd:           3000,
				PaymentFeeBps:           150,
				NeedsManualReview:       true,
				Items: []*orderv1.OrderItem{
					{Id: "oi-1", SkuId: "sku-1", CommissionBps: 500, ShopProgramItemId: "sdpi-1"},
				},
			},
		},
		Total: 1,
	}, nil
}

func (o *w2bOrderClient) ResolveOrderReview(_ context.Context, in *orderv1.ResolveOrderReviewRequest, _ ...grpc.CallOption) (*orderv1.ResolveOrderReviewResponse, error) {
	o.mu.Lock()
	o.lastResolveReview = in
	o.mu.Unlock()
	return &orderv1.ResolveOrderReviewResponse{
		Order: &orderv1.Order{
			Id:                in.GetOrderId(),
			Status:            orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
			NeedsManualReview: false,
		},
	}, nil
}

type w2bPaymentClient struct {
	paymentv1.PaymentServiceClient
	mu            sync.Mutex
	lastLedgerReq *paymentv1.ListSellerLedgerRequest
	lastGetFeeReq *paymentv1.GetFeeConfigRequest
	lastSetFeeReq *paymentv1.SetFeeConfigRequest
	payments      map[string]*paymentv1.Payment
}

func (p *w2bPaymentClient) GetPayment(_ context.Context, in *paymentv1.GetPaymentRequest, _ ...grpc.CallOption) (*paymentv1.GetPaymentResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pay, ok := p.payments[in.GetPaymentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "payment not found")
	}
	return &paymentv1.GetPaymentResponse{Payment: pay}, nil
}

func (p *w2bPaymentClient) ListSellerLedger(_ context.Context, in *paymentv1.ListSellerLedgerRequest, _ ...grpc.CallOption) (*paymentv1.ListSellerLedgerResponse, error) {
	p.mu.Lock()
	p.lastLedgerReq = in
	p.mu.Unlock()
	return &paymentv1.ListSellerLedgerResponse{
		Entries: []*paymentv1.SellerLedgerEntry{
			{
				Id:            "le-1",
				TransactionId: "tx-1",
				AccountCode:   "seller_pending:" + in.GetShopId(),
				ReferenceType: "order_settlement",
				ReferenceId:   "ord-1",
				Description:   "Tiền đơn hàng",
				DebitVnd:      0,
				CreditVnd:     190000,
				DeltaVnd:      190000,
				CreatedAt:     timestamppb.Now(),
			},
		},
		Total: 1,
	}, nil
}

func (p *w2bPaymentClient) GetFeeConfig(_ context.Context, in *paymentv1.GetFeeConfigRequest, _ ...grpc.CallOption) (*paymentv1.GetFeeConfigResponse, error) {
	p.mu.Lock()
	p.lastGetFeeReq = in
	p.mu.Unlock()
	now := timestamppb.Now()
	item := &paymentv1.PaymentFeeConfigItem{
		Id:            "pfc-1",
		PaymentMethod: paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
		FeeBps:        150,
		EffectiveFrom: now,
		CreatedBy:     "admin-1",
		Note:          "default",
		CreatedAt:     now,
	}
	resp := &paymentv1.GetFeeConfigResponse{ActiveConfigs: []*paymentv1.PaymentFeeConfigItem{item}}
	if in.GetIncludeHistory() {
		resp.History = []*paymentv1.PaymentFeeConfigItem{item}
	}
	return resp, nil
}

func (p *w2bPaymentClient) SetFeeConfig(_ context.Context, in *paymentv1.SetFeeConfigRequest, _ ...grpc.CallOption) (*paymentv1.SetFeeConfigResponse, error) {
	p.mu.Lock()
	p.lastSetFeeReq = in
	p.mu.Unlock()
	eff := in.GetEffectiveFrom()
	if eff == nil {
		eff = timestamppb.Now()
	}
	return &paymentv1.SetFeeConfigResponse{
		Config: &paymentv1.PaymentFeeConfigItem{
			Id:            "pfc-new",
			PaymentMethod: in.GetPaymentMethod(),
			FeeBps:        in.GetFeeBps(),
			EffectiveFrom: eff,
			CreatedBy:     "admin-1",
			Note:          in.GetNote(),
			CreatedAt:     timestamppb.Now(),
		},
	}, nil
}

type w2bShippingClient struct {
	shippingv1.ShippingServiceClient
	mu                    sync.Mutex
	settingsByShop        map[string]*shippingv1.ShopShippingSettings
	getErr                error
	lastGetSettingsMD     metadata.MD
	lastUpdateSettingsReq *shippingv1.UpdateShopShippingSettingsRequest
	lastUpdateCarrier     *shippingv1.AdminUpdateCarrierRequest
	lastCalculateFeeReq   *shippingv1.CalculateFeeRequest
}

func (s *w2bShippingClient) GetShopShippingSettings(ctx context.Context, in *shippingv1.GetShopShippingSettingsRequest, _ ...grpc.CallOption) (*shippingv1.GetShopShippingSettingsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastGetSettingsMD, _ = metadata.FromOutgoingContext(ctx)
	if s.getErr != nil {
		return nil, s.getErr
	}
	if st, ok := s.settingsByShop[in.GetShopId()]; ok {
		return &shippingv1.GetShopShippingSettingsResponse{Settings: st}, nil
	}
	return &shippingv1.GetShopShippingSettingsResponse{
		Settings: &shippingv1.ShopShippingSettings{ShopId: in.GetShopId()},
	}, nil
}

func (s *w2bShippingClient) UpdateShopShippingSettings(_ context.Context, in *shippingv1.UpdateShopShippingSettingsRequest, _ ...grpc.CallOption) (*shippingv1.UpdateShopShippingSettingsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUpdateSettingsReq = in
	return &shippingv1.UpdateShopShippingSettingsResponse{
		Settings: &shippingv1.ShopShippingSettings{
			ShopId:              in.GetShopId(),
			EnabledCarrierCodes: in.GetEnabledCarrierCodes(),
			DefaultWeightGrams:  in.GetDefaultWeightGrams(),
			FreeShipEnabled:     in.GetFreeShipEnabled(),
			FreeShipMinOrderVnd: in.GetFreeShipMinOrderVnd(),
			FreeShipMaxVnd:      in.GetFreeShipMaxVnd(),
		},
	}, nil
}

func (s *w2bShippingClient) AdminUpdateCarrier(_ context.Context, in *shippingv1.AdminUpdateCarrierRequest, _ ...grpc.CallOption) (*shippingv1.AdminUpdateCarrierResponse, error) {
	s.mu.Lock()
	s.lastUpdateCarrier = in
	s.mu.Unlock()
	return &shippingv1.AdminUpdateCarrierResponse{
		Carrier: &shippingv1.CarrierConfig{
			CarrierCode: in.GetCarrierCode(),
			Name:        "Giao Hang Nhanh",
			Enabled:     in.GetEnabled(),
		},
	}, nil
}

func (s *w2bShippingClient) CalculateFee(_ context.Context, in *shippingv1.CalculateFeeRequest, _ ...grpc.CallOption) (*shippingv1.CalculateFeeResponse, error) {
	s.mu.Lock()
	s.lastCalculateFeeReq = in
	s.mu.Unlock()
	return &shippingv1.CalculateFeeResponse{
		Quotes: []*shippingv1.CarrierQuote{
			{
				CarrierCode:   "ghn",
				CarrierName:   "Giao Hang Nhanh",
				ServiceCode:   "standard",
				ServiceName:   "Tieu chuan",
				FeeVnd:        25000,
				EstimatedDays: 2,
			},
		},
	}, nil
}

func newW2BTestHarness(t *testing.T) (http.Handler, *auth.Manager, *w2bUserClient, *w2bProductClient, *w2bPromotionClient, *w2bOrderClient, *w2bPaymentClient, *w2bShippingClient) {
	t.Helper()
	authMgr, err := auth.NewManager("w2b-user-secret", 15*time.Minute)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}

	userMock := &w2bUserClient{}
	prodMock := &w2bProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a1": {Id: "sku-a1", ShopId: "shop-a", CategoryId: "cat-real", PriceVnd: 250000, MinPrice_30DVnd: 220000, IsActive: true},
			"sku-b1": {Id: "sku-b1", ShopId: "shop-b", CategoryId: "cat-other", PriceVnd: 100000, MinPrice_30DVnd: 100000, IsActive: true},
		},
	}
	promoMock := &w2bPromotionClient{
		activeShopDiscounts: []*promotionv1.ActiveShopDiscount{
			{
				ItemId:             "sdpi-real-1",
				ProgramId:          "sdp-1",
				ShopId:             "shop-a",
				SkuId:              "sku-a1",
				OriginalPriceVnd:   250000,
				DiscountedPriceVnd: 200000,
				DiscountPercent:    20,
				PerUserLimit:       2,
				StartsAt:           timestamppb.New(time.Now().UTC().Add(-time.Hour)),
				EndsAt:             timestamppb.New(time.Now().UTC().Add(time.Hour)),
			},
		},
	}
	orderMock := &w2bOrderClient{
		sellerReportResp: &orderv1.GetSellerReportResponse{
			ShopId:          "shop-a",
			FromDate:        "2026-11-20",
			ToDate:          "2026-11-21",
			GmvVnd:          500000,
			NetRevenueVnd:   450000,
			CommissionVnd:   25000,
			PaymentFeeVnd:   7500,
			TotalOrders:     3,
			CompletedOrders: 2,
			CancelledOrders: 1,
			ReturnedOrders:  0,
			CancelRateBps:   3333,
			ReturnRateBps:   0,
			OrdersByStatus: []*orderv1.OrderStatusCount{
				{Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED, Count: 2},
				{Status: orderv1.OrderStatus_ORDER_STATUS_CANCELLED, Count: 1},
			},
			Daily: []*orderv1.DailyReportPoint{
				{Date: "2026-11-20", OrderCount: 2, CompletedCount: 1, CancelledCount: 1, GmvVnd: 300000, NetRevenueVnd: 270000, CommissionVnd: 15000, PaymentFeeVnd: 4500, PlatformSubsidyVnd: 99999},
				{Date: "2026-11-21", OrderCount: 1, CompletedCount: 1, GmvVnd: 200000, NetRevenueVnd: -500, CommissionVnd: 10000, PaymentFeeVnd: 3000, PlatformSubsidyVnd: 88888},
			},
			TopProducts: []*orderv1.TopProductStat{
				{ProductId: "p-eq", ProductName: "=CMD|' /C calc'!A0", UnitsSold: 2, RevenueVnd: 200000, OrderCount: 1},
				{ProductId: "p-plus", ProductName: "+1+1", UnitsSold: 1, RevenueVnd: 100000, OrderCount: 1},
				{ProductId: "p-minus", ProductName: "-2+3", UnitsSold: 1, RevenueVnd: 50000, OrderCount: 1},
				{ProductId: "p-at", ProductName: "@SUM(A1:A2)", UnitsSold: 1, RevenueVnd: 50000, OrderCount: 1},
				{ProductId: "p-tab", ProductName: "\tTabInjection", UnitsSold: 1, RevenueVnd: 50000, OrderCount: 1},
				{ProductId: "p-cr", ProductName: "\rCRInjection", UnitsSold: 1, RevenueVnd: 50000, OrderCount: 1},
			},
		},
		platformReportResp: &orderv1.GetPlatformReportResponse{
			FromDate:           "2026-11-20",
			ToDate:             "2026-11-21",
			GmvVnd:             900000,
			NetRevenueVnd:      800000,
			CommissionVnd:      45000,
			PaymentFeeVnd:      13500,
			PlatformSubsidyVnd: 40000,
			TotalOrders:        5,
			CompletedOrders:    4,
			CancelledOrders:    1,
			ReturnedOrders:     0,
			CancelRateBps:      2000,
			OrdersByStatus: []*orderv1.OrderStatusCount{
				{Status: orderv1.OrderStatus_ORDER_STATUS_COMPLETED, Count: 4},
			},
			Daily: []*orderv1.DailyReportPoint{
				{Date: "2026-11-20", OrderCount: 3, CompletedCount: 2, CancelledCount: 1, GmvVnd: 500000, NetRevenueVnd: 450000, CommissionVnd: 25000, PaymentFeeVnd: 7500, PlatformSubsidyVnd: 25000},
				{Date: "2026-11-21", OrderCount: 2, CompletedCount: 2, GmvVnd: 400000, NetRevenueVnd: 350000, CommissionVnd: 20000, PaymentFeeVnd: 6000, PlatformSubsidyVnd: 15000},
			},
			TopProducts: []*orderv1.TopProductStat{
				{ProductId: "p-eq", ProductName: "=HYPERLINK(\"http://evil\",\"click\")", UnitsSold: 4, RevenueVnd: 400000, OrderCount: 2},
			},
		},
		orderByID: map[string]*orderv1.Order{
			"ord-flagged": {
				Id:                      "ord-flagged",
				OrderCode:               "NM-FLAG",
				BuyerId:                 "buyer-1",
				ShopId:                  "shop-a",
				Status:                  orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
				ShopProgramDiscountVnd:  15000,
				ShopFreeshipDiscountVnd: 10000,
				CommissionVnd:           12500,
				PaymentFeeVnd:           2500,
				PaymentFeeBps:           100,
				NeedsManualReview:       true,
				Items: []*orderv1.OrderItem{
					{Id: "oi-1", SkuId: "sku-a1", CommissionBps: 500, ShopProgramItemId: "sdpi-real-1"},
				},
			},
		},
	}
	paymentMock := &w2bPaymentClient{
		payments: map[string]*paymentv1.Payment{
			"pay-multi-1": {
				PaymentId:    "pay-multi-1",
				OrderGroupId: "cg-1",
				UserId:       "buyer-1",
				AmountVnd:    350000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				QrUrl:        "https://qr.example/pay-multi-1",
				QrCode:       "VIETQR-RAW-CODE-1",
				Provider:     "vietqr",
				ProviderRef:  "PROV-REF-1",
				ShopAllocations: []*paymentv1.ShopAllocation{
					{OrderId: "ord-a", ShopId: "shop-a", SubtotalVnd: 200000, CommissionVnd: 12000, ShippingFeeVnd: 20000, PlatformDiscountVnd: 10000},
					{OrderId: "ord-b", ShopId: "shop-b", SubtotalVnd: 150000, CommissionVnd: 9000, ShippingFeeVnd: 15000, PlatformDiscountVnd: 5000},
				},
			},
			"pay-multi-2": {
				PaymentId:    "pay-multi-2",
				OrderGroupId: "cg-1",
				UserId:       "buyer-1",
				AmountVnd:    350000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				QrUrl:        "https://qr.example/pay-multi-2",
				QrCode:       "VIETQR-RAW-CODE-2",
				Provider:     "vietqr",
				ProviderRef:  "PROV-REF-2",
				ShopAllocations: []*paymentv1.ShopAllocation{
					{OrderId: "ord-a", ShopId: "shop-a", SubtotalVnd: 200000, CommissionVnd: 12000, ShippingFeeVnd: 20000, PlatformDiscountVnd: 10000},
					{OrderId: "ord-b", ShopId: "shop-b", SubtotalVnd: 150000, CommissionVnd: 9000, ShippingFeeVnd: 15000, PlatformDiscountVnd: 5000},
				},
			},
			"pay-empty-user": {
				PaymentId:    "pay-empty-user",
				OrderGroupId: "cg-empty",
				UserId:       "",
				AmountVnd:    100000,
				Method:       paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
				Status:       paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
				ShopAllocations: []*paymentv1.ShopAllocation{
					{OrderId: "ord-a", ShopId: "shop-a", SubtotalVnd: 100000, CommissionVnd: 5000},
				},
			},
			"pay-nil": nil,
		},
	}
	shippingMock := &w2bShippingClient{
		settingsByShop: map[string]*shippingv1.ShopShippingSettings{
			"shop-a": {
				ShopId:              "shop-a",
				EnabledCarrierCodes: []string{"ghn"},
				DefaultWeightGrams:  400,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 300000,
				FreeShipMaxVnd:      15000,
			},
		},
	}

	h := New(Dependencies{
		ServiceName: "gateway-w2b-test",
		Clients: &clients.Clients{
			User:      userMock,
			Product:   prodMock,
			Promotion: promoMock,
			Order:     orderMock,
			Payment:   paymentMock,
			Shipping:  shippingMock,
			Timeout:   5 * time.Second,
		},
		PaymentSSEProxy: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: payment.pending\ndata: {\"payment_id\":\"pay-multi-1\"}\n\n"))
		}),
		AuthManager: authMgr,
		Logger:      logger.New("gateway-w2b-test", logger.WithWriter(io.Discard)),
	})
	return h, authMgr, userMock, prodMock, promoMock, orderMock, paymentMock, shippingMock
}

func TestW2BRBACMatrixEveryNewRoute(t *testing.T) {
	h, authMgr, _, _, _, _, _, _ := newW2BTestHarness(t)

	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	sellerNoShopTok, _, _, _ := authMgr.SignAccessToken("seller-0", "seller", "")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	do := func(method, path, token, body string) *httptest.ResponseRecorder {
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

	// Public route: accessible without token.
	if rec := do(http.MethodGet, "/api/v1/shop-discounts/active?sku_ids=sku-a1", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("public GET /shop-discounts/active: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	sellerRoutes := []struct {
		method, path, body string
		wantSellerStatus   int
	}{
		{http.MethodGet, "/api/v1/seller/bank-account", "", http.StatusOK},
		{http.MethodPost, "/api/v1/seller/bank-account", `{"bank_code":"VCB","bank_name":"Vietcombank","account_number":"01234567897890","account_holder_name":"NGUYEN VAN A"}`, http.StatusCreated},
		{http.MethodPost, "/api/v1/seller/bank-account/verify-otp", `{"otp":"123456"}`, http.StatusOK},
		{http.MethodPut, "/api/v1/seller/vouchers/v-1", `{"name":"Renamed"}`, http.StatusOK},
		{http.MethodPatch, "/api/v1/seller/vouchers/v-1/status", `{"status":"PAUSED"}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/seller/vouchers/v-1", "", http.StatusOK},
		{http.MethodGet, "/api/v1/seller/flash-sales?status=ACTIVE", "", http.StatusOK},
		{http.MethodGet, "/api/v1/seller/flash-sales/items?status=PENDING", "", http.StatusOK},
		{http.MethodGet, "/api/v1/seller/discount-programs?status=ACTIVE", "", http.StatusOK},
		{http.MethodPost, "/api/v1/seller/discount-programs", `{"name":"Weekend","starts_at":"2026-11-20T00:00:00Z","ends_at":"2026-11-25T00:00:00Z","items":[{"sku_id":"sku-a1","discounted_price_vnd":200000,"per_user_limit":2}]}`, http.StatusCreated},
		{http.MethodPut, "/api/v1/seller/discount-programs/sdp-1", `{"name":"Updated"}`, http.StatusOK},
		{http.MethodPost, "/api/v1/seller/discount-programs/sdp-1/end", "", http.StatusOK},
		{http.MethodGet, "/api/v1/seller/reports?from_date=2026-11-20&to_date=2026-11-21", "", http.StatusOK},
		{http.MethodGet, "/api/v1/seller/reports/export?from_date=2026-11-20&to_date=2026-11-21", "", http.StatusOK},
		{http.MethodGet, "/api/v1/seller/ledger?account_filter=pending", "", http.StatusOK},
	}

	for _, rt := range sellerRoutes {
		if rec := do(rt.method, rt.path, "", rt.body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s: want 401, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, buyerTok, rt.body); rec.Code != http.StatusForbidden {
			t.Fatalf("buyer %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, adminTok, rt.body); rec.Code != http.StatusForbidden {
			t.Fatalf("admin %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, sellerNoShopTok, rt.body); rec.Code != http.StatusForbidden {
			t.Fatalf("seller-no-shop %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, sellerTok, rt.body); rec.Code != rt.wantSellerStatus {
			t.Fatalf("seller %s %s: want %d, got %d (%s)", rt.method, rt.path, rt.wantSellerStatus, rec.Code, rec.Body.String())
		}
	}

	adminRoutes := []struct {
		method, path, body string
		wantAdminStatus    int
	}{
		{http.MethodGet, "/api/v1/admin/shops?bank_review_pending=true", "", http.StatusOK},
		{http.MethodGet, "/api/v1/admin/shops/shop-a/bank-account", "", http.StatusOK},
		{http.MethodPost, "/api/v1/admin/shops/shop-a/bank-account/review", `{"bank_account_id":"ba-1","decision":"APPROVE","reason":"verified"}`, http.StatusOK},
		{http.MethodGet, "/api/v1/admin/fees/categories?category_id=cat-1", "", http.StatusOK},
		{http.MethodPost, "/api/v1/admin/fees/categories/cat-1", `{"commission_rate_bps":600,"effective_from":"2026-12-01T00:00:00Z","note":"q4"}`, http.StatusCreated},
		{http.MethodGet, "/api/v1/admin/fees/payment?payment_method=VIETQR&include_history=true", "", http.StatusOK},
		{http.MethodPost, "/api/v1/admin/fees/payment", `{"payment_method":"VIETQR","fee_bps":150,"effective_from":"2026-12-01T00:00:00Z","note":"q4"}`, http.StatusCreated},
		{http.MethodGet, "/api/v1/admin/vouchers?scope=PLATFORM&include_inactive=true", "", http.StatusOK},
		{http.MethodPut, "/api/v1/admin/vouchers/v-1", `{"name":"Admin Rename","category_ids":["cat-1"],"replace_category_ids":true}`, http.StatusOK},
		{http.MethodPatch, "/api/v1/admin/vouchers/v-1/status", `{"status":"ACTIVE"}`, http.StatusOK},
		{http.MethodDelete, "/api/v1/admin/vouchers/v-1", "", http.StatusOK},
		{http.MethodGet, "/api/v1/admin/flash-sales?status=UPCOMING", "", http.StatusOK},
		{http.MethodGet, "/api/v1/admin/flash-sale-items?status=PENDING&shop_id=shop-a", "", http.StatusOK},
		{http.MethodGet, "/api/v1/admin/orders/needs-review", "", http.StatusOK},
		{http.MethodPost, "/api/v1/admin/orders/ord-orphan-1/resolve-review", `{"note":"resolved"}`, http.StatusOK},
		{http.MethodGet, "/api/v1/admin/reports?from_date=2026-11-20&to_date=2026-11-21", "", http.StatusOK},
		{http.MethodGet, "/api/v1/admin/reports/export?from_date=2026-11-20&to_date=2026-11-21", "", http.StatusOK},
	}

	for _, rt := range adminRoutes {
		if rec := do(rt.method, rt.path, "", rt.body); rec.Code != http.StatusUnauthorized {
			t.Fatalf("anon %s %s: want 401, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, buyerTok, rt.body); rec.Code != http.StatusForbidden {
			t.Fatalf("buyer %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, sellerTok, rt.body); rec.Code != http.StatusForbidden {
			t.Fatalf("seller %s %s: want 403, got %d", rt.method, rt.path, rec.Code)
		}
		if rec := do(rt.method, rt.path, adminTok, rt.body); rec.Code != rt.wantAdminStatus {
			t.Fatalf("admin %s %s: want %d, got %d (%s)", rt.method, rt.path, rt.wantAdminStatus, rec.Code, rec.Body.String())
		}
	}
}

func TestW2BPayloadsPresenceFlagsPrivacyAndManualReviewVisibility(t *testing.T) {
	h, authMgr, userMock, prodMock, promoMock, orderMock, paymentMock, shippingMock := newW2BTestHarness(t)

	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	do := func(method, path, token, body string) *httptest.ResponseRecorder {
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

	// 1. UpdateCategory presence flags: omitted fields stay false; explicit zero/empty sets true.
	rec := do(http.MethodPut, "/api/v1/admin/categories/cat-1", adminTok, `{"name":"Renamed Only"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT category partial: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if prodMock.lastUpdateCat.GetHasCommissionRateBps() || prodMock.lastUpdateCat.GetHasSortOrder() || prodMock.lastUpdateCat.GetHasParentId() {
		t.Fatalf("omitted category fields must have presence flags=false: %+v", prodMock.lastUpdateCat)
	}
	rec = do(http.MethodPut, "/api/v1/admin/categories/cat-1", adminTok, `{"name":"Full","parent_id":"","commission_rate_bps":0,"sort_order":0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT category explicit zero: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !prodMock.lastUpdateCat.GetHasCommissionRateBps() || !prodMock.lastUpdateCat.GetHasSortOrder() || !prodMock.lastUpdateCat.GetHasParentId() {
		t.Fatalf("explicit zero/empty category fields must have presence flags=true: %+v", prodMock.lastUpdateCat)
	}

	// 2. Product SKU includes min_price_30d_vnd.
	rec = do(http.MethodGet, "/api/v1/products/prod-1", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"min_price_30d_vnd":220000`) {
		t.Fatalf("GET /products/prod-1 missing min_price_30d_vnd: code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 3. Seller bank account responses NEVER leak raw account_number; shop_id forced from JWT.
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/seller/bank-account", ""},
		{http.MethodPost, "/api/v1/seller/bank-account", `{"shop_id":"shop-b","bank_code":"VCB","bank_name":"Vietcombank","account_number":"01234567897890","account_holder_name":"NGUYEN VAN A"}`},
		{http.MethodPost, "/api/v1/seller/bank-account/verify-otp", `{"shop_id":"shop-b","otp":"123456"}`},
	} {
		rec = do(tc.method, tc.path, sellerTok, tc.body)
		if strings.Contains(rec.Body.String(), "01234567897890") {
			t.Fatalf("%s %s leaked raw account_number: %s", tc.method, tc.path, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"account_number_masked":"****7890"`) {
			t.Fatalf("%s %s missing account_number_masked: %s", tc.method, tc.path, rec.Body.String())
		}
	}
	if userMock.lastAddBank.GetShopId() != "shop-a" || userMock.lastVerifyOTP.GetShopId() != "shop-a" || userMock.lastGetBank.GetShopId() != "shop-a" {
		t.Fatalf("seller bank account routes must force JWT shop_id=shop-a")
	}

	// 4. Seller shipping-settings freeship fields forwarded + forced JWT shop_id.
	rec = do(http.MethodPut, "/api/v1/seller/shipping-settings", sellerTok,
		`{"shop_id":"shop-b","enabled_carrier_codes":["ghn"],"default_weight_grams":450,"free_ship_enabled":true,"free_ship_min_order_vnd":299000,"free_ship_max_vnd":20000}`)
	if rec.Code != http.StatusOK ||
		shippingMock.lastUpdateSettingsReq.GetShopId() != "shop-a" ||
		!shippingMock.lastUpdateSettingsReq.GetFreeShipEnabled() ||
		shippingMock.lastUpdateSettingsReq.GetFreeShipMinOrderVnd() != 299000 ||
		shippingMock.lastUpdateSettingsReq.GetFreeShipMaxVnd() != 20000 ||
		!strings.Contains(rec.Body.String(), `"free_ship_enabled":true`) {
		t.Fatalf("PUT /seller/shipping-settings freeship mismatch: code=%d req=%+v body=%s", rec.Code, shippingMock.lastUpdateSettingsReq, rec.Body.String())
	}

	// 5. Seller ledger forces JWT shop_id and maps fields per contracts.md.
	rec = do(http.MethodGet, "/api/v1/seller/ledger?shop_id=shop-b&account_filter=pending&reference_type=order_settlement", sellerTok, "")
	if rec.Code != http.StatusOK || paymentMock.lastLedgerReq.GetShopId() != "shop-a" ||
		!strings.Contains(rec.Body.String(), `"delta_vnd":190000`) || !strings.Contains(rec.Body.String(), `"id":"le-1"`) {
		t.Fatalf("GET /seller/ledger mismatch: code=%d req=%+v body=%s", rec.Code, paymentMock.lastLedgerReq, rec.Body.String())
	}

	// 6. Order 2b fields + privacy:
	// - Buyer routes (and seller-as-buyer) ALWAYS zero commission_vnd, commission_bps, payment_fee_vnd, payment_fee_bps and hide needs_manual_review.
	// - Seller owner sees fees (commission/payment_fee) while needs_manual_review stays false.
	// - Admin sees fees AND needs_manual_review=true.
	sellerBTok, _, _, _ := authMgr.SignAccessToken("seller-b", "seller", "shop-b")
	assertOrderFeesHidden := func(label string, body []byte) {
		t.Helper()
		var wrap struct {
			Order  orderDTO   `json:"order"`
			Orders []orderDTO `json:"orders"`
			Items  []orderDTO `json:"items"`
		}
		if err := json.Unmarshal(body, &wrap); err != nil {
			t.Fatalf("%s decode: %v (%s)", label, err, string(body))
		}
		ord := wrap.Order
		if ord.ID == "" && len(wrap.Orders) > 0 {
			ord = wrap.Orders[0]
		}
		if ord.ID == "" && len(wrap.Items) > 0 {
			ord = wrap.Items[0]
		}
		if ord.ID == "" {
			t.Fatalf("%s missing order payload: %s", label, string(body))
		}
		if ord.CommissionVND != 0 || ord.PaymentFeeVND != 0 || ord.PaymentFeeBps != 0 || ord.NeedsManualReview {
			t.Fatalf("%s leaked seller/admin fields: comm=%d pay_fee=%d pay_bps=%d review=%v",
				label, ord.CommissionVND, ord.PaymentFeeVND, ord.PaymentFeeBps, ord.NeedsManualReview)
		}
		if len(ord.Items) == 0 || ord.Items[0].CommissionBps != 0 {
			t.Fatalf("%s leaked item commission_bps: %+v", label, ord.Items)
		}
		if ord.ShopProgramDiscountVND != 15000 || ord.ShopFreeshipDiscountVND != 10000 || ord.Items[0].ShopProgramItemID != "sdpi-real-1" {
			t.Fatalf("%s missing buyer-visible discount fields: %+v", label, ord)
		}
	}
	assertOrderFeesVisible := func(label string, body []byte, wantManualReview bool) {
		t.Helper()
		var wrap struct {
			Order orderDTO   `json:"order"`
			Items []orderDTO `json:"items"`
		}
		if err := json.Unmarshal(body, &wrap); err != nil {
			t.Fatalf("%s decode: %v (%s)", label, err, string(body))
		}
		ord := wrap.Order
		if ord.ID == "" && len(wrap.Items) > 0 {
			ord = wrap.Items[0]
		}
		if ord.CommissionVND != 12500 || ord.PaymentFeeVND != 2500 || ord.PaymentFeeBps != 100 ||
			len(ord.Items) == 0 || ord.Items[0].CommissionBps != 500 || ord.NeedsManualReview != wantManualReview {
			t.Fatalf("%s mismatch: comm=%d pay_fee=%d pay_bps=%d item_bps=%d review=%v (want %v)",
				label, ord.CommissionVND, ord.PaymentFeeVND, ord.PaymentFeeBps, ord.Items[0].CommissionBps, ord.NeedsManualReview, wantManualReview)
		}
	}

	// Buyer routes:
	reqCheckout := httptest.NewRequest(http.MethodPost, "/api/v1/checkout", strings.NewReader(`{"address_id":"a1","payment_method":"COD","items":[{"sku_id":"sku-a1","quantity":1}]}`))
	reqCheckout.Header.Set("Authorization", "Bearer "+buyerTok)
	reqCheckout.Header.Set("Idempotency-Key", "idem-buyer-privacy-1")
	recCheckout := httptest.NewRecorder()
	h.ServeHTTP(recCheckout, reqCheckout)
	if recCheckout.Code != http.StatusCreated {
		t.Fatalf("POST /checkout want 201, got %d (%s)", recCheckout.Code, recCheckout.Body.String())
	}
	assertOrderFeesHidden("buyer POST /checkout", recCheckout.Body.Bytes())
	assertOrderFeesHidden("buyer GET /orders", do(http.MethodGet, "/api/v1/orders", buyerTok, "").Body.Bytes())
	assertOrderFeesHidden("buyer GET /orders/{id}", do(http.MethodGet, "/api/v1/orders/ord-flagged", buyerTok, "").Body.Bytes())
	assertOrderFeesHidden("buyer POST /orders/{id}/cancel", do(http.MethodPost, "/api/v1/orders/ord-flagged/cancel", buyerTok, `{"reason":"changed mind"}`).Body.Bytes())
	assertOrderFeesHidden("buyer POST /orders/{id}/confirm-received", do(http.MethodPost, "/api/v1/orders/ord-flagged/confirm-received", buyerTok, "").Body.Bytes())
	assertOrderFeesHidden("buyer POST /orders/{id}/return", do(http.MethodPost, "/api/v1/orders/ord-flagged/return", buyerTok, `{"reason":"damaged"}`).Body.Bytes())
	// Seller B acting as buyer on Shop A's order via shared /orders/{id} route must also not see Shop A's fees!
	assertOrderFeesHidden("other-seller GET /orders/{id}", do(http.MethodGet, "/api/v1/orders/ord-flagged", sellerBTok, "").Body.Bytes())

	// Seller owner routes (and shared route as shop owner):
	assertOrderFeesVisible("seller GET /seller/orders", do(http.MethodGet, "/api/v1/seller/orders", sellerTok, "").Body.Bytes(), false)
	assertOrderFeesVisible("seller GET /seller/orders/{id}", do(http.MethodGet, "/api/v1/seller/orders/ord-flagged", sellerTok, "").Body.Bytes(), false)
	assertOrderFeesVisible("seller GET /orders/{id}", do(http.MethodGet, "/api/v1/orders/ord-flagged", sellerTok, "").Body.Bytes(), false)
	assertOrderFeesVisible("seller POST /orders/{id}/cancel", do(http.MethodPost, "/api/v1/orders/ord-flagged/cancel", sellerTok, `{"reason":"oos"}`).Body.Bytes(), false)

	// Admin routes:
	assertOrderFeesVisible("admin GET /orders/{id}", do(http.MethodGet, "/api/v1/orders/ord-flagged", adminTok, "").Body.Bytes(), true)
	assertOrderFeesVisible("admin GET /admin/orders", do(http.MethodGet, "/api/v1/admin/orders", adminTok, "").Body.Bytes(), true)

	// 6b. Admin shops filter bank_review_pending=true + admin-only bank_review_pending field (absent on public & seller routes).
	recAdminShops := do(http.MethodGet, "/api/v1/admin/shops?bank_review_pending=true", adminTok, "")
	if recAdminShops.Code != http.StatusOK || userMock.lastAdminShopsReq == nil || !userMock.lastAdminShopsReq.GetBankReviewPendingOnly() ||
		!strings.Contains(recAdminShops.Body.String(), `"bank_review_pending":true`) {
		t.Fatalf("GET /admin/shops?bank_review_pending=true mismatch: code=%d req=%+v body=%s", recAdminShops.Code, userMock.lastAdminShopsReq, recAdminShops.Body.String())
	}
	recAdminShopsAll := do(http.MethodGet, "/api/v1/admin/shops", adminTok, "")
	if recAdminShopsAll.Code != http.StatusOK || userMock.lastAdminShopsReq.GetBankReviewPendingOnly() {
		t.Fatalf("GET /admin/shops without param must set BankReviewPendingOnly=false: req=%+v", userMock.lastAdminShopsReq)
	}
	for _, tc := range []struct {
		label, path, tok string
	}{
		{"public GET /shops", "/api/v1/shops", ""},
		{"public GET /shops/{id}", "/api/v1/shops/shop-a", ""},
		{"seller GET /seller/shop", "/api/v1/seller/shop", sellerTok},
		{"auth GET /shops/me", "/api/v1/shops/me", sellerTok},
	} {
		recShop := do(http.MethodGet, tc.path, tc.tok, "")
		if recShop.Code != http.StatusOK {
			t.Fatalf("%s want 200, got %d (%s)", tc.label, recShop.Code, recShop.Body.String())
		}
		if strings.Contains(recShop.Body.String(), "bank_review_pending") {
			t.Fatalf("%s must NOT expose bank_review_pending field: %s", tc.label, recShop.Body.String())
		}
	}

	// 6c. GET /api/v1/shop-discounts/active requires sku_ids (<= 200) or shop_id -> 400 otherwise.
	promoMock.lastActiveDiscounts = nil
	if rec := do(http.MethodGet, "/api/v1/shop-discounts/active", "", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /shop-discounts/active without filter: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := do(http.MethodGet, "/api/v1/shop-discounts/active?sku_ids=,,+", "", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /shop-discounts/active with blank sku_ids: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	tooManySKUs := make([]string, 201)
	for i := range tooManySKUs {
		tooManySKUs[i] = fmt.Sprintf("sku-%d", i+1)
	}
	if rec := do(http.MethodGet, "/api/v1/shop-discounts/active?sku_ids="+strings.Join(tooManySKUs, ","), "", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("GET /shop-discounts/active with 201 sku_ids: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if promoMock.lastActiveDiscounts != nil {
		t.Fatalf("invalid /shop-discounts/active requests must fail at gateway before calling promotion")
	}
	if rec := do(http.MethodGet, "/api/v1/shop-discounts/active?shop_id=shop-a", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /shop-discounts/active?shop_id=shop-a: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// 7. Admin reports aggregates Order.GetPlatformReport + User.GetPlatformUserStats in {"report":{...,"user_stats":{...}}}.
	recRep := do(http.MethodGet, "/api/v1/admin/reports?from_date=2026-11-20&to_date=2026-11-21&top_limit=5", adminTok, "")
	if recRep.Code != http.StatusOK {
		t.Fatalf("GET /admin/reports want 200, got %d (%s)", recRep.Code, recRep.Body.String())
	}
	var adminRep struct {
		Report struct {
			GMVVND             int64 `json:"gmv_vnd"`
			PlatformSubsidyVND int64 `json:"platform_subsidy_vnd"`
			UserStats          struct {
				TotalUsers int64 `json:"total_users"`
				NewUsers   int64 `json:"new_users"`
				NewShops   int64 `json:"new_shops"`
			} `json:"user_stats"`
		} `json:"report"`
	}
	if err := json.Unmarshal(recRep.Body.Bytes(), &adminRep); err != nil {
		t.Fatalf("decode /admin/reports: %v (%s)", err, recRep.Body.String())
	}
	if adminRep.Report.GMVVND != 900000 || adminRep.Report.PlatformSubsidyVND != 40000 ||
		adminRep.Report.UserStats.TotalUsers != 120 || adminRep.Report.UserStats.NewUsers != 15 || adminRep.Report.UserStats.NewShops != 3 {
		t.Fatalf("unexpected /admin/reports payload: %+v", adminRep)
	}
	if orderMock.lastPlatformReport.GetTopLimit() != 5 || userMock.lastUserStats.GetFromDate() != "2026-11-20" {
		t.Fatalf("report query forwarding mismatch: order=%+v user=%+v", orderMock.lastPlatformReport, userMock.lastUserStats)
	}

	// 8. Admin create voucher forwards category_ids; admin create flash sale forwards min_discount_percent & category_ids;
	// seller list flash items forces shop_id from JWT.
	rec = do(http.MethodPost, "/api/v1/admin/vouchers", adminTok,
		`{"code":"CAT10","name":"Cat 10","scope":"PLATFORM","discount_type":"FIXED","value_vnd":10000,"quota":50,"starts_at":"2026-11-20T00:00:00Z","ends_at":"2026-12-01T00:00:00Z","category_ids":["cat-1","cat-2"]}`)
	if rec.Code != http.StatusCreated || len(promoMock.lastCreateVoucher.GetCategoryIds()) != 2 {
		t.Fatalf("POST /admin/vouchers category_ids mismatch: code=%d req=%+v", rec.Code, promoMock.lastCreateVoucher)
	}
	rec = do(http.MethodPost, "/api/v1/admin/flash-sales", adminTok,
		`{"name":"Mega","starts_at":"2026-11-20T00:00:00Z","ends_at":"2026-11-21T00:00:00Z","min_discount_percent":25,"category_ids":["cat-1"]}`)
	if rec.Code != http.StatusCreated || promoMock.lastCreateFlashSale.GetMinDiscountPercent() != 25 || len(promoMock.lastCreateFlashSale.GetCategoryIds()) != 1 {
		t.Fatalf("POST /admin/flash-sales mismatch: code=%d req=%+v", rec.Code, promoMock.lastCreateFlashSale)
	}
	rec = do(http.MethodGet, "/api/v1/seller/flash-sales/items?shop_id=shop-b&status=PENDING", sellerTok, "")
	if rec.Code != http.StatusOK || promoMock.lastListFlashItems.GetShopId() != "shop-a" || !strings.Contains(rec.Body.String(), `"original_price_vnd":250000`) {
		t.Fatalf("GET /seller/flash-sales/items mismatch: code=%d req=%+v body=%s", rec.Code, promoMock.lastListFlashItems, rec.Body.String())
	}

	// 9. Validation errors on invalid enum/status params.
	if rec := do(http.MethodPatch, "/api/v1/seller/vouchers/v-1/status", sellerTok, `{"status":"DELETED"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("PATCH voucher status DELETED: want 400, got %d", rec.Code)
	}
	if rec := do(http.MethodPost, "/api/v1/admin/fees/payment", adminTok, `{"payment_method":"CRYPTO","fee_bps":100}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("POST payment fee invalid method: want 400, got %d", rec.Code)
	}
}

func TestW2BPreviewDiscountsServerAuthoritativeIgnoresClientTampering(t *testing.T) {
	h, authMgr, _, prodMock, promoMock, _, _, shippingMock := newW2BTestHarness(t)
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")

	// Client attempts to tamper unit_price_vnd, subtotal_vnd, category_id, shop_discount_program_item_id, and free_ship_*!
	tamperedBody := `{
		"shop_items": [{
			"shop_id": "shop-a",
			"subtotal_vnd": 1,
			"shipping_fee_vnd": 30000,
			"free_ship_enabled": false,
			"free_ship_min_order_vnd": 99999999,
			"free_ship_max_vnd": 99999999,
			"items": [{
				"sku_id": "sku-a1",
				"quantity": 2,
				"unit_price_vnd": 1,
				"category_id": "cat-spoofed",
				"shop_discount_program_item_id": "sdpi-spoofed"
			}]
		}],
		"shop_voucher_ids": {"shop-a": "v-shop-1"},
		"platform_voucher_id": "v-plat-1",
		"freeship_voucher_id": "v-fs-1",
		"coins_to_use": 1000
	}`

	req := httptest.NewRequest(http.MethodPost, "/api/v1/promotions/preview", strings.NewReader(tamperedBody))
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /promotions/preview want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// Verify Product.GetSKUsBatch was called for sku-a1.
	if prodMock.lastGetSKUsBatch == nil || len(prodMock.lastGetSKUsBatch.GetSkuIds()) != 1 || prodMock.lastGetSKUsBatch.GetSkuIds()[0] != "sku-a1" {
		t.Fatalf("expected GetSKUsBatch([sku-a1]), got %+v", prodMock.lastGetSKUsBatch)
	}
	// Verify Shipping.GetShopShippingSettings forwarded the buyer's Bearer JWT (never a service token!).
	authVals := shippingMock.lastGetSettingsMD.Get(auth.MDAuthorization)
	if len(authVals) == 0 {
		t.Fatalf("expected buyer Authorization header on GetShopShippingSettings, got empty metadata")
	}
	if authVals[0] != "Bearer "+buyerTok {
		t.Fatalf("GetShopShippingSettings must forward buyer Bearer JWT, got %q", authVals[0])
	}
	buyerClaims, err := authMgr.VerifyAccessToken(strings.TrimPrefix(authVals[0], "Bearer "))
	if err != nil || buyerClaims.UserID() != "buyer-1" || buyerClaims.Role != "buyer" {
		t.Fatalf("GetShopShippingSettings forwarded unexpected token claims: err=%v claims=%+v", err, buyerClaims)
	}

	// Verify Promotion.PreviewDiscounts received authoritative shop_id and freeship while dropping
	// client-tampered price/subtotal/category/program IDs (D4: promotion resolves prices/categories itself).
	if promoMock.lastActiveDiscounts != nil {
		t.Fatalf("handlePreviewDiscounts must NOT call ListActiveShopDiscounts; got %+v", promoMock.lastActiveDiscounts)
	}
	got := promoMock.lastPreview
	if got == nil || len(got.GetShopItems()) != 1 {
		t.Fatalf("unexpected PreviewDiscountsRequest: %+v", got)
	}
	si := got.GetShopItems()[0]
	if si.GetShopId() != "shop-a" || si.GetSubtotalVnd() != 0 {
		t.Fatalf("expected authoritative shop_id=shop-a and omitted subtotal_vnd=0 (D4), got shop=%q subtotal=%d", si.GetShopId(), si.GetSubtotalVnd())
	}
	if !si.GetFreeShipEnabled() || si.GetFreeShipMinOrderVnd() != 300000 || si.GetFreeShipMaxVnd() != 15000 {
		t.Fatalf("expected authoritative freeship (true, 300000, 15000), got (%v, %d, %d)", si.GetFreeShipEnabled(), si.GetFreeShipMinOrderVnd(), si.GetFreeShipMaxVnd())
	}
	if len(si.GetItems()) != 1 {
		t.Fatalf("expected 1 line item, got %d", len(si.GetItems()))
	}
	li := si.GetItems()[0]
	if li.GetSkuId() != "sku-a1" || li.GetQuantity() != 2 || li.GetUnitPriceVnd() != 0 || li.GetCategoryId() != "" || li.GetShopDiscountProgramItemId() != "" {
		t.Fatalf("line item must drop tampered unit_price/category/program_id under D4: %+v", li)
	}

	// Verify response JSON includes new breakdown fields (including applied_shop_program_item_ids and warnings).
	if !strings.Contains(rec.Body.String(), `"total_shop_program_discount_vnd":50000`) ||
		!strings.Contains(rec.Body.String(), `"total_shop_freeship_discount_vnd":15000`) ||
		!strings.Contains(rec.Body.String(), `"shop_program_discount_vnd":50000`) ||
		!strings.Contains(rec.Body.String(), `"shop_freeship_discount_vnd":15000`) ||
		!strings.Contains(rec.Body.String(), `"applied_shop_program_item_ids":["sdpi-real-1"]`) ||
		!strings.Contains(rec.Body.String(), `"warnings":["item limit reached for secondary line"]`) {
		t.Fatalf("preview response missing 2b breakdown fields: %s", rec.Body.String())
	}

	// Shipping.GetShopShippingSettings NotFound (shop-b has no settings) -> treated as freeship disabled without error,
	// and nil applied_shop_program_item_ids + nil warnings serialize as [] (never null).
	noSettingsReq := httptest.NewRequest(http.MethodPost, "/api/v1/promotions/preview", strings.NewReader(`{
		"shop_items": [{"shop_id":"shop-b","shipping_fee_vnd":20000,"free_ship_enabled":true,"free_ship_max_vnd":20000,"items":[{"sku_id":"sku-b1","quantity":1}]}]
	}`))
	noSettingsReq.Header.Set("Authorization", "Bearer "+buyerTok)
	recNoSettings := httptest.NewRecorder()
	h.ServeHTTP(recNoSettings, noSettingsReq)
	if recNoSettings.Code != http.StatusOK {
		t.Fatalf("preview when shipping settings NotFound: want 200, got %d (%s)", recNoSettings.Code, recNoSettings.Body.String())
	}
	if !strings.Contains(recNoSettings.Body.String(), `"applied_shop_program_item_ids":[]`) ||
		!strings.Contains(recNoSettings.Body.String(), `"warnings":[]`) {
		t.Fatalf("preview empty applied_shop_program_item_ids/warnings must serialize as [] (not null): %s", recNoSettings.Body.String())
	}
	if gotB := promoMock.lastPreview.GetShopItems(); len(gotB) != 1 || gotB[0].GetFreeShipEnabled() || gotB[0].GetFreeShipMaxVnd() != 0 {
		t.Fatalf("preview when shipping settings NotFound must treat freeship as disabled, got %+v", gotB)
	}

	// Cross-shop SKU spoofing (sku-b1 claimed under shop-a) -> 400.
	spoofShopReq := httptest.NewRequest(http.MethodPost, "/api/v1/promotions/preview", strings.NewReader(`{
		"shop_items": [{"shop_id":"shop-a","shipping_fee_vnd":20000,"items":[{"sku_id":"sku-b1","quantity":1}]}]
	}`))
	spoofShopReq.Header.Set("Authorization", "Bearer "+buyerTok)
	recSpoof := httptest.NewRecorder()
	h.ServeHTTP(recSpoof, spoofShopReq)
	if recSpoof.Code != http.StatusBadRequest {
		t.Fatalf("cross-shop SKU spoof in preview: want 400, got %d (%s)", recSpoof.Code, recSpoof.Body.String())
	}

	// Unknown SKU -> uniform 400 INVALID_ARGUMENT (indistinguishable from cross-shop SKU; P2-3).
	missingSKUReq := httptest.NewRequest(http.MethodPost, "/api/v1/promotions/preview", strings.NewReader(`{
		"shop_items": [{"shop_id":"shop-a","shipping_fee_vnd":20000,"items":[{"sku_id":"sku-nonexistent","quantity":1}]}]
	}`))
	missingSKUReq.Header.Set("Authorization", "Bearer "+buyerTok)
	recMissing := httptest.NewRecorder()
	h.ServeHTTP(recMissing, missingSKUReq)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("unknown SKU in preview: want 400, got %d (%s)", recMissing.Code, recMissing.Body.String())
	}
}

func TestW2BReportsCSVExportBOMInjectionGuardHeadersAndSizeLimit(t *testing.T) {
	h, authMgr, _, _, _, _, _, _ := newW2BTestHarness(t)
	sellerTok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	utf8BOM := []byte{0xEF, 0xBB, 0xBF}

	// 1. Seller CSV export: BOM, headers, RFC-4180 parseable, negative integer preserved, formula cells neutralized.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/seller/reports/export?from_date=2026-11-20&to_date=2026-11-21", nil)
	req.Header.Set("Authorization", "Bearer "+sellerTok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /seller/reports/export: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") || !strings.Contains(strings.ToLower(ct), "utf-8") {
		t.Fatalf("Content-Type = %q, want text/csv; charset=utf-8", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, ".csv") {
		t.Fatalf("Content-Disposition = %q, want attachment with .csv filename", cd)
	}
	raw := rec.Body.Bytes()
	if !bytes.HasPrefix(raw, utf8BOM) {
		t.Fatalf("seller CSV missing UTF-8 BOM prefix, first bytes=%x", raw[:min(6, len(raw))])
	}
	rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw, utf8BOM))).ReadAll()
	if err != nil {
		t.Fatalf("seller CSV must be valid RFC-4180 CSV: %v\n%s", err, string(raw))
	}
	if len(rows) < 5 {
		t.Fatalf("expected summary + status + daily + top_product rows, got %d", len(rows))
	}

	// Check every dangerous prefix was neutralized with leading single quote `'`, while pure negative integer "-500" stays intact.
	wantCells := map[string]bool{
		"-500":                false,
		"'=CMD|' /C calc'!A0": false,
		"'+1+1":               false,
		"'-2+3":               false,
		"'@SUM(A1:A2)":        false,
		"'\tTabInjection":     false,
		"'\rCRInjection":      false,
	}
	for _, row := range rows {
		for _, cell := range row {
			if _, tracked := wantCells[cell]; tracked {
				wantCells[cell] = true
			}
			if cell == "'-500" {
				t.Fatalf("negative integer -500 must NOT be quoted as text formula")
			}
			if cell != "" && !isPureInteger(cell) && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
				t.Fatalf("unneutralized CSV formula cell found: %q", cell)
			}
		}
	}
	for expected, seen := range wantCells {
		if !seen {
			t.Fatalf("expected CSV cell %q not found in output:\n%s", expected, string(raw))
		}
	}

	// 2. Direct renderCSVWithLimit cell cases: pure integers preserved; formulas/phone numbers/mixed strings neutralized.
	csvCases := []struct {
		in   string
		want string
	}{
		{"-500", "-500"},
		{"0", "0"},
		{"123456", "123456"},
		{"-1+2", "'-1+2"},
		{"=1", "'=1"},
		{"+84901234567", "'+84901234567"},
		{"@x", "'@x"},
		{"\tindented", "'\tindented"},
		{"\rcarriage", "'\rcarriage"},
		{"-", "'-"},
		{"-12.5", "'-12.5"},
	}
	inRow := make([]string, len(csvCases))
	hdrRow := make([]string, len(csvCases))
	for i, tc := range csvCases {
		hdrRow[i] = "c"
		inRow[i] = tc.in
	}
	rendered, err := renderCSVWithLimit(hdrRow, [][]string{inRow}, maxCSVExportBytes)
	if err != nil {
		t.Fatalf("renderCSVWithLimit direct cases: %v", err)
	}
	parsed, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(rendered, utf8BOM))).ReadAll()
	if err != nil || len(parsed) != 2 {
		t.Fatalf("parse direct CSV cases: err=%v rows=%d", err, len(parsed))
	}
	for i, tc := range csvCases {
		if got := parsed[1][i]; got != tc.want {
			t.Fatalf("renderCSVWithLimit(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}

	// 3. Admin CSV export: BOM, attachment header, sanitized hyperlink, includes user stats.
	reqAdmin := httptest.NewRequest(http.MethodGet, "/api/v1/admin/reports/export?from_date=2026-11-20&to_date=2026-11-21", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+adminTok)
	recAdmin := httptest.NewRecorder()
	h.ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusOK {
		t.Fatalf("GET /admin/reports/export: want 200, got %d (%s)", recAdmin.Code, recAdmin.Body.String())
	}
	if !strings.HasPrefix(recAdmin.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("admin CSV missing attachment Content-Disposition: %q", recAdmin.Header().Get("Content-Disposition"))
	}
	adminRaw := recAdmin.Body.Bytes()
	if !bytes.HasPrefix(adminRaw, utf8BOM) {
		t.Fatalf("admin CSV missing UTF-8 BOM")
	}
	adminRows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(adminRaw, utf8BOM))).ReadAll()
	if err != nil {
		t.Fatalf("admin CSV invalid RFC-4180: %v", err)
	}
	foundHyperlinkGuarded := false
	for _, row := range adminRows {
		for _, cell := range row {
			if strings.HasPrefix(cell, "'=HYPERLINK(") {
				foundHyperlinkGuarded = true
			}
			if cell != "" && !isPureInteger(cell) && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
				t.Fatalf("unneutralized admin CSV cell: %q", cell)
			}
		}
	}
	if !foundHyperlinkGuarded {
		t.Fatalf("expected neutralized HYPERLINK formula in admin CSV:\n%s", string(adminRaw))
	}

	// 4. Direct CSV size-limit enforcement check.
	if _, err := renderCSVWithLimit([]string{"a", "b"}, [][]string{{strings.Repeat("x", 100), "y"}}, 64); err == nil {
		t.Fatalf("expected size limit error when rendered CSV exceeds maxBytes")
	}
}

func TestW2BRound4ReviewFixes(t *testing.T) {
	h, authMgr, _, _, promoMock, orderMock, _, shippingMock := newW2BTestHarness(t)
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	otherBuyerTok, _, _, _ := authMgr.SignAccessToken("buyer-2", "buyer", "")
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-a", "seller", "shop-a")
	sellerCTok, _, _, _ := authMgr.SignAccessToken("seller-c", "seller", "shop-c")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	do := func(method, path, token, body string, extraHeaders map[string]string) *httptest.ResponseRecorder {
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
		for k, v := range extraHeaders {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. [P2 security] sanitizeCSVCell handles '\n' and leading whitespace before = + - @ while preserving pure integers.
	t.Run("CSVNewlineAndLeadingWhitespaceFormulaGuard", func(t *testing.T) {
		cases := []struct {
			in   string
			want string
		}{
			{"-500", "-500"},
			{"0", "0"},
			{"hello", "hello"},
			{"  hello", "  hello"},
			{"\n=1", "'\n=1"},
			{"\nplain", "'\nplain"},
			{"  =CMD|calc", "'  =CMD|calc"},
			{" \t+84901234567", "' \t+84901234567"},
			{" \r\n-1+2", "' \r\n-1+2"},
			{"  @SUM(A1)", "'  @SUM(A1)"},
			{" -500", "' -500"},
		}
		for _, tc := range cases {
			if got := sanitizeCSVCell(tc.in); got != tc.want {
				t.Fatalf("sanitizeCSVCell(%q): want %q, got %q", tc.in, tc.want, got)
			}
		}
	})

	// 2. [P2] GET /admin/vouchers defaults include_inactive=true when both include_inactive & status are omitted.
	t.Run("AdminListVouchersDefaultIncludeInactive", func(t *testing.T) {
		if rec := do(http.MethodGet, "/api/v1/admin/vouchers", adminTok, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("GET /admin/vouchers want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		if !promoMock.lastListVouchers.GetIncludeInactive() {
			t.Fatalf("GET /admin/vouchers with no params must default include_inactive=true")
		}
		if rec := do(http.MethodGet, "/api/v1/admin/vouchers?status=ACTIVE", adminTok, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("GET /admin/vouchers?status=ACTIVE want 200, got %d", rec.Code)
		}
		if promoMock.lastListVouchers.GetIncludeInactive() {
			t.Fatalf("GET /admin/vouchers?status=ACTIVE must keep include_inactive=false")
		}
		if rec := do(http.MethodGet, "/api/v1/admin/vouchers?include_inactive=false", adminTok, "", nil); rec.Code != http.StatusOK {
			t.Fatalf("GET /admin/vouchers?include_inactive=false want 200, got %d", rec.Code)
		}
		if promoMock.lastListVouchers.GetIncludeInactive() {
			t.Fatalf("GET /admin/vouchers?include_inactive=false must respect explicit false")
		}
	})

	// 3. [P2 money] Required numeric/fee/money fields must return 400 when omitted (not silently 0); explicit 0 allowed where valid.
	t.Run("RequiredMoneyFeeQuotaFieldsRejectOmittedWith400", func(t *testing.T) {
		// Category fee schedule: missing commission_rate_bps -> 400; explicit 0 -> 201.
		if rec := do(http.MethodPost, "/api/v1/admin/fees/categories/cat-1", adminTok, `{"note":"missing bps"}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /admin/fees/categories missing commission_rate_bps: want 400, got %d (%s)", rec.Code, rec.Body.String())
		}
		if rec := do(http.MethodPost, "/api/v1/admin/fees/categories/cat-1", adminTok, `{"commission_rate_bps":0,"note":"zero promo"}`, nil); rec.Code != http.StatusCreated {
			t.Fatalf("POST /admin/fees/categories explicit 0 bps: want 201, got %d (%s)", rec.Code, rec.Body.String())
		}
		// Payment fee config: missing fee_bps -> 400; explicit 0 -> 201.
		if rec := do(http.MethodPost, "/api/v1/admin/fees/payment", adminTok, `{"payment_method":"COD","note":"missing fee"}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /admin/fees/payment missing fee_bps: want 400, got %d (%s)", rec.Code, rec.Body.String())
		}
		if rec := do(http.MethodPost, "/api/v1/admin/fees/payment", adminTok, `{"payment_method":"COD","fee_bps":0,"note":"zero cod"}`, nil); rec.Code != http.StatusCreated {
			t.Fatalf("POST /admin/fees/payment explicit 0 bps: want 201, got %d (%s)", rec.Code, rec.Body.String())
		}
		// Seller shipping-settings: free_ship_enabled=true with omitted free_ship_max_vnd or free_ship_min_order_vnd -> 400.
		if rec := do(http.MethodPut, "/api/v1/seller/shipping-settings", sellerATok, `{"enabled_carrier_codes":["ghn"],"free_ship_enabled":true,"free_ship_min_order_vnd":100000}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT /seller/shipping-settings missing free_ship_max_vnd: want 400, got %d", rec.Code)
		}
		if rec := do(http.MethodPut, "/api/v1/seller/shipping-settings", sellerATok, `{"enabled_carrier_codes":["ghn"],"free_ship_enabled":true,"free_ship_max_vnd":15000}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("PUT /seller/shipping-settings missing free_ship_min_order_vnd: want 400, got %d", rec.Code)
		}
		// Create voucher: missing quota or value_vnd -> 400.
		if rec := do(http.MethodPost, "/api/v1/seller/vouchers", sellerATok, `{"code":"NOQ","name":"No Quota","discount_type":"FIXED","value_vnd":10000}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /seller/vouchers missing quota: want 400, got %d (%s)", rec.Code, rec.Body.String())
		}
		if rec := do(http.MethodPost, "/api/v1/admin/vouchers", adminTok, `{"code":"NOV","name":"No Value","discount_type":"FIXED","quota":10}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /admin/vouchers missing value_vnd: want 400, got %d (%s)", rec.Code, rec.Body.String())
		}
		// Register flash sale item: missing flash_price_vnd or total_slots -> 400.
		if rec := do(http.MethodPost, "/api/v1/seller/flash-sales/fs-1/items", sellerATok, `{"sku_id":"sku-a1","total_slots":5}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST flash sale item missing flash_price_vnd: want 400, got %d", rec.Code)
		}
		if rec := do(http.MethodPost, "/api/v1/seller/flash-sales/fs-1/items", sellerATok, `{"sku_id":"sku-a1","flash_price_vnd":150000}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST flash sale item missing total_slots: want 400, got %d", rec.Code)
		}
		// Admin refund & credit coins: missing amount -> 400.
		if rec := do(http.MethodPost, "/api/v1/admin/payments/pay-1/refund", adminTok, `{"order_id":"ord-1","reason":"r"}`, map[string]string{"Idempotency-Key": "idem-r4-1"}); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /admin/payments/{id}/refund missing amount_vnd: want 400, got %d", rec.Code)
		}
		if rec := do(http.MethodPost, "/api/v1/admin/coins/credit", adminTok, `{"user_id":"buyer-1","reason":"bonus","reference_id":"ref-1"}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("POST /admin/coins/credit missing amount: want 400, got %d", rec.Code)
		}
		// Seller update stock: missing new_stock or expected_version -> 400.
		if rec := do(http.MethodPatch, "/api/v1/seller/skus/sku-a1/stock", sellerATok, `{"expected_version":1}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH /seller/skus/{id}/stock missing new_stock: want 400, got %d", rec.Code)
		}
		if rec := do(http.MethodPatch, "/api/v1/seller/skus/sku-a1/stock", sellerATok, `{"new_stock":5}`, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("PATCH /seller/skus/{id}/stock missing expected_version: want 400, got %d", rec.Code)
		}
	})

	// 4. Checkout: returns warnings ([] when empty, populated when present) & ignores client shop_program_item_id.
	t.Run("CheckoutWarningsAndIgnoresClientShopProgramItemID", func(t *testing.T) {
		orderMock.checkoutWarnings = nil
		rec := do(http.MethodPost, "/api/v1/checkout", buyerTok,
			`{"address_id":"a1","payment_method":"COD","items":[{"sku_id":"sku-a1","quantity":1,"shop_program_item_id":"spoofed-prog-id"}]}`,
			map[string]string{"Idempotency-Key": "idem-r4-co-1"})
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST /checkout with extra shop_program_item_id: want 201, got %d (%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"warnings":[]`) {
			t.Fatalf("POST /checkout empty warnings must be []: %s", rec.Body.String())
		}
		if orderMock.lastCheckoutReq == nil || len(orderMock.lastCheckoutReq.GetItems()) != 1 || orderMock.lastCheckoutReq.GetItems()[0].GetShopProgramItemId() != "" {
			t.Fatalf("POST /checkout must NOT forward client shop_program_item_id, got %+v", orderMock.lastCheckoutReq)
		}

		orderMock.checkoutWarnings = []string{"shop discount per_user_limit reached"}
		rec2 := do(http.MethodPost, "/api/v1/checkout", buyerTok,
			`{"address_id":"a1","payment_method":"COD","items":[{"sku_id":"sku-a1","quantity":1}]}`,
			map[string]string{"Idempotency-Key": "idem-r4-co-2"})
		if rec2.Code != http.StatusCreated || !strings.Contains(rec2.Body.String(), `"warnings":["shop discount per_user_limit reached"]`) {
			t.Fatalf("POST /checkout missing populated warnings: code=%d body=%s", rec2.Code, rec2.Body.String())
		}
		orderMock.checkoutWarnings = nil
	})

	// 5. Admin JSON & CSV include daily platform_subsidy_vnd; Seller JSON excludes it.
	t.Run("AdminDailyPlatformSubsidyJSONAndCSVAndAbsentOnSeller", func(t *testing.T) {
		recAdminJSON := do(http.MethodGet, "/api/v1/admin/reports?from_date=2026-11-20&to_date=2026-11-21", adminTok, "", nil)
		if recAdminJSON.Code != http.StatusOK || !strings.Contains(recAdminJSON.Body.String(), `"platform_subsidy_vnd":25000`) || !strings.Contains(recAdminJSON.Body.String(), `"platform_subsidy_vnd":15000`) {
			t.Fatalf("GET /admin/reports missing daily platform_subsidy_vnd: code=%d body=%s", recAdminJSON.Code, recAdminJSON.Body.String())
		}

		recSellerJSON := do(http.MethodGet, "/api/v1/seller/reports?from_date=2026-11-20&to_date=2026-11-21", sellerATok, "", nil)
		if recSellerJSON.Code != http.StatusOK || strings.Contains(recSellerJSON.Body.String(), "platform_subsidy_vnd") {
			t.Fatalf("GET /seller/reports must NOT expose platform_subsidy_vnd: code=%d body=%s", recSellerJSON.Code, recSellerJSON.Body.String())
		}

		recAdminCSV := do(http.MethodGet, "/api/v1/admin/reports/export?from_date=2026-11-20&to_date=2026-11-21", adminTok, "", nil)
		if recAdminCSV.Code != http.StatusOK {
			t.Fatalf("GET /admin/reports/export want 200, got %d", recAdminCSV.Code)
		}
		rows, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(recAdminCSV.Body.Bytes(), utf8BOM))).ReadAll()
		if err != nil {
			t.Fatalf("parse admin CSV: %v", err)
		}
		subIdx := -1
		for i, col := range rows[0] {
			if col == "platform_subsidy_vnd" {
				subIdx = i
				break
			}
		}
		if subIdx < 0 {
			t.Fatalf("admin CSV header missing platform_subsidy_vnd: %v", rows[0])
		}
		foundDaily20, foundDaily21 := false, false
		for _, row := range rows[1:] {
			if row[0] == "daily" && row[1] == "2026-11-20" && row[subIdx] == "25000" {
				foundDaily20 = true
			}
			if row[0] == "daily" && row[1] == "2026-11-21" && row[subIdx] == "15000" {
				foundDaily21 = true
			}
		}
		if !foundDaily20 || !foundDaily21 {
			t.Fatalf("admin CSV daily rows missing platform_subsidy_vnd values (25000, 15000):\n%s", recAdminCSV.Body.String())
		}
	})

	// 6. Payment privacy on GET /payments/{id}, POST /payments/{id}/retry, and SSE /payments/{id}/events.
	t.Run("PaymentPrivacyGetRetryAndSSE", func(t *testing.T) {
		// Buyer GET /payments/{id}: sees both shop allocations but commission_vnd MUST be 0 on all.
		recBuyerGet := do(http.MethodGet, "/api/v1/payments/pay-multi-1", buyerTok, "", nil)
		if recBuyerGet.Code != http.StatusOK {
			t.Fatalf("buyer GET /payments/pay-multi-1 want 200, got %d (%s)", recBuyerGet.Code, recBuyerGet.Body.String())
		}
		var payWrap struct {
			Payment paymentDTO `json:"payment"`
		}
		if err := json.Unmarshal(recBuyerGet.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode buyer payment: %v", err)
		}
		if len(payWrap.Payment.ShopAllocations) != 2 {
			t.Fatalf("buyer expected 2 allocations, got %d", len(payWrap.Payment.ShopAllocations))
		}
		for _, al := range payWrap.Payment.ShopAllocations {
			if al.CommissionVND != 0 {
				t.Fatalf("buyer GET /payments/{id} leaked commission_vnd=%d on shop %s", al.CommissionVND, al.ShopID)
			}
		}

		// Buyer POST /payments/{id}/retry: commission_vnd MUST also be 0 on all.
		recBuyerRetry := do(http.MethodPost, "/api/v1/payments/pay-multi-1/retry", buyerTok, `{"method":"VIETQR"}`, map[string]string{"Idempotency-Key": "idem-privacy-retry-1"})
		if recBuyerRetry.Code != http.StatusOK {
			t.Fatalf("buyer POST /payments/pay-multi-1/retry want 200, got %d (%s)", recBuyerRetry.Code, recBuyerRetry.Body.String())
		}
		payWrap.Payment = paymentDTO{}
		if err := json.Unmarshal(recBuyerRetry.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode buyer retry payment: %v", err)
		}
		for _, al := range payWrap.Payment.ShopAllocations {
			if al.CommissionVND != 0 {
				t.Fatalf("buyer POST /payments/{id}/retry leaked commission_vnd=%d on shop %s", al.CommissionVND, al.ShopID)
			}
		}

		// Seller A (participating shop-a) GET /payments/{id}: sees ONLY shop-a allocation (with own commission_vnd=12000), NEVER shop-b.
		recSellerGet := do(http.MethodGet, "/api/v1/payments/pay-multi-1", sellerATok, "", nil)
		if recSellerGet.Code != http.StatusOK {
			t.Fatalf("seller-a GET /payments/pay-multi-1 want 200, got %d (%s)", recSellerGet.Code, recSellerGet.Body.String())
		}
		payWrap.Payment = paymentDTO{}
		if err := json.Unmarshal(recSellerGet.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode seller payment: %v", err)
		}
		if len(payWrap.Payment.ShopAllocations) != 1 || payWrap.Payment.ShopAllocations[0].ShopID != "shop-a" || payWrap.Payment.ShopAllocations[0].CommissionVND != 12000 {
			t.Fatalf("seller-a must see only own allocation with commission=12000, got %+v", payWrap.Payment.ShopAllocations)
		}

		// Seller C (unrelated shop-c) & Buyer 2 get 403 on GET /payments/{id} and SSE /payments/{id}/events.
		for _, tok := range []string{sellerCTok, otherBuyerTok} {
			if rec := do(http.MethodGet, "/api/v1/payments/pay-multi-1", tok, "", nil); rec.Code != http.StatusForbidden {
				t.Fatalf("unrelated caller GET /payments/pay-multi-1: want 403, got %d", rec.Code)
			}
			if rec := do(http.MethodGet, "/api/v1/payments/pay-multi-1/events", tok, "", nil); rec.Code != http.StatusForbidden {
				t.Fatalf("unrelated caller SSE /payments/pay-multi-1/events: want 403, got %d", rec.Code)
			}
		}
		// Buyer owner, participating Seller A, and Admin are allowed on SSE without leaking commission_vnd.
		for _, tok := range []string{buyerTok, sellerATok, adminTok} {
			recSSE := do(http.MethodGet, "/api/v1/payments/pay-multi-1/events", tok, "", nil)
			if recSSE.Code != http.StatusOK || strings.Contains(recSSE.Body.String(), "commission_vnd") {
				t.Fatalf("SSE /payments/pay-multi-1/events: code=%d body=%s", recSSE.Code, recSSE.Body.String())
			}
		}

		// Admin GET /payments/{id}: sees both allocations with full commission_vnd.
		recAdminGet := do(http.MethodGet, "/api/v1/payments/pay-multi-1", adminTok, "", nil)
		if recAdminGet.Code != http.StatusOK {
			t.Fatalf("admin GET /payments/pay-multi-1 want 200, got %d", recAdminGet.Code)
		}
		payWrap.Payment = paymentDTO{}
		if err := json.Unmarshal(recAdminGet.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode admin payment: %v", err)
		}
		if len(payWrap.Payment.ShopAllocations) != 2 || payWrap.Payment.ShopAllocations[0].CommissionVND != 12000 || payWrap.Payment.ShopAllocations[1].CommissionVND != 9000 {
			t.Fatalf("admin must see full allocations+commission, got %+v", payWrap.Payment.ShopAllocations)
		}
	})

	// 7. [P3] handlePreviewDiscounts: <= 200 items cap, int64 overflow check, and no dead NotFound swallow on shipping settings.
	t.Run("PreviewCapOverflowAndShippingErrorPropagation", func(t *testing.T) {
		// > 200 line items -> 400.
		items201 := make([]string, 201)
		for i := range items201 {
			items201[i] = fmt.Sprintf(`{"sku_id":"sku-%d","quantity":1}`, i+1)
		}
		recCap := do(http.MethodPost, "/api/v1/promotions/preview", buyerTok,
			`{"shop_items":[{"shop_id":"shop-a","shipping_fee_vnd":0,"items":[`+strings.Join(items201, ",")+`]}]}`, nil)
		if recCap.Code != http.StatusBadRequest {
			t.Fatalf("preview with 201 items: want 400, got %d (%s)", recCap.Code, recCap.Body.String())
		}

		// int64 overflow when merging shipping_fee_vnd across duplicate shop entries -> 400 (never silently wrap!).
		recOvf := do(http.MethodPost, "/api/v1/promotions/preview", buyerTok,
			`{"shop_items":[{"shop_id":"shop-a","shipping_fee_vnd":9223372036854775800,"items":[{"sku_id":"sku-a1","quantity":1}]},{"shop_id":"shop-a","shipping_fee_vnd":100,"items":[{"sku_id":"sku-a1","quantity":1}]}]}`, nil)
		if recOvf.Code != http.StatusBadRequest {
			t.Fatalf("preview int64 shipping_fee_vnd overflow: want 400, got %d (%s)", recOvf.Code, recOvf.Body.String())
		}

		// Dead NotFound branch removed: if Shipping.GetShopShippingSettings returns NotFound, propagate 404 (do not swallow).
		shippingMock.mu.Lock()
		shippingMock.getErr = status.Error(codes.NotFound, "unexpected not found")
		shippingMock.mu.Unlock()
		recNotFound := do(http.MethodPost, "/api/v1/promotions/preview", buyerTok,
			`{"shop_items":[{"shop_id":"shop-a","shipping_fee_vnd":20000,"items":[{"sku_id":"sku-a1","quantity":1}]}]}`, nil)
		shippingMock.mu.Lock()
		shippingMock.getErr = nil
		shippingMock.mu.Unlock()
		if recNotFound.Code != http.StatusNotFound {
			t.Fatalf("preview shipping NotFound must propagate 404 (dead branch removed), got %d (%s)", recNotFound.Code, recNotFound.Body.String())
		}
	})
}

func TestW2BRound5ReviewFixes(t *testing.T) {
	h, authMgr, _, _, promoMock, _, paymentMock, shippingMock := newW2BTestHarness(t)
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")
	sellerATok, _, _, _ := authMgr.SignAccessToken("seller-a-user", "seller", "shop-a")
	adminTok, _, _, _ := authMgr.SignAccessToken("admin-1", "admin", "")

	do := func(method, target, tok, body string) *httptest.ResponseRecorder {
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, target, rdr)
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

	// 1. [P2] Required *bool fields on flash-sale approve and admin carrier update.
	t.Run("RequiredBoolFieldsFlashApproveAndAdminCarrier", func(t *testing.T) {
		// POST /api/v1/admin/flash-sale-items/{item_id}/approve: missing approve -> 400 (never silently reject).
		promoMock.lastApproveFlash = nil
		for _, badBody := range []string{`{}`, `{"reason":"missing approve"}`, `{"approve":null}`} {
			rec := do(http.MethodPost, "/api/v1/admin/flash-sale-items/fsi-1/approve", adminTok, badBody)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST /admin/flash-sale-items/fsi-1/approve %s: want 400, got %d (%s)", badBody, rec.Code, rec.Body.String())
			}
		}
		if promoMock.lastApproveFlash != nil {
			t.Fatalf("ApproveFlashSaleItem must not be called when approve is missing, got %+v", promoMock.lastApproveFlash)
		}
		// Reject with explicit approve:false and omitted reason -> 200 (reason not required by docs/contracts.md).
		recReject := do(http.MethodPost, "/api/v1/admin/flash-sale-items/fsi-1/approve", adminTok, `{"approve":false}`)
		if recReject.Code != http.StatusOK {
			t.Fatalf("POST /admin/flash-sale-items/fsi-1/approve approve=false: want 200, got %d (%s)", recReject.Code, recReject.Body.String())
		}
		if promoMock.lastApproveFlash == nil || promoMock.lastApproveFlash.GetApprove() {
			t.Fatalf("expected Approve=false forwarded, got %+v", promoMock.lastApproveFlash)
		}
		// Approve with explicit approve:true -> 200.
		recApprove := do(http.MethodPost, "/api/v1/admin/flash-sale-items/fsi-1/approve", adminTok, `{"approve":true,"reason":"ok"}`)
		if recApprove.Code != http.StatusOK {
			t.Fatalf("POST /admin/flash-sale-items/fsi-1/approve approve=true: want 200, got %d (%s)", recApprove.Code, recApprove.Body.String())
		}
		if promoMock.lastApproveFlash == nil || !promoMock.lastApproveFlash.GetApprove() || promoMock.lastApproveFlash.GetReason() != "ok" {
			t.Fatalf("expected Approve=true reason=ok forwarded, got %+v", promoMock.lastApproveFlash)
		}

		// PUT /api/v1/admin/carriers/{code}: missing enabled -> 400 (never silently disable carrier platform-wide).
		shippingMock.lastUpdateCarrier = nil
		for _, badBody := range []string{`{}`, `{"api_token":"tok-1"}`, `{"enabled":null}`} {
			rec := do(http.MethodPut, "/api/v1/admin/carriers/ghn", adminTok, badBody)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT /admin/carriers/ghn %s: want 400, got %d (%s)", badBody, rec.Code, rec.Body.String())
			}
		}
		if shippingMock.lastUpdateCarrier != nil {
			t.Fatalf("AdminUpdateCarrier must not be called when enabled is missing, got %+v", shippingMock.lastUpdateCarrier)
		}
		// Explicit enabled:false -> 200.
		recDis := do(http.MethodPut, "/api/v1/admin/carriers/ghn", adminTok, `{"enabled":false}`)
		if recDis.Code != http.StatusOK {
			t.Fatalf("PUT /admin/carriers/ghn enabled=false: want 200, got %d (%s)", recDis.Code, recDis.Body.String())
		}
		if shippingMock.lastUpdateCarrier == nil || shippingMock.lastUpdateCarrier.GetEnabled() {
			t.Fatalf("expected Enabled=false forwarded, got %+v", shippingMock.lastUpdateCarrier)
		}
		// Explicit enabled:true -> 200.
		recEn := do(http.MethodPut, "/api/v1/admin/carriers/ghn", adminTok, `{"enabled":true,"api_token":"tok-2"}`)
		if recEn.Code != http.StatusOK {
			t.Fatalf("PUT /admin/carriers/ghn enabled=true: want 200, got %d (%s)", recEn.Code, recEn.Body.String())
		}
		if shippingMock.lastUpdateCarrier == nil || !shippingMock.lastUpdateCarrier.GetEnabled() || shippingMock.lastUpdateCarrier.GetApiToken() != "tok-2" {
			t.Fatalf("expected Enabled=true api_token=tok-2 forwarded, got %+v", shippingMock.lastUpdateCarrier)
		}
	})

	// 2. [P2] handlePreviewDiscounts does not call ListActiveShopDiscounts and leaves ShopDiscountProgramItemId empty.
	t.Run("PreviewDoesNotCallListActiveShopDiscountsOrSetProgramItemID", func(t *testing.T) {
		promoMock.mu.Lock()
		promoMock.lastActiveDiscounts = nil
		promoMock.lastPreview = nil
		promoMock.mu.Unlock()

		rec := do(http.MethodPost, "/api/v1/promotions/preview", buyerTok, `{
			"shop_items": [{
				"shop_id": "shop-a",
				"shipping_fee_vnd": 25000,
				"items": [{"sku_id": "sku-a1", "quantity": 1, "shop_discount_program_item_id": "sdpi-client-spoof"}]
			}]
		}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST /promotions/preview want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		if promoMock.lastActiveDiscounts != nil {
			t.Fatalf("gateway preview must NOT invoke ListActiveShopDiscounts, got %+v", promoMock.lastActiveDiscounts)
		}
		if promoMock.lastPreview == nil || len(promoMock.lastPreview.GetShopItems()) != 1 || len(promoMock.lastPreview.GetShopItems()[0].GetItems()) != 1 {
			t.Fatalf("unexpected PreviewDiscountsRequest: %+v", promoMock.lastPreview)
		}
		li := promoMock.lastPreview.GetShopItems()[0].GetItems()[0]
		if li.GetShopDiscountProgramItemId() != "" {
			t.Fatalf("PreviewDiscountsRequest line item must leave ShopDiscountProgramItemId empty, got %q", li.GetShopDiscountProgramItemId())
		}
		if li.GetUnitPriceVnd() != 0 || li.GetCategoryId() != "" || !promoMock.lastPreview.GetShopItems()[0].GetFreeShipEnabled() {
			t.Fatalf("expected D4 omitted price/category and authoritative freeship preserved, got req=%+v", promoMock.lastPreview.GetShopItems()[0])
		}
	})

	// 3. [P3] canAccessPayment fail-closed on nil/empty user_id (except admin) & seller clears qr_url/qr_code/provider_ref.
	t.Run("PaymentAccessFailClosedAndSellerClearsQRAndProviderRef", func(t *testing.T) {
		buyerClaims := &auth.Claims{Role: "buyer"}
		buyerClaims.Subject = "buyer-1"
		sellerAClaims := &auth.Claims{Role: "seller", ShopID: "shop-a"}
		sellerAClaims.Subject = "seller-a-user"
		adminClaims := &auth.Claims{Role: "admin"}
		adminClaims.Subject = "admin-1"
		superAdminClaims := &auth.Claims{Role: "super_admin"}
		superAdminClaims.Subject = "super-1"

		emptyUserPay := &paymentv1.Payment{
			PaymentId: "p-empty",
			UserId:    "",
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: "ord-a", ShopId: "shop-a", SubtotalVnd: 100000},
			},
		}
		whitespaceUserPay := &paymentv1.Payment{
			PaymentId: "p-ws",
			UserId:    "   ",
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: "ord-a", ShopId: "shop-a", SubtotalVnd: 100000},
			},
		}
		for _, tc := range []struct {
			name   string
			claims *auth.Claims
			pay    *paymentv1.Payment
			want   bool
		}{
			{"nil claims + nil payment", nil, nil, false},
			{"buyer + nil payment", buyerClaims, nil, false},
			{"seller + nil payment", sellerAClaims, nil, false},
			{"admin + nil payment", adminClaims, nil, true},
			{"super_admin + nil payment", superAdminClaims, nil, true},
			{"buyer + empty user_id", buyerClaims, emptyUserPay, false},
			{"seller + empty user_id even with matching shop", sellerAClaims, emptyUserPay, false},
			{"seller + whitespace user_id", sellerAClaims, whitespaceUserPay, false},
			{"admin + empty user_id", adminClaims, emptyUserPay, true},
			{"super_admin + empty user_id", superAdminClaims, emptyUserPay, true},
		} {
			if got := canAccessPayment(tc.claims, tc.pay); got != tc.want {
				t.Fatalf("canAccessPayment(%s): want %v, got %v", tc.name, tc.want, got)
			}
		}

		// HTTP checks for empty user_id and nil payment: 403 for buyer & seller-a, 200 for admin.
		for _, payID := range []string{"pay-empty-user", "pay-nil"} {
			if rec := do(http.MethodGet, "/api/v1/payments/"+payID, buyerTok, ""); rec.Code != http.StatusForbidden {
				t.Fatalf("buyer GET /payments/%s: want 403, got %d (%s)", payID, rec.Code, rec.Body.String())
			}
			if rec := do(http.MethodGet, "/api/v1/payments/"+payID, sellerATok, ""); rec.Code != http.StatusForbidden {
				t.Fatalf("seller-a GET /payments/%s: want 403, got %d (%s)", payID, rec.Code, rec.Body.String())
			}
			if rec := do(http.MethodGet, "/api/v1/payments/"+payID, adminTok, ""); rec.Code != http.StatusOK {
				t.Fatalf("admin GET /payments/%s: want 200, got %d (%s)", payID, rec.Code, rec.Body.String())
			}
			if rec := do(http.MethodGet, "/api/v1/payments/"+payID+"/events", buyerTok, ""); rec.Code != http.StatusForbidden {
				t.Fatalf("buyer SSE /payments/%s/events: want 403, got %d", payID, rec.Code)
			}
			if rec := do(http.MethodGet, "/api/v1/payments/"+payID+"/events", sellerATok, ""); rec.Code != http.StatusForbidden {
				t.Fatalf("seller-a SSE /payments/%s/events: want 403, got %d", payID, rec.Code)
			}
		}

		// Non-buyer seller GET /payments/pay-multi-1: qr_url, qr_code, provider_ref MUST be cleared defensively.
		recSeller := do(http.MethodGet, "/api/v1/payments/pay-multi-1", sellerATok, "")
		if recSeller.Code != http.StatusOK {
			t.Fatalf("seller-a GET /payments/pay-multi-1: want 200, got %d (%s)", recSeller.Code, recSeller.Body.String())
		}
		var payWrap struct {
			Payment paymentDTO `json:"payment"`
		}
		if err := json.Unmarshal(recSeller.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode seller payment: %v", err)
		}
		if payWrap.Payment.QRURL != "" || payWrap.Payment.QRCode != "" || payWrap.Payment.ProviderRef != "" {
			t.Fatalf("non-buyer seller must have qr_url/qr_code/provider_ref cleared, got qr_url=%q qr_code=%q provider_ref=%q",
				payWrap.Payment.QRURL, payWrap.Payment.QRCode, payWrap.Payment.ProviderRef)
		}
		if len(payWrap.Payment.ShopAllocations) != 1 || payWrap.Payment.ShopAllocations[0].ShopID != "shop-a" {
			t.Fatalf("seller-a must still see own shop-a allocation, got %+v", payWrap.Payment.ShopAllocations)
		}

		// Buyer owner GET /payments/pay-multi-1: preserves qr_url/qr_code/provider_ref and zeros commission_vnd.
		recBuyer := do(http.MethodGet, "/api/v1/payments/pay-multi-1", buyerTok, "")
		if recBuyer.Code != http.StatusOK {
			t.Fatalf("buyer GET /payments/pay-multi-1: want 200, got %d", recBuyer.Code)
		}
		payWrap.Payment = paymentDTO{}
		if err := json.Unmarshal(recBuyer.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode buyer payment: %v", err)
		}
		if payWrap.Payment.QRURL != "https://qr.example/pay-multi-1" || payWrap.Payment.QRCode != "VIETQR-RAW-CODE-1" || payWrap.Payment.ProviderRef != "PROV-REF-1" {
			t.Fatalf("buyer owner must keep qr_url/qr_code/provider_ref, got %+v", payWrap.Payment)
		}

		// Seller who IS the buyer of their own order keeps qr_url/qr_code/provider_ref (buyer branch takes precedence).
		paymentMock.mu.Lock()
		paymentMock.payments["pay-seller-as-buyer"] = &paymentv1.Payment{
			PaymentId:   "pay-seller-as-buyer",
			UserId:      "seller-a-user",
			AmountVnd:   120000,
			Method:      paymentv1.PaymentMethod_PAYMENT_METHOD_VIETQR,
			Status:      paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING,
			QrUrl:       "https://qr.example/own-order",
			QrCode:      "VIETQR-OWN",
			ProviderRef: "PROV-OWN",
			ShopAllocations: []*paymentv1.ShopAllocation{
				{OrderId: "ord-b2", ShopId: "shop-b", SubtotalVnd: 120000, CommissionVnd: 6000},
			},
		}
		paymentMock.mu.Unlock()
		recSellerBuyer := do(http.MethodGet, "/api/v1/payments/pay-seller-as-buyer", sellerATok, "")
		if recSellerBuyer.Code != http.StatusOK {
			t.Fatalf("seller-as-buyer GET /payments/pay-seller-as-buyer: want 200, got %d", recSellerBuyer.Code)
		}
		payWrap.Payment = paymentDTO{}
		if err := json.Unmarshal(recSellerBuyer.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode seller-as-buyer payment: %v", err)
		}
		if payWrap.Payment.QRURL != "https://qr.example/own-order" || payWrap.Payment.QRCode != "VIETQR-OWN" || payWrap.Payment.ProviderRef != "PROV-OWN" {
			t.Fatalf("seller who is buyer must keep qr_url/qr_code/provider_ref, got %+v", payWrap.Payment)
		}
		if len(payWrap.Payment.ShopAllocations) != 1 || payWrap.Payment.ShopAllocations[0].CommissionVND != 0 {
			t.Fatalf("seller who is buyer must have commission_vnd zeroed, got %+v", payWrap.Payment.ShopAllocations)
		}

		// Admin GET /payments/pay-multi-1: preserves qr_url/qr_code/provider_ref and full commission_vnd.
		recAdmin := do(http.MethodGet, "/api/v1/payments/pay-multi-1", adminTok, "")
		if recAdmin.Code != http.StatusOK {
			t.Fatalf("admin GET /payments/pay-multi-1: want 200, got %d", recAdmin.Code)
		}
		payWrap.Payment = paymentDTO{}
		if err := json.Unmarshal(recAdmin.Body.Bytes(), &payWrap); err != nil {
			t.Fatalf("decode admin payment: %v", err)
		}
		if payWrap.Payment.QRURL != "https://qr.example/pay-multi-1" || payWrap.Payment.QRCode != "VIETQR-RAW-CODE-1" || payWrap.Payment.ProviderRef != "PROV-REF-1" {
			t.Fatalf("admin must keep qr_url/qr_code/provider_ref, got %+v", payWrap.Payment)
		}
	})
}

func TestW2BCalculateShippingFeeAuthoritativeItems(t *testing.T) {
	h, authMgr, _, prodMock, _, _, _, shippingMock := newW2BTestHarness(t)
	buyerTok, _, _, _ := authMgr.SignAccessToken("buyer-1", "buyer", "")

	prodMock.mu.Lock()
	prodMock.skus["sku-zero"] = &productv1.SKU{Id: "sku-zero", ShopId: "shop-w", PriceVnd: 120000, WeightGrams: 0, IsActive: true}
	prodMock.skus["sku-light"] = &productv1.SKU{Id: "sku-light", ShopId: "shop-w", PriceVnd: 80000, WeightGrams: 50, IsActive: true}
	prodMock.skus["sku-a2"] = &productv1.SKU{Id: "sku-a2", ShopId: "shop-a", PriceVnd: 75000, WeightGrams: 200, IsActive: true}
	prodMock.skus["sku-zero-def"] = &productv1.SKU{Id: "sku-zero-def", ShopId: "shop-zero-def", PriceVnd: 50000, WeightGrams: 0, IsActive: true}
	prodMock.skus["sku-huge-w"] = &productv1.SKU{Id: "sku-huge-w", ShopId: "shop-w", PriceVnd: 10000, WeightGrams: 1_000_000_000, IsActive: true}
	prodMock.mu.Unlock()

	shippingMock.mu.Lock()
	shippingMock.settingsByShop["shop-w"] = &shippingv1.ShopShippingSettings{
		ShopId:             "shop-w",
		DefaultWeightGrams: 1000,
	}
	shippingMock.settingsByShop["shop-zero-def"] = &shippingv1.ShopShippingSettings{
		ShopId:             "shop-zero-def",
		DefaultWeightGrams: 0,
	}
	shippingMock.mu.Unlock()

	postFee := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/shipping/calculate-fee", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+buyerTok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	t.Run("SKU0gUsesShopDefault1000gTimesQty3AndIgnoresClientWeight", func(t *testing.T) {
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.lastGetSettingsMD = nil
		shippingMock.getErr = nil
		shippingMock.mu.Unlock()

		rec := postFee(`{
			"shop_id": "shop-w",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"weight_grams": 100,
			"cod_amount_vnd": 999999,
			"items": [{"sku_id": "sku-zero", "quantity": 3}]
		}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		got := shippingMock.lastCalculateFeeReq
		if got == nil || got.GetWeightGrams() != 3000 || got.GetCodAmountVnd() != 0 {
			t.Fatalf("want weight=3000 cod=0, got %+v", got)
		}
		authVals := shippingMock.lastGetSettingsMD.Get(auth.MDAuthorization)
		if len(authVals) == 0 || authVals[0] != "Bearer "+buyerTok {
			t.Fatalf("GetShopShippingSettings must forward buyer Bearer JWT, got %v", authVals)
		}
	})

	t.Run("SKU50gIsNotFlooredTo100g", func(t *testing.T) {
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.getErr = nil
		shippingMock.mu.Unlock()

		rec := postFee(`{
			"shop_id": "shop-w",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"weight_grams": 100,
			"items": [{"sku_id": "sku-light", "quantity": 1}]
		}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		if got := shippingMock.lastCalculateFeeReq; got == nil || got.GetWeightGrams() != 50 {
			t.Fatalf("SKU 50g must not be floored to 100g: want weight=50, got %+v", got)
		}
	})

	t.Run("SKUFromOtherShopOrUnknownReturns400WithoutLeaking", func(t *testing.T) {
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.getErr = nil
		shippingMock.mu.Unlock()

		recOther := postFee(`{
			"shop_id": "shop-a",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"items": [{"sku_id": "sku-b1", "quantity": 1}]
		}`)
		if recOther.Code != http.StatusBadRequest {
			t.Fatalf("other shop SKU: want 400, got %d (%s)", recOther.Code, recOther.Body.String())
		}
		if strings.Contains(recOther.Body.String(), "shop-b") || strings.Contains(recOther.Body.String(), "sku-b1") {
			t.Fatalf("error must not leak other shop or SKU identity: %s", recOther.Body.String())
		}
		if shippingMock.lastCalculateFeeReq != nil {
			t.Fatalf("CalculateFee must not be called for cross-shop SKU")
		}

		recMissing := postFee(`{
			"shop_id": "shop-a",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"items": [{"sku_id": "sku-does-not-exist", "quantity": 1}]
		}`)
		if recMissing.Code != http.StatusBadRequest {
			t.Fatalf("missing SKU: want 400, got %d (%s)", recMissing.Code, recMissing.Body.String())
		}
		var errOther, errMissing struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(recOther.Body.Bytes(), &errOther)
		_ = json.Unmarshal(recMissing.Body.Bytes(), &errMissing)
		if errOther.Error.Code != errMissing.Error.Code || errOther.Error.Message != errMissing.Error.Message {
			t.Fatalf("cross-shop vs missing SKU errors should be indistinguishable: other=%+v missing=%+v", errOther, errMissing)
		}
	})

	t.Run("CODUsesMergedPreDiscountSubtotalAndClientTamperingIgnored", func(t *testing.T) {
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.getErr = nil
		shippingMock.mu.Unlock()

		// shop-a has default_weight_grams=400; sku-a1 has weight=0, price=250000 (merged qty 2+1=3 -> 1200g, 750000 VND);
		// sku-a2 has weight=200, price=75000 (qty 2 -> 400g, 150000 VND). Total weight=1600g, COD=900000 VND.
		recCOD := postFee(`{
			"shop_id": "shop-a",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"weight_grams": 100,
			"cod_amount_vnd": 1,
			"payment_method": "COD",
			"items": [
				{"sku_id": "sku-a1", "quantity": 2},
				{"sku_id": "sku-a2", "quantity": 2},
				{"sku_id": "sku-a1", "quantity": 1}
			]
		}`)
		if recCOD.Code != http.StatusOK {
			t.Fatalf("COD calculate-fee: want 200, got %d (%s)", recCOD.Code, recCOD.Body.String())
		}
		got := shippingMock.lastCalculateFeeReq
		if got == nil || got.GetWeightGrams() != 1600 || got.GetCodAmountVnd() != 900000 {
			t.Fatalf("want merged weight=1600 cod=900000, got %+v", got)
		}

		// VIETQR must force cod_amount_vnd=0 even if client sends cod_amount_vnd.
		recQR := postFee(`{
			"shop_id": "shop-a",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"weight_grams": 100,
			"cod_amount_vnd": 900000,
			"payment_method": "VIETQR",
			"items": [{"sku_id": "sku-a1", "quantity": 2}]
		}`)
		if recQR.Code != http.StatusOK {
			t.Fatalf("VIETQR calculate-fee: want 200, got %d (%s)", recQR.Code, recQR.Body.String())
		}
		if gotQR := shippingMock.lastCalculateFeeReq; gotQR == nil || gotQR.GetWeightGrams() != 800 || gotQR.GetCodAmountVnd() != 0 {
			t.Fatalf("VIETQR must zero cod_amount_vnd: want weight=800 cod=0, got %+v", gotQR)
		}
	})

	t.Run("SettingsNotFoundOrNonPositiveDefaultsTo500g", func(t *testing.T) {
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.getErr = status.Error(codes.NotFound, "no shop settings")
		shippingMock.mu.Unlock()
		defer func() {
			shippingMock.mu.Lock()
			shippingMock.getErr = nil
			shippingMock.mu.Unlock()
		}()

		recNF := postFee(`{
			"shop_id": "shop-w",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"items": [{"sku_id": "sku-zero", "quantity": 2}]
		}`)
		if recNF.Code != http.StatusOK {
			t.Fatalf("settings NotFound: want 200, got %d (%s)", recNF.Code, recNF.Body.String())
		}
		if got := shippingMock.lastCalculateFeeReq; got == nil || got.GetWeightGrams() != 1000 {
			t.Fatalf("settings NotFound must default to 500g * 2 = 1000g, got %+v", got)
		}

		shippingMock.mu.Lock()
		shippingMock.getErr = nil
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.mu.Unlock()

		recZeroDef := postFee(`{
			"shop_id": "shop-zero-def",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"items": [{"sku_id": "sku-zero-def", "quantity": 2}]
		}`)
		if recZeroDef.Code != http.StatusOK {
			t.Fatalf("settings default<=0: want 200, got %d (%s)", recZeroDef.Code, recZeroDef.Body.String())
		}
		if got := shippingMock.lastCalculateFeeReq; got == nil || got.GetWeightGrams() != 1000 {
			t.Fatalf("settings default<=0 must fallback to 500g * 2 = 1000g, got %+v", got)
		}
	})

	t.Run("SettingsUnavailablePropagatesError", func(t *testing.T) {
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.getErr = status.Error(codes.Unavailable, "shipping service unavailable")
		shippingMock.mu.Unlock()
		defer func() {
			shippingMock.mu.Lock()
			shippingMock.getErr = nil
			shippingMock.mu.Unlock()
		}()

		rec := postFee(`{
			"shop_id": "shop-w",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"items": [{"sku_id": "sku-zero", "quantity": 1}]
		}`)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("settings Unavailable: want 503, got %d (%s)", rec.Code, rec.Body.String())
		}
		if shippingMock.lastCalculateFeeReq != nil {
			t.Fatalf("CalculateFee must not be called when GetShopShippingSettings returns Unavailable")
		}
	})

	t.Run("ValidationBoundsAndInt32WeightOverflow", func(t *testing.T) {
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.getErr = nil
		shippingMock.mu.Unlock()

		lines101 := make([]string, 101)
		for i := range lines101 {
			lines101[i] = `{"sku_id":"sku-light","quantity":1}`
		}
		badPayloads := []string{
			`{"shop_id":"","to_province_code":"79","items":[{"sku_id":"sku-light","quantity":1}]}`,
			`{"shop_id":"shop-w","to_province_code":"79","items":[{"sku_id":"","quantity":1}]}`,
			`{"shop_id":"shop-w","to_province_code":"79","items":[{"sku_id":"sku-light","quantity":0}]}`,
			`{"shop_id":"shop-w","to_province_code":"79","items":[{"sku_id":"sku-light","quantity":-1}]}`,
			`{"shop_id":"shop-w","to_province_code":"79","items":[{"sku_id":"sku-light","quantity":1000}]}`,
			`{"shop_id":"shop-w","to_province_code":"79","payment_method":"CRYPTO","items":[{"sku_id":"sku-light","quantity":1}]}`,
			`{"shop_id":"shop-w","to_province_code":"79","items":[` + strings.Join(lines101, ",") + `]}`,
			`{"shop_id":"shop-w","to_province_code":"79","items":[{"sku_id":"sku-huge-w","quantity":3}]}`,
		}
		for _, body := range badPayloads {
			rec := postFee(body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("payload %s: want 400, got %d (%s)", body[:min(len(body), 90)], rec.Code, rec.Body.String())
			}
		}
		if shippingMock.lastCalculateFeeReq != nil {
			t.Fatalf("CalculateFee must not be called on invalid items or int32 overflow")
		}
	})

	t.Run("LegacyWithoutItemsPreserved", func(t *testing.T) {
		prodMock.mu.Lock()
		prodMock.lastGetSKUsBatch = nil
		prodMock.mu.Unlock()
		shippingMock.mu.Lock()
		shippingMock.lastCalculateFeeReq = nil
		shippingMock.getErr = nil
		shippingMock.mu.Unlock()

		rec := postFee(`{
			"shop_id": "shop-w",
			"to_province_code": "79",
			"to_ward_code": "79001",
			"weight_grams": 750,
			"cod_amount_vnd": 123000
		}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("legacy calculate-fee: want 200, got %d (%s)", rec.Code, rec.Body.String())
		}
		if prodMock.lastGetSKUsBatch != nil {
			t.Fatalf("legacy calculate-fee without items must not call GetSKUsBatch")
		}
		if got := shippingMock.lastCalculateFeeReq; got == nil || got.GetWeightGrams() != 750 || got.GetCodAmountVnd() != 123000 {
			t.Fatalf("legacy calculate-fee must pass through weight=750 cod=123000, got %+v", got)
		}
	})
}
