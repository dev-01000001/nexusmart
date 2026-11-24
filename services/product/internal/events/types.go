package events

import (
	"encoding/json"
	"time"

	"github.com/nexusmart/core/pkg/common/jetstream"
)

const (
	// SubjectProductReviewed matches docs/contracts.md Section 5.
	SubjectProductReviewed = "product.reviewed"

	// SubjectReviewCreated matches docs/contracts.md Section 5 (E6).
	SubjectReviewCreated = "review.created"

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

// ReviewCreatedPayload matches docs/contracts.md Section 5 for review.created (E6).
type ReviewCreatedPayload struct {
	ReviewID      string `json:"review_id"`
	ProductID     string `json:"product_id"`
	ShopID        string `json:"shop_id"`
	OrderID       string `json:"order_id"`
	OrderItemID   string `json:"order_item_id"`
	BuyerID       string `json:"buyer_id"`
	Rating        int32  `json:"rating"`
	ReviewCount   int32  `json:"review_count"`
	RatingAvgX100 int32  `json:"rating_avg_x100"`
	OccurredAt    string `json:"occurred_at"`
}
