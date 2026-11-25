package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PaymentResultInput captures normalized payment event / RPC data.
type PaymentResultInput struct {
	EventID               string
	EventType             string
	CheckoutGroupID       string
	OrderID               string
	PaymentID             string
	Status                orderv1.PaymentResultStatus
	PaymentStatusOverride string
	Reason                string
	ActorID               string
	ActorRole             string
}

// HandlePaymentResult processes payment.succeeded, payment.failed, and payment.expired idempotently
// and runs required stock confirmation or full stock+promotion saga compensation.
func (s *OrderService) HandlePaymentResult(ctx context.Context, in PaymentResultInput) error {
	if in.EventID != "" {
		processed, err := s.store.IsEventProcessed(ctx, in.EventID)
		if err != nil {
			return fmt.Errorf("check processed_events: %w", err)
		}
		if processed {
			return nil
		}
	}

	// Review-A P0-2: perform order state transition under FOR UPDATE (checking payment_id match)
	// BEFORE running downstream ReleaseStock/ReleasePromotion/ConfirmStock so a concurrent RetryPayment
	// cannot have its stock/voucher released out from under it. Defer MarkEventProcessed until
	// downstream calls succeed so transient failures trigger NATS redelivery.
	traceID, traceparent := traceMetaFromContext(ctx)
	outcome, err := s.store.TransitionOrdersPaymentResult(ctx, store.PaymentResultTransitionParams{
		EventID:               in.EventID,
		EventType:             in.EventType,
		SkipMarkProcessed:     in.EventID != "",
		CheckoutGroupID:       strings.TrimSpace(in.CheckoutGroupID),
		OrderID:               in.OrderID,
		PaymentID:             in.PaymentID,
		ResultStatus:          in.Status,
		PaymentStatusOverride: in.PaymentStatusOverride,
		Reason:                in.Reason,
		ActorID:               fallbackStr(in.ActorID, "payment-service"),
		ActorRole:             fallbackStr(in.ActorRole, "system"),
		TraceID:               traceID,
		Traceparent:           traceparent,
		Now:                   s.nowFn(),
	})
	if err != nil {
		return err
	}
	if outcome.AlreadyProcessed {
		return nil
	}

	if len(outcome.Transitioned) > 0 {
		s.notifyOutbox()
	}

	// F5 race guard: if a late payment.succeeded arrived for an old online payment that was just
	// superseded by RetryPayment (e.g. switched to COD or another online method), immediately trigger
	// an automatic refund (RequireApprovalIfLarge=false) so the buyer is never left with a CONFIRMED
	// COD order and unrefunded paid online funds.
	if len(outcome.SupersededPaidOrders) > 0 &&
		in.Status == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED &&
		strings.TrimSpace(in.PaymentID) != "" &&
		s.clients.Payment != nil {
		if err := s.refundSupersededPaidOrders(ctx, strings.TrimSpace(in.PaymentID), outcome.SupersededPaidOrders); err != nil {
			return fmt.Errorf("refund superseded payment %s: %w", in.PaymentID, err)
		}
	}

	groupID := outcome.CheckoutGroupID
	if outcome.NeedsDownstream && groupID != "" {
		sysCtx := clients.WithInternalAdminContext(ctx)
		switch in.Status {
		case orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED:
			if s.clients.Product != nil {
				if err := s.confirmGroupStockAndClear(ctx, groupID); err != nil {
					return fmt.Errorf("confirm stock for %s: %w", groupID, err)
				}
			}
		case orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_FAILED,
			orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED:
			if s.clients.Product != nil {
				if err := clients.RetryIdempotent(sysCtx, 3, func(c context.Context) error {
					_, rErr := s.clients.Product.ReleaseStock(c, &productv1.ReleaseStockRequest{
						OrderId: groupID,
					})
					if status.Code(rErr) == codes.NotFound {
						return nil
					}
					return rErr
				}); err != nil {
					return fmt.Errorf("release stock compensation for %s: %w", groupID, err)
				}
			}
			if s.clients.Promotion != nil {
				if err := clients.RetryIdempotent(sysCtx, 3, func(c context.Context) error {
					_, rErr := s.clients.Promotion.ReleasePromotion(c, &promotionv1.ReleasePromotionRequest{
						OrderId: groupID,
					})
					if status.Code(rErr) == codes.NotFound {
						return nil
					}
					return rErr
				}); err != nil {
					return fmt.Errorf("release promotion compensation for %s: %w", groupID, err)
				}
			}
			// P3-1(b): clear pending_cancel_compensation across all cancelled orders in the group
			// (including NATS redeliveries where outcome.Transitioned is empty).
			if groupOrders, lErr := s.store.ListOrdersByCheckoutGroup(sysCtx, groupID); lErr == nil {
				for _, goAgg := range groupOrders {
					if statemachine.StringToStatus(goAgg.Order.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
						goAgg.Order.PendingCancelCompensation {
						_ = s.store.ClearOrderPendingCancelCompensation(sysCtx, goAgg.Order.ID)
					}
				}
			} else {
				for _, tr := range outcome.Transitioned {
					_ = s.store.ClearOrderPendingCancelCompensation(sysCtx, tr.ID)
				}
			}
		}
	}

	if in.EventID != "" {
		if err := s.store.MarkEventProcessed(ctx, in.EventID, in.EventType); err != nil {
			return fmt.Errorf("mark payment event processed: %w", err)
		}
	}
	return nil
}

// HandlePaymentSucceeded implements events.EventHandler for payment.succeeded.
func (s *OrderService) HandlePaymentSucceeded(ctx context.Context, eventID string, payload events.PaymentSucceededPayload) error {
	return s.HandlePaymentResult(ctx, PaymentResultInput{
		EventID:         eventID,
		EventType:       events.SubjectPaymentSucceeded,
		CheckoutGroupID: payload.OrderGroupID,
		OrderID:         payload.OrderID,
		PaymentID:       payload.PaymentID,
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
		Reason:          "Payment succeeded (" + payload.ProviderTxID + ")",
		ActorID:         "payment-service",
		ActorRole:       "system",
	})
}

// HandlePaymentFailed implements events.EventHandler for payment.failed.
func (s *OrderService) HandlePaymentFailed(ctx context.Context, eventID string, payload events.PaymentFailedPayload) error {
	return s.HandlePaymentResult(ctx, PaymentResultInput{
		EventID:         eventID,
		EventType:       events.SubjectPaymentFailed,
		CheckoutGroupID: payload.OrderGroupID,
		OrderID:         payload.OrderID,
		PaymentID:       payload.PaymentID,
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_FAILED,
		Reason:          payload.Reason,
		ActorID:         "payment-service",
		ActorRole:       "system",
	})
}

// HandlePaymentExpired implements events.EventHandler for payment.expired.
func (s *OrderService) HandlePaymentExpired(ctx context.Context, eventID string, payload events.PaymentFailedPayload) error {
	return s.HandlePaymentResult(ctx, PaymentResultInput{
		EventID:         eventID,
		EventType:       events.SubjectPaymentExpired,
		CheckoutGroupID: payload.OrderGroupID,
		OrderID:         payload.OrderID,
		PaymentID:       payload.PaymentID,
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
		Reason:          payload.Reason,
		ActorID:         "payment-service",
		ActorRole:       "system",
	})
}

// refundSupersededPaidOrders automatically refunds each payable sub-order on a non-current payment_id
// whose payment.succeeded arrived after RetryPayment moved the group forward or on an orphaned payment (F5 / Item A-B).
// Only codes.OK (including true idempotent replay resp.AlreadyExisted=true with matching fields) is treated as done;
// all errors (including codes.AlreadyExists which maps from PaymentError::Conflict in Rust payment, NotFound, and
// FailedPrecondition) are logged at ERROR with payment_id/order_id and returned for consumer retry -> NEXUSMART_DLQ.
func (s *OrderService) refundSupersededPaidOrders(ctx context.Context, supersededPaymentID string, orders []db.Order) error {
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	for _, ord := range orders {
		if ord.TotalPayableVND <= 0 {
			continue
		}
		idemKey := "superseded-late-refund:" + supersededPaymentID + ":" + ord.ID
		if err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			resp, rErr := s.clients.Payment.CreateRefund(c, &paymentv1.CreateRefundRequest{
				PaymentId:              supersededPaymentID,
				OrderId:                ord.ID,
				AmountVnd:              ord.TotalPayableVND,
				Reason:                 "Late payment succeeded for superseded checkout payment",
				IdempotencyKey:         idemKey,
				RequireApprovalIfLarge: false,
			})
			if rErr != nil {
				return rErr
			}
			if ref := resp.GetRefund(); ref != nil {
				if ref.GetStatus() == paymentv1.RefundStatus_REFUND_STATUS_FAILED {
					return status.Errorf(codes.Internal, "superseded refund %s returned FAILED status", ref.GetRefundId())
				}
				if resp.GetAlreadyExisted() {
					if pid := strings.TrimSpace(ref.GetPaymentId()); pid != "" && pid != supersededPaymentID {
						return status.Errorf(codes.FailedPrecondition, "idempotent refund payment_id mismatch: got %s want %s", pid, supersededPaymentID)
					}
					if oid := strings.TrimSpace(ref.GetOrderId()); oid != "" && oid != ord.ID {
						return status.Errorf(codes.FailedPrecondition, "idempotent refund order_id mismatch: got %s want %s", oid, ord.ID)
					}
					if k := strings.TrimSpace(ref.GetIdempotencyKey()); k != "" && k != idemKey {
						return status.Errorf(codes.FailedPrecondition, "idempotent refund key mismatch: got %s want %s", k, idemKey)
					}
				}
			}
			return nil
		}); err != nil {
			s.logger.ErrorContext(ctx, "failed to create automatic refund for superseded paid order; returning error for consumer retry/DLQ",
				"payment_id", supersededPaymentID,
				"order_id", ord.ID,
				"checkout_group_id", ord.CheckoutGroupID,
				"amount_vnd", ord.TotalPayableVND,
				"idempotency_key", idemKey,
				"error", err,
			)
			return fmt.Errorf("create automatic refund for superseded payment %s order %s: %w", supersededPaymentID, ord.ID, err)
		}
	}
	return nil
}

