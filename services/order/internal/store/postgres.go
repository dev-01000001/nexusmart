package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

// PostgresStore implements Store backed by PostgreSQL (pgx/v5 + sqlc).
type PostgresStore struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// NewPostgresStore creates a PostgresStore over pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{
		pool:    pool,
		queries: db.New(pool),
	}
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PostgresStore) GetActiveCheckoutGroup(ctx context.Context, buyerID, idempotencyKey string) (*db.CheckoutGroup, error) {
	row, err := s.queries.GetActiveCheckoutGroupByIdempotencyKey(ctx, db.GetActiveCheckoutGroupByIdempotencyKeyParams{
		BuyerID:        buyerID,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) GetCheckoutGroupByID(ctx context.Context, id string) (*db.CheckoutGroup, error) {
	row, err := s.queries.GetCheckoutGroupByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) CreateCheckoutGroup(ctx context.Context, arg db.CreateCheckoutGroupParams) (*db.CheckoutGroup, error) {
	row, err := s.queries.CreateCheckoutGroup(ctx, arg)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateIdempotencyKey
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) FailCheckoutGroup(ctx context.Context, id, paymentStatus string) error {
	return s.queries.FailCheckoutGroup(ctx, db.FailCheckoutGroupParams{
		ID:            id,
		PaymentStatus: paymentStatus,
	})
}

func (s *PostgresStore) PersistCheckoutBatch(ctx context.Context, batch CheckoutBatch) ([]OrderAggregate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin persist checkout batch tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	result := make([]OrderAggregate, 0, len(batch.Orders))

	for _, rec := range batch.Orders {
		ord, err := q.InsertOrder(ctx, rec.Order)
		if err != nil {
			return nil, fmt.Errorf("insert order %s: %w", rec.Order.ID, err)
		}

		items := make([]db.OrderItem, 0, len(rec.Items))
		for _, itemParam := range rec.Items {
			it, err := q.InsertOrderItem(ctx, itemParam)
			if err != nil {
				return nil, fmt.Errorf("insert order item %s: %w", itemParam.SKUID, err)
			}
			items = append(items, it)
		}

		timeline := make([]db.OrderTimelineEvent, 0, len(rec.Timeline))
		for _, tlParam := range rec.Timeline {
			tl, err := q.InsertTimelineEvent(ctx, tlParam)
			if err != nil {
				return nil, fmt.Errorf("insert timeline event: %w", err)
			}
			timeline = append(timeline, tl)
		}

		for _, ob := range rec.Outbox {
			if err := q.InsertOutboxEvent(ctx, ob); err != nil {
				return nil, fmt.Errorf("insert outbox event %s: %w", ob.EventType, err)
			}
		}

		result = append(result, OrderAggregate{
			Order:    ord,
			Items:    items,
			Timeline: timeline,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit persist checkout batch tx: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) CompleteCheckoutWithPayment(ctx context.Context, arg UpdateCheckoutPaymentParams) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete checkout tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	if err := q.UpdateOrderPaymentInfoByGroup(ctx, db.UpdateOrderPaymentInfoByGroupParams{
		CheckoutGroupID:  arg.CheckoutGroupID,
		PaymentID:        arg.PaymentID,
		PaymentStatus:    arg.PaymentStatus,
		PaymentExpiresAt: arg.PaymentExpiresAt,
	}); err != nil {
		return fmt.Errorf("update order payment info: %w", err)
	}

	if err := q.CompleteCheckoutGroup(ctx, db.CompleteCheckoutGroupParams{
		ID:                   arg.CheckoutGroupID,
		PaymentID:            arg.PaymentID,
		PaymentStatus:        arg.PaymentStatus,
		QRURL:                arg.QRURL,
		QRCode:               arg.QRCode,
		PaymentExpiresAt:     arg.PaymentExpiresAt,
		TotalPayableVND:      arg.TotalPayableVND,
		ResponseSnapshotJSON: arg.ResponseSnapshotJSON,
	}); err != nil {
		return fmt.Errorf("complete checkout group: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *PostgresStore) CancelCheckoutGroupOrders(ctx context.Context, params CancelGroupOrdersParams) ([]db.Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel group tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	orders, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, params.CheckoutGroupID)
	if err != nil {
		return nil, err
	}

	cancelled := make([]db.Order, 0, len(orders))
	for _, ord := range orders {
		curStatus := statemachine.StringToStatus(ord.Status)
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			continue
		}
		if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_CANCELLED); err != nil {
			continue
		}

		updated, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
			ID:            ord.ID,
			Status:        statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_CANCELLED),
			CancelReason:  params.Reason,
			PaymentStatus: params.PaymentStatus,
			PaymentID:     ord.PaymentID,
		})
		if err != nil {
			return nil, err
		}

		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_CANCELLED),
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       params.Reason,
		}); err != nil {
			return nil, err
		}

		payloadBytes, err := json.Marshal(events.OrderCancelledPayload{
			OrderID:          updated.ID,
			OrderCode:        updated.OrderCode,
			BuyerID:          updated.BuyerID,
			ShopID:           updated.ShopID,
			StorefrontShopID: updated.StorefrontShopID,
			Reason:           params.Reason,
			WasPaid:          false,
		})
		if err != nil {
			return nil, err
		}

		if err := q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			EventType:   events.SubjectOrderCancelled,
			TraceID:     params.TraceID,
			Traceparent: params.Traceparent,
			PayloadJSON: payloadBytes,
		}); err != nil {
			return nil, err
		}

		cancelled = append(cancelled, updated)
	}

	if err := q.FailCheckoutGroup(ctx, db.FailCheckoutGroupParams{
		ID:            params.CheckoutGroupID,
		PaymentStatus: params.PaymentStatus,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return cancelled, nil
}

func (s *PostgresStore) GetOrderWithDetails(ctx context.Context, orderID string) (*OrderAggregate, error) {
	return loadOrderAggregate(ctx, s.queries, orderID)
}

func (s *PostgresStore) ListOrdersByCheckoutGroup(ctx context.Context, checkoutGroupID string) ([]OrderAggregate, error) {
	rows, err := s.queries.ListOrdersByCheckoutGroup(ctx, checkoutGroupID)
	if err != nil {
		return nil, err
	}
	aggs := make([]OrderAggregate, 0, len(rows))
	for _, ord := range rows {
		items, err := s.queries.ListOrderItemsByOrderID(ctx, ord.ID)
		if err != nil {
			return nil, err
		}
		tl, err := s.queries.ListOrderTimelineByOrderID(ctx, ord.ID)
		if err != nil {
			return nil, err
		}
		aggs = append(aggs, OrderAggregate{Order: ord, Items: items, Timeline: tl})
	}
	return aggs, nil
}

func (s *PostgresStore) ListOrdersFiltered(ctx context.Context, buyerID, shopID, status, salesChannel, storefrontShopID string, limit, offset int32) ([]OrderAggregate, int64, error) {
	total, err := s.queries.CountOrdersFiltered(ctx, db.CountOrdersFilteredParams{
		BuyerID:          buyerID,
		ShopID:           shopID,
		Status:           status,
		SalesChannel:     salesChannel,
		StorefrontShopID: storefrontShopID,
	})
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.queries.ListOrdersFiltered(ctx, db.ListOrdersFilteredParams{
		BuyerID:          buyerID,
		ShopID:           shopID,
		Status:           status,
		SalesChannel:     salesChannel,
		StorefrontShopID: storefrontShopID,
		PageLimit:        limit,
		PageOffset:       offset,
	})
	if err != nil {
		return nil, 0, err
	}

	aggs := make([]OrderAggregate, 0, len(rows))
	for _, ord := range rows {
		items, err := s.queries.ListOrderItemsByOrderID(ctx, ord.ID)
		if err != nil {
			return nil, 0, err
		}
		tl, err := s.queries.ListOrderTimelineByOrderID(ctx, ord.ID)
		if err != nil {
			return nil, 0, err
		}
		aggs = append(aggs, OrderAggregate{Order: ord, Items: items, Timeline: tl})
	}
	return aggs, total, nil
}

func (s *PostgresStore) GetOrderTimeline(ctx context.Context, orderID string) ([]db.OrderTimelineEvent, error) {
	return s.queries.ListOrderTimelineByOrderID(ctx, orderID)
}

func (s *PostgresStore) TransitionOrdersPaymentResult(ctx context.Context, params PaymentResultTransitionParams) (*PaymentResultTransitionOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin payment result tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	if params.EventID != "" {
		inserted, err := q.TryMarkEventProcessed(ctx, db.TryMarkEventProcessedParams{
			EventID:   params.EventID,
			EventType: params.EventType,
		})
		if err != nil {
			return nil, fmt.Errorf("mark event processed: %w", err)
		}
		if inserted == 0 {
			return &PaymentResultTransitionOutcome{AlreadyProcessed: true}, nil
		}
	}

	groupID := strings.TrimSpace(params.CheckoutGroupID)
	if groupID == "" && strings.TrimSpace(params.OrderID) != "" {
		ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				if grp, gErr := q.GetCheckoutGroupByID(ctx, params.OrderID); gErr == nil {
					groupID = grp.ID
				} else {
					if err := tx.Commit(ctx); err != nil {
						return nil, err
					}
					return &PaymentResultTransitionOutcome{}, nil
				}
			} else {
				return nil, err
			}
		} else {
			groupID = ord.CheckoutGroupID
		}
	}
	if groupID == "" {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &PaymentResultTransitionOutcome{}, nil
	}

	orders, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, groupID)
	if err != nil {
		return nil, err
	}

	var targetStatus orderv1.OrderStatus
	var paymentStatusStr string
	var defaultNote string

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

	if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
		ID:            groupID,
		PaymentStatus: paymentStatusStr,
		PaymentID:     params.PaymentID,
	}); err != nil {
		return nil, err
	}

	outcome := &PaymentResultTransitionOutcome{
		CheckoutGroupID: groupID,
		Transitioned:    make([]db.Order, 0, len(orders)),
	}

	for _, ord := range orders {
		curStatus := statemachine.StringToStatus(ord.Status)
		if curStatus != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			// Idempotent: COD orders are already CONFIRMED when payment.succeeded arrives,
			// and duplicate notifications should never fail or double-emit outbox events.
			if params.ResultStatus == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED &&
				curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED &&
				!strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") {
				_ = q.UpdateOrderPaymentStatus(ctx, db.UpdateOrderPaymentStatusParams{
					ID:            ord.ID,
					PaymentStatus: "SUCCEEDED",
				})
			}
			continue
		}

		if err := statemachine.ValidateTransition(curStatus, targetStatus); err != nil {
			return nil, err
		}

		cancelReason := ""
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			cancelReason = note
		}

		updated, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
			ID:            ord.ID,
			Status:        statemachine.StatusToString(targetStatus),
			CancelReason:  cancelReason,
			PaymentStatus: paymentStatusStr,
			PaymentID:     params.PaymentID,
		})
		if err != nil {
			return nil, err
		}

		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   statemachine.StatusToString(targetStatus),
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       note,
		}); err != nil {
			return nil, err
		}

		var eventType string
		var payloadBytes []byte
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			eventType = events.SubjectOrderConfirmed
			payloadBytes, err = json.Marshal(events.OrderConfirmedPayload{
				OrderID:          updated.ID,
				OrderCode:        updated.OrderCode,
				BuyerID:          updated.BuyerID,
				ShopID:           updated.ShopID,
				StorefrontShopID: updated.StorefrontShopID,
				PaymentMethod:    updated.PaymentMethod,
				TotalPayableVND:  updated.TotalPayableVND,
			})
		} else {
			eventType = events.SubjectOrderCancelled
			payloadBytes, err = json.Marshal(events.OrderCancelledPayload{
				OrderID:          updated.ID,
				OrderCode:        updated.OrderCode,
				BuyerID:          updated.BuyerID,
				ShopID:           updated.ShopID,
				StorefrontShopID: updated.StorefrontShopID,
				Reason:           note,
				WasPaid:          false,
			})
		}
		if err != nil {
			return nil, err
		}

		if err := q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
			ID:          uuid.NewString(),
			EventType:   eventType,
			TraceID:     params.TraceID,
			Traceparent: params.Traceparent,
			PayloadJSON: payloadBytes,
		}); err != nil {
			return nil, err
		}

		outcome.Transitioned = append(outcome.Transitioned, updated)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return outcome, nil
}

