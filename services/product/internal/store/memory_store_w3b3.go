package store

import (
	"context"
	"sort"
	"sync"
	"time"
)

func monotonicTimestamp(now, prev time.Time) time.Time {
	now = now.UTC()
	if !prev.IsZero() && !now.After(prev) {
		return prev.UTC().Add(time.Microsecond)
	}
	return now
}

func (m *MemoryStore) getJobLock(jobID string) *sync.Mutex {
	m.jobLocksMu.Lock()
	defer m.jobLocksMu.Unlock()
	mu, ok := m.jobLocks[jobID]
	if !ok {
		mu = &sync.Mutex{}
		m.jobLocks[jobID] = mu
	}
	return mu
}

func (t *memTx) unlockJobs() {
	for i := len(t.lockedJobs) - 1; i >= 0; i-- {
		t.store.getJobLock(t.lockedJobs[i]).Unlock()
	}
	t.lockedJobs = nil
	t.lockedJobSet = nil
}

func (t *memTx) lockJob(jobID string) {
	if jobID == "" || t.lockedJobSet[jobID] {
		return
	}
	t.store.getJobLock(jobID).Lock()
	t.lockedJobSet[jobID] = true
	t.lockedJobs = append(t.lockedJobs, jobID)
}

func cloneImportJob(j ProductImportJob) ProductImportJob {
	if j.RawCSV != nil {
		j.RawCSV = append([]byte(nil), j.RawCSV...)
	}
	if j.ClaimedUntil != nil {
		ts := *j.ClaimedUntil
		j.ClaimedUntil = &ts
	}
	if j.ConfirmedAt != nil {
		ts := *j.ConfirmedAt
		j.ConfirmedAt = &ts
	}
	if j.FinishedAt != nil {
		ts := *j.FinishedAt
		j.FinishedAt = &ts
	}
	return j
}

func cloneImportRow(r ProductImportRow) ProductImportRow {
	if r.RawRowJSON != nil {
		r.RawRowJSON = append([]byte(nil), r.RawRowJSON...)
	}
	return r
}

func categoryAncestorsInMap(cats map[string]Category, categoryID string) []string {
	out := make([]string, 0, 4)
	visited := make(map[string]bool)
	cur := categoryID
	for cur != "" && len(out) < 32 {
		if visited[cur] {
			break
		}
		c, ok := cats[cur]
		if !ok {
			break
		}
		visited[cur] = true
		out = append(out, c.ID)
		cur = c.ParentID
	}
	return out
}

// --- MemoryStore direct methods ---

func (m *MemoryStore) GetCategoryAncestors(_ context.Context, categoryID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return categoryAncestorsInMap(m.categories, categoryID), nil
}

func (m *MemoryStore) GetProductShopIDAny(_ context.Context, id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.products[id]
	if !ok {
		return "", ErrNotFound
	}
	return p.ShopID, nil
}

func (m *MemoryStore) ListProductsForIndex(_ context.Context, afterID string, limit int32) ([]Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	matched := make([]Product, 0)
	for _, p := range m.products {
		if p.DeletedAt != nil {
			continue
		}
		if p.Status != ProductStatusApproved && p.Status != ProductStatusOutOfStock {
			continue
		}
		if afterID != "" && p.ID <= afterID {
			continue
		}
		matched = append(matched, cloneProduct(p))
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].ID < matched[j].ID
	})
	if limit > 0 && int(limit) < len(matched) {
		matched = matched[:limit]
	}
	return matched, nil
}

