package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func setupService(t *testing.T) (*service.PromotionService, *store.MemoryStore, *lua.Engine) {
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
	log := logger.New("promotion-test", logger.WithWriter(io.Discard))
	svc := service.NewPromotionService(mem, eng, log)
	return svc, mem, eng
}

func ctxWithRole(userID, role, shopID string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:   role,
		ShopID: shopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: userID,
		},
	})
}

func TestService_1000Concurrent_FlashSale_100Slots_AndRelease(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Midnight Mega Flash",
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}

	regResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsResp.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-flash-1",
		FlashPriceVnd: 99000,
		TotalSlots:    100,
		PerUserLimit:  1,
	})
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem: %v", err)
	}
	itemID := regResp.Item.Id

	appResp, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  itemID,
		Approve: true,
	})
	if err != nil {
		t.Fatalf("ApproveFlashSaleItem: %v", err)
	}
	if appResp.Item.RemainingSlots != 100 {
		t.Fatalf("expected 100 remaining slots after approval, got %d", appResp.Item.RemainingSlots)
	}

	const (
		totalSlots  = 100
		concurrency = 1000
	)
	var (
		wg          sync.WaitGroup
		startCh     = make(chan struct{})
		succeeded   atomic.Int64
		exhausted   atomic.Int64
		unexpected  atomic.Int64
		winnersMu   sync.Mutex
		winnerOrder = make([]string, 0, totalSlots)
	)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh

			orderID := fmt.Sprintf("flash-ord-%04d", idx)
			buyerID := fmt.Sprintf("buyer-%04d", idx)
			resp, rErr := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
				OrderId: orderID,
				UserId:  buyerID,
				ShopItems: []*promotionv1.ShopDiscountInput{
					{
						ShopId: "shop-1",
						Items: []*promotionv1.DiscountLineItem{
							{SkuId: "sku-flash-1", Quantity: 1, UnitPriceVnd: 150000, FlashSaleItemId: itemID},
						},
					},
				},
				FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
					{FlashSaleItemId: itemID, SkuId: "sku-flash-1", Quantity: 1},
				},
			})
			if rErr != nil {
				if status.Code(rErr) == codes.ResourceExhausted {
					exhausted.Add(1)
					return
				}
				unexpected.Add(1)
				return
			}
			if resp.Reserved && !resp.AlreadyReserved {
				succeeded.Add(1)
				winnersMu.Lock()
				winnerOrder = append(winnerOrder, orderID)
				winnersMu.Unlock()
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	if unexpected.Load() != 0 {
		t.Fatalf("unexpected non-ResourceExhausted errors: %d", unexpected.Load())
	}
	if succeeded.Load() != totalSlots {
		t.Fatalf("expected EXACTLY %d flash sale reservations to succeed, got %d", totalSlots, succeeded.Load())
	}
	if exhausted.Load() != concurrency-totalSlots {
		t.Fatalf("expected EXACTLY %d ResourceExhausted rejections, got %d", concurrency-totalSlots, exhausted.Load())
	}

	// Verify ListActiveFlashSales reports live remaining_slots == 0 from Redis
	activeResp, err := svc.ListActiveFlashSales(context.Background(), &promotionv1.ListActiveFlashSalesRequest{})
	if err != nil || len(activeResp.FlashSales) != 1 || len(activeResp.FlashSales[0].Items) != 1 {
		t.Fatalf("ListActiveFlashSales unexpected: resp=%+v err=%v", activeResp, err)
	}
	if got := activeResp.FlashSales[0].Items[0].RemainingSlots; got != 0 {
		t.Fatalf("expected live remaining_slots 0, got %d", got)
	}

	// Release 25 winning orders (twice each to verify idempotency) -> remaining_slots must become 25
	svcCtx := ctxWithService("order-service")
	for i := 0; i < 25; i++ {
		rel1, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: winnerOrder[i]})
		if err != nil || !rel1.Released || rel1.AlreadyReleased {
			t.Fatalf("1st ReleasePromotion failed: resp=%+v err=%v", rel1, err)
		}
		rel2, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: winnerOrder[i]})
		if err != nil || !rel2.Released || !rel2.AlreadyReleased {
			t.Fatalf("2nd ReleasePromotion should be idempotent: resp=%+v err=%v", rel2, err)
		}
	}

	activeResp, err = svc.ListActiveFlashSales(context.Background(), &promotionv1.ListActiveFlashSalesRequest{})
	if err != nil {
		t.Fatalf("ListActiveFlashSales after release: %v", err)
	}
	if got := activeResp.FlashSales[0].Items[0].RemainingSlots; got != 25 {
		t.Fatalf("expected live remaining_slots restored to 25, got %d", got)
	}
}

