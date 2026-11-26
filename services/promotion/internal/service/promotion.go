package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/promotion/internal/calculator"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

// orderServiceTokenName matches InternalServiceName in services/order/internal/clients/clients.go.
const (
	orderServiceTokenName     = "order-service"
	defaultLockedRedisTimeout = 500 * time.Millisecond
	maxConcurrentRedisSyncs   = 4
)

// ProductClient abstracts ProductService calls needed for flash/shop-discount validation
// and category descendant expansion.
type ProductClient interface {
	GetSKUsBatch(ctx context.Context, skuIDs []string) (map[string]*productv1.SKU, error)
	GetCategoryDescendants(ctx context.Context, categoryIDs []string) ([]string, error)
}

// PromotionService implements nexusmart.promotion.v1.PromotionServiceServer.
type PromotionService struct {
	promotionv1.UnimplementedPromotionServiceServer

	store              store.Store
	engine             *lua.Engine
	productClient      ProductClient
	logger             *slog.Logger
	nowFn              func() time.Time
	lockedRedisTimeout time.Duration
	redisSyncSem       chan struct{}
}

// NewPromotionService creates a PromotionService wired to a Store and Redis Lua Engine.
func NewPromotionService(st store.Store, engine *lua.Engine, logger *slog.Logger) *PromotionService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PromotionService{
		store:              st,
		engine:             engine,
		logger:             logger,
		nowFn:              func() time.Time { return time.Now().UTC() },
		lockedRedisTimeout: defaultLockedRedisTimeout,
		redisSyncSem:       make(chan struct{}, maxConcurrentRedisSyncs),
	}
}

func (s *PromotionService) acquireRedisSyncSlot(ctx context.Context) error {
	if s.redisSyncSem == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.redisSyncSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *PromotionService) releaseRedisSyncSlot() {
	if s.redisSyncSem != nil {
		<-s.redisSyncSem
	}
}

func (s *PromotionService) lockedRedisCtx(parent context.Context) (context.Context, context.CancelFunc) {
	timeout := s.lockedRedisTimeout
	if timeout <= 0 {
		timeout = defaultLockedRedisTimeout
	}
	return context.WithTimeout(parent, timeout)
}

// SetProductClient wires the upstream ProductService client (used for SKU validation & category descendants).
func (s *PromotionService) SetProductClient(pc ProductClient) {
	s.productClient = pc
}

// SetClock overrides the wall clock function (intended for deterministic unit tests).
func (s *PromotionService) SetClock(fn func() time.Time) {
	if fn != nil {
		s.nowFn = fn
	}
}

// SyncRedisFromStore authoritatively converges Redis voucher keys and flash sale slot counters
// from Postgres on startup so restarts heal any prior Redis sync drift or wipe.
// Per-user :user: keys use 120s TTL + max(Redis, PG) reads and are not bulk-loaded on startup (D12).
func (s *PromotionService) SyncRedisFromStore(ctx context.Context) error {
	vouchers, err := s.store.ListAllVouchers(ctx)
	if err != nil {
		return fmt.Errorf("list vouchers for redis sync: %w", err)
	}
	for _, v := range vouchers {
		if _, syncErr := s.syncVoucherRedis(ctx, v.ID, false); syncErr != nil {
			return fmt.Errorf("sync voucher %s to redis: %w", v.ID, syncErr)
		}
	}

	now := s.nowFn()
	items, err := s.store.ListActiveApprovedFlashSaleItems(ctx, now)
	if err != nil {
		return fmt.Errorf("list approved flash items for redis sync: %w", err)
	}
	for _, item := range items {
		if item.WindowStatus == domain.FlashWindowStatusEnded || !item.EndsAt.After(now) {
			continue
		}
		if _, syncErr := s.syncFlashItemRedis(ctx, item.ID, false); syncErr != nil {
			return fmt.Errorf("sync flash item %s to redis: %w", item.ID, syncErr)
		}
	}
	return nil
}

// ReconcileVoucherRedisOnce reconciles any vouchers whose Postgres redis_sync_version is ahead of
// redis_synced_version (for example after a post-commit Redis write failure).
func (s *PromotionService) ReconcileVoucherRedisOnce(ctx context.Context) (int, error) {
	if err := s.acquireRedisSyncSlot(ctx); err != nil {
		return 0, err
	}
	defer s.releaseRedisSyncSlot()
	return s.store.ReconcilePendingVouchers(ctx, 100, s.reconcileVoucherFn)
}

func (s *PromotionService) reconcileVoucherFn(ctx context.Context, v *domain.Voucher) error {
	rCtx, cancel := s.lockedRedisCtx(ctx)
	defer cancel()
	return s.engine.ReconcileVoucherKeys(
		rCtx,
		v.ID,
		v.EffectiveStatus(),
		v.Quota,
		v.UsedCount,
		v.BudgetVND,
		v.UsedBudgetVND,
	)
}

func (s *PromotionService) syncVoucherRedis(ctx context.Context, voucherID string, skipLocked bool) (bool, error) {
	if err := s.acquireRedisSyncSlot(ctx); err != nil {
		return false, err
	}
	defer s.releaseRedisSyncSlot()
	return s.store.SyncVoucherRedis(ctx, voucherID, skipLocked, s.reconcileVoucherFn)
}

func (s *PromotionService) reconcileFlashItemFn(ctx context.Context, item *domain.FlashSaleItem) error {
	rCtx, cancel := s.lockedRedisCtx(ctx)
	defer cancel()
	if item.Status != domain.FlashStatusApproved || item.WindowStatus == domain.FlashWindowStatusEnded {
		return s.engine.DeleteFlashItemSlots(rCtx, item.ID)
	}
	return s.engine.InitFlashItemSlots(rCtx, item.ID, min(max(item.RemainingSlots, 0), item.TotalSlots), true)
}

func (s *PromotionService) syncFlashItemRedis(ctx context.Context, itemID string, skipLocked bool) (bool, error) {
	if err := s.acquireRedisSyncSlot(ctx); err != nil {
		return false, err
	}
	defer s.releaseRedisSyncSlot()
	return s.store.SyncFlashItemRedis(ctx, itemID, skipLocked, s.reconcileFlashItemFn)
}

func (s *PromotionService) syncUserUsageRedis(ctx context.Context, kind, targetID, userID string, skipLocked bool) error {
	if err := s.acquireRedisSyncSlot(ctx); err != nil {
		return err
	}
	defer s.releaseRedisSyncSlot()
	_, err := s.store.SyncUserUsageRedis(ctx, kind, targetID, userID, skipLocked, func(sctx context.Context, usedQty int32) error {
		rCtx, cancel := s.lockedRedisCtx(sctx)
		defer cancel()
		return s.engine.ReconcileUserUsageKeys(rCtx, []domain.UserUsage{{
			Kind:     kind,
			TargetID: targetID,
			UserID:   userID,
			UsedQty:  usedQty,
		}})
	})
	return err
}

type driftSnapshot struct {
	pgRem, redisRem       int64
	redisExists           bool
	pgBudget, redisBudget int64
	budgetExists          bool
	redisStatus           string
}

// RedisDriftTracker stores per-loop PG-vs-Redis drift snapshots across consecutive reconciler ticks.
type RedisDriftTracker map[string]driftSnapshot

// ReconcileRedisDriftOnce compares active vouchers and non-ended APPROVED flash items between Postgres and Redis
// (1 SELECT + 1 MGET per type). Only items showing the exact same (pg, redis) drift on TWO consecutive ticks
// are reconciled under a row lock so in-flight reservations are left untouched (D12).
// ponytail: scans all active vouchers and non-ended approved flash items every 5s (O(active promos) per tick).
func (s *PromotionService) ReconcileRedisDriftOnce(ctx context.Context, prev RedisDriftTracker) (int, error) {
	if prev == nil {
		prev = make(RedisDriftTracker)
	}
	now := s.nowFn()
	seen := make(map[string]struct{})
	reconciled := 0
	var firstErr error
	recordErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	handleDrift := func(tk string, cur driftSnapshot, syncFn func() (bool, error)) bool {
		if old, ok := prev[tk]; ok && old == cur {
			synced, syncErr := syncFn()
			recordErr(syncErr)
			if store.IsRedisConnOrTimeoutErr(syncErr) {
				return true
			}
			if syncErr == nil && synced {
				delete(prev, tk)
				reconciled++
			}
		} else {
			prev[tk] = cur
		}
		return false
	}

	vouchers, err := s.store.ListActiveVouchers(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list active vouchers for drift check: %w", err)
	}
	vKeys := make([]string, 0, len(vouchers)*3)
	for _, v := range vouchers {
		vKeys = append(vKeys, lua.VoucherRemainingKey(v.ID), lua.VoucherStatusKey(v.ID))
		if v.BudgetVND > 0 {
			vKeys = append(vKeys, lua.VoucherBudgetRemainingKey(v.ID))
		}
	}
	vVals, err := s.engine.MGetStrings(ctx, vKeys)
	if err != nil {
		return 0, fmt.Errorf("mget active voucher redis keys: %w", err)
	}
	parseInt := func(k string) (int64, bool) {
		raw, ok := vVals[k]
		if !ok {
			return 0, false
		}
		n, pErr := strconv.ParseInt(raw, 10, 64)
		return n, pErr == nil
	}
	for _, v := range vouchers {
		tk := "v:" + v.ID
		seen[tk] = struct{}{}
		pgRem := int64(min(max(v.Quota-v.UsedCount, 0), v.Quota))
		redisRem, remOK := parseInt(lua.VoucherRemainingKey(v.ID))
		redisStatus, statusOK := vVals[lua.VoucherStatusKey(v.ID)]
		drifted := !remOK || redisRem != pgRem || (statusOK && redisStatus != domain.VoucherStatusActive)
		var pgBud, redisBud int64
		var budOK bool
		if v.BudgetVND > 0 {
			pgBud = min(max(v.BudgetVND-v.UsedBudgetVND, 0), v.BudgetVND)
			redisBud, budOK = parseInt(lua.VoucherBudgetRemainingKey(v.ID))
			if !budOK || redisBud != pgBud {
				drifted = true
			}
		}
		if !drifted {
			delete(prev, tk)
			continue
		}
		cur := driftSnapshot{
			pgRem: pgRem, redisRem: redisRem, redisExists: remOK,
			pgBudget: pgBud, redisBudget: redisBud, budgetExists: budOK,
			redisStatus: redisStatus,
		}
		if handleDrift(tk, cur, func() (bool, error) { return s.syncVoucherRedis(ctx, v.ID, true) }) {
			return reconciled, firstErr
		}
	}

	flashItems, err := s.store.ListActiveApprovedFlashSaleItems(ctx, now)
	if err != nil {
		return reconciled, fmt.Errorf("list active approved flash items for drift check: %w", err)
	}
	fKeys := make([]string, 0, len(flashItems))
	for _, it := range flashItems {
		fKeys = append(fKeys, lua.FlashRemainingKey(it.ID))
	}
	fVals, err := s.engine.MGetInt64(ctx, fKeys)
	if err != nil {
		return reconciled, fmt.Errorf("mget active flash redis keys: %w", err)
	}
	for _, it := range flashItems {
		tk := "f:" + it.ID
		seen[tk] = struct{}{}
		pgRem := int64(min(max(it.RemainingSlots, 0), it.TotalSlots))
		redisRem, remOK := fVals[lua.FlashRemainingKey(it.ID)]
		if remOK && redisRem == pgRem {
			delete(prev, tk)
			continue
		}
		cur := driftSnapshot{pgRem: pgRem, redisRem: redisRem, redisExists: remOK}
		if handleDrift(tk, cur, func() (bool, error) { return s.syncFlashItemRedis(ctx, it.ID, true) }) {
			return reconciled, firstErr
		}
	}

	for k := range prev {
		if _, ok := seen[k]; !ok {
			delete(prev, k)
		}
	}
	return reconciled, firstErr
}

// StartRedisReconciler periodically heals pending voucher updates and persistent PG↔Redis drift until ctx is canceled.
func (s *PromotionService) StartRedisReconciler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	driftTracker := make(RedisDriftTracker)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			reconciled, err := s.ReconcileVoucherRedisOnce(runCtx)
			if !store.IsRedisConnOrTimeoutErr(err) {
				drifted, driftErr := s.ReconcileRedisDriftOnce(runCtx, driftTracker)
				reconciled += drifted
				if err == nil {
					err = driftErr
				}
			}
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				s.logger.Warn("promotion redis reconcile tick encountered errors",
					slog.Int("reconciled", reconciled),
					slog.String("error", err.Error()),
				)
			} else if reconciled > 0 {
				s.logger.Info("reconciled promotion redis counters", slog.Int("reconciled", reconciled))
			}
		}
	}
}

