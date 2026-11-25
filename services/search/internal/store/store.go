package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/pkg/common/textnorm"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

const (
	reindexAdvisoryLockName = "nexusmart_search_reindex"
	shopStatusLockPrefix    = "search_shop_status:"
	defaultUnlockTimeout    = 5 * time.Second
)

// VectorSearchParams filters pgvector HNSW cosine similarity queries (S6/S8/S14).
type VectorSearchParams struct {
	Embedding           []float32
	CategoryID          string
	ShopID              string
	MinPriceVND         int64
	MaxPriceVND         int64
	MinRatingX100       int32
	InStockOnly         bool
	OnlyApprovedInStock bool
	SizeFolded          string
	ColorFolded         string
	MinSimilarity       float64
	Limit               int32
}

// VectorHit represents a public product candidate returned by pgvector HNSW or DB fallback.
type VectorHit struct {
	ProductID       string
	ShopID          string
	ShopName        string
	ShopSlug        string
	CategoryID      string
	CategorySlug    string
	CategoryName    string
	Name            string
	Slug            string
	Description     string
	ThumbnailURL    string
	MinPriceVND     int64
	MaxPriceVND     int64
	Status          string
	RatingAvgX100   int32
	ReviewCount     int32
	InStock         bool
	SKUCount        int32
	SKUIDs          []string
	DefaultSKUID    string
	Colors          []string
	Sizes           []string
	SourceUpdatedAt time.Time
	Similarity      float64
}

// Store wraps pgxpool and sqlc Queries for search_db (pgvector).
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// OpenPool runs embedded migrations (creating the pgvector extension under pg_advisory_xact_lock)
// and returns a pgxpool configured with pgvector type codecs.
func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	initPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect bootstrap postgres pool: %w", err)
	}
	if err := db.ApplyMigrations(ctx, initPool); err != nil {
		initPool.Close()
		return nil, fmt.Errorf("apply search migrations: %w", err)
	}
	initPool.Close()

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 25
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.AfterConnect = func(cctx context.Context, conn *pgx.Conn) error {
		return pgxvec.RegisterTypes(cctx, conn)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect search pgvector pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping search postgres: %w", err)
	}
	return pool, nil
}

// New constructs a Store over an existing pgvector-enabled pgxpool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:    pool,
		queries: db.New(pool),
	}
}

// Queries returns the underlying sqlc Queries bound to the pool.
func (s *Store) Queries() *db.Queries { return s.queries }

// Pool returns the underlying pgxpool.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Ping checks PostgreSQL connectivity for /readyz.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// WithTx executes fn inside a PostgreSQL transaction using transaction-bound sqlc Queries.
func (s *Store) WithTx(ctx context.Context, fn func(q *db.Queries, tx pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(db.New(tx), tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// LockShopStatus serializes concurrent shop status transitions per shop_id inside tx.
func (s *Store) LockShopStatus(ctx context.Context, tx pgx.Tx, shopID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, shopStatusLockPrefix+shopID)
	return err
}

// AcquireReindexLock attempts a non-blocking session-level Postgres advisory lock to ensure
// strictly one ReindexAll job runs across all replicas (G11/S10).
func (s *Store) AcquireReindexLock(ctx context.Context) (func(), bool, error) {
	return s.acquireReindexLockWithUnlockTimeout(ctx, defaultUnlockTimeout)
}

func (s *Store) acquireReindexLockWithUnlockTimeout(ctx context.Context, unlockTimeout time.Duration) (func(), bool, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire conn for reindex lock: %w", err)
	}

	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, reindexAdvisoryLockName).Scan(&locked); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("try reindex advisory lock: %w", err)
	}
	if !locked {
		conn.Release()
		return nil, false, nil
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
			defer cancel()
			var unlocked bool
			unlockErr := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, reindexAdvisoryLockName).Scan(&unlocked)
			if unlockErr != nil || !unlocked {
				// S10: Never return a connection that may still hold a session advisory lock to the pool.
				// Hijack and close the underlying connection so Postgres releases the session lock cleanly.
				rawConn := conn.Hijack()
				closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer closeCancel()
				_ = rawConn.Close(closeCtx)
				return
			}
			conn.Release()
		})
	}
	return release, true, nil
}

