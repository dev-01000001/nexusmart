package server

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/services/search/internal/embed"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/normalize"
	"github.com/nexusmart/core/services/search/internal/rrf"
	"github.com/nexusmart/core/services/search/internal/store"
)

const (
	defaultPageSize     = 20
	maxPageSize         = 100
	defaultSuggestLimit = 8
	maxSuggestLimit     = 20
	defaultSimilarLimit = 10
	maxSimilarLimit     = 50
	hybridMinTopK       = 100
	hybridMaxTopK       = 200
)

var defaultFacets = []string{"category_slug", "shop_id", "price_bucket"}

// SearchClient abstracts Meilisearch read operations for clean unit testing.
type SearchClient interface {
	Search(ctx context.Context, params meili.SearchParams) (*meili.SearchResult, error)
	Suggest(ctx context.Context, q, shopID string, limit int) ([]meili.Suggestion, error)
}

// Option customizes Server behavior.
type Option func(*Server)

// WithCategoryLabels injects a callback returning category_slug -> Vietnamese display name mappings.
func WithCategoryLabels(fn func() map[string]string) Option {
	return func(s *Server) {
		s.categoryLabelsFn = fn
	}
}

// Server implements searchv1.SearchServiceServer with hybrid Meilisearch + pgvector RRF search.
type Server struct {
	searchv1.UnimplementedSearchServiceServer

	meili            SearchClient
	store            store.Store
	embedder         embed.Embedder
	categoryLabelsFn func() map[string]string
	logger           *slog.Logger
}

// New constructs a SearchService gRPC server.
func New(
	meiliClient SearchClient,
	st store.Store,
	emb embed.Embedder,
	logger *slog.Logger,
	opts ...Option,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		meili:    meiliClient,
		store:    st,
		embedder: emb,
		logger:   logger,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Search executes hybrid (Meilisearch + pgvector RRF), keyword-only, or vector-only product search.
func (s *Server) Search(ctx context.Context, req *searchv1.SearchRequest) (*searchv1.SearchResponse, error) {
	if err := validatePriceRange(req.GetMinPriceVnd(), req.GetMaxPriceVnd()); err != nil {
		return nil, err
	}

	sortBy := strings.ToLower(strings.TrimSpace(req.GetSort()))
	switch sortBy {
	case "", "relevance", "price_asc", "price_desc", "newest":
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported sort %q", req.GetSort())
	}

	mode := strings.ToLower(strings.TrimSpace(req.GetMode()))
	switch mode {
	case "", "hybrid", "keyword", "vector":
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported mode %q", req.GetMode())
	}

	page := req.GetPage()
	if page <= 0 {
		page = 1
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	offset := int(page-1) * int(pageSize)
	query := strings.TrimSpace(req.GetQuery())

	if mode == "vector" {
		return s.searchVectorOnly(ctx, req, query, page, pageSize, offset)
	}

	useHybrid := query != "" &&
		(sortBy == "" || sortBy == "relevance") &&
		mode != "keyword" &&
		!normalize.IsSKUOrShortQuery(query) &&
		offset < hybridMaxTopK &&
		s.store != nil &&
		s.embedder != nil

	if useHybrid {
		return s.searchHybrid(ctx, req, query, page, pageSize, offset)
	}

	return s.searchKeywordOnly(ctx, req, query, sortBy, page, pageSize, offset)
}

func (s *Server) searchVectorOnly(
	ctx context.Context,
	req *searchv1.SearchRequest,
	query string,
	page, pageSize int32,
	offset int,
) (*searchv1.SearchResponse, error) {
	if query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required when mode is vector")
	}
	if s.embedder == nil || s.store == nil {
		return nil, status.Error(codes.Unavailable, "vector search backend is not configured")
	}
	vec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "embed search query: %v", err)
	}
	fetchLimit := offset + int(pageSize)
	if fetchLimit < hybridMinTopK {
		fetchLimit = hybridMinTopK
	}
	if fetchLimit > hybridMaxTopK {
		fetchLimit = hybridMaxTopK
	}
	hits, err := s.store.VectorSearch(ctx, vec, store.VectorSearchFilter{
		ShopID:      req.GetShopId(),
		Category:    req.GetCategory(),
		MinPriceVND: req.GetMinPriceVnd(),
		MaxPriceVND: req.GetMaxPriceVnd(),
		InStockOnly: req.GetInStockOnly(),
		Limit:       fetchLimit,
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "vector search failed: %v", err)
	}
	paged := sliceVectorHits(hits, offset, int(pageSize))
	items := make([]*searchv1.SearchItem, len(paged))
	for i, h := range paged {
		items[i] = vectorHitToProto(h)
	}
	return &searchv1.SearchResponse{
		Items:    items,
		Total:    int64(len(hits)),
		Page:     page,
		PageSize: pageSize,
		Facets:   emptyFacets(),
	}, nil
}

