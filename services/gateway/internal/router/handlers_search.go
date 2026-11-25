package router

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/textnorm"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"github.com/nexusmart/core/services/gateway/internal/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultSearchSuggestLimit  = int32(8)
	maxSearchSuggestLimit      = int32(20)
	suggestFallbackPageSize    = int32(5)
	defaultSearchHistoryLimit  = int32(20)
	maxSearchHistoryLimit      = int32(50)
	defaultSearchReindexBatch  = int32(200)
	maxSearchReindexBatch      = int32(500)
	maxDescriptionSnippetRunes = 200
)

// searchProductHitDTO mirrors SearchProductHit and strictly omits commission_rate_bps (G9/G10).
type searchProductHitDTO struct {
	ID                 string   `json:"id"`
	ShopID             string   `json:"shop_id"`
	ShopName           string   `json:"shop_name"`
	ShopSlug           string   `json:"shop_slug"`
	CategoryID         string   `json:"category_id"`
	CategorySlug       string   `json:"category_slug"`
	CategoryName       string   `json:"category_name"`
	Name               string   `json:"name"`
	Slug               string   `json:"slug"`
	ThumbnailURL       string   `json:"thumbnail_url"`
	MinPriceVND        int64    `json:"min_price_vnd"`
	MaxPriceVND        int64    `json:"max_price_vnd"`
	Status             string   `json:"status"`
	RatingAvgX100      int32    `json:"rating_avg_x100"`
	ReviewCount        int32    `json:"review_count"`
	InStock            bool     `json:"in_stock"`
	SKUCount           int32    `json:"sku_count"`
	Score              float64  `json:"score"`
	DescriptionSnippet string   `json:"description_snippet"`
	SKUIDs             []string `json:"sku_ids"`
	DefaultSKUID       string   `json:"default_sku_id"`
	Colors             []string `json:"colors"`
	Sizes              []string `json:"sizes"`
}

type facetCountDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Slug  string `json:"slug"`
	Count int64  `json:"count"`
}

type suggestEntityDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type searchHistoryItemDTO struct {
	ID        string `json:"id"`
	Query     string `json:"query"`
	CreatedAt string `json:"created_at,omitempty"`
}

func mapSearchProductHit(in *searchv1.SearchProductHit) *searchProductHitDTO {
	if in == nil {
		return nil
	}
	skuIDs := in.GetSkuIds()
	if skuIDs == nil {
		skuIDs = []string{}
	}
	colors := in.GetColors()
	if colors == nil {
		colors = []string{}
	}
	sizes := in.GetSizes()
	if sizes == nil {
		sizes = []string{}
	}
	return &searchProductHitDTO{
		ID:                 in.GetId(),
		ShopID:             in.GetShopId(),
		ShopName:           in.GetShopName(),
		ShopSlug:           in.GetShopSlug(),
		CategoryID:         in.GetCategoryId(),
		CategorySlug:       in.GetCategorySlug(),
		CategoryName:       in.GetCategoryName(),
		Name:               in.GetName(),
		Slug:               in.GetSlug(),
		ThumbnailURL:       in.GetThumbnailUrl(),
		MinPriceVND:        in.GetMinPriceVnd(),
		MaxPriceVND:        in.GetMaxPriceVnd(),
		Status:             in.GetStatus(),
		RatingAvgX100:      in.GetRatingAvgX100(),
		ReviewCount:        in.GetReviewCount(),
		InStock:            in.GetInStock(),
		SKUCount:           in.GetSkuCount(),
		Score:              in.GetScore(),
		DescriptionSnippet: in.GetDescriptionSnippet(),
		SKUIDs:             skuIDs,
		DefaultSKUID:       in.GetDefaultSkuId(),
		Colors:             colors,
		Sizes:              sizes,
	}
}

// normalizeAttributeFacetKey aligns fallback SKU attribute key matching with product's search indexer.
func normalizeAttributeFacetKey(k string) string {
	norm := textnorm.NormalizeSearchText(k)
	norm = strings.ReplaceAll(norm, "-", "_")
	norm = strings.ReplaceAll(norm, " ", "_")
	return norm
}

