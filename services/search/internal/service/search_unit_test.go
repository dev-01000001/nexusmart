package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/logger"
	"github.com/nexusmart/core/pkg/common/textnorm"
	searchcfg "github.com/nexusmart/core/services/search/config"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/nexusmart/core/services/search/internal/embedder"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/store"
	"github.com/pgvector/pgvector-go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type stubSearchBackend struct {
	mu              sync.Mutex
	suspendedIDs    []string
	categories      map[string]db.SearchCategory
	shopCategories  map[string][]db.SearchCategory
	shops           map[string]db.SearchShop
	embeddings      map[string]db.GetProductEmbeddingForSimilarRow
	vectorHits      []store.VectorHit
	fallbackHits    []store.VectorHit
	vectorErr       error
	lastVectorArg   store.VectorSearchParams
	lastFallbackArg struct {
		shopID string
		q      string
		limit  int32
	}
	activeShopCalls int
}

func (b *stubSearchBackend) ListSuspendedShopIDs(context.Context) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.suspendedIDs...), nil
}

func (b *stubSearchBackend) GetCategoryBySlug(_ context.Context, slug string) (db.SearchCategory, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.categories {
		if c.Slug == slug {
			return c, nil
		}
	}
	return db.SearchCategory{}, pgx.ErrNoRows
}

func (b *stubSearchBackend) GetShopBySlug(_ context.Context, slug string) (db.SearchShop, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sh := range b.shops {
		if sh.Slug == slug {
			return sh, nil
		}
	}
	return db.SearchShop{}, pgx.ErrNoRows
}

func (b *stubSearchBackend) ListCategoriesByIDs(_ context.Context, ids []string) ([]db.SearchCategory, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []db.SearchCategory
	for _, id := range ids {
		if c, ok := b.categories[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (b *stubSearchBackend) ListShopsByIDs(_ context.Context, ids []string) ([]db.SearchShop, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []db.SearchShop
	for _, id := range ids {
		if sh, ok := b.shops[id]; ok {
			out = append(out, sh)
		}
	}
	return out, nil
}

func (b *stubSearchBackend) ListAllCategories(context.Context) ([]db.SearchCategory, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]db.SearchCategory, 0, len(b.categories))
	for _, c := range b.categories {
		out = append(out, c)
	}
	return out, nil
}

func (b *stubSearchBackend) ListCategoriesByShop(_ context.Context, shopID string) ([]db.SearchCategory, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]db.SearchCategory(nil), b.shopCategories[shopID]...), nil
}

func (b *stubSearchBackend) SuggestCategories(_ context.Context, arg db.SuggestCategoriesParams) ([]db.SuggestCategoriesRow, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []db.SuggestCategoriesRow
	for _, c := range b.categories {
		if arg.Column1 == "" || strings.Contains(c.NameFolded, arg.Column1) || strings.Contains(c.Slug, arg.Column1) {
			out = append(out, db.SuggestCategoriesRow{CategoryID: c.CategoryID, Name: c.Name, Slug: c.Slug})
		}
	}
	return out, nil
}

func (b *stubSearchBackend) SuggestCategoriesByShop(_ context.Context, arg db.SuggestCategoriesByShopParams) ([]db.SuggestCategoriesByShopRow, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []db.SuggestCategoriesByShopRow
	for _, c := range b.shopCategories[arg.ShopID] {
		if arg.Column2 == "" || strings.Contains(c.NameFolded, arg.Column2) || strings.Contains(c.Slug, arg.Column2) {
			out = append(out, db.SuggestCategoriesByShopRow{CategoryID: c.CategoryID, Name: c.Name, Slug: c.Slug})
		}
	}
	return out, nil
}

func (b *stubSearchBackend) SuggestActiveShops(_ context.Context, _ db.SuggestActiveShopsParams) ([]db.SuggestActiveShopsRow, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.activeShopCalls++
	var out []db.SuggestActiveShopsRow
	for _, sh := range b.shops {
		if sh.Status == "ACTIVE" {
			out = append(out, db.SuggestActiveShopsRow{ShopID: sh.ShopID, Name: sh.Name, Slug: sh.Slug})
		}
	}
	return out, nil
}

