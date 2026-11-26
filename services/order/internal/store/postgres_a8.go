package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

func (s *PostgresStore) GetOrderByCode(ctx context.Context, orderCode string) (*OrderAggregate, error) {
	ord, err := s.queries.GetOrderByCode(ctx, strings.TrimSpace(orderCode))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return loadOrderAggregate(ctx, s.queries, ord.ID)
}

func (s *PostgresStore) ListOrdersByIDs(ctx context.Context, orderIDs []string) ([]db.Order, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	return s.queries.ListOrdersByIDs(ctx, orderIDs)
}

func (s *PostgresStore) ListOrdersWithFilter(ctx context.Context, filter OrderListFilter) (*OrderListResult, error) {
	statuses := filter.Statuses
	if statuses == nil {
		statuses = []string{}
	}
	keywordPattern := EscapeILIKEPattern(filter.Keyword)

	total, err := s.queries.CountOrdersFiltered(ctx, db.CountOrdersFilteredParams{
		BuyerID:          filter.BuyerID,
		ShopID:           filter.ShopID,
		Status:           filter.Status,
		Statuses:         statuses,
		CarrierCode:      filter.CarrierCode,
		PaymentMethod:    filter.PaymentMethod,
		KeywordPattern:   keywordPattern,
		DueCutoff:        filter.DueCutoff,
		SalesChannel:     filter.SalesChannel,
		StorefrontShopID: filter.StorefrontShopID,
	})
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListOrdersFiltered(ctx, db.ListOrdersFilteredParams{
		BuyerID:          filter.BuyerID,
		ShopID:           filter.ShopID,
		Status:           filter.Status,
		Statuses:         statuses,
		CarrierCode:      filter.CarrierCode,
		PaymentMethod:    filter.PaymentMethod,
		KeywordPattern:   keywordPattern,
		DueCutoff:        filter.DueCutoff,
		SalesChannel:     filter.SalesChannel,
		StorefrontShopID: filter.StorefrontShopID,
		PageLimit:        filter.Limit,
		PageOffset:       filter.Offset,
	})
	if err != nil {
		return nil, err
	}

	var statusCounts map[string]int64
	if filter.IncludeStatusCounts {
		countRows, err := s.queries.CountOrdersByStatus(ctx, db.CountOrdersByStatusParams{
			BuyerID:          filter.BuyerID,
			ShopID:           filter.ShopID,
			SalesChannel:     filter.SalesChannel,
			StorefrontShopID: filter.StorefrontShopID,
		})
		if err != nil {
			return nil, err
		}
		statusCounts = make(map[string]int64, len(countRows))
		for _, cr := range countRows {
			statusCounts[cr.Status] = cr.Count
		}
	}

	orderIDs := make([]string, 0, len(rows))
	for _, ord := range rows {
		orderIDs = append(orderIDs, ord.ID)
	}
	retByOrder, err := loadReturnSummaries(ctx, s.queries, orderIDs)
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
		ret := retByOrder[ord.ID]
		aggs = append(aggs, OrderAggregate{
			Order:           ord,
			Items:           items,
			Timeline:        tl,
			ReturnRequestID: ret.ID,
			ReturnStatus:    ret.Status,
		})
	}
	return &OrderListResult{
		Orders:       aggs,
		Total:        total,
		StatusCounts: statusCounts,
	}, nil
}

func (s *PostgresStore) GetPickingListByOrderIDs(ctx context.Context, shopID string, orderIDs []string) ([]PickingListRow, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	rows, err := s.queries.GetPickingListByOrderIDs(ctx, db.GetPickingListByOrderIDsParams{
		ShopID:   shopID,
		OrderIds: orderIDs,
	})
	if err != nil {
		return nil, err
	}
	out := make([]PickingListRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, PickingListRow{
			SKUID:         r.SKUID,
			ProductID:     r.ProductID,
			ProductName:   r.ProductName,
			SKUName:       r.SKUName,
			TotalQuantity: r.TotalQuantity,
			OrderCount:    r.OrderCount,
			OrderCodes:    r.OrderCodes,
		})
	}
	return out, nil
}