func (s *PostgresStore) TransitionOrderStatus(ctx context.Context, params SingleOrderTransitionParams) (*OrderAggregate, []db.Order, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin transition order tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	var ord db.Order
	var toCancel []db.Order

	if params.CancelGroupPend && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		// Non-locking lookup of checkout_group_id first, then lock all group rows in deterministic
		// (shop_id ASC, created_at ASC, id ASC) order to prevent AB-BA deadlocks with payment.succeeded.
		peek, err := q.GetOrderByID(ctx, params.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, ErrNotFound
			}
			return nil, nil, err
		}
		siblings, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, peek.CheckoutGroupID)
		if err != nil {
			return nil, nil, err
		}
		found := false
		for _, sib := range siblings {
			if sib.ID == params.OrderID {
				ord = sib
				found = true
				break
			}
		}
		if !found {
			return nil, nil, ErrNotFound
		}

		curStatus := statemachine.StringToStatus(ord.Status)
		if err := statemachine.ValidateTransition(curStatus, params.TargetStatus); err != nil {
			return nil, nil, err
		}

		if curStatus == orderv1.OrderStatus_ORDER_STATUS_PENDING {
			for _, sib := range siblings {
				if statemachine.StringToStatus(sib.Status) == orderv1.OrderStatus_ORDER_STATUS_PENDING {
					toCancel = append(toCancel, sib)
				}
			}
		} else {
			toCancel = []db.Order{ord}
		}
	} else {
		var err error
		ord, err = q.GetOrderByIDForUpdate(ctx, params.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, ErrNotFound
			}
			return nil, nil, err
		}

		curStatus := statemachine.StringToStatus(ord.Status)
		if err := statemachine.ValidateTransition(curStatus, params.TargetStatus); err != nil {
			return nil, nil, err
		}
		toCancel = []db.Order{ord}
	}

	transitioned := make([]db.Order, 0, len(toCancel))
	for _, targetOrd := range toCancel {
		updated, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
			ID:            targetOrd.ID,
			Status:        statemachine.StatusToString(params.TargetStatus),
			CancelReason:  params.CancelReason,
			PaymentStatus: params.PaymentStatus,
			PaymentID:     targetOrd.PaymentID,
		})
		if err != nil {
			return nil, nil, err
		}

		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    targetOrd.ID,
			FromStatus: targetOrd.Status,
			ToStatus:   statemachine.StatusToString(params.TargetStatus),
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       params.Note,
		}); err != nil {
			return nil, nil, err
		}

		if params.BuildOutboxFn != nil {
			for _, ob := range params.BuildOutboxFn(updated) {
				if err := q.InsertOutboxEvent(ctx, ob); err != nil {
					return nil, nil, err
				}
			}
		}
		transitioned = append(transitioned, updated)
	}

	for _, ob := range params.OutboxEvents {
		if err := q.InsertOutboxEvent(ctx, ob); err != nil {
			return nil, nil, err
		}
	}

	agg, err := loadOrderAggregate(ctx, q, params.OrderID)
	if err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return agg, transitioned, nil
}

