package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexusmart/core/services/product/internal/db"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// PgStore implements Store backed by PostgreSQL using pgx/v5 and sqlc queries.
type PgStore struct {
	pool *pgxpool.Pool
	*pgQueries
}

// NewPgStore creates a PgStore wrapping the given pgxpool.Pool.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{
		pool:      pool,
		pgQueries: &pgQueries{q: db.New(pool)},
	}
}

func (s *PgStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PgStore) WithTx(ctx context.Context, fn func(q Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	txq := &pgQueries{q: db.New(tx)}
	if err := fn(txq); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (s *PgStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev OutboxEvent) error) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin outbox tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)
	rows, err := q.LockPendingOutboxEvents(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("lock pending outbox events: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	published := 0
	for _, row := range rows {
		ev := fromDBOutboxEvent(row)
		if pubErr := publishFn(ctx, ev); pubErr != nil {
			_ = q.MarkOutboxEventAttemptFailed(ctx, db.MarkOutboxEventAttemptFailedParams{
				ID:        ev.ID,
				LastError: pubErr.Error(),
			})
			continue
		}
		if err := q.MarkOutboxEventPublished(ctx, ev.ID); err != nil {
			return published, fmt.Errorf("mark outbox event published: %w", err)
		}
		published++
	}

	if err := tx.Commit(ctx); err != nil {
		return published, fmt.Errorf("commit outbox tx: %w", err)
	}
	return published, nil
}

type pgQueries struct {
	q *db.Queries
}

func (p *pgQueries) LockCategoryTree(ctx context.Context) error {
	return mapPgError(p.q.LockCategoryTree(ctx))
}

func (p *pgQueries) CreateCategory(ctx context.Context, cat Category) (Category, error) {
	row, err := p.q.CreateCategory(ctx, db.CreateCategoryParams{
		ID:                cat.ID,
		Name:              cat.Name,
		Slug:              cat.Slug,
		ParentID:          toPgText(cat.ParentID),
		CommissionRateBps: cat.CommissionRateBps,
		SortOrder:         cat.SortOrder,
	})
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return fromDBCategory(row), nil
}

func (p *pgQueries) GetCategoryByID(ctx context.Context, id string) (Category, error) {
	row, err := p.q.GetCategoryByID(ctx, db.GetCategoryByIDParams{
		AsOf: AsOfFromContext(ctx),
		ID:   id,
	})
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return Category{
		ID:                row.ID,
		Name:              row.Name,
		Slug:              row.Slug,
		ParentID:          row.ParentID.String,
		CommissionRateBps: row.CommissionRateBps,
		SortOrder:         row.SortOrder,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}, nil
}

func (p *pgQueries) GetCategoryBySlug(ctx context.Context, slug string) (Category, error) {
	row, err := p.q.GetCategoryBySlug(ctx, slug)
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return fromDBCategory(row), nil
}

func (p *pgQueries) UpdateCategory(ctx context.Context, cat Category) (Category, error) {
	row, err := p.q.UpdateCategory(ctx, db.UpdateCategoryParams{
		ID:                cat.ID,
		Name:              cat.Name,
		Slug:              cat.Slug,
		ParentID:          toPgText(cat.ParentID),
		CommissionRateBps: cat.CommissionRateBps,
		SortOrder:         cat.SortOrder,
	})
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return fromDBCategory(row), nil
}

func (p *pgQueries) UpdateCategoryCommissionRate(ctx context.Context, id string, bps int32) error {
	return mapPgError(p.q.UpdateCategoryCommissionRate(ctx, db.UpdateCategoryCommissionRateParams{
		ID:                id,
		CommissionRateBps: bps,
	}))
}

func (p *pgQueries) CountProductsInCategory(ctx context.Context, categoryID string) (CategoryProductCounts, error) {
	row, err := p.q.CountProductsInCategory(ctx, categoryID)
	if err != nil {
		return CategoryProductCounts{}, mapPgError(err)
	}
	return CategoryProductCounts{
		Total:  row.TotalCnt,
		Active: row.ActiveCnt,
	}, nil
}

func (p *pgQueries) CountChildCategories(ctx context.Context, parentID string) (int64, error) {
	return p.q.CountChildCategories(ctx, toPgText(parentID))
}

func (p *pgQueries) DeleteCategory(ctx context.Context, id string) (bool, error) {
	n, err := p.q.DeleteCategory(ctx, id)
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) ListCategories(ctx context.Context, parentID string) ([]Category, error) {
	asOf := AsOfFromContext(ctx)
	switch parentID {
	case "":
		rows, err := p.q.ListAllCategories(ctx, asOf)
		if err != nil {
			return nil, mapPgError(err)
		}
		out := make([]Category, 0, len(rows))
		for _, r := range rows {
			out = append(out, Category{
				ID:                r.ID,
				Name:              r.Name,
				Slug:              r.Slug,
				ParentID:          r.ParentID.String,
				CommissionRateBps: r.CommissionRateBps,
				SortOrder:         r.SortOrder,
				CreatedAt:         r.CreatedAt,
				UpdatedAt:         r.UpdatedAt,
			})
		}
		return out, nil
	case "root":
		rows, err := p.q.ListRootCategories(ctx, asOf)
		if err != nil {
			return nil, mapPgError(err)
		}
		out := make([]Category, 0, len(rows))
		for _, r := range rows {
			out = append(out, Category{
				ID:                r.ID,
				Name:              r.Name,
				Slug:              r.Slug,
				ParentID:          r.ParentID.String,
				CommissionRateBps: r.CommissionRateBps,
				SortOrder:         r.SortOrder,
				CreatedAt:         r.CreatedAt,
				UpdatedAt:         r.UpdatedAt,
			})
		}
		return out, nil
	default:
		rows, err := p.q.ListCategoriesByParent(ctx, db.ListCategoriesByParentParams{
			AsOf:     asOf,
			ParentID: toPgText(parentID),
		})
		if err != nil {
			return nil, mapPgError(err)
		}
		out := make([]Category, 0, len(rows))
		for _, r := range rows {
			out = append(out, Category{
				ID:                r.ID,
				Name:              r.Name,
				Slug:              r.Slug,
				ParentID:          r.ParentID.String,
				CommissionRateBps: r.CommissionRateBps,
				SortOrder:         r.SortOrder,
				CreatedAt:         r.CreatedAt,
				UpdatedAt:         r.UpdatedAt,
			})
		}
		return out, nil
	}
}

func (p *pgQueries) CategoryHasAncestor(ctx context.Context, startID, targetAncestorID string) (bool, error) {
	has, err := p.q.CategoryHasAncestor(ctx, db.CategoryHasAncestorParams{
		TargetAncestorID: targetAncestorID,
		StartID:          startID,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return has, nil
}

func (p *pgQueries) GetCategoryDescendants(ctx context.Context, categoryIDs []string) ([]string, error) {
	if len(categoryIDs) == 0 {
		return []string{}, nil
	}
	ids, err := p.q.GetCategoryDescendants(ctx, categoryIDs)
	if err != nil {
		return nil, mapPgError(err)
	}
	return ids, nil
}

func (p *pgQueries) InsertCategoryFeeSchedule(ctx context.Context, sched CategoryFeeSchedule) (CategoryFeeSchedule, error) {
	createdAt := sched.CreatedAt
	if createdAt.IsZero() {
		createdAt = AsOfFromContext(ctx)
	}
	row, err := p.q.InsertCategoryFeeSchedule(ctx, db.InsertCategoryFeeScheduleParams{
		ID:                sched.ID,
		CategoryID:        sched.CategoryID,
		CommissionRateBps: sched.CommissionRateBps,
		EffectiveFrom:     sched.EffectiveFrom.UTC(),
		CreatedBy:         sched.CreatedBy,
		Note:              sched.Note,
		CreatedAt:         createdAt.UTC(),
	})
	if err != nil {
		return CategoryFeeSchedule{}, mapPgError(err)
	}
	return fromDBCategoryFeeSchedule(row), nil
}

func (p *pgQueries) ListCategoryFeeSchedules(ctx context.Context, categoryID string, limit, offset int32) ([]CategoryFeeSchedule, int64, error) {
	total, err := p.q.CountCategoryFeeSchedules(ctx, categoryID)
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []CategoryFeeSchedule{}, 0, nil
	}
	rows, err := p.q.ListCategoryFeeSchedules(ctx, db.ListCategoryFeeSchedulesParams{
		CategoryID: categoryID,
		LimitVal:   limit,
		OffsetVal:  offset,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]CategoryFeeSchedule, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBCategoryFeeSchedule(r))
	}
	return out, total, nil
}

func (p *pgQueries) CreateProduct(ctx context.Context, prod Product) (Product, error) {
	imgBytes, err := marshalStringSlice(prod.Images)
	if err != nil {
		return Product{}, err
	}
	row, err := p.q.CreateProduct(ctx, db.CreateProductParams{
		ID:              prod.ID,
		ShopID:          prod.ShopID,
		CategoryID:      prod.CategoryID,
		Name:            prod.Name,
		Description:     prod.Description,
		Images:          imgBytes,
		Status:          prod.Status,
		RejectionReason: prod.RejectionReason,
		MinPriceVnd:     prod.MinPriceVND,
		MaxPriceVnd:     prod.MaxPriceVND,
		Slug:            prod.Slug,
		SearchText:      BuildProductSearchText(prod.Name, prod.Description),
	})
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetProductByID(ctx context.Context, id string) (Product, error) {
	row, err := p.q.GetProductByID(ctx, id)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetProductByIDForUpdate(ctx context.Context, id string) (Product, error) {
	row, err := p.q.GetProductByIDForUpdate(ctx, id)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetProductBySlug(ctx context.Context, slug string) (Product, error) {
	row, err := p.q.GetProductBySlug(ctx, slug)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) UpdateProduct(ctx context.Context, prod Product) (Product, error) {
	imgBytes, err := marshalStringSlice(prod.Images)
	if err != nil {
		return Product{}, err
	}
	row, err := p.q.UpdateProduct(ctx, db.UpdateProductParams{
		ID:              prod.ID,
		CategoryID:      prod.CategoryID,
		Name:            prod.Name,
		Description:     prod.Description,
		Images:          imgBytes,
		Status:          prod.Status,
		RejectionReason: prod.RejectionReason,
		MinPriceVnd:     prod.MinPriceVND,
		MaxPriceVnd:     prod.MaxPriceVND,
		Slug:            prod.Slug,
		SearchText:      BuildProductSearchText(prod.Name, prod.Description),
	})
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) UpdateProductReview(ctx context.Context, id, status, rejectionReason string) (Product, error) {
	row, err := p.q.UpdateProductReview(ctx, db.UpdateProductReviewParams{
		ID:              id,
		Status:          status,
		RejectionReason: rejectionReason,
	})
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) UpdateProductStatus(ctx context.Context, id, status string) error {
	return mapPgError(p.q.UpdateProductStatus(ctx, db.UpdateProductStatusParams{
		ID:     id,
		Status: status,
	}))
}

func (p *pgQueries) SoftDeleteProduct(ctx context.Context, id string) (bool, error) {
	n, err := p.q.SoftDeleteProduct(ctx, id)
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) ListProducts(ctx context.Context, filter ListProductsFilter) ([]Product, int64, error) {
	sqlKeyword := ""
	if strings.TrimSpace(filter.Keyword) != "" {
		norm := NormalizeSearchText(filter.Keyword)
		if norm == "" {
			return []Product{}, 0, nil
		}
		sqlKeyword = EscapeLikePattern(norm)
	}

	total, err := p.q.CountProductsFiltered(ctx, db.CountProductsFilteredParams{
		CategoryID:   filter.CategoryID,
		Keyword:      sqlKeyword,
		ShopID:       filter.ShopID,
		StatusFilter: filter.Status,
		PublicOnly:   filter.PublicOnly,
		MinPriceVnd:  filter.MinPriceVND,
		MaxPriceVnd:  filter.MaxPriceVND,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []Product{}, 0, nil
	}

	rows, err := p.q.ListProductsFiltered(ctx, db.ListProductsFilteredParams{
		CategoryID:   filter.CategoryID,
		Keyword:      sqlKeyword,
		ShopID:       filter.ShopID,
		StatusFilter: filter.Status,
		PublicOnly:   filter.PublicOnly,
		MinPriceVnd:  filter.MinPriceVND,
		MaxPriceVnd:  filter.MaxPriceVND,
		SortBy:       filter.SortBy,
		LimitVal:     filter.Limit,
		OffsetVal:    filter.Offset,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]Product, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBProduct(r))
	}
	return out, total, nil
}

func (p *pgQueries) CreateSKU(ctx context.Context, s SKU) (SKU, error) {
	attrBytes, err := marshalStringMap(s.Attributes)
	if err != nil {
		return SKU{}, err
	}
	row, err := p.q.CreateSKU(ctx, db.CreateSKUParams{
		ID:          s.ID,
		ProductID:   s.ProductID,
		SkuCode:     s.SKUCode,
		Name:        s.Name,
		Attributes:  attrBytes,
		PriceVnd:    s.PriceVND,
		Stock:       s.Stock,
		WeightGrams: s.WeightGrams,
		ImageURL:    s.ImageURL,
		Status:      s.Status,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	out := mergeDBSKU(row, s)
	if out.MinPrice30dVND <= 0 {
		out.MinPrice30dVND = out.PriceVND
	}
	return out, nil
}

func (p *pgQueries) InsertSKUPriceHistory(ctx context.Context, h SKUPriceHistoryEntry) error {
	recordedAt := h.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = AsOfFromContext(ctx)
	}
	return mapPgError(p.q.InsertSKUPriceHistory(ctx, db.InsertSKUPriceHistoryParams{
		ID:         h.ID,
		SkuID:      h.SKUID,
		PriceVnd:   h.PriceVND,
		ActorID:    h.ActorID,
		Reason:     h.Reason,
		RecordedAt: recordedAt.UTC(),
	}))
}

func (p *pgQueries) UpdateSKUMetadata(ctx context.Context, s SKU) (SKU, error) {
	attrBytes, err := marshalStringMap(s.Attributes)
	if err != nil {
		return SKU{}, err
	}
	row, err := p.q.UpdateSKUMetadata(ctx, db.UpdateSKUMetadataParams{
		ID:          s.ID,
		SkuCode:     s.SKUCode,
		Name:        s.Name,
		Attributes:  attrBytes,
		PriceVnd:    s.PriceVND,
		WeightGrams: s.WeightGrams,
		ImageURL:    s.ImageURL,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, s), nil
}

func (p *pgQueries) ReactivateSKUOptimistic(ctx context.Context, s SKU, expectedVersion int64) (SKU, error) {
	attrBytes, err := marshalStringMap(s.Attributes)
	if err != nil {
		return SKU{}, err
	}
	row, err := p.q.ReactivateSKUOptimistic(ctx, db.ReactivateSKUOptimisticParams{
		ID:          s.ID,
		SkuCode:     s.SKUCode,
		Name:        s.Name,
		Attributes:  attrBytes,
		PriceVnd:    s.PriceVND,
		Stock:       s.Stock,
		WeightGrams: s.WeightGrams,
		ImageURL:    s.ImageURL,
		Version:     expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, getErr := p.GetSKUWithContext(ctx, s.ID)
			if getErr != nil {
				return SKU{}, getErr
			}
			if current.Version != expectedVersion {
				return SKU{}, ErrVersionConflict
			}
			if s.Stock < current.ReservedStock {
				return SKU{}, ErrInvalidStock
			}
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, s), nil
}

func (p *pgQueries) DeactivateSKUsByProduct(ctx context.Context, productID string) error {
	return mapPgError(p.q.DeactivateSKUsByProduct(ctx, productID))
}

func (p *pgQueries) DeactivateSKU(ctx context.Context, skuID string) error {
	return mapPgError(p.q.DeactivateSKU(ctx, skuID))
}

func (p *pgQueries) ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]SKU, error) {
	if len(productIDs) == 0 {
		return []SKU{}, nil
	}
	asOf := AsOfFromContext(ctx)
	rows, err := p.q.ListSKUsByProductIDs(ctx, db.ListSKUsByProductIDsParams{
		AsOf:       asOf,
		Cutoff30d:  asOf.Add(-PriceHistoryWindow),
		ProductIds: productIDs,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]SKU, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromSKUContextFields(
			r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
			r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
			r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
			r.SkuStatus, r.ProductStatus, r.ProductDeletedAt,
		))
	}
	return out, nil
}

func (p *pgQueries) ListAllSKUsByProductID(ctx context.Context, productID string) ([]SKU, error) {
	asOf := AsOfFromContext(ctx)
	rows, err := p.q.ListAllSKUsByProductID(ctx, db.ListAllSKUsByProductIDParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		ProductID: productID,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]SKU, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromSKUContextFields(
			r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
			r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
			r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
			r.SkuStatus, r.ProductStatus, r.ProductDeletedAt,
		))
	}
	return out, nil
}

func (p *pgQueries) GetSKUsBatch(ctx context.Context, skuIDs []string) ([]SKU, error) {
	if len(skuIDs) == 0 {
		return []SKU{}, nil
	}
	asOf := AsOfFromContext(ctx)
	rows, err := p.q.GetSKUsBatch(ctx, db.GetSKUsBatchParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		SkuIds:    skuIDs,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]SKU, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromSKUContextFields(
			r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
			r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
			r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
			r.SkuStatus, r.ProductStatus, r.ProductDeletedAt,
		))
	}
	return out, nil
}

