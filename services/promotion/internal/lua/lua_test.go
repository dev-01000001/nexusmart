package lua_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/nexusmart/core/services/promotion/internal/lua"
)

func newTestEngine(t *testing.T) (*miniredis.Miniredis, *lua.Engine) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr:     mr.Addr(),
		PoolSize: 200,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, lua.NewEngine(rdb)
}

func TestFlashSale_1000Concurrent_100Slots_ExactNoOversell(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	const (
		itemID      = "flash-item-100"
		totalSlots  = 100
		concurrency = 1000
	)
	if err := engine.InitFlashItemSlots(ctx, itemID, totalSlots, true); err != nil {
		t.Fatalf("InitFlashItemSlots: %v", err)
	}

	var (
		wg          sync.WaitGroup
		startCh     = make(chan struct{})
		succeeded   atomic.Int64
		soldOut     atomic.Int64
		otherErrors atomic.Int64
		winnersMu   sync.Mutex
		winnerIDs   = make([]string, 0, totalSlots)
	)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh

			orderID := fmt.Sprintf("order-%04d", idx)
			userID := fmt.Sprintf("user-%04d", idx)
			out, err := engine.Reserve(ctx, lua.ReserveInput{
				OrderID: orderID,
				UserID:  userID,
				FlashItems: []lua.FlashReserveSpec{
					{ItemID: itemID, Quantity: 1, PerUserLimit: 1},
				},
			})
			if err != nil {
				var resErr *lua.ReservationError
				if errors.As(err, &resErr) && resErr.Code == lua.ErrCodeFlashSoldOut {
					soldOut.Add(1)
					return
				}
				otherErrors.Add(1)
				return
			}
			if out.Reserved && !out.AlreadyReserved {
				succeeded.Add(1)
				winnersMu.Lock()
				winnerIDs = append(winnerIDs, orderID)
				winnersMu.Unlock()
			}
		}(i)
	}

	close(startCh)
	wg.Wait()

	if got := otherErrors.Load(); got != 0 {
		t.Fatalf("unexpected non-soldout errors: %d", got)
	}
	if got := succeeded.Load(); got != totalSlots {
		t.Fatalf("expected EXACTLY %d successes, got %d", totalSlots, got)
	}
	if got := soldOut.Load(); got != concurrency-totalSlots {
		t.Fatalf("expected EXACTLY %d sold-out rejections, got %d", concurrency-totalSlots, got)
	}

	rem, exists, err := engine.GetInt64(ctx, lua.FlashRemainingKey(itemID))
	if err != nil || !exists {
		t.Fatalf("GetInt64 remaining: exists=%v err=%v", exists, err)
	}
	if rem != 0 {
		t.Fatalf("expected 0 remaining slots, got %d", rem)
	}

	// Release all 100 winning orders (plus a duplicate release per order to prove idempotency).
	for _, orderID := range winnerIDs {
		out1, err := engine.Release(ctx, orderID)
		if err != nil {
			t.Fatalf("Release(%s) 1st: %v", orderID, err)
		}
		if !out1.Released || out1.AlreadyReleased {
			t.Fatalf("Release(%s) 1st unexpected outcome: %+v", orderID, out1)
		}

		out2, err := engine.Release(ctx, orderID)
		if err != nil {
			t.Fatalf("Release(%s) 2nd: %v", orderID, err)
		}
		if !out2.Released || !out2.AlreadyReleased {
			t.Fatalf("Release(%s) 2nd expected already_released=true, got %+v", orderID, out2)
		}
	}

	rem, exists, err = engine.GetInt64(ctx, lua.FlashRemainingKey(itemID))
	if err != nil || !exists {
		t.Fatalf("GetInt64 remaining after release: exists=%v err=%v", exists, err)
	}
	if rem != totalSlots {
		t.Fatalf("expected remaining slots restored to %d, got %d", totalSlots, rem)
	}
}

