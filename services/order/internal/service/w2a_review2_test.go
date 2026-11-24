package service

import (
	"context"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Review 2 P0-1: Exhaustive race interleavings between CancelOrder / 48h auto-cancel and SellerShipOrder.
func TestReview2_P0_1_CancelVsSellerShipOrderAllInterleavings(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	// Interleaving 1: MarkCreating -> Cancel(NotFound) -> CreateShipment + TransitionOrderPreparedForShipment(TRK1) + pickup -> TransitionOrderStatus MUST NOT commit CANCELLED!
	t.Run("MarkCreating_CancelNotFound_CreateShipmentAndPickup_NeverCancelsOrRefunds", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &interceptStore{Store: h.store}
		h.svc.store = wrapped

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p01-case1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-r2-p01-pay1", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		})

		// Step 1: Seller starts creating shipment (pending_shipment_cancel=true, tracking="").
		if _, err := h.store.MarkOrderShipmentCreating(context.Background(), ordID, time.Now().UTC()); err != nil {
			t.Fatalf("MarkOrderShipmentCreating: %v", err)
		}

		// Step 2: CancelOrder's first CancelShipment sees NotFound (shipping hasn't inserted yet),
		// then Seller commits TRK-RACE-1 AND carrier immediately picks up before CancelOrder's TransitionOrderStatus!
		cancelCalls := 0
		h.shipping.onCancelShipment = func(_ *shippingv1.CancelShipmentRequest) error {
			cancelCalls++
			if cancelCalls == 1 {
				return status.Error(codes.NotFound, "shipment not created yet")
			}
			return status.Error(codes.FailedPrecondition, "carrier already picked up TRK-RACE-1")
		}
		sellerCommitted := false
		wrapped.onTransitionOrderStatus = func(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
			if !sellerCommitted && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				sellerCommitted = true
				_, sErr := h.store.TransitionOrderPreparedForShipment(ctx, store.ShipOrderParams{
					OrderID:      ordID,
					CarrierCode:  "ghn",
					ServiceCode:  "standard",
					TrackingCode: "TRK-RACE-1",
					LabelURL:     "https://labels.sandbox/r2-p01",
					ActorID:      "seller-1",
					ActorRole:    "seller",
					Note:         "Seller created shipment",
					Now:          time.Now().UTC(),
				})
				if sErr != nil {
					t.Errorf("TransitionOrderPreparedForShipment: %v", sErr)
				}
			}
			return h.store.TransitionOrderStatus(ctx, params)
		}

		_, err = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel race"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CancelOrder = %v, want FailedPrecondition (carrier picked up live shipment)", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("order MUST NOT be CANCELLED when live shipment was picked up")
		}
		if len(h.payment.refundCalls) != 0 {
			t.Fatalf("MUST NOT refund when shipment was picked up, got %d refunds", len(h.payment.refundCalls))
		}
	})

	// Interleaving 1b: MarkCreating stays in flight (CancelShipment returns NotFound 3 times) -> 409 "vận đơn đang được tạo, thử lại".
	t.Run("MarkCreating_InFlight3Times_Returns409TryAgain", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p01-case1b",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id
		_, _ = h.store.MarkOrderShipmentCreating(context.Background(), ordID, time.Now().UTC())
		h.shipping.cancelShipErr = status.Error(codes.NotFound, "shipment still being created")

		_, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel while creating"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CancelOrder while shipment creating = %v, want FailedPrecondition", err)
		}
		if !strings.Contains(err.Error(), "vận đơn đang được tạo") {
			t.Fatalf("expected Vietnamese retry message, got %q", err.Error())
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("order status = %v, want CONFIRMED", got.Order.Status)
		}
	})

	// Interleaving 2: MarkCreating -> CreateShipment(TRK2) -> CancelOrder before TransitionOrderPreparedForShipment.
	t.Run("MarkCreating_CreateShipment_CancelBeforeTransition", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p01-case2",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id

		// SellerShipOrder creates shipment; before TransitionOrderPreparedForShipment runs, buyer cancels!
		h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			// Shipment TRK-LIVE-2 now exists in shipping (not yet picked up) -> buyer cancel cancels it cleanly.
			if _, cErr := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "buyer cancel"}); cErr != nil {
				t.Errorf("CancelOrder before transition: %v", cErr)
			}
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId: "shp-2", OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard",
					TrackingCode: "TRK-LIVE-2", LabelUrl: "https://labels.sandbox/2",
				},
			}, nil
		}

		_, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SellerShipOrder after concurrent cancel = %v, want FailedPrecondition", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("order status = %v, want CANCELLED", got.Order.Status)
		}
	})

	// Interleaving 3: Cancel BEFORE MarkCreating -> SellerShipOrder blocked before CreateShipment!
	t.Run("CancelBeforeMarkCreating_BlocksCreateShipment", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p01-case3",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel first"}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SellerShipOrder on CANCELLED = %v, want FailedPrecondition", err)
		}
		if len(h.shipping.createShipCalls) != 0 {
			t.Fatalf("CreateShipment must NOT be called after CancelOrder, got %d calls", len(h.shipping.createShipCalls))
		}
	})

	// Interleaving 4: 48h auto-cancel job hits the exact same MarkCreating->NotFound->CreateShipment+pickup race.
	t.Run("AutoCancel48hJob_NeverCancelsPickedUpOrder", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &interceptStore{Store: h.store}
		h.svc.store = wrapped
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p01-job48h",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ordID := co.Orders[0].Id
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-r2-job-pay", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		})
		_, _ = h.store.MarkOrderShipmentCreating(context.Background(), ordID, now)

		cancelCalls := 0
		h.shipping.onCancelShipment = func(_ *shippingv1.CancelShipmentRequest) error {
			cancelCalls++
			if cancelCalls == 1 {
				return status.Error(codes.NotFound, "not created yet")
			}
			return status.Error(codes.FailedPrecondition, "picked up")
		}
		sellerDone := false
		wrapped.onTransitionOrderStatus = func(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
			if !sellerDone && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				sellerDone = true
				_, _ = h.store.TransitionOrderPreparedForShipment(ctx, store.ShipOrderParams{
					OrderID: ordID, CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-JOB-1",
					LabelURL: "https://labels.sandbox/job1", ActorID: "seller-1", ActorRole: "seller", Now: now,
				})
			}
			return h.store.TransitionOrderStatus(ctx, params)
		}

		now = now.Add(49 * time.Hour)
		if n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background()); err != nil || n != 0 {
			t.Fatalf("ScanAutoCancelUnconfirmedOrders = %d, %v; want 0, nil", n, err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status == orderv1.OrderStatus_ORDER_STATUS_CANCELLED || len(h.payment.refundCalls) != 0 {
			t.Fatalf("48h job MUST NOT cancel or refund picked-up order: status=%v refunds=%d", got.Order.Status, len(h.payment.refundCalls))
		}

		// Also verify 48h job skips (does NOT cancel) when shipment is still in flight (CONFIRMED + pending_shipment_cancel=true + NotFound).
		co2, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p01-job48h-inflight",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ord2ID := co2.Orders[0].Id
		// Advance past 48h, then seller starts creating shipment right before the 48h job runs (fresh flag <= 5m TTL).
		now = now.Add(49 * time.Hour)
		_, _ = h.store.MarkOrderShipmentCreating(context.Background(), ord2ID, now)
		h.shipping.onCancelShipment = nil
		h.shipping.cancelShipErr = status.Error(codes.NotFound, "still creating")
		if n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background()); err != nil || n != 0 {
			t.Fatalf("ScanAutoCancelUnconfirmedOrders while shipment in flight = %d, %v; want 0, nil", n, err)
		}
		got2, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord2ID})
		if got2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			t.Fatalf("in-flight order status = %v, want CONFIRMED", got2.Order.Status)
		}
	})
}

