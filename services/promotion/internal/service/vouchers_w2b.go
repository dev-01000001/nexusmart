package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

// UpdateVoucher updates mutable fields of an existing voucher.
// Once used_count > 0, only name, raising quota/budget_vnd, and extending ends_at are permitted.
// Code is immutable; deleted vouchers cannot be updated.
func (s *PromotionService) UpdateVoucher(ctx context.Context, req *promotionv1.UpdateVoucherRequest) (*promotionv1.UpdateVoucherResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !isSeller(claims) && !auth.HasCapability(claims, auth.CapVouchersWrite) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions to manage voucher")
	}
	if isSeller(claims) && claims.ShopID != "" {
		if err := s.requireShopNotSuspended(ctx, claims.ShopID); err != nil {
			return nil, err
		}
	}

	voucherID := strings.TrimSpace(req.GetVoucherId())
	if voucherID == "" {
		return nil, status.Error(codes.InvalidArgument, "voucher_id is required")
	}

	after, err := s.store.UpdateVoucher(ctx, voucherID, func(cur *domain.Voucher, hasActiveReservation bool) error {
		if err := checkVoucherManagePermission(claims, cur); err != nil {
			return err
		}
		if cur.EffectiveStatus() == domain.VoucherStatusDeleted {
			return status.Error(codes.FailedPrecondition, "cannot update a deleted voucher")
		}

		// Snapshot live Redis usage INSIDE the Postgres row lock to prevent TOCTOU with ReservePromotion.
		// Only trust Redis liveUsed when redis_synced_version == redis_sync_version (a prior failed sync
		// can leave Redis remaining below the new PG quota even on an unused voucher).
		// Bounded to lockedRedisTimeout (<= 500ms) so Redis stalls never hold row locks or drain pgxpool.
		var liveUsed int32
		redisCheckFailed := false
		if cur.RedisSyncedVersion == cur.RedisSyncVersion {
			rCtx, rCancel := s.lockedRedisCtx(ctx)
			remQuota, _, _, cErr := s.readVoucherLiveCounters(rCtx, cur, "")
			rCancel()
			if cErr != nil {
				redisCheckFailed = true
			} else if u := cur.Quota - remQuota; u > 0 {
				liveUsed = u
			}
		}

		hasUsed := cur.UsedCount > 0 || hasActiveReservation || liveUsed > 0 || redisCheckFailed

		newName := cur.Name
		if req.Name != nil {
			trimmed := strings.TrimSpace(req.GetName())
			if trimmed == "" {
				return status.Error(codes.InvalidArgument, "voucher name must not be empty")
			}
			newName = trimmed
		}

		newDiscType := cur.DiscountType
		if req.DiscountType != nil {
			parsed, dtErr := protoDiscountTypeToDomain(req.GetDiscountType())
			if dtErr != nil {
				return status.Error(codes.InvalidArgument, dtErr.Error())
			}
			newDiscType = parsed
		}

		newValueVND := cur.ValueVND
		if req.ValueVnd != nil {
			newValueVND = req.GetValueVnd()
		}

		newDiscPct := cur.DiscountPercent
		if req.DiscountPercent != nil {
			newDiscPct = req.GetDiscountPercent()
		}

		newMaxDisc := cur.MaxDiscountVND
		if req.MaxDiscountVnd != nil {
			newMaxDisc = req.GetMaxDiscountVnd()
		}

		newMinOrder := cur.MinOrderVND
		if req.MinOrderVnd != nil {
			newMinOrder = req.GetMinOrderVnd()
		}

		newQuota := cur.Quota
		if req.Quota != nil {
			newQuota = req.GetQuota()
		}

		newPerUser := cur.PerUserLimit
		if req.PerUserLimit != nil {
			newPerUser = req.GetPerUserLimit()
		}

		newBudget := cur.BudgetVND
		if req.BudgetVnd != nil {
			newBudget = req.GetBudgetVnd()
		}

		newStartsAt := cur.StartsAt.UTC()
		if req.GetStartsAt() != nil {
			newStartsAt = req.GetStartsAt().AsTime().UTC()
		}

		newEndsAt := cur.EndsAt.UTC()
		if req.GetEndsAt() != nil {
			newEndsAt = req.GetEndsAt().AsTime().UTC()
		}

		newCatIDs := cur.CategoryIDs
		catRequested := req.GetReplaceCategoryIds() || len(req.GetCategoryIds()) > 0
		if catRequested {
			newCatIDs = normalizeUniqueStrings(req.GetCategoryIds())
			if len(newCatIDs) > 0 && cur.Scope != domain.VoucherScopePlatform {
				return status.Error(codes.InvalidArgument, "category_ids is only supported for PLATFORM vouchers")
			}
		}

		if hasUsed {
			if newDiscType != cur.DiscountType {
				return status.Error(codes.FailedPrecondition, "cannot change discount_type after voucher has been used")
			}
			if newValueVND != cur.ValueVND {
				return status.Error(codes.FailedPrecondition, "cannot change value_vnd after voucher has been used")
			}
			if newDiscPct != cur.DiscountPercent {
				return status.Error(codes.FailedPrecondition, "cannot change discount_percent after voucher has been used")
			}
			if newMaxDisc != cur.MaxDiscountVND {
				return status.Error(codes.FailedPrecondition, "cannot change max_discount_vnd after voucher has been used")
			}
			if newMinOrder != cur.MinOrderVND {
				return status.Error(codes.FailedPrecondition, "cannot change min_order_vnd after voucher has been used")
			}
			if newPerUser != cur.PerUserLimit {
				return status.Error(codes.FailedPrecondition, "cannot change per_user_limit after voucher has been used")
			}
			if !newStartsAt.Equal(cur.StartsAt.UTC()) {
				return status.Error(codes.FailedPrecondition, "cannot change starts_at after voucher has been used")
			}
			if catRequested && !equalStringSets(cur.CategoryIDs, newCatIDs) {
				return status.Error(codes.FailedPrecondition, "cannot change category_ids after voucher has been used")
			}
			if newQuota < cur.Quota {
				return status.Error(codes.FailedPrecondition, "cannot decrease quota after voucher has been used")
			}
			if cur.BudgetVND == 0 && newBudget != 0 {
				return status.Error(codes.FailedPrecondition, "cannot add a finite budget cap after voucher has been used")
			}
			if cur.BudgetVND > 0 && newBudget < cur.BudgetVND {
				return status.Error(codes.FailedPrecondition, "cannot decrease budget_vnd after voucher has been used")
			}
			if newEndsAt.Before(cur.EndsAt.UTC()) {
				return status.Error(codes.FailedPrecondition, "cannot shorten ends_at after voucher has been used")
			}
		} else {
			// Clear opposite-type fields when switching discount_type while unused.
			if newDiscType != cur.DiscountType {
				if newDiscType == domain.DiscountTypeFixed && req.DiscountPercent == nil {
					newDiscPct = 0
				}
				if newDiscType == domain.DiscountTypeFixed && req.MaxDiscountVnd == nil {
					newMaxDisc = 0
				}
				if newDiscType == domain.DiscountTypePercent && req.ValueVnd == nil {
					newValueVND = 0
				}
			}
		}

		if newDiscType == domain.DiscountTypeFixed {
			if newValueVND <= 0 {
				return status.Error(codes.InvalidArgument, "value_vnd must be > 0 for FIXED discount")
			}
			if newDiscPct != 0 {
				return status.Error(codes.InvalidArgument, "discount_percent must be 0 for FIXED discount")
			}
		} else {
			if newDiscPct < 1 || newDiscPct > 100 {
				return status.Error(codes.InvalidArgument, "discount_percent must be between 1 and 100 for PERCENT discount")
			}
			if newValueVND != 0 {
				return status.Error(codes.InvalidArgument, "value_vnd must be 0 for PERCENT discount")
			}
		}
		if newMaxDisc < 0 || newMinOrder < 0 || newBudget < 0 {
			return status.Error(codes.InvalidArgument, "monetary fields must be non-negative")
		}
		if newQuota <= 0 {
			return status.Error(codes.InvalidArgument, "quota must be > 0")
		}
		if newPerUser <= 0 {
			return status.Error(codes.InvalidArgument, "per_user_limit must be > 0")
		}
		if !newEndsAt.After(newStartsAt) {
			return status.Error(codes.InvalidArgument, "ends_at must be after starts_at")
		}

		ruleChanged := newDiscType != cur.DiscountType ||
			newValueVND != cur.ValueVND ||
			newDiscPct != cur.DiscountPercent ||
			newMaxDisc != cur.MaxDiscountVND ||
			newMinOrder != cur.MinOrderVND ||
			newPerUser != cur.PerUserLimit ||
			!newStartsAt.Equal(cur.StartsAt.UTC()) ||
			(catRequested && !equalStringSets(cur.CategoryIDs, newCatIDs)) ||
			newQuota < cur.Quota ||
			(cur.BudgetVND == 0 && newBudget != 0) ||
			(cur.BudgetVND > 0 && newBudget < cur.BudgetVND) ||
			newEndsAt.Before(cur.EndsAt.UTC())

		if ruleChanged {
			if cur.RulesVersion <= 0 {
				cur.RulesVersion = 1
			}
			cur.RulesVersion++
		}

		cur.Name = newName
		cur.DiscountType = newDiscType
		cur.ValueVND = newValueVND
		cur.DiscountPercent = newDiscPct
		cur.MaxDiscountVND = newMaxDisc
		cur.MinOrderVND = newMinOrder
		cur.Quota = newQuota
		cur.PerUserLimit = newPerUser
		cur.BudgetVND = newBudget
		cur.StartsAt = newStartsAt
		cur.EndsAt = newEndsAt
		cur.CategoryIDs = newCatIDs
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "voucher not found")
		}
		if _, isGRPC := status.FromError(err); isGRPC {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "update voucher: %v", err)
	}

	if _, err := s.syncVoucherRedis(ctx, after.ID, false); err != nil {
		return nil, status.Errorf(codes.Internal, "sync voucher redis counters: %v", err)
	}

	remQuota, remBudget, _, err := s.readVoucherLiveCounters(ctx, after, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read live voucher counters: %v", err)
	}
	return &promotionv1.UpdateVoucherResponse{
		Voucher: voucherToProto(after, remQuota, remBudget, 0, false, 0, s.nowFn()),
	}, nil
}

