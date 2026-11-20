package lua

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

const (
	// DefaultReservationTTLSeconds keeps reservation/release tombstones for 30 days.
	DefaultReservationTTLSeconds = 30 * 24 * 3600
)

var (
	//go:embed reserve_atomic.lua
	reserveAtomicLua string

	//go:embed release_atomic.lua
	releaseAtomicLua string

	reserveScript = redis.NewScript(reserveAtomicLua)
	releaseScript = redis.NewScript(releaseAtomicLua)
)

// ReservationErrorCode categorizes failures returned by reserve_atomic.lua.
type ReservationErrorCode string

const (
	ErrCodeFlashSoldOut             ReservationErrorCode = "FLASH_SOLD_OUT"
	ErrCodeFlashUserLimitExceeded   ReservationErrorCode = "FLASH_USER_LIMIT_EXCEEDED"
	ErrCodeFlashNotInitialized      ReservationErrorCode = "FLASH_NOT_INITIALIZED"
	ErrCodeInvalidFlashQty          ReservationErrorCode = "INVALID_FLASH_QTY"
	ErrCodeVoucherOutOfQuota        ReservationErrorCode = "VOUCHER_OUT_OF_QUOTA"
	ErrCodeVoucherUserLimitExceeded ReservationErrorCode = "VOUCHER_USER_LIMIT_EXCEEDED"
	ErrCodeVoucherBudgetExhausted   ReservationErrorCode = "VOUCHER_BUDGET_EXHAUSTED"
	ErrCodeVoucherNotInitialized    ReservationErrorCode = "VOUCHER_NOT_INITIALIZED"
	ErrCodeVoucherBudgetNotInit     ReservationErrorCode = "VOUCHER_BUDGET_NOT_INITIALIZED"
	ErrCodeOrderAlreadyReleased     ReservationErrorCode = "ORDER_ALREADY_RELEASED"
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

// ReserveInput holds all parameters for an atomic promotion reservation.
type ReserveInput struct {
	OrderID    string
	UserID     string
	TTLSeconds int
	FlashItems []FlashReserveSpec
	Vouchers   []VoucherReserveSpec
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

// Engine executes embedded Lua scripts and key operations against Redis.
type Engine struct {
	rdb redis.UniversalClient
}

// NewEngine creates a Redis Lua promotion engine.
func NewEngine(rdb redis.UniversalClient) *Engine {
	return &Engine{rdb: rdb}
}

// InitVoucherKeys sets the initial Redis quota and optional budget keys for a voucher.
func (e *Engine) InitVoucherKeys(ctx context.Context, voucherID string, quota int32, budgetVND int64) error {
	pipe := e.rdb.TxPipeline()
	pipe.Set(ctx, VoucherRemainingKey(voucherID), quota, 0)
	if budgetVND > 0 {
		pipe.Set(ctx, VoucherBudgetRemainingKey(voucherID), budgetVND, 0)
	}
	_, err := pipe.Exec(ctx)
	return err
}

// SyncVoucherKeysIfMissing initializes missing Redis keys without overwriting live counters.
func (e *Engine) SyncVoucherKeysIfMissing(ctx context.Context, voucherID string, remainingQuota int32, budgetVND, remainingBudgetVND int64) error {
	pipe := e.rdb.Pipeline()
	pipe.SetNX(ctx, VoucherRemainingKey(voucherID), remainingQuota, 0)
	if budgetVND > 0 {
		pipe.SetNX(ctx, VoucherBudgetRemainingKey(voucherID), remainingBudgetVND, 0)
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

// IsReservationReleased reports whether promo:res:<order_id> has been marked RELEASED or removed.
func (e *Engine) IsReservationReleased(ctx context.Context, orderID string) bool {
	val, err := e.rdb.Get(ctx, ReservationKey(orderID)).Result()
	if errors.Is(err, redis.Nil) {
		return true
	}
	if err != nil {
		return false
	}
	return val == "RELEASED"
}

// MGetInt64 fetches multiple integer keys in a single round-trip.
func (e *Engine) MGetInt64(ctx context.Context, keys []string) (map[string]int64, error) {
	res := make(map[string]int64, len(keys))
	if len(keys) == 0 {
		return res, nil
	}
	vals, err := e.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	for i, raw := range vals {
		if raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if n, parseErr := strconv.ParseInt(v, 10, 64); parseErr == nil {
				res[keys[i]] = n
			}
		case int64:
			res[keys[i]] = v
		}
	}
	return res, nil
}

// Reserve runs reserve_atomic.lua atomically across all requested flash sale items and vouchers.
func (e *Engine) Reserve(ctx context.Context, in ReserveInput) (ReserveOutcome, error) {
	ttl := in.TTLSeconds
	if ttl == 0 {
		ttl = DefaultReservationTTLSeconds
	}

	keys := make([]string, 0, 1+len(in.FlashItems)*2+len(in.Vouchers)*3)
	args := make([]any, 0, 3+len(in.FlashItems)*2+len(in.Vouchers)*3)

	keys = append(keys, ReservationKey(in.OrderID))
	args = append(args, len(in.FlashItems), len(in.Vouchers), ttl)

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
		)
		args = append(args, v.PerUserLimit, v.DiscountVND, hasBudget)
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

// Release runs release_atomic.lua idempotently to restore flash sale slots, voucher quotas, budgets, and per-user counters.
func (e *Engine) Release(ctx context.Context, orderID string, extraKeys ...string) (ReleaseOutcome, error) {
	keys := make([]string, 0, 1+len(extraKeys))
	keys = append(keys, ReservationKey(orderID))
	keys = append(keys, extraKeys...)

	raw, err := releaseScript.Run(ctx, e.rdb, keys, DefaultReservationTTLSeconds).Result()
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
