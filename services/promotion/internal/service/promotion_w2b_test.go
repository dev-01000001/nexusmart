package service_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func setupBurstService(t *testing.T) (*service.PromotionService, *store.MemoryStore, *lua.Engine) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr:         mr.Addr(),
		PoolSize:     200,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		PoolTimeout:  30 * time.Second,
	})
	t.Cleanup(func() { _ = rdb.Close() })

	mem := store.NewMemoryStore()
	eng := lua.NewEngine(rdb)
	log := logger.New("promotion-w2b-test", logger.WithWriter(io.Discard))
	svc := service.NewPromotionService(mem, eng, log)
	return svc, mem, eng
}

type fakeProductClient struct {
	mu          sync.RWMutex
	skus        map[string]*productv1.SKU
	descendants map[string][]string
	descCalls   atomic.Int32
}

func newFakeProductClient() *fakeProductClient {
	return &fakeProductClient{
		skus:        make(map[string]*productv1.SKU),
		descendants: make(map[string][]string),
	}
}

func (f *fakeProductClient) setSKU(sku *productv1.SKU) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.skus[sku.Id] = sku
}

func (f *fakeProductClient) setDescendants(root string, ids []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.descendants[root] = append([]string(nil), ids...)
}

func (f *fakeProductClient) GetSKUsBatch(_ context.Context, skuIDs []string) (map[string]*productv1.SKU, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make(map[string]*productv1.SKU, len(skuIDs))
	for _, id := range skuIDs {
		if sk, ok := f.skus[id]; ok {
			out[id] = sk
		}
	}
	return out, nil
}

func (f *fakeProductClient) GetCategoryDescendants(_ context.Context, categoryIDs []string) ([]string, error) {
	f.descCalls.Add(1)
	f.mu.RLock()
	defer f.mu.RUnlock()
	seen := make(map[string]bool)
	out := make([]string, 0)
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, cid := range categoryIDs {
		add(cid)
		for _, child := range f.descendants[cid] {
			add(child)
		}
	}
	return out, nil
}

func strPtr(s string) *string                                    { return &s }
func i32Ptr(v int32) *int32                                      { return &v }
func i64Ptr(v int64) *int64                                      { return &v }
func dtPtr(v promotionv1.DiscountType) *promotionv1.DiscountType { return &v }

func TestService_W2B_VoucherLifecycle_UpdatePauseDeleteAndWallet(t *testing.T) {
	svc, _, eng := setupService(t)
	pcLife := newFakeProductClient()
	pcLife.setSKU(&productv1.SKU{Id: "sku-life", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 150000, MinPrice_30DVnd: 150000, AvailableStock: 20, IsActive: true})
	svc.SetProductClient(pcLife)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	orderSvcCtx := ctxWithService("order-service")
	buyer1Ctx := ctxWithRole("buyer-1", "buyer", "")
	buyer2Ctx := ctxWithRole("buyer-2", "buyer", "")

	// 1. Seller creates SHOP voucher
	vResp, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "W2BLIFE",
		Name:         "Original Name",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		MinOrderVnd:  100000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    100000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	vid := vResp.Voucher.Id
	if vResp.Voucher.Status != promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE {
		t.Fatalf("expected ACTIVE status, got %v", vResp.Voucher.Status)
	}

	// 2. While used_count == 0, full UpdateVoucher allowed (value, quota, budget, ends_at)
	upd0, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid,
		Name:      strPtr("Renamed Before Use"),
		ValueVnd:  i64Ptr(25000),
		Quota:     i32Ptr(8),
		BudgetVnd: i64Ptr(200000),
		EndsAt:    timestamppb.New(now.Add(48 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("UpdateVoucher (unused): %v", err)
	}
	if upd0.Voucher.ValueVnd != 25000 || upd0.Voucher.Quota != 8 || upd0.Voucher.RemainingQuota != 8 || upd0.Voucher.BudgetVnd != 200000 {
		t.Fatalf("unexpected updated unused voucher: %+v", upd0.Voucher)
	}

	// Buyer-1 saves voucher to wallet and uses it once (used_count = 1)
	if _, err := svc.SaveVoucher(buyer1Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vid}); err != nil {
		t.Fatalf("SaveVoucher: %v", err)
	}
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "w2b-life-use-1",
		UserId:         "buyer-1",
		ShopVoucherIds: map[string]string{"shop-1": vid},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 150000, ShippingFeeVnd: 20000}},
	}); err != nil {
		t.Fatalf("ReservePromotion 1: %v", err)
	}

	// 3. After used_count > 0: disallowed changes rejected (value_vnd, lowering quota, shortening ends_at, changing starts_at)
	if _, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid,
		ValueVnd:  i64Ptr(30000),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("changing value_vnd after use: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid,
		Quota:     i32Ptr(6),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("lowering quota after use: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid,
		EndsAt:    timestamppb.New(now.Add(12 * time.Hour)),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("shortening ends_at after use: want FailedPrecondition, got %v", err)
	}

	// Allowed after use: rename + raise quota (8->12) + raise budget (200k->300k) + extend ends_at (48h->72h)
	// Also passing unchanged ValueVnd/DiscountType (full PUT form compatibility) must succeed!
	upd1, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId:    vid,
		Name:         strPtr("Extended After Use"),
		DiscountType: dtPtr(promotionv1.DiscountType_DISCOUNT_TYPE_FIXED),
		ValueVnd:     i64Ptr(25000),
		Quota:        i32Ptr(12),
		BudgetVnd:    i64Ptr(300000),
		EndsAt:       timestamppb.New(now.Add(72 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("UpdateVoucher (allowed after use): %v", err)
	}
	// Used=1 so remaining quota should be 12-1=11, remaining budget 300k-25k=275k in Redis too!
	if upd1.Voucher.RemainingQuota != 11 {
		t.Fatalf("expected remaining_quota=11 after quota bump, got %d", upd1.Voucher.RemainingQuota)
	}
	if remB, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid)); remB != 275000 {
		t.Fatalf("expected Redis remaining budget=275000, got %d", remB)
	}

	// 4. RBAC checks: seller-2, admin, buyer, anon cannot mutate shop-1 voucher
	for name, c := range map[string]context.Context{"seller2": seller2Ctx, "admin": adminCtx, "buyer": buyer1Ctx} {
		if _, err := svc.SetVoucherStatus(c, &promotionv1.SetVoucherStatusRequest{
			VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s SetVoucherStatus on shop-1 voucher: want PermissionDenied, got %v", name, err)
		}
		if _, err := svc.DeleteVoucher(c, &promotionv1.DeleteVoucherRequest{VoucherId: vid}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s DeleteVoucher on shop-1 voucher: want PermissionDenied, got %v", name, err)
		}
	}
	if _, err := svc.DeleteVoucher(context.Background(), &promotionv1.DeleteVoucherRequest{VoucherId: vid}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon DeleteVoucher: want Unauthenticated, got %v", err)
	}

	// 5. Pause voucher -> immediately rejected in Save, Preview, Reserve (even for buyer-1 who already saved it!)
	pausedResp, err := svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
	})
	if err != nil || pausedResp.Voucher.Status != promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED || pausedResp.Voucher.IsActive {
		t.Fatalf("SetVoucherStatus(PAUSED) unexpected: resp=%+v err=%v", pausedResp, err)
	}

	if _, err := svc.SaveVoucher(buyer2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vid}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SaveVoucher on PAUSED: want FailedPrecondition, got %v", err)
	}
	prevPaused, err := svc.PreviewDiscounts(buyer1Ctx, &promotionv1.PreviewDiscountsRequest{
		ShopVoucherIds: map[string]string{"shop-1": vid},
		ShopItems: []*promotionv1.ShopDiscountInput{{
			ShopId:         "shop-1",
			ShippingFeeVnd: 20000,
			Items:          []*promotionv1.DiscountLineItem{{SkuId: "sku-life", Quantity: 1}},
		}},
	})
	if err != nil || prevPaused.Breakdown.TotalShopVoucherDiscountVnd != 0 || len(prevPaused.Breakdown.Warnings) == 0 {
		t.Fatalf("PreviewDiscounts on PAUSED should give 0 discount with warning, got %+v err=%v", prevPaused, err)
	}
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "w2b-life-paused-try",
		UserId:         "buyer-1",
		ShopVoucherIds: map[string]string{"shop-1": vid},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 150000, ShippingFeeVnd: 20000}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion on PAUSED: want FailedPrecondition, got %v", err)
	}

	// Buyer-1 wallet (saved=true) MUST still see PAUSED voucher as unusable (eligible=false, status=PAUSED)
	w1, err := svc.ListAvailableVouchers(buyer1Ctx, &promotionv1.ListAvailableVouchersRequest{ShopId: "shop-1"})
	if err != nil || len(w1.Vouchers) != 1 || !w1.Vouchers[0].Saved || w1.Vouchers[0].Eligible || w1.Vouchers[0].Status != promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED {
		t.Fatalf("buyer-1 wallet should show saved PAUSED voucher as ineligible, got %+v err=%v", w1, err)
	}
	// Buyer-2 (did NOT save) MUST NOT see PAUSED voucher on storefront
	w2, err := svc.ListAvailableVouchers(buyer2Ctx, &promotionv1.ListAvailableVouchersRequest{ShopId: "shop-1"})
	if err != nil || len(w2.Vouchers) != 0 {
		t.Fatalf("buyer-2 storefront should hide unsaved PAUSED voucher, got %+v err=%v", w2, err)
	}

	// 6. Resume ACTIVE -> works again!
	resumed, err := svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
	})
	if err != nil || resumed.Voucher.Status != promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE || !resumed.Voucher.IsActive {
		t.Fatalf("SetVoucherStatus(ACTIVE) failed: resp=%+v err=%v", resumed, err)
	}
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "w2b-life-resumed-use",
		UserId:         "buyer-2",
		ShopVoucherIds: map[string]string{"shop-1": vid},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 150000, ShippingFeeVnd: 20000}},
	}); err != nil {
		t.Fatalf("ReservePromotion after resume failed: %v", err)
	}

	// 7. Soft-delete voucher -> blocked everywhere, cannot reactivate/update, excluded from default manage list
	delResp, err := svc.DeleteVoucher(seller1Ctx, &promotionv1.DeleteVoucherRequest{VoucherId: vid})
	if err != nil || !delResp.Ok {
		t.Fatalf("DeleteVoucher failed: %+v err=%v", delResp, err)
	}
	if _, err := svc.SetVoucherStatus(seller1Ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("reactivating DELETED voucher: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.UpdateVoucher(seller1Ctx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, Name: strPtr("Nope"),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("updating DELETED voucher: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "w2b-life-del-try",
		UserId:         "buyer-2",
		ShopVoucherIds: map[string]string{"shop-1": vid},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 150000, ShippingFeeVnd: 20000}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion on DELETED: want FailedPrecondition, got %v", err)
	}
	// Default seller manage view excludes DELETED; filtering by status=DELETED shows it
	mDefault, _ := svc.ListAvailableVouchers(seller1Ctx, &promotionv1.ListAvailableVouchersRequest{ShopId: "shop-1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP})
	if len(mDefault.Vouchers) != 0 {
		t.Fatalf("default seller manage view should exclude DELETED, got %+v", mDefault.Vouchers)
	}
	mDeleted, _ := svc.ListAvailableVouchers(seller1Ctx, &promotionv1.ListAvailableVouchersRequest{
		ShopId: "shop-1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, IncludeInactive: true, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED,
	})
	if len(mDeleted.Vouchers) != 1 || mDeleted.Vouchers[0].Status != promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED {
		t.Fatalf("filtered DELETED manage view should include deleted voucher, got %+v", mDeleted.Vouchers)
	}
}

func TestService_W2B_VoucherCategoryScope_Descendants(t *testing.T) {
	svc, _, _ := setupService(t)
	pc := newFakeProductClient()
	pc.setDescendants("cat-fashion", []string{"cat-tops", "cat-tees"})
	pc.setSKU(&productv1.SKU{Id: "sku-tee", ShopId: "shop-1", CategoryId: "cat-tees", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})
	pc.setSKU(&productv1.SKU{Id: "sku-phone", ShopId: "shop-2", CategoryId: "cat-elec", PriceVnd: 500000, MinPrice_30DVnd: 500000, AvailableStock: 20, IsActive: true})
	svc.SetProductClient(pc)

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Seller cannot create category-scoped SHOP voucher
	if _, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code: "SHOPCATBAD", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 5,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		CategoryIds: []string{"cat-fashion"},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SHOP voucher with category_ids: want InvalidArgument, got %v", err)
	}

	// Admin creates category-scoped PLATFORM voucher (10% on fashion & descendants)
	vResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "FASH10", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT, DiscountPercent: 10, MaxDiscountVnd: 50000,
		MinOrderVnd: 100000, Quota: 10, PerUserLimit: 2,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		CategoryIds: []string{"cat-fashion"},
	})
	if err != nil {
		t.Fatalf("CreateVoucher platform cat: %v", err)
	}
	if len(vResp.Voucher.CategoryIds) != 1 || vResp.Voucher.CategoryIds[0] != "cat-fashion" {
		t.Fatalf("expected category_ids=[cat-fashion], got %v", vResp.Voucher.CategoryIds)
	}

	// Preview with 1 tee (200k, cat-tees descendant!) + 1 phone (500k, cat-elec) -> 10% of 200k = 20k ONLY!
	prev, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		PlatformVoucherId: vResp.Voucher.Id,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-tee", Quantity: 1, UnitPriceVnd: 200000}}},
			{ShopId: "shop-2", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-phone", Quantity: 1, UnitPriceVnd: 500000}}},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts cat: %v", err)
	}
	if prev.Breakdown.TotalPlatformVoucherDiscountVnd != 20000 {
		t.Fatalf("expected 20000 platform discount (10%% of 200k tee only), got %d", prev.Breakdown.TotalPlatformVoucherDiscountVnd)
	}
	if prev.Breakdown.Shops[0].PlatformVoucherDiscountVnd != 20000 || prev.Breakdown.Shops[1].PlatformVoucherDiscountVnd != 0 {
		t.Fatalf("expected all 20000 allocated to fashion shop, got %+v", prev.Breakdown.Shops)
	}

	// Order with ONLY electronics -> Reserve rejected with FailedPrecondition!
	if _, err := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
		OrderId:           "cat-mismatch-ord",
		UserId:            "buyer-1",
		PlatformVoucherId: vResp.Voucher.Id,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-2", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-phone", Quantity: 1, UnitPriceVnd: 500000}}},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion with only ineligible category: want FailedPrecondition, got %v", err)
	}
}

