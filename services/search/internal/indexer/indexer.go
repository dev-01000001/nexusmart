package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	productv1 "github.com/nexusmart/core/gen/go/nexusmart/product/v1"
	userv1 "github.com/nexusmart/core/gen/go/nexusmart/user/v1"
	"github.com/nexusmart/core/pkg/common/auth"
	"github.com/nexusmart/core/pkg/common/textnorm"
	"github.com/nexusmart/core/services/search/internal/db"
	"github.com/nexusmart/core/services/search/internal/embedder"
	"github.com/nexusmart/core/services/search/internal/meili"
	"github.com/nexusmart/core/services/search/internal/store"
	"github.com/pgvector/pgvector-go"
	"google.golang.org/grpc"
)

const (
	defaultReindexBatchSize = 200
	maxReindexBatchSize     = 500
	sweepBatchSize          = 200
	maxSnippetRunes         = 240
	shopSyncPageSize        = 100
)

var maxShopSyncPages int32 = 10000

var (
	// ErrReindexAlreadyRunning is returned when another ReindexAll job holds the Postgres advisory lock (G11).
	ErrReindexAlreadyRunning = errors.New("REINDEX_ALREADY_RUNNING")
	// ErrPermanentPayload indicates a malformed/invalid event payload that should be routed directly to DLQ.
	ErrPermanentPayload = errors.New("permanent payload validation error")
)

// ProductIndexClient is the minimal subset of ProductServiceClient needed for ReindexAll.
type ProductIndexClient interface {
	ListCategories(ctx context.Context, in *productv1.ListCategoriesRequest, opts ...grpc.CallOption) (*productv1.ListCategoriesResponse, error)
	ListProductsForIndex(ctx context.Context, in *productv1.ListProductsForIndexRequest, opts ...grpc.CallOption) (*productv1.ListProductsForIndexResponse, error)
}

// UserIndexClient is the minimal subset of UserServiceClient needed for shop sync & ReindexAll.
type UserIndexClient interface {
	GetShop(ctx context.Context, in *userv1.GetShopRequest, opts ...grpc.CallOption) (*userv1.GetShopResponse, error)
	ListPublicShops(ctx context.Context, in *userv1.ListPublicShopsRequest, opts ...grpc.CallOption) (*userv1.ListPublicShopsResponse, error)
}

// ReindexOptions configures an asynchronous ReindexAll run.
type ReindexOptions struct {
	ReindexProducts   bool
	ReindexEmbeddings bool
	ReindexCategories bool
	ReindexShops      bool
	BatchSize         int32
}

// ProductUpsertInput represents a normalized public product ready to index.
type ProductUpsertInput struct {
	ProductID           string
	ShopID              string
	CategoryID          string
	CategoryAncestorIDs []string
	Name                string
	Slug                string
	Description         string
	ThumbnailURL        string
	MinPriceVND         int64
	MaxPriceVND         int64
	Status              string
	SKUCodes            []string
	SKUAttributesText   string
	TotalStock          int32
	SKUCount            int32
	SKUIDs              []string
	DefaultSKUID        string
	Colors              []string
	ColorCodes          []string
	Sizes               []string
	RatingAvgX100       int32
	ReviewCount         int32
	ShopSuspended       bool
	UpdatedAt           time.Time
}

// CategoryUpdateInput represents a category upsert/delete event.
type CategoryUpdateInput struct {
	CategoryID string
	Name       string
	Slug       string
	ParentID   string
	SortOrder  int32
	Action     string
	UpdatedAt  time.Time
}

// ShopReviewedInput represents a user.shop_reviewed event.
type ShopReviewedInput struct {
	ShopID      string
	OwnerUserID string
	ShopName    string
	Decision    string
	Reason      string
}

// ShopStatusChangedInput represents a user.shop_status_changed event (F12/G10).
type ShopStatusChangedInput struct {
	ShopID     string
	ShopName   string
	FromStatus string
	ToStatus   string
	Version    int64
	OccurredAt time.Time
}

// Indexer synchronizes Postgres (product_embeddings, search_categories, search_shops) and Meilisearch
// across live NATS events and asynchronous background ReindexAll jobs (G9–G12, S1–S4, S12, S14).
type Indexer struct {
	store         *store.Store
	meili         meili.Engine
	embedder      embedder.Embedder
	productClient ProductIndexClient
	userClient    UserIndexClient
	svcAuth       *auth.Manager
	logger        *slog.Logger

	baseCtx    context.Context
	baseCancel context.CancelFunc
	wg         sync.WaitGroup
}

// New constructs an Indexer. Call Stop() during graceful shutdown.
func New(
	baseCtx context.Context,
	st *store.Store,
	meiliEngine meili.Engine,
	emb embedder.Embedder,
	productClient ProductIndexClient,
	userClient UserIndexClient,
	svcAuth *auth.Manager,
	logger *slog.Logger,
) *Indexer {
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(baseCtx)
	return &Indexer{
		store:         st,
		meili:         meiliEngine,
		embedder:      emb,
		productClient: productClient,
		userClient:    userClient,
		svcAuth:       svcAuth,
		logger:        logger,
		baseCtx:       ctx,
		baseCancel:    cancel,
	}
}

// Stop cancels any background reindex jobs and waits for them to exit cleanly.
func (idx *Indexer) Stop() {
	idx.baseCancel()
	idx.wg.Wait()
}

// IsPublicProductStatus reports whether status is visible on public catalog search.
func IsPublicProductStatus(status string) bool {
	s := NormalizeProductStatus(status)
	return s == "APPROVED" || s == "OUT_OF_STOCK"
}

// NormalizeProductStatus converts raw or proto enum status names to canonical uppercase strings.
func NormalizeProductStatus(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "PRODUCT_STATUS_")
	return s
}

// NormalizeShopStatus converts raw or proto enum shop status names to canonical uppercase strings.
func NormalizeShopStatus(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "SHOP_STATUS_")
	return s
}

// ComputeTextHash computes a deterministic SHA-256 fingerprint of the semantic text fields
// so unchanged products skip redundant embedding computation during ReindexAll (G11/S3).
func ComputeTextHash(in ProductUpsertInput) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(in.Name)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(in.Description)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(strings.TrimSpace(in.SKUAttributesText)))
	_, _ = h.Write([]byte{0})
	codes := append([]string(nil), in.SKUCodes...)
	sort.Strings(codes)
	_, _ = h.Write([]byte(strings.Join(codes, ",")))
	return hex.EncodeToString(h.Sum(nil))
}

// BuildEmbedText assembles the text passed to the embedder for semantic vector representation.
func BuildEmbedText(in ProductUpsertInput) string {
	parts := make([]string, 0, 4)
	if s := strings.TrimSpace(in.Name); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(in.Description); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(in.SKUAttributesText); s != "" {
		parts = append(parts, s)
	}
	if len(in.Colors) > 0 {
		parts = append(parts, strings.Join(in.Colors, " "))
	}
	return strings.Join(parts, " ")
}

