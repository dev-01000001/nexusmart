package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

// SetOrderConfirmedAt overrides confirmed_at (and updated_at) for testing seller confirm timeout.
func (m *MemoryStore) SetOrderConfirmedAt(orderID string, t *time.Time, updatedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		ord.ConfirmedAt = t
		if !updatedAt.IsZero() {
			ord.UpdatedAt = updatedAt
		}
		m.orders[orderID] = ord
	}
}

// SetOrderPaymentIDForTest overrides payment_id on an order for unit testing missing payment_id cases.
func (m *MemoryStore) SetOrderPaymentIDForTest(orderID, paymentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		ord.PaymentID = paymentID
		m.orders[orderID] = ord
	}
}

// SeedClosedReturnForDeliveredOrderForTest inserts a CLOSED return_requests row on a DELIVERED order
// (simulating legacy rows backfilled by migration 0005).
func (m *MemoryStore) SeedClosedReturnForDeliveredOrderForTest(orderID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		id := "ret-legacy-closed-" + orderID
		m.returnRequests[id] = db.ReturnRequest{
			ID:      id,
			OrderID: ord.ID,
			BuyerID: ord.BuyerID,
			ShopID:  ord.ShopID,
			Status:  "CLOSED",
		}
	}
}

func (m *MemoryStore) getReturnLocked(returnRequestID, orderID string) (*ReturnAggregate, error) {
	if id := strings.TrimSpace(returnRequestID); id != "" {
		rr, ok := m.returnRequests[id]
		if !ok {
			return nil, ErrNotFound
		}
		evs := append([]db.ReturnEvent(nil), m.returnEvents[rr.ID]...)
		return &ReturnAggregate{Request: rr, Events: evs}, nil
	}
	oid := strings.TrimSpace(orderID)
	for _, rr := range m.returnRequests {
		if rr.OrderID == oid {
			evs := append([]db.ReturnEvent(nil), m.returnEvents[rr.ID]...)
			return &ReturnAggregate{Request: rr, Events: evs}, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) GetReturnRequest(_ context.Context, returnRequestID, orderID string) (*ReturnAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getReturnLocked(returnRequestID, orderID)
}

func (m *MemoryStore) ListReturnRequestsFiltered(_ context.Context, buyerID, shopID, status string, limit, offset int32) ([]ReturnAggregate, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []ReturnAggregate
	for _, rr := range m.returnRequests {
		if buyerID != "" && rr.BuyerID != buyerID {
			continue
		}
		if shopID != "" && rr.ShopID != shopID {
			continue
		}
		if status != "" && rr.Status != status {
			continue
		}
		evs := append([]db.ReturnEvent(nil), m.returnEvents[rr.ID]...)
		matched = append(matched, ReturnAggregate{Request: rr, Events: evs})
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].Request.CreatedAt.Equal(matched[j].Request.CreatedAt) {
			return matched[i].Request.CreatedAt.After(matched[j].Request.CreatedAt)
		}
		return matched[i].Request.ID > matched[j].Request.ID
	})
	total := int64(len(matched))
	start := int(offset)
	if start >= len(matched) {
		return []ReturnAggregate{}, total, nil
	}
	end := start + int(limit)
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], total, nil
}

