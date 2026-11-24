package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
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

type errorProductClient struct {
	fakeProductClient
	skuErr error
}

func (e *errorProductClient) GetSKUsBatch(ctx context.Context, skuIDs []string) (map[string]*productv1.SKU, error) {
	if e.skuErr != nil {
		return nil, e.skuErr
	}
	return e.fakeProductClient.GetSKUsBatch(ctx, skuIDs)
}

func TestService_W3A_D3_ReservePromotion_OrderServiceOnly(t *testing.T) {
	svc, mem, eng := setupBurstService(t)
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	superAdminCtx := ctxWithRole("super-1", "super_admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	otherSvcCtx := ctxWithService("payment-service")
	orderSvcCtx := ctxWithService("order-service")
	anonCtx := context.Background()

	vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "W3AD3V",
		Name:         "W3A D3 Shop Voucher",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		MinOrderVnd:  100000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    100000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-1",
		Amount:      10000,
		Reason:      "seed",
		ReferenceId: "w3a-d3-coins",
	}); err != nil {
		t.Fatalf("CreditCoins: %v", err)
	}

	makeReq := func(orderID string) *promotionv1.ReservePromotionRequest {
		return &promotionv1.ReservePromotionRequest{
			OrderId:        orderID,
			UserId:         "buyer-1",
			ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
			CoinsToUse:     3000,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 20000},
			},
		}
	}

	// 1. Anonymous -> Unauthenticated; buyer/seller/admin/super_admin/other-service -> PermissionDenied.
	if _, err := svc.ReservePromotion(anonCtx, makeReq("w3a-d3-anon")); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ReservePromotion: want Unauthenticated, got %v", err)
	}
	for name, ctx := range map[string]context.Context{
		"buyer":         buyerCtx,
		"seller":        sellerCtx,
		"admin":         adminCtx,
		"super_admin":   superAdminCtx,
		"other_service": otherSvcCtx,
	} {
		ordID := "w3a-d3-deny-" + name
		if resp, err := svc.ReservePromotion(ctx, makeReq(ordID)); status.Code(err) != codes.PermissionDenied || resp != nil {
			t.Fatalf("%s ReservePromotion: want PermissionDenied & nil resp, got resp=%+v err=%v", name, resp, err)
		}
		if _, gErr := mem.GetReservation(context.Background(), ordID); !errors.Is(gErr, store.ErrNotFound) {
			t.Fatalf("%s rejected ReservePromotion must not persist PG reservation, got err=%v", name, gErr)
		}
	}

	// Verify Redis and PG state are completely untouched after all unauthorized attempts.
	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id)); rem != 5 {
		t.Fatalf("Redis voucher quota mutated on unauthorized ReservePromotion: got %d want 5", rem)
	}
	if bud, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vResp.Voucher.Id)); bud != 100000 {
		t.Fatalf("Redis voucher budget mutated on unauthorized ReservePromotion: got %d want 100000", bud)
	}
	if gotV, _ := mem.GetVoucherByID(context.Background(), vResp.Voucher.Id); gotV.UsedCount != 0 {
		t.Fatalf("PG voucher used_count mutated on unauthorized ReservePromotion: got %d want 0", gotV.UsedCount)
	}
	if bal := mem.AccountBalance("buyer-1"); bal != 10000 {
		t.Fatalf("PG coins mutated on unauthorized ReservePromotion: got %d want 10000", bal)
	}

	// 2. order-service token with empty user_id -> InvalidArgument.
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:   "w3a-d3-no-user",
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("order-service ReservePromotion without user_id: want InvalidArgument, got %v", err)
	}

	// 3. order-service token without shop_items (bare branch removed) -> InvalidArgument.
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:    "w3a-d3-no-shop-items",
		UserId:     "buyer-1",
		CoinsToUse: 1000,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("order-service bare ReservePromotion without shop_items: want InvalidArgument, got %v", err)
	}

	// 4. order-service token with valid user_id + shop_items -> succeeds!
	okResp, err := svc.ReservePromotion(orderSvcCtx, makeReq("w3a-d3-ok-1"))
	if err != nil || !okResp.GetReserved() || okResp.GetBreakdown().GetTotalShopVoucherDiscountVnd() != 20000 {
		t.Fatalf("order-service ReservePromotion failed: resp=%+v err=%v", okResp, err)
	}
}

