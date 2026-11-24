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

type ordW2OrderDTO struct {
	ID              string         `json:"id"`
	OrderCode       string         `json:"order_code"`
	CheckoutGroupID string         `json:"checkout_group_id"`
	Status          string         `json:"status"`
	PaymentMethod   string         `json:"payment_method"`
	PaymentID       string         `json:"payment_id"`
	PaymentStatus   string         `json:"payment_status"`
	SubtotalVND     int64          `json:"subtotal_vnd"`
	CommissionVND   int64          `json:"commission_vnd"`
	PaymentFeeVND   int64          `json:"payment_fee_vnd"`
	PaymentFeeBps   int32          `json:"payment_fee_bps"`
	TotalPayableVND int64          `json:"total_payable_vnd"`
	Items           []OrderItemDTO `json:"items"`
	CarrierCode     string         `json:"carrier_code"`
	TrackingCode    string         `json:"tracking_code"`
	DeliveredAt     string         `json:"delivered_at"`
	ReturnRequestID string         `json:"return_request_id"`
	ReturnStatus    string         `json:"return_status"`
}

type ordW2OrderResponse struct {
	Order ordW2OrderDTO `json:"order"`
}

type ordW2ReturnEventDTO struct {
	ID         string `json:"id"`
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	ActorRole  string `json:"actor_role"`
	Note       string `json:"note"`
}

type ordW2ReturnRequestDTO struct {
	ID                 string                `json:"id"`
	OrderID            string                `json:"order_id"`
	BuyerID            string                `json:"buyer_id"`
	ShopID             string                `json:"shop_id"`
	Status             string                `json:"status"`
	Reason             string                `json:"reason"`
	SellerResponse     string                `json:"seller_response"`
	DisputeReason      string                `json:"dispute_reason"`
	AdminNote          string                `json:"admin_note"`
	RefundAmountVND    int64                 `json:"refund_amount_vnd"`
	ReturnTrackingCode string                `json:"return_tracking_code"`
	ReturnLabelURL     string                `json:"return_label_url"`
	ReturnFeeVND       int64                 `json:"return_fee_vnd"`
	ReturnFeeBearer    string                `json:"return_fee_bearer"`
	GoodsReceived      bool                  `json:"goods_received"`
	DeadlineAt         string                `json:"deadline_at"`
	Events             []ordW2ReturnEventDTO `json:"events"`
}

type ordW2ReturnResponse struct {
	ReturnRequest ordW2ReturnRequestDTO `json:"return_request"`
}

type ordW2RequestReturnResponse struct {
	Order           ordW2OrderDTO `json:"order"`
	ReturnRequestID string        `json:"return_request_id"`
}

type ordW2RefundsListResponse struct {
	Items []RefundDTO `json:"items"`
	Total int64       `json:"total"`
}

type ordW2ShipmentDTO struct {
	ShipmentID      string `json:"shipment_id"`
	OrderID         string `json:"order_id"`
	CarrierCode     string `json:"carrier_code"`
	TrackingCode    string `json:"tracking_code"`
	Status          string `json:"status"`
	Direction       string `json:"direction"`
	ReturnRequestID string `json:"return_request_id"`
}

type ordW2ShipmentResponse struct {
	Shipment ordW2ShipmentDTO `json:"shipment"`
}

func ordW2GetOrder(t *testing.T, env *testEnv, token, orderID string) ordW2OrderDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/orders/%s", url.PathEscape(orderID))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out ordW2OrderResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode order %s: %v", orderID, err)
	}
	return out.Order
}

func ordW2GetReturn(t *testing.T, env *testEnv, token, returnID string) ordW2ReturnRequestDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/returns/%s", url.PathEscape(returnID))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out ordW2ReturnResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode return %s: %v", returnID, err)
	}
	return out.ReturnRequest
}

func ordW2GetOrderRefunds(t *testing.T, env *testEnv, token, orderID string) []RefundDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/orders/%s/refunds", url.PathEscape(orderID))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out ordW2RefundsListResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode order refunds %s: %v", orderID, err)
	}
	return out.Items
}

func ordW2GetSellerBalance(t *testing.T, env *testEnv, sellerToken string) SellerBalanceResponse {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/seller/balance", sellerToken, nil, nil, http.StatusOK)
	var out SellerBalanceResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode seller balance: %v", err)
	}
	return out
}

func ordW2GetShipment(t *testing.T, env *testEnv, token, orderID, direction string) ordW2ShipmentDTO {
	t.Helper()
	path := fmt.Sprintf("/api/v1/shipping/shipments/%s?direction=%s", url.PathEscape(orderID), url.QueryEscape(direction))
	resp := env.mustDoJSON(t, http.MethodGet, path, token, nil, nil, http.StatusOK)
	var out ordW2ShipmentResponse
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode shipment %s (%s): %v", orderID, direction, err)
	}
	return out.Shipment
}

func ordW2SendShippingWebhook(t *testing.T, env *testEnv, carrier, orderID, trackingCode, rawStatus, desc string) {
	t.Helper()
	if strings.TrimSpace(carrier) == "" {
		carrier = "ghn"
	}
	path := fmt.Sprintf("/webhooks/shipping/%s", url.PathEscape(carrier))
	resp, err := env.client.PostSignedWebhook(env.ctx(t), path, ShippingWebhookPayload{
		EventID:      "ordw2-shp-" + uuid.NewString(),
		TrackingCode: trackingCode,
		OrderID:      orderID,
		RawStatus:    rawStatus,
		Description:  desc,
		OccurredAt:   time.Now().UTC().Format(time.RFC3339),
	}, env.cfg.ShippingWebhookSecret, time.Now().UTC(), "")
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s returned HTTP %d: %s", path, resp.StatusCode, string(resp.Body))
	}
}

