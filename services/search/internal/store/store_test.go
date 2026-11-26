package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/nexusmart/core/services/search/migrations"
	"github.com/pgvector/pgvector-go"
)

func TestPG_S10_AcquireReindexLock_HijackClosesConnOnUnlockFailure(t *testing.T) {
	dbURL := strings.TrimSpace(os.Getenv("TEST_SEARCH_DB_URL"))
	if dbURL == "" {
		t.Skip("TEST_SEARCH_DB_URL not set; run via TEST_WITH_SEARCH_DB=1 bash scripts/test-db.sh --run ...")
	}

	ctx := context.Background()
	pool, err := OpenPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)
	st := New(pool)

	// Acquire lock with an immediately-expiring unlock timeout (1ns) so pg_advisory_unlock fails
	// with context.DeadlineExceeded while the Postgres session is still alive holding the lock.
	release, acquired, err := st.acquireReindexLockWithUnlockTimeout(ctx, 1*time.Nanosecond)
	if err != nil || !acquired {
		t.Fatalf("initial AcquireReindexLock failed: acquired=%v err=%v", acquired, err)
	}

	// While held, a second caller cannot acquire the lock.
	_, acquired2, err := st.AcquireReindexLock(ctx)
	if err != nil || acquired2 {
		t.Fatalf("expected second AcquireReindexLock to be rejected while lock held: acquired=%v err=%v", acquired2, err)
	}

	// Calling release() triggers unlock error -> S10 MUST Hijack().Close() the connection instead
	// of returning it to the pool with the lock still held!
	release()

	// Postgres closes the session and frees the session advisory lock; a new AcquireReindexLock MUST succeed!
	var (
		relAfter func()
		okAfter  bool
	)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		relAfter, okAfter, err = st.AcquireReindexLock(ctx)
		if err != nil {
			t.Fatalf("AcquireReindexLock after hijack-close error: %v", err)
		}
		if okAfter {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if !okAfter {
		t.Fatalf("S10: lock remained permanently stuck after failed pg_advisory_unlock!")
	}
	relAfter()
}

func TestDetectIterativeScan_SupportedUnsupportedAndTransientRetry(t *testing.T) {
	ctx := context.Background()

	t.Run("pgvector >= 0.8 returns true and caches probe", func(t *testing.T) {
		var probes atomic.Int32
		st := &Store{
			probeIterScanFn: func(context.Context) (bool, error) {
				probes.Add(1)
				return true, nil
			},
		}
		if !st.detectIterativeScan(ctx) {
			t.Fatalf("expected detectIterativeScan=true on first call when hnsw.iterative_scan is supported")
		}
		if !st.detectIterativeScan(ctx) {
			t.Fatalf("expected detectIterativeScan=true on cached second call")
		}
		if probes.Load() != 1 {
			t.Fatalf("expected probe cached after 1 call, got %d", probes.Load())
		}
	})

	t.Run("pgvector < 0.8 returns false without failing and caches probe", func(t *testing.T) {
		var probes atomic.Int32
		st := &Store{
			probeIterScanFn: func(context.Context) (bool, error) {
				probes.Add(1)
				return false, nil
			},
		}
		if st.detectIterativeScan(ctx) {
			t.Fatalf("expected detectIterativeScan=false on first call when pgvector < 0.8")
		}
		if st.detectIterativeScan(ctx) {
			t.Fatalf("expected detectIterativeScan=false on cached second call")
		}
		if probes.Load() != 1 {
			t.Fatalf("expected false result cached after 1 call, got %d", probes.Load())
		}
	})

	t.Run("transient probe error does not poison cache", func(t *testing.T) {
		var probes atomic.Int32
		st := &Store{
			probeIterScanFn: func(context.Context) (bool, error) {
				if probes.Add(1) == 1 {
					return false, errors.New("connection reset")
				}
				return true, nil
			},
		}
		if st.detectIterativeScan(ctx) {
			t.Fatalf("expected false on transient probe error")
		}
		if !st.detectIterativeScan(ctx) {
			t.Fatalf("expected second probe after transient error to succeed and return true")
		}
		if probes.Load() != 2 {
			t.Fatalf("expected 2 probes total (retry then cache), got %d", probes.Load())
		}
		if !st.detectIterativeScan(ctx) || probes.Load() != 2 {
			t.Fatalf("expected cached true on 3rd call, probes=%d", probes.Load())
		}
	})
}

