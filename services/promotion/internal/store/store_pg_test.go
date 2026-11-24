package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexusmart/core/services/promotion/internal/domain"
	"github.com/nexusmart/core/services/promotion/internal/store"
)

func openTestPGStore(t *testing.T) (*store.PostgresStore, *pgxpool.Pool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_PROMOTION_DB_URL"))
	if dsn == "" {
		t.Skip("set TEST_PROMOTION_DB_URL to run real Postgres tests")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	st := store.NewPostgresStore(pool)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (1st): %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (2nd idempotency): %v", err)
	}
	return st, pool
}

func TestPGStore_CommitReservation_FinalGatesAndTombstone(t *testing.T) {
	st, _ := openTestPGStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

	suffix := uuid.NewString()[:8]
	buyerID := "pg-buyer-" + suffix
	shopID := "pg-shop-" + suffix

	// Seed coins for buyer
	if _, _, err := st.CreditCoins(ctx, buyerID, 50000, "test_seed", "seed-"+suffix); err != nil {
		t.Fatalf("CreditCoins: %v", err)
	}

	// Seed voucher (quota=1, budget=25000)
	v, err := st.CreateVoucher(ctx, domain.Voucher{
		Code:         "PGV_" + suffix,
		Name:         "PG Gate Voucher",
		Scope:        domain.VoucherScopeShop,
		ShopID:       shopID,
		DiscountType: domain.DiscountTypeFixed,
		ValueVND:     20000,
		Quota:        1,
		PerUserLimit: 1,
		BudgetVND:    25000,
		StartsAt:     now.Add(-time.Hour),
		EndsAt:       now.Add(2 * time.Hour),
		IsActive:     true,
		Status:       domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}

	// Seed flash sale & approved item (total_slots=2)
	fs, err := st.CreateFlashSale(ctx, domain.FlashSale{
		Name:     "PG Flash " + suffix,
		StartsAt: now.Add(-time.Hour),
		EndsAt:   now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}
	fi, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID:      fs.ID,
		ShopID:           shopID,
		SKUID:            "pg-sku-flash-" + suffix,
		OriginalPriceVND: 200000,
		FlashPriceVND:    120000,
		TotalSlots:       2,
		PerUserLimit:     2,
	}, now)
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem: %v", err)
	}
	if _, _, err := st.ApproveFlashSaleItem(ctx, fi.ID, "admin-1", true, "", 200000, now); err != nil {
		t.Fatalf("ApproveFlashSaleItem: %v", err)
	}

	// Seed active shop discount program
	prog, err := st.CreateShopDiscountProgram(ctx, domain.ShopDiscountProgram{
		ShopID:   shopID,
		Name:     "PG Prog " + suffix,
		StartsAt: now.Add(-time.Hour),
		EndsAt:   now.Add(2 * time.Hour),
		Items: []domain.ShopDiscountProgramItem{
			{SKUID: "pg-sku-prog-" + suffix, OriginalPriceVND: 100000, DiscountedPriceVND: 80000, DiscountPercent: 20, PerUserLimit: 2},
		},
	}, now)
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram: %v", err)
	}
	progItem := prog.Items[0]

	// 1. Tombstone: ReleaseReservation BEFORE CommitReservation writes RELEASED row -> late Commit fails with ErrReservationAlreadyReleased
	tombOrdID := "pg-tomb-" + suffix
	relTomb, err := st.ReleaseReservation(ctx, tombOrdID)
	if err != nil || !relTomb.AlreadyReleased {
		t.Fatalf("ReleaseReservation(missing) expected already=true err=nil, got res=%+v err=%v", relTomb, err)
	}
	tombRow, err := st.GetReservation(ctx, tombOrdID)
	if err != nil || tombRow.Status != domain.ReservationStatusReleased {
		t.Fatalf("expected RELEASED tombstone row in PG, got %+v err=%v", tombRow, err)
	}
	_, _, err = st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:          tombOrdID,
		UserID:           buyerID,
		CoinsUsed:        1000,
		Vouchers:         []domain.ReservedVoucher{{VoucherID: v.ID, Scope: v.Scope, ShopID: shopID, DiscountVND: 20000, HasBudget: true}},
		FlashSaleItems:   []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 1, ShopID: shopID}},
		ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: progItem.ID, SKUID: progItem.SKUID, Quantity: 1, ShopID: shopID}},
	}, now)
	if !errors.Is(err, store.ErrReservationAlreadyReleased) {
		t.Fatalf("late CommitReservation after tombstone: want ErrReservationAlreadyReleased, got %v", err)
	}
	// Verify nothing mutated on tombstone rejection
	bal, _, _ := st.GetCoinBalance(ctx, buyerID, 5)
	vAfter, _ := st.GetVoucherByID(ctx, v.ID)
	fiAfter, _ := st.GetFlashSaleItem(ctx, fi.ID)
	if bal != 50000 || vAfter.UsedCount != 0 || fiAfter.RemainingSlots != 2 {
		t.Fatalf("tombstone rejection mutated state: bal=%d used=%d slots=%d", bal, vAfter.UsedCount, fiAfter.RemainingSlots)
	}

	// 2. Concurrent race between ReleaseReservation and CommitReservation on same order_id: never leaves RESERVED + lost counters
	for i := 0; i < 6; i++ {
		raceOrdID := fmt.Sprintf("pg-race-rel-%s-%d", suffix, i)
		var wg sync.WaitGroup
		start := make(chan struct{})
		var commitErr error
		var releaseErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _, commitErr = st.CommitReservation(ctx, domain.PromotionReservation{
				OrderID:        raceOrdID,
				UserID:         buyerID,
				CoinsUsed:      500,
				FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 1, ShopID: shopID}},
			}, now)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, releaseErr = st.ReleaseReservation(ctx, raceOrdID)
		}()
		close(start)
		wg.Wait()
		if releaseErr != nil {
			t.Fatalf("race iter %d ReleaseReservation error: %v", i, releaseErr)
		}
		if commitErr != nil && !errors.Is(commitErr, store.ErrReservationAlreadyReleased) {
			t.Fatalf("race iter %d unexpected CommitReservation error: %v", i, commitErr)
		}
		row, getErr := st.GetReservation(ctx, raceOrdID)
		if getErr != nil {
			t.Fatalf("race iter %d GetReservation: %v", i, getErr)
		}
		// If commit won before release, release subsequently released it; if release won first, commit got ErrReservationAlreadyReleased.
		// Either way, if commit succeeded, row may still be RESERVED only if release finished BEFORE commit -> impossible with tombstone!
		if row.Status != domain.ReservationStatusReleased {
			t.Fatalf("race iter %d: after both commit & release completed, status MUST be RELEASED, got %s (commitErr=%v)", i, row.Status, commitErr)
		}
	}
	bal, _, _ = st.GetCoinBalance(ctx, buyerID, 5)
	fiAfter, _ = st.GetFlashSaleItem(ctx, fi.ID)
	if bal != 50000 || fiAfter.RemainingSlots != 2 {
		t.Fatalf("race iterations leaked state: bal=%d slots=%d", bal, fiAfter.RemainingSlots)
	}

	// 3. Voucher PAUSED between Lua and CommitReservation -> fails & rolls back coins/flash!
	if _, err := st.SetVoucherStatus(ctx, v.ID, domain.VoucherStatusPaused, nil); err != nil {
		t.Fatalf("SetVoucherStatus(PAUSED): %v", err)
	}
	_, _, err = st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:        "pg-paused-v-" + suffix,
		UserID:         buyerID,
		CoinsUsed:      2000,
		Vouchers:       []domain.ReservedVoucher{{VoucherID: v.ID, Scope: v.Scope, ShopID: shopID, DiscountVND: 20000, HasBudget: true}},
		FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 1, ShopID: shopID}},
	}, now)
	if !errors.Is(err, store.ErrVoucherNotEligibleForCommit) {
		t.Fatalf("CommitReservation on PAUSED voucher: want ErrVoucherNotEligibleForCommit, got %v", err)
	}
	bal, _, _ = st.GetCoinBalance(ctx, buyerID, 5)
	fiAfter, _ = st.GetFlashSaleItem(ctx, fi.ID)
	if bal != 50000 || fiAfter.RemainingSlots != 2 {
		t.Fatalf("PAUSED voucher rollback failed: bal=%d slots=%d", bal, fiAfter.RemainingSlots)
	}

	// Reactivate voucher -> over-budget commit rejected -> valid commit succeeds -> 2nd commit hits quota=1 and rolls back!
	if _, err := st.SetVoucherStatus(ctx, v.ID, domain.VoucherStatusActive, nil); err != nil {
		t.Fatalf("SetVoucherStatus(ACTIVE): %v", err)
	}
	_, _, err = st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:  "pg-overbudget-" + suffix,
		UserID:   buyerID,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: v.Scope, ShopID: shopID, DiscountVND: 30000, HasBudget: true}},
	}, now)
	if !errors.Is(err, store.ErrVoucherNotEligibleForCommit) {
		t.Fatalf("CommitReservation over budget: want ErrVoucherNotEligibleForCommit, got %v", err)
	}
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:          "pg-valid-1-" + suffix,
		UserID:           buyerID,
		CoinsUsed:        1000,
		Vouchers:         []domain.ReservedVoucher{{VoucherID: v.ID, Scope: v.Scope, ShopID: shopID, DiscountVND: 20000, HasBudget: true}},
		FlashSaleItems:   []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 1, ShopID: shopID}},
		ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: progItem.ID, SKUID: progItem.SKUID, Quantity: 1, ShopID: shopID}},
	}, now); err != nil {
		t.Fatalf("valid CommitReservation: %v", err)
	}
	_, _, err = st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:   "pg-overquota-" + suffix,
		UserID:    buyerID,
		CoinsUsed: 1000,
		Vouchers:  []domain.ReservedVoucher{{VoucherID: v.ID, Scope: v.Scope, ShopID: shopID, DiscountVND: 20000, HasBudget: true}},
	}, now)
	if !errors.Is(err, store.ErrVoucherNotEligibleForCommit) {
		t.Fatalf("CommitReservation over quota: want ErrVoucherNotEligibleForCommit, got %v", err)
	}

	// 4. Flash remaining_slots < qty (remaining=1, request qty=2) -> ErrFlashNotEligibleForCommit (NO GREATEST clamp!)
	_, _, err = st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:        "pg-flash-oversell-" + suffix,
		UserID:         buyerID,
		CoinsUsed:      1000,
		FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 2, ShopID: shopID}},
	}, now)
	if !errors.Is(err, store.ErrFlashNotEligibleForCommit) {
		t.Fatalf("CommitReservation flash oversell: want ErrFlashNotEligibleForCommit, got %v", err)
	}
	fiAfter, _ = st.GetFlashSaleItem(ctx, fi.ID)
	if fiAfter.RemainingSlots != 1 {
		t.Fatalf("flash remaining_slots should remain 1 after rollback, got %d", fiAfter.RemainingSlots)
	}

	// 5. Shop discount program ENDED between Lua and CommitReservation -> ErrShopProgramNotEligibleForCommit + rollback!
	if _, err := st.EndShopDiscountProgram(ctx, prog.ID, now, nil); err != nil {
		t.Fatalf("EndShopDiscountProgram: %v", err)
	}
	_, _, err = st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:          "pg-prog-ended-" + suffix,
		UserID:           buyerID,
		CoinsUsed:        1000,
		FlashSaleItems:   []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 1, ShopID: shopID}},
		ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: progItem.ID, SKUID: progItem.SKUID, Quantity: 1, ShopID: shopID}},
	}, now)
	if !errors.Is(err, store.ErrShopProgramNotEligibleForCommit) {
		t.Fatalf("CommitReservation on ENDED shop program: want ErrShopProgramNotEligibleForCommit, got %v", err)
	}
	bal, _, _ = st.GetCoinBalance(ctx, buyerID, 5)
	fiAfter, _ = st.GetFlashSaleItem(ctx, fi.ID)
	if bal != 49000 || fiAfter.RemainingSlots != 1 {
		t.Fatalf("ENDED shop program rollback leaked coins/flash: bal=%d slots=%d", bal, fiAfter.RemainingSlots)
	}
}

