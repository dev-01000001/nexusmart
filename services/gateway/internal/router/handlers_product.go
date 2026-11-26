package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type categoryDTO struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	ParentID          string `json:"parent_id"`
	CommissionRateBps *int32 `json:"commission_rate_bps,omitempty"`
	SortOrder         int32  `json:"sort_order"`
	IsProhibited      bool   `json:"is_prohibited"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

type categoryFeeScheduleDTO struct {
	ID                string `json:"id"`
	CategoryID        string `json:"category_id"`
	CommissionRateBps int32  `json:"commission_rate_bps"`
	EffectiveFrom     string `json:"effective_from,omitempty"`
	CreatedBy         string `json:"created_by"`
	Note              string `json:"note"`
	CreatedAt         string `json:"created_at,omitempty"`
}

func mapCategoryFeeSchedule(s *productv1.CategoryFeeSchedule) *categoryFeeScheduleDTO {
	if s == nil {
		return nil
	}
	return &categoryFeeScheduleDTO{
		ID:                s.GetId(),
		CategoryID:        s.GetCategoryId(),
		CommissionRateBps: s.GetCommissionRateBps(),
		EffectiveFrom:     httpx.FormatTimestamp(s.GetEffectiveFrom()),
		CreatedBy:         s.GetCreatedBy(),
		Note:              s.GetNote(),
		CreatedAt:         httpx.FormatTimestamp(s.GetCreatedAt()),
	}
}

type skuDTO struct {
	ID                     string            `json:"id"`
	ProductID              string            `json:"product_id"`
	ShopID                 string            `json:"shop_id"`
	ProductName            string            `json:"product_name"`
	SKUCode                string            `json:"sku_code"`
	Name                   string            `json:"name"`
	Attributes             map[string]string `json:"attributes"`
	PriceVND               int64             `json:"price_vnd"`
	MinPrice30dVND         int64             `json:"min_price_30d_vnd"`
	Stock                  int32             `json:"stock"`
	ReservedStock          int32             `json:"reserved_stock"`
	FlashSaleReservedStock int32             `json:"flash_sale_reserved_stock"`
	AvailableStock         int32             `json:"available_stock"`
	Version                int64             `json:"version"`
	WeightGrams            int32             `json:"weight_grams"`
	ImageURL               string            `json:"image_url"`
	CategoryID             string            `json:"category_id"`
	CommissionRateBps      *int32            `json:"commission_rate_bps,omitempty"`
	IsActive               bool              `json:"is_active"`
}

type productDTO struct {
	ID              string    `json:"id"`
	ShopID          string    `json:"shop_id"`
	CategoryID      string    `json:"category_id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	Description     string    `json:"description"`
	Images          []string  `json:"images"`
	Status          string    `json:"status"`
	RejectionReason string    `json:"rejection_reason"`
	MinPriceVND     int64     `json:"min_price_vnd"`
	MaxPriceVND     int64     `json:"max_price_vnd"`
	RatingAvgX100   int32     `json:"rating_avg_x100"`
	ReviewCount     int32     `json:"review_count"`
	FlaggedKeywords *[]string `json:"flagged_keywords,omitempty"`
	SKUs            []*skuDTO `json:"skus"`
	CreatedAt       string    `json:"created_at,omitempty"`
	UpdatedAt       string    `json:"updated_at,omitempty"`
}

type skuInputBody struct {
	ID          string            `json:"id"`
	SKUCode     string            `json:"sku_code"`
	Name        string            `json:"name"`
	Attributes  map[string]string `json:"attributes"`
	PriceVND    int64             `json:"price_vnd"`
	Stock       int32             `json:"stock"`
	WeightGrams int32             `json:"weight_grams"`
	ImageURL    string            `json:"image_url"`
}

func int32Ptr(v int32) *int32 {
	return &v
}

func mapCategory(c *productv1.Category) *categoryDTO {
	if c == nil {
		return nil
	}
	return &categoryDTO{
		ID:                c.GetId(),
		Name:              c.GetName(),
		Slug:              c.GetSlug(),
		ParentID:          c.GetParentId(),
		CommissionRateBps: int32Ptr(c.GetCommissionRateBps()),
		SortOrder:         c.GetSortOrder(),
		IsProhibited:      c.GetIsProhibited(),
		CreatedAt:         httpx.FormatTimestamp(c.GetCreatedAt()),
		UpdatedAt:         httpx.FormatTimestamp(c.GetUpdatedAt()),
	}
}

func mapPublicCategory(c *productv1.Category) *categoryDTO {
	dto := mapCategory(c)
	if dto != nil {
		dto.CommissionRateBps = nil
	}
	return dto
}

func mapSKU(s *productv1.SKU) *skuDTO {
	if s == nil {
		return nil
	}
	attrs := s.GetAttributes()
	if attrs == nil {
		attrs = map[string]string{}
	}
	return &skuDTO{
		ID:                     s.GetId(),
		ProductID:              s.GetProductId(),
		ShopID:                 s.GetShopId(),
		ProductName:            s.GetProductName(),
		SKUCode:                s.GetSkuCode(),
		Name:                   s.GetName(),
		Attributes:             attrs,
		PriceVND:               s.GetPriceVnd(),
		MinPrice30dVND:         s.GetMinPrice_30DVnd(),
		Stock:                  s.GetStock(),
		ReservedStock:          s.GetReservedStock(),
		FlashSaleReservedStock: s.GetFlashSaleReservedStock(),
		AvailableStock:         s.GetAvailableStock(),
		Version:                s.GetVersion(),
		WeightGrams:            s.GetWeightGrams(),
		ImageURL:               s.GetImageUrl(),
		CategoryID:             s.GetCategoryId(),
		CommissionRateBps:      int32Ptr(s.GetCommissionRateBps()),
		IsActive:               s.GetIsActive(),
	}
}