func (s *PostgresStore) TransitionOrderShipped(ctx context.Context, params ShipOrderParams) (*OrderAggregate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin ship order tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_SHIPPING); err != nil {
		return nil, err
	}

	if _, err := q.UpdateOrderShipped(ctx, db.UpdateOrderShippedParams{
		ID:           params.OrderID,
		CarrierCode:  params.CarrierCode,
		ServiceCode:  params.ServiceCode,
		TrackingCode: params.TrackingCode,
		LabelURL:     params.LabelURL,
	}); err != nil {
		return nil, err
	}

	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    params.OrderID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_SHIPPING),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
	}); err != nil {
		return nil, err
	}

	if params.OutboxEvent.ID != "" {
		if err := q.InsertOutboxEvent(ctx, params.OutboxEvent); err != nil {
			return nil, err
		}
	}

	agg, err := loadOrderAggregate(ctx, q, params.OrderID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return agg, nil
}

func (s *PostgresStore) TransitionOrderCompleted(ctx context.Context, params CompleteOrderParams) (*OrderAggregate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin complete order tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_COMPLETED); err != nil {
		return nil, err
	}

	if _, err := q.UpdateOrderCompleted(ctx, params.OrderID); err != nil {
		return nil, err
	}

	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    params.OrderID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_COMPLETED),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
	}); err != nil {
		return nil, err
	}

	if params.OutboxEvent.ID != "" {
		if err := q.InsertOutboxEvent(ctx, params.OutboxEvent); err != nil {
			return nil, err
		}
	}

	agg, err := loadOrderAggregate(ctx, q, params.OrderID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return agg, nil
}

