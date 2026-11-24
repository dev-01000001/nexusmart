package calculator

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/services/promotion/internal/domain"
)

var (
	// ErrVoucherIneligible indicates a requested voucher cannot be applied in strict mode.
	ErrVoucherIneligible = errors.New("voucher ineligible")
	// ErrInsufficientCoins indicates the user lacks enough coins in strict mode.
	ErrInsufficientCoins = errors.New("insufficient coin balance")
	// ErrInvalidFlashSaleItem indicates a requested flash sale item is invalid or inactive.
	ErrInvalidFlashSaleItem = errors.New("invalid flash sale item")
	// ErrInvalidShopDiscountProgram indicates a requested shop discount program item is invalid or inactive.
	ErrInvalidShopDiscountProgram = errors.New("invalid shop discount program item")
	// ErrShopProgramLimitExceeded indicates a user exceeded a shop discount program's per-user limit.
	ErrShopProgramLimitExceeded = errors.New("shop discount program per-user limit exceeded")
	// ErrShopSuspended indicates a shop is suspended and its promotions cannot be applied (F12).
	ErrShopSuspended = errors.New("SHOP_SUSPENDED")
)

// LineItemInput represents a SKU line inside a shop checkout group.
type LineItemInput struct {
	SKUID                     string
	Quantity                  int32
	UnitPriceVND              int64
	FlashSaleItemID           string
	CategoryID                string
	ShopDiscountProgramItemID string
	// AutoShopProgram is true when ShopDiscountProgramItemID was auto-populated by the server in Preview mode
	// rather than explicitly supplied by the caller; suppresses redundant "takes precedence" warnings when flash wins.
	AutoShopProgram bool
}

// ShopInput represents one shop's subtotal, shipping fee, freeship config, and items for discount calculation.
type ShopInput struct {
	ShopID              string
	SubtotalVND         int64
	ShippingFeeVND      int64
	Items               []LineItemInput
	FreeShipEnabled     bool
	FreeShipMinOrderVND int64
	FreeShipMaxVND      int64
}

// FlashItemCandidate holds validated flash sale metadata for discount preview/reservation.
type FlashItemCandidate struct {
	Item           *domain.FlashSaleItem
	Found          bool
	ShopSuspended  bool
	UserUsedCount  int32
	CheckRemaining bool
	RemainingSlots int32
}

// ShopProgramItemCandidate holds validated shop discount program item metadata and live user usage.
type ShopProgramItemCandidate struct {
	Item          *domain.ShopDiscountProgramItem
	Found         bool
	ShopSuspended bool
	UserUsedCount int32
}

// VoucherCandidate holds a voucher along with its live Redis quota/budget/user-usage counters
// and optional expanded allowed category IDs (including recursive descendants).
type VoucherCandidate struct {
	RequestedID        string
	Voucher            *domain.Voucher
	Found              bool
	ShopSuspended      bool
	RemainingQuota     int32
	RemainingBudget    int64
	UserUsedCount      int32
	AllowedCategoryIDs map[string]bool
}

// Input holds all resolved data needed for pure deterministic discount calculation.
type Input struct {
	Now              time.Time
	Strict           bool
	Shops            []ShopInput
	FlashItems       map[string]FlashItemCandidate
	ShopProgramItems map[string]ShopProgramItemCandidate
	ShopVouchers     map[string]*VoucherCandidate
	PlatformVoucher  *VoucherCandidate
	FreeshipVoucher  *VoucherCandidate
	CoinsToUse       int64
	AvailableCoins   int64
}

// AppliedVoucher describes a voucher that successfully applied during calculation.
type AppliedVoucher struct {
	VoucherID    string
	Scope        string
	ShopID       string
	DiscountVND  int64
	HasBudget    bool
	PerUserLimit int32
	RulesVersion int32
}

// AppliedShopProgram describes a shop discount program item applied during calculation.
type AppliedShopProgram struct {
	ItemID       string
	SKUID        string
	ShopID       string
	Quantity     int32
	PerUserLimit int32
}

// Output holds the computed DiscountBreakdown plus reservation metadata.
type Output struct {
	Breakdown           *promotionv1.DiscountBreakdown
	AppliedVouchers     []AppliedVoucher
	AppliedShopPrograms []AppliedShopProgram
	CoinsUsed           int64
}