func TestService_W2B_FlashSaleValidation_ListsAndActiveWindow(t *testing.T) {
	svc, _, _ := setupService(t)
	pc := newFakeProductClient()
	pc.setDescendants("cat-fashion", []string{"cat-tee"})
	pc.setSKU(&productv1.SKU{
		Id: "sku-s1", ShopId: "shop-1", CategoryId: "cat-tee",
		PriceVnd: 200000, MinPrice_30DVnd: 180000, AvailableStock: 10, IsActive: true,
	})
	pc.setSKU(&productv1.SKU{
		Id: "sku-s2", ShopId: "shop-2", CategoryId: "cat-tee",
		PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 10, IsActive: true,
	})
	pc.setSKU(&productv1.SKU{
		Id: "sku-elec", ShopId: "shop-1", CategoryId: "cat-elec",
		PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 10, IsActive: true,
	})
	svc.SetProductClient(pc)

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	adminCtx := ctxWithRole("admin-1", "admin", "")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Campaign with min 20% discount and fashion category
	fsLive, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:               "Fashion Flash Live",
		StartsAt:           timestamppb.New(now.Add(-10 * time.Minute)),
		EndsAt:             timestamppb.New(now.Add(2 * time.Hour)),
		MinDiscountPercent: 20,
		CategoryIds:        []string{"cat-fashion"},
	})
	if err != nil {
		t.Fatalf("CreateFlashSale live: %v", err)
	}
	if fsLive.FlashSale.MinDiscountPercent != 20 || len(fsLive.FlashSale.CategoryIds) != 1 {
		t.Fatalf("unexpected flash sale metadata: %+v", fsLive.FlashSale)
	}
	fsUpcoming, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Upcoming Flash",
		StartsAt: timestamppb.New(now.Add(3 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(5 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Cross-shop SKU rejected at service layer!
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsLive.FlashSale.Id, SkuId: "sku-s2", FlashPriceVnd: 100000, TotalSlots: 5,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop SKU: want PermissionDenied, got %v", err)
	}
	// 2. Price >= min_price_30d_vnd (185k < 200k current, but >= 180k min_30d!) rejected!
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsLive.FlashSale.Id, SkuId: "sku-s1", FlashPriceVnd: 185000, TotalSlots: 5,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("price >= min_price_30d: want FailedPrecondition, got %v", err)
	}
	// 3. Below min_discount_percent (20% of 180k -> max 144k; 150k is only 16.6% off) rejected!
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsLive.FlashSale.Id, SkuId: "sku-s1", FlashPriceVnd: 150000, TotalSlots: 5,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("below min_discount_percent: want FailedPrecondition, got %v", err)
	}
	// 4. Wrong category rejected!
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsLive.FlashSale.Id, SkuId: "sku-elec", FlashPriceVnd: 100000, TotalSlots: 5,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("wrong category: want FailedPrecondition, got %v", err)
	}
	// 5. total_slots > available_stock (11 > 10) rejected!
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsLive.FlashSale.Id, SkuId: "sku-s1", FlashPriceVnd: 140000, TotalSlots: 11,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("slots > available_stock: want FailedPrecondition, got %v", err)
	}

	// 6. Valid registration (144k = exact 20% off 180k, slots=8 <= 10)
	regLive, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsLive.FlashSale.Id, SkuId: "sku-s1", FlashPriceVnd: 144000, TotalSlots: 8, PerUserLimit: 2,
	})
	if err != nil {
		t.Fatalf("valid RegisterFlashSaleItem: %v", err)
	}
	if regLive.Item.OriginalPriceVnd != 200000 {
		t.Fatalf("expected original_price_vnd=200000, got %d", regLive.Item.OriginalPriceVnd)
	}

	// 7. Re-validation on Approve: drop available_stock to 5 (< 8) -> Approve MUST reject!
	pc.setSKU(&productv1.SKU{
		Id: "sku-s1", ShopId: "shop-1", CategoryId: "cat-tee",
		PriceVnd: 200000, MinPrice_30DVnd: 180000, AvailableStock: 5, IsActive: true,
	})
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId: regLive.Item.Id, Approve: true,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("approve with dropped stock: want FailedPrecondition, got %v", err)
	}

	// Restore stock -> Approve succeeds
	pc.setSKU(&productv1.SKU{
		Id: "sku-s1", ShopId: "shop-1", CategoryId: "cat-tee",
		PriceVnd: 200000, MinPrice_30DVnd: 180000, AvailableStock: 10, IsActive: true,
	})
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId: regLive.Item.Id, Approve: true,
	}); err != nil {
		t.Fatalf("ApproveFlashSaleItem: %v", err)
	}

	// Register & approve item in UPCOMING flash sale -> ReservePromotion MUST reject upcoming!
	regUpc, err := svc.RegisterFlashSaleItem(seller2Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsUpcoming.FlashSale.Id, SkuId: "sku-s2", FlashPriceVnd: 150000, TotalSlots: 5, PerUserLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId: regUpc.Item.Id, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
		OrderId: "ord-upc-flash",
		UserId:  "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-2", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-s2", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: regUpc.Item.Id}}},
		},
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: regUpc.Item.Id, SkuId: "sku-s2", Quantity: 1}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion upcoming flash: want FailedPrecondition, got %v", err)
	}

	// 8. ListFlashSaleItems & ListAllFlashSales RBAC and filtering
	if _, err := svc.ListFlashSaleItems(buyerCtx, &promotionv1.ListFlashSaleItemsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListFlashSaleItems: want PermissionDenied, got %v", err)
	}
	if _, err := svc.ListFlashSaleItems(seller1Ctx, &promotionv1.ListFlashSaleItemsRequest{ShopId: "shop-2"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller1 listing shop-2 items: want PermissionDenied, got %v", err)
	}
	s1Items, err := svc.ListFlashSaleItems(seller1Ctx, &promotionv1.ListFlashSaleItemsRequest{Status: promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED})
	if err != nil || s1Items.Total != 1 || len(s1Items.Items) != 1 || s1Items.Items[0].Id != regLive.Item.Id {
		t.Fatalf("seller1 ListFlashSaleItems unexpected: %+v err=%v", s1Items, err)
	}
	adminItems, err := svc.ListFlashSaleItems(adminCtx, &promotionv1.ListFlashSaleItemsRequest{})
	if err != nil || adminItems.Total != 2 {
		t.Fatalf("admin ListFlashSaleItems: want total=2, got %+v err=%v", adminItems, err)
	}

	if _, err := svc.ListAllFlashSales(buyerCtx, &promotionv1.ListAllFlashSalesRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListAllFlashSales: want PermissionDenied, got %v", err)
	}
	activeSales, err := svc.ListAllFlashSales(seller1Ctx, &promotionv1.ListAllFlashSalesRequest{Status: "ACTIVE"})
	if err != nil || activeSales.Total != 1 || activeSales.FlashSales[0].Id != fsLive.FlashSale.Id {
		t.Fatalf("ListAllFlashSales(ACTIVE) unexpected: %+v err=%v", activeSales, err)
	}
	upcSales, err := svc.ListAllFlashSales(adminCtx, &promotionv1.ListAllFlashSalesRequest{Status: "UPCOMING"})
	if err != nil || upcSales.Total != 1 || upcSales.FlashSales[0].Id != fsUpcoming.FlashSale.Id {
		t.Fatalf("ListAllFlashSales(UPCOMING) unexpected: %+v err=%v", upcSales, err)
	}
}