func (s *PostgresStore) PrepareOrderForBatchShip(ctx context.Context, orderID, shopID, reqCarrier, reqService string, now time.Time) (*BatchShipPrepareOutcome, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin batch ship prepare tx: %w", err)
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
	if shopID != "" && ord.ShopID != shopID {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "FORBIDDEN",
			SkipMessage: "order does not belong to caller shop",
		}, nil
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "ORDER_CANCELLED",
			SkipMessage: "order was cancelled",
		}, nil
	}
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "INVALID_STATUS",
			SkipMessage: fmt.Sprintf("cannot create shipment from status %s", statemachine.StatusToString(curStatus)),
		}, nil
	}
	if trk := strings.TrimSpace(ord.TrackingCode); trk != "" {
		return &BatchShipPrepareOutcome{
			OrderCode:    ord.OrderCode,
			TrackingCode: trk,
			SkipReason:   "ALREADY_HAS_SHIPMENT",
			SkipMessage:  "order already has an active shipment",
		}, nil
	}
	if IsOrderShipmentQueued(ord) {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "ALREADY_QUEUED",
			SkipMessage: "order shipment creation is already queued",
		}, nil
	}
	hasOpenChange, err := q.HasOpenOrderAddressChange(ctx, ord.ID)
	if err != nil {
		return nil, err
	}
	if hasOpenChange {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "ADDRESS_UPDATE_SHIPMENT_IN_PROGRESS",
			SkipMessage: "order has an active address change in progress",
		}, nil
	}
	if isPendingShipmentCancelFresh(ord, now) {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "ALREADY_QUEUED",
			SkipMessage: "order shipment creation is already in progress",
		}, nil
	}
	if gid := strings.TrimSpace(ord.CheckoutGroupID); gid != "" {
		grp, gErr := q.GetCheckoutGroupByID(ctx, gid)
		if gErr != nil && !errors.Is(gErr, pgx.ErrNoRows) {
			return nil, gErr
		}
		if gErr == nil && grp.Status == "IN_PROGRESS" {
			return &BatchShipPrepareOutcome{
				OrderCode:   ord.OrderCode,
				SkipReason:  "PAYMENT_NOT_INITIALIZED",
				SkipMessage: "order payment initialization is still in progress",
			}, nil
		}
	}

	reqC := strings.TrimSpace(reqCarrier)
	reqS := strings.TrimSpace(reqService)
	ordC := strings.TrimSpace(ord.CarrierCode)
	ordS := strings.TrimSpace(ord.ServiceCode)
	if !strings.EqualFold(ordC, "fallback") {
		if reqC != "" && ordC != "" && !strings.EqualFold(reqC, ordC) {
			return &BatchShipPrepareOutcome{
				OrderCode:   ord.OrderCode,
				SkipReason:  "CARRIER_REJECTED",
				SkipMessage: fmt.Sprintf("carrier_code %q does not match buyer's selected carrier %q", reqC, ordC),
			}, nil
		}
		if reqS != "" && ordS != "" && !strings.EqualFold(reqS, ordS) {
			return &BatchShipPrepareOutcome{
				OrderCode:   ord.OrderCode,
				SkipReason:  "CARRIER_REJECTED",
				SkipMessage: fmt.Sprintf("service_code %q does not match buyer's selected service %q", reqS, ordS),
			}, nil
		}
	}

	token := uuid.NewString()
	if err := q.SetOrderPendingShipmentCancel(ctx, db.SetOrderPendingShipmentCancelParams{
		ID:                    ord.ID,
		PendingShipmentCancel: true,
		MarkedAt:              now,
		Token:                 token,
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
	return &BatchShipPrepareOutcome{
		Order:         agg,
		OrderCode:     ord.OrderCode,
		CreatingToken: token,
	}, nil
}

func (s *PostgresStore) GetOrderAddressChangeByOrderAndIdemKey(ctx context.Context, orderID, idemKey string) (*db.OrderAddressChange, error) {
	if strings.TrimSpace(idemKey) == "" {
		return nil, ErrNotFound
	}
	row, err := s.queries.GetOrderAddressChangeByOrderAndIdemKey(ctx, db.GetOrderAddressChangeByOrderAndIdemKeyParams{
		OrderID:        orderID,
		IdempotencyKey: idemKey,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) GetOrderAddressChangeByID(ctx context.Context, id string) (*db.OrderAddressChange, error) {
	row, err := s.queries.GetOrderAddressChangeByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) HasOpenOrderAddressChange(ctx context.Context, orderID string) (bool, error) {
	return s.queries.HasOpenOrderAddressChange(ctx, orderID)
}

func (s *PostgresStore) OpenOrderAddressChange(ctx context.Context, params OpenAddressChangeParams) (*OpenAddressChangeResult, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease := params.LeaseDuration
	if lease <= 0 {
		lease = 30 * time.Second
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin open address change tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := s.queries.WithTx(tx)
	var ord db.Order
	var siblings []db.Order
	if params.MaxCODGroupTotalVND > 0 {
		// O3: Enforce COD group ceiling transactionally while following the canonical lock order
		// used across postgres.go (orders in (shop_id, created_at, id) order -> checkout_groups)
		// to prevent AB-BA deadlocks with CancelOrder / ApplyShipmentUpdate / payment flows.
		peek, pErr := q.GetOrderByID(ctx, params.OrderID)
		if pErr != nil {
			if errors.Is(pErr, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, pErr
		}
		if gid := strings.TrimSpace(peek.CheckoutGroupID); gid != "" {
			siblings, err = q.ListOrdersByCheckoutGroupForUpdate(ctx, gid)
			if err != nil {
				return nil, err
			}
			if _, gErr := q.GetCheckoutGroupByIDForUpdate(ctx, gid); gErr != nil && !errors.Is(gErr, pgx.ErrNoRows) {
				return nil, gErr
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
		} else {
			ord, err = q.GetOrderByIDForUpdate(ctx, params.OrderID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return nil, ErrNotFound
				}
				return nil, err
			}
		}
	} else {
		ord, err = q.GetOrderByIDForUpdate(ctx, params.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
	}

	idemKey := strings.TrimSpace(params.IdempotencyKey)
	if idemKey != "" {
		existing, gErr := q.GetOrderAddressChangeByOrderAndIdemKey(ctx, db.GetOrderAddressChangeByOrderAndIdemKeyParams{
			OrderID:        ord.ID,
			IdempotencyKey: idemKey,
		})
		if gErr == nil {
			if existing.RequestHash != params.RequestHash {
				return nil, ErrAddressChangeIdemConflict
			}
			agg, aErr := loadOrderAggregate(ctx, q, ord.ID)
			if aErr != nil {
				return nil, aErr
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &OpenAddressChangeResult{
				Change:   existing,
				Order:    agg,
				Replayed: true,
			}, nil
		} else if !errors.Is(gErr, pgx.ErrNoRows) {
			return nil, gErr
		}
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return nil, fmt.Errorf("%w: current status is %s", ErrAddressUpdateNotAllowed, statemachine.StatusToString(curStatus))
	}
	if IsOrderShipmentQueued(ord) || ord.PendingShipmentCancel {
		return nil, ErrAddressChangeInProgress
	}
	hasOpen, err := q.HasOpenOrderAddressChange(ctx, ord.ID)
	if err != nil {
		return nil, err
	}
	if hasOpen {
		return nil, ErrAddressChangeInProgress
	}
	if ord.ShippingFeeVND != params.ExpectedOldShippingFeeVND || ord.TotalPayableVND != params.ExpectedOldTotalPayable ||
		(params.ExpectedOldAddressJSON != nil && !bytes.Equal(ord.ShippingAddressJSON, params.ExpectedOldAddressJSON)) {
		return nil, ErrAddressChangeInProgress
	}
	if floor := OrderFreeshipFloorVND(ord); params.AppliedShippingFeeVND < floor {
		return nil, fmt.Errorf("%w: applied shipping fee %d is below applied freeship floor %d",
			ErrAddressUpdateNotAllowed, params.AppliedShippingFeeVND, floor)
	}

	if params.MaxCODGroupTotalVND > 0 {
		groupTotal := params.NewTotalPayableVND
		if len(siblings) > 0 {
			sibIDs := make([]string, 0, len(siblings))
			for _, sib := range siblings {
				if sib.ID == ord.ID || statemachine.StringToStatus(sib.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
					continue
				}
				sibIDs = append(sibIDs, sib.ID)
			}
			openByOrder := make(map[string]db.OrderAddressChange, len(sibIDs))
			if len(sibIDs) > 0 {
				openChanges, oErr := q.ListOpenOrderAddressChangesByOrderIDs(ctx, sibIDs)
				if oErr != nil {
					return nil, oErr
				}
				for _, oc := range openChanges {
					openByOrder[oc.OrderID] = oc
				}
			}
			for _, sib := range siblings {
				if sib.ID == ord.ID || statemachine.StringToStatus(sib.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
					continue
				}
				sibTotal := sib.TotalPayableVND
				if oc, ok := openByOrder[sib.ID]; ok && oc.NewTotalPayableVND > sibTotal {
					sibTotal = oc.NewTotalPayableVND
				}
				groupTotal += sibTotal
			}
		}
		if groupTotal > params.MaxCODGroupTotalVND {
			return nil, &CODLimitExceededError{TotalVND: groupTotal, MaxVND: params.MaxCODGroupTotalVND}
		}
	}

	liveTracking := strings.TrimSpace(ord.TrackingCode)
	hadShipment := liveTracking != ""
	claimedUntil := now.Add(lease)
	changeID := params.ID
	if changeID == "" {
		changeID = uuid.NewString()
	}

	created, err := q.InsertOrderAddressChange(ctx, db.InsertOrderAddressChangeParams{
		ID:                    changeID,
		OrderID:               ord.ID,
		BuyerID:               params.BuyerID,
		IdempotencyKey:        idemKey,
		RequestHash:           params.RequestHash,
		NewAddressJSON:        params.NewAddressJSON,
		CarrierCode:           params.CarrierCode,
		ServiceCode:           params.ServiceCode,
		OldCarrierCode:        ord.CarrierCode,
		OldServiceCode:        ord.ServiceCode,
		OldTrackingCode:       liveTracking,
		HadShipment:           hadShipment,
		ShipmentCancelled:     false,
		OldShippingFeeVND:     ord.ShippingFeeVND,
		NewShippingFeeVND:     params.NewShippingFeeVND,
		AppliedShippingFeeVND: params.AppliedShippingFeeVND,
		OldTotalPayableVND:    ord.TotalPayableVND,
		NewTotalPayableVND:    params.NewTotalPayableVND,
		FeeAdjusted:           params.FeeAdjusted,
		NeedsCODAdjust:        params.NeedsCODAdjust,
		CODAdjusted:           false,
		State:                 AddressChangeStateOpen,
		NextAttemptAt:         now,
		ClaimedUntil:          &claimedUntil,
		ClaimToken:            params.ClaimToken,
		TraceID:               params.TraceID,
		Traceparent:           params.Traceparent,
		CreatedAt:             now,
		UpdatedAt:             now,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			_ = tx.Rollback(ctx)
			if idemKey != "" && pgErr.ConstraintName == "idx_order_address_changes_order_idem" {
				existing, rErr := s.GetOrderAddressChangeByOrderAndIdemKey(ctx, ord.ID, idemKey)
				if rErr == nil {
					if existing.RequestHash != params.RequestHash {
						return nil, ErrAddressChangeIdemConflict
					}
					agg, aErr := s.GetOrderWithDetails(ctx, ord.ID)
					if aErr != nil {
						return nil, aErr
					}
					return &OpenAddressChangeResult{
						Change:   *existing,
						Order:    agg,
						Replayed: true,
					}, nil
				}
			}
			return nil, ErrAddressChangeInProgress
		}
		return nil, err
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &OpenAddressChangeResult{
		Change:   created,
		Order:    agg,
		Replayed: false,
	}, nil
}

func (s *PostgresStore) AdvanceAddressChangeState(ctx context.Context, params AddressChangeProgressUpdate) (*db.OrderAddressChange, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if params.State == AddressChangeStateRestoringShipment {
		// O2: Atomically clear dead OldTrackingCode & label_url on the order when transitioning to RESTORING_SHIPMENT
		// so restored shipment.updated(created) events are never dropped and dead labels are never exposed.
		peek, err := s.queries.GetOrderAddressChangeByID(ctx, params.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrAddressChangeLostLease
			}
			return nil, err
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("begin restoring shipment state tx: %w", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()

		q := s.queries.WithTx(tx)
		ord, err := q.GetOrderByIDForUpdate(ctx, peek.OrderID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		ch, err := q.GetOrderAddressChangeByIDForUpdate(ctx, params.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrAddressChangeLostLease
			}
			return nil, err
		}
		if ch.ClaimToken != params.ExpectedClaimToken || IsAddressChangeTerminal(ch.State) {
			return nil, ErrAddressChangeLostLease
		}
		if oldTrk := strings.TrimSpace(ch.OldTrackingCode); oldTrk != "" && strings.EqualFold(strings.TrimSpace(ord.TrackingCode), oldTrk) {
			if _, err := q.UpdateOrderAddressAndFees(ctx, db.UpdateOrderAddressAndFeesParams{
				ID:                  ord.ID,
				ShippingAddressJSON: ord.ShippingAddressJSON,
				ShippingFeeVND:      ord.ShippingFeeVND,
				TotalPayableVND:     ord.TotalPayableVND,
				CarrierCode:         ord.CarrierCode,
				ServiceCode:         ord.ServiceCode,
				UpdatedAt:           now,
			}); err != nil {
				return nil, err
			}
		}
		row, err := q.UpdateOrderAddressChangeState(ctx, db.UpdateOrderAddressChangeStateParams{
			ID:                 params.ID,
			State:              params.State,
			ShipmentCancelled:  params.ShipmentCancelled,
			CODAdjusted:        params.CODAdjusted,
			ShipmentRecreated:  params.ShipmentRecreated,
			ShipmentQueued:     params.ShipmentQueued,
			ShipmentJobID:      params.ShipmentJobID,
			NewTrackingCode:    params.NewTrackingCode,
			OutboxEmitted:      params.OutboxEmitted,
			AbortCode:          params.AbortCode,
			LastError:          params.LastError,
			ClaimedUntil:       params.ClaimedUntil,
			ClaimToken:         params.ClaimToken,
			UpdatedAt:          now,
			ExpectedClaimToken: params.ExpectedClaimToken,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrAddressChangeLostLease
			}
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &row, nil
	}

	row, err := s.queries.UpdateOrderAddressChangeState(ctx, db.UpdateOrderAddressChangeStateParams{
		ID:                 params.ID,
		State:              params.State,
		ShipmentCancelled:  params.ShipmentCancelled,
		CODAdjusted:        params.CODAdjusted,
		ShipmentRecreated:  params.ShipmentRecreated,
		ShipmentQueued:     params.ShipmentQueued,
		ShipmentJobID:      params.ShipmentJobID,
		NewTrackingCode:    params.NewTrackingCode,
		OutboxEmitted:      params.OutboxEmitted,
		AbortCode:          params.AbortCode,
		LastError:          params.LastError,
		ClaimedUntil:       params.ClaimedUntil,
		ClaimToken:         params.ClaimToken,
		UpdatedAt:          now,
		ExpectedClaimToken: params.ExpectedClaimToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressChangeLostLease
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) ApplyAddressChangeToOrder(ctx context.Context, params ApplyAddressChangeParams) (*ApplyAddressChangeResult, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease := params.LeaseDuration
	if lease <= 0 {
		lease = 30 * time.Second
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin apply address change tx: %w", err)
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
	ch, err := q.GetOrderAddressChangeByIDForUpdate(ctx, params.ChangeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ch.OrderID != ord.ID {
		return nil, ErrNotFound
	}
	if ch.State == AddressChangeStateCompleted {
		agg, aErr := loadOrderAggregate(ctx, q, ord.ID)
		if aErr != nil {
			return nil, aErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ApplyAddressChangeResult{Change: ch, Order: agg}, nil
	}
	if ch.ClaimToken != params.ClaimToken {
		return nil, ErrAddressChangeLostLease
	}
	if ch.State == AddressChangeStateApplied {
		agg, aErr := loadOrderAggregate(ctx, q, ord.ID)
		if aErr != nil {
			return nil, aErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ApplyAddressChangeResult{Change: ch, Order: agg}, nil
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	belowFreeshipFloor := ch.AppliedShippingFeeVND < OrderFreeshipFloorVND(ord)
	if (curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING) || belowFreeshipFloor {
		abortReason := "order transitioned to " + ord.Status + " before apply"
		if belowFreeshipFloor {
			abortReason = fmt.Sprintf("applied shipping fee %d violates freeship floor %d", ch.AppliedShippingFeeVND, OrderFreeshipFloorVND(ord))
		}
		if ch.CODAdjusted {
			nextLease := now.Add(lease)
			upd, uErr := q.UpdateOrderAddressChangeState(ctx, db.UpdateOrderAddressChangeStateParams{
				ID:                 ch.ID,
				State:              AddressChangeStateRevertingCOD,
				ShipmentCancelled:  ch.ShipmentCancelled,
				CODAdjusted:        ch.CODAdjusted,
				ShipmentRecreated:  ch.ShipmentRecreated,
				ShipmentQueued:     ch.ShipmentQueued,
				ShipmentJobID:      ch.ShipmentJobID,
				NewTrackingCode:    ch.NewTrackingCode,
				OutboxEmitted:      ch.OutboxEmitted,
				AbortCode:          "ADDRESS_UPDATE_NOT_ALLOWED",
				LastError:          abortReason + "; reverting COD",
				ClaimedUntil:       &nextLease,
				ClaimToken:         params.ClaimToken,
				UpdatedAt:          now,
				ExpectedClaimToken: params.ClaimToken,
			})
			if uErr != nil {
				return nil, uErr
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &ApplyAddressChangeResult{
				Change:         upd,
				NeedsCODRevert: true,
				AbortedStatus:  ord.Status,
			}, nil
		}
		upd, uErr := q.UpdateOrderAddressChangeState(ctx, db.UpdateOrderAddressChangeStateParams{
			ID:                 ch.ID,
			State:              AddressChangeStateAborted,
			ShipmentCancelled:  ch.ShipmentCancelled,
			CODAdjusted:        ch.CODAdjusted,
			ShipmentRecreated:  ch.ShipmentRecreated,
			ShipmentQueued:     ch.ShipmentQueued,
			ShipmentJobID:      ch.ShipmentJobID,
			NewTrackingCode:    ch.NewTrackingCode,
			OutboxEmitted:      ch.OutboxEmitted,
			AbortCode:          "ADDRESS_UPDATE_NOT_ALLOWED",
			LastError:          abortReason,
			ClaimedUntil:       nil,
			ClaimToken:         "",
			UpdatedAt:          now,
			ExpectedClaimToken: params.ClaimToken,
		})
		if uErr != nil {
			return nil, uErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &ApplyAddressChangeResult{
			Change:        upd,
			Aborted:       true,
			AbortedStatus: ord.Status,
		}, nil
	}

	updatedOrd, err := q.UpdateOrderAddressAndFees(ctx, db.UpdateOrderAddressAndFeesParams{
		ID:                  ord.ID,
		ShippingAddressJSON: ch.NewAddressJSON,
		ShippingFeeVND:      ch.AppliedShippingFeeVND,
		TotalPayableVND:     ch.NewTotalPayableVND,
		CarrierCode:         ch.CarrierCode,
		ServiceCode:         ch.ServiceCode,
		UpdatedAt:           now,
	})
	if err != nil {
		return nil, err
	}
	if delta := ch.NewTotalPayableVND - ch.OldTotalPayableVND; delta != 0 && strings.TrimSpace(ord.CheckoutGroupID) != "" {
		if err := q.AdjustCheckoutGroupTotalPayable(ctx, db.AdjustCheckoutGroupTotalPayableParams{
			ID:       ord.CheckoutGroupID,
			DeltaVnd: delta,
		}); err != nil {
			return nil, err
		}
	}
	if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   ord.Status,
		ActorID:    ch.BuyerID,
		ActorRole:  "buyer",
		Note:       buildAddressChangedTimelineNote(ch),
	}); err != nil {
		return nil, err
	}

	ob, err := BuildOrderAddressUpdatedOutbox(updatedOrd, ch)
	if err != nil {
		return nil, err
	}
	if err := q.InsertOutboxEvent(ctx, ob); err != nil {
		return nil, err
	}

	nextState := AddressChangeStateApplied
	nextClaimToken := params.ClaimToken
	nextLease := now.Add(lease)
	nextClaimedUntil := &nextLease
	if !ch.HadShipment {
		nextState = AddressChangeStateCompleted
		nextClaimToken = ""
		nextClaimedUntil = nil
	}

	upd, err := q.UpdateOrderAddressChangeState(ctx, db.UpdateOrderAddressChangeStateParams{
		ID:                 ch.ID,
		State:              nextState,
		ShipmentCancelled:  ch.ShipmentCancelled,
		CODAdjusted:        ch.CODAdjusted,
		ShipmentRecreated:  ch.ShipmentRecreated,
		ShipmentQueued:     ch.ShipmentQueued,
		ShipmentJobID:      ch.ShipmentJobID,
		NewTrackingCode:    ch.NewTrackingCode,
		OutboxEmitted:      true,
		AbortCode:          "",
		LastError:          "",
		ClaimedUntil:       nextClaimedUntil,
		ClaimToken:         nextClaimToken,
		UpdatedAt:          now,
		ExpectedClaimToken: params.ClaimToken,
	})
	if err != nil {
		return nil, err
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ApplyAddressChangeResult{
		Change:        upd,
		Order:         agg,
		EmittedOutbox: true,
	}, nil
}

// FinalizeAddressChangeShipment records the outcome of recreating the forward shipment and closes the intent:
// APPLIED -> COMPLETED, or RESTORING_SHIPMENT (old address and fee kept after a rejected change) -> ABORTED.
func (s *PostgresStore) FinalizeAddressChangeShipment(ctx context.Context, params FinalizeAddressChangeShipmentParams) (*FinalizeAddressChangeResult, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin finalize address change shipment tx: %w", err)
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
	ch, err := q.GetOrderAddressChangeByIDForUpdate(ctx, params.ChangeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if ch.OrderID != ord.ID {
		return nil, ErrNotFound
	}
	curStatus := statemachine.StringToStatus(ord.Status)
	createdOrphan := curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
		!params.PermanentFail &&
		(strings.TrimSpace(params.TrackingCode) != "" || params.Queued)
	if IsAddressChangeTerminal(ch.State) {
		if createdOrphan {
			// O1: Even when the intent is already terminal (e.g., watchdog finished it before a stale caller returned),
			// never swallow an orphan shipment created on a CANCELLED order.
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
		}
		agg, aErr := loadOrderAggregate(ctx, q, ord.ID)
		if aErr != nil {
			return nil, aErr
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &FinalizeAddressChangeResult{
			Change:                    ch,
			Order:                     agg,
			NeedsOrphanShipmentCancel: createdOrphan,
		}, nil
	}
	if ch.ClaimToken != params.ClaimToken {
		if createdOrphan {
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
			agg, aErr := loadOrderAggregate(ctx, q, ord.ID)
			if aErr != nil {
				return nil, aErr
			}
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return &FinalizeAddressChangeResult{
				Change:                    ch,
				Order:                     agg,
				NeedsOrphanShipmentCancel: true,
			}, nil
		}
		return nil, ErrAddressChangeLostLease
	}

	restoring := ch.State == AddressChangeStateRestoringShipment
	shippable := curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING
	liveTracking := strings.TrimSpace(ord.TrackingCode)
	if restoring && shippable && strings.EqualFold(liveTracking, strings.TrimSpace(ch.OldTrackingCode)) {
		// Step 2 already cancelled this shipment at the carrier: never leave its dead tracking code on the order.
		if ord, err = q.UpdateOrderAddressAndFees(ctx, db.UpdateOrderAddressAndFeesParams{
			ID:                  ord.ID,
			ShippingAddressJSON: ord.ShippingAddressJSON,
			ShippingFeeVND:      ord.ShippingFeeVND,
			TotalPayableVND:     ord.TotalPayableVND,
			CarrierCode:         ord.CarrierCode,
			ServiceCode:         ord.ServiceCode,
			UpdatedAt:           now,
		}); err != nil {
			return nil, err
		}
		liveTracking = ""
	}

	recreated := false
	queued := false
	newTracking := strings.TrimSpace(params.TrackingCode)
	needsOrphanCancel := false
	toStatus := ord.Status
	note := ""

	switch {
	case params.PermanentFail:
		if shippable && liveTracking == "" {
			if _, err := q.SetOrderShipmentJobFailed(ctx, ord.ID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			note = addressShipmentNote(restoring, "failed", "", "")
		}

	case curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
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
		needsOrphanCancel = true

	case params.Queued && liveTracking != "":
		recreated = true
		newTracking = liveTracking
		if ord.ShipmentJobStatus != "" {
			if err := q.ClearOrderShipmentJobStatus(ctx, ord.ID); err != nil {
				return nil, err
			}
		}

	case params.Queued:
		if shippable {
			queued = true
			if _, err := q.SetOrderShipmentQueued(ctx, ord.ID); err != nil {
				return nil, err
			}
			note = addressShipmentNote(restoring, "queued", "", "")
		}

	default:
		recreated = true
		carrier := strings.TrimSpace(params.CarrierCode)
		if carrier == "" {
			carrier = ord.CarrierCode
		}
		service := strings.TrimSpace(params.ServiceCode)
		if service == "" {
			service = ord.ServiceCode
		}
		if shippable {
			if _, err := q.UpdateOrderPreparingWithShipment(ctx, db.UpdateOrderPreparingWithShipmentParams{
				ID:           ord.ID,
				CarrierCode:  carrier,
				ServiceCode:  service,
				TrackingCode: newTracking,
				LabelURL:     strings.TrimSpace(params.LabelURL),
			}); err != nil {
				return nil, err
			}
			toStatus = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_PREPARING)
			note = addressShipmentNote(restoring, "recreated", carrier, newTracking)
		}
	}

	if note != "" {
		if _, err := q.InsertTimelineEvent(ctx, db.InsertTimelineEventParams{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: ord.Status,
			ToStatus:   toStatus,
			ActorID:    "order-service",
			ActorRole:  "system",
			Note:       note,
		}); err != nil {
			return nil, err
		}
	}

	finalState, abortCode, lastErr := AddressChangeStateCompleted, "", strings.TrimSpace(params.LastError)
	if restoring {
		finalState, abortCode, lastErr = AddressChangeStateAborted, ch.AbortCode, ch.LastError
	}
	upd, err := q.UpdateOrderAddressChangeState(ctx, db.UpdateOrderAddressChangeStateParams{
		ID:                 ch.ID,
		State:              finalState,
		ShipmentCancelled:  ch.ShipmentCancelled,
		CODAdjusted:        ch.CODAdjusted,
		ShipmentRecreated:  recreated,
		ShipmentQueued:     queued,
		ShipmentJobID:      strings.TrimSpace(params.JobID),
		NewTrackingCode:    newTracking,
		OutboxEmitted:      ch.OutboxEmitted,
		AbortCode:          abortCode,
		LastError:          lastErr,
		ClaimedUntil:       nil,
		ClaimToken:         "",
		UpdatedAt:          now,
		ExpectedClaimToken: params.ClaimToken,
	})
	if err != nil {
		return nil, err
	}

	agg, err := loadOrderAggregate(ctx, q, ord.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &FinalizeAddressChangeResult{
		Change:                    upd,
		Order:                     agg,
		NeedsOrphanShipmentCancel: needsOrphanCancel,
	}, nil
}

func (s *PostgresStore) ReleaseAddressChangeWithBackoff(ctx context.Context, id, claimToken string, nextAttemptAt time.Time, lastErr string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.queries.ReleaseOrderAddressChangeWithBackoff(ctx, db.ReleaseOrderAddressChangeWithBackoffParams{
		ID:                 id,
		NextAttemptAt:      nextAttemptAt,
		LastError:          lastErr,
		UpdatedAt:          now,
		ExpectedClaimToken: claimToken,
	})
	return err
}

func (s *PostgresStore) RenewOrderAddressChangeLease(ctx context.Context, id, claimToken string, now time.Time, lease time.Duration) (*db.OrderAddressChange, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	until := now.Add(lease)
	row, err := s.queries.RenewOrderAddressChangeLease(ctx, db.RenewOrderAddressChangeLeaseParams{
		ID:                 id,
		ClaimedUntil:       &until,
		UpdatedAt:          now,
		ExpectedClaimToken: claimToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressChangeLostLease
		}
		return nil, err
	}
	return &row, nil
}

func (s *PostgresStore) ClaimDueOrderAddressChanges(ctx context.Context, now time.Time, lease time.Duration, claimToken string, limit int32) ([]db.OrderAddressChange, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if limit <= 0 {
		limit = 20
	}
	return s.queries.ClaimDueOrderAddressChanges(ctx, db.ClaimDueOrderAddressChangesParams{
		Now:        now,
		LeaseMs:    lease.Milliseconds(),
		ClaimToken: claimToken,
		ExcludeIds: []string{},
		BatchLimit: limit,
	})
}

func (s *PostgresStore) ClaimNextDueOrderAddressChange(ctx context.Context, now time.Time, lease time.Duration, claimToken string, excludeIDs []string) (*db.OrderAddressChange, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if excludeIDs == nil {
		excludeIDs = []string{}
	}
	rows, err := s.queries.ClaimDueOrderAddressChanges(ctx, db.ClaimDueOrderAddressChangesParams{
		Now:        now,
		LeaseMs:    lease.Milliseconds(),
		ClaimToken: claimToken,
		ExcludeIds: excludeIDs,
		BatchLimit: 1,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (s *PostgresStore) ClaimOrderAddressChangeByID(ctx context.Context, id, claimToken string, now time.Time, lease time.Duration) (*db.OrderAddressChange, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	until := now.Add(lease)
	row, err := s.queries.ClaimOrderAddressChangeByID(ctx, db.ClaimOrderAddressChangeByIDParams{
		ID:           id,
		ClaimedUntil: &until,
		ClaimToken:   claimToken,
		UpdatedAt:    now,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAddressChangeLostLease
		}
		return nil, err
	}
	return &row, nil
}
