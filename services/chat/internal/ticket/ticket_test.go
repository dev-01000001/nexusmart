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