func TestService_W2B_ShopDiscountPrograms_FullLifecycle_FlashWins_Concurrency_And_LegacyRelease(t *testing.T) {
	svc, mem, eng := setupBurstService(t)
	pc := newFakeProductClient()
	// sku-1: price increased in last 30d (200k > 170k min_30d) -> price_inflation_flagged!
	pc.setSKU(&productv1.SKU{Id: "sku-1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 170000, AvailableStock: 50, IsActive: true})
	pc.setSKU(&productv1.SKU{Id: "sku-2", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 50, IsActive: true})
	pc.setSKU(&productv1.SKU{Id: "sku-3", ShopId: "shop-2", CategoryId: "cat-1", PriceVnd: 300000, MinPrice_30DVnd: 300000, AvailableStock: 50, IsActive: true})
	svc.SetProductClient(pc)

	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	adminCtx := ctxWithRole("admin-1", "admin", "")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	svcCtx := ctxWithService("order-service")

	// Approved flash sale on sku-1 (120k) to verify overlap warning & flash-wins rule
	fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Overlap Flash", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fiResp, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsResp.FlashSale.Id, SkuId: "sku-1", FlashPriceVnd: 120000, TotalSlots: 20, PerUserLimit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
		t.Fatal(err)
	}

	// 1. Validation failures: cross-shop SKU, price >= orig, both price & percent
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "Cross Shop", StartsAt: timestamppb.New(now.Add(-time.Minute)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-3", DiscountedPriceVnd: 250000}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop SKU: want PermissionDenied, got %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "Bad Price", StartsAt: timestamppb.New(now.Add(-time.Minute)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-2", DiscountedPriceVnd: 100000}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("price >= orig: want InvalidArgument, got %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "Both Specs", StartsAt: timestamppb.New(now.Add(-time.Minute)), EndsAt: timestamppb.New(now.Add(time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-2", DiscountedPriceVnd: 80000, DiscountPercent: 20}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("both price and percent: want InvalidArgument, got %v", err)
	}

	// 2. Create valid shop-1 program covering sku-1 (inflation + flash overlap warnings) & sku-2 (20% off -> 80k, limit=5)
	prog1Resp, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "Shop1 Fall Sale",
		StartsAt: timestamppb.New(now.Add(-10 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(3 * time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{
			{SkuId: "sku-1", DiscountedPriceVnd: 150000, PerUserLimit: 3},
			{SkuId: "sku-2", DiscountPercent: 20, PerUserLimit: 5},
		},
	})
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram shop-1: %v", err)
	}
	if len(prog1Resp.Warnings) < 2 {
		t.Fatalf("expected both price inflation and flash overlap warnings, got %v", prog1Resp.Warnings)
	}
	var spi1, spi2 *promotionv1.ShopDiscountProgramItem
	for _, it := range prog1Resp.Program.Items {
		if it.SkuId == "sku-1" {
			spi1 = it
		}
		if it.SkuId == "sku-2" {
			spi2 = it
		}
	}
	if spi1 == nil || !spi1.PriceInflationFlagged || !strings.Contains(strings.ToLower(spi1.Warning), "flash") {
		t.Fatalf("expected spi1 inflation flag + flash warning, got %+v", spi1)
	}
	if spi2 == nil || spi2.DiscountedPriceVnd != 80000 || spi2.DiscountPercent != 20 || spi2.PriceInflationFlagged {
		t.Fatalf("unexpected spi2: %+v", spi2)
	}

	// 3. Overlapping another shop discount program on same SKU MUST be rejected (AlreadyExists)
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "Overlapping Program",
		StartsAt: timestamppb.New(now.Add(time.Hour)),
		EndsAt:   timestamppb.New(now.Add(4 * time.Hour)),
		Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-2", DiscountPercent: 10}},
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("overlapping shop program: want AlreadyExists, got %v", err)
	}

	// Shop-2 creates active program on sku-3 (240k, limit=2)
	prog2Resp, err := svc.CreateShopDiscountProgram(seller2Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "Shop2 Sale",
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(3 * time.Hour)),
		Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-3", DiscountedPriceVnd: 240000, PerUserLimit: 2}},
	})
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram shop-2: %v", err)
	}
	spi3 := prog2Resp.Program.Items[0]

	// 4. Public ListActiveShopDiscounts (anonymous call succeeds!)
	pubAct, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{
		SkuIds: []string{"sku-2", "sku-3"},
	})
	if err != nil || len(pubAct.Discounts) != 2 {
		t.Fatalf("ListActiveShopDiscounts unexpected: %+v err=%v", pubAct, err)
	}

	// 5. Preview & Reserve where sku-1 has BOTH flash & shop program (flash MUST win!) and sku-2 & sku-3 use shop programs
	prev, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         "shop-1",
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-1", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fiResp.Item.Id, ShopDiscountProgramItemId: spi1.Id},
					{SkuId: "sku-2", Quantity: 2, UnitPriceVnd: 100000, ShopDiscountProgramItemId: spi2.Id},
				},
			},
			{
				ShopId:         "shop-2",
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-3", Quantity: 1, UnitPriceVnd: 300000, ShopDiscountProgramItemId: spi3.Id},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts: %v", err)
	}
	// sku-1: flash -80k (NOT shop program -50k); sku-2: shop prog -20k*2 = -40k; sku-3: shop prog -60k
	if prev.Breakdown.TotalFlashSaleDiscountVnd != 80000 || prev.Breakdown.TotalShopProgramDiscountVnd != 100000 {
		t.Fatalf("expected flash=80000 shopProg=100000, got flash=%d shopProg=%d",
			prev.Breakdown.TotalFlashSaleDiscountVnd, prev.Breakdown.TotalShopProgramDiscountVnd)
	}

	const groupID = "w2b-grp-prog-1"
	res, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId: groupID,
		UserId:  "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         "shop-1",
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-1", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fiResp.Item.Id, ShopDiscountProgramItemId: spi1.Id},
					{SkuId: "sku-2", Quantity: 2, UnitPriceVnd: 100000, ShopDiscountProgramItemId: spi2.Id},
				},
			},
			{
				ShopId:         "shop-2",
				ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-3", Quantity: 1, UnitPriceVnd: 300000, ShopDiscountProgramItemId: spi3.Id},
				},
			},
		},
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: fiResp.Item.Id, SkuId: "sku-1", Quantity: 1, ShopId: "shop-1"},
		},
		ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
			{ShopDiscountProgramItemId: spi1.Id, SkuId: "sku-1", Quantity: 1, ShopId: "shop-1"}, // MUST be ignored because flash wins!
			{ShopDiscountProgramItemId: spi2.Id, SkuId: "sku-2", Quantity: 2, ShopId: "shop-1"},
			{ShopDiscountProgramItemId: spi3.Id, SkuId: "sku-3", Quantity: 1, ShopId: "shop-2"},
		},
	})
	if err != nil || !res.Reserved {
		t.Fatalf("ReservePromotion: resp=%+v err=%v", res, err)
	}
	// Verify spi1 per-user counter was NOT consumed (flash won!), while spi2=2 and spi3=1
	if u1, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(spi1.Id, "buyer-1")); u1 != 0 {
		t.Fatalf("spi1 user counter should be 0 because flash won, got %d", u1)
	}
	if u2, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(spi2.Id, "buyer-1")); u2 != 2 {
		t.Fatalf("spi2 user counter should be 2, got %d", u2)
	}
	if u3, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(spi3.Id, "buyer-1")); u3 != 1 {
		t.Fatalf("spi3 user counter should be 1, got %d", u3)
	}

	// Partial release shop-1 -> releases flash=1 and spi2=2; spi3 stays at 1!
	relS1, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: groupID, ShopId: "shop-1"})
	if err != nil || !relS1.Released || relS1.AlreadyReleased || relS1.FlashSaleQuantityReleased != 1 || relS1.ShopProgramQuantityReleased != 2 {
		t.Fatalf("ReleasePromotionShop(shop-1) unexpected: %+v err=%v", relS1, err)
	}
	if u2, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(spi2.Id, "buyer-1")); u2 != 0 {
		t.Fatalf("spi2 user counter should be 0 after shop-1 release, got %d", u2)
	}
	if u3, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(spi3.Id, "buyer-1")); u3 != 1 {
		t.Fatalf("spi3 user counter should still be 1, got %d", u3)
	}
	// Duplicate shop-1 release is idempotent
	relS1Dup, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: groupID, ShopId: "shop-1"})
	if err != nil || !relS1Dup.AlreadyReleased || relS1Dup.ShopProgramQuantityReleased != 0 {
		t.Fatalf("duplicate shop-1 release unexpected: %+v err=%v", relS1Dup, err)
	}
	// Full release releases remaining shop-2 cleanly without double-releasing shop-1
	if _, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: groupID}); err != nil {
		t.Fatalf("ReleasePromotion: %v", err)
	}
	if u3, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(spi3.Id, "buyer-1")); u3 != 0 {
		t.Fatalf("spi3 user counter should be 0 after full release, got %d", u3)
	}

	// 6. 1,000 concurrent ReservePromotion requests from same buyer against spi2 (per_user_limit=5)
	// With shop_items, exactly 5 get the shop discount program (and increment Redis to 5) while 995 fall back to regular price!
	const concurrency = 1000
	var (
		wg          sync.WaitGroup
		startCh     = make(chan struct{})
		discounted  atomic.Int64
		regularFall atomic.Int64
		unexpected  atomic.Int64
	)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			rResp, rErr := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: fmt.Sprintf("conc-prog-ord-%04d", idx),
				UserId:  "buyer-conc-prog",
				ShopItems: []*promotionv1.ShopDiscountInput{
					{
						ShopId: "shop-1",
						Items: []*promotionv1.DiscountLineItem{
							{SkuId: "sku-2", Quantity: 1, UnitPriceVnd: 100000, ShopDiscountProgramItemId: spi2.Id},
						},
					},
				},
			})
			if rErr != nil {
				unexpected.Add(1)
				return
			}
			if rResp.Breakdown.TotalShopProgramDiscountVnd == 20000 {
				discounted.Add(1)
			} else if rResp.Breakdown.TotalShopProgramDiscountVnd == 0 {
				regularFall.Add(1)
			} else {
				unexpected.Add(1)
			}
		}(i)
	}
	close(startCh)
	wg.Wait()
	if unexpected.Load() != 0 || discounted.Load() != 5 || regularFall.Load() != concurrency-5 {
		t.Fatalf("1000 concurrent shop program reserve: want 5 discounted / 995 regular / 0 unexpected, got %d / %d / %d",
			discounted.Load(), regularFall.Load(), unexpected.Load())
	}
	if uConc, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(spi2.Id, "buyer-conc-prog")); uConc != 5 {
		t.Fatalf("1000 concurrent shop program Redis counter: want 5, got %d", uConc)
	}

	// 7. EndShopDiscountProgram -> immediately removed from ListActiveShopDiscounts and new overlapping program now allowed!
	ended, err := svc.EndShopDiscountProgram(seller1Ctx, &promotionv1.EndShopDiscountProgramRequest{ProgramId: prog1Resp.Program.Id})
	if err != nil || ended.Program.Status != promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ENDED {
		t.Fatalf("EndShopDiscountProgram unexpected: %+v err=%v", ended, err)
	}
	pubAfterEnd, _ := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{SkuIds: []string{"sku-2"}})
	if len(pubAfterEnd.Discounts) != 0 {
		t.Fatalf("ended program should disappear from ListActiveShopDiscounts, got %+v", pubAfterEnd.Discounts)
	}
	// Updating ended program rejected
	if _, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId: prog1Resp.Program.Id, Name: strPtr("Cannot Edit Ended"),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateShopDiscountProgram on ENDED: want FailedPrecondition, got %v", err)
	}

	// 8. Legacy reservation (no shop_program_items_json) still releases cleanly
	legacyID := "legacy-no-prog-1"
	bdBytes, _ := protojson.Marshal(&promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{{ShopId: "shop-1", SubtotalVnd: 100000, FinalPayableVnd: 100000}},
	})
	if _, _, err := mem.CommitReservation(context.Background(), domain.PromotionReservation{
		OrderID:       legacyID,
		UserID:        "buyer-legacy",
		Status:        domain.ReservationStatusReserved,
		BreakdownJSON: bdBytes,
	}, now); err != nil {
		t.Fatal(err)
	}
	legRel, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: legacyID, ShopId: "shop-1"})
	if err != nil || !legRel.Released || legRel.ShopProgramQuantityReleased != 0 {
		t.Fatalf("legacy reservation release failed: %+v err=%v", legRel, err)
	}
}

func TestService_W2B_ShopDiscountProgram_UpdateListAndSyncRedis(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	pc := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a": {Id: "sku-a", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 30, IsActive: true},
			"sku-b": {Id: "sku-b", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 150000, MinPrice_30DVnd: 150000, AvailableStock: 30, IsActive: true},
			"sku-c": {Id: "sku-c", ShopId: "shop-2", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 30, IsActive: true},
		},
	}
	svc, mem, eng := setupBurstService(t)
	svc.SetProductClient(pc)
	svc.SetClock(func() time.Time { return now })

	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Create 1 SCHEDULED and 1 ACTIVE program for shop-1, plus 1 ACTIVE for shop-2
	schedResp, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "Scheduled Sale",
		StartsAt: timestamppb.New(now.Add(2 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(5 * time.Hour)),
		Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-a", DiscountedPriceVnd: 170000, PerUserLimit: 2}},
	})
	if err != nil {
		t.Fatalf("create scheduled: %v", err)
	}
	activeResp, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "Active Sale",
		StartsAt: timestamppb.New(now.Add(-1 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(1 * time.Hour)),
		Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-b", DiscountPercent: 10, PerUserLimit: 3}},
	})
	if err != nil {
		t.Fatalf("create active: %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller2Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "Shop2 Sale",
		StartsAt: timestamppb.New(now.Add(-1 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
		Items:    []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-c", DiscountPercent: 15}},
	}); err != nil {
		t.Fatalf("create shop2: %v", err)
	}

	// UpdateShopDiscountProgram: cross-shop denied, active starts_at immutable, replace items + extend scheduled succeeds
	if _, err := svc.UpdateShopDiscountProgram(seller2Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId: schedResp.Program.Id, Name: strPtr("Hijack"),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-shop UpdateShopDiscountProgram: want PermissionDenied, got %v", err)
	}
	if _, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId: activeResp.Program.Id,
		StartsAt:  timestamppb.New(now.Add(-2 * time.Hour)),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("changing starts_at of active program: want FailedPrecondition, got %v", err)
	}
	// Extending active program to overlap scheduled program on sku-a (when replacing items with sku-a) must fail AlreadyExists
	if _, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId:    activeResp.Program.Id,
		EndsAt:       timestamppb.New(now.Add(3 * time.Hour)),
		ReplaceItems: true,
		Items:        []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-a", DiscountedPriceVnd: 160000}},
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("overlapping UpdateShopDiscountProgram: want AlreadyExists, got %v", err)
	}
	// Updating scheduled program name, starts_at, ends_at, and replacing items with sku-a (new price) succeeds
	updSched, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId:    schedResp.Program.Id,
		Name:         strPtr("Scheduled Sale v2"),
		StartsAt:     timestamppb.New(now.Add(3 * time.Hour)),
		EndsAt:       timestamppb.New(now.Add(6 * time.Hour)),
		ReplaceItems: true,
		Items:        []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-a", DiscountedPriceVnd: 150000, PerUserLimit: 4}},
	})
	if err != nil || updSched.Program.Name != "Scheduled Sale v2" || len(updSched.Program.Items) != 1 || updSched.Program.Items[0].DiscountedPriceVnd != 150000 {
		t.Fatalf("UpdateShopDiscountProgram scheduled unexpected: %+v err=%v", updSched, err)
	}
	// Updating active program name & ends_at without replacing items succeeds
	updActive, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId: activeResp.Program.Id,
		Name:      strPtr("Active Sale Extended"),
		EndsAt:    timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil || updActive.Program.Name != "Active Sale Extended" {
		t.Fatalf("UpdateShopDiscountProgram active rename unexpected: %+v err=%v", updActive, err)
	}

	// ListShopDiscountPrograms: buyer denied, seller cross-shop denied, seller own shop filtered, admin sees all
	if _, err := svc.ListShopDiscountPrograms(buyerCtx, &promotionv1.ListShopDiscountProgramsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListShopDiscountPrograms: want PermissionDenied, got %v", err)
	}
	if _, err := svc.ListShopDiscountPrograms(seller1Ctx, &promotionv1.ListShopDiscountProgramsRequest{ShopId: "shop-2"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller1 cross-shop ListShopDiscountPrograms: want PermissionDenied, got %v", err)
	}
	s1All, err := svc.ListShopDiscountPrograms(seller1Ctx, &promotionv1.ListShopDiscountProgramsRequest{})
	if err != nil || s1All.Total != 2 {
		t.Fatalf("seller1 ListShopDiscountPrograms all: want 2, got %+v err=%v", s1All, err)
	}
	s1Sched, err := svc.ListShopDiscountPrograms(seller1Ctx, &promotionv1.ListShopDiscountProgramsRequest{
		Status: promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_SCHEDULED,
	})
	if err != nil || s1Sched.Total != 1 || s1Sched.Programs[0].Id != schedResp.Program.Id {
		t.Fatalf("seller1 ListShopDiscountPrograms SCHEDULED unexpected: %+v err=%v", s1Sched, err)
	}
	admActive, err := svc.ListShopDiscountPrograms(adminCtx, &promotionv1.ListShopDiscountProgramsRequest{
		Status: promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE,
	})
	if err != nil || admActive.Total != 2 {
		t.Fatalf("admin ListShopDiscountPrograms ACTIVE: want 2, got %+v err=%v", admActive, err)
	}

	// SyncRedisFromStore: rehydrates vouchers (including PAUSED/DELETED statuses) and approved flash slots
	vAct, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "SYNCACT", Name: "Sync Active", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1, BudgetVnd: 50000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
	})
	vPaused, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "SYNCPAU", Name: "Sync Paused", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
	})
	_, _ = svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: vPaused.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
	})
	fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Sync Flash", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
	})
	fi, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, SkuId: "sku-a", FlashPriceVnd: 120000, TotalSlots: 5, PerUserLimit: 1,
	})
	_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true})
	_ = eng.DeleteFlashItemSlots(context.Background(), fi.Item.Id)

	if err := svc.SyncRedisFromStore(context.Background()); err != nil {
		t.Fatalf("SyncRedisFromStore: %v", err)
	}
	if rem, ok, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi.Item.Id)); !ok || rem != 5 {
		t.Fatalf("expected SyncRedisFromStore to rehydrate flash slots=5, got %d ok=%v", rem, ok)
	}
	if _, err := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
		OrderId: "sync-paused-check", UserId: "buyer-1", PlatformVoucherId: vPaused.Voucher.Id,
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("paused voucher after SyncRedisFromStore should stay rejected, got %v", err)
	}
	if listPaused, err := svc.ListAvailableVouchers(adminCtx, &promotionv1.ListAvailableVouchersRequest{
		Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
	}); err != nil || len(listPaused.Vouchers) != 1 || listPaused.Vouchers[0].Id != vPaused.Voucher.Id {
		t.Fatalf("admin ListAvailableVouchers(PAUSED) unexpected: %+v err=%v", listPaused, err)
	}
	_ = vAct
	_ = mem
}

