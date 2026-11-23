package service

import (
	"context"
	"testing"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestW2A_SellerShipOrder_TransitionsToPreparingIdempotentAndSetsBuyerID(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "w2a-ship-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	orderID := resp.Orders[0].Id

	ship1, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId: orderID,
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("SellerShipOrder #1: %v", err)
	}
	if ship1.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		t.Fatalf("SellerShipOrder status = %v, want PREPARING", ship1.Order.Status)
	}
	if ship1.TrackingCode == "" || ship1.Order.TrackingCode != ship1.TrackingCode {
		t.Fatalf("expected non-empty tracking code, got %+v", ship1)
	}
	if len(h.shipping.createShipCalls) != 1 {
		t.Fatalf("expected 1 CreateShipment call, got %d", len(h.shipping.createShipCalls))
	}
	if h.shipping.createShipCalls[0].GetBuyerId() != "buyer-1" {
		t.Fatalf("CreateShipment.buyer_id = %q, want buyer-1", h.shipping.createShipCalls[0].GetBuyerId())
	}
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderShipped {
			t.Fatalf("SellerShipOrder must NOT emit order.shipped before carrier pickup")
		}
	}

	// Second call when already PREPARING with tracking_code always calls CreateShipment (idempotent on shipping side)
	// and preserves the same live shipment without duplicating timeline rows.
	tlCountBefore := len(ship1.Order.Timeline)
	ship2, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{
		OrderId: orderID,
		ShopId:  "shop-1",
	})
	if err != nil {
		t.Fatalf("SellerShipOrder #2 (idempotent): %v", err)
	}
	if ship2.TrackingCode != ship1.TrackingCode || ship2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		t.Fatalf("idempotent SellerShipOrder mismatch: got %+v want tracking=%s", ship2, ship1.TrackingCode)
	}
	if len(h.shipping.createShipCalls) != 2 {
		t.Fatalf("SellerShipOrder must always call CreateShipment (idempotent on shipping), calls=%d", len(h.shipping.createShipCalls))
	}
	if len(ship2.Order.Timeline) != tlCountBefore {
		t.Fatalf("idempotent replay with unchanged tracking must not duplicate timeline rows: before=%d after=%d", tlCountBefore, len(ship2.Order.Timeline))
	}
}

