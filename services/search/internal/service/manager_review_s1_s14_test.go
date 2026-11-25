package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	searchv1 "github.com/nexusmart/core/gen/go/nexusmart/search/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/services/search/internal/embedder"
	"github.com/nexusmart/core/services/search/internal/events"
	"github.com/nexusmart/core/services/search/internal/indexer"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/store"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPG_S1_MeiliFailureRollsBackTXAndRetriesCleanly(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// 1. Upsert when Meili fails -> MUST return error, roll back processed_events, and succeed on retry!
	h.meili.failUpsert.Store(true)
	err := h.idx.HandleProductUpserted(ctx, "ev-s1-up", indexer.ProductUpsertInput{
		ProductID: "p-s1", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo khoác S1", Slug: "ao-khoac-s1",
		MinPriceVND: 200000, MaxPriceVND: 200000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	})
	if err == nil {
		t.Fatalf("S1: expected error when Meilisearch upsert fails")
	}
	var processed bool
	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = 'ev-s1-up')`).Scan(&processed)
	if processed {
		t.Fatalf("S1: processed_events must be rolled back when Meilisearch upsert fails")
	}

	h.meili.failUpsert.Store(false)
	if err := h.idx.HandleProductUpserted(ctx, "ev-s1-up", indexer.ProductUpsertInput{
		ProductID: "p-s1", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo khoác S1", Slug: "ao-khoac-s1",
		MinPriceVND: 200000, MaxPriceVND: 200000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("S1: retry HandleProductUpserted failed: %v", err)
	}
	if !h.meili.HasDoc("p-s1") {
		t.Fatalf("S1: expected p-s1 in Meilisearch after successful retry")
	}

	// 2. Delete when Meili fails -> MUST return error, roll back processed_events, and remove doc on retry!
	h.meili.failDelete.Store(true)
	err = h.idx.HandleProductDeleted(ctx, "ev-s1-del", "p-s1", "shop-1", now.Add(time.Second))
	if err == nil {
		t.Fatalf("S1: expected error when Meilisearch delete fails")
	}
	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = 'ev-s1-del')`).Scan(&processed)
	if processed {
		t.Fatalf("S1: processed_events must be rolled back when Meilisearch delete fails")
	}

	h.meili.failDelete.Store(false)
	if err := h.idx.HandleProductDeleted(ctx, "ev-s1-del", "p-s1", "shop-1", now.Add(time.Second)); err != nil {
		t.Fatalf("S1: retry HandleProductDeleted failed: %v", err)
	}
	if h.meili.HasDoc("p-s1") {
		t.Fatalf("S1: p-s1 must be removed from Meilisearch after delete retry")
	}
}

