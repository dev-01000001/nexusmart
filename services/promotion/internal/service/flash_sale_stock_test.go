package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/services/promotion/internal/lua"
)

func TestApproveFlashSaleItem_ProductStockCheckAndStrictStateMachine(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	pc := newFakeProductClient()
	pc.setSKU(&productv1.SKU{
		Id:              "sku-tight",
		ShopId:          "shop-1",
		CategoryId:      "cat-1",
		PriceVnd:        100000,
		MinPrice_30DVnd: 100000,
		AvailableStock:  10,
		IsActive:        true,
	})
	svc.SetProductClient(pc)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	orderSvcCtx := ctxWithService("order-service")

	fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Tight Stock Sale",
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}

	// Registering more slots than current SKU available_stock is rejected immediately.
	if _, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fs.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-tight",
		FlashPriceVnd: 50000,
		TotalSlots:    15, // > 10 available
		PerUserLimit:  1,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("RegisterFlashSaleItem over stock: want FailedPrecondition, got %v", err)
	}

	// Registering 10 slots succeeds while stock=10.
	reg, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fs.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-tight",
		FlashPriceVnd: 50000,
		TotalSlots:    10,
		PerUserLimit:  5,
	})
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem(10): %v", err)
	}

	// If SKU available stock drops below total_slots before approval, ApproveFlashSaleItem rejects it.
	pc.setSKU(&productv1.SKU{
		Id:              "sku-tight",
		ShopId:          "shop-1",
		CategoryId:      "cat-1",
		PriceVnd:        100000,
		MinPrice_30DVnd: 100000,
		AvailableStock:  6,
		IsActive:        true,
	})
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: true,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ApproveFlashSaleItem after stock drop: want FailedPrecondition, got %v", err)
	}

	got, err := mem.GetFlashSaleItem(context.Background(), reg.Item.Id)
	if err != nil || got.Status != "PENDING" {
		t.Fatalf("expected item to stay PENDING after failed stock check, got %+v err=%v", got, err)
	}
	if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(reg.Item.Id)); exists {
		t.Fatalf("Redis slot key must NOT be initialized when SKU stock check fails")
	}

	// Restore stock to 20 -> Approve succeeds and initializes 10 Redis slots.
	pc.setSKU(&productv1.SKU{
		Id:              "sku-tight",
		ShopId:          "shop-1",
		CategoryId:      "cat-1",
		PriceVnd:        100000,
		MinPrice_30DVnd: 100000,
		AvailableStock:  20,
		IsActive:        true,
	})
	app1, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: true,
	})
	if err != nil || app1.Item.RemainingSlots != 10 {
		t.Fatalf("ApproveFlashSaleItem 1st: resp=%+v err=%v", app1, err)
	}

	// Consume 3 slots via ReservePromotion.
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "ord-consume-3",
		UserId:  "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-tight", Quantity: 3, UnitPriceVnd: 100000, FlashSaleItemId: reg.Item.Id},
				},
			},
		},
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: reg.Item.Id, SkuId: "sku-tight", Quantity: 3},
		},
	}); err != nil {
		t.Fatalf("ReservePromotion(3): %v", err)
	}

	// Idempotent re-approve preserves live Redis remaining slots (7) and skips stock re-check.
	app2, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: true,
	})
	if err != nil || app2.Item.RemainingSlots != 7 {
		t.Fatalf("idempotent re-approve mutated state: resp=%+v err=%v", app2, err)
	}

	// Strict state machine forbids APPROVED -> REJECTED and leaves Redis slots intact.
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  reg.Item.Id,
		Approve: false,
		Reason:  "cannot revoke approved item",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("APPROVED -> REJECTED: want FailedPrecondition, got %v", err)
	}
	if live, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(reg.Item.Id)); !exists || live != 7 {
		t.Fatalf("Redis slots must stay 7 after rejected revoke, got live=%d exists=%v", live, exists)
	}
}