func (s *Server) searchKeywordOnly(
	ctx context.Context,
	req *searchv1.SearchRequest,
	query, sortBy string,
	page, pageSize int32,
	offset int,
) (*searchv1.SearchResponse, error) {
	if s.meili == nil {
		return nil, status.Error(codes.Unavailable, "keyword search backend is not configured")
	}
	res, err := s.meili.Search(ctx, meili.SearchParams{
		Query:       query,
		ShopID:      req.GetShopId(),
		Category:    req.GetCategory(),
		MinPriceVND: req.GetMinPriceVnd(),
		MaxPriceVND: req.GetMaxPriceVnd(),
		InStockOnly: req.GetInStockOnly(),
		Sort:        sortBy,
		Limit:       int(pageSize),
		Offset:      offset,
		Facets:      defaultFacets,
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "keyword search failed: %v", err)
	}

	items := make([]*searchv1.SearchItem, len(res.Hits))
	for i, doc := range res.Hits {
		items[i] = meiliDocToProto(doc, doc.RankingScore)
	}
	return &searchv1.SearchResponse{
		Items:    items,
		Total:    res.TotalHits,
		Page:     page,
		PageSize: pageSize,
		Facets:   s.buildFacets(res.FacetDistribution, res.Hits),
	}, nil
}

