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

type ordFixShopAllocationDTO struct {
	OrderID             string `json:"order_id"`
	ShopID              string `json:"shop_id"`
	SubtotalVND         int64  `json:"subtotal_vnd"`
	CommissionVND       int64  `json:"commission_vnd"`
	ShippingFeeVND      int64  `json:"shipping_fee_vnd"`
	PlatformDiscountVND int64  `json:"platform_discount_vnd"`
}

type ordFixPaymentDTO struct {
	PaymentID       string                    `json:"payment_id"`
	OrderGroupID    string                    `json:"order_group_id"`
	AmountVND       int64                     `json:"amount_vnd"`
	Method          string                    `json:"method"`
	Status          string                    `json:"status"`
	QRURL           string                    `json:"qr_url"`
	QRCode          string                    `json:"qr_code"`
	ProviderRef     string                    `json:"provider_ref"`
	ShopAllocations []ordFixShopAllocationDTO `json:"shop_allocations"`
}

type ordFixPaymentResponse struct {
	Payment ordFixPaymentDTO `json:"payment"`
}

func ordFixGetPaymentDetail(t *testing.T, env *testEnv, token, paymentID string) ordFixPaymentDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/payments/%s", url.PathEscape(paymentID))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out ordFixPaymentResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode payment %s: %v", paymentID, err)
	}
	return out.Payment
}

func ordFixGetShopVoucherRemainingQuota(t *testing.T, env *testEnv, token, shopID, voucherID string) int32 {
	t.Helper()
	path := fmt.Sprintf("/api/v1/vouchers?scope=SHOP&shop_id=%s", url.QueryEscape(shopID))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out VouchersListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode shop vouchers: %v", err)
	}
	for _, v := range out.Vouchers {
		if v.ID == voucherID {
			if v.RemainingQuota == 0 && v.Quota > 0 && v.UsedCount < v.Quota {
				return v.Quota - v.UsedCount
			}
			return v.RemainingQuota
		}
	}
	t.Fatalf("shop voucher %s not found in GET %s", voucherID, path)
	return -1
}

