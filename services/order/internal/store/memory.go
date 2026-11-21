package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

// MemoryStore is a thread-safe in-memory implementation of Store for unit tests.
type MemoryStore struct {
	mu              sync.Mutex
	groups          map[string]db.CheckoutGroup
	orders          map[string]db.Order
	items           map[string][]db.OrderItem
	timelines       map[string][]db.OrderTimelineEvent
	returnRequests  map[string]db.ReturnRequest
	outbox          []db.OutboxEvent
	processedEvents map[string]db.ProcessedEvent
	deadLetters     []db.DeadLetterEvent
}

// NewMemoryStore initializes an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		groups:          make(map[string]db.CheckoutGroup),
		orders:          make(map[string]db.Order),
		items:           make(map[string][]db.OrderItem),
		timelines:       make(map[string][]db.OrderTimelineEvent),
		returnRequests:  make(map[string]db.ReturnRequest),
		processedEvents: make(map[string]db.ProcessedEvent),
	}
}

// OutboxEvents returns a copy of all recorded outbox events (helper for unit tests).
func (m *MemoryStore) OutboxEvents() []db.OutboxEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]db.OutboxEvent, len(m.outbox))
	copy(out, m.outbox)
	return out
}

// DeadLetters returns a copy of all recorded dead letter events (helper for unit tests).
func (m *MemoryStore) DeadLetters() []db.DeadLetterEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]db.DeadLetterEvent, len(m.deadLetters))
	copy(out, m.deadLetters)
	return out
}

// SetOrderPaymentExpiresAt overrides payment_expires_at for testing expiry watchdog.
func (m *MemoryStore) SetOrderPaymentExpiresAt(orderID string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		ts := t
		ord.PaymentExpiresAt = &ts
		m.orders[orderID] = ord
	}
}

// SeedLegacyOrder inserts a raw order (potentially with empty SalesChannel simulating a pre-migration row) for tests.
func (m *MemoryStore) SeedLegacyOrder(group db.CheckoutGroup, ord db.Order, items []db.OrderItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groups[group.ID] = group
	m.orders[ord.ID] = ord
	m.items[ord.ID] = append([]db.OrderItem(nil), items...)
}

func (m *MemoryStore) Ping(_ context.Context) error { return nil }