// HandlePaymentRefunded records a payment.refunded event idempotently on the order timeline
// and settles any REFUNDING return request waiting on F11 large-refund approval.
func (s *OrderService) HandlePaymentRefunded(ctx context.Context, eventID string, payload events.PaymentRefundedPayload) error {
	if _, err := s.store.RecordPaymentRefunded(ctx, store.PaymentRefundedParams{
		EventID:          eventID,
		EventType:        events.SubjectPaymentRefunded,
		PaymentID:        payload.PaymentID,
		OrderID:          payload.OrderID,
		RefundID:         payload.RefundID,
		IdempotencyKey:   payload.IdempotencyKey,
		AmountVND:        payload.AmountVND,
		Reason:           payload.Reason,
		IsPostSettlement: payload.IsPostSettlement,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(payload.OrderID) != "" {
		if err := s.onPaymentRefundApprovedForReturn(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}

// HandlePaymentRefundRejected processes payment.refund_rejected idempotently for F11 large-refund
// approvals rejected by a checker, transitioning the waiting return from REFUNDING -> ESCALATED.
// Handles duplicate event_id, out-of-order delivery before refund_id is persisted, and cleanly
// ignores manual admin refunds that do not belong to an approval-gated REFUNDING return.
func (s *OrderService) HandlePaymentRefundRejected(ctx context.Context, eventID string, payload events.PaymentRefundRejectedPayload) error {
	if eventID != "" {
		processed, err := s.store.IsEventProcessed(ctx, eventID)
		if err != nil {
			return fmt.Errorf("check processed_events: %w", err)
		}
		if processed {
			return nil
		}
	}

	if err := s.onPaymentRefundRejectedForReturn(ctx, payload); err != nil {
		return err
	}

	if eventID != "" {
		if err := s.store.MarkEventProcessed(ctx, eventID, events.SubjectPaymentRefundRejected); err != nil {
			return fmt.Errorf("mark payment.refund_rejected processed: %w", err)
		}
	}
	return nil
}

// HandleShipmentUpdated processes shipment.updated events idempotently for both FORWARD and RETURN directions.
func (s *OrderService) HandleShipmentUpdated(ctx context.Context, eventID string, payload events.ShipmentUpdatedPayload) error {
	traceID, traceparent := traceMetaFromContext(ctx)

	direction := strings.ToUpper(strings.TrimSpace(payload.Direction))
	if direction == "RETURN" {
		outcome, err := s.store.ApplyReturnShipmentUpdate(ctx, store.ReturnShipmentUpdateParams{
			EventID:         eventID,
			EventType:       events.SubjectShipmentUpdated,
			OrderID:         payload.OrderID,
			ReturnRequestID: payload.ReturnRequestID,
			ShipmentStatus:  payload.Status,
			RawStatus:       payload.RawStatus,
			TrackingCode:    payload.TrackingCode,
			Description:     payload.Description,
			InspectDuration: s.returnInspectDuration(),
			TransitDuration: returnTransitWindow,
			TraceID:         traceID,
			Traceparent:     traceparent,
			Now:             s.nowFn(),
		})
		if err != nil {
			return err
		}
		if outcome.Transitioned {
			s.notifyOutbox()
		}
		return nil
	}

	isReturned := strings.EqualFold(strings.TrimSpace(payload.Status), "returned")
	if isReturned && eventID != "" {
		processed, err := s.store.IsEventProcessed(ctx, eventID)
		if err != nil {
			return fmt.Errorf("check processed_events: %w", err)
		}
		if processed {
			return nil
		}
	}

	// P0 M1: Block moving an order to SHIPPING/DELIVERED/RETURNED while its checkout_group is still IN_PROGRESS
	// (causes NATS redelivery after payment initialization finishes instead of emitting order.delivered before payment exists).
	if strings.TrimSpace(payload.OrderID) != "" {
		if agg, gErr := s.store.GetOrderWithDetails(ctx, payload.OrderID); gErr == nil && agg != nil {
			if pErr := s.requireCheckoutPaymentInitialized(ctx, agg.Order.CheckoutGroupID); pErr != nil {
				return pErr
			}
		} else if gErr != nil && !errors.Is(gErr, store.ErrNotFound) {
			return gErr
		}
	}

	outcome, err := s.store.ApplyShipmentUpdate(ctx, store.ShipmentUpdateParams{
		EventID:           eventID,
		EventType:         events.SubjectShipmentUpdated,
		SkipMarkProcessed: isReturned && eventID != "",
		OrderID:           payload.OrderID,
		ShipmentStatus:    payload.Status,
		CarrierCode:       payload.CarrierCode,
		ServiceCode:       payload.ServiceCode,
		TrackingCode:      payload.TrackingCode,
		LabelURL:          payload.LabelURL,
		Description:       payload.Description,
		TraceID:           traceID,
		Traceparent:       traceparent,
		Now:               s.nowFn(),
	})
	if err != nil {
		return err
	}
	if outcome.AlreadyProcessed {
		return nil
	}
	if outcome.NeedsOrphanShipmentCancel {
		s.compensateOrphanForwardShipment(ctx, payload.OrderID, fmt.Sprintf("orphan forward shipment event (%s) received for cancelled order", strings.TrimSpace(payload.Status)))
		if isReturned && eventID != "" {
			if mErr := s.store.MarkEventProcessed(ctx, eventID, events.SubjectShipmentUpdated); mErr != nil {
				return fmt.Errorf("mark orphan-shipment event processed: %w", mErr)
			}
		}
		return nil
	}
	if outcome.NotFound {
		if isReturned && eventID != "" {
			if mErr := s.store.MarkEventProcessed(ctx, eventID, events.SubjectShipmentUpdated); mErr != nil {
				return fmt.Errorf("mark missing-order shipment event processed: %w", mErr)
			}
		}
		return nil
	}

	if outcome.EmittedOutbox || outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_DELIVERED || outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
		s.notifyOutbox()
	}

	if (outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_RETURNED || outcome.AlreadyReturned) && outcome.Order != nil {
		if err := s.compensateReturnedOrder(ctx, outcome.Order.Order, outcome.Order.Items, outcome.TransitionedFrom, payload.Description); err != nil {
			return err
		}
		clearCtx, clearCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer clearCancel()
		_ = s.store.ClearOrderPendingReturnCompensation(clearCtx, outcome.Order.Order.ID)
	}

	if isReturned && eventID != "" {
		if err := s.store.MarkEventProcessed(ctx, eventID, events.SubjectShipmentUpdated); err != nil {
			return fmt.Errorf("mark shipment returned event processed: %w", err)
		}
	}

	return nil
}

// HandleShipmentCreateFailed processes shipment.create_failed events idempotently (E18).
// Records orders.shipment_job_status="FAILED" when no tracking_code is attached yet so the seller can retry.
func (s *OrderService) HandleShipmentCreateFailed(ctx context.Context, eventID string, payload events.ShipmentCreateFailedPayload) error {
	if strings.TrimSpace(payload.OrderID) == "" {
		return fmt.Errorf("shipment.create_failed missing order_id")
	}
	outcome, err := s.store.ApplyShipmentCreateFailed(ctx, store.ShipmentCreateFailedParams{
		EventID:      eventID,
		EventType:    events.SubjectShipmentCreateFailed,
		JobID:        payload.JobID,
		OrderID:      payload.OrderID,
		CarrierCode:  payload.CarrierCode,
		ServiceCode:  payload.ServiceCode,
		Attempts:     payload.Attempts,
		ErrorMessage: payload.ErrorMessage,
		Now:          s.nowFn(),
	})
	if err != nil {
		return err
	}
	if outcome == nil || !outcome.AlreadyProcessed {
		s.logger.WarnContext(ctx, "async shipment creation failed",
			"order_id", payload.OrderID,
			"job_id", payload.JobID,
			"carrier_code", payload.CarrierCode,
			"attempts", payload.Attempts,
			"updated", outcome != nil && outcome.Updated,
			"error_message", payload.ErrorMessage,
		)
	}
	return nil
}

// compensateReturnedOrder runs all idempotent post-commit compensations for a SHIPPING -> RETURNED
// (forward delivery failure) order: ConfirmStock+Restock, ReleasePromotionShop, COD rejection, and VietQR refund.
func (s *OrderService) compensateReturnedOrder(
	ctx context.Context,
	ord db.Order,
	items []db.OrderItem,
	fromStatus orderv1.OrderStatus,
	description string,
) error {
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	// Review-A P1-7 + P0-2: ConfirmStock(checkout_group_id) before RestockOrderItems so a failed checkout
	// ConfirmStock never produces phantom stock or leaked reserved_stock.
	if err := s.restockOrderItemsWithConfirmErr(compCtx, ord.CheckoutGroupID, ord.ID, items); err != nil {
		return fmt.Errorf("restock returned order %s: %w", ord.ID, err)
	}

	// R5: Release shop-scoped promotions (coins, shop voucher, flash slots) for returned sub-order.
	if err := s.releasePromotionShopErr(compCtx, ord.CheckoutGroupID, ord.ShopID, "order_returned"); err != nil {
		return fmt.Errorf("release shop promotion for returned order %s: %w", ord.ID, err)
	}

	// R6 (ORD-B6): COD delivery rejection (SHIPPING -> RETURNED) increments buyer's cod_rejected_count.
	if fromStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING &&
		statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD &&
		s.clients.User != nil {
		rejReason := strings.TrimSpace(description)
		if rejReason == "" {
			rejReason = "COD delivery rejected/returned"
		}
		if rErr := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			_, err := s.clients.User.RecordCODRejection(c, &userv1.RecordCODRejectionRequest{
				UserId:  ord.BuyerID,
				OrderId: ord.ID,
				Reason:  rejReason,
			})
			return err
		}); rErr != nil {
			return fmt.Errorf("record COD rejection for returned order %s: %w", ord.ID, rErr)
		}
	}

	// Review-A P0-1: if order was paid online, refund only the remaining unrefunded balance.
	wasPaidOnline := statemachine.IsOnlineMethod(ord.PaymentMethod) &&
		strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") &&
		ord.TotalPayableVND > 0
	remaining := ord.TotalPayableVND - ord.RefundedVND
	if wasPaidOnline && remaining > 0 && ord.PaymentID != "" && s.clients.Payment != nil {
		if rErr := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			_, err := s.clients.Payment.CreateRefund(c, &paymentv1.CreateRefundRequest{
				PaymentId:      ord.PaymentID,
				OrderId:        ord.ID,
				AmountVnd:      remaining,
				Reason:         "Order returned to seller",
				IdempotencyKey: "order-return-refund:" + ord.ID,
			})
			return err
		}); rErr != nil {
			return fmt.Errorf("create refund for returned order %s: %w", ord.ID, rErr)
		}
	}
	return nil
}

