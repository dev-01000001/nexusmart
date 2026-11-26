package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestMGetRevocationChecker(t *testing.T) {
	cutoff := time.Unix(1700000100, 0).UTC()
	data := map[string]any{
		RevokedJTIKeyPrefix + "jti-revoked":     "1",
		RevokedBeforeKeyPrefix + "user-locked":  strconv.FormatInt(cutoff.Unix(), 10),
		RevokedBeforeKeyPrefix + "user-corrupt": "not-a-timestamp",
	}

	var lastKeys []string
	var injectErr error
	checker := NewMGetRevocationChecker(MGetFunc(func(_ context.Context, keys ...string) ([]any, error) {
		lastKeys = append([]string(nil), keys...)
		if injectErr != nil {
			return nil, injectErr
		}
		vals := make([]any, len(keys))
		for i, k := range keys {
			if v, ok := data[k]; ok {
				vals[i] = v
			}
		}
		return vals, nil
	}))

	mkClaims := func(uid, jti string, iat *time.Time) *Claims {
		rc := jwt.RegisteredClaims{Subject: uid, ID: jti}
		if iat != nil {
			rc.IssuedAt = jwt.NewNumericDate(*iat)
		}
		return &Claims{Role: "buyer", RegisteredClaims: rc}
	}

	tBefore := cutoff.Add(-5 * time.Second)
	tEqual := cutoff
	tAfter := cutoff.Add(5 * time.Second)

	cases := []struct {
		name        string
		claims      *Claims
		wantRevoked bool
		wantKeys    []string
	}{
		{
			name:        "clean user passes",
			claims:      mkClaims("user-clean", "jti-clean", &tBefore),
			wantRevoked: false,
			wantKeys:    []string{RevokedJTIKeyPrefix + "jti-clean", RevokedBeforeKeyPrefix + "user-clean"},
		},
		{
			name:        "revoked jti rejected",
			claims:      mkClaims("user-clean", "jti-revoked", &tAfter),
			wantRevoked: true,
			wantKeys:    []string{RevokedJTIKeyPrefix + "jti-revoked", RevokedBeforeKeyPrefix + "user-clean"},
		},
		{
			name:        "iat before revoked_before rejected",
			claims:      mkClaims("user-locked", "jti-ok", &tBefore),
			wantRevoked: true,
			wantKeys:    []string{RevokedJTIKeyPrefix + "jti-ok", RevokedBeforeKeyPrefix + "user-locked"},
		},
		{
			name:        "iat equal to revoked_before rejected",
			claims:      mkClaims("user-locked", "jti-ok", &tEqual),
			wantRevoked: true,
			wantKeys:    []string{RevokedJTIKeyPrefix + "jti-ok", RevokedBeforeKeyPrefix + "user-locked"},
		},
		{
			name:        "iat strictly after revoked_before allowed",
			claims:      mkClaims("user-locked", "jti-ok", &tAfter),
			wantRevoked: false,
			wantKeys:    []string{RevokedJTIKeyPrefix + "jti-ok", RevokedBeforeKeyPrefix + "user-locked"},
		},
		{
			name:        "missing iat fails closed when revoked_before set",
			claims:      mkClaims("user-locked", "jti-ok", nil),
			wantRevoked: true,
			wantKeys:    []string{RevokedJTIKeyPrefix + "jti-ok", RevokedBeforeKeyPrefix + "user-locked"},
		},
		{
			name:        "corrupt revoked_before fails closed",
			claims:      mkClaims("user-corrupt", "jti-ok", &tAfter),
			wantRevoked: true,
			wantKeys:    []string{RevokedJTIKeyPrefix + "jti-ok", RevokedBeforeKeyPrefix + "user-corrupt"},
		},
		{
			name:        "nil claims ignored",
			claims:      nil,
			wantRevoked: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lastKeys = nil
			revoked, err := checker.IsTokenRevoked(context.Background(), tc.claims)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if revoked != tc.wantRevoked {
				t.Fatalf("revoked = %v, want %v", revoked, tc.wantRevoked)
			}
			if len(tc.wantKeys) > 0 {
				if len(lastKeys) != len(tc.wantKeys) {
					t.Fatalf("keys = %v, want %v", lastKeys, tc.wantKeys)
				}
				for i := range tc.wantKeys {
					if lastKeys[i] != tc.wantKeys[i] {
						t.Fatalf("keys[%d] = %q, want %q", i, lastKeys[i], tc.wantKeys[i])
					}
				}
			}
		})
	}

	// Store error propagates so caller fails closed with 503.
	injectErr = errors.New("redis connection refused")
	if _, err := checker.IsTokenRevoked(context.Background(), mkClaims("user-clean", "jti-1", &tAfter)); err == nil {
		t.Fatalf("expected error on MGET failure")
	}

	// Nil store fails closed with error.
	nilChecker := NewMGetRevocationChecker(nil)
	if _, err := nilChecker.IsTokenRevoked(context.Background(), mkClaims("user-clean", "jti-1", &tAfter)); err == nil {
		t.Fatalf("expected error on nil store")
	}
}