// TestE2E_OrderMoneyAndLifecycleFixes covers w1-order.md E2E scenarios (a)-(f).
func TestE2E_OrderMoneyAndLifecycleFixes(t *testing.T) {
	env := getEnv()
	cat, seller1, seller2 := env.ensureBaseMarketplace(t)

	// (a) Platform voucher + coins: allocation sent to payment carries exact R and D.
	t.Run("A_AllocationWithPlatformVoucherAndCoins", func(t *testing.T) {
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "OrderFix Alloc Product", 200000, 30, 500)
		shopV := env.createShopVoucher(t, seller1, 20000, 100000, 10)
		platV := env.createAdminVoucher(t, "PLATFORM", 30000, 100000, 10)

		buyer := env.registerVerifyAndLogin(t, "ordfix-alloc")
		addr := env.addAddress(t, buyer)
		env.creditCoins(t, buyer.UserID, 10000, "ordfix_alloc_coins")
		env.saveVoucher(t, buyer.AccessToken, shopV)
		env.saveVoucher(t, buyer.AccessToken, platV)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 0)

		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordfix-a-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:      "MARKETPLACE",
			AddressID:         addr.ID,
			Items:             []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			ShopVoucherIDs:    map[string]string{seller1.ShopID: shopV.ID},
			PlatformVoucherID: platV.ID,
			CoinsToUse:        5000,
			PaymentMethod:     "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(resp, &checkout); err != nil {
			t.Fatalf("decode checkout: %v", err)
		}
		if len(checkout.Orders) != 1 || checkout.PaymentID == "" {
			t.Fatalf("unexpected checkout response: %s", string(resp.Body))
		}
		ord := checkout.Orders[0]
		wantR := ord.SubtotalVND - ord.FlashSaleDiscountVND - ord.ShopVoucherDiscountVND
		wantD := ord.PlatformVoucherDiscountV + ord.FreeshipDiscountVND + ord.CoinsDiscountVND
		wantShip := ord.ShippingFeeVND

		pay := ordFixGetPaymentDetail(t, env, buyer.AccessToken, checkout.PaymentID)
		if len(pay.ShopAllocations) != 1 {
			t.Fatalf("expected 1 shop allocation, got %+v", pay.ShopAllocations)
		}
		al := pay.ShopAllocations[0]
		if al.SubtotalVND != wantR {
			t.Fatalf("allocation subtotal_vnd (R) = %d, want %d (payment=%+v)", al.SubtotalVND, wantR, pay)
		}
		if al.ShippingFeeVND != wantShip {
			t.Fatalf("allocation shipping_fee_vnd (Ship) = %d, want %d", al.ShippingFeeVND, wantShip)
		}
		if al.PlatformDiscountVND != wantD {
			t.Fatalf("allocation platform_discount_vnd (D) = %d, want %d (note: requires payment+gateway platform_discount_vnd support)", al.PlatformDiscountVND, wantD)
		}
		if al.CommissionVND != 0 {
			t.Fatalf("buyer GET /payments/{id} must mask allocation commission_vnd to 0, got %d", al.CommissionVND)
		}
		if gotNet := al.SubtotalVND + al.ShippingFeeVND - al.PlatformDiscountVND; gotNet != pay.AmountVND {
			t.Fatalf("R+Ship-D = %d != payment amount_vnd %d", gotNet, pay.AmountVND)
		}

		// Role-based GET /payments/{id} visibility (payment r4 / contracts §4.6):
		// admin sees full allocation + commission_vnd; owning seller sees only own shop allocation with qr/provider_ref cleared; unrelated seller -> 403.
		adminPay := ordFixGetPaymentDetail(t, env, env.adminLogin(t), checkout.PaymentID)
		if len(adminPay.ShopAllocations) != 1 || adminPay.ShopAllocations[0].CommissionVND <= 0 ||
			adminPay.ShopAllocations[0].SubtotalVND != wantR || adminPay.ShopAllocations[0].PlatformDiscountVND != wantD {
			t.Fatalf("admin GET /payments/{id} allocation mismatch: %+v", adminPay)
		}
		sellerPay := ordFixGetPaymentDetail(t, env, seller1.AccessToken, checkout.PaymentID)
		if len(sellerPay.ShopAllocations) != 1 || sellerPay.ShopAllocations[0].ShopID != seller1.ShopID ||
			sellerPay.ShopAllocations[0].CommissionVND != adminPay.ShopAllocations[0].CommissionVND {
			t.Fatalf("seller1 GET /payments/{id} allocation mismatch: got=%+v want comm=%d", sellerPay, adminPay.ShopAllocations[0].CommissionVND)
		}
		if sellerPay.QRURL != "" || sellerPay.QRCode != "" || sellerPay.ProviderRef != "" {
			t.Fatalf("non-buyer seller GET /payments/{id} must hide qr_url/qr_code/provider_ref, got qr_url=%q qr_code=%q provider_ref=%q",
				sellerPay.QRURL, sellerPay.QRCode, sellerPay.ProviderRef)
		}
		env.mustDoJSON(t, http.MethodGet, fmt.Sprintf("/api/v1/payments/%s", url.PathEscape(checkout.PaymentID)),
			seller2.AccessToken, nil, nil, http.StatusForbidden)
	})

	// (b) 0 VND order still creates a real payment (status SUCCEEDED, non-empty payment_id).
	t.Run("B_ZeroPayableOrderCreatesPayment", func(t *testing.T) {
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "OrderFix ZeroPay Product", 100000, 30, 500)
		buyer := env.registerVerifyAndLogin(t, "ordfix-zero")
		addr := env.addAddress(t, buyer)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 0)
		freeshipV := env.createAdminVoucher(t, "FREESHIP", quote.FeeVND+10000, 0, 10)
		platV := env.createAdminVoucher(t, "PLATFORM", 60000, 50000, 10)
		env.saveVoucher(t, buyer.AccessToken, freeshipV)
		env.saveVoucher(t, buyer.AccessToken, platV)
		env.creditCoins(t, buyer.UserID, 100000, "ordfix_zero_coins")

		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordfix-b-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:      "MARKETPLACE",
			AddressID:         addr.ID,
			Items:             []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PlatformVoucherID: platV.ID,
			FreeshipVoucherID: freeshipV.ID,
			CoinsToUse:        40000,
			PaymentMethod:     "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(resp, &checkout); err != nil {
			t.Fatalf("decode 0 VND checkout: %v", err)
		}
		if checkout.TotalPayableVND != 0 {
			t.Fatalf("expected 0 VND checkout, got total_payable_vnd=%d", checkout.TotalPayableVND)
		}
		if checkout.PaymentID == "" || checkout.Orders[0].PaymentID == "" {
			t.Fatalf("0 VND order MUST have real payment_id, got resp=%q order=%q", checkout.PaymentID, checkout.Orders[0].PaymentID)
		}
		pay := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
		if !StatusEquals(pay.Status, "SUCCEEDED") || pay.AmountVND != 0 {
			t.Fatalf("expected 0 VND payment SUCCEEDED/0, got status=%q amount=%d", pay.Status, pay.AmountVND)
		}
	})

	// (c) Cancel 1 sub-order in 2-shop checkout refunds that sub-order's coins and shop voucher quota.
	t.Run("C_CancelSingleSubOrderReleasesShopCoinsAndVoucher", func(t *testing.T) {
		_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "OrderFix Sub1 Product", 200000, 30, 500)
		_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "OrderFix Sub2 Product", 200000, 30, 500)
		shopV1 := env.createShopVoucher(t, seller1, 20000, 100000, 5)
		platV := env.createAdminVoucher(t, "PLATFORM", 20000, 100000, 5)

		buyer := env.registerVerifyAndLogin(t, "ordfix-subcancel")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		env.saveVoucher(t, buyer.AccessToken, shopV1)
		env.saveVoucher(t, buyer.AccessToken, platV)
		env.creditCoins(t, buyer.UserID, 10000, "ordfix_subcancel_coins")

		q1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 200000)
		q2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 500, 200000)
		shopVQuotaBefore := ordFixGetShopVoucherRemainingQuota(t, env, buyer.AccessToken, seller1.ShopID, shopV1.ID)

		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordfix-c-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel: "MARKETPLACE",
			AddressID:    addr.ID,
			Items: []CheckoutItemInputDTO{
				{SKUID: sku1.ID, Quantity: 1},
				{SKUID: sku2.ID, Quantity: 1},
			},
			ShopVoucherIDs:    map[string]string{seller1.ShopID: shopV1.ID},
			PlatformVoucherID: platV.ID,
			CoinsToUse:        10000,
			PaymentMethod:     "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: q1.CarrierCode, ServiceCode: q1.ServiceCode},
				{ShopID: seller2.ShopID, CarrierCode: q2.CarrierCode, ServiceCode: q2.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(resp, &checkout); err != nil {
			t.Fatalf("decode multi-shop COD checkout: %v", err)
		}
		if len(checkout.Orders) != 2 {
			t.Fatalf("expected 2 sub-orders, got %d", len(checkout.Orders))
		}

		var ord1 OrderDTO
		for _, o := range checkout.Orders {
			if o.ShopID == seller1.ShopID {
				ord1 = o
			}
		}
		if ord1.ID == "" || ord1.CoinsDiscountVND <= 0 {
			t.Fatalf("expected shop-1 sub-order with positive coins_discount_vnd, got %+v", ord1)
		}
		coinsAfterCheckout := env.getCoinBalance(t, buyer.AccessToken)

		// Cancel ONLY shop-1 sub-order; shop-2 stays CONFIRMED.
		cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord1.ID))
		env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, map[string]string{
			"reason": "cancel shop-1 sub-order only",
		}, http.StatusOK)

		wantCoins := coinsAfterCheckout + ord1.CoinsDiscountVND
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shop-1 coins refunded", func() (bool, string) {
			cur := env.getCoinBalance(t, buyer.AccessToken)
			return cur == wantCoins, fmt.Sprintf("got=%d want=%d", cur, wantCoins)
		})
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shop-1 voucher quota restored", func() (bool, string) {
			cur := ordFixGetShopVoucherRemainingQuota(t, env, buyer.AccessToken, seller1.ShopID, shopV1.ID)
			return cur == shopVQuotaBefore, fmt.Sprintf("got=%d want=%d", cur, shopVQuotaBefore)
		})
	})

	// (d) Retry on cancelled group -> 409; valid retry -> payment_id of ALL sub-orders updates.
	t.Run("D_RetryPaymentValidAndCancelledConflict", func(t *testing.T) {
		_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "OrderFix Retry1", 150000, 30, 500)
		_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "OrderFix Retry2", 160000, 30, 500)
		buyer := env.registerVerifyAndLogin(t, "ordfix-retry")
		addr := env.addAddress(t, buyer)
		q1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 0)
		q2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 500, 0)

		checkoutIdemKey := "ordfix-d-" + uuid.NewString()
		checkoutReq := CheckoutRequest{
			SalesChannel: "MARKETPLACE",
			AddressID:    addr.ID,
			Items: []CheckoutItemInputDTO{
				{SKUID: sku1.ID, Quantity: 1},
				{SKUID: sku2.ID, Quantity: 1},
			},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: q1.CarrierCode, ServiceCode: q1.ServiceCode},
				{ShopID: seller2.ShopID, CarrierCode: q2.CarrierCode, ServiceCode: q2.ServiceCode},
			},
		}
		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": checkoutIdemKey,
		}, checkoutReq, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(resp, &checkout); err != nil {
			t.Fatalf("decode retry checkout: %v", err)
		}
		oldPayID := checkout.PaymentID

		// Valid retry on pending 2-shop VietQR group -> new payment_id synced to BOTH sub-orders.
		retryPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(oldPayID))
		retryIdemKey := "ordfix-d-retry-" + uuid.NewString()
		retryResp := env.mustDoJSON(t, http.MethodPost, retryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": retryIdemKey,
		}, map[string]string{
			"method": "VIETQR",
		}, http.StatusOK)
		var retryOut PaymentResponse
		if err := DecodeJSON(retryResp, &retryOut); err != nil {
			t.Fatalf("decode retry response: %v", err)
		}
		newPayID := retryOut.Payment.PaymentID
		if newPayID == "" || newPayID == oldPayID {
			t.Fatalf("expected new payment_id != %q, got %q", oldPayID, newPayID)
		}
		for _, sub := range checkout.Orders {
			got := env.getOrder(t, buyer.AccessToken, sub.ID)
			if got.PaymentID != newPayID {
				t.Fatalf("sub-order %s payment_id = %q, want updated %q", sub.ID, got.PaymentID, newPayID)
			}
		}

		// Review-A P0-3(c): sequential replay with same (oldPayID, retryIdemKey) returns newPayID idempotently.
		retryReplayResp := env.mustDoJSON(t, http.MethodPost, retryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": retryIdemKey,
		}, map[string]string{
			"method": "VIETQR",
		}, http.StatusOK)
		var retryReplayOut PaymentResponse
		if err := DecodeJSON(retryReplayResp, &retryReplayOut); err != nil {
			t.Fatalf("decode retry replay response: %v", err)
		}
		if retryReplayOut.Payment.PaymentID != newPayID {
			t.Fatalf("P0-3(c): sequential retry replay returned %q, want %q", retryReplayOut.Payment.PaymentID, newPayID)
		}
		// Different key on stale oldPayID must still return 409 Conflict.
		env.mustDoJSON(t, http.MethodPost, retryPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordfix-d-retry-other-" + uuid.NewString(),
		}, map[string]string{
			"method": "VIETQR",
		}, http.StatusConflict)

		// Review-A P2-9: checkout replay with original Idempotency-Key returns updated newPayID (not stale oldPayID).
		checkoutReplayResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": checkoutIdemKey,
		}, checkoutReq, http.StatusCreated, http.StatusOK)
		var checkoutReplay CheckoutResponse
		if err := DecodeJSON(checkoutReplayResp, &checkoutReplay); err != nil {
			t.Fatalf("decode checkout replay: %v", err)
		}
		if checkoutReplay.PaymentID != newPayID {
			t.Fatalf("P2-9: checkout replay returned stale payment_id=%q, want %q", checkoutReplay.PaymentID, newPayID)
		}

		// Cancel one sub-order of the unpaid pending VietQR group -> cancels ALL pending siblings in the group (Round 3 design decision).
		cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(checkout.Orders[0].ID))
		env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, map[string]string{
			"reason": "cancel before retry test",
		}, http.StatusOK)

		for _, sub := range checkout.Orders {
			got := env.getOrder(t, buyer.AccessToken, sub.ID)
			if !StatusEquals(got.Status, "CANCELLED") {
				t.Fatalf("unpaid VietQR group cancel: sub-order %s status = %q, want CANCELLED", sub.ID, got.Status)
			}
		}

		// Attempting retry after group cancel -> MUST return 409 Conflict!
		retryCancelledPath := fmt.Sprintf("/api/v1/payments/%s/retry", url.PathEscape(newPayID))
		env.mustDoJSON(t, http.MethodPost, retryCancelledPath, buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordfix-d-retry-cancelled-" + uuid.NewString(),
		}, map[string]string{
			"method": "VIETQR",
		}, http.StatusConflict)
	})

	// (e) Same Idempotency-Key with different payload -> 409 Conflict.
	t.Run("E_SameIdempotencyKeyDifferentPayloadReturns409", func(t *testing.T) {
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "OrderFix IdemConflict", 120000, 30, 400)
		buyer := env.registerVerifyAndLogin(t, "ordfix-idem")
		addr := env.addAddress(t, buyer)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 0)
		idemKey := "ordfix-e-" + uuid.NewString()

		req1 := CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, req1, http.StatusCreated, http.StatusOK)

		// Same key, different payment_method & quantity -> MUST return 409 Conflict!
		reqDiff := CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 2}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}
		env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": idemKey,
		}, reqDiff, http.StatusConflict)
	})

	// (f) confirm-received when order is RETURN_REQUESTED -> 409 Conflict.
	t.Run("F_ConfirmReceivedWhenReturnRequestedReturns409", func(t *testing.T) {
		ctx := env.ctx(t)
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "OrderFix ReturnGuard", 140000, 30, 400)
		buyer := env.registerVerifyAndLogin(t, "ordfix-retguard")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 140000)

		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordfix-f-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(resp, &checkout); err != nil {
			t.Fatalf("decode checkout: %v", err)
		}
		orderID := checkout.Orders[0].ID

		shipCarrier := strings.ToLower(quote.CarrierCode)
		if shipCarrier == "" {
			shipCarrier = "ghn"
		}
		shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(orderID))
		shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: quote.CarrierCode,
			ServiceCode: quote.ServiceCode,
		}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil {
			t.Fatalf("decode ship response: %v", err)
		}

		webhookPath := fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(shipCarrier))
		whResp, err := env.client.PostSignedWebhook(ctx, webhookPath, ShippingWebhookPayload{
			EventID:      "ordfix-deliv-" + uuid.NewString(),
			TrackingCode: shipOut.TrackingCode,
			OrderID:      orderID,
			RawStatus:    "delivered",
			Description:  "Delivered",
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("shipping webhook delivered failed: err=%v resp=%+v", err, whResp)
		}

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order DELIVERED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, orderID)
			return StatusEquals(o.Status, "DELIVERED"), o.Status
		})

		returnPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(orderID))
		env.mustDoJSON(t, http.MethodPost, returnPath, buyer.AccessToken, nil, map[string]any{
			"reason":        "Defective item",
			"evidence_urls": []string{"https://cdn.local/proof.jpg"},
		}, http.StatusOK)

		// Now order is RETURN_REQUESTED -> confirm-received MUST return 409 Conflict!
		confirmPath := fmt.Sprintf("/api/v1/orders/%s/confirm-received", url.PathEscape(orderID))
		env.mustDoJSON(t, http.MethodPost, confirmPath, buyer.AccessToken, nil, map[string]any{}, http.StatusConflict)
	})

	// (g) Review-A P0-1 + P2-8(b): Partial refund then shipment returned -> refunds remaining balance only and reaches REFUNDED.
	t.Run("G_PartialRefundThenReturn_RefundsRemainingOnly", func(t *testing.T) {
		ctx := env.ctx(t)
		_, sku := env.createApprovedProduct(t, seller1, cat.ID, "OrderFix PartialRefRet", 200000, 30, 500)
		buyer := env.registerVerifyAndLogin(t, "ordfix-partref")
		addr := env.addAddress(t, buyer)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 500, 0)

		resp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordfix-g-" + uuid.NewString(),
		}, CheckoutRequest{
			SalesChannel:  "MARKETPLACE",
			AddressID:     addr.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)

		var checkout CheckoutResponse
		if err := DecodeJSON(resp, &checkout); err != nil {
			t.Fatalf("decode checkout: %v", err)
		}
		orderID := checkout.Orders[0].ID

		whPayResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-ORDFIX-G-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    checkout.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whPayResp.StatusCode != http.StatusOK {
			t.Fatalf("payment webhook failed: err=%v resp=%+v", err, whPayResp)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order CONFIRMED after VietQR webhook", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, orderID)
			return StatusEquals(o.Status, "CONFIRMED") && StatusEquals(o.PaymentStatus, "SUCCEEDED"),
				fmt.Sprintf("status=%s payment_status=%s", o.Status, o.PaymentStatus)
		})

		// Admin partial refund of 50,000 VND.
		adminTok := env.adminLogin(t)
		refundPath := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(checkout.PaymentID))
		env.mustDoJSON(t, http.MethodPost, refundPath, adminTok, map[string]string{
			"Idempotency-Key": "ordfix-g-part-" + uuid.NewString(),
		}, RefundRequest{
			OrderID:   orderID,
			AmountVND: 50000,
			Reason:    "E2E partial refund before return",
		}, http.StatusOK, http.StatusCreated)

		// Wait until order service consumes payment.refunded and records timeline (refunded_vnd = 50,000).
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order timeline records partial refund", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, orderID)
			for _, tl := range o.Timeline {
				if strings.Contains(tl.Note, "50000 VND") {
					return StatusEquals(o.PaymentStatus, "SUCCEEDED"), o.PaymentStatus
				}
			}
			return false, fmt.Sprintf("timeline len=%d payment_status=%s", len(o.Timeline), o.PaymentStatus)
		})

		// Ship and return the order -> order service MUST refund only remaining (TotalPayable - 50,000), reaching REFUNDED!
		shipCarrier := strings.ToLower(quote.CarrierCode)
		if shipCarrier == "" {
			shipCarrier = "ghn"
		}
		shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(orderID))
		shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller1.AccessToken, nil, SellerShipRequest{
			CarrierCode: quote.CarrierCode,
			ServiceCode: quote.ServiceCode,
		}, http.StatusOK)
		var shipOut SellerShipResponse
		if err := DecodeJSON(shipResp, &shipOut); err != nil {
			t.Fatalf("decode ship response: %v", err)
		}

		whShipResp, err := env.client.PostSignedWebhook(ctx, fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(shipCarrier)), ShippingWebhookPayload{
			EventID:      "ordfix-g-ret-" + uuid.NewString(),
			TrackingCode: shipOut.TrackingCode,
			OrderID:      orderID,
			RawStatus:    "returned",
			Description:  "Returned to seller after partial refund",
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
		if err != nil || whShipResp.StatusCode != http.StatusOK {
			t.Fatalf("shipping webhook returned failed: err=%v resp=%+v", err, whShipResp)
		}

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "returned order refunds remaining balance and reaches REFUNDED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, orderID)
			return StatusEquals(o.Status, "RETURNED") && StatusEquals(o.PaymentStatus, "REFUNDED"),
				fmt.Sprintf("status=%s payment_status=%s", o.Status, o.PaymentStatus)
		})
	})
}
