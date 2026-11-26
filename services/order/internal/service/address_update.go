package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	paymentv1 "github.com/nexusmart/core/gen/go/nexusmart/payment/v1"
	shippingv1 "github.com/nexusmart/core/gen/go/nexusmart/shipping/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/services/order/internal/clients"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/statemachine"
	"github.com/nexusmart/core/services/order/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	addressChangeLeaseDuration     = 30 * time.Second
	addressChangeLeaseSafetyMargin = 5 * time.Second
	addressChangeBaseBackoff       = 5 * time.Second
	addressChangeMaxBackoff        = 5 * time.Minute
	// An open intent blocks manual shipping, so after this many transient failures (about 35 minutes of backoff)
	// the shipment is handed back to the seller instead of being retried forever.
	addressChangeMaxShipmentAttempts = 12

	// addressChangeStepRestoreOld is the crash-test hook fired after RESTORING_SHIPMENT is persisted.
	addressChangeStepRestoreOld = 6
)

func (s *OrderService) addrChangeLease() time.Duration {
	if s.addressChangeLease > 0 {
		return s.addressChangeLease
	}
	return addressChangeLeaseDuration
}

func (s *OrderService) addrChangeLeaseSafety() time.Duration {
	if s.addressChangeLeaseSafety > 0 {
		return s.addressChangeLeaseSafety
	}
	return addressChangeLeaseSafetyMargin
}

// prepareAddressChangeRPCContext fences a side-effecting downstream RPC in UpdateOrderAddress:
//  1. Captures real monotonic start before renewing the intent's lease in DB guarded by claim_token and non-terminal state;
//     0 rows -> ErrAddressChangeLostLease.
//  2. Derives a detached internal-admin context via WithDeadline(base, start.Add(min(GRPCCallTimeout, lease - safetyMargin)));
//     if the deadline has already elapsed during/after Renew (e.g. VM/DB stall), returns ErrAddressChangeLostLease without calling RPC (O1).
func (s *OrderService) prepareAddressChangeRPCContext(
	parent context.Context,
	ch *db.OrderAddressChange,
) (context.Context, context.CancelFunc, error) {
	lease := s.addrChangeLease()
	safety := s.addrChangeLeaseSafety()
	start := time.Now()
	now := s.nowFn()

	renewCtx, renewCancel := detachedCompensationContext(parent, s.cfg.GRPCCallTimeout)
	renewed, err := s.store.RenewOrderAddressChangeLease(renewCtx, ch.ID, ch.ClaimToken, now, lease)
	renewCancel()
	if err != nil {
		return nil, nil, err
	}
	*ch = *renewed

	if renewed.ClaimedUntil == nil {
		return nil, nil, store.ErrAddressChangeLostLease
	}
	budget := renewed.ClaimedUntil.Sub(now) - safety
	if budget <= 0 {
		return nil, nil, store.ErrAddressChangeLostLease
	}
	timeout := budget
	if s.cfg.GRPCCallTimeout > 0 && s.cfg.GRPCCallTimeout < timeout {
		timeout = s.cfg.GRPCCallTimeout
	}

	deadline := start.Add(timeout)
	if !deadline.After(time.Now()) {
		return nil, nil, store.ErrAddressChangeLostLease
	}

	base := clients.WithInternalAdminContext(context.WithoutCancel(parent))
	rpcCtx, cancel := context.WithDeadline(base, deadline)
	return rpcCtx, cancel, nil
}

func isUncollectedCODOrder(ord db.Order) bool {
	return statemachine.IsCODMethod(ord.PaymentMethod) &&
		!strings.EqualFold(strings.TrimSpace(ord.PaymentStatus), "SUCCEEDED")
}

