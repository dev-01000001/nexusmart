package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/events"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

// IsTerminalReturnFailureRawStatus reports whether a carrier raw_status on a "failed"
// RETURN shipment represents a terminal loss/damage event requiring admin arbitration,
// rather than a retryable delivery attempt failure.
func IsTerminalReturnFailureRawStatus(raw string) bool {
	n := strings.ToLower(strings.TrimSpace(raw))
	if n == "" {
		return false
	}
	return strings.Contains(n, "lost") || strings.Contains(n, "damage") || strings.Contains(n, "destroy")
}

func isUnpaidPendingStatus(paymentStatus string) bool {
	return strings.EqualFold(paymentStatus, "PENDING") || strings.EqualFold(paymentStatus, "COD_PENDING")
}

func isFailedOrExpiredStatus(paymentStatus string) bool {
	return strings.EqualFold(paymentStatus, "FAILED") || strings.EqualFold(paymentStatus, "EXPIRED")
}

func isOrderRefunded(ord db.Order) bool {
	return strings.EqualFold(ord.PaymentStatus, "REFUNDED") ||
		(ord.TotalPayableVND > 0 && ord.RefundedVND >= ord.TotalPayableVND)
}

// shouldMarkGroupRefunded reports whether every remaining non-cancelled payable sub-order in the group
// is REFUNDED and no active 0-VND sub-order remains (review2-O P2-5).
func shouldMarkGroupRefunded(groupOrders []db.Order, overrideID, overrideStatus, overridePayStatus string) bool {
	hasRefundedPayable := false
	for _, sib := range groupOrders {
		stStr := sib.Status
		paySt := sib.PaymentStatus
		if sib.ID == overrideID {
			if overrideStatus != "" {
				stStr = overrideStatus
			}
			if overridePayStatus != "" {
				paySt = overridePayStatus
			}
		}
		st := statemachine.StringToStatus(stStr)
		if sib.TotalPayableVND <= 0 {
			if st != orderv1.OrderStatus_ORDER_STATUS_CANCELLED && st != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
				return false
			}
			continue
		}
		if (st == orderv1.OrderStatus_ORDER_STATUS_CANCELLED || st == orderv1.OrderStatus_ORDER_STATUS_RETURNED) &&
			strings.EqualFold(paySt, "CANCELLED") {
			continue
		}
		if !strings.EqualFold(paySt, "REFUNDED") {
			return false
		}
		hasRefundedPayable = true
	}
	return hasRefundedPayable
}

// shouldMarkGroupCancelled reports whether every sub-order in the checkout group is terminal
// (CANCELLED or RETURNED) and no paid online (VIETQR/ZALOPAY) payable sub-order exists.
func shouldMarkGroupCancelled(groupOrders []db.Order, overrideID, overrideStatus, overridePayStatus string) bool {
	if len(groupOrders) == 0 {
		return false
	}
	for _, sib := range groupOrders {
		stStr := sib.Status
		paySt := sib.PaymentStatus
		if sib.ID == overrideID {
			if overrideStatus != "" {
				stStr = overrideStatus
			}
			if overridePayStatus != "" {
				paySt = overridePayStatus
			}
		}
		st := statemachine.StringToStatus(stStr)
		if st != orderv1.OrderStatus_ORDER_STATUS_CANCELLED && st != orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			return false
		}
		if statemachine.IsOnlineMethod(sib.PaymentMethod) &&
			(strings.EqualFold(paySt, "SUCCEEDED") || strings.EqualFold(paySt, "REFUNDED")) &&
			sib.TotalPayableVND > 0 {
			return false
		}
	}
	return true
}

// PendingShipmentCancelTTL is the maximum age an active pending_shipment_cancel flag
// (with no tracking_code yet) blocks cancellation when Shipping.CancelShipment returns NotFound.
// Derivation: ORDER_GRPC_TIMEOUT (10s) + shipping CARRIER_TIMEOUT (3s; GHN fallback 5s) +
// retries/backoff (~15s) + gateway/proxy margin (~30s) < 1m; 5m provides >10x safety margin
// over any live in-flight CreateShipment while preventing crashed/Unavailable attempts from
// blocking cancellations permanently.
const PendingShipmentCancelTTL = 5 * time.Minute

