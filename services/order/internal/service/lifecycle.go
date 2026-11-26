package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetOrder fetches a single order after verifying object-level RBAC (buyer owner, shop owner, or admin).
func (s *OrderService) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canViewOrder(caller, agg.Order) {
		return nil, status.Error(codes.PermissionDenied, "access denied to this order")
	}

	return &orderv1.GetOrderResponse{
		Order: aggregateToProtoForCaller(*agg, caller),
	}, nil
}

// GetOrderTimeline fetches an order's timeline events with object-level RBAC.
func (s *OrderService) GetOrderTimeline(ctx context.Context, req *orderv1.GetOrderTimelineRequest) (*orderv1.GetOrderTimelineResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canViewOrder(caller, agg.Order) {
		return nil, status.Error(codes.PermissionDenied, "access denied to order timeline")
	}

	out := make([]*orderv1.OrderTimelineEvent, 0, len(agg.Timeline))
	for _, tl := range agg.Timeline {
		out = append(out, timelineToProto(tl))
	}
	return &orderv1.GetOrderTimelineResponse{Events: out}, nil
}

// ListOrders returns paginated orders strictly scoped to buyer, seller shop, or admin filters.
func (s *OrderService) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	scope := strings.ToLower(strings.TrimSpace(req.GetRoleScope()))
	canReadAdminOrders := caller.HasCapability(auth.CapOrdersRead)

	var buyerFilter, shopFilter string

	switch scope {
	case "admin":
		if !canReadAdminOrders {
			return nil, status.Error(codes.PermissionDenied, "orders.read capability required to list all orders")
		}
		buyerFilter = strings.TrimSpace(req.GetBuyerId())
		shopFilter = strings.TrimSpace(req.GetShopId())
	case "seller":
		if canReadAdminOrders {
			shopFilter = strings.TrimSpace(req.GetShopId())
			buyerFilter = strings.TrimSpace(req.GetBuyerId())
		} else {
			if caller.Role != "seller" || caller.ShopID == "" {
				return nil, status.Error(codes.PermissionDenied, "seller shop identity is required")
			}
			if req.GetShopId() != "" && req.GetShopId() != caller.ShopID {
				return nil, status.Error(codes.PermissionDenied, "cannot list orders for another shop")
			}
			shopFilter = caller.ShopID
		}
	default:
		if canReadAdminOrders && req.GetBuyerId() == "" && req.GetShopId() != "" {
			shopFilter = strings.TrimSpace(req.GetShopId())
		} else if canReadAdminOrders && scope == "" && req.GetBuyerId() == "" {
			// Authorized staff with no explicit filter sees all orders.
			buyerFilter = ""
			shopFilter = ""
		} else {
			if caller.UserID == "" {
				return nil, status.Error(codes.Unauthenticated, "buyer identity is required")
			}
			if !canReadAdminOrders && req.GetBuyerId() != "" && req.GetBuyerId() != caller.UserID {
				return nil, status.Error(codes.PermissionDenied, "cannot list orders for another buyer")
			}
			buyerFilter = caller.UserID
			if canReadAdminOrders && req.GetBuyerId() != "" {
				buyerFilter = strings.TrimSpace(req.GetBuyerId())
			}
		}
	}

	var statusFilter string
	if req.GetStatus() != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED {
		statusFilter = statemachine.StatusToString(req.GetStatus())
		if statusFilter == statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED) {
			return nil, status.Error(codes.InvalidArgument, "status contains an unknown order status")
		}
	}

	var statusesFilter []string
	if rawStatuses := req.GetStatuses(); len(rawStatuses) > 0 {
		seen := make(map[string]struct{}, len(rawStatuses))
		statusesFilter = make([]string, 0, len(rawStatuses))
		for _, st := range rawStatuses {
			sStr := statemachine.StatusToString(st)
			if sStr == statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED) {
				return nil, status.Error(codes.InvalidArgument, "statuses contains an unknown order status")
			}
			if _, dup := seen[sStr]; dup {
				continue
			}
			seen[sStr] = struct{}{}
			statusesFilter = append(statusesFilter, sStr)
		}
	}

	var paymentMethodFilter string
	if req.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
		paymentMethodFilter = statemachine.PaymentMethodToString(req.GetPaymentMethod())
		if paymentMethodFilter == statemachine.PaymentMethodToString(orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED) {
			return nil, status.Error(codes.InvalidArgument, "payment_method contains an unknown payment method")
		}
	}

	var dueCutoff *time.Time
	if req.GetDueBefore() != nil {
		if !req.GetDueBefore().IsValid() {
			return nil, status.Error(codes.InvalidArgument, "due_before timestamp is invalid")
		}
		cutoff := req.GetDueBefore().AsTime().UTC().Add(-s.sellerConfirmTimeout())
		dueCutoff = &cutoff
	}
	salesChannelFilter, err := normalizeSalesChannelFilter(req.GetSalesChannel())
	if err != nil {
		return nil, err
	}
	storefrontFilter := strings.TrimSpace(req.GetStorefrontShopId())
	if _, perr := uuid.Parse(storefrontFilter); storefrontFilter != "" && perr != nil {
		return nil, status.Error(codes.InvalidArgument, "storefront_shop_id must be a UUID")
	}

	page := req.GetPage()
	if page < 1 {
		page = 1
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset64 := int64(page-1) * int64(pageSize)
	if offset64 > math.MaxInt32 {
		return nil, status.Error(codes.InvalidArgument, "page offset exceeds supported range")
	}
	offset := int32(offset64)

	listRes, err := s.store.ListOrdersWithFilter(ctx, store.OrderListFilter{
		BuyerID:             buyerFilter,
		ShopID:              shopFilter,
		Status:              statusFilter,
		Statuses:            statusesFilter,
		SalesChannel:        salesChannelFilter,
		StorefrontShopID:    storefrontFilter,
		CarrierCode:         strings.TrimSpace(req.GetCarrierCode()),
		PaymentMethod:       paymentMethodFilter,
		Keyword:             strings.TrimSpace(req.GetKeyword()),
		DueCutoff:           dueCutoff,
		Limit:               pageSize,
		Offset:              offset,
		IncludeStatusCounts: req.GetIncludeStatusCounts(),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	orders := make([]*orderv1.Order, 0, len(listRes.Orders))
	for _, agg := range listRes.Orders {
		orders = append(orders, aggregateToProtoForCaller(agg, caller))
	}

	var statusCounts []*orderv1.OrderStatusCount
	if req.GetIncludeStatusCounts() {
		statusCounts = buildOrderedStatusCounts(listRes.StatusCounts)
	}

	return &orderv1.ListOrdersResponse{
		Orders:       orders,
		Total:        listRes.Total,
		StatusCounts: statusCounts,
	}, nil
}

func buildOrderedStatusCounts(counts map[string]int64) []*orderv1.OrderStatusCount {
	order := []orderv1.OrderStatus{
		orderv1.OrderStatus_ORDER_STATUS_PENDING,
		orderv1.OrderStatus_ORDER_STATUS_CONFIRMED,
		orderv1.OrderStatus_ORDER_STATUS_PREPARING,
		orderv1.OrderStatus_ORDER_STATUS_SHIPPING,
		orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED,
		orderv1.OrderStatus_ORDER_STATUS_RETURNED,
	}
	out := make([]*orderv1.OrderStatusCount, 0, len(order))
	for _, st := range order {
		if c := counts[statemachine.StatusToString(st)]; c > 0 {
			out = append(out, &orderv1.OrderStatusCount{
				Status: st,
				Count:  c,
			})
		}
	}
	return out
}

// CancelOrder cancels an order in PENDING, CONFIRMED, or PREPARING state and compensates downstream services.
func (s *OrderService) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canCancelOrder(caller, agg.Order) {
		return nil, status.Error(codes.PermissionDenied, "access denied to cancel this order")
	}

	prevStatus := statemachine.StringToStatus(agg.Order.Status)
	if err := statemachine.ValidateTransition(prevStatus, orderv1.OrderStatus_ORDER_STATUS_CANCELLED); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		reason = "Cancelled by " + fallbackStr(caller.Role, "user")
	}

	cancelledAgg, err := s.cancelOrderInternal(ctx, agg, caller.UserID, fallbackStr(caller.Role, "buyer"), reason, cancelOrderOptions{})
	if err != nil {
		return nil, err
	}

	return &orderv1.CancelOrderResponse{
		Order: aggregateToProtoForCaller(*cancelledAgg, caller),
	}, nil
}

