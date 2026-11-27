package router

import (
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/metrics"
	"github.com/nexusmart/core/pkg/common/storage"
	"github.com/nexusmart/core/pkg/common/telemetry"
	"github.com/nexusmart/core/services/gateway/internal/clients"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
)

// Dependencies wires all handlers, middlewares, and reverse proxies into the gateway router.
type Dependencies struct {
	ServiceName                      string
	Clients                          *clients.Clients
	AuthManager                      *auth.Manager
	RevocationChecker                middleware.TokenRevocationChecker
	RateLimiter                      middleware.RateLimiter
	RateLimitIPPerMin                int
	RateLimitBFFPerMin               int
	RateLimitUserPerMin              int
	RateLimitFlashPerMin             int
	RateLimitAuthPerMin              int
	RateLimitBankOTPPerMin           int
	RateLimitPhoneOTPPerMin          int
	RateLimitShippingFeePerMin       int
	RateLimitWebhookPerMin           int
	RateLimitOrderLookupPerMin       int
	RateLimitOrderLookupCodePer10Min int
	ProductImportMaxRows             int
	ProductImportMaxBytes            int64
	SearchFallbackTimeout            time.Duration
	TrustedProxies                   *middleware.TrustedProxies
	RED                              *metrics.RED
	OpsHandler                       http.Handler
	PaymentWebhookProxy              http.Handler
	ShippingWebhookProxy             http.Handler
	ShippingLabelProxy               http.Handler
	PaymentSSEProxy                  http.Handler
	MediaStore                       storage.Store
	MediaStorageTimeout              time.Duration
	Logger                           *slog.Logger
}

type handler struct {
	clients                          *clients.Clients
	log                              *slog.Logger
	rateLimiter                      middleware.RateLimiter
	rateLimitOrderLookupCodePer10Min int
	productImportMaxRows             int
	productImportMaxBytes            int64
	searchFallbackTimeout            time.Duration
	mediaStore                       storage.Store
	mediaStorageTimeout              time.Duration
}