func isPendingShipmentCancelFresh(ord db.Order, now time.Time) bool {
	if !ord.PendingShipmentCancel {
		return false
	}
	markedAt := ord.UpdatedAt
	if ord.PendingShipmentCancelAt != nil {
		markedAt = *ord.PendingShipmentCancelAt
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.Sub(markedAt) <= PendingShipmentCancelTTL
}

// IsOrderShipmentQueued reports whether an order currently has an async shipment creation job queued.
func IsOrderShipmentQueued(ord db.Order) bool {
	return strings.EqualFold(strings.TrimSpace(ord.ShipmentJobStatus), "QUEUED")
}

// checkCancelShipmentGuard enforces P0-1 & E18 under FOR UPDATE when transitioning an order to CANCELLED:
// any non-empty tracking_code must match the tracking code cancelled by Shipping.CancelShipment
// (or match KnownTrackingBeforeCancel when CancelShipment returned NotFound and no fresh creation or QUEUED job exists),
// a fresh in-flight shipment creation (pending_shipment_cancel=true with age <= PendingShipmentCancelTTL)
// cannot be bypassed after a NotFound cancel, and a QUEUED async shipment job is treated as active shipment
// creation: CancelShipment(FORWARD) must have been called for the observed QUEUED state before committing CANCELLED.
func checkCancelShipmentGuard(ord db.Order, params SingleOrderTransitionParams, now time.Time) error {
	if params.TargetStatus != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
		return nil
	}
	if !params.VerifyShipmentCancel && !params.RequireNoTracking {
		return nil
	}
	ordTrk := strings.TrimSpace(ord.TrackingCode)
	canTrk := strings.TrimSpace(params.CancelledTrackingCode)
	knownTrk := strings.TrimSpace(params.KnownTrackingBeforeCancel)
	if ordTrk != "" {
		if canTrk != "" && strings.EqualFold(ordTrk, canTrk) {
			return nil
		}
		// Pre-existing tracking where shipping confirmed NotFound and no fresh shipment creation or QUEUED job exists.
		if params.LastCancelNotFound && !isPendingShipmentCancelFresh(ord, now) && !IsOrderShipmentQueued(ord) && knownTrk != "" && strings.EqualFold(ordTrk, knownTrk) {
			return nil
		}
		return ErrOrderHasTracking
	}
	if ord.PendingShipmentCancel {
		// Always require at least one CancelShipment probe when RequireNoTracking=true (even past TTL)
		// so a real shipment created before a crash is cancelled or blocked if already picked up.
		if params.RequireNoTracking {
			return ErrOrderHasTracking
		}
		if params.LastCancelNotFound && isPendingShipmentCancelFresh(ord, now) {
			return ErrOrderHasTracking
		}
	}
	if IsOrderShipmentQueued(ord) {
		// E18: A QUEUED shipment job may have already created a shipment or been picked up by the carrier
		// before order receives shipment.updated(created). Always require Shipping.CancelShipment(FORWARD)
		// before committing CANCELLED, and reject if QUEUED appeared or was refreshed between steps.
		if params.RequireNoTracking || !params.KnownShipmentQueued {
			return ErrOrderHasTracking
		}
		if params.KnownQueuedUpdatedAt != nil &&
			ord.UpdatedAt.Truncate(time.Microsecond).After(params.KnownQueuedUpdatedAt.Truncate(time.Microsecond)) {
			return ErrOrderHasTracking
		}
	}
	return nil
}

func shouldClearPendingShipmentCancelOnCancel(ord db.Order, params SingleOrderTransitionParams, now time.Time) bool {
	if !ord.PendingShipmentCancel {
		return false
	}
	if params.ClearPendingShipmentCancel && strings.TrimSpace(ord.TrackingCode) != "" {
		return true
	}
	if params.LastCancelNotFound && !isPendingShipmentCancelFresh(ord, now) {
		return true
	}
	if !params.LastCancelNotFound && strings.TrimSpace(params.CancelledTrackingCode) != "" {
		return true
	}
	return false
}

// mergeUpdatedOrders returns a copy of siblings with each transitioned order's updated fields applied.
func mergeUpdatedOrders(siblings, transitioned []db.Order) []db.Order {
	if len(transitioned) == 0 {
		return siblings
	}
	byID := make(map[string]db.Order, len(transitioned))
	for _, tr := range transitioned {
		byID[tr.ID] = tr
	}
	out := make([]db.Order, len(siblings))
	for i, sib := range siblings {
		if upd, ok := byID[sib.ID]; ok {
			out[i] = upd
		} else {
			out[i] = sib
		}
	}
	return out
}

func appendPreviousReturnTracking(rawJSON []byte, tracking string) ([]byte, error) {
	trk := strings.TrimSpace(tracking)
	var list []string
	if len(rawJSON) > 0 {
		_ = json.Unmarshal(rawJSON, &list)
	}
	if trk != "" {
		exists := false
		for _, existing := range list {
			if strings.EqualFold(strings.TrimSpace(existing), trk) {
				exists = true
				break
			}
		}
		if !exists {
			list = append(list, trk)
		}
	}
	if list == nil {
		list = []string{}
	}
	return json.Marshal(list)
}

func isPreviousReturnTracking(rawJSON []byte, tracking string) bool {
	trk := strings.TrimSpace(tracking)
	if trk == "" || len(rawJSON) == 0 {
		return false
	}
	var list []string
	if err := json.Unmarshal(rawJSON, &list); err != nil {
		return false
	}
	for _, existing := range list {
		if strings.EqualFold(strings.TrimSpace(existing), trk) {
			return true
		}
	}
	return false
}

var (
	ErrNotFound                      = errors.New("record not found")
	ErrDuplicateIdempotencyKey       = errors.New("duplicate active idempotency key")
	ErrCheckoutGroupNotInProgress    = errors.New("checkout group is not in progress")
	ErrCheckoutGroupFailedWithOrders = errors.New("checkout group already failed after persisting orders")
	ErrDuplicateReturnRequest        = errors.New("return request already exists for order")
	ErrAlreadyProcessed              = errors.New("event already processed")
	ErrRetryPrecondition             = errors.New("order group is not eligible for payment retry")
	ErrReturnDeadlineNotReached      = errors.New("return deadline has not expired")
	ErrOrderHasTracking              = errors.New("order already has tracking code")
	ErrOrphanShipmentNeedsCancel     = fmt.Errorf("%w: order was cancelled while creating shipment", statemachine.ErrInvalidTransition)
	ErrStaleShipmentAttempt          = fmt.Errorf("%w: shipment creation attempt expired or superseded", statemachine.ErrInvalidTransition)
	ErrReturnTrackingPending         = errors.New("return shipment tracking code not yet persisted; retry event")
	ErrOrderNotNeedingReview         = errors.New("order does not require manual review")
)