func TestReserveAtomic_AllOrNothingRollback(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	if err := engine.InitFlashItemSlots(ctx, "fi-1", 10, true); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitVoucherKeys(ctx, "v-ok", 5, 100000); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitVoucherKeys(ctx, "v-empty", 0, 100000); err != nil {
		t.Fatal(err)
	}

	_, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "order-atomic-fail",
		UserID:  "user-1",
		FlashItems: []lua.FlashReserveSpec{
			{ItemID: "fi-1", Quantity: 2, PerUserLimit: 5},
		},
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-ok", PerUserLimit: 1, DiscountVND: 20000, HasBudget: true},
			{VoucherID: "v-empty", PerUserLimit: 1, DiscountVND: 10000, HasBudget: true},
		},
	})
	var resErr *lua.ReservationError
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherOutOfQuota {
		t.Fatalf("expected VOUCHER_OUT_OF_QUOTA, got %v", err)
	}

	// Verify NOTHING was mutated!
	flashRem, _, _ := engine.GetInt64(ctx, lua.FlashRemainingKey("fi-1"))
	vOkRem, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-ok"))
	vOkBudget, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-ok"))
	flashUser, existsFlashUser, _ := engine.GetInt64(ctx, lua.FlashUserKey("fi-1", "user-1"))
	vOkUser, existsVOkUser, _ := engine.GetInt64(ctx, lua.VoucherUserKey("v-ok", "user-1"))

	if flashRem != 10 || vOkRem != 5 || vOkBudget != 100000 || existsFlashUser || existsVOkUser || flashUser != 0 || vOkUser != 0 {
		t.Fatalf("atomic rollback failed: flashRem=%d vOkRem=%d vOkBudget=%d flashUser=%d(%v) vOkUser=%d(%v)",
			flashRem, vOkRem, vOkBudget, flashUser, existsFlashUser, vOkUser, existsVOkUser)
	}
}

func TestReserveAtomic_IdempotentAndVoucherLimits(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	if err := engine.InitVoucherKeys(ctx, "v-budget", 10, 50000); err != nil {
		t.Fatal(err)
	}

	// 1st order: 30,000 VND discount (budget 50k -> 20k, quota 10 -> 9, user-1 count -> 1)
	res1, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-1",
		UserID:  "user-1",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-budget", PerUserLimit: 1, DiscountVND: 30000, HasBudget: true},
		},
	})
	if err != nil || !res1.Reserved || res1.AlreadyReserved {
		t.Fatalf("1st reserve unexpected: out=%+v err=%v", res1, err)
	}

	// Duplicate reserve for same order_id -> ALREADY_RESERVED, no double decrement
	res1Dup, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-1",
		UserID:  "user-1",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-budget", PerUserLimit: 1, DiscountVND: 30000, HasBudget: true},
		},
	})
	if err != nil || !res1Dup.Reserved || !res1Dup.AlreadyReserved {
		t.Fatalf("duplicate reserve unexpected: out=%+v err=%v", res1Dup, err)
	}

	// Same user, new order -> rejected by per_user_limit=1
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-2",
		UserID:  "user-1",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-budget", PerUserLimit: 1, DiscountVND: 10000, HasBudget: true},
		},
	})
	var resErr *lua.ReservationError
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherUserLimitExceeded {
		t.Fatalf("expected VOUCHER_USER_LIMIT_EXCEEDED, got %v", err)
	}

	// Different user, 30,000 VND discount -> rejected by remaining budget (20k < 30k)
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-3",
		UserID:  "user-2",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-budget", PerUserLimit: 1, DiscountVND: 30000, HasBudget: true},
		},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherBudgetExhausted {
		t.Fatalf("expected VOUCHER_BUDGET_EXHAUSTED, got %v", err)
	}

	// Release ord-1 -> restores quota (10), budget (50,000), and user-1 usage (0)
	rel, err := engine.Release(ctx, "ord-1")
	if err != nil || !rel.Released || rel.AlreadyReleased {
		t.Fatalf("release ord-1 unexpected: out=%+v err=%v", rel, err)
	}

	remQuota, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-budget"))
	remBudget, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-budget"))
	if remQuota != 10 || remBudget != 50000 {
		t.Fatalf("expected restored quota=10 budget=50000, got quota=%d budget=%d", remQuota, remBudget)
	}

	// Re-reserving released order_id must be rejected
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-1",
		UserID:  "user-1",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-budget", PerUserLimit: 1, DiscountVND: 30000, HasBudget: true},
		},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeOrderAlreadyReleased {
		t.Fatalf("expected ORDER_ALREADY_RELEASED, got %v", err)
	}

	// user-1 can now reserve on a fresh order_id since their per-user counter was restored!
	resFresh, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-4",
		UserID:  "user-1",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-budget", PerUserLimit: 1, DiscountVND: 30000, HasBudget: true},
		},
	})
	if err != nil || !resFresh.Reserved {
		t.Fatalf("expected user-1 fresh order to succeed after release, got out=%+v err=%v", resFresh, err)
	}
}