// Review 2 P1-1: cancelPaymentIgnoreTerminal ignores AlreadyExists and skips CancelPayment on 0-VND or already-refunded orders.
func TestReview2_P1_1_CancelPaymentAlreadyExistsAndSkipZeroOrRefunded(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	t.Run("AlreadyExistsFromRustPayment_ClearsPendingCancelCompensation", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p11-ae",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ordID := co.Orders[0].Id
		// Rust payment returns codes.AlreadyExists (PaymentError::Conflict) when payment is already expired/failed/superseded.
		h.payment.cancelErr = status.Error(codes.AlreadyExists, "payment is not pending")
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel"}); err != nil {
			t.Fatalf("CancelOrder: %v", err)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingCancelCompensation {
			t.Fatalf("pending_cancel_compensation MUST be cleared when CancelPayment returns AlreadyExists")
		}
	})

	t.Run("ZeroVNDOrderCancel_SkipsCancelPaymentAndClearsFlag", func(t *testing.T) {
		h := newTestHarness()
		h.promo.breakdown = &promotionv1.DiscountBreakdown{
			Shops: []*promotionv1.ShopDiscountBreakdown{
				{ShopId: "shop-1", SubtotalVnd: 1000000, PlatformVoucherDiscountVnd: 500000, FreeshipDiscountVnd: 30000, CoinsDiscountVnd: 500000, ShippingFeeVnd: 30000},
			},
		}
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r2-p11-zero",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-p",
			FreeshipVoucherId: "v-f",
			CoinsToUse:        500000,
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout 0 VND: %v", err)
		}
		ordID := co.Orders[0].Id
		h.payment.cancelErr = status.Error(codes.AlreadyExists, "0 VND payment already succeeded")
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel 0 vnd"}); err != nil {
			t.Fatalf("CancelOrder 0 VND: %v", err)
		}
		if len(h.payment.cancelCalls) != 0 {
			t.Fatalf("0-VND order cancel must NOT call CancelPayment, got %d calls", len(h.payment.cancelCalls))
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingCancelCompensation {
			t.Fatalf("pending_cancel_compensation must be cleared for 0-VND cancel")
		}
	})

	t.Run("AlreadyRefundedVietQROrderWatchdogRetry_SkipsCancelPayment", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p11-ref",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ord := co.Orders[0]
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-p11-ref-ok", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		})
		// Cancel while promo fails so pending_cancel_compensation stays true.
		h.promo.releaseErr = status.Error(codes.Unavailable, "promo blip")
		_, _ = h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord.Id, Reason: "cancel"})
		// Full refund settles -> orders.payment_status becomes REFUNDED before watchdog retries.
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-p11-ref-done", events.PaymentRefundedPayload{
			RefundID: "ref-p11", PaymentID: co.PaymentId, OrderID: ord.Id, AmountVND: ord.TotalPayableVnd, Reason: "cancel refund",
		})
		h.promo.releaseErr = nil
		h.payment.cancelCalls = nil
		h.payment.cancelErr = status.Error(codes.AlreadyExists, "already refunded")
		if n, err := h.svc.ScanPendingCancelCompensations(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanPendingCancelCompensations = %d, %v; want 1, nil", n, err)
		}
		if len(h.payment.cancelCalls) != 0 {
			t.Fatalf("already-refunded order retry must NOT call CancelPayment, got %d calls", len(h.payment.cancelCalls))
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ord.Id)
		if agg.Order.PendingCancelCompensation {
			t.Fatalf("pending_cancel_compensation must be cleared after retry")
		}
	})
}

