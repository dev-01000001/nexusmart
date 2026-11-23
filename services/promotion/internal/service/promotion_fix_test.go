package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
)

func ctxWithService(serviceName string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: serviceName,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: serviceName,
		},
	})
}

func TestService_ReleasePromotionShop_SplitsByShop_AndReleasesPlatformOnLastShop(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Shop vouchers for shop-1 and shop-2
	shop1V, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "S1_15K",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    75000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create shop1 voucher: %v", err)
	}
	shop2V, err := svc.CreateVoucher(seller2Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "S2_10K",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-2",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    50000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create shop2 voucher: %v", err)
	}
	platV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "PLAT_20K",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    100000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create platform voucher: %v", err)
	}
	shipV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "SHIP_10K",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    50000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create freeship voucher: %v", err)
	}

	// Flash sale items for shop-1 and shop-2
	fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Multi-Shop Flash",
		StartsAt: timestamppb.New(now.Add(-time.Hour)),
		EndsAt:   timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create flash sale: %v", err)
	}
	fi1Resp, err := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsResp.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-1",
		FlashPriceVnd: 150000,
		TotalSlots:    10,
		PerUserLimit:  5,
	})
	if err != nil {
		t.Fatalf("register fi1: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi1Resp.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve fi1: %v", err)
	}
	fi2Resp, err := svc.RegisterFlashSaleItem(seller2Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsResp.FlashSale.Id,
		ShopId:        "shop-2",
		SkuId:         "sku-2",
		FlashPriceVnd: 200000,
		TotalSlots:    10,
		PerUserLimit:  5,
	})
	if err != nil {
		t.Fatalf("register fi2: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi2Resp.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve fi2: %v", err)
	}

	// Credit 20,000 coins
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-1",
		Amount:      20000,
		Reason:      "test_init",
		ReferenceId: "split-init-1",
	}); err != nil {
		t.Fatalf("credit coins: %v", err)
	}

	// Reserve promotions for a 2-shop checkout group
	const groupID = "grp-split-01"
	resResp, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: groupID,
		UserId:  "buyer-1",
		ShopVoucherIds: map[string]string{
			"shop-1": shop1V.Voucher.Id,
			"shop-2": shop2V.Voucher.Id,
		},
		PlatformVoucherId: platV.Voucher.Id,
		FreeshipVoucherId: shipV.Voucher.Id,
		CoinsToUse:        10000,
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: fi1Resp.Item.Id, SkuId: "sku-1", Quantity: 2, ShopId: "shop-1"},
			{FlashSaleItemId: fi2Resp.Item.Id, SkuId: "sku-2", Quantity: 1, ShopId: "shop-2"},
		},
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         "shop-1",
				SubtotalVnd:    400000,
				ShippingFeeVnd: 20000,
				Items:          []*promotionv1.DiscountLineItem{{SkuId: "sku-1", Quantity: 2, UnitPriceVnd: 200000, FlashSaleItemId: fi1Resp.Item.Id}},
			},
			{
				ShopId:         "shop-2",
				SubtotalVnd:    300000,
				ShippingFeeVnd: 20000,
				Items:          []*promotionv1.DiscountLineItem{{SkuId: "sku-2", Quantity: 1, UnitPriceVnd: 300000, FlashSaleItemId: fi2Resp.Item.Id}},
			},
		},
	})
	if err != nil {
		t.Fatalf("ReservePromotion: %v", err)
	}

	// Verify shop_id is persisted on reserved flash items (Item 2)
	savedRes, err := mem.GetReservation(context.Background(), groupID)
	if err != nil {
		t.Fatalf("GetReservation: %v", err)
	}
	flashShopMap := make(map[string]string)
	for _, rf := range savedRes.FlashSaleItems {
		flashShopMap[rf.FlashSaleItemID] = rf.ShopID
	}
	if flashShopMap[fi1Resp.Item.Id] != "shop-1" || flashShopMap[fi2Resp.Item.Id] != "shop-2" {
		t.Fatalf("expected persisted shop_id on flash items, got %+v", flashShopMap)
	}

	var shop1Coins, shop2Coins int64
	for _, sb := range resResp.Breakdown.Shops {
		switch sb.ShopId {
		case "shop-1":
			shop1Coins = sb.CoinsDiscountVnd
		case "shop-2":
			shop2Coins = sb.CoinsDiscountVnd
		}
	}
	if shop1Coins <= 0 || shop2Coins <= 0 || shop1Coins+shop2Coins != 10000 {
		t.Fatalf("unexpected coin split: shop1=%d shop2=%d", shop1Coins, shop2Coins)
	}

	// 1. Release shop-1 only
	rel1, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: groupID,
		ShopId:  "shop-1",
		Reason:  "buyer_cancelled_suborder",
	})
	if err != nil {
		t.Fatalf("ReleasePromotionShop(shop-1): %v", err)
	}
	if !rel1.Released || rel1.AlreadyReleased {
		t.Fatalf("expected released=true already_released=false, got %+v", rel1)
	}
	if rel1.CoinsReturned != shop1Coins {
		t.Fatalf("expected coins_returned=%d, got %d", shop1Coins, rel1.CoinsReturned)
	}
	if !rel1.ShopVoucherReleased {
		t.Fatalf("expected shop_voucher_released=true")
	}
	if rel1.FlashSaleQuantityReleased != 2 {
		t.Fatalf("expected flash_sale_quantity_released=2, got %d", rel1.FlashSaleQuantityReleased)
	}
	if rel1.PlatformVoucherReleased {
		t.Fatalf("expected platform_voucher_released=false while shop-2 is still active")
	}

	// Verify coins, shop-1 voucher, fi-1 restored; shop-2 voucher, plat/ship vouchers, fi-2 still held
	wantBalAfter1 := 10000 + shop1Coins
	if mem.AccountBalance("buyer-1") != wantBalAfter1 || mem.LedgerSum("buyer-1") != wantBalAfter1 {
		t.Fatalf("coin invariant mismatch after shop-1 release: acct=%d sum=%d want=%d",
			mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"), wantBalAfter1)
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shop1V.Voucher.Id)); q != 5 {
		t.Fatalf("expected shop-1 voucher Redis quota=5, got %d", q)
	}
	if b, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(shop1V.Voucher.Id)); b != 75000 {
		t.Fatalf("expected shop-1 voucher Redis budget=75000, got %d", b)
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shop2V.Voucher.Id)); q != 4 {
		t.Fatalf("expected shop-2 voucher Redis quota still 4, got %d", q)
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(platV.Voucher.Id)); q != 4 {
		t.Fatalf("expected platform voucher Redis quota still 4, got %d", q)
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shipV.Voucher.Id)); q != 4 {
		t.Fatalf("expected freeship voucher Redis quota still 4, got %d", q)
	}
	if s1, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi1Resp.Item.Id)); s1 != 10 {
		t.Fatalf("expected fi1 Redis slots=10, got %d", s1)
	}
	if s2, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi2Resp.Item.Id)); s2 != 9 {
		t.Fatalf("expected fi2 Redis slots still 9, got %d", s2)
	}
	if v1DB, _ := mem.GetVoucherByIDOrCode(context.Background(), shop1V.Voucher.Id); v1DB.UsedCount != 0 {
		t.Fatalf("expected shop-1 voucher DB used_count=0, got %d", v1DB.UsedCount)
	}
	if vpDB, _ := mem.GetVoucherByIDOrCode(context.Background(), platV.Voucher.Id); vpDB.UsedCount != 1 {
		t.Fatalf("expected platform voucher DB used_count still 1, got %d", vpDB.UsedCount)
	}
	if resMid, _ := mem.GetReservation(context.Background(), groupID); resMid.Status != domain.ReservationStatusReserved {
		t.Fatalf("expected reservation still RESERVED, got %s", resMid.Status)
	}

	// 2. Idempotent 2nd call for shop-1 -> no double release
	rel1Dup, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: groupID,
		ShopId:  "shop-1",
		Reason:  "duplicate_retry",
	})
	if err != nil {
		t.Fatalf("2nd ReleasePromotionShop(shop-1): %v", err)
	}
	if !rel1Dup.Released || !rel1Dup.AlreadyReleased || rel1Dup.CoinsReturned != 0 || rel1Dup.ShopVoucherReleased || rel1Dup.FlashSaleQuantityReleased != 0 || rel1Dup.PlatformVoucherReleased {
		t.Fatalf("2nd call should be idempotent no-op, got %+v", rel1Dup)
	}
	if mem.AccountBalance("buyer-1") != wantBalAfter1 || mem.LedgerSum("buyer-1") != wantBalAfter1 {
		t.Fatalf("coins changed on duplicate shop-1 release: acct=%d sum=%d", mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shop1V.Voucher.Id)); q != 5 {
		t.Fatalf("shop-1 voucher quota changed on duplicate release: %d", q)
	}
	if s1, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi1Resp.Item.Id)); s1 != 10 {
		t.Fatalf("fi1 slots changed on duplicate release: %d", s1)
	}

	// 3. Release shop-2 (last shop) -> releases shop-2 share + platform & freeship vouchers -> status RELEASED
	rel2, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: groupID,
		ShopId:  "shop-2",
		Reason:  "buyer_cancelled_last_suborder",
	})
	if err != nil {
		t.Fatalf("ReleasePromotionShop(shop-2): %v", err)
	}
	if !rel2.Released || rel2.AlreadyReleased {
		t.Fatalf("expected released=true already_released=false for shop-2, got %+v", rel2)
	}
	if rel2.CoinsReturned != shop2Coins || !rel2.ShopVoucherReleased || rel2.FlashSaleQuantityReleased != 1 || !rel2.PlatformVoucherReleased {
		t.Fatalf("unexpected shop-2 release response: %+v", rel2)
	}

	if mem.AccountBalance("buyer-1") != 20000 || mem.LedgerSum("buyer-1") != 20000 {
		t.Fatalf("expected full 20000 coins restored, got acct=%d sum=%d", mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shop2V.Voucher.Id)); q != 5 {
		t.Fatalf("expected shop-2 voucher Redis quota=5, got %d", q)
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(platV.Voucher.Id)); q != 5 {
		t.Fatalf("expected platform voucher Redis quota=5, got %d", q)
	}
	if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shipV.Voucher.Id)); q != 5 {
		t.Fatalf("expected freeship voucher Redis quota=5, got %d", q)
	}
	if s2, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi2Resp.Item.Id)); s2 != 10 {
		t.Fatalf("expected fi2 Redis slots=10, got %d", s2)
	}
	if vpDB, _ := mem.GetVoucherByIDOrCode(context.Background(), platV.Voucher.Id); vpDB.UsedCount != 0 {
		t.Fatalf("expected platform voucher DB used_count=0, got %d", vpDB.UsedCount)
	}
	if resFinal, _ := mem.GetReservation(context.Background(), groupID); resFinal.Status != domain.ReservationStatusReleased {
		t.Fatalf("expected reservation RELEASED after all shops released, got %s", resFinal.Status)
	}

	// 4. Subsequent full ReleasePromotion must be idempotent no-op
	fullRel, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: groupID})
	if err != nil || !fullRel.Released || !fullRel.AlreadyReleased {
		t.Fatalf("expected ReleasePromotion after all shops released to be already_released=true, got %+v err=%v", fullRel, err)
	}
	if mem.AccountBalance("buyer-1") != 20000 || mem.LedgerSum("buyer-1") != 20000 {
		t.Fatalf("coins drifted after redundant ReleasePromotion: acct=%d sum=%d", mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
}

func TestService_ReleasePromotion_AfterPartialShopRelease_DoesNotDoubleRelease(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	shop1V, _ := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "PART_S1",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    75000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	shop2V, _ := svc.CreateVoucher(seller2Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "PART_S2",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-2",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    50000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	platV, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "PART_PLAT",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		Quota:        5,
		PerUserLimit: 2,
		BudgetVnd:    100000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})

	fsResp, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Partial Then Full Flash",
		StartsAt: timestamppb.New(now.Add(-time.Hour)),
		EndsAt:   timestamppb.New(now.Add(time.Hour)),
	})
	fi1Resp, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsResp.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-a", FlashPriceVnd: 100000, TotalSlots: 10, PerUserLimit: 5,
	})
	_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi1Resp.Item.Id, Approve: true})
	fi2Resp, _ := svc.RegisterFlashSaleItem(seller2Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsResp.FlashSale.Id, ShopId: "shop-2", SkuId: "sku-b", FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 5,
	})
	_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi2Resp.Item.Id, Approve: true})

	_, _ = svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-1", Amount: 20000, ReferenceId: "part-full-init",
	})

	const groupID = "grp-partial-then-full"
	_, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: groupID,
		UserId:  "buyer-1",
		ShopVoucherIds: map[string]string{
			"shop-1": shop1V.Voucher.Id,
			"shop-2": shop2V.Voucher.Id,
		},
		PlatformVoucherId: platV.Voucher.Id,
		CoinsToUse:        10000,
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: fi1Resp.Item.Id, SkuId: "sku-a", Quantity: 2, ShopId: "shop-1"},
			{FlashSaleItemId: fi2Resp.Item.Id, SkuId: "sku-b", Quantity: 3, ShopId: "shop-2"},
		},
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-1", SubtotalVnd: 300000, ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-a", Quantity: 2, UnitPriceVnd: 150000, FlashSaleItemId: fi1Resp.Item.Id}}},
			{ShopId: "shop-2", SubtotalVnd: 450000, ShippingFeeVnd: 20000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-b", Quantity: 3, UnitPriceVnd: 150000, FlashSaleItemId: fi2Resp.Item.Id}}},
		},
	})
	if err != nil {
		t.Fatalf("ReservePromotion: %v", err)
	}

	// Step 1: Partial release of shop-1
	if _, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: groupID,
		ShopId:  "shop-1",
	}); err != nil {
		t.Fatalf("ReleasePromotionShop(shop-1): %v", err)
	}

	// Step 2: Full ReleasePromotion (simulates order cancelling last active sibling via ReleasePromotion)
	fullRel, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: groupID})
	if err != nil || !fullRel.Released || fullRel.AlreadyReleased {
		t.Fatalf("ReleasePromotion after partial release unexpected: %+v err=%v", fullRel, err)
	}

	// Must restore EXACT initial balances/quotas/slots — zero double release!
	if mem.AccountBalance("buyer-1") != 20000 || mem.LedgerSum("buyer-1") != 20000 {
		t.Fatalf("double coin release detected: acct=%d sum=%d want=20000", mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
	for name, vid := range map[string]string{"shop-1": shop1V.Voucher.Id, "shop-2": shop2V.Voucher.Id, "platform": platV.Voucher.Id} {
		if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); q != 5 {
			t.Fatalf("voucher %s Redis quota drift: got %d want 5", name, q)
		}
		if vDB, _ := mem.GetVoucherByIDOrCode(context.Background(), vid); vDB.UsedCount != 0 || vDB.UsedBudgetVND != 0 {
			t.Fatalf("voucher %s DB drift: used=%d budget=%d", name, vDB.UsedCount, vDB.UsedBudgetVND)
		}
	}
	for name, fid := range map[string]string{"fi-1": fi1Resp.Item.Id, "fi-2": fi2Resp.Item.Id} {
		if s, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); s != 10 {
			t.Fatalf("flash %s Redis slots drift: got %d want 10", name, s)
		}
		if fiDB, _ := mem.GetFlashSaleItem(context.Background(), fid); fiDB.RemainingSlots != 10 {
			t.Fatalf("flash %s DB slots drift: got %d want 10", name, fiDB.RemainingSlots)
		}
	}
}

