package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
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

type retryLockEntry struct {
	ch   chan struct{}
	refs int
}

// MemoryStore is a thread-safe in-memory implementation of Store for unit tests.
type MemoryStore struct {
	mu              sync.Mutex
	retryMu         sync.Mutex
	retryLocks      map[string]*retryLockEntry
	groups          map[string]db.CheckoutGroup
	orders          map[string]db.Order
	items           map[string][]db.OrderItem
	timelines       map[string][]db.OrderTimelineEvent
	returnRequests  map[string]db.ReturnRequest
	returnEvents    map[string][]db.ReturnEvent
	outbox          []db.OutboxEvent
	processedEvents map[string]db.ProcessedEvent
	deadLetters     []db.DeadLetterEvent
}

// NewMemoryStore initializes an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		retryLocks:      make(map[string]*retryLockEntry),
		groups:          make(map[string]db.CheckoutGroup),
		orders:          make(map[string]db.Order),
		items:           make(map[string][]db.OrderItem),
		timelines:       make(map[string][]db.OrderTimelineEvent),
		returnRequests:  make(map[string]db.ReturnRequest),
		returnEvents:    make(map[string][]db.ReturnEvent),
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

// SetCheckoutGroupUpdatedAt overrides updated_at for testing stale checkout group cleanup.
func (m *MemoryStore) SetCheckoutGroupUpdatedAt(groupID string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.groups[groupID]; ok {
		g.UpdatedAt = t
		m.groups[groupID] = g
	}
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

func (m *MemoryStore) GetCheckoutGroupByPaymentID(_ context.Context, paymentID string) (*db.CheckoutGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched *db.CheckoutGroup
	for _, g := range m.groups {
		if g.PaymentID == paymentID && g.Status != "FAILED" {
			if matched == nil || g.CreatedAt.After(matched.CreatedAt) {
				cp := g
				matched = &cp
			}
		}
	}
	if matched == nil {
		return nil, ErrNotFound
	}
	return matched, nil
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
	g := db.CheckoutGroup{
		ID:              arg.ID,
		BuyerID:         arg.BuyerID,
		IdempotencyKey:  arg.IdempotencyKey,
		RequestHash:     arg.RequestHash,
		Status:          arg.Status,
		PaymentMethod:   arg.PaymentMethod,
		TotalPayableVND: arg.TotalPayableVND,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	m.groups[g.ID] = g
	cp := g
	return &cp, nil
}

func (m *MemoryStore) FailCheckoutGroup(_ context.Context, id, paymentStatus string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ord := range m.orders {
		if ord.CheckoutGroupID == id && statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			return nil
		}
	}
	if g, ok := m.groups[id]; ok && g.Status == "IN_PROGRESS" {
		g.Status = "FAILED"
		g.PaymentStatus = paymentStatus
		g.NeedsCleanup = false
		g.UpdatedAt = time.Now().UTC()
		m.groups[id] = g
	}
	return nil
}

func (m *MemoryStore) ClaimStaleCheckoutGroupForCleanup(_ context.Context, id, reason string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok {
		return false, nil
	}
	for _, ord := range m.orders {
		if ord.CheckoutGroupID == id {
			return false, nil
		}
	}
	if g.Status == "FAILED" && g.NeedsCleanup {
		return true, nil
	}
	if g.Status != "IN_PROGRESS" {
		return false, nil
	}
	g.Status = "FAILED"
	if isPendingOrEmptyPaymentStatus(g.PaymentStatus) {
		g.PaymentStatus = reason
	}
	g.NeedsCleanup = true
	g.UpdatedAt = time.Now().UTC()
	m.groups[id] = g
	return true, nil
}

func (m *MemoryStore) ClearCheckoutGroupNeedsCleanup(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.groups[id]; ok {
		g.NeedsCleanup = false
		g.UpdatedAt = time.Now().UTC()
		m.groups[id] = g
	}
	return nil
}

func (m *MemoryStore) PersistCheckoutBatch(_ context.Context, batch CheckoutBatch) ([]OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	g, ok := m.groups[batch.CheckoutGroupID]
	if !ok {
		return nil, ErrNotFound
	}
	if g.Status != "IN_PROGRESS" {
		return nil, fmt.Errorf("%w: checkout group %s has status %s", ErrCheckoutGroupNotInProgress, g.ID, g.Status)
	}

	now := batch.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	out := make([]OrderAggregate, 0, len(batch.Orders))

	for _, rec := range batch.Orders {
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
			ShopProgramDiscountVND:     rec.Order.ShopProgramDiscountVND,
			ShopVoucherDiscountVND:     rec.Order.ShopVoucherDiscountVND,
			ShopFreeshipDiscountVND:    rec.Order.ShopFreeshipDiscountVND,
			PlatformVoucherDiscountVND: rec.Order.PlatformVoucherDiscountVND,
			FreeshipDiscountVND:        rec.Order.FreeshipDiscountVND,
			CoinsDiscountVND:           rec.Order.CoinsDiscountVND,
			ShippingFeeVND:             rec.Order.ShippingFeeVND,
			CommissionVND:              rec.Order.CommissionVND,
			PaymentFeeVND:              rec.Order.PaymentFeeVND,
			PaymentFeeBPS:              rec.Order.PaymentFeeBPS,
			TotalPayableVND:            rec.Order.TotalPayableVND,
			CarrierCode:                rec.Order.CarrierCode,
			ServiceCode:                rec.Order.ServiceCode,
			TrackingCode:               rec.Order.TrackingCode,
			LabelURL:                   rec.Order.LabelURL,
			CancelReason:               rec.Order.CancelReason,
			ShippingAddressJSON:        rec.Order.ShippingAddressJSON,
			PaymentExpiresAt:           rec.Order.PaymentExpiresAt,
			ConfirmedAt:                rec.Order.ConfirmedAt,
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
	if len(batch.WarningsJSON) > 0 {
		g.WarningsJSON = append([]byte(nil), batch.WarningsJSON...)
	}
	if batch.PendingStockConfirm {
		g.PendingStockConfirm = true
	}
	g.UpdatedAt = now
	m.groups[batch.CheckoutGroupID] = g
	return out, nil
}

func (m *MemoryStore) InspectCheckoutGroupOrdersLocked(_ context.Context, checkoutGroupID string) (*db.CheckoutGroup, []OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[checkoutGroupID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	var aggs []OrderAggregate
	for _, ord := range m.orders {
		if ord.CheckoutGroupID == checkoutGroupID {
			if agg, err := m.getOrderLocked(ord.ID); err == nil {
				aggs = append(aggs, *agg)
			}
		}
	}
	sort.Slice(aggs, func(i, j int) bool {
		if aggs[i].Order.ShopID != aggs[j].Order.ShopID {
			return aggs[i].Order.ShopID < aggs[j].Order.ShopID
		}
		if !aggs[i].Order.CreatedAt.Equal(aggs[j].Order.CreatedAt) {
			return aggs[i].Order.CreatedAt.Before(aggs[j].Order.CreatedAt)
		}
		return aggs[i].Order.ID < aggs[j].Order.ID
	})
	cp := g
	return &cp, aggs, nil
}

func (m *MemoryStore) CompleteCheckoutWithPayment(_ context.Context, arg UpdateCheckoutPaymentParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	g, ok := m.groups[arg.CheckoutGroupID]
	if !ok {
		return ErrNotFound
	}

	// P1 N4: Never write to orders or checkout_groups when group has already left IN_PROGRESS.
	if g.Status != "IN_PROGRESS" {
		if arg.BuildSnapshot != nil {
			var curOrders []db.Order
			for _, ord := range m.orders {
				if ord.CheckoutGroupID == arg.CheckoutGroupID {
					curOrders = append(curOrders, ord)
				}
			}
			sort.Slice(curOrders, func(i, j int) bool {
				if curOrders[i].ShopID != curOrders[j].ShopID {
					return curOrders[i].ShopID < curOrders[j].ShopID
				}
				if !curOrders[i].CreatedAt.Equal(curOrders[j].CreatedAt) {
					return curOrders[i].CreatedAt.Before(curOrders[j].CreatedAt)
				}
				return curOrders[i].ID < curOrders[j].ID
			})
			curAggs := make([]OrderAggregate, 0, len(curOrders))
			for _, ord := range curOrders {
				if agg, err := m.getOrderLocked(ord.ID); err == nil {
					curAggs = append(curAggs, *agg)
				}
			}
			_, _ = arg.BuildSnapshot(g, curAggs)
		}
		return nil
	}

	postOrders := make([]db.Order, 0)
	for id, ord := range m.orders {
		if ord.CheckoutGroupID != arg.CheckoutGroupID {
			continue
		}
		if statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
			(ord.PaymentID == "" || ord.PaymentID == arg.PaymentID) &&
			(isPendingOrEmptyPaymentStatus(ord.PaymentStatus) || ord.PaymentID == "") {
			if ord.PaymentID == "" {
				ord.PaymentID = arg.PaymentID
			}
			if isPendingOrEmptyPaymentStatus(ord.PaymentStatus) {
				ord.PaymentStatus = arg.PaymentStatus
				if arg.PaymentExpiresAt != nil {
					ord.PaymentExpiresAt = arg.PaymentExpiresAt
				}
			}
			ord.UpdatedAt = now
			m.orders[id] = ord
		}
		postOrders = append(postOrders, ord)
	}
	sort.Slice(postOrders, func(i, j int) bool {
		if postOrders[i].ShopID != postOrders[j].ShopID {
			return postOrders[i].ShopID < postOrders[j].ShopID
		}
		if !postOrders[i].CreatedAt.Equal(postOrders[j].CreatedAt) {
			return postOrders[i].CreatedAt.Before(postOrders[j].CreatedAt)
		}
		return postOrders[i].ID < postOrders[j].ID
	})

	resolvedPayStatus := resolveNonDowngradingGroupPaymentStatus(g.PaymentStatus, arg.PaymentStatus, postOrders)
	g.Status = "COMPLETED"
	if g.PaymentID == "" {
		g.PaymentID = arg.PaymentID
	}
	if isPendingOrEmptyPaymentStatus(g.PaymentStatus) {
		g.PaymentStatus = resolvedPayStatus
		if g.QRURL == "" {
			g.QRURL = arg.QRURL
		}
		if g.QRCode == "" {
			g.QRCode = arg.QRCode
		}
		if arg.PaymentExpiresAt != nil {
			g.PaymentExpiresAt = arg.PaymentExpiresAt
		}
	}
	g.TotalPayableVND = arg.TotalPayableVND
	g.NeedsCleanup = false
	g.UpdatedAt = now

	snapshotBytes := arg.ResponseSnapshotJSON
	if arg.BuildSnapshot != nil {
		postAggs := make([]OrderAggregate, 0, len(postOrders))
		for _, ord := range postOrders {
			if agg, err := m.getOrderLocked(ord.ID); err == nil {
				postAggs = append(postAggs, *agg)
			}
		}
		built, bErr := arg.BuildSnapshot(g, postAggs)
		if bErr != nil {
			return bErr
		}
		snapshotBytes = built
	}
	if snapshotBytes != nil {
		g.ResponseSnapshotJSON = snapshotBytes
	}
	m.groups[arg.CheckoutGroupID] = g
	return nil
}

func (m *MemoryStore) WithCheckoutGroupRetryLock(ctx context.Context, checkoutGroupID string, fn func() error) error {
	m.retryMu.Lock()
	if m.retryLocks == nil {
		m.retryLocks = make(map[string]*retryLockEntry)
	}
	entry, ok := m.retryLocks[checkoutGroupID]
	if !ok {
		entry = &retryLockEntry{ch: make(chan struct{}, 1)}
		m.retryLocks[checkoutGroupID] = entry
	}
	entry.refs++
	m.retryMu.Unlock()

	releaseRef := func() {
		m.retryMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(m.retryLocks, checkoutGroupID)
		}
		m.retryMu.Unlock()
	}

	select {
	case entry.ch <- struct{}{}:
	case <-ctx.Done():
		releaseRef()
		return ctx.Err()
	}
	defer func() {
		<-entry.ch
		releaseRef()
	}()
	return fn()
}

func (m *MemoryStore) UpdateGroupPaymentOnRetry(_ context.Context, params RetryPaymentUpdateParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var orderIDs []string
	alreadyApplied := params.NewPaymentID != ""
	for id, ord := range m.orders {
		if ord.CheckoutGroupID != params.CheckoutGroupID {
			continue
		}
		if statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			return fmt.Errorf("%w: order %s is in status %s", ErrRetryPrecondition, ord.ID, ord.Status)
		}
		if statemachine.StringToPaymentMethod(ord.PaymentMethod) != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR {
			return fmt.Errorf("%w: order %s payment method is %s", ErrRetryPrecondition, ord.ID, ord.PaymentMethod)
		}
		if ord.PaymentID != params.NewPaymentID {
			alreadyApplied = false
		}
		orderIDs = append(orderIDs, id)
	}
	if len(orderIDs) == 0 {
		return ErrNotFound
	}
	if alreadyApplied {
		return nil
	}
	for _, id := range orderIDs {
		ord := m.orders[id]
		if params.ExpectedPaymentID != "" && ord.PaymentID != params.ExpectedPaymentID {
			return fmt.Errorf("%w: order %s payment_id mismatch", ErrRetryPrecondition, ord.ID)
		}
		if ord.PaymentExpiresAt != nil && !ord.PaymentExpiresAt.After(now) {
			return fmt.Errorf("%w: order %s payment expired", ErrRetryPrecondition, ord.ID)
		}
	}
	updatedAt := time.Now().UTC()
	for _, id := range orderIDs {
		ord := m.orders[id]
		ord.PaymentID = params.NewPaymentID
		ord.PaymentStatus = params.PaymentStatus
		if params.PaymentExpiresAt != nil {
			exp := *params.PaymentExpiresAt
			ord.PaymentExpiresAt = &exp
		}
		ord.UpdatedAt = updatedAt
		m.orders[id] = ord
	}
	if g, ok := m.groups[params.CheckoutGroupID]; ok {
		g.PaymentID = params.NewPaymentID
		g.PaymentStatus = params.PaymentStatus
		g.QRURL = params.QRURL
		g.QRCode = params.QRCode
		if params.PaymentExpiresAt != nil {
			exp := *params.PaymentExpiresAt
			g.PaymentExpiresAt = &exp
		}
		g.ResponseSnapshotJSON = nil
		g.RetryIdempotencyKey = params.RetryIdempotencyKey
		g.RetryFromPaymentID = params.ExpectedPaymentID
		g.UpdatedAt = updatedAt
		m.groups[params.CheckoutGroupID] = g
	}
	return nil
}

func (m *MemoryStore) CancelCheckoutGroupOrders(_ context.Context, params CancelGroupOrdersParams) (*CancelGroupOrdersResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()

	// P1 M4: If group exists and already left IN_PROGRESS, refuse to cancel any order.
	if g, ok := m.groups[params.CheckoutGroupID]; ok && g.Status != "IN_PROGRESS" {
		return &CancelGroupOrdersResult{
			Cancelled:     nil,
			HasLiveOrders: true,
		}, nil
	}

	var cancelled []db.Order
	hasLiveOrders := false

	for id, ord := range m.orders {
		if ord.CheckoutGroupID != params.CheckoutGroupID {
			continue
		}
		cur := statemachine.StringToStatus(ord.Status)
		if cur == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			continue
		}
		if !canCancelGroupOrder(ord, params.OnlyPending) {
			hasLiveOrders = true
			continue
		}
		prev := ord.Status
		ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_CANCELLED)
		ord.CancelReason = params.Reason
		if ord.TotalPayableVND == 0 {
			ord.PaymentStatus = "CANCELLED"
		} else if params.PaymentStatus != "" && !isOrderPaymentTerminalProtected(ord) {
			ord.PaymentStatus = params.PaymentStatus
		}
		ord.PendingCancelCompensation = true
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
			OrderID:   ord.ID,
			OrderCode: ord.OrderCode,
			BuyerID:   ord.BuyerID,
			ShopID:    ord.ShopID,
			Reason:    params.Reason,
			WasPaid:   false,
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

	if !hasLiveOrders {
		if g, ok := m.groups[params.CheckoutGroupID]; ok {
			if g.Status == "IN_PROGRESS" {
				g.Status = "FAILED"
				g.PaymentStatus = params.PaymentStatus
				g.NeedsCleanup = false
			}
			g.PendingStockConfirm = false
			g.UpdatedAt = now
			m.groups[params.CheckoutGroupID] = g
		}
	}
	return &CancelGroupOrdersResult{
		Cancelled:     cancelled,
		HasLiveOrders: hasLiveOrders,
	}, nil
}