func TestService_W2B_ValidationAndEdgeCases(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	pc := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-ok":   {Id: "sku-ok", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
			"sku-in":   {Id: "sku-in", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: false},
			"sku-zero": {Id: "sku-zero", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 0, AvailableStock: 20, IsActive: true},
		},
	}
	svc, _, _ := setupBurstService(t)
	svc.SetProductClient(pc)
	svc.SetClock(func() time.Time { return now })

	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	sellerNoShop := ctxWithRole("seller-x", "seller", "")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	anonCtx := context.Background()

	// Voucher update/status/delete invalid args, NotFound, and unused field edits (percent switch + category update)
	if _, err := svc.UpdateVoucher(anonCtx, &promotionv1.UpdateVoucherRequest{VoucherId: "v1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon UpdateVoucher: want Unauthenticated, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty UpdateVoucher: want InvalidArgument, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{VoucherId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing UpdateVoucher: want NotFound, got %v", err)
	}
	if _, err := svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty SetVoucherStatus: want InvalidArgument, got %v", err)
	}
	if _, err := svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{VoucherId: "v1", Status: promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("DELETED via SetVoucherStatus: want InvalidArgument, got %v", err)
	}
	if _, err := svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{VoucherId: "missing", Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing SetVoucherStatus: want NotFound, got %v", err)
	}
	if _, err := svc.DeleteVoucher(adminCtx, &promotionv1.DeleteVoucherRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty DeleteVoucher: want InvalidArgument, got %v", err)
	}
	if _, err := svc.DeleteVoucher(adminCtx, &promotionv1.DeleteVoucherRequest{VoucherId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing DeleteVoucher: want NotFound, got %v", err)
	}

	// Create unused platform voucher, switch FIXED -> PERCENT -> FIXED, test category_ids replacement and invalid bounds
	pv, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "EDGE1", Name: "Edge", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		CategoryIds: []string{"cat-1"},
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	pctType := promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT
	fixType := promotionv1.DiscountType_DISCOUNT_TYPE_FIXED
	pctVal := int32(15)
	maxDisc := int64(30000)
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: pv.Voucher.Id, DiscountType: &pctType, DiscountPercent: &pctVal, MaxDiscountVnd: &maxDisc,
		CategoryIds: []string{"cat-2", "cat-2"}, ReplaceCategoryIds: true,
	}); err != nil {
		t.Fatalf("UpdateVoucher unused switch to PERCENT: %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: pv.Voucher.Id, DiscountType: &fixType, ValueVnd: i64Ptr(15000),
		ReplaceCategoryIds: true,
	}); err != nil {
		t.Fatalf("UpdateVoucher unused switch back to FIXED: %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: pv.Voucher.Id, Name: strPtr("   "),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("blank name UpdateVoucher: want InvalidArgument, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: pv.Voucher.Id, Quota: i32Ptr(0),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("zero quota UpdateVoucher: want InvalidArgument, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: pv.Voucher.Id, PerUserLimit: i32Ptr(0),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("zero per_user_limit UpdateVoucher: want InvalidArgument, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: pv.Voucher.Id, StartsAt: timestamppb.New(now.Add(48 * time.Hour)),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("starts_at >= ends_at UpdateVoucher: want InvalidArgument, got %v", err)
	}

	// Flash sale validation edge cases (inactive SKU, zero-price SKU, missing SKU, bad status filter, seller no shop)
	fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name: "Edge Flash", StartsAt: timestamppb.New(now.Add(time.Hour)), EndsAt: timestamppb.New(now.Add(3 * time.Hour)),
	})
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, SkuId: "sku-missing", FlashPriceVnd: 100000, TotalSlots: 1,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing SKU flash: want NotFound, got %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, SkuId: "sku-in", FlashPriceVnd: 100000, TotalSlots: 1,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("inactive SKU flash: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, SkuId: "sku-zero", FlashPriceVnd: 100000, TotalSlots: 1,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("zero-price SKU flash: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.ListFlashSaleItems(buyerCtx, &promotionv1.ListFlashSaleItemsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListFlashSaleItems: want PermissionDenied, got %v", err)
	}
	if _, err := svc.ListFlashSaleItems(sellerNoShop, &promotionv1.ListFlashSaleItemsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller without shop ListFlashSaleItems: want PermissionDenied, got %v", err)
	}
	if _, err := svc.ListAllFlashSales(buyerCtx, &promotionv1.ListAllFlashSalesRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListAllFlashSales: want PermissionDenied, got %v", err)
	}
	if _, err := svc.ListAllFlashSales(adminCtx, &promotionv1.ListAllFlashSalesRequest{Status: "BOGUS"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid status ListAllFlashSales: want InvalidArgument, got %v", err)
	}

	// Shop discount program validation edge cases
	if _, err := svc.CreateShopDiscountProgram(buyerCtx, &promotionv1.CreateShopDiscountProgramRequest{Name: "x"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer CreateShopDiscountProgram: want PermissionDenied, got %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty name CreateShopDiscountProgram: want InvalidArgument, got %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "NoItems", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty items CreateShopDiscountProgram: want InvalidArgument, got %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "Past", StartsAt: timestamppb.New(now.Add(-2 * time.Hour)), EndsAt: timestamppb.New(now.Add(-time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-ok", DiscountPercent: 10}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("past ends_at CreateShopDiscountProgram: want InvalidArgument, got %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "BothPriceAndPct", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-ok", DiscountedPriceVnd: 150000, DiscountPercent: 10}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("both price and percent: want InvalidArgument, got %v", err)
	}
	if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "DupSKU", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{
			{SkuId: "sku-ok", DiscountPercent: 10},
			{SkuId: "sku-ok", DiscountPercent: 20},
		},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("duplicate SKU: want InvalidArgument, got %v", err)
	}
	if _, err := svc.EndShopDiscountProgram(seller1Ctx, &promotionv1.EndShopDiscountProgramRequest{ProgramId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing EndShopDiscountProgram: want NotFound, got %v", err)
	}
	if _, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{ProgramId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing UpdateShopDiscountProgram: want NotFound, got %v", err)
	}
}

type coinHookStore struct {
	store.Store
	onGetCoinBalance func(call int)
	calls            atomic.Int32
}

func (c *coinHookStore) GetCoinBalance(ctx context.Context, userID string, recentLimit int) (int64, []domain.CoinTransaction, error) {
	n := int(c.calls.Add(1))
	if c.onGetCoinBalance != nil {
		c.onGetCoinBalance(n)
	}
	return c.Store.GetCoinBalance(ctx, userID, recentLimit)
}

func TestService_W2B_ShopProgramPerUserLimit_PreviewEqualsReserve_MultiLine_Race_AndReplay(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	pc := &fakeProductClient{
		skus: map[string]*productv1.SKU{
			"sku-a": {Id: "sku-a", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true},
			"sku-b": {Id: "sku-b", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 50, IsActive: true},
		},
	}
	svc, mem, eng := setupBurstService(t)
	svc.SetProductClient(pc)
	svc.SetClock(func() time.Time { return now })

	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-limit", "buyer", "")
	svcCtx := ctxWithService("order-service")

	progResp, err := svc.CreateShopDiscountProgram(sellerCtx, &promotionv1.CreateShopDiscountProgramRequest{
		Name:     "PerUser Limit Program",
		StartsAt: timestamppb.New(now.Add(-time.Hour)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{
			{SkuId: "sku-a", DiscountedPriceVnd: 160000, PerUserLimit: 2},
			{SkuId: "sku-b", DiscountedPriceVnd: 80000, PerUserLimit: 2},
		},
	})
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram: %v", err)
	}
	var itemA, itemB *promotionv1.ShopDiscountProgramItem
	for _, it := range progResp.Program.Items {
		if it.SkuId == "sku-a" {
			itemA = it
		}
		if it.SkuId == "sku-b" {
			itemB = it
		}
	}

	// 1. Order 1 uses full per_user_limit (qty=2) on sku-a
	ord1ShopItems := []*promotionv1.ShopDiscountInput{
		{
			ShopId:         "shop-1",
			ShippingFeeVnd: 20000,
			Items: []*promotionv1.DiscountLineItem{
				{SkuId: "sku-a", Quantity: 2, UnitPriceVnd: 200000, ShopDiscountProgramItemId: itemA.Id},
			},
		},
	}
	res1, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "ord-limit-1",
		UserId:    "buyer-limit",
		ShopItems: ord1ShopItems,
		ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
			{ShopDiscountProgramItemId: itemA.Id, SkuId: "sku-a", Quantity: 2, ShopId: "shop-1"},
		},
	})
	if err != nil || !res1.Reserved || res1.Breakdown.TotalShopProgramDiscountVnd != 80000 {
		t.Fatalf("ord-limit-1 unexpected: resp=%+v err=%v", res1, err)
	}
	if ids := res1.Breakdown.Shops[0].GetAppliedShopProgramItemIds(); len(ids) != 1 || ids[0] != itemA.Id {
		t.Fatalf("ord-limit-1 applied_shop_program_item_ids: got %v want [%s]", ids, itemA.Id)
	}
	if uA, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(itemA.Id, "buyer-limit")); uA != 2 {
		t.Fatalf("expected itemA counter=2, got %d", uA)
	}

	// 2. Order 2 exceeds per_user_limit on sku-a -> Preview and Reserve MUST match (regular price, warning, empty applied IDs, Redis unchanged)
	ord2ShopItems := []*promotionv1.ShopDiscountInput{
		{
			ShopId:         "shop-1",
			ShippingFeeVnd: 20000,
			Items: []*promotionv1.DiscountLineItem{
				{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: itemA.Id},
			},
		},
	}
	prev2, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{ShopItems: ord2ShopItems})
	if err != nil {
		t.Fatalf("PreviewDiscounts ord-limit-2: %v", err)
	}
	res2, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "ord-limit-2",
		UserId:    "buyer-limit",
		ShopItems: ord2ShopItems,
		// Pass explicit itemA too (as order service does) -> MUST NOT be reserved because calculator excluded it!
		ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
			{ShopDiscountProgramItemId: itemA.Id, SkuId: "sku-a", Quantity: 1, ShopId: "shop-1"},
		},
	})
	if err != nil || !res2.Reserved {
		t.Fatalf("ReservePromotion ord-limit-2 should succeed at regular price, got resp=%+v err=%v", res2, err)
	}
	if res2.Breakdown.TotalShopProgramDiscountVnd != 0 || res2.Breakdown.FinalPayableVnd != 220000 {
		t.Fatalf("ord-limit-2 expected regular price payable=220000 disc=0, got %+v", res2.Breakdown)
	}
	if res2.Breakdown.FinalPayableVnd != prev2.Breakdown.FinalPayableVnd ||
		res2.Breakdown.TotalShopProgramDiscountVnd != prev2.Breakdown.TotalShopProgramDiscountVnd ||
		len(res2.Breakdown.Warnings) == 0 ||
		len(res2.Breakdown.Warnings) != len(prev2.Breakdown.Warnings) ||
		res2.Breakdown.Warnings[0] != prev2.Breakdown.Warnings[0] {
		t.Fatalf("Preview vs Reserve mismatch: prev=%+v res=%+v", prev2.Breakdown, res2.Breakdown)
	}
	if ids := res2.Breakdown.Shops[0].GetAppliedShopProgramItemIds(); len(ids) != 0 {
		t.Fatalf("ord-limit-2 applied_shop_program_item_ids should be empty, got %v", ids)
	}
	if uA, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(itemA.Id, "buyer-limit")); uA != 2 {
		t.Fatalf("Redis counter for itemA MUST stay 2 (not incremented), got %d", uA)
	}
	savedOrd2, err := mem.GetReservation(context.Background(), "ord-limit-2")
	if err != nil || len(savedOrd2.ShopProgramItems) != 0 {
		t.Fatalf("ord-limit-2 stored reservation must have 0 ShopProgramItems, got %+v err=%v", savedOrd2, err)
	}

	// 3. Multi-line same shop: sku-a over limit (regular price) + sku-b within limit (discounted & reserved)
	multiShopItems := []*promotionv1.ShopDiscountInput{
		{
			ShopId:         "shop-1",
			ShippingFeeVnd: 20000,
			Items: []*promotionv1.DiscountLineItem{
				{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: itemA.Id},
				{SkuId: "sku-b", Quantity: 1, UnitPriceVnd: 100000, ShopDiscountProgramItemId: itemB.Id},
			},
		},
	}
	prevMulti, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{ShopItems: multiShopItems})
	if err != nil {
		t.Fatalf("PreviewDiscounts multi: %v", err)
	}
	resMulti, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "ord-limit-multi",
		UserId:    "buyer-limit",
		ShopItems: multiShopItems,
		ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
			{ShopDiscountProgramItemId: itemA.Id, SkuId: "sku-a", Quantity: 1, ShopId: "shop-1"},
			{ShopDiscountProgramItemId: itemB.Id, SkuId: "sku-b", Quantity: 1, ShopId: "shop-1"},
		},
	})
	if err != nil || !resMulti.Reserved {
		t.Fatalf("ReservePromotion multi: resp=%+v err=%v", resMulti, err)
	}
	if resMulti.Breakdown.TotalShopProgramDiscountVnd != 20000 ||
		resMulti.Breakdown.FinalPayableVnd != 300000 ||
		resMulti.Breakdown.FinalPayableVnd != prevMulti.Breakdown.FinalPayableVnd {
		t.Fatalf("multi breakdown mismatch: prev=%+v res=%+v", prevMulti.Breakdown, resMulti.Breakdown)
	}
	if ids := resMulti.Breakdown.Shops[0].GetAppliedShopProgramItemIds(); len(ids) != 1 || ids[0] != itemB.Id {
		t.Fatalf("multi applied_shop_program_item_ids: got %v want [%s]", ids, itemB.Id)
	}
	if uA, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(itemA.Id, "buyer-limit")); uA != 2 {
		t.Fatalf("itemA counter must remain 2, got %d", uA)
	}
	if uB, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(itemB.Id, "buyer-limit")); uB != 1 {
		t.Fatalf("itemB counter must be 1, got %d", uB)
	}
	relMulti, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: "ord-limit-multi", ShopId: "shop-1"})
	if err != nil || relMulti.ShopProgramQuantityReleased != 1 {
		t.Fatalf("ReleasePromotionShop multi: resp=%+v err=%v", relMulti, err)
	}
	if uA, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(itemA.Id, "buyer-limit")); uA != 2 {
		t.Fatalf("itemA counter must remain 2 after releasing multi, got %d", uA)
	}
	if uB, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(itemB.Id, "buyer-limit")); uB != 0 {
		t.Fatalf("itemB counter must return to 0 after release, got %d", uB)
	}

	// 4. Concurrent race of 2 orders for fresh buyer at exact per_user_limit=2 (each qty=2):
	// BOTH succeed; exactly 1 gets discount + applied ID, 1 gets regular price + warning; Redis counter == 2!
	var (
		wg          sync.WaitGroup
		startCh     = make(chan struct{})
		raceResps   [2]*promotionv1.ReservePromotionResponse
		raceErrs    [2]error
		discountedN int
		regularN    int
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			raceResps[idx], raceErrs[idx] = svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: fmt.Sprintf("ord-race-exact-%d", idx),
				UserId:  "buyer-race-exact",
				ShopItems: []*promotionv1.ShopDiscountInput{
					{
						ShopId:         "shop-1",
						ShippingFeeVnd: 20000,
						Items: []*promotionv1.DiscountLineItem{
							{SkuId: "sku-a", Quantity: 2, UnitPriceVnd: 200000, ShopDiscountProgramItemId: itemA.Id},
						},
					},
				},
				ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
					{ShopDiscountProgramItemId: itemA.Id, SkuId: "sku-a", Quantity: 2, ShopId: "shop-1"},
				},
			})
		}(i)
	}
	close(startCh)
	wg.Wait()
	for i := 0; i < 2; i++ {
		if raceErrs[i] != nil || !raceResps[i].Reserved {
			t.Fatalf("race order %d failed: resp=%+v err=%v", i, raceResps[i], raceErrs[i])
		}
		ids := raceResps[i].Breakdown.Shops[0].GetAppliedShopProgramItemIds()
		if raceResps[i].Breakdown.TotalShopProgramDiscountVnd == 80000 && len(ids) == 1 && ids[0] == itemA.Id {
			discountedN++
		} else if raceResps[i].Breakdown.TotalShopProgramDiscountVnd == 0 && len(ids) == 0 && len(raceResps[i].Breakdown.Warnings) > 0 {
			regularN++
		} else {
			t.Fatalf("race order %d unexpected breakdown: %+v", i, raceResps[i].Breakdown)
		}
	}
	if discountedN != 1 || regularN != 1 {
		t.Fatalf("race expected 1 discounted + 1 regular, got discounted=%d regular=%d", discountedN, regularN)
	}
	if uRace, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(itemA.Id, "buyer-race-exact")); uRace != 2 {
		t.Fatalf("race Redis counter must be exactly 2, got %d", uRace)
	}

	// 5. Deterministic Lua SHOP_PROG_USER_LIMIT_EXCEEDED race retry:
	// (a) Attempt 0 hits Lua SHOP_PROG_USER_LIMIT_EXCEEDED -> recomputes once -> Attempt 1 succeeds at regular price!
	// (b) Attempt 0 hits race on itemA AND Attempt 1 hits race on itemB -> returns FailedPrecondition!
	t.Run("DeterministicLuaRaceRetry", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		baseMem := store.NewMemoryStore()
		rEng := lua.NewEngine(rdb)
		hooked := &coinHookStore{Store: baseMem}
		rSvc := service.NewPromotionService(hooked, rEng, logger.New("race-retry-test", logger.WithWriter(io.Discard)))
		rSvc.SetProductClient(pc)
		rSvc.SetClock(func() time.Time { return now })

		pResp, err := rSvc.CreateShopDiscountProgram(sellerCtx, &promotionv1.CreateShopDiscountProgramRequest{
			Name:     "Hook Race Prog",
			StartsAt: timestamppb.New(now.Add(-time.Hour)),
			EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{
				{SkuId: "sku-a", DiscountedPriceVnd: 160000, PerUserLimit: 2},
				{SkuId: "sku-b", DiscountedPriceVnd: 80000, PerUserLimit: 2},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		var rItemA, rItemB string
		for _, it := range pResp.Program.Items {
			if it.SkuId == "sku-a" {
				rItemA = it.Id
			}
			if it.SkuId == "sku-b" {
				rItemB = it.Id
			}
		}
		_, _, _ = baseMem.CreditCoins(context.Background(), "buyer-hook-1", 1000, "seed", "ref-1")
		_, _, _ = baseMem.CreditCoins(context.Background(), "buyer-hook-2", 1000, "seed", "ref-2")

		// (a) Inject race between MGetInt64 and Reserve on attempt 0 -> retry succeeds at regular price
		hooked.calls.Store(0)
		hooked.onGetCoinBalance = func(call int) {
			if call == 1 {
				_, _, _ = baseMem.CommitReservation(context.Background(), domain.PromotionReservation{
					OrderID: "ord-hook-comp-1", UserID: "buyer-hook-1",
					ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: rItemA, SKUID: "sku-a", Quantity: 2, ShopID: "shop-1"}},
				}, now)
				_ = rdb.Set(context.Background(), lua.ShopProgramUserKey(rItemA, "buyer-hook-1"), 2, 0).Err()
			}
		}
		retryResp, err := rSvc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:    "ord-hook-retry-ok",
			UserId:     "buyer-hook-1",
			CoinsToUse: 500,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{
					ShopId: "shop-1", ShippingFeeVnd: 20000,
					Items: []*promotionv1.DiscountLineItem{
						{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: rItemA},
					},
				},
			},
		})
		if err != nil || !retryResp.Reserved {
			t.Fatalf("deterministic race retry should succeed on attempt 1, got resp=%+v err=%v", retryResp, err)
		}
		if hooked.calls.Load() != 2 {
			t.Fatalf("expected exactly 2 calculation attempts (initial + 1 retry), got %d", hooked.calls.Load())
		}
		if retryResp.Breakdown.TotalShopProgramDiscountVnd != 0 ||
			len(retryResp.Breakdown.Shops[0].GetAppliedShopProgramItemIds()) != 0 ||
			len(retryResp.Breakdown.Warnings) == 0 {
			t.Fatalf("retry breakdown unexpected: %+v", retryResp.Breakdown)
		}
		if uVal, _, _ := rEng.GetInt64(context.Background(), lua.ShopProgramUserKey(rItemA, "buyer-hook-1")); uVal != 2 {
			t.Fatalf("Redis counter must stay 2, got %d", uVal)
		}

		// (b) Inject race on BOTH attempt 0 (itemA) and attempt 1 (itemB): with 2 items, maxAttempts=3 converges cleanly!
		hooked.calls.Store(0)
		hooked.onGetCoinBalance = func(call int) {
			if call == 1 {
				_, _, _ = baseMem.CommitReservation(context.Background(), domain.PromotionReservation{
					OrderID: "ord-hook-comp-2a", UserID: "buyer-hook-2",
					ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: rItemA, SKUID: "sku-a", Quantity: 2, ShopID: "shop-1"}},
				}, now)
				_ = rdb.Set(context.Background(), lua.ShopProgramUserKey(rItemA, "buyer-hook-2"), 2, 0).Err()
			} else if call == 2 {
				_, _, _ = baseMem.CommitReservation(context.Background(), domain.PromotionReservation{
					OrderID: "ord-hook-comp-2b", UserID: "buyer-hook-2",
					ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: rItemB, SKUID: "sku-b", Quantity: 2, ShopID: "shop-1"}},
				}, now)
				_ = rdb.Set(context.Background(), lua.ShopProgramUserKey(rItemB, "buyer-hook-2"), 2, 0).Err()
			}
		}
		convResp, err := rSvc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:    "ord-hook-retry-converge",
			UserId:     "buyer-hook-2",
			CoinsToUse: 500,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{
					ShopId: "shop-1", ShippingFeeVnd: 20000,
					Items: []*promotionv1.DiscountLineItem{
						{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: rItemA},
						{SkuId: "sku-b", Quantity: 1, UnitPriceVnd: 100000, ShopDiscountProgramItemId: rItemB},
					},
				},
			},
		})
		if err != nil || !convResp.Reserved || convResp.Breakdown.TotalShopProgramDiscountVnd != 0 {
			t.Fatalf("2-item race should converge on attempt 3 at regular price, got resp=%+v err=%v", convResp, err)
		}
		if hooked.calls.Load() != 3 {
			t.Fatalf("expected 3 attempts to converge 2-item race, got %d", hooked.calls.Load())
		}
	})

	// 6. Strict error on invalid shop program item (non-existent / ended / wrong SKU) STILL fails!
	if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "ord-invalid-prog-missing",
		UserId:  "buyer-limit",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1", ShippingFeeVnd: 20000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: "does-not-exist"},
				},
			},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("missing shop program item strict: want FailedPrecondition, got %v", err)
	}

	// 7. End program, then verify idempotent replay of ord-limit-1 & ord-limit-2 STILL returns saved breakdown & applied IDs!
	if _, err := svc.EndShopDiscountProgram(sellerCtx, &promotionv1.EndShopDiscountProgramRequest{ProgramId: progResp.Program.Id}); err != nil {
		t.Fatalf("EndShopDiscountProgram: %v", err)
	}
	replay1, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "ord-limit-1",
		UserId:    "buyer-limit",
		ShopItems: ord1ShopItems,
	})
	if err != nil || !replay1.Reserved || !replay1.AlreadyReserved || replay1.Breakdown.TotalShopProgramDiscountVnd != 80000 {
		t.Fatalf("replay ord-limit-1 unexpected: resp=%+v err=%v", replay1, err)
	}
	if ids := replay1.Breakdown.Shops[0].GetAppliedShopProgramItemIds(); len(ids) != 1 || ids[0] != itemA.Id {
		t.Fatalf("replay ord-limit-1 lost applied_shop_program_item_ids: got %v want [%s]", ids, itemA.Id)
	}
	replay2, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "ord-limit-2",
		UserId:    "buyer-limit",
		ShopItems: ord2ShopItems,
	})
	if err != nil || !replay2.Reserved || !replay2.AlreadyReserved ||
		replay2.Breakdown.TotalShopProgramDiscountVnd != 0 ||
		len(replay2.Breakdown.Shops[0].GetAppliedShopProgramItemIds()) != 0 ||
		len(replay2.Breakdown.Warnings) == 0 {
		t.Fatalf("replay ord-limit-2 unexpected: resp=%+v err=%v", replay2, err)
	}
}