// UpdateOrderAddress lets the owning buyer preview or apply a shipping address change before carrier pickup (UC-B26, G1-G3).
func (s *OrderService) UpdateOrderAddress(ctx context.Context, req *orderv1.UpdateOrderAddressRequest) (*orderv1.UpdateOrderAddressResponse, error) {
	caller, err := requireAuthenticatedCaller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service tokens cannot call UpdateOrderAddress")
	}

	orderID := strings.TrimSpace(req.GetOrderId())
	if orderID == "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}

	agg, err := s.store.GetOrderWithDetails(ctx, orderID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if caller.UserID == "" || caller.UserID != agg.Order.BuyerID {
		return nil, status.Error(codes.PermissionDenied, "only the buyer who owns this order can update its shipping address")
	}

	resolvedAddr, err := s.resolveUpdatedBuyerAddress(ctx, caller.UserID, req.GetAddressId())
	if err != nil {
		return nil, err
	}
	newAddrJSON, err := json.Marshal(resolvedAddr)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal resolved address: %v", err)
	}
	requestHash := computeAddressChangeRequestHash(resolvedAddr)

	idemKey := strings.TrimSpace(req.GetIdempotencyKey())
	if !req.GetPreviewOnly() && idemKey != "" {
		existing, gErr := s.store.GetOrderAddressChangeByOrderAndIdemKey(ctx, orderID, idemKey)
		if gErr == nil {
			return s.handleExistingAddressChange(ctx, existing, requestHash, caller)
		}
		if !errors.Is(gErr, store.ErrNotFound) {
			return nil, mapStoreError(gErr)
		}
	}

	curStatus := statemachine.StringToStatus(agg.Order.Status)
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return nil, status.Errorf(codes.FailedPrecondition,
			"ADDRESS_UPDATE_NOT_ALLOWED: cannot update address when order status is %s",
			statemachine.StatusToString(curStatus))
	}
	if store.IsOrderShipmentQueued(agg.Order) || agg.Order.PendingShipmentCancel {
		return nil, status.Error(codes.FailedPrecondition, store.ErrAddressChangeInProgress.Error())
	}
	// O6(a): Reject both same-address fast-path and preview_only while another address change intent is open.
	hasOpenChange, err := s.store.HasOpenOrderAddressChange(ctx, agg.Order.ID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	if hasOpenChange {
		return nil, status.Error(codes.FailedPrecondition, store.ErrAddressChangeInProgress.Error())
	}
	if err := s.requireCheckoutPaymentInitialized(ctx, agg.Order.CheckoutGroupID); err != nil {
		return nil, err
	}

	// Picking the address the order already has must not cancel and recreate its shipment.
	current := &orderv1.OrderAddressSnapshot{}
	if json.Unmarshal(agg.Order.ShippingAddressJSON, current) == nil && computeAddressChangeRequestHash(current) == requestHash {
		return &orderv1.UpdateOrderAddressResponse{
			Order:              aggregateToProtoForCaller(*agg, caller),
			OldShippingFeeVnd:  agg.Order.ShippingFeeVND,
			NewShippingFeeVnd:  agg.Order.ShippingFeeVND,
			OldTotalPayableVnd: agg.Order.TotalPayableVND,
			NewTotalPayableVnd: agg.Order.TotalPayableVND,
			PreviewOnly:        req.GetPreviewOnly(),
			ResolvedAddress:    resolvedAddr,
		}, nil
	}

	if s.clients.Shipping == nil {
		return nil, status.Error(codes.Unavailable, "shipping service unavailable")
	}
	fromAddr, err := s.resolveShopPickupAddress(ctx, agg.Order.ShopID)
	if err != nil {
		return nil, err
	}

	explicitWeight, fallbackQty := summarizeOrderFeeWeight(agg.Items)
	// O4: Already-settled COD orders (0-VND COD with payment_status=SUCCEEDED) behave like prepaid:
	// no COD collection at carrier, fee increases blocked before cancelling shipment, equal/decreases keep totals unchanged.
	isCOD := isUncollectedCODOrder(agg.Order)
	var codAmountForQuote int64
	if isCOD {
		codAmountForQuote = agg.Order.TotalPayableVND
	}

	feeResp, err := s.clients.Shipping.CalculateFee(ctx, &shippingv1.CalculateFeeRequest{
		ShopId:                agg.Order.ShopID,
		FromProvinceCode:      fromAddr.GetProvinceCode(),
		FromWardCode:          fromAddr.GetWardCode(),
		ToProvinceCode:        resolvedAddr.GetProvinceCode(),
		ToWardCode:            resolvedAddr.GetWardCode(),
		WeightGrams:           explicitWeight,
		FallbackWeightItemQty: fallbackQty,
		CodAmountVnd:          codAmountForQuote,
	})
	if err != nil {
		return nil, err
	}
	quotes := feeResp.GetQuotes()
	if len(quotes) == 0 {
		return nil, status.Error(codes.FailedPrecondition, "SHIPPING_QUOTE_UNAVAILABLE: không có báo giá vận chuyển cho địa chỉ mới")
	}
	quotePayMethod := ""
	if isCOD {
		quotePayMethod = agg.Order.PaymentMethod
	}
	quote := pickShippingQuote(quotes, &orderv1.ShopShippingChoice{
		CarrierCode: agg.Order.CarrierCode,
		ServiceCode: agg.Order.ServiceCode,
	}, quotePayMethod)
	if quote == nil {
		return nil, status.Error(codes.FailedPrecondition, "SHIPPING_QUOTE_UNAVAILABLE: không có báo giá vận chuyển phù hợp")
	}
	if isCOD && !quote.GetCodSupported() {
		return nil, status.Error(codes.FailedPrecondition, "COD_AREA_NOT_SUPPORTED: địa chỉ mới không hỗ trợ thanh toán COD")
	}

	oldShippingFee := agg.Order.ShippingFeeVND
	newShippingFee := quote.GetFeeVnd()
	feeDelta := newShippingFee - oldShippingFee
	oldTotalPayable := agg.Order.TotalPayableVND

	appliedShippingFee := oldShippingFee
	effectiveNewShippingFee := newShippingFee
	newTotalPayable := oldTotalPayable
	feeAdjusted := false
	needsCODAdjust := false
	requiresFeeConfirmation := false
	blockedPrepaidIncrease := false
	var maxCODGroupTotalVND int64

	if isCOD {
		appliedShippingFee = maxInt64(newShippingFee, store.OrderFreeshipFloorVND(agg.Order))
		effectiveNewShippingFee = appliedShippingFee
		feeDelta = appliedShippingFee - oldShippingFee
		newTotalPayable = oldTotalPayable + feeDelta
		if newTotalPayable < 0 {
			return nil, status.Error(codes.FailedPrecondition,
				"ADDRESS_UPDATE_NOT_ALLOWED: phí vận chuyển mới thấp hơn phần giảm giá đã áp dụng cho đơn")
		}
		feeAdjusted = feeDelta != 0
		needsCODAdjust = feeAdjusted

		if feeDelta > 0 {
			maxCODGroupTotalVND, err = s.validateCODCeilingForAddressChange(ctx, agg.Order, newTotalPayable)
			if err != nil {
				return nil, err
			}
			requiresFeeConfirmation = true
			if !req.GetPreviewOnly() {
				if !req.GetAcceptFeeIncrease() {
					return nil, status.Error(codes.FailedPrecondition,
						"ADDRESS_FEE_CONFIRMATION_REQUIRED: phí vận chuyển tăng, vui lòng xác nhận phí mới")
				}
				if req.GetExpectedNewShippingFeeVnd() != appliedShippingFee {
					return nil, status.Errorf(codes.FailedPrecondition,
						`ADDRESS_FEE_CHANGED: {"old_shipping_fee_vnd":%d,"new_shipping_fee_vnd":%d,"fee_delta_vnd":%d,"new_total_payable_vnd":%d}`,
						oldShippingFee, appliedShippingFee, feeDelta, newTotalPayable)
				}
			}
		}
	} else {
		// Prepaid (VIETQR / ZALOPAY) and already-settled 0-VND COD: no surcharge in 3b-3; equal/decrease keeps total unchanged (G2, O4).
		if feeDelta > 0 {
			blockedPrepaidIncrease = true
			if !req.GetPreviewOnly() {
				return nil, status.Error(codes.FailedPrecondition,
					"ADDRESS_FEE_INCREASE_PREPAID: đơn đã thanh toán trước không hỗ trợ đổi địa chỉ làm tăng phí vận chuyển")
			}
		}
	}

	if req.GetPreviewOnly() {
		return &orderv1.UpdateOrderAddressResponse{
			Order:                   aggregateToProtoForCaller(*agg, caller),
			OldShippingFeeVnd:       oldShippingFee,
			NewShippingFeeVnd:       effectiveNewShippingFee,
			FeeDeltaVnd:             feeDelta,
			OldTotalPayableVnd:      oldTotalPayable,
			NewTotalPayableVnd:      newTotalPayable,
			PreviewOnly:             true,
			FeeAdjusted:             feeAdjusted,
			RequiresFeeConfirmation: requiresFeeConfirmation,
			BlockedPrepaidIncrease:  blockedPrepaidIncrease,
			ResolvedAddress:         resolvedAddr,
		}, nil
	}

	if needsCODAdjust && s.clients.Payment == nil {
		return nil, status.Error(codes.Unavailable, "payment service unavailable")
	}

	// Step 1: Persist durable OPEN intent under orders FOR UPDATE (enforces <= 1 open intent per order and O3 COD ceiling).
	carrierCode := strings.TrimSpace(quote.GetCarrierCode())
	if carrierCode == "" {
		carrierCode = agg.Order.CarrierCode
	}
	serviceCode := strings.TrimSpace(quote.GetServiceCode())
	if serviceCode == "" {
		serviceCode = agg.Order.ServiceCode
	}

	traceID, traceparent := traceMetaFromContext(ctx)
	claimToken := "req:" + uuid.NewString()
	openRes, err := s.store.OpenOrderAddressChange(ctx, store.OpenAddressChangeParams{
		ID:                        uuid.NewString(),
		OrderID:                   agg.Order.ID,
		BuyerID:                   caller.UserID,
		IdempotencyKey:            idemKey,
		RequestHash:               requestHash,
		NewAddressJSON:            newAddrJSON,
		CarrierCode:               carrierCode,
		ServiceCode:               serviceCode,
		ExpectedOldAddressJSON:    agg.Order.ShippingAddressJSON,
		ExpectedOldShippingFeeVND: oldShippingFee,
		ExpectedOldTotalPayable:   oldTotalPayable,
		NewShippingFeeVND:         effectiveNewShippingFee,
		AppliedShippingFeeVND:     appliedShippingFee,
		NewTotalPayableVND:        newTotalPayable,
		FeeAdjusted:               feeAdjusted,
		NeedsCODAdjust:            needsCODAdjust,
		MaxCODGroupTotalVND:       maxCODGroupTotalVND,
		ClaimToken:                claimToken,
		LeaseDuration:             s.addrChangeLease(),
		TraceID:                   traceID,
		Traceparent:               traceparent,
		Now:                       s.nowFn(),
	})
	if err != nil {
		return nil, mapStoreError(err)
	}
	if openRes.Replayed {
		return s.handleExistingAddressChange(ctx, &openRes.Change, requestHash, caller)
	}

	if err := s.invokeAfterAddressChangeStep(1, openRes.Change.ID); err != nil {
		return nil, err
	}

	return s.completeAddressChange(ctx, openRes.Change, fromAddr, caller)
}

