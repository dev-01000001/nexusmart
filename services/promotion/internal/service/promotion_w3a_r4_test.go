package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
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

type r4HookStore struct {
	store.Store
	onBeforeCommit   func(res domain.PromotionReservation)
	onAfterRelease   func(orderID string)
	onGetReservation func(orderID string) (*domain.PromotionReservation, error, bool)
	onAfterUpdate    func(after *domain.Voucher)
	commitCalls      atomic.Int32
}

func (h *r4HookStore) CommitReservation(
	ctx context.Context,
	res domain.PromotionReservation,
	now time.Time,
) (*domain.PromotionReservation, bool, error) {
	h.commitCalls.Add(1)
	if h.onBeforeCommit != nil {
		h.onBeforeCommit(res)
	}
	return h.Store.CommitReservation(ctx, res, now)
}

func (h *r4HookStore) ReleaseReservation(ctx context.Context, orderID string) (*store.FullReleaseResult, error) {
	res, err := h.Store.ReleaseReservation(ctx, orderID)
	if err == nil && h.onAfterRelease != nil {
		h.onAfterRelease(orderID)
	}
	return res, err
}

func (h *r4HookStore) GetReservation(ctx context.Context, orderID string) (*domain.PromotionReservation, error) {
	if h.onGetReservation != nil {
		if r, err, handled := h.onGetReservation(orderID); handled {
			return r, err
		}
	}
	return h.Store.GetReservation(ctx, orderID)
}

func (h *r4HookStore) UpdateVoucher(
	ctx context.Context,
	id string,
	mutate func(cur *domain.Voucher, hasActiveReservation bool) error,
) (*domain.Voucher, error) {
	after, err := h.Store.UpdateVoucher(ctx, id, mutate)
	if err == nil && h.onAfterUpdate != nil {
		h.onAfterUpdate(after)
	}
	return after, err
}

type countingBlockRedisHook struct {
	mu            sync.RWMutex
	err           error
	blockDuration time.Duration
	pipelineCalls atomic.Int32
	cmdCalls      atomic.Int32
}

func (h *countingBlockRedisHook) SetError(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
}

func (h *countingBlockRedisHook) SetBlock(d time.Duration) {
	h.mu.Lock()
	h.blockDuration = d
	h.mu.Unlock()
}