func TestService_W2B_ListActiveShopDiscounts_ValidationAndLimit500(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc, mem, _ := setupBurstService(t)
	svc.SetClock(func() time.Time { return now })

	// 1. Neither sku_ids nor shop_id -> InvalidArgument
	if _, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty ListActiveShopDiscounts: want InvalidArgument, got %v", err)
	}
	if _, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{
		SkuIds: []string{"  ", ""}, ShopId: "   ",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("blank ListActiveShopDiscounts: want InvalidArgument, got %v", err)
	}

	// 2. > 200 sku_ids -> InvalidArgument; exactly 200 sku_ids -> OK
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("sku-%03d", i)
	}
	if _, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{
		SkuIds: tooMany,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("201 sku_ids: want InvalidArgument, got %v", err)
	}
	if _, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{
		SkuIds: tooMany[:200],
	}); err != nil {
		t.Fatalf("200 sku_ids should succeed, got %v", err)
	}

	// 3. > 500 active items in a shop -> capped at 500 rows
	items := make([]domain.ShopDiscountProgramItem, 520)
	for i := range items {
		items[i] = domain.ShopDiscountProgramItem{
			ShopID:             "shop-big",
			SKUID:              fmt.Sprintf("big-sku-%04d", i),
			OriginalPriceVND:   100000,
			DiscountedPriceVND: 90000,
			DiscountPercent:    10,
		}
	}
	if _, err := mem.CreateShopDiscountProgram(context.Background(), domain.ShopDiscountProgram{
		ShopID:   "shop-big",
		Name:     "Big Catalog Sale",
		StartsAt: now.Add(-time.Hour),
		EndsAt:   now.Add(2 * time.Hour),
		Items:    items,
	}, now); err != nil {
		t.Fatalf("seed 520 items: %v", err)
	}
	resp, err := svc.ListActiveShopDiscounts(context.Background(), &promotionv1.ListActiveShopDiscountsRequest{
		ShopId: "shop-big",
	})
	if err != nil {
		t.Fatalf("ListActiveShopDiscounts(shop-big): %v", err)
	}
	if len(resp.Discounts) != 500 {
		t.Fatalf("expected LIMIT 500 rows, got %d", len(resp.Discounts))
	}
}