// completeAddressChange drives the intent and answers the buyer. Once Step 4 committed, the address and money have
// changed: a failure to recreate the shipment is reported as queued and finished by the watchdog, never as an error.
// If the buyer's request lost its lease to a watchdog (O5), re-reads the intent and returns its terminal outcome when finished,
// or 409 ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS while the watchdog is still processing it.
func (s *OrderService) completeAddressChange(
	ctx context.Context,
	ch db.OrderAddressChange,
	fromAddr *shippingv1.ShipmentAddress,
	caller callerIdentity,
) (*orderv1.UpdateOrderAddressResponse, error) {
	finalChange, finalAgg, err := s.driveAddressChange(ctx, ch, fromAddr)
	if err != nil {
		if errors.Is(err, store.ErrAddressChangeLostLease) {
			if latest, gErr := s.store.GetOrderAddressChangeByID(ctx, ch.ID); gErr == nil {
				switch latest.State {
				case store.AddressChangeStateCompleted:
					if agg, aErr := s.store.GetOrderWithDetails(ctx, latest.OrderID); aErr == nil {
						return buildUpdateOrderAddressResponse(*latest, agg, caller), nil
					}
				case store.AddressChangeStateAborted:
					return nil, abortedAddressChangeError(*latest)
				case store.AddressChangeStateApplied:
					if agg, aErr := s.store.GetOrderWithDetails(ctx, latest.OrderID); aErr == nil {
						respChange := *latest
						respChange.ShipmentQueued = true
						return buildUpdateOrderAddressResponse(respChange, agg, caller), nil
					}
				}
			}
			return nil, mapStoreError(err)
		}
		if finalChange.State != store.AddressChangeStateApplied || finalAgg == nil {
			return nil, mapStoreError(err)
		}
		s.logger.WarnContext(ctx, "address change applied; shipment recreation deferred to watchdog",
			"change_id", finalChange.ID, "order_id", finalChange.OrderID, "error", err)
		finalChange.ShipmentQueued = true
	}
	return buildUpdateOrderAddressResponse(finalChange, finalAgg, caller), nil
}