// CreateVoucher creates a shop, platform, or freeship voucher and initializes its Redis counters.
func (s *PromotionService) CreateVoucher(ctx context.Context, req *promotionv1.CreateVoucherRequest) (*promotionv1.CreateVoucherResponse, error) {
	scopeStr, err := protoScopeToDomain(req.GetScope())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	discTypeStr, err := protoDiscountTypeToDomain(req.GetDiscountType())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	shopID := strings.TrimSpace(req.GetShopId())

	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	createdBy := claims.UserID()
	switch {
	case isSeller(claims):
		if scopeStr != domain.VoucherScopeShop {
			return nil, status.Error(codes.PermissionDenied, "sellers can only create SHOP vouchers")
		}
		if claims.ShopID == "" {
			return nil, status.Error(codes.PermissionDenied, "seller account has no shop_id")
		}
		if shopID != "" && shopID != claims.ShopID {
			return nil, status.Error(codes.PermissionDenied, "sellers can only create vouchers for their own shop")
		}
		shopID = claims.ShopID
		if err := s.requireShopNotSuspended(ctx, shopID); err != nil {
			return nil, err
		}
	case auth.HasCapability(claims, auth.CapVouchersWrite):
		if scopeStr != domain.VoucherScopePlatform && scopeStr != domain.VoucherScopeFreeship {
			return nil, status.Error(codes.PermissionDenied, "admins can only create PLATFORM or FREESHIP vouchers")
		}
		shopID = ""
	default:
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions to create voucher")
	}

	code := strings.TrimSpace(req.GetCode())
	if code == "" {
		return nil, status.Error(codes.InvalidArgument, "voucher code is required")
	}
	if err := validateKeySegment("code", code); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		name = code
	}

	if scopeStr == domain.VoucherScopeShop && shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required for SHOP vouchers")
	}
	if scopeStr != domain.VoucherScopeShop && shopID != "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id must be empty for PLATFORM and FREESHIP vouchers")
	}

	categoryIDs := normalizeUniqueStrings(req.GetCategoryIds())
	if len(categoryIDs) > 0 && scopeStr != domain.VoucherScopePlatform {
		return nil, status.Error(codes.InvalidArgument, "category_ids is only supported for PLATFORM vouchers")
	}

	if discTypeStr == domain.DiscountTypeFixed {
		if req.GetValueVnd() <= 0 {
			return nil, status.Error(codes.InvalidArgument, "value_vnd must be > 0 for FIXED discount")
		}
		if req.GetDiscountPercent() != 0 {
			return nil, status.Error(codes.InvalidArgument, "discount_percent must be 0 for FIXED discount")
		}
	} else {
		if req.GetDiscountPercent() < 1 || req.GetDiscountPercent() > 100 {
			return nil, status.Error(codes.InvalidArgument, "discount_percent must be between 1 and 100 for PERCENT discount")
		}
		if req.GetValueVnd() != 0 {
			return nil, status.Error(codes.InvalidArgument, "value_vnd must be 0 for PERCENT discount")
		}
	}

	if req.GetMaxDiscountVnd() < 0 || req.GetMinOrderVnd() < 0 || req.GetBudgetVnd() < 0 {
		return nil, status.Error(codes.InvalidArgument, "monetary fields must be non-negative")
	}
	if req.GetQuota() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "quota must be > 0")
	}
	perUserLimit := req.GetPerUserLimit()
	if perUserLimit == 0 {
		perUserLimit = 1
	}
	if perUserLimit < 0 {
		return nil, status.Error(codes.InvalidArgument, "per_user_limit must be > 0")
	}

	if req.GetStartsAt() == nil || req.GetEndsAt() == nil {
		return nil, status.Error(codes.InvalidArgument, "starts_at and ends_at are required")
	}
	startsAt := req.GetStartsAt().AsTime().UTC()
	endsAt := req.GetEndsAt().AsTime().UTC()
	if !endsAt.After(startsAt) {
		return nil, status.Error(codes.InvalidArgument, "ends_at must be after starts_at")
	}

	created, err := s.store.CreateVoucher(ctx, domain.Voucher{
		Code:            code,
		Name:            name,
		Scope:           scopeStr,
		ShopID:          shopID,
		DiscountType:    discTypeStr,
		ValueVND:        req.GetValueVnd(),
		DiscountPercent: req.GetDiscountPercent(),
		MaxDiscountVND:  req.GetMaxDiscountVnd(),
		MinOrderVND:     req.GetMinOrderVnd(),
		Quota:           req.GetQuota(),
		PerUserLimit:    perUserLimit,
		BudgetVND:       req.GetBudgetVnd(),
		StartsAt:        startsAt,
		EndsAt:          endsAt,
		IsActive:        true,
		Status:          domain.VoucherStatusActive,
		CategoryIDs:     categoryIDs,
		CreatedBy:       createdBy,
	})
	if err != nil {
		if errors.Is(err, store.ErrDuplicateCode) {
			return nil, status.Error(codes.AlreadyExists, "voucher code already exists")
		}
		return nil, status.Errorf(codes.Internal, "create voucher: %v", err)
	}

	if _, err := s.syncVoucherRedis(ctx, created.ID, false); err != nil {
		return nil, status.Errorf(codes.Internal, "initialize voucher redis counters: %v", err)
	}

	now := s.nowFn()
	pb := voucherToProto(created, created.Quota, created.BudgetVND, 0, false, 0, now)
	return &promotionv1.CreateVoucherResponse{Voucher: pb}, nil
}

// ListAvailableVouchers filters vouchers and computes live Redis eligibility + reasons.
func (s *PromotionService) ListAvailableVouchers(ctx context.Context, req *promotionv1.ListAvailableVouchersRequest) (*promotionv1.ListAvailableVouchersResponse, error) {
	channel, storefrontShopID, err := normalizeSalesChannel(req.GetSalesChannel(), req.GetStorefrontShopId())
	if err != nil {
		return nil, err
	}

	var scopeFilter string
	if req.GetScope() != promotionv1.VoucherScope_VOUCHER_SCOPE_UNSPECIFIED {
		scopeFilter, err = protoScopeToDomain(req.GetScope())
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	statusFilter, err := protoVoucherStatusToDomain(req.GetStatus())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	shopFilter := strings.TrimSpace(req.GetShopId())
	reqUserID := strings.TrimSpace(req.GetUserId())
	claims, hasClaims := auth.ClaimsFromContext(ctx)

	// Seller managing their own shop's vouchers (GET /api/v1/seller/vouchers)
	sellerManageView := hasClaims &&
		isSeller(claims) &&
		claims.ShopID != "" &&
		shopFilter == claims.ShopID &&
		(scopeFilter == "" || scopeFilter == domain.VoucherScopeShop) &&
		reqUserID == "" &&
		req.GetOrderAmountVnd() == 0

	// Admin managing vouchers (GET /api/v1/admin/vouchers)
	wantsManageView := req.GetIncludeInactive() || statusFilter != ""
	if wantsManageView && !sellerManageView {
		if !hasClaims {
			return nil, status.Error(codes.Unauthenticated, "authentication required to list inactive or status-filtered vouchers")
		}
		if !auth.HasCapability(claims, auth.CapVouchersRead) {
			return nil, status.Error(codes.PermissionDenied, "insufficient permissions to list vouchers in admin mode")
		}
	}

	adminManageView := hasClaims &&
		auth.HasCapability(claims, auth.CapVouchersRead) &&
		wantsManageView

	manageView := sellerManageView || adminManageView

	var userID string
	switch {
	case !hasClaims:
		if reqUserID != "" {
			return nil, status.Error(codes.Unauthenticated, "authentication required to query user-specific vouchers")
		}
	case auth.HasCapability(claims, auth.CapVouchersRead) || claims.Service != "":
		userID = reqUserID
		if userID == "" && claims.Service == "" && !adminManageView {
			userID = claims.UserID()
		}
	case sellerManageView:
		userID = ""
	default:
		if reqUserID != "" && reqUserID != claims.UserID() {
			return nil, status.Error(codes.PermissionDenied, "cannot access another user's vouchers")
		}
		userID = claims.UserID()
	}

	if channel == domain.SalesChannelStorefront {
		if shopFilter != "" && shopFilter != storefrontShopID {
			return nil, status.Errorf(codes.InvalidArgument, "STOREFRONT_SHOP_MISMATCH: shop_id %q does not match storefront_shop_id %q", shopFilter, storefrontShopID)
		}
		if scopeFilter != "" && scopeFilter != domain.VoucherScopeShop {
			return &promotionv1.ListAvailableVouchersResponse{}, nil
		}
		shopFilter = storefrontShopID
		scopeFilter = domain.VoucherScopeShop
	}

	vouchers, err := s.store.ListVouchers(ctx, shopFilter, scopeFilter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list vouchers: %v", err)
	}
	if len(vouchers) == 0 {
		return &promotionv1.ListAvailableVouchersResponse{}, nil
	}

	savedMap := map[string]bool{}
	if userID != "" {
		savedMap, err = s.store.GetUserSavedVoucherIDs(ctx, userID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "load saved vouchers: %v", err)
		}
	}

	redisKeys := make([]string, 0, len(vouchers)*3)
	var usageTargets []store.UserUsageKey
	if userID != "" {
		usageTargets = make([]store.UserUsageKey, 0, len(vouchers))
	}
	for _, v := range vouchers {
		redisKeys = append(redisKeys, lua.VoucherRemainingKey(v.ID))
		if v.BudgetVND > 0 {
			redisKeys = append(redisKeys, lua.VoucherBudgetRemainingKey(v.ID))
		}
		if userID != "" {
			redisKeys = append(redisKeys, lua.VoucherUserKey(v.ID, userID))
			usageTargets = append(usageTargets, store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: v.ID})
		}
	}
	liveVals, err := s.engine.MGetInt64(ctx, redisKeys)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read live voucher counters: %v", err)
	}
	var pgUsage map[store.UserUsageKey]int32
	if userID != "" {
		pgUsage, err = s.store.GetUserUsageBatch(ctx, userID, usageTargets)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "read user voucher usage: %v", err)
		}
	}

	var suspendedShops map[string]bool
	if !manageView {
		shopIDs := make([]string, 0, len(vouchers))
		for _, v := range vouchers {
			if v.Scope == domain.VoucherScopeShop && v.ShopID != "" {
				shopIDs = append(shopIDs, v.ShopID)
			}
		}
		if len(shopIDs) > 0 {
			suspendedShops, err = s.store.GetSuspendedShopIDs(ctx, shopIDs)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "check suspended shops: %v", err)
			}
		}
	}

	now := s.nowFn()
	resp := &promotionv1.ListAvailableVouchersResponse{
		Vouchers: make([]*promotionv1.Voucher, 0, len(vouchers)),
	}
	for i := range vouchers {
		v := &vouchers[i]
		st := v.EffectiveStatus()

		remQuota := max(v.Quota-v.UsedCount, 0)
		if live, ok := liveVals[lua.VoucherRemainingKey(v.ID)]; ok {
			remQuota = min(remQuota, max(int32(live), 0))
		}

		remBudget := max(v.BudgetVND-v.UsedBudgetVND, 0)
		if v.BudgetVND > 0 {
			if live, ok := liveVals[lua.VoucherBudgetRemainingKey(v.ID)]; ok {
				remBudget = min(remBudget, max(live, 0))
			}
		}

		var userUsed int32
		if userID != "" {
			if live, ok := liveVals[lua.VoucherUserKey(v.ID, userID)]; ok {
				userUsed = int32(live)
			}
			userUsed = max(userUsed, pgUsage[store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: v.ID}])
		}

		if manageView {
			if statusFilter != "" {
				if st != statusFilter {
					continue
				}
			} else if st == domain.VoucherStatusDeleted {
				continue
			}
		} else {
			if v.Scope == domain.VoucherScopeShop && v.ShopID != "" && suspendedShops[v.ShopID] {
				continue
			}
			saved := savedMap[v.ID]
			// Saved PAUSED/DELETED vouchers remain visible in the buyer's wallet as unusable (eligible=false).
			if saved && (st == domain.VoucherStatusPaused || st == domain.VoucherStatusDeleted) {
				resp.Vouchers = append(resp.Vouchers, voucherToProto(v, remQuota, remBudget, userUsed, true, req.GetOrderAmountVnd(), now))
				continue
			}
			if st != domain.VoucherStatusActive ||
				!v.IsActive ||
				now.Before(v.StartsAt) ||
				now.After(v.EndsAt) ||
				remQuota <= 0 ||
				isVoucherBudgetExhausted(v, remBudget, req.GetOrderAmountVnd()) ||
				(userID != "" && v.PerUserLimit > 0 && userUsed >= v.PerUserLimit) {
				continue
			}
		}

		resp.Vouchers = append(resp.Vouchers, voucherToProto(v, remQuota, remBudget, userUsed, savedMap[v.ID], req.GetOrderAmountVnd(), now))
	}
	return resp, nil
}

// SaveVoucher atomically checks voucher status/quota/eligibility and saves the voucher into user's wallet.
func (s *PromotionService) SaveVoucher(ctx context.Context, req *promotionv1.SaveVoucherRequest) (*promotionv1.SaveVoucherResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	lookup := strings.TrimSpace(req.GetVoucherId())
	if lookup == "" {
		lookup = strings.TrimSpace(req.GetCode())
	}
	if lookup == "" {
		return nil, status.Error(codes.InvalidArgument, "voucher_id or code is required")
	}

	v, err := s.store.GetVoucherByIDOrCode(ctx, lookup)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "voucher not found")
		}
		return nil, status.Errorf(codes.Internal, "get voucher: %v", err)
	}
	if v.Scope == domain.VoucherScopeShop && v.ShopID != "" {
		if err := s.requireShopNotSuspended(ctx, v.ShopID); err != nil {
			return nil, err
		}
	}

	now := s.nowFn()
	switch v.EffectiveStatus() {
	case domain.VoucherStatusDeleted:
		return nil, status.Error(codes.FailedPrecondition, "voucher has been deleted")
	case domain.VoucherStatusPaused:
		return nil, status.Error(codes.FailedPrecondition, "voucher is paused")
	}
	if !v.IsActive {
		return nil, status.Error(codes.FailedPrecondition, "voucher is inactive")
	}
	if now.After(v.EndsAt) {
		return nil, status.Error(codes.FailedPrecondition, "voucher has expired")
	}

	remQuota, remBudget, userUsed, err := s.readVoucherLiveCounters(ctx, v, userID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read live voucher counters: %v", err)
	}
	pgUsage, err := s.store.GetUserUsageBatch(ctx, userID, []store.UserUsageKey{{Kind: domain.UsageKindVoucher, TargetID: v.ID}})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read user voucher usage: %v", err)
	}
	userUsed = max(userUsed, pgUsage[store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: v.ID}])
	if remQuota <= 0 {
		return nil, status.Error(codes.ResourceExhausted, "voucher is out of quota")
	}
	if isVoucherBudgetExhausted(v, remBudget, 0) {
		return nil, status.Error(codes.ResourceExhausted, "voucher budget exhausted")
	}
	if v.PerUserLimit > 0 && userUsed >= v.PerUserLimit {
		return nil, status.Error(codes.FailedPrecondition, "per-user voucher limit already reached")
	}

	if _, err := s.store.SaveUserVoucher(ctx, userID, v.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "save voucher: %v", err)
	}

	return &promotionv1.SaveVoucherResponse{
		Saved:   true,
		Voucher: voucherToProto(v, remQuota, remBudget, userUsed, true, 0, now),
	}, nil
}

