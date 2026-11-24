package lua

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

const (
	// DefaultReservationTTLSeconds keeps reservation/release tombstones for 400 days
	// so late returns and disputes never lose their Redis compensation payload.
	DefaultReservationTTLSeconds = 400 * 24 * 3600

	// DefaultUserUsageTTLSeconds bounds per-user Redis usage keys so any stale/orphaned
	// counter self-clears within 2 minutes while Postgres enforces authoritative per_user_limit (D12).
	DefaultUserUsageTTLSeconds = 120
)

var (
	//go:embed reserve_atomic.lua
	reserveAtomicLua string

	//go:embed release_atomic.lua
	releaseAtomicLua string

	//go:embed release_partial_atomic.lua
	releasePartialAtomicLua string

	reserveScript        = redis.NewScript(reserveAtomicLua)
	releaseScript        = redis.NewScript(releaseAtomicLua)
	releasePartialScript = redis.NewScript(releasePartialAtomicLua)
)

// ReservationErrorCode categorizes failures returned by reserve_atomic.lua.
type ReservationErrorCode string

const (
	ErrCodeFlashSoldOut              ReservationErrorCode = "FLASH_SOLD_OUT"
	ErrCodeFlashUserLimitExceeded    ReservationErrorCode = "FLASH_USER_LIMIT_EXCEEDED"
	ErrCodeFlashNotInitialized       ReservationErrorCode = "FLASH_NOT_INITIALIZED"
	ErrCodeInvalidFlashQty           ReservationErrorCode = "INVALID_FLASH_QTY"
	ErrCodeVoucherInactive           ReservationErrorCode = "VOUCHER_INACTIVE"
	ErrCodeVoucherOutOfQuota         ReservationErrorCode = "VOUCHER_OUT_OF_QUOTA"
	ErrCodeVoucherUserLimitExceeded  ReservationErrorCode = "VOUCHER_USER_LIMIT_EXCEEDED"
	ErrCodeVoucherBudgetExhausted    ReservationErrorCode = "VOUCHER_BUDGET_EXHAUSTED"
	ErrCodeVoucherNotInitialized     ReservationErrorCode = "VOUCHER_NOT_INITIALIZED"
	ErrCodeVoucherBudgetNotInit      ReservationErrorCode = "VOUCHER_BUDGET_NOT_INITIALIZED"
	ErrCodeShopProgUserLimitExceeded ReservationErrorCode = "SHOP_PROG_USER_LIMIT_EXCEEDED"
	ErrCodeInvalidShopProgQty        ReservationErrorCode = "INVALID_SHOP_PROG_QTY"
	ErrCodeOrderAlreadyReleased      ReservationErrorCode = "ORDER_ALREADY_RELEASED"
)

// ReservationError represents a business failure returned atomically by Redis Lua.
type ReservationError struct {
	Code ReservationErrorCode
	Key  string
}

func (e *ReservationError) Error() string {
	return fmt.Sprintf("promotion reservation rejected: %s (%s)", e.Code, e.Key)
}

// FlashReserveSpec describes one flash sale item to reserve atomically.
type FlashReserveSpec struct {
	ItemID       string
	Quantity     int32
	PerUserLimit int32
}

// VoucherReserveSpec describes one voucher to reserve atomically.
type VoucherReserveSpec struct {
	VoucherID    string
	PerUserLimit int32
	DiscountVND  int64
	HasBudget    bool
}

// ShopProgramReserveSpec describes one shop discount program item to reserve atomically.
type ShopProgramReserveSpec struct {
	ItemID       string
	Quantity     int32
	PerUserLimit int32
}

// ReserveInput holds all parameters for an atomic promotion reservation.
type ReserveInput struct {
	OrderID          string
	UserID           string
	TTLSeconds       int
	FlashItems       []FlashReserveSpec
	Vouchers         []VoucherReserveSpec
	ShopProgramItems []ShopProgramReserveSpec
}

// ReserveOutcome captures the result of reserve_atomic.lua.
type ReserveOutcome struct {
	Reserved        bool
	AlreadyReserved bool
}

// ReleaseOutcome captures the result of release_atomic.lua.
type ReleaseOutcome struct {
	Released        bool
	AlreadyReleased bool
}

// Key helper functions matching NexusMart promotion Redis schema.

func ReservationKey(orderID string) string {
	return "promo:res:" + orderID
}