func TestService_W3A_D4_PreviewDiscounts_AuthoritativeSKUs(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	pc := &errorProductClient{
		fakeProductClient: fakeProductClient{
			skus: map[string]*productv1.SKU{
				"sku-real": {
					Id: "sku-real", ShopId: "shop-1", CategoryId: "cat-elec",
					PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true,
				},
				"sku-shop2": {
					Id: "sku-shop2", ShopId: "shop-2", CategoryId: "cat-fashion",
					PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true,
				},
				// D2 redacted hidden/inactive SKU (only id, shop_id, is_active=false; price/category zeroed)
				"sku-hidden": {
					Id: "sku-hidden", ShopId: "shop-1", IsActive: false,
				},
			},
			descendants: map[string][]string{
				"cat-fashion": {"cat-fashion"},
				"cat-elec":    {"cat-elec"},
			},
		},
	}

	svc, _, _ := setupBurstService(t)
	svc.SetProductClient(pc)
	svc.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Shop voucher: 10% off, min order 150k
	shopV, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code: "W3AD4S", Name: "Shop 10%", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT, DiscountPercent: 10, MaxDiscountVnd: 500000,
		MinOrderVnd: 150000, Quota: 10, PerUserLimit: 5,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Fashion-only platform voucher
	fashV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "W3AD4F", Name: "Fashion 20k", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
		Quota: 10, PerUserLimit: 5, CategoryIds: []string{"cat-fashion"},
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Spoofed unit_price_vnd (5,000,000), spoofed subtotal_vnd (5,000,000), and spoofed category_id (cat-fashion)
	// MUST be ignored: real SKU is 100,000 VND in cat-elec, so qty=1 -> subtotal=100,000 (below 150k min order, not fashion).
	prevSpoof, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopVoucherIds:    map[string]string{"shop-1": shopV.Voucher.Id},
		PlatformVoucherId: fashV.Voucher.Id,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         "shop-1",
				SubtotalVnd:    5000000,
				ShippingFeeVnd: 25000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-real", Quantity: 1, UnitPriceVnd: 5000000, CategoryId: "cat-fashion"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts spoofed price: %v", err)
	}
	bd := prevSpoof.GetBreakdown()
	if bd.GetTotalSubtotalVnd() != 100000 {
		t.Fatalf("PreviewDiscounts trusted client price/subtotal: got subtotal=%d want 100000", bd.GetTotalSubtotalVnd())
	}
	if bd.GetTotalShopVoucherDiscountVnd() != 0 || bd.GetTotalPlatformVoucherDiscountVnd() != 0 {
		t.Fatalf("PreviewDiscounts applied voucher from spoofed price/category: shopV=%d platV=%d",
			bd.GetTotalShopVoucherDiscountVnd(), bd.GetTotalPlatformVoucherDiscountVnd())
	}
	if bd.GetFinalPayableVnd() != 125000 {
		t.Fatalf("PreviewDiscounts final_payable_vnd: got %d want 125000", bd.GetFinalPayableVnd())
	}

	// 2. Spoofed subtotal_vnd with empty items MUST be ignored (subtotal = 0, no voucher applied).
	prevEmptyItems, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopVoucherIds: map[string]string{"shop-1": shopV.Voucher.Id},
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-1", SubtotalVnd: 500000, ShippingFeeVnd: 20000},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts empty items: %v", err)
	}
	if prevEmptyItems.GetBreakdown().GetTotalSubtotalVnd() != 0 || prevEmptyItems.GetBreakdown().GetTotalShopVoucherDiscountVnd() != 0 {
		t.Fatalf("PreviewDiscounts trusted subtotal_vnd with empty items: %+v", prevEmptyItems.GetBreakdown())
	}

	// 3. SKU belonging to another shop -> InvalidArgument.
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items:  []*promotionv1.DiscountLineItem{{SkuId: "sku-shop2", Quantity: 1, UnitPriceVnd: 200000}},
			},
		},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PreviewDiscounts wrong shop SKU: want InvalidArgument, got %v", err)
	}

	// 4. Hidden/inactive SKU (is_active=false) -> FailedPrecondition (SKU_INACTIVE).
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items:  []*promotionv1.DiscountLineItem{{SkuId: "sku-hidden", Quantity: 1, UnitPriceVnd: 100000}},
			},
		},
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "SKU_INACTIVE") {
		t.Fatalf("PreviewDiscounts inactive SKU: want FailedPrecondition with SKU_INACTIVE, got %v", err)
	}

	// 5. Nonexistent SKU -> NotFound.
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items:  []*promotionv1.DiscountLineItem{{SkuId: "sku-missing", Quantity: 1, UnitPriceVnd: 100000}},
			},
		},
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("PreviewDiscounts missing SKU: want NotFound, got %v", err)
	}

	// 6. ProductService error -> Unavailable (NEVER falls back to client unit_price_vnd!).
	pc.skuErr = errors.New("product service connection refused")
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items:  []*promotionv1.DiscountLineItem{{SkuId: "sku-real", Quantity: 2, UnitPriceVnd: 100000}},
			},
		},
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("PreviewDiscounts on product error: want Unavailable, got %v", err)
	}
	pc.skuErr = nil

	// 7. Nil productClient with items -> Unavailable (fail closed!).
	svcNoPC, _, _ := setupBurstService(t)
	if _, err := svcNoPC.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items:  []*promotionv1.DiscountLineItem{{SkuId: "sku-real", Quantity: 1, UnitPriceVnd: 100000}},
			},
		},
	}); status.Code(err) != codes.Unavailable {
		t.Fatalf("PreviewDiscounts without productClient: want Unavailable, got %v", err)
	}

	// 8. shipping_fee_vnd & free_ship_* remain respected from request (read-only), while empty sku_id / qty<=0 fail InvalidArgument.
	prevShip, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:              "", // omitted shop_id resolved authoritatively from SKU
				ShippingFeeVnd:      30000,
				FreeShipEnabled:     true,
				FreeShipMinOrderVnd: 100000,
				FreeShipMaxVnd:      15000,
				Items:               []*promotionv1.DiscountLineItem{{SkuId: "sku-real", Quantity: 1}},
			},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts freeship & inferred shop_id: %v", err)
	}
	if prevShip.GetBreakdown().GetTotalSubtotalVnd() != 100000 ||
		prevShip.GetBreakdown().GetTotalShopFreeshipDiscountVnd() != 15000 ||
		prevShip.GetBreakdown().GetFinalPayableVnd() != 115000 {
		t.Fatalf("PreviewDiscounts freeship unexpected: %+v", prevShip.GetBreakdown())
	}
	if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-real", Quantity: 0}}}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("PreviewDiscounts qty=0: want InvalidArgument, got %v", err)
	}
}