func (s *PostgresStore) TransitionOrderReturnRequested(ctx context.Context, params ReturnRequestParams) (*OrderAggregate, *db.ReturnRequest, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin return request tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, err
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED); err != nil {
		return nil, nil, err
	}

	if _, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
		ID:     params.OrderID,
		Status: statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED),
	}); err != nil {
		return nil, nil, err
	}

	retReq, err := q.InsertReturnRequest(ctx, db.InsertReturnRequestParams{
		ID:               params.ReturnRequestID,
		OrderID:          params.OrderID,
		BuyerID:          params.BuyerID,
		ShopID:           params.ShopID,
		ItemsJSON:        defaultJSONArray(params.ItemsJSON),
		Reason:           params.Reason,
		EvidenceURLsJSON: defaultJSONArray(params.EvidenceURLsJSON),
		Status:           "PENDING",
		RefundAmountVND:  params.RefundAmountVND,
	})
	if err != nil {
		return nil, nil, err
	}

	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    params.OrderID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
	}); err != nil {
		return nil, nil, err
	}

	if params.OutboxEvent.ID != "" {
		if err := q.InsertOutboxEvent(ctx, params.OutboxEvent); err != nil {
			return nil, nil, err
		}
	}

	agg, err := loadOrderAggregate(ctx, q, params.OrderID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return agg, &retReq, nil
}

