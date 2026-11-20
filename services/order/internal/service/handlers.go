package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PaymentResultInput captures normalized payment event / RPC data.
type PaymentResultInput struct {
	EventID         string
	EventType       string
	CheckoutGroupID string
	OrderID         string
	PaymentID       string
	Status          orderv1.PaymentResultStatus
	Reason          string
	ActorID         string
	ActorRole       string
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

	groupID := strings.TrimSpace(in.CheckoutGroupID)
	if groupID == "" && strings.TrimSpace(in.OrderID) != "" {
		if agg, err := s.store.GetOrderWithDetails(ctx, in.OrderID); err == nil && agg != nil {
			groupID = agg.Order.CheckoutGroupID
		} else if grp, gErr := s.store.GetCheckoutGroupByID(ctx, in.OrderID); gErr == nil && grp != nil {
			groupID = grp.ID
		}
	}

	hasPending := false
	if groupID != "" {
		aggs, err := s.store.ListOrdersByCheckoutGroup(ctx, groupID)
		if err != nil {
			return err
		}
		for _, a := range aggs {
			if statemachine.StringToStatus(a.Order.Status) == orderv1.OrderStatus_ORDER_STATUS_PENDING {
				hasPending = true
				break
			}
		}
	}

	// Execute idempotent downstream calls BEFORE committing processed_events so transient failures
	// trigger NATS redelivery and guarantee stock/voucher/coins compensation completes!
	if hasPending && groupID != "" {
		sysCtx := clients.WithInternalAdminContext(ctx)
		switch in.Status {
		case orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED:
			if s.clients.Product != nil {
				if err := clients.RetryIdempotent(sysCtx, 3, func(c context.Context) error {
					_, cErr := s.clients.Product.ConfirmStock(c, &productv1.ConfirmStockRequest{
						OrderId: groupID,
					})
					return cErr
				}); err != nil {
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
		}
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	outcome, err := s.store.TransitionOrdersPaymentResult(ctx, store.PaymentResultTransitionParams{
		EventID:         in.EventID,
		EventType:       in.EventType,
		CheckoutGroupID: groupID,
		OrderID:         in.OrderID,
		PaymentID:       in.PaymentID,
		ResultStatus:    in.Status,
		Reason:          in.Reason,
		ActorID:         fallbackStr(in.ActorID, "payment-service"),
		ActorRole:       fallbackStr(in.ActorRole, "system"),
		TraceID:         traceID,
		Traceparent:     traceparent,
	})
	if err != nil {
		return err
	}

	if len(outcome.Transitioned) > 0 {
		s.notifyOutbox()
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

// HandlePaymentRefunded records a payment.refunded event idempotently on the order timeline.
func (s *OrderService) HandlePaymentRefunded(ctx context.Context, eventID string, payload events.PaymentRefundedPayload) error {
	_, err := s.store.RecordPaymentRefunded(ctx, store.PaymentRefundedParams{
		EventID:   eventID,
		EventType: events.SubjectPaymentRefunded,
		OrderID:   payload.OrderID,
		RefundID:  payload.RefundID,
		AmountVND: payload.AmountVND,
		Reason:    payload.Reason,
	})
	return err
}

// HandleShipmentUpdated processes shipment.updated events idempotently, updating timeline and state.
func (s *OrderService) HandleShipmentUpdated(ctx context.Context, eventID string, payload events.ShipmentUpdatedPayload) error {
	traceID, traceparent := traceMetaFromContext(ctx)
	outcome, err := s.store.ApplyShipmentUpdate(ctx, store.ShipmentUpdateParams{
		EventID:        eventID,
		EventType:      events.SubjectShipmentUpdated,
		OrderID:        payload.OrderID,
		ShipmentStatus: payload.Status,
		TrackingCode:   payload.TrackingCode,
		Description:    payload.Description,
		TraceID:        traceID,
		Traceparent:    traceparent,
	})
	if err != nil {
		return err
	}
	if outcome.AlreadyProcessed || outcome.NotFound {
		return nil
	}

	if outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
		s.notifyOutbox()
	}

	if outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_RETURNED && outcome.Order != nil {
		ord := outcome.Order.Order
		compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer cancel()

		// Restock returned items.
		s.restockOrderItems(compCtx, ord.ShopID, outcome.Order.Items)

		// If order was paid online, initiate refund idempotently.
		wasPaidOnline := statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
			strings.EqualFold(ord.PaymentStatus, "SUCCEEDED")
		if wasPaidOnline && ord.PaymentID != "" && s.clients.Payment != nil {
			if rErr := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
				_, err := s.clients.Payment.CreateRefund(c, &paymentv1.CreateRefundRequest{
					PaymentId:      ord.PaymentID,
					OrderId:        ord.ID,
					AmountVnd:      ord.TotalPayableVND,
					Reason:         "Order returned to seller",
					IdempotencyKey: "order-return-refund:" + ord.ID,
				})
				return err
			}); rErr != nil {
				s.logger.ErrorContext(ctx, "failed to trigger refund for returned order",
					"order_id", ord.ID, "payment_id", ord.PaymentID, "error", rErr)
			}
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

	// UC-PAY06: Always check PaymentService before cancelling an expired PENDING order!
	if s.clients.Payment != nil {
		payResp, err := s.clients.Payment.GetPayment(sysCtx, &paymentv1.GetPaymentRequest{
			PaymentId:    cand.PaymentID,
			OrderGroupId: cand.CheckoutGroupID,
		})
		if err != nil {
			// If payment gateway/service is unreachable, do NOT cancel yet; retry next tick.
			if status.Code(err) != codes.NotFound {
				return fmt.Errorf("check payment status before expiry cancel: %w", err)
			}
		} else if pay := payResp.GetPayment(); pay != nil {
			if pay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED {
				// Late payment succeeded before cancel — confirm order instead of cancelling!
				return s.HandlePaymentResult(sysCtx, PaymentResultInput{
					CheckoutGroupID: cand.CheckoutGroupID,
					PaymentID:       pay.GetPaymentId(),
					Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED,
					Reason:          "Payment confirmed by expiry watchdog",
					ActorID:         "order-expiry-watchdog",
					ActorRole:       "system",
				})
			}
			if pay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
				_, _ = s.clients.Payment.CancelPayment(sysCtx, &paymentv1.CancelPaymentRequest{
					PaymentId:    pay.GetPaymentId(),
					OrderGroupId: cand.CheckoutGroupID,
					Reason:       "Payment expired",
				})
			}
		}
	}

	return s.HandlePaymentResult(sysCtx, PaymentResultInput{
		CheckoutGroupID: cand.CheckoutGroupID,
		PaymentID:       cand.PaymentID,
		Status:          orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
		Reason:          "Payment expired after timeout",
		ActorID:         "order-expiry-watchdog",
		ActorRole:       "system",
	})
}

// RunExpiryWatchdog periodically scans and resolves expired PENDING VietQR orders until ctx is cancelled.
func (s *OrderService) RunExpiryWatchdog(ctx context.Context) {
	period := s.cfg.ExpiryWatchdogPeriod
	if period <= 0 {
		period = 10 * time.Second
	}
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweepCtx, cancel := context.WithTimeout(ctx, s.cfg.ExpiryWatchdogTimeout)
			if _, err := s.ScanExpiredPendingOrders(sweepCtx); err != nil {
				s.logger.WarnContext(sweepCtx, "payment expiry watchdog sweep failed", "error", err)
			}
			cancel()
		}
	}
}