// Calculate computes integer-VND discounts strictly in Wave 2b §1 order:
// (0) Item-level price: Flash sale OR Shop discount program (flash wins, never stack on the same line) ->
// (1) Shop vouchers (seller-borne) ->
// (2) Platform voucher (platform-borne, optional category scope over post-shop-voucher eligible lines) ->
// (3a) Shop freeship (seller-borne, checked on post-(F+P_shop+V_shop) goods, trimmed so R >= 0) ->
// (3b) Platform freeship voucher (platform-borne, applied on remaining shipping Ship - S_shop) ->
// (4) Coins (platform-borne, 1 coin = 1 VND).
func Calculate(in Input) (*Output, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	n := len(in.Shops)
	subtotals := make([]int64, n)
	shippingFees := make([]int64, n)
	flashDiscounts := make([]int64, n)
	shopProgramDiscounts := make([]int64, n)
	afterItem := make([]int64, n)
	shopLineAfterItem := make([][]int64, n)
	shopVoucherIDs := make([]string, n)
	shopVoucherDiscounts := make([]int64, n)
	afterShop := make([]int64, n)
	shopLineAfterShop := make([][]int64, n)
	warnings := make([]string, 0)
	applied := make([]AppliedVoucher, 0, len(in.ShopVouchers)+2)

	appliedProgByID := make(map[string]*AppliedShopProgram)
	appliedProgOrder := make([]string, 0)
	shopAppliedProgIDs := make([][]string, n)
	usedProgQty := make(map[string]int32)
	usedFlashQty := make(map[string]int32)

	shopIndexByID := make(map[string]int, n)
	for i, s := range in.Shops {
		shopIndexByID[s.ShopID] = i

		var itemsGross int64
		for _, item := range s.Items {
			if item.Quantity > 0 && item.UnitPriceVND > 0 {
				itemsGross += item.UnitPriceVND * int64(item.Quantity)
			}
		}
		sub := s.SubtotalVND
		if sub <= 0 && len(s.Items) > 0 {
			sub = itemsGross
		}
		if sub < 0 {
			sub = 0
		}
		subtotals[i] = sub

		ship := s.ShippingFeeVND
		if ship < 0 {
			ship = 0
		}
		shippingFees[i] = ship

		// Step 0: Item-level discounts (Flash sale OR Shop discount program; NEVER both on the same line)
		var fDisc int64
		var pDisc int64
		lineAfter := make([]int64, len(s.Items))
		shopAppliedSeen := make(map[string]bool)

		for li, item := range s.Items {
			var lineGross int64
			if item.Quantity > 0 && item.UnitPriceVND > 0 {
				lineGross = item.UnitPriceVND * int64(item.Quantity)
			}

			var lineFlashDisc int64
			var lineProgDisc int64
			flashApplied := false

			if item.FlashSaleItemID != "" {
				cand, ok := in.FlashItems[item.FlashSaleItemID]
				flashReason := validateFlashCandidate(cand, ok, item, s.ShopID, now, usedFlashQty[item.FlashSaleItemID])
				if flashReason != "" {
					if in.Strict {
						if cand.ShopSuspended && cand.Item != nil {
							return nil, fmt.Errorf("%w: shop for flash sale item %s is suspended", ErrShopSuspended, cand.Item.ID)
						}
						return nil, fmt.Errorf("%w: %s", ErrInvalidFlashSaleItem, flashReason)
					}
					warnings = append(warnings, flashReason)
				} else {
					flashApplied = true
					usedFlashQty[item.FlashSaleItemID] += item.Quantity
					if item.Quantity > 0 && item.UnitPriceVND > cand.Item.FlashPriceVND {
						lineFlashDisc = (item.UnitPriceVND - cand.Item.FlashPriceVND) * int64(item.Quantity)
					}
					if item.ShopDiscountProgramItemID != "" && !item.AutoShopProgram {
						warnings = append(warnings, fmt.Sprintf(
							"flash sale item %s takes precedence over shop discount program item %s for SKU %s",
							item.FlashSaleItemID, item.ShopDiscountProgramItemID, item.SKUID,
						))
					}
				}
			}

			if !flashApplied && item.ShopDiscountProgramItemID != "" {
				cand, ok := in.ShopProgramItems[item.ShopDiscountProgramItemID]
				progReason, progErr := validateShopProgramCandidate(cand, ok, item, s.ShopID, now, usedProgQty[item.ShopDiscountProgramItemID])
				if progReason != "" {
					// Exceeding per_user_limit falls back to regular price for the whole line with a warning
					// in BOTH Preview and Reserve (never blocks buying the SKU at regular price).
					// All other validation errors (missing, inactive, wrong SKU/shop) still fail in Strict mode.
					if in.Strict && !errors.Is(progErr, ErrShopProgramLimitExceeded) {
						if errors.Is(progErr, ErrShopSuspended) && cand.Item != nil {
							return nil, fmt.Errorf("%w: shop for shop discount program item %s is suspended", ErrShopSuspended, cand.Item.ID)
						}
						return nil, fmt.Errorf("%w: %s", progErr, progReason)
					}
					warnings = append(warnings, progReason)
				} else if item.Quantity > 0 {
					spi := cand.Item
					if item.UnitPriceVND <= spi.DiscountedPriceVND {
						warnings = append(warnings, fmt.Sprintf(
							"shop discount program item %s provides 0 VND discount for SKU %s and was not applied",
							spi.ID, item.SKUID,
						))
					} else {
						usedProgQty[spi.ID] += item.Quantity
						lineProgDisc = (item.UnitPriceVND - spi.DiscountedPriceVND) * int64(item.Quantity)
						if !shopAppliedSeen[spi.ID] {
							shopAppliedSeen[spi.ID] = true
							shopAppliedProgIDs[i] = append(shopAppliedProgIDs[i], spi.ID)
						}
						if existing, exists := appliedProgByID[spi.ID]; exists {
							existing.Quantity += item.Quantity
						} else {
							shopID := spi.ShopID
							if shopID == "" {
								shopID = s.ShopID
							}
							appliedProgByID[spi.ID] = &AppliedShopProgram{
								ItemID:       spi.ID,
								SKUID:        spi.SKUID,
								ShopID:       shopID,
								Quantity:     item.Quantity,
								PerUserLimit: spi.PerUserLimit,
							}
							appliedProgOrder = append(appliedProgOrder, spi.ID)
						}
					}
				}
			}

			fDisc += lineFlashDisc
			pDisc += lineProgDisc
			la := lineGross - lineFlashDisc - lineProgDisc
			if la < 0 {
				la = 0
			}
			lineAfter[li] = la
		}

		if fDisc > sub {
			fDisc = sub
		}
		if pDisc > sub-fDisc {
			pDisc = sub - fDisc
		}
		flashDiscounts[i] = fDisc
		shopProgramDiscounts[i] = pDisc
		afterItem[i] = sub - fDisc - pDisc
		shopLineAfterItem[i] = lineAfter
	}

	// Check for shop vouchers referencing shops not in input
	if len(in.ShopVouchers) > 0 && n > 0 {
		extraShopIDs := make([]string, 0)
		for shopID := range in.ShopVouchers {
			if _, exists := shopIndexByID[shopID]; !exists {
				extraShopIDs = append(extraShopIDs, shopID)
			}
		}
		sort.Strings(extraShopIDs)
		for _, shopID := range extraShopIDs {
			msg := fmt.Sprintf("shop voucher provided for shop %s not present in items", shopID)
			if in.Strict {
				return nil, fmt.Errorf("%w: %s", ErrVoucherIneligible, msg)
			}
			warnings = append(warnings, msg)
		}
	}

	// Step 1: Per-shop SHOP voucher (cost bearer = SHOP)
	var totalAfterShop int64
	for i, s := range in.Shops {
		cand := in.ShopVouchers[s.ShopID]
		if cand != nil {
			disc, reason := evaluateVoucher(cand, domain.VoucherScopeShop, s.ShopID, afterItem[i], afterItem[i], now)
			if reason != "" {
				if in.Strict {
					if cand.ShopSuspended && cand.Voucher != nil {
						return nil, fmt.Errorf("%w: shop for voucher %s is suspended", ErrShopSuspended, cand.Voucher.Code)
					}
					return nil, fmt.Errorf("%w: %s", ErrVoucherIneligible, reason)
				}
				warnings = append(warnings, reason)
			} else if disc <= 0 {
				warnings = append(warnings, fmt.Sprintf("voucher %s provides 0 VND discount and was not applied", cand.Voucher.Code))
			} else {
				shopVoucherIDs[i] = cand.Voucher.ID
				shopVoucherDiscounts[i] = disc
				applied = append(applied, AppliedVoucher{
					VoucherID:    cand.Voucher.ID,
					Scope:        domain.VoucherScopeShop,
					ShopID:       s.ShopID,
					DiscountVND:  disc,
					HasBudget:    cand.Voucher.BudgetVND > 0,
					PerUserLimit: cand.Voucher.PerUserLimit,
					RulesVersion: cand.Voucher.RulesVersion,
				})
			}
		}
		afterShop[i] = afterItem[i] - shopVoucherDiscounts[i]
		totalAfterShop += afterShop[i]

		// Distribute shop voucher proportionally across shop lines so category-scoped platform
		// vouchers compute strictly against post-(flash|program+shop_voucher) eligible line values.
		if len(s.Items) > 0 {
			lineVoucherAlloc := allocateProportional(shopVoucherDiscounts[i], shopLineAfterItem[i])
			linePost := make([]int64, len(s.Items))
			for li := range s.Items {
				v := shopLineAfterItem[i][li] - lineVoucherAlloc[li]
				if v < 0 {
					v = 0
				}
				linePost[li] = v
			}
			shopLineAfterShop[i] = linePost
		}
	}

	// Step 2: PLATFORM voucher applied on post-shop-voucher subtotal (cost bearer = PLATFORM).
	// When category_ids are configured on the voucher, only lines in those categories (and descendants) qualify.
	var totalPlatformDiscount int64
	platformAlloc := make([]int64, n)
	if in.PlatformVoucher != nil {
		eligiblePerShop := afterShop
		totalEligible := totalAfterShop
		categoryRestricted := false

		if in.PlatformVoucher.Found && in.PlatformVoucher.Voucher != nil && len(in.PlatformVoucher.Voucher.CategoryIDs) > 0 {
			categoryRestricted = true
			allowed := in.PlatformVoucher.AllowedCategoryIDs
			if len(allowed) == 0 {
				allowed = make(map[string]bool, len(in.PlatformVoucher.Voucher.CategoryIDs))
				for _, cid := range in.PlatformVoucher.Voucher.CategoryIDs {
					if cid != "" {
						allowed[cid] = true
					}
				}
			}
			eligiblePerShop = make([]int64, n)
			totalEligible = 0
			for i, s := range in.Shops {
				var shopElig int64
				for li, item := range s.Items {
					if item.CategoryID != "" && allowed[item.CategoryID] && li < len(shopLineAfterShop[i]) {
						shopElig += shopLineAfterShop[i][li]
					}
				}
				if shopElig > afterShop[i] {
					shopElig = afterShop[i]
				}
				eligiblePerShop[i] = shopElig
				totalEligible += shopElig
			}
		}

		var disc int64
		var reason string
		if categoryRestricted && totalEligible <= 0 {
			// Check basic eligibility first so inactive/expired vouchers report their primary reason.
			_, baseReason := evaluateVoucherMetadata(in.PlatformVoucher, domain.VoucherScopePlatform, "", now)
			if baseReason != "" {
				reason = baseReason
			} else {
				reason = fmt.Sprintf("voucher %s does not apply to any items in the selected categories", in.PlatformVoucher.Voucher.Code)
			}
		} else {
			disc, reason = evaluateVoucher(in.PlatformVoucher, domain.VoucherScopePlatform, "", totalEligible, totalEligible, now)
		}

		if reason != "" {
			if in.Strict {
				return nil, fmt.Errorf("%w: %s", ErrVoucherIneligible, reason)
			}
			warnings = append(warnings, reason)
		} else if disc <= 0 {
			warnings = append(warnings, fmt.Sprintf("voucher %s provides 0 VND discount and was not applied", in.PlatformVoucher.Voucher.Code))
		} else {
			totalPlatformDiscount = disc
			platformAlloc = allocateProportional(disc, eligiblePerShop)
			applied = append(applied, AppliedVoucher{
				VoucherID:    in.PlatformVoucher.Voucher.ID,
				Scope:        domain.VoucherScopePlatform,
				DiscountVND:  disc,
				HasBudget:    in.PlatformVoucher.Voucher.BudgetVND > 0,
				PerUserLimit: in.PlatformVoucher.Voucher.PerUserLimit,
				RulesVersion: in.PlatformVoucher.Voucher.RulesVersion,
			})
		}
	}

	afterPlatform := make([]int64, n)
	var totalAfterPlatform int64
	var totalShipping int64
	for i := 0; i < n; i++ {
		afterPlatform[i] = afterShop[i] - platformAlloc[i]
		totalAfterPlatform += afterPlatform[i]
		totalShipping += shippingFees[i]
	}

	// Step 3a: Shop freeship (S_shop, seller-borne; threshold checked on post-(F+P_shop+V_shop) goods;
	// trimmed if needed so seller net revenue R = G - F - P_shop - V_shop - S_shop >= 0).
	shopFreeshipDiscounts := make([]int64, n)
	shippingAfterShop := make([]int64, n)
	var totalShopFreeship int64
	var totalShippingAfterShop int64
	for i, s := range in.Shops {
		var sShop int64
		if s.FreeShipEnabled && s.FreeShipMaxVND > 0 && shippingFees[i] > 0 {
			minOrder := s.FreeShipMinOrderVND
			if minOrder < 0 {
				minOrder = 0
			}
			if afterShop[i] >= minOrder {
				sShop = minInt64(shippingFees[i], s.FreeShipMaxVND)
				if sShop > afterShop[i] {
					sShop = afterShop[i]
				}
			}
		}
		shopFreeshipDiscounts[i] = sShop
		shippingAfterShop[i] = shippingFees[i] - sShop
		totalShopFreeship += sShop
		totalShippingAfterShop += shippingAfterShop[i]
	}

	// Step 3b: Platform FREESHIP voucher (S_free, platform-borne) applied on remaining shipping (Ship - S_shop).
	var totalFreeshipDiscount int64
	freeshipAlloc := make([]int64, n)
	if in.FreeshipVoucher != nil {
		disc, reason := evaluateVoucher(in.FreeshipVoucher, domain.VoucherScopeFreeship, "", totalAfterPlatform, totalShippingAfterShop, now)
		if reason != "" {
			if in.Strict {
				return nil, fmt.Errorf("%w: %s", ErrVoucherIneligible, reason)
			}
			warnings = append(warnings, reason)
		} else if disc <= 0 {
			warnings = append(warnings, fmt.Sprintf("freeship voucher %s provides 0 VND discount and was not applied", in.FreeshipVoucher.Voucher.Code))
		} else {
			totalFreeshipDiscount = disc
			freeshipAlloc = allocateProportional(disc, shippingAfterShop)
			applied = append(applied, AppliedVoucher{
				VoucherID:    in.FreeshipVoucher.Voucher.ID,
				Scope:        domain.VoucherScopeFreeship,
				DiscountVND:  disc,
				HasBudget:    in.FreeshipVoucher.Voucher.BudgetVND > 0,
				PerUserLimit: in.FreeshipVoucher.Voucher.PerUserLimit,
				RulesVersion: in.FreeshipVoucher.Voucher.RulesVersion,
			})
		}
	}

	finalShipping := make([]int64, n)
	payableBeforeCoins := make([]int64, n)
	var totalPayableBeforeCoins int64
	for i := 0; i < n; i++ {
		finalShipping[i] = shippingAfterShop[i] - freeshipAlloc[i]
		payableBeforeCoins[i] = afterPlatform[i] + finalShipping[i]
		totalPayableBeforeCoins += payableBeforeCoins[i]
	}

	// Step 4: Coins (1 coin = 1 VND, capped at available balance and remaining payable; cost bearer = PLATFORM)
	var coinsUsed int64
	coinsAlloc := make([]int64, n)
	if in.CoinsToUse > 0 {
		desired := in.CoinsToUse
		if n > 0 && desired > totalPayableBeforeCoins {
			desired = totalPayableBeforeCoins
		}
		avail := in.AvailableCoins
		if avail < 0 {
			avail = 0
		}
		if in.Strict {
			if avail < desired {
				return nil, fmt.Errorf("%w: requested %d coins, available %d", ErrInsufficientCoins, desired, avail)
			}
			coinsUsed = desired
		} else {
			if in.CoinsToUse > avail {
				warnings = append(warnings, fmt.Sprintf("requested %d coins exceeds available balance %d; capped to %d", in.CoinsToUse, avail, minInt64(desired, avail)))
			}
			coinsUsed = minInt64(desired, avail)
		}
		if coinsUsed > 0 && n > 0 {
			coinsAlloc = allocateProportional(coinsUsed, payableBeforeCoins)
		}
	}

	// Assemble final per-shop and total breakdown with strict §1 accounting identities
	shopBreakdowns := make([]*promotionv1.ShopDiscountBreakdown, n)
	var totalSubtotal int64
	var totalFlashDiscount int64
	var totalShopProgramDiscount int64
	var totalShopVoucherDiscount int64
	var totalFinalPayable int64

	for i, s := range in.Shops {
		shopFinal := payableBeforeCoins[i] - coinsAlloc[i]
		shopBreakdowns[i] = &promotionv1.ShopDiscountBreakdown{
			ShopId:                     s.ShopID,
			SubtotalVnd:                subtotals[i],
			FlashSaleDiscountVnd:       flashDiscounts[i],
			ShopProgramDiscountVnd:     shopProgramDiscounts[i],
			ShopVoucherId:              shopVoucherIDs[i],
			ShopVoucherDiscountVnd:     shopVoucherDiscounts[i],
			ShopFreeshipDiscountVnd:    shopFreeshipDiscounts[i],
			PlatformVoucherDiscountVnd: platformAlloc[i],
			FreeshipDiscountVnd:        freeshipAlloc[i],
			CoinsDiscountVnd:           coinsAlloc[i],
			ShippingFeeVnd:             shippingFees[i],
			FinalShippingFeeVnd:        finalShipping[i],
			FinalPayableVnd:            shopFinal,
			AppliedShopProgramItemIds:  shopAppliedProgIDs[i],
		}
		totalSubtotal += subtotals[i]
		totalFlashDiscount += flashDiscounts[i]
		totalShopProgramDiscount += shopProgramDiscounts[i]
		totalShopVoucherDiscount += shopVoucherDiscounts[i]
		totalFinalPayable += shopFinal
	}

	shopCost := totalFlashDiscount + totalShopProgramDiscount + totalShopVoucherDiscount + totalShopFreeship
	platformCost := totalPlatformDiscount + totalFreeshipDiscount + coinsUsed
	totalDiscount := shopCost + platformCost

	sort.Strings(appliedProgOrder)
	appliedPrograms := make([]AppliedShopProgram, 0, len(appliedProgOrder))
	for _, id := range appliedProgOrder {
		appliedPrograms = append(appliedPrograms, *appliedProgByID[id])
	}

	return &Output{
		Breakdown: &promotionv1.DiscountBreakdown{
			Shops:                           shopBreakdowns,
			TotalSubtotalVnd:                totalSubtotal,
			TotalFlashSaleDiscountVnd:       totalFlashDiscount,
			TotalShopProgramDiscountVnd:     totalShopProgramDiscount,
			TotalShopVoucherDiscountVnd:     totalShopVoucherDiscount,
			TotalShopFreeshipDiscountVnd:    totalShopFreeship,
			TotalPlatformVoucherDiscountVnd: totalPlatformDiscount,
			TotalFreeshipDiscountVnd:        totalFreeshipDiscount,
			TotalCoinsDiscountVnd:           coinsUsed,
			TotalDiscountVnd:                totalDiscount,
			PlatformCostVnd:                 platformCost,
			ShopCostVnd:                     shopCost,
			TotalShippingFeeVnd:             totalShipping,
			FinalPayableVnd:                 totalFinalPayable,
			Warnings:                        warnings,
		},
		AppliedVouchers:     applied,
		AppliedShopPrograms: appliedPrograms,
		CoinsUsed:           coinsUsed,
	}, nil
}

