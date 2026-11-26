package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

	ReviewStatusPublished = "PUBLISHED"
	ReviewStatusHidden    = "HIDDEN"

	BannedKeywordActionBlock  = "BLOCK"
	BannedKeywordActionReview = "REVIEW"

	ImportJobStatusQueued     = "QUEUED"
	ImportJobStatusValidating = "VALIDATING"
	ImportJobStatusReady      = "READY"
	ImportJobStatusConfirming = "CONFIRMING"
	ImportJobStatusCompleted  = "COMPLETED"
	ImportJobStatusFailed     = "FAILED"

	ImportRowStatusPending = "PENDING"
	ImportRowStatusValid   = "VALID"
	ImportRowStatusInvalid = "INVALID"
	ImportRowStatusCreated = "CREATED"
	ImportRowStatusFailed  = "FAILED"

	PriceHistoryWindow = 30 * 24 * time.Hour
	LowStockThreshold  = 5
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
	IsProhibited      bool
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
	ID           string
	EventType    string
	AggregateID  string
	TraceID      string
	Traceparent  string
	PayloadJSON  []byte
	PublishedAt  *time.Time
	Attempts     int32
	LastError    string
	CreatedAt    time.Time
	Seq          int64
	ClaimedUntil *time.Time
	ClaimToken   string
}