// SetVoucherStatus switches a voucher between ACTIVE and PAUSED and immediately syncs Redis.
func (s *PromotionService) SetVoucherStatus(ctx context.Context, req *promotionv1.SetVoucherStatusRequest) (*promotionv1.SetVoucherStatusResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !isSeller(claims) && !auth.HasCapability(claims, auth.CapVouchersWrite) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions to manage voucher")
	}
	if isSeller(claims) && claims.ShopID != "" {
		if err := s.requireShopNotSuspended(ctx, claims.ShopID); err != nil {
			return nil, err
		}
	}

	voucherID := strings.TrimSpace(req.GetVoucherId())
	if voucherID == "" {
		return nil, status.Error(codes.InvalidArgument, "voucher_id is required")
	}

	var targetStatus string
	switch req.GetStatus() {
	case promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE:
		targetStatus = domain.VoucherStatusActive
	case promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED:
		targetStatus = domain.VoucherStatusPaused
	default:
		return nil, status.Error(codes.InvalidArgument, "status must be VOUCHER_STATUS_ACTIVE or VOUCHER_STATUS_PAUSED")
	}

	after, err := s.store.SetVoucherStatus(ctx, voucherID, targetStatus, func(cur *domain.Voucher) error {
		if err := checkVoucherManagePermission(claims, cur); err != nil {
			return err
		}
		if cur.EffectiveStatus() == domain.VoucherStatusDeleted {
			return status.Error(codes.FailedPrecondition, "deleted voucher cannot be reactivated or paused")
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "voucher not found")
		}
		if _, isGRPC := status.FromError(err); isGRPC {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "set voucher status: %v", err)
	}

	if _, err := s.syncVoucherRedis(ctx, after.ID, false); err != nil {
		return nil, status.Errorf(codes.Internal, "sync voucher status to redis: %v", err)
	}

	remQuota, remBudget, _, err := s.readVoucherLiveCounters(ctx, after, "")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read live voucher counters: %v", err)
	}
	return &promotionv1.SetVoucherStatusResponse{
		Voucher: voucherToProto(after, remQuota, remBudget, 0, false, 0, s.nowFn()),
	}, nil
}

