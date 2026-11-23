package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/redis/go-redis/v9"
)

const slidingWindowScript = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
local count = redis.call('ZCARD', key)
if count >= limit then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  local retry_ms = window
  if #oldest >= 2 then
    retry_ms = math.max(1000, (tonumber(oldest[2]) + window) - now)
  end
  return {0, count, retry_ms}
end
redis.call('ZADD', key, now, member)
redis.call('PEXPIRE', key, window)
return {1, count + 1, 0}
`

// RateLimitResult captures the outcome of a rate limit check.
type RateLimitResult struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

// RateLimiter checks whether a key is within limit over window.
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (RateLimitResult, error)
}

// RedisRateLimiter implements atomic sliding-window rate limiting on Redis via Lua.
type RedisRateLimiter struct {
	client *redis.Client
	script *redis.Script
	nowFn  func() time.Time
}

// NewRedisRateLimiter creates a Redis sliding-window rate limiter.
func NewRedisRateLimiter(client *redis.Client) *RedisRateLimiter {
	return &RedisRateLimiter{
		client: client,
		script: redis.NewScript(slidingWindowScript),
		nowFn:  time.Now,
	}
}

func (l *RedisRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (RateLimitResult, error) {
	if limit <= 0 {
		return RateLimitResult{Allowed: true, Remaining: 0}, nil
	}
	if window <= 0 {
		window = time.Minute
	}
	nowMS := l.nowFn().UTC().UnixMilli()
	member := fmt.Sprintf("%d-%s", nowMS, uuid.NewString())

	raw, err := l.script.Run(ctx, l.client, []string{key}, nowMS, window.Milliseconds(), limit, member).Int64Slice()
	if err != nil {
		return RateLimitResult{}, fmt.Errorf("run rate limit lua: %w", err)
	}
	if len(raw) < 3 {
		return RateLimitResult{}, fmt.Errorf("unexpected rate limit lua response length: %d", len(raw))
	}

	allowed := raw[0] == 1
	count := int(raw[1])
	retryMS := raw[2]
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	return RateLimitResult{
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: time.Duration(retryMS) * time.Millisecond,
	}, nil
}

// RateLimitByIP enforces per-IP rate limiting (disabled when limitPerMin <= 0).
func RateLimitByIP(limiter RateLimiter, limitPerMin int, log *slog.Logger, tp *TrustedProxies) func(http.Handler) http.Handler {
	return rateLimitMiddleware(limiter, limitPerMin, time.Minute, log, func(r *http.Request) string {
		return "rl:ip:" + tp.ClientIP(r)
	})
}

// RateLimitByUser enforces per-authenticated-user rate limiting (disabled when limitPerMin <= 0).
func RateLimitByUser(limiter RateLimiter, limitPerMin int, log *slog.Logger) func(http.Handler) http.Handler {
	return rateLimitMiddleware(limiter, limitPerMin, time.Minute, log, func(r *http.Request) string {
		if uid := auth.UserIDFromContext(r.Context()); uid != "" {
			return "rl:user:" + uid
		}
		return ""
	})
}

// RateLimitFlashSale enforces stricter rate limits on flash-sale routes (disabled when limitPerMin <= 0).
func RateLimitFlashSale(limiter RateLimiter, limitPerMin int, log *slog.Logger, tp *TrustedProxies) func(http.Handler) http.Handler {
	return rateLimitMiddleware(limiter, limitPerMin, time.Minute, log, func(r *http.Request) string {
		if uid := auth.UserIDFromContext(r.Context()); uid != "" {
			return "rl:flash:user:" + uid
		}
		return "rl:flash:ip:" + tp.ClientIP(r)
	})
}

// RateLimitAuth enforces tighter per-IP rate limiting on sensitive auth endpoints (e.g. forgot/reset password).
func RateLimitAuth(limiter RateLimiter, limitPerMin int, log *slog.Logger, tp *TrustedProxies) func(http.Handler) http.Handler {
	return rateLimitMiddleware(limiter, limitPerMin, time.Minute, log, func(r *http.Request) string {
		return "rl:auth:ip:" + tp.ClientIP(r)
	})
}

// RateLimitWebhook enforces a dedicated per-IP rate limit on /webhooks/* routes.
func RateLimitWebhook(limiter RateLimiter, limitPerMin int, log *slog.Logger, tp *TrustedProxies) func(http.Handler) http.Handler {
	return rateLimitMiddleware(limiter, limitPerMin, time.Minute, log, func(r *http.Request) string {
		return "rl:webhook:ip:" + tp.ClientIP(r)
	})
}

func rateLimitMiddleware(
	limiter RateLimiter,
	limit int,
	window time.Duration,
	log *slog.Logger,
	keyFn func(*http.Request) string,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if limiter == nil || limit <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			res, err := limiter.Allow(r.Context(), key, limit, window)
			if err != nil {
				if log != nil {
					log.WarnContext(r.Context(), "rate limiter unavailable, failing open", slog.String("error", err.Error()))
				}
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))

			if !res.Allowed {
				retrySec := int(math.Ceil(res.RetryAfter.Seconds()))
				if retrySec < 1 {
					retrySec = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retrySec))
				httpx.WriteError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded, please retry later")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
