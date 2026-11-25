package events

import (
	"encoding/json"
	"time"

	"github.com/nexusmart/core/pkg/common/jetstream"
)

const (
	// SubjectProductCreated matches docs/contracts.md Section 5.
	SubjectProductCreated = "product.created"

	// SubjectProductUpdated matches docs/contracts.md Section 5.
	SubjectProductUpdated = "product.updated"

	// SubjectProductStockChanged matches docs/contracts.md Section 5.
	SubjectProductStockChanged = "product.stock_changed"

	// SubjectProductReviewed matches docs/contracts.md Section 5.
	SubjectProductReviewed = "product.reviewed"

	// SubjectProductUpserted matches docs/contracts.md Section 5 (B1/G9/G11).
	SubjectProductUpserted = "product.upserted"

	// SubjectProductDeleted matches docs/contracts.md Section 5 (B1/G11).
	SubjectProductDeleted = "product.deleted"

	// SubjectProductCategoryUpdated matches docs/contracts.md Section 5 (B1/G9).
	SubjectProductCategoryUpdated = "product.category_updated"

	// SubjectReviewCreated matches docs/contracts.md Section 5 (E6).
	SubjectReviewCreated = "review.created"

	// SubjectShopStatusChanged matches docs/contracts.md Section 5 / A7 §2.2 (F12).
	SubjectShopStatusChanged = "user.shop_status_changed"

	// DefaultStreamName is the canonical NexusMart primary JetStream stream name.
	DefaultStreamName = jetstream.DefaultPrimaryStream

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

// ProductEventPayload is the canonical payload for product.created, product.updated, and product.stock_changed.
type ProductEventPayload struct {
	ProductID  string `json:"product_id"`
	ShopID     string `json:"shop_id"`
	Version    int64  `json:"version"`
	OccurredAt string `json:"occurred_at"`
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
// Includes public review comment for downstream summarization and omits buyer_id/order_id PII.
type ReviewCreatedPayload struct {
	ReviewID      string `json:"review_id"`
	ProductID     string `json:"product_id"`
	ShopID        string `json:"shop_id"`
	OrderItemID   string `json:"order_item_id"`
	Rating        int32  `json:"rating"`
	Comment       string `json:"comment"`
	ReviewCount   int32  `json:"review_count"`
	RatingAvgX100 int32  `json:"rating_avg_x100"`
	OccurredAt    string `json:"occurred_at"`
}

// ShopStatusChangedPayload matches A7 §2.2 / F12 for user.shop_status_changed.
type ShopStatusChangedPayload struct {
	ShopID      string `json:"shop_id"`
	OwnerUserID string `json:"owner_user_id,omitempty"`
	ShopName    string `json:"shop_name,omitempty"`
	FromStatus  string `json:"from_status,omitempty"`
	ToStatus    string `json:"to_status"`
	Version     int64  `json:"version"`
	Reason      string `json:"reason,omitempty"`
	ActorID     string `json:"actor_id,omitempty"`
	ApproverID  string `json:"approver_id,omitempty"`
	OccurredAt  string `json:"occurred_at,omitempty"`
}

// ProductUpsertedPayload matches docs/contracts.md Section 5 for product.upserted (B1/G9/G11).
// Strictly omits commission_rate_bps and flagged_keywords.
type ProductUpsertedPayload struct {
	ProductID           string   `json:"product_id"`
	ShopID              string   `json:"shop_id"`
	CategoryID          string   `json:"category_id"`
	CategoryAncestorIDs []string `json:"category_ancestor_ids"`
	Name                string   `json:"name"`
	Slug                string   `json:"slug"`
	Description         string   `json:"description"`
	ThumbnailURL        string   `json:"thumbnail_url"`
	MinPriceVND         int64    `json:"min_price_vnd"`
	MaxPriceVND         int64    `json:"max_price_vnd"`
	Status              string   `json:"status"`
	SKUCodes            []string `json:"sku_codes"`
	SKUAttributesText   string   `json:"sku_attributes_text"`
	TotalStock          int32    `json:"total_stock"`
	SKUCount            int32    `json:"sku_count"`
	SKUIDs              []string `json:"sku_ids"`
	DefaultSKUID        string   `json:"default_sku_id"`
	Colors              []string `json:"colors"`
	ColorCodes          []string `json:"color_codes"`
	Sizes               []string `json:"sizes"`
	RatingAvgX100       int32    `json:"rating_avg_x100"`
	ReviewCount         int32    `json:"review_count"`
	ShopSuspended       bool     `json:"shop_suspended"`
	UpdatedAt           string   `json:"updated_at"`
}

// ProductDeletedPayload matches docs/contracts.md Section 5 for product.deleted (B1/G11),
// while also carrying monotonic version and occurred_at for legacy product.* consumers.
type ProductDeletedPayload struct {
	ProductID  string `json:"product_id"`
	ShopID     string `json:"shop_id"`
	Reason     string `json:"reason"`
	UpdatedAt  string `json:"updated_at"`
	Version    int64  `json:"version,omitempty"`
	OccurredAt string `json:"occurred_at,omitempty"`
}

// ProductCategoryUpdatedPayload matches docs/contracts.md Section 5 for product.category_updated (B1/G9).
type ProductCategoryUpdatedPayload struct {
	CategoryID string `json:"category_id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	ParentID   string `json:"parent_id"`
	SortOrder  int32  `json:"sort_order"`
	Action     string `json:"action"`
	UpdatedAt  string `json:"updated_at"`
}
