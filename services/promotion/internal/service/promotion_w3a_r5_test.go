package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type r5HookStore struct {
	store.Store
	mu                   sync.Mutex
	onBeforeCommit       func(res domain.PromotionReservation)
	onCommit             func(ctx context.Context, res domain.PromotionReservation, now time.Time) (*domain.PromotionReservation, bool, error, bool)
	onGetReservation     func(ctx context.Context, orderID string) (*domain.PromotionReservation, error, bool)
	onSyncVoucherRedis   func(voucherID string, skipLocked bool)
	onSyncFlashItemRedis func(itemID string, skipLocked bool)
	onSyncUserUsageRedis func(kind, targetID, userID string, skipLocked bool) error
	syncVoucherCalls     atomic.Int32
	syncFlashCalls       atomic.Int32
	syncUserUsageCalls   atomic.Int32
	commitCalls          atomic.Int32
}

func (h *r5HookStore) CommitReservation(
	ctx context.Context,
	res domain.PromotionReservation,
	now time.Time,
) (*domain.PromotionReservation, bool, error) {
	h.commitCalls.Add(1)
	h.mu.Lock()
	before := h.onBeforeCommit
	override := h.onCommit
	h.mu.Unlock()
	if before != nil {
		before(res)
	}
	if override != nil {
		if r, already, err, handled := override(ctx, res, now); handled {
			return r, already, err
		}
	}
	return h.Store.CommitReservation(ctx, res, now)
}

func (h *r5HookStore) GetReservation(ctx context.Context, orderID string) (*domain.PromotionReservation, error) {
	h.mu.Lock()
	override := h.onGetReservation
	h.mu.Unlock()
	if override != nil {
		if r, err, handled := override(ctx, orderID); handled {
			return r, err
		}
	}
	return h.Store.GetReservation(ctx, orderID)
}

func (h *r5HookStore) SyncVoucherRedis(
	ctx context.Context,
	voucherID string,
	skipLocked bool,
	syncFn func(ctx context.Context, v *domain.Voucher) error,
) (bool, error) {
	h.syncVoucherCalls.Add(1)
	h.mu.Lock()
	fn := h.onSyncVoucherRedis
	h.mu.Unlock()
	if fn != nil {
		fn(voucherID, skipLocked)
	}
	return h.Store.SyncVoucherRedis(ctx, voucherID, skipLocked, syncFn)
}

func (h *r5HookStore) SyncFlashItemRedis(
	ctx context.Context,
	itemID string,
	skipLocked bool,
	syncFn func(ctx context.Context, item *domain.FlashSaleItem) error,
) (bool, error) {
	h.syncFlashCalls.Add(1)
	h.mu.Lock()
	fn := h.onSyncFlashItemRedis
	h.mu.Unlock()
	if fn != nil {
		fn(itemID, skipLocked)
	}
	return h.Store.SyncFlashItemRedis(ctx, itemID, skipLocked, syncFn)
}

func (h *r5HookStore) SyncUserUsageRedis(
	ctx context.Context,
	kind, targetID, userID string,
	skipLocked bool,
	syncFn func(ctx context.Context, usedQty int32) error,
) (bool, error) {
	h.syncUserUsageCalls.Add(1)
	h.mu.Lock()
	fn := h.onSyncUserUsageRedis
	h.mu.Unlock()
	if fn != nil {
		if err := fn(kind, targetID, userID, skipLocked); err != nil {
			return false, err
		}
	}
	return h.Store.SyncUserUsageRedis(ctx, kind, targetID, userID, skipLocked, syncFn)
}

// selectiveRedisHook can delay/fail specific Redis commands or pipelines deterministically.
type selectiveRedisHook struct {
	mu           sync.RWMutex
	failPipeline error
	delaySet     time.Duration
	delayPipe    time.Duration
	evalCalls    atomic.Int32
	pipeCalls    atomic.Int32
	inFlightPipe atomic.Int32
	maxInFlight  atomic.Int32
	onSetKey     func(ctx context.Context, key string)
	onAfterCmd   func(ctx context.Context, cmd redis.Cmder, err error)
}

func (h *selectiveRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *selectiveRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		if name == "evalsha" || name == "eval" {
			h.evalCalls.Add(1)
		}
		h.mu.RLock()
		dSet := h.delaySet
		cb := h.onSetKey
		after := h.onAfterCmd
		h.mu.RUnlock()
		if name == "set" {
			if cb != nil && len(cmd.Args()) > 1 {
				cb(ctx, fmt.Sprint(cmd.Args()[1]))
			}
			if dSet > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(dSet):
				}
			}
		}
		err := next(ctx, cmd)
		if after != nil {
			after(ctx, cmd, err)
		}
		return err
	}
}

func (h *selectiveRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipeCalls.Add(1)
		cur := h.inFlightPipe.Add(1)
		defer h.inFlightPipe.Add(-1)
		for {
			prev := h.maxInFlight.Load()
			if cur <= prev || h.maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		h.mu.RLock()
		failErr := h.failPipeline
		dPipe := h.delayPipe
		cb := h.onSetKey
		h.mu.RUnlock()
		if cb != nil {
			for _, c := range cmds {
				if strings.ToLower(c.Name()) == "set" && len(c.Args()) > 1 {
					cb(ctx, fmt.Sprint(c.Args()[1]))
				}
			}
		}
		if dPipe > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(dPipe):
			}
		}
		if failErr != nil {
			return failErr
		}
		return next(ctx, cmds)
	}
}