func (s *OrderService) handleExistingAddressChange(
	ctx context.Context,
	existing *db.OrderAddressChange,
	requestHash string,
	caller callerIdentity,
) (*orderv1.UpdateOrderAddressResponse, error) {
	if existing.RequestHash != requestHash {
		return nil, status.Error(codes.FailedPrecondition, store.ErrAddressChangeIdemConflict.Error())
	}
	switch existing.State {
	case store.AddressChangeStateCompleted:
		agg, err := s.store.GetOrderWithDetails(ctx, existing.OrderID)
		if err != nil {
			return nil, mapStoreError(err)
		}
		return buildUpdateOrderAddressResponse(*existing, agg, caller), nil
	case store.AddressChangeStateAborted:
		return nil, abortedAddressChangeError(*existing)
	default:
		claimToken := "retry:" + uuid.NewString()
		claimed, err := s.store.ClaimOrderAddressChangeByID(ctx, existing.ID, claimToken, s.nowFn(), s.addrChangeLease())
		if err != nil {
			if errors.Is(err, store.ErrAddressChangeLostLease) {
				return nil, status.Error(codes.FailedPrecondition, store.ErrAddressChangeInProgress.Error())
			}
			return nil, mapStoreError(err)
		}
		return s.completeAddressChange(ctx, *claimed, nil, caller)
	}
}

