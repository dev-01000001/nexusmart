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
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/lua"
	"github.com/nexusmart/core/services/promotion/internal/service"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

type r2VoucherHookStore struct {
	store.Store
	onAfterUpdate       func(after *domain.Voucher)
	onAfterSetStatus    func(after *domain.Voucher)
	onAfterGetVoucher   func(v *domain.Voucher)
	onAfterGetFlashItem func(fi *domain.FlashSaleItem)
	onBeforeCommit      func(res domain.PromotionReservation)
	commitCalls         atomic.Int32
}

func (h *r2VoucherHookStore) GetVoucherByID(ctx context.Context, id string) (*domain.Voucher, error) {
	v, err := h.Store.GetVoucherByID(ctx, id)
	if err == nil && h.onAfterGetVoucher != nil {
		h.onAfterGetVoucher(v)
	}
	return v, err
}

func (h *r2VoucherHookStore) GetFlashSaleItem(ctx context.Context, itemID string) (*domain.FlashSaleItem, error) {
	fi, err := h.Store.GetFlashSaleItem(ctx, itemID)
	if err == nil && h.onAfterGetFlashItem != nil {
		h.onAfterGetFlashItem(fi)
	}
	return fi, err
}

func (h *r2VoucherHookStore) UpdateVoucher(
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

func (h *r2VoucherHookStore) SetVoucherStatus(
	ctx context.Context,
	id, newStatus string,
	check func(cur *domain.Voucher) error,
) (*domain.Voucher, error) {
	after, err := h.Store.SetVoucherStatus(ctx, id, newStatus, check)
	if err == nil && h.onAfterSetStatus != nil {
		h.onAfterSetStatus(after)
	}
	return after, err
}

func (h *r2VoucherHookStore) CommitReservation(
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

func openR2PostgresStore(t *testing.T) *store.PostgresStore {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_PROMOTION_DB_URL"))
	if dsn == "" {
		t.Skip("TEST_PROMOTION_DB_URL not set; run via scripts/test-db.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)
	pgStore := store.NewPostgresStore(pool)
	if err := pgStore.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return pgStore
}

// TestService_W3A_R2_Point1_DeterministicSyncRaces proves all 3 deterministic voucher sync races (a, b, c)
// fail on unlocked delta sync and converge to exact PG truth with locked absolute SET sync.
func TestService_W3A_R2_Point1_DeterministicSyncRaces(t *testing.T) {
	runCases := func(t *testing.T, newBase func(t *testing.T) store.Store) {
		now := time.Now().UTC()
		adminCtx := ctxWithRole("admin-r2", "admin", "")
		orderSvcCtx := ctxWithService("order-service")

		// (1a) UpdateVoucher quota 10->20 commits PG -> reconciler tick runs BEFORE UpdateVoucher syncs Redis
		// -> UpdateVoucher finishes its Redis sync -> Redis MUST equal 20-used (NEVER 30-used > quota!).
		t.Run("1a_UpdateVoucherVsReconcilerNeverExceedsQuota", func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			eng := lua.NewEngine(rdb)
			hook := &r2VoucherHookStore{Store: newBase(t)}
			svc := service.NewPromotionService(hook, eng, logger.New("r2-1a", logger.WithWriter(io.Discard)))
			svc.SetClock(func() time.Time { return now })

			suffix := uuid.NewString()[:8]
			vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
				Code: "R21A" + suffix, Name: "R2 1a", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
				Quota: 10, PerUserLimit: 5, BudgetVnd: 100000,
				StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			vid := vr.Voucher.Id

			// Use 2 slots -> PG quota=10 used=2, budget=100k used_budget=20k; Redis rem=8 bud=80k.
			for i := 0; i < 2; i++ {
				if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
					OrderId: fmt.Sprintf("r2-1a-ord-%s-%d", suffix, i), UserId: "buyer-1a-" + suffix,
					PlatformVoucherId: vid,
					ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
				}); err != nil {
					t.Fatal(err)
				}
			}

			// Deterministically run reconciler right AFTER PG UpdateVoucher commits and BEFORE UpdateVoucher syncs Redis!
			hook.onAfterUpdate = func(_ *domain.Voucher) {
				hook.onAfterUpdate = nil
				if _, rErr := svc.ReconcileVoucherRedisOnce(context.Background()); rErr != nil {
					t.Errorf("interleaved ReconcileVoucherRedisOnce: %v", rErr)
				}
			}
			if _, err := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vid, Quota: i32Ptr(20), BudgetVnd: i64Ptr(200000),
			}); err != nil {
				t.Fatalf("UpdateVoucher(10->20): %v", err)
			}

			remQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
			remB, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid))
			if remQ != 18 {
				t.Fatalf("1a race: Redis remaining drifted to %d (expected exact PG 20-2=18, never > quota 20!)", remQ)
			}
			if remB != 180000 {
				t.Fatalf("1a race: Redis budget drifted to %d (expected exact PG 200000-20000=180000)", remB)
			}
		})

		// (1b) Update A (v2: quota 10->15) fails Redis sync, then Update B (v3: quota 15->20) succeeds Redis sync
		// -> Redis MUST equal 20-used (NOT 15-used with A's delta lost forever!).
		t.Run("1b_FailedUpdateAThenSuccessfulUpdateBDoesNotLoseDelta", func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			failHook := &failRedisHook{}
			rdb.AddHook(failHook)
			eng := lua.NewEngine(rdb)
			hook := &r2VoucherHookStore{Store: newBase(t)}
			svc := service.NewPromotionService(hook, eng, logger.New("r2-1b", logger.WithWriter(io.Discard)))
			svc.SetClock(func() time.Time { return now })

			suffix := uuid.NewString()[:8]
			vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
				Code: "R21B" + suffix, Name: "R2 1b", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
				Quota: 10, PerUserLimit: 5, BudgetVnd: 100000,
				StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			vid := vr.Voucher.Id

			// Update A (10->15, budget 100k->150k): PG commits, then deterministic client-side Redis error fails sync!
			hook.onAfterUpdate = func(_ *domain.Voucher) {
				hook.onAfterUpdate = nil
				failHook.SetError(errors.New("redis failed during Update A sync"))
			}
			_, errA := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vid, Quota: i32Ptr(15), BudgetVnd: i64Ptr(150000),
			})
			failHook.SetError(nil)
			if status.Code(errA) != codes.Internal {
				t.Fatalf("Update A expected Internal on Redis error, got %v", errA)
			}
			if staleQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); staleQ != 10 {
				t.Fatalf("Update A failed Redis sync must leave Redis untouched at 10, got %d", staleQ)
			}

			// Update B (15->20, budget 150k->200k): PG commits and Redis sync succeeds.
			if _, errB := svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vid, Quota: i32Ptr(20), BudgetVnd: i64Ptr(200000),
			}); errB != nil {
				t.Fatalf("Update B failed: %v", errB)
			}

			remQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
			remB, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid))
			if remQ != 20 || remB != 200000 {
				t.Fatalf("1b race: Update A delta lost! got Redis remQ=%d remB=%d, want remQ=20 remB=200000", remQ, remB)
			}
		})

		// (1c) SetVoucherStatus A=PAUSED (v2) commits PG then pauses BEFORE Redis sync;
		// B=ACTIVE (v3) commits PG and syncs Redis; then A resumes Redis sync -> Redis MUST remain ACTIVE!
		t.Run("1c_OutOfOrderSetVoucherStatusNeverWritesStaleStatus", func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			eng := lua.NewEngine(rdb)
			hook := &r2VoucherHookStore{Store: newBase(t)}
			svc := service.NewPromotionService(hook, eng, logger.New("r2-1c", logger.WithWriter(io.Discard)))
			svc.SetClock(func() time.Time { return now })

			suffix := uuid.NewString()[:8]
			vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
				Code: "R21C" + suffix, Name: "R2 1c", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
				Quota: 10, PerUserLimit: 5,
				StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			vid := vr.Voucher.Id

			aCommitted := make(chan struct{})
			bDone := make(chan struct{})
			var calls atomic.Int32
			hook.onAfterSetStatus = func(after *domain.Voucher) {
				if calls.Add(1) == 1 && after.EffectiveStatus() == domain.VoucherStatusPaused {
					close(aCommitted)
					<-bDone
				}
			}

			aDone := make(chan error, 1)
			go func() {
				_, errA := svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{
					VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED,
				})
				aDone <- errA
			}()

			<-aCommitted
			// While A is paused after PG commit (PAUSED) and before Redis sync, B sets ACTIVE and syncs Redis!
			if _, errB := svc.SetVoucherStatus(adminCtx, &promotionv1.SetVoucherStatusRequest{
				VoucherId: vid, Status: promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE,
			}); errB != nil {
				t.Fatalf("SetVoucherStatus(ACTIVE): %v", errB)
			}
			close(bDone)
			if errA := <-aDone; errA != nil {
				t.Fatalf("SetVoucherStatus(PAUSED): %v", errA)
			}

			gotPG, _ := hook.Store.GetVoucherByID(context.Background(), vid)
			gotRedisStatus, err := mr.Get(lua.VoucherStatusKey(vid))
			if err != nil {
				t.Fatalf("get redis status: %v", err)
			}
			if gotPG.EffectiveStatus() != domain.VoucherStatusActive || gotRedisStatus != domain.VoucherStatusActive {
				t.Fatalf("1c race: out-of-order status write! PG=%s Redis=%s (want both ACTIVE)", gotPG.EffectiveStatus(), gotRedisStatus)
			}
		})

		// (1d) reconcileAfterLostRedisRelease MUST read & SET voucher AND flash item inside a row lock
		// so a concurrent ReleasePromotion committing & INCRing in between NEVER causes Redis to end up
		// lower than PG (losing a real sellable slot).
		t.Run("1d_LostRedisReleaseDoesNotLoseSlotToConcurrentRelease", func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			eng := lua.NewEngine(rdb)
			hook := &r2VoucherHookStore{Store: newBase(t)}

			suffix := uuid.NewString()[:8]
			shopID := "r2-1d-shop-" + suffix
			skuF := "r2-1d-skuf-" + suffix
			pc := newFakeProductClient()
			pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

			svc := service.NewPromotionService(hook, eng, logger.New("r2-1d", logger.WithWriter(io.Discard)))
			svc.SetProductClient(pc)
			svc.SetClock(func() time.Time { return now })
			sellerCtx := ctxWithRole("seller-1d", "seller", shopID)

			vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
				Code: "R21D" + suffix, Name: "R2 1d", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
				DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
				Quota: 10, PerUserLimit: 5, BudgetVnd: 100000,
				StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			vid := vr.Voucher.Id

			fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
				Name: "R2 1d Flash " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			})
			if err != nil {
				t.Fatal(err)
			}
			fiResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
				FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 10, PerUserLimit: 5,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
				t.Fatal(err)
			}
			fid := fiResp.Item.Id

			// Reserve O1 and O2 on the same voucher & flash item -> PG used=2 (rem=8), flash rem=8; Redis rem=8.
			ord1 := "r2-1d-o1-" + suffix
			ord2 := "r2-1d-o2-" + suffix
			for _, oid := range []string{ord1, ord2} {
				if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
					OrderId: oid, UserId: "buyer-1d-" + suffix, PlatformVoucherId: vid,
					ShopItems: []*promotionv1.ShopDiscountInput{{
						ShopId: shopID,
						Items:  []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
					}},
				}); err != nil {
					t.Fatalf("reserve %s: %v", oid, err)
				}
			}

			// Delete ONLY promo:res:O1 (O1 hits lost-Redis reconcile path; O2 stays intact for normal INCR release).
			mr.Del(lua.ReservationKey(ord1))

			// If reconcileAfterLostRedisRelease uses unlocked GetVoucherByID/GetFlashSaleItem,
			// interleave ReleasePromotion(O2) right after the unlocked read and before Redis SET.
			var triggered atomic.Bool
			releaseO2 := func() {
				if triggered.CompareAndSwap(false, true) {
					if _, rErr := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ord2}); rErr != nil {
						t.Errorf("interleaved ReleasePromotion(O2): %v", rErr)
					}
				}
			}
			hook.onAfterGetVoucher = func(v *domain.Voucher) {
				if v.ID == vid {
					releaseO2()
				}
			}
			hook.onAfterGetFlashItem = func(fi *domain.FlashSaleItem) {
				if fi.ID == fid {
					releaseO2()
				}
			}

			if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ord1}); err != nil {
				t.Fatalf("ReleasePromotion(O1): %v", err)
			}
			hook.onAfterGetVoucher = nil
			hook.onAfterGetFlashItem = nil
			// If fixed code avoided unlocked GetVoucherByID/GetFlashSaleItem, release O2 now and verify Redis == PG.
			if triggered.CompareAndSwap(false, true) {
				if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ord2}); err != nil {
					t.Fatalf("ReleasePromotion(O2): %v", err)
				}
			}

			remV, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
			remF, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid))
			if remV != 10 {
				t.Fatalf("1d race (voucher): lost sellable slot! Redis remaining=%d, want exact PG 10", remV)
			}
			if remF != 10 {
				t.Fatalf("1d race (flash): lost sellable slot! Redis remaining=%d, want exact PG 10", remF)
			}
		})
	}

	t.Run("MemoryStore", func(t *testing.T) {
		runCases(t, func(*testing.T) store.Store { return store.NewMemoryStore() })
	})
	t.Run("PostgresStore", func(t *testing.T) {
		pg := openR2PostgresStore(t)
		runCases(t, func(*testing.T) store.Store { return pg })
	})
}

