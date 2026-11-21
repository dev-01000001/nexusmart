package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	upsertBatchChunkSize = 500
	defaultVectorLimit   = 20
	maxVectorLimit       = 500
	upsertDocumentSQL    = `
INSERT INTO product_documents (
	product_id, shop_id, category_id, category_slug, category_name,
	name, description, images, status, min_price_vnd, max_price_vnd,
	total_stock, in_stock, version, embedding,
	product_created_at, product_updated_at, indexed_at
) VALUES (
	$1, $2, $3, $4, $5,
	$6, $7, $8::jsonb, $9, $10, $11,
	$12, $13, $14, $15::vector,
	$16, $17, NOW()
)
ON CONFLICT (product_id) DO UPDATE SET
	shop_id = EXCLUDED.shop_id,
	category_id = EXCLUDED.category_id,
	category_slug = EXCLUDED.category_slug,
	category_name = EXCLUDED.category_name,
	name = EXCLUDED.name,
	description = EXCLUDED.description,
	images = EXCLUDED.images,
	status = EXCLUDED.status,
	min_price_vnd = EXCLUDED.min_price_vnd,
	max_price_vnd = EXCLUDED.max_price_vnd,
	total_stock = EXCLUDED.total_stock,
	in_stock = EXCLUDED.in_stock,
	version = GREATEST(product_documents.version, EXCLUDED.version),
	embedding = EXCLUDED.embedding,
	product_created_at = EXCLUDED.product_created_at,
	product_updated_at = EXCLUDED.product_updated_at,
	indexed_at = NOW()
WHERE EXCLUDED.version = 0 OR EXCLUDED.version >= product_documents.version
`
)

// PgStore implements Store backed by PostgreSQL + pgvector via pgx/v5.
type PgStore struct {
	pool *pgxpool.Pool

	iterOnce         sync.Once
	hasIterativeScan bool
}

// NewPgStore creates a PgStore using the provided pgxpool.Pool.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

func (s *PgStore) detectIterativeScan(ctx context.Context) bool {
	s.iterOnce.Do(func() {
		var exists bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_settings WHERE name = 'hnsw.iterative_scan')`).Scan(&exists)
		if err == nil && exists {
			s.hasIterativeScan = true
		}
	})
	return s.hasIterativeScan
}

// UpsertDocument inserts or updates a product document with optimistic version protection.
func (s *PgStore) UpsertDocument(ctx context.Context, doc ProductDocument) (bool, error) {
	imagesJSON, createdAt, updatedAt, err := prepareDocFields(doc)
	if err != nil {
		return false, err
	}
	tag, err := s.pool.Exec(ctx, upsertDocumentSQL,
		doc.ProductID, doc.ShopID, doc.CategoryID, doc.CategorySlug, doc.CategoryName,
		doc.Name, doc.Description, string(imagesJSON), doc.Status, doc.MinPriceVND, doc.MaxPriceVND,
		doc.TotalStock, doc.InStock, doc.Version, FormatVector(doc.Embedding),
		createdAt, updatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("upsert product_document %s: %w", doc.ProductID, err)
	}
	return tag.RowsAffected() > 0, nil
}

// UpsertDocumentsBatch efficiently upserts a slice of documents in pgx batches.
func (s *PgStore) UpsertDocumentsBatch(ctx context.Context, docs []ProductDocument) error {
	if len(docs) == 0 {
		return nil
	}
	for start := 0; start < len(docs); start += upsertBatchChunkSize {
		end := start + upsertBatchChunkSize
		if end > len(docs) {
			end = len(docs)
		}
		chunk := docs[start:end]
		batch := &pgx.Batch{}
		for _, doc := range chunk {
			imagesJSON, createdAt, updatedAt, err := prepareDocFields(doc)
			if err != nil {
				return err
			}
			batch.Queue(upsertDocumentSQL,
				doc.ProductID, doc.ShopID, doc.CategoryID, doc.CategorySlug, doc.CategoryName,
				doc.Name, doc.Description, string(imagesJSON), doc.Status, doc.MinPriceVND, doc.MaxPriceVND,
				doc.TotalStock, doc.InStock, doc.Version, FormatVector(doc.Embedding),
				createdAt, updatedAt,
			)
		}

		br := s.pool.SendBatch(ctx, batch)
		for i := range chunk {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return fmt.Errorf("batch upsert product_document %s: %w", chunk[i].ProductID, err)
			}
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("close upsert batch: %w", err)
		}
	}
	return nil
}

// DeleteDocument removes a product document by product ID.
func (s *PgStore) DeleteDocument(ctx context.Context, productID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM product_documents WHERE product_id = $1`, productID); err != nil {
		return fmt.Errorf("delete product_document %s: %w", productID, err)
	}
	return nil
}

