package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

// CreateShopDiscountProgram creates a direct seller SKU discount program (UC-S18).
// Validates SKU ownership, discounted_price < original_price, no overlapping shop discount
// program for the same SKU, warns on approved flash sale overlap (flash wins), and sets
// price_inflation_flagged + warning if SKU price rose within the past 30 days.
func (s *PromotionService) CreateShopDiscountProgram(ctx context.Context, req *promotionv1.CreateShopDiscountProgramRequest) (*promotionv1.CreateShopDiscountProgramResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !isSeller(claims) {
		return nil, status.Error(codes.PermissionDenied, "only sellers can create shop discount programs")
	}
	if claims.ShopID == "" {
		return nil, status.Error(codes.PermissionDenied, "seller account has no shop_id")
	}
	shopID := strings.TrimSpace(req.GetShopId())
	if shopID != "" && shopID != claims.ShopID {
		return nil, status.Error(codes.PermissionDenied, "sellers can only create discount programs for their own shop")
	}
	shopID = claims.ShopID
	if err := s.requireShopNotSuspended(ctx, shopID); err != nil {
		return nil, err
	}

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "program name is required")
	}
	if req.GetStartsAt() == nil || req.GetEndsAt() == nil {
		return nil, status.Error(codes.InvalidArgument, "starts_at and ends_at are required")
	}
	startsAt := req.GetStartsAt().AsTime().UTC()
	endsAt := req.GetEndsAt().AsTime().UTC()
	now := s.nowFn()
	if !endsAt.After(startsAt) {
		return nil, status.Error(codes.InvalidArgument, "ends_at must be after starts_at")
	}
	if !endsAt.After(now) {
		return nil, status.Error(codes.InvalidArgument, "ends_at must be in the future")
	}

	items, warnings, err := s.validateShopDiscountProgramItems(ctx, shopID, req.GetItems(), startsAt, endsAt, now)
	if err != nil {
		return nil, err
	}

	created, err := s.store.CreateShopDiscountProgram(ctx, domain.ShopDiscountProgram{
		ShopID:    shopID,
		Name:      name,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		CreatedBy: claims.UserID(),
		Items:     items,
	}, now)
	if err != nil {
		if errors.Is(err, store.ErrOverlappingShopProgram) {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "create shop discount program: %v", err)
	}

	return &promotionv1.CreateShopDiscountProgramResponse{
		Program:  shopDiscountProgramToProto(created, now, warnings),
		Warnings: warnings,
	}, nil
}

