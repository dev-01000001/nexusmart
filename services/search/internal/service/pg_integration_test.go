package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/textnorm"
	searchcfg "github.com/nexusmart/core/services/search/config"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/nexusmart/core/services/search/internal/embedder"
	"github.com/nexusmart/core/services/search/internal/events"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/service"
	"github.com/nexusmart/core/services/search/internal/store"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeMeiliEngine is a thread-safe in-memory Meilisearch simulator that evaluates filters,
// Vietnamese folding, synonyms, facets, and sorting for Postgres+NATS+Redis integration tests.
type fakeMeiliEngine struct {
	mu          sync.RWMutex
	docs        map[string]meili.ProductDoc
	synonyms    map[string][]string
	failSearch  atomic.Bool
	failUpsert  atomic.Bool
	failDelete  atomic.Bool
	upsertCalls atomic.Int32
	deleteCalls atomic.Int32
	configured  atomic.Bool
}

func newFakeMeiliEngine() *fakeMeiliEngine {
	f := &fakeMeiliEngine{
		docs:     make(map[string]meili.ProductDoc),
		synonyms: searchcfg.MustLoadSynonyms(),
	}
	f.configured.Store(true)
	return f
}

func (f *fakeMeiliEngine) EnsureConfigured(context.Context) error { return nil }
func (f *fakeMeiliEngine) StartReconnectLoop(context.Context)     {}
func (f *fakeMeiliEngine) IsConfigured() bool                     { return f.configured.Load() }

func (f *fakeMeiliEngine) UpsertProducts(_ context.Context, docs []meili.ProductDoc, _ bool) error {
	f.upsertCalls.Add(1)
	if f.failUpsert.Load() {
		return errors.New("meilisearch simulated upsert failure")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range docs {
		f.docs[d.ID] = d
	}
	return nil
}

func (f *fakeMeiliEngine) DeleteProduct(_ context.Context, productID string, _ bool) error {
	f.deleteCalls.Add(1)
	if f.failDelete.Load() {
		return errors.New("meilisearch simulated delete failure")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.docs, productID)
	return nil
}

func (f *fakeMeiliEngine) DeleteProducts(_ context.Context, productIDs []string, _ bool) error {
	f.deleteCalls.Add(1)
	if f.failDelete.Load() {
		return errors.New("meilisearch simulated batch delete failure")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range productIDs {
		delete(f.docs, id)
	}
	return nil
}

func (f *fakeMeiliEngine) HasDoc(productID string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.docs[productID]
	return ok
}

func (f *fakeMeiliEngine) GetDoc(productID string) (meili.ProductDoc, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	d, ok := f.docs[productID]
	return d, ok
}

func (f *fakeMeiliEngine) Search(_ context.Context, req meili.SearchQuery) (*meili.SearchResult, error) {
	if f.failSearch.Load() {
		return nil, errors.New("meilisearch simulated outage")
	}
	if _, err := meili.BuildFilters(req); err != nil {
		return nil, err
	}

	excluded := make(map[string]bool, len(req.ExcludedShopIDs))
	for _, s := range req.ExcludedShopIDs {
		excluded[s] = true
	}

	expandedQueries := f.expandQuery(req.Query)

	f.mu.RLock()
	matched := make([]meili.ProductDoc, 0, len(f.docs))
	catCounts := make(map[string]int64)
	shopCounts := make(map[string]int64)
	for _, d := range f.docs {
		if req.OnlyApprovedInStock {
			if d.Status != "APPROVED" || !d.InStock {
				continue
			}
		} else if d.Status != "APPROVED" && d.Status != "OUT_OF_STOCK" {
			continue
		}
		if req.CategoryID != "" && !containsString(d.CategoryAncestorIDs, req.CategoryID) && d.CategoryID != req.CategoryID {
			continue
		}
		if req.ShopID != "" && d.ShopID != req.ShopID {
			continue
		}
		if excluded[d.ShopID] {
			continue
		}
		if req.MinPriceVND > 0 && d.MaxPriceVND < req.MinPriceVND {
			continue
		}
		if req.MaxPriceVND > 0 && d.MinPriceVND > req.MaxPriceVND {
			continue
		}
		if req.MinRatingX100 > 0 && d.RatingAvgX100 < req.MinRatingX100 {
			continue
		}
		if req.InStockOnly && !d.InStock {
			continue
		}
		if req.SizeFolded != "" && !containsString(d.SizesFolded, req.SizeFolded) {
			continue
		}
		if req.ColorFolded != "" && !containsString(d.ColorsFolded, req.ColorFolded) && !containsString(d.ColorCodes, req.ColorFolded) {
			continue
		}
		if req.Query != "" && !docMatchesAnyQuery(d, expandedQueries) {
			continue
		}
		matched = append(matched, d)
		for _, cid := range d.CategoryAncestorIDs {
			catCounts[cid]++
		}
		if d.ShopID != "" {
			shopCounts[d.ShopID]++
		}
	}
	f.mu.RUnlock()

	switch req.SortBy {
	case "price_asc":
		sort.Slice(matched, func(i, j int) bool { return matched[i].MinPriceVND < matched[j].MinPriceVND })
	case "price_desc":
		sort.Slice(matched, func(i, j int) bool { return matched[i].MinPriceVND > matched[j].MinPriceVND })
	case "newest":
		sort.Slice(matched, func(i, j int) bool { return matched[i].UpdatedAtUnix > matched[j].UpdatedAtUnix })
	case "rating_desc":
		sort.Slice(matched, func(i, j int) bool {
			if matched[i].RatingAvgX100 != matched[j].RatingAvgX100 {
				return matched[i].RatingAvgX100 > matched[j].RatingAvgX100
			}
			return matched[i].ReviewCount > matched[j].ReviewCount
		})
	default:
		sort.Slice(matched, func(i, j int) bool {
			si := keywordRelevance(matched[i], req.Query)
			sj := keywordRelevance(matched[j], req.Query)
			if si != sj {
				return si > sj
			}
			return matched[i].ID < matched[j].ID
		})
	}

	total := int64(len(matched))
	start := int(req.Offset)
	if start > len(matched) {
		start = len(matched)
	}
	end := start + int(req.Limit)
	if end > len(matched) {
		end = len(matched)
	}

	facets := map[string]map[string]int64{}
	for _, facetName := range req.Facets {
		switch facetName {
		case "category_ancestor_ids":
			facets[facetName] = catCounts
		case "shop_id":
			facets[facetName] = shopCounts
		}
	}

	return &meili.SearchResult{
		Hits:              matched[start:end],
		Total:             total,
		FacetDistribution: facets,
	}, nil
}

func (f *fakeMeiliEngine) expandQuery(q string) []string {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	out := []string{q}
	for phrase, syns := range f.synonyms {
		if q == phrase || strings.Contains(q, phrase) {
			for _, syn := range syns {
				out = append(out, strings.ReplaceAll(q, phrase, syn))
				out = append(out, syn)
			}
		}
	}
	return out
}

func docMatchesAnyQuery(d meili.ProductDoc, queries []string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		d.NameFolded,
		d.DescriptionFolded,
		d.SKUAttributesText,
		strings.Join(d.SKUCodes, " "),
		textnorm.NormalizeSearchText(d.ShopName),
		textnorm.NormalizeSearchText(d.CategoryName),
	}, " "))
	for _, q := range queries {
		words := strings.Fields(q)
		if len(words) == 0 {
			continue
		}
		allMatch := true
		for _, w := range words {
			if !strings.Contains(haystack, w) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true
		}
	}
	return false
}

func keywordRelevance(d meili.ProductDoc, q string) int {
	if q == "" {
		return 0
	}
	score := 0
	for _, code := range d.SKUCodes {
		if strings.EqualFold(code, q) {
			score += 100
		}
	}
	if strings.Contains(d.NameFolded, q) {
		score += 50
	}
	for _, w := range strings.Fields(q) {
		if strings.Contains(d.NameFolded, w) {
			score += 10
		}
		if strings.Contains(d.DescriptionFolded, w) {
			score += 3
		}
	}
	return score
}

func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}

type toggleFailEmbedder struct {
	inner embedder.Embedder
	fail  atomic.Bool
	calls atomic.Int64
}

func (e *toggleFailEmbedder) Dim() int     { return e.inner.Dim() }
func (e *toggleFailEmbedder) Mode() string { return e.inner.Mode() }
func (e *toggleFailEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.calls.Add(1)
	if e.fail.Load() {
		return nil, errors.New("simulated embedder outage")
	}
	return e.inner.Embed(ctx, text)
}
func (e *toggleFailEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	e.calls.Add(int64(len(texts)))
	if e.fail.Load() {
		return nil, errors.New("simulated embedder outage")
	}
	return e.inner.EmbedBatch(ctx, texts)
}

type stubProductIndexClient struct {
	mu           sync.Mutex
	categories   []*productv1.Category
	products     []*productv1.IndexableProduct
	batchDelay   time.Duration
	onBatchStart func(afterID string)
	sawSvcToken  atomic.Bool
	svcMgr       *auth.Manager
}