func (m *MemoryStore) MarkOrderPendingCancelCompensation(_ context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		ord.PendingCancelCompensation = true
		ord.UpdatedAt = time.Now().UTC()
		m.orders[orderID] = ord
	}
	return nil
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
	agg := &OrderAggregate{Order: ord, Items: items, Timeline: tl}
	for _, rr := range m.returnRequests {
		if rr.OrderID == orderID {
			agg.ReturnRequestID = rr.ID
			agg.ReturnStatus = rr.Status
			break
		}
	}
	return agg, nil
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

func (m *MemoryStore) ListOrdersFiltered(_ context.Context, buyerID, shopID, status string, limit, offset int32) ([]OrderAggregate, int64, error) {
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
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if params.EventID != "" {
		if _, exists := m.processedEvents[params.EventID]; exists {
			return &PaymentResultTransitionOutcome{AlreadyProcessed: true}, nil
		}
		if !params.SkipMarkProcessed {
			m.processedEvents[params.EventID] = db.ProcessedEvent{
				EventID:     params.EventID,
				EventType:   params.EventType,
				ProcessedAt: now,
			}
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

	groupAlreadyRefunded := false
	if g, ok := m.groups[groupID]; ok {
		if params.PaymentID != "" && g.PaymentID != "" && g.PaymentID != params.PaymentID {
			return &PaymentResultTransitionOutcome{CheckoutGroupID: groupID}, nil
		}
		groupAlreadyRefunded = strings.EqualFold(g.PaymentStatus, "REFUNDED")
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
	if override := strings.TrimSpace(params.PaymentStatusOverride); override != "" {
		paymentStatusStr = override
	}

	note := strings.TrimSpace(params.Reason)
	if note == "" {
		note = defaultNote
	}

	outcome := &PaymentResultTransitionOutcome{CheckoutGroupID: groupID}
	codUpdated := 0
	allActivePaid := true
	activeOrderCount := 0
	reqOrderID := strings.TrimSpace(params.OrderID)
	targetSpecificOrder := reqOrderID != "" && reqOrderID != groupID

	for id, ord := range m.orders {
		if ord.CheckoutGroupID != groupID {
			continue
		}
		if params.PaymentID != "" && ord.PaymentID != "" && ord.PaymentID != params.PaymentID {
			continue
		}
		cur := statemachine.StringToStatus(ord.Status)
		if cur != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			if params.ResultStatus == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED {
				if statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
					(strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") || isOrderRefunded(ord)) &&
					cur != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
					outcome.NeedsDownstream = true
				}
			} else if cur == orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
				(isFailedOrExpiredStatus(ord.PaymentStatus) ||
					(params.PaymentStatusOverride != "" && strings.EqualFold(ord.PaymentStatus, params.PaymentStatusOverride))) {
				outcome.NeedsDownstream = true
			}
			if cur == orderv1.OrderStatus_ORDER_STATUS_CANCELLED ||
				cur == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
				continue
			}
			activeOrderCount++
			ordPaid := strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") || isOrderRefunded(ord)
			if params.ResultStatus == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED &&
				statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD &&
				(cur == orderv1.OrderStatus_ORDER_STATUS_DELIVERED ||
					cur == orderv1.OrderStatus_ORDER_STATUS_COMPLETED ||
					cur == orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED) &&
				(!targetSpecificOrder || ord.ID == reqOrderID) &&
				!ordPaid {
				ord.PaymentStatus = "SUCCEEDED"
				ord.UpdatedAt = now
				m.orders[id] = ord
				codUpdated++
				ordPaid = true
			}
			if !ordPaid {
				allActivePaid = false
			}
			continue
		}
		if err := statemachine.ValidateTransition(cur, targetStatus); err != nil {
			return nil, err
		}
		prev := ord.Status
		ord.Status = statemachine.StatusToString(targetStatus)
		ordPaymentStatus := paymentStatusStr
		if isOrderRefunded(ord) {
			ordPaymentStatus = "REFUNDED"
		}
		ord.PaymentStatus = ordPaymentStatus
		if params.PaymentID != "" {
			ord.PaymentID = params.PaymentID
		}
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			ord.CancelReason = note
		}
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && ord.ConfirmedAt == nil {
			ts := now
			ord.ConfirmedAt = &ts
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
				OrderID:         ord.ID,
				OrderCode:       ord.OrderCode,
				BuyerID:         ord.BuyerID,
				ShopID:          ord.ShopID,
				PaymentMethod:   ord.PaymentMethod,
				TotalPayableVND: ord.TotalPayableVND,
			})
		} else {
			eventType = events.SubjectOrderCancelled
			payloadBytes, _ = json.Marshal(events.OrderCancelledPayload{
				OrderID:   ord.ID,
				OrderCode: ord.OrderCode,
				BuyerID:   ord.BuyerID,
				ShopID:    ord.ShopID,
				Reason:    note,
				WasPaid:   false,
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
		outcome.NeedsDownstream = true
	}

	if len(outcome.Transitioned) > 0 || (codUpdated > 0 && activeOrderCount > 0 && allActivePaid) {
		if g, ok := m.groups[groupID]; ok {
			groupPayStatus := paymentStatusStr
			if groupAlreadyRefunded {
				groupPayStatus = "REFUNDED"
			} else if len(outcome.Transitioned) > 0 {
				allTransRefunded := true
				for _, tr := range outcome.Transitioned {
					if !strings.EqualFold(tr.PaymentStatus, "REFUNDED") {
						allTransRefunded = false
						break
					}
				}
				if allTransRefunded {
					groupPayStatus = "REFUNDED"
				}
			}
			g.PaymentStatus = groupPayStatus
			if params.PaymentID != "" {
				g.PaymentID = params.PaymentID
			}
			g.UpdatedAt = now
			m.groups[groupID] = g
		}
	}
	if len(outcome.Transitioned) > 0 {
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			if g, ok := m.groups[groupID]; ok {
				g.PendingStockConfirm = true
				g.UpdatedAt = now
				m.groups[groupID] = g
			}
		} else if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			for _, tr := range outcome.Transitioned {
				if curOrd, ok := m.orders[tr.ID]; ok {
					curOrd.PendingCancelCompensation = true
					m.orders[tr.ID] = curOrd
				}
			}
		}
	}
	return outcome, nil
}

