package calculator_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nexusmart/core/services/promotion/internal/calculator"
	"github.com/nexusmart/core/services/promotion/internal/domain"
)

func activeVoucher(id, code, scope, shopID, discType string, val int64, pct int32, maxDisc, minOrder, budget int64, now time.Time) *calculator.VoucherCandidate {
	return &calculator.VoucherCandidate{
		RequestedID: id,
		Found:       true,
		Voucher: &domain.Voucher{
			ID:              id,
			Code:            code,
			Name:            code,
			Scope:           scope,
			ShopID:          shopID,
			DiscountType:    discType,
			ValueVND:        val,
			DiscountPercent: pct,
			MaxDiscountVND:  maxDisc,
			MinOrderVND:     minOrder,
			Quota:           100,
			PerUserLimit:    1,
			BudgetVND:       budget,
			StartsAt:        now.Add(-time.Hour),
			EndsAt:          now.Add(time.Hour),
			IsActive:        true,
		},
		RemainingQuota:  100,
		RemainingBudget: budget,
		UserUsedCount:   0,
	}
}

func TestCalculate_StrictOrder_ShopPlatformFreeshipCoins_MultiShop(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	shopAVoucher := activeVoucher("v-shop-a", "SHOPA10", domain.VoucherScopeShop, "shop-a", domain.DiscountTypePercent, 0, 10, 25000, 100000, 0, now)
	shopBVoucher := activeVoucher("v-shop-b", "SHOPB20K", domain.VoucherScopeShop, "shop-b", domain.DiscountTypeFixed, 20000, 0, 0, 100000, 0, now)
	// Platform 10% applies on POST-shop-voucher subtotal (455,000 -> 45,500), NOT original 500,000 (50,000)!
	platformVoucher := activeVoucher("v-plat", "PLAT10", domain.VoucherScopePlatform, "", domain.DiscountTypePercent, 0, 10, 100000, 400000, 500000, now)
	freeshipVoucher := activeVoucher("v-ship", "FREESHIP35K", domain.VoucherScopeFreeship, "", domain.DiscountTypeFixed, 35000, 0, 0, 300000, 0, now)

	out, err := calculator.Calculate(calculator.Input{
		Now:    now,
		Strict: true,
		Shops: []calculator.ShopInput{
			{ShopID: "shop-a", SubtotalVND: 300000, ShippingFeeVND: 30000},
			{ShopID: "shop-b", SubtotalVND: 200000, ShippingFeeVND: 20000},
		},
		ShopVouchers: map[string]*calculator.VoucherCandidate{
			"shop-a": shopAVoucher,
			"shop-b": shopBVoucher,
		},
		PlatformVoucher: platformVoucher,
		FreeshipVoucher: freeshipVoucher,
		CoinsToUse:      10000,
		AvailableCoins:  50000,
	})
	if err != nil {
		t.Fatalf("Calculate failed: %v", err)
	}

	bd := out.Breakdown
	if bd.TotalSubtotalVnd != 500000 {
		t.Fatalf("expected subtotal 500000, got %d", bd.TotalSubtotalVnd)
	}
	if bd.TotalShopVoucherDiscountVnd != 45000 {
		t.Fatalf("expected shop voucher discount 45000 (25000+20000), got %d", bd.TotalShopVoucherDiscountVnd)
	}
	if bd.TotalPlatformVoucherDiscountVnd != 45500 {
		t.Fatalf("expected platform voucher discount 45500 (10%% of 455000 post-shop), got %d", bd.TotalPlatformVoucherDiscountVnd)
	}
	if bd.TotalFreeshipDiscountVnd != 35000 {
		t.Fatalf("expected freeship discount 35000, got %d", bd.TotalFreeshipDiscountVnd)
	}
	if bd.TotalCoinsDiscountVnd != 10000 || out.CoinsUsed != 10000 {
		t.Fatalf("expected coins discount 10000, got %d (coinsUsed=%d)", bd.TotalCoinsDiscountVnd, out.CoinsUsed)
	}
	if bd.ShopCostVnd != 45000 {
		t.Fatalf("expected shop_cost_vnd 45000, got %d", bd.ShopCostVnd)
	}
	if bd.PlatformCostVnd != 90500 {
		t.Fatalf("expected platform_cost_vnd 90500 (45500+35000+10000), got %d", bd.PlatformCostVnd)
	}
	if bd.TotalDiscountVnd != bd.ShopCostVnd+bd.PlatformCostVnd {
		t.Fatalf("cost bearer mismatch: total=%d shop=%d platform=%d", bd.TotalDiscountVnd, bd.ShopCostVnd, bd.PlatformCostVnd)
	}
	wantPayable := int64(500000 + 50000 - 135500)
	if bd.FinalPayableVnd != wantPayable {
		t.Fatalf("expected final_payable_vnd %d, got %d", wantPayable, bd.FinalPayableVnd)
	}

	// Verify exact integer allocation sums across shops
	var sumPlat, sumShip, sumCoins, sumPay int64
	for _, s := range bd.Shops {
		sumPlat += s.PlatformVoucherDiscountVnd
		sumShip += s.FreeshipDiscountVnd
		sumCoins += s.CoinsDiscountVnd
		sumPay += s.FinalPayableVnd
	}
	if sumPlat != bd.TotalPlatformVoucherDiscountVnd ||
		sumShip != bd.TotalFreeshipDiscountVnd ||
		sumCoins != bd.TotalCoinsDiscountVnd ||
		sumPay != bd.FinalPayableVnd {
		t.Fatalf("per-shop allocation sum drift: plat=%d ship=%d coins=%d pay=%d", sumPlat, sumShip, sumCoins, sumPay)
	}
	if len(out.AppliedVouchers) != 4 {
		t.Fatalf("expected 4 applied vouchers, got %d", len(out.AppliedVouchers))
	}
}

