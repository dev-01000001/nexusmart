package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/services/order/internal/events"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createDeliveredOrder(
	t *testing.T,
	h *testHarness,
	idemKey string,
	method orderv1.OrderPaymentMethod,
	items []*orderv1.CheckoutItemInput,
	coins int64,
	platVoucher string,
) (*orderv1.CreateCheckoutResponse, *orderv1.Order) {
	t.Helper()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey:    idemKey,
		BuyerId:           "buyer-1",
		AddressId:         "addr-buyer-1",
		Items:             items,
		CoinsToUse:        coins,
		PlatformVoucherId: platVoucher,
		PaymentMethod:     method,
	})
	if err != nil {
		t.Fatalf("CreateCheckout(%s) failed: %v", idemKey, err)
	}
	if method != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD {
		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-pay-"+idemKey, events.PaymentSucceededPayload{
			PaymentID:    resp.GetPaymentId(),
			OrderGroupID: resp.GetCheckoutGroupId(),
			ProviderTxID: "TX-" + idemKey,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded failed: %v", err)
		}
	}

	ord := resp.GetOrders()[0]
	if _, err := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{
		OrderId: ord.GetId(),
		ShopId:  ord.GetShopId(),
	}); err != nil {
		t.Fatalf("SellerShipOrder failed: %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-deliv-"+idemKey, events.ShipmentUpdatedPayload{
		OrderID:      ord.GetId(),
		Status:       "delivered",
		TrackingCode: "TRK-" + idemKey,
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(delivered) failed: %v", err)
	}
	got, err := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.GetId()})
	if err != nil {
		t.Fatalf("GetOrder failed: %v", err)
	}
	return resp, got.GetOrder()
}

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
	_, _ = h3.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: resp3.GetOrders()[0].GetId()})
	if err := h3.svc.HandleShipmentUpdated(context.Background(), "evt-zp-ship-ret", events.ShipmentUpdatedPayload{
		OrderID: resp3.GetOrders()[0].GetId(), Status: "returned",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(returned): %v", err)
	}
	if len(h3.payment.refundCalls) != 1 {
		t.Fatalf("expected refund on returned paid ZaloPay shipment, got %d", len(h3.payment.refundCalls))
	}
}

