package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestAllocationFormula_AllDiscountComponentsAndZeroPayable covers brief items 1 & 2 (R1: PAY-B3, PRM-B1, ORD-B1).
func TestAllocationFormula_AllDiscountComponentsAndZeroPayable(t *testing.T) {
	t.Run("FullDiscountBreakdown_R_D_Ship_C_SumMatchesAmount", func(t *testing.T) {
		h := newTestHarness()
		// G=1,000,000; F=200,000; V_shop=50,000 -> R=750,000
		// V_plat=25,000; S_free=15,000; X=10,000 -> D=50,000
		// Ship=30,000; P = 750,000 + 30,000 - 50,000 = 730,000
		// C = 5% of effective 800,000 = 40,000 (<= R=750,000)
		h.promo.flashSales = []*promotionv1.FlashSale{
			{
				Id:    "fs-alloc",
				Items: []*promotionv1.FlashSaleItem{{Id: "fsi-alloc", SkuId: "sku-1", FlashPriceVnd: 800000}},
			},
		}
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1000000,
					FlashSaleDiscountVnd:       200000,
					ShopVoucherDiscountVnd:     50000,
					PlatformVoucherDiscountVnd: 25000,
					FreeshipDiscountVnd:        15000,
					CoinsDiscountVnd:           10000,
					ShippingFeeVnd:             30000,
				},
			},
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-alloc-full",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1, FlashSaleItemId: "fsi-alloc"}},
			ShopVoucherIds:    map[string]string{"shop-1": "v-shop"},
			PlatformVoucherId: "v-plat",
			FreeshipVoucherId: "v-free",
			CoinsToUse:        10000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		if len(h.payment.createCalls) != 1 {
			t.Fatalf("expected 1 CreatePayment call, got %d", len(h.payment.createCalls))
		}
		payReq := h.payment.createCalls[0]
		if len(payReq.ShopAllocations) != 1 {
			t.Fatalf("expected 1 ShopAllocation, got %d", len(payReq.ShopAllocations))
		}
		al := payReq.ShopAllocations[0]
		if al.SubtotalVnd != 750000 {
			t.Fatalf("allocation subtotal_vnd (R) = %d, want 750000", al.SubtotalVnd)
		}
		if al.ShippingFeeVnd != 30000 {
			t.Fatalf("allocation shipping_fee_vnd (Ship) = %d, want 30000", al.ShippingFeeVnd)
		}
		if al.PlatformDiscountVnd != 50000 {
			t.Fatalf("allocation platform_discount_vnd (D) = %d, want 50000", al.PlatformDiscountVnd)
		}
		if al.CommissionVnd != 40000 {
			t.Fatalf("allocation commission_vnd (C) = %d, want 40000", al.CommissionVnd)
		}
		if gotSum := al.SubtotalVnd + al.ShippingFeeVnd - al.PlatformDiscountVnd; gotSum != payReq.AmountVnd || gotSum != resp.TotalPayableVnd {
			t.Fatalf("R+Ship-D = %d, amount_vnd = %d, resp.TotalPayableVnd = %d", gotSum, payReq.AmountVnd, resp.TotalPayableVnd)
		}
	})

	t.Run("ZeroPayableOrder_StillCreatesPaymentWithFullAllocation", func(t *testing.T) {
		h := newTestHarness()
		// G=1,000,000, Ship=30,000; V_plat=500,000, S_free=30,000, X=500,000 -> D=1,030,000, P=0
		// R=1,000,000, C=50,000 (capped at R, NOT 0!)
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1000000,
					PlatformVoucherDiscountVnd: 500000,
					FreeshipDiscountVnd:        30000,
					CoinsDiscountVnd:           500000,
					ShippingFeeVnd:             30000,
				},
			},
		}

		resp, err := h.svc.CreateCheckout(ctxWithUser("buyer-1", "buyer", ""), &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-zero-payable",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat-500k",
			FreeshipVoucherId: "v-free-30k",
			CoinsToUse:        500000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout 0 VND failed: %v", err)
		}
		if resp.TotalPayableVnd != 0 {
			t.Fatalf("total_payable_vnd = %d, want 0", resp.TotalPayableVnd)
		}
		if len(h.payment.createCalls) != 1 {
			t.Fatalf("0 VND order MUST call CreatePayment, got %d calls", len(h.payment.createCalls))
		}
		if resp.PaymentId == "" || resp.Orders[0].PaymentId == "" {
			t.Fatalf("0 VND order MUST have real payment_id, got resp=%q order=%q", resp.PaymentId, resp.Orders[0].PaymentId)
		}
		if resp.PaymentStatus != "SUCCEEDED" || resp.Orders[0].Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("0 VND order status=%v payment_status=%q, want CONFIRMED/SUCCEEDED", resp.Orders[0].Status, resp.PaymentStatus)
		}
		al := h.payment.createCalls[0].ShopAllocations[0]
		if al.SubtotalVnd != 1000000 || al.CommissionVnd != 50000 || al.ShippingFeeVnd != 30000 || al.PlatformDiscountVnd != 1030000 {
			t.Fatalf("0 VND allocation = %+v, want R=1000000 C=50000 Ship=30000 D=1030000", al)
		}
		if al.SubtotalVnd+al.ShippingFeeVnd-al.PlatformDiscountVnd != 0 {
			t.Fatalf("0 VND allocation net != 0")
		}
	})
}