func TestEngine_HelpersAndRemainingErrorPaths(t *testing.T) {
	ctx := context.Background()
	mr, engine := newTestEngine(t)

	// SyncVoucherKeysIfMissing initializes missing keys and does NOT overwrite live keys
	if err := engine.SyncVoucherKeysIfMissing(ctx, "v-sync", 10, 50000, 40000); err != nil {
		t.Fatalf("SyncVoucherKeysIfMissing 1st: %v", err)
	}
	if err := engine.SyncVoucherKeysIfMissing(ctx, "v-sync", 99, 50000, 99999); err != nil {
		t.Fatalf("SyncVoucherKeysIfMissing 2nd: %v", err)
	}
	vals, err := engine.MGetInt64(ctx, []string{
		lua.VoucherRemainingKey("v-sync"),
		lua.VoucherBudgetRemainingKey("v-sync"),
		"promo:missing:key",
	})
	if err != nil {
		t.Fatalf("MGetInt64: %v", err)
	}
	if vals[lua.VoucherRemainingKey("v-sync")] != 10 || vals[lua.VoucherBudgetRemainingKey("v-sync")] != 40000 {
		t.Fatalf("SyncVoucherKeysIfMissing overwrote live keys: %+v", vals)
	}
	if _, exists := vals["promo:missing:key"]; exists {
		t.Fatal("missing key should not appear in MGetInt64 result")
	}
	emptyVals, err := engine.MGetInt64(ctx, nil)
	if err != nil || len(emptyVals) != 0 {
		t.Fatalf("MGetInt64 empty: vals=%+v err=%v", emptyVals, err)
	}

	// InitFlashItemSlots overwrite=false (SetNX) & DeleteFlashItemSlots
	if err := engine.InitFlashItemSlots(ctx, "fi-nx", 5, false); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitFlashItemSlots(ctx, "fi-nx", 20, false); err != nil {
		t.Fatal(err)
	}
	if rem, ok, _ := engine.GetInt64(ctx, lua.FlashRemainingKey("fi-nx")); !ok || rem != 5 {
		t.Fatalf("SetNX flash slots expected 5, got %d (%v)", rem, ok)
	}
	if err := engine.DeleteFlashItemSlots(ctx, "fi-nx"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := engine.GetInt64(ctx, lua.FlashRemainingKey("fi-nx")); ok {
		t.Fatal("expected flash slots key deleted")
	}

	// Lua error paths: FLASH_NOT_INITIALIZED, INVALID_FLASH_QTY, FLASH_USER_LIMIT_EXCEEDED, VOUCHER_NOT_INITIALIZED, VOUCHER_BUDGET_NOT_INITIALIZED
	var resErr *lua.ReservationError

	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID:    "err-flash-uninit",
		UserID:     "u1",
		FlashItems: []lua.FlashReserveSpec{{ItemID: "fi-nx", Quantity: 1, PerUserLimit: 1}},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeFlashNotInitialized || resErr.Error() == "" {
		t.Fatalf("expected FLASH_NOT_INITIALIZED, got %v", err)
	}

	if err := engine.InitFlashItemSlots(ctx, "fi-lim", 10, true); err != nil {
		t.Fatal(err)
	}
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID:    "err-flash-qty",
		UserID:     "u1",
		FlashItems: []lua.FlashReserveSpec{{ItemID: "fi-lim", Quantity: 0, PerUserLimit: 2}},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeInvalidFlashQty {
		t.Fatalf("expected INVALID_FLASH_QTY, got %v", err)
	}

	// Reserve 2 (limit=2), then try 1 more for same user -> FLASH_USER_LIMIT_EXCEEDED
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID:    "ok-flash-lim-1",
		UserID:     "u1",
		TTLSeconds: 60,
		FlashItems: []lua.FlashReserveSpec{{ItemID: "fi-lim", Quantity: 2, PerUserLimit: 2}},
	}); err != nil {
		t.Fatalf("ok-flash-lim-1: %v", err)
	}
	if engine.IsReservationReleased(ctx, "ok-flash-lim-1") {
		t.Fatal("expected active reservation IsReservationReleased=false")
	}
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID:    "err-flash-user-lim",
		UserID:     "u1",
		FlashItems: []lua.FlashReserveSpec{{ItemID: "fi-lim", Quantity: 1, PerUserLimit: 2}},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeFlashUserLimitExceeded {
		t.Fatalf("expected FLASH_USER_LIMIT_EXCEEDED, got %v", err)
	}

	// Release ok-flash-lim-1 -> IsReservationReleased=true; release unknown order -> AlreadyReleased=true
	if _, err := engine.Release(ctx, "ok-flash-lim-1"); err != nil {
		t.Fatal(err)
	}
	if !engine.IsReservationReleased(ctx, "ok-flash-lim-1") {
		t.Fatal("expected released reservation IsReservationReleased=true")
	}
	if !engine.IsReservationReleased(ctx, "never-existed") {
		t.Fatal("expected missing reservation IsReservationReleased=true")
	}
	relMiss, err := engine.Release(ctx, "never-existed")
	if err != nil || !relMiss.AlreadyReleased {
		t.Fatalf("release missing order unexpected: %+v err=%v", relMiss, err)
	}

	// VOUCHER_NOT_INITIALIZED & VOUCHER_BUDGET_NOT_INITIALIZED
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID:  "err-v-uninit",
		UserID:   "u1",
		Vouchers: []lua.VoucherReserveSpec{{VoucherID: "v-uninit", PerUserLimit: 1}},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherNotInitialized {
		t.Fatalf("expected VOUCHER_NOT_INITIALIZED, got %v", err)
	}
	if err := engine.InitVoucherKeys(ctx, "v-nobudget", 5, 0); err != nil {
		t.Fatal(err)
	}
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID:  "err-v-budget-uninit",
		UserID:   "u1",
		Vouchers: []lua.VoucherReserveSpec{{VoucherID: "v-nobudget", PerUserLimit: 1, DiscountVND: 1000, HasBudget: true}},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherBudgetNotInit {
		t.Fatalf("expected VOUCHER_BUDGET_NOT_INITIALIZED, got %v", err)
	}

	// Closed Redis connection error paths
	mr.Close()
	if _, _, err := engine.GetInt64(ctx, "k"); err == nil {
		t.Fatal("expected GetInt64 error after miniredis close")
	}
	if _, err := engine.MGetInt64(ctx, []string{"k"}); err == nil {
		t.Fatal("expected MGetInt64 error after miniredis close")
	}
	if engine.IsReservationReleased(ctx, "ord-closed") {
		t.Fatal("expected IsReservationReleased=false on redis error")
	}
	if _, err := engine.Reserve(ctx, lua.ReserveInput{OrderID: "x", UserID: "u"}); err == nil {
		t.Fatal("expected Reserve error after miniredis close")
	}
	if _, err := engine.Release(ctx, "x"); err == nil {
		t.Fatal("expected Release error after miniredis close")
	}
}