func productMatchesFallbackVariantFilters(p *productv1.Product, wantSize, wantColor string) bool {
	if wantSize == "" && wantColor == "" {
		return true
	}
	if p == nil {
		return false
	}
	var matchedSize, matchedColor bool
	for _, s := range p.GetSkus() {
		if s == nil || !s.GetIsActive() {
			continue
		}
		for k, rawVal := range s.GetAttributes() {
			normVal := textnorm.NormalizeSearchText(rawVal)
			if normVal == "" {
				continue
			}
			switch normalizeAttributeFacetKey(k) {
			case "size", "kich_co", "kichco", "kich_thuoc", "kichthuoc":
				if wantSize != "" && normVal == wantSize {
					matchedSize = true
				}
			case "color", "colour", "mau", "mau_sac", "mausac", "color_code", "colorcode":
				if wantColor != "" && normVal == wantColor {
					matchedColor = true
				}
			}
		}
	}
	return (wantSize == "" || matchedSize) && (wantColor == "" || matchedColor)
}

func mapProductToSearchHit(p *productv1.Product) *searchProductHitDTO {
	if p == nil {
		return nil
	}
	thumb := ""
	if imgs := p.GetImages(); len(imgs) > 0 {
		thumb = imgs[0]
	}

	skuIDs := make([]string, 0, len(p.GetSkus()))
	colors := make([]string, 0)
	sizes := make([]string, 0)
	seenColor := make(map[string]struct{})
	seenSize := make(map[string]struct{})
	var (
		defaultSKUID string
		inStock      bool
		activeSKUs   int32
	)
	for _, s := range p.GetSkus() {
		if s == nil || !s.GetIsActive() {
			continue
		}
		activeSKUs++
		skuIDs = append(skuIDs, s.GetId())
		avail := s.GetAvailableStock()
		if avail <= 0 {
			avail = s.GetStock() - s.GetReservedStock()
		}
		if avail > 0 {
			inStock = true
			if defaultSKUID == "" {
				defaultSKUID = s.GetId()
			}
		}
		attrs := s.GetAttributes()
		if len(attrs) > 0 {
			keys := make([]string, 0, len(attrs))
			for k := range attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				val := strings.TrimSpace(attrs[k])
				if val == "" {
					continue
				}
				switch normalizeAttributeFacetKey(k) {
				case "color", "colour", "mau", "mau_sac", "mausac":
					lk := strings.ToLower(val)
					if _, ok := seenColor[lk]; !ok {
						seenColor[lk] = struct{}{}
						colors = append(colors, val)
					}
				case "size", "kich_co", "kichco", "kich_thuoc", "kichthuoc":
					lk := strings.ToLower(val)
					if _, ok := seenSize[lk]; !ok {
						seenSize[lk] = struct{}{}
						sizes = append(sizes, val)
					}
				}
			}
		}
	}
	if defaultSKUID == "" && len(skuIDs) > 0 {
		defaultSKUID = skuIDs[0]
	}
	if !inStock && len(p.GetSkus()) == 0 && p.GetStatus() == productv1.ProductStatus_PRODUCT_STATUS_APPROVED {
		inStock = true
	}

	snippet := strings.TrimSpace(p.GetDescription())
	if runes := []rune(snippet); len(runes) > maxDescriptionSnippetRunes {
		snippet = string(runes[:maxDescriptionSnippetRunes])
	}

	return &searchProductHitDTO{
		ID:                 p.GetId(),
		ShopID:             p.GetShopId(),
		CategoryID:         p.GetCategoryId(),
		Name:               p.GetName(),
		Slug:               p.GetSlug(),
		ThumbnailURL:       thumb,
		MinPriceVND:        p.GetMinPriceVnd(),
		MaxPriceVND:        p.GetMaxPriceVnd(),
		Status:             productStatusString(p.GetStatus()),
		RatingAvgX100:      p.GetRatingAvgX100(),
		ReviewCount:        p.GetReviewCount(),
		InStock:            inStock,
		SKUCount:           activeSKUs,
		Score:              0,
		DescriptionSnippet: snippet,
		SKUIDs:             skuIDs,
		DefaultSKUID:       defaultSKUID,
		Colors:             colors,
		Sizes:              sizes,
	}
}

