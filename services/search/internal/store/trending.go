package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nexusmart/core/pkg/common/textnorm"
	"github.com/redis/go-redis/v9"
)

const (
	// TrendingKeyPrefix is the Redis per-day sorted set prefix search:trending:<YYYYMMDD> (S5).
	TrendingKeyPrefix = "search:trending:"
	trendingBucketTTL = 8 * 24 * time.Hour
	trendingWindowDay = 7
	trendingMaxItems  = 500
	minTrendingScore  = 3.0
)

// TrendingStore tracks and reads rolling 7-day trending search terms in Redis using daily buckets (S5).
type TrendingStore struct {
	rdb *redis.Client
	now func() time.Time
}

// NewTrendingStore builds a TrendingStore backed by Redis (nil-safe).
func NewTrendingStore(rdb *redis.Client) *TrendingStore {
	return &TrendingStore{
		rdb: rdb,
		now: time.Now,
	}
}

// SetClock overrides the clock used for daily bucket selection (useful for tests).
func (t *TrendingStore) SetClock(fn func() time.Time) {
	if t != nil && fn != nil {
		t.now = fn
	}
}

func (t *TrendingStore) dayKey(shopID string, ts time.Time) string {
	day := ts.UTC().Format("20060102")
	shopID = strings.TrimSpace(shopID)
	if shopID != "" {
		return TrendingKeyPrefix + shopID + ":" + day
	}
	return TrendingKeyPrefix + day
}

// Record increments the score of a validated normalized query in today's global daily Redis key
// search:trending:<YYYYMMDD> with an 8-day TTL (S5).
func (t *TrendingStore) Record(ctx context.Context, normalizedQuery string) error {
	return t.RecordForShop(ctx, "", normalizedQuery)
}

// RecordForShop increments the score of a validated normalized query in today's daily Redis key
// search:trending:<shop_id>:<YYYYMMDD> (or search:trending:<YYYYMMDD> when shopID is empty) with an 8-day TTL (S5).
func (t *TrendingStore) RecordForShop(ctx context.Context, shopID, normalizedQuery string) error {
	if t == nil || t.rdb == nil {
		return nil
	}
	clean := collapseNormalizedQuery(normalizedQuery)
	if utf8.RuneCountInString(clean) < 2 || utf8.RuneCountInString(clean) > 80 {
		return nil
	}

	key := t.dayKey(shopID, t.now())
	pipe := t.rdb.Pipeline()
	pipe.ZIncrBy(ctx, key, 1, clean)
	pipe.ExpireNX(ctx, key, trendingBucketTTL)
	pipe.ZRemRangeByRank(ctx, key, 0, -(trendingMaxItems + 1))
	_, err := pipe.Exec(ctx)
	return err
}

// Top computes the 7-day rolling ZUNION across global search:trending:<YYYYMMDD> daily buckets
// and returns up to limit terms with aggregate score >= 3 matching queryFolded (S5).
func (t *TrendingStore) Top(ctx context.Context, queryFolded string, limit int) ([]string, error) {
	return t.TopForShop(ctx, "", queryFolded, limit)
}

// TopForShop computes the 7-day rolling ZUNION across per-shop search:trending:<shop_id>:<YYYYMMDD>
// (or global search:trending:<YYYYMMDD> when shopID is empty) daily buckets and returns up to limit
// terms with aggregate score >= 3 matching queryFolded (S5).
func (t *TrendingStore) TopForShop(ctx context.Context, shopID, queryFolded string, limit int) ([]string, error) {
	if t == nil || t.rdb == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}

	base := t.now().UTC()
	keys := make([]string, trendingWindowDay)
	for i := 0; i < trendingWindowDay; i++ {
		keys[i] = t.dayKey(shopID, base.AddDate(0, 0, -i))
	}

	zs, err := t.rdb.ZUnionWithScores(ctx, redis.ZStore{
		Keys:      keys,
		Aggregate: "SUM",
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(zs) == 0 {
		return nil, nil
	}

	// ZUNION returns ascending scores; sort descending by score, then alphabetically for determinism.
	sort.Slice(zs, func(i, j int) bool {
		if zs[i].Score != zs[j].Score {
			return zs[i].Score > zs[j].Score
		}
		return fmt.Sprint(zs[i].Member) < fmt.Sprint(zs[j].Member)
	})

	out := make([]string, 0, limit)
	for _, z := range zs {
		if z.Score < minTrendingScore {
			break
		}
		member := fmt.Sprint(z.Member)
		if queryFolded != "" && !strings.Contains(textnorm.NormalizeSearchText(member), queryFolded) {
			continue
		}
		out = append(out, member)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func collapseNormalizedQuery(q string) string {
	fields := strings.Fields(textnorm.NormalizeSearchText(q))
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, " ")
}
