package e2e

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestE2E_PaymentFailed_SagaCompensation(t *testing.T) {
	env := getEnv()
	ctx := env.ctx(t)

	cat, seller1, _ := env.ensureBaseMarketplace(t)

	// Dedicated isolated SKU, dedicated isolated Platform Voucher (quota=5), and fresh buyer with coins.
	const (
		initialStock = int32(30)
		orderQty     = int32(3)
		initialQuota = int32(5)
		initialCoins = int64(10000)
		coinsToUse   = int64(7000)
	)
	prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "Saga Compensation Monitor E2E", 250000, initialStock, 800)
	voucher := env.createAdminVoucher(t, "PLATFORM", 25000, 100000, initialQuota)

	buyer := env.registerVerifyAndLogin(t, "buyer-saga")
	addr := env.addAddress(t, buyer)
	env.saveVoucher(t, buyer.AccessToken, voucher)
	env.creditCoins(t, buyer.UserID, initialCoins, "e2e_saga_coins")
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 2400, 0)

	// Snapshot BEFORE checkout.
	stockBefore := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
	voucherQuotaBefore := env.getVoucherRemainingQuota(t, buyer.AccessToken, "PLATFORM", voucher.ID)
	coinsBefore := env.getCoinBalance(t, buyer.AccessToken)

	if stockBefore != initialStock {
		t.Fatalf("expected initial available_stock=%d, got %d", initialStock, stockBefore)
	}
	if voucherQuotaBefore != initialQuota {
		t.Fatalf("expected initial voucher remaining_quota=%d, got %d", initialQuota, voucherQuotaBefore)
	}
	if coinsBefore != initialCoins {
		t.Fatalf("expected initial coin balance=%d, got %d", initialCoins, coinsBefore)
	}

	// Place a VIETQR order using the SKU, voucher, and coins.
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "saga-fail-" + uuid.NewString(),
	}, CheckoutRequest{
		SalesChannel:      "MARKETPLACE",
		AddressID:         addr.ID,
		FromCart:          false,
		Items:             []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: orderQty}},
		PlatformVoucherID: voucher.ID,
		CoinsToUse:        coinsToUse,
		PaymentMethod:     "VIETQR",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode saga VIETQR checkout: %v", err)
	}
	if len(checkout.Orders) != 1 || checkout.PaymentID == "" {
		t.Fatalf("unexpected checkout response: %s", string(checkoutResp.Body))
	}
	orderID := checkout.Orders[0].ID
	if !StatusEquals(checkout.Orders[0].Status, "PENDING") {
		t.Fatalf("expected VIETQR order status PENDING, got %q", checkout.Orders[0].Status)
	}

	// Assert stock, voucher quota, and coins are RESERVED immediately after checkout.
	stockReserved := env.getProductSKU(t, prod.ID, sku.ID).AvailableStock
	voucherReserved := env.getVoucherRemainingQuota(t, buyer.AccessToken, "PLATFORM", voucher.ID)
	coinsReserved := env.getCoinBalance(t, buyer.AccessToken)
	t.Logf("Reserved state: stock=%d (before=%d) voucher_quota=%d (before=%d) coins=%d (before=%d)",
		stockReserved, stockBefore, voucherReserved, voucherQuotaBefore, coinsReserved, coinsBefore)

	if stockReserved != stockBefore-orderQty {
		t.Fatalf("expected stock reserved to %d, got %d", stockBefore-orderQty, stockReserved)
	}
	if voucherReserved != voucherQuotaBefore-1 {
		t.Fatalf("expected voucher remaining_quota reserved to %d, got %d", voucherQuotaBefore-1, voucherReserved)
	}
	if coinsReserved != coinsBefore-coinsToUse {
		t.Fatalf("expected coin balance reserved to %d, got %d", coinsBefore-coinsToUse, coinsReserved)
	}

	// Trigger payment failure via HMAC-signed FAILED payment webhook.
	whResp, err := env.client.PostSignedWebhook(ctx, "/webhooks/payment", PaymentWebhookPayload{
		Provider:     "vietqr_mock",
		ProviderTxID: "TXN-FAIL-" + uuid.NewString(),
		PaymentID:    checkout.PaymentID,
		OrderGroupID: checkout.CheckoutGroupID,
		AmountVND:    checkout.TotalPayableVND,
		Status:       "FAILED",
		Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("POST /webhooks/payment (FAILED) failed: %v", err)
	}
	if whResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /webhooks/payment (FAILED) returned HTTP %d: %s", whResp.StatusCode, string(whResp.Body))
	}

	// Poll until order transitions PENDING -> CANCELLED via async saga compensation.
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order CANCELLED after payment failure", func() (bool, string) {
		o := env.getOrder(t, buyer.AccessToken, orderID)
		return StatusEquals(o.Status, "CANCELLED"), fmt.Sprintf("status=%s", o.Status)
	})

	// Assert payment status is FAILED.
	payAfter := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
	if !StatusEquals(payAfter.Status, "FAILED") {
		t.Fatalf("expected payment status FAILED, got %q", payAfter.Status)
	}

	// Assert ALL THREE resources (stock, voucher quota, buyer coins) are 100% restored.
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "SKU stock 100% restored", func() (bool, string) {
		cur := env.getProductSKU(t, prod.ID, sku.ID)
		return cur.AvailableStock == stockBefore,
			fmt.Sprintf("available=%d stock=%d reserved=%d want=%d", cur.AvailableStock, cur.Stock, cur.ReservedStock, stockBefore)
	})

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "voucher remaining_quota 100% restored", func() (bool, string) {
		cur := env.getVoucherRemainingQuota(t, buyer.AccessToken, "PLATFORM", voucher.ID)
		return cur == voucherQuotaBefore, fmt.Sprintf("remaining_quota=%d want=%d", cur, voucherQuotaBefore)
	})

	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "buyer coin balance 100% restored", func() (bool, string) {
		cur := env.getCoinBalance(t, buyer.AccessToken)
		return cur == coinsBefore, fmt.Sprintf("coins=%d want=%d", cur, coinsBefore)
	})

	t.Logf("Saga compensation verified: stock=%d voucher_quota=%d coins=%d all 100%% restored",
		stockBefore, voucherQuotaBefore, coinsBefore)
}
