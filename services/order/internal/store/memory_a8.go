package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

func cloneAddressChange(ch db.OrderAddressChange) db.OrderAddressChange {
	cp := ch
	if len(ch.NewAddressJSON) > 0 {
		cp.NewAddressJSON = append([]byte(nil), ch.NewAddressJSON...)
	}
	if ch.ClaimedUntil != nil {
		t := *ch.ClaimedUntil
		cp.ClaimedUntil = &t
	}
	return cp
}

func (m *MemoryStore) hasOpenAddressChangeLocked(orderID string) bool {
	for _, ch := range m.addressChanges {
		if ch.OrderID == orderID && !IsAddressChangeTerminal(ch.State) {
			return true
		}
	}
	return false
}

func (m *MemoryStore) isCancelledAddressChangeTrackingLocked(orderID, trackingCode string) bool {
	trk := strings.TrimSpace(trackingCode)
	if trk == "" {
		return false
	}
	for _, ch := range m.addressChanges {
		if ch.OrderID == orderID && ch.ShipmentCancelled && strings.TrimSpace(ch.OldTrackingCode) != "" &&
			strings.EqualFold(strings.TrimSpace(ch.OldTrackingCode), trk) {
			return true
		}
	}
	return false
}

func (m *MemoryStore) GetOrderByCode(_ context.Context, orderCode string) (*OrderAggregate, error) {
	code := strings.TrimSpace(orderCode)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ord := range m.orders {
		if ord.OrderCode == code {
			return m.getOrderLocked(ord.ID)
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) ListOrdersByIDs(_ context.Context, orderIDs []string) ([]db.Order, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]struct{}, len(orderIDs))
	out := make([]db.Order, 0, len(orderIDs))
	for _, id := range orderIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if ord, ok := m.orders[id]; ok {
			out = append(out, ord)
		}
	}
	return out, nil
}