func validateFlashCandidate(
	cand FlashItemCandidate,
	ok bool,
	item LineItemInput,
	shopID string,
	now time.Time,
	alreadyInOrder int32,
) string {
	if !ok || !cand.Found || cand.Item == nil {
		return fmt.Sprintf("flash sale item %s not found", item.FlashSaleItemID)
	}
	fi := cand.Item
	if cand.ShopSuspended {
		return fmt.Sprintf("SHOP_SUSPENDED: shop for flash sale item %s is suspended", fi.ID)
	}
	if fi.Status != domain.FlashStatusApproved {
		return fmt.Sprintf("flash sale item %s is not approved", fi.ID)
	}
	// Active flash sale window is strictly half-open [StartsAt, EndsAt)
	if now.Before(fi.StartsAt) || !now.Before(fi.EndsAt) {
		return fmt.Sprintf("flash sale item %s is not active", fi.ID)
	}
	if item.SKUID != "" && fi.SKUID != "" && item.SKUID != fi.SKUID {
		return fmt.Sprintf("flash sale item %s does not match SKU %s", fi.ID, item.SKUID)
	}
	if shopID != "" && fi.ShopID != "" && shopID != fi.ShopID {
		return fmt.Sprintf("flash sale item %s does not belong to shop %s", fi.ID, shopID)
	}
	if fi.PerUserLimit > 0 && item.Quantity > 0 && cand.UserUsedCount+alreadyInOrder+item.Quantity > fi.PerUserLimit {
		return fmt.Sprintf("flash sale item %s per-user limit (%d) exceeded", fi.ID, fi.PerUserLimit)
	}
	if cand.CheckRemaining && item.Quantity > 0 && alreadyInOrder+item.Quantity > cand.RemainingSlots {
		return fmt.Sprintf("flash sale item %s is sold out (remaining %d, requested %d)", fi.ID, cand.RemainingSlots, alreadyInOrder+item.Quantity)
	}
	return ""
}