func (m *MemoryStore) GetActiveCheckoutGroup(_ context.Context, buyerID, idempotencyKey string) (*db.CheckoutGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.groups {
		if g.BuyerID == buyerID && g.IdempotencyKey == idempotencyKey && g.Status != "FAILED" {
			cp := g
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) GetCheckoutGroupByID(_ context.Context, id string) (*db.CheckoutGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := g
	return &cp, nil
}

func (m *MemoryStore) CreateCheckoutGroup(_ context.Context, arg db.CreateCheckoutGroupParams) (*db.CheckoutGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.groups {
		if g.BuyerID == arg.BuyerID && g.IdempotencyKey == arg.IdempotencyKey && g.Status != "FAILED" {
			return nil, ErrDuplicateIdempotencyKey
		}
	}
	now := time.Now().UTC()
	ch := arg.SalesChannel
	if ch == "" {
		ch = "MARKETPLACE"
	}
	g := db.CheckoutGroup{
		ID:               arg.ID,
		BuyerID:          arg.BuyerID,
		IdempotencyKey:   arg.IdempotencyKey,
		Status:           arg.Status,
		PaymentMethod:    arg.PaymentMethod,
		TotalPayableVND:  arg.TotalPayableVND,
		SalesChannel:     ch,
		StorefrontShopID: arg.StorefrontShopID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	m.groups[g.ID] = g
	cp := g
	return &cp, nil
}

func (m *MemoryStore) FailCheckoutGroup(_ context.Context, id, paymentStatus string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.groups[id]; ok {
		g.Status = "FAILED"
		g.PaymentStatus = paymentStatus
		g.UpdatedAt = time.Now().UTC()
		m.groups[id] = g
	}
	return nil
}

func (m *MemoryStore) PersistCheckoutBatch(_ context.Context, batch CheckoutBatch) ([]OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	out := make([]OrderAggregate, 0, len(batch.Orders))

	for _, rec := range batch.Orders {
		ordChannel := rec.Order.SalesChannel
		if ordChannel == "" {
			ordChannel = "MARKETPLACE"
		}
		ord := db.Order{
			ID:                         rec.Order.ID,
			OrderCode:                  rec.Order.OrderCode,
			CheckoutGroupID:            rec.Order.CheckoutGroupID,
			BuyerID:                    rec.Order.BuyerID,
			ShopID:                     rec.Order.ShopID,
			Status:                     rec.Order.Status,
			PaymentMethod:              rec.Order.PaymentMethod,
			PaymentID:                  rec.Order.PaymentID,
			PaymentStatus:              rec.Order.PaymentStatus,
			SubtotalVND:                rec.Order.SubtotalVND,
			FlashSaleDiscountVND:       rec.Order.FlashSaleDiscountVND,
			ShopVoucherDiscountVND:     rec.Order.ShopVoucherDiscountVND,
			PlatformVoucherDiscountVND: rec.Order.PlatformVoucherDiscountVND,
			FreeshipDiscountVND:        rec.Order.FreeshipDiscountVND,
			CoinsDiscountVND:           rec.Order.CoinsDiscountVND,
			ShippingFeeVND:             rec.Order.ShippingFeeVND,
			CommissionVND:              rec.Order.CommissionVND,
			TotalPayableVND:            rec.Order.TotalPayableVND,
			CarrierCode:                rec.Order.CarrierCode,
			ServiceCode:                rec.Order.ServiceCode,
			TrackingCode:               rec.Order.TrackingCode,
			LabelURL:                   rec.Order.LabelURL,
			CancelReason:               rec.Order.CancelReason,
			ShippingAddressJSON:        rec.Order.ShippingAddressJSON,
			PaymentExpiresAt:           rec.Order.PaymentExpiresAt,
			SalesChannel:               ordChannel,
			StorefrontShopID:           rec.Order.StorefrontShopID,
			CreatedAt:                  now,
			UpdatedAt:                  now,
		}
		m.orders[ord.ID] = ord

		items := make([]db.OrderItem, 0, len(rec.Items))
		for _, ip := range rec.Items {
			items = append(items, db.OrderItem(ip))
		}
		m.items[ord.ID] = items

		tl := make([]db.OrderTimelineEvent, 0, len(rec.Timeline))
		for _, tp := range rec.Timeline {
			tl = append(tl, db.OrderTimelineEvent{
				ID:         tp.ID,
				OrderID:    tp.OrderID,
				FromStatus: tp.FromStatus,
				ToStatus:   tp.ToStatus,
				ActorID:    tp.ActorID,
				ActorRole:  tp.ActorRole,
				Note:       tp.Note,
				CreatedAt:  now,
			})
		}
		m.timelines[ord.ID] = tl

		for _, ob := range rec.Outbox {
			m.outbox = append(m.outbox, db.OutboxEvent{
				ID:          ob.ID,
				EventType:   ob.EventType,
				TraceID:     ob.TraceID,
				Traceparent: ob.Traceparent,
				PayloadJSON: ob.PayloadJSON,
				CreatedAt:   now,
			})
		}

		out = append(out, OrderAggregate{Order: ord, Items: items, Timeline: tl})
	}
	return out, nil
}

func (m *MemoryStore) CompleteCheckoutWithPayment(_ context.Context, arg UpdateCheckoutPaymentParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for id, ord := range m.orders {
		if ord.CheckoutGroupID == arg.CheckoutGroupID {
			ord.PaymentID = arg.PaymentID
			ord.PaymentStatus = arg.PaymentStatus
			if arg.PaymentExpiresAt != nil {
				ord.PaymentExpiresAt = arg.PaymentExpiresAt
			}
			ord.UpdatedAt = now
			m.orders[id] = ord
		}
	}
	if g, ok := m.groups[arg.CheckoutGroupID]; ok {
		g.Status = "COMPLETED"
		g.PaymentID = arg.PaymentID
		g.PaymentStatus = arg.PaymentStatus
		g.QRURL = arg.QRURL
		g.QRCode = arg.QRCode
		g.PaymentExpiresAt = arg.PaymentExpiresAt
		g.TotalPayableVND = arg.TotalPayableVND
		g.ResponseSnapshotJSON = arg.ResponseSnapshotJSON
		g.UpdatedAt = now
		m.groups[arg.CheckoutGroupID] = g
	}
	return nil
}

func (m *MemoryStore) CancelCheckoutGroupOrders(_ context.Context, params CancelGroupOrdersParams) ([]db.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var cancelled []db.Order

	for id, ord := range m.orders {
		if ord.CheckoutGroupID != params.CheckoutGroupID {
			continue
		}
		cur := statemachine.StringToStatus(ord.Status)
		if cur == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			continue
		}
		if err := statemachine.ValidateTransition(cur, orderv1.OrderStatus_ORDER_STATUS_CANCELLED); err != nil {
			continue
		}
		prev := ord.Status
		ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_CANCELLED)
		ord.CancelReason = params.Reason
		if params.PaymentStatus != "" {
			ord.PaymentStatus = params.PaymentStatus
		}
		ord.UpdatedAt = now
		m.orders[id] = ord

		m.timelines[id] = append(m.timelines[id], db.OrderTimelineEvent{
			ID:         uuid.NewString(),
			OrderID:    id,
			FromStatus: prev,
			ToStatus:   ord.Status,
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       params.Reason,
			CreatedAt:  now,
		})

		payloadBytes, _ := json.Marshal(events.OrderCancelledPayload{
			OrderID:          ord.ID,
			OrderCode:        ord.OrderCode,
			BuyerID:          ord.BuyerID,
			ShopID:           ord.ShopID,
			StorefrontShopID: ord.StorefrontShopID,
			Reason:           params.Reason,
			WasPaid:          false,
		})
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderCancelled,
			TraceID:     params.TraceID,
			Traceparent: params.Traceparent,
			PayloadJSON: payloadBytes,
			CreatedAt:   now,
		})
		cancelled = append(cancelled, ord)
	}

	if g, ok := m.groups[params.CheckoutGroupID]; ok {
		g.Status = "FAILED"
		g.PaymentStatus = params.PaymentStatus
		g.UpdatedAt = now
		m.groups[params.CheckoutGroupID] = g
	}
	return cancelled, nil
}

