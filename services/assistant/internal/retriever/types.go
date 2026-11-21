package retriever

import "context"

// SearchQuery filters product retrieval, always honoring ShopID when scoped.
type SearchQuery struct {
	Query       string
	ShopID      string
	Category    string
	MinPriceVND int64
	MaxPriceVND int64
	Sort        string
	Page        int32
	PageSize    int32
}

// SKUHit represents a single SKU variant of a retrieved product.
type SKUHit struct {
	ID             string            `json:"id"`
	SKUCode        string            `json:"sku_code,omitempty"`
	Name           string            `json:"name"`
	PriceVND       int64             `json:"price_vnd"`
	AvailableStock int32             `json:"available_stock"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

// ProductHit is a normalized product returned from Search gRPC or fallback Product API.
type ProductHit struct {
	ID          string   `json:"id"`
	ShopID      string   `json:"shop_id"`
	ShopName    string   `json:"shop_name,omitempty"`
	CategoryID  string   `json:"category_id,omitempty"`
	Category    string   `json:"category,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	MinPriceVND int64    `json:"min_price_vnd"`
	MaxPriceVND int64    `json:"max_price_vnd"`
	ImageURL    string   `json:"image_url,omitempty"`
	InStock     bool     `json:"in_stock"`
	Score       float64  `json:"score,omitempty"`
	SKUs        []SKUHit `json:"skus,omitempty"`
}

// ProductRetriever fetches products from the Search service (gRPC primary, fallback allowed)
// while enforcing strict shop_id tenant isolation.
type ProductRetriever interface {
	Search(ctx context.Context, q SearchQuery) ([]ProductHit, int64, error)
	SimilarProducts(ctx context.Context, productID, shopID string, limit int32) ([]ProductHit, error)
	GetProduct(ctx context.Context, productID string) (*ProductHit, error)
}
