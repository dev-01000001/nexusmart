package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

func formatOptionalRFC3339(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func buildReturnUpdatedOutbox(
	rr db.ReturnRequest,
	checkoutGroupID, prevStatus, reason, actorRole, traceID, traceparent string,
) (db.InsertOutboxEventParams, error) {
	payloadBytes, err := json.Marshal(events.OrderReturnUpdatedPayload{
		ReturnRequestID:      rr.ID,
		OrderID:              rr.OrderID,
		OrderCode:            rr.OrderCode,
		CheckoutGroupID:      checkoutGroupID,
		BuyerID:              rr.BuyerID,
		ShopID:               rr.ShopID,
		Status:               rr.Status,
		PreviousStatus:       prevStatus,
		Reason:               reason,
		ReturnTrackingCode:   rr.ReturnTrackingCode,
		DeadlineAt:           formatOptionalRFC3339(rr.DeadlineAt),
		ActorRole:            actorRole,
		RefundApprovalStatus: rr.RefundApprovalStatus,
	})
	if err != nil {
		return db.InsertOutboxEventParams{}, err
	}
	return db.InsertOutboxEventParams{
		ID:          uuid.NewString(),
		EventType:   events.SubjectOrderReturnUpdated,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
	}, nil
}

func (s *PostgresStore) GetReturnRequest(ctx context.Context, returnRequestID, orderID string) (*ReturnAggregate, error) {
	var (
		rr  db.ReturnRequest
		err error
	)
	if strings.TrimSpace(returnRequestID) != "" {
		rr, err = s.queries.GetReturnRequestByID(ctx, strings.TrimSpace(returnRequestID))
	} else {
		rr, err = s.queries.GetReturnRequestByOrderID(ctx, strings.TrimSpace(orderID))
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	evs, err := s.queries.ListReturnEventsByRequestID(ctx, rr.ID)
	if err != nil {
		return nil, err
	}
	return &ReturnAggregate{Request: rr, Events: evs}, nil
}

func (s *PostgresStore) ListReturnRequestsFiltered(ctx context.Context, buyerID, shopID, status string, limit, offset int32) ([]ReturnAggregate, int64, error) {
	total, err := s.queries.CountReturnRequestsFiltered(ctx, db.CountReturnRequestsFilteredParams{
		BuyerID: buyerID,
		ShopID:  shopID,
		Status:  status,
	})
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.queries.ListReturnRequestsFiltered(ctx, db.ListReturnRequestsFilteredParams{
		BuyerID:    buyerID,
		ShopID:     shopID,
		Status:     status,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]ReturnAggregate, 0, len(rows))
	for _, rr := range rows {
		evs, err := s.queries.ListReturnEventsByRequestID(ctx, rr.ID)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, ReturnAggregate{Request: rr, Events: evs})
	}
	return out, total, nil
}

func (s *PostgresStore) TransitionReturnRequest(ctx context.Context, params ReturnTransitionParams) (*ReturnTransitionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin return transition tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	// Non-locking peek to acquire locks in canonical order (orders first, then return_requests).
	// D9-bis §9: EVERY TransitionReturnRequest emits an outbox event keyed by order_id
	// (COALESCE(NULLIF(payload_json->>'order_id',''), id)), so we MUST lock the orders row
	// FOR UPDATE before inserting into outbox_events on all target statuses.
	peek, err := q.GetReturnRequestByID(ctx, params.ReturnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	ord, err := q.GetOrderByIDForUpdate(ctx, peek.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	rr, err := q.GetReturnRequestByIDForUpdate(ctx, params.ReturnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	curStatus := statemachine.StringToReturnStatus(rr.Status)
	if params.ExpectedStatus != orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED && curStatus != params.ExpectedStatus {
		return nil, fmt.Errorf("%w: expected return status %s, got %s",
			statemachine.ErrInvalidReturnTransition,
			statemachine.ReturnStatusToString(params.ExpectedStatus),
			statemachine.ReturnStatusToString(curStatus))
	}
	if err := statemachine.ValidateReturnTransition(curStatus, params.TargetStatus); err != nil {
		return nil, err
	}
	if params.RequireDeadlineExpired && (rr.DeadlineAt == nil || rr.DeadlineAt.After(now)) {
		return nil, ErrReturnDeadlineNotReached
	}
	if params.RequireNotGoodsRecv && rr.GoodsReceived {
		return nil, fmt.Errorf("%w: cannot require return shipment after goods were already received", statemachine.ErrInvalidReturnTransition)
	}
	if params.RequireNoTracking && strings.TrimSpace(rr.ReturnTrackingCode) != "" {
		return nil, fmt.Errorf("%w: return request already has tracking code", statemachine.ErrInvalidReturnTransition)
	}
	if params.RequireHasTracking && strings.TrimSpace(rr.ReturnTrackingCode) == "" {
		return nil, fmt.Errorf("%w: return request has no tracking code", statemachine.ErrInvalidReturnTransition)
	}

	nextEvidence := rr.EvidenceURLsJSON
	if params.EvidenceURLsJSON != nil {
		nextEvidence = params.EvidenceURLsJSON
	}
	nextSellerResp := rr.SellerResponse
	if params.SellerResponse != nil {
		nextSellerResp = *params.SellerResponse
	}
	nextSellerEv := rr.SellerEvidenceURLsJSON
	if params.SellerEvidenceURLsJSON != nil {
		nextSellerEv = params.SellerEvidenceURLsJSON
	}
	nextDispute := rr.DisputeReason
	if params.DisputeReason != nil {
		nextDispute = *params.DisputeReason
	}
	nextAdminNote := rr.AdminNote
	if params.AdminNote != nil {
		nextAdminNote = *params.AdminNote
	}
	nextBearer := rr.ReturnFeeBearer
	if params.ReturnFeeBearer != nil {
		nextBearer = *params.ReturnFeeBearer
	}
	nextGoodsRecv := rr.GoodsReceived
	if params.GoodsReceived != nil {
		nextGoodsRecv = *params.GoodsReceived
	}
	nextShouldRestock := rr.ShouldRestock
	if params.ShouldRestock != nil {
		nextShouldRestock = *params.ShouldRestock
	}
	nextShipmentTerminal := rr.ReturnShipmentTerminal
	if params.ReturnShipmentTerminal != nil {
		nextShipmentTerminal = *params.ReturnShipmentTerminal
	}
	nextEscTransit := rr.EscalatedFromTransitTimeout
	if params.EscalatedFromTransitTimeout != nil {
		nextEscTransit = *params.EscalatedFromTransitTimeout
	}
	nextEscCancelFail := rr.EscalatedFromCancelFailure
	if params.EscalatedFromCancelFailure != nil {
		nextEscCancelFail = *params.EscalatedFromCancelFailure
	}
	nextRequireApproval := rr.RequireApprovalIfLarge
	if params.RequireApprovalIfLarge != nil {
		nextRequireApproval = *params.RequireApprovalIfLarge
	}
	nextRefundRequestedBy := rr.RefundRequestedBy
	if params.RefundRequestedBy != nil {
		nextRefundRequestedBy = *params.RefundRequestedBy
	}
	nextRefundID := rr.RefundID
	if params.RefundID != nil {
		nextRefundID = *params.RefundID
	}
	nextRefundApprovalStatus := rr.RefundApprovalStatus
	if params.RefundApprovalStatus != nil {
		nextRefundApprovalStatus = *params.RefundApprovalStatus
	}
	nextRefundAttempt := rr.RefundAttempt
	if params.IncrementRefundAttempt {
		nextRefundAttempt++
	}
	nextExtCount := rr.DeadlineExtensionCount
	nextPrevTrackings := rr.PreviousReturnTrackingsJSON
	if len(nextPrevTrackings) == 0 {
		nextPrevTrackings = []byte("[]")
	}
	nextDeadline := rr.DeadlineAt
	if params.UpdateDeadline {
		nextDeadline = params.DeadlineAt
	}
	nextRefundAmt := rr.RefundAmountVND
	if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
		rem := ord.TotalPayableVND - ord.RefundedVND
		if rem < 0 {
			rem = 0
		}
		nextRefundAmt = rem
	}

	if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
		ordCur := statemachine.StringToStatus(ord.Status)
		if err := statemachine.ValidateTransition(ordCur, orderv1.OrderStatus_ORDER_STATUS_RETURNED); err != nil {
			return nil, err
		}
		nextOrdPayStatus := ""
		if ord.TotalPayableVND == 0 {
			nextOrdPayStatus = "CANCELLED"
		}
		updatedOrd, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
			ID:            ord.ID,
			Status:        statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED),
			PaymentStatus: nextOrdPayStatus,
		})
		if err != nil {
			return nil, err
		}
		// P3-1(a): serialize checkout_groups payment_status sync via group row lock (orders -> checkout_groups order).
		if nextOrdPayStatus != "" {
			if _, gErr := q.GetCheckoutGroupByIDForUpdate(ctx, ord.CheckoutGroupID); gErr == nil {
				if siblings, sErr := q.ListOrdersByCheckoutGroup(ctx, ord.CheckoutGroupID); sErr == nil {
					if shouldMarkGroupRefunded(siblings, updatedOrd.ID, updatedOrd.Status, updatedOrd.PaymentStatus) {
						if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
							ID: ord.CheckoutGroupID, PaymentStatus: "REFUNDED", PaymentID: "",
						}); err != nil {
							return nil, err
						}
					} else if shouldMarkGroupCancelled(siblings, updatedOrd.ID, updatedOrd.Status, updatedOrd.PaymentStatus) {
						if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
							ID: ord.CheckoutGroupID, PaymentStatus: "CANCELLED", PaymentID: "",
						}); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED),
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       params.Note,
		}); err != nil {
			return nil, err
		}
		wasPaid := updatedOrd.TotalPayableVND > 0 &&
			(strings.EqualFold(updatedOrd.PaymentStatus, "SUCCEEDED") ||
				statemachine.StringToPaymentMethod(updatedOrd.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		retPayload, err := json.Marshal(events.OrderReturnedPayload{
			OrderID:         updatedOrd.ID,
			OrderCode:       updatedOrd.OrderCode,
			CheckoutGroupID: updatedOrd.CheckoutGroupID,
			BuyerID:         updatedOrd.BuyerID,
			ShopID:          updatedOrd.ShopID,
			PaymentMethod:   updatedOrd.PaymentMethod,
			FromStatus:      ord.Status,
			TotalPayableVND: updatedOrd.TotalPayableVND,
			Reason:          params.Note,
			WasPaid:         wasPaid,
		})
		if err != nil {
			return nil, err
		}
		if err := q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderReturned,
			TraceID:     params.TraceID,
			Traceparent: params.Traceparent,
			PayloadJSON: retPayload,
		}); err != nil {
			return nil, err
		}
	} else if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_CLOSED {
		ordCur := statemachine.StringToStatus(ord.Status)
		if err := statemachine.ValidateTransition(ordCur, orderv1.OrderStatus_ORDER_STATUS_COMPLETED); err != nil {
			return nil, err
		}
		var completedOutbox db.InsertOutboxEventParams
		if params.BuildCompletedOutboxFn != nil {
			completedOutbox, err = params.BuildCompletedOutboxFn(ord)
			if err != nil {
				return nil, err
			}
		}
		if _, err := completeOrderInTx(ctx, q, ord, params.ActorID, params.ActorRole, params.Note, completedOutbox); err != nil {
			return nil, err
		}
	} else if curStatus == orderv1.ReturnStatus_RETURN_STATUS_REFUNDING &&
		params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
		// F11: Large refund approval was rejected by checker before any refund/promo/restock ran.
		// Re-escalate the return cleanly and restore order RETURNED -> RETURN_REQUESTED so admin can re-arbitrate.
		if rr.RefundDone || rr.PromoReleased || rr.RestockDone {
			return nil, fmt.Errorf("%w: cannot re-escalate REFUNDING return after refund/compensation already executed", statemachine.ErrInvalidReturnTransition)
		}
		ordCur := statemachine.StringToStatus(ord.Status)
		if ordCur == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			if err := statemachine.ValidateTransition(ordCur, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED); err != nil {
				return nil, err
			}
			if _, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
				ID:     ord.ID,
				Status: statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED),
			}); err != nil {
				return nil, err
			}
			if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: ord.Status,
				ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED),
				ActorID:    params.ActorID,
				ActorRole:  params.ActorRole,
				Note:       params.Note,
			}); err != nil {
				return nil, err
			}
		}
	}

	prevStatusStr := rr.Status
	nextTrackingCode := rr.ReturnTrackingCode
	nextLabelURL := rr.ReturnLabelURL
	nextFeeVND := rr.ReturnFeeVND
	eventNote := params.Note
	if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
		if prevTrk := strings.TrimSpace(rr.ReturnTrackingCode); prevTrk != "" {
			eventNote = fmt.Sprintf("%s (previous return tracking: %s)", eventNote, prevTrk)
			updatedPrev, pErr := appendPreviousReturnTracking(nextPrevTrackings, prevTrk)
			if pErr != nil {
				return nil, pErr
			}
			nextPrevTrackings = updatedPrev
		}
		nextTrackingCode = ""
		nextLabelURL = ""
		nextFeeVND = 0
		nextShipmentTerminal = false
		nextExtCount = 0
		nextEscTransit = false
		nextEscCancelFail = false
	}

	updatedRR, err := q.UpdateReturnRequestState(ctx, db.UpdateReturnRequestStateParams{
		ID:                          rr.ID,
		Status:                      statemachine.ReturnStatusToString(params.TargetStatus),
		EvidenceURLsJSON:            nextEvidence,
		SellerResponse:              nextSellerResp,
		SellerEvidenceURLsJSON:      nextSellerEv,
		DisputeReason:               nextDispute,
		AdminNote:                   nextAdminNote,
		RefundAmountVND:             nextRefundAmt,
		ReturnTrackingCode:          nextTrackingCode,
		ReturnLabelURL:              nextLabelURL,
		ReturnFeeVND:                nextFeeVND,
		ReturnFeeBearer:             nextBearer,
		GoodsReceived:               nextGoodsRecv,
		ShouldRestock:               nextShouldRestock,
		DeadlineAt:                  nextDeadline,
		ReturnShipmentTerminal:      nextShipmentTerminal,
		PreviousReturnTrackingsJSON: nextPrevTrackings,
		DeadlineExtensionCount:      nextExtCount,
		EscalatedFromTransitTimeout: nextEscTransit,
		EscalatedFromCancelFailure:  nextEscCancelFail,
		RequireApprovalIfLarge:      nextRequireApproval,
		RefundRequestedBy:           nextRefundRequestedBy,
		RefundID:                    nextRefundID,
		RefundApprovalStatus:        nextRefundApprovalStatus,
		RefundAttempt:               nextRefundAttempt,
	})
	if err != nil {
		return nil, err
	}

	if _, err := q.InsertReturnEvent(ctx, db.InsertReturnEventParams{
		ID:              uuid.NewString(),
		ReturnRequestID: rr.ID,
		FromStatus:      prevStatusStr,
		ToStatus:        updatedRR.Status,
		ActorID:         params.ActorID,
		ActorRole:       params.ActorRole,
		Note:            eventNote,
	}); err != nil {
		return nil, err
	}

	outboxReason := strings.TrimSpace(params.OutboxReason)
	if outboxReason == "" {
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_REFUNDING &&
			params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_ESCALATED &&
			strings.EqualFold(updatedRR.RefundApprovalStatus, "REJECTED") {
			outboxReason = "refund_rejected"
		} else {
			outboxReason = eventNote
		}
	}
	ob, err := buildReturnUpdatedOutbox(updatedRR, ord.CheckoutGroupID, prevStatusStr, outboxReason, params.ActorRole, params.TraceID, params.Traceparent)
	if err != nil {
		return nil, err
	}
	if err := q.InsertOutboxEvent(ctx, ob); err != nil {
		return nil, err
	}

	evs, err := q.ListReturnEventsByRequestID(ctx, rr.ID)
	if err != nil {
		return nil, err
	}
	ordAgg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ReturnTransitionResult{
		Return:     &ReturnAggregate{Request: updatedRR, Events: evs},
		Order:      ordAgg,
		PrevStatus: curStatus,
	}, nil
}

