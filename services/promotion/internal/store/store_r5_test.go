package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

func openR5PGStore(t *testing.T) (*PostgresStore, *pgxpool.Pool) {
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
	st := NewPostgresStore(pool)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st, pool
}

func TestStore_W3A_R5_Item1_CoinPrefixConflictAndDuplicateRelease(t *testing.T) {
	t.Run("MemoryStore", func(t *testing.T) {
		ctx := context.Background()
		now := time.Now().UTC()
		m := NewMemoryStore()

		if _, _, err := m.CreditCoins(ctx, "u1", 1000, "bad", "reserve:ord-1"); !errors.Is(err, ErrReservedCoinReferenceID) {
			t.Fatalf("MemoryStore CreditCoins(reserve:): want ErrReservedCoinReferenceID, got %v", err)
		}
		if _, _, err := m.CreditCoins(ctx, "u1", 1000, "bad", "RELEASE:ord-1"); !errors.Is(err, ErrReservedCoinReferenceID) {
			t.Fatalf("MemoryStore CreditCoins(RELEASE:): want ErrReservedCoinReferenceID, got %v", err)
		}

		if _, _, err := m.CreditCoins(ctx, "u1", 10000, "seed", "seed-1"); err != nil {
			t.Fatal(err)
		}

		// Simulate pre-existing conflicting reserve:<ord> in ledger -> CommitReservation must fail & rollback.
		m.mu.Lock()
		m.coinRefIndex["reserve:ord-conflict"] = true
		m.mu.Unlock()
		if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
			OrderID:   "ord-conflict",
			UserID:    "u1",
			CoinsUsed: 5000,
		}, now); !errors.Is(err, ErrCoinLedgerConflict) {
			t.Fatalf("MemoryStore CommitReservation conflict: want ErrCoinLedgerConflict, got %v", err)
		}
		if _, err := m.GetReservation(ctx, "ord-conflict"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("MemoryStore reservation must not exist after rollback: err=%v", err)
		}
		if bal, _, _ := m.GetCoinBalance(ctx, "u1", 10); bal != 10000 {
			t.Fatalf("MemoryStore balance want 10000, got %d", bal)
		}

		// Simulate pre-existing release:<ord> -> ReleaseReservation must NOT credit AND must report CoinsReturned=0.
		if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
			OrderID:   "ord-dup-rel",
			UserID:    "u1",
			CoinsUsed: 3000,
		}, now); err != nil {
			t.Fatal(err)
		}
		m.mu.Lock()
		m.coinRefIndex["release:ord-dup-rel"] = true
		m.mu.Unlock()
		relRes, err := m.ReleaseReservation(ctx, "ord-dup-rel")
		if err != nil {
			t.Fatalf("ReleaseReservation: %v", err)
		}
		if relRes.CoinsReturned != 0 {
			t.Fatalf("duplicate release:<id> must report CoinsReturned=0, got %d", relRes.CoinsReturned)
		}
		if bal, _, _ := m.GetCoinBalance(ctx, "u1", 10); bal != 7000 {
			t.Fatalf("duplicate release:<id> must not credit balance: want 7000 got %d", bal)
		}
	})

	t.Run("PostgresStore", func(t *testing.T) {
		st, pool := openR5PGStore(t)
		ctx := context.Background()
		now := time.Now().UTC()
		suffix := uuid.NewString()[:8]
		buyer := "st-r5-1-buyer-" + suffix
		other := "st-r5-1-other-" + suffix

		if _, _, err := st.CreditCoins(ctx, buyer, 1000, "bad", "reserve:ord-pg"); !errors.Is(err, ErrReservedCoinReferenceID) {
			t.Fatalf("PostgresStore CreditCoins(reserve:): want ErrReservedCoinReferenceID, got %v", err)
		}
		if _, _, err := st.CreditCoins(ctx, buyer, 1000, "bad", "release:ord-pg"); !errors.Is(err, ErrReservedCoinReferenceID) {
			t.Fatalf("PostgresStore CreditCoins(release:): want ErrReservedCoinReferenceID, got %v", err)
		}
		if _, _, err := st.CreditCoins(ctx, buyer, 10000, "seed", "st-seed-"+suffix); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.CreditCoins(ctx, other, 100, "seed", "st-other-"+suffix); err != nil {
			t.Fatal(err)
		}

		// Duplicate release:<id> on ReleaseReservation -> no double credit AND CoinsReturned == 0.
		ordRel := "st-r5-1-rel-" + suffix
		if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
			OrderID:   ordRel,
			UserID:    buyer,
			CoinsUsed: 4000,
		}, now); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO coin_transactions (id, user_id, delta, reason, reference_id)
			VALUES ($1, $2, 1, 'legacy_collision', $3)
		`, uuid.NewString(), other, "release:"+ordRel); err != nil {
			t.Fatal(err)
		}
		relRes, err := st.ReleaseReservation(ctx, ordRel)
		if err != nil {
			t.Fatalf("ReleaseReservation: %v", err)
		}
		if relRes.CoinsReturned != 0 {
			t.Fatalf("PostgresStore duplicate release:<id> must report CoinsReturned=0, got %d", relRes.CoinsReturned)
		}
		if bal, _, _ := st.GetCoinBalance(ctx, buyer, 10); bal != 6000 {
			t.Fatalf("PostgresStore duplicate release:<id> must not credit balance: want 6000 got %d", bal)
		}
	})
}

func TestPGStore_W3A_R5_Item2_And_Item8_SyncUserUsageExistsDistinctionAndBatchedStartupSync(t *testing.T) {
	st, pool := openR5PGStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	suffix := uuid.NewString()[:8]

	v, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGR5I2" + suffix, Name: "PG R5 I2", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 10000, Quota: 10, PerUserLimit: 5,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Absent row + skipLocked=true MUST call syncFn(0) and return (true, nil).
	absentBuyer := "pg-r5-absent-" + suffix
	called := false
	gotQty := int32(-1)
	synced, err := st.SyncUserUsageRedis(ctx, domain.UsageKindVoucher, v.ID, absentBuyer, true, func(_ context.Context, usedQty int32) error {
		called = true
		gotQty = usedQty
		return nil
	})
	if err != nil || !synced || !called || gotQty != 0 {
		t.Fatalf("SyncUserUsageRedis(absent, skipLocked=true): want (synced=true, called=true, qty=0), got (%v, %v, %d, err=%v)", synced, called, gotQty, err)
	}

	// 2. Existing row locked by another TX + skipLocked=true MUST return (false, nil) without calling syncFn!
	lockedBuyer := "pg-r5-locked-" + suffix
	if _, _, err := st.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:  "pg-r5-lock-ord-" + suffix,
		UserID:   lockedBuyer,
		Vouchers: []domain.ReservedVoucher{{VoucherID: v.ID, Scope: domain.VoucherScopePlatform, DiscountVND: 10000}},
	}, now); err != nil {
		t.Fatal(err)
	}

	holdTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holdTx.Rollback(ctx) }()
	var heldQty int32
	if err := holdTx.QueryRow(ctx, `
		SELECT used_qty FROM promotion_user_usage
		WHERE kind = $1 AND target_id = $2 AND user_id = $3
		FOR UPDATE
	`, domain.UsageKindVoucher, v.ID, lockedBuyer).Scan(&heldQty); err != nil {
		t.Fatalf("lock usage row: %v", err)
	}

	calledLocked := false
	syncedLocked, err := st.SyncUserUsageRedis(ctx, domain.UsageKindVoucher, v.ID, lockedBuyer, true, func(_ context.Context, _ int32) error {
		calledLocked = true
		return nil
	})
	if err != nil || syncedLocked || calledLocked {
		t.Fatalf("SyncUserUsageRedis(locked, skipLocked=true): want (synced=false, called=false), got (%v, %v, err=%v)", syncedLocked, calledLocked, err)
	}

	// 3. D12 Item 2: ListActiveVouchers returns only ACTIVE/is_active/in-window vouchers, and
	// ListActiveApprovedFlashSaleItems returns only APPROVED items whose sale has not ended.
	pausedV, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGR6PAU" + suffix, Name: "Paused", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 10000, Quota: 10, PerUserLimit: 5,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour), IsActive: false, Status: domain.VoucherStatusPaused,
	})
	if err != nil {
		t.Fatal(err)
	}
	expiredV, err := st.CreateVoucher(ctx, domain.Voucher{
		Code: "PGR6EXP" + suffix, Name: "Expired", Scope: domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed, ValueVND: 10000, Quota: 10, PerUserLimit: 5,
		StartsAt: now.Add(-3 * time.Hour), EndsAt: now.Add(-time.Hour), IsActive: true, Status: domain.VoucherStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	activeVs, err := st.ListActiveVouchers(ctx, now)
	if err != nil {
		t.Fatalf("ListActiveVouchers: %v", err)
	}
	var sawActive, sawPaused, sawExpired bool
	for _, av := range activeVs {
		switch av.ID {
		case v.ID:
			sawActive = true
		case pausedV.ID:
			sawPaused = true
		case expiredV.ID:
			sawExpired = true
		}
	}
	if !sawActive || sawPaused || sawExpired {
		t.Fatalf("ListActiveVouchers mismatch: active=%v paused=%v expired=%v", sawActive, sawPaused, sawExpired)
	}

	fsActive, err := st.CreateFlashSale(ctx, domain.FlashSale{
		Name: "PGR6Act" + suffix, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fsEnded, err := st.CreateFlashSale(ctx, domain.FlashSale{
		Name: "PGR6End" + suffix, StartsAt: now.Add(-3 * time.Hour), EndsAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	fiAct, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fsActive.ID, ShopID: "shop-r6", SKUID: "sku-r6-act-" + suffix,
		OriginalPriceVND: 200000, FlashPriceVND: 100000, TotalSlots: 5, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ApproveFlashSaleItem(ctx, fiAct.ID, "admin-r6", true, "", 200000, now); err != nil {
		t.Fatal(err)
	}
	fiPending, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fsActive.ID, ShopID: "shop-r6", SKUID: "sku-r6-pend-" + suffix,
		OriginalPriceVND: 200000, FlashPriceVND: 100000, TotalSlots: 5, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	fiEnded, err := st.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fsEnded.ID, ShopID: "shop-r6", SKUID: "sku-r6-end-" + suffix,
		OriginalPriceVND: 200000, FlashPriceVND: 100000, TotalSlots: 5, PerUserLimit: 2,
	}, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ApproveFlashSaleItem(ctx, fiEnded.ID, "admin-r6", true, "", 200000, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	activeFlash, err := st.ListActiveApprovedFlashSaleItems(ctx, now)
	if err != nil {
		t.Fatalf("ListActiveApprovedFlashSaleItems: %v", err)
	}
	var sawFAct, sawFPend, sawFEnd bool
	for _, af := range activeFlash {
		switch af.ID {
		case fiAct.ID:
			sawFAct = true
		case fiPending.ID:
			sawFPend = true
		case fiEnded.ID:
			sawFEnd = true
		}
	}
	if !sawFAct || sawFPend || sawFEnd {
		t.Fatalf("ListActiveApprovedFlashSaleItems mismatch: act=%v pend=%v end=%v", sawFAct, sawFPend, sawFEnd)
	}
}
