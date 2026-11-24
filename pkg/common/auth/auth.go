package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// DefaultAccessTTL is the required 15-minute access token lifetime.
	DefaultAccessTTL = 15 * time.Minute

	// ServiceTokenTTL is the lifetime of internal service tokens (minted per call).
	ServiceTokenTTL = time.Minute
	// ServiceRole is the role carried by service tokens. It is only honoured on internalRPCs
	// entries marked true, never on any other RPC.
	ServiceRole = "admin"

	MDAuthorization = "authorization"
	MDRequestID     = "x-request-id"
)

// internalRPCs are RPCs that must never run for an anonymous caller.
// true = accepts a service token (saga/job/consumer calls with no end user; handlers may
// restrict further, e.g. service-token-only or a specific claims.Service such as "order-service");
// false = needs the forwarded end-user JWT (service tokens are rejected with PermissionDenied).
// Every other RPC (publicRPCs or unlisted) rejects service tokens with PermissionDenied.
var internalRPCs = map[string]bool{
	// UserService (public: Register, VerifyOTP, ResendOTP, ForgotPassword, ResetPassword, Login, RefreshToken, GetShop, ListPublicShops)
	"/nexusmart.user.v1.UserService/GetAddress":                  true,
	"/nexusmart.user.v1.UserService/GetProfile":                  true,
	"/nexusmart.user.v1.UserService/RecordCODRejection":          true,
	"/nexusmart.user.v1.UserService/WriteAuditLog":               true,
	"/nexusmart.user.v1.UserService/GetShopBankAccountForPayout": true,
	"/nexusmart.user.v1.UserService/Logout":                      false,
	"/nexusmart.user.v1.UserService/UpdateProfile":               false,
	"/nexusmart.user.v1.UserService/ValidateUser":                false,
	"/nexusmart.user.v1.UserService/AdminListUsers":              false,
	"/nexusmart.user.v1.UserService/AdminUpdateUserStatus":       false,
	"/nexusmart.user.v1.UserService/AdminUpdateUserRole":         false,
	"/nexusmart.user.v1.UserService/RegisterShop":                false,
	"/nexusmart.user.v1.UserService/AdminListShops":              false,
	"/nexusmart.user.v1.UserService/AdminReviewShop":             false,
	"/nexusmart.user.v1.UserService/CreateAddress":               false,
	"/nexusmart.user.v1.UserService/ListAddresses":               false,
	"/nexusmart.user.v1.UserService/UpdateAddress":               false,
	"/nexusmart.user.v1.UserService/DeleteAddress":               false,
	"/nexusmart.user.v1.UserService/ListAuditLogs":               false,
	"/nexusmart.user.v1.UserService/AddShopBankAccount":          false,
	"/nexusmart.user.v1.UserService/VerifyShopBankAccountOTP":    false,
	"/nexusmart.user.v1.UserService/GetShopBankAccount":          false,
	"/nexusmart.user.v1.UserService/AdminReviewShopBankAccount":  false,
	"/nexusmart.user.v1.UserService/GetPlatformUserStats":        false,
	"/nexusmart.user.v1.UserService/RequestPhoneOTP":             false,
	"/nexusmart.user.v1.UserService/VerifyPhoneOTP":              false,
	"/nexusmart.user.v1.UserService/AdminCreateStaffUser":        false,
	"/nexusmart.user.v1.UserService/AdminListUserActivities":     false,
	"/nexusmart.user.v1.UserService/AdminRequestShopSuspension":  false,
	"/nexusmart.user.v1.UserService/AdminListShopApprovals":      false,
	"/nexusmart.user.v1.UserService/AdminDecideShopSuspension":   false,
	"/nexusmart.user.v1.UserService/AdminUnsuspendShop":          false,
	// ProductService (public: ListCategories, GetProduct, ListProducts, GetSKUsBatch, ListProductReviews)
	"/nexusmart.product.v1.ProductService/ReleaseStock":                   true,
	"/nexusmart.product.v1.ProductService/ConfirmStock":                   true,
	"/nexusmart.product.v1.ProductService/RestockOrderItems":              true,
	"/nexusmart.product.v1.ProductService/GetCategoryDescendants":         true,
	"/nexusmart.product.v1.ProductService/CreateCategory":                 false,
	"/nexusmart.product.v1.ProductService/UpdateCategory":                 false,
	"/nexusmart.product.v1.ProductService/DeleteCategory":                 false,
	"/nexusmart.product.v1.ProductService/SetCategoryFeeSchedule":         false,
	"/nexusmart.product.v1.ProductService/ListCategoryFeeSchedules":       false,
	"/nexusmart.product.v1.ProductService/CreateProduct":                  false,
	"/nexusmart.product.v1.ProductService/UpdateProduct":                  false,
	"/nexusmart.product.v1.ProductService/DeleteProduct":                  false,
	"/nexusmart.product.v1.ProductService/AdminReviewProduct":             false,
	"/nexusmart.product.v1.ProductService/UpdateStock":                    false,
	"/nexusmart.product.v1.ProductService/ReserveStock":                   false,
	"/nexusmart.product.v1.ProductService/CreateReview":                   false,
	"/nexusmart.product.v1.ProductService/ListOrderReviews":               false,
	"/nexusmart.product.v1.ProductService/ListBannedKeywords":             false,
	"/nexusmart.product.v1.ProductService/CreateBannedKeyword":            false,
	"/nexusmart.product.v1.ProductService/UpdateBannedKeyword":            false,
	"/nexusmart.product.v1.ProductService/DeleteBannedKeyword":            false,
	"/nexusmart.product.v1.ProductService/PreviewBannedKeyword":           false,
	"/nexusmart.product.v1.ProductService/RescanProductsForBannedContent": false,
	// CartService (all RPCs require forwarded end-user JWT)
	"/nexusmart.cart.v1.CartService/GetCart":       false,
	"/nexusmart.cart.v1.CartService/AddItem":       false,
	"/nexusmart.cart.v1.CartService/UpdateItemQty": false,
	"/nexusmart.cart.v1.CartService/RemoveItem":    false,
	"/nexusmart.cart.v1.CartService/ClearItems":    false,
	// PromotionService (public: ListAvailableVouchers, ListActiveFlashSales, ListActiveShopDiscounts)
	"/nexusmart.promotion.v1.PromotionService/ReleasePromotion":          true,
	"/nexusmart.promotion.v1.PromotionService/ReleasePromotionShop":      true,
	"/nexusmart.promotion.v1.PromotionService/CreditCoins":               true,
	"/nexusmart.promotion.v1.PromotionService/ReservePromotion":          true, // D3: order-service token only (handler enforces claims.Service == "order-service")
	"/nexusmart.promotion.v1.PromotionService/CreateVoucher":             false,
	"/nexusmart.promotion.v1.PromotionService/UpdateVoucher":             false,
	"/nexusmart.promotion.v1.PromotionService/SetVoucherStatus":          false,
	"/nexusmart.promotion.v1.PromotionService/DeleteVoucher":             false,
	"/nexusmart.promotion.v1.PromotionService/SaveVoucher":               false,
	"/nexusmart.promotion.v1.PromotionService/CreateFlashSale":           false,
	"/nexusmart.promotion.v1.PromotionService/RegisterFlashSaleItem":     false,
	"/nexusmart.promotion.v1.PromotionService/ApproveFlashSaleItem":      false,
	"/nexusmart.promotion.v1.PromotionService/ListFlashSaleItems":        false,
	"/nexusmart.promotion.v1.PromotionService/ListAllFlashSales":         false,
	"/nexusmart.promotion.v1.PromotionService/CreateShopDiscountProgram": false,
	"/nexusmart.promotion.v1.PromotionService/UpdateShopDiscountProgram": false,
	"/nexusmart.promotion.v1.PromotionService/EndShopDiscountProgram":    false,
	"/nexusmart.promotion.v1.PromotionService/ListShopDiscountPrograms":  false,
	"/nexusmart.promotion.v1.PromotionService/GetCoinBalance":            false,
	"/nexusmart.promotion.v1.PromotionService/PreviewDiscounts":          false,
	// OrderService (all RPCs require authentication)
	"/nexusmart.order.v1.OrderService/MarkPaymentResult":       true,
	"/nexusmart.order.v1.OrderService/CreateCheckout":          false,
	"/nexusmart.order.v1.OrderService/GetOrder":                false,
	"/nexusmart.order.v1.OrderService/ListOrders":              false,
	"/nexusmart.order.v1.OrderService/CancelOrder":             false,
	"/nexusmart.order.v1.OrderService/SellerConfirmOrder":      false,
	"/nexusmart.order.v1.OrderService/SellerShipOrder":         false,
	"/nexusmart.order.v1.OrderService/BuyerConfirmReceived":    false,
	"/nexusmart.order.v1.OrderService/RequestReturn":           false,
	"/nexusmart.order.v1.OrderService/GetOrderTimeline":        false,
	"/nexusmart.order.v1.OrderService/RetryPayment":            false,
	"/nexusmart.order.v1.OrderService/GetReturnRequest":        false,
	"/nexusmart.order.v1.OrderService/ListReturnRequests":      false,
	"/nexusmart.order.v1.OrderService/RespondReturn":           false,
	"/nexusmart.order.v1.OrderService/EscalateReturn":          false,
	"/nexusmart.order.v1.OrderService/AddReturnEvidence":       false,
	"/nexusmart.order.v1.OrderService/ConfirmReturnReceived":   false,
	"/nexusmart.order.v1.OrderService/DisputeReturnGoods":      false,
	"/nexusmart.order.v1.OrderService/AdminResolveReturn":      false,
	"/nexusmart.order.v1.OrderService/GetSellerReport":         false,
	"/nexusmart.order.v1.OrderService/GetPlatformReport":       false,
	"/nexusmart.order.v1.OrderService/ListOrdersNeedingReview": false,
	"/nexusmart.order.v1.OrderService/ResolveOrderReview":      false,
	// ShippingService (public: ListProvinces, ListWards, MapLegacyAddress, CalculateFee)
	"/nexusmart.shipping.v1.ShippingService/GetShipment":                true,
	"/nexusmart.shipping.v1.ShippingService/CancelShipment":             true,
	"/nexusmart.shipping.v1.ShippingService/CreateReturnShipment":       true,
	"/nexusmart.shipping.v1.ShippingService/GetShopShippingSettings":    true,
	"/nexusmart.shipping.v1.ShippingService/CreateShipment":             false,
	"/nexusmart.shipping.v1.ShippingService/RequestRedelivery":          false,
	"/nexusmart.shipping.v1.ShippingService/UpdateShopShippingSettings": false,
	"/nexusmart.shipping.v1.ShippingService/AdminListCarriers":          false,
	"/nexusmart.shipping.v1.ShippingService/AdminUpdateCarrier":         false,
	"/nexusmart.shipping.v1.ShippingService/AdminTestCarrier":           false,
	// NotificationService (all RPCs require authentication)
	"/nexusmart.notification.v1.NotificationService/ListNotifications": false,
	"/nexusmart.notification.v1.NotificationService/MarkRead":          false,
	"/nexusmart.notification.v1.NotificationService/GetPreferences":    false,
	"/nexusmart.notification.v1.NotificationService/UpdatePreferences": false,
}

