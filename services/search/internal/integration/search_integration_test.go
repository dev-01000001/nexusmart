package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	"github.com/nexusmart/core/services/search/internal/consumer"
	"github.com/nexusmart/core/services/search/internal/embed"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/server"
	"github.com/nexusmart/core/services/search/internal/store"
	"github.com/nexusmart/core/services/search/migrations"
)

type liveTestFetcher struct {
	mu         sync.RWMutex
	products   map[string]*productv1.Product
	categories []*productv1.Category
}

func (f *liveTestFetcher) GetProduct(_ context.Context, productID string) (*productv1.Product, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	p, ok := f.products[productID]
	if !ok {
		return nil, indexer.ErrProductNotFound
	}
	return p, nil
}

func (f *liveTestFetcher) ListProducts(_ context.Context, _, _ int32) ([]*productv1.Product, int64, error) {
	return nil, 0, nil
}

func (f *liveTestFetcher) ListCategories(_ context.Context) ([]*productv1.Category, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.categories, nil
}

func setupLiveDeps(t *testing.T, indexSuffix string) (*pgxpool.Pool, *store.PgStore, *meili.Client, *embed.MockEmbedder) {
	t.Helper()

	pgURL := strings.TrimSpace(os.Getenv("SEARCH_TEST_PG_URL"))
	meiliURL := strings.TrimSpace(os.Getenv("SEARCH_TEST_MEILI_URL"))
	if pgURL == "" || meiliURL == "" {
		t.Skip("skipping live integration test: set SEARCH_TEST_PG_URL and SEARCH_TEST_MEILI_URL to run")
	}
	meiliKey := strings.TrimSpace(os.Getenv("SEARCH_TEST_MEILI_KEY"))
	if meiliKey == "" {
		meiliKey = strings.TrimSpace(os.Getenv("MEILI_MASTER_KEY"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("migrations.Apply: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE product_documents, processed_events, dead_letter_events`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	indexName := fmt.Sprintf("products_test_%s_%d", indexSuffix, time.Now().UnixNano())
	mc := meili.NewClient(meili.Config{
		BaseURL: meiliURL,
		APIKey:  meiliKey,
		Index:   indexName,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err := mc.EnsureIndex(ctx); err != nil {
		t.Fatalf("meili.EnsureIndex: %v", err)
	}

	return pool, store.NewPgStore(pool), mc, embed.NewMockEmbedder()
}

func TestSearchIntegrationLive(t *testing.T) {
	pool, pgStore, mc, emb := setupLiveDeps(t, "live")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	cats := []*productv1.Category{
		{Id: "cat-ao", Slug: "ao-thun", Name: "Áo thun"},
		{Id: "cat-quan", Slug: "quan-the-thao", Name: "Quần thể thao"},
		{Id: "cat-den", Slug: "den-ban", Name: "Đèn bàn học"},
		{Id: "cat-audio", Slug: "tai-nghe", Name: "Tai nghe"},
	}

	now := time.Now().UTC()
	seedProds := []*productv1.Product{
		{
			Id: "prod-ao-1", ShopId: "shop-bach-dang", CategoryId: "cat-ao",
			Name: "Áo thun thể thao đẹp thoáng khí", Description: "Áo thun chạy bộ nam nữ co giãn 4 chiều",
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, MinPriceVnd: 189000, MaxPriceVnd: 199000,
			Skus:      []*productv1.SKU{{Id: "s1", SkuCode: "AT-DEP-01", Name: "Đen M", PriceVnd: 189000, Stock: 40, AvailableStock: 40}},
			CreatedAt: timestamppb.New(now.Add(-3 * time.Hour)), UpdatedAt: timestamppb.New(now),
		},
		{
			Id: "prod-ao-2", ShopId: "shop-sai-gon", CategoryId: "cat-ao",
			Name: "Áo thun cotton cao cấp", Description: "Áo thun mặc hằng ngày thấm hút mồ hôi",
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, MinPriceVnd: 320000, MaxPriceVnd: 350000,
			Skus:      []*productv1.SKU{{Id: "s2", SkuCode: "AT-COT-02", Name: "Trắng L", PriceVnd: 320000, Stock: 15, AvailableStock: 15}},
			CreatedAt: timestamppb.New(now.Add(-2 * time.Hour)), UpdatedAt: timestamppb.New(now),
		},
		{
			Id: "prod-quan-1", ShopId: "shop-bach-dang", CategoryId: "cat-quan",
			Name: "Quần đùi chạy bộ thể thao", Description: "Quần thể thao nhẹ nhanh khô",
			Status: productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK, MinPriceVnd: 220000, MaxPriceVnd: 220000,
			Skus:      []*productv1.SKU{{Id: "s3", SkuCode: "QD-CB-03", Name: "Xám M", PriceVnd: 220000, Stock: 0, AvailableStock: 0}},
			CreatedAt: timestamppb.New(now.Add(-1 * time.Hour)), UpdatedAt: timestamppb.New(now),
		},
		{
			Id: "prod-den-1", ShopId: "shop-ha-noi", CategoryId: "cat-den",
			Name: "Đèn bàn học chống cận LED", Description: "Đèn bàn làm việc ánh sáng bảo vệ mắt",
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, MinPriceVnd: 650000, MaxPriceVnd: 650000,
			Skus:      []*productv1.SKU{{Id: "s4", SkuCode: "DB-LED-04", Name: "Trắng", PriceVnd: 650000, Stock: 20, AvailableStock: 20}},
			CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now),
		},
	}

	fetcher := &liveTestFetcher{
		categories: cats,
		products:   map[string]*productv1.Product{},
	}
	for _, p := range seedProds {
		fetcher.products[p.Id] = p
	}

	idx := indexer.New(pgStore, mc, emb, fetcher, logger)
	n, err := idx.BulkIndexProducts(ctx, seedProds, cats)
	if err != nil || n != len(seedProds) {
		t.Fatalf("BulkIndexProducts = (%d, %v), want (%d, nil)", n, err, len(seedProds))
	}

	srv := server.New(mc, pgStore, emb, logger, server.WithCategoryLabels(idx.CategoryLabels))

	// 1. Accented vs unaccented Vietnamese + đ/Đ equivalence.
	resAcc, err := srv.Search(ctx, &searchv1.SearchRequest{Query: "Áo thun thể thao đẹp"})
	if err != nil {
		t.Fatalf("Search accented: %v", err)
	}
	resPlain, err := srv.Search(ctx, &searchv1.SearchRequest{Query: "ao thun the thao dep"})
	if err != nil {
		t.Fatalf("Search plain: %v", err)
	}
	if len(resAcc.GetItems()) == 0 || resAcc.GetItems()[0].GetId() != "prod-ao-1" {
		t.Fatalf("expected prod-ao-1 first for accented query, got %+v", resAcc.GetItems())
	}
	if len(resPlain.GetItems()) == 0 || resPlain.GetItems()[0].GetId() != resAcc.GetItems()[0].GetId() {
		t.Fatalf("expected identical top result for accented vs unaccented query")
	}

	// 2. Đ/đ query: "den ban hoc" finds "Đèn bàn học chống cận LED".
	resDen, err := srv.Search(ctx, &searchv1.SearchRequest{Query: "den ban hoc"})
	if err != nil || len(resDen.GetItems()) == 0 || resDen.GetItems()[0].GetId() != "prod-den-1" {
		t.Fatalf("expected prod-den-1 for 'den ban hoc', got err=%v items=%+v", err, resDen.GetItems())
	}

	// 3. Typo tolerance (oneTypo >= 4 chars): "ao thum the thao" matches "ao thun the thao".
	resTypo, err := srv.Search(ctx, &searchv1.SearchRequest{Query: "ao thum the thao", Mode: "keyword"})
	if err != nil || len(resTypo.GetItems()) == 0 {
		t.Fatalf("expected typo-tolerant hit for 'ao thum the thao', got err=%v items=%+v", err, resTypo.GetItems())
	}

	// 4. Filters, sort, facets, and in_stock_only.
	resFiltered, err := srv.Search(ctx, &searchv1.SearchRequest{
		ShopId:      "shop-bach-dang",
		InStockOnly: true,
		MinPriceVnd: 100000,
		MaxPriceVnd: 250000,
		Sort:        "price_asc",
	})
	if err != nil {
		t.Fatalf("Search filtered: %v", err)
	}
	if len(resFiltered.GetItems()) != 1 || resFiltered.GetItems()[0].GetId() != "prod-ao-1" {
		t.Fatalf("expected only in-stock prod-ao-1, got %+v", resFiltered.GetItems())
	}
	if len(resFiltered.GetFacets().GetCategories()) == 0 || len(resFiltered.GetFacets().GetPriceRanges()) == 0 {
		t.Fatalf("expected non-empty facets: %+v", resFiltered.GetFacets())
	}

	// 5. Suggest.
	sug, err := srv.Suggest(ctx, &searchv1.SuggestRequest{Query: "ao thun", Limit: 5})
	if err != nil || len(sug.GetSuggestions()) == 0 {
		t.Fatalf("Suggest failed: err=%v resp=%+v", err, sug)
	}

	// 6. SimilarProducts by product_id.
	sim, err := srv.SimilarProducts(ctx, &searchv1.SimilarProductsRequest{ProductId: "prod-ao-1", Limit: 3})
	if err != nil || len(sim.GetItems()) == 0 {
		t.Fatalf("SimilarProducts failed: err=%v resp=%+v", err, sim)
	}
	for _, item := range sim.GetItems() {
		if item.GetId() == "prod-ao-1" {
			t.Fatal("SimilarProducts must exclude the source product_id")
		}
	}

	// 7. Optional live NATS JetStream consumer test when SEARCH_TEST_NATS_URL is set.
	if natsURL := strings.TrimSpace(os.Getenv("SEARCH_TEST_NATS_URL")); natsURL != "" {
		testLiveNATSConsumer(ctx, t, pool, pgStore, mc, idx, fetcher, natsURL)
	}
}

func testLiveNATSConsumer(
	ctx context.Context,
	t *testing.T,
	pool *pgxpool.Pool,
	pgStore *store.PgStore,
	mc *meili.Client,
	idx *indexer.Indexer,
	fetcher *liveTestFetcher,
	natsURL string,
) {
	t.Helper()

	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("nats.Connect: %v", err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("nc.JetStream: %v", err)
	}
	if err := consumer.EnsureStreams(js, consumer.DefaultPrimaryStream, consumer.DefaultDLQStream); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}

	c := consumer.NewConsumer(js, idx, pgStore, consumer.DefaultPrimaryStream, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := c.Start(ctx); err != nil {
		t.Fatalf("consumer.Start: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Add a new product in fake fetcher and publish product.created.
	newID := "prod-nats-" + uuid.NewString()[:8]
	fetcher.mu.Lock()
	fetcher.products[newID] = &productv1.Product{
		Id: newID, ShopId: "shop-da-nang", CategoryId: "cat-audio",
		Name: "Tai nghe bluetooth NATS live", Description: "Kiểm thử đồng bộ thời gian thực",
		Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, MinPriceVnd: 450000, MaxPriceVnd: 450000,
		Skus: []*productv1.SKU{{Id: "sn1", SkuCode: "TN-NATS-1", PriceVnd: 450000, Stock: 12, AvailableStock: 12}},
	}
	fetcher.mu.Unlock()

	payload, _ := json.Marshal(indexer.ProductEventPayload{
		ProductID:  newID,
		ShopID:     "shop-da-nang",
		Version:    1,
		OccurredAt: time.Now().UTC().Format(time.RFC3339),
	})
	envBytes, _ := json.Marshal(indexer.Envelope{
		EventID:    uuid.NewString(),
		EventType:  indexer.SubjectProductCreated,
		OccurredAt: time.Now().UTC(),
		Payload:    payload,
	})
	if _, err := js.Publish(indexer.SubjectProductCreated, envBytes); err != nil {
		t.Fatalf("js.Publish created: %v", err)
	}

	// Wait up to 5s for indexing to land in Postgres + Meilisearch.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := pgStore.GetDocument(ctx, newID); err == nil {
			res, sErr := mc.Search(ctx, meili.SearchParams{Query: "Tai nghe bluetooth NATS live", Limit: 5})
			if sErr == nil && len(res.Hits) > 0 && res.Hits[0].ID == newID {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := pgStore.GetDocument(ctx, newID); err != nil {
		t.Fatalf("expected %s indexed via NATS within 5s: %v", newID, err)
	}

	// Publish malformed poison message and verify dead_letter_events persistence.
	if _, err := js.Publish(indexer.SubjectProductUpdated, []byte(`{bad-json`)); err != nil {
		t.Fatalf("js.Publish poison: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	var dlqCount int
	for time.Now().Before(deadline) {
		_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM dead_letter_events`).Scan(&dlqCount)
		if dlqCount > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dlqCount == 0 {
		t.Fatal("expected poison message persisted in dead_letter_events")
	}
}

func TestSearchBenchmark20K(t *testing.T) {
	if os.Getenv("SEARCH_BENCH_20K") != "1" {
		t.Skip("skipping 20,000-product benchmark: set SEARCH_BENCH_20K=1 along with SEARCH_TEST_PG_URL & SEARCH_TEST_MEILI_URL")
	}

	_, pgStore, mc, emb := setupLiveDeps(t, "bench20k")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	cats := []*productv1.Category{
		{Id: "c1", Slug: "ao-thun", Name: "Áo thun"},
		{Id: "c2", Slug: "ao-khoac", Name: "Áo khoác"},
		{Id: "c3", Slug: "quan-the-thao", Name: "Quần thể thao"},
		{Id: "c4", Slug: "giay-chay-bo", Name: "Giày chạy bộ"},
		{Id: "c5", Slug: "den-ban-hoc", Name: "Đèn bàn học"},
		{Id: "c6", Slug: "tai-nghe", Name: "Tai nghe"},
		{Id: "c7", Slug: "balo-du-lich", Name: "Balo du lịch"},
		{Id: "c8", Slug: "do-gia-dung", Name: "Đồ gia dụng"},
	}
	shops := []string{"shop-bach-dang", "shop-sai-gon", "shop-da-nang", "shop-ha-noi"}
	adjectives := []string{"đẹp", "cao cấp", "thể thao", "thoáng khí", "chống nước", "siêu nhẹ", "chính hãng", "bền bỉ"}
	nouns := []string{"Áo thun", "Áo khoác gió", "Quần đùi chạy bộ", "Giày chạy bộ", "Đèn bàn học", "Tai nghe không dây", "Balo chống sốc", "Bình giữ nhiệt"}

	const totalProducts = 20000
	rng := rand.New(rand.NewPCG(20261121, 1))
	prods := make([]*productv1.Product, totalProducts)
	baseTime := time.Now().UTC().Add(-20000 * time.Minute)

	for i := 0; i < totalProducts; i++ {
		catIdx := i % len(cats)
		shop := shops[i%len(shops)]
		adj := adjectives[rng.IntN(len(adjectives))]
		price := int64(59+rng.IntN(1900)) * 1000
		stock := int32(10 + rng.IntN(100))
		st := productv1.ProductStatus_PRODUCT_STATUS_APPROVED
		if i%20 == 0 {
			stock = 0
			st = productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK
		}
		prods[i] = &productv1.Product{
			Id:          fmt.Sprintf("bench-prod-%05d", i),
			ShopId:      shop,
			CategoryId:  cats[catIdx].Id,
			Name:        fmt.Sprintf("%s %s mẫu %d", nouns[catIdx], adj, i),
			Description: fmt.Sprintf("Sản phẩm %s %s chất liệu cao cấp phù hợp sử dụng hằng ngày tại Việt Nam", nouns[catIdx], adj),
			Status:      st,
			MinPriceVnd: price,
			MaxPriceVnd: price + 20000,
			Skus: []*productv1.SKU{
				{Id: fmt.Sprintf("sku-%05d", i), SkuCode: fmt.Sprintf("NM-%05d", i), Name: "Tiêu chuẩn", PriceVnd: price, Stock: stock, AvailableStock: stock},
			},
			CreatedAt: timestamppb.New(baseTime.Add(time.Duration(i) * time.Minute)),
			UpdatedAt: timestamppb.New(baseTime.Add(time.Duration(i) * time.Minute)),
		}
	}

	idx := indexer.New(pgStore, mc, emb, nil, logger)
	indexStart := time.Now()
	indexed, err := idx.BulkIndexProducts(ctx, prods, cats)
	if err != nil {
		t.Fatalf("BulkIndexProducts 20k: %v", err)
	}
	indexElapsed := time.Since(indexStart)
	t.Logf("indexed %d products in %v (%.1f docs/sec)", indexed, indexElapsed, float64(indexed)/indexElapsed.Seconds())

	srv := server.New(mc, pgStore, emb, logger, server.WithCategoryLabels(idx.CategoryLabels))

	queries := []*searchv1.SearchRequest{
		{Query: "Áo thun thể thao đẹp", Page: 1, PageSize: 20},
		{Query: "ao thun the thao dep", Page: 1, PageSize: 20},
		{Query: "Quần đùi chạy bộ", ShopId: "shop-bach-dang", Page: 1, PageSize: 20},
		{Query: "den ban hoc cao cap", Category: "den-ban-hoc", Page: 1, PageSize: 20},
		{Query: "tai nghe khong day", MinPriceVnd: 200000, MaxPriceVnd: 1000000, InStockOnly: true, Page: 1, PageSize: 20},
		{Query: "ao khoac gio chong nuoc", Sort: "price_asc", Page: 1, PageSize: 20},
		{Query: "giay chay bo sieu nhe", Sort: "newest", Page: 1, PageSize: 20},
		{Query: "ao thum the thao", Page: 1, PageSize: 20},
		{Query: "NM-01234", Page: 1, PageSize: 20},
		{ShopId: "shop-ha-noi", Sort: "price_desc", Page: 1, PageSize: 20},
	}

	// Warm up caches/HNSW index pages.
	for _, q := range queries {
		if _, err := srv.Search(ctx, q); err != nil {
			t.Fatalf("warmup search failed: %v", err)
		}
	}

	const runs = 100
	latencies := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		q := queries[i%len(queries)]
		start := time.Now()
		resp, err := srv.Search(ctx, q)
		lat := time.Since(start)
		if err != nil {
			t.Fatalf("benchmark search %d failed: %v", i, err)
		}
		if len(resp.GetItems()) == 0 {
			t.Fatalf("benchmark search %d (%q) returned 0 items", i, q.GetQuery())
		}
		latencies = append(latencies, lat)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[runs*50/100]
	p95 := latencies[runs*95/100]
	p99 := latencies[runs*99/100]
	t.Logf("20k search latency over %d queries: p50=%v p95=%v p99=%v max=%v", runs, p50, p95, p99, latencies[runs-1])

	if p95 >= 200*time.Millisecond {
		t.Fatalf("p95 latency %v exceeded 200ms SLA", p95)
	}
}