func (s *Server) searchHybrid(
	ctx context.Context,
	req *searchv1.SearchRequest,
	query string,
	page, pageSize int32,
	offset int,
) (*searchv1.SearchResponse, error) {
	topK := int(page) * int(pageSize)
	if topK < hybridMinTopK {
		topK = hybridMinTopK
	}
	if topK > hybridMaxTopK {
		topK = hybridMaxTopK
	}

	var (
		meiliRes *meili.SearchResult
		meiliErr error
		vecHits  []store.VectorHit
		vecErr   error
		wg       sync.WaitGroup
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		if s.meili == nil {
			meiliErr = errors.New("meilisearch client not configured")
			return
		}
		meiliRes, meiliErr = s.meili.Search(ctx, meili.SearchParams{
			Query:       query,
			ShopID:      req.GetShopId(),
			Category:    req.GetCategory(),
			MinPriceVND: req.GetMinPriceVnd(),
			MaxPriceVND: req.GetMaxPriceVnd(),
			InStockOnly: req.GetInStockOnly(),
			Limit:       topK,
			Offset:      0,
			Facets:      defaultFacets,
		})
	}()

	go func() {
		defer wg.Done()
		vec, err := s.embedder.Embed(ctx, query)
		if err != nil {
			vecErr = err
			return
		}
		vecHits, vecErr = s.store.VectorSearch(ctx, vec, store.VectorSearchFilter{
			ShopID:      req.GetShopId(),
			Category:    req.GetCategory(),
			MinPriceVND: req.GetMinPriceVnd(),
			MaxPriceVND: req.GetMaxPriceVnd(),
			InStockOnly: req.GetInStockOnly(),
			Limit:       topK,
		})
	}()
	wg.Wait()

	if meiliErr != nil && vecErr != nil {
		s.logger.ErrorContext(ctx, "both keyword and vector search backends failed",
			slog.String("meili_error", meiliErr.Error()),
			slog.String("vector_error", vecErr.Error()),
		)
		return nil, status.Errorf(codes.Unavailable, "search backends unavailable: keyword=%v; vector=%v", meiliErr, vecErr)
	}

	// Graceful fallback: vector failed -> return Meilisearch results.
	if vecErr != nil {
		s.logger.WarnContext(ctx, "vector search degraded; falling back to keyword results",
			slog.String("error", vecErr.Error()),
		)
		paged := sliceMeiliDocs(meiliRes.Hits, offset, int(pageSize))
		items := make([]*searchv1.SearchItem, len(paged))
		for i, doc := range paged {
			items[i] = meiliDocToProto(doc, doc.RankingScore)
		}
		return &searchv1.SearchResponse{
			Items:    items,
			Total:    meiliRes.TotalHits,
			Page:     page,
			PageSize: pageSize,
			Facets:   s.buildFacets(meiliRes.FacetDistribution, meiliRes.Hits),
		}, nil
	}

	// Graceful fallback: Meilisearch failed -> return pgvector results.
	if meiliErr != nil {
		s.logger.WarnContext(ctx, "keyword search degraded; falling back to vector results",
			slog.String("error", meiliErr.Error()),
		)
		paged := sliceVectorHits(vecHits, offset, int(pageSize))
		items := make([]*searchv1.SearchItem, len(paged))
		for i, h := range paged {
			items[i] = vectorHitToProto(h)
		}
		return &searchv1.SearchResponse{
			Items:    items,
			Total:    int64(len(vecHits)),
			Page:     page,
			PageSize: pageSize,
			Facets:   emptyFacets(),
		}, nil
	}

	// Both succeeded -> Reciprocal Rank Fusion.
	kwIDs := make([]string, len(meiliRes.Hits))
	meiliByID := make(map[string]meili.Document, len(meiliRes.Hits))
	for i, doc := range meiliRes.Hits {
		kwIDs[i] = doc.ID
		meiliByID[doc.ID] = doc
	}

	vecIDs := make([]string, len(vecHits))
	vecByID := make(map[string]store.VectorHit, len(vecHits))
	for i, hit := range vecHits {
		vecIDs[i] = hit.Doc.ProductID
		vecByID[hit.Doc.ProductID] = hit
	}

	fused := rrf.Fuse(kwIDs, vecIDs, rrf.DefaultK, rrf.DefaultKeywordWeight, rrf.DefaultVectorWeight)
	total := meiliRes.TotalHits
	if int64(len(fused)) > total {
		total = int64(len(fused))
	}

	start := offset
	if start > len(fused) {
		start = len(fused)
	}
	end := start + int(pageSize)
	if end > len(fused) {
		end = len(fused)
	}

	items := make([]*searchv1.SearchItem, 0, end-start)
	for _, scored := range fused[start:end] {
		if doc, ok := meiliByID[scored.ID]; ok {
			items = append(items, meiliDocToProto(doc, scored.Score))
			continue
		}
		if hit, ok := vecByID[scored.ID]; ok {
			item := vectorHitToProto(hit)
			item.Score = scored.Score
			items = append(items, item)
		}
	}

	return &searchv1.SearchResponse{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Facets:   s.buildFacets(meiliRes.FacetDistribution, meiliRes.Hits),
	}, nil
}

// Suggest returns prefix/typo-tolerant autocomplete suggestions from Meilisearch.
func (s *Server) Suggest(ctx context.Context, req *searchv1.SuggestRequest) (*searchv1.SuggestResponse, error) {
	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return &searchv1.SuggestResponse{Suggestions: []*searchv1.Suggestion{}}, nil
	}
	if s.meili == nil {
		return nil, status.Error(codes.Unavailable, "suggest backend is not configured")
	}

	limit := req.GetLimit()
	if limit <= 0 {
		limit = defaultSuggestLimit
	}
	if limit > maxSuggestLimit {
		limit = maxSuggestLimit
	}

	raw, err := s.meili.Suggest(ctx, query, req.GetShopId(), int(limit))
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "suggest query failed: %v", err)
	}

	out := make([]*searchv1.Suggestion, len(raw))
	for i, sg := range raw {
		out[i] = &searchv1.Suggestion{
			Text:         sg.Text,
			Kind:         sg.Kind,
			ProductId:    sg.ProductID,
			CategorySlug: sg.CategorySlug,
			MinPriceVnd:  sg.MinPriceVND,
		}
	}
	return &searchv1.SuggestResponse{Suggestions: out}, nil
}

