package quota

import (
	"context"
	"time"
)

// TokenDecision describes whether a user/subject is within their daily token quota.
type TokenDecision struct {
	Allowed       bool
	UsedTokens    int64
	LimitTokens   int64
	Remaining     int64
	RetryAfterSec int64
}

// CostDecision describes whether the system is within its daily global LLM budget.
type CostDecision struct {
	Allowed       bool
	UsedCostVND   int64
	LimitCostVND  int64
	RemainingVND  int64
	RetryAfterSec int64
}

// AbuseDecision describes whether a subject is locked out due to repeated prompt-injection attacks.
type AbuseDecision struct {
	Locked        bool
	Strikes       int64
	Limit         int64
	RetryAfterSec int64
}

// Manager enforces per-user daily token quotas, global daily cost caps, response caching,
// and repeated-attack abuse lockouts (backed by Redis in production, or in-memory fallback).
type Manager interface {
	CheckAndReserveTokens(ctx context.Context, subject string, estimatedTokens int64) (TokenDecision, error)
	RecordActualTokens(ctx context.Context, subject string, deltaTokens int64) error
	CheckDailyCost(ctx context.Context) (CostDecision, error)
	RecordCostVND(ctx context.Context, costVND int64) (CostDecision, error)
	CheckAbuse(ctx context.Context, subject string) (AbuseDecision, error)
	RecordAbuseStrike(ctx context.Context, subject string) (AbuseDecision, error)

	GetCache(ctx context.Context, key string) ([]byte, bool, error)
	SetCache(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Ping(ctx context.Context) error
}