// FailedCheckoutWithOrdersError is returned by CreateCheckoutGroup when a previous FAILED checkout
// group for the same (buyer_id, idempotency_key) already persisted sub-orders (D11).
type FailedCheckoutWithOrdersError struct {
	GroupID string
	Reason  string
}

func (e *FailedCheckoutWithOrdersError) Error() string {
	return fmt.Sprintf("%s: group %s failed (%s)", ErrCheckoutGroupFailedWithOrders.Error(), e.GroupID, e.Reason)
}

func (e *FailedCheckoutWithOrdersError) Unwrap() error {
	return ErrCheckoutGroupFailedWithOrders
}

// CheckoutGroupLookup pairs a checkout_group row with whether it already has persisted sub-orders.
type CheckoutGroupLookup struct {
	Group     db.CheckoutGroup
	HasOrders bool
}

// OrderReportParams configures a seller (ShopID != "") or platform (ShopID == "") report query.
// StartTS (inclusive) and EndTS (exclusive) represent Asia/Ho_Chi_Minh calendar day bounds in UTC.
type OrderReportParams struct {
	ShopID   string
	StartTS  time.Time
	EndTS    time.Time
	TopLimit int32
}

// CreatedDailyStat aggregates orders created on a single Vietnam calendar date (YYYY-MM-DD).
type CreatedDailyStat struct {
	Date           string
	OrderCount     int64
	CancelledCount int64
	ReturnedCount  int64
	GMVVND         int64
}

// CompletedDailyStat aggregates orders completed on a single Vietnam calendar date (YYYY-MM-DD).
type CompletedDailyStat struct {
	Date               string
	CompletedCount     int64
	NetRevenueVND      int64
	CommissionVND      int64
	PaymentFeeVND      int64
	PlatformSubsidyVND int64
}

// TopProductRow aggregates sales for one product across COMPLETED orders in the reporting window.
type TopProductRow struct {
	ProductID   string
	ProductName string
	UnitsSold   int64
	RevenueVND  int64
	OrderCount  int64
}

// OrderReportData holds raw aggregated metrics returned by Store.QueryOrderReport.
type OrderReportData struct {
	StatusCounts   map[string]int64
	CreatedDaily   []CreatedDailyStat
	CompletedDaily []CompletedDailyStat
	TopProducts    []TopProductRow
}

// OrderAggregate groups a persisted order with its line items, timeline history, and return summary.
type OrderAggregate struct {
	Order           db.Order
	Items           []db.OrderItem
	Timeline        []db.OrderTimelineEvent
	ReturnRequestID string
	ReturnStatus    string
}

// ReturnAggregate groups a persisted return_requests row with its append-only return_events timeline.
type ReturnAggregate struct {
	Request db.ReturnRequest
	Events  []db.ReturnEvent
}

// NewOrderRecord bundles everything inserted atomically for a single sub-order.
type NewOrderRecord struct {
	Order    db.InsertOrderParams
	Items    []db.InsertOrderItemParams
	Timeline []db.InsertTimelineEventParams
	Outbox   []db.InsertOutboxEventParams
}

// CheckoutBatch holds all sub-orders created in one checkout transaction.
type CheckoutBatch struct {
	CheckoutGroupID     string
	Orders              []NewOrderRecord
	PendingStockConfirm bool   // Set true atomically for COD / 0-VND checkouts starting CONFIRMED.
	WarningsJSON        []byte // Persisted onto checkout_groups atomically so forward recovery keeps warnings (N7).
	Now                 time.Time
}

// UpdateCheckoutPaymentParams updates payment details on a checkout group and all its sub-orders.
type UpdateCheckoutPaymentParams struct {
	CheckoutGroupID      string
	PaymentID            string
	PaymentStatus        string
	QRURL                string
	QRCode               string
	PayURL               string
	PaymentExpiresAt     *time.Time
	ResponseSnapshotJSON []byte
	// BuildSnapshot, when non-nil, is invoked inside the transaction after locking orders and
	// applying non-downgrading payment updates so response_snapshot_json reflects real current state.
	BuildSnapshot func(grp db.CheckoutGroup, aggs []OrderAggregate) ([]byte, error)
}

func buildOrderCancelledOutbox(ord db.Order, reason, traceID, traceparent string) (db.InsertOutboxEventParams, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "Order cancelled before payment creation"
	}
	payloadBytes, err := json.Marshal(events.OrderCancelledPayload{
		OrderID:   ord.ID,
		OrderCode: ord.OrderCode,
		BuyerID:   ord.BuyerID,
		ShopID:    ord.ShopID,
		Reason:    reason,
		WasPaid:   false,
	})
	if err != nil {
		return db.InsertOutboxEventParams{}, err
	}
	return db.InsertOutboxEventParams{
		ID:          uuid.NewString(),
		EventType:   events.SubjectOrderCancelled,
		TraceID:     traceID,
		Traceparent: traceparent,
		PayloadJSON: payloadBytes,
	}, nil
}