func validateShopProgramCandidate(
	cand ShopProgramItemCandidate,
	ok bool,
	item LineItemInput,
	shopID string,
	now time.Time,
	alreadyInOrder int32,
) (string, error) {
	if !ok || !cand.Found || cand.Item == nil {
		return fmt.Sprintf("shop discount program item %s not found", item.ShopDiscountProgramItemID), ErrInvalidShopDiscountProgram
	}
	spi := cand.Item
	if cand.ShopSuspended {
		return fmt.Sprintf("SHOP_SUSPENDED: shop for shop discount program item %s is suspended", spi.ID), ErrShopSuspended
	}
	if !spi.IsActiveAt(now) {
		return fmt.Sprintf("shop discount program item %s is not active", spi.ID), ErrInvalidShopDiscountProgram
	}
	if item.SKUID != "" && spi.SKUID != "" && item.SKUID != spi.SKUID {
		return fmt.Sprintf("shop discount program item %s does not match SKU %s", spi.ID, item.SKUID), ErrInvalidShopDiscountProgram
	}
	if shopID != "" && spi.ShopID != "" && shopID != spi.ShopID {
		return fmt.Sprintf("shop discount program item %s does not belong to shop %s", spi.ID, shopID), ErrInvalidShopDiscountProgram
	}
	if spi.PerUserLimit > 0 && item.Quantity > 0 && cand.UserUsedCount+alreadyInOrder+item.Quantity > spi.PerUserLimit {
		return fmt.Sprintf("shop discount program item %s per-user limit (%d) exceeded", spi.ID, spi.PerUserLimit), ErrShopProgramLimitExceeded
	}
	return "", nil
}