// TestCODPaymentStatus_PendingAtCheckout_SucceededOnDeliveredEvent covers brief item 3 (R2: ORD-B8).
func TestCODPaymentStatus_PendingAtCheckout_SucceededOnDeliveredEvent(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-cod-pending",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		FromCart:       true,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout COD failed: %v", err)
	}
	if resp.PaymentStatus != "PENDING" {
		t.Fatalf("new COD checkout payment_status = %q, want PENDING", resp.PaymentStatus)
	}
	var o1, o2 *orderv1.Order
	for _, o := range resp.Orders {
		if o.PaymentStatus != "PENDING" {
			t.Fatalf("COD sub-order %s payment_status = %q, want PENDING", o.Id, o.PaymentStatus)
		}
		if o.ShopId == "shop-1" {
			o1 = o
		} else {
			o2 = o
		}
	}

	// Ship & deliver only sub-order 1.
	ship1, err := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: o1.Id, ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder failed: %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-cod-deliv-1", events.ShipmentUpdatedPayload{
		OrderID:      o1.Id,
		TrackingCode: ship1.TrackingCode,
		Status:       "delivered",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(delivered) failed: %v", err)
	}

	// Payment emits payment.succeeded for delivered COD sub-order 1.
	if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-cod-paid-1", events.PaymentSucceededPayload{
		PaymentID:    resp.PaymentId,
		OrderGroupID: resp.CheckoutGroupId,
		OrderID:      o1.Id,
		Method:       "COD",
	}); err != nil {
		t.Fatalf("HandlePaymentSucceeded for delivered COD failed: %v", err)
	}

	got1, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: o1.Id})
	got2, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: o2.Id})
	if got1.Order.PaymentStatus != "SUCCEEDED" {
		t.Fatalf("delivered COD sub-order 1 payment_status = %q, want SUCCEEDED", got1.Order.PaymentStatus)
	}
	if got2.Order.PaymentStatus != "PENDING" {
		t.Fatalf("undelivered COD sub-order 2 payment_status = %q, want PENDING", got2.Order.PaymentStatus)
	}
}