func (m *MemoryStore) ListStockHistory(_ context.Context, filter ListStockHistoryFilter) ([]StockHistoryRecord, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	matched := make([]StockHistoryRecord, 0)
	for _, h := range m.history {
		sk, ok := m.skus[h.SKUID]
		if !ok {
			continue
		}
		p, ok := m.products[sk.ProductID]
		if !ok {
			continue
		}
		if filter.ShopID != "" && p.ShopID != filter.ShopID {
			continue
		}
		if filter.ProductID != "" && sk.ProductID != filter.ProductID {
			continue
		}
		if filter.SKUID != "" && h.SKUID != filter.SKUID {
			continue
		}
		if filter.ChangeType != "" && h.ChangeType != filter.ChangeType {
			continue
		}
		if filter.FromTime != nil && !filter.FromTime.IsZero() && h.CreatedAt.Before(filter.FromTime.UTC()) {
			continue
		}
		if filter.ToTime != nil && !filter.ToTime.IsZero() && h.CreatedAt.After(filter.ToTime.UTC()) {
			continue
		}
		matched = append(matched, StockHistoryRecord{
			ID:             h.ID,
			SKUID:          h.SKUID,
			ProductID:      sk.ProductID,
			ShopID:         p.ShopID,
			ProductName:    p.Name,
			SKUCode:        sk.SKUCode,
			SKUName:        sk.Name,
			OrderID:        h.OrderID,
			ChangeType:     h.ChangeType,
			Delta:          h.Delta,
			StockBefore:    h.StockBefore,
			StockAfter:     h.StockAfter,
			ReservedBefore: h.ReservedBefore,
			ReservedAfter:  h.ReservedAfter,
			ActorID:        h.ActorID,
			Reason:         h.Reason,
			CreatedAt:      h.CreatedAt,
		})
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.After(matched[j].CreatedAt)
		}
		return matched[i].ID > matched[j].ID
	})
	total := int64(len(matched))
	start := int(filter.Offset)
	if start >= len(matched) {
		return []StockHistoryRecord{}, total, nil
	}
	end := len(matched)
	if filter.Limit > 0 && start+int(filter.Limit) < end {
		end = start + int(filter.Limit)
	}
	return matched[start:end], total, nil
}

func (m *MemoryStore) ListExistingShopSKUCodes(_ context.Context, shopID string, skuCodes []string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(skuCodes) == 0 {
		return map[string]bool{}, nil
	}
	want := make(map[string]bool, len(skuCodes))
	for _, c := range skuCodes {
		want[c] = true
	}
	found := make(map[string]bool)
	for _, sk := range m.skus {
		if sk.Status == SKUStatusInactive || !want[sk.SKUCode] {
			continue
		}
		p, ok := m.products[sk.ProductID]
		if !ok || p.DeletedAt != nil || p.ShopID != shopID {
			continue
		}
		found[sk.SKUCode] = true
	}
	return found, nil
}

func (m *MemoryStore) CreateProductImportJob(ctx context.Context, job ProductImportJob) (ProductImportJob, error) {
	var created ProductImportJob
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		created, e = q.CreateProductImportJob(ctx, job)
		return e
	})
	return created, err
}

func isActiveImportJobStatus(status string) bool {
	return status == ImportJobStatusQueued || status == ImportJobStatusValidating || status == ImportJobStatusConfirming
}

func (m *MemoryStore) HasActiveProductImportJob(_ context.Context, shopID, excludeJobID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.importJobs {
		if j.ShopID == shopID && (excludeJobID == "" || j.ID != excludeJobID) && isActiveImportJobStatus(j.Status) {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryStore) GetProductImportJobByID(_ context.Context, id string) (ProductImportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.importJobs[id]
	if !ok {
		return ProductImportJob{}, ErrNotFound
	}
	return cloneImportJob(j), nil
}

func (m *MemoryStore) GetProductImportJobByIDForUpdate(ctx context.Context, id string) (ProductImportJob, error) {
	return m.GetProductImportJobByID(ctx, id)
}

func (m *MemoryStore) ClaimNextProductImportJob(ctx context.Context, claimToken string, lease time.Duration) (ProductImportJob, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := AsOfFromContext(ctx)
	if lease <= 0 {
		lease = time.Millisecond
	}
	candidates := make([]ProductImportJob, 0)
	for _, j := range m.importJobs {
		if !isActiveImportJobStatus(j.Status) {
			continue
		}
		if j.ClaimedUntil != nil && j.ClaimedUntil.After(now) {
			continue
		}
		candidates = append(candidates, j)
	}
	if len(candidates) == 0 {
		return ProductImportJob{}, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return candidates[i].ID < candidates[j].ID
	})
	j := cloneImportJob(candidates[0])
	if j.Status == ImportJobStatusQueued {
		j.Status = ImportJobStatusValidating
	}
	until := now.Add(lease)
	j.ClaimedUntil = &until
	j.ClaimToken = claimToken
	j.UpdatedAt = now
	m.importJobs[j.ID] = cloneImportJob(j)
	return j, true, nil
}

func (m *MemoryStore) MarkProductImportJobReady(ctx context.Context, id, claimToken string, totalRows, validRows, invalidRows int32) (ProductImportJob, bool, error) {
	var (
		job ProductImportJob
		ok  bool
	)
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		job, ok, e = q.MarkProductImportJobReady(ctx, id, claimToken, totalRows, validRows, invalidRows)
		return e
	})
	return job, ok, err
}