func (m *MemoryStore) GetOrderWithDetails(_ context.Context, orderID string) (*OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getOrderLocked(orderID)
}

func (m *MemoryStore) getOrderLocked(orderID string) (*OrderAggregate, error) {
	ord, ok := m.orders[orderID]
	if !ok {
		return nil, ErrNotFound
	}
	items := append([]db.OrderItem(nil), m.items[orderID]...)
	tl := append([]db.OrderTimelineEvent(nil), m.timelines[orderID]...)
	return &OrderAggregate{Order: ord, Items: items, Timeline: tl}, nil
}

func (m *MemoryStore) ListOrdersByCheckoutGroup(_ context.Context, checkoutGroupID string) ([]OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var aggs []OrderAggregate
	for _, ord := range m.orders {
		if ord.CheckoutGroupID == checkoutGroupID {
			agg, _ := m.getOrderLocked(ord.ID)
			aggs = append(aggs, *agg)
		}
	}
	sort.Slice(aggs, func(i, j int) bool {
		return aggs[i].Order.ShopID < aggs[j].Order.ShopID
	})
	return aggs, nil
}

func (m *MemoryStore) ListOrdersFiltered(_ context.Context, buyerID, shopID, status, salesChannel, storefrontShopID string, limit, offset int32) ([]OrderAggregate, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []OrderAggregate
	for _, ord := range m.orders {
		if buyerID != "" && ord.BuyerID != buyerID {
			continue
		}
		if shopID != "" && ord.ShopID != shopID {
			continue
		}
		if status != "" && ord.Status != status {
			continue
		}
		if storefrontShopID != "" && ord.StorefrontShopID != storefrontShopID {
			continue
		}
		if salesChannel != "" {
			ordCh := ord.SalesChannel
			if ordCh == "" {
				ordCh = "MARKETPLACE"
			}
			if ordCh != salesChannel {
				continue
			}
		}
		agg, _ := m.getOrderLocked(ord.ID)
		matched = append(matched, *agg)
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].Order.CreatedAt.Equal(matched[j].Order.CreatedAt) {
			return matched[i].Order.CreatedAt.After(matched[j].Order.CreatedAt)
		}
		return matched[i].Order.ID > matched[j].Order.ID
	})
	total := int64(len(matched))
	start := int(offset)
	if start >= len(matched) {
		return []OrderAggregate{}, total, nil
	}
	end := start + int(limit)
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], total, nil
}