func (s *PostgresStore) GetReturnRequestByID(ctx context.Context, id string) (*db.ReturnRequest, error) {
	row, err := s.queries.GetReturnRequestByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) GetLatestReturnRequestByOrderID(ctx context.Context, orderID string) (*db.ReturnRequest, error) {
	row, err := s.queries.GetLatestReturnRequestByOrderID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) ListReturnRequestsFiltered(ctx context.Context, orderID, buyerID, shopID, status string, limit, offset int32) ([]db.ReturnRequest, int64, error) {
	total, err := s.queries.CountReturnRequestsFiltered(ctx, db.CountReturnRequestsFilteredParams{
		OrderID: orderID,
		BuyerID: buyerID,
		ShopID:  shopID,
		Status:  status,
	})
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.queries.ListReturnRequestsFiltered(ctx, db.ListReturnRequestsFilteredParams{
		OrderID:    orderID,
		BuyerID:    buyerID,
		ShopID:     shopID,
		Status:     status,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *PostgresStore) ApproveReturnReturning(ctx context.Context, params ApproveReturnReturningParams) (*OrderAggregate, *db.ReturnRequest, *db.Complaint, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("begin approve return tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	var resolvedComplaint *db.Complaint
	if params.ComplaintID != "" {
		cp, err := q.GetComplaintByIDForUpdate(ctx, params.ComplaintID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil, nil, ErrNotFound
			}
			return nil, nil, nil, err
		}
		if cp.Status != "OPEN" {
			return nil, nil, nil, ErrComplaintNotOpen
		}
	}

	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, err
	}

	rr, err := q.GetReturnRequestByIDForUpdate(ctx, params.ReturnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, err
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED); err != nil {
		return nil, nil, nil, err
	}
	if err := statemachine.ValidateTransition(orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED, orderv1.OrderStatus_ORDER_STATUS_RETURNING); err != nil {
		return nil, nil, nil, err
	}

	if _, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
		ID:     ord.ID,
		Status: statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNING),
	}); err != nil {
		return nil, nil, nil, err
	}

	updatedRR, err := q.UpdateReturnRequest(ctx, db.UpdateReturnRequestParams{
		ID:                  rr.ID,
		Status:              "RETURNING",
		SellerNote:          params.SellerNote,
		ReverseTrackingCode: params.ReverseTrackingCode,
		ReverseLabelURL:     params.ReverseLabelURL,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.ApproveNote,
	}); err != nil {
		return nil, nil, nil, err
	}
	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED),
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNING),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.ReturningNote,
	}); err != nil {
		return nil, nil, nil, err
	}

	if params.ComplaintID != "" {
		cp, err := q.ResolveComplaintRow(ctx, db.ResolveComplaintRowParams{
			ID:         params.ComplaintID,
			Status:     params.ComplaintStatus,
			Resolution: params.ComplaintResolution,
			AdminNote:  params.AdminNote,
			ResolvedBy: params.ResolvedBy,
		})
		if err != nil {
			return nil, nil, nil, err
		}
		resolvedComplaint = &cp
	}

	for _, ob := range params.OutboxEvents {
		if ob.ID != "" {
			if err := q.InsertOutboxEvent(ctx, ob); err != nil {
				return nil, nil, nil, err
			}
		}
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, err
	}
	return agg, &updatedRR, resolvedComplaint, nil
}