func TestEndFlashSaleWindow_ActiveAndUpcoming_EnforcedEverywhereAndIdempotent(t *testing.T) {
	svc, _, eng := setupService(t)
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	pc := newFakeProductClient()
	for _, id := range []string{"sku-a", "sku-b", "sku-c", "sku-u"} {
		pc.setSKU(&productv1.SKU{
			Id:              id,
			ShopId:          "shop-1",
			CategoryId:      "cat-1",
			PriceVnd:        100000,
			MinPrice_30DVnd: 100000,
			AvailableStock:  30,
			IsActive:        true,
		})
	}
	svc.SetProductClient(pc)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	orderSvcCtx := ctxWithService("order-service")

	// Active window with 2 approved items + 1 pending item.
	fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Active Window",
		StartsAt: timestamppb.New(now.Add(-30 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale active: %v", err)
	}
	regA, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-a", FlashPriceVnd: 70000, TotalSlots: 10,
	})
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	regB, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-b", FlashPriceVnd: 80000, TotalSlots: 5,
	})
	if err != nil {
		t.Fatalf("register B: %v", err)
	}
	regPending, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-c", FlashPriceVnd: 85000, TotalSlots: 7,
	})
	if err != nil {
		t.Fatalf("register C: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regA.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve A: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regB.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve B: %v", err)
	}

	// End active window -> clears Redis slots for 2 approved items, marks window ENDED.
	end1, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fs.FlashSale.Id})
	if err != nil {
		t.Fatalf("EndFlashSaleWindow 1st: %v", err)
	}
	if end1.GetReleasedItems() != 2 || end1.GetWindow().GetStatus() != "ENDED" {
		t.Fatalf("unexpected EndFlashSaleWindow 1st resp: %+v", end1)
	}
	for _, id := range []string{regA.Item.Id, regB.Item.Id} {
		if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(id)); exists {
			t.Fatalf("expected Redis slot key deleted for item %s", id)
		}
	}

	// Idempotent second EndFlashSaleWindow returns released_items=0.
	end2, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fs.FlashSale.Id})
	if err != nil || end2.GetReleasedItems() != 0 || end2.GetWindow().GetStatus() != "ENDED" {
		t.Fatalf("unexpected EndFlashSaleWindow 2nd resp: %+v err=%v", end2, err)
	}

	// ListActiveFlashSales excludes ended window.
	active, err := svc.ListActiveFlashSales(context.Background(), &promotionv1.ListActiveFlashSalesRequest{IncludeUpcoming: true})
	if err != nil || len(active.GetFlashSales()) != 0 {
		t.Fatalf("ended window must not appear in ListActiveFlashSales: resp=%+v err=%v", active, err)
	}

	// ListAllFlashSales respects status=ENDED across ACTIVE, UPCOMING, and ENDED filters.
	allActive, err := svc.ListAllFlashSales(adminCtx, &promotionv1.ListAllFlashSalesRequest{Status: "ACTIVE"})
	if err != nil || len(allActive.GetFlashSales()) != 0 {
		t.Fatalf("ListAllFlashSales(ACTIVE) must exclude ENDED window: resp=%+v err=%v", allActive, err)
	}
	allUpcoming, err := svc.ListAllFlashSales(adminCtx, &promotionv1.ListAllFlashSalesRequest{Status: "UPCOMING"})
	if err != nil || len(allUpcoming.GetFlashSales()) != 0 {
		t.Fatalf("ListAllFlashSales(UPCOMING) must exclude ENDED window: resp=%+v err=%v", allUpcoming, err)
	}
	allEnded, err := svc.ListAllFlashSales(adminCtx, &promotionv1.ListAllFlashSalesRequest{Status: "ENDED"})
	if err != nil || len(allEnded.GetFlashSales()) != 1 || allEnded.GetFlashSales()[0].GetStatus() != "ENDED" {
		t.Fatalf("ListAllFlashSales(ENDED) must include ENDED window: resp=%+v err=%v", allEnded, err)
	}

	// PreviewDiscounts must NOT apply flash discount for an ENDED window.
	prevResp, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 100000, FlashSaleItemId: regA.Item.Id},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("PreviewDiscounts on ended window: %v", err)
	}
	if prevResp.GetBreakdown().GetTotalFlashSaleDiscountVnd() != 0 || len(prevResp.GetBreakdown().GetWarnings()) == 0 {
		t.Fatalf("PreviewDiscounts must reject ended flash sale item with warning: %+v", prevResp.GetBreakdown())
	}

	// ReservePromotion rejects ended flash sale item.
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId: "ord-after-end",
		UserId:  "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-a", Quantity: 1, UnitPriceVnd: 100000, FlashSaleItemId: regA.Item.Id},
				},
			},
		},
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: regA.Item.Id, SkuId: "sku-a", Quantity: 1},
		},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("ReservePromotion on ended window: want FailedPrecondition, got %v", err)
	}

	// ApproveFlashSaleItem rejects BOTH pending item AND stale idempotent re-approve on ended window,
	// without recreating Redis slot keys.
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  regPending.Item.Id,
		Approve: true,
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "FLASH_SALE_ENDED") {
		t.Fatalf("ApproveFlashSaleItem pending on ended window: want FailedPrecondition FLASH_SALE_ENDED, got %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  regA.Item.Id,
		Approve: true,
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "FLASH_SALE_ENDED") {
		t.Fatalf("ApproveFlashSaleItem stale retry on ended window: want FailedPrecondition FLASH_SALE_ENDED, got %v", err)
	}
	if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(regA.Item.Id)); exists {
		t.Fatalf("stale ApproveFlashSaleItem retry must NOT recreate Redis key for ended window")
	}

	// RegisterFlashSaleItem on ended window is rejected, while registering sku-a in a NEW overlapping window succeeds!
	if _, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-u", FlashPriceVnd: 80000, TotalSlots: 5,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("RegisterFlashSaleItem on ended window: want FailedPrecondition, got %v", err)
	}
	fsReplacement, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Replacement Window",
		StartsAt: timestamppb.New(now.Add(-10 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(1 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale replacement: %v", err)
	}
	if _, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsReplacement.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-a", FlashPriceVnd: 75000, TotalSlots: 5,
	}); err != nil {
		t.Fatalf("RegisterFlashSaleItem in replacement window after previous ended should succeed: %v", err)
	}

	// Upcoming window (starts_at in the future) can also be canceled cleanly without violating ends_at > starts_at.
	fsUp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Upcoming Window",
		StartsAt: timestamppb.New(now.Add(2 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(4 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale upcoming: %v", err)
	}
	regU, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fsUp.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-u", FlashPriceVnd: 75000, TotalSlots: 8,
	})
	if err != nil {
		t.Fatalf("register upcoming item: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: regU.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve upcoming item: %v", err)
	}
	endUp, err := svc.EndFlashSaleWindow(adminCtx, &promotionv1.EndFlashSaleWindowRequest{WindowId: fsUp.FlashSale.Id})
	if err != nil || endUp.GetReleasedItems() != 1 || endUp.GetWindow().GetStatus() != "ENDED" {
		t.Fatalf("EndFlashSaleWindow upcoming unexpected: resp=%+v err=%v", endUp, err)
	}
	if !endUp.GetWindow().GetEndsAt().AsTime().After(endUp.GetWindow().GetStartsAt().AsTime()) {
		t.Fatalf("upcoming canceled window violated ends_at > starts_at invariant")
	}
	if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(regU.Item.Id)); exists {
		t.Fatalf("expected upcoming Redis slot key deleted after EndFlashSaleWindow")
	}
}