func TestService_RBAC_VouchersAndFlashSales(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Now().UTC()
	seller1Ctx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	adminCtx := ctxWithRole("admin-1", "admin", "")

	// Seller can create SHOP voucher for own shop
	if _, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "SHOP1OK",
		Name:         "Shop 1 20k",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		Quota:        10,
		PerUserLimit: 1,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); err != nil {
		t.Fatalf("seller own shop voucher should succeed: %v", err)
	}

	// Seller CANNOT create SHOP voucher for another shop
	if _, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "SHOP2BAD",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-2",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		Quota:        10,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-shop voucher, got %v", err)
	}

	// Seller CANNOT create PLATFORM voucher
	if _, err := svc.CreateVoucher(seller1Ctx, &promotionv1.CreateVoucherRequest{
		Code:         "PLATBAD",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		Quota:        10,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for seller platform voucher, got %v", err)
	}

	// Buyer CANNOT create voucher or flash sale or credit coins
	if _, err := svc.CreateVoucher(buyerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "BUYERBAD",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CreateVoucher, got %v", err)
	}
	if _, err := svc.CreateFlashSale(buyerCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Bad",
		StartsAt: timestamppb.New(now),
		EndsAt:   timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CreateFlashSale, got %v", err)
	}
	if _, err := svc.CreditCoins(buyerCtx, &promotionv1.CreditCoinsRequest{
		UserId: "buyer-1",
		Amount: 1000,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer CreditCoins, got %v", err)
	}

	// Admin CAN create PLATFORM & FREESHIP, CANNOT create SHOP
	if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "PLATOK",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		Quota:        10,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); err != nil {
		t.Fatalf("admin platform voucher should succeed: %v", err)
	}
	if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "ADMINSHOPBAD",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		Quota:        10,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for admin SHOP voucher, got %v", err)
	}
}