type cancelOrderOptions struct {
	expectedStatus         orderv1.OrderStatus
	requireConfirmedBefore *time.Time
}

func (s *OrderService) cancelForwardShipment(ctx context.Context, orderID, reason string) (cancelledTracking string, notFound bool, err error) {
	if s.clients.Shipping == nil {
		return "", false, nil
	}
	svcCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	return s.cancelForwardShipmentWithCtx(svcCtx, orderID, reason)
}

func (s *OrderService) cancelForwardShipmentWithCtx(svcCtx context.Context, orderID, reason string) (cancelledTracking string, notFound bool, err error) {
	if s.clients.Shipping == nil {
		return "", false, nil
	}
	var respOut *shippingv1.CancelShipmentResponse
	err = clients.RetryIdempotent(svcCtx, 3, func(c context.Context) error {
		resp, cErr := s.clients.Shipping.CancelShipment(c, &shippingv1.CancelShipmentRequest{
			OrderId:   orderID,
			Reason:    reason,
			Direction: shippingv1.ShipmentDirection_SHIPMENT_DIRECTION_FORWARD,
		})
		if cErr != nil {
			return cErr
		}
		respOut = resp
		return nil
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", true, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(respOut.GetShipment().GetTrackingCode()), false, nil
}

// compensateOrphanForwardShipment cancels a forward shipment orphaned by a concurrent order cancel
// and clears the durable pending_shipment_cancel flag once resolved.
// On FailedPrecondition (carrier already picked up), records a durable manual-review flag + timeline
// event for admin visibility instead of silently clearing the flag; transient errors leave the flag set
// so ScanPendingOrphanShipmentCancels retries on subsequent sweeps.
func (s *OrderService) compensateOrphanForwardShipment(ctx context.Context, orderID, reason string) bool {
	_, _, cErr := s.cancelForwardShipment(ctx, orderID, reason)
	clearCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	if cErr != nil {
		if status.Code(cErr) == codes.FailedPrecondition {
			note := fmt.Sprintf("MANUAL_REVIEW_REQUIRED: orphan forward shipment could not be cancelled because carrier already picked up (%s: %v)", reason, cErr)
			s.logger.ErrorContext(ctx, "orphan forward shipment could not be cancelled (already picked up); flagging orphan_shipment_needs_manual_review for admin",
				"order_id", orderID, "orphan_shipment_needs_manual_review", true, "reason", reason, "error", cErr)
			if mErr := s.store.MarkOrderOrphanShipmentManualReview(clearCtx, orderID, note); mErr != nil {
				s.logger.WarnContext(ctx, "failed to record manual review for orphan shipment; watchdog will retry",
					"order_id", orderID, "error", mErr)
				return false
			}
			return true
		}
		s.logger.WarnContext(ctx, "orphan forward shipment cancel failed; watchdog will retry",
			"order_id", orderID, "error", cErr)
		return false
	}
	if err := s.store.ClearOrderPendingShipmentCancel(clearCtx, orderID); err != nil {
		s.logger.WarnContext(ctx, "failed to clear pending_shipment_cancel flag; watchdog will retry",
			"order_id", orderID, "error", err)
		return false
	}
	return true
}

const maxCancelShipmentAttempts = 3

func (s *OrderService) cancelOrderInternal(ctx context.Context, agg *store.OrderAggregate, actorID, actorRole, reason string, opts cancelOrderOptions) (*store.OrderAggregate, error) {
	var (
		cancelledTracking  string
		knownTracking      = strings.TrimSpace(agg.Order.TrackingCode)
		knownQueued        = store.IsOrderShipmentQueued(agg.Order)
		knownQueuedAt      *time.Time
		lastCancelNotFound bool
		hasCalledCancel    bool
		cancelledQueuedJob bool
	)
	if knownQueued {
		t := agg.Order.UpdatedAt
		knownQueuedAt = &t
	}
	if knownTracking != "" || agg.Order.PendingShipmentCancel || knownQueued {
		trk, nf, err := s.cancelForwardShipment(ctx, agg.Order.ID, reason)
		if err != nil {
			return nil, err
		}
		cancelledTracking = trk
		lastCancelNotFound = nf
		hasCalledCancel = true
		cancelledQueuedJob = true
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	runTransition := func(requireNoTracking bool, canTrk, knownTrk string, kQueued bool, kQueuedAt *time.Time, lastNF, clearPending bool) (*store.OrderTransitionResult, error) {
		return s.store.TransitionOrderStatus(ctx, store.SingleOrderTransitionParams{
			OrderID:                    agg.Order.ID,
			ExpectedStatus:             opts.expectedStatus,
			RequireConfirmedBefore:     opts.requireConfirmedBefore,
			TargetStatus:               orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
			CancelReason:               reason,
			PaymentStatus:              "", // Computed atomically inside FOR UPDATE TX (review-A P0-4, P2-10, P3-12).
			ActorID:                    actorID,
			ActorRole:                  actorRole,
			Note:                       reason,
			CancelGroupPend:            true,
			RequireNoTracking:          requireNoTracking,
			VerifyShipmentCancel:       s.clients.Shipping != nil,
			CancelledTrackingCode:      canTrk,
			KnownTrackingBeforeCancel:  knownTrk,
			KnownShipmentQueued:        kQueued,
			KnownQueuedUpdatedAt:       kQueuedAt,
			LastCancelNotFound:         lastNF,
			ClearPendingShipmentCancel: clearPending,
			Now:                        s.nowFn(),
			BuildOutboxFn: func(ord db.Order) []db.InsertOutboxEventParams {
				ordPaid := statemachine.IsOnlineMethod(ord.PaymentMethod) &&
					strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") &&
					ord.TotalPayableVND > 0
				payload, _ := json.Marshal(events.OrderCancelledPayload{
					OrderID:          ord.ID,
					OrderCode:        ord.OrderCode,
					BuyerID:          ord.BuyerID,
					ShopID:           ord.ShopID,
					StorefrontShopID: ord.StorefrontShopID,
					Reason:           reason,
					WasPaid:          ordPaid,
				})
				return []db.InsertOutboxEventParams{
					{
						ID:          uuid.NewString(),
						EventType:   events.SubjectOrderCancelled,
						TraceID:     traceID,
						Traceparent: traceparent,
						PayloadJSON: payload,
					},
				}
			},
		})
	}

	var res *store.OrderTransitionResult
	var err error
	for attempt := 0; attempt < maxCancelShipmentAttempts; attempt++ {
		res, err = runTransition(!hasCalledCancel, cancelledTracking, knownTracking, knownQueued, knownQueuedAt, lastCancelNotFound, hasCalledCancel && cancelledTracking != "")
		if !errors.Is(err, store.ErrOrderHasTracking) {
			break
		}
		if attempt == maxCancelShipmentAttempts-1 {
			return nil, status.Error(codes.FailedPrecondition, "vận đơn đang được tạo, thử lại")
		}
		// Refresh observed tracking/queued state before retrying CancelShipment.
		if latest, gErr := s.store.GetOrderWithDetails(ctx, agg.Order.ID); gErr == nil {
			knownTracking = strings.TrimSpace(latest.Order.TrackingCode)
			knownQueued = store.IsOrderShipmentQueued(latest.Order)
			if knownQueued {
				t := latest.Order.UpdatedAt
				knownQueuedAt = &t
			} else {
				knownQueuedAt = nil
			}
		}
		// Concurrent SellerShipOrder is creating, queued, or attached a tracking code before our FOR UPDATE lock.
		trk, nf, cErr := s.cancelForwardShipment(ctx, agg.Order.ID, reason)
		if cErr != nil {
			return nil, cErr
		}
		cancelledTracking = trk
		lastCancelNotFound = nf
		hasCalledCancel = true
		cancelledQueuedJob = true
	}
	if err != nil {
		// E18: Shipping.CancelShipment already cancelled any pending async retry job in shipping,
		// but transitioning the order to CANCELLED failed (order stays alive). Flip any lingering
		// QUEUED status (with empty tracking and no active creating flag) to FAILED so the order is
		// never left stuck QUEUED without a backing job and the seller can retry SellerShipOrder.
		if cancelledQueuedJob {
			cleanupCtx, cleanupCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
			defer cleanupCancel()
			if _, mErr := s.store.MarkOrderShipmentJobFailedIfQueued(cleanupCtx, agg.Order.ID); mErr != nil {
				s.logger.WarnContext(ctx, "failed to clear aborted-cancel QUEUED shipment job status to FAILED",
					"order_id", agg.Order.ID, "error", mErr)
			}
		}
		return nil, mapStoreError(err)
	}

	// Execute downstream compensations from the locked pre-transition snapshot (review-A P0-4, P1-4).
	// pending_cancel_compensation was set atomically in TransitionOrderStatus; cleared only once all succeed.
	if compErr := s.compensateCancelledOrder(ctx, res.PrevOrder, res.Order.Items, res.HasActiveSibling, reason); compErr != nil {
		s.logger.WarnContext(ctx, "post-commit cancel compensation incomplete; watchdog will retry",
			"order_id", res.PrevOrder.ID, "error", compErr)
	} else {
		clearCtx, clearCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer clearCancel()
		for _, tr := range res.Transitioned {
			_ = s.store.ClearOrderPendingCancelCompensation(clearCtx, tr.ID)
		}
	}
	s.notifyOutbox()
	return res.Order, nil
}

func (s *OrderService) compensateCancelledOrder(
	ctx context.Context,
	prevOrd db.Order,
	items []db.OrderItem,
	hasActiveSibling bool,
	reason string,
) error {
	groupID := prevOrd.CheckoutGroupID
	prevStatus := statemachine.StringToStatus(prevOrd.Status)
	wasPaid := statemachine.IsOnlineMethod(prevOrd.PaymentMethod) &&
		strings.EqualFold(prevOrd.PaymentStatus, "SUCCEEDED") &&
		prevOrd.TotalPayableVND > 0

	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	cancelPaymentIgnoreTerminal := func() error {
		// P1-1: Never call CancelPayment for standalone 0-VND orders (which start CONFIRMED/SUCCEEDED)
		// or already-refunded orders.
		// P1 N5: Allow CancelPayment by OrderGroupId even when prevOrd.PaymentID is empty.
		// P2 M7: When prevStatus == PENDING, the group grand total is > 0, so a 0-VND sub-order in a
		// multi-shop VietQR group MUST still cancel the group payment.
		alreadyRefunded := strings.EqualFold(prevOrd.PaymentStatus, "REFUNDED") ||
			(prevOrd.TotalPayableVND > 0 && prevOrd.RefundedVND >= prevOrd.TotalPayableVND)
		skipZeroPayable := prevStatus != orderv1.OrderStatus_ORDER_STATUS_PENDING && prevOrd.TotalPayableVND <= 0
		if (prevOrd.PaymentID == "" && groupID == "") || s.clients.Payment == nil || skipZeroPayable || alreadyRefunded {
			return nil
		}
		return clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
			_, cErr := s.clients.Payment.CancelPayment(c, &paymentv1.CancelPaymentRequest{
				PaymentId:    prevOrd.PaymentID,
				OrderGroupId: groupID,
				Reason:       reason,
			})
			// P1-1: Rust payment maps PaymentError::Conflict (non-pending payment) to codes.AlreadyExists.
			if code := status.Code(cErr); code == codes.NotFound || code == codes.FailedPrecondition || code == codes.AlreadyExists {
				return nil
			}
			return cErr
		})
	}

	if prevStatus == orderv1.OrderStatus_ORDER_STATUS_PENDING {
		if _, err := s.releaseStockErr(compCtx, groupID); err != nil {
			return fmt.Errorf("release stock for cancelled pending order %s: %w", prevOrd.ID, err)
		}
		if err := s.releasePromotionErr(compCtx, groupID); err != nil {
			return fmt.Errorf("release promotion for cancelled pending order %s: %w", prevOrd.ID, err)
		}
		if err := cancelPaymentIgnoreTerminal(); err != nil {
			return fmt.Errorf("cancel payment for cancelled pending order %s: %w", prevOrd.ID, err)
		}
		return nil
	}

	// Order was CONFIRMED or PREPARING.
	if !hasActiveSibling {
		if err := s.releasePromotionErr(compCtx, groupID); err != nil {
			return fmt.Errorf("release promotion for cancelled order %s: %w", prevOrd.ID, err)
		}
		if s.clients.Product != nil {
			released, rErr := s.releaseStockErr(compCtx, groupID)
			if rErr != nil {
				// Review2-O P1-4a: do NOT fall back to RestockOrderItems on transient ReleaseStock errors.
				return fmt.Errorf("ReleaseStock for cancelled order %s: %w", prevOrd.ID, rErr)
			}
			if released {
				_ = s.store.ClearCheckoutGroupPendingStockConfirm(compCtx, groupID)
			} else {
				if err := s.restockOrderItemsWithConfirmErr(compCtx, groupID, prevOrd.ID, items); err != nil {
					return fmt.Errorf("restock cancelled order %s: %w", prevOrd.ID, err)
				}
			}
		}
	} else {
		// Active sibling exists: release only this sub-order's shop promotions and confirm+restock its items.
		if err := s.releasePromotionShopErr(compCtx, groupID, prevOrd.ShopID, prevOrd.ID, reason); err != nil {
			return fmt.Errorf("release shop promotion for cancelled order %s: %w", prevOrd.ID, err)
		}
		if err := s.restockOrderItemsWithConfirmErr(compCtx, groupID, prevOrd.ID, items); err != nil {
			return fmt.Errorf("confirm+restock cancelled sub-order %s: %w", prevOrd.ID, err)
		}
	}

	if s.clients.Payment != nil {
		if wasPaid && prevOrd.PaymentID != "" {
			// Review-A P0-1: refund only the remaining unrefunded balance.
			remaining := prevOrd.TotalPayableVND - prevOrd.RefundedVND
			if remaining > 0 {
				if err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
					_, rErr := s.clients.Payment.CreateRefund(c, &paymentv1.CreateRefundRequest{
						PaymentId:      prevOrd.PaymentID,
						OrderId:        prevOrd.ID,
						AmountVnd:      remaining,
						Reason:         reason,
						IdempotencyKey: "order-cancel-refund:" + prevOrd.ID,
					})
					return rErr
				}); err != nil {
					return fmt.Errorf("create refund on cancelled paid order %s: %w", prevOrd.ID, err)
				}
			}
		} else if !wasPaid && !hasActiveSibling {
			if err := cancelPaymentIgnoreTerminal(); err != nil {
				return fmt.Errorf("cancel payment on cancelled order %s: %w", prevOrd.ID, err)
			}
		}
	}
	return nil
}

