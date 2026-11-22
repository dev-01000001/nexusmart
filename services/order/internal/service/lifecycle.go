package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
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

const returnWindowDuration = 7 * 24 * time.Hour

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

	if !canViewOrCancelOrder(caller, agg.Order) {
		return nil, status.Error(codes.PermissionDenied, "access denied to this order")
	}

	return &orderv1.GetOrderResponse{
		Order: aggregateToProto(*agg),
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

	if !canViewOrCancelOrder(caller, agg.Order) {
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

	var buyerFilter, shopFilter string

	switch scope {
	case "admin":
		if !caller.IsPrivileged {
			return nil, status.Error(codes.PermissionDenied, "admin role required to list all orders")
		}
		buyerFilter = strings.TrimSpace(req.GetBuyerId())
		shopFilter = strings.TrimSpace(req.GetShopId())
	case "seller":
		if caller.IsPrivileged {
			shopFilter = strings.TrimSpace(req.GetShopId())
			buyerFilter = strings.TrimSpace(req.GetBuyerId())
		} else {
			if caller.ShopID == "" {
				return nil, status.Error(codes.PermissionDenied, "seller shop identity is required")
			}
			if req.GetShopId() != "" && req.GetShopId() != caller.ShopID {
				return nil, status.Error(codes.PermissionDenied, "cannot list orders for another shop")
			}
			shopFilter = caller.ShopID
		}
	default:
		if caller.IsPrivileged && req.GetBuyerId() == "" && req.GetShopId() != "" {
			shopFilter = strings.TrimSpace(req.GetShopId())
		} else if caller.IsPrivileged && scope == "" && req.GetBuyerId() == "" {
			// Admin with no explicit filter sees all orders.
			buyerFilter = ""
			shopFilter = ""
		} else {
			if caller.UserID == "" {
				return nil, status.Error(codes.Unauthenticated, "buyer identity is required")
			}
			if !caller.IsPrivileged && req.GetBuyerId() != "" && req.GetBuyerId() != caller.UserID {
				return nil, status.Error(codes.PermissionDenied, "cannot list orders for another buyer")
			}
			buyerFilter = caller.UserID
			if caller.IsPrivileged && req.GetBuyerId() != "" {
				buyerFilter = strings.TrimSpace(req.GetBuyerId())
			}
		}
	}

	var statusFilter string
	if req.GetStatus() != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED {
		statusFilter = statemachine.StatusToString(req.GetStatus())
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
	offset := (page - 1) * pageSize

	aggs, total, err := s.store.ListOrdersFiltered(ctx, buyerFilter, shopFilter, statusFilter, pageSize, offset)
	if err != nil {
		return nil, mapStoreError(err)
	}

	orders := make([]*orderv1.Order, 0, len(aggs))
	for _, agg := range aggs {
		orders = append(orders, aggregateToProto(agg))
	}

	return &orderv1.ListOrdersResponse{
		Orders: orders,
		Total:  total,
	}, nil
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

	if !canViewOrCancelOrder(caller, agg.Order) {
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

	wasPaid := statemachine.StringToPaymentMethod(agg.Order.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
		strings.EqualFold(agg.Order.PaymentStatus, "SUCCEEDED")

	traceID, traceparent := traceMetaFromContext(ctx)
	updatedAgg, cancelledOrders, err := s.store.TransitionOrderStatus(ctx, store.SingleOrderTransitionParams{
		OrderID:         orderID,
		TargetStatus:    orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		CancelReason:    reason,
		ActorID:         caller.UserID,
		ActorRole:       fallbackStr(caller.Role, "buyer"),
		Note:            reason,
		CancelGroupPend: true,
		BuildOutboxFn: func(ord db.Order) []db.InsertOutboxEventParams {
			ordPaid := statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
				strings.EqualFold(ord.PaymentStatus, "SUCCEEDED")
			payload, _ := json.Marshal(events.OrderCancelledPayload{
				OrderID:   ord.ID,
				OrderCode: ord.OrderCode,
				BuyerID:   ord.BuyerID,
				ShopID:    ord.ShopID,
				Reason:    reason,
				WasPaid:   ordPaid,
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
	if err != nil {
		return nil, mapStoreError(err)
	}

	// Execute downstream compensations cleanly.
	s.compensateCancelledOrder(ctx, agg, prevStatus, wasPaid, reason, len(cancelledOrders))
	s.notifyOutbox()

	return &orderv1.CancelOrderResponse{
		Order: aggregateToProto(*updatedAgg),
	}, nil
}

func (s *OrderService) compensateCancelledOrder(
	ctx context.Context,
	agg *store.OrderAggregate,
	prevStatus orderv1.OrderStatus,
	wasPaid bool,
	reason string,
	_ int,
) {
	groupID := agg.Order.CheckoutGroupID
	allGroupOrders, err := s.store.ListOrdersByCheckoutGroup(ctx, groupID)
	hasActiveSibling := false
	if err == nil {
		for _, sibling := range allGroupOrders {
			if sibling.Order.ID != agg.Order.ID &&
				statemachine.StringToStatus(sibling.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				hasActiveSibling = true
				break
			}
		}
	}

	compCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()

	if prevStatus == orderv1.OrderStatus_ORDER_STATUS_PENDING {
		s.compensateReleaseStock(ctx, groupID)
		s.compensateReleasePromotion(ctx, groupID)
		if agg.Order.PaymentID != "" && s.clients.Payment != nil {
			_ = clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
				_, cErr := s.clients.Payment.CancelPayment(c, &paymentv1.CancelPaymentRequest{
					PaymentId:    agg.Order.PaymentID,
					OrderGroupId: groupID,
					Reason:       reason,
				})
				return cErr
			})
		}
		return
	}

	// Order was CONFIRMED or PREPARING.
	if !hasActiveSibling {
		s.compensateReleasePromotion(ctx, groupID)
		releasedViaReservation := false
		if s.clients.Product != nil {
			resp, rErr := s.clients.Product.ReleaseStock(compCtx, &productv1.ReleaseStockRequest{OrderId: groupID})
			if rErr == nil && resp != nil && resp.GetReleased() {
				releasedViaReservation = true
			}
		}
		if !releasedViaReservation {
			s.restockOrderItems(compCtx, agg.Order.ID, agg.Items)
		}
	} else {
		// Only restock this specific cancelled sub-order so active sibling sub-orders keep their stock.
		s.restockOrderItems(compCtx, agg.Order.ID, agg.Items)
	}

	if s.clients.Payment != nil && agg.Order.PaymentID != "" {
		if wasPaid {
			if err := clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
				_, rErr := s.clients.Payment.CreateRefund(c, &paymentv1.CreateRefundRequest{
					PaymentId:      agg.Order.PaymentID,
					OrderId:        agg.Order.ID,
					AmountVnd:      agg.Order.TotalPayableVND,
					Reason:         reason,
					IdempotencyKey: "order-cancel-refund:" + agg.Order.ID,
				})
				return rErr
			}); err != nil {
				s.logger.ErrorContext(ctx, "failed to create refund on cancelled paid order",
					"order_id", agg.Order.ID, "payment_id", agg.Order.PaymentID, "error", err)
			}
		} else if !hasActiveSibling {
			_ = clients.RetryIdempotent(compCtx, 3, func(c context.Context) error {
				_, cErr := s.clients.Payment.CancelPayment(c, &paymentv1.CancelPaymentRequest{
					PaymentId:    agg.Order.PaymentID,
					OrderGroupId: groupID,
					Reason:       reason,
				})
				return cErr
			})
		}
	}
}

func (s *OrderService) restockOrderItems(ctx context.Context, orderID string, items []db.OrderItem) {
	if s.clients.Product == nil || strings.TrimSpace(orderID) == "" || len(items) == 0 {
		return
	}
	qtyBySKU := make(map[string]int32, len(items))
	for _, it := range items {
		skuID := strings.TrimSpace(it.SKUID)
		if skuID != "" && it.Quantity > 0 {
			qtyBySKU[skuID] += it.Quantity
		}
	}
	if len(qtyBySKU) == 0 {
		return
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

	if err := clients.RetryIdempotent(ctx, 3, func(c context.Context) error {
		_, rErr := s.clients.Product.RestockOrderItems(c, &productv1.RestockOrderItemsRequest{
			OrderId: orderID,
			Items:   reqItems,
		})
		return rErr
	}); err != nil {
		s.logger.WarnContext(ctx, "restock RestockOrderItems failed", "order_id", orderID, "error", err)
	}
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

	updatedAgg, _, err := s.store.TransitionOrderStatus(ctx, store.SingleOrderTransitionParams{
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
		Order: aggregateToProto(*updatedAgg),
	}, nil
}

// SellerShipOrder creates a shipment via ShippingService and transitions CONFIRMED|PREPARING -> SHIPPING.
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
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_SHIPPING); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	carrierCode := strings.TrimSpace(req.GetCarrierCode())
	if carrierCode == "" {
		carrierCode = agg.Order.CarrierCode
	}
	serviceCode := strings.TrimSpace(req.GetServiceCode())
	if serviceCode == "" {
		serviceCode = agg.Order.ServiceCode
	}

	var codAmountVND int64
	if statemachine.StringToPaymentMethod(agg.Order.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD {
		codAmountVND = agg.Order.TotalPayableVND
	}

	var totalWeight int32
	summaryParts := make([]string, 0, len(agg.Items))
	for _, it := range agg.Items {
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

	var toSnapshot orderv1.OrderAddressSnapshot
	_ = json.Unmarshal(agg.Order.ShippingAddressJSON, &toSnapshot)
	toAddr := &shippingv1.ShipmentAddress{
		Name:          toSnapshot.GetRecipientName(),
		Phone:         toSnapshot.GetRecipientPhone(),
		ProvinceCode:  toSnapshot.GetProvinceCode(),
		WardCode:      toSnapshot.GetWardCode(),
		StreetAddress: toSnapshot.GetStreetAddress(),
	}
	fromAddr := s.resolveShopPickupAddress(ctx, agg.Order.ShopID, toAddr)

	shipResp, err := s.clients.Shipping.CreateShipment(ctx, &shippingv1.CreateShipmentRequest{
		OrderId:      agg.Order.ID,
		ShopId:       agg.Order.ShopID,
		CarrierCode:  carrierCode,
		ServiceCode:  serviceCode,
		CodAmountVnd: codAmountVND,
		WeightGrams:  totalWeight,
		FromAddress:  fromAddr,
		ToAddress:    toAddr,
		ItemsSummary: strings.Join(summaryParts, ", "),
	})
	if err != nil {
		return nil, err
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

	traceID, traceparent := traceMetaFromContext(ctx)
	outboxPayload, err := json.Marshal(events.OrderShippedPayload{
		OrderID:      agg.Order.ID,
		OrderCode:    agg.Order.OrderCode,
		BuyerID:      agg.Order.BuyerID,
		ShopID:       agg.Order.ShopID,
		CarrierCode:  carrierCode,
		TrackingCode: trackingCode,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal order.shipped event: %v", err)
	}

	updatedAgg, err := s.store.TransitionOrderShipped(ctx, store.ShipOrderParams{
		OrderID:      agg.Order.ID,
		CarrierCode:  carrierCode,
		ServiceCode:  serviceCode,
		TrackingCode: trackingCode,
		LabelURL:     labelURL,
		ActorID:      fallbackStr(caller.UserID, req.GetSellerUserId()),
		ActorRole:    fallbackStr(caller.Role, "seller"),
		Note:         fmt.Sprintf("Shipped via %s (tracking: %s)", carrierCode, trackingCode),
		OutboxEvent: db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderShipped,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: outboxPayload,
		},
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	s.notifyOutbox()
	return &orderv1.SellerShipOrderResponse{
		Order:        aggregateToProto(*updatedAgg),
		TrackingCode: trackingCode,
		LabelUrl:     labelURL,
	}, nil
}

func (s *OrderService) resolveShopPickupAddress(ctx context.Context, shopID string, fallback *shippingv1.ShipmentAddress) *shippingv1.ShipmentAddress {
	res := &shippingv1.ShipmentAddress{
		Name:          "Shop " + shopID,
		Phone:         "0900000000",
		ProvinceCode:  fallback.GetProvinceCode(),
		WardCode:      fallback.GetWardCode(),
		StreetAddress: "Pickup Warehouse",
	}
	if s.clients.User == nil {
		return res
	}
	shopResp, err := s.clients.User.GetShop(ctx, &userv1.GetShopRequest{ShopId: shopID})
	if err != nil || shopResp.GetShop() == nil {
		return res
	}
	shop := shopResp.GetShop()
	if shop.GetName() != "" {
		res.Name = shop.GetName()
	}
	if shop.GetContactPhone() != "" {
		res.Phone = shop.GetContactPhone()
	}
	if shop.GetPickupAddressId() == "" || shop.GetOwnerUserId() == "" {
		return res
	}
	addrResp, err := s.clients.User.GetAddress(clients.WithInternalAdminContext(ctx), &userv1.GetAddressRequest{
		UserId:    shop.GetOwnerUserId(),
		AddressId: shop.GetPickupAddressId(),
	})
	if err != nil || addrResp.GetAddress() == nil {
		return res
	}
	a := addrResp.GetAddress()
	if a.GetRecipientName() != "" {
		res.Name = a.GetRecipientName()
	}
	if a.GetRecipientPhone() != "" {
		res.Phone = a.GetRecipientPhone()
	}
	if a.GetProvinceCode() != "" {
		res.ProvinceCode = a.GetProvinceCode()
	}
	if a.GetWardCode() != "" {
		res.WardCode = a.GetWardCode()
	}
	if a.GetStreetAddress() != "" {
		res.StreetAddress = a.GetStreetAddress()
	}
	return res
}

// BuyerConfirmReceived transitions DELIVERED -> COMPLETED, emits order.completed outbox, and releases payment escrow.
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
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_COMPLETED); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	coinsReward := ComputeRewardCoins(agg.Order.SubtotalVND, s.cfg.RewardCoinsBPS)
	traceID, traceparent := traceMetaFromContext(ctx)
	payloadBytes, err := json.Marshal(events.OrderCompletedPayload{
		OrderID:         agg.Order.ID,
		OrderCode:       agg.Order.OrderCode,
		BuyerID:         agg.Order.BuyerID,
		ShopID:          agg.Order.ShopID,
		SubtotalVND:     agg.Order.SubtotalVND,
		CommissionVND:   agg.Order.CommissionVND,
		TotalPayableVND: agg.Order.TotalPayableVND,
		CoinsReward:     coinsReward,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal order.completed event: %v", err)
	}

	updatedAgg, err := s.store.TransitionOrderCompleted(ctx, store.CompleteOrderParams{
		OrderID:   agg.Order.ID,
		ActorID:   fallbackStr(caller.UserID, agg.Order.BuyerID),
		ActorRole: fallbackStr(caller.Role, "buyer"),
		Note:      "Buyer confirmed receipt of order",
		OutboxEvent: db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderCompleted,
			TraceID:     traceID,
			Traceparent: traceparent,
			PayloadJSON: payloadBytes,
		},
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	// Best-effort direct ReleaseEscrow alongside idempotent order.completed event.
	if s.clients.Payment != nil {
		relCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		defer cancel()
		if err := clients.RetryIdempotent(relCtx, 3, func(c context.Context) error {
			_, rErr := s.clients.Payment.ReleaseEscrow(c, &paymentv1.ReleaseEscrowRequest{
				OrderId: agg.Order.ID,
				ShopId:  agg.Order.ShopID,
			})
			return rErr
		}); err != nil {
			s.logger.WarnContext(ctx, "direct ReleaseEscrow call failed (will rely on order.completed event)",
				"order_id", agg.Order.ID, "error", err)
		}
	}

	s.notifyOutbox()
	return &orderv1.BuyerConfirmReceivedResponse{
		Order: aggregateToProto(*updatedAgg),
	}, nil
}

// RequestReturn transitions DELIVERED -> RETURN_REQUESTED and persists a return_requests record.
func (s *OrderService) RequestReturn(ctx context.Context, req *orderv1.RequestReturnRequest) (*orderv1.RequestReturnResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, status.Error(codes.InvalidArgument, "return reason is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	if !canActAsBuyer(caller, agg.Order, req.GetBuyerId()) {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order can request a return")
	}

	if agg.Order.DeliveredAt != nil && s.nowFn().Sub(*agg.Order.DeliveredAt) > returnWindowDuration {
		return nil, status.Error(codes.FailedPrecondition, "return request window (7 days) has expired")
	}

	evidence := req.GetEvidenceUrls()
	if evidence == nil {
		evidence = []string{}
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal evidence_urls: %v", err)
	}

	returnID := uuid.NewString()
	updatedAgg, _, err := s.store.TransitionOrderReturnRequested(ctx, store.ReturnRequestParams{
		OrderID:          agg.Order.ID,
		BuyerID:          agg.Order.BuyerID,
		ReturnRequestID:  returnID,
		Reason:           reason,
		EvidenceURLsJSON: evidenceJSON,
		ActorID:          fallbackStr(caller.UserID, agg.Order.BuyerID),
		ActorRole:        fallbackStr(caller.Role, "buyer"),
		Note:             "Return requested: " + reason,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	return &orderv1.RequestReturnResponse{
		Order:           aggregateToProto(*updatedAgg),
		ReturnRequestId: returnID,
	}, nil
}

// MarkPaymentResult handles explicit internal payment result notifications alongside NATS events.
func (s *OrderService) MarkPaymentResult(ctx context.Context, req *orderv1.MarkPaymentResultRequest) (*orderv1.MarkPaymentResultResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if !caller.IsPrivileged && caller.Role != "system" {
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

func fallbackStr(v, fb string) string {
	if strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fb
}