func (c *stubProductIndexClient) ListCategories(ctx context.Context, _ *productv1.ListCategoriesRequest, _ ...grpc.CallOption) (*productv1.ListCategoriesResponse, error) {
	// Ensure public RPC is called WITHOUT service token (per pkg/common/auth publicRPCs).
	if md, ok := metadata.FromOutgoingContext(ctx); ok && len(md.Get(auth.MDAuthorization)) > 0 {
		return nil, status.Error(codes.PermissionDenied, "public ListCategories must not send service token")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return &productv1.ListCategoriesResponse{Categories: c.categories}, nil
}

func (c *stubProductIndexClient) ListProductsForIndex(ctx context.Context, in *productv1.ListProductsForIndexRequest, _ ...grpc.CallOption) (*productv1.ListProductsForIndexResponse, error) {
	if c.svcMgr != nil {
		md, _ := metadata.FromOutgoingContext(ctx)
		vals := md.Get(auth.MDAuthorization)
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing service token")
		}
		if _, err := c.svcMgr.VerifyServiceToken(vals[0]); err != nil {
			return nil, status.Error(codes.PermissionDenied, "invalid service token")
		}
		c.sawSvcToken.Store(true)
	}
	if c.onBatchStart != nil {
		c.onBatchStart(in.GetAfterId())
	}
	if c.batchDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.batchDelay):
		}
	}

	c.mu.Lock()
	all := append([]*productv1.IndexableProduct(nil), c.products...)
	c.mu.Unlock()

	sort.Slice(all, func(i, j int) bool { return all[i].GetId() < all[j].GetId() })
	var filtered []*productv1.IndexableProduct
	for _, p := range all {
		if in.GetAfterId() == "" || p.GetId() > in.GetAfterId() {
			filtered = append(filtered, p)
		}
	}
	limit := int(in.GetPageSize())
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	nextCursor := ""
	if len(filtered) > 0 {
		nextCursor = filtered[len(filtered)-1].GetId()
	}
	return &productv1.ListProductsForIndexResponse{
		Items:      filtered,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

type stubUserIndexClient struct {
	mu                   sync.Mutex
	svcMgr               *auth.Manager
	shops                map[string]*userv1.Shop
	getShopInternalCalls int
}

func (c *stubUserIndexClient) GetShopInternal(ctx context.Context, in *userv1.GetShopInternalRequest, _ ...grpc.CallOption) (*userv1.GetShopInternalResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	vals := md.Get(auth.MDAuthorization)
	if len(vals) == 0 {
		return nil, status.Error(codes.Unauthenticated, "GetShopInternal requires service token")
	}
	if c.svcMgr != nil {
		claims, err := c.svcMgr.VerifyServiceToken(vals[0])
		if err != nil || claims.Service != "search-service" {
			return nil, status.Error(codes.PermissionDenied, "invalid service token for GetShopInternal")
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getShopInternalCalls++
	sh, ok := c.shops[in.GetShopId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "shop not found")
	}
	return &userv1.GetShopInternalResponse{Shop: sh}, nil
}

func (c *stubUserIndexClient) ListPublicShops(ctx context.Context, in *userv1.ListPublicShopsRequest, _ ...grpc.CallOption) (*userv1.ListPublicShopsResponse, error) {
	if md, ok := metadata.FromOutgoingContext(ctx); ok && len(md.Get(auth.MDAuthorization)) > 0 {
		return nil, status.Error(codes.PermissionDenied, "public ListPublicShops must not send service token")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var list []*userv1.Shop
	for _, s := range c.shops {
		if s.GetStatus() == userv1.ShopStatus_SHOP_STATUS_ACTIVE {
			list = append(list, s)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].GetId() < list[j].GetId() })
	page := int(in.GetPage())
	if page < 1 {
		page = 1
	}
	pageSize := int(in.GetPageSize())
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	start := (page - 1) * pageSize
	if start > len(list) {
		start = len(list)
	}
	end := start + pageSize
	if end > len(list) {
		end = len(list)
	}
	return &userv1.ListPublicShopsResponse{
		Shops: list[start:end],
		Total: int64(len(list)),
	}, nil
}

type testHarness struct {
	st          *store.Store
	trending    *store.TrendingStore
	rdb         *redis.Client
	meili       *fakeMeiliEngine
	emb         *toggleFailEmbedder
	idx         *indexer.Indexer
	svc         *service.Service
	prodClient  *stubProductIndexClient
	userClient  *stubUserIndexClient
	svcMgr      *auth.Manager
	logBuf      *bytes.Buffer
	natsURL     string
	databaseURL string
}

func setupIntegrationHarness(t *testing.T) *testHarness {
	t.Helper()
	dbURL := strings.TrimSpace(os.Getenv("TEST_SEARCH_DB_URL"))
	if dbURL == "" {
		t.Skip("TEST_SEARCH_DB_URL not set; run via TEST_WITH_SEARCH_DB=1 bash scripts/test-db.sh --run ...")
	}
	redisAddr := strings.TrimSpace(os.Getenv("TEST_REDIS_ADDR"))
	if redisAddr == "" {
		t.Skip("TEST_REDIS_ADDR not set")
	}
	natsURL := strings.TrimSpace(os.Getenv("TEST_NATS_URL"))
	if natsURL == "" {
		t.Skip("TEST_NATS_URL not set")
	}

	ctx := context.Background()
	pool, err := store.OpenPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Clean tables between tests for isolation.
	if _, err := pool.Exec(ctx, `
		TRUNCATE TABLE processed_events, dead_letter_events, product_embeddings,
		               search_categories, search_shops, search_history, reindex_jobs
	`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("redis FlushDB: %v", err)
	}

	svcMgr, err := auth.NewManager("test-service-secret", auth.ServiceTokenTTL)
	if err != nil {
		t.Fatalf("auth.NewManager: %v", err)
	}

	var logBuf bytes.Buffer
	log := logger.New("search", logger.WithWriter(&logBuf), logger.WithLevel(slog.LevelDebug))

	st := store.New(pool)
	tr := store.NewTrendingStore(rdb)
	fm := newFakeMeiliEngine()
	baseEmb := embedder.NewMockEmbedder(embedder.DefaultDim, searchcfg.MustLoadSynonyms())
	emb := &toggleFailEmbedder{inner: baseEmb}

	prodClient := &stubProductIndexClient{svcMgr: svcMgr}
	userClient := &stubUserIndexClient{svcMgr: svcMgr, shops: make(map[string]*userv1.Shop)}

	idx := indexer.New(ctx, st, fm, emb, prodClient, userClient, svcMgr, log)
	t.Cleanup(idx.Stop)

	svc := service.New(st, tr, fm, emb, idx, service.Options{
		RRFK:          60,
		MeiliTimeout:  1500 * time.Millisecond,
		VectorTimeout: 800 * time.Millisecond,
		Logger:        log,
	})

	return &testHarness{
		st:          st,
		trending:    tr,
		rdb:         rdb,
		meili:       fm,
		emb:         emb,
		idx:         idx,
		svc:         svc,
		prodClient:  prodClient,
		userClient:  userClient,
		svcMgr:      svcMgr,
		logBuf:      &logBuf,
		natsURL:     natsURL,
		databaseURL: dbURL,
	}
}

func userCtx(userID, role string) context.Context {
	c := &auth.Claims{Role: role}
	c.Subject = userID
	return auth.WithClaims(context.Background(), c)
}

func serviceCtx(serviceName string) context.Context {
	c := &auth.Claims{Role: auth.ServiceRole, Service: serviceName}
	c.Subject = serviceName
	return auth.WithClaims(context.Background(), c)
}

func publishEvent(t *testing.T, js nats.JetStreamContext, subject, eventID string, payload any) {
	t.Helper()
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	env := events.Envelope{
		EventID:    eventID,
		EventType:  subject,
		TraceID:    "0123456789abcdef0123456789abcdef",
		OccurredAt: time.Now().UTC(),
		Payload:    rawPayload,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	msg := nats.NewMsg(subject)
	msg.Data = raw
	msg.Header.Set(nats.MsgIdHdr, eventID)
	if _, err := js.PublishMsg(msg); err != nil {
		t.Fatalf("PublishMsg(%s): %v", subject, err)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestPG_MigrationsAdvisoryLockAndInt64PriceConstraints(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()

	// Concurrent ApplyMigrations under pg_advisory_xact_lock must not race or error.
	var wg sync.WaitGroup
	errCh := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.ApplyMigrations(ctx, h.st.Pool()); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent ApplyMigrations failed: %v", err)
	}

	// Verify pgvector HNSW and GIN indexes exist.
	var idxCount int
	if err := h.st.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM pg_indexes
		WHERE tablename = 'product_embeddings'
		  AND indexname IN ('idx_product_embeddings_hnsw', 'idx_product_embeddings_category_ancestors')
	`).Scan(&idxCount); err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	if idxCount != 2 {
		t.Fatalf("expected HNSW + GIN indexes on product_embeddings, got %d", idxCount)
	}

	// Verify int64 VND prices > math.MaxInt32 (3.5 billion VND) survive round-trip.
	bigPrice := int64(math.MaxInt32) + 1_500_000_000
	if err := h.idx.HandleProductUpserted(ctx, "evt-big-price", indexer.ProductUpsertInput{
		ProductID:     "prod-big-price",
		ShopID:        "shop-1",
		CategoryID:    "cat-1",
		Name:          "Áo khoác cao cấp",
		Slug:          "ao-khoac-cao-cap",
		MinPriceVND:   bigPrice,
		MaxPriceVND:   bigPrice + 500_000,
		Status:        "APPROVED",
		TotalStock:    5,
		SKUCount:      1,
		RatingAvgX100: 495,
		ReviewCount:   12,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("HandleProductUpserted bigPrice: %v", err)
	}
	resp, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q:           "ao khoac",
		MinPriceVnd: bigPrice,
	})
	if err != nil {
		t.Fatalf("SearchProducts bigPrice: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].MinPriceVnd != bigPrice || resp.Items[0].RatingAvgX100 != 495 || resp.Items[0].ReviewCount != 12 {
		t.Fatalf("unexpected bigPrice hit: %+v", resp.Items)
	}
}

func TestPG_SearchProducts_VietnameseSynonymsFacetsHybridRRFAndDegradedMode(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed categories & active shops.
	if err := h.idx.HandleCategoryUpdated(ctx, "evt-cat-root", indexer.CategoryUpdateInput{
		CategoryID: "cat-ao", Name: "Áo nam", Slug: "ao-nam", SortOrder: 1, Action: "UPSERT", UpdatedAt: now,
	}); err != nil {
		t.Fatalf("cat-ao: %v", err)
	}
	if err := h.idx.HandleCategoryUpdated(ctx, "evt-cat-child", indexer.CategoryUpdateInput{
		CategoryID: "cat-ao-thun", Name: "Áo thun", Slug: "ao-thun", ParentID: "cat-ao", SortOrder: 2, Action: "UPSERT", UpdatedAt: now,
	}); err != nil {
		t.Fatalf("cat-ao-thun: %v", err)
	}
	if err := h.idx.HandleCategoryUpdated(ctx, "evt-cat-dolot", indexer.CategoryUpdateInput{
		CategoryID: "cat-do-lot", Name: "Đồ lót", Slug: "do-lot", SortOrder: 3, Action: "UPSERT", UpdatedAt: now,
	}); err != nil {
		t.Fatalf("cat-do-lot: %v", err)
	}
	h.userClient.shops["shop-cool"] = &userv1.Shop{
		Id: "shop-cool", Name: "CoolWear Studio", Slug: "coolwear-studio",
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	if err := h.idx.HandleShopReviewed(ctx, "evt-shop-cool", indexer.ShopReviewedInput{
		ShopID: "shop-cool", ShopName: "CoolWear Studio", Decision: "APPROVE",
	}); err != nil {
		t.Fatalf("shop-cool: %v", err)
	}

	products := []indexer.ProductUpsertInput{
		{
			ProductID: "p-tee", ShopID: "shop-cool", CategoryID: "cat-ao-thun",
			Name: "Áo thun nam cotton thoáng mát", Slug: "ao-thun-nam-cotton",
			Description: "Áo thun nam chất liệu cotton 220gsm mặc mát mùa hè thấm hút mồ hôi",
			MinPriceVND: 199000, MaxPriceVND: 249000, Status: "APPROVED",
			SKUCodes: []string{"NM-AT-001", "NM-AT-002"}, SKUAttributesText: "mau den trang size M L",
			TotalStock: 30, SKUCount: 2, SKUIDs: []string{"sku-1", "sku-2"}, DefaultSKUID: "sku-1",
			Colors: []string{"Đen", "Trắng"}, Sizes: []string{"M", "L"},
			RatingAvgX100: 485, ReviewCount: 42, UpdatedAt: now,
		},
		{
			ProductID: "p-boxer", ShopID: "shop-cool", CategoryID: "cat-do-lot",
			Name: "Đồ lót boxer nam gỗ sồi modal", Slug: "do-lot-boxer-modal",
			Description: "Quần lót boxer co giãn bốn chiều mềm mại",
			MinPriceVND: 129000, MaxPriceVND: 159000, Status: "APPROVED",
			SKUCodes: []string{"NM-DL-001"}, SKUAttributesText: "mau xanh navy size XL",
			TotalStock: 15, SKUCount: 1, SKUIDs: []string{"sku-3"}, DefaultSKUID: "sku-3",
			Colors: []string{"Xanh Navy"}, Sizes: []string{"XL"},
			RatingAvgX100: 440, ReviewCount: 18, UpdatedAt: now.Add(time.Second),
		},
	}
	for i, p := range products {
		if err := h.idx.HandleProductUpserted(ctx, fmt.Sprintf("evt-prod-%d", i), p); err != nil {
			t.Fatalf("upsert product %s: %v", p.ProductID, err)
		}
	}

	// 1. Short query + SKU code in mode=AUTO -> keyword only (used_vector=false).
	skuResp, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q:    "NM-AT-001",
		Mode: searchv1.SearchMode_SEARCH_MODE_AUTO,
	})
	if err != nil {
		t.Fatalf("SearchProducts SKU: %v", err)
	}
	if skuResp.UsedVector || len(skuResp.Items) != 1 || skuResp.Items[0].Id != "p-tee" {
		t.Fatalf("SKU search want used_vector=false & p-tee, got used_vector=%v items=%+v", skuResp.UsedVector, skuResp.Items)
	}

	// 2. Vietnamese đ/Đ folding + synonym ("ao phong", "do lot").
	synResp, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q: "ao phong",
	})
	if err != nil || len(synResp.Items) != 1 || synResp.Items[0].Id != "p-tee" {
		t.Fatalf("synonym search 'ao phong' got err=%v items=%+v", err, synResp.Items)
	}
	dlResp, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q: "do lot",
	})
	if err != nil || len(dlResp.Items) != 1 || dlResp.Items[0].Id != "p-boxer" {
		t.Fatalf("đ->d search 'do lot' got err=%v items=%+v", err, dlResp.Items)
	}

	// 3. Long descriptive query (>= 3 words) in mode=AUTO triggers Hybrid RRF (pgvector HNSW + Meilisearch).
	rrfResp, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q:    "ao phong nam mac mat mua he",
		Mode: searchv1.SearchMode_SEARCH_MODE_AUTO,
	})
	if err != nil {
		t.Fatalf("Hybrid RRF SearchProducts: %v", err)
	}
	if !rrfResp.UsedVector || rrfResp.DegradedMode != "" {
		t.Fatalf("expected used_vector=true degraded_mode='', got used_vector=%v degraded=%q", rrfResp.UsedVector, rrfResp.DegradedMode)
	}
	if len(rrfResp.Items) == 0 || rrfResp.Items[0].Id != "p-tee" {
		t.Fatalf("expected p-tee top hit in Hybrid RRF, got %+v", rrfResp.Items)
	}
	// Verify real facets, real rating_avg_x100 & review_count, and ZERO commission_rate_bps.
	if rrfResp.Items[0].RatingAvgX100 != 485 || rrfResp.Items[0].ReviewCount != 42 {
		t.Fatalf("expected real rating_avg_x100=485 review_count=42, got %d/%d", rrfResp.Items[0].RatingAvgX100, rrfResp.Items[0].ReviewCount)
	}
	if len(rrfResp.CategoryFacets) == 0 || len(rrfResp.ShopFacets) == 0 {
		t.Fatalf("expected populated category & shop facets, got cats=%+v shops=%+v", rrfResp.CategoryFacets, rrfResp.ShopFacets)
	}
	jsonBytes, _ := protojson.Marshal(rrfResp)
	if strings.Contains(strings.ToLower(string(jsonBytes)), "commission_rate_bps") {
		t.Fatalf("response must never contain commission_rate_bps: %s", string(jsonBytes))
	}

	// 4. Parent category facet filter + min_rating_x100 + color/size filters.
	filtered, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		CategorySlug:  "ao-nam",
		MinRatingX100: 450,
		InStockOnly:   true,
		Size:          "L",
		Color:         "Đen",
	})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Id != "p-tee" {
		t.Fatalf("facet filter expected p-tee, got err=%v items=%+v", err, filtered.Items)
	}

	// 5. Embedder failure during mode=HYBRID -> 200 OK with degraded_mode="keyword_only".
	h.emb.fail.Store(true)
	degResp, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q:    "ao thun nam cotton",
		Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID,
	})
	h.emb.fail.Store(false)
	if err != nil {
		t.Fatalf("expected degraded keyword_only success on embedder outage, got %v", err)
	}
	if degResp.DegradedMode != "keyword_only" || degResp.UsedVector || len(degResp.Items) != 1 {
		t.Fatalf("unexpected degraded response: mode=%q used_vector=%v items=%d", degResp.DegradedMode, degResp.UsedVector, len(degResp.Items))
	}

	// 6. Meilisearch failure -> Unavailable (gateway falls back to Product.ListProducts with product_fallback, G13).
	h.meili.failSearch.Store(true)
	_, err = h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "ao thun"})
	h.meili.failSearch.Store(false)
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("SearchProducts during Meili outage code = %v, want Unavailable", status.Code(err))
	}
}

func TestPG_Security_NoHiddenLeak_FilterInjection_AndNoRawQueryInLogs(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed 1 public product and 3 non-public statuses (PENDING, HIDDEN, REJECTED) plus 1 deleted.
	if err := h.idx.HandleProductUpserted(ctx, "evt-pub", indexer.ProductUpsertInput{
		ProductID: "p-pub", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo khoác công khai", Slug: "ao-khoac-cong-khai",
		MinPriceVND: 200000, MaxPriceVND: 200000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert p-pub: %v", err)
	}
	for _, stName := range []string{"PENDING", "HIDDEN", "REJECTED"} {
		pid := "p-" + strings.ToLower(stName)
		if err := h.idx.HandleProductUpserted(ctx, "evt-"+pid, indexer.ProductUpsertInput{
			ProductID: pid, ShopID: "shop-1", CategoryID: "cat-1",
			Name: "Bí mật " + stName, Slug: pid,
			MinPriceVND: 100000, MaxPriceVND: 100000, Status: stName, TotalStock: 5, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("upsert %s: %v", pid, err)
		}
		if h.meili.HasDoc(pid) {
			t.Fatalf("non-public product %s (%s) must never be indexed into Meilisearch", pid, stName)
		}
	}
	// Inject a HIDDEN doc directly into Meili to prove read-path defense-in-depth STILL blocks it!
	_ = h.meili.UpsertProducts(ctx, []meili.ProductDoc{{
		ID: "p-rogue-hidden", ShopID: "shop-1", Name: "Hidden Rogue", NameFolded: "hidden rogue",
		Status: "HIDDEN", MinPriceVND: 100000, MaxPriceVND: 100000, InStock: true,
	}}, true)

	// Even with admin JWT and spoofed metadata, non-public products NEVER appear.
	adminSpoofCtx := metadata.NewIncomingContext(
		userCtx("admin-1", auth.RoleSuperAdmin),
		metadata.Pairs("x-user-role", "super_admin"),
	)
	for _, q := range []string{"", "bi mat", "hidden rogue"} {
		res, err := h.svc.SearchProducts(adminSpoofCtx, &searchv1.SearchProductsRequest{Q: q, Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID})
		if err != nil {
			t.Fatalf("SearchProducts(%q): %v", q, err)
		}
		for _, item := range res.Items {
			if item.Status != "APPROVED" && item.Status != "OUT_OF_STOCK" {
				t.Fatalf("leaked non-public product %s (%s)", item.Id, item.Status)
			}
		}
		hyb, err := h.svc.HybridSearch(adminSpoofCtx, &searchv1.HybridSearchRequest{Query: q, IncludeOutOfStock: true})
		if err != nil {
			t.Fatalf("HybridSearch(%q): %v", q, err)
		}
		for _, item := range hyb.Hits {
			if item.Status != "APPROVED" && item.Status != "OUT_OF_STOCK" {
				t.Fatalf("HybridSearch leaked non-public product %s (%s)", item.Id, item.Status)
			}
		}
		sug, err := h.svc.Suggest(adminSpoofCtx, &searchv1.SuggestRequest{Query: q})
		if err != nil {
			t.Fatalf("Suggest(%q): %v", q, err)
		}
		for _, item := range sug.Products {
			if item.Status != "APPROVED" && item.Status != "OUT_OF_STOCK" {
				t.Fatalf("Suggest leaked non-public product %s (%s)", item.Id, item.Status)
			}
		}
		if q != "" {
			sim, err := h.svc.SimilarProducts(adminSpoofCtx, &searchv1.SimilarProductsRequest{Query: q})
			if err != nil {
				t.Fatalf("SimilarProducts(%q): %v", q, err)
			}
			for _, item := range sim.Items {
				if item.Status != "APPROVED" && item.Status != "OUT_OF_STOCK" {
					t.Fatalf("SimilarProducts leaked non-public product %s (%s)", item.Id, item.Status)
				}
			}
		}
	}

	// Filter injection attack: shop_id = `x" OR status="HIDDEN` must be blocked (InvalidArgument).
	if _, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		ShopId: `x" OR status="HIDDEN`,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("filter injection shop_id code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := h.svc.HybridSearch(serviceCtx("ai-assistant"), &searchv1.HybridSearchRequest{
		Query:  "ao",
		ShopId: `x" OR status="HIDDEN`,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("HybridSearch filter injection shop_id code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{
		Query:  "ao",
		ShopId: `x" OR status="HIDDEN`,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Suggest filter injection shop_id code = %v, want InvalidArgument", status.Code(err))
	}
	if _, err := h.svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{
		Query:  "ao",
		ShopId: `x" OR status="HIDDEN`,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SimilarProducts filter injection shop_id code = %v, want InvalidArgument", status.Code(err))
	}

	// G14: Raw search queries must NEVER appear in logs (normal + degraded/error paths).
	const canary = "bi_mat_tu_khoa_g14_998877"
	h.emb.fail.Store(true)
	_, _ = h.svc.SearchProducts(userCtx("buyer-log-1", auth.RoleBuyer), &searchv1.SearchProductsRequest{
		Q:    canary + " ba tu tro len",
		Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID,
	})
	_, _ = h.svc.HybridSearch(serviceCtx("ai-assistant"), &searchv1.HybridSearchRequest{Query: canary})
	_, _ = h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: canary})
	_, _ = h.svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{Query: canary})
	h.emb.fail.Store(false)

	if strings.Contains(h.logBuf.String(), canary) {
		t.Fatalf("G14 violation: raw search query leaked into logs:\n%s", h.logBuf.String())
	}
}

func TestPG_SearchHistory_Cap50_IDOR_AndSuggestTrendingRedis(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed category, active shop, and public product for Suggest.
	_ = h.idx.HandleCategoryUpdated(ctx, "evt-c1", indexer.CategoryUpdateInput{
		CategoryID: "cat-1", Name: "Áo sơ mi", Slug: "ao-so-mi", Action: "UPSERT", UpdatedAt: now,
	})
	h.userClient.shops["shop-a"] = &userv1.Shop{
		Id: "shop-a", Name: "Áo Đẹp Shop", Slug: "ao-dep-shop",
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	_ = h.idx.HandleShopReviewed(ctx, "evt-s1", indexer.ShopReviewedInput{ShopID: "shop-a", Decision: "APPROVE"})
	_ = h.idx.HandleProductUpserted(ctx, "evt-p1", indexer.ProductUpsertInput{
		ProductID: "p-somi", ShopID: "shop-a", CategoryID: "cat-1",
		Name: "Áo sơ mi trắng công sở", Slug: "ao-so-mi-trang",
		MinPriceVND: 299000, MaxPriceVND: 299000, Status: "APPROVED", TotalStock: 10, UpdatedAt: now,
	})

	// 1. Anonymous search does not save history; authenticated buyer A saves 55 queries -> capped at 50 (G14).
	// Run "ao so mi" 3 times so it crosses S5 min_score >= 3 in Redis daily trending buckets.
	for i := 0; i < 3; i++ {
		if _, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "ao so mi"}); err != nil {
			t.Fatalf("anonymous SearchProducts #%d: %v", i+1, err)
		}
	}
	ctxBuyerA := userCtx("buyer-A", auth.RoleBuyer)
	ctxBuyerB := userCtx("buyer-B", auth.RoleBuyer)

	for i := 1; i <= 55; i++ {
		_, err := h.svc.SearchProducts(ctxBuyerA, &searchv1.SearchProductsRequest{
			Q: fmt.Sprintf("ao so mi %02d", i),
		})
		if err != nil {
			t.Fatalf("buyerA search %d: %v", i, err)
		}
	}

	histA, err := h.svc.ListSearchHistory(ctxBuyerA, &searchv1.ListSearchHistoryRequest{Limit: 100})
	if err != nil {
		t.Fatalf("ListSearchHistory buyerA: %v", err)
	}
	if len(histA.Items) != 50 {
		t.Fatalf("expected history capped at 50 items, got %d", len(histA.Items))
	}
	if histA.Items[0].Query != "ao so mi 55" {
		t.Fatalf("expected newest query 'ao so mi 55' first, got %q", histA.Items[0].Query)
	}

	// 2. IDOR attack: buyer B tries to delete buyer A's history item -> 0 rows deleted.
	targetID := histA.Items[0].Id
	if _, err := h.svc.DeleteSearchHistory(ctxBuyerB, &searchv1.DeleteSearchHistoryRequest{Id: targetID}); err != nil {
		t.Fatalf("buyerB DeleteSearchHistory: %v", err)
	}
	histAAfterIDOR, err := h.svc.ListSearchHistory(ctxBuyerA, &searchv1.ListSearchHistoryRequest{Limit: 50})
	if err != nil || len(histAAfterIDOR.Items) != 50 || histAAfterIDOR.Items[0].Id != targetID {
		t.Fatalf("IDOR failure: buyerB deleted buyerA's history item! len=%d", len(histAAfterIDOR.Items))
	}

	// 3. Buyer A deletes 1 item, then clear_all=true.
	if _, err := h.svc.DeleteSearchHistory(ctxBuyerA, &searchv1.DeleteSearchHistoryRequest{Id: targetID}); err != nil {
		t.Fatalf("buyerA delete 1: %v", err)
	}
	histAfter1, _ := h.svc.ListSearchHistory(ctxBuyerA, &searchv1.ListSearchHistoryRequest{Limit: 50})
	if len(histAfter1.Items) != 49 {
		t.Fatalf("expected 49 items after single delete, got %d", len(histAfter1.Items))
	}
	if _, err := h.svc.DeleteSearchHistory(ctxBuyerA, &searchv1.DeleteSearchHistoryRequest{ClearAll: true}); err != nil {
		t.Fatalf("buyerA clear_all: %v", err)
	}
	histEmpty, _ := h.svc.ListSearchHistory(ctxBuyerA, &searchv1.ListSearchHistoryRequest{Limit: 50})
	if len(histEmpty.Items) != 0 {
		t.Fatalf("expected 0 items after clear_all, got %d", len(histEmpty.Items))
	}

	// 4. Suggest returns keywords, categories, active shops, Redis trending, products (< 80ms).
	start := time.Now()
	sug, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao", Limit: 8})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(sug.Categories) == 0 || len(sug.Shops) == 0 || len(sug.Trending) == 0 || len(sug.Products) == 0 || len(sug.Keywords) == 0 {
		t.Fatalf("Suggest missing sections: cats=%d shops=%d trending=%d products=%d keywords=%d",
			len(sug.Categories), len(sug.Shops), len(sug.Trending), len(sug.Products), len(sug.Keywords))
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("Suggest unexpectedly slow: %v", elapsed)
	}
}

func TestPG_ShopSuspensionImmediateVisibility_VersionGatingAndDLQ_G10(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Connect real NATS JetStream and start search consumer.
	nc, err := nats.Connect(h.natsURL)
	if err != nil {
		t.Fatalf("nats.Connect: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("JetStream: %v", err)
	}
	if err := jetstream.EnsureStreams(js, jetstream.DefaultPrimaryStream, jetstream.DefaultDLQStream); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}

	suffix := uuid.NewString()[:8]
	consumer := events.NewConsumer(js, js, h.st, h.idx, slog.New(slog.NewTextHandler(h.logBuf, nil)), jetstream.DefaultPrimaryStream)
	consumer.SetSubscriptions([]events.SubscriptionSpec{
		{Subject: "product.>", Durable: "search-prod-g10-" + suffix, Queue: "q-prod-g10-" + suffix},
		{Subject: "user.>", Durable: "search-user-g10-" + suffix, Queue: "q-user-g10-" + suffix},
	})
	consCtx, consCancel := context.WithCancel(ctx)
	if err := consumer.Start(consCtx); err != nil {
		t.Fatalf("consumer.Start: %v", err)
	}
	t.Cleanup(func() {
		consCancel()
		_ = consumer.Stop()
	})

	// Seed 2 shops & 2 products directly (unique shop IDs & names per test run so NATS DeliverAll across -count=N stays isolated).
	shopSuspID := "shop-susp-" + suffix
	shopOkID := "shop-ok-" + suffix
	h.userClient.shops[shopSuspID] = &userv1.Shop{
		Id: shopSuspID, Name: "Gian Hoạt Động " + suffix, Slug: "gian-hoat-dong-" + suffix,
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	h.userClient.shops[shopOkID] = &userv1.Shop{
		Id: shopOkID, Name: "Gian An Toàn " + suffix, Slug: "gian-an-toan-" + suffix,
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	_ = h.idx.HandleShopReviewed(ctx, "ev-rev-1-"+suffix, indexer.ShopReviewedInput{ShopID: shopSuspID, Decision: "APPROVE"})
	_ = h.idx.HandleShopReviewed(ctx, "ev-rev-2-"+suffix, indexer.ShopReviewedInput{ShopID: shopOkID, Decision: "APPROVE"})
	_ = h.idx.HandleProductUpserted(ctx, "ev-p-susp-"+suffix, indexer.ProductUpsertInput{
		ProductID: "p-susp-" + suffix, ShopID: shopSuspID, CategoryID: "cat-1",
		Name: "Áo khoác gió chống nước " + suffix + " susp", Slug: "ao-khoac-susp-" + suffix,
		MinPriceVND: 350000, MaxPriceVND: 350000, Status: "APPROVED", TotalStock: 20, UpdatedAt: now,
	})
	_ = h.idx.HandleProductUpserted(ctx, "ev-p-ok-"+suffix, indexer.ProductUpsertInput{
		ProductID: "p-ok-" + suffix, ShopID: shopOkID, CategoryID: "cat-1",
		Name: "Áo khoác gió chống nước " + suffix + " ok", Slug: "ao-khoac-ok-" + suffix,
		MinPriceVND: 360000, MaxPriceVND: 360000, Status: "APPROVED", TotalStock: 20, UpdatedAt: now,
	})

	// 1. Publish user.shop_status_changed SUSPENDED (version=2) -> must hide shop-susp across ALL 3 read paths without per-product events!
	publishEvent(t, js, events.SubjectUserShopStatusChanged, "ev-susp-v2-"+suffix, events.ShopStatusChangedPayload{
		ShopID: shopSuspID, ShopName: "Gian Hoạt Động " + suffix, FromStatus: "ACTIVE", ToStatus: "SUSPENDED", Version: 2, OccurredAt: now.Format(time.RFC3339),
	})

	waitUntil(t, 3*time.Second, "shop-susp hidden in SearchProducts", func() bool {
		res, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
			Q: "ao khoac gio chong nuoc " + suffix, Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID,
		})
		if err != nil {
			return false
		}
		for _, it := range res.Items {
			if it.ShopId == shopSuspID {
				return false
			}
		}
		return len(res.Items) == 1 && res.Items[0].ShopId == shopOkID
	})

	// Verify HybridSearch and Suggest also immediately exclude shop-susp.
	hyb, err := h.svc.HybridSearch(serviceCtx("ai-assistant"), &searchv1.HybridSearchRequest{Query: "ao khoac gio chong nuoc " + suffix})
	if err != nil {
		t.Fatalf("HybridSearch after suspend: %v", err)
	}
	for _, hit := range hyb.Hits {
		if hit.ShopId == shopSuspID {
			t.Fatalf("HybridSearch leaked suspended shop product %s", hit.Id)
		}
	}
	sug, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: suffix})
	if err != nil {
		t.Fatalf("Suggest after suspend: %v", err)
	}
	for _, sh := range sug.Shops {
		if sh.Id == shopSuspID {
			t.Fatalf("Suggest.Shops leaked suspended shop")
		}
	}
	for _, pr := range sug.Products {
		if pr.ShopId == shopSuspID {
			t.Fatalf("Suggest.Products leaked suspended shop product")
		}
	}
	simSusp, err := h.svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{Query: "ao khoac gio chong nuoc " + suffix})
	if err != nil {
		t.Fatalf("SimilarProducts after suspend: %v", err)
	}
	for _, it := range simSusp.Items {
		if it.ShopId == shopSuspID {
			t.Fatalf("SimilarProducts leaked suspended shop product %s", it.Id)
		}
	}
	if _, err := h.svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{ProductId: "p-susp-" + suffix}); status.Code(err) != codes.NotFound {
		t.Fatalf("SimilarProducts on suspended source product code = %v, want NotFound", status.Code(err))
	}

	// 2. Stale older version (version=1 ACTIVE) MUST be ignored (shop stays SUSPENDED).
	publishEvent(t, js, events.SubjectUserShopStatusChanged, "ev-stale-v1-"+suffix, events.ShopStatusChangedPayload{
		ShopID: shopSuspID, ToStatus: "ACTIVE", Version: 1, OccurredAt: now.Format(time.RFC3339),
	})
	waitUntil(t, 3*time.Second, "stale version event recorded in processed_events", func() bool {
		var exists bool
		_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)`, "ev-stale-v1-"+suffix).Scan(&exists)
		return exists
	})
	resAfterStale, _ := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "ao khoac " + suffix})
	for _, it := range resAfterStale.Items {
		if it.ShopId == shopSuspID {
			t.Fatalf("stale version=1 wrongly unsuspended shop-susp")
		}
	}

	// 3. Invalid version <= 0 MUST be routed to DLQ (dead_letter_events + NEXUSMART_DLQ).
	publishEvent(t, js, events.SubjectUserShopStatusChanged, "ev-bad-v0-"+suffix, events.ShopStatusChangedPayload{
		ShopID: shopSuspID, ToStatus: "ACTIVE", Version: 0, OccurredAt: now.Format(time.RFC3339),
	})
	waitUntil(t, 3*time.Second, "version=0 routed to dead_letter_events", func() bool {
		var cnt int
		_ = h.st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM dead_letter_events WHERE event_id = $1`, "ev-bad-v0-"+suffix).Scan(&cnt)
		return cnt == 1
	})

	// 4. Newer ACTIVE (version=3) immediately restores shop-susp across ALL 3 read paths without per-product events!
	publishEvent(t, js, events.SubjectUserShopStatusChanged, "ev-unsusp-v3-"+suffix, events.ShopStatusChangedPayload{
		ShopID: shopSuspID, FromStatus: "SUSPENDED", ToStatus: "ACTIVE", Version: 3, OccurredAt: now.Format(time.RFC3339),
	})
	waitUntil(t, 3*time.Second, "shop-susp restored in SearchProducts", func() bool {
		res, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
			Q: "ao khoac gio chong nuoc " + suffix, Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID,
		})
		return err == nil && len(res.Items) == 2
	})
	hybRestored, err := h.svc.HybridSearch(serviceCtx("ai-assistant"), &searchv1.HybridSearchRequest{Query: "ao khoac gio chong nuoc " + suffix})
	if err != nil || len(hybRestored.Hits) != 2 {
		t.Fatalf("expected both shops visible in HybridSearch after unsuspend, got err=%v count=%d", err, len(hybRestored.Hits))
	}
	sugRestored, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: suffix})
	if err != nil || len(sugRestored.Shops) != 2 {
		t.Fatalf("expected both shops visible in Suggest after unsuspend, got err=%v shops=%+v", err, sugRestored.Shops)
	}
}

func TestPG_EventOrdering_CategoryReparent_AndE1DurableConsumerRestart(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(10 * time.Second)
	t3 := t1.Add(20 * time.Second)

	// 1. Out-of-order product.upserted with older updated_at < source_updated_at is ignored (G11).
	if err := h.idx.HandleProductUpserted(ctx, "ev-newer", indexer.ProductUpsertInput{
		ProductID: "p-order", ShopID: "shop-1", CategoryID: "cat-child",
		Name: "Áo Mới Nhất", Slug: "ao-moi-nhat",
		MinPriceVND: 250000, MaxPriceVND: 250000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t2,
	}); err != nil {
		t.Fatalf("HandleProductUpserted newer: %v", err)
	}
	if err := h.idx.HandleProductUpserted(ctx, "ev-older", indexer.ProductUpsertInput{
		ProductID: "p-order", ShopID: "shop-1", CategoryID: "cat-child",
		Name: "Áo Cũ Hơn", Slug: "ao-cu-hon",
		MinPriceVND: 100000, MaxPriceVND: 100000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t1,
	}); err != nil {
		t.Fatalf("HandleProductUpserted older: %v", err)
	}
	doc, ok := h.meili.GetDoc("p-order")
	if !ok || doc.Name != "Áo Mới Nhất" || doc.MinPriceVND != 250000 {
		t.Fatalf("stale older event overwrote newer product: %+v", doc)
	}

	// 2. product.category_updated reparenting updates search_categories AND reindexes descendant products.
	_ = h.idx.HandleCategoryUpdated(ctx, "ev-cat-p1", indexer.CategoryUpdateInput{
		CategoryID: "cat-parent-old", Name: "Cha Cũ", Slug: "cha-cu", Action: "UPSERT", UpdatedAt: t1,
	})
	_ = h.idx.HandleCategoryUpdated(ctx, "ev-cat-p2", indexer.CategoryUpdateInput{
		CategoryID: "cat-parent-new", Name: "Cha Mới", Slug: "cha-moi", Action: "UPSERT", UpdatedAt: t1,
	})
	_ = h.idx.HandleCategoryUpdated(ctx, "ev-cat-ch1", indexer.CategoryUpdateInput{
		CategoryID: "cat-child", Name: "Con", Slug: "con", ParentID: "cat-parent-old", Action: "UPSERT", UpdatedAt: t1,
	})
	// Reparent cat-child -> cat-parent-new!
	if err := h.idx.HandleCategoryUpdated(ctx, "ev-cat-reparent", indexer.CategoryUpdateInput{
		CategoryID: "cat-child", Name: "Con", Slug: "con", ParentID: "cat-parent-new", Action: "UPSERT", UpdatedAt: t3,
	}); err != nil {
		t.Fatalf("HandleCategoryUpdated reparent: %v", err)
	}
	resNewParent, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{CategoryId: "cat-parent-new"})
	if err != nil || len(resNewParent.Items) != 1 || resNewParent.Items[0].Id != "p-order" {
		t.Fatalf("expected p-order under cat-parent-new after reparent, got err=%v items=%+v", err, resNewParent.Items)
	}

	// 3. E1 Durable Consumer Restart: no Drain/Unsubscribe -> restarting does NOT replay acked messages,
	//    and shutdown cancellation does NOT send to DLQ.
	tag := uuid.NewString()[:8]
	var deliveryCount atomic.Int32
	countingHandler := &countingEventHandler{
		onUpsert: func() { deliveryCount.Add(1) },
	}

	ncA, err := nats.Connect(h.natsURL)
	if err != nil {
		t.Fatalf("nats.Connect A: %v", err)
	}
	jsA, _ := ncA.JetStream()
	if err := jetstream.EnsureStreams(jsA, jetstream.DefaultPrimaryStream, jetstream.DefaultDLQStream); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	subjIso := "product.e1iso_" + tag
	durableIso := "search-e1iso-" + tag
	queueIso := "q-e1iso-" + tag

	pubTyped := func(js nats.JetStreamContext, id string) {
		raw, _ := json.Marshal(events.Envelope{
			EventID:    id,
			EventType:  events.SubjectProductUpserted,
			OccurredAt: time.Now().UTC(),
			Payload:    json.RawMessage(`{"product_id":"p-iso","status":"APPROVED"}`),
		})
		msg := nats.NewMsg(subjIso)
		msg.Data = raw
		msg.Header.Set(nats.MsgIdHdr, id)
		if _, err := js.PublishMsg(msg); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}

	pubTyped(jsA, "iso-1-"+tag)
	pubTyped(jsA, "iso-2-"+tag)

	deliveryCount.Store(0)
	consA := events.NewConsumer(jsA, jsA, h.st, countingHandler, nil, jetstream.DefaultPrimaryStream)
	consA.SetSubscriptions([]events.SubscriptionSpec{{Subject: subjIso, Durable: durableIso, Queue: queueIso}})
	ctxA, cancelA := context.WithCancel(ctx)
	if err := consA.Start(ctxA); err != nil {
		t.Fatalf("consA.Start: %v", err)
	}
	waitUntil(t, 3*time.Second, "consA processes 2 messages", func() bool {
		return deliveryCount.Load() == 2
	})
	cancelA()
	_ = consA.Stop()
	ncA.Close()

	// Restart consumer #2 on same durable -> must NOT replay iso-1 / iso-2!
	ncB, err := nats.Connect(h.natsURL)
	if err != nil {
		t.Fatalf("nats.Connect B: %v", err)
	}
	t.Cleanup(ncB.Close)
	jsB, _ := ncB.JetStream()
	consB := events.NewConsumer(jsB, jsB, h.st, countingHandler, nil, jetstream.DefaultPrimaryStream)
	consB.SetSubscriptions([]events.SubscriptionSpec{{Subject: subjIso, Durable: durableIso, Queue: queueIso}})
	ctxB, cancelB := context.WithCancel(ctx)
	if err := consB.Start(ctxB); err != nil {
		t.Fatalf("consB.Start: %v", err)
	}
	pubTyped(jsB, "iso-3-"+tag)
	waitUntil(t, 3*time.Second, "consB processes only 1 new message", func() bool {
		return deliveryCount.Load() == 3
	})
	// Give brief window to verify no duplicate replay occurs.
	time.Sleep(150 * time.Millisecond)
	if got := deliveryCount.Load(); got != 3 {
		t.Fatalf("E1 durable restart replayed old messages: deliveryCount=%d, want 3", got)
	}
	cancelB()
	_ = consB.Stop()

	// E1: Shutdown cancellation MUST NOT route messages to DLQ.
	var dlqBefore int64
	_ = h.st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM dead_letter_events`).Scan(&dlqBefore)
	canceledCtx, cancelNow := context.WithCancel(ctx)
	cancelNow()
	action, _ := consB.ProcessRawMessage(canceledCtx, events.SubjectProductUpserted, []byte(`not-json`), nil, 5)
	if action != events.ActionNak {
		t.Fatalf("expected ActionNak on shutdown-canceled context, got %v", action)
	}
	var dlqAfter int64
	_ = h.st.Pool().QueryRow(ctx, `SELECT COUNT(*) FROM dead_letter_events`).Scan(&dlqAfter)
	if dlqAfter != dlqBefore {
		t.Fatalf("shutdown cancellation must not write DLQ: before=%d after=%d", dlqBefore, dlqAfter)
	}
}

