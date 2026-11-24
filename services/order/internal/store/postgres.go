package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

// PostgresStore implements Store backed by PostgreSQL (pgx/v5 + sqlc).
type PostgresStore struct {
	pool                 *pgxpool.Pool
	queries              *db.Queries
	outboxLease          atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxLease
	outboxPublishTimeout atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxPublishTimeout
	clockMu              sync.RWMutex
	outboxNowFn          func() time.Time
	logger               *slog.Logger
}

// NewPostgresStore creates a PostgresStore over pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	st := &PostgresStore{
		pool:    pool,
		queries: db.New(pool),
	}
	st.outboxLease.Store(int64(jetstream.DefaultOutboxLease))
	st.outboxPublishTimeout.Store(int64(jetstream.DefaultOutboxPublishTimeout))
	return st
}

// SetLogger configures the structured logger used for outbox poison-event alerts.
func (s *PostgresStore) SetLogger(l *slog.Logger) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	s.logger = l
}

func (s *PostgresStore) outboxLogger() *slog.Logger {
	s.clockMu.RLock()
	l := s.logger
	s.clockMu.RUnlock()
	if l != nil {
		return l
	}
	return slog.Default()
}

// SetOutboxLease overrides the outbox claim lease duration (defaults to jetstream.DefaultOutboxLease).
func (s *PostgresStore) SetOutboxLease(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxLease
	}
	s.outboxLease.Store(int64(d))
}

// SetOutboxPublishTimeout overrides the max per-event publish timeout budget (defaults to jetstream.DefaultOutboxPublishTimeout).
func (s *PostgresStore) SetOutboxPublishTimeout(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxPublishTimeout
	}
	s.outboxPublishTimeout.Store(int64(d))
}

// SetOutboxClock overrides the Go clock used for lease-budget checks (defaults to time.Now with monotonic clock).
func (s *PostgresStore) SetOutboxClock(fn func() time.Time) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	s.outboxNowFn = fn
}

func (s *PostgresStore) outboxNow() time.Time {
	s.clockMu.RLock()
	fn := s.outboxNowFn
	s.clockMu.RUnlock()
	if fn == nil {
		return time.Now()
	}
	return fn()
}