func TestCalculate_PlatformMinOrderCheckedPostShopVoucher(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	shopVoucher := activeVoucher("v-shop", "SHOP30K", domain.VoucherScopeShop, "shop-1", domain.DiscountTypeFixed, 30000, 0, 0, 100000, 0, now)
	// Original subtotal is 200,000 >= 180,000, but post-shop subtotal is 170,000 < 180,000!
	platformVoucher := activeVoucher("v-plat", "PLAT20K", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 20000, 0, 0, 180000, 0, now)

	input := calculator.Input{
		Now: now,
		Shops: []calculator.ShopInput{
			{ShopID: "shop-1", SubtotalVND: 200000, ShippingFeeVND: 20000},
		},
		ShopVouchers:    map[string]*calculator.VoucherCandidate{"shop-1": shopVoucher},
		PlatformVoucher: platformVoucher,
	}

	// Preview mode: platform voucher skipped with warning mentioning 10000 VND shortfall
	input.Strict = false
	preview, err := calculator.Calculate(input)
	if err != nil {
		t.Fatalf("Preview failed: %v", err)
	}
	if preview.Breakdown.TotalShopVoucherDiscountVnd != 30000 {
		t.Fatalf("expected shop voucher 30000, got %d", preview.Breakdown.TotalShopVoucherDiscountVnd)
	}
	if preview.Breakdown.TotalPlatformVoucherDiscountVnd != 0 {
		t.Fatalf("expected platform voucher 0 due to post-shop min_order, got %d", preview.Breakdown.TotalPlatformVoucherDiscountVnd)
	}
	if len(preview.Breakdown.Warnings) != 1 || !strings.Contains(preview.Breakdown.Warnings[0], "shortfall 10000 VND") {
		t.Fatalf("expected shortfall warning, got %v", preview.Breakdown.Warnings)
	}

	// Strict mode: returns ErrVoucherIneligible
	input.Strict = true
	_, err = calculator.Calculate(input)
	if !errors.Is(err, calculator.ErrVoucherIneligible) {
		t.Fatalf("expected ErrVoucherIneligible in strict mode, got %v", err)
	}
}