func (m *MemoryStore) GetOrderTimeline(_ context.Context, orderID string) ([]db.OrderTimelineEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]db.OrderTimelineEvent(nil), m.timelines[orderID]...), nil
}

func (m *MemoryStore) TransitionOrdersPaymentResult(_ context.Context, params PaymentResultTransitionParams) (*PaymentResultTransitionOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	if params.EventID != "" {
		if _, exists := m.processedEvents[params.EventID]; exists {
			return &PaymentResultTransitionOutcome{AlreadyProcessed: true}, nil
		}
		m.processedEvents[params.EventID] = db.ProcessedEvent{
			EventID:     params.EventID,
			EventType:   params.EventType,
			ProcessedAt: now,
		}
	}

	groupID := strings.TrimSpace(params.CheckoutGroupID)
	if groupID == "" && strings.TrimSpace(params.OrderID) != "" {
		if ord, ok := m.orders[params.OrderID]; ok {
			groupID = ord.CheckoutGroupID
		} else if grp, ok := m.groups[params.OrderID]; ok {
			groupID = grp.ID
		}
	}
	if groupID == "" {
		return &PaymentResultTransitionOutcome{}, nil
	}

	var targetStatus orderv1.OrderStatus
	var paymentStatusStr, defaultNote string
	switch params.ResultStatus {
	case orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED:
		targetStatus = orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
		paymentStatusStr = "SUCCEEDED"
		defaultNote = "Payment succeeded"
	case orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_EXPIRED:
		targetStatus = orderv1.OrderStatus_ORDER_STATUS_CANCELLED
		paymentStatusStr = "EXPIRED"
		defaultNote = "Payment expired"
	default:
		targetStatus = orderv1.OrderStatus_ORDER_STATUS_CANCELLED
		paymentStatusStr = "FAILED"
		defaultNote = "Payment failed"
	}

	note := strings.TrimSpace(params.Reason)
	if note == "" {
		note = defaultNote
	}

	if g, ok := m.groups[groupID]; ok {
		g.PaymentStatus = paymentStatusStr
		if params.PaymentID != "" {
			g.PaymentID = params.PaymentID
		}
		g.UpdatedAt = now
		m.groups[groupID] = g
	}

	outcome := &PaymentResultTransitionOutcome{CheckoutGroupID: groupID}
	for id, ord := range m.orders {
		if ord.CheckoutGroupID != groupID {
			continue
		}
		cur := statemachine.StringToStatus(ord.Status)
		if cur != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			if params.ResultStatus == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED &&
				cur == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED &&
				!strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") {
				ord.PaymentStatus = "SUCCEEDED"
				m.orders[id] = ord
			}
			continue
		}
		if err := statemachine.ValidateTransition(cur, targetStatus); err != nil {
			return nil, err
		}
		prev := ord.Status
		ord.Status = statemachine.StatusToString(targetStatus)
		ord.PaymentStatus = paymentStatusStr
		if params.PaymentID != "" {
			ord.PaymentID = params.PaymentID
		}
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			ord.CancelReason = note
		}
		ord.UpdatedAt = now
		m.orders[id] = ord

		m.timelines[id] = append(m.timelines[id], db.OrderTimelineEvent{
			ID:         uuid.NewString(),
			OrderID:    id,
			FromStatus: prev,
			ToStatus:   ord.Status,
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       note,
			CreatedAt:  now,
		})

		var eventType string
		var payloadBytes []byte
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			eventType = events.SubjectOrderConfirmed
			payloadBytes, _ = json.Marshal(events.OrderConfirmedPayload{
				OrderID:          ord.ID,
				OrderCode:        ord.OrderCode,
				BuyerID:          ord.BuyerID,
				ShopID:           ord.ShopID,
				StorefrontShopID: ord.StorefrontShopID,
				PaymentMethod:    ord.PaymentMethod,
				TotalPayableVND:  ord.TotalPayableVND,
			})
		} else {
			eventType = events.SubjectOrderCancelled
			payloadBytes, _ = json.Marshal(events.OrderCancelledPayload{
				OrderID:          ord.ID,
				OrderCode:        ord.OrderCode,
				BuyerID:          ord.BuyerID,
				ShopID:           ord.ShopID,
				StorefrontShopID: ord.StorefrontShopID,
				Reason:           note,
				WasPaid:          false,
			})
		}
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          uuid.NewString(),
			EventType:   eventType,
			TraceID:     params.TraceID,
			Traceparent: params.Traceparent,
			PayloadJSON: payloadBytes,
			CreatedAt:   now,
		})
		outcome.Transitioned = append(outcome.Transitioned, ord)
	}
	return outcome, nil
}

