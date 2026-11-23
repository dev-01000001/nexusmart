package lua_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
)

func newBurstTestEngine(t *testing.T) *lua.Engine {
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
	return lua.NewEngine(rdb)
}

func TestLua_W2B_VoucherPausedAndDeletedBlockedAtomically(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	if err := engine.InitVoucherKeys(ctx, "v-life", 10, 100000); err != nil {
		t.Fatal(err)
	}

	// Pause voucher in Redis -> Reserve MUST fail atomically with VOUCHER_INACTIVE without mutating counters
	if err := engine.SetVoucherStatusKey(ctx, "v-life", domain.VoucherStatusPaused); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-paused",
		UserID:  "u-1",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-life", PerUserLimit: 5, DiscountVND: 10000, HasBudget: true},
		},
	})
	var resErr *lua.ReservationError
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherInactive {
		t.Fatalf("expected VOUCHER_INACTIVE for PAUSED voucher, got %v", err)
	}
	if rem, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-life")); rem != 10 {
		t.Fatalf("expected quota untouched at 10, got %d", rem)
	}

	// Resume ACTIVE -> succeeds
	if err := engine.SetVoucherStatusKey(ctx, "v-life", domain.VoucherStatusActive); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-resumed",
		UserID:  "u-1",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-life", PerUserLimit: 5, DiscountVND: 10000, HasBudget: true},
		},
	}); err != nil {
		t.Fatalf("Reserve after resume failed: %v", err)
	}

	// Delete -> blocked atomically
	if err := engine.SetVoucherStatusKey(ctx, "v-life", domain.VoucherStatusDeleted); err != nil {
		t.Fatal(err)
	}
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-deleted",
		UserID:  "u-2",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-life", PerUserLimit: 5, DiscountVND: 10000, HasBudget: true},
		},
	})
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherInactive {
		t.Fatalf("expected VOUCHER_INACTIVE for DELETED voucher, got %v", err)
	}
}

func TestLua_W2B_AdjustVoucherKeys_DeltaSync(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	if err := engine.InitVoucherKeys(ctx, "v-adj", 10, 100000); err != nil {
		t.Fatal(err)
	}
	// Reserve 1 (rem quota 9, rem budget 80000)
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID:  "ord-adj-1",
		UserID:   "u-1",
		Vouchers: []lua.VoucherReserveSpec{{VoucherID: "v-adj", PerUserLimit: 5, DiscountVND: 20000, HasBudget: true}},
	}); err != nil {
		t.Fatal(err)
	}

	// Increase quota by +5 (10->15) and budget by +50000 (100k->150k)
	if err := engine.AdjustVoucherKeys(ctx, "v-adj", domain.VoucherStatusActive, 5, 14, 100000, 150000, 130000); err != nil {
		t.Fatalf("AdjustVoucherKeys: %v", err)
	}

	remQ, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-adj"))
	remB, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-adj"))
	if remQ != 14 || remB != 130000 {
		t.Fatalf("expected remQ=14 remB=130000 after delta adjust, got remQ=%d remB=%d", remQ, remB)
	}
}