func mapFacetCounts(in []*searchv1.FacetCount) []*facetCountDTO {
	out := make([]*facetCountDTO, 0, len(in))
	for _, f := range in {
		if f == nil {
			continue
		}
		out = append(out, &facetCountDTO{
			ID:    f.GetId(),
			Name:  f.GetName(),
			Slug:  f.GetSlug(),
			Count: f.GetCount(),
		})
	}
	return out
}

func mapSuggestEntities(in []*searchv1.SuggestEntity) []*suggestEntityDTO {
	out := make([]*suggestEntityDTO, 0, len(in))
	for _, e := range in {
		if e == nil {
			continue
		}
		out = append(out, &suggestEntityDTO{
			ID:   e.GetId(),
			Name: e.GetName(),
			Slug: e.GetSlug(),
		})
	}
	return out
}

func parseSearchMode(raw string) (searchv1.SearchMode, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto", "search_mode_auto":
		return searchv1.SearchMode_SEARCH_MODE_AUTO, true
	case "keyword", "search_mode_keyword":
		return searchv1.SearchMode_SEARCH_MODE_KEYWORD, true
	case "hybrid", "search_mode_hybrid":
		return searchv1.SearchMode_SEARCH_MODE_HYBRID, true
	default:
		return searchv1.SearchMode_SEARCH_MODE_UNSPECIFIED, false
	}
}

func normalizeSearchSortBy(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "relevance":
		return "relevance", true
	case "price_asc", "price-asc":
		return "price_asc", true
	case "price_desc", "price-desc":
		return "price_desc", true
	case "newest":
		return "newest", true
	case "rating_desc", "rating-desc":
		return "rating_desc", true
	default:
		return "", false
	}
}

func reindexJobStatusString(st searchv1.ReindexJobStatus) string {
	switch st {
	case searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_RUNNING:
		return "RUNNING"
	case searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED:
		return "COMPLETED"
	case searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_FAILED:
		return "FAILED"
	default:
		return ""
	}
}

func shouldFallbackSearch(reqCtx context.Context, err error) bool {
	if err == nil || reqCtx.Err() != nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

// writeSearchGRPCError maps upstream gRPC errors for public search endpoints without
// logging raw error messages or query keywords (G14).
func (h *handler) writeSearchGRPCError(w http.ResponseWriter, r *http.Request, err error) {
	httpStatus, code, msg := httpx.MapGRPCError(err)
	if httpStatus >= http.StatusInternalServerError {
		if h.log != nil {
			h.log.ErrorContext(r.Context(), "search upstream error",
				"http_status", httpStatus,
				"grpc_code", status.Code(err).String(),
				"method", r.Method,
				"path", r.URL.Path,
			)
		}
		if httpStatus == http.StatusServiceUnavailable || httpStatus == http.StatusGatewayTimeout {
			msg = "search service temporarily unavailable"
		} else {
			msg = "internal server error"
		}
	}
	httpx.WriteError(w, r, httpStatus, code, msg)
}

func writeEmptySearchFallbackPage(w http.ResponseWriter, page, pageSize int32) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":           []*searchProductHitDTO{},
		"total":           int64(0),
		"page":            page,
		"page_size":       pageSize,
		"category_facets": []*facetCountDTO{},
		"shop_facets":     []*facetCountDTO{},
		"did_you_mean":    "",
		"degraded_mode":   "product_fallback",
		"used_vector":     false,
	})
}

