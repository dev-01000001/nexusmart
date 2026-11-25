package service

import (
	"context"
	"math/big"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
)

// validateFlashSaleSKU validates SKU ownership, active status, 30-day minimum price,
// campaign minimum discount percent, campaign category scope, and available stock.
// Returns the SKU's current price_vnd (0 if productClient is nil in legacy unit tests).
//
// ponytail: flash approval does not pre-lock product stock; checkout saga reserves stock atomically.
func (s *PromotionService) validateFlashSaleSKU(
	ctx context.Context,
	fs *domain.FlashSale,
	shopID string,
	skuID string,
	flashPriceVND int64,
	totalSlots int32,
	isApproval bool,
) (int64, error) {
	if s.productClient == nil {
		return 0, nil
	}

	skus, err := s.productClient.GetSKUsBatch(ctx, []string{skuID})
	if err != nil {
		return 0, status.Errorf(codes.Unavailable, "fetch SKU %s from product service: %v", skuID, err)
	}
	sku := skus[skuID]
	if sku == nil {
		return 0, status.Errorf(codes.NotFound, "sku %s not found", skuID)
	}

	if sku.GetShopId() != shopID {
		if isApproval {
			return 0, status.Errorf(codes.FailedPrecondition, "sku %s does not belong to registering shop %s", skuID, shopID)
		}
		return 0, status.Error(codes.PermissionDenied, "cannot register another shop's SKU for flash sale")
	}
	if !sku.GetIsActive() {
		return 0, status.Errorf(codes.FailedPrecondition, "sku %s is not active", skuID)
	}

	origPrice := sku.GetPriceVnd()
	if origPrice <= 0 {
		return 0, status.Errorf(codes.FailedPrecondition, "sku %s has invalid price", skuID)
	}
	minPrice30d := effectiveMinPrice30d(sku)
	if flashPriceVND >= minPrice30d {
		return 0, status.Errorf(
			codes.FailedPrecondition,
			"flash_price_vnd (%d) must be strictly less than SKU 30-day minimum price (%d)",
			flashPriceVND, minPrice30d,
		)
	}

	if fs != nil && fs.MinDiscountPercent > 0 {
		// Exact integer check: (minPrice30d - flashPriceVND) * 100 >= minPrice30d * minDiscountPercent
		lhs := new(big.Int).Mul(big.NewInt(minPrice30d-flashPriceVND), big.NewInt(100))
		rhs := new(big.Int).Mul(big.NewInt(minPrice30d), big.NewInt(int64(fs.MinDiscountPercent)))
		if lhs.Cmp(rhs) < 0 {
			return 0, status.Errorf(
				codes.FailedPrecondition,
				"flash_price_vnd (%d) does not meet campaign minimum discount of %d%% off 30-day minimum price (%d)",
				flashPriceVND, fs.MinDiscountPercent, minPrice30d,
			)
		}
	}

	if fs != nil && len(fs.CategoryIDs) > 0 {
		allowed, catErr := s.resolveAllowedCategories(ctx, fs.CategoryIDs)
		if catErr != nil {
			return 0, catErr
		}
		if !allowed[sku.GetCategoryId()] {
			return 0, status.Errorf(codes.FailedPrecondition, "sku %s category %s is not eligible for this flash sale campaign", skuID, sku.GetCategoryId())
		}
	}

	availStock := sku.GetAvailableStock()
	if totalSlots > availStock {
		return 0, status.Errorf(codes.FailedPrecondition, "total_slots (%d) exceeds SKU available stock (%d)", totalSlots, availStock)
	}

	return origPrice, nil
}

func effectiveMinPrice30d(sku *productv1.SKU) int64 {
	if sku == nil {
		return 0
	}
	orig := sku.GetPriceVnd()
	min30 := sku.GetMinPrice_30DVnd()
	if min30 <= 0 || min30 > orig {
		return orig
	}
	return min30
}

