package domain

import "time"

const (
	VoucherScopePlatform = "PLATFORM"
	VoucherScopeShop     = "SHOP"
	VoucherScopeFreeship = "FREESHIP"

	DiscountTypeFixed   = "FIXED"
	DiscountTypePercent = "PERCENT"

	FlashStatusPending  = "PENDING"
	FlashStatusApproved = "APPROVED"
	FlashStatusRejected = "REJECTED"

	ReservationStatusReserved = "RESERVED"
	ReservationStatusReleased = "RELEASED"
)

// Voucher represents a persisted promotion voucher.
type Voucher struct {
	ID              string
	Code            string
	Name            string
	Scope           string
	ShopID          string
	DiscountType    string
	ValueVND        int64
	DiscountPercent int32
	MaxDiscountVND  int64
	MinOrderVND     int64
	Quota           int32
	UsedCount       int32
	PerUserLimit    int32
	BudgetVND       int64
	UsedBudgetVND   int64
	StartsAt        time.Time
	EndsAt          time.Time
	IsActive        bool
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// FlashSale represents a scheduled flash sale campaign window.
type FlashSale struct {
	ID        string
	Name      string
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	Items     []FlashSaleItem
}

// FlashSaleItem represents a seller SKU enrolled in a flash sale campaign.
type FlashSaleItem struct {
	ID              string
	FlashSaleID     string
	ShopID          string
	SKUID           string
	FlashPriceVND   int64
	TotalSlots      int32
	RemainingSlots  int32
	PerUserLimit    int32
	Status          string
	RejectionReason string
	ApprovedBy      string
	StartsAt        time.Time
	EndsAt          time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CoinTransaction represents an immutable entry in the user's coin ledger.
type CoinTransaction struct {
	ID          string
	UserID      string
	Delta       int64
	Reason      string
	ReferenceID string
	CreatedAt   time.Time
}

// ReservedVoucher records a voucher reserved for an order.
type ReservedVoucher struct {
	VoucherID   string `json:"voucher_id"`
	Scope       string `json:"scope"`
	ShopID      string `json:"shop_id,omitempty"`
	DiscountVND int64  `json:"discount_vnd"`
	HasBudget   bool   `json:"has_budget"`
}

// ReservedFlashItem records a flash sale item reserved for an order.
type ReservedFlashItem struct {
	FlashSaleItemID string `json:"flash_sale_item_id"`
	SKUID           string `json:"sku_id"`
	Quantity        int32  `json:"quantity"`
}

// PromotionReservation records an order's promotion reservation in PostgreSQL.
type PromotionReservation struct {
	OrderID        string
	UserID         string
	Status         string
	CoinsUsed      int64
	Vouchers       []ReservedVoucher
	FlashSaleItems []ReservedFlashItem
	BreakdownJSON  []byte
	CreatedAt      time.Time
	ReleasedAt     *time.Time
	UpdatedAt      time.Time
}