// driveAddressChange advances a claimed order_address_changes intent deterministically through
// Steps 2 -> 3 -> 4 -> 5 (or REVERTING_COD / RESTORING_SHIPMENT -> ABORTED) and is shared by synchronous requests
// and the watchdog.
func (s *OrderService) driveAddressChange(
	ctx context.Context,
	ch db.OrderAddressChange,
	cachedFromAddr *shippingv1.ShipmentAddress,
) (db.OrderAddressChange, *store.OrderAggregate, error) {
	claimToken := ch.ClaimToken
	lease := s.addrChangeLease()

	// Step 2: Cancel existing forward shipment (if any).
	if ch.State == store.AddressChangeStateOpen {
		if ch.HadShipment && !ch.ShipmentCancelled {
			if s.clients.Shipping == nil {
				s.releaseAddressChangeRetry(ctx, ch, "shipping service unavailable")
				return ch, nil, status.Error(codes.Unavailable, "shipping service unavailable")
			}
			cancelCtx, cancelTimeout, leaseErr := s.prepareAddressChangeRPCContext(ctx, &ch)
			if leaseErr != nil {
				return ch, nil, leaseErr
			}
			_, notFound, cErr := s.cancelForwardShipmentWithCtx(cancelCtx, ch.OrderID, "buyer updated shipping address")
			cancelTimeout()
			if cErr != nil && !notFound {
				if status.Code(cErr) == codes.FailedPrecondition {
					aborted, aErr := s.store.AdvanceAddressChangeState(ctx, store.AddressChangeProgressUpdate{
						ID:                 ch.ID,
						ExpectedClaimToken: claimToken,
						State:              store.AddressChangeStateAborted,
						ShipmentCancelled:  false,
						CODAdjusted:        ch.CODAdjusted,
						OutboxEmitted:      ch.OutboxEmitted,
						AbortCode:          "ADDRESS_UPDATE_CARRIER_PICKED_UP",
						LastError:          status.Convert(cErr).Message(),
						ClaimedUntil:       nil,
						ClaimToken:         "",
						Now:                s.nowFn(),
					})
					if aErr != nil {
						return ch, nil, aErr
					}
					return *aborted, nil, abortedAddressChangeError(*aborted)
				}
				s.releaseAddressChangeRetry(ctx, ch, cErr.Error())
				return ch, nil, cErr
			}

			nextLease := s.nowFn().Add(lease)
			upd, err := s.store.AdvanceAddressChangeState(ctx, store.AddressChangeProgressUpdate{
				ID:                 ch.ID,
				ExpectedClaimToken: claimToken,
				State:              store.AddressChangeStateShipmentCancelled,
				ShipmentCancelled:  true,
				CODAdjusted:        ch.CODAdjusted,
				OutboxEmitted:      ch.OutboxEmitted,
				ClaimedUntil:       &nextLease,
				ClaimToken:         claimToken,
				Now:                s.nowFn(),
			})
			if err != nil {
				return ch, nil, err
			}
			ch = *upd
			if err := s.invokeAfterAddressChangeStep(2, ch.ID); err != nil {
				return ch, nil, err
			}
		}
	}

	// Step 3: Adjust COD shipping fee on PaymentService (idempotent by intent ID).
	if ch.State == store.AddressChangeStateOpen || ch.State == store.AddressChangeStateShipmentCancelled {
		if ch.NeedsCODAdjust && !ch.CODAdjusted {
			if s.clients.Payment == nil {
				s.releaseAddressChangeRetry(ctx, ch, "payment service unavailable")
				return ch, nil, status.Error(codes.Unavailable, "payment service unavailable")
			}
			payCtx, cancel, leaseErr := s.prepareAddressChangeRPCContext(ctx, &ch)
			if leaseErr != nil {
				return ch, nil, leaseErr
			}
			pErr := clients.RetryIdempotent(payCtx, 3, func(c context.Context) error {
				_, err := s.clients.Payment.AdjustCODShippingFee(c, &paymentv1.AdjustCODShippingFeeRequest{
					OrderId:           ch.OrderID,
					NewShippingFeeVnd: ch.AppliedShippingFeeVND,
					IdempotencyKey:    ch.ID,
				})
				return err
			})
			cancel()
			if pErr != nil {
				code := status.Code(pErr)
				if code == codes.FailedPrecondition || code == codes.InvalidArgument || code == codes.NotFound {
					return s.rejectAddressChangeAfterPaymentRefusal(ctx, ch, pErr, cachedFromAddr)
				}
				s.releaseAddressChangeRetry(ctx, ch, pErr.Error())
				return ch, nil, pErr
			}

			nextLease := s.nowFn().Add(lease)
			upd, err := s.store.AdvanceAddressChangeState(ctx, store.AddressChangeProgressUpdate{
				ID:                 ch.ID,
				ExpectedClaimToken: claimToken,
				State:              store.AddressChangeStateCODAdjusted,
				ShipmentCancelled:  ch.ShipmentCancelled,
				CODAdjusted:        true,
				OutboxEmitted:      ch.OutboxEmitted,
				ClaimedUntil:       &nextLease,
				ClaimToken:         claimToken,
				Now:                s.nowFn(),
			})
			if err != nil {
				return ch, nil, err
			}
			ch = *upd
			if err := s.invokeAfterAddressChangeStep(3, ch.ID); err != nil {
				return ch, nil, err
			}
		}
	}

	// A refused change whose shipment was already cancelled: recreate the shipment for the unchanged address.
	if ch.State == store.AddressChangeStateRestoringShipment {
		return s.finishAddressChangeShipment(ctx, ch, nil, cachedFromAddr)
	}

	// Step 4: Atomically update orders + checkout_groups + timeline + order.address_updated outbox.
	var latestAgg *store.OrderAggregate
	if ch.State == store.AddressChangeStateOpen ||
		ch.State == store.AddressChangeStateShipmentCancelled ||
		ch.State == store.AddressChangeStateCODAdjusted {
		applyRes, err := s.store.ApplyAddressChangeToOrder(ctx, store.ApplyAddressChangeParams{
			ChangeID:      ch.ID,
			OrderID:       ch.OrderID,
			ClaimToken:    claimToken,
			LeaseDuration: lease,
			Now:           s.nowFn(),
		})
		if err != nil {
			return ch, nil, err
		}
		if applyRes.EmittedOutbox {
			s.notifyOutbox()
		}
		ch = applyRes.Change
		latestAgg = applyRes.Order
		if applyRes.Aborted {
			return ch, nil, abortedAddressChangeError(ch)
		}
		if !applyRes.NeedsCODRevert {
			if err := s.invokeAfterAddressChangeStep(4, ch.ID); err != nil {
				return ch, nil, err
			}
		}
	}

	// Compensation branch: order transitioned (e.g. CancelOrder won) between Step 3 and Step 4.
	if ch.State == store.AddressChangeStateRevertingCOD {
		if ch.CODAdjusted {
			if s.clients.Payment == nil {
				s.releaseAddressChangeRetry(ctx, ch, "payment service unavailable")
				return ch, nil, status.Error(codes.Unavailable, "payment service unavailable")
			}
			revCtx, cancel, leaseErr := s.prepareAddressChangeRPCContext(ctx, &ch)
			if leaseErr != nil {
				return ch, nil, leaseErr
			}
			revErr := clients.RetryIdempotent(revCtx, 3, func(c context.Context) error {
				_, err := s.clients.Payment.AdjustCODShippingFee(c, &paymentv1.AdjustCODShippingFeeRequest{
					OrderId:           ch.OrderID,
					NewShippingFeeVnd: ch.OldShippingFeeVND,
					IdempotencyKey:    ch.ID + ":revert",
				})
				return err
			})
			cancel()
			if revErr != nil {
				code := status.Code(revErr)
				// Payment/allocation already cancelled or settled: nothing left to revert.
				if code != codes.FailedPrecondition && code != codes.NotFound {
					s.releaseAddressChangeRetry(ctx, ch, "revert COD failed: "+revErr.Error())
					return ch, nil, revErr
				}
			}
		}
		aborted, err := s.store.AdvanceAddressChangeState(ctx, store.AddressChangeProgressUpdate{
			ID:                 ch.ID,
			ExpectedClaimToken: claimToken,
			State:              store.AddressChangeStateAborted,
			ShipmentCancelled:  ch.ShipmentCancelled,
			CODAdjusted:        false,
			OutboxEmitted:      ch.OutboxEmitted,
			AbortCode:          fallbackStr(ch.AbortCode, "ADDRESS_UPDATE_NOT_ALLOWED"),
			LastError:          ch.LastError,
			ClaimedUntil:       nil,
			ClaimToken:         "",
			Now:                s.nowFn(),
		})
		if err != nil {
			return ch, nil, err
		}
		return *aborted, nil, abortedAddressChangeError(*aborted)
	}

	// Step 5: Recreate forward shipment via Shipping.CreateShipment using order-service token (G3).
	if ch.State == store.AddressChangeStateApplied {
		return s.finishAddressChangeShipment(ctx, ch, latestAgg, cachedFromAddr)
	}

	if latestAgg == nil {
		var err error
		if latestAgg, err = s.store.GetOrderWithDetails(ctx, ch.OrderID); err != nil {
			return ch, nil, err
		}
	}
	return ch, latestAgg, nil
}