// New constructs the complete API Gateway HTTP router matching docs/contracts.md Section 4.
func New(deps Dependencies) http.Handler {
	serviceName := deps.ServiceName
	if serviceName == "" {
		serviceName = "gateway"
	}

	importMaxRows := deps.ProductImportMaxRows
	if importMaxRows <= 0 {
		importMaxRows = 1000
	}
	importMaxBytes := deps.ProductImportMaxBytes
	if importMaxBytes <= 0 {
		importMaxBytes = 1 << 20 // 1 MiB
	}
	searchTimeout := deps.SearchFallbackTimeout
	if searchTimeout <= 0 {
		searchTimeout = 1500 * time.Millisecond
	}
	mediaTimeout := deps.MediaStorageTimeout
	if mediaTimeout <= 0 {
		mediaTimeout = defaultMediaStorageTimeout
	}

	h := &handler{
		clients:                          deps.Clients,
		log:                              deps.Logger,
		rateLimiter:                      deps.RateLimiter,
		rateLimitOrderLookupCodePer10Min: deps.RateLimitOrderLookupCodePer10Min,
		productImportMaxRows:             importMaxRows,
		productImportMaxBytes:            importMaxBytes,
		searchFallbackTimeout:            searchTimeout,
		mediaStore:                       deps.MediaStore,
		mediaStorageTimeout:              mediaTimeout,
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
		sse.Use(middleware.MaxBodyBytes(httpx.MaxBodyBytes))
		sse.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.Logger, deps.TrustedProxies, deps.RateLimitBFFPerMin))
		sse.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, true))
		sse.Use(middleware.RateLimitByUser(deps.RateLimiter, deps.RateLimitUserPerMin, deps.Logger))

		sse.Get("/api/v1/payments/{id}/events", func(w http.ResponseWriter, req *http.Request) {
			if deps.PaymentSSEProxy == nil {
				httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "payment SSE proxy not configured")
				return
			}
			// Enforce object-level ownership before streaming payment events.
			claims, _ := auth.ClaimsFromContext(req.Context())
			if !canAdminReadPayment(claims) && deps.Clients != nil && deps.Clients.Payment != nil {
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

	mountChatRoutes(r, deps)
	mountAssistantRoutes(r, deps)

	// All standard REST & webhook routes share OTel trace + RED metrics + structured JSON logger.
	r.Group(func(main chi.Router) {
		main.Use(telemetry.HTTPMiddleware(serviceName))
		main.Use(middleware.Recoverer(deps.Logger))
		if deps.RED != nil {
			main.Use(deps.RED.HTTPMiddleware())
		}
		main.Use(middleware.RequestLogger(deps.Logger, deps.TrustedProxies))
		main.Use(middleware.MaxBodyBytesFunc(h.requestBodyLimit))

		// Section 4.9 Webhooks (no JWT; HMAC verified by upstream; exact raw body & headers preserved).
		main.Group(func(wh chi.Router) {
			wh.Use(middleware.RateLimitWebhook(deps.RateLimiter, deps.RateLimitWebhookPerMin, deps.Logger, deps.TrustedProxies))
			if deps.PaymentWebhookProxy != nil {
				wh.Post("/webhooks/payment", deps.PaymentWebhookProxy.ServeHTTP)
				wh.Post("/webhooks/vietqr", deps.PaymentWebhookProxy.ServeHTTP)
				wh.Post("/webhooks/zalopay", deps.PaymentWebhookProxy.ServeHTTP)
			}
			if deps.ShippingWebhookProxy != nil {
				wh.Post("/webhooks/shipping/{carrier}", deps.ShippingWebhookProxy.ServeHTTP)
			}
		})

		// Public media serving (ADR-0005): strict key regex, streamed from S3 with immutable cache headers.
		main.Group(func(media chi.Router) {
			media.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.Logger, deps.TrustedProxies, deps.RateLimitBFFPerMin))
			media.Get("/media/*", h.handleGetMedia)
		})

		// Public & authenticated REST API v1 routes with per-IP rate limiting.
		main.Route("/api/v1", func(api chi.Router) {
			api.Use(middleware.RateLimitByIP(deps.RateLimiter, deps.RateLimitIPPerMin, deps.Logger, deps.TrustedProxies, deps.RateLimitBFFPerMin))

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
			api.Get("/categories", h.handleListCategories)
			api.Get("/products", h.handleListProducts)
			api.Get("/products/slug/{slug}", h.handleGetProductBySlug)
			api.Get("/products/{id}", h.handleGetProduct)
			api.Get("/products/{id}/reviews", h.handleListProductReviews)
			api.Get("/sites", h.handleListPublishedSites)
			api.Get("/sites/by-host", h.handleGetSiteByHost)
			api.Get("/sites/by-shop/{shop_id}", h.handleGetSiteByShop)
			h.mountSiteTemplatePublicRoutes(api)

			api.Get("/shipping/provinces", h.handleListProvinces)
			api.Get("/shipping/provinces/{code}/wards", h.handleListWards)
			api.Post("/shipping/addresses/map-legacy", h.handleMapLegacyAddress)

			// Public / optional-auth shipping fee calculator (E17 / E17-bis):
			// anonymous callers rate-limited by IP; authenticated callers rate-limited by user ID.
			api.Group(func(fee chi.Router) {
				fee.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
				fee.Use(middleware.RateLimitShippingFee(deps.RateLimiter, deps.RateLimitShippingFeePerMin, deps.Logger, deps.TrustedProxies))
				fee.Post("/shipping/calculate-fee", h.handleCalculateShippingFee)
			})

			// Public guest order lookup (UC-B08 / G7): POST only with dual IP + order_code rate limits.
			// Explicitly reject GET /orders/lookup with 405 so chi never falls back to GET /orders/{id}.
			api.Get("/orders/lookup", func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Allow", http.MethodPost)
				httpx.WriteError(w, req, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "use POST /api/v1/orders/lookup")
			})
			api.Group(func(lookup chi.Router) {
				lookup.Use(middleware.RateLimitOrderLookupIP(
					deps.RateLimiter,
					deps.RateLimitOrderLookupPerMin,
					deps.Logger,
					deps.TrustedProxies,
				))
				lookup.Post("/orders/lookup", h.handleLookupOrder)
			})

			// Public search autocomplete (B1 / G13)
			api.Get("/search/suggest", h.handleSearchSuggest)

			// Optional-auth endpoints (vouchers, shops/{id} & search forward JWT claims when present; payment-methods always returns public-only fields).
			api.Group(func(opt chi.Router) {
				opt.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
				opt.Get("/vouchers", h.handleListVouchers)
				opt.Get("/payment-methods", h.handleListPublicPaymentMethods)
				opt.Get("/shops/{id}", h.handleGetShop)
				opt.Get("/search", h.handleSearchProducts)
			})

			// Public flash-sales list with flash-sale rate limit if configured.
			api.Group(func(fl chi.Router) {
				fl.Use(middleware.Auth(deps.AuthManager, deps.RevocationChecker, deps.Logger, false))
				fl.Use(middleware.RateLimitFlashSale(deps.RateLimiter, deps.RateLimitFlashPerMin, deps.Logger, deps.TrustedProxies, deps.RateLimitBFFPerMin))
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
				authed.Group(func(phoneOTP chi.Router) {
					phoneOTP.Use(middleware.RateLimitPhoneOTP(deps.RateLimiter, deps.RateLimitPhoneOTPPerMin, deps.Logger))
					phoneOTP.Post("/users/me/phone/otp", h.handleRequestPhoneOTP)
					phoneOTP.Post("/users/me/phone/verify", h.handleVerifyPhoneOTP)
				})
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
				authed.Patch("/orders/{id}/address", h.handleUpdateOrderAddress)
				authed.Get("/orders/{id}/timeline", h.handleGetOrderTimeline)
				authed.Post("/orders/{id}/cancel", h.handleCancelOrder)
				authed.Post("/orders/{id}/confirm-received", h.handleConfirmReceived)
				authed.Post("/orders/{id}/return", h.handleRequestReturn)
				authed.Get("/orders/{id}/return", h.handleGetOrderReturn)
				authed.Post("/orders/{id}/reviews", h.handleCreateOrderReview)
				authed.Get("/orders/{id}/reviews", h.handleListOrderReviews)
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
				authed.Get("/shipping/shipments/{order_id}", h.handleGetShipment)
				authed.Get("/shipping/labels/{tracking_code}", func(w http.ResponseWriter, req *http.Request) {
					if deps.ShippingLabelProxy == nil {
						httpx.WriteError(w, req, http.StatusServiceUnavailable, "UNAVAILABLE", "shipping label proxy not configured")
						return
					}
					deps.ShippingLabelProxy.ServeHTTP(w, req)
				})
				authed.Post("/shipping/labels/batch", func(w http.ResponseWriter, req *http.Request) {
					claims, _ := auth.ClaimsFromContext(req.Context())
					isSellerWithShop := auth.HasRole(claims, auth.RoleSeller) && strings.TrimSpace(claims.ShopID) != ""
					isAuthorizedStaff := auth.HasCapability(claims, auth.CapOrdersRead) || auth.HasCapability(claims, auth.CapCarriersRead)
					if !isSellerWithShop && !isAuthorizedStaff {
						httpx.WriteError(w, req, http.StatusForbidden, "PERMISSION_DENIED", "insufficient permissions to print batch shipping labels")
						return
					}
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

				// Section 4.8a Search History (B1 / G14)
				authed.Get("/search/history", h.handleListSearchHistory)
				authed.Delete("/search/history", h.handleClearSearchHistory)
				authed.Delete("/search/history/{id}", h.handleDeleteSearchHistoryItem)

				// --- Seller routes (`seller`) ---
				authed.Route("/seller", func(seller chi.Router) {
					seller.Use(middleware.RequireRoles("seller"))
					seller.Use(middleware.RequireSellerShop())

					// Section 4.1 Seller Shop, Bank Account, Sites & Custom Domains
					seller.Get("/shop", h.handleSellerGetShop)
					seller.Get("/bank-account", h.handleSellerGetBankAccount)
					seller.Group(func(bankOTP chi.Router) {
						bankOTP.Use(middleware.RateLimitBankOTP(deps.RateLimiter, deps.RateLimitBankOTPPerMin, deps.Logger))
						bankOTP.Post("/bank-account", h.handleSellerAddBankAccount)
						bankOTP.Post("/bank-account/verify-otp", h.handleSellerVerifyBankAccountOTP)
					})
					seller.Get("/site", h.handleSellerGetSite)
					seller.Put("/site/draft", h.handleSellerSaveSiteDraft)
					seller.Post("/site/publish", h.handleSellerPublishSite)
					seller.Post("/site/rollback", h.handleSellerRollbackSite)
					seller.Get("/site/versions", h.handleSellerListSiteVersions)
					seller.Get("/site/domains", h.handleSellerListSiteDomains)
					seller.Post("/site/domains", h.handleSellerAddCustomDomain)
					seller.Post("/site/domains/{id}/check", h.handleSellerCheckCustomDomain)
					seller.Delete("/site/domains/{id}", h.handleSellerRemoveCustomDomain)
					h.mountSiteTemplateSellerRoutes(seller)

					// Section 4.2 Seller Categories, Products & Stock
					seller.Post("/media", h.handleSellerUploadMedia)
					seller.Get("/categories", h.handleListCategoriesWithCommission)
					seller.Get("/products", h.handleSellerListProducts)
					seller.Get("/products/{id}", h.handleSellerGetProduct)
					seller.Post("/products", h.handleSellerCreateProduct)
					seller.Put("/products/{id}", h.handleSellerUpdateProduct)
					seller.Delete("/products/{id}", h.handleSellerDeleteProduct)
					seller.Patch("/skus/{sku_id}/stock", h.handleSellerUpdateStock)
					seller.Get("/stock-history", h.handleSellerListStockHistory)
					seller.Get("/skus/{sku_id}/stock-history", h.handleSellerListSKUStockHistory)
					seller.Get("/products/import/template", h.handleSellerProductImportTemplate)
					seller.Post("/products/import", h.handleSellerCreateProductImport)
					seller.Get("/products/import/{job_id}", h.handleSellerGetProductImportJob)
					seller.Get("/products/import/{job_id}/rows", h.handleSellerListProductImportRows)
					seller.Get("/products/import/{job_id}/errors.csv", h.handleSellerExportProductImportErrorsCSV)
					seller.Post("/products/import/{job_id}/confirm", h.handleSellerConfirmProductImportJob)

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
					seller.Post("/orders/batch-ship", h.handleSellerBatchShipOrders)
					seller.Post("/orders/picking-list", h.handleSellerPickingList)
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

					// Section 4.6 Seller Balance, Ledger & Payouts
					seller.Get("/balance", h.handleSellerBalance)
					seller.Get("/ledger", h.handleSellerLedger)
					seller.Get("/payouts", h.handleSellerListPayouts)
					seller.Post("/payouts", h.handleSellerRequestPayout)

					// Section 4.7 Seller Shipping Settings
					seller.Get("/shipping-settings", h.handleSellerGetShippingSettings)
					seller.Put("/shipping-settings", h.handleSellerUpdateShippingSettings)
				})

				// --- Admin / Internal Staff routes (capability matrix per pkg/common/auth/capabilities.json, F9) ---
				authed.Route("/admin", func(admin chi.Router) {
					admin.Use(middleware.RequireRoles(auth.StaffRoles()...))
					if deps.Clients != nil && deps.Clients.User != nil {
						admin.Use(middleware.AdminAudit(middleware.AdminAuditConfig{
							UserClient:     deps.Clients.User,
							TrustedProxies: deps.TrustedProxies,
							Logger:         deps.Logger,
						}))
					}

					// Section 4.1 Admin Users, Staff, Shops, Sites, Approvals, Bank Accounts & Audit Logs
					admin.With(middleware.RequireCapability(auth.CapUsersRead)).Get("/users", h.handleAdminListUsers)
					admin.With(middleware.RequireCapability(auth.CapStaffCreate)).Post("/staff", h.handleAdminCreateStaffUser)
					admin.With(middleware.RequireCapability(auth.CapUsersRead)).Get("/users/{id}/activities", h.handleAdminListUserActivities)
					admin.With(middleware.RequireCapability(auth.CapUsersStatusWrite)).Patch("/users/{id}/status", h.handleAdminUpdateUserStatus)
					admin.With(middleware.RequireCapability(auth.CapUsersRoleWrite)).Patch("/users/{id}/role", h.handleAdminUpdateUserRole)
					admin.With(middleware.RequireCapability(auth.CapShopsRead)).Get("/shops", h.handleAdminListShops)
					admin.With(middleware.RequireCapability(auth.CapShopsReview)).Post("/shops/{id}/review", h.handleAdminReviewShop)
					admin.With(middleware.RequireCapability(auth.CapShopsSuspendRequest)).Post("/shops/{id}/suspend", h.handleAdminSuspendShop)
					admin.With(middleware.RequireCapability(auth.CapShopsUnsuspend)).Post("/shops/{id}/unsuspend", h.handleAdminUnsuspendShop)
					admin.With(middleware.RequireCapability(auth.CapShopsBankRead)).Get("/shops/{id}/bank-account", h.handleAdminGetShopBankAccount)
					admin.With(middleware.RequireCapability(auth.CapShopsBankReview)).Post("/shops/{id}/bank-account/review", h.handleAdminReviewShopBankAccount)
					admin.With(middleware.RequireCapability(auth.CapSitesRead)).Get("/sites", h.handleAdminListSites)
					admin.With(middleware.RequireCapability(auth.CapSitesRead)).Get("/sites/{shop_id}", h.handleAdminGetSiteByShop)
					admin.With(middleware.RequireCapability(auth.CapSitesRead)).Get("/sites/{shop_id}/domains", h.handleAdminListSiteDomains)
					admin.With(middleware.RequireCapability(auth.CapSitesDomainsWrite)).Delete("/sites/{shop_id}/domains/{id}", h.handleAdminRemoveCustomDomain)
					admin.With(middleware.RequireCapability(auth.CapSitesSuspend)).Post("/sites/{site_id}/suspend", h.handleAdminSuspendSite)
					admin.With(middleware.RequireCapability(auth.CapApprovalsRead)).Get("/approvals", h.handleAdminListApprovals)
					admin.With(middleware.RequireCapability(auth.CapShopsSuspendDecide)).Post("/approvals/shop/{id}/decide", h.handleAdminDecideShopSuspension)
					admin.With(middleware.RequireCapability(auth.CapRefundsApprove)).Post("/approvals/refund/{id}/decide", h.handleAdminDecideRefundApproval)
					admin.With(middleware.RequireCapability(auth.CapAuditLogsRead)).Get("/audit-logs", h.handleAdminListAuditLogs)

					// Section 4.2 Admin Categories, Fee Schedules, Products & Banned Keywords
					admin.With(middleware.RequireCapability(auth.CapCategoriesRead)).Get("/categories", h.handleListCategoriesWithCommission)
					admin.With(middleware.RequireCapability(auth.CapCategoriesWrite)).Post("/categories", h.handleCreateCategory)
					admin.With(middleware.RequireCapability(auth.CapCategoriesWrite)).Put("/categories/{id}", h.handleUpdateCategory)
					admin.With(middleware.RequireCapability(auth.CapCategoriesWrite)).Delete("/categories/{id}", h.handleDeleteCategory)
					admin.With(middleware.RequireCapability(auth.CapFeesCategoryRead)).Get("/fees/categories", h.handleAdminListCategoryFeeSchedules)
					admin.With(middleware.RequireCapability(auth.CapFeesCategoryWrite)).Post("/fees/categories/{id}", h.handleAdminSetCategoryFeeSchedule)
					admin.With(middleware.RequireCapability(auth.CapProductsRead)).Get("/products", h.handleAdminListProducts)
					admin.With(middleware.RequireCapability(auth.CapProductsRead)).Get("/products/{id}", h.handleAdminGetProduct)
					admin.With(middleware.RequireCapability(auth.CapProductsReview)).Post("/products/{id}/review", h.handleAdminReviewProduct)
					admin.With(middleware.RequireCapability(auth.CapBannedKeywordsRead)).Get("/banned-keywords", h.handleAdminListBannedKeywords)
					admin.With(middleware.RequireCapability(auth.CapBannedKeywordsWrite)).Post("/banned-keywords", h.handleAdminCreateBannedKeyword)
					admin.With(middleware.RequireCapability(auth.CapBannedKeywordsWrite)).Put("/banned-keywords/{id}", h.handleAdminUpdateBannedKeyword)
					admin.With(middleware.RequireCapability(auth.CapBannedKeywordsWrite)).Delete("/banned-keywords/{id}", h.handleAdminDeleteBannedKeyword)
					admin.With(middleware.RequireCapability(auth.CapBannedKeywordsRead)).Post("/banned-keywords/preview", h.handleAdminPreviewBannedKeyword)
					admin.With(middleware.RequireCapability(auth.CapBannedKeywordsWrite)).Post("/banned-keywords/rescan", h.handleAdminRescanBannedContent)

					// Section 4.4 Admin Vouchers, Flash Sales & Coins
					admin.With(middleware.RequireCapability(auth.CapVouchersRead)).Get("/vouchers", h.handleAdminListVouchers)
					admin.With(middleware.RequireCapability(auth.CapVouchersWrite)).Post("/vouchers", h.handleAdminCreateVoucher)
					admin.With(middleware.RequireCapability(auth.CapVouchersWrite)).Put("/vouchers/{id}", h.handleAdminUpdateVoucher)
					admin.With(middleware.RequireCapability(auth.CapVouchersWrite)).Patch("/vouchers/{id}/status", h.handleSetVoucherStatus)
					admin.With(middleware.RequireCapability(auth.CapVouchersWrite)).Delete("/vouchers/{id}", h.handleDeleteVoucher)
					admin.With(middleware.RequireCapability(auth.CapFlashSalesRead)).Get("/flash-sales", h.handleListAllFlashSales)
					admin.With(middleware.RequireCapability(auth.CapFlashSalesWrite)).Post("/flash-sales", h.handleAdminCreateFlashSale)
					admin.With(middleware.RequireCapability(auth.CapFlashSalesWrite)).Post("/flash-sales/{id}/end", h.handleAdminEndFlashSaleWindow)
					admin.With(middleware.RequireCapability(auth.CapFlashSalesRead)).Get("/flash-sale-items", h.handleAdminListFlashSaleItems)
					admin.With(middleware.RequireCapability(auth.CapFlashSalesItemApprove)).Post("/flash-sale-items/{item_id}/approve", h.handleAdminApproveFlashSaleItem)
					admin.With(middleware.RequireCapability(auth.CapCoinsCredit)).Post("/coins/credit", h.handleAdminCreditCoins)

					// Section 4.5 Admin Orders, Manual Review, Reports & Returns
					admin.With(middleware.RequireCapability(auth.CapOrdersRead)).Get("/orders", h.handleAdminListOrders)
					admin.With(middleware.RequireCapability(auth.CapOrdersRead)).Get("/orders/needs-review", h.handleAdminListOrdersNeedingReview)
					admin.With(middleware.RequireCapability(auth.CapOrdersReviewResolve)).Post("/orders/{id}/resolve-review", h.handleAdminResolveOrderReview)
					admin.With(middleware.RequireCapability(auth.CapReportsRead)).Get("/reports", h.handleAdminReports)
					admin.With(middleware.RequireCapability(auth.CapReportsRead)).Get("/reports/export", h.handleAdminReportsExport)
					admin.With(middleware.RequireCapability(auth.CapReturnsRead)).Get("/returns", h.handleAdminListReturns)
					admin.With(middleware.RequireCapability(auth.CapReturnsRead)).Get("/returns/{id}", h.handleAdminGetReturn)
					admin.With(middleware.RequireCapability(auth.CapReturnsResolve)).Post("/returns/{id}/resolve", h.handleAdminResolveReturn)

					// Section 4.6 Admin Payment Methods, Fee Config, Ledger, Refunds & Reconciliation
					admin.With(middleware.RequireCapability(auth.CapPaymentMethodsRead)).Get("/payment-methods", h.handleAdminListPaymentMethods)
					admin.With(middleware.RequireCapability(auth.CapPaymentMethodsWrite)).Put("/payment-methods/{method}", h.handleAdminUpdatePaymentMethodConfig)
					admin.With(middleware.RequireCapability(auth.CapFeesPaymentRead)).Get("/fees/payment", h.handleAdminGetPaymentFeeConfig)
					admin.With(middleware.RequireCapability(auth.CapFeesPaymentWrite)).Post("/fees/payment", h.handleAdminSetPaymentFeeConfig)
					admin.With(middleware.RequireCapability(auth.CapLedgerRead)).Get("/ledger/summary", h.handleAdminLedgerSummary)
					admin.With(middleware.RequireCapability(auth.CapRefundsCreate)).Post("/payments/{id}/refund", h.handleAdminRefundPayment)
					admin.With(middleware.RequireCapability(auth.CapRefundsRead)).Get("/refunds", h.handleAdminListRefunds)
					admin.With(middleware.RequireCapability(auth.CapRefundsSettle)).Post("/refunds/{id}/settle", h.handleAdminSettleRefund)
					admin.With(middleware.RequireCapability(auth.CapPayoutsRead)).Get("/payouts", h.handleAdminListPayouts)
					admin.With(middleware.RequireCapability(auth.CapPayoutsDecide)).Post("/payouts/{id}/decision", h.handleAdminDecidePayout)
					admin.With(middleware.RequireCapability(auth.CapEscrowRelease)).Post("/orders/{id}/escrow/release", h.handleAdminReleaseEscrow)
					admin.With(middleware.RequireCapability(auth.CapReconciliationWrite)).Post("/payments/reconcile", h.handleAdminReconcilePayments)
					admin.With(middleware.RequireCapability(auth.CapReconciliationWrite)).Post("/reconciliation/cod/import", h.handleAdminImportCodReconciliation)
					admin.With(middleware.RequireCapability(auth.CapLedgerRead)).Get("/reconciliation/runs", h.handleAdminListReconciliationRuns)
					admin.With(middleware.RequireCapability(auth.CapLedgerRead)).Get("/reconciliation/queue", h.handleAdminListReconciliationQueue)
					admin.With(middleware.RequireCapability(auth.CapReconciliationWrite)).Post("/reconciliation/queue/{id}/resolve", h.handleAdminResolveReconciliationItem)

					// Section 4.7 Admin Carriers
					admin.With(middleware.RequireCapability(auth.CapCarriersRead)).Get("/carriers", h.handleAdminListCarriers)
					admin.With(middleware.RequireCapability(auth.CapCarriersWrite)).Put("/carriers/{code}", h.handleAdminUpdateCarrier)
					admin.With(middleware.RequireCapability(auth.CapCarriersWrite)).Post("/carriers/{code}/test", h.handleAdminTestCarrier)

					// Section 4.8a Admin Search Reindex (B1 / G11)
					admin.With(middleware.RequireCapability(auth.CapSearchReindex)).Post("/search/reindex", h.handleAdminSearchReindex)
					admin.With(middleware.RequireCapability(auth.CapSearchReindex)).Get("/search/reindex/{job_id}", h.handleAdminGetSearchReindexStatus)
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

const (
	codImportRoutePath              = "/api/v1/admin/reconciliation/cod/import"
	productImportRoutePath          = "/api/v1/seller/products/import"
	sellerMediaRoutePath            = "/api/v1/seller/media"
	codImportMultipartOverheadBytes = 64 << 10 // 64 KiB multipart framing overhead for CSV uploads
)

func (h *handler) requestBodyLimit(r *http.Request) int64 {
	if r.Method != http.MethodPost {
		return httpx.MaxBodyBytes
	}
	isMultipart := false
	if ct := strings.TrimSpace(r.Header.Get("Content-Type")); ct != "" {
		if mt, _, err := mime.ParseMediaType(ct); err == nil && strings.EqualFold(strings.TrimSpace(mt), "multipart/form-data") {
			isMultipart = true
		}
	}
	switch r.URL.Path {
	case sellerMediaRoutePath:
		return mediaMaxUploadBytes
	case codImportRoutePath:
		if isMultipart {
			return httpx.MaxBodyBytes + codImportMultipartOverheadBytes
		}
	case productImportRoutePath:
		limit := h.productImportMaxBytes
		if limit <= 0 {
			limit = 1 << 20
		}
		if isMultipart {
			return limit + codImportMultipartOverheadBytes
		}
		return limit
	}
	return httpx.MaxBodyBytes
}
