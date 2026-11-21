package router

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/services/gateway/internal/httpx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type searchItemDTO struct {
	ID           string   `json:"id"`
	ShopID       string   `json:"shop_id"`
	CategoryID   string   `json:"category_id"`
	CategorySlug string   `json:"category_slug"`
	CategoryName string   `json:"category_name"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Images       []string `json:"images"`
	Status       string   `json:"status"`
	MinPriceVND  int64    `json:"min_price_vnd"`
	MaxPriceVND  int64    `json:"max_price_vnd"`
	TotalStock   int32    `json:"total_stock"`
	InStock      bool     `json:"in_stock"`
	Score        float64  `json:"score"`
	CreatedAt    string   `json:"created_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

type facetBucketDTO struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type searchFacetsDTO struct {
	Categories  []*facetBucketDTO `json:"categories"`
	Shops       []*facetBucketDTO `json:"shops"`
	PriceRanges []*facetBucketDTO `json:"price_ranges"`
}

type searchResponseDTO struct {
	Items    []*searchItemDTO `json:"items"`
	Total    int64            `json:"total"`
	Page     int32            `json:"page"`
	PageSize int32            `json:"page_size"`
	Facets   *searchFacetsDTO `json:"facets"`
}

type suggestionDTO struct {
	Text         string `json:"text"`
	Kind         string `json:"kind"`
	ProductID    string `json:"product_id"`
	CategorySlug string `json:"category_slug"`
	MinPriceVND  int64  `json:"min_price_vnd"`
}

func (h *handler) mountSearchRoutes(r chi.Router) {
	r.Get("/search", h.handleSearch)
	r.Get("/search/suggest", h.handleSearchSuggest)
}

func emptySearchFacets() *searchFacetsDTO {
	return &searchFacetsDTO{
		Categories:  []*facetBucketDTO{},
		Shops:       []*facetBucketDTO{},
		PriceRanges: []*facetBucketDTO{},
	}
}

func mapFacetBuckets(in []*searchv1.FacetBucket) []*facetBucketDTO {
	out := make([]*facetBucketDTO, 0, len(in))
	for _, b := range in {
		if b == nil {
			continue
		}
		out = append(out, &facetBucketDTO{
			Value: b.GetValue(),
			Label: b.GetLabel(),
			Count: b.GetCount(),
		})
	}
	return out
}

func mapSearchFacets(f *searchv1.SearchFacets) *searchFacetsDTO {
	if f == nil {
		return emptySearchFacets()
	}
	return &searchFacetsDTO{
		Categories:  mapFacetBuckets(f.GetCategories()),
		Shops:       mapFacetBuckets(f.GetShops()),
		PriceRanges: mapFacetBuckets(f.GetPriceRanges()),
	}
}

func mapSearchItem(it *searchv1.SearchItem) *searchItemDTO {
	if it == nil {
		return nil
	}
	imgs := make([]string, len(it.GetImages()))
	copy(imgs, it.GetImages())
	return &searchItemDTO{
		ID:           it.GetId(),
		ShopID:       it.GetShopId(),
		CategoryID:   it.GetCategoryId(),
		CategorySlug: it.GetCategorySlug(),
		CategoryName: it.GetCategoryName(),
		Name:         it.GetName(),
		Description:  it.GetDescription(),
		Images:       imgs,
		Status:       it.GetStatus(),
		MinPriceVND:  it.GetMinPriceVnd(),
		MaxPriceVND:  it.GetMaxPriceVnd(),
		TotalStock:   it.GetTotalStock(),
		InStock:      it.GetInStock(),
		Score:        it.GetScore(),
		CreatedAt:    httpx.FormatTimestamp(it.GetCreatedAt()),
		UpdatedAt:    httpx.FormatTimestamp(it.GetUpdatedAt()),
	}
}

func mapProductToSearchItem(p *productv1.Product) *searchItemDTO {
	if p == nil {
		return nil
	}
	imgs := make([]string, len(p.GetImages()))
	copy(imgs, p.GetImages())

	var totalStock int32
	for _, sk := range p.GetSkus() {
		if sk == nil {
			continue
		}
		avail := sk.GetAvailableStock()
		if avail == 0 && sk.GetStock() > 0 {
			computed := sk.GetStock() - sk.GetReservedStock() - sk.GetFlashSaleReservedStock()
			if computed > 0 {
				avail = computed
			}
		}
		if avail > 0 {
			totalStock += avail
		}
	}
	inStock := totalStock > 0
	if len(p.GetSkus()) == 0 {
		inStock = p.GetStatus() == productv1.ProductStatus_PRODUCT_STATUS_APPROVED
	}

	return &searchItemDTO{
		ID:           p.GetId(),
		ShopID:       p.GetShopId(),
		CategoryID:   p.GetCategoryId(),
		CategorySlug: "",
		CategoryName: "",
		Name:         p.GetName(),
		Description:  p.GetDescription(),
		Images:       imgs,
		Status:       productStatusString(p.GetStatus()),
		MinPriceVND:  p.GetMinPriceVnd(),
		MaxPriceVND:  p.GetMaxPriceVnd(),
		TotalStock:   totalStock,
		InStock:      inStock,
		Score:        0,
		CreatedAt:    httpx.FormatTimestamp(p.GetCreatedAt()),
		UpdatedAt:    httpx.FormatTimestamp(p.GetUpdatedAt()),
	}
}

func mapSuggestion(s *searchv1.Suggestion) *suggestionDTO {
	if s == nil {
		return nil
	}
	return &suggestionDTO{
		Text:         s.GetText(),
		Kind:         s.GetKind(),
		ProductID:    s.GetProductId(),
		CategorySlug: s.GetCategorySlug(),
		MinPriceVND:  s.GetMinPriceVnd(),
	}
}

func isSearchFallbackError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.Unavailable || st.Code() == codes.DeadlineExceeded
}

