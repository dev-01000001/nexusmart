package events

import (
	"encoding/json"
	"time"
)

// Event subject constants matching docs/contracts.md Section 5.
const (
	SubjectOrderCreated         = "order.created"
	SubjectOrderConfirmed       = "order.confirmed"
	SubjectOrderCancelled       = "order.cancelled"
	SubjectOrderShipped         = "order.shipped"
	SubjectOrderDelivered       = "order.delivered"
	SubjectOrderCompleted       = "order.completed"
	SubjectOrderReturnRequested = "order.return_requested"
	SubjectOrderReturnApproved  = "order.return_approved"
	SubjectOrderRefunded        = "order.refunded"

	SubjectPaymentSucceeded = "payment.succeeded"
	SubjectPaymentFailed    = "payment.failed"
	SubjectPaymentExpired   = "payment.expired"
	SubjectPaymentRefunded  = "payment.refunded"

	SubjectShipmentUpdated = "shipment.updated"

	DLQStreamName = "NEXUSMART_DLQ"
)

// Envelope is the canonical JSON message wrapper for all NEXUSMART NATS JetStream events.
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// OrderCreatedPayload matches docs/contracts.md Section 5.
type OrderCreatedPayload struct {
	CheckoutGroupID  string `json:"checkout_group_id"`
	OrderID          string `json:"order_id"`
	OrderCode        string `json:"order_code"`
	BuyerID          string `json:"buyer_id"`
	ShopID           string `json:"shop_id"`
	StorefrontShopID string `json:"storefront_shop_id,omitempty"`
	PaymentMethod    string `json:"payment_method"`
	TotalPayableVND  int64  `json:"total_payable_vnd"`
}

// OrderConfirmedPayload matches docs/contracts.md Section 5.
type OrderConfirmedPayload struct {
	OrderID          string `json:"order_id"`
	OrderCode        string `json:"order_code"`
	BuyerID          string `json:"buyer_id"`
	ShopID           string `json:"shop_id"`
	StorefrontShopID string `json:"storefront_shop_id,omitempty"`
	PaymentMethod    string `json:"payment_method"`
	TotalPayableVND  int64  `json:"total_payable_vnd"`
}

// OrderCancelledPayload matches docs/contracts.md Section 5.
type OrderCancelledPayload struct {
	OrderID          string `json:"order_id"`
	OrderCode        string `json:"order_code"`
	BuyerID          string `json:"buyer_id"`
	ShopID           string `json:"shop_id"`
	StorefrontShopID string `json:"storefront_shop_id,omitempty"`
	Reason           string `json:"reason"`
	WasPaid          bool   `json:"was_paid"`
}

// OrderShippedPayload matches docs/contracts.md Section 5.
type OrderShippedPayload struct {
	OrderID          string `json:"order_id"`
	OrderCode        string `json:"order_code"`
	BuyerID          string `json:"buyer_id"`
	ShopID           string `json:"shop_id"`
	StorefrontShopID string `json:"storefront_shop_id,omitempty"`
	CarrierCode      string `json:"carrier_code"`
	TrackingCode     string `json:"tracking_code"`
}

// OrderDeliveredPayload matches docs/contracts.md Section 5.
type OrderDeliveredPayload struct {
	OrderID          string `json:"order_id"`
	OrderCode        string `json:"order_code"`
	BuyerID          string `json:"buyer_id"`
	ShopID           string `json:"shop_id"`
	StorefrontShopID string `json:"storefront_shop_id,omitempty"`
	TrackingCode     string `json:"tracking_code"`
}

// OrderCompletedPayload matches docs/contracts.md Section 5.
type OrderCompletedPayload struct {
	OrderID          string `json:"order_id"`
	OrderCode        string `json:"order_code"`
	BuyerID          string `json:"buyer_id"`
	ShopID           string `json:"shop_id"`
	StorefrontShopID string `json:"storefront_shop_id,omitempty"`
	SubtotalVND      int64  `json:"subtotal_vnd"`
	CommissionVND    int64  `json:"commission_vnd"`
	TotalPayableVND  int64  `json:"total_payable_vnd"`
	CoinsReward      int64  `json:"coins_reward"`
}

