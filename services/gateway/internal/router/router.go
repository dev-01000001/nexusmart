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
	ServiceName            string
	Clients                *clients.Clients
	AuthManager            *auth.Manager
	RevocationChecker      middleware.TokenRevocationChecker
	RateLimiter            middleware.RateLimiter
	RateLimitIPPerMin      int
	RateLimitUserPerMin    int
	RateLimitFlashPerMin   int
	RateLimitAuthPerMin    int
	RateLimitWebhookPerMin int
	TrustedProxies         *middleware.TrustedProxies
	RED                    *metrics.RED
	OpsHandler             http.Handler
	PaymentWebhookProxy    http.Handler
	ShippingWebhookProxy   http.Handler
	ShippingLabelProxy     http.Handler
	PaymentSSEProxy        http.Handler
	Logger                 *slog.Logger
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
	r.Use(middleware.SecurityHeaders())
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
		sse.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.Logger, deps.TrustedProxies))
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
				if !canAccessPayment(claims, getResp.GetPayment()) {
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
		main.Use(middleware.MaxBodyBytes(httpx.MaxBodyBytes))

		// Section 4.9 Webhooks (no JWT; HMAC verified by upstream; exact raw body & headers preserved).
		main.Group(func(wh chi.Router) {
			wh.Use(middleware.RateLimitWebhook(deps.RateLimiter, deps.RateLimitWebhookPerMin, deps.Logger, deps.TrustedProxies))
			if deps.PaymentWebhookProxy != nil {
				wh.Post("/webhooks/payment", deps.PaymentWebhookProxy.ServeHTTP)
				wh.Post("/webhooks/vietqr", deps.PaymentWebhookProxy.ServeHTTP)
			}
			if deps.ShippingWebhookProxy != nil {
				wh.Post("/webhooks/shipping/{carrier}", deps.ShippingWebhookProxy.ServeHTTP)
			}
		})

		// Public & authenticated REST API v1 routes with per-IP rate limiting.
		main.Route("/api/v1", func(api chi.Router) {
			api.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.Logger, deps.TrustedProxies))

			// --- Public routes ---
			api.Post("/auth/register", h.handleRegister)
			api.Post("/auth/verify-otp", h.handleVerifyOTP)
			api.Post("/auth/resend-otp", h.handleResendOTP)
			api.Post("/auth/login", h.handleLogin)
			api.Post("/auth/refresh", h.handleRefreshToken)

			api.Group(func(authTight chi.Router) {
				authTight.Use(middleware.RateLimitAuth(deps.RateLimiter, deps.RateLimitAuthPerMin, deps.Logger, deps.TrustedProxies))
				authTight.Post("/auth/forgot-password", h.handleForgotPassword)
				authTight.Post("/auth/reset-password", h.handleResetPassword)
			})

			api.Get("/shops", h.handleListPublicShops)
			api.Get("/shops/slug/{slug}", h.handleGetShopBySlug)
			api.Get("/shops/{id}", h.handleGetShop)
			api.Get("/categories", h.handleListCategories)
			api.Get("/products", h.handleListProducts)
			api.Get("/products/slug/{slug}", h.handleGetProductBySlug)
			api.Get("/products/{id}", h.handleGetProduct)

			api.Get("/shipping/provinces", h.handleListProvinces)
			api.Get("/shipping/provinces/{code}/wards", h.handleListWards)
			api.Post("/shipping/addresses/map-legacy", h.handleMapLegacyAddress)

			// Optional-auth vouchers endpoint (forwards JWT claims when present for saved/eligible status).
			api.Group(func(opt chi.Router) {
				opt.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
				opt.Get("/vouchers", h.handleListVouchers)
			})

			// Public flash-sales list with flash-sale rate limit if configured.
			api.Group(func(fl chi.Router) {
				fl.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
				fl.Use(middleware.RateLimitFlashSale(deps.RateLimiter, deps.RateLimitFlashPerMin, deps.Logger, deps.TrustedProxies))
				fl.Get("/flash-sales", h.handleListFlashSales)
			})
			api.Get("/shop-discounts/active", h.handleListActiveShopDiscounts)

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

				// Shop ownership & registration (buyer, seller)
				authed.Get("/shops/me", h.handleGetMyShop)
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

				// Section 4.5 Checkout & Buyer Orders / Returns
				authed.Post("/checkout", h.handleCheckout)
				authed.Get("/orders", h.handleListBuyerOrders)
				authed.Get("/orders/{id}", h.handleGetOrder)
				authed.Get("/orders/{id}/timeline", h.handleGetOrderTimeline)
				authed.Post("/orders/{id}/cancel", h.handleCancelOrder)
				authed.Post("/orders/{id}/confirm-received", h.handleConfirmReceived)
				authed.Post("/orders/{id}/return", h.handleRequestReturn)
				authed.Get("/orders/{id}/return", h.handleGetOrderReturn)
				authed.Get("/returns", h.handleListBuyerReturns)
				authed.Get("/returns/{id}", h.handleGetReturn)
				authed.Post("/returns/{id}/escalate", h.handleEscalateReturn)
				authed.Post("/returns/{id}/evidence", h.handleAddReturnEvidence)

				// Section 4.6 Payments & Refunds (Buyer / Admin)
				authed.Get("/payments/{id}", h.handleGetPayment)
				authed.Post("/payments/{id}/retry", h.handleRetryPayment)
				authed.Get("/orders/{id}/refunds", h.handleListOrderRefunds)
				authed.Post("/refunds/{id}/bank-account", h.handleSubmitRefundBankAccount)

				// Section 4.7 Shipping (Authenticated)
				authed.Post("/shipping/calculate-fee", h.handleCalculateShippingFee)
				authed.Get("/shipping/shipments/{order_id}", h.handleGetShipment)
				authed.Get("/shipping/labels/{tracking_code}", func(w http.ResponseWriter, req *http.Request) {
					if deps.ShippingLabelProxy == nil {
						httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "shipping label proxy not configured")
						return
					}
					deps.ShippingLabelProxy.ServeHTTP(w, req)
				})
				authed.Post("/orders/{id}/redelivery", h.handleRequestRedelivery)

				// Section 4.8 Notifications
				authed.Get("/notifications", h.handleListNotifications)
				authed.Post("/notifications/read", h.handleMarkNotificationsRead)
				authed.Get("/notifications/preferences", h.handleGetNotificationPreferences)
				authed.Put("/notifications/preferences", h.handleUpdateNotificationPreferences)

				// --- Seller routes (`seller`) ---
				authed.Route("/seller", func(seller chi.Router) {
					seller.Use(middleware.RequireRoles("seller"))
					seller.Use(middleware.RequireSellerShop())

					// Section 4.1 Seller Shop & Bank Account
					seller.Get("/shop", h.handleSellerGetShop)
					seller.Get("/bank-account", h.handleSellerGetBankAccount)
					seller.Post("/bank-account", h.handleSellerAddBankAccount)
					seller.Post("/bank-account/verify-otp", h.handleSellerVerifyBankAccountOTP)

					// Section 4.2 Seller Products & Stock
					seller.Get("/products", h.handleSellerListProducts)
					seller.Get("/products/{id}", h.handleSellerGetProduct)
					seller.Post("/products", h.handleSellerCreateProduct)
					seller.Put("/products/{id}", h.handleSellerUpdateProduct)
					seller.Delete("/products/{id}", h.handleSellerDeleteProduct)
					seller.Patch("/skus/{sku_id}/stock", h.handleSellerUpdateStock)

					// Section 4.4 Seller Vouchers, Flash Sales & Discount Programs
					seller.Get("/vouchers", h.handleSellerListVouchers)
					seller.Post("/vouchers", h.handleSellerCreateVoucher)
					seller.Put("/vouchers/{id}", h.handleSellerUpdateVoucher)
					seller.Patch("/vouchers/{id}/status", h.handleSetVoucherStatus)
					seller.Delete("/vouchers/{id}", h.handleDeleteVoucher)
					seller.Get("/flash-sales", h.handleListAllFlashSales)
					seller.Get("/flash-sales/items", h.handleSellerListFlashSaleItems)
					seller.With(middleware.RateLimitFlashSale(deps.RateLimiter, deps.RateLimitFlashPerMin, deps.Logger, deps.TrustedProxies)).
						Post("/flash-sales/{id}/items", h.handleSellerRegisterFlashSaleItem)
					seller.Get("/discount-programs", h.handleSellerListDiscountPrograms)
					seller.Post("/discount-programs", h.handleSellerCreateDiscountProgram)
					seller.Put("/discount-programs/{id}", h.handleSellerUpdateDiscountProgram)
					seller.Post("/discount-programs/{id}/end", h.handleSellerEndDiscountProgram)

					// Section 4.5 Seller Orders, Returns & Reports
					seller.Get("/orders", h.handleSellerListOrders)
					seller.Get("/orders/{id}", h.handleSellerGetOrder)
					seller.Post("/orders/{id}/confirm", h.handleSellerConfirmOrder)
					seller.Post("/orders/{id}/ship", h.handleSellerShipOrder)
					seller.Get("/returns", h.handleSellerListReturns)
					seller.Get("/returns/{id}", h.handleSellerGetReturn)
					seller.Post("/returns/{id}/respond", h.handleSellerRespondReturn)
					seller.Post("/returns/{id}/confirm-received", h.handleSellerConfirmReturnReceived)
					seller.Post("/returns/{id}/dispute", h.handleSellerDisputeReturnGoods)
					seller.Get("/reports", h.handleSellerReports)
					seller.Get("/reports/export", h.handleSellerReportsExport)

					// Section 4.6 Seller Balance & Ledger
					seller.Get("/balance", h.handleSellerBalance)
					seller.Get("/ledger", h.handleSellerLedger)

					// Section 4.7 Seller Shipping Settings
					seller.Get("/shipping-settings", h.handleSellerGetShippingSettings)
					seller.Put("/shipping-settings", h.handleSellerUpdateShippingSettings)
				})

				// --- Admin routes (`admin`, `super_admin`) ---
				authed.Route("/admin", func(admin chi.Router) {
					admin.Use(middleware.RequireRoles("admin", "super_admin"))
					if deps.Clients != nil && deps.Clients.User != nil {
						admin.Use(middleware.AdminAudit(middleware.AdminAuditConfig{
							UserClient:     deps.Clients.User,
							TrustedProxies: deps.TrustedProxies,
							Logger:         deps.Logger,
						}))
					}

					// Section 4.1 Admin Users, Shops, Bank Accounts & Audit Logs
					admin.Get("/users", h.handleAdminListUsers)
					admin.Patch("/users/{id}/status", h.handleAdminUpdateUserStatus)
					admin.Patch("/users/{id}/role", h.handleAdminUpdateUserRole)
					admin.Get("/shops", h.handleAdminListShops)
					admin.Post("/shops/{id}/review", h.handleAdminReviewShop)
					admin.Get("/shops/{id}/bank-account", h.handleAdminGetShopBankAccount)
					admin.Post("/shops/{id}/bank-account/review", h.handleAdminReviewShopBankAccount)
					admin.Get("/audit-logs", h.handleAdminListAuditLogs)

					// Section 4.2 Admin Categories, Fee Schedules & Product Review
					admin.Post("/categories", h.handleCreateCategory)
					admin.Put("/categories/{id}", h.handleUpdateCategory)
					admin.Delete("/categories/{id}", h.handleDeleteCategory)
					admin.Get("/fees/categories", h.handleAdminListCategoryFeeSchedules)
					admin.Post("/fees/categories/{id}", h.handleAdminSetCategoryFeeSchedule)
					admin.Get("/products", h.handleAdminListProducts)
					admin.Get("/products/{id}", h.handleAdminGetProduct)
					admin.Post("/products/{id}/review", h.handleAdminReviewProduct)

					// Section 4.4 Admin Vouchers, Flash Sales & Coins
					admin.Get("/vouchers", h.handleAdminListVouchers)
					admin.Post("/vouchers", h.handleAdminCreateVoucher)
					admin.Put("/vouchers/{id}", h.handleAdminUpdateVoucher)
					admin.Patch("/vouchers/{id}/status", h.handleSetVoucherStatus)
					admin.Delete("/vouchers/{id}", h.handleDeleteVoucher)
					admin.Get("/flash-sales", h.handleListAllFlashSales)
					admin.Post("/flash-sales", h.handleAdminCreateFlashSale)
					admin.Get("/flash-sale-items", h.handleAdminListFlashSaleItems)
					admin.Post("/flash-sale-items/{item_id}/approve", h.handleAdminApproveFlashSaleItem)
					admin.Post("/coins/credit", h.handleAdminCreditCoins)

					// Section 4.5 Admin Orders, Manual Review, Reports & Returns
					admin.Get("/orders", h.handleAdminListOrders)
					admin.Get("/orders/needs-review", h.handleAdminListOrdersNeedingReview)
					admin.Post("/orders/{id}/resolve-review", h.handleAdminResolveOrderReview)
					admin.Get("/reports", h.handleAdminReports)
					admin.Get("/reports/export", h.handleAdminReportsExport)
					admin.Get("/returns", h.handleAdminListReturns)
					admin.Get("/returns/{id}", h.handleAdminGetReturn)
					admin.Post("/returns/{id}/resolve", h.handleAdminResolveReturn)

					// Section 4.6 Admin Fee Config, Ledger, Refunds & Reconciliation
					admin.Get("/fees/payment", h.handleAdminGetPaymentFeeConfig)
					admin.Post("/fees/payment", h.handleAdminSetPaymentFeeConfig)
					admin.Get("/ledger/summary", h.handleAdminLedgerSummary)
					admin.Post("/payments/{id}/refund", h.handleAdminRefundPayment)
					admin.Get("/refunds", h.handleAdminListRefunds)
					admin.Post("/refunds/{id}/settle", h.handleAdminSettleRefund)
					admin.Post("/payments/reconcile", h.handleAdminReconcilePayments)
					admin.Get("/reconciliation/queue", h.handleAdminListReconciliationQueue)
					admin.Post("/reconciliation/queue/{id}/resolve", h.handleAdminResolveReconciliationItem)

					// Section 4.7 Admin Carriers
					admin.Get("/carriers", h.handleAdminListCarriers)
					admin.Put("/carriers/{code}", h.handleAdminUpdateCarrier)
					admin.Post("/carriers/{code}/test", h.handleAdminTestCarrier)
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
