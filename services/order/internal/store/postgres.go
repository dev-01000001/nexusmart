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

func (s *PostgresStore) GetCheckoutGroupByPaymentID(ctx context.Context, paymentID string) (*db.CheckoutGroup, error) {
	row, err := s.queries.GetCheckoutGroupByPaymentID(ctx, paymentID)
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

	if batch.PendingStockConfirm {
		if err := q.SetCheckoutGroupPendingStockConfirm(ctx, db.SetCheckoutGroupPendingStockConfirmParams{
			ID:                  batch.CheckoutGroupID,
			PendingStockConfirm: true,
		}); err != nil {
			return nil, fmt.Errorf("mark checkout group pending stock confirm: %w", err)
		}
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

func (s *PostgresStore) WithCheckoutGroupRetryLock(ctx context.Context, checkoutGroupID string, fn func() error) error {
	// Review2-O P1-3: hold a Postgres transaction-scoped advisory lock across check -> CreatePayment -> DB update
	// so concurrent RetryPayment calls across replicas serialize cleanly.
	tx, err := s.pool.Begin(context.WithoutCancel(ctx))
	if err != nil {
		return fmt.Errorf("begin retry advisory lock tx: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = tx.Rollback(closeCtx)
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('order_retry:' || $1, 0))", checkoutGroupID); err != nil {
		return fmt.Errorf("acquire retry advisory lock: %w", err)
	}
	return fn()
}

func (s *PostgresStore) UpdateGroupPaymentOnRetry(ctx context.Context, params RetryPaymentUpdateParams) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin retry payment update tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	orders, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, params.CheckoutGroupID)
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return ErrNotFound
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	alreadyApplied := params.NewPaymentID != ""
	for _, ord := range orders {
		if statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			return fmt.Errorf("%w: order %s is in status %s", ErrRetryPrecondition, ord.ID, ord.Status)
		}
		if statemachine.StringToPaymentMethod(ord.PaymentMethod) != orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR {
			return fmt.Errorf("%w: order %s payment method is %s", ErrRetryPrecondition, ord.ID, ord.PaymentMethod)
		}
		if ord.PaymentID != params.NewPaymentID {
			alreadyApplied = false
		}
	}
	if alreadyApplied {
		// Review-A P0-3: idempotent success if another replica already committed this exact new payment_id.
		return tx.Commit(ctx)
	}
	for _, ord := range orders {
		if params.ExpectedPaymentID != "" && ord.PaymentID != params.ExpectedPaymentID {
			return fmt.Errorf("%w: order %s payment_id mismatch", ErrRetryPrecondition, ord.ID)
		}
		if ord.PaymentExpiresAt != nil && !ord.PaymentExpiresAt.After(now) {
			return fmt.Errorf("%w: order %s payment expired", ErrRetryPrecondition, ord.ID)
		}
	}

	if err := q.UpdateOrderPaymentInfoByGroup(ctx, db.UpdateOrderPaymentInfoByGroupParams{
		CheckoutGroupID:  params.CheckoutGroupID,
		PaymentID:        params.NewPaymentID,
		PaymentStatus:    params.PaymentStatus,
		PaymentExpiresAt: params.PaymentExpiresAt,
	}); err != nil {
		return fmt.Errorf("update orders payment info on retry: %w", err)
	}

	if err := q.UpdateCheckoutGroupPaymentOnRetry(ctx, db.UpdateCheckoutGroupPaymentOnRetryParams{
		ID:                  params.CheckoutGroupID,
		PaymentID:           params.NewPaymentID,
		PaymentStatus:       params.PaymentStatus,
		QRURL:               params.QRURL,
		QRCode:              params.QRCode,
		PaymentExpiresAt:    params.PaymentExpiresAt,
		RetryIdempotencyKey: params.RetryIdempotencyKey,
		RetryFromPaymentID:  params.ExpectedPaymentID,
	}); err != nil {
		return fmt.Errorf("update checkout group payment on retry: %w", err)
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
		if err := q.SetOrderPendingCancelCompensation(ctx, db.SetOrderPendingCancelCompensationParams{
			ID:                        ord.ID,
			PendingCancelCompensation: true,
		}); err != nil {
			return nil, err
		}
		updated.PendingCancelCompensation = true

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
			OrderID:   updated.ID,
			OrderCode: updated.OrderCode,
			BuyerID:   updated.BuyerID,
			ShopID:    updated.ShopID,
			Reason:    params.Reason,
			WasPaid:   false,
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
	if err := q.SetCheckoutGroupPendingStockConfirm(ctx, db.SetCheckoutGroupPendingStockConfirmParams{
		ID:                  params.CheckoutGroupID,
		PendingStockConfirm: false,
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
	orderIDs := make([]string, 0, len(rows))
	for _, ord := range rows {
		orderIDs = append(orderIDs, ord.ID)
	}
	retByOrder, err := loadReturnSummaries(ctx, s.queries, orderIDs)
	if err != nil {
		return nil, err
	}
	for _, ord := range rows {
		items, err := s.queries.ListOrderItemsByOrderID(ctx, ord.ID)
		if err != nil {
			return nil, err
		}
		tl, err := s.queries.ListOrderTimelineByOrderID(ctx, ord.ID)
		if err != nil {
			return nil, err
		}
		ret := retByOrder[ord.ID]
		aggs = append(aggs, OrderAggregate{
			Order:           ord,
			Items:           items,
			Timeline:        tl,
			ReturnRequestID: ret.ID,
			ReturnStatus:    ret.Status,
		})
	}
	return aggs, nil
}

func (s *PostgresStore) ListOrdersFiltered(ctx context.Context, buyerID, shopID, status string, limit, offset int32) ([]OrderAggregate, int64, error) {
	total, err := s.queries.CountOrdersFiltered(ctx, db.CountOrdersFilteredParams{
		BuyerID: buyerID,
		ShopID:  shopID,
		Status:  status,
	})
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.queries.ListOrdersFiltered(ctx, db.ListOrdersFilteredParams{
		BuyerID:    buyerID,
		ShopID:     shopID,
		Status:     status,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, 0, err
	}

	orderIDs := make([]string, 0, len(rows))
	for _, ord := range rows {
		orderIDs = append(orderIDs, ord.ID)
	}
	retByOrder, err := loadReturnSummaries(ctx, s.queries, orderIDs)
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
		ret := retByOrder[ord.ID]
		aggs = append(aggs, OrderAggregate{
			Order:           ord,
			Items:           items,
			Timeline:        tl,
			ReturnRequestID: ret.ID,
			ReturnStatus:    ret.Status,
		})
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
		if params.SkipMarkProcessed {
			processed, err := q.IsEventProcessed(ctx, params.EventID)
			if err != nil {
				return nil, fmt.Errorf("check event processed: %w", err)
			}
			if processed {
				return &PaymentResultTransitionOutcome{AlreadyProcessed: true}, nil
			}
		} else {
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
	}

	groupID := strings.TrimSpace(params.CheckoutGroupID)
	if groupID == "" && strings.TrimSpace(params.OrderID) != "" {
		// Review2-O P2-6: non-locking lookup first so all locks are acquired in canonical group order below.
		ord, err := q.GetOrderByID(ctx, params.OrderID)
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

	grp, grpErr := q.GetCheckoutGroupByID(ctx, groupID)
	if grpErr != nil && !errors.Is(grpErr, pgx.ErrNoRows) {
		return nil, grpErr
	}
	if grpErr == nil && params.PaymentID != "" && grp.PaymentID != "" && grp.PaymentID != params.PaymentID {
		// PAY-B6: ignore payment events belonging to a superseded/non-current payment_id.
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &PaymentResultTransitionOutcome{CheckoutGroupID: groupID}, nil
	}
	groupAlreadyRefunded := grpErr == nil && strings.EqualFold(grp.PaymentStatus, "REFUNDED")

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
	if override := strings.TrimSpace(params.PaymentStatusOverride); override != "" {
		paymentStatusStr = override
	}

	note := strings.TrimSpace(params.Reason)
	if note == "" {
		note = defaultNote
	}

	outcome := &PaymentResultTransitionOutcome{
		CheckoutGroupID: groupID,
		Transitioned:    make([]db.Order, 0, len(orders)),
	}

	codUpdated := 0
	allActivePaid := true
	activeOrderCount := 0
	reqOrderID := strings.TrimSpace(params.OrderID)
	targetSpecificOrder := reqOrderID != "" && reqOrderID != groupID

	for _, ord := range orders {
		if params.PaymentID != "" && ord.PaymentID != "" && ord.PaymentID != params.PaymentID {
			continue
		}
		curStatus := statemachine.StringToStatus(ord.Status)
		if curStatus != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			// Review2-O P1-1: signal idempotent downstream completion on retries/redeliveries
			// regardless of EventID (covers MarkPaymentResult, watchdog->payment.failed rescue,
			// and payment.succeeded redelivery after seller already shipped the order).
			if params.ResultStatus == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED {
				if statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
					(strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") || isOrderRefunded(ord)) &&
					curStatus != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
					outcome.NeedsDownstream = true
				}
			} else if curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
				(isFailedOrExpiredStatus(ord.PaymentStatus) ||
					(params.PaymentStatusOverride != "" && strings.EqualFold(ord.PaymentStatus, params.PaymentStatusOverride))) {
				outcome.NeedsDownstream = true
			}
			// PAY-B6: never mutate CANCELLED or RETURNED orders on late payment.succeeded.
			if curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED ||
				curStatus == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
				continue
			}
			activeOrderCount++
			// Review2-O P1-2: treat REFUNDED as already paid so out-of-order payment.succeeded never downgrades it.
			ordPaid := strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") || isOrderRefunded(ord)
			// R2 COD: payment.succeeded arrives when a COD sub-order is delivered.
			if params.ResultStatus == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED &&
				statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD &&
				(curStatus == orderv1.OrderStatus_ORDER_STATUS_DELIVERED ||
					curStatus == orderv1.OrderStatus_ORDER_STATUS_COMPLETED ||
					curStatus == orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED) &&
				(!targetSpecificOrder || ord.ID == reqOrderID) &&
				!ordPaid {
				if err := q.UpdateOrderPaymentStatus(ctx, db.UpdateOrderPaymentStatusParams{
					ID:            ord.ID,
					PaymentStatus: "SUCCEEDED",
				}); err != nil {
					return nil, err
				}
				codUpdated++
				ordPaid = true
			}
			if !ordPaid {
				allActivePaid = false
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
		ordPaymentStatus := paymentStatusStr
		if isOrderRefunded(ord) {
			ordPaymentStatus = "REFUNDED"
		}

		updated, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
			ID:            ord.ID,
			Status:        statemachine.StatusToString(targetStatus),
			CancelReason:  cancelReason,
			PaymentStatus: ordPaymentStatus,
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
				OrderID:         updated.ID,
				OrderCode:       updated.OrderCode,
				BuyerID:         updated.BuyerID,
				ShopID:          updated.ShopID,
				PaymentMethod:   updated.PaymentMethod,
				TotalPayableVND: updated.TotalPayableVND,
			})
		} else {
			eventType = events.SubjectOrderCancelled
			payloadBytes, err = json.Marshal(events.OrderCancelledPayload{
				OrderID:   updated.ID,
				OrderCode: updated.OrderCode,
				BuyerID:   updated.BuyerID,
				ShopID:    updated.ShopID,
				Reason:    note,
				WasPaid:   false,
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
		outcome.NeedsDownstream = true
	}

	if len(outcome.Transitioned) > 0 || (codUpdated > 0 && activeOrderCount > 0 && allActivePaid) {
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
		if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
			ID:            groupID,
			PaymentStatus: groupPayStatus,
			PaymentID:     params.PaymentID,
		}); err != nil {
			return nil, err
		}
	}
	if len(outcome.Transitioned) > 0 {
		if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED {
			if err := q.SetCheckoutGroupPendingStockConfirm(ctx, db.SetCheckoutGroupPendingStockConfirmParams{
				ID:                  groupID,
				PendingStockConfirm: true,
			}); err != nil {
				return nil, err
			}
		} else if targetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			for _, tr := range outcome.Transitioned {
				if err := q.SetOrderPendingCancelCompensation(ctx, db.SetOrderPendingCancelCompensationParams{
					ID:                        tr.ID,
					PendingCancelCompensation: true,
				}); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return outcome, nil
}

func (s *PostgresStore) TransitionOrderStatus(ctx context.Context, params SingleOrderTransitionParams) (*OrderTransitionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transition order tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	var ord db.Order
	var siblings []db.Order
	var toCancel []db.Order
	hasActiveSibling := false
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if params.CancelGroupPend && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		// Non-locking lookup of checkout_group_id first, then lock all group rows in deterministic
		// (shop_id ASC, created_at ASC, id ASC) order to prevent AB-BA deadlocks with payment.succeeded.
		peek, err := q.GetOrderByID(ctx, params.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		siblings, err = q.ListOrdersByCheckoutGroupForUpdate(ctx, peek.CheckoutGroupID)
		if err != nil {
			return nil, err
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
			return nil, ErrNotFound
		}

		curStatus := statemachine.StringToStatus(ord.Status)
		if params.ExpectedStatus != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED && curStatus != params.ExpectedStatus {
			return nil, fmt.Errorf("%w: expected order status %s, got %s",
				statemachine.ErrInvalidTransition,
				statemachine.StatusToString(params.ExpectedStatus),
				statemachine.StatusToString(curStatus))
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
		if err := statemachine.ValidateTransition(curStatus, params.TargetStatus); err != nil {
			return nil, err
		}
		if err := checkCancelShipmentGuard(ord, params, now); err != nil {
			return nil, err
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

		cancellingIDs := make(map[string]struct{}, len(toCancel))
		for _, c := range toCancel {
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
	} else {
		var err error
		ord, err = q.GetOrderByIDForUpdate(ctx, params.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}

		curStatus := statemachine.StringToStatus(ord.Status)
		if params.ExpectedStatus != orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED && curStatus != params.ExpectedStatus {
			return nil, fmt.Errorf("%w: expected order status %s, got %s",
				statemachine.ErrInvalidTransition,
				statemachine.StatusToString(params.ExpectedStatus),
				statemachine.StatusToString(curStatus))
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
		if err := statemachine.ValidateTransition(curStatus, params.TargetStatus); err != nil {
			return nil, err
		}
		if err := checkCancelShipmentGuard(ord, params, now); err != nil {
			return nil, err
		}
		toCancel = []db.Order{ord}
	}

	prevOrder := ord
	transitioned := make([]db.Order, 0, len(toCancel))
	for _, targetOrd := range toCancel {
		payStatus := params.PaymentStatus
		if params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED && payStatus == "" {
			ordPaid := statemachine.StringToPaymentMethod(targetOrd.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_VIETQR &&
				strings.EqualFold(targetOrd.PaymentStatus, "SUCCEEDED") &&
				targetOrd.TotalPayableVND > 0
			if !ordPaid && (isUnpaidPendingStatus(targetOrd.PaymentStatus) || targetOrd.TotalPayableVND == 0) {
				payStatus = "CANCELLED"
			}
		}

		updated, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
			ID:            targetOrd.ID,
			Status:        statemachine.StatusToString(params.TargetStatus),
			CancelReason:  params.CancelReason,
			PaymentStatus: payStatus,
			PaymentID:     targetOrd.PaymentID,
		})
		if err != nil {
			return nil, err
		}
		if shouldClearPendingShipmentCancelOnCancel(targetOrd, params, now) {
			if err := q.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
				ID:                    targetOrd.ID,
				PendingShipmentCancel: false,
				MarkedAt:              time.Time{},
				Token:                 "",
			}); err != nil {
				return nil, err
			}
			updated.PendingShipmentCancel = false
			updated.PendingShipmentCancelAt = nil
			updated.PendingShipmentCancelToken = ""
		}
		if params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			if err := q.SetOrderPendingCancelCompensation(ctx, db.SetOrderPendingCancelCompensationParams{
				ID:                        targetOrd.ID,
				PendingCancelCompensation: true,
			}); err != nil {
				return nil, err
			}
			updated.PendingCancelCompensation = true
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
			return nil, err
		}

		if params.BuildOutboxFn != nil {
			for _, ob := range params.BuildOutboxFn(updated) {
				if err := q.InsertOutboxEvent(ctx, ob); err != nil {
					return nil, err
				}
			}
		}
		transitioned = append(transitioned, updated)
	}

	if params.CancelGroupPend && params.TargetStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		mergedSiblings := mergeUpdatedOrders(siblings, transitioned)
		if shouldMarkGroupRefunded(mergedSiblings, "", "", "") {
			if _, gErr := q.GetCheckoutGroupByIDForUpdate(ctx, ord.CheckoutGroupID); gErr == nil {
				if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
					ID:            ord.CheckoutGroupID,
					PaymentStatus: "REFUNDED",
					PaymentID:     "",
				}); err != nil {
					return nil, err
				}
			}
		} else if shouldMarkGroupCancelled(mergedSiblings, "", "", "") {
			if _, gErr := q.GetCheckoutGroupByIDForUpdate(ctx, ord.CheckoutGroupID); gErr == nil {
				if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
					ID:            ord.CheckoutGroupID,
					PaymentStatus: "CANCELLED",
					PaymentID:     "",
				}); err != nil {
					return nil, err
				}
			}
		}
	}

	for _, ob := range params.OutboxEvents {
		if err := q.InsertOutboxEvent(ctx, ob); err != nil {
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
	return &OrderTransitionResult{
		Order:            agg,
		PrevOrder:        prevOrder,
		Transitioned:     transitioned,
		HasActiveSibling: hasActiveSibling,
	}, nil
}

func (s *PostgresStore) MarkOrderShipmentCreating(ctx context.Context, orderID string, now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin mark shipment creating tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	ord, err := q.GetOrderByIDForUpdate(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	curStatus := statemachine.StringToStatus(ord.Status)
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return "", fmt.Errorf("%w: cannot create shipment from status %s", statemachine.ErrInvalidTransition, statemachine.StatusToString(curStatus))
	}
	// Always refresh pending_shipment_cancel_at and rotate token on every SellerShipOrder attempt/retry.
	token := uuid.NewString()
	if err := q.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
		ID:                    ord.ID,
		PendingShipmentCancel: true,
		MarkedAt:              now,
		Token:                 token,
	}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return token, nil
}