func TestService_W3A_CrossShop_ForbiddenAndUnchanged(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	pc := newFakeProductClient()
	pc.setSKU(&productv1.SKU{Id: "sku-a1", ShopId: "shop-a", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})
	pc.setSKU(&productv1.SKU{Id: "sku-b1", ShopId: "shop-b", CategoryId: "cat-1", PriceVnd: 150000, MinPrice_30DVnd: 150000, AvailableStock: 20, IsActive: true})

	svc, mem, eng := setupBurstService(t)
	svc.SetProductClient(pc)
	svc.SetClock(func() time.Time { return now })

	sellerACtx := ctxWithRole("seller-a", "seller", "shop-a")
	sellerBCtx := ctxWithRole("seller-b", "seller", "shop-b")

	// Seed Shop A voucher and Shop A discount program.
	vA, err := svc.CreateVoucher(sellerACtx, &promotionv1.CreateVoucherRequest{
		Code: "SHOPAV1", Name: "Shop A Original", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-a",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
		Quota: 10, PerUserLimit: 2, BudgetVnd: 200000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateVoucher shop-a: %v", err)
	}
	progA, err := svc.CreateShopDiscountProgram(sellerACtx, &promotionv1.CreateShopDiscountProgramRequest{
		Name: "Shop A Sale", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		Items: []*promotionv1.ShopDiscountProgramItemInput{
			{SkuId: "sku-a1", DiscountedPriceVnd: 160000, PerUserLimit: 3},
		},
	})
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram shop-a: %v", err)
	}

	// 1. Shop B calls UpdateShopDiscountProgram (metadata AND replace_items=true) on Shop A's program -> PermissionDenied.
	if _, err := svc.UpdateShopDiscountProgram(sellerBCtx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId: progA.Program.Id,
		Name:      strPtr("Hijacked by Shop B"),
		EndsAt:    timestamppb.New(now.Add(48 * time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("shop-b UpdateShopDiscountProgram(meta): want PermissionDenied, got %v", err)
	}
	if _, err := svc.UpdateShopDiscountProgram(sellerBCtx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId:    progA.Program.Id,
		ReplaceItems: true,
		Items:        []*promotionv1.ShopDiscountProgramItemInput{{SkuId: "sku-b1", DiscountedPriceVnd: 100000}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("shop-b UpdateShopDiscountProgram(replace): want PermissionDenied, got %v", err)
	}

	// 2. Shop B calls EndShopDiscountProgram on Shop A's program -> PermissionDenied.
	if _, err := svc.EndShopDiscountProgram(sellerBCtx, &promotionv1.EndShopDiscountProgramRequest{
		ProgramId: progA.Program.Id,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("shop-b EndShopDiscountProgram: want PermissionDenied, got %v", err)
	}

	// 3. Shop B calls UpdateVoucher on Shop A's voucher -> PermissionDenied.
	if _, err := svc.UpdateVoucher(sellerBCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vA.Voucher.Id,
		Name:      strPtr("Hijacked Voucher"),
		ValueVnd:  i64Ptr(99000),
		Quota:     i32Ptr(99),
		BudgetVnd: i64Ptr(999000),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("shop-b UpdateVoucher: want PermissionDenied, got %v", err)
	}

	// Verify Shop A program & voucher are 100% unchanged in store and Redis.
	gotProg, err := mem.GetShopDiscountProgram(context.Background(), progA.Program.Id)
	if err != nil {
		t.Fatalf("GetShopDiscountProgram: %v", err)
	}
	if gotProg.Name != "Shop A Sale" || gotProg.EffectiveStatus(now) != domain.ShopProgramStatusActive ||
		len(gotProg.Items) != 1 || gotProg.Items[0].SKUID != "sku-a1" || gotProg.Items[0].DiscountedPriceVND != 160000 {
		t.Fatalf("shop-a program was mutated by shop-b: %+v", gotProg)
	}

	gotV, err := mem.GetVoucherByID(context.Background(), vA.Voucher.Id)
	if err != nil {
		t.Fatalf("GetVoucherByID: %v", err)
	}
	if gotV.Name != "Shop A Original" || gotV.ValueVND != 20000 || gotV.Quota != 10 || gotV.BudgetVND != 200000 {
		t.Fatalf("shop-a voucher was mutated by shop-b: %+v", gotV)
	}
	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vA.Voucher.Id)); rem != 10 {
		t.Fatalf("shop-a voucher Redis quota changed: got %d want 10", rem)
	}
	if bud, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vA.Voucher.Id)); bud != 200000 {
		t.Fatalf("shop-a voucher Redis budget changed: got %d want 200000", bud)
	}
}

