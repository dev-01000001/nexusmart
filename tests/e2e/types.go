package e2e

import "strings"

// StatusEquals compares status strings ignoring case and optional proto enum prefixes
// (e.g. "ORDER_STATUS_CONFIRMED", "CONFIRMED", and "confirmed" all match "CONFIRMED").
func StatusEquals(got, want string) bool {
	return NormalizeStatus(got) == NormalizeStatus(want)
}

// NormalizeStatus strips well-known proto enum prefixes and uppercases the value.
func NormalizeStatus(s string) string {
	u := strings.ToUpper(strings.TrimSpace(s))
	for _, prefix := range []string{
		"ORDER_STATUS_",
		"PAYMENT_STATUS_",
		"PAYMENT_METHOD_",
		"ORDER_PAYMENT_METHOD_",
		"USER_STATUS_",
		"USER_ROLE_",
		"SHOP_STATUS_",
		"PRODUCT_STATUS_",
		"FLASH_SALE_ITEM_STATUS_",
		"SHIPMENT_STATUS_",
		"REFUND_STATUS_",
	} {
		u = strings.TrimPrefix(u, prefix)
	}
	return u
}

// --- Auth & User ---

type RegisterRequest struct {
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

type RegisterResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Status string `json:"status"`
}

type VerifyOTPRequest struct {
	Email string `json:"email"`
	OTP   string `json:"otp"`
}

type UserDTO struct {
	ID               string `json:"id"`
	FullName         string `json:"full_name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	Role             string `json:"role"`
	Status           string `json:"status"`
	ShopID           string `json:"shop_id"`
	PhoneVerified    bool   `json:"phone_verified"`
	CODRejectedCount int32  `json:"cod_rejected_count"`
}

type VerifyOTPResponse struct {
	Verified bool    `json:"verified"`
	User     UserDTO `json:"user"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	AccessToken      string  `json:"access_token"`
	RefreshToken     string  `json:"refresh_token"`
	ExpiresInSeconds int64   `json:"expires_in_seconds"`
	User             UserDTO `json:"user"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type RefreshResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type CreateAddressRequest struct {
	RecipientName  string `json:"recipient_name"`
	RecipientPhone string `json:"recipient_phone"`
	ProvinceCode   string `json:"province_code"`
	ProvinceName   string `json:"province_name,omitempty"`
	WardCode       string `json:"ward_code"`
	WardName       string `json:"ward_name,omitempty"`
	StreetAddress  string `json:"street_address"`
	IsDefault      bool   `json:"is_default"`
	LegacyProvince string `json:"legacy_province,omitempty"`
	LegacyDistrict string `json:"legacy_district,omitempty"`
	LegacyWard     string `json:"legacy_ward,omitempty"`
}

type AddressDTO struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	RecipientName  string `json:"recipient_name"`
	RecipientPhone string `json:"recipient_phone"`
	ProvinceCode   string `json:"province_code"`
	ProvinceName   string `json:"province_name"`
	WardCode       string `json:"ward_code"`
	WardName       string `json:"ward_name"`
	StreetAddress  string `json:"street_address"`
	IsDefault      bool   `json:"is_default"`
}

type CreateAddressResponse struct {
	Address AddressDTO `json:"address"`
}

type RegisterShopRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	PickupAddressID    string `json:"pickup_address_id"`
	ContactPhone       string `json:"contact_phone"`
	BusinessLicenseDoc string `json:"business_license_doc"`
}

type ShopDTO struct {
	ID              string `json:"id"`
	OwnerUserID     string `json:"owner_user_id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	PickupAddressID string `json:"pickup_address_id"`
	ContactPhone    string `json:"contact_phone"`
	Status          string `json:"status"`
}

type ShopResponse struct {
	Shop ShopDTO `json:"shop"`
}

type ReviewRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// --- Categories & Products ---

type CreateCategoryRequest struct {
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	ParentID          string `json:"parent_id,omitempty"`
	CommissionRateBps int32  `json:"commission_rate_bps"`
	SortOrder         int32  `json:"sort_order"`
	IsProhibited      bool   `json:"is_prohibited,omitempty"`
}

