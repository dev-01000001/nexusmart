package service

import (
	"context"
	"errors"
	"strings"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// batchShipInternalMessage replaces raw store/transport errors, which are only logged server-side.
const batchShipInternalMessage = "shipment could not be created right now; please retry"

// BatchShipOrders enqueues async forward shipments for 1..50 orders belonging to the caller's shop (UC-S15, G4).
// Any order belonging to a different shop rejects the entire batch with PermissionDenied before mutating anything.
func (s *OrderService) BatchShipOrders(ctx context.Context, req *orderv1.BatchShipOrdersRequest) (*orderv1.BatchShipOrdersResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.Service != "" || caller.Role != auth.RoleSeller || caller.ShopID == "" {
		return nil, status.Error(codes.PermissionDenied, "only an authenticated seller can batch ship orders")
	}

	uniqueIDs, err := validateBatchOrderIDs(req.GetOrderIds(), s.batchShipMaxItems())
	if err != nil {
		return nil, err
	}

	existingOrders, err := s.store.ListOrdersByIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, mapStoreError(err)
	}
	existingByID := make(map[string]db.Order, len(existingOrders))
	for _, ord := range existingOrders {
		if ord.ShopID != caller.ShopID {
			return nil, status.Error(codes.PermissionDenied, "cannot batch ship orders belonging to another shop")
		}
		existingByID[ord.ID] = ord
	}

	reqCarrier := strings.TrimSpace(req.GetCarrierCode())
	reqService := strings.TrimSpace(req.GetServiceCode())

	var (
		pickupResolved bool
		fromAddr       *shippingv1.ShipmentAddress
		pickupErr      error
	)
	getPickupAddress := func() (*shippingv1.ShipmentAddress, error) {
		if !pickupResolved {
			fromAddr, pickupErr = s.resolveShopPickupAddress(ctx, caller.ShopID)
			pickupResolved = true
		}
		return fromAddr, pickupErr
	}

	results := make([]*orderv1.BatchShipOrderResult, 0, len(uniqueIDs))
	var queuedCount, skippedCount int32

	for _, orderID := range uniqueIDs {
		ordPreview, exists := existingByID[orderID]
		if !exists {
			skippedCount++
			results = append(results, &orderv1.BatchShipOrderResult{
				OrderId:    orderID,
				Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode: "ORDER_NOT_FOUND",
				Message:    "order not found",
			})
			continue
		}

		// Quick pre-check before touching pickup address if the order is already obviously ineligible.
		// Authoritative validation always runs under orders FOR UPDATE inside PrepareOrderForBatchShip.
		curStatus := statemachine.StringToStatus(ordPreview.Status)
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			if strings.TrimSpace(ordPreview.TrackingCode) == "" && !store.IsOrderShipmentQueued(ordPreview) {
				if _, pErr := getPickupAddress(); pErr != nil {
					code, msg := classifyBatchShipError(pErr)
					skippedCount++
					results = append(results, &orderv1.BatchShipOrderResult{
						OrderId:    orderID,
						OrderCode:  ordPreview.OrderCode,
						Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
						ReasonCode: code,
						Message:    msg,
					})
					continue
				}
			}
		}

		prep, pErr := s.store.PrepareOrderForBatchShip(ctx, orderID, caller.ShopID, reqCarrier, reqService, s.nowFn())
		if pErr != nil {
			skippedCount++
			reason, msg := "INTERNAL_ERROR", batchShipInternalMessage
			if errors.Is(pErr, store.ErrNotFound) {
				reason, msg = "ORDER_NOT_FOUND", "order not found"
			} else {
				s.logger.ErrorContext(ctx, "batch ship prepare failed", "order_id", orderID, "error", pErr)
			}
			results = append(results, &orderv1.BatchShipOrderResult{
				OrderId:    orderID,
				OrderCode:  ordPreview.OrderCode,
				Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode: reason,
				Message:    msg,
			})
			continue
		}
		if prep.SkipReason != "" {
			skippedCount++
			results = append(results, &orderv1.BatchShipOrderResult{
				OrderId:      orderID,
				OrderCode:    prep.OrderCode,
				Outcome:      orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode:   prep.SkipReason,
				Message:      prep.SkipMessage,
				TrackingCode: prep.TrackingCode,
			})
			continue
		}

		pickupAddr, addrErr := getPickupAddress()
		if addrErr != nil {
			clearCtx, clearCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
			_ = s.store.ClearOrderPendingShipmentCancelByToken(clearCtx, orderID, prep.CreatingToken)
			clearCancel()
			code, msg := classifyBatchShipError(addrErr)
			skippedCount++
			results = append(results, &orderv1.BatchShipOrderResult{
				OrderId:    orderID,
				OrderCode:  prep.OrderCode,
				Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode: code,
				Message:    msg,
			})
			continue
		}

		if s.clients.Shipping == nil {
			clearCtx, clearCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
			_ = s.store.ClearOrderPendingShipmentCancelByToken(clearCtx, orderID, prep.CreatingToken)
			clearCancel()
			skippedCount++
			results = append(results, &orderv1.BatchShipOrderResult{
				OrderId:    orderID,
				OrderCode:  prep.OrderCode,
				Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode: "INTERNAL_ERROR",
				Message:    "shipping service unavailable",
			})
			continue
		}

		ord := prep.Order.Order
		orderCarrier := strings.TrimSpace(ord.CarrierCode)
		orderService := strings.TrimSpace(ord.ServiceCode)
		isFallback := strings.EqualFold(orderCarrier, "fallback")

		carrierCode := orderCarrier
		if isFallback || carrierCode == "" {
			carrierCode = reqCarrier
		}
		serviceCode := orderService
		if isFallback || serviceCode == "" {
			serviceCode = reqService
		}

		var codAmountVND int64
		if statemachine.IsCODMethod(ord.PaymentMethod) {
			codAmountVND = ord.TotalPayableVND
		}
		totalWeight, itemsSummary := summarizeOrderParcel(prep.Order.Items)
		toAddr := orderAddressToShipmentAddress(ord.ShippingAddressJSON)

		shipResp, shipErr := s.clients.Shipping.CreateShipment(ctx, &shippingv1.CreateShipmentRequest{
			OrderId:      ord.ID,
			ShopId:       ord.ShopID,
			BuyerId:      ord.BuyerID,
			CarrierCode:  carrierCode,
			ServiceCode:  serviceCode,
			CodAmountVnd: codAmountVND,
			WeightGrams:  totalWeight,
			FromAddress:  pickupAddr,
			ToAddress:    toAddr,
			ItemsSummary: itemsSummary,
			EnqueueOnly:  true,
		})
		if shipErr != nil {
			if code := status.Code(shipErr); code == codes.InvalidArgument || code == codes.FailedPrecondition || code == codes.PermissionDenied {
				clearCtx, clearCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
				_ = s.store.ClearOrderPendingShipmentCancelByToken(clearCtx, ord.ID, prep.CreatingToken)
				clearCancel()
			}
			reason, msg := classifyBatchShipError(shipErr)
			skippedCount++
			results = append(results, &orderv1.BatchShipOrderResult{
				OrderId:    ord.ID,
				OrderCode:  ord.OrderCode,
				Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode: reason,
				Message:    msg,
			})
			continue
		}

		if shipResp.GetQueued() {
			jobID := strings.TrimSpace(shipResp.GetJobId())
			qCtx, qCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
			qOut, qErr := s.store.MarkOrderShipmentQueued(qCtx, store.QueueShipmentParams{
				OrderID:               ord.ID,
				JobID:                 jobID,
				ExpectedCreatingToken: prep.CreatingToken,
				ActorID:               caller.UserID,
				ActorRole:             fallbackStr(caller.Role, auth.RoleSeller),
				Note:                  "Batch shipment creation queued",
				Now:                   s.nowFn(),
			})
			qCancel()
			if qErr != nil {
				if errors.Is(qErr, store.ErrOrphanShipmentNeedsCancel) {
					s.compensateOrphanForwardShipment(ctx, ord.ID, "order cancelled concurrently during batch ship")
					skippedCount++
					results = append(results, &orderv1.BatchShipOrderResult{
						OrderId:    ord.ID,
						OrderCode:  ord.OrderCode,
						Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
						ReasonCode: "ORDER_CANCELLED",
						Message:    "order was cancelled concurrently while queueing shipment",
					})
					continue
				}
				s.logger.ErrorContext(ctx, "batch ship queue state update failed", "order_id", ord.ID, "error", qErr)
				skippedCount++
				results = append(results, &orderv1.BatchShipOrderResult{
					OrderId:    ord.ID,
					OrderCode:  ord.OrderCode,
					Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
					ReasonCode: "INTERNAL_ERROR",
					Message:    batchShipInternalMessage,
				})
				continue
			}

			queuedCount++
			res := &orderv1.BatchShipOrderResult{
				OrderId:       ord.ID,
				OrderCode:     ord.OrderCode,
				Outcome:       orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_QUEUED,
				ShipmentJobId: jobID,
			}
			if qOut != nil && qOut.AlreadyAttached && qOut.Order != nil {
				res.TrackingCode = qOut.Order.Order.TrackingCode
			}
			results = append(results, res)
			continue
		}

		// Shipping returned an existing live shipment synchronously (already_existed=true).
		ship := shipResp.GetShipment()
		trk := strings.TrimSpace(ship.GetTrackingCode())
		updCtx, updCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
		_, updErr := s.store.TransitionOrderPreparedForShipment(updCtx, store.ShipOrderParams{
			OrderID:               ord.ID,
			CarrierCode:           fallbackStr(strings.TrimSpace(ship.GetCarrierCode()), carrierCode),
			ServiceCode:           fallbackStr(strings.TrimSpace(ship.GetServiceCode()), serviceCode),
			TrackingCode:          trk,
			LabelURL:              strings.TrimSpace(ship.GetLabelUrl()),
			ExpectedCreatingToken: prep.CreatingToken,
			ActorID:               caller.UserID,
			ActorRole:             fallbackStr(caller.Role, auth.RoleSeller),
			Note:                  "Existing shipment synced during batch ship",
			Now:                   s.nowFn(),
		})
		updCancel()
		if updErr != nil {
			if errors.Is(updErr, store.ErrOrphanShipmentNeedsCancel) {
				s.compensateOrphanForwardShipment(ctx, ord.ID, "order cancelled concurrently during batch ship")
				skippedCount++
				results = append(results, &orderv1.BatchShipOrderResult{
					OrderId:    ord.ID,
					OrderCode:  ord.OrderCode,
					Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
					ReasonCode: "ORDER_CANCELLED",
					Message:    "order was cancelled concurrently during batch ship",
				})
				continue
			}
			s.logger.ErrorContext(ctx, "batch ship shipment sync failed", "order_id", ord.ID, "error", updErr)
			skippedCount++
			results = append(results, &orderv1.BatchShipOrderResult{
				OrderId:    ord.ID,
				OrderCode:  ord.OrderCode,
				Outcome:    orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
				ReasonCode: "INTERNAL_ERROR",
				Message:    batchShipInternalMessage,
			})
			continue
		}
		skippedCount++
		results = append(results, &orderv1.BatchShipOrderResult{
			OrderId:      ord.ID,
			OrderCode:    ord.OrderCode,
			Outcome:      orderv1.BatchShipOrderOutcome_BATCH_SHIP_ORDER_OUTCOME_SKIPPED,
			ReasonCode:   "ALREADY_HAS_SHIPMENT",
			Message:      "order already has an active shipment",
			TrackingCode: trk,
		})
	}

	return &orderv1.BatchShipOrdersResponse{
		Results:      results,
		QueuedCount:  queuedCount,
		SkippedCount: skippedCount,
	}, nil
}