// GetDocument fetches a product document including its stored vector embedding.
func (s *PgStore) GetDocument(ctx context.Context, productID string) (*ProductDocument, error) {
	var (
		doc        ProductDocument
		imagesRaw  []byte
		embTextRaw string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT product_id, shop_id, category_id, category_slug, category_name,
		       name, description, images, status, min_price_vnd, max_price_vnd,
		       total_stock, in_stock, version, embedding::text,
		       product_created_at, product_updated_at, indexed_at
		FROM product_documents
		WHERE product_id = $1
	`, productID).Scan(
		&doc.ProductID, &doc.ShopID, &doc.CategoryID, &doc.CategorySlug, &doc.CategoryName,
		&doc.Name, &doc.Description, &imagesRaw, &doc.Status, &doc.MinPriceVND, &doc.MaxPriceVND,
		&doc.TotalStock, &doc.InStock, &doc.Version, &embTextRaw,
		&doc.ProductCreatedAt, &doc.ProductUpdatedAt, &doc.IndexedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get product_document %s: %w", productID, err)
	}
	if len(imagesRaw) > 0 {
		_ = json.Unmarshal(imagesRaw, &doc.Images)
	}
	if doc.Images == nil {
		doc.Images = []string{}
	}
	vec, err := ParseVector(embTextRaw)
	if err != nil {
		return nil, fmt.Errorf("parse embedding for %s: %w", productID, err)
	}
	doc.Embedding = vec
	return &doc, nil
}

// GetDocumentVersion returns the stored version for a product ID if present.
func (s *PgStore) GetDocumentVersion(ctx context.Context, productID string) (int64, bool, error) {
	var version int64
	err := s.pool.QueryRow(ctx, `SELECT version FROM product_documents WHERE product_id = $1`, productID).Scan(&version)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("get document version %s: %w", productID, err)
	}
	return version, true, nil
}

// VectorSearch performs HNSW cosine similarity search in pgvector with optional structured filters.
func (s *PgStore) VectorSearch(ctx context.Context, queryEmbedding []float32, filter VectorSearchFilter) ([]VectorHit, error) {
	if len(queryEmbedding) == 0 {
		return []VectorHit{}, nil
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultVectorLimit
	}
	if limit > maxVectorLimit {
		limit = maxVectorLimit
	}

	useIterative := filter.HasFilter() && s.detectIterativeScan(ctx)

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin vector search tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL hnsw.ef_search = 64`); err != nil {
		return nil, fmt.Errorf("set hnsw.ef_search: %w", err)
	}
	if useIterative {
		// pgvector >= 0.8 iterative index scan improves recall under selective filters.
		_, _ = tx.Exec(ctx, `SET LOCAL hnsw.iterative_scan = relaxed_order`)
	}

	args := make([]any, 0, 8)
	args = append(args, FormatVector(queryEmbedding))

	var whereClauses []string
	if shopID := strings.TrimSpace(filter.ShopID); shopID != "" {
		args = append(args, shopID)
		whereClauses = append(whereClauses, fmt.Sprintf("shop_id = $%d", len(args)))
	}
	if cat := strings.TrimSpace(filter.Category); cat != "" {
		args = append(args, cat)
		whereClauses = append(whereClauses, fmt.Sprintf("(category_slug = $%d OR category_id = $%d)", len(args), len(args)))
	}
	if filter.MinPriceVND > 0 {
		args = append(args, filter.MinPriceVND)
		whereClauses = append(whereClauses, fmt.Sprintf("min_price_vnd >= $%d", len(args)))
	}
	if filter.MaxPriceVND > 0 {
		args = append(args, filter.MaxPriceVND)
		whereClauses = append(whereClauses, fmt.Sprintf("min_price_vnd <= $%d", len(args)))
	}
	if filter.InStockOnly {
		whereClauses = append(whereClauses, "in_stock = TRUE")
	}
	if excludeID := strings.TrimSpace(filter.ExcludeProductID); excludeID != "" {
		args = append(args, excludeID)
		whereClauses = append(whereClauses, fmt.Sprintf("product_id <> $%d", len(args)))
	}

	args = append(args, limit)
	limitPlaceholder := fmt.Sprintf("$%d", len(args))

	var query strings.Builder
	query.WriteString(`
		SELECT product_id, shop_id, category_id, category_slug, category_name,
		       name, description, images, status, min_price_vnd, max_price_vnd,
		       total_stock, in_stock, version,
		       product_created_at, product_updated_at, indexed_at,
		       (1.0 - (embedding <=> $1::vector))::double precision AS score
		FROM product_documents
	`)
	if len(whereClauses) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(whereClauses, " AND "))
	}
	query.WriteString(" ORDER BY embedding <=> $1::vector ASC LIMIT ")
	query.WriteString(limitPlaceholder)

	rows, err := tx.Query(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("vector search query: %w", err)
	}
	defer rows.Close()

	hits := make([]VectorHit, 0, limit)
	for rows.Next() {
		var (
			doc       ProductDocument
			imagesRaw []byte
			score     float64
		)
		if err := rows.Scan(
			&doc.ProductID, &doc.ShopID, &doc.CategoryID, &doc.CategorySlug, &doc.CategoryName,
			&doc.Name, &doc.Description, &imagesRaw, &doc.Status, &doc.MinPriceVND, &doc.MaxPriceVND,
			&doc.TotalStock, &doc.InStock, &doc.Version,
			&doc.ProductCreatedAt, &doc.ProductUpdatedAt, &doc.IndexedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scan vector search row: %w", err)
		}
		if len(imagesRaw) > 0 {
			_ = json.Unmarshal(imagesRaw, &doc.Images)
		}
		if doc.Images == nil {
			doc.Images = []string{}
		}
		if math.IsNaN(score) || math.IsInf(score, 0) {
			score = 0
		}
		hits = append(hits, VectorHit{Doc: doc, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vector search rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit vector search tx: %w", err)
	}
	return hits, nil
}

// CountDocuments returns total indexed product documents.
func (s *PgStore) CountDocuments(ctx context.Context) (int64, error) {
	var cnt int64
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*)::bigint FROM product_documents`).Scan(&cnt); err != nil {
		return 0, fmt.Errorf("count product_documents: %w", err)
	}
	return cnt, nil
}

// IsEventProcessed reports whether eventID already exists in processed_events.
func (s *PgStore) IsEventProcessed(ctx context.Context, eventID string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1)`, eventID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check processed_events %s: %w", eventID, err)
	}
	return exists, nil
}

