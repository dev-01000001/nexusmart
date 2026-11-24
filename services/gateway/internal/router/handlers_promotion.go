package router

import (
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type voucherDTO struct {
	ID               string   `json:"id"`
	Code             string   `json:"code"`
	Name             string   `json:"name"`
	Scope            string   `json:"scope"`
	ShopID           string   `json:"shop_id"`
	DiscountType     string   `json:"discount_type"`
	ValueVND         int64    `json:"value_vnd"`
	DiscountPercent  int32    `json:"discount_percent"`
	MaxDiscountVND   int64    `json:"max_discount_vnd"`
	MinOrderVND      int64    `json:"min_order_vnd"`
	Quota            int32    `json:"quota"`
	UsedCount        int32    `json:"used_count"`
	RemainingQuota   int32    `json:"remaining_quota"`
	PerUserLimit     int32    `json:"per_user_limit"`
	BudgetVND        int64    `json:"budget_vnd"`
	UsedBudgetVND    int64    `json:"used_budget_vnd"`
	StartsAt         string   `json:"starts_at,omitempty"`
	EndsAt           string   `json:"ends_at,omitempty"`
	IsActive         bool     `json:"is_active"`
	Status           string   `json:"status"`
	CategoryIDs      []string `json:"category_ids"`
	Saved            bool     `json:"saved"`
	Eligible         bool     `json:"eligible"`
	IneligibleReason string   `json:"ineligible_reason"`
}

type flashSaleItemDTO struct {
	ID               string `json:"id"`
	FlashSaleID      string `json:"flash_sale_id"`
	ShopID           string `json:"shop_id"`
	SKUID            string `json:"sku_id"`
	FlashPriceVND    int64  `json:"flash_price_vnd"`
	OriginalPriceVND int64  `json:"original_price_vnd"`
	TotalSlots       int32  `json:"total_slots"`
	RemainingSlots   int32  `json:"remaining_slots"`
	PerUserLimit     int32  `json:"per_user_limit"`
	Status           string `json:"status"`
	RejectionReason  string `json:"rejection_reason"`
	CreatedAt        string `json:"created_at,omitempty"`
}

type flashSaleDTO struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	StartsAt           string              `json:"starts_at,omitempty"`
	EndsAt             string              `json:"ends_at,omitempty"`
	MinDiscountPercent int32               `json:"min_discount_percent"`
	CategoryIDs        []string            `json:"category_ids"`
	Items              []*flashSaleItemDTO `json:"items"`
}

type shopDiscountProgramItemDTO struct {
	ID                    string `json:"id"`
	ProgramID             string `json:"program_id"`
	ShopID                string `json:"shop_id"`
	SKUID                 string `json:"sku_id"`
	OriginalPriceVND      int64  `json:"original_price_vnd"`
	DiscountedPriceVND    int64  `json:"discounted_price_vnd"`
	DiscountPercent       int32  `json:"discount_percent"`
	PerUserLimit          int32  `json:"per_user_limit"`
	PriceInflationFlagged bool   `json:"price_inflation_flagged"`
	Warning               string `json:"warning"`
}

type shopDiscountProgramDTO struct {
	ID        string                        `json:"id"`
	ShopID    string                        `json:"shop_id"`
	Name      string                        `json:"name"`
	StartsAt  string                        `json:"starts_at,omitempty"`
	EndsAt    string                        `json:"ends_at,omitempty"`
	Status    string                        `json:"status"`
	Items     []*shopDiscountProgramItemDTO `json:"items"`
	Warnings  []string                      `json:"warnings"`
	CreatedAt string                        `json:"created_at,omitempty"`
	UpdatedAt string                        `json:"updated_at,omitempty"`
}

type activeShopDiscountDTO struct {
	ItemID             string `json:"item_id"`
	ProgramID          string `json:"program_id"`
	ShopID             string `json:"shop_id"`
	SKUID              string `json:"sku_id"`
	OriginalPriceVND   int64  `json:"original_price_vnd"`
	DiscountedPriceVND int64  `json:"discounted_price_vnd"`
	DiscountPercent    int32  `json:"discount_percent"`
	PerUserLimit       int32  `json:"per_user_limit"`
	StartsAt           string `json:"starts_at,omitempty"`
	EndsAt             string `json:"ends_at,omitempty"`
}