// GetPickingList aggregates SKU pick quantities across 1..50 orders belonging to the caller's shop (UC-S15, G4).
func (s *OrderService) GetPickingList(ctx context.Context, req *orderv1.GetPickingListRequest) (*orderv1.GetPickingListResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.Service != "" || caller.Role != auth.RoleSeller || caller.ShopID == "" {
		return nil, status.Error(codes.PermissionDenied, "only an authenticated seller can view picking list")
	}

	uniqueIDs, err := validateBatchOrderIDs(req.GetOrderIds(), s.batchShipMaxItems())
	if err != nil {
		return nil, err
	}

	existingOrders, err := s.store.ListOrdersByIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, mapStoreError(err)
	}
	matchedOrders := int32(0)
	for _, ord := range existingOrders {
		if ord.ShopID != caller.ShopID {
			return nil, status.Error(codes.PermissionDenied, "cannot view picking list for orders belonging to another shop")
		}
		matchedOrders++
	}

	rows, err := s.store.GetPickingListByOrderIDs(ctx, caller.ShopID, uniqueIDs)
	if err != nil {
		return nil, mapStoreError(err)
	}

	items := make([]*orderv1.PickingListItem, 0, len(rows))
	var totalUnits int32
	for _, r := range rows {
		totalUnits += r.TotalQuantity
		items = append(items, &orderv1.PickingListItem{
			SkuId:         r.SKUID,
			ProductId:     r.ProductID,
			ProductName:   r.ProductName,
			SkuName:       r.SKUName,
			TotalQuantity: r.TotalQuantity,
			OrderCount:    r.OrderCount,
			OrderCodes:    r.OrderCodes,
		})
	}

	return &orderv1.GetPickingListResponse{
		Items:      items,
		OrderCount: matchedOrders,
		TotalUnits: totalUnits,
	}, nil
}

