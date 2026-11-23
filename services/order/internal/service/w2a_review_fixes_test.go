package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type interceptStore struct {
	store.Store
	onTransitionOrderStatus func(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error)
}

func (s *interceptStore) TransitionOrderStatus(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
	if s.onTransitionOrderStatus != nil {
		return s.onTransitionOrderStatus(ctx, params)
	}
	return s.Store.TransitionOrderStatus(ctx, params)
}

// Review Point 1: Orphan shipment created while order is cancelled concurrently must be cancelled via service token.
func TestW2A_Review1_OrphanShipmentCancelledOnConcurrentCancel(t *testing.T) {
	h := newTestHarness()
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "rev1-orphan-1",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ordID := resp.Orders[0].Id

	// Simulate buyer cancelling the order while CreateShipment is in flight.
	h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		if _, cErr := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
			OrderId: ordID,
			Reason:  "buyer cancelled mid-ship",
		}); cErr != nil {
			t.Errorf("concurrent CancelOrder failed: %v", cErr)
		}
		return &shippingv1.CreateShipmentResponse{
			Shipment: &shippingv1.Shipment{
				ShipmentId:   "shp-orphan-1",
				OrderId:      req.GetOrderId(),
				ShopId:       req.GetShopId(),
				CarrierCode:  req.GetCarrierCode(),
				ServiceCode:  req.GetServiceCode(),
				TrackingCode: "TRK-ORPHAN-1",
				LabelUrl:     "https://labels.sandbox/orphan-1",
				Status:       shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			},
		}, nil
	}

	_, err = h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SellerShipOrder after concurrent cancel = %v, want FailedPrecondition", err)
	}

	// SellerShipOrder MUST compensate by calling CancelShipment(FORWARD) with service token.
	foundForwardCancel := false
	for i, call := range h.shipping.cancelShipCalls {
		if call.GetOrderId() == ordID && call.GetDirection() == shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD {
			authSnap := h.shipping.cancelShipAuth[i]
			if authSnap.service != clients.InternalServiceName || authSnap.rawToken != "" {
				t.Fatalf("orphan CancelShipment must use service token, got %+v", authSnap)
			}
			foundForwardCancel = true
		}
	}
	if !foundForwardCancel {
		t.Fatalf("expected CancelShipment(FORWARD) for orphan shipment on %s, got %+v", ordID, h.shipping.cancelShipCalls)
	}

	// Subtest: if orphan CancelShipment fails transiently, watchdog ScanPendingOrphanShipmentCancels retries and clears flag.
	t.Run("TransientCancelFailureRetriedByWatchdog", func(t *testing.T) {
		h2 := newTestHarness()
		r2, err := h2.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: "rev1-orphan-wd",
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout: %v", err)
		}
		ord2ID := r2.Orders[0].Id
		h2.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
			// Cancel order while CreateShipment is running (with CancelShipment succeeding or NotFound before tracking exists).
			if _, cErr := h2.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{
				OrderId: ord2ID, Reason: "buyer cancel mid-ship",
			}); cErr != nil {
				t.Errorf("concurrent CancelOrder: %v", cErr)
			}
			// Now simulate shipping outage when SellerShipOrder tries to cancel the newly-created orphan shipment!
			h2.shipping.cancelShipErr = status.Error(codes.Unavailable, "shipping down")
			return &shippingv1.CreateShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId:   "shp-orphan-2",
					OrderId:      req.GetOrderId(),
					ShopId:       req.GetShopId(),
					CarrierCode:  req.GetCarrierCode(),
					ServiceCode:  req.GetServiceCode(),
					TrackingCode: "TRK-ORPHAN-2",
					Status:       shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
				},
			}, nil
		}

		if _, err := h2.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ord2ID, ShopId: "shop-1"}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("SellerShipOrder = %v, want FailedPrecondition", err)
		}
		// While shipping is still down, watchdog keeps pending flag (handled=0).
		if n, err := h2.svc.ScanPendingOrphanShipmentCancels(context.Background()); err != nil || n != 0 {
			t.Fatalf("watchdog while shipping down = %d, %v; want 0, nil", n, err)
		}
		// Recover shipping -> watchdog cancels orphan shipment and clears flag!
		h2.shipping.cancelShipErr = nil
		beforeCalls := len(h2.shipping.cancelShipCalls)
		if n, err := h2.svc.ScanPendingOrphanShipmentCancels(context.Background()); err != nil || n != 1 {
			t.Fatalf("watchdog after recovery = %d, %v; want 1, nil", n, err)
		}
		if len(h2.shipping.cancelShipCalls) != beforeCalls+1 {
			t.Fatalf("expected watchdog to call CancelShipment once, calls before=%d after=%d", beforeCalls, len(h2.shipping.cancelShipCalls))
		}
		// Second sweep is a no-op (flag cleared).
		if n, err := h2.svc.ScanPendingOrphanShipmentCancels(context.Background()); err != nil || n != 0 {
			t.Fatalf("second watchdog sweep = %d, %v; want 0, nil", n, err)
		}
	})
}