// restockOrderItemsWithConfirmErr ensures Product.ConfirmStock(checkoutGroupID) succeeds BEFORE
// calling RestockOrderItems(orderID, items), preventing P0-2 phantom stock or leaked reserved_stock
// when an earlier checkout ConfirmStock failed transiently. If reservations were already released
// by ReleaseStock (ConfirmStock returns FailedPrecondition and ReleaseStock reports Released=true),
// skips RestockOrderItems cleanly without double-adding stock.
func (s *OrderService) restockOrderItemsWithConfirmErr(ctx context.Context, checkoutGroupID, orderID string, items []db.OrderItem) error {
	if s.clients.Product == nil || strings.TrimSpace(orderID) == "" || len(items) == 0 {
		return nil
	}
	if strings.TrimSpace(checkoutGroupID) != "" {
		cErr := clients.RetryIdempotent(ctx, 3, func(c context.Context) error {
			_, err := s.clients.Product.ConfirmStock(c, &productv1.ConfirmStockRequest{
				OrderId: checkoutGroupID,
			})
			return err
		})
		if cErr != nil {
			if status.Code(cErr) == codes.FailedPrecondition {
				// Reservation may already have been RELEASED by a sibling's final ReleaseStock(groupID).
				released, relErr := s.releaseStockErr(ctx, checkoutGroupID)
				if relErr == nil && released {
					_ = s.store.ClearCheckoutGroupPendingStockConfirm(ctx, checkoutGroupID)
					return nil
				}
			}
			if status.Code(cErr) == codes.NotFound {
				// P2-5: Legacy/seeded order with no stock_reservations row -> clear flag and proceed to RestockOrderItems.
				s.logger.WarnContext(ctx, "ConfirmStock returned NotFound before RestockOrderItems; proceeding with restock",
					"checkout_group_id", checkoutGroupID, "order_id", orderID)
				_ = s.store.ClearCheckoutGroupPendingStockConfirm(ctx, checkoutGroupID)
			} else {
				return fmt.Errorf("ConfirmStock(%s) before RestockOrderItems(%s): %w", checkoutGroupID, orderID, cErr)
			}
		} else {
			_ = s.store.ClearCheckoutGroupPendingStockConfirm(ctx, checkoutGroupID)
		}
	}
	return s.restockOrderItemsErr(ctx, orderID, items)
}