func TestService_ReleasePromotionShop_LegacyFlashItemWithoutShopID(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")

	fsResp, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Legacy Flash",
		StartsAt: timestamppb.New(now.Add(-time.Hour)),
		EndsAt:   timestamppb.New(now.Add(time.Hour)),
	})
	fi1Resp, _ := svc.RegisterFlashSaleItem(seller1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsResp.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-legacy", FlashPriceVnd: 90000, TotalSlots: 10, PerUserLimit: 5,
	})
	_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi1Resp.Item.Id, Approve: true})

	// Simulate a legacy reservation saved before flash_sale_items_json included shop_id
	const groupID = "grp-legacy-flash"
	if _, err := eng.Reserve(context.Background(), lua.ReserveInput{
		OrderID: groupID,
		UserID:  "buyer-legacy",
		FlashItems: []lua.FlashReserveSpec{
			{ItemID: fi1Resp.Item.Id, Quantity: 2, PerUserLimit: 5},
		},
	}); err != nil {
		t.Fatalf("lua reserve: %v", err)
	}
	bdBytes, _ := protojson.Marshal(&promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{ShopId: "shop-1", SubtotalVnd: 200000, FlashSaleDiscountVnd: 20000, FinalPayableVnd: 180000},
			{ShopId: "shop-2", SubtotalVnd: 100000, FinalPayableVnd: 100000},
		},
	})
	if _, _, err := mem.CommitReservation(context.Background(), domain.PromotionReservation{
		OrderID: groupID,
		UserID:  "buyer-legacy",
		Status:  domain.ReservationStatusReserved,
		FlashSaleItems: []domain.ReservedFlashItem{
			// ShopID intentionally omitted to simulate legacy DB row
			{FlashSaleItemID: fi1Resp.Item.Id, SKUID: "sku-legacy", Quantity: 2},
		},
		BreakdownJSON: bdBytes,
	}); err != nil {
		t.Fatalf("commit legacy reservation: %v", err)
	}

	rel, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: groupID,
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("ReleasePromotionShop(legacy): %v", err)
	}
	if !rel.Released || rel.AlreadyReleased || rel.FlashSaleQuantityReleased != 2 {
		t.Fatalf("expected legacy flash fallback to release qty=2, got %+v", rel)
	}
	if s, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fi1Resp.Item.Id)); s != 10 {
		t.Fatalf("expected fi1 Redis slots restored to 10, got %d", s)
	}
	if fiDB, _ := mem.GetFlashSaleItem(context.Background(), fi1Resp.Item.Id); fiDB.RemainingSlots != 10 {
		t.Fatalf("expected fi1 DB slots restored to 10, got %d", fiDB.RemainingSlots)
	}
}