func mapProduct(p *productv1.Product) *productDTO {
	if p == nil {
		return nil
	}
	imgs := p.GetImages()
	if imgs == nil {
		imgs = []string{}
	}
	skus := make([]*skuDTO, 0, len(p.GetSkus()))
	for _, s := range p.GetSkus() {
		skus = append(skus, mapSKU(s))
	}
	flagged := p.GetFlaggedKeywords()
	if flagged == nil {
		flagged = []string{}
	}
	return &productDTO{
		ID:              p.GetId(),
		ShopID:          p.GetShopId(),
		CategoryID:      p.GetCategoryId(),
		Name:            p.GetName(),
		Slug:            p.GetSlug(),
		Description:     p.GetDescription(),
		Images:          imgs,
		Status:          productStatusString(p.GetStatus()),
		RejectionReason: p.GetRejectionReason(),
		MinPriceVND:     p.GetMinPriceVnd(),
		MaxPriceVND:     p.GetMaxPriceVnd(),
		RatingAvgX100:   p.GetRatingAvgX100(),
		ReviewCount:     p.GetReviewCount(),
		FlaggedKeywords: &flagged,
		SKUs:            skus,
		CreatedAt:       httpx.FormatTimestamp(p.GetCreatedAt()),
		UpdatedAt:       httpx.FormatTimestamp(p.GetUpdatedAt()),
	}
}

func mapPublicProduct(p *productv1.Product) *productDTO {
	dto := mapProduct(p)
	if dto != nil {
		dto.FlaggedKeywords = nil
		dto.RejectionReason = ""
		for _, sk := range dto.SKUs {
			if sk != nil {
				sk.CommissionRateBps = nil
			}
		}
	}
	return dto
}

func toProtoSKUInputs(in []skuInputBody) []*productv1.SKUInput {
	out := make([]*productv1.SKUInput, 0, len(in))
	for _, s := range in {
		out = append(out, &productv1.SKUInput{
			Id:          s.ID,
			SkuCode:     s.SKUCode,
			Name:        s.Name,
			Attributes:  s.Attributes,
			PriceVnd:    s.PriceVND,
			Stock:       s.Stock,
			WeightGrams: s.WeightGrams,
			ImageUrl:    s.ImageURL,
		})
	}
	return out
}

func productStatusString(st productv1.ProductStatus) string {
	switch st {
	case productv1.ProductStatus_PRODUCT_STATUS_PENDING:
		return "PENDING"
	case productv1.ProductStatus_PRODUCT_STATUS_APPROVED:
		return "APPROVED"
	case productv1.ProductStatus_PRODUCT_STATUS_REJECTED:
		return "REJECTED"
	case productv1.ProductStatus_PRODUCT_STATUS_HIDDEN:
		return "HIDDEN"
	case productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK:
		return "OUT_OF_STOCK"
	default:
		return ""
	}
}

func parseProductStatus(s string) productv1.ProductStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "PRODUCT_STATUS_PENDING":
		return productv1.ProductStatus_PRODUCT_STATUS_PENDING
	case "APPROVED", "PRODUCT_STATUS_APPROVED":
		return productv1.ProductStatus_PRODUCT_STATUS_APPROVED
	case "REJECTED", "PRODUCT_STATUS_REJECTED":
		return productv1.ProductStatus_PRODUCT_STATUS_REJECTED
	case "HIDDEN", "PRODUCT_STATUS_HIDDEN":
		return productv1.ProductStatus_PRODUCT_STATUS_HIDDEN
	case "OUT_OF_STOCK", "PRODUCT_STATUS_OUT_OF_STOCK":
		return productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK
	default:
		return productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED
	}
}

func parseProductReviewDecision(s string) productv1.ProductReviewDecision {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "APPROVE", "PRODUCT_REVIEW_DECISION_APPROVE":
		return productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_APPROVE
	case "REJECT", "PRODUCT_REVIEW_DECISION_REJECT":
		return productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_REJECT
	default:
		return productv1.ProductReviewDecision_PRODUCT_REVIEW_DECISION_UNSPECIFIED
	}
}

