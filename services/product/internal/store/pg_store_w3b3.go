package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nexusmart/core/services/product/internal/db"
)

func (p *pgQueries) GetCategoryAncestors(ctx context.Context, categoryID string) ([]string, error) {
	ids, err := p.q.GetCategoryAncestors(ctx, categoryID)
	if err != nil {
		return nil, mapPgError(err)
	}
	if ids == nil {
		return []string{}, nil
	}
	return ids, nil
}

func (p *pgQueries) GetProductShopIDAny(ctx context.Context, id string) (string, error) {
	shopID, err := p.q.GetProductShopIDAny(ctx, id)
	if err != nil {
		return "", mapPgError(err)
	}
	return shopID, nil
}

func (p *pgQueries) ListProductsForIndex(ctx context.Context, afterID string, limit int32) ([]Product, error) {
	rows, err := p.q.ListProductsForIndex(ctx, db.ListProductsForIndexParams{
		AfterID:  afterID,
		LimitVal: limit,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]Product, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBProduct(r))
	}
	return out, nil
}

func (p *pgQueries) ListStockHistory(ctx context.Context, filter ListStockHistoryFilter) ([]StockHistoryRecord, int64, error) {
	var (
		hasFrom bool
		fromTs  time.Time
		hasTo   bool
		toTs    time.Time
	)
	if filter.FromTime != nil && !filter.FromTime.IsZero() {
		hasFrom = true
		fromTs = filter.FromTime.UTC()
	}
	if filter.ToTime != nil && !filter.ToTime.IsZero() {
		hasTo = true
		toTs = filter.ToTime.UTC()
	}

	total, err := p.q.CountStockHistory(ctx, db.CountStockHistoryParams{
		ShopID:      filter.ShopID,
		ProductID:   filter.ProductID,
		SkuID:       filter.SKUID,
		ChangeType:  filter.ChangeType,
		HasFromTime: hasFrom,
		FromTime:    fromTs,
		HasToTime:   hasTo,
		ToTime:      toTs,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []StockHistoryRecord{}, 0, nil
	}

	rows, err := p.q.ListStockHistory(ctx, db.ListStockHistoryParams{
		ShopID:      filter.ShopID,
		ProductID:   filter.ProductID,
		SkuID:       filter.SKUID,
		ChangeType:  filter.ChangeType,
		HasFromTime: hasFrom,
		FromTime:    fromTs,
		HasToTime:   hasTo,
		ToTime:      toTs,
		LimitVal:    filter.Limit,
		OffsetVal:   filter.Offset,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]StockHistoryRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, StockHistoryRecord{
			ID:             r.ID,
			SKUID:          r.SkuID,
			ProductID:      r.ProductID,
			ShopID:         r.ShopID,
			ProductName:    r.ProductName,
			SKUCode:        r.SkuCode,
			SKUName:        r.SkuName,
			OrderID:        r.OrderID,
			ChangeType:     r.ChangeType,
			Delta:          r.Delta,
			StockBefore:    r.StockBefore,
			StockAfter:     r.StockAfter,
			ReservedBefore: r.ReservedBefore,
			ReservedAfter:  r.ReservedAfter,
			ActorID:        r.ActorID,
			Reason:         r.Reason,
			CreatedAt:      r.CreatedAt,
		})
	}
	return out, total, nil
}