// SimilarProducts finds semantically similar products using pgvector cosine similarity.
func (s *Server) SimilarProducts(ctx context.Context, req *searchv1.SimilarProductsRequest) (*searchv1.SimilarProductsResponse, error) {
	if err := validatePriceRange(req.GetMinPriceVnd(), req.GetMaxPriceVnd()); err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, status.Error(codes.Unavailable, "vector store is not configured")
	}

	limit := req.GetLimit()
	if limit <= 0 {
		limit = defaultSimilarLimit
	}
	if limit > maxSimilarLimit {
		limit = maxSimilarLimit
	}

	var (
		queryVec  []float32
		excludeID string
	)

	if productID := strings.TrimSpace(req.GetProductId()); productID != "" {
		doc, err := s.store.GetDocument(ctx, productID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, status.Errorf(codes.NotFound, "product %s not found in search index", productID)
			}
			return nil, status.Errorf(codes.Internal, "load product embedding: %v", err)
		}
		queryVec = doc.Embedding
		excludeID = productID
	} else if query := strings.TrimSpace(req.GetQuery()); query != "" {
		if s.embedder == nil {
			return nil, status.Error(codes.Unavailable, "embedder is not configured")
		}
		vec, err := s.embedder.Embed(ctx, query)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "embed similar query: %v", err)
		}
		queryVec = vec
	} else {
		return nil, status.Error(codes.InvalidArgument, "either product_id or query is required")
	}

	hits, err := s.store.VectorSearch(ctx, queryVec, store.VectorSearchFilter{
		ShopID:           req.GetShopId(),
		Category:         req.GetCategory(),
		MinPriceVND:      req.GetMinPriceVnd(),
		MaxPriceVND:      req.GetMaxPriceVnd(),
		InStockOnly:      req.GetInStockOnly(),
		ExcludeProductID: excludeID,
		Limit:            int(limit),
	})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "similar products search failed: %v", err)
	}

	items := make([]*searchv1.SearchItem, len(hits))
	for i, h := range hits {
		items[i] = vectorHitToProto(h)
	}
	return &searchv1.SimilarProductsResponse{Items: items}, nil
}

func validatePriceRange(minPrice, maxPrice int64) error {
	if minPrice < 0 || maxPrice < 0 {
		return status.Error(codes.InvalidArgument, "price bounds must be non-negative")
	}
	if minPrice > 0 && maxPrice > 0 && minPrice > maxPrice {
		return status.Error(codes.InvalidArgument, "min_price_vnd must be less than or equal to max_price_vnd")
	}
	return nil
}

func (s *Server) buildFacets(dist map[string]map[string]int64, hits []meili.Document) *searchv1.SearchFacets {
	facets := emptyFacets()
	if len(dist) == 0 {
		return facets
	}

	catLabels := make(map[string]string)
	if s.categoryLabelsFn != nil {
		for k, v := range s.categoryLabelsFn() {
			catLabels[k] = v
		}
	}
	for _, h := range hits {
		if h.CategorySlug != "" && h.CategoryName != "" {
			catLabels[h.CategorySlug] = h.CategoryName
		}
	}

	if cats, ok := dist["category_slug"]; ok {
		facets.Categories = mapToSortedBuckets(cats, func(slug string) string {
			if lbl := catLabels[slug]; lbl != "" {
				return lbl
			}
			return slug
		})
	}
	if shops, ok := dist["shop_id"]; ok {
		facets.Shops = mapToSortedBuckets(shops, func(shopID string) string {
			return shopID
		})
	}
	if prices, ok := dist["price_bucket"]; ok {
		for _, bucket := range meili.PriceBucketOrder {
			if cnt := prices[bucket]; cnt > 0 {
				facets.PriceRanges = append(facets.PriceRanges, &searchv1.FacetBucket{
					Value: bucket,
					Label: meili.PriceBucketLabel(bucket),
					Count: cnt,
				})
			}
		}
	}
	return facets
}