func (s *OrderService) restockOrderItemsErr(ctx context.Context, orderID string, items []db.OrderItem) error {
	if s.clients.Product == nil || strings.TrimSpace(orderID) == "" || len(items) == 0 {
		return nil
	}
	qtyBySKU := make(map[string]int32, len(items))
	for _, it := range items {
		skuID := strings.TrimSpace(it.SKUID)
		if skuID != "" && it.Quantity > 0 {
			qtyBySKU[skuID] += it.Quantity
		}
	}
	if len(qtyBySKU) == 0 {
		return nil
	}

	skuIDs := make([]string, 0, len(qtyBySKU))
	for skuID := range qtyBySKU {
		skuIDs = append(skuIDs, skuID)
	}
	sort.Strings(skuIDs)

	reqItems := make([]*productv1.StockReservationItem, 0, len(skuIDs))
	for _, skuID := range skuIDs {
		reqItems = append(reqItems, &productv1.StockReservationItem{
			SkuId:    skuID,
			Quantity: qtyBySKU[skuID],
		})
	}

	return clients.RetryIdempotent(ctx, 3, func(c context.Context) error {
		_, rErr := s.clients.Product.RestockOrderItems(c, &productv1.RestockOrderItemsRequest{
			OrderId: orderID,
			Items:   reqItems,
		})
		return rErr
	})
}

// SellerConfirmOrder transitions a shop's order from CONFIRMED -> PREPARING.
func (s *OrderService) SellerConfirmOrder(ctx context.Context, req *orderv1.SellerConfirmOrderRequest) (*orderv1.SellerConfirmOrderResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canManageShopOrder(caller, agg.Order, req.GetShopId()) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller can confirm this order")
	}

	res, err := s.store.TransitionOrderStatus(ctx, store.SingleOrderTransitionParams{
		OrderID:      orderID,
		TargetStatus: orderv1.OrderStatus_ORDER_STATUS_PREPARING,
		ActorID:      caller.UserID,
		ActorRole:    fallbackStr(caller.Role, "seller"),
		Note:         "Seller confirmed order and began preparation",
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	return &orderv1.SellerConfirmOrderResponse{
		Order: aggregateToProtoForCaller(*res.Order, caller),
	}, nil
}

// SellerShipOrder creates a shipment via ShippingService and transitions CONFIRMED|PREPARING -> PREPARING with tracking.
// Calling it again once tracking_code is already assigned on a PREPARING order is idempotent.
func (s *OrderService) SellerShipOrder(ctx context.Context, req *orderv1.SellerShipOrderRequest) (*orderv1.SellerShipOrderResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canManageShopOrder(caller, agg.Order, req.GetShopId()) {
		return nil, status.Error(codes.PermissionDenied, "only the owning shop seller can ship this order")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return nil, status.Errorf(codes.FailedPrecondition,
			"invalid order status transition: cannot create shipment from status %s",
			statemachine.StatusToString(curStatus))
	}

	// P0 M1: Do NOT allow creating a shipment while checkout payment initialization is still IN_PROGRESS.
	if err := s.requireCheckoutPaymentInitialized(ctx, agg.Order.CheckoutGroupID); err != nil {
		return nil, err
	}

	reqCarrier := strings.TrimSpace(req.GetCarrierCode())
	reqService := strings.TrimSpace(req.GetServiceCode())
	orderCarrier := strings.TrimSpace(agg.Order.CarrierCode)
	orderService := strings.TrimSpace(agg.Order.ServiceCode)

	// P2-2: checkout fallback quotes allow seller to select any real enabled carrier/service on shipment creation.
	isFallbackCarrier := strings.EqualFold(orderCarrier, "fallback")
	if !isFallbackCarrier {
		if reqCarrier != "" && orderCarrier != "" && !strings.EqualFold(reqCarrier, orderCarrier) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"carrier_code %q does not match buyer's selected carrier %q", reqCarrier, orderCarrier)
		}
		if reqService != "" && orderService != "" && !strings.EqualFold(reqService, orderService) {
			return nil, status.Errorf(codes.FailedPrecondition,
				"service_code %q does not match buyer's selected service %q", reqService, orderService)
		}
	}

	totalWeight, itemsSummary := summarizeOrderParcel(agg.Items)
	toAddr := orderAddressToShipmentAddress(agg.Order.ShippingAddressJSON)
	// Resolve the shop's configured pickup address BEFORE marking pending_shipment_cancel so a shop
	// missing a pickup address fails clearly without mutating or locking the existing order.
	fromAddr, err := s.resolveShopPickupAddress(ctx, agg.Order.ShopID)
	if err != nil {
		return nil, err
	}

	// Mark durable shipment-creation intent (with fresh TTL timestamp + attempt token) before calling ShippingService
	// so even a crash or concurrent cancel cannot leave an uncancelled orphan shipment behind.
	// Retrying SellerShipOrder refreshes pending_shipment_cancel_at; CANCELLED orders fail immediately under FOR UPDATE.
	markedAt := s.nowFn()
	creatingToken, err := s.store.MarkOrderShipmentCreating(ctx, agg.Order.ID, markedAt)
	if err != nil {
		return nil, mapStoreError(err)
	}

	carrierCode := orderCarrier
	if isFallbackCarrier || carrierCode == "" {
		carrierCode = reqCarrier
	}
	serviceCode := orderService
	if isFallbackCarrier || serviceCode == "" {
		serviceCode = reqService
	}

	var codAmountVND int64
	if statemachine.StringToPaymentMethod(agg.Order.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD {
		codAmountVND = agg.Order.TotalPayableVND
	}

	// Guard against a stalled context/goroutine waking up past PendingShipmentCancelTTL before calling CreateShipment.
	if ctx.Err() != nil || s.nowFn().Sub(markedAt) > store.PendingShipmentCancelTTL {
		clearCtx, clearCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer clearCancel()
		_ = s.store.ClearOrderPendingShipmentCancelByToken(clearCtx, agg.Order.ID, creatingToken)
		return nil, status.Error(codes.FailedPrecondition, "lượt tạo vận đơn đã quá hạn, vui lòng thử lại")
	}

	// Always call CreateShipment (idempotent on shipping side: returns live shipment or creates
	// a replacement if a previous shipment was cancelled during a partial cancel failure).
	shipResp, err := s.clients.Shipping.CreateShipment(ctx, &shippingv1.CreateShipmentRequest{
		OrderId:      agg.Order.ID,
		ShopId:       agg.Order.ShopID,
		BuyerId:      agg.Order.BuyerID,
		CarrierCode:  carrierCode,
		ServiceCode:  serviceCode,
		CodAmountVnd: codAmountVND,
		WeightGrams:  totalWeight,
		FromAddress:  fromAddr,
		ToAddress:    toAddr,
		ItemsSummary: itemsSummary,
	})
	if err != nil {
		// P3-2: clear pending_shipment_cancel (scoped by attempt token) on errors that guarantee no shipment was created.
		if code := status.Code(err); code == codes.InvalidArgument || code == codes.FailedPrecondition || code == codes.PermissionDenied {
			clearCtx, clearCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
			defer clearCancel()
			_ = s.store.ClearOrderPendingShipmentCancelByToken(clearCtx, agg.Order.ID, creatingToken)
		}
		return nil, err
	}

	// E18: Transient carrier failure (5xx/timeout/circuit-open) queued an async retry job in shipping.
	// Record shipment_job_status="QUEUED" without advancing order status (CONFIRMED stays CONFIRMED).
	if shipResp.GetQueued() {
		jobID := strings.TrimSpace(shipResp.GetJobId())
		s.logger.InfoContext(ctx, "shipment creation queued for async retry",
			"order_id", agg.Order.ID,
			"job_id", jobID,
		)
		note := "Shipment creation queued for async retry"
		qCtx, qCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer qCancel()
		qOut, qErr := s.store.MarkOrderShipmentQueued(qCtx, store.QueueShipmentParams{
			OrderID:               agg.Order.ID,
			JobID:                 jobID,
			ExpectedCreatingToken: creatingToken,
			ActorID:               fallbackStr(caller.UserID, req.GetSellerUserId()),
			ActorRole:             fallbackStr(caller.Role, "seller"),
			Note:                  note,
			Now:                   s.nowFn(),
		})
		if qErr != nil {
			if errors.Is(qErr, store.ErrOrphanShipmentNeedsCancel) {
				s.compensateOrphanForwardShipment(ctx, agg.Order.ID, "order cancelled concurrently while queueing shipment")
			}
			return nil, mapStoreError(qErr)
		}
		if qOut.AlreadyAttached {
			return &orderv1.SellerShipOrderResponse{
				Order:        aggregateToProtoForCaller(*qOut.Order, caller),
				TrackingCode: qOut.Order.Order.TrackingCode,
				LabelUrl:     qOut.Order.Order.LabelURL,
				Queued:       false,
			}, nil
		}
		return &orderv1.SellerShipOrderResponse{
			Order:         aggregateToProtoForCaller(*qOut.Order, caller),
			Queued:        true,
			ShipmentJobId: jobID,
		}, nil
	}

	shipment := shipResp.GetShipment()
	trackingCode := shipment.GetTrackingCode()
	labelURL := shipment.GetLabelUrl()
	if shipment.GetCarrierCode() != "" {
		carrierCode = shipment.GetCarrierCode()
	}
	if shipment.GetServiceCode() != "" {
		serviceCode = shipment.GetServiceCode()
	}

	updatedAgg, err := s.store.TransitionOrderPreparedForShipment(ctx, store.ShipOrderParams{
		OrderID:               agg.Order.ID,
		CarrierCode:           carrierCode,
		ServiceCode:           serviceCode,
		TrackingCode:          trackingCode,
		LabelURL:              labelURL,
		ExpectedCreatingToken: creatingToken,
		ActorID:               fallbackStr(caller.UserID, req.GetSellerUserId()),
		ActorRole:             fallbackStr(caller.Role, "seller"),
		Note:                  fmt.Sprintf("Shipment created via %s (tracking: %s)", carrierCode, trackingCode),
		Now:                   s.nowFn(),
	})
	if err != nil {
		if errors.Is(err, store.ErrOrphanShipmentNeedsCancel) {
			s.compensateOrphanForwardShipment(ctx, agg.Order.ID, "order cancelled concurrently during shipment creation")
		}
		return nil, mapStoreError(err)
	}

	return &orderv1.SellerShipOrderResponse{
		Order:        aggregateToProtoForCaller(*updatedAgg, caller),
		TrackingCode: updatedAgg.Order.TrackingCode,
		LabelUrl:     updatedAgg.Order.LabelURL,
	}, nil
}