type preCommitHookStore struct {
	store.Store
	onBeforeCommit func(res domain.PromotionReservation)
}

func (p *preCommitHookStore) CommitReservation(ctx context.Context, res domain.PromotionReservation, now time.Time) (*domain.PromotionReservation, bool, error) {
	if p.onBeforeCommit != nil {
		p.onBeforeCommit(res)
	}
	return p.Store.CommitReservation(ctx, res, now)
}

func TestService_R3_AuditFixes_A_D_E_F_G_H_I_J_K_L(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerCtx := ctxWithRole("buyer-r3", "buyer", "")
	svcCtx := ctxWithService("order-service")

	// A. Service-level CommitReservation gate failures roll back and compensate Redis!
	t.Run("A_CommitGateFailureCompensatesRedis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		mem := store.NewMemoryStore()
		eng := lua.NewEngine(rdb)
		hook := &preCommitHookStore{Store: mem}
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-a": {Id: "sku-a", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
			},
		}
		svc := service.NewPromotionService(hook, eng, logger.New("r3-a", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		vResp, _ := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "R3GATEV", Name: "GateV", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		progResp, _ := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R3 Gate Prog", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-a", DiscountedPriceVnd: 160000, PerUserLimit: 3}},
		})
		progItemID := progResp.Program.Items[0].Id

		// (1) Release arrives BEFORE CommitReservation -> tombstone rejects late commit & compensates Redis!
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			_, _ = mem.ReleaseReservation(context.Background(), res.OrderID)
		}
		_, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:        "r3-a-tomb",
			UserId:         "buyer-r3",
			ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: progItemID}}},
			},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("tombstone race: want FailedPrecondition, got %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id)); rem != 5 {
			t.Fatalf("Redis voucher quota not compensated after tombstone reject: got %d want 5", rem)
		}
		if pu, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(progItemID, "buyer-r3")); pu != 0 {
			t.Fatalf("Redis shop program counter not compensated after tombstone reject: got %d want 0", pu)
		}

		// (2) Voucher PAUSED between Lua and CommitReservation -> fails FailedPrecondition & compensates Redis!
		hook.onBeforeCommit = func(_ domain.PromotionReservation) {
			_, _ = mem.SetVoucherStatus(context.Background(), vResp.Voucher.Id, domain.VoucherStatusPaused, nil)
		}
		_, err = svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:        "r3-a-paused",
			UserId:         "buyer-r3",
			ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 20000}},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("mid-flight PAUSED voucher: want FailedPrecondition, got %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id)); rem != 5 {
			t.Fatalf("Redis voucher quota not compensated after PAUSED reject: got %d want 5", rem)
		}

		// (3) Shop program ENDED between Lua and CommitReservation -> fails FailedPrecondition & compensates Redis!
		hook.onBeforeCommit = func(_ domain.PromotionReservation) {
			_, _ = mem.EndShopDiscountProgram(context.Background(), progResp.Program.Id, now, nil)
		}
		_, err = svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r3-a-ended-prog",
			UserId:  "buyer-r3",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: progItemID}}},
			},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("mid-flight ENDED shop program: want FailedPrecondition, got %v", err)
		}
		if pu, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(progItemID, "buyer-r3")); pu != 0 {
			t.Fatalf("Redis shop program counter not compensated after ENDED reject: got %d want 0", pu)
		}
	})

	// D. When shop_items is present, flash slots MUST be derived from shop_items (rejecting mismatched flash_sale_items!)
	t.Run("D_FlashDerivedFromShopItems_RejectsMismatch", func(t *testing.T) {
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-f": {Id: "sku-f", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
			},
		}
		svc, _, eng := setupBurstService(t)
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R3 Flash D", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		fi, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, SkuId: "sku-f", FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 10,
		})
		_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true})

		// Attack: FlashSaleItems claims qty=1 while ShopItems has qty=5 -> MUST fail InvalidArgument & deduct 0 slots!
		_, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:        "r3-d-mismatch",
			UserId:         "buyer-r3",
			FlashSaleItems: []*promotionv1.FlashSaleReservationItem{{FlashSaleItemId: fi.Item.Id, SkuId: "sku-f", Quantity: 1, ShopId: "shop-1"}},
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-f", Quantity: 5, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("mismatched FlashSaleItems vs ShopItems: want InvalidArgument, got %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi.Item.Id)); rem != 10 {
			t.Fatalf("mismatch attack must leave 10 slots intact, got %d", rem)
		}

		// Valid: ShopItems alone (qty=3, no explicit FlashSaleItems) -> deducts 3 slots!
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r3-d-shop-only",
			UserId:  "buyer-r3",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-f", Quantity: 3, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		}); err != nil {
			t.Fatalf("ShopItems-derived flash reserve failed: %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi.Item.Id)); rem != 7 {
			t.Fatalf("expected 7 remaining slots after qty=3, got %d", rem)
		}
	})

	// E. Flash item state machine: idempotent re-approve ignores reduced stock & preserves Redis; APPROVED<->REJECTED forbidden
	t.Run("E_FlashStateMachineAndIdempotentApprove", func(t *testing.T) {
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-e1": {Id: "sku-e1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 10, IsActive: true},
				"sku-e2": {Id: "sku-e2", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 10, IsActive: true},
			},
		}
		svc, _, eng := setupBurstService(t)
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R3 Flash E", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		fi1, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, SkuId: "sku-e1", FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 5,
		})
		fi2, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, SkuId: "sku-e2", FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 5,
		})

		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi1.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		// Reserve 3 slots (7 remaining) AND drop SKU stock below total_slots (simulate sales)
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r3-e-res",
			UserId:  "buyer-r3",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-e1", Quantity: 3, UnitPriceVnd: 200000, FlashSaleItemId: fi1.Item.Id}}},
			},
		}); err != nil {
			t.Fatal(err)
		}
		pc.setSKU(&productv1.SKU{Id: "sku-e1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 2, IsActive: true})

		// Idempotent re-approve MUST succeed without stock check & keep Redis remaining=7!
		reApp, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi1.Item.Id, Approve: true})
		if err != nil || reApp.Item.RemainingSlots != 7 {
			t.Fatalf("idempotent re-approve failed: resp=%+v err=%v", reApp, err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi1.Item.Id)); rem != 7 {
			t.Fatalf("idempotent re-approve overwrote Redis slots: got %d want 7", rem)
		}
		// APPROVED -> REJECTED forbidden
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi1.Item.Id, Approve: false, Reason: "no"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("APPROVED->REJECTED: want FailedPrecondition, got %v", err)
		}
		// PENDING -> REJECTED -> idempotent REJECTED OK -> REJECTED -> APPROVED forbidden
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi2.Item.Id, Approve: false, Reason: "bad"}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi2.Item.Id, Approve: false, Reason: "bad"}); err != nil {
			t.Fatalf("idempotent REJECTED->REJECTED failed: %v", err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi2.Item.Id, Approve: true}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("REJECTED->APPROVED: want FailedPrecondition, got %v", err)
		}
	})

	// F. PreviewDiscounts with sold-out flash (0 remaining OR missing Redis key) warns & uses regular price
	t.Run("F_PreviewSoldOutOrMissingFlashKey", func(t *testing.T) {
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-f": {Id: "sku-f", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 5, IsActive: true},
			},
		}
		svc, _, eng := setupBurstService(t)
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R3 Flash F", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		fi, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, SkuId: "sku-f", FlashPriceVnd: 120000, TotalSlots: 1, PerUserLimit: 5,
		})
		_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true})

		// Exhaust the single slot
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r3-f-take",
			UserId:  "other-buyer",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-f", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		}); err != nil {
			t.Fatal(err)
		}
		prevSold, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-f", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		})
		if err != nil || prevSold.Breakdown.TotalFlashSaleDiscountVnd != 0 || len(prevSold.Breakdown.Warnings) == 0 {
			t.Fatalf("Preview sold-out flash unexpected: %+v err=%v", prevSold, err)
		}

		// Delete Redis slot key entirely -> missing key also treated as 0 remaining in Preview!
		_ = eng.DeleteFlashItemSlots(context.Background(), fi.Item.Id)
		prevMissing, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-f", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		})
		if err != nil || prevMissing.Breakdown.TotalFlashSaleDiscountVnd != 0 || len(prevMissing.Breakdown.Warnings) == 0 {
			t.Fatalf("Preview missing Redis flash key unexpected: %+v err=%v", prevMissing, err)
		}
	})

	// G. Remove & re-add SKU in UpdateShopDiscountProgram preserves stable UUIDv5 ID & per_user_limit!
	t.Run("G_StableShopProgramItemIDAcrossRemoveReadd", func(t *testing.T) {
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-g1": {Id: "sku-g1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
				"sku-g2": {Id: "sku-g2", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true},
			},
		}
		svc, _, _ := setupBurstService(t)
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		prog, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R3 Stable Prog", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-g1", DiscountedPriceVnd: 150000, PerUserLimit: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		id1 := prog.Program.Items[0].Id

		// Buyer uses 1/1 limit
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r3-g-use1",
			UserId:  "buyer-r3",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-g1", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: id1}}},
			},
		}); err != nil {
			t.Fatal(err)
		}

		// Seller removes sku-g1 (replaces with sku-g2) then re-adds sku-g1
		if _, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
			ProgramId: prog.Program.Id, ReplaceItems: true,
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-g2", DiscountedPriceVnd: 80000, PerUserLimit: 1}},
		}); err != nil {
			t.Fatal(err)
		}
		reAdded, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
			ProgramId: prog.Program.Id, ReplaceItems: true,
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-g1", DiscountedPriceVnd: 150000, PerUserLimit: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		id2 := reAdded.Program.Items[0].Id
		if id2 != id1 {
			t.Fatalf("expected stable UUIDv5 item ID %s after remove+readd, got %s", id1, id2)
		}

		// Buyer MUST STILL be over per_user_limit=1 -> regular price, 0 discount!
		res2, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r3-g-use2",
			UserId:  "buyer-r3",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-g1", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: id2}}},
			},
		})
		if err != nil || res2.Breakdown.TotalShopProgramDiscountVnd != 0 || len(res2.Breakdown.Shops[0].GetAppliedShopProgramItemIds()) != 0 {
			t.Fatalf("per_user_limit reset after remove+readd! resp=%+v err=%v", res2, err)
		}
	})

	// H. Multi-shop 3-item race converges cleanly without failing checkout
	t.Run("H_MultiShop3ItemRaceConverges", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		mem := store.NewMemoryStore()
		eng := lua.NewEngine(rdb)
		hooked := &coinHookStore{Store: mem}
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-h1": {Id: "sku-h1", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
				"sku-h2": {Id: "sku-h2", ShopId: "shop-2", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
				"sku-h3": {Id: "sku-h3", ShopId: "shop-3", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
			},
		}
		svc := service.NewPromotionService(hooked, eng, logger.New("r3-h", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		var ids [3]string
		for i, shop := range []string{"shop-1", "shop-2", "shop-3"} {
			p, err := svc.CreateShopDiscountProgram(ctxWithRole(fmt.Sprintf("s-%d", i), "seller", shop), &promotionv1.CreateShopDiscountProgramRequest{
				Name: "Prog " + shop, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
				Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: fmt.Sprintf("sku-h%d", i+1), DiscountedPriceVnd: 150000, PerUserLimit: 1}},
			})
			if err != nil {
				t.Fatal(err)
			}
			ids[i] = p.Program.Items[0].Id
		}
		_, _, _ = mem.CreditCoins(context.Background(), "buyer-h", 1000, "seed", "seed-h")

		// Inject 3 sequential races (call 1 hits shop-1, call 2 hits shop-2, call 3 hits shop-3) -> attempt 4 converges!
		hooked.onGetCoinBalance = func(call int) {
			if call >= 1 && call <= 3 {
				_, _, _ = mem.CommitReservation(context.Background(), domain.PromotionReservation{
					OrderID: fmt.Sprintf("r3-h-comp-%d", call), UserID: "buyer-h",
					ShopProgramItems: []domain.ReservedShopProgramItem{{
						ShopDiscountProgramItemID: ids[call-1],
						SKUID:                     fmt.Sprintf("sku-h%d", call),
						Quantity:                  1,
						ShopID:                    fmt.Sprintf("shop-%d", call),
					}},
				}, now)
				_ = rdb.Set(context.Background(), lua.ShopProgramUserKey(ids[call-1], "buyer-h"), 1, 0).Err()
			}
		}
		resp, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:    "r3-h-conv",
			UserId:     "buyer-h",
			CoinsToUse: 500,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-h1", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: ids[0]}}},
				{ShopId: "shop-2", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-h2", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: ids[1]}}},
				{ShopId: "shop-3", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-h3", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: ids[2]}}},
			},
		})
		if err != nil || !resp.Reserved {
			t.Fatalf("3-shop race must converge cleanly, got resp=%+v err=%v", resp, err)
		}
		if hooked.calls.Load() != 4 || resp.Breakdown.TotalShopProgramDiscountVnd != 0 {
			t.Fatalf("expected 4 attempts & 0 shop program discount after convergence, got calls=%d disc=%d",
				hooked.calls.Load(), resp.Breakdown.TotalShopProgramDiscountVnd)
		}
	})

	// I + J + K + L checks in one focused subtest
	t.Run("I_J_K_L_ZeroDiscount_SpoofedCategory_InactiveSKU_CrossValidate_BareReject", func(t *testing.T) {
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-act":   {Id: "sku-act", ShopId: "shop-1", CategoryId: "cat-other", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true},
				"sku-inact": {Id: "sku-inact", ShopId: "shop-1", CategoryId: "cat-other", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: false},
			},
			descendants: map[string][]string{"cat-fashion": {"cat-fashion"}},
		}
		svc, mem, eng := setupBurstService(t)
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		// I: 0 VND platform voucher does NOT decrement Redis quota or PG used_count
		vShopFull, _ := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "R3FULL", Name: "Full", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 100000,
			Quota: 5, PerUserLimit: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		vPlatZero, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R3ZERO", Name: "Zero", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:           "r3-i-zero",
			UserId:            "buyer-r3",
			ShopVoucherIds:    map[string]string{"shop-1": vShopFull.Voucher.Id},
			PlatformVoucherId: vPlatZero.Voucher.Id,
			ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("ReservePromotion with 0 VND platform voucher failed: %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vPlatZero.Voucher.Id)); rem != 5 {
			t.Fatalf("0 VND platform voucher burned Redis quota! got rem=%d want 5", rem)
		}
		if gotV, _ := mem.GetVoucherByID(context.Background(), vPlatZero.Voucher.Id); gotV.UsedCount != 0 {
			t.Fatalf("0 VND platform voucher incremented DB used_count! got %d want 0", gotV.UsedCount)
		}

		// J: Client-spoofed category_id is IGNORED (real SKU in cat-other); unknown/empty SKUs fail strictly under D4!
		vCat, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R3CAT", Name: "Cat", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 5, PerUserLimit: 5, CategoryIds: []string{"cat-fashion"},
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		prevSpoof, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
			PlatformVoucherId: vCat.Voucher.Id,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-act", Quantity: 1, UnitPriceVnd: 100000, CategoryId: "cat-fashion"},
				}},
			},
		})
		if err != nil || prevSpoof.Breakdown.TotalPlatformVoucherDiscountVnd != 0 {
			t.Fatalf("spoofed client category_id must NOT match voucher: resp=%+v err=%v", prevSpoof, err)
		}
		if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
			PlatformVoucherId: vCat.Voucher.Id,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-nonexistent", Quantity: 1, UnitPriceVnd: 100000, CategoryId: "cat-fashion"},
				}},
			},
		}); status.Code(err) != codes.NotFound {
			t.Fatalf("unknown SKU in PreviewDiscounts: want NotFound, got %v", err)
		}
		if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
			PlatformVoucherId: vCat.Voucher.Id,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{
					{SkuId: "", Quantity: 1, UnitPriceVnd: 100000, CategoryId: "cat-fashion"},
				}},
			},
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("empty sku_id in PreviewDiscounts: want InvalidArgument, got %v", err)
		}

		// K#13: Inactive SKU rejected in CreateShopDiscountProgram
		if _, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "Inact", StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-inact", DiscountPercent: 10}},
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("inactive SKU shop program: want FailedPrecondition, got %v", err)
		}

		// K#14: Non-replace UpdateShopDiscountProgram STILL checks flash overlap even when item already has inflation warning
		pc.setSKU(&productv1.SKU{Id: "sku-act", ShopId: "shop-1", CategoryId: "cat-other", PriceVnd: 120000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true})
		progInf, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "Inf", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-act", DiscountedPriceVnd: 90000}},
		})
		if err != nil || len(progInf.Warnings) == 0 {
			t.Fatalf("expected inflation warning on create, got %+v err=%v", progInf, err)
		}
		fsLater, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "Later Flash", StartsAt: timestamppb.New(now.Add(3 * time.Hour)), EndsAt: timestamppb.New(now.Add(5 * time.Hour)),
		})
		fiLater, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsLater.FlashSale.Id, SkuId: "sku-act", FlashPriceVnd: 80000, TotalSlots: 5, PerUserLimit: 1,
		})
		_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiLater.Item.Id, Approve: true})
		updExt, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
			ProgramId: progInf.Program.Id, EndsAt: timestamppb.New(now.Add(4 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("extend program: %v", err)
		}
		foundFlashWarn := false
		for _, w := range updExt.Warnings {
			if strings.Contains(strings.ToLower(w), "flash") {
				foundFlashWarn = true
			}
		}
		if !foundFlashWarn {
			t.Fatalf("non-replace UpdateShopDiscountProgram skipped flash overlap check! warnings=%v", updExt.Warnings)
		}

		// K#15: Cross-field value_vnd / discount_percent rejected on BOTH CreateVoucher and UpdateVoucher
		if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R3BAD1", Name: "Bad1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, DiscountPercent: 150,
			Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("FIXED with discount_percent!=0 on Create: want InvalidArgument, got %v", err)
		}
		if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R3BAD2", Name: "Bad2", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT, DiscountPercent: 10, ValueVnd: -5,
			Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now), EndsAt: timestamppb.New(now.Add(time.Hour)),
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("PERCENT with value_vnd!=0 on Create: want InvalidArgument, got %v", err)
		}
		if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
			VoucherId: vPlatZero.Voucher.Id, DiscountPercent: i32Ptr(50),
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("FIXED with discount_percent!=0 on Update: want InvalidArgument, got %v", err)
		}

		// L: Bare voucher / bare shop program reservation without shop_items rejected with InvalidArgument
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:           "r3-l-bare-v",
			UserId:            "buyer-r3",
			PlatformVoucherId: vPlatZero.Voucher.Id,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("bare voucher ReservePromotion without shop_items: want InvalidArgument, got %v", err)
		}
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r3-l-bare-sp",
			UserId:  "buyer-r3",
			ShopDiscountProgramItems: []*promotionv1.ShopDiscountProgramReservationItem{
				{ShopDiscountProgramItemId: progInf.Program.Items[0].Id, SkuId: "sku-act", Quantity: 1, ShopId: "shop-1"},
			},
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("bare shop program ReservePromotion without shop_items: want InvalidArgument, got %v", err)
		}
	})
}

