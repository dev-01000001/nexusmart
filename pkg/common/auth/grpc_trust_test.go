package auth

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
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
		"/nexusmart.user.v1.UserService/GetShopBankAccountForPayout",
		"/nexusmart.product.v1.ProductService/ReleaseStock",
		"/nexusmart.product.v1.ProductService/ConfirmStock",
		"/nexusmart.product.v1.ProductService/RestockOrderItems",
		"/nexusmart.product.v1.ProductService/GetCategoryDescendants",
		"/nexusmart.promotion.v1.PromotionService/ReleasePromotion",
		"/nexusmart.promotion.v1.PromotionService/ReleasePromotionShop",
		"/nexusmart.promotion.v1.PromotionService/CreditCoins",
		"/nexusmart.promotion.v1.PromotionService/ReservePromotion",
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
		"/nexusmart.user.v1.UserService/AddShopBankAccount",
		"/nexusmart.user.v1.UserService/VerifyShopBankAccountOTP",
		"/nexusmart.user.v1.UserService/GetShopBankAccount",
		"/nexusmart.user.v1.UserService/AdminReviewShopBankAccount",
		"/nexusmart.user.v1.UserService/GetPlatformUserStats",
		// ProductService
		"/nexusmart.product.v1.ProductService/CreateCategory",
		"/nexusmart.product.v1.ProductService/UpdateCategory",
		"/nexusmart.product.v1.ProductService/DeleteCategory",
		"/nexusmart.product.v1.ProductService/SetCategoryFeeSchedule",
		"/nexusmart.product.v1.ProductService/ListCategoryFeeSchedules",
		"/nexusmart.product.v1.ProductService/CreateProduct",
		"/nexusmart.product.v1.ProductService/UpdateProduct",
		"/nexusmart.product.v1.ProductService/DeleteProduct",
		"/nexusmart.product.v1.ProductService/AdminReviewProduct",
		"/nexusmart.product.v1.ProductService/UpdateStock",
		"/nexusmart.product.v1.ProductService/ReserveStock",
		// CartService
		"/nexusmart.cart.v1.CartService/GetCart",
		"/nexusmart.cart.v1.CartService/AddItem",
		"/nexusmart.cart.v1.CartService/UpdateItemQty",
		"/nexusmart.cart.v1.CartService/RemoveItem",
		"/nexusmart.cart.v1.CartService/ClearItems",
		// PromotionService
		"/nexusmart.promotion.v1.PromotionService/CreateVoucher",
		"/nexusmart.promotion.v1.PromotionService/UpdateVoucher",
		"/nexusmart.promotion.v1.PromotionService/SetVoucherStatus",
		"/nexusmart.promotion.v1.PromotionService/DeleteVoucher",
		"/nexusmart.promotion.v1.PromotionService/SaveVoucher",
		"/nexusmart.promotion.v1.PromotionService/CreateFlashSale",
		"/nexusmart.promotion.v1.PromotionService/RegisterFlashSaleItem",
		"/nexusmart.promotion.v1.PromotionService/ApproveFlashSaleItem",
		"/nexusmart.promotion.v1.PromotionService/ListFlashSaleItems",
		"/nexusmart.promotion.v1.PromotionService/ListAllFlashSales",
		"/nexusmart.promotion.v1.PromotionService/CreateShopDiscountProgram",
		"/nexusmart.promotion.v1.PromotionService/UpdateShopDiscountProgram",
		"/nexusmart.promotion.v1.PromotionService/EndShopDiscountProgram",
		"/nexusmart.promotion.v1.PromotionService/ListShopDiscountPrograms",
		"/nexusmart.promotion.v1.PromotionService/GetCoinBalance",
		"/nexusmart.promotion.v1.PromotionService/PreviewDiscounts",
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
		"/nexusmart.order.v1.OrderService/GetSellerReport",
		"/nexusmart.order.v1.OrderService/GetPlatformReport",
		"/nexusmart.order.v1.OrderService/ListOrdersNeedingReview",
		"/nexusmart.order.v1.OrderService/ResolveOrderReview",
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
	expectedPublicRPCs := []string{
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
		"/nexusmart.promotion.v1.PromotionService/ListActiveShopDiscounts",
		"/nexusmart.product.v1.ProductService/ListCategories",
		"/nexusmart.product.v1.ProductService/GetProduct",
		"/nexusmart.product.v1.ProductService/ListProducts",
		"/nexusmart.product.v1.ProductService/GetSKUsBatch",
	}

	if got, want := len(serviceAllowedRPCs)+len(userOnlyInternalRPCs), len(internalRPCs); got != want {
		t.Fatalf("contract test internal RPC count=%d does not match internalRPCs map size=%d", got, want)
	}
	if got, want := len(expectedPublicRPCs), len(publicRPCs); got != want {
		t.Fatalf("contract test public RPC count=%d does not match publicRPCs map size=%d", got, want)
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
		{"/nexusmart.user.v1.UserService/AddShopBankAccount", sellerTok, "seller-1"},
		{"/nexusmart.user.v1.UserService/GetPlatformUserStats", adminTok, "admin-1"},
		{"/nexusmart.product.v1.ProductService/SetCategoryFeeSchedule", adminTok, "admin-1"},
		{"/nexusmart.product.v1.ProductService/ListCategoryFeeSchedules", adminTok, "admin-1"},
		{"/nexusmart.promotion.v1.PromotionService/UpdateVoucher", sellerTok, "seller-1"},
		{"/nexusmart.promotion.v1.PromotionService/ListFlashSaleItems", adminTok, "admin-1"},
		{"/nexusmart.promotion.v1.PromotionService/CreateShopDiscountProgram", sellerTok, "seller-1"},
		{"/nexusmart.order.v1.OrderService/GetSellerReport", sellerTok, "seller-1"},
		{"/nexusmart.order.v1.OrderService/GetPlatformReport", adminTok, "admin-1"},
		{"/nexusmart.order.v1.OrderService/ListOrdersNeedingReview", adminTok, "admin-1"},
		{"/nexusmart.order.v1.OrderService/ResolveOrderReview", adminTok, "admin-1"},
		{"/nexusmart.cart.v1.CartService/GetCart", buyerTok, "buyer-1"},
		{"/nexusmart.cart.v1.CartService/AddItem", buyerTok, "buyer-1"},
		{"/nexusmart.cart.v1.CartService/UpdateItemQty", buyerTok, "buyer-1"},
		{"/nexusmart.cart.v1.CartService/RemoveItem", buyerTok, "buyer-1"},
		{"/nexusmart.cart.v1.CartService/ClearItems", buyerTok, "buyer-1"},
	} {
		claims, err := callInterceptor(t, users, svc, tc.method, MDAuthorization, "Bearer "+tc.tok)
		if err != nil || claims == nil || claims.UserID() != tc.wantID {
			t.Fatalf("user JWT on %s: want sub=%s, got claims=%+v err=%v", tc.method, tc.wantID, claims, err)
		}
	}

	// Explicitly public RPCs allow anonymous callers (ignoring spoofed headers) and reject service tokens.
	for _, m := range expectedPublicRPCs {
		if !publicRPCs[m] {
			t.Fatalf("expectedPublicRPCs entry %s missing from auth.publicRPCs", m)
		}
		claims, err := callInterceptor(t, users, svc, m, spoof...)
		if err != nil || claims != nil {
			t.Fatalf("public RPC %s with spoofed anonymous headers: want nil claims & nil err, got claims=%+v err=%v", m, claims, err)
		}
		if _, err := callInterceptor(t, users, svc, m, MDAuthorization, "Bearer "+svcTok); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token on public RPC %s: want PermissionDenied, got %v", m, err)
		}
	}
}