func (m *MemoryStore) TransitionReturnRequest(_ context.Context, params ReturnTransitionParams) (*ReturnTransitionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rr, ok := m.returnRequests[params.ReturnRequestID]
	if !ok {
		return nil, ErrNotFound
	}
	ord, ok := m.orders[rr.OrderID]
	if !ok {
		return nil, ErrNotFound
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

	if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
		ordCur := statemachine.StringToStatus(ord.Status)
		if err := statemachine.ValidateTransition(ordCur, orderv1.OrderStatus_ORDER_STATUS_RETURNED); err != nil {
			return nil, err
		}
		prevOrdStatus := ord.Status
		ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED)
		if ord.TotalPayableVND == 0 {
			ord.PaymentStatus = "CANCELLED"
		}
		ord.UpdatedAt = now
		m.orders[ord.ID] = ord
		if ord.TotalPayableVND == 0 {
			var siblings []db.Order
			for _, sib := range m.orders {
				if sib.CheckoutGroupID == ord.CheckoutGroupID {
					siblings = append(siblings, sib)
				}
			}
			if g, ok := m.groups[ord.CheckoutGroupID]; ok {
				if shouldMarkGroupRefunded(siblings, ord.ID, ord.Status, ord.PaymentStatus) {
					g.PaymentStatus = "REFUNDED"
					g.UpdatedAt = now
					m.groups[ord.CheckoutGroupID] = g
				} else if shouldMarkGroupCancelled(siblings, ord.ID, ord.Status, ord.PaymentStatus) {
					g.PaymentStatus = "CANCELLED"
					g.UpdatedAt = now
					m.groups[ord.CheckoutGroupID] = g
				}
			}
		}
		m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: prevOrdStatus,
			ToStatus:   ord.Status,
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       params.Note,
			CreatedAt:  now,
		})
		wasPaid := ord.TotalPayableVND > 0 &&
			(strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") ||
				statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD)
		retPayload, err := json.Marshal(events.OrderReturnedPayload{
			OrderID:         ord.ID,
			OrderCode:       ord.OrderCode,
			CheckoutGroupID: ord.CheckoutGroupID,
			BuyerID:         ord.BuyerID,
			ShopID:          ord.ShopID,
			PaymentMethod:   ord.PaymentMethod,
			FromStatus:      prevOrdStatus,
			TotalPayableVND: ord.TotalPayableVND,
			Reason:          params.Note,
			WasPaid:         wasPaid,
		})
		if err != nil {
			return nil, err
		}
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderReturned,
			TraceID:     params.TraceID,
			Traceparent: params.Traceparent,
			PayloadJSON: retPayload,
			CreatedAt:   now,
		})
	} else if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_CLOSED {
		ordCur := statemachine.StringToStatus(ord.Status)
		if err := statemachine.ValidateTransition(ordCur, orderv1.OrderStatus_ORDER_STATUS_COMPLETED); err != nil {
			return nil, err
		}
		var completedOutbox db.InsertOutboxEventParams
		if params.BuildCompletedOutboxFn != nil {
			var err error
			completedOutbox, err = params.BuildCompletedOutboxFn(ord)
			if err != nil {
				return nil, err
			}
		}
		m.completeOrderLocked(ord, params.ActorID, params.ActorRole, params.Note, completedOutbox, now)
	}

	prevStatusStr := rr.Status
	eventNote := params.Note
	rr.Status = statemachine.ReturnStatusToString(params.TargetStatus)
	if params.EvidenceURLsJSON != nil {
		rr.EvidenceURLsJSON = params.EvidenceURLsJSON
	}
	if params.SellerResponse != nil {
		rr.SellerResponse = *params.SellerResponse
	}
	if params.SellerEvidenceURLsJSON != nil {
		rr.SellerEvidenceURLsJSON = params.SellerEvidenceURLsJSON
	}
	if params.DisputeReason != nil {
		rr.DisputeReason = *params.DisputeReason
	}
	if params.AdminNote != nil {
		rr.AdminNote = *params.AdminNote
	}
	if params.ReturnFeeBearer != nil {
		rr.ReturnFeeBearer = *params.ReturnFeeBearer
	}
	if params.GoodsReceived != nil {
		rr.GoodsReceived = *params.GoodsReceived
	}
	if params.ShouldRestock != nil {
		rr.ShouldRestock = *params.ShouldRestock
	}
	if params.ReturnShipmentTerminal != nil {
		rr.ReturnShipmentTerminal = *params.ReturnShipmentTerminal
	}
	if params.EscalatedFromTransitTimeout != nil {
		rr.EscalatedFromTransitTimeout = *params.EscalatedFromTransitTimeout
	}
	if params.EscalatedFromCancelFailure != nil {
		rr.EscalatedFromCancelFailure = *params.EscalatedFromCancelFailure
	}
	if params.UpdateDeadline {
		rr.DeadlineAt = params.DeadlineAt
	}
	if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
		if prevTrk := strings.TrimSpace(rr.ReturnTrackingCode); prevTrk != "" {
			eventNote = fmt.Sprintf("%s (previous return tracking: %s)", eventNote, prevTrk)
			updatedPrev, pErr := appendPreviousReturnTracking(rr.PreviousReturnTrackingsJSON, prevTrk)
			if pErr != nil {
				return nil, pErr
			}
			rr.PreviousReturnTrackingsJSON = updatedPrev
		}
		rr.ReturnTrackingCode = ""
		rr.ReturnLabelURL = ""
		rr.ReturnFeeVND = 0
		rr.ReturnShipmentTerminal = false
		rr.DeadlineExtensionCount = 0
		rr.EscalatedFromTransitTimeout = false
		rr.EscalatedFromCancelFailure = false
	}
	if params.TargetStatus == orderv1.ReturnStatus_RETURN_STATUS_REFUNDING {
		rem := ord.TotalPayableVND - ord.RefundedVND
		if rem < 0 {
			rem = 0
		}
		rr.RefundAmountVND = rem
	}
	rr.UpdatedAt = now
	m.returnRequests[rr.ID] = rr

	m.returnEvents[rr.ID] = append(m.returnEvents[rr.ID], db.ReturnEvent{
		ID:              uuid.NewString(),
		ReturnRequestID: rr.ID,
		FromStatus:      prevStatusStr,
		ToStatus:        rr.Status,
		ActorID:         params.ActorID,
		ActorRole:       params.ActorRole,
		Note:            eventNote,
		CreatedAt:       now,
	})

	ob, err := buildReturnUpdatedOutbox(rr, ord.CheckoutGroupID, prevStatusStr, eventNote, params.ActorRole, params.TraceID, params.Traceparent)
	if err != nil {
		return nil, err
	}
	m.outbox = append(m.outbox, db.OutboxEvent{
		ID:          ob.ID,
		EventType:   ob.EventType,
		TraceID:     ob.TraceID,
		Traceparent: ob.Traceparent,
		PayloadJSON: ob.PayloadJSON,
		CreatedAt:   now,
	})

	retAgg, _ := m.getReturnLocked(rr.ID, "")
	ordAgg, _ := m.getOrderLocked(ord.ID)
	return &ReturnTransitionResult{
		Return:     retAgg,
		Order:      ordAgg,
		PrevStatus: curStatus,
	}, nil
}