func (m *MemoryStore) MarkProductImportJobFailed(ctx context.Context, id, claimToken, errMsg string) (ProductImportJob, bool, error) {
	var (
		job ProductImportJob
		ok  bool
	)
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		job, ok, e = q.MarkProductImportJobFailed(ctx, id, claimToken, errMsg)
		return e
	})
	return job, ok, err
}

func (m *MemoryStore) TransitionProductImportJobConfirming(ctx context.Context, id string) (ProductImportJob, bool, error) {
	var (
		job ProductImportJob
		ok  bool
	)
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		job, ok, e = q.TransitionProductImportJobConfirming(ctx, id)
		return e
	})
	return job, ok, err
}

func (m *MemoryStore) UpdateProductImportJobProgress(ctx context.Context, id, claimToken string, createdDelta, failedDelta int32, lease time.Duration) (bool, error) {
	var ok bool
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		ok, e = q.UpdateProductImportJobProgress(ctx, id, claimToken, createdDelta, failedDelta, lease)
		return e
	})
	return ok, err
}

func (m *MemoryStore) MarkProductImportJobCompleted(ctx context.Context, id, claimToken string, createdRows, failedRows int32) (ProductImportJob, bool, error) {
	var (
		job ProductImportJob
		ok  bool
	)
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		job, ok, e = q.MarkProductImportJobCompleted(ctx, id, claimToken, createdRows, failedRows)
		return e
	})
	return job, ok, err
}

func (m *MemoryStore) DeleteProductImportRowsByJob(ctx context.Context, jobID string) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.DeleteProductImportRowsByJob(ctx, jobID)
	})
}

func (m *MemoryStore) InsertProductImportRow(ctx context.Context, row ProductImportRow) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.InsertProductImportRow(ctx, row)
	})
}

func (m *MemoryStore) ListProductImportRows(_ context.Context, filter ListProductImportRowsFilter) ([]ProductImportRow, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	matched := make([]ProductImportRow, 0)
	for _, r := range m.importRows {
		if r.JobID != filter.JobID {
			continue
		}
		if filter.ErrorsOnly && r.Status != ImportRowStatusInvalid && r.Status != ImportRowStatusFailed {
			continue
		}
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		matched = append(matched, cloneImportRow(r))
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].RowNumber < matched[j].RowNumber
	})
	total := int64(len(matched))
	start := int(filter.Offset)
	if start >= len(matched) {
		return []ProductImportRow{}, total, nil
	}
	end := len(matched)
	if filter.Limit > 0 && start+int(filter.Limit) < end {
		end = start + int(filter.Limit)
	}
	return matched[start:end], total, nil
}

func (m *MemoryStore) ListValidProductImportRows(_ context.Context, jobID string) ([]ProductImportRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	matched := make([]ProductImportRow, 0)
	for _, r := range m.importRows {
		if r.JobID == jobID && r.Status == ImportRowStatusValid {
			matched = append(matched, cloneImportRow(r))
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		return matched[i].RowNumber < matched[j].RowNumber
	})
	return matched, nil
}

func (m *MemoryStore) GetProductImportRowByIDForUpdate(_ context.Context, id string) (ProductImportRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.importRows[id]
	if !ok {
		return ProductImportRow{}, ErrNotFound
	}
	return cloneImportRow(r), nil
}

