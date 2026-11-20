package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/product/internal/store"
)

func ctxWithService(serviceName string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Role:    auth.ServiceRole,
		Service: serviceName,
	})
}

func TestFlashSaleStockReservation_LifecycleIsolationAndGuards(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-1", "admin", "")
	promoSvcCtx := ctxWithService("promotion-service")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")
	sellerCtx := ctxWithRole("seller-shop-1", "seller", "shop-1")

	_, prod := setupApprovedProduct(t, srv, "shop-1", []*productv1.SKUInput{
		{SkuCode: "FS-SKU-1", Name: "Default", PriceVnd: 500000, Stock: 20},
		{SkuCode: "FS-SKU-2", Name: "Other", PriceVnd: 600000, Stock: 15},
	})
	skuID := prod.Skus[0].Id
	otherSKUID := prod.Skus[1].Id
	endsAt := now.Add(2 * time.Hour)

	// 1. Insufficient stock (> 20) rejected cleanly without mutating SKU.
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-too-big",
		SkuId:           skuID,
		Quantity:        21,
		EndsAt:          timestamppb.New(endsAt),
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_STOCK") {
		t.Fatalf("expected FailedPrecondition INSUFFICIENT_STOCK, got %v", err)
	}

	// 2. Past ends_at rejected.
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-expired",
		SkuId:           skuID,
		Quantity:        5,
		EndsAt:          timestamppb.New(now.Add(-time.Minute)),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for past ends_at, got %v", err)
	}

	// 3. Reserve 12 units for fs-item-1 -> available drops from 20 to 8, flash_sale_reserved becomes 12.
	res1, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-1",
		SkuId:           skuID,
		Quantity:        12,
		EndsAt:          timestamppb.New(endsAt),
	})
	if err != nil {
		t.Fatalf("ReserveFlashSaleStock 1st: %v", err)
	}
	if res1.GetReservedQty() != 12 || res1.GetAvailableStock() != 8 || res1.GetAlreadyReserved() {
		t.Fatalf("unexpected 1st ReserveFlashSaleStock response: %+v", res1)
	}

	// 4. Idempotent retry with same params succeeds without double-reserving.
	res2, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-1",
		SkuId:           skuID,
		Quantity:        12,
		EndsAt:          timestamppb.New(endsAt),
	})
	if err != nil {
		t.Fatalf("ReserveFlashSaleStock idempotent retry: %v", err)
	}
	if res2.GetReservedQty() != 12 || res2.GetAvailableStock() != 8 || !res2.GetAlreadyReserved() {
		t.Fatalf("unexpected idempotent ReserveFlashSaleStock response: %+v", res2)
	}

	// 5. Conflicting quantity for same flash_sale_item_id rejected.
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-1",
		SkuId:           skuID,
		Quantity:        10,
		EndsAt:          timestamppb.New(endsAt),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition on conflicting reservation qty, got %v", err)
	}

	// 6. GetSKUsBatch reflects flash_sale_reserved_stock=12 and available_stock=8.
	checkSKU := func(wantStock, wantReserved, wantFlash, wantAvail int32) *productv1.SKU {
		t.Helper()
		b, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
		if err != nil || len(b.GetSkus()) != 1 {
			t.Fatalf("GetSKUsBatch failed: resp=%+v err=%v", b, err)
		}
		sk := b.GetSkus()[0]
		if sk.GetStock() != wantStock ||
			sk.GetReservedStock() != wantReserved ||
			sk.GetFlashSaleReservedStock() != wantFlash ||
			sk.GetAvailableStock() != wantAvail {
			t.Fatalf("SKU mismatch: got stock=%d reserved=%d flash=%d avail=%d; want stock=%d reserved=%d flash=%d avail=%d",
				sk.GetStock(), sk.GetReservedStock(), sk.GetFlashSaleReservedStock(), sk.GetAvailableStock(),
				wantStock, wantReserved, wantFlash, wantAvail)
		}
		return sk
	}
	sk := checkSKU(20, 0, 12, 8)

	// 7. Seller UpdateStock cannot lower stock below reserved + flash_sale_reserved (12).
	if _, err := srv.UpdateStock(sellerCtx, &productv1.UpdateStockRequest{
		SkuId:           skuID,
		ShopId:          "shop-1",
		NewStock:        11,
		ExpectedVersion: sk.GetVersion(),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("UpdateStock below flash reserve: want FailedPrecondition, got %v", err)
	}

	// 8. Regular order can consume all 8 available units, then the next regular order is BLOCKED.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "reg-order-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 8}},
	}); err != nil {
		t.Fatalf("regular ReserveStock(8): %v", err)
	}
	checkSKU(20, 8, 12, 0)

	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "reg-order-2",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1}},
	}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_STOCK") {
		t.Fatalf("regular order should NOT consume flash reserve: want FailedPrecondition INSUFFICIENT_STOCK, got %v", err)
	}

	// 9. Flash sale order consumes from flash_sale_reserved_stock -> reserved_stock even when available_stock == 0!
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "flash-order-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 5, FlashSaleItemId: "fs-item-1"}},
	}); err != nil {
		t.Fatalf("flash ReserveStock(5): %v", err)
	}
	checkSKU(20, 13, 7, 0)

	// Exceeding remaining flash reserve (7) is rejected.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "flash-order-over",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 8, FlashSaleItemId: "fs-item-1"}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("flash order exceeding remaining reserve: want FailedPrecondition, got %v", err)
	}

	// Wrong SKU for flash_sale_item_id rejected.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "flash-order-wrong-sku",
		Items:   []*productv1.StockReservationItem{{SkuId: otherSKUID, Quantity: 1, FlashSaleItemId: "fs-item-1"}},
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("flash order with mismatched SKU: want InvalidArgument, got %v", err)
	}

	// 10. ConfirmStock on flash-order-1 deducts stock and reserved_stock seamlessly.
	if _, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{OrderId: "flash-order-1"}); err != nil {
		t.Fatalf("ConfirmStock(flash-order-1): %v", err)
	}
	checkSKU(15, 8, 7, 0)

	// 11. Flash order canceled/released WHILE window is active returns stock to flash_sale_reserved_stock!
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "flash-order-cancel-active",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 3, FlashSaleItemId: "fs-item-1"}},
	}); err != nil {
		t.Fatalf("flash ReserveStock(3): %v", err)
	}
	checkSKU(15, 11, 4, 0)

	if _, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "flash-order-cancel-active"}); err != nil {
		t.Fatalf("ReleaseStock(flash-order-cancel-active): %v", err)
	}
	checkSKU(15, 8, 7, 0)

	// Verify another flash order can reuse all 7 remaining units (3 + 2 + 2 left unconsumed).
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "flash-order-2",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 3, FlashSaleItemId: "fs-item-1"}},
	}); err != nil {
		t.Fatalf("flash ReserveStock(3) reuse: %v", err)
	}
	if _, err := srv.ConfirmStock(adminCtx, &productv1.ConfirmStockRequest{OrderId: "flash-order-2"}); err != nil {
		t.Fatalf("ConfirmStock(flash-order-2): %v", err)
	}
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "flash-order-pending-release",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 2, FlashSaleItemId: "fs-item-1"}},
	}); err != nil {
		t.Fatalf("flash ReserveStock(2): %v", err)
	}
	// Now: stock=12, reserved=10 (8 reg + 2 pending flash), flash_reserved=2 (unconsumed), available=0.
	checkSKU(12, 10, 2, 0)

	// 12. ReleaseFlashSaleStock releases ONLY the 2 unconsumed units back to available_stock.
	rel1, err := srv.ReleaseFlashSaleStock(promoSvcCtx, &productv1.ReleaseFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-1",
	})
	if err != nil {
		t.Fatalf("ReleaseFlashSaleStock 1st: %v", err)
	}
	if rel1.GetReleasedQty() != 2 || rel1.GetAvailableStock() != 2 || rel1.GetAlreadyReleased() {
		t.Fatalf("unexpected 1st ReleaseFlashSaleStock response: %+v", rel1)
	}
	checkSKU(12, 10, 0, 2)

	// Idempotent second ReleaseFlashSaleStock returns released_qty=0, already_released=true.
	rel2, err := srv.ReleaseFlashSaleStock(promoSvcCtx, &productv1.ReleaseFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-1",
	})
	if err != nil {
		t.Fatalf("ReleaseFlashSaleStock 2nd: %v", err)
	}
	if rel2.GetReleasedQty() != 0 || rel2.GetAvailableStock() != 2 || !rel2.GetAlreadyReleased() {
		t.Fatalf("unexpected idempotent ReleaseFlashSaleStock response: %+v", rel2)
	}

	// ReleaseFlashSaleStock on unknown ID is idempotent no-op.
	relUnknown, err := srv.ReleaseFlashSaleStock(promoSvcCtx, &productv1.ReleaseFlashSaleStockRequest{
		FlashSaleItemId: "non-existent-fs-item",
	})
	if err != nil || relUnknown.GetReleasedQty() != 0 || !relUnknown.GetAlreadyReleased() {
		t.Fatalf("unknown ReleaseFlashSaleStock: resp=%+v err=%v", relUnknown, err)
	}

	// 13. Releasing a flash order AFTER the window was already released returns units to NORMAL available_stock!
	if _, err := srv.ReleaseStock(adminCtx, &productv1.ReleaseStockRequest{OrderId: "flash-order-pending-release"}); err != nil {
		t.Fatalf("ReleaseStock(flash-order-pending-release): %v", err)
	}
	checkSKU(12, 8, 0, 4)

	// 14. New flash order after window release fails cleanly; re-reserving released item_id fails cleanly.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "flash-order-after-end",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1, FlashSaleItemId: "fs-item-1"}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("flash order after release: want FailedPrecondition, got %v", err)
	}
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-item-1",
		SkuId:           skuID,
		Quantity:        2,
		EndsAt:          timestamppb.New(endsAt),
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("re-reserving released flash item: want FailedPrecondition, got %v", err)
	}
}