// rejectAddressChangeAfterPaymentRefusal closes an intent PaymentService permanently refused in Step 3. If Step 2 already
// cancelled the shipment of a still-shippable order, the shipment is first recreated for the unchanged address and fee.
func (s *OrderService) rejectAddressChangeAfterPaymentRefusal(
	ctx context.Context,
	ch db.OrderAddressChange,
	refusal error,
	fromAddr *shippingv1.ShipmentAddress,
) (db.OrderAddressChange, *store.OrderAggregate, error) {
	upd := store.AddressChangeProgressUpdate{
		ID:                 ch.ID,
		ExpectedClaimToken: ch.ClaimToken,
		State:              store.AddressChangeStateAborted,
		ShipmentCancelled:  ch.ShipmentCancelled,
		OutboxEmitted:      ch.OutboxEmitted,
		AbortCode:          extractAddressAbortCode(refusal, "ADDRESS_UPDATE_NOT_ALLOWED"),
		LastError:          status.Convert(refusal).Message(),
		Now:                s.nowFn(),
	}
	if ch.ShipmentCancelled {
		agg, err := s.store.GetOrderWithDetails(ctx, ch.OrderID)
		if err != nil {
			s.releaseAddressChangeRetry(ctx, ch, refusal.Error())
			return ch, nil, err
		}
		switch statemachine.StringToStatus(agg.Order.Status) {
		case orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, orderv1.OrderStatus_ORDER_STATUS_PREPARING:
			lease := s.nowFn().Add(s.addrChangeLease())
			upd.State = store.AddressChangeStateRestoringShipment
			upd.ClaimedUntil = &lease
			upd.ClaimToken = ch.ClaimToken
		default:
			upd.AbortCode = "ADDRESS_UPDATE_NOT_ALLOWED"
		}
	}
	next, err := s.store.AdvanceAddressChangeState(ctx, upd)
	if err != nil {
		return ch, nil, err
	}
	if next.State == store.AddressChangeStateAborted {
		return *next, nil, abortedAddressChangeError(*next)
	}
	if err := s.invokeAfterAddressChangeStep(addressChangeStepRestoreOld, next.ID); err != nil {
		return *next, nil, err
	}
	return s.finishAddressChangeShipment(ctx, *next, nil, fromAddr)
}

// finishAddressChangeShipment recreates the forward shipment from the order's freshly reloaded address, fee and carrier
// and closes the intent. APPLIED: the order already carries the new address (Step 5). RESTORING_SHIPMENT: it still carries
// the old one. On a transient failure the intent stays open for the watchdog and (change, order, error) is returned.
func (s *OrderService) finishAddressChangeShipment(
	ctx context.Context,
	ch db.OrderAddressChange,
	agg *store.OrderAggregate,
	fromAddr *shippingv1.ShipmentAddress,
) (db.OrderAddressChange, *store.OrderAggregate, error) {
	if agg == nil {
		var err error
		if agg, err = s.store.GetOrderWithDetails(ctx, ch.OrderID); err != nil {
			return ch, nil, err
		}
	}
	fin := store.FinalizeAddressChangeShipmentParams{
		ChangeID:   ch.ID,
		OrderID:    ch.OrderID,
		ClaimToken: ch.ClaimToken,
	}
	shipErr := s.recreateAddressChangeShipment(ctx, &ch, &agg, fromAddr, &fin)
	if shipErr != nil {
		if errors.Is(shipErr, store.ErrAddressChangeLostLease) {
			return ch, agg, store.ErrAddressChangeLostLease
		}
		code := status.Code(shipErr)
		permanent := code == codes.InvalidArgument || code == codes.FailedPrecondition || code == codes.PermissionDenied
		if !permanent && ch.Attempts < addressChangeMaxShipmentAttempts {
			s.releaseAddressChangeRetry(ctx, ch, shipErr.Error())
			return ch, agg, shipErr
		}
		fin.PermanentFail = true
		fin.LastError = shipErr.Error()
	}

	fin.Now = s.nowFn()
	finCtx, finCancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	finRes, err := s.store.FinalizeAddressChangeShipment(finCtx, fin)
	finCancel()
	if err != nil {
		return ch, agg, err
	}
	if finRes.NeedsOrphanShipmentCancel {
		s.compensateOrphanForwardShipment(ctx, ch.OrderID, "order cancelled concurrently during address-update shipment recreation")
	}
	if finRes.Change.State == store.AddressChangeStateAborted {
		return finRes.Change, finRes.Order, abortedAddressChangeError(finRes.Change)
	}
	return finRes.Change, finRes.Order, nil
}

// recreateAddressChangeShipment resolves pickup address (if needed), renews the DB lease fence, reloads the latest intent
// and order so stale snapshots can never create wrong-address/wrong-COD/orphan shipments (O1), and calls Shipping.CreateShipment
// under a deadline strictly before claimed_until.
func (s *OrderService) recreateAddressChangeShipment(
	ctx context.Context,
	ch *db.OrderAddressChange,
	aggPtr **store.OrderAggregate,
	fromAddr *shippingv1.ShipmentAddress,
	fin *store.FinalizeAddressChangeShipmentParams,
) error {
	if s.clients.Shipping == nil {
		return status.Error(codes.Unavailable, "shipping service unavailable")
	}
	shopID := (*aggPtr).Order.ShopID
	if fromAddr == nil {
		var err error
		if fromAddr, err = s.resolveShopPickupAddress(ctx, shopID); err != nil {
			return err
		}
	}

	// O1: Fence lease right after slow calls (resolveShopPickupAddress) and BEFORE reloading & calling CreateShipment.
	shipCtx, cancel, leaseErr := s.prepareAddressChangeRPCContext(ctx, ch)
	if leaseErr != nil {
		return leaseErr
	}
	defer cancel()
	if ch.State != store.AddressChangeStateApplied && ch.State != store.AddressChangeStateRestoringShipment {
		return store.ErrAddressChangeLostLease
	}

	// O1: Reload the latest order state from DB so CreateShipment always uses fresh address/COD/status.
	freshAgg, err := s.store.GetOrderWithDetails(shipCtx, ch.OrderID)
	if err != nil {
		return err
	}
	*aggPtr = freshAgg
	ord := freshAgg.Order

	// A cancelled order must never get a fresh (orphan) shipment.
	if statemachine.StringToStatus(ord.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		return status.Error(codes.FailedPrecondition, "order cancelled before shipment recreation")
	}
	var codAmountVND int64
	if isUncollectedCODOrder(ord) {
		codAmountVND = ord.TotalPayableVND
	}
	totalWeight, itemsSummary := summarizeOrderParcel(freshAgg.Items)

	resp, err := s.clients.Shipping.CreateShipment(shipCtx, &shippingv1.CreateShipmentRequest{
		OrderId:      ord.ID,
		ShopId:       ord.ShopID,
		BuyerId:      ord.BuyerID,
		CarrierCode:  ord.CarrierCode,
		ServiceCode:  ord.ServiceCode,
		CodAmountVnd: codAmountVND,
		WeightGrams:  totalWeight,
		FromAddress:  fromAddr,
		ToAddress:    orderAddressToShipmentAddress(ord.ShippingAddressJSON),
		ItemsSummary: itemsSummary,
	})
	if err != nil {
		return err
	}
	if resp.GetQueued() {
		fin.Queued = true
		fin.JobID = strings.TrimSpace(resp.GetJobId())
		return nil
	}
	ship := resp.GetShipment()
	fin.CarrierCode = strings.TrimSpace(ship.GetCarrierCode())
	fin.ServiceCode = strings.TrimSpace(ship.GetServiceCode())
	fin.TrackingCode = strings.TrimSpace(ship.GetTrackingCode())
	fin.LabelURL = strings.TrimSpace(ship.GetLabelUrl())
	return nil
}

