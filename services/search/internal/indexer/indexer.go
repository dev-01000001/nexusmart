package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	"github.com/nexusmart/core/services/search/internal/embed"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/normalize"
	"github.com/nexusmart/core/services/search/internal/store"
)

const (
	defaultCategoryTTL    = 5 * time.Minute
	minCategoryRefreshGap = 5 * time.Second
	bulkChunkSize         = 1000
	syncPageSize          = 100
	syncFlushBatchSize    = 1000
)

// SearchEngine abstracts Meilisearch write operations for indexing and unit tests.
type SearchEngine interface {
	UpsertDocuments(ctx context.Context, docs []meili.Document) (int64, error)
	DeleteDocument(ctx context.Context, id string) (int64, error)
	WaitTask(ctx context.Context, taskUID int64) error
}

// Indexer synchronizes product catalog updates into both pgvector (Store) and Meilisearch.
type Indexer struct {
	store    store.Store
	engine   SearchEngine
	embedder embed.Embedder
	fetcher  ProductFetcher
	logger   *slog.Logger

	catMu          sync.RWMutex
	catsByID       map[string]CategoryMeta
	catsBySlug     map[string]CategoryMeta
	catExpiresAt   time.Time
	lastCatRefresh time.Time
	catTTL         time.Duration
}

// New constructs an Indexer.
func New(
	st store.Store,
	engine SearchEngine,
	embedder embed.Embedder,
	fetcher ProductFetcher,
	logger *slog.Logger,
) *Indexer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Indexer{
		store:      st,
		engine:     engine,
		embedder:   embedder,
		fetcher:    fetcher,
		logger:     logger,
		catsByID:   make(map[string]CategoryMeta),
		catsBySlug: make(map[string]CategoryMeta),
		catTTL:     defaultCategoryTTL,
	}
}

// ProcessEvent idempotently processes a product.* NATS JetStream event.
func (idx *Indexer) ProcessEvent(ctx context.Context, subject string, env Envelope) (bool, error) {
	if strings.TrimSpace(env.EventID) == "" {
		return false, fmt.Errorf("%w: missing event_id", ErrMalformedPayload)
	}

	processed, err := idx.store.IsEventProcessed(ctx, env.EventID)
	if err != nil {
		return false, fmt.Errorf("check processed event %s: %w", env.EventID, err)
	}
	if processed {
		return true, nil
	}

	eventType := strings.TrimSpace(subject)
	if eventType == "" {
		eventType = strings.TrimSpace(env.EventType)
	}

	var payload ProductEventPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return false, fmt.Errorf("%w: decode payload: %w", ErrMalformedPayload, err)
	}
	payload.ProductID = strings.TrimSpace(payload.ProductID)
	if payload.ProductID == "" {
		return false, fmt.Errorf("%w: missing product_id", ErrMalformedPayload)
	}

	switch eventType {
	case SubjectProductDeleted:
		if err := idx.deleteProduct(ctx, payload.ProductID); err != nil {
			return false, err
		}

	case SubjectProductCreated, SubjectProductUpdated, SubjectProductStockChanged:
		if err := idx.syncSingleProduct(ctx, payload.ProductID, payload.Version); err != nil {
			return false, err
		}

	default:
		idx.logger.DebugContext(ctx, "ignoring unhandled product event subject",
			slog.String("subject", eventType),
			slog.String("event_id", env.EventID),
		)
	}

	if _, err := idx.store.TryMarkEventProcessed(ctx, env.EventID, eventType); err != nil {
		return false, fmt.Errorf("mark event processed %s: %w", env.EventID, err)
	}
	return false, nil
}