// shouldReemitOrderCancelledForPayment reports whether a sub-order was already CANCELLED before
// payment existed (empty payment_id, not yet REFUNDED) and therefore needs a fresh order.cancelled
// outbox event once paymentID exists so payment cancels/refunds it idempotently (P0-1 / P1-2).
func shouldReemitOrderCancelledForPayment(ord db.Order, paymentID string) bool {
	return strings.TrimSpace(paymentID) != "" &&
		statemachine.StringToStatus(ord.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED &&
		strings.TrimSpace(ord.PaymentID) == "" &&
		!strings.EqualFold(strings.TrimSpace(ord.PaymentStatus), "REFUNDED")
}

func sumLiveOrderPayableVND(orders []db.Order) int64 {
	var total int64
	for _, o := range orders {
		if statemachine.StringToStatus(o.Status) == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			continue
		}
		total += o.TotalPayableVND
	}
	return total
}

func isPendingOrEmptyPaymentStatus(st string) bool {
	s := strings.ToUpper(strings.TrimSpace(st))
	return s == "" || s == "PENDING"
}

// ResolveAllCancelledPaymentStatus chooses the closing payment_status for an all-cancelled group,
// preserving terminal expiry/refund/failure reasons (EXPIRED > REFUNDED > FAILED > other > CANCELLED).
func ResolveAllCancelledPaymentStatus(orders []db.Order) string {
	var fallback string
	for _, o := range orders {
		st := strings.ToUpper(strings.TrimSpace(o.PaymentStatus))
		if isPendingOrEmptyPaymentStatus(st) {
			continue
		}
		if st == "EXPIRED" {
			return "EXPIRED"
		}
		if st == "REFUNDED" && fallback != "REFUNDED" {
			fallback = "REFUNDED"
		} else if st == "FAILED" && fallback != "REFUNDED" && fallback != "FAILED" {
			fallback = "FAILED"
		} else if fallback == "" {
			fallback = st
		}
	}
	if fallback != "" {
		return fallback
	}
	return "CANCELLED"
}

func outboxAggregateKey(ev db.OutboxEvent) string {
	if len(ev.PayloadJSON) > 0 {
		var p struct {
			OrderID string `json:"order_id"`
		}
		if err := json.Unmarshal(ev.PayloadJSON, &p); err == nil && strings.TrimSpace(p.OrderID) != "" {
			return strings.TrimSpace(p.OrderID)
		}
	}
	return ev.ID
}

// clampOutboxTimings normalizes lease and per-event publish timeout using shared jetstream defaults
// and guarantees pubTimeout < lease (P3-8) so the first event in a claimed batch is never starved.
func clampOutboxTimings(lease, pubTimeout time.Duration) (time.Duration, time.Duration) {
	if lease <= 0 {
		lease = jetstream.DefaultOutboxLease
	}
	if lease < 2*time.Millisecond {
		lease = 2 * time.Millisecond
	}
	if pubTimeout <= 0 {
		pubTimeout = jetstream.DefaultOutboxPublishTimeout
	}
	if pubTimeout >= lease {
		pubTimeout = lease / 2
	}
	return lease, pubTimeout
}

func collectOutboxClaimIDs(skipped []string, remaining []db.OutboxEvent) []string {
	total := len(skipped) + len(remaining)
	if total == 0 {
		return nil
	}
	ids := make([]string, 0, total)
	ids = append(ids, skipped...)
	for _, ev := range remaining {
		ids = append(ids, ev.ID)
	}
	return ids
}

func allOrdersCancelledDB(orders []db.Order) bool {
	if len(orders) == 0 {
		return false
	}
	for _, ord := range orders {
		if statemachine.StringToStatus(ord.Status) != orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			return false
		}
	}
	return true
}

func nextSettlementRefundedVND(ord db.Order, delta, newRefunded int64, isPostSettlement *bool) int64 {
	next := ord.SettlementRefundedVND
	if ord.Status == "COMPLETED" && isPostSettlement != nil && !*isPostSettlement && delta > 0 {
		next += delta
	}
	if next > newRefunded {
		next = newRefunded
	}
	if next < 0 {
		next = 0
	}
	return next
}

// resolveNonDowngradingGroupPaymentStatus chooses the checkout_group payment_status without ever
// downgrading a terminal/settled status or prematurely marking a multi-shop group SUCCEEDED/REFUNDED
// while any live sibling sub-order is still unpaid (N8).
func resolveNonDowngradingGroupPaymentStatus(curGroupStatus, requestedStatus string, orders []db.Order) string {
	if !isPendingOrEmptyPaymentStatus(curGroupStatus) {
		return curGroupStatus
	}
	if len(orders) == 0 {
		return requestedStatus
	}
	if shouldMarkGroupRefunded(orders, "", "", "") {
		return "REFUNDED"
	}
	allCancelled := true
	allActivePaid := true
	hasActive := false
	for _, o := range orders {
		st := statemachine.StringToStatus(o.Status)
		if st == orderv1.OrderStatus_ORDER_STATUS_CANCELLED {
			continue
		}
		allCancelled = false
		if st == orderv1.OrderStatus_ORDER_STATUS_RETURNED {
			continue
		}
		hasActive = true
		if !strings.EqualFold(o.PaymentStatus, "SUCCEEDED") && !strings.EqualFold(o.PaymentStatus, "REFUNDED") {
			allActivePaid = false
		}
	}
	if allCancelled {
		return ResolveAllCancelledPaymentStatus(orders)
	}
	if hasActive && allActivePaid {
		return "SUCCEEDED"
	}
	return requestedStatus
}

