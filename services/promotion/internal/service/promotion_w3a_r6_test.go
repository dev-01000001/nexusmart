package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// TestService_W3A_R6_Item1_UserKeysHave120sTTL verifies D12 Item 1:
// Every Redis :user: write (reserve_atomic.lua, release_atomic.lua, release_partial_atomic.lua,
// and ReconcileUserUsageKeys) sets TTL == 120s so stale keys expire automatically.
func TestService_W3A_R6_Item1_UserKeysHave120sTTL(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	eng := lua.NewEngine(rdb)

	const wantTTL = 120 * time.Second
	checkTTL := func(t *testing.T, label, key string) {
		t.Helper()
		ttl := mr.TTL(key)
		if ttl <= 0 || ttl > wantTTL || ttl < wantTTL-5*time.Second {
			t.Fatalf("D12 Item 1 bug (%s): key %s TTL=%v, want ~%v", label, key, ttl, wantTTL)
		}
	}

	// 1. reserve_atomic.lua sets 120s TTL on voucher, flash, and shop program :user: keys.
	if err := eng.ReconcileVoucherKeys(ctx, "v-ttl120", domain.VoucherStatusActive, 10, 0, 100000, 0); err != nil {
		t.Fatal(err)
	}
	if err := eng.InitFlashItemSlots(ctx, "f-ttl120", 10, true); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if _, err := eng.Reserve(ctx, lua.ReserveInput{
			OrderID:          fmt.Sprintf("ord-ttl120-%d", i),
			UserID:           "u-ttl",
			Vouchers:         []lua.VoucherReserveSpec{{VoucherID: "v-ttl120", PerUserLimit: 5, DiscountVND: 10000, HasBudget: true}},
			FlashItems:       []lua.FlashReserveSpec{{ItemID: "f-ttl120", Quantity: 1, PerUserLimit: 5}},
			ShopProgramItems: []lua.ShopProgramReserveSpec{{ItemID: "p-ttl120", Quantity: 1, PerUserLimit: 5}},
		}); err != nil {
			t.Fatalf("Reserve %d: %v", i, err)
		}
	}
	checkTTL(t, "reserve voucher", lua.VoucherUserKey("v-ttl120", "u-ttl"))
	checkTTL(t, "reserve flash", lua.FlashUserKey("f-ttl120", "u-ttl"))
	checkTTL(t, "reserve shop_prog", lua.ShopProgramUserKey("p-ttl120", "u-ttl"))

	// Fast-forward 60s then release_partial_atomic.lua (count 2 -> 1) MUST refresh TTL back to 120s.
	mr.FastForward(60 * time.Second)
	if _, err := eng.ReleasePartial(ctx, "ord-ttl120-1", []string{
		lua.VoucherRemainingKey("v-ttl120"),
		lua.FlashRemainingKey("f-ttl120"),
		lua.ShopProgramUserKey("p-ttl120", "u-ttl"),
	}); err != nil {
		t.Fatalf("ReleasePartial: %v", err)
	}
	checkTTL(t, "release_partial voucher", lua.VoucherUserKey("v-ttl120", "u-ttl"))
	checkTTL(t, "release_partial flash", lua.FlashUserKey("f-ttl120", "u-ttl"))
	checkTTL(t, "release_partial shop_prog", lua.ShopProgramUserKey("p-ttl120", "u-ttl"))

	// Reserve a 3rd order (count 1 -> 2), fast-forward 60s, then full release_atomic.lua (2 -> 1) refreshes TTL to 120s.
	if _, err := eng.Reserve(ctx, lua.ReserveInput{
		OrderID:          "ord-ttl120-3",
		UserID:           "u-ttl",
		Vouchers:         []lua.VoucherReserveSpec{{VoucherID: "v-ttl120", PerUserLimit: 5, DiscountVND: 10000, HasBudget: true}},
		FlashItems:       []lua.FlashReserveSpec{{ItemID: "f-ttl120", Quantity: 1, PerUserLimit: 5}},
		ShopProgramItems: []lua.ShopProgramReserveSpec{{ItemID: "p-ttl120", Quantity: 1, PerUserLimit: 5}},
	}); err != nil {
		t.Fatalf("Reserve 3: %v", err)
	}
	mr.FastForward(60 * time.Second)
	if _, err := eng.Release(ctx, "ord-ttl120-3"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	checkTTL(t, "release voucher", lua.VoucherUserKey("v-ttl120", "u-ttl"))
	checkTTL(t, "release flash", lua.FlashUserKey("f-ttl120", "u-ttl"))
	checkTTL(t, "release shop_prog", lua.ShopProgramUserKey("p-ttl120", "u-ttl"))

	// ReconcileUserUsageKeys MUST also set 120s TTL on positive counts.
	if err := eng.ReconcileUserUsageKeys(ctx, []domain.UserUsage{
		{Kind: domain.UsageKindVoucher, TargetID: "v-rec120", UserID: "u-rec", UsedQty: 1},
		{Kind: domain.UsageKindFlash, TargetID: "f-rec120", UserID: "u-rec", UsedQty: 1},
		{Kind: domain.UsageKindShopProgram, TargetID: "p-rec120", UserID: "u-rec", UsedQty: 1},
	}); err != nil {
		t.Fatalf("ReconcileUserUsageKeys: %v", err)
	}
	checkTTL(t, "reconcile voucher", lua.VoucherUserKey("v-rec120", "u-rec"))
	checkTTL(t, "reconcile flash", lua.FlashUserKey("f-rec120", "u-rec"))
	checkTTL(t, "reconcile shop_prog", lua.ShopProgramUserKey("p-rec120", "u-rec"))

	// Fast-forward 121s -> stale :user: keys auto-expire cleanly!
	mr.FastForward(121 * time.Second)
	if _, exists, _ := eng.GetInt64(ctx, lua.VoucherUserKey("v-rec120", "u-rec")); exists {
		t.Fatalf("D12 Item 1 bug: :user: key did not expire after 121s")
	}
}

