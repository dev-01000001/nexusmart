package quota_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nexusmart/core/services/assistant/internal/quota"
	"github.com/redis/go-redis/v9"
)

func runQuotaContractSuite(t *testing.T, makeMgr func(t *testing.T, cfg quota.Config) (quota.Manager, func(d time.Duration))) {
	t.Helper()

	baseTime := time.Date(2026, 11, 21, 14, 0, 0, 0, time.FixedZone("Asia/Ho_Chi_Minh", 7*3600))
	now := baseTime
	cfg := quota.Config{
		DailyUserTokenLimit: 1000,
		DailyCostLimitVND:   5000,
		AbuseStrikeLimit:    3,
		AbuseLockoutWindow:  10 * time.Minute,
		Now:                 func() time.Time { return now },
	}

	mgr, advance := makeMgr(t, cfg)
	ctx := context.Background()

	if err := mgr.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	// 1. Token quota check, reservation, reconciliation, and exhaustion.
	dec, err := mgr.CheckAndReserveTokens(ctx, "user-1", 600)
	if err != nil || !dec.Allowed || dec.UsedTokens != 600 || dec.Remaining != 400 {
		t.Fatalf("first reserve unexpected: dec=%+v err=%v", dec, err)
	}
	// Overestimate adjustment (-100) brings usage to 500.
	if err := mgr.RecordActualTokens(ctx, "user-1", -100); err != nil {
		t.Fatalf("RecordActualTokens: %v", err)
	}
	dec, err = mgr.CheckAndReserveTokens(ctx, "user-1", 500)
	if err != nil || !dec.Allowed || dec.UsedTokens != 1000 || dec.Remaining != 0 {
		t.Fatalf("second reserve unexpected: dec=%+v err=%v", dec, err)
	}
	// Exceeding limit returns Allowed=false + RetryAfterSec > 0.
	dec, err = mgr.CheckAndReserveTokens(ctx, "user-1", 10)
	if err != nil || dec.Allowed || dec.RetryAfterSec <= 0 {
		t.Fatalf("expected quota exhaustion, got dec=%+v err=%v", dec, err)
	}

	// 2. Global daily cost cap.
	costDec, err := mgr.CheckDailyCost(ctx)
	if err != nil || !costDec.Allowed || costDec.RemainingVND != 5000 {
		t.Fatalf("initial cost check unexpected: %+v err=%v", costDec, err)
	}
	costDec, err = mgr.RecordCostVND(ctx, 3000)
	if err != nil || !costDec.Allowed || costDec.UsedCostVND != 3000 || costDec.RemainingVND != 2000 {
		t.Fatalf("first cost record unexpected: %+v err=%v", costDec, err)
	}
	costDec, err = mgr.RecordCostVND(ctx, 2500)
	if err != nil || costDec.Allowed || costDec.UsedCostVND != 5500 || costDec.RemainingVND != 0 || costDec.RetryAfterSec <= 0 {
		t.Fatalf("exceeded cost cap unexpected: %+v err=%v", costDec, err)
	}
	costDec, err = mgr.CheckDailyCost(ctx)
	if err != nil || costDec.Allowed {
		t.Fatalf("subsequent CheckDailyCost should be denied, got %+v err=%v", costDec, err)
	}

	// 3. Cache miss, set, hit, and expiration.
	if _, hit, err := mgr.GetCache(ctx, "q1"); err != nil || hit {
		t.Fatalf("expected cache miss, got hit=%v err=%v", hit, err)
	}
	if err := mgr.SetCache(ctx, "q1", []byte("cached-answer"), 2*time.Minute); err != nil {
		t.Fatalf("SetCache: %v", err)
	}
	val, hit, err := mgr.GetCache(ctx, "q1")
	if err != nil || !hit || string(val) != "cached-answer" {
		t.Fatalf("expected cache hit, got val=%q hit=%v err=%v", string(val), hit, err)
	}

	// 4. Abuse strike tracking and lockout on strike 3.
	ab, err := mgr.CheckAbuse(ctx, "attacker-1")
	if err != nil || ab.Locked || ab.Strikes != 0 {
		t.Fatalf("initial CheckAbuse unexpected: %+v err=%v", ab, err)
	}
	for i := int64(1); i <= 2; i++ {
		ab, err = mgr.RecordAbuseStrike(ctx, "attacker-1")
		if err != nil || ab.Locked || ab.Strikes != i {
			t.Fatalf("strike %d unexpected: %+v err=%v", i, ab, err)
		}
	}
	ab, err = mgr.RecordAbuseStrike(ctx, "attacker-1")
	if err != nil || !ab.Locked || ab.Strikes != 3 || ab.RetryAfterSec <= 0 {
		t.Fatalf("strike 3 should lock out, got %+v err=%v", ab, err)
	}
	ab, err = mgr.CheckAbuse(ctx, "attacker-1")
	if err != nil || !ab.Locked || ab.RetryAfterSec <= 0 {
		t.Fatalf("CheckAbuse while locked unexpected: %+v err=%v", ab, err)
	}

	// Advance past cache TTL & abuse lockout window.
	now = now.Add(11 * time.Minute)
	advance(11 * time.Minute)

	if _, hit, err := mgr.GetCache(ctx, "q1"); err != nil || hit {
		t.Fatalf("expected expired cache miss, got hit=%v err=%v", hit, err)
	}
	ab, err = mgr.CheckAbuse(ctx, "attacker-1")
	if err != nil || ab.Locked {
		t.Fatalf("expected abuse lockout expired, got %+v err=%v", ab, err)
	}

	// Advance past Vietnam midnight -> daily quotas reset cleanly.
	now = now.Add(12 * time.Hour)
	advance(12 * time.Hour)

	dec, err = mgr.CheckAndReserveTokens(ctx, "user-1", 100)
	if err != nil || !dec.Allowed || dec.UsedTokens != 100 {
		t.Fatalf("expected next-day token quota reset, got %+v err=%v", dec, err)
	}
	costDec, err = mgr.CheckDailyCost(ctx)
	if err != nil || !costDec.Allowed || costDec.UsedCostVND != 0 {
		t.Fatalf("expected next-day cost quota reset, got %+v err=%v", costDec, err)
	}
}

func TestRedisQuotaManager(t *testing.T) {
	t.Parallel()

	runQuotaContractSuite(t, func(t *testing.T, cfg quota.Config) (quota.Manager, func(time.Duration)) {
		mr := miniredis.RunT(t)
		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		return quota.NewRedisManager(rdb, cfg), mr.FastForward
	})
}

func TestMemoryQuotaManager(t *testing.T) {
	t.Parallel()

	runQuotaContractSuite(t, func(_ *testing.T, cfg quota.Config) (quota.Manager, func(time.Duration)) {
		return quota.NewMemoryManager(cfg), func(time.Duration) {}
	})
}

func TestRedisCacheKeyPrefix(t *testing.T) {
	t.Parallel()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()

	mgr := quota.NewRedisManager(rdb, quota.Config{})
	if err := mgr.SetCache(context.Background(), "hello", []byte("world"), time.Minute); err != nil {
		t.Fatalf("SetCache: %v", err)
	}
	if !mr.Exists("assistant:cache:hello") {
		t.Errorf("expected Redis key with prefix assistant:cache:hello")
	}
}