// isOrderPaymentTerminalProtected reports whether an order's payment_status is already settled
// (SUCCEEDED or REFUNDED) and must never be overwritten with FAILED.
func isOrderPaymentTerminalProtected(ord db.Order) bool {
	return strings.EqualFold(ord.PaymentStatus, "SUCCEEDED") || strings.EqualFold(ord.PaymentStatus, "REFUNDED")
}

// canCancelGroupOrder reports whether an order inside a checkout group is eligible for group-level
// cancellation (CreatePayment failure or orphan PENDING recovery). Never cancels already-paid orders
// (SUCCEEDED/REFUNDED with positive payable), orders with a tracking code or pending shipment creation,
// or orders that cannot legally transition to CANCELLED (N2, N3).
func canCancelGroupOrder(ord db.Order, onlyPending bool) bool {
	cur := statemachine.StringToStatus(ord.Status)
	if onlyPending && (cur != orderv1.OrderStatus_ORDER_STATUS_PENDING || strings.TrimSpace(ord.PaymentID) != "") {
		return false
	}
	if statemachine.ValidateTransition(cur, orderv1.OrderStatus_ORDER_STATUS_CANCELLED) != nil {
		return false
	}
	if isOrderPaymentTerminalProtected(ord) && ord.TotalPayableVND > 0 {
		return false
	}
	if strings.TrimSpace(ord.TrackingCode) != "" || ord.PendingShipmentCancel || IsOrderShipmentQueued(ord) {
		return false
	}
	return true
}

// RetryOrderFeeSpec specifies the recalculated seller-borne payment fee for one sub-order on retry/switch (F5).
type RetryOrderFeeSpec struct {
	OrderID       string
	PaymentFeeVND int64
	PaymentFeeBPS int32
}

// RetryPaymentUpdateParams atomically updates payment details (and optionally switches payment method,
// recalculates seller-borne payment fee, and confirms orders when switching to COD) on a pending group (F5).
type RetryPaymentUpdateParams struct {
	CheckoutGroupID     string
	ExpectedPaymentID   string
	TargetMethod        string // Optional: empty preserves each order's current online method.
	NewPaymentID        string
	PaymentStatus       string
	QRURL               string
	QRCode              string
	PayURL              string
	PaymentExpiresAt    *time.Time
	SwitchToCOD         bool
	OrderFees           []RetryOrderFeeSpec // Optional: omitted keeps existing payment_fee_vnd/bps.
	ActorID             string
	ActorRole           string
	TraceID             string
	Traceparent         string
	RetryIdempotencyKey string
	Now                 time.Time
}

// CancelGroupOrdersParams cancels eligible orders in a checkout group (e.g. CreatePayment failure compensation).
type CancelGroupOrdersParams struct {
	CheckoutGroupID string
	Reason          string
	ActorID         string
	ActorRole       string
	PaymentStatus   string
	OnlyPending     bool // When true, only orders currently in PENDING status may be cancelled (N3).
	TraceID         string
	Traceparent     string
}

// CancelGroupOrdersResult reports which orders were cancelled and whether any live orders remain in the group.
type CancelGroupOrdersResult struct {
	Cancelled     []db.Order
	HasLiveOrders bool
}

// OutboxBuilder creates an outbox event payload for a given transitioned order.
type OutboxSpec struct {
	ID          string
	EventType   string
	TraceID     string
	Traceparent string
	Payload     any
}

// PaymentResultTransitionParams drives atomic payment.succeeded / failed / expired handling.
type PaymentResultTransitionParams struct {
	EventID               string // Optional NATS event_id for processed_events dedup in same TX.
	EventType             string
	SkipMarkProcessed     bool // When true, caller calls MarkEventProcessed after downstream calls succeed.
	CheckoutGroupID       string
	OrderID               string
	PaymentID             string
	ResultStatus          orderv1.PaymentResultStatus
	PaymentStatusOverride string // Optional override (e.g. "REFUNDED" when expiry watchdog sees already-refunded payment).
	Reason                string
	ActorID               string
	ActorRole             string
	TraceID               string
	Traceparent           string
	Now                   time.Time
}

// PaymentResultTransitionOutcome reports what changed in TransitionOrdersPaymentResult.
type PaymentResultTransitionOutcome struct {
	AlreadyProcessed     bool
	CheckoutGroupID      string
	Transitioned         []db.Order
	NeedsDownstream      bool       // True when orders transitioned OR matched an uncommitted redelivery requiring downstream completion.
	SupersededPaidOrders []db.Order // Populated when a late payment.succeeded arrives for a payment just superseded by RetryPayment (F5 race guard).
}

// OrderTransitionResult reports the committed aggregate, its locked pre-transition snapshot,
// all orders transitioned in the TX, and whether active siblings remain in the checkout group.
type OrderTransitionResult struct {
	Order            *OrderAggregate
	PrevOrder        db.Order
	Transitioned     []db.Order
	HasActiveSibling bool
}