func TestPG_S2_CategoryUpdatedBranchFailureRetriesCleanly(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(10 * time.Second)

	_ = h.idx.HandleCategoryUpdated(ctx, "ev-s2-p1", indexer.CategoryUpdateInput{
		CategoryID: "cat-old", Name: "Cha Cũ", Slug: "cha-cu", Action: "UPSERT", UpdatedAt: t1,
	})
	_ = h.idx.HandleCategoryUpdated(ctx, "ev-s2-p2", indexer.CategoryUpdateInput{
		CategoryID: "cat-new", Name: "Cha Mới", Slug: "cha-moi", Action: "UPSERT", UpdatedAt: t1,
	})
	_ = h.idx.HandleCategoryUpdated(ctx, "ev-s2-ch", indexer.CategoryUpdateInput{
		CategoryID: "cat-sub", Name: "Con", Slug: "con", ParentID: "cat-old", Action: "UPSERT", UpdatedAt: t1,
	})
	if err := h.idx.HandleProductUpserted(ctx, "ev-s2-prod", indexer.ProductUpsertInput{
		ProductID: "p-s2", ShopID: "shop-1", CategoryID: "cat-sub",
		Name: "Áo S2", Slug: "ao-s2",
		MinPriceVND: 100000, MaxPriceVND: 100000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t1,
	}); err != nil {
		t.Fatalf("seed p-s2: %v", err)
	}

	// Attempt 1: Meili upsert fails during branch reindex -> MUST error and NOT record processed_events.
	h.meili.failUpsert.Store(true)
	err := h.idx.HandleCategoryUpdated(ctx, "ev-s2-reparent", indexer.CategoryUpdateInput{
		CategoryID: "cat-sub", Name: "Con", Slug: "con", ParentID: "cat-new", Action: "UPSERT", UpdatedAt: t2,
	})
	if err == nil {
		t.Fatalf("S2: expected error when Meilisearch branch update fails")
	}
	var processed bool
	_ = h.st.Pool().QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = 'ev-s2-reparent')`).Scan(&processed)
	if processed {
		t.Fatalf("S2: processed_events must NOT be saved before branch reindex succeeds")
	}

	// Attempt 2: Meili recovers -> redelivery MUST re-run branch reindex and update Meilisearch.
	h.meili.failUpsert.Store(false)
	if err := h.idx.HandleCategoryUpdated(ctx, "ev-s2-reparent", indexer.CategoryUpdateInput{
		CategoryID: "cat-sub", Name: "Con", Slug: "con", ParentID: "cat-new", Action: "UPSERT", UpdatedAt: t2,
	}); err != nil {
		t.Fatalf("S2: retry HandleCategoryUpdated failed: %v", err)
	}
	res, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{CategoryId: "cat-new"})
	if err != nil || len(res.Items) != 1 || res.Items[0].Id != "p-s2" {
		t.Fatalf("S2: expected p-s2 under cat-new after retry, got err=%v items=%+v", err, res.Items)
	}
}

func TestPG_S3_TextHashOnlyChangesWithEmbedding(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	t1 := time.Now().UTC().Truncate(time.Second)
	t2 := t1.Add(5 * time.Second)

	// 1. Initial index with healthy embedder -> stores embedding + text_hash v1.
	if err := h.idx.HandleProductUpserted(ctx, "ev-s3-1", indexer.ProductUpsertInput{
		ProductID: "p-s3", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Áo thun cotton cũ", Slug: "ao-thun-cu",
		MinPriceVND: 100000, MaxPriceVND: 100000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t1,
	}); err != nil {
		t.Fatalf("seed p-s3: %v", err)
	}
	var hash1 string
	_ = h.st.Pool().QueryRow(ctx, `SELECT text_hash FROM product_embeddings WHERE product_id = 'p-s3'`).Scan(&hash1)
	if hash1 == "" {
		t.Fatalf("expected non-empty initial text_hash")
	}

	// 2. Update text while embedder is failing -> embedding stays v1, so text_hash MUST remain hash1!
	h.emb.fail.Store(true)
	if err := h.idx.HandleProductUpserted(ctx, "ev-s3-2", indexer.ProductUpsertInput{
		ProductID: "p-s3", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Giày chạy bộ thể thao mới", Slug: "giay-chay-bo-moi",
		MinPriceVND: 500000, MaxPriceVND: 500000, Status: "APPROVED", TotalStock: 5, UpdatedAt: t2,
	}); err != nil {
		t.Fatalf("update p-s3 with failing embedder: %v", err)
	}
	h.emb.fail.Store(false)

	var hashAfterFail string
	_ = h.st.Pool().QueryRow(ctx, `SELECT text_hash FROM product_embeddings WHERE product_id = 'p-s3'`).Scan(&hashAfterFail)
	if hashAfterFail != hash1 {
		t.Fatalf("S3: text_hash changed to %q while embedding stayed stale (want %q)", hashAfterFail, hash1)
	}

	// 3. ReindexAll(ReindexEmbeddings=true) with healthy embedder MUST recompute embedding now!
	h.prodClient.products = []*productv1.IndexableProduct{
		{
			Id: "p-s3", ShopId: "shop-1", CategoryId: "cat-1",
			Name: "Giày chạy bộ thể thao mới", Slug: "giay-chay-bo-moi",
			MinPriceVnd: 500000, MaxPriceVnd: 500000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 5,
			UpdatedAt: timestamppb.New(t2),
		},
	}
	callsBefore := h.emb.calls.Load()
	adminCtx := userCtx("admin-s3", auth.RoleAdmin)
	resp, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
		ReindexProducts: true, ReindexEmbeddings: true,
	})
	if err != nil {
		t.Fatalf("ReindexAll S3: %v", err)
	}
	waitUntil(t, 5*time.Second, "S3 reindex completed", func() bool {
		st, err := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: resp.JobId})
		return err == nil && st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED
	})
	if h.emb.calls.Load() == callsBefore {
		t.Fatalf("S3: expected ReindexAll to recompute stale embedding when previous embed failed")
	}
	var hashAfterReindex string
	_ = h.st.Pool().QueryRow(ctx, `SELECT text_hash FROM product_embeddings WHERE product_id = 'p-s3'`).Scan(&hashAfterReindex)
	if hashAfterReindex == hash1 {
		t.Fatalf("S3: expected text_hash to advance after embedding recomputed")
	}
}

func TestPG_S4_ReindexResurrectsNewerAndSweepsMissingAndMeiliTombstones(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	tOld := time.Now().UTC().Add(-2 * time.Minute)
	tTomb := time.Now().UTC().Add(-1 * time.Minute)
	tNewer := time.Now().UTC().Add(-10 * time.Second)

	// Seed 3 products:
	// (a) p-reapprove: tombstoned at tTomb, later re-approved at tNewer (lost event) -> ReindexAll MUST resurrect!
	_ = h.idx.HandleProductDeleted(ctx, "ev-s4-tomb-reapp", "p-reapprove", "shop-1", tTomb)
	// (b) p-missing: live at tOld, NOT returned by ProductService keyset -> ReindexAll MUST tombstone + delete from Meili!
	_ = h.idx.HandleProductUpserted(ctx, "ev-s4-missing", indexer.ProductUpsertInput{
		ProductID: "p-missing", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Sản phẩm đã mất event xoá", Slug: "sp-mat-event",
		MinPriceVND: 100000, MaxPriceVND: 100000, Status: "APPROVED", TotalStock: 5, UpdatedAt: tOld,
	})
	// (c) p-meili-ghost: tombstoned in PG, artificially lingering in Meili -> ReindexAll MUST delete from Meili!
	_ = h.idx.HandleProductDeleted(ctx, "ev-s4-ghost", "p-meili-ghost", "shop-1", tTomb)
	_ = h.meili.UpsertProducts(ctx, []meili.ProductDoc{{
		ID: "p-meili-ghost", ShopID: "shop-1", Name: "Ghost", NameFolded: "ghost", Status: "APPROVED", InStock: true,
	}}, true)

	h.prodClient.products = []*productv1.IndexableProduct{
		{
			Id: "p-reapprove", ShopId: "shop-1", CategoryId: "cat-1",
			Name: "Áo đã duyệt lại", Slug: "ao-da-duyet-lai",
			MinPriceVnd: 220000, MaxPriceVnd: 220000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 10,
			UpdatedAt: timestamppb.New(tNewer),
		},
	}

	adminCtx := userCtx("admin-s4", auth.RoleAdmin)
	resp, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
		ReindexProducts: true, ReindexEmbeddings: true,
	})
	if err != nil {
		t.Fatalf("ReindexAll S4: %v", err)
	}
	waitUntil(t, 5*time.Second, "S4 reindex completed", func() bool {
		st, err := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: resp.JobId})
		return err == nil && st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED
	})

	// (a) p-reapprove resurrected!
	var reappDeleted bool
	_ = h.st.Pool().QueryRow(ctx, `SELECT deleted FROM product_embeddings WHERE product_id = 'p-reapprove'`).Scan(&reappDeleted)
	if reappDeleted || !h.meili.HasDoc("p-reapprove") {
		t.Fatalf("S4: expected p-reapprove resurrected (deleted=%v, inMeili=%v)", reappDeleted, h.meili.HasDoc("p-reapprove"))
	}

	// (b) p-missing swept & tombstoned!
	var missingDeleted bool
	_ = h.st.Pool().QueryRow(ctx, `SELECT deleted FROM product_embeddings WHERE product_id = 'p-missing'`).Scan(&missingDeleted)
	if !missingDeleted || h.meili.HasDoc("p-missing") {
		t.Fatalf("S4: expected p-missing tombstoned and removed from Meili (deleted=%v, inMeili=%v)", missingDeleted, h.meili.HasDoc("p-missing"))
	}

	// (c) p-meili-ghost purged from Meili!
	if h.meili.HasDoc("p-meili-ghost") {
		t.Fatalf("S4: expected lingering tombstone p-meili-ghost deleted from Meilisearch")
	}
}

func TestPG_S5_TrendingVocabularyGateDailyBucketsAndMinScore3(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	_ = h.idx.HandleCategoryUpdated(ctx, "ev-s5-c1", indexer.CategoryUpdateInput{
		CategoryID: "cat-s5", Name: "Áo sơ mi", Slug: "ao-so-mi", Action: "UPSERT", UpdatedAt: now,
	})
	h.userClient.shops["shop-s5"] = &userv1.Shop{
		Id: "shop-s5", Name: "CoolWear", Slug: "coolwear",
		Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
	}
	_ = h.idx.HandleShopReviewed(ctx, "ev-s5-s1", indexer.ShopReviewedInput{ShopID: "shop-s5", Decision: "APPROVE"})
	_ = h.idx.HandleProductUpserted(ctx, "ev-s5-p1", indexer.ProductUpsertInput{
		ProductID: "p-s5", ShopID: "shop-s5", CategoryID: "cat-s5",
		Name: "Áo sơ mi trắng công sở", Description: "liên hệ 0901234567 hoặc admin@example.com",
		Slug: "ao-so-mi-trang", MinPriceVND: 250000, MaxPriceVND: 250000,
		Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	})

	// 1. Queries NOT substring of any page-1 product/category/shop NAME (e.g. phone/email from description, profanity)
	// MUST NEVER be recorded into trending even after 5 searches!
	for i := 0; i < 5; i++ {
		_, _ = h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "0901234567"})
		_, _ = h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "admin@example.com"})
		_, _ = h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "tu tuc tiu khong co"})
	}

	// 2. Valid marketplace vocabulary ("ao so mi") searched only 2 times (< 3) MUST NOT appear yet.
	for i := 0; i < 2; i++ {
		_, _ = h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "Áo sơ mi"})
	}
	sug2, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "", Limit: 10})
	if err != nil {
		t.Fatalf("Suggest after 2 searches: %v", err)
	}
	if len(sug2.Trending) != 0 {
		t.Fatalf("S5: expected 0 trending terms before min score 3 (and no PII), got %+v", sug2.Trending)
	}

	// 3. Third search reaches score = 3 -> appears in trending via daily key search:trending:YYYYMMDD (TTL ~8d).
	_, _ = h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "ao so mi"})
	todayKey := fmt.Sprintf("search:trending:%s", time.Now().UTC().Format("20060102"))
	ttl, err := h.rdb.TTL(ctx, todayKey).Result()
	if err != nil || ttl <= 7*24*time.Hour || ttl > 8*24*time.Hour+time.Minute {
		t.Fatalf("S5: expected daily key %s with ~8d TTL, got ttl=%v err=%v", todayKey, ttl, err)
	}

	// Seed an expired 8-day-old bucket with huge score -> MUST NOT appear in 7-day ZUNION!
	oldKey := fmt.Sprintf("search:trending:%s", time.Now().UTC().AddDate(0, 0, -8).Format("20060102"))
	_ = h.rdb.ZAdd(ctx, oldKey, redis.Z{Score: 999, Member: "tu cu 8 ngay truoc"}).Err()

	sug3, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "", Limit: 10})
	if err != nil {
		t.Fatalf("Suggest after 3 searches: %v", err)
	}
	if len(sug3.Trending) != 1 || sug3.Trending[0] != "ao so mi" {
		t.Fatalf("S5: expected trending=['ao so mi'] (excluding PII and 8-day-old bucket), got %+v", sug3.Trending)
	}
}

func TestPG_S6_VectorMinSimilarityAndNonRelevanceSortKeywordOnly(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	for i, name := range []string{"Áo thun nam cotton thoáng mát", "Quần short chạy bộ", "Đồ lót boxer nam"} {
		_ = h.idx.HandleProductUpserted(ctx, fmt.Sprintf("ev-s6-%d", i), indexer.ProductUpsertInput{
			ProductID: fmt.Sprintf("p-s6-%d", i), ShopID: "shop-1", CategoryID: "cat-1",
			Name: name, Slug: fmt.Sprintf("p-s6-%d", i),
			MinPriceVND: int64(100000 * (i + 1)), MaxPriceVND: int64(100000 * (i + 1)),
			Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
		})
	}

	// 1. Nonsense 3-word query -> vector min similarity (0.25) filters out unrelated products -> 0 hits!
	nonsense, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q:    "zxqwy plmkn vfrtg",
		Mode: searchv1.SearchMode_SEARCH_MODE_AUTO,
	})
	if err != nil {
		t.Fatalf("SearchProducts nonsense: %v", err)
	}
	if len(nonsense.Items) != 0 || nonsense.Total != 0 {
		t.Fatalf("S6: expected 0 hits for unrelated nonsense query under min_similarity, got total=%d items=%d", nonsense.Total, len(nonsense.Items))
	}

	// 2. Non-relevance sort_by (e.g. price_asc) with >=3 words -> keyword-only (used_vector=false)!
	sorted, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q:      "ao thun nam cotton",
		Mode:   searchv1.SearchMode_SEARCH_MODE_AUTO,
		SortBy: "price_asc",
	})
	if err != nil {
		t.Fatalf("SearchProducts price_asc: %v", err)
	}
	if sorted.UsedVector {
		t.Fatalf("S6: expected used_vector=false when sort_by='price_asc'")
	}
}

func TestPG_S7_HybridPaginationTotalAndKeywordOnlyFallbackOffset(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed 215 public products (> 200 hybrid candidate cap!) matching "ao thun nam".
	for i := 1; i <= 215; i++ {
		pid := fmt.Sprintf("p-s7-%03d", i)
		_ = h.idx.HandleProductUpserted(ctx, "ev-"+pid, indexer.ProductUpsertInput{
			ProductID: pid, ShopID: "shop-1", CategoryID: "cat-1",
			Name: fmt.Sprintf("Áo thun nam cotton số %03d", i), Slug: pid,
			MinPriceVND: 100000 + int64(i)*100, MaxPriceVND: 100000 + int64(i)*100,
			Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
		})
	}

	// 1. Hybrid mode: total MUST equal real len(fused) (<= 200, NOT inflated to Meili's 215!),
	//    so the last reachable page ceil(total/20) is guaranteed non-empty!
	p1, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q: "ao thun nam", Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID, Page: 1, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("S7 hybrid page 1: %v", err)
	}
	if p1.Total <= 0 || p1.Total > 200 {
		t.Fatalf("S7 expected hybrid total = len(fused) in (0, 200], got %d", p1.Total)
	}
	lastPage := int32((p1.Total + 19) / 20)
	pLast, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q: "ao thun nam", Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID, Page: lastPage, PageSize: 20,
	})
	if err != nil {
		t.Fatalf("S7 hybrid last page %d: %v", lastPage, err)
	}
	if len(pLast.Items) == 0 {
		t.Fatalf("S7: hybrid last page %d (total=%d) returned 0 items!", lastPage, p1.Total)
	}

	// 2. keyword_only fallback when embedder fails: page 11 (offset 200..215, beyond 200!)
	//    MUST paginate directly against Meilisearch and return 15 items with total=215!
	h.emb.fail.Store(true)
	p11, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
		Q: "ao thun nam", Mode: searchv1.SearchMode_SEARCH_MODE_HYBRID, Page: 11, PageSize: 20,
	})
	h.emb.fail.Store(false)
	if err != nil {
		t.Fatalf("S7 keyword_only page 11: %v", err)
	}
	if p11.DegradedMode != "keyword_only" || p11.Total != 215 || len(p11.Items) != 15 {
		t.Fatalf("S7: keyword_only page 11 want degraded='keyword_only' total=215 items=15, got degraded=%q total=%d items=%d",
			p11.DegradedMode, p11.Total, len(p11.Items))
	}
}

func TestPG_S8_HNSWNarrowCategoryFilter300Products(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()

	// Insert 300 products directly into product_embeddings with an HNSW index:
	// - 295 in cat-broad with vectors very close to queryVec = [1, 0, 0, ...]
	// - 5 in cat-narrow with vectors at cosine similarity ~0.50 (further than all 295 broad docs!)
	queryVec := make([]float32, embedder.DefaultDim)
	queryVec[0] = 1.0

	for i := 0; i < 295; i++ {
		v := make([]float32, embedder.DefaultDim)
		v[0] = 0.999
		v[1+(i%100)] = 0.0447
		vec := pgvector.NewVector(v)
		_, err := h.st.Pool().Exec(ctx, `
			INSERT INTO product_embeddings (
				product_id, shop_id, category_id, category_ancestor_ids,
				name, slug, min_price_vnd, max_price_vnd, status, in_stock, deleted, text_hash, embedding, source_updated_at
			) VALUES ($1, 'shop-1', 'cat-broad', ARRAY['cat-broad'], $2, $1, 100000, 100000, 'APPROVED', TRUE, FALSE, 'h', $3, NOW())
		`, fmt.Sprintf("p-broad-%03d", i), fmt.Sprintf("Broad %03d", i), vec)
		if err != nil {
			t.Fatalf("insert broad %d: %v", i, err)
		}
	}
	for i := 0; i < 5; i++ {
		v := make([]float32, embedder.DefaultDim)
		v[0] = 0.5
		v[10+i] = 0.866
		vec := pgvector.NewVector(v)
		_, err := h.st.Pool().Exec(ctx, `
			INSERT INTO product_embeddings (
				product_id, shop_id, category_id, category_ancestor_ids,
				name, slug, min_price_vnd, max_price_vnd, status, in_stock, deleted, text_hash, embedding, source_updated_at
			) VALUES ($1, 'shop-1', 'cat-narrow', ARRAY['cat-narrow'], $2, $1, 100000, 100000, 'APPROVED', TRUE, FALSE, 'h', $3, NOW())
		`, fmt.Sprintf("p-narrow-%d", i), fmt.Sprintf("Narrow %d", i), vec)
		if err != nil {
			t.Fatalf("insert narrow %d: %v", i, err)
		}
	}
	// Analyze and open a session forcing HNSW index scan (disabling seqscan/bitmapscan) so
	// PostgreSQL MUST traverse idx_product_embeddings_hnsw with SET LOCAL ef_search=200 + iterative_scan='relaxed_order'.
	_, _ = h.st.Pool().Exec(ctx, `ANALYZE product_embeddings`)
	hnswCfg, err := pgxpool.ParseConfig(h.databaseURL)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	hnswCfg.AfterConnect = func(cctx context.Context, conn *pgx.Conn) error {
		if err := pgxvec.RegisterTypes(cctx, conn); err != nil {
			return err
		}
		_, err := conn.Exec(cctx, `SET enable_seqscan = off; SET enable_bitmapscan = off;`)
		return err
	}
	hnswPool, err := pgxpool.NewWithConfig(ctx, hnswCfg)
	if err != nil {
		t.Fatalf("open hnsw test pool: %v", err)
	}
	t.Cleanup(hnswPool.Close)
	hnswStore := store.New(hnswPool)

	hits, err := hnswStore.SearchVector(ctx, store.VectorSearchParams{
		Embedding:     queryVec,
		CategoryID:    "cat-narrow",
		MinSimilarity: 0.25,
		Limit:         20,
	})
	if err != nil {
		t.Fatalf("S8 SearchVector narrow: %v", err)
	}
	if len(hits) != 5 {
		t.Fatalf("S8: expected all 5 cat-narrow products out of 300 via HNSW ef_search=200 + iterative_scan, got %d", len(hits))
	}
}

func TestPG_S9_BuildFacetsLoadsOnlyFacetIDs(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed 25 categories & 25 shops, only 1 category and 1 shop present in search results.
	for i := 1; i <= 25; i++ {
		cid := fmt.Sprintf("cat-s9-%02d", i)
		sid := fmt.Sprintf("shop-s9-%02d", i)
		_ = h.idx.HandleCategoryUpdated(ctx, "ev-s9-c-"+cid, indexer.CategoryUpdateInput{
			CategoryID: cid, Name: fmt.Sprintf("Danh mục %02d", i), Slug: cid, Action: "UPSERT", UpdatedAt: now,
		})
		h.userClient.shops[sid] = &userv1.Shop{
			Id: sid, Name: fmt.Sprintf("Gian hàng %02d", i), Slug: sid,
			Status: userv1.ShopStatus_SHOP_STATUS_ACTIVE, StatusVersion: 1,
		}
		_ = h.idx.HandleShopReviewed(ctx, "ev-s9-s-"+sid, indexer.ShopReviewedInput{ShopID: sid, Decision: "APPROVE"})
	}
	if err := h.idx.HandleProductUpserted(ctx, "ev-s9-p1", indexer.ProductUpsertInput{
		ProductID: "p-s9-1", ShopID: "shop-s9-07", CategoryID: "cat-s9-07",
		Name: "Áo khoác đặc biệt S9", Slug: "ao-khoac-s9",
		MinPriceVND: 250000, MaxPriceVND: 250000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed p-s9-1: %v", err)
	}

	// Verify ListCategoriesByIDs & ListShopsByIDs only fetch requested IDs.
	cats, err := h.st.Queries().ListCategoriesByIDs(ctx, []string{"cat-s9-07"})
	if err != nil || len(cats) != 1 || cats[0].CategoryID != "cat-s9-07" {
		t.Fatalf("S9 ListCategoriesByIDs got err=%v len=%d", err, len(cats))
	}
	shops, err := h.st.Queries().ListShopsByIDs(ctx, []string{"shop-s9-07"})
	if err != nil || len(shops) != 1 || shops[0].ShopID != "shop-s9-07" {
		t.Fatalf("S9 ListShopsByIDs got err=%v len=%d", err, len(shops))
	}

	res, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{Q: "ao khoac dac biet s9"})
	if err != nil {
		t.Fatalf("S9 SearchProducts: %v", err)
	}
	if len(res.CategoryFacets) != 1 || res.CategoryFacets[0].Name != "Danh mục 07" {
		t.Fatalf("S9 category facets = %+v", res.CategoryFacets)
	}
	if len(res.ShopFacets) != 1 || res.ShopFacets[0].Name != "Gian hàng 07" {
		t.Fatalf("S9 shop facets = %+v", res.ShopFacets)
	}
}

func TestPG_S12_SuggestFallbackVietnameseUnaccented(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := h.idx.HandleProductUpserted(ctx, "ev-s12", indexer.ProductUpsertInput{
		ProductID: "p-s12", ShopID: "shop-1", CategoryID: "cat-1",
		Name: "Đồ lót boxer nam gỗ sồi", Slug: "sp-boxer-001",
		MinPriceVND: 150000, MaxPriceVND: 150000, Status: "APPROVED", TotalStock: 5, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed p-s12: %v", err)
	}

	// Simulate Meilisearch offline -> Suggest falls back to SuggestFallbackProducts in Postgres.
	h.meili.failSearch.Store(true)
	sug, err := h.svc.Suggest(ctx, &searchv1.SuggestRequest{Query: "do lot", Limit: 5})
	h.meili.failSearch.Store(false)
	if err != nil {
		t.Fatalf("S12 Suggest fallback: %v", err)
	}
	if len(sug.Products) != 1 || sug.Products[0].Id != "p-s12" {
		t.Fatalf("S12: expected SuggestFallbackProducts to match 'do lot' against 'Đồ lót boxer nam gỗ sồi', got %+v", sug.Products)
	}
}

func TestPG_S14_ColorFilterMatchesNameOrColorCode(t *testing.T) {
	h := setupIntegrationHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Seed product via ReindexAll with proto IndexableProduct carrying Colors=["Trắng tinh"] & ColorCodes=["trang"].
	h.prodClient.products = []*productv1.IndexableProduct{
		{
			Id: "p-s14-white", ShopId: "shop-1", CategoryId: "cat-1", CategoryAncestorIds: []string{"cat-1"},
			Name: "Áo sơ mi cổ tàu cao cấp", Slug: "ao-so-mi-co-tau",
			Description: "Áo sơ mi màu trắng tinh mặc công sở thoáng mát",
			MinPriceVnd: 320000, MaxPriceVnd: 320000,
			Status: productv1.ProductStatus_PRODUCT_STATUS_APPROVED, TotalStock: 10, SkuCount: 1,
			Colors: []string{"Trắng tinh"}, ColorCodes: []string{"trang"},
			Sizes: []string{"XL (60kg)"}, UpdatedAt: timestamppb.New(now),
		},
	}

	adminCtx := userCtx("admin-s14", auth.RoleAdmin)
	resp, err := h.svc.ReindexAll(adminCtx, &searchv1.ReindexAllRequest{
		ReindexProducts: true, ReindexEmbeddings: true,
	})
	if err != nil {
		t.Fatalf("S14 ReindexAll: %v", err)
	}
	waitUntil(t, 5*time.Second, "S14 reindex completed", func() bool {
		st, err := h.svc.GetReindexStatus(adminCtx, &searchv1.GetReindexStatusRequest{JobId: resp.JobId})
		return err == nil && st.Status == searchv1.ReindexJobStatus_REINDEX_JOB_STATUS_COMPLETED
	})

	for _, mode := range []searchv1.SearchMode{searchv1.SearchMode_SEARCH_MODE_KEYWORD, searchv1.SearchMode_SEARCH_MODE_HYBRID} {
		for _, colorQuery := range []string{"trang", "Trắng tinh"} {
			r, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
				Q:     "ao so mi co tau",
				Color: colorQuery,
				Mode:  mode,
			})
			if err != nil {
				t.Fatalf("S14 SearchProducts(mode=%v, color=%q): %v", mode, colorQuery, err)
			}
			if len(r.Items) != 1 || r.Items[0].Id != "p-s14-white" {
				t.Fatalf("S14: mode=%v color=%q want [p-s14-white], got %+v", mode, colorQuery, r.Items)
			}
		}
	}

	// Also verify product.upserted NATS JSON event carries color_codes -> matches both color name & color_code!
	consumer := events.NewConsumer(nil, nil, h.st, h.idx, nil, "NEXUSMART")
	rawEvt, _ := json.Marshal(events.Envelope{
		EventID:    "ev-s14-black",
		EventType:  events.SubjectProductUpserted,
		OccurredAt: now,
		Payload: json.RawMessage(`{
			"product_id":"p-s14-black","shop_id":"shop-1","category_id":"cat-1",
			"name":"Quần jogger đen tuyền","slug":"quan-jogger-den-tuyen",
			"min_price_vnd":290000,"max_price_vnd":290000,"status":"APPROVED","total_stock":8,"sku_count":1,
			"colors":["Đen tuyền"],"color_codes":["den"],"sizes":["L"]
		}`),
	})
	if act, err := consumer.ProcessRawMessage(ctx, events.SubjectProductUpserted, rawEvt, nil, 1); err != nil || act != events.ActionAck {
		t.Fatalf("S14 event product.upserted failed: action=%v err=%v", act, err)
	}
	for _, colorQ := range []string{"den", "Đen tuyền"} {
		r, err := h.svc.SearchProducts(ctx, &searchv1.SearchProductsRequest{
			Q:     "quan jogger",
			Color: colorQ,
		})
		if err != nil || len(r.Items) != 1 || r.Items[0].Id != "p-s14-black" {
			t.Fatalf("S14 event color=%q want [p-s14-black], got err=%v items=%+v", colorQ, err, r.Items)
		}
	}
}