func (p *pgQueries) GetSKUWithContext(ctx context.Context, skuID string) (SKU, error) {
	asOf := AsOfFromContext(ctx)
	r, err := p.q.GetSKUWithContext(ctx, db.GetSKUWithContextParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		ID:        skuID,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return fromSKUContextFields(
		r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
		r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
		r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
		r.SkuStatus, r.ProductStatus, r.ProductDeletedAt,
	), nil
}

func (p *pgQueries) GetSKUWithContextForUpdate(ctx context.Context, skuID string) (SKU, error) {
	asOf := AsOfFromContext(ctx)
	r, err := p.q.GetSKUWithContextForUpdate(ctx, db.GetSKUWithContextForUpdateParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		ID:        skuID,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return fromSKUContextFields(
		r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
		r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
		r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
		r.SkuStatus, r.ProductStatus, r.ProductDeletedAt,
	), nil
}

func (p *pgQueries) CountActiveInStockSKUsByProduct(ctx context.Context, productID string) (int64, error) {
	return p.q.CountActiveInStockSKUsByProduct(ctx, productID)
}

func (p *pgQueries) UpdateSKUStockOptimistic(ctx context.Context, skuID string, newStock int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.UpdateSKUStockOptimistic(ctx, db.UpdateSKUStockOptimisticParams{
		Stock:   newStock,
		ID:      skuID,
		Version: expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, getErr := p.GetSKUWithContext(ctx, skuID)
			if getErr != nil {
				return SKU{}, getErr
			}
			if current.Status == SKUStatusInactive {
				return SKU{}, ErrSKUInactive
			}
			if current.Version != expectedVersion {
				return SKU{}, ErrVersionConflict
			}
			if newStock < current.ReservedStock {
				return SKU{}, ErrInvalidStock
			}
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	ctxSKU, err := p.GetSKUWithContext(ctx, row.ID)
	if err != nil {
		return mergeDBSKU(row, SKU{}), nil
	}
	return ctxSKU, nil
}

func (p *pgQueries) ReserveSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.ReserveSKUOptimistic(ctx, db.ReserveSKUOptimisticParams{
		ReservedStock: qty,
		ID:            skuID,
		Version:       expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, getErr := p.GetSKUWithContext(ctx, skuID)
			if getErr != nil {
				return SKU{}, getErr
			}
			if current.AvailableStock() < qty {
				return SKU{}, ErrInsufficientStock
			}
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) ReleaseSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.ReleaseSKUOptimistic(ctx, db.ReleaseSKUOptimisticParams{
		ReservedStock: qty,
		ID:            skuID,
		Version:       expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) ConfirmSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.ConfirmSKUOptimistic(ctx, db.ConfirmSKUOptimisticParams{
		Stock:   qty,
		ID:      skuID,
		Version: expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) RestockSKUAtomic(ctx context.Context, skuID string, qty int32) (SKU, error) {
	row, err := p.q.RestockSKUAtomic(ctx, db.RestockSKUAtomicParams{
		Stock: qty,
		ID:    skuID,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) LockStockOrder(ctx context.Context, orderID string) error {
	return mapPgError(p.q.LockStockOrder(ctx, orderID))
}

func (p *pgQueries) InsertStockReleaseTombstone(ctx context.Context, orderID string) (bool, error) {
	n, err := p.q.InsertStockReleaseTombstone(ctx, orderID)
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) HasStockReleaseTombstone(ctx context.Context, orderID string) (bool, error) {
	exists, err := p.q.HasStockReleaseTombstone(ctx, orderID)
	if err != nil {
		return false, mapPgError(err)
	}
	return exists, nil
}

func (p *pgQueries) ListReservationsByOrderID(ctx context.Context, orderID string, forUpdate bool) ([]StockReservation, error) {
	var (
		rows []db.StockReservation
		err  error
	)
	if forUpdate {
		rows, err = p.q.ListReservationsByOrderIDForUpdate(ctx, orderID)
	} else {
		rows, err = p.q.ListReservationsByOrderID(ctx, orderID)
	}
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]StockReservation, 0, len(rows))
	for _, r := range rows {
		out = append(out, StockReservation{
			ID:                 r.ID,
			ReservationGroupID: r.ReservationGroupID,
			OrderID:            r.OrderID,
			SKUID:              r.SkuID,
			Quantity:           r.Quantity,
			Status:             r.Status,
			CreatedAt:          r.CreatedAt,
			UpdatedAt:          r.UpdatedAt,
		})
	}
	return out, nil
}

func (p *pgQueries) CreateStockReservation(ctx context.Context, r StockReservation) (StockReservation, error) {
	row, err := p.q.CreateStockReservation(ctx, db.CreateStockReservationParams{
		ID:                 r.ID,
		ReservationGroupID: r.ReservationGroupID,
		OrderID:            r.OrderID,
		SkuID:              r.SKUID,
		Quantity:           r.Quantity,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return StockReservation{}, ErrDuplicateReservation
		}
		return StockReservation{}, mapPgError(err)
	}
	return StockReservation{
		ID:                 row.ID,
		ReservationGroupID: row.ReservationGroupID,
		OrderID:            row.OrderID,
		SKUID:              row.SkuID,
		Quantity:           row.Quantity,
		Status:             row.Status,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

func (p *pgQueries) TransitionStockReservation(ctx context.Context, id, toStatus, fromStatus string) (bool, error) {
	n, err := p.q.TransitionStockReservation(ctx, db.TransitionStockReservationParams{
		ID:       id,
		Status:   toStatus,
		Status_2: fromStatus,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) InsertStockHistory(ctx context.Context, h StockHistoryEntry) error {
	return mapPgError(p.q.InsertStockHistory(ctx, db.InsertStockHistoryParams{
		ID:             h.ID,
		SkuID:          h.SKUID,
		OrderID:        h.OrderID,
		ChangeType:     h.ChangeType,
		Delta:          h.Delta,
		StockBefore:    h.StockBefore,
		StockAfter:     h.StockAfter,
		ReservedBefore: h.ReservedBefore,
		ReservedAfter:  h.ReservedAfter,
		ActorID:        h.ActorID,
		Reason:         h.Reason,
	}))
}

func (p *pgQueries) TryInsertRestockHistory(ctx context.Context, h StockHistoryEntry) (bool, error) {
	n, err := p.q.TryInsertRestockHistory(ctx, db.TryInsertRestockHistoryParams{
		ID:             h.ID,
		SkuID:          h.SKUID,
		OrderID:        h.OrderID,
		Delta:          h.Delta,
		StockBefore:    h.StockBefore,
		StockAfter:     h.StockAfter,
		ReservedBefore: h.ReservedBefore,
		ReservedAfter:  h.ReservedAfter,
		ActorID:        h.ActorID,
		Reason:         h.Reason,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) InsertOutboxEvent(ctx context.Context, ev OutboxEvent) error {
	createdAt := ev.CreatedAt
	if createdAt.IsZero() {
		createdAt = AsOfFromContext(ctx)
	}
	return mapPgError(p.q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
		ID:          ev.ID,
		EventType:   ev.EventType,
		TraceID:     ev.TraceID,
		Traceparent: ev.Traceparent,
		PayloadJson: ev.PayloadJSON,
		CreatedAt:   createdAt.UTC(),
	}))
}

func mapPgError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			return ErrAlreadyExists
		case pgForeignKeyViolation:
			return ErrForeignKeyViolation
		}
	}
	return err
}

func toPgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

func fromDBCategory(c db.Category) Category {
	return Category{
		ID:                c.ID,
		Name:              c.Name,
		Slug:              c.Slug,
		ParentID:          c.ParentID.String,
		CommissionRateBps: c.CommissionRateBps,
		SortOrder:         c.SortOrder,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}
}

func fromDBCategoryFeeSchedule(s db.CategoryFeeSchedule) CategoryFeeSchedule {
	return CategoryFeeSchedule{
		ID:                s.ID,
		Seq:               s.Seq,
		CategoryID:        s.CategoryID,
		CommissionRateBps: s.CommissionRateBps,
		EffectiveFrom:     s.EffectiveFrom,
		CreatedBy:         s.CreatedBy,
		Note:              s.Note,
		CreatedAt:         s.CreatedAt,
	}
}

func fromDBOutboxEvent(e db.OutboxEvent) OutboxEvent {
	return OutboxEvent{
		ID:          e.ID,
		EventType:   e.EventType,
		TraceID:     e.TraceID,
		Traceparent: e.Traceparent,
		PayloadJSON: append([]byte(nil), e.PayloadJson...),
		PublishedAt: e.PublishedAt,
		Attempts:    e.Attempts,
		LastError:   e.LastError,
		CreatedAt:   e.CreatedAt,
	}
}

func fromDBProduct(p db.Product) Product {
	return Product{
		ID:              p.ID,
		ShopID:          p.ShopID,
		CategoryID:      p.CategoryID,
		Name:            p.Name,
		Slug:            p.Slug,
		SearchText:      p.SearchText,
		Description:     p.Description,
		Images:          unmarshalStringSlice(p.Images),
		Status:          p.Status,
		RejectionReason: p.RejectionReason,
		MinPriceVND:     p.MinPriceVnd,
		MaxPriceVND:     p.MaxPriceVnd,
		DeletedAt:       p.DeletedAt,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
}

func mergeDBSKU(row db.Sku, base SKU) SKU {
	base.ID = row.ID
	base.ProductID = row.ProductID
	base.SKUCode = row.SkuCode
	base.Name = row.Name
	base.Attributes = unmarshalStringMap(row.Attributes)
	base.PriceVND = row.PriceVnd
	base.Stock = row.Stock
	base.ReservedStock = row.ReservedStock
	base.Version = row.Version
	base.WeightGrams = row.WeightGrams
	base.ImageURL = row.ImageURL
	base.Status = row.Status
	base.CreatedAt = row.CreatedAt
	base.UpdatedAt = row.UpdatedAt
	return base
}

func fromSKUContextFields(
	id, productID, shopID, productName, skuCode, name string,
	attrs []byte, priceVND, minPrice30dVND int64, stock, reservedStock int32, version int64,
	weightGrams int32, imageURL, categoryID string, commissionRateBps int32,
	skuStatus, productStatus string, productDeletedAt *time.Time,
) SKU {
	if minPrice30dVND <= 0 {
		minPrice30dVND = priceVND
	}
	return SKU{
		ID:                id,
		ProductID:         productID,
		ShopID:            shopID,
		ProductName:       productName,
		SKUCode:           skuCode,
		Name:              name,
		Attributes:        unmarshalStringMap(attrs),
		PriceVND:          priceVND,
		MinPrice30dVND:    minPrice30dVND,
		Stock:             stock,
		ReservedStock:     reservedStock,
		Version:           version,
		WeightGrams:       weightGrams,
		ImageURL:          imageURL,
		CategoryID:        categoryID,
		CommissionRateBps: commissionRateBps,
		Status:            skuStatus,
		ProductStatus:     productStatus,
		ProductDeleted:    productDeletedAt != nil,
	}
}

func marshalStringSlice(in []string) ([]byte, error) {
	if in == nil {
		in = []string{}
	}
	return json.Marshal(in)
}

func unmarshalStringSlice(b []byte) []string {
	var out []string
	if len(b) == 0 {
		return []string{}
	}
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func marshalStringMap(in map[string]string) ([]byte, error) {
	if in == nil {
		in = map[string]string{}
	}
	return json.Marshal(in)
}

func unmarshalStringMap(b []byte) map[string]string {
	out := map[string]string{}
	if len(b) == 0 {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}