// InsertDeadLetterEvent persists a dead-lettered NATS event to dead_letter_events (E1).
func (s *Store) InsertDeadLetterEvent(ctx context.Context, arg db.InsertDeadLetterEventParams) error {
	return s.queries.InsertDeadLetterEvent(ctx, arg)
}

// SaveSearchHistory upserts a user search history item and trims history to maxItems (<= 50, G14).
func (s *Store) SaveSearchHistory(ctx context.Context, userID, query, queryFolded string, maxItems int32) error {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(queryFolded) == "" {
		return nil
	}
	if maxItems <= 0 || maxItems > 50 {
		maxItems = 50
	}
	return s.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if err := q.UpsertSearchHistory(ctx, db.UpsertSearchHistoryParams{
			ID:          uuid.NewString(),
			UserID:      userID,
			Query:       query,
			QueryFolded: queryFolded,
		}); err != nil {
			return err
		}
		return q.TrimSearchHistory(ctx, db.TrimSearchHistoryParams{
			UserID: userID,
			Limit:  maxItems,
		})
	})
}

// SearchVector runs cosine-distance HNSW search inside a read-only transaction with
// SET LOCAL hnsw.ef_search = 200 and hnsw.iterative_scan = 'relaxed_order' (S6/S8/S14),
// enforcing public status, non-deleted, non-suspended shop visibility, and min similarity.
func (s *Store) SearchVector(ctx context.Context, p VectorSearchParams) ([]VectorHit, error) {
	if len(p.Embedding) == 0 {
		return nil, nil
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin vector search tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// S8: Expand HNSW search beam and enable pgvector >= 0.8 iterative scan so filtered queries
	// (narrow category/shop/price) do not starve behind top-40 unfiltered neighbors.
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.ef_search = 200`); err != nil {
		return nil, fmt.Errorf("set hnsw.ef_search: %w", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = 'relaxed_order'`); err != nil {
		return nil, fmt.Errorf("set hnsw.iterative_scan: %w", err)
	}

	const q = `
SELECT
    pe.product_id, pe.shop_id, COALESCE(s.name, ''), COALESCE(s.slug, ''),
    pe.category_id, COALESCE(c.slug, ''), COALESCE(c.name, ''),
    pe.name, pe.slug, pe.description, pe.thumbnail_url,
    pe.min_price_vnd, pe.max_price_vnd, pe.status,
    pe.rating_avg_x100, pe.review_count, pe.in_stock, pe.sku_count,
    pe.sku_ids, pe.default_sku_id, pe.colors, pe.sizes,
    pe.source_updated_at,
    (1.0 - (pe.embedding <=> $1::vector))::double precision AS similarity
FROM product_embeddings pe
LEFT JOIN search_shops s ON s.shop_id = pe.shop_id
LEFT JOIN search_categories c ON c.category_id = pe.category_id
WHERE pe.deleted = FALSE
  AND pe.embedding IS NOT NULL
  AND COALESCE(s.status, 'ACTIVE') <> 'SUSPENDED'
  AND (
      CASE WHEN $2::bool THEN pe.status = 'APPROVED' AND pe.in_stock = TRUE
           ELSE pe.status IN ('APPROVED', 'OUT_OF_STOCK')
      END
  )
  AND ($3::text = '' OR pe.category_ancestor_ids @> ARRAY[$3::text] OR pe.category_id = $3::text)
  AND ($4::text = '' OR pe.shop_id = $4::text)
  AND ($5::bigint <= 0 OR pe.max_price_vnd >= $5::bigint)
  AND ($6::bigint <= 0 OR pe.min_price_vnd <= $6::bigint)
  AND ($7::int <= 0 OR pe.rating_avg_x100 >= $7::int)
  AND ($8::bool = FALSE OR pe.in_stock = TRUE)
  AND ($9::text = '' OR pe.sizes_folded @> ARRAY[$9::text])
  AND ($10::text = '' OR pe.colors_folded @> ARRAY[$10::text] OR pe.color_codes @> ARRAY[$10::text])
  AND ($11::double precision <= 0 OR (pe.embedding <=> $1::vector) <= (1.0 - $11::double precision))
ORDER BY pe.embedding <=> $1::vector ASC, pe.product_id ASC
LIMIT $12`

	vec := pgvector.NewVector(p.Embedding)
	rows, err := tx.Query(ctx, q,
		vec,
		p.OnlyApprovedInStock,
		p.CategoryID,
		p.ShopID,
		p.MinPriceVND,
		p.MaxPriceVND,
		p.MinRatingX100,
		p.InStockOnly,
		p.SizeFolded,
		p.ColorFolded,
		p.MinSimilarity,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("vector search query: %w", err)
	}
	defer rows.Close()

	var hits []VectorHit
	for rows.Next() {
		var h VectorHit
		if scanErr := rows.Scan(
			&h.ProductID, &h.ShopID, &h.ShopName, &h.ShopSlug,
			&h.CategoryID, &h.CategorySlug, &h.CategoryName,
			&h.Name, &h.Slug, &h.Description, &h.ThumbnailURL,
			&h.MinPriceVND, &h.MaxPriceVND, &h.Status,
			&h.RatingAvgX100, &h.ReviewCount, &h.InStock, &h.SKUCount,
			&h.SKUIDs, &h.DefaultSKUID, &h.Colors, &h.Sizes,
			&h.SourceUpdatedAt, &h.Similarity,
		); scanErr != nil {
			return nil, fmt.Errorf("scan vector hit: %w", scanErr)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector hits: %w", err)
	}
	return hits, nil
}

// SuggestFallbackProducts queries public non-suspended products from Postgres when Meilisearch is unavailable,
// matching folded unaccented Vietnamese text via pe.name_folded (S12).
func (s *Store) SuggestFallbackProducts(ctx context.Context, queryFolded string, limit int32) ([]VectorHit, error) {
	if limit <= 0 {
		limit = 5
	}
	escaped := textnorm.EscapeLikePattern(queryFolded)

	const q = `
SELECT
    pe.product_id, pe.shop_id, COALESCE(s.name, ''), COALESCE(s.slug, ''),
    pe.category_id, COALESCE(c.slug, ''), COALESCE(c.name, ''),
    pe.name, pe.slug, pe.description, pe.thumbnail_url,
    pe.min_price_vnd, pe.max_price_vnd, pe.status,
    pe.rating_avg_x100, pe.review_count, pe.in_stock, pe.sku_count,
    pe.sku_ids, pe.default_sku_id, pe.colors, pe.sizes,
    pe.source_updated_at
FROM product_embeddings pe
LEFT JOIN search_shops s ON s.shop_id = pe.shop_id
LEFT JOIN search_categories c ON c.category_id = pe.category_id
WHERE pe.deleted = FALSE
  AND pe.status IN ('APPROVED', 'OUT_OF_STOCK')
  AND COALESCE(s.status, 'ACTIVE') <> 'SUSPENDED'
  AND ($1::text = '' OR pe.name_folded LIKE '%' || $1::text || '%' ESCAPE '\' OR pe.slug LIKE '%' || $1::text || '%' ESCAPE '\')
ORDER BY
    CASE WHEN $1::text <> '' AND pe.name_folded LIKE $1::text || '%' ESCAPE '\' THEN 0 ELSE 1 END,
    pe.rating_avg_x100 DESC,
    pe.updated_at DESC
LIMIT $2`

	rows, err := s.pool.Query(ctx, q, escaped, limit)
	if err != nil {
		return nil, fmt.Errorf("suggest fallback products: %w", err)
	}
	defer rows.Close()

	var hits []VectorHit
	for rows.Next() {
		var h VectorHit
		if scanErr := rows.Scan(
			&h.ProductID, &h.ShopID, &h.ShopName, &h.ShopSlug,
			&h.CategoryID, &h.CategorySlug, &h.CategoryName,
			&h.Name, &h.Slug, &h.Description, &h.ThumbnailURL,
			&h.MinPriceVND, &h.MaxPriceVND, &h.Status,
			&h.RatingAvgX100, &h.ReviewCount, &h.InStock, &h.SKUCount,
			&h.SKUIDs, &h.DefaultSKUID, &h.Colors, &h.Sizes,
			&h.SourceUpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan fallback suggest product: %w", scanErr)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fallback suggest products: %w", err)
	}
	return hits, nil
}