// getReservationOverrideStore lets tests simulate GetReservation misses (e.g. concurrent race / stale read)
// to exercise luaOutcome.AlreadyReserved and CommitReservation alreadyCommitted branches cleanly.
type getReservationOverrideStore struct {
	store.Store
	onGetReservation func(orderID string) (*domain.PromotionReservation, error, bool)
}

func (s *getReservationOverrideStore) GetReservation(ctx context.Context, orderID string) (*domain.PromotionReservation, error) {
	if s.onGetReservation != nil {
		if res, err, override := s.onGetReservation(orderID); override {
			return res, err
		}
	}
	return s.Store.GetReservation(ctx, orderID)
}

func TestService_R4_ReviewFixes(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyerACtx := ctxWithRole("buyer-r4-a", "buyer", "")
	svcCtx := ctxWithService("order-service")

	// 1. ApproveFlashSaleItem idempotent APPROVED retry heals missing Redis key without overwriting live counter or re-checking SKU stock.
	t.Run("1_ApproveFlashIdempotentHealsMissingRedisKey", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		mem := store.NewMemoryStore()
		eng := lua.NewEngine(rdb)
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-r4-fl": {Id: "sku-r4-fl", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
			},
		}
		svc := service.NewPromotionService(mem, eng, logger.New("r4-1", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R4 Flash Heal", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fi, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, SkuId: "sku-r4-fl", FlashPriceVnd: 120000, TotalSlots: 5, PerUserLimit: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
			ItemId: fi.Item.Id, Approve: true,
		}); err != nil {
			t.Fatalf("initial ApproveFlashSaleItem: %v", err)
		}

		// Simulate prior failure where PG committed APPROVED but InitFlashItemSlots failed (Redis key absent).
		// Also drop SKU stock to 0 to confirm idempotent retry does NOT re-validate SKU stock.
		mr.Del(lua.FlashRemainingKey(fi.Item.Id))
		pc.setSKU(&productv1.SKU{Id: "sku-r4-fl", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 0, IsActive: true})

		healed, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
			ItemId: fi.Item.Id, Approve: true,
		})
		if err != nil {
			t.Fatalf("idempotent ApproveFlashSaleItem heal failed: %v", err)
		}
		if healed.Item.RemainingSlots != 5 {
			t.Fatalf("healed RemainingSlots: want 5, got %d", healed.Item.RemainingSlots)
		}
		rem, exists, err := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi.Item.Id))
		if err != nil || !exists || rem != 5 {
			t.Fatalf("expected Redis flash key healed to 5, got exists=%v val=%d err=%v", exists, rem, err)
		}

		// Decrement 1 slot via real ReservePromotion (Redis becomes 4), then retry ApproveFlashSaleItem -> MUST NOT overwrite 4 back to 5!
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-heal-res-1",
			UserId:  "buyer-r4-a",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-r4-fl", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		}); err != nil {
			t.Fatalf("ReservePromotion after heal failed: %v", err)
		}
		again, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
			ItemId: fi.Item.Id, Approve: true,
		})
		if err != nil || again.Item.RemainingSlots != 4 {
			t.Fatalf("re-approving live item must keep RemainingSlots=4, got %+v err=%v", again, err)
		}
		if rem2, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi.Item.Id)); rem2 != 4 {
			t.Fatalf("re-approving live item overwrote Redis counter: got %d want 4", rem2)
		}
	})

	// 3. Preview auto-looks up shop discount program even on lines with flash_sale_item_id:
	//    - Flash valid -> flash wins with NO "takes precedence" warning (unless caller explicitly sent both).
	//    - Flash sold out / missing Redis / expired -> falls back to shop discount program price + flash warning.
	//    - ReservePromotion (strict) still rejects sold-out flash.
	t.Run("3_PreviewAutoShopProgramFallbackOnFlash", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		mem := store.NewMemoryStore()
		eng := lua.NewEngine(rdb)
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-r4-both": {Id: "sku-r4-both", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
			},
		}
		svc := service.NewPromotionService(mem, eng, logger.New("r4-3", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		prog, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R4 Fallback Prog", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-r4-both", DiscountedPriceVnd: 160000, PerUserLimit: 3}},
		})
		if err != nil {
			t.Fatal(err)
		}
		progItemID := prog.Program.Items[0].Id

		fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R4 Flash Both", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		fi, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, SkuId: "sku-r4-both", FlashPriceVnd: 120000, TotalSlots: 1, PerUserLimit: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
			ItemId: fi.Item.Id, Approve: true,
		}); err != nil {
			t.Fatal(err)
		}

		// (a) Preview with ONLY flash_sale_item_id when flash has slot -> flash wins (-80k), NO "takes precedence" warning!
		prevFlashOK, err := svc.PreviewDiscounts(buyerACtx, &promotionv1.PreviewDiscountsRequest{
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-r4-both", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		})
		if err != nil {
			t.Fatalf("PreviewDiscounts flash ok: %v", err)
		}
		if prevFlashOK.Breakdown.TotalFlashSaleDiscountVnd != 80000 || prevFlashOK.Breakdown.TotalShopProgramDiscountVnd != 0 {
			t.Fatalf("expected flash=80k prog=0, got %+v", prevFlashOK.Breakdown)
		}
		for _, w := range prevFlashOK.Breakdown.Warnings {
			if strings.Contains(strings.ToLower(w), "takes precedence") {
				t.Fatalf("auto-looked-up shop program must NOT emit 'takes precedence' warning, got %v", prevFlashOK.Breakdown.Warnings)
			}
		}

		// (b) Preview with EXPLICIT flash_sale_item_id + shop_discount_program_item_id -> flash wins AND emits "takes precedence"!
		prevExplicitBoth, err := svc.PreviewDiscounts(buyerACtx, &promotionv1.PreviewDiscountsRequest{
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-r4-both", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id, ShopDiscountProgramItemId: progItemID}}},
			},
		})
		if err != nil {
			t.Fatalf("PreviewDiscounts explicit both: %v", err)
		}
		foundPrec := false
		for _, w := range prevExplicitBoth.Breakdown.Warnings {
			if strings.Contains(strings.ToLower(w), "takes precedence") {
				foundPrec = true
			}
		}
		if !foundPrec {
			t.Fatalf("explicit flash+prog MUST emit 'takes precedence' warning, got %v", prevExplicitBoth.Breakdown.Warnings)
		}

		// (c) Flash sold out (0 slots left) -> Preview with ONLY flash_sale_item_id falls back to shop program (-40k)!
		_ = mr.Set(lua.FlashRemainingKey(fi.Item.Id), "0")
		prevSoldOut, err := svc.PreviewDiscounts(buyerACtx, &promotionv1.PreviewDiscountsRequest{
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-r4-both", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		})
		if err != nil {
			t.Fatalf("PreviewDiscounts sold out: %v", err)
		}
		if prevSoldOut.Breakdown.TotalFlashSaleDiscountVnd != 0 || prevSoldOut.Breakdown.TotalShopProgramDiscountVnd != 40000 || prevSoldOut.Breakdown.FinalPayableVnd != 180000 {
			t.Fatalf("sold-out flash preview must fall back to shop program (disc=40k, pay=180k), got %+v", prevSoldOut.Breakdown)
		}
		if ids := prevSoldOut.Breakdown.Shops[0].AppliedShopProgramItemIds; len(ids) != 1 || ids[0] != progItemID {
			t.Fatalf("sold-out flash fallback must populate applied_shop_program_item_ids=[%s], got %v", progItemID, ids)
		}
		if len(prevSoldOut.Breakdown.Warnings) == 0 {
			t.Fatalf("sold-out flash fallback must still include flash warning")
		}

		// (d) ReservePromotion (strict) with sold-out flash still rejects strictly!
		if _, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-strict-soldout",
			UserId:  "buyer-r4-a",
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-r4-both", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
			},
		}); status.Code(err) != codes.ResourceExhausted && status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("strict ReservePromotion with sold-out flash must reject, got %v", err)
		}
	})

	// 4. ReleasePromotion on unseen order_id tombstones Redis so late ReservePromotion is blocked immediately at Redis.
	t.Run("4_ReleasePromotionTombstonesRedis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		mem := store.NewMemoryStore()
		eng := lua.NewEngine(rdb)
		override := &getReservationOverrideStore{Store: mem}
		svc := service.NewPromotionService(override, eng, logger.New("r4-4", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vResp, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "R4RELV", Name: "R4RelV", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 5, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}

		const orderID = "r4-early-rel-svc"
		if _, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: orderID}); err != nil {
			t.Fatalf("ReleasePromotion(unseen): %v", err)
		}
		if val, err := mr.Get(lua.ReservationKey(orderID)); err != nil || val != "RELEASED" {
			t.Fatalf("expected Redis tombstone RELEASED after ReleasePromotion, got val=%q err=%v", val, err)
		}

		// Bypass PG fast-check to prove Redis itself rejects the late ReservePromotion before touching counters or PG commit!
		override.onGetReservation = func(id string) (*domain.PromotionReservation, error, bool) {
			if id == orderID {
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		_, err = svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:        orderID,
			UserId:         "buyer-r4-a",
			ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 20000}},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("late ReservePromotion after Redis tombstone: want FailedPrecondition, got %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id)); rem != 5 {
			t.Fatalf("Redis voucher quota mutated despite tombstone: got %d want 5", rem)
		}
	})

	// 5. ReservePromotion idempotent replay MUST enforce owner check (PermissionDenied) across all 3 branches.
	t.Run("5_ReservePromotionReplayIDORAll3Branches", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		mem := store.NewMemoryStore()
		eng := lua.NewEngine(rdb)
		override := &getReservationOverrideStore{Store: mem}
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-r4-idor": {Id: "sku-r4-idor", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true},
			},
		}
		svc := service.NewPromotionService(override, eng, logger.New("r4-5", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		prog, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R4 IDOR Prog", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-r4-idor", DiscountedPriceVnd: 160000, PerUserLimit: 5}},
		})
		if err != nil {
			t.Fatal(err)
		}
		progItemID := prog.Program.Items[0].Id

		makeReq := func(orderID, userID string) *promotionv1.ReservePromotionRequest {
			return &promotionv1.ReservePromotionRequest{
				OrderId: orderID,
				UserId:  userID,
				ShopItems: []*promotionv1.ShopDiscountInput{
					{ShopId: "shop-1", ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-r4-idor", Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: progItemID}}},
				},
			}
		}

		// Seed buyer-A's reservation
		const ordID = "r4-idor-ord-1"
		resA, err := svc.ReservePromotion(svcCtx, makeReq(ordID, "buyer-r4-a"))
		if err != nil || !resA.Reserved || resA.Breakdown.TotalShopProgramDiscountVnd != 40000 {
			t.Fatalf("buyerA initial ReservePromotion: resp=%+v err=%v", resA, err)
		}

		// Branch 1: Fast Postgres GetReservation replay by buyer-B -> PermissionDenied (no breakdown)
		if respB, err := svc.ReservePromotion(svcCtx, makeReq(ordID, "buyer-r4-b")); status.Code(err) != codes.PermissionDenied || respB != nil {
			t.Fatalf("Branch 1 IDOR (PG fast-path): want PermissionDenied & nil resp, got resp=%+v err=%v", respB, err)
		}
		// Legitimate replay by buyer-A still succeeds
		if replayA, err := svc.ReservePromotion(svcCtx, makeReq(ordID, "buyer-r4-a")); err != nil || !replayA.AlreadyReserved || replayA.Breakdown.TotalShopProgramDiscountVnd != 40000 {
			t.Fatalf("Branch 1 buyerA own replay failed: resp=%+v err=%v", replayA, err)
		}

		// Branch 2: luaOutcome.AlreadyReserved replay by buyer-B (skip Branch 1 once so Lua returns AlreadyReserved, then waitForCommittedReservation reads PG)
		calls := 0
		override.onGetReservation = func(id string) (*domain.PromotionReservation, error, bool) {
			if id == ordID {
				calls++
				if calls == 1 {
					return nil, store.ErrNotFound, true
				}
			}
			return nil, nil, false
		}
		if respB2, err := svc.ReservePromotion(svcCtx, makeReq(ordID, "buyer-r4-b")); status.Code(err) != codes.PermissionDenied || respB2 != nil {
			t.Fatalf("Branch 2 IDOR (lua AlreadyReserved): want PermissionDenied & nil resp, got resp=%+v err=%v", respB2, err)
		}

		// Branch 3: CommitReservation alreadyCommitted replay by buyer-B (Redis key absent + Branch 1 bypassed -> CommitReservation returns alreadyCommitted=true)
		mr.Del(lua.ReservationKey(ordID))
		override.onGetReservation = func(id string) (*domain.PromotionReservation, error, bool) {
			if id == ordID {
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		if respB3, err := svc.ReservePromotion(svcCtx, makeReq(ordID, "buyer-r4-b")); status.Code(err) != codes.PermissionDenied || respB3 != nil {
			t.Fatalf("Branch 3 IDOR (CommitReservation alreadyCommitted): want PermissionDenied & nil resp, got resp=%+v err=%v", respB3, err)
		}
		// Ensure buyer-B did NOT leak shop program per-user counter in Redis on Branch 3 rejection
		if puB, _, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(progItemID, "buyer-r4-b")); puB != 0 {
			t.Fatalf("Branch 3 IDOR leaked buyer-B Redis counter: got %d want 0", puB)
		}
	})

	// 6. Non-replace UpdateShopDiscountProgram persists new flash overlap warning into warning column.
	t.Run("6_UpdateShopProgramNonReplacePersistsFlashWarning", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		mem := store.NewMemoryStore()
		eng := lua.NewEngine(rdb)
		pc := &fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-r4-warn": {Id: "sku-r4-warn", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true},
			},
		}
		svc := service.NewPromotionService(mem, eng, logger.New("r4-6", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		prog, err := svc.CreateShopDiscountProgram(seller1Ctx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R4 Warn", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-r4-warn", DiscountedPriceVnd: 80000}},
		})
		if err != nil {
			t.Fatal(err)
		}
		fsLater, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R4 Later Flash", StartsAt: timestamppb.New(now.Add(3 * time.Hour)), EndsAt: timestamppb.New(now.Add(5 * time.Hour)),
		})
		fiLater, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsLater.FlashSale.Id, SkuId: "sku-r4-warn", FlashPriceVnd: 70000, TotalSlots: 5, PerUserLimit: 1,
		})
		_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiLater.Item.Id, Approve: true})

		if _, err := svc.UpdateShopDiscountProgram(seller1Ctx, &promotionv1.UpdateShopDiscountProgramRequest{
			ProgramId: prog.Program.Id, EndsAt: timestamppb.New(now.Add(4 * time.Hour)),
		}); err != nil {
			t.Fatalf("UpdateShopDiscountProgram non-replace: %v", err)
		}

		// Subsequent ListShopDiscountPrograms MUST return persisted flash overlap warning on the item.
		listed, err := svc.ListShopDiscountPrograms(seller1Ctx, &promotionv1.ListShopDiscountProgramsRequest{})
		if err != nil || len(listed.Programs) != 1 || len(listed.Programs[0].Items) != 1 {
			t.Fatalf("ListShopDiscountPrograms unexpected: %+v err=%v", listed, err)
		}
		if !strings.Contains(strings.ToLower(listed.Programs[0].Items[0].Warning), "flash") {
			t.Fatalf("expected persisted flash warning on item after non-replace update, got %q", listed.Programs[0].Items[0].Warning)
		}
	})
}

var _ service.ProductClient = (*fakeProductClient)(nil)
