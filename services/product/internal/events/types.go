package events

import (
	"encoding/json"
	"time"

	"github.com/nexusmart/core/pkg/common/jetstream"
)

const (
	// SubjectProductReviewed matches docs/contracts.md Section 5.
	SubjectProductReviewed = "product.reviewed"

	// DLQStreamName is the canonical NexusMart dead-letter stream name.
	DLQStreamName = jetstream.DefaultDLQStream
)

// Envelope is the canonical JSON message wrapper for all NEXUSMART NATS JetStream events.
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ProductReviewedPayload matches docs/contracts.md Section 5 for product.reviewed.
type ProductReviewedPayload struct {
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	ShopID      string `json:"shop_id"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
	OccurredAt  string `json:"occurred_at"`
}