// TestService_W3A_R5_Item1_CoinPrefixRejectedAndLedgerConflictRollsBack (P0-1):
//  1. CreditCoins rejects reserved internal prefixes (reserve:, release:, credit:) case-insensitively.
//  2. If coin_transactions already contains reserve:<order_id>, CommitReservation fails & rolls back (no free coins).
//  3. If coin_transactions already contains release:<order_id>:shop:<shop_id>, ReleasePromotionShop neither
//     double-credits nor reports CoinsReturned > 0.
func TestService_W3A_R5_Item1_CoinPrefixRejectedAndLedgerConflictRollsBack(t *testing.T) {
	runBasic := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		svc := service.NewPromotionService(base, eng, logger.New("r5-item1", logger.WithWriter(io.Discard)))

		adminCtx := ctxWithRole("admin-r5-1", "admin", "")
		buyerID := "r5-1-buyer-" + uuid.NewString()[:8]

		for _, badRef := range []string{
			"reserve:ord-1",
			"RESERVE:ord-2",
			"release:ord-1",
			"Release:ord-1:shop:shop-1",
			"credit:user:123",
			"CREDIT:user:456",
		} {
			if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
				UserId: buyerID, Amount: 1000, Reason: "bad", ReferenceId: badRef,
			}); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("CreditCoins(%q): want InvalidArgument, got %v", badRef, err)
			}
		}

		// Omitting ReferenceId (auto-generated credit:...) and normal external reference_id still succeed.
		if resp, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
			UserId: buyerID, Amount: 5000, Reason: "auto_ref",
		}); err != nil || resp.GetBalance() != 5000 {
			t.Fatalf("CreditCoins(empty ref): resp=%+v err=%v", resp, err)
		}
		if resp, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
			UserId: buyerID, Amount: 5000, Reason: "ext_ref", ReferenceId: "promo-ext-" + uuid.NewString()[:8],
		}); err != nil || resp.GetBalance() != 10000 {
			t.Fatalf("CreditCoins(external ref): resp=%+v err=%v", resp, err)
		}
	}

	t.Run("PrefixRejection_MemoryStore", func(t *testing.T) { runBasic(t, store.NewMemoryStore()) })
	t.Run("PrefixRejection_PostgresStore", func(t *testing.T) { runBasic(t, openR2PostgresStore(t)) })

	t.Run("LedgerConflictRollbackAndDuplicateRelease_PostgresStore", func(t *testing.T) {
		dsn := os.Getenv("TEST_PROMOTION_DB_URL")
		if dsn == "" {
			t.Skip("TEST_PROMOTION_DB_URL not set")
		}
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("pgxpool.New: %v", err)
		}
		t.Cleanup(pool.Close)
		pgStore := store.NewPostgresStore(pool)
		if err := pgStore.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		svc := service.NewPromotionService(pgStore, eng, logger.New("r5-item1-pg", logger.WithWriter(io.Discard)))

		adminCtx := ctxWithRole("admin-r5-1pg", "admin", "")
		orderSvcCtx := ctxWithService("order-service")
		suffix := uuid.NewString()[:8]
		buyerID := "r5-1pg-buyer-" + suffix
		ordConflict := "r5-1pg-ord-conflict-" + suffix

		if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
			UserId: buyerID, Amount: 10000, Reason: "seed", ReferenceId: "seed-" + suffix,
		}); err != nil {
			t.Fatal(err)
		}

		// Simulate pre-existing conflicting reserve:<order_id> row in coin_transactions (e.g. from another user/legacy).
		otherUser := "other-user-" + suffix
		if _, err := pool.Exec(ctx, `
			INSERT INTO coin_accounts (user_id, balance) VALUES ($1, 2) ON CONFLICT (user_id) DO NOTHING
		`, otherUser); err != nil {
			t.Fatalf("seed other coin account: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO coin_transactions (id, user_id, delta, reason, reference_id)
			VALUES ($1, $2, 1, 'legacy_collision', $3)
		`, uuid.NewString(), otherUser, "reserve:"+ordConflict); err != nil {
			t.Fatalf("seed conflicting reserve ref: %v", err)
		}

		// ReservePromotion MUST fail & rollback (never persist coins_used without debiting).
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordConflict, UserId: buyerID, CoinsToUse: 10000,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err == nil {
			t.Fatalf("P0-1 bug: ReservePromotion succeeded despite coin_transactions reserve:%s conflict!", ordConflict)
		}
		if _, err := pgStore.GetReservation(ctx, ordConflict); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("P0-1 bug: reservation was persisted despite coin debit rollback: err=%v", err)
		}
		// Calling ReleasePromotion afterwards must NOT mint free coins (balance stays 10,000, NOT 20,000!).
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ordConflict}); err != nil {
			t.Fatalf("ReleasePromotion after failed reserve: %v", err)
		}
		bal, _, err := pgStore.GetCoinBalance(ctx, buyerID, 10)
		if err != nil || bal != 10000 {
			t.Fatalf("P0-1 free-coins bug: want balance=10000, got %d (err=%v)", bal, err)
		}

		// Symmetric refund check: pre-existing release:<ord2>:shop:shop-1 in coin_transactions ->
		// ReleasePromotionShop MUST NOT double-credit AND MUST report CoinsReturned == 0!
		ord2 := "r5-1pg-ord-rel-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord2, UserId: buyerID, CoinsToUse: 4000,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("ReservePromotion(ord2): %v", err)
		}
		// Balance is now 6,000. Pre-insert release:<ord2>:shop:shop-1 into coin_transactions.
		if _, err := pool.Exec(ctx, `
			INSERT INTO coin_transactions (id, user_id, delta, reason, reference_id)
			VALUES ($1, $2, 1, 'legacy_release_collision', $3)
		`, uuid.NewString(), "other-user-"+suffix, "release:"+ord2+":shop:shop-1"); err != nil {
			t.Fatalf("seed conflicting release ref: %v", err)
		}
		relShopResp, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{
			OrderId: ord2, ShopId: "shop-1",
		})
		if err != nil {
			t.Fatalf("ReleasePromotionShop(ord2): %v", err)
		}
		if relShopResp.GetCoinsReturned() != 0 {
			t.Fatalf("P0-1 refund bug: duplicate release ref must report CoinsReturned=0, got %d", relShopResp.GetCoinsReturned())
		}
		bal2, _, _ := pgStore.GetCoinBalance(ctx, buyerID, 10)
		if bal2 != 6000 {
			t.Fatalf("P0-1 refund bug: duplicate release ref must not credit balance, want 6000 got %d", bal2)
		}
	})
}

// TestService_W3A_R5_Item2_SyncUserUsageDeletesAbsentRowAndStrictSelfHealsStaleHighRedis (P1-2):
// (a) Brand-new buyer (no promotion_user_usage row) fails CommitReservation + promo:res evicted ->
//
//	reconcileTargetsBestEffort (skipLocked=true) MUST delete stale Redis :user: key.
//
// (b) Strict buildCalcInput with stale high Redis :user: (PG=0, Redis=1, limit=1) across voucher, flash,
//
//	and shop program self-heals once under lock and lets ReservePromotion succeed.
func TestService_W3A_R5_Item2_SyncUserUsageDeletesAbsentRowAndStrictSelfHealsStaleHighRedis(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "shop-r5-2-" + suffix
		skuF := "sku-r5-2f-" + suffix
		skuP := "sku-r5-2p-" + suffix
		adminCtx := ctxWithRole("admin-r5-2", "admin", "")
		sellerCtx := ctxWithRole("seller-r5-2", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: skuP, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 150000, MinPrice_30DVnd: 150000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(hook, eng, logger.New("r5-item2", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R5I2V" + suffix, Name: "R5 Item2 V", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 10, PerUserLimit: 1,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R5I2FS " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fi, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fi.Item.Id

		sp, err := svc.CreateShopDiscountProgram(sellerCtx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R5I2SP " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: skuP, DiscountedPriceVnd: 110000, PerUserLimit: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pid := sp.Program.Items[0].Id

		// (a) Brand-new buyer (NO row in promotion_user_usage!) hits ErrInsufficientCoins on CommitReservation
		// while promo:res is evicted -> reconcileTargetsBestEffort (skipLocked=true) MUST DEL Redis :user: key!
		buyerA := "r5-2-buyerA-" + suffix
		ordFail := "r5-2-ord-fail-" + suffix
		// Give buyer 1000 coins so buildCalcInput passes, then spend them right before CommitReservation.
		if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
			UserId: buyerA, Amount: 1000, Reason: "seed", ReferenceId: "r5-2-seed-" + suffix,
		}); err != nil {
			t.Fatal(err)
		}
		hook.mu.Lock()
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.OrderID != ordFail {
				return
			}
			// Evict promo:res so compensateRedisRelease is a no-op!
			mr.Del(lua.ReservationKey(ordFail))
			// Drain buyerA's coins via another reservation so ordFail hits ErrInsufficientCoins!
			_, _, _ = base.CommitReservation(context.Background(), domain.PromotionReservation{
				OrderID:   "r5-2-coin-drain-" + suffix,
				UserID:    buyerA,
				CoinsUsed: 1000,
			}, now)
		}
		hook.mu.Unlock()

		_, err = svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordFail, UserId: buyerA, PlatformVoucherId: vid, CoinsToUse: 1000,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 100000}},
		})
		hook.mu.Lock()
		hook.onBeforeCommit = nil
		hook.mu.Unlock()
		if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "INSUFFICIENT_COINS") {
			t.Fatalf("expected INSUFFICIENT_COINS, got %v", err)
		}
		if val, exists, _ := eng.GetInt64(context.Background(), lua.VoucherUserKey(vid, buyerA)); exists {
			t.Fatalf("P1-2 bug: stale Redis :user: key=%d left behind when PG row does not exist!", val)
		}
		// Buyer A can now reserve normally!
		if resp, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r5-2-ord-ok-" + suffix, UserId: buyerA, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 100000}},
		}); err != nil || !resp.GetReserved() {
			t.Fatalf("buyerA locked out after failed commit: resp=%+v err=%v", resp, err)
		}

		// (b) Under D12, stale :user: keys written with 120s TTL auto-expire cleanly without in-flight healing,
		// so buyerB is blocked before expiry (max(Redis, PG)) and unblocked across all 3 kinds after 120s TTL!
		buyerB := "r5-2-buyerB-" + suffix
		if err := eng.ReconcileUserUsageKeys(context.Background(), []domain.UserUsage{
			{Kind: domain.UsageKindVoucher, TargetID: vid, UserID: buyerB, UsedQty: 1},
			{Kind: domain.UsageKindFlash, TargetID: fid, UserID: buyerB, UsedQty: 1},
			{Kind: domain.UsageKindShopProgram, TargetID: pid, UserID: buyerB, UsedQty: 1},
		}); err != nil {
			t.Fatal(err)
		}
		mr.FastForward(121 * time.Second)

		respB, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r5-2-ord-heal-" + suffix, UserId: buyerB, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId:      shopID,
				SubtotalVnd: 350000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid},
					{SkuId: skuP, Quantity: 1, UnitPriceVnd: 150000, ShopDiscountProgramItemId: pid},
				},
			}},
		})
		if err != nil || !respB.GetReserved() {
			t.Fatalf("D12 TTL expiry failed: stale Redis locked out buyerB: resp=%+v err=%v", respB, err)
		}
		if respB.GetBreakdown().GetTotalPlatformVoucherDiscountVnd() != 10000 ||
			respB.GetBreakdown().GetTotalFlashSaleDiscountVnd() != 80000 ||
			respB.GetBreakdown().GetTotalShopProgramDiscountVnd() != 40000 {
			t.Fatalf("D12 TTL expiry did not apply all 3 discounts: %+v", respB.GetBreakdown())
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R5_Item3_AlreadyCommittedSameUserReconcilesRedis (P1-3):
// Two same-order same-user ReservePromotion calls pass early GetReservation check; after Req1 commits and
// promo:res is evicted, Req2 decrements Redis a second time and hits alreadyCommitted=true -> MUST reconcile Redis!
func TestService_W3A_R5_Item3_AlreadyCommittedSameUserReconcilesRedis(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		buyerID := "r5-3-buyer-" + suffix
		ordID := "r5-3-ord-" + suffix
		adminCtx := ctxWithRole("admin-r5-3", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r5-item3", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R5I3V" + suffix, Name: "R5 Item3", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 10, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		// Request 1 commits normally (PG used=1 -> rem=9, user=1; Redis rem=9, user=1).
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordID, UserId: buyerID, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatalf("req1 ReservePromotion: %v", err)
		}

		// Evict promo:res:<ordID> and bypass early GetReservation check to simulate concurrent Request 2.
		mr.Del(lua.ReservationKey(ordID))
		var skippedOnce atomic.Bool
		hook.mu.Lock()
		hook.onGetReservation = func(_ context.Context, id string) (*domain.PromotionReservation, error, bool) {
			if id == ordID && skippedOnce.CompareAndSwap(false, true) {
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		hook.mu.Unlock()

		resp2, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordID, UserId: buyerID, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if err != nil || !resp2.GetReserved() || !resp2.GetAlreadyReserved() {
			t.Fatalf("req2 ReservePromotion: want AlreadyReserved=true, got resp=%+v err=%v", resp2, err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 9 {
			t.Fatalf("P1-3 bug: Redis voucher remaining leaked to %d after same-user alreadyCommitted (want 9)", rem)
		}
		if uUsed, _, _ := eng.GetInt64(context.Background(), lua.VoucherUserKey(vid, buyerID)); uUsed != 1 {
			t.Fatalf("P1-3 bug: Redis :user: leaked to %d after same-user alreadyCommitted (want 1)", uUsed)
		}
		if eng.IsReservationReleased(context.Background(), ordID) {
			t.Fatalf("P1-3 bug: promo:res:%s must NOT be tombstoned RELEASED for active order", ordID)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R5_Item4_NotInitializedRehydrationSerializesAndFailsClosed (P1-4):
// (a) Concurrent requests right after Redis flush serialize on skipLocked=false so NONE fail FLASH_NOT_INITIALIZED.
// (b) When rehydration fails, ReservePromotion returns codes.Unavailable and does NOT retry Lua on empty Redis.
func TestService_W3A_R5_Item4_NotInitializedRehydrationSerializesAndFailsClosed(t *testing.T) {
	t.Run("ConcurrentFlushRehydration_PostgresStore", func(t *testing.T) {
		base := openR2PostgresStore(t)
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{
			Addr:         mr.Addr(),
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			PoolTimeout:  30 * time.Second,
		})
		t.Cleanup(func() { _ = rdb.Close() })
		rHook := &selectiveRedisHook{}
		rdb.AddHook(rHook)
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "shop-r5-4-" + suffix
		skuID := "sku-r5-4-" + suffix
		adminCtx := ctxWithRole("admin-r5-4", "admin", "")
		sellerCtx := ctxWithRole("seller-r5-4", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuID, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(base, eng, logger.New("r5-item4-conc", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R5I4FS " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fi, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, ShopId: shopID, SkuId: skuID, FlashPriceVnd: 120000, TotalSlots: 5, PerUserLimit: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fi.Item.Id

		// Wipe Redis completely, and stall the first SET (inside Req1's FOR UPDATE) so Req2 hits PG lock concurrently!
		mr.FlushAll()
		firstSetEntered := make(chan struct{})
		releaseFirstSet := make(chan struct{})
		var triggered atomic.Bool
		rHook.mu.Lock()
		rHook.onSetKey = func(ctx context.Context, key string) {
			if key == lua.FlashRemainingKey(fid) && triggered.CompareAndSwap(false, true) {
				close(firstSetEntered)
				select {
				case <-releaseFirstSet:
				case <-ctx.Done():
				}
			}
		}
		rHook.mu.Unlock()

		var wg sync.WaitGroup
		err1Ch := make(chan error, 1)
		err2Ch := make(chan error, 1)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, rErr := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: "r5-4-o1-" + suffix, UserId: "r5-4-u1-" + suffix,
				ShopItems: []*promotionv1.ShopDiscountInput{{
					ShopId: shopID,
					Items:  []*promotionv1.DiscountLineItem{{SkuId: skuID, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
				}},
			})
			err1Ch <- rErr
		}()

		<-firstSetEntered
		go func() {
			defer wg.Done()
			_, rErr := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: "r5-4-o2-" + suffix, UserId: "r5-4-u2-" + suffix,
				ShopItems: []*promotionv1.ShopDiscountInput{{
					ShopId: shopID,
					Items:  []*promotionv1.DiscountLineItem{{SkuId: skuID, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
				}},
			})
			err2Ch <- rErr
		}()

		// Give Req2 time to hit SyncFlashItemRedis while Req1 holds FOR UPDATE, then release Req1's SET.
		time.Sleep(80 * time.Millisecond)
		close(releaseFirstSet)
		wg.Wait()

		if err1 := <-err1Ch; err1 != nil {
			t.Fatalf("req1 failed: %v", err1)
		}
		if err2 := <-err2Ch; err2 != nil {
			t.Fatalf("P1-4 bug: concurrent req2 after Redis flush failed (skipLocked=true skipped rehydration): %v", err2)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); rem != 3 {
			t.Fatalf("expected flash remaining=3 after 2 reserves, got %d", rem)
		}
	})

	t.Run("RehydrateErrorReturnsUnavailableWithoutRetry", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		rHook := &selectiveRedisHook{}
		rdb.AddHook(rHook)
		eng := lua.NewEngine(rdb)
		mem := store.NewMemoryStore()

		now := time.Now().UTC()
		adminCtx := ctxWithRole("admin-r5-4b", "admin", "")
		orderSvcCtx := ctxWithService("order-service")
		svc := service.NewPromotionService(mem, eng, logger.New("r5-item4-err", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R5I4ERR", Name: "R5 Item4 Err", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 5, PerUserLimit: 2,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}

		// Flush Redis and make pipeline writes (ReconcileVoucherKeys) fail while Lua EVAL still runs.
		mr.FlushAll()
		rHook.mu.Lock()
		rHook.failPipeline = errors.New("connection refused")
		rHook.mu.Unlock()
		rHook.evalCalls.Store(0)
		rHook.pipeCalls.Store(0)

		_, err = svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r5-4b-ord", UserId: "r5-4b-u1", PlatformVoucherId: vr.Voucher.Id,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if status.Code(err) != codes.Unavailable {
			t.Fatalf("P1-4 bug: rehydrate failure must return codes.Unavailable, got %v", err)
		}
		// Lua Reserve should have run ONCE (initial VOUCHER_NOT_INITIALIZED), NOT a second time on empty Redis.
		// Note: first EVALSHA may fall back to EVAL once on cold script cache, so check <= 2 (never 3+ or 2nd Reserve).
		// To be exact, pre-warm script cache or check pipeCalls == 1.
		if got := rHook.pipeCalls.Load(); got != 1 {
			t.Fatalf("expected 1 failed rehydrate pipeline call (early-abort), got %d", got)
		}
	})
}

// TestService_W3A_R5_Item5_AmbiguousCommitErrorChecksStoreBeforeCompensating (P1-5):
// (a) PG actually committed RESERVED, CommitReservation returns timeout/EOF -> returns AlreadyReserved=true,
//
//	does NOT compensate Redis or write RELEASED tombstone.
//
// (b) PG did not commit (ErrNotFound) -> compensates Redis & returns Internal.
// (c) GetReservation fails (DB unreachable) -> does NOT compensate Redis, returns codes.Unavailable so
//
//	order-service calls idempotent ReleasePromotion (verified afterward).
func TestService_W3A_R5_Item5_AmbiguousCommitErrorChecksStoreBeforeCompensating(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r5-5", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r5-item5", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R5I5V" + suffix, Name: "R5 Item5", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 10, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		// (a) PG committed RESERVED, then tx.Commit returned ambiguous context.DeadlineExceeded!
		ordA := "r5-5a-" + suffix
		buyerA := "r5-5-uA-" + suffix
		hook.mu.Lock()
		hook.onCommit = func(ctx context.Context, res domain.PromotionReservation, ts time.Time) (*domain.PromotionReservation, bool, error, bool) {
			if res.OrderID != ordA {
				return nil, false, nil, false
			}
			_, _, cErr := base.CommitReservation(context.Background(), res, ts)
			if cErr != nil {
				return nil, false, cErr, true
			}
			return nil, false, fmt.Errorf("commit reservation tx: %w", context.DeadlineExceeded), true
		}
		hook.mu.Unlock()

		respA, errA := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordA, UserId: buyerA, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if errA != nil || !respA.GetReserved() || !respA.GetAlreadyReserved() || respA.GetBreakdown().GetTotalPlatformVoucherDiscountVnd() != 10000 {
			t.Fatalf("P1-5a bug: committed order on ambiguous commit error must return AlreadyReserved=true with breakdown, got resp=%+v err=%v", respA, errA)
		}
		if eng.IsReservationReleased(context.Background(), ordA) {
			t.Fatalf("P1-5a bug: promo:res:%s was overwritten with RELEASED despite PG RESERVED!", ordA)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 9 {
			t.Fatalf("P1-5a bug: Redis voucher rem must stay 9, got %d", rem)
		}

		// (b) PG did NOT commit (ErrNotFound on verification) -> compensate Redis & return Internal.
		ordB := "r5-5b-" + suffix
		buyerB := "r5-5-uB-" + suffix
		hook.mu.Lock()
		hook.onCommit = func(_ context.Context, res domain.PromotionReservation, _ time.Time) (*domain.PromotionReservation, bool, error, bool) {
			if res.OrderID != ordB {
				return nil, false, nil, false
			}
			return nil, false, errors.New("connection reset by peer"), true
		}
		hook.mu.Unlock()

		if _, errB := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordB, UserId: buyerB, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); status.Code(errB) != codes.Internal {
			t.Fatalf("P1-5b: expected Internal when confirmed ErrNotFound, got %v", errB)
		}
		if !eng.IsReservationReleased(context.Background(), ordB) {
			t.Fatalf("P1-5b: expected promo:res:%s compensated to RELEASED", ordB)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 9 {
			t.Fatalf("P1-5b: expected Redis rem restored to 9, got %d", rem)
		}

		// (c) GetReservation fails (DB unreachable) -> DO NOT compensate Redis, return Unavailable;
		//     then subsequent ReleasePromotion releases cleanly via promo:res:<order_id>!
		ordC := "r5-5c-" + suffix
		buyerC := "r5-5-uC-" + suffix
		var getCalls atomic.Int32
		hook.mu.Lock()
		hook.onCommit = func(_ context.Context, res domain.PromotionReservation, _ time.Time) (*domain.PromotionReservation, bool, error, bool) {
			if res.OrderID != ordC {
				return nil, false, nil, false
			}
			return nil, false, errors.New("EOF"), true
		}
		hook.onGetReservation = func(_ context.Context, id string) (*domain.PromotionReservation, error, bool) {
			if id != ordC {
				return nil, nil, false
			}
			// 1st call is early idempotency check (ErrNotFound); 2nd call is post-commitErr check (DB unreachable).
			if getCalls.Add(1) == 1 {
				return nil, store.ErrNotFound, true
			}
			return nil, errors.New("database unreachable"), true
		}
		hook.mu.Unlock()

		if _, errC := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordC, UserId: buyerC, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); status.Code(errC) != codes.Unavailable {
			t.Fatalf("P1-5c bug: DB read failure during commit verification must return codes.Unavailable, got %v", errC)
		}
		if eng.IsReservationReleased(context.Background(), ordC) {
			t.Fatalf("P1-5c bug: must NOT compensate/tombstone promo:res:%s when DB check failed", ordC)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 8 {
			t.Fatalf("P1-5c: Redis should still hold ordC reservation (rem=8) pending order ReleasePromotion, got %d", rem)
		}

		// Clear hooks (DB recovered) and simulate order-service calling ReleasePromotion(ordC)!
		hook.mu.Lock()
		hook.onCommit = nil
		hook.onGetReservation = nil
		hook.mu.Unlock()
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ordC}); err != nil {
			t.Fatalf("order-service compensation ReleasePromotion(ordC): %v", err)
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); rem != 9 {
			t.Fatalf("expected Redis rem restored to 9 after ReleasePromotion(ordC), got %d", rem)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R5_Item6_IsReservationReleasedExactAndUndoAlreadyReleasedReconciles (P1-6a & P1-6b):
// (6a) IsReservationReleased returns false on absent key; concurrent retry during UndoReserve+continue
//
//	waits and succeeds with AlreadyReserved=true instead of returning codes.Aborted.
//
// (6b) When promo:res is evicted before UndoReserve (AlreadyReleased=true), ReservePromotion runs
//
//	locked reconcileTargets(skipLocked=false); if reconcile fails it stops (Unavailable), and if it
//	succeeds flash slots and :user: are NOT double-deducted on attempt 1.
func TestService_W3A_R5_Item6_IsReservationReleasedExactAndUndoAlreadyReleasedReconciles(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{
			Addr:         mr.Addr(),
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			PoolTimeout:  30 * time.Second,
		})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: base}

		// 6a unit assertion: absent key MUST NOT report released.
		if eng.IsReservationReleased(context.Background(), "never-existed-order") {
			t.Fatalf("P1-6a bug: IsReservationReleased(absent) returned true, want false")
		}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "shop-r5-6-" + suffix
		skuF := "sku-r5-6f-" + suffix
		skuP := "sku-r5-6p-" + suffix
		adminCtx := ctxWithRole("admin-r5-6", "admin", "")
		sellerCtx := ctxWithRole("seller-r5-6", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: skuP, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 150000, MinPrice_30DVnd: 150000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(hook, eng, logger.New("r5-item6", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		fs, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R5I6FS " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fi, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 3,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fi.Item.Id

		sp, err := svc.CreateShopDiscountProgram(sellerCtx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R5I6SP " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: skuP, DiscountedPriceVnd: 100000, PerUserLimit: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pid := sp.Program.Items[0].Id

		// 6a concurrency test (deterministic hook sync, zero time.Sleep per P2-8):
		// ReqA enters attempt 0 -> promo:res is active -> ReqB deterministically observes ALREADY_RESERVED
		// and enters waitForCommittedReservation -> ReqA hits ErrShopProgramUserLimitExceeded, runs UndoReserve
		// (DEL promo:res) -> ReqB deterministically observes GET promo:res == redis.Nil (IsReservationReleased=false)
		// -> ReqA proceeds to attempt 1 & commits -> ReqB MUST return AlreadyReserved=true (NOT Aborted!).
		rHook6a := &selectiveRedisHook{}
		rdb.AddHook(rHook6a)
		buyer6a := "r5-6a-buyer-" + suffix
		ord6a := "r5-6a-ord-" + suffix
		resKey6a := lua.ReservationKey(ord6a)
		reqACommit0 := make(chan struct{})
		reqBSawAlreadyRes := make(chan struct{})
		reqBSawNilDuringUndo := make(chan struct{})
		var onceAlreadyRes, onceNilDuringUndo sync.Once
		var watchNil atomic.Bool
		var attemptCount atomic.Int32

		rHook6a.mu.Lock()
		rHook6a.onAfterCmd = func(_ context.Context, cmd redis.Cmder, cmdErr error) {
			name := strings.ToLower(cmd.Name())
			if name == "evalsha" || name == "eval" {
				if c, ok := cmd.(*redis.Cmd); ok {
					if arr, ok := c.Val().([]any); ok && len(arr) > 0 {
						switch fmt.Sprint(arr[0]) {
						case "ALREADY_RESERVED":
							onceAlreadyRes.Do(func() { close(reqBSawAlreadyRes) })
						case "RELEASED":
							if len(cmd.Args()) > 3 && fmt.Sprint(cmd.Args()[3]) == resKey6a {
								watchNil.Store(true)
								select {
								case <-reqBSawNilDuringUndo:
								case <-time.After(5 * time.Second):
								}
							}
						}
					}
				}
			} else if name == "get" && watchNil.Load() && errors.Is(cmdErr, redis.Nil) && len(cmd.Args()) > 1 && fmt.Sprint(cmd.Args()[1]) == resKey6a {
				onceNilDuringUndo.Do(func() { close(reqBSawNilDuringUndo) })
			}
		}
		rHook6a.mu.Unlock()

		hook.mu.Lock()
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.OrderID != ord6a {
				return
			}
			if attemptCount.Add(1) == 1 {
				// Let ReqB start while promo:res:ord6a is active and wait until ReqB hits ALREADY_RESERVED!
				close(reqACommit0)
				select {
				case <-reqBSawAlreadyRes:
				case <-time.After(5 * time.Second):
				}
				// Exhaust shop program in PG so ReqA attempt 0 gets ErrShopProgramUserLimitExceeded!
				_, _, _ = base.CommitReservation(context.Background(), domain.PromotionReservation{
					OrderID: "r5-6a-comp-" + suffix, UserID: buyer6a,
					ShopProgramItems: []domain.ReservedShopProgramItem{{
						ShopDiscountProgramItemID: pid, SKUID: skuP, Quantity: 1, ShopID: shopID,
					}},
				}, now)
			}
		}
		hook.mu.Unlock()

		var wg sync.WaitGroup
		var respA, respB *promotionv1.ReservePromotionResponse
		var errA, errB error
		req6a := &promotionv1.ReservePromotionRequest{
			OrderId: ord6a, UserId: buyer6a,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuP, Quantity: 1, UnitPriceVnd: 150000, ShopDiscountProgramItemId: pid}},
			}},
		}
		wg.Add(2)
		go func() {
			defer wg.Done()
			respA, errA = svc.ReservePromotion(orderSvcCtx, req6a)
		}()
		<-reqACommit0
		go func() {
			defer wg.Done()
			respB, errB = svc.ReservePromotion(orderSvcCtx, req6a)
		}()
		wg.Wait()
		hook.mu.Lock()
		hook.onBeforeCommit = nil
		hook.mu.Unlock()
		rHook6a.mu.Lock()
		rHook6a.onAfterCmd = nil
		rHook6a.mu.Unlock()

		select {
		case <-reqBSawAlreadyRes:
		default:
			t.Fatalf("P2-8 sync error: ReqB never observed ALREADY_RESERVED")
		}
		select {
		case <-reqBSawNilDuringUndo:
		default:
			t.Fatalf("P2-8 sync error: ReqB never polled IsReservationReleased during deleted promo:res window")
		}
		if errA != nil || !respA.GetReserved() {
			t.Fatalf("6a ReqA failed: resp=%+v err=%v", respA, errA)
		}
		if errB != nil || !respB.GetReserved() || !respB.GetAlreadyReserved() {
			t.Fatalf("P1-6a bug: concurrent ReqB failed during UndoReserve+continue: resp=%+v err=%v", respB, errB)
		}

		// 6b-1: Evict promo:res before UndoReserve (AlreadyReleased=true) -> MUST reconcile skipLocked=false
		// so flash slot (10->9) and flash :user: (0->1) are NOT double-deducted!
		buyer6b := "r5-6b-buyer-" + suffix
		ord6b := "r5-6b-ord-" + suffix
		var sawFlashSyncWait atomic.Bool
		var att6b atomic.Int32
		hook.mu.Lock()
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.OrderID != ord6b || att6b.Add(1) != 1 {
				return
			}
			mr.Del(lua.ReservationKey(ord6b))
			_, _, _ = base.CommitReservation(context.Background(), domain.PromotionReservation{
				OrderID: "r5-6b-comp-" + suffix, UserID: buyer6b,
				ShopProgramItems: []domain.ReservedShopProgramItem{{
					ShopDiscountProgramItemID: pid, SKUID: skuP, Quantity: 1, ShopID: shopID,
				}},
			}, now)
		}
		hook.onSyncFlashItemRedis = func(itemID string, skipLocked bool) {
			if itemID == fid && !skipLocked {
				sawFlashSyncWait.Store(true)
			}
		}
		hook.mu.Unlock()

		resp6b, err6b := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord6b, UserId: buyer6b,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid},
					{SkuId: skuP, Quantity: 1, UnitPriceVnd: 150000, ShopDiscountProgramItemId: pid},
				},
			}},
		})
		hook.mu.Lock()
		hook.onBeforeCommit = nil
		hook.onSyncFlashItemRedis = nil
		hook.mu.Unlock()

		if err6b != nil || !resp6b.GetReserved() {
			t.Fatalf("6b-1 ReservePromotion failed: resp=%+v err=%v", resp6b, err6b)
		}
		if !sawFlashSyncWait.Load() {
			t.Fatalf("P1-6b bug: UndoReserve AlreadyReleased did not call reconcileTargets with skipLocked=false")
		}
		if rem, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); rem != 9 {
			t.Fatalf("P1-6b bug: flash remaining double-deducted: want 9, got %d", rem)
		}
		if uUsed, _, _ := eng.GetInt64(context.Background(), lua.FlashUserKey(fid, buyer6b)); uUsed != 1 {
			t.Fatalf("P1-6b bug: flash :user: double-incremented: want 1, got %d", uUsed)
		}

		// 6b-2: When UndoReserve returns AlreadyReleased=true AND reconcileTargets errors -> MUST STOP (Unavailable), NOT continue!
		buyer6c := "r5-6c-buyer-" + suffix
		ord6c := "r5-6c-ord-" + suffix
		hook.commitCalls.Store(0)
		var att6c atomic.Int32
		hook.mu.Lock()
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.OrderID != ord6c || att6c.Add(1) != 1 {
				return
			}
			mr.Del(lua.ReservationKey(ord6c))
			_, _, _ = base.CommitReservation(context.Background(), domain.PromotionReservation{
				OrderID: "r5-6c-comp-" + suffix, UserID: buyer6c,
				ShopProgramItems: []domain.ReservedShopProgramItem{{
					ShopDiscountProgramItemID: pid, SKUID: skuP, Quantity: 1, ShopID: shopID,
				}},
			}, now)
		}
		hook.onSyncUserUsageRedis = func(_, _, _ string, skipLocked bool) error {
			if !skipLocked {
				return errors.New("forced reconcile failure")
			}
			return nil
		}
		hook.mu.Unlock()

		_, err6c := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord6c, UserId: buyer6c,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid},
					{SkuId: skuP, Quantity: 1, UnitPriceVnd: 150000, ShopDiscountProgramItemId: pid},
				},
			}},
		})
		hook.mu.Lock()
		hook.onBeforeCommit = nil
		hook.onSyncUserUsageRedis = nil
		hook.mu.Unlock()

		if status.Code(err6c) != codes.Unavailable {
			t.Fatalf("P1-6b bug: reconcile failure after UndoReserve AlreadyReleased must return Unavailable and stop, got %v", err6c)
		}
		if got := hook.commitCalls.Load(); got != 1 {
			t.Fatalf("P1-6b bug: must NOT continue to attempt 1 after failed reconcile (commitCalls=%d, want 1)", got)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R5_Item7_ReconcileEarlyBreak_PoolSemaphore_AndReadPathNoLock (P1-7):
// (7a) reconcileTargets aborts immediately on first Redis conn/timeout error (does not loop all 8 targets).
// (7b) Small pgxpool (MaxConns=6) + 12 concurrent ReleasePromotion with 450ms slow Redis -> pg.Ping responds promptly.
// (7c) PreviewDiscounts and ListAvailableVouchers never call locked SyncVoucherRedis on PAUSED/DELETED vouchers.
func TestService_W3A_R5_Item7_ReconcileEarlyBreak_PoolSemaphore_AndReadPathNoLock(t *testing.T) {
	t.Run("7a_ReconcileTargetsEarlyBreakOnRedisTimeout", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		rHook := &selectiveRedisHook{}
		rdb.AddHook(rHook)
		eng := lua.NewEngine(rdb)
		mem := store.NewMemoryStore()

		now := time.Now().UTC()
		adminCtx := ctxWithRole("admin-r5-7a", "admin", "")
		s1Ctx := ctxWithRole("s1-r5-7a", "seller", "shop-1")
		s2Ctx := ctxWithRole("s2-r5-7a", "seller", "shop-2")
		orderSvcCtx := ctxWithService("order-service")

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: "sku-7a", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(mem, eng, logger.New("r5-7a", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		vs1, _ := svc.CreateVoucher(s1Ctx, &promotionv1.CreateVoucherRequest{
			Code: "R57AS1", Name: "S1", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-1",
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 10, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		vs2, _ := svc.CreateVoucher(s2Ctx, &promotionv1.CreateVoucherRequest{
			Code: "R57AS2", Name: "S2", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: "shop-2",
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 10, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		vp, _ := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R57AP", Name: "P", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 10, PerUserLimit: 5,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		fs, _ := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "7aFS", StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		fi, _ := svc.RegisterFlashSaleItem(s1Ctx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fs.FlashSale.Id, ShopId: "shop-1", SkuId: "sku-7a", FlashPriceVnd: 150000, TotalSlots: 10, PerUserLimit: 5,
		})
		_, _ = svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fi.Item.Id, Approve: true})

		// Reserve 1 multi-shop order with 3 vouchers + 1 flash item + 4 user usages = 8 reconcile targets!
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r5-7a-ord", UserId: "r5-7a-u1",
			PlatformVoucherId: vp.Voucher.Id,
			ShopVoucherIds:    map[string]string{"shop-1": vs1.Voucher.Id, "shop-2": vs2.Voucher.Id},
			ShopItems: []*promotionv1.ShopDiscountInput{
				{ShopId: "shop-1", SubtotalVnd: 200000, Items: []*promotionv1.DiscountLineItem{{SkuId: "sku-7a", Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fi.Item.Id}}},
				{ShopId: "shop-2", SubtotalVnd: 100000},
			},
		}); err != nil {
			t.Fatalf("ReservePromotion: %v", err)
		}

		// Stall Redis pipelines for 600ms (> 500ms lockedRedisTimeout) and count pipeline calls during ReleasePromotion!
		rHook.mu.Lock()
		rHook.delayPipe = 600 * time.Millisecond
		rHook.mu.Unlock()
		rHook.pipeCalls.Store(0)

		start := time.Now()
		_, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: "r5-7a-ord"})
		elapsed := time.Since(start)
		if err == nil {
			t.Fatalf("expected ReleasePromotion error when Redis timed out")
		}
		if got := rHook.pipeCalls.Load(); got != 1 {
			t.Fatalf("P1-7a bug: reconcileTargets must early-break after 1 timed-out target, got %d pipeline calls (elapsed=%v)", got, elapsed)
		}
		if elapsed > 1500*time.Millisecond {
			t.Fatalf("P1-7a bug: ReleasePromotion took too long (%v) instead of aborting after 1×500ms timeout", elapsed)
		}
	})

	t.Run("7b_SmallPGPoolWith450msSlowRedisKeepsPingResponsive_PostgresStore", func(t *testing.T) {
		dsn := os.Getenv("TEST_PROMOTION_DB_URL")
		if dsn == "" {
			t.Skip("TEST_PROMOTION_DB_URL not set")
		}
		ctx := context.Background()
		cfg, err := pgxpool.ParseConfig(dsn)
		if err != nil {
			t.Fatalf("ParseConfig: %v", err)
		}
		cfg.MaxConns = 6
		cfg.MinConns = 1
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("NewWithConfig: %v", err)
		}
		t.Cleanup(pool.Close)
		pgStore := store.NewPostgresStore(pool)
		if err := pgStore.Migrate(ctx); err != nil {
			t.Fatalf("Migrate: %v", err)
		}

		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{
			Addr:         mr.Addr(),
			PoolSize:     64,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			PoolTimeout:  30 * time.Second,
		})
		t.Cleanup(func() { _ = rdb.Close() })
		rHook := &selectiveRedisHook{}
		rdb.AddHook(rHook)
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r5-7b", "admin", "")
		orderSvcCtx := ctxWithService("order-service")
		svc := service.NewPromotionService(pgStore, eng, logger.New("r5-7b", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		// 12 orders on distinct vouchers with MaxConns=6 and 450ms slow Redis:
		// Without the reconcile channel semaphore, all 6 connections enter SyncVoucherRedis and block 450ms,
		// starving pg.Ping(200ms). With semaphore cap=4, at most 4 connections enter reconcile, leaving >=2 free.
		const nOrders = 12
		orderIDs := make([]string, nOrders)
		for i := 0; i < nOrders; i++ {
			vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
				Code: fmt.Sprintf("R57B%s%02d", suffix, i), Name: "7b", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 5, PerUserLimit: 5,
				StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			oid := fmt.Sprintf("r5-7b-ord-%s-%02d", suffix, i)
			orderIDs[i] = oid
			if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: oid, UserId: fmt.Sprintf("r5-7b-u-%s-%02d", suffix, i), PlatformVoucherId: vr.Voucher.Id,
				ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
			}); err != nil {
				t.Fatalf("reserve %d: %v", i, err)
			}
		}

		// Make Redis pipelines slow (450ms < 500ms timeout) and launch 12 concurrent ReleasePromotion calls on MaxConns=6!
		rHook.mu.Lock()
		rHook.delayPipe = 450 * time.Millisecond
		rHook.mu.Unlock()
		rHook.pipeCalls.Store(0)
		rHook.maxInFlight.Store(0)

		var wg sync.WaitGroup
		startCh := make(chan struct{})
		for _, oid := range orderIDs {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				<-startCh
				_, _ = svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: id})
			}(oid)
		}
		close(startCh)

		// Wait until reconcile pipelines saturate the semaphore (4 in-flight; without semaphore all 6 saturate!).
		deadline := time.Now().Add(2 * time.Second)
		for rHook.inFlightPipe.Load() < 4 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(40 * time.Millisecond)

		pingCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		pingErr := pool.Ping(pingCtx)
		cancel()
		stat := pool.Stat()

		wg.Wait()

		if pingErr != nil {
			t.Fatalf("P1-7b bug: pg.Ping failed under slow-Redis reconcile load on MaxConns=6 (acquired=%d total=%d maxInFlight=%d): %v",
				stat.AcquiredConns(), stat.TotalConns(), rHook.maxInFlight.Load(), pingErr)
		}
		if got := rHook.maxInFlight.Load(); got > 4 {
			t.Fatalf("P1-7b bug: semaphore failed to cap concurrent Redis syncs <= 4, saw maxInFlight=%d", got)
		}
	})

	t.Run("7c_ReadPathsNeverCallLockedSyncVoucherRedis", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r5HookStore{Store: store.NewMemoryStore()}

		now := time.Now().UTC()
		adminCtx := ctxWithRole("admin-r5-7c", "admin", "")
		buyerCtx := ctxWithRole("buyer-r5-7c", "buyer", "")
		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: "sku-7c", ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(hook, eng, logger.New("r5-7c", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R57CV", Name: "7c", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000, Quota: 10, PerUserLimit: 2,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{
			VoucherId: vr.Voucher.Id, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
		}); err != nil {
			t.Fatal(err)
		}

		hook.syncVoucherCalls.Store(0)
		if _, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
			PlatformVoucherId: vr.Voucher.Id,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: "shop-1",
				Items:  []*promotionv1.DiscountLineItem{{SkuId: "sku-7c", Quantity: 1}},
			}},
		}); err != nil {
			t.Fatalf("PreviewDiscounts: %v", err)
		}
		if _, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{}); err != nil {
			t.Fatalf("ListAvailableVouchers: %v", err)
		}
		if got := hook.syncVoucherCalls.Load(); got != 0 {
			t.Fatalf("P1-7c bug: read paths (PreviewDiscounts/ListAvailableVouchers) called SyncVoucherRedis %d times (want 0)", got)
		}
	})
}

// TestService_W3A_R5_Item10_MinClampAndLastShopCoinSweep (P2-11 & P2-12):
// (10a) ListAvailableVouchers & PreviewDiscounts clamp remaining quota/budget to min(PG, Redis).
// (10b) Last shop released via ReleasePromotionShop sweeps all remaining unreturned coins (clamped [0, remaining]).
func TestService_W3A_R5_Item10_MinClampAndLastShopCoinSweep(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r5-10", "admin", "")
		buyerCtx := ctxWithRole("buyer-r5-10-"+suffix, "buyer", "")
		orderSvcCtx := ctxWithService("order-service")

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: "sku-10-" + suffix, ShopId: "shop-1", CategoryId: "cat-1", PriceVnd: 100000, MinPrice_30DVnd: 100000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(base, eng, logger.New("r5-item10", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		// 10a: Voucher quota=5, budget=100000; reserve 2 -> PG rem=3, PG remBudget=60000.
		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R510V" + suffix, Name: "R5 Item10", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 5, BudgetVnd: 100000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id
		for i := 0; i < 2; i++ {
			if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: fmt.Sprintf("r5-10-init-%s-%d", suffix, i), UserId: fmt.Sprintf("r5-10-u-%s-%d", suffix, i),
				PlatformVoucherId: vid,
				ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
			}); err != nil {
				t.Fatal(err)
			}
		}

		// Inflate Redis higher than PG (Scenario B): remaining=9, budget_remaining=200000!
		_ = mr.Set(lua.VoucherRemainingKey(vid), "9")
		_ = mr.Set(lua.VoucherBudgetRemainingKey(vid), "200000")

		listResp, err := svc.ListAvailableVouchers(buyerCtx, &promotionv1.ListAvailableVouchersRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var found *promotionv1.Voucher
		for _, v := range listResp.GetVouchers() {
			if v.GetId() == vid {
				found = v
				break
			}
		}
		if found == nil {
			t.Fatalf("expected voucher %s in ListAvailableVouchers", vid)
		}
		if found.GetRemainingQuota() != 3 || found.GetUsedCount() != 2 || found.GetUsedBudgetVnd() != 40000 {
			t.Fatalf("P2-11 bug: ListAvailableVouchers did not clamp min(PG, Redis): remQuota=%d usedCount=%d usedBudget=%d (want 3, 2, 40000)",
				found.GetRemainingQuota(), found.GetUsedCount(), found.GetUsedBudgetVnd())
		}

		// Exhaust remaining 3 in PG (PG rem=0, PG remBudget=0), keep Redis inflated at 9/200000 -> PreviewDiscounts must see 0!
		for i := 2; i < 5; i++ {
			if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: fmt.Sprintf("r5-10-init-%s-%d", suffix, i), UserId: fmt.Sprintf("r5-10-u-%s-%d", suffix, i),
				PlatformVoucherId: vid,
				ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
			}); err != nil {
				t.Fatal(err)
			}
		}
		_ = mr.Set(lua.VoucherRemainingKey(vid), "9")
		_ = mr.Set(lua.VoucherBudgetRemainingKey(vid), "200000")
		prevResp, err := svc.PreviewDiscounts(buyerCtx, &promotionv1.PreviewDiscountsRequest{
			PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: "shop-1",
				Items:  []*promotionv1.DiscountLineItem{{SkuId: "sku-10-" + suffix, Quantity: 1}},
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if prevResp.GetBreakdown().GetTotalPlatformVoucherDiscountVnd() != 0 {
			t.Fatalf("P2-11 bug: PreviewDiscounts trusted inflated Redis when PG rem=0: got discount=%d",
				prevResp.GetBreakdown().GetTotalPlatformVoucherDiscountVnd())
		}

		// 10b (P2-12): Last shop sweeps all remaining unreturned coins when sum(bd.Shops[].CoinsDiscountVnd) < res.CoinsUsed.
		buyerCoin := "r5-10-coin-" + suffix
		if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
			UserId: buyerCoin, Amount: 1000, Reason: "seed", ReferenceId: "r5-10-seed-" + suffix,
		}); err != nil {
			t.Fatal(err)
		}
		bdBytes, err := protojson.Marshal(&promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", CoinsDiscountVnd: 600},
				{ShopId: "shop-2", CoinsDiscountVnd: 300}, // 100 VND short of res.CoinsUsed=1000!
			},
			TotalCoinsDiscountVnd: 1000,
		})
		if err != nil {
			t.Fatal(err)
		}
		ordSweep := "r5-10-sweep-" + suffix
		if _, _, err := base.CommitReservation(context.Background(), domain.PromotionReservation{
			OrderID:       ordSweep,
			UserID:        buyerCoin,
			CoinsUsed:     1000,
			BreakdownJSON: bdBytes,
		}, now); err != nil {
			t.Fatalf("CommitReservation(sweep): %v", err)
		}

		r1, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: ordSweep, ShopId: "shop-1"})
		if err != nil || r1.GetCoinsReturned() != 600 {
			t.Fatalf("ReleasePromotionShop(shop-1): resp=%+v err=%v", r1, err)
		}
		r2, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: ordSweep, ShopId: "shop-2"})
		if err != nil {
			t.Fatalf("ReleasePromotionShop(shop-2): %v", err)
		}
		if r2.GetCoinsReturned() != 400 {
			t.Fatalf("P2-12 bug: last shop must sweep remaining 400 coins (not 300), got %d", r2.GetCoinsReturned())
		}
		bal, _, _ := base.GetCoinBalance(context.Background(), buyerCoin, 10)
		if bal != 1000 {
			t.Fatalf("P2-12 bug: buyer final balance want 1000, got %d", bal)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}