func summarizeOrderParcel(items []db.OrderItem) (int32, string) {
	var totalWeight int32
	summaryParts := make([]string, 0, len(items))
	for _, it := range items {
		w := it.WeightGrams
		if w <= 0 {
			w = 500
		}
		totalWeight += w * it.Quantity
		summaryParts = append(summaryParts, fmt.Sprintf("%s x%d", fallbackStr(it.ProductName, it.SKUID), it.Quantity))
	}
	if totalWeight <= 0 {
		totalWeight = 500
	}
	return totalWeight, strings.Join(summaryParts, ", ")
}

func orderAddressToShipmentAddress(raw []byte) *shippingv1.ShipmentAddress {
	var snap orderv1.OrderAddressSnapshot
	_ = json.Unmarshal(raw, &snap)
	return &shippingv1.ShipmentAddress{
		Name:          snap.GetRecipientName(),
		Phone:         snap.GetRecipientPhone(),
		ProvinceCode:  snap.GetProvinceCode(),
		WardCode:      snap.GetWardCode(),
		StreetAddress: snap.GetStreetAddress(),
	}
}

// stripServiceAuth clears internal service claims before calling public RPCs not listed in internalRPCs (e.g. UserService.GetShop).
func stripServiceAuth(ctx context.Context) context.Context {
	if c, ok := auth.ClaimsFromContext(ctx); ok && c.Service != "" {
		return auth.WithRawToken(auth.WithClaims(ctx, nil), "")
	}
	return ctx
}

// resolveShopPickupAddress loads the shop's configured pickup address from UserService.
// It NEVER falls back to the buyer's shipping address: missing or incomplete shop pickup configuration
// returns FailedPrecondition with code SHOP_PICKUP_ADDRESS_REQUIRED.
func (s *OrderService) resolveShopPickupAddress(ctx context.Context, shopID string) (*shippingv1.ShipmentAddress, error) {
	if s.clients.User == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: user service unavailable for shop %s", shopID)
	}
	shopResp, err := s.clients.User.GetShop(stripServiceAuth(ctx), &userv1.GetShopRequest{ShopId: shopID})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: shop %s not found", shopID)
		}
		return nil, fmt.Errorf("resolve shop %s for pickup address: %w", shopID, err)
	}
	shop := shopResp.GetShop()
	if shop == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: shop %s not found", shopID)
	}
	return s.resolveShopPickupAddressFromShop(ctx, shop)
}

func (s *OrderService) resolveShopPickupAddressFromShop(ctx context.Context, shop *userv1.Shop) (*shippingv1.ShipmentAddress, error) {
	if shop == nil {
		return nil, status.Error(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: shop not found")
	}
	shopID := strings.TrimSpace(shop.GetId())
	if s.clients.User == nil {
		return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: user service unavailable for shop %s", shopID)
	}
	pickupID := strings.TrimSpace(shop.GetPickupAddressId())
	ownerID := strings.TrimSpace(shop.GetOwnerUserId())
	if pickupID == "" || ownerID == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: shop %s has no pickup address configured", shopID)
	}

	addrResp, err := s.clients.User.GetAddress(clients.WithInternalAdminContext(ctx), &userv1.GetAddressRequest{
		UserId:    ownerID,
		AddressId: pickupID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: pickup address %s for shop %s not found", pickupID, shopID)
		}
		return nil, fmt.Errorf("resolve pickup address %s for shop %s: %w", pickupID, shopID, err)
	}
	a := addrResp.GetAddress()
	if a == nil || strings.TrimSpace(a.GetProvinceCode()) == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "SHOP_PICKUP_ADDRESS_REQUIRED: pickup address for shop %s is missing province_code", shopID)
	}

	res := &shippingv1.ShipmentAddress{
		Name:          fallbackStr(strings.TrimSpace(a.GetRecipientName()), fallbackStr(strings.TrimSpace(shop.GetName()), "Shop "+shopID)),
		Phone:         fallbackStr(strings.TrimSpace(a.GetRecipientPhone()), strings.TrimSpace(shop.GetContactPhone())),
		ProvinceCode:  strings.TrimSpace(a.GetProvinceCode()),
		WardCode:      strings.TrimSpace(a.GetWardCode()),
		StreetAddress: strings.TrimSpace(a.GetStreetAddress()),
	}
	return res, nil
}