// ScanExpiredPendingOrders runs one sweep of the UC-PAY06 payment expiry watchdog.
func (s *OrderService) ScanExpiredPendingOrders(ctx context.Context) (int, error) {
	candidates, err := s.store.FindExpiredPendingGroups(ctx, 20)
	if err != nil {
		return 0, err
	}

	handled := 0
	for _, cand := range candidates {
		if err := s.processExpiredGroup(ctx, cand); err != nil {
			s.logger.WarnContext(ctx, "expiry watchdog skipped group due to error",
				"checkout_group_id", cand.CheckoutGroupID, "error", err)
			continue
		}
		handled++
	}
	return handled, nil
}

func (s *OrderService) processExpiredGroup(ctx context.Context, cand store.ExpiredGroupCandidate) error {
	sysCtx := clients.WithInternalAdminContext(ctx)
	candPaymentID := strings.TrimSpace(cand.PaymentID)
	activePaymentID := candPaymentID

	// UC-PAY06 + Item F (#6): Query the active payment by OrderGroupId ONLY (do not pin old cand.PaymentID,
	// which can return a superseded/CANCELLED payment while an orphaned newer payment remains PENDING).
	if s.clients.Payment != nil {
		payResp, err := s.clients.Payment.GetPayment(sysCtx, &paymentv1.GetPaymentRequest{
			OrderGroupId: cand.CheckoutGroupID,
		})
		if err != nil {
			// If payment gateway/service is unreachable, do NOT cancel yet; retry next tick.
			if status.Code(err) != codes.NotFound {
				return fmt.Errorf("check payment status before expiry cancel: %w", err)
			}
		} else if pay := payResp.GetPayment(); pay != nil {
			if pid := strings.TrimSpace(pay.GetPaymentId()); pid != "" {
				activePaymentID = pid
			}
			// Guard against cancelling a legitimately moved, non-expired replacement payment in direct races.
			if candPaymentID != "" && activePaymentID != candPaymentID && pay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
				if grp, gErr := s.store.GetCheckoutGroupByID(sysCtx, cand.CheckoutGroupID); gErr == nil && grp != nil {
					if grp.PaymentID == activePaymentID && grp.PaymentExpiresAt != nil && grp.PaymentExpiresAt.After(s.nowFn()) {
						return nil
					}
				}
			}
			if done, hErr := s.handleExpiredGroupObservedPayment(sysCtx, cand.CheckoutGroupID, candPaymentID, pay); done || hErr != nil {
				return hErr
			}
			if pay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
				_, cancelErr := s.clients.Payment.CancelPayment(sysCtx, &paymentv1.CancelPaymentRequest{
					PaymentId:    activePaymentID,
					OrderGroupId: cand.CheckoutGroupID,
					Reason:       "Payment expired",
				})
				if cancelErr != nil {
					code := status.Code(cancelErr)
					switch code {
					case codes.NotFound:
						// Payment already absent; safe to proceed with order expiry cancel.
					case codes.AlreadyExists, codes.FailedPrecondition:
						// Concurrent webhook or state transition raced ahead at payment service;
						// re-read authoritative active payment status for the group before deciding.
						reResp, reErr := s.clients.Payment.GetPayment(sysCtx, &paymentv1.GetPaymentRequest{
							OrderGroupId: cand.CheckoutGroupID,
						})
						if reErr != nil {
							if status.Code(reErr) != codes.NotFound {
								return fmt.Errorf("re-check payment status after cancel race: %w", reErr)
							}
						} else if rePay := reResp.GetPayment(); rePay != nil {
							if pid := strings.TrimSpace(rePay.GetPaymentId()); pid != "" {
								activePaymentID = pid
							}
							if done, hErr := s.handleExpiredGroupObservedPayment(sysCtx, cand.CheckoutGroupID, candPaymentID, rePay); done || hErr != nil {
								return hErr
							}
							if rePay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
								return fmt.Errorf("payment %s remained PENDING after CancelPayment (%s); retry next tick", activePaymentID, code)
							}
						}
					default:
						return fmt.Errorf("cancel expired payment %s: %w", activePaymentID, cancelErr)
					}
				}
			}
		}
	}

	// Preserve candPaymentID when order_db still points to the pre-orphan payment so TransitionOrdersPaymentResult
	// cancels the expired PENDING order; fall back to activePaymentID when candPaymentID was empty or order_db moved.
	orderPaymentID := fallbackStr(candPaymentID, activePaymentID)
	if candPaymentID != "" && activePaymentID != candPaymentID {
		if grp, gErr := s.store.GetCheckoutGroupByID(sysCtx, cand.CheckoutGroupID); gErr == nil && grp != nil && grp.PaymentID == activePaymentID {
			orderPaymentID = activePaymentID
		}
	}

	return s.HandlePaymentResult(sysCtx, PaymentResultInput{
		CheckoutGroupID: cand.CheckoutGroupID,
		PaymentID:       orderPaymentID,
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
		Reason:          "Payment expired after timeout",
		ActorID:         "order-expiry-watchdog",
		ActorRole:       "system",
	})
}

