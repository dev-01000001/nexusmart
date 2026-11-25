package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexusmart/core/services/search/migrations"
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