func (m *MemoryStore) ExtendReturnDeadline(_ context.Context, returnRequestID string, expectedStatus orderv1.ReturnStatus, newDeadline, now time.Time, maxExtensions int32) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rr, ok := m.returnRequests[returnRequestID]
	if !ok {
		return false, ErrNotFound
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
	rr.DeadlineAt = &dl
	rr.DeadlineExtensionCount++
	rr.UpdatedAt = now
	m.returnRequests[rr.ID] = rr
	return true, nil
}

func (m *MemoryStore) SaveReturnShipmentInfo(_ context.Context, params SaveReturnShipmentParams) (*ReturnAggregate, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rr, ok := m.returnRequests[params.ReturnRequestID]
	if !ok {
		return nil, false, ErrNotFound
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	updated := false
	if statemachine.StringToReturnStatus(rr.Status) == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT &&
		strings.TrimSpace(rr.ReturnTrackingCode) == "" {
		fee := params.FeeVND
		if fee < 0 {
			fee = 0
		}
		rr.ReturnTrackingCode = params.TrackingCode
		rr.ReturnLabelURL = params.LabelURL
		rr.ReturnFeeVND = fee
		rr.DeadlineAt = params.DeadlineAt
		rr.ReturnShipmentTerminal = false
		rr.DeadlineExtensionCount = 0
		rr.EscalatedFromTransitTimeout = false
		rr.EscalatedFromCancelFailure = false
		rr.UpdatedAt = now
		m.returnRequests[rr.ID] = rr
		updated = true

		var checkoutGroupID string
		if ord, ok := m.orders[rr.OrderID]; ok {
			checkoutGroupID = ord.CheckoutGroupID
		}
		ob, obErr := buildReturnUpdatedOutbox(rr, checkoutGroupID, rr.Status, "Return shipping label created", "system", params.TraceID, params.Traceparent)
		if obErr != nil {
			return nil, false, obErr
		}
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          ob.ID,
			EventType:   ob.EventType,
			TraceID:     ob.TraceID,
			Traceparent: ob.Traceparent,
			PayloadJSON: ob.PayloadJSON,
			CreatedAt:   now,
		})
	}
	agg, err := m.getReturnLocked(rr.ID, "")
	return agg, updated, err
}

