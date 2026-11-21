package service_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func TestService_SyncRedisFromStore(t *testing.T) {
	ctx := context.Background()
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()

	// Seed active voucher with budget, active voucher without budget, and inactive voucher directly in store
	vActive, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "SYNCBUDGET", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 10000,
		Quota: 20, PerUserLimit: 1, BudgetVND: 200000,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	vNoBudget, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "SYNCNOBUDGET", Scope: domain.VoucherScopeShop, ShopID: "shop-1",
		DiscountType: domain.DiscountTypeFixed, ValueVND: 5000,
		Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	vInactive, err := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "SYNCINACTIVE", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 5000,
		Quota: 10, PerUserLimit: 1,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Seed active approved flash sale item & ended flash sale item
	fsLive, err := mem.CreateFlashSale(ctx, domain.FlashSale{
		Name: "Sync Live FS", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fiLive, err := mem.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fsLive.ID, ShopID: "shop-1", SKUID: "sku-sync-live", FlashPriceVND: 50000, TotalSlots: 15, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mem.ApproveFlashSaleItem(ctx, fiLive.ID, "admin-1", true, ""); err != nil {
		t.Fatal(err)
	}

	// Pre-populate one live Redis key to verify SyncRedisFromStore does NOT overwrite live counters
	if err := eng.InitVoucherKeys(ctx, vNoBudget.ID, 4, 0); err != nil {
		t.Fatal(err)
	}

	if err := svc.SyncRedisFromStore(ctx); err != nil {
		t.Fatalf("SyncRedisFromStore: %v", err)
	}

	if q, ok, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vActive.ID)); !ok || q != 20 {
		t.Fatalf("expected vActive quota=20 synced, got %d (%v)", q, ok)
	}
	if b, ok, _ := eng.GetInt64(ctx, lua.VoucherBudgetRemainingKey(vActive.ID)); !ok || b != 200000 {
		t.Fatalf("expected vActive budget=200000 synced, got %d (%v)", b, ok)
	}
	if q, ok, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vNoBudget.ID)); !ok || q != 4 {
		t.Fatalf("SyncRedisFromStore must not overwrite live key (want 4), got %d (%v)", q, ok)
	}
	if _, ok, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vInactive.ID)); ok {
		t.Fatal("inactive voucher should not be synced to Redis")
	}
	if rem, ok, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fiLive.ID)); !ok || rem != 15 {
		t.Fatalf("expected live flash item slots=15 synced, got %d (%v)", rem, ok)
	}
}

func TestService_CreateVoucher_ValidationBranches(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	sellerNoShopCtx := ctxWithRole("seller-2", "seller", "")

	cases := []struct {
		name string
		ctx  context.Context
		req  *promotionv1.CreateVoucherRequest
		want codes.Code
	}{
		{
			name: "invalid_scope",
			ctx:  adminCtx,
			req:  &promotionv1.CreateVoucherRequest{Code: "C1", DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED},
			want: codes.InvalidArgument,
		},
		{
			name: "invalid_discount_type",
			ctx:  adminCtx,
			req:  &promotionv1.CreateVoucherRequest{Code: "C2", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM},
			want: codes.InvalidArgument,
		},
		{
			name: "seller_without_shop_id",
			ctx:  sellerNoShopCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "C3", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 1,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.PermissionDenied,
		},
		{
			name: "empty_code",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "   ", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 1,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.InvalidArgument,
		},
		{
			name: "invalid_key_segment_in_code",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "BAD|CODE", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 1,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.InvalidArgument,
		},
		{
			name: "fixed_zero_value",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "ZEROFIX", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 0, Quota: 1,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.InvalidArgument,
		},
		{
			name: "percent_out_of_range",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "PCT150", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT, DiscountPercent: 150, Quota: 1,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.InvalidArgument,
		},
		{
			name: "negative_budget",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "NEGBUD", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, BudgetVnd: -1, Quota: 1,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.InvalidArgument,
		},
		{
			name: "zero_quota",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "ZEROQ", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 0,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.InvalidArgument,
		},
		{
			name: "negative_per_user_limit",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "NEGLIM", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 5, PerUserLimit: -2,
				StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			},
			want: codes.InvalidArgument,
		},
		{
			name: "missing_timestamps",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "NOTS", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 5,
			},
			want: codes.InvalidArgument,
		},
		{
			name: "ends_before_starts",
			ctx:  adminCtx,
			req: &promotionv1.CreateVoucherRequest{
				Code: "BADWIN", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000, Quota: 5,
				StartsAt: timestamppb.New(now.Add(time.Hour)), EndsAt: timestamppb.New(now),
			},
			want: codes.InvalidArgument,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.CreateVoucher(tc.ctx, tc.req); status.Code(err) != tc.want {
				t.Fatalf("CreateVoucher(%s): got %v, want %v", tc.name, err, tc.want)
			}
		})
	}

	// Valid creation with empty name defaults name=code, and duplicate code returns AlreadyExists
	created, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code: "DUPCODE", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT, DiscountPercent: 15, MaxDiscountVnd: 30000,
		Quota: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil || created.Voucher.Name != "DUPCODE" || created.Voucher.ShopId != "shop-1" {
		t.Fatalf("CreateVoucher valid: resp=%+v err=%v", created, err)
	}
	if _, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code: "DUPCODE", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 5000,
		Quota: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("duplicate voucher code: want AlreadyExists, got %v", err)
	}
}