func (idx *Indexer) syncSingleProduct(ctx context.Context, productID string, version int64) error {
	if version > 0 {
		storedVer, exists, err := idx.store.GetDocumentVersion(ctx, productID)
		if err != nil {
			return fmt.Errorf("check stored version for %s: %w", productID, err)
		}
		if exists && version < storedVer {
			idx.logger.DebugContext(ctx, "skipping stale product event",
				slog.String("product_id", productID),
				slog.Int64("incoming_version", version),
				slog.Int64("stored_version", storedVer),
			)
			return nil
		}
	}

	if idx.fetcher == nil {
		return errors.New("product fetcher is not configured")
	}

	prod, err := idx.fetcher.GetProduct(ctx, productID)
	if err != nil {
		if isNotFoundError(err) {
			return idx.deleteProduct(ctx, productID)
		}
		return err
	}

	if !IsIndexableStatus(prod.GetStatus()) {
		return idx.deleteProduct(ctx, productID)
	}

	catMeta := idx.resolveCategory(ctx, prod.GetCategoryId())
	embText := BuildEmbeddingText(prod, catMeta.Name)
	vec, err := idx.embedder.Embed(ctx, embText)
	if err != nil {
		return fmt.Errorf("embed product %s: %w", productID, err)
	}

	pgDoc, meiliDoc := BuildDocuments(prod, catMeta, vec, version)
	applied, err := idx.store.UpsertDocument(ctx, pgDoc)
	if err != nil {
		return fmt.Errorf("upsert store document %s: %w", productID, err)
	}
	if !applied {
		idx.logger.DebugContext(ctx, "skipping meilisearch update due to newer stored version",
			slog.String("product_id", productID),
			slog.Int64("version", version),
		)
		return nil
	}

	if _, err := idx.engine.UpsertDocuments(ctx, []meili.Document{meiliDoc}); err != nil {
		return fmt.Errorf("upsert meilisearch document %s: %w", productID, err)
	}
	return nil
}

func (idx *Indexer) deleteProduct(ctx context.Context, productID string) error {
	if err := idx.store.DeleteDocument(ctx, productID); err != nil {
		return fmt.Errorf("delete store document %s: %w", productID, err)
	}
	if _, err := idx.engine.DeleteDocument(ctx, productID); err != nil {
		return fmt.Errorf("delete meilisearch document %s: %w", productID, err)
	}
	return nil
}

// BulkIndexProducts indexes a slice of products into both pgvector and Meilisearch in batches,
// waiting for all Meilisearch indexing tasks to complete before returning.
func (idx *Indexer) BulkIndexProducts(ctx context.Context, products []*productv1.Product, categories []*productv1.Category) (int, error) {
	if len(categories) > 0 {
		idx.UpdateCategoryCache(categories)
	}

	indexable := make([]*productv1.Product, 0, len(products))
	for _, p := range products {
		if p != nil && IsIndexableStatus(p.GetStatus()) {
			indexable = append(indexable, p)
		}
	}
	if len(indexable) == 0 {
		return 0, nil
	}

	var (
		totalIndexed int
		taskUIDs     []int64
	)

	for start := 0; start < len(indexable); start += bulkChunkSize {
		end := start + bulkChunkSize
		if end > len(indexable) {
			end = len(indexable)
		}
		chunk := indexable[start:end]

		catMetas := make([]CategoryMeta, len(chunk))
		texts := make([]string, len(chunk))
		for i, p := range chunk {
			cm := idx.lookupCachedCategory(p.GetCategoryId())
			catMetas[i] = cm
			texts[i] = BuildEmbeddingText(p, cm.Name)
		}

		vecs, err := idx.embedder.EmbedBatch(ctx, texts)
		if err != nil {
			return totalIndexed, fmt.Errorf("embed bulk batch [%d:%d]: %w", start, end, err)
		}
		if len(vecs) != len(chunk) {
			return totalIndexed, fmt.Errorf("embed batch length mismatch: got %d want %d", len(vecs), len(chunk))
		}

		pgDocs := make([]store.ProductDocument, len(chunk))
		meiliDocs := make([]meili.Document, len(chunk))
		for i, p := range chunk {
			pgDocs[i], meiliDocs[i] = BuildDocuments(p, catMetas[i], vecs[i], 0)
		}

		if err := idx.store.UpsertDocumentsBatch(ctx, pgDocs); err != nil {
			return totalIndexed, fmt.Errorf("store batch upsert [%d:%d]: %w", start, end, err)
		}

		taskUID, err := idx.engine.UpsertDocuments(ctx, meiliDocs)
		if err != nil {
			return totalIndexed, fmt.Errorf("meilisearch batch upsert [%d:%d]: %w", start, end, err)
		}
		if taskUID >= 0 {
			taskUIDs = append(taskUIDs, taskUID)
		}
		totalIndexed += len(chunk)
	}

	for _, uid := range taskUIDs {
		if err := idx.engine.WaitTask(ctx, uid); err != nil {
			return totalIndexed, fmt.Errorf("wait meilisearch bulk task %d: %w", uid, err)
		}
	}
	return totalIndexed, nil
}

