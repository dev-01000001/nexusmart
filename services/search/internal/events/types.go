package events

import (
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	SubjectProductUpserted        = "product.upserted"
	SubjectProductDeleted         = "product.deleted"
	SubjectProductCategoryUpdated = "product.category_updated"
	SubjectUserShopReviewed       = "user.shop_reviewed"
	SubjectUserShopStatusChanged  = "user.shop_status_changed"

	DefaultProductDurable = "search-product-events"
	DefaultProductQueue   = "search-product-workers"
	DefaultUserDurable    = "search-user-events"
	DefaultUserQueue      = "search-user-workers"
)

// Envelope matches docs/contracts.md §5 canonical NEXUSMART event wrapper.
type Envelope struct {
	EventID    string          `json:"event_id"`
	EventType  string          `json:"event_type"`
	TraceID    string          `json:"trace_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ProductUpsertedPayload matches docs/contracts.md §5 for product.upserted.
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

// ProductDeletedPayload matches docs/contracts.md §5 for product.deleted.
type ProductDeletedPayload struct {
	ProductID string `json:"product_id"`
	ShopID    string `json:"shop_id"`
	Reason    string `json:"reason"`
	UpdatedAt string `json:"updated_at"`
}

// ProductCategoryUpdatedPayload matches docs/contracts.md §5 for product.category_updated.
type ProductCategoryUpdatedPayload struct {
	CategoryID string `json:"category_id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	ParentID   string `json:"parent_id"`
	SortOrder  int32  `json:"sort_order"`
	Action     string `json:"action"`
	UpdatedAt  string `json:"updated_at"`
}

// ShopReviewedPayload matches docs/contracts.md §5 for user.shop_reviewed.
type ShopReviewedPayload struct {
	ShopID      string `json:"shop_id"`
	OwnerUserID string `json:"owner_user_id"`
	ShopName    string `json:"shop_name"`
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
}

// ShopStatusChangedPayload matches docs/contracts.md §5 for user.shop_status_changed (F12/G10).
type ShopStatusChangedPayload struct {
	ShopID      string `json:"shop_id"`
	OwnerUserID string `json:"owner_user_id"`
	ShopName    string `json:"shop_name"`
	FromStatus  string `json:"from_status"`
	ToStatus    string `json:"to_status"`
	Reason      string `json:"reason"`
	ActorID     string `json:"actor_id"`
	ApproverID  string `json:"approver_id"`
	Version     int64  `json:"version"`
	OccurredAt  string `json:"occurred_at"`
}

// Publisher abstracts JetStream PublishMsg for DLQ publishing.
type Publisher interface {
	PublishMsg(m *nats.Msg, opts ...nats.PubOpt) (*nats.PubAck, error)
}
