package calculator_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nexusmart/core/services/promotion/internal/calculator"
	"github.com/nexusmart/core/services/promotion/internal/domain"
)

func assertAccountingIdentities(t *testing.T, out *calculator.Output) {
	t.Helper()
	bd := out.Breakdown
	if bd.FinalPayableVnd < 0 {
		t.Fatalf("final_payable_vnd must be non-negative, got %d", bd.FinalPayableVnd)
	}
	if bd.TotalDiscountVnd != bd.ShopCostVnd+bd.PlatformCostVnd {
		t.Fatalf("total_discount (%d) != shop_cost (%d) + platform_cost (%d)",
			bd.TotalDiscountVnd, bd.ShopCostVnd, bd.PlatformCostVnd)
	}

	wantShopCost := bd.TotalFlashSaleDiscountVnd +
		bd.TotalShopProgramDiscountVnd +
		bd.TotalShopVoucherDiscountVnd +
		bd.TotalShopFreeshipDiscountVnd
	if bd.ShopCostVnd != wantShopCost {
		t.Fatalf("shop_cost_vnd (%d) != F(%d)+P_shop(%d)+V_shop(%d)+S_shop(%d) = %d",
			bd.ShopCostVnd, bd.TotalFlashSaleDiscountVnd, bd.TotalShopProgramDiscountVnd,
			bd.TotalShopVoucherDiscountVnd, bd.TotalShopFreeshipDiscountVnd, wantShopCost)
	}

	wantPlatCost := bd.TotalPlatformVoucherDiscountVnd +
		bd.TotalFreeshipDiscountVnd +
		bd.TotalCoinsDiscountVnd
	if bd.PlatformCostVnd != wantPlatCost {
		t.Fatalf("platform_cost_vnd (%d) != V_plat(%d)+S_free(%d)+X(%d) = %d",
			bd.PlatformCostVnd, bd.TotalPlatformVoucherDiscountVnd,
			bd.TotalFreeshipDiscountVnd, bd.TotalCoinsDiscountVnd, wantPlatCost)
	}

	var sumSub, sumShip, sumF, sumPShop, sumVShop, sumSShop, sumVPlat, sumSFree, sumX, sumPay int64
	for _, s := range bd.Shops {
		if s.FinalPayableVnd < 0 {
			t.Fatalf("shop %s final_payable_vnd negative: %d", s.ShopId, s.FinalPayableVnd)
		}
		if s.FinalShippingFeeVnd < 0 {
			t.Fatalf("shop %s final_shipping_fee_vnd negative: %d", s.ShopId, s.FinalShippingFeeVnd)
		}
		// §1 seller net revenue R = G - F - P_shop - V_shop - S_shop >= 0
		r := s.SubtotalVnd - s.FlashSaleDiscountVnd - s.ShopProgramDiscountVnd - s.ShopVoucherDiscountVnd - s.ShopFreeshipDiscountVnd
		if r < 0 {
			t.Fatalf("shop %s seller net revenue R negative: %d (G=%d F=%d P_shop=%d V_shop=%d S_shop=%d)",
				s.ShopId, r, s.SubtotalVnd, s.FlashSaleDiscountVnd, s.ShopProgramDiscountVnd, s.ShopVoucherDiscountVnd, s.ShopFreeshipDiscountVnd)
		}
		// §1 platform subsidy D = V_plat + S_free + X
		d := s.PlatformVoucherDiscountVnd + s.FreeshipDiscountVnd + s.CoinsDiscountVnd
		// §1 identity: P + D == R + Ship
		if s.FinalPayableVnd+d != r+s.ShippingFeeVnd {
			t.Fatalf("shop %s identity P(%d)+D(%d)=%d != R(%d)+Ship(%d)=%d",
				s.ShopId, s.FinalPayableVnd, d, s.FinalPayableVnd+d, r, s.ShippingFeeVnd, r+s.ShippingFeeVnd)
		}
		if s.FinalShippingFeeVnd != s.ShippingFeeVnd-s.ShopFreeshipDiscountVnd-s.FreeshipDiscountVnd {
			t.Fatalf("shop %s final shipping mismatch: got %d, want %d",
				s.ShopId, s.FinalShippingFeeVnd, s.ShippingFeeVnd-s.ShopFreeshipDiscountVnd-s.FreeshipDiscountVnd)
		}
		sumSub += s.SubtotalVnd
		sumShip += s.ShippingFeeVnd
		sumF += s.FlashSaleDiscountVnd
		sumPShop += s.ShopProgramDiscountVnd
		sumVShop += s.ShopVoucherDiscountVnd
		sumSShop += s.ShopFreeshipDiscountVnd
		sumVPlat += s.PlatformVoucherDiscountVnd
		sumSFree += s.FreeshipDiscountVnd
		sumX += s.CoinsDiscountVnd
		sumPay += s.FinalPayableVnd
	}
	if sumSub != bd.TotalSubtotalVnd ||
		sumShip != bd.TotalShippingFeeVnd ||
		sumF != bd.TotalFlashSaleDiscountVnd ||
		sumPShop != bd.TotalShopProgramDiscountVnd ||
		sumVShop != bd.TotalShopVoucherDiscountVnd ||
		sumSShop != bd.TotalShopFreeshipDiscountVnd ||
		sumVPlat != bd.TotalPlatformVoucherDiscountVnd ||
		sumSFree != bd.TotalFreeshipDiscountVnd ||
		sumX != bd.TotalCoinsDiscountVnd ||
		sumPay != bd.FinalPayableVnd {
		t.Fatalf("per-shop sums disagree with totals: sumPay=%d totalPay=%d sumPShop=%d totalPShop=%d sumSShop=%d totalSShop=%d",
			sumPay, bd.FinalPayableVnd, sumPShop, bd.TotalShopProgramDiscountVnd, sumSShop, bd.TotalShopFreeshipDiscountVnd)
	}
}