func TestLua_W2B_ShopProgramPerUserLimit_1000Concurrent_ExactAndRelease(t *testing.T) {
	ctx := context.Background()
	engine := newBurstTestEngine(t)

	const (
		progItemID   = "spi-conc-1"
		perUserLimit = 5
		concurrency  = 1000
	)

	var (
		wg          sync.WaitGroup
		startCh     = make(chan struct{})
		succeeded   atomic.Int64
		limitHit    atomic.Int64
		otherErrors atomic.Int64
		winnersMu   sync.Mutex
		winnerIDs   = make([]string, 0, perUserLimit)
	)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-startCh

			orderID := fmt.Sprintf("sp-ord-%04d", idx)
			out, err := engine.Reserve(ctx, lua.ReserveInput{
				OrderID: orderID,
				UserID:  "same-buyer",
				ShopProgramItems: []lua.ShopProgramReserveSpec{
					{ItemID: progItemID, Quantity: 1, PerUserLimit: perUserLimit},
				},
			})
			if err != nil {
				var resErr *lua.ReservationError
				if errors.As(err, &resErr) && resErr.Code == lua.ErrCodeShopProgUserLimitExceeded {
					limitHit.Add(1)
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
		t.Fatalf("unexpected errors: %d", got)
	}
	if got := succeeded.Load(); got != perUserLimit {
		t.Fatalf("expected EXACTLY %d successes, got %d", perUserLimit, got)
	}
	if got := limitHit.Load(); got != concurrency-perUserLimit {
		t.Fatalf("expected EXACTLY %d per-user limit rejections, got %d", concurrency-perUserLimit, got)
	}

	used, exists, _ := engine.GetInt64(ctx, lua.ShopProgramUserKey(progItemID, "same-buyer"))
	if !exists || used != perUserLimit {
		t.Fatalf("expected user counter=%d, got %d (exists=%v)", perUserLimit, used, exists)
	}

	// Partial release on winner 0 (twice for idempotency) -> user counter drops from 5 to 4
	p1, err := engine.ReleasePartial(ctx, winnerIDs[0], []string{lua.ShopProgramUserKey(progItemID, "same-buyer")})
	if err != nil || !p1.Released || p1.AlreadyReleased {
		t.Fatalf("1st ReleasePartial unexpected: %+v err=%v", p1, err)
	}
	p2, err := engine.ReleasePartial(ctx, winnerIDs[0], []string{lua.ShopProgramUserKey(progItemID, "same-buyer")})
	if err != nil || !p2.Released || !p2.AlreadyReleased {
		t.Fatalf("2nd ReleasePartial should be already_released: %+v err=%v", p2, err)
	}
	used, _, _ = engine.GetInt64(ctx, lua.ShopProgramUserKey(progItemID, "same-buyer"))
	if used != perUserLimit-1 {
		t.Fatalf("expected user counter=%d after 1 partial release, got %d", perUserLimit-1, used)
	}

	// Full release on remaining winners -> user counter returns cleanly to 0 (key deleted)
	for _, oid := range winnerIDs {
		if _, err := engine.Release(ctx, oid); err != nil {
			t.Fatalf("Release(%s): %v", oid, err)
		}
	}
	used, exists, _ = engine.GetInt64(ctx, lua.ShopProgramUserKey(progItemID, "same-buyer"))
	if exists || used != 0 {
		t.Fatalf("expected user counter cleaned to 0 after full releases, got %d (exists=%v)", used, exists)
	}
}

func TestLua_W2B_HelperSyncMGetAndDelete(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	// SyncVoucherKeysWithStatus initializes counters once and overwrites status without clobbering live counters
	if err := engine.SyncVoucherKeysWithStatus(ctx, "v-sync", domain.VoucherStatusActive, 10, 50000, 40000); err != nil {
		t.Fatalf("SyncVoucherKeysWithStatus(ACTIVE): %v", err)
	}
	if err := engine.SyncVoucherKeysWithStatus(ctx, "v-sync", domain.VoucherStatusPaused, 99, 50000, 99999); err != nil {
		t.Fatalf("SyncVoucherKeysWithStatus(PAUSED): %v", err)
	}
	vals, err := engine.MGetInt64(ctx, []string{
		lua.VoucherRemainingKey("v-sync"),
		lua.VoucherBudgetRemainingKey("v-sync"),
		lua.VoucherRemainingKey("v-missing"),
	})
	if err != nil {
		t.Fatalf("MGetInt64: %v", err)
	}
	if vals[lua.VoucherRemainingKey("v-sync")] != 10 || vals[lua.VoucherBudgetRemainingKey("v-sync")] != 40000 {
		t.Fatalf("SyncVoucherKeysWithStatus should preserve live counters while overwriting status, got %+v", vals)
	}
	if _, ok := vals[lua.VoucherRemainingKey("v-missing")]; ok {
		t.Fatalf("missing key should not be present in MGetInt64 result")
	}

	// Empty MGetInt64 returns empty map
	if emptyVals, err := engine.MGetInt64(ctx, nil); err != nil || len(emptyVals) != 0 {
		t.Fatalf("MGetInt64(nil): got %+v err=%v", emptyVals, err)
	}

	// InitFlashItemSlots + DeleteFlashItemSlots
	if err := engine.InitFlashItemSlots(ctx, "fi-del", 5, true); err != nil {
		t.Fatalf("InitFlashItemSlots: %v", err)
	}
	if err := engine.DeleteFlashItemSlots(ctx, "fi-del"); err != nil {
		t.Fatalf("DeleteFlashItemSlots: %v", err)
	}
	if _, exists, _ := engine.GetInt64(ctx, lua.FlashRemainingKey("fi-del")); exists {
		t.Fatalf("expected flash slot key deleted")
	}

	// IsReservationReleased: absent key -> true; active reservation -> false; after Release -> true
	if !engine.IsReservationReleased(ctx, "ord-never") {
		t.Fatalf("absent reservation key should report IsReservationReleased=true")
	}
	if err := engine.InitFlashItemSlots(ctx, "fi-live", 3, true); err != nil {
		t.Fatalf("InitFlashItemSlots(fi-live): %v", err)
	}
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID:    "ord-live",
		UserID:     "u-live",
		FlashItems: []lua.FlashReserveSpec{{ItemID: "fi-live", Quantity: 1, PerUserLimit: 2}},
	}); err != nil {
		t.Fatalf("Reserve(ord-live): %v", err)
	}
	if engine.IsReservationReleased(ctx, "ord-live") {
		t.Fatalf("active reservation should report IsReservationReleased=false")
	}
	if _, err := engine.Release(ctx, "ord-live"); err != nil {
		t.Fatalf("Release(ord-live): %v", err)
	}
	if !engine.IsReservationReleased(ctx, "ord-live") {
		t.Fatalf("released order should report IsReservationReleased=true")
	}
	resErr := &lua.ReservationError{Code: lua.ErrCodeVoucherInactive, Key: "v-sync"}
	if resErr.Error() == "" {
		t.Fatalf("expected non-empty ReservationError string")
	}
}