// UpdateShopDiscountProgram updates a non-ended shop discount program owned by the seller.
func (s *PromotionService) UpdateShopDiscountProgram(ctx context.Context, req *promotionv1.UpdateShopDiscountProgramRequest) (*promotionv1.UpdateShopDiscountProgramResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !isSeller(claims) || claims.ShopID == "" {
		return nil, status.Error(codes.PermissionDenied, "only sellers can update shop discount programs")
	}
	if err := s.requireShopNotSuspended(ctx, claims.ShopID); err != nil {
		return nil, err
	}

	programID := strings.TrimSpace(req.GetProgramId())
	if programID == "" {
		return nil, status.Error(codes.InvalidArgument, "program_id is required")
	}

	existing, err := s.store.GetShopDiscountProgram(ctx, programID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "shop discount program not found")
		}
		return nil, status.Errorf(codes.Internal, "get shop discount program: %v", err)
	}
	if existing.ShopID != claims.ShopID {
		return nil, status.Error(codes.PermissionDenied, "sellers can only update their own shop discount programs")
	}

	now := s.nowFn()
	if existing.EffectiveStatus(now) == domain.ShopProgramStatusEnded {
		return nil, status.Error(codes.FailedPrecondition, "cannot update an ended shop discount program")
	}

	newName := existing.Name
	if req.Name != nil {
		trimmed := strings.TrimSpace(req.GetName())
		if trimmed == "" {
			return nil, status.Error(codes.InvalidArgument, "program name must not be empty")
		}
		newName = trimmed
	}

	newStartsAt := existing.StartsAt.UTC()
	if req.GetStartsAt() != nil {
		candStart := req.GetStartsAt().AsTime().UTC()
		if !now.Before(existing.StartsAt) && !candStart.Equal(existing.StartsAt.UTC()) {
			return nil, status.Error(codes.FailedPrecondition, "cannot change starts_at of an already active program")
		}
		newStartsAt = candStart
	}

	newEndsAt := existing.EndsAt.UTC()
	if req.GetEndsAt() != nil {
		newEndsAt = req.GetEndsAt().AsTime().UTC()
	}
	if !newEndsAt.After(newStartsAt) {
		return nil, status.Error(codes.InvalidArgument, "ends_at must be after starts_at")
	}
	if !newEndsAt.After(now) {
		return nil, status.Error(codes.InvalidArgument, "ends_at must be in the future")
	}

	replaceItems := req.GetReplaceItems() || len(req.GetItems()) > 0
	var validatedItems []domain.ShopDiscountProgramItem
	var warnings []string
	var updatedWarnings map[string]string

	if replaceItems {
		validatedItems, warnings, err = s.validateShopDiscountProgramItems(ctx, existing.ShopID, req.GetItems(), newStartsAt, newEndsAt, now)
		if err != nil {
			return nil, err
		}
	} else {
		// Always re-check flash overlap warnings for every SKU against the updated window,
		// even if the item already carried an inflation or other warning, and persist new warnings.
		updatedWarnings = make(map[string]string, len(existing.Items))
		for _, it := range existing.Items {
			merged := it.Warning
			if merged != "" {
				warnings = append(warnings, merged)
			}
			hasFlash, fErr := s.store.HasOverlappingApprovedFlashSale(ctx, it.SKUID, newStartsAt, newEndsAt, now)
			if fErr != nil {
				return nil, status.Errorf(codes.Internal, "check flash sale overlap: %v", fErr)
			}
			if hasFlash {
				flashWarn := fmt.Sprintf("SKU %s overlaps with an approved flash sale window; flash sale price will take precedence during the flash window", it.SKUID)
				if !strings.Contains(merged, flashWarn) {
					warnings = append(warnings, flashWarn)
					if merged != "" {
						merged += "; " + flashWarn
					} else {
						merged = flashWarn
					}
					updatedWarnings[it.ID] = merged
				}
			}
		}
	}

	updated, err := s.store.UpdateShopDiscountProgram(ctx, programID, now, func(cur *domain.ShopDiscountProgram) (bool, error) {
		if cur.ShopID != claims.ShopID {
			return false, status.Error(codes.PermissionDenied, "sellers can only update their own shop discount programs")
		}
		if cur.EffectiveStatus(now) == domain.ShopProgramStatusEnded {
			return false, status.Error(codes.FailedPrecondition, "cannot update an ended shop discount program")
		}
		cur.Name = newName
		cur.StartsAt = newStartsAt
		cur.EndsAt = newEndsAt
		if replaceItems {
			cur.Items = validatedItems
		} else if len(updatedWarnings) > 0 {
			for i := range cur.Items {
				if w, ok := updatedWarnings[cur.Items[i].ID]; ok {
					cur.Items[i].Warning = w
				}
			}
		}
		return replaceItems, nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "shop discount program not found")
		}
		if errors.Is(err, store.ErrOverlappingShopProgram) {
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		if _, isGRPC := status.FromError(err); isGRPC {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "update shop discount program: %v", err)
	}

	return &promotionv1.UpdateShopDiscountProgramResponse{
		Program:  shopDiscountProgramToProto(updated, now, warnings),
		Warnings: warnings,
	}, nil
}