func TestPGStore_FlashStateMachine_StableProgramItemID_AndExpiryBudgetSkip(t *testing.T) {
	st, _ := openTestPGStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	suffix := uuid.NewString()[:8]
	shopID := "pg-sm-shop-" + suffix

	// 1. Flash state machine: PENDING -> APPROVED (idempotent replay OK; APPROVED -> REJECTED ErrInvalidFlashTransition)
	fs, err := st.CreateFlashSale(ctx, domain.FlashSale{
		Name: "PG SM Flash " + suffix, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fi1, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fs.ID, ShopID: shopID, SKUID: "pg-sm-sku1-" + suffix,
		OriginalPriceVND: 200000, FlashPriceVND: 120000, TotalSlots: 5, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	fi2, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fs.ID, ShopID: shopID, SKUID: "pg-sm-sku2-" + suffix,
		OriginalPriceVND: 200000, FlashPriceVND: 120000, TotalSlots: 5, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, prev, err := st.ApproveFlashSaleItem(ctx, fi1.ID, "admin-1", true, "", 200000, now); err != nil || prev != domain.FlashStatusPending {
		t.Fatalf("PENDING->APPROVED failed: prev=%s err=%v", prev, err)
	}
	if _, prev, err := st.ApproveFlashSaleItem(ctx, fi1.ID, "admin-1", true, "", 200000, now); err != nil || prev != domain.FlashStatusApproved {
		t.Fatalf("idempotent APPROVED->APPROVED failed: prev=%s err=%v", prev, err)
	}
	if _, _, err := st.ApproveFlashSaleItem(ctx, fi1.ID, "admin-1", false, "nope", 0, now); !errors.Is(err, store.ErrInvalidFlashTransition) {
		t.Fatalf("APPROVED->REJECTED: want ErrInvalidFlashTransition, got %v", err)
	}

	if _, prev, err := st.ApproveFlashSaleItem(ctx, fi2.ID, "admin-1", false, "bad", 0, now); err != nil || prev != domain.FlashStatusPending {
		t.Fatalf("PENDING->REJECTED failed: prev=%s err=%v", prev, err)
	}
	if _, prev, err := st.ApproveFlashSaleItem(ctx, fi2.ID, "admin-1", false, "bad", 0, now); err != nil || prev != domain.FlashStatusRejected {
		t.Fatalf("idempotent REJECTED->REJECTED failed: prev=%s err=%v", prev, err)
	}
	if _, _, err := st.ApproveFlashSaleItem(ctx, fi2.ID, "admin-1", true, "", 200000, now); !errors.Is(err, store.ErrInvalidFlashTransition) {
		t.Fatalf("REJECTED->APPROVED: want ErrInvalidFlashTransition, got %v", err)
	}

	// 2. Stable UUIDv5 shop discount program item ID across remove + re-add SKU
	skuStable := "pg-stable-sku-" + suffix
	skuOther := "pg-other-sku-" + suffix
	prog, err := st.CreateShopDiscountProgram(ctx, domain.ShopDiscountProgram{
		ShopID: shopID, Name: "PG Stable " + suffix, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
		Items: []domain.ShopDiscountProgramItem{
			{SKUID: skuStable, OriginalPriceVND: 100000, DiscountedPriceVND: 80000, DiscountPercent: 20, PerUserLimit: 1},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	origItemID := prog.Items[0].ID

	// Remove skuStable (replace with skuOther)
	if _, err := st.UpdateShopDiscountProgram(ctx, prog.ID, now, func(cur *domain.ShopDiscountProgram) (bool, error) {
		cur.Items = []domain.ShopDiscountProgramItem{
			{SKUID: skuOther, OriginalPriceVND: 100000, DiscountedPriceVND: 85000, DiscountPercent: 15, PerUserLimit: 1},
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	// Re-add skuStable -> MUST receive exact same ID!
	reAdded, err := st.UpdateShopDiscountProgram(ctx, prog.ID, now, func(cur *domain.ShopDiscountProgram) (bool, error) {
		cur.Items = []domain.ShopDiscountProgramItem{
			{SKUID: skuStable, OriginalPriceVND: 100000, DiscountedPriceVND: 80000, DiscountPercent: 20, PerUserLimit: 1},
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reAdded.Items) != 1 || reAdded.Items[0].ID != origItemID {
		t.Fatalf("re-added SKU item ID drifted: got %v want %s", reAdded.Items, origItemID)
	}

	// 3. ProcessExpiringSavedVouchers skips budget-exhausted vouchers even when used_count < quota
	vBud, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGBUD_" + suffix, Name: "PG Budget Exhausted", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 20000,
		Quota: 10, PerUserLimit: 1, BudgetVND: 20000,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(6 * time.Hour),
		IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveUserVoucher(ctx, "pg-exp-user-"+suffix, vBud.ID); err != nil {
		t.Fatal(err)
	}
	// Consume full 20,000 budget (used_count=1 < quota=10, used_budget_vnd=20000 == budget_vnd)
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:  "pg-bud-consume-" + suffix,
		UserID:   "pg-other-" + suffix,
		Vouchers: []domain.ReservedVoucher{{VoucherID: vBud.ID, Scope: vBud.Scope, DiscountVND: 20000, HasBudget: true}},
	}, now); err != nil {
		t.Fatal(err)
	}
	var notified []string
	sent, _, err := st.ProcessExpiringSavedVouchers(ctx, now, 24*time.Hour, 50, func(_ context.Context, it domain.ExpiringSavedVoucher) error {
		if it.VoucherID == vBud.ID {
			notified = append(notified, it.UserID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ProcessExpiringSavedVouchers: %v", err)
	}
	if len(notified) != 0 {
		t.Fatalf("budget-exhausted voucher %s should NOT notify, got notified=%v (total sent=%d)", vBud.ID, notified, sent)
	}
}

func TestPGStore_CommitReservation_ConcurrentUpdateShopDiscountProgram_NoDeadlock(t *testing.T) {
	st, _ := openTestPGStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	suffix := uuid.NewString()[:8]
	shopID := "pg-dl-shop-" + suffix

	// Seed 2 active shop discount programs with multiple SKUs so CommitReservation and
	// UpdateShopDiscountProgram(replace_items=true) contend heavily on both sdp and sdi rows.
	skuA := "pg-dl-sku-a-" + suffix
	skuB := "pg-dl-sku-b-" + suffix
	skuC := "pg-dl-sku-c-" + suffix
	skuD := "pg-dl-sku-d-" + suffix

	prog1, err := st.CreateShopDiscountProgram(ctx, domain.ShopDiscountProgram{
		ShopID: shopID, Name: "PG DL Prog1 " + suffix,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
		Items: []domain.ShopDiscountProgramItem{
			{SKUID: skuA, OriginalPriceVND: 200000, DiscountedPriceVND: 160000, DiscountPercent: 20, PerUserLimit: 100},
			{SKUID: skuB, OriginalPriceVND: 150000, DiscountedPriceVND: 120000, DiscountPercent: 20, PerUserLimit: 100},
		},
	}, now)
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram prog1: %v", err)
	}
	prog2, err := st.CreateShopDiscountProgram(ctx, domain.ShopDiscountProgram{
		ShopID: shopID, Name: "PG DL Prog2 " + suffix,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
		Items: []domain.ShopDiscountProgramItem{
			{SKUID: skuC, OriginalPriceVND: 180000, DiscountedPriceVND: 144000, DiscountPercent: 20, PerUserLimit: 100},
			{SKUID: skuD, OriginalPriceVND: 100000, DiscountedPriceVND: 80000, DiscountPercent: 20, PerUserLimit: 100},
		},
	}, now)
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram prog2: %v", err)
	}

	var itemA, itemB, itemC, itemD domain.ShopDiscountProgramItem
	for _, it := range prog1.Items {
		if it.SKUID == skuA {
			itemA = it
		} else if it.SKUID == skuB {
			itemB = it
		}
	}
	for _, it := range prog2.Items {
		if it.SKUID == skuC {
			itemC = it
		} else if it.SKUID == skuD {
			itemD = it
		}
	}

	t.Run("ConcurrentCommitVsReplaceItemsNoDeadlock", func(t *testing.T) {
		const rounds = 60
		errCh := make(chan error, rounds*8)
		var wg sync.WaitGroup
		start := make(chan struct{})

		// 4 concurrent committers reserving items across both programs in every iteration
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				<-start
				for r := 0; r < rounds; r++ {
					_, _, cErr := st.CommitReservation(ctx, domain.PromotionReservation{
						OrderID: fmt.Sprintf("pg-dl-ord-%s-w%d-r%d", suffix, worker, r),
						UserID:  fmt.Sprintf("pg-dl-buyer-%s-%d", suffix, worker),
						ShopProgramItems: []domain.ReservedShopProgramItem{
							{ShopDiscountProgramItemID: itemA.ID, SKUID: skuA, Quantity: 1, ShopID: shopID},
							{ShopDiscountProgramItemID: itemB.ID, SKUID: skuB, Quantity: 1, ShopID: shopID},
							{ShopDiscountProgramItemID: itemC.ID, SKUID: skuC, Quantity: 1, ShopID: shopID},
							{ShopDiscountProgramItemID: itemD.ID, SKUID: skuD, Quantity: 1, ShopID: shopID},
						},
					}, now)
					if cErr != nil && !errors.Is(cErr, store.ErrShopProgramNotEligibleForCommit) {
						errCh <- fmt.Errorf("commit w=%d r=%d: %w", worker, r, cErr)
					}
				}
			}(w)
		}

		// 2 concurrent updaters running replace_items=true on prog1 and prog2 (updating rows in sdp -> sdi order)
		updateProg := func(progID, s1, s2 string) {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				price1 := int64(150000 + (r%3)*1000)
				price2 := int64(110000 + (r%3)*1000)
				_, uErr := st.UpdateShopDiscountProgram(ctx, progID, now, func(cur *domain.ShopDiscountProgram) (bool, error) {
					cur.EndsAt = now.Add(time.Duration(2+r%3) * time.Hour)
					cur.Items = []domain.ShopDiscountProgramItem{
						{SKUID: s1, OriginalPriceVND: 200000, DiscountedPriceVND: price1, DiscountPercent: 20, PerUserLimit: 100},
						{SKUID: s2, OriginalPriceVND: 150000, DiscountedPriceVND: price2, DiscountPercent: 20, PerUserLimit: 100},
					}
					return true, nil
				})
				if uErr != nil {
					errCh <- fmt.Errorf("update prog=%s r=%d: %w", progID, r, uErr)
				}
			}
		}
		wg.Add(2)
		go updateProg(prog1.ID, skuA, skuB)
		go updateProg(prog2.ID, skuC, skuD)

		close(start)
		wg.Wait()
		close(errCh)

		for err := range errCh {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
				t.Fatalf("PostgreSQL deadlock (40P01) between CommitReservation and UpdateShopDiscountProgram: %v", err)
			}
			t.Fatalf("unexpected concurrent SQL error: %v", err)
		}
	})

	t.Run("NonReplaceUpdatePersistsWarning", func(t *testing.T) {
		if _, err := st.UpdateShopDiscountProgram(ctx, prog1.ID, now, func(cur *domain.ShopDiscountProgram) (bool, error) {
			matched := false
			for i := range cur.Items {
				if cur.Items[i].SKUID == skuA {
					cur.Items[i].Warning = "SKU overlaps with an approved flash sale window"
					matched = true
					break
				}
			}
			if !matched {
				return false, fmt.Errorf("sku %s not found in program items", skuA)
			}
			return false, nil
		}); err != nil {
			t.Fatalf("non-replace warning persist: %v", err)
		}
		reloaded, err := st.GetShopDiscountProgram(ctx, prog1.ID)
		if err != nil {
			t.Fatalf("GetShopDiscountProgram after non-replace update: %v", err)
		}
		foundPersistedWarn := false
		for _, it := range reloaded.Items {
			if it.SKUID == skuA && strings.Contains(it.Warning, "flash") {
				foundPersistedWarn = true
			}
		}
		if !foundPersistedWarn {
			t.Fatalf("expected persisted flash warning in Postgres after non-replace update, got %+v", reloaded.Items)
		}
	})
}

func TestPGStore_W3A_PerUserLimit_EnforcedInPostgresAndConcurrentRace(t *testing.T) {
	st, pool := openTestPGStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := uuid.NewString()[:8]

	// Re-running Migrate must stay 100% idempotent (004 backfill marker guarded).
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("idempotent re-Migrate failed: %v", err)
	}

	shopID := "pg-w3a-shop-" + suffix
	v, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGPUL" + suffix, Name: "PG PerUser", Scope: domain.VoucherScopeShop, ShopID: shopID,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 20000, Quota: 20, PerUserLimit: 1, BudgetVND: 400000,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	fs, err := st.CreateFlashSale(ctx, domain.FlashSale{
		Name: "PG PUL Flash " + suffix, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fs.ID, ShopID: shopID, SKUID: "pg-pul-fsku-" + suffix,
		OriginalPriceVND: 200000, FlashPriceVND: 120000, TotalSlots: 20, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ApproveFlashSaleItem(ctx, fi.ID, "admin-pg", true, "", 200000, now); err != nil {
		t.Fatal(err)
	}
	prog, err := st.CreateShopDiscountProgram(ctx, domain.ShopDiscountProgram{
		ShopID: shopID, Name: "PG PUL Prog " + suffix,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
		Items: []domain.ShopDiscountProgramItem{
			{SKUID: "pg-pul-psku-" + suffix, OriginalPriceVND: 200000, DiscountedPriceVND: 150000, DiscountPercent: 25, PerUserLimit: 2},
		},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	progItemID := prog.Items[0].ID

	// 1. Sequential per_user_limit checks on real Postgres (voucher, flash, shop program) + release restore.
	buyer1 := "pg-pul-b1-" + suffix
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-pul-v-1-" + suffix, UserID: buyer1,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopeShop, ShopID: shopID, DiscountVND: 20000, HasBudget: true, RulesVersion: 1}},
	}, now); err != nil {
		t.Fatalf("voucher 1st commit: %v", err)
	}
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-pul-v-2-" + suffix, UserID: buyer1,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopeShop, ShopID: shopID, DiscountVND: 20000, HasBudget: true, RulesVersion: 1}},
	}, now); !errors.Is(err, store.ErrVoucherUserLimitExceeded) {
		t.Fatalf("voucher 2nd commit over per_user_limit: want ErrVoucherUserLimitExceeded, got %v", err)
	}
	if gotV, _ := st.GetVoucherByID(ctx, v.ID); gotV.UsedCount != 1 {
		t.Fatalf("failed voucher commit mutated used_count: got %d want 1", gotV.UsedCount)
	}
	if _, err := st.ReleaseReservation(ctx, "pg-pul-v-1-"+suffix); err != nil {
		t.Fatalf("ReleaseReservation voucher: %v", err)
	}
	if qty, err := pgUserUsageQty(ctx, st, domain.UsageKindVoucher, v.ID, buyer1); err != nil || qty != 0 {
		t.Fatalf("expected voucher user usage restored to 0 after release, got %d err=%v", qty, err)
	}

	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-pul-f-1-" + suffix, UserID: buyer1,
		FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 2, ShopID: shopID}},
	}, now); err != nil {
		t.Fatalf("flash 1st commit (qty=2): %v", err)
	}
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-pul-f-2-" + suffix, UserID: buyer1,
		FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 1, ShopID: shopID}},
	}, now); !errors.Is(err, store.ErrFlashUserLimitExceeded) {
		t.Fatalf("flash 2nd commit over per_user_limit: want ErrFlashUserLimitExceeded, got %v", err)
	}
	if gotFI, _ := st.GetFlashSaleItem(ctx, fi.ID); gotFI.RemainingSlots != 18 {
		t.Fatalf("failed flash commit mutated remaining_slots: got %d want 18", gotFI.RemainingSlots)
	}

	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-pul-p-1-" + suffix, UserID: buyer1,
		ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: progItemID, SKUID: prog.Items[0].SKUID, Quantity: 2, ShopID: shopID}},
	}, now); err != nil {
		t.Fatalf("shop prog 1st commit (qty=2): %v", err)
	}
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-pul-p-2-" + suffix, UserID: buyer1,
		ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: progItemID, SKUID: prog.Items[0].SKUID, Quantity: 1, ShopID: shopID}},
	}, now); !errors.Is(err, store.ErrShopProgramUserLimitExceeded) {
		t.Fatalf("shop prog 2nd commit over per_user_limit: want ErrShopProgramUserLimitExceeded, got %v", err)
	}

	// 2. Concurrent 2-goroutine race for SAME user on real Postgres across all 3 kinds -> exactly 1 wins each!
	buyerRace := "pg-pul-race-" + suffix
	runRace := func(name string, wantErr error, buildRes func(idx int) domain.PromotionReservation) {
		t.Helper()
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				<-start
				_, _, errs[idx] = st.CommitReservation(ctx, buildRes(idx), now)
			}(i)
		}
		close(start)
		wg.Wait()

		okN, limitN := 0, 0
		for _, e := range errs {
			switch {
			case e == nil:
				okN++
			case errors.Is(e, wantErr):
				limitN++
			default:
				t.Fatalf("%s race unexpected error: %v", name, e)
			}
		}
		if okN != 1 || limitN != 1 {
			t.Fatalf("%s race: want 1 success + 1 %v, got ok=%d limit=%d errs=%v", name, wantErr, okN, limitN, errs)
		}
	}

	runRace("voucher", store.ErrVoucherUserLimitExceeded, func(idx int) domain.PromotionReservation {
		return domain.PromotionReservation{
			OrderID: fmt.Sprintf("pg-race-v-%s-%d", suffix, idx), UserID: buyerRace,
			Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopeShop, ShopID: shopID, DiscountVND: 20000, HasBudget: true, RulesVersion: 1}},
		}
	})
	runRace("flash", store.ErrFlashUserLimitExceeded, func(idx int) domain.PromotionReservation {
		return domain.PromotionReservation{
			OrderID: fmt.Sprintf("pg-race-f-%s-%d", suffix, idx), UserID: buyerRace,
			FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 2, ShopID: shopID}},
		}
	})
	runRace("shop_program", store.ErrShopProgramUserLimitExceeded, func(idx int) domain.PromotionReservation {
		return domain.PromotionReservation{
			OrderID: fmt.Sprintf("pg-race-p-%s-%d", suffix, idx), UserID: buyerRace,
			ShopProgramItems: []domain.ReservedShopProgramItem{{ShopDiscountProgramItemID: progItemID, SKUID: prog.Items[0].SKUID, Quantity: 2, ShopID: shopID}},
		}
	})

	// Direct SQL check confirms persisted positive usage for buyerRace across all 3 kinds.
	allUsage := queryAllUserUsage(ctx, t, pool)
	foundKinds := make(map[string]int32)
	for _, u := range allUsage {
		if u.UserID == buyerRace {
			foundKinds[u.Kind] = u.UsedQty
		}
	}
	if foundKinds[domain.UsageKindVoucher] != 1 || foundKinds[domain.UsageKindFlash] != 2 || foundKinds[domain.UsageKindShopProgram] != 2 {
		t.Fatalf("promotion_user_usage mismatch for %s: got %+v", buyerRace, foundKinds)
	}
}