// publicRPCs are Go service RPCs explicitly permitted for anonymous callers
// (matching docs/contracts.md §4.10). Service tokens are still rejected on these RPCs.
// Every Go RPC in gen/go/nexusmart/ must appear in exactly one of internalRPCs or publicRPCs
// (enforced by TestAllGoServiceRPCsAreClassified).
var publicRPCs = map[string]bool{
	"/nexusmart.user.v1.UserService/Register":                          true,
	"/nexusmart.user.v1.UserService/VerifyOTP":                         true,
	"/nexusmart.user.v1.UserService/ResendOTP":                         true,
	"/nexusmart.user.v1.UserService/ForgotPassword":                    true,
	"/nexusmart.user.v1.UserService/ResetPassword":                     true,
	"/nexusmart.user.v1.UserService/Login":                             true,
	"/nexusmart.user.v1.UserService/RefreshToken":                      true,
	"/nexusmart.user.v1.UserService/GetShop":                           true,
	"/nexusmart.user.v1.UserService/ListPublicShops":                   true,
	"/nexusmart.product.v1.ProductService/ListCategories":              true,
	"/nexusmart.product.v1.ProductService/GetProduct":                  true,
	"/nexusmart.product.v1.ProductService/ListProducts":                true,
	"/nexusmart.product.v1.ProductService/GetSKUsBatch":                true,
	"/nexusmart.product.v1.ProductService/ListProductReviews":          true,
	"/nexusmart.promotion.v1.PromotionService/ListAvailableVouchers":   true,
	"/nexusmart.promotion.v1.PromotionService/ListActiveFlashSales":    true,
	"/nexusmart.promotion.v1.PromotionService/ListActiveShopDiscounts": true,
	"/nexusmart.shipping.v1.ShippingService/ListProvinces":             true,
	"/nexusmart.shipping.v1.ShippingService/ListWards":                 true,
	"/nexusmart.shipping.v1.ShippingService/MapLegacyAddress":          true,
	"/nexusmart.shipping.v1.ShippingService/CalculateFee":              true,
}