func (s *OrderService) handleExpiredGroupObservedPayment(ctx context.Context, checkoutGroupID, candPaymentID string, pay *paymentv1.Payment) (bool, error) {
	switch pay.GetStatus() {
	case paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED:
		// Late payment succeeded before cancel — confirm order (or auto-refund if orphan/superseded via Item B).
		return true, s.HandlePaymentResult(ctx, PaymentResultInput{
			CheckoutGroupID: checkoutGroupID,
			PaymentID:       pay.GetPaymentId(),
			Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
			Reason:          "Payment confirmed by expiry watchdog",
			ActorID:         "order-expiry-watchdog",
			ActorRole:       "system",
		})
	case paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED:
		// Review2-O P1-2: payment was already 100% refunded before expiry sweep;
		// cancel the pending order and release reservations without downgrading payment_status to EXPIRED.
		return true, s.HandlePaymentResult(ctx, PaymentResultInput{
			CheckoutGroupID:       checkoutGroupID,
			PaymentID:             fallbackStr(candPaymentID, pay.GetPaymentId()),
			Status:                orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
			PaymentStatusOverride: "REFUNDED",
			Reason:                "Payment already refunded before order confirmation",
			ActorID:               "order-expiry-watchdog",
			ActorRole:             "system",
		})
	default:
		return false, nil
	}
}

const defaultStaleCheckoutAfter = 5 * time.Minute

// ScanStaleCheckoutGroups sweeps IN_PROGRESS checkout_groups with no sub-orders that have
// exceeded StaleCheckoutAfter, releases held promotion/stock reservations, and marks them FAILED.
func (s *OrderService) ScanStaleCheckoutGroups(ctx context.Context) (int, error) {
	staleAfter := s.cfg.StaleCheckoutAfter
	if staleAfter <= 0 {
		staleAfter = defaultStaleCheckoutAfter
	}

	groupIDs, err := s.store.FindStaleInProgressCheckoutGroups(ctx, staleAfter, 20)
	if err != nil {
		return 0, err
	}

	handled := 0
	for _, groupID := range groupIDs {
		if err := s.cleanupStaleCheckoutGroup(ctx, groupID); err != nil {
			s.logger.WarnContext(ctx, "stale checkout cleanup skipped group due to error",
				"checkout_group_id", groupID, "error", err)
			continue
		}
		handled++
	}
	return handled, nil
}

func (s *OrderService) cleanupStaleCheckoutGroup(ctx context.Context, groupID string) error {
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	// P1 M3 (claim-first, release-later): atomically lock checkout_groups FOR UPDATE, verify with a fresh
	// statement snapshot that no orders exist, and flip status='FAILED', needs_cleanup=TRUE BEFORE calling
	// ReleasePromotion/ReleaseStock so a racing PersistCheckoutBatch can never commit live orders under us.
	claimed, err := s.store.ClaimStaleCheckoutGroupForCleanup(compCtx, groupID, "STALE_CHECKOUT_EXPIRED")
	if err != nil {
		return fmt.Errorf("claim stale checkout group %s: %w", groupID, err)
	}
	if !claimed {
		return nil
	}

	if s.clients.Promotion != nil {
		if err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			_, rErr := s.clients.Promotion.ReleasePromotion(c, &promotionv1.ReleasePromotionRequest{
				OrderId: groupID,
			})
			if status.Code(rErr) == codes.NotFound {
				return nil
			}
			return rErr
		}); err != nil {
			return fmt.Errorf("release promotion for stale checkout %s: %w", groupID, err)
		}
	}

	if s.clients.Product != nil {
		if err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			_, rErr := s.clients.Product.ReleaseStock(c, &productv1.ReleaseStockRequest{
				OrderId: groupID,
			})
			if status.Code(rErr) == codes.NotFound {
				return nil
			}
			return rErr
		}); err != nil {
			return fmt.Errorf("release stock for stale checkout %s: %w", groupID, err)
		}
	}

	if err := s.store.ClearCheckoutGroupNeedsCleanup(compCtx, groupID); err != nil {
		return fmt.Errorf("clear needs_cleanup for stale checkout group %s: %w", groupID, err)
	}
	return nil
}

