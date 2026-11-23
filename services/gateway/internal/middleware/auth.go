package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/redis/go-redis/v9"
)

// TokenRevocationChecker checks whether an access token has been revoked either individually
// (by JTI, e.g. on logout) or globally for its subject (user:revoked_before:<sub>).
type TokenRevocationChecker interface {
	IsTokenRevoked(ctx context.Context, claims *auth.Claims) (bool, error)
}

// RedisRevocationChecker checks `user:revoked_jti:<jti>` and `user:revoked_before:<sub>`
// in Redis using a single MGET round-trip.
type RedisRevocationChecker struct {
	client *redis.Client
}

// NewRedisRevocationChecker creates a Redis-backed token revocation checker.
func NewRedisRevocationChecker(client *redis.Client) *RedisRevocationChecker {
	return &RedisRevocationChecker{client: client}
}

func (c *RedisRevocationChecker) IsTokenRevoked(ctx context.Context, claims *auth.Claims) (bool, error) {
	if claims == nil {
		return false, nil
	}
	jti := strings.TrimSpace(claims.JTI())
	userID := strings.TrimSpace(claims.UserID())

	keys := make([]string, 0, 2)
	jtiIdx, beforeIdx := -1, -1
	if jti != "" {
		jtiIdx = len(keys)
		keys = append(keys, "user:revoked_jti:"+jti)
	}
	if userID != "" {
		beforeIdx = len(keys)
		keys = append(keys, "user:revoked_before:"+userID)
	}
	if len(keys) == 0 {
		return false, nil
	}

	vals, err := c.client.MGet(ctx, keys...).Result()
	if err != nil {
		return false, fmt.Errorf("check token revocation: %w", err)
	}

	if jtiIdx >= 0 && jtiIdx < len(vals) && vals[jtiIdx] != nil {
		return true, nil
	}
	if beforeIdx >= 0 && beforeIdx < len(vals) && vals[beforeIdx] != nil {
		if claims.IssuedAt == nil || claims.IssuedAt.Time.IsZero() {
			return true, nil
		}
		raw := strings.TrimSpace(fmt.Sprint(vals[beforeIdx]))
		revokedBefore, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			// Fail closed on corrupt revocation timestamp value.
			return true, nil
		}
		if claims.IssuedAt.Unix() <= revokedBefore {
			return true, nil
		}
	}
	return false, nil
}

// Auth validates Bearer JWT tokens (including Redis JTI & user revoked_before checks when revoker is set)
// and writes standard NexusMart error envelopes on 401.
// When required is false, requests without Authorization pass through unauthenticated,
// while invalid or revoked Authorization tokens still return 401 (and Redis errors fail closed with 503).
func Auth(mgr *auth.Manager, revoker TokenRevocationChecker, log *slog.Logger, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if authHeader == "" {
				if required {
					httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "missing authorization header")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			claims, err := mgr.VerifyAccessToken(authHeader)
			if err != nil {
				httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid or expired access token")
				return
			}

			if revoker != nil {
				revoked, rErr := revoker.IsTokenRevoked(r.Context(), claims)
				if rErr != nil {
					if log != nil {
						log.ErrorContext(r.Context(), "token revocation check failed", slog.String("error", rErr.Error()))
					}
					httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "authentication service unavailable")
					return
				}
				if revoked {
					httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "access token has been revoked")
					return
				}
			}

			setRequestLogUserID(r.Context(), claims.UserID())
			ctx := auth.WithClaims(r.Context(), claims)
			ctx = auth.WithRawToken(ctx, authHeader)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireSellerShop ensures an authenticated seller has an active shop_id claim.
func RequireSellerShop() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.TrimSpace(auth.ShopIDFromContext(r.Context())) == "" {
				httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "seller account has no active shop")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRoles enforces route-level RBAC after Auth middleware.
func RequireRoles(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := auth.ClaimsFromContext(r.Context())
			if !ok {
				httpx.WriteError(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
				return
			}
			if !auth.HasRole(claims, allowed...) {
				httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "insufficient permissions for this route")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