func TestTrendingStore_DayKeyPerShopIsolation(t *testing.T) {
	ts := time.Date(2026, 11, 25, 13, 0, 0, 0, time.UTC)
	tr := NewTrendingStore(nil)

	if got := tr.dayKey("", ts); got != "search:trending:20261125" {
		t.Fatalf("global dayKey = %q, want search:trending:20261125", got)
	}
	if got := tr.dayKey("shop-alpha", ts); got != "search:trending:shop-alpha:20261125" {
		t.Fatalf("shop-alpha dayKey = %q, want search:trending:shop-alpha:20261125", got)
	}
	if got := tr.dayKey(" shop-beta ", ts); got != "search:trending:shop-beta:20261125" {
		t.Fatalf("shop-beta dayKey = %q, want search:trending:shop-beta:20261125", got)
	}
	if tr.dayKey("shop-alpha", ts) == tr.dayKey("shop-beta", ts) || tr.dayKey("shop-alpha", ts) == tr.dayKey("", ts) {
		t.Fatalf("shop trending keys must be strictly isolated from each other and from global")
	}

	// Nil Redis client stays nil-safe for both global and per-shop methods.
	if err := tr.RecordForShop(context.Background(), "shop-alpha", "ao thun"); err != nil {
		t.Fatalf("nil-redis RecordForShop: %v", err)
	}
	if vals, err := tr.TopForShop(context.Background(), "shop-alpha", "", 5); err != nil || len(vals) != 0 {
		t.Fatalf("nil-redis TopForShop = (%v, %v), want empty/nil", vals, err)
	}
}

func TestMigrations_Phase0BPreservedAndUpgradeSQL(t *testing.T) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		t.Fatalf("ReadDir embedded migrations: %v", err)
	}
	var sqlFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles = append(sqlFiles, e.Name())
		}
	}
	sort.Strings(sqlFiles)

	wantFiles := []string{"000001_init.sql", "000002_w3b3_search_schema.sql"}
	if !slices.Equal(sqlFiles, wantFiles) {
		t.Fatalf("embedded migration files = %v, want %v", sqlFiles, wantFiles)
	}

	mig2Bytes, err := migrations.Files.ReadFile("000002_w3b3_search_schema.sql")
	if err != nil {
		t.Fatalf("read 000002_w3b3_search_schema.sql: %v", err)
	}
	mig2 := string(mig2Bytes)
	for _, mustContain := range []string{
		"CREATE EXTENSION IF NOT EXISTS vector;",
		"ALTER TABLE processed_events",
		"ALTER COLUMN subject SET DEFAULT ''",
		"ALTER TABLE dead_letter_events",
		"ALTER COLUMN id TYPE TEXT USING id::text",
		"ALTER COLUMN attempts SET DEFAULT 1",
		"CREATE INDEX IF NOT EXISTS idx_dead_letter_events_created_at",
		"CREATE TABLE IF NOT EXISTS search_categories",
		"CREATE TABLE IF NOT EXISTS search_shops",
		"CREATE TABLE IF NOT EXISTS product_embeddings",
		"embedding             vector(1536)",
		"CREATE TABLE IF NOT EXISTS search_history",
		"CREATE TABLE IF NOT EXISTS reindex_jobs",
	} {
		if !strings.Contains(mig2, mustContain) {
			t.Fatalf("000002_w3b3_search_schema.sql missing required upgrade clause %q", mustContain)
		}
	}
	if strings.Contains(strings.ToUpper(mig2), "DROP TABLE") {
		t.Fatalf("000002_w3b3_search_schema.sql must not drop phase-0b product_documents table")
	}
}

func TestVectorFetchLimit(t *testing.T) {
	cases := []struct {
		name  string
		limit int32
		want  int32
	}{
		{name: "zero defaults to 100 then doubles to cap", limit: 0, want: 200},
		{name: "negative defaults to 100 then doubles to cap", limit: -5, want: 200},
		{name: "small limit doubles", limit: 10, want: 20},
		{name: "hybrid default 50 doubles to 100", limit: 50, want: 100},
		{name: "search hybrid 100 doubles to 200", limit: 100, want: 200},
		{name: "over-fetch capped at 200", limit: 150, want: 200},
		{name: "at cap stays 200", limit: 200, want: 200},
		{name: "above cap respects caller limit without doubling", limit: 250, want: 250},
		{name: "max int32 does not overflow", limit: math.MaxInt32, want: math.MaxInt32},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := vectorFetchLimit(tc.limit); got != tc.want {
				t.Fatalf("vectorFetchLimit(%d) = %d, want %d", tc.limit, got, tc.want)
			}
		})
	}
}