func TestPGStore_W3A_UpdateVoucher_LockActiveReservationRulesVersion_AndReconcile(t *testing.T) {
	st, _ := openTestPGStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := uuid.NewString()[:8]

	v, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGUPDR" + suffix, Name: "PG Update Race", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 20000, Quota: 10, PerUserLimit: 2, BudgetVND: 200000,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	if v.RulesVersion != 1 || v.RedisSyncVersion != 1 || v.RedisSyncedVersion != 0 {
		t.Fatalf("unexpected initial versions: rules=%d sync=%d synced=%d", v.RulesVersion, v.RedisSyncVersion, v.RedisSyncedVersion)
	}

	// SyncVoucherRedis clears initial dirty state under FOR UPDATE.
	if synced, err := st.SyncVoucherRedis(ctx, v.ID, false, nil); err != nil || !synced {
		t.Fatalf("SyncVoucherRedis: synced=%v err=%v", synced, err)
	}

	// UpdateVoucher with no active reservation: hasActiveReservation=false, bumps rules_version (1->2) & redis_sync_version (1->2).
	var sawActiveBefore bool
	after, err := st.UpdateVoucher(ctx, v.ID, func(cur *domain.Voucher, hasActiveReservation bool) error {
		sawActiveBefore = hasActiveReservation
		cur.ValueVND = 35000
		cur.RulesVersion++
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateVoucher unused: %v", err)
	}
	if sawActiveBefore || after.RulesVersion != 2 || after.RedisSyncVersion != 2 || after.RedisSyncedVersion != 1 {
		t.Fatalf("unexpected UpdateVoucher result: sawActive=%v after=%+v", sawActiveBefore, after)
	}

	// CommitReservation carrying stale RulesVersion=1 MUST fail ErrVoucherNotEligibleForCommit!
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-stale-rv-" + suffix, UserID: "pg-rv-buyer-" + suffix,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopePlatform, DiscountVND: 20000, HasBudget: true, RulesVersion: 1}},
	}, now); !errors.Is(err, store.ErrVoucherNotEligibleForCommit) {
		t.Fatalf("stale RulesVersion=1 commit: want ErrVoucherNotEligibleForCommit, got %v", err)
	}

	// CommitReservation with current RulesVersion=2 succeeds -> UpdateVoucher now sees hasActiveReservation=true!
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-cur-rv-" + suffix, UserID: "pg-rv-buyer-" + suffix,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopePlatform, DiscountVND: 35000, HasBudget: true, RulesVersion: 2}},
	}, now); err != nil {
		t.Fatalf("current RulesVersion=2 commit: %v", err)
	}
	var sawActiveAfter bool
	_, err = st.UpdateVoucher(ctx, v.ID, func(_ *domain.Voucher, hasActiveReservation bool) error {
		sawActiveAfter = hasActiveReservation
		return errors.New("abort check")
	})
	if err == nil || !sawActiveAfter {
		t.Fatalf("expected hasActiveReservation=true when RESERVED reservation exists, got saw=%v err=%v", sawActiveAfter, err)
	}

	// ReconcilePendingVouchers: failed syncFn keeps voucher dirty; succeeding syncFn reconciles and marks synced!
	simErr := errors.New("redis down")
	rec0, err := st.ReconcilePendingVouchers(ctx, 50, func(_ context.Context, cur *domain.Voucher) error {
		if cur.ID == v.ID {
			return simErr
		}
		return nil
	})
	if err == nil {
		t.Fatalf("expected error from failing syncFn, got rec=%d err=nil", rec0)
	}
	reloadedDirty, _ := st.GetVoucherByID(ctx, v.ID)
	if reloadedDirty.RedisSyncedVersion >= reloadedDirty.RedisSyncVersion {
		t.Fatalf("failed reconcile must leave voucher dirty, got synced=%d sync=%d", reloadedDirty.RedisSyncedVersion, reloadedDirty.RedisSyncVersion)
	}

	reconciledTarget := false
	if _, err := st.ReconcilePendingVouchers(ctx, 50, func(_ context.Context, cur *domain.Voucher) error {
		if cur.ID == v.ID {
			reconciledTarget = true
		}
		return nil
	}); err != nil {
		t.Fatalf("ReconcilePendingVouchers(ok): %v", err)
	}
	if !reconciledTarget {
		t.Fatalf("expected ReconcilePendingVouchers to visit dirty voucher %s", v.ID)
	}
	reloadedClean, _ := st.GetVoucherByID(ctx, v.ID)
	if reloadedClean.RedisSyncedVersion != reloadedClean.RedisSyncVersion {
		t.Fatalf("expected voucher synced after reconcile, got synced=%d sync=%d", reloadedClean.RedisSyncedVersion, reloadedClean.RedisSyncVersion)
	}
}

