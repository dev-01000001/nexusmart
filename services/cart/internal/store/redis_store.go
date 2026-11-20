package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	cartKeyPrefix = "cart:"
	defaultTTL    = 30 * 24 * time.Hour
)

// Item is the JSON payload stored per SKU field inside Redis hash cart:<user_id>.
type Item struct {
	Quantity      int32 `json:"quantity"`
	AddedPriceVND int64 `json:"added_price_vnd"`
	Selected      bool  `json:"selected"`
}

// Store defines cart persistence operations.
type Store interface {
	GetAll(ctx context.Context, userID string) (map[string]Item, error)
	Get(ctx context.Context, userID, skuID string) (Item, bool, error)
	Set(ctx context.Context, userID, skuID string, item Item) error
	Delete(ctx context.Context, userID string, skuIDs ...string) error
	ClearAll(ctx context.Context, userID string) error
	Ping(ctx context.Context) error
}

// RedisStore stores buyer carts in Redis hashes keyed by cart:<user_id>.
type RedisStore struct {
	client redis.UniversalClient
	ttl    time.Duration
}

// NewRedisStore creates a RedisStore backed by go-redis/v9.
func NewRedisStore(client redis.UniversalClient) *RedisStore {
	return &RedisStore{
		client: client,
		ttl:    defaultTTL,
	}
}

func cartKey(userID string) string {
	return cartKeyPrefix + userID
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) GetAll(ctx context.Context, userID string) (map[string]Item, error) {
	raw, err := s.client.HGetAll(ctx, cartKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("redis hgetall cart: %w", err)
	}
	items := make(map[string]Item, len(raw))
	for skuID, payload := range raw {
		var it Item
		if err := json.Unmarshal([]byte(payload), &it); err != nil {
			continue
		}
		if it.Quantity <= 0 {
			continue
		}
		items[skuID] = it
	}
	return items, nil
}

func (s *RedisStore) Get(ctx context.Context, userID, skuID string) (Item, bool, error) {
	raw, err := s.client.HGet(ctx, cartKey(userID), skuID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Item{}, false, nil
		}
		return Item{}, false, fmt.Errorf("redis hget cart item: %w", err)
	}
	var it Item
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		return Item{}, false, nil
	}
	if it.Quantity <= 0 {
		return Item{}, false, nil
	}
	return it, true, nil
}

func (s *RedisStore) Set(ctx context.Context, userID, skuID string, item Item) error {
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal cart item: %w", err)
	}
	key := cartKey(userID)
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, key, skuID, string(payload))
	pipe.Expire(ctx, key, s.ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis hset cart item: %w", err)
	}
	return nil
}

func (s *RedisStore) Delete(ctx context.Context, userID string, skuIDs ...string) error {
	if len(skuIDs) == 0 {
		return nil
	}
	if err := s.client.HDel(ctx, cartKey(userID), skuIDs...).Err(); err != nil {
		return fmt.Errorf("redis hdel cart items: %w", err)
	}
	return nil
}

func (s *RedisStore) ClearAll(ctx context.Context, userID string) error {
	if err := s.client.Del(ctx, cartKey(userID)).Err(); err != nil {
		return fmt.Errorf("redis del cart: %w", err)
	}
	return nil
}