func TestRequestReturn_FullPartialValidationAndOutbox(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithUser("buyer-2", "buyer", "")

	// Order with 2 units of sku-1 (2,000,000 + 30,000 shipping = 2,030,000).
	_, ord := createDeliveredOrder(t, h, "ret-partial-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		[]*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 2}}, 0, "")

	// Validation errors: empty order_id, empty reason, invalid item, qty <= 0, qty > ordered, wrong buyer.
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{Reason: "x"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing order_id: want InvalidArgument, got %v", err)
	}
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.GetId()}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing reason: want InvalidArgument, got %v", err)
	}
	if _, err := h.svc.RequestReturn(otherBuyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.GetId(), Reason: "hack"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer RequestReturn: want PermissionDenied, got %v", err)
	}
	badItemCases := [][]*orderv1.ReturnItemInput{
		{{SkuId: "sku-nonexistent", Quantity: 1}},
		{{OrderItemId: "bad-item-id", Quantity: 1}},
		{{OrderItemId: ord.GetItems()[0].GetId(), SkuId: "wrong-sku", Quantity: 1}},
		{{SkuId: "sku-1", Quantity: 0}},
		{{SkuId: "sku-1", Quantity: 3}},
		{{}},
	}
	for i, items := range badItemCases {
		if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
			OrderId: ord.GetId(), Reason: "bad", Items: items,
		}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("bad items case %d: want InvalidArgument, got %v", i, err)
		}
	}

	// Partial return: 1 of 2 units -> proportional integer VND = (2,030,000 * 1,000,000) / 2,000,000 = 1,015,000.
	retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId:      ord.GetId(),
		Reason:       "one unit scratched",
		EvidenceUrls: []string{"https://cdn.local/scratch.jpg"},
		Items:        []*orderv1.ReturnItemInput{{OrderItemId: ord.GetItems()[0].GetId(), Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("partial RequestReturn failed: %v", err)
	}
	if retResp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED {
		t.Fatalf("expected RETURN_REQUESTED, got %v", retResp.GetOrder().GetStatus())
	}
	rr := retResp.GetReturnRequest()
	if rr == nil || rr.GetId() != retResp.GetReturnRequestId() || rr.GetShopId() != "shop-1" {
		t.Fatalf("unexpected ReturnRequest: %+v", rr)
	}
	if rr.GetRefundAmountVnd() != 1015000 || len(rr.GetItems()) != 1 || rr.GetItems()[0].GetQuantity() != 1 {
		t.Fatalf("unexpected partial refund math/items: amount=%d items=%+v", rr.GetRefundAmountVnd(), rr.GetItems())
	}
	if len(rr.GetPhotos()) != 1 || rr.GetPhotos()[0] != "https://cdn.local/scratch.jpg" {
		t.Fatalf("unexpected return photos: %+v", rr.GetPhotos())
	}

	// Verify order.return_requested outbox emitted.
	foundOutbox := false
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderReturnRequested {
			var p events.OrderReturnRequestedPayload
			if err := json.Unmarshal(ev.PayloadJSON, &p); err == nil && p.ReturnRequestID == rr.GetId() && p.RefundAmountVND == 1015000 {
				foundOutbox = true
			}
		}
	}
	if !foundOutbox {
		t.Fatalf("expected order.return_requested outbox event")
	}

	// 7-day return window expiry check on a second order.
	_, ord2 := createDeliveredOrder(t, h, "ret-expired-window", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		[]*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}}, 0, "")
	h.svc.nowFn = func() time.Time { return time.Now().UTC().Add(8 * 24 * time.Hour) }
	if _, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ord2.GetId(), Reason: "too late",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expired return window: want FailedPrecondition, got %v", err)
	}
}