func TestCalculate_W2B_TableDriven_FullFormulaAndEdges(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	liveFlash := &domain.FlashSaleItem{
		ID:            "fi-1",
		ShopID:        "shop-1",
		SKUID:         "sku-1",
		FlashPriceVND: 120000,
		Status:        domain.FlashStatusApproved,
		StartsAt:      now.Add(-time.Hour),
		EndsAt:        now.Add(time.Hour),
	}
	liveProg1 := &domain.ShopDiscountProgramItem{
		ID:                 "spi-1",
		ProgramID:          "prog-1",
		ShopID:             "shop-1",
		SKUID:              "sku-1",
		OriginalPriceVND:   200000,
		DiscountedPriceVND: 150000,
		DiscountPercent:    25,
		PerUserLimit:       3,
		ProgramStatus:      domain.ShopProgramStatusActive,
		StartsAt:           now.Add(-time.Hour),
		EndsAt:             now.Add(time.Hour),
	}
	liveProg2 := &domain.ShopDiscountProgramItem{
		ID:                 "spi-2",
		ProgramID:          "prog-1",
		ShopID:             "shop-1",
		SKUID:              "sku-2",
		OriginalPriceVND:   100000,
		DiscountedPriceVND: 80000,
		DiscountPercent:    20,
		PerUserLimit:       2,
		ProgramStatus:      domain.ShopProgramStatusActive,
		StartsAt:           now.Add(-time.Hour),
		EndsAt:             now.Add(time.Hour),
	}

	cases := []struct {
		name           string
		input          calculator.Input
		wantErr        error
		wantFlash      int64
		wantShopProg   int64
		wantShopV      int64
		wantPlatV      int64
		wantShopFree   int64
		wantPlatFree   int64
		wantCoins      int64
		wantPayable    int64
		wantWarnSubstr string
	}{
		{
			name: "shop_program_then_shop_voucher_then_cat_plat_then_shop_freeship_then_plat_freeship_then_coins",
			input: calculator.Input{
				Now:    now,
				Strict: true,
				Shops: []calculator.ShopInput{
					{
						ShopID:              "shop-1",
						ShippingFeeVND:      30000,
						FreeShipEnabled:     true,
						FreeShipMinOrderVND: 150000,
						FreeShipMaxVND:      15000,
						Items: []calculator.LineItemInput{
							// sku-1: 200k -> shop program 150k (-50k), cat-shirt (eligible)
							{SKUID: "sku-1", Quantity: 1, UnitPriceVND: 200000, CategoryID: "cat-shirt", ShopDiscountProgramItemID: "spi-1"},
							// sku-2: 100k -> shop program 80k (-20k), cat-elec (NOT eligible)
							{SKUID: "sku-2", Quantity: 1, UnitPriceVND: 100000, CategoryID: "cat-elec", ShopDiscountProgramItemID: "spi-2"},
						},
					},
				},
				ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{
					"spi-1": {Item: liveProg1, Found: true},
					"spi-2": {Item: liveProg2, Found: true},
				},
				// After shop program: 150k + 80k = 230k. Shop voucher -30k -> afterShop = 200k (>= 150k freeship min!).
				// Shop voucher 30k split proportionally across (150k, 80k):
				//   line1 share = floor(30000*150000/230000) = 19565, rem 50000/230000
				//   line2 share = floor(30000*80000/230000) = 10434, rem 180000/230000 -> +1 => 10435
				//   line1 afterShop = 150000 - 19565 = 130435 (eligible for cat-shirt!)
				// Platform voucher 10% on eligible (130435) = 13043.
				// Shop freeship = min(30000, 15000) = 15000 (remaining ship = 15000).
				// Platform freeship 20000 capped at remaining ship 15000!
				// Coins 5000 -> payable = (200000 - 13043) + 0 - 5000 = 181957.
				ShopVouchers: map[string]*calculator.VoucherCandidate{
					"shop-1": activeVoucher("vs1", "VS30K", domain.VoucherScopeShop, "shop-1", domain.DiscountTypeFixed, 30000, 0, 0, 200000, 0, now),
				},
				PlatformVoucher: func() *calculator.VoucherCandidate {
					vc := activeVoucher("vp1", "PLATCAT10", domain.VoucherScopePlatform, "", domain.DiscountTypePercent, 0, 10, 50000, 100000, 0, now)
					vc.Voucher.CategoryIDs = []string{"cat-fashion"}
					vc.AllowedCategoryIDs = map[string]bool{"cat-fashion": true, "cat-shirt": true}
					return vc
				}(),
				FreeshipVoucher: activeVoucher("vf1", "FS20K", domain.VoucherScopeFreeship, "", domain.DiscountTypeFixed, 20000, 0, 0, 100000, 0, now),
				CoinsToUse:      5000,
				AvailableCoins:  10000,
			},
			wantFlash:    0,
			wantShopProg: 70000,
			wantShopV:    30000,
			wantPlatV:    13043,
			wantShopFree: 15000,
			wantPlatFree: 15000,
			wantCoins:    5000,
			wantPayable:  181957,
		},
		{
			name: "flash_wins_over_shop_program_no_stacking",
			input: calculator.Input{
				Now:    now,
				Strict: false,
				Shops: []calculator.ShopInput{
					{
						ShopID:         "shop-1",
						ShippingFeeVND: 20000,
						Items: []calculator.LineItemInput{
							// Both flash_sale_item_id AND shop_discount_program_item_id on same SKU!
							// Flash (120k, disc 80k) MUST win; shop program (150k, disc 50k) MUST be 0!
							{SKUID: "sku-1", Quantity: 2, UnitPriceVND: 200000, FlashSaleItemID: "fi-1", ShopDiscountProgramItemID: "spi-1"},
						},
					},
				},
				FlashItems: map[string]calculator.FlashItemCandidate{
					"fi-1": {Item: liveFlash, Found: true},
				},
				ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{
					"spi-1": {Item: liveProg1, Found: true},
				},
			},
			wantFlash:      160000,
			wantShopProg:   0,
			wantPayable:    260000, // 400k - 160k + 20k
			wantWarnSubstr: "precedence",
		},
		{
			name: "shop_freeship_trimmed_so_R_never_negative",
			input: calculator.Input{
				Now:    now,
				Strict: true,
				Shops: []calculator.ShopInput{
					{
						ShopID:              "shop-1",
						SubtotalVND:         100000,
						ShippingFeeVND:      30000,
						FreeShipEnabled:     true,
						FreeShipMinOrderVND: 0,
						FreeShipMaxVND:      30000,
					},
				},
				// Shop voucher -90k -> afterShop = 10k. Shop freeship max 30k MUST trim to 10k so R = 0 >= 0!
				// Platform freeship 25k applies on remaining shipping (30k - 10k = 20k) -> 20k!
				ShopVouchers: map[string]*calculator.VoucherCandidate{
					"shop-1": activeVoucher("vs90", "VS90K", domain.VoucherScopeShop, "shop-1", domain.DiscountTypeFixed, 90000, 0, 0, 0, 0, now),
				},
				FreeshipVoucher: activeVoucher("vf25", "FS25K", domain.VoucherScopeFreeship, "", domain.DiscountTypeFixed, 25000, 0, 0, 0, 0, now),
			},
			wantShopV:    90000,
			wantShopFree: 10000,
			wantPlatFree: 20000,
			wantPayable:  10000, // R=0, P = 10k goods + 0 ship
		},
		{
			name: "zero_vnd_boundary_all_discounts_and_coins_exact_zero",
			input: calculator.Input{
				Now:    now,
				Strict: true,
				Shops: []calculator.ShopInput{
					{
						ShopID:              "shop-1",
						SubtotalVND:         100000,
						ShippingFeeVND:      25000,
						FreeShipEnabled:     true,
						FreeShipMinOrderVND: 50000,
						FreeShipMaxVND:      25000,
					},
				},
				// afterShop = 100k, V_plat = 100k -> afterPlatform = 0; S_shop = 25k -> R = 75k >= 0, ship = 0, P = 0!
				PlatformVoucher: activeVoucher("vp100", "VP100K", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 100000, 0, 0, 0, 0, now),
				CoinsToUse:      50000,
				AvailableCoins:  50000,
			},
			wantPlatV:    100000,
			wantShopFree: 25000,
			wantCoins:    0,
			wantPayable:  0,
		},
		{
			name: "shop_freeship_threshold_not_met_after_shop_voucher",
			input: calculator.Input{
				Now:    now,
				Strict: true,
				Shops: []calculator.ShopInput{
					{
						ShopID:              "shop-1",
						SubtotalVND:         200000,
						ShippingFeeVND:      25000,
						FreeShipEnabled:     true,
						FreeShipMinOrderVND: 190000,
						FreeShipMaxVND:      25000,
					},
				},
				// 200k - 20k = 180k < 190k threshold -> shop freeship MUST NOT apply!
				ShopVouchers: map[string]*calculator.VoucherCandidate{
					"shop-1": activeVoucher("vs20", "VS20K", domain.VoucherScopeShop, "shop-1", domain.DiscountTypeFixed, 20000, 0, 0, 0, 0, now),
				},
			},
			wantShopV:    20000,
			wantShopFree: 0,
			wantPayable:  205000,
		},
		{
			name: "category_scoped_platform_voucher_no_matching_items_rejected_strict",
			input: calculator.Input{
				Now:    now,
				Strict: true,
				Shops: []calculator.ShopInput{
					{
						ShopID:         "shop-1",
						ShippingFeeVND: 20000,
						Items: []calculator.LineItemInput{
							{SKUID: "sku-e", Quantity: 1, UnitPriceVND: 300000, CategoryID: "cat-elec"},
						},
					},
				},
				PlatformVoucher: func() *calculator.VoucherCandidate {
					vc := activeVoucher("vpcat", "FASHIONONLY", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 30000, 0, 0, 100000, 0, now)
					vc.Voucher.CategoryIDs = []string{"cat-fashion"}
					vc.AllowedCategoryIDs = map[string]bool{"cat-fashion": true, "cat-shirt": true}
					return vc
				}(),
			},
			wantErr: calculator.ErrVoucherIneligible,
		},
		{
			name: "paused_voucher_rejected_even_if_is_active_stale",
			input: calculator.Input{
				Now:    now,
				Strict: true,
				Shops: []calculator.ShopInput{
					{ShopID: "shop-1", SubtotalVND: 200000, ShippingFeeVND: 20000},
				},
				PlatformVoucher: func() *calculator.VoucherCandidate {
					vc := activeVoucher("vpause", "PAUSED1", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 20000, 0, 0, 0, 0, now)
					vc.Voucher.Status = domain.VoucherStatusPaused
					vc.Voucher.IsActive = false
					return vc
				}(),
			},
			wantErr: calculator.ErrVoucherIneligible,
		},
		{
			name: "deleted_voucher_rejected",
			input: calculator.Input{
				Now:    now,
				Strict: true,
				Shops: []calculator.ShopInput{
					{ShopID: "shop-1", SubtotalVND: 200000, ShippingFeeVND: 20000},
				},
				PlatformVoucher: func() *calculator.VoucherCandidate {
					vc := activeVoucher("vdel", "DEL1", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 20000, 0, 0, 0, 0, now)
					vc.Voucher.Status = domain.VoucherStatusDeleted
					vc.Voucher.IsActive = false
					return vc
				}(),
			},
			wantErr: calculator.ErrVoucherIneligible,
		},
		{
			name: "flash_sale_at_exact_ends_at_boundary_rejected",
			input: calculator.Input{
				Now:    now.Add(time.Hour), // exact EndsAt -> half-open [StartsAt, EndsAt) means ended!
				Strict: true,
				Shops: []calculator.ShopInput{
					{
						ShopID:         "shop-1",
						ShippingFeeVND: 15000,
						Items: []calculator.LineItemInput{
							{SKUID: "sku-1", Quantity: 1, UnitPriceVND: 200000, FlashSaleItemID: "fi-1"},
						},
					},
				},
				FlashItems: map[string]calculator.FlashItemCandidate{
					"fi-1": {Item: liveFlash, Found: true},
				},
			},
			wantErr: calculator.ErrInvalidFlashSaleItem,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := calculator.Calculate(tc.input)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertAccountingIdentities(t, out)
			bd := out.Breakdown
			if bd.TotalFlashSaleDiscountVnd != tc.wantFlash {
				t.Fatalf("flash: got %d want %d", bd.TotalFlashSaleDiscountVnd, tc.wantFlash)
			}
			if bd.TotalShopProgramDiscountVnd != tc.wantShopProg {
				t.Fatalf("shop_program: got %d want %d", bd.TotalShopProgramDiscountVnd, tc.wantShopProg)
			}
			if bd.TotalShopVoucherDiscountVnd != tc.wantShopV {
				t.Fatalf("shop_voucher: got %d want %d", bd.TotalShopVoucherDiscountVnd, tc.wantShopV)
			}
			if bd.TotalPlatformVoucherDiscountVnd != tc.wantPlatV {
				t.Fatalf("platform_voucher: got %d want %d", bd.TotalPlatformVoucherDiscountVnd, tc.wantPlatV)
			}
			if bd.TotalShopFreeshipDiscountVnd != tc.wantShopFree {
				t.Fatalf("shop_freeship: got %d want %d", bd.TotalShopFreeshipDiscountVnd, tc.wantShopFree)
			}
			if bd.TotalFreeshipDiscountVnd != tc.wantPlatFree {
				t.Fatalf("platform_freeship: got %d want %d", bd.TotalFreeshipDiscountVnd, tc.wantPlatFree)
			}
			if bd.TotalCoinsDiscountVnd != tc.wantCoins {
				t.Fatalf("coins: got %d want %d", bd.TotalCoinsDiscountVnd, tc.wantCoins)
			}
			if bd.FinalPayableVnd != tc.wantPayable {
				t.Fatalf("final_payable: got %d want %d", bd.FinalPayableVnd, tc.wantPayable)
			}
			if tc.wantWarnSubstr != "" {
				found := false
				for _, w := range bd.Warnings {
					if strings.Contains(strings.ToLower(w), strings.ToLower(tc.wantWarnSubstr)) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected warning containing %q, got %v", tc.wantWarnSubstr, bd.Warnings)
				}
			}
		})
	}
}

