package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/services/search/internal/embed"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/store"
)

type fakeMeiliClient struct {
	searchFn  func(ctx context.Context, params meili.SearchParams) (*meili.SearchResult, error)
	suggestFn func(ctx context.Context, q, shopID string, limit int) ([]meili.Suggestion, error)
	calls     int
}

func (f *fakeMeiliClient) Search(ctx context.Context, params meili.SearchParams) (*meili.SearchResult, error) {
	f.calls++
	if f.searchFn != nil {
		return f.searchFn(ctx, params)
	}
	return &meili.SearchResult{}, nil
}

func (f *fakeMeiliClient) Suggest(ctx context.Context, q, shopID string, limit int) ([]meili.Suggestion, error) {
	if f.suggestFn != nil {
		return f.suggestFn(ctx, q, shopID, limit)
	}
	return []meili.Suggestion{}, nil
}

type failingEmbedder struct {
	err error
}

func (f failingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, f.err
}
func (f failingEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float32, error) {
	return nil, f.err
}
func (f failingEmbedder) Dim() int { return 256 }

func seedMemoryStore(t *testing.T, st *store.MemoryStore, emb embed.Embedder) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	docs := []struct {
		id, shop, catID, catSlug, catName, name, desc string
		price                                         int64
		inStock                                       bool
	}{
		{"p1", "shop-1", "c1", "ao-thun", "Áo thun", "Áo thun thể thao đẹp", "Áo chạy bộ thoáng khí", 199000, true},
		{"p2", "shop-1", "c1", "ao-thun", "Áo thun", "Áo thun chạy bộ nam", "Áo thun thể thao thấm hút mồ hôi", 249000, true},
		{"p3", "shop-2", "c2", "tai-nghe", "Tai nghe", "Tai nghe không dây chống ồn", "Âm thanh sống động pin 30h", 890000, true},
	}
	for _, d := range docs {
		vec, err := emb.Embed(ctx, d.name+" "+d.catName+" "+d.desc)
		if err != nil {
			t.Fatalf("embed seed: %v", err)
		}
		if _, err := st.UpsertDocument(ctx, store.ProductDocument{
			ProductID:        d.id,
			ShopID:           d.shop,
			CategoryID:       d.catID,
			CategorySlug:     d.catSlug,
			CategoryName:     d.catName,
			Name:             d.name,
			Description:      d.desc,
			Status:           "APPROVED",
			MinPriceVND:      d.price,
			MaxPriceVND:      d.price,
			TotalStock:       20,
			InStock:          d.inStock,
			Embedding:        vec,
			ProductCreatedAt: now,
			ProductUpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed upsert: %v", err)
		}
	}
}