func TestReturnSaga_ApproveImmediateRefundAndMultiShopSiblingPromotion(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithUser("seller-2", "seller", "shop-2")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	h.promo.breakdown = &promotionv1.DiscountBreakdown{
		Shops: []*promotionv1.ShopDiscountBreakdown{
			{ShopId: "shop-1", SubtotalVnd: 1000000, CoinsDiscountVnd: 5000, ShippingFeeVnd: 30000},
			{ShopId: "shop-2", SubtotalVnd: 500000, CoinsDiscountVnd: 5000, ShippingFeeVnd: 30000},
		},
	}

	// Multi-shop ZaloPay checkout (shop-1 + shop-2) with coins & platform voucher.
	resp, ord1 := createDeliveredOrder(t, h, "ret-imm-multi", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		[]*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}, {SkuId: "sku-2", Quantity: 1}}, 10000, "v-plat-1")

	ret1, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ord1.GetId(),
		Reason:  "wrong item sent",
	})
	if err != nil {
		t.Fatalf("RequestReturn ord1 failed: %v", err)
	}

	// RBAC checks on ApproveReturn.
	if _, err := h.svc.ApproveReturn(buyerCtx, &orderv1.ApproveReturnRequest{ReturnRequestId: ret1.GetReturnRequestId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ApproveReturn: want PermissionDenied, got %v", err)
	}
	if _, err := h.svc.ApproveReturn(seller2Ctx, &orderv1.ApproveReturnRequest{ReturnRequestId: ret1.GetReturnRequestId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other seller ApproveReturn: want PermissionDenied, got %v", err)
	}

	// ApproveReturn with immediate_refund=true while shop-2 sibling is still CONFIRMED (active!).
	appResp, err := h.svc.ApproveReturn(seller1Ctx, &orderv1.ApproveReturnRequest{
		ReturnRequestId: ret1.GetReturnRequestId(),
		Note:            "keep item, refunding immediately",
		ImmediateRefund: true,
	})
	if err != nil {
		t.Fatalf("ApproveReturn(immediate_refund=true) failed: %v", err)
	}
	if appResp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("expected REFUNDED, got %v", appResp.GetOrder().GetStatus())
	}
	if appResp.GetReturnRequest().GetStatus() != orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED ||
		appResp.GetReturnRequest().GetRefundId() == "" {
		t.Fatalf("expected ReturnRequest REFUNDED with refund_id, got %+v", appResp.GetReturnRequest())
	}

	// Verify Payment.CreateRefund used idempotency key "order-return-refund:<rrID>".
	if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetIdempotencyKey() != "order-return-refund:"+ret1.GetReturnRequestId() {
		t.Fatalf("unexpected refund calls: %+v", h.payment.refundCalls)
	}
	// Active sibling shop-2 still open -> ReleasePromotion MUST NOT have been called yet, and order.refunded has all_group_orders_closed=false!
	if len(h.promo.releaseCalls) != 0 {
		t.Fatalf("ReleasePromotion must not run while active sibling remains, got %v", h.promo.releaseCalls)
	}
	if len(h.product.updateStockCalls) != 1 || h.product.updateStockCalls[0].GetSkuId() != "sku-1" {
		t.Fatalf("expected restock of sku-1, got %+v", h.product.updateStockCalls)
	}

	var sawApproved, sawRefunded bool
	for _, ev := range h.store.OutboxEvents() {
		switch ev.EventType {
		case events.SubjectOrderReturnApproved:
			sawApproved = true
		case events.SubjectOrderRefunded:
			var p events.OrderRefundedPayload
			if err := json.Unmarshal(ev.PayloadJSON, &p); err != nil {
				t.Fatalf("unmarshal order.refunded: %v", err)
			}
			if p.OrderID == ord1.GetId() {
				if p.AllGroupOrdersClosed || p.CoinsDiscountVND != 5000 || p.ReturnRequestID != ret1.GetReturnRequestId() {
					t.Fatalf("unexpected order.refunded payload for ord1: %+v", p)
				}
				sawRefunded = true
			}
		}
	}
	if !sawApproved || !sawRefunded {
		t.Fatalf("expected both order.return_approved and order.refunded outbox events, saw approved=%v refunded=%v", sawApproved, sawRefunded)
	}

	// Now ship/deliver/return shop-2 and approve immediate refund -> ALL siblings closed -> ReleasePromotion MUST run!
	ord2 := resp.GetOrders()[1]
	_, _ = h.svc.SellerShipOrder(seller2Ctx, &orderv1.SellerShipOrderRequest{OrderId: ord2.GetId()})
	_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-deliv-ord2", events.ShipmentUpdatedPayload{
		OrderID: ord2.GetId(), Status: "delivered",
	})
	ret2, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord2.GetId(), Reason: "defect"})
	if err != nil {
		t.Fatalf("RequestReturn ord2: %v", err)
	}
	if _, err := h.svc.ApproveReturn(adminCtx, &orderv1.ApproveReturnRequest{
		ReturnRequestId: ret2.GetReturnRequestId(),
		ImmediateRefund: true,
	}); err != nil {
		t.Fatalf("admin ApproveReturn ord2: %v", err)
	}
	if len(h.promo.releaseCalls) != 1 || h.promo.releaseCalls[0] != resp.GetCheckoutGroupId() {
		t.Fatalf("expected ReleasePromotion(%s) once all group orders closed, got %v", resp.GetCheckoutGroupId(), h.promo.releaseCalls)
	}

	var ord2RefundClosed bool
	for _, ev := range h.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderRefunded {
			var p events.OrderRefundedPayload
			_ = json.Unmarshal(ev.PayloadJSON, &p)
			if p.OrderID == ord2.GetId() && p.AllGroupOrdersClosed {
				ord2RefundClosed = true
			}
		}
	}
	if !ord2RefundClosed {
		t.Fatalf("expected order.refunded for ord2 with all_group_orders_closed=true")
	}

	// ListOrderReturns RBAC & filters.
	allAdmin, err := h.svc.ListOrderReturns(adminCtx, &orderv1.ListOrderReturnsRequest{
		Status: orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REFUNDED,
	})
	if err != nil || allAdmin.GetTotal() != 2 {
		t.Fatalf("admin ListOrderReturns: total=%d err=%v", allAdmin.GetTotal(), err)
	}
	s1List, err := h.svc.ListOrderReturns(seller1Ctx, &orderv1.ListOrderReturnsRequest{})
	if err != nil || s1List.GetTotal() != 1 || s1List.GetReturns()[0].GetShopId() != "shop-1" {
		t.Fatalf("seller1 ListOrderReturns: %+v err=%v", s1List, err)
	}
	if _, err := h.svc.ListOrderReturns(seller1Ctx, &orderv1.ListOrderReturnsRequest{ShopId: "shop-2"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller1 listing shop-2 returns: want PermissionDenied, got %v", err)
	}
	buyerList, err := h.svc.ListOrderReturns(buyerCtx, &orderv1.ListOrderReturnsRequest{OrderId: ord1.GetId()})
	if err != nil || buyerList.GetTotal() != 1 {
		t.Fatalf("buyer ListOrderReturns by order_id: %+v err=%v", buyerList, err)
	}
	if _, err := h.svc.ListOrderReturns(buyerCtx, &orderv1.ListOrderReturnsRequest{ShopId: "shop-1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer listing returns by shop_id: want PermissionDenied, got %v", err)
	}
	if _, err := h.svc.ListOrderReturns(ctxWithUser("buyer-2", "buyer", ""), &orderv1.ListOrderReturnsRequest{OrderId: ord1.GetId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer listing ord1 returns: want PermissionDenied, got %v", err)
	}
}

func TestReturnSaga_ReverseShipmentAndConfirmReturnReceivedIdempotency(t *testing.T) {
	// Path A: ApproveReturn(immediate=false) -> reverse shipment -> HandleShipmentUpdated(direction="return", status="returned") -> REFUNDED
	// followed by ConfirmReturnReceived -> idempotent no-op!
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	_, ord := createDeliveredOrder(t, h, "ret-rev-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		[]*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}}, 0, "")

	ret, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
		OrderId: ord.GetId(), Reason: "broken switch",
	})
	if err != nil {
		t.Fatalf("RequestReturn: %v", err)
	}

	app, err := h.svc.ApproveReturn(sellerCtx, &orderv1.ApproveReturnRequest{
		ReturnRequestId: ret.GetReturnRequestId(),
		CarrierCode:     "ghtk",
		ServiceCode:     "ROAD",
		Note:            "ship it back",
	})
	if err != nil {
		t.Fatalf("ApproveReturn: %v", err)
	}
	if app.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_RETURNING ||
		app.GetReturnRequest().GetStatus() != orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_RETURNING ||
		app.GetReturnRequest().GetReverseTrackingCode() == "" ||
		app.GetReturnRequest().GetReverseLabelUrl() == "" {
		t.Fatalf("unexpected ApproveReturn returning result: %+v", app)
	}

	// Intermediate reverse shipment event does not clobber forward tracking code or mark delivered.
	origTracking := app.GetOrder().GetTrackingCode()
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-rev-picking", events.ShipmentUpdatedPayload{
		OrderID:         ord.GetId(),
		Status:          "picking",
		Direction:       "return",
		ReturnRequestID: ret.GetReturnRequestId(),
		TrackingCode:    app.GetReturnRequest().GetReverseTrackingCode(),
		Description:     "Courier picked up return parcel",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(return/picking): %v", err)
	}
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-rev-deliv-ignore", events.ShipmentUpdatedPayload{
		OrderID:   ord.GetId(),
		Status:    "delivered",
		Direction: "return",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(return/delivered): %v", err)
	}
	mid, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.GetId()})
	if mid.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_RETURNING || mid.GetOrder().GetTrackingCode() != origTracking {
		t.Fatalf("intermediate reverse shipment mutated order unexpectedly: status=%v tracking=%s", mid.GetOrder().GetStatus(), mid.GetOrder().GetTrackingCode())
	}

	// Reverse shipment returned -> RETURNING -> RETURNED -> REFUNDED + order.refunded outbox!
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-rev-returned", events.ShipmentUpdatedPayload{
		OrderID:         ord.GetId(),
		Status:          "returned",
		Direction:       "return",
		ReturnRequestID: ret.GetReturnRequestId(),
		Description:     "Returned to seller warehouse",
	}); err != nil {
		t.Fatalf("HandleShipmentUpdated(return/returned): %v", err)
	}

	// Duplicate event_id -> idempotent no-op!
	if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-rev-returned", events.ShipmentUpdatedPayload{
		OrderID:         ord.GetId(),
		Status:          "returned",
		Direction:       "return",
		ReturnRequestID: ret.GetReturnRequestId(),
	}); err != nil {
		t.Fatalf("duplicate HandleShipmentUpdated: %v", err)
	}

	// Subsequent ConfirmReturnReceived -> idempotent no-op (no double refund/restock)!
	confResp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{
		ReturnRequestId: ret.GetReturnRequestId(),
		Note:            "late confirm",
	})
	if err != nil || confResp.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("idempotent ConfirmReturnReceived: resp=%+v err=%v", confResp, err)
	}
	if len(h.payment.refundCalls) != 1 || len(h.product.updateStockCalls) != 1 {
		t.Fatalf("expected exactly 1 refund and 1 restock, got refund=%d restock=%d",
			len(h.payment.refundCalls), len(h.product.updateStockCalls))
	}

	// Path B: Seller explicitly calls ConfirmReturnReceived while order is RETURNING.
	h2 := newTestHarness()
	_, ord2 := createDeliveredOrder(t, h2, "ret-confirm-direct", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
		[]*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}}, 0, "")
	ret2, _ := h2.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord2.GetId(), Reason: "defect"})
	_, _ = h2.svc.ApproveReturn(sellerCtx, &orderv1.ApproveReturnRequest{ReturnRequestId: ret2.GetReturnRequestId()})

	if _, err := h2.svc.ConfirmReturnReceived(buyerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: ret2.GetReturnRequestId()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ConfirmReturnReceived: want PermissionDenied, got %v", err)
	}
	conf2, err := h2.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{
		ReturnRequestId: ret2.GetReturnRequestId(),
		Note:            "checked ok",
	})
	if err != nil || conf2.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("ConfirmReturnReceived failed: resp=%+v err=%v", conf2, err)
	}
	if len(h2.payment.refundCalls) != 1 || len(h2.product.updateStockCalls) != 1 {
		t.Fatalf("expected refund + restock on ConfirmReturnReceived")
	}
}