// ScanIncompleteCheckoutGroups recovers IN_PROGRESS checkout_groups that already have persisted
// sub-orders (e.g. CreatePayment succeeded but CompleteCheckoutWithPayment failed/crashed, or
// checkout crashed before CreatePayment finished).
func (s *OrderService) ScanIncompleteCheckoutGroups(ctx context.Context) (int, error) {
	staleAfter := s.cfg.StaleCheckoutAfter
	if staleAfter <= 0 {
		staleAfter = defaultStaleCheckoutAfter
	}

	groupIDs, err := s.store.FindIncompleteCheckoutGroupsWithOrders(ctx, staleAfter, 20)
	if err != nil {
		return 0, err
	}

	handled := 0
	for _, groupID := range groupIDs {
		if err := s.reconcileIncompleteCheckoutGroup(ctx, groupID); err != nil {
			s.logger.WarnContext(ctx, "incomplete checkout recovery skipped group due to error",
				"checkout_group_id", groupID, "error", err)
			continue
		}
		handled++
	}
	return handled, nil
}

func (s *OrderService) reconcileIncompleteCheckoutGroup(ctx context.Context, groupID string) error {
	recovered, notFound, err := s.tryFinalizeInProgressGroupFromPayment(ctx, groupID)
	if err != nil {
		return err
	}
	if recovered || !notFound {
		return nil
	}

	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	aggs, err := s.store.ListOrdersByCheckoutGroup(compCtx, groupID)
	if err != nil {
		return fmt.Errorf("list orders for incomplete checkout %s: %w", groupID, err)
	}
	if len(aggs) == 0 {
		return nil
	}

	// P0 M1: If legacy data already reached DELIVERED/COMPLETED/RETURN_REQUESTED/RETURNED without a
	// payment record (prevented on new orders by requireCheckoutPaymentInitialized), do NOT call
	// CreatePayment and do NOT close the group — log Error for manual reconciliation and return error.
	for _, agg := range aggs {
		st := statemachine.StringToStatus(agg.Order.Status)
		switch st {
		case orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
			orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
			orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
			orderv1.OrderStatus_ORDER_STATUS_RETURNED:
			s.logger.ErrorContext(compCtx,
				"manual intervention required: IN_PROGRESS checkout group contains post-delivery/returned order with no payment record",
				"checkout_group_id", groupID,
				"order_id", agg.Order.ID,
				"order_status", agg.Order.Status,
			)
			return fmt.Errorf("manual intervention required: checkout group %s has order %s in status %s with no payment record",
				groupID, agg.Order.ID, agg.Order.Status)
		}
	}

	// P1 M2: Exclude CANCELLED orders when computing live orders, live total, and forward-recovery eligibility.
	allCancelled := true
	allPending := true
	hasConfirmedOrBeyond := false
	hasTrackingOrPaid := false
	liveAggs := make([]store.OrderAggregate, 0, len(aggs))
	var liveTotalPayableVND int64
	var paymentExpiresAt *time.Time
	for _, agg := range aggs {
		ord := agg.Order
		st := statemachine.StringToStatus(ord.Status)
		if st == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			allPending = false
			continue
		}
		allCancelled = false
		liveAggs = append(liveAggs, agg)
		liveTotalPayableVND += ord.TotalPayableVND
		if ord.PaymentExpiresAt != nil {
			exp := *ord.PaymentExpiresAt
			if paymentExpiresAt == nil || exp.Before(*paymentExpiresAt) {
				paymentExpiresAt = &exp
			}
		}
		if st != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			allPending = false
			hasConfirmedOrBeyond = true // CONFIRMED, PREPARING, or legacy SHIPPING
		}
		if strings.TrimSpace(ord.TrackingCode) != "" || ord.PendingShipmentCancel || store.IsOrderShipmentQueued(ord) ||
			((strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") || strings.EqualFold(ord.PaymentStatus, "REFUNDED")) && ord.TotalPayableVND > 0) {
			hasTrackingOrPaid = true
		}
	}

	// P0 N3 (no infinite loop): all sub-orders are already CANCELLED -> close group FAILED without re-releasing promo,
	// preserving terminal EXPIRED/REFUNDED payment_status instead of overwriting with CANCELLED.
	if allCancelled {
		cancelledOrders := make([]db.Order, 0, len(aggs))
		for _, agg := range aggs {
			cancelledOrders = append(cancelledOrders, agg.Order)
		}
		closeStatus := store.ResolveAllCancelledPaymentStatus(cancelledOrders)
		if err := s.store.FailCheckoutGroup(compCtx, groupID, closeStatus); err != nil {
			return fmt.Errorf("close already-cancelled checkout group %s: %w", groupID, err)
		}
		_ = s.store.ClearCheckoutGroupPendingStockConfirm(compCtx, groupID)
		return nil
	}

	// P0 N3 + P1 M2 (forward recovery): if ANY live sub-order is CONFIRMED/PREPARING/SHIPPING
	// (COD, 0-VND, or legacy SHIPPING), NEVER cancel or release promotions/stock — create the payment
	// idempotently ("checkout:"+groupID) for ONLY the live (non-CANCELLED) orders and live total.
	if hasConfirmedOrBeyond {
		if s.clients.Payment == nil {
			return fmt.Errorf("payment client unavailable for forward recovery of %s", groupID)
		}
		buyerID := liveAggs[0].Order.BuyerID
		payReq, rErr := buildCreatePaymentRequest(
			groupID,
			buyerID,
			liveTotalPayableVND,
			liveAggs[0].Order.PaymentMethod,
			paymentExpiresAt,
			liveAggs,
			effectiveSalesChannel(liveAggs[0].Order.SalesChannel),
			liveAggs[0].Order.StorefrontShopID,
		)
		if rErr != nil {
			return fmt.Errorf("build forward-recover CreatePayment for %s: %w", groupID, rErr)
		}
		payResp, pErr := s.clients.Payment.CreatePayment(compCtx, payReq)
		if pErr != nil {
			return fmt.Errorf("forward-recover CreatePayment for %s: %w", groupID, pErr)
		}
		pay := payResp.GetPayment()
		if pay == nil || strings.TrimSpace(pay.GetPaymentId()) == "" {
			return fmt.Errorf("forward-recover CreatePayment for %s returned empty payment", groupID)
		}
		if err := s.finalizeRecoveredCheckoutGroup(compCtx, groupID, pay, liveAggs); err != nil {
			return err
		}
		return nil
	}

	// P0 N3: Only cancel and release stock+promotion when EVERY order in the group is still unpaid PENDING (VietQR)
	// with no tracking code.
	traceID, traceparent := traceMetaFromContext(ctx)
	if !allPending || hasTrackingOrPaid {
		// Mixed PENDING + CANCELLED (zero CONFIRMED+): cancel only remaining PENDING orders without
		// releasing group promotions here (leaving pending_cancel_compensation for cancel watchdog) so the
		// group closes FAILED once zero live orders remain and never loops infinitely.
		cancelRes, cErr := s.store.CancelCheckoutGroupOrders(compCtx, store.CancelGroupOrdersParams{
			CheckoutGroupID: groupID,
			Reason:          "Checkout payment was never initialized before timeout",
			ActorID:         "order-checkout-recovery",
			ActorRole:       "system",
			PaymentStatus:   "FAILED",
			OnlyPending:     true,
			TraceID:         traceID,
			Traceparent:     traceparent,
		})
		if cErr != nil {
			return fmt.Errorf("cancel remaining pending orders for %s: %w", groupID, cErr)
		}
		if cancelRes.HasLiveOrders {
			return fmt.Errorf("incomplete checkout group %s still has live orders; retry next tick", groupID)
		}
		s.notifyOutbox()
		return nil
	}

	cancelRes, cancelErr := s.store.CancelCheckoutGroupOrders(compCtx, store.CancelGroupOrdersParams{
		CheckoutGroupID: groupID,
		Reason:          "Checkout payment was never initialized before timeout",
		ActorID:         "order-checkout-recovery",
		ActorRole:       "system",
		PaymentStatus:   "FAILED",
		OnlyPending:     true,
		TraceID:         traceID,
		Traceparent:     traceparent,
	})
	if cancelErr != nil {
		return fmt.Errorf("cancel orphaned incomplete checkout group %s: %w", groupID, cancelErr)
	}
	if cancelRes.HasLiveOrders {
		return fmt.Errorf("incomplete checkout group %s transitioned before cancel; retry next tick", groupID)
	}

	s.compensateCancelledCheckoutGroup(compCtx, groupID, cancelRes.Cancelled, aggs, true)
	s.notifyOutbox()
	return nil
}