func (m *MemoryStore) UpdateReturnSideEffectProgress(_ context.Context, returnRequestID string, refundDone, promoReleased, restockDone bool, refundAmountVND *int64) (*db.ReturnRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rr, ok := m.returnRequests[returnRequestID]
	if !ok {
		return nil, ErrNotFound
	}
	rr.RefundDone = rr.RefundDone || refundDone
	rr.PromoReleased = rr.PromoReleased || promoReleased
	rr.RestockDone = rr.RestockDone || restockDone
	if refundAmountVND != nil {
		amt := *refundAmountVND
		if amt < 0 {
			amt = 0
		}
		rr.RefundAmountVND = amt
	}
	rr.UpdatedAt = time.Now().UTC()
	m.returnRequests[rr.ID] = rr
	cp := rr
	return &cp, nil
}

func (m *MemoryStore) ApplyReturnShipmentUpdate(_ context.Context, params ReturnShipmentUpdateParams) (*ReturnShipmentUpdateOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if params.EventID != "" {
		if _, exists := m.processedEvents[params.EventID]; exists {
			return &ReturnShipmentUpdateOutcome{AlreadyProcessed: true}, nil
		}
	}
	markProcessed := func() {
		if params.EventID != "" {
			m.processedEvents[params.EventID] = db.ProcessedEvent{
				EventID:     params.EventID,
				EventType:   params.EventType,
				ProcessedAt: now,
			}
		}
	}

	agg, err := m.getReturnLocked(params.ReturnRequestID, params.OrderID)
	if err != nil {
		markProcessed()
		return &ReturnShipmentUpdateOutcome{NotFound: true}, nil
	}
	rr := agg.Request
	curStatus := statemachine.StringToReturnStatus(rr.Status)
	if statemachine.IsTerminalReturnStatus(curStatus) {
		markProcessed()
		return &ReturnShipmentUpdateOutcome{}, nil
	}

	curTracking := strings.TrimSpace(rr.ReturnTrackingCode)
	evTracking := strings.TrimSpace(params.TrackingCode)
	if evTracking == "" {
		markProcessed()
		return &ReturnShipmentUpdateOutcome{}, nil
	}
	if curTracking == "" {
		if isPreviousReturnTracking(rr.PreviousReturnTrackingsJSON, evTracking) {
			markProcessed()
			return &ReturnShipmentUpdateOutcome{}, nil
		}
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			// Early event before SaveReturnShipmentInfo: do NOT mark processed so NATS redelivers.
			return nil, ErrReturnTrackingPending
		}
		markProcessed()
		return &ReturnShipmentUpdateOutcome{}, nil
	}
	if !strings.EqualFold(evTracking, curTracking) {
		markProcessed()
		return &ReturnShipmentUpdateOutcome{}, nil
	}
	markProcessed()

	shipStatus := strings.ToLower(strings.TrimSpace(params.ShipmentStatus))
	note := strings.TrimSpace(params.Description)
	if note == "" {
		note = fmt.Sprintf("Return shipment update: %s", shipStatus)
	}

	var targetStatus orderv1.ReturnStatus
	switch shipStatus {
	case "delivering", "returning":
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT
			transitDur := params.TransitDuration
			if transitDur <= 0 {
				transitDur = 14 * 24 * time.Hour
			}
			dl := now.Add(transitDur)
			rr.DeadlineAt = &dl
		}
	case "delivered":
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			curStatus == orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_RECEIVED
			rr.GoodsReceived = true
			inspectDur := params.InspectDuration
			if inspectDur <= 0 {
				inspectDur = 3 * 24 * time.Hour
			}
			dl := now.Add(inspectDur)
			rr.DeadlineAt = &dl
		}
	case "returned", "cancelled":
		if curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			curStatus == orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
			rr.DeadlineAt = nil
			rr.ReturnShipmentTerminal = true
			rr.DisputeReason = note
		}
	case "failed":
		if (curStatus == orderv1.ReturnStatus_RETURN_STATUS_AWAITING_SHIPMENT ||
			curStatus == orderv1.ReturnStatus_RETURN_STATUS_IN_TRANSIT) &&
			IsTerminalReturnFailureRawStatus(params.RawStatus) {
			targetStatus = orderv1.ReturnStatus_RETURN_STATUS_ESCALATED
			rr.DeadlineAt = nil
			rr.ReturnShipmentTerminal = true
			rr.DisputeReason = note
		}
	}

	if targetStatus == orderv1.ReturnStatus_RETURN_STATUS_UNSPECIFIED {
		return &ReturnShipmentUpdateOutcome{}, nil
	}

	prevStatusStr := rr.Status
	rr.Status = statemachine.ReturnStatusToString(targetStatus)
	rr.DeadlineExtensionCount = 0
	rr.UpdatedAt = now
	m.returnRequests[rr.ID] = rr

	m.returnEvents[rr.ID] = append(m.returnEvents[rr.ID], db.ReturnEvent{
		ID:              uuid.NewString(),
		ReturnRequestID: rr.ID,
		FromStatus:      prevStatusStr,
		ToStatus:        rr.Status,
		ActorID:         "shipping-service",
		ActorRole:       "system",
		Note:            note,
		CreatedAt:       now,
	})

	var checkoutGroupID string
	if ord, ok := m.orders[rr.OrderID]; ok {
		checkoutGroupID = ord.CheckoutGroupID
	}
	ob, err := buildReturnUpdatedOutbox(rr, checkoutGroupID, prevStatusStr, note, "system", params.TraceID, params.Traceparent)
	if err != nil {
		return nil, err
	}
	m.outbox = append(m.outbox, db.OutboxEvent{
		ID:          ob.ID,
		EventType:   ob.EventType,
		TraceID:     ob.TraceID,
		Traceparent: ob.Traceparent,
		PayloadJSON: ob.PayloadJSON,
		CreatedAt:   now,
	})

	updatedAgg, _ := m.getReturnLocked(rr.ID, "")
	return &ReturnShipmentUpdateOutcome{Transitioned: true, Return: updatedAgg}, nil
}