type coinTransactionDTO struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Delta       int64  `json:"delta"`
	Reason      string `json:"reason"`
	ReferenceID string `json:"reference_id"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type shopDiscountBreakdownDTO struct {
	ShopID                     string   `json:"shop_id"`
	SubtotalVND                int64    `json:"subtotal_vnd"`
	FlashSaleDiscountVND       int64    `json:"flash_sale_discount_vnd"`
	ShopProgramDiscountVND     int64    `json:"shop_program_discount_vnd"`
	ShopVoucherID              string   `json:"shop_voucher_id"`
	ShopVoucherDiscountVND     int64    `json:"shop_voucher_discount_vnd"`
	ShopFreeshipDiscountVND    int64    `json:"shop_freeship_discount_vnd"`
	PlatformVoucherDiscountVND int64    `json:"platform_voucher_discount_vnd"`
	FreeshipDiscountVND        int64    `json:"freeship_discount_vnd"`
	CoinsDiscountVND           int64    `json:"coins_discount_vnd"`
	ShippingFeeVND             int64    `json:"shipping_fee_vnd"`
	FinalShippingFeeVND        int64    `json:"final_shipping_fee_vnd"`
	FinalPayableVND            int64    `json:"final_payable_vnd"`
	AppliedShopProgramItemIDs  []string `json:"applied_shop_program_item_ids"`
}

type discountBreakdownDTO struct {
	Shops                           []*shopDiscountBreakdownDTO `json:"shops"`
	TotalSubtotalVND                int64                       `json:"total_subtotal_vnd"`
	TotalFlashSaleDiscountVND       int64                       `json:"total_flash_sale_discount_vnd"`
	TotalShopProgramDiscountVND     int64                       `json:"total_shop_program_discount_vnd"`
	TotalShopVoucherDiscountVND     int64                       `json:"total_shop_voucher_discount_vnd"`
	TotalShopFreeshipDiscountVND    int64                       `json:"total_shop_freeship_discount_vnd"`
	TotalPlatformVoucherDiscountVND int64                       `json:"total_platform_voucher_discount_vnd"`
	TotalFreeshipDiscountVND        int64                       `json:"total_freeship_discount_vnd"`
	TotalCoinsDiscountVND           int64                       `json:"total_coins_discount_vnd"`
	TotalDiscountVND                int64                       `json:"total_discount_vnd"`
	PlatformCostVND                 int64                       `json:"platform_cost_vnd"`
	ShopCostVND                     int64                       `json:"shop_cost_vnd"`
	TotalShippingFeeVND             int64                       `json:"total_shipping_fee_vnd"`
	FinalPayableVND                 int64                       `json:"final_payable_vnd"`
	Warnings                        []string                    `json:"warnings"`
}

func mapVoucher(v *promotionv1.Voucher) *voucherDTO {
	if v == nil {
		return nil
	}
	catIDs := v.GetCategoryIds()
	if catIDs == nil {
		catIDs = []string{}
	}
	return &voucherDTO{
		ID:               v.GetId(),
		Code:             v.GetCode(),
		Name:             v.GetName(),
		Scope:            voucherScopeString(v.GetScope()),
		ShopID:           v.GetShopId(),
		DiscountType:     discountTypeString(v.GetDiscountType()),
		ValueVND:         v.GetValueVnd(),
		DiscountPercent:  v.GetDiscountPercent(),
		MaxDiscountVND:   v.GetMaxDiscountVnd(),
		MinOrderVND:      v.GetMinOrderVnd(),
		Quota:            v.GetQuota(),
		UsedCount:        v.GetUsedCount(),
		RemainingQuota:   v.GetRemainingQuota(),
		PerUserLimit:     v.GetPerUserLimit(),
		BudgetVND:        v.GetBudgetVnd(),
		UsedBudgetVND:    v.GetUsedBudgetVnd(),
		StartsAt:         httpx.FormatTimestamp(v.GetStartsAt()),
		EndsAt:           httpx.FormatTimestamp(v.GetEndsAt()),
		IsActive:         v.GetIsActive(),
		Status:           voucherStatusString(v.GetStatus()),
		CategoryIDs:      catIDs,
		Saved:            v.GetSaved(),
		Eligible:         v.GetEligible(),
		IneligibleReason: v.GetIneligibleReason(),
	}
}

func mapFlashSaleItem(it *promotionv1.FlashSaleItem) *flashSaleItemDTO {
	if it == nil {
		return nil
	}
	return &flashSaleItemDTO{
		ID:               it.GetId(),
		FlashSaleID:      it.GetFlashSaleId(),
		ShopID:           it.GetShopId(),
		SKUID:            it.GetSkuId(),
		FlashPriceVND:    it.GetFlashPriceVnd(),
		OriginalPriceVND: it.GetOriginalPriceVnd(),
		TotalSlots:       it.GetTotalSlots(),
		RemainingSlots:   it.GetRemainingSlots(),
		PerUserLimit:     it.GetPerUserLimit(),
		Status:           flashSaleItemStatusString(it.GetStatus()),
		RejectionReason:  it.GetRejectionReason(),
		CreatedAt:        httpx.FormatTimestamp(it.GetCreatedAt()),
	}
}

func mapFlashSale(fs *promotionv1.FlashSale) *flashSaleDTO {
	if fs == nil {
		return nil
	}
	items := make([]*flashSaleItemDTO, 0, len(fs.GetItems()))
	for _, it := range fs.GetItems() {
		items = append(items, mapFlashSaleItem(it))
	}
	catIDs := fs.GetCategoryIds()
	if catIDs == nil {
		catIDs = []string{}
	}
	return &flashSaleDTO{
		ID:                 fs.GetId(),
		Name:               fs.GetName(),
		StartsAt:           httpx.FormatTimestamp(fs.GetStartsAt()),
		EndsAt:             httpx.FormatTimestamp(fs.GetEndsAt()),
		MinDiscountPercent: fs.GetMinDiscountPercent(),
		CategoryIDs:        catIDs,
		Items:              items,
	}
}

func mapShopDiscountProgram(p *promotionv1.ShopDiscountProgram) *shopDiscountProgramDTO {
	if p == nil {
		return nil
	}
	items := make([]*shopDiscountProgramItemDTO, 0, len(p.GetItems()))
	for _, it := range p.GetItems() {
		items = append(items, &shopDiscountProgramItemDTO{
			ID:                    it.GetId(),
			ProgramID:             it.GetProgramId(),
			ShopID:                it.GetShopId(),
			SKUID:                 it.GetSkuId(),
			OriginalPriceVND:      it.GetOriginalPriceVnd(),
			DiscountedPriceVND:    it.GetDiscountedPriceVnd(),
			DiscountPercent:       it.GetDiscountPercent(),
			PerUserLimit:          it.GetPerUserLimit(),
			PriceInflationFlagged: it.GetPriceInflationFlagged(),
			Warning:               it.GetWarning(),
		})
	}
	warnings := p.GetWarnings()
	if warnings == nil {
		warnings = []string{}
	}
	return &shopDiscountProgramDTO{
		ID:        p.GetId(),
		ShopID:    p.GetShopId(),
		Name:      p.GetName(),
		StartsAt:  httpx.FormatTimestamp(p.GetStartsAt()),
		EndsAt:    httpx.FormatTimestamp(p.GetEndsAt()),
		Status:    shopDiscountProgramStatusString(p.GetStatus()),
		Items:     items,
		Warnings:  warnings,
		CreatedAt: httpx.FormatTimestamp(p.GetCreatedAt()),
		UpdatedAt: httpx.FormatTimestamp(p.GetUpdatedAt()),
	}
}

func mapActiveShopDiscount(d *promotionv1.ActiveShopDiscount) *activeShopDiscountDTO {
	if d == nil {
		return nil
	}
	return &activeShopDiscountDTO{
		ItemID:             d.GetItemId(),
		ProgramID:          d.GetProgramId(),
		ShopID:             d.GetShopId(),
		SKUID:              d.GetSkuId(),
		OriginalPriceVND:   d.GetOriginalPriceVnd(),
		DiscountedPriceVND: d.GetDiscountedPriceVnd(),
		DiscountPercent:    d.GetDiscountPercent(),
		PerUserLimit:       d.GetPerUserLimit(),
		StartsAt:           httpx.FormatTimestamp(d.GetStartsAt()),
		EndsAt:             httpx.FormatTimestamp(d.GetEndsAt()),
	}
}

func mapDiscountBreakdown(b *promotionv1.DiscountBreakdown) *discountBreakdownDTO {
	if b == nil {
		return &discountBreakdownDTO{Shops: []*shopDiscountBreakdownDTO{}, Warnings: []string{}}
	}
	shops := make([]*shopDiscountBreakdownDTO, 0, len(b.GetShops()))
	for _, s := range b.GetShops() {
		appliedIDs := s.GetAppliedShopProgramItemIds()
		if appliedIDs == nil {
			appliedIDs = []string{}
		}
		shops = append(shops, &shopDiscountBreakdownDTO{
			ShopID:                     s.GetShopId(),
			SubtotalVND:                s.GetSubtotalVnd(),
			FlashSaleDiscountVND:       s.GetFlashSaleDiscountVnd(),
			ShopProgramDiscountVND:     s.GetShopProgramDiscountVnd(),
			ShopVoucherID:              s.GetShopVoucherId(),
			ShopVoucherDiscountVND:     s.GetShopVoucherDiscountVnd(),
			ShopFreeshipDiscountVND:    s.GetShopFreeshipDiscountVnd(),
			PlatformVoucherDiscountVND: s.GetPlatformVoucherDiscountVnd(),
			FreeshipDiscountVND:        s.GetFreeshipDiscountVnd(),
			CoinsDiscountVND:           s.GetCoinsDiscountVnd(),
			ShippingFeeVND:             s.GetShippingFeeVnd(),
			FinalShippingFeeVND:        s.GetFinalShippingFeeVnd(),
			FinalPayableVND:            s.GetFinalPayableVnd(),
			AppliedShopProgramItemIDs:  appliedIDs,
		})
	}
	warnings := b.GetWarnings()
	if warnings == nil {
		warnings = []string{}
	}
	return &discountBreakdownDTO{
		Shops:                           shops,
		TotalSubtotalVND:                b.GetTotalSubtotalVnd(),
		TotalFlashSaleDiscountVND:       b.GetTotalFlashSaleDiscountVnd(),
		TotalShopProgramDiscountVND:     b.GetTotalShopProgramDiscountVnd(),
		TotalShopVoucherDiscountVND:     b.GetTotalShopVoucherDiscountVnd(),
		TotalShopFreeshipDiscountVND:    b.GetTotalShopFreeshipDiscountVnd(),
		TotalPlatformVoucherDiscountVND: b.GetTotalPlatformVoucherDiscountVnd(),
		TotalFreeshipDiscountVND:        b.GetTotalFreeshipDiscountVnd(),
		TotalCoinsDiscountVND:           b.GetTotalCoinsDiscountVnd(),
		TotalDiscountVND:                b.GetTotalDiscountVnd(),
		PlatformCostVND:                 b.GetPlatformCostVnd(),
		ShopCostVND:                     b.GetShopCostVnd(),
		TotalShippingFeeVND:             b.GetTotalShippingFeeVnd(),
		FinalPayableVND:                 b.GetFinalPayableVnd(),
		Warnings:                        warnings,
	}
}

func voucherScopeString(s promotionv1.VoucherScope) string {
	switch s {
	case promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM:
		return "PLATFORM"
	case promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP:
		return "SHOP"
	case promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP:
		return "FREESHIP"
	default:
		return ""
	}
}

func parseVoucherScope(s string) promotionv1.VoucherScope {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PLATFORM", "VOUCHER_SCOPE_PLATFORM":
		return promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM
	case "SHOP", "VOUCHER_SCOPE_SHOP":
		return promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP
	case "FREESHIP", "VOUCHER_SCOPE_FREESHIP":
		return promotionv1.VoucherScope_VOUCHER_SCOPE_FREESHIP
	default:
		return promotionv1.VoucherScope_VOUCHER_SCOPE_UNSPECIFIED
	}
}

func discountTypeString(d promotionv1.DiscountType) string {
	switch d {
	case promotionv1.DiscountType_DISCOUNT_TYPE_FIXED:
		return "FIXED"
	case promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT:
		return "PERCENT"
	default:
		return ""
	}
}

func parseDiscountType(s string) promotionv1.DiscountType {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "FIXED", "DISCOUNT_TYPE_FIXED":
		return promotionv1.DiscountType_DISCOUNT_TYPE_FIXED
	case "PERCENT", "DISCOUNT_TYPE_PERCENT":
		return promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT
	default:
		return promotionv1.DiscountType_DISCOUNT_TYPE_UNSPECIFIED
	}
}

func flashSaleItemStatusString(st promotionv1.FlashSaleItemStatus) string {
	switch st {
	case promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING:
		return "PENDING"
	case promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED:
		return "APPROVED"
	case promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_REJECTED:
		return "REJECTED"
	default:
		return ""
	}
}

func parseFlashSaleItemStatus(s string) promotionv1.FlashSaleItemStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "FLASH_SALE_ITEM_STATUS_PENDING":
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_PENDING
	case "APPROVED", "FLASH_SALE_ITEM_STATUS_APPROVED":
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_APPROVED
	case "REJECTED", "FLASH_SALE_ITEM_STATUS_REJECTED":
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_REJECTED
	default:
		return promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_UNSPECIFIED
	}
}

func voucherStatusString(st promotionv1.VoucherStatus) string {
	switch st {
	case promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE:
		return "ACTIVE"
	case promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED:
		return "PAUSED"
	case promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED:
		return "DELETED"
	default:
		return ""
	}
}

func parseVoucherStatus(s string) promotionv1.VoucherStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ACTIVE", "VOUCHER_STATUS_ACTIVE":
		return promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE
	case "PAUSED", "VOUCHER_STATUS_PAUSED":
		return promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED
	case "DELETED", "VOUCHER_STATUS_DELETED":
		return promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED
	default:
		return promotionv1.VoucherStatus_VOUCHER_STATUS_UNSPECIFIED
	}
}

func shopDiscountProgramStatusString(st promotionv1.ShopDiscountProgramStatus) string {
	switch st {
	case promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_SCHEDULED:
		return "SCHEDULED"
	case promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE:
		return "ACTIVE"
	case promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ENDED:
		return "ENDED"
	default:
		return ""
	}
}

func parseShopDiscountProgramStatus(s string) promotionv1.ShopDiscountProgramStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "SCHEDULED", "SHOP_DISCOUNT_PROGRAM_STATUS_SCHEDULED":
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_SCHEDULED
	case "ACTIVE", "SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE":
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ACTIVE
	case "ENDED", "SHOP_DISCOUNT_PROGRAM_STATUS_ENDED":
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_ENDED
	default:
		return promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_UNSPECIFIED
	}
}

type createVoucherBody struct {
	Code            string   `json:"code"`
	Name            string   `json:"name"`
	Scope           string   `json:"scope"`
	ShopID          string   `json:"shop_id"`
	DiscountType    string   `json:"discount_type"`
	ValueVND        *int64   `json:"value_vnd"`
	DiscountPercent *int32   `json:"discount_percent"`
	MaxDiscountVND  int64    `json:"max_discount_vnd"`
	MinOrderVND     int64    `json:"min_order_vnd"`
	Quota           *int32   `json:"quota"`
	PerUserLimit    int32    `json:"per_user_limit"`
	BudgetVND       int64    `json:"budget_vnd"`
	StartsAt        string   `json:"starts_at"`
	EndsAt          string   `json:"ends_at"`
	CategoryIDs     []string `json:"category_ids"`
}

func validateCreateVoucherBody(b *createVoucherBody) (promotionv1.DiscountType, int64, int32, int32, string) {
	if b.Quota == nil || *b.Quota <= 0 {
		return 0, 0, 0, 0, "quota is required and must be > 0"
	}
	dt := parseDiscountType(b.DiscountType)
	var valueVND int64
	var discountPct int32
	switch dt {
	case promotionv1.DiscountType_DISCOUNT_TYPE_FIXED:
		if b.ValueVND == nil || *b.ValueVND <= 0 {
			return 0, 0, 0, 0, "value_vnd is required and must be > 0 for FIXED voucher"
		}
		valueVND = *b.ValueVND
	case promotionv1.DiscountType_DISCOUNT_TYPE_PERCENT:
		if b.DiscountPercent == nil || *b.DiscountPercent <= 0 || *b.DiscountPercent > 100 {
			return 0, 0, 0, 0, "discount_percent is required and must be between 1 and 100 for PERCENT voucher"
		}
		discountPct = *b.DiscountPercent
	default:
		return 0, 0, 0, 0, "discount_type must be FIXED or PERCENT"
	}
	if b.MaxDiscountVND < 0 || b.MinOrderVND < 0 || b.PerUserLimit < 0 || b.BudgetVND < 0 {
		return 0, 0, 0, 0, "voucher amounts and limits must be non-negative"
	}
	return dt, valueVND, discountPct, *b.Quota, ""
}

type updateVoucherBody struct {
	Name               *string  `json:"name"`
	DiscountType       *string  `json:"discount_type"`
	ValueVND           *int64   `json:"value_vnd"`
	DiscountPercent    *int32   `json:"discount_percent"`
	MaxDiscountVND     *int64   `json:"max_discount_vnd"`
	MinOrderVND        *int64   `json:"min_order_vnd"`
	Quota              *int32   `json:"quota"`
	PerUserLimit       *int32   `json:"per_user_limit"`
	BudgetVND          *int64   `json:"budget_vnd"`
	StartsAt           string   `json:"starts_at"`
	EndsAt             string   `json:"ends_at"`
	CategoryIDs        []string `json:"category_ids"`
	ReplaceCategoryIDs bool     `json:"replace_category_ids"`
}

type shopDiscountProgramItemInputBody struct {
	SKUID              string `json:"sku_id"`
	DiscountedPriceVND int64  `json:"discounted_price_vnd"`
	DiscountPercent    int32  `json:"discount_percent"`
	PerUserLimit       int32  `json:"per_user_limit"`
}

func toProtoShopDiscountProgramItems(in []shopDiscountProgramItemInputBody) []*promotionv1.ShopDiscountProgramItemInput {
	out := make([]*promotionv1.ShopDiscountProgramItemInput, 0, len(in))
	for _, it := range in {
		out = append(out, &promotionv1.ShopDiscountProgramItemInput{
			SkuId:              strings.TrimSpace(it.SKUID),
			DiscountedPriceVnd: it.DiscountedPriceVND,
			DiscountPercent:    it.DiscountPercent,
			PerUserLimit:       it.PerUserLimit,
		})
	}
	return out
}

func (h *handler) handleListVouchers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListAvailableVouchers(ctx, &promotionv1.ListAvailableVouchersRequest{
		UserId:         auth.UserIDFromContext(r.Context()),
		ShopId:         q.Get("shop_id"),
		Scope:          parseVoucherScope(q.Get("scope")),
		OrderAmountVnd: httpx.ParseInt64Query(r, "order_amount_vnd", 0),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	vouchers := make([]*voucherDTO, 0, len(resp.GetVouchers()))
	for _, v := range resp.GetVouchers() {
		vouchers = append(vouchers, mapVoucher(v))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"vouchers": vouchers})
}

func (h *handler) handleSaveVoucher(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.SaveVoucher(ctx, &promotionv1.SaveVoucherRequest{
		UserId:    auth.UserIDFromContext(r.Context()),
		VoucherId: chi.URLParam(r, "id"),
		Code:      body.Code,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"saved":   resp.GetSaved(),
		"voucher": mapVoucher(resp.GetVoucher()),
	})
}

func (h *handler) handleSellerListVouchers(w http.ResponseWriter, r *http.Request) {
	shopID := auth.ShopIDFromContext(r.Context())
	q := r.URL.Query()
	rawStatus := strings.TrimSpace(q.Get("status"))
	st := parseVoucherStatus(rawStatus)
	if rawStatus != "" && st == promotionv1.VoucherStatus_VOUCHER_STATUS_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be ACTIVE, PAUSED, or DELETED")
		return
	}
	includeInactive := httpx.ParseBoolQuery(r, "include_inactive")
	if !q.Has("include_inactive") && (st == promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED || st == promotionv1.VoucherStatus_VOUCHER_STATUS_DELETED) {
		includeInactive = true
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListAvailableVouchers(ctx, &promotionv1.ListAvailableVouchersRequest{
		ShopId:          shopID,
		Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		IncludeInactive: includeInactive,
		Status:          st,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	vouchers := make([]*voucherDTO, 0, len(resp.GetVouchers()))
	for _, v := range resp.GetVouchers() {
		if v.GetShopId() != shopID {
			continue
		}
		vouchers = append(vouchers, mapVoucher(v))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"vouchers": vouchers})
}

func (h *handler) handleUpdateVoucherCommon(w http.ResponseWriter, r *http.Request, allowCategoryScope bool) {
	var body updateVoucherBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	startsAt, err := httpx.ParseTimestamp(body.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid starts_at timestamp")
		return
	}
	endsAt, err := httpx.ParseTimestamp(body.EndsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid ends_at timestamp")
		return
	}

	req := &promotionv1.UpdateVoucherRequest{
		VoucherId:       chi.URLParam(r, "id"),
		Name:            body.Name,
		ValueVnd:        body.ValueVND,
		DiscountPercent: body.DiscountPercent,
		MaxDiscountVnd:  body.MaxDiscountVND,
		MinOrderVnd:     body.MinOrderVND,
		Quota:           body.Quota,
		PerUserLimit:    body.PerUserLimit,
		BudgetVnd:       body.BudgetVND,
		StartsAt:        startsAt,
		EndsAt:          endsAt,
	}
	if body.DiscountType != nil {
		dt := parseDiscountType(*body.DiscountType)
		if dt == promotionv1.DiscountType_DISCOUNT_TYPE_UNSPECIFIED {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "discount_type must be FIXED or PERCENT")
			return
		}
		req.DiscountType = &dt
	}
	if allowCategoryScope {
		req.CategoryIds = body.CategoryIDs
		req.ReplaceCategoryIds = body.ReplaceCategoryIDs
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.UpdateVoucher(ctx, req)
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"voucher": mapVoucher(resp.GetVoucher())})
}

func (h *handler) handleSellerUpdateVoucher(w http.ResponseWriter, r *http.Request) {
	h.handleUpdateVoucherCommon(w, r, false)
}

func (h *handler) handleAdminUpdateVoucher(w http.ResponseWriter, r *http.Request) {
	h.handleUpdateVoucherCommon(w, r, true)
}

func (h *handler) handleSetVoucherStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	st := parseVoucherStatus(body.Status)
	if st != promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE && st != promotionv1.VoucherStatus_VOUCHER_STATUS_PAUSED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be ACTIVE or PAUSED")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.SetVoucherStatus(ctx, &promotionv1.SetVoucherStatusRequest{
		VoucherId: chi.URLParam(r, "id"),
		Status:    st,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"voucher": mapVoucher(resp.GetVoucher())})
}

func (h *handler) handleDeleteVoucher(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.DeleteVoucher(ctx, &promotionv1.DeleteVoucherRequest{
		VoucherId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *handler) handleAdminListVouchers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawStatus := strings.TrimSpace(q.Get("status"))
	st := parseVoucherStatus(rawStatus)
	if rawStatus != "" && st == promotionv1.VoucherStatus_VOUCHER_STATUS_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be ACTIVE, PAUSED, or DELETED")
		return
	}
	includeInactive := httpx.ParseBoolQuery(r, "include_inactive")
	if !q.Has("include_inactive") && st != promotionv1.VoucherStatus_VOUCHER_STATUS_ACTIVE {
		includeInactive = true
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListAvailableVouchers(ctx, &promotionv1.ListAvailableVouchersRequest{
		Scope:           parseVoucherScope(q.Get("scope")),
		IncludeInactive: includeInactive,
		Status:          st,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	vouchers := make([]*voucherDTO, 0, len(resp.GetVouchers()))
	for _, v := range resp.GetVouchers() {
		vouchers = append(vouchers, mapVoucher(v))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"vouchers": vouchers})
}

func (h *handler) handleSellerCreateVoucher(w http.ResponseWriter, r *http.Request) {
	var body createVoucherBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	dt, valueVND, discountPct, quota, errMsg := validateCreateVoucherBody(&body)
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}
	startsAt, err := httpx.ParseTimestamp(body.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid starts_at timestamp")
		return
	}
	endsAt, err := httpx.ParseTimestamp(body.EndsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid ends_at timestamp")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.CreateVoucher(ctx, &promotionv1.CreateVoucherRequest{
		Code:            body.Code,
		Name:            body.Name,
		Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:          auth.ShopIDFromContext(r.Context()),
		DiscountType:    dt,
		ValueVnd:        valueVND,
		DiscountPercent: discountPct,
		MaxDiscountVnd:  body.MaxDiscountVND,
		MinOrderVnd:     body.MinOrderVND,
		Quota:           quota,
		PerUserLimit:    body.PerUserLimit,
		BudgetVnd:       body.BudgetVND,
		StartsAt:        startsAt,
		EndsAt:          endsAt,
		CreatedBy:       auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"voucher": mapVoucher(resp.GetVoucher())})
}

func (h *handler) handleAdminCreateVoucher(w http.ResponseWriter, r *http.Request) {
	var body createVoucherBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	dt, valueVND, discountPct, quota, errMsg := validateCreateVoucherBody(&body)
	if errMsg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", errMsg)
		return
	}
	startsAt, err := httpx.ParseTimestamp(body.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid starts_at timestamp")
		return
	}
	endsAt, err := httpx.ParseTimestamp(body.EndsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid ends_at timestamp")
		return
	}

	scope := parseVoucherScope(body.Scope)
	if scope == promotionv1.VoucherScope_VOUCHER_SCOPE_UNSPECIFIED {
		scope = promotionv1.VoucherScope_VOUCHER_SCOPE_PLATFORM
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.CreateVoucher(ctx, &promotionv1.CreateVoucherRequest{
		Code:            body.Code,
		Name:            body.Name,
		Scope:           scope,
		ShopId:          body.ShopID,
		DiscountType:    dt,
		ValueVnd:        valueVND,
		DiscountPercent: discountPct,
		MaxDiscountVnd:  body.MaxDiscountVND,
		MinOrderVnd:     body.MinOrderVND,
		Quota:           quota,
		PerUserLimit:    body.PerUserLimit,
		BudgetVnd:       body.BudgetVND,
		StartsAt:        startsAt,
		EndsAt:          endsAt,
		CreatedBy:       auth.UserIDFromContext(r.Context()),
		CategoryIds:     body.CategoryIDs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"voucher": mapVoucher(resp.GetVoucher())})
}

func (h *handler) handleListFlashSales(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListActiveFlashSales(ctx, &promotionv1.ListActiveFlashSalesRequest{
		IncludeUpcoming: httpx.ParseBoolQuery(r, "include_upcoming"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	sales := make([]*flashSaleDTO, 0, len(resp.GetFlashSales()))
	for _, fs := range resp.GetFlashSales() {
		sales = append(sales, mapFlashSale(fs))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"flash_sales": sales})
}

func (h *handler) handleAdminCreateFlashSale(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name               string   `json:"name"`
		StartsAt           string   `json:"starts_at"`
		EndsAt             string   `json:"ends_at"`
		MinDiscountPercent int32    `json:"min_discount_percent"`
		CategoryIDs        []string `json:"category_ids"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	startsAt, err := httpx.ParseTimestamp(body.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid starts_at timestamp")
		return
	}
	endsAt, err := httpx.ParseTimestamp(body.EndsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid ends_at timestamp")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.CreateFlashSale(ctx, &promotionv1.CreateFlashSaleRequest{
		Name:               body.Name,
		StartsAt:           startsAt,
		EndsAt:             endsAt,
		CreatedBy:          auth.UserIDFromContext(r.Context()),
		MinDiscountPercent: body.MinDiscountPercent,
		CategoryIds:        body.CategoryIDs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"flash_sale": mapFlashSale(resp.GetFlashSale())})
}