func TestService_OverlappingFlashSaleWindowRejected(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	fs1, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Window 1",
		StartsAt: timestamppb.New(now.Add(1 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(3 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fsOverlap, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Window Overlap",
		StartsAt: timestamppb.New(now.Add(2 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(4 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	fsNonOverlap, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Window Next",
		StartsAt: timestamppb.New(now.Add(3 * time.Hour)),
		EndsAt:   timestamppb.New(now.Add(5 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fs1.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-same",
		FlashPriceVnd: 50000,
		TotalSlots:    10,
	}); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// Overlapping window for same SKU MUST be rejected
	if _, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsOverlap.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-same",
		FlashPriceVnd: 49000,
		TotalSlots:    10,
	}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists for overlapping flash sale window, got %v", err)
	}

	// Non-overlapping window for same SKU succeeds
	if _, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsNonOverlap.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-same",
		FlashPriceVnd: 50000,
		TotalSlots:    10,
	}); err != nil {
		t.Fatalf("non-overlapping registration should succeed: %v", err)
	}
}

func TestService_FullCheckoutSagaReserveRelease_AndCoinLedgerInvariant(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	// 1. Create Shop, Platform, Freeship vouchers + credit coins idempotently
	shopV, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "SHOP20K",
		Name:         "Shop 20k",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     20000,
		MinOrderVnd:  100000,
		Quota:        5,
		PerUserLimit: 1,
		BudgetVnd:    60000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	platV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:            "PLAT10P",
		Name:            "Platform 10%",
		Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType:    promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT,
		DiscountPercent: 10,
		MaxDiscountVnd:  50000,
		MinOrderVnd:     150000,
		Quota:           5,
		PerUserLimit:    1,
		StartsAt:        timestamppb.New(now.Add(-time.Hour)),
		EndsAt:          timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	shipV, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "SHIP15K",
		Name:         "Freeship 15k",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     15000,
		MinOrderVnd:  100000,
		Quota:        5,
		PerUserLimit: 1,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Save voucher twice -> deduped cleanly
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{VoucherId: shopV.Voucher.Id}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveVoucher(buyerCtx, &promotionv1.SaveVoucherRequest{VoucherId: shopV.Voucher.Id}); err != nil {
		t.Fatal(err)
	}

	// ListAvailableVouchers with insufficient order amount shows ineligible reason
	listResp, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{
		ShopId:         "shop-1",
		OrderAmountVnd: 80000,
	})
	if err != nil || len(listResp.Vouchers) != 1 {
		t.Fatalf("ListAvailableVouchers unexpected: %+v err=%v", listResp, err)
	}
	if listResp.Vouchers[0].Eligible || !listResp.Vouchers[0].Saved {
		t.Fatalf("expected saved=true eligible=false for shortfall, got %+v", listResp.Vouchers[0])
	}

	// Credit 12,000 coins with duplicate reference_id -> credited ONCE
	cr1, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-1",
		Amount:      12000,
		Reason:      "welcome_bonus",
		ReferenceId: "bonus-ref-1",
	})
	if err != nil || cr1.Balance != 12000 || cr1.AlreadyCredited {
		t.Fatalf("1st credit unexpected: %+v err=%v", cr1, err)
	}
	cr2, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-1",
		Amount:      12000,
		Reason:      "welcome_bonus",
		ReferenceId: "bonus-ref-1",
	})
	if err != nil || cr2.Balance != 12000 || !cr2.AlreadyCredited {
		t.Fatalf("duplicate credit unexpected: %+v err=%v", cr2, err)
	}

	// 2. ReservePromotion (200k subtotal + 25k shipping)
	// Order: Shop -20k (180k) -> Platform 10% of 180k = -18k (162k) -> Freeship -15k (ship 10k) -> Coins -5k -> Payable 167k
	reserveReq := &promotionv1.ReservePromotionRequest{
		OrderId:           "saga-order-1",
		UserId:            "buyer-1",
		ShopVoucherIds:    map[string]string{"shop-1": shopV.Voucher.Id},
		PlatformVoucherId: platV.Voucher.Id,
		FreeshipVoucherId: shipV.Voucher.Id,
		CoinsToUse:        5000,
		ShopItems: []*promotionv1.ShopDiscountInput{
			{ShopId: "shop-1", SubtotalVnd: 200000, ShippingFeeVnd: 25000},
		},
	}

	svcCtx := ctxWithService("order-service")
	res1, err := svc.ReservePromotion(svcCtx, reserveReq)
	if err != nil {
		t.Fatalf("ReservePromotion 1st: %v", err)
	}
	if !res1.Reserved || res1.AlreadyReserved {
		t.Fatalf("expected reserved=true already=false, got %+v", res1)
	}
	if res1.Breakdown.FinalPayableVnd != 167000 {
		t.Fatalf("expected final payable 167000, got %d", res1.Breakdown.FinalPayableVnd)
	}
	if res1.Breakdown.ShopCostVnd != 20000 || res1.Breakdown.PlatformCostVnd != 38000 {
		t.Fatalf("expected shopCost=20000 platformCost=38000, got shop=%d plat=%d",
			res1.Breakdown.ShopCostVnd, res1.Breakdown.PlatformCostVnd)
	}

	// Idempotent retry of ReservePromotion with same order_id
	res2, err := svc.ReservePromotion(svcCtx, reserveReq)
	if err != nil || !res2.Reserved || !res2.AlreadyReserved {
		t.Fatalf("idempotent ReservePromotion unexpected: %+v err=%v", res2, err)
	}
	if res2.Breakdown.FinalPayableVnd != 167000 {
		t.Fatalf("expected replay breakdown 167000, got %d", res2.Breakdown.FinalPayableVnd)
	}

	// Verify coin balance deducted ONCE and matches SUM(coin_transactions.delta)
	balResp, err := svc.GetCoinBalance(buyerCtx, &promotionv1.GetCoinBalanceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if balResp.Balance != 7000 || mem.AccountBalance("buyer-1") != 7000 || mem.LedgerSum("buyer-1") != 7000 {
		t.Fatalf("coin ledger invariant broken after reserve: resp=%d acct=%d sum=%d",
			balResp.Balance, mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}

	// Verify Redis counters were decremented once
	shopRem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shopV.Voucher.Id))
	shopBudget, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(shopV.Voucher.Id))
	if shopRem != 4 || shopBudget != 40000 {
		t.Fatalf("expected shop voucher rem=4 budget=40000, got rem=%d budget=%d", shopRem, shopBudget)
	}

	// 3. Saga compensation: ReleasePromotion (called twice to prove idempotency!)
	rel1, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: "saga-order-1"})
	if err != nil || !rel1.Released || rel1.AlreadyReleased {
		t.Fatalf("1st ReleasePromotion unexpected: %+v err=%v", rel1, err)
	}
	rel2, err := svc.ReleasePromotion(svcCtx, &promotionv1.ReleasePromotionRequest{OrderId: "saga-order-1"})
	if err != nil || !rel2.Released || !rel2.AlreadyReleased {
		t.Fatalf("2nd ReleasePromotion unexpected: %+v err=%v", rel2, err)
	}

	// Verify 100% restoration of voucher quota, voucher budget, and buyer coin balance
	shopRem, _, _ = eng.GetInt64(context.Background(), lua.VoucherRemainingKey(shopV.Voucher.Id))
	shopBudget, _, _ = eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(shopV.Voucher.Id))
	if shopRem != 5 || shopBudget != 60000 {
		t.Fatalf("expected restored shop voucher rem=5 budget=60000, got rem=%d budget=%d", shopRem, shopBudget)
	}

	balResp, err = svc.GetCoinBalance(buyerCtx, &promotionv1.GetCoinBalanceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if balResp.Balance != 12000 || mem.AccountBalance("buyer-1") != 12000 || mem.LedgerSum("buyer-1") != 12000 {
		t.Fatalf("coin ledger invariant broken after release: resp=%d acct=%d sum=%d",
			balResp.Balance, mem.AccountBalance("buyer-1"), mem.LedgerSum("buyer-1"))
	}
}

