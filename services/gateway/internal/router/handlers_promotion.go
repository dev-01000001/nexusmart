package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	promotionv1 "github.com/nexusmart/core/gen/go/nexusmart/promotion/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type voucherDTO struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	Scope            string `json:"scope"`
	ShopID           string `json:"shop_id"`
	DiscountType     string `json:"discount_type"`
	ValueVND         int64  `json:"value_vnd"`
	DiscountPercent  int32  `json:"discount_percent"`
	MaxDiscountVND   int64  `json:"max_discount_vnd"`
	MinOrderVND      int64  `json:"min_order_vnd"`
	Quota            int32  `json:"quota"`
	UsedCount        int32  `json:"used_count"`
	RemainingQuota   int32  `json:"remaining_quota"`
	PerUserLimit     int32  `json:"per_user_limit"`
	BudgetVND        int64  `json:"budget_vnd"`
	UsedBudgetVND    int64  `json:"used_budget_vnd"`
	StartsAt         string `json:"starts_at,omitempty"`
	EndsAt           string `json:"ends_at,omitempty"`
	IsActive         bool   `json:"is_active"`
	Saved            bool   `json:"saved"`
	Eligible         bool   `json:"eligible"`
	IneligibleReason string `json:"ineligible_reason"`
}

type flashSaleItemDTO struct {
	ID              string `json:"id"`
	FlashSaleID     string `json:"flash_sale_id"`
	ShopID          string `json:"shop_id"`
	SKUID           string `json:"sku_id"`
	FlashPriceVND   int64  `json:"flash_price_vnd"`
	TotalSlots      int32  `json:"total_slots"`
	RemainingSlots  int32  `json:"remaining_slots"`
	PerUserLimit    int32  `json:"per_user_limit"`
	Status          string `json:"status"`
	RejectionReason string `json:"rejection_reason"`
}

type flashSaleDTO struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	StartsAt string              `json:"starts_at,omitempty"`
	EndsAt   string              `json:"ends_at,omitempty"`
	Items    []*flashSaleItemDTO `json:"items"`
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
	ShopID                     string `json:"shop_id"`
	SubtotalVND                int64  `json:"subtotal_vnd"`
	FlashSaleDiscountVND       int64  `json:"flash_sale_discount_vnd"`
	ShopVoucherID              string `json:"shop_voucher_id"`
	ShopVoucherDiscountVND     int64  `json:"shop_voucher_discount_vnd"`
	PlatformVoucherDiscountVND int64  `json:"platform_voucher_discount_vnd"`
	FreeshipDiscountVND        int64  `json:"freeship_discount_vnd"`
	CoinsDiscountVND           int64  `json:"coins_discount_vnd"`
	ShippingFeeVND             int64  `json:"shipping_fee_vnd"`
	FinalShippingFeeVND        int64  `json:"final_shipping_fee_vnd"`
	FinalPayableVND            int64  `json:"final_payable_vnd"`
}

type discountBreakdownDTO struct {
	Shops                           []*shopDiscountBreakdownDTO `json:"shops"`
	TotalSubtotalVND                int64                       `json:"total_subtotal_vnd"`
	TotalFlashSaleDiscountVND       int64                       `json:"total_flash_sale_discount_vnd"`
	TotalShopVoucherDiscountVND     int64                       `json:"total_shop_voucher_discount_vnd"`
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
		ID:              it.GetId(),
		FlashSaleID:     it.GetFlashSaleId(),
		ShopID:          it.GetShopId(),
		SKUID:           it.GetSkuId(),
		FlashPriceVND:   it.GetFlashPriceVnd(),
		TotalSlots:      it.GetTotalSlots(),
		RemainingSlots:  it.GetRemainingSlots(),
		PerUserLimit:    it.GetPerUserLimit(),
		Status:          flashSaleItemStatusString(it.GetStatus()),
		RejectionReason: it.GetRejectionReason(),
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
	return &flashSaleDTO{
		ID:       fs.GetId(),
		Name:     fs.GetName(),
		StartsAt: httpx.FormatTimestamp(fs.GetStartsAt()),
		EndsAt:   httpx.FormatTimestamp(fs.GetEndsAt()),
		Items:    items,
	}
}