func TestPGStore_W3A_ExpiringSavedVouchers_SkipsPerUserLimitExhausted(t *testing.T) {
	st, _ := openTestPGStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := uuid.NewString()[:8]

	v, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGEXPUL" + suffix, Name: "PG Expiring Limit", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 15000, Quota: 20, PerUserLimit: 1,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(4 * time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	buyerExhausted := "pg-exp-ex-" + suffix
	buyerFresh := "pg-exp-ok-" + suffix
	buyerReleased := "pg-exp-rel-" + suffix
	for _, b := range []string{buyerExhausted, buyerFresh, buyerReleased} {
		if _, err := st.SaveUserVoucher(ctx, b, v.ID); err != nil {
			t.Fatalf("SaveUserVoucher(%s): %v", b, err)
		}
	}

	// buyerExhausted uses 1/1; buyerReleased uses 1/1 then releases back to 0/1; buyerFresh uses 0/1.
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-exp-ord-ex-" + suffix, UserID: buyerExhausted,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopePlatform, DiscountVND: 15000}},
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "pg-exp-ord-rel-" + suffix, UserID: buyerReleased,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopePlatform, DiscountVND: 15000}},
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReleaseReservation(ctx, "pg-exp-ord-rel-"+suffix); err != nil {
		t.Fatal(err)
	}

	notified := make(map[string]bool)
	if _, _, err := st.ProcessExpiringSavedVouchers(ctx, now, 24*time.Hour, 100, func(_ context.Context, item domain.ExpiringSavedVoucher) error {
		if item.VoucherID == v.ID {
			notified[item.UserID] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("ProcessExpiringSavedVouchers: %v", err)
	}
	if notified[buyerExhausted] {
		t.Fatalf("buyerExhausted reached per_user_limit=1 in PG and must NOT be notified")
	}
	if !notified[buyerFresh] {
		t.Fatalf("buyerFresh has remaining per_user_limit and MUST be notified")
	}
	if !notified[buyerReleased] {
		t.Fatalf("buyerReleased released their usage back to 0 and MUST be notified")
	}
}

func TestPGStore_W3A_R2_LockedSync_ZeroUsage_And_004Idempotent(t *testing.T) {
	st, pool := openTestPGStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := uuid.NewString()[:8]
	shopID := "pg-r2-shop-" + suffix

	// 1. Verify promotion_schema_markers table does NOT exist (point 4 ponytail cleanup).
	var markersExists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.promotion_schema_markers') IS NOT NULL`).Scan(&markersExists); err != nil {
		t.Fatalf("check promotion_schema_markers: %v", err)
	}
	if markersExists {
		t.Fatalf("promotion_schema_markers table must not be created once promotion_schema_migrations tracks 004")
	}

	// Seed voucher + approved flash item + 2 reservations (one released to used_qty=0, one active used_qty=1).
	v, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGR2" + suffix, Name: "PG R2 Lock", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 10000, Quota: 10, PerUserLimit: 5, BudgetVND: 100000,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateVoucher: %v", err)
	}
	fs, err := st.CreateFlashSale(ctx, domain.FlashSale{
		Name: "PG R2 Flash " + suffix, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale: %v", err)
	}
	fi, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fs.ID, ShopID: shopID, SKUID: "pg-r2-sku-" + suffix,
		OriginalPriceVND: 200000, FlashPriceVND: 120000, TotalSlots: 10, PerUserLimit: 5,
	}, now)
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem: %v", err)
	}
	if _, _, err := st.ApproveFlashSaleItem(ctx, fi.ID, "admin-r2", true, "", 200000, now); err != nil {
		t.Fatalf("ApproveFlashSaleItem: %v", err)
	}

	buyerRel := "pg-r2-rel-" + suffix
	buyerAct := "pg-r2-act-" + suffix
	ordRel := "pg-r2-ord-rel-" + suffix
	ordAct := "pg-r2-ord-act-" + suffix
	for _, tc := range []struct{ oid, uid string }{{ordRel, buyerRel}, {ordAct, buyerAct}} {
		if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
			OrderID: tc.oid, UserID: tc.uid,
			Vouchers:       []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopePlatform, DiscountVND: 10000, HasBudget: true}},
			FlashSaleItems: []domain.ReservedFlashItem{{FlashSaleItemID: fi.ID, SKUID: fi.SKUID, Quantity: 1, ShopID: shopID}},
		}, now); err != nil {
			t.Fatalf("CommitReservation(%s): %v", tc.oid, err)
		}
	}

	// 2. SyncVoucherRedis holds FOR UPDATE: skipLocked=true returns synced=false immediately, and concurrent
	// ReleaseReservation blocks until syncFn returns!
	vLocked := make(chan struct{})
	vRelease := make(chan struct{})
	vDone := make(chan error, 1)
	go func() {
		_, sErr := st.SyncVoucherRedis(ctx, v.ID, false, func(_ context.Context, cur *domain.Voucher) error {
			if cur.UsedCount != 2 {
				return fmt.Errorf("expected locked used_count=2, got %d", cur.UsedCount)
			}
			close(vLocked)
			<-vRelease
			return nil
		})
		vDone <- sErr
	}()
	<-vLocked
	if skipped, err := st.SyncVoucherRedis(ctx, v.ID, true, func(context.Context, *domain.Voucher) error {
		return errors.New("skipLocked callback must not execute while row is locked")
	}); err != nil || skipped {
		t.Fatalf("SyncVoucherRedis(skipLocked=true) while locked: want (false, nil), got (%v, %v)", skipped, err)
	}
	relDone := make(chan error, 1)
	go func() {
		_, rErr := st.ReleaseReservation(ctx, ordRel)
		relDone <- rErr
	}()
	// Confirm ReleaseReservation does not commit before vRelease (short select check).
	select {
	case rErr := <-relDone:
		t.Fatalf("ReleaseReservation bypassed SyncVoucherRedis FOR UPDATE lock (err=%v)", rErr)
	case <-time.After(80 * time.Millisecond):
	}
	close(vRelease)
	if err := <-vDone; err != nil {
		t.Fatalf("SyncVoucherRedis: %v", err)
	}
	if err := <-relDone; err != nil {
		t.Fatalf("ReleaseReservation after unlock: %v", err)
	}

	// 3. SyncFlashItemRedis holds FOR UPDATE OF fsi: skipLocked=true skips while locked.
	fLocked := make(chan struct{})
	fRelease := make(chan struct{})
	fDone := make(chan error, 1)
	go func() {
		_, sErr := st.SyncFlashItemRedis(ctx, fi.ID, false, func(_ context.Context, cur *domain.FlashSaleItem) error {
			if cur.RemainingSlots != 9 {
				return fmt.Errorf("expected locked flash remaining=9, got %d", cur.RemainingSlots)
			}
			close(fLocked)
			<-fRelease
			return nil
		})
		fDone <- sErr
	}()
	<-fLocked
	if skipped, err := st.SyncFlashItemRedis(ctx, fi.ID, true, func(context.Context, *domain.FlashSaleItem) error {
		return errors.New("skipLocked flash callback must not execute while locked")
	}); err != nil || skipped {
		t.Fatalf("SyncFlashItemRedis(skipLocked=true) while locked: want (false, nil), got (%v, %v)", skipped, err)
	}
	close(fRelease)
	if err := <-fDone; err != nil {
		t.Fatalf("SyncFlashItemRedis: %v", err)
	}

	// 3b. SyncUserUsageRedis holds FOR UPDATE on promotion_user_usage: skipLocked=true skips while locked,
	// and GetUserUsageBatch returns all requested targets in one batch query.
	uLocked := make(chan struct{})
	uRelease := make(chan struct{})
	uDone := make(chan error, 1)
	go func() {
		_, sErr := st.SyncUserUsageRedis(ctx, domain.UsageKindVoucher, v.ID, buyerAct, false, func(_ context.Context, usedQty int32) error {
			if usedQty != 1 {
				return fmt.Errorf("expected locked user usage=1, got %d", usedQty)
			}
			close(uLocked)
			<-uRelease
			return nil
		})
		uDone <- sErr
	}()
	<-uLocked
	if skipped, err := st.SyncUserUsageRedis(ctx, domain.UsageKindVoucher, v.ID, buyerAct, true, func(context.Context, int32) error {
		return errors.New("skipLocked user usage callback must not run while locked")
	}); err != nil || skipped {
		t.Fatalf("SyncUserUsageRedis(skipLocked=true) while locked: want (false, nil), got (%v, %v)", skipped, err)
	}
	close(uRelease)
	if err := <-uDone; err != nil {
		t.Fatalf("SyncUserUsageRedis: %v", err)
	}
	batchMap, err := st.GetUserUsageBatch(ctx, buyerAct, []store.UserUsageKey{
		{Kind: domain.UsageKindVoucher, TargetID: v.ID},
		{Kind: domain.UsageKindFlash, TargetID: fi.ID},
	})
	if err != nil || batchMap[store.UserUsageKey{Kind: domain.UsageKindVoucher, TargetID: v.ID}] != 1 ||
		batchMap[store.UserUsageKey{Kind: domain.UsageKindFlash, TargetID: fi.ID}] != 1 {
		t.Fatalf("GetUserUsageBatch mismatch: got %+v err=%v", batchMap, err)
	}

	// 4. promotion_user_usage includes used_qty == 0 rows as well as active used_qty == 1 rows.
	allUsage := queryAllUserUsage(ctx, t, pool)
	var sawRelZeroV, sawRelZeroF, sawActOneV bool
	for _, u := range allUsage {
		if u.UserID == buyerRel && u.Kind == domain.UsageKindVoucher && u.TargetID == v.ID && u.UsedQty == 0 {
			sawRelZeroV = true
		}
		if u.UserID == buyerRel && u.Kind == domain.UsageKindFlash && u.TargetID == fi.ID && u.UsedQty == 0 {
			sawRelZeroF = true
		}
		if u.UserID == buyerAct && u.Kind == domain.UsageKindVoucher && u.TargetID == v.ID && u.UsedQty == 1 {
			sawActOneV = true
		}
	}
	if !sawRelZeroV || !sawRelZeroF || !sawActOneV {
		t.Fatalf("promotion_user_usage missing expected rows: relZeroV=%v relZeroF=%v actOneV=%v", sawRelZeroV, sawRelZeroF, sawActOneV)
	}

	// 5. Manually re-running 004_w3a_per_user_usage_and_sync.sql is completely safe and preserves exact usage counts.
	sql004, err := os.ReadFile("migrations/004_w3a_per_user_usage_and_sync.sql")
	if err != nil {
		t.Fatalf("read 004 migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql004)); err != nil {
		t.Fatalf("manual re-exec of 004_w3a_per_user_usage_and_sync.sql failed: %v", err)
	}
	if q, err := pgUserUsageQty(ctx, st, domain.UsageKindVoucher, v.ID, buyerRel); err != nil || q != 0 {
		t.Fatalf("after 004 re-exec: buyerRel voucher used_qty=%d err=%v (want 0)", q, err)
	}
	if q, err := pgUserUsageQty(ctx, st, domain.UsageKindVoucher, v.ID, buyerAct); err != nil || q != 1 {
		t.Fatalf("after 004 re-exec: buyerAct voucher used_qty=%d err=%v (want 1)", q, err)
	}
}