func TestW2A_ApplyShipmentUpdate_ForwardLifecycleAndDirectDeliveredReturned(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("PickingStaysPreparing_DeliveringMovesToShipping", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-fwd-1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := resp.Orders[0].Id
		shipResp, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		// picking keeps order PREPARING.
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-pick-1", events.ShipmentUpdatedPayload{
			OrderID: ordID, Status: "picking", TrackingCode: shipResp.TrackingCode, Description: "Carrier picking up",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(picking): %v", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			t.Fatalf("status after picking = %v, want PREPARING", got.Order.Status)
		}

		// delivering moves PREPARING -> SHIPPING and emits order.shipped.
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-deliv-1", events.ShipmentUpdatedPayload{
			OrderID: ordID, Status: "delivering", TrackingCode: shipResp.TrackingCode, Description: "Picked up",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivering): %v", err)
		}
		got, _ = h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
			t.Fatalf("status after delivering = %v, want SHIPPING", got.Order.Status)
		}
		shippedCount := 0
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderShipped {
				shippedCount++
			}
		}
		if shippedCount != 1 {
			t.Fatalf("order.shipped outbox count = %d, want 1", shippedCount)
		}
	})

	t.Run("DirectDeliveredFromPreparing_TraversesShippingInOneTx", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-fwd-2",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := resp.Orders[0].Id
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-direct-deliv", events.ShipmentUpdatedPayload{
			OrderID: ordID, Status: "delivered", TrackingCode: shipResp.TrackingCode, Description: "Delivered directly",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
			t.Fatalf("status = %v, want DELIVERED", got.Order.Status)
		}
		if got.Order.DeliveredAt == nil {
			t.Fatalf("expected delivered_at to be populated on Order proto")
		}

		// Verify both PREPARING->SHIPPING and SHIPPING->DELIVERED in timeline & both events in outbox.
		var sawShipTL, sawDelivTL bool
		for _, tl := range got.Order.Timeline {
			if tl.FromStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING && tl.ToStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
				sawShipTL = true
			}
			if tl.FromStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING && tl.ToStatus == orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
				sawDelivTL = true
			}
		}
		if !sawShipTL || !sawDelivTL {
			t.Fatalf("expected both PREPARING->SHIPPING and SHIPPING->DELIVERED timeline rows, got %+v", got.Order.Timeline)
		}
		var sawShipEv, sawDelivEv bool
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderShipped {
				sawShipEv = true
			}
			if ev.EventType == events.SubjectOrderDelivered {
				sawDelivEv = true
			}
		}
		if !sawShipEv || !sawDelivEv {
			t.Fatalf("expected both order.shipped and order.delivered outbox events, ship=%v deliv=%v", sawShipEv, sawDelivEv)
		}
	})

	t.Run("DirectReturnedFromPreparing_TraversesShippingInOneTx", func(t *testing.T) {
		h := newTestHarness()
		resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-fwd-3",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := resp.Orders[0].Id
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-direct-ret", events.ShipmentUpdatedPayload{
			OrderID: ordID, Status: "returned", TrackingCode: shipResp.TrackingCode, Description: "COD rejected",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned): %v", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			t.Fatalf("status = %v, want RETURNED", got.Order.Status)
		}
		var sawShipTL, sawRetTL bool
		for _, tl := range got.Order.Timeline {
			if tl.FromStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING && tl.ToStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
				sawShipTL = true
			}
			if tl.FromStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING && tl.ToStatus == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
				sawRetTL = true
			}
		}
		if !sawShipTL || !sawRetTL {
			t.Fatalf("expected PREPARING->SHIPPING and SHIPPING->RETURNED timeline rows, got %+v", got.Order.Timeline)
		}
		var sawShipEv, sawRetEv bool
		for _, ev := range h.store.OutboxEvents() {
			if ev.EventType == events.SubjectOrderShipped {
				sawShipEv = true
			}
			if ev.EventType == events.SubjectOrderReturned {
				sawRetEv = true
			}
		}
		if !sawShipEv || !sawRetEv {
			t.Fatalf("expected both order.shipped and order.returned outbox events, ship=%v ret=%v", sawShipEv, sawRetEv)
		}
	})
}

func TestW2A_CancelOrder_WithTracking_CallsCancelShipmentServiceToken(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("CancelBeforePickup_CancelsShipmentAndOrder", func(t *testing.T) {
		h := newTestHarness()
		resp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-can-1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := resp.Orders[0].Id
		_, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}

		cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "changed mind"})
		if err != nil {
			t.Fatalf("CancelOrder with tracking before pickup failed: %v", err)
		}
		if cancelResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("order status = %v, want CANCELLED", cancelResp.Order.Status)
		}
		if len(h.shipping.cancelShipCalls) != 1 || h.shipping.cancelShipCalls[0].GetOrderId() != ordID ||
			h.shipping.cancelShipCalls[0].GetDirection() != shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD {
			t.Fatalf("expected 1 CancelShipment(FORWARD) call, got %+v", h.shipping.cancelShipCalls)
		}
		if gotAuth := h.shipping.cancelShipAuth[0]; gotAuth.service != clients.InternalServiceName || gotAuth.rawToken != "" {
			t.Fatalf("CancelShipment must use service token, got %+v", gotAuth)
		}
	})

	t.Run("CancelAfterPickup_FailedPrecondition_LeavesOrderUnchanged", func(t *testing.T) {
		h := newTestHarness()
		resp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-can-2",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := resp.Orders[0].Id
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "carrier already picked up parcel")

		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "too late"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CancelOrder after pickup = %v, want FailedPrecondition (409)", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			t.Fatalf("order status mutated after failed CancelShipment: %v, want PREPARING", got.Order.Status)
		}
	})

	t.Run("CancelWhenShipmentNotFound_ContinuesCancellingOrder", func(t *testing.T) {
		h := newTestHarness()
		resp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "w2a-can-3",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := resp.Orders[0].Id
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		h.shipping.cancelShipErr = status.Error(codes.NotFound, "shipment not found")

		cancelResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "no shipment"})
		if err != nil {
			t.Fatalf("CancelOrder when shipment NotFound should succeed, got %v", err)
		}
		if cancelResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("order status = %v, want CANCELLED", cancelResp.Order.Status)
		}
	})
}