// TruncateSnippet trims description text to maxSnippetRunes without splitting UTF-8 characters.
func TruncateSnippet(desc string) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(desc)), " ")
	if utf8.RuneCountInString(s) <= maxSnippetRunes {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxSnippetRunes-3])) + "..."
}

func foldSlice(vals []string) []string {
	seen := make(map[string]struct{}, len(vals))
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		f := textnorm.NormalizeSearchText(v)
		if f == "" {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

func cleanSlice(vals []string) []string {
	seen := make(map[string]struct{}, len(vals))
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		c := strings.TrimSpace(v)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

// ComputeCategoryAncestors returns [categoryID, parent, grandparent, ...] with cycle protection.
func ComputeCategoryAncestors(categoryID string, cats map[string]db.SearchCategory) []string {
	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		return []string{}
	}
	out := make([]string, 0, 4)
	visited := make(map[string]struct{}, 4)
	cur := categoryID
	for cur != "" {
		if _, seen := visited[cur]; seen {
			break
		}
		visited[cur] = struct{}{}
		out = append(out, cur)
		c, ok := cats[cur]
		if !ok {
			break
		}
		cur = strings.TrimSpace(c.ParentID)
	}
	return out
}

// HandleProductUpserted processes a live product.upserted NATS event with idempotency,
// source_updated_at out-of-order protection, and synchronous Meilisearch sync inside the DB TX (G9/G11/S1/S3/S12/S14).
// If Meilisearch fails, the transaction rolls back (including processed_events) so NATS redelivers.
func (idx *Indexer) HandleProductUpserted(ctx context.Context, eventID string, in ProductUpsertInput) error {
	productID := strings.TrimSpace(in.ProductID)
	if productID == "" {
		return fmt.Errorf("%w: missing product_id in product.upserted", ErrPermanentPayload)
	}
	if in.MinPriceVND < 0 || in.MaxPriceVND < 0 {
		return fmt.Errorf("%w: negative price in product.upserted", ErrPermanentPayload)
	}
	in.ProductID = productID
	in.ShopID = strings.TrimSpace(in.ShopID)
	in.CategoryID = strings.TrimSpace(in.CategoryID)
	in.Status = NormalizeProductStatus(in.Status)
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = time.Now().UTC()
	}
	if in.RatingAvgX100 < 0 {
		in.RatingAvgX100 = 0
	}
	if in.RatingAvgX100 > 500 {
		in.RatingAvgX100 = 500
	}
	if in.ReviewCount < 0 {
		in.ReviewCount = 0
	}

	// Never index non-public products; treat non-public status as tombstone/removal.
	if !IsPublicProductStatus(in.Status) {
		return idx.HandleProductDeleted(ctx, eventID, productID, in.ShopID, in.UpdatedAt)
	}

	textHash := ComputeTextHash(in)

	// Compute embedding outside DB TX so slow external calls never hold DB row locks.
	// S3: If embedder fails, embVec stays nil and SQL keeps existing (text_hash, embedding) unchanged
	// so a future ReindexAll / event automatically recomputes the stale embedding.
	var embVec *pgvector.Vector
	needEmbed := idx.embedder != nil
	if existing, err := idx.store.Queries().GetProductEmbeddingState(ctx, productID); err == nil {
		if existing.TextHash == textHash && existing.HasEmbedding {
			needEmbed = false
		}
	}
	if needEmbed {
		vec, err := idx.embedder.Embed(ctx, BuildEmbedText(in))
		if err != nil {
			idx.logger.WarnContext(ctx, "embedder failed during product.upserted; continuing with keyword sync",
				slog.String("product_id", productID),
				slog.String("error", err.Error()),
			)
		} else if len(vec) > 0 {
			v := pgvector.NewVector(vec)
			embVec = &v
		}
	}

	catMap, shopMap := idx.loadSingleMetadata(ctx, in.CategoryID, in.ShopID)
	ancestors := resolveAncestors(in.CategoryID, in.CategoryAncestorIDs, catMap)
	inStock := in.Status == "APPROVED" && in.TotalStock > 0
	skuCodes := cleanSlice(in.SKUCodes)
	skuIDs := cleanSlice(in.SKUIDs)
	colors := cleanSlice(in.Colors)
	colorsFolded := foldSlice(colors)
	colorCodes := foldSlice(in.ColorCodes)
	sizes := cleanSlice(in.Sizes)
	sizesFolded := foldSlice(sizes)
	nameFolded := textnorm.NormalizeSearchText(in.Name)

	doc := buildMeiliDoc(in, nameFolded, ancestors, inStock, skuCodes, skuIDs, colors, colorsFolded, colorCodes, sizes, sizesFolded, catMap, shopMap)

	// S1: Hold product_embeddings row lock through Meilisearch sync before committing processed_events.
	return idx.store.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if eventID != "" {
			inserted, procErr := q.TryInsertProcessedEvent(ctx, db.TryInsertProcessedEventParams{
				EventID: eventID,
				Subject: "product.upserted",
			})
			if procErr != nil {
				return procErr
			}
			if inserted == 0 {
				return nil
			}
		}

		if in.ShopSuspended && in.ShopID != "" {
			if err := q.SeedSuspendedShopIfMissing(ctx, in.ShopID); err != nil {
				return err
			}
		}

		rows, upErr := q.UpsertProductFromEvent(ctx, db.UpsertProductFromEventParams{
			ProductID:           productID,
			ShopID:              in.ShopID,
			CategoryID:          in.CategoryID,
			CategoryAncestorIDs: ancestors,
			Name:                in.Name,
			NameFolded:          nameFolded,
			Slug:                in.Slug,
			Description:         in.Description,
			ThumbnailURL:        in.ThumbnailURL,
			MinPriceVND:         in.MinPriceVND,
			MaxPriceVND:         in.MaxPriceVND,
			Status:              in.Status,
			RatingAvgX100:       in.RatingAvgX100,
			ReviewCount:         in.ReviewCount,
			InStock:             inStock,
			SKUCount:            in.SKUCount,
			SKUCodes:            skuCodes,
			SKUAttributesText:   in.SKUAttributesText,
			SKUIDs:              skuIDs,
			DefaultSKUID:        in.DefaultSKUID,
			Colors:              colors,
			ColorsFolded:        colorsFolded,
			ColorCodes:          colorCodes,
			Sizes:               sizes,
			SizesFolded:         sizesFolded,
			Column26:            textHash,
			Embedding:           embVec,
			SourceUpdatedAt:     in.UpdatedAt.UTC(),
		})
		if upErr != nil {
			return upErr
		}
		if rows == 0 {
			return nil
		}

		if idx.meili != nil {
			if err := idx.meili.UpsertProducts(ctx, []meili.ProductDoc{doc}, true); err != nil {
				return fmt.Errorf("meilisearch upsert product %s: %w", productID, err)
			}
		}
		return nil
	})
}