func ordW2CheckoutAndDeliver(
	t *testing.T,
	env *testEnv,
	seller *sellerFixture,
	buyer *userFixture,
	addr AddressDTO,
	sku SKUDTO,
	qty int32,
	paymentMethod string,
) (CheckoutResponse, ordW2OrderDTO, string) {
	t.Helper()
	codEstimate := int64(0)
	if paymentMethod == "COD" {
		codEstimate = sku.PriceVND * int64(qty)
	}
	quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, sku.WeightGrams*qty, codEstimate)

	checkoutResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
		"Idempotency-Key": "ordw2-co-" + uuid.NewString(),
	}, CheckoutRequest{
		AddressID:     addr.ID,
		Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: qty}},
		PaymentMethod: paymentMethod,
		ShippingChoices: []ShopShippingChoiceDTO{
			{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
		},
	}, http.StatusCreated, http.StatusOK)

	var checkout CheckoutResponse
	if err := DecodeJSON(checkoutResp, &checkout); err != nil {
		t.Fatalf("decode checkout: %v", err)
	}
	if len(checkout.Orders) != 1 {
		t.Fatalf("expected 1 order, got %d", len(checkout.Orders))
	}
	ordID := checkout.Orders[0].ID

	if paymentMethod == "VIETQR" {
		whResp, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-ORDW2-" + uuid.NewString(),
			PaymentID:    checkout.PaymentID,
			OrderGroupID: checkout.CheckoutGroupID,
			AmountVND:    checkout.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + checkout.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whResp.StatusCode != http.StatusOK {
			t.Fatalf("payment webhook failed: err=%v status=%v", err, whResp)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order CONFIRMED", func() (bool, string) {
			o := ordW2GetOrder(t, env, buyer.AccessToken, ordID)
			return StatusEquals(o.Status, "CONFIRMED"), fmt.Sprintf("status=%s", o.Status)
		})
	}

	// Seller ships -> must stay PREPARING with tracking_code!
	shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ordID))
	shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
	var shipOut SellerShipResponse
	if err := DecodeJSON(shipResp, &shipOut); err != nil {
		t.Fatalf("decode ship order: %v", err)
	}
	if !StatusEquals(shipOut.Order.Status, "PREPARING") || shipOut.TrackingCode == "" {
		t.Fatalf("expected PREPARING + tracking_code after /ship, got status=%q tracking=%q", shipOut.Order.Status, shipOut.TrackingCode)
	}

	// Carrier delivers directly -> order traverses SHIPPING -> DELIVERED.
	carrier := shipOut.Order.CarrierCode
	if carrier == "" {
		carrier = quote.CarrierCode
	}
	ordW2SendShippingWebhook(t, env, carrier, ordID, shipOut.TrackingCode, "delivered", "Giao hàng thành công")

	var delivered ordW2OrderDTO
	env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order DELIVERED", func() (bool, string) {
		delivered = ordW2GetOrder(t, env, buyer.AccessToken, ordID)
		return StatusEquals(delivered.Status, "DELIVERED"), fmt.Sprintf("status=%s", delivered.Status)
	})
	return checkout, delivered, carrier
}