// TestGRPCInterceptorServiceTokenPolicyD3 verifies D3: ReservePromotion accepts a service token
// without forwarding a raw token downstream, while every internalRPCs entry set to false rejects
// service tokens with PermissionDenied without invoking the handler.
func TestGRPCInterceptorServiceTokenPolicyD3(t *testing.T) {
	users, _ := NewManager("user-key", 15*time.Minute)
	svc, _ := NewManager("service-key", ServiceTokenTTL)
	orderTok, err := svc.SignServiceToken("order-service")
	if err != nil {
		t.Fatalf("SignServiceToken: %v", err)
	}

	const reserveMethod = "/nexusmart.promotion.v1.PromotionService/ReservePromotion"
	if !internalRPCs[reserveMethod] {
		t.Fatalf("expected internalRPCs[%q] == true (D3)", reserveMethod)
	}
	inCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(MDAuthorization, "Bearer "+orderTok))
	var gotClaims *Claims
	var gotRaw string
	_, err = GRPCUnaryServerInterceptor(users, svc)(inCtx, nil, &grpc.UnaryServerInfo{FullMethod: reserveMethod},
		func(ctx context.Context, _ any) (any, error) {
			gotClaims, _ = ClaimsFromContext(ctx)
			gotRaw = RawTokenFromContext(ctx)
			return "ok", nil
		})
	if err != nil {
		t.Fatalf("ReservePromotion with order-service token: got err=%v, want nil", err)
	}
	if gotClaims == nil || gotClaims.Service != "order-service" || gotClaims.Role != ServiceRole {
		t.Fatalf("ReservePromotion claims: got %+v, want Service=order-service Role=%s", gotClaims, ServiceRole)
	}
	if gotRaw != "" {
		t.Fatalf("service token must never populate RawTokenFromContext, got non-empty token")
	}

	// Every internalRPCs entry marked false must reject service tokens with PermissionDenied
	// and never invoke the underlying handler.
	falseCount := 0
	for method, allowService := range internalRPCs {
		if allowService {
			continue
		}
		falseCount++
		handlerCalled := false
		_, err := GRPCUnaryServerInterceptor(users, svc)(inCtx, nil, &grpc.UnaryServerInfo{FullMethod: method},
			func(_ context.Context, _ any) (any, error) {
				handlerCalled = true
				return "ok", nil
			})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("service token on false RPC %s: got %v, want PermissionDenied", method, err)
		}
		if handlerCalled {
			t.Fatalf("handler must not be invoked when service token is rejected on %s", method)
		}
	}
	if falseCount == 0 {
		t.Fatalf("expected user-only (false) entries in internalRPCs")
	}
}