func TestSweepExpiredFlashSales_ClearsSlotsOnce(t *testing.T) {
	svc, _, eng := setupService(t)
	curTime := time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return curTime })

	pc := newFakeProductClient()
	pc.setSKU(&productv1.SKU{Id: "sku-exp-1", ShopId: "shop-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true})
	pc.setSKU(&productv1.SKU{Id: "sku-exp-2", ShopId: "shop-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true})
	svc.SetProductClient(pc)

	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Expiring Window",
		StartsAt: timestamppb.New(curTime.Add(-10 * time.Minute)),
		EndsAt:   timestamppb.New(curTime.Add(10 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}
	r1, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-exp-1", FlashPriceVnd: 70000, TotalSlots: 6,
	})
	if err != nil {
		t.Fatalf("register r1: %v", err)
	}
	r2, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-exp-2", FlashPriceVnd: 70000, TotalSlots: 4,
	})
	if err != nil {
		t.Fatalf("register r2: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: r1.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve r1: %v", err)
	}
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: r2.Item.Id, Approve: true}); err != nil {
		t.Fatalf("approve r2: %v", err)
	}

	// Before expiry: sweep does nothing.
	n, err := svc.SweepExpiredFlashSales(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("sweep before expiry: want n=0 err=nil, got n=%d err=%v", n, err)
	}
	for _, id := range []string{r1.Item.Id, r2.Item.Id} {
		if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(id)); !exists {
			t.Fatalf("expected Redis slot key %s still present before expiry", id)
		}
	}

	// Advance past expiry -> sweep deletes Redis keys and marks items released.
	curTime = curTime.Add(15 * time.Minute)
	n, err = svc.SweepExpiredFlashSales(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("sweep after expiry: want n=2 err=nil, got n=%d err=%v", n, err)
	}
	for _, id := range []string{r1.Item.Id, r2.Item.Id} {
		if _, exists, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(id)); exists {
			t.Fatalf("expected Redis slot key %s deleted by sweeper", id)
		}
	}

	// Second sweep is a no-op (idempotent).
	n, err = svc.SweepExpiredFlashSales(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second sweep: want n=0 err=nil, got n=%d err=%v", n, err)
	}
}