func (m *MemoryStore) TransitionOrderStatus(_ context.Context, params SingleOrderTransitionParams) (*OrderTransitionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if params.ExpectedStatus != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED && cur != params.ExpectedStatus {
		return nil, fmt.Errorf("%w: expected order status %s, got %s",
			statemachine.ErrInvalidTransition,
			statemachine.StatusToString(params.ExpectedStatus),
			statemachine.StatusToString(cur))
	}
	if params.RequireConfirmedBefore != nil {
		confirmTS := ord.UpdatedAt
		if ord.ConfirmedAt != nil {
			confirmTS = *ord.ConfirmedAt
		}
		if confirmTS.After(*params.RequireConfirmedBefore) {
			return nil, fmt.Errorf("%w: order confirmation timestamp has not exceeded timeout", statemachine.ErrInvalidTransition)
		}
	}
	if err := statemachine.ValidateTransition(cur, params.TargetStatus); err != nil {
		return nil, err
	}
	if err := checkCancelShipmentGuard(ord, params, now); err != nil {
		return nil, err
	}

	prevOrder := ord
	var siblings []db.Order
	targets := []db.Order{ord}
	hasActiveSibling := false
	if params.CancelGroupPend && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		for _, sib := range m.orders {
			if sib.CheckoutGroupID == ord.CheckoutGroupID {
				siblings = append(siblings, sib)
			}
		}
		if cur == orderv1.OrderStatus_ORDER_STATUS_PENDING {
			targets = targets[:0]
			for _, sib := range siblings {
				if statemachine.StringToStatus(sib.Status) == orderv1.OrderStatus_ORDER_STATUS_PENDING {
					targets = append(targets, sib)
				}
			}
		}
		cancellingIDs := make(map[string]struct{}, len(targets))
		for _, c := range targets {
			cancellingIDs[c.ID] = struct{}{}
		}
		for _, sib := range siblings {
			if _, cancelling := cancellingIDs[sib.ID]; cancelling {
				continue
			}
			if statemachine.StringToStatus(sib.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
				hasActiveSibling = true
				break
			}
		}
	}

	var transitioned []db.Order
	for _, target := range targets {
		prev := target.Status
		payStatus := params.PaymentStatus
		if params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED && payStatus == "" {
			ordPaid := statemachine.StringToPaymentMethod(target.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
				strings.EqualFold(target.PaymentStatus, "SUCCEEDED") &&
				target.TotalPayableVND > 0
			if !ordPaid && (isUnpaidPendingStatus(target.PaymentStatus) || target.TotalPayableVND == 0) {
				payStatus = "CANCELLED"
			}
		}
		target.Status = statemachine.StatusToString(params.TargetStatus)
		if params.CancelReason != "" {
			target.CancelReason = params.CancelReason
		}
		if payStatus != "" {
			target.PaymentStatus = payStatus
		}
		if shouldClearPendingShipmentCancelOnCancel(target, params, now) {
			target.PendingShipmentCancel = false
			target.PendingShipmentCancelAt = nil
			target.PendingShipmentCancelToken = ""
		}
		if params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			target.PendingCancelCompensation = true
		}
		target.UpdatedAt = now
		m.orders[target.ID] = target
		if target.ID == ord.ID {
			ord.PaymentStatus = target.PaymentStatus
		}

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

	if params.CancelGroupPend && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		mergedSiblings := mergeUpdatedOrders(siblings, transitioned)
		if shouldMarkGroupRefunded(mergedSiblings, "", "", "") {
			if g, ok := m.groups[ord.CheckoutGroupID]; ok {
				g.PaymentStatus = "REFUNDED"
				g.UpdatedAt = now
				m.groups[ord.CheckoutGroupID] = g
			}
		} else if shouldMarkGroupCancelled(mergedSiblings, "", "", "") {
			if g, ok := m.groups[ord.CheckoutGroupID]; ok {
				g.PaymentStatus = "CANCELLED"
				g.UpdatedAt = now
				m.groups[ord.CheckoutGroupID] = g
			}
		}
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
	return &OrderTransitionResult{
		Order:            agg,
		PrevOrder:        prevOrder,
		Transitioned:     transitioned,
		HasActiveSibling: hasActiveSibling,
	}, nil
}