func FlashRemainingKey(itemID string) string {
	return "promo:flash:" + itemID + ":remaining"
}

func FlashUserKey(itemID, userID string) string {
	return "promo:flash:" + itemID + ":user:" + userID
}

func VoucherRemainingKey(voucherID string) string {
	return "promo:voucher:" + voucherID + ":remaining"
}

func VoucherUserKey(voucherID, userID string) string {
	return "promo:voucher:" + voucherID + ":user:" + userID
}

func VoucherBudgetRemainingKey(voucherID string) string {
	return "promo:voucher:" + voucherID + ":budget_remaining"
}

func VoucherStatusKey(voucherID string) string {
	return "promo:voucher:" + voucherID + ":status"
}

func ShopProgramUserKey(itemID, userID string) string {
	return "promo:shop_prog:" + itemID + ":user:" + userID
}

// Engine executes embedded Lua scripts and key operations against Redis.
type Engine struct {
	rdb redis.UniversalClient
}

// NewEngine creates a Redis Lua promotion engine.
func NewEngine(rdb redis.UniversalClient) *Engine {
	return &Engine{rdb: rdb}
}

// ReconcileVoucherKeys authoritatively converges a voucher's Redis status, remaining quota,
// and remaining budget (or deletes the budget key when budgetVND == 0) to exact Postgres values.
// Remaining quota is strictly bounded to [0, quota] and remaining budget to [0, budgetVND].
func (e *Engine) ReconcileVoucherKeys(
	ctx context.Context,
	voucherID, status string,
	quota, usedCount int32,
	budgetVND, usedBudgetVND int64,
) error {
	remQuota := min(max(quota-usedCount, 0), quota)
	remBudget := min(max(budgetVND-usedBudgetVND, 0), budgetVND)

	pipe := e.rdb.TxPipeline()
	pipe.Set(ctx, VoucherStatusKey(voucherID), status, 0)
	pipe.Set(ctx, VoucherRemainingKey(voucherID), remQuota, 0)
	if budgetVND > 0 {
		pipe.Set(ctx, VoucherBudgetRemainingKey(voucherID), remBudget, 0)
	} else {
		pipe.Del(ctx, VoucherBudgetRemainingKey(voucherID))
	}
	_, err := pipe.Exec(ctx)
	return err
}

func userUsageKey(kind, targetID, userID string) (string, bool) {
	if targetID == "" || userID == "" {
		return "", false
	}
	switch kind {
	case domain.UsageKindVoucher:
		return VoucherUserKey(targetID, userID), true
	case domain.UsageKindFlash:
		return FlashUserKey(targetID, userID), true
	case domain.UsageKindShopProgram:
		return ShopProgramUserKey(targetID, userID), true
	default:
		return "", false
	}
}

// ReconcileUserUsageKeys authoritatively converges per-user usage counters in Redis from Postgres:
// positive counts are SET with 120s TTL and zero counts are DELETED.
func (e *Engine) ReconcileUserUsageKeys(ctx context.Context, entries []domain.UserUsage) error {
	pipe := e.rdb.Pipeline()
	queued := 0
	for _, u := range entries {
		key, ok := userUsageKey(u.Kind, u.TargetID, u.UserID)
		if !ok {
			continue
		}
		if u.UsedQty <= 0 {
			pipe.Del(ctx, key)
		} else {
			pipe.Set(ctx, key, u.UsedQty, time.Duration(DefaultUserUsageTTLSeconds)*time.Second)
		}
		queued++
	}
	if queued == 0 {
		return nil
	}
	_, err := pipe.Exec(ctx)
	return err
}

// InitFlashItemSlots sets the atomic slot counter when a flash sale item is approved.
func (e *Engine) InitFlashItemSlots(ctx context.Context, itemID string, totalSlots int32, overwrite bool) error {
	if overwrite {
		return e.rdb.Set(ctx, FlashRemainingKey(itemID), totalSlots, 0).Err()
	}
	return e.rdb.SetNX(ctx, FlashRemainingKey(itemID), totalSlots, 0).Err()
}

// DeleteFlashItemSlots removes the Redis slot key when a flash sale item is rejected.
func (e *Engine) DeleteFlashItemSlots(ctx context.Context, itemID string) error {
	return e.rdb.Del(ctx, FlashRemainingKey(itemID)).Err()
}