func TestCalculate_FreeshipCap_CoinsCap_AndRoundingExactness(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	// 3 equal shops (100k each), 10,000 VND platform voucher -> [3334, 3333, 3333]
	plat := activeVoucher("v-plat", "PLAT10K", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 10000, 0, 0, 0, 0, now)
	// Freeship 50,000 VND capped at total shipping 25,000 VND
	ship := activeVoucher("v-ship", "SHIP50K", domain.VoucherScopeFreeship, "", domain.DiscountTypeFixed, 50000, 0, 0, 0, 0, now)

	out, err := calculator.Calculate(calculator.Input{
		Now:    now,
		Strict: false,
		Shops: []calculator.ShopInput{
			{ShopID: "s1", SubtotalVND: 100000, ShippingFeeVND: 10000},
			{ShopID: "s2", SubtotalVND: 100000, ShippingFeeVND: 10000},
			{ShopID: "s3", SubtotalVND: 100000, ShippingFeeVND: 5000},
		},
		PlatformVoucher: plat,
		FreeshipVoucher: ship,
		CoinsToUse:      500000, // exceeds payable (290,000) and balance (350,000)
		AvailableCoins:  350000,
	})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	bd := out.Breakdown
	if bd.TotalFreeshipDiscountVnd != 25000 {
		t.Fatalf("expected freeship capped at 25000, got %d", bd.TotalFreeshipDiscountVnd)
	}
	if bd.TotalCoinsDiscountVnd != 290000 {
		t.Fatalf("expected coins capped at remaining payable 290000, got %d", bd.TotalCoinsDiscountVnd)
	}
	if bd.FinalPayableVnd != 0 {
		t.Fatalf("expected final payable 0, got %d", bd.FinalPayableVnd)
	}
	if bd.Shops[0].PlatformVoucherDiscountVnd != 3334 ||
		bd.Shops[1].PlatformVoucherDiscountVnd != 3333 ||
		bd.Shops[2].PlatformVoucherDiscountVnd != 3333 {
		t.Fatalf("unexpected deterministic rounding split: [%d, %d, %d]",
			bd.Shops[0].PlatformVoucherDiscountVnd,
			bd.Shops[1].PlatformVoucherDiscountVnd,
			bd.Shops[2].PlatformVoucherDiscountVnd)
	}
}

func TestCalculate_FlashSaleDiscountCostBearer(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	shopV := activeVoucher("v-shop", "SHOP10", domain.VoucherScopeShop, "shop-1", domain.DiscountTypePercent, 0, 10, 0, 200000, 0, now)
	fi := &domain.FlashSaleItem{
		ID:            "fi-1",
		ShopID:        "shop-1",
		SKUID:         "sku-1",
		FlashPriceVND: 120000,
		Status:        domain.FlashStatusApproved,
		StartsAt:      now.Add(-time.Minute),
		EndsAt:        now.Add(time.Hour),
	}

	out, err := calculator.Calculate(calculator.Input{
		Now:    now,
		Strict: true,
		Shops: []calculator.ShopInput{
			{
				ShopID:         "shop-1",
				ShippingFeeVND: 15000,
				Items: []calculator.LineItemInput{
					{SKUID: "sku-1", Quantity: 2, UnitPriceVND: 200000, FlashSaleItemID: "fi-1"},
				},
			},
		},
		FlashItems: map[string]calculator.FlashItemCandidate{
			"fi-1": {Item: fi, Found: true},
		},
		ShopVouchers: map[string]*calculator.VoucherCandidate{"shop-1": shopV},
	})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	bd := out.Breakdown
	// Subtotal = 400,000; Flash discount = (200k-120k)*2 = 160,000; Post-flash = 240,000
	// Shop voucher 10% of 240,000 = 24,000; Final payable = 240,000 - 24,000 + 15,000 = 231,000
	if bd.TotalFlashSaleDiscountVnd != 160000 {
		t.Fatalf("expected flash discount 160000, got %d", bd.TotalFlashSaleDiscountVnd)
	}
	if bd.TotalShopVoucherDiscountVnd != 24000 {
		t.Fatalf("expected shop voucher 24000 (10%% of 240000), got %d", bd.TotalShopVoucherDiscountVnd)
	}
	if bd.ShopCostVnd != 184000 || bd.PlatformCostVnd != 0 {
		t.Fatalf("expected shopCost=184000 platformCost=0, got shop=%d plat=%d", bd.ShopCostVnd, bd.PlatformCostVnd)
	}
	if bd.FinalPayableVnd != 231000 {
		t.Fatalf("expected final payable 231000, got %d", bd.FinalPayableVnd)
	}
}