func (s *PostgresStore) ClearOrderPendingShipmentCancel(ctx context.Context, orderID string) error {
	return s.queries.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
		ID:                    orderID,
		PendingShipmentCancel: false,
		MarkedAt:              time.Time{},
		Token:                 "",
	})
}

func (s *PostgresStore) ClearOrderPendingShipmentCancelByToken(ctx context.Context, orderID, token string) error {
	_, err := s.queries.ClearOrderPendingShipmentCancelByToken(ctx, db.ClearOrderPendingShipmentCancelByTokenParams{
		ID:                         orderID,
		PendingShipmentCancelToken: token,
	})
	return err
}

func (s *PostgresStore) ClearStaleOrderPendingShipmentCancel(ctx context.Context, orderID, expectedToken string, cutoff time.Time) (bool, error) {
	rows, err := s.queries.ClearStaleOrderPendingShipmentCancel(ctx, db.ClearStaleOrderPendingShipmentCancelParams{
		ID:                         orderID,
		PendingShipmentCancelToken: expectedToken,
		Cutoff:                     cutoff,
	})
	return rows > 0, err
}

func (s *PostgresStore) MarkOrderOrphanShipmentManualReview(ctx context.Context, orderID, note string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin orphan manual review tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	ord, err := q.GetOrderByIDForUpdate(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := q.MarkOrderOrphanShipmentManualReview(ctx, ord.ID); err != nil {
		return err
	}
	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   ord.Status,
		ActorID:    "order-service",
		ActorRole:  "system",
		Note:       note,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PostgresStore) ClearCheckoutGroupPendingStockConfirm(ctx context.Context, checkoutGroupID string) error {
	return s.queries.SetCheckoutGroupPendingStockConfirm(ctx, db.SetCheckoutGroupPendingStockConfirmParams{
		ID:                  checkoutGroupID,
		PendingStockConfirm: false,
	})
}

func (s *PostgresStore) ClearOrderPendingCancelCompensation(ctx context.Context, orderID string) error {
	return s.queries.SetOrderPendingCancelCompensation(ctx, db.SetOrderPendingCancelCompensationParams{
		ID:                        orderID,
		PendingCancelCompensation: false,
	})
}

func (s *PostgresStore) ClearOrderPendingReturnCompensation(ctx context.Context, orderID string) error {
	return s.queries.SetOrderPendingReturnCompensation(ctx, db.SetOrderPendingReturnCompensationParams{
		ID:                        orderID,
		PendingReturnCompensation: false,
	})
}

func (s *PostgresStore) FindCancelledOrdersPendingShipmentCancel(ctx context.Context, limit int32) ([]string, error) {
	return s.queries.FindCancelledOrdersPendingShipmentCancel(ctx, limit)
}

func (s *PostgresStore) FindActiveOrdersWithStalePendingShipmentCancel(ctx context.Context, cutoff time.Time, limit int32) ([]string, error) {
	return s.queries.FindActiveOrdersWithStalePendingShipmentCancel(ctx, db.FindActiveOrdersWithStalePendingShipmentCancelParams{
		Cutoff:     cutoff,
		BatchLimit: limit,
	})
}

func (s *PostgresStore) FindCheckoutGroupsPendingStockConfirm(ctx context.Context, limit int32) ([]string, error) {
	return s.queries.FindCheckoutGroupsPendingStockConfirm(ctx, limit)
}

func (s *PostgresStore) FindCancelledOrdersPendingCompensation(ctx context.Context, limit int32) ([]string, error) {
	return s.queries.FindCancelledOrdersPendingCompensation(ctx, limit)
}

func (s *PostgresStore) FindReturnedOrdersPendingCompensation(ctx context.Context, limit int32) ([]string, error) {
	return s.queries.FindReturnedOrdersPendingCompensation(ctx, limit)
}

func (s *PostgresStore) TransitionOrderPreparedForShipment(ctx context.Context, params ShipOrderParams) (*OrderAggregate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin prepare shipment tx: %w", err)
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

	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	curStatus := statemachine.StringToStatus(ord.Status)
	if curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		// Record durable flag before returning so transient CancelShipment failures are retried by watchdog.
		if err := q.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
			ID:                    ord.ID,
			PendingShipmentCancel: true,
			MarkedAt:              now,
			Token:                 "",
		}); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrOrphanShipmentNeedsCancel
	}
	// P3-3: if delivering webhook beat TransitionOrderPreparedForShipment with the same tracking code,
	// persist label_url / clear pending_shipment_cancel idempotently without downgrading SHIPPING.
	if curStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING &&
		strings.TrimSpace(params.TrackingCode) != "" &&
		strings.EqualFold(strings.TrimSpace(ord.TrackingCode), strings.TrimSpace(params.TrackingCode)) {
		if _, err := q.UpdateOrderShippingLabel(ctx, db.UpdateOrderShippingLabelParams{
			ID:          ord.ID,
			CarrierCode: params.CarrierCode,
			ServiceCode: params.ServiceCode,
			LabelURL:    params.LabelURL,
		}); err != nil {
			return nil, err
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
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return nil, fmt.Errorf("%w: cannot create shipment from status %s", statemachine.ErrInvalidTransition, statemachine.StatusToString(curStatus))
	}

	// Idempotent replay only when shipping returned the exact same live shipment details.
	if curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING &&
		strings.TrimSpace(ord.TrackingCode) != "" &&
		ord.TrackingCode == params.TrackingCode &&
		ord.LabelURL == params.LabelURL &&
		ord.CarrierCode == params.CarrierCode &&
		ord.ServiceCode == params.ServiceCode {
		if ord.PendingShipmentCancel &&
			(params.ExpectedCreatingToken == "" || ord.PendingShipmentCancelToken == params.ExpectedCreatingToken) {
			if err := q.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
				ID:                    ord.ID,
				PendingShipmentCancel: false,
				MarkedAt:              time.Time{},
				Token:                 "",
			}); err != nil {
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

	// Guard against stale/expired SellerShipOrder attempts whose flag was cleared or superseded by a newer retry.
	if params.ExpectedCreatingToken != "" && ord.PendingShipmentCancelToken != params.ExpectedCreatingToken {
		return nil, ErrStaleShipmentAttempt
	}

	if _, err := q.UpdateOrderPreparingWithShipment(ctx, db.UpdateOrderPreparingWithShipmentParams{
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
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_PREPARING),
		ActorID:    params.ActorID,
		ActorRole:  params.ActorRole,
		Note:       params.Note,
	}); err != nil {
		return nil, err
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

func completeOrderInTx(
	ctx context.Context,
	q *db.Queries,
	ord db.Order,
	actorID, actorRole, note string,
	outboxEvent db.InsertOutboxEventParams,
) (db.Order, error) {
	updated, err := q.UpdateOrderCompleted(ctx, ord.ID)
	if err != nil {
		return db.Order{}, err
	}
	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_COMPLETED),
		ActorID:    actorID,
		ActorRole:  actorRole,
		Note:       note,
	}); err != nil {
		return db.Order{}, err
	}
	if outboxEvent.ID != "" {
		if err := q.InsertOutboxEvent(ctx, outboxEvent); err != nil {
			return db.Order{}, err
		}
	}
	return updated, nil
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
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
		return nil, fmt.Errorf("%w: completing delivered order requires DELIVERED, got %s", statemachine.ErrInvalidTransition, statemachine.StatusToString(curStatus))
	}
	if params.RequireNoReturnRequest {
		hasRet, err := q.HasActiveReturnRequestForOrder(ctx, ord.ID)
		if err != nil {
			return nil, err
		}
		if hasRet {
			return nil, fmt.Errorf("%w: order %s has an active return request", statemachine.ErrInvalidTransition, ord.ID)
		}
	}

	if _, err := completeOrderInTx(ctx, q, ord, params.ActorID, params.ActorRole, params.Note, params.OutboxEvent); err != nil {
		return nil, err
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

func (s *PostgresStore) TransitionOrderReturnRequested(ctx context.Context, params ReturnRequestParams) (*OrderAggregate, *ReturnAggregate, error) {
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
	hasRet, err := q.HasReturnRequestForOrder(ctx, ord.ID)
	if err != nil {
		return nil, nil, err
	}
	if hasRet {
		return nil, nil, ErrDuplicateReturnRequest
	}

	if _, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
		ID:     params.OrderID,
		Status: statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED),
	}); err != nil {
		return nil, nil, err
	}

	refundAmt := params.RefundAmountVND
	if refundAmt < 0 {
		refundAmt = 0
	}
	orderCode := params.OrderCode
	if orderCode == "" {
		orderCode = ord.OrderCode
	}
	shopID := params.ShopID
	if shopID == "" {
		shopID = ord.ShopID
	}

	retReq, err := q.InsertReturnRequest(ctx, db.InsertReturnRequestParams{
		ID:               params.ReturnRequestID,
		OrderID:          params.OrderID,
		OrderCode:        orderCode,
		BuyerID:          params.BuyerID,
		ShopID:           shopID,
		Reason:           params.Reason,
		EvidenceURLsJSON: params.EvidenceURLsJSON,
		Status:           statemachine.ReturnStatusToString(orderv1.ReturnStatus_RETURN_STATUS_PENDING),
		RefundAmountVND:  refundAmt,
		DeadlineAt:       params.DeadlineAt,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, nil, ErrDuplicateReturnRequest
		}
		return nil, nil, err
	}

	retEv, err := q.InsertReturnEvent(ctx, db.InsertReturnEventParams{
		ID:              uuid.NewString(),
		ReturnRequestID: retReq.ID,
		FromStatus:      "UNSPECIFIED",
		ToStatus:        retReq.Status,
		ActorID:         params.ActorID,
		ActorRole:       params.ActorRole,
		Note:            params.Reason,
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
	return agg, &ReturnAggregate{Request: retReq, Events: []db.ReturnEvent{retEv}}, nil
}

func transitionOrderToShippingInTx(
	ctx context.Context,
	q *db.Queries,
	ord db.Order,
	trackingCode, note, traceID, traceparent string,
) (db.Order, error) {
	updated, err := q.UpdateOrderShippedFromWebhook(ctx, db.UpdateOrderShippedFromWebhookParams{
		ID:           ord.ID,
		TrackingCode: trackingCode,
	})
	if err != nil {
		return db.Order{}, err
	}
	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_SHIPPING),
		ActorID:    "shipping-service",
		ActorRole:  "system",
		Note:       note,
	}); err != nil {
		return db.Order{}, err
	}
	payloadBytes, err := json.Marshal(events.OrderShippedPayload{
		OrderID:      updated.ID,
		OrderCode:    updated.OrderCode,
		BuyerID:      updated.BuyerID,
		ShopID:       updated.ShopID,
		CarrierCode:  updated.CarrierCode,
		TrackingCode: updated.TrackingCode,
	})
	if err != nil {
		return db.Order{}, err
	}
	if err := q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
		ID:          uuid.NewString(),
		EventType:   events.SubjectOrderShipped,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
	}); err != nil {
		return db.Order{}, err
	}
	return updated, nil
}