func (m *MemoryStore) MarkOrderShipmentCreating(_ context.Context, orderID string, now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ord, ok := m.orders[orderID]
	if !ok {
		return "", ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if cur != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && cur != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return "", fmt.Errorf("%w: cannot create shipment from status %s", statemachine.ErrInvalidTransition, statemachine.StatusToString(cur))
	}
	token := uuid.NewString()
	markedAt := now
	ord.PendingShipmentCancel = true
	ord.PendingShipmentCancelAt = &markedAt
	ord.PendingShipmentCancelToken = token
	ord.UpdatedAt = now
	m.orders[orderID] = ord
	return token, nil
}

func (m *MemoryStore) ClearOrderPendingShipmentCancel(_ context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		ord.PendingShipmentCancel = false
		ord.PendingShipmentCancelAt = nil
		ord.PendingShipmentCancelToken = ""
		m.orders[orderID] = ord
	}
	return nil
}

func (m *MemoryStore) ClearOrderPendingShipmentCancelByToken(_ context.Context, orderID, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok && ord.PendingShipmentCancel && ord.PendingShipmentCancelToken == token {
		ord.PendingShipmentCancel = false
		ord.PendingShipmentCancelAt = nil
		ord.PendingShipmentCancelToken = ""
		m.orders[orderID] = ord
	}
	return nil
}

func (m *MemoryStore) ClearStaleOrderPendingShipmentCancel(_ context.Context, orderID, expectedToken string, cutoff time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ord, ok := m.orders[orderID]
	if !ok || !ord.PendingShipmentCancel || ord.PendingShipmentCancelToken != expectedToken {
		return false, nil
	}
	if ord.Status != "CONFIRMED" && ord.Status != "PREPARING" {
		return false, nil
	}
	markedAt := ord.UpdatedAt
	if ord.PendingShipmentCancelAt != nil {
		markedAt = *ord.PendingShipmentCancelAt
	}
	if !markedAt.Before(cutoff) {
		return false, nil
	}
	ord.PendingShipmentCancel = false
	ord.PendingShipmentCancelAt = nil
	ord.PendingShipmentCancelToken = ""
	ord.UpdatedAt = time.Now().UTC()
	m.orders[orderID] = ord
	return true, nil
}