// Review Point 2: Partial cancel (shipment cancelled, DB transition fails transiently) -> SellerShipOrder recreates shipment and updates tracking.
func TestW2A_Review2_PartialCancel_SellerShipOrderCallsCreateShipmentAndUpdatesTracking(t *testing.T) {
	h := newTestHarness()
	wrapped := &interceptStore{Store: h.store}
	h.svc.store = wrapped

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "rev2-partial-cancel",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ordID := resp.Orders[0].Id

	callNum := 0
	h.shipping.onCreateShipment = func(req *shippingv1.CreateShipmentRequest) (*shippingv1.CreateShipmentResponse, error) {
		callNum++
		return &shippingv1.CreateShipmentResponse{
			Shipment: &shippingv1.Shipment{
				ShipmentId:   fmt.Sprintf("shp-%d", callNum),
				OrderId:      req.GetOrderId(),
				ShopId:       req.GetShopId(),
				CarrierCode:  req.GetCarrierCode(),
				ServiceCode:  req.GetServiceCode(),
				TrackingCode: fmt.Sprintf("TRK-NEW-%d", callNum),
				LabelUrl:     fmt.Sprintf("https://labels.sandbox/%d", callNum),
				Status:       shippingv1.ShipmentStatus_SHIPMENT_STATUS_CREATED,
			},
		}, nil
	}

	ship1, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder #1: %v", err)
	}
	if ship1.TrackingCode != "TRK-NEW-1" {
		t.Fatalf("ship1 tracking = %q, want TRK-NEW-1", ship1.TrackingCode)
	}

	// CancelOrder cancels shipment in shipping, then fails transiently on DB transition.
	wrapped.onTransitionOrderStatus = func(_ context.Context, _ store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
		return nil, errors.New("simulated transient db error")
	}
	if _, cErr := h.svc.CancelOrder(buyerCtx, &orderv1.CancelOrderRequest{OrderId: ordID, Reason: "try cancel"}); cErr == nil {
		t.Fatalf("expected CancelOrder to fail on simulated DB error")
	}
	wrapped.onTransitionOrderStatus = nil

	// Order is still PREPARING with cancelled tracking TRK-NEW-1.
	// SellerShipOrder MUST call CreateShipment again (shipping creates replacement TRK-NEW-2) and update tracking!
	ship2, err := h.svc.SellerShipOrder(sellerCtx, &orderv1.SellerShipOrderRequest{OrderId: ordID, ShopId: "shop-1"})
	if err != nil {
		t.Fatalf("SellerShipOrder #2: %v", err)
	}
	if len(h.shipping.createShipCalls) != 2 {
		t.Fatalf("expected 2 CreateShipment calls, got %d", len(h.shipping.createShipCalls))
	}
	if ship2.TrackingCode != "TRK-NEW-2" || ship2.Order.TrackingCode != "TRK-NEW-2" || ship2.LabelUrl != "https://labels.sandbox/2" {
		t.Fatalf("expected updated replacement tracking TRK-NEW-2, got resp=%+v order=%+v", ship2, ship2.Order)
	}
}