func TestService_ListAvailableVouchersAndSaveVoucher_AllBranches(t *testing.T) {
	ctx := context.Background()
	svc, mem, eng := setupService(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Empty list returns cleanly
	emptyResp, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{})
	if err != nil || len(emptyResp.Vouchers) != 0 {
		t.Fatalf("empty ListAvailableVouchers: %+v err=%v", emptyResp, err)
	}

	// Storefront shop_id mismatch
	if _, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{
		SalesChannel: "STOREFRONT", StorefrontShopId: "shop-a", ShopId: "shop-b",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected storefront mismatch error, got %v", err)
	}

	// Seed vouchers covering EVERY eligibility/ineligibility branch
	vInactive, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "INACT", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 10000, Quota: 5, PerUserLimit: 1, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: false,
	})
	vFuture, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "FUTURE", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 10000, Quota: 5, PerUserLimit: 1, StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), IsActive: true,
	})
	_ = vFuture
	vExpired, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "EXP", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 10000, Quota: 5, PerUserLimit: 1, StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), IsActive: true,
	})
	vBudgetZero, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "NOBUD", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 10000, Quota: 5, PerUserLimit: 1, BudgetVND: 10000, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
	})
	_ = eng.InitVoucherKeys(ctx, vBudgetZero.ID, 5, 10000)
	// Drain budget to 0 via reserve
	if _, err := svc.ReservePromotion(ctxWithRole("other-u", "buyer", ""), &promotionv1.ReservePromotionRequest{
		OrderId: "drain-bud-1", UserId: "other-u", PlatformVoucherId: vBudgetZero.ID,
	}); err != nil {
		t.Fatalf("drain budget reserve: %v", err)
	}
	vPerUser, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "LIMIT1", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 10000, Quota: 5, PerUserLimit: 1, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
	})
	_ = eng.InitVoucherKeys(ctx, vPerUser.ID, 5, 0)
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "use-limit1", UserId: "buyer-1", PlatformVoucherId: vPerUser.ID,
	}); err != nil {
		t.Fatalf("use per-user limit: %v", err)
	}
	vOutQuota, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "NOQUOTA", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 10000, Quota: 1, PerUserLimit: 1, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
	})
	_ = eng.InitVoucherKeys(ctx, vOutQuota.ID, 0, 0)

	vMinOrder, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "MIN200K", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 10000, MinOrderVND: 200000, Quota: 5, PerUserLimit: 1, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
	})
	_ = eng.InitVoucherKeys(ctx, vMinOrder.ID, 5, 0)

	// Admin querying on behalf of buyer-1 exercises resolveOptionalUserID admin branch + all voucherToProto cases
	listResp, err := svc.ListAvailableVouchers(adminCtx, &promotionv1.ListAvailableVouchersRequest{
		UserId:         "buyer-1",
		Scope:          promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		OrderAmountVnd: 100000,
	})
	if err != nil || len(listResp.Vouchers) != 7 {
		t.Fatalf("expected 7 vouchers, got %d err=%v", len(listResp.Vouchers), err)
	}
	gotReasons := make(map[string]string)
	for _, v := range listResp.Vouchers {
		if v.Eligible {
			t.Fatalf("voucher %s unexpectedly eligible", v.Code)
		}
		gotReasons[v.Code] = v.IneligibleReason
	}
	wantReasons := map[string]string{
		"INACT":   "voucher is inactive",
		"FUTURE":  "voucher has not started yet",
		"EXP":     "voucher has expired",
		"NOQUOTA": "voucher is out of quota",
		"NOBUD":   "voucher budget exhausted",
		"LIMIT1":  "per-user usage limit reached",
	}
	for code, want := range wantReasons {
		if gotReasons[code] != want {
			t.Fatalf("voucher %s reason=%q, want %q", code, gotReasons[code], want)
		}
	}

	// SaveVoucher error branches + save by Code
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SaveVoucher empty: want InvalidArgument, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{Code: "NONEXISTENT"}); status.Code(err) != codes.NotFound {
		t.Fatalf("SaveVoucher missing: want NotFound, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{VoucherId: vInactive.ID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SaveVoucher inactive: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{VoucherId: vExpired.ID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SaveVoucher expired: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{VoucherId: vOutQuota.ID}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("SaveVoucher out of quota: want ResourceExhausted, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{VoucherId: vBudgetZero.ID}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("SaveVoucher budget exhausted: want ResourceExhausted, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{VoucherId: vPerUser.ID}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SaveVoucher per-user limit: want FailedPrecondition, got %v", err)
	}
	// Save by Code succeeds and marks Saved=true
	saveResp, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{Code: "MIN200K"})
	if err != nil || !saveResp.Saved || !saveResp.Voucher.Saved {
		t.Fatalf("SaveVoucher by code failed: resp=%+v err=%v", saveResp, err)
	}
}