// TestService_W3A_R6_Item3_CrossUserAndSameUserCommitReconcile verifies Review 3 P1-2a, P1-2b, P1-3, and P1-1 (alreadyCommitted):
//   - 2a: Ambiguous commit error when order belongs to User A while User B overwrote promo:res:<ord1> with vB ->
//     undoRedisReserve(ord1) (no tombstone) + reconcile User B's targets -> canceling ord1 does NOT inflate vB!
//   - 2b: alreadyCommitted when order belongs to User A while User B ran Lua ->
//     undoRedisReserve(ord1) (NOT compensateRedisRelease) -> ord1 stays NOT released in Redis!
//   - 3:  Ambiguous commit error when order is RESERVED for SAME user after promo:res eviction ->
//     reconcileTargets(skipLocked=false) restores Redis rem & :user: before returning AlreadyReserved=true!
//   - 1:  alreadyCommitted same user when voucher row is momentarily locked -> waits (skipLocked=false) and converges Redis!
func TestService_W3A_R6_Item3_CrossUserAndSameUserCommitReconcile(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		ctx := context.Background()
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r6-3", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r6-item3", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vAResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R63VA" + suffix, Name: "VA", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 10, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vA := vAResp.Voucher.Id

		vBResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R63VB" + suffix, Name: "VB", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 10, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vB := vBResp.Voucher.Id

		userA := "r6-3-userA-" + suffix
		userB := "r6-3-userB-" + suffix

		// User B validly reserves vB on ordB -> PG vB used=1 (rem=9), vB:user:userB=1.
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r6-3-ordB-" + suffix, UserId: userB, PlatformVoucherId: vB,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("Reserve(ordB): %v", err)
		}

		// --- Scenario 2a: Ambiguous commit error + cross-user (User B hits User A's ord1 with vB) ---
		ord2a := "r6-3-ord2a-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord2a, UserId: userA, PlatformVoucherId: vA,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("Reserve(ord2a): %v", err)
		}
		mr.Del(lua.ReservationKey(ord2a))

		getBypass2a := false
		hook.mu.Lock()
		hook.onGetReservation = func(_ context.Context, id string) (*domain.PromotionReservation, error, bool) {
			if id == ord2a && !getBypass2a {
				getBypass2a = true
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		hook.onCommit = func(_ context.Context, res domain.PromotionReservation, _ time.Time) (*domain.PromotionReservation, bool, error, bool) {
			if res.OrderID == ord2a {
				return nil, false, context.DeadlineExceeded, true
			}
			return nil, false, nil, false
		}
		hook.mu.Unlock()

		_, err2a := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord2a, UserId: userB, PlatformVoucherId: vB,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		hook.mu.Lock()
		hook.onGetReservation = nil
		hook.onCommit = nil
		hook.mu.Unlock()
		if status.Code(err2a) != codes.PermissionDenied {
			t.Fatalf("P1-2a: want PermissionDenied, got %v", err2a)
		}
		if eng.IsReservationReleased(ctx, ord2a) {
			t.Fatalf("P1-2a bug: promo:res:%s must NOT be tombstoned RELEASED for User A's active order", ord2a)
		}
		if mr.Exists(lua.ReservationKey(ord2a)) {
			t.Fatalf("P1-2a bug: User B's payload remained in promo:res:%s (must be undone via undoRedisReserve)", ord2a)
		}
		// User A now cancels ord2a -> MUST NOT touch unrelated voucher vB!
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ord2a}); err != nil {
			t.Fatalf("ReleasePromotion(ord2a): %v", err)
		}
		if remB, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vB)); remB != 9 {
			t.Fatalf("P1-2a bug: canceling User A's ord2a inflated unrelated vB remaining to %d (want 9)", remB)
		}
		if uB, _, _ := eng.GetInt64(ctx, lua.VoucherUserKey(vB, userB)); uB != 1 {
			t.Fatalf("P1-2a bug: canceling User A's ord2a corrupted unrelated vB:user:userB to %d (want 1)", uB)
		}

		// --- Scenario 2b: alreadyCommitted + cross-user (User B hits User A's ord2b) ---
		ord2b := "r6-3-ord2b-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord2b, UserId: userA, PlatformVoucherId: vA,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("Reserve(ord2b): %v", err)
		}
		mr.Del(lua.ReservationKey(ord2b))

		getBypass2b := false
		hook.mu.Lock()
		hook.onGetReservation = func(_ context.Context, id string) (*domain.PromotionReservation, error, bool) {
			if id == ord2b && !getBypass2b {
				getBypass2b = true
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		hook.mu.Unlock()

		_, err2b := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord2b, UserId: userB, PlatformVoucherId: vB,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		hook.mu.Lock()
		hook.onGetReservation = nil
		hook.mu.Unlock()
		if status.Code(err2b) != codes.PermissionDenied {
			t.Fatalf("P1-2b: want PermissionDenied, got %v", err2b)
		}
		if eng.IsReservationReleased(ctx, ord2b) {
			t.Fatalf("P1-2b bug: alreadyCommitted cross-user wrote RELEASED tombstone onto User A's active order %s!", ord2b)
		}
		if remB, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vB)); remB != 9 {
			t.Fatalf("P1-2b bug: vB remaining drifted to %d (want 9)", remB)
		}

		// --- Scenario 3: Ambiguous commit error + SAME user RESERVED -> MUST reconcile Redis ---
		ord3 := "r6-3-ord3-" + suffix
		// Currently vA has 1 active reservation (ord2b -> PG rem=9, userA=1). Reserve ord3 -> PG rem=8, userA=2.
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord3, UserId: userA, PlatformVoucherId: vA,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("Reserve(ord3): %v", err)
		}
		mr.Del(lua.ReservationKey(ord3))

		getBypass3 := false
		hook.mu.Lock()
		hook.onGetReservation = func(_ context.Context, id string) (*domain.PromotionReservation, error, bool) {
			if id == ord3 && !getBypass3 {
				getBypass3 = true
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		hook.onCommit = func(_ context.Context, res domain.PromotionReservation, _ time.Time) (*domain.PromotionReservation, bool, error, bool) {
			if res.OrderID == ord3 {
				return nil, false, errors.New("connection reset by peer"), true
			}
			return nil, false, nil, false
		}
		hook.mu.Unlock()

		resp3, err3 := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord3, UserId: userA, PlatformVoucherId: vA,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		hook.mu.Lock()
		hook.onGetReservation = nil
		hook.onCommit = nil
		hook.mu.Unlock()
		if err3 != nil || !resp3.GetReserved() || !resp3.GetAlreadyReserved() {
			t.Fatalf("P1-3: want AlreadyReserved=true, got resp=%+v err=%v", resp3, err3)
		}
		if remA, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vA)); remA != 8 {
			t.Fatalf("P1-3 bug: ambiguous commit same-user leaked vA remaining to %d (want 8)", remA)
		}
		if uA, _, _ := eng.GetInt64(ctx, lua.VoucherUserKey(vA, userA)); uA != 2 {
			t.Fatalf("P1-3 bug: ambiguous commit same-user leaked vA:user:userA to %d (want 2)", uA)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })

	// --- Scenario P1-1 (alreadyCommitted skipLocked=false on real Postgres) ---
	t.Run("P1_1_AlreadyCommittedWaitsForRowLock_PostgresStore", func(t *testing.T) {
		dsn := os.Getenv("TEST_PROMOTION_DB_URL")
		if dsn == "" {
			t.Skip("TEST_PROMOTION_DB_URL not set")
		}
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		pgStore := store.NewPostgresStore(pool)
		if err := pgStore.Migrate(ctx); err != nil {
			t.Fatal(err)
		}

		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: pgStore}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r6-p11", "admin", "")
		orderSvcCtx := ctxWithService("order-service")
		svc := service.NewPromotionService(hook, eng, logger.New("r6-p11", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R6P11" + suffix, Name: "P11", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 2, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id
		ord1 := "r6-p11-ord1-" + suffix
		userA := "r6-p11-uA-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord1, UserId: userA, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatal(err)
		}

		// Hold FOR UPDATE on the voucher row for 100ms while duplicate ord1 hits alreadyCommitted!
		// With skipLocked=true (old bug), SyncVoucherRedis skips and leaves Redis rem=0 forever (PG rem=1)!
		// With skipLocked=false (fix), SyncVoucherRedis waits for the lock to release and restores Redis rem=1!
		mr.Del(lua.ReservationKey(ord1))
		lockTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var lockedID string
		if err := lockTx.QueryRow(ctx, `SELECT id FROM vouchers WHERE id = $1 FOR UPDATE`, vid).Scan(&lockedID); err != nil {
			_ = lockTx.Rollback(ctx)
			t.Fatal(err)
		}
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = lockTx.Rollback(ctx)
		}()

		getBypass := false
		hook.mu.Lock()
		hook.onGetReservation = func(_ context.Context, id string) (*domain.PromotionReservation, error, bool) {
			if id == ord1 && !getBypass {
				getBypass = true
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		hook.mu.Unlock()

		resp, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord1, UserId: userA, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if err != nil || !resp.GetAlreadyReserved() {
			t.Fatalf("alreadyCommitted retry failed: resp=%+v err=%v", resp, err)
		}
		if rem, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid)); rem != 1 {
			t.Fatalf("P1-1 bug: alreadyCommitted used skipLocked=true and left Redis rem=%d (want 1)", rem)
		}
	})
}