// CreateFlashSale creates a flash sale time window (requires flash_sales.write capability).
func (s *PromotionService) CreateFlashSale(ctx context.Context, req *promotionv1.CreateFlashSaleRequest) (*promotionv1.CreateFlashSaleResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasCapability(claims, auth.CapFlashSalesWrite) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions to create flash sales")
	}
	createdBy := claims.UserID()

	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if req.GetStartsAt() == nil || req.GetEndsAt() == nil {
		return nil, status.Error(codes.InvalidArgument, "starts_at and ends_at are required")
	}
	startsAt := req.GetStartsAt().AsTime().UTC()
	endsAt := req.GetEndsAt().AsTime().UTC()
	if !endsAt.After(startsAt) {
		return nil, status.Error(codes.InvalidArgument, "ends_at must be after starts_at")
	}
	if req.GetMinDiscountPercent() < 0 || req.GetMinDiscountPercent() > 99 {
		return nil, status.Error(codes.InvalidArgument, "min_discount_percent must be between 0 and 99")
	}

	fs, err := s.store.CreateFlashSale(ctx, domain.FlashSale{
		Name:               name,
		StartsAt:           startsAt,
		EndsAt:             endsAt,
		MinDiscountPercent: req.GetMinDiscountPercent(),
		CategoryIDs:        normalizeUniqueStrings(req.GetCategoryIds()),
		CreatedBy:          createdBy,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create flash sale: %v", err)
	}
	return &promotionv1.CreateFlashSaleResponse{FlashSale: flashSaleToProto(fs, nil, s.nowFn())}, nil
}

// RegisterFlashSaleItem enrolls a seller SKU into a flash sale after full SKU, price, category, stock & overlap checks.
func (s *PromotionService) RegisterFlashSaleItem(ctx context.Context, req *promotionv1.RegisterFlashSaleItemRequest) (*promotionv1.RegisterFlashSaleItemResponse, error) {
	shopID := strings.TrimSpace(req.GetShopId())
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case isSeller(claims):
		if claims.ShopID == "" {
			return nil, status.Error(codes.PermissionDenied, "seller account has no shop_id")
		}
		if shopID != "" && shopID != claims.ShopID {
			return nil, status.Error(codes.PermissionDenied, "sellers can only register items for their own shop")
		}
		shopID = claims.ShopID
		if err := s.requireShopNotSuspended(ctx, shopID); err != nil {
			return nil, err
		}
	case auth.HasCapability(claims, auth.CapFlashSalesWrite):
		// Authorized staff with flash_sales.write allowed when shop_id provided
	default:
		return nil, status.Error(codes.PermissionDenied, "only sellers can register flash sale items")
	}

	flashSaleID := strings.TrimSpace(req.GetFlashSaleId())
	skuID := strings.TrimSpace(req.GetSkuId())
	if flashSaleID == "" || shopID == "" || skuID == "" {
		return nil, status.Error(codes.InvalidArgument, "flash_sale_id, shop_id, and sku_id are required")
	}
	if req.GetFlashPriceVnd() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "flash_price_vnd must be > 0")
	}
	if req.GetTotalSlots() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "total_slots must be > 0")
	}
	perUserLimit := req.GetPerUserLimit()
	if perUserLimit == 0 {
		perUserLimit = 1
	}
	if perUserLimit < 0 {
		return nil, status.Error(codes.InvalidArgument, "per_user_limit must be > 0")
	}

	fs, err := s.store.GetFlashSale(ctx, flashSaleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "flash sale not found")
		}
		return nil, status.Errorf(codes.Internal, "get flash sale: %v", err)
	}
	now := s.nowFn()
	if fs.Status == domain.FlashWindowStatusEnded || !fs.EndsAt.After(now) {
		return nil, status.Error(codes.FailedPrecondition, "flash sale has already ended")
	}

	origPrice, err := s.validateFlashSaleSKU(ctx, fs, shopID, skuID, req.GetFlashPriceVnd(), req.GetTotalSlots(), false)
	if err != nil {
		return nil, err
	}

	item, err := s.store.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID:      flashSaleID,
		ShopID:           shopID,
		SKUID:            skuID,
		OriginalPriceVND: origPrice,
		FlashPriceVND:    req.GetFlashPriceVnd(),
		TotalSlots:       req.GetTotalSlots(),
		PerUserLimit:     perUserLimit,
	}, now)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.NotFound, "flash sale not found")
		case errors.Is(err, store.ErrFlashSaleEnded):
			return nil, status.Error(codes.FailedPrecondition, "flash sale has already ended")
		case errors.Is(err, store.ErrOverlappingFlashSale):
			return nil, status.Error(codes.AlreadyExists, "SKU is already registered in an overlapping active flash sale window")
		default:
			return nil, status.Errorf(codes.Internal, "register flash sale item: %v", err)
		}
	}

	return &promotionv1.RegisterFlashSaleItemResponse{Item: flashSaleItemToProto(item, item.RemainingSlots)}, nil
}

// ApproveFlashSaleItem approves or rejects a PENDING flash sale item.
// Strict state machine: only PENDING -> APPROVED|REJECTED is allowed.
// Repeating the same decision is idempotent (no stock/price re-check, no Redis overwrite);
// APPROVED <-> REJECTED transitions fail with FailedPrecondition.
func (s *PromotionService) ApproveFlashSaleItem(ctx context.Context, req *promotionv1.ApproveFlashSaleItemRequest) (*promotionv1.ApproveFlashSaleItemResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasCapability(claims, auth.CapFlashSalesItemApprove) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions to review flash sale items")
	}
	adminID := claims.UserID()

	itemID := strings.TrimSpace(req.GetItemId())
	if itemID == "" {
		return nil, status.Error(codes.InvalidArgument, "item_id is required")
	}

	targetStatus := domain.FlashStatusRejected
	if req.GetApprove() {
		targetStatus = domain.FlashStatusApproved
	}

	now := s.nowFn()
	existing, err := s.store.GetFlashSaleItem(ctx, itemID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "flash sale item not found")
		}
		return nil, status.Errorf(codes.Internal, "get flash sale item: %v", err)
	}
	if req.GetApprove() && (existing.WindowStatus == domain.FlashWindowStatusEnded || !existing.EndsAt.After(now)) {
		return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_ENDED: flash sale has already ended")
	}
	// Fast-path idempotency: skip SKU stock/price re-validation; for APPROVED, call InitFlashItemSlots
	// with overwrite=false (SetNX) so a prior post-commit Redis init failure self-heals without overwriting live slots.
	if existing.Status == targetStatus {
		remaining := existing.RemainingSlots
		if targetStatus == domain.FlashStatusApproved {
			if err := s.engine.InitFlashItemSlots(ctx, existing.ID, existing.RemainingSlots, false); err != nil {
				return nil, status.Errorf(codes.Internal, "initialize flash sale redis slots: %v", err)
			}
			if live, liveOK, getErr := s.engine.GetInt64(ctx, lua.FlashRemainingKey(existing.ID)); getErr == nil && liveOK {
				remaining = min(max(remaining, 0), max(int32(live), 0))
			}
		}
		return &promotionv1.ApproveFlashSaleItemResponse{Item: flashSaleItemToProto(existing, remaining)}, nil
	}
	if existing.Status != domain.FlashStatusPending {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot transition flash sale item from %s to %s", existing.Status, targetStatus)
	}

	var refreshedOrigPrice int64
	if req.GetApprove() {
		fs, err := s.store.GetFlashSale(ctx, existing.FlashSaleID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, status.Error(codes.NotFound, "flash sale not found")
			}
			return nil, status.Errorf(codes.Internal, "get flash sale: %v", err)
		}
		if fs.Status == domain.FlashWindowStatusEnded || !fs.EndsAt.After(now) {
			return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_ENDED: flash sale has already ended")
		}
		origPrice, vErr := s.validateFlashSaleSKU(ctx, fs, existing.ShopID, existing.SKUID, existing.FlashPriceVND, existing.TotalSlots, true)
		if vErr != nil {
			return nil, vErr
		}
		refreshedOrigPrice = origPrice
	}

	item, _, err := s.store.ApproveFlashSaleItem(ctx, itemID, adminID, req.GetApprove(), strings.TrimSpace(req.GetReason()), refreshedOrigPrice, now)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			return nil, status.Error(codes.NotFound, "flash sale item not found")
		case errors.Is(err, store.ErrFlashSaleEnded):
			return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_ENDED: flash sale has already ended")
		case errors.Is(err, store.ErrInvalidFlashTransition):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case errors.Is(err, store.ErrOverlappingFlashSale):
			return nil, status.Error(codes.AlreadyExists, "SKU overlaps with another registered flash sale window")
		default:
			return nil, status.Errorf(codes.Internal, "approve flash sale item: %v", err)
		}
	}

	remaining := item.RemainingSlots
	if req.GetApprove() {
		// overwrite=false (SetNX) heals any missing Redis key without overwriting live slot counters.
		if err := s.engine.InitFlashItemSlots(ctx, item.ID, item.RemainingSlots, false); err != nil {
			return nil, status.Errorf(codes.Internal, "initialize flash sale redis slots: %v", err)
		}
		if live, liveOK, getErr := s.engine.GetInt64(ctx, lua.FlashRemainingKey(item.ID)); getErr == nil && liveOK {
			remaining = min(max(remaining, 0), max(int32(live), 0))
		}
	} else {
		if err := s.engine.DeleteFlashItemSlots(ctx, item.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "delete rejected flash sale redis slots: %v", err)
		}
	}

	return &promotionv1.ApproveFlashSaleItemResponse{Item: flashSaleItemToProto(item, remaining)}, nil
}

// EndFlashSaleWindow immediately ends/cancels a flash sale window (requires flash_sales.write capability)
// and deletes Redis slot keys for its approved items idempotently.
func (s *PromotionService) EndFlashSaleWindow(ctx context.Context, req *promotionv1.EndFlashSaleWindowRequest) (*promotionv1.EndFlashSaleWindowResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if !auth.HasCapability(claims, auth.CapFlashSalesWrite) {
		return nil, status.Error(codes.PermissionDenied, "insufficient permissions to end flash sale windows")
	}

	windowID := strings.TrimSpace(req.GetWindowId())
	if windowID == "" {
		return nil, status.Error(codes.InvalidArgument, "window_id is required")
	}

	now := s.nowFn()
	fs, unreleased, err := s.store.EndFlashSaleWindow(ctx, windowID, now)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "flash sale window not found")
		}
		return nil, status.Errorf(codes.Internal, "end flash sale window: %v", err)
	}

	var releasedCount int32
	for _, it := range unreleased {
		if relErr := s.releaseFlashItemSlots(ctx, it.ID, now); relErr != nil {
			s.logger.WarnContext(ctx, "failed to clear flash sale redis slots while ending window",
				slog.String("window_id", windowID),
				slog.String("item_id", it.ID),
				slog.String("error", relErr.Error()),
			)
			continue
		}
		releasedCount++
	}

	return &promotionv1.EndFlashSaleWindowResponse{
		Window:        flashSaleToProto(fs, nil, now),
		ReleasedItems: releasedCount,
	}, nil
}

// SweepExpiredFlashSales marks expired flash sale windows ENDED and clears their Redis slot keys.
func (s *PromotionService) SweepExpiredFlashSales(ctx context.Context) (int, error) {
	now := s.nowFn()
	expired, err := s.store.ListExpiredUnreleasedFlashSaleItems(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list expired unreleased flash sale items: %w", err)
	}
	released := 0
	var firstErr error
	for _, it := range expired {
		if relErr := s.releaseFlashItemSlots(ctx, it.ID, now); relErr != nil {
			if firstErr == nil {
				firstErr = relErr
			}
			s.logger.WarnContext(ctx, "failed to sweep expired flash sale item",
				slog.String("flash_sale_id", it.FlashSaleID),
				slog.String("item_id", it.ID),
				slog.String("error", relErr.Error()),
			)
			continue
		}
		released++
	}
	return released, firstErr
}

// StartFlashSaleSweeper periodically marks expired flash sale windows ENDED and clears their Redis slots until ctx is canceled.
func (s *PromotionService) StartFlashSaleSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				n, err := s.SweepExpiredFlashSales(sweepCtx)
				cancel()
				if err != nil && ctx.Err() == nil {
					s.logger.WarnContext(sweepCtx, "flash sale expiry sweep error", slog.String("error", err.Error()))
				} else if n > 0 {
					s.logger.InfoContext(sweepCtx, "swept expired flash sale items", slog.Int("released_items", n))
				}
			}
		}
	}()
}

func (s *PromotionService) releaseFlashItemSlots(ctx context.Context, itemID string, now time.Time) error {
	if err := s.engine.DeleteFlashItemSlots(ctx, itemID); err != nil {
		return fmt.Errorf("delete flash sale redis slots: %w", err)
	}
	if err := s.store.MarkFlashSaleItemStockReleased(ctx, itemID, now); err != nil {
		return err
	}
	return nil
}

// ListActiveFlashSales returns active (and optionally upcoming) flash sales with live remaining slots from Redis.
func (s *PromotionService) ListActiveFlashSales(ctx context.Context, req *promotionv1.ListActiveFlashSalesRequest) (*promotionv1.ListActiveFlashSalesResponse, error) {
	now := s.nowFn()
	sales, err := s.store.ListFlashSales(ctx, now, req.GetIncludeUpcoming())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list flash sales: %v", err)
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

	resp := &promotionv1.ListActiveFlashSalesResponse{
		FlashSales: make([]*promotionv1.FlashSale, 0, len(sales)),
	}
	for i := range sales {
		resp.FlashSales = append(resp.FlashSales, flashSaleToProto(&sales[i], liveSlots, now))
	}
	return resp, nil
}

// GetCoinBalance returns the user's ledger balance (SUM(delta)) and recent coin transactions.
func (s *PromotionService) GetCoinBalance(ctx context.Context, req *promotionv1.GetCoinBalanceRequest) (*promotionv1.GetCoinBalanceResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	bal, txns, err := s.store.GetCoinBalance(ctx, userID, 50)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get coin balance: %v", err)
	}

	pbTxns := make([]*promotionv1.CoinTransaction, 0, len(txns))
	for _, t := range txns {
		pbTxns = append(pbTxns, &promotionv1.CoinTransaction{
			Id:          t.ID,
			UserId:      t.UserID,
			Delta:       t.Delta,
			Reason:      t.Reason,
			ReferenceId: t.ReferenceID,
			CreatedAt:   timestamppb.New(t.CreatedAt),
		})
	}
	return &promotionv1.GetCoinBalanceResponse{
		UserId:             userID,
		Balance:            bal,
		RecentTransactions: pbTxns,
	}, nil
}

