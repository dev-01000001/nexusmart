package ticket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/redis/go-redis/v9"
)

func TestTicketCreateRedeemSingleUseAndRevocation(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	const secret = "test-chat-secret"
	mgr, err := NewManager(rdb, secret, 60*time.Second)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	verifier, _ := auth.NewManager(secret, auth.DefaultAccessTTL)

	ctx := context.Background()
	claims := &auth.Claims{
		Role:   "seller",
		ShopID: "shop-42",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-42",
			ID:      "jti-42",
		},
	}

	raw, exp, err := mgr.Create(ctx, claims)
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if len(raw) < 40 || exp.Before(time.Now().UTC()) {
		t.Fatalf("unexpected ticket=%q exp=%v", raw, exp)
	}

	// Raw ticket itself must NEVER be stored as Redis key (only SHA-256 hash).
	if mr.Exists(ticketKeyPrefix + raw) {
		t.Fatal("raw ticket must not be used directly as Redis key")
	}

	// First redeem succeeds and returns verifiable ws_token.
	res, err := mgr.Redeem(ctx, raw)
	if err != nil {
		t.Fatalf("first redeem failed: %v", err)
	}
	if res.UserID != "user-42" || res.Role != "seller" || res.ShopID != "shop-42" || res.JTI != "jti-42" {
		t.Fatalf("unexpected redeemed payload: %+v", res)
	}
	wsClaims, err := verifier.VerifyAccessToken(res.WSToken)
	if err != nil || wsClaims.UserID() != "user-42" || wsClaims.ShopID != "shop-42" {
		t.Fatalf("ws_token verification failed: claims=%+v err=%v", wsClaims, err)
	}

	// Replay MUST fail atomically!
	if _, err := mgr.Redeem(ctx, raw); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("expected ErrInvalidTicket on replay, got %v", err)
	}

	// Expired ticket fails.
	raw2, _, err := mgr.Create(ctx, claims)
	if err != nil {
		t.Fatalf("create ticket 2: %v", err)
	}
	mr.FastForward(61 * time.Second)
	if _, err := mgr.Redeem(ctx, raw2); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("expected ErrInvalidTicket after TTL expiry, got %v", err)
	}

	// Revoked JTI fails on redeem.
	raw3, _, err := mgr.Create(ctx, claims)
	if err != nil {
		t.Fatalf("create ticket 3: %v", err)
	}
	_ = mr.Set(revokedJTIKeyPrefix+"jti-42", "1")
	if _, err := mgr.Redeem(ctx, raw3); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("expected ErrRevokedToken for revoked JTI, got %v", err)
	}
}

func TestTicketRevokedBeforeAndFailClosed(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	mgr, err := NewManager(rdb, "test-chat-revoked-before-secret", 60*time.Second)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	ctx := context.Background()

	revokedAt := int64(1700000100)
	_ = mr.Set(revokedBeforeKeyPrefix+"user-locked", "1700000100")

	// 1. Token issued at or before revoked_before MUST fail on Redeem and IsTokenRevoked.
	oldClaims := &auth.Claims{
		Role: "buyer",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  "user-locked",
			ID:       "jti-old",
			IssuedAt: jwt.NewNumericDate(time.Unix(revokedAt, 0).UTC()),
		},
	}
	oldTicket, _, err := mgr.Create(ctx, oldClaims)
	if err != nil {
		t.Fatalf("create old ticket: %v", err)
	}
	if _, err := mgr.Redeem(ctx, oldTicket); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("expected ErrRevokedToken for iat == revoked_before, got %v", err)
	}

	// 2. Token without iat MUST fail closed when revoked_before is set.
	noIATClaims := &auth.Claims{
		Role: "buyer",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-locked",
			ID:      "jti-no-iat",
		},
	}
	noIATTicket, _, err := mgr.Create(ctx, noIATClaims)
	if err != nil {
		t.Fatalf("create no-iat ticket: %v", err)
	}
	if _, err := mgr.Redeem(ctx, noIATTicket); !errors.Is(err, ErrRevokedToken) {
		t.Fatalf("expected fail-closed ErrRevokedToken for missing iat, got %v", err)
	}

	// 3. Fresh token issued strictly after revoked_before MUST succeed.
	freshClaims := &auth.Claims{
		Role: "buyer",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  "user-locked",
			ID:       "jti-fresh",
			IssuedAt: jwt.NewNumericDate(time.Unix(revokedAt+1, 0).UTC()),
		},
	}
	freshTicket, _, err := mgr.Create(ctx, freshClaims)
	if err != nil {
		t.Fatalf("create fresh ticket: %v", err)
	}
	if _, err := mgr.Redeem(ctx, freshTicket); err != nil {
		t.Fatalf("expected fresh token after revoked_before to redeem cleanly, got %v", err)
	}

	// 4. Corrupt revoked_before value fails closed.
	_ = mr.Set(revokedBeforeKeyPrefix+"user-locked", "not-a-timestamp")
	if revoked, err := mgr.IsTokenRevoked(ctx, freshClaims); err != nil || !revoked {
		t.Fatalf("expected corrupt revoked_before to fail closed, got revoked=%v err=%v", revoked, err)
	}
}