// SyncAllFromProduct paginates ProductService.ListProducts and indexes all public products.
func (idx *Indexer) SyncAllFromProduct(ctx context.Context) (int, error) {
	if idx.fetcher == nil {
		return 0, errors.New("product fetcher is not configured")
	}

	cats, err := idx.fetcher.ListCategories(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetch categories for startup sync: %w", err)
	}
	idx.UpdateCategoryCache(cats)

	var (
		page         int32 = 1
		totalIndexed int
		buffer       = make([]*productv1.Product, 0, syncFlushBatchSize)
	)

	for {
		prods, total, err := idx.fetcher.ListProducts(ctx, page, syncPageSize)
		if err != nil {
			return totalIndexed, fmt.Errorf("list products page %d: %w", page, err)
		}
		if len(prods) == 0 {
			break
		}
		buffer = append(buffer, prods...)
		if len(buffer) >= syncFlushBatchSize {
			n, err := idx.BulkIndexProducts(ctx, buffer, nil)
			if err != nil {
				return totalIndexed, err
			}
			totalIndexed += n
			buffer = buffer[:0]
		}
		if int64(page*syncPageSize) >= total || len(prods) < syncPageSize {
			break
		}
		page++
	}

	if len(buffer) > 0 {
		n, err := idx.BulkIndexProducts(ctx, buffer, nil)
		if err != nil {
			return totalIndexed, err
		}
		totalIndexed += n
	}

	idx.logger.InfoContext(ctx, "startup catalog sync completed",
		slog.Int("indexed_products", totalIndexed),
		slog.Int("categories", len(cats)),
	)
	return totalIndexed, nil
}

// UpdateCategoryCache replaces the in-memory category metadata cache.
func (idx *Indexer) UpdateCategoryCache(cats []*productv1.Category) {
	byID := make(map[string]CategoryMeta, len(cats))
	bySlug := make(map[string]CategoryMeta, len(cats))
	for _, c := range cats {
		if c == nil || c.GetId() == "" {
			continue
		}
		meta := CategoryMeta{
			ID:   c.GetId(),
			Slug: c.GetSlug(),
			Name: c.GetName(),
		}
		byID[meta.ID] = meta
		if meta.Slug != "" {
			bySlug[meta.Slug] = meta
		}
	}

	now := time.Now()
	idx.catMu.Lock()
	idx.catsByID = byID
	idx.catsBySlug = bySlug
	idx.catExpiresAt = now.Add(idx.catTTL)
	idx.lastCatRefresh = now
	idx.catMu.Unlock()
}

// CategoryLabels returns a snapshot map of category_slug -> Vietnamese display name.
func (idx *Indexer) CategoryLabels() map[string]string {
	idx.catMu.RLock()
	defer idx.catMu.RUnlock()
	out := make(map[string]string, len(idx.catsBySlug))
	for slug, meta := range idx.catsBySlug {
		if meta.Name != "" {
			out[slug] = meta.Name
		}
	}
	return out
}