// TryMarkEventProcessed atomically inserts eventID into processed_events and returns true if newly recorded.
func (s *PgStore) TryMarkEventProcessed(ctx context.Context, eventID, subject string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO processed_events (event_id, subject)
		VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING
	`, eventID, subject)
	if err != nil {
		return false, fmt.Errorf("mark processed_events %s: %w", eventID, err)
	}
	return tag.RowsAffected() > 0, nil
}

// InsertDeadLetter persists a dead-lettered event payload and error details in Postgres.
func (s *PgStore) InsertDeadLetter(ctx context.Context, eventID, subject string, payload []byte, errMsg string, attempts int) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO dead_letter_events (id, event_id, subject, payload, error_message, attempts)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), eventID, subject, payload, errMsg, attempts); err != nil {
		return fmt.Errorf("insert dead_letter_events: %w", err)
	}
	return nil
}

// Ping verifies database connectivity.
func (s *PgStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func prepareDocFields(doc ProductDocument) ([]byte, time.Time, time.Time, error) {
	images := doc.Images
	if images == nil {
		images = []string{}
	}
	imagesJSON, err := json.Marshal(images)
	if err != nil {
		return nil, time.Time{}, time.Time{}, fmt.Errorf("marshal images for %s: %w", doc.ProductID, err)
	}
	now := time.Now().UTC()
	createdAt := doc.ProductCreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}
	updatedAt := doc.ProductUpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	return imagesJSON, createdAt, updatedAt, nil
}