func (m *MemoryStore) UpdateProductImportRowOutcome(ctx context.Context, id, status, errCode, errMsg, createdProductID string) (bool, error) {
	var ok bool
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		ok, e = q.UpdateProductImportRowOutcome(ctx, id, status, errCode, errMsg, createdProductID)
		return e
	})
	return ok, err
}

func (m *MemoryStore) GetProductImportRowOutcomeCounts(_ context.Context, jobID string) (ImportRowOutcomeCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var counts ImportRowOutcomeCounts
	for _, r := range m.importRows {
		if r.JobID != jobID {
			continue
		}
		switch r.Status {
		case ImportRowStatusValid:
			counts.RemainingValid++
		case ImportRowStatusCreated:
			counts.Created++
		case ImportRowStatusFailed:
			counts.Failed++
		}
	}
	return counts, nil
}

// --- memTx methods ---

func (t *memTx) GetCategoryAncestors(_ context.Context, categoryID string) ([]string, error) {
	t.store.mu.Lock()
	merged := make(map[string]Category, len(t.store.categories)+len(t.stagedCats))
	for id, c := range t.store.categories {
		if !t.deletedCats[id] {
			merged[id] = c
		}
	}
	t.store.mu.Unlock()
	for id, c := range t.stagedCats {
		merged[id] = c
	}
	return categoryAncestorsInMap(merged, categoryID), nil
}

func (t *memTx) GetProductShopIDAny(_ context.Context, id string) (string, error) {
	if p, ok := t.stagedProds[id]; ok {
		return p.ShopID, nil
	}
	return t.store.GetProductShopIDAny(context.Background(), id)
}

func (t *memTx) ListProductsForIndex(ctx context.Context, afterID string, limit int32) ([]Product, error) {
	return t.store.ListProductsForIndex(ctx, afterID, limit)
}

func (t *memTx) ListStockHistory(ctx context.Context, filter ListStockHistoryFilter) ([]StockHistoryRecord, int64, error) {
	return t.store.ListStockHistory(ctx, filter)
}

func (t *memTx) ListExistingShopSKUCodes(_ context.Context, shopID string, skuCodes []string) (map[string]bool, error) {
	if len(skuCodes) == 0 {
		return map[string]bool{}, nil
	}
	want := make(map[string]bool, len(skuCodes))
	for _, c := range skuCodes {
		want[c] = true
	}
	t.store.mu.Lock()
	mergedProds := make(map[string]Product, len(t.store.products)+len(t.stagedProds))
	for id, p := range t.store.products {
		mergedProds[id] = p
	}
	mergedSKUs := make(map[string]SKU, len(t.store.skus)+len(t.stagedSKUs))
	for id, sk := range t.store.skus {
		mergedSKUs[id] = sk
	}
	t.store.mu.Unlock()
	for id, p := range t.stagedProds {
		mergedProds[id] = p
	}
	for id, sk := range t.stagedSKUs {
		mergedSKUs[id] = sk
	}
	found := make(map[string]bool)
	for _, sk := range mergedSKUs {
		if sk.Status == SKUStatusInactive || !want[sk.SKUCode] {
			continue
		}
		p, ok := mergedProds[sk.ProductID]
		if !ok || p.DeletedAt != nil || p.ShopID != shopID {
			continue
		}
		found[sk.SKUCode] = true
	}
	return found, nil
}

func (t *memTx) CreateProductImportJob(ctx context.Context, job ProductImportJob) (ProductImportJob, error) {
	now := job.CreatedAt
	if now.IsZero() {
		now = AsOfFromContext(ctx)
	}
	job.Status = ImportJobStatusQueued
	job.CreatedAt = now.UTC()
	job.UpdatedAt = now.UTC()
	t.stagedImportJobs[job.ID] = cloneImportJob(job)
	return cloneImportJob(job), nil
}

func (t *memTx) HasActiveProductImportJob(_ context.Context, shopID, excludeJobID string) (bool, error) {
	t.store.mu.Lock()
	for _, j := range t.store.importJobs {
		if staged, ok := t.stagedImportJobs[j.ID]; ok {
			j = staged
		}
		if j.ShopID == shopID && (excludeJobID == "" || j.ID != excludeJobID) && isActiveImportJobStatus(j.Status) {
			t.store.mu.Unlock()
			return true, nil
		}
	}
	t.store.mu.Unlock()
	for _, j := range t.stagedImportJobs {
		if j.ShopID == shopID && (excludeJobID == "" || j.ID != excludeJobID) && isActiveImportJobStatus(j.Status) {
			return true, nil
		}
	}
	return false, nil
}