// EndShopDiscountProgram immediately ends a shop discount program owned by the seller.
func (s *PromotionService) EndShopDiscountProgram(ctx context.Context, req *promotionv1.EndShopDiscountProgramRequest) (*promotionv1.EndShopDiscountProgramResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !isSeller(claims) || claims.ShopID == "" {
		return nil, status.Error(codes.PermissionDenied, "only sellers can end shop discount programs")
	}

	programID := strings.TrimSpace(req.GetProgramId())
	if programID == "" {
		return nil, status.Error(codes.InvalidArgument, "program_id is required")
	}

	now := s.nowFn()
	ended, err := s.store.EndShopDiscountProgram(ctx, programID, now, func(cur *domain.ShopDiscountProgram) error {
		if cur.ShopID != claims.ShopID {
			return status.Error(codes.PermissionDenied, "sellers can only end their own shop discount programs")
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "shop discount program not found")
		}
		if _, isGRPC := status.FromError(err); isGRPC {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "end shop discount program: %v", err)
	}

	return &promotionv1.EndShopDiscountProgramResponse{
		Program: shopDiscountProgramToProto(ended, now, nil),
	}, nil
}

// ListShopDiscountPrograms returns a paginated list of shop discount programs for seller/admin.
func (s *PromotionService) ListShopDiscountPrograms(ctx context.Context, req *promotionv1.ListShopDiscountProgramsRequest) (*promotionv1.ListShopDiscountProgramsResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}

	shopID := strings.TrimSpace(req.GetShopId())
	switch {
	case isSeller(claims):
		if claims.ShopID == "" {
			return nil, status.Error(codes.PermissionDenied, "seller account has no shop_id")
		}
		if shopID != "" && shopID != claims.ShopID {
			return nil, status.Error(codes.PermissionDenied, "sellers can only list their own shop discount programs")
		}
		shopID = claims.ShopID
	case auth.HasCapability(claims, auth.CapVouchersRead):
		// Authorized staff with vouchers.read may filter by shop_id or list across all shops.
	default:
		return nil, status.Error(codes.PermissionDenied, "only sellers or authorized admins can list shop discount programs")
	}

	var statusFilter string
	switch req.GetStatus() {
	case promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_UNSPECIFIED:
		statusFilter = ""
	case promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_SCHEDULED:
		statusFilter = domain.ShopProgramStatusScheduled
	case promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE:
		statusFilter = domain.ShopProgramStatusActive
	case promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ENDED:
		statusFilter = domain.ShopProgramStatusEnded
	default:
		return nil, status.Errorf(codes.InvalidArgument, "invalid shop discount program status: %v", req.GetStatus())
	}

	now := s.nowFn()
	programs, total, err := s.store.ListShopDiscountPrograms(ctx, shopID, statusFilter, now, req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list shop discount programs: %v", err)
	}

	pbProgs := make([]*promotionv1.ShopDiscountProgram, 0, len(programs))
	for i := range programs {
		pbProgs = append(pbProgs, shopDiscountProgramToProto(&programs[i], now, nil))
	}
	return &promotionv1.ListShopDiscountProgramsResponse{
		Programs: pbProgs,
		Total:    total,
	}, nil
}

const (
	maxActiveShopDiscountSKUs = 200
)

// ListActiveShopDiscounts returns currently live shop discount program items (public storefront & checkout).
// Requires at least one of sku_ids or shop_id, caps sku_ids at 200, and caps returned rows at 500.
func (s *PromotionService) ListActiveShopDiscounts(ctx context.Context, req *promotionv1.ListActiveShopDiscountsRequest) (*promotionv1.ListActiveShopDiscountsResponse, error) {
	if len(req.GetSkuIds()) > maxActiveShopDiscountSKUs {
		return nil, status.Errorf(codes.InvalidArgument, "sku_ids exceeds maximum limit of %d", maxActiveShopDiscountSKUs)
	}
	skuIDs := normalizeUniqueStrings(req.GetSkuIds())
	shopID := strings.TrimSpace(req.GetShopId())
	if len(skuIDs) == 0 && shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "at least one of sku_ids or shop_id is required")
	}

	items, err := s.store.ListActiveShopDiscounts(ctx, skuIDs, shopID, s.nowFn())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list active shop discounts: %v", err)
	}

	out := make([]*promotionv1.ActiveShopDiscount, 0, len(items))
	for _, it := range items {
		out = append(out, &promotionv1.ActiveShopDiscount{
			ItemId:             it.ID,
			ProgramId:          it.ProgramID,
			ShopId:             it.ShopID,
			SkuId:              it.SKUID,
			OriginalPriceVnd:   it.OriginalPriceVND,
			DiscountedPriceVnd: it.DiscountedPriceVND,
			DiscountPercent:    it.DiscountPercent,
			PerUserLimit:       it.PerUserLimit,
			StartsAt:           timestamppb.New(it.StartsAt),
			EndsAt:             timestamppb.New(it.EndsAt),
		})
	}
	return &promotionv1.ListActiveShopDiscountsResponse{Discounts: out}, nil
}