// Review 2 P1-3: SHIPPING -> RETURNED sets durable pending_return_compensation inside same TX and watchdog retries restock/promo/COD-rejection/refund idempotently.
func TestReview2_P1_3_ShippingToReturnedDurableCompensationWatchdog(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("VietQROrder_OutageOver5s_RecoveredByScanPendingReturnCompensations", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r2-p13-vqr",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ord := co.Orders[0]
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-p13-pay", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		})
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord.Id, ShopId: "shop-1"})

		// Simulate Payment outage > 5s (NATS DLQs shipment.updated after HandleShipmentUpdated returns error).
		h.payment.refundErr = status.Error(codes.Unavailable, "payment down > 5s")
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p13-vqr-ret", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, TrackingCode: shipResp.TrackingCode, Status: "returned", Description: "undeliverable",
		}); err == nil {
			t.Fatalf("expected HandleShipmentUpdated to fail while payment down")
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ord.Id)
		if agg.Order.Status != "RETURNED" || !agg.Order.PendingReturnCompensation {
			t.Fatalf("expected status=RETURNED & pending_return_compensation=true, got status=%s flag=%v", agg.Order.Status, agg.Order.PendingReturnCompensation)
		}

		// Payment recovers; watchdog recovers refund/restock/promo and clears flag!
		h.payment.refundErr = nil
		h.payment.refundCalls = nil
		if n, err := h.svc.ScanPendingReturnCompensations(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanPendingReturnCompensations = %d, %v; want 1, nil", n, err)
		}
		if len(h.payment.refundCalls) != 1 || h.payment.refundCalls[0].GetAmountVnd() != ord.TotalPayableVnd {
			t.Fatalf("expected 1 CreateRefund for %d VND, got %+v", ord.TotalPayableVnd, h.payment.refundCalls)
		}
		agg, _ = h.store.GetOrderWithDetails(context.Background(), ord.Id)
		if agg.Order.PendingReturnCompensation {
			t.Fatalf("pending_return_compensation must be cleared after watchdog succeeds")
		}
		// Idempotent: second sweep handles 0.
		if n, err := h.svc.ScanPendingReturnCompensations(context.Background()); err != nil || n != 0 {
			t.Fatalf("second ScanPendingReturnCompensations = %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("CODOrder_UserServiceOutage_WatchdogRetriesRecordCODRejection", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p13-cod",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id
		shipResp, _ := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

		h.user.codRejectionErr = status.Error(codes.Unavailable, "user service down")
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p13-cod-ret", events.ShipmentUpdatedPayload{
			OrderID: ordID, TrackingCode: shipResp.TrackingCode, Status: "returned", Description: "buyer refused COD",
		}); err == nil {
			t.Fatalf("expected error while user service down")
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if !agg.Order.PendingReturnCompensation {
			t.Fatalf("expected pending_return_compensation=true")
		}

		h.user.codRejectionErr = nil
		if n, err := h.svc.ScanPendingReturnCompensations(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanPendingReturnCompensations = %d, %v; want 1, nil", n, err)
		}
		if h.user.codRejectedCount != 1 {
			t.Fatalf("codRejectedCount = %d, want 1", h.user.codRejectedCount)
		}
		agg, _ = h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingReturnCompensation {
			t.Fatalf("pending_return_compensation must be cleared after watchdog succeeds")
		}
	})
}