func (m *MemoryStore) MarkOrderOrphanShipmentManualReview(_ context.Context, orderID, note string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ord, ok := m.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	ord.PendingShipmentCancel = false
	ord.PendingShipmentCancelAt = nil
	ord.PendingShipmentCancelToken = ""
	ord.OrphanShipmentNeedsManualReview = true
	ord.UpdatedAt = now
	m.orders[orderID] = ord
	m.timelines[orderID] = append(m.timelines[orderID], db.OrderTimelineEvent{
		ID:         uuid.NewString(),
		OrderID:    orderID,
		FromStatus: ord.Status,
		ToStatus:   ord.Status,
		ActorID:    "order-service",
		ActorRole:  "system",
		Note:       note,
		CreatedAt:  now,
	})
	return nil
}

func (m *MemoryStore) ClearCheckoutGroupPendingStockConfirm(_ context.Context, checkoutGroupID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.groups[checkoutGroupID]; ok {
		g.PendingStockConfirm = false
		g.UpdatedAt = time.Now().UTC()
		m.groups[checkoutGroupID] = g
	}
	return nil
}

func (m *MemoryStore) ClearOrderPendingCancelCompensation(_ context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		ord.PendingCancelCompensation = false
		ord.UpdatedAt = time.Now().UTC()
		m.orders[orderID] = ord
	}
	return nil
}