type CategoryDTO struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	CommissionRateBps int32  `json:"commission_rate_bps"`
	IsProhibited      bool   `json:"is_prohibited"`
}

type CategoryResponse struct {
	Category CategoryDTO `json:"category"`
}

type CategoriesListResponse struct {
	Categories []CategoryDTO `json:"categories"`
}

type SKUInputDTO struct {
	SKUCode     string            `json:"sku_code"`
	Name        string            `json:"name"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	PriceVND    int64             `json:"price_vnd"`
	Stock       int32             `json:"stock"`
	WeightGrams int32             `json:"weight_grams"`
	ImageURL    string            `json:"image_url,omitempty"`
}

type SKUDTO struct {
	ID             string            `json:"id"`
	ProductID      string            `json:"product_id"`
	ShopID         string            `json:"shop_id"`
	ProductName    string            `json:"product_name"`
	SKUCode        string            `json:"sku_code"`
	Name           string            `json:"name"`
	Attributes     map[string]string `json:"attributes"`
	PriceVND       int64             `json:"price_vnd"`
	Stock          int32             `json:"stock"`
	ReservedStock  int32             `json:"reserved_stock"`
	AvailableStock int32             `json:"available_stock"`
	Version        int64             `json:"version"`
	WeightGrams    int32             `json:"weight_grams"`
}

type CreateProductRequest struct {
	CategoryID  string        `json:"category_id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Images      []string      `json:"images"`
	SKUs        []SKUInputDTO `json:"skus"`
}

type ProductDTO struct {
	ID              string   `json:"id"`
	ShopID          string   `json:"shop_id"`
	CategoryID      string   `json:"category_id"`
	Name            string   `json:"name"`
	Slug            string   `json:"slug"`
	Description     string   `json:"description"`
	Status          string   `json:"status"`
	MinPriceVND     int64    `json:"min_price_vnd"`
	MaxPriceVND     int64    `json:"max_price_vnd"`
	SKUs            []SKUDTO `json:"skus"`
	RatingAvgX100   int32    `json:"rating_avg_x100"`
	ReviewCount     int32    `json:"review_count"`
	FlaggedKeywords []string `json:"flagged_keywords"`
}

type ProductResponse struct {
	Product ProductDTO `json:"product"`
}

// --- Cart ---

type AddCartItemRequest struct {
	SKUID    string `json:"sku_id"`
	Quantity int32  `json:"quantity"`
}

type CartItemDTO struct {
	SKUID             string `json:"sku_id"`
	ProductID         string `json:"product_id"`
	ShopID            string `json:"shop_id"`
	ProductName       string `json:"product_name"`
	SKUName           string `json:"sku_name"`
	PriceVND          int64  `json:"price_vnd"`
	Quantity          int32  `json:"quantity"`
	AvailableStock    int32  `json:"available_stock"`
	WeightGrams       int32  `json:"weight_grams"`
	Selected          bool   `json:"selected"`
	Unavailable       bool   `json:"unavailable"`
	UnavailableReason string `json:"unavailable_reason"`
}

type CartShopGroupDTO struct {
	ShopID           string        `json:"shop_id"`
	ShopName         string        `json:"shop_name"`
	Items            []CartItemDTO `json:"items"`
	SubtotalVND      int64         `json:"subtotal_vnd"`
	TotalWeightGrams int32         `json:"total_weight_grams"`
}

type CartDTO struct {
	UserID     string             `json:"user_id"`
	ShopGroups []CartShopGroupDTO `json:"shop_groups"`
	TotalItems int32              `json:"total_items"`
	TotalVND   int64              `json:"total_vnd"`
}

type CartResponse struct {
	Cart           CartDTO `json:"cart"`
	ClampedToStock bool    `json:"clamped_to_stock"`
}

// --- Promotions ---