// TestOrderReturnedEvent_EmittedOnShippingAndReturnRequested covers brief item 4 (order.returned outbox).
func TestOrderReturnedEvent_EmittedOnShippingAndReturnRequested(t *testing.T) {
	t.Run("ShippingToReturned_COD", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-ret-ev-1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder failed: %v", err)
		}
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-ship-1", events.ShipmentUpdatedPayload{
			OrderID:      ord.Id,
			TrackingCode: shipResp.TrackingCode,
			Status:       "returned",
			Description:  "Buyer refused COD delivery",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned) failed: %v", err)
		}

		var got map[string]any
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == "order.returned" {
				if err := json.Unmarshal(ev.PayloadJSON, &got); err != nil {
					t.Fatalf("unmarshal order.returned: %v", err)
				}
			}
		}
		if got == nil {
			t.Fatalf("expected order.returned outbox event on SHIPPING -> RETURNED")
		}
		if got["order_id"] != ord.Id || got["order_code"] != ord.OrderCode ||
			got["checkout_group_id"] != resp.CheckoutGroupId || got["buyer_id"] != "buyer-1" ||
			got["shop_id"] != "shop-1" || got["payment_method"] != "COD" ||
			got["from_status"] != "SHIPPING" || int64(got["total_payable_vnd"].(float64)) != ord.TotalPayableVnd {
			t.Fatalf("unexpected order.returned payload: %+v", got)
		}
	})

	t.Run("ReturnRequestedToReturned_VietQR", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-ret-ev-2",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-ret-ev-pay", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-ret-ev-deliv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		})
		retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "damaged"})
		if err != nil {
			t.Fatalf("RequestReturn failed: %v", err)
		}
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
			Note:            "Approved refund",
		}); err != nil {
			t.Fatalf("RespondReturn(ACCEPT_REFUND) failed: %v", err)
		}

		var got map[string]any
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == "order.returned" {
				_ = json.Unmarshal(ev.PayloadJSON, &got)
			}
		}
		if got == nil {
			t.Fatalf("expected order.returned outbox event on RETURN_REQUESTED -> RETURNED")
		}
		if got["from_status"] != "RETURN_REQUESTED" || got["payment_method"] != "VIETQR" {
			t.Fatalf("unexpected order.returned payload: %+v", got)
		}
	})
}

// TestRetryPayment_ValidAndRejectedCases covers brief item 5 (R3: PAY-B1, PAY-B5, ORD-B5).
func TestRetryPayment_ValidAndRejectedCases(t *testing.T) {
	h := newTestHarness()
	baseNow := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return baseNow }

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithUser("buyer-2", "buyer", "")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-retry-multi",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		FromCart:       true,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil || len(resp.Orders) != 2 {
		t.Fatalf("CreateCheckout multi-shop VIETQR failed: %v", err)
	}
	oldPayID := resp.PaymentId
	oldExp := resp.PaymentExpiresAt.AsTime()

	// 1. Other buyer rejected with PermissionDenied.
	if _, err := h.svc.RetryPayment(otherBuyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:     oldPayID,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer RetryPayment = %v, want PermissionDenied", err)
	}

	// 2. Switching to COD rejected with FailedPrecondition (409).
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:     oldPayID,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("COD RetryPayment = %v, want FailedPrecondition", err)
	}

	// 3. Valid retry 5 minutes later updates payment_id + payment_expires_at on ALL sub-orders!
	h.svc.nowFn = func() time.Time { return baseNow.Add(5 * time.Minute) }
	retryResp, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:      oldPayID,
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		IdempotencyKey: "retry-key-1",
	})
	if err != nil {
		t.Fatalf("valid RetryPayment failed: %v", err)
	}
	if retryResp.CheckoutGroupId != resp.CheckoutGroupId {
		t.Fatalf("checkout_group_id = %q, want %q", retryResp.CheckoutGroupId, resp.CheckoutGroupId)
	}
	if retryResp.PaymentId == "" || retryResp.PaymentId == oldPayID {
		t.Fatalf("expected new payment_id != %q, got %q", oldPayID, retryResp.PaymentId)
	}
	if !retryResp.PaymentExpiresAt.AsTime().After(oldExp) {
		t.Fatalf("expected new expiry %v > old expiry %v", retryResp.PaymentExpiresAt.AsTime(), oldExp)
	}
	lastPayReq := h.payment.createCalls[len(h.payment.createCalls)-1]
	if !lastPayReq.SupersedePrevious || len(lastPayReq.ShopAllocations) != 2 {
		t.Fatalf("expected supersede_previous=true and 2 allocations, got %+v", lastPayReq)
	}
	for _, o := range resp.Orders {
		got, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: o.Id})
		if err != nil {
			t.Fatalf("GetOrder(%s) failed: %v", o.Id, err)
		}
		if got.Order.PaymentId != retryResp.PaymentId {
			t.Fatalf("sub-order %s payment_id = %q, want %q", o.Id, got.Order.PaymentId, retryResp.PaymentId)
		}
		if !got.Order.PaymentExpiresAt.AsTime().Equal(retryResp.PaymentExpiresAt.AsTime()) {
			t.Fatalf("sub-order %s payment_expires_at = %v, want %v", o.Id, got.Order.PaymentExpiresAt.AsTime(), retryResp.PaymentExpiresAt.AsTime())
		}
	}

	// 4. Retrying with OLD superseded payment_id -> FailedPrecondition (409).
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:     oldPayID,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale payment_id RetryPayment = %v, want FailedPrecondition", err)
	}

	// 5. Retrying after expiry -> FailedPrecondition (409).
	h.svc.nowFn = func() time.Time { return retryResp.PaymentExpiresAt.AsTime().Add(time.Second) }
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:     retryResp.PaymentId,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expired RetryPayment = %v, want FailedPrecondition", err)
	}

	// 6. Retrying on CANCELLED group -> FailedPrecondition (409).
	h.svc.nowFn = func() time.Time { return baseNow.Add(6 * time.Minute) }
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: resp.Orders[0].Id, Reason: "cancel"}); err != nil {
		t.Fatalf("CancelOrder failed: %v", err)
	}
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:     retryResp.PaymentId,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("cancelled group RetryPayment = %v, want FailedPrecondition", err)
	}

	// 7. Retrying on already paid (CONFIRMED) group -> FailedPrecondition (409).
	respPaid, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-retry-paid",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	if err != nil {
		t.Fatalf("CreateCheckout paid failed: %v", err)
	}
	_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-retry-paid", events.PaymentSucceededPayload{
		PaymentID: respPaid.PaymentId, OrderGroupID: respPaid.CheckoutGroupId,
	})
	if _, err := h.svc.RetryPayment(buyerCtx, &orderv1.RetryPaymentRequest{
		PaymentId:     respPaid.PaymentId,
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("already paid RetryPayment = %v, want FailedPrecondition", err)
	}
}

