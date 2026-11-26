package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestE2E_MultiShopCheckout_COD(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	// 1. Admin logs in, creates category; fetch 2-level provinces & wards.
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	t.Logf("Marketplace ready: category=%s shop1=%s shop2=%s", cat.ID, seller1.ShopID, seller2.ShopID)

	// 2. Each seller creates a product with variants/SKU; admin approves both.
	prod1, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "Mechanical Keyboard E2E", 200000, 50, 500)
	prod2, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "Wireless Mouse E2E", 300000, 50, 400)
	stock1Before := sku1.AvailableStock
	if stock1Before == 0 {
		stock1Before = sku1.Stock
	}
	stock2Before := sku2.AvailableStock
	if stock2Before == 0 {
		stock2Before = sku2.Stock
	}

	// 3. Create Shop voucher (Shop 1), Platform voucher, and Freeship voucher.
	shopVoucher := env.createShopVoucher(t, seller1, 20000, 100000, 20)
	platformVoucher := env.createAdminVoucher(t, "PLATFORM", 30000, 200000, 20)
	freeshipVoucher := env.createAdminVoucher(t, "FREESHIP", 15000, 100000, 20)

	// 4. Buyer registers -> fetches OTP from Mailpit -> verifies -> logs in -> adds 2-level address -> verifies phone for COD.
	buyer := env.registerVerifyAndLogin(t, "buyer-cod")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)

	// 5. Admin credits coins to Buyer and Buyer saves all 3 vouchers.
	creditedBalance := env.creditCoins(t, buyer.UserID, 10000, "e2e_cod_welcome")
	if creditedBalance < 10000 {
		t.Fatalf("expected buyer coin balance >= 10000 after credit, got %d", creditedBalance)
	}
	env.saveVoucher(t, buyer.AccessToken, shopVoucher)
	env.saveVoucher(t, buyer.AccessToken, platformVoucher)
	env.saveVoucher(t, buyer.AccessToken, freeshipVoucher)

	// 6. Buyer adds SKUs from BOTH shops to cart and verifies 2 shop groups.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{
		SKUID:    sku1.ID,
		Quantity: 2,
	}, http.StatusOK)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{
		SKUID:    sku2.ID,
		Quantity: 1,
	}, http.StatusOK)

	cartResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", buyer.AccessToken, nil, nil, http.StatusOK)
	var cartOut CartResponse
	if err := DecodeJSON(cartResp, &cartOut); err != nil {
		t.Fatalf("decode cart: %v", err)
	}
	if len(cartOut.Cart.ShopGroups) != 2 {
		t.Fatalf("expected cart to contain 2 shop groups, got %d (body=%s)", len(cartOut.Cart.ShopGroups), string(cartResp.Body))
	}

	// 7. Calculate shipping fees for both shops.
	quote1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 1000, 400000)
	quote2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 400, 300000)
	t.Logf("Shipping quotes: shop1=%s/%s (%d VND), shop2=%s/%s (%d VND)",
		quote1.CarrierCode, quote1.ServiceCode, quote1.FeeVND,
		quote2.CarrierCode, quote2.ServiceCode, quote2.FeeVND)

	// 8. Preview discounts (shop voucher -> platform voucher -> freeship -> coins).
	previewResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/promotions/preview", buyer.AccessToken, nil, PreviewDiscountsRequest{
		SalesChannel: "MARKETPLACE",
		ShopItems: []ShopDiscountInputDTO{
			{
				ShopID:         seller1.ShopID,
				SubtotalVND:    400000,
				ShippingFeeVND: quote1.FeeVND,
				Items:          []DiscountLineItemDTO{{SKUID: sku1.ID, Quantity: 2, UnitPriceVND: 200000}},
			},
			{
				ShopID:         seller2.ShopID,
				SubtotalVND:    300000,
				ShippingFeeVND: quote2.FeeVND,
				Items:          []DiscountLineItemDTO{{SKUID: sku2.ID, Quantity: 1, UnitPriceVND: 300000}},
			},
		},
		ShopVoucherIDs:    map[string]string{seller1.ShopID: shopVoucher.ID},
		PlatformVoucherID: platformVoucher.ID,
		FreeshipVoucherID: freeshipVoucher.ID,
		CoinsToUse:        5000,
	}, http.StatusOK)

	var previewOut PreviewDiscountsResponse
	if err := DecodeJSON(previewResp, &previewOut); err != nil {
		t.Fatalf("decode preview discounts: %v", err)
	}
	if previewOut.Breakdown.TotalShopVoucherDiscountVND <= 0 {
		t.Fatalf("expected positive shop voucher discount in preview, got %+v", previewOut.Breakdown)
	}
	if previewOut.Breakdown.TotalPlatformVoucherDiscountVN <= 0 {
		t.Fatalf("expected positive platform voucher discount in preview, got %+v", previewOut.Breakdown)
	}
	if previewOut.Breakdown.TotalFreeshipDiscountVND <= 0 {
		t.Fatalf("expected positive freeship discount in preview, got %+v", previewOut.Breakdown)
	}
	if previewOut.Breakdown.TotalCoinsDiscountVND != 5000 {
		t.Fatalf("expected coins discount 5000 in preview, got %d", previewOut.Breakdown.TotalCoinsDiscountVND)
	}
	if previewOut.Breakdown.FinalPayableVND <= 0 {
		t.Fatalf("expected positive final_payable_vnd in preview, got %d", previewOut.Breakdown.FinalPayableVND)
	}

	// 9. Multi-shop checkout with COD + Idempotency-Key.
	idemKey := "cod-e2e-" + uuid.NewString()
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": idemKey,
	}, CheckoutRequest{
		SalesChannel:      "MARKETPLACE",
		AddressID:         addr.ID,
		FromCart:          true,
		ShopVoucherIDs:    map[string]string{seller1.ShopID: shopVoucher.ID},
		PlatformVoucherID: platformVoucher.ID,
		FreeshipVoucherID: freeshipVoucher.ID,
		CoinsToUse:        5000,
		PaymentMethod:     "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote1.CarrierCode, ServiceCode: quote1.ServiceCode},
			{ShopID: seller2.ShopID, CarrierCode: quote2.CarrierCode, ServiceCode: quote2.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode COD checkout: %v", err)
	}
	if checkout.CheckoutGroupID == "" {
		t.Fatalf("checkout_group_id is empty: %s", string(checkoutResp.Body))
	}
	if checkout.IdempotentReplay {
		t.Fatalf("expected idempotent_replay=false on first COD checkout")
	}
	if len(checkout.Orders) != 2 {
		t.Fatalf("expected 1 checkout group split into 2 shop sub-orders, got %d (body=%s)", len(checkout.Orders), string(checkoutResp.Body))
	}

	var shop1Order, shop2Order OrderDTO
	for _, o := range checkout.Orders {
		if !StatusEquals(o.Status, "CONFIRMED") {
			t.Fatalf("expected COD sub-order %s status CONFIRMED, got %q", o.ID, o.Status)
		}
		if o.SalesChannel != "MARKETPLACE" {
			t.Fatalf("expected default COD sub-order %s sales_channel=MARKETPLACE, got %q", o.ID, o.SalesChannel)
		}
		if o.StorefrontShopID != "" {
			t.Fatalf("expected default COD sub-order %s storefront_shop_id empty, got %q", o.ID, o.StorefrontShopID)
		}
		switch o.ShopID {
		case seller1.ShopID:
			shop1Order = o
		case seller2.ShopID:
			shop2Order = o
		default:
			t.Fatalf("unexpected shop_id %s on sub-order %s", o.ShopID, o.ID)
		}
	}
	if shop1Order.ID == "" || shop2Order.ID == "" {
		t.Fatalf("expected distinct sub-orders for shop1 (%s) and shop2 (%s)", seller1.ShopID, seller2.ShopID)
	}

	// 10. Verify cart cleared after checkout.
	cartAfterResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/cart", buyer.AccessToken, nil, nil, http.StatusOK)
	var cartAfter CartResponse
	if err := DecodeJSON(cartAfterResp, &cartAfter); err != nil {
		t.Fatalf("decode cart after checkout: %v", err)
	}
	if len(cartAfter.Cart.ShopGroups) != 0 || cartAfter.Cart.TotalItems != 0 {
		t.Fatalf("expected cart cleared after COD checkout, got shop_groups=%d total_items=%d",
			len(cartAfter.Cart.ShopGroups), cartAfter.Cart.TotalItems)
	}

	// 11. Verify SKU stock decremented.
	sku1After := env.getProductSKU(t, prod1.ID, sku1.ID)
	sku2After := env.getProductSKU(t, prod2.ID, sku2.ID)
	if sku1After.AvailableStock != stock1Before-2 {
		t.Fatalf("expected sku1 available_stock=%d (before=%d minus 2), got %d (stock=%d reserved=%d)",
			stock1Before-2, stock1Before, sku1After.AvailableStock, sku1After.Stock, sku1After.ReservedStock)
	}
	if sku2After.AvailableStock != stock2Before-1 {
		t.Fatalf("expected sku2 available_stock=%d (before=%d minus 1), got %d (stock=%d reserved=%d)",
			stock2Before-1, stock2Before, sku2After.AvailableStock, sku2After.Stock, sku2After.ReservedStock)
	}

	// 12. Verify in-app notifications & Mailpit order email delivered.
	env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "buyer in-app order notification", func() (bool, string) {
		nResp, err := env.client.DoJSON(ctx, http.MethodGet, "/api/v1/notifications", buyer.AccessToken, nil, nil)
		if err != nil {
			return false, err.Error()
		}
		if nResp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("HTTP %d: %s", nResp.StatusCode, string(nResp.Body))
		}
		var out NotificationsListResponse
		if err := DecodeJSON(nResp, &out); err != nil {
			return false, err.Error()
		}
		for _, item := range out.Items {
			combined := strings.ToLower(item.Title + " " + item.Body)
			if strings.Contains(combined, "order") || strings.Contains(combined, "đơn") ||
				strings.Contains(combined, strings.ToLower(shop1Order.OrderCode)) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("received %d notifications, none order-related yet", len(out.Items))
	})

	if _, err := env.mailpit.WaitForEmail(ctx, buyer.Email, shop1Order.OrderCode, env.cfg.PollTimeout); err != nil {
		// Fallback check: accept any order confirmation email to buyer if order code isn't embedded verbatim.
		if _, err2 := env.mailpit.WaitForEmail(ctx, buyer.Email, "order", 5*time.Second); err2 != nil {
			t.Fatalf("expected order confirmation email in Mailpit for %s: %v (fallback: %v)", buyer.Email, err, err2)
		}
	}

	// 13. Object-level RBAC check: another buyer MUST get 403 reading this order,
	// and another seller MUST get 403 confirming this sub-order.
	otherBuyer := env.registerVerifyAndLogin(t, "buyer-intruder")
	intruderOrderPath := fmt.Sprintf("/api/v1/orders/%s", url.PathEscape(shop1Order.ID))
	env.mustDoJSON(t, http.MethodGet, intruderOrderPath, otherBuyer.AccessToken, nil, nil, http.StatusForbidden)
	env.mustDoJSON(t, http.MethodGet, intruderOrderPath+"/timeline", otherBuyer.AccessToken, nil, nil, http.StatusForbidden)

	wrongSellerConfirm := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(shop1Order.ID))
	env.mustDoJSON(t, http.MethodPost, wrongSellerConfirm, seller2.AccessToken, nil, nil, http.StatusForbidden)

	// 14. Seller 1 confirms & ships sub-order 1.
	confirmPath := fmt.Sprintf("/api/v1/seller/orders/%s/confirm", url.PathEscape(shop1Order.ID))
	confirmResp := env.mustDoJSON(t, http.MethodPost, confirmPath, seller1.AccessToken, nil, nil, http.StatusOK)
	var confirmedOrder OrderResponse
	if err := DecodeJSON(confirmResp, &confirmedOrder); err != nil {
		t.Fatalf("decode seller confirm: %v", err)
	}
	if !StatusEquals(confirmedOrder.Order.Status, "PREPARING") {
		t.Fatalf("expected status PREPARING after seller confirm, got %q", confirmedOrder.Order.Status)
	}

	shipCarrier := strings.ToLower(quote1.CarrierCode)
	if shipCarrier == "" {
		shipCarrier = "ghn"
	}
	shipService := quote1.ServiceCode
	if shipService == "" {
		shipService = "standard"
	}
	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(shop1Order.ID))
	shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller1.AccessToken, nil, SellerShipRequest{
		CarrierCode: shipCarrier,
		ServiceCode: shipService,
	}, http.StatusOK)
	var shipOut SellerShipResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil {
		t.Fatalf("decode seller ship: %v", err)
	}
	if !StatusEquals(shipOut.Order.Status, "PREPARING") {
		t.Fatalf("expected status PREPARING after seller ship (awaiting carrier pickup), got %q", shipOut.Order.Status)
	}
	if shipOut.TrackingCode == "" {
		t.Fatalf("expected non-empty tracking_code after seller ship: %s", string(shipResp.Body))
	}

	// 15. Shipping webhook (HMAC-signed) transitions PREPARING -> SHIPPING (delivering) -> DELIVERED (delivered).
	webhookPath := fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(shipCarrier))
	now := time.Now().UTC()
	for _, step := range []struct {
		rawStatus  string
		desc       string
		wantOrder  string
		occurredAt time.Time
	}{
		{"delivering", "Shipper is out for delivery", "SHIPPING", now.Add(-2 * time.Second)},
		{"delivered", "Package delivered successfully", "DELIVERED", now},
	} {
		whResp, err := env.client.PostSignedWebhook(ctx, webhookPath, ShippingWebhookPayload{
			EventID:      "e2e-ship-" + uuid.NewString(),
			TrackingCode: shipOut.TrackingCode,
			OrderID:      shop1Order.ID,
			RawStatus:    step.rawStatus,
			Description:  step.desc,
			OccurredAt:   step.occurredAt.Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("POST %s (%s) failed: %v", webhookPath, step.rawStatus, err)
		}
		if whResp.StatusCode != http.StatusOK {
			t.Fatalf("POST %s (%s) returned HTTP %d: %s", webhookPath, step.rawStatus, whResp.StatusCode, string(whResp.Body))
		}
		env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "order1 status "+step.wantOrder, func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, shop1Order.ID)
			return StatusEquals(o.Status, step.wantOrder), fmt.Sprintf("status=%s", o.Status)
		})
	}

	// Record coin balance & seller1 balance before buyer confirms received.
	coinsBeforeComplete := env.getCoinBalance(t, buyer.AccessToken)
	balRespBefore := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller1.AccessToken, nil, nil, http.StatusOK)
	var sellerBalBefore SellerBalanceResponse
	if err := DecodeJSON(balRespBefore, &sellerBalBefore); err != nil {
		t.Fatalf("decode seller balance before complete: %v", err)
	}

	// 16. Buyer confirms received -> COMPLETED, timeline intact, reward coins credited, seller balance updated.
	confirmRecPath := fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(shop1Order.ID))
	recResp := env.mustDoJSON(t, http.MethodPost, confirmRecPath, buyer.AccessToken, nil, nil, http.StatusOK)
	var recOut OrderResponse
	if err := DecodeJSON(recResp, &recOut); err != nil {
		t.Fatalf("decode confirm-received: %v", err)
	}
	if !StatusEquals(recOut.Order.Status, "COMPLETED") {
		t.Fatalf("expected status COMPLETED after confirm-received, got %q", recOut.Order.Status)
	}

	// Verify order timeline records full lifecycle progression.
	tlPath := fmt.Sprintf("/api/v1/orders/%s/timeline", url.PathEscape(shop1Order.ID))
	tlResp := env.mustDoJSON(t, http.MethodGet, tlPath, buyer.AccessToken, nil, nil, http.StatusOK)
	var tlOut OrderTimelineResponse
	if err := DecodeJSON(tlResp, &tlOut); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	seenStatuses := map[string]bool{}
	for _, ev := range tlOut.Events {
		seenStatuses[NormalizeStatus(ev.ToStatus)] = true
	}
	for _, wantStatus := range []string{"CONFIRMED", "PREPARING", "SHIPPING", "DELIVERED", "COMPLETED"} {
		if !seenStatuses[wantStatus] {
			t.Fatalf("order timeline missing status %s; got events=%+v", wantStatus, tlOut.Events)
		}
	}

	// Verify reward coins credited asynchronously (or synchronously) on completion.
	env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "buyer reward coins credited after completion", func() (bool, string) {
		after := env.getCoinBalance(t, buyer.AccessToken)
		return after > coinsBeforeComplete, fmt.Sprintf("before=%d after=%d", coinsBeforeComplete, after)
	})

	// Verify seller1 pending balance updated on completion (funds remain held in pending_vnd during hold period).
	var sellerBalAfter SellerBalanceResponse
	env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "seller1 pending balance updated after completion", func() (bool, string) {
		bResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", seller1.AccessToken, nil, nil, http.StatusOK)
		var bal SellerBalanceResponse
		if err := DecodeJSON(bResp, &bal); err != nil {
			return false, err.Error()
		}
		sellerBalAfter = bal
		updated := bal.PendingVND > sellerBalBefore.PendingVND && bal.TotalEarnedVND > sellerBalBefore.TotalEarnedVND
		return updated, fmt.Sprintf("before=%+v after=%+v", sellerBalBefore, bal)
	})
	if sellerBalAfter.AvailableVND != sellerBalBefore.AvailableVND {
		t.Fatalf("expected seller1 available_vnd unchanged (%d) during hold period, got %+v", sellerBalBefore.AvailableVND, sellerBalAfter)
	}
}