func (m *MemoryStore) TransitionOrderStatus(_ context.Context, params SingleOrderTransitionParams) (*OrderAggregate, []db.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(cur, params.TargetStatus); err != nil {
		return nil, nil, err
	}

	targets := []db.Order{ord}
	if params.CancelGroupPend && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED && cur == orderv1.OrderStatus_ORDER_STATUS_PENDING {
		targets = targets[:0]
		for _, sib := range m.orders {
			if sib.CheckoutGroupID == ord.CheckoutGroupID && statemachine.StringToStatus(sib.Status) == orderv1.OrderStatus_ORDER_STATUS_PENDING {
				targets = append(targets, sib)
			}
		}
	}

	var transitioned []db.Order
	for _, target := range targets {
		prev := target.Status
		target.Status = statemachine.StatusToString(params.TargetStatus)
		if params.CancelReason != "" {
			target.CancelReason = params.CancelReason
		}
		if params.PaymentStatus != "" {
			target.PaymentStatus = params.PaymentStatus
		}
		target.UpdatedAt = now
		m.orders[target.ID] = target

		m.timelines[target.ID] = append(m.timelines[target.ID], db.OrderTimelineEvent{
			ID:         uuid.NewString(),
			OrderID:    target.ID,
			FromStatus: prev,
			ToStatus:   target.Status,
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       params.Note,
			CreatedAt:  now,
		})
		if params.BuildOutboxFn != nil {
			for _, ob := range params.BuildOutboxFn(target) {
				m.outbox = append(m.outbox, db.OutboxEvent{
					ID:          ob.ID,
					EventType:   ob.EventType,
					TraceID:     ob.TraceID,
					Traceparent: ob.Traceparent,
					PayloadJSON: ob.PayloadJSON,
					CreatedAt:   now,
				})
			}
		}
		transitioned = append(transitioned, target)
	}

	for _, ob := range params.OutboxEvents {
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          ob.ID,
			EventType:   ob.EventType,
			TraceID:     ob.TraceID,
			Traceparent: ob.Traceparent,
			PayloadJSON: ob.PayloadJSON,
			CreatedAt:   now,
		})
	}

	agg, _ := m.getOrderLocked(params.OrderID)
	return agg, transitioned, nil
}

func (m *MemoryStore) TransitionOrderShipped(_ context.Context, params ShipOrderParams) (*OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(cur, orderv1.OrderStatus_ORDER_STATUS_SHIPPING); err != nil {
		return nil, err
	}
	prev := ord.Status
	ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_SHIPPING)
	ord.CarrierCode = params.CarrierCode
	ord.ServiceCode = params.ServiceCode
	ord.TrackingCode = params.TrackingCode
	ord.LabelURL = params.LabelURL
	ord.UpdatedAt = now
	m.orders[ord.ID] = ord

	m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: prev,
		ToStatus:   ord.Status,
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
		CreatedAt:  now,
	})
	if params.OutboxEvent.ID != "" {
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          params.OutboxEvent.ID,
			EventType:   params.OutboxEvent.EventType,
			TraceID:     params.OutboxEvent.TraceID,
			Traceparent: params.OutboxEvent.Traceparent,
			PayloadJSON: params.OutboxEvent.PayloadJSON,
			CreatedAt:   now,
		})
	}
	return m.getOrderLocked(ord.ID)
}