func TestFilterAndSortVectorHits(t *testing.T) {
	t.Run("filters below min_similarity, breaks ties by product_id asc, truncates to limit", func(t *testing.T) {
		input := []VectorHit{
			{ProductID: "p-below", Similarity: 0.249},
			{ProductID: "p-tie-c", Similarity: 0.80},
			{ProductID: "p-edge", Similarity: 0.25},
			{ProductID: "p-tie-a", Similarity: 0.80},
			{ProductID: "p-top", Similarity: 0.95},
			{ProductID: "p-nan", Similarity: math.NaN()},
			{ProductID: "p-tie-b", Similarity: 0.80},
		}
		origFirst := input[0].ProductID

		got := filterAndSortVectorHits(input, 0.25, 4)
		gotIDs := make([]string, len(got))
		for i, h := range got {
			gotIDs[i] = h.ProductID
		}
		wantIDs := []string{"p-top", "p-tie-a", "p-tie-b", "p-tie-c"}
		if !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("got IDs = %v, want %v", gotIDs, wantIDs)
		}
		if input[0].ProductID != origFirst {
			t.Fatalf("input slice mutated: input[0]=%q, want %q", input[0].ProductID, origFirst)
		}

		// Boundary inclusive: exact min_similarity=0.25 kept when limit allows; below & NaN dropped.
		gotAll := filterAndSortVectorHits(input, 0.25, 10)
		gotAllIDs := make([]string, len(gotAll))
		for i, h := range gotAll {
			gotAllIDs[i] = h.ProductID
		}
		wantAllIDs := []string{"p-top", "p-tie-a", "p-tie-b", "p-tie-c", "p-edge"}
		if !slices.Equal(gotAllIDs, wantAllIDs) {
			t.Fatalf("got all IDs = %v, want %v", gotAllIDs, wantAllIDs)
		}
	})

	t.Run("min_similarity <= 0 keeps non-positive similarities and sorts NaN last", func(t *testing.T) {
		input := []VectorHit{
			{ProductID: "p-nan-b", Similarity: math.NaN()},
			{ProductID: "p-neg", Similarity: -0.1},
			{ProductID: "p-zero", Similarity: 0.0},
			{ProductID: "p-nan-a", Similarity: math.NaN()},
			{ProductID: "p-pos", Similarity: 0.3},
		}
		got := filterAndSortVectorHits(input, 0, 10)
		gotIDs := make([]string, len(got))
		for i, h := range got {
			gotIDs[i] = h.ProductID
		}
		wantIDs := []string{"p-pos", "p-zero", "p-neg", "p-nan-a", "p-nan-b"}
		if !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("got IDs = %v, want %v", gotIDs, wantIDs)
		}
	})
}

func TestVectorSearchPlannerConfigAndSQLShape(t *testing.T) {
	// SQL MUST keep a single HNSW-compatible ORDER BY distance key and omit min_similarity from WHERE.
	if !strings.Contains(vectorSearchSQL, "ORDER BY pe.embedding <=> $1::vector ASC\nLIMIT $12") {
		t.Fatalf("vectorSearchSQL must end with single-key ORDER BY pe.embedding <=> $1::vector ASC and LIMIT $12")
	}
	wherePart, _, ok := strings.Cut(vectorSearchSQL, "ORDER BY")
	if !ok {
		t.Fatalf("vectorSearchSQL missing ORDER BY")
	}
	if _, afterWhere, found := strings.Cut(wherePart, "WHERE"); !found || strings.Contains(afterWhere, "<=>") {
		t.Fatalf("vectorSearchSQL WHERE clause must not contain <=> distance threshold")
	}

	// OnlyApprovedInStock / InStockOnly / ExcludeProductID are broad visibility flags, not narrow filters.
	if hasSelectiveVectorFilter(VectorSearchParams{
		OnlyApprovedInStock: true,
		InStockOnly:         true,
		ExcludeProductID:    "p-self",
	}) {
		t.Fatalf("broad visibility / self-exclude params must not count as selective filters")
	}
	for _, p := range []VectorSearchParams{
		{ShopID: "shop-1"},
		{CategoryID: "cat-1"},
		{MinPriceVND: 100000},
		{MaxPriceVND: 500000},
		{MinRatingX100: 400},
		{SizeFolded: "m"},
		{ColorFolded: "den"},
	} {
		if !hasSelectiveVectorFilter(p) {
			t.Fatalf("expected selective filter true for %+v", p)
		}
	}

	withIter := buildVectorSearchTxConfigSQL(true)
	for _, must := range []string{
		"SET LOCAL hnsw.ef_search = 200",
		"SET LOCAL enable_seqscan = off",
		"SET LOCAL enable_bitmapscan = off",
		"SET LOCAL plan_cache_mode = force_custom_plan",
		"SET LOCAL jit = off",
		"SET LOCAL enable_sort = off",
		"SET LOCAL hnsw.iterative_scan = 'relaxed_order'",
		"SET LOCAL hnsw.max_scan_tuples = 2000",
	} {
		if !strings.Contains(withIter, must) {
			t.Fatalf("HNSW tx config missing %q: %s", must, withIter)
		}
	}

	withoutIter := buildVectorSearchTxConfigSQL(false)
	for _, must := range []string{
		"SET LOCAL hnsw.ef_search = 200",
		"SET LOCAL enable_seqscan = off",
		"SET LOCAL enable_bitmapscan = off",
		"SET LOCAL plan_cache_mode = force_custom_plan",
		"SET LOCAL jit = off",
		"SET LOCAL enable_sort = off",
	} {
		if !strings.Contains(withoutIter, must) {
			t.Fatalf("non-iterative tx config missing %q: %s", must, withoutIter)
		}
	}
	if strings.Contains(withoutIter, "hnsw.iterative_scan") || strings.Contains(withoutIter, "hnsw.max_scan_tuples") {
		t.Fatalf("tx config must omit iterative_scan/max_scan_tuples when unsupported: %s", withoutIter)
	}

	fbWithIter := buildVectorSearchFallbackConfigSQL(true)
	for _, must := range []string{
		"SET LOCAL enable_sort = on",
		"SET LOCAL hnsw.max_scan_tuples = DEFAULT",
	} {
		if !strings.Contains(fbWithIter, must) {
			t.Fatalf("fallback tx config missing %q: %s", must, fbWithIter)
		}
	}

	fbWithoutIter := buildVectorSearchFallbackConfigSQL(false)
	if fbWithoutIter != "SET LOCAL enable_sort = on" {
		t.Fatalf("non-iterative fallback tx config = %q, want %q", fbWithoutIter, "SET LOCAL enable_sort = on")
	}

	if !shouldFallbackExactVectorSearch(5, 20, VectorSearchParams{ShopID: "shop-narrow"}) {
		t.Fatalf("expected fallback when selective filter returns fewer rows than fetchLimit")
	}
	if shouldFallbackExactVectorSearch(20, 20, VectorSearchParams{ShopID: "shop-large", CategoryID: "cat-1"}) {
		t.Fatalf("broad selective filter that fills fetchLimit must not fall back to exact sort")
	}
	if shouldFallbackExactVectorSearch(5, 20, VectorSearchParams{OnlyApprovedInStock: true, InStockOnly: true}) {
		t.Fatalf("unfiltered query without selective filters must not fall back")
	}
}