// Review 2 P1-2: previous_return_trackings only checked when curTracking==""; RETURN_AND_REFUND blocked on CREATED/PICKING;
// REJECT allowed after 3-extension auto-escalate with audit note.
func TestReview2_P1_2_PreviousTrackingAndAutoEscalateResolve(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("AdminReturnAndRefund_BlockedWhenOldShipmentCreatedOrPicking", func(t *testing.T) {
		for _, st := range []shippingv1.ShipmentStatus{
			shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			shippingv1.ShipmentStatus_SHIPMENT_STATUS_PICKING,
		} {
			t.Run(st.String(), func(t *testing.T) {
				h := newTestHarness()
				now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
				h.svc.nowFn = func() time.Time { return now }

				ord := createDeliveredOrderHelper(t, h, "r2-p12-act-"+st.String(), orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
				retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
				retID := retResp.ReturnRequestId
				acc, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
					ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
				})

				// Escalate via 3 extensions when CancelShipment fails while shipment is still CREATED/PICKING.
				h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "carrier refused cancel")
				h.shipping.getShipResp = &shippingv1.GetShipmentResponse{
					Shipment: &shippingv1.Shipment{
						OrderId: ord.Id, TrackingCode: acc.ReturnRequest.ReturnTrackingCode,
						Status: st, Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
					},
				}
				now = now.Add(5*24*time.Hour + time.Minute)
				for i := 0; i < 4; i++ {
					_, _ = h.svc.ScanReturnDeadlines(context.Background())
					now = now.Add(24*time.Hour + time.Minute)
				}

				_, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
					ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
				})
				if status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("AdminResolveReturn(RETURN_AND_REFUND) while old shipment %s = %v, want FailedPrecondition", st, err)
				}
			})
		}
	})

	t.Run("AdminReject_AllowedAfter3ExtensionAutoEscalate_WithAuditNote", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "r2-p12-rej3", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})

		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "cannot cancel")
		h.shipping.getShipErr = status.Error(codes.Unavailable, "shipping read blip")
		now = now.Add(5*24*time.Hour + time.Minute)
		for i := 0; i < 4; i++ {
			_, _ = h.svc.ScanReturnDeadlines(context.Background())
			now = now.Add(24*time.Hour + time.Minute)
		}

		res, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT, Note: "Admin closes after 3 extensions",
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn(REJECT) after 3-extension auto-escalate failed: %v", err)
		}
		if res.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED {
			t.Fatalf("status = %v, want CLOSED", res.ReturnRequest.Status)
		}
		lastEv := res.ReturnRequest.Events[len(res.ReturnRequest.Events)-1]
		if !strings.Contains(lastEv.Note, "gia hạn") {
			t.Fatalf("expected audit note mentioning 3 extensions, got %q", lastEv.Note)
		}
	})

	t.Run("PreviousTrackingOnlyCheckedWhenCurrentTrackingEmpty", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "r2-p12-prev", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		// Always return TRK-SAME-1 (simulating shipping returning alreadyExisted TRK-SAME-1).
		h.shipping.onCreateReturnShipment = func(req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
			return &shippingv1.CreateReturnShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId: "rshp-same", OrderId: req.GetOrderId(), TrackingCode: "TRK-SAME-1",
					LabelUrl: "https://labels.sandbox/same", FeeVnd: 25000, Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
				},
			}, nil
		}
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p12-can", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "TRK-SAME-1", Status: "cancelled",
		})
		// Admin re-opens RETURN_AND_REFUND -> TRK-SAME-1 goes into previous_return_trackings_json,
		// and shipping returns TRK-SAME-1 again as current return_tracking_code.
		_, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}

		// Delivered webhook for current tracking TRK-SAME-1 MUST NOT be swallowed!
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p12-deliv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "TRK-SAME-1", Status: "delivered",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered): %v", err)
		}
		got, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_RECEIVED {
			t.Fatalf("status = %v, want RECEIVED (current tracking must not be swallowed by previous_return_trackings)", got.ReturnRequest.Status)
		}
	})
}