func TestLua_R3_AdjustVoucherNoNegativeClamp_AndLongReservationTTL(t *testing.T) {
	ctx := context.Background()
	mr, engine := newTestEngine(t)

	// 1. Item B: AdjustVoucherKeys MUST NOT clamp negative remaining quota/budget to 0!
	// Voucher starts with quota=10, budget=100000. 5 concurrent orders reserve (rem=5, bud=50000).
	if err := engine.InitVoucherKeys(ctx, "v-noclamp", 10, 100000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := engine.Reserve(ctx, lua.ReserveInput{
			OrderID: fmt.Sprintf("ord-nc-%d", i),
			UserID:  fmt.Sprintf("u-nc-%d", i),
			Vouchers: []lua.VoucherReserveSpec{
				{VoucherID: "v-noclamp", PerUserLimit: 1, DiscountVND: 10000, HasBudget: true},
			},
		}); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}

	// Lower quota from 10 -> 3 (delta -7 => 5 - 7 = -2) and budget 100k -> 30k (delta -70k => 50k - 70k = -20k)
	if err := engine.AdjustVoucherKeys(ctx, "v-noclamp", domain.VoucherStatusActive, -7, 3, 100000, 30000, 30000); err != nil {
		t.Fatalf("AdjustVoucherKeys: %v", err)
	}
	rem, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-noclamp"))
	bud, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-noclamp"))
	if rem != -2 || bud != -20000 {
		t.Fatalf("adjust_voucher.lua must NOT clamp negative deficit: want rem=-2 bud=-20000, got rem=%d bud=%d", rem, bud)
	}

	// Release 1 of the 5 orders -> rem becomes -1, bud becomes -10000 -> next order MUST STILL be rejected!
	if _, err := engine.Release(ctx, "ord-nc-0"); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-nc-over",
		UserID:  "u-nc-over",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-noclamp", PerUserLimit: 1, DiscountVND: 10000, HasBudget: true},
		},
	})
	var resErr *lua.ReservationError
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeVoucherOutOfQuota {
		t.Fatalf("release 1 should leave deficit (-1) and reject new reserve, got %v", err)
	}

	// Release 2 more orders -> rem becomes +1, bud becomes +10000 -> now 2 active < quota 3, so next reserve succeeds!
	_, _ = engine.Release(ctx, "ord-nc-1")
	_, _ = engine.Release(ctx, "ord-nc-2")
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-nc-ok",
		UserID:  "u-nc-ok",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-noclamp", PerUserLimit: 1, DiscountVND: 10000, HasBudget: true},
		},
	}); err != nil {
		t.Fatalf("after repaying deficit, reserve should succeed: %v", err)
	}

	// 2. Item C: Reservation key TTL must be either no-TTL (0) or >= 365 days (not 30 days)
	ttl := mr.TTL(lua.ReservationKey("ord-nc-ok"))
	if ttl != 0 && ttl < 365*24*time.Hour {
		t.Fatalf("promo:res key TTL too short (%v); must be >= 365d or no TTL", ttl)
	}
}