// TestPaymentEvents_PAYB6_IgnoreStalePaymentID_CancelledGuard_PartialRefund covers brief item 6 (PAY-B6).
func TestPaymentEvents_PAYB6_IgnoreStalePaymentID_CancelledGuard_PartialRefund(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sysCtx := ctxWithUser("payment-service", "system", "")

	t.Run("IgnoreSupersededPaymentID_OnEventsAndMarkPaymentResult", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-payb6-stale",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		staleID := "pay-superseded-old"

		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-stale-ok", events.PaymentSucceededPayload{
			PaymentID:    staleID,
			OrderGroupID: resp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded(stale) failed: %v", err)
		}
		if err := h.svc.HandlePaymentFailed(context.Background(), "evt-stale-fail", events.PaymentFailedPayload{
			PaymentID:    staleID,
			OrderGroupID: resp.CheckoutGroupId,
			Reason:       "old failed",
		}); err != nil {
			t.Fatalf("HandlePaymentFailed(stale) failed: %v", err)
		}
		if _, err := h.svc.MarkPaymentResult(sysCtx, &orderv1.MarkPaymentResultRequest{
			CheckoutGroupId: resp.CheckoutGroupId,
			PaymentId:       staleID,
			Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
		}); err != nil {
			t.Fatalf("MarkPaymentResult(stale) failed: %v", err)
		}

		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PENDING || got.Order.PaymentStatus != "PENDING" {
			t.Fatalf("stale payment_id mutated order: status=%v payment_status=%q", got.Order.Status, got.Order.PaymentStatus)
		}
		if len(h.product.confirmCalls) != 0 || len(h.product.releaseCalls) != 0 {
			t.Fatalf("stale payment_id must not trigger stock confirm/release: confirm=%v release=%v",
				h.product.confirmCalls, h.product.releaseCalls)
		}
	})

	t.Run("CancelledGroup_StaysCancelled_OnLatePaymentSucceeded", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-payb6-cancelled",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: resp.Orders[0].Id,
			Reason:  "buyer cancelled",
		}); err != nil {
			t.Fatalf("CancelOrder failed: %v", err)
		}

		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-late-ok", events.PaymentSucceededPayload{
			PaymentID:    resp.PaymentId,
			OrderGroupID: resp.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded(late) failed: %v", err)
		}

		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp.Orders[0].Id})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("cancelled order status = %v, want CANCELLED", got.Order.Status)
		}
		if got.Order.PaymentStatus == "SUCCEEDED" {
			t.Fatalf("late payment.succeeded must NOT overwrite cancelled order payment_status to SUCCEEDED")
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), resp.CheckoutGroupId)
		if grp.PaymentStatus == "SUCCEEDED" {
			t.Fatalf("late payment.succeeded must NOT overwrite cancelled checkout_group payment_status to SUCCEEDED")
		}
	})

	t.Run("PartialRefund_KeepsStatus_FullCumulativeRefund_SetsREFUNDED", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-payb6-refund",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-pay-ok-ref", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})

		// Mismatched payment_id refund must be ignored.
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-ref-wrong-pay", events.PaymentRefundedPayload{
			RefundID: "ref-wrong", PaymentID: "pay-other", OrderID: ord.Id, AmountVND: ord.TotalPayableVnd, Reason: "stale",
		})
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if got.Order.PaymentStatus != "SUCCEEDED" {
			t.Fatalf("mismatched payment_id refund mutated payment_status to %q", got.Order.PaymentStatus)
		}

		// Partial refund (300,000 of 1,030,000) -> keeps payment_status = SUCCEEDED.
		part1 := int64(300000)
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-ref-part-1", events.PaymentRefundedPayload{
			RefundID: "ref-p1", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: part1, Reason: "partial 1",
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded(part1) failed: %v", err)
		}
		got, _ = h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if got.Order.PaymentStatus != "SUCCEEDED" || got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("after partial refund: status=%v payment_status=%q, want CONFIRMED/SUCCEEDED", got.Order.Status, got.Order.PaymentStatus)
		}

		// Second refund covering remaining amount -> cumulative == total_payable_vnd -> REFUNDED!
		part2 := ord.TotalPayableVnd - part1
		if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-ref-part-2", events.PaymentRefundedPayload{
			RefundID: "ref-p2", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: part2, Reason: "partial 2",
		}); err != nil {
			t.Fatalf("HandlePaymentRefunded(part2) failed: %v", err)
		}
		got, _ = h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if got.Order.PaymentStatus != "REFUNDED" {
			t.Fatalf("after full cumulative refund: payment_status=%q, want REFUNDED", got.Order.PaymentStatus)
		}
	})
}

