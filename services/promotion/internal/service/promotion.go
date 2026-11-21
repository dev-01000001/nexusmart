package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc"
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

// ProductStockClient defines the subset of ProductService RPCs used to reserve/release SKU stock for flash sales.
type ProductStockClient interface {
	ReserveFlashSaleStock(ctx context.Context, in *productv1.ReserveFlashSaleStockRequest, opts ...grpc.CallOption) (*productv1.ReserveFlashSaleStockResponse, error)
	ReleaseFlashSaleStock(ctx context.Context, in *productv1.ReleaseFlashSaleStockRequest, opts ...grpc.CallOption) (*productv1.ReleaseFlashSaleStockResponse, error)
}

// PromotionService implements nexusmart.promotion.v1.PromotionServiceServer.
type PromotionService struct {
	promotionv1.UnimplementedPromotionServiceServer

	store         store.Store
	engine        *lua.Engine
	productClient ProductStockClient
	logger        *slog.Logger
	nowFn         func() time.Time
}

// NewPromotionService creates a PromotionService wired to a Store and Redis Lua Engine.
func NewPromotionService(st store.Store, engine *lua.Engine, logger *slog.Logger) *PromotionService {
	if logger == nil {
		logger = slog.Default()
	}
	return &PromotionService{
		store:  st,
		engine: engine,
		logger: logger,
		nowFn:  func() time.Time { return time.Now().UTC() },
	}
}

// SetProductClient wires the ProductService client used for Phase 2b flash sale stock reservations.
func (s *PromotionService) SetProductClient(client ProductStockClient) {
	s.productClient = client
}

// SetClock overrides the wall clock function (intended for deterministic unit tests).
func (s *PromotionService) SetClock(fn func() time.Time) {
	if fn != nil {
		s.nowFn = fn
	}
}

// SyncRedisFromStore rehydrates missing Redis counters from Postgres without overwriting live keys.
func (s *PromotionService) SyncRedisFromStore(ctx context.Context) error {
	vouchers, err := s.store.ListAllActiveVouchers(ctx)
	if err != nil {
		return fmt.Errorf("list active vouchers for redis sync: %w", err)
	}
	for _, v := range vouchers {
		remQuota := v.Quota - v.UsedCount
		if remQuota < 0 {
			remQuota = 0
		}
		remBudget := v.BudgetVND - v.UsedBudgetVND
		if remBudget < 0 {
			remBudget = 0
		}
		if syncErr := s.engine.SyncVoucherKeysIfMissing(ctx, v.ID, remQuota, v.BudgetVND, remBudget); syncErr != nil {
			return fmt.Errorf("sync voucher %s to redis: %w", v.ID, syncErr)
		}
	}

	items, err := s.store.ListApprovedFlashSaleItems(ctx)
	if err != nil {
		return fmt.Errorf("list approved flash items for redis sync: %w", err)
	}
	now := s.nowFn()
	for _, item := range items {
		if item.WindowStatus == domain.FlashWindowStatusEnded || !item.EndsAt.After(now) {
			continue
		}
		if syncErr := s.engine.InitFlashItemSlots(ctx, item.ID, item.RemainingSlots, false); syncErr != nil {
			return fmt.Errorf("sync flash item %s to redis: %w", item.ID, syncErr)
		}
	}
	return nil
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

	// Enforce authentication and RBAC on voucher creation.
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
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
	case isAdmin(claims):
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

	if discTypeStr == domain.DiscountTypeFixed {
		if req.GetValueVnd() <= 0 {
			return nil, status.Error(codes.InvalidArgument, "value_vnd must be > 0 for FIXED discount")
		}
	} else {
		if req.GetDiscountPercent() < 1 || req.GetDiscountPercent() > 100 {
			return nil, status.Error(codes.InvalidArgument, "discount_percent must be between 1 and 100 for PERCENT discount")
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
		CreatedBy:       createdBy,
	})
	if err != nil {
		if errors.Is(err, store.ErrDuplicateCode) {
			return nil, status.Error(codes.AlreadyExists, "voucher code already exists")
		}
		return nil, status.Errorf(codes.Internal, "create voucher: %v", err)
	}

	if err := s.engine.InitVoucherKeys(ctx, created.ID, created.Quota, created.BudgetVND); err != nil {
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

	userID, err := resolveOptionalUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	shopFilter := strings.TrimSpace(req.GetShopId())
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
	for _, v := range vouchers {
		redisKeys = append(redisKeys, lua.VoucherRemainingKey(v.ID))
		if v.BudgetVND > 0 {
			redisKeys = append(redisKeys, lua.VoucherBudgetRemainingKey(v.ID))
		}
		if userID != "" {
			redisKeys = append(redisKeys, lua.VoucherUserKey(v.ID, userID))
		}
	}
	liveVals, err := s.engine.MGetInt64(ctx, redisKeys)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read live voucher counters: %v", err)
	}

	now := s.nowFn()
	resp := &promotionv1.ListAvailableVouchersResponse{
		Vouchers: make([]*promotionv1.Voucher, 0, len(vouchers)),
	}
	for i := range vouchers {
		v := &vouchers[i]
		remQuota := v.Quota - v.UsedCount
		if live, ok := liveVals[lua.VoucherRemainingKey(v.ID)]; ok {
			remQuota = int32(live)
		}
		if remQuota < 0 {
			remQuota = 0
		}

		remBudget := v.BudgetVND - v.UsedBudgetVND
		if v.BudgetVND > 0 {
			if live, ok := liveVals[lua.VoucherBudgetRemainingKey(v.ID)]; ok {
				remBudget = live
			}
			if remBudget < 0 {
				remBudget = 0
			}
		}

		var userUsed int32
		if userID != "" {
			if live, ok := liveVals[lua.VoucherUserKey(v.ID, userID)]; ok {
				userUsed = int32(live)
			}
		}

		resp.Vouchers = append(resp.Vouchers, voucherToProto(v, remQuota, remBudget, userUsed, savedMap[v.ID], req.GetOrderAmountVnd(), now))
	}
	return resp, nil
}