type countingEventHandler struct {
	onUpsert func()
}

func (c *countingEventHandler) HandleProductUpserted(context.Context, string, indexer.ProductUpsertInput) error {
	if c.onUpsert != nil {
		c.onUpsert()
	}
	return nil
}
func (c *countingEventHandler) HandleProductDeleted(context.Context, string, string, string, time.Time) error {
	return nil
}
func (c *countingEventHandler) HandleCategoryUpdated(context.Context, string, indexer.CategoryUpdateInput) error {
	return nil
}
func (c *countingEventHandler) HandleShopReviewed(context.Context, string, indexer.ShopReviewedInput) error {
	return nil
}
func (c *countingEventHandler) HandleShopStatusChanged(context.Context, string, indexer.ShopStatusChangedInput) error {
	return nil
}

func TestPG_Race_ReindexVsDelete_ConcurrentReindex_TextHashSkip_AndCrashRecovery_G11(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Prepare 45 public products across 3 keyset batches (batch_size=15).
	h.prodClient.categories = []*productv1.Category{
		{Id: "cat-r1", Name: "Áo thun", Slug: "ao-thun", SortOrder: 1, UpdatedAt: timestamppb.New(now)},
	}
	h.userClient.shops["shop-r1"] = &userv1.Shop{
		Id: "shop-r1", Name: "Shop Reindex", Slug: "shop-reindex",
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	const totalProducts = 45
	prods := make([]*productv1.IndexableProduct, 0, totalProducts)
	for i := 1; i <= totalProducts; i++ {
		pid := fmt.Sprintf("prod-%03d", i)
		prods = append(prods, &productv1.IndexableProduct{
			Id:                  pid,
			ShopId:              "shop-r1",
			CategoryId:          "cat-r1",
			CategoryAncestorIds: []string{"cat-r1"},
			Name:                fmt.Sprintf("Áo thun mẫu số %03d", i),
			Slug:                fmt.Sprintf("ao-thun-mau-%03d", i),
			Description:         "Áo thun cotton thoáng mát",
			MinPriceVnd:         150000 + int64(i)*1000,
			MaxPriceVnd:         200000 + int64(i)*1000,
			Status:              productv1.ProductStatus_PRODUCT_STATUS_APPROVED,
			TotalStock:          10,
			SkuCount:            1,
			SkuIds:              []string{"sku-" + pid},
			DefaultSkuId:        "sku-" + pid,
			RatingAvgX100:       460,
			ReviewCount:         5,
			UpdatedAt:           timestamppb.New(now),
		})
	}
	h.prodClient.products = prods
	h.prodClient.batchDelay = 25 * time.Millisecond

	// Simulate an orphaned RUNNING reindex_jobs row from a previous crash (no advisory lock held).
	if err := h.st.Queries().CreateReindexJob(ctx, "orphaned-job-crash"); err != nil {
		t.Fatalf("seed orphaned job: %v", err)
	}

	adminCtx := userCtx("admin-1", auth.RoleAdmin)

	// (d) Launch 4 concurrent ReindexAll calls -> EXACTLY 1 succeeds, 3 return REINDEX_ALREADY_RUNNING!
	var (
		startWG    sync.WaitGroup
		jobIDs     = make(chan string, 4)
		conflictCt atomic.Int32
		otherErrs  = make(chan error, 4)
	)
	for i := 0; i < 4; i++ {
		startWG.Add(1)
		go func() {
			defer startWG.Done()
			resp, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
				ReindexProducts:   true,
				ReindexEmbeddings: true,
				ReindexCategories: true,
				ReindexShops:      true,
				BatchSize:         15,
			})
			if err != nil {
				if status.Code(err) == codes.FailedPrecondition && strings.Contains(err.Error(), "REINDEX_ALREADY_RUNNING") {
					conflictCt.Add(1)
					return
				}
				otherErrs <- err
				return
			}
			jobIDs <- resp.GetJobId()
		}()
	}

	// (a) Concurrently interleave product.deleted events for 15 products while ReindexAll runs!
	var delWG sync.WaitGroup
	deletedIDs := make(map[string]bool, 15)
	for i := 1; i <= 15; i++ {
		pid := fmt.Sprintf("prod-%03d", i*3) // 003, 006, ..., 045
		deletedIDs[pid] = true
		delWG.Add(1)
		go func(productID string, idx int) {
			defer delWG.Done()
			time.Sleep(time.Duration(idx%5) * 8 * time.Millisecond)
			if err := h.idx.HandleProductDeleted(ctx, "ev-race-del-"+productID, productID, "shop-r1", now.Add(time.Second)); err != nil {
				t.Errorf("HandleProductDeleted(%s): %v", productID, err)
			}
		}(pid, i)
	}

	startWG.Wait()
	delWG.Wait()
	close(jobIDs)
	close(otherErrs)

	for err := range otherErrs {
		t.Fatalf("unexpected ReindexAll error: %v", err)
	}
	var winningJobID string
	winCount := 0
	for id := range jobIDs {
		winningJobID = id
		winCount++
	}
	if winCount != 1 || conflictCt.Load() != 3 {
		t.Fatalf("concurrent ReindexAll: winners=%d conflicts=%d, want 1 and 3", winCount, conflictCt.Load())
	}

	// Wait for winning job to reach COMPLETED.
	waitUntil(t, 5*time.Second, "reindex job COMPLETED", func() bool {
		stResp, err := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: winningJobID})
		return err == nil && stResp.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED
	})

	// Verify service token was sent on ListProductsForIndex.
	if !h.prodClient.sawSvcToken.Load() {
		t.Fatalf("expected ListProductsForIndex to receive valid service token")
	}

	// Verify crash recovery marked the orphaned job FAILED.
	orphanResp, err := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: "orphaned-job-crash"})
	if err != nil || orphanResp.Status != searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_FAILED {
		t.Fatalf("expected orphaned crashed job to be marked FAILED, got err=%v status=%v", err, orphanResp.GetStatus())
	}

	// Verify ZERO deleted products were resurrected in Postgres or Meilisearch!
	for pid := range deletedIDs {
		var isDeleted bool
		if err := h.st.Pool().QueryRow(ctx, `SELECT deleted FROM product_embeddings WHERE product_id = $1`, pid).Scan(&isDeleted); err != nil {
			t.Fatalf("query deleted tombstone %s: %v", pid, err)
		}
		if !isDeleted {
			t.Fatalf("RACE VIOLATION: deleted product %s was resurrected in product_embeddings!", pid)
		}
		if h.meili.HasDoc(pid) {
			t.Fatalf("RACE VIOLATION: deleted product %s was resurrected in Meilisearch!", pid)
		}
	}

	// Run a SECOND ReindexAll:
	//  - Must STILL never resurrect tombstoned products (even with newer timestamp in upstream snapshot!)
	//  - Must SKIP embedding recomputation for all unchanged products (text_hash unchanged -> 0 embed calls!)
	h.prodClient.batchDelay = 0
	embedCallsBefore := h.emb.calls.Load()
	resp2, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
		ReindexProducts:   true,
		ReindexEmbeddings: true,
		BatchSize:         500,
	})
	if err != nil {
		t.Fatalf("second ReindexAll: %v", err)
	}
	waitUntil(t, 5*time.Second, "second reindex COMPLETED", func() bool {
		stResp, err := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: resp2.JobId})
		return err == nil && stResp.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED
	})

	if delta := h.emb.calls.Load() - embedCallsBefore; delta != 0 {
		t.Fatalf("expected 0 embedding calls when text_hash unchanged, got %d", delta)
	}
	for pid := range deletedIDs {
		if h.meili.HasDoc(pid) {
			t.Fatalf("second ReindexAll resurrected tombstoned product %s in Meilisearch", pid)
		}
	}
}

