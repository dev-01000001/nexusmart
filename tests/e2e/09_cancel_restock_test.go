package e2e

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestE2E_CancelConfirmedCODOrder_RestocksSKU(t *testing.T) {
	env := getEnv()

	cat, seller1, _ := env.ensureBaseMarketplace(t)

	const (
		initialStock = int32(20)
		orderQty     = int32(3)
	)
	prod, sku := env.createApprovedProduct(t, seller1, cat.ID, "Cancel Restock Keyboard E2E", 180000, initialStock, 600)

	buyer := env.registerVerifyAndLogin(t, "buyer-cancel-restock")
	env.verifyBuyerPhone(t, buyer)
	addr := env.addAddress(t, buyer)
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 1800, 540000)

	// 1. Snapshot SKU stock BEFORE checkout via public GET /api/v1/products/{id}.
	skuBefore := env.getProductSKU(t, prod.ID, sku.ID)
	if skuBefore.Stock != initialStock || skuBefore.AvailableStock != initialStock {
		t.Fatalf("expected initial stock=%d available=%d, got stock=%d available=%d reserved=%d",
			initialStock, initialStock, skuBefore.Stock, skuBefore.AvailableStock, skuBefore.ReservedStock)
	}

	// 2. Place single-shop COD order (starts CONFIRMED and ConfirmStock deducts SKU stock immediately).
	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "cod-cancel-restock-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     addr.ID,
		FromCart:      false,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: orderQty}},
		PaymentMethod: "COD",
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller1.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode COD checkout: %v", err)
	}
	if len(checkout.Orders) != 1 {
		t.Fatalf("expected 1 single-shop sub-order, got %d (body=%s)", len(checkout.Orders), string(checkoutResp.Body))
	}
	order := checkout.Orders[0]
	if !StatusEquals(order.Status, "CONFIRMED") {
		t.Fatalf("expected COD order status CONFIRMED, got %q", order.Status)
	}

	// 3. Verify SKU stock was actually deducted by ConfirmStock (stock decreased and reserved_stock == 0).
	skuAfterCheckout := env.getProductSKU(t, prod.ID, sku.ID)
	wantAfterCheckout := skuBefore.Stock - orderQty
	if skuAfterCheckout.Stock != wantAfterCheckout || skuAfterCheckout.AvailableStock != wantAfterCheckout || skuAfterCheckout.ReservedStock != 0 {
		t.Fatalf("expected stock=%d available=%d reserved=0 after CONFIRMED COD checkout, got stock=%d available=%d reserved=%d",
			wantAfterCheckout, wantAfterCheckout, skuAfterCheckout.Stock, skuAfterCheckout.AvailableStock, skuAfterCheckout.ReservedStock)
	}
	t.Logf("After CONFIRMED COD checkout order=%s: stock=%d (before=%d)", order.ID, skuAfterCheckout.Stock, skuBefore.Stock)

	// 4. Buyer cancels the CONFIRMED order via Gateway REST -> order calls ProductService.RestockOrderItems with service token.
	cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(order.ID))
	cancelResp := env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, struct {
		Reason string `json:"reason"`
	}{
		Reason: "e2e cancel confirmed COD order to verify restock",
	}, http.StatusOK)

	var cancelOut OrderResponse
	if err := DecodeJSON(cancelResp, &cancelOut); err != nil {
		t.Fatalf("decode cancel order response: %v", err)
	}
	if !StatusEquals(cancelOut.Order.Status, "CANCELLED") {
		t.Fatalf("expected order status CANCELLED after cancel, got %q", cancelOut.Order.Status)
	}

	// 5. Poll public GET /api/v1/products/{id} until SKU stock is restored to skuBefore.Stock.
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "SKU stock restored after cancelling CONFIRMED COD order", func() (bool, string) {
		cur := env.getProductSKU(t, prod.ID, sku.ID)
		restored := cur.Stock == skuBefore.Stock && cur.AvailableStock == skuBefore.Stock && cur.ReservedStock == 0
		return restored, fmt.Sprintf("stock=%d available=%d reserved=%d want=%d",
			cur.Stock, cur.AvailableStock, cur.ReservedStock, skuBefore.Stock)
	})

	t.Logf("Cancel restock verified for order=%s sku=%s: stock restored %d -> %d -> %d",
		order.ID, sku.ID, skuBefore.Stock, skuAfterCheckout.Stock, skuBefore.Stock)
}