func TestService_W3A_UpdateVoucher_RejectsRuleChangeWhenReservedOrConcurrentReserve(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	mem := store.NewMemoryStore()
	eng := lua.NewEngine(rdb)
	svc := service.NewPromotionService(mem, eng, logger.New("w3a-upd-test", logger.WithWriter(io.Discard)))

	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	adminCtx := ctxWithRole("admin-1", "admin", "")
	orderSvcCtx := ctxWithService("order-service")

	// Seed platform voucher with finite budget & category_ids
	vResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "W3AUPDV",
		Name:         "Update Race Voucher",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		Quota:        10,
		PerUserLimit: 2,
		BudgetVnd:    200000,
		CategoryIds:  []string{"cat-1"},
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	vid := vResp.Voucher.Id

	// Simulate an active RESERVED reservation in store while Redis counter was wiped/stale (reset back to quota=10)
	// AND test that UpdateVoucher rejects rule changes (type, value, per_user_limit, category_ids, disabling budget).
	_, _, err = mem.CommitReservation(context.Background(), domain.PromotionReservation{
		OrderID: "w3a-upd-res-1",
		UserID:  "buyer-1",
		Vouchers: []domain.ReservedVoucher{
			{VoucherID: vid, Scope: domain.VoucherScopePlatform, DiscountVND: 20000, HasBudget: true},
		},
	}, now)
	if err != nil {
		t.Fatalf("CommitReservation: %v", err)
	}
	// Even if Redis remaining still shows 10 (stale), PG has RESERVED reservation -> rule mutations MUST be rejected!
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, DiscountType: dtPtr(promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT), DiscountPercent: i32Ptr(10),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateVoucher discount_type with RESERVED reservation: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, ValueVnd: i64Ptr(30000),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateVoucher value_vnd with RESERVED reservation: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, PerUserLimit: i32Ptr(5),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateVoucher per_user_limit with RESERVED reservation: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, CategoryIds: []string{"cat-2"}, ReplaceCategoryIds: true,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateVoucher category_ids with RESERVED reservation: want FailedPrecondition, got %v", err)
	}
	if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, BudgetVnd: i64Ptr(0),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateVoucher disable budget (>0 -> 0) with RESERVED reservation: want FailedPrecondition, got %v", err)
	}

	// Concurrent race: unused voucher raced between ReservePromotion (order-service) and UpdateVoucher (changing value_vnd / disabling budget).
	// Invariant: NEVER both commit with mismatched rules (old discount applied while voucher rules changed).
	for i := 0; i < 10; i++ {
		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code:         "W3ARACE" + string(rune('A'+i)),
			Name:         "Race Voucher",
			Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
			ValueVnd:     20000,
			Quota:        5,
			PerUserLimit: 1,
			BudgetVnd:    100000,
			StartsAt:     timestamppb.New(now.Add(-time.Hour)),
			EndsAt:       timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		var (
			wg      sync.WaitGroup
			start   = make(chan struct{})
			resResp *promotionv1.ReservePromotionResponse
			resErr  error
			updErr  error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			resResp, resErr = svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId:           "w3a-race-ord-" + string(rune('a'+i)),
				UserId:            "buyer-race",
				PlatformVoucherId: vr.Voucher.Id,
				ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, updErr = svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vr.Voucher.Id,
				ValueVnd:  i64Ptr(50000),
				BudgetVnd: i64Ptr(0),
			})
		}()
		close(start)
		wg.Wait()

		if resErr == nil && updErr == nil {
			if resResp.GetBreakdown().GetTotalPlatformVoucherDiscountVnd() == 20000 {
				t.Fatalf("race iter %d: BOTH ReservePromotion (old 20k rule) AND UpdateVoucher (50k + budget=0) succeeded!", i)
			}
		}
	}

	// Deterministic TOCTOU test A: ReservePromotion finishes Redis Lua (remaining 5->4) and pauses BEFORE CommitReservation
	// (so PG used_count is still 0 and no RESERVED row exists yet). UpdateVoucher MUST read Redis inside the row lock
	// and reject rule mutation with FailedPrecondition!
	t.Run("DeterministicTOCTOU_RedisReservedBeforePGCommit", func(t *testing.T) {
		mr2 := miniredis.RunT(t)
		rdb2 := redis.NewClient(&redis.Options{Addr: mr2.Addr()})
		t.Cleanup(func() { _ = rdb2.Close() })
		mem2 := store.NewMemoryStore()
		eng2 := lua.NewEngine(rdb2)
		entered := make(chan struct{})
		release := make(chan struct{})
		hook := &preCommitHookStore{
			Store: mem2,
			onBeforeCommit: func(_ domain.PromotionReservation) {
				close(entered)
				<-release
			},
		}
		svc2 := service.NewPromotionService(hook, eng2, logger.New("w3a-toctou-a", logger.WithWriter(io.Discard)))
		svc2.SetClock(func() time.Time { return now })

		vr, err := svc2.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "W3ATOCTOUA", Name: "TOCTOU A", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 1, BudgetVnd: 100000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}

		resDone := make(chan error, 1)
		go func() {
			_, rErr := svc2.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId:           "w3a-toctou-a-ord",
				UserId:            "buyer-toctou-a",
				PlatformVoucherId: vr.Voucher.Id,
				ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
			})
			resDone <- rErr
		}()

		<-entered
		// PG still has used_count=0, but Redis already decremented remaining from 5 -> 4.
		_, uErr := svc2.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
			VoucherId: vr.Voucher.Id,
			ValueVnd:  i64Ptr(50000),
			BudgetVnd: i64Ptr(0),
		})
		close(release)
		rErr := <-resDone

		if status.Code(uErr) != codes.FailedPrecondition {
			t.Fatalf("UpdateVoucher while Redis reservation in-flight must fail FailedPrecondition, got %v", uErr)
		}
		if rErr != nil {
			t.Fatalf("in-flight ReservePromotion should commit cleanly, got %v", rErr)
		}
	})

	// Deterministic TOCTOU test B: ReservePromotion calculates old 20k discount (rules_version=1) and pauses BEFORE Lua.
	// UpdateVoucher runs while voucher is genuinely unused, bumps rules_version=2 (value_vnd=50k, keeping budget_vnd=100k
	// intact so Redis Lua passes), and commits. When ReservePromotion resumes, Lua passes and PG CommitReservation MUST
	// reject stale rules_version=1 with VOUCHER_UNAVAILABLE_ON_COMMIT (commitCalls == 1) and compensate Redis cleanly!
	t.Run("DeterministicTOCTOU_UpdateBetweenCalcAndCommit", func(t *testing.T) {
		mr3 := miniredis.RunT(t)
		rdb3 := redis.NewClient(&redis.Options{Addr: mr3.Addr()})
		t.Cleanup(func() { _ = rdb3.Close() })
		mem3 := store.NewMemoryStore()
		eng3 := lua.NewEngine(rdb3)
		_, _, _ = mem3.CreditCoins(context.Background(), "buyer-toctou-b", 1000, "seed", "toctou-b-coins")
		var svc3 *service.PromotionService
		var vidB string
		var updErrB error
		coinHooked := &coinHookStore{
			Store: mem3,
			onGetCoinBalance: func(call int) {
				if call == 1 {
					_, updErrB = svc3.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
						VoucherId: vidB,
						ValueVnd:  i64Ptr(50000),
					})
				}
			},
		}
		commitTracker := &r4HookStore{Store: coinHooked}
		svc3 = service.NewPromotionService(commitTracker, eng3, logger.New("w3a-toctou-b", logger.WithWriter(io.Discard)))
		svc3.SetClock(func() time.Time { return now })

		vr, err := svc3.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "W3ATOCTOUB", Name: "TOCTOU B", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 1, BudgetVnd: 100000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vidB = vr.Voucher.Id

		commitTracker.commitCalls.Store(0)
		_, rErr := svc3.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:           "w3a-toctou-b-ord",
			UserId:            "buyer-toctou-b",
			PlatformVoucherId: vidB,
			CoinsToUse:        500,
			ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if updErrB != nil {
			t.Fatalf("unused UpdateVoucher should succeed, got %v", updErrB)
		}
		if status.Code(rErr) != codes.FailedPrecondition || !strings.Contains(rErr.Error(), "VOUCHER_UNAVAILABLE_ON_COMMIT") {
			t.Fatalf("ReservePromotion with stale pre-update rules MUST fail VOUCHER_UNAVAILABLE_ON_COMMIT, got %v", rErr)
		}
		if got := commitTracker.commitCalls.Load(); got != 1 {
			t.Fatalf("expected Lua to pass and CommitReservation to be called exactly once, got commitCalls=%d", got)
		}
		if rem, _, _ := eng3.GetInt64(context.Background(), lua.VoucherRemainingKey(vidB)); rem != 5 {
			t.Fatalf("Redis quota not compensated after rules_version reject: got %d want 5", rem)
		}
		if bud, _, _ := eng3.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vidB)); bud != 100000 {
			t.Fatalf("Redis budget not compensated after rules_version reject: got %d want 100000", bud)
		}
		if _, exists, _ := eng3.GetInt64(context.Background(), lua.VoucherUserKey(vidB, "buyer-toctou-b")); exists {
			t.Fatalf("Redis :user: key not compensated after rules_version reject")
		}
	})
}