// evaluateVoucherMetadata checks non-amount voucher validity (existence, lifecycle status, scope,
// shop ownership, time window, quota, per-user limit).
func evaluateVoucherMetadata(
	cand *VoucherCandidate,
	expectedScope string,
	expectedShopID string,
	now time.Time,
) (*domain.Voucher, string) {
	if cand == nil {
		return nil, ""
	}
	if !cand.Found || cand.Voucher == nil {
		return nil, fmt.Sprintf("voucher %s not found", cand.RequestedID)
	}
	v := cand.Voucher
	if cand.ShopSuspended {
		return nil, fmt.Sprintf("SHOP_SUSPENDED: shop for voucher %s is suspended", v.Code)
	}
	switch v.EffectiveStatus() {
	case domain.VoucherStatusDeleted:
		return nil, fmt.Sprintf("voucher %s has been deleted", v.Code)
	case domain.VoucherStatusPaused:
		return nil, fmt.Sprintf("voucher %s is paused", v.Code)
	}
	if !v.IsActive {
		return nil, fmt.Sprintf("voucher %s is inactive", v.Code)
	}
	if v.Scope != expectedScope {
		return nil, fmt.Sprintf("voucher %s scope %s does not match required scope %s", v.Code, v.Scope, expectedScope)
	}
	if expectedScope == domain.VoucherScopeShop && v.ShopID != expectedShopID {
		return nil, fmt.Sprintf("voucher %s does not belong to shop %s", v.Code, expectedShopID)
	}
	if now.Before(v.StartsAt) {
		return nil, fmt.Sprintf("voucher %s has not started yet", v.Code)
	}
	if now.After(v.EndsAt) {
		return nil, fmt.Sprintf("voucher %s has expired", v.Code)
	}
	if cand.RemainingQuota <= 0 {
		return nil, fmt.Sprintf("voucher %s is out of quota", v.Code)
	}
	if v.PerUserLimit > 0 && cand.UserUsedCount >= v.PerUserLimit {
		return nil, fmt.Sprintf("voucher %s per-user limit reached", v.Code)
	}
	return v, ""
}