// OrderReturnRequestedPayload is emitted when a buyer requests a return on a delivered order.
type OrderReturnRequestedPayload struct {
	OrderID          string `json:"order_id"`
	OrderCode        string `json:"order_code"`
	BuyerID          string `json:"buyer_id"`
	ShopID           string `json:"shop_id"`
	StorefrontShopID string `json:"storefront_shop_id,omitempty"`
	ReturnRequestID  string `json:"return_request_id"`
	RefundAmountVND  int64  `json:"refund_amount_vnd"`
	Reason           string `json:"reason"`
}

// OrderReturnApprovedPayload is emitted when a seller or admin approves a return request.
type OrderReturnApprovedPayload struct {
	OrderID             string `json:"order_id"`
	OrderCode           string `json:"order_code"`
	BuyerID             string `json:"buyer_id"`
	ShopID              string `json:"shop_id"`
	StorefrontShopID    string `json:"storefront_shop_id,omitempty"`
	ReturnRequestID     string `json:"return_request_id"`
	ImmediateRefund     bool   `json:"immediate_refund"`
	ReverseTrackingCode string `json:"reverse_tracking_code,omitempty"`
	ReverseLabelURL     string `json:"reverse_label_url,omitempty"`
	RefundAmountVND     int64  `json:"refund_amount_vnd"`
}

// OrderRefundedPayload is emitted when a return refund saga completes and transitions an order to REFUNDED.
type OrderRefundedPayload struct {
	OrderID              string `json:"order_id"`
	OrderCode            string `json:"order_code"`
	CheckoutGroupID      string `json:"checkout_group_id"`
	BuyerID              string `json:"buyer_id"`
	ShopID               string `json:"shop_id"`
	StorefrontShopID     string `json:"storefront_shop_id,omitempty"`
	ReturnRequestID      string `json:"return_request_id"`
	RefundID             string `json:"refund_id"`
	RefundAmountVND      int64  `json:"refund_amount_vnd"`
	CoinsDiscountVND     int64  `json:"coins_discount_vnd"`
	AllGroupOrdersClosed bool   `json:"all_group_orders_closed"`
	Reason               string `json:"reason"`
}

// PaymentSucceededPayload matches docs/contracts.md Section 5.
type PaymentSucceededPayload struct {
	PaymentID    string `json:"payment_id"`
	OrderGroupID string `json:"order_group_id"`
	OrderID      string `json:"order_id"`
	UserID       string `json:"user_id"`
	AmountVND    int64  `json:"amount_vnd"`
	Method       string `json:"method"`
	ProviderTxID string `json:"provider_tx_id"`
}

// PaymentFailedPayload matches payment.failed and payment.expired events.
type PaymentFailedPayload struct {
	PaymentID    string `json:"payment_id"`
	OrderGroupID string `json:"order_group_id"`
	OrderID      string `json:"order_id"`
	UserID       string `json:"user_id"`
	Reason       string `json:"reason"`
}

// PaymentRefundedPayload matches payment.refunded events.
type PaymentRefundedPayload struct {
	RefundID  string `json:"refund_id"`
	PaymentID string `json:"payment_id"`
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"`
	AmountVND int64  `json:"amount_vnd"`
	Reason    string `json:"reason"`
}

// ShipmentUpdatedPayload matches shipment.updated events.
type ShipmentUpdatedPayload struct {
	ShipmentID      string `json:"shipment_id"`
	OrderID         string `json:"order_id"`
	ShopID          string `json:"shop_id"`
	CarrierCode     string `json:"carrier_code"`
	TrackingCode    string `json:"tracking_code"`
	Status          string `json:"status"`
	RawStatus       string `json:"raw_status"`
	Description     string `json:"description"`
	OccurredAt      string `json:"occurred_at"`
	Direction       string `json:"direction,omitempty"`
	ReturnRequestID string `json:"return_request_id,omitempty"`
}