// TestPromotionRelease_SubOrderVsLastOrder_ReturnedAndFlashShopID covers brief item 7 (R5: PRM-B3, ORD-B3).
func TestPromotionRelease_SubOrderVsLastOrder_ReturnedAndFlashShopID(t *testing.T) {
	h := newTestHarness()
	buyerCtx := auth.WithRawToken(ctxWithUser("buyer-1", "buyer", ""), "Bearer buyer-jwt")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-r5-promo",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		FromCart:          true,
		Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", FlashSaleItemId: "fsi-shop1"}},
		ShopVoucherIds:    map[string]string{"shop-1": "v-s1"},
		PlatformVoucherId: "v-plat",
		CoinsToUse:        5000,
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil || len(resp.Orders) != 2 {
		t.Fatalf("CreateCheckout failed: %v", err)
	}

	// Verify FlashSaleReservationItem.shop_id was populated in ReservePromotion!
	if len(h.promo.reserveReqs) != 1 || len(h.promo.reserveReqs[0].FlashSaleItems) != 1 {
		t.Fatalf("expected 1 ReservePromotion with 1 flash item, got %+v", h.promo.reserveReqs)
	}
	if gotShop := h.promo.reserveReqs[0].FlashSaleItems[0].GetShopId(); gotShop != "shop-1" {
		t.Fatalf("FlashSaleReservationItem.shop_id = %q, want shop-1", gotShop)
	}

	var o1, o2 *orderv1.Order
	for _, o := range resp.Orders {
		if o.ShopId == "shop-1" {
			o1 = o
		} else {
			o2 = o
		}
	}

	// 1. Cancel sub-order 2 while sub-order 1 is still active -> ReleasePromotionShop(group, shop-2), NOT ReleasePromotion!
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: o2.Id, Reason: "cancel shop-2"}); err != nil {
		t.Fatalf("CancelOrder(o2) failed: %v", err)
	}
	if len(h.promo.releaseCalls) != 0 {
		t.Fatalf("expected 0 group ReleasePromotion calls while sibling active, got %v", h.promo.releaseCalls)
	}
	if len(h.promo.releaseShopCalls) != 1 ||
		h.promo.releaseShopCalls[0].GetOrderId() != resp.CheckoutGroupId ||
		h.promo.releaseShopCalls[0].GetShopId() != "shop-2" {
		t.Fatalf("expected ReleasePromotionShop(%s, shop-2), got %+v", resp.CheckoutGroupId, h.promo.releaseShopCalls)
	}
	if h.promo.releaseShopAuth[0].service != clients.InternalServiceName || h.promo.releaseShopAuth[0].rawToken != "" {
		t.Fatalf("ReleasePromotionShop must use service token, got %+v", h.promo.releaseShopAuth[0])
	}

	// 2. Sub-order 1 ships and is RETURNED -> calls ReleasePromotionShop(group, shop-1).
	ship1, err := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: o1.Id, ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder(o1) failed: %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-r5-ret", events.ShipmentUpdatedPayload{
		OrderID: o1.Id, TrackingCode: ship1.TrackingCode, Status: "returned", Description: "refused",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(returned) failed: %v", err)
	}
	if len(h.promo.releaseShopCalls) != 2 || h.promo.releaseShopCalls[1].GetShopId() != "shop-1" {
		t.Fatalf("expected ReleasePromotionShop for returned order shop-1, got %+v", h.promo.releaseShopCalls)
	}

	// 3. Separate checkout where BOTH sub-orders are cancelled -> 1st uses ReleasePromotionShop, 2nd (last) uses ReleasePromotion!
	resp2, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    "idem-r5-cancel-all",
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		FromCart:          true,
		PlatformVoucherId: "v-plat",
		PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout 2 failed: %v", err)
	}
	h.promo.releaseCalls = nil
	h.promo.releaseShopCalls = nil

	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: resp2.Orders[0].Id, Reason: "c1"}); err != nil {
		t.Fatalf("CancelOrder 1 failed: %v", err)
	}
	if len(h.promo.releaseShopCalls) != 1 || len(h.promo.releaseCalls) != 0 {
		t.Fatalf("1st cancel: want 1 ReleasePromotionShop and 0 ReleasePromotion, got shop=%d group=%d",
			len(h.promo.releaseShopCalls), len(h.promo.releaseCalls))
	}

	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: resp2.Orders[1].Id, Reason: "c2"}); err != nil {
		t.Fatalf("CancelOrder 2 failed: %v", err)
	}
	if len(h.promo.releaseCalls) != 1 || h.promo.releaseCalls[0] != resp2.CheckoutGroupId {
		t.Fatalf("last sub-order cancel: want ReleasePromotion(%s), got %v", resp2.CheckoutGroupId, h.promo.releaseCalls)
	}
}