func TestService_PRM_B4_AuthEnforcement(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Now().UTC()
	anonCtx := context.Background()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")
	buyer1Ctx := ctxWithRole("buyer-1", "buyer", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	vResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "AUTHV1",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        10,
		PerUserLimit: 2,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("create voucher: %v", err)
	}

	// 1. CreditCoins: ONLY admin or service token
	if _, err := svc.CreditCoins(anonCtx, &promotionv1.CreditCoinsRequest{UserId: "buyer-1", Amount: 1000}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon CreditCoins: want Unauthenticated, got %v", err)
	}
	if _, err := svc.CreditCoins(buyer1Ctx, &promotionv1.CreditCoinsRequest{UserId: "buyer-1", Amount: 1000}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer CreditCoins: want PermissionDenied, got %v", err)
	}
	if _, err := svc.CreditCoins(sellerCtx, &promotionv1.CreditCoinsRequest{UserId: "buyer-1", Amount: 1000}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller CreditCoins: want PermissionDenied, got %v", err)
	}
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{UserId: "buyer-1", Amount: 1000, ReferenceId: "auth-admin-1"}); err != nil {
		t.Fatalf("admin CreditCoins should succeed: %v", err)
	}
	if _, err := svc.CreditCoins(svcCtx, &promotionv1.CreditCoinsRequest{UserId: "buyer-1", Amount: 2000, ReferenceId: "auth-svc-1"}); err != nil {
		t.Fatalf("service token CreditCoins should succeed: %v", err)
	}

	// 2. SaveVoucher: requires end-user claims (rejects anon and service token)
	if _, err := svc.SaveVoucher(anonCtx, &promotionv1.SaveVoucherRequest{UserId: "buyer-1", VoucherId: vResp.Voucher.Id}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon SaveVoucher: want Unauthenticated, got %v", err)
	}
	if _, err := svc.SaveVoucher(svcCtx, &promotionv1.SaveVoucherRequest{UserId: "buyer-1", VoucherId: vResp.Voucher.Id}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token SaveVoucher: want PermissionDenied, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyer1Ctx, &promotionv1.SaveVoucherRequest{UserId: "buyer-2", VoucherId: vResp.Voucher.Id}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user SaveVoucher: want PermissionDenied, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyer1Ctx, &promotionv1.SaveVoucherRequest{UserId: "buyer-1", VoucherId: vResp.Voucher.Id}); err != nil {
		t.Fatalf("buyer own SaveVoucher should succeed: %v", err)
	}

	// 3. GetCoinBalance: requires end-user claims (rejects anon and service token)
	if _, err := svc.GetCoinBalance(anonCtx, &promotionv1.GetCoinBalanceRequest{UserId: "buyer-1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon GetCoinBalance: want Unauthenticated, got %v", err)
	}
	if _, err := svc.GetCoinBalance(svcCtx, &promotionv1.GetCoinBalanceRequest{UserId: "buyer-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token GetCoinBalance: want PermissionDenied, got %v", err)
	}
	if _, err := svc.GetCoinBalance(buyer1Ctx, &promotionv1.GetCoinBalanceRequest{UserId: "buyer-2"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user GetCoinBalance: want PermissionDenied, got %v", err)
	}
	bal, err := svc.GetCoinBalance(buyer1Ctx, &promotionv1.GetCoinBalanceRequest{})
	if err != nil || bal.Balance != 3000 {
		t.Fatalf("buyer own GetCoinBalance: got bal=%+v err=%v", bal, err)
	}

	// 4. PreviewDiscounts: requires end-user claims (rejects anon and service token)
	previewReq := &promotionv1.PreviewDiscountsRequest{
		UserId: "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-1", SubtotalVnd: 100000, ShippingFeeVnd: 20000},
		},
		PlatformVoucherId: vResp.Voucher.Id,
	}
	if _, err := svc.PreviewDiscounts(anonCtx, previewReq); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon PreviewDiscounts: want Unauthenticated, got %v", err)
	}
	if _, err := svc.PreviewDiscounts(svcCtx, previewReq); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("service token PreviewDiscounts: want PermissionDenied, got %v", err)
	}
	if _, err := svc.PreviewDiscounts(buyer1Ctx, &promotionv1.PreviewDiscountsRequest{
		UserId:    "buyer-2",
		ShopItems: previewReq.ShopItems,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("cross-user PreviewDiscounts: want PermissionDenied, got %v", err)
	}
	if _, err := svc.PreviewDiscounts(buyer1Ctx, previewReq); err != nil {
		t.Fatalf("buyer own PreviewDiscounts should succeed: %v", err)
	}

	// 5. ReleasePromotionShop: rejects anon and non-admin user; accepts service token
	if _, err := svc.ReleasePromotionShop(anonCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: "o1", ShopId: "s1"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon ReleasePromotionShop: want Unauthenticated, got %v", err)
	}
	if _, err := svc.ReleasePromotionShop(buyer1Ctx, &promotionv1.ReleasePromotionShopRequest{OrderId: "o1", ShopId: "s1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ReleasePromotionShop: want PermissionDenied, got %v", err)
	}
}