// evaluateVoucher checks eligibility and returns (discountVND, ineligibleReason).
// minOrderBase is the goods subtotal checked against MinOrderVND.
// discountBase is the amount the discount is computed against and capped at (goods subtotal or shipping fee).
func evaluateVoucher(
	cand *VoucherCandidate,
	expectedScope string,
	expectedShopID string,
	minOrderBase int64,
	discountBase int64,
	now time.Time,
) (int64, string) {
	v, reason := evaluateVoucherMetadata(cand, expectedScope, expectedShopID, now)
	if reason != "" || v == nil {
		return 0, reason
	}
	if minOrderBase < v.MinOrderVND {
		shortfall := v.MinOrderVND - minOrderBase
		return 0, fmt.Sprintf("voucher %s requires minimum order %d VND (shortfall %d VND)", v.Code, v.MinOrderVND, shortfall)
	}

	disc := ComputeVoucherDiscount(v, discountBase)
	if v.BudgetVND > 0 && cand.RemainingBudget < disc {
		return 0, fmt.Sprintf("voucher %s budget exhausted", v.Code)
	}
	return disc, ""
}

// ComputeVoucherDiscount computes the integer-VND discount for a voucher against baseAmount,
// applying max_discount_vnd (for PERCENT) and capping at baseAmount.
func ComputeVoucherDiscount(v *domain.Voucher, baseAmount int64) int64 {
	if v == nil || baseAmount <= 0 {
		return 0
	}

	var raw int64
	switch v.DiscountType {
	case domain.DiscountTypeFixed:
		raw = v.ValueVND
	case domain.DiscountTypePercent:
		num := new(big.Int).Mul(big.NewInt(baseAmount), big.NewInt(int64(v.DiscountPercent)))
		raw = new(big.Int).Quo(num, big.NewInt(100)).Int64()
		if v.MaxDiscountVND > 0 && raw > v.MaxDiscountVND {
			raw = v.MaxDiscountVND
		}
	default:
		return 0
	}

	if raw < 0 {
		return 0
	}
	if raw > baseAmount {
		return baseAmount
	}
	return raw
}