// TestCODRejection_ShippingToReturnedCallsRecordCODRejection covers brief item 8 (R6: ORD-B6).
func TestCODRejection_ShippingToReturnedCallsRecordCODRejection(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("COD_ShippingToReturned_CallsRecordCODRejectionWithServiceToken", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-cod-rej-1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ord := resp.Orders[0]
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-cod-rej-1", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, TrackingCode: shipResp.TrackingCode, Status: "returned", Description: "Customer refused COD parcel",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned) failed: %v", err)
		}
		if len(h.user.codRejectionCalls) != 1 {
			t.Fatalf("expected 1 RecordCODRejection call, got %d", len(h.user.codRejectionCalls))
		}
		req := h.user.codRejectionCalls[0]
		if req.GetUserId() != "buyer-1" || req.GetOrderId() != ord.Id || req.GetReason() == "" {
			t.Fatalf("unexpected RecordCODRejection request: %+v", req)
		}
		if gotAuth := h.user.codRejectionAuth[0]; gotAuth.service != clients.InternalServiceName || gotAuth.rawToken != "" {
			t.Fatalf("RecordCODRejection must use service token, got %+v", gotAuth)
		}
	})

	t.Run("COD_ReturnRequestedToReturned_DoesNotCallRecordCODRejection", func(t *testing.T) {
		h := newTestHarness()
		resp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-cod-rej-2",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ord := resp.Orders[0]
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-cod-deliv-2", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		})
		retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "wrong size"})
		if err != nil {
			t.Fatalf("RequestReturn failed: %v", err)
		}
		if _, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retResp.ReturnRequestId,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		}); err != nil {
			t.Fatalf("RespondReturn(ACCEPT_REFUND) failed: %v", err)
		}
		if len(h.user.codRejectionCalls) != 0 {
			t.Fatalf("RETURN_REQUESTED -> RETURNED must NOT call RecordCODRejection, got %d", len(h.user.codRejectionCalls))
		}
	})

	t.Run("VietQR_ShippingToReturned_DoesNotCallRecordCODRejection", func(t *testing.T) {
		h := newTestHarness()
		resp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "idem-vietqr-rej",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ord := resp.Orders[0]
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-vq-ok", events.PaymentSucceededPayload{
			PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
		})
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-vq-ret", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Status: "returned",
		})
		if len(h.user.codRejectionCalls) != 0 {
			t.Fatalf("VIETQR SHIPPING -> RETURNED must NOT call RecordCODRejection, got %d", len(h.user.codRejectionCalls))
		}
	})
}