func TestLua_R4_ReleaseMissingWritesTombstoneAndBlocksLateReserve(t *testing.T) {
	ctx := context.Background()
	mr, engine := newTestEngine(t)

	if err := engine.InitFlashItemSlots(ctx, "fi-r4-tomb", 5, true); err != nil {
		t.Fatal(err)
	}
	if err := engine.InitVoucherKeys(ctx, "v-r4-tomb", 5, 50000); err != nil {
		t.Fatal(err)
	}

	const orderID = "ord-r4-early-release"
	rel, err := engine.Release(ctx, orderID)
	if err != nil {
		t.Fatalf("Release(missing): %v", err)
	}
	if !rel.Released || !rel.AlreadyReleased {
		t.Fatalf("Release(missing) unexpected outcome: %+v", rel)
	}

	// release_atomic.lua MUST write RELEASED tombstone with TTL so late Reserve is blocked at Redis.
	resKey := lua.ReservationKey(orderID)
	val, err := mr.Get(resKey)
	if err != nil || val != "RELEASED" {
		t.Fatalf("expected Redis tombstone %s=RELEASED, got val=%q err=%v", resKey, val, err)
	}
	ttl := mr.TTL(resKey)
	if ttl != 0 && ttl < 365*24*time.Hour {
		t.Fatalf("expected tombstone TTL >= 365d or no TTL, got %v", ttl)
	}

	// Late Reserve with the same order_id MUST fail immediately with ORDER_ALREADY_RELEASED without mutating counters.
	_, err = engine.Reserve(ctx, lua.ReserveInput{
		OrderID:    orderID,
		UserID:     "u-r4",
		FlashItems: []lua.FlashReserveSpec{{ItemID: "fi-r4-tomb", Quantity: 1, PerUserLimit: 2}},
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-r4-tomb", PerUserLimit: 1, DiscountVND: 10000, HasBudget: true},
		},
		ShopProgramItems: []lua.ShopProgramReserveSpec{
			{ItemID: "sp-r4-tomb", Quantity: 1, PerUserLimit: 2},
		},
	})
	var resErr *lua.ReservationError
	if !errors.As(err, &resErr) || resErr.Code != lua.ErrCodeOrderAlreadyReleased {
		t.Fatalf("late Reserve after missing-key Release: want ORDER_ALREADY_RELEASED, got %v", err)
	}

	// Verify counters were NOT touched.
	if rem, _, _ := engine.GetInt64(ctx, lua.FlashRemainingKey("fi-r4-tomb")); rem != 5 {
		t.Fatalf("flash slots mutated: got %d want 5", rem)
	}
	if rem, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-r4-tomb")); rem != 5 {
		t.Fatalf("voucher quota mutated: got %d want 5", rem)
	}
	if bud, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-r4-tomb")); bud != 50000 {
		t.Fatalf("voucher budget mutated: got %d want 50000", bud)
	}
	if pu, _, _ := engine.GetInt64(ctx, lua.ShopProgramUserKey("sp-r4-tomb", "u-r4")); pu != 0 {
		t.Fatalf("shop program user counter mutated: got %d want 0", pu)
	}
}