// tryFinalizeInProgressGroupFromPayment checks PaymentService by OrderGroupId for an IN_PROGRESS
// checkout_group that already has persisted orders and finalizes it atomically without downgrading.
// Returns (recovered=true, notFound=false, nil) once finalized, or (false, true, nil) when PaymentService
// definitively reports NotFound.
func (s *OrderService) tryFinalizeInProgressGroupFromPayment(ctx context.Context, groupID string) (bool, bool, error) {
	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	aggs, err := s.store.ListOrdersByCheckoutGroup(compCtx, groupID)
	if err != nil {
		return false, false, fmt.Errorf("list orders for incomplete checkout %s: %w", groupID, err)
	}
	if len(aggs) == 0 {
		return false, false, nil
	}
	if s.clients.Payment == nil {
		return false, false, nil
	}

	payResp, pErr := s.clients.Payment.GetPayment(compCtx, &paymentv1.GetPaymentRequest{
		OrderGroupId: groupID,
	})
	if pErr != nil {
		if status.Code(pErr) == codes.NotFound {
			return false, true, nil
		}
		return false, false, fmt.Errorf("lookup payment for incomplete checkout %s: %w", groupID, pErr)
	}
	pay := payResp.GetPayment()
	if pay == nil || strings.TrimSpace(pay.GetPaymentId()) == "" {
		return false, true, nil
	}

	if err := s.finalizeRecoveredCheckoutGroup(compCtx, groupID, pay, aggs); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (s *OrderService) reconcileObservedPaymentStatus(ctx context.Context, groupID string, pay *paymentv1.Payment) error {
	paymentID := strings.TrimSpace(pay.GetPaymentId())
	switch pay.GetStatus() {
	case paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED:
		return s.HandlePaymentResult(ctx, PaymentResultInput{
			CheckoutGroupID: groupID,
			PaymentID:       paymentID,
			Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
			Reason:          "Payment confirmed during checkout recovery",
			ActorID:         "order-checkout-recovery",
			ActorRole:       "system",
		})
	case paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED:
		return s.HandlePaymentResult(ctx, PaymentResultInput{
			CheckoutGroupID: groupID,
			PaymentID:       paymentID,
			Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_FAILED,
			Reason:          "Payment failed before checkout finalization",
			ActorID:         "order-checkout-recovery",
			ActorRole:       "system",
		})
	case paymentv1.PaymentStatus_PAYMENT_STATUS_EXPIRED:
		return s.HandlePaymentResult(ctx, PaymentResultInput{
			CheckoutGroupID: groupID,
			PaymentID:       paymentID,
			Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
			Reason:          "Payment expired before checkout finalization",
			ActorID:         "order-checkout-recovery",
			ActorRole:       "system",
		})
	case paymentv1.PaymentStatus_PAYMENT_STATUS_CANCELLED:
		return s.HandlePaymentResult(ctx, PaymentResultInput{
			CheckoutGroupID:       groupID,
			PaymentID:             paymentID,
			Status:                orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_FAILED,
			PaymentStatusOverride: "CANCELLED",
			Reason:                "Payment cancelled before checkout finalization",
			ActorID:               "order-checkout-recovery",
			ActorRole:             "system",
		})
	case paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED:
		return s.HandlePaymentResult(ctx, PaymentResultInput{
			CheckoutGroupID:       groupID,
			PaymentID:             paymentID,
			Status:                orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
			PaymentStatusOverride: "REFUNDED",
			Reason:                "Payment refunded before checkout finalization",
			ActorID:               "order-checkout-recovery",
			ActorRole:             "system",
		})
	default:
		return nil
	}
}

func (s *OrderService) finalizeRecoveredCheckoutGroup(ctx context.Context, groupID string, pay *paymentv1.Payment, aggs []store.OrderAggregate) error {
	paymentID := strings.TrimSpace(pay.GetPaymentId())
	paymentStatus := paymentStatusToString(pay.GetStatus(), "PENDING")
	qrURL := pay.GetQrUrl()
	qrCode := pay.GetQrCode()

	var paymentExpiresAt *time.Time
	hasPendingOrder := false
	hasConfirmedOrder := false
	for _, agg := range aggs {
		st := statemachine.StringToStatus(agg.Order.Status)
		if st == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			continue
		}
		if agg.Order.PaymentExpiresAt != nil {
			exp := *agg.Order.PaymentExpiresAt
			if paymentExpiresAt == nil || exp.Before(*paymentExpiresAt) {
				paymentExpiresAt = &exp
			}
		}
		if st == orderv1.OrderStatus_ORDER_STATUS_PENDING {
			hasPendingOrder = true
		}
		if st == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			hasConfirmedOrder = true
		}
	}
	if pay.GetExpiresAt() != nil {
		exp := pay.GetExpiresAt().AsTime().UTC()
		if paymentExpiresAt == nil || exp.Before(*paymentExpiresAt) {
			paymentExpiresAt = &exp
		}
	}

	if hasPendingOrder {
		if hErr := s.reconcileObservedPaymentStatus(ctx, groupID, pay); hErr != nil {
			return fmt.Errorf("reconcile payment status for %s: %w", groupID, hErr)
		}
	}
	if hasConfirmedOrder {
		if err := s.confirmGroupStockAndClear(ctx, groupID); err != nil {
			s.logger.WarnContext(ctx, "ConfirmStock during checkout recovery failed; watchdog will retry",
				"checkout_group_id", groupID, "error", err)
		}
	}

	var (
		finalGroup    db.CheckoutGroup
		finalAggs     []store.OrderAggregate
		builtSnapshot bool
	)
	resetFinalState := func() {
		finalGroup = db.CheckoutGroup{}
		finalAggs = nil
		builtSnapshot = false
	}
	if err := s.completeCheckoutGroupWithRetry(ctx, store.UpdateCheckoutPaymentParams{
		CheckoutGroupID:  groupID,
		PaymentID:        paymentID,
		PaymentStatus:    paymentStatus,
		QRURL:            qrURL,
		QRCode:           qrCode,
		PayURL:           pay.GetPayUrl(),
		PaymentExpiresAt: paymentExpiresAt,
		BuildSnapshot:    s.makeBuyerCheckoutSnapshotBuilder(nil, &finalGroup, &finalAggs, &builtSnapshot),
	}, resetFinalState); err != nil {
		return fmt.Errorf("finalize incomplete checkout group %s: %w", groupID, err)
	}

	s.notifyOutbox()
	return nil
}

