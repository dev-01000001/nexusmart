package e2e

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestE2E_SalesChannel(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	adminTok := env.adminLogin(t)

	// Isolated SKUs for shopA (seller1) and shopB (seller2).
	prodA, skuA := env.createApprovedProduct(t, seller1, cat.ID, "Storefront Linen Shirt E2E", 250000, 50, 400)
	prodB, skuB := env.createApprovedProduct(t, seller2, cat.ID, "Storefront Chino Pant E2E", 220000, 50, 450)

	// Shop vouchers for shopA & shopB, plus platform & freeship vouchers.
	shopAVoucher := env.createShopVoucher(t, seller1, 20000, 100000, 20)
	shopBVoucher := env.createShopVoucher(t, seller2, 20000, 100000, 20)
	platformVoucher := env.createAdminVoucher(t, "PLATFORM", 30000, 100000, 20)
	freeshipVoucher := env.createAdminVoucher(t, "FREESHIP", 15000, 100000, 20)

	// Single buyer shared across sales-channel scenarios to stay well inside default IP rate limits.
	buyer := env.registerVerifyAndLogin(t, "buyer-channel")
	addr := env.addAddress(t, buyer)
	env.creditCoins(t, buyer.UserID, 20000, "e2e_sales_channel_coins")
	env.saveVoucher(t, buyer.AccessToken, shopAVoucher)
	env.saveVoucher(t, buyer.AccessToken, shopBVoucher)
	env.saveVoucher(t, buyer.AccessToken, platformVoucher)
	env.saveVoucher(t, buyer.AccessToken, freeshipVoucher)

	quoteA := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 800, 500000)

	// 1. Default/legacy checkout without sales_channel reads as MARKETPLACE across buyer/seller/admin.
	t.Run("DefaultCheckout_ReadsAsMarketplace", func(t *testing.T) {
		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ch-default-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteA.CarrierCode, ServiceCode: quoteA.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var out CheckoutResponse
		if err := DecodeJSON(resp, &out); err != nil {
			t.Fatalf("decode default checkout: %v", err)
		}
		if out.SalesChannel != "MARKETPLACE" || out.StorefrontShopID != "" {
			t.Fatalf("expected default checkout sales_channel=MARKETPLACE storefront_shop_id='', got channel=%q shop=%q",
				out.SalesChannel, out.StorefrontShopID)
		}
		if len(out.Orders) != 1 {
			t.Fatalf("expected 1 order, got %d", len(out.Orders))
		}
		ord := out.Orders[0]
		assertOrderChannel(t, "checkout response", ord, "MARKETPLACE", "")
		assertOrderChannel(t, "buyer GET /orders/{id}", env.getOrder(t, buyer.AccessToken, ord.ID), "MARKETPLACE", "")

		sellerOrd := env.getSellerOrder(t, seller1.AccessToken, ord.ID)
		assertOrderChannel(t, "seller GET /seller/orders/{id}", sellerOrd, "MARKETPLACE", "")

		// B3 live check: seller JWT with empty shop_id MUST be rejected (403) on GET /api/v1/seller/orders/{id}.
		emptyShopSellerTok := MintAccessToken(env.cfg.JWTSecret, seller1.UserID, "seller")
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/seller/orders/%s", url.PathEscape(ord.ID)),
			emptyShopSellerTok, nil, nil, http.StatusForbidden)
	})

	// 2. Cart shop_id filter + STOREFRONT single-shop COD checkout + selective cart clearing +
	//    visibility (buyer/seller/admin) + immutability across full lifecycle.
	t.Run("Storefront_COD_CartFilter_SelectiveClear_And_Immutability", func(t *testing.T) {
		// Put items from BOTH shopA and shopB into the single user cart.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{
			SKUID:    skuA.ID,
			Quantity: 2,
		}, http.StatusOK)
		env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{
			SKUID:    skuB.ID,
			Quantity: 1,
		}, http.StatusOK)

		// Unfiltered cart has 2 shop groups.
		fullCartResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", buyer.AccessToken, nil, nil, http.StatusOK)
		var fullCart CartResponse
		if err := DecodeJSON(fullCartResp, &fullCart); err != nil {
			t.Fatalf("decode full cart: %v", err)
		}
		if len(fullCart.Cart.ShopGroups) != 2 || fullCart.Cart.TotalItems != 3 {
			t.Fatalf("expected 2 shop groups and 3 total_items in unfiltered cart, got groups=%d items=%d",
				len(fullCart.Cart.ShopGroups), fullCart.Cart.TotalItems)
		}

		// Filtered cart by shop_id=shopA returns ONLY shopA without mutating Redis cart.
		filteredPath := "/api/v1/cart?shop_id=" + url.QueryEscape(seller1.ShopID)
		filtResp := env.mustDoJSON(t, http.MethodGet, filteredPath, buyer.AccessToken, nil, nil, http.StatusOK)
		var filtCart CartResponse
		if err := DecodeJSON(filtResp, &filtCart); err != nil {
			t.Fatalf("decode filtered cart: %v", err)
		}
		if len(filtCart.Cart.ShopGroups) != 1 || filtCart.Cart.ShopGroups[0].ShopID != seller1.ShopID {
			t.Fatalf("expected filtered cart to have only shopA (%s), got %+v", seller1.ShopID, filtCart.Cart.ShopGroups)
		}
		if filtCart.Cart.TotalItems != 2 || filtCart.Cart.TotalVND != 500000 {
			t.Fatalf("expected filtered cart total_items=2 total_vnd=500000, got items=%d vnd=%d",
				filtCart.Cart.TotalItems, filtCart.Cart.TotalVND)
		}

		// Checkout STOREFRONT from cart for shopA with shopA SHOP voucher.
		checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-cod-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         true,
			ShopVoucherIDs:   map[string]string{seller1.ShopID: shopAVoucher.ID},
			PaymentMethod:    "COD",
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteA.CarrierCode, ServiceCode: quoteA.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(checkoutResp, &checkout); err != nil {
			t.Fatalf("decode STOREFRONT COD checkout: %v", err)
		}
		if checkout.SalesChannel != "STOREFRONT" || checkout.StorefrontShopID != seller1.ShopID {
			t.Fatalf("expected STOREFRONT checkout metadata shop=%s, got channel=%q shop=%q",
				seller1.ShopID, checkout.SalesChannel, checkout.StorefrontShopID)
		}
		if len(checkout.Orders) != 1 {
			t.Fatalf("expected 1 STOREFRONT sub-order (shopA only), got %d (body=%s)", len(checkout.Orders), string(checkoutResp.Body))
		}
		ord := checkout.Orders[0]
		if ord.ShopID != seller1.ShopID {
			t.Fatalf("expected order shop_id=%s, got %s", seller1.ShopID, ord.ShopID)
		}
		if !StatusEquals(ord.Status, "CONFIRMED") {
			t.Fatalf("expected STOREFRONT COD status CONFIRMED, got %q", ord.Status)
		}
		if ord.ShopVoucherDiscountVND != 20000 {
			t.Fatalf("expected shopA voucher discount 20000 on STOREFRONT order, got %d", ord.ShopVoucherDiscountVND)
		}
		assertOrderChannel(t, "STOREFRONT COD response", ord, "STOREFRONT", seller1.ShopID)

		// Selective cart clearing: shopB's item MUST remain untouched in the user's cart!
		afterCartResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", buyer.AccessToken, nil, nil, http.StatusOK)
		var afterCart CartResponse
		if err := DecodeJSON(afterCartResp, &afterCart); err != nil {
			t.Fatalf("decode cart after STOREFRONT checkout: %v", err)
		}
		if len(afterCart.Cart.ShopGroups) != 1 || afterCart.Cart.ShopGroups[0].ShopID != seller2.ShopID {
			t.Fatalf("expected shopB (%s) items to remain in cart after shopA STOREFRONT checkout, got %+v",
				seller2.ShopID, afterCart.Cart.ShopGroups)
		}
		if afterCart.Cart.TotalItems != 1 || len(afterCart.Cart.ShopGroups[0].Items) != 1 ||
			afterCart.Cart.ShopGroups[0].Items[0].SKUID != skuB.ID {
			t.Fatalf("expected skuB (%s, qty=1) intact in cart, got %+v", skuB.ID, afterCart.Cart)
		}
		// Clean up remaining shopB cart item so subsequent tests start clean.
		env.mustDoJSON(t, http.MethodDelete, "/api/v1/cart", buyer.AccessToken, nil, nil, http.StatusOK)

		// Verify buyer, seller, and admin order APIs all return sales_channel=STOREFRONT & storefront_shop_id=shopA.
		assertOrderChannel(t, "buyer GET /orders/{id}", env.getOrder(t, buyer.AccessToken, ord.ID), "STOREFRONT", seller1.ShopID)
		assertOrderChannel(t, "seller GET /seller/orders/{id}", env.getSellerOrder(t, seller1.AccessToken, ord.ID), "STOREFRONT", seller1.ShopID)

		// Verify buyer GET /api/v1/orders?storefront_shop_id=<uuid> filtering, pagination, isolation, and UUID validation.
		buyerSFResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders?storefront_shop_id="+url.QueryEscape(seller1.ShopID)+"&page=1&page_size=10", buyer.AccessToken, nil, nil, http.StatusOK)
		var buyerSFList OrdersListResponse
		if err := DecodeJSON(buyerSFResp, &buyerSFList); err != nil {
			t.Fatalf("decode buyer storefront_shop_id orders: %v", err)
		}
		if buyerSFList.Total < 1 || len(buyerSFList.Items) < 1 {
			t.Fatalf("expected >= 1 order for buyer filtered by storefront_shop_id=%s, got total=%d", seller1.ShopID, buyerSFList.Total)
		}
		foundInBuyerSF := false
		for _, item := range buyerSFList.Items {
			assertOrderChannel(t, "buyer storefront_shop_id filter item", item, "STOREFRONT", seller1.ShopID)
			if item.ID == ord.ID {
				foundInBuyerSF = true
			}
		}
		if !foundInBuyerSF {
			t.Fatalf("order %s missing from GET /api/v1/orders?storefront_shop_id=%s", ord.ID, seller1.ShopID)
		}
		// Filtering by another shop's storefront_shop_id must exclude shopA's order.
		buyerOtherSFResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/orders?storefront_shop_id="+url.QueryEscape(seller2.ShopID)+"&page=1&page_size=10", buyer.AccessToken, nil, nil, http.StatusOK)
		var buyerOtherSFList OrdersListResponse
		if err := DecodeJSON(buyerOtherSFResp, &buyerOtherSFList); err != nil {
			t.Fatalf("decode buyer other storefront_shop_id orders: %v", err)
		}
		for _, item := range buyerOtherSFList.Items {
			if item.ID == ord.ID {
				t.Fatalf("shopA order %s leaked into GET /api/v1/orders?storefront_shop_id=%s", ord.ID, seller2.ShopID)
			}
		}
		// Invalid storefront_shop_id UUID must return 400.
		env.mustDoJSON(t, http.MethodGet, "/api/v1/orders?storefront_shop_id=not-a-uuid", buyer.AccessToken, nil, nil, http.StatusBadRequest)

		sellerListResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/orders?sales_channel=STOREFRONT&page=1&page_size=50", seller1.AccessToken, nil, nil, http.StatusOK)
		var sellerList OrdersListResponse
		if err := DecodeJSON(sellerListResp, &sellerList); err != nil {
			t.Fatalf("decode seller list orders: %v", err)
		}
		foundInSeller := false
		for _, item := range sellerList.Items {
			if item.ID == ord.ID {
				foundInSeller = true
				assertOrderChannel(t, "seller list STOREFRONT", item, "STOREFRONT", seller1.ShopID)
			}
		}
		if !foundInSeller {
			t.Fatalf("STOREFRONT order %s not found in GET /api/v1/seller/orders?sales_channel=STOREFRONT", ord.ID)
		}

		adminSFResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/admin/orders?sales_channel=STOREFRONT&shop_id="+url.QueryEscape(seller1.ShopID)+"&page=1&page_size=50", adminTok, nil, nil, http.StatusOK)
		var adminSFList OrdersListResponse
		if err := DecodeJSON(adminSFResp, &adminSFList); err != nil {
			t.Fatalf("decode admin STOREFRONT orders: %v", err)
		}
		foundInAdmin := false
		for _, item := range adminSFList.Items {
			assertOrderChannel(t, "admin STOREFRONT filter item", item, "STOREFRONT", seller1.ShopID)
			if item.ID == ord.ID {
				foundInAdmin = true
			}
		}
		if !foundInAdmin {
			t.Fatalf("STOREFRONT order %s missing from admin orders filtered by STOREFRONT", ord.ID)
		}

		// Verify immutability across full order lifecycle: confirm -> ship -> delivered -> completed.
		confirmResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(ord.ID)), seller1.AccessToken, nil, nil, http.StatusOK)
		var confirmed OrderResponse
		if err := DecodeJSON(confirmResp, &confirmed); err != nil {
			t.Fatalf("decode confirm: %v", err)
		}
		assertOrderChannel(t, "after seller confirm", confirmed.Order, "STOREFRONT", seller1.ShopID)

		shipCarrier := strings.ToLower(quoteA.CarrierCode)
		if shipCarrier == "" {
			shipCarrier = "ghn"
		}
		shipService := quoteA.ServiceCode
		if shipService == "" {
			shipService = "standard"
		}
		shipResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord.ID)), seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: shipCarrier,
			ServiceCode: shipService,
		}, http.StatusOK)
		var shipped SellerShipResponse
		if err := DecodeJSON(shipResp, &shipped); err != nil {
			t.Fatalf("decode ship: %v", err)
		}
		assertOrderChannel(t, "after seller ship", shipped.Order, "STOREFRONT", seller1.ShopID)

		whResp, err := env.client.PostSignedWebhook(ctx, fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(shipCarrier)), ShippingWebhookPayload{
			EventID:      "e2e-sf-ship-" + uuid.NewString(),
			TrackingCode: shipped.TrackingCode,
			OrderID:      ord.ID,
			RawStatus:    "delivered",
			Description:  "Storefront order delivered",
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("shipping webhook failed: err=%v resp=%v", err, whResp)
		}

		env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "STOREFRONT order DELIVERED", func() (bool, string) {
			cur := env.getOrder(t, buyer.AccessToken, ord.ID)
			return StatusEquals(cur.Status, "DELIVERED"), fmt.Sprintf("status=%s", cur.Status)
		})

		recResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(ord.ID)), buyer.AccessToken, nil, nil, http.StatusOK)
		var completed OrderResponse
		if err := DecodeJSON(recResp, &completed); err != nil {
			t.Fatalf("decode confirm-received: %v", err)
		}
		if !StatusEquals(completed.Order.Status, "COMPLETED") {
			t.Fatalf("expected COMPLETED status, got %q", completed.Order.Status)
		}
		assertOrderChannel(t, "after buyer confirm-received (COMPLETED)", completed.Order, "STOREFRONT", seller1.ShopID)
	})

	// 3. STOREFRONT single-shop VietQR mock + HMAC webhook + partial refund + immutability.
	t.Run("Storefront_VietQRMock_Webhook_And_Refund", func(t *testing.T) {
		checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-vietqr-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			Items:            []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
			ShopVoucherIDs:   map[string]string{seller1.ShopID: shopAVoucher.ID},
			PaymentMethod:    "VIETQR",
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quoteA.CarrierCode, ServiceCode: quoteA.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(checkoutResp, &checkout); err != nil {
			t.Fatalf("decode STOREFRONT VietQR checkout: %v", err)
		}
		if len(checkout.Orders) != 1 || checkout.PaymentID == "" {
			t.Fatalf("unexpected STOREFRONT VietQR checkout response: %s", string(checkoutResp.Body))
		}
		ord := checkout.Orders[0]
		assertOrderChannel(t, "STOREFRONT VietQR checkout", ord, "STOREFRONT", seller1.ShopID)

		payBefore := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
		if payBefore.SalesChannel != "STOREFRONT" || payBefore.StorefrontShopID != seller1.ShopID {
			t.Fatalf("expected payment sales_channel=STOREFRONT storefront_shop_id=%s, got channel=%q shop=%q",
				seller1.ShopID, payBefore.SalesChannel, payBefore.StorefrontShopID)
		}
		for _, alloc := range payBefore.ShopAllocations {
			if alloc.SalesChannel != "STOREFRONT" || alloc.StorefrontShopID != seller1.ShopID {
				t.Fatalf("expected payment allocation sales_channel=STOREFRONT shop=%s, got %+v", seller1.ShopID, alloc)
			}
		}

		// Settle VietQR via HMAC-signed webhook.
		whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-SF-VIETQR-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    checkout.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("POST /webhooks/payment failed: err=%v resp=%v", err, whResp)
		}

		env.pollUntil(t, env.cfg.PollTimeout, 350*time.Millisecond, "STOREFRONT VietQR order CONFIRMED", func() (bool, string) {
			cur := env.getOrder(t, buyer.AccessToken, ord.ID)
			return StatusEquals(cur.Status, "CONFIRMED"), fmt.Sprintf("status=%s", cur.Status)
		})

		payAfter := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
		if !StatusEquals(payAfter.Status, "SUCCEEDED") {
			t.Fatalf("expected payment SUCCEEDED, got %q", payAfter.Status)
		}
		if payAfter.SalesChannel != "STOREFRONT" || payAfter.StorefrontShopID != seller1.ShopID {
			t.Fatalf("payment channel mutated after settlement: got channel=%q shop=%q",
				payAfter.SalesChannel, payAfter.StorefrontShopID)
		}
		assertOrderChannel(t, "buyer GET after VietQR settlement", env.getOrder(t, buyer.AccessToken, ord.ID), "STOREFRONT", seller1.ShopID)
		assertOrderChannel(t, "seller GET after VietQR settlement", env.getSellerOrder(t, seller1.AccessToken, ord.ID), "STOREFRONT", seller1.ShopID)

		// Partial refund on STOREFRONT VietQR payment: verifies refund ledger entries stay balanced & channel-attributed.
		refundPath := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(checkout.PaymentID))
		env.mustDoJSON(t, http.MethodPost, refundPath, adminTok, map[string]string{
			"Idempotency-Key": "sf-refund-" + uuid.NewString(),
		}, RefundRequest{
			OrderID:   ord.ID,
			AmountVND: 10000,
			Reason:    "E2E storefront partial refund",
		}, http.StatusOK, http.StatusCreated)
		assertOrderChannel(t, "buyer GET after partial refund", env.getOrder(t, buyer.AccessToken, ord.ID), "STOREFRONT", seller1.ShopID)
	})

	// 4. STOREFRONT cross-shop item rejection (sole other-shop item, mixed items, AND direct gRPC bypass) -> 0 stock change!
	t.Run("Storefront_CrossShopItemRejected_ZeroStockChange", func(t *testing.T) {
		skuABefore := env.getProductSKU(t, prodA.ID, skuA.ID)
		skuBBefore := env.getProductSKU(t, prodB.ID, skuB.ID)

		// 4a. Sole item from shopB on shopA storefront -> 400 INVALID_ARGUMENT.
		resp1 := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-xshop-1-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			Items:            []CheckoutItemInputDTO{{SKUID: skuB.ID, Quantity: 2}},
			PaymentMethod:    "COD",
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
		}, http.StatusBadRequest)
		t.Logf("Cross-shop sole item rejected cleanly: HTTP %d body=%s", resp1.StatusCode, string(resp1.Body))

		// 4b. Mixed shopA + shopB items on shopA storefront -> 400 INVALID_ARGUMENT.
		resp2 := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-xshop-2-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID: addr.ID,
			FromCart:  false,
			Items: []CheckoutItemInputDTO{
				{SKUID: skuA.ID, Quantity: 2},
				{SKUID: skuB.ID, Quantity: 1},
			},
			PaymentMethod:    "COD",
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: seller1.ShopID,
		}, http.StatusBadRequest)
		t.Logf("Cross-shop mixed items rejected cleanly: HTTP %d body=%s", resp2.StatusCode, string(resp2.Body))

		// 4c. Direct gRPC OrderService.CreateCheckout bypass check -> proves order service itself enforces it!
		conn, err := grpc.NewClient(env.cfg.OrderGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial OrderService gRPC %s: %v", env.cfg.OrderGRPCAddr, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		orderClient := orderv1.NewOrderServiceClient(conn)

		gCtx, cancel := context.WithTimeout(
			metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+buyer.AccessToken)),
			15*time.Second,
		)
		defer cancel()

		_, rpcErr := orderClient.CreateCheckout(gCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:   "sf-xshop-grpc-" + uuid.NewString(),
			BuyerId:          buyer.UserID,
			AddressId:        addr.ID,
			FromCart:         false,
			Items:            []*orderv1.CheckoutItemInput{{SkuId: skuB.ID, Quantity: 1}},
			PaymentMethod:    orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
			SalesChannel:     "STOREFRONT",
			StorefrontShopId: seller1.ShopID,
		})
		if status.Code(rpcErr) != codes.InvalidArgument {
			t.Fatalf("expected direct gRPC cross-shop STOREFRONT checkout to return InvalidArgument, got %v", rpcErr)
		}

		// Assert stock AND reserved_stock are 100% UNCHANGED on BOTH SKUs!
		skuAAfter := env.getProductSKU(t, prodA.ID, skuA.ID)
		skuBAfter := env.getProductSKU(t, prodB.ID, skuB.ID)
		assertSKUStockUnchanged(t, "skuA", skuABefore, skuAAfter)
		assertSKUStockUnchanged(t, "skuB", skuBBefore, skuBAfter)
	})

	// 5. STOREFRONT invalid/non-existent/inactive shop & unknown sales_channel rejection -> 0 stock change!
	t.Run("Storefront_InvalidAndInactiveShopRejected_ZeroStockChange", func(t *testing.T) {
		skuABefore := env.getProductSKU(t, prodA.ID, skuA.ID)

		// 5a. Empty storefront_shop_id on STOREFRONT.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-empty-shop-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
			PaymentMethod: "COD",
			SalesChannel:  "STOREFRONT",
		}, http.StatusBadRequest)

		// 5b. Non-existent storefront_shop_id.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-missing-shop-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			Items:            []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
			PaymentMethod:    "COD",
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: uuid.NewString(),
		}, http.StatusBadRequest, http.StatusNotFound)

		// 5c. Inactive (PENDING, unapproved) shop cannot be used as storefront_shop_id.
		pendingUser := env.registerVerifyAndLogin(t, "pending-shop-owner")
		pendingAddr := env.addAddress(t, pendingUser)
		pendingShopResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/shops/register", pendingUser.AccessToken, nil, RegisterShopRequest{
			Name:               "PendingShop-" + env.runID,
			Description:        "Unapproved inactive shop",
			PickupAddressID:    pendingAddr.ID,
			ContactPhone:       pendingUser.Phone,
			BusinessLicenseDoc: "https://sandbox.nexusmart.local/docs/pending.pdf",
		}, http.StatusCreated, http.StatusOK)
		var pendingShop ShopResponse
		if err := DecodeJSON(pendingShopResp, &pendingShop); err != nil {
			t.Fatalf("decode pending shop: %v", err)
		}
		if StatusEquals(pendingShop.Shop.Status, "ACTIVE") {
			t.Fatalf("expected newly registered shop to be non-ACTIVE, got %q", pendingShop.Shop.Status)
		}

		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-pending-shop-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			Items:            []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
			PaymentMethod:    "COD",
			SalesChannel:     "STOREFRONT",
			StorefrontShopID: pendingShop.Shop.ID,
		}, http.StatusBadRequest, http.StatusConflict)

		// 5d. Unknown sales_channel rejected.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-unknown-ch-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addr.ID,
			FromCart:      false,
			Items:         []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
			PaymentMethod: "COD",
			SalesChannel:  "SOCIAL_COMMERCE",
		}, http.StatusBadRequest)

		// 5e. MARKETPLACE with non-empty storefront_shop_id rejected.
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "sf-mkt-with-shop-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:        addr.ID,
			FromCart:         false,
			Items:            []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
			PaymentMethod:    "COD",
			SalesChannel:     "MARKETPLACE",
			StorefrontShopID: seller1.ShopID,
		}, http.StatusBadRequest)

		// Assert stock untouched.
		skuAAfter := env.getProductSKU(t, prodA.ID, skuA.ID)
		assertSKUStockUnchanged(t, "skuA", skuABefore, skuAAfter)
	})

	// 6. STOREFRONT rejects PLATFORM voucher, FREESHIP voucher, other-shop SHOP voucher, and platform coins!
	t.Run("Storefront_PlatformVoucher_Freeship_OtherShopVoucher_Coins_Rejected", func(t *testing.T) {
		skuABefore := env.getProductSKU(t, prodA.ID, skuA.ID)
		coinsBefore := env.getCoinBalance(t, buyer.AccessToken)
		platQuotaBefore := env.getVoucherRemainingQuota(t, buyer.AccessToken, "PLATFORM", platformVoucher.ID)
		freeQuotaBefore := env.getVoucherRemainingQuota(t, buyer.AccessToken, "FREESHIP", freeshipVoucher.ID)

		rejectCases := []struct {
			name string
			req  CheckoutRequest
		}{
			{
				name: "PLATFORM voucher on STOREFRONT",
				req: CheckoutRequest{
					AddressID:         addr.ID,
					FromCart:          false,
					Items:             []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
					PlatformVoucherID: platformVoucher.ID,
					PaymentMethod:     "COD",
					SalesChannel:      "STOREFRONT",
					StorefrontShopID:  seller1.ShopID,
				},
			},
			{
				name: "FREESHIP voucher on STOREFRONT",
				req: CheckoutRequest{
					AddressID:         addr.ID,
					FromCart:          false,
					Items:             []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
					FreeshipVoucherID: freeshipVoucher.ID,
					PaymentMethod:     "COD",
					SalesChannel:      "STOREFRONT",
					StorefrontShopID:  seller1.ShopID,
				},
			},
			{
				name: "Other-shop SHOP voucher on STOREFRONT",
				req: CheckoutRequest{
					AddressID:        addr.ID,
					FromCart:         false,
					Items:            []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
					ShopVoucherIDs:   map[string]string{seller1.ShopID: shopBVoucher.ID},
					PaymentMethod:    "COD",
					SalesChannel:     "STOREFRONT",
					StorefrontShopID: seller1.ShopID,
				},
			},
			{
				name: "Platform coins on STOREFRONT",
				req: CheckoutRequest{
					AddressID:        addr.ID,
					FromCart:         false,
					Items:            []CheckoutItemInputDTO{{SKUID: skuA.ID, Quantity: 1}},
					CoinsToUse:       5000,
					PaymentMethod:    "COD",
					SalesChannel:     "STOREFRONT",
					StorefrontShopID: seller1.ShopID,
				},
			},
		}

		for _, tc := range rejectCases {
			resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
				"Idempotency-Key": "sf-promo-rej-" + uuid.NewString(),
			}, tc.req, http.StatusBadRequest, http.StatusConflict)
			t.Logf("%s rejected cleanly: HTTP %d body=%s", tc.name, resp.StatusCode, string(resp.Body))
		}

		// Also verify Gateway GET /api/v1/vouchers?sales_channel=STOREFRONT returns only shopA SHOP vouchers
		// and Gateway POST /api/v1/promotions/preview rejects PLATFORM voucher on STOREFRONT.
		vListPath := "/api/v1/vouchers?sales_channel=STOREFRONT&storefront_shop_id=" + url.QueryEscape(seller1.ShopID)
		vListResp := env.mustDoJSON(t, http.MethodGet, vListPath, buyer.AccessToken, nil, nil, http.StatusOK)
		var sfVouchers VouchersListResponse
		if err := DecodeJSON(vListResp, &sfVouchers); err != nil {
			t.Fatalf("decode STOREFRONT vouchers: %v", err)
		}
		for _, v := range sfVouchers.Vouchers {
			if !strings.EqualFold(v.Scope, "SHOP") || v.ShopID != seller1.ShopID {
				t.Fatalf("GET %s leaked non-shopA voucher: %+v", vListPath, v)
			}
		}

		env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer.AccessToken, nil, PreviewDiscountsRequest{
			ShopItems: []ShopDiscountInputDTO{
				{
					ShopID:      seller1.ShopID,
					SubtotalVND: 250000,
					Items:       []DiscountLineItemDTO{{SKUID: skuA.ID, Quantity: 1, UnitPriceVND: 250000}},
				},
			},
			PlatformVoucherID: platformVoucher.ID,
			SalesChannel:      "STOREFRONT",
			StorefrontShopID:  seller1.ShopID,
		}, http.StatusBadRequest, http.StatusConflict)

		// Also verify PromotionService.PreviewDiscounts directly enforces STOREFRONT voucher/coin rules.
		promoConn, err := grpc.NewClient(env.cfg.PromotionGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("dial PromotionService gRPC %s: %v", env.cfg.PromotionGRPCAddr, err)
		}
		t.Cleanup(func() { _ = promoConn.Close() })
		promoClient := promotionv1.NewPromotionServiceClient(promoConn)

		pCtx, cancel := context.WithTimeout(
			metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+buyer.AccessToken)),
			15*time.Second,
		)
		defer cancel()

		_, previewErr := promoClient.PreviewDiscounts(pCtx, &promotionv1.PreviewDiscountsRequest{
			UserId: buyer.UserID,
			ShopItems: []*promotionv1.ShopDiscountInput{
				{
					ShopId:      seller1.ShopID,
					SubtotalVnd: 250000,
					Items:       []*promotionv1.DiscountLineItem{{SkuId: skuA.ID, Quantity: 1, UnitPriceVnd: 250000}},
				},
			},
			PlatformVoucherId: platformVoucher.ID,
			SalesChannel:      "STOREFRONT",
			StorefrontShopId:  seller1.ShopID,
		})
		if c := status.Code(previewErr); c != codes.InvalidArgument && c != codes.FailedPrecondition {
			t.Fatalf("expected PromotionService.PreviewDiscounts STOREFRONT+PLATFORM voucher to return InvalidArgument/FailedPrecondition, got %v", previewErr)
		}

		// Verify ZERO stock, coin, or voucher quota leakage after all rejections.
		assertSKUStockUnchanged(t, "skuA", skuABefore, env.getProductSKU(t, prodA.ID, skuA.ID))
		if got := env.getCoinBalance(t, buyer.AccessToken); got != coinsBefore {
			t.Fatalf("buyer coins changed after rejected STOREFRONT checkouts: before=%d after=%d", coinsBefore, got)
		}
		if got := env.getVoucherRemainingQuota(t, buyer.AccessToken, "PLATFORM", platformVoucher.ID); got != platQuotaBefore {
			t.Fatalf("platform voucher quota changed: before=%d after=%d", platQuotaBefore, got)
		}
		if got := env.getVoucherRemainingQuota(t, buyer.AccessToken, "FREESHIP", freeshipVoucher.ID); got != freeQuotaBefore {
			t.Fatalf("freeship voucher quota changed: before=%d after=%d", freeQuotaBefore, got)
		}
	})
}

