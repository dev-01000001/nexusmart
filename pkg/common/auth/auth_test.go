package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTSignVerifyAndContextHelpers(t *testing.T) {
	if _, err := NewManager("   ", 0); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("expected ErrEmptySecret, got %v", err)
	}

	mgr, err := NewManager("test-hs256-secret-key", 15*time.Minute)
	if err != nil {
		t.Fatalf("NewManager error: %v", err)
	}

	token, jti, exp, err := mgr.SignAccessToken("user-123", "seller", "shop-456")
	if err != nil {
		t.Fatalf("SignAccessToken error: %v", err)
	}
	if token == "" || jti == "" || time.Until(exp) < 14*time.Minute {
		t.Fatalf("unexpected token/jti/exp: jti=%q exp=%v", jti, exp)
	}

	claims, err := mgr.VerifyAccessToken("Bearer " + token)
	if err != nil {
		t.Fatalf("VerifyAccessToken error: %v", err)
	}
	if claims.UserID() != "user-123" || claims.Role != "seller" || claims.ShopID != "shop-456" || claims.JTI() != jti {
		t.Fatalf("unexpected claims: %#v", claims)
	}

	// Wrong secret must fail
	otherMgr, _ := NewManager("different-secret", 15*time.Minute)
	if _, err := otherMgr.VerifyAccessToken(token); err == nil {
		t.Fatalf("expected error for wrong signing key")
	}

	// Expired token must fail
	expiredMgr, _ := NewManager("test-hs256-secret-key", time.Millisecond)
	shortTok, _, _, err := expiredMgr.SignAccessToken("u1", "buyer", "")
	if err != nil {
		t.Fatalf("sign short token: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := mgr.VerifyAccessToken(shortTok); err == nil {
		t.Fatalf("expected expired token error")
	}

	// Token missing exp claim must be rejected
	noExpTok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Role: "admin",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-no-exp",
			ID:      "jti-no-exp",
		},
	}).SignedString([]byte("test-hs256-secret-key"))
	if err != nil {
		t.Fatalf("sign no-exp token: %v", err)
	}
	if _, err := mgr.VerifyAccessToken(noExpTok); err == nil {
		t.Fatalf("expected error for token missing exp claim")
	}

	ctx := WithClaims(context.Background(), claims)
	if UserIDFromContext(ctx) != "user-123" || RoleFromContext(ctx) != "seller" || ShopIDFromContext(ctx) != "shop-456" {
		t.Fatalf("context getters mismatch")
	}
	if !HasRole(claims, "admin", "seller") || HasRole(claims, "admin") {
		t.Fatalf("HasRole check failed")
	}
}

func TestGRPCMetadataPropagationAndInterceptor(t *testing.T) {
	mgr, _ := NewManager("grpc-secret", 15*time.Minute)
	token, _, _, err := mgr.SignAccessToken("user-9", "admin", "")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	outCtx := InjectOutgoingGRPC(context.Background(), token)
	outMD, ok := metadata.FromOutgoingContext(outCtx)
	if !ok {
		t.Fatalf("expected outgoing metadata")
	}
	inCtx := metadata.NewIncomingContext(context.Background(), outMD)

	interceptor := GRPCUnaryServerInterceptor(mgr, nil)
	var gotUID, gotRole string
	_, err = interceptor(inCtx, "req", &grpc.UnaryServerInfo{FullMethod: "/test.Svc/Call"}, func(ctx context.Context, _ any) (any, error) {
		gotUID = UserIDFromContext(ctx)
		gotRole = RoleFromContext(ctx)
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
	if gotUID != "user-9" || gotRole != "admin" {
		t.Fatalf("got uid=%q role=%q, want user-9/admin", gotUID, gotRole)
	}

	// Invalid bearer token must be rejected with Unauthenticated
	badMD := metadata.Pairs(MDAuthorization, "Bearer invalid.jwt.token")
	_, err = interceptor(metadata.NewIncomingContext(context.Background(), badMD), "req", &grpc.UnaryServerInfo{FullMethod: "/test.Svc/Call"}, func(context.Context, any) (any, error) {
		return "ok", nil
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestHTTPAuthAndRBACMiddleware(t *testing.T) {
	mgr, _ := NewManager("http-secret", 15*time.Minute)
	buyerTok, _, _, _ := mgr.SignAccessToken("buyer-1", "buyer", "")
	adminTok, _, _, _ := mgr.SignAccessToken("admin-1", "admin", "")

	h := HTTPMiddleware(mgr, true)(RequireRolesHTTP("admin")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(UserIDFromContext(r.Context())))
	})))

	// Missing header -> 401
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing header code=%d, want 401", rr.Code)
	}

	// Buyer role -> 403
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+buyerTok)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("buyer role code=%d, want 403", rr.Code)
	}

	// Admin role -> 200
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "admin-1" {
		t.Fatalf("admin role code=%d body=%q", rr.Code, rr.Body.String())
	}
}