// HandleProductDeleted tombstones a product in product_embeddings and deletes it from Meilisearch
// inside the same DB transaction so Meilisearch failures roll back processed_events and retry (G11/S1).
func (idx *Indexer) HandleProductDeleted(ctx context.Context, eventID, productID, shopID string, updatedAt time.Time) error {
	productID = strings.TrimSpace(productID)
	if productID == "" {
		return fmt.Errorf("%w: missing product_id in product.deleted", ErrPermanentPayload)
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	return idx.store.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if eventID != "" {
			inserted, procErr := q.TryInsertProcessedEvent(ctx, db.TryInsertProcessedEventParams{
				EventID: eventID,
				Subject: "product.deleted",
			})
			if procErr != nil {
				return procErr
			}
			if inserted == 0 {
				return nil
			}
		}

		rows, delErr := q.TombstoneProduct(ctx, db.TombstoneProductParams{
			ProductID:       productID,
			ShopID:          strings.TrimSpace(shopID),
			SourceUpdatedAt: updatedAt.UTC(),
		})
		if delErr != nil {
			return delErr
		}
		if rows == 0 {
			return nil
		}

		if idx.meili != nil {
			if err := idx.meili.DeleteProduct(ctx, productID, true); err != nil {
				return fmt.Errorf("meilisearch delete product %s: %w", productID, err)
			}
		}
		return nil
	})
}