type CreateVoucherRequest struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	Scope           string `json:"scope,omitempty"`
	DiscountType    string `json:"discount_type"`
	ValueVND        int64  `json:"value_vnd"`
	DiscountPercent int32  `json:"discount_percent"`
	MaxDiscountVND  int64  `json:"max_discount_vnd"`
	MinOrderVND     int64  `json:"min_order_vnd"`
	Quota           int32  `json:"quota"`
	PerUserLimit    int32  `json:"per_user_limit"`
	BudgetVND       int64  `json:"budget_vnd"`
	StartsAt        string `json:"starts_at"`
	EndsAt          string `json:"ends_at"`
}

type VoucherDTO struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	Scope          string `json:"scope"`
	ShopID         string `json:"shop_id"`
	DiscountType   string `json:"discount_type"`
	ValueVND       int64  `json:"value_vnd"`
	Quota          int32  `json:"quota"`
	UsedCount      int32  `json:"used_count"`
	RemainingQuota int32  `json:"remaining_quota"`
	PerUserLimit   int32  `json:"per_user_limit"`
	BudgetVND      int64  `json:"budget_vnd"`
	IsActive       bool   `json:"is_active"`
	Saved          bool   `json:"saved"`
	Eligible       bool   `json:"eligible"`
}

type VoucherResponse struct {
	Voucher VoucherDTO `json:"voucher"`
}

type VouchersListResponse struct {
	Vouchers []VoucherDTO `json:"vouchers"`
}

type SaveVoucherRequest struct {
	Code string `json:"code,omitempty"`
}

type SaveVoucherResponse struct {
	Saved   bool       `json:"saved"`
	Voucher VoucherDTO `json:"voucher"`
}

type CreateFlashSaleRequest struct {
	Name     string `json:"name"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
}

type FlashSaleItemDTO struct {
	ID             string `json:"id"`
	FlashSaleID    string `json:"flash_sale_id"`
	ShopID         string `json:"shop_id"`
	SKUID          string `json:"sku_id"`
	FlashPriceVND  int64  `json:"flash_price_vnd"`
	TotalSlots     int32  `json:"total_slots"`
	RemainingSlots int32  `json:"remaining_slots"`
	PerUserLimit   int32  `json:"per_user_limit"`
	Status         string `json:"status"`
}

type FlashSaleDTO struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	StartsAt string             `json:"starts_at"`
	EndsAt   string             `json:"ends_at"`
	Items    []FlashSaleItemDTO `json:"items"`
}

type FlashSaleResponse struct {
	FlashSale FlashSaleDTO `json:"flash_sale"`
}

type FlashSalesListResponse struct {
	FlashSales []FlashSaleDTO `json:"flash_sales"`
}

type RegisterFlashSaleItemRequest struct {
	SKUID         string `json:"sku_id"`
	FlashPriceVND int64  `json:"flash_price_vnd"`
	TotalSlots    int32  `json:"total_slots"`
	PerUserLimit  int32  `json:"per_user_limit"`
}

type FlashSaleItemResponse struct {
	Item FlashSaleItemDTO `json:"item"`
}

type ApproveFlashSaleItemRequest struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason"`
}

type CoinBalanceResponse struct {
	UserID  string `json:"user_id"`
	Balance int64  `json:"balance"`
}

type CreditCoinsRequest struct {
	UserID      string `json:"user_id"`
	Amount      int64  `json:"amount"`
	Reason      string `json:"reason"`
	ReferenceID string `json:"reference_id"`
}

type CreditCoinsResponse struct {
	UserID          string `json:"user_id"`
	Balance         int64  `json:"balance"`
	AlreadyCredited bool   `json:"already_credited"`
}

type DiscountLineItemDTO struct {
	SKUID           string `json:"sku_id"`
	Quantity        int32  `json:"quantity"`
	UnitPriceVND    int64  `json:"unit_price_vnd"`
	FlashSaleItemID string `json:"flash_sale_item_id,omitempty"`
}

type ShopDiscountInputDTO struct {
	ShopID         string                `json:"shop_id"`
	SubtotalVND    int64                 `json:"subtotal_vnd"`
	ShippingFeeVND int64                 `json:"shipping_fee_vnd"`
	Items          []DiscountLineItemDTO `json:"items"`
}

