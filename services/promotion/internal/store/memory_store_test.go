package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

func TestMemoryStore_MigratePingAndVoucherCRUD(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	if err := m.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := m.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	now := time.Now().UTC()
	v1, err := m.CreateVoucher(ctx, domain.Voucher{
		Code:         "SHOP10K",
		Name:         "Shop 10k",
		Scope:        domain.VoucherScopeShop,
		ShopID:       "shop-1",
		DiscountType: domain.DiscountTypeFixed,
		ValueVND:     10000,
		Quota:        5,
		PerUserLimit: 1,
		BudgetVND:    50000,
		StartsAt:     now.Add(-time.Hour),
		EndsAt:       now.Add(time.Hour),
		IsActive:     true,
	})
	if err != nil {
		t.Fatalf("CreateVoucher v1: %v", err)
	}
	if v1.ID == "" {
		t.Fatal("expected generated voucher ID")
	}

	// Duplicate code rejected
	if _, err := m.CreateVoucher(ctx, domain.Voucher{Code: "SHOP10K"}); !errors.Is(err, ErrDuplicateCode) {
		t.Fatalf("expected ErrDuplicateCode, got %v", err)
	}

	// Create second inactive voucher with explicit ID
	v2, err := m.CreateVoucher(ctx, domain.Voucher{
		ID:           "voucher-custom-id",
		Code:         "PLATINACTIVE",
		Scope:        domain.VoucherScopePlatform,
		DiscountType: domain.DiscountTypeFixed,
		ValueVND:     5000,
		Quota:        10,
		StartsAt:     now.Add(-time.Hour),
		EndsAt:       now.Add(time.Hour),
		IsActive:     false,
	})
	if err != nil {
		t.Fatalf("CreateVoucher v2: %v", err)
	}
	if v2.ID != "voucher-custom-id" {
		t.Fatalf("expected custom ID preserved, got %q", v2.ID)
	}

	// Lookup by ID, by Code, and not found
	byID, err := m.GetVoucherByIDOrCode(ctx, v1.ID)
	if err != nil || byID.Code != "SHOP10K" {
		t.Fatalf("GetVoucherByIDOrCode by ID: %+v err=%v", byID, err)
	}
	byCode, err := m.GetVoucherByIDOrCode(ctx, "SHOP10K")
	if err != nil || byCode.ID != v1.ID {
		t.Fatalf("GetVoucherByIDOrCode by code: %+v err=%v", byCode, err)
	}
	if _, err := m.GetVoucherByIDOrCode(ctx, "MISSING"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// ListVouchers with filters
	all, err := m.ListVouchers(ctx, "", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("ListVouchers all: got %d err=%v", len(all), err)
	}
	shopOnly, err := m.ListVouchers(ctx, "shop-1", domain.VoucherScopeShop)
	if err != nil || len(shopOnly) != 1 || shopOnly[0].ID != v1.ID {
		t.Fatalf("ListVouchers shop filter unexpected: %+v err=%v", shopOnly, err)
	}

	// ListActiveVouchers excludes inactive
	active, err := m.ListActiveVouchers(ctx, now)
	if err != nil || len(active) != 1 || active[0].ID != v1.ID {
		t.Fatalf("ListActiveVouchers unexpected: %+v err=%v", active, err)
	}

	// SaveUserVoucher idempotency & lookup
	already1, err := m.SaveUserVoucher(ctx, "user-1", v1.ID)
	if err != nil || already1 {
		t.Fatalf("1st SaveUserVoucher: already=%v err=%v", already1, err)
	}
	already2, err := m.SaveUserVoucher(ctx, "user-1", v1.ID)
	if err != nil || !already2 {
		t.Fatalf("2nd SaveUserVoucher: already=%v err=%v", already2, err)
	}
	saved, err := m.GetUserSavedVoucherIDs(ctx, "user-1")
	if err != nil || !saved[v1.ID] || len(saved) != 1 {
		t.Fatalf("GetUserSavedVoucherIDs unexpected: %+v err=%v", saved, err)
	}
}

func TestMemoryStore_FlashSalesLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC()

	fsActive, err := m.CreateFlashSale(ctx, domain.FlashSale{
		Name:     "Active Sale",
		StartsAt: now.Add(-30 * time.Minute),
		EndsAt:   now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale active: %v", err)
	}
	gotFS, err := m.GetFlashSale(ctx, fsActive.ID)
	if err != nil || gotFS.Name != "Active Sale" || gotFS.Status != domain.FlashWindowStatusActive {
		t.Fatalf("GetFlashSale unexpected: %+v err=%v", gotFS, err)
	}
	if _, err := m.GetFlashSale(ctx, "missing-fs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing flash sale, got %v", err)
	}

	fsUpcoming, err := m.CreateFlashSale(ctx, domain.FlashSale{
		Name:     "Upcoming Sale",
		StartsAt: now.Add(3 * time.Hour),
		EndsAt:   now.Add(5 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateFlashSale upcoming: %v", err)
	}

	fsEnded, err := m.CreateFlashSale(ctx, domain.FlashSale{
		Name:     "Already Ended",
		StartsAt: now.Add(-4 * time.Hour),
		EndsAt:   now.Add(-2 * time.Hour),
		Status:   domain.FlashWindowStatusEnded,
	})
	if err != nil {
		t.Fatalf("CreateFlashSale ended: %v", err)
	}

	// Register errors: missing flash sale, ended flash sale
	if _, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{FlashSaleID: "nope", SKUID: "sku-1", TotalSlots: 5}, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{FlashSaleID: fsEnded.ID, SKUID: "sku-1", TotalSlots: 5}, now); !errors.Is(err, ErrFlashSaleEnded) {
		t.Fatalf("expected ErrFlashSaleEnded, got %v", err)
	}

	// Register & reject item on same SKU -> rejected does NOT block overlap
	it1, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID:   fsActive.ID,
		ShopID:        "shop-1",
		SKUID:         "sku-1",
		FlashPriceVND: 50000,
		TotalSlots:    10,
		PerUserLimit:  2,
	}, now)
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem it1: %v", err)
	}
	// Duplicate SKU in same overlapping window rejected
	if _, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID:   fsActive.ID,
		ShopID:        "shop-1",
		SKUID:         "sku-1",
		FlashPriceVND: 45000,
		TotalSlots:    5,
	}, now); !errors.Is(err, ErrOverlappingFlashSale) {
		t.Fatalf("expected ErrOverlappingFlashSale, got %v", err)
	}

	// Approve missing item -> ErrNotFound
	if _, _, err := m.ApproveFlashSaleItem(ctx, "missing", "admin-1", true, "", 0, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Reject then re-register same SKU succeeds
	rejItem, prevStatus, err := m.ApproveFlashSaleItem(ctx, it1.ID, "admin-1", false, "price too high", 0, now)
	if err != nil || prevStatus != domain.FlashStatusPending || rejItem.Status != domain.FlashStatusRejected || rejItem.RejectionReason != "price too high" {
		t.Fatalf("reject item unexpected: item=%+v prev=%q err=%v", rejItem, prevStatus, err)
	}

	it2, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID:   fsActive.ID,
		ShopID:        "shop-1",
		SKUID:         "sku-1",
		FlashPriceVND: 40000,
		TotalSlots:    8,
		PerUserLimit:  2,
	}, time.Time{})
	if err != nil {
		t.Fatalf("RegisterFlashSaleItem after rejection failed: %v", err)
	}
	if _, prev, err := m.ApproveFlashSaleItem(ctx, it2.ID, "admin-1", true, "", 80000, now); err != nil || prev != domain.FlashStatusPending {
		t.Fatalf("approve it2: prev=%q err=%v", prev, err)
	}

	// GetFlashSaleItem found & not found
	gotItem, err := m.GetFlashSaleItem(ctx, it2.ID)
	if err != nil || gotItem.Status != domain.FlashStatusApproved {
		t.Fatalf("GetFlashSaleItem: %+v err=%v", gotItem, err)
	}
	if _, err := m.GetFlashSaleItem(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// ListFlashSales active only vs includeUpcoming
	activeOnly, err := m.ListFlashSales(ctx, now, false)
	if err != nil || len(activeOnly) != 1 || activeOnly[0].ID != fsActive.ID || len(activeOnly[0].Items) != 1 {
		t.Fatalf("ListFlashSales active only unexpected: %+v err=%v", activeOnly, err)
	}
	withUpcoming, err := m.ListFlashSales(ctx, now, true)
	if err != nil || len(withUpcoming) != 2 || withUpcoming[0].ID != fsActive.ID || withUpcoming[1].ID != fsUpcoming.ID {
		t.Fatalf("ListFlashSales withUpcoming unexpected: %+v err=%v", withUpcoming, err)
	}

	// ListApprovedFlashSaleItems uses wall clock; create a future-ending window relative to real time
	realNow := time.Now().UTC()
	fsLive, err := m.CreateFlashSale(ctx, domain.FlashSale{
		Name:     "Live Wall Clock",
		StartsAt: realNow.Add(-10 * time.Minute),
		EndsAt:   realNow.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	itLive, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fsLive.ID, ShopID: "shop-1", SKUID: "sku-live", FlashPriceVND: 30000, TotalSlots: 5, PerUserLimit: 1,
	}, realNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.ApproveFlashSaleItem(ctx, itLive.ID, "admin-1", true, "", 60000, realNow); err != nil {
		t.Fatal(err)
	}
	approvedItems, err := m.ListApprovedFlashSaleItems(ctx)
	if err != nil || len(approvedItems) == 0 {
		t.Fatalf("ListApprovedFlashSaleItems expected at least 1 live item, got %+v err=%v", approvedItems, err)
	}

	// EndFlashSaleWindow: not found, active window, and cannot approve item in ended window
	if _, _, err := m.EndFlashSaleWindow(ctx, "missing", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("EndFlashSaleWindow missing: got %v", err)
	}
	endedFS, unreleased, err := m.EndFlashSaleWindow(ctx, fsActive.ID, now)
	if err != nil || endedFS.Status != domain.FlashWindowStatusEnded || len(unreleased) != 1 || unreleased[0].ID != it2.ID {
		t.Fatalf("EndFlashSaleWindow unexpected: fs=%+v unreleased=%+v err=%v", endedFS, unreleased, err)
	}
	if _, _, err := m.ApproveFlashSaleItem(ctx, it1.ID, "admin-1", true, "", 0, now); !errors.Is(err, ErrFlashSaleEnded) {
		t.Fatalf("expected ErrFlashSaleEnded approving item in ended window, got %v", err)
	}

	// MarkFlashSaleItemStockReleased (real item + missing item + idempotent second call)
	if err := m.MarkFlashSaleItemStockReleased(ctx, "non-existent", time.Time{}); err != nil {
		t.Fatalf("MarkFlashSaleItemStockReleased missing: %v", err)
	}
	if err := m.MarkFlashSaleItemStockReleased(ctx, it2.ID, now); err != nil {
		t.Fatalf("MarkFlashSaleItemStockReleased it2: %v", err)
	}
	// After marking released, ListExpiredUnreleasedFlashSaleItems skips it2
	expItems, err := m.ListExpiredUnreleasedFlashSaleItems(ctx, now.Add(10*time.Hour))
	if err != nil {
		t.Fatalf("ListExpiredUnreleasedFlashSaleItems: %v", err)
	}
	for _, it := range expItems {
		if it.ID == it2.ID {
			t.Fatalf("already-released item %s must not be returned", it2.ID)
		}
	}
}

// TestMemoryStore_FlashSaleEndedWindowEnforcedAcrossQueriesAndCommit verifies that ending a flash sale window
// early (including upcoming windows where starts_at > now so ends_at stays in the future) is strictly enforced
// by CommitReservation, ListFlashSales, ListAllFlashSales, ListActiveApprovedFlashSaleItems, and HasOverlappingApprovedFlashSale.
func TestMemoryStore_FlashSaleEndedWindowEnforcedAcrossQueriesAndCommit(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

	// Case 1: Upcoming window ended early -> ends_at stays in the future (now < starts_at < ends_at), status='ENDED'.
	fsUp, err := m.CreateFlashSale(ctx, domain.FlashSale{
		Name:     "Upcoming Ended Early",
		StartsAt: now.Add(1 * time.Hour),
		EndsAt:   now.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	itUp, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fsUp.ID, ShopID: "shop-1", SKUID: "sku-up", FlashPriceVND: 50000, TotalSlots: 10, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.ApproveFlashSaleItem(ctx, itUp.ID, "admin-1", true, "", 100000, now); err != nil {
		t.Fatal(err)
	}

	if _, _, err := m.EndFlashSaleWindow(ctx, fsUp.ID, now); err != nil {
		t.Fatalf("EndFlashSaleWindow(fsUp): %v", err)
	}

	// ListAllFlashSales MUST NOT show fsUp under UPCOMING or ACTIVE, and MUST show it under ENDED.
	upList, upTotal, err := m.ListAllFlashSales(ctx, now, "UPCOMING", 1, 20)
	if err != nil || upTotal != 0 || len(upList) != 0 {
		t.Fatalf("ended upcoming window leaked into UPCOMING: list=%+v total=%d err=%v", upList, upTotal, err)
	}
	inWindowTime := now.Add(2 * time.Hour)
	actList, actTotal, err := m.ListAllFlashSales(ctx, inWindowTime, "ACTIVE", 1, 20)
	if err != nil || actTotal != 0 || len(actList) != 0 {
		t.Fatalf("ended upcoming window leaked into ACTIVE at future time: list=%+v total=%d err=%v", actList, actTotal, err)
	}
	endList, endTotal, err := m.ListAllFlashSales(ctx, now, "ENDED", 1, 20)
	if err != nil || endTotal != 1 || len(endList) != 1 || endList[0].ID != fsUp.ID {
		t.Fatalf("ended upcoming window missing from ENDED: list=%+v total=%d err=%v", endList, endTotal, err)
	}

	// ListFlashSales (both active-only and includeUpcoming) MUST exclude it even inside [starts_at, ends_at).
	if live, err := m.ListFlashSales(ctx, inWindowTime, false); err != nil || len(live) != 0 {
		t.Fatalf("ListFlashSales(active) leaked ended window: %+v err=%v", live, err)
	}
	if liveUp, err := m.ListFlashSales(ctx, now, true); err != nil || len(liveUp) != 0 {
		t.Fatalf("ListFlashSales(includeUpcoming) leaked ended window: %+v err=%v", liveUp, err)
	}

	// ListActiveApprovedFlashSaleItems & HasOverlappingApprovedFlashSale MUST exclude ended windows.
	if items, err := m.ListActiveApprovedFlashSaleItems(ctx, inWindowTime); err != nil || len(items) != 0 {
		t.Fatalf("ListActiveApprovedFlashSaleItems leaked ended item: %+v err=%v", items, err)
	}
	if overlap, err := m.HasOverlappingApprovedFlashSale(ctx, "sku-up", now.Add(time.Hour), now.Add(3*time.Hour), now); err != nil || overlap {
		t.Fatalf("HasOverlappingApprovedFlashSale should ignore ended window: overlap=%v err=%v", overlap, err)
	}

	// CommitReservation at inWindowTime (where starts_at <= t < ends_at STILL holds!) MUST reject because status == ENDED!
	if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "ord-ended-fs",
		UserID:  "buyer-1",
		FlashSaleItems: []domain.ReservedFlashItem{
			{FlashSaleItemID: itUp.ID, SKUID: "sku-up", Quantity: 1, ShopID: "shop-1"},
		},
	}, inWindowTime); !errors.Is(err, ErrFlashNotEligibleForCommit) {
		t.Fatalf("CommitReservation on ENDED flash window: want ErrFlashNotEligibleForCommit, got %v", err)
	}
}

func TestMemoryStore_ReservationsAndCoinLedgerInvariant(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC()

	v, err := m.CreateVoucher(ctx, domain.Voucher{
		Code: "RESV1", Scope: domain.VoucherScopeShop, ShopID: "shop-1",
		DiscountType: domain.DiscountTypeFixed, ValueVND: 15000,
		Quota: 5, PerUserLimit: 2, BudgetVND: 75000,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), IsActive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := m.CreateFlashSale(ctx, domain.FlashSale{
		Name: "Res FS", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := m.RegisterFlashSaleItem(ctx, domain.FlashSaleItem{
		FlashSaleID: fs.ID, ShopID: "shop-1", SKUID: "sku-r", FlashPriceVND: 20000, TotalSlots: 3, PerUserLimit: 2,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.ApproveFlashSaleItem(ctx, fi.ID, "admin-1", true, "", 50000, now); err != nil {
		t.Fatal(err)
	}

	// Credit 10,000 coins
	bal, already, err := m.CreditCoins(ctx, "u1", 10000, "seed", "seed-u1")
	if err != nil || already || bal != 10000 {
		t.Fatalf("CreditCoins unexpected: bal=%d already=%v err=%v", bal, already, err)
	}

	// GetReservation missing
	if _, err := m.GetReservation(ctx, "ord-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// CommitReservation with insufficient coins fails cleanly
	if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "ord-poor", UserID: "u1", CoinsUsed: 20000,
	}, now); !errors.Is(err, ErrInsufficientCoins) {
		t.Fatalf("expected ErrInsufficientCoins, got %v", err)
	}

	// CommitReservation with coins + voucher + flash item
	resIn := domain.PromotionReservation{
		OrderID:   "ord-1",
		UserID:    "u1",
		CoinsUsed: 4000,
		Vouchers: []domain.ReservedVoucher{
			{VoucherID: v.ID, Scope: domain.VoucherScopeShop, ShopID: "shop-1", DiscountVND: 15000, HasBudget: true},
		},
		FlashSaleItems: []domain.ReservedFlashItem{
			{FlashSaleItemID: fi.ID, SKUID: "sku-r", Quantity: 2, ShopID: "shop-1"},
		},
		BreakdownJSON: []byte(`{"finalPayableVnd":"50000"}`),
	}
	saved, alreadyRes, err := m.CommitReservation(ctx, resIn, now)
	if err != nil || alreadyRes || saved.Status != domain.ReservationStatusReserved {
		t.Fatalf("CommitReservation 1st: saved=%+v already=%v err=%v", saved, alreadyRes, err)
	}

	// Duplicate CommitReservation returns existing reservation
	saved2, alreadyRes2, err := m.CommitReservation(ctx, resIn, now)
	if err != nil || !alreadyRes2 || saved2.OrderID != "ord-1" {
		t.Fatalf("CommitReservation 2nd: saved=%+v already=%v err=%v", saved2, alreadyRes2, err)
	}

	// Check counters decremented & balance == ledger sum
	if m.AccountBalance("u1") != 6000 || m.LedgerSum("u1") != 6000 {
		t.Fatalf("coin invariant broken after commit: acct=%d sum=%d", m.AccountBalance("u1"), m.LedgerSum("u1"))
	}
	gotFI, _ := m.GetFlashSaleItem(ctx, fi.ID)
	if gotFI.RemainingSlots != 1 {
		t.Fatalf("expected RemainingSlots=1, got %d", gotFI.RemainingSlots)
	}
	gotV, _ := m.GetVoucherByIDOrCode(ctx, v.ID)
	if gotV.UsedCount != 1 || gotV.UsedBudgetVND != 15000 {
		t.Fatalf("expected voucher UsedCount=1 UsedBudgetVND=15000, got %+v", gotV)
	}

	// ReleaseReservation restores coins, voucher, flash slots
	rel1, err := m.ReleaseReservation(ctx, "ord-1")
	if err != nil || rel1.AlreadyReleased {
		t.Fatalf("1st ReleaseReservation: res=%+v err=%v", rel1, err)
	}
	rel2, err := m.ReleaseReservation(ctx, "ord-1")
	if err != nil || !rel2.AlreadyReleased {
		t.Fatalf("2nd ReleaseReservation: res=%+v err=%v", rel2, err)
	}
	if m.AccountBalance("u1") != 10000 || m.LedgerSum("u1") != 10000 {
		t.Fatalf("coin invariant broken after release: acct=%d sum=%d", m.AccountBalance("u1"), m.LedgerSum("u1"))
	}
	gotFI, _ = m.GetFlashSaleItem(ctx, fi.ID)
	if gotFI.RemainingSlots != 3 {
		t.Fatalf("expected RemainingSlots restored to 3, got %d", gotFI.RemainingSlots)
	}
	gotV, _ = m.GetVoucherByIDOrCode(ctx, v.ID)
	if gotV.UsedCount != 0 || gotV.UsedBudgetVND != 0 {
		t.Fatalf("expected voucher counters restored to 0, got %+v", gotV)
	}

	// Re-committing a released reservation returns ErrReservationAlreadyReleased
	if _, _, err := m.CommitReservation(ctx, resIn, now); !errors.Is(err, ErrReservationAlreadyReleased) {
		t.Fatalf("expected ErrReservationAlreadyReleased, got %v", err)
	}

	// GetReservation after release preserves ReleasedAt timestamp
	gotRes, err := m.GetReservation(ctx, "ord-1")
	if err != nil || gotRes.ReleasedAt == nil || gotRes.Status != domain.ReservationStatusReleased {
		t.Fatalf("GetReservation released unexpected: %+v err=%v", gotRes, err)
	}
}

func TestMemoryStore_OrderCompletedAndReleaseShopRewardClawback(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStore()
	now := time.Now().UTC()

	// 1. Order completed credits reward coins (and empty orderID falls back to eventID).
	already, err := m.ProcessOrderCompletedEvent(ctx, "evt-c1", "order.completed", "ord-1", "buyer-1", 1500)
	if err != nil || already {
		t.Fatalf("ProcessOrderCompletedEvent: already=%v err=%v", already, err)
	}
	if processed, err := m.IsEventProcessed(ctx, "evt-c1"); err != nil || !processed {
		t.Fatalf("IsEventProcessed evt-c1: processed=%v err=%v", processed, err)
	}
	if processed, err := m.IsEventProcessed(ctx, "evt-unknown"); err != nil || processed {
		t.Fatalf("IsEventProcessed evt-unknown: processed=%v err=%v", processed, err)
	}
	if already, err := m.ProcessOrderCompletedEvent(ctx, "evt-c-no-ord", "order.completed", "", "buyer-2", 500); err != nil || already {
		t.Fatalf("ProcessOrderCompletedEvent empty orderID: already=%v err=%v", already, err)
	}
	if m.AccountBalance("buyer-2") != 500 || m.LedgerSum("buyer-2") != 500 {
		t.Fatalf("buyer-2 balance mismatch: acct=%d sum=%d", m.AccountBalance("buyer-2"), m.LedgerSum("buyer-2"))
	}

	// 2. ReleaseReservationShop WITH a reservation: restores reserved shop coins (+2000) AND claws back reward (-1500).
	if _, _, err := m.CreditCoins(ctx, "buyer-1", 2000, "seed", "seed-b1"); err != nil {
		t.Fatal(err)
	}
	// Balance is now 1500 + 2000 = 3500; commit reservation using 2000 coins -> balance 1500.
	if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:       "ord-1",
		UserID:        "buyer-1",
		CoinsUsed:     2000,
		BreakdownJSON: []byte(`{"shops":[{"shopId":"shop-1","coinsDiscountVnd":"2000"}]}`),
	}, now); err != nil {
		t.Fatalf("CommitReservation(ord-1): %v", err)
	}
	res1, err := m.ReleaseReservationShop(ctx, "ord-1", "shop-1", "", false)
	if err != nil || res1.AlreadyReleased || res1.CoinsReturned != 2000 || res1.RewardCoinsClawedBack != 1500 {
		t.Fatalf("ReleaseReservationShop 1st: res=%+v err=%v", res1, err)
	}
	// Balance should be 1500 (before) + 2000 (restored) - 1500 (clawed back) = 2000.
	if m.AccountBalance("buyer-1") != 2000 || m.LedgerSum("buyer-1") != 2000 {
		t.Fatalf("expected buyer-1 balance=2000, got acct=%d sum=%d", m.AccountBalance("buyer-1"), m.LedgerSum("buyer-1"))
	}

	// Idempotent replay MUST NOT double-clawback or double-restore.
	res1Dup, err := m.ReleaseReservationShop(ctx, "ord-1", "shop-1", "", false)
	if err != nil || !res1Dup.AlreadyReleased || res1Dup.RewardCoinsClawedBack != 0 {
		t.Fatalf("ReleaseReservationShop dup: res=%+v err=%v", res1Dup, err)
	}
	if m.AccountBalance("buyer-1") != 2000 || m.LedgerSum("buyer-1") != 2000 {
		t.Fatalf("duplicate ReleaseReservationShop mutated balance: acct=%d sum=%d", m.AccountBalance("buyer-1"), m.LedgerSum("buyer-1"))
	}

	// 3. ReleaseReservationShop WITHOUT any promotion_reservations row: still claws back reward and is idempotent.
	if _, err := m.ProcessOrderCompletedEvent(ctx, "evt-c-no-res", "order.completed", "ord-no-res", "buyer-no-res", 1200); err != nil {
		t.Fatal(err)
	}
	resNoRow, err := m.ReleaseReservationShop(ctx, "ord-no-res", "shop-1", "", false)
	if err != nil || resNoRow.AlreadyReleased || resNoRow.RewardCoinsClawedBack != 1200 {
		t.Fatalf("ReleaseReservationShop no-reservation clawback: res=%+v err=%v", resNoRow, err)
	}
	if m.AccountBalance("buyer-no-res") != 0 || m.LedgerSum("buyer-no-res") != 0 {
		t.Fatalf("buyer-no-res balance mismatch: acct=%d sum=%d", m.AccountBalance("buyer-no-res"), m.LedgerSum("buyer-no-res"))
	}
	resNoRowDup, err := m.ReleaseReservationShop(ctx, "ord-no-res", "shop-1", "", false)
	if err != nil || !resNoRowDup.AlreadyReleased || resNoRowDup.RewardCoinsClawedBack != 0 {
		t.Fatalf("ReleaseReservationShop no-reservation dup: res=%+v err=%v", resNoRowDup, err)
	}

	// 4. Clawback capped at current balance when buyer spent part of reward before refund.
	if _, err := m.ProcessOrderCompletedEvent(ctx, "evt-c3", "order.completed", "ord-cap", "buyer-cap", 1500); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "ord-other", UserID: "buyer-cap", CoinsUsed: 1000,
	}, now); err != nil {
		t.Fatal(err)
	}
	resCap, err := m.ReleaseReservationShop(ctx, "ord-cap", "shop-1", "", false)
	if err != nil || resCap.RewardCoinsClawedBack != 500 {
		t.Fatalf("expected capped clawback=500, got res=%+v err=%v", resCap, err)
	}
	if m.AccountBalance("buyer-cap") != 0 || m.LedgerSum("buyer-cap") != 0 {
		t.Fatalf("expected clawback capped at 500 -> balance 0, got acct=%d sum=%d",
			m.AccountBalance("buyer-cap"), m.LedgerSum("buyer-cap"))
	}

	// 5. Clawback when balance is already 0 -> no negative balance and no 0-delta tx.
	if _, err := m.ProcessOrderCompletedEvent(ctx, "evt-c4", "order.completed", "ord-zero", "buyer-zero", 800); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
		OrderID: "ord-spend-all", UserID: "buyer-zero", CoinsUsed: 800,
	}, now); err != nil {
		t.Fatal(err)
	}
	resZero, err := m.ReleaseReservationShop(ctx, "ord-zero", "shop-1", "", false)
	if err != nil || !resZero.AlreadyReleased || resZero.RewardCoinsClawedBack != 0 {
		t.Fatalf("zero-balance clawback unexpected: res=%+v err=%v", resZero, err)
	}
	balZero, txnsZero, err := m.GetCoinBalance(ctx, "buyer-zero", 0)
	if err != nil || balZero != 0 || m.AccountBalance("buyer-zero") != 0 || m.LedgerSum("buyer-zero") != 0 {
		t.Fatalf("buyer-zero invariant broken: bal=%d acct=%d sum=%d err=%v", balZero, m.AccountBalance("buyer-zero"), m.LedgerSum("buyer-zero"), err)
	}
	for _, tx := range txnsZero {
		if tx.Delta == 0 {
			t.Fatalf("found forbidden 0-delta coin transaction: %+v", tx)
		}
	}

	// 6. Distinct checkout_group_id vs sub_order_id + clawbackOnly=true: claws back reward without
	// restoring spent coins or mutating the committed reservation.
	if _, _, err := m.CreditCoins(ctx, "buyer-sub", 3000, "seed", "seed-bsub"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CommitReservation(ctx, domain.PromotionReservation{
		OrderID:       "grp-sub-1",
		UserID:        "buyer-sub",
		CoinsUsed:     3000,
		BreakdownJSON: []byte(`{"shops":[{"shopId":"shop-1","coinsDiscountVnd":"3000"}]}`),
	}, now); err != nil {
		t.Fatalf("CommitReservation(grp-sub-1): %v", err)
	}
	if _, err := m.ProcessOrderCompletedEvent(ctx, "evt-c-sub", "order.completed", "sub-ord-1", "buyer-sub", 1800); err != nil {
		t.Fatal(err)
	}
	resSub, err := m.ReleaseReservationShop(ctx, "grp-sub-1", "shop-1", "sub-ord-1", true)
	if err != nil || resSub.AlreadyReleased || resSub.CoinsReturned != 0 || resSub.RewardCoinsClawedBack != 1800 {
		t.Fatalf("ReleaseReservationShop(sub_order_id, clawbackOnly): res=%+v err=%v", resSub, err)
	}
	if m.AccountBalance("buyer-sub") != 0 || m.LedgerSum("buyer-sub") != 0 {
		t.Fatalf("buyer-sub balance mismatch: acct=%d sum=%d", m.AccountBalance("buyer-sub"), m.LedgerSum("buyer-sub"))
	}
	resSubDup, err := m.ReleaseReservationShop(ctx, "grp-sub-1", "shop-1", "sub-ord-1", true)
	if err != nil || !resSubDup.AlreadyReleased || resSubDup.CoinsReturned != 0 || resSubDup.RewardCoinsClawedBack != 0 {
		t.Fatalf("ReleaseReservationShop(sub_order_id, clawbackOnly) dup: res=%+v err=%v", resSubDup, err)
	}

	// 7. Refund (clawbackOnly=true) arrives BEFORE order.completed -> writes tombstone in processed_events;
	// late order.completed records event and skips crediting coins; repeated calls remain idempotent.
	pre1, err := m.ReleaseReservationShop(ctx, "grp-pre-1", "shop-1", "sub-pre-1", true)
	if err != nil || pre1.AlreadyReleased || pre1.RewardCoinsClawedBack != 0 {
		t.Fatalf("pre-reward clawback 1st: res=%+v err=%v", pre1, err)
	}
	pre2, err := m.ReleaseReservationShop(ctx, "grp-pre-1", "shop-1", "sub-pre-1", true)
	if err != nil || !pre2.AlreadyReleased || pre2.RewardCoinsClawedBack != 0 {
		t.Fatalf("pre-reward clawback 2nd replay: res=%+v err=%v", pre2, err)
	}
	alreadyPre, err := m.ProcessOrderCompletedEvent(ctx, "evt-c-pre-1", "order.completed", "sub-pre-1", "buyer-pre", 1500)
	if err != nil || alreadyPre {
		t.Fatalf("late ProcessOrderCompletedEvent after tombstone: already=%v err=%v", alreadyPre, err)
	}
	if processed, pErr := m.IsEventProcessed(ctx, "evt-c-pre-1"); pErr != nil || !processed {
		t.Fatalf("late order.completed event not marked processed: processed=%v err=%v", processed, pErr)
	}
	if m.AccountBalance("buyer-pre") != 0 || m.LedgerSum("buyer-pre") != 0 {
		t.Fatalf("late order.completed credited coins despite tombstone: acct=%d sum=%d", m.AccountBalance("buyer-pre"), m.LedgerSum("buyer-pre"))
	}
	if alreadyPre2, err := m.ProcessOrderCompletedEvent(ctx, "evt-c-pre-1", "order.completed", "sub-pre-1", "buyer-pre", 1500); err != nil || !alreadyPre2 {
		t.Fatalf("replay late ProcessOrderCompletedEvent: already=%v err=%v", alreadyPre2, err)
	}
	if pre3, err := m.ReleaseReservationShop(ctx, "grp-pre-1", "shop-1", "sub-pre-1", true); err != nil || !pre3.AlreadyReleased || pre3.RewardCoinsClawedBack != 0 {
		t.Fatalf("pre-reward clawback 3rd replay: res=%+v err=%v", pre3, err)
	}
}