func (h *handler) fallbackSearchProducts(w http.ResponseWriter, r *http.Request, req *searchv1.SearchProductsRequest, cause error) {
	if h.log != nil {
		grpcCode := "nil_client"
		if cause != nil {
			grpcCode = status.Code(cause).String()
		}
		// G14: never log raw query string or upstream error message text.
		h.log.WarnContext(r.Context(), "search service degraded, falling back to product catalog",
			"path", r.URL.Path,
			"grpc_code", grpcCode,
		)
	}
	if h.clients == nil || h.clients.Product == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search fallback unavailable")
		return
	}

	// Strictly strip caller JWT on fallback so only public catalog products are returned (G10/G13).
	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	categoryID := strings.TrimSpace(req.GetCategoryId())
	if categoryID == "" {
		if catSlug := strings.TrimSpace(req.GetCategorySlug()); catSlug != "" {
			catResp, err := h.clients.Product.ListCategories(ctx, &productv1.ListCategoriesRequest{})
			if err != nil {
				h.writeSearchGRPCError(w, r, err)
				return
			}
			for _, c := range catResp.GetCategories() {
				if strings.EqualFold(c.GetSlug(), catSlug) {
					categoryID = c.GetId()
					break
				}
			}
			if categoryID == "" {
				writeEmptySearchFallbackPage(w, req.GetPage(), req.GetPageSize())
				return
			}
		}
	}

	shopID := strings.TrimSpace(req.GetShopId())
	if shopID == "" {
		if shopSlug := strings.TrimSpace(req.GetShopSlug()); shopSlug != "" {
			if h.clients.User == nil {
				writeEmptySearchFallbackPage(w, req.GetPage(), req.GetPageSize())
				return
			}
			shopResp, err := h.clients.User.GetShop(ctx, &userv1.GetShopRequest{Slug: shopSlug})
			if err != nil {
				if status.Code(err) == codes.NotFound {
					writeEmptySearchFallbackPage(w, req.GetPage(), req.GetPageSize())
					return
				}
				h.writeSearchGRPCError(w, r, err)
				return
			}
			sh := shopResp.GetShop()
			if sh == nil || sh.GetStatus() != userv1.ShopStatus_SHOP_STATUS_ACTIVE {
				writeEmptySearchFallbackPage(w, req.GetPage(), req.GetPageSize())
				return
			}
			shopID = sh.GetId()
		}
	}

	fallbackSort := ""
	switch req.GetSortBy() {
	case "price_asc", "price_desc", "newest":
		fallbackSort = req.GetSortBy()
	}

	resp, err := h.clients.Product.ListProducts(ctx, &productv1.ListProductsRequest{
		CategoryId:  categoryID,
		Keyword:     req.GetQ(),
		ShopId:      shopID,
		MinPriceVnd: req.GetMinPriceVnd(),
		MaxPriceVnd: req.GetMaxPriceVnd(),
		SortBy:      fallbackSort,
		Page:        req.GetPage(),
		PageSize:    req.GetPageSize(),
	})
	if err != nil {
		h.writeSearchGRPCError(w, r, err)
		return
	}

	wantSize := textnorm.NormalizeSearchText(req.GetSize())
	wantColor := textnorm.NormalizeSearchText(req.GetColor())

	items := make([]*searchProductHitDTO, 0, len(resp.GetProducts()))
	var filteredOut int64
	for _, p := range resp.GetProducts() {
		if p == nil || !isPublicProductStatus(p.GetStatus()) || p.GetShopSuspended() {
			filteredOut++
			continue
		}
		if req.GetMinRatingX100() > 0 && p.GetRatingAvgX100() < req.GetMinRatingX100() {
			filteredOut++
			continue
		}
		if !productMatchesFallbackVariantFilters(p, wantSize, wantColor) {
			filteredOut++
			continue
		}
		hit := mapProductToSearchHit(p)
		if req.GetInStockOnly() && !hit.InStock {
			filteredOut++
			continue
		}
		items = append(items, hit)
	}
	total := resp.GetTotal()
	if filteredOut > 0 {
		total -= filteredOut
		if total < int64(len(items)) {
			total = int64(len(items))
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":           items,
		"total":           total,
		"page":            req.GetPage(),
		"page_size":       req.GetPageSize(),
		"category_facets": []*facetCountDTO{},
		"shop_facets":     []*facetCountDTO{},
		"did_you_mean":    "",
		"degraded_mode":   "product_fallback",
		"used_vector":     false,
	})
}

