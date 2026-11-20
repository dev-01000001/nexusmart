package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound                     = errors.New("not found")
	ErrAlreadyExists                = errors.New("already exists")
	ErrVersionConflict              = errors.New("version conflict")
	ErrInsufficientStock            = errors.New("insufficient stock")
	ErrInvalidStock                 = errors.New("new stock cannot be less than reserved stock")
	ErrDuplicateReservation         = errors.New("duplicate stock reservation")
	ErrFlashSaleReservationInactive = errors.New("flash sale reservation inactive or expired")
)

const (
	ProductStatusPending    = "PENDING"
	ProductStatusApproved   = "APPROVED"
	ProductStatusRejected   = "REJECTED"
	ProductStatusHidden     = "HIDDEN"
	ProductStatusOutOfStock = "OUT_OF_STOCK"

	SKUStatusActive     = "ACTIVE"
	SKUStatusOutOfStock = "OUT_OF_STOCK"
	SKUStatusInactive   = "INACTIVE"

	ReservationStatusReserved  = "RESERVED"
	ReservationStatusConfirmed = "CONFIRMED"
	ReservationStatusReleased  = "RELEASED"

	FlashReservationStatusActive   = "ACTIVE"
	FlashReservationStatusReleased = "RELEASED"
)

type Category struct {
	ID                string
	Name              string
	Slug              string
	ParentID          string
	CommissionRateBps int32
	SortOrder         int32
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Product struct {
	ID              string
	ShopID          string
	CategoryID      string
	Name            string
	Description     string
	Images          []string
	Status          string
	RejectionReason string
	MinPriceVND     int64
	MaxPriceVND     int64
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type SKU struct {
	ID                     string
	ProductID              string
	ShopID                 string
	ProductName            string
	SKUCode                string
	Name                   string
	Attributes             map[string]string
	PriceVND               int64
	Stock                  int32
	ReservedStock          int32
	FlashSaleReservedStock int32
	Version                int64
	WeightGrams            int32
	ImageURL               string
	CategoryID             string
	CommissionRateBps      int32
	Status                 string
	ProductStatus          string
	ProductDeleted         bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// AvailableStock returns stock - reserved_stock - flash_sale_reserved_stock, clamped at 0.
func (s SKU) AvailableStock() int32 {
	avail := s.Stock - s.ReservedStock - s.FlashSaleReservedStock
	if avail < 0 {
		return 0
	}
	return avail
}

// TotalReservedStock returns reserved_stock + flash_sale_reserved_stock.
func (s SKU) TotalReservedStock() int32 {
	return s.ReservedStock + s.FlashSaleReservedStock
}

// IsActive reports whether the SKU belongs to an approved (or out-of-stock) non-deleted product and is not deactivated.
func (s SKU) IsActive() bool {
	if s.ProductDeleted || s.Status == SKUStatusInactive {
		return false
	}
	return s.ProductStatus == ProductStatusApproved || s.ProductStatus == ProductStatusOutOfStock
}

type StockReservation struct {
	ID                 string
	ReservationGroupID string
	OrderID            string
	SKUID              string
	Quantity           int32
	Status             string
	FlashSaleItemID    string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type FlashSaleStockReservation struct {
	FlashSaleItemID string
	SKUID           string
	ReservedQty     int32
	ConsumedQty     int32
	Status          string
	EndsAt          time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type StockHistoryEntry struct {
	ID             string
	SKUID          string
	OrderID        string
	ChangeType     string
	Delta          int32
	StockBefore    int32
	StockAfter     int32
	ReservedBefore int32
	ReservedAfter  int32
	ActorID        string
	Reason         string
	CreatedAt      time.Time
}

type ListProductsFilter struct {
	CategoryID  string
	Keyword     string
	ShopID      string
	Status      string
	PublicOnly  bool
	MinPriceVND int64
	MaxPriceVND int64
	SortBy      string
	Limit       int32
	Offset      int32
}

// Queries defines all repository operations usable directly or inside a transaction.
type Queries interface {
	CreateCategory(ctx context.Context, cat Category) (Category, error)
	GetCategoryByID(ctx context.Context, id string) (Category, error)
	UpdateCategory(ctx context.Context, cat Category) (Category, error)
	CountProductsInCategory(ctx context.Context, categoryID string) (int64, error)
	CountChildCategories(ctx context.Context, parentID string) (int64, error)
	DeleteCategory(ctx context.Context, id string) (bool, error)
	ListCategories(ctx context.Context, parentID string) ([]Category, error)

	CreateProduct(ctx context.Context, p Product) (Product, error)
	GetProductByID(ctx context.Context, id string) (Product, error)
	UpdateProduct(ctx context.Context, p Product) (Product, error)
	UpdateProductReview(ctx context.Context, id, status, rejectionReason string) (Product, error)
	UpdateProductStatus(ctx context.Context, id, status string) error
	SoftDeleteProduct(ctx context.Context, id string) (bool, error)
	ListProducts(ctx context.Context, filter ListProductsFilter) ([]Product, int64, error)

	CreateSKU(ctx context.Context, s SKU) (SKU, error)
	UpdateSKUDetails(ctx context.Context, s SKU) (SKU, error)
	DeactivateSKUsByProduct(ctx context.Context, productID string) error
	DeactivateSKU(ctx context.Context, skuID string) error
	ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]SKU, error)
	GetSKUsBatch(ctx context.Context, skuIDs []string) ([]SKU, error)
	GetSKUWithContext(ctx context.Context, skuID string) (SKU, error)
	GetSKUWithContextForUpdate(ctx context.Context, skuID string) (SKU, error)
	CountActiveInStockSKUsByProduct(ctx context.Context, productID string) (int64, error)

	UpdateSKUStockOptimistic(ctx context.Context, skuID string, newStock int32, expectedVersion int64) (SKU, error)
	ReserveSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error)
	ReleaseSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error)
	ConfirmSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error)

	AddFlashSaleReservedStock(ctx context.Context, skuID string, qty int32) (SKU, error)
	SubFlashSaleReservedStock(ctx context.Context, skuID string, qty int32) (SKU, error)
	ConsumeFlashSaleStockToOrder(ctx context.Context, skuID string, qty int32) (SKU, error)
	RestoreOrderStockToFlashSale(ctx context.Context, skuID string, releaseReservedQty, addFlashReservedQty int32) (SKU, error)

	ListReservationsByOrderID(ctx context.Context, orderID string, forUpdate bool) ([]StockReservation, error)
	CreateStockReservation(ctx context.Context, r StockReservation) (StockReservation, error)
	TransitionStockReservation(ctx context.Context, id, toStatus, fromStatus string) (bool, error)
	InsertStockHistory(ctx context.Context, h StockHistoryEntry) error

	GetFlashSaleReservation(ctx context.Context, flashSaleItemID string, forUpdate bool) (FlashSaleStockReservation, error)
	CreateFlashSaleReservation(ctx context.Context, r FlashSaleStockReservation) (FlashSaleStockReservation, error)
	ConsumeFlashSaleReservation(ctx context.Context, flashSaleItemID string, qty int32, now time.Time) (FlashSaleStockReservation, error)
	RestoreFlashSaleReservationConsumed(ctx context.Context, flashSaleItemID string, qty int32) (FlashSaleStockReservation, error)
	MarkFlashSaleReservationReleased(ctx context.Context, flashSaleItemID string) (FlashSaleStockReservation, error)
	ListExpiredActiveFlashSaleReservations(ctx context.Context, now time.Time, limit int32) ([]FlashSaleStockReservation, error)
}

// Store is the top-level persistence interface supporting transactions and health pings.
type Store interface {
	Queries
	WithTx(ctx context.Context, fn func(q Queries) error) error
	Ping(ctx context.Context) error
}
