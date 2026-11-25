package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	"github.com/nexusmart/core/services/order/internal/events"
)

func TestZaloPay_CheckoutCancelExpiryAndShipmentReturned(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	// 1. ZaloPay checkout starts PENDING with payment_expires_at and maps to PAYMENT_METHOD_ZALOPAY.
	h := newTestHarness()
	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "zp-checkout-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
	})
	if err != nil {
		t.Fatalf("ZaloPay CreateCheckout failed: %v", err)
	}
	if resp.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY ||
		resp.GetPaymentExpiresAt() == nil ||
		resp.GetOrders()[0].GetStatus() != orderv1.OrderStatus_ORDER_STATUS_PENDING {
		t.Fatalf("unexpected ZaloPay checkout response: %+v", resp)
	}
	if len(h.payment.createCalls) != 1 || h.payment.createCalls[0].GetMethod() != paymentv1.PaymentMethod_PAYMENT_METHOD_ZALOPAY {
		t.Fatalf("expected PAYMENT_METHOD_ZALOPAY, got %+v", h.payment.createCalls)
	}

	// Mark succeeded -> CancelOrder -> triggers refund (wasPaid=true) + order.cancelled WasPaid=true!
	if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-zp-ok", events.PaymentSucceededPayload{
		PaymentID: resp.GetPaymentId(), OrderGroupID: resp.GetCheckoutGroupId(),
	}); err != nil {
		t.Fatalf("HandlePaymentSucceeded: %v", err)
	}
	if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
		OrderId: resp.GetOrders()[0].GetId(), Reason: "cancel zalopay",
	}); err != nil {
		t.Fatalf("CancelOrder paid ZaloPay: %v", err)
	}
	if len(h.payment.refundCalls) != 1 {
		t.Fatalf("expected 1 CreateRefund for paid ZaloPay cancel, got %d", len(h.payment.refundCalls))
	}
	var foundPaidCancel bool
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderCancelled {
			var p events.OrderCancelledPayload
			_ = json.Unmarshal(ev.PayloadJSON, &p)
			if p.OrderID == resp.GetOrders()[0].GetId() && p.WasPaid {
				foundPaidCancel = true
			}
		}
	}
	if !foundPaidCancel {
		t.Fatalf("expected order.cancelled with WasPaid=true for paid ZaloPay order")
	}

	// 2. ZaloPay expiry watchdog cancels unpaid expired PENDING ZaloPay orders.
	h2 := newTestHarness()
	h2.payment.getPaymentStatus = paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING
	resp2, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "zp-exp-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
	})
	if err != nil {
		t.Fatalf("ZaloPay checkout 2: %v", err)
	}
	h2.store.SetOrderPaymentExpiresAt(resp2.GetOrders()[0].GetId(), time.Now().UTC().Add(-2*time.Minute))
	n, err := h2.svc.ScanExpiredPendingOrders(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ScanExpiredPendingOrders ZaloPay: n=%d err=%v", n, err)
	}
	got2, _ := h2.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: resp2.GetOrders()[0].GetId()})
	if got2.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		t.Fatalf("expected expired ZaloPay order CANCELLED, got %v", got2.GetOrder().GetStatus())
	}

	// 3. ZaloPay forward shipment returned triggers refund (wasPaidOnline=true).
	h3 := newTestHarness()
	resp3, err := h3.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "zp-ship-ret",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
	})
	if err != nil {
		t.Fatalf("ZaloPay checkout 3: %v", err)
	}
	_ = h3.svc.HandlePaymentSucceeded(context.Background(), "evt-zp-3", events.PaymentSucceededPayload{
		PaymentID: resp3.GetPaymentId(), OrderGroupID: resp3.GetCheckoutGroupId(),
	})
	ship3, err := h3.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: resp3.GetOrders()[0].GetId()})
	if err != nil {
		t.Fatalf("SellerShipOrder ZaloPay: %v", err)
	}
	if err := h3.svc.HandleShipmentUpdated(context.Background(), "evt-zp-ship-ret", events.ShipmentUpdatedPayload{
		OrderID:      resp3.GetOrders()[0].GetId(),
		Status:       "returned",
		TrackingCode: ship3.GetTrackingCode(),
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(returned): %v", err)
	}
	if len(h3.payment.refundCalls) != 1 {
		t.Fatalf("expected refund on returned paid ZaloPay shipment, got %d", len(h3.payment.refundCalls))
	}
}