// TestService_W3A_R6_Item4_ListFlashSaleItemsClampsMinPGRedis verifies Review 3 P2-7:
// ListFlashSaleItems clamps remaining_slots to min(PG remaining_slots, Redis live) and never > total_slots.
func TestService_W3A_R6_Item4_ListFlashSaleItemsClampsMinPGRedis(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "shop-r6-7-" + suffix
		skuID := "sku-r6-7-" + suffix
		adminCtx := ctxWithRole("admin-r6-7", "admin", "")
		sellerCtx := ctxWithRole("seller-r6-7", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuID, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(base, eng, logger.New("r6-p27", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R6P27 " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fi, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, ShopId: shopID, SkuId: skuID, FlashPriceVnd: 120000, TotalSlots: 2, PerUserLimit: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fi.Item.Id

		// Reserve 1 -> PG remaining_slots = 1 (total_slots = 2).
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r6-p27-o1-" + suffix, UserId: "r6-p27-u1-" + suffix,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuID, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		}); err != nil {
			t.Fatal(err)
		}

		// Inflate Redis to 9 (Scenario B) -> ListFlashSaleItems MUST clamp to min(PG=1, Redis=9) = 1!
		_ = mr.Set(lua.FlashRemainingKey(fid), "9")
		listResp, err := svc.ListFlashSaleItems(sellerCtx, &promotionv1.ListFlashSaleItemsRequest{
			FlashSaleId: fs.FlashSale.Id, ShopId: shopID,
		})
		if err != nil || len(listResp.GetItems()) != 1 {
			t.Fatalf("ListFlashSaleItems: resp=%+v err=%v", listResp, err)
		}
		if got := listResp.GetItems()[0].GetRemainingSlots(); got != 1 {
			t.Fatalf("P2-7 bug: ListFlashSaleItems returned remaining_slots=%d (want min(PG=1, Redis=9)=1)", got)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R6_Item2_BackgroundDriftReconcilerTwoConsecutiveTicks verifies D12 Item 2 on both
// MemoryStore and real PostgresStore:
//
//	(a) PG rem=1, Redis rem=0 (voucher + flash) -> unchanged after tick 1, converged to 1 after tick 2 & reservable!
//	(b) Reverse direction PG rem=0, Redis rem=5 (voucher + budget + flash) -> unchanged after tick 1, 0 after tick 2!
//	(c) In-flight order (Redis lower on tick 1, commits PG between tick 1 & tick 2, plus moving-window pair change) -> untouched!
//	(d) StartRedisReconciler background loop uses a local tracker and heals persistent drift automatically.
func TestService_W3A_R6_Item2_BackgroundDriftReconcilerTwoConsecutiveTicks(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: base}

		var syncMu sync.Mutex
		syncedVouchers := make(map[string]int)
		syncedFlash := make(map[string]int)
		hook.onSyncVoucherRedis = func(voucherID string, _ bool) {
			syncMu.Lock()
			syncedVouchers[voucherID]++
			syncMu.Unlock()
		}
		hook.onSyncFlashItemRedis = func(itemID string, _ bool) {
			syncMu.Lock()
			syncedFlash[itemID]++
			syncMu.Unlock()
		}
		resetSyncCounts := func() {
			syncMu.Lock()
			clear(syncedVouchers)
			clear(syncedFlash)
			syncMu.Unlock()
		}
		getSyncCounts := func(vid, fid string) (int, int) {
			syncMu.Lock()
			defer syncMu.Unlock()
			return syncedVouchers[vid], syncedFlash[fid]
		}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "r6-i2-shop-" + suffix
		sku1 := "r6-i2-sku1-" + suffix
		sku2 := "r6-i2-sku2-" + suffix
		sku3 := "r6-i2-sku3-" + suffix

		pc := newFakeProductClient()
		for _, sku := range []string{sku1, sku2, sku3} {
			pc.setSKU(&productv1.SKU{Id: sku, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 50, IsActive: true})
		}

		svc := service.NewPromotionService(hook, eng, logger.New("r6-i2", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		ctx := context.Background()
		adminCtx := ctxWithRole("admin-r6-i2", "admin", "")
		sellerCtx := ctxWithRole("seller-r6-i2", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		vResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R6I2V1" + suffix, Name: "I2 V1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 2, PerUserLimit: 5, BudgetVnd: 20000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vResp.Voucher.Id

		fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R6I2FS " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fiResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: sku1, FlashPriceVnd: 120000, TotalSlots: 2, PerUserLimit: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fiResp.Item.Id

		// Consume 1 of each -> PG voucher rem=1 (budget=10000), PG flash rem=1.
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r6-i2-o1-" + suffix, UserId: "r6-i2-u1-" + suffix, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID, SubtotalVnd: 200000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: sku1, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		}); err != nil {
			t.Fatal(err)
		}

		// --- (a) PG rem=1, Redis rem=0 -> unchanged after tick 1, converged to 1 after tick 2 & reservable ---
		_ = mr.Set(lua.VoucherRemainingKey(vid), "0")
		_ = mr.Set(lua.VoucherBudgetRemainingKey(vid), "0")
		_ = mr.Set(lua.FlashRemainingKey(fid), "0")

		tracker := make(service.RedisDriftTracker)
		resetSyncCounts()
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 1 (a): %v", err)
		}
		if vCnt, fCnt := getSyncCounts(vid, fid); vCnt != 0 || fCnt != 0 {
			t.Fatalf("(a) tick 1 must NOT sync on first drift observation: vSync=%d fSync=%d", vCnt, fCnt)
		}
		if rV, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid)); rV != 0 {
			t.Fatalf("(a) tick 1 changed voucher rem prematurely: got %d want 0", rV)
		}
		if rF, _, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fid)); rF != 0 {
			t.Fatalf("(a) tick 1 changed flash rem prematurely: got %d want 0", rF)
		}

		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 2 (a): %v", err)
		}
		if rV, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid)); rV != 1 {
			t.Fatalf("(a) tick 2 did not converge voucher rem to 1: got %d", rV)
		}
		if rB, _, _ := eng.GetInt64(ctx, lua.VoucherBudgetRemainingKey(vid)); rB != 10000 {
			t.Fatalf("(a) tick 2 did not converge voucher budget to 10000: got %d", rB)
		}
		if rF, _, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fid)); rF != 1 {
			t.Fatalf("(a) tick 2 did not converge flash rem to 1: got %d", rF)
		}

		// Recovered slot is now truly reservable -> consumes the final slot (PG rem=0, budget=0)!
		if resp2, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r6-i2-o2-" + suffix, UserId: "r6-i2-u2-" + suffix, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID, SubtotalVnd: 200000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: sku1, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		}); err != nil || !resp2.GetReserved() {
			t.Fatalf("(a) reserve after 2-tick convergence failed: resp=%+v err=%v", resp2, err)
		}

		// --- (b) Reverse direction: PG rem=0, Redis rem=5 (Scenario B) -> unchanged on tick 1, 0 on tick 2 ---
		_ = mr.Set(lua.VoucherRemainingKey(vid), "5")
		_ = mr.Set(lua.VoucherBudgetRemainingKey(vid), "50000")
		_ = mr.Set(lua.FlashRemainingKey(fid), "5")
		resetSyncCounts()

		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 1 (b): %v", err)
		}
		if vCnt, fCnt := getSyncCounts(vid, fid); vCnt != 0 || fCnt != 0 {
			t.Fatalf("(b) tick 1 must NOT sync on first drift observation: vSync=%d fSync=%d", vCnt, fCnt)
		}
		if rV, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid)); rV != 5 {
			t.Fatalf("(b) tick 1 changed voucher rem prematurely: got %d want 5", rV)
		}
		if rF, _, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fid)); rF != 5 {
			t.Fatalf("(b) tick 1 changed flash rem prematurely: got %d want 5", rF)
		}

		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 2 (b): %v", err)
		}
		if rV, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid)); rV != 0 {
			t.Fatalf("(b) tick 2 did not converge voucher rem to 0: got %d", rV)
		}
		if rB, _, _ := eng.GetInt64(ctx, lua.VoucherBudgetRemainingKey(vid)); rB != 0 {
			t.Fatalf("(b) tick 2 did not converge voucher budget to 0: got %d", rB)
		}
		if rF, _, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fid)); rF != 0 {
			t.Fatalf("(b) tick 2 did not converge flash rem to 0: got %d", rF)
		}

		// --- (c) In-flight order: Redis lower on tick 1, commits PG between tick 1 & tick 2 -> NEVER touched! ---
		vResp2, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R6I2V2" + suffix, Name: "I2 V2", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 3, PerUserLimit: 5, BudgetVnd: 30000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid2 := vResp2.Voucher.Id
		fiResp2, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: sku2, FlashPriceVnd: 120000, TotalSlots: 3, PerUserLimit: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp2.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid2 := fiResp2.Item.Id

		inFlightEntered := make(chan struct{})
		releaseInFlight := make(chan struct{})
		ordInFlight := "r6-i2-inflight-" + suffix
		hook.mu.Lock()
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.OrderID == ordInFlight {
				close(inFlightEntered)
				<-releaseInFlight
			}
		}
		hook.mu.Unlock()

		var wg sync.WaitGroup
		var inFlightErr error
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, inFlightErr = svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: ordInFlight, UserId: "r6-i2-u3-" + suffix, PlatformVoucherId: vid2,
				ShopItems: []*promotionv1.ShopDiscountInput{{
					ShopId: shopID, SubtotalVnd: 200000,
					Items: []*promotionv1.DiscountLineItem{{SkuId: sku2, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid2}},
				}},
			})
		}()
		<-inFlightEntered

		// While in-flight: Redis rem=2, PG rem=3. Tick 1 observes (PG=3, Redis=2) without touching!
		resetSyncCounts()
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 1 (c): %v", err)
		}
		if vCnt, fCnt := getSyncCounts(vid2, fid2); vCnt != 0 || fCnt != 0 {
			t.Fatalf("(c) tick 1 touched in-flight reservation: vSync=%d fSync=%d", vCnt, fCnt)
		}

		// Commit completes between tick 1 and tick 2 -> now PG rem=2 == Redis rem=2!
		close(releaseInFlight)
		wg.Wait()
		hook.mu.Lock()
		hook.onBeforeCommit = nil
		hook.mu.Unlock()
		if inFlightErr != nil {
			t.Fatalf("in-flight ReservePromotion failed: %v", inFlightErr)
		}

		// Tick 2 sees PG==Redis -> MUST NOT sync!
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 2 (c): %v", err)
		}
		if vCnt, fCnt := getSyncCounts(vid2, fid2); vCnt != 0 || fCnt != 0 {
			t.Fatalf("(c) tick 2 synced already-converged in-flight reservation: vSync=%d fSync=%d", vCnt, fCnt)
		}
		if rV, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid2)); rV != 2 {
			t.Fatalf("(c) voucher rem=%d want 2", rV)
		}
		if rF, _, _ := eng.GetInt64(ctx, lua.FlashRemainingKey(fid2)); rF != 2 {
			t.Fatalf("(c) flash rem=%d want 2", rF)
		}

		// Moving-window in-flight: tick 1 sees (PG=2, Redis=1); before tick 2 that order commits and
		// another enters Lua so tick 2 sees (PG=1, Redis=0) -> pair changed, still MUST NOT touch!
		if _, err := eng.Reserve(ctx, lua.ReserveInput{
			OrderID:    "r6-i2-mw1-" + suffix,
			UserID:     "r6-i2-mw-u1-" + suffix,
			Vouchers:   []lua.VoucherReserveSpec{{VoucherID: vid2, PerUserLimit: 5, DiscountVND: 10000, HasBudget: true}},
			FlashItems: []lua.FlashReserveSpec{{ItemID: fid2, Quantity: 1, PerUserLimit: 5}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("moving-window tick 1: %v", err)
		}
		// Commit first in-flight order in PG (PG rem 2->1) and start second in Lua (Redis rem 1->0).
		if _, _, err := base.CommitReservation(ctx, domain.PromotionReservation{
			OrderID: "r6-i2-mw1-" + suffix, UserID: "r6-i2-mw-u1-" + suffix,
			Vouchers:       []domain.ReservedVoucher{{VoucherID: vid2, Scope: domain.VoucherScopePlatform, DiscountVND: 10000, HasBudget: true}},
			FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fid2, SKUID: sku2, Quantity: 1, ShopID: shopID}},
		}, now); err != nil {
			t.Fatal(err)
		}
		if _, err := eng.Reserve(ctx, lua.ReserveInput{
			OrderID:    "r6-i2-mw2-" + suffix,
			UserID:     "r6-i2-mw-u2-" + suffix,
			Vouchers:   []lua.VoucherReserveSpec{{VoucherID: vid2, PerUserLimit: 5, DiscountVND: 10000, HasBudget: true}},
			FlashItems: []lua.FlashReserveSpec{{ItemID: fid2, Quantity: 1, PerUserLimit: 5}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("moving-window tick 2: %v", err)
		}
		if vCnt, fCnt := getSyncCounts(vid2, fid2); vCnt != 0 || fCnt != 0 {
			t.Fatalf("(c) moving-window in-flight with changed (pg,redis) pair was touched: vSync=%d fSync=%d", vCnt, fCnt)
		}

		// --- (d) StartRedisReconciler loop uses its own local tracker and heals persistent drift ---
		vResp3, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R6I2V3" + suffix, Name: "I2 V3", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 1, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid3 := vResp3.Voucher.Id
		_ = mr.Set(lua.VoucherRemainingKey(vid3), "0")

		recCtx, recCancel := context.WithCancel(ctx)
		recDone := make(chan struct{})
		go func() {
			defer close(recDone)
			svc.StartRedisReconciler(recCtx, 25*time.Millisecond)
		}()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if rV, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid3)); rV == 1 {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
		recCancel()
		<-recDone
		if rV, _, _ := eng.GetInt64(ctx, lua.VoucherRemainingKey(vid3)); rV != 1 {
			t.Fatalf("(d) StartRedisReconciler did not converge drifted voucher to 1: got %d", rV)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R8_P1_1_DriftReconcilerHealsStaleVoucherStatus verifies Review 4 P1-1:
// When Redis promo:voucher:<id>:status is stale (e.g. PAUSED from snapshot rollback / delayed write)
// while PG is ACTIVE and remaining/budget match PG, ReconcileRedisDriftOnce detects the status drift
// in the same MGET and heals it back to ACTIVE after two consecutive identical ticks.
func TestService_W3A_R8_P1_1_DriftReconcilerHealsStaleVoucherStatus(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: base}

		var syncMu sync.Mutex
		syncedVouchers := make(map[string]int)
		hook.onSyncVoucherRedis = func(voucherID string, _ bool) {
			syncMu.Lock()
			syncedVouchers[voucherID]++
			syncMu.Unlock()
		}
		getVoucherSyncs := func(vid string) int {
			syncMu.Lock()
			defer syncMu.Unlock()
			return syncedVouchers[vid]
		}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		ctx := context.Background()
		adminCtx := ctxWithRole("admin-r8-p11", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r8-p11", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vResp, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R8P11" + suffix, Name: "R8 Status Drift", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 2, PerUserLimit: 5, BudgetVnd: 20000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vResp.Voucher.Id

		// Reserve 1 -> PG rem=1, budget=10000; Redis rem=1, budget=10000, status=ACTIVE.
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r8-p11-o1-" + suffix, UserId: "r8-p11-u1-" + suffix, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatal(err)
		}

		// Corrupt ONLY Redis status to PAUSED while remaining & budget still match PG exactly!
		statusKey := lua.VoucherStatusKey(vid)
		_ = mr.Set(statusKey, domain.VoucherStatusPaused)

		// Before healing, ReservePromotion is blocked with VOUCHER_INACTIVE.
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r8-p11-blocked-" + suffix, UserId: "r8-p11-u2-" + suffix, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("expected VOUCHER_INACTIVE while Redis status=PAUSED, got %v", err)
		}

		tracker := make(service.RedisDriftTracker)
		beforeSyncs := getVoucherSyncs(vid)

		// Tick 1: records drift snapshot without touching Redis yet.
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 1: %v", err)
		}
		if getVoucherSyncs(vid) != beforeSyncs {
			t.Fatalf("tick 1 must not reconcile on first status drift observation")
		}
		if got, _ := mr.Get(statusKey); got != domain.VoucherStatusPaused {
			t.Fatalf("tick 1 changed status prematurely: got %q want PAUSED", got)
		}

		// Status changes PAUSED -> DELETED before tick 2: snapshot differs, so tick 2 must NOT sync yet.
		_ = mr.Set(statusKey, domain.VoucherStatusDeleted)
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 2 (changed status): %v", err)
		}
		if getVoucherSyncs(vid) != beforeSyncs {
			t.Fatalf("tick 2 must not reconcile when status drift snapshot changed (PAUSED->DELETED)")
		}
		if got, _ := mr.Get(statusKey); got != domain.VoucherStatusDeleted {
			t.Fatalf("tick 2 changed status prematurely: got %q want DELETED", got)
		}

		// Tick 3: second consecutive identical DELETED observation -> MUST heal back to ACTIVE!
		if _, err := svc.ReconcileRedisDriftOnce(ctx, tracker); err != nil {
			t.Fatalf("tick 3: %v", err)
		}
		if got, _ := mr.Get(statusKey); got != domain.VoucherStatusActive {
			t.Fatalf("P1-1 bug: ReconcileRedisDriftOnce did not heal stale status=%q back to ACTIVE", got)
		}

		// ReservePromotion now succeeds!
		if resp2, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r8-p11-o2-" + suffix, UserId: "r8-p11-u2-" + suffix, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil || !resp2.GetReserved() {
			t.Fatalf("ReservePromotion after status heal failed: resp=%+v err=%v", resp2, err)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}