func mapToSortedBuckets(counts map[string]int64, labelFn func(string) string) []*searchv1.FacetBucket {
	buckets := make([]*searchv1.FacetBucket, 0, len(counts))
	for val, cnt := range counts {
		if val == "" || cnt <= 0 {
			continue
		}
		buckets = append(buckets, &searchv1.FacetBucket{
			Value: val,
			Label: labelFn(val),
			Count: cnt,
		})
	}
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Count != buckets[j].Count {
			return buckets[i].Count > buckets[j].Count
		}
		return buckets[i].Value < buckets[j].Value
	})
	return buckets
}

func emptyFacets() *searchv1.SearchFacets {
	return &searchv1.SearchFacets{
		Categories:  []*searchv1.FacetBucket{},
		Shops:       []*searchv1.FacetBucket{},
		PriceRanges: []*searchv1.FacetBucket{},
	}
}

func meiliDocToProto(d meili.Document, score float64) *searchv1.SearchItem {
	imgs := make([]string, len(d.Images))
	copy(imgs, d.Images)
	var createdTS, updatedTS *timestamppb.Timestamp
	if d.CreatedAtUnix > 0 {
		createdTS = timestamppb.New(time.Unix(d.CreatedAtUnix, 0).UTC())
	}
	if d.UpdatedAtUnix > 0 {
		updatedTS = timestamppb.New(time.Unix(d.UpdatedAtUnix, 0).UTC())
	}
	return &searchv1.SearchItem{
		Id:           d.ID,
		ShopId:       d.ShopID,
		CategoryId:   d.CategoryID,
		CategorySlug: d.CategorySlug,
		CategoryName: d.CategoryName,
		Name:         d.Name,
		Description:  d.Description,
		Images:       imgs,
		Status:       d.Status,
		MinPriceVnd:  d.MinPriceVND,
		MaxPriceVnd:  d.MaxPriceVND,
		TotalStock:   d.TotalStock,
		InStock:      d.InStock,
		Score:        score,
		CreatedAt:    createdTS,
		UpdatedAt:    updatedTS,
	}
}

func vectorHitToProto(h store.VectorHit) *searchv1.SearchItem {
	d := h.Doc
	imgs := make([]string, len(d.Images))
	copy(imgs, d.Images)
	var createdTS, updatedTS *timestamppb.Timestamp
	if !d.ProductCreatedAt.IsZero() {
		createdTS = timestamppb.New(d.ProductCreatedAt.UTC())
	}
	if !d.ProductUpdatedAt.IsZero() {
		updatedTS = timestamppb.New(d.ProductUpdatedAt.UTC())
	}
	return &searchv1.SearchItem{
		Id:           d.ProductID,
		ShopId:       d.ShopID,
		CategoryId:   d.CategoryID,
		CategorySlug: d.CategorySlug,
		CategoryName: d.CategoryName,
		Name:         d.Name,
		Description:  d.Description,
		Images:       imgs,
		Status:       d.Status,
		MinPriceVnd:  d.MinPriceVND,
		MaxPriceVnd:  d.MaxPriceVND,
		TotalStock:   d.TotalStock,
		InStock:      d.InStock,
		Score:        h.Score,
		CreatedAt:    createdTS,
		UpdatedAt:    updatedTS,
	}
}

func sliceMeiliDocs(docs []meili.Document, offset, limit int) []meili.Document {
	if offset >= len(docs) {
		return []meili.Document{}
	}
	end := offset + limit
	if end > len(docs) {
		end = len(docs)
	}
	return docs[offset:end]
}

func sliceVectorHits(hits []store.VectorHit, offset, limit int) []store.VectorHit {
	if offset >= len(hits) {
		return []store.VectorHit{}
	}
	end := offset + limit
	if end > len(hits) {
		end = len(hits)
	}
	return hits[offset:end]
}