func (idx *Indexer) lookupCachedCategory(categoryID string) CategoryMeta {
	idx.catMu.RLock()
	meta, ok := idx.catsByID[categoryID]
	idx.catMu.RUnlock()
	if ok {
		return meta
	}
	return CategoryMeta{ID: categoryID}
}

func (idx *Indexer) resolveCategory(ctx context.Context, categoryID string) CategoryMeta {
	if categoryID == "" {
		return CategoryMeta{}
	}
	now := time.Now()
	idx.catMu.RLock()
	meta, ok := idx.catsByID[categoryID]
	expired := now.After(idx.catExpiresAt)
	canRefresh := now.Sub(idx.lastCatRefresh) >= minCategoryRefreshGap
	idx.catMu.RUnlock()

	if ok && !expired {
		return meta
	}
	if idx.fetcher != nil && (expired || (!ok && canRefresh)) {
		if cats, err := idx.fetcher.ListCategories(ctx); err == nil {
			idx.UpdateCategoryCache(cats)
		} else {
			idx.catMu.Lock()
			idx.lastCatRefresh = time.Now()
			idx.catMu.Unlock()
			idx.logger.WarnContext(ctx, "failed to refresh category cache", slog.String("error", err.Error()))
		}
	}
	return idx.lookupCachedCategory(categoryID)
}

// IsIndexableStatus reports whether a product status should appear in public search.
func IsIndexableStatus(st productv1.ProductStatus) bool {
	return st == productv1.ProductStatus_PRODUCT_STATUS_APPROVED ||
		st == productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK
}

// BuildEmbeddingText constructs the text fed into the Embedder for a product.
func BuildEmbeddingText(p *productv1.Product, categoryName string) string {
	parts := make([]string, 0, 4+len(p.GetSkus()))
	if name := strings.TrimSpace(p.GetName()); name != "" {
		parts = append(parts, name)
	}
	if cn := strings.TrimSpace(categoryName); cn != "" {
		parts = append(parts, cn)
	}
	if desc := strings.TrimSpace(p.GetDescription()); desc != "" {
		parts = append(parts, desc)
	}
	if skuText := BuildSKUText(p.GetSkus()); skuText != "" {
		parts = append(parts, skuText)
	}
	return strings.Join(parts, " ")
}

// BuildSKUText joins SKU codes, names, and sorted attribute values for search indexing.
func BuildSKUText(skus []*productv1.SKU) string {
	if len(skus) == 0 {
		return ""
	}
	parts := make([]string, 0, len(skus)*2)
	for _, sk := range skus {
		if sk == nil {
			continue
		}
		if code := strings.TrimSpace(sk.GetSkuCode()); code != "" {
			parts = append(parts, code)
		}
		if name := strings.TrimSpace(sk.GetName()); name != "" {
			parts = append(parts, name)
		}
		if attrs := sk.GetAttributes(); len(attrs) > 0 {
			keys := make([]string, 0, len(attrs))
			for k := range attrs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if val := strings.TrimSpace(attrs[k]); val != "" {
					parts = append(parts, val)
				}
			}
		}
	}
	return strings.Join(parts, " ")
}