func (s *PostgresStore) CompleteReturnRefund(ctx context.Context, params CompleteReturnRefundParams) (*CompleteReturnRefundOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin complete return refund tx: %w", err)
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
			return &CompleteReturnRefundOutcome{AlreadyProcessed: true}, nil
		}
	}

	var lockedComplaint *db.Complaint
	if params.ComplaintID != "" {
		cp, err := q.GetComplaintByIDForUpdate(ctx, params.ComplaintID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if cp.Status != "OPEN" {
			return nil, ErrComplaintNotOpen
		}
		lockedComplaint = &cp
	}

	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	var rr *db.ReturnRequest
	if params.ReturnRequestID != "" {
		row, err := q.GetReturnRequestByIDForUpdate(ctx, params.ReturnRequestID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		rr = &row
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if curStatus == orderv1.OrderStatus_ORDER_STATUS_REFUNDED {
		agg, err := loadOrderAggregate(ctx, q, ord.ID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &CompleteReturnRefundOutcome{
			AlreadyRefunded: true,
			Order:           agg,
			ReturnRequest:   rr,
			Complaint:       lockedComplaint,
		}, nil
	}

	fromForRefund := ord.Status
	if params.IntermediateStatus != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED && curStatus != params.IntermediateStatus {
		if err := statemachine.ValidateTransition(curStatus, params.IntermediateStatus); err != nil {
			return nil, err
		}
		if err := statemachine.ValidateTransition(params.IntermediateStatus, orderv1.OrderStatus_ORDER_STATUS_REFUNDED); err != nil {
			return nil, err
		}
		interStr := statemachine.StatusToString(params.IntermediateStatus)
		interActor := params.IntermediateActorID
		if interActor == "" {
			interActor = params.ActorID
		}
		interRole := params.IntermediateActorRole
		if interRole == "" {
			interRole = params.ActorRole
		}
		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   interStr,
			ActorID:    interActor,
			ActorRole:  interRole,
			Note:       params.IntermediateNote,
		}); err != nil {
			return nil, err
		}
		fromForRefund = interStr
	} else {
		if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_REFUNDED); err != nil {
			return nil, err
		}
	}

	if _, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
		ID:            ord.ID,
		Status:        statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_REFUNDED),
		PaymentStatus: params.PaymentStatus,
	}); err != nil {
		return nil, err
	}

	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: fromForRefund,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_REFUNDED),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
	}); err != nil {
		return nil, err
	}

	if rr != nil {
		updatedRR, err := q.UpdateReturnRequest(ctx, db.UpdateReturnRequestParams{
			ID:              rr.ID,
			Status:          "REFUNDED",
			SellerNote:      params.SellerNote,
			RefundAmountVND: params.RefundAmountVND,
			RefundID:        params.RefundID,
		})
		if err != nil {
			return nil, err
		}
		rr = &updatedRR
	}

	if params.ComplaintID != "" {
		cp, err := q.ResolveComplaintRow(ctx, db.ResolveComplaintRowParams{
			ID:         params.ComplaintID,
			Status:     params.ComplaintStatus,
			Resolution: params.ComplaintResolution,
			AdminNote:  params.AdminNote,
			ResolvedBy: params.ResolvedBy,
		})
		if err != nil {
			return nil, err
		}
		lockedComplaint = &cp
	}

	for _, ob := range params.OutboxEvents {
		if ob.ID != "" {
			if err := q.InsertOutboxEvent(ctx, ob); err != nil {
				return nil, err
			}
		}
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &CompleteReturnRefundOutcome{
		Order:         agg,
		ReturnRequest: rr,
		Complaint:     lockedComplaint,
	}, nil
}

func (s *PostgresStore) RejectReturnAndCreateComplaint(ctx context.Context, params RejectReturnParams) (*OrderAggregate, *db.ReturnRequest, *db.Complaint, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("begin reject return tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, err
	}

	rr, err := q.GetReturnRequestByIDForUpdate(ctx, params.ReturnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, err
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED); err != nil {
		return nil, nil, nil, err
	}

	if _, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
		ID:     ord.ID,
		Status: statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED),
	}); err != nil {
		return nil, nil, nil, err
	}

	updatedRR, err := q.UpdateReturnRequest(ctx, db.UpdateReturnRequestParams{
		ID:         rr.ID,
		Status:     "REJECTED",
		SellerNote: params.Reason,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REJECTED),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
	}); err != nil {
		return nil, nil, nil, err
	}

	cp, cpErr := q.GetComplaintByReturnRequestID(ctx, rr.ID)
	if cpErr != nil {
		if !errors.Is(cpErr, pgx.ErrNoRows) {
			return nil, nil, nil, cpErr
		}
		evJSON := params.EvidenceURLsJSON
		if len(evJSON) == 0 {
			evJSON = defaultJSONArray(rr.EvidenceURLsJSON)
		}
		shopID := rr.ShopID
		if shopID == "" {
			shopID = ord.ShopID
		}
		cp, err = q.InsertComplaint(ctx, db.InsertComplaintParams{
			ID:               params.ComplaintID,
			ReturnRequestID:  rr.ID,
			OrderID:          ord.ID,
			BuyerID:          ord.BuyerID,
			ShopID:           shopID,
			Reason:           params.ComplaintReason,
			EvidenceURLsJSON: defaultJSONArray(evJSON),
			Status:           "OPEN",
		})
		if err != nil {
			return nil, nil, nil, err
		}
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, err
	}
	return agg, &updatedRR, &cp, nil
}