// TestE2E_OrderW2AReturnsAndShipments covers w2a-order.md E2E scenarios (a)-(e).
func TestE2E_OrderW2AReturnsAndShipments(t *testing.T) {
	env := getEnv()
	cat := env.ensureCategory(t)
	adminTok := env.adminLogin(t)

	// (a) Delivered -> Return requested -> Seller ACCEPT_RETURN -> RETURN webhook delivered ->
	// Seller confirm received -> Order RETURNED, refund pending = P, SKU restocked, seller gets 0.
	t.Run("A_ReturnAcceptedShippedConfirmed_RefundPendingRestockNoSellerPayout", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-s-a")
		buyer := env.registerVerifyAndLogin(t, "ordw2-b-a")
		addr := env.addAddress(t, buyer)
		const initialStock = int32(20)
		const qty = int32(2)
		prod, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 Return Flow A", 200000, initialStock, 500)

		_, delivered, carrier := ordW2CheckoutAndDeliver(t, env, seller, buyer, addr, sku, qty, "VIETQR")

		skuAfterDeliv := env.getProductSKU(t, prod.ID, sku.ID)
		if skuAfterDeliv.Stock != initialStock-qty {
			t.Fatalf("expected stock=%d after delivery, got %d", initialStock-qty, skuAfterDeliv.Stock)
		}

		// Buyer requests return.
		retPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(delivered.ID))
		retResp := env.mustDoJSON(t, http.MethodPost, retPath, buyer.AccessToken, nil, map[string]any{
			"reason":        "Sản phẩm bị móp góc",
			"evidence_urls": []string{"https://cdn.example.com/ordw2-a1.jpg"},
		}, http.StatusOK)
		var retOut ordW2RequestReturnResponse
		if err := DecodeJSON(retResp, &retOut); err != nil {
			t.Fatalf("decode request return: %v", err)
		}
		if retOut.ReturnRequestID == "" || !StatusEquals(retOut.Order.Status, "RETURN_REQUESTED") {
			t.Fatalf("unexpected request return response: %s", string(retResp.Body))
		}
		retID := retOut.ReturnRequestID

		// Seller accepts return (creates RETURN shipment).
		respondPath := fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, respondPath, seller.AccessToken, nil, map[string]any{
			"decision": "ACCEPT_RETURN",
			"note":     "Đồng ý nhận lại hàng",
		}, http.StatusOK)

		var retTrack string
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return shipment tracking assigned", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			retTrack = rr.ReturnTrackingCode
			return StatusEquals(rr.Status, "AWAITING_SHIPMENT") && retTrack != "", fmt.Sprintf("status=%s tracking=%q", rr.Status, retTrack)
		})

		// Webhook RETURN delivered -> return status becomes RECEIVED.
		ordW2SendShippingWebhook(t, env, carrier, delivered.ID, retTrack, "delivered", "Đã hoàn hàng về kho shop")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return RECEIVED", func() (bool, string) {
			rr := ordW2GetReturn(t, env, seller.AccessToken, retID)
			return StatusEquals(rr.Status, "RECEIVED") && rr.GoodsReceived, fmt.Sprintf("status=%s goods_received=%v", rr.Status, rr.GoodsReceived)
		})

		// Seller confirms received -> return REFUNDED, order RETURNED, refund pending = P, stock restored, seller balance = 0.
		confirmPath := fmt.Sprintf("/api/v1/seller/returns/%s/confirm-received", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, confirmPath, seller.AccessToken, nil, nil, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return REFUNDED and order RETURNED", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			ord := ordW2GetOrder(t, env, buyer.AccessToken, delivered.ID)
			return StatusEquals(rr.Status, "REFUNDED") && StatusEquals(ord.Status, "RETURNED"),
				fmt.Sprintf("ret=%s ord=%s", rr.Status, ord.Status)
		})

		// Verify refund record in status PENDING for exact amount P.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "pending refund for P", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer.AccessToken, delivered.ID)
			for _, r := range refunds {
				if r.AmountVND == delivered.TotalPayableVND && StatusEquals(r.Status, "PENDING") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want amount=%d status=PENDING", refunds, delivered.TotalPayableVND)
		})

		// Verify SKU stock restored.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "SKU stock restocked", func() (bool, string) {
			cur := env.getProductSKU(t, prod.ID, sku.ID)
			return cur.Stock == initialStock && cur.AvailableStock == initialStock,
				fmt.Sprintf("stock=%d avail=%d want=%d", cur.Stock, cur.AvailableStock, initialStock)
		})

		// Verify seller did NOT receive payout.
		bal := ordW2GetSellerBalance(t, env, seller.AccessToken)
		if bal.PendingVND != 0 || bal.AvailableVND != 0 {
			t.Fatalf("expected seller balance 0 after return refund, got pending=%d available=%d", bal.PendingVND, bal.AvailableVND)
		}
	})

	// (b) Seller rejects -> Buyer escalates -> Admin rejects -> Order COMPLETED, seller_pending credited.
	t.Run("B_ReturnRejectedEscalatedAdminRejected_OrderCompletedSellerPending", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-s-b")
		buyer := env.registerVerifyAndLogin(t, "ordw2-b-b")
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 Return Flow B", 250000, 10, 500)

		_, delivered, _ := ordW2CheckoutAndDeliver(t, env, seller, buyer, addr, sku, 1, "VIETQR")

		retPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(delivered.ID))
		retResp := env.mustDoJSON(t, http.MethodPost, retPath, buyer.AccessToken, nil, map[string]any{
			"reason": "Không thích màu này",
		}, http.StatusOK)
		var retOut ordW2RequestReturnResponse
		if err := DecodeJSON(retResp, &retOut); err != nil {
			t.Fatalf("decode request return: %v", err)
		}
		retID := retOut.ReturnRequestID

		// Seller rejects.
		respondPath := fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, respondPath, seller.AccessToken, nil, map[string]any{
			"decision": "REJECT",
			"note":     "Hàng đúng mô tả",
		}, http.StatusOK)

		// Buyer escalates.
		escalatePath := fmt.Sprintf("/api/v1/returns/%s/escalate", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, escalatePath, buyer.AccessToken, nil, map[string]any{
			"reason": "Đề nghị sàn xem xét lại",
		}, http.StatusOK)

		// Admin rejects -> CLOSED -> order COMPLETED, seller_pending receives R - C!
		resolvePath := fmt.Sprintf("/api/v1/admin/returns/%s/resolve", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, resolvePath, adminTok, nil, map[string]any{
			"decision": "REJECT",
			"note":     "Không đủ căn cứ trả hàng",
		}, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "order COMPLETED after admin reject", func() (bool, string) {
			ord := ordW2GetOrder(t, env, buyer.AccessToken, delivered.ID)
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			return StatusEquals(ord.Status, "COMPLETED") && StatusEquals(rr.Status, "CLOSED"),
				fmt.Sprintf("ord=%s ret=%s", ord.Status, rr.Status)
		})

		// Wave 2b buyer fee privacy: buyer order view masks fees to 0; read seller order for authoritative commission.
		if delivered.CommissionVND != 0 || delivered.PaymentFeeVND != 0 || delivered.PaymentFeeBps != 0 {
			t.Fatalf("buyer order view must mask fees to 0, got comm=%d pay_fee=%d pay_fee_bps=%d",
				delivered.CommissionVND, delivered.PaymentFeeVND, delivered.PaymentFeeBps)
		}
		for _, it := range delivered.Items {
			if it.CommissionBps != 0 {
				t.Fatalf("buyer order view must mask item commission_bps to 0, got %d", it.CommissionBps)
			}
		}
		sellerOrd := ordW2GetOrder(t, env, seller.AccessToken, delivered.ID)
		// Wave 2b (docs/contracts.md §3/§6): seller net = R - (C_cat + C_pay).
		wantPending := sellerOrd.SubtotalVND - sellerOrd.CommissionVND - sellerOrd.PaymentFeeVND
		env.pollUntil(t, env.cfg.PollTimeout, 400*time.Millisecond, "seller_pending credited after CLOSED", func() (bool, string) {
			bal := ordW2GetSellerBalance(t, env, seller.AccessToken)
			return bal.PendingVND == wantPending && bal.AvailableVND == 0,
				fmt.Sprintf("pending=%d available=%d wantPending=%d", bal.PendingVND, bal.AvailableVND, wantPending)
		})
	})

	// (c) Seller ACCEPT_REFUND refunds without restocking SKU.
	t.Run("C_AcceptRefund_DoesNotRestock", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-s-c")
		buyer := env.registerVerifyAndLogin(t, "ordw2-b-c")
		addr := env.addAddress(t, buyer)
		const initialStock = int32(20)
		const qty = int32(2)
		prod, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 Return Flow C", 150000, initialStock, 400)

		_, delivered, _ := ordW2CheckoutAndDeliver(t, env, seller, buyer, addr, sku, qty, "VIETQR")

		retPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(delivered.ID))
		retResp := env.mustDoJSON(t, http.MethodPost, retPath, buyer.AccessToken, nil, map[string]any{
			"reason": "Thiếu phụ kiện nhỏ",
		}, http.StatusOK)
		var retOut ordW2RequestReturnResponse
		if err := DecodeJSON(retResp, &retOut); err != nil {
			t.Fatalf("decode request return: %v", err)
		}
		retID := retOut.ReturnRequestID

		respondPath := fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, respondPath, seller.AccessToken, nil, map[string]any{
			"decision": "ACCEPT_REFUND",
			"note":     "Hoàn tiền luôn không cần gửi trả",
		}, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "ACCEPT_REFUND -> REFUNDED", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			ord := ordW2GetOrder(t, env, buyer.AccessToken, delivered.ID)
			return StatusEquals(rr.Status, "REFUNDED") && StatusEquals(ord.Status, "RETURNED"),
				fmt.Sprintf("ret=%s ord=%s", rr.Status, ord.Status)
		})

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "pending refund for ACCEPT_REFUND", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer.AccessToken, delivered.ID)
			for _, r := range refunds {
				if r.AmountVND == delivered.TotalPayableVND && StatusEquals(r.Status, "PENDING") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v", refunds)
		})

		// SKU stock must NOT be restocked (remains initialStock - qty).
		curSKU := env.getProductSKU(t, prod.ID, sku.ID)
		if curSKU.Stock != initialStock-qty {
			t.Fatalf("ACCEPT_REFUND must NOT restock SKU: got stock=%d, want %d", curSKU.Stock, initialStock-qty)
		}
	})

	// (d) COD order delivered -> return -> refund pending for P.
	t.Run("D_CODDelivered_ReturnRefunds", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-s-d")
		buyer := env.registerVerifyAndLogin(t, "ordw2-b-d")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 Return Flow D COD", 180000, 15, 500)

		checkout, delivered, _ := ordW2CheckoutAndDeliver(t, env, seller, buyer, addr, sku, 1, "COD")

		// Wait until COD payment is collected (SUCCEEDED) upon order.delivered.
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD payment collected", func() (bool, string) {
			pay := env.getPayment(t, buyer.AccessToken, checkout.PaymentID)
			return StatusEquals(pay.Status, "SUCCEEDED"), fmt.Sprintf("payment_status=%s", pay.Status)
		})

		retPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(delivered.ID))
		retResp := env.mustDoJSON(t, http.MethodPost, retPath, buyer.AccessToken, nil, map[string]any{
			"reason": "Sản phẩm hỏng khi mở hộp",
		}, http.StatusOK)
		var retOut ordW2RequestReturnResponse
		if err := DecodeJSON(retResp, &retOut); err != nil {
			t.Fatalf("decode request return: %v", err)
		}
		retID := retOut.ReturnRequestID

		respondPath := fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, respondPath, seller.AccessToken, nil, map[string]any{
			"decision": "ACCEPT_REFUND",
			"note":     "Hoàn tiền COD",
		}, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD return REFUNDED", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			ord := ordW2GetOrder(t, env, buyer.AccessToken, delivered.ID)
			return StatusEquals(rr.Status, "REFUNDED") && StatusEquals(ord.Status, "RETURNED"),
				fmt.Sprintf("ret=%s ord=%s", rr.Status, ord.Status)
		})

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD pending refund created for P", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer.AccessToken, delivered.ID)
			for _, r := range refunds {
				if r.AmountVND == delivered.TotalPayableVND && StatusEquals(r.Status, "PENDING") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want amount=%d", refunds, delivered.TotalPayableVND)
		})
	})

	// (e) Cancel in PREPARING with shipment -> shipment becomes cancelled; after carrier picked -> 409.
	t.Run("E_CancelOrderWithShipment_BeforeAndAfterPicked", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-s-e")
		buyer := env.registerVerifyAndLogin(t, "ordw2-b-e")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 Cancel Shipment E", 120000, 20, 400)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 400, 120000)

		placeAndShip := func(label string) (string, string, string) {
			coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
				"Idempotency-Key": "ordw2-e-" + label + "-" + uuid.NewString(),
			}, CheckoutRequest{
				AddressID:     addr.ID,
				Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
				PaymentMethod: "COD",
				ShippingChoices: []ShopShippingChoiceDTO{
					{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
				},
			}, http.StatusCreated, http.StatusOK)
			var co CheckoutResponse
			if err := DecodeJSON(coResp, &co); err != nil {
				t.Fatalf("decode checkout (%s): %v", label, err)
			}
			ordID := co.Orders[0].ID
			shipPath := fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ordID))
			shipResp := env.mustDoJSON(t, http.MethodPost, shipPath, seller.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
			var shipOut SellerShipResponse
			if err := DecodeJSON(shipResp, &shipOut); err != nil {
				t.Fatalf("decode ship (%s): %v", label, err)
			}
			if !StatusEquals(shipOut.Order.Status, "PREPARING") || shipOut.TrackingCode == "" {
				t.Fatalf("expected PREPARING + tracking (%s), got status=%q tracking=%q", label, shipOut.Order.Status, shipOut.TrackingCode)
			}
			carrier := shipOut.Order.CarrierCode
			if carrier == "" {
				carrier = quote.CarrierCode
			}
			return ordID, shipOut.TrackingCode, carrier
		}

		// E1: Cancel before pickup -> order CANCELLED and shipment cancelled.
		ord1, _, _ := placeAndShip("before-pick")
		cancelPath1 := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord1))
		env.mustDoJSON(t, http.MethodPost, cancelPath1, buyer.AccessToken, nil, map[string]string{
			"reason": "huỷ trước khi hãng lấy hàng",
		}, http.StatusOK)
		o1 := ordW2GetOrder(t, env, buyer.AccessToken, ord1)
		if !StatusEquals(o1.Status, "CANCELLED") {
			t.Fatalf("expected order1 CANCELLED, got %q", o1.Status)
		}
		shp1 := ordW2GetShipment(t, env, buyer.AccessToken, ord1, "FORWARD")
		if !StatusEquals(shp1.Status, "CANCELLED") {
			t.Fatalf("expected shipment1 CANCELLED, got %q", shp1.Status)
		}

		// E2: After carrier picked -> cancel returns 409 and order stays non-cancelled.
		ord2, track2, carrier2 := placeAndShip("after-pick")
		ordW2SendShippingWebhook(t, env, carrier2, ord2, track2, "picked", "Đã lấy hàng")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shipment picked -> delivering", func() (bool, string) {
			shp := ordW2GetShipment(t, env, buyer.AccessToken, ord2, "FORWARD")
			return StatusEquals(shp.Status, "DELIVERING"), fmt.Sprintf("shipment_status=%s", shp.Status)
		})

		cancelPath2 := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord2))
		env.mustDoJSON(t, http.MethodPost, cancelPath2, buyer.AccessToken, nil, map[string]string{
			"reason": "huỷ sau khi hãng đã lấy",
		}, http.StatusConflict)
		o2 := ordW2GetOrder(t, env, buyer.AccessToken, ord2)
		if StatusEquals(o2.Status, "CANCELLED") {
			t.Fatalf("order2 must NOT be cancelled after carrier pickup, got %q", o2.Status)
		}
	})

	// (f) RETURN returned -> ESCALATED -> Admin RETURN_AND_REFUND -> new RETURN shipment with different tracking ->
	// delivered -> Seller confirms received -> REFUNDED + restocked.
	t.Run("F_ReturnReturnedEscalated_AdminReturnAndRefund_NewShipmentDeliveredRefunded", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-s-f")
		buyer := env.registerVerifyAndLogin(t, "ordw2-b-f")
		addr := env.addAddress(t, buyer)
		const initialStock = int32(20)
		const qty = int32(1)
		prod, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 Return Reship F", 210000, initialStock, 500)

		_, delivered, carrier := ordW2CheckoutAndDeliver(t, env, seller, buyer, addr, sku, qty, "VIETQR")

		retPath := fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(delivered.ID))
		retResp := env.mustDoJSON(t, http.MethodPost, retPath, buyer.AccessToken, nil, map[string]any{
			"reason": "Hàng lỗi cần đổi trả",
		}, http.StatusOK)
		var retOut ordW2RequestReturnResponse
		if err := DecodeJSON(retResp, &retOut); err != nil {
			t.Fatalf("decode request return: %v", err)
		}
		retID := retOut.ReturnRequestID

		// Seller accepts return -> creates RETURN shipment #1 (oldTrack).
		respondPath := fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, respondPath, seller.AccessToken, nil, map[string]any{
			"decision": "ACCEPT_RETURN",
			"note":     "Chấp nhận trả hàng lần 1",
		}, http.StatusOK)

		var oldTrack string
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "initial return shipment tracking assigned", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			oldTrack = rr.ReturnTrackingCode
			return StatusEquals(rr.Status, "AWAITING_SHIPMENT") && oldTrack != "", fmt.Sprintf("status=%s tracking=%q", rr.Status, oldTrack)
		})

		// First RETURN shipment fails/returns to buyer -> order escalates return to ESCALATED.
		ordW2SendShippingWebhook(t, env, carrier, delivered.ID, oldTrack, "returned", "Không giao được hàng hoàn, trả lại người mua")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return ESCALATED after RETURN shipment returned", func() (bool, string) {
			rr := ordW2GetReturn(t, env, adminTok, retID)
			return StatusEquals(rr.Status, "ESCALATED"), fmt.Sprintf("status=%s", rr.Status)
		})

		// Admin approves RETURN_AND_REFUND -> clears oldTrack (logged in return_events), creates NEW RETURN shipment with different tracking.
		resolvePath := fmt.Sprintf("/api/v1/admin/returns/%s/resolve", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, resolvePath, adminTok, nil, map[string]any{
			"decision":          "RETURN_AND_REFUND",
			"note":              "Tạo vận đơn hoàn trả mới cho người mua",
			"return_fee_bearer": "SELLER",
		}, http.StatusOK)

		var newTrack string
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "new return shipment created with distinct tracking", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			newTrack = rr.ReturnTrackingCode
			return StatusEquals(rr.Status, "AWAITING_SHIPMENT") && newTrack != "" && newTrack != oldTrack,
				fmt.Sprintf("status=%s old=%q new=%q", rr.Status, oldTrack, newTrack)
		})

		// Verify previous tracking was preserved in return_events audit note.
		rrAfterReship := ordW2GetReturn(t, env, adminTok, retID)
		foundOldInAudit := false
		for _, ev := range rrAfterReship.Events {
			if StatusEquals(ev.FromStatus, "ESCALATED") && StatusEquals(ev.ToStatus, "AWAITING_SHIPMENT") &&
				strings.Contains(ev.Note, oldTrack) {
				foundOldInAudit = true
			}
		}
		if !foundOldInAudit {
			t.Fatalf("expected ESCALATED->AWAITING_SHIPMENT return_event note to preserve old tracking %q, got %+v", oldTrack, rrAfterReship.Events)
		}

		// Deliver new RETURN shipment -> RECEIVED.
		ordW2SendShippingWebhook(t, env, carrier, delivered.ID, newTrack, "delivered", "Vận đơn hoàn trả mới đã giao về shop")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return RECEIVED on new shipment", func() (bool, string) {
			rr := ordW2GetReturn(t, env, seller.AccessToken, retID)
			return StatusEquals(rr.Status, "RECEIVED") && rr.GoodsReceived, fmt.Sprintf("status=%s goods_received=%v", rr.Status, rr.GoodsReceived)
		})

		// Seller confirms received -> REFUNDED + order RETURNED + refund pending P + stock restored.
		confirmPath := fmt.Sprintf("/api/v1/seller/returns/%s/confirm-received", url.PathEscape(retID))
		env.mustDoJSON(t, http.MethodPost, confirmPath, seller.AccessToken, nil, nil, http.StatusOK)

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "reshipped return REFUNDED and order RETURNED", func() (bool, string) {
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retID)
			ord := ordW2GetOrder(t, env, buyer.AccessToken, delivered.ID)
			return StatusEquals(rr.Status, "REFUNDED") && StatusEquals(ord.Status, "RETURNED"),
				fmt.Sprintf("ret=%s ord=%s", rr.Status, ord.Status)
		})

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "pending refund created for P after reshipped return", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyer.AccessToken, delivered.ID)
			for _, r := range refunds {
				if r.AmountVND == delivered.TotalPayableVND && StatusEquals(r.Status, "PENDING") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want amount=%d", refunds, delivered.TotalPayableVND)
		})

		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "SKU stock restocked after reshipped return", func() (bool, string) {
			cur := env.getProductSKU(t, prod.ID, sku.ID)
			return cur.Stock == initialStock && cur.AvailableStock == initialStock,
				fmt.Sprintf("stock=%d avail=%d want=%d", cur.Stock, cur.AvailableStock, initialStock)
		})
	})
}