func TestFlashSaleStockReservation_ExpirySweeper(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	curTime := time.Date(2026, 11, 20, 14, 0, 0, 0, time.UTC)
	srv.SetClock(func() time.Time { return curTime })

	promoSvcCtx := ctxWithService("promotion-service")
	buyerCtx := ctxWithRole("buyer-1", "buyer", "")

	_, prod := setupApprovedProduct(t, srv, "shop-sweep", []*productv1.SKUInput{
		{SkuCode: "SWEEP-1", Name: "Default", PriceVnd: 100000, Stock: 30},
	})
	skuID := prod.Skus[0].Id

	// Reserve two flash items: short ends in 10m (10 units), long ends in 2h (8 units).
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-short",
		SkuId:           skuID,
		Quantity:        10,
		EndsAt:          timestamppb.New(curTime.Add(10 * time.Minute)),
	}); err != nil {
		t.Fatalf("reserve fs-short: %v", err)
	}
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-long",
		SkuId:           skuID,
		Quantity:        8,
		EndsAt:          timestamppb.New(curTime.Add(2 * time.Hour)),
	}); err != nil {
		t.Fatalf("reserve fs-long: %v", err)
	}

	// Consume 3 units from fs-short.
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "sweep-flash-ord-1",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 3, FlashSaleItemId: "fs-short"}},
	}); err != nil {
		t.Fatalf("consume fs-short: %v", err)
	}

	// Before expiry: sweep releases 0.
	curTime = curTime.Add(5 * time.Minute)
	n, err := srv.SweepExpiredFlashSaleReservations(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("sweep before expiry: want n=0 err=nil, got n=%d err=%v", n, err)
	}

	// Advance past fs-short expiry (11m total): flash order rejected immediately even before sweeper runs!
	curTime = curTime.Add(6 * time.Minute)
	if _, err := srv.ReserveStock(buyerCtx, &productv1.ReserveStockRequest{
		OrderId: "sweep-flash-ord-late",
		Items:   []*productv1.StockReservationItem{{SkuId: skuID, Quantity: 1, FlashSaleItemId: "fs-short"}},
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expired flash order should be rejected: want FailedPrecondition, got %v", err)
	}

	// Sweep releases fs-short (7 unconsumed units returned to available), leaves fs-long (8 units) intact.
	n, err = srv.SweepExpiredFlashSaleReservations(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("sweep at expiry: want n=1 err=nil, got n=%d err=%v", n, err)
	}
	b, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{skuID}})
	if err != nil || len(b.GetSkus()) != 1 {
		t.Fatalf("GetSKUsBatch: %v", err)
	}
	sk := b.GetSkus()[0]
	// stock=30, reserved=3, flash_reserved=8 (fs-long only), available=19 (12 original + 7 released)
	if sk.GetStock() != 30 || sk.GetReservedStock() != 3 || sk.GetFlashSaleReservedStock() != 8 || sk.GetAvailableStock() != 19 {
		t.Fatalf("post-sweep SKU state unexpected: stock=%d reserved=%d flash=%d avail=%d",
			sk.GetStock(), sk.GetReservedStock(), sk.GetFlashSaleReservedStock(), sk.GetAvailableStock())
	}

	// Second sweep is idempotent (0 released).
	n, err = srv.SweepExpiredFlashSaleReservations(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("second sweep: want n=0 err=nil, got n=%d err=%v", n, err)
	}
}