func (m *MemoryStore) ListOrdersWithFilter(_ context.Context, filter OrderListFilter) (*OrderListResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	statusSet := make(map[string]struct{}, len(filter.Statuses))
	for _, st := range filter.Statuses {
		if st != "" {
			statusSet[st] = struct{}{}
		}
	}
	kwLower := strings.ToLower(strings.TrimSpace(filter.Keyword))

	var statusCounts map[string]int64
	if filter.IncludeStatusCounts {
		statusCounts = make(map[string]int64)
	}

	var matched []OrderAggregate
	for _, ord := range m.orders {
		if filter.BuyerID != "" && ord.BuyerID != filter.BuyerID {
			continue
		}
		if filter.ShopID != "" && ord.ShopID != filter.ShopID {
			continue
		}
		if filter.StorefrontShopID != "" && ord.StorefrontShopID != filter.StorefrontShopID {
			continue
		}
		if filter.SalesChannel != "" {
			ordCh := ord.SalesChannel
			if ordCh == "" {
				ordCh = "MARKETPLACE"
			}
			if ordCh != filter.SalesChannel {
				continue
			}
		}
		if filter.IncludeStatusCounts {
			statusCounts[ord.Status]++
		}
		if filter.Status != "" && ord.Status != filter.Status {
			continue
		}
		if len(statusSet) > 0 {
			if _, ok := statusSet[ord.Status]; !ok {
				continue
			}
		}
		if filter.CarrierCode != "" && ord.CarrierCode != filter.CarrierCode {
			continue
		}
		if filter.PaymentMethod != "" && ord.PaymentMethod != filter.PaymentMethod {
			continue
		}
		if kwLower != "" {
			var snap orderv1.OrderAddressSnapshot
			if len(ord.ShippingAddressJSON) > 0 {
				_ = json.Unmarshal(ord.ShippingAddressJSON, &snap)
			}
			if !strings.Contains(strings.ToLower(ord.OrderCode), kwLower) &&
				!strings.Contains(strings.ToLower(snap.GetRecipientName()), kwLower) {
				continue
			}
		}
		if filter.DueCutoff != nil {
			baseTime := ord.CreatedAt
			if ord.ConfirmedAt != nil {
				baseTime = *ord.ConfirmedAt
			}
			if baseTime.After(*filter.DueCutoff) {
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
	start := int(filter.Offset)
	if start < 0 {
		start = 0
	}
	if start >= len(matched) {
		return &OrderListResult{
			Orders:       []OrderAggregate{},
			Total:        total,
			StatusCounts: statusCounts,
		}, nil
	}
	end := start + int(filter.Limit)
	if filter.Limit <= 0 || end > len(matched) {
		end = len(matched)
	}
	return &OrderListResult{
		Orders:       matched[start:end],
		Total:        total,
		StatusCounts: statusCounts,
	}, nil
}

func (m *MemoryStore) GetPickingListByOrderIDs(_ context.Context, shopID string, orderIDs []string) ([]PickingListRow, error) {
	if len(orderIDs) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	idSet := make(map[string]struct{}, len(orderIDs))
	for _, id := range orderIDs {
		idSet[id] = struct{}{}
	}

	type skuAcc struct {
		row        PickingListRow
		orderIDs   map[string]struct{}
		orderCodes map[string]struct{}
	}
	bySKU := make(map[string]*skuAcc)
	for _, ord := range m.orders {
		if _, ok := idSet[ord.ID]; !ok {
			continue
		}
		if shopID != "" && ord.ShopID != shopID {
			continue
		}
		for _, it := range m.items[ord.ID] {
			acc, ok := bySKU[it.SKUID]
			if !ok {
				acc = &skuAcc{
					row: PickingListRow{
						SKUID:       it.SKUID,
						ProductID:   it.ProductID,
						ProductName: it.ProductName,
						SKUName:     it.SKUName,
					},
					orderIDs:   make(map[string]struct{}),
					orderCodes: make(map[string]struct{}),
				}
				bySKU[it.SKUID] = acc
			}
			acc.row.TotalQuantity += it.Quantity
			acc.orderIDs[ord.ID] = struct{}{}
			acc.orderCodes[ord.OrderCode] = struct{}{}
		}
	}

	out := make([]PickingListRow, 0, len(bySKU))
	for _, acc := range bySKU {
		codes := make([]string, 0, len(acc.orderCodes))
		for c := range acc.orderCodes {
			codes = append(codes, c)
		}
		sort.Strings(codes)
		acc.row.OrderCount = int32(len(acc.orderIDs))
		acc.row.OrderCodes = codes
		out = append(out, acc.row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProductName != out[j].ProductName {
			return out[i].ProductName < out[j].ProductName
		}
		return out[i].SKUID < out[j].SKUID
	})
	return out, nil
}

func (m *MemoryStore) PrepareOrderForBatchShip(_ context.Context, orderID, shopID, reqCarrier, reqService string, now time.Time) (*BatchShipPrepareOutcome, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	ord, ok := m.orders[orderID]
	if !ok {
		return nil, ErrNotFound
	}
	if shopID != "" && ord.ShopID != shopID {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "FORBIDDEN",
			SkipMessage: "order does not belong to caller shop",
		}, nil
	}

	cur := statemachine.StringToStatus(ord.Status)
	if cur == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "ORDER_CANCELLED",
			SkipMessage: "order was cancelled",
		}, nil
	}
	if cur != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && cur != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return &BatchShipPrepareOutcome{
			OrderCode:   ord.OrderCode,
			SkipReason:  "INVALID_STATUS",
			SkipMessage: fmt.Sprintf("cannot create shipment from status %s", statemachine.StatusToString(cur)),
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
	if m.hasOpenAddressChangeLocked(ord.ID) {
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
		if grp, ok := m.groups[gid]; ok && grp.Status == "IN_PROGRESS" {
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
	markedAt := now
	ord.PendingShipmentCancel = true
	ord.PendingShipmentCancelAt = &markedAt
	ord.PendingShipmentCancelToken = token
	ord.UpdatedAt = now
	m.orders[ord.ID] = ord

	agg, err := m.getOrderLocked(ord.ID)
	if err != nil {
		return nil, err
	}
	return &BatchShipPrepareOutcome{
		Order:         agg,
		OrderCode:     ord.OrderCode,
		CreatingToken: token,
	}, nil
}

func (m *MemoryStore) GetOrderAddressChangeByOrderAndIdemKey(_ context.Context, orderID, idemKey string) (*db.OrderAddressChange, error) {
	k := strings.TrimSpace(idemKey)
	if k == "" {
		return nil, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.addressChanges {
		if ch.OrderID == orderID && ch.IdempotencyKey == k {
			cp := cloneAddressChange(ch)
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) GetOrderAddressChangeByID(_ context.Context, id string) (*db.OrderAddressChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.addressChanges[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := cloneAddressChange(ch)
	return &cp, nil
}

func (m *MemoryStore) HasOpenOrderAddressChange(_ context.Context, orderID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasOpenAddressChangeLocked(orderID), nil
}

func (m *MemoryStore) OpenOrderAddressChange(_ context.Context, params OpenAddressChangeParams) (*OpenAddressChangeResult, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease := params.LeaseDuration
	if lease <= 0 {
		lease = 30 * time.Second
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}

	idemKey := strings.TrimSpace(params.IdempotencyKey)
	if idemKey != "" {
		for _, existing := range m.addressChanges {
			if existing.OrderID == ord.ID && existing.IdempotencyKey == idemKey {
				if existing.RequestHash != params.RequestHash {
					return nil, ErrAddressChangeIdemConflict
				}
				agg, err := m.getOrderLocked(ord.ID)
				if err != nil {
					return nil, err
				}
				return &OpenAddressChangeResult{
					Change:   cloneAddressChange(existing),
					Order:    agg,
					Replayed: true,
				}, nil
			}
		}
	}

	curStatus := statemachine.StringToStatus(ord.Status)
	if curStatus != orderv1.OrderStatus_ORDER_STATUS_CONFIRMED && curStatus != orderv1.OrderStatus_ORDER_STATUS_PREPARING {
		return nil, fmt.Errorf("%w: current status is %s", ErrAddressUpdateNotAllowed, statemachine.StatusToString(curStatus))
	}
	if IsOrderShipmentQueued(ord) || ord.PendingShipmentCancel {
		return nil, ErrAddressChangeInProgress
	}
	if m.hasOpenAddressChangeLocked(ord.ID) {
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
		if gid := strings.TrimSpace(ord.CheckoutGroupID); gid != "" {
			openByOrder := make(map[string]db.OrderAddressChange)
			for _, oc := range m.addressChanges {
				if !IsAddressChangeTerminal(oc.State) {
					openByOrder[oc.OrderID] = oc
				}
			}
			for _, sib := range m.orders {
				if sib.CheckoutGroupID != gid || sib.ID == ord.ID || statemachine.StringToStatus(sib.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
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

	ch := db.OrderAddressChange{
		ID:                    changeID,
		OrderID:               ord.ID,
		BuyerID:               params.BuyerID,
		IdempotencyKey:        idemKey,
		RequestHash:           params.RequestHash,
		NewAddressJSON:        append([]byte(nil), params.NewAddressJSON...),
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
	}
	m.addressChanges[ch.ID] = ch

	agg, err := m.getOrderLocked(ord.ID)
	if err != nil {
		return nil, err
	}
	return &OpenAddressChangeResult{
		Change:   cloneAddressChange(ch),
		Order:    agg,
		Replayed: false,
	}, nil
}

func (m *MemoryStore) AdvanceAddressChangeState(_ context.Context, params AddressChangeProgressUpdate) (*db.OrderAddressChange, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	ch, ok := m.addressChanges[params.ID]
	if !ok || ch.ClaimToken != params.ExpectedClaimToken {
		return nil, ErrAddressChangeLostLease
	}
	if params.State == AddressChangeStateRestoringShipment {
		if IsAddressChangeTerminal(ch.State) {
			return nil, ErrAddressChangeLostLease
		}
		if ord, exists := m.orders[ch.OrderID]; exists {
			if oldTrk := strings.TrimSpace(ch.OldTrackingCode); oldTrk != "" && strings.EqualFold(strings.TrimSpace(ord.TrackingCode), oldTrk) {
				ord.TrackingCode = ""
				ord.LabelURL = ""
				ord.ShipmentJobStatus = ""
				ord.PendingShipmentCancel = false
				ord.PendingShipmentCancelAt = nil
				ord.PendingShipmentCancelToken = ""
				ord.UpdatedAt = now
				m.orders[ord.ID] = ord
			}
		}
	}
	ch.State = params.State
	ch.ShipmentCancelled = params.ShipmentCancelled
	ch.CODAdjusted = params.CODAdjusted
	ch.ShipmentRecreated = params.ShipmentRecreated
	ch.ShipmentQueued = params.ShipmentQueued
	ch.ShipmentJobID = params.ShipmentJobID
	ch.NewTrackingCode = params.NewTrackingCode
	ch.OutboxEmitted = params.OutboxEmitted
	ch.AbortCode = params.AbortCode
	ch.LastError = params.LastError
	if params.ClaimedUntil != nil {
		t := *params.ClaimedUntil
		ch.ClaimedUntil = &t
	} else {
		ch.ClaimedUntil = nil
	}
	ch.ClaimToken = params.ClaimToken
	ch.UpdatedAt = now
	m.addressChanges[ch.ID] = ch

	cp := cloneAddressChange(ch)
	return &cp, nil
}

func (m *MemoryStore) ApplyAddressChangeToOrder(_ context.Context, params ApplyAddressChangeParams) (*ApplyAddressChangeResult, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	lease := params.LeaseDuration
	if lease <= 0 {
		lease = 30 * time.Second
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}
	ch, ok := m.addressChanges[params.ChangeID]
	if !ok || ch.OrderID != ord.ID {
		return nil, ErrNotFound
	}
	if ch.State == AddressChangeStateCompleted {
		agg, err := m.getOrderLocked(ord.ID)
		if err != nil {
			return nil, err
		}
		return &ApplyAddressChangeResult{Change: cloneAddressChange(ch), Order: agg}, nil
	}
	if ch.ClaimToken != params.ClaimToken {
		return nil, ErrAddressChangeLostLease
	}
	if ch.State == AddressChangeStateApplied {
		agg, err := m.getOrderLocked(ord.ID)
		if err != nil {
			return nil, err
		}
		return &ApplyAddressChangeResult{Change: cloneAddressChange(ch), Order: agg}, nil
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
			ch.State = AddressChangeStateRevertingCOD
			ch.AbortCode = "ADDRESS_UPDATE_NOT_ALLOWED"
			ch.LastError = abortReason + "; reverting COD"
			ch.ClaimedUntil = &nextLease
			ch.ClaimToken = params.ClaimToken
			ch.UpdatedAt = now
			m.addressChanges[ch.ID] = ch
			return &ApplyAddressChangeResult{
				Change:         cloneAddressChange(ch),
				NeedsCODRevert: true,
				AbortedStatus:  ord.Status,
			}, nil
		}
		ch.State = AddressChangeStateAborted
		ch.AbortCode = "ADDRESS_UPDATE_NOT_ALLOWED"
		ch.LastError = abortReason
		ch.ClaimedUntil = nil
		ch.ClaimToken = ""
		ch.UpdatedAt = now
		m.addressChanges[ch.ID] = ch
		return &ApplyAddressChangeResult{
			Change:        cloneAddressChange(ch),
			Aborted:       true,
			AbortedStatus: ord.Status,
		}, nil
	}

	ord.ShippingAddressJSON = append([]byte(nil), ch.NewAddressJSON...)
	ord.ShippingFeeVND = ch.AppliedShippingFeeVND
	ord.TotalPayableVND = ch.NewTotalPayableVND
	ord.CarrierCode = ch.CarrierCode
	ord.ServiceCode = ch.ServiceCode
	ord.TrackingCode = ""
	ord.LabelURL = ""
	ord.ShipmentJobStatus = ""
	ord.PendingShipmentCancel = false
	ord.PendingShipmentCancelAt = nil
	ord.PendingShipmentCancelToken = ""
	ord.UpdatedAt = now
	m.orders[ord.ID] = ord

	if delta := ch.NewTotalPayableVND - ch.OldTotalPayableVND; delta != 0 && strings.TrimSpace(ord.CheckoutGroupID) != "" {
		if grp, exists := m.groups[ord.CheckoutGroupID]; exists {
			grp.TotalPayableVND = max64(0, grp.TotalPayableVND+delta)
			grp.UpdatedAt = now
			m.groups[grp.ID] = grp
		}
	}

	m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
		ID:         uuid.NewString(),
		OrderID:    ord.ID,
		FromStatus: ord.Status,
		ToStatus:   ord.Status,
		ActorID:    ch.BuyerID,
		ActorRole:  "buyer",
		Note:       buildAddressChangedTimelineNote(ch),
		CreatedAt:  now,
	})

	ob, err := BuildOrderAddressUpdatedOutbox(ord, ch)
	if err != nil {
		return nil, err
	}
	m.appendOutboxLocked(db.OutboxEvent{
		ID:          ob.ID,
		EventType:   ob.EventType,
		TraceID:     ob.TraceID,
		Traceparent: ob.Traceparent,
		PayloadJSON: ob.PayloadJSON,
		CreatedAt:   now,
	})
	ch.OutboxEmitted = true

	if !ch.HadShipment {
		ch.State = AddressChangeStateCompleted
		ch.ClaimToken = ""
		ch.ClaimedUntil = nil
	} else {
		nextLease := now.Add(lease)
		ch.State = AddressChangeStateApplied
		ch.ClaimToken = params.ClaimToken
		ch.ClaimedUntil = &nextLease
	}
	ch.AbortCode = ""
	ch.LastError = ""
	ch.UpdatedAt = now
	m.addressChanges[ch.ID] = ch

	agg, err := m.getOrderLocked(ord.ID)
	if err != nil {
		return nil, err
	}
	return &ApplyAddressChangeResult{
		Change:        cloneAddressChange(ch),
		Order:         agg,
		EmittedOutbox: true,
	}, nil
}

// FinalizeAddressChangeShipment mirrors PostgresStore.FinalizeAddressChangeShipment for unit tests.
func (m *MemoryStore) FinalizeAddressChangeShipment(_ context.Context, params FinalizeAddressChangeShipmentParams) (*FinalizeAddressChangeResult, error) {
	now := params.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ord, ok := m.orders[params.OrderID]
	if !ok {
		return nil, ErrNotFound
	}
	ch, ok := m.addressChanges[params.ChangeID]
	if !ok || ch.OrderID != ord.ID {
		return nil, ErrNotFound
	}
	curStatus := statemachine.StringToStatus(ord.Status)
	createdOrphan := curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
		!params.PermanentFail &&
		(strings.TrimSpace(params.TrackingCode) != "" || params.Queued)
	if IsAddressChangeTerminal(ch.State) {
		if createdOrphan {
			markedAt := now
			ord.PendingShipmentCancel = true
			ord.PendingShipmentCancelAt = &markedAt
			ord.PendingShipmentCancelToken = ""
			ord.ShipmentJobStatus = ""
			ord.UpdatedAt = now
			m.orders[ord.ID] = ord
		}
		agg, err := m.getOrderLocked(ord.ID)
		if err != nil {
			return nil, err
		}
		return &FinalizeAddressChangeResult{
			Change:                    cloneAddressChange(ch),
			Order:                     agg,
			NeedsOrphanShipmentCancel: createdOrphan,
		}, nil
	}
	if ch.ClaimToken != params.ClaimToken {
		if createdOrphan {
			markedAt := now
			ord.PendingShipmentCancel = true
			ord.PendingShipmentCancelAt = &markedAt
			ord.PendingShipmentCancelToken = ""
			ord.ShipmentJobStatus = ""
			ord.UpdatedAt = now
			m.orders[ord.ID] = ord
			agg, err := m.getOrderLocked(ord.ID)
			if err != nil {
				return nil, err
			}
			return &FinalizeAddressChangeResult{
				Change:                    cloneAddressChange(ch),
				Order:                     agg,
				NeedsOrphanShipmentCancel: true,
			}, nil
		}
		return nil, ErrAddressChangeLostLease
	}

	restoring := ch.State == AddressChangeStateRestoringShipment
	shippable := curStatus == orderv1.OrderStatus_ORDER_STATUS_CONFIRMED || curStatus == orderv1.OrderStatus_ORDER_STATUS_PREPARING
	if restoring && shippable && strings.EqualFold(strings.TrimSpace(ord.TrackingCode), strings.TrimSpace(ch.OldTrackingCode)) {
		ord.TrackingCode = ""
		ord.LabelURL = ""
		ord.ShipmentJobStatus = ""
	}
	liveTracking := strings.TrimSpace(ord.TrackingCode)

	recreated := false
	queued := false
	newTracking := strings.TrimSpace(params.TrackingCode)
	needsOrphanCancel := false
	prevStatus := ord.Status
	note := ""

	switch {
	case params.PermanentFail:
		if shippable && liveTracking == "" {
			ord.ShipmentJobStatus = "FAILED"
			note = addressShipmentNote(restoring, "failed", "", "")
		}

	case curStatus == orderv1.OrderStatus_ORDER_STATUS_CANCELLED:
		markedAt := now
		ord.PendingShipmentCancel = true
		ord.PendingShipmentCancelAt = &markedAt
		ord.PendingShipmentCancelToken = ""
		ord.ShipmentJobStatus = ""
		needsOrphanCancel = true

	case params.Queued && liveTracking != "":
		recreated = true
		newTracking = liveTracking
		ord.ShipmentJobStatus = ""

	case params.Queued:
		if shippable {
			queued = true
			ord.ShipmentJobStatus = "QUEUED"
			ord.PendingShipmentCancel = false
			ord.PendingShipmentCancelAt = nil
			ord.PendingShipmentCancelToken = ""
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
			ord.Status = statemachine.StatusToString(orderv1.OrderStatus_ORDER_STATUS_PREPARING)
			ord.CarrierCode = carrier
			ord.ServiceCode = service
			ord.TrackingCode = newTracking
			ord.LabelURL = strings.TrimSpace(params.LabelURL)
			ord.ShipmentJobStatus = ""
			ord.PendingShipmentCancel = false
			ord.PendingShipmentCancelAt = nil
			ord.PendingShipmentCancelToken = ""
			note = addressShipmentNote(restoring, "recreated", carrier, newTracking)
		}
	}

	ord.UpdatedAt = now
	m.orders[ord.ID] = ord
	if note != "" {
		m.timelines[ord.ID] = append(m.timelines[ord.ID], db.OrderTimelineEvent{
			ID:         uuid.NewString(),
			OrderID:    ord.ID,
			FromStatus: prevStatus,
			ToStatus:   ord.Status,
			ActorID:    "order-service",
			ActorRole:  "system",
			Note:       note,
			CreatedAt:  now,
		})
	}

	ch.State = AddressChangeStateCompleted
	if restoring {
		ch.State = AddressChangeStateAborted
	} else {
		ch.AbortCode = ""
		ch.LastError = strings.TrimSpace(params.LastError)
	}
	ch.ShipmentRecreated = recreated
	ch.ShipmentQueued = queued
	ch.ShipmentJobID = strings.TrimSpace(params.JobID)
	ch.NewTrackingCode = newTracking
	ch.ClaimedUntil = nil
	ch.ClaimToken = ""
	ch.UpdatedAt = now
	m.addressChanges[ch.ID] = ch

	agg, err := m.getOrderLocked(ord.ID)
	if err != nil {
		return nil, err
	}
	return &FinalizeAddressChangeResult{
		Change:                    cloneAddressChange(ch),
		Order:                     agg,
		NeedsOrphanShipmentCancel: needsOrphanCancel,
	}, nil
}

func (m *MemoryStore) ReleaseAddressChangeWithBackoff(_ context.Context, id, claimToken string, nextAttemptAt time.Time, lastErr string, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.addressChanges[id]
	if !ok || ch.ClaimToken != claimToken || IsAddressChangeTerminal(ch.State) {
		return nil
	}
	ch.Attempts++
	ch.NextAttemptAt = nextAttemptAt
	ch.ClaimedUntil = nil
	ch.ClaimToken = ""
	ch.LastError = lastErr
	ch.UpdatedAt = now
	m.addressChanges[id] = ch
	return nil
}

func (m *MemoryStore) RenewOrderAddressChangeLease(_ context.Context, id, claimToken string, now time.Time, lease time.Duration) (*db.OrderAddressChange, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.addressChanges[id]
	if !ok || ch.ClaimToken != claimToken || IsAddressChangeTerminal(ch.State) {
		return nil, ErrAddressChangeLostLease
	}
	until := now.Add(lease)
	ch.ClaimedUntil = &until
	ch.UpdatedAt = now
	m.addressChanges[id] = ch
	cp := cloneAddressChange(ch)
	return &cp, nil
}

func (m *MemoryStore) ClaimDueOrderAddressChanges(ctx context.Context, now time.Time, lease time.Duration, claimToken string, limit int32) ([]db.OrderAddressChange, error) {
	return m.claimDueOrderAddressChangesFiltered(ctx, now, lease, claimToken, limit, nil)
}

func (m *MemoryStore) ClaimNextDueOrderAddressChange(ctx context.Context, now time.Time, lease time.Duration, claimToken string, excludeIDs []string) (*db.OrderAddressChange, error) {
	rows, err := m.claimDueOrderAddressChangesFiltered(ctx, now, lease, claimToken, 1, excludeIDs)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func (m *MemoryStore) claimDueOrderAddressChangesFiltered(_ context.Context, now time.Time, lease time.Duration, claimToken string, limit int32, excludeIDs []string) ([]db.OrderAddressChange, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	if limit <= 0 {
		limit = 20
	}

	excluded := make(map[string]struct{}, len(excludeIDs))
	for _, id := range excludeIDs {
		excluded[id] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var due []db.OrderAddressChange
	for _, ch := range m.addressChanges {
		if IsAddressChangeTerminal(ch.State) {
			continue
		}
		if _, skip := excluded[ch.ID]; skip {
			continue
		}
		if ch.NextAttemptAt.After(now) {
			continue
		}
		if ch.ClaimedUntil != nil && ch.ClaimedUntil.After(now) {
			continue
		}
		due = append(due, ch)
	}
	sort.Slice(due, func(i, j int) bool {
		if !due[i].NextAttemptAt.Equal(due[j].NextAttemptAt) {
			return due[i].NextAttemptAt.Before(due[j].NextAttemptAt)
		}
		if !due[i].CreatedAt.Equal(due[j].CreatedAt) {
			return due[i].CreatedAt.Before(due[j].CreatedAt)
		}
		return due[i].ID < due[j].ID
	})
	if int(limit) < len(due) {
		due = due[:limit]
	}

	until := now.Add(lease)
	claimed := make([]db.OrderAddressChange, 0, len(due))
	for _, ch := range due {
		u := until
		ch.ClaimedUntil = &u
		ch.ClaimToken = claimToken
		ch.UpdatedAt = now
		m.addressChanges[ch.ID] = ch
		claimed = append(claimed, cloneAddressChange(ch))
	}
	return claimed, nil
}

func (m *MemoryStore) ClaimOrderAddressChangeByID(_ context.Context, id, claimToken string, now time.Time, lease time.Duration) (*db.OrderAddressChange, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ch, ok := m.addressChanges[id]
	if !ok || IsAddressChangeTerminal(ch.State) {
		return nil, ErrAddressChangeLostLease
	}
	if ch.ClaimedUntil != nil && ch.ClaimedUntil.After(now) {
		return nil, ErrAddressChangeLostLease
	}
	until := now.Add(lease)
	ch.ClaimedUntil = &until
	ch.ClaimToken = claimToken
	ch.UpdatedAt = now
	m.addressChanges[id] = ch
	cp := cloneAddressChange(ch)
	return &cp, nil
}