func (h *countingBlockRedisHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h *countingBlockRedisHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.cmdCalls.Add(1)
		h.mu.RLock()
		err := h.err
		d := h.blockDuration
		h.mu.RUnlock()
		if d > 0 {
			select {
			case <-ctx.Done():
				cmd.SetErr(ctx.Err())
				return ctx.Err()
			case <-time.After(d):
			}
		}
		if err != nil {
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

func (h *countingBlockRedisHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelineCalls.Add(1)
		h.mu.RLock()
		err := h.err
		d := h.blockDuration
		h.mu.RUnlock()
		if d > 0 {
			select {
			case <-ctx.Done():
				for _, c := range cmds {
					c.SetErr(ctx.Err())
				}
				return ctx.Err()
			case <-time.After(d):
			}
		}
		if err != nil {
			for _, c := range cmds {
				c.SetErr(err)
			}
			return err
		}
		return next(ctx, cmds)
	}
}

// TestService_W3A_R4_1A_FailedCommitAfterConcurrentSetNeverInflatesRedis (Review 2A):
// O1 runs Lua (rem 10->9, budget 100k->80k), pauses before CommitReservation.
// A concurrent absolute SET (UpdateVoucher rename) sets Redis back to PG truth (10, 100k).
// O1 then fails CommitReservation (ErrInsufficientCoins) -> compensateRedisRelease INCRs to 11/120k.
// Unified post-compensation sync MUST immediately restore Redis to exact PG (10, 100k), never > quota!
func TestService_W3A_R4_1A_FailedCommitAfterConcurrentSetNeverInflatesRedis(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r4HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r4-1a", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r4-1a", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R41A" + suffix, Name: "R4 1A", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 10, PerUserLimit: 5, BudgetVnd: 100000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id
		buyerID := "buyer-r4-1a-" + suffix
		// Give buyer 5000 coins so buildCalcInput allows CoinsToUse=5000, then drain coins before CommitReservation
		// AND trigger UpdateVoucher rename (absolute SET -> 10 / 100k) right before CommitReservation fails ErrInsufficientCoins!
		if _, err := svc.CreditCoins(adminCtx, &promotionv1.CreditCoinsRequest{
			UserId: buyerID, Amount: 5000, Reason: "seed", ReferenceId: "r4-1a-seed-" + suffix,
		}); err != nil {
			t.Fatal(err)
		}

		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if !strings.HasPrefix(res.OrderID, "r4-1a-o1-") {
				return
			}
			hook.onBeforeCommit = nil
			// Concurrent absolute SET sees PG used=0 -> SET remaining=10, budget=100k.
			if _, uErr := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vid, Name: strPtr("R4 1A Renamed"),
			}); uErr != nil {
				t.Errorf("interleaved UpdateVoucher rename: %v", uErr)
			}
			// Drain buyer's 5000 coins on another order so O1 fails CommitReservation with ErrInsufficientCoins!
			if _, _, cErr := base.CommitReservation(context.Background(), domain.PromotionReservation{
				OrderID: "r4-1a-drain-" + suffix, UserID: buyerID, CoinsUsed: 5000,
			}, now); cErr != nil {
				t.Errorf("drain coins: %v", cErr)
			}
		}

		_, rErr := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-1a-o1-" + suffix, UserId: buyerID,
			PlatformVoucherId: vid, CoinsToUse: 5000,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if status.Code(rErr) != codes.FailedPrecondition || !strings.Contains(rErr.Error(), "INSUFFICIENT_COINS") {
			t.Fatalf("expected INSUFFICIENT_COINS failure, got %v", rErr)
		}

		remQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
		remB, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid))
		if remQ != 10 || remB != 100000 {
			t.Fatalf("1A inflation bug: Redis drifted above quota/budget after failed commit! got remQ=%d remB=%d, want 10 / 100000", remQ, remB)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R4_1C_ReleaseAfterConcurrentSetAndEvictedBudget (Review 2C & Review 9):
// (i) Release commits PG (used 1->0), concurrent SET writes 10/100k, then Release runs Lua INCR -> unified sync MUST pull back to 10/100k.
// (ii) budget_remaining key evicted while promo:res intact -> Release MUST restore budget_remaining from PG.
func TestService_W3A_R4_1C_ReleaseAfterConcurrentSetAndEvictedBudget(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r4HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r4-1c", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r4-1c", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R41C" + suffix, Name: "R4 1C", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 10, PerUserLimit: 5, BudgetVnd: 100000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		// (i) Review 2C: concurrent SET between PG release and Redis release must NOT leave Redis at 11 / 120k.
		ord1 := "r4-1c-o1-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord1, UserId: "buyer-r4-1c-" + suffix, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatal(err)
		}
		hook.onAfterRelease = func(orderID string) {
			if orderID != ord1 {
				return
			}
			hook.onAfterRelease = nil
			// PG already has used=0; concurrent UpdateVoucher rename SETs Redis to 10 / 100k BEFORE Release runs Lua INCR.
			if _, uErr := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vid, Name: strPtr("R4 1C Renamed"),
			}); uErr != nil {
				t.Errorf("interleaved UpdateVoucher rename: %v", uErr)
			}
		}
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ord1}); err != nil {
			t.Fatalf("ReleasePromotion(O1): %v", err)
		}
		remQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
		remB, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid))
		if remQ != 10 || remB != 100000 {
			t.Fatalf("1C inflation bug: Redis drifted to remQ=%d remB=%d, want exact PG 10 / 100000", remQ, remB)
		}

		// (ii) Review 9: budget_remaining evicted while promo:res:O2 is still in Redis -> Release MUST restore budget_remaining!
		ord2 := "r4-1c-o2-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord2, UserId: "buyer-r4-1c-" + suffix, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil {
			t.Fatal(err)
		}
		mr.Del(lua.VoucherBudgetRemainingKey(vid))
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ord2}); err != nil {
			t.Fatalf("ReleasePromotion(O2): %v", err)
		}
		remB2, existsB2, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid))
		if !existsB2 || remB2 != 100000 {
			t.Fatalf("Review 9 bug: evicted budget_remaining not restored on release! exists=%v val=%d want 100000", existsB2, remB2)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R4_1Retry_AndGetReservationRace (Review 3 Scenarios 1 & 2):
// (a) ReleasePromotion attempt 1 commits PG then fails Redis -> retry (PG alreadyReleased=true, promo:res lost) MUST still reconcile Redis!
// (b) ReleasePromotionShop attempt 1 commits PG then fails Redis -> retry MUST still reconcile Redis!
// (c) GetReservation race: pre-release GetReservation returns NotFound while commit races in before ReleaseReservation -> MUST still reconcile Redis!
func TestService_W3A_R4_1Retry_AndGetReservationRace(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		failHook := &failRedisHook{}
		rdb.AddHook(failHook)
		eng := lua.NewEngine(rdb)
		hook := &r4HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "r4-r3-shop-" + suffix
		skuF := "r4-r3-skuf-" + suffix
		buyer1 := "r4-r3-b1-" + suffix
		buyer2 := "r4-r3-b2-" + suffix
		buyer3 := "r4-r3-b3-" + suffix

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		adminCtx := ctxWithRole("admin-r4-r3", "admin", "")
		sellerCtx := ctxWithRole("seller-r4-r3", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r4-r3", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
			Code: "R4R3" + suffix, Name: "R4 R3", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shopID,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 10, PerUserLimit: 1, BudgetVnd: 200000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R4 R3 Flash " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fiResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fiResp.Item.Id

		reserveOne := func(oid, uid string) {
			t.Helper()
			if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
				OrderId: oid, UserId: uid, ShopVoucherIds: map[string]string{shopID: vid},
				ShopItems: []*promotionv1.ShopDiscountInput{{
					ShopId: shopID, SubtotalVnd: 200000,
					Items: []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
				}},
			}); err != nil {
				t.Fatalf("reserve %s: %v", oid, err)
			}
		}

		// (a) Retry ReleasePromotion after Redis failure & lost promo:res key.
		ordA := "r4-r3-a-" + suffix
		reserveOne(ordA, buyer1)
		failHook.SetError(errors.New("redis down during release attempt 1"))
		_, err1 := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ordA})
		failHook.SetError(nil)
		if status.Code(err1) != codes.Internal {
			t.Fatalf("attempt 1 ReleasePromotion expected Internal on Redis error, got %v", err1)
		}
		// promo:res:ordA lost in Redis while counters still show 9 / 180k / :user:=1!
		mr.Del(lua.ReservationKey(ordA))
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ordA}); err != nil {
			t.Fatalf("retry ReleasePromotion: %v", err)
		}
		if remV, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); remV != 10 {
			t.Fatalf("retry ReleasePromotion leaked voucher quota: got %d want 10", remV)
		}
		if remF, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); remF != 10 {
			t.Fatalf("retry ReleasePromotion leaked flash slots: got %d want 10", remF)
		}
		if _, exists, _ := eng.GetInt64(context.Background(), lua.VoucherUserKey(vid, buyer1)); exists {
			t.Fatalf("retry ReleasePromotion leaked voucher :user: key")
		}

		// (b) Retry ReleasePromotionShop after Redis failure & lost promo:res key.
		ordB := "r4-r3-b-" + suffix
		reserveOne(ordB, buyer2)
		failHook.SetError(errors.New("redis down during shop release attempt 1"))
		_, errShop1 := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: ordB, ShopId: shopID})
		failHook.SetError(nil)
		if status.Code(errShop1) != codes.Internal {
			t.Fatalf("attempt 1 ReleasePromotionShop expected Internal, got %v", errShop1)
		}
		mr.Del(lua.ReservationKey(ordB))
		if _, err := svc.ReleasePromotionShop(orderSvcCtx, &promotionv1.ReleasePromotionShopRequest{OrderId: ordB, ShopId: shopID}); err != nil {
			t.Fatalf("retry ReleasePromotionShop: %v", err)
		}
		if remV, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); remV != 10 {
			t.Fatalf("retry ReleasePromotionShop leaked voucher quota: got %d want 10", remV)
		}
		if remF, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); remF != 10 {
			t.Fatalf("retry ReleasePromotionShop leaked flash slots: got %d want 10", remF)
		}

		// (c) GetReservation race: if ReleasePromotion reads GetReservation before ReleaseReservation, simulate NotFound!
		ordC := "r4-r3-c-" + suffix
		reserveOne(ordC, buyer3)
		mr.Del(lua.ReservationKey(ordC))
		hook.onGetReservation = func(orderID string) (*domain.PromotionReservation, error, bool) {
			if orderID == ordC {
				return nil, store.ErrNotFound, true
			}
			return nil, nil, false
		}
		_, errC := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ordC})
		hook.onGetReservation = nil
		if errC != nil {
			t.Fatalf("ReleasePromotion(ordC): %v", errC)
		}
		if remV, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); remV != 10 {
			t.Fatalf("GetReservation race leaked voucher quota: got %d want 10", remV)
		}
		if remF, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); remF != 10 {
			t.Fatalf("GetReservation race leaked flash slots: got %d want 10", remF)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R4_2_PerUserUsageMaxPGRedis_AndShopProgramFallbackOnRace (Review 1 & Review 7):
// (a) Lost Redis :user: key -> ListAvailableVouchers, SaveVoucher, PreviewDiscounts use max(PG, Redis);
//
//	ReservePromotion for shop program falls back to regular price with warning (contracts §4.10).
//
// (b) Two concurrent orders racing on shop program per_user_limit=1 collide in CommitReservation ->
//
//	loser cleanly undoes Redis (no tombstone!) and retries inside ReservePromotion at regular price!
func TestService_W3A_R4_2_PerUserUsageMaxPGRedis_AndShopProgramFallbackOnRace(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 32, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, PoolTimeout: 30 * time.Second})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r4HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "r4-p2-shop-" + suffix
		skuF := "r4-p2-skuf-" + suffix
		skuP := "r4-p2-skup-" + suffix
		buyer1 := "r4-p2-b1-" + suffix
		buyer2 := "r4-p2-b2-" + suffix

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: skuP, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		adminCtx := ctxWithRole("admin-r4-p2", "admin", "")
		sellerCtx := ctxWithRole("seller-r4-p2", "seller", shopID)
		buyer1Ctx := ctxWithRole(buyer1, "buyer", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r4-p2", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
			Code: "R4P2V" + suffix, Name: "R4 P2 Voucher", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shopID,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 10, PerUserLimit: 1, BudgetVnd: 200000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R4 P2 Flash " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fiResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fiResp.Item.Id

		progResp, err := svc.CreateShopDiscountProgram(sellerCtx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "R4 P2 Prog " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: skuP, DiscountedPriceVnd: 150000, PerUserLimit: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pid := progResp.Program.Items[0].Id

		// (a) Exhaust per_user_limit=1 on all 3 for buyer1, then delete all 3 Redis :user: keys!
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-p2-init-" + suffix, UserId: buyer1, ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID, SubtotalVnd: 400000,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid},
					{SkuId: skuP, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: pid},
				},
			}},
		}); err != nil {
			t.Fatalf("initial reserve: %v", err)
		}
		mr.Del(lua.VoucherUserKey(vid, buyer1))
		mr.Del(lua.FlashUserKey(fid, buyer1))
		mr.Del(lua.ShopProgramUserKey(pid, buyer1))

		// ListAvailableVouchers must exclude exhausted voucher despite missing Redis :user: key!
		listResp, err := svc.ListAvailableVouchers(buyer1Ctx, &promotionv1.ListAvailableVouchersRequest{ShopId: shopID})
		if err != nil {
			t.Fatalf("ListAvailableVouchers: %v", err)
		}
		for _, v := range listResp.GetVouchers() {
			if v.GetId() == vid {
				t.Fatalf("ListAvailableVouchers returned exhausted voucher %s after Redis :user: wipe", vid)
			}
		}

		// SaveVoucher must reject exhausted voucher despite missing Redis :user: key!
		if _, err := svc.SaveVoucher(buyer1Ctx, &promotionv1.SaveVoucherRequest{VoucherId: vid}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SaveVoucher after Redis :user: wipe: want FailedPrecondition, got %v", err)
		}

		// PreviewDiscounts must see PG used=1 for voucher, flash, AND shop program (all 0 discount + warnings)!
		prev, err := svc.PreviewDiscounts(buyer1Ctx, &promotionv1.PreviewDiscountsRequest{
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuF, Quantity: 1, FlashSaleItemId: fid},
					{SkuId: skuP, Quantity: 1, ShopDiscountProgramItemId: pid},
				},
			}},
		})
		if err != nil {
			t.Fatalf("PreviewDiscounts: %v", err)
		}
		bd := prev.GetBreakdown()
		if bd.GetTotalShopVoucherDiscountVnd() != 0 || bd.GetTotalFlashSaleDiscountVnd() != 0 || bd.GetTotalShopProgramDiscountVnd() != 0 {
			t.Fatalf("PreviewDiscounts ignored PG user usage after Redis wipe: voucher=%d flash=%d prog=%d",
				bd.GetTotalShopVoucherDiscountVnd(), bd.GetTotalFlashSaleDiscountVnd(), bd.GetTotalShopProgramDiscountVnd())
		}
		if len(bd.GetWarnings()) < 3 {
			t.Fatalf("expected warnings for voucher/flash/shop_program per-user limits, got %v", bd.GetWarnings())
		}

		// ReservePromotion on shop program after Redis :user: wipe MUST fall back to regular price with warning!
		resFallback, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-p2-fb-" + suffix, UserId: buyer1,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID, SubtotalVnd: 200000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: skuP, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: pid}},
			}},
		})
		if err != nil || !resFallback.GetReserved() ||
			resFallback.GetBreakdown().GetTotalShopProgramDiscountVnd() != 0 ||
			resFallback.GetBreakdown().GetFinalPayableVnd() != 200000 ||
			len(resFallback.GetBreakdown().GetWarnings()) == 0 {
			t.Fatalf("ReservePromotion shop program over limit after Redis wipe must fall back to regular price, got resp=%+v err=%v", resFallback, err)
		}

		// (b) 2-order concurrent race for buyer2 (per_user_limit=1): BOTH pass buildCalcInput + Lua and collide in CommitReservation!
		// Winner gets 50k discount; loser cleanly undoes Redis (no tombstone) and retries at regular price (0 discount + warning)!
		var entered atomic.Int32
		g1Ready := make(chan struct{})
		bothIn := make(chan struct{})
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.UserID != buyer2 {
				return
			}
			n := entered.Add(1)
			if n == 1 {
				mr.Del(lua.ShopProgramUserKey(pid, buyer2))
				close(g1Ready)
				<-bothIn
			} else if n == 2 {
				close(bothIn)
			}
		}

		type raceRes struct {
			resp *promotionv1.ReservePromotionResponse
			err  error
		}
		results := make([]raceRes, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func(idx int) {
				defer wg.Done()
				if idx == 1 {
					<-g1Ready
				}
				resp, rErr := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
					OrderId: fmt.Sprintf("r4-p2-race-%s-%d", suffix, idx), UserId: buyer2,
					ShopItems: []*promotionv1.ShopDiscountInput{{
						ShopId: shopID, SubtotalVnd: 200000,
						Items: []*promotionv1.DiscountLineItem{{SkuId: skuP, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: pid}},
					}},
				})
				results[idx] = raceRes{resp: resp, err: rErr}
			}(i)
		}
		wg.Wait()
		hook.onBeforeCommit = nil

		var discounted, regular int
		for idx, r := range results {
			if r.err != nil || !r.resp.GetReserved() {
				t.Fatalf("concurrent race order %d failed instead of falling back cleanly: resp=%+v err=%v", idx, r.resp, r.err)
			}
			switch r.resp.GetBreakdown().GetTotalShopProgramDiscountVnd() {
			case 50000:
				discounted++
			case 0:
				if len(r.resp.GetBreakdown().GetWarnings()) == 0 {
					t.Fatalf("fallback order %d missing warning", idx)
				}
				regular++
			default:
				t.Fatalf("unexpected discount on order %d: %d", idx, r.resp.GetBreakdown().GetTotalShopProgramDiscountVnd())
			}
		}
		if discounted != 1 || regular != 1 {
			t.Fatalf("2-order shop program race: want 1 discounted + 1 regular fallback, got discounted=%d regular=%d", discounted, regular)
		}
		usageKey := store.UserUsageKey{Kind: domain.UsageKindShopProgram, TargetID: pid}
		usageMap, uErr := base.GetUserUsageBatch(context.Background(), buyer2, []store.UserUsageKey{usageKey})
		if uErr != nil || usageMap[usageKey] != 1 {
			t.Fatalf("PG shop program used_qty for buyer2: want 1, got %+v err=%v", usageMap, uErr)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R4_3_ReconcileStopsOnRedisConnErr_AndLockedTimeout (Review 4):
// (a) ReconcilePendingVouchers stops batch immediately on Redis connection/timeout error (1 call, not 5).
// (b) Redis call inside FOR UPDATE lock times out within <= 500ms.
func TestService_W3A_R4_3_ReconcileStopsOnRedisConnErr_AndLockedTimeout(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: 0})
		t.Cleanup(func() { _ = rdb.Close() })
		rHook := &countingBlockRedisHook{}
		rdb.AddHook(rHook)
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		adminCtx := ctxWithRole("admin-r4-p3", "admin", "")
		svc := service.NewPromotionService(base, eng, logger.New("r4-p3", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		// Seed 5 dirty vouchers directly in store (redis_sync_version=1, redis_synced_version=0).
		for i := 0; i < 5; i++ {
			if _, err := base.CreateVoucher(context.Background(), domain.Voucher{
				Code: fmt.Sprintf("R4P3%s%d", suffix, i), Name: "Dirty", Scope: domain.VoucherScopePlatform,
				DiscountType: domain.DiscountTypeFixed, ValueVND: 10000, Quota: 10, PerUserLimit: 2,
				StartsAt: now.Add(-time.Hour), EndsAt: now.Add(24 * time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
			}); err != nil {
				t.Fatal(err)
			}
		}

		// (a) On context.DeadlineExceeded / transport error, batch MUST stop after 1st failure (not try all 5)!
		rHook.SetError(context.DeadlineExceeded)
		rHook.pipelineCalls.Store(0)
		_, recErr := svc.ReconcileVoucherRedisOnce(context.Background())
		rHook.SetError(nil)
		if recErr == nil {
			t.Fatalf("expected error from ReconcileVoucherRedisOnce when Redis times out")
		}
		if got := rHook.pipelineCalls.Load(); got != 1 {
			t.Fatalf("ReconcilePendingVouchers did not stop batch on Redis timeout: pipelineCalls=%d (want 1)", got)
		}

		// (b) Locked Redis operation enforces <= 500ms timeout even when parent context has no short deadline!
		rHook.SetBlock(2 * time.Second)
		t0 := time.Now()
		_, blockErr := svc.ReconcileVoucherRedisOnce(adminCtx)
		elapsed := time.Since(t0)
		rHook.SetBlock(0)
		if !errors.Is(blockErr, context.DeadlineExceeded) {
			t.Fatalf("expected DeadlineExceeded on blocked locked Redis op, got %v", blockErr)
		}
		if elapsed > 1200*time.Millisecond {
			t.Fatalf("locked Redis op held row lock too long (%s); must enforce <= 500ms timeout", elapsed)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R4_4_MidFlightRedisFlush_AutoHydratesAndPreviewFallsBackToPG (Review 5):
// After mid-flight Redis FlushAll (no restart):
// (a) PreviewDiscounts on approved flash item uses PG RemainingSlots (not 0).
// (b) ReservePromotion with voucher (budget) + flash item hits *_NOT_INITIALIZED -> hydrates from PG & retries Lua ONCE -> succeeds!
func TestService_W3A_R4_4_MidFlightRedisFlush_AutoHydratesAndPreviewFallsBackToPG(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "r4-p4-shop-" + suffix
		skuF := "r4-p4-skuf-" + suffix
		buyer1 := "r4-p4-b1-" + suffix
		buyer2 := "r4-p4-b2-" + suffix

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		adminCtx := ctxWithRole("admin-r4-p4", "admin", "")
		sellerCtx := ctxWithRole("seller-r4-p4", "seller", shopID)
		buyer2Ctx := ctxWithRole(buyer2, "buyer", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(base, eng, logger.New("r4-p4", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
			Code: "R4P4V" + suffix, Name: "R4 P4 Voucher", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shopID,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 1, BudgetVnd: 100000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "R4 P4 Flash " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fiResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 5, PerUserLimit: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fiResp.Item.Id

		// buyer1 reserves 1 (PG: voucher rem=4, budget=80k, flash rem=4, buyer1 used=1).
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-p4-o1-" + suffix, UserId: buyer1, ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID, SubtotalVnd: 200000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		}); err != nil {
			t.Fatal(err)
		}

		// Simulate mid-flight Redis wipe (NO restart / NO SyncRedisFromStore)!
		mr.FlushAll()

		// (a) PreviewDiscounts MUST fall back to PG RemainingSlots (=4) and apply flash discount (80k)!
		prev, err := svc.PreviewDiscounts(buyer2Ctx, &promotionv1.PreviewDiscountsRequest{
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, FlashSaleItemId: fid}},
			}},
		})
		if err != nil {
			t.Fatalf("PreviewDiscounts after Redis flush: %v", err)
		}
		if prev.GetBreakdown().GetTotalFlashSaleDiscountVnd() != 80000 || prev.GetBreakdown().GetTotalShopVoucherDiscountVnd() != 20000 {
			t.Fatalf("PreviewDiscounts after Redis flush failed to use PG remaining slots: %+v", prev.GetBreakdown())
		}

		// (b) ReservePromotion for buyer2 MUST auto-hydrate voucher, flash, & user usage from PG and succeed on 1 retry!
		res2, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-p4-o2-" + suffix, UserId: buyer2, ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID, SubtotalVnd: 200000,
				Items: []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		})
		if err != nil || !res2.GetReserved() ||
			res2.GetBreakdown().GetTotalFlashSaleDiscountVnd() != 80000 ||
			res2.GetBreakdown().GetTotalShopVoucherDiscountVnd() != 20000 {
			t.Fatalf("ReservePromotion after mid-flight Redis flush failed: resp=%+v err=%v", res2, err)
		}
		if remV, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); remV != 3 {
			t.Fatalf("hydrated voucher remaining: got %d want 3", remV)
		}
		if remF, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); remF != 3 {
			t.Fatalf("hydrated flash remaining: got %d want 3", remF)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R4_5_UserUsageSyncRaceDoesNotLockOutBuyer (Review 6 & Round 5 P2-9):
// Genuinely interleaves ReleasePromotion(O1) right between SyncUserUsageRedis's PG read (used_qty=1)
// and its Redis SET :user: 1 write (inside the Redis pipeline hook).
// With FOR UPDATE in SyncUserUsageRedis, ReleasePromotion(O1) MUST block until sync commits and then
// delete the Redis :user: key. Removing FOR UPDATE causes ReleasePromotion(O1) to finish BEFORE sync
// writes stale 1 to Redis, failing both assertions deterministically!
func TestService_W3A_R4_5_UserUsageSyncRaceDoesNotLockOutBuyer(t *testing.T) {
	run := func(t *testing.T, base store.Store) {
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
		hook := &r4HookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		buyerID := "r4-p5-buyer-" + suffix
		adminCtx := ctxWithRole("admin-r4-p5", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		svc := service.NewPromotionService(hook, eng, logger.New("r4-p5", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R4P5" + suffix, Name: "R4 P5", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
			Quota: 10, PerUserLimit: 1,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		ord1 := "r4-p5-o1-" + suffix
		ord2 := "r4-p5-o2-" + suffix

		// Force O2 to pass buildCalcInput & Lua (when buyer usage is 0), then commit O1 right before O2's
		// CommitReservation so O2 genuinely fails ErrVoucherUserLimitExceeded -> reconcileTargetsBestEffort
		// -> SyncUserUsageRedis (reading PG used_qty=1 and issuing Redis SET :user: 1).
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.OrderID != ord2 {
				return
			}
			hook.onBeforeCommit = nil
			if _, _, cErr := base.CommitReservation(context.Background(), domain.PromotionReservation{
				OrderID:  ord1,
				UserID:   buyerID,
				Vouchers: []domain.ReservedVoucher{{VoucherID: vid, Scope: domain.VoucherScopePlatform, DiscountVND: 10000}},
			}, now); cErr != nil {
				t.Errorf("seed O1 commit: %v", cErr)
			}
		}

		// Intercept O2's SyncUserUsageRedis pipeline SET for :user: BEFORE it executes on Redis,
		// spawn ReleasePromotion(O1) concurrently, and wait up to 150ms to detect whether Release
		// bypassed the row lock and finished BEFORE SyncUserUsageRedis released it!
		targetUserKey := lua.VoucherUserKey(vid, buyerID)
		var intercepted atomic.Bool
		var relFinishedDuringSync atomic.Bool
		relDone := make(chan error, 1)

		rHook.mu.Lock()
		rHook.onSetKey = func(ctx context.Context, key string) {
			if key != targetUserKey || !intercepted.CompareAndSwap(false, true) {
				return
			}
			go func() {
				_, rErr := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ord1})
				relDone <- rErr
			}()
			select {
			case rErr := <-relDone:
				// BUG if this triggers: ReleasePromotion(O1) was NOT serialized by FOR UPDATE!
				relFinishedDuringSync.Store(true)
				relDone <- rErr
			case <-time.After(150 * time.Millisecond):
				// Expected with FOR UPDATE: ReleasePromotion(O1) is blocked waiting for our row lock!
			case <-ctx.Done():
			}
		}
		rHook.mu.Unlock()

		_, errO2 := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ord2, UserId: buyerID, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if status.Code(errO2) != codes.FailedPrecondition || !strings.Contains(errO2.Error(), "VOUCHER_USER_LIMIT_EXCEEDED") {
			t.Fatalf("O2 expected VOUCHER_USER_LIMIT_EXCEEDED from CommitReservation, got %v", errO2)
		}
		if !intercepted.Load() {
			t.Fatalf("test setup error: SyncUserUsageRedis SET on %s was never intercepted", targetUserKey)
		}
		if rErr := <-relDone; rErr != nil {
			t.Fatalf("concurrent ReleasePromotion(O1) failed: %v", rErr)
		}
		if relFinishedDuringSync.Load() {
			t.Fatalf("P2-9 race bug: ReleasePromotion(O1) completed while SyncUserUsageRedis was in-flight (missing FOR UPDATE serialization!)")
		}
		if val, exists, _ := eng.GetInt64(context.Background(), targetUserKey); exists {
			t.Fatalf("Review 6 lockout bug: stale Redis :user: key left at %d after concurrent O1 release to 0!", val)
		}
		if res3, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r4-p5-o3-" + suffix, UserId: buyerID, PlatformVoucherId: vid,
			ShopItems: []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		}); err != nil || !res3.GetReserved() {
			t.Fatalf("buyer permanently locked out after concurrent O1 release: resp=%+v err=%v", res3, err)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) { run(t, store.NewMemoryStore()) })
	t.Run("PostgresStore", func(t *testing.T) { run(t, openR2PostgresStore(t)) })
}

