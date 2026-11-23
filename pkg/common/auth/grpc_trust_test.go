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
	publicRPC          = "/nexusmart.product.v1.ProductService/GetProduct"
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
	sellerTok, _, _, _ := users.SignAccessToken("seller-1", "seller", "shop-1")
	spoof := []string{"x-user-id", "attacker", "x-user-role", "admin", "x-shop-id", "shop-b"}

	serviceAllowedRPCs := []string{
		"/nexusmart.user.v1.UserService/GetAddress",
		"/nexusmart.user.v1.UserService/GetProfile",
		"/nexusmart.user.v1.UserService/RecordCODRejection",
		"/nexusmart.user.v1.UserService/WriteAuditLog",
		"/nexusmart.product.v1.ProductService/ReleaseStock",
		"/nexusmart.product.v1.ProductService/ConfirmStock",
		"/nexusmart.product.v1.ProductService/RestockOrderItems",
		"/nexusmart.promotion.v1.PromotionService/ReleasePromotion",
		"/nexusmart.promotion.v1.PromotionService/ReleasePromotionShop",
		"/nexusmart.promotion.v1.PromotionService/CreditCoins",
		"/nexusmart.order.v1.OrderService/MarkPaymentResult",
		"/nexusmart.shipping.v1.ShippingService/GetShipment",
		"/nexusmart.shipping.v1.ShippingService/CancelShipment",
		"/nexusmart.shipping.v1.ShippingService/CreateReturnShipment",
		"/nexusmart.shipping.v1.ShippingService/GetShopShippingSettings",
	}
	userOnlyInternalRPCs := []string{
		// UserService
		"/nexusmart.user.v1.UserService/Logout",
		"/nexusmart.user.v1.UserService/UpdateProfile",
		"/nexusmart.user.v1.UserService/ValidateUser",
		"/nexusmart.user.v1.UserService/AdminListUsers",
		"/nexusmart.user.v1.UserService/AdminUpdateUserStatus",
		"/nexusmart.user.v1.UserService/AdminUpdateUserRole",
		"/nexusmart.user.v1.UserService/RegisterShop",
		"/nexusmart.user.v1.UserService/AdminListShops",
		"/nexusmart.user.v1.UserService/AdminReviewShop",
		"/nexusmart.user.v1.UserService/CreateAddress",
		"/nexusmart.user.v1.UserService/ListAddresses",
		"/nexusmart.user.v1.UserService/UpdateAddress",
		"/nexusmart.user.v1.UserService/DeleteAddress",
		"/nexusmart.user.v1.UserService/ListAuditLogs",
		// ProductService
		"/nexusmart.product.v1.ProductService/CreateCategory",
		"/nexusmart.product.v1.ProductService/UpdateCategory",
		"/nexusmart.product.v1.ProductService/DeleteCategory",
		"/nexusmart.product.v1.ProductService/CreateProduct",
		"/nexusmart.product.v1.ProductService/UpdateProduct",
		"/nexusmart.product.v1.ProductService/DeleteProduct",
		"/nexusmart.product.v1.ProductService/AdminReviewProduct",
		"/nexusmart.product.v1.ProductService/UpdateStock",
		"/nexusmart.product.v1.ProductService/ReserveStock",
		// PromotionService
		"/nexusmart.promotion.v1.PromotionService/CreateVoucher",
		"/nexusmart.promotion.v1.PromotionService/SaveVoucher",
		"/nexusmart.promotion.v1.PromotionService/CreateFlashSale",
		"/nexusmart.promotion.v1.PromotionService/RegisterFlashSaleItem",
		"/nexusmart.promotion.v1.PromotionService/ApproveFlashSaleItem",
		"/nexusmart.promotion.v1.PromotionService/GetCoinBalance",
		"/nexusmart.promotion.v1.PromotionService/PreviewDiscounts",
		"/nexusmart.promotion.v1.PromotionService/ReservePromotion",
		// OrderService
		"/nexusmart.order.v1.OrderService/CreateCheckout",
		"/nexusmart.order.v1.OrderService/GetOrder",
		"/nexusmart.order.v1.OrderService/ListOrders",
		"/nexusmart.order.v1.OrderService/CancelOrder",
		"/nexusmart.order.v1.OrderService/SellerConfirmOrder",
		"/nexusmart.order.v1.OrderService/SellerShipOrder",
		"/nexusmart.order.v1.OrderService/BuyerConfirmReceived",
		"/nexusmart.order.v1.OrderService/RequestReturn",
		"/nexusmart.order.v1.OrderService/GetOrderTimeline",
		"/nexusmart.order.v1.OrderService/RetryPayment",
		"/nexusmart.order.v1.OrderService/GetReturnRequest",
		"/nexusmart.order.v1.OrderService/ListReturnRequests",
		"/nexusmart.order.v1.OrderService/RespondReturn",
		"/nexusmart.order.v1.OrderService/EscalateReturn",
		"/nexusmart.order.v1.OrderService/AddReturnEvidence",
		"/nexusmart.order.v1.OrderService/ConfirmReturnReceived",
		"/nexusmart.order.v1.OrderService/DisputeReturnGoods",
		"/nexusmart.order.v1.OrderService/AdminResolveReturn",
		// ShippingService
		"/nexusmart.shipping.v1.ShippingService/CalculateFee",
		"/nexusmart.shipping.v1.ShippingService/CreateShipment",
		"/nexusmart.shipping.v1.ShippingService/RequestRedelivery",
		"/nexusmart.shipping.v1.ShippingService/UpdateShopShippingSettings",
		"/nexusmart.shipping.v1.ShippingService/AdminListCarriers",
		"/nexusmart.shipping.v1.ShippingService/AdminUpdateCarrier",
		"/nexusmart.shipping.v1.ShippingService/AdminTestCarrier",
		// NotificationService
		"/nexusmart.notification.v1.NotificationService/ListNotifications",
		"/nexusmart.notification.v1.NotificationService/MarkRead",
		"/nexusmart.notification.v1.NotificationService/GetPreferences",
		"/nexusmart.notification.v1.NotificationService/UpdatePreferences",
	}
	publicRPCs := []string{
		"/nexusmart.user.v1.UserService/Register",
		"/nexusmart.user.v1.UserService/VerifyOTP",
		"/nexusmart.user.v1.UserService/ResendOTP",
		"/nexusmart.user.v1.UserService/ForgotPassword",
		"/nexusmart.user.v1.UserService/ResetPassword",
		"/nexusmart.user.v1.UserService/Login",
		"/nexusmart.user.v1.UserService/RefreshToken",
		"/nexusmart.user.v1.UserService/GetShop",
		"/nexusmart.user.v1.UserService/ListPublicShops",
		"/nexusmart.shipping.v1.ShippingService/ListProvinces",
		"/nexusmart.shipping.v1.ShippingService/ListWards",
		"/nexusmart.shipping.v1.ShippingService/MapLegacyAddress",
		"/nexusmart.promotion.v1.PromotionService/ListAvailableVouchers",
		"/nexusmart.promotion.v1.PromotionService/ListActiveFlashSales",
		"/nexusmart.product.v1.ProductService/ListCategories",
		"/nexusmart.product.v1.ProductService/GetProduct",
		"/nexusmart.product.v1.ProductService/ListProducts",
		"/nexusmart.product.v1.ProductService/GetSKUsBatch",
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
		{"/nexusmart.order.v1.OrderService/GetOrder", buyerTok, "buyer-1"},
		{"/nexusmart.order.v1.OrderService/RetryPayment", buyerTok, "buyer-1"},
		{"/nexusmart.order.v1.OrderService/GetReturnRequest", buyerTok, "buyer-1"},
		{"/nexusmart.order.v1.OrderService/RespondReturn", sellerTok, "seller-1"},
		{"/nexusmart.order.v1.OrderService/AdminResolveReturn", adminTok, "admin-1"},
		{"/nexusmart.shipping.v1.ShippingService/GetShipment", buyerTok, "buyer-1"},
		{"/nexusmart.shipping.v1.ShippingService/CalculateFee", buyerTok, "buyer-1"},
		{"/nexusmart.shipping.v1.ShippingService/CreateShipment", sellerTok, "seller-1"},
		{"/nexusmart.shipping.v1.ShippingService/RequestRedelivery", buyerTok, "buyer-1"},
		{"/nexusmart.shipping.v1.ShippingService/UpdateShopShippingSettings", sellerTok, "seller-1"},
		{"/nexusmart.shipping.v1.ShippingService/AdminListCarriers", adminTok, "admin-1"},
		{"/nexusmart.shipping.v1.ShippingService/CancelShipment", sellerTok, "seller-1"},
		{"/nexusmart.notification.v1.NotificationService/ListNotifications", buyerTok, "buyer-1"},
		{"/nexusmart.promotion.v1.PromotionService/GetCoinBalance", buyerTok, "buyer-1"},
		{"/nexusmart.promotion.v1.PromotionService/CreditCoins", adminTok, "admin-1"},
		{"/nexusmart.product.v1.ProductService/CreateProduct", sellerTok, "seller-1"},
		{"/nexusmart.user.v1.UserService/Logout", buyerTok, "buyer-1"},
		{"/nexusmart.user.v1.UserService/ListAddresses", buyerTok, "buyer-1"},
		{"/nexusmart.user.v1.UserService/ValidateUser", buyerTok, "buyer-1"},
		{"/nexusmart.user.v1.UserService/WriteAuditLog", adminTok, "admin-1"},
	} {
		claims, err := callInterceptor(t, users, svc, tc.method, MDAuthorization, "Bearer "+tc.tok)
		if err != nil || claims == nil || claims.UserID() != tc.wantID {
			t.Fatalf("user JWT on %s: want sub=%s, got claims=%+v err=%v", tc.method, tc.wantID, claims, err)
		}
	}

	// Explicitly public RPCs allow anonymous callers (ignoring spoofed headers) and reject service tokens.
	for _, m := range publicRPCs {
		claims, err := callInterceptor(t, users, svc, m, spoof...)
		if err != nil || claims != nil {
			t.Fatalf("public RPC %s with spoofed anonymous headers: want nil claims & nil err, got claims=%+v err=%v", m, claims, err)
		}
		if _, err := callInterceptor(t, users, svc, m, MDAuthorization, "Bearer "+svcTok); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token on public RPC %s: want PermissionDenied, got %v", m, err)
		}
	}
}