func firstNonEmptyQuery(r *http.Request, keys ...string) string {
	q := r.URL.Query()
	for _, k := range keys {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

func parseFirstInt64Query(r *http.Request, keys ...string) int64 {
	q := r.URL.Query()
	for _, k := range keys {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

func mapSearchSortToProductSort(sortParam string) string {
	switch strings.ToLower(strings.TrimSpace(sortParam)) {
	case "price_asc":
		return "price_asc"
	case "price_desc":
		return "price_desc"
	case "newest":
		return "newest"
	default:
		return "newest"
	}
}

func (h *handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	if h.clients == nil {
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search service unavailable")
		return
	}

	page, pageSize := httpx.ParsePagination(r)
	q := r.URL.Query()
	searchReq := &searchv1.SearchRequest{
		Query:       strings.TrimSpace(q.Get("q")),
		ShopId:      strings.TrimSpace(q.Get("shop_id")),
		Category:    firstNonEmptyQuery(r, "category", "category_id"),
		MinPriceVnd: parseFirstInt64Query(r, "min_price", "min_price_vnd"),
		MaxPriceVnd: parseFirstInt64Query(r, "max_price", "max_price_vnd"),
		Sort:        strings.TrimSpace(q.Get("sort")),
		Page:        page,
		PageSize:    pageSize,
		Mode:        strings.TrimSpace(q.Get("mode")),
		InStockOnly: httpx.ParseBoolQuery(r, "in_stock_only"),
	}

	if h.clients.Search == nil {
		if h.clients.Product != nil {
			h.warnSearchFallback(r, "search client not configured")
			h.handleSearchProductFallback(w, r, searchReq)
			return
		}
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "UNAVAILABLE", "search service unavailable")
		return
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	resp, err := h.clients.Search.Search(ctx, searchReq)
	cancel()
	if err != nil {
		if isSearchFallbackError(err) && h.clients.Product != nil {
			h.warnSearchFallback(r, err.Error())
			h.handleSearchProductFallback(w, r, searchReq)
			return
		}
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*searchItemDTO, 0, len(resp.GetItems()))
	for _, it := range resp.GetItems() {
		if mapped := mapSearchItem(it); mapped != nil {
			items = append(items, mapped)
		}
	}

	respPage := resp.GetPage()
	if respPage <= 0 {
		respPage = page
	}
	respPageSize := resp.GetPageSize()
	if respPageSize <= 0 {
		respPageSize = pageSize
	}

	httpx.WriteJSON(w, http.StatusOK, searchResponseDTO{
		Items:    items,
		Total:    resp.GetTotal(),
		Page:     respPage,
		PageSize: respPageSize,
		Facets:   mapSearchFacets(resp.GetFacets()),
	})
}

func (h *handler) handleSearchProductFallback(w http.ResponseWriter, r *http.Request, searchReq *searchv1.SearchRequest) {
	ctx, cancel := h.clients.OutgoingContext(r.Context())
	defer cancel()

	statusFilter := productv1.ProductStatus_PRODUCT_STATUS_APPROVED
	resp, err := h.clients.Product.ListProducts(ctx, &productv1.ListProductsRequest{
		CategoryId:  searchReq.GetCategory(),
		Keyword:     searchReq.GetQuery(),
		ShopId:      searchReq.GetShopId(),
		Status:      statusFilter,
		MinPriceVnd: searchReq.GetMinPriceVnd(),
		MaxPriceVnd: searchReq.GetMaxPriceVnd(),
		SortBy:      mapSearchSortToProductSort(searchReq.GetSort()),
		Page:        searchReq.GetPage(),
		PageSize:    searchReq.GetPageSize(),
	})
	if err != nil {
		httpx.WriteGRPCError(w, r, err)
		return
	}

	items := make([]*searchItemDTO, 0, len(resp.GetProducts()))
	for _, p := range resp.GetProducts() {
		if mapped := mapProductToSearchItem(p); mapped != nil {
			items = append(items, mapped)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, searchResponseDTO{
		Items:    items,
		Total:    resp.GetTotal(),
		Page:     searchReq.GetPage(),
		PageSize: searchReq.GetPageSize(),
		Facets:   emptySearchFacets(),
	})
}

func (h *handler) handleSearchSuggest(w http.ResponseWriter, r *http.Request) {
	if h.clients == nil || h.clients.Search == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": []*suggestionDTO{}})
		return
	}

	q := r.URL.Query()
	suggestReq := &searchv1.SuggestRequest{
		Query:  strings.TrimSpace(q.Get("q")),
		ShopId: strings.TrimSpace(q.Get("shop_id")),
		Limit:  httpx.ParseInt32Query(r, "limit", 0),
	}

	ctx, cancel := h.clients.OutgoingContext(r.Context())
	resp, err := h.clients.Search.Suggest(ctx, suggestReq)
	cancel()
	if err != nil {
		if isSearchFallbackError(err) {
			h.warnSearchFallback(r, err.Error())
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": []*suggestionDTO{}})
			return
		}
		httpx.WriteGRPCError(w, r, err)
		return
	}

	suggestions := make([]*suggestionDTO, 0, len(resp.GetSuggestions()))
	for _, s := range resp.GetSuggestions() {
		if mapped := mapSuggestion(s); mapped != nil {
			suggestions = append(suggestions, mapped)
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"suggestions": suggestions})
}

func (h *handler) warnSearchFallback(r *http.Request, reason string) {
	if h.log == nil {
		return
	}
	h.log.WarnContext(r.Context(), "search service unavailable, falling back",
		slog.String("path", r.URL.Path),
		slog.String("reason", reason),
	)
}
