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
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
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

	groupID := outcome.CheckoutGroupID
	if outcome.NeedsDownstream && groupID != "" {
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

// HandlePaymentRefunded records a payment.refunded event idempotently on the order timeline.
func (s *OrderService) HandlePaymentRefunded(ctx context.Context, eventID string, payload events.PaymentRefundedPayload) error {
	_, err := s.store.RecordPaymentRefunded(ctx, store.PaymentRefundedParams{
		EventID:   eventID,
		EventType: events.SubjectPaymentRefunded,
		PaymentID: payload.PaymentID,
		OrderID:   payload.OrderID,
		RefundID:  payload.RefundID,
		AmountVND: payload.AmountVND,
		Reason:    payload.Reason,
	})
	return err
}

// HandleShipmentUpdated processes shipment.updated events idempotently, updating timeline and state.
func (s *OrderService) HandleShipmentUpdated(ctx context.Context, eventID string, payload events.ShipmentUpdatedPayload) error {
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

	traceID, traceparent := traceMetaFromContext(ctx)
	outcome, err := s.store.ApplyShipmentUpdate(ctx, store.ShipmentUpdateParams{
		EventID:           eventID,
		EventType:         events.SubjectShipmentUpdated,
		SkipMarkProcessed: isReturned && eventID != "",
		OrderID:           payload.OrderID,
		ShipmentStatus:    payload.Status,
		TrackingCode:      payload.TrackingCode,
		Description:       payload.Description,
		TraceID:           traceID,
		Traceparent:       traceparent,
	})
	if err != nil {
		return err
	}
	if outcome.AlreadyProcessed {
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

	if outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
		s.notifyOutbox()
	}

	if (outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_RETURNED || outcome.AlreadyReturned) && outcome.Order != nil {
		if outcome.TransitionedTo == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			s.notifyOutbox()
		}

		ord := outcome.Order.Order
		compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer cancel()

		// Review-A P1-7: return errors from all idempotent compensations before marking processed_events
		// so transient downstream failures trigger NATS redelivery/DLQ instead of silent loss.
		if err := s.restockOrderItemsErr(compCtx, ord.ID, outcome.Order.Items); err != nil {
			return fmt.Errorf("restock returned order %s: %w", ord.ID, err)
		}

		// R5: Release shop-scoped promotions (coins, shop voucher, flash slots) for returned sub-order.
		if err := s.releasePromotionShopErr(compCtx, ord.CheckoutGroupID, ord.ShopID, "order_returned"); err != nil {
			return fmt.Errorf("release shop promotion for returned order %s: %w", ord.ID, err)
		}

		// R6 (ORD-B6): COD delivery rejection (SHIPPING -> RETURNED) increments buyer's cod_rejected_count.
		if outcome.TransitionedFrom == orderv1.OrderStatus_ORDER_STATUS_SHIPPING &&
			statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD &&
			s.clients.User != nil {
			rejReason := strings.TrimSpace(payload.Description)
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
		wasPaidOnline := statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
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
	}

	if isReturned && eventID != "" {
		if err := s.store.MarkEventProcessed(ctx, eventID, events.SubjectShipmentUpdated); err != nil {
			return fmt.Errorf("mark shipment returned event processed: %w", err)
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
			if pay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_REFUNDED {
				// Review2-O P1-2: payment was already 100% refunded before expiry sweep;
				// cancel the pending order and release reservations without downgrading payment_status to EXPIRED.
				return s.HandlePaymentResult(sysCtx, PaymentResultInput{
					CheckoutGroupID:       cand.CheckoutGroupID,
					PaymentID:             pay.GetPaymentId(),
					Status:                orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED,
					PaymentStatusOverride: "REFUNDED",
					Reason:                "Payment already refunded before order confirmation",
					ActorID:               "order-expiry-watchdog",
					ActorRole:             "system",
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

	if err := s.store.FailCheckoutGroup(compCtx, groupID, "STALE_CHECKOUT_EXPIRED"); err != nil {
		return fmt.Errorf("fail stale checkout group %s: %w", groupID, err)
	}
	return nil
}

// RunExpiryWatchdog periodically scans and resolves expired PENDING VietQR orders
// and stale IN_PROGRESS checkout groups until ctx is cancelled.
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
			if _, err := s.ScanStaleCheckoutGroups(sweepCtx); err != nil {
				s.logger.WarnContext(sweepCtx, "stale checkout group sweep failed", "error", err)
			}
			cancel()
		}
	}
}