// TestService_W3A_R2_Point1_ConcurrentStress_UpdateReconcileReserveRelease races UpdateVoucher,
// ReconcileVoucherRedisOnce, ReservePromotion, and ReleasePromotion on real Postgres + miniredis
// and asserts final Redis state == exact Postgres state (never > quota).
func TestService_W3A_R2_Point1_ConcurrentStress_UpdateReconcileReserveRelease(t *testing.T) {
	pg := openR2PostgresStore(t)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{
		Addr:         mr.Addr(),
		PoolSize:     64,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		PoolTimeout:  30 * time.Second,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	eng := lua.NewEngine(rdb)

	now := time.Now().UTC()
	svc := service.NewPromotionService(pg, eng, logger.New("r2-stress", logger.WithWriter(io.Discard)))
	svc.SetClock(func() time.Time { return now })

	adminCtx := ctxWithRole("admin-stress", "admin", "")
	orderSvcCtx := ctxWithService("order-service")
	suffix := uuid.NewString()[:8]

	vr, err := svc.CreateVoucher(adminCtx, &promotionv1.CreateVoucherRequest{
		Code: "R2STR" + suffix, Name: "Stress Voucher", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM,
		DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 10000,
		Quota: 10, PerUserLimit: 50, BudgetVnd: 100000,
		StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	vid := vr.Voucher.Id

	var wg sync.WaitGroup
	start := make(chan struct{})

	// 1. Updater raising quota & budget monotonically (10 -> 30, 100k -> 300k)
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for step := int32(1); step <= 10; step++ {
			q := 10 + step*2
			b := int64(q) * 10000
			_, _ = svc.UpdateVoucher(adminCtx, &promotionv1.UpdateVoucherRequest{
				VoucherId: vid, Quota: &q, BudgetVnd: &b,
			})
		}
	}()

	// 2. Concurrent reconciler ticks
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 20; i++ {
			_, _ = svc.ReconcileVoucherRedisOnce(context.Background())
		}
	}()

	// 3. Concurrent reserve + release workers
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for r := 0; r < 8; r++ {
				ordID := fmt.Sprintf("r2-str-%s-w%d-r%d", suffix, worker, r)
				resp, rErr := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
					OrderId:           ordID,
					UserId:            fmt.Sprintf("r2-str-b-%s-%d", suffix, worker),
					PlatformVoucherId: vid,
					ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
				})
				if rErr == nil && resp.GetReserved() && r%2 == 0 {
					_, _ = svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ordID})
				}
			}
		}(w)
	}

	close(start)
	wg.Wait()

	// Final reconciler pass settles any pending dirty version without calling startup SyncRedisFromStore.
	if _, err := svc.ReconcileVoucherRedisOnce(context.Background()); err != nil {
		t.Fatalf("final ReconcileVoucherRedisOnce: %v", err)
	}

	gotV, err := pg.GetVoucherByID(context.Background(), vid)
	if err != nil {
		t.Fatal(err)
	}
	if gotV.UsedCount > gotV.Quota || gotV.UsedBudgetVND > gotV.BudgetVND {
		t.Fatalf("PG oversell detected: used=%d/%d budget=%d/%d", gotV.UsedCount, gotV.Quota, gotV.UsedBudgetVND, gotV.BudgetVND)
	}
	wantRemQ := int64(gotV.Quota - gotV.UsedCount)
	gotRemQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
	if gotRemQ < wantRemQ {
		t.Fatalf("lost sellable slots after stress race: Redis remQ=%d < PG wantRemQ=%d", gotRemQ, wantRemQ)
	}

	// Drain all remaining slots via ReservePromotion (without SyncRedisFromStore):
	// PG must accept EXACTLY wantRemQ more orders (0 oversell, 0 lost slots), and the first rejection
	// self-heals any transient Redis>PG skew so final Redis == exact PG (0 == 0)!
	var drained int64
	for i := int64(0); i < wantRemQ+5; i++ {
		resp, dErr := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId:           fmt.Sprintf("r2-str-drain-%s-%d", suffix, i),
			UserId:            fmt.Sprintf("r2-str-drain-b-%s-%d", suffix, i),
			PlatformVoucherId: vid,
			ShopItems:         []*promotionv1.ShopDiscountInput{{ShopId: "shop-1", SubtotalVnd: 100000}},
		})
		if dErr == nil && resp.GetReserved() {
			drained++
		}
	}
	if drained != wantRemQ {
		t.Fatalf("drain mismatch: accepted %d orders, want exact PG remaining %d", drained, wantRemQ)
	}

	// Under D12, background drift reconciler (2 consecutive identical ticks) heals any Scenario B Redis>PG skew.
	driftTracker := make(service.RedisDriftTracker)
	for tick := 0; tick < 2; tick++ {
		if _, err := svc.ReconcileRedisDriftOnce(context.Background(), driftTracker); err != nil {
			t.Fatalf("final ReconcileRedisDriftOnce tick %d: %v", tick+1, err)
		}
	}

	finalV, err := pg.GetVoucherByID(context.Background(), vid)
	if err != nil {
		t.Fatal(err)
	}
	finalWantQ := int64(finalV.Quota - finalV.UsedCount)
	finalWantB := finalV.BudgetVND - finalV.UsedBudgetVND
	finalRemQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid))
	finalRemB, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid))
	if finalWantQ != 0 || finalRemQ != finalWantQ || finalRemB != finalWantB {
		t.Fatalf("final Redis != PG after drain & self-heal: Redis=(remQ=%d remB=%d) PG=(wantQ=%d wantB=%d quota=%d used=%d)",
			finalRemQ, finalRemB, finalWantQ, finalWantB, finalV.Quota, finalV.UsedCount)
	}
}