// ListFlashSaleItems lists flash sale item registrations filtered by status/campaign/shop.
// Sellers are restricted to their own shop_id from verified claims; admins may view all shops.
func (s *PromotionService) ListFlashSaleItems(ctx context.Context, req *promotionv1.ListFlashSaleItemsRequest) (*promotionv1.ListFlashSaleItemsResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}

	shopID := strings.TrimSpace(req.GetShopId())
	switch {
	case auth.HasCapability(claims, auth.CapFlashSalesRead):
		// Authorized staff with flash_sales.read may filter by any shop_id or leave empty for all shops.
	case isSeller(claims):
		if claims.ShopID == "" {
			return nil, status.Error(codes.PermissionDenied, "seller account has no shop_id")
		}
		if shopID != "" && shopID != claims.ShopID {
			return nil, status.Error(codes.PermissionDenied, "sellers can only list their own shop's flash sale items")
		}
		shopID = claims.ShopID
	default:
		return nil, status.Error(codes.PermissionDenied, "only sellers or admins can list flash sale items")
	}

	var statusFilter string
	switch req.GetStatus() {
	case promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_UNSPECIFIED:
		statusFilter = ""
	case promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING:
		statusFilter = domain.FlashStatusPending
	case promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED:
		statusFilter = domain.FlashStatusApproved
	case promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_REJECTED:
		statusFilter = domain.FlashStatusRejected
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid flash sale item status: %v", req.GetStatus())
	}

	items, total, err := s.store.ListFlashSaleItems(ctx, statusFilter, strings.TrimSpace(req.GetFlashSaleId()), shopID, req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list flash sale items: %v", err)
	}

	slotKeys := make([]string, 0, len(items))
	for _, it := range items {
		if it.Status == domain.FlashStatusApproved {
			slotKeys = append(slotKeys, lua.FlashRemainingKey(it.ID))
		}
	}
	liveSlots, err := s.engine.MGetInt64(ctx, slotKeys)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read live flash sale slots: %v", err)
	}

	pbItems := make([]*promotionv1.FlashSaleItem, 0, len(items))
	for i := range items {
		it := &items[i]
		rem := min(max(it.RemainingSlots, 0), it.TotalSlots)
		if it.Status == domain.FlashStatusApproved {
			if live, ok := liveSlots[lua.FlashRemainingKey(it.ID)]; ok {
				rem = min(rem, max(int32(live), 0))
			}
		}
		pbItems = append(pbItems, flashSaleItemToProto(it, rem))
	}

	return &promotionv1.ListFlashSaleItemsResponse{
		Items: pbItems,
		Total: total,
	}, nil
}

// ListAllFlashSales lists UPCOMING, ACTIVE, and/or ENDED flash sale campaigns for sellers and admins.
func (s *PromotionService) ListAllFlashSales(ctx context.Context, req *promotionv1.ListAllFlashSalesRequest) (*promotionv1.ListAllFlashSalesResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasCapability(claims, auth.CapFlashSalesRead) && !isSeller(claims) {
		return nil, status.Error(codes.PermissionDenied, "only sellers or authorized admins can list all flash sales")
	}

	statusFilter := strings.ToUpper(strings.TrimSpace(req.GetStatus()))
	switch statusFilter {
	case "", "UPCOMING", "ACTIVE", "ENDED":
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid flash sale window status %q (expected UPCOMING, ACTIVE, or ENDED)", statusFilter)
	}

	now := s.nowFn()
	sales, total, err := s.store.ListAllFlashSales(ctx, now, statusFilter, req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list all flash sales: %v", err)
	}

	var slotKeys []string
	for _, fs := range sales {
		for _, item := range fs.Items {
			slotKeys = append(slotKeys, lua.FlashRemainingKey(item.ID))
		}
	}
	liveSlots, err := s.engine.MGetInt64(ctx, slotKeys)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read live flash sale slots: %v", err)
	}

	pbSales := make([]*promotionv1.FlashSale, 0, len(sales))
	for i := range sales {
		pbSales = append(pbSales, flashSaleToProto(&sales[i], liveSlots, now))
	}
	return &promotionv1.ListAllFlashSalesResponse{
		FlashSales: pbSales,
		Total:      total,
	}, nil
}