// Review Point 3: Cancel RETURN shipment BEFORE closing on AWAITING_SHIPMENT deadline and AdminResolveReturn(REJECT).
func TestW2A_Review3_CancelReturnShipmentBeforeClosing(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("AwaitingShipmentExpired_FailedPrecondition_ExtendsDeadlineAndDoesNotClose", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "rev3-fp-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "broken"})
		retID := retResp.ReturnRequestId
		_, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}

		// Carrier already picked up (event hasn't arrived yet) -> CancelShipment returns FailedPrecondition.
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already picked up")
		now = now.Add(5*24*time.Hour + time.Minute)
		if _, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil {
			t.Fatalf("ScanReturnDeadlines: %v", err)
		}

		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			t.Fatalf("return status = %v, want AWAITING_SHIPMENT (must NOT close when carrier already picked up)", gotRet.ReturnRequest.Status)
		}
		if gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED {
			t.Fatalf("order status = %v, want RETURN_REQUESTED", gotOrd.Order.Status)
		}
		if gotRet.ReturnRequest.DeadlineAt == nil || !gotRet.ReturnRequest.DeadlineAt.AsTime().After(now) {
			t.Fatalf("expected extended deadline > %v, got %v", now, gotRet.ReturnRequest.DeadlineAt)
		}
	})

	t.Run("AwaitingShipmentExpired_TransientError_DoesNotCloseAndRetriesLater", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "rev3-tr-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "broken"})
		retID := retResp.ReturnRequestId
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})

		h.shipping.cancelShipErr = status.Error(codes.Unavailable, "shipping down")
		now = now.Add(5*24*time.Hour + time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 0 {
			t.Fatalf("ScanReturnDeadlines during shipping outage = %d, %v; want 0, nil", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			t.Fatalf("return status = %v, want AWAITING_SHIPMENT while shipping unavailable", gotRet.ReturnRequest.Status)
		}

		// Once shipping recovers, next sweep cancels return shipment and closes return.
		h.shipping.cancelShipErr = nil
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines after recovery = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ = h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_CLOSED {
			t.Fatalf("return status after recovery = %v, want CLOSED", gotRet.ReturnRequest.Status)
		}
	})

	t.Run("AdminResolveReturnReject_LiveReturnShipmentFailedPrecondition_BlocksClose", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "rev3-adm-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "broken"})
		retID := retResp.ReturnRequestId
		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		// Transition to ESCALATED directly (neither 14d transit timeout nor 3-extension cancel-failure)
		// while a live RETURN shipment exists -> Admin REJECT must still call CancelShipment and block on FailedPrecondition!
		if _, tErr := h.store.TransitionReturnRequest(context.Background(), store.ReturnTransitionParams{
			ReturnRequestID: retID,
			ExpectedStatus:  orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT,
			TargetStatus:    orderv1.ReturnStatus_RETURN_STATUS_ESCALATED,
			ActorID:         "system",
			ActorRole:       "system",
			Note:            "escalated with live return shipment",
			Now:             now,
		}); tErr != nil {
			t.Fatalf("TransitionReturnRequest(ESCALATED): %v", tErr)
		}

		// CancelShipment(RETURN) returns FailedPrecondition (live shipment already picked up) -> Admin REJECT must NOT close!
		h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already picked up")
		_, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT,
			Note:            "rejecting",
		})
		if status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("AdminResolveReturn(REJECT) with live shipment = %v, want FailedPrecondition", err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("return status = %v, want ESCALATED (must NOT close)", gotRet.ReturnRequest.Status)
		}
	})
}