// TestService_W3A_R2_Point2_CommitRejectionSelfHealsInflatedRedis proves that if Redis counters are higher
// than PG (e.g., from an in-flight reservation overlapping an absolute SET), the 1st ReservePromotion is
// rejected by PG and immediately self-heals Redis back to exact PG so the 2nd call is blocked BEFORE PG!
func TestService_W3A_R2_Point2_CommitRejectionSelfHealsInflatedRedis(t *testing.T) {
	runSuite := func(t *testing.T, base store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)
		hook := &r2VoucherHookStore{Store: base}

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "r2-p2-shop-" + suffix
		skuF := "r2-p2-skuf-" + suffix

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(hook, eng, logger.New("r2-p2", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		adminCtx := ctxWithRole("admin-p2", "admin", "")
		sellerCtx := ctxWithRole("seller-p2", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		// A. Voucher: quota=1, inflate Redis remaining to 5 (Scenario B) while a concurrent commit
		// exhausts PG quota=1 right before hit1 enters CommitReservation -> hit1 passes calculator & Lua,
		// gets rejected by PG CommitReservation (VOUCHER_UNAVAILABLE_ON_COMMIT), and MUST self-heal Redis to 0!
		vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
			Code: "R2P2V" + suffix, Name: "P2 Voucher", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shopID,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 1, PerUserLimit: 5, BudgetVnd: 20000,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vResp.Voucher.Id

		// Force Redis higher than PG (remaining=5, budget=100000) and exhaust PG quota concurrently in onBeforeCommit.
		_ = mr.Set(lua.VoucherRemainingKey(vid), "5")
		_ = mr.Set(lua.VoucherBudgetRemainingKey(vid), "100000")
		hit1Ord := "r2-p2-v-hit1-" + suffix
		hook.onBeforeCommit = func(res domain.PromotionReservation) {
			if res.OrderID != hit1Ord {
				return
			}
			hook.onBeforeCommit = nil
			if _, _, cErr := base.CommitReservation(context.Background(), domain.PromotionReservation{
				OrderID: "r2-p2-v-init-" + suffix, UserID: "buyer-p2-1-" + suffix,
				Vouchers: []domain.ReservedVoucher{{VoucherID: vid, Scope: domain.VoucherScopeShop, ShopID: shopID, DiscountVND: 20000, HasBudget: true}},
			}, now); cErr != nil {
				t.Errorf("concurrent PG quota exhaust commit: %v", cErr)
			}
		}
		hook.commitCalls.Store(0)

		// 1st ReservePromotion: passes calculator & Lua, rejected by PG CommitReservation -> MUST self-heal Redis to 0!
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: hit1Ord, UserId: "buyer-p2-2-" + suffix,
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 100000}},
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "VOUCHER_UNAVAILABLE_ON_COMMIT") {
			t.Fatalf("1st inflated voucher reserve: want VOUCHER_UNAVAILABLE_ON_COMMIT, got %v", err)
		}
		if hook.commitCalls.Load() != 1 {
			t.Fatalf("expected 1 CommitReservation call on 1st attempt, got %d", hook.commitCalls.Load())
		}
		if remQ, _, _ := eng.GetInt64(context.Background(), lua.VoucherRemainingKey(vid)); remQ != 0 {
			t.Fatalf("expected Redis voucher remaining self-healed to 0 after PG reject, got %d", remQ)
		}
		if remB, _, _ := eng.GetInt64(context.Background(), lua.VoucherBudgetRemainingKey(vid)); remB != 0 {
			t.Fatalf("expected Redis voucher budget self-healed to 0 after PG reject, got %d", remB)
		}
		// Direct Lua check confirms VOUCHER_OUT_OF_QUOTA immediately!
		_, luaErr := eng.Reserve(context.Background(), lua.ReserveInput{
			OrderID:  "r2-p2-v-lua-" + suffix,
			UserID:   "buyer-p2-3-" + suffix,
			Vouchers: []lua.VoucherReserveSpec{{VoucherID: vid, PerUserLimit: 5, DiscountVND: 20000, HasBudget: true}},
		})
		var resErr *lua.ReservationError
		if !errors.As(luaErr, &resErr) || resErr.Code != lua.ErrCodeVoucherOutOfQuota {
			t.Fatalf("expected Lua VOUCHER_OUT_OF_QUOTA after self-heal, got %v", luaErr)
		}
		// 2nd ReservePromotion MUST be rejected BEFORE PG CommitReservation with old calculator error (0 new DB calls)!
		hook.commitCalls.Store(0)
		_, errV2 := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r2-p2-v-hit2-" + suffix, UserId: "buyer-p2-3-" + suffix,
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems:      []*promotionv1.ShopDiscountInput{{ShopId: shopID, SubtotalVnd: 100000}},
		})
		if status.Code(errV2) != codes.FailedPrecondition || !strings.Contains(errV2.Error(), "out of quota") {
			t.Fatalf("2nd voucher reserve after self-heal: want FailedPrecondition out of quota before PG, got %v", errV2)
		}
		if hook.commitCalls.Load() != 0 {
			t.Fatalf("2nd voucher reserve still reached PG CommitReservation (%d calls) — Redis was not healed!", hook.commitCalls.Load())
		}

		// B. Flash item: total_slots=1, consume 1 -> PG remaining_slots=0. Inflate Redis remaining to 5!
		fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "P2 Flash " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		fiResp, err := svc.RegisterFlashSaleItem(sellerCtx, &promotionv1.RegisterFlashSaleItemRequest{
			FlashSaleId: fsResp.FlashSale.Id, ShopId: shopID, SkuId: skuF, FlashPriceVnd: 120000, TotalSlots: 1, PerUserLimit: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ApproveFlashSaleItem(adminCtx, &promotionv1.ApproveFlashSaleItemRequest{ItemId: fiResp.Item.Id, Approve: true}); err != nil {
			t.Fatal(err)
		}
		fid := fiResp.Item.Id

		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r2-p2-f-init-" + suffix, UserId: "buyer-p2-1-" + suffix,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		}); err != nil {
			t.Fatal(err)
		}

		// Force Redis higher than PG (5 slots while PG has 0).
		_ = mr.Set(lua.FlashRemainingKey(fid), "5")
		hook.commitCalls.Store(0)

		// 1st ReservePromotion: passes Lua, rejected by PG -> MUST self-heal Redis flash remaining to 0!
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r2-p2-f-hit1-" + suffix, UserId: "buyer-p2-2-" + suffix,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "FLASH_UNAVAILABLE_ON_COMMIT") {
			t.Fatalf("1st inflated flash reserve: want FLASH_UNAVAILABLE_ON_COMMIT, got %v", err)
		}
		if remF, _, _ := eng.GetInt64(context.Background(), lua.FlashRemainingKey(fid)); remF != 0 {
			t.Fatalf("expected Redis flash remaining self-healed to 0 after PG reject, got %d", remF)
		}

		// 2nd ReservePromotion: blocked immediately by Lua with ResourceExhausted / FLASH_SOLD_OUT (0 PG calls)!
		hook.commitCalls.Store(0)
		_, err2 := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r2-p2-f-hit2-" + suffix, UserId: "buyer-p2-3-" + suffix,
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items:  []*promotionv1.DiscountLineItem{{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid}},
			}},
		})
		if status.Code(err2) != codes.ResourceExhausted || !strings.Contains(err2.Error(), "FLASH_SOLD_OUT") {
			t.Fatalf("2nd flash reserve after self-heal: want ResourceExhausted FLASH_SOLD_OUT from Lua, got %v", err2)
		}
		if hook.commitCalls.Load() != 0 {
			t.Fatalf("2nd flash reserve still reached PG CommitReservation (%d calls) — Redis was not healed!", hook.commitCalls.Load())
		}
	}

	t.Run("MemoryStore", func(t *testing.T) {
		runSuite(t, store.NewMemoryStore())
	})
	t.Run("PostgresStore", func(t *testing.T) {
		runSuite(t, openR2PostgresStore(t))
	})
}