func TestService_PRM_B8_ListAvailableVouchers_FiltersUnavailableAndMatchesPreview(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyer1Ctx := ctxWithRole("buyer-1", "buyer", "")
	buyer2Ctx := ctxWithRole("buyer-2", "buyer", "")

	// 1. Expired voucher -> MUST be filtered out
	if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "B8_EXPIRED",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        10,
		StartsAt:     timestamppb.New(now.Add(-2 * time.Hour)),
		EndsAt:       timestamppb.New(now.Add(-1 * time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	// 2. Upcoming (not started yet) voucher -> MUST be filtered out
	if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "B8_UPCOMING",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        10,
		StartsAt:     timestamppb.New(now.Add(1 * time.Hour)),
		EndsAt:       timestamppb.New(now.Add(2 * time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	// 3. Out-of-quota voucher -> MUST be filtered out
	vQuota, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "B8_QUOTA0",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        1,
		PerUserLimit: 1,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId:           "b8-quota-use",
		UserId:            "buyer-1",
		PlatformVoucherId: vQuota.Voucher.Id,
	}); err != nil {
		t.Fatal(err)
	}

	// 4. Budget-exhausted-for-order voucher (exact PRM-B8 repro: 10%, max 15k, budget 20k; after 15k used, rem=5k < 15k)
	vBudget, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:            "B8_LOWBUDGET",
		Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType:    promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT,
		DiscountPercent: 10,
		MaxDiscountVnd:  15000,
		Quota:           10,
		PerUserLimit:    1,
		BudgetVnd:       20000,
		StartsAt:        timestamppb.New(now.Add(-time.Hour)),
		EndsAt:          timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId:           "b8-budget-use",
		UserId:            "buyer-1",
		PlatformVoucherId: vBudget.Voucher.Id,
		ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 20000}},
	}); err != nil {
		t.Fatal(err)
	}

	// 5. Per-user-limit-reached voucher for buyer-1 (still available for buyer-2)
	vPerUser, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "B8_PERUSER",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        10,
		PerUserLimit: 1,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId:           "b8-peruser-use",
		UserId:            "buyer-1",
		PlatformVoucherId: vPerUser.Voucher.Id,
	}); err != nil {
		t.Fatal(err)
	}

	// 6. Active eligible voucher with min_order_vnd = 150000
	if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "B8_ACTIVE",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		MinOrderVnd:  150000,
		Quota:        10,
		PerUserLimit: 2,
		BudgetVnd:    150000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	// For buyer-2 with order_amount_vnd=200000: ONLY B8_PERUSER and B8_ACTIVE should be returned!
	// B8_EXPIRED, B8_UPCOMING, B8_QUOTA0, and B8_LOWBUDGET (needs 15k > rem 5k) MUST be excluded.
	list2, err := svc.ListAvailableVouchers(buyer2Ctx, &promotionv1.ListAvailableVouchersRequest{
		UserId:         "buyer-2",
		Scope:          promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		OrderAmountVnd: 200000,
	})
	if err != nil {
		t.Fatalf("ListAvailableVouchers(buyer-2): %v", err)
	}
	got2 := make(map[string]*promotionv1.Voucher)
	for _, v := range list2.Vouchers {
		got2[v.Code] = v
	}
	for _, excluded := range []string{"B8_EXPIRED", "B8_UPCOMING", "B8_QUOTA0", "B8_LOWBUDGET"} {
		if _, found := got2[excluded]; found {
			t.Fatalf("voucher %s should be excluded from ListAvailableVouchers for 200k order", excluded)
		}
	}
	if len(got2) != 2 || got2["B8_ACTIVE"] == nil || got2["B8_PERUSER"] == nil {
		t.Fatalf("expected only B8_ACTIVE and B8_PERUSER for buyer-2 at 200k, got %v", got2)
	}

	// Verify PreviewDiscounts agrees that B8_LOWBUDGET fails at 200k
	prev200, err := svc.PreviewDiscounts(buyer2Ctx, &promotionv1.PreviewDiscountsRequest{
		UserId:            "buyer-2",
		PlatformVoucherId: vBudget.Voucher.Id,
		ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 20000}},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts(200k): %v", err)
	}
	if prev200.Breakdown.TotalPlatformVoucherDiscountVnd != 0 {
		t.Fatalf("expected 0 discount for exhausted budget at 200k, got %d", prev200.Breakdown.TotalPlatformVoucherDiscountVnd)
	}

	// For buyer-2 with order_amount_vnd=40000: B8_LOWBUDGET needs 4k <= rem 5k so it IS included and eligible (matching PreviewDiscounts!)
	listSmall, err := svc.ListAvailableVouchers(buyer2Ctx, &promotionv1.ListAvailableVouchersRequest{
		UserId:         "buyer-2",
		Scope:          promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		OrderAmountVnd: 40000,
	})
	if err != nil {
		t.Fatalf("ListAvailableVouchers(40k): %v", err)
	}
	gotSmall := make(map[string]*promotionv1.Voucher)
	for _, v := range listSmall.Vouchers {
		gotSmall[v.Code] = v
	}
	if gotSmall["B8_LOWBUDGET"] == nil || !gotSmall["B8_LOWBUDGET"].Eligible {
		t.Fatalf("expected B8_LOWBUDGET included & eligible at 40k (4k <= 5k rem budget), got %+v", gotSmall["B8_LOWBUDGET"])
	}
	if gotSmall["B8_ACTIVE"] == nil || gotSmall["B8_ACTIVE"].Eligible {
		t.Fatalf("expected B8_ACTIVE included with eligible=false (shortfall) at 40k, got %+v", gotSmall["B8_ACTIVE"])
	}

	// For buyer-1 at 200k: B8_PERUSER is also excluded because buyer-1 already reached per_user_limit=1
	list1, err := svc.ListAvailableVouchers(buyer1Ctx, &promotionv1.ListAvailableVouchersRequest{
		UserId:         "buyer-1",
		Scope:          promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		OrderAmountVnd: 200000,
	})
	if err != nil {
		t.Fatalf("ListAvailableVouchers(buyer-1): %v", err)
	}
	if len(list1.Vouchers) != 1 || list1.Vouchers[0].Code != "B8_ACTIVE" {
		t.Fatalf("expected only B8_ACTIVE for buyer-1 at 200k, got %+v", list1.Vouchers)
	}
}

