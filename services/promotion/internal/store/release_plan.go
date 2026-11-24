package store

import (
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/services/promotion/internal/domain"
)

// UserUsageKey identifies one (kind, target_id) counter in promotion_user_usage.
type UserUsageKey struct {
	Kind     string
	TargetID string
}

// FullReleaseResult captures the outcome of releasing all remaining portions of a reservation,
// including all promotion targets so callers can always converge Redis idempotently on retries.
type FullReleaseResult struct {
	AlreadyReleased bool
	UserID          string
	CoinsReturned   int64
	Vouchers        []domain.ReservedVoucher
	FlashSaleItems  []domain.ReservedFlashItem
	ShopPrograms    []domain.ReservedShopProgramItem
}

// ShopReleaseResult captures the outcome of releasing a single shop's share of a reservation,
// including Redis sync targets so callers can keep Redis idempotently in sync on retries.
type ShopReleaseResult struct {
	AlreadyReleased             bool
	AllReleased                 bool
	UserID                      string
	CoinsReturned               int64
	ShopVoucherReleased         bool
	FlashQuantityReleased       int32
	ShopProgramQuantityReleased int32
	PlatformVoucherReleased     bool
	TargetVouchers              []domain.ReservedVoucher
	TargetFlash                 []domain.ReservedFlashItem
	TargetShopPrograms          []domain.ReservedShopProgramItem
	AllVouchers                 []domain.ReservedVoucher
	AllFlash                    []domain.ReservedFlashItem
	AllShopPrograms             []domain.ReservedShopProgramItem
}

type shopReleasePlan struct {
	alreadyReleased             bool
	allReleased                 bool
	coinsToReturn               int64
	vouchersToRelease           []domain.ReservedVoucher
	flashToRelease              []domain.ReservedFlashItem
	shopProgramsToRelease       []domain.ReservedShopProgramItem
	targetVouchersForRedis      []domain.ReservedVoucher
	targetFlashForRedis         []domain.ReservedFlashItem
	targetShopProgramsForRedis  []domain.ReservedShopProgramItem
	shopVoucherReleased         bool
	flashQuantityReleased       int32
	shopProgramQuantityReleased int32
	platformVoucherReleased     bool
	newReleasedShops            []string
}

type fullRemainingPlan struct {
	alreadyReleased       bool
	coinsToReturn         int64
	vouchersToRelease     []domain.ReservedVoucher
	flashToRelease        []domain.ReservedFlashItem
	shopProgramsToRelease []domain.ReservedShopProgramItem
	newReleasedShops      []string
}

func unmarshalBreakdownJSON(raw []byte) *promotionv1.DiscountBreakdown {
	bd := &promotionv1.DiscountBreakdown{}
	if len(raw) > 0 {
		_ = protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(raw, bd)
	}
	return bd
}