func (e *testEnv) getSellerOrder(t *testing.T, sellerToken, orderID string) OrderDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/seller/orders/%s", url.PathEscape(orderID))
	resp := e.mustDoJSON(t, http.MethodGet, path, sellerToken, nil, nil, http.StatusOK)
	var out OrderResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode seller get order %s: %v", orderID, err)
	}
	return out.Order
}

func assertOrderChannel(t *testing.T, label string, ord OrderDTO, wantChannel, wantShopID string) {
	t.Helper()
	if ord.SalesChannel != wantChannel {
		t.Fatalf("%s: expected order %s sales_channel=%q, got %q", label, ord.ID, wantChannel, ord.SalesChannel)
	}
	if ord.StorefrontShopID != wantShopID {
		t.Fatalf("%s: expected order %s storefront_shop_id=%q, got %q", label, ord.ID, wantShopID, ord.StorefrontShopID)
	}
}

func assertSKUStockUnchanged(t *testing.T, label string, before, after SKUDTO) {
	t.Helper()
	if after.Stock != before.Stock || after.ReservedStock != before.ReservedStock || after.AvailableStock != before.AvailableStock {
		t.Fatalf("%s stock changed unexpectedly: before=(stock=%d reserved=%d avail=%d) after=(stock=%d reserved=%d avail=%d)",
			label, before.Stock, before.ReservedStock, before.AvailableStock,
			after.Stock, after.ReservedStock, after.AvailableStock)
	}
}
