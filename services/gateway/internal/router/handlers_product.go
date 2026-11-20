package router

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
)

type categoryDTO struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	ParentID          string `json:"parent_id"`
	CommissionRateBps int32  `json:"commission_rate_bps"`
	SortOrder         int32  `json:"sort_order"`
	CreatedAt         string `json:"created_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

type skuDTO struct {
	ID                string            `json:"id"`
	ProductID         string            `json:"product_id"`
	ShopID            string            `json:"shop_id"`
	ProductName       string            `json:"product_name"`
	SKUCode           string            `json:"sku_code"`
	Name              string            `json:"name"`
	Attributes        map[string]string `json:"attributes"`
	PriceVND          int64             `json:"price_vnd"`
	Stock             int32             `json:"stock"`
	ReservedStock     int32             `json:"reserved_stock"`
	AvailableStock    int32             `json:"available_stock"`
	Version           int64             `json:"version"`
	WeightGrams       int32             `json:"weight_grams"`
	ImageURL          string            `json:"image_url"`
	CategoryID        string            `json:"category_id"`
	CommissionRateBps int32             `json:"commission_rate_bps"`
	IsActive          bool              `json:"is_active"`
}

type productDTO struct {
	ID              string    `json:"id"`
	ShopID          string    `json:"shop_id"`
	CategoryID      string    `json:"category_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Images          []string  `json:"images"`
	Status          string    `json:"status"`
	RejectionReason string    `json:"rejection_reason"`
	MinPriceVND     int64     `json:"min_price_vnd"`
	MaxPriceVND     int64     `json:"max_price_vnd"`
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

func mapCategory(c *productv1.Category) *categoryDTO {
	if c == nil {
		return nil
	}
	return &categoryDTO{
		ID:                c.GetId(),
		Name:              c.GetName(),
		Slug:              c.GetSlug(),
		ParentID:          c.GetParentId(),
		CommissionRateBps: c.GetCommissionRateBps(),
		SortOrder:         c.GetSortOrder(),
		CreatedAt:         httpx.FormatTimestamp(c.GetCreatedAt()),
		UpdatedAt:         httpx.FormatTimestamp(c.GetUpdatedAt()),
	}
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
		ID:                s.GetId(),
		ProductID:         s.GetProductId(),
		ShopID:            s.GetShopId(),
		ProductName:       s.GetProductName(),
		SKUCode:           s.GetSkuCode(),
		Name:              s.GetName(),
		Attributes:        attrs,
		PriceVND:          s.GetPriceVnd(),
		Stock:             s.GetStock(),
		ReservedStock:     s.GetReservedStock(),
		AvailableStock:    s.GetAvailableStock(),
		Version:           s.GetVersion(),
		WeightGrams:       s.GetWeightGrams(),
		ImageURL:          s.GetImageUrl(),
		CategoryID:        s.GetCategoryId(),
		CommissionRateBps: s.GetCommissionRateBps(),
		IsActive:          s.GetIsActive(),
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
	return &productDTO{
		ID:              p.GetId(),
		ShopID:          p.GetShopId(),
		CategoryID:      p.GetCategoryId(),
		Name:            p.GetName(),
		Description:     p.GetDescription(),
		Images:          imgs,
		Status:          productStatusString(p.GetStatus()),
		RejectionReason: p.GetRejectionReason(),
		MinPriceVND:     p.GetMinPriceVnd(),
		MaxPriceVND:     p.GetMaxPriceVnd(),
		SKUs:            skus,
		CreatedAt:       httpx.FormatTimestamp(p.GetCreatedAt()),
		UpdatedAt:       httpx.FormatTimestamp(p.GetUpdatedAt()),
	}
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
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"category": mapCategory(resp.GetCategory())})
}

func (h *handler) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name              string `json:"name"`
		Slug              string `json:"slug"`
		ParentID          string `json:"parent_id"`
		CommissionRateBps int32  `json:"commission_rate_bps"`
		SortOrder         int32  `json:"sort_order"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.UpdateCategory(ctx, &productv1.UpdateCategoryRequest{
		Id:                chi.URLParam(r, "id"),
		Name:              body.Name,
		Slug:              body.Slug,
		ParentId:          body.ParentID,
		CommissionRateBps: body.CommissionRateBps,
		SortOrder:         body.SortOrder,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"category": mapCategory(resp.GetCategory())})
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

func (h *handler) handleListProducts(w http.ResponseWriter, r *http.Request) {
	page, pageSize := httpx.ParsePagination(r)
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
		SortBy:      q.Get("sort_by"),
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

func (h *handler) handleGetProduct(w http.ResponseWriter, r *http.Request) {
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

func (h *handler) handleSellerCreateProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CategoryID  string         `json:"category_id"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Images      []string       `json:"images"`
		SKUs        []skuInputBody `json:"skus"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.CreateProduct(ctx, &productv1.CreateProductRequest{
		ShopId:      auth.ShopIDFromContext(r.Context()),
		CategoryId:  body.CategoryID,
		Name:        body.Name,
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
	var body struct {
		CategoryID  string         `json:"category_id"`
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Images      []string       `json:"images"`
		Status      string         `json:"status"`
		SKUs        []skuInputBody `json:"skus"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.UpdateProduct(ctx, &productv1.UpdateProductRequest{
		Id:          chi.URLParam(r, "id"),
		ShopId:      auth.ShopIDFromContext(r.Context()),
		CategoryId:  body.CategoryID,
		Name:        body.Name,
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
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.DeleteProduct(ctx, &productv1.DeleteProductRequest{
		Id:     chi.URLParam(r, "id"),
		ShopId: auth.ShopIDFromContext(r.Context()),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *handler) handleSellerUpdateStock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NewStock        int32 `json:"new_stock"`
		ExpectedVersion int64 `json:"expected_version"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		return
	}
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.UpdateStock(ctx, &productv1.UpdateStockRequest{
		SkuId:           chi.URLParam(r, "sku_id"),
		ShopId:          auth.ShopIDFromContext(r.Context()),
		NewStock:        body.NewStock,
		ExpectedVersion: body.ExpectedVersion,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sku": mapSKU(resp.GetSku())})
}

func (h *handler) handleAdminReviewProduct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
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
