package quota

import (
	"context"
	"math"
	"sync"
	"time"
)

type abuseEntry struct {
	strikes   int64
	expiresAt time.Time
}

type cacheEntry struct {
	val       []byte
	expiresAt time.Time
}

// MemoryManager is a thread-safe in-memory implementation of quota.Manager
// with semantics matching RedisManager (for standalone mode, tests, and evals).
type MemoryManager struct {
	cfg Config

	mu     sync.Mutex
	tokens map[string]int64
	costs  map[string]int64
	abuse  map[string]abuseEntry
	cache  map[string]cacheEntry
}

// NewMemoryManager constructs a thread-safe in-memory quota.Manager.
func NewMemoryManager(cfg Config) *MemoryManager {
	return &MemoryManager{
		cfg:    normalizeConfig(cfg),
		tokens: make(map[string]int64),
		costs:  make(map[string]int64),
		abuse:  make(map[string]abuseEntry),
		cache:  make(map[string]cacheEntry),
	}
}

// CheckAndReserveTokens checks the subject's daily token quota and reserves estimatedTokens if allowed.
func (m *MemoryManager) CheckAndReserveTokens(ctx context.Context, subject string, estimatedTokens int64) (TokenDecision, error) {
	if err := ctx.Err(); err != nil {
		return TokenDecision{}, err
	}
	if estimatedTokens < 0 {
		estimatedTokens = 0
	}

	now := m.cfg.Now()
	dateStr, _, retryAfterSec := vietnamDayWindow(now)
	key := dateStr + ":" + normalizeSubject(subject)

	m.mu.Lock()
	defer m.mu.Unlock()

	cur := m.tokens[key]
	if cur+estimatedTokens > m.cfg.DailyUserTokenLimit {
		rem := m.cfg.DailyUserTokenLimit - cur
		if rem < 0 {
			rem = 0
		}
		return TokenDecision{
			Allowed:       false,
			UsedTokens:    cur,
			LimitTokens:   m.cfg.DailyUserTokenLimit,
			Remaining:     rem,
			RetryAfterSec: retryAfterSec,
		}, nil
	}

	updated := cur + estimatedTokens
	m.tokens[key] = updated
	rem := m.cfg.DailyUserTokenLimit - updated
	if rem < 0 {
		rem = 0
	}
	return TokenDecision{
		Allowed:       true,
		UsedTokens:    updated,
		LimitTokens:   m.cfg.DailyUserTokenLimit,
		Remaining:     rem,
		RetryAfterSec: 0,
	}, nil
}

// RecordActualTokens adjusts the subject's daily token usage by deltaTokens (clamped at >= 0).
func (m *MemoryManager) RecordActualTokens(ctx context.Context, subject string, deltaTokens int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dateStr, _, _ := vietnamDayWindow(m.cfg.Now())
	key := dateStr + ":" + normalizeSubject(subject)

	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.tokens[key] + deltaTokens
	if next < 0 {
		next = 0
	}
	m.tokens[key] = next
	return nil
}

// CheckDailyCost checks whether today's global LLM VND spend is below DailyCostLimitVND.
func (m *MemoryManager) CheckDailyCost(ctx context.Context) (CostDecision, error) {
	if err := ctx.Err(); err != nil {
		return CostDecision{}, err
	}
	dateStr, _, retryAfterSec := vietnamDayWindow(m.cfg.Now())

	m.mu.Lock()
	used := m.costs[dateStr]
	m.mu.Unlock()

	return buildCostDecision(used, m.cfg.DailyCostLimitVND, retryAfterSec), nil
}

// RecordCostVND adds costVND to today's global spend counter.
func (m *MemoryManager) RecordCostVND(ctx context.Context, costVND int64) (CostDecision, error) {
	if err := ctx.Err(); err != nil {
		return CostDecision{}, err
	}
	if costVND < 0 {
		costVND = 0
	}
	dateStr, _, retryAfterSec := vietnamDayWindow(m.cfg.Now())

	m.mu.Lock()
	updated := m.costs[dateStr] + costVND
	m.costs[dateStr] = updated
	m.mu.Unlock()

	return buildCostDecision(updated, m.cfg.DailyCostLimitVND, retryAfterSec), nil
}

// CheckAbuse reports whether subject is currently locked out from repeated abuse strikes.
func (m *MemoryManager) CheckAbuse(ctx context.Context, subject string) (AbuseDecision, error) {
	if err := ctx.Err(); err != nil {
		return AbuseDecision{}, err
	}
	now := m.cfg.Now()
	subj := normalizeSubject(subject)
	limit := int64(m.cfg.AbuseStrikeLimit)

	m.mu.Lock()
	entry, exists := m.abuse[subj]
	if exists && !now.Before(entry.expiresAt) {
		delete(m.abuse, subj)
		exists = false
	}
	m.mu.Unlock()

	if !exists {
		return AbuseDecision{Locked: false, Strikes: 0, Limit: limit, RetryAfterSec: 0}, nil
	}
	locked := entry.strikes >= limit
	retry := int64(0)
	if locked {
		retry = int64(math.Ceil(entry.expiresAt.Sub(now).Seconds()))
		if retry < 1 {
			retry = 1
		}
	}
	return AbuseDecision{
		Locked:        locked,
		Strikes:       entry.strikes,
		Limit:         limit,
		RetryAfterSec: retry,
	}, nil
}

// RecordAbuseStrike increments the subject's abuse strike counter and locks out when limit is reached.
func (m *MemoryManager) RecordAbuseStrike(ctx context.Context, subject string) (AbuseDecision, error) {
	if err := ctx.Err(); err != nil {
		return AbuseDecision{}, err
	}
	now := m.cfg.Now()
	subj := normalizeSubject(subject)
	limit := int64(m.cfg.AbuseStrikeLimit)

	m.mu.Lock()
	entry, exists := m.abuse[subj]
	if exists && !now.Before(entry.expiresAt) {
		entry = abuseEntry{}
	}
	entry.strikes++
	if entry.strikes == 1 || entry.strikes >= limit {
		entry.expiresAt = now.Add(m.cfg.AbuseLockoutWindow)
	}
	m.abuse[subj] = entry
	m.mu.Unlock()

	locked := entry.strikes >= limit
	retry := int64(0)
	if locked {
		retry = int64(math.Ceil(entry.expiresAt.Sub(now).Seconds()))
		if retry < 1 {
			retry = 1
		}
	}
	return AbuseDecision{
		Locked:        locked,
		Strikes:       entry.strikes,
		Limit:         limit,
		RetryAfterSec: retry,
	}, nil
}

// GetCache retrieves a cached payload if present and not expired.
func (m *MemoryManager) GetCache(ctx context.Context, key string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	now := m.cfg.Now()
	fullKey := cacheKeyPrefix + key

	m.mu.Lock()
	entry, ok := m.cache[fullKey]
	if ok && !now.Before(entry.expiresAt) {
		delete(m.cache, fullKey)
		ok = false
	}
	m.mu.Unlock()

	if !ok {
		return nil, false, nil
	}
	cp := make([]byte, len(entry.val))
	copy(cp, entry.val)
	return cp, true, nil
}

// SetCache stores a deep copy of val with TTL.
func (m *MemoryManager) SetCache(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	cp := make([]byte, len(val))
	copy(cp, val)

	m.mu.Lock()
	m.cache[cacheKeyPrefix+key] = cacheEntry{
		val:       cp,
		expiresAt: m.cfg.Now().Add(ttl),
	}
	m.mu.Unlock()
	return nil
}

// Ping verifies context health.
func (m *MemoryManager) Ping(ctx context.Context) error {
	return ctx.Err()
}