// ScanAutoCancelUnconfirmedOrders cancels CONFIRMED orders past ORDER_SELLER_CONFIRM_HOURS
// using the standard cancel path with cancel_reason = "seller_confirm_timeout".
func (s *OrderService) ScanAutoCancelUnconfirmedOrders(ctx context.Context) (int, error) {
	cutoff := s.nowFn().Add(-s.sellerConfirmTimeout())
	orderIDs, err := s.store.FindConfirmedOrdersForAutoCancel(ctx, cutoff, 20)
	if err != nil {
		return 0, err
	}

	handled := 0
	for _, orderID := range orderIDs {
		agg, gErr := s.store.GetOrderWithDetails(ctx, orderID)
		if gErr != nil {
			if !errors.Is(gErr, store.ErrNotFound) {
				s.logger.WarnContext(ctx, "auto-cancel load order failed", "order_id", orderID, "error", gErr)
			}
			continue
		}
		if statemachine.StringToStatus(agg.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			continue
		}
		if _, cErr := s.cancelOrderInternal(ctx, agg, "order-auto-cancel-job", "system", "seller_confirm_timeout", cancelOrderOptions{
			expectedStatus:         orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
			requireConfirmedBefore: &cutoff,
		}); cErr != nil {
			if status.Code(cErr) == codes.FailedPrecondition {
				// Already confirmed/cancelled concurrently or shipment already picked up.
				continue
			}
			s.logger.WarnContext(ctx, "auto-cancel order failed, continuing batch", "order_id", orderID, "error", cErr)
			continue
		}
		handled++
	}
	return handled, nil
}

// ScanPendingOrphanShipmentCancels retries CancelShipment(FORWARD) for CANCELLED orders
// flagged with pending_shipment_cancel after a SellerShipOrder/CancelOrder race or crash.
func (s *OrderService) ScanPendingOrphanShipmentCancels(ctx context.Context) (int, error) {
	orderIDs, err := s.store.FindCancelledOrdersPendingShipmentCancel(ctx, 20)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, orderID := range orderIDs {
		if s.compensateOrphanForwardShipment(ctx, orderID, "cancel orphaned shipment for cancelled order") {
			handled++
		}
	}
	return handled, nil
}

func (s *OrderService) getForwardShipment(ctx context.Context, orderID string) (*shippingv1.GetShipmentResponse, error) {
	if s.clients.Shipping == nil || strings.TrimSpace(orderID) == "" {
		return nil, status.Error(codes.NotFound, "shipping client unavailable")
	}
	callCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	return s.clients.Shipping.GetShipment(callCtx, &shippingv1.GetShipmentRequest{
		OrderId:   orderID,
		Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD,
	})
}

// ScanStalePendingShipmentCancels reconciles active CONFIRMED/PREPARING orders whose
// pending_shipment_cancel flag has exceeded PendingShipmentCancelTTL (5m):
//   - If GetShipment(FORWARD) returns NotFound (or shipment is cancelled) -> conditionally clear flag by token.
//   - If GetShipment(FORWARD) returns a live shipment -> attach/sync tracking onto the order instead of dropping it.
func (s *OrderService) ScanStalePendingShipmentCancels(ctx context.Context) (int, error) {
	now := s.nowFn()
	cutoff := now.Add(-store.PendingShipmentCancelTTL)
	orderIDs, err := s.store.FindActiveOrdersWithStalePendingShipmentCancel(ctx, cutoff, 20)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, orderID := range orderIDs {
		agg, gErr := s.store.GetOrderWithDetails(ctx, orderID)
		if gErr != nil {
			continue
		}
		curStatus := statemachine.StringToStatus(agg.Order.Status)
		if (curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING) ||
			!agg.Order.PendingShipmentCancel {
			continue
		}
		staleToken := agg.Order.PendingShipmentCancelToken

		shResp, sErr := s.getForwardShipment(ctx, orderID)
		if sErr != nil {
			if status.Code(sErr) == codes.NotFound {
				if cleared, cErr := s.store.ClearStaleOrderPendingShipmentCancel(ctx, orderID, staleToken, cutoff); cErr == nil && cleared {
					handled++
				}
				continue
			}
			s.logger.WarnContext(ctx, "stale pending_shipment_cancel GetShipment failed, continuing batch",
				"order_id", orderID, "error", sErr)
			continue
		}
		sh := shResp.GetShipment()
		if sh == nil || sh.GetStatus() == shippingv1.ShipmentStatus_SHIPMENT_STATUS_CANCELLED || strings.TrimSpace(sh.GetTrackingCode()) == "" {
			if cleared, cErr := s.store.ClearStaleOrderPendingShipmentCancel(ctx, orderID, staleToken, cutoff); cErr == nil && cleared {
				handled++
			}
			continue
		}

		// Live forward shipment exists in shipping: sync tracking/label onto the order and clear the flag.
		carrierCode := fallbackStr(strings.TrimSpace(sh.GetCarrierCode()), agg.Order.CarrierCode)
		serviceCode := fallbackStr(strings.TrimSpace(sh.GetServiceCode()), agg.Order.ServiceCode)
		if _, tErr := s.store.TransitionOrderPreparedForShipment(ctx, store.ShipOrderParams{
			OrderID:               orderID,
			CarrierCode:           carrierCode,
			ServiceCode:           serviceCode,
			TrackingCode:          strings.TrimSpace(sh.GetTrackingCode()),
			LabelURL:              strings.TrimSpace(sh.GetLabelUrl()),
			ExpectedCreatingToken: staleToken,
			ActorID:               "order-shipment-sync-job",
			ActorRole:             "system",
			Note:                  "Synced live forward shipment after pending_shipment_cancel TTL expiry",
			Now:                   now,
		}); tErr != nil {
			if errors.Is(tErr, store.ErrOrphanShipmentNeedsCancel) {
				s.compensateOrphanForwardShipment(ctx, orderID, "cancel orphaned shipment discovered by stale sync watchdog")
			} else if !errors.Is(tErr, store.ErrStaleShipmentAttempt) {
				s.logger.WarnContext(ctx, "stale pending_shipment_cancel sync tracking failed",
					"order_id", orderID, "tracking_code", sh.GetTrackingCode(), "error", tErr)
			}
			continue
		}
		// If the synced shipment has already progressed past CREATED/PICKING, catch up order status too.
		if shipStatusStr := protoShipmentStatusToString(sh.GetStatus()); shipStatusStr != "" {
			_ = s.HandleShipmentUpdated(ctx, "", events.ShipmentUpdatedPayload{
				OrderID:      orderID,
				CarrierCode:  strings.TrimSpace(sh.GetCarrierCode()),
				ServiceCode:  strings.TrimSpace(sh.GetServiceCode()),
				TrackingCode: strings.TrimSpace(sh.GetTrackingCode()),
				LabelURL:     strings.TrimSpace(sh.GetLabelUrl()),
				Status:       shipStatusStr,
				Description:  "Synced live forward shipment status from shipping service",
			})
		}
		handled++
	}
	return handled, nil
}

// ScanPendingStockConfirms retries Product.ConfirmStock for checkout groups whose orders were confirmed
// (COD, 0-VND, or VietQR payment.succeeded) when the immediate ConfirmStock call failed or crashed (P0-2).
func (s *OrderService) ScanPendingStockConfirms(ctx context.Context) (int, error) {
	groupIDs, err := s.store.FindCheckoutGroupsPendingStockConfirm(ctx, 20)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, groupID := range groupIDs {
		aggs, lErr := s.store.ListOrdersByCheckoutGroup(ctx, groupID)
		if lErr != nil {
			continue
		}
		hasNonCancelled := false
		for _, a := range aggs {
			if statemachine.StringToStatus(a.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				hasNonCancelled = true
				break
			}
		}
		if !hasNonCancelled {
			// All sub-orders cancelled; ReleaseStock or cancel compensation watchdog owns the reservation.
			_ = s.store.ClearCheckoutGroupPendingStockConfirm(ctx, groupID)
			handled++
			continue
		}
		if err := s.confirmGroupStockAndClear(ctx, groupID); err != nil {
			s.logger.WarnContext(ctx, "retry ConfirmStock failed, continuing batch",
				"checkout_group_id", groupID, "error", err)
			continue
		}
		handled++
	}
	return handled, nil
}