type stubProductClient struct {
	reserveErr error
	releaseErr error
	failFirst  int32
	relCalls   atomic.Int32
}

func (s *stubProductClient) ReserveFlashSaleStock(_ context.Context, _ *productv1.ReserveFlashSaleStockRequest, _ ...grpc.CallOption) (*productv1.ReserveFlashSaleStockResponse, error) {
	if s.reserveErr != nil {
		return nil, s.reserveErr
	}
	return &productv1.ReserveFlashSaleStockResponse{ReservedQty: 1}, nil
}

func (s *stubProductClient) ReleaseFlashSaleStock(_ context.Context, _ *productv1.ReleaseFlashSaleStockRequest, _ ...grpc.CallOption) (*productv1.ReleaseFlashSaleStockResponse, error) {
	n := s.relCalls.Add(1)
	if n <= s.failFirst {
		return nil, status.Error(codes.Unavailable, "transient product service down")
	}
	if s.releaseErr != nil {
		return nil, s.releaseErr
	}
	return &productv1.ReleaseFlashSaleStockResponse{ReleasedQty: 1}, nil
}

type failingApproveStore struct {
	*store.MemoryStore
	approveErr error
}

func (f *failingApproveStore) ApproveFlashSaleItem(_ context.Context, _, _ string, _ bool, _ string) (*domain.FlashSaleItem, string, error) {
	return nil, "", f.approveErr
}