func TestPG_SearchVector_HNSWPlanOrderingAndThreshold(t *testing.T) {
	dbURL := strings.TrimSpace(os.Getenv("TEST_SEARCH_DB_URL"))
	if dbURL == "" {
		t.Skip("TEST_SEARCH_DB_URL not set; run via TEST_WITH_SEARCH_DB=1 bash scripts/test-db.sh --run ...")
	}

	ctx := context.Background()
	pool, err := OpenPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)
	st := New(pool)

	if _, err := pool.Exec(ctx, `TRUNCATE TABLE product_embeddings, search_shops, search_categories`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO search_shops (shop_id, name, name_folded, slug, status, status_version) VALUES
			('shop-main', 'Shop Main', 'shop main', 'shop-main', 'ACTIVE', 1),
			('shop-narrow', 'Shop Narrow', 'shop narrow', 'shop-narrow', 'ACTIVE', 1),
			('shop-susp', 'Shop Suspended', 'shop suspended', 'shop-susp', 'SUSPENDED', 2)
	`); err != nil {
		t.Fatalf("seed search_shops: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO search_categories (category_id, name, name_folded, slug, parent_id, sort_order) VALUES
			('cat-1', 'Áo thun', 'ao thun', 'ao-thun', '', 1)
	`); err != nil {
		t.Fatalf("seed search_categories: %v", err)
	}

	const vecDim = 1536
	makeUnitVec := func(sim float64, orthIdx int) []float32 {
		v := make([]float32, vecDim)
		v[0] = float32(sim)
		rem := 1.0 - sim*sim
		if rem > 0 {
			v[orthIdx] = float32(math.Sqrt(rem))
		}
		return v
	}
	queryVec := makeUnitVec(1.0, 1)

	insertProd := func(id, shopID, status string, inStock, deleted bool, vec []float32) {
		t.Helper()
		_, execErr := pool.Exec(ctx, `
			INSERT INTO product_embeddings (
				product_id, shop_id, category_id, category_ancestor_ids,
				name, slug, min_price_vnd, max_price_vnd, status, in_stock, deleted, text_hash, embedding, source_updated_at
			) VALUES ($1, $2, 'cat-1', ARRAY['cat-1'], $1, $1, 150000, 200000, $3, $4, $5, 'h', $6, NOW())
		`, id, shopID, status, inStock, deleted, pgvector.NewVector(vec))
		if execErr != nil {
			t.Fatalf("insert product %s: %v", id, execErr)
		}
	}

	// 40 low-similarity background rows (sim = 0.10 < 0.25) so table/index has enough tuples for HNSW.
	for i := 0; i < 40; i++ {
		insertProd(fmt.Sprintf("p-bg-%02d", i), "shop-main", "APPROVED", true, false, makeUnitVec(0.10, 100+(i%50)))
	}

	// Exact-tie cohort inserted in reverse lexical order so Go tie-breaking (product_id asc) is exercised.
	tieVec := makeUnitVec(0.80, 2)
	for _, id := range []string{"p-tie-d", "p-tie-c", "p-tie-b", "p-tie-a"} {
		insertProd(id, "shop-main", "APPROVED", true, false, tieVec)
	}
	insertProd("p-top", "shop-main", "APPROVED", true, false, makeUnitVec(1.0, 1))
	insertProd("p-oos", "shop-main", "OUT_OF_STOCK", false, false, makeUnitVec(0.95, 3))
	insertProd("p-edge", "shop-main", "APPROVED", true, false, makeUnitVec(0.50, 4))

	// Visibility exclusions (all sim = 1.0 so any leak immediately floats to rank 1).
	insertProd("p-susp", "shop-susp", "APPROVED", true, false, makeUnitVec(1.0, 1))
	insertProd("p-hidden", "shop-main", "HIDDEN", true, false, makeUnitVec(1.0, 1))
	insertProd("p-deleted", "shop-main", "APPROVED", true, true, makeUnitVec(1.0, 1))

	// Narrow single-shop cohort (5 products: 4 above 0.25 with a tie, 1 below 0.25).
	narrowTieVec := makeUnitVec(0.70, 5)
	insertProd("p-narrow-0", "shop-narrow", "APPROVED", true, false, makeUnitVec(0.90, 6))
	insertProd("p-narrow-2", "shop-narrow", "APPROVED", true, false, narrowTieVec)
	insertProd("p-narrow-1", "shop-narrow", "APPROVED", true, false, narrowTieVec)
	insertProd("p-narrow-3", "shop-narrow", "APPROVED", true, false, makeUnitVec(0.55, 7))
	insertProd("p-narrow-4", "shop-narrow", "APPROVED", true, false, makeUnitVec(0.15, 8))

	if _, err := pool.Exec(ctx, `ANALYZE product_embeddings, search_shops, search_categories`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	explainInTx := func(t *testing.T, tx pgx.Tx, p VectorSearchParams) []string {
		t.Helper()
		rows, qErr := tx.Query(ctx, "EXPLAIN (FORMAT TEXT) "+vectorSearchSQL, vectorSearchArgs(p, vectorFetchLimit(p.Limit))...)
		if qErr != nil {
			t.Fatalf("EXPLAIN query: %v", qErr)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var line string
			if scanErr := rows.Scan(&line); scanErr != nil {
				t.Fatalf("scan EXPLAIN line: %v", scanErr)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate EXPLAIN lines: %v", err)
		}
		return lines
	}

	t.Run("EXPLAIN uses idx_product_embeddings_hnsw for unfiltered and broad category filters", func(t *testing.T) {
		useIter := st.detectIterativeScan(ctx)
		explainCases := []struct {
			name string
			p    VectorSearchParams
		}{
			{
				name: "unfiltered all public",
				p:    VectorSearchParams{Embedding: queryVec, OnlyApprovedInStock: false, MinSimilarity: 0.25, Limit: 10},
			},
			{
				name: "unfiltered in-stock",
				p:    VectorSearchParams{Embedding: queryVec, OnlyApprovedInStock: true, MinSimilarity: 0.25, Limit: 10},
			},
			{
				name: "broad category",
				p:    VectorSearchParams{Embedding: queryVec, CategoryID: "cat-1", MinSimilarity: 0.25, Limit: 10},
			},
			{
				name: "broad shop+category",
				p:    VectorSearchParams{Embedding: queryVec, ShopID: "shop-main", CategoryID: "cat-1", MinSimilarity: 0.25, Limit: 10},
			},
		}
		for _, tc := range explainCases {
			tx, txErr := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
			if txErr != nil {
				t.Fatalf("begin tx (%s): %v", tc.name, txErr)
			}
			if _, execErr := tx.Exec(ctx, buildVectorSearchTxConfigSQL(useIter)); execErr != nil {
				_ = tx.Rollback(ctx)
				t.Fatalf("configure tx (%s): %v", tc.name, execErr)
			}
			lines := explainInTx(t, tx, tc.p)
			_ = tx.Rollback(ctx)

			plan := strings.Join(lines, "\n")
			if !strings.Contains(plan, "Index Scan using idx_product_embeddings_hnsw") {
				t.Fatalf("expected HNSW index scan (%s), got plan:\n%s", tc.name, plan)
			}
			if strings.Contains(plan, "Seq Scan on product_embeddings") || strings.Contains(plan, "Bitmap Heap Scan on product_embeddings") {
				t.Fatalf("unexpected seq/bitmap scan on product_embeddings (%s):\n%s", tc.name, plan)
			}
			if strings.Contains(plan, "Sort Key:") {
				t.Fatalf("unexpected Sort node on HNSW first pass (%s):\n%s", tc.name, plan)
			}
			for _, line := range lines {
				if strings.Contains(line, "Filter:") && strings.Contains(line, "<=>") {
					t.Fatalf("min_similarity threshold must not be evaluated in SQL Filter (%s):\n%s", tc.name, plan)
				}
			}
		}

		// Broad shop+category has 47 rows >= fetchLimit(5)=10 so HNSW satisfies it directly in exact order.
		broadHits, searchErr := st.SearchVector(ctx, VectorSearchParams{
			Embedding:     queryVec,
			ShopID:        "shop-main",
			CategoryID:    "cat-1",
			MinSimilarity: 0.25,
			Limit:         5,
		})
		if searchErr != nil {
			t.Fatalf("SearchVector broad shop+category: %v", searchErr)
		}
		gotBroad := make([]string, len(broadHits))
		for i, h := range broadHits {
			gotBroad[i] = h.ProductID
		}
		wantBroad := []string{"p-top", "p-oos", "p-tie-a", "p-tie-b", "p-tie-c"}
		if !slices.Equal(gotBroad, wantBroad) {
			t.Fatalf("broad shop+category IDs = %v, want %v", gotBroad, wantBroad)
		}
	})

	t.Run("ordering, deterministic tie-breaking, min_similarity and visibility", func(t *testing.T) {
		hits, searchErr := st.SearchVector(ctx, VectorSearchParams{
			Embedding:     queryVec,
			MinSimilarity: 0.25,
			Limit:         5,
		})
		if searchErr != nil {
			t.Fatalf("SearchVector limit=5: %v", searchErr)
		}
		gotIDs := make([]string, len(hits))
		for i, h := range hits {
			gotIDs[i] = h.ProductID
			if h.Similarity < 0.25 {
				t.Fatalf("hit %s below min_similarity: %f", h.ProductID, h.Similarity)
			}
		}
		// Top 3 are p-top (1.0), p-oos (0.95), p-narrow-0 (0.90); next 2 must be p-tie-a, p-tie-b from the 4-way 0.80 tie.
		wantTop5 := []string{"p-top", "p-oos", "p-narrow-0", "p-tie-a", "p-tie-b"}
		if !slices.Equal(gotIDs, wantTop5) {
			t.Fatalf("limit=5 IDs = %v, want %v", gotIDs, wantTop5)
		}

		// OnlyApprovedInStock + ExcludeProductID must drop p-oos and p-top while keeping deterministic order.
		inStockHits, searchErr := st.SearchVector(ctx, VectorSearchParams{
			Embedding:           queryVec,
			OnlyApprovedInStock: true,
			ExcludeProductID:    "p-top",
			MinSimilarity:       0.25,
			Limit:               20,
		})
		if searchErr != nil {
			t.Fatalf("SearchVector OnlyApprovedInStock+Exclude: %v", searchErr)
		}
		gotInStock := make([]string, len(inStockHits))
		for i, h := range inStockHits {
			gotInStock[i] = h.ProductID
		}
		wantInStock := []string{
			"p-narrow-0",
			"p-tie-a", "p-tie-b", "p-tie-c", "p-tie-d",
			"p-narrow-1", "p-narrow-2",
			"p-narrow-3", "p-edge",
		}
		if !slices.Equal(gotInStock, wantInStock) {
			t.Fatalf("OnlyApprovedInStock+Exclude IDs = %v, want %v", gotInStock, wantInStock)
		}
	})

	t.Run("narrow single-shop filter returns all matching shop rows in order", func(t *testing.T) {
		hits, searchErr := st.SearchVector(ctx, VectorSearchParams{
			Embedding:     queryVec,
			ShopID:        "shop-narrow",
			MinSimilarity: 0.25,
			Limit:         10,
		})
		if searchErr != nil {
			t.Fatalf("SearchVector shop-narrow min_sim=0.25: %v", searchErr)
		}
		gotIDs := make([]string, len(hits))
		for i, h := range hits {
			gotIDs[i] = h.ProductID
		}
		wantIDs := []string{"p-narrow-0", "p-narrow-1", "p-narrow-2", "p-narrow-3"}
		if !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("shop-narrow min_sim=0.25 IDs = %v, want %v", gotIDs, wantIDs)
		}

		allHits, searchErr := st.SearchVector(ctx, VectorSearchParams{
			Embedding:     queryVec,
			ShopID:        "shop-narrow",
			MinSimilarity: 0,
			Limit:         10,
		})
		if searchErr != nil {
			t.Fatalf("SearchVector shop-narrow min_sim=0: %v", searchErr)
		}
		gotAll := make([]string, len(allHits))
		for i, h := range allHits {
			gotAll[i] = h.ProductID
		}
		wantAll := []string{"p-narrow-0", "p-narrow-1", "p-narrow-2", "p-narrow-3", "p-narrow-4"}
		if !slices.Equal(gotAll, wantAll) {
			t.Fatalf("shop-narrow min_sim=0 IDs = %v, want %v", gotAll, wantAll)
		}
	})

	t.Run("small shop fallback rescues all rows when HNSW first pass is incomplete", func(t *testing.T) {
		// Insert 260 closer decoy rows in shop-main (sim=0.99 > max shop-narrow sim=0.90)
		// so a bounded HNSW pass (ef_search=200 without iterative scan) returns 0 shop-narrow rows.
		for i := 0; i < 260; i++ {
			insertProd(fmt.Sprintf("p-decoy-%03d", i), "shop-main", "APPROVED", true, false, makeUnitVec(0.99, 200+(i%1000)))
		}
		if _, err := pool.Exec(ctx, `ANALYZE product_embeddings`); err != nil {
			t.Fatalf("analyze after decoy insert: %v", err)
		}

		// Verify same-transaction switch: Pass 1 uses HNSW, Pass 2 fallback switches to shop_id index + Sort.
		useIter := st.detectIterativeScan(ctx)
		narrowParams := VectorSearchParams{
			Embedding: queryVec,
			ShopID:    "shop-narrow",
			Limit:     10,
		}
		tx, txErr := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if txErr != nil {
			t.Fatalf("begin same-tx EXPLAIN: %v", txErr)
		}
		if _, execErr := tx.Exec(ctx, buildVectorSearchTxConfigSQL(useIter)); execErr != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("configure pass-1 tx: %v", execErr)
		}
		pass1Plan := strings.Join(explainInTx(t, tx, narrowParams), "\n")
		if !strings.Contains(pass1Plan, "Index Scan using idx_product_embeddings_hnsw") {
			_ = tx.Rollback(ctx)
			t.Fatalf("expected Pass 1 HNSW plan for shop-narrow, got:\n%s", pass1Plan)
		}
		if _, execErr := tx.Exec(ctx, buildVectorSearchFallbackConfigSQL(useIter)); execErr != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("configure pass-2 fallback tx: %v", execErr)
		}
		pass2Plan := strings.Join(explainInTx(t, tx, narrowParams), "\n")
		_ = tx.Rollback(ctx)
		if !strings.Contains(pass2Plan, "Index Scan using idx_product_embeddings_shop_id") || !strings.Contains(pass2Plan, "Sort") {
			t.Fatalf("expected Pass 2 exact shop_id index + Sort plan for shop-narrow, got:\n%s", pass2Plan)
		}

		// Counter-proof: Pass 1 alone without iterative scan returns fewer than 5 shop-narrow rows
		// because all 200 ef_search HNSW candidates are closer decoy/main rows.
		fetchLim := vectorFetchLimit(narrowParams.Limit)
		txProbe, txErr := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if txErr != nil {
			t.Fatalf("begin pass-1 probe tx: %v", txErr)
		}
		if _, execErr := txProbe.Exec(ctx, buildVectorSearchTxConfigSQL(false)); execErr != nil {
			_ = txProbe.Rollback(ctx)
			t.Fatalf("configure pass-1 probe tx: %v", execErr)
		}
		pass1OnlyHits, qErr := queryVectorHits(ctx, txProbe, vectorSearchArgs(narrowParams, fetchLim), fetchLim)
		_ = txProbe.Rollback(ctx)
		if qErr != nil {
			t.Fatalf("pass-1 probe query: %v", qErr)
		}
		if len(pass1OnlyHits) >= 5 {
			t.Fatalf("expected Pass 1 alone to miss shop-narrow rows behind 260 closer decoys, got %d", len(pass1OnlyHits))
		}

		// Full SearchVector (with fallback) MUST rescue all 5 shop-narrow rows even when Pass 1 misses them.
		stNoIter := New(pool)
		stNoIter.probeIterScanFn = func(context.Context) (bool, error) { return false, nil }
		rescued, searchErr := stNoIter.SearchVector(ctx, narrowParams)
		if searchErr != nil {
			t.Fatalf("SearchVector fallback rescue (no-iter): %v", searchErr)
		}
		gotRescued := make([]string, len(rescued))
		for i, h := range rescued {
			gotRescued[i] = h.ProductID
		}
		wantAll := []string{"p-narrow-0", "p-narrow-1", "p-narrow-2", "p-narrow-3", "p-narrow-4"}
		if !slices.Equal(gotRescued, wantAll) {
			t.Fatalf("fallback rescued IDs = %v, want %v", gotRescued, wantAll)
		}

		// Default Store (iterative scan + max_scan_tuples=2000 + fallback) also returns all 4/5 rows in exact order.
		iterHits, searchErr := st.SearchVector(ctx, VectorSearchParams{
			Embedding:     queryVec,
			ShopID:        "shop-narrow",
			MinSimilarity: 0.25,
			Limit:         10,
		})
		if searchErr != nil {
			t.Fatalf("SearchVector shop-narrow with iterative+fallback: %v", searchErr)
		}
		gotIter := make([]string, len(iterHits))
		for i, h := range iterHits {
			gotIter[i] = h.ProductID
		}
		wantFiltered := []string{"p-narrow-0", "p-narrow-1", "p-narrow-2", "p-narrow-3"}
		if !slices.Equal(gotIter, wantFiltered) {
			t.Fatalf("iterative+fallback shop-narrow IDs = %v, want %v", gotIter, wantFiltered)
		}
	})
}

