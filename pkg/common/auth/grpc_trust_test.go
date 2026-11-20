package auth

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	internalServiceRPC = "/nexusmart.product.v1.ProductService/ReleaseStock"
	internalUserRPC    = "/nexusmart.product.v1.ProductService/ReserveStock"
	publicRPC          = "/nexusmart.product.v1.ProductService/CreateProduct"
)

// callInterceptor runs the interceptor with the given incoming metadata and returns the claims
// the handler observed (nil when anonymous) plus the interceptor error.
func callInterceptor(t *testing.T, users, svc *Manager, method string, kv ...string) (*Claims, error) {
	t.Helper()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(kv...))
	var seen *Claims
	_, err := GRPCUnaryServerInterceptor(users, svc)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, _ any) (any, error) {
			seen, _ = ClaimsFromContext(ctx)
			return "ok", nil
		})
	return seen, err
}

func TestGRPCInterceptorIgnoresSpoofedIdentityMetadata(t *testing.T) {
	users, _ := NewManager("user-key", 15*time.Minute)
	svc, _ := NewManager("service-key", ServiceTokenTTL)
	spoof := []string{"x-user-id", "attacker", "x-user-role", "admin", "x-shop-id", "shop-b"}

	// Public RPC: spoofed metadata must not produce any identity.
	claims, err := callInterceptor(t, users, svc, publicRPC, spoof...)
	if err != nil || claims != nil {
		t.Fatalf("spoofed metadata must stay anonymous, got claims=%+v err=%v", claims, err)
	}
	// Internal RPC: anonymous caller is rejected outright.
	if _, err := callInterceptor(t, users, svc, internalServiceRPC, spoof...); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("anonymous internal RPC: want Unauthenticated, got %v", err)
	}

	// Valid shop-A JWT + x-shop-id of shop B: metadata ignored, identity is shop A.
	tokA, _, _, _ := users.SignAccessToken("seller-a", "seller", "shop-a")
	claims, err = callInterceptor(t, users, svc, publicRPC, append(spoof, MDAuthorization, "Bearer "+tokA)...)
	if err != nil || claims == nil || claims.ShopID != "shop-a" || claims.Subject != "seller-a" || claims.Role != "seller" {
		t.Fatalf("want shop-a seller identity from JWT, got claims=%+v err=%v", claims, err)
	}
}

func TestGRPCInterceptorServiceTokens(t *testing.T) {
	users, _ := NewManager("user-key", 15*time.Minute)
	svc, _ := NewManager("service-key", ServiceTokenTTL)

	good, _ := svc.SignServiceToken("order-service")
	claims, err := callInterceptor(t, users, svc, internalServiceRPC, MDAuthorization, "Bearer "+good)
	if err != nil || claims == nil || claims.Service != "order-service" || claims.Role != ServiceRole {
		t.Fatalf("valid service token on allowed RPC: claims=%+v err=%v", claims, err)
	}
	// Service identity is refused on RPCs not marked for it.
	for _, m := range []string{internalUserRPC, publicRPC} {
		if _, err := callInterceptor(t, users, svc, m, MDAuthorization, "Bearer "+good); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token on %s: want PermissionDenied, got %v", m, err)
		}
	}

	wrongKey, _ := NewManager("not-the-service-key", ServiceTokenTTL)
	forged, _ := wrongKey.SignServiceToken("order-service")
	// Service-shaped token signed with the *user* key must not pass either path.
	userKeyForged, _ := users.SignServiceToken("order-service")
	expiredMgr, _ := NewManager("service-key", time.Millisecond)
	expired, _ := expiredMgr.SignServiceToken("order-service")
	time.Sleep(10 * time.Millisecond)
	for name, tok := range map[string]string{"wrong key": forged, "user key": userKeyForged, "expired": expired, "garbage": "a.b.c"} {
		if _, err := callInterceptor(t, users, svc, internalServiceRPC, MDAuthorization, "Bearer "+tok); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("%s service token: want Unauthenticated, got %v", name, err)
		}
	}

	// Verify* keep the two token kinds apart even if keys were (mis)configured equal.
	same, _ := NewManager("user-key", 15*time.Minute)
	userTok, _, _, _ := users.SignAccessToken("buyer-1", "buyer", "")
	if _, err := same.VerifyServiceToken(userTok); err == nil {
		t.Fatalf("user token must not verify as a service token")
	}
	if _, err := same.VerifyAccessToken(userKeyForged); err == nil {
		t.Fatalf("service token must not verify as a user token")
	}
}