func (s *PromotionService) validateShopDiscountProgramItems(
	ctx context.Context,
	shopID string,
	inputs []*promotionv1.ShopDiscountProgramItemInput,
	startsAt, endsAt, now time.Time,
) ([]domain.ShopDiscountProgramItem, []string, error) {
	if len(inputs) == 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "at least one item is required")
	}

	seenSKU := make(map[string]bool, len(inputs))
	skuIDs := make([]string, 0, len(inputs))
	for _, in := range inputs {
		skuID := strings.TrimSpace(in.GetSkuId())
		if skuID == "" {
			return nil, nil, status.Error(codes.InvalidArgument, "sku_id is required for every item")
		}
		if seenSKU[skuID] {
			return nil, nil, status.Errorf(codes.InvalidArgument, "duplicate sku_id %s in discount program", skuID)
		}
		seenSKU[skuID] = true
		skuIDs = append(skuIDs, skuID)

		if in.GetPerUserLimit() < 0 {
			return nil, nil, status.Errorf(codes.InvalidArgument, "per_user_limit for sku %s must be >= 0", skuID)
		}
		hasPrice := in.GetDiscountedPriceVnd() > 0
		hasPct := in.GetDiscountPercent() > 0
		if in.GetDiscountedPriceVnd() < 0 || in.GetDiscountPercent() < 0 || in.GetDiscountPercent() >= 100 {
			return nil, nil, status.Errorf(codes.InvalidArgument, "invalid discount specification for sku %s", skuID)
		}
		if hasPrice == hasPct {
			return nil, nil, status.Errorf(codes.InvalidArgument, "specify exactly one of discounted_price_vnd (> 0) or discount_percent (1..99) for sku %s", skuID)
		}
	}

	if s.productClient == nil {
		return nil, nil, status.Error(codes.Unavailable, "product service client is unavailable")
	}
	skuMap, err := s.productClient.GetSKUsBatch(ctx, skuIDs)
	if err != nil {
		return nil, nil, status.Errorf(codes.Unavailable, "fetch SKUs from product service: %v", err)
	}

	items := make([]domain.ShopDiscountProgramItem, 0, len(inputs))
	warnings := make([]string, 0)

	for _, in := range inputs {
		skuID := strings.TrimSpace(in.GetSkuId())
		sku := skuMap[skuID]
		if sku == nil {
			return nil, nil, status.Errorf(codes.NotFound, "sku %s not found", skuID)
		}
		if sku.GetShopId() != shopID {
			return nil, nil, status.Errorf(codes.PermissionDenied, "cannot include another shop's SKU %s in discount program", skuID)
		}
		if !sku.GetIsActive() {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "sku %s is not active", skuID)
		}
		origPrice := sku.GetPriceVnd()
		if origPrice <= 0 {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "sku %s has invalid price", skuID)
		}

		var discPrice int64
		var discPct int32
		if in.GetDiscountedPriceVnd() > 0 {
			discPrice = in.GetDiscountedPriceVnd()
			if discPrice >= origPrice {
				return nil, nil, status.Errorf(codes.InvalidArgument, "discounted_price_vnd (%d) for sku %s must be strictly less than original price (%d)", discPrice, skuID, origPrice)
			}
			pctBig := new(big.Int).Quo(
				new(big.Int).Mul(big.NewInt(origPrice-discPrice), big.NewInt(100)),
				big.NewInt(origPrice),
			)
			discPct = int32(pctBig.Int64())
		} else {
			discPct = in.GetDiscountPercent()
			priceBig := new(big.Int).Quo(
				new(big.Int).Mul(big.NewInt(origPrice), big.NewInt(int64(100-discPct))),
				big.NewInt(100),
			)
			discPrice = priceBig.Int64()
			if discPrice <= 0 || discPrice >= origPrice {
				return nil, nil, status.Errorf(codes.InvalidArgument, "computed discounted price (%d) for sku %s is invalid", discPrice, skuID)
			}
		}

		var itemWarns []string
		inflationFlagged := false
		minPrice30d := effectiveMinPrice30d(sku)
		if minPrice30d > 0 && origPrice > minPrice30d {
			inflationFlagged = true
			w := fmt.Sprintf("SKU %s had a lower price (%d VND) in the last 30 days than current price (%d VND); flagged for price inflation", skuID, minPrice30d, origPrice)
			itemWarns = append(itemWarns, w)
			warnings = append(warnings, w)
		}

		hasFlashOverlap, fErr := s.store.HasOverlappingApprovedFlashSale(ctx, skuID, startsAt, endsAt, now)
		if fErr != nil {
			return nil, nil, status.Errorf(codes.Internal, "check approved flash sale overlap for sku %s: %v", skuID, fErr)
		}
		if hasFlashOverlap {
			w := fmt.Sprintf("SKU %s overlaps with an approved flash sale window; flash sale price will take precedence during the flash window", skuID)
			itemWarns = append(itemWarns, w)
			warnings = append(warnings, w)
		}

		items = append(items, domain.ShopDiscountProgramItem{
			ShopID:                shopID,
			SKUID:                 skuID,
			OriginalPriceVND:      origPrice,
			DiscountedPriceVND:    discPrice,
			DiscountPercent:       discPct,
			PerUserLimit:          in.GetPerUserLimit(),
			PriceInflationFlagged: inflationFlagged,
			Warning:               strings.Join(itemWarns, "; "),
		})
	}
	return items, warnings, nil
}