// TestAllGoServiceRPCsAreClassified inspects every generated gRPC ServiceDesc under
// gen/go/nexusmart/ (excluding Rust-only PaymentService) and enforces that every Go RPC
// is explicitly classified in either internalRPCs or publicRPCs, with no overlap and no
// stale entries. Any newly added Go RPC forgotten from both fails this test.
func TestAllGoServiceRPCsAreClassified(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	genNexusmartDir := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "gen", "go", "nexusmart"))

	// Rust services enforce service-token allowlists in their own codebase
	// (PaymentService -> services/payment/src/auth.rs SERVICE_METHODS).
	nonGoServices := map[string]bool{
		"nexusmart.payment.v1.PaymentService": true,
	}

	discoveredRPCs, discoveredServices := discoverGeneratedGoRPCs(t, genNexusmartDir, nonGoServices)
	if len(discoveredServices) < 7 {
		t.Fatalf("expected at least 7 Go gRPC services under %s, found %d (%v)", genNexusmartDir, len(discoveredServices), discoveredServices)
	}
	if len(discoveredRPCs) < 100 {
		t.Fatalf("expected >100 Go RPCs under %s, found %d", genNexusmartDir, len(discoveredRPCs))
	}

	// 1. Every generated Go RPC must be classified in exactly one of internalRPCs or publicRPCs.
	var unclassified, overlapped []string
	for method := range discoveredRPCs {
		_, inInternal := internalRPCs[method]
		inPublic := publicRPCs[method]
		switch {
		case !inInternal && !inPublic:
			unclassified = append(unclassified, method)
		case inInternal && inPublic:
			overlapped = append(overlapped, method)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(overlapped)
	if len(unclassified) > 0 {
		t.Errorf("unclassified Go RPCs (add each to internalRPCs or publicRPCs in pkg/common/auth/auth.go and update docs/contracts.md §4.10): %v", unclassified)
	}
	if len(overlapped) > 0 {
		t.Errorf("RPCs classified as BOTH internal and public: %v", overlapped)
	}

	// 2. No stale/typo RPC names in internalRPCs or publicRPCs.
	var staleInternal, stalePublic []string
	for method := range internalRPCs {
		if !discoveredRPCs[method] {
			staleInternal = append(staleInternal, method)
		}
	}
	for method := range publicRPCs {
		if !discoveredRPCs[method] {
			stalePublic = append(stalePublic, method)
		}
	}
	sort.Strings(staleInternal)
	sort.Strings(stalePublic)
	if len(staleInternal) > 0 {
		t.Errorf("stale RPCs in internalRPCs not found in gen/go ServiceDesc: %v", staleInternal)
	}
	if len(stalePublic) > 0 {
		t.Errorf("stale RPCs in publicRPCs not found in gen/go ServiceDesc: %v", stalePublic)
	}
}

func discoverGeneratedGoRPCs(t *testing.T, rootDir string, skipServices map[string]bool) (map[string]bool, []string) {
	t.Helper()

	rpcs := make(map[string]bool)
	servicesSet := make(map[string]bool)
	fset := token.NewFileSet()

	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), "_grpc.pb.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.VAR {
				continue
			}
			for _, spec := range genDecl.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 || !strings.HasSuffix(vs.Names[0].Name, "_ServiceDesc") {
					continue
				}
				comp, ok := vs.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				svcName, methods := parseServiceDescLit(t, path, comp)
				if svcName == "" {
					t.Fatalf("%s: %s missing ServiceName", path, vs.Names[0].Name)
				}
				if skipServices[svcName] {
					continue
				}
				servicesSet[svcName] = true
				for _, m := range methods {
					rpcs["/"+svcName+"/"+m] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", rootDir, err)
	}

	services := make([]string, 0, len(servicesSet))
	for s := range servicesSet {
		services = append(services, s)
	}
	sort.Strings(services)
	return rpcs, services
}