// Review Point 4: Prevent permanent stuck states on RETURN shipment failures, IN_TRANSIT 14d timeout, and AWAITING_SHIPMENT missing tracking.
func TestW2A_Review4_ReturnStuckStateGuards(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("4a_ReturnShipmentTerminalFailureEvents_EscalateToAdmin", func(t *testing.T) {
		cases := []struct {
			name      string
			inTransit bool
			status    string
			rawStatus string
			wantEsc   bool
		}{
			{name: "returned_from_in_transit", inTransit: true, status: "returned", rawStatus: "returned", wantEsc: true},
			{name: "cancelled_from_awaiting", inTransit: false, status: "cancelled", rawStatus: "cancel", wantEsc: true},
			{name: "failed_lost_from_in_transit", inTransit: true, status: "failed", rawStatus: "lost", wantEsc: true},
			{name: "failed_damage_from_in_transit", inTransit: true, status: "failed", rawStatus: "damage", wantEsc: true},
			{name: "failed_delivery_fail_retryable_does_not_escalate", inTransit: true, status: "failed", rawStatus: "delivery_fail", wantEsc: false},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				h := newTestHarness()
				ord := createDeliveredOrderHelper(t, h, "rev4a-"+tc.name, orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
				retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
				retID := retResp.ReturnRequestId
				acc4a, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
					ReturnRequestId: retID,
					Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
				})
				trk4a := acc4a.ReturnRequest.ReturnTrackingCode
				if tc.inTransit {
					_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-4a-go-"+tc.name, events.ShipmentUpdatedPayload{
						OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: trk4a, Status: "delivering",
					})
				}

				if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-4a-ev-"+tc.name, events.ShipmentUpdatedPayload{
					OrderID:         ord.Id,
					Direction:       "RETURN",
					ReturnRequestID: retID,
					TrackingCode:    trk4a,
					Status:          tc.status,
					RawStatus:       tc.rawStatus,
					Description:     "carrier event " + tc.rawStatus,
				}); err != nil {
					t.Fatalf("HandleShipmentUpdated: %v", err)
				}

				gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
				if tc.wantEsc {
					if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
						t.Fatalf("status = %v, want ESCALATED", gotRet.ReturnRequest.Status)
					}
					// Because shipment is already terminal, admin can REJECT -> CLOSED without being blocked by CancelShipment FailedPrecondition.
					h.shipping.cancelShipErr = status.Error(codes.FailedPrecondition, "already returned")
					if _, aErr := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
						ReturnRequestId: retID,
						Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_REJECT,
						Note:            "parcel returned to buyer",
					}); aErr != nil {
						t.Fatalf("AdminResolveReturn(REJECT) after terminal return shipment failed: %v", aErr)
					}
				} else {
					if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT {
						t.Fatalf("retryable failure should keep IN_TRANSIT, got %v", gotRet.ReturnRequest.Status)
					}
				}
			})
		}
	})

	t.Run("4b_InTransitHas14DayDeadline_ExpiredEscalates", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "rev4b-1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId
		acc4b, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-4b-deliv", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: acc4b.ReturnRequest.ReturnTrackingCode, Status: "delivering",
		})

		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		wantDL := now.Add(14 * 24 * time.Hour)
		if gotRet.ReturnRequest.DeadlineAt == nil || !gotRet.ReturnRequest.DeadlineAt.AsTime().Equal(wantDL) {
			t.Fatalf("IN_TRANSIT deadline = %v, want %v", gotRet.ReturnRequest.DeadlineAt, wantDL)
		}

		// Before 14d: no expiry
		now = now.Add(14*24*time.Hour - time.Minute)
		if n, _ := h.svc.ScanReturnDeadlines(context.Background()); n != 0 {
			t.Fatalf("expected 0 before 14d, got %d", n)
		}

		// After 14d: escalates to ESCALATED!
		now = now.Add(2 * time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(IN_TRANSIT 14d) = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ = h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("status after 14d transit timeout = %v, want ESCALATED", gotRet.ReturnRequest.Status)
		}
	})

	t.Run("4c_AwaitingShipmentNoTracking_SetsImmediateDeadlineAndEscalatesNotCloses", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "rev4c-notrk", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		h.shipping.createReturnShipErr = status.Error(codes.Unavailable, "shipping permanently down")
		resp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		wantDL := now.Add(5 * 24 * time.Hour)
		if resp.ReturnRequest.DeadlineAt == nil || !resp.ReturnRequest.DeadlineAt.AsTime().Equal(wantDL) {
			t.Fatalf("AWAITING_SHIPMENT without tracking must have immediate 5d deadline %v, got %v", wantDL, resp.ReturnRequest.DeadlineAt)
		}

		// After 5d with still no tracking -> MUST ESCALATE (NOT CLOSE, because it's not buyer's fault!).
		now = now.Add(5*24*time.Hour + time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines(AWAITING_SHIPMENT no tracking) = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("status = %v, want ESCALATED (must NOT close when tracking was never created)", gotRet.ReturnRequest.Status)
		}
		if gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED {
			t.Fatalf("order status = %v, want RETURN_REQUESTED", gotOrd.Order.Status)
		}
	})

	t.Run("4c_CreateReturnShipmentFallbackToEmptyCarrierOnDisabledCarrier", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "rev4c-fallback", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		h.shipping.onCreateReturnShipment = func(req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
			if req.GetCarrierCode() != "" || req.GetServiceCode() != "" {
				return nil, status.Error(codes.FailedPrecondition, "carrier disabled")
			}
			return &shippingv1.CreateReturnShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId:      "rshp-fallback",
					OrderId:         req.GetOrderId(),
					CarrierCode:     "mock_express",
					ServiceCode:     "fast",
					TrackingCode:    "RET-FALLBACK-1",
					LabelUrl:        "https://labels.sandbox/ret-fallback",
					FeeVnd:          28000,
					Direction:       shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
					ReturnRequestId: req.GetReturnRequestId(),
				},
			}, nil
		}

		resp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		if resp.ReturnRequest.ReturnTrackingCode != "RET-FALLBACK-1" {
			t.Fatalf("expected fallback tracking RET-FALLBACK-1, got %+v", resp.ReturnRequest)
		}
		if len(h.shipping.createReturnShipCalls) != 2 {
			t.Fatalf("expected 2 CreateReturnShipment attempts (original + empty fallback), got %d", len(h.shipping.createReturnShipCalls))
		}
		if h.shipping.createReturnShipCalls[1].GetCarrierCode() != "" || h.shipping.createReturnShipCalls[1].GetServiceCode() != "" {
			t.Fatalf("second attempt must clear carrier_code and service_code, got %+v", h.shipping.createReturnShipCalls[1])
		}
	})
}