// allocateProportional distributes integer amount across non-negative weights using the
// largest-remainder method in exact big-integer arithmetic. Guarantees:
//   - sum(result) == min(amount, sum(weights))
//   - 0 <= result[i] <= weights[i] for all i
//   - deterministic tie-breaking by ascending index
func allocateProportional(amount int64, weights []int64) []int64 {
	n := len(weights)
	alloc := make([]int64, n)
	if n == 0 || amount <= 0 {
		return alloc
	}

	var totalWeight int64
	for _, w := range weights {
		if w > 0 {
			totalWeight += w
		}
	}
	if totalWeight <= 0 {
		return alloc
	}
	if amount >= totalWeight {
		for i, w := range weights {
			if w > 0 {
				alloc[i] = w
			}
		}
		return alloc
	}

	type remEntry struct {
		idx int
		rem *big.Int
	}

	bigAmt := big.NewInt(amount)
	bigTotal := big.NewInt(totalWeight)
	rems := make([]remEntry, n)
	var distributed int64

	for i, w := range weights {
		if w <= 0 {
			rems[i] = remEntry{idx: i, rem: big.NewInt(0)}
			continue
		}
		prod := new(big.Int).Mul(bigAmt, big.NewInt(w))
		q, r := new(big.Int).QuoRem(prod, bigTotal, new(big.Int))
		share := q.Int64()
		if share > w {
			share = w
		}
		alloc[i] = share
		distributed += share
		rems[i] = remEntry{idx: i, rem: r}
	}

	leftover := amount - distributed
	if leftover <= 0 {
		return alloc
	}

	sort.Slice(rems, func(i, j int) bool {
		cmp := rems[i].rem.Cmp(rems[j].rem)
		if cmp != 0 {
			return cmp > 0
		}
		return rems[i].idx < rems[j].idx
	})

	for _, entry := range rems {
		if leftover == 0 {
			break
		}
		if alloc[entry.idx] < weights[entry.idx] {
			alloc[entry.idx]++
			leftover--
		}
	}

	return alloc
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