// completeDeliveredOrder is the single shared completion path for BuyerConfirmReceived and the 7-day auto-complete job.
// Payment escrow settlement and hold release are driven asynchronously via order.completed (no direct ReleaseEscrow RPC).
func (s *OrderService) completeDeliveredOrder(
	ctx context.Context,
	ord db.Order,
	actorID, actorRole, note string,
	requireNoReturnRequest bool,
) (*store.OrderAggregate, error) {
	// P0 M1: Refuse completion while checkout payment initialization is still IN_PROGRESS.
	if err := s.requireCheckoutPaymentInitialized(ctx, ord.CheckoutGroupID); err != nil {
		return nil, err
	}
	traceID, traceparent := traceMetaFromContext(ctx)
	outboxEv, err := s.buildOrderCompletedOutbox(ord, traceID, traceparent)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	updatedAgg, err := s.store.TransitionOrderCompleted(ctx, store.CompleteOrderParams{
		OrderID:                ord.ID,
		ActorID:                actorID,
		ActorRole:              actorRole,
		Note:                   note,
		RequireNoReturnRequest: requireNoReturnRequest,
		OutboxEvent:            outboxEv,
		Now:                    s.nowFn(),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return updatedAgg, nil
}

// BuyerConfirmReceived transitions DELIVERED -> COMPLETED and emits order.completed via outbox.
func (s *OrderService) BuyerConfirmReceived(ctx context.Context, req *orderv1.BuyerConfirmReceivedRequest) (*orderv1.BuyerConfirmReceivedResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canActAsBuyer(caller, agg.Order, req.GetBuyerId()) {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order can confirm receipt")
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
		return nil, status.Errorf(codes.FailedPrecondition,
			"buyer can only confirm receipt of a DELIVERED order (current status: %s)",
			statemachine.StatusToString(curStatus))
	}

	updatedAgg, err := s.completeDeliveredOrder(
		ctx,
		agg.Order,
		fallbackStr(caller.UserID, agg.Order.BuyerID),
		fallbackStr(caller.Role, "buyer"),
		"Buyer confirmed receipt of order",
		false,
	)
	if err != nil {
		return nil, err
	}

	return &orderv1.BuyerConfirmReceivedResponse{
		Order: aggregateToProtoForCaller(*updatedAgg, caller),
	}, nil
}

// MarkPaymentResult handles explicit internal payment result notifications alongside NATS events.
func (s *OrderService) MarkPaymentResult(ctx context.Context, req *orderv1.MarkPaymentResultRequest) (*orderv1.MarkPaymentResultResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.IsFullAdmin() && caller.Service == "" && caller.Role != "system" {
		return nil, status.Error(codes.PermissionDenied, "admin or internal system role required")
	}
	if req == nil || req.GetStatus() == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "valid payment result status is required")
	}
	if strings.TrimSpace(req.GetCheckoutGroupId()) == "" && strings.TrimSpace(req.GetOrderId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "checkout_group_id or order_id is required")
	}

	if err := s.HandlePaymentResult(ctx, PaymentResultInput{
		CheckoutGroupID: req.GetCheckoutGroupId(),
		OrderID:         req.GetOrderId(),
		PaymentID:       req.GetPaymentId(),
		Status:          req.GetStatus(),
		Reason:          req.GetReason(),
		ActorID:         fallbackStr(caller.UserID, "payment-service"),
		ActorRole:       fallbackStr(caller.Role, "system"),
	}); err != nil {
		return nil, mapStoreError(err)
	}

	return &orderv1.MarkPaymentResultResponse{Ok: true}, nil
}

type retryPaymentOutcome struct {
	CheckoutGroupID  string
	PaymentID        string
	PaymentMethod    string
	SwitchedToCOD    bool
	PayURL           string
	PaymentExpiresAt *time.Time
	Orders           []store.OrderAggregate
}

// RetryPayment validates a pending unexpired online order group and creates a superseding payment
// (keeping the original payment window, recalculating seller-borne payment fees, or switching to COD
// after full E19 COD eligibility & flash-sale checks; UC-B25 / F5 / F6 / F12).
func (s *OrderService) RetryPayment(ctx context.Context, req *orderv1.RetryPaymentRequest) (*orderv1.RetryPaymentResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.GetPaymentId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_id is required")
	}

	requestedMethod := ""
	if req.GetPaymentMethod() != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_UNSPECIFIED {
		requestedMethod = statemachine.PaymentMethodToString(req.GetPaymentMethod())
		if !statemachine.IsValidPaymentMethod(requestedMethod) {
			return nil, status.Error(codes.FailedPrecondition, "unsupported payment method for retry")
		}
	}

	out, err := s.retryPaymentByMethod(ctx, caller, strings.TrimSpace(req.GetPaymentId()), requestedMethod, req.GetIdempotencyKey())
	if err != nil {
		return nil, err
	}

	orders := make([]*orderv1.Order, 0, len(out.Orders))
	for _, agg := range out.Orders {
		orders = append(orders, aggregateToProtoForCaller(agg, caller))
	}
	resp := &orderv1.RetryPaymentResponse{
		CheckoutGroupId: out.CheckoutGroupID,
		PaymentId:       out.PaymentID,
		PaymentMethod:   statemachine.StringToPaymentMethod(out.PaymentMethod),
		SwitchedToCod:   out.SwitchedToCOD,
		PayUrl:          out.PayURL,
		Orders:          orders,
	}
	if out.PaymentExpiresAt != nil {
		resp.PaymentExpiresAt = timestamppb.New(*out.PaymentExpiresAt)
	}
	return resp, nil
}