// TestIdempotencyPayloadHash_SameKeyDiffPayloadConflict covers brief item 9 (ORD-B4).
func TestIdempotencyPayloadHash_SameKeyDiffPayloadConflict(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	req1 := &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-hash-key",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-2", Quantity: 1},
			{SkuId: "sku-1", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		ShippingChoices: []*orderv1.ShopShippingChoice{
			{ShopId: "shop-2", CarrierCode: "ghn", ServiceCode: "standard"},
			{ShopId: "shop-1", CarrierCode: "ghn", ServiceCode: "standard"},
		},
	}
	resp1, err := h.svc.CreateCheckout(buyerCtx, req1)
	if err != nil {
		t.Fatalf("first CreateCheckout failed: %v", err)
	}

	// Reordered items & shipping choices -> SAME canonical hash -> idempotent replay!
	reqSameReordered := &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-hash-key",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
			{SkuId: "sku-2", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		ShippingChoices: []*orderv1.ShopShippingChoice{
			{ShopId: "shop-1", CarrierCode: "GHN", ServiceCode: "STANDARD"},
			{ShopId: "shop-2", CarrierCode: "ghn", ServiceCode: "standard"},
		},
	}
	replay, err := h.svc.CreateCheckout(buyerCtx, reqSameReordered)
	if err != nil {
		t.Fatalf("reordered identical payload replay failed: %v", err)
	}
	if !replay.IdempotentReplay || replay.CheckoutGroupId != resp1.CheckoutGroupId {
		t.Fatalf("expected idempotent replay for canonical equivalent payload, got %+v", replay)
	}

	// Same key, different payment_method -> 409 (FailedPrecondition).
	reqDiffMethod := &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-hash-key",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 1},
			{SkuId: "sku-2", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, reqDiffMethod); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("same key different payment_method = %v, want FailedPrecondition (409)", err)
	}

	// Same key, different quantity -> 409 (FailedPrecondition).
	reqDiffQty := &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-hash-key",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items: []*orderv1.CheckoutItemInput{
			{SkuId: "sku-1", Quantity: 2},
			{SkuId: "sku-2", Quantity: 1},
		},
		PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		ShippingChoices: []*orderv1.ShopShippingChoice{
			{ShopId: "shop-1", CarrierCode: "ghn", ServiceCode: "standard"},
			{ShopId: "shop-2", CarrierCode: "ghn", ServiceCode: "standard"},
		},
	}
	if _, err := h.svc.CreateCheckout(buyerCtx, reqDiffQty); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("same key different quantity = %v, want FailedPrecondition (409)", err)
	}
}

// TestBuyerConfirmReceived_OnlyFromDelivered_RejectsReturnRequested covers brief item 10 (ORD-B7).
func TestBuyerConfirmReceived_OnlyFromDelivered_RejectsReturnRequested(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-ordb7",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	ordID := resp.Orders[0].Id
	shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-ordb7-deliv", events.ShipmentUpdatedPayload{
		OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "delivered",
	})
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ordID, Reason: "broken item",
	}); err != nil {
		t.Fatalf("RequestReturn failed: %v", err)
	}

	if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{
		OrderId: ordID,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("BuyerConfirmReceived on RETURN_REQUESTED = %v, want FailedPrecondition (409)", err)
	}
}