func mapDiscountBreakdown(b *promotionv1.DiscountBreakdown) *discountBreakdownDTO {
	if b == nil {
		return &discountBreakdownDTO{Shops: []*shopDiscountBreakdownDTO{}, Warnings: []string{}}
	}
	shops := make([]*shopDiscountBreakdownDTO, 0, len(b.GetShops()))
	for _, s := range b.GetShops() {
		shops = append(shops, &shopDiscountBreakdownDTO{
			ShopID:                     s.GetShopId(),
			SubtotalVND:                s.GetSubtotalVnd(),
			FlashSaleDiscountVND:       s.GetFlashSaleDiscountVnd(),
			ShopVoucherID:              s.GetShopVoucherId(),
			ShopVoucherDiscountVND:     s.GetShopVoucherDiscountVnd(),
			PlatformVoucherDiscountVND: s.GetPlatformVoucherDiscountVnd(),
			FreeshipDiscountVND:        s.GetFreeshipDiscountVnd(),
			CoinsDiscountVND:           s.GetCoinsDiscountVnd(),
			ShippingFeeVND:             s.GetShippingFeeVnd(),
			FinalShippingFeeVND:        s.GetFinalShippingFeeVnd(),
			FinalPayableVND:            s.GetFinalPayableVnd(),
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
		TotalShopVoucherDiscountVND:     b.GetTotalShopVoucherDiscountVnd(),
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

type createVoucherBody struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	Scope           string `json:"scope"`
	ShopID          string `json:"shop_id"`
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
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ListAvailableVouchers(ctx, &promotionv1.ListAvailableVouchersRequest{
		ShopId: shopID,
		Scope:  promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
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

func (h *handler) handleSellerCreateVoucher(w http.ResponseWriter, r *http.Request) {
	var body createVoucherBody
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

	resp, err := h.clients.Promotion.CreateVoucher(ctx, &promotionv1.CreateVoucherRequest{
		Code:            body.Code,
		Name:            body.Name,
		Scope:           promotionv1.VoucherScope_VOUCHER_SCOPE_SHOP,
		ShopId:          auth.ShopIDFromContext(r.Context()),
		DiscountType:    parseDiscountType(body.DiscountType),
		ValueVnd:        body.ValueVND,
		DiscountPercent: body.DiscountPercent,
		MaxDiscountVnd:  body.MaxDiscountVND,
		MinOrderVnd:     body.MinOrderVND,
		Quota:           body.Quota,
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
		DiscountType:    parseDiscountType(body.DiscountType),
		ValueVnd:        body.ValueVND,
		DiscountPercent: body.DiscountPercent,
		MaxDiscountVnd:  body.MaxDiscountVND,
		MinOrderVnd:     body.MinOrderVND,
		Quota:           body.Quota,
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
		Name     string `json:"name"`
		StartsAt string `json:"starts_at"`
		EndsAt   string `json:"ends_at"`
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
		Name:      body.Name,
		StartsAt:  startsAt,
		EndsAt:    endsAt,
		CreatedBy: auth.UserIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"flash_sale": mapFlashSale(resp.GetFlashSale())})
}

func (h *handler) handleSellerRegisterFlashSaleItem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SKUID         string `json:"sku_id"`
		FlashPriceVND int64  `json:"flash_price_vnd"`
		TotalSlots    int32  `json:"total_slots"`
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
		FlashPriceVnd: body.FlashPriceVND,
		TotalSlots:    body.TotalSlots,
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
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.ApproveFlashSaleItem(ctx, &promotionv1.ApproveFlashSaleItemRequest{
		AdminId: auth.UserIDFromContext(r.Context()),
		ItemId:  chi.URLParam(r, "item_id"),
		Approve: body.Approve,
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
		Amount      int64  `json:"amount"`
		Reason      string `json:"reason"`
		ReferenceID string `json:"reference_id"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.CreditCoins(ctx, &promotionv1.CreditCoinsRequest{
		UserId:      body.UserID,
		Amount:      body.Amount,
		Reason:      body.Reason,
		ReferenceId: body.ReferenceID,
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
			SubtotalVND    int64  `json:"subtotal_vnd"`
			ShippingFeeVND int64  `json:"shipping_fee_vnd"`
			Items          []struct {
				SKUID           string `json:"sku_id"`
				Quantity        int32  `json:"quantity"`
				UnitPriceVND    int64  `json:"unit_price_vnd"`
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

	shopInputs := make([]*promotionv1.ShopDiscountInput, 0, len(body.ShopItems))
	for _, s := range body.ShopItems {
		lineItems := make([]*promotionv1.DiscountLineItem, 0, len(s.Items))
		for _, it := range s.Items {
			lineItems = append(lineItems, &promotionv1.DiscountLineItem{
				SkuId:           it.SKUID,
				Quantity:        it.Quantity,
				UnitPriceVnd:    it.UnitPriceVND,
				FlashSaleItemId: it.FlashSaleItemID,
			})
		}
		shopInputs = append(shopInputs, &promotionv1.ShopDiscountInput{
			ShopId:         s.ShopID,
			SubtotalVnd:    s.SubtotalVND,
			ShippingFeeVnd: s.ShippingFeeVND,
			Items:          lineItems,
		})
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Promotion.PreviewDiscounts(ctx, &promotionv1.PreviewDiscountsRequest{
		UserId:            auth.UserIDFromContext(r.Context()),
		ShopItems:         shopInputs,
		ShopVoucherIds:    body.ShopVoucherIDs,
		PlatformVoucherId: body.PlatformVoucherID,
		FreeshipVoucherId: body.FreeshipVoucherID,
		CoinsToUse:        body.CoinsToUse,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"breakdown": mapDiscountBreakdown(resp.GetBreakdown())})
}