// retryPaymentByMethod executes the string-based F5/F6/F12 retry & payment-method switch core.
func (s *OrderService) retryPaymentByMethod(
	ctx context.Context,
	caller callerIdentity,
	paymentID string,
	requestedMethod string,
	rawClientKey string,
) (*retryPaymentOutcome, error) {
	if strings.TrimSpace(paymentID) == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_id is required")
	}
	reqMethodNorm := statemachine.NormalizePaymentMethod(requestedMethod)
	if reqMethodNorm != "" && reqMethodNorm != "UNSPECIFIED" && !statemachine.IsValidPaymentMethod(reqMethodNorm) {
		return nil, status.Errorf(codes.FailedPrecondition, "unsupported payment method %q for retry", requestedMethod)
	}

	group, err := s.store.GetCheckoutGroupByPaymentID(ctx, paymentID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, mapStoreError(err)
		}
		// Payment ID may belong to a superseded/stale payment; check PaymentService to return 403/409 accurately.
		if s.clients.Payment == nil {
			return nil, status.Error(codes.NotFound, "payment not found")
		}
		payResp, pErr := s.clients.Payment.GetPayment(ctx, &paymentv1.GetPaymentRequest{PaymentId: paymentID})
		if pErr != nil {
			if status.Code(pErr) == codes.NotFound || status.Code(pErr) == codes.PermissionDenied {
				return nil, pErr
			}
			return nil, status.Error(codes.NotFound, "payment not found")
		}
		pay := payResp.GetPayment()
		if pay == nil {
			return nil, status.Error(codes.NotFound, "payment not found")
		}
		if pay.GetUserId() != "" && pay.GetUserId() != caller.UserID {
			return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order group can retry payment")
		}
		if pay.GetOrderGroupId() == "" {
			return nil, status.Error(codes.NotFound, "order group for payment not found")
		}
		group, err = s.store.GetCheckoutGroupByID(ctx, pay.GetOrderGroupId())
		if err != nil {
			return nil, mapStoreError(err)
		}
	}

	if group.BuyerID != caller.UserID {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order group can retry payment")
	}

	// Review2-O P1-3 + F5: acquire cross-replica DB advisory lock scoped to checkout_group_id spanning
	// validation -> Payment.CreatePayment -> UpdateGroupPaymentOnRetry so concurrent retries/switches
	// across replicas serialize cleanly.
	var (
		retryOut      *retryPaymentOutcome
		switchedToCOD bool
	)
	err = s.store.WithCheckoutGroupRetryLock(ctx, group.ID, func() error {
		lockedGroup, gErr := s.store.GetCheckoutGroupByID(ctx, group.ID)
		if gErr != nil {
			return mapStoreError(gErr)
		}

		// P2 N10 + P3(b): If CompleteCheckoutWithPayment failed all retries earlier leaving the group IN_PROGRESS
		// with payment_id == "", recover it from PaymentService before checking lockedGroup.PaymentID.
		if lockedGroup.Status == "IN_PROGRESS" && lockedGroup.PaymentID == "" {
			if _, _, recErr := s.tryFinalizeInProgressGroupFromPayment(ctx, lockedGroup.ID); recErr != nil {
				s.logger.WarnContext(ctx, "RetryPayment failed to recover IN_PROGRESS checkout group",
					"checkout_group_id", lockedGroup.ID, "error", recErr)
				return status.Errorf(codes.Unavailable, "checkout payment finalization is still recovering; please retry shortly: %v", recErr)
			}
			reloaded, rErr := s.store.GetCheckoutGroupByID(ctx, lockedGroup.ID)
			if rErr != nil {
				return mapStoreError(rErr)
			}
			lockedGroup = reloaded
		}

		clientKey := strings.TrimSpace(rawClientKey)
		idemKey := "retry:" + lockedGroup.ID + ":" + paymentID
		if clientKey != "" {
			idemKey = "retry:" + lockedGroup.ID + ":" + clientKey
		}

		if lockedGroup.PaymentID == "" {
			return status.Error(codes.FailedPrecondition, "payment_id does not match current active payment for this order group")
		}

		now := s.nowFn()
		if lockedGroup.PaymentID != paymentID {
			// Review-A P0-3(c): sequential replay with the same (payment_id, idempotency_key)
			// returns the already-created payment idempotently without re-checking shop/config.
			if clientKey != "" && lockedGroup.RetryIdempotencyKey == idemKey && lockedGroup.RetryFromPaymentID == paymentID {
				committedMethod := statemachine.NormalizePaymentMethod(lockedGroup.PaymentMethod)
				if reqMethodNorm != "" && reqMethodNorm != "UNSPECIFIED" && reqMethodNorm != committedMethod {
					return status.Error(codes.FailedPrecondition, "IDEMPOTENCY_KEY_CONFLICT: idempotency_key was already used with a different payment method")
				}
				aggs, lErr := s.store.ListOrdersByCheckoutGroup(ctx, lockedGroup.ID)
				if lErr != nil {
					return mapStoreError(lErr)
				}
				if statemachine.IsCODMethod(committedMethod) {
					if len(aggs) == 0 {
						return status.Error(codes.FailedPrecondition, "cannot retry payment: order group has no orders")
					}
					for _, agg := range aggs {
						if statemachine.StringToStatus(agg.Order.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
							return status.Errorf(codes.FailedPrecondition, "cannot retry payment when order %s is in status %s", agg.Order.ID, agg.Order.Status)
						}
					}
					retryOut = &retryPaymentOutcome{
						CheckoutGroupID: lockedGroup.ID,
						PaymentID:       lockedGroup.PaymentID,
						PaymentMethod:   committedMethod,
						SwitchedToCOD:   true,
						Orders:          aggs,
					}
					return nil
				}
				_, _, exp, vErr := validateRetryableGroupOrders(aggs, lockedGroup.PaymentID, lockedGroup.PaymentExpiresAt, now)
				if vErr != nil {
					return vErr
				}
				retryOut = &retryPaymentOutcome{
					CheckoutGroupID:  lockedGroup.ID,
					PaymentID:        lockedGroup.PaymentID,
					PaymentMethod:    committedMethod,
					PayURL:           lockedGroup.PayURL,
					PaymentExpiresAt: exp,
					Orders:           aggs,
				}
				return nil
			}
			return status.Error(codes.FailedPrecondition, "payment_id does not match current active payment for this order group")
		}

		aggs, lErr := s.store.ListOrdersByCheckoutGroup(ctx, lockedGroup.ID)
		if lErr != nil {
			return mapStoreError(lErr)
		}
		_, totalPayableVND, _, vErr := validateRetryableGroupOrders(aggs, paymentID, lockedGroup.PaymentExpiresAt, now)
		if vErr != nil {
			return vErr
		}

		targetMethod, mErr := ResolveRetryTargetMethod(aggs[0].Order.PaymentMethod, reqMethodNorm)
		if mErr != nil {
			return mErr
		}

		// F5: Fail fast when attempting to switch a Flash Sale order group to COD.
		if statemachine.IsCODMethod(targetMethod) && groupHasFlashSale(aggs) {
			return status.Error(
				codes.FailedPrecondition,
				"FLASH_SALE_COD_NOT_ALLOWED: đơn hàng có sản phẩm Flash Sale không hỗ trợ chuyển sang thanh toán khi nhận hàng (COD)",
			)
		}

		// F12: Synchronously verify every shop in the group is still ACTIVE before creating a replacement payment.
		shopIDs := make([]string, 0, len(aggs))
		for _, agg := range aggs {
			shopIDs = append(shopIDs, agg.Order.ShopID)
		}
		shopsByID, _, shopErr := s.verifyShopsActive(ctx, shopIDs)
		if shopErr != nil {
			return shopErr
		}

		// F6: Read authoritative payment method configuration directly from PaymentService (no caching).
		methodCfg, _, cfgErr := s.loadPaymentMethodConfig(ctx, targetMethod)
		if cfgErr != nil {
			return cfgErr
		}

		// F5: Look up authoritative payment_fee_bps for the target payment method.
		paymentFeeBPS, feeErr := s.lookupPaymentFeeBPS(ctx, targetMethod, now)
		if feeErr != nil {
			return feeErr
		}

		// F5 + E19: Switching Online -> COD runs the full assessCOD pipeline (verified phone, ward/carrier COD
		// support, cumulative rejected count, terminal COD reject rate, and F6 COD min/max bounds).
		var codAssessmentErr error
		if statemachine.IsCODMethod(targetMethod) {
			if s.clients.User == nil {
				return status.Error(codes.Unavailable, "user service is unavailable")
			}
			valResp, valErr := s.clients.User.ValidateUser(ctx, &userv1.ValidateUserRequest{UserId: lockedGroup.BuyerID})
			if valErr != nil {
				return wrapDownstreamError("validate buyer account for COD switch", valErr)
			}
			if !valResp.GetValid() || valResp.GetStatus() != userv1.UserStatus_USER_STATUS_ACTIVE {
				return status.Error(codes.PermissionDenied, "buyer account is not active")
			}
			orderedShopIDs, shopCODSupport, shipErr := s.verifyOrderCODShippingSupport(ctx, aggs, shopsByID)
			if shipErr != nil {
				return shipErr
			}
			_, codAssessmentErr = s.assessCOD(ctx, lockedGroup.BuyerID, valResp, orderedShopIDs, shopCODSupport, totalPayableVND, methodCfg, true)
		}

		// F5: Pure planner validates invariants, preserves original expiry, and recalculates fees.
		plan, planErr := PlanRetryPaymentSwitch(RetrySwitchInput{
			Group:            *lockedGroup,
			Aggs:             aggs,
			ExpectedPayID:    paymentID,
			RequestedMethod:  targetMethod,
			MethodConfig:     methodCfg,
			PaymentFeeBPS:    paymentFeeBPS,
			Now:              now,
			FlashExpiryCap:   s.flashPaymentExpiryDuration(),
			CODAssessmentErr: codAssessmentErr,
		})
		if planErr != nil {
			return planErr
		}

		payProtoMethod, pmErr := paymentProtoMethod(plan.TargetMethod)
		if pmErr != nil {
			return pmErr
		}

		createPayReq := &paymentv1.CreatePaymentRequest{
			OrderGroupId:      lockedGroup.ID,
			OrderId:           lockedGroup.ID,
			UserId:            lockedGroup.BuyerID,
			AmountVnd:         plan.TotalPayableVND,
			Method:            payProtoMethod,
			ShopAllocations:   plan.ShopAllocations,
			IdempotencyKey:    idemKey,
			SupersedePrevious: true,
			SalesChannel:      effectiveSalesChannel(lockedGroup.SalesChannel),
			StorefrontShopId:  lockedGroup.StorefrontShopID,
		}
		if !plan.SwitchedToCOD && plan.ExpiresAt != nil {
			createPayReq.ExpiresAt = timestamppb.New(*plan.ExpiresAt)
		}

		// Item F (#6): run CreatePayment with detachedCallerContext (keeps buyer JWT while insulating
		// from client disconnect, matching CreateCheckout). On ambiguous errors, probe GetPayment(order_group_id)
		// before returning error so a committed replacement payment is recovered instead of orphaned.
		payCtx, payCancel := detachedCallerContext(ctx, s.cfg.GRPCCallTimeout)
		payResp, pErr := s.clients.Payment.CreatePayment(payCtx, createPayReq)
		payCancel()
		if pErr != nil {
			if isAmbiguousReserveError(pErr) {
				probeCtx, probeCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
				var probedPay *paymentv1.Payment
				probeErr := clients.RetryIdempotent(probeCtx, 3, func(c context.Context) error {
					gResp, gErr := s.clients.Payment.GetPayment(c, &paymentv1.GetPaymentRequest{
						OrderGroupId: lockedGroup.ID,
					})
					if gErr != nil {
						return gErr
					}
					probedPay = gResp.GetPayment()
					return nil
				})
				probeCancel()
				if probeErr != nil {
					if status.Code(probeErr) == codes.NotFound {
						return pErr
					}
					return status.Errorf(codes.Unavailable, "retry payment status uncertain after ambiguous CreatePayment error; retry with same idempotency_key: %v", pErr)
				}
				if probedPay != nil &&
					strings.TrimSpace(probedPay.GetPaymentId()) != "" &&
					probedPay.GetPaymentId() != paymentID &&
					(probedPay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING ||
						probedPay.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED) {
					payResp = &paymentv1.CreatePaymentResponse{Payment: probedPay}
					pErr = nil
				}
			}
			if pErr != nil {
				return pErr
			}
		}

		newPay := payResp.GetPayment()
		if newPay == nil || strings.TrimSpace(newPay.GetPaymentId()) == "" {
			return status.Error(codes.Internal, "payment service returned empty payment on retry")
		}
		newPaymentID := newPay.GetPaymentId()
		// Review2-O P2-7: reject replayed payment records that have already left PENDING state.
		if newPay.GetStatus() != paymentv1.PaymentStatus_PAYMENT_STATUS_PENDING {
			return status.Errorf(codes.FailedPrecondition,
				"cannot retry payment: payment %s is in status %s",
				newPaymentID, paymentStatusToString(newPay.GetStatus(), newPay.GetStatus().String()))
		}
		newPaymentStatus := paymentStatusToString(newPay.GetStatus(), "PENDING")

		var finalExpiresAt *time.Time
		if !plan.SwitchedToCOD && plan.ExpiresAt != nil {
			exp := *plan.ExpiresAt
			// F5: Never allow PaymentService response to extend the original checkout group expiry.
			if newPay.GetExpiresAt() != nil {
				payExp := newPay.GetExpiresAt().AsTime().UTC()
				if payExp.Before(exp) {
					exp = payExp
				}
			}
			finalExpiresAt = &exp
		}

		// Review-A P0-3(b): persist the newly created superseding payment even if the client disconnects.
		dbTimeout := s.cfg.GRPCCallTimeout
		if dbTimeout <= 0 {
			dbTimeout = 10 * time.Second
		}
		dbCtx, dbCancel := context.WithTimeout(context.WithoutCancel(ctx), dbTimeout)
		defer dbCancel()

		payURL := ""
		if !plan.SwitchedToCOD {
			payURL = newPay.GetPayUrl()
		}

		traceID, traceparent := traceMetaFromContext(ctx)
		retryUpdateParams := store.RetryPaymentUpdateParams{
			CheckoutGroupID:     lockedGroup.ID,
			ExpectedPaymentID:   paymentID,
			TargetMethod:        plan.TargetMethod,
			NewPaymentID:        newPaymentID,
			PaymentStatus:       newPaymentStatus,
			QRURL:               newPay.GetQrUrl(),
			QRCode:              newPay.GetQrCode(),
			PayURL:              payURL,
			PaymentExpiresAt:    finalExpiresAt,
			SwitchToCOD:         plan.SwitchedToCOD,
			OrderFees:           plan.StoreOrderFees,
			ActorID:             caller.UserID,
			ActorRole:           fallbackStr(caller.Role, "buyer"),
			TraceID:             traceID,
			Traceparent:         traceparent,
			RetryIdempotencyKey: idemKey,
			Now:                 now,
		}
		// Item F (#6): retry transient DB errors up to 3 attempts with backoff so a committed replacement
		// payment is persisted instead of orphaned; never retry deterministic precondition/domain errors.
		var uErr error
		backoff := 25 * time.Millisecond
		for attempt := 0; attempt < 3; attempt++ {
			uErr = s.store.UpdateGroupPaymentOnRetry(dbCtx, retryUpdateParams)
			if uErr == nil ||
				errors.Is(uErr, store.ErrRetryPrecondition) ||
				errors.Is(uErr, store.ErrNotFound) ||
				errors.Is(uErr, statemachine.ErrInvalidTransition) {
				break
			}
			if attempt < 2 {
				timer := time.NewTimer(backoff)
				select {
				case <-dbCtx.Done():
					timer.Stop()
				case <-timer.C:
				}
				if dbCtx.Err() != nil {
					break
				}
				backoff *= 2
			}
		}
		if uErr != nil {
			// Review-A P0-3(a): only cancel the newly created payment when order state raced to a
			// precondition failure (not PENDING, expired, or moved to a different payment); never on transient DB errors.
			if errors.Is(uErr, store.ErrRetryPrecondition) && s.clients.Payment != nil {
				compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
				defer cancel()
				if cErr := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
					_, err := s.clients.Payment.CancelPayment(c, &paymentv1.CancelPaymentRequest{
						PaymentId:    newPaymentID,
						OrderGroupId: lockedGroup.ID,
						Reason:       "Retry payment DB sync conflict",
					})
					if code := status.Code(err); code == codes.NotFound || code == codes.FailedPrecondition || code == codes.AlreadyExists {
						return nil
					}
					return err
				}); cErr != nil {
					s.logger.WarnContext(ctx, "failed to cancel superseded retry payment after DB precondition conflict",
						"checkout_group_id", lockedGroup.ID, "payment_id", newPaymentID, "error", cErr)
				}
			}
			return mapStoreError(uErr)
		}

		updatedAggs, reloadErr := s.store.ListOrdersByCheckoutGroup(dbCtx, lockedGroup.ID)
		if reloadErr != nil {
			s.logger.WarnContext(ctx, "failed to reload orders after UpdateGroupPaymentOnRetry; returning pre-update snapshot",
				"checkout_group_id", lockedGroup.ID, "error", reloadErr)
			updatedAggs = aggs
		}

		switchedToCOD = plan.SwitchedToCOD
		retryOut = &retryPaymentOutcome{
			CheckoutGroupID:  lockedGroup.ID,
			PaymentID:        newPaymentID,
			PaymentMethod:    plan.TargetMethod,
			SwitchedToCOD:    plan.SwitchedToCOD,
			PayURL:           payURL,
			PaymentExpiresAt: finalExpiresAt,
			Orders:           updatedAggs,
		}
		return nil
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	if switchedToCOD {
		// Confirm reserved stock immediately for the newly confirmed COD group; pending_stock_confirm=TRUE
		// guarantees ScanPendingStockConfirms retries on any transient failure.
		if cErr := s.confirmGroupStockAndClear(ctx, group.ID); cErr != nil {
			s.logger.WarnContext(ctx, "ConfirmStock after COD switch failed; watchdog will retry",
				"checkout_group_id", group.ID, "error", cErr)
		}
		s.notifyOutbox()
	}

	return retryOut, nil
}