// CreditCoins atomically and idempotently (by reference_id) credits coins to a user's ledger.
func (s *PromotionService) CreditCoins(ctx context.Context, req *promotionv1.CreditCoinsRequest) (*promotionv1.CreditCoinsResponse, error) {
	if _, err := requireCapabilityOrService(ctx, auth.CapCoinsCredit, "only authorized admins or internal services can credit coins"); err != nil {
		return nil, err
	}

	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		reason = "admin_credit"
	}
	refID := strings.TrimSpace(req.GetReferenceId())
	if refID != "" {
		if hasReservedExternalCoinRefPrefix(refID) {
			return nil, status.Error(codes.InvalidArgument, "reference_id must not use reserved internal prefix (reserve:, release:, credit:)")
		}
	} else {
		refID = fmt.Sprintf("credit:%s:%d", userID, s.nowFn().UnixNano())
	}

	bal, already, err := s.store.CreditCoins(ctx, userID, req.GetAmount(), reason, refID)
	if err != nil {
		if errors.Is(err, store.ErrReservedCoinReferenceID) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "credit coins: %v", err)
	}
	return &promotionv1.CreditCoinsResponse{
		UserId:          userID,
		Balance:         bal,
		AlreadyCredited: already,
	}, nil
}

func hasReservedExternalCoinRefPrefix(refID string) bool {
	lower := strings.ToLower(strings.TrimSpace(refID))
	return strings.HasPrefix(lower, "reserve:") ||
		strings.HasPrefix(lower, "release:") ||
		strings.HasPrefix(lower, "credit:")
}

// PreviewDiscounts runs pure deterministic integer-VND discount calculation without mutating state.
func (s *PromotionService) PreviewDiscounts(ctx context.Context, req *promotionv1.PreviewDiscountsRequest) (*promotionv1.PreviewDiscountsResponse, error) {
	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if req.GetCoinsToUse() < 0 {
		return nil, status.Error(codes.InvalidArgument, "coins_to_use must be non-negative")
	}

	channel, storefrontShopID, err := normalizeSalesChannel(req.GetSalesChannel(), req.GetStorefrontShopId())
	if err != nil {
		return nil, err
	}
	if channel == domain.SalesChannelStorefront {
		if err := s.validateStorefrontPromotion(
			ctx,
			storefrontShopID,
			req.GetShopItems(),
			req.GetShopVoucherIds(),
			req.GetPlatformVoucherId(),
			req.GetFreeshipVoucherId(),
			req.GetCoinsToUse(),
			nil,
		); err != nil {
			return nil, err
		}
	}

	calcInput, err := s.buildCalcInput(ctx, userID, req.GetShopItems(), req.GetShopVoucherIds(), req.GetPlatformVoucherId(), req.GetFreeshipVoucherId(), req.GetCoinsToUse(), false, nil)
	if err != nil {
		return nil, err
	}

	out, err := calculator.Calculate(calcInput)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "calculate discounts: %v", err)
	}
	return &promotionv1.PreviewDiscountsResponse{Breakdown: out.Breakdown}, nil
}

// ReservePromotion atomically reserves flash sale slots, voucher quotas/budgets, shop discount
// program per-user limits (via Redis Lua), and coins + per-user limits (via Postgres TX) idempotently by order_id.
// Restricted strictly to internal service tokens issued to order-service; compensates Redis on Postgres failure.
func (s *PromotionService) ReservePromotion(ctx context.Context, req *promotionv1.ReservePromotionRequest) (*promotionv1.ReservePromotionResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != orderServiceTokenName {
		return nil, status.Error(codes.PermissionDenied, "only order-service may reserve promotions")
	}

	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if err := validateKeySegment("order_id", orderID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	userID := strings.TrimSpace(req.GetUserId())
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if err := validateKeySegment("user_id", userID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.GetCoinsToUse() < 0 {
		return nil, status.Error(codes.InvalidArgument, "coins_to_use must be non-negative")
	}
	if len(req.GetShopItems()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "shop_items is required")
	}

	channel, storefrontShopID, err := normalizeSalesChannel(req.GetSalesChannel(), req.GetStorefrontShopId())
	if err != nil {
		return nil, err
	}
	if channel == domain.SalesChannelStorefront {
		if err := s.validateStorefrontPromotion(
			ctx,
			storefrontShopID,
			req.GetShopItems(),
			req.GetShopVoucherIds(),
			req.GetPlatformVoucherId(),
			req.GetFreeshipVoucherId(),
			req.GetCoinsToUse(),
			req.GetFlashSaleItems(),
		); err != nil {
			return nil, err
		}
	}

	// Fast idempotency check in Postgres
	if existing, err := s.store.GetReservation(ctx, orderID); err == nil {
		if existing.UserID != "" && existing.UserID != userID {
			return nil, status.Error(codes.PermissionDenied, "cannot access another user's promotion reservation")
		}
		if existing.Status == domain.ReservationStatusReleased {
			return nil, status.Error(codes.FailedPrecondition, "promotion reservation for this order was already released")
		}
		return &promotionv1.ReservePromotionResponse{
			Reserved:        true,
			AlreadyReserved: true,
			Breakdown:       unmarshalBreakdown(existing.BreakdownJSON),
		}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, status.Errorf(codes.Internal, "check existing reservation: %v", err)
	}

	now := s.nowFn()

	var (
		breakdown         *promotionv1.DiscountBreakdown
		flashSpecs        []lua.FlashReserveSpec
		reservedFlash     []domain.ReservedFlashItem
		voucherSpecs      []lua.VoucherReserveSpec
		reservedVouchers  []domain.ReservedVoucher
		shopProgSpecs     []lua.ShopProgramReserveSpec
		reservedShopProgs []domain.ReservedShopProgramItem
		coinsToDeduct     int64
		luaOutcome        lua.ReserveOutcome
	)

	// Flash sale quantities are derived EXCLUSIVELY from shop_items
	// (rejecting mismatched explicit flash_sale_items to prevent qty-mismatch oversell).
	flashSpecs, reservedFlash, err = s.resolveFlashFromShopItems(ctx, req.GetShopItems(), req.GetFlashSaleItems(), now)
	if err != nil {
		return nil, err
	}

	// Convergent retry loop on concurrent Lua SHOP_PROG_USER_LIMIT_EXCEEDED race:
	// cap attempts to distinct shop-program items + 1 (bounded by maxShopProgRaceCap)
	// so multi-shop checkouts converge cleanly to regular price without failing.
	const maxShopProgRaceCap = 20
	maxAttempts := countDistinctShopProgramItems(req.GetShopItems()) + 1
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	if maxAttempts > maxShopProgRaceCap {
		maxAttempts = maxShopProgRaceCap
	}

	rehydratedOnce := false
	exceededShopProgs := make(map[string]bool)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		calcInput, buildErr := s.buildCalcInput(ctx, userID, req.GetShopItems(), req.GetShopVoucherIds(), req.GetPlatformVoucherId(), req.GetFreeshipVoucherId(), req.GetCoinsToUse(), true, exceededShopProgs)
		if buildErr != nil {
			return nil, buildErr
		}
		out, calcErr := calculator.Calculate(calcInput)
		if calcErr != nil {
			return nil, mapCalcError(calcErr)
		}

		breakdown = out.Breakdown
		coinsToDeduct = out.CoinsUsed

		voucherSpecs = make([]lua.VoucherReserveSpec, 0, len(out.AppliedVouchers))
		reservedVouchers = make([]domain.ReservedVoucher, 0, len(out.AppliedVouchers))
		seenVoucher := make(map[string]bool, len(out.AppliedVouchers))
		for _, av := range out.AppliedVouchers {
			if seenVoucher[av.VoucherID] {
				return nil, status.Errorf(codes.InvalidArgument, "duplicate voucher %s in reservation", av.VoucherID)
			}
			seenVoucher[av.VoucherID] = true
			voucherSpecs = append(voucherSpecs, lua.VoucherReserveSpec{
				VoucherID:    av.VoucherID,
				PerUserLimit: av.PerUserLimit,
				DiscountVND:  av.DiscountVND,
				HasBudget:    av.HasBudget,
			})
			reservedVouchers = append(reservedVouchers, domain.ReservedVoucher{
				VoucherID:    av.VoucherID,
				Scope:        av.Scope,
				ShopID:       av.ShopID,
				DiscountVND:  av.DiscountVND,
				HasBudget:    av.HasBudget,
				RulesVersion: av.RulesVersion,
			})
		}

		shopProgSpecs = make([]lua.ShopProgramReserveSpec, 0, len(out.AppliedShopPrograms))
		reservedShopProgs = make([]domain.ReservedShopProgramItem, 0, len(out.AppliedShopPrograms))
		for _, ap := range out.AppliedShopPrograms {
			if kErr := validateKeySegment("shop_discount_program_item_id", ap.ItemID); kErr != nil {
				return nil, status.Error(codes.InvalidArgument, kErr.Error())
			}
			shopProgSpecs = append(shopProgSpecs, lua.ShopProgramReserveSpec{
				ItemID:       ap.ItemID,
				Quantity:     ap.Quantity,
				PerUserLimit: ap.PerUserLimit,
			})
			reservedShopProgs = append(reservedShopProgs, domain.ReservedShopProgramItem{
				ShopDiscountProgramItemID: ap.ItemID,
				SKUID:                     ap.SKUID,
				Quantity:                  ap.Quantity,
				ShopID:                    ap.ShopID,
			})
		}

		reserveIn := lua.ReserveInput{
			OrderID:          orderID,
			UserID:           userID,
			FlashItems:       flashSpecs,
			Vouchers:         voucherSpecs,
			ShopProgramItems: shopProgSpecs,
		}
		luaOutcome, err = s.engine.Reserve(ctx, reserveIn)
		if err != nil {
			var resErr *lua.ReservationError
			if !rehydratedOnce && errors.As(err, &resErr) && isNotInitializedLuaError(resErr.Code) {
				rehydratedOnce = true
				if recErr := s.reconcileTargets(ctx, false, userID, reservedVouchers, reservedFlash, reservedShopProgs); recErr != nil {
					return nil, status.Errorf(codes.Unavailable, "rehydrate promotion redis counters: %v", recErr)
				}
				luaOutcome, err = s.engine.Reserve(ctx, reserveIn)
			}
		}
		if err != nil {
			var resErr *lua.ReservationError
			if attempt+1 < maxAttempts && errors.As(err, &resErr) && resErr.Code == lua.ErrCodeShopProgUserLimitExceeded {
				if itemID := parseShopProgramItemIDFromUserKey(resErr.Key); itemID != "" {
					exceededShopProgs[itemID] = true
				}
				continue
			}
			return nil, mapLuaError(err)
		}

		if luaOutcome.AlreadyReserved {
			saved := s.waitForCommittedReservation(ctx, orderID)
			if saved != nil {
				if saved.UserID != "" && saved.UserID != userID {
					return nil, status.Error(codes.PermissionDenied, "cannot access another user's promotion reservation")
				}
				if saved.Status == domain.ReservationStatusReleased {
					return nil, status.Error(codes.FailedPrecondition, "RESERVATION_ALREADY_RELEASED: promotion reservation for this order was already released")
				}
				return &promotionv1.ReservePromotionResponse{
					Reserved:        true,
					AlreadyReserved: true,
					Breakdown:       unmarshalBreakdown(saved.BreakdownJSON),
				}, nil
			}
			return nil, status.Error(codes.Aborted, "concurrent promotion reservation did not commit")
		}

		breakdownBytes, marshalErr := protojson.Marshal(breakdown)
		if marshalErr != nil {
			s.compensateRedisRelease(ctx, orderID)
			s.reconcileTargetsDetached(true, userID, reservedVouchers, reservedFlash, reservedShopProgs)
			return nil, status.Errorf(codes.Internal, "marshal discount breakdown: %v", marshalErr)
		}

		existing, alreadyCommitted, commitErr := s.store.CommitReservation(ctx, domain.PromotionReservation{
			OrderID:          orderID,
			UserID:           userID,
			Status:           domain.ReservationStatusReserved,
			CoinsUsed:        coinsToDeduct,
			Vouchers:         reservedVouchers,
			FlashSaleItems:   reservedFlash,
			ShopProgramItems: reservedShopProgs,
			BreakdownJSON:    breakdownBytes,
		}, now)
		if commitErr != nil {
			// Concurrent 2-order race on shop discount program per_user_limit:
			// cleanly undo Redis WITHOUT writing a RELEASED tombstone for orderID, reconcile counters,
			// and loop back so the next attempt drops the exhausted shop program to regular price (contracts §4.10).
			if errors.Is(commitErr, store.ErrShopProgramUserLimitExceeded) && attempt+1 < maxAttempts {
				undoOut, undoErr := s.undoRedisReserve(orderID)
				if undoErr != nil {
					s.reconcileTargetsDetached(true, userID, reservedVouchers, reservedFlash, reservedShopProgs)
					return nil, status.Errorf(codes.Internal, "undo redis reservation before shop program fallback: %v", undoErr)
				}
				if undoOut.AlreadyReleased {
					if recErr := s.reconcileTargets(ctx, false, userID, reservedVouchers, reservedFlash, reservedShopProgs); recErr != nil {
						return nil, status.Errorf(codes.Unavailable, "reconcile redis reservation before shop program fallback: %v", recErr)
					}
				} else {
					s.reconcileTargetsDetached(true, userID, reservedVouchers, reservedFlash, reservedShopProgs)
				}
				continue
			}

			compensateAndHeal := func() {
				s.compensateRedisRelease(ctx, orderID)
				s.reconcileTargetsDetached(true, userID, reservedVouchers, reservedFlash, reservedShopProgs)
			}
			switch {
			case errors.Is(commitErr, store.ErrInsufficientCoins):
				compensateAndHeal()
				return nil, status.Error(codes.FailedPrecondition, "INSUFFICIENT_COINS: insufficient coin balance")
			case errors.Is(commitErr, store.ErrReservationAlreadyReleased):
				compensateAndHeal()
				return nil, status.Error(codes.FailedPrecondition, "RESERVATION_ALREADY_RELEASED: promotion reservation for this order was already released")
			case errors.Is(commitErr, store.ErrVoucherUserLimitExceeded):
				compensateAndHeal()
				return nil, status.Errorf(codes.FailedPrecondition, "VOUCHER_USER_LIMIT_EXCEEDED: %v", commitErr)
			case errors.Is(commitErr, store.ErrFlashUserLimitExceeded):
				compensateAndHeal()
				return nil, status.Errorf(codes.FailedPrecondition, "FLASH_USER_LIMIT_EXCEEDED: %v", commitErr)
			case errors.Is(commitErr, store.ErrShopProgramUserLimitExceeded):
				compensateAndHeal()
				return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PROG_USER_LIMIT_EXCEEDED: %v", commitErr)
			case errors.Is(commitErr, store.ErrVoucherNotEligibleForCommit):
				compensateAndHeal()
				return nil, status.Errorf(codes.FailedPrecondition, "VOUCHER_UNAVAILABLE_ON_COMMIT: %v", commitErr)
			case errors.Is(commitErr, store.ErrFlashNotEligibleForCommit):
				compensateAndHeal()
				return nil, status.Errorf(codes.FailedPrecondition, "FLASH_UNAVAILABLE_ON_COMMIT: %v", commitErr)
			case errors.Is(commitErr, store.ErrShopProgramNotEligibleForCommit):
				compensateAndHeal()
				return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PROGRAM_UNAVAILABLE_ON_COMMIT: %v", commitErr)
			case errors.Is(commitErr, store.ErrCoinLedgerConflict):
				compensateAndHeal()
				return nil, status.Errorf(codes.Internal, "persist promotion reservation: %v", commitErr)
			default:
				// Ambiguous commit failure (e.g. tx.Commit EOF/deadline exceeded): inspect Postgres via a detached
				// context before deciding whether to compensate Redis.
				checkCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				saved, checkErr := s.store.GetReservation(checkCtx, orderID)
				cancel()
				switch {
				case checkErr == nil:
					if saved.UserID != "" && saved.UserID != userID {
						_, _ = s.undoRedisReserve(orderID)
						s.reconcileTargetsDetached(true, userID, reservedVouchers, reservedFlash, reservedShopProgs)
						return nil, status.Error(codes.PermissionDenied, "cannot access another user's promotion reservation")
					}
					if saved.Status == domain.ReservationStatusReleased {
						compensateAndHeal()
						return nil, status.Error(codes.FailedPrecondition, "RESERVATION_ALREADY_RELEASED: promotion reservation for this order was already released")
					}
					s.reconcileTargetsDetached(false, userID, reservedVouchers, reservedFlash, reservedShopProgs)
					return &promotionv1.ReservePromotionResponse{
						Reserved:        true,
						AlreadyReserved: true,
						Breakdown:       unmarshalBreakdown(saved.BreakdownJSON),
					}, nil
				case errors.Is(checkErr, store.ErrNotFound):
					compensateAndHeal()
					return nil, status.Errorf(codes.Internal, "persist promotion reservation: %v", commitErr)
				default:
					// DB unreachable: leave promo:res intact and return Unavailable so order-service invokes
					// idempotent ReleasePromotion (releasing Redis via promo:res:<order_id>).
					return nil, status.Errorf(codes.Unavailable, "promotion reservation commit outcome uncertain: %v", commitErr)
				}
			}
		}

		if alreadyCommitted {
			if existing.UserID != "" && existing.UserID != userID {
				_, _ = s.undoRedisReserve(orderID)
				s.reconcileTargetsDetached(true, userID, reservedVouchers, reservedFlash, reservedShopProgs)
				return nil, status.Error(codes.PermissionDenied, "cannot access another user's promotion reservation")
			}
			s.reconcileTargetsDetached(false, userID, reservedVouchers, reservedFlash, reservedShopProgs)
			return &promotionv1.ReservePromotionResponse{
				Reserved:        true,
				AlreadyReserved: true,
				Breakdown:       unmarshalBreakdown(existing.BreakdownJSON),
			}, nil
		}

		return &promotionv1.ReservePromotionResponse{
			Reserved:        true,
			AlreadyReserved: false,
			Breakdown:       breakdown,
		}, nil
	}

	return nil, status.Error(codes.Aborted, "promotion reservation exhausted retry attempts")
}

