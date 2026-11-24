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

	if err := engine.ReconcileVoucherKeys(ctx, "v-life", domain.VoucherStatusActive, 10, 0, 100000, 0); err != nil {
		t.Fatal(err)
	}

	// Pause voucher in Redis -> Reserve MUST fail atomically with VOUCHER_INACTIVE without mutating counters
	if err := engine.ReconcileVoucherKeys(ctx, "v-life", domain.VoucherStatusPaused, 10, 0, 100000, 0); err != nil {
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
	if err := engine.ReconcileVoucherKeys(ctx, "v-life", domain.VoucherStatusActive, 10, 0, 100000, 0); err != nil {
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
	if err := engine.ReconcileVoucherKeys(ctx, "v-life", domain.VoucherStatusDeleted, 10, 1, 100000, 10000); err != nil {
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

func TestLua_W2B_ReconcileVoucherKeys_AbsoluteSync(t *testing.T) {
	ctx := context.Background()
	_, engine := newTestEngine(t)

	if err := engine.ReconcileVoucherKeys(ctx, "v-adj", domain.VoucherStatusActive, 10, 0, 100000, 0); err != nil {
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

	// Reconcile to quota=15 (used=1 -> rem=14) and budget=150000 (used=20000 -> rem=130000)
	if err := engine.ReconcileVoucherKeys(ctx, "v-adj", domain.VoucherStatusActive, 15, 1, 150000, 20000); err != nil {
		t.Fatalf("ReconcileVoucherKeys: %v", err)
	}

	remQ, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-adj"))
	remB, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-adj"))
	if remQ != 14 || remB != 130000 {
		t.Fatalf("expected remQ=14 remB=130000 after reconcile, got remQ=%d remB=%d", remQ, remB)
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

	// ReconcileVoucherKeys sets exact remaining counters and status atomically
	if err := engine.ReconcileVoucherKeys(ctx, "v-sync", domain.VoucherStatusActive, 10, 0, 50000, 10000); err != nil {
		t.Fatalf("ReconcileVoucherKeys(ACTIVE): %v", err)
	}
	if err := engine.ReconcileVoucherKeys(ctx, "v-sync", domain.VoucherStatusPaused, 10, 0, 50000, 10000); err != nil {
		t.Fatalf("ReconcileVoucherKeys(PAUSED): %v", err)
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
		t.Fatalf("ReconcileVoucherKeys counters unexpected, got %+v", vals)
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

	// IsReservationReleased: absent key -> false; active reservation -> false; after Release -> true
	if engine.IsReservationReleased(ctx, "ord-never") {
		t.Fatalf("absent reservation key should report IsReservationReleased=false")
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

func TestLua_R3_LongReservationTTL(t *testing.T) {
	ctx := context.Background()
	mr, engine := newTestEngine(t)

	if err := engine.ReconcileVoucherKeys(ctx, "v-ttl", domain.VoucherStatusActive, 5, 0, 50000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID: "ord-ttl-ok",
		UserID:  "u-ttl-ok",
		Vouchers: []lua.VoucherReserveSpec{
			{VoucherID: "v-ttl", PerUserLimit: 1, DiscountVND: 10000, HasBudget: true},
		},
	}); err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// Reservation key TTL must be either no-TTL (0) or >= 365 days (not 30 days)
	ttl := mr.TTL(lua.ReservationKey("ord-ttl-ok"))
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
	if err := engine.ReconcileVoucherKeys(ctx, "v-r4-tomb", domain.VoucherStatusActive, 5, 0, 50000, 0); err != nil {
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

func TestLua_W3A_ReconcileVoucherAndUserUsageKeys_AndNoZombieBudget(t *testing.T) {
	ctx := context.Background()
	mr, engine := newTestEngine(t)

	// 1. ReconcileVoucherKeys clamps remaining strictly to [0, quota] and [0, budget], overwrites stale Redis drift,
	// and deletes budget_remaining when budgetVND == 0.
	_ = mr.Set(lua.VoucherRemainingKey("v-rec"), "999")
	_ = mr.Set(lua.VoucherBudgetRemainingKey("v-rec"), "9999999")
	_ = mr.Set(lua.VoucherStatusKey("v-rec"), domain.VoucherStatusActive)

	if err := engine.ReconcileVoucherKeys(ctx, "v-rec", domain.VoucherStatusPaused, 10, 3, 100000, 30000); err != nil {
		t.Fatalf("ReconcileVoucherKeys: %v", err)
	}
	if rem, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-rec")); rem != 7 {
		t.Fatalf("ReconcileVoucherKeys rem: got %d want 7", rem)
	}
	if bud, _, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-rec")); bud != 70000 {
		t.Fatalf("ReconcileVoucherKeys budget: got %d want 70000", bud)
	}
	if st, err := mr.Get(lua.VoucherStatusKey("v-rec")); err != nil || st != domain.VoucherStatusPaused {
		t.Fatalf("ReconcileVoucherKeys status: got %q err=%v", st, err)
	}

	// Clamping negative used_count or used_count > quota never exceeds quota or goes negative.
	if err := engine.ReconcileVoucherKeys(ctx, "v-rec", domain.VoucherStatusActive, 10, -5, 0, 0); err != nil {
		t.Fatalf("ReconcileVoucherKeys clamp upper + del budget: %v", err)
	}
	if rem, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-rec")); rem != 10 {
		t.Fatalf("ReconcileVoucherKeys upper clamp: got %d want 10 (never > quota)", rem)
	}
	if _, exists, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-rec")); exists {
		t.Fatalf("ReconcileVoucherKeys with budgetVND=0 must delete budget_remaining key")
	}

	// 2. release_atomic.lua & release_partial_atomic.lua MUST NOT create a zombie budget key if budget was disabled.
	if err := engine.ReconcileVoucherKeys(ctx, "v-zombie", domain.VoucherStatusActive, 5, 0, 50000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID:  "ord-zombie-1",
		UserID:   "u-z1",
		Vouchers: []lua.VoucherReserveSpec{{VoucherID: "v-zombie", PerUserLimit: 2, DiscountVND: 10000, HasBudget: true}},
	}); err != nil {
		t.Fatal(err)
	}
	mr.Del(lua.VoucherBudgetRemainingKey("v-zombie"))
	if _, err := engine.Release(ctx, "ord-zombie-1"); err != nil {
		t.Fatal(err)
	}
	if _, exists, _ := engine.GetInt64(ctx, lua.VoucherBudgetRemainingKey("v-zombie")); exists {
		t.Fatalf("release_atomic.lua resurrected deleted budget_remaining key (zombie key)")
	}

	// 3. ReconcileUserUsageKeys & SetUserUsageKey restore positive and DELETE zero per-user counters for all 3 kinds.
	if err := engine.ReconcileUserUsageKeys(ctx, []domain.UserUsage{
		{Kind: domain.UsageKindVoucher, TargetID: "v-rec", UserID: "u-1", UsedQty: 2},
		{Kind: domain.UsageKindFlash, TargetID: "f-rec", UserID: "u-1", UsedQty: 3},
		{Kind: domain.UsageKindShopProgram, TargetID: "p-rec", UserID: "u-1", UsedQty: 4},
	}); err != nil {
		t.Fatalf("ReconcileUserUsageKeys: %v", err)
	}
	if v, _, _ := engine.GetInt64(ctx, lua.VoucherUserKey("v-rec", "u-1")); v != 2 {
		t.Fatalf("voucher user usage: got %d want 2", v)
	}
	if v, _, _ := engine.GetInt64(ctx, lua.FlashUserKey("f-rec", "u-1")); v != 3 {
		t.Fatalf("flash user usage: got %d want 3", v)
	}
	if v, _, _ := engine.GetInt64(ctx, lua.ShopProgramUserKey("p-rec", "u-1")); v != 4 {
		t.Fatalf("shop prog user usage: got %d want 4", v)
	}
	if err := engine.ReconcileUserUsageKeys(ctx, []domain.UserUsage{
		{Kind: domain.UsageKindVoucher, TargetID: "v-rec", UserID: "u-1", UsedQty: 0},
		{Kind: domain.UsageKindFlash, TargetID: "f-rec", UserID: "u-1", UsedQty: 0},
		{Kind: domain.UsageKindShopProgram, TargetID: "p-rec", UserID: "u-1", UsedQty: 0},
	}); err != nil {
		t.Fatalf("ReconcileUserUsageKeys(0): %v", err)
	}
	for _, key := range []string{
		lua.VoucherUserKey("v-rec", "u-1"),
		lua.FlashUserKey("f-rec", "u-1"),
		lua.ShopProgramUserKey("p-rec", "u-1"),
	} {
		if _, exists, _ := engine.GetInt64(ctx, key); exists {
			t.Fatalf("ReconcileUserUsageKeys(0) must delete stale key %s", key)
		}
	}
	// 4. UndoReserve reverts counters AND deletes promo:res key (NO tombstone), allowing immediate retry on same order_id,
	// while never deleting an already-written RELEASED tombstone.
	if err := engine.ReconcileVoucherKeys(ctx, "v-undo", domain.VoucherStatusActive, 5, 0, 50000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID:          "ord-undo-1",
		UserID:           "u-undo",
		Vouchers:         []lua.VoucherReserveSpec{{VoucherID: "v-undo", PerUserLimit: 2, DiscountVND: 10000, HasBudget: true}},
		ShopProgramItems: []lua.ShopProgramReserveSpec{{ItemID: "sp-undo", Quantity: 1, PerUserLimit: 1}},
	}); err != nil {
		t.Fatalf("Reserve(ord-undo-1): %v", err)
	}
	if uOut, err := engine.UndoReserve(ctx, "ord-undo-1"); err != nil || !uOut.Released || uOut.AlreadyReleased {
		t.Fatalf("UndoReserve(ord-undo-1): out=%+v err=%v", uOut, err)
	}
	if mr.Exists(lua.ReservationKey("ord-undo-1")) {
		t.Fatalf("UndoReserve must delete promo:res key without writing RELEASED tombstone")
	}
	if rem, _, _ := engine.GetInt64(ctx, lua.VoucherRemainingKey("v-undo")); rem != 5 {
		t.Fatalf("UndoReserve voucher rem: got %d want 5", rem)
	}
	if _, exists, _ := engine.GetInt64(ctx, lua.ShopProgramUserKey("sp-undo", "u-undo")); exists {
		t.Fatalf("UndoReserve must restore shop program :user: key")
	}
	// Immediate re-reserve with same order_id succeeds!
	if r2, err := engine.Reserve(ctx, lua.ReserveInput{
		OrderID:  "ord-undo-1",
		UserID:   "u-undo",
		Vouchers: []lua.VoucherReserveSpec{{VoucherID: "v-undo", PerUserLimit: 2, DiscountVND: 10000, HasBudget: true}},
	}); err != nil || !r2.Reserved || r2.AlreadyReserved {
		t.Fatalf("retry Reserve after UndoReserve failed: out=%+v err=%v", r2, err)
	}
	// Existing RELEASED tombstone is preserved by UndoReserve.
	if _, err := engine.Release(ctx, "ord-undo-1"); err != nil {
		t.Fatal(err)
	}
	if u2, err := engine.UndoReserve(ctx, "ord-undo-1"); err != nil || !u2.AlreadyReleased {
		t.Fatalf("UndoReserve on RELEASED tombstone: out=%+v err=%v", u2, err)
	}
	if val, err := mr.Get(lua.ReservationKey("ord-undo-1")); err != nil || val != "RELEASED" {
		t.Fatalf("UndoReserve corrupted existing RELEASED tombstone: val=%q err=%v", val, err)
	}
}

// TestLua_W3A_R8_P2_1_ReleaseDoesNotCreateMissingRemainingKeys verifies Review 4 P2-1:
// release_atomic.lua (Release & UndoReserve) and release_partial_atomic.lua MUST NOT INCR/INCRBY
// a missing :remaining key into a bogus 1/qty that blocks *_NOT_INITIALIZED self-rehydration.
func TestLua_W3A_R8_P2_1_ReleaseDoesNotCreateMissingRemainingKeys(t *testing.T) {
	ctx := context.Background()

	runCase := func(t *testing.T, mode string) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)

		const (
			vid = "v-p21"
			fid = "f-p21"
			uid = "u-p21"
			oid = "ord-p21"
		)
		if err := eng.ReconcileVoucherKeys(ctx, vid, domain.VoucherStatusActive, 10, 0, 100000, 0); err != nil {
			t.Fatal(err)
		}
		if err := eng.InitFlashItemSlots(ctx, fid, 10, true); err != nil {
			t.Fatal(err)
		}
		if _, err := eng.Reserve(ctx, lua.ReserveInput{
			OrderID:    oid,
			UserID:     uid,
			Vouchers:   []lua.VoucherReserveSpec{{VoucherID: vid, PerUserLimit: 5, DiscountVND: 10000, HasBudget: true}},
			FlashItems: []lua.FlashReserveSpec{{ItemID: fid, Quantity: 2, PerUserLimit: 5}},
		}); err != nil {
			t.Fatalf("Reserve: %v", err)
		}

		// Simulate eviction/loss of :remaining and :budget_remaining keys before release.
		mr.Del(lua.VoucherRemainingKey(vid))
		mr.Del(lua.VoucherBudgetRemainingKey(vid))
		mr.Del(lua.FlashRemainingKey(fid))

		switch mode {
		case "Release":
			if _, err := eng.Release(ctx, oid); err != nil {
				t.Fatalf("Release: %v", err)
			}
		case "UndoReserve":
			if _, err := eng.UndoReserve(ctx, oid); err != nil {
				t.Fatalf("UndoReserve: %v", err)
			}
		case "ReleasePartial":
			if _, err := eng.ReleasePartial(ctx, oid, []string{
				lua.VoucherRemainingKey(vid),
				lua.FlashRemainingKey(fid),
			}); err != nil {
				t.Fatalf("ReleasePartial: %v", err)
			}
		}

		if val, exists, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid)); exists {
			t.Fatalf("P2-1 bug (%s): recreated missing voucher :remaining key with bogus val=%d", mode, val)
		}
		if val, exists, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fid)); exists {
			t.Fatalf("P2-1 bug (%s): recreated missing flash :remaining key with bogus val=%d", mode, val)
		}

		// Subsequent Reserve MUST hit *_NOT_INITIALIZED so the service rehydrates true PG counts!
		_, vErr := eng.Reserve(ctx, lua.ReserveInput{
			OrderID:  "ord-p21-next-v",
			UserID:   "u-p21-2",
			Vouchers: []lua.VoucherReserveSpec{{VoucherID: vid, PerUserLimit: 5, DiscountVND: 10000, HasBudget: true}},
		})
		var resErr *lua.ReservationError
		if !errors.As(vErr, &resErr) || resErr.Code != lua.ErrCodeVoucherNotInitialized {
			t.Fatalf("P2-1 (%s): expected VOUCHER_NOT_INITIALIZED after release on missing key, got %v", mode, vErr)
		}
		_, fErr := eng.Reserve(ctx, lua.ReserveInput{
			OrderID:    "ord-p21-next-f",
			UserID:     "u-p21-2",
			FlashItems: []lua.FlashReserveSpec{{ItemID: fid, Quantity: 1, PerUserLimit: 5}},
		})
		if !errors.As(fErr, &resErr) || resErr.Code != lua.ErrCodeFlashNotInitialized {
			t.Fatalf("P2-1 (%s): expected FLASH_NOT_INITIALIZED after release on missing key, got %v", mode, fErr)
		}
	}

	t.Run("Release", func(t *testing.T) { runCase(t, "Release") })
	t.Run("UndoReserve", func(t *testing.T) { runCase(t, "UndoReserve") })
	t.Run("ReleasePartial", func(t *testing.T) { runCase(t, "ReleasePartial") })
}
