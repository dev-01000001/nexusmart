package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
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

// TrustedProxies holds CIDR ranges whose X-Forwarded-For / X-Real-IP headers are trusted.
// A nil or empty TrustedProxies trusts no proxy headers and always uses RemoteAddr.
type TrustedProxies struct {
	prefixes []netip.Prefix
}

// ParseTrustedProxies parses a comma-separated list of CIDRs (e.g. "172.31.250.0/28,10.0.0.0/8").
// Empty input returns an empty TrustedProxies that trusts no proxy headers.
func ParseTrustedProxies(raw string) (*TrustedProxies, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &TrustedProxies{}, nil
	}
	var prefixes []netip.Prefix
	for _, part := range strings.Split(raw, ",") {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(token)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q: %w", token, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return &TrustedProxies{prefixes: prefixes}, nil
}

// Contains reports whether addr falls within any configured trusted proxy CIDR.
func (tp *TrustedProxies) Contains(addr netip.Addr) bool {
	if tp == nil || len(tp.prefixes) == 0 || !addr.IsValid() {
		return false
	}
	unmapped := addr.Unmap()
	for _, p := range tp.prefixes {
		if p.Contains(unmapped) {
			return true
		}
	}
	return false
}

// ClientIP extracts the caller IP using only RemoteAddr unless RemoteAddr belongs to tp.
// When RemoteAddr is trusted, X-Forwarded-For is parsed right-to-left to pick the right-most
// untrusted IP (preventing spoofed left-most XFF entries); X-Real-IP is used only as fallback.
func (tp *TrustedProxies) ClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	remote := strings.TrimSpace(r.RemoteAddr)
	peerAddr, peerOK := parseIPAddr(remote)
	if !peerOK {
		host, _, err := net.SplitHostPort(remote)
		if err == nil && host != "" {
			return host
		}
		if remote != "" {
			return remote
		}
		return "unknown"
	}
	if !tp.Contains(peerAddr) {
		return peerAddr.String()
	}

	var hops []string
	for _, hdr := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(hdr, ",") {
			if s := strings.TrimSpace(part); s != "" {
				hops = append(hops, s)
			}
		}
	}
	if len(hops) > 0 {
		var lastValid netip.Addr
		for i := len(hops) - 1; i >= 0; i-- {
			hopAddr, ok := parseIPAddr(hops[i])
			if !ok {
				break
			}
			lastValid = hopAddr
			if !tp.Contains(hopAddr) {
				return hopAddr.String()
			}
		}
		if lastValid.IsValid() {
			return lastValid.String()
		}
	}

	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if realAddr, ok := parseIPAddr(xri); ok {
			return realAddr.String()
		}
	}
	return peerAddr.String()
}

func parseIPAddr(raw string) (netip.Addr, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(s); err == nil && host != "" {
		s = host
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// RateLimitByIP enforces per-IP rate limiting (disabled when limitPerMin <= 0).
func RateLimitByIP(limiter RateLimiter, limitPerMin int, trusted *TrustedProxies, log *slog.Logger) func(http.Handler) http.Handler {
	return rateLimitMiddleware(limiter, limitPerMin, time.Minute, log, func(r *http.Request) string {
		return "rl:ip:" + trusted.ClientIP(r)
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
func RateLimitFlashSale(limiter RateLimiter, limitPerMin int, trusted *TrustedProxies, log *slog.Logger) func(http.Handler) http.Handler {
	return rateLimitMiddleware(limiter, limitPerMin, time.Minute, log, func(r *http.Request) string {
		if uid := auth.UserIDFromContext(r.Context()); uid != "" {
			return "rl:flash:user:" + uid
		}
		return "rl:flash:ip:" + trusted.ClientIP(r)
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

// ClientIP extracts the caller IP strictly from RemoteAddr without trusting proxy headers.
// To honor proxy headers from configured CIDRs, use (*TrustedProxies).ClientIP.
func ClientIP(r *http.Request) string {
	return (*TrustedProxies)(nil).ClientIP(r)
}