// SaveVoucher atomically checks voucher quota/eligibility and saves the voucher into user's wallet without duplicates.
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

	now := s.nowFn()
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
	if remQuota <= 0 {
		return nil, status.Error(codes.ResourceExhausted, "voucher is out of quota")
	}
	if v.BudgetVND > 0 && remBudget <= 0 {
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

// CreateFlashSale creates a flash sale time window (admin only).
func (s *PromotionService) CreateFlashSale(ctx context.Context, req *promotionv1.CreateFlashSaleRequest) (*promotionv1.CreateFlashSaleResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !isAdmin(claims) {
		return nil, status.Error(codes.PermissionDenied, "only admins can create flash sales")
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

	fs, err := s.store.CreateFlashSale(ctx, domain.FlashSale{
		Name:      name,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		CreatedBy: createdBy,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create flash sale: %v", err)
	}
	return &promotionv1.CreateFlashSaleResponse{FlashSale: flashSaleToProto(fs, nil, s.nowFn())}, nil
}

// RegisterFlashSaleItem enrolls a seller SKU into a flash sale, rejecting overlapping windows for the same SKU.
func (s *PromotionService) RegisterFlashSaleItem(ctx context.Context, req *promotionv1.RegisterFlashSaleItemRequest) (*promotionv1.RegisterFlashSaleItemResponse, error) {
	shopID := strings.TrimSpace(req.GetShopId())
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
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
	case isAdmin(claims):
		// Admin allowed when shop_id provided
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

	item, err := s.store.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID:   flashSaleID,
		ShopID:        shopID,
		SKUID:         skuID,
		FlashPriceVND: req.GetFlashPriceVnd(),
		TotalSlots:    req.GetTotalSlots(),
		PerUserLimit:  perUserLimit,
	}, s.nowFn())
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

// ApproveFlashSaleItem approves or rejects a flash sale item, reserving or releasing SKU stock in ProductService
// and managing its Redis atomic slot counter.
func (s *PromotionService) ApproveFlashSaleItem(ctx context.Context, req *promotionv1.ApproveFlashSaleItemRequest) (*promotionv1.ApproveFlashSaleItemResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !isAdmin(claims) {
		return nil, status.Error(codes.PermissionDenied, "only admins can review flash sale items")
	}
	adminID := claims.UserID()

	itemID := strings.TrimSpace(req.GetItemId())
	if itemID == "" {
		return nil, status.Error(codes.InvalidArgument, "item_id is required")
	}

	existing, err := s.store.GetFlashSaleItem(ctx, itemID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "flash sale item not found")
		}
		return nil, status.Errorf(codes.Internal, "get flash sale item: %v", err)
	}

	now := s.nowFn()
	if req.GetApprove() {
		if existing.WindowStatus == domain.FlashWindowStatusEnded || !existing.EndsAt.After(now) {
			return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_ENDED: cannot approve item in an ended flash sale window")
		}

		// Reserve SKU stock in ProductService BEFORE marking item APPROVED or loading Redis slots.
		// ProductService.ReserveFlashSaleStock is strictly idempotent by flash_sale_item_id.
		if s.productClient != nil {
			if _, rErr := s.productClient.ReserveFlashSaleStock(ctx, &productv1.ReserveFlashSaleStockRequest{
				FlashSaleItemId: existing.ID,
				SkuId:           existing.SKUID,
				Quantity:        existing.TotalSlots,
				EndsAt:          timestamppb.New(existing.EndsAt),
			}); rErr != nil {
				return nil, rErr
			}
		}

		if existing.Status == domain.FlashStatusApproved {
			if err := s.engine.InitFlashItemSlots(ctx, existing.ID, existing.RemainingSlots, false); err != nil {
				return nil, status.Errorf(codes.Internal, "ensure flash sale redis slots: %v", err)
			}
			remaining := existing.RemainingSlots
			if live, ok, getErr := s.engine.GetInt64(ctx, lua.FlashRemainingKey(existing.ID)); getErr == nil && ok {
				remaining = int32(live)
			}
			return &promotionv1.ApproveFlashSaleItemResponse{Item: flashSaleItemToProto(existing, remaining)}, nil
		}

		item, prevStatus, err := s.store.ApproveFlashSaleItem(ctx, itemID, adminID, true, "")
		if err != nil {
			if s.productClient != nil {
				compCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_, _ = s.productClient.ReleaseFlashSaleStock(compCtx, &productv1.ReleaseFlashSaleStockRequest{
					FlashSaleItemId: existing.ID,
				})
				cancel()
			}
			if errors.Is(err, store.ErrNotFound) {
				return nil, status.Error(codes.NotFound, "flash sale item not found")
			}
			if errors.Is(err, store.ErrFlashSaleEnded) {
				return nil, status.Error(codes.FailedPrecondition, "FLASH_SALE_ENDED: cannot approve item in an ended flash sale window")
			}
			return nil, status.Errorf(codes.Internal, "approve flash sale item: %v", err)
		}

		overwrite := prevStatus != domain.FlashStatusApproved
		if err := s.engine.InitFlashItemSlots(ctx, item.ID, item.RemainingSlots, overwrite); err != nil {
			return nil, status.Errorf(codes.Internal, "initialize flash sale redis slots: %v", err)
		}
		remaining := item.RemainingSlots
		if live, ok, getErr := s.engine.GetInt64(ctx, lua.FlashRemainingKey(item.ID)); getErr == nil && ok {
			remaining = int32(live)
		}
		return &promotionv1.ApproveFlashSaleItemResponse{Item: flashSaleItemToProto(item, remaining)}, nil
	}

	// Rejecting or revoking an item.
	item, prevStatus, err := s.store.ApproveFlashSaleItem(ctx, itemID, adminID, false, strings.TrimSpace(req.GetReason()))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "flash sale item not found")
		}
		return nil, status.Errorf(codes.Internal, "reject flash sale item: %v", err)
	}

	if err := s.engine.DeleteFlashItemSlots(ctx, item.ID); err != nil {
		return nil, status.Errorf(codes.Internal, "delete rejected flash sale redis slots: %v", err)
	}
	if prevStatus == domain.FlashStatusApproved {
		if err := s.releaseFlashItemProductStock(ctx, item.ID); err != nil {
			return nil, status.Errorf(codes.Internal, "release rejected flash sale product stock: %v", err)
		}
		_ = s.store.MarkFlashSaleItemStockReleased(ctx, item.ID, now)
	}

	return &promotionv1.ApproveFlashSaleItemResponse{Item: flashSaleItemToProto(item, item.RemainingSlots)}, nil
}

