package events

import (
	"encoding/json"
	"time"
)

// Event subject and stream constants matching m-all-contract.md and docs/contracts.md.
const (
	SubjectProductCreated      = "product.created"
	SubjectProductUpdated      = "product.updated"
	SubjectProductDeleted      = "product.deleted"
	SubjectProductStockChanged = "product.stock_changed"

	DefaultStreamName = "NEXUSMART"
	DLQStreamName     = "NEXUSMART_DLQ"
)

// Envelope is the canonical JSON message wrapper for all NEXUSMART NATS JetStream events.
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ProductEventPayload is the canonical payload for product.* events.
type ProductEventPayload struct {
	ProductID  string `json:"product_id"`
	ShopID     string `json:"shop_id"`
	Version    int64  `json:"version"`
	OccurredAt string `json:"occurred_at"`
}