func (s *PostgresStore) ExtendReturnDeadline(ctx context.Context, returnRequestID string, expectedStatus orderv1.ReturnStatus, newDeadline, now time.Time, maxExtensions int32) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin extend return deadline tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	rr, err := q.GetReturnRequestByIDForUpdate(ctx, returnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if expectedStatus != orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED &&
		statemachine.StringToReturnStatus(rr.Status) != expectedStatus {
		return false, nil
	}
	if rr.DeadlineAt != nil && rr.DeadlineAt.After(now) {
		return false, nil
	}
	if maxExtensions > 0 && rr.DeadlineExtensionCount >= maxExtensions {
		return false, nil
	}
	dl := newDeadline.UTC()
	if _, err := q.ExtendReturnRequestDeadline(ctx, db.ExtendReturnRequestDeadlineParams{
		ID:         rr.ID,
		DeadlineAt: &dl,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *PostgresStore) SaveReturnShipmentInfo(ctx context.Context, params SaveReturnShipmentParams) (*ReturnAggregate, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin save return shipment tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	// D9-bis §9: Non-locking peek then canonical lock order (orders FOR UPDATE -> return_requests FOR UPDATE)
	// because SaveReturnShipmentInfo inserts an outbox event keyed by rr.OrderID.
	peek, err := q.GetReturnRequestByID(ctx, params.ReturnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	ord, err := q.GetOrderByIDForUpdate(ctx, peek.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}
	rr, err := q.GetReturnRequestByIDForUpdate(ctx, params.ReturnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}

	updated := false
	if statemachine.StringToReturnStatus(rr.Status) == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT &&
		strings.TrimSpace(rr.ReturnTrackingCode) == "" {
		fee := params.FeeVND
		if fee < 0 {
			fee = 0
		}
		rr, err = q.UpdateReturnShipmentInfo(ctx, db.UpdateReturnShipmentInfoParams{
			ID:                 rr.ID,
			ReturnTrackingCode: params.TrackingCode,
			ReturnLabelURL:     params.LabelURL,
			ReturnFeeVND:       fee,
			DeadlineAt:         params.DeadlineAt,
		})
		if err != nil {
			return nil, false, err
		}
		updated = true
		// P2-3: emit order.return_updated once return tracking code is persisted so notification delivers it to buyer.
		ob, obErr := buildReturnUpdatedOutbox(rr, ord.CheckoutGroupID, rr.Status, "Return shipping label created", "system", params.TraceID, params.Traceparent)
		if obErr != nil {
			return nil, false, obErr
		}
		if err := q.InsertOutboxEvent(ctx, ob); err != nil {
			return nil, false, err
		}
	}

	evs, err := q.ListReturnEventsByRequestID(ctx, rr.ID)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &ReturnAggregate{Request: rr, Events: evs}, updated, nil
}

func (s *PostgresStore) UpdateReturnSideEffectProgress(ctx context.Context, returnRequestID string, refundDone, promoReleased, restockDone bool, refundAmountVND *int64) (*db.ReturnRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	rr, err := q.GetReturnRequestByIDForUpdate(ctx, returnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var updAmt bool
	var amt int64
	if refundAmountVND != nil {
		updAmt = true
		amt = *refundAmountVND
		if amt < 0 {
			amt = 0
		}
	}
	updated, err := q.UpdateReturnRequestSideEffects(ctx, db.UpdateReturnRequestSideEffectsParams{
		ID:                         rr.ID,
		RefundDone:                 rr.RefundDone || refundDone,
		PromoReleased:              rr.PromoReleased || promoReleased,
		RestockDone:                rr.RestockDone || restockDone,
		UpdateRefundAmount:         updAmt,
		RefundAmountVND:            amt,
		UpdateRefundID:             false,
		RefundID:                   rr.RefundID,
		UpdateRefundApprovalStatus: false,
		RefundApprovalStatus:       rr.RefundApprovalStatus,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *PostgresStore) UpdateReturnRefundApprovalState(ctx context.Context, returnRequestID, refundID, approvalStatus string, refundDone bool, refundAmountVND *int64) (*db.ReturnRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	rr, err := q.GetReturnRequestByIDForUpdate(ctx, returnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	cleanApprovalStatus := strings.TrimSpace(approvalStatus)
	if statemachine.StringToReturnStatus(rr.Status) != orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
		return nil, fmt.Errorf("%w: cannot update refund approval state from return status %s", statemachine.ErrInvalidReturnTransition, rr.Status)
	}
	if strings.EqualFold(cleanApprovalStatus, "PENDING_APPROVAL") &&
		(rr.RefundDone || strings.EqualFold(rr.RefundApprovalStatus, "APPROVED") || strings.EqualFold(rr.RefundApprovalStatus, "REJECTED")) {
		return nil, fmt.Errorf("%w: cannot downgrade refund approval status from %q to PENDING_APPROVAL", statemachine.ErrInvalidReturnTransition, rr.RefundApprovalStatus)
	}
	var updAmt bool
	var amt int64
	if refundAmountVND != nil {
		updAmt = true
		amt = *refundAmountVND
		if amt < 0 {
			amt = 0
		}
	}
	nextRefundID := strings.TrimSpace(refundID)
	updRefundID := nextRefundID != ""
	updated, err := q.UpdateReturnRequestSideEffects(ctx, db.UpdateReturnRequestSideEffectsParams{
		ID:                         rr.ID,
		RefundDone:                 rr.RefundDone || refundDone,
		PromoReleased:              rr.PromoReleased,
		RestockDone:                rr.RestockDone,
		UpdateRefundAmount:         updAmt,
		RefundAmountVND:            amt,
		UpdateRefundID:             updRefundID,
		RefundID:                   nextRefundID,
		UpdateRefundApprovalStatus: true,
		RefundApprovalStatus:       cleanApprovalStatus,
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (s *PostgresStore) ApplyReturnShipmentUpdate(ctx context.Context, params ReturnShipmentUpdateParams) (*ReturnShipmentUpdateOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin return shipment update tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	if params.EventID != "" {
		inserted, err := q.TryMarkEventProcessed(ctx, db.TryMarkEventProcessedParams{
			EventID:   params.EventID,
			EventType: params.EventType,
		})
		if err != nil {
			return nil, err
		}
		if inserted == 0 {
			return &ReturnShipmentUpdateOutcome{AlreadyProcessed: true}, nil
		}
	}

	// D9-bis §9: Non-locking peek then canonical lock order (orders FOR UPDATE -> return_requests FOR UPDATE)
	// because ApplyReturnShipmentUpdate inserts an outbox event keyed by rr.OrderID.
	var peek db.ReturnRequest
	if strings.TrimSpace(params.ReturnRequestID) != "" {
		peek, err = q.GetReturnRequestByID(ctx, strings.TrimSpace(params.ReturnRequestID))
	} else {
		peek, err = q.GetReturnRequestByOrderID(ctx, strings.TrimSpace(params.OrderID))
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ReturnShipmentUpdateOutcome{NotFound: true}, nil
		}
		return nil, err
	}
	ord, err := q.GetOrderByIDForUpdate(ctx, peek.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ReturnShipmentUpdateOutcome{NotFound: true}, nil
		}
		return nil, err
	}
	rr, err := q.GetReturnRequestByIDForUpdate(ctx, peek.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ReturnShipmentUpdateOutcome{NotFound: true}, nil
		}
		return nil, err
	}

	curStatus := statemachine.StringToReturnStatus(rr.Status)
	if statemachine.IsTerminalReturnStatus(curStatus) {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ReturnShipmentUpdateOutcome{}, nil
	}

	// P1-1 + P1-2(a): Check previous_return_trackings ONLY when return_tracking_code is empty (pending save).
	// If return_tracking_code is set, matching current tracking ALWAYS takes precedence.
	curTracking := strings.TrimSpace(rr.ReturnTrackingCode)
	evTracking := strings.TrimSpace(params.TrackingCode)
	if evTracking == "" {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ReturnShipmentUpdateOutcome{}, nil
	}
	if curTracking == "" {
		if isPreviousReturnTracking(rr.PreviousReturnTrackingsJSON, evTracking) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ReturnShipmentUpdateOutcome{}, nil
		}
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			return nil, ErrReturnTrackingPending
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ReturnShipmentUpdateOutcome{}, nil
	}
	if !strings.EqualFold(evTracking, curTracking) {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ReturnShipmentUpdateOutcome{}, nil
	}

	shipStatus := strings.ToLower(strings.TrimSpace(params.ShipmentStatus))
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	note := strings.TrimSpace(params.Description)
	if note == "" {
		note = fmt.Sprintf("Return shipment update: %s", shipStatus)
	}

	var targetStatus orderv1.ReturnStatus
	var nextDeadline *time.Time
	nextGoodsRecv := rr.GoodsReceived
	nextDispute := rr.DisputeReason
	nextShipmentTerminal := rr.ReturnShipmentTerminal

	switch shipStatus {
	case "delivering", "returning":
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT
			transitDur := params.TransitDuration
			if transitDur <= 0 {
				transitDur = 14 * 24 * time.Hour
			}
			dl := now.Add(transitDur)
			nextDeadline = &dl
		}
	case "delivered":
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			curStatus == orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_RECEIVED
			nextGoodsRecv = true
			inspectDur := params.InspectDuration
			if inspectDur <= 0 {
				inspectDur = 3 * 24 * time.Hour
			}
			dl := now.Add(inspectDur)
			nextDeadline = &dl
		}
	case "returned", "cancelled":
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			curStatus == orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
			nextDeadline = nil
			nextShipmentTerminal = true
			nextDispute = note
		}
	case "failed":
		if (curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			curStatus == orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT) &&
			IsTerminalReturnFailureRawStatus(params.RawStatus) {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
			nextDeadline = nil
			nextShipmentTerminal = true
			nextDispute = note
		}
	}

	if targetStatus == orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ReturnShipmentUpdateOutcome{}, nil
	}

	prevStatusStr := rr.Status
	prevTrackingsJSON := rr.PreviousReturnTrackingsJSON
	if len(prevTrackingsJSON) == 0 {
		prevTrackingsJSON = []byte("[]")
	}
	nextExtCount := rr.DeadlineExtensionCount
	if targetStatus == orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT ||
		targetStatus == orderv1.ReturnStatus_RETURN_STATUS_RECEIVED ||
		targetStatus == orderv1.ReturnStatus_RETURN_STATUS_ESCALATED {
		nextExtCount = 0
	}
	updatedRR, err := q.UpdateReturnRequestState(ctx, db.UpdateReturnRequestStateParams{
		ID:                          rr.ID,
		Status:                      statemachine.ReturnStatusToString(targetStatus),
		EvidenceURLsJSON:            rr.EvidenceURLsJSON,
		SellerResponse:              rr.SellerResponse,
		SellerEvidenceURLsJSON:      rr.SellerEvidenceURLsJSON,
		DisputeReason:               nextDispute,
		AdminNote:                   rr.AdminNote,
		RefundAmountVND:             rr.RefundAmountVND,
		ReturnTrackingCode:          rr.ReturnTrackingCode,
		ReturnLabelURL:              rr.ReturnLabelURL,
		ReturnFeeVND:                rr.ReturnFeeVND,
		ReturnFeeBearer:             rr.ReturnFeeBearer,
		GoodsReceived:               nextGoodsRecv,
		ShouldRestock:               rr.ShouldRestock,
		DeadlineAt:                  nextDeadline,
		ReturnShipmentTerminal:      nextShipmentTerminal,
		PreviousReturnTrackingsJSON: prevTrackingsJSON,
		DeadlineExtensionCount:      nextExtCount,
		EscalatedFromTransitTimeout: rr.EscalatedFromTransitTimeout,
		EscalatedFromCancelFailure:  rr.EscalatedFromCancelFailure,
		RequireApprovalIfLarge:      rr.RequireApprovalIfLarge,
		RefundRequestedBy:           rr.RefundRequestedBy,
		RefundID:                    rr.RefundID,
		RefundApprovalStatus:        rr.RefundApprovalStatus,
		RefundAttempt:               rr.RefundAttempt,
	})
	if err != nil {
		return nil, err
	}

	if _, err := q.InsertReturnEvent(ctx, db.InsertReturnEventParams{
		ID:              uuid.NewString(),
		ReturnRequestID: rr.ID,
		FromStatus:      prevStatusStr,
		ToStatus:        updatedRR.Status,
		ActorID:         "shipping-service",
		ActorRole:       "system",
		Note:            note,
	}); err != nil {
		return nil, err
	}

	ob, err := buildReturnUpdatedOutbox(updatedRR, ord.CheckoutGroupID, prevStatusStr, note, "system", params.TraceID, params.Traceparent)
	if err != nil {
		return nil, err
	}
	if err := q.InsertOutboxEvent(ctx, ob); err != nil {
		return nil, err
	}

	evs, err := q.ListReturnEventsByRequestID(ctx, rr.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ReturnShipmentUpdateOutcome{
		Transitioned: true,
		Return:       &ReturnAggregate{Request: updatedRR, Events: evs},
	}, nil
}

func (s *PostgresStore) FindConfirmedOrdersForAutoCancel(ctx context.Context, cutoff time.Time, limit int32) ([]string, error) {
	return s.queries.FindConfirmedOrdersPastTimeout(ctx, db.FindConfirmedOrdersPastTimeoutParams{
		Cutoff:     cutoff.UTC(),
		BatchLimit: limit,
	})
}

func (s *PostgresStore) FindDeliveredOrdersForAutoComplete(ctx context.Context, cutoff time.Time, limit int32) ([]string, error) {
	return s.queries.FindDeliveredOrdersForAutoComplete(ctx, db.FindDeliveredOrdersForAutoCompleteParams{
		Cutoff:     cutoff.UTC(),
		BatchLimit: limit,
	})
}

func (s *PostgresStore) FindExpiredReturnRequests(ctx context.Context, now time.Time, limit int32) ([]string, error) {
	return s.queries.FindExpiredReturnRequests(ctx, db.FindExpiredReturnRequestsParams{
		Now:        now.UTC(),
		BatchLimit: limit,
	})
}

func (s *PostgresStore) FindAwaitingShipmentReturnsWithoutTracking(ctx context.Context, limit int32) ([]string, error) {
	return s.queries.FindAwaitingShipmentReturnsWithoutTracking(ctx, limit)
}

func (s *PostgresStore) FindRefundingReturnRequests(ctx context.Context, limit int32) ([]string, error) {
	return s.queries.FindRefundingReturnRequests(ctx, limit)
}