func (h *handler) handleSearchProducts(w http.ResponseWriter, r *http.Request) {
	page, pageSize, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	q := r.URL.Query()

	var minPriceVND int64
	if raw := strings.TrimSpace(q.Get("min_price_vnd")); raw != "" {
		v, pErr := strconv.ParseInt(raw, 10, 64)
		if pErr != nil || v < 0 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "min_price_vnd must be a non-negative integer")
			return
		}
		minPriceVND = v
	}
	var maxPriceVND int64
	if raw := strings.TrimSpace(q.Get("max_price_vnd")); raw != "" {
		v, pErr := strconv.ParseInt(raw, 10, 64)
		if pErr != nil || v < 0 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "max_price_vnd must be a non-negative integer")
			return
		}
		maxPriceVND = v
	}
	if minPriceVND > 0 && maxPriceVND > 0 && minPriceVND > maxPriceVND {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "min_price_vnd must not exceed max_price_vnd")
		return
	}

	var minRatingX100 int32
	if raw := strings.TrimSpace(q.Get("min_rating_x100")); raw != "" {
		v, pErr := strconv.ParseInt(raw, 10, 32)
		if pErr != nil || v < 0 || v > 500 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "min_rating_x100 must be an integer between 0 and 500")
			return
		}
		minRatingX100 = int32(v)
	}

	mode, ok := parseSearchMode(q.Get("mode"))
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "mode must be auto, keyword, or hybrid")
		return
	}

	sortBy, ok := normalizeSearchSortBy(q.Get("sort_by"))
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid sort_by value")
		return
	}

	req := &searchv1.SearchProductsRequest{
		Q:             strings.TrimSpace(q.Get("q")),
		CategoryId:    strings.TrimSpace(q.Get("category_id")),
		CategorySlug:  strings.TrimSpace(q.Get("category_slug")),
		ShopId:        strings.TrimSpace(q.Get("shop_id")),
		ShopSlug:      strings.TrimSpace(q.Get("shop_slug")),
		MinPriceVnd:   minPriceVND,
		MaxPriceVnd:   maxPriceVND,
		MinRatingX100: minRatingX100,
		InStockOnly:   httpx.ParseBoolQuery(r, "in_stock_only"),
		SortBy:        sortBy,
		Mode:          mode,
		Page:          page,
		PageSize:      pageSize,
		Size:          strings.TrimSpace(q.Get("size")),
		Color:         strings.TrimSpace(q.Get("color")),
	}

	if h.clients == nil || h.clients.Search == nil {
		h.fallbackSearchProducts(w, r, req, nil)
		return
	}

	// Optional auth: forward verified end-user JWT when present so SearchService records
	// authenticated history (G14) while honoring SEARCH_FALLBACK_TIMEOUT_MS (G13).
	ctx, cancel := h.clients.OutgoingContextWithTimeout(r.Context(), h.searchFallbackTimeout)
	resp, err := h.clients.Search.SearchProducts(ctx, req)
	cancel()
	if err != nil {
		if shouldFallbackSearch(r.Context(), err) {
			h.fallbackSearchProducts(w, r, req, err)
			return
		}
		h.writeSearchGRPCError(w, r, err)
		return
	}

	items := make([]*searchProductHitDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		items = append(items, mapSearchProductHit(it))
	}
	respPage := resp.GetPage()
	if respPage <= 0 {
		respPage = page
	}
	respPageSize := resp.GetPageSize()
	if respPageSize <= 0 {
		respPageSize = pageSize
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items":           items,
		"total":           resp.GetTotal(),
		"page":            respPage,
		"page_size":       respPageSize,
		"category_facets": mapFacetCounts(resp.GetCategoryFacets()),
		"shop_facets":     mapFacetCounts(resp.GetShopFacets()),
		"did_you_mean":    resp.GetDidYouMean(),
		"degraded_mode":   resp.GetDegradedMode(),
		"used_vector":     resp.GetUsedVector(),
	})
}