type PreviewDiscountsRequest struct {
	ShopItems         []ShopDiscountInputDTO `json:"shop_items"`
	ShopVoucherIDs    map[string]string      `json:"shop_voucher_ids,omitempty"`
	PlatformVoucherID string                 `json:"platform_voucher_id,omitempty"`
	FreeshipVoucherID string                 `json:"freeship_voucher_id,omitempty"`
	CoinsToUse        int64                  `json:"coins_to_use,omitempty"`
}

type DiscountBreakdownDTO struct {
	TotalSubtotalVND               int64    `json:"total_subtotal_vnd"`
	TotalFlashSaleDiscountVND      int64    `json:"total_flash_sale_discount_vnd"`
	TotalShopVoucherDiscountVND    int64    `json:"total_shop_voucher_discount_vnd"`
	TotalPlatformVoucherDiscountVN int64    `json:"total_platform_voucher_discount_vnd"`
	TotalFreeshipDiscountVND       int64    `json:"total_freeship_discount_vnd"`
	TotalCoinsDiscountVND          int64    `json:"total_coins_discount_vnd"`
	TotalDiscountVND               int64    `json:"total_discount_vnd"`
	PlatformCostVND                int64    `json:"platform_cost_vnd"`
	ShopCostVND                    int64    `json:"shop_cost_vnd"`
	TotalShippingFeeVND            int64    `json:"total_shipping_fee_vnd"`
	FinalPayableVND                int64    `json:"final_payable_vnd"`
	Warnings                       []string `json:"warnings"`
}

type PreviewDiscountsResponse struct {
	Breakdown DiscountBreakdownDTO `json:"breakdown"`
}

// --- Shipping ---

type ProvinceDTO struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type ProvincesResponse struct {
	Provinces []ProvinceDTO `json:"provinces"`
}

type WardDTO struct {
	Code         string `json:"code"`
	ProvinceCode string `json:"province_code"`
	Name         string `json:"name"`
	CODSupported bool   `json:"cod_supported"`
}

type WardsResponse struct {
	Wards []WardDTO `json:"wards"`
}

type CalculateFeeRequest struct {
	ShopID           string `json:"shop_id"`
	FromProvinceCode string `json:"from_province_code"`
	FromWardCode     string `json:"from_ward_code"`
	ToProvinceCode   string `json:"to_province_code"`
	ToWardCode       string `json:"to_ward_code"`
	WeightGrams      int32  `json:"weight_grams"`
	CODAmountVND     int64  `json:"cod_amount_vnd"`
}

type CarrierQuoteDTO struct {
	CarrierCode   string `json:"carrier_code"`
	CarrierName   string `json:"carrier_name"`
	ServiceCode   string `json:"service_code"`
	ServiceName   string `json:"service_name"`
	FeeVND        int64  `json:"fee_vnd"`
	EstimatedDays int32  `json:"estimated_days"`
	IsFallback    bool   `json:"is_fallback"`
	CODSupported  bool   `json:"cod_supported"`
}

type CalculateFeeResponse struct {
	Quotes      []CarrierQuoteDTO `json:"quotes"`
	IsEstimated bool              `json:"is_estimated"`
}

type ShippingWebhookPayload struct {
	EventID      string `json:"event_id"`
	TrackingCode string `json:"tracking_code"`
	OrderID      string `json:"order_id"`
	RawStatus    string `json:"raw_status"`
	Description  string `json:"description"`
	OccurredAt   string `json:"occurred_at"`
}

// --- Checkout & Orders ---

type CheckoutItemInputDTO struct {
	SKUID           string `json:"sku_id"`
	Quantity        int32  `json:"quantity"`
	FlashSaleItemID string `json:"flash_sale_item_id,omitempty"`
}

type ShopShippingChoiceDTO struct {
	ShopID      string `json:"shop_id"`
	CarrierCode string `json:"carrier_code"`
	ServiceCode string `json:"service_code"`
}

