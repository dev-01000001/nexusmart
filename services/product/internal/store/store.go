package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrAlreadyExists        = errors.New("already exists")
	ErrVersionConflict      = errors.New("version conflict")
	ErrInsufficientStock    = errors.New("insufficient stock")
	ErrInvalidStock         = errors.New("new stock cannot be less than reserved stock")
	ErrSKUInactive          = errors.New("sku is inactive")
	ErrDuplicateReservation = errors.New("duplicate stock reservation")
	ErrOrderAlreadyReleased = errors.New("stock reservation for order already released")
	ErrForeignKeyViolation  = errors.New("foreign key constraint violation")
	ErrCategoryCycle        = errors.New("category hierarchy cycle")
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

	PriceHistoryWindow = 30 * 24 * time.Hour
)

type asOfContextKey struct{}

// WithAsOf attaches an explicit evaluation timestamp to ctx for time-sensitive reads
// (30-day minimum SKU price history and effective category commission rate schedules).
func WithAsOf(ctx context.Context, t time.Time) context.Context {
	if t.IsZero() {
		return ctx
	}
	return context.WithValue(ctx, asOfContextKey{}, t.UTC())
}

// AsOfFromContext returns the evaluation timestamp stored in ctx, or time.Now().UTC().
func AsOfFromContext(ctx context.Context) time.Time {
	if ctx != nil {
		if t, ok := ctx.Value(asOfContextKey{}).(time.Time); ok && !t.IsZero() {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

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

type CategoryProductCounts struct {
	Total  int64
	Active int64
}

type CategoryFeeSchedule struct {
	ID                string
	Seq               int64
	CategoryID        string
	CommissionRateBps int32
	EffectiveFrom     time.Time
	CreatedBy         string
	Note              string
	CreatedAt         time.Time
}

type SKUPriceHistoryEntry struct {
	ID         string
	Seq        int64
	SKUID      string
	PriceVND   int64
	ActorID    string
	Reason     string
	RecordedAt time.Time
}

type OutboxEvent struct {
	ID          string
	EventType   string
	TraceID     string
	Traceparent string
	PayloadJSON []byte
	PublishedAt *time.Time
	Attempts    int32
	LastError   string
	CreatedAt   time.Time
}

type Product struct {
	ID              string
	ShopID          string
	CategoryID      string
	Name            string
	Slug            string
	SearchText      string
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
	ID                string
	ProductID         string
	ShopID            string
	ProductName       string
	SKUCode           string
	Name              string
	Attributes        map[string]string
	PriceVND          int64
	MinPrice30dVND    int64
	Stock             int32
	ReservedStock     int32
	Version           int64
	WeightGrams       int32
	ImageURL          string
	CategoryID        string
	CommissionRateBps int32
	Status            string
	ProductStatus     string
	ProductDeleted    bool
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// AvailableStock returns stock - reserved_stock, clamped at 0.
func (s SKU) AvailableStock() int32 {
	avail := s.Stock - s.ReservedStock
	if avail < 0 {
		return 0
	}
	return avail
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
	CreatedAt          time.Time
	UpdatedAt          time.Time
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
	LockCategoryTree(ctx context.Context) error
	CreateCategory(ctx context.Context, cat Category) (Category, error)
	GetCategoryByID(ctx context.Context, id string) (Category, error)
	GetCategoryBySlug(ctx context.Context, slug string) (Category, error)
	UpdateCategory(ctx context.Context, cat Category) (Category, error)
	UpdateCategoryCommissionRate(ctx context.Context, id string, bps int32) error
	CountProductsInCategory(ctx context.Context, categoryID string) (CategoryProductCounts, error)
	CountChildCategories(ctx context.Context, parentID string) (int64, error)
	DeleteCategory(ctx context.Context, id string) (bool, error)
	ListCategories(ctx context.Context, parentID string) ([]Category, error)
	CategoryHasAncestor(ctx context.Context, startID, targetAncestorID string) (bool, error)
	GetCategoryDescendants(ctx context.Context, categoryIDs []string) ([]string, error)

	InsertCategoryFeeSchedule(ctx context.Context, sched CategoryFeeSchedule) (CategoryFeeSchedule, error)
	ListCategoryFeeSchedules(ctx context.Context, categoryID string, limit, offset int32) ([]CategoryFeeSchedule, int64, error)

	CreateProduct(ctx context.Context, p Product) (Product, error)
	GetProductByID(ctx context.Context, id string) (Product, error)
	GetProductByIDForUpdate(ctx context.Context, id string) (Product, error)
	GetProductBySlug(ctx context.Context, slug string) (Product, error)
	UpdateProduct(ctx context.Context, p Product) (Product, error)
	UpdateProductReview(ctx context.Context, id, status, rejectionReason string) (Product, error)
	UpdateProductStatus(ctx context.Context, id, status string) error
	SoftDeleteProduct(ctx context.Context, id string) (bool, error)
	ListProducts(ctx context.Context, filter ListProductsFilter) ([]Product, int64, error)

	CreateSKU(ctx context.Context, s SKU) (SKU, error)
	InsertSKUPriceHistory(ctx context.Context, h SKUPriceHistoryEntry) error
	UpdateSKUMetadata(ctx context.Context, s SKU) (SKU, error)
	ReactivateSKUOptimistic(ctx context.Context, s SKU, expectedVersion int64) (SKU, error)
	DeactivateSKUsByProduct(ctx context.Context, productID string) error
	DeactivateSKU(ctx context.Context, skuID string) error
	ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]SKU, error)
	ListAllSKUsByProductID(ctx context.Context, productID string) ([]SKU, error)
	GetSKUsBatch(ctx context.Context, skuIDs []string) ([]SKU, error)
	GetSKUWithContext(ctx context.Context, skuID string) (SKU, error)
	GetSKUWithContextForUpdate(ctx context.Context, skuID string) (SKU, error)
	CountActiveInStockSKUsByProduct(ctx context.Context, productID string) (int64, error)

	UpdateSKUStockOptimistic(ctx context.Context, skuID string, newStock int32, expectedVersion int64) (SKU, error)
	ReserveSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error)
	ReleaseSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error)
	ConfirmSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error)
	RestockSKUAtomic(ctx context.Context, skuID string, qty int32) (SKU, error)

	LockStockOrder(ctx context.Context, orderID string) error
	InsertStockReleaseTombstone(ctx context.Context, orderID string) (bool, error)
	HasStockReleaseTombstone(ctx context.Context, orderID string) (bool, error)
	ListReservationsByOrderID(ctx context.Context, orderID string, forUpdate bool) ([]StockReservation, error)
	CreateStockReservation(ctx context.Context, r StockReservation) (StockReservation, error)
	TransitionStockReservation(ctx context.Context, id, toStatus, fromStatus string) (bool, error)
	InsertStockHistory(ctx context.Context, h StockHistoryEntry) error
	TryInsertRestockHistory(ctx context.Context, h StockHistoryEntry) (bool, error)

	InsertOutboxEvent(ctx context.Context, ev OutboxEvent) error
}

// Store is the top-level persistence interface supporting transactions, outbox relay, and health pings.
type Store interface {
	Queries
	WithTx(ctx context.Context, fn func(q Queries) error) error
	ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev OutboxEvent) error) (int, error)
	Ping(ctx context.Context) error
}