func TestSearchHybridRRFAndFacets(t *testing.T) {
	st := store.NewMemoryStore()
	emb := embed.NewMockEmbedder()
	seedMemoryStore(t, st, emb)

	mc := &fakeMeiliClient{
		searchFn: func(_ context.Context, params meili.SearchParams) (*meili.SearchResult, error) {
			return &meili.SearchResult{
				Hits: []meili.Document{
					{ID: "p2", ShopID: "shop-1", CategorySlug: "ao-thun", CategoryName: "Áo thun", Name: "Áo thun chạy bộ nam", MinPriceVND: 249000, InStock: true, RankingScore: 0.9},
					{ID: "p1", ShopID: "shop-1", CategorySlug: "ao-thun", CategoryName: "Áo thun", Name: "Áo thun thể thao đẹp", MinPriceVND: 199000, InStock: true, RankingScore: 0.8},
				},
				TotalHits: 2,
				FacetDistribution: map[string]map[string]int64{
					"category_slug": {"ao-thun": 2},
					"shop_id":       {"shop-1": 2},
					"price_bucket":  {"0-200000": 1, "200000-500000": 1},
				},
			}, nil
		},
	}

	srv := New(mc, st, emb, slog.New(slog.NewTextHandler(io.Discard, nil)), WithCategoryLabels(func() map[string]string {
		return map[string]string{"ao-thun": "Áo thun"}
	}))

	resp, err := srv.Search(context.Background(), &searchv1.SearchRequest{
		Query:    "Áo thun thể thao đẹp",
		Page:     1,
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("Search hybrid: %v", err)
	}
	if len(resp.GetItems()) < 2 || resp.GetTotal() < 2 {
		t.Fatalf("unexpected hybrid response: items=%d total=%d", len(resp.GetItems()), resp.GetTotal())
	}
	if len(resp.GetFacets().GetCategories()) != 1 || resp.GetFacets().GetCategories()[0].GetLabel() != "Áo thun" {
		t.Fatalf("unexpected category facets: %+v", resp.GetFacets().GetCategories())
	}
	if len(resp.GetFacets().GetPriceRanges()) != 2 || resp.GetFacets().GetPriceRanges()[0].GetLabel() != "Dưới 200.000đ" {
		t.Fatalf("unexpected price facets: %+v", resp.GetFacets().GetPriceRanges())
	}
}

func TestSearchGracefulFallbackWhenOneBackendFails(t *testing.T) {
	st := store.NewMemoryStore()
	emb := embed.NewMockEmbedder()
	seedMemoryStore(t, st, emb)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Case 1: Embedding fails -> falls back cleanly to Meilisearch results.
	mc := &fakeMeiliClient{
		searchFn: func(_ context.Context, _ meili.SearchParams) (*meili.SearchResult, error) {
			return &meili.SearchResult{
				Hits:      []meili.Document{{ID: "p1", Name: "Áo thun thể thao đẹp", MinPriceVND: 199000}},
				TotalHits: 1,
			}, nil
		},
	}
	srv1 := New(mc, st, failingEmbedder{err: errors.New("voyage down")}, logger)
	resp1, err := srv1.Search(context.Background(), &searchv1.SearchRequest{Query: "áo thun thể thao"})
	if err != nil {
		t.Fatalf("expected graceful fallback to Meilisearch, got error: %v", err)
	}
	if len(resp1.GetItems()) != 1 || resp1.GetItems()[0].GetId() != "p1" {
		t.Fatalf("unexpected fallback items: %+v", resp1.GetItems())
	}

	// Case 2: Meilisearch fails -> falls back cleanly to pgvector results.
	mcFail := &fakeMeiliClient{
		searchFn: func(_ context.Context, _ meili.SearchParams) (*meili.SearchResult, error) {
			return nil, errors.New("meili unavailable")
		},
	}
	srv2 := New(mcFail, st, emb, logger)
	resp2, err := srv2.Search(context.Background(), &searchv1.SearchRequest{Query: "áo thun thể thao đẹp thoáng khí"})
	if err != nil {
		t.Fatalf("expected graceful fallback to pgvector, got error: %v", err)
	}
	if len(resp2.GetItems()) == 0 || resp2.GetItems()[0].GetId() != "p1" {
		t.Fatalf("expected p1 as top pgvector fallback hit, got %+v", resp2.GetItems())
	}

	// Case 3: Both backends fail -> returns Unavailable.
	srv3 := New(mcFail, st, failingEmbedder{err: errors.New("embed down")}, logger)
	if _, err := srv3.Search(context.Background(), &searchv1.SearchRequest{Query: "áo thun thể thao"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("expected Unavailable when both backends fail, got %v", err)
	}
}

func TestSearchRoutesDirectKeywordForSortAndSKU(t *testing.T) {
	st := store.NewMemoryStore()
	emb := embed.NewMockEmbedder()
	seedMemoryStore(t, st, emb)

	var lastParams meili.SearchParams
	mc := &fakeMeiliClient{
		searchFn: func(_ context.Context, params meili.SearchParams) (*meili.SearchResult, error) {
			lastParams = params
			return &meili.SearchResult{Hits: []meili.Document{}, TotalHits: 0}, nil
		},
	}

	srv := New(mc, st, failingEmbedder{err: errors.New("should not be called")}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Sort by price_asc bypasses vector fusion (would fail if embedder were called).
	if _, err := srv.Search(context.Background(), &searchv1.SearchRequest{
		Query: "áo thun thể thao",
		Sort:  "price_asc",
	}); err != nil {
		t.Fatalf("price_asc search should bypass embedder: %v", err)
	}
	if lastParams.Sort != "price_asc" {
		t.Fatalf("sort = %q, want price_asc", lastParams.Sort)
	}

	// SKU query bypasses vector fusion.
	if _, err := srv.Search(context.Background(), &searchv1.SearchRequest{
		Query: "SKU-1001",
	}); err != nil {
		t.Fatalf("SKU search should bypass embedder: %v", err)
	}
}

func TestSuggestAndSimilarProductsAndValidation(t *testing.T) {
	st := store.NewMemoryStore()
	emb := embed.NewMockEmbedder()
	seedMemoryStore(t, st, emb)

	mc := &fakeMeiliClient{
		suggestFn: func(_ context.Context, q, shopID string, limit int) ([]meili.Suggestion, error) {
			if limit != 20 {
				t.Errorf("expected clamped limit 20, got %d", limit)
			}
			return []meili.Suggestion{
				{Text: "Áo thun", Kind: "category", CategorySlug: "ao-thun"},
				{Text: "Áo thun thể thao đẹp", Kind: "product", ProductID: "p1", CategorySlug: "ao-thun", MinPriceVND: 199000},
			}, nil
		},
	}
	srv := New(mc, st, emb, slog.New(slog.NewTextHandler(io.Discard, nil)))

	sugResp, err := srv.Suggest(context.Background(), &searchv1.SuggestRequest{
		Query: "ao thun",
		Limit: 99,
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(sugResp.GetSuggestions()) != 2 {
		t.Fatalf("len(suggestions) = %d, want 2", len(sugResp.GetSuggestions()))
	}

	// SimilarProducts by product_id excludes self and ranks related p2 above unrelated p3.
	simResp, err := srv.SimilarProducts(context.Background(), &searchv1.SimilarProductsRequest{
		ProductId: "p1",
		Limit:     5,
	})
	if err != nil {
		t.Fatalf("SimilarProducts(product_id): %v", err)
	}
	if len(simResp.GetItems()) != 2 || simResp.GetItems()[0].GetId() != "p2" {
		t.Fatalf("expected p2 as closest similar product to p1, got %+v", simResp.GetItems())
	}

	// Missing product_id returns NotFound.
	if _, err := srv.SimilarProducts(context.Background(), &searchv1.SimilarProductsRequest{ProductId: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}

	// Empty product_id and query returns InvalidArgument.
	if _, err := srv.SimilarProducts(context.Background(), &searchv1.SimilarProductsRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}

	// SimilarProducts by query with shop filter.
	simByQuery, err := srv.SimilarProducts(context.Background(), &searchv1.SimilarProductsRequest{
		Query:       "tai nghe không dây",
		ShopId:      "shop-2",
		InStockOnly: true,
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("SimilarProducts(query): %v", err)
	}
	if len(simByQuery.GetItems()) != 1 || simByQuery.GetItems()[0].GetId() != "p3" {
		t.Fatalf("expected p3 for tai nghe query in shop-2, got %+v", simByQuery.GetItems())
	}

	// Vector-only mode.
	vecResp, err := srv.Search(context.Background(), &searchv1.SearchRequest{
		Query: "tai nghe chống ồn",
		Mode:  "vector",
		Page:  1,
	})
	if err != nil {
		t.Fatalf("Search(mode=vector): %v", err)
	}
	if len(vecResp.GetItems()) == 0 || vecResp.GetItems()[0].GetId() != "p3" {
		t.Fatalf("expected p3 first in vector mode, got %+v", vecResp.GetItems())
	}

	// Vector-only mode with empty query returns InvalidArgument.
	if _, err := srv.Search(context.Background(), &searchv1.SearchRequest{Mode: "vector"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty vector query, got %v", err)
	}

	// Invalid price range returns InvalidArgument.
	if _, err := srv.Search(context.Background(), &searchv1.SearchRequest{MinPriceVnd: 500000, MaxPriceVnd: 100000}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for min > max price, got %v", err)
	}
}