// Review Point 5: executeRefundingSideEffects with remaining > 0 and empty PaymentID or nil Payment client must error and stay REFUNDING.
func TestW2A_Review5_RefundingMissingPaymentIDOrNilClientStaysRefunding(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	t.Run("NilPaymentClient_StaysRefunding", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "rev5-nil-pay", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		h.svc.clients.Payment = nil
		resp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		if resp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
			t.Fatalf("status = %v, want REFUNDING when Payment client is nil and remaining > 0", resp.ReturnRequest.Status)
		}
		if n, _ := h.svc.ScanRefundingReturns(context.Background()); n != 0 {
			t.Fatalf("ScanRefundingReturns should fail and handle 0 while Payment client is nil, got %d", n)
		}
		rawAgg, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rawAgg.Request.RefundDone {
			t.Fatalf("RefundDone must remain false when Payment client is nil")
		}
	})

	t.Run("EmptyPaymentID_StaysRefunding", func(t *testing.T) {
		h := newTestHarness()
		ord := createDeliveredOrderHelper(t, h, "rev5-empty-pid", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		// Clear payment_id on the order to simulate missing payment reference.
		h.store.SetOrderPaymentIDForTest(ord.Id, "")

		resp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_REFUND,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		if resp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
			t.Fatalf("status = %v, want REFUNDING when PaymentID is empty and remaining > 0", resp.ReturnRequest.Status)
		}
		rawAgg, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rawAgg.Request.RefundDone {
			t.Fatalf("RefundDone must remain false when PaymentID is empty")
		}
	})
}

// Review Point 6: ScanAutoCancelUnconfirmedOrders must check CONFIRMED (+ confirmed_at cutoff) inside the lock so a racing SellerConfirmOrder is never cancelled.
func TestW2A_Review6_AutoCancelChecksConfirmedInsideTx(t *testing.T) {
	h := newTestHarness()
	wrapped := &interceptStore{Store: h.store}
	h.svc.store = wrapped

	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return now }

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")

	resp, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
		IdempotencyKey: "rev6-race-confirm",
		BuyerId:        "buyer-1",
		AddressId:      "addr-buyer-1",
		Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
		PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	ordID := resp.Orders[0].Id

	// Right before auto-cancel enters TransitionOrderStatus, seller confirms the order -> PREPARING!
	raced := false
	wrapped.onTransitionOrderStatus = func(ctx context.Context, params store.SingleOrderTransitionParams) (*store.OrderTransitionResult, error) {
		if params.ActorID == "order-auto-cancel-job" && !raced {
			raced = true
			if _, sErr := h.svc.SellerConfirmOrder(sellerCtx, &orderv1.SellerConfirmOrderRequest{
				OrderId: ordID,
				ShopId:  "shop-1",
			}); sErr != nil {
				t.Errorf("racing SellerConfirmOrder failed: %v", sErr)
			}
		}
		return h.store.TransitionOrderStatus(ctx, params)
	}

	now = now.Add(49 * time.Hour)
	n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background())
	if err != nil {
		t.Fatalf("ScanAutoCancelUnconfirmedOrders: %v", err)
	}
	if n != 0 {
		t.Fatalf("auto-cancel handled = %d, want 0 after racing SellerConfirmOrder", n)
	}
	got, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ordID})
	if got.Order.Status != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		t.Fatalf("order status = %v, want PREPARING (auto-cancel must NOT cancel a freshly confirmed order)", got.Order.Status)
	}
}

