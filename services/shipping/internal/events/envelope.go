package events

import (
	"encoding/json"

	"github.com/nexusmart/core/pkg/common/jetstream"
)

const (
	SubjectShipmentUpdated = "shipment.updated"
	SubjectOrderCancelled  = "order.cancelled"
)

// Envelope is the canonical NexusMart NATS JetStream event wrapper (docs/contracts.md §5).
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ShipmentUpdatedPayload matches docs/contracts.md §5 for shipment.updated.
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
	Direction       string `json:"direction"`
	ReturnRequestID string `json:"return_request_id"`
	RedeliveryCount int32  `json:"redelivery_count"`
}

// OrderCancelledPayload matches docs/contracts.md §5 for order.cancelled.
type OrderCancelledPayload struct {
	OrderID   string `json:"order_id"`
	OrderCode string `json:"order_code"`
	BuyerID   string `json:"buyer_id"`
	ShopID    string `json:"shop_id"`
	Reason    string `json:"reason"`
	WasPaid   bool   `json:"was_paid"`
}

// EnsureStreams idempotently creates or union-updates the NEXUSMART and NEXUSMART_DLQ JetStream streams.
func EnsureStreams(js jetstream.StreamManager, primaryStream, dlqStream string) error {
	return jetstream.EnsureStreams(js, primaryStream, dlqStream)
}