func isNotInitializedLuaError(code lua.ReservationErrorCode) bool {
	return code == lua.ErrCodeVoucherNotInitialized ||
		code == lua.ErrCodeVoucherBudgetNotInit ||
		code == lua.ErrCodeFlashNotInitialized
}

func parseShopProgramItemIDFromUserKey(key string) string {
	const prefix = "promo:shop_prog:"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(key, prefix)
	idx := strings.Index(rest, ":user:")
	if idx <= 0 {
		return ""
	}
	return rest[:idx]
}

// ReleasePromotion idempotently compensates remaining Redis flash-sale slots, voucher quotas/budgets,
// shop discount program per-user counters, and Postgres coin deductions for an order.
func (s *PromotionService) ReleasePromotion(ctx context.Context, req *promotionv1.ReleasePromotionRequest) (*promotionv1.ReleasePromotionResponse, error) {
	if _, err := requireAdminOrService(ctx, "only admins or internal services can release promotions"); err != nil {
		return nil, err
	}

	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if err := validateKeySegment("order_id", orderID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	relRes, err := s.store.ReleaseReservation(ctx, orderID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "release postgres promotion reservation: %v", err)
	}

	extraKeys := collectReservationRedisKeys(relRes.UserID, relRes.FlashSaleItems, relRes.Vouchers, relRes.ShopPrograms)
	redisOutcome, err := s.engine.Release(ctx, orderID, extraKeys...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "release redis promotion reservation: %v", err)
	}
	if err := s.reconcileTargets(ctx, false, relRes.UserID, relRes.Vouchers, relRes.FlashSaleItems, relRes.ShopPrograms); err != nil {
		return nil, status.Errorf(codes.Internal, "reconcile released promotion in redis: %v", err)
	}

	return &promotionv1.ReleasePromotionResponse{
		Released:        true,
		AlreadyReleased: redisOutcome.AlreadyReleased && relRes.AlreadyReleased,
	}, nil
}

// ReleasePromotionShop idempotently releases one shop sub-order's share (coins, shop voucher,
// flash sale slots, shop discount program limits) from a checkout group's reservation
// and claws back any `order_completed_reward` coins previously credited for order_id.
func (s *PromotionService) ReleasePromotionShop(ctx context.Context, req *promotionv1.ReleasePromotionShopRequest) (*promotionv1.ReleasePromotionShopResponse, error) {
	if _, err := requireAdminOrService(ctx, "only admins or internal services can release shop promotions"); err != nil {
		return nil, err
	}

	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if err := validateKeySegment("order_id", orderID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	shopID := strings.TrimSpace(req.GetShopId())
	if shopID == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if err := validateKeySegment("shop_id", shopID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	subOrderID := strings.TrimSpace(req.GetSubOrderId())
	if subOrderID != "" {
		if err := validateKeySegment("sub_order_id", subOrderID); err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}

	res, err := s.store.ReleaseReservationShop(ctx, orderID, shopID, subOrderID, req.GetClawbackOnly())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "release shop promotion reservation: %v", err)
	}

	if res.AllReleased {
		extraKeys := collectReservationRedisKeys(res.UserID, res.AllFlash, res.AllVouchers, res.AllShopPrograms)
		if _, err := s.engine.Release(ctx, orderID, extraKeys...); err != nil {
			return nil, status.Errorf(codes.Internal, "release final redis promotion reservation: %v", err)
		}
		if err := s.reconcileTargets(ctx, false, res.UserID, res.AllVouchers, res.AllFlash, res.AllShopPrograms); err != nil {
			return nil, status.Errorf(codes.Internal, "reconcile final released promotion in redis: %v", err)
		}
	} else if len(res.TargetFlash) > 0 || len(res.TargetVouchers) > 0 || len(res.TargetShopPrograms) > 0 {
		targetKeys := make([]string, 0, len(res.TargetFlash)+len(res.TargetVouchers)+len(res.TargetShopPrograms))
		for _, f := range res.TargetFlash {
			targetKeys = append(targetKeys, lua.FlashRemainingKey(f.FlashSaleItemID))
		}
		for _, v := range res.TargetVouchers {
			targetKeys = append(targetKeys, lua.VoucherRemainingKey(v.VoucherID))
		}
		for _, p := range res.TargetShopPrograms {
			targetKeys = append(targetKeys, lua.ShopProgramUserKey(p.ShopDiscountProgramItemID, res.UserID))
		}
		extraKeys := collectReservationRedisKeys(res.UserID, res.TargetFlash, res.TargetVouchers, res.TargetShopPrograms)
		if _, err := s.engine.ReleasePartial(ctx, orderID, targetKeys, extraKeys...); err != nil {
			return nil, status.Errorf(codes.Internal, "release partial redis promotion reservation: %v", err)
		}
		if err := s.reconcileTargets(ctx, false, res.UserID, res.TargetVouchers, res.TargetFlash, res.TargetShopPrograms); err != nil {
			return nil, status.Errorf(codes.Internal, "reconcile partial released promotion in redis: %v", err)
		}
	}

	return &promotionv1.ReleasePromotionShopResponse{
		Released:                    true,
		AlreadyReleased:             res.AlreadyReleased,
		CoinsReturned:               res.CoinsReturned,
		ShopVoucherReleased:         res.ShopVoucherReleased,
		FlashSaleQuantityReleased:   res.FlashQuantityReleased,
		ShopProgramQuantityReleased: res.ShopProgramQuantityReleased,
		PlatformVoucherReleased:     res.PlatformVoucherReleased,
	}, nil
}

// reconcileTargets authoritatively converges Redis voucher, flash-item, and per-user usage keys
// from Postgres under row locks (FOR UPDATE, plus SKIP LOCKED when skipLocked is true).
// ponytail: accepted Scenario B — if an absolute SET runs between Lua Reserve and PG CommitReservation,
// Redis can momentarily sit higher than PG by the in-flight count; PG CommitReservation strictly blocks
// oversell and triggers reconcileTargetsBestEffort to self-heal Redis back to PG on the next rejection.
func (s *PromotionService) reconcileTargets(
	ctx context.Context,
	skipLocked bool,
	userID string,
	vouchers []domain.ReservedVoucher,
	flashItems []domain.ReservedFlashItem,
	shopProgs []domain.ReservedShopProgramItem,
) error {
	var firstErr error
	recordErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	seenV := make(map[string]struct{}, len(vouchers))
	for _, rv := range vouchers {
		if rv.VoucherID == "" {
			continue
		}
		if _, ok := seenV[rv.VoucherID]; ok {
			continue
		}
		seenV[rv.VoucherID] = struct{}{}
		_, err := s.syncVoucherRedis(ctx, rv.VoucherID, skipLocked)
		recordErr(err)
		if store.IsRedisConnOrTimeoutErr(err) {
			return firstErr
		}
	}

	seenF := make(map[string]struct{}, len(flashItems))
	for _, rf := range flashItems {
		if rf.FlashSaleItemID == "" {
			continue
		}
		if _, ok := seenF[rf.FlashSaleItemID]; ok {
			continue
		}
		seenF[rf.FlashSaleItemID] = struct{}{}
		_, err := s.syncFlashItemRedis(ctx, rf.FlashSaleItemID, skipLocked)
		recordErr(err)
		if store.IsRedisConnOrTimeoutErr(err) {
			return firstErr
		}
	}

	if userID != "" {
		seenU := make(map[store.UserUsageKey]struct{}, len(vouchers)+len(flashItems)+len(shopProgs))
		reconcileUser := func(kind, targetID string) bool {
			if targetID == "" {
				return false
			}
			k := store.UserUsageKey{Kind: kind, TargetID: targetID}
			if _, ok := seenU[k]; ok {
				return false
			}
			seenU[k] = struct{}{}
			err := s.syncUserUsageRedis(ctx, kind, targetID, userID, skipLocked)
			recordErr(err)
			return store.IsRedisConnOrTimeoutErr(err)
		}
		for _, rv := range vouchers {
			if reconcileUser(domain.UsageKindVoucher, rv.VoucherID) {
				return firstErr
			}
		}
		for _, rf := range flashItems {
			if reconcileUser(domain.UsageKindFlash, rf.FlashSaleItemID) {
				return firstErr
			}
		}
		for _, rp := range shopProgs {
			if reconcileUser(domain.UsageKindShopProgram, rp.ShopDiscountProgramItemID) {
				return firstErr
			}
		}
	}

	return firstErr
}

func (s *PromotionService) reconcileTargetsDetached(
	skipLocked bool,
	userID string,
	vouchers []domain.ReservedVoucher,
	flashItems []domain.ReservedFlashItem,
	shopProgs []domain.ReservedShopProgramItem,
) {
	if len(vouchers) == 0 && len(flashItems) == 0 && len(shopProgs) == 0 {
		return
	}
	healCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.reconcileTargets(healCtx, skipLocked, userID, vouchers, flashItems, shopProgs)
}

func (s *PromotionService) undoRedisReserve(orderID string) (lua.ReleaseOutcome, error) {
	undoCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.engine.UndoReserve(undoCtx, orderID)
}

func collectReservationRedisKeys(
	userID string,
	flashItems []domain.ReservedFlashItem,
	vouchers []domain.ReservedVoucher,
	shopProgs []domain.ReservedShopProgramItem,
) []string {
	keys := make([]string, 0, len(flashItems)*2+len(vouchers)*3+len(shopProgs))
	for _, f := range flashItems {
		keys = append(keys, lua.FlashRemainingKey(f.FlashSaleItemID), lua.FlashUserKey(f.FlashSaleItemID, userID))
	}
	for _, v := range vouchers {
		keys = append(keys,
			lua.VoucherRemainingKey(v.VoucherID),
			lua.VoucherUserKey(v.VoucherID, userID),
			lua.VoucherBudgetRemainingKey(v.VoucherID),
		)
	}
	for _, p := range shopProgs {
		keys = append(keys, lua.ShopProgramUserKey(p.ShopDiscountProgramItemID, userID))
	}
	return keys
}

func (s *PromotionService) compensateRedisRelease(ctx context.Context, orderID string) {
	compCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := s.engine.Release(compCtx, orderID); err != nil {
		s.logger.ErrorContext(compCtx, "failed to compensate redis promotion reservation after postgres failure",
			slog.String("order_id", orderID),
			slog.String("error", err.Error()),
		)
	}
}