func shopDiscountProgramToProto(p *domain.ShopDiscountProgram, now time.Time, extraWarnings []string) *promotionv1.ShopDiscountProgram {
	if p == nil {
		return nil
	}
	effStatus := p.EffectiveStatus(now)
	items := make([]*promotionv1.ShopDiscountProgramItem, 0, len(p.Items))
	warnings := make([]string, 0, len(extraWarnings))
	seenWarn := make(map[string]bool)
	addWarn := func(w string) {
		w = strings.TrimSpace(w)
		if w != "" && !seenWarn[w] {
			seenWarn[w] = true
			warnings = append(warnings, w)
		}
	}
	for _, w := range extraWarnings {
		addWarn(w)
	}
	for _, it := range p.Items {
		if it.Warning != "" {
			addWarn(it.Warning)
		}
		items = append(items, &promotionv1.ShopDiscountProgramItem{
			Id:                    it.ID,
			ProgramId:             it.ProgramID,
			ShopId:                it.ShopID,
			SkuId:                 it.SKUID,
			OriginalPriceVnd:      it.OriginalPriceVND,
			DiscountedPriceVnd:    it.DiscountedPriceVND,
			DiscountPercent:       it.DiscountPercent,
			PerUserLimit:          it.PerUserLimit,
			PriceInflationFlagged: it.PriceInflationFlagged,
			Warning:               it.Warning,
		})
	}
	return &promotionv1.ShopDiscountProgram{
		Id:        p.ID,
		ShopId:    p.ShopID,
		Name:      p.Name,
		StartsAt:  timestamppb.New(p.StartsAt),
		EndsAt:    timestamppb.New(p.EndsAt),
		Status:    domainShopProgramStatusToProto(effStatus),
		Items:     items,
		Warnings:  warnings,
		CreatedAt: timestamppb.New(p.CreatedAt),
		UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
}

func domainShopProgramStatusToProto(st string) promotionv1.ShopDiscountProgramStatus {
	switch st {
	case domain.ShopProgramStatusScheduled:
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_SCHEDULED
	case domain.ShopProgramStatusActive:
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE
	case domain.ShopProgramStatusEnded:
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ENDED
	default:
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_UNSPECIFIED
	}
}