type fakeRowScanner struct {
	vals []any
	err  error
}

func (f *fakeRowScanner) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = f.vals[i].(string)
		case *int64:
			*d = f.vals[i].(int64)
		case *int32:
			*d = f.vals[i].(int32)
		case *bool:
			*d = f.vals[i].(bool)
		case *time.Time:
			*d = f.vals[i].(time.Time)
		case **time.Time:
			*d = f.vals[i].(*time.Time)
		case *[]byte:
			*d = f.vals[i].([]byte)
		}
	}
	return nil
}

func TestPostgresStore_HelpersAndScanners(t *testing.T) {
	if ps := NewPostgresStore(nil); ps == nil {
		t.Fatal("NewPostgresStore returned nil")
	}

	now := time.Now().UTC()
	var nilTime *time.Time
	vRow := &fakeRowScanner{
		vals: []any{
			"vid-1", "CODE1", "Name 1", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed,
			int64(10000), int32(0), int64(0), int64(50000), int32(10), int32(2), int32(1),
			int64(100000), int64(20000), now, now.Add(time.Hour), true, domain.VoucherStatusActive,
			[]byte(`["cat-1"]`), nilTime, int32(1), int64(1), int64(1), "admin-1", now, now,
		},
	}
	v, err := scanVoucher(vRow)
	if err != nil || v.ID != "vid-1" || v.Code != "CODE1" || v.ValueVND != 10000 || len(v.CategoryIDs) != 1 {
		t.Fatalf("scanVoucher unexpected: v=%+v err=%v", v, err)
	}
	if _, err := scanVoucher(&fakeRowScanner{err: errors.New("scan fail")}); err == nil {
		t.Fatal("expected scanVoucher error")
	}

	fsRow := &fakeRowScanner{
		vals: []any{
			"fs-1", "Flash 1", now, now.Add(time.Hour), domain.FlashWindowStatusActive,
			int32(15), []byte(`["cat-1"]`), "admin-1", now, now,
		},
	}
	fs, err := scanFlashSale(fsRow)
	if err != nil || fs.ID != "fs-1" || fs.Status != domain.FlashWindowStatusActive || fs.MinDiscountPercent != 15 {
		t.Fatalf("scanFlashSale unexpected: fs=%+v err=%v", fs, err)
	}

	relAt := now.Add(time.Minute)
	fiRow := &fakeRowScanner{
		vals: []any{
			"fi-1", "fs-1", "shop-1", "sku-1", int64(100000), int64(70000),
			int32(10), int32(5), int32(2), domain.FlashStatusApproved, "",
			"admin-1", domain.FlashWindowStatusEnded, &relAt, now, now.Add(time.Hour), now, now,
		},
	}
	fi, err := scanFlashSaleItem(fiRow)
	if err != nil || fi.ID != "fi-1" || fi.WindowStatus != domain.FlashWindowStatusEnded || fi.StockReleasedAt == nil {
		t.Fatalf("scanFlashSaleItem unexpected: fi=%+v err=%v", fi, err)
	}

	rRow := &fakeRowScanner{
		vals: []any{
			"ord-1", "u-1", domain.ReservationStatusReleased, int64(5000),
			[]byte(`[{"voucher_id":"v1","scope":"SHOP","shop_id":"s1","discount_vnd":10000,"has_budget":true}]`),
			[]byte(`[{"flash_sale_item_id":"fi1","sku_id":"sku1","quantity":2}]`),
			[]byte(`[]`),
			[]byte(`{"finalPayableVnd":"90000"}`),
			[]byte(`["s1"]`),
			now, &relAt, now,
		},
	}
	res, err := scanReservation(rRow)
	if err != nil || res.OrderID != "ord-1" || len(res.Vouchers) != 1 || len(res.FlashSaleItems) != 1 || res.ReleasedAt == nil || len(res.ReleasedShops) != 1 {
		t.Fatalf("scanReservation unexpected: res=%+v err=%v", res, err)
	}
	if _, err := scanReservation(&fakeRowScanner{err: errors.New("scan fail")}); err == nil {
		t.Fatal("expected scanReservation error")
	}
}
