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

func TestReleasePartial_IdempotentAndLeavesRemainderForFullRelease(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	if err := engine.InitFlashItemSlots(ctx, "fi-1", 10, true); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitFlashItemSlots(ctx, "fi-2", 10, true); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitVoucherKeys(ctx, "v-s1", 5, 50000); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitVoucherKeys(ctx, "v-s2", 5, 50000); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitVoucherKeys(ctx, "v-plat", 5, 100000); err != nil {
		t.Fatal(err)
	}

	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "grp-1",
		UserID:  "u-1",
		FlashItems: []lua.FlashReserveSpec{
			{ItemID: "fi-1", Quantity: 2, PerUserLimit: 5},
			{ItemID: "fi-2", Quantity: 3, PerUserLimit: 5},
		},
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-s1", PerUserLimit: 2, DiscountVND: 15000, HasBudget: true},
			{VoucherID: "v-s2", PerUserLimit: 2, DiscountVND: 10000, HasBudget: true},
			{VoucherID: "v-plat", PerUserLimit: 2, DiscountVND: 20000, HasBudget: true},
		},
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// Partial release shop-1 keys (fi-1, v-s1)
	targets := []string{lua.FlashRemainingKey("fi-1"), lua.VoucherRemainingKey("v-s1")}
	p1, err := engine.ReleasePartial(ctx, "grp-1", targets)
	if err != nil || !p1.Released || p1.AlreadyReleased {
		t.Fatalf("1st ReleasePartial unexpected: %+v err=%v", p1, err)
	}

	// Duplicate partial release for same keys -> idempotent no-op
	p2, err := engine.ReleasePartial(ctx, "grp-1", targets)
	if err != nil || !p2.Released || !p2.AlreadyReleased {
		t.Fatalf("2nd ReleasePartial should be already_released: %+v err=%v", p2, err)
	}

	// Verify ONLY shop-1 restored; shop-2 + platform still reserved
	fi1, _, _ := engine.GetInt64(ctx, lua.FlashRemainingKey("fi-1"))
	fi2, _, _ := engine.GetInt64(ctx, lua.FlashRemainingKey("fi-2"))
	vs1, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-s1"))
	vs1Bud, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-s1"))
	vs2, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-s2"))
	vplat, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-plat"))
	if fi1 != 10 || fi2 != 7 || vs1 != 5 || vs1Bud != 50000 || vs2 != 4 || vplat != 4 {
		t.Fatalf("unexpected state after partial release: fi1=%d fi2=%d vs1=%d vs1Bud=%d vs2=%d vplat=%d",
			fi1, fi2, vs1, vs1Bud, vs2, vplat)
	}

	// Full Release releases ONLY remaining entries (fi-2, v-s2, v-plat) and never double-restores fi-1 or v-s1
	full, err := engine.Release(ctx, "grp-1")
	if err != nil || !full.Released || full.AlreadyReleased {
		t.Fatalf("full Release after partial unexpected: %+v err=%v", full, err)
	}
	fi1, _, _ = engine.GetInt64(ctx, lua.FlashRemainingKey("fi-1"))
	fi2, _, _ = engine.GetInt64(ctx, lua.FlashRemainingKey("fi-2"))
	vs1, _, _ = engine.GetInt64(ctx, lua.VoucherRemainingKey("v-s1"))
	vs2, _, _ = engine.GetInt64(ctx, lua.VoucherRemainingKey("v-s2"))
	vplat, _, _ = engine.GetInt64(ctx, lua.VoucherRemainingKey("v-plat"))
	if fi1 != 10 || fi2 != 10 || vs1 != 5 || vs2 != 5 || vplat != 5 {
		t.Fatalf("double release detected after full Release: fi1=%d fi2=%d vs1=%d vs2=%d vplat=%d",
			fi1, fi2, vs1, vs2, vplat)
	}

	// Partial release after full release -> ALREADY_RELEASED
	pAfter, err := engine.ReleasePartial(ctx, "grp-1", []string{lua.FlashRemainingKey("fi-2")})
	if err != nil || !pAfter.Released || !pAfter.AlreadyReleased {
		t.Fatalf("ReleasePartial after full Release should be already_released: %+v err=%v", pAfter, err)
	}
}