// OutboxAggregateKey returns the ordering key for an outbox event (explicit AggregateID,
// falling back to payload_json.product_id for legacy/test events).
func OutboxAggregateKey(ev OutboxEvent) string {
	if id := strings.TrimSpace(ev.AggregateID); id != "" {
		return id
	}
	if len(ev.PayloadJSON) == 0 {
		return ""
	}
	var payload struct {
		ProductID string `json:"product_id"`
	}
	if err := json.Unmarshal(ev.PayloadJSON, &payload); err == nil {
		return strings.TrimSpace(payload.ProductID)
	}
	return ""
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
	RatingSum       int64
	ReviewCount     int32
	FlaggedKeywords []string
	FirstApprovedAt *time.Time
	ShopSuspended   bool
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ShopSuspensionState struct {
	ShopID    string
	Suspended bool
	Version   int64
	UpdatedAt time.Time
}

type DeadLetterEvent struct {
	ID           string
	Subject      string
	EventID      string
	Payload      []byte
	ErrorMessage string
	Attempts     int32
	CreatedAt    time.Time
}

// ComputeRatingAvgX100 computes integer average rating scaled by 100 (0..500) using integer arithmetic only (E6/E14).
func ComputeRatingAvgX100(ratingSum int64, reviewCount int32) int32 {
	if reviewCount <= 0 || ratingSum <= 0 {
		return 0
	}
	rc := int64(reviewCount)
	avg := (ratingSum*100 + rc/2) / rc
	if avg < 0 {
		return 0
	}
	if avg > 500 {
		return 500
	}
	return int32(avg)
}

// RatingAvgX100 returns the integer average rating scaled by 100 (0..500).
func (p Product) RatingAvgX100() int32 {
	return ComputeRatingAvgX100(p.RatingSum, p.ReviewCount)
}

type ProductReview struct {
	ID          string
	ProductID   string
	ShopID      string
	OrderID     string
	OrderItemID string
	SKUID       string
	SKUName     string
	BuyerID     string
	BuyerName   string
	Rating      int32
	Comment     string
	ImageURLs   []string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ListProductReviewsFilter struct {
	ProductID    string
	RatingFilter int32
	HasImages    bool
	Limit        int32
	Offset       int32
}

type BannedKeyword struct {
	ID             string
	Term           string
	NormalizedTerm string
	Category       string
	Action         string
	IsActive       bool
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ListBannedKeywordsFilter struct {
	Query           string
	Action          string
	IncludeInactive bool
	Limit           int32
	Offset          int32
}

type ModerationSKU struct {
	ProductID  string
	Name       string
	Attributes map[string]string
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
	MinPrice30dVND         int64
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
	ShopSuspended          bool
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
// Intentionally ignores ShopSuspended so in-flight order stock reservations (ReserveStock) are not blocked by shop suspension.
func (s SKU) IsActive() bool {
	if s.ProductDeleted || s.Status == SKUStatusInactive {
		return false
	}
	return s.ProductStatus == ProductStatusApproved || s.ProductStatus == ProductStatusOutOfStock
}

// IsPubliclyActive reports whether the SKU is active and its owning shop is not suspended (F12).
func (s SKU) IsPubliclyActive() bool {
	return s.IsActive() && !s.ShopSuspended
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

type StockHistoryRecord struct {
	ID             string
	SKUID          string
	ProductID      string
	ShopID         string
	ProductName    string
	SKUCode        string
	SKUName        string
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

type ListStockHistoryFilter struct {
	ShopID     string
	ProductID  string
	SKUID      string
	ChangeType string
	FromTime   *time.Time
	ToTime     *time.Time
	Limit      int32
	Offset     int32
}

type ProductImportJob struct {
	ID           string
	ShopID       string
	CreatedBy    string
	Filename     string
	RawCSV       []byte
	Status       string
	TotalRows    int32
	ValidRows    int32
	InvalidRows  int32
	CreatedRows  int32
	FailedRows   int32
	ErrorMessage string
	ClaimedUntil *time.Time
	ClaimToken   string
	ConfirmedAt  *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ProductImportRow struct {
	ID               string
	JobID            string
	RowNumber        int32
	Status           string
	ErrorCode        string
	ErrorMessage     string
	ProductName      string
	SKUCode          string
	CategoryID       string
	PriceVND         int64
	Stock            int32
	WeightGrams      int32
	CreatedProductID string
	RawRowJSON       []byte
}

type ListProductImportRowsFilter struct {
	JobID      string
	Status     string
	ErrorsOnly bool
	Limit      int32
	Offset     int32
}

type ImportRowOutcomeCounts struct {
	RemainingValid int32
	Created        int32
	Failed         int32
}

type ListProductsFilter struct {
	CategoryID   string
	Keyword      string
	MatchSKUCode bool
	ShopID       string
	Status       string
	PublicOnly   bool
	LowStock     bool
	MinPriceVND  int64
	MaxPriceVND  int64
	SKUIDs       []string
	SortBy       string
	Limit        int32
	Offset       int32
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
	IsCategoryProhibited(ctx context.Context, categoryID string) (bool, error)
	ListProhibitedCategoryIDs(ctx context.Context) ([]string, error)
	GetCategoryDescendants(ctx context.Context, categoryIDs []string) ([]string, error)
	GetCategoryAncestors(ctx context.Context, categoryID string) ([]string, error)

	InsertCategoryFeeSchedule(ctx context.Context, sched CategoryFeeSchedule) (CategoryFeeSchedule, error)
	ListCategoryFeeSchedules(ctx context.Context, categoryID string, limit, offset int32) ([]CategoryFeeSchedule, int64, error)

	CreateProduct(ctx context.Context, p Product) (Product, error)
	GetProductByID(ctx context.Context, id string) (Product, error)
	GetProductByIDForUpdate(ctx context.Context, id string) (Product, error)
	GetProductByIDForUpdateAny(ctx context.Context, id string) (Product, error)
	GetProductShopIDAny(ctx context.Context, id string) (string, error)
	GetProductBySlug(ctx context.Context, slug string) (Product, error)
	GetDeletedApprovedProductBySlug(ctx context.Context, slug string) (Product, error)
	UpdateProduct(ctx context.Context, p Product) (Product, error)
	UpdateProductReview(ctx context.Context, id, status, rejectionReason string, flaggedKeywords []string) (Product, error)
	IncrementProductRating(ctx context.Context, id string, ratingDelta int64) (int64, int32, error)
	ListProductsForModerationScan(ctx context.Context, cursorID string, limit int32) ([]Product, error)
	ListProductsForIndex(ctx context.Context, afterID string, limit int32) ([]Product, error)
	UpdateProductStatus(ctx context.Context, id, status string) error
	SoftDeleteProduct(ctx context.Context, id string) (time.Time, bool, error)
	ListProducts(ctx context.Context, filter ListProductsFilter) ([]Product, int64, error)

	CreateProductReview(ctx context.Context, r ProductReview) (ProductReview, error)
	ListProductReviews(ctx context.Context, filter ListProductReviewsFilter) ([]ProductReview, int64, error)
	GetProductReviewRatingCounts(ctx context.Context, productID string) (map[int32]int32, error)
	ListReviewsByOrderAndBuyer(ctx context.Context, orderID, buyerID string) ([]ProductReview, error)

	CreateBannedKeyword(ctx context.Context, kw BannedKeyword) (BannedKeyword, error)
	GetBannedKeywordByID(ctx context.Context, id string) (BannedKeyword, error)
	UpdateBannedKeyword(ctx context.Context, kw BannedKeyword) (BannedKeyword, error)
	DeleteBannedKeyword(ctx context.Context, id string) (bool, error)
	ListBannedKeywords(ctx context.Context, filter ListBannedKeywordsFilter) ([]BannedKeyword, int64, error)
	ListActiveBannedKeywords(ctx context.Context) ([]BannedKeyword, error)

	CreateSKU(ctx context.Context, s SKU) (SKU, error)
	InsertSKUPriceHistory(ctx context.Context, h SKUPriceHistoryEntry) error
	UpdateSKUMetadata(ctx context.Context, s SKU) (SKU, error)
	ReactivateSKUOptimistic(ctx context.Context, s SKU, expectedVersion int64) (SKU, error)
	DeactivateSKUsByProduct(ctx context.Context, productID string) error
	DeactivateSKU(ctx context.Context, skuID string) error
	ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]SKU, error)
	ListModerationSKUsByProductIDs(ctx context.Context, productIDs []string) ([]ModerationSKU, error)
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
	ListStockHistory(ctx context.Context, filter ListStockHistoryFilter) ([]StockHistoryRecord, int64, error)

	ListExistingShopSKUCodes(ctx context.Context, shopID string, skuCodes []string) (map[string]bool, error)
	CreateProductImportJob(ctx context.Context, job ProductImportJob) (ProductImportJob, error)
	// HasActiveProductImportJob reports whether shopID has a QUEUED, VALIDATING or CONFIRMING import job
	// other than excludeJobID (when non-empty).
	HasActiveProductImportJob(ctx context.Context, shopID, excludeJobID string) (bool, error)
	GetProductImportJobByID(ctx context.Context, id string) (ProductImportJob, error)
	GetProductImportJobByIDForUpdate(ctx context.Context, id string) (ProductImportJob, error)
	ClaimNextProductImportJob(ctx context.Context, claimToken string, lease time.Duration) (ProductImportJob, bool, error)
	MarkProductImportJobReady(ctx context.Context, id, claimToken string, totalRows, validRows, invalidRows int32) (ProductImportJob, bool, error)
	MarkProductImportJobFailed(ctx context.Context, id, claimToken, errMsg string) (ProductImportJob, bool, error)
	TransitionProductImportJobConfirming(ctx context.Context, id string) (ProductImportJob, bool, error)
	UpdateProductImportJobProgress(ctx context.Context, id, claimToken string, createdDelta, failedDelta int32, lease time.Duration) (bool, error)
	MarkProductImportJobCompleted(ctx context.Context, id, claimToken string, createdRows, failedRows int32) (ProductImportJob, bool, error)
	DeleteProductImportRowsByJob(ctx context.Context, jobID string) error
	InsertProductImportRow(ctx context.Context, row ProductImportRow) error
	ListProductImportRows(ctx context.Context, filter ListProductImportRowsFilter) ([]ProductImportRow, int64, error)
	ListValidProductImportRows(ctx context.Context, jobID string) ([]ProductImportRow, error)
	GetProductImportRowByIDForUpdate(ctx context.Context, id string) (ProductImportRow, error)
	UpdateProductImportRowOutcome(ctx context.Context, id, status, errCode, errMsg, createdProductID string) (bool, error)
	GetProductImportRowOutcomeCounts(ctx context.Context, jobID string) (ImportRowOutcomeCounts, error)

	NextProductEventVersion(ctx context.Context) (int64, error)
	InsertOutboxEvent(ctx context.Context, ev OutboxEvent) error

	LockShopSuspension(ctx context.Context, shopID string) error
	GetShopSuspensionState(ctx context.Context, shopID string) (ShopSuspensionState, error)
	IsShopSuspended(ctx context.Context, shopID string) (bool, error)
	UpsertShopSuspensionState(ctx context.Context, shopID string, suspended bool, version int64) (ShopSuspensionState, bool, error)
	SetProductsShopSuspendedByShop(ctx context.Context, shopID string, suspended bool) error
	TryInsertProcessedEvent(ctx context.Context, eventID, eventType string) (bool, error)
	InsertDeadLetterEvent(ctx context.Context, ev DeadLetterEvent) error
}

// Store is the top-level persistence interface supporting transactions, outbox relay, and health pings.
type Store interface {
	Queries
	WithTx(ctx context.Context, fn func(q Queries) error) error
	ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev OutboxEvent) error) (int, error)
	Ping(ctx context.Context) error
}