func (m *MemoryStore) FindCancelledOrdersPendingShipmentCancel(_ context.Context, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, ord := range m.orders {
		if ord.Status == "CANCELLED" && ord.PendingShipmentCancel {
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

func (m *MemoryStore) FindActiveOrdersWithStalePendingShipmentCancel(_ context.Context, cutoff time.Time, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, ord := range m.orders {
		if (ord.Status != "CONFIRMED" && ord.Status != "PREPARING") || !ord.PendingShipmentCancel {
			continue
		}
		markedAt := ord.UpdatedAt
		if ord.PendingShipmentCancelAt != nil {
			markedAt = *ord.PendingShipmentCancelAt
		}
		if markedAt.Before(cutoff) {
			matched = append(matched, ord.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random().
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) FindCheckoutGroupsPendingStockConfirm(_ context.Context, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, g := range m.groups {
		if g.PendingStockConfirm {
			matched = append(matched, g.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random().
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) ClearOrderPendingReturnCompensation(_ context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		ord.PendingReturnCompensation = false
		ord.UpdatedAt = time.Now().UTC()
		m.orders[orderID] = ord
	}
	return nil
}

func (m *MemoryStore) FindCancelledOrdersPendingCompensation(_ context.Context, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, ord := range m.orders {
		if ord.Status == "CANCELLED" && ord.PendingCancelCompensation {
			matched = append(matched, ord.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random().
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) FindReturnedOrdersPendingCompensation(_ context.Context, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []string
	for _, ord := range m.orders {
		if ord.Status == "RETURNED" && ord.PendingReturnCompensation {
			matched = append(matched, ord.ID)
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random().
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) TransitionOrderPreparedForShipment(_ context.Context, params ShipOrderParams) (*OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if cur == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		markedAt := now
		ord.PendingShipmentCancel = true
		ord.PendingShipmentCancelAt = &markedAt
		ord.PendingShipmentCancelToken = ""
		ord.UpdatedAt = now
		m.orders[ord.ID] = ord
		return nil, ErrOrphanShipmentNeedsCancel
	}
	if cur == orderv1.OrderStatus_ORDER_STATUS_SHIPPING &&
		strings.TrimSpace(params.TrackingCode) != "" &&
		strings.EqualFold(strings.TrimSpace(ord.TrackingCode), strings.TrimSpace(params.TrackingCode)) {
		wasFallback := strings.EqualFold(strings.TrimSpace(ord.CarrierCode), "fallback")
		if ord.CarrierCode == "" || wasFallback {
			ord.CarrierCode = params.CarrierCode
		}
		if ord.ServiceCode == "" || wasFallback {
			ord.ServiceCode = params.ServiceCode
		}
		if params.LabelURL != "" {
			ord.LabelURL = params.LabelURL
		}
		ord.PendingShipmentCancel = false
		ord.PendingShipmentCancelAt = nil
		ord.PendingShipmentCancelToken = ""
		ord.UpdatedAt = now
		m.orders[ord.ID] = ord
		return m.getOrderLocked(ord.ID)
	}
	if cur != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && cur != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return nil, fmt.Errorf("%w: cannot create shipment from status %s", statemachine.ErrInvalidTransition, statemachine.StatusToString(cur))
	}
	if cur == orderv1.OrderStatus_ORDER_STATUS_PREPARING &&
		strings.TrimSpace(ord.TrackingCode) != "" &&
		ord.TrackingCode == params.TrackingCode &&
		ord.LabelURL == params.LabelURL &&
		ord.CarrierCode == params.CarrierCode &&
		ord.ServiceCode == params.ServiceCode {
		if ord.PendingShipmentCancel &&
			(params.ExpectedCreatingToken == "" || ord.PendingShipmentCancelToken == params.ExpectedCreatingToken) {
			ord.PendingShipmentCancel = false
			ord.PendingShipmentCancelAt = nil
			ord.PendingShipmentCancelToken = ""
			m.orders[ord.ID] = ord
		}
		return m.getOrderLocked(ord.ID)
	}
	if params.ExpectedCreatingToken != "" && ord.PendingShipmentCancelToken != params.ExpectedCreatingToken {
		return nil, ErrStaleShipmentAttempt
	}
	prev := ord.Status
	ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_PREPARING)
	ord.CarrierCode = params.CarrierCode
	ord.ServiceCode = params.ServiceCode
	ord.TrackingCode = params.TrackingCode
	ord.LabelURL = params.LabelURL
	ord.PendingShipmentCancel = false
	ord.PendingShipmentCancelAt = nil
	ord.PendingShipmentCancelToken = ""
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
	return m.getOrderLocked(ord.ID)
}

func (m *MemoryStore) completeOrderLocked(ord db.Order, actorID, actorRole, note string, outboxEvent db.InsertOutboxEventParams, now time.Time) db.Order {
	prev := ord.Status
	ts := now
	ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_COMPLETED)
	ord.CompletedAt = &ts
	ord.SettlementRefundedVND = ord.RefundedVND
	ord.UpdatedAt = now
	m.orders[ord.ID] = ord

	m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: prev,
		ToStatus:   ord.Status,
		ActorID:    actorID,
		ActorRole:  actorRole,
		Note:       note,
		CreatedAt:  now,
	})
	if outboxEvent.ID != "" {
		m.outbox = append(m.outbox, db.OutboxEvent{
			ID:          outboxEvent.ID,
			EventType:   outboxEvent.EventType,
			TraceID:     outboxEvent.TraceID,
			Traceparent: outboxEvent.Traceparent,
			PayloadJSON: outboxEvent.PayloadJSON,
			CreatedAt:   now,
		})
	}
	return ord
}

func (m *MemoryStore) TransitionOrderCompleted(_ context.Context, params CompleteOrderParams) (*OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if cur != orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
		return nil, fmt.Errorf("%w: completing delivered order requires DELIVERED, got %s", statemachine.ErrInvalidTransition, statemachine.StatusToString(cur))
	}
	if params.RequireNoReturnRequest {
		for _, rr := range m.returnRequests {
			if rr.OrderID == ord.ID && rr.Status != "CLOSED" {
				return nil, fmt.Errorf("%w: order %s has an active return request", statemachine.ErrInvalidTransition, ord.ID)
			}
		}
	}
	m.completeOrderLocked(ord, params.ActorID, params.ActorRole, params.Note, params.OutboxEvent, now)
	return m.getOrderLocked(ord.ID)
}

func (m *MemoryStore) TransitionOrderReturnRequested(_ context.Context, params ReturnRequestParams) (*OrderAggregate, *ReturnAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	cur := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(cur, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED); err != nil {
		return nil, nil, err
	}
	for _, existing := range m.returnRequests {
		if existing.OrderID == ord.ID {
			return nil, nil, ErrDuplicateReturnRequest
		}
	}
	prev := ord.Status
	ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED)
	ord.UpdatedAt = now
	m.orders[ord.ID] = ord

	orderCode := params.OrderCode
	if orderCode == "" {
		orderCode = ord.OrderCode
	}
	shopID := params.ShopID
	if shopID == "" {
		shopID = ord.ShopID
	}
	refundAmt := params.RefundAmountVND
	if refundAmt < 0 {
		refundAmt = 0
	}
	rr := db.ReturnRequest{
		ID:                     params.ReturnRequestID,
		OrderID:                params.OrderID,
		OrderCode:              orderCode,
		BuyerID:                params.BuyerID,
		ShopID:                 shopID,
		Reason:                 params.Reason,
		EvidenceURLsJSON:       params.EvidenceURLsJSON,
		SellerEvidenceURLsJSON: []byte("[]"),
		Status:                 statemachine.ReturnStatusToString(orderv1.ReturnStatus_RETURN_STATUS_PENDING),
		RefundAmountVND:        refundAmt,
		DeadlineAt:             params.DeadlineAt,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	m.returnRequests[rr.ID] = rr

	retEv := db.ReturnEvent{
		ID:              uuid.NewString(),
		ReturnRequestID: rr.ID,
		FromStatus:      "UNSPECIFIED",
		ToStatus:        rr.Status,
		ActorID:         params.ActorID,
		ActorRole:       params.ActorRole,
		Note:            params.Reason,
		CreatedAt:       now,
	}
	m.returnEvents[rr.ID] = []db.ReturnEvent{retEv}

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

	agg, _ := m.getOrderLocked(ord.ID)
	return agg, &ReturnAggregate{Request: rr, Events: []db.ReturnEvent{retEv}}, nil
}

func (m *MemoryStore) transitionOrderToShippingLocked(ord db.Order, trackingCode, note, traceID, traceparent string, now time.Time) db.Order {
	prev := ord.Status
	ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_SHIPPING)
	if trackingCode != "" {
		ord.TrackingCode = trackingCode
	}
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
	payloadBytes, _ := json.Marshal(events.OrderShippedPayload{
		OrderID:      ord.ID,
		OrderCode:    ord.OrderCode,
		BuyerID:      ord.BuyerID,
		ShopID:       ord.ShopID,
		CarrierCode:  ord.CarrierCode,
		TrackingCode: ord.TrackingCode,
	})
	m.outbox = append(m.outbox, db.OutboxEvent{
		ID:          uuid.NewString(),
		EventType:   events.SubjectOrderShipped,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
		CreatedAt:   now,
	})
	return ord
}

func (m *MemoryStore) ApplyShipmentUpdate(_ context.Context, params ShipmentUpdateParams) (*ShipmentUpdateOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if params.EventID != "" {
		if _, exists := m.processedEvents[params.EventID]; exists {
			return &ShipmentUpdateOutcome{AlreadyProcessed: true}, nil
		}
		if !params.SkipMarkProcessed {
			m.processedEvents[params.EventID] = db.ProcessedEvent{
				EventID:     params.EventID,
				EventType:   params.EventType,
				ProcessedAt: now,
			}
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

	curTracking := strings.TrimSpace(ord.TrackingCode)
	evTracking := strings.TrimSpace(params.TrackingCode)
	if curTracking != "" && (evTracking == "" || !strings.EqualFold(evTracking, curTracking)) {
		return outcome, nil
	}
	switch cur {
	case orderv1.OrderStatus_ORDER_STATUS_PENDING,
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
		orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED:
		return outcome, nil
	case orderv1.OrderStatus_ORDER_STATUS_RETURNED:
		if !(shipStatus == "returned" && params.SkipMarkProcessed) {
			return outcome, nil
		}
	}

	switch shipStatus {
	case "delivering", "failed", "returning":
		if cur == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || cur == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			shipNote := note
			if shipStatus != "delivering" {
				shipNote = "Shipment in transit (" + shipStatus + ")"
			}
			ord = m.transitionOrderToShippingLocked(ord, params.TrackingCode, shipNote, params.TraceID, params.Traceparent, now)
			outcome.TransitionedFrom = cur
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_SHIPPING
			outcome.EmittedOutbox = true
			if shipStatus != "delivering" {
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
		} else {
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

	case "delivered":
		if cur == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || cur == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			ord = m.transitionOrderToShippingLocked(ord, params.TrackingCode, "Shipment picked up and in transit", params.TraceID, params.Traceparent, now)
			cur = orderv1.OrderStatus_ORDER_STATUS_SHIPPING
			outcome.EmittedOutbox = true
		}
		if cur == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
			prev := ord.Status
			ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_DELIVERED)
			if params.TrackingCode != "" {
				ord.TrackingCode = params.TrackingCode
			}
			ts := now
			ord.DeliveredAt = &ts
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
				OrderID:      ord.ID,
				OrderCode:    ord.OrderCode,
				BuyerID:      ord.BuyerID,
				ShopID:       ord.ShopID,
				TrackingCode: ord.TrackingCode,
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
			outcome.EmittedOutbox = true
		}

	case "returned":
		if cur == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || cur == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			ord = m.transitionOrderToShippingLocked(ord, params.TrackingCode, "Shipment picked up and in transit", params.TraceID, params.Traceparent, now)
			cur = orderv1.OrderStatus_ORDER_STATUS_SHIPPING
			outcome.EmittedOutbox = true
		}
		if cur == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
			prev := ord.Status
			wasPaid := strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") && ord.TotalPayableVND > 0
			ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED)
			ord.PendingReturnCompensation = true
			nextPayStatus := ""
			if ord.TotalPayableVND == 0 ||
				(statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD &&
					isUnpaidPendingStatus(ord.PaymentStatus)) {
				nextPayStatus = "CANCELLED"
				ord.PaymentStatus = nextPayStatus
			}
			ord.UpdatedAt = now
			m.orders[ord.ID] = ord
			if nextPayStatus != "" {
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
				FromStatus: prev,
				ToStatus:   ord.Status,
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       note,
				CreatedAt:  now,
			})
			payloadBytes, _ := json.Marshal(events.OrderReturnedPayload{
				OrderID:         ord.ID,
				OrderCode:       ord.OrderCode,
				CheckoutGroupID: ord.CheckoutGroupID,
				BuyerID:         ord.BuyerID,
				ShopID:          ord.ShopID,
				PaymentMethod:   ord.PaymentMethod,
				FromStatus:      statemachine.StatusToString(cur),
				TotalPayableVND: ord.TotalPayableVND,
				Reason:          note,
				WasPaid:         wasPaid,
			})
			m.outbox = append(m.outbox, db.OutboxEvent{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderReturned,
				TraceID:     params.TraceID,
				Traceparent: params.Traceparent,
				PayloadJSON: payloadBytes,
				CreatedAt:   now,
			})
			outcome.TransitionedFrom = cur
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_RETURNED
			outcome.EmittedOutbox = true
		} else if cur == orderv1.OrderStatus_ORDER_STATUS_RETURNED && params.SkipMarkProcessed {
			outcome.AlreadyReturned = true
			tls := m.timelines[ord.ID]
			for i := len(tls) - 1; i >= 0; i-- {
				if statemachine.StringToStatus(tls[i].ToStatus) == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
					outcome.TransitionedFrom = statemachine.StringToStatus(tls[i].FromStatus)
					break
				}
			}
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
	note := fmt.Sprintf("Refund %s processed: %d VND (%s)", params.RefundID, params.AmountVND, params.Reason)
	if ord, ok := m.orders[params.OrderID]; ok {
		if params.PaymentID == "" || ord.PaymentID == "" || ord.PaymentID == params.PaymentID {
			ord.RefundedVND += params.AmountVND
			if ord.TotalPayableVND > 0 && ord.RefundedVND >= ord.TotalPayableVND {
				ord.PaymentStatus = "REFUNDED"
			}
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
			if strings.EqualFold(ord.PaymentStatus, "REFUNDED") {
				var groupOrders []db.Order
				for _, sib := range m.orders {
					if sib.CheckoutGroupID == ord.CheckoutGroupID {
						groupOrders = append(groupOrders, sib)
					}
				}
				if shouldMarkGroupRefunded(groupOrders, ord.ID, "", ord.PaymentStatus) {
					if g, gOK := m.groups[ord.CheckoutGroupID]; gOK {
						g.PaymentStatus = "REFUNDED"
						g.UpdatedAt = now
						m.groups[ord.CheckoutGroupID] = g
					}
				}
			}
		}
		return false, nil
	}

	var matched []db.Order
	var groupPayable, groupRefunded int64
	for _, o := range m.orders {
		if o.CheckoutGroupID != params.OrderID {
			continue
		}
		if params.PaymentID != "" && o.PaymentID != "" && o.PaymentID != params.PaymentID {
			continue
		}
		matched = append(matched, o)
		groupPayable += o.TotalPayableVND
		groupRefunded += o.RefundedVND
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].ShopID != matched[j].ShopID {
			return matched[i].ShopID < matched[j].ShopID
		}
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.Before(matched[j].CreatedAt)
		}
		return matched[i].ID < matched[j].ID
	})
	isFullGroupRefund := groupPayable > 0 && groupRefunded+params.AmountVND >= groupPayable
	if len(matched) > 0 && isFullGroupRefund {
		if g, ok := m.groups[params.OrderID]; ok {
			g.PaymentStatus = "REFUNDED"
			g.UpdatedAt = now
			m.groups[params.OrderID] = g
		}
	}
	remainingToRecord := params.AmountVND
	for _, ord := range matched {
		if isFullGroupRefund {
			ord.RefundedVND = ord.TotalPayableVND
			ord.PaymentStatus = "REFUNDED"
			ord.UpdatedAt = now
			m.orders[ord.ID] = ord
		} else if remainingToRecord > 0 {
			avail := ord.TotalPayableVND - ord.RefundedVND
			if avail > 0 {
				add := min(remainingToRecord, avail)
				remainingToRecord -= add
				ord.RefundedVND += add
				if ord.TotalPayableVND > 0 && ord.RefundedVND >= ord.TotalPayableVND {
					ord.PaymentStatus = "REFUNDED"
				}
				ord.UpdatedAt = now
				m.orders[ord.ID] = ord
			}
		}
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

func (m *MemoryStore) MarkEventProcessed(_ context.Context, eventID, eventType string) error {
	if eventID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.processedEvents[eventID]; !exists {
		m.processedEvents[eventID] = db.ProcessedEvent{
			EventID:     eventID,
			EventType:   eventType,
			ProcessedAt: time.Now().UTC(),
		}
	}
	return nil
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
		}
	}
	// ponytail: random order mirrors SQL ORDER BY random() to prevent starvation behind failing rows.
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	if limit > 0 && int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryStore) MarkCheckoutGroupNeedsCleanup(_ context.Context, id, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if g, ok := m.groups[id]; ok && g.Status == "IN_PROGRESS" {
		g.NeedsCleanup = true
		if reason != "" {
			g.PaymentStatus = reason
		}
		g.UpdatedAt = time.Now().UTC()
		m.groups[id] = g
	}
	return nil
}

