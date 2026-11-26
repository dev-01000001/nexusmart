package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	// RevokedJTIKeyPrefix is the Redis key prefix for individually revoked access-token JTIs (logout).
	RevokedJTIKeyPrefix = "user:revoked_jti:"
	// RevokedBeforeKeyPrefix is the Redis key prefix storing the Unix timestamp (seconds)
	// before or at which all access tokens for a user are revoked (password reset/change, lock, role change, shop suspension).
	RevokedBeforeKeyPrefix = "user:revoked_before:"
)

// TokenRevocationChecker checks whether a verified end-user JWT has been revoked either
// individually (by JTI) or globally for its subject (user:revoked_before:<sub>).
type TokenRevocationChecker interface {
	IsTokenRevoked(ctx context.Context, claims *Claims) (bool, error)
}

// MGetter is the minimal multi-key lookup interface required by MGetRevocationChecker.
// Keeping this narrow avoids coupling pkg/common to a specific Redis client library.
type MGetter interface {
	MGetValues(ctx context.Context, keys ...string) ([]any, error)
}

// MGetFunc adapts a function to MGetter.
type MGetFunc func(ctx context.Context, keys ...string) ([]any, error)

// MGetValues calls f(ctx, keys...).
func (f MGetFunc) MGetValues(ctx context.Context, keys ...string) ([]any, error) {
	return f(ctx, keys...)
}

// MGetRevocationChecker checks `user:revoked_jti:<jti>` and `user:revoked_before:<sub>`
// in a single MGET round-trip, failing closed on missing/unparseable `iat` or store errors.
type MGetRevocationChecker struct {
	store MGetter
}

// NewMGetRevocationChecker constructs a TokenRevocationChecker backed by store.
func NewMGetRevocationChecker(store MGetter) *MGetRevocationChecker {
	return &MGetRevocationChecker{store: store}
}

// IsTokenRevoked returns true when claims' JTI is revoked or its IssuedAt <= user:revoked_before:<sub>.
// Fails closed if the store returns an error, if store is nil, or if revoked_before is set while
// claims.IssuedAt is missing or the stored timestamp is corrupt.
func (c *MGetRevocationChecker) IsTokenRevoked(ctx context.Context, claims *Claims) (bool, error) {
	if claims == nil || claims.Service != "" {
		return false, nil
	}
	if c == nil || c.store == nil {
		return false, errors.New("revocation store unavailable")
	}

	jti := strings.TrimSpace(claims.JTI())
	userID := strings.TrimSpace(claims.UserID())

	keys := make([]string, 0, 2)
	jtiIdx, beforeIdx := -1, -1
	if jti != "" {
		jtiIdx = len(keys)
		keys = append(keys, RevokedJTIKeyPrefix+jti)
	}
	if userID != "" {
		beforeIdx = len(keys)
		keys = append(keys, RevokedBeforeKeyPrefix+userID)
	}
	if len(keys) == 0 {
		return false, nil
	}

	vals, err := c.store.MGetValues(ctx, keys...)
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