func TestCalculate_W2B_MultiShop_CategoryScopedAllocation_OnlyToEligibleShop(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	plat := activeVoucher("vp-fash", "FASH20K", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 20000, 0, 0, 100000, 0, now)
	plat.Voucher.CategoryIDs = []string{"cat-fashion"}
	plat.AllowedCategoryIDs = map[string]bool{"cat-fashion": true, "cat-tee": true}

	out, err := calculator.Calculate(calculator.Input{
		Now:    now,
		Strict: true,
		Shops: []calculator.ShopInput{
			{
				ShopID:         "shop-fashion",
				ShippingFeeVND: 20000,
				Items:          []calculator.LineItemInput{{SKUID: "s1", Quantity: 1, UnitPriceVND: 150000, CategoryID: "cat-tee"}},
			},
			{
				ShopID:         "shop-elec",
				ShippingFeeVND: 20000,
				Items:          []calculator.LineItemInput{{SKUID: "s2", Quantity: 1, UnitPriceVND: 500000, CategoryID: "cat-phone"}},
			},
		},
		PlatformVoucher: plat,
	})
	if err != nil {
		t.Fatalf("Calculate: %v", err)
	}
	assertAccountingIdentities(t, out)
	if out.Breakdown.Shops[0].PlatformVoucherDiscountVnd != 20000 {
		t.Fatalf("expected all 20000 platform discount on fashion shop, got %d", out.Breakdown.Shops[0].PlatformVoucherDiscountVnd)
	}
	if out.Breakdown.Shops[1].PlatformVoucherDiscountVnd != 0 {
		t.Fatalf("expected 0 platform discount on electronics shop, got %d", out.Breakdown.Shops[1].PlatformVoucherDiscountVnd)
	}
}