func TestComputeVoucherDiscount_EdgeCases(t *testing.T) {
	if got := calculator.ComputeVoucherDiscount(nil, 100000); got != 0 {
		t.Fatalf("nil voucher: got %d, want 0", got)
	}
	fixed := &domain.Voucher{DiscountType: domain.DiscountTypeFixed, ValueVND: 50000}
	if got := calculator.ComputeVoucherDiscount(fixed, 0); got != 0 {
		t.Fatalf("zero base: got %d, want 0", got)
	}
	if got := calculator.ComputeVoucherDiscount(fixed, -1000); got != 0 {
		t.Fatalf("negative base: got %d, want 0", got)
	}
	// Fixed exceeding subtotal capped at baseAmount
	if got := calculator.ComputeVoucherDiscount(fixed, 30000); got != 30000 {
		t.Fatalf("fixed exceeding base: got %d, want 30000", got)
	}
	// Negative fixed value clamped to 0
	negFixed := &domain.Voucher{DiscountType: domain.DiscountTypeFixed, ValueVND: -500}
	if got := calculator.ComputeVoucherDiscount(negFixed, 50000); got != 0 {
		t.Fatalf("negative value: got %d, want 0", got)
	}
	// Percent with max cap vs uncapped
	pctCapped := &domain.Voucher{DiscountType: domain.DiscountTypePercent, DiscountPercent: 25, MaxDiscountVND: 40000}
	if got := calculator.ComputeVoucherDiscount(pctCapped, 300000); got != 40000 {
		t.Fatalf("percent capped: got %d, want 40000", got)
	}
	pctUncapped := &domain.Voucher{DiscountType: domain.DiscountTypePercent, DiscountPercent: 15, MaxDiscountVND: 0}
	if got := calculator.ComputeVoucherDiscount(pctUncapped, 200000); got != 30000 {
		t.Fatalf("percent uncapped: got %d, want 30000", got)
	}
	// Unknown discount type returns 0
	unknown := &domain.Voucher{DiscountType: "UNKNOWN", ValueVND: 10000}
	if got := calculator.ComputeVoucherDiscount(unknown, 100000); got != 0 {
		t.Fatalf("unknown type: got %d, want 0", got)
	}
}