func TestPG_ShopIsolation_SuggestHybridSearchSimilarProductsAndTrending(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed 2 independent shops with distinct categories and products.
	_ = h.idx.HandleCategoryUpdated(ctx, "ev-iso-cat-a", indexer.CategoryUpdateInput{
		CategoryID: "cat-iso-a", Name: "Áo khoác shop A", Slug: "ao-khoac-shop-a", Action: "UPSERT", UpdatedAt: now,
	})
	_ = h.idx.HandleCategoryUpdated(ctx, "ev-iso-cat-b", indexer.CategoryUpdateInput{
		CategoryID: "cat-iso-b", Name: "Áo khoác shop B", Slug: "ao-khoac-shop-b", Action: "UPSERT", UpdatedAt: now,
	})
	h.userClient.shops["shop-iso-a"] = &userv1.Shop{
		Id: "shop-iso-a", Name: "Shop Iso A", Slug: "shop-iso-a",
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	h.userClient.shops["shop-iso-b"] = &userv1.Shop{
		Id: "shop-iso-b", Name: "Shop Iso B", Slug: "shop-iso-b",
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	_ = h.idx.HandleShopReviewed(ctx, "ev-iso-sa", indexer.ShopReviewedInput{ShopID: "shop-iso-a", Decision: "APPROVE"})
	_ = h.idx.HandleShopReviewed(ctx, "ev-iso-sb", indexer.ShopReviewedInput{ShopID: "shop-iso-b", Decision: "APPROVE"})

	for _, p := range []indexer.ProductUpsertInput{
		{
			ProductID: "p-iso-a1", ShopID: "shop-iso-a", CategoryID: "cat-iso-a",
			Name: "Áo khoác gió chống nước A1", Slug: "ao-khoac-a1",
			MinPriceVND: 300000, MaxPriceVND: 300000, Status: "APPROVED", TotalStock: 10, UpdatedAt: now,
		},
		{
			ProductID: "p-iso-a2", ShopID: "shop-iso-a", CategoryID: "cat-iso-a",
			Name: "Áo khoác gió thể thao A2", Slug: "ao-khoac-a2",
			MinPriceVND: 320000, MaxPriceVND: 320000, Status: "APPROVED", TotalStock: 10, UpdatedAt: now,
		},
		{
			ProductID: "p-iso-b1", ShopID: "shop-iso-b", CategoryID: "cat-iso-b",
			Name: "Áo khoác gió chống nước B1", Slug: "ao-khoac-b1",
			MinPriceVND: 310000, MaxPriceVND: 310000, Status: "APPROVED", TotalStock: 10, UpdatedAt: now,
		},
	} {
		if err := h.idx.HandleProductUpserted(ctx, "ev-"+p.ProductID, p); err != nil {
			t.Fatalf("seed %s: %v", p.ProductID, err)
		}
	}

	// 1. Per-shop trending: 3 searches on shop-iso-a write ONLY to search:trending:shop-iso-a:<YYYYMMDD>.
	for i := 0; i < 3; i++ {
		if _, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "Áo khoác gió", ShopId: "shop-iso-a"}); err != nil {
			t.Fatalf("SearchProducts shop-iso-a #%d: %v", i+1, err)
		}
	}
	today := time.Now().UTC().Format("20060102")
	shopAKey := fmt.Sprintf("search:trending:shop-iso-a:%s", today)
	globalKey := fmt.Sprintf("search:trending:%s", today)
	if cnt, err := h.rdb.Exists(ctx, shopAKey).Result(); err != nil || cnt != 1 {
		t.Fatalf("expected per-shop trending key %s to exist, got cnt=%d err=%v", shopAKey, cnt, err)
	}
	if cnt, err := h.rdb.Exists(ctx, globalKey).Result(); err != nil || cnt != 0 {
		t.Fatalf("scoped shop search must NOT pollute global key %s, got cnt=%d err=%v", globalKey, cnt, err)
	}

	// 2. Suggest(shop-iso-a) vs Suggest(shop-iso-b): strict isolation across products, categories, shops[], trending, and PG fallback.
	sugA, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao khoac", ShopId: "shop-iso-a", Limit: 10})
	if err != nil {
		t.Fatalf("Suggest shop-iso-a: %v", err)
	}
	if len(sugA.Shops) != 0 {
		t.Fatalf("Suggest(shop_id) must not return shops[], got %+v", sugA.Shops)
	}
	if len(sugA.Categories) != 1 || sugA.Categories[0].Id != "cat-iso-a" {
		t.Fatalf("Suggest(shop-iso-a) categories = %+v, want [cat-iso-a]", sugA.Categories)
	}
	if len(sugA.Trending) != 1 || sugA.Trending[0] != "ao khoac gio" {
		t.Fatalf("Suggest(shop-iso-a) trending = %+v, want ['ao khoac gio']", sugA.Trending)
	}
	for _, pr := range sugA.Products {
		if pr.ShopId != "shop-iso-a" {
			t.Fatalf("Suggest(shop-iso-a) leaked product from %s", pr.ShopId)
		}
	}

	sugB, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao khoac", ShopId: "shop-iso-b", Limit: 10})
	if err != nil {
		t.Fatalf("Suggest shop-iso-b: %v", err)
	}
	if len(sugB.Trending) != 0 {
		t.Fatalf("Suggest(shop-iso-b) leaked shop-iso-a trending: %+v", sugB.Trending)
	}
	if len(sugB.Categories) != 1 || sugB.Categories[0].Id != "cat-iso-b" {
		t.Fatalf("Suggest(shop-iso-b) categories = %+v, want [cat-iso-b]", sugB.Categories)
	}

	// Suggest PG fallback when Meili fails MUST also respect shop_id.
	h.meili.failSearch.Store(true)
	sugAFB, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao khoac", ShopId: "shop-iso-a", Limit: 10})
	h.meili.failSearch.Store(false)
	if err != nil || len(sugAFB.Products) == 0 {
		t.Fatalf("Suggest PG fallback shop-iso-a: err=%v products=%+v", err, sugAFB.GetProducts())
	}
	for _, pr := range sugAFB.Products {
		if pr.ShopId != "shop-iso-a" {
			t.Fatalf("Suggest PG fallback leaked product from %s", pr.ShopId)
		}
	}

	// 3. HybridSearch(shop-iso-a): strict isolation in both hybrid and vector_only modes.
	hybA, err := h.svc.HybridSearch(serviceCtx("assistant"), &searchv1.HybridSearchRequest{
		Query:  "ao khoac gio chong nuoc",
		ShopId: "shop-iso-a",
		Limit:  10,
	})
	if err != nil || len(hybA.Hits) == 0 {
		t.Fatalf("HybridSearch shop-iso-a: err=%v hits=%+v", err, hybA.GetHits())
	}
	for _, hit := range hybA.Hits {
		if hit.ShopId != "shop-iso-a" {
			t.Fatalf("HybridSearch(shop-iso-a) leaked product from %s", hit.ShopId)
		}
	}

	// 4. SimilarProducts: by product_id excludes self & isolates shop; cross-shop probe returns NotFound.
	simA, err := h.svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{
		ProductId: "p-iso-a1",
		ShopId:    "shop-iso-a",
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("SimilarProducts(p-iso-a1, shop-iso-a): %v", err)
	}
	if len(simA.Items) != 1 || simA.Items[0].Id != "p-iso-a2" {
		t.Fatalf("SimilarProducts(p-iso-a1, shop-iso-a) = %+v, want [p-iso-a2]", simA.Items)
	}
	if _, err := h.svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{
		ProductId: "p-iso-b1",
		ShopId:    "shop-iso-a",
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("SimilarProducts cross-shop probe code = %v, want NotFound", status.Code(err))
	}
}