func TestCalculate_W2B_ShopProgramPerUserLimit_RegularPriceAndAppliedIDs(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	progA := &domain.ShopDiscountProgramItem{
		ID:                 "sp-a",
		ProgramID:          "prog-1",
		ShopID:             "shop-1",
		SKUID:              "sku-a",
		OriginalPriceVND:   200000,
		DiscountedPriceVND: 160000,
		PerUserLimit:       2,
		ProgramStatus:      domain.ShopProgramStatusActive,
		StartsAt:           now.Add(-time.Hour),
		EndsAt:             now.Add(time.Hour),
	}
	progB := &domain.ShopDiscountProgramItem{
		ID:                 "sp-b",
		ProgramID:          "prog-1",
		ShopID:             "shop-1",
		SKUID:              "sku-b",
		OriginalPriceVND:   100000,
		DiscountedPriceVND: 80000,
		PerUserLimit:       2,
		ProgramStatus:      domain.ShopProgramStatusActive,
		StartsAt:           now.Add(-time.Hour),
		EndsAt:             now.Add(time.Hour),
	}

	// 1. Single line exceeding per_user_limit: Preview (Strict=false) and Reserve (Strict=true) MUST agree
	// and bill the entire line at regular price, with warning and empty applied_shop_program_item_ids.
	makeExceededInput := func(strict bool) calculator.Input {
		return calculator.Input{
			Now:    now,
			Strict: strict,
			Shops: []calculator.ShopInput{
				{
					ShopID:         "shop-1",
					ShippingFeeVND: 20000,
					Items: []calculator.LineItemInput{
						{SKUID: "sku-a", Quantity: 2, UnitPriceVND: 200000, ShopDiscountProgramItemID: "sp-a"},
					},
				},
			},
			ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{
				"sp-a": {Item: progA, Found: true, UserUsedCount: 1}, // 1 + 2 = 3 > limit 2 -> whole line regular price!
			},
		}
	}

	prevOut, err := calculator.Calculate(makeExceededInput(false))
	if err != nil {
		t.Fatalf("Preview Calculate: %v", err)
	}
	resOut, err := calculator.Calculate(makeExceededInput(true))
	if err != nil {
		t.Fatalf("Reserve (Strict) Calculate must NOT error on per_user_limit exceeded, got %v", err)
	}
	assertAccountingIdentities(t, prevOut)
	assertAccountingIdentities(t, resOut)

	if prevOut.Breakdown.TotalShopProgramDiscountVnd != 0 || resOut.Breakdown.TotalShopProgramDiscountVnd != 0 {
		t.Fatalf("expected 0 shop program discount, got prev=%d res=%d",
			prevOut.Breakdown.TotalShopProgramDiscountVnd, resOut.Breakdown.TotalShopProgramDiscountVnd)
	}
	if prevOut.Breakdown.FinalPayableVnd != 420000 || resOut.Breakdown.FinalPayableVnd != 420000 {
		t.Fatalf("expected regular price payable 420000, got prev=%d res=%d",
			prevOut.Breakdown.FinalPayableVnd, resOut.Breakdown.FinalPayableVnd)
	}
	if len(resOut.AppliedShopPrograms) != 0 || len(resOut.Breakdown.Shops[0].AppliedShopProgramItemIds) != 0 {
		t.Fatalf("expected no applied shop programs, got applied=%+v ids=%v",
			resOut.AppliedShopPrograms, resOut.Breakdown.Shops[0].AppliedShopProgramItemIds)
	}
	if len(prevOut.Breakdown.Warnings) == 0 || len(resOut.Breakdown.Warnings) != len(prevOut.Breakdown.Warnings) ||
		prevOut.Breakdown.Warnings[0] != resOut.Breakdown.Warnings[0] {
		t.Fatalf("expected identical non-empty warnings, got prev=%v res=%v",
			prevOut.Breakdown.Warnings, resOut.Breakdown.Warnings)
	}

	// 2. Multi-line same shop: line 1 exceeds limit (regular price), line 2 within limit (discounted & applied).
	multiOut, err := calculator.Calculate(calculator.Input{
		Now:    now,
		Strict: true,
		Shops: []calculator.ShopInput{
			{
				ShopID:         "shop-1",
				ShippingFeeVND: 20000,
				Items: []calculator.LineItemInput{
					{SKUID: "sku-a", Quantity: 1, UnitPriceVND: 200000, ShopDiscountProgramItemID: "sp-a"},
					{SKUID: "sku-b", Quantity: 1, UnitPriceVND: 100000, ShopDiscountProgramItemID: "sp-b"},
				},
			},
		},
		ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{
			"sp-a": {Item: progA, Found: true, UserUsedCount: 2},
			"sp-b": {Item: progB, Found: true, UserUsedCount: 0},
		},
	})
	if err != nil {
		t.Fatalf("multi-line strict Calculate: %v", err)
	}
	assertAccountingIdentities(t, multiOut)
	if multiOut.Breakdown.TotalShopProgramDiscountVnd != 20000 || multiOut.Breakdown.FinalPayableVnd != 300000 {
		t.Fatalf("expected disc=20000 payable=300000, got disc=%d payable=%d",
			multiOut.Breakdown.TotalShopProgramDiscountVnd, multiOut.Breakdown.FinalPayableVnd)
	}
	if len(multiOut.AppliedShopPrograms) != 1 || multiOut.AppliedShopPrograms[0].ItemID != "sp-b" {
		t.Fatalf("expected AppliedShopPrograms=[sp-b], got %+v", multiOut.AppliedShopPrograms)
	}
	gotIDs := multiOut.Breakdown.Shops[0].AppliedShopProgramItemIds
	if len(gotIDs) != 1 || gotIDs[0] != "sp-b" {
		t.Fatalf("expected AppliedShopProgramItemIds=[sp-b], got %v", gotIDs)
	}
	if len(multiOut.Breakdown.Warnings) == 0 {
		t.Fatalf("expected warning for exceeded line sp-a")
	}

	// 3. Other strict errors (missing, ended, wrong SKU, wrong shop) MUST still fail in Strict mode.
	endedProg := *progA
	endedProg.ProgramStatus = domain.ShopProgramStatusEnded
	for _, tc := range []struct {
		name string
		item calculator.LineItemInput
		cand map[string]calculator.ShopProgramItemCandidate
	}{
		{
			name: "not_found",
			item: calculator.LineItemInput{SKUID: "sku-a", Quantity: 1, UnitPriceVND: 200000, ShopDiscountProgramItemID: "sp-missing"},
			cand: map[string]calculator.ShopProgramItemCandidate{"sp-missing": {Found: false}},
		},
		{
			name: "ended",
			item: calculator.LineItemInput{SKUID: "sku-a", Quantity: 1, UnitPriceVND: 200000, ShopDiscountProgramItemID: "sp-a"},
			cand: map[string]calculator.ShopProgramItemCandidate{"sp-a": {Item: &endedProg, Found: true}},
		},
		{
			name: "wrong_sku",
			item: calculator.LineItemInput{SKUID: "sku-wrong", Quantity: 1, UnitPriceVND: 200000, ShopDiscountProgramItemID: "sp-a"},
			cand: map[string]calculator.ShopProgramItemCandidate{"sp-a": {Item: progA, Found: true}},
		},
		{
			name: "wrong_shop",
			item: calculator.LineItemInput{SKUID: "sku-a", Quantity: 1, UnitPriceVND: 200000, ShopDiscountProgramItemID: "sp-a"},
			cand: map[string]calculator.ShopProgramItemCandidate{"sp-a": {Item: &domain.ShopDiscountProgramItem{
				ID: "sp-a", ShopID: "shop-other", SKUID: "sku-a", DiscountedPriceVND: 160000,
				ProgramStatus: domain.ShopProgramStatusActive, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
			}, Found: true}},
		},
	} {
		t.Run("strict_error_"+tc.name, func(t *testing.T) {
			_, err := calculator.Calculate(calculator.Input{
				Now:              now,
				Strict:           true,
				Shops:            []calculator.ShopInput{{ShopID: "shop-1", Items: []calculator.LineItemInput{tc.item}}},
				ShopProgramItems: tc.cand,
			})
			if !errors.Is(err, calculator.ErrInvalidShopDiscountProgram) {
				t.Fatalf("expected ErrInvalidShopDiscountProgram, got %v", err)
			}
		})
	}
}