// TestService_W3A_R2_Point3_StartupReconcileDeletesZeroUserUsageKeys proves that when a buyer's
// reservation was released in PG (promotion_user_usage.used_qty == 0) while a stale Redis :user: key
// remained at 1, SyncRedisFromStore deletes the stale Redis :user: keys so the buyer is no longer blocked.
func TestService_W3A_R2_Point3_StartupReconcileDeletesZeroUserUsageKeys(t *testing.T) {
	runSuite := func(t *testing.T, st store.Store) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		eng := lua.NewEngine(rdb)

		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		shopID := "r2-p3-shop-" + suffix
		skuF := "r2-p3-skuf-" + suffix
		skuP := "r2-p3-skup-" + suffix
		buyerID := "r2-p3-buyer-" + suffix

		pc := newFakeProductClient()
		pc.setSKU(&productv1.SKU{Id: skuF, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})
		pc.setSKU(&productv1.SKU{Id: skuP, ShopId: shopID, CategoryId: "cat-1", PriceVnd: 200000, MinPrice_30DVnd: 200000, AvailableStock: 20, IsActive: true})

		svc := service.NewPromotionService(st, eng, logger.New("r2-p3", logger.WithWriter(io.Discard)))
		svc.SetProductClient(pc)
		svc.SetClock(func() time.Time { return now })

		adminCtx := ctxWithRole("admin-p3", "admin", "")
		sellerCtx := ctxWithRole("seller-p3", "seller", shopID)
		orderSvcCtx := ctxWithService("order-service")

		vResp, err := svc.CreateVoucher(sellerCtx, &promotionv1.CreateVoucherRequest{
			Code: "R2P3V" + suffix, Name: "P3 Voucher", Scope: promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP, ShopId: shopID,
			DiscountType: promotionv1.DiscountType_DISCOUNT_TYPE_FIXED, ValueVnd: 20000,
			Quota: 5, PerUserLimit: 1,
			StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(24 * time.Hour)),
		})
		if err != nil {
			t.Fatal(err)
		}
		vid := vResp.Voucher.Id

		fsResp, err := svc.CreateFlashSale(adminCtx, &promotionv1.CreateFlashSaleRequest{
			Name: "P3 Flash " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
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

		progResp, err := svc.CreateShopDiscountProgram(sellerCtx, &promotionv1.CreateShopDiscountProgramRequest{
			Name: "P3 Prog " + suffix, StartsAt: timestamppb.New(now.Add(-time.Hour)), EndsAt: timestamppb.New(now.Add(2 * time.Hour)),
			Items: []*promotionv1.ShopDiscountProgramItemInput{{SkuId: skuP, DiscountedPriceVnd: 150000, PerUserLimit: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		pid := progResp.Program.Items[0].Id

		// Reserve all 3 (per_user_limit=1 each), then release in PG (used_qty becomes 0 in PG!).
		ordID := "r2-p3-ord1-" + suffix
		if _, err := svc.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: ordID, UserId: buyerID,
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid},
					{SkuId: skuP, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: pid},
				},
			}},
		}); err != nil {
			t.Fatalf("initial reserve: %v", err)
		}
		if _, err := svc.ReleasePromotion(orderSvcCtx, &promotionv1.ReleasePromotionRequest{OrderId: ordID}); err != nil {
			t.Fatalf("ReleasePromotion: %v", err)
		}

		// ReleasePromotion already deleted all 3 :user: keys immediately.
		for name, key := range map[string]string{
			"voucher":      lua.VoucherUserKey(vid, buyerID),
			"flash":        lua.FlashUserKey(fid, buyerID),
			"shop_program": lua.ShopProgramUserKey(pid, buyerID),
		} {
			if val, exists, _ := eng.GetInt64(context.Background(), key); exists {
				t.Fatalf("%s :user: key must be deleted immediately by ReleasePromotion, got val=%d", name, val)
			}
		}

		// Under D12, any :user: key written by the service carries 120s TTL so stale keys self-expire instead of locking buyers out.
		if err := eng.ReconcileUserUsageKeys(context.Background(), []domain.UserUsage{
			{Kind: domain.UsageKindVoucher, TargetID: vid, UserID: buyerID, UsedQty: 1},
			{Kind: domain.UsageKindFlash, TargetID: fid, UserID: buyerID, UsedQty: 1},
			{Kind: domain.UsageKindShopProgram, TargetID: pid, UserID: buyerID, UsedQty: 1},
		}); err != nil {
			t.Fatalf("seed TTL :user: keys: %v", err)
		}
		mr.FastForward(121 * time.Second)

		restarted := service.NewPromotionService(st, eng, logger.New("r2-p3-restart", logger.WithWriter(io.Discard)))
		restarted.SetProductClient(pc)
		restarted.SetClock(func() time.Time { return now })
		if err := restarted.SyncRedisFromStore(context.Background()); err != nil {
			t.Fatalf("SyncRedisFromStore: %v", err)
		}

		for name, key := range map[string]string{
			"voucher":      lua.VoucherUserKey(vid, buyerID),
			"flash":        lua.FlashUserKey(fid, buyerID),
			"shop_program": lua.ShopProgramUserKey(pid, buyerID),
		} {
			if val, exists, _ := eng.GetInt64(context.Background(), key); exists {
				t.Fatalf("stale %s :user: key must expire after 120s TTL, still exists with val=%d", name, val)
			}
		}

		// Buyer can now reserve again without being permanently blocked by stale Redis :user: keys!
		res2, err := restarted.ReservePromotion(orderSvcCtx, &promotionv1.ReservePromotionRequest{
			OrderId: "r2-p3-ord2-" + suffix, UserId: buyerID,
			ShopVoucherIds: map[string]string{shopID: vid},
			ShopItems: []*promotionv1.ShopDiscountInput{{
				ShopId: shopID,
				Items: []*promotionv1.DiscountLineItem{
					{SkuId: skuF, Quantity: 1, UnitPriceVnd: 200000, FlashSaleItemId: fid},
					{SkuId: skuP, Quantity: 1, UnitPriceVnd: 200000, ShopDiscountProgramItemId: pid},
				},
			}},
		})
		if err != nil || !res2.GetReserved() || res2.GetBreakdown().GetTotalShopProgramDiscountVnd() != 50000 {
			t.Fatalf("buyer should be unblocked after startup reconcile: resp=%+v err=%v", res2, err)
		}
	}

	t.Run("MemoryStore", func(t *testing.T) {
		runSuite(t, store.NewMemoryStore())
	})
	t.Run("PostgresStore", func(t *testing.T) {
		runSuite(t, openR2PostgresStore(t))
	})
}