// ScanPendingAddressChanges claims due open order_address_changes intents ONE AT A TIME via FOR UPDATE SKIP LOCKED
// (up to 20 per tick), checking ctx.Err() before every claim and excluding already-attempted IDs so an errored intent
// is never re-claimed within the same sweep (UC-B26, G3, O1).
func (s *OrderService) ScanPendingAddressChanges(ctx context.Context) (int, error) {
	const maxPerSweep = 20
	handled := 0
	attempted := make([]string, 0, maxPerSweep)
	lease := s.addrChangeLease()

	for i := 0; i < maxPerSweep; i++ {
		if ctx.Err() != nil {
			break
		}
		claimToken := "watchdog:" + uuid.NewString()
		ch, err := s.store.ClaimNextDueOrderAddressChange(ctx, s.nowFn(), lease, claimToken, attempted)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				break
			}
			return handled, err
		}
		attempted = append(attempted, ch.ID)

		finalCh, _, driveErr := s.driveAddressChange(ctx, *ch, nil)
		if driveErr != nil {
			if store.IsAddressChangeTerminal(finalCh.State) {
				handled++
				continue
			}
			s.logger.WarnContext(ctx, "pending address change retry deferred",
				"change_id", ch.ID,
				"order_id", ch.OrderID,
				"state", ch.State,
				"error", driveErr,
			)
			continue
		}
		handled++
	}
	return handled, nil
}

func (s *OrderService) releaseAddressChangeRetry(ctx context.Context, ch db.OrderAddressChange, errMsg string) {
	now := s.nowFn()
	nextAt := now.Add(addressChangeRetryBackoff(ch.Attempts))
	relCtx, cancel := detachedCompensationContext(ctx, s.cfg.GRPCCallTimeout)
	defer cancel()
	_ = s.store.ReleaseAddressChangeWithBackoff(relCtx, ch.ID, ch.ClaimToken, nextAt, errMsg, now)
}

func addressChangeRetryBackoff(attempts int32) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	d := addressChangeBaseBackoff
	for i := int32(0); i < attempts && d < addressChangeMaxBackoff; i++ {
		d *= 2
	}
	if d > addressChangeMaxBackoff {
		return addressChangeMaxBackoff
	}
	return d
}

func (s *OrderService) invokeAfterAddressChangeStep(step int, changeID string) error {
	if s.afterAddressChangeStep != nil {
		return s.afterAddressChangeStep(step, changeID)
	}
	return nil
}

func (s *OrderService) resolveUpdatedBuyerAddress(
	ctx context.Context,
	buyerID, addressID string,
) (*orderv1.OrderAddressSnapshot, error) {
	addrID := strings.TrimSpace(addressID)
	if addrID == "" {
		return nil, status.Error(codes.InvalidArgument, "address_id is required")
	}
	if s.clients.User == nil {
		return nil, status.Error(codes.Unavailable, "user service unavailable")
	}
	resp, err := s.clients.User.GetAddress(ctx, &userv1.GetAddressRequest{
		UserId:    buyerID,
		AddressId: addrID,
	})
	if err != nil {
		return nil, err
	}
	a := resp.GetAddress()
	if a == nil {
		return nil, status.Error(codes.NotFound, "address not found")
	}
	return validateNormalizedAddressSnapshot(&orderv1.OrderAddressSnapshot{
		RecipientName:  a.GetRecipientName(),
		RecipientPhone: a.GetRecipientPhone(),
		ProvinceCode:   a.GetProvinceCode(),
		ProvinceName:   a.GetProvinceName(),
		WardCode:       a.GetWardCode(),
		WardName:       a.GetWardName(),
		StreetAddress:  a.GetStreetAddress(),
	})
}

func validateNormalizedAddressSnapshot(in *orderv1.OrderAddressSnapshot) (*orderv1.OrderAddressSnapshot, error) {
	name := strings.TrimSpace(in.GetRecipientName())
	rawPhone := strings.TrimSpace(in.GetRecipientPhone())
	provCode := strings.TrimSpace(in.GetProvinceCode())
	provName := strings.TrimSpace(in.GetProvinceName())
	wardCode := strings.TrimSpace(in.GetWardCode())
	wardName := strings.TrimSpace(in.GetWardName())
	street := strings.TrimSpace(in.GetStreetAddress())

	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "recipient_name is required")
	}
	normPhone, ok := normalizeVNPhone(rawPhone)
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "valid Vietnam recipient_phone is required")
	}
	if provCode == "" {
		return nil, status.Error(codes.InvalidArgument, "province_code is required")
	}
	if wardCode == "" {
		return nil, status.Error(codes.InvalidArgument, "ward_code is required")
	}
	if street == "" {
		return nil, status.Error(codes.InvalidArgument, "street_address is required")
	}
	return &orderv1.OrderAddressSnapshot{
		RecipientName:  name,
		RecipientPhone: normPhone,
		ProvinceCode:   provCode,
		ProvinceName:   provName,
		WardCode:       wardCode,
		WardName:       wardName,
		StreetAddress:  street,
	}, nil
}