func (m *MemoryStore) FindConfirmedOrdersForAutoCancel(_ context.Context, cutoff time.Time, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, ord := range m.orders {
		if ord.Status != "CONFIRMED" {
			continue
		}
		ts := ord.UpdatedAt
		if ord.ConfirmedAt != nil {
			ts = *ord.ConfirmedAt
		}
		if !ts.After(cutoff) {
			matched = append(matched, ord.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random() to prevent starvation behind permanently failing rows.
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) FindDeliveredOrdersForAutoComplete(_ context.Context, cutoff time.Time, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hasActiveReturn := make(map[string]struct{}, len(m.returnRequests))
	for _, rr := range m.returnRequests {
		if rr.Status != "CLOSED" {
			hasActiveReturn[rr.OrderID] = struct{}{}
		}
	}
	var matched []string
	for _, ord := range m.orders {
		if ord.Status != "DELIVERED" || ord.DeliveredAt == nil {
			continue
		}
		if _, has := hasActiveReturn[ord.ID]; has {
			continue
		}
		if !ord.DeliveredAt.After(cutoff) {
			matched = append(matched, ord.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random() to prevent starvation behind permanently failing rows.
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) FindExpiredReturnRequests(_ context.Context, now time.Time, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, rr := range m.returnRequests {
		switch rr.Status {
		case "PENDING", "AWAITING_SHIPMENT", "IN_TRANSIT", "REJECTED", "RECEIVED", "EVIDENCE_REQUESTED":
		default:
			continue
		}
		if rr.DeadlineAt != nil && !rr.DeadlineAt.After(now) {
			matched = append(matched, rr.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random() to prevent starvation behind permanently failing rows.
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) FindAwaitingShipmentReturnsWithoutTracking(_ context.Context, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, rr := range m.returnRequests {
		if rr.Status == "AWAITING_SHIPMENT" && strings.TrimSpace(rr.ReturnTrackingCode) == "" {
			matched = append(matched, rr.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random() to prevent starvation behind permanently failing rows.
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) FindRefundingReturnRequests(_ context.Context, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, rr := range m.returnRequests {
		if rr.Status == "REFUNDING" {
			matched = append(matched, rr.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random() to prevent starvation behind permanently failing rows.
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}
