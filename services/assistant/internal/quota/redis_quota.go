package quota

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	cacheKeyPrefix = "assistant:cache:"
	defaultTTL     = 10 * time.Minute
)

// Config tunes daily per-user token limits, global daily VND cost limits, and abuse lockouts.
type Config struct {
	DailyUserTokenLimit int64
	DailyCostLimitVND   int64
	AbuseStrikeLimit    int
	AbuseLockoutWindow  time.Duration
	Now                 func() time.Time
}

var vietnamLocation = loadVietnamLocation()

func loadVietnamLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		return time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)
	}
	return loc
}

func normalizeConfig(cfg Config) Config {
	if cfg.DailyUserTokenLimit <= 0 {
		cfg.DailyUserTokenLimit = 50000
	}
	if cfg.DailyCostLimitVND <= 0 {
		cfg.DailyCostLimitVND = 500000
	}
	if cfg.AbuseStrikeLimit <= 0 {
		cfg.AbuseStrikeLimit = 5
	}
	if cfg.AbuseLockoutWindow <= 0 {
		cfg.AbuseLockoutWindow = 15 * time.Minute
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return cfg
}

func vietnamDayWindow(now time.Time) (dateStr string, ttlSec int64, retryAfterSec int64) {
	local := now.In(vietnamLocation)
	dateStr = local.Format("2006-01-02")
	nextMidnight := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, vietnamLocation)
	retryAfterSec = int64(math.Ceil(nextMidnight.Sub(local).Seconds()))
	if retryAfterSec < 1 {
		retryAfterSec = 1
	}
	ttlSec = retryAfterSec + 3600
	return dateStr, ttlSec, retryAfterSec
}

var (
	reserveTokensScript = redis.NewScript(`
local cur = tonumber(redis.call("GET", KEYS[1]) or "0")
local est = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local ttl = tonumber(ARGV[3])
if est < 0 then est = 0 end
if cur + est > limit then
	return {0, cur}
end
local updated = redis.call("INCRBY", KEYS[1], est)
if cur == 0 or redis.call("TTL", KEYS[1]) < 0 then
	redis.call("EXPIRE", KEYS[1], ttl)
end
return {1, updated}
`)

	adjustTokensScript = redis.NewScript(`
local cur = tonumber(redis.call("GET", KEYS[1]) or "0")
local delta = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
local nextVal = cur + delta
if nextVal < 0 then
	nextVal = 0
end
redis.call("SET", KEYS[1], nextVal, "EX", ttl)
return nextVal
`)

	recordCostScript = redis.NewScript(`
local add = tonumber(ARGV[1])
local ttl = tonumber(ARGV[2])
if add < 0 then add = 0 end
local updated = redis.call("INCRBY", KEYS[1], add)
if redis.call("TTL", KEYS[1]) < 0 then
	redis.call("EXPIRE", KEYS[1], ttl)
end
return updated
`)

	recordAbuseScript = redis.NewScript(`
local strikes = redis.call("INCR", KEYS[1])
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
if strikes == 1 or strikes >= limit then
	redis.call("EXPIRE", KEYS[1], window)
end
local ttl = redis.call("TTL", KEYS[1])
if ttl < 0 then
	redis.call("EXPIRE", KEYS[1], window)
	ttl = window
end
return {strikes, ttl}
`)

	checkAbuseScript = redis.NewScript(`
local cur = tonumber(redis.call("GET", KEYS[1]) or "0")
local ttl = redis.call("TTL", KEYS[1])
if ttl < 0 then ttl = 0 end
return {cur, ttl}
`)
)

// RedisManager enforces daily token quotas, global VND cost caps, response caching,
// and repeated-injection abuse lockouts atomically on Redis using Lua scripts.
type RedisManager struct {
	rdb *redis.Client
	cfg Config
}

// NewRedisManager constructs a Redis-backed quota.Manager.
func NewRedisManager(rdb *redis.Client, cfg Config) *RedisManager {
	return &RedisManager{
		rdb: rdb,
		cfg: normalizeConfig(cfg),
	}
}