func (s *PromotionService) waitForCommittedReservation(ctx context.Context, orderID string) *domain.PromotionReservation {
	for i := 0; i < 25; i++ {
		if saved, err := s.store.GetReservation(ctx, orderID); err == nil {
			return saved
		}
		if s.engine.IsReservationReleased(ctx, orderID) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(15 * time.Millisecond):
		}
	}
	return nil
}

type mergedFlashReq struct {
	itemID string
	skuID  string
	shopID string
	qty    int32
}

func (s *PromotionService) resolveFlashFromShopItems(
	ctx context.Context,
	shopItems []*promotionv1.ShopDiscountInput,
	explicit []*promotionv1.FlashSaleReservationItem,
	now time.Time,
) ([]lua.FlashReserveSpec, []domain.ReservedFlashItem, error) {
	merged := make(map[string]*mergedFlashReq)
	order := make([]string, 0)

	for _, shop := range shopItems {
		shopID := strings.TrimSpace(shop.GetShopId())
		for _, line := range shop.GetItems() {
			id := strings.TrimSpace(line.GetFlashSaleItemId())
			if id == "" {
				continue
			}
			if err := addMergedFlash(merged, &order, id, line.GetSkuId(), shopID, line.GetQuantity()); err != nil {
				return nil, nil, err
			}
		}
	}

	// If explicit flash_sale_items were also provided alongside shop_items, verify exact parity
	// so callers cannot claim a smaller quantity in flash_sale_items than in shop_items.
	if len(explicit) > 0 {
		explicitQty := make(map[string]int32, len(explicit))
		for _, f := range explicit {
			id := strings.TrimSpace(f.GetFlashSaleItemId())
			if id == "" {
				return nil, nil, status.Error(codes.InvalidArgument, "flash_sale_item_id is required")
			}
			if f.GetQuantity() <= 0 {
				return nil, nil, status.Errorf(codes.InvalidArgument, "flash sale item %s quantity must be > 0", id)
			}
			explicitQty[id] += f.GetQuantity()
		}
		if len(explicitQty) != len(merged) {
			return nil, nil, status.Error(codes.InvalidArgument, "flash_sale_items does not match flash items in shop_items")
		}
		for id, qty := range explicitQty {
			mf, ok := merged[id]
			if !ok || mf.qty != qty {
				return nil, nil, status.Errorf(codes.InvalidArgument, "flash_sale_items quantity for %s (%d) does not match shop_items", id, qty)
			}
		}
	}

	return s.validateMergedFlashReservations(ctx, merged, order, now)
}

func addMergedFlash(merged map[string]*mergedFlashReq, order *[]string, itemID, skuID, shopID string, qty int32) error {
	if err := validateKeySegment("flash_sale_item_id", itemID); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if qty <= 0 {
		return status.Errorf(codes.InvalidArgument, "flash sale item %s quantity must be > 0", itemID)
	}
	skuID = strings.TrimSpace(skuID)
	shopID = strings.TrimSpace(shopID)
	if cur, ok := merged[itemID]; ok {
		cur.qty += qty
		if cur.skuID == "" {
			cur.skuID = skuID
		}
		if cur.shopID == "" {
			cur.shopID = shopID
		}
		return nil
	}
	merged[itemID] = &mergedFlashReq{itemID: itemID, skuID: skuID, shopID: shopID, qty: qty}
	*order = append(*order, itemID)
	return nil
}

func (s *PromotionService) validateMergedFlashReservations(
	ctx context.Context,
	merged map[string]*mergedFlashReq,
	order []string,
	now time.Time,
) ([]lua.FlashReserveSpec, []domain.ReservedFlashItem, error) {
	sort.Strings(order)
	specs := make([]lua.FlashReserveSpec, 0, len(order))
	reserved := make([]domain.ReservedFlashItem, 0, len(order))

	for _, id := range order {
		mf := merged[id]
		fi, err := s.store.GetFlashSaleItem(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, nil, status.Errorf(codes.NotFound, "flash sale item %s not found", id)
			}
			return nil, nil, status.Errorf(codes.Internal, "get flash sale item: %v", err)
		}
		if fi.Status != domain.FlashStatusApproved {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "flash sale item %s is not approved", id)
		}
		// Strictly half-open [StartsAt, EndsAt) and not ENDED: upcoming and ended slots are rejected.
		if fi.WindowStatus == domain.FlashWindowStatusEnded || now.Before(fi.StartsAt) || !now.Before(fi.EndsAt) {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "flash sale item %s is outside active window", id)
		}
		if mf.skuID != "" && fi.SKUID != "" && mf.skuID != fi.SKUID {
			return nil, nil, status.Errorf(codes.InvalidArgument, "flash sale item %s does not match SKU %s", id, mf.skuID)
		}
		if mf.shopID != "" && fi.ShopID != "" && mf.shopID != fi.ShopID {
			return nil, nil, status.Errorf(codes.InvalidArgument, "flash sale item %s does not belong to shop %s", id, mf.shopID)
		}

		shopID := fi.ShopID
		if shopID == "" {
			shopID = mf.shopID
		}

		specs = append(specs, lua.FlashReserveSpec{
			ItemID:       fi.ID,
			Quantity:     mf.qty,
			PerUserLimit: fi.PerUserLimit,
		})
		reserved = append(reserved, domain.ReservedFlashItem{
			FlashSaleItemID: fi.ID,
			SKUID:           fi.SKUID,
			Quantity:        mf.qty,
			ShopID:          shopID,
		})
	}
	return specs, reserved, nil
}

func countDistinctShopProgramItems(shopItems []*promotionv1.ShopDiscountInput) int {
	seen := make(map[string]struct{})
	for _, shop := range shopItems {
		for _, line := range shop.GetItems() {
			if id := strings.TrimSpace(line.GetShopDiscountProgramItemId()); id != "" {
				seen[id] = struct{}{}
			}
		}
	}
	return len(seen)
}