func TestService_PostgresCoinFailureCompensatesRedis(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "COMP10K",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        3,
		PerUserLimit: 2,
		BudgetVnd:    30000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Credit only 5,000 coins
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-poor",
		Amount:      5000,
		ReferenceId: "poor-init",
	}); err != nil {
		t.Fatal(err)
	}

	// ReservePromotion rejects insufficient coins (needs 8000 > 5000) and leaves Redis quota/budget intact!
	_, err = svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
		OrderId:        "order-coin-fail",
		UserId:         "buyer-poor",
		ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		CoinsToUse:     8000,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for insufficient coins, got %v", err)
	}

	remQuota, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id))
	remBudget, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vResp.Voucher.Id))
	if remQuota != 3 || remBudget != 30000 {
		t.Fatalf("expected Redis compensation to restore quota=3 budget=30000, got quota=%d budget=%d", remQuota, remBudget)
	}
	if mem.AccountBalance("buyer-poor") != 5000 || mem.LedgerSum("buyer-poor") != 5000 {
		t.Fatalf("expected buyer-poor balance unchanged at 5000, got acct=%d sum=%d",
			mem.AccountBalance("buyer-poor"), mem.LedgerSum("buyer-poor"))
	}
}

func TestService_ConcurrentCoinsRace_CompensatesRedisAndPreservesInvariant(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "RACEVOUCHER",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        50,
		PerUserLimit: 50,
		BudgetVnd:    500000,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// User has 10,000 coins; 20 concurrent orders each try to spend 3,000 coins + 1 voucher.
	// Exactly 3 can succeed (9,000 coins spent, 1,000 remaining); 17 must fail and release Redis voucher quota!
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-race",
		Amount:      10000,
		ReferenceId: "race-init",
	}); err != nil {
		t.Fatal(err)
	}

	var (
		wg        sync.WaitGroup
		startCh   = make(chan struct{})
		succeeded atomic.Int64
		failed    atomic.Int64
	)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			resp, rErr := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
				OrderId:        fmt.Sprintf("coin-race-ord-%02d", idx),
				UserId:         "buyer-race",
				ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
				CoinsToUse:     3000,
				ShopItems: []*promotionv1.ShopDiscountInput{
					{ShopId: "shop-1", SubtotalVnd: 100000, ShippingFeeVnd: 10000},
				},
			})
			if rErr != nil {
				failed.Add(1)
				return
			}
			if resp.Reserved {
				succeeded.Add(1)
			}
		}(i)
	}
	close(startCh)
	wg.Wait()

	if succeeded.Load() != 3 || failed.Load() != 17 {
		t.Fatalf("expected 3 successes and 17 failures, got succ=%d fail=%d", succeeded.Load(), failed.Load())
	}

	remQuota, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id))
	remBudget, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vResp.Voucher.Id))
	userUsed, _, _ := eng.GetInt64(context.Background(), lua.VoucherUserKey(vResp.Voucher.Id, "buyer-race"))
	if remQuota != 47 || remBudget != 470000 || userUsed != 3 {
		t.Fatalf("expected compensated Redis counters quota=47 budget=470000 userUsed=3, got quota=%d budget=%d userUsed=%d",
			remQuota, remBudget, userUsed)
	}
	if mem.AccountBalance("buyer-race") != 1000 || mem.LedgerSum("buyer-race") != 1000 {
		t.Fatalf("coin ledger invariant violated: acct=%d sum=%d",
			mem.AccountBalance("buyer-race"), mem.LedgerSum("buyer-race"))
	}
}