// SingleOrderTransitionParams drives a generic single-order transition (e.g. Cancel, SellerConfirm).
type SingleOrderTransitionParams struct {
	OrderID                    string
	ExpectedStatus             orderv1.OrderStatus // Optional: locked order must match this status before transitioning.
	RequireConfirmedBefore     *time.Time          // Optional: COALESCE(confirmed_at, updated_at) must be <= cutoff.
	TargetStatus               orderv1.OrderStatus
	CancelReason               string
	PaymentStatus              string
	ActorID                    string
	ActorRole                  string
	Note                       string
	CancelGroupPend            bool       // When cancelling PENDING order, cancel sibling PENDING orders in group too.
	RequireNoTracking          bool       // If true and locked order already has tracking_code or pending_shipment_cancel, return ErrOrderHasTracking.
	VerifyShipmentCancel       bool       // P0-1: When cancelling, always verify tracking_code matches CancelledTrackingCode and in-flight pending_shipment_cancel was not NotFound.
	CancelledTrackingCode      string     // Tracking code actually cancelled by Shipping.CancelShipment (empty if NotFound or not called).
	KnownTrackingBeforeCancel  string     // Tracking code observed on the order before calling Shipping.CancelShipment.
	KnownShipmentQueued        bool       // True when shipment_job_status='QUEUED' was observed before calling Shipping.CancelShipment (E18).
	KnownQueuedUpdatedAt       *time.Time // UpdatedAt observed while QUEUED before calling Shipping.CancelShipment; detects re-queued jobs between steps.
	LastCancelNotFound         bool       // True when Shipping.CancelShipment returned NotFound (shipment may still be in flight).
	ClearPendingShipmentCancel bool       // If true, clear pending_shipment_cancel flag in the same transaction.
	OutboxEvents               []db.InsertOutboxEventParams
	BuildOutboxFn              func(order db.Order) []db.InsertOutboxEventParams
	Now                        time.Time
}

// ShipOrderParams drives SellerShipOrder transition CONFIRMED|PREPARING -> PREPARING with tracking.
type ShipOrderParams struct {
	OrderID               string
	CarrierCode           string
	ServiceCode           string
	TrackingCode          string
	LabelURL              string
	ExpectedCreatingToken string // Token returned by MarkOrderShipmentCreating; prevents stale/expired attempts from overwriting.
	ActorID               string
	ActorRole             string
	Note                  string
	Now                   time.Time
}

// CompleteOrderParams drives DELIVERED -> COMPLETED (BuyerConfirmReceived or 7d auto-complete job).
type CompleteOrderParams struct {
	OrderID                string
	ActorID                string
	ActorRole              string
	Note                   string
	RequireNoReturnRequest bool // When true (7d auto-complete), reject if a return request exists.
	OutboxEvent            db.InsertOutboxEventParams
	Now                    time.Time
}

// ReturnRequestParams drives RequestReturn transition DELIVERED -> RETURN_REQUESTED.
type ReturnRequestParams struct {
	OrderID          string
	OrderCode        string
	BuyerID          string
	ShopID           string
	ReturnRequestID  string
	Reason           string
	EvidenceURLsJSON []byte
	RefundAmountVND  int64
	DeadlineAt       *time.Time
	ActorID          string
	ActorRole        string
	Note             string
	OutboxEvent      db.InsertOutboxEventParams
	Now              time.Time
}

// ReturnTransitionParams drives an atomic return_requests transition under FOR UPDATE.
type ReturnTransitionParams struct {
	ReturnRequestID             string
	ExpectedStatus              orderv1.ReturnStatus
	TargetStatus                orderv1.ReturnStatus
	RequireDeadlineExpired      bool
	RequireNotGoodsRecv         bool
	RequireNoTracking           bool
	RequireHasTracking          bool
	ActorID                     string
	ActorRole                   string
	Note                        string
	OutboxReason                string
	EvidenceURLsJSON            []byte
	SellerResponse              *string
	SellerEvidenceURLsJSON      []byte
	DisputeReason               *string
	AdminNote                   *string
	ReturnFeeBearer             *string
	GoodsReceived               *bool
	ShouldRestock               *bool
	ReturnShipmentTerminal      *bool
	EscalatedFromTransitTimeout *bool
	EscalatedFromCancelFailure  *bool
	RequireApprovalIfLarge      *bool
	RefundRequestedBy           *string
	RefundID                    *string
	RefundApprovalStatus        *string
	IncrementRefundAttempt      bool
	UpdateDeadline              bool
	DeadlineAt                  *time.Time
	BuildCompletedOutboxFn      func(order db.Order) (db.InsertOutboxEventParams, error)
	TraceID                     string
	Traceparent                 string
	Now                         time.Time
}

// ReturnTransitionResult reports the committed return aggregate and associated order aggregate.
type ReturnTransitionResult struct {
	Return     *ReturnAggregate
	Order      *OrderAggregate
	PrevStatus orderv1.ReturnStatus
}

// SaveReturnShipmentParams attaches tracking/label/fee and 5d deadline to an AWAITING_SHIPMENT return.
type SaveReturnShipmentParams struct {
	ReturnRequestID string
	TrackingCode    string
	LabelURL        string
	FeeVND          int64
	DeadlineAt      *time.Time
	TraceID         string
	Traceparent     string
	Now             time.Time
}

// ReturnShipmentUpdateParams drives idempotent shipment.updated (direction=RETURN) consumption.
type ReturnShipmentUpdateParams struct {
	EventID         string
	EventType       string
	OrderID         string
	ReturnRequestID string
	ShipmentStatus  string
	RawStatus       string
	TrackingCode    string
	Description     string
	InspectDuration time.Duration
	TransitDuration time.Duration
	TraceID         string
	Traceparent     string
	Now             time.Time
}

