package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
	"github.com/nexusmart/core/services/order/internal/statemachine"
)

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

var (
	ErrNotFound                = errors.New("record not found")
	ErrDuplicateIdempotencyKey = errors.New("duplicate active idempotency key")
	ErrAlreadyProcessed        = errors.New("event already processed")
	ErrRetryPrecondition       = errors.New("order group is not eligible for payment retry")
)

// OrderAggregate groups a persisted order with its line items and timeline history.
type OrderAggregate struct {
	Order    db.Order
	Items    []db.OrderItem
	Timeline []db.OrderTimelineEvent
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
	CheckoutGroupID string
	Orders          []NewOrderRecord
}

// UpdateCheckoutPaymentParams updates payment details on a checkout group and all its sub-orders.
type UpdateCheckoutPaymentParams struct {
	CheckoutGroupID      string
	PaymentID            string
	PaymentStatus        string
	QRURL                string
	QRCode               string
	PaymentExpiresAt     *time.Time
	TotalPayableVND      int64
	ResponseSnapshotJSON []byte
}

// RetryPaymentUpdateParams atomically updates payment_id and payment_expires_at on a pending group.
type RetryPaymentUpdateParams struct {
	CheckoutGroupID     string
	ExpectedPaymentID   string
	NewPaymentID        string
	PaymentStatus       string
	QRURL               string
	QRCode              string
	PaymentExpiresAt    *time.Time
	RetryIdempotencyKey string
	Now                 time.Time
}

// CancelGroupOrdersParams cancels all orders in a checkout group (e.g. CreatePayment failure compensation).
type CancelGroupOrdersParams struct {
	CheckoutGroupID string
	Reason          string
	ActorID         string
	ActorRole       string
	PaymentStatus   string
	TraceID         string
	Traceparent     string
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
}

// PaymentResultTransitionOutcome reports what changed in TransitionOrdersPaymentResult.
type PaymentResultTransitionOutcome struct {
	AlreadyProcessed bool
	CheckoutGroupID  string
	Transitioned     []db.Order
	NeedsDownstream  bool // True when orders transitioned OR matched an uncommitted redelivery requiring downstream completion.
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
	OrderID         string
	TargetStatus    orderv1.OrderStatus
	CancelReason    string
	PaymentStatus   string
	ActorID         string
	ActorRole       string
	Note            string
	CancelGroupPend bool // When cancelling PENDING order, cancel sibling PENDING orders in group too.
	OutboxEvents    []db.InsertOutboxEventParams
	BuildOutboxFn   func(order db.Order) []db.InsertOutboxEventParams
}

// ShipOrderParams drives SellerShipOrder transition -> SHIPPING.
type ShipOrderParams struct {
	OrderID      string
	CarrierCode  string
	ServiceCode  string
	TrackingCode string
	LabelURL     string
	ActorID      string
	ActorRole    string
	Note         string
	OutboxEvent  db.InsertOutboxEventParams
}

// CompleteOrderParams drives BuyerConfirmReceived transition DELIVERED -> COMPLETED.
type CompleteOrderParams struct {
	OrderID     string
	ActorID     string
	ActorRole   string
	Note        string
	OutboxEvent db.InsertOutboxEventParams
}

// ReturnRequestParams drives RequestReturn transition DELIVERED -> RETURN_REQUESTED.
type ReturnRequestParams struct {
	OrderID          string
	BuyerID          string
	ReturnRequestID  string
	Reason           string
	EvidenceURLsJSON []byte
	ActorID          string
	ActorRole        string
	Note             string
}

// ShipmentUpdateParams drives idempotent shipment.updated event consumption.
type ShipmentUpdateParams struct {
	EventID           string
	EventType         string
	SkipMarkProcessed bool // When true, caller calls MarkEventProcessed after downstream compensations succeed.
	OrderID           string
	ShipmentStatus    string
	TrackingCode      string
	Description       string
	TraceID           string
	Traceparent       string
}