func (p *pgQueries) ListExistingShopSKUCodes(ctx context.Context, shopID string, skuCodes []string) (map[string]bool, error) {
	if len(skuCodes) == 0 {
		return map[string]bool{}, nil
	}
	rows, err := p.q.ListExistingShopSKUCodes(ctx, db.ListExistingShopSKUCodesParams{
		ShopID:   shopID,
		SkuCodes: skuCodes,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make(map[string]bool, len(rows))
	for _, code := range rows {
		out[code] = true
	}
	return out, nil
}

func (p *pgQueries) CreateProductImportJob(ctx context.Context, job ProductImportJob) (ProductImportJob, error) {
	createdAt := job.CreatedAt
	if createdAt.IsZero() {
		createdAt = AsOfFromContext(ctx)
	}
	row, err := p.q.CreateProductImportJob(ctx, db.CreateProductImportJobParams{
		ID:        job.ID,
		ShopID:    job.ShopID,
		CreatedBy: job.CreatedBy,
		Filename:  job.Filename,
		RawCsv:    job.RawCSV,
		TotalRows: job.TotalRows,
		CreatedAt: createdAt.UTC(),
	})
	if err != nil {
		return ProductImportJob{}, mapPgError(err)
	}
	return fromDBProductImportJob(row), nil
}

func (p *pgQueries) HasActiveProductImportJob(ctx context.Context, shopID, excludeJobID string) (bool, error) {
	active, err := p.q.HasActiveProductImportJob(ctx, db.HasActiveProductImportJobParams{
		ShopID:       shopID,
		ExcludeJobID: excludeJobID,
	})
	return active, mapPgError(err)
}

func (p *pgQueries) GetProductImportJobByID(ctx context.Context, id string) (ProductImportJob, error) {
	row, err := p.q.GetProductImportJobByID(ctx, id)
	if err != nil {
		return ProductImportJob{}, mapPgError(err)
	}
	return fromDBProductImportJob(row), nil
}

func (p *pgQueries) GetProductImportJobByIDForUpdate(ctx context.Context, id string) (ProductImportJob, error) {
	row, err := p.q.GetProductImportJobByIDForUpdate(ctx, id)
	if err != nil {
		return ProductImportJob{}, mapPgError(err)
	}
	return fromDBProductImportJob(row), nil
}

func (p *pgQueries) ClaimNextProductImportJob(ctx context.Context, claimToken string, lease time.Duration) (ProductImportJob, bool, error) {
	ms := lease.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	row, err := p.q.ClaimNextProductImportJob(ctx, db.ClaimNextProductImportJobParams{
		LeaseMs:    ms,
		ClaimToken: claimToken,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductImportJob{}, false, nil
		}
		return ProductImportJob{}, false, mapPgError(err)
	}
	return fromDBProductImportJob(row), true, nil
}

func (p *pgQueries) MarkProductImportJobReady(ctx context.Context, id, claimToken string, totalRows, validRows, invalidRows int32) (ProductImportJob, bool, error) {
	row, err := p.q.MarkProductImportJobReady(ctx, db.MarkProductImportJobReadyParams{
		ID:          id,
		ClaimToken:  claimToken,
		TotalRows:   totalRows,
		ValidRows:   validRows,
		InvalidRows: invalidRows,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductImportJob{}, false, nil
		}
		return ProductImportJob{}, false, mapPgError(err)
	}
	return fromDBProductImportJob(row), true, nil
}

func (p *pgQueries) MarkProductImportJobFailed(ctx context.Context, id, claimToken, errMsg string) (ProductImportJob, bool, error) {
	row, err := p.q.MarkProductImportJobFailed(ctx, db.MarkProductImportJobFailedParams{
		ID:           id,
		ClaimToken:   claimToken,
		ErrorMessage: errMsg,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductImportJob{}, false, nil
		}
		return ProductImportJob{}, false, mapPgError(err)
	}
	return fromDBProductImportJob(row), true, nil
}

func (p *pgQueries) TransitionProductImportJobConfirming(ctx context.Context, id string) (ProductImportJob, bool, error) {
	row, err := p.q.TransitionProductImportJobConfirming(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductImportJob{}, false, nil
		}
		return ProductImportJob{}, false, mapPgError(err)
	}
	return fromDBProductImportJob(row), true, nil
}

func (p *pgQueries) UpdateProductImportJobProgress(ctx context.Context, id, claimToken string, createdDelta, failedDelta int32, lease time.Duration) (bool, error) {
	ms := lease.Milliseconds()
	if ms <= 0 {
		ms = 1
	}
	n, err := p.q.UpdateProductImportJobProgress(ctx, db.UpdateProductImportJobProgressParams{
		CreatedDelta: createdDelta,
		FailedDelta:  failedDelta,
		LeaseMs:      ms,
		ID:           id,
		ClaimToken:   claimToken,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) MarkProductImportJobCompleted(ctx context.Context, id, claimToken string, createdRows, failedRows int32) (ProductImportJob, bool, error) {
	row, err := p.q.MarkProductImportJobCompleted(ctx, db.MarkProductImportJobCompletedParams{
		ID:          id,
		ClaimToken:  claimToken,
		CreatedRows: createdRows,
		FailedRows:  failedRows,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductImportJob{}, false, nil
		}
		return ProductImportJob{}, false, mapPgError(err)
	}
	return fromDBProductImportJob(row), true, nil
}

func (p *pgQueries) DeleteProductImportRowsByJob(ctx context.Context, jobID string) error {
	return mapPgError(p.q.DeleteProductImportRowsByJob(ctx, jobID))
}

func (p *pgQueries) InsertProductImportRow(ctx context.Context, row ProductImportRow) error {
	raw := row.RawRowJSON
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return mapPgError(p.q.InsertProductImportRow(ctx, db.InsertProductImportRowParams{
		ID:               row.ID,
		JobID:            row.JobID,
		RowNumber:        row.RowNumber,
		Status:           row.Status,
		ErrorCode:        row.ErrorCode,
		ErrorMessage:     row.ErrorMessage,
		ProductName:      row.ProductName,
		SkuCode:          row.SKUCode,
		CategoryID:       row.CategoryID,
		PriceVnd:         row.PriceVND,
		Stock:            row.Stock,
		WeightGrams:      row.WeightGrams,
		CreatedProductID: row.CreatedProductID,
		RawRowJson:       raw,
	}))
}

func (p *pgQueries) ListProductImportRows(ctx context.Context, filter ListProductImportRowsFilter) ([]ProductImportRow, int64, error) {
	total, err := p.q.CountProductImportRows(ctx, db.CountProductImportRowsParams{
		JobID:        filter.JobID,
		ErrorsOnly:   filter.ErrorsOnly,
		StatusFilter: filter.Status,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []ProductImportRow{}, 0, nil
	}
	rows, err := p.q.ListProductImportRows(ctx, db.ListProductImportRowsParams{
		JobID:        filter.JobID,
		ErrorsOnly:   filter.ErrorsOnly,
		StatusFilter: filter.Status,
		LimitVal:     filter.Limit,
		OffsetVal:    filter.Offset,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]ProductImportRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBProductImportRow(r))
	}
	return out, total, nil
}

func (p *pgQueries) ListValidProductImportRows(ctx context.Context, jobID string) ([]ProductImportRow, error) {
	rows, err := p.q.ListValidProductImportRows(ctx, jobID)
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]ProductImportRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBProductImportRow(r))
	}
	return out, nil
}