var (
	ErrEmptySecret   = errors.New("jwt secret must not be empty")
	ErrInvalidToken  = errors.New("invalid or expired access token")
	ErrMissingClaims = errors.New("missing authentication claims")
)

// Claims represents NexusMart HS256 JWT claims. Service is set only on internal service tokens.
type Claims struct {
	Role    string `json:"role"`
	ShopID  string `json:"shop_id,omitempty"`
	Service string `json:"service,omitempty"`
	jwt.RegisteredClaims
}

// UserID returns the subject (sub) claim.
func (c *Claims) UserID() string {
	if c == nil {
		return ""
	}
	return c.Subject
}

// JTI returns the unique token ID (jti) claim.
func (c *Claims) JTI() string {
	if c == nil {
		return ""
	}
	return c.ID
}

// Manager signs and verifies HS256 JWT access tokens.
type Manager struct {
	secret []byte
	ttl    time.Duration
}

// NewManager constructs a JWT Manager with HS256 secret and TTL (defaults to 15m).
func NewManager(secret string, ttl time.Duration) (*Manager, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrEmptySecret
	}
	if ttl <= 0 {
		ttl = DefaultAccessTTL
	}
	return &Manager{
		secret: []byte(secret),
		ttl:    ttl,
	}, nil
}

// SignAccessToken generates a signed HS256 JWT containing sub, role, shop_id, and jti.
func (m *Manager) SignAccessToken(userID, role, shopID string) (string, string, time.Time, error) {
	now := time.Now().UTC()
	exp := now.Add(m.ttl)
	jti := uuid.NewString()

	claims := Claims{
		Role:   role,
		ShopID: shopID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        jti,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			Issuer:    "nexusmart",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("sign jwt: %w", err)
	}
	return signed, jti, exp, nil
}