func TestService_ConcurrentDuplicateOrderID_Idempotent(t *testing.T) {
	svc, mem, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "IDEMV",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        10,
		PerUserLimit: 5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-idem",
		Amount:      20000,
		ReferenceId: "idem-init",
	}); err != nil {
		t.Fatal(err)
	}

	var (
		wg      sync.WaitGroup
		startCh = make(chan struct{})
		errs    atomic.Int64
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			resp, rErr := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
				OrderId:        "same-order-id-99",
				UserId:         "buyer-idem",
				ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
				CoinsToUse:     4000,
				ShopItems: []*promotionv1.ShopDiscountInput{
					{ShopId: "shop-1", SubtotalVnd: 100000, ShippingFeeVnd: 10000},
				},
			})
			if rErr != nil || !resp.Reserved {
				errs.Add(1)
			}
		}()
	}
	close(startCh)
	wg.Wait()

	if errs.Load() != 0 {
		t.Fatalf("expected 0 errors on concurrent duplicate order_id, got %d", errs.Load())
	}
	remQuota, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id))
	if remQuota != 9 {
		t.Fatalf("expected voucher quota decremented once (9), got %d", remQuota)
	}
	if mem.AccountBalance("buyer-idem") != 16000 || mem.LedgerSum("buyer-idem") != 16000 {
		t.Fatalf("expected coins deducted once (16000), got acct=%d sum=%d",
			mem.AccountBalance("buyer-idem"), mem.LedgerSum("buyer-idem"))
	}
}

func TestService_ListAvailableVouchers_EligibilityReasons(t *testing.T) {
	svc, _, _ := setupService(t)
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })
	adminCtx := ctxWithRole("admin-1", "admin", "")

	// Expired voucher
	if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "EXPIRED1",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        5,
		StartsAt:     timestamppb.New(now.Add(-2 * time.Hour)),
		EndsAt:       timestamppb.New(now.Add(-1 * time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	// Out of quota voucher (quota=1, reserve it)
	vOut, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "QUOTA1",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        1,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
		OrderId:           "consume-quota-1",
		UserId:            "other-buyer",
		PlatformVoucherId: vOut.Voucher.Id,
		ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}); err != nil {
		t.Fatal(err)
	}

	// Active voucher with min_order_vnd shortfall (stays listed with ineligible_reason)
	if _, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code:         "MINORDER1",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		MinOrderVnd:  200000,
		Quota:        5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	listResp, err := svc.ListAvailableVouchers(ctxWithRole("buyer-x", "buyer", ""), &promotionv1.ListAvailableVouchersRequest{
		UserId:         "buyer-x",
		Scope:          promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		OrderAmountVnd: 100000,
	})
	if err != nil || len(listResp.Vouchers) != 1 {
		t.Fatalf("expected only active MINORDER1 (expired & out-of-quota excluded), got %+v err=%v", listResp, err)
	}
	if listResp.Vouchers[0].Code != "MINORDER1" || listResp.Vouchers[0].Eligible {
		t.Fatalf("expected MINORDER1 ineligible for 100k order, got %+v", listResp.Vouchers[0])
	}
	if listResp.Vouchers[0].IneligibleReason == "" {
		t.Fatalf("expected non-empty ineligible_reason for MINORDER1 shortfall")
	}
}