func TestReleasePromotionShop_OrderRewardCoinClawback(t *testing.T) {
	ctx := context.Background()
	svc, mem, _ := setupService(t)
	adminCtx := ctxWithRole("admin-1", "admin", "")
	orderSvcCtx := ctxWithService("order-service")

	// Case 1: Full clawback alongside reserved coin restoration + idempotent replay.
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-cb-1",
		Amount:      10000,
		Reason:      "seed",
		ReferenceId: "cb1-seed",
	}); err != nil {
		t.Fatalf("seed buyer-cb-1 coins: %v", err)
	}
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:    "ord-cb-1",
		UserId:     "buyer-cb-1",
		CoinsToUse: 4000,
		ShopItems:  []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 300000}},
	}); err != nil {
		t.Fatalf("ReservePromotion ord-cb-1: %v", err)
	}
	// Order completes -> 1% of 300,000 = 3,000 reward coins credited (balance: 6,000 -> 9,000).
	if _, err := mem.ProcessOrderCompletedEvent(ctx, "evt-comp-cb-1", "order.completed", "ord-cb-1", "buyer-cb-1", 3000); err != nil {
		t.Fatalf("ProcessOrderCompletedEvent ord-cb-1: %v", err)
	}
	if got := mem.AccountBalance("buyer-cb-1"); got != 9000 {
		t.Fatalf("expected balance 9000 before ReleasePromotionShop, got %d", got)
	}

	// ReleasePromotionShop restores 4,000 reserved coins and claws back 3,000 reward coins -> 10,000.
	rel1, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: "ord-cb-1",
		ShopId:  "shop-1",
	})
	if err != nil || !rel1.GetReleased() || rel1.GetAlreadyReleased() || rel1.GetCoinsReturned() != 4000 {
		t.Fatalf("ReleasePromotionShop 1st unexpected: resp=%+v err=%v", rel1, err)
	}
	if mem.AccountBalance("buyer-cb-1") != 10000 || mem.LedgerSum("buyer-cb-1") != 10000 {
		t.Fatalf("coin ledger mismatch after full clawback: acct=%d sum=%d",
			mem.AccountBalance("buyer-cb-1"), mem.LedgerSum("buyer-cb-1"))
	}

	// Replay is strictly idempotent: no double refund and no double clawback.
	rel2, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: "ord-cb-1",
		ShopId:  "shop-1",
	})
	if err != nil || !rel2.GetReleased() || !rel2.GetAlreadyReleased() {
		t.Fatalf("ReleasePromotionShop 2nd unexpected: resp=%+v err=%v", rel2, err)
	}
	if mem.AccountBalance("buyer-cb-1") != 10000 || mem.LedgerSum("buyer-cb-1") != 10000 {
		t.Fatalf("coin ledger mutated on replay: acct=%d sum=%d",
			mem.AccountBalance("buyer-cb-1"), mem.LedgerSum("buyer-cb-1"))
	}

	// Case 2: No promotion reservation row + buyer already spent part of reward -> clawback capped at balance, never negative.
	if _, err := mem.ProcessOrderCompletedEvent(ctx, "evt-comp-cb-2", "order.completed", "ord-cb-2", "buyer-cb-2", 5000); err != nil {
		t.Fatalf("ProcessOrderCompletedEvent ord-cb-2: %v", err)
	}
	if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
		OrderId:    "ord-spend-cb-2",
		UserId:     "buyer-cb-2",
		CoinsToUse: 3500, // balance 5,000 -> 1,500
		ShopItems:  []*promotionv1.ShopDiscountInput{{ShopId: "shop-2", SubtotalVnd: 100000}},
	}); err != nil {
		t.Fatalf("ReservePromotion ord-spend-cb-2: %v", err)
	}
	if _, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: "ord-cb-2",
		ShopId:  "shop-1",
	}); err != nil {
		t.Fatalf("ReleasePromotionShop capped clawback: %v", err)
	}
	if mem.AccountBalance("buyer-cb-2") != 0 || mem.LedgerSum("buyer-cb-2") != 0 {
		t.Fatalf("expected capped clawback to floor at 0, got acct=%d sum=%d",
			mem.AccountBalance("buyer-cb-2"), mem.LedgerSum("buyer-cb-2"))
	}
	// Repeat call stays at 0 (idempotent).
	if _, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{
		OrderId: "ord-cb-2",
		ShopId:  "shop-1",
	}); err != nil {
		t.Fatalf("ReleasePromotionShop capped clawback replay: %v", err)
	}
	if mem.AccountBalance("buyer-cb-2") != 0 || mem.LedgerSum("buyer-cb-2") != 0 {
		t.Fatalf("expected balance 0 after replay, got acct=%d sum=%d",
			mem.AccountBalance("buyer-cb-2"), mem.LedgerSum("buyer-cb-2"))
	}
}
