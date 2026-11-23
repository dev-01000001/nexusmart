package domain

import "time"

const (
	VoucherScopePlatform = "PLATFORM"
	VoucherScopeShop     = "SHOP"
	VoucherScopeFreeship = "FREESHIP"

	VoucherStatusActive  = "ACTIVE"
	VoucherStatusPaused  = "PAUSED"
	VoucherStatusDeleted = "DELETED"

	DiscountTypeFixed   = "FIXED"
	DiscountTypePercent = "PERCENT"

	FlashStatusPending  = "PENDING"
	FlashStatusApproved = "APPROVED"
	FlashStatusRejected = "REJECTED"

	ShopProgramStatusScheduled = "SCHEDULED"
	ShopProgramStatusActive    = "ACTIVE"
	ShopProgramStatusEnded     = "ENDED"

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
	Status          string
	CategoryIDs     []string
	DeletedAt       *time.Time
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EffectiveStatus normalizes legacy rows where Status may be empty.
func (v *Voucher) EffectiveStatus() string {
	if v == nil {
		return VoucherStatusDeleted
	}
	switch v.Status {
	case VoucherStatusActive, VoucherStatusPaused, VoucherStatusDeleted:
		return v.Status
	default:
		if v.DeletedAt != nil {
			return VoucherStatusDeleted
		}
		if v.IsActive {
			return VoucherStatusActive
		}
		return VoucherStatusPaused
	}
}

// FlashSale represents a scheduled flash sale campaign window.
type FlashSale struct {
	ID                 string
	Name               string
	StartsAt           time.Time
	EndsAt             time.Time
	MinDiscountPercent int32
	CategoryIDs        []string
	CreatedBy          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Items              []FlashSaleItem
}

// FlashSaleItem represents a seller SKU enrolled in a flash sale campaign.
type FlashSaleItem struct {
	ID               string
	FlashSaleID      string
	ShopID           string
	SKUID            string
	OriginalPriceVND int64
	FlashPriceVND    int64
	TotalSlots       int32
	RemainingSlots   int32
	PerUserLimit     int32
	Status           string
	RejectionReason  string
	ApprovedBy       string
	StartsAt         time.Time
	EndsAt           time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ShopDiscountProgram represents a seller-managed direct SKU discount campaign (UC-S18).
type ShopDiscountProgram struct {
	ID        string
	ShopID    string
	Name      string
	StartsAt  time.Time
	EndsAt    time.Time
	Status    string
	EndedAt   *time.Time
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	Items     []ShopDiscountProgramItem
}

// EffectiveStatus computes the live status of a shop discount program at time now.
func (p *ShopDiscountProgram) EffectiveStatus(now time.Time) string {
	if p == nil {
		return ShopProgramStatusEnded
	}
	if p.Status == ShopProgramStatusEnded || p.EndedAt != nil || !p.EndsAt.After(now) {
		return ShopProgramStatusEnded
	}
	if now.Before(p.StartsAt) {
		return ShopProgramStatusScheduled
	}
	return ShopProgramStatusActive
}

// ShopDiscountProgramItem represents one SKU entry in a shop discount program.
type ShopDiscountProgramItem struct {
	ID                    string
	ProgramID             string
	ShopID                string
	SKUID                 string
	OriginalPriceVND      int64
	DiscountedPriceVND    int64
	DiscountPercent       int32
	PerUserLimit          int32
	PriceInflationFlagged bool
	Warning               string
	ProgramStatus         string
	StartsAt              time.Time
	EndsAt                time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// IsActiveAt reports whether this program item is currently live at time now.
func (i *ShopDiscountProgramItem) IsActiveAt(now time.Time) bool {
	if i == nil {
		return false
	}
	if i.ProgramStatus == ShopProgramStatusEnded {
		return false
	}
	return !now.Before(i.StartsAt) && now.Before(i.EndsAt)
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
	ShopID          string `json:"shop_id,omitempty"`
}

// ReservedShopProgramItem records a shop discount program item reserved for an order.
type ReservedShopProgramItem struct {
	ShopDiscountProgramItemID string `json:"shop_discount_program_item_id"`
	SKUID                     string `json:"sku_id"`
	Quantity                  int32  `json:"quantity"`
	ShopID                    string `json:"shop_id,omitempty"`
}

// PromotionReservation records an order's promotion reservation in PostgreSQL.
type PromotionReservation struct {
	OrderID          string
	UserID           string
	Status           string
	CoinsUsed        int64
	Vouchers         []ReservedVoucher
	FlashSaleItems   []ReservedFlashItem
	ShopProgramItems []ReservedShopProgramItem
	BreakdownJSON    []byte
	ReleasedShops    []string
	CreatedAt        time.Time
	ReleasedAt       *time.Time
	UpdatedAt        time.Time
}

// ExpiringSavedVoucher represents a user's saved voucher nearing expiration.
type ExpiringSavedVoucher struct {
	UserID    string
	VoucherID string
	Code      string
	EndsAt    time.Time
}