// ReturnShipmentUpdateOutcome reports the effect of a RETURN shipment.updated event.
type ReturnShipmentUpdateOutcome struct {
	AlreadyProcessed bool
	NotFound         bool
	Transitioned     bool
	Return           *ReturnAggregate
}

// BuyerCODStats aggregates terminal COD delivery outcomes for a buyer (E19).
type BuyerCODStats struct {
	TotalTerminal int64
	RejectedCount int64
}

// QueueShipmentParams marks an order's async shipment creation job as QUEUED without advancing status (E18).
type QueueShipmentParams struct {
	OrderID               string
	JobID                 string
	ExpectedCreatingToken string
	ActorID               string
	ActorRole             string
	Note                  string
	Now                   time.Time
}

// QueueShipmentOutcome reports the order state after queueing an async shipment creation job (E18).
type QueueShipmentOutcome struct {
	Order           *OrderAggregate
	AlreadyAttached bool // True if shipment.updated(status="created") already attached tracking_code before RPC returned.
}

// ShipmentCreateFailedParams drives idempotent shipment.create_failed consumption (E18).
type ShipmentCreateFailedParams struct {
	EventID      string
	EventType    string
	JobID        string
	OrderID      string
	CarrierCode  string
	ServiceCode  string
	Attempts     int32
	ErrorMessage string
	Now          time.Time
}

// ShipmentCreateFailedOutcome reports the effect of a shipment.create_failed event.
type ShipmentCreateFailedOutcome struct {
	AlreadyProcessed bool
	NotFound         bool
	Updated          bool
	Order            *OrderAggregate
}

// ShipmentUpdateParams drives idempotent shipment.updated event consumption.
type ShipmentUpdateParams struct {
	EventID           string
	EventType         string
	SkipMarkProcessed bool // When true, caller calls MarkEventProcessed after downstream compensations succeed.
	OrderID           string
	ShipmentStatus    string
	CarrierCode       string
	ServiceCode       string
	TrackingCode      string
	LabelURL          string
	Description       string
	TraceID           string
	Traceparent       string
	Now               time.Time
}

// ShipmentUpdateOutcome reports the effect of applying a shipment.updated event.
type ShipmentUpdateOutcome struct {
	AlreadyProcessed          bool
	AlreadyReturned           bool // Redelivery where order was already RETURNED before event was marked processed.
	NeedsOrphanShipmentCancel bool // Async shipment.updated(status="created") arrived after order was already CANCELLED (E18).
	NotFound                  bool
	EmittedOutbox             bool
	TransitionedFrom          orderv1.OrderStatus
	TransitionedTo            orderv1.OrderStatus
	Order                     *OrderAggregate
}

// PaymentRefundedParams drives idempotent payment.refunded event consumption.
type PaymentRefundedParams struct {
	EventID          string
	EventType        string
	PaymentID        string
	OrderID          string
	RefundID         string
	IdempotencyKey   string
	AmountVND        int64
	Reason           string
	IsPostSettlement *bool
}

// ExpiredGroupCandidate identifies an expired PENDING VietQR checkout group.
type ExpiredGroupCandidate struct {
	CheckoutGroupID string
	PaymentID       string
}