func (b *stubSearchBackend) GetProductEmbeddingForSimilar(_ context.Context, productID string) (db.GetProductEmbeddingForSimilarRow, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	row, ok := b.embeddings[productID]
	if !ok {
		return db.GetProductEmbeddingForSimilarRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (b *stubSearchBackend) ListSearchHistory(context.Context, db.ListSearchHistoryParams) ([]db.SearchHistory, error) {
	return nil, nil
}
func (b *stubSearchBackend) ClearSearchHistory(context.Context, string) error { return nil }
func (b *stubSearchBackend) DeleteSearchHistoryItem(context.Context, db.DeleteSearchHistoryItemParams) (int64, error) {
	return 0, nil
}
func (b *stubSearchBackend) GetReindexJob(context.Context, string) (db.ReindexJob, error) {
	return db.ReindexJob{}, pgx.ErrNoRows
}
func (b *stubSearchBackend) SaveSearchHistory(context.Context, string, string, string, int32) error {
	return nil
}

func (b *stubSearchBackend) SearchVector(_ context.Context, p store.VectorSearchParams) ([]store.VectorHit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastVectorArg = p
	if b.vectorErr != nil {
		return nil, b.vectorErr
	}
	out := make([]store.VectorHit, 0, len(b.vectorHits))
	for _, h := range b.vectorHits {
		if p.ExcludeProductID != "" && h.ProductID == p.ExcludeProductID {
			continue
		}
		if p.ShopID != "" && h.ShopID != p.ShopID {
			continue
		}
		if p.CategoryID != "" && h.CategoryID != p.CategoryID {
			continue
		}
		if p.MinPriceVND > 0 && h.MaxPriceVND < p.MinPriceVND {
			continue
		}
		if p.MaxPriceVND > 0 && h.MinPriceVND > p.MaxPriceVND {
			continue
		}
		if p.InStockOnly && !h.InStock {
			continue
		}
		out = append(out, h)
	}
	if p.Limit > 0 && len(out) > int(p.Limit) {
		out = out[:p.Limit]
	}
	return out, nil
}

func (b *stubSearchBackend) SuggestFallbackProducts(_ context.Context, shopID, queryFolded string, limit int32) ([]store.VectorHit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lastFallbackArg.shopID = shopID
	b.lastFallbackArg.q = queryFolded
	b.lastFallbackArg.limit = limit
	out := make([]store.VectorHit, 0, len(b.fallbackHits))
	for _, h := range b.fallbackHits {
		if shopID != "" && h.ShopID != shopID {
			continue
		}
		if queryFolded != "" && !strings.Contains(textnorm.NormalizeSearchText(h.Name), queryFolded) {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

type stubTrendingBackend struct {
	mu       sync.Mutex
	recorded map[string][]string
	topByKey map[string][]string
}

func (tr *stubTrendingBackend) RecordForShop(_ context.Context, shopID, normalizedQuery string) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.recorded == nil {
		tr.recorded = make(map[string][]string)
	}
	tr.recorded[shopID] = append(tr.recorded[shopID], normalizedQuery)
	return nil
}

func (tr *stubTrendingBackend) TopForShop(_ context.Context, shopID, queryFolded string, limit int) ([]string, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	var out []string
	for _, v := range tr.topByKey[shopID] {
		if queryFolded == "" || strings.Contains(textnorm.NormalizeSearchText(v), queryFolded) {
			out = append(out, v)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

type unitMeiliEngine struct {
	mu       sync.Mutex
	docs     []meili.ProductDoc
	fail     error
	lastReq  meili.SearchQuery
	callSeen bool
}

func (m *unitMeiliEngine) EnsureConfigured(context.Context) error { return nil }
func (m *unitMeiliEngine) StartReconnectLoop(context.Context)     {}
func (m *unitMeiliEngine) IsConfigured() bool                     { return true }
func (m *unitMeiliEngine) UpsertProducts(context.Context, []meili.ProductDoc, bool) error {
	return nil
}
func (m *unitMeiliEngine) DeleteProduct(context.Context, string, bool) error    { return nil }
func (m *unitMeiliEngine) DeleteProducts(context.Context, []string, bool) error { return nil }
func (m *unitMeiliEngine) Search(_ context.Context, req meili.SearchQuery) (*meili.SearchResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastReq = req
	m.callSeen = true
	if m.fail != nil {
		return nil, m.fail
	}
	hits := make([]meili.ProductDoc, 0, len(m.docs))
	for _, d := range m.docs {
		if req.ShopID != "" && d.ShopID != req.ShopID {
			continue
		}
		if req.Query != "" && !strings.Contains(d.NameFolded, req.Query) {
			continue
		}
		hits = append(hits, d)
	}
	return &meili.SearchResult{
		Hits:  hits,
		Total: int64(len(hits)),
	}, nil
}

type failingUnitEmbedder struct {
	err error
}

func (f failingUnitEmbedder) Embed(context.Context, string) ([]float32, error) { return nil, f.err }
func (f failingUnitEmbedder) EmbedBatch(context.Context, []string) ([][]float32, error) {
	return nil, f.err
}
func (f failingUnitEmbedder) Dim() int     { return embedder.DefaultDim }
func (f failingUnitEmbedder) Mode() string { return embedder.ModeMock }

func authClaimsCtx(userID, role, serviceName string) context.Context {
	c := &auth.Claims{Role: role, Service: serviceName}
	c.Subject = userID
	return auth.WithClaims(context.Background(), c)
}

func TestUnit_ShopIsolation_Suggest(t *testing.T) {
	ctx := context.Background()
	st := &stubSearchBackend{
		suspendedIDs: []string{"shop-susp"},
		categories: map[string]db.SearchCategory{
			"cat-a": {CategoryID: "cat-a", Name: "Áo shop A", NameFolded: "ao shop a", Slug: "ao-shop-a"},
			"cat-b": {CategoryID: "cat-b", Name: "Áo shop B", NameFolded: "ao shop b", Slug: "ao-shop-b"},
		},
		shopCategories: map[string][]db.SearchCategory{
			"shop-a": {{CategoryID: "cat-a", Name: "Áo shop A", NameFolded: "ao shop a", Slug: "ao-shop-a"}},
			"shop-b": {{CategoryID: "cat-b", Name: "Áo shop B", NameFolded: "ao shop b", Slug: "ao-shop-b"}},
		},
		shops: map[string]db.SearchShop{
			"shop-a":    {ShopID: "shop-a", Name: "Shop A", Slug: "shop-a", Status: "ACTIVE"},
			"shop-b":    {ShopID: "shop-b", Name: "Shop B", Slug: "shop-b", Status: "ACTIVE"},
			"shop-susp": {ShopID: "shop-susp", Name: "Shop Susp", Slug: "shop-susp", Status: "SUSPENDED"},
		},
		fallbackHits: []store.VectorHit{
			{ProductID: "p-a1", ShopID: "shop-a", Name: "Áo thun shop A", Status: "APPROVED", InStock: true},
			{ProductID: "p-b1", ShopID: "shop-b", Name: "Áo thun shop B", Status: "APPROVED", InStock: true},
		},
	}
	tr := &stubTrendingBackend{
		topByKey: map[string][]string{
			"":       {"ao global"},
			"shop-a": {"ao rieng shop a"},
			"shop-b": {"ao rieng shop b"},
		},
	}
	mc := &unitMeiliEngine{
		docs: []meili.ProductDoc{
			{ID: "p-a1", ShopID: "shop-a", Name: "Áo thun shop A", NameFolded: "ao thun shop a", Status: "APPROVED", InStock: true},
			{ID: "p-b1", ShopID: "shop-b", Name: "Áo thun shop B", NameFolded: "ao thun shop b", Status: "APPROVED", InStock: true},
			{ID: "p-hidden", ShopID: "shop-a", Name: "Áo ẩn shop A", NameFolded: "ao an shop a", Status: "HIDDEN", InStock: true},
		},
	}

	svc := newWithBackends(st, tr, mc, embedder.NewMockEmbedder(embedder.DefaultDim, searchcfg.MustLoadSynonyms()), nil, Options{})

	// 1. Scoped to shop-a: strictly returns shop-a products, shop-a categories, shop-a trending, and NO shops[].
	resp, err := svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao", ShopId: "shop-a", Limit: 8})
	if err != nil {
		t.Fatalf("Suggest(shop-a): %v", err)
	}
	if len(resp.Shops) != 0 || st.activeShopCalls != 0 {
		t.Fatalf("Suggest(shop-a) must omit shops[], got shops=%+v activeShopCalls=%d", resp.Shops, st.activeShopCalls)
	}
	if len(resp.Categories) != 1 || resp.Categories[0].Id != "cat-a" {
		t.Fatalf("Suggest(shop-a) categories = %+v, want [cat-a]", resp.Categories)
	}
	if !slices.Equal(resp.Trending, []string{"ao rieng shop a"}) {
		t.Fatalf("Suggest(shop-a) trending = %v, want [ao rieng shop a]", resp.Trending)
	}
	if len(resp.Products) != 1 || resp.Products[0].Id != "p-a1" || resp.Products[0].ShopId != "shop-a" {
		t.Fatalf("Suggest(shop-a) products = %+v, want [p-a1]", resp.Products)
	}
	for _, kw := range resp.Keywords {
		if strings.Contains(strings.ToLower(kw), "shop b") || strings.Contains(strings.ToLower(kw), "global") {
			t.Fatalf("Suggest(shop-a) keywords leaked other shop/global term: %q", kw)
		}
	}

	// 2. Meili down -> PG fallback MUST also isolate by shop_id.
	mc.fail = errors.New("meili down")
	fbResp, err := svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao", ShopId: "shop-a", Limit: 5})
	mc.fail = nil
	if err != nil {
		t.Fatalf("Suggest(shop-a) fallback: %v", err)
	}
	if st.lastFallbackArg.shopID != "shop-a" {
		t.Fatalf("SuggestFallbackProducts shopID = %q, want shop-a", st.lastFallbackArg.shopID)
	}
	if len(fbResp.Products) != 1 || fbResp.Products[0].Id != "p-a1" {
		t.Fatalf("Suggest(shop-a) fallback products = %+v, want [p-a1]", fbResp.Products)
	}

	// 3. Suspended shop returns empty response.
	suspResp, err := svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao", ShopId: "shop-susp"})
	if err != nil {
		t.Fatalf("Suggest(shop-susp): %v", err)
	}
	if len(suspResp.Products) != 0 || len(suspResp.Categories) != 0 || len(suspResp.Trending) != 0 || len(suspResp.Keywords) != 0 {
		t.Fatalf("Suggest(shop-susp) must return empty response, got %+v", suspResp)
	}

	// 4. Filter injection on shop_id rejected with InvalidArgument.
	if _, err := svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao", ShopId: `shop-a" OR status="HIDDEN`}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Suggest injection code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestUnit_ShopIsolation_HybridSearch(t *testing.T) {
	st := &stubSearchBackend{
		suspendedIDs: []string{"shop-susp"},
		vectorHits: []store.VectorHit{
			{ProductID: "p-a1", ShopID: "shop-a", Name: "Áo khoác nam shop A", Status: "APPROVED", InStock: true, Similarity: 0.9},
			{ProductID: "p-b1", ShopID: "shop-b", Name: "Áo khoác nam shop B", Status: "APPROVED", InStock: true, Similarity: 0.95},
		},
	}
	mc := &unitMeiliEngine{
		docs: []meili.ProductDoc{
			{ID: "p-a1", ShopID: "shop-a", Name: "Áo khoác nam shop A", NameFolded: "ao khoac nam shop a", Status: "APPROVED", InStock: true},
			{ID: "p-b1", ShopID: "shop-b", Name: "Áo khoác nam shop B", NameFolded: "ao khoac nam shop b", Status: "APPROVED", InStock: true},
		},
	}
	emb := embedder.NewMockEmbedder(embedder.DefaultDim, searchcfg.MustLoadSynonyms())
	svc := newWithBackends(st, nil, mc, emb, nil, Options{})
	svcCtx := authClaimsCtx("assistant", auth.ServiceRole, "assistant")

	// 1. Anonymous rejected.
	if _, err := svc.HybridSearch(context.Background(), &searchv1.HybridSearchRequest{Query: "ao khoac", ShopId: "shop-a"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("HybridSearch anonymous code = %v, want Unauthenticated", status.Code(err))
	}

	// 2. Filter injection rejected.
	if _, err := svc.HybridSearch(svcCtx, &searchv1.HybridSearchRequest{Query: "ao", ShopId: `shop-a" OR 1=1`}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("HybridSearch injection code = %v, want InvalidArgument", status.Code(err))
	}

	// 3. Hybrid path passes shop_id to BOTH Meili & Vector and isolates results to shop-a.
	res, err := svc.HybridSearch(svcCtx, &searchv1.HybridSearchRequest{Query: "ao khoac", ShopId: "shop-a", Limit: 10})
	if err != nil {
		t.Fatalf("HybridSearch(shop-a): %v", err)
	}
	if !res.UsedVector || len(res.Hits) != 1 || res.Hits[0].Id != "p-a1" || res.Hits[0].ShopId != "shop-a" {
		t.Fatalf("HybridSearch(shop-a) = %+v, want [p-a1]", res)
	}
	if mc.lastReq.ShopID != "shop-a" || st.lastVectorArg.ShopID != "shop-a" {
		t.Fatalf("shop_id not propagated: meili=%q vector=%q", mc.lastReq.ShopID, st.lastVectorArg.ShopID)
	}

	// 4. Meili failure -> vector_only STILL isolates by shop-a.
	mc.fail = errors.New("meili down")
	vecOnly, err := svc.HybridSearch(svcCtx, &searchv1.HybridSearchRequest{Query: "ao khoac", ShopId: "shop-a", Limit: 10})
	mc.fail = nil
	if err != nil {
		t.Fatalf("HybridSearch vector_only: %v", err)
	}
	if vecOnly.DegradedMode != "vector_only" || len(vecOnly.Hits) != 1 || vecOnly.Hits[0].Id != "p-a1" {
		t.Fatalf("HybridSearch vector_only(shop-a) = %+v, want [p-a1]", vecOnly)
	}

	// 5. Suspended shop returns empty hits without hitting backends.
	suspRes, err := svc.HybridSearch(svcCtx, &searchv1.HybridSearchRequest{Query: "ao khoac", ShopId: "shop-susp", Limit: 10})
	if err != nil || len(suspRes.Hits) != 0 {
		t.Fatalf("HybridSearch(shop-susp) = (%+v, %v), want empty", suspRes, err)
	}
}

func TestUnit_SimilarProducts_IsolationSecurityAndValidation(t *testing.T) {
	ctx := context.Background()
	emb := embedder.NewMockEmbedder(embedder.DefaultDim, searchcfg.MustLoadSynonyms())
	v1Raw, _ := emb.Embed(ctx, "Áo thun thể thao cotton")
	v1 := pgvector.NewVector(v1Raw)
	bigPrice := int64(math.MaxInt32) + 2_000_000_000

	st := &stubSearchBackend{
		suspendedIDs: []string{"shop-susp"},
		categories: map[string]db.SearchCategory{
			"cat-tee": {CategoryID: "cat-tee", Name: "Áo thun", NameFolded: "ao thun", Slug: "ao-thun"},
		},
		embeddings: map[string]db.GetProductEmbeddingForSimilarRow{
			"p-a1":  {ProductID: "p-a1", ShopID: "shop-a", Status: "APPROVED", Deleted: false, Embedding: &v1, ShopStatus: "ACTIVE"},
			"p-b1":  {ProductID: "p-b1", ShopID: "shop-b", Status: "APPROVED", Deleted: false, Embedding: &v1, ShopStatus: "ACTIVE"},
			"p-hid": {ProductID: "p-hid", ShopID: "shop-a", Status: "HIDDEN", Deleted: false, Embedding: &v1, ShopStatus: "ACTIVE"},
			"p-del": {ProductID: "p-del", ShopID: "shop-a", Status: "APPROVED", Deleted: true, Embedding: &v1, ShopStatus: "ACTIVE"},
			"p-sus": {ProductID: "p-sus", ShopID: "shop-susp", Status: "APPROVED", Deleted: false, Embedding: &v1, ShopStatus: "SUSPENDED"},
			"p-nil": {ProductID: "p-nil", ShopID: "shop-a", Status: "APPROVED", Deleted: false, Embedding: nil, ShopStatus: "ACTIVE"},
		},
		vectorHits: []store.VectorHit{
			{ProductID: "p-a1", ShopID: "shop-a", CategoryID: "cat-tee", Name: "Áo thun nguồn", MinPriceVND: 199000, MaxPriceVND: 199000, Status: "APPROVED", InStock: true, Similarity: 1.0},
			{ProductID: "p-a2", ShopID: "shop-a", CategoryID: "cat-tee", Name: "Áo thun tương tự A", MinPriceVND: bigPrice, MaxPriceVND: bigPrice, Status: "APPROVED", InStock: true, Similarity: 0.88},
			{ProductID: "p-b1", ShopID: "shop-b", CategoryID: "cat-tee", Name: "Áo thun shop B", MinPriceVND: 210000, MaxPriceVND: 210000, Status: "APPROVED", InStock: true, Similarity: 0.95},
		},
	}

	var logBuf bytes.Buffer
	log := logger.New("search", logger.WithWriter(&logBuf), logger.WithLevel(slog.LevelDebug))
	svc := newWithBackends(st, nil, nil, emb, nil, Options{Logger: log})

	// 1. By product_id + shop_id: excludes self (p-a1), isolates shop-a (excludes p-b1), preserves int64 VND > MaxInt32, zero commission_rate_bps.
	resp, err := svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{
		ProductId:   "p-a1",
		ShopId:      "shop-a",
		Category:    "ao-thun",
		InStockOnly: true,
		Limit:       99, // clamped to 50
	})
	if err != nil {
		t.Fatalf("SimilarProducts(p-a1, shop-a): %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Id != "p-a2" || resp.Items[0].MinPriceVnd != bigPrice {
		t.Fatalf("SimilarProducts(p-a1, shop-a) = %+v, want [p-a2] with int64 price", resp.Items)
	}
	if st.lastVectorArg.Limit != maxSimilarLimit || st.lastVectorArg.CategoryID != "cat-tee" || st.lastVectorArg.ExcludeProductID != "p-a1" {
		t.Fatalf("unexpected VectorSearchParams: %+v", st.lastVectorArg)
	}
	rawJSON, _ := protojson.Marshal(resp)
	if strings.Contains(strings.ToLower(string(rawJSON)), "commission_rate_bps") {
		t.Fatalf("SimilarProducts must never return commission_rate_bps: %s", string(rawJSON))
	}

	// 2. Cross-shop probe (p-b1 queried under shop-a) MUST return NotFound.
	if _, err := svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{ProductId: "p-b1", ShopId: "shop-a"}); status.Code(err) != codes.NotFound {
		t.Fatalf("cross-shop probe code = %v, want NotFound", status.Code(err))
	}

	// 3. Missing, hidden, deleted, and suspended-shop products MUST return NotFound.
	for _, pid := range []string{"missing", "p-hid", "p-del", "p-sus"} {
		if _, err := svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{ProductId: pid}); status.Code(err) != codes.NotFound {
			t.Fatalf("SimilarProducts(%s) code = %v, want NotFound", pid, status.Code(err))
		}
	}

	// 4. Null embedding without query returns empty cleanly (no error/panic).
	nilResp, err := svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{ProductId: "p-nil", ShopId: "shop-a"})
	if err != nil || len(nilResp.Items) != 0 {
		t.Fatalf("SimilarProducts(p-nil) = (%+v, %v), want empty/nil", nilResp, err)
	}

	// 5. Query mode with shop_id and direct category_id (not in slug map) works.
	qResp, err := svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{
		Query:       "áo thun cotton",
		ShopId:      "shop-a",
		Category:    "cat-tee",
		MinPriceVnd: bigPrice,
		Limit:       5,
	})
	if err != nil || len(qResp.Items) != 1 || qResp.Items[0].Id != "p-a2" {
		t.Fatalf("SimilarProducts(query) = (%+v, %v), want [p-a2]", qResp, err)
	}

	// 6. Suspended shop returns empty.
	suspResp, err := svc.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{Query: "áo thun", ShopId: "shop-susp"})
	if err != nil || len(suspResp.Items) != 0 {
		t.Fatalf("SimilarProducts(shop-susp) = (%+v, %v), want empty", suspResp, err)
	}

	// 7. Validation errors.
	invalidReqs := []*searchv1.SimilarProductsRequest{
		{},
		{ProductId: "p-a1", MinPriceVnd: -1},
		{ProductId: "p-a1", MaxPriceVnd: -10},
		{ProductId: "p-a1", MinPriceVnd: 500000, MaxPriceVnd: 100000},
		{ProductId: `p-a1" OR 1=1`},
		{Query: "ao", ShopId: `shop-a" OR status="HIDDEN`},
		{Query: "ao", Category: `cat" OR 1=1`},
	}
	for i, req := range invalidReqs {
		if _, err := svc.SimilarProducts(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid req[%d] code = %v, want InvalidArgument", i, status.Code(err))
		}
	}

	// 8. G14: Raw query never appears in logs even when embedder fails!
	const canary = "bi_mat_similar_query_g14_554433"
	svcFailEmb := newWithBackends(st, nil, nil, failingUnitEmbedder{err: errors.New("upstream timeout")}, nil, Options{Logger: log})
	if _, err := svcFailEmb.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{Query: canary, ShopId: "shop-a"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable on embedder failure, got %v", err)
	}
	if strings.Contains(logBuf.String(), canary) {
		t.Fatalf("G14 violation: raw SimilarProducts query leaked in logs:\n%s", logBuf.String())
	}
}

func TestUnit_SearchProducts_PerShopTrendingDidYouMeanAndRRF(t *testing.T) {
	ctx := context.Background()
	st := &stubSearchBackend{
		categories: map[string]db.SearchCategory{
			"cat-a": {CategoryID: "cat-a", Name: "Váy lụa", NameFolded: "vay lua", Slug: "vay-lua"},
			"cat-b": {CategoryID: "cat-b", Name: "Giày da", NameFolded: "giay da", Slug: "giay-da"},
		},
		shopCategories: map[string][]db.SearchCategory{
			"shop-a": {{CategoryID: "cat-a", Name: "Váy lụa", NameFolded: "vay lua", Slug: "vay-lua"}},
			"shop-b": {{CategoryID: "cat-b", Name: "Giày da", NameFolded: "giay da", Slug: "giay-da"}},
		},
	}
	tr := &stubTrendingBackend{
		topByKey: map[string][]string{
			"shop-b": {"giay the thao"},
		},
	}
	mc := &unitMeiliEngine{
		docs: []meili.ProductDoc{
			{ID: "p-a1", ShopID: "shop-a", CategoryID: "cat-a", Name: "Áo thun cotton shop A", NameFolded: "ao thun cotton shop a", Status: "APPROVED", InStock: true, MinPriceVND: 200000, MaxPriceVND: 200000},
		},
	}
	svc := newWithBackends(st, tr, mc, nil, nil, Options{Synonyms: map[string][]string{}})

	// 1. SearchProducts with shop_id records trending ONLY under that shop_id.
	res, err := svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "áo thun", ShopId: "shop-a"})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("SearchProducts(shop-a): items=%d err=%v", len(res.Items), err)
	}
	if !slices.Equal(tr.recorded["shop-a"], []string{"ao thun"}) || len(tr.recorded[""]) != 0 || len(tr.recorded["shop-b"]) != 0 {
		t.Fatalf("trending must record strictly under shop-a, got %+v", tr.recorded)
	}

	// 2. Did-you-mean on shop-a uses shop-a categories/trending and must NOT suggest shop-b's "Giày da".
	dymA, err := svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "vay lu", ShopId: "shop-a"})
	if err != nil {
		t.Fatalf("SearchProducts did_you_mean shop-a: %v", err)
	}
	if dymA.DidYouMean != "Váy lụa" {
		t.Fatalf("shop-a did_you_mean = %q, want 'Váy lụa'", dymA.DidYouMean)
	}
	dymLeak, err := svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "giay d", ShopId: "shop-a"})
	if err != nil {
		t.Fatalf("SearchProducts did_you_mean shop-a leak check: %v", err)
	}
	if dymLeak.DidYouMean != "" {
		t.Fatalf("shop-a did_you_mean leaked shop-b category/trending: %q", dymLeak.DidYouMean)
	}

	// 3. ShouldUseHybrid respects SKU codes and 3-word threshold.
	if ShouldUseHybrid(searchv1.SearchMode_SEARCH_MODE_AUTO, "nm-at-001") {
		t.Fatalf("SKU code must stay keyword-only in AUTO mode")
	}
	if ShouldUseHybrid(searchv1.SearchMode_SEARCH_MODE_AUTO, "ao thun") {
		t.Fatalf("2-word query must stay keyword-only in AUTO mode")
	}
	if !ShouldUseHybrid(searchv1.SearchMode_SEARCH_MODE_AUTO, "ao thun cotton mat") {
		t.Fatalf("3+ word query must use hybrid in AUTO mode")
	}
	if ShouldUseHybrid(searchv1.SearchMode_SEARCH_MODE_KEYWORD, "ao thun cotton mat") {
		t.Fatalf("KEYWORD mode must disable hybrid")
	}
	if !ShouldUseHybrid(searchv1.SearchMode_SEARCH_MODE_HYBRID, "ao") {
		t.Fatalf("HYBRID mode must enable hybrid for non-empty query")
	}

	// 4. fuseRRF boosts overlap, enforces 0.6/0.4 weighting, rating tie-break, and blocks non-public/other-shop docs.
	fused := svc.fuseRRF(
		[]meili.ProductDoc{
			{ID: "p1", ShopID: "shop-a", Status: "APPROVED", InStock: true, RatingAvgX100: 400},
			{ID: "p2", ShopID: "shop-a", Status: "APPROVED", InStock: true, RatingAvgX100: 450},
			{ID: "p-leak", ShopID: "shop-b", Status: "APPROVED", InStock: true},
			{ID: "p-hid", ShopID: "shop-a", Status: "HIDDEN", InStock: true},
		},
		[]store.VectorHit{
			{ProductID: "p2", ShopID: "shop-a", Status: "APPROVED", InStock: true, RatingAvgX100: 450},
			{ProductID: "p3", ShopID: "shop-a", Status: "APPROVED", InStock: true, RatingAvgX100: 500},
		},
		map[string]bool{"shop-susp": true},
		"shop-a",
		false,
	)
	if len(fused) != 3 || fused[0].Id != "p2" || fused[1].Id != "p1" || fused[2].Id != "p3" {
		t.Fatalf("unexpected fuseRRF order/filtering: %+v", fused)
	}
	wantP2 := 0.6/62.0 + 0.4/61.0
	if math.Abs(fused[0].Score-wantP2) > 1e-12 {
		t.Fatalf("p2 RRF score = %.12f, want %.12f", fused[0].Score, wantP2)
	}

	// 5. S5 matchesCatalogVocabulary blocks PII not in product/category/shop names.
	if matchesCatalogVocabulary("0901234567", res.Items, nil, nil) {
		t.Fatalf("S5: phone number not in names must be rejected by vocabulary gate")
	}
	if !matchesCatalogVocabulary("ao thun", res.Items, nil, nil) {
		t.Fatalf("S5: valid product name substring must pass vocabulary gate")
	}
}