func (t *memTx) GetProductImportJobByID(_ context.Context, id string) (ProductImportJob, error) {
	if j, ok := t.stagedImportJobs[id]; ok {
		return cloneImportJob(j), nil
	}
	return t.store.GetProductImportJobByID(context.Background(), id)
}

func (t *memTx) GetProductImportJobByIDForUpdate(ctx context.Context, id string) (ProductImportJob, error) {
	t.lockJob(id)
	return t.GetProductImportJobByID(ctx, id)
}

func (t *memTx) ClaimNextProductImportJob(ctx context.Context, claimToken string, lease time.Duration) (ProductImportJob, bool, error) {
	return t.store.ClaimNextProductImportJob(ctx, claimToken, lease)
}

func (t *memTx) MarkProductImportJobReady(ctx context.Context, id, claimToken string, totalRows, validRows, invalidRows int32) (ProductImportJob, bool, error) {
	t.lockJob(id)
	cur, err := t.GetProductImportJobByID(ctx, id)
	if err != nil {
		return ProductImportJob{}, false, nil
	}
	if cur.Status != ImportJobStatusValidating || cur.ClaimToken != claimToken {
		return ProductImportJob{}, false, nil
	}
	now := AsOfFromContext(ctx)
	cur.Status = ImportJobStatusReady
	cur.TotalRows = totalRows
	cur.ValidRows = validRows
	cur.InvalidRows = invalidRows
	cur.RawCSV = nil
	cur.ClaimedUntil = nil
	cur.ClaimToken = ""
	cur.ErrorMessage = ""
	cur.UpdatedAt = now
	t.stagedImportJobs[id] = cloneImportJob(cur)
	return cloneImportJob(cur), true, nil
}

func (t *memTx) MarkProductImportJobFailed(ctx context.Context, id, claimToken, errMsg string) (ProductImportJob, bool, error) {
	t.lockJob(id)
	cur, err := t.GetProductImportJobByID(ctx, id)
	if err != nil {
		return ProductImportJob{}, false, nil
	}
	if (cur.Status != ImportJobStatusValidating && cur.Status != ImportJobStatusConfirming) || cur.ClaimToken != claimToken {
		return ProductImportJob{}, false, nil
	}
	now := AsOfFromContext(ctx)
	cur.Status = ImportJobStatusFailed
	cur.ErrorMessage = errMsg
	cur.RawCSV = nil
	cur.ClaimedUntil = nil
	cur.ClaimToken = ""
	cur.FinishedAt = &now
	cur.UpdatedAt = now
	t.stagedImportJobs[id] = cloneImportJob(cur)
	return cloneImportJob(cur), true, nil
}

func (t *memTx) TransitionProductImportJobConfirming(ctx context.Context, id string) (ProductImportJob, bool, error) {
	t.lockJob(id)
	cur, err := t.GetProductImportJobByID(ctx, id)
	if err != nil {
		return ProductImportJob{}, false, nil
	}
	if cur.Status != ImportJobStatusReady || cur.ValidRows <= 0 {
		return ProductImportJob{}, false, nil
	}
	now := AsOfFromContext(ctx)
	cur.Status = ImportJobStatusConfirming
	cur.ConfirmedAt = &now
	cur.ClaimedUntil = nil
	cur.ClaimToken = ""
	cur.UpdatedAt = now
	t.stagedImportJobs[id] = cloneImportJob(cur)
	return cloneImportJob(cur), true, nil
}