func (s *PostgresStore) ApplyShipmentUpdate(ctx context.Context, params ShipmentUpdateParams) (*ShipmentUpdateOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin shipment update tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	if params.EventID != "" {
		if params.SkipMarkProcessed {
			processed, err := q.IsEventProcessed(ctx, params.EventID)
			if err != nil {
				return nil, err
			}
			if processed {
				return &ShipmentUpdateOutcome{AlreadyProcessed: true}, nil
			}
		} else {
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
	note := strings.TrimSpace(params.Description)
	if note == "" {
		note = fmt.Sprintf("Shipment update: %s", shipStatus)
	}

	outcome := &ShipmentUpdateOutcome{}

	// P1-5: Match tracking_code when the order already has one assigned (ignore stale replaced shipments).
	curTracking := strings.TrimSpace(ord.TrackingCode)
	evTracking := strings.TrimSpace(params.TrackingCode)
	if curTracking != "" && (evTracking == "" || !strings.EqualFold(evTracking, curTracking)) {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return outcome, nil
	}

	// P1-5: Ignore FORWARD events on terminal or post-delivery orders (preserving AlreadyReturned redelivery path).
	switch curStatus {
	case orderv1.OrderStatus_ORDER_STATUS_PENDING,
		orderv1.OrderStatus_ORDER_STATUS_CANCELLED,
		orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
		orderv1.OrderStatus_ORDER_STATUS_DELIVERED,
		orderv1.OrderStatus_ORDER_STATUS_RETURN_REQUESTED:
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return outcome, nil
	case orderv1.OrderStatus_ORDER_STATUS_RETURNED:
		if !(shipStatus == "returned" && params.SkipMarkProcessed) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return outcome, nil
		}
	}

	switch shipStatus {
	case "delivering", "failed", "returning":
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			shipNote := note
			if shipStatus != "delivering" {
				shipNote = "Shipment in transit (" + shipStatus + ")"
			}
			updated, err := transitionOrderToShippingInTx(ctx, q, ord, params.TrackingCode, shipNote, params.TraceID, params.Traceparent)
			if err != nil {
				return nil, err
			}
			ord = updated
			outcome.TransitionedFrom = curStatus
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_SHIPPING
			outcome.EmittedOutbox = true
			if shipStatus != "delivering" {
				if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
					ID:         uuid.NewString(),
					OrderID:    ord.ID,
					FromStatus: ord.Status,
					ToStatus:   ord.Status,
					ActorID:    "shipping-service",
					ActorRole:  "system",
					Note:       fmt.Sprintf("[%s] %s", shipStatus, note),
				}); err != nil {
					return nil, err
				}
			}
		} else {
			if params.TrackingCode != "" {
				if err := q.UpdateOrderTrackingCode(ctx, db.UpdateOrderTrackingCodeParams{
					ID:           ord.ID,
					TrackingCode: params.TrackingCode,
				}); err != nil {
					return nil, err
				}
			}
			if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: ord.Status,
				ToStatus:   ord.Status,
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       fmt.Sprintf("[%s] %s", shipStatus, note),
			}); err != nil {
				return nil, err
			}
		}

	case "delivered":
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			updated, err := transitionOrderToShippingInTx(ctx, q, ord, params.TrackingCode, "Shipment picked up and in transit", params.TraceID, params.Traceparent)
			if err != nil {
				return nil, err
			}
			ord = updated
			curStatus = orderv1.OrderStatus_ORDER_STATUS_SHIPPING
			outcome.EmittedOutbox = true
		}
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
				OrderID:      updated.ID,
				OrderCode:    updated.OrderCode,
				BuyerID:      updated.BuyerID,
				ShopID:       updated.ShopID,
				TrackingCode: updated.TrackingCode,
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
			outcome.EmittedOutbox = true
		}

	case "returned":
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			updated, err := transitionOrderToShippingInTx(ctx, q, ord, params.TrackingCode, "Shipment picked up and in transit", params.TraceID, params.Traceparent)
			if err != nil {
				return nil, err
			}
			ord = updated
			curStatus = orderv1.OrderStatus_ORDER_STATUS_SHIPPING
			outcome.EmittedOutbox = true
		}
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
			if err := statemachine.ValidateTransition(curStatus, orderv1.OrderStatus_ORDER_STATUS_RETURNED); err != nil {
				return nil, err
			}
			nextPaymentStatus := ""
			if ord.TotalPayableVND == 0 ||
				(statemachine.StringToPaymentMethod(ord.PaymentMethod) == orderv1.OrderPaymentMethod_ORDER_PAYMENT_METHOD_COD &&
					isUnpaidPendingStatus(ord.PaymentStatus)) {
				nextPaymentStatus = "CANCELLED"
			}
			updated, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
				ID:            ord.ID,
				Status:        statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_RETURNED),
				PaymentStatus: nextPaymentStatus,
			})
			if err != nil {
				return nil, err
			}
			// P1-3: set durable pending_return_compensation inside the same TX so watchdog retries if NATS DLQs.
			if err := q.SetOrderPendingReturnCompensation(ctx, db.SetOrderPendingReturnCompensationParams{
				ID:                        ord.ID,
				PendingReturnCompensation: true,
			}); err != nil {
				return nil, err
			}
			updated.PendingReturnCompensation = true
			// P3-1(a): serialize checkout_groups payment_status sync via group row lock (orders -> checkout_groups order).
			if nextPaymentStatus != "" {
				if _, gErr := q.GetCheckoutGroupByIDForUpdate(ctx, ord.CheckoutGroupID); gErr == nil {
					if siblings, sErr := q.ListOrdersByCheckoutGroup(ctx, ord.CheckoutGroupID); sErr == nil {
						if shouldMarkGroupRefunded(siblings, updated.ID, updated.Status, updated.PaymentStatus) {
							if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
								ID: ord.CheckoutGroupID, PaymentStatus: "REFUNDED", PaymentID: "",
							}); err != nil {
								return nil, err
							}
						} else if shouldMarkGroupCancelled(siblings, updated.ID, updated.Status, updated.PaymentStatus) {
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
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       note,
			}); err != nil {
				return nil, err
			}
			wasPaid := strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") && ord.TotalPayableVND > 0
			payloadBytes, err := json.Marshal(events.OrderReturnedPayload{
				OrderID:         updated.ID,
				OrderCode:       updated.OrderCode,
				CheckoutGroupID: updated.CheckoutGroupID,
				BuyerID:         updated.BuyerID,
				ShopID:          updated.ShopID,
				PaymentMethod:   updated.PaymentMethod,
				FromStatus:      statemachine.StatusToString(curStatus),
				TotalPayableVND: updated.TotalPayableVND,
				Reason:          note,
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
				PayloadJSON: payloadBytes,
			}); err != nil {
				return nil, err
			}
			outcome.TransitionedFrom = curStatus
			outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_RETURNED
			outcome.EmittedOutbox = true
		} else if curStatus == orderv1.OrderStatus_ORDER_STATUS_RETURNED && params.SkipMarkProcessed {
			// Review-A P1-7 / Review2-O P3-8: redelivery after a transient compensation error.
			// Recover original FromStatus from timeline without duplicating timeline/outbox rows.
			outcome.AlreadyReturned = true
			tls, tlErr := q.ListOrderTimelineByOrderID(ctx, ord.ID)
			if tlErr != nil {
				return nil, tlErr
			}
			for i := len(tls) - 1; i >= 0; i-- {
				if statemachine.StringToStatus(tls[i].ToStatus) == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
					outcome.TransitionedFrom = statemachine.StringToStatus(tls[i].FromStatus)
					break
				}
			}
		}

	default:
		if params.TrackingCode != "" {
			if err := q.UpdateOrderTrackingCode(ctx, db.UpdateOrderTrackingCodeParams{
				ID:           ord.ID,
				TrackingCode: params.TrackingCode,
			}); err != nil {
				return nil, err
			}
		}
		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   ord.Status,
			ActorID:    "shipping-service",
			ActorRole:  "system",
			Note:       fmt.Sprintf("[%s] %s", shipStatus, note),
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
		note := fmt.Sprintf("Refund %s processed: %d VND (%s)", params.RefundID, params.AmountVND, params.Reason)
		peek, err := q.GetOrderByID(ctx, params.OrderID)
		if err == nil {
			// Lock all orders in the group in deterministic order so we can sync checkout_groups.payment_status safely.
			groupOrders, gErr := q.ListOrdersByCheckoutGroupForUpdate(ctx, peek.CheckoutGroupID)
			if gErr != nil {
				return false, gErr
			}
			var ord db.Order
			found := false
			for _, o := range groupOrders {
				if o.ID == params.OrderID {
					ord = o
					found = true
					break
				}
			}
			// PAY-B6: ignore refund events for a non-current payment_id.
			if found && (params.PaymentID == "" || ord.PaymentID == "" || ord.PaymentID == params.PaymentID) {
				newRefunded := ord.RefundedVND + params.AmountVND
				nextPayStatus := ""
				if ord.TotalPayableVND > 0 && newRefunded >= ord.TotalPayableVND {
					nextPayStatus = "REFUNDED"
				}
				if err := q.UpdateOrderRefunded(ctx, db.UpdateOrderRefundedParams{
					ID:            ord.ID,
					RefundedVND:   newRefunded,
					PaymentStatus: nextPayStatus,
				}); err != nil {
					return false, err
				}
				if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
					ID:         uuid.NewString(),
					OrderID:    ord.ID,
					FromStatus: ord.Status,
					ToStatus:   ord.Status,
					ActorID:    "payment-service",
					ActorRole:  "system",
					Note:       note,
				}); err != nil {
					return false, err
				}
				// Review2-O P2-5(a)/(b): sync checkout_groups.payment_status = REFUNDED when all
				// remaining paid sub-orders are REFUNDED and no active 0-VND sub-order remains.
				if nextPayStatus == "REFUNDED" && shouldMarkGroupRefunded(groupOrders, ord.ID, "", nextPayStatus) {
					if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
						ID:            ord.CheckoutGroupID,
						PaymentStatus: "REFUNDED",
						PaymentID:     "",
					}); err != nil {
						return false, err
					}
				}
			}
		} else if errors.Is(err, pgx.ErrNoRows) {
			// Group-level refund: payment.refunded carries checkout_group_id in order_id.
			groupOrders, gErr := q.ListOrdersByCheckoutGroupForUpdate(ctx, params.OrderID)
			if gErr != nil {
				return false, gErr
			}
			matched := make([]db.Order, 0, len(groupOrders))
			var groupPayable, groupRefunded int64
			for _, o := range groupOrders {
				if params.PaymentID != "" && o.PaymentID != "" && o.PaymentID != params.PaymentID {
					continue
				}
				matched = append(matched, o)
				groupPayable += o.TotalPayableVND
				groupRefunded += o.RefundedVND
			}
			isFullGroupRefund := groupPayable > 0 && groupRefunded+params.AmountVND >= groupPayable
			if len(matched) > 0 && isFullGroupRefund {
				if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
					ID:            params.OrderID,
					PaymentStatus: "REFUNDED",
					PaymentID:     "",
				}); err != nil {
					return false, err
				}
			}
			remainingToRecord := params.AmountVND
			for _, target := range matched {
				if isFullGroupRefund {
					if err := q.UpdateOrderRefunded(ctx, db.UpdateOrderRefundedParams{
						ID:            target.ID,
						RefundedVND:   target.TotalPayableVND,
						PaymentStatus: "REFUNDED",
					}); err != nil {
						return false, err
					}
				} else if remainingToRecord > 0 {
					// Review-A P2-8(a) / Review2-O P2-5(c): accumulate partial group-level refunds across
					// sub-orders and flip a sub-order to REFUNDED once its full payable amount is covered.
					avail := target.TotalPayableVND - target.RefundedVND
					if avail > 0 {
						add := min(remainingToRecord, avail)
						remainingToRecord -= add
						newRef := target.RefundedVND + add
						targetPayStatus := ""
						if target.TotalPayableVND > 0 && newRef >= target.TotalPayableVND {
							targetPayStatus = "REFUNDED"
						}
						if err := q.UpdateOrderRefunded(ctx, db.UpdateOrderRefundedParams{
							ID:            target.ID,
							RefundedVND:   newRef,
							PaymentStatus: targetPayStatus,
						}); err != nil {
							return false, err
						}
					}
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
		} else {
			return false, err
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

func (s *PostgresStore) MarkEventProcessed(ctx context.Context, eventID, eventType string) error {
	if eventID == "" {
		return nil
	}
	_, err := s.queries.TryMarkEventProcessed(ctx, db.TryMarkEventProcessedParams{
		EventID:   eventID,
		EventType: eventType,
	})
	return err
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
	rows, err := s.queries.FindExpiredPendingOrders(ctx, limit)
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
	return candidates, nil
}

func (s *PostgresStore) FindStaleInProgressCheckoutGroups(ctx context.Context, olderThan time.Duration, limit int32) ([]string, error) {
	return s.queries.FindStaleInProgressCheckoutGroups(ctx, db.FindStaleInProgressCheckoutGroupsParams{
		StaleAfterMs: olderThan.Milliseconds(),
		BatchLimit:   limit,
	})
}

func loadReturnSummaries(ctx context.Context, q *db.Queries, orderIDs []string) (map[string]db.ListReturnSummariesByOrderIDsRow, error) {
	out := make(map[string]db.ListReturnSummariesByOrderIDsRow)
	if len(orderIDs) == 0 {
		return out, nil
	}
	rows, err := q.ListReturnSummariesByOrderIDs(ctx, orderIDs)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.OrderID] = r
	}
	return out, nil
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
	agg := &OrderAggregate{
		Order:    ord,
		Items:    items,
		Timeline: tl,
	}
	if rr, rErr := q.GetReturnRequestByOrderID(ctx, orderID); rErr == nil {
		agg.ReturnRequestID = rr.ID
		agg.ReturnStatus = rr.Status
	} else if !errors.Is(rErr, pgx.ErrNoRows) {
		return nil, rErr
	}
	return agg, nil
}
