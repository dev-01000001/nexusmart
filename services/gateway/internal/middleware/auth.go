package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/redis/go-redis/v9"
)

// JTIRevocationChecker checks whether an access token JTI has been revoked (e.g. on logout).
type JTIRevocationChecker interface {
	IsJTIRevoked(ctx context.Context, jti string) (bool, error)
}

// RedisRevocationChecker checks `user:revoked_jti:<jti>` in Redis.
type RedisRevocationChecker struct {
	client *redis.Client
}

// NewRedisRevocationChecker creates a Redis-backed JTI revocation checker.
func NewRedisRevocationChecker(client *redis.Client) *RedisRevocationChecker {
	return &RedisRevocationChecker{client: client}
}

func (c *RedisRevocationChecker) IsJTIRevoked(ctx context.Context, jti string) (bool, error) {
	jti = strings.TrimSpace(jti)
	if jti == "" {
		return false, nil
	}
	exists, err := c.client.Exists(ctx, "user:revoked_jti:"+jti).Result()
	if err != nil {
		return false, fmt.Errorf("check revoked jti: %w", err)
	}
	return exists > 0, nil
}

// Auth validates Bearer JWT tokens (including Redis JTI revocation checks when revoker is set)
// and writes standard NexusMart error envelopes on 401.
// When required is false, requests without Authorization pass through unauthenticated,
// while invalid or revoked Authorization tokens still return 401.
func Auth(mgr *auth.Manager, revoker JTIRevocationChecker, log *slog.Logger, required bool) func(http.Handler) http.Handler {
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

			if revoker != nil && claims.JTI() != "" {
				revoked, rErr := revoker.IsJTIRevoked(r.Context(), claims.JTI())
				if rErr != nil {
					if log != nil {
						log.ErrorContext(r.Context(), "jti revocation check failed", slog.String("error", rErr.Error()))
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