func (m *RedisManager) tokenKey(dateStr, subject string) string {
	return fmt.Sprintf("assistant:quota:tokens:%s:%s", dateStr, normalizeSubject(subject))
}

func (m *RedisManager) costKey(dateStr string) string {
	return fmt.Sprintf("assistant:quota:cost:%s", dateStr)
}

func (m *RedisManager) abuseKey(subject string) string {
	return fmt.Sprintf("assistant:abuse:strikes:%s", normalizeSubject(subject))
}

func normalizeSubject(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return "anonymous"
	}
	return s
}

// CheckAndReserveTokens atomically checks the subject's daily token quota and reserves estimatedTokens.
func (m *RedisManager) CheckAndReserveTokens(ctx context.Context, subject string, estimatedTokens int64) (TokenDecision, error) {
	if m.rdb == nil {
		return TokenDecision{}, errors.New("redis client is nil")
	}
	dateStr, ttlSec, retryAfterSec := vietnamDayWindow(m.cfg.Now())
	key := m.tokenKey(dateStr, subject)

	vals, err := reserveTokensScript.Run(ctx, m.rdb, []string{key}, estimatedTokens, m.cfg.DailyUserTokenLimit, ttlSec).Int64Slice()
	if err != nil {
		return TokenDecision{}, fmt.Errorf("reserve tokens lua: %w", err)
	}
	if len(vals) < 2 {
		return TokenDecision{}, errors.New("unexpected reserve tokens script response")
	}

	allowed := vals[0] == 1
	used := vals[1]
	remaining := m.cfg.DailyUserTokenLimit - used
	if remaining < 0 {
		remaining = 0
	}
	retry := int64(0)
	if !allowed {
		retry = retryAfterSec
	}
	return TokenDecision{
		Allowed:       allowed,
		UsedTokens:    used,
		LimitTokens:   m.cfg.DailyUserTokenLimit,
		Remaining:     remaining,
		RetryAfterSec: retry,
	}, nil
}

// RecordActualTokens reconciles actual token usage by applying deltaTokens (positive or negative).
func (m *RedisManager) RecordActualTokens(ctx context.Context, subject string, deltaTokens int64) error {
	if m.rdb == nil {
		return errors.New("redis client is nil")
	}
	dateStr, ttlSec, _ := vietnamDayWindow(m.cfg.Now())
	key := m.tokenKey(dateStr, subject)
	if err := adjustTokensScript.Run(ctx, m.rdb, []string{key}, deltaTokens, ttlSec).Err(); err != nil {
		return fmt.Errorf("record actual tokens lua: %w", err)
	}
	return nil
}

// CheckDailyCost checks whether the global daily LLM VND spend is below DailyCostLimitVND.
func (m *RedisManager) CheckDailyCost(ctx context.Context) (CostDecision, error) {
	if m.rdb == nil {
		return CostDecision{}, errors.New("redis client is nil")
	}
	dateStr, _, retryAfterSec := vietnamDayWindow(m.cfg.Now())
	key := m.costKey(dateStr)

	var used int64
	raw, err := m.rdb.Get(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return CostDecision{}, fmt.Errorf("get daily cost: %w", err)
	}
	if raw != "" {
		parsed, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			return CostDecision{}, fmt.Errorf("parse daily cost %q: %w", raw, parseErr)
		}
		used = parsed
	}

	return buildCostDecision(used, m.cfg.DailyCostLimitVND, retryAfterSec), nil
}

// RecordCostVND atomically adds costVND to today's global spend counter.
func (m *RedisManager) RecordCostVND(ctx context.Context, costVND int64) (CostDecision, error) {
	if m.rdb == nil {
		return CostDecision{}, errors.New("redis client is nil")
	}
	dateStr, ttlSec, retryAfterSec := vietnamDayWindow(m.cfg.Now())
	key := m.costKey(dateStr)

	updated, err := recordCostScript.Run(ctx, m.rdb, []string{key}, costVND, ttlSec).Int64()
	if err != nil {
		return CostDecision{}, fmt.Errorf("record daily cost lua: %w", err)
	}
	return buildCostDecision(updated, m.cfg.DailyCostLimitVND, retryAfterSec), nil
}