func TestService_FlashSaleValidation_RetryAndSweeper(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := setupService(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	sellerNoShop := ctxWithRole("seller-2", "seller", "")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// CreateFlashSale validation errors
	if _, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{Name: "  "}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty name: %v", err)
	}
	if _, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{Name: "FS"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing timestamps: %v", err)
	}
	if _, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "FS", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ends_at <= starts_at: %v", err)
	}

	fsUp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Upcoming FS", StartsAt: timestamppb.New(now.Add(time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fsEnded, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Past FS", StartsAt: timestamppb.New(now.Add(-2 * time.Hour)), EndsAt: timestamppb.New(now.Add(-time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// RegisterFlashSaleItem RBAC & validation errors
	if _, err := svc.RegisterFlashSaleItem(sellerNoShop, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsUp.FlashSale.Id, SkuId: "s", FlashPriceVnd: 1000, TotalSlots: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller no shop: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-2", SkuId: "s", FlashPriceVnd: 1000, TotalSlots: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller cross shop: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(buyerCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-1", SkuId: "s", FlashPriceVnd: 1000, TotalSlots: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer register: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(adminCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsUp.FlashSale.Id}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing fields: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(adminCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-1", SkuId: "s", FlashPriceVnd: 0, TotalSlots: 1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("price <= 0: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(adminCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-1", SkuId: "s", FlashPriceVnd: 1000, TotalSlots: 0}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("slots <= 0: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(adminCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-1", SkuId: "s", FlashPriceVnd: 1000, TotalSlots: 1, PerUserLimit: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("per_user_limit < 0: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(adminCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: "missing-fs", ShopId: "shop-1", SkuId: "s", FlashPriceVnd: 1000, TotalSlots: 1}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing flash sale: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(adminCtx, &promotionv1.RegisterFlashSaleItemRequest{FlashSaleId: fsEnded.FlashSale.Id, ShopId: "shop-1", SkuId: "s", FlashPriceVnd: 1000, TotalSlots: 1}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ended flash sale: %v", err)
	}

	// Admin can register & approve item in upcoming sale, ListActiveFlashSales(IncludeUpcoming=true) reports UPCOMING
	regUp, err := svc.RegisterFlashSaleItem(adminCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-up", FlashPriceVnd: 50000, TotalSlots: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveFlashSaleItem(sellerCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regUp.Item.Id, Approve: true}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller approve: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: "  ", Approve: true}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty item_id: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: "missing-item", Approve: true}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing item: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regUp.Item.Id, Approve: true}); err != nil {
		t.Fatal(err)
	}
	upList, err := svc.ListActiveFlashSales(ctx, &promotionv1.ListActiveFlashSalesRequest{IncludeUpcoming: true})
	if err != nil || len(upList.FlashSales) != 1 || upList.FlashSales[0].Status != "UPCOMING" {
		t.Fatalf("expected UPCOMING flash sale, got %+v err=%v", upList, err)
	}

	// EndFlashSaleWindow validation + transient productClient retry on Unavailable
	if _, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: "  "}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty window_id: %v", err)
	}
	if _, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing window: %v", err)
	}
	stubProd := &stubProductClient{failFirst: 1}
	svc.SetProductClient(stubProd)
	endResp, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fsUp.FlashSale.Id})
	if err != nil || endResp.ReleasedItems != 1 || stubProd.relCalls.Load() != 2 {
		t.Fatalf("expected retryable release to succeed on attempt 2: resp=%+v calls=%d err=%v", endResp, stubProd.relCalls.Load(), err)
	}

	// Permanent non-retryable productClient release error logs warning and skips marking released
	fsFail, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Fail Release FS", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	regFail, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsFail.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-fail-rel", FlashPriceVnd: 10000, TotalSlots: 5,
	})
	svc.SetProductClient(&stubProductClient{})
	_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regFail.Item.Id, Approve: true})
	svc.SetProductClient(&stubProductClient{releaseErr: status.Error(codes.InvalidArgument, "bad request")})
	endFailResp, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fsFail.FlashSale.Id})
	if err != nil || endFailResp.ReleasedItems != 0 {
		t.Fatalf("expected ReleasedItems=0 when product release fails permanently, got %+v err=%v", endFailResp, err)
	}
	// Sweep also surfaces firstErr
	if n, err := svc.SweepExpiredFlashSales(ctx); err == nil || n != 0 {
		t.Fatalf("expected sweep error when product release fails, got n=%d err=%v", n, err)
	}

	// StartFlashSaleSweeper ticks, sweeps recovered item, and stops cleanly on cancel
	svc.SetProductClient(&stubProductClient{})
	sweepCtx, cancelSweep := context.WithCancel(ctx)
	svc.StartFlashSaleSweeper(sweepCtx, 15*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	cancelSweep()
	// After background sweep released regFail, a manual sweep returns 0
	if n, err := svc.SweepExpiredFlashSales(ctx); err != nil || n != 0 {
		t.Fatalf("expected background sweeper to have released item, got n=%d err=%v", n, err)
	}

	// Approve DB error triggers ProductService ReleaseFlashSaleStock compensation
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	mem2 := store.NewMemoryStore()
	fStore := &failingApproveStore{MemoryStore: mem2, approveErr: errors.New("db write fail")}
	svc2 := service.NewPromotionService(fStore, lua.NewEngine(rdb), logger.New("t", logger.WithWriter(io.Discard)))
	compProd := &stubProductClient{}
	svc2.SetProductClient(compProd)
	fs2, _ := mem2.CreateFlashSale(ctx, domain.FlashSale{Name: "Comp FS", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour)})
	fi2, _ := mem2.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{FlashSaleID: fs2.ID, ShopID: "shop-1", SKUID: "sku-c", FlashPriceVND: 1000, TotalSlots: 3}, now)
	svc2.SetClock(func() time.Time { return now })
	if _, err := svc2.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi2.ID, Approve: true}); status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal on DB approve failure, got %v", err)
	}
	if compProd.relCalls.Load() != 1 {
		t.Fatalf("expected ProductService ReleaseFlashSaleStock compensation called once, got %d", compProd.relCalls.Load())
	}
}

