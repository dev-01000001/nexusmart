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
