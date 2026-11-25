package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/textnorm"
	searchcfg "github.com/nexusmart/core/services/search/config"
	"github.com/nexusmart/core/services/search/internal/config"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/nexusmart/core/services/search/internal/embedder"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultPageSize     = 20
	maxPageSize         = 100
	defaultSuggestLimit = 8
	maxSuggestLimit     = 20
	defaultSimilarLimit = 10
	maxSimilarLimit     = 50
	defaultHistoryLimit = 20
	maxHistoryLimit     = 50
	defaultHybridLimit  = 10
	maxHybridLimit      = 50
	maxQueryRunes       = 200

	rrfKeywordWeight = 0.6
	rrfVectorWeight  = 0.4
)

var skuCodePattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9-]+$`)

type searchBackend interface {
	ListSuspendedShopIDs(ctx context.Context) ([]string, error)
	GetCategoryBySlug(ctx context.Context, slug string) (db.SearchCategory, error)
	GetShopBySlug(ctx context.Context, slug string) (db.SearchShop, error)
	ListCategoriesByIDs(ctx context.Context, ids []string) ([]db.SearchCategory, error)
	ListShopsByIDs(ctx context.Context, ids []string) ([]db.SearchShop, error)
	ListAllCategories(ctx context.Context) ([]db.SearchCategory, error)
	ListCategoriesByShop(ctx context.Context, shopID string) ([]db.SearchCategory, error)
	SuggestCategories(ctx context.Context, arg db.SuggestCategoriesParams) ([]db.SuggestCategoriesRow, error)
	SuggestCategoriesByShop(ctx context.Context, arg db.SuggestCategoriesByShopParams) ([]db.SuggestCategoriesByShopRow, error)
	SuggestActiveShops(ctx context.Context, arg db.SuggestActiveShopsParams) ([]db.SuggestActiveShopsRow, error)
	GetProductEmbeddingForSimilar(ctx context.Context, productID string) (db.GetProductEmbeddingForSimilarRow, error)
	ListSearchHistory(ctx context.Context, arg db.ListSearchHistoryParams) ([]db.SearchHistory, error)
	ClearSearchHistory(ctx context.Context, userID string) error
	DeleteSearchHistoryItem(ctx context.Context, arg db.DeleteSearchHistoryItemParams) (int64, error)
	GetReindexJob(ctx context.Context, jobID string) (db.ReindexJob, error)
	SaveSearchHistory(ctx context.Context, userID, query, queryFolded string, maxItems int32) error
	SearchVector(ctx context.Context, p store.VectorSearchParams) ([]store.VectorHit, error)
	SuggestFallbackProducts(ctx context.Context, shopID, queryFolded string, limit int32) ([]store.VectorHit, error)
}

type trendingBackend interface {
	RecordForShop(ctx context.Context, shopID, normalizedQuery string) error
	TopForShop(ctx context.Context, shopID, queryFolded string, limit int) ([]string, error)
}

type storeAdapter struct {
	st *store.Store
}

func (a *storeAdapter) ListSuspendedShopIDs(ctx context.Context) ([]string, error) {
	return a.st.Queries().ListSuspendedShopIDs(ctx)
}
func (a *storeAdapter) GetCategoryBySlug(ctx context.Context, slug string) (db.SearchCategory, error) {
	return a.st.Queries().GetCategoryBySlug(ctx, slug)
}
func (a *storeAdapter) GetShopBySlug(ctx context.Context, slug string) (db.SearchShop, error) {
	return a.st.Queries().GetShopBySlug(ctx, slug)
}
func (a *storeAdapter) ListCategoriesByIDs(ctx context.Context, ids []string) ([]db.SearchCategory, error) {
	return a.st.Queries().ListCategoriesByIDs(ctx, ids)
}
func (a *storeAdapter) ListShopsByIDs(ctx context.Context, ids []string) ([]db.SearchShop, error) {
	return a.st.Queries().ListShopsByIDs(ctx, ids)
}
func (a *storeAdapter) ListAllCategories(ctx context.Context) ([]db.SearchCategory, error) {
	return a.st.Queries().ListAllCategories(ctx)
}
func (a *storeAdapter) ListCategoriesByShop(ctx context.Context, shopID string) ([]db.SearchCategory, error) {
	return a.st.Queries().ListCategoriesByShop(ctx, shopID)
}
func (a *storeAdapter) SuggestCategories(ctx context.Context, arg db.SuggestCategoriesParams) ([]db.SuggestCategoriesRow, error) {
	return a.st.Queries().SuggestCategories(ctx, arg)
}
func (a *storeAdapter) SuggestCategoriesByShop(ctx context.Context, arg db.SuggestCategoriesByShopParams) ([]db.SuggestCategoriesByShopRow, error) {
	return a.st.Queries().SuggestCategoriesByShop(ctx, arg)
}
func (a *storeAdapter) SuggestActiveShops(ctx context.Context, arg db.SuggestActiveShopsParams) ([]db.SuggestActiveShopsRow, error) {
	return a.st.Queries().SuggestActiveShops(ctx, arg)
}
func (a *storeAdapter) GetProductEmbeddingForSimilar(ctx context.Context, productID string) (db.GetProductEmbeddingForSimilarRow, error) {
	return a.st.Queries().GetProductEmbeddingForSimilar(ctx, productID)
}
func (a *storeAdapter) ListSearchHistory(ctx context.Context, arg db.ListSearchHistoryParams) ([]db.SearchHistory, error) {
	return a.st.Queries().ListSearchHistory(ctx, arg)
}
func (a *storeAdapter) ClearSearchHistory(ctx context.Context, userID string) error {
	return a.st.Queries().ClearSearchHistory(ctx, userID)
}
func (a *storeAdapter) DeleteSearchHistoryItem(ctx context.Context, arg db.DeleteSearchHistoryItemParams) (int64, error) {
	return a.st.Queries().DeleteSearchHistoryItem(ctx, arg)
}
func (a *storeAdapter) GetReindexJob(ctx context.Context, jobID string) (db.ReindexJob, error) {
	return a.st.Queries().GetReindexJob(ctx, jobID)
}
func (a *storeAdapter) SaveSearchHistory(ctx context.Context, userID, query, queryFolded string, maxItems int32) error {
	return a.st.SaveSearchHistory(ctx, userID, query, queryFolded, maxItems)
}
func (a *storeAdapter) SearchVector(ctx context.Context, p store.VectorSearchParams) ([]store.VectorHit, error) {
	return a.st.SearchVector(ctx, p)
}
func (a *storeAdapter) SuggestFallbackProducts(ctx context.Context, shopID, queryFolded string, limit int32) ([]store.VectorHit, error) {
	return a.st.SuggestFallbackProducts(ctx, shopID, queryFolded, limit)
}

// Options configures SearchService runtime tuning.
type Options struct {
	RRFK                int
	MeiliTimeout        time.Duration
	VectorTimeout       time.Duration
	VectorMinSimilarity float64
	Synonyms            map[string][]string
	Logger              *slog.Logger
}

// Service implements the core business logic for all 8 SearchService RPCs.
type Service struct {
	store               searchBackend
	trending            trendingBackend
	meili               meili.Engine
	embedder            embedder.Embedder
	indexer             *indexer.Indexer
	rrfK                int
	meiliTimeout        time.Duration
	vectorTimeout       time.Duration
	vectorMinSimilarity float64
	synonyms            map[string][]string
	logger              *slog.Logger
}

// New constructs a Search Service.
func New(
	st *store.Store,
	trending *store.TrendingStore,
	meiliEngine meili.Engine,
	emb embedder.Embedder,
	idx *indexer.Indexer,
	opts Options,
) *Service {
	var sb searchBackend
	if st != nil {
		sb = &storeAdapter{st: st}
	}
	var tb trendingBackend
	if trending != nil {
		tb = trending
	}
	return newWithBackends(sb, tb, meiliEngine, emb, idx, opts)
}

func newWithBackends(
	st searchBackend,
	trending trendingBackend,
	meiliEngine meili.Engine,
	emb embedder.Embedder,
	idx *indexer.Indexer,
	opts Options,
) *Service {
	rrfK := opts.RRFK
	if rrfK <= 0 {
		rrfK = 60
	}
	meiliTimeout := opts.MeiliTimeout
	if meiliTimeout <= 0 {
		meiliTimeout = 1500 * time.Millisecond
	}
	vectorTimeout := opts.VectorTimeout
	if vectorTimeout <= 0 {
		vectorTimeout = 800 * time.Millisecond
	}
	minSim := opts.VectorMinSimilarity
	if minSim <= 0 {
		minSim = config.DefaultVectorMinSimilarity
	}
	syns := opts.Synonyms
	if syns == nil {
		syns = searchcfg.MustLoadSynonyms()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:               st,
		trending:            trending,
		meili:               meiliEngine,
		embedder:            emb,
		indexer:             idx,
		rrfK:                rrfK,
		meiliTimeout:        meiliTimeout,
		vectorTimeout:       vectorTimeout,
		vectorMinSimilarity: minSim,
		synonyms:            syns,
		logger:              logger,
	}
}

// ShouldUseHybrid determines whether SearchProducts runs Hybrid RRF (UC-AI02 / G9):
// mode=HYBRID forces RRF; mode=AUTO runs RRF when normalized query has >= 3 words and is not a SKU code.
func ShouldUseHybrid(mode searchv1.SearchMode, qFolded string) bool {
	if qFolded == "" {
		return false
	}
	switch mode {
	case searchv1.SearchMode_SEARCH_MODE_KEYWORD:
		return false
	case searchv1.SearchMode_SEARCH_MODE_HYBRID:
		return true
	default:
		if skuCodePattern.MatchString(qFolded) {
			return false
		}
		return len(strings.Fields(qFolded)) >= 3
	}
}

// SearchProducts implements public/optional-auth catalog search with Meilisearch + Hybrid RRF (G9–G14, S5–S9, S11, S14).
func (s *Service) SearchProducts(ctx context.Context, req *searchv1.SearchProductsRequest) (*searchv1.SearchProductsResponse, error) {
	if req == nil {
		req = &searchv1.SearchProductsRequest{}
	}

	page, pageSize, err := normalizePagination(req.GetPage(), req.GetPageSize())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.GetMinPriceVnd() < 0 || req.GetMaxPriceVnd() < 0 {
		return nil, status.Error(codes.InvalidArgument, "price bounds must be non-negative")
	}
	if req.GetMinPriceVnd() > 0 && req.GetMaxPriceVnd() > 0 && req.GetMinPriceVnd() > req.GetMaxPriceVnd() {
		return nil, status.Error(codes.InvalidArgument, "min_price_vnd must not exceed max_price_vnd")
	}
	if req.GetMinRatingX100() < 0 || req.GetMinRatingX100() > 500 {
		return nil, status.Error(codes.InvalidArgument, "min_rating_x100 must be between 0 and 500")
	}

	sortBy := strings.ToLower(strings.TrimSpace(req.GetSortBy()))
	switch sortBy {
	case "", "relevance", "price_asc", "price_desc", "newest", "rating_desc":
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported sort_by")
	}

	categoryID := strings.TrimSpace(req.GetCategoryId())
	categorySlug := strings.TrimSpace(req.GetCategorySlug())
	shopID := strings.TrimSpace(req.GetShopId())
	shopSlug := strings.TrimSpace(req.GetShopSlug())
	rawSize := strings.TrimSpace(req.GetSize())
	rawColor := strings.TrimSpace(req.GetColor())

	if err := meili.ValidateIdentifier("category_id", categoryID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := meili.ValidateSlug("category_slug", categorySlug); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := meili.ValidateIdentifier("shop_id", shopID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := meili.ValidateSlug("shop_slug", shopSlug); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := meili.ValidateAttrFilterValue("size", rawSize); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := meili.ValidateAttrFilterValue("color", rawColor); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	sizeFolded := textnorm.NormalizeSearchText(rawSize)
	colorFolded := textnorm.NormalizeSearchText(rawColor)
	rawQ := trimQuery(req.GetQ())
	qFolded := textnorm.NormalizeSearchText(rawQ)

	// Load suspended shops immediately from Postgres (G10: < 5s enforcement across all read paths).
	suspendedIDs, suspendedSet, err := s.loadSuspendedShops(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load shop visibility: %v", err)
	}

	// Resolve category_slug if needed.
	if categoryID == "" && categorySlug != "" {
		if s.store == nil {
			return nil, status.Error(codes.Unavailable, "search store unavailable")
		}
		cat, catErr := s.store.GetCategoryBySlug(ctx, categorySlug)
		if errors.Is(catErr, pgx.ErrNoRows) {
			return emptySearchResponse(page, pageSize), nil
		}
		if catErr != nil {
			return nil, status.Errorf(codes.Internal, "resolve category_slug: %v", catErr)
		}
		categoryID = cat.CategoryID
	}

	// Resolve shop_slug if needed.
	if shopID == "" && shopSlug != "" {
		if s.store == nil {
			return nil, status.Error(codes.Unavailable, "search store unavailable")
		}
		sh, shErr := s.store.GetShopBySlug(ctx, shopSlug)
		if errors.Is(shErr, pgx.ErrNoRows) {
			return emptySearchResponse(page, pageSize), nil
		}
		if shErr != nil {
			return nil, status.Errorf(codes.Internal, "resolve shop_slug: %v", shErr)
		}
		if strings.EqualFold(sh.Status, "SUSPENDED") {
			return emptySearchResponse(page, pageSize), nil
		}
		shopID = sh.ShopID
	}
	if shopID != "" && suspendedSet[shopID] {
		return emptySearchResponse(page, pageSize), nil
	}

	if s.meili == nil {
		return nil, status.Error(codes.Unavailable, "search engine unavailable")
	}

	// S6: Non-relevance sorting uses keyword leg only so vector neighbors never distort price/rating/newest order.
	isRelevanceSort := sortBy == "" || sortBy == "relevance"
	useHybrid := ShouldUseHybrid(req.GetMode(), qFolded) && isRelevanceSort

	baseMeiliQuery := meili.SearchQuery{
		Query:           qFolded,
		CategoryID:      categoryID,
		ShopID:          shopID,
		ExcludedShopIDs: suspendedIDs,
		MinPriceVND:     req.GetMinPriceVnd(),
		MaxPriceVND:     req.GetMaxPriceVnd(),
		MinRatingX100:   req.GetMinRatingX100(),
		InStockOnly:     req.GetInStockOnly(),
		SizeFolded:      sizeFolded,
		ColorFolded:     colorFolded,
		Facets:          []string{"category_ancestor_ids", "shop_id"},
	}

	var (
		hits         []*searchv1.SearchProductHit
		total        int64
		facetDist    map[string]map[string]int64
		usedVector   bool
		degradedMode string
	)

	if !useHybrid {
		kwQuery := baseMeiliQuery
		kwQuery.SortBy = sortBy
		kwQuery.Limit = int64(pageSize)
		kwQuery.Offset = int64(page-1) * int64(pageSize)

		mRes, mErr := s.runMeiliSearch(ctx, kwQuery)
		if mErr != nil {
			return nil, status.Error(codes.Unavailable, "search engine unavailable")
		}
		facetDist = mRes.FacetDistribution
		hits = filterPublicMeiliHits(mRes.Hits, suspendedSet, shopID, false)
		total = adjustTotal(mRes.Total, len(mRes.Hits), len(hits))
	} else {
		candidateLimit := int64(page) * int64(pageSize)
		if candidateLimit < 100 {
			candidateLimit = 100
		}
		if candidateLimit > 200 {
			candidateLimit = 200
		}

		kwQuery := baseMeiliQuery
		kwQuery.SortBy = ""
		kwQuery.Limit = candidateLimit
		kwQuery.Offset = 0

		var (
			mRes *meili.SearchResult
			mErr error
			vRes []store.VectorHit
			vErr error
			wg   sync.WaitGroup
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			mRes, mErr = s.runMeiliSearch(ctx, kwQuery)
		}()
		go func() {
			defer wg.Done()
			vRes, vErr = s.runVectorSearch(ctx, rawQ, store.VectorSearchParams{
				CategoryID:    categoryID,
				ShopID:        shopID,
				MinPriceVND:   req.GetMinPriceVnd(),
				MaxPriceVND:   req.GetMaxPriceVnd(),
				MinRatingX100: req.GetMinRatingX100(),
				InStockOnly:   req.GetInStockOnly(),
				SizeFolded:    sizeFolded,
				ColorFolded:   colorFolded,
				MinSimilarity: s.vectorMinSimilarity,
				Limit:         100,
			})
		}()
		wg.Wait()

		if mErr != nil {
			return nil, status.Error(codes.Unavailable, "search engine unavailable")
		}
		facetDist = mRes.FacetDistribution

		if vErr != nil {
			// G12/S7: Embedder/vector failure degrades to keyword_only using direct Meilisearch offset pagination
			// (never truncating at the 100/200 hybrid candidate cap).
			s.logger.WarnContext(ctx, "vector leg failed during hybrid search; falling back to keyword_only",
				slog.String("error", vErr.Error()),
			)
			degradedMode = "keyword_only"
			usedVector = false

			fbQuery := baseMeiliQuery
			fbQuery.SortBy = sortBy
			fbQuery.Limit = int64(pageSize)
			fbQuery.Offset = int64(page-1) * int64(pageSize)
			fbRes, fbErr := s.runMeiliSearch(ctx, fbQuery)
			if fbErr != nil {
				return nil, status.Error(codes.Unavailable, "search engine unavailable")
			}
			facetDist = fbRes.FacetDistribution
			hits = filterPublicMeiliHits(fbRes.Hits, suspendedSet, shopID, false)
			total = adjustTotal(fbRes.Total, len(fbRes.Hits), len(hits))
		} else {
			usedVector = true
			fused := s.fuseRRF(mRes.Hits, vRes, suspendedSet, shopID, false)
			// S7: Total equals actual fused candidate count so every page within total is populated.
			hits, total = paginateHits(fused, int64(len(fused)), page, pageSize)
		}
	}

	catFacets, shopFacets := s.buildFacets(ctx, facetDist, suspendedSet)

	didYouMean := ""
	if total == 0 && qFolded != "" {
		didYouMean = s.computeDidYouMean(ctx, qFolded, shopID)
	}

	// Record search history (end-user JWT only, <= 50 items/user, never log raw query — G14)
	// and per-shop trending (S5: only when normalized query is a substring of a page-1 product/category/shop name).
	if qFolded != "" && page <= 1 {
		if len(hits) > 0 && s.trending != nil && matchesCatalogVocabulary(qFolded, hits, catFacets, shopFacets) {
			tCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			_ = s.trending.RecordForShop(tCtx, shopID, qFolded)
			cancel()
		}
		if s.store != nil {
			if claims, ok := auth.ClaimsFromContext(ctx); ok && claims.Service == "" && claims.UserID() != "" {
				if err := s.store.SaveSearchHistory(ctx, claims.UserID(), rawQ, qFolded, maxHistoryLimit); err != nil {
					// G14: Never log raw query text.
					s.logger.WarnContext(ctx, "failed to save search history",
						slog.String("user_id", claims.UserID()),
						slog.String("error", err.Error()),
					)
				}
			}
		}
	}

	return &searchv1.SearchProductsResponse{
		Items:          hits,
		Total:          total,
		Page:           page,
		PageSize:       pageSize,
		CategoryFacets: catFacets,
		ShopFacets:     shopFacets,
		DidYouMean:     didYouMean,
		DegradedMode:   degradedMode,
		UsedVector:     usedVector,
	}, nil
}

// HybridSearch serves B3 RAG retrieval (service token or authenticated JWT; public active non-suspended products only,
// isolated to shop_id when supplied).
func (s *Service) HybridSearch(ctx context.Context, req *searchv1.HybridSearchRequest) (*searchv1.HybridSearchResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if req == nil {
		req = &searchv1.HybridSearchRequest{}
	}

	limit := req.GetLimit()
	if limit <= 0 {
		limit = defaultHybridLimit
	}
	if limit > maxHybridLimit {
		limit = maxHybridLimit
	}
	if req.GetMinPriceVnd() < 0 || req.GetMaxPriceVnd() < 0 {
		return nil, status.Error(codes.InvalidArgument, "price bounds must be non-negative")
	}
	if req.GetMinPriceVnd() > 0 && req.GetMaxPriceVnd() > 0 && req.GetMinPriceVnd() > req.GetMaxPriceVnd() {
		return nil, status.Error(codes.InvalidArgument, "min_price_vnd must not exceed max_price_vnd")
	}
	categoryID := strings.TrimSpace(req.GetCategoryId())
	if err := meili.ValidateIdentifier("category_id", categoryID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	shopID := strings.TrimSpace(req.GetShopId())
	if err := meili.ValidateIdentifier("shop_id", shopID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	rawQ := trimQuery(req.GetQuery())
	qFolded := textnorm.NormalizeSearchText(rawQ)
	onlyApprovedInStock := !req.GetIncludeOutOfStock()

	suspendedIDs, suspendedSet, err := s.loadSuspendedShops(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load shop visibility: %v", err)
	}
	if shopID != "" && suspendedSet[shopID] {
		return &searchv1.HybridSearchResponse{Hits: []*searchv1.SearchProductHit{}}, nil
	}

	var (
		mRes *meili.SearchResult
		mErr error
		vRes []store.VectorHit
		vErr error
		wg   sync.WaitGroup
	)

	if s.meili != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mRes, mErr = s.runMeiliSearch(ctx, meili.SearchQuery{
				Query:               qFolded,
				CategoryID:          categoryID,
				ShopID:              shopID,
				ExcludedShopIDs:     suspendedIDs,
				MinPriceVND:         req.GetMinPriceVnd(),
				MaxPriceVND:         req.GetMaxPriceVnd(),
				OnlyApprovedInStock: onlyApprovedInStock,
				Limit:               int64(maxInt32(limit*2, 50)),
				Offset:              0,
			})
		}()
	} else {
		mErr = errors.New("meilisearch client not configured")
	}

	if qFolded != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vRes, vErr = s.runVectorSearch(ctx, rawQ, store.VectorSearchParams{
				CategoryID:          categoryID,
				ShopID:              shopID,
				MinPriceVND:         req.GetMinPriceVnd(),
				MaxPriceVND:         req.GetMaxPriceVnd(),
				OnlyApprovedInStock: onlyApprovedInStock,
				MinSimilarity:       s.vectorMinSimilarity,
				Limit:               maxInt32(limit*2, 50),
			})
		}()
	}
	wg.Wait()

	if mErr != nil && (qFolded == "" || vErr != nil) {
		return nil, status.Error(codes.Unavailable, "search backends unavailable")
	}

	var (
		hits         []*searchv1.SearchProductHit
		usedVector   bool
		degradedMode string
	)

	switch {
	case mErr == nil && qFolded != "" && vErr == nil:
		usedVector = true
		hits = s.fuseRRF(mRes.Hits, vRes, suspendedSet, shopID, onlyApprovedInStock)
	case mErr == nil && qFolded != "" && vErr != nil:
		s.logger.WarnContext(ctx, "vector leg failed during HybridSearch; returning keyword_only",
			slog.String("error", vErr.Error()),
		)
		degradedMode = "keyword_only"
		hits = filterPublicMeiliHits(mRes.Hits, suspendedSet, shopID, onlyApprovedInStock)
	case mErr == nil:
		hits = filterPublicMeiliHits(mRes.Hits, suspendedSet, shopID, onlyApprovedInStock)
	default:
		s.logger.WarnContext(ctx, "meilisearch failed during HybridSearch; returning vector_only",
			slog.String("error", mErr.Error()),
		)
		usedVector = true
		degradedMode = "vector_only"
		hits = filterPublicVectorHits(vRes, suspendedSet, shopID, onlyApprovedInStock)
	}

	if len(hits) > int(limit) {
		hits = hits[:limit]
	}

	return &searchv1.HybridSearchResponse{
		Hits:         hits,
		UsedVector:   usedVector,
		DegradedMode: degradedMode,
	}, nil
}

// Suggest returns fast (< 80 ms target) keywords, categories, active shops (omitted when shop_id is set),
// per-shop Redis trending, and matching products strictly isolated to shop_id when provided.
func (s *Service) Suggest(ctx context.Context, req *searchv1.SuggestRequest) (*searchv1.SuggestResponse, error) {
	if req == nil {
		req = &searchv1.SuggestRequest{}
	}
	shopID := strings.TrimSpace(req.GetShopId())
	if err := meili.ValidateIdentifier("shop_id", shopID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultSuggestLimit
	}
	if limit > maxSuggestLimit {
		limit = maxSuggestLimit
	}
	entityLimit := limit
	if entityLimit > 5 {
		entityLimit = 5
	}

	rawQ := trimQuery(req.GetQuery())
	qFolded := textnorm.NormalizeSearchText(rawQ)
	qLike := textnorm.EscapeLikePattern(qFolded)

	suspendedIDs, suspendedSet, err := s.loadSuspendedShops(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load shop visibility: %v", err)
	}
	if shopID != "" && suspendedSet[shopID] {
		return &searchv1.SuggestResponse{}, nil
	}

	var (
		categories []*searchv1.SuggestEntity
		shops      []*searchv1.SuggestEntity
		trending   []string
		products   []*searchv1.SearchProductHit
		wg         sync.WaitGroup
	)

	wg.Add(4)
	go func() {
		defer wg.Done()
		if s.store == nil {
			return
		}
		if shopID != "" {
			rows, qErr := s.store.SuggestCategoriesByShop(ctx, db.SuggestCategoriesByShopParams{
				ShopID:  shopID,
				Column2: qLike,
				Limit:   int32(entityLimit),
			})
			if qErr != nil {
				return
			}
			categories = make([]*searchv1.SuggestEntity, 0, len(rows))
			for _, r := range rows {
				categories = append(categories, &searchv1.SuggestEntity{
					Id:   r.CategoryID,
					Name: r.Name,
					Slug: r.Slug,
				})
			}
			return
		}
		rows, qErr := s.store.SuggestCategories(ctx, db.SuggestCategoriesParams{
			Column1: qLike,
			Limit:   int32(entityLimit),
		})
		if qErr != nil {
			return
		}
		categories = make([]*searchv1.SuggestEntity, 0, len(rows))
		for _, r := range rows {
			categories = append(categories, &searchv1.SuggestEntity{
				Id:   r.CategoryID,
				Name: r.Name,
				Slug: r.Slug,
			})
		}
	}()

	go func() {
		defer wg.Done()
		// Storefront isolation: never suggest other shops when scoped to a single shop_id.
		if shopID != "" || s.store == nil {
			return
		}
		// G10: SuggestActiveShops strictly filters status = 'ACTIVE' (excludes SUSPENDED).
		rows, qErr := s.store.SuggestActiveShops(ctx, db.SuggestActiveShopsParams{
			Column1: qLike,
			Limit:   int32(entityLimit),
		})
		if qErr != nil {
			return
		}
		shops = make([]*searchv1.SuggestEntity, 0, len(rows))
		for _, r := range rows {
			if suspendedSet[r.ShopID] {
				continue
			}
			shops = append(shops, &searchv1.SuggestEntity{
				Id:   r.ShopID,
				Name: r.Name,
				Slug: r.Slug,
			})
		}
	}()

	go func() {
		defer wg.Done()
		if s.trending == nil {
			return
		}
		tCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		if vals, tErr := s.trending.TopForShop(tCtx, shopID, qFolded, limit); tErr == nil {
			trending = vals
		}
	}()

	go func() {
		defer wg.Done()
		if s.meili != nil {
			mRes, mErr := s.runMeiliSearch(ctx, meili.SearchQuery{
				Query:           qFolded,
				ShopID:          shopID,
				ExcludedShopIDs: suspendedIDs,
				Limit:           int64(entityLimit),
				Offset:          0,
			})
			if mErr == nil {
				products = filterPublicMeiliHits(mRes.Hits, suspendedSet, shopID, false)
				return
			}
		}
		if s.store != nil {
			if fb, fbErr := s.store.SuggestFallbackProducts(ctx, shopID, qFolded, int32(entityLimit)); fbErr == nil {
				products = filterPublicVectorHits(fb, suspendedSet, shopID, false)
			}
		}
	}()

	wg.Wait()

	keywords := buildKeywordSuggestions(qFolded, products, trending, s.synonyms, limit)

	return &searchv1.SuggestResponse{
		Keywords:   keywords,
		Categories: categories,
		Shops:      shops,
		Trending:   trending,
		Products:   products,
	}, nil
}

// SimilarProducts finds semantically similar public non-suspended products using pgvector cosine similarity,
// scoped to shop_id when provided and excluding the source product_id.
func (s *Service) SimilarProducts(ctx context.Context, req *searchv1.SimilarProductsRequest) (*searchv1.SimilarProductsResponse, error) {
	if req == nil {
		req = &searchv1.SimilarProductsRequest{}
	}
	if req.GetMinPriceVnd() < 0 || req.GetMaxPriceVnd() < 0 {
		return nil, status.Error(codes.InvalidArgument, "price bounds must be non-negative")
	}
	if req.GetMinPriceVnd() > 0 && req.GetMaxPriceVnd() > 0 && req.GetMinPriceVnd() > req.GetMaxPriceVnd() {
		return nil, status.Error(codes.InvalidArgument, "min_price_vnd must not exceed max_price_vnd")
	}

	productID := strings.TrimSpace(req.GetProductId())
	shopID := strings.TrimSpace(req.GetShopId())
	category := strings.TrimSpace(req.GetCategory())
	rawQ := trimQuery(req.GetQuery())

	if err := meili.ValidateIdentifier("product_id", productID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := meili.ValidateIdentifier("shop_id", shopID); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := meili.ValidateIdentifier("category", category); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if productID == "" && rawQ == "" {
		return nil, status.Error(codes.InvalidArgument, "either product_id or query is required")
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

	_, suspendedSet, err := s.loadSuspendedShops(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load shop visibility: %v", err)
	}
	if shopID != "" && suspendedSet[shopID] {
		return &searchv1.SimilarProductsResponse{Items: []*searchv1.SearchProductHit{}}, nil
	}

	categoryID := ""
	if category != "" {
		cat, catErr := s.store.GetCategoryBySlug(ctx, category)
		if catErr == nil {
			categoryID = cat.CategoryID
		} else if errors.Is(catErr, pgx.ErrNoRows) {
			categoryID = category
		} else {
			return nil, status.Errorf(codes.Internal, "resolve category: %v", catErr)
		}
	}

	callCtx, cancel := context.WithTimeout(ctx, s.vectorTimeout)
	defer cancel()

	var (
		queryVec  []float32
		excludeID string
	)

	if productID != "" {
		src, srcErr := s.store.GetProductEmbeddingForSimilar(callCtx, productID)
		if errors.Is(srcErr, pgx.ErrNoRows) {
			return nil, status.Errorf(codes.NotFound, "product %s not found in search index", productID)
		}
		if srcErr != nil {
			return nil, status.Errorf(codes.Internal, "load product embedding: %v", srcErr)
		}
		if src.Deleted ||
			!indexer.IsPublicProductStatus(src.Status) ||
			strings.EqualFold(src.ShopStatus, "SUSPENDED") ||
			suspendedSet[src.ShopID] ||
			(shopID != "" && src.ShopID != shopID) {
			return nil, status.Errorf(codes.NotFound, "product %s not found in search index", productID)
		}
		excludeID = productID
		if src.Embedding != nil && len(src.Embedding.Slice()) > 0 {
			queryVec = src.Embedding.Slice()
		} else if rawQ != "" && s.embedder != nil {
			vec, embErr := s.embedder.Embed(callCtx, rawQ)
			if embErr != nil {
				s.logger.WarnContext(ctx, "embedder failed during SimilarProducts fallback",
					slog.String("error", embErr.Error()),
				)
				return nil, status.Error(codes.Unavailable, "similar products embedder unavailable")
			}
			queryVec = vec
		} else {
			return &searchv1.SimilarProductsResponse{Items: []*searchv1.SearchProductHit{}}, nil
		}
	} else {
		if s.embedder == nil {
			return nil, status.Error(codes.Unavailable, "embedder is not configured")
		}
		vec, embErr := s.embedder.Embed(callCtx, rawQ)
		if embErr != nil {
			// G14: Never log raw query text.
			s.logger.WarnContext(ctx, "embedder failed during SimilarProducts",
				slog.String("error", embErr.Error()),
			)
			return nil, status.Error(codes.Unavailable, "similar products embedder unavailable")
		}
		queryVec = vec
	}

	hits, vecErr := s.store.SearchVector(callCtx, store.VectorSearchParams{
		Embedding:        queryVec,
		CategoryID:       categoryID,
		ShopID:           shopID,
		ExcludeProductID: excludeID,
		MinPriceVND:      req.GetMinPriceVnd(),
		MaxPriceVND:      req.GetMaxPriceVnd(),
		InStockOnly:      req.GetInStockOnly(),
		MinSimilarity:    0,
		Limit:            limit,
	})
	if vecErr != nil {
		s.logger.WarnContext(ctx, "vector search failed during SimilarProducts",
			slog.String("error", vecErr.Error()),
		)
		return nil, status.Error(codes.Unavailable, "similar products search unavailable")
	}

	filtered := filterPublicVectorHits(hits, suspendedSet, shopID, false)
	items := make([]*searchv1.SearchProductHit, 0, len(filtered))
	for _, it := range filtered {
		if excludeID != "" && it.GetId() == excludeID {
			continue
		}
		items = append(items, it)
	}
	return &searchv1.SimilarProductsResponse{Items: items}, nil
}

// ListSearchHistory lists the authenticated end-user's search history (<= 50 items, G14).
func (s *Service) ListSearchHistory(ctx context.Context, req *searchv1.ListSearchHistoryRequest) (*searchv1.ListSearchHistoryResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, status.Error(codes.Unavailable, "search store unavailable")
	}
	limit := req.GetLimit()
	if limit <= 0 {
		limit = defaultHistoryLimit
	}
	if limit > maxHistoryLimit {
		limit = maxHistoryLimit
	}

	rows, dbErr := s.store.ListSearchHistory(ctx, db.ListSearchHistoryParams{
		UserID: claims.UserID(),
		Limit:  limit,
	})
	if dbErr != nil {
		return nil, status.Errorf(codes.Internal, "list search history: %v", dbErr)
	}

	items := make([]*searchv1.SearchHistoryItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &searchv1.SearchHistoryItem{
			Id:        r.ID,
			Query:     r.Query,
			CreatedAt: timestamppb.New(r.CreatedAt),
		})
	}
	return &searchv1.ListSearchHistoryResponse{Items: items}, nil
}

// DeleteSearchHistory deletes one item (strictly scoped to caller's user_id) or clears all (G14).
func (s *Service) DeleteSearchHistory(ctx context.Context, req *searchv1.DeleteSearchHistoryRequest) (*searchv1.DeleteSearchHistoryResponse, error) {
	claims, err := requireEndUserClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if s.store == nil {
		return nil, status.Error(codes.Unavailable, "search store unavailable")
	}

	if req.GetClearAll() {
		if dbErr := s.store.ClearSearchHistory(ctx, claims.UserID()); dbErr != nil {
			return nil, status.Errorf(codes.Internal, "clear search history: %v", dbErr)
		}
		return &searchv1.DeleteSearchHistoryResponse{Ok: true}, nil
	}

	id := strings.TrimSpace(req.GetId())
	if id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required when clear_all is false")
	}
	if _, dbErr := s.store.DeleteSearchHistoryItem(ctx, db.DeleteSearchHistoryItemParams{
		ID:     id,
		UserID: claims.UserID(),
	}); dbErr != nil {
		return nil, status.Errorf(codes.Internal, "delete search history item: %v", dbErr)
	}
	return &searchv1.DeleteSearchHistoryResponse{Ok: true}, nil
}

// ReindexAll launches an async full reindex protected by search.reindex capability (or service token) and Postgres advisory lock (G11).
func (s *Service) ReindexAll(ctx context.Context, req *searchv1.ReindexAllRequest) (*searchv1.ReindexAllResponse, error) {
	if err := requireReindexPermission(ctx); err != nil {
		return nil, err
	}
	if req == nil {
		req = &searchv1.ReindexAllRequest{}
	}
	if req.GetBatchSize() < 0 {
		return nil, status.Error(codes.InvalidArgument, "batch_size must be non-negative")
	}
	if s.indexer == nil {
		return nil, status.Error(codes.Unavailable, "indexer unavailable")
	}

	jobID, err := s.indexer.StartReindex(ctx, indexer.ReindexOptions{
		ReindexProducts:   req.GetReindexProducts(),
		ReindexEmbeddings: req.GetReindexEmbeddings(),
		ReindexCategories: req.GetReindexCategories(),
		ReindexShops:      req.GetReindexShops(),
		BatchSize:         req.GetBatchSize(),
	})
	if err != nil {
		if errors.Is(err, indexer.ErrReindexAlreadyRunning) {
			return nil, status.Error(codes.FailedPrecondition, "REINDEX_ALREADY_RUNNING: a reindex job is already running")
		}
		return nil, status.Errorf(codes.Internal, "start reindex: %v", err)
	}

	return &searchv1.ReindexAllResponse{
		JobId:  jobID,
		Status: searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_RUNNING,
	}, nil
}

// GetReindexStatus returns status & progress of a reindex job (requires service token or search.reindex capability).
func (s *Service) GetReindexStatus(ctx context.Context, req *searchv1.GetReindexStatusRequest) (*searchv1.GetReindexStatusResponse, error) {
	if err := requireReindexPermission(ctx); err != nil {
		return nil, err
	}
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id is required")
	}
	if s.store == nil {
		return nil, status.Error(codes.Unavailable, "search store unavailable")
	}

	job, err := s.store.GetReindexJob(ctx, jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "reindex job not found")
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get reindex job: %v", err)
	}

	resp := &searchv1.GetReindexStatusResponse{
		JobId:             job.JobID,
		Status:            parseReindexJobStatus(job.Status),
		ProductsIndexed:   job.ProductsIndexed,
		EmbeddingsUpdated: job.EmbeddingsUpdated,
		ErrorMessage:      job.ErrorMessage,
		StartedAt:         timestamppb.New(job.StartedAt),
	}
	if job.FinishedAt != nil {
		resp.FinishedAt = timestamppb.New(*job.FinishedAt)
	}
	return resp, nil
}

func requireEndUserClaims(ctx context.Context) (*auth.Claims, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return nil, status.Error(codes.PermissionDenied, "service tokens are not allowed for personal search history")
	}
	if strings.TrimSpace(claims.UserID()) == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user subject")
	}
	return claims, nil
}

func requireReindexPermission(ctx context.Context) error {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "authentication required")
	}
	if claims.Service != "" {
		return nil
	}
	if auth.HasCapability(claims, auth.CapSearchReindex) {
		return nil
	}
	return status.Error(codes.PermissionDenied, "missing search.reindex capability")
}

func (s *Service) runMeiliSearch(ctx context.Context, q meili.SearchQuery) (*meili.SearchResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.meiliTimeout)
	defer cancel()
	return s.meili.Search(callCtx, q)
}

func (s *Service) runVectorSearch(ctx context.Context, rawQuery string, params store.VectorSearchParams) ([]store.VectorHit, error) {
	if s.embedder == nil {
		return nil, errors.New("embedder is not configured")
	}
	if s.store == nil {
		return nil, errors.New("vector store is not configured")
	}
	callCtx, cancel := context.WithTimeout(ctx, s.vectorTimeout)
	defer cancel()

	vec, err := s.embedder.Embed(callCtx, rawQuery)
	if err != nil {
		return nil, err
	}
	params.Embedding = vec
	return s.store.SearchVector(callCtx, params)
}

func (s *Service) loadSuspendedShops(ctx context.Context) ([]string, map[string]bool, error) {
	if s.store == nil {
		return nil, map[string]bool{}, nil
	}
	ids, err := s.store.ListSuspendedShopIDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return ids, set, nil
}

func (s *Service) fuseRRF(
	kwDocs []meili.ProductDoc,
	vecHits []store.VectorHit,
	suspendedSet map[string]bool,
	requiredShopID string,
	onlyApprovedInStock bool,
) []*searchv1.SearchProductHit {
	byID := make(map[string]*searchv1.SearchProductHit, len(kwDocs)+len(vecHits))

	kwRank := 0
	for _, doc := range kwDocs {
		if !isDocPublic(doc.Status, doc.InStock, doc.ShopID, suspendedSet, requiredShopID, onlyApprovedInStock) {
			continue
		}
		kwRank++
		score := rrfKeywordWeight / float64(s.rrfK+kwRank)
		byID[doc.ID] = meiliDocToProto(doc, score)
	}

	vecRank := 0
	for _, vh := range vecHits {
		if !isDocPublic(vh.Status, vh.InStock, vh.ShopID, suspendedSet, requiredShopID, onlyApprovedInStock) {
			continue
		}
		vecRank++
		vecScore := rrfVectorWeight / float64(s.rrfK+vecRank)
		if existing, ok := byID[vh.ProductID]; ok {
			existing.Score += vecScore
		} else {
			byID[vh.ProductID] = vectorHitToProto(vh, vecScore)
		}
	}

	hits := make([]*searchv1.SearchProductHit, 0, len(byID))
	for _, item := range byID {
		hits = append(hits, item)
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].RatingAvgX100 != hits[j].RatingAvgX100 {
			return hits[i].RatingAvgX100 > hits[j].RatingAvgX100
		}
		return hits[i].Id < hits[j].Id
	})
	return hits
}

// buildFacets enriches facet counts by loading ONLY the category/shop IDs present in facetDist (S9).
func (s *Service) buildFacets(
	ctx context.Context,
	dist map[string]map[string]int64,
	suspendedSet map[string]bool,
) ([]*searchv1.FacetCount, []*searchv1.FacetCount) {
	if len(dist) == 0 {
		return nil, nil
	}

	var catFacets []*searchv1.FacetCount
	if rawCats := dist["category_ancestor_ids"]; len(rawCats) > 0 {
		catIDs := make([]string, 0, len(rawCats))
		for id, cnt := range rawCats {
			if cnt > 0 && id != "" {
				catIDs = append(catIDs, id)
			}
		}
		if len(catIDs) > 0 {
			catMap := make(map[string]db.SearchCategory, len(catIDs))
			if s.store != nil {
				if rows, err := s.store.ListCategoriesByIDs(ctx, catIDs); err == nil {
					for _, c := range rows {
						catMap[c.CategoryID] = c
					}
				}
			}
			catFacets = make([]*searchv1.FacetCount, 0, len(catIDs))
			for _, id := range catIDs {
				c := catMap[id]
				catFacets = append(catFacets, &searchv1.FacetCount{
					Id:    id,
					Name:  c.Name,
					Slug:  c.Slug,
					Count: rawCats[id],
				})
			}
			sort.Slice(catFacets, func(i, j int) bool {
				if catFacets[i].Count != catFacets[j].Count {
					return catFacets[i].Count > catFacets[j].Count
				}
				return catFacets[i].Id < catFacets[j].Id
			})
		}
	}

	var shopFacets []*searchv1.FacetCount
	if rawShops := dist["shop_id"]; len(rawShops) > 0 {
		shopIDs := make([]string, 0, len(rawShops))
		for id, cnt := range rawShops {
			if cnt > 0 && id != "" && !suspendedSet[id] {
				shopIDs = append(shopIDs, id)
			}
		}
		if len(shopIDs) > 0 {
			shopMap := make(map[string]db.SearchShop, len(shopIDs))
			if s.store != nil {
				if rows, err := s.store.ListShopsByIDs(ctx, shopIDs); err == nil {
					for _, sh := range rows {
						shopMap[sh.ShopID] = sh
					}
				}
			}
			shopFacets = make([]*searchv1.FacetCount, 0, len(shopIDs))
			for _, id := range shopIDs {
				sh := shopMap[id]
				if strings.EqualFold(sh.Status, "SUSPENDED") {
					continue
				}
				shopFacets = append(shopFacets, &searchv1.FacetCount{
					Id:    id,
					Name:  sh.Name,
					Slug:  sh.Slug,
					Count: rawShops[id],
				})
			}
			sort.Slice(shopFacets, func(i, j int) bool {
				if shopFacets[i].Count != shopFacets[j].Count {
					return shopFacets[i].Count > shopFacets[j].Count
				}
				return shopFacets[i].Id < shopFacets[j].Id
			})
		}
	}

	return catFacets, shopFacets
}

// matchesCatalogVocabulary enforces S5: only allow trending recording when the normalized query
// is a substring of the normalized name of a product, category, or shop present in page-1 results.
func matchesCatalogVocabulary(
	qFolded string,
	hits []*searchv1.SearchProductHit,
	catFacets []*searchv1.FacetCount,
	shopFacets []*searchv1.FacetCount,
) bool {
	cleanQ := strings.Join(strings.Fields(qFolded), " ")
	if utf8.RuneCountInString(cleanQ) < 2 {
		return false
	}
	for _, h := range hits {
		if strings.Contains(textnorm.NormalizeSearchText(h.GetName()), cleanQ) {
			return true
		}
		if strings.Contains(textnorm.NormalizeSearchText(h.GetCategoryName()), cleanQ) {
			return true
		}
		if strings.Contains(textnorm.NormalizeSearchText(h.GetShopName()), cleanQ) {
			return true
		}
	}
	for _, cf := range catFacets {
		if strings.Contains(textnorm.NormalizeSearchText(cf.GetName()), cleanQ) {
			return true
		}
	}
	for _, sf := range shopFacets {
		if strings.Contains(textnorm.NormalizeSearchText(sf.GetName()), cleanQ) {
			return true
		}
	}
	return false
}

func (s *Service) computeDidYouMean(ctx context.Context, qFolded, shopID string) string {
	bestTerm := ""
	bestDist := 3

	checkCandidate := func(display, folded string) {
		if folded == "" || folded == qFolded {
			return
		}
		d := levenshtein(qFolded, folded)
		if d < bestDist {
			bestDist = d
			bestTerm = display
		}
	}

	for term := range s.synonyms {
		checkCandidate(term, term)
	}
	if s.store != nil {
		var (
			cats []db.SearchCategory
			err  error
		)
		if shopID != "" {
			cats, err = s.store.ListCategoriesByShop(ctx, shopID)
		} else {
			cats, err = s.store.ListAllCategories(ctx)
		}
		if err == nil {
			for _, c := range cats {
				checkCandidate(c.Name, c.NameFolded)
			}
		}
	}
	if s.trending != nil {
		tCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		if vals, err := s.trending.TopForShop(tCtx, shopID, "", 20); err == nil {
			for _, v := range vals {
				checkCandidate(v, textnorm.NormalizeSearchText(v))
			}
		}
		cancel()
	}
	return bestTerm
}

func adjustTotal(rawTotal int64, rawCount, filteredCount int) int64 {
	if dropped := int64(rawCount - filteredCount); dropped > 0 {
		rawTotal -= dropped
		if rawTotal < 0 {
			rawTotal = 0
		}
	}
	return rawTotal
}

func filterPublicMeiliHits(docs []meili.ProductDoc, suspendedSet map[string]bool, requiredShopID string, onlyApprovedInStock bool) []*searchv1.SearchProductHit {
	out := make([]*searchv1.SearchProductHit, 0, len(docs))
	for i, d := range docs {
		if !isDocPublic(d.Status, d.InStock, d.ShopID, suspendedSet, requiredShopID, onlyApprovedInStock) {
			continue
		}
		score := 1.0 / float64(i+1)
		out = append(out, meiliDocToProto(d, score))
	}
	return out
}

func filterPublicVectorHits(hits []store.VectorHit, suspendedSet map[string]bool, requiredShopID string, onlyApprovedInStock bool) []*searchv1.SearchProductHit {
	out := make([]*searchv1.SearchProductHit, 0, len(hits))
	for _, h := range hits {
		if !isDocPublic(h.Status, h.InStock, h.ShopID, suspendedSet, requiredShopID, onlyApprovedInStock) {
			continue
		}
		out = append(out, vectorHitToProto(h, h.Similarity))
	}
	return out
}

func isDocPublic(statusStr string, inStock bool, shopID string, suspendedSet map[string]bool, requiredShopID string, onlyApprovedInStock bool) bool {
	if requiredShopID != "" && shopID != requiredShopID {
		return false
	}
	if suspendedSet[shopID] {
		return false
	}
	st := indexer.NormalizeProductStatus(statusStr)
	if onlyApprovedInStock {
		return st == "APPROVED" && inStock
	}
	return st == "APPROVED" || st == "OUT_OF_STOCK"
}

func meiliDocToProto(d meili.ProductDoc, score float64) *searchv1.SearchProductHit {
	return &searchv1.SearchProductHit{
		Id:                 d.ID,
		ShopId:             d.ShopID,
		ShopName:           d.ShopName,
		ShopSlug:           d.ShopSlug,
		CategoryId:         d.CategoryID,
		CategorySlug:       d.CategorySlug,
		CategoryName:       d.CategoryName,
		Name:               d.Name,
		Slug:               d.Slug,
		ThumbnailUrl:       d.ThumbnailURL,
		MinPriceVnd:        d.MinPriceVND,
		MaxPriceVnd:        d.MaxPriceVND,
		Status:             d.Status,
		RatingAvgX100:      d.RatingAvgX100,
		ReviewCount:        d.ReviewCount,
		InStock:            d.InStock,
		SkuCount:           d.SKUCount,
		Score:              score,
		DescriptionSnippet: d.DescriptionSnippet,
		SkuIds:             d.SKUIDs,
		DefaultSkuId:       d.DefaultSKUID,
		Colors:             d.Colors,
		Sizes:              d.Sizes,
	}
}

func vectorHitToProto(h store.VectorHit, score float64) *searchv1.SearchProductHit {
	return &searchv1.SearchProductHit{
		Id:                 h.ProductID,
		ShopId:             h.ShopID,
		ShopName:           h.ShopName,
		ShopSlug:           h.ShopSlug,
		CategoryId:         h.CategoryID,
		CategorySlug:       h.CategorySlug,
		CategoryName:       h.CategoryName,
		Name:               h.Name,
		Slug:               h.Slug,
		ThumbnailUrl:       h.ThumbnailURL,
		MinPriceVnd:        h.MinPriceVND,
		MaxPriceVnd:        h.MaxPriceVND,
		Status:             h.Status,
		RatingAvgX100:      h.RatingAvgX100,
		ReviewCount:        h.ReviewCount,
		InStock:            h.InStock,
		SkuCount:           h.SKUCount,
		Score:              score,
		DescriptionSnippet: indexer.TruncateSnippet(h.Description),
		SkuIds:             h.SKUIDs,
		DefaultSkuId:       h.DefaultSKUID,
		Colors:             h.Colors,
		Sizes:              h.Sizes,
	}
}

func paginateHits(all []*searchv1.SearchProductHit, total int64, page, pageSize int32) ([]*searchv1.SearchProductHit, int64) {
	start := int((page - 1) * pageSize)
	if start >= len(all) {
		return []*searchv1.SearchProductHit{}, total
	}
	end := start + int(pageSize)
	if end > len(all) {
		end = len(all)
	}
	return all[start:end], total
}

func buildKeywordSuggestions(
	qFolded string,
	products []*searchv1.SearchProductHit,
	trending []string,
	synonyms map[string][]string,
	limit int,
) []string {
	seen := make(map[string]struct{}, limit)
	out := make([]string, 0, limit)
	add := func(term string) {
		clean := strings.TrimSpace(term)
		key := textnorm.NormalizeSearchText(clean)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}

	for _, t := range trending {
		if len(out) >= limit {
			return out
		}
		add(t)
	}
	for _, p := range products {
		if len(out) >= limit {
			return out
		}
		add(p.GetName())
	}
	if qFolded != "" {
		keys := make([]string, 0, len(synonyms))
		for k := range synonyms {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(out) >= limit {
				break
			}
			if strings.HasPrefix(k, qFolded) || strings.Contains(k, qFolded) {
				add(k)
				for _, syn := range synonyms[k] {
					if len(out) >= limit {
						break
					}
					add(syn)
				}
			}
		}
	}
	return out
}

func normalizePagination(page, pageSize int32) (int32, int32, error) {
	if page < 0 || pageSize < 0 {
		return 0, 0, errors.New("page and page_size must be non-negative")
	}
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	if int64(page-1)*int64(pageSize) > math.MaxInt32 {
		return 0, 0, errors.New("pagination offset overflow")
	}
	return page, pageSize, nil
}

func trimQuery(raw string) string {
	s := strings.TrimSpace(raw)
	if utf8.RuneCountInString(s) > maxQueryRunes {
		s = string([]rune(s)[:maxQueryRunes])
	}
	return s
}

func emptySearchResponse(page, pageSize int32) *searchv1.SearchProductsResponse {
	return &searchv1.SearchProductsResponse{
		Items:    []*searchv1.SearchProductHit{},
		Total:    0,
		Page:     page,
		PageSize: pageSize,
	}
}

func parseReindexJobStatus(raw string) searchv1.ReindexJobStatus {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "RUNNING":
		return searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_RUNNING
	case "COMPLETED":
		return searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED
	case "FAILED":
		return searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_FAILED
	default:
		return searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_UNSPECIFIED
	}
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	if absInt(len(ra)-len(rb)) > 3 {
		return 4
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 0
			if ra[i-1] != rb[j-1] {
				cost = 1
			}
			cur[j] = minInt(minInt(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