// Store defines the persistence contract for OrderService (Postgres in prod, Memory in unit tests).
type Store interface {
	GetCheckoutGroupForIdempotency(ctx context.Context, buyerID, idempotencyKey string) (*CheckoutGroupLookup, error)
	GetCheckoutGroupByID(ctx context.Context, id string) (*db.CheckoutGroup, error)
	GetCheckoutGroupByPaymentID(ctx context.Context, paymentID string) (*db.CheckoutGroup, error)
	CreateCheckoutGroup(ctx context.Context, arg db.CreateCheckoutGroupParams) (*db.CheckoutGroup, error)
	FailCheckoutGroup(ctx context.Context, id, paymentStatus string) error
	MarkCheckoutGroupNeedsCleanup(ctx context.Context, id, reason string) error
	ClaimStaleCheckoutGroupForCleanup(ctx context.Context, id, reason string) (bool, error)
	ClearCheckoutGroupNeedsCleanup(ctx context.Context, id string) error

	PersistCheckoutBatch(ctx context.Context, batch CheckoutBatch) ([]OrderAggregate, error)
	InspectCheckoutGroupOrdersLocked(ctx context.Context, checkoutGroupID string) (*db.CheckoutGroup, []OrderAggregate, error)
	CompleteCheckoutWithPayment(ctx context.Context, arg UpdateCheckoutPaymentParams) error
	WithCheckoutGroupRetryLock(ctx context.Context, checkoutGroupID string, fn func() error) error
	UpdateGroupPaymentOnRetry(ctx context.Context, params RetryPaymentUpdateParams) error
	CancelCheckoutGroupOrders(ctx context.Context, params CancelGroupOrdersParams) (*CancelGroupOrdersResult, error)

	GetOrderWithDetails(ctx context.Context, orderID string) (*OrderAggregate, error)
	ListOrdersByCheckoutGroup(ctx context.Context, checkoutGroupID string) ([]OrderAggregate, error)
	ListOrdersFiltered(ctx context.Context, buyerID, shopID, status string, limit, offset int32) ([]OrderAggregate, int64, error)
	GetOrderTimeline(ctx context.Context, orderID string) ([]db.OrderTimelineEvent, error)

	TransitionOrdersPaymentResult(ctx context.Context, params PaymentResultTransitionParams) (*PaymentResultTransitionOutcome, error)
	TransitionOrderStatus(ctx context.Context, params SingleOrderTransitionParams) (*OrderTransitionResult, error)
	MarkOrderShipmentCreating(ctx context.Context, orderID string, now time.Time) (string, error)
	ClearOrderPendingShipmentCancel(ctx context.Context, orderID string) error
	ClearOrderPendingShipmentCancelByToken(ctx context.Context, orderID, token string) error
	ClearStaleOrderPendingShipmentCancel(ctx context.Context, orderID, expectedToken string, cutoff time.Time) (bool, error)
	MarkOrderOrphanShipmentManualReview(ctx context.Context, orderID, note string) error
	ClearCheckoutGroupPendingStockConfirm(ctx context.Context, checkoutGroupID string) error
	MarkOrderPendingCancelCompensation(ctx context.Context, orderID string) error
	ClearOrderPendingCancelCompensation(ctx context.Context, orderID string) error
	ClearOrderPendingReturnCompensation(ctx context.Context, orderID string) error
	TransitionOrderPreparedForShipment(ctx context.Context, params ShipOrderParams) (*OrderAggregate, error)
	MarkOrderShipmentQueued(ctx context.Context, params QueueShipmentParams) (*QueueShipmentOutcome, error)
	MarkOrderShipmentJobFailedIfQueued(ctx context.Context, orderID string) (bool, error)
	TransitionOrderCompleted(ctx context.Context, params CompleteOrderParams) (*OrderAggregate, error)
	TransitionOrderReturnRequested(ctx context.Context, params ReturnRequestParams) (*OrderAggregate, *ReturnAggregate, error)
	ApplyShipmentUpdate(ctx context.Context, params ShipmentUpdateParams) (*ShipmentUpdateOutcome, error)
	ApplyShipmentCreateFailed(ctx context.Context, params ShipmentCreateFailedParams) (*ShipmentCreateFailedOutcome, error)
	ApplyReturnShipmentUpdate(ctx context.Context, params ReturnShipmentUpdateParams) (*ReturnShipmentUpdateOutcome, error)
	RecordPaymentRefunded(ctx context.Context, params PaymentRefundedParams) (bool, error)
	GetBuyerTerminalCODStats(ctx context.Context, buyerID string) (BuyerCODStats, error)

	GetReturnRequest(ctx context.Context, returnRequestID, orderID string) (*ReturnAggregate, error)
	ListReturnRequestsFiltered(ctx context.Context, buyerID, shopID, status string, limit, offset int32) ([]ReturnAggregate, int64, error)
	TransitionReturnRequest(ctx context.Context, params ReturnTransitionParams) (*ReturnTransitionResult, error)
	ExtendReturnDeadline(ctx context.Context, returnRequestID string, expectedStatus orderv1.ReturnStatus, newDeadline, now time.Time, maxExtensions int32) (bool, error)
	SaveReturnShipmentInfo(ctx context.Context, params SaveReturnShipmentParams) (*ReturnAggregate, bool, error)
	UpdateReturnSideEffectProgress(ctx context.Context, returnRequestID string, refundDone, promoReleased, restockDone bool, refundAmountVND *int64) (*db.ReturnRequest, error)
	UpdateReturnRefundApprovalState(ctx context.Context, returnRequestID, refundID, approvalStatus string, refundDone bool, refundAmountVND *int64) (*db.ReturnRequest, error)

	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID, eventType string) error
	InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error
	ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error)
	FindExpiredPendingGroups(ctx context.Context, limit int32) ([]ExpiredGroupCandidate, error)
	FindStaleInProgressCheckoutGroups(ctx context.Context, olderThan time.Duration, limit int32) ([]string, error)
	FindIncompleteCheckoutGroupsWithOrders(ctx context.Context, olderThan time.Duration, limit int32) ([]string, error)
	FindConfirmedOrdersForAutoCancel(ctx context.Context, cutoff time.Time, limit int32) ([]string, error)
	FindDeliveredOrdersForAutoComplete(ctx context.Context, cutoff time.Time, limit int32) ([]string, error)
	FindExpiredReturnRequests(ctx context.Context, now time.Time, limit int32) ([]string, error)
	FindAwaitingShipmentReturnsWithoutTracking(ctx context.Context, limit int32) ([]string, error)
	FindRefundingReturnRequests(ctx context.Context, limit int32) ([]string, error)
	FindCancelledOrdersPendingShipmentCancel(ctx context.Context, limit int32) ([]string, error)
	FindActiveOrdersWithStalePendingShipmentCancel(ctx context.Context, cutoff time.Time, limit int32) ([]string, error)
	FindCheckoutGroupsPendingStockConfirm(ctx context.Context, limit int32) ([]string, error)
	FindCancelledOrdersPendingCompensation(ctx context.Context, limit int32) ([]string, error)
	FindReturnedOrdersPendingCompensation(ctx context.Context, limit int32) ([]string, error)

	ListOrdersNeedingManualReview(ctx context.Context, limit, offset int32) ([]OrderAggregate, int64, error)
	ResolveOrderManualReview(ctx context.Context, orderID, actorID, actorRole, note string, now time.Time) (*OrderAggregate, error)
	QueryOrderReport(ctx context.Context, params OrderReportParams) (*OrderReportData, error)

	Ping(ctx context.Context) error
}

// MarshalJSONBytes marshals v into JSON bytes cleanly.
func MarshalJSONBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
