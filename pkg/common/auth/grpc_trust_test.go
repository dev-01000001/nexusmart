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
	restockServiceRPC  = "/nexusmart.product.v1.ProductService/RestockOrderItems"
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
	for _, m := range []string{internalServiceRPC, restockServiceRPC} {
		if _, err := callInterceptor(t, users, svc, m, spoof...); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anonymous internal RPC %s: want Unauthenticated, got %v", m, err)
		}
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
	for _, m := range []string{internalServiceRPC, restockServiceRPC} {
		claims, err := callInterceptor(t, users, svc, m, MDAuthorization, "Bearer "+good)
		if err != nil || claims == nil || claims.Service != "order-service" || claims.Role != ServiceRole {
			t.Fatalf("valid service token on allowed RPC %s: claims=%+v err=%v", m, claims, err)
		}
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

func TestGRPCInterceptorContractAllowlist(t *testing.T) {
	users, _ := NewManager("user-key", 15*time.Minute)
	svc, _ := NewManager("service-key", ServiceTokenTTL)

	svcTok, _ := svc.SignServiceToken("order-service")
	adminTok, _, _, _ := users.SignAccessToken("admin-1", "admin", "")
	buyerTok, _, _, _ := users.SignAccessToken("buyer-1", "buyer", "")
	spoof := []string{"x-user-id", "attacker", "x-user-role", "admin", "x-shop-id", "shop-b"}

	serviceAllowedRPCs := []string{
		"/nexusmart.promotion.v1.PromotionService/ReleasePromotionShop",
		"/nexusmart.user.v1.UserService/RecordCODRejection",
		"/nexusmart.user.v1.UserService/WriteAuditLog",
		"/nexusmart.promotion.v1.PromotionService/CreditCoins",
	}
	userOnlyInternalRPCs := []string{
		"/nexusmart.order.v1.OrderService/RetryPayment",
		"/nexusmart.user.v1.UserService/ValidateUser",
	}

	// None of these RPCs may run anonymously (even with spoofed headers).
	for _, m := range append(append([]string{}, serviceAllowedRPCs...), userOnlyInternalRPCs...) {
		if _, err := callInterceptor(t, users, svc, m); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("anonymous %s: want Unauthenticated, got %v", m, err)
		}
		if _, err := callInterceptor(t, users, svc, m, spoof...); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("spoofed anonymous %s: want Unauthenticated, got %v", m, err)
		}
	}

	// Service tokens are accepted on service-allowed RPCs and refused on user-only RPCs.
	for _, m := range serviceAllowedRPCs {
		claims, err := callInterceptor(t, users, svc, m, MDAuthorization, "Bearer "+svcTok)
		if err != nil || claims == nil || claims.Service != "order-service" || claims.Role != ServiceRole {
			t.Fatalf("service token on %s: claims=%+v err=%v", m, claims, err)
		}
	}
	for _, m := range userOnlyInternalRPCs {
		if _, err := callInterceptor(t, users, svc, m, MDAuthorization, "Bearer "+svcTok); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token on %s: want PermissionDenied, got %v", m, err)
		}
	}

	// Valid end-user JWTs pass through with verified claims (handlers enforce RBAC/ownership).
	for _, tc := range []struct {
		method string
		tok    string
		wantID string
	}{
		{"/nexusmart.order.v1.OrderService/RetryPayment", buyerTok, "buyer-1"},
		{"/nexusmart.user.v1.UserService/ValidateUser", buyerTok, "buyer-1"},
		{"/nexusmart.user.v1.UserService/WriteAuditLog", adminTok, "admin-1"},
		{"/nexusmart.promotion.v1.PromotionService/CreditCoins", adminTok, "admin-1"},
	} {
		claims, err := callInterceptor(t, users, svc, tc.method, MDAuthorization, "Bearer "+tc.tok)
		if err != nil || claims == nil || claims.UserID() != tc.wantID {
			t.Fatalf("user JWT on %s: want sub=%s, got claims=%+v err=%v", tc.method, tc.wantID, claims, err)
		}
	}
}