func (s *PostgresStore) GetComplaintByID(ctx context.Context, id string) (*db.Complaint, error) {
	row, err := s.queries.GetComplaintByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) GetComplaintByReturnRequestID(ctx context.Context, returnRequestID string) (*db.Complaint, error) {
	row, err := s.queries.GetComplaintByReturnRequestID(ctx, returnRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) UpsertOpenComplaint(ctx context.Context, params UpsertComplaintParams) (*db.Complaint, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin upsert complaint tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	existing, err := q.GetComplaintByReturnRequestID(ctx, params.ReturnRequestID)
	if err == nil {
		if existing.Status != "OPEN" {
			return nil, ErrComplaintNotOpen
		}
		updated, err := q.UpdateComplaintDetails(ctx, db.UpdateComplaintDetailsParams{
			ID:               existing.ID,
			Reason:           params.Reason,
			UpdateEvidence:   params.UpdateEvidence,
			EvidenceURLsJSON: defaultJSONArray(params.EvidenceURLsJSON),
		})
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &updated, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	created, err := q.InsertComplaint(ctx, db.InsertComplaintParams{
		ID:               params.ComplaintID,
		ReturnRequestID:  params.ReturnRequestID,
		OrderID:          params.OrderID,
		BuyerID:          params.BuyerID,
		ShopID:           params.ShopID,
		Reason:           params.Reason,
		EvidenceURLsJSON: defaultJSONArray(params.EvidenceURLsJSON),
		Status:           "OPEN",
	})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *PostgresStore) ListComplaintsFiltered(ctx context.Context, status, orderID, shopID string, limit, offset int32) ([]db.Complaint, int64, error) {
	total, err := s.queries.CountComplaintsFiltered(ctx, db.CountComplaintsFilteredParams{
		Status:  status,
		OrderID: orderID,
		ShopID:  shopID,
	})
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.queries.ListComplaintsFiltered(ctx, db.ListComplaintsFilteredParams{
		Status:     status,
		OrderID:    orderID,
		ShopID:     shopID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (s *PostgresStore) DismissComplaintAndCompleteOrder(ctx context.Context, params DismissComplaintParams) (*OrderAggregate, *db.ReturnRequest, *db.Complaint, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("begin dismiss complaint tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	cp, err := q.GetComplaintByIDForUpdate(ctx, params.ComplaintID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, err
	}
	if cp.Status != "OPEN" {
		return nil, nil, nil, ErrComplaintNotOpen
	}

	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, err
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_COMPLETED); err != nil {
		return nil, nil, nil, err
	}

	if _, err := q.UpdateOrderCompleted(ctx, ord.ID); err != nil {
		return nil, nil, nil, err
	}

	resolvedCP, err := q.ResolveComplaintRow(ctx, db.ResolveComplaintRowParams{
		ID:         cp.ID,
		Status:     "DISMISSED",
		Resolution: "DISMISS",
		AdminNote:  params.AdminNote,
		ResolvedBy: params.ResolvedBy,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_COMPLETED),
		ActorID:    params.ResolvedBy,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
	}); err != nil {
		return nil, nil, nil, err
	}

	if params.OutboxEvent.ID != "" {
		if err := q.InsertOutboxEvent(ctx, params.OutboxEvent); err != nil {
			return nil, nil, nil, err
		}
	}

	var rrPtr *db.ReturnRequest
	if rr, err := q.GetReturnRequestByID(ctx, cp.ReturnRequestID); err == nil {
		rrPtr = &rr
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, err
	}
	return agg, rrPtr, &resolvedCP, nil
}

func defaultJSONArray(b []byte) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

func (s *PostgresStore) ApplyShipmentUpdate(ctx context.Context, params ShipmentUpdateParams) (*ShipmentUpdateOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin shipment update tx: %w", err)
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
			return &ShipmentUpdateOutcome{AlreadyProcessed: true}, nil
		}
	}

	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ShipmentUpdateOutcome{NotFound: true}, nil
		}
		return nil, err
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	shipStatus := strings.ToLower(strings.TrimSpace(params.ShipmentStatus))
	isReturnDir := strings.EqualFold(strings.TrimSpace(params.Direction), "return")
	note := strings.TrimSpace(params.Description)
	if note == "" {
		note = fmt.Sprintf("Shipment update: %s", shipStatus)
	}

	outcome := &ShipmentUpdateOutcome{}

	switch {
	case shipStatus == "delivered" && !isReturnDir:
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
			if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_DELIVERED); err != nil {
				return nil, err
			}
			updated, err := q.UpdateOrderDelivered(ctx, db.UpdateOrderDeliveredParams{
				ID:           ord.ID,
				TrackingCode: params.TrackingCode,
			})
			if err != nil {
				return nil, err
			}
			if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: ord.Status,
				ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_DELIVERED),
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       note,
			}); err != nil {
				return nil, err
			}
			payloadBytes, err := json.Marshal(events.OrderDeliveredPayload{
				OrderID:          updated.ID,
				OrderCode:        updated.OrderCode,
				BuyerID:          updated.BuyerID,
				ShopID:           updated.ShopID,
				StorefrontShopID: updated.StorefrontShopID,
				TrackingCode:     updated.TrackingCode,
			})
			if err != nil {
				return nil, err
			}
			if err := q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderDelivered,
				TraceID:     params.TraceID,
				Traceparent: params.Traceparent,
				PayloadJSON: payloadBytes,
			}); err != nil {
				return nil, err
			}
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_DELIVERED
		}
	case shipStatus == "returned":
		canReturn := curStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING ||
			curStatus == orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED ||
			curStatus == orderv1.OrderStatus_ORDER_STATUS_RETURNING ||
			curStatus == orderv1.OrderStatus_ORDER_STATUS_RETURN_APPROVED
		if canReturn {
			if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURNED); err != nil {
				return nil, err
			}
			if _, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
				ID:     ord.ID,
				Status: statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED),
			}); err != nil {
				return nil, err
			}
			if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: ord.Status,
				ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED),
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       note,
			}); err != nil {
				return nil, err
			}
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_RETURNED
		}
	default:
		if params.TrackingCode != "" && !isReturnDir {
			if err := q.UpdateOrderTrackingCode(ctx, db.UpdateOrderTrackingCodeParams{
				ID:           ord.ID,
				TrackingCode: params.TrackingCode,
			}); err != nil {
				return nil, err
			}
		}
		tlNote := fmt.Sprintf("[%s] %s", shipStatus, note)
		if isReturnDir {
			tlNote = fmt.Sprintf("[return:%s] %s", shipStatus, note)
		}
		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   ord.Status,
			ActorID:    "shipping-service",
			ActorRole:  "system",
			Note:       tlNote,
		}); err != nil {
			return nil, err
		}
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}
	outcome.Order = agg

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return outcome, nil
}

