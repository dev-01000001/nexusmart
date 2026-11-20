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

func TestE2E_MultiShopCheckout_VietQRMock(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	cat, seller1, seller2 := env.ensureBaseMarketplace(t)
	_, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "VietQR Monitor E2E", 250000, 30, 1200)
	_, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "VietQR Headset E2E", 180000, 30, 500)

	buyer := env.registerVerifyAndLogin(t, "buyer-vietqr")
	addr := env.addAddress(t, buyer)

	// Add items from both shops to cart.
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{
		SKUID:    sku1.ID,
		Quantity: 1,
	}, http.StatusOK)
	env.mustDoJSON(t, http.MethodPost, "/api/v1/cart/items", buyer.AccessToken, nil, AddCartItemRequest{
		SKUID:    sku2.ID,
		Quantity: 1,
	}, http.StatusOK)

	quote1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 1200, 0)
	quote2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 500, 0)

	// Checkout with VIETQR.
	idemKey := "vietqr-e2e-" + uuid.NewString()
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": idemKey,
	}, CheckoutRequest{
		AddressID:     addr.ID,
		FromCart:      true,
		PaymentMethod: "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote1.CarrierCode, ServiceCode: quote1.ServiceCode},
			{ShopID: seller2.ShopID, CarrierCode: quote2.CarrierCode, ServiceCode: quote2.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode VietQR checkout: %v", err)
	}
	if checkout.CheckoutGroupID == "" {
		t.Fatalf("expected non-empty checkout_group_id: %s", string(checkoutResp.Body))
	}
	if len(checkout.Orders) != 2 {
		t.Fatalf("expected 2 sub-orders for multi-shop VietQR checkout, got %d", len(checkout.Orders))
	}
	for _, o := range checkout.Orders {
		if !StatusEquals(o.Status, "PENDING") {
			t.Fatalf("expected VietQR sub-order %s to start PENDING, got %q", o.ID, o.Status)
		}
	}
	if checkout.PaymentID == "" {
		t.Fatalf("expected non-empty payment_id for VietQR checkout: %s", string(checkoutResp.Body))
	}
	if checkout.QRURL == "" && checkout.QRCode == "" {
		t.Fatalf("expected non-empty qr_url or qr_code for VietQR checkout: %s", string(checkoutResp.Body))
	}
	if checkout.TotalPayableVND <= 0 {
		t.Fatalf("expected positive total_payable_vnd, got %d", checkout.TotalPayableVND)
	}

	payBefore := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
	if !StatusEquals(payBefore.Status, "PENDING") {
		t.Fatalf("expected payment %s status PENDING before webhook, got %q", checkout.PaymentID, payBefore.Status)
	}

	// Send valid HMAC-signed VietQR webhook through Gateway.
	providerTxID := "TXN-VIETQR-" + uuid.NewString()
	whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: providerTxID,
		PaymentID:    checkout.PaymentID,
		OrderGroupID: checkout.CheckoutGroupID,
		AmountVND:    checkout.TotalPayableVND,
		Status:       "SUCCEEDED",
		Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("POST /webhooks/payment failed: %v", err)
	}
	if whResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /webhooks/payment returned HTTP %d: %s", whResp.StatusCode, string(whResp.Body))
	}

	// Poll until BOTH sub-orders transition PENDING -> CONFIRMED.
	for _, sub := range checkout.Orders {
		orderID := sub.ID
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "VietQR sub-order "+orderID+" CONFIRMED", func() (bool, string) {
			o := env.getOrder(t, buyer.AccessToken, orderID)
			return StatusEquals(o.Status, "CONFIRMED"), fmt.Sprintf("status=%s", o.Status)
		})
	}

	payAfter := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
	if !StatusEquals(payAfter.Status, "SUCCEEDED") {
		t.Fatalf("expected payment %s status SUCCEEDED after webhook, got %q", checkout.PaymentID, payAfter.Status)
	}

	// Verify confirmation/payment notification received by buyer.
	env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "buyer VietQR confirmation notification", func() (bool, string) {
		nResp := env.mustDoJSON(t, http.MethodGet, "/api/v1/notifications", buyer.AccessToken, nil, nil, http.StatusOK)
		var out NotificationsListResponse
		if err := DecodeJSON(nResp, &out); err != nil {
			return false, err.Error()
		}
		for _, item := range out.Items {
			text := strings.ToLower(item.Title + " " + item.Body)
			if strings.Contains(text, "payment") || strings.Contains(text, "confirmed") ||
				strings.Contains(text, "thanh toán") || strings.Contains(text, "xác nhận") {
				return true, ""
			}
		}
		return false, fmt.Sprintf("got %d notifications, waiting for payment/confirmed", len(out.Items))
	})

	// Exercise an idempotent partial refund so ledger integrity covers refund reversals as well.
	adminTok := env.adminLogin(t)
	refundKey := "refund-e2e-" + uuid.NewString()
	refundPath := fmt.Sprintf("/api/v1/admin/payments/%s/refund", url.PathEscape(checkout.PaymentID))
	refundBody := RefundRequest{
		OrderID:   checkout.Orders[0].ID,
		AmountVND: 10000,
		Reason:    "E2E partial refund ledger test",
	}

	ref1Resp := env.mustDoJSON(t, http.MethodPost, refundPath, adminTok, map[string]string{
		"Idempotency-Key": refundKey,
	}, refundBody, http.StatusOK, http.StatusCreated)
	var ref1 RefundResponse
	if err := DecodeJSON(ref1Resp, &ref1); err != nil {
		t.Fatalf("decode first refund: %v", err)
	}
	if ref1.AlreadyExisted {
		t.Fatalf("expected already_existed=false on first refund call")
	}

	ref2Resp := env.mustDoJSON(t, http.MethodPost, refundPath, adminTok, map[string]string{
		"Idempotency-Key": refundKey,
	}, refundBody, http.StatusOK, http.StatusCreated)
	var ref2 RefundResponse
	if err := DecodeJSON(ref2Resp, &ref2); err != nil {
		t.Fatalf("decode replayed refund: %v", err)
	}
	if !ref2.AlreadyExisted {
		t.Fatalf("expected already_existed=true on replayed refund with same Idempotency-Key")
	}
	if ref1.Refund.RefundID != ref2.Refund.RefundID {
		t.Fatalf("replayed refund returned different refund_id: %s vs %s", ref1.Refund.RefundID, ref2.Refund.RefundID)
	}
}