// TestPGStore_W3A_R4_004BackfillIncludesReleased proves (Review 8) that 004 backfill:
// (a) creates used_qty=0 rows for legacy RELEASED reservations (so startup SyncRedisFromStore deletes stale :user: keys), and
// (b) resets drifted positive promotion_user_usage rows back to 0 when their reservation is RELEASED.
func TestPGStore_W3A_R4_004BackfillIncludesReleased(t *testing.T) {
	st, pool := openTestPGStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := uuid.NewString()[:8]

	v, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGR404" + suffix, Name: "PG R4 004", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 10000, Quota: 10, PerUserLimit: 5,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	buyerLegacyRel := "pg-r4-004-leg-" + suffix
	buyerDriftRel := "pg-r4-004-drf-" + suffix
	ordLeg := "pg-r4-004-ord-leg-" + suffix
	ordDrf := "pg-r4-004-ord-drf-" + suffix

	for _, tc := range []struct{ oid, uid string }{{ordLeg, buyerLegacyRel}, {ordDrf, buyerDriftRel}} {
		if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
			OrderID:  tc.oid,
			UserID:   tc.uid,
			Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopePlatform, DiscountVND: 10000}},
		}, now); err != nil {
			t.Fatalf("CommitReservation(%s): %v", tc.oid, err)
		}
		if _, err := st.ReleaseReservation(ctx, tc.oid); err != nil {
			t.Fatalf("ReleaseReservation(%s): %v", tc.oid, err)
		}
	}

	// Simulate pre-3a legacy RELEASED row (no row in promotion_user_usage) and drifted positive row (used_qty=3 while RELEASED).
	if _, err := pool.Exec(ctx, `DELETE FROM promotion_user_usage WHERE kind = 'VOUCHER' AND target_id = $1 AND user_id = $2`, v.ID, buyerLegacyRel); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE promotion_user_usage SET used_qty = 3 WHERE kind = 'VOUCHER' AND target_id = $1 AND user_id = $2`, v.ID, buyerDriftRel); err != nil {
		t.Fatal(err)
	}

	sql004, err := os.ReadFile("migrations/004_w3a_per_user_usage_and_sync.sql")
	if err != nil {
		t.Fatalf("read 004 migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sql004)); err != nil {
		t.Fatalf("re-exec 004: %v", err)
	}

	// (a) buyerLegacyRel MUST now have a row with used_qty == 0 in promotion_user_usage!
	allUsage := queryAllUserUsage(ctx, t, pool)
	foundLegacyZero := false
	for _, u := range allUsage {
		if u.Kind == domain.UsageKindVoucher && u.TargetID == v.ID && u.UserID == buyerLegacyRel && u.UsedQty == 0 {
			foundLegacyZero = true
		}
	}
	if !foundLegacyZero {
		t.Fatalf("004 backfill did not create used_qty=0 row for legacy RELEASED reservation")
	}

	// (b) buyerDriftRel drifted used_qty=3 MUST be reset to 0!
	if q, err := pgUserUsageQty(ctx, st, domain.UsageKindVoucher, v.ID, buyerDriftRel); err != nil || q != 0 {
		t.Fatalf("004 backfill did not reset drifted RELEASED usage from 3 to 0: got %d err=%v", q, err)
	}
}

func queryAllUserUsage(ctx context.Context, t *testing.T, pool *pgxpool.Pool) []domain.UserUsage {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT kind, target_id, user_id, used_qty FROM promotion_user_usage`)
	if err != nil {
		t.Fatalf("query promotion_user_usage: %v", err)
	}
	defer rows.Close()
	var out []domain.UserUsage
	for rows.Next() {
		var u domain.UserUsage
		if err := rows.Scan(&u.Kind, &u.TargetID, &u.UserID, &u.UsedQty); err != nil {
			t.Fatalf("scan promotion_user_usage: %v", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate promotion_user_usage: %v", err)
	}
	return out
}

func pgUserUsageQty(ctx context.Context, st store.Store, kind, targetID, userID string) (int32, error) {
	k := store.UserUsageKey{Kind: kind, TargetID: targetID}
	m, err := st.GetUserUsageBatch(ctx, userID, []store.UserUsageKey{k})
	if err != nil {
		return 0, err
	}
	return m[k], nil
}
