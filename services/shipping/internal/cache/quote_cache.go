package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/nexusmart/core/services/shipping/internal/carrier"
	"github.com/redis/go-redis/v9"
)

// QuoteCache caches shipping fee quotes by route, weight, and COD amount.
type QuoteCache interface {
	Get(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, bool)
	Set(ctx context.Context, req carrier.FeeRequest, quotes []carrier.Quote)
}

// RedisQuoteCache implements QuoteCache backed by Redis with a short TTL.
type RedisQuoteCache struct {
	client *redis.Client
	ttl    time.Duration
	logger *slog.Logger
}

// NewRedisQuoteCache creates a Redis-backed shipping quote cache.
func NewRedisQuoteCache(client *redis.Client, ttl time.Duration, logger *slog.Logger) *RedisQuoteCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisQuoteCache{
		client: client,
		ttl:    ttl,
		logger: logger,
	}
}

func quoteCacheKey(req carrier.FeeRequest) string {
	fromProv := strings.TrimSpace(req.FromProvinceCode)
	toProv := strings.TrimSpace(req.ToProvinceCode)
	if fromProv == "" || toProv == "" || req.WeightGrams <= 0 {
		return ""
	}
	// Bucket weight in 100g increments to maximize cache hit ratio for identical routes.
	weightBucket := ((req.WeightGrams + 99) / 100) * 100
	codAmount := req.CODAmountVND
	if codAmount < 0 {
		codAmount = 0
	}
	carriersPart := "all"
	if len(req.AllowedCarriers) > 0 {
		sorted := make([]string, 0, len(req.AllowedCarriers))
		for _, c := range req.AllowedCarriers {
			if norm := strings.ToLower(strings.TrimSpace(c)); norm != "" {
				sorted = append(sorted, norm)
			}
		}
		if len(sorted) > 0 {
			sort.Strings(sorted)
			carriersPart = strings.Join(sorted, ",")
		}
	}
	return fmt.Sprintf("shipping:quotes:v2:%s:%s:%s:%s:%s:%d:%d",
		carriersPart,
		fromProv,
		strings.TrimSpace(req.FromWardCode),
		toProv,
		strings.TrimSpace(req.ToWardCode),
		weightBucket,
		codAmount,
	)
}

// Get retrieves cached quotes when available.
func (c *RedisQuoteCache) Get(ctx context.Context, req carrier.FeeRequest) ([]carrier.Quote, bool) {
	if c == nil || c.client == nil {
		return nil, false
	}
	key := quoteCacheKey(req)
	if key == "" {
		return nil, false
	}
	raw, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			c.logger.WarnContext(ctx, "redis quote cache get failed", slog.String("error", err.Error()))
		}
		return nil, false
	}
	var quotes []carrier.Quote
	if err := json.Unmarshal(raw, &quotes); err != nil {
		c.logger.WarnContext(ctx, "corrupt quote cache entry", slog.String("error", err.Error()))
		return nil, false
	}
	if len(quotes) == 0 {
		return nil, false
	}
	return quotes, true
}

// Set stores quotes in Redis for the configured short TTL.
func (c *RedisQuoteCache) Set(ctx context.Context, req carrier.FeeRequest, quotes []carrier.Quote) {
	if c == nil || c.client == nil || len(quotes) == 0 {
		return
	}
	key := quoteCacheKey(req)
	if key == "" {
		return
	}
	raw, err := json.Marshal(quotes)
	if err != nil {
		return
	}
	if err := c.client.Set(ctx, key, raw, c.ttl).Err(); err != nil {
		c.logger.WarnContext(ctx, "redis quote cache set failed", slog.String("error", err.Error()))
	}
}