func (m *MemoryStore) FindStaleInProgressCheckoutGroups(_ context.Context, olderThan time.Duration, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-olderThan)

	groupsWithOrders := make(map[string]struct{}, len(m.orders))
	for _, ord := range m.orders {
		groupsWithOrders[ord.CheckoutGroupID] = struct{}{}
	}

	var matched []db.CheckoutGroup
	for _, g := range m.groups {
		isStaleInProgress := g.Status == "IN_PROGRESS" && (g.NeedsCleanup || g.UpdatedAt.Before(cutoff))
		isClaimedFailed := g.Status == "FAILED" && g.NeedsCleanup
		if !isStaleInProgress && !isClaimedFailed {
			continue
		}
		if _, hasOrders := groupsWithOrders[g.ID]; hasOrders {
			continue
		}
		matched = append(matched, g)
	}

	// ponytail: random order mirrors SQL ORDER BY random() to prevent starvation behind failing rows.
	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}

	out := make([]string, 0, len(matched))
	for _, g := range matched {
		out = append(out, g.ID)
	}
	return out, nil
}

func (m *MemoryStore) FindIncompleteCheckoutGroupsWithOrders(_ context.Context, olderThan time.Duration, limit int32) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-olderThan)

	groupsWithOrders := make(map[string]struct{}, len(m.orders))
	for _, ord := range m.orders {
		groupsWithOrders[ord.CheckoutGroupID] = struct{}{}
	}

	var matched []db.CheckoutGroup
	for _, g := range m.groups {
		if g.Status != "IN_PROGRESS" {
			continue
		}
		if !g.UpdatedAt.Before(cutoff) {
			continue
		}
		if _, hasOrders := groupsWithOrders[g.ID]; !hasOrders {
			continue
		}
		matched = append(matched, g)
	}

	rand.Shuffle(len(matched), func(i, j int) { matched[i], matched[j] = matched[j], matched[i] })
	if limit > 0 && int32(len(matched)) > limit {
		matched = matched[:limit]
	}

	out := make([]string, 0, len(matched))
	for _, g := range matched {
		out = append(out, g.ID)
	}
	return out, nil
}