func TestService_CoinsPreviewReserveRelease_EdgeCases(t *testing.T) {
	ctx := context.Background()
	svc, mem, _ := setupService(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// CreditCoins validation & default reason/refID
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{UserId: "  ", Amount: 100}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty user_id: %v", err)
	}
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{UserId: "buyer-1", Amount: 0}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("amount <= 0: %v", err)
	}
	crResp, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{UserId: "buyer-1", Amount: 5000})
	if err != nil || crResp.Balance != 5000 {
		t.Fatalf("CreditCoins default reason/ref failed: %+v err=%v", crResp, err)
	}
	// Admin GetCoinBalance for buyer-1
	balResp, err := svc.GetCoinBalance(adminCtx, &promotionv1.GetCoinBalanceRequest{UserId: "buyer-1"})
	if err != nil || balResp.Balance != 5000 || len(balResp.RecentTransactions) != 1 || balResp.RecentTransactions[0].Reason != "admin_credit" {
		t.Fatalf("admin GetCoinBalance unexpected: %+v err=%v", balResp, err)
	}

	// PreviewDiscounts negative coins & warnings for missing flash item / missing voucher
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{CoinsToUse: -10}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative coins preview: %v", err)
	}
	prevResp, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		PlatformVoucherId: "MISSING_VOUCHER",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1", SubtotalVnd: 100000, ShippingFeeVnd: 15000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-1", Quantity: 1, UnitPriceVnd: 100000, FlashSaleItemId: "missing-fi"},
				},
			},
		},
		CoinsToUse: 1000,
	})
	if err != nil || len(prevResp.Breakdown.Warnings) < 2 || prevResp.Breakdown.TotalCoinsDiscountVnd != 1000 {
		t.Fatalf("preview with warnings unexpected: %+v err=%v", prevResp, err)
	}

	// ReservePromotion validation errors
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{OrderId: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty order_id: %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{OrderId: "bad|ord"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid order_id segment: %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{OrderId: "ord-neg", CoinsToUse: -1}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("negative coins reserve: %v", err)
	}

	// Bare voucher reservation edge cases (no shop_items)
	vShop, _ := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code: "BARESHOP", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	vMin, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "BAREMIN", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, MinOrderVnd: 50000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	vShip, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "BARESHIP", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 15000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	vInactive, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "BAREINACT", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 5000, Quota: 5, PerUserLimit: 1, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: false,
	})
	vExpired, _ := mem.CreateVoucher(ctx, domain.Voucher{
		Code: "BAREEXP", Scope: domain.VoucherScopePlatform, DiscountType: domain.DiscountTypeFixed,
		ValueVND: 5000, Quota: 5, PerUserLimit: 1, StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(-time.Hour), IsActive: true,
	})

	// Missing voucher -> NotFound
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "bare-1", PlatformVoucherId: "MISSING",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("bare missing voucher: %v", err)
	}
	// Inactive voucher -> FailedPrecondition
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "bare-2", PlatformVoucherId: vInactive.ID,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bare inactive voucher: %v", err)
	}
	// Scope mismatch -> FailedPrecondition
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "bare-3", PlatformVoucherId: vShop.Voucher.Id,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bare scope mismatch: %v", err)
	}
	// Wrong shop -> FailedPrecondition
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "bare-4", ShopVoucherIds: map[string]string{"shop-other": vShop.Voucher.Id},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bare wrong shop: %v", err)
	}
	// Outside window -> FailedPrecondition
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "bare-5", PlatformVoucherId: vExpired.ID,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bare expired: %v", err)
	}
	// MinOrder > 0 without shop_items -> FailedPrecondition
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "bare-6", PlatformVoucherId: vMin.Voucher.Id,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bare min_order: %v", err)
	}
	// Valid bare reservation with shop + freeship vouchers -> succeeds, then same user hits VOUCHER_USER_LIMIT_EXCEEDED -> FailedPrecondition
	bareResp, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId:           "bare-ok-1",
		ShopVoucherIds:    map[string]string{"shop-1": vShop.Voucher.Id},
		FreeshipVoucherId: vShip.Voucher.Id,
	})
	if err != nil || !bareResp.Reserved || bareResp.Breakdown.TotalDiscountVnd != 25000 {
		t.Fatalf("bare valid reserve unexpected: %+v err=%v", bareResp, err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId:           "bare-user-lim",
		FreeshipVoucherId: vShip.Voucher.Id,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("bare per-user limit exceeded: want FailedPrecondition, got %v", err)
	}

	// Flash reservation validation branches & dedup/merge across explicit + shop_items
	fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Res Flash", StartsAt: timestamppb.New(now.Add(-time.Minute)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	fiPending, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-pend", FlashPriceVnd: 50000, TotalSlots: 5, PerUserLimit: 2,
	})
	fiApp, _ := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-app", FlashPriceVnd: 50000, TotalSlots: 5, PerUserLimit: 3,
	})
	_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiApp.Item.Id, Approve: true})

	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-empty", FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: "", Quantity: 1}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty flash_sale_item_id: %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-badseg", FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: "a|b", Quantity: 1}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid flash_sale_item_id segment: %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-qty0", FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: fiApp.Item.Id, Quantity: 0}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("qty <= 0: %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-notfound", FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: "missing-fi", Quantity: 1}},
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing flash item: %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-pend", FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: fiPending.Item.Id, Quantity: 1}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("unapproved flash item: %v", err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-skumis", FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: fiApp.Item.Id, SkuId: "wrong-sku", Quantity: 1}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("sku mismatch: %v", err)
	}

	// Duplicate explicit flash item merges quantities (1+1=2), and ReleasePromotion releases both flash + vouchers
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-merge-1",
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: fiApp.Item.Id, SkuId: "sku-app", Quantity: 1},
			{FlashSaleItemId: fiApp.Item.Id, SkuId: "sku-app", Quantity: 1},
		},
	}); err != nil {
		t.Fatalf("merged flash reserve: %v", err)
	}
	if _, err := svc.ReleasePromotion(adminCtx, &promotionv1.ReleasePromotionRequest{OrderId: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReleasePromotion empty order_id: %v", err)
	}
	if _, err := svc.ReleasePromotion(adminCtx, &promotionv1.ReleasePromotionRequest{OrderId: "bad|id"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("ReleasePromotion invalid order_id: %v", err)
	}
	if relResp, err := svc.ReleasePromotion(adminCtx, &promotionv1.ReleasePromotionRequest{OrderId: "fl-merge-1"}); err != nil || !relResp.Released || relResp.AlreadyReleased {
		t.Fatalf("ReleasePromotion fl-merge-1: %+v err=%v", relResp, err)
	}
	// Re-reserving a released order_id returns FailedPrecondition
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "fl-merge-1",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("re-reserving released order: want FailedPrecondition, got %v", err)
	}
}