func computeAddressChangeRequestHash(addr *orderv1.OrderAddressSnapshot) string {
	h := sha256.New()
	parts := []string{
		strings.TrimSpace(addr.GetRecipientName()),
		strings.TrimSpace(addr.GetRecipientPhone()),
		strings.TrimSpace(addr.GetProvinceCode()),
		strings.TrimSpace(addr.GetProvinceName()),
		strings.TrimSpace(addr.GetWardCode()),
		strings.TrimSpace(addr.GetWardName()),
		strings.TrimSpace(addr.GetStreetAddress()),
	}
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func summarizeOrderFeeWeight(items []db.OrderItem) (explicitWeight int32, fallbackQty int32) {
	for _, it := range items {
		if it.Quantity <= 0 {
			continue
		}
		if it.WeightGrams > 0 {
			explicitWeight += it.WeightGrams * it.Quantity
		} else {
			fallbackQty += it.Quantity
		}
	}
	return explicitWeight, fallbackQty
}

// validateCODCeilingForAddressChange applies the COD ceiling used at checkout to the checkout group's remaining total
// and returns the active MaxOrderVND so OpenOrderAddressChange can enforce the same ceiling transactionally (O3).
// Only the ceiling matters here: the order already exists, so a temporarily disabled COD method or the minimum do not block it.
func (s *OrderService) validateCODCeilingForAddressChange(ctx context.Context, ord db.Order, newOrderPayable int64) (int64, error) {
	if s.methodConfigs == nil {
		return 0, status.Error(codes.Unavailable, "PAYMENT_METHOD_CONFIG_UNAVAILABLE: payment method config source is not configured")
	}
	cfg, err := s.methodConfigs.GetPaymentMethodConfig(clients.WithInternalAdminContext(ctx), statemachine.PaymentMethodCOD)
	if err != nil {
		return 0, status.Errorf(codes.Unavailable, "PAYMENT_METHOD_CONFIG_UNAVAILABLE: failed to load COD config: %v", err)
	}
	if cfg == nil {
		return 0, status.Error(codes.Unavailable, "PAYMENT_METHOD_CONFIG_UNAVAILABLE: COD config is empty")
	}
	if cfg.MaxOrderVND <= 0 {
		return 0, nil
	}
	total := newOrderPayable
	if strings.TrimSpace(ord.CheckoutGroupID) != "" {
		siblings, err := s.store.ListOrdersByCheckoutGroup(ctx, ord.CheckoutGroupID)
		if err != nil {
			return 0, mapStoreError(err)
		}
		for _, sib := range siblings {
			if sib.Order.ID != ord.ID && statemachine.StringToStatus(sib.Order.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				total += sib.Order.TotalPayableVND
			}
		}
	}
	if total > cfg.MaxOrderVND {
		return 0, status.Errorf(codes.FailedPrecondition,
			"COD_LIMIT_EXCEEDED: checkout COD total %d VND exceeds COD maximum %d VND", total, cfg.MaxOrderVND)
	}
	return cfg.MaxOrderVND, nil
}

func abortedAddressChangeError(ch db.OrderAddressChange) error {
	code := strings.TrimSpace(ch.AbortCode)
	if code == "" {
		code = "ADDRESS_UPDATE_NOT_ALLOWED"
	}
	// Only a message that already carries the business code is safe to show; other errors stay in last_error.
	if strings.HasPrefix(ch.LastError, code) {
		return status.Error(codes.FailedPrecondition, ch.LastError)
	}
	return status.Errorf(codes.FailedPrecondition, "%s: address update was aborted", code)
}

func extractAddressAbortCode(err error, fallback string) string {
	if err == nil {
		return fallback
	}
	msg := status.Convert(err).Message()
	if idx := strings.Index(msg, ":"); idx > 0 {
		candidate := strings.TrimSpace(msg[:idx])
		if candidate != "" && !strings.ContainsAny(candidate, " \t\n") {
			return candidate
		}
	}
	return fallback
}

func buildUpdateOrderAddressResponse(ch db.OrderAddressChange, agg *store.OrderAggregate, caller callerIdentity) *orderv1.UpdateOrderAddressResponse {
	var resolved orderv1.OrderAddressSnapshot
	if len(ch.NewAddressJSON) > 0 {
		_ = json.Unmarshal(ch.NewAddressJSON, &resolved)
	}
	resp := &orderv1.UpdateOrderAddressResponse{
		OldShippingFeeVnd:  ch.OldShippingFeeVND,
		NewShippingFeeVnd:  ch.NewShippingFeeVND,
		FeeDeltaVnd:        ch.NewShippingFeeVND - ch.OldShippingFeeVND,
		OldTotalPayableVnd: ch.OldTotalPayableVND,
		NewTotalPayableVnd: ch.NewTotalPayableVND,
		PreviewOnly:        false,
		FeeAdjusted:        ch.FeeAdjusted,
		ShipmentRecreated:  ch.ShipmentRecreated,
		ShipmentQueued:     ch.ShipmentQueued,
		ShipmentJobId:      ch.ShipmentJobID,
		ResolvedAddress:    &resolved,
	}
	if agg != nil {
		resp.Order = aggregateToProtoForCaller(*agg, caller)
	}
	return resp
}