func TestRejectReturn_AutoComplaintCreateUpdateListAndAllResolutions(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	otherBuyerCtx := ctxWithUser("buyer-2", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")
	superAdminCtx := ctxWithUser("super-1", "super_admin", "")

	// Helper to reach RETURN_REJECTED with auto-created OPEN complaint.
	setupRejected := func(t *testing.T, h *testHarness, key string) (*orderv1.Order, *orderv1.ReturnRequest, *orderv1.Complaint) {
		t.Helper()
		_, ord := createDeliveredOrder(t, h, key, orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_ZALOPAY,
			[]*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}}, 0, "")
		ret, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{
			OrderId:      ord.GetId(),
			Reason:       "not as described",
			EvidenceUrls: []string{"https://cdn.local/e1.jpg"},
		})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		rej, err := h.svc.RejectReturn(sellerCtx, &orderv1.RejectReturnRequest{
			ReturnRequestId: ret.GetReturnRequestId(),
			Reason:          "seal broken by buyer",
		})
		if err != nil {
			t.Fatalf("RejectReturn: %v", err)
		}
		if rej.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED ||
			rej.GetReturnRequest().GetStatus() != orderv1.ReturnRequestStatus_RETURN_REQUEST_STATUS_REJECTED ||
			rej.GetComplaint() == nil ||
			rej.GetComplaint().GetStatus() != orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN {
			t.Fatalf("unexpected RejectReturn response: %+v", rej)
		}
		return rej.GetOrder(), rej.GetReturnRequest(), rej.GetComplaint()
	}

	// 1. RejectReturn validation + RBAC + CreateComplaint idempotent update + ListComplaints RBAC + Resolve REFUND (immediate).
	h1 := newTestHarness()
	ord1, rr1, cp1 := setupRejected(t, h1, "cmp-refund-imm")

	// RejectReturn empty reason & RBAC checks on fresh rejected attempt.
	if _, err := h1.svc.RejectReturn(sellerCtx, &orderv1.RejectReturnRequest{ReturnRequestId: rr1.GetId(), Reason: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty reject reason: want InvalidArgument, got %v", err)
	}
	if _, err := h1.svc.RejectReturn(buyerCtx, &orderv1.RejectReturnRequest{ReturnRequestId: rr1.GetId(), Reason: "no"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer RejectReturn: want PermissionDenied, got %v", err)
	}

	// Buyer updates evidence/reason on the auto-created OPEN complaint idempotently (same complaint ID!).
	upCP, err := h1.svc.CreateComplaint(buyerCtx, &orderv1.CreateComplaintRequest{
		OrderId:      ord1.GetId(),
		Reason:       "unboxing video proves defect",
		EvidenceUrls: []string{"https://cdn.local/video.mp4"},
	})
	if err != nil {
		t.Fatalf("CreateComplaint update failed: %v", err)
	}
	if upCP.GetComplaint().GetId() != cp1.GetId() ||
		!strings.Contains(upCP.GetComplaint().GetReason(), "unboxing video") ||
		len(upCP.GetComplaint().GetEvidenceUrls()) != 1 {
		t.Fatalf("expected idempotent update on existing OPEN complaint, got %+v", upCP.GetComplaint())
	}
	if _, err := h1.svc.CreateComplaint(otherBuyerCtx, &orderv1.CreateComplaintRequest{
		OrderId: ord1.GetId(), Reason: "spoof",
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("other buyer CreateComplaint: want PermissionDenied, got %v", err)
	}

	// ListComplaints: 403 for buyer & seller, allowed for admin & super_admin!
	if _, err := h1.svc.ListComplaints(buyerCtx, &orderv1.ListComplaintsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ListComplaints: want PermissionDenied, got %v", err)
	}
	if _, err := h1.svc.ListComplaints(sellerCtx, &orderv1.ListComplaintsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller ListComplaints: want PermissionDenied, got %v", err)
	}
	listResp, err := h1.svc.ListComplaints(superAdminCtx, &orderv1.ListComplaintsRequest{
		Status:  orderv1.ComplaintStatus_COMPLAINT_STATUS_OPEN,
		OrderId: ord1.GetId(),
		ShopId:  "shop-1",
	})
	if err != nil || listResp.GetTotal() != 1 || listResp.GetComplaints()[0].GetId() != cp1.GetId() {
		t.Fatalf("super_admin ListComplaints: %+v err=%v", listResp, err)
	}

	// ResolveComplaint RBAC: 403 for buyer & seller.
	if _, err := h1.svc.ResolveComplaint(buyerCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId: cp1.GetId(), Resolution: orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("buyer ResolveComplaint: want PermissionDenied, got %v", err)
	}
	if _, err := h1.svc.ResolveComplaint(sellerCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId: cp1.GetId(), Resolution: orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS,
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("seller ResolveComplaint: want PermissionDenied, got %v", err)
	}

	// ResolveComplaint: REFUND (require_return_shipping=false) -> immediate Refund Saga -> REFUNDED + both outbox events!
	res1, err := h1.svc.ResolveComplaint(adminCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId:           cp1.GetId(),
		Resolution:            orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND,
		AdminNote:             "buyer evidence valid, instant refund",
		RequireReturnShipping: false,
	})
	if err != nil {
		t.Fatalf("ResolveComplaint(REFUND immediate) failed: %v", err)
	}
	if res1.GetComplaint().GetStatus() != orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED ||
		res1.GetComplaint().GetResolution() != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND ||
		res1.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_REFUNDED {
		t.Fatalf("unexpected ResolveComplaint(REFUND immediate): %+v", res1)
	}
	if len(h1.payment.refundCalls) != 1 || len(h1.product.updateStockCalls) != 1 {
		t.Fatalf("expected refund + restock on immediate complaint refund")
	}

	// Double-resolve must fail with FailedPrecondition, and CreateComplaint on closed complaint fails too.
	if _, err := h1.svc.ResolveComplaint(adminCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId: cp1.GetId(), Resolution: orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS,
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("double ResolveComplaint: want FailedPrecondition, got %v", err)
	}
	if _, err := h1.svc.CreateComplaint(buyerCtx, &orderv1.CreateComplaintRequest{
		ReturnRequestId: rr1.GetId(), Reason: "reopen",
	}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("CreateComplaint on closed complaint: want FailedPrecondition, got %v", err)
	}

	// 2. ResolveComplaint: REFUND with require_return_shipping=true -> creates reverse shipment -> RETURNING!
	h2 := newTestHarness()
	_, _, cp2 := setupRejected(t, h2, "cmp-refund-ship")
	res2, err := h2.svc.ResolveComplaint(adminCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId:           cp2.GetId(),
		Resolution:            orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REFUND,
		AdminNote:             "return item first",
		RequireReturnShipping: true,
	})
	if err != nil {
		t.Fatalf("ResolveComplaint(REFUND+shipping) failed: %v", err)
	}
	if res2.GetComplaint().GetStatus() != orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED ||
		res2.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_RETURNING ||
		len(h2.shipping.createReturnShipCalls) != 1 {
		t.Fatalf("unexpected ResolveComplaint(REFUND+shipping): %+v", res2)
	}

	// 3. ResolveComplaint: REPLACE -> creates reverse shipment -> RETURNING!
	h3 := newTestHarness()
	_, _, cp3 := setupRejected(t, h3, "cmp-replace")
	res3, err := h3.svc.ResolveComplaint(adminCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId: cp3.GetId(),
		Resolution:  orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE,
		AdminNote:   "send replacement after return",
	})
	if err != nil {
		t.Fatalf("ResolveComplaint(REPLACE) failed: %v", err)
	}
	if res3.GetComplaint().GetStatus() != orderv1.ComplaintStatus_COMPLAINT_STATUS_RESOLVED ||
		res3.GetComplaint().GetResolution() != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_REPLACE ||
		res3.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_RETURNING ||
		len(h3.shipping.createReturnShipCalls) != 1 {
		t.Fatalf("unexpected ResolveComplaint(REPLACE): %+v", res3)
	}

	// 4. ResolveComplaint: DISMISS -> complaint=DISMISSED, order=COMPLETED, emits order.completed, calls ReleaseEscrow!
	h4 := newTestHarness()
	ord4, _, cp4 := setupRejected(t, h4, "cmp-dismiss")
	res4, err := h4.svc.ResolveComplaint(adminCtx, &orderv1.ResolveComplaintRequest{
		ComplaintId: cp4.GetId(),
		Resolution:  orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS,
		AdminNote:   "buyer claim unsubstantiated",
	})
	if err != nil {
		t.Fatalf("ResolveComplaint(DISMISS) failed: %v", err)
	}
	if res4.GetComplaint().GetStatus() != orderv1.ComplaintStatus_COMPLAINT_STATUS_DISMISSED ||
		res4.GetComplaint().GetResolution() != orderv1.ComplaintResolution_COMPLAINT_RESOLUTION_DISMISS ||
		res4.GetOrder().GetStatus() != orderv1.OrderStatus_ORDER_STATUS_COMPLETED {
		t.Fatalf("unexpected ResolveComplaint(DISMISS): %+v", res4)
	}
	if len(h4.payment.releaseEscrowCalls) != 1 || h4.payment.releaseEscrowCalls[0].GetOrderId() != ord4.GetId() {
		t.Fatalf("expected ReleaseEscrow(%s) on complaint dismissal, got %+v", ord4.GetId(), h4.payment.releaseEscrowCalls)
	}
	foundCompleted := false
	for _, ev := range h4.store.OutboxEvents() {
		if ev.EventType == events.SubjectOrderCompleted {
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Fatalf("expected order.completed outbox event on complaint dismissal")
	}
}