// validateRetryableGroupOrders verifies every sub-order in a checkout group is PENDING, online (VIETQR/ZALOPAY),
// matches expectedPaymentID, has a retryable payment_status, and is unexpired (review2-O P2-7, P3-9).
func validateRetryableGroupOrders(
	aggs []store.OrderAggregate,
	expectedPaymentID string,
	groupExpiresAt *time.Time,
	now time.Time,
) ([]*paymentv1.ShopAllocation, int64, *time.Time, error) {
	if len(aggs) == 0 {
		return nil, 0, nil, status.Error(codes.NotFound, "no orders found for checkout group")
	}
	allocs := make([]*paymentv1.ShopAllocation, 0, len(aggs))
	var totalPayableVND int64
	var lastExp *time.Time
	for _, agg := range aggs {
		ord := agg.Order
		if ord.PaymentID != expectedPaymentID {
			return nil, 0, nil, status.Error(codes.FailedPrecondition, "payment_id does not match sub-order current payment")
		}
		if !statemachine.IsOnlineMethod(ord.PaymentMethod) {
			return nil, 0, nil, status.Error(codes.FailedPrecondition, "only online payment orders can retry payment")
		}
		if statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			return nil, 0, nil, status.Errorf(codes.FailedPrecondition,
				"cannot retry payment when order %s is in status %s", ord.ID, ord.Status)
		}
		if strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") ||
			strings.EqualFold(ord.PaymentStatus, "CANCELLED") ||
			strings.EqualFold(ord.PaymentStatus, "EXPIRED") ||
			strings.EqualFold(ord.PaymentStatus, "REFUNDED") {
			return nil, 0, nil, status.Errorf(codes.FailedPrecondition,
				"cannot retry payment in payment status %s", ord.PaymentStatus)
		}
		exp := ord.PaymentExpiresAt
		if exp == nil {
			exp = groupExpiresAt
		}
		if exp == nil || !exp.After(now) {
			return nil, 0, nil, status.Error(codes.FailedPrecondition, "payment window has expired; please place a new order")
		}
		if lastExp == nil || exp.Before(*lastExp) {
			ts := *exp
			lastExp = &ts
		}
		allocs = append(allocs, buildShopAllocation(ord))
		totalPayableVND += ord.TotalPayableVND
	}
	return allocs, totalPayableVND, lastExp, nil
}

func groupHasFlashSale(aggs []store.OrderAggregate) bool {
	for _, agg := range aggs {
		for _, it := range agg.Items {
			if strings.TrimSpace(it.FlashSaleItemID) != "" {
				return true
			}
		}
	}
	return false
}

func fallbackStr(v, fb string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fb
}