type postUpdateHookStore struct {
	store.Store
	onAfterUpdate func(after *domain.Voucher)
}

func (p *postUpdateHookStore) UpdateVoucher(
	ctx context.Context,
	id string,
	mutate func(cur *domain.Voucher, hasActiveReservation bool) error,
) (*domain.Voucher, error) {
	after, err := p.Store.UpdateVoucher(ctx, id, mutate)
	if err == nil && p.onAfterUpdate != nil {
		p.onAfterUpdate(after)
	}
	return after, err
}

func TestService_W3A_PerUserLimit_RedisWipedMidFlightAndRace(t *testing.T) {
	runSuite := func(t *testing.T, st store.Store, suffix string) {
		t.Helper()
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 50})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		shopID := "w3a-pul-shop-" + suffix
		skuF := "w3a-pul-skuf-" + suffix
		skuP := "w3a-pul-skup-" + suffix

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: skuP, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})

		svc := service.NewPromotionService(st, eng, logger.New("w3a-pul", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		adminCtx := ctxWithRole("admin-1", "admin", "")
		sellerCtx := ctxWithRole("seller-1", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
			Code: "W3APUL" + suffix, Name: "PUL Voucher", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shopID,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 10, PerUserLimit: 1, BudgetVnd: 200000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatalf("CreateVoucher: %v", err)
		}
		vid := vResp.Voucher.Id

		fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "PUL Flash " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fiResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fiResp.Item.Id

		progResp, err := svc.CreateShopDiscountProgram(sellerCtx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "PUL Prog " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: skuP, DiscountedPriceVnd: 150000, PerUserLimit: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pid := progResp.Program.Items[0].Id

		// A. Voucher per_user_limit=1: reserve O1 -> wipe :user: key in Redis -> O2 rejected (via max(PG,Redis) or PG CommitReservation)
		// without mutating Redis counters!
		buyerA := "buyer-pul-a-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "pul-v1-" + suffix, UserId: buyerA,
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 200000}},
		}); err != nil {
			t.Fatalf("O1 voucher reserve: %v", err)
		}
		mr.Del(lua.VoucherUserKey(vid, buyerA))
		_, err = svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "pul-v2-" + suffix, UserId: buyerA,
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 200000}},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("voucher over limit after Redis wipe: want FailedPrecondition, got %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 9 {
			t.Fatalf("Redis voucher quota mutated on rejected O2: got %d want 9", rem)
		}
		if bud, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid)); bud != 180000 {
			t.Fatalf("Redis voucher budget mutated on rejected O2: got %d want 180000", bud)
		}

		// B. Flash per_user_limit=2: reserve qty=2 -> wipe :user: key in Redis -> qty=1 rejected without mutating Redis slots!
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "pul-f1-" + suffix, UserId: buyerA,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 2, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		}); err != nil {
			t.Fatalf("O1 flash reserve: %v", err)
		}
		mr.Del(lua.FlashUserKey(fid, buyerA))
		_, err = svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "pul-f2-" + suffix, UserId: buyerA,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("flash over limit after Redis wipe: want FailedPrecondition, got %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); rem != 8 {
			t.Fatalf("Redis flash remaining mutated on rejected O2: got %d want 8", rem)
		}

		// C. Shop discount program per_user_limit=1: reserve qty=1 -> wipe :user: key in Redis -> qty=1 MUST fall back
		// cleanly to regular price with warning (contracts §4.10) instead of blocking checkout, keeping PG used_qty == 1!
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "pul-p1-" + suffix, UserId: buyerA,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuP, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: pid}},
			}},
		}); err != nil {
			t.Fatalf("O1 shop program reserve: %v", err)
		}
		mr.Del(lua.ShopProgramUserKey(pid, buyerA))
		resP2, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "pul-p2-" + suffix, UserId: buyerA,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuP, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: pid}},
			}},
		})
		if err != nil || !resP2.GetReserved() ||
			resP2.GetBreakdown().GetTotalShopProgramDiscountVnd() != 0 ||
			resP2.GetBreakdown().GetFinalPayableVnd() != 200000 ||
			len(resP2.GetBreakdown().GetWarnings()) == 0 ||
			len(resP2.GetBreakdown().GetShops()[0].GetAppliedShopProgramItemIds()) != 0 {
			t.Fatalf("shop program over limit after Redis wipe must fall back to regular price with warning, got resp=%+v err=%v", resP2, err)
		}
		usageMap, uErr := st.GetUserUsageBatch(context.Background(), buyerA, []store.UserUsageKey{{Kind: domain.UsageKindShopProgram, TargetID: pid}})
		if uErr != nil || usageMap[store.UserUsageKey{Kind: domain.UsageKindShopProgram, TargetID: pid}] != 1 {
			t.Fatalf("PG shop program used_qty must remain 1 after regular-price fallback, got %+v err=%v", usageMap, uErr)
		}

		// D. Lost promo:res:<order_id> on ReleasePromotion AND ReleasePromotionShop still reconciles Redis quota/slots/user usage back to PG!
		mr.Del(lua.ReservationKey("pul-v1-" + suffix))
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: "pul-v1-" + suffix}); err != nil {
			t.Fatalf("ReleasePromotion after lost promo:res key: %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 10 {
			t.Fatalf("expected voucher quota reconciled to 10 after lost-Redis release, got %d", rem)
		}
		if _, exists, _ := eng.GetInt64(context.Background(), lua.VoucherUserKey(vid, buyerA)); exists {
			t.Fatalf("expected voucher :user: key cleared after lost-Redis release")
		}
		// Partial/final shop release with lost promo:res key on flash + shop program reservations:
		mr.Del(lua.ReservationKey("pul-f1-" + suffix))
		if _, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: "pul-f1-" + suffix, ShopId: shopID}); err != nil {
			t.Fatalf("ReleasePromotionShop(flash) after lost promo:res key: %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); rem != 10 {
			t.Fatalf("expected flash slots reconciled to 10 after lost-Redis shop release, got %d", rem)
		}
		mr.Del(lua.ReservationKey("pul-p1-" + suffix))
		if _, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: "pul-p1-" + suffix, ShopId: shopID}); err != nil {
			t.Fatalf("ReleasePromotionShop(prog) after lost promo:res key: %v", err)
		}
		if _, exists, _ := eng.GetInt64(context.Background(), lua.ShopProgramUserKey(pid, buyerA)); exists {
			t.Fatalf("expected shop program :user: key cleared after lost-Redis shop release")
		}

		// E. Deterministic 2-goroutine race for SAME user where Redis :user: key is wiped mid-flight so BOTH
		// goroutines pass Redis Lua and collide head-to-head inside Postgres CommitReservation!
		// Invariant: EXACTLY 1 commits, 1 fails VOUCHER_USER_LIMIT_EXCEEDED, and Redis remaining is compensated back to 9.
		buyerRace := "buyer-pul-race-" + suffix
		var (
			enteredCount atomic.Int32
			g1Ready      = make(chan struct{})
			bothInCommit = make(chan struct{})
		)
		raceHook := &preCommitHookStore{
			Store: st,
			onBeforeCommit: func(_ domain.PromotionReservation) {
				if enteredCount.Add(1) == 1 {
					// Wipe Redis :user: key before G1 commits PG so G2 also passes Redis Lua!
					mr.Del(lua.VoucherUserKey(vid, buyerRace))
					close(g1Ready)
					<-bothInCommit
				} else {
					close(bothInCommit)
				}
			},
		}
		raceSvc := service.NewPromotionService(raceHook, eng, logger.New("w3a-pul-race", logger.WithWriter(io.Discard)))
		raceSvc.SetProductClient(pc)
		raceSvc.SetClock(func() time.Time { return now })

		var (
			wg      sync.WaitGroup
			okCount atomic.Int32
			ulCount atomic.Int32
			other   atomic.Int32
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, rErr := raceSvc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId:        fmt.Sprintf("pul-race-%s-0", suffix),
				UserId:         buyerRace,
				ShopVoucherIds: map[string]string{shopID: vid},
				ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 200000}},
			})
			switch {
			case rErr == nil:
				okCount.Add(1)
			case status.Code(rErr) == codes.FailedPrecondition && strings.Contains(rErr.Error(), "VOUCHER_USER_LIMIT_EXCEEDED"):
				ulCount.Add(1)
			default:
				other.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			<-g1Ready
			_, rErr := raceSvc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId:        fmt.Sprintf("pul-race-%s-1", suffix),
				UserId:         buyerRace,
				ShopVoucherIds: map[string]string{shopID: vid},
				ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 200000}},
			})
			switch {
			case rErr == nil:
				okCount.Add(1)
			case status.Code(rErr) == codes.FailedPrecondition && strings.Contains(rErr.Error(), "VOUCHER_USER_LIMIT_EXCEEDED"):
				ulCount.Add(1)
			default:
				other.Add(1)
			}
		}()
		wg.Wait()
		if okCount.Load() != 1 || ulCount.Load() != 1 || other.Load() != 0 {
			t.Fatalf("2-goroutine PG race after Redis wipe: want ok=1 ul=1 other=0, got ok=%d ul=%d other=%d", okCount.Load(), ulCount.Load(), other.Load())
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 9 {
			t.Fatalf("Redis voucher quota after 2-goroutine race compensation: got %d want 9", rem)
		}
		if gotV, _ := st.GetVoucherByID(context.Background(), vid); gotV.UsedCount != 1 {
			t.Fatalf("PG voucher used_count after 2-goroutine race: got %d want 1", gotV.UsedCount)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) {
		runSuite(t, store.NewMemoryStore(), "mem")
	})

	t.Run("PostgresStore", func(t *testing.T) {
		dsn := os.Getenv("TEST_PROMOTION_DB_URL")
		if dsn == "" {
			t.Skip("TEST_PROMOTION_DB_URL not set; run via scripts/test-db.sh")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("pgxpool.New: %v", err)
		}
		t.Cleanup(pool.Close)
		pgStore := store.NewPostgresStore(pool)
		if err := pgStore.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		runSuite(t, pgStore, uuid.NewString()[:8])
	})
}