func (h *handler) handleListCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListCategories(ctx, &productv1.ListCategoriesRequest{
		ParentId: r.URL.Query().Get("parent_id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	cats := make([]*categoryDTO, 0, len(resp.GetCategories()))
	for _, c := range resp.GetCategories() {
		cats = append(cats, mapPublicCategory(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

func (h *handler) handleListCategoriesWithCommission(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListCategories(ctx, &productv1.ListCategoriesRequest{
		ParentId: r.URL.Query().Get("parent_id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	cats := make([]*categoryDTO, 0, len(resp.GetCategories()))
	for _, c := range resp.GetCategories() {
		cats = append(cats, mapCategory(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

func (h *handler) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name              string `json:"name"`
		Slug              string `json:"slug"`
		ParentID          string `json:"parent_id"`
		CommissionRateBps int32  `json:"commission_rate_bps"`
		SortOrder         int32  `json:"sort_order"`
		IsProhibited      bool   `json:"is_prohibited"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.CreateCategory(ctx, &productv1.CreateCategoryRequest{
		Name:              body.Name,
		Slug:              body.Slug,
		ParentId:          body.ParentID,
		CommissionRateBps: body.CommissionRateBps,
		SortOrder:         body.SortOrder,
		IsProhibited:      body.IsProhibited,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"category": mapCategory(resp.GetCategory())})
}

func (h *handler) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name              string  `json:"name"`
		Slug              string  `json:"slug"`
		ParentID          *string `json:"parent_id"`
		CommissionRateBps *int32  `json:"commission_rate_bps"`
		SortOrder         *int32  `json:"sort_order"`
		IsProhibited      *bool   `json:"is_prohibited"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	req := &productv1.UpdateCategoryRequest{
		Id:   chi.URLParam(r, "id"),
		Name: body.Name,
		Slug: body.Slug,
	}
	if body.ParentID != nil {
		req.ParentId = *body.ParentID
		req.HasParentId = true
	}
	if body.CommissionRateBps != nil {
		req.CommissionRateBps = *body.CommissionRateBps
		req.HasCommissionRateBps = true
	}
	if body.SortOrder != nil {
		req.SortOrder = *body.SortOrder
		req.HasSortOrder = true
	}
	if body.IsProhibited != nil {
		req.IsProhibited = *body.IsProhibited
		req.HasIsProhibited = true
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.UpdateCategory(ctx, req)
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"category": mapCategory(resp.GetCategory())})
}

func (h *handler) handleAdminListCategoryFeeSchedules(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListCategoryFeeSchedules(ctx, &productv1.ListCategoryFeeSchedulesRequest{
		CategoryId: strings.TrimSpace(r.URL.Query().Get("category_id")),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*categoryFeeScheduleDTO, 0, len(resp.GetSchedules()))
	for _, s := range resp.GetSchedules() {
		items = append(items, mapCategoryFeeSchedule(s))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminSetCategoryFeeSchedule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CommissionRateBps *int32 `json:"commission_rate_bps"`
		EffectiveFrom     string `json:"effective_from"`
		Note              string `json:"note"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.CommissionRateBps == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "commission_rate_bps is required")
		return
	}
	if *body.CommissionRateBps < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "commission_rate_bps must be non-negative")
		return
	}
	effFrom, err := httpx.ParseTimestamp(body.EffectiveFrom)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid effective_from timestamp")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.SetCategoryFeeSchedule(ctx, &productv1.SetCategoryFeeScheduleRequest{
		CategoryId:        chi.URLParam(r, "id"),
		CommissionRateBps: *body.CommissionRateBps,
		EffectiveFrom:     effFrom,
		Note:              body.Note,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"schedule": mapCategoryFeeSchedule(resp.GetSchedule()),
	})
}

func (h *handler) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.DeleteCategory(ctx, &productv1.DeleteCategoryRequest{
		Id: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func normalizeProductSortQuery(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "price-asc":
		return "price_asc"
	case "price-desc":
		return "price_desc"
	case "name-asc":
		return "name_asc"
	default:
		return strings.TrimSpace(s)
	}
}

func writeEmptyProductPage(w http.ResponseWriter, page, pageSize int32) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     []*productDTO{},
		"page":      page,
		"page_size": pageSize,
		"total":     int64(0),
	})
}

func isPublicProductStatus(st productv1.ProductStatus) bool {
	return st == productv1.ProductStatus_PRODUCT_STATUS_APPROVED ||
		st == productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK
}

const (
	maxSKUIDsQuery   = 200
	maxSKUIDTokenLen = 128
)

func isValidSKUIDToken(id string) bool {
	if id == "" || len(id) > maxSKUIDTokenLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// parseSKUIDsQuery parses and validates comma-separated / repeated sku_ids query parameters
// (max 200 raw entries, non-empty ASCII token elements, deduplicated preserving order).
func parseSKUIDsQuery(q url.Values) ([]string, string) {
	if !q.Has("sku_ids") {
		return nil, ""
	}
	var (
		skuIDs   []string
		seen     = make(map[string]struct{})
		rawCount int
	)
	for _, raw := range q["sku_ids"] {
		for _, part := range strings.Split(raw, ",") {
			id := strings.TrimSpace(part)
			if id == "" {
				return nil, "sku_ids must not contain empty elements"
			}
			rawCount++
			if rawCount > maxSKUIDsQuery {
				return nil, "sku_ids exceeds maximum limit of 200"
			}
			if !isValidSKUIDToken(id) {
				return nil, "sku_ids contains an invalid SKU ID"
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			skuIDs = append(skuIDs, id)
		}
	}
	return skuIDs, ""
}

func (h *handler) handleListProducts(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	skuIDs, skuErr := parseSKUIDsQuery(q)
	if skuErr != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", skuErr)
		return
	}

	requestedStatus := parseProductStatus(q.Get("status"))
	if requestedStatus != productv1.ProductStatus_PRODUCT_STATUS_UNSPECIFIED && !isPublicProductStatus(requestedStatus) {
		writeEmptyProductPage(w, page, pageSize)
		return
	}

	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	categoryID := strings.TrimSpace(q.Get("category_id"))
	if categoryID == "" {
		if catSlug := strings.TrimSpace(q.Get("category_slug")); catSlug != "" {
			catResp, err := h.clients.Product.ListCategories(ctx, &productv1.ListCategoriesRequest{})
			if err != nil {
				httpx.WriteGRPCError(w, r, err)
				return
			}
			for _, c := range catResp.GetCategories() {
				if strings.EqualFold(c.GetSlug(), catSlug) {
					categoryID = c.GetId()
					break
				}
			}
			if categoryID == "" {
				writeEmptyProductPage(w, page, pageSize)
				return
			}
		}
	}

	shopID := strings.TrimSpace(q.Get("shop_id"))
	if shopID == "" {
		if shopSlug := strings.TrimSpace(q.Get("shop_slug")); shopSlug != "" {
			if h.clients.User == nil {
				writeEmptyProductPage(w, page, pageSize)
				return
			}
			shopResp, err := h.clients.User.GetShop(ctx, &userv1.GetShopRequest{Slug: shopSlug})
			if err != nil {
				if status.Code(err) == codes.NotFound {
					writeEmptyProductPage(w, page, pageSize)
					return
				}
				httpx.WriteGRPCError(w, r, err)
				return
			}
			sh := shopResp.GetShop()
			if sh == nil || sh.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
				writeEmptyProductPage(w, page, pageSize)
				return
			}
			shopID = sh.GetId()
		}
	}

	resp, err := h.clients.Product.ListProducts(ctx, &productv1.ListProductsRequest{
		CategoryId:  categoryID,
		Keyword:     q.Get("keyword"),
		ShopId:      shopID,
		Status:      requestedStatus,
		MinPriceVnd: httpx.ParseInt64Query(r, "min_price_vnd", 0),
		MaxPriceVnd: httpx.ParseInt64Query(r, "max_price_vnd", 0),
		SortBy:      normalizeProductSortQuery(q.Get("sort_by")),
		Page:        page,
		PageSize:    pageSize,
		SkuIds:      skuIDs,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*productDTO, 0, len(resp.GetProducts()))
	var filteredOut int64
	for _, p := range resp.GetProducts() {
		if !isPublicProductStatus(p.GetStatus()) {
			filteredOut++
			continue
		}
		items = append(items, mapPublicProduct(p))
	}
	total := resp.GetTotal()
	if filteredOut > 0 {
		total -= filteredOut
		if total < int64(len(items)) {
			total = int64(len(items))
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     total,
	})
}

func (h *handler) handleGetProduct(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.GetProduct(ctx, &productv1.GetProductRequest{
		Id: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	prod := resp.GetProduct()
	if prod == nil || !isPublicProductStatus(prod.GetStatus()) {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "product not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": mapPublicProduct(prod)})
}

func (h *handler) handleGetProductBySlug(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.GetProduct(ctx, &productv1.GetProductRequest{
		Slug:                    chi.URLParam(r, "slug"),
		IncludeDiscontinuedStub: true,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	if resp.GetDiscontinued() {
		stub := map[string]any{
			"discontinued": true,
			"category_id":  resp.GetDiscontinuedCategoryId(),
		}
		if shopID := strings.TrimSpace(resp.GetDiscontinuedShopId()); shopID != "" {
			stub["shop_id"] = shopID
		}
		if name := strings.TrimSpace(resp.GetDiscontinuedName()); name != "" {
			stub["name"] = name
		}
		httpx.WriteJSON(w, http.StatusOK, stub)
		return
	}
	prod := resp.GetProduct()
	if prod == nil || !isPublicProductStatus(prod.GetStatus()) {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "product not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": mapPublicProduct(prod)})
}

func (h *handler) handleSellerListProducts(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	var lowStock bool
	if rawLowStock := strings.TrimSpace(q.Get("low_stock")); rawLowStock != "" {
		b, parseErr := strconv.ParseBool(rawLowStock)
		if parseErr != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "low_stock must be a boolean")
			return
		}
		lowStock = b
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	// Always force shop_id from verified seller claims; never trust client query shop_id.
	resp, err := h.clients.Product.ListProducts(ctx, &productv1.ListProductsRequest{
		CategoryId:  q.Get("category_id"),
		Keyword:     q.Get("keyword"),
		ShopId:      shopID,
		Status:      parseProductStatus(q.Get("status")),
		LowStock:    lowStock,
		MinPriceVnd: httpx.ParseInt64Query(r, "min_price_vnd", 0),
		MaxPriceVnd: httpx.ParseInt64Query(r, "max_price_vnd", 0),
		SortBy:      normalizeProductSortQuery(q.Get("sort_by")),
		Page:        page,
		PageSize:    pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*productDTO, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		items = append(items, mapProduct(p))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerGetProduct(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.GetProduct(ctx, &productv1.GetProductRequest{
		Id: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	prod := resp.GetProduct()
	if prod == nil {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "product not found")
		return
	}
	if prod.GetShopId() != shopID {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another shop's product")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": mapProduct(prod)})
}

func (h *handler) handleSellerCreateProduct(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		CategoryID  string         `json:"category_id"`
		Name        string         `json:"name"`
		Slug        string         `json:"slug"`
		Description string         `json:"description"`
		Images      []string       `json:"images"`
		SKUs        []skuInputBody `json:"skus"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.CreateProduct(ctx, &productv1.CreateProductRequest{
		ShopId:      shopID,
		CategoryId:  body.CategoryID,
		Name:        body.Name,
		Slug:        body.Slug,
		Description: body.Description,
		Images:      body.Images,
		Skus:        toProtoSKUInputs(body.SKUs),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"product": mapProduct(resp.GetProduct())})
}

func (h *handler) handleSellerUpdateProduct(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		CategoryID  string         `json:"category_id"`
		Name        string         `json:"name"`
		Slug        string         `json:"slug"`
		Description string         `json:"description"`
		Images      []string       `json:"images"`
		Status      string         `json:"status"`
		SKUs        []skuInputBody `json:"skus"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.UpdateProduct(ctx, &productv1.UpdateProductRequest{
		Id:          chi.URLParam(r, "id"),
		ShopId:      shopID,
		CategoryId:  body.CategoryID,
		Name:        body.Name,
		Slug:        body.Slug,
		Description: body.Description,
		Images:      body.Images,
		Status:      parseProductStatus(body.Status),
		Skus:        toProtoSKUInputs(body.SKUs),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": mapProduct(resp.GetProduct())})
}

func (h *handler) handleSellerDeleteProduct(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.DeleteProduct(ctx, &productv1.DeleteProductRequest{
		Id:     chi.URLParam(r, "id"),
		ShopId: shopID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *handler) handleSellerUpdateStock(w http.ResponseWriter, r *http.Request) {
	shopID, ok := requireSellerShopID(w, r)
	if !ok {
		return
	}
	var body struct {
		NewStock        *int32 `json:"new_stock"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.NewStock == nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "new_stock is required")
		return
	}
	if *body.NewStock < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "new_stock must be non-negative")
		return
	}
	if body.ExpectedVersion == nil || *body.ExpectedVersion < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "expected_version is required and must be non-negative")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.UpdateStock(ctx, &productv1.UpdateStockRequest{
		SkuId:           chi.URLParam(r, "sku_id"),
		ShopId:          shopID,
		NewStock:        *body.NewStock,
		ExpectedVersion: *body.ExpectedVersion,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sku": mapSKU(resp.GetSku())})
}

func (h *handler) handleAdminListProducts(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListProducts(ctx, &productv1.ListProductsRequest{
		CategoryId:  q.Get("category_id"),
		Keyword:     q.Get("keyword"),
		ShopId:      q.Get("shop_id"),
		Status:      parseProductStatus(q.Get("status")),
		MinPriceVnd: httpx.ParseInt64Query(r, "min_price_vnd", 0),
		MaxPriceVnd: httpx.ParseInt64Query(r, "max_price_vnd", 0),
		SortBy:      normalizeProductSortQuery(q.Get("sort_by")),
		Page:        page,
		PageSize:    pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*productDTO, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		items = append(items, mapProduct(p))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleAdminGetProduct(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.GetProduct(ctx, &productv1.GetProductRequest{
		Id: chi.URLParam(r, "id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": mapProduct(resp.GetProduct())})
}

func (h *handler) handleAdminReviewProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.AdminReviewProduct(ctx, &productv1.AdminReviewProductRequest{
		AdminId:   auth.UserIDFromContext(r.Context()),
		ProductId: chi.URLParam(r, "id"),
		Decision:  parseProductReviewDecision(body.Decision),
		Reason:    body.Reason,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"product": mapProduct(resp.GetProduct())})
}

// --- Wave 3b-3 A8: Seller Stock History (S03/G8) & Bulk Product CSV Import (UC-S13/G6) ---

type stockHistoryItemDTO struct {
	ID             string `json:"id"`
	SKUID          string `json:"sku_id"`
	ProductID      string `json:"product_id"`
	ShopID         string `json:"shop_id"`
	ProductName    string `json:"product_name"`
	SKUCode        string `json:"sku_code"`
	SKUName        string `json:"sku_name"`
	OrderID        string `json:"order_id"`
	ChangeType     string `json:"change_type"`
	Delta          int32  `json:"delta"`
	StockBefore    int32  `json:"stock_before"`
	StockAfter     int32  `json:"stock_after"`
	ReservedBefore int32  `json:"reserved_before"`
	ReservedAfter  int32  `json:"reserved_after"`
	ActorID        string `json:"actor_id"`
	Reason         string `json:"reason"`
	CreatedAt      string `json:"created_at,omitempty"`
}

func mapStockHistoryItem(it *productv1.StockHistoryItem) *stockHistoryItemDTO {
	if it == nil {
		return nil
	}
	return &stockHistoryItemDTO{
		ID:             it.GetId(),
		SKUID:          it.GetSkuId(),
		ProductID:      it.GetProductId(),
		ShopID:         it.GetShopId(),
		ProductName:    it.GetProductName(),
		SKUCode:        it.GetSkuCode(),
		SKUName:        it.GetSkuName(),
		OrderID:        it.GetOrderId(),
		ChangeType:     it.GetChangeType(),
		Delta:          it.GetDelta(),
		StockBefore:    it.GetStockBefore(),
		StockAfter:     it.GetStockAfter(),
		ReservedBefore: it.GetReservedBefore(),
		ReservedAfter:  it.GetReservedAfter(),
		ActorID:        it.GetActorId(),
		Reason:         it.GetReason(),
		CreatedAt:      httpx.FormatTimestamp(it.GetCreatedAt()),
	}
}

func (h *handler) listSellerStockHistory(w http.ResponseWriter, r *http.Request, forcedSKUID string) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	fromTime, err := httpx.ParseTimestamp(q.Get("from_time"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid from_time timestamp")
		return
	}
	toTime, err := httpx.ParseTimestamp(q.Get("to_time"))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid to_time timestamp")
		return
	}

	skuID := strings.TrimSpace(forcedSKUID)
	if skuID == "" {
		skuID = strings.TrimSpace(q.Get("sku_id"))
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	// Always force shop_id from verified seller claims.
	resp, err := h.clients.Product.ListStockHistory(ctx, &productv1.ListStockHistoryRequest{
		ShopId:     auth.ShopIDFromContext(r.Context()),
		ProductId:  strings.TrimSpace(q.Get("product_id")),
		SkuId:      skuID,
		ChangeType: strings.TrimSpace(q.Get("change_type")),
		FromTime:   fromTime,
		ToTime:     toTime,
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*stockHistoryItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, mapStockHistoryItem(it))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

func (h *handler) handleSellerListStockHistory(w http.ResponseWriter, r *http.Request) {
	h.listSellerStockHistory(w, r, "")
}

func (h *handler) handleSellerListSKUStockHistory(w http.ResponseWriter, r *http.Request) {
	h.listSellerStockHistory(w, r, chi.URLParam(r, "sku_id"))
}

type productImportJobDTO struct {
	ID           string `json:"id"`
	ShopID       string `json:"shop_id"`
	CreatedBy    string `json:"created_by"`
	Filename     string `json:"filename"`
	Status       string `json:"status"`
	TotalRows    int32  `json:"total_rows"`
	ValidRows    int32  `json:"valid_rows"`
	InvalidRows  int32  `json:"invalid_rows"`
	CreatedRows  int32  `json:"created_rows"`
	FailedRows   int32  `json:"failed_rows"`
	ErrorMessage string `json:"error_message"`
	CreatedAt    string `json:"created_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	ConfirmedAt  string `json:"confirmed_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

type productImportRowDTO struct {
	ID               string `json:"id"`
	JobID            string `json:"job_id"`
	RowNumber        int32  `json:"row_number"`
	Status           string `json:"status"`
	ErrorCode        string `json:"error_code"`
	ErrorMessage     string `json:"error_message"`
	ProductName      string `json:"product_name"`
	SKUCode          string `json:"sku_code"`
	CategoryID       string `json:"category_id"`
	PriceVND         int64  `json:"price_vnd"`
	Stock            int32  `json:"stock"`
	WeightGrams      int32  `json:"weight_grams"`
	CreatedProductID string `json:"created_product_id"`
	RawRowJSON       string `json:"raw_row_json"`
}

func productImportJobStatusString(st productv1.ProductImportJobStatus) string {
	switch st {
	case productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_QUEUED:
		return "QUEUED"
	case productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_VALIDATING:
		return "VALIDATING"
	case productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_READY:
		return "READY"
	case productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_CONFIRMING:
		return "CONFIRMING"
	case productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_COMPLETED:
		return "COMPLETED"
	case productv1.ProductImportJobStatus_PRODUCT_IMPORT_JOB_STATUS_FAILED:
		return "FAILED"
	default:
		return ""
	}
}

func productImportRowStatusString(st productv1.ProductImportRowStatus) string {
	switch st {
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_PENDING:
		return "PENDING"
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_VALID:
		return "VALID"
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID:
		return "INVALID"
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_CREATED:
		return "CREATED"
	case productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_FAILED:
		return "FAILED"
	default:
		return ""
	}
}

func parseProductImportRowStatus(s string) productv1.ProductImportRowStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PENDING", "PRODUCT_IMPORT_ROW_STATUS_PENDING":
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_PENDING
	case "VALID", "PRODUCT_IMPORT_ROW_STATUS_VALID":
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_VALID
	case "INVALID", "PRODUCT_IMPORT_ROW_STATUS_INVALID":
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_INVALID
	case "CREATED", "PRODUCT_IMPORT_ROW_STATUS_CREATED":
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_CREATED
	case "FAILED", "PRODUCT_IMPORT_ROW_STATUS_FAILED":
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_FAILED
	default:
		return productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_UNSPECIFIED
	}
}

func mapProductImportJob(j *productv1.ProductImportJob) *productImportJobDTO {
	if j == nil {
		return nil
	}
	return &productImportJobDTO{
		ID:           j.GetId(),
		ShopID:       j.GetShopId(),
		CreatedBy:    j.GetCreatedBy(),
		Filename:     j.GetFilename(),
		Status:       productImportJobStatusString(j.GetStatus()),
		TotalRows:    j.GetTotalRows(),
		ValidRows:    j.GetValidRows(),
		InvalidRows:  j.GetInvalidRows(),
		CreatedRows:  j.GetCreatedRows(),
		FailedRows:   j.GetFailedRows(),
		ErrorMessage: j.GetErrorMessage(),
		CreatedAt:    httpx.FormatTimestamp(j.GetCreatedAt()),
		UpdatedAt:    httpx.FormatTimestamp(j.GetUpdatedAt()),
		ConfirmedAt:  httpx.FormatTimestamp(j.GetConfirmedAt()),
		FinishedAt:   httpx.FormatTimestamp(j.GetFinishedAt()),
	}
}

func mapProductImportRow(r *productv1.ProductImportRow) *productImportRowDTO {
	if r == nil {
		return nil
	}
	return &productImportRowDTO{
		ID:               r.GetId(),
		JobID:            r.GetJobId(),
		RowNumber:        r.GetRowNumber(),
		Status:           productImportRowStatusString(r.GetStatus()),
		ErrorCode:        r.GetErrorCode(),
		ErrorMessage:     r.GetErrorMessage(),
		ProductName:      r.GetProductName(),
		SKUCode:          r.GetSkuCode(),
		CategoryID:       r.GetCategoryId(),
		PriceVND:         r.GetPriceVnd(),
		Stock:            r.GetStock(),
		WeightGrams:      r.GetWeightGrams(),
		CreatedProductID: r.GetCreatedProductId(),
		RawRowJSON:       r.GetRawRowJson(),
	}
}

func cleanProductImportFilename(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", errors.New("invalid filename")
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return "products-import.csv", nil
	}
	s = strings.ReplaceAll(s, `\`, "/")
	s = path.Base(s)
	if s == "." || s == "/" || s == "" {
		return "products-import.csv", nil
	}
	if len(s) > maxCODCSVFileNameLen || hasControlChars(s) || hasCSVFormulaPrefix(s) {
		return "", errors.New("invalid filename")
	}
	return s, nil
}

func (h *handler) handleSellerProductImportTemplate(w http.ResponseWriter, r *http.Request) {
	catID := strings.TrimSpace(r.URL.Query().Get("category_id"))
	if catID == "" {
		catID = "cat-sample-id"
	}
	header := []string{
		"product_name", "sku_code", "category_id", "price_vnd",
		"stock", "weight_grams", "description", "color", "size",
	}
	rows := [][]string{
		{
			"Ao thun cotton co tron", "AT-DEN-M", catID, "150000",
			"50", "300", "Chat lieu cotton 100% thoang mat", "Den", "M",
		},
	}
	writeReportCSV(w, r, "product-import-template.csv", header, rows)
}

func (h *handler) readProductImportUpload(r *http.Request) (raw []byte, fileName string, httpStatus int, errCode, errMsg string) {
	maxBytes := h.productImportMaxBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}

	q := r.URL.Query()
	fileName = strings.TrimSpace(q.Get("filename"))
	if fileName == "" {
		fileName = strings.TrimSpace(q.Get("file_name"))
	}
	if fileName == "" {
		fileName = strings.TrimSpace(r.Header.Get("X-File-Name"))
	}

	mediaType := ""
	if ct := strings.TrimSpace(r.Header.Get("Content-Type")); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil {
			return nil, "", http.StatusBadRequest, "INVALID_ARGUMENT", "invalid Content-Type header"
		}
		mediaType = strings.ToLower(strings.TrimSpace(mt))
	}

	if mediaType == "multipart/form-data" {
		if err := r.ParseMultipartForm(maxBytes + codImportMultipartOverheadBytes); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size limit"
			}
			return nil, "", http.StatusBadRequest, "INVALID_ARGUMENT", "invalid multipart/form-data payload"
		}
		if r.MultipartForm != nil {
			defer func() { _ = r.MultipartForm.RemoveAll() }()
		}
		if fileName == "" {
			fileName = strings.TrimSpace(r.FormValue("filename"))
		}
		if fileName == "" {
			fileName = strings.TrimSpace(r.FormValue("file_name"))
		}
		f, fh, err := r.FormFile("file")
		if err != nil {
			f, fh, err = r.FormFile("csv")
			if err != nil {
				return nil, "", http.StatusBadRequest, "INVALID_ARGUMENT", "multipart file field 'file' or 'csv' is required"
			}
		}
		defer func() { _ = f.Close() }()
		if fileName == "" && fh != nil {
			fileName = strings.TrimSpace(fh.Filename)
		}
		data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size limit"
			}
			return nil, "", http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read uploaded CSV file"
		}
		if int64(len(data)) > maxBytes {
			return nil, "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size limit"
		}
		raw = data
	} else {
		switch mediaType {
		case "", "text/csv", "text/plain", "application/csv", "application/octet-stream", "application/vnd.ms-excel":
		default:
			return nil, "", http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be text/csv or multipart/form-data"
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size limit"
			}
			return nil, "", http.StatusBadRequest, "INVALID_ARGUMENT", "failed to read CSV body"
		}
		if int64(len(data)) > maxBytes {
			return nil, "", http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "CSV upload exceeds maximum size limit"
		}
		raw = data
	}

	if len(raw) == 0 {
		return nil, "", http.StatusBadRequest, "IMPORT_EMPTY_FILE", "CSV upload is empty"
	}
	if !utf8.Valid(raw) {
		return nil, "", http.StatusBadRequest, "IMPORT_INVALID_UTF8", "CSV file must be valid UTF-8"
	}
	cleanedName, err := cleanProductImportFilename(fileName)
	if err != nil {
		return nil, "", http.StatusBadRequest, "INVALID_ARGUMENT", err.Error()
	}
	return raw, cleanedName, 0, "", ""
}

func (h *handler) handleSellerCreateProductImport(w http.ResponseWriter, r *http.Request) {
	raw, fileName, errStatus, errCode, errMsg := h.readProductImportUpload(r)
	if errStatus != 0 {
		httpx.WriteError(w, r, errStatus, errCode, errMsg)
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.CreateProductImportJob(ctx, &productv1.CreateProductImportJobRequest{
		Filename:   fileName,
		CsvContent: raw,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"job": mapProductImportJob(resp.GetJob()),
	})
}

func (h *handler) handleSellerGetProductImportJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.GetProductImportJob(ctx, &productv1.GetProductImportJobRequest{
		JobId: chi.URLParam(r, "job_id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	job := resp.GetJob()
	if job == nil {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "import job not found")
		return
	}
	if job.GetShopId() != "" && job.GetShopId() != auth.ShopIDFromContext(r.Context()) {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another shop's import job")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"job": mapProductImportJob(job),
	})
}

func (h *handler) handleSellerListProductImportRows(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()
	var rowStatus productv1.ProductImportRowStatus
	if rawStatus := strings.TrimSpace(q.Get("status")); rawStatus != "" {
		rowStatus = parseProductImportRowStatus(rawStatus)
		if rowStatus == productv1.ProductImportRowStatus_PRODUCT_IMPORT_ROW_STATUS_UNSPECIFIED {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid import row status filter")
			return
		}
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListProductImportRows(ctx, &productv1.ListProductImportRowsRequest{
		JobId:      chi.URLParam(r, "job_id"),
		Status:     rowStatus,
		ErrorsOnly: httpx.ParseBoolQuery(r, "errors_only"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	items := make([]*productImportRowDTO, 0, len(resp.GetRows()))
	for _, row := range resp.GetRows() {
		items = append(items, mapProductImportRow(row))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      page,
		"page_size": pageSize,
		"total":     resp.GetTotal(),
	})
}

// productImportRowErrorLabelsVN mirrors web/src/lib/a8-types.ts PRODUCT_IMPORT_ROW_ERROR_LABELS
// for every per-row ProductImportRow.error_code listed in docs/contracts.md §7.
var productImportRowErrorLabelsVN = map[string]string{
	"INVALID_PRODUCT_NAME":  "Tên sản phẩm không hợp lệ hoặc bị bỏ trống.",
	"INVALID_PRODUCT_TEXT":  "Mô tả hoặc thông tin văn bản của sản phẩm không hợp lệ.",
	"INVALID_SKU_CODE":      "Mã SKU không hợp lệ hoặc bị bỏ trống.",
	"INVALID_SKU_TEXT":      "Tên phân loại SKU không hợp lệ.",
	"DUPLICATE_SKU_IN_FILE": "Mã SKU bị trùng lặp trong cùng tệp CSV.",
	"DUPLICATE_SKU_IN_SHOP": "Mã SKU đã tồn tại trong gian hàng.",
	"INVALID_CATEGORY":      "Mã danh mục không hợp lệ hoặc bị bỏ trống.",
	"CATEGORY_NOT_FOUND":    "Không tìm thấy danh mục tương ứng trên hệ thống.",
	"PROHIBITED_CATEGORY":   "Danh mục này thuộc nhóm hạn chế hoặc cấm kinh doanh.",
	"INVALID_PRICE":         "Giá bán không hợp lệ (phải là số nguyên VND lớn hơn 0).",
	"INVALID_STOCK":         "Số lượng tồn kho không hợp lệ (phải là số nguyên không âm).",
	"INVALID_WEIGHT":        "Khối lượng sản phẩm (gram) không hợp lệ.",
	"INVALID_SLUG":          "Đường dẫn (slug) sản phẩm không hợp lệ.",
	"DUPLICATE_SLUG":        "Đường dẫn (slug) sản phẩm đã tồn tại hoặc bị trùng lặp.",
	"INVALID_ATTRIBUTES":    "Thuộc tính phân loại sản phẩm không đúng định dạng.",
	"BANNED_CONTENT":        "Nội dung sản phẩm chứa từ khóa vi phạm quy định kiểm duyệt.",
	"SHOP_SUSPENDED":        "Gian hàng đang tạm ngưng hoạt động nên không thể tạo sản phẩm mới.",
	"INTERNAL_ERROR":        "Lỗi hệ thống tạm thời khi xử lý dòng này.",
}

func productImportRowErrorCSVMessage(errorCode, rawMessage string) string {
	if label, ok := productImportRowErrorLabelsVN[strings.ToUpper(strings.TrimSpace(errorCode))]; ok {
		return label
	}
	return rawMessage
}

func parseImportRawRowFields(rawJSON string) map[string]string {
	trimmed := strings.TrimSpace(rawJSON)
	if trimmed == "" {
		return nil
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return nil
	}
	return fields
}

func importRawCellOrFallback(rawFields map[string]string, key, fallback string) string {
	if rawFields != nil {
		if v, ok := rawFields[key]; ok {
			return v
		}
	}
	return fallback
}

func (h *handler) handleSellerExportProductImportErrorsCSV(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "job_id")
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	jobResp, err := h.clients.Product.GetProductImportJob(ctx, &productv1.GetProductImportJobRequest{
		JobId: jobID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	job := jobResp.GetJob()
	if job == nil {
		httpx.WriteError(w, r, http.StatusNotFound, "NOT_FOUND", "import job not found")
		return
	}
	if job.GetShopId() != "" && job.GetShopId() != auth.ShopIDFromContext(r.Context()) {
		httpx.WriteError(w, r, http.StatusForbidden, "PERMISSION_DENIED", "cannot access another shop's import job")
		return
	}

	const pageSize = int32(100)
	maxPages := int32((h.productImportMaxRows / int(pageSize)) + 2)
	if maxPages < 12 {
		maxPages = 12
	}
	var allRows []*productv1.ProductImportRow
	for page := int32(1); page <= maxPages; page++ {
		resp, listErr := h.clients.Product.ListProductImportRows(ctx, &productv1.ListProductImportRowsRequest{
			JobId:      jobID,
			ErrorsOnly: true,
			Page:       page,
			PageSize:   pageSize,
		})
		if listErr != nil {
			httpx.WriteGRPCError(w, r, listErr)
			return
		}
		batch := resp.GetRows()
		allRows = append(allRows, batch...)
		if len(batch) == 0 || int64(len(allRows)) >= resp.GetTotal() {
			break
		}
	}

	header := []string{
		"row_number", "status", "error_code", "error_message",
		"product_name", "sku_code", "category_id", "price_vnd", "stock", "weight_grams",
	}
	rows := make([][]string, 0, len(allRows))
	for _, row := range allRows {
		if row == nil {
			continue
		}
		rawFields := parseImportRawRowFields(row.GetRawRowJson())
		rows = append(rows, []string{
			strconv.Itoa(int(row.GetRowNumber())),
			productImportRowStatusString(row.GetStatus()),
			row.GetErrorCode(),
			productImportRowErrorCSVMessage(row.GetErrorCode(), row.GetErrorMessage()),
			importRawCellOrFallback(rawFields, "product_name", row.GetProductName()),
			importRawCellOrFallback(rawFields, "sku_code", row.GetSkuCode()),
			importRawCellOrFallback(rawFields, "category_id", row.GetCategoryId()),
			importRawCellOrFallback(rawFields, "price_vnd", i64s(row.GetPriceVnd())),
			importRawCellOrFallback(rawFields, "stock", strconv.Itoa(int(row.GetStock()))),
			importRawCellOrFallback(rawFields, "weight_grams", strconv.Itoa(int(row.GetWeightGrams()))),
		})
	}

	filename := fmt.Sprintf("product-import-errors-%s.csv", sanitizeFilenamePart(jobID))
	writeReportCSV(w, r, filename, header, rows)
}

func (h *handler) handleSellerConfirmProductImportJob(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ConfirmProductImportJob(ctx, &productv1.ConfirmProductImportJobRequest{
		JobId: chi.URLParam(r, "job_id"),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"job":               mapProductImportJob(resp.GetJob()),
		"already_confirmed": resp.GetAlreadyConfirmed(),
	})
}