// SignServiceToken mints a short-lived internal token for a caller with no end user
// (saga compensation, jobs, consumers). Use a Manager built from the service key, never the user key.
//
// ponytail: one shared HS256 service key; any holder can mint any service identity and
// nothing binds the token to the TCP peer. Upgrade path: mTLS between services, or
// asymmetric per-service JWT (EdDSA) verified via JWKS.
func (m *Manager) SignServiceToken(service string) (string, error) {
	now := time.Now().UTC()
	claims := Claims{
		Role:    ServiceRole,
		Service: service,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   service,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
			Issuer:    "nexusmart",
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign service jwt: %w", err)
	}
	return signed, nil
}

// VerifyAccessToken validates an end-user HS256 JWT. Service tokens are rejected.
func (m *Manager) VerifyAccessToken(tokenStr string) (*Claims, error) {
	claims, err := m.verify(tokenStr)
	if err != nil || claims.Service != "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// VerifyServiceToken validates an internal service token. End-user tokens are rejected.
func (m *Manager) VerifyServiceToken(tokenStr string) (*Claims, error) {
	claims, err := m.verify(tokenStr)
	if err != nil || claims.Service == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (m *Manager) verify(tokenStr string) (*Claims, error) {
	tokenStr = strings.TrimSpace(tokenStr)
	tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")
	tokenStr = strings.TrimPrefix(tokenStr, "bearer ")
	if tokenStr == "" {
		return nil, ErrInvalidToken
	}

	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(tokenStr, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
	if err != nil || !parsed.Valid || claims.Subject == "" {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

type claimsContextKey struct{}
type rawTokenContextKey struct{}

// WithClaims stores Claims in ctx.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, claimsContextKey{}, claims)
}

// ClaimsFromContext retrieves Claims from ctx.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsContextKey{}).(*Claims)
	return c, ok && c != nil
}

// WithRawToken stores the raw Authorization Bearer token string in ctx for downstream forwarding.
func WithRawToken(ctx context.Context, rawToken string) context.Context {
	return context.WithValue(ctx, rawTokenContextKey{}, rawToken)
}

// RawTokenFromContext retrieves the raw Bearer token stored in ctx.
func RawTokenFromContext(ctx context.Context) string {
	v, _ := ctx.Value(rawTokenContextKey{}).(string)
	return v
}

// UserIDFromContext extracts the authenticated user ID (sub) from ctx.
func UserIDFromContext(ctx context.Context) string {
	if c, ok := ClaimsFromContext(ctx); ok {
		return c.Subject
	}
	return ""
}

// RoleFromContext extracts the authenticated user role from ctx.
func RoleFromContext(ctx context.Context) string {
	if c, ok := ClaimsFromContext(ctx); ok {
		return c.Role
	}
	return ""
}

// ShopIDFromContext extracts the authenticated seller shop ID from ctx.
func ShopIDFromContext(ctx context.Context) string {
	if c, ok := ClaimsFromContext(ctx); ok {
		return c.ShopID
	}
	return ""
}

// HasRole checks whether claims has one of the allowed roles.
func HasRole(claims *Claims, allowed ...string) bool {
	if claims == nil {
		return false
	}
	for _, r := range allowed {
		if strings.EqualFold(claims.Role, r) {
			return true
		}
	}
	return false
}

// InjectOutgoingGRPC forwards the Bearer token (end-user JWT or service token) in outgoing
// gRPC metadata. Identity travels only inside the signed token, never as plain metadata.
func InjectOutgoingGRPC(ctx context.Context, rawBearer string) context.Context {
	if rawBearer == "" {
		return ctx
	}
	if !strings.HasPrefix(strings.ToLower(rawBearer), "bearer ") {
		rawBearer = "Bearer " + rawBearer
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		md = md.Copy()
	} else {
		md = metadata.New(nil)
	}
	md.Set(MDAuthorization, rawBearer)
	return metadata.NewOutgoingContext(ctx, md)
}

// GRPCUnaryServerInterceptor authenticates gRPC callers from the Bearer token only:
//   - end-user JWT signed with the user key (users) -> Claims + raw token for onward forwarding;
//   - service token signed with the internal key (svc) -> Claims, only on internalRPCs marked true;
//   - no token -> anonymous (handlers enforce their own auth), except on internalRPCs.
//
// Metadata such as x-user-id / x-user-role / x-shop-id is ignored. Either manager may be nil
// (that token kind is then rejected).
func GRPCUnaryServerInterceptor(users, svc *Manager) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		serviceAllowed, internal := internalRPCs[info.FullMethod]
		var rawBearer string
		if md, ok := metadata.FromIncomingContext(ctx); ok {
			rawBearer = strings.TrimSpace(firstMD(md, MDAuthorization))
		}
		if rawBearer == "" {
			if internal {
				return nil, status.Error(codes.Unauthenticated, "authentication required")
			}
			return handler(ctx, req)
		}
		if users != nil {
			if claims, err := users.VerifyAccessToken(rawBearer); err == nil {
				return handler(WithRawToken(WithClaims(ctx, claims), rawBearer), req)
			}
		}
		if svc != nil {
			if claims, err := svc.VerifyServiceToken(rawBearer); err == nil {
				if !serviceAllowed {
					return nil, status.Error(codes.PermissionDenied, "service identity not allowed for this RPC")
				}
				// No raw token: a service token is never forwarded further downstream.
				return handler(WithClaims(ctx, claims), req)
			}
		}
		return nil, status.Error(codes.Unauthenticated, "invalid access token")
	}
}

// HTTPMiddleware validates Authorization Bearer tokens on HTTP requests when required=true,
// or attaches Claims when present if required=false.
func HTTPMiddleware(mgr *Manager, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
			if authHeader == "" {
				if required {
					http.Error(w, `{"error":"missing authorization header"}`, http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			claims, err := mgr.VerifyAccessToken(authHeader)
			if err != nil {
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}
			ctx := WithClaims(r.Context(), claims)
			ctx = WithRawToken(ctx, authHeader)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireRolesHTTP enforces RBAC on HTTP handlers after HTTPMiddleware has populated Claims.
func RequireRolesHTTP(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := ClaimsFromContext(r.Context())
			if !ok {
				http.Error(w, `{"error":"unauthenticated"}`, http.StatusUnauthorized)
				return
			}
			if !HasRole(claims, allowed...) {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func firstMD(md metadata.MD, key string) string {
	vals := md.Get(key)
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}