func (s *PostgresStore) outboxTimings() (lease, pubTimeout time.Duration) {
	return clampOutboxTimings(
		time.Duration(s.outboxLease.Load()),
		time.Duration(s.outboxPublishTimeout.Load()),
	)
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func checkoutGroupLookupFromRow(row db.GetCheckoutGroupForIdempotencyRow) *CheckoutGroupLookup {
	return &CheckoutGroupLookup{
		Group: db.CheckoutGroup{
			ID:                   row.ID,
			BuyerID:              row.BuyerID,
			IdempotencyKey:       row.IdempotencyKey,
			Status:               row.Status,
			PaymentMethod:        row.PaymentMethod,
			PaymentID:            row.PaymentID,
			PaymentStatus:        row.PaymentStatus,
			QRURL:                row.QRURL,
			QRCode:               row.QRCode,
			PaymentExpiresAt:     row.PaymentExpiresAt,
			TotalPayableVND:      row.TotalPayableVND,
			ResponseSnapshotJSON: row.ResponseSnapshotJSON,
			CreatedAt:            row.CreatedAt,
			UpdatedAt:            row.UpdatedAt,
			RequestHash:          row.RequestHash,
			RetryIdempotencyKey:  row.RetryIdempotencyKey,
			RetryFromPaymentID:   row.RetryFromPaymentID,
			PendingStockConfirm:  row.PendingStockConfirm,
			NeedsCleanup:         row.NeedsCleanup,
			WarningsJSON:         row.WarningsJSON,
			PayURL:               row.PayURL,
		},
		HasOrders: row.HasOrders,
	}
}

func (s *PostgresStore) GetCheckoutGroupForIdempotency(ctx context.Context, buyerID, idempotencyKey string) (*CheckoutGroupLookup, error) {
	row, err := s.queries.GetCheckoutGroupForIdempotency(ctx, db.GetCheckoutGroupForIdempotencyParams{
		BuyerID:        buyerID,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return checkoutGroupLookupFromRow(row), nil
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

// CreateCheckoutGroup serializes creation per (buyer_id, idempotency_key) across replicas using a
// transaction-scoped advisory lock, checks inside the same transaction whether a previous FAILED group
// already persisted sub-orders (D11) or still requires cleanup, and inserts the new IN_PROGRESS row.
func (s *PostgresStore) CreateCheckoutGroup(ctx context.Context, arg db.CreateCheckoutGroupParams) (*db.CheckoutGroup, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin create checkout group tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('order_checkout_idem:' || $1 || ':' || $2, 0))", arg.BuyerID, arg.IdempotencyKey); err != nil {
		return nil, fmt.Errorf("lock checkout idempotency key: %w", err)
	}

	q := s.queries.WithTx(tx)
	existing, err := q.GetCheckoutGroupForIdempotency(ctx, db.GetCheckoutGroupForIdempotencyParams{
		BuyerID:        arg.BuyerID,
		IdempotencyKey: arg.IdempotencyKey,
	})
	if err == nil {
		if existing.Status != "FAILED" {
			return nil, ErrDuplicateIdempotencyKey
		}
		if existing.HasOrders {
			return nil, &FailedCheckoutWithOrdersError{
				GroupID: existing.ID,
				Reason:  existing.PaymentStatus,
			}
		}
		if existing.NeedsCleanup {
			return nil, ErrDuplicateIdempotencyKey
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("check idempotency checkout group: %w", err)
	}

	row, err := q.CreateCheckoutGroup(ctx, arg)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateIdempotencyKey
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit create checkout group tx: %w", err)
	}
	return &row, nil
}

func (s *PostgresStore) FailCheckoutGroup(ctx context.Context, id, paymentStatus string) error {
	return s.queries.FailCheckoutGroup(ctx, db.FailCheckoutGroupParams{
		ID:            id,
		PaymentStatus: paymentStatus,
	})
}

// ClaimStaleCheckoutGroupForCleanup implements P1 M3 claim-first stale cleanup using explicit
// two-statement READ COMMITTED serialization:
//  1. SELECT checkout_groups FOR UPDATE (blocks behind any in-flight PersistCheckoutBatch).
//  2. Second statement (fresh snapshot AFTER lock) checks whether any orders exist for the group.
//  3. Only if status == IN_PROGRESS and zero orders exist, flips status = 'FAILED' + needs_cleanup = TRUE and commits.
//
// Also returns claimed=true if the group is already FAILED + needs_cleanup=TRUE with zero orders so
// retries after transient ReleasePromotion/ReleaseStock errors can finish and clear needs_cleanup.
func (s *PostgresStore) ClaimStaleCheckoutGroupForCleanup(ctx context.Context, id, reason string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin claim stale checkout tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	grp, err := q.GetCheckoutGroupByIDForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("lock checkout group for cleanup claim: %w", err)
	}

	// Statement 2: fresh snapshot after acquiring row lock so any concurrently committed orders are visible!
	hasOrders, err := q.CheckoutGroupHasOrders(ctx, id)
	if err != nil {
		return false, fmt.Errorf("check orders for cleanup claim: %w", err)
	}
	if hasOrders {
		return false, nil
	}

	if grp.Status == "FAILED" && grp.NeedsCleanup {
		return true, nil
	}
	if grp.Status != "IN_PROGRESS" {
		return false, nil
	}

	if err := q.ClaimCheckoutGroupForCleanup(ctx, db.ClaimCheckoutGroupForCleanupParams{
		ID:            id,
		PaymentStatus: reason,
	}); err != nil {
		return false, fmt.Errorf("claim checkout group for cleanup: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit claim stale checkout tx: %w", err)
	}
	return true, nil
}

func (s *PostgresStore) ClearCheckoutGroupNeedsCleanup(ctx context.Context, id string) error {
	return s.queries.ClearCheckoutGroupNeedsCleanup(ctx, id)
}

func (s *PostgresStore) PersistCheckoutBatch(ctx context.Context, batch CheckoutBatch) ([]OrderAggregate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin persist checkout batch tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	// P0 N1: Lock the checkout_group row FOR UPDATE and require status = 'IN_PROGRESS' so stale/failed
	// groups can never receive late sub-order inserts and post-error inspection serializes cleanly.
	grp, err := q.GetCheckoutGroupByIDForUpdate(ctx, batch.CheckoutGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("lock checkout group for persist: %w", err)
	}
	if grp.Status != "IN_PROGRESS" {
		return nil, fmt.Errorf("%w: checkout group %s has status %s", ErrCheckoutGroupNotInProgress, grp.ID, grp.Status)
	}

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

	if len(batch.WarningsJSON) > 0 {
		if err := q.SetCheckoutGroupWarnings(ctx, db.SetCheckoutGroupWarningsParams{
			ID:           batch.CheckoutGroupID,
			WarningsJSON: batch.WarningsJSON,
		}); err != nil {
			return nil, fmt.Errorf("persist checkout group warnings: %w", err)
		}
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

// InspectCheckoutGroupOrdersLocked acquires FOR UPDATE on the checkout_group row (waiting for any
// in-flight PersistCheckoutBatch transaction to finish) and reads all committed sub-orders in the same TX (P0 N1).
func (s *PostgresStore) InspectCheckoutGroupOrdersLocked(ctx context.Context, checkoutGroupID string) (*db.CheckoutGroup, []OrderAggregate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin inspect checkout group tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	grp, err := q.GetCheckoutGroupByIDForUpdate(ctx, checkoutGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("lock checkout group for inspect: %w", err)
	}

	rows, err := q.ListOrdersByCheckoutGroup(ctx, checkoutGroupID)
	if err != nil {
		return nil, nil, fmt.Errorf("list orders for inspect: %w", err)
	}
	aggs := make([]OrderAggregate, 0, len(rows))
	for _, ord := range rows {
		agg, lErr := loadOrderAggregate(ctx, q, ord.ID)
		if lErr != nil {
			return nil, nil, fmt.Errorf("load order aggregate %s for inspect: %w", ord.ID, lErr)
		}
		aggs = append(aggs, *agg)
	}
	return &grp, aggs, nil
}

func (s *PostgresStore) CompleteCheckoutWithPayment(ctx context.Context, arg UpdateCheckoutPaymentParams) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin complete checkout tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)

	// P0-1: Lock orders FIRST in canonical (shop_id, created_at, id) order matching TransitionOrdersPaymentResult
	// to prevent AB-BA deadlocks, then lock checkout_groups.
	lockedOrders, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, arg.CheckoutGroupID)
	if err != nil {
		return fmt.Errorf("lock orders for checkout completion: %w", err)
	}
	grp, err := q.GetCheckoutGroupByIDForUpdate(ctx, arg.CheckoutGroupID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock checkout group for completion: %w", err)
	}

	// P1 N4: If the group already left IN_PROGRESS (e.g. COMPLETED by another pod + RetryPayment P2, or FAILED),
	// write NOTHING to orders or checkout_groups; still hand current locked state to BuildSnapshot so caller can
	// render an accurate response without mutating DB.
	// P0-1 / P1-2: When the group is already FAILED (NOT COMPLETED) and arg.PaymentID != "", re-emit a fresh
	// order.cancelled outbox event for any CANCELLED sub-order with empty payment_id and non-REFUNDED status
	// so payment handles cancellation/refund now that the payment exists.
	if grp.Status != "IN_PROGRESS" {
		emitted := false
		if grp.Status == "FAILED" && strings.TrimSpace(arg.PaymentID) != "" {
			for _, ord := range lockedOrders {
				if !shouldReemitOrderCancelledForPayment(ord, arg.PaymentID) {
					continue
				}
				ob, obErr := buildOrderCancelledOutbox(ord, ord.CancelReason, "", "")
				if obErr != nil {
					return fmt.Errorf("marshal re-emitted order.cancelled for %s: %w", ord.ID, obErr)
				}
				if err := q.InsertOutboxEvent(ctx, ob); err != nil {
					return fmt.Errorf("insert re-emitted order.cancelled for %s: %w", ord.ID, err)
				}
				emitted = true
			}
		}
		if arg.BuildSnapshot != nil {
			curAggs := make([]OrderAggregate, 0, len(lockedOrders))
			for _, ord := range lockedOrders {
				agg, lErr := loadOrderAggregate(ctx, q, ord.ID)
				if lErr != nil {
					return fmt.Errorf("load current order aggregate %s: %w", ord.ID, lErr)
				}
				curAggs = append(curAggs, *agg)
			}
			_, _ = arg.BuildSnapshot(grp, curAggs)
		}
		if emitted {
			return tx.Commit(ctx)
		}
		return nil
	}

	if err := q.AttachCheckoutPaymentToOrders(ctx, db.AttachCheckoutPaymentToOrdersParams{
		CheckoutGroupID:  arg.CheckoutGroupID,
		PaymentID:        arg.PaymentID,
		PaymentStatus:    arg.PaymentStatus,
		PaymentExpiresAt: arg.PaymentExpiresAt,
	}); err != nil {
		return fmt.Errorf("attach order payment info: %w", err)
	}

	// Re-read post-update orders (already locked in this TX) to compute non-downgrading group status,
	// live total_payable_vnd, re-emitted order.cancelled outbox events, and fresh snapshot.
	postOrders, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, arg.CheckoutGroupID)
	if err != nil {
		return fmt.Errorf("reload locked orders after attach: %w", err)
	}
	for _, ord := range postOrders {
		if !shouldReemitOrderCancelledForPayment(ord, arg.PaymentID) {
			continue
		}
		ob, obErr := buildOrderCancelledOutbox(ord, ord.CancelReason, "", "")
		if obErr != nil {
			return fmt.Errorf("marshal re-emitted order.cancelled for %s: %w", ord.ID, obErr)
		}
		if err := q.InsertOutboxEvent(ctx, ob); err != nil {
			return fmt.Errorf("insert re-emitted order.cancelled for %s: %w", ord.ID, err)
		}
	}

	allCancelled := allOrdersCancelledDB(postOrders)
	targetStatus := "COMPLETED"
	if allCancelled {
		targetStatus = "FAILED"
	}
	resolvedPayStatus := resolveNonDowngradingGroupPaymentStatus(grp.PaymentStatus, arg.PaymentStatus, postOrders)
	liveTotalPayableVND := sumLiveOrderPayableVND(postOrders)

	effectiveGrp := grp
	effectiveGrp.Status = targetStatus
	if allCancelled {
		effectiveGrp.PendingStockConfirm = false
	}
	if effectiveGrp.PaymentID == "" {
		effectiveGrp.PaymentID = arg.PaymentID
	}
	if isPendingOrEmptyPaymentStatus(effectiveGrp.PaymentStatus) {
		effectiveGrp.PaymentStatus = resolvedPayStatus
		if effectiveGrp.QRURL == "" {
			effectiveGrp.QRURL = arg.QRURL
		}
		if effectiveGrp.QRCode == "" {
			effectiveGrp.QRCode = arg.QRCode
		}
		if effectiveGrp.PayURL == "" {
			effectiveGrp.PayURL = arg.PayURL
		}
		if arg.PaymentExpiresAt != nil {
			effectiveGrp.PaymentExpiresAt = arg.PaymentExpiresAt
		}
	}
	effectiveGrp.TotalPayableVND = liveTotalPayableVND

	snapshotBytes := arg.ResponseSnapshotJSON
	if arg.BuildSnapshot != nil {
		postAggs := make([]OrderAggregate, 0, len(postOrders))
		for _, ord := range postOrders {
			agg, lErr := loadOrderAggregate(ctx, q, ord.ID)
			if lErr != nil {
				return fmt.Errorf("load post-attach order aggregate %s: %w", ord.ID, lErr)
			}
			postAggs = append(postAggs, *agg)
		}
		built, bErr := arg.BuildSnapshot(effectiveGrp, postAggs)
		if bErr != nil {
			return fmt.Errorf("build checkout response snapshot: %w", bErr)
		}
		snapshotBytes = built
	}
	if allCancelled {
		// D11 (P1-1): When every sub-order is already CANCELLED before completion, close FAILED
		// without storing a COMPLETED response snapshot and with pending_stock_confirm cleared.
		snapshotBytes = nil
	}

	if _, err := q.CompleteCheckoutGroup(ctx, db.CompleteCheckoutGroupParams{
		ID:                   arg.CheckoutGroupID,
		PaymentID:            arg.PaymentID,
		PaymentStatus:        resolvedPayStatus,
		QRURL:                arg.QRURL,
		QRCode:               arg.QRCode,
		PayURL:               arg.PayURL,
		PaymentExpiresAt:     arg.PaymentExpiresAt,
		TotalPayableVND:      liveTotalPayableVND,
		TargetStatus:         targetStatus,
		ResponseSnapshotJSON: snapshotBytes,
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
	// Canonical lock order: orders first (ordered shop_id ASC, created_at ASC, id ASC), then checkout_groups.
	orders, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, params.CheckoutGroupID)
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return ErrNotFound
	}
	grp, grpErr := q.GetCheckoutGroupByIDForUpdate(ctx, params.CheckoutGroupID)
	if grpErr != nil && !errors.Is(grpErr, pgx.ErrNoRows) {
		return grpErr
	}

	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	targetMethod := statemachine.NormalizePaymentMethod(params.TargetMethod)
	if params.SwitchToCOD {
		targetMethod = statemachine.PaymentMethodCOD
	} else if targetMethod == "" {
		targetMethod = statemachine.NormalizePaymentMethod(orders[0].PaymentMethod)
	}
	if !statemachine.IsValidPaymentMethod(targetMethod) {
		return fmt.Errorf("%w: invalid target payment method %q", ErrRetryPrecondition, targetMethod)
	}
	switchToCOD := params.SwitchToCOD || statemachine.IsCODMethod(targetMethod)

	alreadyApplied := params.NewPaymentID != ""
	for _, ord := range orders {
		expectedStatus := orderv1.OrderStatus_ORDER_STATUS_PENDING
		if switchToCOD {
			expectedStatus = orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
		}
		if ord.PaymentID != params.NewPaymentID ||
			statemachine.NormalizePaymentMethod(ord.PaymentMethod) != targetMethod ||
			statemachine.StringToStatus(ord.Status) != expectedStatus {
			alreadyApplied = false
			break
		}
	}
	if alreadyApplied {
		// Review-A P0-3: idempotent success if another replica already committed this exact retry/switch.
		return tx.Commit(ctx)
	}

	if grpErr == nil {
		if grp.Status == "FAILED" {
			return fmt.Errorf("%w: checkout group %s is FAILED", ErrRetryPrecondition, grp.ID)
		}
		if params.ExpectedPaymentID != "" && grp.PaymentID != "" && grp.PaymentID != params.ExpectedPaymentID {
			return fmt.Errorf("%w: checkout group payment_id mismatch", ErrRetryPrecondition)
		}
	}

	for _, ord := range orders {
		if statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_PENDING {
			return fmt.Errorf("%w: order %s is in status %s", ErrRetryPrecondition, ord.ID, ord.Status)
		}
		if !statemachine.IsOnlineMethod(ord.PaymentMethod) {
			return fmt.Errorf("%w: order %s payment method is %s", ErrRetryPrecondition, ord.ID, ord.PaymentMethod)
		}
		if params.ExpectedPaymentID != "" && ord.PaymentID != params.ExpectedPaymentID {
			return fmt.Errorf("%w: order %s payment_id mismatch", ErrRetryPrecondition, ord.ID)
		}
		exp := ord.PaymentExpiresAt
		if exp == nil && grpErr == nil {
			exp = grp.PaymentExpiresAt
		}
		if exp != nil && !exp.After(now) {
			return fmt.Errorf("%w: order %s payment expired", ErrRetryPrecondition, ord.ID)
		}
	}

	feeByOrder := make(map[string]RetryOrderFeeSpec, len(params.OrderFees))
	for _, f := range params.OrderFees {
		feeByOrder[f.OrderID] = f
	}

	for _, ord := range orders {
		prevMethod := statemachine.NormalizePaymentMethod(ord.PaymentMethod)
		feeVND := ord.PaymentFeeVND
		feeBPS := ord.PaymentFeeBPS
		if spec, ok := feeByOrder[ord.ID]; ok {
			feeVND = spec.PaymentFeeVND
			feeBPS = spec.PaymentFeeBPS
		}
		orderExpiresAt := params.PaymentExpiresAt
		if switchToCOD {
			orderExpiresAt = nil
		}

		updated, uErr := q.UpdateOrderPaymentOnRetry(ctx, db.UpdateOrderPaymentOnRetryParams{
			ID:               ord.ID,
			PaymentMethod:    targetMethod,
			PaymentID:        params.NewPaymentID,
			PaymentStatus:    params.PaymentStatus,
			PaymentFeeVND:    feeVND,
			PaymentFeeBPS:    feeBPS,
			PaymentExpiresAt: orderExpiresAt,
			ConfirmOrder:     switchToCOD,
		})
		if uErr != nil {
			if errors.Is(uErr, pgx.ErrNoRows) {
				return fmt.Errorf("%w: order %s left PENDING state concurrently", ErrRetryPrecondition, ord.ID)
			}
			return fmt.Errorf("update order %s payment on retry: %w", ord.ID, uErr)
		}

		actorID := strings.TrimSpace(params.ActorID)
		if actorID == "" {
			actorID = ord.BuyerID
		}
		actorRole := strings.TrimSpace(params.ActorRole)
		if actorRole == "" {
			actorRole = "buyer"
		}

		if switchToCOD {
			if _, tlErr := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: ord.Status,
				ToStatus:   updated.Status,
				ActorID:    actorID,
				ActorRole:  actorRole,
				Note:       fmt.Sprintf("Switched payment method from %s to COD; order confirmed", prevMethod),
			}); tlErr != nil {
				return fmt.Errorf("insert COD switch timeline event: %w", tlErr)
			}

			payloadBytes, mErr := json.Marshal(events.OrderConfirmedPayload{
				OrderID:         updated.ID,
				OrderCode:       updated.OrderCode,
				BuyerID:         updated.BuyerID,
				ShopID:          updated.ShopID,
				PaymentMethod:   updated.PaymentMethod,
				TotalPayableVND: updated.TotalPayableVND,
			})
			if mErr != nil {
				return fmt.Errorf("marshal order.confirmed on COD switch: %w", mErr)
			}
			if obErr := q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
				ID:          uuid.NewString(),
				EventType:   events.SubjectOrderConfirmed,
				TraceID:     params.TraceID,
				Traceparent: params.Traceparent,
				PayloadJSON: payloadBytes,
			}); obErr != nil {
				return fmt.Errorf("insert order.confirmed outbox on COD switch: %w", obErr)
			}
		} else if prevMethod != targetMethod {
			if _, tlErr := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: ord.Status,
				ToStatus:   updated.Status,
				ActorID:    actorID,
				ActorRole:  actorRole,
				Note:       fmt.Sprintf("Switched payment method from %s to %s", prevMethod, targetMethod),
			}); tlErr != nil {
				return fmt.Errorf("insert method switch timeline event: %w", tlErr)
			}
		}
	}

	groupQRURL := params.QRURL
	groupQRCode := params.QRCode
	groupPayURL := params.PayURL
	groupExpiresAt := params.PaymentExpiresAt
	if switchToCOD {
		groupQRURL = ""
		groupQRCode = ""
		groupPayURL = ""
		groupExpiresAt = nil
	}

	if err := q.UpdateCheckoutGroupPaymentOnRetry(ctx, db.UpdateCheckoutGroupPaymentOnRetryParams{
		ID:                     params.CheckoutGroupID,
		PaymentMethod:          targetMethod,
		PaymentID:              params.NewPaymentID,
		PaymentStatus:          params.PaymentStatus,
		QRURL:                  groupQRURL,
		QRCode:                 groupQRCode,
		PayURL:                 groupPayURL,
		PaymentExpiresAt:       groupExpiresAt,
		RetryIdempotencyKey:    params.RetryIdempotencyKey,
		RetryFromPaymentID:     params.ExpectedPaymentID,
		SetPendingStockConfirm: switchToCOD,
	}); err != nil {
		return fmt.Errorf("update checkout group payment on retry: %w", err)
	}

	return tx.Commit(ctx)
}