func (s *PromotionService) buildCalcInput(
	ctx context.Context,
	userID string,
	pbShops []*promotionv1.ShopDiscountInput,
	shopVoucherIDs map[string]string,
	platformVoucherID string,
	freeshipVoucherID string,
	coinsToUse int64,
	strict bool,
	exceededShopProgs map[string]bool,
) (calculator.Input, error) {
	now := s.nowFn()

	var platformCand *calculator.VoucherCandidate
	if vid := strings.TrimSpace(platformVoucherID); vid != "" {
		var err error
		platformCand, err = s.loadVoucherCandidate(ctx, vid, userID)
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "load platform voucher %s: %v", vid, err)
		}
		if platformCand.Found && platformCand.Voucher != nil && len(platformCand.Voucher.CategoryIDs) > 0 {
			allowed, catErr := s.resolveAllowedCategories(ctx, platformCand.Voucher.CategoryIDs)
			if catErr != nil {
				return calculator.Input{}, catErr
			}
			platformCand.AllowedCategoryIDs = allowed
		}
	}

	// Collect SKU IDs:
	// - In Preview mode (!strict): ALWAYS resolve authoritative price_vnd, shop_id, category_id, and
	//   is_active from ProductService (never trusting client unit_price_vnd, category_id, or subtotal_vnd).
	// - In Reserve mode (strict, order-service only): resolve category_id from ProductService when a
	//   category-scoped platform voucher is applied.
	needCategorySKUs := platformCand != nil && platformCand.Found && platformCand.Voucher != nil && len(platformCand.Voucher.CategoryIDs) > 0
	allSKUs := make([]string, 0)
	autoProgSKUs := make([]string, 0)
	for _, ps := range pbShops {
		for _, pi := range ps.GetItems() {
			skuID := strings.TrimSpace(pi.GetSkuId())
			if !strict {
				if skuID == "" {
					return calculator.Input{}, status.Error(codes.InvalidArgument, "sku_id is required")
				}
				if pi.GetQuantity() <= 0 {
					return calculator.Input{}, status.Errorf(codes.InvalidArgument, "sku %s quantity must be > 0", skuID)
				}
				allSKUs = append(allSKUs, skuID)
				if strings.TrimSpace(pi.GetShopDiscountProgramItemId()) == "" {
					autoProgSKUs = append(autoProgSKUs, skuID)
				}
			} else if skuID != "" && needCategorySKUs {
				allSKUs = append(allSKUs, skuID)
			}
		}
	}

	var skuMeta map[string]*productv1.SKU
	if !strict && len(allSKUs) > 0 {
		if s.productClient == nil {
			return calculator.Input{}, status.Error(codes.Unavailable, "product service is unavailable to resolve SKU pricing")
		}
		var err error
		skuMeta, err = s.productClient.GetSKUsBatch(ctx, normalizeUniqueStrings(allSKUs))
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Unavailable, "resolve SKUs from product service: %v", err)
		}
	} else if strict && needCategorySKUs && len(allSKUs) > 0 && s.productClient != nil {
		var err error
		skuMeta, err = s.productClient.GetSKUsBatch(ctx, normalizeUniqueStrings(allSKUs))
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Unavailable, "resolve SKU categories from product service: %v", err)
		}
	}

	activeShopProgByShopSKU := make(map[string]domain.ShopDiscountProgramItem)
	if !strict && len(autoProgSKUs) > 0 {
		activeProgs, err := s.store.ListActiveShopDiscounts(ctx, normalizeUniqueStrings(autoProgSKUs), "", now)
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "load active shop discounts for preview: %v", err)
		}
		for _, it := range activeProgs {
			activeShopProgByShopSKU[it.ShopID+"|"+it.SKUID] = it
		}
	}

	shops := make([]calculator.ShopInput, 0, len(pbShops))
	flashCandidates := make(map[string]calculator.FlashItemCandidate)
	shopProgCandidates := make(map[string]calculator.ShopProgramItemCandidate)

	for _, ps := range pbShops {
		shopID := strings.TrimSpace(ps.GetShopId())
		subtotalVND := ps.GetSubtotalVnd()
		if !strict {
			// Ignore client-supplied subtotal_vnd; recompute strictly from authoritative ProductService SKU prices.
			subtotalVND = 0
		}
		si := calculator.ShopInput{
			ShopID:              shopID,
			SubtotalVND:         subtotalVND,
			ShippingFeeVND:      ps.GetShippingFeeVnd(),
			FreeShipEnabled:     ps.GetFreeShipEnabled(),
			FreeShipMinOrderVND: ps.GetFreeShipMinOrderVnd(),
			FreeShipMaxVND:      ps.GetFreeShipMaxVnd(),
			Items:               make([]calculator.LineItemInput, 0, len(ps.GetItems())),
		}
		for _, pi := range ps.GetItems() {
			skuID := strings.TrimSpace(pi.GetSkuId())
			fiID := strings.TrimSpace(pi.GetFlashSaleItemId())
			spID := strings.TrimSpace(pi.GetShopDiscountProgramItemId())

			unitPriceVND := pi.GetUnitPriceVnd()
			// Never trust client-supplied category_id; resolve strictly from ProductService.
			catID := ""
			if !strict {
				sk, ok := skuMeta[skuID]
				if !ok || sk == nil {
					return calculator.Input{}, status.Errorf(codes.NotFound, "sku %s not found", skuID)
				}
				if !sk.GetIsActive() {
					return calculator.Input{}, status.Errorf(codes.FailedPrecondition, "SKU_INACTIVE: sku %s is not active", skuID)
				}
				authShopID := strings.TrimSpace(sk.GetShopId())
				if authShopID == "" {
					return calculator.Input{}, status.Errorf(codes.InvalidArgument, "sku %s has no associated shop", skuID)
				}
				if shopID != "" && authShopID != shopID {
					return calculator.Input{}, status.Errorf(codes.InvalidArgument, "sku %s does not belong to shop %s", skuID, shopID)
				}
				if shopID == "" {
					shopID = authShopID
					si.ShopID = authShopID
				}
				unitPriceVND = sk.GetPriceVnd()
				if unitPriceVND <= 0 {
					return calculator.Input{}, status.Errorf(codes.FailedPrecondition, "sku %s has invalid price", skuID)
				}
				qty := int64(pi.GetQuantity())
				if qty > math.MaxInt64/unitPriceVND {
					return calculator.Input{}, status.Errorf(codes.InvalidArgument, "line subtotal overflow for sku %s", skuID)
				}
				lineTotal := unitPriceVND * qty
				if si.SubtotalVND > math.MaxInt64-lineTotal {
					return calculator.Input{}, status.Error(codes.InvalidArgument, "shop subtotal overflow")
				}
				si.SubtotalVND += lineTotal
				catID = strings.TrimSpace(sk.GetCategoryId())
			} else if skuMeta != nil && skuID != "" {
				if sk, ok := skuMeta[skuID]; ok {
					catID = strings.TrimSpace(sk.GetCategoryId())
				}
			}

			// In non-strict preview mode, automatically look up active shop discount programs for every SKU
			// (including flash lines) so lines fall back to shop program price when flash cannot apply.
			autoShopProg := false
			if !strict && spID == "" && skuID != "" {
				if autoItem, ok := activeShopProgByShopSKU[shopID+"|"+skuID]; ok {
					spID = autoItem.ID
					autoShopProg = true
					cp := autoItem
					shopProgCandidates[spID] = calculator.ShopProgramItemCandidate{Item: &cp, Found: true}
				}
			}

			si.Items = append(si.Items, calculator.LineItemInput{
				SKUID:                     skuID,
				Quantity:                  pi.GetQuantity(),
				UnitPriceVND:              unitPriceVND,
				FlashSaleItemID:           fiID,
				CategoryID:                catID,
				ShopDiscountProgramItemID: spID,
				AutoShopProgram:           autoShopProg,
			})

			if fiID != "" {
				if _, loaded := flashCandidates[fiID]; !loaded {
					fi, err := s.store.GetFlashSaleItem(ctx, fiID)
					if err != nil {
						if errors.Is(err, store.ErrNotFound) {
							flashCandidates[fiID] = calculator.FlashItemCandidate{Found: false}
						} else {
							return calculator.Input{}, status.Errorf(codes.Internal, "load flash sale item %s: %v", fiID, err)
						}
					} else {
						flashCandidates[fiID] = calculator.FlashItemCandidate{Item: fi, Found: true}
					}
				}
			}

			if spID != "" {
				if _, loaded := shopProgCandidates[spID]; !loaded {
					spi, err := s.store.GetShopDiscountProgramItem(ctx, spID)
					if err != nil {
						if errors.Is(err, store.ErrNotFound) {
							shopProgCandidates[spID] = calculator.ShopProgramItemCandidate{Found: false}
						} else {
							return calculator.Input{}, status.Errorf(codes.Internal, "load shop discount program item %s: %v", spID, err)
						}
					} else {
						shopProgCandidates[spID] = calculator.ShopProgramItemCandidate{Item: spi, Found: true}
					}
				}
			}
		}
		shops = append(shops, si)
	}

	// Hydrate live per-user counters and (in Preview mode) live Redis flash remaining slots.
	if len(flashCandidates) > 0 || (userID != "" && len(shopProgCandidates) > 0) {
		redisKeys := make([]string, 0, len(flashCandidates)*2+len(shopProgCandidates))
		for id, cand := range flashCandidates {
			if !cand.Found {
				continue
			}
			if userID != "" {
				redisKeys = append(redisKeys, lua.FlashUserKey(id, userID))
			}
			if !strict {
				redisKeys = append(redisKeys, lua.FlashRemainingKey(id))
			}
		}
		if userID != "" {
			for id, cand := range shopProgCandidates {
				if cand.Found {
					redisKeys = append(redisKeys, lua.ShopProgramUserKey(id, userID))
				}
			}
		}
		if len(redisKeys) > 0 {
			vals, err := s.engine.MGetInt64(ctx, redisKeys)
			if err != nil {
				return calculator.Input{}, status.Errorf(codes.Internal, "read live item counters: %v", err)
			}
			for id, cand := range flashCandidates {
				if !cand.Found {
					continue
				}
				if userID != "" {
					if v, ok := vals[lua.FlashUserKey(id, userID)]; ok {
						cand.UserUsedCount = int32(v)
					}
				}
				if !strict {
					cand.CheckRemaining = true
					var pgRem int32
					if cand.Item != nil {
						pgRem = min(max(cand.Item.RemainingSlots, 0), cand.Item.TotalSlots)
					}
					if v, ok := vals[lua.FlashRemainingKey(id)]; ok {
						cand.RemainingSlots = min(pgRem, max(int32(v), 0))
					} else {
						cand.RemainingSlots = pgRem
					}
				}
				flashCandidates[id] = cand
			}
			if userID != "" {
				for id, cand := range shopProgCandidates {
					if v, ok := vals[lua.ShopProgramUserKey(id, userID)]; ok {
						cand.UserUsedCount = int32(v)
						shopProgCandidates[id] = cand
					}
				}
			}
		}
	}

	shopVoucherCands := make(map[string]*calculator.VoucherCandidate, len(shopVoucherIDs))
	for shopID, rawVID := range shopVoucherIDs {
		vid := strings.TrimSpace(rawVID)
		if vid == "" {
			continue
		}
		cand, err := s.loadVoucherCandidate(ctx, vid, userID)
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "load shop voucher %s: %v", vid, err)
		}
		shopVoucherCands[shopID] = cand
	}

	var freeshipCand *calculator.VoucherCandidate
	if vid := strings.TrimSpace(freeshipVoucherID); vid != "" {
		var err error
		freeshipCand, err = s.loadVoucherCandidate(ctx, vid, userID)
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "load freeship voucher %s: %v", vid, err)
		}
	}

	// Merge authoritative Postgres per-user usage in ONE batch query: max(PG, Redis) across
	// vouchers, flash items, and shop discount programs so lost Redis :user: keys never drift Preview/Checkout.
	if userID != "" {
		usageTargets := make([]store.UserUsageKey, 0, len(flashCandidates)+len(shopProgCandidates)+len(shopVoucherCands)+2)
		for id, cand := range flashCandidates {
			if cand.Found {
				usageTargets = append(usageTargets, store.UserUsageKey{Kind: domain.UsageKindFlash, TargetID: id})
			}
		}
		for id, cand := range shopProgCandidates {
			if cand.Found {
				usageTargets = append(usageTargets, store.UserUsageKey{Kind: domain.UsageKindShopProgram, TargetID: id})
			}
		}
		for _, cand := range shopVoucherCands {
			if cand != nil && cand.Found && cand.Voucher != nil {
				usageTargets = append(usageTargets, store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: cand.Voucher.ID})
			}
		}
		if platformCand != nil && platformCand.Found && platformCand.Voucher != nil {
			usageTargets = append(usageTargets, store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: platformCand.Voucher.ID})
		}
		if freeshipCand != nil && freeshipCand.Found && freeshipCand.Voucher != nil {
			usageTargets = append(usageTargets, store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: freeshipCand.Voucher.ID})
		}
		if len(usageTargets) > 0 {
			pgUsage, err := s.store.GetUserUsageBatch(ctx, userID, usageTargets)
			if err != nil {
				return calculator.Input{}, status.Errorf(codes.Internal, "load per-user promotion usage: %v", err)
			}
			for id, cand := range flashCandidates {
				if cand.Found && cand.Item != nil {
					cand.UserUsedCount = max(cand.UserUsedCount, pgUsage[store.UserUsageKey{Kind: domain.UsageKindFlash, TargetID: id}])
					flashCandidates[id] = cand
				}
			}
			for id, cand := range shopProgCandidates {
				if cand.Found && cand.Item != nil {
					cand.UserUsedCount = max(cand.UserUsedCount, pgUsage[store.UserUsageKey{Kind: domain.UsageKindShopProgram, TargetID: id}])
					if exceededShopProgs[id] && cand.Item.PerUserLimit > 0 {
						cand.UserUsedCount = max(cand.UserUsedCount, cand.Item.PerUserLimit)
					}
					shopProgCandidates[id] = cand
				}
			}
			for _, cand := range shopVoucherCands {
				if cand != nil && cand.Found && cand.Voucher != nil {
					cand.UserUsedCount = max(cand.UserUsedCount, pgUsage[store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: cand.Voucher.ID}])
				}
			}
			if platformCand != nil && platformCand.Found && platformCand.Voucher != nil {
				platformCand.UserUsedCount = max(platformCand.UserUsedCount, pgUsage[store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: platformCand.Voucher.ID}])
			}
			if freeshipCand != nil && freeshipCand.Found && freeshipCand.Voucher != nil {
				freeshipCand.UserUsedCount = max(freeshipCand.UserUsedCount, pgUsage[store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: freeshipCand.Voucher.ID}])
			}
		}
	}

	// Mark promotions belonging to suspended shops so neither PreviewDiscounts nor ReservePromotion
	// applies shop vouchers, flash sale items, or shop discount programs of a suspended shop (F12).
	promoShopIDs := make([]string, 0, len(shopVoucherCands)+len(flashCandidates)+len(shopProgCandidates))
	for _, cand := range shopVoucherCands {
		if cand != nil && cand.Found && cand.Voucher != nil && cand.Voucher.Scope == domain.VoucherScopeShop && cand.Voucher.ShopID != "" {
			promoShopIDs = append(promoShopIDs, cand.Voucher.ShopID)
		}
	}
	for _, cand := range flashCandidates {
		if cand.Found && cand.Item != nil && cand.Item.ShopID != "" {
			promoShopIDs = append(promoShopIDs, cand.Item.ShopID)
		}
	}
	for _, cand := range shopProgCandidates {
		if cand.Found && cand.Item != nil && cand.Item.ShopID != "" {
			promoShopIDs = append(promoShopIDs, cand.Item.ShopID)
		}
	}
	if len(promoShopIDs) > 0 {
		suspendedShops, suspErr := s.store.GetSuspendedShopIDs(ctx, promoShopIDs)
		if suspErr != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "check suspended shops: %v", suspErr)
		}
		if len(suspendedShops) > 0 {
			for _, cand := range shopVoucherCands {
				if cand != nil && cand.Found && cand.Voucher != nil && cand.Voucher.Scope == domain.VoucherScopeShop && suspendedShops[cand.Voucher.ShopID] {
					cand.ShopSuspended = true
				}
			}
			for id, cand := range flashCandidates {
				if cand.Found && cand.Item != nil && suspendedShops[cand.Item.ShopID] {
					cand.ShopSuspended = true
					flashCandidates[id] = cand
				}
			}
			for id, cand := range shopProgCandidates {
				if cand.Found && cand.Item != nil && suspendedShops[cand.Item.ShopID] {
					cand.ShopSuspended = true
					shopProgCandidates[id] = cand
				}
			}
		}
	}

	var availCoins int64
	if coinsToUse > 0 && userID != "" {
		bal, _, err := s.store.GetCoinBalance(ctx, userID, 1)
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "load coin balance: %v", err)
		}
		availCoins = bal
	}

	return calculator.Input{
		Now:              now,
		Strict:           strict,
		Shops:            shops,
		FlashItems:       flashCandidates,
		ShopProgramItems: shopProgCandidates,
		ShopVouchers:     shopVoucherCands,
		PlatformVoucher:  platformCand,
		FreeshipVoucher:  freeshipCand,
		CoinsToUse:       coinsToUse,
		AvailableCoins:   availCoins,
	}, nil
}

func (s *PromotionService) loadVoucherCandidate(ctx context.Context, idOrCode, userID string) (*calculator.VoucherCandidate, error) {
	v, err := s.store.GetVoucherByIDOrCode(ctx, idOrCode)
	if errors.Is(err, store.ErrNotFound) {
		return &calculator.VoucherCandidate{RequestedID: idOrCode, Found: false}, nil
	}
	if err != nil {
		return nil, err
	}
	remQuota, remBudget, userUsed, err := s.readVoucherLiveCounters(ctx, v, userID)
	if err != nil {
		return nil, err
	}
	return &calculator.VoucherCandidate{
		RequestedID:     idOrCode,
		Voucher:         v,
		Found:           true,
		RemainingQuota:  remQuota,
		RemainingBudget: remBudget,
		UserUsedCount:   userUsed,
	}, nil
}

func (s *PromotionService) readVoucherLiveCounters(ctx context.Context, v *domain.Voucher, userID string) (int32, int64, int32, error) {
	keys := []string{lua.VoucherRemainingKey(v.ID)}
	if v.BudgetVND > 0 {
		keys = append(keys, lua.VoucherBudgetRemainingKey(v.ID))
	}
	if userID != "" {
		keys = append(keys, lua.VoucherUserKey(v.ID, userID))
	}
	vals, err := s.engine.MGetInt64(ctx, keys)
	if err != nil {
		return 0, 0, 0, err
	}

	remQuota := max(v.Quota-v.UsedCount, 0)
	if live, ok := vals[lua.VoucherRemainingKey(v.ID)]; ok {
		remQuota = min(remQuota, max(int32(live), 0))
	}

	remBudget := max(v.BudgetVND-v.UsedBudgetVND, 0)
	if v.BudgetVND > 0 {
		if live, ok := vals[lua.VoucherBudgetRemainingKey(v.ID)]; ok {
			remBudget = min(remBudget, max(live, 0))
		}
	}

	var userUsed int32
	if userID != "" {
		if live, ok := vals[lua.VoucherUserKey(v.ID, userID)]; ok {
			userUsed = int32(live)
		}
	}
	return remQuota, remBudget, userUsed, nil
}

func voucherToProto(
	v *domain.Voucher,
	remQuota int32,
	remBudget int64,
	userUsed int32,
	saved bool,
	orderAmountVND int64,
	now time.Time,
) *promotionv1.Voucher {
	usedCount := v.Quota - remQuota
	if usedCount < 0 {
		usedCount = 0
	}
	var usedBudget int64
	if v.BudgetVND > 0 {
		usedBudget = v.BudgetVND - remBudget
		if usedBudget < 0 {
			usedBudget = 0
		}
	}

	st := v.EffectiveStatus()
	isActive := st == domain.VoucherStatusActive && v.IsActive

	eligible := true
	ineligibleReason := ""
	switch {
	case st == domain.VoucherStatusDeleted:
		eligible = false
		ineligibleReason = "voucher has been deleted"
	case st == domain.VoucherStatusPaused:
		eligible = false
		ineligibleReason = "voucher is paused"
	case !isActive:
		eligible = false
		ineligibleReason = "voucher is inactive"
	case now.Before(v.StartsAt):
		eligible = false
		ineligibleReason = "voucher has not started yet"
	case now.After(v.EndsAt):
		eligible = false
		ineligibleReason = "voucher has expired"
	case remQuota <= 0:
		eligible = false
		ineligibleReason = "voucher is out of quota"
	case isVoucherBudgetExhausted(v, remBudget, orderAmountVND):
		eligible = false
		ineligibleReason = "voucher budget exhausted"
	case userUsed > 0 && v.PerUserLimit > 0 && userUsed >= v.PerUserLimit:
		eligible = false
		ineligibleReason = "per-user usage limit reached"
	case orderAmountVND > 0 && orderAmountVND < v.MinOrderVND:
		eligible = false
		ineligibleReason = fmt.Sprintf("minimum order %d VND not met (shortfall %d VND)", v.MinOrderVND, v.MinOrderVND-orderAmountVND)
	}

	return &promotionv1.Voucher{
		Id:               v.ID,
		Code:             v.Code,
		Name:             v.Name,
		Scope:            domainScopeToProto(v.Scope),
		ShopId:           v.ShopID,
		DiscountType:     domainDiscountTypeToProto(v.DiscountType),
		ValueVnd:         v.ValueVND,
		DiscountPercent:  v.DiscountPercent,
		MaxDiscountVnd:   v.MaxDiscountVND,
		MinOrderVnd:      v.MinOrderVND,
		Quota:            v.Quota,
		UsedCount:        usedCount,
		RemainingQuota:   remQuota,
		PerUserLimit:     v.PerUserLimit,
		BudgetVnd:        v.BudgetVND,
		UsedBudgetVnd:    usedBudget,
		StartsAt:         timestamppb.New(v.StartsAt),
		EndsAt:           timestamppb.New(v.EndsAt),
		IsActive:         isActive,
		Saved:            saved,
		Eligible:         eligible,
		IneligibleReason: ineligibleReason,
		Status:           domainVoucherStatusToProto(st),
		CategoryIds:      append([]string(nil), v.CategoryIDs...),
	}
}