// CheckAbuse reports whether subject is currently locked out from repeated abuse strikes.
func (m *RedisManager) CheckAbuse(ctx context.Context, subject string) (AbuseDecision, error) {
	if m.rdb == nil {
		return AbuseDecision{}, errors.New("redis client is nil")
	}
	vals, err := checkAbuseScript.Run(ctx, m.rdb, []string{m.abuseKey(subject)}).Int64Slice()
	if err != nil {
		return AbuseDecision{}, fmt.Errorf("check abuse lua: %w", err)
	}
	if len(vals) < 2 {
		return AbuseDecision{}, errors.New("unexpected check abuse script response")
	}
	strikes, ttl := vals[0], vals[1]
	limit := int64(m.cfg.AbuseStrikeLimit)
	locked := strikes >= limit && ttl > 0
	retry := int64(0)
	if locked {
		retry = ttl
	}
	return AbuseDecision{
		Locked:        locked,
		Strikes:       strikes,
		Limit:         limit,
		RetryAfterSec: retry,
	}, nil
}

// RecordAbuseStrike atomically increments the subject's abuse strike counter and activates lockout at limit.
func (m *RedisManager) RecordAbuseStrike(ctx context.Context, subject string) (AbuseDecision, error) {
	if m.rdb == nil {
		return AbuseDecision{}, errors.New("redis client is nil")
	}
	windowSec := int64(math.Ceil(m.cfg.AbuseLockoutWindow.Seconds()))
	if windowSec < 1 {
		windowSec = 1
	}
	vals, err := recordAbuseScript.Run(ctx, m.rdb, []string{m.abuseKey(subject)}, m.cfg.AbuseStrikeLimit, windowSec).Int64Slice()
	if err != nil {
		return AbuseDecision{}, fmt.Errorf("record abuse strike lua: %w", err)
	}
	if len(vals) < 2 {
		return AbuseDecision{}, errors.New("unexpected record abuse script response")
	}
	strikes, ttl := vals[0], vals[1]
	limit := int64(m.cfg.AbuseStrikeLimit)
	locked := strikes >= limit
	retry := int64(0)
	if locked {
		retry = ttl
	}
	return AbuseDecision{
		Locked:        locked,
		Strikes:       strikes,
		Limit:         limit,
		RetryAfterSec: retry,
	}, nil
}

// GetCache retrieves a cached payload under the assistant:cache: namespace.
func (m *RedisManager) GetCache(ctx context.Context, key string) ([]byte, bool, error) {
	if m.rdb == nil {
		return nil, false, errors.New("redis client is nil")
	}
	val, err := m.rdb.Get(ctx, cacheKeyPrefix+key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("redis cache get: %w", err)
	}
	return val, true, nil
}

// SetCache stores a cached payload under the assistant:cache: namespace with TTL.
func (m *RedisManager) SetCache(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if m.rdb == nil {
		return errors.New("redis client is nil")
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if err := m.rdb.Set(ctx, cacheKeyPrefix+key, val, ttl).Err(); err != nil {
		return fmt.Errorf("redis cache set: %w", err)
	}
	return nil
}

// Ping verifies Redis connectivity.
func (m *RedisManager) Ping(ctx context.Context) error {
	if m.rdb == nil {
		return errors.New("redis client is nil")
	}
	return m.rdb.Ping(ctx).Err()
}

func buildCostDecision(used, limit, retryAfterSec int64) CostDecision {
	allowed := used < limit
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	retry := int64(0)
	if !allowed {
		retry = retryAfterSec
	}
	return CostDecision{
		Allowed:       allowed,
		UsedCostVND:   used,
		LimitCostVND:  limit,
		RemainingVND:  remaining,
		RetryAfterSec: retry,
	}
}