// Review Point 7: Randomized scan ordering avoids starvation when >= 20 oldest records fail permanently.
func TestW2A_Review7_ScanQueuesDoNotStarveBehind20PoisonRecords(t *testing.T) {
	h := newTestHarness()
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	h.svc.nowFn = func() time.Time { return now }

	buyerCtx := ctxWithUser("buyer-1", "buyer", "")

	// Create 20 oldest "poison" CONFIRMED orders that always fail CancelShipment with Unavailable.
	poisonIDs := make(map[string]bool, 20)
	for i := 0; i < 20; i++ {
		ts := now.Add(time.Duration(i) * time.Minute)
		h.svc.nowFn = func() time.Time { return ts }
		r, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: fmt.Sprintf("rev7-poison-%d", i),
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout poison %d: %v", i, err)
		}
		id := r.Orders[0].Id
		poisonIDs[id] = true
		_ = h.svc.HandleShipmentUpdated(context.Background(), fmt.Sprintf("evt-rev7-trk-%d", i), events.ShipmentUpdatedPayload{
			OrderID: id, Status: "picking", TrackingCode: fmt.Sprintf("TRK-POISON-%d", i),
		})
	}

	// Create 5 newer healthy CONFIRMED orders (no tracking) that can be auto-cancelled cleanly.
	healthyIDs := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		ts := now.Add(time.Duration(30+i) * time.Minute)
		h.svc.nowFn = func() time.Time { return ts }
		r, err := h.svc.CreateCheckout(buyerCtx, &orderv1.CreateCheckoutRequest{
			IdempotencyKey: fmt.Sprintf("rev7-healthy-%d", i),
			BuyerId:        "buyer-1",
			AddressId:      "addr-buyer-1",
			Items:          []*orderv1.CheckoutItemInput{{SkuId: "sku-1", Quantity: 1}},
			PaymentMethod:  orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD,
		})
		if err != nil {
			t.Fatalf("CreateCheckout healthy %d: %v", i, err)
		}
		healthyIDs = append(healthyIDs, r.Orders[0].Id)
	}

	h.shipping.cancelShipErr = status.Error(codes.Internal, "poison shipment permanently failing")

	// Advance past 48h and run multiple sweeps: all 5 healthy orders must be cancelled despite 20 older poison orders.
	expiredNow := now.Add(50 * time.Hour)
	h.svc.nowFn = func() time.Time { return expiredNow }
	cancelledHealthy := 0
	for sweep := 0; sweep < 20; sweep++ {
		n, err := h.svc.ScanAutoCancelUnconfirmedOrders(context.Background())
		if err != nil {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
		cancelledHealthy += n
		if cancelledHealthy == len(healthyIDs) {
			break
		}
	}
	if cancelledHealthy != len(healthyIDs) {
		t.Fatalf("healthy orders cancelled = %d/%d (starved behind 20 oldest poison records!)", cancelledHealthy, len(healthyIDs))
	}
}

