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
)

// LineItemInput represents a SKU line inside a shop checkout group.
type LineItemInput struct {
	SKUID           string
	Quantity        int32
	UnitPriceVND    int64
	FlashSaleItemID string
}

// ShopInput represents one shop's subtotal, shipping fee, and items for discount calculation.
type ShopInput struct {
	ShopID         string
	SubtotalVND    int64
	ShippingFeeVND int64
	Items          []LineItemInput
}

// FlashItemCandidate holds validated flash sale metadata for discount preview/reservation.
type FlashItemCandidate struct {
	Item  *domain.FlashSaleItem
	Found bool
}

// VoucherCandidate holds a voucher along with its live Redis quota/budget/user-usage counters.
type VoucherCandidate struct {
	RequestedID     string
	Voucher         *domain.Voucher
	Found           bool
	RemainingQuota  int32
	RemainingBudget int64
	UserUsedCount   int32
}

// Input holds all resolved data needed for pure deterministic discount calculation.
type Input struct {
	Now             time.Time
	Strict          bool
	Shops           []ShopInput
	FlashItems      map[string]FlashItemCandidate
	ShopVouchers    map[string]*VoucherCandidate
	PlatformVoucher *VoucherCandidate
	FreeshipVoucher *VoucherCandidate
	CoinsToUse      int64
	AvailableCoins  int64
}

// AppliedVoucher describes a voucher that successfully applied during calculation.
type AppliedVoucher struct {
	VoucherID    string
	Scope        string
	ShopID       string
	DiscountVND  int64
	HasBudget    bool
	PerUserLimit int32
}

// Output holds the computed DiscountBreakdown plus reservation metadata.
type Output struct {
	Breakdown       *promotionv1.DiscountBreakdown
	AppliedVouchers []AppliedVoucher
	CoinsUsed       int64
}