func (t *memTx) UpdateProductImportJobProgress(ctx context.Context, id, claimToken string, createdDelta, failedDelta int32, lease time.Duration) (bool, error) {
	t.lockJob(id)
	cur, err := t.GetProductImportJobByID(ctx, id)
	if err != nil {
		return false, nil
	}
	if cur.Status != ImportJobStatusConfirming || cur.ClaimToken != claimToken {
		return false, nil
	}
	now := AsOfFromContext(ctx)
	if lease <= 0 {
		lease = time.Millisecond
	}
	until := now.Add(lease)
	cur.CreatedRows += createdDelta
	cur.FailedRows += failedDelta
	cur.ClaimedUntil = &until
	cur.UpdatedAt = now
	t.stagedImportJobs[id] = cloneImportJob(cur)
	return true, nil
}

func (t *memTx) MarkProductImportJobCompleted(ctx context.Context, id, claimToken string, createdRows, failedRows int32) (ProductImportJob, bool, error) {
	t.lockJob(id)
	cur, err := t.GetProductImportJobByID(ctx, id)
	if err != nil {
		return ProductImportJob{}, false, nil
	}
	if cur.Status != ImportJobStatusConfirming || cur.ClaimToken != claimToken {
		return ProductImportJob{}, false, nil
	}
	now := AsOfFromContext(ctx)
	cur.Status = ImportJobStatusCompleted
	cur.CreatedRows = createdRows
	cur.FailedRows = failedRows
	cur.RawCSV = nil
	cur.ClaimedUntil = nil
	cur.ClaimToken = ""
	cur.FinishedAt = &now
	cur.UpdatedAt = now
	t.stagedImportJobs[id] = cloneImportJob(cur)
	return cloneImportJob(cur), true, nil
}

func (t *memTx) DeleteProductImportRowsByJob(_ context.Context, jobID string) error {
	t.deletedImportRowsByJob[jobID] = true
	for id, r := range t.stagedImportRows {
		if r.JobID == jobID {
			delete(t.stagedImportRows, id)
		}
	}
	return nil
}

func (t *memTx) InsertProductImportRow(_ context.Context, row ProductImportRow) error {
	if len(row.RawRowJSON) == 0 {
		row.RawRowJSON = []byte("{}")
	}
	t.stagedImportRows[row.ID] = cloneImportRow(row)
	return nil
}

func (t *memTx) ListProductImportRows(ctx context.Context, filter ListProductImportRowsFilter) ([]ProductImportRow, int64, error) {
	return t.store.ListProductImportRows(ctx, filter)
}

func (t *memTx) ListValidProductImportRows(ctx context.Context, jobID string) ([]ProductImportRow, error) {
	return t.store.ListValidProductImportRows(ctx, jobID)
}

func (t *memTx) GetProductImportRowByIDForUpdate(_ context.Context, id string) (ProductImportRow, error) {
	if r, ok := t.stagedImportRows[id]; ok {
		return cloneImportRow(r), nil
	}
	return t.store.GetProductImportRowByIDForUpdate(context.Background(), id)
}

func (t *memTx) UpdateProductImportRowOutcome(ctx context.Context, id, status, errCode, errMsg, createdProductID string) (bool, error) {
	cur, err := t.GetProductImportRowByIDForUpdate(ctx, id)
	if err != nil {
		return false, nil
	}
	if cur.Status != ImportRowStatusValid {
		return false, nil
	}
	cur.Status = status
	cur.ErrorCode = errCode
	cur.ErrorMessage = errMsg
	cur.CreatedProductID = createdProductID
	t.stagedImportRows[id] = cloneImportRow(cur)
	return true, nil
}

func (t *memTx) GetProductImportRowOutcomeCounts(_ context.Context, jobID string) (ImportRowOutcomeCounts, error) {
	t.store.mu.Lock()
	merged := make(map[string]ProductImportRow)
	if !t.deletedImportRowsByJob[jobID] {
		for id, r := range t.store.importRows {
			if r.JobID == jobID {
				merged[id] = r
			}
		}
	}
	t.store.mu.Unlock()
	for id, r := range t.stagedImportRows {
		if r.JobID == jobID {
			merged[id] = r
		}
	}
	var counts ImportRowOutcomeCounts
	for _, r := range merged {
		switch r.Status {
		case ImportRowStatusValid:
			counts.RemainingValid++
		case ImportRowStatusCreated:
			counts.Created++
		case ImportRowStatusFailed:
			counts.Failed++
		}
	}
	return counts, nil
}