// BuildDocuments converts a Product protobuf into synced pgvector and Meilisearch documents.
func BuildDocuments(
	p *productv1.Product,
	cat CategoryMeta,
	embedding []float32,
	version int64,
) (store.ProductDocument, meili.Document) {
	totalStock, minPrice, maxPrice := computeInventoryAndPrices(p)

	statusStr := statusToString(p.GetStatus())
	inStock := totalStock > 0 && p.GetStatus() == productv1.ProductStatus_PRODUCT_STATUS_APPROVED

	images := make([]string, len(p.GetImages()))
	copy(images, p.GetImages())

	createdAt := time.Now().UTC()
	if ts := p.GetCreatedAt(); ts != nil && ts.IsValid() {
		createdAt = ts.AsTime().UTC()
	}
	updatedAt := createdAt
	if ts := p.GetUpdatedAt(); ts != nil && ts.IsValid() {
		updatedAt = ts.AsTime().UTC()
	}

	skuText := BuildSKUText(p.GetSkus())

	pgDoc := store.ProductDocument{
		ProductID:        p.GetId(),
		ShopID:           p.GetShopId(),
		CategoryID:       p.GetCategoryId(),
		CategorySlug:     cat.Slug,
		CategoryName:     cat.Name,
		Name:             p.GetName(),
		Description:      p.GetDescription(),
		Images:           images,
		Status:           statusStr,
		MinPriceVND:      minPrice,
		MaxPriceVND:      maxPrice,
		TotalStock:       totalStock,
		InStock:          inStock,
		Version:          version,
		Embedding:        embedding,
		ProductCreatedAt: createdAt,
		ProductUpdatedAt: updatedAt,
	}

	meiliDoc := meili.Document{
		ID:                 p.GetId(),
		ShopID:             p.GetShopId(),
		CategoryID:         p.GetCategoryId(),
		CategorySlug:       cat.Slug,
		CategoryName:       cat.Name,
		CategoryNameFolded: normalize.NormalizeVietnamese(cat.Name),
		Name:               p.GetName(),
		NameFolded:         normalize.NormalizeVietnamese(p.GetName()),
		Description:        p.GetDescription(),
		DescriptionFolded:  normalize.NormalizeVietnamese(p.GetDescription()),
		SKUTextFolded:      normalize.NormalizeVietnamese(skuText),
		Images:             images,
		Status:             statusStr,
		MinPriceVND:        minPrice,
		MaxPriceVND:        maxPrice,
		PriceBucket:        meili.PriceBucket(minPrice),
		TotalStock:         totalStock,
		InStock:            inStock,
		CreatedAtUnix:      createdAt.Unix(),
		UpdatedAtUnix:      updatedAt.Unix(),
	}
	return pgDoc, meiliDoc
}

func computeInventoryAndPrices(p *productv1.Product) (int32, int64, int64) {
	minPrice := p.GetMinPriceVnd()
	maxPrice := p.GetMaxPriceVnd()
	var totalStock int32

	skus := p.GetSkus()
	for i, sk := range skus {
		if sk == nil {
			continue
		}
		avail := sk.GetAvailableStock()
		if avail == 0 && sk.GetStock() > 0 && sk.GetReservedStock() == 0 && sk.GetFlashSaleReservedStock() == 0 {
			avail = sk.GetStock()
		}
		if avail > 0 {
			totalStock += avail
		}
		if price := sk.GetPriceVnd(); price > 0 {
			if minPrice == 0 || (i == 0 && minPrice == 0) || price < minPrice {
				minPrice = price
			}
			if price > maxPrice {
				maxPrice = price
			}
		}
	}
	if maxPrice < minPrice {
		maxPrice = minPrice
	}
	return totalStock, minPrice, maxPrice
}

func statusToString(st productv1.ProductStatus) string {
	switch st {
	case productv1.ProductStatus_PRODUCT_STATUS_APPROVED:
		return "APPROVED"
	case productv1.ProductStatus_PRODUCT_STATUS_OUT_OF_STOCK:
		return "OUT_OF_STOCK"
	case productv1.ProductStatus_PRODUCT_STATUS_PENDING:
		return "PENDING"
	case productv1.ProductStatus_PRODUCT_STATUS_REJECTED:
		return "REJECTED"
	case productv1.ProductStatus_PRODUCT_STATUS_HIDDEN:
		return "HIDDEN"
	default:
		return "UNSPECIFIED"
	}
}

func isNotFoundError(err error) bool {
	if errors.Is(err, ErrProductNotFound) {
		return true
	}
	if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
		return true
	}
	return false
}