// SetOrderTimestamps overrides created_at and completed_at on an order (unit-test helper).
func (m *MemoryStore) SetOrderTimestamps(orderID string, createdAt time.Time, completedAt *time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ord, ok := m.orders[orderID]; ok {
		if !createdAt.IsZero() {
			ord.CreatedAt = createdAt
		}
		if completedAt != nil {
			ts := *completedAt
			ord.CompletedAt = &ts
		}
		m.orders[orderID] = ord
	}
}

func (m *MemoryStore) ListOrdersNeedingManualReview(_ context.Context, limit, offset int32) ([]OrderAggregate, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var matched []OrderAggregate
	for _, ord := range m.orders {
		if !ord.OrphanShipmentNeedsManualReview {
			continue
		}
		agg, _ := m.getOrderLocked(ord.ID)
		matched = append(matched, *agg)
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].Order.UpdatedAt.Equal(matched[j].Order.UpdatedAt) {
			return matched[i].Order.UpdatedAt.Before(matched[j].Order.UpdatedAt)
		}
		return matched[i].Order.ID < matched[j].Order.ID
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

func (m *MemoryStore) ResolveOrderManualReview(_ context.Context, orderID, actorID, actorRole, note string, now time.Time) (*OrderAggregate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ord, ok := m.orders[orderID]
	if !ok {
		return nil, ErrNotFound
	}
	if !ord.OrphanShipmentNeedsManualReview {
		return nil, ErrOrderNotNeedingReview
	}
	ord.OrphanShipmentNeedsManualReview = false
	ord.UpdatedAt = now
	m.orders[orderID] = ord
	m.timelines[orderID] = append(m.timelines[orderID], db.OrderTimelineEvent{
		ID:         uuid.NewString(),
		OrderID:    orderID,
		FromStatus: ord.Status,
		ToStatus:   ord.Status,
		ActorID:    actorID,
		ActorRole:  actorRole,
		Note:       note,
		CreatedAt:  now,
	})
	return m.getOrderLocked(orderID)
}

var vnReportLoc = time.FixedZone("Asia/Ho_Chi_Minh", 7*3600)

func (m *MemoryStore) QueryOrderReport(_ context.Context, params OrderReportParams) (*OrderReportData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	shopID := strings.TrimSpace(params.ShopID)
	statusCounts := make(map[string]int64)
	createdByDate := make(map[string]*CreatedDailyStat)
	completedByDate := make(map[string]*CompletedDailyStat)
	type prodAgg struct {
		name     string
		units    int64
		revenue  int64
		orderIDs map[string]struct{}
	}
	prods := make(map[string]*prodAgg)

	for _, ord := range m.orders {
		if shopID != "" && ord.ShopID != shopID {
			continue
		}
		if !ord.CreatedAt.Before(params.StartTS) && ord.CreatedAt.Before(params.EndTS) {
			statusCounts[ord.Status]++
			d := ord.CreatedAt.In(vnReportLoc).Format("2006-01-02")
			cr := createdByDate[d]
			if cr == nil {
				cr = &CreatedDailyStat{Date: d}
				createdByDate[d] = cr
			}
			cr.OrderCount++
			if ord.Status == "CANCELLED" {
				cr.CancelledCount++
			}
			if ord.Status == "RETURNED" {
				cr.ReturnedCount++
			}
			if ord.Status != "PENDING" && ord.Status != "CANCELLED" && ord.Status != "RETURNED" {
				dPlat := max64(0, ord.PlatformVoucherDiscountVND) + max64(0, ord.FreeshipDiscountVND) + max64(0, ord.CoinsDiscountVND)
				cr.GMVVND += ord.TotalPayableVND + dPlat
			}
		}

		if ord.Status == "COMPLETED" && ord.CompletedAt != nil &&
			!ord.CompletedAt.Before(params.StartTS) && ord.CompletedAt.Before(params.EndTS) {
			d := ord.CompletedAt.In(vnReportLoc).Format("2006-01-02")
			cp := completedByDate[d]
			if cp == nil {
				cp = &CompletedDailyStat{Date: d}
				completedByDate[d] = cp
			}
			cp.CompletedCount++

			r := max64(0, ord.SubtotalVND-ord.FlashSaleDiscountVND-ord.ShopProgramDiscountVND-ord.ShopVoucherDiscountVND-ord.ShopFreeshipDiscountVND)
			cCat := min64(max64(0, ord.CommissionVND), r)
			cPay := min64(max64(0, ord.PaymentFeeVND), max64(0, r-cCat))
			rawNet := r - cCat - cPay
			refunded := max64(0, ord.RefundedVND)
			pre := min64(max64(0, ord.SettlementRefundedVND), refunded)
			post := refunded - pre

			sellerNetAtSettle := max64(0, rawNet-pre)
			remAfterSeller := max64(0, pre-rawNet)
			payFeeDeduct := min64(remAfterSeller, cPay)
			effPayFee := cPay - payFeeDeduct
			remAfterPayFee := remAfterSeller - payFeeDeduct
			commDeduct := min64(remAfterPayFee, cCat)
			effComm := cCat - commDeduct

			cp.NetRevenueVND += sellerNetAtSettle - post
			cp.CommissionVND += effComm
			cp.PaymentFeeVND += effPayFee

			ship := max64(0, ord.ShippingFeeVND)
			subsidy := max64(0, r+ship-ord.TotalPayableVND)
			cp.PlatformSubsidyVND += subsidy

			for _, it := range m.items[ord.ID] {
				pa := prods[it.ProductID]
				if pa == nil {
					pa = &prodAgg{name: it.ProductName, orderIDs: make(map[string]struct{})}
					prods[it.ProductID] = pa
				}
				if it.ProductName > pa.name {
					pa.name = it.ProductName
				}
				pa.units += int64(it.Quantity)
				pa.revenue += it.LineTotalVND
				pa.orderIDs[ord.ID] = struct{}{}
			}
		}
	}

	createdDaily := make([]CreatedDailyStat, 0, len(createdByDate))
	for _, v := range createdByDate {
		createdDaily = append(createdDaily, *v)
	}
	sort.Slice(createdDaily, func(i, j int) bool { return createdDaily[i].Date < createdDaily[j].Date })

	completedDaily := make([]CompletedDailyStat, 0, len(completedByDate))
	for _, v := range completedByDate {
		completedDaily = append(completedDaily, *v)
	}
	sort.Slice(completedDaily, func(i, j int) bool { return completedDaily[i].Date < completedDaily[j].Date })

	top := make([]TopProductRow, 0, len(prods))
	for pid, pa := range prods {
		top = append(top, TopProductRow{
			ProductID:   pid,
			ProductName: pa.name,
			UnitsSold:   pa.units,
			RevenueVND:  pa.revenue,
			OrderCount:  int64(len(pa.orderIDs)),
		})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].RevenueVND != top[j].RevenueVND {
			return top[i].RevenueVND > top[j].RevenueVND
		}
		if top[i].UnitsSold != top[j].UnitsSold {
			return top[i].UnitsSold > top[j].UnitsSold
		}
		return top[i].ProductID < top[j].ProductID
	})
	if params.TopLimit > 0 && int32(len(top)) > params.TopLimit {
		top = top[:params.TopLimit]
	}

	return &OrderReportData{
		StatusCounts:   statusCounts,
		CreatedDaily:   createdDaily,
		CompletedDaily: completedDaily,
		TopProducts:    top,
	}, nil
}