func TestService_ConcurrentReleasePromotionShop_TwoShops_ExactRestore(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-conc", "buyer", "")

	s1V, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code: "CONC_S1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 5, BudgetVnd: 50000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	s2V, err := svc.CreateVoucher(seller2Ctx, &promotionv1.CreateVoucherRequest{
		Code: "CONC_S2", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-2",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 5, BudgetVnd: 50000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	pV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "CONC_PLAT", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
		Quota: 5, PerUserLimit: 5, BudgetVnd: 100000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-conc", Amount: 15000, ReferenceId: "conc-init",
	}); err != nil {
		t.Fatal(err)
	}

	const groupID = "grp-conc-release"
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: groupID, UserId: "buyer-conc",
		ShopVoucherIds:    map[string]string{"shop-1": s1V.Voucher.Id, "shop-2": s2V.Voucher.Id},
		PlatformVoucherId: pV.Voucher.Id,
		CoinsToUse:        10000,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 20000},
			{ShopId: "shop-2", SubtotalVnd: 200000, ShippingFeeVnd: 20000},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Release both shops concurrently (plus duplicate retries) under -race
	start := make(chan struct{})
	errCh := make(chan error, 4)
	for _, sid := range []string{"shop-1", "shop-2", "shop-1", "shop-2"} {
		shopID := sid
		go func() {
			<-start
			_, rErr := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
				OrderId: groupID,
				ShopId:  shopID,
			})
			errCh <- rErr
		}()
	}
	close(start)
	for i := 0; i < 4; i++ {
		if rErr := <-errCh; rErr != nil {
			t.Fatalf("concurrent ReleasePromotionShop failed: %v", rErr)
		}
	}

	if mem.AccountBalance("buyer-conc") != 15000 || mem.LedgerSum("buyer-conc") != 15000 {
		t.Fatalf("concurrent coin restore drift: acct=%d sum=%d want=15000", mem.AccountBalance("buyer-conc"), mem.LedgerSum("buyer-conc"))
	}
	for name, vid := range map[string]string{"s1": s1V.Voucher.Id, "s2": s2V.Voucher.Id, "plat": pV.Voucher.Id} {
		if q, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); q != 5 {
			t.Fatalf("concurrent %s Redis quota drift: got %d want 5", name, q)
		}
		if vDB, _ := mem.GetVoucherByIDOrCode(context.Background(), vid); vDB.UsedCount != 0 {
			t.Fatalf("concurrent %s DB used_count drift: got %d want 0", name, vDB.UsedCount)
		}
	}
	if res, _ := mem.GetReservation(context.Background(), groupID); res.Status != domain.ReservationStatusReleased {
		t.Fatalf("expected reservation RELEASED, got %s", res.Status)
	}
}