type ordW2UserMeResp struct {
	User UserDTO `json:"user"`
}

func ordW2GetMe(t *testing.T, env *testEnv, token string) UserDTO {
	t.Helper()
	resp := env.mustDoJSON(t, http.MethodGet, "/api/v1/users/me", token, nil, nil, http.StatusOK)
	var out ordW2UserMeResp
	if err := DecodeJSON(resp, &out); err != nil {
		t.Fatalf("decode GET /api/v1/users/me: %v", err)
	}
	return out.User
}

// TestE2E_OrderW2A_Review2EdgeCases covers review-round-2 order fixes (6)-(9) strictly via public/admin APIs:
// (6) 0 VND order cancel -> order & payment_status CANCELLED, payment CANCELLED, stock & coins restored
// (7) Forward failed delivery: VietQR -> RETURNED, empty delivered_at, pending refund, restocked; COD -> cod_rejected_count increments
// (8) Two-shop COD: one RETURNED + one CANCELLED -> both sub-orders & shared checkout group payment CANCELLED
// (9) Admin RETURN_AND_REFUND while RETURN shipment is still alive (FAILED/lost or live DELIVERING) -> 409
func TestE2E_OrderW2A_Review2EdgeCases(t *testing.T) {
	env := getEnv()
	cat := env.ensureCategory(t)
	adminTok := env.adminLogin(t)

	// (6) 0 VND order cancel -> payment_status CANCELLED, payment CANCELLED (not stuck).
	t.Run("6_ZeroPayableOrderCancel_PaymentStatusCancelled", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-r3-s6")
		buyer := env.registerVerifyAndLogin(t, "ordw2-r3-b6")
		addr := env.addAddress(t, buyer)
		const initialStock = int32(15)
		prod, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 R3 Zero Cancel", 100000, initialStock, 500)
		quote := env.calculateShippingQuote(t, buyer.AccessToken, seller.ShopID, 500, 0)

		freeshipV := env.createAdminVoucher(t, "FREESHIP", quote.FeeVND+10000, 0, 10)
		platV := env.createAdminVoucher(t, "PLATFORM", 60000, 50000, 10)
		env.saveVoucher(t, buyer.AccessToken, freeshipV)
		env.saveVoucher(t, buyer.AccessToken, platV)
		env.creditCoins(t, buyer.UserID, 100000, "ordw2_r3_zero_coins")

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2-r3-zero-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:         addr.ID,
			Items:             []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PlatformVoucherID: platV.ID,
			FreeshipVoucherID: freeshipV.ID,
			CoinsToUse:        40000,
			PaymentMethod:     "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: quote.CarrierCode, ServiceCode: quote.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co CheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil {
			t.Fatalf("decode 0 VND checkout: %v", err)
		}
		if co.TotalPayableVND != 0 || len(co.Orders) != 1 || co.PaymentID == "" {
			t.Fatalf("expected 0 VND checkout with 1 order & payment_id, got %+v", co)
		}
		ordID := co.Orders[0].ID

		cancelPath := fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ordID))
		env.mustDoJSON(t, http.MethodPost, cancelPath, buyer.AccessToken, nil, map[string]string{
			"reason": "huỷ đơn 0 đồng",
		}, http.StatusOK)

		ordAfter := ordW2GetOrder(t, env, buyer.AccessToken, ordID)
		if !StatusEquals(ordAfter.Status, "CANCELLED") || !StatusEquals(ordAfter.PaymentStatus, "CANCELLED") {
			t.Fatalf("0 VND cancelled order: want status=CANCELLED payment_status=CANCELLED, got status=%q payment_status=%q",
				ordAfter.Status, ordAfter.PaymentStatus)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "0 VND payment CANCELLED", func() (bool, string) {
			pay := env.getPayment(t, buyer.AccessToken, co.PaymentID)
			return StatusEquals(pay.Status, "CANCELLED"), fmt.Sprintf("payment_status=%s", pay.Status)
		})
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "0 VND SKU restocked and coins returned", func() (bool, string) {
			cur := env.getProductSKU(t, prod.ID, sku.ID)
			coins := env.getCoinBalance(t, buyer.AccessToken)
			return cur.Stock == initialStock && cur.AvailableStock == initialStock && coins == 100000,
				fmt.Sprintf("stock=%d avail=%d coins=%d", cur.Stock, cur.AvailableStock, coins)
		})
	})

	// (7) Failed forward delivery: VietQR -> RETURNED, delivered_at empty, PENDING refund, restocked; COD -> cod_rejected_count increments.
	t.Run("7_FailedDelivery_VietQRRefundRestock_And_CODRejectedCount", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-r3-s7")
		const initialStock = int32(20)
		prod, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 R3 Failed Deliv", 180000, initialStock, 400)

		// 7a. VietQR failed delivery
		buyerVQR := env.registerVerifyAndLogin(t, "ordw2-r3-b7vqr")
		addrVQR := env.addAddress(t, buyerVQR)
		qVQR := env.calculateShippingQuote(t, buyerVQR.AccessToken, seller.ShopID, 400, 0)
		coVQRResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyerVQR.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2-r3-7vqr-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addrVQR.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 2}},
			PaymentMethod: "VIETQR",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: qVQR.CarrierCode, ServiceCode: qVQR.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var coVQR CheckoutResponse
		if err := DecodeJSON(coVQRResp, &coVQR); err != nil {
			t.Fatalf("decode VietQR checkout: %v", err)
		}
		ordVQRID := coVQR.Orders[0].ID
		whPay, err := env.client.PostSignedWebhook(env.ctx(t), "/webhooks/payment", PaymentWebhookPayload{
			Provider:     "vietqr_mock",
			ProviderTxID: "TXN-ORDW2-R3-7-" + uuid.NewString(),
			PaymentID:    coVQR.PaymentID,
			OrderGroupID: coVQR.CheckoutGroupID,
			AmountVND:    coVQR.TotalPayableVND,
			Status:       "SUCCEEDED",
			Reference:    "NEXUSMART " + coVQR.CheckoutGroupID,
			OccurredAt:   time.Now().UTC().Format(time.RFC3339),
		}, env.cfg.PaymentWebhookSecret, time.Now().UTC(), "")
		if err != nil || whPay.StatusCode != http.StatusOK {
			t.Fatalf("VietQR webhook failed: err=%v status=%v", err, whPay)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "VietQR order CONFIRMED", func() (bool, string) {
			o := ordW2GetOrder(t, env, buyerVQR.AccessToken, ordVQRID)
			return StatusEquals(o.Status, "CONFIRMED"), o.Status
		})
		shipVQRResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ordVQRID)),
			seller.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
		var shipVQR SellerShipResponse
		if err := DecodeJSON(shipVQRResp, &shipVQR); err != nil {
			t.Fatalf("decode VietQR ship: %v", err)
		}
		carrierVQR := shipVQR.Order.CarrierCode
		if carrierVQR == "" {
			carrierVQR = qVQR.CarrierCode
		}
		ordW2SendShippingWebhook(t, env, carrierVQR, ordVQRID, shipVQR.TrackingCode, "delivering", "Đang giao")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "VietQR order SHIPPING", func() (bool, string) {
			o := ordW2GetOrder(t, env, buyerVQR.AccessToken, ordVQRID)
			return StatusEquals(o.Status, "SHIPPING"), o.Status
		})
		ordW2SendShippingWebhook(t, env, carrierVQR, ordVQRID, shipVQR.TrackingCode, "returned", "Giao thất bại hoàn về shop")

		var vqrReturned ordW2OrderDTO
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "VietQR order RETURNED", func() (bool, string) {
			vqrReturned = ordW2GetOrder(t, env, buyerVQR.AccessToken, ordVQRID)
			return StatusEquals(vqrReturned.Status, "RETURNED"), vqrReturned.Status
		})
		if strings.TrimSpace(vqrReturned.DeliveredAt) != "" {
			t.Fatalf("failed-delivery VietQR order must have empty delivered_at, got %q", vqrReturned.DeliveredAt)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "VietQR failed-delivery PENDING refund", func() (bool, string) {
			refunds := ordW2GetOrderRefunds(t, env, buyerVQR.AccessToken, ordVQRID)
			for _, r := range refunds {
				if r.AmountVND == vqrReturned.TotalPayableVND && StatusEquals(r.Status, "PENDING") {
					return true, ""
				}
			}
			return false, fmt.Sprintf("refunds=%+v want=%d/PENDING", refunds, vqrReturned.TotalPayableVND)
		})
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "VietQR failed-delivery stock restocked", func() (bool, string) {
			cur := env.getProductSKU(t, prod.ID, sku.ID)
			return cur.Stock == initialStock && cur.AvailableStock == initialStock,
				fmt.Sprintf("stock=%d avail=%d want=%d", cur.Stock, cur.AvailableStock, initialStock)
		})

		// 7b. COD failed delivery -> RETURNED, empty delivered_at, cod_rejected_count increments, restocked.
		buyerCOD := env.registerVerifyAndLogin(t, "ordw2-r3-b7cod")
		env.verifyBuyerPhone(t, buyerCOD)
		addrCOD := env.addAddress(t, buyerCOD)
		meBefore := ordW2GetMe(t, env, buyerCOD.AccessToken)
		qCOD := env.calculateShippingQuote(t, buyerCOD.AccessToken, seller.ShopID, 400, 180000)
		coCODResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyerCOD.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2-r3-7cod-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID:     addrCOD.ID,
			Items:         []CheckoutItemInputDTO{{SKUID: sku.ID, Quantity: 1}},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller.ShopID, CarrierCode: qCOD.CarrierCode, ServiceCode: qCOD.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var coCOD CheckoutResponse
		if err := DecodeJSON(coCODResp, &coCOD); err != nil {
			t.Fatalf("decode COD checkout: %v", err)
		}
		ordCODID := coCOD.Orders[0].ID
		shipCODResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ordCODID)),
			seller.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
		var shipCOD SellerShipResponse
		if err := DecodeJSON(shipCODResp, &shipCOD); err != nil {
			t.Fatalf("decode COD ship: %v", err)
		}
		carrierCOD := shipCOD.Order.CarrierCode
		if carrierCOD == "" {
			carrierCOD = qCOD.CarrierCode
		}
		ordW2SendShippingWebhook(t, env, carrierCOD, ordCODID, shipCOD.TrackingCode, "delivering", "Đang giao COD")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD order SHIPPING", func() (bool, string) {
			o := ordW2GetOrder(t, env, buyerCOD.AccessToken, ordCODID)
			return StatusEquals(o.Status, "SHIPPING"), o.Status
		})
		ordW2SendShippingWebhook(t, env, carrierCOD, ordCODID, shipCOD.TrackingCode, "returned", "Khách bom hàng COD")

		var codReturned ordW2OrderDTO
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD order RETURNED", func() (bool, string) {
			codReturned = ordW2GetOrder(t, env, buyerCOD.AccessToken, ordCODID)
			return StatusEquals(codReturned.Status, "RETURNED"), codReturned.Status
		})
		if strings.TrimSpace(codReturned.DeliveredAt) != "" {
			t.Fatalf("failed-delivery COD order must have empty delivered_at, got %q", codReturned.DeliveredAt)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD buyer cod_rejected_count incremented", func() (bool, string) {
			me := ordW2GetMe(t, env, buyerCOD.AccessToken)
			return me.CODRejectedCount == meBefore.CODRejectedCount+1,
				fmt.Sprintf("before=%d after=%d", meBefore.CODRejectedCount, me.CODRejectedCount)
		})
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "COD failed-delivery stock restocked", func() (bool, string) {
			cur := env.getProductSKU(t, prod.ID, sku.ID)
			return cur.Stock == initialStock && cur.AvailableStock == initialStock,
				fmt.Sprintf("stock=%d avail=%d want=%d", cur.Stock, cur.AvailableStock, initialStock)
		})
	})

	// (8) Two-shop COD: one RETURNED + one CANCELLED -> both sub-orders and checkout group payment CANCELLED.
	t.Run("8_TwoShopCOD_OneReturnedOneCancelled_GroupCancelled", func(t *testing.T) {
		seller1 := env.createApprovedSeller(t, "ordw2-r3-s8a")
		seller2 := env.createApprovedSeller(t, "ordw2-r3-s8b")
		buyer := env.registerVerifyAndLogin(t, "ordw2-r3-b8")
		env.verifyBuyerPhone(t, buyer)
		addr := env.addAddress(t, buyer)
		prod1, sku1 := env.createApprovedProduct(t, seller1, cat.ID, "OrdW2 R3 MultiShop1", 130000, 10, 400)
		prod2, sku2 := env.createApprovedProduct(t, seller2, cat.ID, "OrdW2 R3 MultiShop2", 160000, 10, 400)
		q1 := env.calculateShippingQuote(t, buyer.AccessToken, seller1.ShopID, 400, 130000)
		q2 := env.calculateShippingQuote(t, buyer.AccessToken, seller2.ShopID, 400, 160000)

		coResp := env.mustDoJSON(t, http.MethodPost, "/api/v1/checkout", buyer.AccessToken, map[string]string{
			"Idempotency-Key": "ordw2-r3-8-" + uuid.NewString(),
		}, CheckoutRequest{
			AddressID: addr.ID,
			Items: []CheckoutItemInputDTO{
				{SKUID: sku1.ID, Quantity: 1},
				{SKUID: sku2.ID, Quantity: 1},
			},
			PaymentMethod: "COD",
			ShippingChoices: []ShopShippingChoiceDTO{
				{ShopID: seller1.ShopID, CarrierCode: q1.CarrierCode, ServiceCode: q1.ServiceCode},
				{ShopID: seller2.ShopID, CarrierCode: q2.CarrierCode, ServiceCode: q2.ServiceCode},
			},
		}, http.StatusCreated, http.StatusOK)
		var co CheckoutResponse
		if err := DecodeJSON(coResp, &co); err != nil {
			t.Fatalf("decode 2-shop COD checkout: %v", err)
		}
		if len(co.Orders) != 2 || co.PaymentID == "" {
			t.Fatalf("expected 2 sub-orders + shared payment_id, got %+v", co)
		}
		var ord1ID, ord2ID string
		for _, o := range co.Orders {
			if o.ShopID == seller1.ShopID {
				ord1ID = o.ID
			} else if o.ShopID == seller2.ShopID {
				ord2ID = o.ID
			}
		}
		if ord1ID == "" || ord2ID == "" {
			t.Fatalf("failed to map 2 shop orders: %+v", co.Orders)
		}

		// Shop 1 ships -> delivering -> returned (failed COD delivery).
		ship1Resp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/orders/%s/ship", url.PathEscape(ord1ID)),
			seller1.AccessToken, nil, SellerShipRequest{}, http.StatusOK)
		var ship1 SellerShipResponse
		if err := DecodeJSON(ship1Resp, &ship1); err != nil {
			t.Fatalf("decode shop1 ship: %v", err)
		}
		carrier1 := ship1.Order.CarrierCode
		if carrier1 == "" {
			carrier1 = q1.CarrierCode
		}
		ordW2SendShippingWebhook(t, env, carrier1, ord1ID, ship1.TrackingCode, "delivering", "Đang giao shop 1")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shop1 order SHIPPING", func() (bool, string) {
			o := ordW2GetOrder(t, env, buyer.AccessToken, ord1ID)
			return StatusEquals(o.Status, "SHIPPING"), o.Status
		})
		ordW2SendShippingWebhook(t, env, carrier1, ord1ID, ship1.TrackingCode, "returned", "Khách không nhận shop 1")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shop1 order RETURNED + payment CANCELLED", func() (bool, string) {
			o := ordW2GetOrder(t, env, buyer.AccessToken, ord1ID)
			return StatusEquals(o.Status, "RETURNED") && StatusEquals(o.PaymentStatus, "CANCELLED"),
				fmt.Sprintf("status=%s pay=%s", o.Status, o.PaymentStatus)
		})

		// Shop 2 cancels -> both sub-orders terminal unpaid -> shared group payment becomes CANCELLED!
		env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/cancel", url.PathEscape(ord2ID)),
			buyer.AccessToken, nil, map[string]string{"reason": "huỷ shop 2"}, http.StatusOK)
		o2 := ordW2GetOrder(t, env, buyer.AccessToken, ord2ID)
		if !StatusEquals(o2.Status, "CANCELLED") || !StatusEquals(o2.PaymentStatus, "CANCELLED") {
			t.Fatalf("shop2 order: want CANCELLED/CANCELLED, got status=%q pay=%q", o2.Status, o2.PaymentStatus)
		}
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "shared COD group payment CANCELLED", func() (bool, string) {
			pay := env.getPayment(t, buyer.AccessToken, co.PaymentID)
			return StatusEquals(pay.Status, "CANCELLED"), fmt.Sprintf("payment_status=%s", pay.Status)
		})
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "both shops SKU stock restored", func() (bool, string) {
			s1 := env.getProductSKU(t, prod1.ID, sku1.ID)
			s2 := env.getProductSKU(t, prod2.ID, sku2.ID)
			return s1.Stock == 10 && s2.Stock == 10, fmt.Sprintf("s1=%d s2=%d", s1.Stock, s2.Stock)
		})
	})

	// (9) Admin RETURN_AND_REFUND when RETURN shipment is still alive (live DELIVERING or FAILED/lost) -> 409.
	t.Run("9_AdminReturnAndRefund_ActiveReturnShipment_Returns409", func(t *testing.T) {
		seller := env.createApprovedSeller(t, "ordw2-r3-s9")
		buyer := env.registerVerifyAndLogin(t, "ordw2-r3-b9")
		addr := env.addAddress(t, buyer)
		_, sku := env.createApprovedProduct(t, seller, cat.ID, "OrdW2 R3 Active Return Guard", 220000, 15, 500)

		setupAcceptedReturn := func(tag string) (ordW2OrderDTO, string, string, string) {
			_, delivered, carrier := ordW2CheckoutAndDeliver(t, env, seller, buyer, addr, sku, 1, "VIETQR")
			retResp := env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/orders/%s/return", url.PathEscape(delivered.ID)),
				buyer.AccessToken, nil, map[string]any{"reason": "Hàng lỗi " + tag}, http.StatusOK)
			var retOut ordW2RequestReturnResponse
			if err := DecodeJSON(retResp, &retOut); err != nil {
				t.Fatalf("decode request return %s: %v", tag, err)
			}
			env.mustDoJSON(t, http.MethodPost, fmt.Sprintf("/api/v1/seller/returns/%s/respond", url.PathEscape(retOut.ReturnRequestID)),
				seller.AccessToken, nil, map[string]any{"decision": "ACCEPT_RETURN", "note": "Chấp nhận hoàn " + tag}, http.StatusOK)
			var retTrack string
			env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return tracking assigned "+tag, func() (bool, string) {
				rr := ordW2GetReturn(t, env, buyer.AccessToken, retOut.ReturnRequestID)
				retTrack = rr.ReturnTrackingCode
				return StatusEquals(rr.Status, "AWAITING_SHIPMENT") && retTrack != "", fmt.Sprintf("status=%s track=%q", rr.Status, retTrack)
			})
			return delivered, retOut.ReturnRequestID, retTrack, carrier
		}

		// 9a: RETURN shipment lost/FAILED -> ESCALATED -> Admin RETURN_AND_REFUND returns 409.
		ordLost, retLostID, trackLost, carrierLost := setupAcceptedReturn("lost")
		ordW2SendShippingWebhook(t, env, carrierLost, ordLost.ID, trackLost, "lost", "Thất lạc kiện hoàn")
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "return ESCALATED on lost shipment", func() (bool, string) {
			rr := ordW2GetReturn(t, env, adminTok, retLostID)
			return StatusEquals(rr.Status, "ESCALATED"), rr.Status
		})
		resolveLostPath := fmt.Sprintf("/api/v1/admin/returns/%s/resolve", url.PathEscape(retLostID))
		env.mustDoJSON(t, http.MethodPost, resolveLostPath, adminTok, nil, map[string]any{
			"decision":          "RETURN_AND_REFUND",
			"note":              "must reject 409 when RETURN shipment failed/lost",
			"return_fee_bearer": "SELLER",
		}, http.StatusConflict)
		// Cleanly resolve with Admin REFUND -> 200.
		env.mustDoJSON(t, http.MethodPost, resolveLostPath, adminTok, nil, map[string]any{
			"decision": "REFUND",
			"note":     "Hoàn tiền do thất lạc kiện hoàn",
		}, http.StatusOK)

		// 9b: RETURN shipment still live (DELIVERING) in shipping -> Admin RETURN_AND_REFUND returns 409.
		ordLive, retLiveID, trackLive, carrierLive := setupAcceptedReturn("live")
		ordW2SendShippingWebhook(t, env, carrierLive, ordLive.ID, trackLive, "picked", "Hãng đã lấy hàng hoàn")
		var shLive ordW2ShipmentDTO
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "RETURN shipment DELIVERING and return IN_TRANSIT", func() (bool, string) {
			sh := ordW2GetShipment(t, env, buyer.AccessToken, ordLive.ID, "RETURN")
			rr := ordW2GetReturn(t, env, buyer.AccessToken, retLiveID)
			shLive = sh
			return StatusEquals(sh.Status, "DELIVERING") && StatusEquals(rr.Status, "IN_TRANSIT"),
				fmt.Sprintf("ship=%s ret=%s", sh.Status, rr.Status)
		})
		// Escalate return in order while shipping still holds the live DELIVERING RETURN shipment.
		payW2PublishNATSEvent(t, "shipment.updated", map[string]any{
			"shipment_id":       shLive.ShipmentID,
			"order_id":          ordLive.ID,
			"shop_id":           seller.ShopID,
			"carrier_code":      carrierLive,
			"direction":         "RETURN",
			"return_request_id": retLiveID,
			"tracking_code":     trackLive,
			"status":            "cancelled",
			"raw_status":        "cancelled",
			"description":       "stale cancel event while carrier still delivering",
			"occurred_at":       time.Now().UTC().Format(time.RFC3339),
			"redelivery_count":  0,
		})
		env.pollUntil(t, env.cfg.PollTimeout, 300*time.Millisecond, "live return ESCALATED", func() (bool, string) {
			rr := ordW2GetReturn(t, env, adminTok, retLiveID)
			return StatusEquals(rr.Status, "ESCALATED"), rr.Status
		})
		resolveLivePath := fmt.Sprintf("/api/v1/admin/returns/%s/resolve", url.PathEscape(retLiveID))
		env.mustDoJSON(t, http.MethodPost, resolveLivePath, adminTok, nil, map[string]any{
			"decision":          "RETURN_AND_REFUND",
			"note":              "must reject 409 while RETURN shipment is live DELIVERING",
			"return_fee_bearer": "SELLER",
		}, http.StatusConflict)
		if rrCheck := ordW2GetReturn(t, env, adminTok, retLiveID); !StatusEquals(rrCheck.Status, "ESCALATED") || rrCheck.ReturnTrackingCode != trackLive {
			t.Fatalf("rejected RETURN_AND_REFUND must keep return ESCALATED with %q, got status=%q track=%q",
				trackLive, rrCheck.Status, rrCheck.ReturnTrackingCode)
		}
		// Cleanly resolve with Admin REFUND -> 200.
		env.mustDoJSON(t, http.MethodPost, resolveLivePath, adminTok, nil, map[string]any{
			"decision": "REFUND",
			"note":     "Đóng tranh chấp hoàn tiền sau khi chặn tạo vận đơn trùng",
		}, http.StatusOK)
	})
}
