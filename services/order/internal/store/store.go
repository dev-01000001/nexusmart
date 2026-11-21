package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	orderv1 "github.com/nexusmart/core/gen/go/nexusmart/order/v1"
	"github.com/nexusmart/core/services/order/internal/db"
)

var (
	ErrNotFound                = errors.New("record not found")
	ErrDuplicateIdempotencyKey = errors.New("duplicate active idempotency key")
	ErrAlreadyProcessed        = errors.New("event already processed")
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
	EventID         string // Optional NATS event_id for processed_events dedup in same TX.
	EventType       string
	CheckoutGroupID string
	OrderID         string
	PaymentID       string
	ResultStatus    orderv1.PaymentResultStatus
	Reason          string
	ActorID         string
	ActorRole       string
	TraceID         string
	Traceparent     string
}

// PaymentResultTransitionOutcome reports what changed in TransitionOrdersPaymentResult.
type PaymentResultTransitionOutcome struct {
	AlreadyProcessed bool
	CheckoutGroupID  string
	Transitioned     []db.Order
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
	EventID        string
	EventType      string
	OrderID        string
	ShipmentStatus string
	TrackingCode   string
	Description    string
	TraceID        string
	Traceparent    string
}

// ShipmentUpdateOutcome reports the effect of applying a shipment.updated event.
type ShipmentUpdateOutcome struct {
	AlreadyProcessed bool
	NotFound         bool
	TransitionedTo   orderv1.OrderStatus
	Order            *OrderAggregate
}

// PaymentRefundedParams drives idempotent payment.refunded event consumption.
type PaymentRefundedParams struct {
	EventID   string
	EventType string
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
	CreateCheckoutGroup(ctx context.Context, arg db.CreateCheckoutGroupParams) (*db.CheckoutGroup, error)
	FailCheckoutGroup(ctx context.Context, id, paymentStatus string) error

	PersistCheckoutBatch(ctx context.Context, batch CheckoutBatch) ([]OrderAggregate, error)
	CompleteCheckoutWithPayment(ctx context.Context, arg UpdateCheckoutPaymentParams) error
	CancelCheckoutGroupOrders(ctx context.Context, params CancelGroupOrdersParams) ([]db.Order, error)

	GetOrderWithDetails(ctx context.Context, orderID string) (*OrderAggregate, error)
	ListOrdersByCheckoutGroup(ctx context.Context, checkoutGroupID string) ([]OrderAggregate, error)
	ListOrdersFiltered(ctx context.Context, buyerID, shopID, status, salesChannel, storefrontShopID string, limit, offset int32) ([]OrderAggregate, int64, error)
	GetOrderTimeline(ctx context.Context, orderID string) ([]db.OrderTimelineEvent, error)

	TransitionOrdersPaymentResult(ctx context.Context, params PaymentResultTransitionParams) (*PaymentResultTransitionOutcome, error)
	TransitionOrderStatus(ctx context.Context, params SingleOrderTransitionParams) (*OrderAggregate, []db.Order, error)
	TransitionOrderShipped(ctx context.Context, params ShipOrderParams) (*OrderAggregate, error)
	TransitionOrderCompleted(ctx context.Context, params CompleteOrderParams) (*OrderAggregate, error)
	TransitionOrderReturnRequested(ctx context.Context, params ReturnRequestParams) (*OrderAggregate, *db.ReturnRequest, error)
	ApplyShipmentUpdate(ctx context.Context, params ShipmentUpdateParams) (*ShipmentUpdateOutcome, error)
	RecordPaymentRefunded(ctx context.Context, params PaymentRefundedParams) (bool, error)

	IsEventProcessed(ctx context.Context, eventID string) (bool, error)
	InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error
	ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev db.OutboxEvent) error) (int, error)
	FindExpiredPendingGroups(ctx context.Context, limit int32) ([]ExpiredGroupCandidate, error)

	Ping(ctx context.Context) error
}

// MarshalJSONBytes marshals v into JSON bytes cleanly.
func MarshalJSONBytes(v any) ([]byte, error) {
	return json.Marshal(v)
}