func (s *PostgresStore) RecordPaymentRefunded(ctx context.Context, params PaymentRefundedParams) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	if params.EventID != "" {
		inserted, err := q.TryMarkEventProcessed(ctx, db.TryMarkEventProcessedParams{
			EventID:   params.EventID,
			EventType: params.EventType,
		})
		if err != nil {
			return false, err
		}
		if inserted == 0 {
			return true, nil
		}
	}

	if params.OrderID != "" {
		var targets []db.Order
		ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
		if err == nil {
			targets = []db.Order{ord}
		} else if errors.Is(err, pgx.ErrNoRows) {
			// Group-level refund: payment.refunded carries checkout_group_id in order_id.
			groupOrders, gErr := q.ListOrdersByCheckoutGroupForUpdate(ctx, params.OrderID)
			if gErr != nil {
				return false, gErr
			}
			targets = groupOrders
			if len(targets) > 0 {
				if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
					ID:            params.OrderID,
					PaymentStatus: "REFUNDED",
					PaymentID:     "",
				}); err != nil {
					return false, err
				}
			}
		} else {
			return false, err
		}

		note := fmt.Sprintf("Refund %s processed: %d VND (%s)", params.RefundID, params.AmountVND, params.Reason)
		for _, target := range targets {
			if err := q.UpdateOrderPaymentStatus(ctx, db.UpdateOrderPaymentStatusParams{
				ID:            target.ID,
				PaymentStatus: "REFUNDED",
			}); err != nil {
				return false, err
			}
			if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    target.ID,
				FromStatus: target.Status,
				ToStatus:   target.Status,
				ActorID:    "payment-service",
				ActorRole:  "system",
				Note:       note,
			}); err != nil {
				return false, err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return false, nil
}

func (s *PostgresStore) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	return s.queries.IsEventProcessed(ctx, eventID)
}

func (s *PostgresStore) InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error {
	return s.queries.InsertDeadLetterEvent(ctx, arg)
}

func (s *PostgresStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin outbox tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	rows, err := q.LockPendingOutboxEvents(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("lock pending outbox events: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	published := 0
	for _, ev := range rows {
		if pubErr := publishFn(ctx, ev); pubErr != nil {
			_ = q.MarkOutboxEventAttemptFailed(ctx, db.MarkOutboxEventAttemptFailedParams{
				ID:        ev.ID,
				LastError: pubErr.Error(),
			})
			continue
		}
		if err := q.MarkOutboxEventPublished(ctx, ev.ID); err != nil {
			return published, fmt.Errorf("mark outbox event published: %w", err)
		}
		published++
	}

	if err := tx.Commit(ctx); err != nil {
		return published, fmt.Errorf("commit outbox tx: %w", err)
	}
	return published, nil
}

func (s *PostgresStore) FindExpiredPendingGroups(ctx context.Context, limit int32) ([]ExpiredGroupCandidate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	rows, err := q.LockExpiredPendingOrders(ctx, limit)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(rows))
	candidates := make([]ExpiredGroupCandidate, 0, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.CheckoutGroupID]; ok {
			continue
		}
		seen[row.CheckoutGroupID] = struct{}{}
		candidates = append(candidates, ExpiredGroupCandidate{
			CheckoutGroupID: row.CheckoutGroupID,
			PaymentID:       row.PaymentID,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return candidates, nil
}

func loadOrderAggregate(ctx context.Context, q *db.Queries, orderID string) (*OrderAggregate, error) {
	ord, err := q.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	items, err := q.ListOrderItemsByOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	tl, err := q.ListOrderTimelineByOrderID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return &OrderAggregate{
		Order:    ord,
		Items:    items,
		Timeline: tl,
	}, nil
}