func validateBatchOrderIDs(raw []string, maxItems int) ([]string, error) {
	if len(raw) == 0 || len(raw) > maxItems {
		return nil, status.Errorf(codes.InvalidArgument, "order_ids must contain between 1 and %d items", maxItems)
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return nil, status.Error(codes.InvalidArgument, "order_ids must not contain empty values")
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}

func classifyBatchShipError(err error) (reasonCode string, message string) {
	if err == nil {
		return "INTERNAL_ERROR", ""
	}
	msg := status.Convert(err).Message()
	upper := strings.ToUpper(msg)
	switch {
	case strings.Contains(upper, "SHOP_PICKUP_ADDRESS_REQUIRED"):
		return "SHOP_PICKUP_ADDRESS_REQUIRED", msg
	case strings.Contains(upper, "UNSUPPORTED_WARD") ||
		(strings.Contains(upper, "WARD") && (strings.Contains(upper, "UNSUPPORTED") || strings.Contains(upper, "NOT FOUND"))):
		return "UNSUPPORTED_WARD", msg
	case strings.Contains(upper, "PAYMENT_NOT_INITIALIZED"):
		return "PAYMENT_NOT_INITIALIZED", msg
	case strings.Contains(upper, "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS"):
		return "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS", msg
	}
	code := status.Code(err)
	if code == codes.InvalidArgument || code == codes.FailedPrecondition {
		return "CARRIER_REJECTED", msg
	}
	return "INTERNAL_ERROR", batchShipInternalMessage
}