func TestPG_MigrationUpgradeSchema_DeadLetterTextAndProductDocuments(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()

	// Verify dead_letter_events.id accepts TEXT (converted from phase-0b UUID by 000002) and defaults work.
	if err := h.st.InsertDeadLetterEvent(ctx, db.InsertDeadLetterEventParams{
		ID:           "dlq-text-id-001",
		Subject:      "product.upserted",
		EventID:      "ev-dlq-001",
		Payload:      []byte(`{"bad":true}`),
		ErrorMessage: "malformed",
		Attempts:     1,
	}); err != nil {
		t.Fatalf("InsertDeadLetterEvent with TEXT id failed: %v", err)
	}

	var idDataType string
	if err := h.st.Pool().QueryRow(ctx, `
		SELECT data_type FROM information_schema.columns
		WHERE table_name = 'dead_letter_events' AND column_name = 'id'
	`).Scan(&idDataType); err != nil {
		t.Fatalf("query dead_letter_events.id type: %v", err)
	}
	if idDataType != "text" {
		t.Fatalf("dead_letter_events.id data_type = %q, want text", idDataType)
	}

	// Verify phase-0b product_documents table is preserved (not dropped by 000002).
	var hasProductDocs bool
	if err := h.st.Pool().QueryRow(ctx, `SELECT to_regclass('public.product_documents') IS NOT NULL`).Scan(&hasProductDocs); err != nil {
		t.Fatalf("check product_documents: %v", err)
	}
	if !hasProductDocs {
		t.Fatalf("expected phase-0b product_documents table to remain present after 000002")
	}
}