// EndFlashSaleWindow immediately ends/cancels a flash sale window (admin only), deletes Redis slot keys,
// and releases all remaining reserved SKU stock in ProductService idempotently.
func (s *PromotionService) EndFlashSaleWindow(ctx context.Context, req *promotionv1.EndFlashSaleWindowRequest) (*promotionv1.EndFlashSaleWindowResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !isAdmin(claims) || claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "only admins can end flash sale windows")
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
		if relErr := s.releaseFlashItemSlotsAndStock(ctx, it.ID, now); relErr != nil {
			s.logger.WarnContext(ctx, "failed to release flash sale stock while ending window",
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

// SweepExpiredFlashSales marks expired flash sale windows ENDED, clears their Redis slot keys,
// and releases remaining reserved SKU stock in ProductService.
func (s *PromotionService) SweepExpiredFlashSales(ctx context.Context) (int, error) {
	now := s.nowFn()
	expired, err := s.store.ListExpiredUnreleasedFlashSaleItems(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list expired unreleased flash sale items: %w", err)
	}
	released := 0
	var firstErr error
	for _, it := range expired {
		if relErr := s.releaseFlashItemSlotsAndStock(ctx, it.ID, now); relErr != nil {
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

// StartFlashSaleSweeper periodically releases expired flash sale windows until ctx is canceled.
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

func (s *PromotionService) releaseFlashItemSlotsAndStock(ctx context.Context, itemID string, now time.Time) error {
	if err := s.engine.DeleteFlashItemSlots(ctx, itemID); err != nil {
		return fmt.Errorf("delete flash sale redis slots: %w", err)
	}
	if err := s.releaseFlashItemProductStock(ctx, itemID); err != nil {
		return err
	}
	if err := s.store.MarkFlashSaleItemStockReleased(ctx, itemID, now); err != nil {
		return err
	}
	return nil
}

func (s *PromotionService) releaseFlashItemProductStock(ctx context.Context, itemID string) error {
	if s.productClient == nil {
		return nil
	}
	var lastErr error
	backoff := 50 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		_, lastErr = s.productClient.ReleaseFlashSaleStock(ctx, &productv1.ReleaseFlashSaleStockRequest{
			FlashSaleItemId: itemID,
		})
		if lastErr == nil {
			return nil
		}
		code := status.Code(lastErr)
		if code != codes.Unavailable && code != codes.DeadlineExceeded && code != codes.Aborted {
			return lastErr
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
			}
		}
	}
	return lastErr
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
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !isAdmin(claims) {
		return nil, status.Error(codes.PermissionDenied, "only admins can credit coins")
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
	if refID == "" {
		refID = fmt.Sprintf("credit:%s:%d", userID, s.nowFn().UnixNano())
	}

	bal, already, err := s.store.CreditCoins(ctx, userID, req.GetAmount(), reason, refID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "credit coins: %v", err)
	}
	return &promotionv1.CreditCoinsResponse{
		UserId:          userID,
		Balance:         bal,
		AlreadyCredited: already,
	}, nil
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

	calcInput, err := s.buildCalcInput(ctx, userID, req.GetShopItems(), req.GetShopVoucherIds(), req.GetPlatformVoucherId(), req.GetFreeshipVoucherId(), req.GetCoinsToUse(), false)
	if err != nil {
		return nil, err
	}

	out, err := calculator.Calculate(calcInput)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "calculate discounts: %v", err)
	}
	return &promotionv1.PreviewDiscountsResponse{Breakdown: out.Breakdown}, nil
}

// ReservePromotion atomically reserves flash sale slots, voucher quotas/budgets (via Redis Lua),
// and coins (via Postgres TX) idempotently by order_id. Compensates Redis on Postgres failure.
func (s *PromotionService) ReservePromotion(ctx context.Context, req *promotionv1.ReservePromotionRequest) (*promotionv1.ReservePromotionResponse, error) {
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if err := validateKeySegment("order_id", orderID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	userID, err := resolveUserID(ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if userID == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	if err := validateKeySegment("user_id", userID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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
			req.GetFlashSaleItems(),
		); err != nil {
			return nil, err
		}
	}

	// Fast idempotency check in Postgres
	if existing, err := s.store.GetReservation(ctx, orderID); err == nil {
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

	// Collect and merge flash sale items to reserve (deduplicating by flash_sale_item_id)
	flashSpecs, reservedFlash, err := s.resolveFlashReservations(ctx, req.GetFlashSaleItems(), req.GetShopItems(), now)
	if err != nil {
		return nil, err
	}

	// Calculate discounts and validate vouchers/coins
	var breakdown *promotionv1.DiscountBreakdown
	var voucherSpecs []lua.VoucherReserveSpec
	var reservedVouchers []domain.ReservedVoucher
	var coinsToDeduct int64

	if len(req.GetShopItems()) > 0 {
		calcInput, buildErr := s.buildCalcInput(ctx, userID, req.GetShopItems(), req.GetShopVoucherIds(), req.GetPlatformVoucherId(), req.GetFreeshipVoucherId(), req.GetCoinsToUse(), true)
		if buildErr != nil {
			return nil, buildErr
		}
		out, calcErr := calculator.Calculate(calcInput)
		if calcErr != nil {
			return nil, mapCalcError(calcErr)
		}
		breakdown = out.Breakdown
		coinsToDeduct = out.CoinsUsed
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
				VoucherID:   av.VoucherID,
				Scope:       av.Scope,
				ShopID:      av.ShopID,
				DiscountVND: av.DiscountVND,
				HasBudget:   av.HasBudget,
			})
		}
	} else {
		// Direct reservation without shop_items (e.g. flash-sale concurrency reserve, standalone voucher/coin reserve)
		vSpecs, rVouchers, vErr := s.resolveBareVoucherReservations(ctx, userID, req.GetShopVoucherIds(), req.GetPlatformVoucherId(), req.GetFreeshipVoucherId(), now)
		if vErr != nil {
			return nil, vErr
		}
		voucherSpecs = vSpecs
		reservedVouchers = rVouchers
		coinsToDeduct = req.GetCoinsToUse()
		breakdown = &promotionv1.DiscountBreakdown{
			TotalCoinsDiscountVnd: coinsToDeduct,
			PlatformCostVnd:       coinsToDeduct,
			TotalDiscountVnd:      coinsToDeduct,
		}
		for _, rv := range reservedVouchers {
			switch rv.Scope {
			case domain.VoucherScopeShop:
				breakdown.TotalShopVoucherDiscountVnd += rv.DiscountVND
				breakdown.ShopCostVnd += rv.DiscountVND
			case domain.VoucherScopePlatform:
				breakdown.TotalPlatformVoucherDiscountVnd += rv.DiscountVND
				breakdown.PlatformCostVnd += rv.DiscountVND
			case domain.VoucherScopeFreeship:
				breakdown.TotalFreeshipDiscountVnd += rv.DiscountVND
				breakdown.PlatformCostVnd += rv.DiscountVND
			}
			breakdown.TotalDiscountVnd += rv.DiscountVND
		}
	}

	// Atomically execute Redis Lua reservation (also guards concurrent duplicate order_id!)
	luaOutcome, err := s.engine.Reserve(ctx, lua.ReserveInput{
		OrderID:    orderID,
		UserID:     userID,
		FlashItems: flashSpecs,
		Vouchers:   voucherSpecs,
	})
	if err != nil {
		return nil, mapLuaError(err)
	}

	if luaOutcome.AlreadyReserved {
		// Wait briefly in case the winning concurrent call is finishing its Postgres commit.
		saved := s.waitForCommittedReservation(ctx, orderID)
		if saved != nil {
			if saved.Status == domain.ReservationStatusReleased {
				return nil, status.Error(codes.FailedPrecondition, "promotion reservation for this order was already released")
			}
			return &promotionv1.ReservePromotionResponse{
				Reserved:        true,
				AlreadyReserved: true,
				Breakdown:       unmarshalBreakdown(saved.BreakdownJSON),
			}, nil
		}
		return nil, status.Error(codes.Aborted, "concurrent promotion reservation did not commit")
	}

	breakdownBytes, err := protojson.Marshal(breakdown)
	if err != nil {
		s.compensateRedisRelease(ctx, orderID)
		return nil, status.Errorf(codes.Internal, "marshal discount breakdown: %v", err)
	}

	// Persist reservation + atomically deduct coins in Postgres.
	// On ANY failure after Redis succeeded, immediately compensate via release_atomic.lua!
	existing, alreadyCommitted, err := s.store.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:        orderID,
		UserID:         userID,
		Status:         domain.ReservationStatusReserved,
		CoinsUsed:      coinsToDeduct,
		Vouchers:       reservedVouchers,
		FlashSaleItems: reservedFlash,
		BreakdownJSON:  breakdownBytes,
	})
	if err != nil {
		s.compensateRedisRelease(ctx, orderID)
		switch {
		case errors.Is(err, store.ErrInsufficientCoins):
			return nil, status.Error(codes.FailedPrecondition, "insufficient coin balance")
		case errors.Is(err, store.ErrReservationAlreadyReleased):
			return nil, status.Error(codes.FailedPrecondition, "promotion reservation for this order was already released")
		default:
			return nil, status.Errorf(codes.Internal, "persist promotion reservation: %v", err)
		}
	}

	if alreadyCommitted {
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

// ReleasePromotion idempotently compensates Redis flash-sale slots, voucher quotas/budgets,
// per-user usage counters, and Postgres coin deductions for an order.
func (s *PromotionService) ReleasePromotion(ctx context.Context, req *promotionv1.ReleasePromotionRequest) (*promotionv1.ReleasePromotionResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if !isAdmin(claims) {
		return nil, status.Error(codes.PermissionDenied, "only admins or internal services can release promotions")
	}

	alreadyReleased, err := s.ReleaseReservationByOrderID(ctx, req.GetOrderId())
	if err != nil {
		return nil, err
	}

	return &promotionv1.ReleasePromotionResponse{
		Released:        true,
		AlreadyReleased: alreadyReleased,
	}, nil
}

// ReleaseReservationByOrderID idempotently releases Redis and Postgres promotion reservations
// for in-process callers (such as the `order.refunded` JetStream consumer) without weakening
// authentication on the external ReleasePromotion gRPC RPC.
func (s *PromotionService) ReleaseReservationByOrderID(ctx context.Context, orderID string) (bool, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return false, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if err := validateKeySegment("order_id", orderID); err != nil {
		return false, status.Error(codes.InvalidArgument, err.Error())
	}

	// Collect known touched keys from Postgres (optional hint for Redis Cluster semantics)
	var extraKeys []string
	if saved, err := s.store.GetReservation(ctx, orderID); err == nil {
		for _, f := range saved.FlashSaleItems {
			extraKeys = append(extraKeys, lua.FlashRemainingKey(f.FlashSaleItemID), lua.FlashUserKey(f.FlashSaleItemID, saved.UserID))
		}
		for _, v := range saved.Vouchers {
			extraKeys = append(extraKeys,
				lua.VoucherRemainingKey(v.VoucherID),
				lua.VoucherUserKey(v.VoucherID, saved.UserID),
				lua.VoucherBudgetRemainingKey(v.VoucherID),
			)
		}
	}

	redisOutcome, err := s.engine.Release(ctx, orderID, extraKeys...)
	if err != nil {
		return false, status.Errorf(codes.Internal, "release redis promotion reservation: %v", err)
	}

	pgAlreadyReleased, err := s.store.ReleaseReservation(ctx, orderID)
	if err != nil {
		return false, status.Errorf(codes.Internal, "release postgres promotion reservation: %v", err)
	}

	return redisOutcome.AlreadyReleased && pgAlreadyReleased, nil
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

func (s *PromotionService) resolveFlashReservations(
	ctx context.Context,
	explicit []*promotionv1.FlashSaleReservationItem,
	shopItems []*promotionv1.ShopDiscountInput,
	now time.Time,
) ([]lua.FlashReserveSpec, []domain.ReservedFlashItem, error) {
	type mergedFlash struct {
		itemID string
		skuID  string
		qty    int32
	}
	merged := make(map[string]*mergedFlash)
	order := make([]string, 0)

	addItem := func(itemID, skuID string, qty int32) error {
		itemID = strings.TrimSpace(itemID)
		if itemID == "" {
			return nil
		}
		if err := validateKeySegment("flash_sale_item_id", itemID); err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		if qty <= 0 {
			return status.Errorf(codes.InvalidArgument, "flash sale item %s quantity must be > 0", itemID)
		}
		if cur, ok := merged[itemID]; ok {
			cur.qty += qty
			if cur.skuID == "" {
				cur.skuID = skuID
			}
			return nil
		}
		merged[itemID] = &mergedFlash{itemID: itemID, skuID: strings.TrimSpace(skuID), qty: qty}
		order = append(order, itemID)
		return nil
	}

	explicitSet := make(map[string]bool, len(explicit))
	for _, f := range explicit {
		id := strings.TrimSpace(f.GetFlashSaleItemId())
		if id == "" {
			return nil, nil, status.Error(codes.InvalidArgument, "flash_sale_item_id is required")
		}
		explicitSet[id] = true
		if err := addItem(id, f.GetSkuId(), f.GetQuantity()); err != nil {
			return nil, nil, err
		}
	}

	// Include flash items referenced only inside shop_items without double-counting explicit entries.
	for _, shop := range shopItems {
		for _, line := range shop.GetItems() {
			id := strings.TrimSpace(line.GetFlashSaleItemId())
			if id == "" || explicitSet[id] {
				continue
			}
			if err := addItem(id, line.GetSkuId(), line.GetQuantity()); err != nil {
				return nil, nil, err
			}
		}
	}

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
		if fi.WindowStatus == domain.FlashWindowStatusEnded || now.Before(fi.StartsAt) || now.After(fi.EndsAt) {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "flash sale item %s is outside active window", id)
		}
		if mf.skuID != "" && fi.SKUID != "" && mf.skuID != fi.SKUID {
			return nil, nil, status.Errorf(codes.InvalidArgument, "flash sale item %s does not match SKU %s", id, mf.skuID)
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
		})
	}
	return specs, reserved, nil
}

