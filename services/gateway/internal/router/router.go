package router

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
)

// Dependencies wires all handlers, middlewares, and reverse proxies into the gateway router.
type Dependencies struct {
	ServiceName          string
	Clients              *clients.Clients
	AuthManager          *auth.Manager
	RevocationChecker    middleware.JTIRevocationChecker
	RateLimiter          middleware.RateLimiter
	RateLimitIPPerMin    int
	RateLimitUserPerMin  int
	RateLimitFlashPerMin int
	TrustedProxies       *middleware.TrustedProxies
	RED                  *metrics.RED
	OpsHandler           http.Handler
	PaymentWebhookProxy  http.Handler
	ShippingWebhookProxy http.Handler
	PaymentSSEProxy      http.Handler
	Logger               *slog.Logger
}

type handler struct {
	clients *clients.Clients
	log     *slog.Logger
}

// New constructs the complete API Gateway HTTP router matching docs/contracts.md Section 4.
func New(deps Dependencies) http.Handler {
	serviceName := deps.ServiceName
	if serviceName == "" {
		serviceName = "gateway"
	}

	h := &handler{
		clients: deps.Clients,
		log:     deps.Logger,
	}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer(deps.Logger))

	// Standard ops endpoints (/healthz, /readyz, /metrics) bypass rate limits & auth.
	if deps.OpsHandler != nil {
		r.Handle("/healthz", deps.OpsHandler)
		r.Handle("/readyz", deps.OpsHandler)
		r.Handle("/metrics", deps.OpsHandler)
	}

	// SSE payment events route uses a flusher-preserving trace middleware so chunks stream immediately.
	r.Group(func(sse chi.Router) {
		sse.Use(middleware.FlusherTraceMiddleware(serviceName))
		sse.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			sse.Use(deps.RED.HTTPMiddleware())
		}
		sse.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))
		sse.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.TrustedProxies, deps.Logger))
		sse.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, true))
		sse.Use(middleware.RateLimitByUser(deps.RateLimiter, deps.RateLimitUserPerMin, deps.Logger))

		sse.Get("/api/v1/payments/{id}/events", func(w http.ResponseWriter, req *http.Request) {
			if deps.PaymentSSEProxy == nil {
				httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "payment SSE proxy not configured")
				return
			}
			// Enforce object-level ownership before streaming payment events.
			claims, _ := auth.ClaimsFromContext(req.Context())
			if !auth.HasRole(claims, "admin", "super_admin") && deps.Clients != nil && deps.Clients.Payment != nil {
				outCtx, cancel := deps.Clients.OutgoingContext(req.Context())
				getResp, err := deps.Clients.Payment.GetPayment(outCtx, &paymentv1.GetPaymentRequest{
					PaymentId: chi.URLParam(req, "id"),
				})
				cancel()
				if err != nil {
					httpx.WriteGRPCError(w, req, err)
					return
				}
				if p := getResp.GetPayment(); p != nil && p.GetUserId() != "" && p.GetUserId() != claims.UserID() {
					httpx.WriteError(w, req, http.StatusForbidden, "PERMISSION_DENIED", "cannot subscribe to another user's payment events")
					return
				}
			}
			deps.PaymentSSEProxy.ServeHTTP(w, req)
		})
	})

	// All standard REST & webhook routes share OTel trace + RED metrics + structured JSON logger.
	r.Group(func(main chi.Router) {
		main.Use(telemetry.HTTPMiddleware(serviceName))
		main.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			main.Use(deps.RED.HTTPMiddleware())
		}
		main.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))

		// Section 4.9 Webhooks (no JWT; HMAC verified by upstream; exact raw body & headers preserved).
		if deps.PaymentWebhookProxy != nil {
			main.Post("/webhooks/payment", deps.PaymentWebhookProxy.ServeHTTP)
			main.Post("/webhooks/vietqr", deps.PaymentWebhookProxy.ServeHTTP)
		}
		h.mountOrderDepthWebhooks(main, deps)
		if deps.ShippingWebhookProxy != nil {
			main.Post("/webhooks/shipping/{carrier}", deps.ShippingWebhookProxy.ServeHTTP)
		}

		// Public & authenticated REST API v1 routes with per-IP rate limiting.
		main.Route("/api/v1", func(api chi.Router) {
			api.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.TrustedProxies, deps.Logger))

			// --- Public routes ---
			api.Post("/auth/register", h.handleRegister)
			api.Post("/auth/verify-otp", h.handleVerifyOTP)
			api.Post("/auth/resend-otp", h.handleResendOTP)
			api.Post("/auth/login", h.handleLogin)
			api.Post("/auth/refresh", h.handleRefreshToken)

			api.Get("/shops", h.handleListShops)
			api.Get("/categories", h.handleListCategories)
			api.Get("/sites", h.handleListPublishedSites)
			api.Get("/sites/by-host", h.handleGetSiteByHost)
			api.Get("/sites/by-shop/{shop_id}", h.handleGetSiteByShop)

			api.Get("/shipping/provinces", h.handleListProvinces)
			api.Get("/shipping/provinces/{code}/wards", h.handleListWards)
			api.Post("/shipping/addresses/map-legacy", h.handleMapLegacyAddress)

			// Optional-auth public catalog & vouchers endpoints (forward JWT claims when present).
			api.Group(func(opt chi.Router) {
				opt.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
				opt.Get("/products", h.handleListProducts)
				opt.Get("/products/{id}", h.handleGetProduct)
				opt.Get("/vouchers", h.handleListVouchers)
				opt.Get("/shops/{id}", h.handleGetShop)
				h.mountSearchRoutes(opt)
			})

			// Public flash-sales list with flash-sale rate limit if configured.
			api.Group(func(fl chi.Router) {
				fl.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
				fl.Use(middleware.RateLimitFlashSale(deps.RateLimiter, deps.RateLimitFlashPerMin, deps.TrustedProxies, deps.Logger))
				fl.Get("/flash-sales", h.handleListFlashSales)
			})

			// --- Authenticated routes (any valid JWT) ---
			api.Group(func(authed chi.Router) {
				authed.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, true))
				authed.Use(middleware.RateLimitByUser(deps.RateLimiter, deps.RateLimitUserPerMin, deps.Logger))

				// Section 4.1 Auth & Profile & Addresses
				authed.Post("/auth/logout", h.handleLogout)
				authed.Get("/users/me", h.handleGetMe)
				authed.Patch("/users/me", h.handleUpdateMe)
				authed.Get("/users/me/addresses", h.handleListAddresses)
				authed.Post("/users/me/addresses", h.handleCreateAddress)
				authed.Put("/users/me/addresses/{id}", h.handleUpdateAddress)
				authed.Delete("/users/me/addresses/{id}", h.handleDeleteAddress)

				// Shop registration (buyer, seller)
				authed.Group(func(shopReg chi.Router) {
					shopReg.Use(middleware.RequireRoles("buyer", "seller", "admin", "super_admin"))
					shopReg.Post("/shops/register", h.handleRegisterShop)
				})

				// Section 4.3 Cart
				authed.Get("/cart", h.handleGetCart)
				authed.Post("/cart/items", h.handleAddCartItem)
				authed.Patch("/cart/items/{sku_id}", h.handleUpdateCartItem)
				authed.Delete("/cart/items/{sku_id}", h.handleRemoveCartItem)
				authed.Delete("/cart", h.handleClearCart)

				// Section 4.4 Vouchers, Coins, Promotion Preview
				authed.Post("/vouchers/{id}/save", h.handleSaveVoucher)
				authed.Get("/coins/balance", h.handleGetCoinBalance)
				authed.Post("/promotions/preview", h.handlePreviewDiscounts)

				// Section 4.5 Checkout & Buyer Orders
				authed.Post("/checkout", h.handleCheckout)
				authed.Get("/orders", h.handleListBuyerOrders)
				authed.Get("/orders/{id}", h.handleGetOrder)
				authed.Get("/orders/{id}/timeline", h.handleGetOrderTimeline)
				authed.Post("/orders/{id}/cancel", h.handleCancelOrder)
				authed.Post("/orders/{id}/confirm-received", h.handleConfirmReceived)
				authed.Post("/orders/{id}/return", h.handleRequestReturn)
				h.mountOrderDepthAuthedRoutes(authed)

				// Section 4.6 Payments (Buyer / Admin)
				authed.Get("/payments/{id}", h.handleGetPayment)
				authed.Post("/payments/{id}/retry", h.handleRetryPayment)

				// Section 4.7 Shipping (Authenticated)
				authed.Post("/shipping/calculate-fee", h.handleCalculateShippingFee)
				authed.Get("/shipping/shipments/{order_id}", h.handleGetShipment)

				// Section 4.8 Notifications
				authed.Get("/notifications", h.handleListNotifications)
				authed.Post("/notifications/read", h.handleMarkNotificationsRead)
				authed.Get("/notifications/preferences", h.handleGetNotificationPreferences)
				authed.Put("/notifications/preferences", h.handleUpdateNotificationPreferences)

				// --- Seller routes (`seller`) ---
				authed.Route("/seller", func(seller chi.Router) {
					seller.Use(middleware.RequireRoles("seller"))
					seller.Use(middleware.RequireSellerShop())

					seller.Get("/shop", h.handleSellerGetShop)

					// Phase 2 Seller Site Management & Phase 5 Custom Domains
					seller.Get("/site", h.handleSellerGetSite)
					seller.Put("/site/draft", h.handleSellerSaveSiteDraft)
					seller.Post("/site/publish", h.handleSellerPublishSite)
					seller.Post("/site/rollback", h.handleSellerRollbackSite)
					seller.Get("/site/versions", h.handleSellerListSiteVersions)
					seller.Get("/site/domains", h.handleSellerListSiteDomains)
					seller.Post("/site/domains", h.handleSellerAddCustomDomain)
					seller.Post("/site/domains/{id}/check", h.handleSellerCheckCustomDomain)
					seller.Delete("/site/domains/{id}", h.handleSellerRemoveCustomDomain)

					// Section 4.2 Seller Products & Stock
					seller.Get("/products", h.handleSellerListProducts)
					seller.Post("/products", h.handleSellerCreateProduct)
					seller.Put("/products/{id}", h.handleSellerUpdateProduct)
					seller.Delete("/products/{id}", h.handleSellerDeleteProduct)
					seller.Patch("/skus/{sku_id}/stock", h.handleSellerUpdateStock)

					// Section 4.4 Seller Vouchers & Flash Sales
					seller.Get("/vouchers", h.handleSellerListVouchers)
					seller.Post("/vouchers", h.handleSellerCreateVoucher)
					seller.With(middleware.RateLimitFlashSale(deps.RateLimiter, deps.RateLimitFlashPerMin, deps.TrustedProxies, deps.Logger)).
						Post("/flash-sales/{id}/items", h.handleSellerRegisterFlashSaleItem)

					// Section 4.5 Seller Orders
					seller.Get("/orders", h.handleSellerListOrders)
					seller.Get("/orders/{id}", h.handleSellerGetOrder)
					seller.Post("/orders/{id}/confirm", h.handleSellerConfirmOrder)
					seller.Post("/orders/{id}/ship", h.handleSellerShipOrder)

					// Section 4.6 Seller Balance
					seller.Get("/balance", h.handleSellerBalance)
					h.mountOrderDepthSellerRoutes(seller)
				})

				// --- Admin routes (`admin`, `super_admin`) ---
				authed.Route("/admin", func(admin chi.Router) {
					admin.Use(middleware.RequireRoles("admin", "super_admin"))

					// Section 4.1 Admin Users, Shops & Audit Logs
					admin.Get("/users", h.handleAdminListUsers)
					admin.Patch("/users/{id}/status", h.handleAdminUpdateUserStatus)
					admin.Patch("/users/{id}/role", h.handleAdminUpdateUserRole)
					admin.Get("/shops", h.handleAdminListShops)
					admin.Post("/shops/{id}/review", h.handleAdminReviewShop)
					admin.Get("/sites", h.handleAdminListSites)
					admin.Get("/sites/{shop_id}", h.handleAdminGetSiteByShop)
					admin.Get("/sites/{shop_id}/domains", h.handleAdminListSiteDomains)
					admin.Delete("/sites/{shop_id}/domains/{id}", h.handleAdminRemoveCustomDomain)
					admin.Post("/sites/{site_id}/suspend", h.handleAdminSuspendSite)
					admin.Get("/audit-logs", h.handleAdminListAuditLogs)

					// Section 4.2 Admin Categories & Product Review
					admin.Post("/categories", h.handleCreateCategory)
					admin.Put("/categories/{id}", h.handleUpdateCategory)
					admin.Delete("/categories/{id}", h.handleDeleteCategory)
					admin.Get("/products", h.handleListProducts)
					admin.Post("/products/{id}/review", h.handleAdminReviewProduct)

					// Section 4.4 Admin Vouchers, Flash Sales & Coins
					admin.Post("/vouchers", h.handleAdminCreateVoucher)
					admin.Post("/flash-sales", h.handleAdminCreateFlashSale)
					admin.Post("/flash-sales/{id}/end", h.handleAdminEndFlashSaleWindow)
					admin.Post("/flash-sale-items/{item_id}/approve", h.handleAdminApproveFlashSaleItem)
					admin.Post("/coins/credit", h.handleAdminCreditCoins)

					// Section 4.5 Admin Orders
					admin.Get("/orders", h.handleAdminListOrders)

					// Section 4.6 Admin Ledger, Refund & Reconcile
					admin.Get("/ledger/summary", h.handleAdminLedgerSummary)
					admin.Post("/payments/{id}/refund", h.handleAdminRefundPayment)
					admin.Post("/payments/reconcile", h.handleAdminReconcilePayments)
					h.mountOrderDepthAdminRoutes(admin)
				})
			})
		})
	})

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, http.StatusNotFound, "NOT_FOUND", "route not found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		httpx.WriteError(w, req, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed")
	})

	return r
}