func (h *handler) handleListAllFlashSales(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	statusFilter := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("status")))
	if statusFilter != "" && statusFilter != "UPCOMING" && statusFilter != "ACTIVE" && statusFilter != "ENDED" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be UPCOMING, ACTIVE, or ENDED")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListAllFlashSales(ctx, &promotionv1.ListAllFlashSalesRequest{
		Status:   statusFilter,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*flashSaleDTO, 0, len(resp.GetFlashSales()))
	for _, fs := range resp.GetFlashSales() {
		items = append(items, mapFlashSale(fs))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleListFlashSaleItemsCommon(w http.ResponseWriter, r *http.Request, shopID string) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	rawStatus := strings.TrimSpace(q.Get("status"))
	st := parseFlashSaleItemStatus(rawStatus)
	if rawStatus != "" && st == promotionv1.FlashSaleItemStatus_FLASH_SALE_ITEM_STATUS_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be PENDING, APPROVED, or REJECTED")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListFlashSaleItems(ctx, &promotionv1.ListFlashSaleItemsRequest{
		Status:      st,
		FlashSaleId: strings.TrimSpace(q.Get("flash_sale_id")),
		ShopId:      shopID,
		Page:        page,
		PageSize:    pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*flashSaleItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, mapFlashSaleItem(it))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerListFlashSaleItems(w http.ResponseWriter, r *http.Request) {
	// Always force shop_id from verified seller JWT claims.
	h.handleListFlashSaleItemsCommon(w, r, auth.ShopIDFromContext(r.Context()))
}