type failRedisHook struct {
	mu  sync.RWMutex
	err error
}

func (h *failRedisHook) SetError(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
}

func (h *failRedisHook) currentErr() error {
	h.mu.RLock()
	err := h.err
	h.mu.RUnlock()
	return err
}

func (h *failRedisHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *failRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := h.currentErr(); err != nil {
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

func (h *failRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if err := h.currentErr(); err != nil {
			for _, cmd := range cmds {
				cmd.SetErr(err)
			}
			return err
		}
		return next(ctx, cmds)
	}
}

func TestService_W3A_VoucherRedisSyncFailure_SelfHealsAndRestartConverges(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	failHook := &failRedisHook{}
	rdb.AddHook(failHook)
	mem := store.NewMemoryStore()
	eng := lua.NewEngine(rdb)

	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	orderSvcCtx := ctxWithService("order-service")

	hooked := &postUpdateHookStore{Store: mem}
	svc := service.NewPromotionService(hooked, eng, logger.New("w3a-reconcile", logger.WithWriter(io.Discard)))
	svc.SetClock(func() time.Time { return now })

	// Create voucher quota=10, budget=100k, reserve 2 -> PG used=2, used_budget=40k; Redis rem=8, budget=60k.
	vResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "W3ARECON", Name: "Reconcile Voucher", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
		Quota: 10, PerUserLimit: 5, BudgetVnd: 100000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	vid := vResp.Voucher.Id
	for i := 0; i < 2; i++ {
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: fmt.Sprintf("w3a-rec-ord-%d", i), UserId: "buyer-rec",
			PlatformVoucherId: vid,
			ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate deterministic client-side Redis error right AFTER PG UpdateVoucher commits (quota 10->15, budget 100k->150k).
	hooked.onAfterUpdate = func(_ *domain.Voucher) {
		failHook.SetError(errors.New("simulated redis write failure"))
	}
	_, updErr := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
		VoucherId: vid, Quota: i32Ptr(15), BudgetVnd: i64Ptr(150000),
	})
	failHook.SetError(nil)
	hooked.onAfterUpdate = nil
	if status.Code(updErr) != codes.Internal {
		t.Fatalf("UpdateVoucher when voucher Redis sync fails: want Internal, got %v", updErr)
	}
	// Redis is still stale (rem=8, budget=60k), while PG has quota=15, used=2 (want rem=13, budget=110k).
	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 8 {
		t.Fatalf("expected stale Redis rem=8 before reconcile, got %d", rem)
	}

	// Start background reconciler briefly -> MUST converge Redis to exact PG numbers (rem=13 <= quota 15, budget=110k)!
	recCtx, recCancel := context.WithCancel(context.Background())
	recDone := make(chan struct{})
	go func() {
		defer close(recDone)
		svc.StartRedisReconciler(recCtx, 20*time.Millisecond)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
		if rem == 13 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	recCancel()
	<-recDone

	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 13 {
		t.Fatalf("reconciler failed to converge Redis remaining to 13, got %d", rem)
	}
	if bud, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid)); bud != 110000 {
		t.Fatalf("reconciler failed to converge Redis budget to 110000, got %d", bud)
	}

	// Even if Redis drifted above quota (inflated at 18 > quota=15),
	// ReconcileVoucherRedisOnce converges back to exact PG remaining (14, never > quota)!
	_ = mr.Set(lua.VoucherRemainingKey(vid), "18")
	// Mark dirty again by raising quota 15 -> 16 with simulated Redis failure
	hooked.onAfterUpdate = func(_ *domain.Voucher) { failHook.SetError(errors.New("transient redis failure")) }
	_, _ = svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{VoucherId: vid, Quota: i32Ptr(16)})
	failHook.SetError(nil)
	hooked.onAfterUpdate = nil
	if n, err := svc.ReconcileVoucherRedisOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("ReconcileVoucherRedisOnce: want n=1 err=nil, got n=%d err=%v", n, err)
	}
	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 14 {
		t.Fatalf("reconciler must set exact PG rem=14 (never > quota 16), got %d", rem)
	}

	// Restart convergence (SyncRedisFromStore): even if Redis has drifted higher than quota (999) and lost :user: key,
	// restarting PromotionService converges Redis to exact PG numbers (NOT stale SetNX!).
	_ = mr.Set(lua.VoucherRemainingKey(vid), "999")
	_ = mr.Set(lua.VoucherBudgetRemainingKey(vid), "999999")
	mr.Del(lua.VoucherUserKey(vid, "buyer-rec"))

	restarted := service.NewPromotionService(mem, eng, logger.New("w3a-restart", logger.WithWriter(io.Discard)))
	restarted.SetClock(func() time.Time { return now })
	if err := restarted.SyncRedisFromStore(context.Background()); err != nil {
		t.Fatalf("SyncRedisFromStore on restart: %v", err)
	}
	if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 14 {
		t.Fatalf("SyncRedisFromStore must converge stale 999 -> 14, got %d", rem)
	}
	if bud, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid)); bud != 110000 {
		t.Fatalf("SyncRedisFromStore must converge stale budget -> 110000, got %d", bud)
	}
	// Under D12, SyncRedisFromStore does NOT bulk-load :user: keys; instead buildCalcInput takes max(Redis, PG)
	// and PG CommitReservation enforces per_user_limit=5, so buyer-rec (PG used=2) can reserve 3 more and the 4th fails.
	for i := 0; i < 3; i++ {
		if _, err := restarted.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: fmt.Sprintf("w3a-rec-post-restart-%d", i), UserId: "buyer-rec",
			PlatformVoucherId: vid,
			ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("post-restart reserve #%d (within PG per_user_limit=5): %v", i, err)
		}
	}
	mr.Del(lua.VoucherUserKey(vid, "buyer-rec"))
	if _, err := restarted.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "w3a-rec-post-restart-over", UserId: "buyer-rec",
		PlatformVoucherId: vid,
		ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("6th reserve after :user: key deleted must still fail via PG max(Redis, PG): got %v", err)
	}
}