func (h *handler) fallbackSuggestProducts(w http.ResponseWriter, r *http.Request, query string, cause error) {
	if h.log != nil {
		grpcCode := "nil_client"
		if cause != nil {
			grpcCode = status.Code(cause).String()
		}
		h.log.WarnContext(r.Context(), "search suggest degraded, falling back to product catalog",
			"path", r.URL.Path,
			"grpc_code", grpcCode,
		)
	}
	if h.clients == nil || h.clients.Product == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search suggest fallback unavailable")
		return
	}

	ctx, cancel := h.clients.PublicOutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Product.ListProducts(ctx, &productv1.ListProductsRequest{
		Keyword:  query,
		Page:     1,
		PageSize: suggestFallbackPageSize,
	})
	if err != nil {
		h.writeSearchGRPCError(w, r, err)
		return
	}

	products := make([]*searchProductHitDTO, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		if p == nil || !isPublicProductStatus(p.GetStatus()) || p.GetShopSuspended() {
			continue
		}
		products = append(products, mapProductToSearchHit(p))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"keywords":   []string{},
		"categories": []*suggestEntityDTO{},
		"shops":      []*suggestEntityDTO{},
		"trending":   []string{},
		"products":   products,
	})
}

func (h *handler) handleSearchSuggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("q"))
	limit := defaultSearchSuggestLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 1 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be a positive integer")
			return
		}
		if int32(v) > maxSearchSuggestLimit {
			limit = maxSearchSuggestLimit
		} else {
			limit = int32(v)
		}
	}

	if h.clients == nil || h.clients.Search == nil {
		h.fallbackSuggestProducts(w, r, query, nil)
		return
	}

	ctx, cancel := h.clients.PublicOutgoingContextWithTimeout(r.Context(), h.searchFallbackTimeout)
	resp, err := h.clients.Search.Suggest(ctx, &searchv1.SuggestRequest{
		Query: query,
		Limit: limit,
	})
	cancel()
	if err != nil {
		if shouldFallbackSearch(r.Context(), err) {
			h.fallbackSuggestProducts(w, r, query, err)
			return
		}
		h.writeSearchGRPCError(w, r, err)
		return
	}

	keywords := resp.GetKeywords()
	if keywords == nil {
		keywords = []string{}
	}
	trending := resp.GetTrending()
	if trending == nil {
		trending = []string{}
	}
	products := make([]*searchProductHitDTO, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		products = append(products, mapSearchProductHit(p))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"keywords":   keywords,
		"categories": mapSuggestEntities(resp.GetCategories()),
		"shops":      mapSuggestEntities(resp.GetShops()),
		"trending":   trending,
		"products":   products,
	})
}

func (h *handler) handleListSearchHistory(w http.ResponseWriter, r *http.Request) {
	if h.clients == nil || h.clients.Search == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search service unavailable")
		return
	}

	limit := defaultSearchHistoryLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || v < 1 {
			httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be a positive integer")
			return
		}
		if int32(v) > maxSearchHistoryLimit {
			limit = maxSearchHistoryLimit
		} else {
			limit = int32(v)
		}
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Search.ListSearchHistory(ctx, &searchv1.ListSearchHistoryRequest{
		Limit: limit,
	})
	if err != nil {
		h.writeSearchGRPCError(w, r, err)
		return
	}

	items := make([]*searchHistoryItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if it == nil {
			continue
		}
		items = append(items, &searchHistoryItemDTO{
			ID:        it.GetId(),
			Query:     it.GetQuery(),
			CreatedAt: httpx.FormatTimestamp(it.GetCreatedAt()),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *handler) handleClearSearchHistory(w http.ResponseWriter, r *http.Request) {
	if h.clients == nil || h.clients.Search == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search service unavailable")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Search.DeleteSearchHistory(ctx, &searchv1.DeleteSearchHistoryRequest{
		ClearAll: true,
	})
	if err != nil {
		h.writeSearchGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func (h *handler) handleDeleteSearchHistoryItem(w http.ResponseWriter, r *http.Request) {
	if h.clients == nil || h.clients.Search == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search service unavailable")
		return
	}

	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "history id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Search.DeleteSearchHistory(ctx, &searchv1.DeleteSearchHistoryRequest{
		Id:       id,
		ClearAll: false,
	})
	if err != nil {
		h.writeSearchGRPCError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": resp.GetOk()})
}

func boolOrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func (h *handler) handleAdminSearchReindex(w http.ResponseWriter, r *http.Request) {
	if h.clients == nil || h.clients.Search == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search service unavailable")
		return
	}

	var body struct {
		ReindexProducts   *bool `json:"reindex_products"`
		ReindexEmbeddings *bool `json:"reindex_embeddings"`
		ReindexCategories *bool `json:"reindex_categories"`
		ReindexShops      *bool `json:"reindex_shops"`
		BatchSize         int32 `json:"batch_size"`
	}
	if err := httpx.DecodeOptionalJSON(r, &body); err != nil {
		httpx.WriteDecodeError(w, r, err)
		return
	}
	if body.BatchSize < 0 {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "batch_size must be positive")
		return
	}
	batchSize := body.BatchSize
	if batchSize == 0 {
		batchSize = defaultSearchReindexBatch
	}
	if batchSize > maxSearchReindexBatch {
		batchSize = maxSearchReindexBatch
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	reindexProducts := boolOrDefault(body.ReindexProducts, true)
	reindexEmbeddings := boolOrDefault(body.ReindexEmbeddings, true)
	reindexCategories := boolOrDefault(body.ReindexCategories, true)
	reindexShops := boolOrDefault(body.ReindexShops, true)

	resp, err := h.clients.Search.ReindexAll(ctx, &searchv1.ReindexAllRequest{
		ReindexProducts:   reindexProducts,
		ReindexEmbeddings: reindexEmbeddings,
		ReindexCategories: reindexCategories,
		ReindexShops:      reindexShops,
		BatchSize:         batchSize,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	statusStr := reindexJobStatusString(resp.GetStatus())
	middleware.SetAuditSummary(r.Context(), resp.GetJobId(), map[string]any{
		"job_id":             resp.GetJobId(),
		"status":             statusStr,
		"reindex_products":   reindexProducts,
		"reindex_embeddings": reindexEmbeddings,
		"reindex_categories": reindexCategories,
		"reindex_shops":      reindexShops,
		"batch_size":         batchSize,
	})
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"job_id": resp.GetJobId(),
		"status": statusStr,
	})
}

func (h *handler) handleAdminGetSearchReindexStatus(w http.ResponseWriter, r *http.Request) {
	if h.clients == nil || h.clients.Search == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search service unavailable")
		return
	}

	jobID := strings.TrimSpace(chi.URLParam(r, "job_id"))
	if jobID == "" {
		httpx.WriteError(w, r, http.StatusBadRequest, "INVALID_ARGUMENT", "job_id is required")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	resp, err := h.clients.Search.GetReindexStatus(ctx, &searchv1.GetReindexStatusRequest{
		JobId: jobID,
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"job_id":             resp.GetJobId(),
		"status":             reindexJobStatusString(resp.GetStatus()),
		"products_indexed":   resp.GetProductsIndexed(),
		"embeddings_updated": resp.GetEmbeddingsUpdated(),
		"error_message":      resp.GetErrorMessage(),
		"started_at":         httpx.FormatTimestamp(resp.GetStartedAt()),
		"finished_at":        httpx.FormatTimestamp(resp.GetFinishedAt()),
	})
}