func (s *PostgresStore) CancelCheckoutGroupOrders(ctx context.Context, params CancelGroupOrdersParams) (*CancelGroupOrdersResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel group tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	// Keep canonical lock order: orders first, then checkout_groups.
	orders, err := q.ListOrdersByCheckoutGroupForUpdate(ctx, params.CheckoutGroupID)
	if err != nil {
		return nil, err
	}
	grp, grpErr := q.GetCheckoutGroupByIDForUpdate(ctx, params.CheckoutGroupID)
	if grpErr != nil && !errors.Is(grpErr, pgx.ErrNoRows) {
		return nil, grpErr
	}
	// P1 M4: If the checkout group exists and has already left IN_PROGRESS (e.g. COMPLETED by checkout
	// or FAILED), refuse to cancel any order and signal HasLiveOrders = true so callers release nothing.
	if grpErr == nil && grp.Status != "IN_PROGRESS" {
		return &CancelGroupOrdersResult{
			Cancelled:     nil,
			HasLiveOrders: true,
		}, nil
	}

	cancelled := make([]db.Order, 0, len(orders))
	hasLiveOrders := false
	for _, ord := range orders {
		curStatus := statemachine.StringToStatus(ord.Status)
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			continue
		}
		if !canCancelGroupOrder(ord, params.OnlyPending) {
			hasLiveOrders = true
			continue
		}

		// P0 N2 + P3(a): Never overwrite paid SUCCEEDED/REFUNDED payment_status with FAILED;
		// for 0-VND orders align with TransitionOrderStatus by setting payment_status = "CANCELLED".
		nextPayStatus := params.PaymentStatus
		if ord.TotalPayableVND == 0 {
			nextPayStatus = "CANCELLED"
		} else if isOrderPaymentTerminalProtected(ord) {
			nextPayStatus = ""
		}

		updated, err := q.UpdateOrderStatus(ctx, db.UpdateOrderStatusParams{
			ID:            ord.ID,
			Status:        statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_CANCELLED),
			CancelReason:  params.Reason,
			PaymentStatus: nextPayStatus,
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

		ob, err := buildOrderCancelledOutbox(updated, params.Reason, params.TraceID, params.Traceparent)
		if err != nil {
			return nil, err
		}

		if err := q.InsertOutboxEvent(ctx, ob); err != nil {
			return nil, err
		}

		cancelled = append(cancelled, updated)
	}

	// P0 N2: Only mark checkout_groups FAILED and clear pending_stock_confirm when NO live orders remain.
	if !hasLiveOrders {
		closeStatus := params.PaymentStatus
		merged := mergeUpdatedOrders(orders, cancelled)
		if derived := ResolveAllCancelledPaymentStatus(merged); derived == "EXPIRED" || derived == "REFUNDED" {
			closeStatus = derived
		}
		if err := q.FailCheckoutGroup(ctx, db.FailCheckoutGroupParams{
			ID:            params.CheckoutGroupID,
			PaymentStatus: closeStatus,
		}); err != nil {
			return nil, err
		}
		if err := q.SetCheckoutGroupPendingStockConfirm(ctx, db.SetCheckoutGroupPendingStockConfirmParams{
			ID:                  params.CheckoutGroupID,
			PendingStockConfirm: false,
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &CancelGroupOrdersResult{
		Cancelled:     cancelled,
		HasLiveOrders: hasLiveOrders,
	}, nil
}

func (s *PostgresStore) MarkOrderPendingCancelCompensation(ctx context.Context, orderID string) error {
	return s.queries.SetOrderPendingCancelCompensation(ctx, db.SetOrderPendingCancelCompensationParams{
		ID:                        orderID,
		PendingCancelCompensation: true,
	})
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
		// PAY-B6 + F5 / Item B (#2 + #6.2): never mutate orders for a non-current payment_id,
		// AND if payment.succeeded arrives for ANY non-current payment of this checkout group
		// (whether >=2-hop superseded P1->P2->P3 or an orphaned unrecorded retry payment),
		// return SupersededPaidOrders so HandlePaymentResult triggers automatic refund exactly once.
		var supersededPaid []db.Order
		if params.ResultStatus == orderv1.PaymentResultStatus_PAYMENT_RESULT_STATUS_SUCCEEDED {
			supersededPaid = append([]db.Order(nil), orders...)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &PaymentResultTransitionOutcome{
			CheckoutGroupID:      groupID,
			SupersededPaidOrders: supersededPaid,
		}, nil
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
				if statemachine.IsOnlineMethod(ord.PaymentMethod) &&
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
			ordPaid := statemachine.IsOnlineMethod(targetOrd.PaymentMethod) &&
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
			if g, gErr := q.GetCheckoutGroupByIDForUpdate(ctx, ord.CheckoutGroupID); gErr == nil {
				closeStatus := ResolveAllCancelledPaymentStatus(mergedSiblings)
				if strings.EqualFold(g.PaymentStatus, "EXPIRED") {
					closeStatus = "EXPIRED"
				}
				if err := q.UpdateCheckoutGroupPaymentStatus(ctx, db.UpdateCheckoutGroupPaymentStatusParams{
					ID:            ord.CheckoutGroupID,
					PaymentStatus: closeStatus,
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

func (s *PostgresStore) ListOrdersNeedingManualReview(ctx context.Context, limit, offset int32) ([]OrderAggregate, int64, error) {
	total, err := s.queries.CountOrdersNeedingManualReview(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.queries.ListOrdersNeedingManualReview(ctx, db.ListOrdersNeedingManualReviewParams{
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

func (s *PostgresStore) ResolveOrderManualReview(ctx context.Context, orderID, actorID, actorRole, note string, _ time.Time) (*OrderAggregate, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin resolve manual review tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	ord, err := q.GetOrderByIDForUpdate(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !ord.OrphanShipmentNeedsManualReview {
		return nil, ErrOrderNotNeedingReview
	}
	if _, err := q.ClearOrderOrphanShipmentManualReview(ctx, ord.ID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOrderNotNeedingReview
		}
		return nil, err
	}
	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   ord.Status,
		ActorID:    actorID,
		ActorRole:  actorRole,
		Note:       note,
	}); err != nil {
		return nil, err
	}
	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return agg, nil
}

func (s *PostgresStore) QueryOrderReport(ctx context.Context, params OrderReportParams) (*OrderReportData, error) {
	data := &OrderReportData{
		StatusCounts: make(map[string]int64),
	}
	shopID := strings.TrimSpace(params.ShopID)
	if shopID != "" {
		stRows, err := s.queries.ReportSellerStatusCounts(ctx, db.ReportSellerStatusCountsParams{
			ShopID:  shopID,
			StartTs: params.StartTS,
			EndTs:   params.EndTS,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range stRows {
			data.StatusCounts[r.Status] = r.Count
		}

		crRows, err := s.queries.ReportSellerCreatedDaily(ctx, db.ReportSellerCreatedDailyParams{
			ShopID:  shopID,
			StartTs: params.StartTS,
			EndTs:   params.EndTS,
		})
		if err != nil {
			return nil, err
		}
		data.CreatedDaily = make([]CreatedDailyStat, 0, len(crRows))
		for _, r := range crRows {
			data.CreatedDaily = append(data.CreatedDaily, CreatedDailyStat{
				Date:           r.ReportDate,
				OrderCount:     r.OrderCount,
				CancelledCount: r.CancelledCount,
				ReturnedCount:  r.ReturnedCount,
				GMVVND:         r.GMVVND,
			})
		}

		cpRows, err := s.queries.ReportSellerCompletedDaily(ctx, db.ReportSellerCompletedDailyParams{
			ShopID:  shopID,
			StartTs: params.StartTS,
			EndTs:   params.EndTS,
		})
		if err != nil {
			return nil, err
		}
		data.CompletedDaily = make([]CompletedDailyStat, 0, len(cpRows))
		for _, r := range cpRows {
			data.CompletedDaily = append(data.CompletedDaily, CompletedDailyStat{
				Date:           r.ReportDate,
				CompletedCount: r.CompletedCount,
				NetRevenueVND:  r.NetRevenueVND,
				CommissionVND:  r.CommissionVND,
				PaymentFeeVND:  r.PaymentFeeVND,
			})
		}

		tpRows, err := s.queries.ReportSellerTopProducts(ctx, db.ReportSellerTopProductsParams{
			ShopID:   shopID,
			StartTs:  params.StartTS,
			EndTs:    params.EndTS,
			TopLimit: params.TopLimit,
		})
		if err != nil {
			return nil, err
		}
		data.TopProducts = make([]TopProductRow, 0, len(tpRows))
		for _, r := range tpRows {
			data.TopProducts = append(data.TopProducts, TopProductRow{
				ProductID:   r.ProductID,
				ProductName: r.ProductName,
				UnitsSold:   r.UnitsSold,
				RevenueVND:  r.RevenueVND,
				OrderCount:  r.OrderCount,
			})
		}
		return data, nil
	}

	stRows, err := s.queries.ReportPlatformStatusCounts(ctx, db.ReportPlatformStatusCountsParams{
		StartTs: params.StartTS,
		EndTs:   params.EndTS,
	})
	if err != nil {
		return nil, err
	}
	for _, r := range stRows {
		data.StatusCounts[r.Status] = r.Count
	}

	crRows, err := s.queries.ReportPlatformCreatedDaily(ctx, db.ReportPlatformCreatedDailyParams{
		StartTs: params.StartTS,
		EndTs:   params.EndTS,
	})
	if err != nil {
		return nil, err
	}
	data.CreatedDaily = make([]CreatedDailyStat, 0, len(crRows))
	for _, r := range crRows {
		data.CreatedDaily = append(data.CreatedDaily, CreatedDailyStat{
			Date:           r.ReportDate,
			OrderCount:     r.OrderCount,
			CancelledCount: r.CancelledCount,
			ReturnedCount:  r.ReturnedCount,
			GMVVND:         r.GMVVND,
		})
	}

	cpRows, err := s.queries.ReportPlatformCompletedDaily(ctx, db.ReportPlatformCompletedDailyParams{
		StartTs: params.StartTS,
		EndTs:   params.EndTS,
	})
	if err != nil {
		return nil, err
	}
	data.CompletedDaily = make([]CompletedDailyStat, 0, len(cpRows))
	for _, r := range cpRows {
		data.CompletedDaily = append(data.CompletedDaily, CompletedDailyStat{
			Date:               r.ReportDate,
			CompletedCount:     r.CompletedCount,
			NetRevenueVND:      r.NetRevenueVND,
			CommissionVND:      r.CommissionVND,
			PaymentFeeVND:      r.PaymentFeeVND,
			PlatformSubsidyVND: r.PlatformSubsidyVND,
		})
	}

	tpRows, err := s.queries.ReportPlatformTopProducts(ctx, db.ReportPlatformTopProductsParams{
		StartTs:  params.StartTS,
		EndTs:    params.EndTS,
		TopLimit: params.TopLimit,
	})
	if err != nil {
		return nil, err
	}
	data.TopProducts = make([]TopProductRow, 0, len(tpRows))
	for _, r := range tpRows {
		data.TopProducts = append(data.TopProducts, TopProductRow{
			ProductID:   r.ProductID,
			ProductName: r.ProductName,
			UnitsSold:   r.UnitsSold,
			RevenueVND:  r.RevenueVND,
			OrderCount:  r.OrderCount,
		})
	}
	return data, nil
}

func (s *PostgresStore) GetBuyerTerminalCODStats(ctx context.Context, buyerID string) (BuyerCODStats, error) {
	row, err := s.queries.GetBuyerTerminalCODStats(ctx, buyerID)
	if err != nil {
		return BuyerCODStats{}, err
	}
	return BuyerCODStats{
		TotalTerminal: row.TotalTerminal,
		RejectedCount: row.RejectedCount,
	}, nil
}

func (s *PostgresStore) MarkOrderShipmentQueued(ctx context.Context, params QueueShipmentParams) (*QueueShipmentOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin mark shipment queued tx: %w", err)
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
		// Order cancelled while CreateShipment queued a job: set pending_shipment_cancel so any
		// shipment created in-flight is cancelled by immediate compensation or watchdog.
		if err := q.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
			ID:                    ord.ID,
			PendingShipmentCancel: true,
			MarkedAt:              now,
			Token:                 "",
		}); err != nil {
			return nil, err
		}
		if err := q.ClearOrderShipmentJobStatus(ctx, ord.ID); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrOrphanShipmentNeedsCancel
	}

	// Race guard: async worker already created the shipment and shipment.updated(created) attached tracking_code
	// before CreateShipment returned queued=true. Never overwrite back to QUEUED.
	if strings.TrimSpace(ord.TrackingCode) != "" {
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
		if ord.ShipmentJobStatus != "" {
			if err := q.ClearOrderShipmentJobStatus(ctx, ord.ID); err != nil {
				return nil, err
			}
		}
		agg, err := loadOrderAggregate(ctx, q, ord.ID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &QueueShipmentOutcome{Order: agg, AlreadyAttached: true}, nil
	}

	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return nil, fmt.Errorf("%w: cannot queue shipment from status %s", statemachine.ErrInvalidTransition, statemachine.StatusToString(curStatus))
	}
	if params.ExpectedCreatingToken != "" && ord.PendingShipmentCancelToken != params.ExpectedCreatingToken {
		return nil, ErrStaleShipmentAttempt
	}

	wasQueued := strings.EqualFold(strings.TrimSpace(ord.ShipmentJobStatus), "QUEUED")
	if _, err := q.SetOrderShipmentQueued(ctx, ord.ID); err != nil {
		return nil, err
	}
	if !wasQueued {
		note := strings.TrimSpace(params.Note)
		if note == "" {
			note = "Shipment creation queued for async retry"
		}
		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   ord.Status,
			ActorID:    params.ActorID,
			ActorRole:  params.ActorRole,
			Note:       note,
		}); err != nil {
			return nil, err
		}
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &QueueShipmentOutcome{Order: agg, AlreadyAttached: false}, nil
}

func (s *PostgresStore) MarkOrderShipmentJobFailedIfQueued(ctx context.Context, orderID string) (bool, error) {
	rows, err := s.queries.MarkOrderShipmentJobFailedIfQueued(ctx, orderID)
	return rows > 0, err
}

func (s *PostgresStore) ApplyShipmentCreateFailed(ctx context.Context, params ShipmentCreateFailedParams) (*ShipmentCreateFailedOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin shipment create failed tx: %w", err)
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
			return &ShipmentCreateFailedOutcome{AlreadyProcessed: true}, nil
		}
	}

	ord, err := q.GetOrderByIDForUpdate(ctx, params.OrderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ShipmentCreateFailedOutcome{NotFound: true}, nil
		}
		return nil, err
	}

	outcome := &ShipmentCreateFailedOutcome{}
	curStatus := statemachine.StringToStatus(ord.Status)
	// E18: Only mark shipment_job_status=FAILED when no tracking_code exists yet, the order is still
	// CONFIRMED or PREPARING, and no newer synchronous SellerShipOrder attempt is actively in flight.
	if strings.TrimSpace(ord.TrackingCode) == "" &&
		!ord.PendingShipmentCancel &&
		(curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING) {
		if _, err := q.SetOrderShipmentJobFailed(ctx, ord.ID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		} else if err == nil {
			note := fmt.Sprintf("Async shipment creation failed after %d attempts", params.Attempts)
			if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
				ID:         uuid.NewString(),
				OrderID:    ord.ID,
				FromStatus: ord.Status,
				ToStatus:   ord.Status,
				ActorID:    "shipping-service",
				ActorRole:  "system",
				Note:       note,
			}); err != nil {
				return nil, err
			}
			outcome.Updated = true
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
		if ord.ShipmentJobStatus != "" {
			if err := q.ClearOrderShipmentJobStatus(ctx, ord.ID); err != nil {
				return nil, err
			}
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
		if ord.ShipmentJobStatus != "" {
			if err := q.ClearOrderShipmentJobStatus(ctx, ord.ID); err != nil {
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
	// F11 / Item E: RETURNED -> RETURN_REQUESTED is reserved strictly for rejected-refund rollback;
	// new buyer return requests are only allowed from DELIVERED.
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_DELIVERED {
		return nil, nil, fmt.Errorf("%w: return request requires order status DELIVERED, got %s",
			statemachine.ErrInvalidTransition, statemachine.StatusToString(curStatus))
	}
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
	carrierCode, serviceCode, trackingCode, note, traceID, traceparent string,
) (db.Order, error) {
	updated, err := q.UpdateOrderShippedFromWebhook(ctx, db.UpdateOrderShippedFromWebhookParams{
		ID:           ord.ID,
		CarrierCode:  strings.TrimSpace(carrierCode),
		ServiceCode:  strings.TrimSpace(serviceCode),
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

	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	curStatus := statemachine.StringToStatus(ord.Status)
	shipStatus := strings.ToLower(strings.TrimSpace(params.ShipmentStatus))
	note := strings.TrimSpace(params.Description)
	if note == "" {
		note = fmt.Sprintf("Shipment update: %s", shipStatus)
	}

	outcome := &ShipmentUpdateOutcome{}

	curTracking := strings.TrimSpace(ord.TrackingCode)
	evTracking := strings.TrimSpace(params.TrackingCode)
	evCarrier := strings.TrimSpace(params.CarrierCode)
	evService := strings.TrimSpace(params.ServiceCode)
	evLabel := strings.TrimSpace(params.LabelURL)

	// E18: Cancelled order with no recorded tracking_code receiving any active forward shipment event
	// (created/picking/picked/in_transit/delivering/delivered/etc., anything except "cancelled") — or a
	// newly created shipment with a different tracking_code — must trigger compensating CancelShipment
	// (and flag orphan_shipment_needs_manual_review if the carrier already picked up).
	// Skip once orphan_shipment_needs_manual_review is already set so repeated events cannot loop.
	// Do not insert a timeline row here: clean/NotFound cancels leave the cancelled order's timeline
	// untouched (preserving P1-5), while FailedPrecondition records MANUAL_REVIEW_REQUIRED.
	if curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
		shipStatus != "" &&
		shipStatus != "cancelled" &&
		(curTracking == "" || (shipStatus == "created" && evTracking != "" && !strings.EqualFold(evTracking, curTracking))) {
		if !ord.OrphanShipmentNeedsManualReview {
			if err := q.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
				ID:                    ord.ID,
				PendingShipmentCancel: true,
				MarkedAt:              now,
				Token:                 "",
			}); err != nil {
				return nil, err
			}
			if ord.ShipmentJobStatus != "" {
				if err := q.ClearOrderShipmentJobStatus(ctx, ord.ID); err != nil {
					return nil, err
				}
			}
			outcome.NeedsOrphanShipmentCancel = true
		}
		agg, err := loadOrderAggregate(ctx, q, ord.ID)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		outcome.Order = agg
		return outcome, nil
	}

	// P1-5: Match tracking_code when the order already has one assigned (ignore stale replaced shipments).
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
	case "created":
		if evTracking == "" {
			break
		}
		carrierCode := evCarrier
		if carrierCode == "" {
			carrierCode = ord.CarrierCode
		}
		serviceCode := evService
		if serviceCode == "" {
			serviceCode = ord.ServiceCode
		}
		labelURL := evLabel
		if labelURL == "" {
			labelURL = ord.LabelURL
		}
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_SHIPPING {
			// Out-of-order created event after delivering already advanced the order to SHIPPING:
			// fill label/carrier metadata and clear job/pending flags without downgrading status.
			if _, err := q.UpdateOrderShippingLabel(ctx, db.UpdateOrderShippingLabelParams{
				ID:          ord.ID,
				CarrierCode: carrierCode,
				ServiceCode: serviceCode,
				LabelURL:    labelURL,
			}); err != nil {
				return nil, err
			}
		} else if curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			if curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING && curTracking != "" && strings.EqualFold(curTracking, evTracking) {
				// Idempotent replay: tracking already attached on PREPARING order.
				if (labelURL != "" && ord.LabelURL != labelURL) || ord.ShipmentJobStatus != "" || ord.PendingShipmentCancel {
					if _, err := q.UpdateOrderShippingLabel(ctx, db.UpdateOrderShippingLabelParams{
						ID:          ord.ID,
						CarrierCode: carrierCode,
						ServiceCode: serviceCode,
						LabelURL:    labelURL,
					}); err != nil {
						return nil, err
					}
				}
			} else {
				if _, err := q.UpdateOrderPreparingWithShipment(ctx, db.UpdateOrderPreparingWithShipmentParams{
					ID:           ord.ID,
					CarrierCode:  carrierCode,
					ServiceCode:  serviceCode,
					TrackingCode: evTracking,
					LabelURL:     labelURL,
				}); err != nil {
					return nil, err
				}
				createdNote := fmt.Sprintf("Shipment created asynchronously via %s (tracking: %s)", carrierCode, evTracking)
				if strings.TrimSpace(params.Description) != "" {
					createdNote = strings.TrimSpace(params.Description)
				}
				if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
					ID:         uuid.NewString(),
					OrderID:    ord.ID,
					FromStatus: ord.Status,
					ToStatus:   statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_PREPARING),
					ActorID:    "shipping-service",
					ActorRole:  "system",
					Note:       createdNote,
				}); err != nil {
					return nil, err
				}
				outcome.TransitionedFrom = curStatus
				outcome.TransitionedTo = orderv1.OrderStatus_ORDER_STATUS_PREPARING
			}
		}

	case "delivering", "failed", "returning":
		if curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING {
			shipNote := note
			if shipStatus != "delivering" {
				shipNote = "Shipment in transit (" + shipStatus + ")"
			}
			updated, err := transitionOrderToShippingInTx(ctx, q, ord, evCarrier, evService, params.TrackingCode, shipNote, params.TraceID, params.Traceparent)
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
			updated, err := transitionOrderToShippingInTx(ctx, q, ord, evCarrier, evService, params.TrackingCode, "Shipment picked up and in transit", params.TraceID, params.Traceparent)
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
				CarrierCode:  evCarrier,
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
			deliveredCarrier := strings.TrimSpace(updated.CarrierCode)
			if deliveredCarrier == "" {
				deliveredCarrier = evCarrier
			}
			payloadBytes, err := json.Marshal(events.OrderDeliveredPayload{
				OrderID:      updated.ID,
				OrderCode:    updated.OrderCode,
				BuyerID:      updated.BuyerID,
				ShopID:       updated.ShopID,
				CarrierCode:  deliveredCarrier,
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
			updated, err := transitionOrderToShippingInTx(ctx, q, ord, evCarrier, evService, params.TrackingCode, "Shipment picked up and in transit", params.TraceID, params.Traceparent)
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
			// P2-2: clamp single-order refund delta to remaining payable so duplicate/oversized events never exceed TotalPayableVND.
			if found && (params.PaymentID == "" || ord.PaymentID == "" || ord.PaymentID == params.PaymentID) {
				avail := max64(0, ord.TotalPayableVND-ord.RefundedVND)
				delta := min(max64(0, params.AmountVND), avail)
				newRefunded := ord.RefundedVND + delta
				newSettlementRefunded := nextSettlementRefundedVND(ord, delta, newRefunded, params.IsPostSettlement)
				nextPayStatus := ""
				if ord.TotalPayableVND > 0 && newRefunded >= ord.TotalPayableVND {
					nextPayStatus = "REFUNDED"
				}
				if err := q.UpdateOrderRefunded(ctx, db.UpdateOrderRefundedParams{
					ID:                    ord.ID,
					RefundedVND:           newRefunded,
					SettlementRefundedVND: newSettlementRefunded,
					PaymentStatus:         nextPayStatus,
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
				// F11 / Item C (#4c): atomically mark matching approval-gated REFUNDING return as APPROVED + refund_done=TRUE
				// inside the same TX (canonical lock order: orders -> return_requests) so ScanRefundingReturns recovers
				// remaining promo/restock/REFUNDED work even if post-TX side effects fail transiently.
				if rrPeek, rrErr := q.GetReturnRequestByOrderID(ctx, ord.ID); rrErr == nil {
					rrLocked, lockErr := q.GetReturnRequestByIDForUpdate(ctx, rrPeek.ID)
					if lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
						return false, lockErr
					}
					if lockErr == nil &&
						statemachine.StringToReturnStatus(rrLocked.Status) == orderv1.ReturnStatus_RETURN_STATUS_REFUNDING &&
						!rrLocked.RefundDone &&
						(rrLocked.RequireApprovalIfLarge || strings.EqualFold(rrLocked.RefundApprovalStatus, "PENDING_APPROVAL")) {
						if statemachine.MatchReturnRefundEvent(
							rrLocked.OrderID,
							rrLocked.RefundID,
							rrLocked.RefundAttempt,
							params.RefundID,
							params.IdempotencyKey,
						) == statemachine.ReturnRefundMatchMatched {
							refID := strings.TrimSpace(params.RefundID)
							var updAmt bool
							var amt int64
							if params.AmountVND > 0 {
								updAmt = true
								amt = params.AmountVND
							}
							if _, uErr := q.UpdateReturnRequestSideEffects(ctx, db.UpdateReturnRequestSideEffectsParams{
								ID:                         rrLocked.ID,
								RefundDone:                 true,
								PromoReleased:              rrLocked.PromoReleased,
								RestockDone:                rrLocked.RestockDone,
								UpdateRefundAmount:         updAmt,
								RefundAmountVND:            amt,
								UpdateRefundID:             refID != "",
								RefundID:                   refID,
								UpdateRefundApprovalStatus: true,
								RefundApprovalStatus:       "APPROVED",
							}); uErr != nil {
								return false, uErr
							}
						}
					}
				} else if !errors.Is(rrErr, pgx.ErrNoRows) {
					return false, rrErr
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
					delta := max64(0, target.TotalPayableVND-target.RefundedVND)
					newSettlementRefunded := nextSettlementRefundedVND(target, delta, target.TotalPayableVND, params.IsPostSettlement)
					if err := q.UpdateOrderRefunded(ctx, db.UpdateOrderRefundedParams{
						ID:                    target.ID,
						RefundedVND:           target.TotalPayableVND,
						SettlementRefundedVND: newSettlementRefunded,
						PaymentStatus:         "REFUNDED",
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
						newSettlementRefunded := nextSettlementRefundedVND(target, add, newRef, params.IsPostSettlement)
						targetPayStatus := ""
						if target.TotalPayableVND > 0 && newRef >= target.TotalPayableVND {
							targetPayStatus = "REFUNDED"
						}
						if err := q.UpdateOrderRefunded(ctx, db.UpdateOrderRefundedParams{
							ID:                    target.ID,
							RefundedVND:           newRef,
							SettlementRefundedVND: newSettlementRefunded,
							PaymentStatus:         targetPayStatus,
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

func (s *PostgresStore) releaseOutboxClaimIDs(ctx context.Context, ids []string, claimToken string) {
	if len(ids) == 0 {
		return
	}
	relCtx, relCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer relCancel()
	_, _ = s.queries.ReleaseOutboxEventClaims(relCtx, db.ReleaseOutboxEventClaimsParams{
		Ids:        ids,
		ClaimToken: claimToken,
	})
}

// ProcessPendingOutbox implements D9 / D9-bis transactional outbox relay with strict lease-budget enforcement:
//  1. Normalizes lease & pubTimeout (clamping pubTimeout < lease) and captures leaseDeadline BEFORE claiming.
//  2. Claims a batch in a short TX using two-tier CTE (NOT EXISTS active lease/backoff before LIMIT + eligible),
//     ordered strictly by monotonic seq ASC, then commits immediately.
//  3. Before each publish outside TX, verifies remaining lease budget >= pubTimeout; if insufficient, releases
//     remaining claims in a single UPDATE = ANY(...) query and returns.
//  4. Wraps each publishFn call in context.WithTimeout(ctx, pubTimeout) (single source of truth).
//     - Infra error (jetstream.IsInfraError): marks attempt without row backoff, releases remaining claims in one
//     query, and returns pubErr so OutboxRelay backs off (250ms -> 5s, ignoring triggers).
//     - Non-infra error: applies exponential row backoff min(2^attempts, 60)s, logs ERROR at attempts >= 10
//     (event_id + event_type, no payload, never dead-lettered), and skips later events of that aggregate.
//  5. On MarkOutboxEventPublished error or affected == 0 (lost lease), releases remaining claims in one query
//     and immediately stops the batch.
func (s *PostgresStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	lease, pubTimeout := s.outboxTimings()
	// Measure lease budget with Go clock captured BEFORE claiming so leaseDeadline <= DB claimed_until.
	leaseDeadline := s.outboxNow().Add(lease)

	leaseMs := lease.Milliseconds()
	if leaseMs <= 0 {
		leaseMs = 1
	}
	claimToken := uuid.NewString()

	// Phase 1: Short transaction to claim batch and commit immediately.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin outbox claim tx: %w", err)
	}
	rows, err := s.queries.WithTx(tx).ClaimPendingOutboxEvents(ctx, db.ClaimPendingOutboxEventsParams{
		LeaseMs:    leaseMs,
		ClaimToken: claimToken,
		BatchLimit: limit,
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		return 0, fmt.Errorf("claim pending outbox events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit outbox claim tx: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	// D9-bis §1: PostgreSQL UPDATE ... RETURNING does not guarantee output order; sort strictly by seq ASC.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Seq != rows[j].Seq {
			return rows[i].Seq < rows[j].Seq
		}
		return rows[i].ID < rows[j].ID
	})

	// Phase 2: Publish outside any DB transaction, enforcing lease budget and per-aggregate order.
	published := 0
	failedAggs := make(map[string]struct{})
	var skippedIDs []string
	for i, ev := range rows {
		if ctx.Err() != nil {
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i:]), claimToken)
			return published, ctx.Err()
		}
		if leaseDeadline.Sub(s.outboxNow()) < pubTimeout {
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i:]), claimToken)
			return published, nil
		}

		aggKey := outboxAggregateKey(ev)
		if _, blocked := failedAggs[aggKey]; blocked {
			skippedIDs = append(skippedIDs, ev.ID)
			continue
		}

		pubCtx, pubCancel := context.WithTimeout(ctx, pubTimeout)
		pubErr := publishFn(pubCtx, ev)
		pubCancel()
		if pubErr != nil {
			isInfra := jetstream.IsInfraError(pubErr)
			failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			newAttempts, failErr := s.queries.MarkOutboxEventAttemptFailed(failCtx, db.MarkOutboxEventAttemptFailedParams{
				ID:           ev.ID,
				LastError:    pubErr.Error(),
				ClaimToken:   claimToken,
				ApplyBackoff: !isInfra,
			})
			failCancel()
			if isInfra {
				releaseFrom := rows[i+1:]
				if failErr != nil {
					s.outboxLogger().WarnContext(ctx, "failed to record infra outbox failure; releasing current claim too",
						"event_id", ev.ID, "error", failErr)
					releaseFrom = rows[i:]
				}
				s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, releaseFrom), claimToken)
				return published, pubErr
			}
			if errors.Is(failErr, pgx.ErrNoRows) {
				// Lost lease while publishing this event; release remaining claims and abort batch.
				s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
				return published, nil
			}
			if failErr != nil {
				s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
				return published, fmt.Errorf("mark outbox event attempt failed: %w", failErr)
			}
			if newAttempts >= jetstream.OutboxPoisonLogAttempt {
				s.outboxLogger().ErrorContext(ctx, "outbox event repeatedly failing publish",
					"event_id", ev.ID,
					"event_type", ev.EventType,
					"attempts", newAttempts,
					"error", pubErr,
				)
			}
			failedAggs[aggKey] = struct{}{}
			continue
		}

		ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		affected, markErr := s.queries.MarkOutboxEventPublished(ackCtx, db.MarkOutboxEventPublishedParams{
			ID:         ev.ID,
			ClaimToken: claimToken,
		})
		ackCancel()
		if markErr != nil {
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
			return published, fmt.Errorf("mark outbox event published: %w", markErr)
		}
		if affected == 0 {
			// D9-bis §3: Lost lease on this event; immediately release remaining claims and stop batch.
			s.releaseOutboxClaimIDs(ctx, collectOutboxClaimIDs(skippedIDs, rows[i+1:]), claimToken)
			return published, nil
		}
		published++
	}

	if len(skippedIDs) > 0 {
		s.releaseOutboxClaimIDs(ctx, skippedIDs, claimToken)
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

func (s *PostgresStore) FindIncompleteCheckoutGroupsWithOrders(ctx context.Context, olderThan time.Duration, limit int32) ([]string, error) {
	return s.queries.FindIncompleteCheckoutGroupsWithOrders(ctx, db.FindIncompleteCheckoutGroupsWithOrdersParams{
		StaleAfterMs: olderThan.Milliseconds(),
		BatchLimit:   limit,
	})
}

func (s *PostgresStore) MarkCheckoutGroupNeedsCleanup(ctx context.Context, id, reason string) error {
	return s.queries.MarkCheckoutGroupNeedsCleanup(ctx, db.MarkCheckoutGroupNeedsCleanupParams{
		ID:     id,
		Reason: reason,
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