func TestFlashSaleStockReservation_ConcurrentRaceSafety(t *testing.T) {
	st := store.NewMemoryStore()
	srv := New(st, nil)

	promoSvcCtx := ctxWithService("promotion-service")
	endsAt := time.Now().UTC().Add(time.Hour)

	_, prod := setupApprovedProduct(t, srv, "shop-race", []*productv1.SKUInput{
		{SkuCode: "RACE-1", Name: "Default", PriceVnd: 100000, Stock: 80},
		{SkuCode: "RACE-2", Name: "ReleaseRace", PriceVnd: 100000, Stock: 50},
	})
	sku1 := prod.Skus[0].Id
	sku2 := prod.Skus[1].Id

	// Phase 1: sku1 has 80 stock; reserve 50 for flash -> 30 normal available + 50 flash reserved.
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-race-1",
		SkuId:           sku1,
		Quantity:        50,
		EndsAt:          timestamppb.New(endsAt),
	}); err != nil {
		t.Fatalf("reserve fs-race-1: %v", err)
	}

	var (
		wg          sync.WaitGroup
		startCh     = make(chan struct{})
		regWins     atomic.Int32
		regFails    atomic.Int32
		flashWins   atomic.Int32
		flashFails  atomic.Int32
		dupResErrs  atomic.Int32
		unexpectedE atomic.Int32
	)

	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			ctx := ctxWithRole(fmt.Sprintf("reg-buyer-%d", idx), "buyer", "")
			_, err := srv.ReserveStock(ctx, &productv1.ReserveStockRequest{
				OrderId: fmt.Sprintf("race-reg-%d", idx),
				Items:   []*productv1.StockReservationItem{{SkuId: sku1, Quantity: 1}},
			})
			if err == nil {
				regWins.Add(1)
				return
			}
			c := status.Code(err)
			if c == codes.FailedPrecondition || c == codes.Aborted {
				regFails.Add(1)
				return
			}
			unexpectedE.Add(1)
		}(i)
	}

	for i := 0; i < 90; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh
			ctx := ctxWithRole(fmt.Sprintf("flash-buyer-%d", idx), "buyer", "")
			_, err := srv.ReserveStock(ctx, &productv1.ReserveStockRequest{
				OrderId: fmt.Sprintf("race-flash-%d", idx),
				Items:   []*productv1.StockReservationItem{{SkuId: sku1, Quantity: 1, FlashSaleItemId: "fs-race-1"}},
			})
			if err == nil {
				flashWins.Add(1)
				return
			}
			if status.Code(err) == codes.FailedPrecondition {
				flashFails.Add(1)
				return
			}
			unexpectedE.Add(1)
		}(i)
	}

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			resp, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
				FlashSaleItemId: "fs-race-1",
				SkuId:           sku1,
				Quantity:        50,
				EndsAt:          timestamppb.New(endsAt),
			})
			if err != nil || !resp.GetAlreadyReserved() {
				dupResErrs.Add(1)
			}
		}()
	}

	close(startCh)
	wg.Wait()

	if unexpectedE.Load() != 0 {
		t.Fatalf("unexpected errors during concurrent race: %d", unexpectedE.Load())
	}
	if dupResErrs.Load() != 0 {
		t.Fatalf("idempotent duplicate ReserveFlashSaleStock failed %d times", dupResErrs.Load())
	}
	if regWins.Load() > 30 {
		t.Fatalf("regular orders OVER-SOLD into flash reserve: wins=%d (max 30)", regWins.Load())
	}
	if flashWins.Load() != 50 {
		t.Fatalf("expected EXACTLY 50 flash order wins, got %d (fails=%d)", flashWins.Load(), flashFails.Load())
	}

	b1, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{sku1}})
	if err != nil || len(b1.GetSkus()) != 1 {
		t.Fatalf("GetSKUsBatch sku1: %v", err)
	}
	sk1 := b1.GetSkus()[0]
	if sk1.GetFlashSaleReservedStock() != 0 {
		t.Fatalf("expected sku1 flash_sale_reserved_stock=0, got %d", sk1.GetFlashSaleReservedStock())
	}
	if sk1.GetReservedStock() != regWins.Load()+flashWins.Load() {
		t.Fatalf("sku1 reserved_stock=%d != regWins(%d)+flashWins(%d)", sk1.GetReservedStock(), regWins.Load(), flashWins.Load())
	}
	if sk1.GetStock() != sk1.GetAvailableStock()+sk1.GetReservedStock()+sk1.GetFlashSaleReservedStock() {
		t.Fatalf("sku1 conservation violated: stock=%d avail=%d reserved=%d flash=%d",
			sk1.GetStock(), sk1.GetAvailableStock(), sk1.GetReservedStock(), sk1.GetFlashSaleReservedStock())
	}

	// Phase 2: Concurrent flash orders racing with concurrent ReleaseFlashSaleStock on sku2 (stock=50, flash=40).
	if _, err := srv.ReserveFlashSaleStock(promoSvcCtx, &productv1.ReserveFlashSaleStockRequest{
		FlashSaleItemId: "fs-race-2",
		SkuId:           sku2,
		Quantity:        40,
		EndsAt:          timestamppb.New(endsAt),
	}); err != nil {
		t.Fatalf("reserve fs-race-2: %v", err)
	}

	var (
		wg2       sync.WaitGroup
		start2    = make(chan struct{})
		wins2     atomic.Int32
		relTotals atomic.Int32
		relErrs   atomic.Int32
	)
	for i := 0; i < 30; i++ {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			<-start2
			ctx := ctxWithRole(fmt.Sprintf("flash2-buyer-%d", idx), "buyer", "")
			if _, err := srv.ReserveStock(ctx, &productv1.ReserveStockRequest{
				OrderId: fmt.Sprintf("race2-flash-%d", idx),
				Items:   []*productv1.StockReservationItem{{SkuId: sku2, Quantity: 1, FlashSaleItemId: "fs-race-2"}},
			}); err == nil {
				wins2.Add(1)
			}
		}(i)
	}
	for i := 0; i < 5; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			<-start2
			resp, err := srv.ReleaseFlashSaleStock(promoSvcCtx, &productv1.ReleaseFlashSaleStockRequest{
				FlashSaleItemId: "fs-race-2",
			})
			if err != nil {
				relErrs.Add(1)
				return
			}
			relTotals.Add(resp.GetReleasedQty())
		}()
	}
	close(start2)
	wg2.Wait()

	if relErrs.Load() != 0 {
		t.Fatalf("concurrent ReleaseFlashSaleStock had %d errors", relErrs.Load())
	}
	b2, err := srv.GetSKUsBatch(context.Background(), &productv1.GetSKUsBatchRequest{SkuIds: []string{sku2}})
	if err != nil || len(b2.GetSkus()) != 1 {
		t.Fatalf("GetSKUsBatch sku2: %v", err)
	}
	sk2 := b2.GetSkus()[0]
	if sk2.GetFlashSaleReservedStock() != 0 {
		t.Fatalf("sku2 flash_sale_reserved_stock should be 0 after release, got %d", sk2.GetFlashSaleReservedStock())
	}
	if wins2.Load()+relTotals.Load() != 40 {
		t.Fatalf("sku2 flash accounting mismatch: wins(%d) + released(%d) != 40", wins2.Load(), relTotals.Load())
	}
	if sk2.GetReservedStock() != wins2.Load() || sk2.GetAvailableStock() != 50-wins2.Load() {
		t.Fatalf("sku2 final state violated: stock=%d reserved=%d avail=%d wins=%d",
			sk2.GetStock(), sk2.GetReservedStock(), sk2.GetAvailableStock(), wins2.Load())
	}
}