type CheckoutRequest struct {
	AddressID         string                  `json:"address_id"`
	FromCart          bool                    `json:"from_cart"`
	Items             []CheckoutItemInputDTO  `json:"items,omitempty"`
	ShopVoucherIDs    map[string]string       `json:"shop_voucher_ids,omitempty"`
	PlatformVoucherID string                  `json:"platform_voucher_id,omitempty"`
	FreeshipVoucherID string                  `json:"freeship_voucher_id,omitempty"`
	CoinsToUse        int64                   `json:"coins_to_use,omitempty"`
	PaymentMethod     string                  `json:"payment_method"`
	ShippingChoices   []ShopShippingChoiceDTO `json:"shipping_choices,omitempty"`
}

type OrderItemDTO struct {
	ID                    string `json:"id"`
	OrderID               string `json:"order_id"`
	SKUID                 string `json:"sku_id"`
	ProductID             string `json:"product_id"`
	ProductName           string `json:"product_name"`
	SKUName               string `json:"sku_name"`
	UnitPriceVND          int64  `json:"unit_price_vnd"`
	EffectiveUnitPriceVND int64  `json:"effective_unit_price_vnd"`
	Quantity              int32  `json:"quantity"`
	LineTotalVND          int64  `json:"line_total_vnd"`
	FlashSaleItemID       string `json:"flash_sale_item_id"`
	CommissionBps         int32  `json:"commission_bps"`
}

type OrderTimelineEventDTO struct {
	ID         string `json:"id"`
	OrderID    string `json:"order_id"`
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	ActorID    string `json:"actor_id"`
	ActorRole  string `json:"actor_role"`
	Note       string `json:"note"`
	CreatedAt  string `json:"created_at"`
}

type OrderDTO struct {
	ID                       string                  `json:"id"`
	OrderCode                string                  `json:"order_code"`
	CheckoutGroupID          string                  `json:"checkout_group_id"`
	BuyerID                  string                  `json:"buyer_id"`
	ShopID                   string                  `json:"shop_id"`
	Status                   string                  `json:"status"`
	PaymentMethod            string                  `json:"payment_method"`
	PaymentID                string                  `json:"payment_id"`
	PaymentStatus            string                  `json:"payment_status"`
	SubtotalVND              int64                   `json:"subtotal_vnd"`
	FlashSaleDiscountVND     int64                   `json:"flash_sale_discount_vnd"`
	ShopVoucherDiscountVND   int64                   `json:"shop_voucher_discount_vnd"`
	PlatformVoucherDiscountV int64                   `json:"platform_voucher_discount_vnd"`
	FreeshipDiscountVND      int64                   `json:"freeship_discount_vnd"`
	CoinsDiscountVND         int64                   `json:"coins_discount_vnd"`
	ShippingFeeVND           int64                   `json:"shipping_fee_vnd"`
	CommissionVND            int64                   `json:"commission_vnd"`
	PaymentFeeVND            int64                   `json:"payment_fee_vnd"`
	PaymentFeeBps            int32                   `json:"payment_fee_bps"`
	TotalPayableVND          int64                   `json:"total_payable_vnd"`
	CarrierCode              string                  `json:"carrier_code"`
	ServiceCode              string                  `json:"service_code"`
	TrackingCode             string                  `json:"tracking_code"`
	LabelURL                 string                  `json:"label_url"`
	ShipmentJobStatus        string                  `json:"shipment_job_status"`
	Items                    []OrderItemDTO          `json:"items"`
	Timeline                 []OrderTimelineEventDTO `json:"timeline"`
}

type CheckoutResponse struct {
	CheckoutGroupID  string     `json:"checkout_group_id"`
	Orders           []OrderDTO `json:"orders"`
	PaymentID        string     `json:"payment_id"`
	PaymentMethod    string     `json:"payment_method"`
	PaymentStatus    string     `json:"payment_status"`
	QRURL            string     `json:"qr_url"`
	QRCode           string     `json:"qr_code"`
	PaymentExpiresAt string     `json:"payment_expires_at"`
	TotalPayableVND  int64      `json:"total_payable_vnd"`
	IdempotentReplay bool       `json:"idempotent_replay"`
	Warnings         []string   `json:"warnings"`
}