// DeleteVoucher soft-deletes a voucher (status -> DELETED) and immediately blocks it in Redis.
func (s *PromotionService) DeleteVoucher(ctx context.Context, req *promotionv1.DeleteVoucherRequest) (*promotionv1.DeleteVoucherResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !isSeller(claims) && !auth.HasCapability(claims, auth.CapVouchersWrite) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions to manage voucher")
	}

	voucherID := strings.TrimSpace(req.GetVoucherId())
	if voucherID == "" {
		return nil, status.Error(codes.InvalidArgument, "voucher_id is required")
	}

	if _, err := s.store.DeleteVoucher(ctx, voucherID, func(cur *domain.Voucher) error {
		return checkVoucherManagePermission(claims, cur)
	}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "voucher not found")
		}
		if _, isGRPC := status.FromError(err); isGRPC {
			return nil, err
		}
		return nil, status.Errorf(codes.Internal, "delete voucher: %v", err)
	}

	if _, err := s.syncVoucherRedis(ctx, voucherID, false); err != nil {
		return nil, status.Errorf(codes.Internal, "sync deleted voucher status to redis: %v", err)
	}
	return &promotionv1.DeleteVoucherResponse{Ok: true}, nil
}

func requireEndUserClaims(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service identity not allowed for this RPC")
	}
	return claims, nil
}