// Calculate computes integer-VND discounts strictly in order:
// (0) Flash sale item discounts -> (1) Shop vouchers -> (2) Platform voucher ->
// (3) Freeship voucher -> (4) Coins (1 coin = 1 VND).
func Calculate(in Input) (*Output, error) {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	n := len(in.Shops)
	subtotals := make([]int64, n)
	shippingFees := make([]int64, n)
	flashDiscounts := make([]int64, n)
	afterFlash := make([]int64, n)
	shopVoucherIDs := make([]string, n)
	shopVoucherDiscounts := make([]int64, n)
	afterShop := make([]int64, n)
	warnings := make([]string, 0)
	applied := make([]AppliedVoucher, 0, len(in.ShopVouchers)+2)

	shopIndexByID := make(map[string]int, n)
	for i, s := range in.Shops {
		shopIndexByID[s.ShopID] = i

		sub := s.SubtotalVND
		if sub <= 0 && len(s.Items) > 0 {
			for _, item := range s.Items {
				if item.Quantity > 0 && item.UnitPriceVND > 0 {
					sub += item.UnitPriceVND * int64(item.Quantity)
				}
			}
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

		// Step 0: Flash sale price differential (borne by shop)
		var fDisc int64
		for _, item := range s.Items {
			if item.FlashSaleItemID == "" {
				continue
			}
			cand, ok := in.FlashItems[item.FlashSaleItemID]
			if !ok || !cand.Found || cand.Item == nil {
				msg := fmt.Sprintf("flash sale item %s not found", item.FlashSaleItemID)
				if in.Strict {
					return nil, fmt.Errorf("%w: %s", ErrInvalidFlashSaleItem, msg)
				}
				warnings = append(warnings, msg)
				continue
			}
			fi := cand.Item
			if fi.Status != domain.FlashStatusApproved {
				msg := fmt.Sprintf("flash sale item %s is not approved", fi.ID)
				if in.Strict {
					return nil, fmt.Errorf("%w: %s", ErrInvalidFlashSaleItem, msg)
				}
				warnings = append(warnings, msg)
				continue
			}
			if fi.WindowStatus == domain.FlashWindowStatusEnded || now.Before(fi.StartsAt) || now.After(fi.EndsAt) {
				msg := fmt.Sprintf("flash sale item %s is not active", fi.ID)
				if in.Strict {
					return nil, fmt.Errorf("%w: %s", ErrInvalidFlashSaleItem, msg)
				}
				warnings = append(warnings, msg)
				continue
			}
			if item.SKUID != "" && fi.SKUID != "" && item.SKUID != fi.SKUID {
				msg := fmt.Sprintf("flash sale item %s does not match SKU %s", fi.ID, item.SKUID)
				if in.Strict {
					return nil, fmt.Errorf("%w: %s", ErrInvalidFlashSaleItem, msg)
				}
				warnings = append(warnings, msg)
				continue
			}
			if item.Quantity > 0 && item.UnitPriceVND > fi.FlashPriceVND {
				fDisc += (item.UnitPriceVND - fi.FlashPriceVND) * int64(item.Quantity)
			}
		}
		if fDisc > sub {
			fDisc = sub
		}
		flashDiscounts[i] = fDisc
		afterFlash[i] = sub - fDisc
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
			disc, reason := evaluateVoucher(cand, domain.VoucherScopeShop, s.ShopID, afterFlash[i], afterFlash[i], now)
			if reason != "" {
				if in.Strict {
					return nil, fmt.Errorf("%w: %s", ErrVoucherIneligible, reason)
				}
				warnings = append(warnings, reason)
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
				})
			}
		}
		afterShop[i] = afterFlash[i] - shopVoucherDiscounts[i]
		totalAfterShop += afterShop[i]
	}

	// Step 2: PLATFORM voucher applied on post-shop-voucher subtotal (cost bearer = PLATFORM)
	var totalPlatformDiscount int64
	platformAlloc := make([]int64, n)
	if in.PlatformVoucher != nil {
		disc, reason := evaluateVoucher(in.PlatformVoucher, domain.VoucherScopePlatform, "", totalAfterShop, totalAfterShop, now)
		if reason != "" {
			if in.Strict {
				return nil, fmt.Errorf("%w: %s", ErrVoucherIneligible, reason)
			}
			warnings = append(warnings, reason)
		} else {
			totalPlatformDiscount = disc
			platformAlloc = allocateProportional(disc, afterShop)
			applied = append(applied, AppliedVoucher{
				VoucherID:    in.PlatformVoucher.Voucher.ID,
				Scope:        domain.VoucherScopePlatform,
				DiscountVND:  disc,
				HasBudget:    in.PlatformVoucher.Voucher.BudgetVND > 0,
				PerUserLimit: in.PlatformVoucher.Voucher.PerUserLimit,
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

	// Step 3: FREESHIP voucher applied against total shipping fee (cost bearer = PLATFORM)
	var totalFreeshipDiscount int64
	freeshipAlloc := make([]int64, n)
	if in.FreeshipVoucher != nil {
		disc, reason := evaluateVoucher(in.FreeshipVoucher, domain.VoucherScopeFreeship, "", totalAfterPlatform, totalShipping, now)
		if reason != "" {
			if in.Strict {
				return nil, fmt.Errorf("%w: %s", ErrVoucherIneligible, reason)
			}
			warnings = append(warnings, reason)
		} else {
			totalFreeshipDiscount = disc
			freeshipAlloc = allocateProportional(disc, shippingFees)
			applied = append(applied, AppliedVoucher{
				VoucherID:    in.FreeshipVoucher.Voucher.ID,
				Scope:        domain.VoucherScopeFreeship,
				DiscountVND:  disc,
				HasBudget:    in.FreeshipVoucher.Voucher.BudgetVND > 0,
				PerUserLimit: in.FreeshipVoucher.Voucher.PerUserLimit,
			})
		}
	}

	finalShipping := make([]int64, n)
	payableBeforeCoins := make([]int64, n)
	var totalPayableBeforeCoins int64
	for i := 0; i < n; i++ {
		finalShipping[i] = shippingFees[i] - freeshipAlloc[i]
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

	// Assemble final per-shop and total breakdown with strict accounting identities
	shopBreakdowns := make([]*promotionv1.ShopDiscountBreakdown, n)
	var totalSubtotal int64
	var totalFlashDiscount int64
	var totalShopVoucherDiscount int64
	var totalFinalPayable int64

	for i, s := range in.Shops {
		shopFinal := payableBeforeCoins[i] - coinsAlloc[i]
		shopBreakdowns[i] = &promotionv1.ShopDiscountBreakdown{
			ShopId:                     s.ShopID,
			SubtotalVnd:                subtotals[i],
			FlashSaleDiscountVnd:       flashDiscounts[i],
			ShopVoucherId:              shopVoucherIDs[i],
			ShopVoucherDiscountVnd:     shopVoucherDiscounts[i],
			PlatformVoucherDiscountVnd: platformAlloc[i],
			FreeshipDiscountVnd:        freeshipAlloc[i],
			CoinsDiscountVnd:           coinsAlloc[i],
			ShippingFeeVnd:             shippingFees[i],
			FinalShippingFeeVnd:        finalShipping[i],
			FinalPayableVnd:            shopFinal,
		}
		totalSubtotal += subtotals[i]
		totalFlashDiscount += flashDiscounts[i]
		totalShopVoucherDiscount += shopVoucherDiscounts[i]
		totalFinalPayable += shopFinal
	}

	shopCost := totalFlashDiscount + totalShopVoucherDiscount
	platformCost := totalPlatformDiscount + totalFreeshipDiscount + coinsUsed
	totalDiscount := shopCost + platformCost

	return &Output{
		Breakdown: &promotionv1.DiscountBreakdown{
			Shops:                           shopBreakdowns,
			TotalSubtotalVnd:                totalSubtotal,
			TotalFlashSaleDiscountVnd:       totalFlashDiscount,
			TotalShopVoucherDiscountVnd:     totalShopVoucherDiscount,
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
		AppliedVouchers: applied,
		CoinsUsed:       coinsUsed,
	}, nil
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
	if cand == nil {
		return 0, ""
	}
	if !cand.Found || cand.Voucher == nil {
		return 0, fmt.Sprintf("voucher %s not found", cand.RequestedID)
	}
	v := cand.Voucher
	if !v.IsActive {
		return 0, fmt.Sprintf("voucher %s is inactive", v.Code)
	}
	if v.Scope != expectedScope {
		return 0, fmt.Sprintf("voucher %s scope %s does not match required scope %s", v.Code, v.Scope, expectedScope)
	}
	if expectedScope == domain.VoucherScopeShop && v.ShopID != expectedShopID {
		return 0, fmt.Sprintf("voucher %s does not belong to shop %s", v.Code, expectedShopID)
	}
	if now.Before(v.StartsAt) {
		return 0, fmt.Sprintf("voucher %s has not started yet", v.Code)
	}
	if now.After(v.EndsAt) {
		return 0, fmt.Sprintf("voucher %s has expired", v.Code)
	}
	if cand.RemainingQuota <= 0 {
		return 0, fmt.Sprintf("voucher %s is out of quota", v.Code)
	}
	if v.PerUserLimit > 0 && cand.UserUsedCount >= v.PerUserLimit {
		return 0, fmt.Sprintf("voucher %s per-user limit reached", v.Code)
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
