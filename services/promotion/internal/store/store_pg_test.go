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
	already, err := st.ReleaseReservation(ctx, tombOrdID)
	if err != nil || !already {
		t.Fatalf("ReleaseReservation(missing) expected already=true err=nil, got already=%v err=%v", already, err)
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
			{SKUID: skuA, OriginalPriceVND: 200000, DiscountedPriceVND: 160000, DiscountPercent: 20, PerUserLimit: 10},
			{SKUID: skuB, OriginalPriceVND: 150000, DiscountedPriceVND: 120000, DiscountPercent: 20, PerUserLimit: 10},
		},
	}, now)
	if err != nil {
		t.Fatalf("CreateShopDiscountProgram prog1: %v", err)
	}
	prog2, err := st.CreateShopDiscountProgram(ctx, domain.ShopDiscountProgram{
		ShopID: shopID, Name: "PG DL Prog2 " + suffix,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
		Items: []domain.ShopDiscountProgramItem{
			{SKUID: skuC, OriginalPriceVND: 180000, DiscountedPriceVND: 144000, DiscountPercent: 20, PerUserLimit: 10},
			{SKUID: skuD, OriginalPriceVND: 100000, DiscountedPriceVND: 80000, DiscountPercent: 20, PerUserLimit: 10},
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
						{SKUID: s1, OriginalPriceVND: 200000, DiscountedPriceVND: price1, DiscountPercent: 20, PerUserLimit: 10},
						{SKUID: s2, OriginalPriceVND: 150000, DiscountedPriceVND: price2, DiscountPercent: 20, PerUserLimit: 10},
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
			cur.Items[0].Warning = "SKU overlaps with an approved flash sale window"
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