// ScanPendingCancelCompensations retries post-commit cancel compensations (ReleaseStock, ConfirmStock+Restock,
// ReleasePromotion/Shop, CreateRefund, CancelPayment) for CANCELLED orders flagged with pending_cancel_compensation (P1-4).
func (s *OrderService) ScanPendingCancelCompensations(ctx context.Context) (int, error) {
	orderIDs, err := s.store.FindCancelledOrdersPendingCompensation(ctx, 20)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, orderID := range orderIDs {
		agg, gErr := s.store.GetOrderWithDetails(ctx, orderID)
		if gErr != nil {
			continue
		}
		if statemachine.StringToStatus(agg.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			_ = s.store.ClearOrderPendingCancelCompensation(ctx, orderID)
			continue
		}
		siblings, sErr := s.store.ListOrdersByCheckoutGroup(ctx, agg.Order.CheckoutGroupID)
		if sErr != nil {
			continue
		}
		hasActiveSibling := false
		for _, sib := range siblings {
			if sib.Order.ID != agg.Order.ID && statemachine.StringToStatus(sib.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				hasActiveSibling = true
				break
			}
		}
		prevOrd := agg.Order
		prevStatus := "CONFIRMED"
		for i := len(agg.Timeline) - 1; i >= 0; i-- {
			if statemachine.StringToStatus(agg.Timeline[i].ToStatus) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED && agg.Timeline[i].FromStatus != "" {
				prevStatus = agg.Timeline[i].FromStatus
				break
			}
		}
		prevOrd.Status = prevStatus
		reason := fallbackStr(agg.Order.CancelReason, "order cancelled")
		if err := s.compensateCancelledOrder(ctx, prevOrd, agg.Items, hasActiveSibling, reason); err != nil {
			s.logger.WarnContext(ctx, "retry cancel compensation failed, continuing batch",
				"order_id", orderID, "error", err)
			continue
		}
		if err := s.store.ClearOrderPendingCancelCompensation(ctx, orderID); err != nil {
			s.logger.WarnContext(ctx, "clear pending_cancel_compensation failed", "order_id", orderID, "error", err)
			continue
		}
		handled++
	}
	return handled, nil
}

// ScanPendingReturnCompensations retries post-commit compensations (ConfirmStock+Restock, ReleasePromotionShop,
// RecordCODRejection, CreateRefund) for SHIPPING -> RETURNED orders flagged with pending_return_compensation (P1-3).
func (s *OrderService) ScanPendingReturnCompensations(ctx context.Context) (int, error) {
	orderIDs, err := s.store.FindReturnedOrdersPendingCompensation(ctx, 20)
	if err != nil {
		return 0, err
	}
	handled := 0
	for _, orderID := range orderIDs {
		agg, gErr := s.store.GetOrderWithDetails(ctx, orderID)
		if gErr != nil {
			continue
		}
		if statemachine.StringToStatus(agg.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			_ = s.store.ClearOrderPendingReturnCompensation(ctx, orderID)
			continue
		}
		fromStatus := orderv1.OrderStatus_ORDER_STATUS_SHIPPING
		reason := "Order returned to seller"
		for i := len(agg.Timeline) - 1; i >= 0; i-- {
			if statemachine.StringToStatus(agg.Timeline[i].ToStatus) == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
				if agg.Timeline[i].FromStatus != "" {
					fromStatus = statemachine.StringToStatus(agg.Timeline[i].FromStatus)
				}
				if strings.TrimSpace(agg.Timeline[i].Note) != "" {
					reason = agg.Timeline[i].Note
				}
				break
			}
		}
		if err := s.compensateReturnedOrder(ctx, agg.Order, agg.Items, fromStatus, reason); err != nil {
			s.logger.WarnContext(ctx, "retry return compensation failed, continuing batch",
				"order_id", orderID, "error", err)
			continue
		}
		if err := s.store.ClearOrderPendingReturnCompensation(ctx, orderID); err != nil {
			s.logger.WarnContext(ctx, "clear pending_return_compensation failed", "order_id", orderID, "error", err)
			continue
		}
		handled++
	}
	return handled, nil
}

// ScanAutoCompleteDeliveredOrders transitions DELIVERED orders past ORDER_AUTO_COMPLETE_DAYS
// (with no return request) to COMPLETED through the shared BuyerConfirmReceived completion path.
func (s *OrderService) ScanAutoCompleteDeliveredOrders(ctx context.Context) (int, error) {
	cutoff := s.nowFn().Add(-s.autoCompleteDuration())
	orderIDs, err := s.store.FindDeliveredOrdersForAutoComplete(ctx, cutoff, 20)
	if err != nil {
		return 0, err
	}

	handled := 0
	for _, orderID := range orderIDs {
		agg, gErr := s.store.GetOrderWithDetails(ctx, orderID)
		if gErr != nil {
			if !errors.Is(gErr, store.ErrNotFound) {
				s.logger.WarnContext(ctx, "auto-complete load order failed", "order_id", orderID, "error", gErr)
			}
			continue
		}
		if statemachine.StringToStatus(agg.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
			continue
		}
		if _, cErr := s.completeDeliveredOrder(
			ctx,
			agg.Order,
			"order-auto-complete-job",
			"system",
			"Auto-completed after delivery confirmation window",
			true,
		); cErr != nil {
			if status.Code(cErr) == codes.FailedPrecondition {
				// Buyer already confirmed or requested return concurrently.
				continue
			}
			s.logger.WarnContext(ctx, "auto-complete order failed, continuing batch", "order_id", orderID, "error", cErr)
			continue
		}
		handled++
	}
	return handled, nil
}

// RunExpiryWatchdog periodically sweeps expired payments, stale checkout groups,
// auto-cancel/auto-complete jobs, and return request deadlines/shipments/refunds.
func (s *OrderService) RunExpiryWatchdog(ctx context.Context) {
	period := s.cfg.ExpiryWatchdogPeriod
	if period <= 0 {
		period = 10 * time.Second
	}
	timeout := s.cfg.ExpiryWatchdogTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	runSweep := func(name string, fn func(context.Context) (int, error)) {
		sweepCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if _, err := fn(sweepCtx); err != nil {
			s.logger.WarnContext(sweepCtx, name+" sweep failed", "error", err)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runSweep("payment expiry watchdog", s.ScanExpiredPendingOrders)
			runSweep("stale checkout group", s.ScanStaleCheckoutGroups)
			runSweep("incomplete checkout group", s.ScanIncompleteCheckoutGroups)
			runSweep("auto-cancel unconfirmed orders", s.ScanAutoCancelUnconfirmedOrders)
			runSweep("auto-complete delivered orders", s.ScanAutoCompleteDeliveredOrders)
			runSweep("return deadlines", s.ScanReturnDeadlines)
			runSweep("return pending shipments", s.ScanReturnPendingShipments)
			runSweep("refunding returns", s.ScanRefundingReturns)
			runSweep("orphan shipment cancels", s.ScanPendingOrphanShipmentCancels)
			runSweep("stale pending shipment cancels", s.ScanStalePendingShipmentCancels)
			runSweep("pending stock confirms", s.ScanPendingStockConfirms)
			runSweep("pending cancel compensations", s.ScanPendingCancelCompensations)
			runSweep("pending return compensations", s.ScanPendingReturnCompensations)
			runSweep("pending address changes", s.ScanPendingAddressChanges)
		}
	}
}
