package indexer

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	SubjectProductCreated      = "product.created"
	SubjectProductUpdated      = "product.updated"
	SubjectProductDeleted      = "product.deleted"
	SubjectProductStockChanged = "product.stock_changed"
)

var (
	// ErrMalformedPayload indicates a poison event payload that must be routed directly to DLQ.
	ErrMalformedPayload = errors.New("malformed event payload")
	// ErrProductNotFound indicates the product does not exist or is not publicly searchable.
	ErrProductNotFound = errors.New("product not found")
)

// Envelope is the canonical JSON event envelope on NATS JetStream stream NEXUSMART.
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

// CategoryMeta holds cached category display metadata.
type CategoryMeta struct {
	ID   string
	Slug string
	Name string
}