func TestPG_ListAndSuggestCategoriesByShop(t *testing.T) {
	dbURL := strings.TrimSpace(os.Getenv("TEST_SEARCH_DB_URL"))
	if dbURL == "" {
		t.Skip("TEST_SEARCH_DB_URL not set; run via TEST_WITH_SEARCH_DB=1 bash scripts/test-db.sh --run ...")
	}

	ctx := context.Background()
	pool, err := OpenPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	t.Cleanup(pool.Close)
	st := New(pool)

	if _, err := pool.Exec(ctx, `TRUNCATE TABLE product_embeddings, search_categories`); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO search_categories (category_id, name, name_folded, slug, parent_id, sort_order) VALUES
			('cat-root',        'Thời trang', 'thoi trang', 'thoi-trang', '',         1),
			('cat-mid',         'Áo nam',     'ao nam',     'ao-nam',     'cat-root', 2),
			('cat-leaf-b',      'Áo thun',    'ao thun',    'ao-thun',    'cat-mid',  3),
			('cat-leaf-a',      'Áo polo',    'ao polo',    'ao-polo',    'cat-mid',  3),
			('cat-oos',         'Áo khoác',   'ao khoac',   'ao-khoac',   'cat-root', 4),
			('cat-leaf-no-anc', 'Phụ kiện',   'phu kien',   'phu-kien',   '',         5),
			('cat-deleted',     'Đã xóa',     'da xoa',     'da-xoa',     '',         6),
			('cat-hidden',      'Đã ẩn',      'da an',      'da-an',      '',         7),
			('cat-pending',     'Chờ duyệt',  'cho duyet',  'cho-duyet',  '',         8),
			('cat-other-shop',  'Shop khác',  'shop khac',  'shop-khac',  '',         9),
			('cat-unused',      'Không dùng', 'khong dung', 'khong-dung', '',         10)
	`); err != nil {
		t.Fatalf("seed search_categories: %v", err)
	}

	insertProd := func(id, shopID, catID string, ancestors []string, status string, deleted bool) {
		t.Helper()
		_, execErr := pool.Exec(ctx, `
			INSERT INTO product_embeddings (
				product_id, shop_id, category_id, category_ancestor_ids,
				name, slug, status, deleted, source_updated_at
			) VALUES ($1, $2, $3, $4, $1, $1, $5, $6, NOW())
		`, id, shopID, catID, ancestors, status, deleted)
		if execErr != nil {
			t.Fatalf("insert product %s: %v", id, execErr)
		}
	}

	// Visible shop-1 rows: leaves + ancestors (with duplicates), OUT_OF_STOCK, and empty category_ancestor_ids.
	insertProd("p-leaf-1", "shop-1", "cat-leaf-b", []string{"cat-leaf-b", "cat-mid", "cat-root"}, "APPROVED", false)
	insertProd("p-leaf-2", "shop-1", "cat-leaf-b", []string{"cat-leaf-b", "cat-mid", "cat-root"}, "APPROVED", false)
	insertProd("p-leaf-a", "shop-1", "cat-leaf-a", []string{"cat-leaf-a", "cat-mid", "cat-root"}, "APPROVED", false)
	insertProd("p-oos", "shop-1", "cat-oos", []string{"cat-oos", "cat-root"}, "OUT_OF_STOCK", false)
	insertProd("p-leaf-only", "shop-1", "cat-leaf-no-anc", []string{}, "APPROVED", false)

	// Excluded rows: deleted, HIDDEN, PENDING, and another shop.
	insertProd("p-deleted", "shop-1", "cat-deleted", []string{"cat-deleted", "cat-unused"}, "APPROVED", true)
	insertProd("p-hidden", "shop-1", "cat-hidden", []string{"cat-hidden", "cat-unused"}, "HIDDEN", false)
	insertProd("p-pending", "shop-1", "cat-pending", []string{"cat-pending"}, "PENDING", false)
	insertProd("p-other", "shop-2", "cat-other-shop", []string{"cat-other-shop", "cat-unused"}, "APPROVED", false)

	cats, err := st.Queries().ListCategoriesByShop(ctx, "shop-1")
	if err != nil {
		t.Fatalf("ListCategoriesByShop(shop-1): %v", err)
	}
	gotIDs := make([]string, len(cats))
	for i, c := range cats {
		gotIDs[i] = c.CategoryID
	}
	wantIDs := []string{"cat-root", "cat-mid", "cat-leaf-a", "cat-leaf-b", "cat-oos", "cat-leaf-no-anc"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("ListCategoriesByShop(shop-1) = %v, want %v", gotIDs, wantIDs)
	}

	emptyCats, err := st.Queries().ListCategoriesByShop(ctx, "shop-empty")
	if err != nil {
		t.Fatalf("ListCategoriesByShop(shop-empty): %v", err)
	}
	if len(emptyCats) != 0 {
		t.Fatalf("ListCategoriesByShop(shop-empty) = %v, want empty", emptyCats)
	}

	sugAO, err := st.Queries().SuggestCategoriesByShop(ctx, db.SuggestCategoriesByShopParams{
		ShopID:  "shop-1",
		Column2: "ao",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("SuggestCategoriesByShop(shop-1, ao): %v", err)
	}
	gotSugAO := make([]string, len(sugAO))
	for i, r := range sugAO {
		gotSugAO[i] = r.CategoryID
	}
	wantSugAO := []string{"cat-mid", "cat-leaf-a", "cat-leaf-b", "cat-oos"}
	if !slices.Equal(gotSugAO, wantSugAO) {
		t.Fatalf("SuggestCategoriesByShop(shop-1, ao) = %v, want %v", gotSugAO, wantSugAO)
	}

	sugExcluded, err := st.Queries().SuggestCategoriesByShop(ctx, db.SuggestCategoriesByShopParams{
		ShopID:  "shop-1",
		Column2: "da",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("SuggestCategoriesByShop(shop-1, da): %v", err)
	}
	if len(sugExcluded) != 0 {
		t.Fatalf("SuggestCategoriesByShop(shop-1, da) leaked deleted/HIDDEN categories: %+v", sugExcluded)
	}

	sugTop, err := st.Queries().SuggestCategoriesByShop(ctx, db.SuggestCategoriesByShopParams{
		ShopID:  "shop-1",
		Column2: "",
		Limit:   3,
	})
	if err != nil {
		t.Fatalf("SuggestCategoriesByShop(shop-1, empty, limit=3): %v", err)
	}
	gotSugTop := make([]string, len(sugTop))
	for i, r := range sugTop {
		gotSugTop[i] = r.CategoryID
	}
	wantSugTop := []string{"cat-root", "cat-mid", "cat-leaf-a"}
	if !slices.Equal(gotSugTop, wantSugTop) {
		t.Fatalf("SuggestCategoriesByShop(shop-1, empty, limit=3) = %v, want %v", gotSugTop, wantSugTop)
	}
}