func TestService_UnauthenticatedMutationsAndCrossUserReserveDenied(t *testing.T) {
	svc, mem, _ := setupService(t)
	now := time.Now().UTC()
	anonCtx := context.Background()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	buyer1Ctx := ctxWithRole("buyer-1", "buyer", "")

	// All 4 admin/seller mutation RPCs MUST reject unauthenticated callers with Unauthenticated.
	if _, err := svc.CreateVoucher(anonCtx, &promotionv1.CreateVoucherRequest{
		Code:         "ANONV",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anon CreateVoucher, got %v", err)
	}

	if _, err := svc.CreateFlashSale(anonCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Anon Sale",
		StartsAt: timestamppb.New(now.Add(-time.Hour)),
		EndsAt:   timestamppb.New(now.Add(time.Hour)),
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anon CreateFlashSale, got %v", err)
	}

	if _, err := svc.RegisterFlashSaleItem(anonCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   "fs-1",
		ShopId:        "shop-1",
		SkuId:         "sku-1",
		FlashPriceVnd: 50000,
		TotalSlots:    10,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anon RegisterFlashSaleItem, got %v", err)
	}

	if _, err := svc.ApproveFlashSaleItem(anonCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  "item-1",
		Approve: true,
	}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated for anon ApproveFlashSaleItem, got %v", err)
	}

	// Authenticated buyer-1 CANNOT call ReservePromotion targeting buyer-2's user_id/coins.
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-2",
		Amount:      10000,
		ReferenceId: "b2-init",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId:    "cross-user-ord-1",
		UserId:     "buyer-2",
		CoinsToUse: 5000,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for cross-user ReservePromotion, got %v", err)
	}
	if mem.AccountBalance("buyer-2") != 10000 {
		t.Fatalf("buyer-2 coins must remain untouched at 10000, got %d", mem.AccountBalance("buyer-2"))
	}

	// Authenticated buyer-1 CANNOT call ReservePromotion even for own user_id (D3: order-service only);
	// order-service calling ReservePromotion for buyer-1 succeeds.
	if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
		UserId:      "buyer-1",
		Amount:      10000,
		ReferenceId: "b1-init",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReservePromotion(buyer1Ctx, &promotionv1.ReservePromotionRequest{
		OrderId:    "own-user-buyer-denied",
		UserId:     "buyer-1",
		CoinsToUse: 3000,
		ShopItems:  []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for buyer own-user ReservePromotion under D3, got %v", err)
	}
	resp, err := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
		OrderId:    "own-user-ord-1",
		UserId:     "buyer-1",
		CoinsToUse: 3000,
		ShopItems:  []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	})
	if err != nil || !resp.Reserved {
		t.Fatalf("expected order-service ReservePromotion to succeed, got resp=%+v err=%v", resp, err)
	}
}

type failingCommitStore struct {
	*store.MemoryStore
	entered chan struct{}
	release chan struct{}
}

func (f *failingCommitStore) CommitReservation(_ context.Context, _ domain.PromotionReservation, _ time.Time) (*domain.PromotionReservation, bool, error) {
	close(f.entered)
	<-f.release
	return nil, false, errors.New("simulated postgres commit failure")
}