func (h *handler) handleAdminListFlashSaleItems(w http.ResponseWriter, r *http.Request) {
	h.handleListFlashSaleItemsCommon(w, r, strings.TrimSpace(r.URL.Query().Get("shop_id")))
}

func (h *handler) handleListActiveShopDiscounts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var skuIDs []string
	for _, raw := range q["sku_ids"] {
		if strings.TrimSpace(raw) != "" {
			var skuErr string
			skuIDs, skuErr = parseSKUIDsQuery(q)
			if skuErr != "" {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", skuErr)
				return
			}
			break
		}
	}
	shopID := strings.TrimSpace(q.Get("shop_id"))
	if len(skuIDs) == 0 && shopID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "sku_ids or shop_id query parameter is required")
		return
	}

	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListActiveShopDiscounts(ctx, &promotionv1.ListActiveShopDiscountsRequest{
		SkuIds: skuIDs,
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	discounts := make([]*activeShopDiscountDTO, 0, len(resp.GetDiscounts()))
	for _, d := range resp.GetDiscounts() {
		discounts = append(discounts, mapActiveShopDiscount(d))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"discounts": discounts})
}

func (h *handler) handleSellerListDiscountPrograms(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	rawStatus := strings.TrimSpace(r.URL.Query().Get("status"))
	st := parseShopDiscountProgramStatus(rawStatus)
	if rawStatus != "" && st == promotionv1.ShopDiscountProgramStatus_SHOP_DISCOUNT_PROGRAM_STATUS_UNSPECIFIED {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "status must be SCHEDULED, ACTIVE, or ENDED")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListShopDiscountPrograms(ctx, &promotionv1.ListShopDiscountProgramsRequest{
		ShopId:   auth.ShopIDFromContext(r.Context()),
		Status:   st,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*shopDiscountProgramDTO, 0, len(resp.GetPrograms()))
	for _, p := range resp.GetPrograms() {
		items = append(items, mapShopDiscountProgram(p))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerCreateDiscountProgram(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string                             `json:"name"`
		StartsAt string                             `json:"starts_at"`
		EndsAt   string                             `json:"ends_at"`
		Items    []shopDiscountProgramItemInputBody `json:"items"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	startsAt, err := httpx.ParseTimestamp(body.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid starts_at timestamp")
		return
	}
	endsAt, err := httpx.ParseTimestamp(body.EndsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid ends_at timestamp")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.CreateShopDiscountProgram(ctx, &promotionv1.CreateShopDiscountProgramRequest{
		ShopId:   auth.ShopIDFromContext(r.Context()),
		Name:     body.Name,
		StartsAt: startsAt,
		EndsAt:   endsAt,
		Items:    toProtoShopDiscountProgramItems(body.Items),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	warnings := resp.GetWarnings()
	if warnings == nil {
		warnings = []string{}
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"program":  mapShopDiscountProgram(resp.GetProgram()),
		"warnings": warnings,
	})
}

func (h *handler) handleSellerUpdateDiscountProgram(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         *string                            `json:"name"`
		StartsAt     string                             `json:"starts_at"`
		EndsAt       string                             `json:"ends_at"`
		Items        []shopDiscountProgramItemInputBody `json:"items"`
		ReplaceItems bool                               `json:"replace_items"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	startsAt, err := httpx.ParseTimestamp(body.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid starts_at timestamp")
		return
	}
	endsAt, err := httpx.ParseTimestamp(body.EndsAt)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid ends_at timestamp")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.UpdateShopDiscountProgram(ctx, &promotionv1.UpdateShopDiscountProgramRequest{
		ProgramId:    chi.URLParam(r, "id"),
		Name:         body.Name,
		StartsAt:     startsAt,
		EndsAt:       endsAt,
		Items:        toProtoShopDiscountProgramItems(body.Items),
		ReplaceItems: body.ReplaceItems,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	warnings := resp.GetWarnings()
	if warnings == nil {
		warnings = []string{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"program":  mapShopDiscountProgram(resp.GetProgram()),
		"warnings": warnings,
	})
}

func (h *handler) handleSellerEndDiscountProgram(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.EndShopDiscountProgram(ctx, &promotionv1.EndShopDiscountProgramRequest{
		ProgramId: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"program": mapShopDiscountProgram(resp.GetProgram()),
	})
}

func (h *handler) handleSellerRegisterFlashSaleItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SKUID         string `json:"sku_id"`
		FlashPriceVND *int64 `json:"flash_price_vnd"`
		TotalSlots    *int32 `json:"total_slots"`
		PerUserLimit  int32  `json:"per_user_limit"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	skuID := strings.TrimSpace(body.SKUID)
	if skuID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "sku_id is required")
		return
	}
	if body.FlashPriceVND == nil || *body.FlashPriceVND <= 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "flash_price_vnd is required and must be > 0")
		return
	}
	if body.TotalSlots == nil || *body.TotalSlots <= 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "total_slots is required and must be > 0")
		return
	}
	if body.PerUserLimit < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "per_user_limit must be non-negative")
		return
	}
	if h.clients.Product == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "product service unavailable")
		return
	}

	shopID := auth.ShopIDFromContext(r.Context())
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	skuResp, err := h.clients.Product.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{
		SkuIds: []string{skuID},
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	var targetSKU *productv1.SKU
	for _, sk := range skuResp.GetSkus() {
		if sk.GetId() == skuID {
			targetSKU = sk
			break
		}
	}
	if targetSKU == nil {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "sku not found")
		return
	}
	if targetSKU.GetShopId() != shopID {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot register another shop's SKU for flash sale")
		return
	}

	resp, err := h.clients.Promotion.RegisterFlashSaleItem(ctx, &promotionv1.RegisterFlashSaleItemRequest{
		FlashSaleId:   chi.URLParam(r, "id"),
		ShopId:        shopID,
		SkuId:         skuID,
		FlashPriceVnd: *body.FlashPriceVND,
		TotalSlots:    *body.TotalSlots,
		PerUserLimit:  body.PerUserLimit,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"item": mapFlashSaleItem(resp.GetItem())})
}

func (h *handler) handleAdminApproveFlashSaleItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Approve *bool  `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.Approve == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "approve is required")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ApproveFlashSaleItem(ctx, &promotionv1.ApproveFlashSaleItemRequest{
		AdminId: auth.UserIDFromContext(r.Context()),
		ItemId:  chi.URLParam(r, "item_id"),
		Approve: *body.Approve,
		Reason:  body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"item": mapFlashSaleItem(resp.GetItem())})
}

func (h *handler) handleGetCoinBalance(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.GetCoinBalance(ctx, &promotionv1.GetCoinBalanceRequest{
		UserId: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	txs := make([]*coinTransactionDTO, 0, len(resp.GetRecentTransactions()))
	for _, tx := range resp.GetRecentTransactions() {
		txs = append(txs, &coinTransactionDTO{
			ID:          tx.GetId(),
			UserID:      tx.GetUserId(),
			Delta:       tx.GetDelta(),
			Reason:      tx.GetReason(),
			ReferenceID: tx.GetReferenceId(),
			CreatedAt:   httpx.FormatTimestamp(tx.GetCreatedAt()),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id":             resp.GetUserId(),
		"balance":             resp.GetBalance(),
		"recent_transactions": txs,
	})
}

func (h *handler) handleAdminCreditCoins(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UserID      string `json:"user_id"`
		Amount      *int64 `json:"amount"`
		Reason      string `json:"reason"`
		ReferenceID string `json:"reference_id"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.Amount == nil || *body.Amount <= 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "amount is required and must be > 0")
		return
	}
	refID := strings.TrimSpace(body.ReferenceID)
	if refID == "" {
		refID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.CreditCoins(ctx, &promotionv1.CreditCoinsRequest{
		UserId:      body.UserID,
		Amount:      *body.Amount,
		Reason:      body.Reason,
		ReferenceId: refID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"user_id":          resp.GetUserId(),
		"balance":          resp.GetBalance(),
		"already_credited": resp.GetAlreadyCredited(),
	})
}

func (h *handler) handlePreviewDiscounts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ShopItems []struct {
			ShopID         string `json:"shop_id"`
			ShippingFeeVND int64  `json:"shipping_fee_vnd"`
			Items          []struct {
				SKUID           string `json:"sku_id"`
				Quantity        int32  `json:"quantity"`
				FlashSaleItemID string `json:"flash_sale_item_id"`
			} `json:"items"`
		} `json:"shop_items"`
		ShopVoucherIDs    map[string]string `json:"shop_voucher_ids"`
		PlatformVoucherID string            `json:"platform_voucher_id"`
		FreeshipVoucherID string            `json:"freeship_voucher_id"`
		CoinsToUse        int64             `json:"coins_to_use"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if len(body.ShopItems) == 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shop_items is required")
		return
	}
	if body.CoinsToUse < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "coins_to_use must be non-negative")
		return
	}
	if h.clients.Product == nil || h.clients.Shipping == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "required upstream service unavailable")
		return
	}

	const maxPreviewLineItems = 200
	skuSet := make(map[string]struct{})
	totalLines := 0
	for _, s := range body.ShopItems {
		if s.ShippingFeeVND < 0 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shipping_fee_vnd must be non-negative")
			return
		}
		if len(s.Items) == 0 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "each shop_items entry must contain at least one item")
			return
		}
		for _, it := range s.Items {
			totalLines++
			if totalLines > maxPreviewLineItems {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "preview items exceed maximum limit of 200")
				return
			}
			skuID := strings.TrimSpace(it.SKUID)
			if skuID == "" {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "sku_id is required for every preview item")
				return
			}
			if it.Quantity <= 0 {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "quantity must be > 0 for every preview item")
				return
			}
			skuSet[skuID] = struct{}{}
			if len(skuSet) > maxPreviewLineItems {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "preview SKUs exceed maximum limit of 200")
				return
			}
		}
	}

	skuIDs := make([]string, 0, len(skuSet))
	for id := range skuSet {
		skuIDs = append(skuIDs, id)
	}
	sort.Strings(skuIDs)

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	// 1. Resolve authoritative SKU shop ownership and active status from ProductService
	// (SKU price and category are resolved authoritatively in PromotionService under D4).
	skuResp, err := h.clients.Product.GetSKUsBatch(ctx, &productv1.GetSKUsBatchRequest{SkuIds: skuIDs})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	skuByID := make(map[string]*productv1.SKU, len(skuResp.GetSkus()))
	for _, sk := range skuResp.GetSkus() {
		skuByID[sk.GetId()] = sk
	}

	// 2. Validate SKU ownership/active status (uniform 400 to prevent SKU existence/ownership oracle;
	// explicit 409 SKU_INACTIVE when matching shop_id has an inactive SKU) and group by authoritative shop_id.
	// Price, subtotal, category, and active shop discount programs are resolved authoritatively inside
	// PromotionService.PreviewDiscounts (D4).
	const invalidPreviewSKUMsg = "one or more SKUs are invalid for this shop"
	shopOrder := make([]string, 0, len(body.ShopItems))
	shopInputByID := make(map[string]*promotionv1.ShopDiscountInput, len(body.ShopItems))
	for _, s := range body.ShopItems {
		claimedShop := strings.TrimSpace(s.ShopID)
		shippingAssigned := false
		for _, it := range s.Items {
			skuID := strings.TrimSpace(it.SKUID)
			sk, ok := skuByID[skuID]
			if !ok || sk == nil {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", invalidPreviewSKUMsg)
				return
			}
			authShopID := strings.TrimSpace(sk.GetShopId())
			if authShopID == "" || (claimedShop != "" && claimedShop != authShopID) {
				httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", invalidPreviewSKUMsg)
				return
			}
			if !sk.GetIsActive() {
				if claimedShop == "" {
					httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", invalidPreviewSKUMsg)
					return
				}
				httpx.WriteError(w, r, http.StatusConflict, "SKU_INACTIVE", "sku "+skuID+" is not active")
				return
			}
			grp, exists := shopInputByID[authShopID]
			if !exists {
				grp = &promotionv1.ShopDiscountInput{
					ShopId: authShopID,
				}
				shopInputByID[authShopID] = grp
				shopOrder = append(shopOrder, authShopID)
			}
			if !shippingAssigned {
				if grp.ShippingFeeVnd > math.MaxInt64-s.ShippingFeeVND {
					httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "shipping_fee_vnd overflow")
					return
				}
				grp.ShippingFeeVnd += s.ShippingFeeVND
				shippingAssigned = true
			}
			grp.Items = append(grp.Items, &promotionv1.DiscountLineItem{
				SkuId:           skuID,
				Quantity:        it.Quantity,
				FlashSaleItemId: strings.TrimSpace(it.FlashSaleItemID),
			})
		}
	}

	// 3. Resolve authoritative shop freeship settings from ShippingService using the buyer's verified JWT.
	// Authenticated non-owner callers receive only shop_id + free_ship_* fields (with defaults when unconfigured).
	shopInputs := make([]*promotionv1.ShopDiscountInput, 0, len(shopOrder))
	for _, shopID := range shopOrder {
		grp := shopInputByID[shopID]
		st, err := h.fetchShopShippingSettings(ctx, shopID)
		if err != nil {
			httpx.WriteGRPCError(w, r, err)
			return
		}
		if st != nil {
			grp.FreeShipEnabled = st.GetFreeShipEnabled()
			grp.FreeShipMinOrderVnd = st.GetFreeShipMinOrderVnd()
			grp.FreeShipMaxVnd = st.GetFreeShipMaxVnd()
		}
		shopInputs = append(shopInputs, grp)
	}

	resp, err := h.clients.Promotion.PreviewDiscounts(ctx, &promotionv1.PreviewDiscountsRequest{
		UserId:            auth.UserIDFromContext(r.Context()),
		ShopItems:         shopInputs,
		ShopVoucherIds:    body.ShopVoucherIDs,
		PlatformVoucherId: body.PlatformVoucherID,
		FreeshipVoucherId: body.FreeshipVoucherID,
		CoinsToUse:        body.CoinsToUse,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.FailedPrecondition && strings.Contains(st.Message(), "SKU_INACTIVE") {
			httpx.WriteError(w, r, http.StatusConflict, "SKU_INACTIVE", st.Message())
			return
		}
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"breakdown": mapDiscountBreakdown(resp.GetBreakdown())})
}