func parseServiceDescLit(t *testing.T, filePath string, comp *ast.CompositeLit) (string, []string) {
	t.Helper()

	var serviceName string
	var methods []string
	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "ServiceName":
			serviceName = unquoteASTString(t, filePath, kv.Value)
		case "Methods":
			methods = append(methods, parseDescSliceNames(t, filePath, kv.Value, "MethodName")...)
		case "Streams":
			methods = append(methods, parseDescSliceNames(t, filePath, kv.Value, "StreamName")...)
		}
	}
	return serviceName, methods
}

func parseDescSliceNames(t *testing.T, filePath string, expr ast.Expr, fieldName string) []string {
	t.Helper()

	sliceLit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	var names []string
	for _, item := range sliceLit.Elts {
		entryLit, ok := item.(*ast.CompositeLit)
		if !ok {
			continue
		}
		for _, field := range entryLit.Elts {
			kv, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			k, ok := kv.Key.(*ast.Ident)
			if ok && k.Name == fieldName {
				names = append(names, unquoteASTString(t, filePath, kv.Value))
			}
		}
	}
	return names
}

func unquoteASTString(t *testing.T, filePath string, expr ast.Expr) string {
	t.Helper()

	bl, ok := expr.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		t.Fatalf("%s: expected string literal, got %T", filePath, expr)
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		t.Fatalf("%s: unquote %s: %v", filePath, bl.Value, err)
	}
	return s
}