func flashSaleToProto(fs *domain.FlashSale, liveSlots map[string]int64, now time.Time) *promotionv1.FlashSale {
	items := make([]*promotionv1.FlashSaleItem, 0, len(fs.Items))
	for i := range fs.Items {
		item := &fs.Items[i]
		rem := min(max(item.RemainingSlots, 0), item.TotalSlots)
		if liveSlots != nil {
			if live, ok := liveSlots[lua.FlashRemainingKey(item.ID)]; ok {
				rem = min(rem, max(int32(live), 0))
			}
		}
		items = append(items, flashSaleItemToProto(item, rem))
	}
	st := domain.FlashWindowStatusActive
	switch {
	case fs.Status == domain.FlashWindowStatusEnded || !fs.EndsAt.After(now):
		st = domain.FlashWindowStatusEnded
	case now.Before(fs.StartsAt):
		st = "UPCOMING"
	}
	return &promotionv1.FlashSale{
		Id:                 fs.ID,
		Name:               fs.Name,
		StartsAt:           timestamppb.New(fs.StartsAt),
		EndsAt:             timestamppb.New(fs.EndsAt),
		Items:              items,
		MinDiscountPercent: fs.MinDiscountPercent,
		CategoryIds:        append([]string(nil), fs.CategoryIDs...),
		Status:             st,
	}
}

func flashSaleItemToProto(item *domain.FlashSaleItem, remaining int32) *promotionv1.FlashSaleItem {
	pb := &promotionv1.FlashSaleItem{
		Id:               item.ID,
		FlashSaleId:      item.FlashSaleID,
		ShopId:           item.ShopID,
		SkuId:            item.SKUID,
		OriginalPriceVnd: item.OriginalPriceVND,
		FlashPriceVnd:    item.FlashPriceVND,
		TotalSlots:       item.TotalSlots,
		RemainingSlots:   remaining,
		PerUserLimit:     item.PerUserLimit,
		Status:           domainFlashStatusToProto(item.Status),
		RejectionReason:  item.RejectionReason,
	}
	if !item.CreatedAt.IsZero() {
		pb.CreatedAt = timestamppb.New(item.CreatedAt)
	}
	return pb
}

func protoScopeToDomain(s promotionv1.VoucherScope) (string, error) {
	switch s {
	case promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM:
		return domain.VoucherScopePlatform, nil
	case promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP:
		return domain.VoucherScopeShop, nil
	case promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP:
		return domain.VoucherScopeFreeship, nil
	default:
		return "", fmt.Errorf("invalid voucher scope: %v", s)
	}
}

func domainScopeToProto(s string) promotionv1.VoucherScope {
	switch s {
	case domain.VoucherScopePlatform:
		return promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM
	case domain.VoucherScopeShop:
		return promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP
	case domain.VoucherScopeFreeship:
		return promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP
	default:
		return promotionv1.VoucherScope_VOUCHER_SCOPE_UNSPECIFIED
	}
}

func protoDiscountTypeToDomain(dt promotionv1.DiscountType) (string, error) {
	switch dt {
	case promotionv1.DiscountType_DISCOUNT_TYPE_FIXED:
		return domain.DiscountTypeFixed, nil
	case promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT:
		return domain.DiscountTypePercent, nil
	default:
		return "", fmt.Errorf("invalid discount type: %v", dt)
	}
}

func domainDiscountTypeToProto(dt string) promotionv1.DiscountType {
	switch dt {
	case domain.DiscountTypeFixed:
		return promotionv1.DiscountType_DISCOUNT_TYPE_FIXED
	case domain.DiscountTypePercent:
		return promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT
	default:
		return promotionv1.DiscountType_DISCOUNT_TYPE_UNSPECIFIED
	}
}

func domainFlashStatusToProto(st string) promotionv1.FlashSaleItemStatus {
	switch st {
	case domain.FlashStatusPending:
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING
	case domain.FlashStatusApproved:
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED
	case domain.FlashStatusRejected:
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_REJECTED
	default:
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_UNSPECIFIED
	}
}

func isVoucherBudgetExhausted(v *domain.Voucher, remBudget, orderAmountVND int64) bool {
	if v == nil || v.BudgetVND <= 0 {
		return false
	}
	if remBudget <= 0 {
		return true
	}
	if v.Scope == domain.VoucherScopeFreeship {
		return false
	}
	base := orderAmountVND
	if v.MinOrderVND > base {
		base = v.MinOrderVND
	}
	if base > 0 {
		return remBudget < calculator.ComputeVoucherDiscount(v, base)
	}
	if v.DiscountType == domain.DiscountTypeFixed {
		return remBudget < v.ValueVND
	}
	return false
}

func normalizeSalesChannel(rawChannel, rawStorefrontShopID string) (string, string, error) {
	ch := strings.ToUpper(strings.TrimSpace(rawChannel))
	shopID := strings.TrimSpace(rawStorefrontShopID)
	if ch == "" {
		ch = domain.SalesChannelMarketplace
	}
	switch ch {
	case domain.SalesChannelMarketplace:
		if shopID != "" {
			return "", "", status.Error(codes.InvalidArgument, "storefront_shop_id must be empty when sales_channel is MARKETPLACE")
		}
		return domain.SalesChannelMarketplace, "", nil
	case domain.SalesChannelStorefront:
		if shopID == "" {
			return "", "", status.Error(codes.InvalidArgument, "storefront_shop_id is required when sales_channel is STOREFRONT")
		}
		return domain.SalesChannelStorefront, shopID, nil
	default:
		return "", "", status.Errorf(codes.InvalidArgument, "invalid sales_channel %q: must be MARKETPLACE or STOREFRONT", rawChannel)
	}
}

func (s *PromotionService) validateStorefrontPromotion(
	ctx context.Context,
	storefrontShopID string,
	shopItems []*promotionv1.ShopDiscountInput,
	shopVoucherIDs map[string]string,
	platformVoucherID string,
	freeshipVoucherID string,
	coinsToUse int64,
	explicitFlashItems []*promotionv1.FlashSaleReservationItem,
) error {
	if strings.TrimSpace(platformVoucherID) != "" {
		return status.Error(codes.InvalidArgument, "STOREFRONT_VOUCHER_NOT_ALLOWED: platform vouchers cannot be used on STOREFRONT channel")
	}
	if strings.TrimSpace(freeshipVoucherID) != "" {
		return status.Error(codes.InvalidArgument, "STOREFRONT_VOUCHER_NOT_ALLOWED: freeship vouchers cannot be used on STOREFRONT channel")
	}
	if coinsToUse > 0 {
		return status.Error(codes.InvalidArgument, "STOREFRONT_COINS_NOT_ALLOWED: platform coins cannot be used on STOREFRONT channel")
	}

	checkedFlash := make(map[string]bool)
	checkFlashShop := func(fiID string) error {
		fiID = strings.TrimSpace(fiID)
		if fiID == "" || checkedFlash[fiID] {
			return nil
		}
		checkedFlash[fiID] = true
		fi, err := s.store.GetFlashSaleItem(ctx, fiID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return status.Errorf(codes.Internal, "load flash sale item %s: %v", fiID, err)
		}
		if fi.ShopID != storefrontShopID {
			return status.Errorf(codes.InvalidArgument, "STOREFRONT_FLASH_SALE_SHOP_MISMATCH: flash sale item %s belongs to shop %q, not storefront shop %q", fiID, fi.ShopID, storefrontShopID)
		}
		return nil
	}

	checkedShopProg := make(map[string]bool)
	checkShopProgramShop := func(spID string) error {
		spID = strings.TrimSpace(spID)
		if spID == "" || checkedShopProg[spID] {
			return nil
		}
		checkedShopProg[spID] = true
		spi, err := s.store.GetShopDiscountProgramItem(ctx, spID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return status.Errorf(codes.Internal, "load shop discount program item %s: %v", spID, err)
		}
		if spi.ShopID != storefrontShopID {
			return status.Errorf(codes.InvalidArgument, "STOREFRONT_SHOP_PROGRAM_MISMATCH: shop discount program item %s belongs to shop %q, not storefront shop %q", spID, spi.ShopID, storefrontShopID)
		}
		return nil
	}

	for _, ps := range shopItems {
		sid := strings.TrimSpace(ps.GetShopId())
		if sid != storefrontShopID {
			return status.Errorf(codes.InvalidArgument, "STOREFRONT_SHOP_MISMATCH: shop %q does not match storefront shop %q", sid, storefrontShopID)
		}
		for _, line := range ps.GetItems() {
			if err := checkFlashShop(line.GetFlashSaleItemId()); err != nil {
				return err
			}
			if err := checkShopProgramShop(line.GetShopDiscountProgramItemId()); err != nil {
				return err
			}
		}
	}
	for _, f := range explicitFlashItems {
		if err := checkFlashShop(f.GetFlashSaleItemId()); err != nil {
			return err
		}
	}

	shopKeys := make([]string, 0, len(shopVoucherIDs))
	for k := range shopVoucherIDs {
		shopKeys = append(shopKeys, k)
	}
	sort.Strings(shopKeys)
	for _, rawShopID := range shopKeys {
		vid := strings.TrimSpace(shopVoucherIDs[rawShopID])
		if vid == "" {
			continue
		}
		mapShopID := strings.TrimSpace(rawShopID)
		if mapShopID != storefrontShopID {
			return status.Errorf(codes.InvalidArgument, "STOREFRONT_VOUCHER_SHOP_MISMATCH: shop voucher for shop %q is not allowed on storefront shop %q", mapShopID, storefrontShopID)
		}
		v, err := s.store.GetVoucherByIDOrCode(ctx, vid)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return status.Errorf(codes.Internal, "load voucher %s: %v", vid, err)
		}
		if v.Scope != domain.VoucherScopeShop {
			return status.Errorf(codes.InvalidArgument, "STOREFRONT_VOUCHER_NOT_ALLOWED: voucher %s has scope %s; only SHOP vouchers are allowed on STOREFRONT channel", v.Code, v.Scope)
		}
		if v.ShopID != storefrontShopID {
			return status.Errorf(codes.InvalidArgument, "STOREFRONT_VOUCHER_SHOP_MISMATCH: shop voucher %s belongs to shop %q, not storefront shop %q", v.Code, v.ShopID, storefrontShopID)
		}
	}
	return nil
}

// resolveUserID requires valid authentication claims and enforces user_id ownership.
// Anonymous callers and service tokens are always rejected.
func resolveUserID(ctx context.Context, reqUserID string) (string, error) {
	reqUserID = strings.TrimSpace(reqUserID)
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return "", status.Error(codes.PermissionDenied, "service identity not allowed for this RPC")
	}
	if isAdmin(claims) {
		if reqUserID != "" {
			return reqUserID, nil
		}
		if uid := strings.TrimSpace(claims.UserID()); uid != "" {
			return uid, nil
		}
		return "", status.Error(codes.InvalidArgument, "user_id is required")
	}
	claimUID := strings.TrimSpace(claims.UserID())
	if claimUID == "" {
		return "", status.Error(codes.Unauthenticated, "invalid authentication claims")
	}
	if reqUserID != "" && reqUserID != claimUID {
		return "", status.Error(codes.PermissionDenied, "cannot access another user's resources")
	}
	return claimUID, nil
}

func requireCapabilityOrService(ctx context.Context, capName, msg string) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service == "" && !auth.HasCapability(claims, capName) {
		return nil, status.Error(codes.PermissionDenied, msg)
	}
	return claims, nil
}

func requireAdminOrService(ctx context.Context, msg string) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service == "" && !isAdmin(claims) {
		return nil, status.Error(codes.PermissionDenied, msg)
	}
	return claims, nil
}

func isAdmin(claims *auth.Claims) bool {
	return claims != nil && claims.Service == "" && auth.HasRole(claims, auth.RoleAdmin, auth.RoleSuperAdmin)
}

func isSeller(claims *auth.Claims) bool {
	return claims != nil && claims.Service == "" && auth.HasRole(claims, auth.RoleSeller)
}

func (s *PromotionService) requireShopNotSuspended(ctx context.Context, shopID string) error {
	shopID = strings.TrimSpace(shopID)
	if shopID == "" {
		return nil
	}
	suspended, err := s.store.IsShopSuspended(ctx, shopID)
	if err != nil {
		return status.Errorf(codes.Internal, "check shop suspension: %v", err)
	}
	if suspended {
		return status.Error(codes.FailedPrecondition, "SHOP_SUSPENDED: shop is currently suspended")
	}
	return nil
}

func validateKeySegment(field, val string) error {
	if strings.ContainsAny(val, "|\n\r") {
		return fmt.Errorf("%s contains invalid characters", field)
	}
	return nil
}

func mapCalcError(err error) error {
	switch {
	case errors.Is(err, calculator.ErrShopSuspended),
		errors.Is(err, calculator.ErrVoucherIneligible),
		errors.Is(err, calculator.ErrInsufficientCoins),
		errors.Is(err, calculator.ErrInvalidFlashSaleItem),
		errors.Is(err, calculator.ErrInvalidShopDiscountProgram),
		errors.Is(err, calculator.ErrShopProgramLimitExceeded):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Errorf(codes.Internal, "calculate discounts: %v", err)
	}
}

func mapLuaError(err error) error {
	var resErr *lua.ReservationError
	if errors.As(err, &resErr) {
		switch resErr.Code {
		case lua.ErrCodeFlashSoldOut,
			lua.ErrCodeVoucherOutOfQuota,
			lua.ErrCodeVoucherBudgetExhausted:
			return status.Error(codes.ResourceExhausted, resErr.Error())
		case lua.ErrCodeFlashUserLimitExceeded,
			lua.ErrCodeVoucherInactive,
			lua.ErrCodeVoucherUserLimitExceeded,
			lua.ErrCodeFlashNotInitialized,
			lua.ErrCodeVoucherNotInitialized,
			lua.ErrCodeVoucherBudgetNotInit,
			lua.ErrCodeShopProgUserLimitExceeded,
			lua.ErrCodeOrderAlreadyReleased:
			return status.Error(codes.FailedPrecondition, resErr.Error())
		case lua.ErrCodeInvalidFlashQty,
			lua.ErrCodeInvalidShopProgQty:
			return status.Error(codes.InvalidArgument, resErr.Error())
		default:
			return status.Error(codes.FailedPrecondition, resErr.Error())
		}
	}
	return status.Errorf(codes.Internal, "reserve promotion in redis: %v", err)
}

func unmarshalBreakdown(raw []byte) *promotionv1.DiscountBreakdown {
	bd := &promotionv1.DiscountBreakdown{}
	if len(raw) > 0 {
		_ = protojson.Unmarshal(raw, bd)
	}
	return bd
}