func TestW2A_OrderProto_ReturnsDeliveredAtAndReturnMetadata(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "w2a-meta-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	ordID := resp.Orders[0].Id
	shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-meta-deliv", events.ShipmentUpdatedPayload{
		OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "delivered",
	})

	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
	if got.Order.DeliveredAt == nil {
		t.Fatalf("Order.DeliveredAt = nil, want non-nil timestamp")
	}

	retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ordID,
		Reason:  "damaged",
	})
	if err != nil {
		t.Fatalf("RequestReturn: %v", err)
	}
	got, _ = h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
	if got.Order.ReturnRequestId != retResp.ReturnRequestId {
		t.Fatalf("Order.ReturnRequestId = %q, want %q", got.Order.ReturnRequestId, retResp.ReturnRequestId)
	}
	if got.Order.ReturnStatus != orderv1.ReturnStatus_RETURN_STATUS_PENDING {
		t.Fatalf("Order.ReturnStatus = %v, want RETURN_STATUS_PENDING", got.Order.ReturnStatus)
	}
}

func TestW2A_PaymentRefunded_OnReturnedOrder_UpdatesRefundedVndWithoutChangingStatus(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "w2a-pref-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
	})
	ord := resp.Orders[0]
	_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-pref-ok", events.PaymentSucceededPayload{
		PaymentID: resp.PaymentId, OrderGroupID: resp.CheckoutGroupId,
	})
	shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-pref-ret", events.ShipmentUpdatedPayload{
		OrderID: ord.Id, TrackingCode: shipResp.TrackingCode, Status: "returned",
	})

	// Partial refund on RETURNED order.
	if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-pref-r1", events.PaymentRefundedPayload{
		RefundID: "ref-1", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: 200000, Reason: "partial",
	}); err != nil {
		t.Fatalf("HandlePaymentRefunded #1: %v", err)
	}
	agg, _ := h.store.GetOrderWithDetails(context.Background(), ord.Id)
	if agg.Order.Status != "RETURNED" || agg.Order.RefundedVND != 200000 || agg.Order.PaymentStatus != "SUCCEEDED" {
		t.Fatalf("after partial refund on RETURNED: status=%s refunded=%d payStatus=%s", agg.Order.Status, agg.Order.RefundedVND, agg.Order.PaymentStatus)
	}

	// Remaining refund brings refunded_vnd == total_payable_vnd -> payment_status REFUNDED, status still RETURNED.
	rem := ord.TotalPayableVnd - 200000
	if err := h.svc.HandlePaymentRefunded(context.Background(), "evt-pref-r2", events.PaymentRefundedPayload{
		RefundID: "ref-2", PaymentID: resp.PaymentId, OrderID: ord.Id, AmountVND: rem, Reason: "final",
	}); err != nil {
		t.Fatalf("HandlePaymentRefunded #2: %v", err)
	}
	agg, _ = h.store.GetOrderWithDetails(context.Background(), ord.Id)
	if agg.Order.Status != "RETURNED" || agg.Order.RefundedVND != ord.TotalPayableVnd || agg.Order.PaymentStatus != "REFUNDED" {
		t.Fatalf("after full refund on RETURNED: status=%s refunded=%d payStatus=%s", agg.Order.Status, agg.Order.RefundedVND, agg.Order.PaymentStatus)
	}
}