// HandleCategoryUpdated updates search_categories, refreshes all descendant products in PG & Meilisearch,
// and marks processed_events AFTER the branch refresh completes inside one DB TX (G9/S1/S2).
// Retries never skip the branch based on comparing previous category state.
func (idx *Indexer) HandleCategoryUpdated(ctx context.Context, eventID string, in CategoryUpdateInput) error {
	catID := strings.TrimSpace(in.CategoryID)
	if catID == "" {
		return fmt.Errorf("%w: missing category_id in product.category_updated", ErrPermanentPayload)
	}
	action := strings.ToUpper(strings.TrimSpace(in.Action))
	if action == "" {
		action = "UPSERT"
	}
	if action != "UPSERT" && action != "DELETE" {
		return fmt.Errorf("%w: invalid action %q in product.category_updated", ErrPermanentPayload, in.Action)
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = time.Now().UTC()
	}

	return idx.store.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if eventID != "" {
			already, err := q.IsEventProcessed(ctx, eventID)
			if err != nil {
				return err
			}
			if already {
				return nil
			}
		}

		stale := false
		if existing, getErr := q.GetCategoryByID(ctx, catID); getErr == nil {
			if in.UpdatedAt.Before(existing.SourceUpdatedAt) {
				stale = true
			}
		} else if !errors.Is(getErr, pgx.ErrNoRows) {
			return getErr
		}

		if !stale {
			if action == "DELETE" {
				if delErr := q.DeleteCategory(ctx, db.DeleteCategoryParams{
					CategoryID:      catID,
					SourceUpdatedAt: in.UpdatedAt.UTC(),
				}); delErr != nil {
					return delErr
				}
			} else {
				if upErr := q.UpsertCategory(ctx, db.UpsertCategoryParams{
					CategoryID:      catID,
					Name:            strings.TrimSpace(in.Name),
					NameFolded:      textnorm.NormalizeSearchText(in.Name),
					Slug:            strings.TrimSpace(in.Slug),
					ParentID:        strings.TrimSpace(in.ParentID),
					SortOrder:       in.SortOrder,
					SourceUpdatedAt: in.UpdatedAt.UTC(),
				}); upErr != nil {
					return upErr
				}
			}

			// S2: Always refresh the category branch (never skip via prev comparison) before recording processed_events.
			if err := idx.reindexCategoryBranchTx(ctx, q, catID); err != nil {
				return err
			}
		}

		if eventID != "" {
			if _, err := q.TryInsertProcessedEvent(ctx, db.TryInsertProcessedEventParams{
				EventID: eventID,
				Subject: "product.category_updated",
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (idx *Indexer) reindexCategoryBranchTx(ctx context.Context, q *db.Queries, categoryID string) error {
	// Query locks branch rows ORDER BY product_id ASC FOR UPDATE.
	rows, err := q.ListProductsByCategoryBranch(ctx, categoryID)
	if err != nil {
		return fmt.Errorf("list category branch products: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	cats, err := q.ListAllCategories(ctx)
	if err != nil {
		return fmt.Errorf("load categories for branch refresh: %w", err)
	}
	catMap := make(map[string]db.SearchCategory, len(cats))
	for _, c := range cats {
		catMap[c.CategoryID] = c
	}

	shopIDSet := make(map[string]struct{}, len(rows))
	shopIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ShopID == "" {
			continue
		}
		if _, ok := shopIDSet[r.ShopID]; ok {
			continue
		}
		shopIDSet[r.ShopID] = struct{}{}
		shopIDs = append(shopIDs, r.ShopID)
	}
	shopMap := make(map[string]db.SearchShop, len(shopIDs))
	if len(shopIDs) > 0 {
		shops, err := q.ListShopsByIDs(ctx, shopIDs)
		if err != nil {
			return fmt.Errorf("load shops for branch refresh: %w", err)
		}
		for _, sh := range shops {
			shopMap[sh.ShopID] = sh
		}
	}

	docs := make([]meili.ProductDoc, 0, len(rows))
	for _, r := range rows {
		newAncestors := ComputeCategoryAncestors(r.CategoryID, catMap)
		if err := q.UpdateProductCategoryAncestors(ctx, db.UpdateProductCategoryAncestorsParams{
			ProductID:           r.ProductID,
			CategoryAncestorIDs: newAncestors,
		}); err != nil {
			return fmt.Errorf("update product %s category ancestors: %w", r.ProductID, err)
		}
		docs = append(docs, meiliDocFromEmbeddingRow(r, newAncestors, catMap, shopMap))
	}

	if idx.meili != nil && len(docs) > 0 {
		if err := idx.meili.UpsertProducts(ctx, docs, true); err != nil {
			return fmt.Errorf("meilisearch branch update category %s: %w", categoryID, err)
		}
	}
	return nil
}

// HandleShopReviewed processes user.shop_reviewed: APPROVE fetches User.GetShop and upserts search_shops; REJECT deletes.
func (idx *Indexer) HandleShopReviewed(ctx context.Context, eventID string, in ShopReviewedInput) error {
	shopID := strings.TrimSpace(in.ShopID)
	if shopID == "" {
		return fmt.Errorf("%w: missing shop_id in user.shop_reviewed", ErrPermanentPayload)
	}
	decision := strings.ToUpper(strings.TrimSpace(in.Decision))
	decision = strings.TrimPrefix(decision, "REVIEW_DECISION_")

	var (
		shopName      = strings.TrimSpace(in.ShopName)
		shopSlug      string
		shopStatus    = "ACTIVE"
		statusVersion int64
	)
	if decision == "APPROVE" && idx.userClient != nil {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		resp, err := idx.userClient.GetShop(callCtx, &userv1.GetShopRequest{ShopId: shopID})
		cancel()
		if err != nil {
			return fmt.Errorf("fetch shop %s from user service: %w", shopID, err)
		}
		if s := resp.GetShop(); s != nil {
			if strings.TrimSpace(s.GetName()) != "" {
				shopName = strings.TrimSpace(s.GetName())
			}
			shopSlug = strings.TrimSpace(s.GetSlug())
			if st := NormalizeShopStatus(s.GetStatus().String()); st == "ACTIVE" || st == "SUSPENDED" {
				shopStatus = st
			}
			statusVersion = s.GetStatusVersion()
		}
	}

	return idx.store.WithTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		if err := idx.store.LockShopStatus(ctx, tx, shopID); err != nil {
			return err
		}
		if eventID != "" {
			inserted, procErr := q.TryInsertProcessedEvent(ctx, db.TryInsertProcessedEventParams{
				EventID: eventID,
				Subject: "user.shop_reviewed",
			})
			if procErr != nil {
				return procErr
			}
			if inserted == 0 {
				return nil
			}
		}

		switch decision {
		case "APPROVE":
			return q.UpsertShopMetadata(ctx, db.UpsertShopMetadataParams{
				ShopID:        shopID,
				Name:          shopName,
				NameFolded:    textnorm.NormalizeSearchText(shopName),
				Slug:          shopSlug,
				Status:        shopStatus,
				StatusVersion: statusVersion,
			})
		case "REJECT":
			return q.DeleteShop(ctx, shopID)
		default:
			return nil
		}
	})
}

// HandleShopStatusChanged processes user.shop_status_changed (F12/G10):
// version <= 0 is permanently rejected to DLQ; version > status_version atomically flips search_shops.status.
func (idx *Indexer) HandleShopStatusChanged(ctx context.Context, eventID string, in ShopStatusChangedInput) error {
	shopID := strings.TrimSpace(in.ShopID)
	if shopID == "" {
		return fmt.Errorf("%w: missing shop_id in user.shop_status_changed", ErrPermanentPayload)
	}
	if in.Version <= 0 {
		return fmt.Errorf("%w: invalid non-positive version %d in user.shop_status_changed", ErrPermanentPayload, in.Version)
	}
	toStatus := NormalizeShopStatus(in.ToStatus)
	if toStatus != "SUSPENDED" && toStatus != "ACTIVE" {
		return fmt.Errorf("%w: invalid to_status %q in user.shop_status_changed", ErrPermanentPayload, in.ToStatus)
	}

	shopName := strings.TrimSpace(in.ShopName)
	return idx.store.WithTx(ctx, func(q *db.Queries, tx pgx.Tx) error {
		if err := idx.store.LockShopStatus(ctx, tx, shopID); err != nil {
			return err
		}
		if eventID != "" {
			inserted, procErr := q.TryInsertProcessedEvent(ctx, db.TryInsertProcessedEventParams{
				EventID: eventID,
				Subject: "user.shop_status_changed",
			})
			if procErr != nil {
				return procErr
			}
			if inserted == 0 {
				return nil
			}
		}

		_, err := q.UpsertShopStatus(ctx, db.UpsertShopStatusParams{
			ShopID:        shopID,
			Name:          shopName,
			NameFolded:    textnorm.NormalizeSearchText(shopName),
			Status:        toStatus,
			StatusVersion: in.Version,
		})
		return err
	})
}

// StartReindex acquires the global Postgres advisory lock and launches an async background reindex job (G11).
func (idx *Indexer) StartReindex(ctx context.Context, opts ReindexOptions) (string, error) {
	if !opts.ReindexProducts && !opts.ReindexEmbeddings && !opts.ReindexCategories && !opts.ReindexShops {
		opts.ReindexProducts = true
		opts.ReindexEmbeddings = true
		opts.ReindexCategories = true
		opts.ReindexShops = true
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultReindexBatchSize
	}
	if opts.BatchSize > maxReindexBatchSize {
		opts.BatchSize = maxReindexBatchSize
	}

	releaseLock, acquired, err := idx.store.AcquireReindexLock(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire reindex advisory lock: %w", err)
	}
	if !acquired {
		return "", ErrReindexAlreadyRunning
	}

	// Clean up any orphaned RUNNING rows left behind by a previous process crash.
	if err := idx.store.Queries().FailStaleRunningReindexJobs(ctx); err != nil {
		releaseLock()
		return "", fmt.Errorf("clean stale reindex jobs: %w", err)
	}

	jobID := uuid.NewString()
	if err := idx.store.Queries().CreateReindexJob(ctx, jobID); err != nil {
		releaseLock()
		return "", fmt.Errorf("create reindex job: %w", err)
	}

	idx.wg.Add(1)
	go func() {
		defer idx.wg.Done()
		defer releaseLock()
		idx.runReindexJob(idx.baseCtx, jobID, opts)
	}()

	return jobID, nil
}

func (idx *Indexer) runReindexJob(ctx context.Context, jobID string, opts ReindexOptions) {
	var (
		productsIndexed   int64
		embeddingsUpdated int64
	)

	finish := func(runErr error) {
		status := "COMPLETED"
		errMsg := ""
		if runErr != nil {
			status = "FAILED"
			errMsg = runErr.Error()
			idx.logger.Error("reindex job failed",
				slog.String("job_id", jobID),
				slog.Int64("products_indexed", productsIndexed),
				slog.Int64("embeddings_updated", embeddingsUpdated),
				slog.String("error", errMsg),
			)
		} else {
			idx.logger.Info("reindex job completed",
				slog.String("job_id", jobID),
				slog.Int64("products_indexed", productsIndexed),
				slog.Int64("embeddings_updated", embeddingsUpdated),
			)
		}
		finCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = idx.store.Queries().FinishReindexJob(finCtx, db.FinishReindexJobParams{
			JobID:             jobID,
			Status:            status,
			ProductsIndexed:   productsIndexed,
			EmbeddingsUpdated: embeddingsUpdated,
			ErrorMessage:      errMsg,
		})
	}

	if opts.ReindexCategories {
		if err := idx.syncCategories(ctx); err != nil {
			finish(err)
			return
		}
	}
	if opts.ReindexShops {
		if err := idx.syncShops(ctx); err != nil {
			finish(err)
			return
		}
	}

	if opts.ReindexProducts || opts.ReindexEmbeddings {
		if idx.productClient == nil {
			finish(errors.New("product gRPC client is not configured"))
			return
		}
		if err := idx.syncProductsKeyset(ctx, jobID, opts, &productsIndexed, &embeddingsUpdated); err != nil {
			finish(err)
			return
		}
	}

	finish(nil)
}

func (idx *Indexer) syncCategories(ctx context.Context) error {
	if idx.productClient == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// ProductService.ListCategories is a public RPC (rejects service tokens).
	resp, err := idx.productClient.ListCategories(callCtx, &productv1.ListCategoriesRequest{})
	if err != nil {
		return fmt.Errorf("list categories from product service: %w", err)
	}
	now := time.Now().UTC()
	for _, c := range resp.GetCategories() {
		if strings.TrimSpace(c.GetId()) == "" {
			continue
		}
		ts := now
		if c.GetUpdatedAt() != nil {
			ts = c.GetUpdatedAt().AsTime().UTC()
		}
		if err := idx.store.Queries().UpsertCategory(ctx, db.UpsertCategoryParams{
			CategoryID:      strings.TrimSpace(c.GetId()),
			Name:            strings.TrimSpace(c.GetName()),
			NameFolded:      textnorm.NormalizeSearchText(c.GetName()),
			Slug:            strings.TrimSpace(c.GetSlug()),
			ParentID:        strings.TrimSpace(c.GetParentId()),
			SortOrder:       c.GetSortOrder(),
			SourceUpdatedAt: ts,
		}); err != nil {
			return fmt.Errorf("upsert category %s: %w", c.GetId(), err)
		}
	}
	return nil
}

func (idx *Indexer) syncShops(ctx context.Context) error {
	if idx.userClient == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for page := int32(1); page <= maxShopSyncPages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		// UserService.ListPublicShops is a public RPC (rejects service tokens).
		resp, err := idx.userClient.ListPublicShops(callCtx, &userv1.ListPublicShopsRequest{
			Page:     page,
			PageSize: shopSyncPageSize,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("list public shops page %d: %w", page, err)
		}
		shops := resp.GetShops()
		if len(shops) == 0 {
			return nil
		}
		newInPage := 0
		for _, s := range shops {
			sid := strings.TrimSpace(s.GetId())
			if sid == "" {
				continue
			}
			if _, ok := seen[sid]; !ok {
				seen[sid] = struct{}{}
				newInPage++
			}
			st := NormalizeShopStatus(s.GetStatus().String())
			if st != "ACTIVE" && st != "SUSPENDED" {
				st = "ACTIVE"
			}
			if err := idx.store.Queries().UpsertShopMetadata(ctx, db.UpsertShopMetadataParams{
				ShopID:        sid,
				Name:          strings.TrimSpace(s.GetName()),
				NameFolded:    textnorm.NormalizeSearchText(s.GetName()),
				Slug:          strings.TrimSpace(s.GetSlug()),
				Status:        st,
				StatusVersion: s.GetStatusVersion(),
			}); err != nil {
				return fmt.Errorf("upsert shop %s: %w", sid, err)
			}
		}
		if newInPage == 0 {
			return fmt.Errorf("list public shops page %d made no forward progress", page)
		}
		if resp.GetTotal() > 0 && int64(len(seen)) >= resp.GetTotal() {
			return nil
		}
	}
	return fmt.Errorf("list public shops exceeded max pages (%d)", maxShopSyncPages)
}

func (idx *Indexer) syncProductsKeyset(
	ctx context.Context,
	jobID string,
	opts ReindexOptions,
	productsIndexed *int64,
	embeddingsUpdated *int64,
) error {
	jobStartedAt := time.Now().UTC()
	var (
		catMap  map[string]db.SearchCategory
		shopMap map[string]db.SearchShop
		seenIDs []string
	)
	if opts.ReindexProducts {
		catMap, shopMap = idx.loadAllMetadata(ctx)
		seenIDs = make([]string, 0, 256)
	}
	afterID := ""

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if idx.svcAuth != nil {
			token, err := idx.svcAuth.SignServiceToken("search-service")
			if err != nil {
				cancel()
				return fmt.Errorf("sign service token for ListProductsForIndex: %w", err)
			}
			callCtx = auth.InjectOutgoingGRPC(callCtx, token)
		}
		resp, err := idx.productClient.ListProductsForIndex(callCtx, &productv1.ListProductsForIndexRequest{
			AfterId:  afterID,
			PageSize: opts.BatchSize,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("ListProductsForIndex(after_id=%q): %w", afterID, err)
		}

		items := resp.GetItems()
		if opts.ReindexProducts {
			for _, it := range items {
				if id := strings.TrimSpace(it.GetId()); id != "" {
					seenIDs = append(seenIDs, id)
				}
			}
		}
		if len(items) > 0 {
			var (
				indexed  int64
				embedded int64
				batchErr error
			)
			if !opts.ReindexProducts && opts.ReindexEmbeddings {
				embedded, batchErr = idx.processEmbeddingsOnlyBatch(ctx, items)
			} else {
				indexed, embedded, batchErr = idx.processReindexBatch(ctx, items, opts, catMap, shopMap)
			}
			if batchErr != nil {
				return batchErr
			}
			*productsIndexed += indexed
			*embeddingsUpdated += embedded
		}

		nextCursor := strings.TrimSpace(resp.GetNextCursor())
		_ = idx.store.Queries().UpdateReindexJobProgress(ctx, db.UpdateReindexJobProgressParams{
			JobID:             jobID,
			ProductsIndexed:   *productsIndexed,
			EmbeddingsUpdated: *embeddingsUpdated,
			LastCursor:        nextCursor,
		})

		if !resp.GetHasMore() || nextCursor == "" || nextCursor == afterID {
			break
		}
		afterID = nextCursor
	}

	// S4: End-of-job reconciliation sweep when ReindexProducts is enabled.
	if opts.ReindexProducts {
		if err := idx.sweepMissingAndTombstoned(ctx, jobStartedAt, seenIDs); err != nil {
			return err
		}
	}
	return nil
}

// sweepMissingAndTombstoned reconciles drift at the end of a ReindexAll products pass (S4):
//  1. Live PG rows missed by the full keyset scan whose source_updated_at < jobStartedAt are
//     tombstoned in PG and deleted from Meilisearch inside a transaction.
//  2. All tombstoned PG rows (deleted = TRUE) are purged from Meilisearch under FOR SHARE locks
//     so lingering Meilisearch ghosts are removed without racing with live un-tombstone events.
func (idx *Indexer) sweepMissingAndTombstoned(ctx context.Context, jobStartedAt time.Time, seenIDs []string) error {
	if seenIDs == nil {
		seenIDs = []string{}
	}

	// 1. Tombstone missing stale live products and remove them from Meilisearch.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		missingIDs, err := idx.store.Queries().ListMissingLiveProductIDsForSweep(ctx, db.ListMissingLiveProductIDsForSweepParams{
			SourceUpdatedAt: jobStartedAt,
			Column2:         seenIDs,
			Limit:           sweepBatchSize,
		})
		if err != nil {
			return fmt.Errorf("list missing live products for sweep: %w", err)
		}
		if len(missingIDs) == 0 {
			break
		}
		sort.Strings(missingIDs)

		if err := idx.store.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
			tombstoned, err := q.TombstoneStaleProductsBatch(ctx, db.TombstoneStaleProductsBatchParams{
				Column1:         missingIDs,
				SourceUpdatedAt: jobStartedAt,
			})
			if err != nil {
				return fmt.Errorf("tombstone stale products batch: %w", err)
			}
			if idx.meili != nil && len(tombstoned) > 0 {
				meiliCtx := meili.WithTaskWaitTimeout(ctx, meili.ReindexTaskWaitTimeout)
				if err := idx.meili.DeleteProducts(meiliCtx, tombstoned, true); err != nil {
					return fmt.Errorf("meilisearch delete swept stale products: %w", err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	// 2. Purge any tombstones still lingering in Meilisearch.
	if idx.meili == nil {
		return nil
	}
	afterTombID := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		tombIDs, err := idx.store.Queries().ListTombstonedProductIDsBatch(ctx, db.ListTombstonedProductIDsBatchParams{
			Column1: afterTombID,
			Limit:   sweepBatchSize,
		})
		if err != nil {
			return fmt.Errorf("list tombstoned products for meili purge: %w", err)
		}
		if len(tombIDs) == 0 {
			break
		}
		afterTombID = tombIDs[len(tombIDs)-1]

		if err := idx.store.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
			lockedIDs, err := q.LockTombstonedProductIDs(ctx, tombIDs)
			if err != nil {
				return fmt.Errorf("lock tombstoned products: %w", err)
			}
			if len(lockedIDs) > 0 {
				meiliCtx := meili.WithTaskWaitTimeout(ctx, meili.ReindexTaskWaitTimeout)
				if err := idx.meili.DeleteProducts(meiliCtx, lockedIDs, true); err != nil {
					return fmt.Errorf("meilisearch purge tombstoned products: %w", err)
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return nil
}

// processEmbeddingsOnlyBatch implements K1 (reindex_products=false, reindex_embeddings=true):
// never touches Meilisearch, never inserts missing rows or overwrites non-embedding columns,
// and updates only (text_hash, embedding) atomically for existing live rows where
// source_updated_at <= snapshot.updated_at and the embedding is missing or stale (S3).
func (idx *Indexer) processEmbeddingsOnlyBatch(ctx context.Context, items []*productv1.IndexableProduct) (int64, error) {
	if idx.embedder == nil {
		return 0, nil
	}

	candidates := make([]ProductUpsertInput, 0, len(items))
	candidateIDs := make([]string, 0, len(items))
	for _, it := range items {
		in := fromProtoIndexable(it)
		if in.ProductID == "" || !IsPublicProductStatus(in.Status) || in.MinPriceVND < 0 || in.MaxPriceVND < 0 {
			continue
		}
		candidates = append(candidates, in)
		candidateIDs = append(candidateIDs, in.ProductID)
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	// Deterministic ascending order matches PostgreSQL row-lock acquisition order across concurrent writers.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ProductID < candidates[j].ProductID })

	existingRows, err := idx.store.Queries().GetProductEmbeddingStatesBatch(ctx, candidateIDs)
	if err != nil {
		return 0, fmt.Errorf("load existing embedding states: %w", err)
	}
	existingByID := make(map[string]db.GetProductEmbeddingStatesBatchRow, len(existingRows))
	for _, r := range existingRows {
		existingByID[r.ProductID] = r
	}

	type embedTarget struct {
		productID string
		textHash  string
		updatedAt time.Time
	}

	targets := make([]embedTarget, 0, len(candidates))
	textsToEmbed := make([]string, 0, len(candidates))
	for _, in := range candidates {
		existing, hasExisting := existingByID[in.ProductID]
		if !hasExisting || existing.Deleted {
			continue
		}
		if existing.SourceUpdatedAt.After(in.UpdatedAt) {
			continue
		}
		textHash := ComputeTextHash(in)
		if existing.HasEmbedding && existing.TextHash == textHash {
			continue
		}
		targets = append(targets, embedTarget{
			productID: in.ProductID,
			textHash:  textHash,
			updatedAt: in.UpdatedAt.UTC(),
		})
		textsToEmbed = append(textsToEmbed, BuildEmbedText(in))
	}
	if len(targets) == 0 {
		return 0, nil
	}

	computedVecs, embErr := idx.embedder.EmbedBatch(ctx, textsToEmbed)
	if embErr != nil {
		return 0, fmt.Errorf("compute batch embeddings: %w", embErr)
	}
	if len(computedVecs) != len(targets) {
		return 0, fmt.Errorf("embedder returned %d vectors for %d targets", len(computedVecs), len(targets))
	}

	var embeddedCount int64
	if err := idx.store.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		for i, t := range targets {
			if len(computedVecs[i]) == 0 {
				continue
			}
			v := pgvector.NewVector(computedVecs[i])
			rows, upErr := q.UpdateProductEmbeddingOnly(ctx, db.UpdateProductEmbeddingOnlyParams{
				ProductID:       t.productID,
				TextHash:        t.textHash,
				Embedding:       &v,
				SourceUpdatedAt: t.updatedAt,
			})
			if upErr != nil {
				return fmt.Errorf("update embedding for product %s: %w", t.productID, upErr)
			}
			embeddedCount += rows
		}
		return nil
	}); err != nil {
		return 0, err
	}

	return embeddedCount, nil
}

func (idx *Indexer) processReindexBatch(
	ctx context.Context,
	items []*productv1.IndexableProduct,
	opts ReindexOptions,
	catMap map[string]db.SearchCategory,
	shopMap map[string]db.SearchShop,
) (int64, int64, error) {
	candidates := make([]ProductUpsertInput, 0, len(items))
	candidateIDs := make([]string, 0, len(items))
	for _, it := range items {
		in := fromProtoIndexable(it)
		if in.ProductID == "" || !IsPublicProductStatus(in.Status) || in.MinPriceVND < 0 || in.MaxPriceVND < 0 {
			continue
		}
		candidates = append(candidates, in)
		candidateIDs = append(candidateIDs, in.ProductID)
	}
	if len(candidates) == 0 {
		return 0, 0, nil
	}

	// Deterministic ascending order matches PostgreSQL row-lock acquisition order across concurrent writers.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ProductID < candidates[j].ProductID })

	existingRows, err := idx.store.Queries().GetProductEmbeddingStatesBatch(ctx, candidateIDs)
	if err != nil {
		return 0, 0, fmt.Errorf("load existing embedding states: %w", err)
	}
	existingByID := make(map[string]db.GetProductEmbeddingStatesBatchRow, len(existingRows))
	for _, r := range existingRows {
		existingByID[r.ProductID] = r
	}

	type preparedItem struct {
		input        ProductUpsertInput
		nameFolded   string
		textHash     string
		ancestors    []string
		inStock      bool
		skuCodes     []string
		skuIDs       []string
		colors       []string
		colorsFolded []string
		colorCodes   []string
		sizes        []string
		sizesFolded  []string
		embedIdx     int
	}

	prepared := make([]preparedItem, 0, len(candidates))
	textsToEmbed := make([]string, 0, len(candidates))

	for _, in := range candidates {
		textHash := ComputeTextHash(in)
		existing, hasExisting := existingByID[in.ProductID]
		// S4: Same timestamp rule as live events — resurrect a tombstone ONLY when incoming
		// source_updated_at is strictly newer than the tombstone; skip stale snapshots.
		if hasExisting {
			if existing.Deleted && !in.UpdatedAt.After(existing.SourceUpdatedAt) {
				continue
			}
			if !existing.Deleted && existing.SourceUpdatedAt.After(in.UpdatedAt) {
				continue
			}
		}

		embedIdx := -1
		if opts.ReindexEmbeddings && idx.embedder != nil {
			if !hasExisting || existing.TextHash != textHash || !existing.HasEmbedding {
				embedIdx = len(textsToEmbed)
				textsToEmbed = append(textsToEmbed, BuildEmbedText(in))
			}
		}

		colors := cleanSlice(in.Colors)
		sizes := cleanSlice(in.Sizes)
		prepared = append(prepared, preparedItem{
			input:        in,
			nameFolded:   textnorm.NormalizeSearchText(in.Name),
			textHash:     textHash,
			ancestors:    resolveAncestors(in.CategoryID, in.CategoryAncestorIDs, catMap),
			inStock:      in.Status == "APPROVED" && in.TotalStock > 0,
			skuCodes:     cleanSlice(in.SKUCodes),
			skuIDs:       cleanSlice(in.SKUIDs),
			colors:       colors,
			colorsFolded: foldSlice(colors),
			colorCodes:   foldSlice(in.ColorCodes),
			sizes:        sizes,
			sizesFolded:  foldSlice(sizes),
			embedIdx:     embedIdx,
		})
	}
	if len(prepared) == 0 {
		return 0, 0, nil
	}

	var computedVecs [][]float32
	if len(textsToEmbed) > 0 {
		vecs, embErr := idx.embedder.EmbedBatch(ctx, textsToEmbed)
		if embErr != nil {
			return 0, 0, fmt.Errorf("compute batch embeddings: %w", embErr)
		}
		computedVecs = vecs
	}

	var (
		indexedCount  int64
		embeddedCount int64
	)

	// S1/S4: Commit PG batch upserts and Meilisearch batch upsert together under PG row locks
	// (acquired in ascending product_id order), so concurrent product.deleted cannot be resurrected.
	if err := idx.store.WithTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		docsToPush := make([]meili.ProductDoc, 0, len(prepared))
		for _, p := range prepared {
			if p.input.ShopSuspended && p.input.ShopID != "" {
				if err := q.SeedSuspendedShopIfMissing(ctx, p.input.ShopID); err != nil {
					return err
				}
			}

			var embPtr *pgvector.Vector
			if p.embedIdx >= 0 && p.embedIdx < len(computedVecs) {
				v := pgvector.NewVector(computedVecs[p.embedIdx])
				embPtr = &v
			}

			rows, upErr := q.UpsertProductFromReindex(ctx, db.UpsertProductFromReindexParams{
				ProductID:           p.input.ProductID,
				ShopID:              p.input.ShopID,
				CategoryID:          p.input.CategoryID,
				CategoryAncestorIDs: p.ancestors,
				Name:                p.input.Name,
				NameFolded:          p.nameFolded,
				Slug:                p.input.Slug,
				Description:         p.input.Description,
				ThumbnailURL:        p.input.ThumbnailURL,
				MinPriceVND:         p.input.MinPriceVND,
				MaxPriceVND:         p.input.MaxPriceVND,
				Status:              p.input.Status,
				RatingAvgX100:       p.input.RatingAvgX100,
				ReviewCount:         p.input.ReviewCount,
				InStock:             p.inStock,
				SKUCount:            p.input.SKUCount,
				SKUCodes:            p.skuCodes,
				SKUAttributesText:   p.input.SKUAttributesText,
				SKUIDs:              p.skuIDs,
				DefaultSKUID:        p.input.DefaultSKUID,
				Colors:              p.colors,
				ColorsFolded:        p.colorsFolded,
				ColorCodes:          p.colorCodes,
				Sizes:               p.sizes,
				SizesFolded:         p.sizesFolded,
				Column26:            p.textHash,
				Embedding:           embPtr,
				SourceUpdatedAt:     p.input.UpdatedAt.UTC(),
			})
			if upErr != nil {
				return fmt.Errorf("upsert product %s from reindex: %w", p.input.ProductID, upErr)
			}
			if rows == 0 {
				continue
			}

			indexedCount++
			if embPtr != nil {
				embeddedCount++
			}
			docsToPush = append(docsToPush, buildMeiliDoc(
				p.input, p.nameFolded, p.ancestors, p.inStock, p.skuCodes, p.skuIDs,
				p.colors, p.colorsFolded, p.colorCodes, p.sizes, p.sizesFolded, catMap, shopMap,
			))
		}

		if idx.meili != nil && len(docsToPush) > 0 {
			meiliCtx := meili.WithTaskWaitTimeout(ctx, meili.ReindexTaskWaitTimeout)
			if err := idx.meili.UpsertProducts(meiliCtx, docsToPush, true); err != nil {
				return fmt.Errorf("meilisearch reindex batch upsert: %w", err)
			}
		}
		return nil
	}); err != nil {
		return 0, 0, err
	}

	return indexedCount, embeddedCount, nil
}

func fromProtoIndexable(it *productv1.IndexableProduct) ProductUpsertInput {
	if it == nil {
		return ProductUpsertInput{}
	}
	ts := time.Now().UTC()
	if it.GetUpdatedAt() != nil {
		ts = it.GetUpdatedAt().AsTime().UTC()
	}
	rating := it.GetRatingAvgX100()
	if rating < 0 {
		rating = 0
	}
	if rating > 500 {
		rating = 500
	}
	reviewCount := it.GetReviewCount()
	if reviewCount < 0 {
		reviewCount = 0
	}
	return ProductUpsertInput{
		ProductID:           strings.TrimSpace(it.GetId()),
		ShopID:              strings.TrimSpace(it.GetShopId()),
		CategoryID:          strings.TrimSpace(it.GetCategoryId()),
		CategoryAncestorIDs: it.GetCategoryAncestorIds(),
		Name:                it.GetName(),
		Slug:                it.GetSlug(),
		Description:         it.GetDescription(),
		ThumbnailURL:        it.GetThumbnailUrl(),
		MinPriceVND:         it.GetMinPriceVnd(),
		MaxPriceVND:         it.GetMaxPriceVnd(),
		Status:              NormalizeProductStatus(it.GetStatus().String()),
		SKUCodes:            it.GetSkuCodes(),
		SKUAttributesText:   it.GetSkuAttributesText(),
		TotalStock:          it.GetTotalStock(),
		SKUCount:            it.GetSkuCount(),
		SKUIDs:              it.GetSkuIds(),
		DefaultSKUID:        it.GetDefaultSkuId(),
		Colors:              it.GetColors(),
		ColorCodes:          it.GetColorCodes(),
		Sizes:               it.GetSizes(),
		RatingAvgX100:       rating,
		ReviewCount:         reviewCount,
		ShopSuspended:       it.GetShopSuspended(),
		UpdatedAt:           ts,
	}
}

func (idx *Indexer) loadSingleMetadata(ctx context.Context, catID, shopID string) (map[string]db.SearchCategory, map[string]db.SearchShop) {
	catMap := make(map[string]db.SearchCategory)
	shopMap := make(map[string]db.SearchShop)
	if catID != "" {
		if cats, err := idx.store.Queries().ListAllCategories(ctx); err == nil {
			for _, c := range cats {
				catMap[c.CategoryID] = c
			}
		}
	}
	if shopID != "" {
		if sh, err := idx.store.Queries().GetShopByID(ctx, shopID); err == nil {
			shopMap[sh.ShopID] = sh
		}
	}
	return catMap, shopMap
}

func (idx *Indexer) loadAllMetadata(ctx context.Context) (map[string]db.SearchCategory, map[string]db.SearchShop) {
	catMap := make(map[string]db.SearchCategory)
	shopMap := make(map[string]db.SearchShop)
	if cats, err := idx.store.Queries().ListAllCategories(ctx); err == nil {
		for _, c := range cats {
			catMap[c.CategoryID] = c
		}
	}
	if shops, err := idx.store.Queries().ListAllShops(ctx); err == nil {
		for _, s := range shops {
			shopMap[s.ShopID] = s
		}
	}
	return catMap, shopMap
}

func resolveAncestors(categoryID string, provided []string, catMap map[string]db.SearchCategory) []string {
	if len(provided) > 0 {
		out := cleanSlice(provided)
		if categoryID != "" {
			found := false
			for _, id := range out {
				if id == categoryID {
					found = true
					break
				}
			}
			if !found {
				out = append([]string{categoryID}, out...)
			}
		}
		return out
	}
	return ComputeCategoryAncestors(categoryID, catMap)
}

func buildMeiliDoc(
	in ProductUpsertInput,
	nameFolded string,
	ancestors []string,
	inStock bool,
	skuCodes, skuIDs, colors, colorsFolded, colorCodes, sizes, sizesFolded []string,
	catMap map[string]db.SearchCategory,
	shopMap map[string]db.SearchShop,
) meili.ProductDoc {
	cat := catMap[in.CategoryID]
	sh := shopMap[in.ShopID]
	return meili.ProductDoc{
		ID:                  in.ProductID,
		ShopID:              in.ShopID,
		ShopName:            sh.Name,
		ShopSlug:            sh.Slug,
		CategoryID:          in.CategoryID,
		CategorySlug:        cat.Slug,
		CategoryName:        cat.Name,
		CategoryAncestorIDs: ancestors,
		Name:                in.Name,
		NameFolded:          nameFolded,
		Slug:                in.Slug,
		DescriptionSnippet:  TruncateSnippet(in.Description),
		DescriptionFolded:   textnorm.NormalizeSearchText(in.Description),
		ThumbnailURL:        in.ThumbnailURL,
		MinPriceVND:         in.MinPriceVND,
		MaxPriceVND:         in.MaxPriceVND,
		Status:              in.Status,
		RatingAvgX100:       in.RatingAvgX100,
		ReviewCount:         in.ReviewCount,
		InStock:             inStock,
		SKUCount:            in.SKUCount,
		SKUCodes:            skuCodes,
		SKUAttributesText:   textnorm.NormalizeSearchText(in.SKUAttributesText),
		SKUIDs:              skuIDs,
		DefaultSKUID:        in.DefaultSKUID,
		Colors:              colors,
		ColorsFolded:        colorsFolded,
		ColorCodes:          colorCodes,
		Sizes:               sizes,
		SizesFolded:         sizesFolded,
		UpdatedAtUnix:       in.UpdatedAt.UTC().Unix(),
	}
}

func meiliDocFromEmbeddingRow(
	r db.ListProductsByCategoryBranchRow,
	ancestors []string,
	catMap map[string]db.SearchCategory,
	shopMap map[string]db.SearchShop,
) meili.ProductDoc {
	cat := catMap[r.CategoryID]
	sh := shopMap[r.ShopID]
	nameFolded := r.NameFolded
	if nameFolded == "" {
		nameFolded = textnorm.NormalizeSearchText(r.Name)
	}
	return meili.ProductDoc{
		ID:                  r.ProductID,
		ShopID:              r.ShopID,
		ShopName:            sh.Name,
		ShopSlug:            sh.Slug,
		CategoryID:          r.CategoryID,
		CategorySlug:        cat.Slug,
		CategoryName:        cat.Name,
		CategoryAncestorIDs: ancestors,
		Name:                r.Name,
		NameFolded:          nameFolded,
		Slug:                r.Slug,
		DescriptionSnippet:  TruncateSnippet(r.Description),
		DescriptionFolded:   textnorm.NormalizeSearchText(r.Description),
		ThumbnailURL:        r.ThumbnailURL,
		MinPriceVND:         r.MinPriceVND,
		MaxPriceVND:         r.MaxPriceVND,
		Status:              r.Status,
		RatingAvgX100:       r.RatingAvgX100,
		ReviewCount:         r.ReviewCount,
		InStock:             r.InStock,
		SKUCount:            r.SKUCount,
		SKUCodes:            r.SKUCodes,
		SKUAttributesText:   textnorm.NormalizeSearchText(r.SKUAttributesText),
		SKUIDs:              r.SKUIDs,
		DefaultSKUID:        r.DefaultSKUID,
		Colors:              r.Colors,
		ColorsFolded:        r.ColorsFolded,
		ColorCodes:          r.ColorCodes,
		Sizes:               r.Sizes,
		SizesFolded:         r.SizesFolded,
		UpdatedAtUnix:       r.SourceUpdatedAt.UTC().Unix(),
	}
}