// Review 2 P2-2, P2-5, P3-1(a)-(d), and ponytail cleanup tests.
func TestReview2_P2_And_P3_Fixes(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	seller1Ctx := ctxWithUser("seller-1", "seller", "shop-1")
	seller2Ctx := ctxWithUser("seller-2", "seller", "shop-2")

	t.Run("P2_2_RefundingCrashAfterCreateRefundPreservesRefundAmountVND", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "r2-p22-amt", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		// First attempt fails CreateRefund so return stays REFUNDING with refund_done=false and refund_amount_vnd = TotalPayableVnd.
		h.payment.refundErr = status.Error(codes.Unavailable, "crash before UpdateReturnSideEffectProgress")
		_, _ = h.svc.RespondReturn(seller1Ctx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		})

		// Simulate payment.refunded(full amount) arriving before watchdog retry (so remaining = 0).
		_ = h.svc.HandlePaymentRefunded(context.Background(), "evt-r2-p22-full", events.PaymentRefundedPayload{
			RefundID: "ref-full-1", PaymentID: ord.PaymentId, OrderID: ord.Id, AmountVND: ord.TotalPayableVnd, Reason: "return refund",
		})

		h.payment.refundErr = nil
		if n, err := h.svc.ScanRefundingReturns(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanRefundingReturns = %d, %v; want 1, nil", n, err)
		}
		got, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("status = %v, want REFUNDED", got.ReturnRequest.Status)
		}
		if got.ReturnRequest.RefundAmountVnd != ord.TotalPayableVnd {
			t.Fatalf("return.refund_amount_vnd = %d, want %d (must NOT overwrite with 0!)", got.ReturnRequest.RefundAmountVnd, ord.TotalPayableVnd)
		}
	})

	t.Run("P2_5_ConfirmStockFailedPreconditionAndNotFoundHandledCleanly", func(t *testing.T) {
		// Case 1: VietQR payment.succeeded after concurrent cancel already released reservation (ConfirmStock -> FailedPrecondition, ReleaseStock -> Released:true).
		h := newTestHarness()
		prodSim := newStatefulProductClient()
		h.svc.clients.Product = prodSim

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p25-fp",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		// Release reservation directly (simulating concurrent cancel releasing stock before HandlePaymentSucceeded calls ConfirmStock).
		_, _ = prodSim.ReleaseStock(context.Background(), &productv1.ReleaseStockRequest{OrderId: co.CheckoutGroupId})

		if err := h.svc.HandlePaymentSucceeded(context.Background(), "evt-r2-p25-ok", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		}); err != nil {
			t.Fatalf("HandlePaymentSucceeded when stock already released MUST return nil (no DLQ), got: %v", err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), co.CheckoutGroupId)
		if grp.PendingStockConfirm {
			t.Fatalf("pending_stock_confirm must be cleared when stock already released")
		}

		// Case 2: Legacy/seeded order where Product.ConfirmStock returns NotFound -> restock + refund still proceed!
		h2 := newTestHarness()
		ord2 := createDeliveredOrderHelper(t, h2, "r2-p25-nf", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		ret2, _ := h2.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord2.Id, Reason: "defect"})
		acc2, _ := h2.svc.RespondReturn(seller1Ctx, &orderv1.RespondReturnRequest{
			ReturnRequestId: ret2.ReturnRequestId, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h2.svc.HandleShipmentUpdated(context.Background(), "evt-r2-p25-retdeliv", events.ShipmentUpdatedPayload{
			OrderID: ord2.Id, Direction: "RETURN", ReturnRequestID: ret2.ReturnRequestId,
			TrackingCode: acc2.ReturnRequest.ReturnTrackingCode, Status: "delivered",
		})
		h2.product.confirmErr = status.Error(codes.NotFound, "legacy order has no stock_reservations row")
		conf, err := h2.svc.ConfirmReturnReceived(seller1Ctx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: ret2.ReturnRequestId})
		if err != nil {
			t.Fatalf("ConfirmReturnReceived: %v", err)
		}
		if conf.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("legacy order with ConfirmStock NotFound status = %v, want REFUNDED", conf.ReturnRequest.Status)
		}
	})

	t.Run("P3_1a_MultiShopCODOneReturnedOneCancelled_MarksGroupCancelled", func(t *testing.T) {
		h := newTestHarness()
		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p31a-cod",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items: []*orderv1.CheckoutItemInput{
				{SkuId: "sku-1", Quantity: 1},
				{SkuId: "sku-2", Quantity: 1},
			},
			PaymentMethod: orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		var o1ID, o2ID string
		for _, o := range co.Orders {
			if o.ShopId == "shop-1" {
				o1ID = o.Id
			} else {
				o2ID = o.Id
			}
		}
		// Shop 1 ships and fails delivery -> RETURNED (payment_status=CANCELLED).
		ship1, _ := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{OrderId: o1ID, ShopId: "shop-1"})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-r2-p31a-ret", events.ShipmentUpdatedPayload{
			OrderID: o1ID, TrackingCode: ship1.TrackingCode, Status: "returned", Description: "refused",
		})
		// Shop 2 cancels -> BOTH sub-orders are terminal unpaid -> group payment_status MUST be CANCELLED!
		if _, err := h.svc.CancelOrder(seller2Ctx, &orderv1.CancelOrderRequest{OrderId: o2ID, Reason: "out of stock"}); err != nil {
			t.Fatalf("CancelOrder(o2): %v", err)
		}
		grp, _ := h.store.GetCheckoutGroupByID(context.Background(), co.CheckoutGroupId)
		if grp.PaymentStatus != "CANCELLED" {
			t.Fatalf("checkout_group.payment_status = %q, want CANCELLED", grp.PaymentStatus)
		}
	})

	t.Run("P3_1b_PaymentFailedRedeliveryClearsPendingCancelCompensation", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey:    "r2-p31b-redeliv",
			BuyerId:           "buyer-1",
			AddressId:         "addr-buyer-1",
			Items:             []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PlatformVoucherId: "v-plat",
			PaymentMethod:     orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ordID := co.Orders[0].Id
		h.promo.releaseErr = status.Error(codes.Unavailable, "promo down on attempt 1")
		if err := h.svc.HandlePaymentFailed(context.Background(), "evt-r2-p31b", events.PaymentFailedPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, Reason: "failed",
		}); err == nil {
			t.Fatalf("expected error on attempt 1")
		}
		h.promo.releaseErr = nil
		if err := h.svc.HandlePaymentFailed(context.Background(), "evt-r2-p31b", events.PaymentFailedPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId, Reason: "failed",
		}); err != nil {
			t.Fatalf("redelivery HandlePaymentFailed: %v", err)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingCancelCompensation {
			t.Fatalf("pending_cancel_compensation MUST be cleared on payment.failed redelivery")
		}
	})

	t.Run("P3_1c_FallbackCarrierUpdatedWhenDeliveringWebhookBeatsSellerShipOrder", func(t *testing.T) {
		h := newTestHarness()
		h.shipping.quotes = []*shippingv1.CarrierQuote{
			{CarrierCode: "fallback", CarrierName: "Fallback", ServiceCode: "standard", FeeVnd: 30000, IsFallback: true, CodSupported: true},
		}
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-p31c-fb",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id
		h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-r2-p31c-go", events.ShipmentUpdatedPayload{
				OrderID: ordID, Status: "delivering", TrackingCode: "TRK-FB-FAST",
			})
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId: "shp-fb", OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "express",
					TrackingCode: "TRK-FB-FAST", LabelUrl: "https://labels.sandbox/fb-fast",
				},
			}, nil
		}
		resp, err := h.svc.SellerShipOrder(seller1Ctx, &orderv1.SellerShipOrderRequest{
			OrderId: ordID, ShopId: "shop-1", CarrierCode: "ghn", ServiceCode: "express",
		})
		if err != nil {
			t.Fatalf("SellerShipOrder: %v", err)
		}
		if resp.Order.CarrierCode != "ghn" || resp.Order.ServiceCode != "express" {
			t.Fatalf("carrier/service = %q/%q, want ghn/express (must replace fallback!)", resp.Order.CarrierCode, resp.Order.ServiceCode)
		}
	})

	t.Run("P3_1d_AlreadyExistedReturnShipmentDeliveringSyncsImmediately", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "r2-p31d-sync", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		h.shipping.onCreateReturnShipment = func(req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
			return &shippingv1.CreateReturnShipmentResponse{
				AlreadyExisted: true,
				Shipment: &shippingv1.Shipment{
					ShipmentId: "rshp-ex", OrderId: req.GetOrderId(), TrackingCode: "TRK-RET-ALREADY",
					LabelUrl: "https://labels.sandbox/already", FeeVnd: 25000,
					Status:    shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNING,
					Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
				},
			}, nil
		}
		h.shipping.getShipResp = &shippingv1.GetShipmentResponse{
			Shipment: &shippingv1.Shipment{
				ShipmentId: "rshp-ex", OrderId: ord.Id, TrackingCode: "TRK-RET-ALREADY",
				Status:    shippingv1.ShipmentStatus_SHIPMENT_STATUS_RETURNING,
				Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
			},
		}
		resp, err := h.svc.RespondReturn(seller1Ctx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID, Decision: orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		if resp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT {
			t.Fatalf("status = %v, want IN_TRANSIT (must sync returning/delivering immediately when alreadyExisted=true)", resp.ReturnRequest.Status)
		}
	})

	t.Run("Ponytail_CanManageShopOrderRejectsEmptyRole", func(t *testing.T) {
		h := newTestHarness()
		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "r2-pt-role",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		emptyRoleCtx := ctxWithUser("seller-1", "", "shop-1")
		if _, err := h.svc.SellerConfirmOrder(emptyRoleCtx, &orderv1.SellerConfirmOrderRequest{
			OrderId: co.Orders[0].Id, ShopId: "shop-1",
		}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("SellerConfirmOrder with empty role = %v, want PermissionDenied", err)
		}
	})
}