func (s *PromotionService) resolveBareVoucherReservations(
	ctx context.Context,
	userID string,
	shopVoucherIDs map[string]string,
	platformVoucherID string,
	freeshipVoucherID string,
	now time.Time,
) ([]lua.VoucherReserveSpec, []domain.ReservedVoucher, error) {
	type reqVoucher struct {
		id            string
		expectedScope string
		expectedShop  string
	}
	var list []reqVoucher

	shopKeys := make([]string, 0, len(shopVoucherIDs))
	for k := range shopVoucherIDs {
		shopKeys = append(shopKeys, k)
	}
	sort.Strings(shopKeys)
	for _, shopID := range shopKeys {
		vid := strings.TrimSpace(shopVoucherIDs[shopID])
		if vid != "" {
			list = append(list, reqVoucher{id: vid, expectedScope: domain.VoucherScopeShop, expectedShop: shopID})
		}
	}
	if vid := strings.TrimSpace(platformVoucherID); vid != "" {
		list = append(list, reqVoucher{id: vid, expectedScope: domain.VoucherScopePlatform})
	}
	if vid := strings.TrimSpace(freeshipVoucherID); vid != "" {
		list = append(list, reqVoucher{id: vid, expectedScope: domain.VoucherScopeFreeship})
	}

	seen := make(map[string]bool, len(list))
	specs := make([]lua.VoucherReserveSpec, 0, len(list))
	reserved := make([]domain.ReservedVoucher, 0, len(list))

	for _, item := range list {
		cand, err := s.loadVoucherCandidate(ctx, item.id, userID)
		if err != nil {
			return nil, nil, status.Errorf(codes.Internal, "load voucher %s: %v", item.id, err)
		}
		if !cand.Found || cand.Voucher == nil {
			return nil, nil, status.Errorf(codes.NotFound, "voucher %s not found", item.id)
		}
		v := cand.Voucher
		if seen[v.ID] {
			return nil, nil, status.Errorf(codes.InvalidArgument, "duplicate voucher %s in reservation", v.ID)
		}
		seen[v.ID] = true

		if !v.IsActive {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "voucher %s is inactive", v.Code)
		}
		if v.Scope != item.expectedScope {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "voucher %s scope mismatch", v.Code)
		}
		if item.expectedScope == domain.VoucherScopeShop && v.ShopID != item.expectedShop {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "voucher %s does not belong to shop %s", v.Code, item.expectedShop)
		}
		if now.Before(v.StartsAt) || now.After(v.EndsAt) {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "voucher %s is not within active time window", v.Code)
		}
		if v.MinOrderVND > 0 {
			return nil, nil, status.Errorf(codes.FailedPrecondition, "voucher %s requires minimum order %d VND", v.Code, v.MinOrderVND)
		}

		var disc int64
		if v.DiscountType == domain.DiscountTypeFixed {
			disc = v.ValueVND
		}
		specs = append(specs, lua.VoucherReserveSpec{
			VoucherID:    v.ID,
			PerUserLimit: v.PerUserLimit,
			DiscountVND:  disc,
			HasBudget:    v.BudgetVND > 0,
		})
		reserved = append(reserved, domain.ReservedVoucher{
			VoucherID:   v.ID,
			Scope:       v.Scope,
			ShopID:      v.ShopID,
			DiscountVND: disc,
			HasBudget:   v.BudgetVND > 0,
		})
	}
	return specs, reserved, nil
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
) (calculator.Input, error) {
	shops := make([]calculator.ShopInput, 0, len(pbShops))
	flashCandidates := make(map[string]calculator.FlashItemCandidate)

	for _, ps := range pbShops {
		si := calculator.ShopInput{
			ShopID:         strings.TrimSpace(ps.GetShopId()),
			SubtotalVND:    ps.GetSubtotalVnd(),
			ShippingFeeVND: ps.GetShippingFeeVnd(),
			Items:          make([]calculator.LineItemInput, 0, len(ps.GetItems())),
		}
		for _, pi := range ps.GetItems() {
			fiID := strings.TrimSpace(pi.GetFlashSaleItemId())
			si.Items = append(si.Items, calculator.LineItemInput{
				SKUID:           strings.TrimSpace(pi.GetSkuId()),
				Quantity:        pi.GetQuantity(),
				UnitPriceVND:    pi.GetUnitPriceVnd(),
				FlashSaleItemID: fiID,
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
		}
		shops = append(shops, si)
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

	var platformCand *calculator.VoucherCandidate
	if vid := strings.TrimSpace(platformVoucherID); vid != "" {
		var err error
		platformCand, err = s.loadVoucherCandidate(ctx, vid, userID)
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "load platform voucher %s: %v", vid, err)
		}
	}

	var freeshipCand *calculator.VoucherCandidate
	if vid := strings.TrimSpace(freeshipVoucherID); vid != "" {
		var err error
		freeshipCand, err = s.loadVoucherCandidate(ctx, vid, userID)
		if err != nil {
			return calculator.Input{}, status.Errorf(codes.Internal, "load freeship voucher %s: %v", vid, err)
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
		Now:             s.nowFn(),
		Strict:          strict,
		Shops:           shops,
		FlashItems:      flashCandidates,
		ShopVouchers:    shopVoucherCands,
		PlatformVoucher: platformCand,
		FreeshipVoucher: freeshipCand,
		CoinsToUse:      coinsToUse,
		AvailableCoins:  availCoins,
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

	remQuota := v.Quota - v.UsedCount
	if live, ok := vals[lua.VoucherRemainingKey(v.ID)]; ok {
		remQuota = int32(live)
	}
	if remQuota < 0 {
		remQuota = 0
	}

	remBudget := v.BudgetVND - v.UsedBudgetVND
	if v.BudgetVND > 0 {
		if live, ok := vals[lua.VoucherBudgetRemainingKey(v.ID)]; ok {
			remBudget = live
		}
		if remBudget < 0 {
			remBudget = 0
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

	eligible := true
	ineligibleReason := ""
	switch {
	case !v.IsActive:
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
	case v.BudgetVND > 0 && remBudget <= 0:
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
		IsActive:         v.IsActive,
		Saved:            saved,
		Eligible:         eligible,
		IneligibleReason: ineligibleReason,
	}
}

func flashSaleToProto(fs *domain.FlashSale, liveSlots map[string]int64, now time.Time) *promotionv1.FlashSale {
	items := make([]*promotionv1.FlashSaleItem, 0, len(fs.Items))
	for i := range fs.Items {
		item := &fs.Items[i]
		rem := item.RemainingSlots
		if liveSlots != nil {
			if live, ok := liveSlots[lua.FlashRemainingKey(item.ID)]; ok {
				rem = int32(live)
			}
		}
		if rem < 0 {
			rem = 0
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
		Id:       fs.ID,
		Name:     fs.Name,
		StartsAt: timestamppb.New(fs.StartsAt),
		EndsAt:   timestamppb.New(fs.EndsAt),
		Items:    items,
		Status:   st,
	}
}

func flashSaleItemToProto(item *domain.FlashSaleItem, remaining int32) *promotionv1.FlashSaleItem {
	return &promotionv1.FlashSaleItem{
		Id:              item.ID,
		FlashSaleId:     item.FlashSaleID,
		ShopId:          item.ShopID,
		SkuId:           item.SKUID,
		FlashPriceVnd:   item.FlashPriceVND,
		TotalSlots:      item.TotalSlots,
		RemainingSlots:  remaining,
		PerUserLimit:    item.PerUserLimit,
		Status:          domainFlashStatusToProto(item.Status),
		RejectionReason: item.RejectionReason,
	}
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

	for _, ps := range shopItems {
		sid := strings.TrimSpace(ps.GetShopId())
		if sid != storefrontShopID {
			return status.Errorf(codes.InvalidArgument, "STOREFRONT_SHOP_MISMATCH: shop %q does not match storefront shop %q", sid, storefrontShopID)
		}
		for _, line := range ps.GetItems() {
			if err := checkFlashShop(line.GetFlashSaleItemId()); err != nil {
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
// Anonymous callers are always rejected with Unauthenticated.
func resolveUserID(ctx context.Context, reqUserID string) (string, error) {
	reqUserID = strings.TrimSpace(reqUserID)
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "authentication required")
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

// resolveOptionalUserID is used by public-readable RPCs (ListAvailableVouchers) where anonymous
// browsing is allowed (returning empty userID) while preventing anonymous or cross-user IDOR.
func resolveOptionalUserID(ctx context.Context, reqUserID string) (string, error) {
	reqUserID = strings.TrimSpace(reqUserID)
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		if reqUserID != "" {
			return "", status.Error(codes.Unauthenticated, "authentication required to query user-specific voucher state")
		}
		return "", nil
	}
	if isAdmin(claims) {
		if reqUserID != "" {
			return reqUserID, nil
		}
		return strings.TrimSpace(claims.UserID()), nil
	}
	claimUID := strings.TrimSpace(claims.UserID())
	if claimUID == "" {
		return "", status.Error(codes.Unauthenticated, "invalid authentication claims")
	}
	if reqUserID != "" && reqUserID != claimUID {
		return "", status.Error(codes.PermissionDenied, "cannot inspect another user's voucher eligibility")
	}
	return claimUID, nil
}

func isAdmin(claims *auth.Claims) bool {
	return auth.HasRole(claims, "admin", "super_admin")
}

func isSeller(claims *auth.Claims) bool {
	return auth.HasRole(claims, "seller")
}

func validateKeySegment(field, val string) error {
	if strings.ContainsAny(val, "|\n\r") {
		return fmt.Errorf("%s contains invalid characters", field)
	}
	return nil
}

func mapCalcError(err error) error {
	switch {
	case errors.Is(err, calculator.ErrVoucherIneligible),
		errors.Is(err, calculator.ErrInsufficientCoins),
		errors.Is(err, calculator.ErrInvalidFlashSaleItem):
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
			lua.ErrCodeVoucherUserLimitExceeded,
			lua.ErrCodeFlashNotInitialized,
			lua.ErrCodeVoucherNotInitialized,
			lua.ErrCodeVoucherBudgetNotInit,
			lua.ErrCodeOrderAlreadyReleased:
			return status.Error(codes.FailedPrecondition, resErr.Error())
		case lua.ErrCodeInvalidFlashQty:
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