func checkVoucherManagePermission(claims *auth.Claims, v *domain.Voucher) error {
	switch {
	case isSeller(claims):
		if v.Scope != domain.VoucherScopeShop {
			return status.Error(codes.PermissionDenied, "sellers can only manage SHOP vouchers")
		}
		if claims.ShopID == "" || v.ShopID != claims.ShopID {
			return status.Error(codes.PermissionDenied, "sellers can only manage vouchers for their own shop")
		}
		return nil
	case auth.HasCapability(claims, auth.CapVouchersWrite):
		if v.Scope != domain.VoucherScopePlatform && v.Scope != domain.VoucherScopeFreeship {
			return status.Error(codes.PermissionDenied, "admins can only manage PLATFORM or FREESHIP vouchers")
		}
		return nil
	default:
		return status.Error(codes.PermissionDenied, "insufficient permissions to manage voucher")
	}
}

func (s *PromotionService) resolveAllowedCategories(ctx context.Context, categoryIDs []string) (map[string]bool, error) {
	normalized := normalizeUniqueStrings(categoryIDs)
	if len(normalized) == 0 {
		return nil, nil
	}

	ids := normalized
	if s.productClient != nil {
		expanded, err := s.productClient.GetCategoryDescendants(ctx, normalized)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "resolve category descendants: %v", err)
		}
		ids = append(ids, expanded...)
	}

	allowed := make(map[string]bool, len(ids))
	for _, cid := range ids {
		if trimmed := strings.TrimSpace(cid); trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return allowed, nil
}

func normalizeUniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func equalStringSets(a, b []string) bool {
	na := normalizeUniqueStrings(a)
	nb := normalizeUniqueStrings(b)
	if len(na) != len(nb) {
		return false
	}
	sort.Strings(na)
	sort.Strings(nb)
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

func protoVoucherStatusToDomain(st promotionv1.VoucherStatus) (string, error) {
	switch st {
	case promotionv1.VoucherStatus_VOUCHER_STATUS_UNSPECIFIED:
		return "", nil
	case promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE:
		return domain.VoucherStatusActive, nil
	case promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED:
		return domain.VoucherStatusPaused, nil
	case promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED:
		return domain.VoucherStatusDeleted, nil
	default:
		return "", fmt.Errorf("invalid voucher status: %v", st)
	}
}

func domainVoucherStatusToProto(st string) promotionv1.VoucherStatus {
	switch st {
	case domain.VoucherStatusActive:
		return promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE
	case domain.VoucherStatusPaused:
		return promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED
	case domain.VoucherStatusDeleted:
		return promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED
	default:
		return promotionv1.VoucherStatus_VOUCHER_STATUS_UNSPECIFIED
	}
}