func (m *MemoryStore) TransitionOrderCompleted(_ context.Context, params CompleteOrderParams) (*OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(cur, orderv1.OrderStatus_ORDER_STATUS_COMPLETED); err != nil {
		return nil, err
	}
	prev := ord.Status
	ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_COMPLETED)
	ord.CompletedAt = &now
	ord.UpdatedAt = now
	m.orders[ord.ID] = ord

	m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: prev,
		ToStatus:   ord.Status,
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
		CreatedAt:  now,
	})
	if params.OutboxEvent.ID != "" {
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          params.OutboxEvent.ID,
			EventType:   params.OutboxEvent.EventType,
			TraceID:     params.OutboxEvent.TraceID,
			Traceparent: params.OutboxEvent.Traceparent,
			PayloadJSON: params.OutboxEvent.PayloadJSON,
			CreatedAt:   now,
		})
	}
	return m.getOrderLocked(ord.ID)
}

func (m *MemoryStore) TransitionOrderReturnRequested(_ context.Context, params ReturnRequestParams) (*OrderAggregate, *db.ReturnRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(cur, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED); err != nil {
		return nil, nil, err
	}
	prev := ord.Status
	ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED)
	ord.UpdatedAt = now
	m.orders[ord.ID] = ord

	rr := db.ReturnRequest{
		ID:               params.ReturnRequestID,
		OrderID:          params.OrderID,
		BuyerID:          params.BuyerID,
		Reason:           params.Reason,
		EvidenceURLsJSON: params.EvidenceURLsJSON,
		Status:           "PENDING",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	m.returnRequests[rr.ID] = rr

	m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: prev,
		ToStatus:   ord.Status,
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
		CreatedAt:  now,
	})

	agg, _ := m.getOrderLocked(ord.ID)
	return agg, &rr, nil
}

func (m *MemoryStore) ApplyShipmentUpdate(_ context.Context, params ShipmentUpdateParams) (*ShipmentUpdateOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	if params.EventID != "" {
		if _, exists := m.processedEvents[params.EventID]; exists {
			return &ShipmentUpdateOutcome{AlreadyProcessed: true}, nil
		}
		m.processedEvents[params.EventID] = db.ProcessedEvent{
			EventID:     params.EventID,
			EventType:   params.EventType,
			ProcessedAt: now,
		}
	}

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return &ShipmentUpdateOutcome{NotFound: true}, nil
	}

	cur := statemachine.StringToStatus(ord.Status)
	shipStatus := strings.ToLower(strings.TrimSpace(params.ShipmentStatus))
	note := strings.TrimSpace(params.Description)
	if note == "" {
		note = fmt.Sprintf("Shipment update: %s", shipStatus)
	}
	outcome := &ShipmentUpdateOutcome{}

	switch shipStatus {
	case "delivered":
		if cur == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
			prev := ord.Status
			ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_DELIVERED)
			if params.TrackingCode != "" {
				ord.TrackingCode = params.TrackingCode
			}
			ord.DeliveredAt = &now
			ord.UpdatedAt = now
			m.orders[ord.ID] = ord
			m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: prev,
				ToStatus:   ord.Status,
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       note,
				CreatedAt:  now,
			})
			payloadBytes, _ := json.Marshal(events.OrderDeliveredPayload{
				OrderID:          ord.ID,
				OrderCode:        ord.OrderCode,
				BuyerID:          ord.BuyerID,
				ShopID:           ord.ShopID,
				StorefrontShopID: ord.StorefrontShopID,
				TrackingCode:     ord.TrackingCode,
			})
			m.outbox = append(m.outbox, db.OutboxEvent{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderDelivered,
				TraceID:     params.TraceID,
				Traceparent: params.Traceparent,
				PayloadJSON: payloadBytes,
				CreatedAt:   now,
			})
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_DELIVERED
		}
	case "returned":
		if cur == orderv1.OrderStatus_ORDER_STATUS_SHIPPING || cur == orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED {
			prev := ord.Status
			ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED)
			ord.UpdatedAt = now
			m.orders[ord.ID] = ord
			m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: prev,
				ToStatus:   ord.Status,
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       note,
				CreatedAt:  now,
			})
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_RETURNED
		}
	default:
		if params.TrackingCode != "" {
			ord.TrackingCode = params.TrackingCode
			ord.UpdatedAt = now
			m.orders[ord.ID] = ord
		}
		m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   ord.Status,
			ActorID:    "shipping-service",
			ActorRole:  "system",
			Note:       fmt.Sprintf("[%s] %s", shipStatus, note),
			CreatedAt:  now,
		})
	}

	outcome.Order, _ = m.getOrderLocked(ord.ID)
	return outcome, nil
}