func TestService_ReviewA_P1_5_SellerManageViewIncludesAllOwnShopVouchers(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithRole("seller-2", "seller", "shop-2")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// Active shop-1 voucher
	if _, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code: "P15_ACTIVE", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	// Expired shop-1 voucher
	if _, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code: "P15_EXPIRED", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-2 * time.Hour)), EndsAt: timestamppb.New(now.Add(-time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	// Upcoming shop-1 voucher
	if _, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code: "P15_UPCOMING", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	// Exhausted quota shop-1 voucher
	vQuota, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code: "P15_QUOTA0", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 1, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "p15-use-quota", UserId: "buyer-1",
		ShopVoucherIds: map[string]string{"shop-1": vQuota.Voucher.Id},
	}); err != nil {
		t.Fatal(err)
	}
	// Budget-exhausted shop-1 voucher (fixed 20k, budget 30k, rem 10k < 20k)
	vBud, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code: "P15_LOWBUD", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
		Quota: 5, PerUserLimit: 5, BudgetVnd: 30000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyerCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "p15-use-bud", UserId: "buyer-1",
		ShopVoucherIds: map[string]string{"shop-1": vBud.Voucher.Id},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}); err != nil {
		t.Fatal(err)
	}
	// Another shop's expired voucher
	if _, err := svc.CreateVoucher(seller2Ctx, &promotionv1.CreateVoucherRequest{
		Code: "P15_S2_EXP", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-2",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 5, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-2 * time.Hour)), EndsAt: timestamppb.New(now.Add(-time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Seller management view (GET /api/v1/seller/vouchers shape) MUST include ALL 5 shop-1 vouchers
	sellerManageResp, err := svc.ListAvailableVouchers(seller1Ctx, &promotionv1.ListAvailableVouchersRequest{
		ShopId: "shop-1",
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
	})
	if err != nil {
		t.Fatalf("seller manage ListAvailableVouchers: %v", err)
	}
	gotCodes := make(map[string]bool)
	for _, v := range sellerManageResp.Vouchers {
		gotCodes[v.Code] = true
	}
	for _, want := range []string{"P15_ACTIVE", "P15_EXPIRED", "P15_UPCOMING", "P15_QUOTA0", "P15_LOWBUD"} {
		if !gotCodes[want] {
			t.Fatalf("seller management view missing voucher %s; got %v", want, gotCodes)
		}
	}

	// 2. Seller calling public buyer list (UserId populated like GET /api/v1/vouchers) still filters to active only
	sellerBuyerView, err := svc.ListAvailableVouchers(seller1Ctx, &promotionv1.ListAvailableVouchersRequest{
		UserId: "seller-1",
		ShopId: "shop-1",
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
	})
	if err != nil {
		t.Fatalf("seller buyer-view ListAvailableVouchers: %v", err)
	}
	if len(sellerBuyerView.Vouchers) != 1 || sellerBuyerView.Vouchers[0].Code != "P15_ACTIVE" {
		t.Fatalf("expected only P15_ACTIVE when UserId is set, got %+v", sellerBuyerView.Vouchers)
	}

	// 3. Seller viewing ANOTHER shop's vouchers must NOT bypass buyer availability filters
	otherShopView, err := svc.ListAvailableVouchers(seller1Ctx, &promotionv1.ListAvailableVouchersRequest{
		ShopId: "shop-2",
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
	})
	if err != nil {
		t.Fatalf("seller other-shop ListAvailableVouchers: %v", err)
	}
	if len(otherShopView.Vouchers) != 0 {
		t.Fatalf("expected expired voucher of another shop to be filtered out, got %+v", otherShopView.Vouchers)
	}

	// 4. Buyer viewing shop-1 vouchers still sees ONLY P15_ACTIVE
	buyerResp, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{
		ShopId: "shop-1",
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
	})
	if err != nil {
		t.Fatalf("buyer ListAvailableVouchers: %v", err)
	}
	if len(buyerResp.Vouchers) != 1 || buyerResp.Vouchers[0].Code != "P15_ACTIVE" {
		t.Fatalf("expected only P15_ACTIVE for buyer, got %+v", buyerResp.Vouchers)
	}
}