// GetInt64 fetches an integer value from Redis, returning (val, exists, err).
func (e *Engine) GetInt64(ctx context.Context, key string) (int64, bool, error) {
	val, err := e.rdb.Get(ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return val, true, nil
}

// IsReservationReleased reports whether promo:res:<order_id> is explicitly marked RELEASED.
// An absent key returns false so waitForCommittedReservation does not abort while UndoReserve+retry is in flight.
func (e *Engine) IsReservationReleased(ctx context.Context, orderID string) bool {
	val, err := e.rdb.Get(ctx, ReservationKey(orderID)).Result()
	if err != nil {
		return false
	}
	return val == "RELEASED"
}

// MGetStrings fetches multiple string keys in a single round-trip.
func (e *Engine) MGetStrings(ctx context.Context, keys []string) (map[string]string, error) {
	res := make(map[string]string, len(keys))
	if len(keys) == 0 {
		return res, nil
	}
	vals, err := e.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, raw := range vals {
		if raw != nil {
			res[keys[i]] = fmt.Sprint(raw)
		}
	}
	return res, nil
}

// MGetInt64 fetches multiple integer keys in a single round-trip.
func (e *Engine) MGetInt64(ctx context.Context, keys []string) (map[string]int64, error) {
	raw, err := e.MGetStrings(ctx, keys)
	if err != nil {
		return nil, err
	}
	res := make(map[string]int64, len(raw))
	for k, s := range raw {
		if n, parseErr := strconv.ParseInt(s, 10, 64); parseErr == nil {
			res[k] = n
		}
	}
	return res, nil
}

// Reserve runs reserve_atomic.lua atomically across all requested flash sale items, vouchers,
// and shop discount program per-user limits.
func (e *Engine) Reserve(ctx context.Context, in ReserveInput) (ReserveOutcome, error) {
	ttl := in.TTLSeconds
	if ttl == 0 {
		ttl = DefaultReservationTTLSeconds
	}

	keys := make([]string, 0, 1+len(in.FlashItems)*2+len(in.Vouchers)*4+len(in.ShopProgramItems))
	args := make([]any, 0, 5+len(in.FlashItems)*2+len(in.Vouchers)*3+len(in.ShopProgramItems)*2)

	keys = append(keys, ReservationKey(in.OrderID))
	args = append(args, len(in.FlashItems), len(in.Vouchers), ttl, len(in.ShopProgramItems), DefaultUserUsageTTLSeconds)

	for _, f := range in.FlashItems {
		keys = append(keys, FlashRemainingKey(f.ItemID), FlashUserKey(f.ItemID, in.UserID))
		args = append(args, f.Quantity, f.PerUserLimit)
	}

	for _, v := range in.Vouchers {
		hasBudget := 0
		if v.HasBudget {
			hasBudget = 1
		}
		keys = append(
			keys,
			VoucherRemainingKey(v.VoucherID),
			VoucherUserKey(v.VoucherID, in.UserID),
			VoucherBudgetRemainingKey(v.VoucherID),
			VoucherStatusKey(v.VoucherID),
		)
		args = append(args, v.PerUserLimit, v.DiscountVND, hasBudget)
	}

	for _, p := range in.ShopProgramItems {
		keys = append(keys, ShopProgramUserKey(p.ItemID, in.UserID))
		args = append(args, p.Quantity, p.PerUserLimit)
	}

	raw, err := reserveScript.Run(ctx, e.rdb, keys, args...).Result()
	if err != nil {
		return ReserveOutcome{}, fmt.Errorf("run reserve_atomic.lua: %w", err)
	}

	parts, err := toStringSlice(raw)
	if err != nil || len(parts) == 0 {
		return ReserveOutcome{}, fmt.Errorf("unexpected reserve_atomic.lua reply: %v", raw)
	}

	switch parts[0] {
	case "RESERVED":
		return ReserveOutcome{Reserved: true, AlreadyReserved: false}, nil
	case "ALREADY_RESERVED":
		return ReserveOutcome{Reserved: true, AlreadyReserved: true}, nil
	case "ERR":
		code := ReservationErrorCode("")
		key := ""
		if len(parts) > 1 {
			code = ReservationErrorCode(parts[1])
		}
		if len(parts) > 2 {
			key = parts[2]
		}
		return ReserveOutcome{}, &ReservationError{Code: code, Key: key}
	default:
		return ReserveOutcome{}, fmt.Errorf("unknown reserve_atomic.lua status: %q", parts[0])
	}
}

// Release runs release_atomic.lua idempotently to restore flash sale slots, voucher quotas, budgets,
// and per-user counters (including shop discount program items), writing a RELEASED tombstone.
func (e *Engine) Release(ctx context.Context, orderID string, extraKeys ...string) (ReleaseOutcome, error) {
	return e.runRelease(ctx, orderID, DefaultReservationTTLSeconds, 0, extraKeys...)
}

// UndoReserve cleanly reverts a reserved order's Redis counters and deletes promo:res:<order_id>
// WITHOUT writing a RELEASED tombstone so the same order_id can immediately retry Reserve at regular price.
// Existing RELEASED tombstones are left intact.
func (e *Engine) UndoReserve(ctx context.Context, orderID string, extraKeys ...string) (ReleaseOutcome, error) {
	return e.runRelease(ctx, orderID, 0, 1, extraKeys...)
}

func (e *Engine) runRelease(ctx context.Context, orderID string, ttlSeconds, noTombstone int, extraKeys ...string) (ReleaseOutcome, error) {
	keys := make([]string, 0, 1+len(extraKeys))
	keys = append(keys, ReservationKey(orderID))
	keys = append(keys, extraKeys...)

	raw, err := releaseScript.Run(ctx, e.rdb, keys, ttlSeconds, noTombstone, DefaultUserUsageTTLSeconds).Result()
	if err != nil {
		return ReleaseOutcome{}, fmt.Errorf("run release_atomic.lua: %w", err)
	}

	parts, err := toStringSlice(raw)
	if err != nil || len(parts) == 0 {
		return ReleaseOutcome{}, fmt.Errorf("unexpected release_atomic.lua reply: %v", raw)
	}

	switch parts[0] {
	case "RELEASED":
		return ReleaseOutcome{Released: true, AlreadyReleased: false}, nil
	case "ALREADY_RELEASED":
		return ReleaseOutcome{Released: true, AlreadyReleased: true}, nil
	default:
		return ReleaseOutcome{}, fmt.Errorf("unknown release_atomic.lua status: %q", parts[0])
	}
}

// ReleasePartial idempotently releases only the specified target keys from promo:res:<order_id>,
// leaving unreleased flash items, vouchers, and shop discount program items intact for subsequent releases.
func (e *Engine) ReleasePartial(ctx context.Context, orderID string, targetKeys []string, extraKeys ...string) (ReleaseOutcome, error) {
	if len(targetKeys) == 0 {
		return ReleaseOutcome{Released: true, AlreadyReleased: true}, nil
	}

	keys := make([]string, 0, 1+len(extraKeys))
	keys = append(keys, ReservationKey(orderID))
	keys = append(keys, extraKeys...)

	args := make([]any, 0, 2+len(targetKeys))
	args = append(args, DefaultReservationTTLSeconds, DefaultUserUsageTTLSeconds)
	for _, k := range targetKeys {
		args = append(args, k)
	}

	raw, err := releasePartialScript.Run(ctx, e.rdb, keys, args...).Result()
	if err != nil {
		return ReleaseOutcome{}, fmt.Errorf("run release_partial_atomic.lua: %w", err)
	}

	parts, err := toStringSlice(raw)
	if err != nil || len(parts) == 0 {
		return ReleaseOutcome{}, fmt.Errorf("unexpected release_partial_atomic.lua reply: %v", raw)
	}

	switch parts[0] {
	case "RELEASED":
		return ReleaseOutcome{Released: true, AlreadyReleased: false}, nil
	case "ALREADY_RELEASED":
		return ReleaseOutcome{Released: true, AlreadyReleased: true}, nil
	default:
		return ReleaseOutcome{}, fmt.Errorf("unknown release_partial_atomic.lua status: %q", parts[0])
	}
}

func toStringSlice(raw any) ([]string, error) {
	slice, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array reply, got %T", raw)
	}
	out := make([]string, len(slice))
	for i, item := range slice {
		switch v := item.(type) {
		case string:
			out[i] = v
		case []byte:
			out[i] = string(v)
		default:
			out[i] = fmt.Sprint(v)
		}
	}
	return out, nil
}
