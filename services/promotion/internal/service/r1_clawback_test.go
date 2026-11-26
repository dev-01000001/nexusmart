package service_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func TestR1Money5_ReleasePromotionShop_SubOrderIDClawbackOnly(t *testing.T) {
	svc, mem, _ := setupService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	svcCtx := ctxWithService("order-service")

	// 1. Create shop voucher (quota=5, per_user_limit=1) and credit buyer 5,000 spent coins.
	shopV, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "R1M5_SHOP1",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		Quota:        5,
		PerUserLimit: 1,
		BudgetVnd:    100000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	if _, _, err := mem.CreditCoins(ctx, "buyer-m5", 5000, "seed", "seed-m5-coins"); err != nil {
		t.Fatalf("CreditCoins: %v", err)
	}

	// 2. Reserve promotion under checkout_group_id = "grp-m5-1" (distinct from sub_order_id = "ord-m5-1").
	resResp, err := svc.ReservePromotion(svcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:        "grp-m5-1",
		UserId:         "buyer-m5",
		ShopVoucherIds: map[string]string{"shop-1": shopV.GetVoucher().GetId()},
		CoinsToUse:     5000,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId:         "shop-1",
				SubtotalVnd:    200000,
				ShippingFeeVnd: 20000,
				Items:          []*promotionv1.DiscountLineItem{{SkuId: "sku-1", Quantity: 1, UnitPriceVnd: 200000}},
			},
		},
	})
	if err != nil || !resResp.GetReserved() {
		t.Fatalf("ReservePromotion: resp=%+v err=%v", resResp, err)
	}
	if mem.AccountBalance("buyer-m5") != 0 {
		t.Fatalf("expected 0 coins after reserving 5000, got %d", mem.AccountBalance("buyer-m5"))
	}

	// 3. Order completes -> order.completed credits +1,500 reward coins under sub_order_id = "ord-m5-1" (!= "grp-m5-1").
	if already, err := mem.ProcessOrderCompletedEvent(ctx, "evt-m5-comp-1", "order.completed", "ord-m5-1", "buyer-m5", 1500); err != nil || already {
		t.Fatalf("ProcessOrderCompletedEvent: already=%v err=%v", already, err)
	}
	if mem.AccountBalance("buyer-m5") != 1500 {
		t.Fatalf("expected 1500 reward coins, got %d", mem.AccountBalance("buyer-m5"))
	}

	// 4. Completed order is refunded post-settlement -> ReleasePromotionShop(ClawbackOnly=true, SubOrderId="ord-m5-1").
	// MUST claw back 1,500 reward coins WITHOUT releasing the 5,000 spent coins or voucher quota / per-user usage!
	rel1, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId:      "grp-m5-1",
		ShopId:       "shop-1",
		SubOrderId:   "ord-m5-1",
		Reason:       "order_completed_refund_clawback",
		ClawbackOnly: true,
	})
	if err != nil {
		t.Fatalf("ReleasePromotionShop(clawback_only): %v", err)
	}
	if rel1.GetAlreadyReleased() || rel1.GetCoinsReturned() != 0 || rel1.GetShopVoucherReleased() || rel1.GetPlatformVoucherReleased() {
		t.Fatalf("clawback_only must not release vouchers/spent coins, got %+v", rel1)
	}
	if mem.AccountBalance("buyer-m5") != 0 || mem.LedgerSum("buyer-m5") != 0 {
		t.Fatalf("expected reward clawed back to 0 (and 5000 spent coins NOT returned), got acct=%d sum=%d",
			mem.AccountBalance("buyer-m5"), mem.LedgerSum("buyer-m5"))
	}

	// Voucher quota and per-user usage must stay consumed (used_count=1, user_usage=1), reservation stays RESERVED.
	vAfter, err := mem.GetVoucherByID(ctx, shopV.GetVoucher().GetId())
	if err != nil || vAfter.UsedCount != 1 {
		t.Fatalf("voucher used_count mutated by clawback_only: v=%+v err=%v", vAfter, err)
	}
	usage, err := mem.GetUserUsageBatch(ctx, "buyer-m5", []store.UserUsageKey{
		{Kind: domain.UsageKindVoucher, TargetID: shopV.GetVoucher().GetId()},
	})
	if err != nil || usage[store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: shopV.GetVoucher().GetId()}] != 1 {
		t.Fatalf("voucher per-user usage mutated by clawback_only: usage=%+v err=%v", usage, err)
	}
	resAfter, err := mem.GetReservation(ctx, "grp-m5-1")
	if err != nil || resAfter.Status != domain.ReservationStatusReserved || len(resAfter.ReleasedShops) != 0 {
		t.Fatalf("reservation mutated by clawback_only: %+v err=%v", resAfter, err)
	}

	// 5. Idempotent replay: calling again after buyer earns new coins MUST NOT deduct a second time!
	if _, _, err := mem.CreditCoins(ctx, "buyer-m5", 3000, "bonus", "bonus-after-m5"); err != nil {
		t.Fatalf("CreditCoins bonus: %v", err)
	}
	rel2, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId:      "grp-m5-1",
		ShopId:       "shop-1",
		SubOrderId:   "ord-m5-1",
		Reason:       "order_completed_refund_clawback",
		ClawbackOnly: true,
	})
	if err != nil || !rel2.GetAlreadyReleased() {
		t.Fatalf("2nd clawback_only must be idempotent no-op: resp=%+v err=%v", rel2, err)
	}
	if mem.AccountBalance("buyer-m5") != 3000 || mem.LedgerSum("buyer-m5") != 3000 {
		t.Fatalf("2nd clawback_only double-deducted coins: acct=%d sum=%d", mem.AccountBalance("buyer-m5"), mem.LedgerSum("buyer-m5"))
	}

	// 6. Fallback compatibility: omitting sub_order_id still claws back by order_id.
	if _, err := mem.ProcessOrderCompletedEvent(ctx, "evt-m5-fallback", "order.completed", "ord-fallback-1", "buyer-fb", 800); err != nil {
		t.Fatalf("ProcessOrderCompletedEvent fallback: %v", err)
	}
	relFB, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId:      "ord-fallback-1",
		ShopId:       "shop-1",
		ClawbackOnly: true,
	})
	if err != nil || relFB.GetAlreadyReleased() || mem.AccountBalance("buyer-fb") != 0 {
		t.Fatalf("fallback clawback by order_id failed: resp=%+v bal=%d err=%v", relFB, mem.AccountBalance("buyer-fb"), err)
	}

	// 7. Out-of-order: refund (clawback_only=true) arrives BEFORE order.completed -> tombstone blocks late credit.
	relPre1, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId:      "grp-pre-ord-1",
		ShopId:       "shop-1",
		SubOrderId:   "sub-pre-ord-1",
		Reason:       "order_completed_refund_clawback",
		ClawbackOnly: true,
	})
	if err != nil || relPre1.GetAlreadyReleased() {
		t.Fatalf("1st pre-order.completed clawback_only: resp=%+v err=%v", relPre1, err)
	}
	relPre2, err := svc.ReleasePromotionShop(svcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId:      "grp-pre-ord-1",
		ShopId:       "shop-1",
		SubOrderId:   "sub-pre-ord-1",
		Reason:       "order_completed_refund_clawback",
		ClawbackOnly: true,
	})
	if err != nil || !relPre2.GetAlreadyReleased() {
		t.Fatalf("2nd pre-order.completed clawback_only replay: resp=%+v err=%v", relPre2, err)
	}
	if already, err := mem.ProcessOrderCompletedEvent(ctx, "evt-late-comp-1", "order.completed", "sub-pre-ord-1", "buyer-late", 1500); err != nil || already {
		t.Fatalf("late ProcessOrderCompletedEvent: already=%v err=%v", already, err)
	}
	if mem.AccountBalance("buyer-late") != 0 || mem.LedgerSum("buyer-late") != 0 {
		t.Fatalf("late order.completed must not credit coins after clawback tombstone: acct=%d sum=%d",
			mem.AccountBalance("buyer-late"), mem.LedgerSum("buyer-late"))
	}
}