func (m *MemoryStore) RecordPaymentRefunded(_ context.Context, params PaymentRefundedParams) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	if params.EventID != "" {
		if _, exists := m.processedEvents[params.EventID]; exists {
			return true, nil
		}
		m.processedEvents[params.EventID] = db.ProcessedEvent{
			EventID:     params.EventID,
			EventType:   params.EventType,
			ProcessedAt: now,
		}
	}
	var targets []db.Order
	if ord, ok := m.orders[params.OrderID]; ok {
		targets = []db.Order{ord}
	} else {
		for _, o := range m.orders {
			if o.CheckoutGroupID == params.OrderID {
				targets = append(targets, o)
			}
		}
		if len(targets) > 0 {
			if g, ok := m.groups[params.OrderID]; ok {
				g.PaymentStatus = "REFUNDED"
				g.UpdatedAt = now
				m.groups[params.OrderID] = g
			}
		}
	}
	note := fmt.Sprintf("Refund %s processed: %d VND (%s)", params.RefundID, params.AmountVND, params.Reason)
	for _, ord := range targets {
		ord.PaymentStatus = "REFUNDED"
		ord.UpdatedAt = now
		m.orders[ord.ID] = ord
		m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   ord.Status,
			ActorID:    "payment-service",
			ActorRole:  "system",
			Note:       note,
			CreatedAt:  now,
		})
	}
	return false, nil
}

func (m *MemoryStore) IsEventProcessed(_ context.Context, eventID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.processedEvents[eventID]
	return exists, nil
}

func (m *MemoryStore) InsertDeadLetterEvent(_ context.Context, arg db.InsertDeadLetterEventParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deadLetters = append(m.deadLetters, db.DeadLetterEvent{
		ID:           arg.ID,
		Subject:      arg.Subject,
		EventID:      arg.EventID,
		Payload:      arg.Payload,
		ErrorMessage: arg.ErrorMessage,
		Attempts:     arg.Attempts,
		CreatedAt:    time.Now().UTC(),
	})
	return nil
}

func (m *MemoryStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	published := 0
	now := time.Now().UTC()
	for i := range m.outbox {
		if int32(published) >= limit {
			break
		}
		if m.outbox[i].PublishedAt != nil {
			continue
		}
		if err := publishFn(ctx, m.outbox[i]); err != nil {
			m.outbox[i].Attempts++
			m.outbox[i].LastError = err.Error()
			continue
		}
		m.outbox[i].Attempts++
		m.outbox[i].PublishedAt = &now
		m.outbox[i].LastError = ""
		published++
	}
	return published, nil
}

func (m *MemoryStore) FindExpiredPendingGroups(_ context.Context, limit int32) ([]ExpiredGroupCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	seen := make(map[string]struct{})
	var out []ExpiredGroupCandidate
	for _, ord := range m.orders {
		if ord.Status != "PENDING" || ord.PaymentMethod != "VIETQR" || ord.PaymentExpiresAt == nil {
			continue
		}
		if !ord.PaymentExpiresAt.After(now) {
			if _, ok := seen[ord.CheckoutGroupID]; ok {
				continue
			}
			seen[ord.CheckoutGroupID] = struct{}{}
			out = append(out, ExpiredGroupCandidate{
				CheckoutGroupID: ord.CheckoutGroupID,
				PaymentID:       ord.PaymentID,
			})
			if int32(len(out)) >= limit {
				break
			}
		}
	}
	return out, nil
}