func TestService_ReviewA_P1_6_ListAvailableVouchers_RejectsSpoofedUserID(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	svcCtx := ctxWithService("order-service")
	buyer1Ctx := ctxWithRole("buyer-1", "buyer", "")
	buyer2Ctx := ctxWithRole("buyer-2", "buyer", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	vSaved, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "P16_SAVED", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 10, PerUserLimit: 2, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	vLimit1, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "P16_LIM1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 10, PerUserLimit: 1, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// buyer-1 saves vSaved and exhausts per_user_limit on vLimit1
	if _, err := svc.SaveVoucher(buyer1Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vSaved.Voucher.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId: "p16-use-lim1", UserId: "buyer-1", PlatformVoucherId: vLimit1.Voucher.Id,
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Anonymous caller probing buyer-1 via req.UserId MUST be rejected with Unauthenticated
	if _, err := svc.ListAvailableVouchers(context.Background(), &promotionv1.ListAvailableVouchersRequest{
		UserId: "buyer-1",
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anon with req.UserId: want Unauthenticated, got %v", err)
	}

	// 2. Anonymous caller with empty req.UserId succeeds and leaks no buyer-1 saved/usage state
	pubResp, err := svc.ListAvailableVouchers(context.Background(), &promotionv1.ListAvailableVouchersRequest{
		Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
	})
	if err != nil {
		t.Fatalf("anon public ListAvailableVouchers: %v", err)
	}
	if len(pubResp.Vouchers) != 2 {
		t.Fatalf("expected 2 public vouchers, got %d", len(pubResp.Vouchers))
	}
	for _, v := range pubResp.Vouchers {
		if v.Saved {
			t.Fatalf("public list must never expose saved=true, got %+v", v)
		}
	}

	// 3. buyer-2 or seller-1 probing buyer-1 via req.UserId MUST be rejected with PermissionDenied
	if _, err := svc.ListAvailableVouchers(buyer2Ctx, &promotionv1.ListAvailableVouchersRequest{
		UserId: "buyer-1",
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer-2 probing buyer-1: want PermissionDenied, got %v", err)
	}
	if _, err := svc.ListAvailableVouchers(sellerCtx, &promotionv1.ListAvailableVouchersRequest{
		UserId: "buyer-1",
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller-1 probing buyer-1: want PermissionDenied, got %v", err)
	}

	// 4. Admin and service token CAN query for buyer-1
	for name, ctx := range map[string]context.Context{"admin": adminCtx, "service": svcCtx} {
		resp, err := svc.ListAvailableVouchers(ctx, &promotionv1.ListAvailableVouchersRequest{
			UserId: "buyer-1",
			Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		})
		if err != nil {
			t.Fatalf("%s ListAvailableVouchers(buyer-1): %v", name, err)
		}
		if len(resp.Vouchers) != 1 || resp.Vouchers[0].Code != "P16_SAVED" || !resp.Vouchers[0].Saved {
			t.Fatalf("%s expected only P16_SAVED (saved=true) for buyer-1, got %+v", name, resp.Vouchers)
		}
	}
}

func TestService_ReviewA_P2_11_FreeshipBudgetNotFalselyExhausted(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyer1Ctx := ctxWithRole("buyer-1", "buyer", "")
	buyer2Ctx := ctxWithRole("buyer-2", "buyer", "")

	// 1. FREESHIP PERCENT 100%, max 30k, budget 50k -> use 30k -> remBudget = 20k (> 0, still valid for 15k shipping!)
	fsPct, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "P211_FSPCT", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT, DiscountPercent: 100, MaxDiscountVnd: 30000,
		Quota: 10, PerUserLimit: 5, BudgetVnd: 50000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId: "p211-use-pct", UserId: "buyer-1", FreeshipVoucherId: fsPct.Voucher.Id,
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 30000}},
	}); err != nil {
		t.Fatal(err)
	}

	// 2. FREESHIP FIXED 30k, budget 50k -> use 30k -> remBudget = 20k (> 0, still valid for 15k shipping!)
	fsFixed, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "P211_FSFIX", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 30000,
		Quota: 10, PerUserLimit: 5, BudgetVnd: 50000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId: "p211-use-fix", UserId: "buyer-1", FreeshipVoucherId: fsFixed.Voucher.Id,
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 30000}},
	}); err != nil {
		t.Fatal(err)
	}

	// 3. FREESHIP FIXED 30k, budget 30k -> use 30k -> remBudget = 0 (truly exhausted!)
	fsZero, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "P211_FSZERO", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 30000,
		Quota: 10, PerUserLimit: 5, BudgetVnd: 30000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId: "p211-use-zero", UserId: "buyer-1", FreeshipVoucherId: fsZero.Voucher.Id,
		ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 30000}},
	}); err != nil {
		t.Fatal(err)
	}

	// ListAvailableVouchers with order_amount_vnd = 200k MUST keep both remBudget=20k freeship vouchers and exclude remBudget=0
	listResp, err := svc.ListAvailableVouchers(buyer2Ctx, &promotionv1.ListAvailableVouchersRequest{
		Scope:          promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		OrderAmountVnd: 200000,
	})
	if err != nil {
		t.Fatalf("ListAvailableVouchers(FREESHIP): %v", err)
	}
	got := make(map[string]*promotionv1.Voucher)
	for _, v := range listResp.Vouchers {
		got[v.Code] = v
	}
	if got["P211_FSPCT"] == nil || !got["P211_FSPCT"].Eligible {
		t.Fatalf("expected P211_FSPCT included & eligible (remBudget=20k > 0), got %+v", got["P211_FSPCT"])
	}
	if got["P211_FSFIX"] == nil || !got["P211_FSFIX"].Eligible {
		t.Fatalf("expected P211_FSFIX included & eligible (remBudget=20k > 0), got %+v", got["P211_FSFIX"])
	}
	if got["P211_FSZERO"] != nil {
		t.Fatalf("expected P211_FSZERO (remBudget=0) excluded, got %+v", got["P211_FSZERO"])
	}

	// Verify PreviewDiscounts agrees that P211_FSPCT works for 15k shipping fee
	prev, err := svc.PreviewDiscounts(buyer2Ctx, &promotionv1.PreviewDiscountsRequest{
		FreeshipVoucherId: fsPct.Voucher.Id,
		ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 15000}},
	})
	if err != nil || prev.Breakdown.TotalFreeshipDiscountVnd != 15000 {
		t.Fatalf("expected PreviewDiscounts freeship 15000, got %+v err=%v", prev, err)
	}

	// SaveVoucher MUST allow saving P211_FSFIX (remBudget=20k > 0) and reject P211_FSZERO (remBudget=0)
	if _, err := svc.SaveVoucher(buyer2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: fsFixed.Voucher.Id}); err != nil {
		t.Fatalf("SaveVoucher(P211_FSFIX) should succeed when remBudget=20k > 0, got %v", err)
	}
	if _, err := svc.SaveVoucher(buyer2Ctx, &promotionv1.SaveVoucherRequest{VoucherId: fsZero.Voucher.Id}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("SaveVoucher(P211_FSZERO): want ResourceExhausted, got %v", err)
	}
}
