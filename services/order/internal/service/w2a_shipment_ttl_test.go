package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestShipmentCancelTTL_FakeClock covers pending_shipment_cancel TTL expiry, 409 within TTL,
// recovery after Unavailable / crash before transition, 48h job cancel after TTL, and P0-1 preservation.
func TestShipmentCancelTTL_FakeClock(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("CarrierUnavailable_BlockedWithin5m_CancelAllowedAfter5m", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		co, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-unavail-1",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ordID := co.Orders[0].Id
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-ttl-pay-1", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		})

		// SellerShipOrder fails with Unavailable (P3-2 keeps pending_shipment_cancel=true because shipment might have been created).
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.Unavailable, "carrier timeout")
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"}); status.Code(err) != codes.Unavailable {
			t.Fatalf("SellerShipOrder = %v, want Unavailable", err)
		}

		// Shipping has no shipment -> CancelShipment returns NotFound.
		h.shipping.cancelShipErr = status.Error(codes.NotFound, "no shipment created")

		// At t+4m59s (within 5m TTL): CancelOrder MUST still return 409 "vận đơn đang được tạo, thử lại"!
		now = now.Add(4*time.Minute + 59*time.Second)
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "try within TTL"}); status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "vận đơn đang được tạo") {
			t.Fatalf("CancelOrder at 4m59s = %v, want 409 'vận đơn đang được tạo'", err)
		}

		// At t+5m01s (past 5m TTL) with shipping confirming NotFound: CancelOrder MUST succeed & clear flag!
		now = now.Add(2 * time.Second)
		canResp, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel after TTL"})
		if err != nil {
			t.Fatalf("CancelOrder after 5m TTL failed: %v", err)
		}
		if canResp.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("order status = %v, want CANCELLED", canResp.Order.Status)
		}
		agg, _ := h.store.GetOrderWithDetails(context.Background(), ordID)
		if agg.Order.PendingShipmentCancel {
			t.Fatalf("pending_shipment_cancel must be cleared after expired-TTL NotFound cancel")
		}
	})

	t.Run("CrashBeforeTransition_Past5mTTL_IfShipmentPickedUp_StillBlocksCancel", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 11, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-crash-picked",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ordID := co.Orders[0].Id
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-ttl-cp-pay", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		})

		// Simulate SellerShipOrder crashing right after CreateShipment (shipment WAS created, flag stays true).
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.DeadlineExceeded, "order timed out waiting for response")
		}
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

		// Even past 5m TTL, CancelOrder MUST still call CancelShipment first; carrier picked up -> FailedPrecondition -> NEVER cancels!
		now = now.Add(10 * time.Minute)
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "carrier already picked up")
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel after crash"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CancelOrder when shipment was actually picked up = %v, want FailedPrecondition", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status == orderv1.OrderStatus_ORDER_STATUS_CANCELLED || len(h.payment.refundCalls) != 0 {
			t.Fatalf("order MUST NOT be cancelled/refunded when CancelShipment returns FailedPrecondition")
		}
	})

	t.Run("AutoCancel48hJob_BlockedWithin5mTTL_CancelsAfter5mTTL", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-job48h",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id

		// Advance past 48h, seller attempts ship right before job runs -> Unavailable (no shipment created).
		now = now.Add(49 * time.Hour)
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.Unavailable, "shipping down")
		}
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
		h.shipping.cancelShipErr = status.Error(codes.NotFound, "no shipment")

		// 2 minutes later (within 5m TTL): 48h job skips (0 cancelled).
		now = now.Add(2 * time.Minute)
		if n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background()); err != nil || n != 0 {
			t.Fatalf("48h job within 5m TTL = %d, %v; want 0, nil", n, err)
		}

		// 4 more minutes later (6m > 5m TTL): 48h job cancels cleanly!
		now = now.Add(4 * time.Minute)
		if n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background()); err != nil || n != 1 {
			t.Fatalf("48h job after 5m TTL = %d, %v; want 1, nil", n, err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			t.Fatalf("status after 48h job past TTL = %v, want CANCELLED", got.Order.Status)
		}
	})

	t.Run("SellerRetryRefreshesTTL_BlocksCancelWithinNew5mWindow", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 13, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-retry-refresh",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.Unavailable, "shipping down")
		}
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

		// Advance 6 minutes (old flag expired), then Seller retries -> refreshes pending_shipment_cancel_at to t+6m!
		now = now.Add(6 * time.Minute)
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

		// At t+8m (only 2m after retry): CancelOrder MUST still be blocked with 409!
		now = now.Add(2 * time.Minute)
		h.shipping.cancelShipErr = status.Error(codes.NotFound, "still creating")
		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "cancel after retry"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CancelOrder 2m after seller retry = %v, want FailedPrecondition (TTL refreshed)", err)
		}
	})

	t.Run("P0_1_StillClosed_WithinTTLAndWhenTrackingAttachedAfterTTL", func(t *testing.T) {
		h := newTestHarness()
		wrapped := &interceptStore{Store: h.store}
		h.svc.store = wrapped
		now := time.Date(2026, 11, 23, 14, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-p01-closed",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR,
		})
		ordID := co.Orders[0].Id
		_ = h.svc.HandlePaymentSucceeded(context.Background(), "evt-ttl-p01-pay", events.PaymentSucceededPayload{
			PaymentID: co.PaymentId, OrderGroupID: co.CheckoutGroupId,
		})
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.Unavailable, "blip")
		}
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})

		// Even past 5m TTL: if Seller commits TRK-LATE-1 between Cancel(NotFound) and TransitionOrderStatus,
		// checkCancelShipmentGuard sees ord.TrackingCode="TRK-LATE-1" != "" and STILL blocks CANCELLED!
		now = now.Add(6 * time.Minute)
		cancelAttempt := 0
		h.shipping.onCancelShipment = func(_ *shippingv1.CancelShipmentRequest) error {
			cancelAttempt++
			if cancelAttempt == 1 {
				return status.Error(codes.NotFound, "not visible yet")
			}
			return status.Error(codes.FailedPrecondition, "already picked up")
		}
		attached := false
		wrapped.onTransitionOrderStatus = func(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
			if !attached && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				attached = true
				_, _ = h.store.TransitionOrderPreparedForShipment(ctx, store.ShipOrderParams{
					OrderID: ordID, CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-LATE-1",
					LabelURL: "https://labels.sandbox/late1", ActorID: "seller-1", ActorRole: "seller", Now: now,
				})
			}
			return h.store.TransitionOrderStatus(ctx, params)
		}

		if _, err := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "late race"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("CancelOrder during late tracking attach + pickup = %v, want FailedPrecondition", err)
		}
		got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
		if got.Order.Status == orderv1.OrderStatus_ORDER_STATUS_CANCELLED || len(h.payment.refundCalls) != 0 {
			t.Fatalf("P0-1 reopened! status=%v refunds=%d", got.Order.Status, len(h.payment.refundCalls))
		}
	})

	t.Run("StaleTokenGuard_ExpiredOrCancelledOrderNeverAttachesOldTracking", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		co, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-token-guard",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ordID := co.Orders[0].Id

		// Attempt 1 acquires token T1 at t=0.
		tok1, err := h.store.MarkOrderShipmentCreating(context.Background(), ordID, now)
		if err != nil || tok1 == "" {
			t.Fatalf("MarkOrderShipmentCreating #1: tok=%q err=%v", tok1, err)
		}
		// Attempt 2 retries at t+6m and gets token T2.
		now = now.Add(6 * time.Minute)
		tok2, err := h.store.MarkOrderShipmentCreating(context.Background(), ordID, now)
		if err != nil || tok2 == "" || tok2 == tok1 {
			t.Fatalf("MarkOrderShipmentCreating #2: tok=%q (old=%q) err=%v", tok2, tok1, err)
		}
		// Stale attempt 1 tries to commit with T1 -> rejected with ErrStaleShipmentAttempt!
		if _, err := h.store.TransitionOrderPreparedForShipment(context.Background(), store.ShipOrderParams{
			OrderID: ordID, CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-STALE-1",
			LabelURL: "https://labels.sandbox/stale1", ExpectedCreatingToken: tok1, ActorID: "seller-1", ActorRole: "seller", Now: now,
		}); !errors.Is(err, store.ErrStaleShipmentAttempt) {
			t.Fatalf("stale attempt transition = %v, want ErrStaleShipmentAttempt", err)
		}
		// Active attempt 2 with T2 succeeds cleanly!
		if agg, err := h.store.TransitionOrderPreparedForShipment(context.Background(), store.ShipOrderParams{
			OrderID: ordID, CarrierCode: "ghn", ServiceCode: "standard", TrackingCode: "TRK-FRESH-2",
			LabelURL: "https://labels.sandbox/fresh2", ExpectedCreatingToken: tok2, ActorID: "seller-1", ActorRole: "seller", Now: now,
		}); err != nil || agg.Order.TrackingCode != "TRK-FRESH-2" {
			t.Fatalf("fresh attempt transition = %+v err=%v", agg, err)
		}

		// Stalled SellerShipOrder past 5m TTL when order was CANCELLED -> triggers orphan CancelShipment, never attaches tracking!
		co2, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-stale-cancelled",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ord2ID := co2.Orders[0].Id
		h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			// Simulate CreateShipment hanging > 5m while buyer cancels after TTL expires!
			now = now.Add(6 * time.Minute)
			h.shipping.cancelShipErr = status.Error(codes.NotFound, "not visible yet")
			if _, cErr := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ord2ID, Reason: "cancel after 6m"}); cErr != nil {
				t.Errorf("CancelOrder after 6m: %v", cErr)
			}
			// Now shipment finishes creating -> orphan cancel will find & cancel it!
			h.shipping.cancelShipErr = nil
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId: "shp-orphan-ttl", OrderId: req.GetOrderId(), CarrierCode: "ghn", ServiceCode: "standard",
					TrackingCode: "TRK-ORPHAN-TTL", LabelUrl: "https://labels.sandbox/orphan",
				},
			}, nil
		}
		if _, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord2ID, ShopId: "shop-1"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("stalled SellerShipOrder on cancelled order = %v, want FailedPrecondition", err)
		}
		got2, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord2ID})
		if got2.Order.Status != orderv1.OrderStatus_ORDER_STATUS_CANCELLED || got2.Order.TrackingCode != "" {
			t.Fatalf("cancelled order must keep status=CANCELLED & empty tracking, got status=%v tracking=%q", got2.Order.Status, got2.Order.TrackingCode)
		}
		if len(h.shipping.cancelShipCalls) < 2 {
			t.Fatalf("expected orphan CancelShipment compensation after stalled CreateShipment, calls=%d", len(h.shipping.cancelShipCalls))
		}
	})

	t.Run("Watchdog_ScanStalePendingShipmentCancels_ClearsOnNotFoundAndSyncsLiveShipment", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 16, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		// Order 1: Unavailable -> no shipment at shipping (GetShipment -> NotFound) -> watchdog clears flag after 5m.
		co1, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-wd-notfound",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ord1ID := co1.Orders[0].Id
		h.shipping.onCreateShipment = func(_ *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			return nil, status.Error(codes.Unavailable, "carrier down")
		}
		_, _ = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord1ID, ShopId: "shop-1"})

		// Within 5m (t+3m): watchdog must NOT touch it.
		now = now.Add(3 * time.Minute)
		h.shipping.getShipErr = status.Error(codes.NotFound, "no shipment")
		if n, err := h.svc.ScanStalePendingShipmentCancels(context.Background()); err != nil || n != 0 {
			t.Fatalf("watchdog within 5m = %d, %v; want 0, nil", n, err)
		}

		// Past 5m (t+6m): GetShipment(FORWARD) returns NotFound -> watchdog clears flag cleanly!
		now = now.Add(3 * time.Minute)
		if n, err := h.svc.ScanStalePendingShipmentCancels(context.Background()); err != nil || n != 1 {
			t.Fatalf("watchdog past 5m (NotFound) = %d, %v; want 1, nil", n, err)
		}
		agg1, _ := h.store.GetOrderWithDetails(context.Background(), ord1ID)
		if agg1.Order.PendingShipmentCancel || agg1.Order.PendingShipmentCancelAt != nil || agg1.Order.PendingShipmentCancelToken != "" {
			t.Fatalf("flag/timestamp/token must all be cleared, got flag=%v at=%v tok=%q",
				agg1.Order.PendingShipmentCancel, agg1.Order.PendingShipmentCancelAt, agg1.Order.PendingShipmentCancelToken)
		}

		// Order 2: Crashed after CreateShipment (live DELIVERING shipment exists in shipping) -> watchdog syncs tracking & status!
		co2, _ := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "ttl-wd-live",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		ord2ID := co2.Orders[0].Id
		_, _ = h.store.MarkOrderShipmentCreating(context.Background(), ord2ID, now)

		now = now.Add(6 * time.Minute)
		h.shipping.getShipErr = nil
		h.shipping.getShipResp = &shippingv1.GetShipmentResponse{
			Shipment: &shippingv1.Shipment{
				ShipmentId:   "shp-live-sync",
				OrderId:      ord2ID,
				CarrierCode:  "ghn",
				ServiceCode:  "standard",
				TrackingCode: "TRK-SYNC-LIVE",
				LabelUrl:     "https://labels.sandbox/sync-live",
				Status:       shippingv1.ShipmentStatus_SHIPMENT_STATUS_DELIVERING,
				Direction:    shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD,
			},
		}
		if n, err := h.svc.ScanStalePendingShipmentCancels(context.Background()); err != nil || n != 1 {
			t.Fatalf("watchdog past 5m (live shipment) = %d, %v; want 1, nil", n, err)
		}
		agg2, _ := h.store.GetOrderWithDetails(context.Background(), ord2ID)
		if agg2.Order.PendingShipmentCancel || agg2.Order.TrackingCode != "TRK-SYNC-LIVE" || agg2.Order.Status != "SHIPPING" {
			t.Fatalf("expected synced SHIPPING order with TRK-SYNC-LIVE and flag cleared, got status=%s tracking=%q flag=%v",
				agg2.Order.Status, agg2.Order.TrackingCode, agg2.Order.PendingShipmentCancel)
		}
	})
}