func TestCalculate_IneligibilityBranchesAndEdgeCases(t *testing.T) {
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	// 1. All voucher ineligibility reasons in preview (warning) and strict (ErrVoucherIneligible)
	makeCand := func(mutate func(*calculator.VoucherCandidate)) *calculator.VoucherCandidate {
		c := activeVoucher("v1", "V1", domain.VoucherScopeShop, "shop-1", domain.DiscountTypeFixed, 20000, 0, 0, 50000, 50000, now)
		mutate(c)
		return c
	}

	voucherErrCases := []struct {
		name    string
		cand    *calculator.VoucherCandidate
		wantSub string
	}{
		{"not_found", &calculator.VoucherCandidate{RequestedID: "missing", Found: false}, "not found"},
		{"inactive", makeCand(func(c *calculator.VoucherCandidate) { c.Voucher.IsActive = false }), "inactive"},
		{"wrong_scope", makeCand(func(c *calculator.VoucherCandidate) { c.Voucher.Scope = domain.VoucherScopePlatform }), "scope"},
		{"wrong_shop", makeCand(func(c *calculator.VoucherCandidate) { c.Voucher.ShopID = "other-shop" }), "does not belong"},
		{"not_started", makeCand(func(c *calculator.VoucherCandidate) { c.Voucher.StartsAt = now.Add(time.Hour) }), "not started"},
		{"expired", makeCand(func(c *calculator.VoucherCandidate) { c.Voucher.EndsAt = now.Add(-time.Minute) }), "expired"},
		{"out_of_quota", makeCand(func(c *calculator.VoucherCandidate) { c.RemainingQuota = 0 }), "out of quota"},
		{"per_user_limit", makeCand(func(c *calculator.VoucherCandidate) { c.UserUsedCount = 1 }), "per-user limit"},
		{"budget_exhausted", makeCand(func(c *calculator.VoucherCandidate) { c.RemainingBudget = 10000 }), "budget exhausted"},
	}
	for _, tc := range voucherErrCases {
		t.Run("shop_voucher_"+tc.name, func(t *testing.T) {
			in := calculator.Input{
				Now:          now,
				Shops:        []calculator.ShopInput{{ShopID: "shop-1", SubtotalVND: 100000, ShippingFeeVND: 15000}},
				ShopVouchers: map[string]*calculator.VoucherCandidate{"shop-1": tc.cand},
			}
			prev, err := calculator.Calculate(in)
			if err != nil {
				t.Fatalf("preview error: %v", err)
			}
			if len(prev.Breakdown.Warnings) != 1 || !strings.Contains(prev.Breakdown.Warnings[0], tc.wantSub) {
				t.Fatalf("expected warning containing %q, got %v", tc.wantSub, prev.Breakdown.Warnings)
			}
			in.Strict = true
			if _, err := calculator.Calculate(in); !errors.Is(err, calculator.ErrVoucherIneligible) {
				t.Fatalf("strict: want ErrVoucherIneligible, got %v", err)
			}
		})
	}

	// 2. Extra shop voucher for a shop not present in items
	extraIn := calculator.Input{
		Now:   now,
		Shops: []calculator.ShopInput{{ShopID: "shop-1", SubtotalVND: 100000}},
		ShopVouchers: map[string]*calculator.VoucherCandidate{
			"shop-ghost": activeVoucher("vg", "VG", domain.VoucherScopeShop, "shop-ghost", domain.DiscountTypeFixed, 10000, 0, 0, 0, 0, now),
		},
	}
	prevExtra, err := calculator.Calculate(extraIn)
	if err != nil || len(prevExtra.Breakdown.Warnings) != 1 {
		t.Fatalf("extra shop preview unexpected: out=%+v err=%v", prevExtra, err)
	}
	extraIn.Strict = true
	if _, err := calculator.Calculate(extraIn); !errors.Is(err, calculator.ErrVoucherIneligible) {
		t.Fatalf("extra shop strict: want ErrVoucherIneligible, got %v", err)
	}

	// 3. Freeship ineligible in preview & strict
	badFreeship := activeVoucher("vf", "VF", domain.VoucherScopeFreeship, "", domain.DiscountTypeFixed, 15000, 0, 0, 500000, 0, now)
	fsIn := calculator.Input{
		Now:             now,
		Shops:           []calculator.ShopInput{{ShopID: "shop-1", SubtotalVND: 100000, ShippingFeeVND: 20000}},
		FreeshipVoucher: badFreeship,
	}
	if prev, err := calculator.Calculate(fsIn); err != nil || len(prev.Breakdown.Warnings) != 1 {
		t.Fatalf("freeship preview unexpected: %+v err=%v", prev, err)
	}
	fsIn.Strict = true
	if _, err := calculator.Calculate(fsIn); !errors.Is(err, calculator.ErrVoucherIneligible) {
		t.Fatalf("freeship strict: want ErrVoucherIneligible, got %v", err)
	}

	// 4. Flash sale item errors (not found, unapproved, ended/inactive, SKU mismatch) + flash discount capped at subtotal
	flashErrCases := []struct {
		name string
		cand calculator.FlashItemCandidate
	}{
		{"not_found", calculator.FlashItemCandidate{Found: false}},
		{"not_approved", calculator.FlashItemCandidate{Found: true, Item: &domain.FlashSaleItem{ID: "fi", Status: domain.FlashStatusPending, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}}},
		{"ended_window", calculator.FlashItemCandidate{Found: true, Item: &domain.FlashSaleItem{ID: "fi", Status: domain.FlashStatusApproved, WindowStatus: domain.FlashWindowStatusEnded, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}}},
		{"sku_mismatch", calculator.FlashItemCandidate{Found: true, Item: &domain.FlashSaleItem{ID: "fi", SKUID: "sku-other", Status: domain.FlashStatusApproved, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)}}},
	}
	for _, tc := range flashErrCases {
		t.Run("flash_"+tc.name, func(t *testing.T) {
			in := calculator.Input{
				Now: now,
				Shops: []calculator.ShopInput{{
					ShopID: "shop-1", SubtotalVND: 100000,
					Items: []calculator.LineItemInput{{SKUID: "sku-1", Quantity: 1, UnitPriceVND: 100000, FlashSaleItemID: "fi"}},
				}},
				FlashItems: map[string]calculator.FlashItemCandidate{"fi": tc.cand},
			}
			prev, err := calculator.Calculate(in)
			if err != nil || len(prev.Breakdown.Warnings) != 1 {
				t.Fatalf("preview flash %s unexpected: %+v err=%v", tc.name, prev, err)
			}
			in.Strict = true
			if _, err := calculator.Calculate(in); !errors.Is(err, calculator.ErrInvalidFlashSaleItem) {
				t.Fatalf("strict flash %s: want ErrInvalidFlashSaleItem, got %v", tc.name, err)
			}
		})
	}

	// Flash discount exceeding subtotal is capped at subtotal; negative subtotal/shipping clamped to 0; zero-time Now defaulted
	capOut, err := calculator.Calculate(calculator.Input{
		Shops: []calculator.ShopInput{
			{ShopID: "s-neg", SubtotalVND: -500, ShippingFeeVND: -100},
			{
				ShopID: "s-cap", SubtotalVND: 50000, ShippingFeeVND: 0,
				Items: []calculator.LineItemInput{{SKUID: "sku-1", Quantity: 2, UnitPriceVND: 100000, FlashSaleItemID: "fi-cap"}},
			},
		},
		FlashItems: map[string]calculator.FlashItemCandidate{
			"fi-cap": {Found: true, Item: &domain.FlashSaleItem{
				ID: "fi-cap", SKUID: "sku-1", FlashPriceVND: 10000, Status: domain.FlashStatusApproved,
				StartsAt: time.Now().UTC().Add(-time.Hour), EndsAt: time.Now().UTC().Add(time.Hour),
			}},
		},
	})
	if err != nil {
		t.Fatalf("capOut failed: %v", err)
	}
	if capOut.Breakdown.Shops[0].SubtotalVnd != 0 || capOut.Breakdown.Shops[0].ShippingFeeVnd != 0 {
		t.Fatalf("negative subtotal/shipping not clamped: %+v", capOut.Breakdown.Shops[0])
	}
	if capOut.Breakdown.Shops[1].FlashSaleDiscountVnd != 50000 || capOut.Breakdown.FinalPayableVnd != 0 {
		t.Fatalf("flash discount should cap at 50000 subtotal: %+v", capOut.Breakdown)
	}

	// 5. Coins strict insufficient, negative available coins, preview warning, and empty shops
	coinsIn := calculator.Input{
		Now:            now,
		Strict:         true,
		Shops:          []calculator.ShopInput{{ShopID: "s1", SubtotalVND: 100000}},
		CoinsToUse:     5000,
		AvailableCoins: -100,
	}
	if _, err := calculator.Calculate(coinsIn); !errors.Is(err, calculator.ErrInsufficientCoins) {
		t.Fatalf("expected ErrInsufficientCoins, got %v", err)
	}
	coinsIn.Strict = false
	coinsIn.AvailableCoins = 2000
	prevCoins, err := calculator.Calculate(coinsIn)
	if err != nil || prevCoins.CoinsUsed != 2000 || len(prevCoins.Breakdown.Warnings) != 1 {
		t.Fatalf("preview insufficient coins unexpected: out=%+v err=%v", prevCoins, err)
	}

	// Zero-weight shops + platform voucher exceeding totalWeight exercises allocateProportional edge paths
	zeroOut, err := calculator.Calculate(calculator.Input{
		Now: now,
		Shops: []calculator.ShopInput{
			{ShopID: "s1", SubtotalVND: 0, ShippingFeeVND: 0},
			{ShopID: "s2", SubtotalVND: 10000, ShippingFeeVND: 0},
		},
		PlatformVoucher: activeVoucher("vp", "VP", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 10000, 0, 0, 0, 0, now),
	})
	if err != nil || zeroOut.Breakdown.Shops[1].PlatformVoucherDiscountVnd != 10000 {
		t.Fatalf("zero-weight proportional alloc unexpected: %+v err=%v", zeroOut, err)
	}
}