func TestCalculate_R3_PreviewFlashSoldOut_AndZeroVNDVoucherOrProgramNotApplied(t *testing.T) {
	now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

	// 1. Item F: Preview with sold-out flash (CheckRemaining=true, RemainingSlots=0 or < qty) warns & bills regular price
	fi := &domain.FlashSaleItem{
		ID: "fi-soldout", ShopID: "shop-1", SKUID: "sku-1",
		FlashPriceVND: 120000, PerUserLimit: 5, Status: domain.FlashStatusApproved,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	outSold, err := calculator.Calculate(calculator.Input{
		Now:    now,
		Strict: false,
		Shops: []calculator.ShopInput{
			{
				ShopID:         "shop-1",
				ShippingFeeVND: 20000,
				Items:          []calculator.LineItemInput{{SKUID: "sku-1", Quantity: 2, UnitPriceVND: 200000, FlashSaleItemID: "fi-soldout"}},
			},
		},
		FlashItems: map[string]calculator.FlashItemCandidate{
			"fi-soldout": {Item: fi, Found: true, CheckRemaining: true, RemainingSlots: 1}, // only 1 slot left, requested 2
		},
	})
	if err != nil {
		t.Fatalf("Preview sold-out Calculate: %v", err)
	}
	assertAccountingIdentities(t, outSold)
	if outSold.Breakdown.TotalFlashSaleDiscountVnd != 0 || outSold.Breakdown.FinalPayableVnd != 420000 {
		t.Fatalf("sold-out flash preview must bill regular price 420000 (0 flash disc), got disc=%d pay=%d",
			outSold.Breakdown.TotalFlashSaleDiscountVnd, outSold.Breakdown.FinalPayableVnd)
	}
	if len(outSold.Breakdown.Warnings) == 0 || !strings.Contains(strings.ToLower(outSold.Breakdown.Warnings[0]), "sold out") {
		t.Fatalf("expected sold-out warning, got %v", outSold.Breakdown.Warnings)
	}

	// 2. Item I: 0 VND voucher (e.g. shop voucher already reduced goods to 0) & 0 VND shop program (unit_price <= discounted_price)
	// MUST NOT appear in AppliedVouchers / AppliedShopPrograms / AppliedShopProgramItemIds and MUST warn!
	zeroProg := &domain.ShopDiscountProgramItem{
		ID: "sp-zero", ProgramID: "p-z", ShopID: "shop-1", SKUID: "sku-z",
		OriginalPriceVND: 100000, DiscountedPriceVND: 100000, PerUserLimit: 2,
		ProgramStatus: domain.ShopProgramStatusActive, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	for _, strict := range []bool{false, true} {
		outZero, err := calculator.Calculate(calculator.Input{
			Now:    now,
			Strict: strict,
			Shops: []calculator.ShopInput{
				{
					ShopID:         "shop-1",
					ShippingFeeVND: 0,
					Items:          []calculator.LineItemInput{{SKUID: "sku-z", Quantity: 1, UnitPriceVND: 100000, ShopDiscountProgramItemID: "sp-zero"}},
				},
			},
			ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{
				"sp-zero": {Item: zeroProg, Found: true},
			},
			ShopVouchers: map[string]*calculator.VoucherCandidate{
				"shop-1": activeVoucher("vs-full", "VS100K", domain.VoucherScopeShop, "shop-1", domain.DiscountTypeFixed, 100000, 0, 0, 0, 0, now),
			},
			// Platform voucher has 0 VND base left after shop voucher -> yields 0 VND!
			PlatformVoucher: activeVoucher("vp-zero", "VP20K", domain.VoucherScopePlatform, "", domain.DiscountTypeFixed, 20000, 0, 0, 0, 0, now),
		})
		if err != nil {
			t.Fatalf("strict=%v 0 VND voucher/program should not fail: %v", strict, err)
		}
		assertAccountingIdentities(t, outZero)
		if len(outZero.AppliedShopPrograms) != 0 || len(outZero.Breakdown.Shops[0].AppliedShopProgramItemIds) != 0 {
			t.Fatalf("strict=%v 0 VND shop program must NOT be applied, got progs=%+v ids=%v",
				strict, outZero.AppliedShopPrograms, outZero.Breakdown.Shops[0].AppliedShopProgramItemIds)
		}
		if len(outZero.AppliedVouchers) != 1 || outZero.AppliedVouchers[0].VoucherID != "vs-full" {
			t.Fatalf("strict=%v 0 VND platform voucher must NOT be in AppliedVouchers, got %+v", strict, outZero.AppliedVouchers)
		}
		if len(outZero.Breakdown.Warnings) < 2 {
			t.Fatalf("strict=%v expected warnings for both 0 VND program and 0 VND voucher, got %v", strict, outZero.Breakdown.Warnings)
		}
	}
}

func TestCalculator_R4_AutoShopProgramFallbackAndPrecedenceWarning(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	fi := &domain.FlashSaleItem{
		ID: "fi-r4", ShopID: "shop-1", SKUID: "sku-1",
		FlashPriceVND: 120000, PerUserLimit: 2, Status: domain.FlashStatusApproved,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}
	sp := &domain.ShopDiscountProgramItem{
		ID: "sp-r4", ProgramID: "prog-r4", ShopID: "shop-1", SKUID: "sku-1",
		OriginalPriceVND: 200000, DiscountedPriceVND: 160000, PerUserLimit: 2,
		ProgramStatus: domain.ShopProgramStatusActive, StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
	}

	// 1. AutoShopProgram=true + available flash -> flash wins, NO "takes precedence" warning.
	outAutoWin, err := calculator.Calculate(calculator.Input{
		Now: now, Strict: false,
		Shops: []calculator.ShopInput{{
			ShopID: "shop-1", ShippingFeeVND: 20000,
			Items: []calculator.LineItemInput{{
				SKUID: "sku-1", Quantity: 1, UnitPriceVND: 200000,
				FlashSaleItemID: "fi-r4", ShopDiscountProgramItemID: "sp-r4", AutoShopProgram: true,
			}},
		}},
		FlashItems:       map[string]calculator.FlashItemCandidate{"fi-r4": {Item: fi, Found: true, CheckRemaining: true, RemainingSlots: 2}},
		ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{"sp-r4": {Item: sp, Found: true}},
	})
	if err != nil {
		t.Fatalf("auto win Calculate: %v", err)
	}
	assertAccountingIdentities(t, outAutoWin)
	if outAutoWin.Breakdown.TotalFlashSaleDiscountVnd != 80000 || outAutoWin.Breakdown.TotalShopProgramDiscountVnd != 0 || len(outAutoWin.Breakdown.Warnings) != 0 {
		t.Fatalf("auto win expected flash=80k prog=0 warnings=0, got %+v", outAutoWin.Breakdown)
	}

	// 2. AutoShopProgram=false (explicit) + available flash -> flash wins AND emits "takes precedence" warning.
	outExplicitWin, err := calculator.Calculate(calculator.Input{
		Now: now, Strict: false,
		Shops: []calculator.ShopInput{{
			ShopID: "shop-1", ShippingFeeVND: 20000,
			Items: []calculator.LineItemInput{{
				SKUID: "sku-1", Quantity: 1, UnitPriceVND: 200000,
				FlashSaleItemID: "fi-r4", ShopDiscountProgramItemID: "sp-r4", AutoShopProgram: false,
			}},
		}},
		FlashItems:       map[string]calculator.FlashItemCandidate{"fi-r4": {Item: fi, Found: true, CheckRemaining: true, RemainingSlots: 2}},
		ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{"sp-r4": {Item: sp, Found: true}},
	})
	if err != nil {
		t.Fatalf("explicit win Calculate: %v", err)
	}
	if len(outExplicitWin.Breakdown.Warnings) != 1 || !strings.Contains(strings.ToLower(outExplicitWin.Breakdown.Warnings[0]), "takes precedence") {
		t.Fatalf("explicit both expected 'takes precedence' warning, got %v", outExplicitWin.Breakdown.Warnings)
	}

	// 3. AutoShopProgram=true + sold-out flash -> falls back to shop program (-40k) + sold-out warning!
	outFallback, err := calculator.Calculate(calculator.Input{
		Now: now, Strict: false,
		Shops: []calculator.ShopInput{{
			ShopID: "shop-1", ShippingFeeVND: 20000,
			Items: []calculator.LineItemInput{{
				SKUID: "sku-1", Quantity: 1, UnitPriceVND: 200000,
				FlashSaleItemID: "fi-r4", ShopDiscountProgramItemID: "sp-r4", AutoShopProgram: true,
			}},
		}},
		FlashItems:       map[string]calculator.FlashItemCandidate{"fi-r4": {Item: fi, Found: true, CheckRemaining: true, RemainingSlots: 0}},
		ShopProgramItems: map[string]calculator.ShopProgramItemCandidate{"sp-r4": {Item: sp, Found: true}},
	})
	if err != nil {
		t.Fatalf("fallback Calculate: %v", err)
	}
	assertAccountingIdentities(t, outFallback)
	if outFallback.Breakdown.TotalFlashSaleDiscountVnd != 0 || outFallback.Breakdown.TotalShopProgramDiscountVnd != 40000 || outFallback.Breakdown.FinalPayableVnd != 180000 {
		t.Fatalf("fallback expected flash=0 prog=40k pay=180k, got %+v", outFallback.Breakdown)
	}
	if len(outFallback.AppliedShopPrograms) != 1 || outFallback.AppliedShopPrograms[0].ItemID != "sp-r4" {
		t.Fatalf("fallback expected AppliedShopPrograms=[sp-r4], got %+v", outFallback.AppliedShopPrograms)
	}
	if len(outFallback.Breakdown.Warnings) != 1 || !strings.Contains(strings.ToLower(outFallback.Breakdown.Warnings[0]), "sold out") {
		t.Fatalf("fallback expected sold-out warning, got %v", outFallback.Breakdown.Warnings)
	}
}