type OrderResponse struct {
	Order OrderDTO `json:"order"`
}

type OrdersListResponse struct {
	Items    []OrderDTO `json:"items"`
	Page     int32      `json:"page"`
	PageSize int32      `json:"page_size"`
	Total    int64      `json:"total"`
}

type OrderTimelineResponse struct {
	Events []OrderTimelineEventDTO `json:"events"`
}

type SellerShipRequest struct {
	CarrierCode string `json:"carrier_code"`
	ServiceCode string `json:"service_code"`
}

type SellerShipResponse struct {
	Order         OrderDTO `json:"order"`
	TrackingCode  string   `json:"tracking_code"`
	LabelURL      string   `json:"label_url"`
	Queued        bool     `json:"queued"`
	ShipmentJobID string   `json:"shipment_job_id"`
}

// --- Payments, Webhooks & Ledger ---

type PaymentDTO struct {
	PaymentID    string `json:"payment_id"`
	ID           string `json:"id"`
	OrderGroupID string `json:"order_group_id"`
	OrderID      string `json:"order_id"`
	UserID       string `json:"user_id"`
	AmountVND    int64  `json:"amount_vnd"`
	Method       string `json:"method"`
	Status       string `json:"status"`
	QRURL        string `json:"qr_url"`
	QRCode       string `json:"qr_code"`
	ProviderRef  string `json:"provider_ref"`
}

type PaymentResponse struct {
	Payment PaymentDTO `json:"payment"`
}

type PaymentWebhookPayload struct {
	Provider     string `json:"provider"`
	ProviderTxID string `json:"provider_tx_id"`
	PaymentID    string `json:"payment_id"`
	OrderGroupID string `json:"order_group_id"`
	AmountVND    int64  `json:"amount_vnd"`
	Status       string `json:"status"`
	Reference    string `json:"reference"`
	OccurredAt   string `json:"occurred_at"`
}

type PaymentWebhookResult struct {
	Status       string `json:"status"`
	ProviderTxID string `json:"provider_tx_id"`
	PaymentID    string `json:"payment_id"`
	Duplicate    bool   `json:"duplicate"`
}

type RefundRequest struct {
	OrderID   string `json:"order_id"`
	AmountVND int64  `json:"amount_vnd"`
	Reason    string `json:"reason"`
}

type RefundDTO struct {
	RefundID       string `json:"refund_id"`
	PaymentID      string `json:"payment_id"`
	OrderID        string `json:"order_id"`
	AmountVND      int64  `json:"amount_vnd"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key"`
}

type RefundResponse struct {
	Refund         RefundDTO `json:"refund"`
	AlreadyExisted bool      `json:"already_existed"`
}

type SellerBalanceResponse struct {
	ShopID         string `json:"shop_id"`
	PendingVND     int64  `json:"pending_vnd"`
	AvailableVND   int64  `json:"available_vnd"`
	TotalEarnedVND int64  `json:"total_earned_vnd"`
}

type AccountBalanceDTO struct {
	AccountCode    string `json:"account_code"`
	AccountType    string `json:"account_type"`
	DebitTotalVND  int64  `json:"debit_total_vnd"`
	CreditTotalVND int64  `json:"credit_total_vnd"`
	BalanceVND     int64  `json:"balance_vnd"`
}

type LedgerSummaryResponse struct {
	TotalDebitsVND  int64               `json:"total_debits_vnd"`
	TotalCreditsVND int64               `json:"total_credits_vnd"`
	IsBalanced      bool                `json:"is_balanced"`
	Accounts        []AccountBalanceDTO `json:"accounts"`
}

// --- Notifications ---

type NotificationDTO struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	EventID   string `json:"event_id"`
	IsRead    bool   `json:"is_read"`
	CreatedAt string `json:"created_at"`
}

type NotificationsListResponse struct {
	Items       []NotificationDTO `json:"items"`
	UnreadCount int64             `json:"unread_count"`
	Page        int32             `json:"page"`
	PageSize    int32             `json:"page_size"`
	Total       int64             `json:"total"`
}