// TestSellerShipOrder_EnforcesBuyerSelectedCarrierAndService covers brief item 11 (SHP-B2).
func TestSellerShipOrder_EnforcesBuyerSelectedCarrierAndService(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "idem-shpb2",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		ShippingChoices: []*orderv1.ShopShippingChoice{
			{ShopId: "shop-1", CarrierCode: "ghn", ServiceCode: "standard"},
		},
	})
	if err != nil {
		t.Fatalf("CreateCheckout failed: %v", err)
	}
	ordID := resp.Orders[0].Id

	// Wrong carrier -> 409 (FailedPrecondition).
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId:     ordID,
		ShopId:      "shop-1",
		CarrierCode: "mock_express",
		ServiceCode: "standard",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("mismatched carrier_code = %v, want FailedPrecondition (409)", err)
	}

	// Wrong service -> 409 (FailedPrecondition).
	if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId:     ordID,
		ShopId:      "shop-1",
		CarrierCode: "ghn",
		ServiceCode: "fast",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("mismatched service_code = %v, want FailedPrecondition (409)", err)
	}

	// Empty carrier/service -> uses buyer's chosen ghn/standard and succeeds!
	shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId: ordID,
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("SellerShipOrder with empty carrier/service failed: %v", err)
	}
	if shipResp.Order.CarrierCode != "ghn" || shipResp.Order.ServiceCode != "standard" {
		t.Fatalf("shipped carrier/service = %s/%s, want ghn/standard", shipResp.Order.CarrierCode, shipResp.Order.ServiceCode)
	}
}

// TestRewardCoins_ComputedOnActualMerchPaidAndZeroForZeroPayable covers brief item 12 (PRM-B2).
func TestRewardCoins_ComputedOnActualMerchPaidAndZeroForZeroPayable(t *testing.T) {
	t.Run("DiscountedOrder_RewardBasedOn_R_Minus_Vplat_Minus_X", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		// G=1,000,000, F=200,000, V_shop=50,000 -> R=750,000
		// V_plat=100,000, S_free=15,000, X=50,000, Ship=30,000
		// Actual merch paid = R - V_plat - X = 750,000 - 100,000 - 50,000 = 600,000
		// Reward (100 bps = 1%) = 6,000 coins (NOT 1% of G=10,000!)
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1000000,
					FlashSaleDiscountVnd:       200000,
					ShopVoucherDiscountVnd:     50000,
					PlatformVoucherDiscountVnd: 100000,
					FreeshipDiscountVnd:        15000,
					CoinsDiscountVnd:           50000,
					ShippingFeeVnd:             30000,
				},
			},
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-prmb2-disc",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			ShopVoucherIds:    map[string]string{"shop-1": "v-s"},
			PlatformVoucherId: "v-p",
			FreeshipVoucherId: "v-f",
			CoinsToUse:        50000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ordID := resp.Orders[0].Id
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-prmb2-deliv", events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		})
		if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
			t.Fatalf("BuyerConfirmReceived failed: %v", err)
		}

		var completed events.OrderCompletedPayload
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCompleted {
				_ = json.Unmarshal(ev.PayloadJSON, &completed)
			}
		}
		if completed.CoinsReward != 6000 {
			t.Fatalf("coins_reward = %d, want 6000 (1%% of R-V_plat-X = 600,000)", completed.CoinsReward)
		}
	})

	t.Run("ZeroPayableOrder_EarnsZeroCoins", func(t *testing.T) {
		h := newTestHarness()
		buyerCtx := ctxWithUser("buyer-1", "buyer", "")
		sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{
					ShopId:                     "shop-1",
					SubtotalVnd:                1000000,
					PlatformVoucherDiscountVnd: 500000,
					FreeshipDiscountVnd:        30000,
					CoinsDiscountVnd:           500000,
					ShippingFeeVnd:             30000,
				},
			},
		}

		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "idem-prmb2-zero",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-p",
			FreeshipVoucherId: "v-f",
			CoinsToUse:        500000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout failed: %v", err)
		}
		ordID := resp.Orders[0].Id
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-prmb2-z-deliv", events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "delivered",
		})
		if _, err := h.svc.BuyerConfirmReceived(buyerCtx, &orderv1.BuyerConfirmReceivedRequest{OrderId: ordID}); err != nil {
			t.Fatalf("BuyerConfirmReceived failed: %v", err)
		}

		var completed events.OrderCompletedPayload
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderCompleted {
				_ = json.Unmarshal(ev.PayloadJSON, &completed)
			}
		}
		if completed.CoinsReward != 0 {
			t.Fatalf("0 VND order coins_reward = %d, want 0", completed.CoinsReward)
		}
	})
}