// Review Round 2 P1: After a RETURN shipment fails/returns -> ESCALATED -> Admin RETURN_AND_REFUND:
//   - Branch 1: Entering AWAITING_SHIPMENT clears old tracking/label/fee and return_shipment_terminal,
//     records old tracking in return_events.note, and creates a new RETURN shipment.
//   - Branch 2: Late RETURN shipment.updated events for an old tracking code (or while current tracking is empty) are ignored.
//   - Branch 3: If shipping rejects CreateReturnShipment with FailedPrecondition (old shipment failed/lost),
//     return stays AWAITING_SHIPMENT with empty tracking and escalates back to ESCALATED (NOT CLOSED) on deadline expiry.
func TestW2A_ReviewP1_ReturnAndRefundAfterFailedReturnShipment(t *testing.T) {
	buyerCtx := ctxWithUser("buyer-1", "buyer", "")
	sellerCtx := ctxWithUser("seller-1", "seller", "shop-1")
	adminCtx := ctxWithUser("admin-1", "admin", "")

	t.Run("Branch1_AdminReturnAndRefund_ClearsOldTrackingLogsAuditNoteAndCreatesNewShipment", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "revp1-b1", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, err := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		if err != nil {
			t.Fatalf("RequestReturn: %v", err)
		}
		retID := retResp.ReturnRequestId

		shipSeq := 0
		h.shipping.onCreateReturnShipment = func(req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
			shipSeq++
			return &shippingv1.CreateReturnShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId:      fmt.Sprintf("rshp-b1-%d", shipSeq),
					OrderId:         req.GetOrderId(),
					CarrierCode:     "ghn",
					ServiceCode:     "standard",
					TrackingCode:    fmt.Sprintf("RET-B1-%d", shipSeq),
					LabelUrl:        fmt.Sprintf("https://labels.sandbox/ret-b1-%d", shipSeq),
					FeeVnd:          int64(20000 + shipSeq*5000),
					Direction:       shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
					ReturnRequestId: req.GetReturnRequestId(),
				},
			}, nil
		}

		accResp, err := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		if err != nil {
			t.Fatalf("RespondReturn: %v", err)
		}
		if accResp.ReturnRequest.ReturnTrackingCode != "RET-B1-1" {
			t.Fatalf("initial return tracking = %q, want RET-B1-1", accResp.ReturnRequest.ReturnTrackingCode)
		}

		// Old RETURN shipment is returned to buyer -> ESCALATED with return_shipment_terminal = true.
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-b1-ret", events.ShipmentUpdatedPayload{
			OrderID:         ord.Id,
			Direction:       "RETURN",
			ReturnRequestID: retID,
			TrackingCode:    "RET-B1-1",
			Status:          "returned",
			RawStatus:       "returned",
			Description:     "Returned to sender",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(returned): %v", err)
		}

		// Admin resolves ESCALATED -> RETURN_AND_REFUND -> MUST clear old tracking, record it in return_events.note, and create RET-B1-2!
		resResp, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
			Note:            "Re-ship return parcel",
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn(RETURN_AND_REFUND): %v", err)
		}
		rr := resResp.GetReturnRequest()
		if rr.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			t.Fatalf("status = %v, want AWAITING_SHIPMENT", rr.Status)
		}
		if len(h.shipping.createReturnShipCalls) != 2 {
			t.Fatalf("CreateReturnShipment calls = %d, want 2 (must create new return shipment)", len(h.shipping.createReturnShipCalls))
		}
		if rr.ReturnTrackingCode != "RET-B1-2" || rr.ReturnLabelUrl != "https://labels.sandbox/ret-b1-2" || rr.ReturnFeeVnd != 30000 {
			t.Fatalf("expected new return shipment RET-B1-2/30000, got tracking=%q label=%q fee=%d", rr.ReturnTrackingCode, rr.ReturnLabelUrl, rr.ReturnFeeVnd)
		}
		rawAgg, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rawAgg.Request.ReturnShipmentTerminal {
			t.Fatalf("return_shipment_terminal must be reset to false on AWAITING_SHIPMENT")
		}
		foundAuditNote := false
		for _, ev := range rr.Events {
			if ev.FromStatus == orderv1.ReturnStatus_RETURN_STATUS_ESCALATED &&
				ev.ToStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT &&
				strings.Contains(ev.Note, "RET-B1-1") {
				foundAuditNote = true
			}
		}
		if !foundAuditNote {
			t.Fatalf("expected ESCALATED->AWAITING_SHIPMENT return_event note to include previous tracking RET-B1-1, got %+v", rr.Events)
		}

		// Deliver new shipment RET-B1-2 -> RECEIVED -> Seller confirm -> REFUNDED.
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-b1-deliv2", events.ShipmentUpdatedPayload{
			OrderID:         ord.Id,
			Direction:       "RETURN",
			ReturnRequestID: retID,
			TrackingCode:    "RET-B1-2",
			Status:          "delivered",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(delivered RET-B1-2): %v", err)
		}
		confResp, err := h.svc.ConfirmReturnReceived(sellerCtx, &orderv1.ConfirmReturnReceivedRequest{ReturnRequestId: retID})
		if err != nil {
			t.Fatalf("ConfirmReturnReceived: %v", err)
		}
		if confResp.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_REFUNDED {
			t.Fatalf("final status = %v, want REFUNDED", confResp.ReturnRequest.Status)
		}
	})

	t.Run("Branch2_StaleReturnShipmentEvent_OldTrackingOrEmptyCurrentTracking_Ignored", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "revp1-b2", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		shipSeq := 0
		failNext := false
		h.shipping.onCreateReturnShipment = func(req *shippingv1.CreateReturnShipmentRequest) (*shippingv1.CreateReturnShipmentResponse, error) {
			if failNext {
				return nil, status.Error(codes.Unavailable, "shipping temporarily unavailable")
			}
			shipSeq++
			return &shippingv1.CreateReturnShipmentResponse{
				Shipment: &shippingv1.Shipment{
					ShipmentId:      fmt.Sprintf("rshp-b2-%d", shipSeq),
					OrderId:         req.GetOrderId(),
					CarrierCode:     "ghn",
					ServiceCode:     "standard",
					TrackingCode:    fmt.Sprintf("RET-B2-%d", shipSeq),
					LabelUrl:        fmt.Sprintf("https://labels.sandbox/ret-b2-%d", shipSeq),
					FeeVnd:          25000,
					Direction:       shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_RETURN,
					ReturnRequestId: req.GetReturnRequestId(),
				},
			}, nil
		}

		_, _ = h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		_ = h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-b2-cancel1", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "RET-B2-1", Status: "cancelled", RawStatus: "cancelled",
		})

		// Admin approves RETURN_AND_REFUND while shipping is temporarily down -> AWAITING_SHIPMENT with empty tracking.
		failNext = true
		_, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}

		// Case 2a: Late event from old tracking RET-B2-1 while current tracking is empty MUST be ignored!
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-b2-late-empty", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "RET-B2-1", Status: "delivered",
		}); err != nil {
			t.Fatalf("late event with empty current tracking: %v", err)
		}
		got, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT || got.ReturnRequest.ReturnTrackingCode != "" {
			t.Fatalf("stale event mutated return while tracking empty: status=%v tracking=%q", got.ReturnRequest.Status, got.ReturnRequest.ReturnTrackingCode)
		}

		// Shipping recovers -> watchdog creates RET-B2-2.
		failNext = false
		if n, err := h.svc.ScanReturnPendingShipments(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnPendingShipments = %d, %v; want 1, nil", n, err)
		}

		// Case 2b: Late event from old tracking RET-B2-1 after RET-B2-2 is attached MUST be ignored!
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-b2-late-old", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "RET-B2-1", Status: "delivered",
		}); err != nil {
			t.Fatalf("late event with old tracking: %v", err)
		}
		got, _ = h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			t.Fatalf("stale event for RET-B2-1 mutated return with current RET-B2-2: status=%v", got.ReturnRequest.Status)
		}

		// Matching event for RET-B2-2 transitions to RECEIVED!
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-b2-match", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: "RET-B2-2", Status: "delivered",
		}); err != nil {
			t.Fatalf("matching event for RET-B2-2: %v", err)
		}
		got, _ = h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		if got.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_RECEIVED {
			t.Fatalf("matching event status = %v, want RECEIVED", got.ReturnRequest.Status)
		}
	})

	t.Run("Branch3_AdminReturnAndRefund_FailedPreconditionKeepsEmptyTrackingAndEscalatesOnExpiry", func(t *testing.T) {
		h := newTestHarness()
		now := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
		h.svc.nowFn = func() time.Time { return now }

		ord := createDeliveredOrderHelper(t, h, "revp1-b3", orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR)
		retResp, _ := h.svc.RequestReturn(buyerCtx, &orderv1.RequestReturnRequest{OrderId: ord.Id, Reason: "defect"})
		retID := retResp.ReturnRequestId

		accResp, _ := h.svc.RespondReturn(sellerCtx, &orderv1.RespondReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnSellerDecision_RETURN_SELLER_DECISION_ACCEPT_RETURN,
		})
		oldTrack := accResp.ReturnRequest.ReturnTrackingCode

		// Old shipment is lost (terminal failed) -> ESCALATED with return_shipment_terminal = true.
		if err := h.svc.HandleShipmentUpdated(context.Background(), "evt-p1-b3-lost", events.ShipmentUpdatedPayload{
			OrderID: ord.Id, Direction: "RETURN", ReturnRequestID: retID, TrackingCode: oldTrack, Status: "failed", RawStatus: "lost", Description: "Parcel lost",
		}); err != nil {
			t.Fatalf("HandleShipmentUpdated(failed/lost): %v", err)
		}

		// Shipping rejects new RETURN shipment with FailedPrecondition because old shipment failed/lost.
		h.shipping.createReturnShipErr = status.Error(codes.FailedPrecondition, "previous return shipment failed/lost")
		resResp, err := h.svc.AdminResolveReturn(adminCtx, &orderv1.AdminResolveReturnRequest{
			ReturnRequestId: retID,
			Decision:        orderv1.ReturnAdminDecision_RETURN_ADMIN_DECISION_RETURN_AND_REFUND,
			Note:            "Try return again",
		})
		if err != nil {
			t.Fatalf("AdminResolveReturn: %v", err)
		}
		rr := resResp.GetReturnRequest()
		if rr.Status != orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			rr.ReturnTrackingCode != "" || rr.ReturnLabelUrl != "" || rr.ReturnFeeVnd != 0 {
			t.Fatalf("expected AWAITING_SHIPMENT with cleared tracking/label/fee, got %+v", rr)
		}
		rawAgg, _ := h.store.GetReturnRequest(context.Background(), retID, "")
		if rawAgg.Request.ReturnShipmentTerminal {
			t.Fatalf("return_shipment_terminal must be false when entering AWAITING_SHIPMENT")
		}

		// On deadline expiry with still no tracking, MUST escalate back to ESCALATED (NOT CLOSED / NOT blame buyer!).
		now = now.Add(5*24*time.Hour + time.Minute)
		if n, err := h.svc.ScanReturnDeadlines(context.Background()); err != nil || n != 1 {
			t.Fatalf("ScanReturnDeadlines = %d, %v; want 1, nil", n, err)
		}
		gotRet, _ := h.svc.GetReturnRequest(buyerCtx, &orderv1.GetReturnRequestRequest{ReturnRequestId: retID})
		gotOrd, _ := h.svc.GetOrder(buyerCtx, &orderv1.GetOrderRequest{OrderId: ord.Id})
		if gotRet.ReturnRequest.Status != orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
			t.Fatalf("expired AWAITING_SHIPMENT without tracking = %v, want ESCALATED (NOT CLOSED!)", gotRet.ReturnRequest.Status)
		}
		if gotOrd.Order.Status != orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED {
			t.Fatalf("order status = %v, want RETURN_REQUESTED (must NOT complete order)", gotOrd.Order.Status)
		}
	})
}