// TestService_W3A_R4_8_UpdateVoucherIgnoresUnsyncedRedisLiveUsed_AndUserUsageBatches1000 (Review 11 & 12).
func TestService_W3A_R4_8_UpdateVoucherIgnoresUnsyncedRedisLiveUsed_AndUserUsageBatches1000(t *testing.T) {
	t.Run("UpdateVoucherAllowsRuleEditWhenUnusedAndRedisUnsynced", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		failHook := &failRedisHook{}
		rdb.AddHook(failHook)
		eng := lua.NewEngine(rdb)
		hook := &r4HookStore{Store: store.NewMemoryStore()}

		now := time.Now().UTC()
		adminCtx := ctxWithRole("admin-r4-p8", "admin", "")
		svc := service.NewPromotionService(hook, eng, logger.New("r4-p8", logger.WithWriter(io.Discard)))
		svc.SetClock(func() time.Time { return now })

		vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
			Code: "R4P8V", Name: "R4 P8", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 10, PerUserLimit: 2,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vr.Voucher.Id

		// Raise quota 10->15 with simulated Redis failure -> PG quota=15 (sync=2, synced=1), Redis still 10!
		hook.onAfterUpdate = func(_ *domain.Voucher) {
			hook.onAfterUpdate = nil
			failHook.SetError(errors.New("redis down"))
		}
		_, err1 := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{VoucherId: vid, Quota: i32Ptr(15)})
		failHook.SetError(nil)
		if status.Code(err1) != codes.Internal {
			t.Fatalf("expected Internal on failed sync, got %v", err1)
		}

		// Immediately edit value_vnd while voucher is UNUSED and Redis is unsynced -> MUST SUCCEED!
		upd2, err2 := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
			VoucherId: vid, ValueVnd: i64Ptr(35000),
		})
		if err2 != nil || upd2.GetVoucher().GetValueVnd() != 35000 {
			t.Fatalf("Review 11 bug: unused voucher rejected rule edit due to unsynced Redis quota diff: resp=%+v err=%v", upd2, err2)
		}
	})

	t.Run("ReconcileUserUsageKeysSetsTTLAndDeletesZero", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		cHook := &countingBlockRedisHook{}
		rdb.AddHook(cHook)
		eng := lua.NewEngine(rdb)

		_ = mr.Set(lua.VoucherUserKey("v-rec", "u-del"), "1")
		entries := []domain.UserUsage{
			{Kind: domain.UsageKindVoucher, TargetID: "v-rec", UserID: "u-pos", UsedQty: 2},
			{Kind: domain.UsageKindVoucher, TargetID: "v-rec", UserID: "u-del", UsedQty: 0},
		}

		cHook.pipelineCalls.Store(0)
		if err := eng.ReconcileUserUsageKeys(context.Background(), entries); err != nil {
			t.Fatalf("ReconcileUserUsageKeys: %v", err)
		}
		if got := cHook.pipelineCalls.Load(); got != 1 {
			t.Fatalf("expected 1 pipeline call, got %d", got)
		}
		if v, _, _ := eng.GetInt64(context.Background(), lua.VoucherUserKey("v-rec", "u-pos")); v != 2 {
			t.Fatalf("positive entry mismatch: got %d want 2", v)
		}
		if _, exists, _ := eng.GetInt64(context.Background(), lua.VoucherUserKey("v-rec", "u-del")); exists {
			t.Fatalf("zero entry must be deleted")
		}
	})
}