func (p *pgQueries) GetProductImportRowByIDForUpdate(ctx context.Context, id string) (ProductImportRow, error) {
	row, err := p.q.GetProductImportRowByIDForUpdate(ctx, id)
	if err != nil {
		return ProductImportRow{}, mapPgError(err)
	}
	return fromDBProductImportRow(row), nil
}

func (p *pgQueries) UpdateProductImportRowOutcome(ctx context.Context, id, status, errCode, errMsg, createdProductID string) (bool, error) {
	n, err := p.q.UpdateProductImportRowOutcome(ctx, db.UpdateProductImportRowOutcomeParams{
		ID:               id,
		Status:           status,
		ErrorCode:        errCode,
		ErrorMessage:     errMsg,
		CreatedProductID: createdProductID,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) GetProductImportRowOutcomeCounts(ctx context.Context, jobID string) (ImportRowOutcomeCounts, error) {
	row, err := p.q.GetProductImportRowOutcomeCounts(ctx, jobID)
	if err != nil {
		return ImportRowOutcomeCounts{}, mapPgError(err)
	}
	return ImportRowOutcomeCounts{
		RemainingValid: row.RemainingValid,
		Created:        row.CreatedCnt,
		Failed:         row.FailedCnt,
	}, nil
}

func fromDBProductImportJob(j db.ProductImportJob) ProductImportJob {
	var rawCopy []byte
	if j.RawCsv != nil {
		rawCopy = append([]byte(nil), j.RawCsv...)
	}
	return ProductImportJob{
		ID:           j.ID,
		ShopID:       j.ShopID,
		CreatedBy:    j.CreatedBy,
		Filename:     j.Filename,
		RawCSV:       rawCopy,
		Status:       j.Status,
		TotalRows:    j.TotalRows,
		ValidRows:    j.ValidRows,
		InvalidRows:  j.InvalidRows,
		CreatedRows:  j.CreatedRows,
		FailedRows:   j.FailedRows,
		ErrorMessage: j.ErrorMessage,
		ClaimedUntil: j.ClaimedUntil,
		ClaimToken:   j.ClaimToken,
		ConfirmedAt:  j.ConfirmedAt,
		FinishedAt:   j.FinishedAt,
		CreatedAt:    j.CreatedAt,
		UpdatedAt:    j.UpdatedAt,
	}
}

func fromDBProductImportRow(r db.ProductImportRow) ProductImportRow {
	rawCopy := append([]byte(nil), r.RawRowJson...)
	return ProductImportRow{
		ID:               r.ID,
		JobID:            r.JobID,
		RowNumber:        r.RowNumber,
		Status:           r.Status,
		ErrorCode:        r.ErrorCode,
		ErrorMessage:     r.ErrorMessage,
		ProductName:      r.ProductName,
		SKUCode:          r.SkuCode,
		CategoryID:       r.CategoryID,
		PriceVND:         r.PriceVnd,
		Stock:            r.Stock,
		WeightGrams:      r.WeightGrams,
		CreatedProductID: r.CreatedProductID,
		RawRowJSON:       rawCopy,
	}
}