// ShipmentUpdateOutcome reports the effect of applying a shipment.updated event.
type ShipmentUpdateOutcome struct {
	AlreadyProcessed bool
	AlreadyReturned  bool // Redelivery where order was already RETURNED before event was marked processed.
	NotFound         bool
	TransitionedFrom orderv1.OrderStatus
	TransitionedTo   orderv1.OrderStatus
	Order            *OrderAggregate
}

// PaymentRefundedParams drives idempotent payment.refunded event consumption.
type PaymentRefundedParams struct {
	EventID   string
	EventType string
	PaymentID string
	OrderID   string
	RefundID  string
	AmountVND int64
	Reason    string
}

// ExpiredGroupCandidate identifies an expired PENDING VietQR checkout group.
type ExpiredGroupCandidate struct {
	CheckoutGroupID string
	PaymentID       string
}

// Store defines the persistence contract for OrderService (Postgres in prod, Memory in unit tests).
type Store interface {
	GetActiveCheckoutGroup(ctx context.Context, buyerID, idempotencyKey string) (*db.CheckoutGroup, error)
	GetCheckoutGroupByID(ctx context.Context, id string) (*db.CheckoutGroup, error)
	GetCheckoutGroupByPaymentID(ctx context.Context, paymentID string) (*db.CheckoutGroup, error)
	CreateCheckoutGroup(ctx context.Context, arg db.CreateCheckoutGroupParams) (*db.CheckoutGroup, error)
	FailCheckoutGroup(ctx context.Context, id, paymentStatus string) error

	PersistCheckoutBatch(ctx context.Context, batch CheckoutBatch) ([]OrderAggregate, error)
	CompleteCheckoutWithPayment(ctx context.Context, arg UpdateCheckoutPaymentParams) error
	WithCheckoutGroupRetryLock(ctx context.Context, checkoutGroupID string, fn func() error) error
	UpdateGroupPaymentOnRetry(ctx context.Context, params RetryPaymentUpdateParams) error
	CancelCheckoutGroupOrders(ctx context.Context, params CancelGroupOrdersParams) ([]db.Order, error)

	GetOrderWithDetails(ctx context.Context, orderID string) (*OrderAggregate, error)
	ListOrdersByCheckoutGroup(ctx context.Context, checkoutGroupID string) ([]OrderAggregate, error)
	ListOrdersFiltered(ctx context.Context, buyerID, shopID, status string, limit, offset int32) ([]OrderAggregate, int64, error)
	GetOrderTimeline(ctx context.Context, orderID string) ([]db.OrderTimelineEvent, error)

	TransitionOrdersPaymentResult(ctx context.Context, params PaymentResultTransitionParams) (*PaymentResultTransitionOutcome, error)
	TransitionOrderStatus(ctx context.Context, params SingleOrderTransitionParams) (*OrderTransitionResult, error)
	TransitionOrderShipped(ctx context.Context, params ShipOrderParams) (*OrderAggregate, error)
	TransitionOrderCompleted(ctx context.Context, params CompleteOrderParams) (*OrderAggregate, error)
	TransitionOrderReturnRequested(ctx context.Context, params ReturnRequestParams) (*OrderAggregate, *db.ReturnRequest, error)
	ApplyShipmentUpdate(ctx context.Context, params ShipmentUpdateParams) (*ShipmentUpdateOutcome, error)
	RecordPaymentRefunded(ctx context.Context, params PaymentRefundedParams) (bool, error)

	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	MarkEventProcessed(ctx context.Context, eventID, eventType string) error
	InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error
	ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error)
	FindExpiredPendingGroups(ctx context.Context, limit int32) ([]ExpiredGroupCandidate, error)
	FindStaleInProgressCheckoutGroups(ctx context.Context, olderThan time.Duration, limit int32) ([]string, error)

	Ping(ctx context.Context) error
}

// MarshalJSONBytes marshals v into JSON bytes cleanly.
func MarshalJSONBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}