func buildShopSet(shops []string) map[string]bool {
	set := make(map[string]bool, len(shops))
	for _, sid := range shops {
		if trimmed := strings.TrimSpace(sid); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}

func voucherShopID(rv domain.ReservedVoucher, voucherShopByID map[string]string) string {
	if sid := strings.TrimSpace(rv.ShopID); sid != "" {
		return sid
	}
	return voucherShopByID[rv.VoucherID]
}

func remainingCoinsAfterReleased(
	res *domain.PromotionReservation,
	bd *promotionv1.DiscountBreakdown,
	releasedSet map[string]bool,
) int64 {
	if res.CoinsUsed <= 0 {
		return 0
	}
	var alreadyReturned int64
	for _, sb := range bd.GetShops() {
		if releasedSet[strings.TrimSpace(sb.GetShopId())] {
			if c := sb.GetCoinsDiscountVnd(); c > 0 {
				alreadyReturned += c
			}
		}
	}
	rem := res.CoinsUsed - alreadyReturned
	if rem < 0 {
		return 0
	}
	if rem > res.CoinsUsed {
		return res.CoinsUsed
	}
	return rem
}

func resolveReservationShops(
	res *domain.PromotionReservation,
	bd *promotionv1.DiscountBreakdown,
	lookupFlashShop func(itemID string) (string, error),
	lookupShopProgShop func(itemID string) (string, error),
) (
	allShops map[string]bool,
	flashShopByItemID map[string]string,
	shopProgShopByItemID map[string]string,
	voucherShopByID map[string]string,
	err error,
) {
	allShops = make(map[string]bool)
	voucherShopByID = make(map[string]string)
	flashShopByItemID = make(map[string]string)
	shopProgShopByItemID = make(map[string]string)

	for _, sb := range bd.GetShops() {
		sid := strings.TrimSpace(sb.GetShopId())
		if sid == "" {
			continue
		}
		allShops[sid] = true
		if svid := strings.TrimSpace(sb.GetShopVoucherId()); svid != "" {
			voucherShopByID[svid] = sid
		}
	}

	for _, rv := range res.Vouchers {
		if rv.Scope != domain.VoucherScopeShop {
			continue
		}
		if sid := voucherShopID(rv, voucherShopByID); sid != "" {
			allShops[sid] = true
			voucherShopByID[rv.VoucherID] = sid
		}
	}

	flashLookupCache := make(map[string]string)
	for _, rf := range res.FlashSaleItems {
		sid := strings.TrimSpace(rf.ShopID)
		if sid == "" && lookupFlashShop != nil {
			if cached, ok := flashLookupCache[rf.FlashSaleItemID]; ok {
				sid = cached
			} else {
				looked, lErr := lookupFlashShop(rf.FlashSaleItemID)
				if lErr != nil {
					return nil, nil, nil, nil, lErr
				}
				sid = strings.TrimSpace(looked)
				flashLookupCache[rf.FlashSaleItemID] = sid
			}
		}
		if sid != "" {
			allShops[sid] = true
			flashShopByItemID[rf.FlashSaleItemID] = sid
		}
	}

	progLookupCache := make(map[string]string)
	for _, rp := range res.ShopProgramItems {
		sid := strings.TrimSpace(rp.ShopID)
		if sid == "" && lookupShopProgShop != nil {
			if cached, ok := progLookupCache[rp.ShopDiscountProgramItemID]; ok {
				sid = cached
			} else {
				looked, lErr := lookupShopProgShop(rp.ShopDiscountProgramItemID)
				if lErr != nil {
					return nil, nil, nil, nil, lErr
				}
				sid = strings.TrimSpace(looked)
				progLookupCache[rp.ShopDiscountProgramItemID] = sid
			}
		}
		if sid != "" {
			allShops[sid] = true
			shopProgShopByItemID[rp.ShopDiscountProgramItemID] = sid
		}
	}

	return allShops, flashShopByItemID, shopProgShopByItemID, voucherShopByID, nil
}

func planShopRelease(
	res *domain.PromotionReservation,
	shopID string,
	lookupFlashShop func(itemID string) (string, error),
	lookupShopProgShop func(itemID string) (string, error),
) (shopReleasePlan, error) {
	bd := unmarshalBreakdownJSON(res.BreakdownJSON)
	allShops, flashShopByItemID, shopProgShopByItemID, voucherShopByID, err := resolveReservationShops(res, bd, lookupFlashShop, lookupShopProgShop)
	if err != nil {
		return shopReleasePlan{}, err
	}

	var shopVouchers []domain.ReservedVoucher
	for _, rv := range res.Vouchers {
		if rv.Scope == domain.VoucherScopeShop && voucherShopID(rv, voucherShopByID) == shopID {
			shopVouchers = append(shopVouchers, rv)
		}
	}

	var shopFlash []domain.ReservedFlashItem
	var flashQty int32
	for _, rf := range res.FlashSaleItems {
		if flashShopByItemID[rf.FlashSaleItemID] == shopID {
			shopFlash = append(shopFlash, rf)
			flashQty += rf.Quantity
		}
	}

	var shopProgs []domain.ReservedShopProgramItem
	var progQty int32
	for _, rp := range res.ShopProgramItems {
		if shopProgShopByItemID[rp.ShopDiscountProgramItemID] == shopID {
			shopProgs = append(shopProgs, rp)
			progQty += rp.Quantity
		}
	}

	releasedSet := buildShopSet(res.ReleasedShops)
	if res.Status == domain.ReservationStatusReleased || releasedSet[shopID] {
		return shopReleasePlan{
			alreadyReleased:            true,
			allReleased:                res.Status == domain.ReservationStatusReleased,
			targetVouchersForRedis:     shopVouchers,
			targetFlashForRedis:        shopFlash,
			targetShopProgramsForRedis: shopProgs,
		}, nil
	}

	remainingCoins := remainingCoinsAfterReleased(res, bd, releasedSet)
	var shopCoins int64
	for _, sb := range bd.GetShops() {
		if strings.TrimSpace(sb.GetShopId()) == shopID {
			shopCoins += sb.GetCoinsDiscountVnd()
		}
	}

	releasedSet[shopID] = true
	allReleased := len(allShops) > 0
	for sid := range allShops {
		if !releasedSet[sid] {
			allReleased = false
			break
		}
	}

	if allReleased {
		shopCoins = remainingCoins
	}
	if shopCoins > remainingCoins {
		shopCoins = remainingCoins
	}
	if shopCoins < 0 {
		shopCoins = 0
	}

	vouchersToRelease := append([]domain.ReservedVoucher(nil), shopVouchers...)
	platformVoucherReleased := false
	if allReleased {
		for _, rv := range res.Vouchers {
			if rv.Scope != domain.VoucherScopeShop {
				vouchersToRelease = append(vouchersToRelease, rv)
				platformVoucherReleased = true
			}
		}
	}

	return shopReleasePlan{
		alreadyReleased:             false,
		allReleased:                 allReleased,
		coinsToReturn:               shopCoins,
		vouchersToRelease:           vouchersToRelease,
		flashToRelease:              shopFlash,
		shopProgramsToRelease:       shopProgs,
		targetVouchersForRedis:      vouchersToRelease,
		targetFlashForRedis:         shopFlash,
		targetShopProgramsForRedis:  shopProgs,
		shopVoucherReleased:         len(shopVouchers) > 0,
		flashQuantityReleased:       flashQty,
		shopProgramQuantityReleased: progQty,
		platformVoucherReleased:     platformVoucherReleased,
		newReleasedShops:            sortedSetKeys(releasedSet),
	}, nil
}

func planRemainingRelease(
	res *domain.PromotionReservation,
	lookupFlashShop func(itemID string) (string, error),
	lookupShopProgShop func(itemID string) (string, error),
) (fullRemainingPlan, error) {
	if res.Status == domain.ReservationStatusReleased {
		return fullRemainingPlan{alreadyReleased: true}, nil
	}

	bd := unmarshalBreakdownJSON(res.BreakdownJSON)
	allShops, flashShopByItemID, shopProgShopByItemID, voucherShopByID, err := resolveReservationShops(res, bd, lookupFlashShop, lookupShopProgShop)
	if err != nil {
		return fullRemainingPlan{}, err
	}

	releasedSet := buildShopSet(res.ReleasedShops)
	coinsToReturn := remainingCoinsAfterReleased(res, bd, releasedSet)

	vouchersToRelease := make([]domain.ReservedVoucher, 0, len(res.Vouchers))
	for _, rv := range res.Vouchers {
		if rv.Scope == domain.VoucherScopeShop {
			if sid := voucherShopID(rv, voucherShopByID); sid != "" && releasedSet[sid] {
				continue
			}
		}
		vouchersToRelease = append(vouchersToRelease, rv)
	}

	flashToRelease := make([]domain.ReservedFlashItem, 0, len(res.FlashSaleItems))
	for _, rf := range res.FlashSaleItems {
		if sid := flashShopByItemID[rf.FlashSaleItemID]; sid != "" && releasedSet[sid] {
			continue
		}
		flashToRelease = append(flashToRelease, rf)
	}

	shopProgsToRelease := make([]domain.ReservedShopProgramItem, 0, len(res.ShopProgramItems))
	for _, rp := range res.ShopProgramItems {
		if sid := shopProgShopByItemID[rp.ShopDiscountProgramItemID]; sid != "" && releasedSet[sid] {
			continue
		}
		shopProgsToRelease = append(shopProgsToRelease, rp)
	}

	for sid := range allShops {
		releasedSet[sid] = true
	}

	return fullRemainingPlan{
		alreadyReleased:       false,
		coinsToReturn:         coinsToReturn,
		vouchersToRelease:     vouchersToRelease,
		flashToRelease:        flashToRelease,
		shopProgramsToRelease: shopProgsToRelease,
		newReleasedShops:      sortedSetKeys(releasedSet),
	}, nil
}

func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