func TestService_ConcurrentDuplicateOrderID_CommitFailureNeverReturnsReserved(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	mem := store.NewMemoryStore()
	fStore := &failingCommitStore{
		MemoryStore: mem,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	eng := lua.NewEngine(rdb)
	log := logger.New("promotion-test", logger.WithWriter(io.Discard))
	svc := service.NewPromotionService(fStore, eng, log)

	now := time.Now().UTC()
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")
	vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
		Code:         "FAILCOMMIT",
		Scope:        promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:       "shop-1",
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED,
		ValueVnd:     10000,
		Quota:        5,
		PerUserLimit: 5,
		StartsAt:     timestamppb.New(now.Add(-time.Hour)),
		EndsAt:       timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := &promotionv1.ReservePromotionRequest{
		OrderId:        "dup-fail-ord-1",
		UserId:         "buyer-1",
		ShopVoucherIds: map[string]string{"shop-1": vResp.Voucher.Id},
		ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
	}

	var (
		resA, resB *promotionv1.ReservePromotionResponse
		errA, errB error
		wg         sync.WaitGroup
	)

	// Caller A wins reserve_atomic.lua and pauses inside CommitReservation.
	orderSvcCtx := ctxWithService("order-service")
	wg.Add(1)
	go func() {
		defer wg.Done()
		resA, errA = svc.ReservePromotion(orderSvcCtx, req)
	}()

	<-fStore.entered

	// Caller B hits reserve_atomic.lua while A is in-flight -> gets AlreadyReserved and polls.
	wg.Add(1)
	go func() {
		defer wg.Done()
		resB, errB = svc.ReservePromotion(orderSvcCtx, req)
	}()

	// Let B enter polling, then unblock A so A fails CommitReservation & runs compensateRedisRelease.
	time.Sleep(30 * time.Millisecond)
	close(fStore.release)
	wg.Wait()

	if errA == nil || (resA != nil && resA.Reserved) {
		t.Fatalf("caller A must fail on DB commit failure, got res=%+v err=%v", resA, errA)
	}
	if errB == nil || (resB != nil && resB.Reserved) {
		t.Fatalf("caller B must NOT return Reserved=true when A failed commit, got res=%+v err=%v", resB, errB)
	}
	if status.Code(errB) != codes.Aborted && status.Code(errB) != codes.FailedPrecondition {
		t.Fatalf("expected Aborted or FailedPrecondition for caller B, got %v", errB)
	}

	// Verify Redis voucher quota was restored back to 5.
	remQuota, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vResp.Voucher.Id))
	if remQuota != 5 {
		t.Fatalf("expected compensated voucher quota=5, got %d", remQuota)
	}
}

func TestService_FlashSaleReapprovalPreservesRemainingSlots(t *testing.T) {
	svc, _, eng := setupService(t)
	now := time.Now().UTC()
	adminCtx := ctxWithRole("admin-1", "admin", "")
	sellerCtx := ctxWithRole("seller-1", "seller", "shop-1")

	fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
		Name:     "Reapproval Test Sale",
		StartsAt: timestamppb.New(now.Add(-5 * time.Minute)),
		EndsAt:   timestamppb.New(now.Add(2 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	regResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   fsResp.FlashSale.Id,
		ShopId:        "shop-1",
		SkuId:         "sku-reapprove",
		FlashPriceVnd: 80000,
		TotalSlots:    10,
		PerUserLimit:  5,
	})
	if err != nil {
		t.Fatal(err)
	}
	itemID := regResp.Item.Id

	// Initial approval -> 10 slots
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  itemID,
		Approve: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Reserve 3 slots -> 7 remaining in both Redis and Postgres
	if _, err := svc.ReservePromotion(ctxWithService("order-service"), &promotionv1.ReservePromotionRequest{
		OrderId: "reapprove-ord-1",
		UserId:  "buyer-1",
		ShopItems: []*promotionv1.ShopDiscountInput{
			{
				ShopId: "shop-1",
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: "sku-reapprove", Quantity: 3, UnitPriceVnd: 120000, FlashSaleItemId: itemID},
				},
			},
		},
		FlashSaleItems: []*promotionv1.FlashSaleReservationItem{
			{FlashSaleItemId: itemID, SkuId: "sku-reapprove", Quantity: 3},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Strict state machine: APPROVED -> REJECTED is forbidden (FailedPrecondition) and MUST keep Redis slots intact!
	if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  itemID,
		Approve: false,
		Reason:  "cannot revoke approved flash",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected APPROVED->REJECTED FailedPrecondition, got %v", err)
	}

	// Idempotent re-approve on APPROVED item MUST preserve 7 remaining slots, NOT reset to 10!
	reApp, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{
		ItemId:  itemID,
		Approve: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reApp.Item.RemainingSlots != 7 {
		t.Fatalf("expected idempotently re-approved item RemainingSlots=7, got %d", reApp.Item.RemainingSlots)
	}
	liveRem, exists, err := eng.GetInt64(context.Background(), lua.FlashRemainingKey(itemID))
	if err != nil || !exists || liveRem != 7 {
		t.Fatalf("expected Redis remaining slots=7 after idempotent re-approval, got val=%d exists=%v err=%v", liveRem, exists, err)
	}
}