func TestPG_HandleShopReviewed_GetShopInternalServiceTokenAndSuspendedVersion(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()

	h.userClient.mu.Lock()
	h.userClient.shops["shop-susp-7"] = &userv1.Shop{
		Id:            "shop-susp-7",
		Name:          "Xưởng Bị Tạm Ngưng",
		Slug:          "xuong-bi-tam-ngung",
		Status:        userv1.ShopStatus_SHOP_STATUS_SUSPENDED,
		StatusVersion: 7,
	}
	h.userClient.mu.Unlock()

	if err := h.idx.HandleShopReviewed(ctx, "evt-approve-susp-7", indexer.ShopReviewedInput{
		ShopID:   "shop-susp-7",
		ShopName: "Fallback Name",
		Decision: "APPROVE",
	}); err != nil {
		t.Fatalf("HandleShopReviewed(APPROVE suspended shop): %v", err)
	}

	h.userClient.mu.Lock()
	calls := h.userClient.getShopInternalCalls
	h.userClient.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 authenticated GetShopInternal call by search-service, got %d", calls)
	}

	got, err := h.st.Queries().GetShopByID(ctx, "shop-susp-7")
	if err != nil {
		t.Fatalf("GetShopByID(shop-susp-7): %v", err)
	}
	if got.Status != "SUSPENDED" || got.StatusVersion != 7 || got.Name != "Xưởng Bị Tạm Ngưng" || got.Slug != "xuong-bi-tam-ngung" {
		t.Fatalf("unexpected search_shops row: %+v", got)
	}
}
