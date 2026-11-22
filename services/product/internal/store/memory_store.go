package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore is a concurrency-safe in-memory implementation of Store with real
// optimistic locking and atomic commit/rollback semantics for unit tests.
type MemoryStore struct {
	mu           sync.Mutex
	categories   map[string]Category
	products     map[string]Product
	skus         map[string]SKU
	reservations map[string]StockReservation
	history      []StockHistoryEntry

	// BeforeReserveWrite is an optional test hook invoked right before an optimistic
	// reserve check inside a transaction to deterministically simulate concurrent updates.
	BeforeReserveWrite func(skuID string)
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		categories:   make(map[string]Category),
		products:     make(map[string]Product),
		skus:         make(map[string]SKU),
		reservations: make(map[string]StockReservation),
	}
}

func (m *MemoryStore) Ping(_ context.Context) error {
	return nil
}

// StockHistory returns a copy of all recorded stock history entries (for test assertions).
func (m *MemoryStore) StockHistory() []StockHistoryEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]StockHistoryEntry, len(m.history))
	copy(out, m.history)
	return out
}

type skuOpKind int

const (
	skuOpUpdateStock skuOpKind = iota + 1
	skuOpReserve
	skuOpRelease
	skuOpConfirm
	skuOpRestock
)

type stagedSKUOp struct {
	kind            skuOpKind
	skuID           string
	amount          int32
	expectedVersion int64
}

type stagedResTransition struct {
	id         string
	fromStatus string
	toStatus   string
}

// WithTx executes fn with optimistic snapshot reads and atomic validation+commit on exit.
func (m *MemoryStore) WithTx(ctx context.Context, fn func(q Queries) error) error {
	tx := &memTx{
		store:       m,
		stagedCats:  make(map[string]Category),
		deletedCats: make(map[string]bool),
		stagedProds: make(map[string]Product),
		stagedSKUs:  make(map[string]SKU),
		stagedRes:   make(map[string]StockReservation),
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.commit()
}

// Non-tx Store methods delegate to single-operation WithTx or direct reads.

func (m *MemoryStore) CreateCategory(ctx context.Context, cat Category) (Category, error) {
	var out Category
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.CreateCategory(ctx, cat)
		return e
	})
	return out, err
}

func (m *MemoryStore) GetCategoryByID(_ context.Context, id string) (Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.categories[id]
	if !ok {
		return Category{}, ErrNotFound
	}
	return c, nil
}

func (m *MemoryStore) UpdateCategory(ctx context.Context, cat Category) (Category, error) {
	var out Category
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.UpdateCategory(ctx, cat)
		return e
	})
	return out, err
}

func (m *MemoryStore) CountProductsInCategory(_ context.Context, categoryID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, p := range m.products {
		if p.DeletedAt == nil && p.CategoryID == categoryID {
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) CountChildCategories(_ context.Context, parentID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, c := range m.categories {
		if c.ParentID == parentID {
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) DeleteCategory(ctx context.Context, id string) (bool, error) {
	var deleted bool
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		deleted, e = q.DeleteCategory(ctx, id)
		return e
	})
	return deleted, err
}

func (m *MemoryStore) ListCategories(_ context.Context, parentID string) ([]Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Category, 0, len(m.categories))
	for _, c := range m.categories {
		switch parentID {
		case "":
			out = append(out, c)
		case "root":
			if c.ParentID == "" {
				out = append(out, c)
			}
		default:
			if c.ParentID == parentID {
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryStore) CreateProduct(ctx context.Context, p Product) (Product, error) {
	var out Product
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.CreateProduct(ctx, p)
		return e
	})
	return out, err
}

func (m *MemoryStore) GetProductByID(_ context.Context, id string) (Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.products[id]
	if !ok || p.DeletedAt != nil {
		return Product{}, ErrNotFound
	}
	return cloneProduct(p), nil
}

func (m *MemoryStore) UpdateProduct(ctx context.Context, p Product) (Product, error) {
	var out Product
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.UpdateProduct(ctx, p)
		return e
	})
	return out, err
}

func (m *MemoryStore) UpdateProductReview(ctx context.Context, id, status, rejectionReason string) (Product, error) {
	var out Product
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.UpdateProductReview(ctx, id, status, rejectionReason)
		return e
	})
	return out, err
}

func (m *MemoryStore) UpdateProductStatus(ctx context.Context, id, status string) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.UpdateProductStatus(ctx, id, status)
	})
}

func (m *MemoryStore) SoftDeleteProduct(ctx context.Context, id string) (bool, error) {
	var ok bool
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		ok, e = q.SoftDeleteProduct(ctx, id)
		return e
	})
	return ok, err
}

func (m *MemoryStore) ListProducts(_ context.Context, filter ListProductsFilter) ([]Product, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listProductsLocked(filter), int64(len(m.filterProductsLocked(filter))), nil
}

func (m *MemoryStore) filterProductsLocked(filter ListProductsFilter) []Product {
	childCats := map[string]bool{}
	if filter.CategoryID != "" {
		childCats[filter.CategoryID] = true
		for _, c := range m.categories {
			if c.ParentID == filter.CategoryID {
				childCats[c.ID] = true
			}
		}
	}
	kw := strings.ToLower(filter.Keyword)
	matched := make([]Product, 0)
	for _, p := range m.products {
		if p.DeletedAt != nil {
			continue
		}
		if filter.CategoryID != "" && !childCats[p.CategoryID] {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(p.Name), kw) && !strings.Contains(strings.ToLower(p.Description), kw) {
			continue
		}
		if filter.ShopID != "" && p.ShopID != filter.ShopID {
			continue
		}
		if filter.PublicOnly && p.Status != ProductStatusApproved && p.Status != ProductStatusOutOfStock {
			continue
		}
		if filter.Status != "" && p.Status != filter.Status {
			continue
		}
		if filter.MinPriceVND > 0 || filter.MaxPriceVND > 0 {
			hasMatch := false
			for _, s := range m.skus {
				if s.ProductID != p.ID || s.Status == SKUStatusInactive {
					continue
				}
				if filter.MinPriceVND > 0 && s.PriceVND < filter.MinPriceVND {
					continue
				}
				if filter.MaxPriceVND > 0 && s.PriceVND > filter.MaxPriceVND {
					continue
				}
				hasMatch = true
				break
			}
			if !hasMatch {
				continue
			}
		}
		matched = append(matched, cloneProduct(p))
	}

	sort.Slice(matched, func(i, j int) bool {
		switch filter.SortBy {
		case "price_asc":
			if matched[i].MinPriceVND != matched[j].MinPriceVND {
				return matched[i].MinPriceVND < matched[j].MinPriceVND
			}
		case "price_desc":
			if matched[i].MaxPriceVND != matched[j].MaxPriceVND {
				return matched[i].MaxPriceVND > matched[j].MaxPriceVND
			}
		case "name_asc":
			if matched[i].Name != matched[j].Name {
				return matched[i].Name < matched[j].Name
			}
		case "oldest":
			if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
				return matched[i].CreatedAt.Before(matched[j].CreatedAt)
			}
		}
		if !matched[i].CreatedAt.Equal(matched[j].CreatedAt) {
			return matched[i].CreatedAt.After(matched[j].CreatedAt)
		}
		return matched[i].ID > matched[j].ID
	})
	return matched
}

func (m *MemoryStore) listProductsLocked(filter ListProductsFilter) []Product {
	all := m.filterProductsLocked(filter)
	start := int(filter.Offset)
	if start >= len(all) {
		return []Product{}
	}
	end := start + int(filter.Limit)
	if filter.Limit <= 0 || end > len(all) {
		end = len(all)
	}
	return all[start:end]
}

func (m *MemoryStore) CreateSKU(ctx context.Context, s SKU) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.CreateSKU(ctx, s)
		return e
	})
	return out, err
}

func (m *MemoryStore) UpdateSKUDetails(ctx context.Context, s SKU) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.UpdateSKUDetails(ctx, s)
		return e
	})
	return out, err
}

func (m *MemoryStore) DeactivateSKUsByProduct(ctx context.Context, productID string) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.DeactivateSKUsByProduct(ctx, productID)
	})
}

func (m *MemoryStore) DeactivateSKU(ctx context.Context, skuID string) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.DeactivateSKU(ctx, skuID)
	})
}

func (m *MemoryStore) ListSKUsByProductIDs(_ context.Context, productIDs []string) ([]SKU, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[string]bool, len(productIDs))
	for _, id := range productIDs {
		want[id] = true
	}
	out := make([]SKU, 0)
	for _, s := range m.skus {
		if want[s.ProductID] && s.Status != SKUStatusInactive {
			out = append(out, m.enrichSKULocked(s))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryStore) GetSKUsBatch(_ context.Context, skuIDs []string) ([]SKU, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[string]bool, len(skuIDs))
	for _, id := range skuIDs {
		want[id] = true
	}
	out := make([]SKU, 0, len(skuIDs))
	for _, s := range m.skus {
		if want[s.ID] {
			out = append(out, m.enrichSKULocked(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemoryStore) GetSKUWithContext(_ context.Context, skuID string) (SKU, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.skus[skuID]
	if !ok {
		return SKU{}, ErrNotFound
	}
	return m.enrichSKULocked(s), nil
}

func (m *MemoryStore) GetSKUWithContextForUpdate(ctx context.Context, skuID string) (SKU, error) {
	return m.GetSKUWithContext(ctx, skuID)
}

func (m *MemoryStore) CountActiveInStockSKUsByProduct(_ context.Context, productID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, s := range m.skus {
		if s.ProductID == productID && s.Status != SKUStatusInactive && s.Stock > 0 {
			n++
		}
	}
	return n, nil
}

func (m *MemoryStore) UpdateSKUStockOptimistic(ctx context.Context, skuID string, newStock int32, expectedVersion int64) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.UpdateSKUStockOptimistic(ctx, skuID, newStock, expectedVersion)
		return e
	})
	return out, err
}

func (m *MemoryStore) ReserveSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.ReserveSKUOptimistic(ctx, skuID, qty, expectedVersion)
		return e
	})
	return out, err
}

func (m *MemoryStore) ReleaseSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.ReleaseSKUOptimistic(ctx, skuID, qty, expectedVersion)
		return e
	})
	return out, err
}

func (m *MemoryStore) ConfirmSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.ConfirmSKUOptimistic(ctx, skuID, qty, expectedVersion)
		return e
	})
	return out, err
}

func (m *MemoryStore) RestockSKUAtomic(ctx context.Context, skuID string, qty int32) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.RestockSKUAtomic(ctx, skuID, qty)
		return e
	})
	return out, err
}

func (m *MemoryStore) ListReservationsByOrderID(_ context.Context, orderID string, _ bool) ([]StockReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]StockReservation, 0)
	for _, r := range m.reservations {
		if r.OrderID == orderID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKUID < out[j].SKUID })
	return out, nil
}

func (m *MemoryStore) CreateStockReservation(ctx context.Context, r StockReservation) (StockReservation, error) {
	var out StockReservation
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.CreateStockReservation(ctx, r)
		return e
	})
	return out, err
}

func (m *MemoryStore) TransitionStockReservation(ctx context.Context, id, toStatus, fromStatus string) (bool, error) {
	var ok bool
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		ok, e = q.TransitionStockReservation(ctx, id, toStatus, fromStatus)
		return e
	})
	return ok, err
}

func (m *MemoryStore) InsertStockHistory(ctx context.Context, h StockHistoryEntry) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.InsertStockHistory(ctx, h)
	})
}

func (m *MemoryStore) TryInsertRestockHistory(ctx context.Context, h StockHistoryEntry) (bool, error) {
	var inserted bool
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		inserted, e = q.TryInsertRestockHistory(ctx, h)
		return e
	})
	return inserted, err
}

func (m *MemoryStore) enrichSKULocked(s SKU) SKU {
	out := cloneSKU(s)
	if p, ok := m.products[s.ProductID]; ok {
		out.ShopID = p.ShopID
		out.ProductName = p.Name
		out.CategoryID = p.CategoryID
		out.ProductStatus = p.Status
		out.ProductDeleted = p.DeletedAt != nil
		if c, ok := m.categories[p.CategoryID]; ok {
			out.CommissionRateBps = c.CommissionRateBps
		}
	}
	return out
}

// memTx buffers writes and validates optimistic locks atomically on commit.
type memTx struct {
	store          *MemoryStore
	stagedCats     map[string]Category
	deletedCats    map[string]bool
	stagedProds    map[string]Product
	stagedSKUs     map[string]SKU
	stagedRes      map[string]StockReservation
	skuOps         []stagedSKUOp
	resTransitions []stagedResTransition
	newRes         []StockReservation
	stagedHistory  []StockHistoryEntry
}

func (t *memTx) commit() error {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()

	now := time.Now().UTC()

	// 1. Validate category slug uniqueness
	for id, cat := range t.stagedCats {
		for existingID, existing := range t.store.categories {
			if existingID != id && !t.deletedCats[existingID] && existing.Slug == cat.Slug {
				return ErrAlreadyExists
			}
		}
	}

	// 2. Validate new stock reservations unique (order_id, sku_id)
	for _, r := range t.newRes {
		for _, existing := range t.store.reservations {
			if existing.OrderID == r.OrderID && existing.SKUID == r.SKUID {
				return ErrDuplicateReservation
			}
		}
	}

	// 3. Validate reservation state transitions
	for _, tr := range t.resTransitions {
		existing, ok := t.store.reservations[tr.id]
		if !ok || existing.Status != tr.fromStatus {
			return ErrVersionConflict
		}
	}

	// 3b. Detect any concurrent RESTOCK history conflict so concurrent transactions never double-restock.
	duplicateRestockSKUs := make(map[string]bool)
	filteredHistory := make([]StockHistoryEntry, 0, len(t.stagedHistory))
	for _, h := range t.stagedHistory {
		if h.ChangeType == "RESTOCK" {
			dup := false
			for _, existing := range t.store.history {
				if existing.ChangeType == "RESTOCK" && existing.OrderID == h.OrderID && existing.SKUID == h.SKUID {
					dup = true
					break
				}
			}
			if dup {
				duplicateRestockSKUs[h.SKUID] = true
				continue
			}
		}
		filteredHistory = append(filteredHistory, h)
	}

	// 4. Validate and compute optimistic SKU operations against committed state
	computedSKUs := make(map[string]SKU)
	for _, op := range t.skuOps {
		if op.kind == skuOpRestock && duplicateRestockSKUs[op.skuID] {
			continue
		}
		cur, ok := computedSKUs[op.skuID]
		if !ok {
			cur, ok = t.store.skus[op.skuID]
			if !ok {
				return ErrNotFound
			}
		}
		switch op.kind {
		case skuOpUpdateStock:
			if cur.Version != op.expectedVersion {
				return ErrVersionConflict
			}
			if op.amount < cur.ReservedStock {
				return ErrInvalidStock
			}
			cur.Stock = op.amount
			if cur.Status != SKUStatusInactive {
				if cur.Stock == 0 {
					cur.Status = SKUStatusOutOfStock
				} else {
					cur.Status = SKUStatusActive
				}
			}
			cur.Version++
			cur.UpdatedAt = now
		case skuOpReserve:
			if cur.AvailableStock() < op.amount {
				return ErrInsufficientStock
			}
			if cur.Version != op.expectedVersion {
				return ErrVersionConflict
			}
			cur.ReservedStock += op.amount
			cur.Version++
			cur.UpdatedAt = now
		case skuOpRelease:
			if cur.Version != op.expectedVersion {
				return ErrVersionConflict
			}
			cur.ReservedStock -= op.amount
			if cur.ReservedStock < 0 {
				cur.ReservedStock = 0
			}
			cur.Version++
			cur.UpdatedAt = now
		case skuOpConfirm:
			if cur.Version != op.expectedVersion {
				return ErrVersionConflict
			}
			cur.Stock -= op.amount
			if cur.Stock < 0 {
				cur.Stock = 0
			}
			cur.ReservedStock -= op.amount
			if cur.ReservedStock < 0 {
				cur.ReservedStock = 0
			}
			if cur.Status != SKUStatusInactive && cur.Stock == 0 {
				cur.Status = SKUStatusOutOfStock
			}
			cur.Version++
			cur.UpdatedAt = now
		case skuOpRestock:
			cur.Stock += op.amount
			if cur.Status != SKUStatusInactive && cur.Stock > 0 {
				cur.Status = SKUStatusActive
			}
			cur.Version++
			cur.UpdatedAt = now
		}
		computedSKUs[op.skuID] = cur
	}

	// 5. Apply all mutations atomically
	for id := range t.deletedCats {
		delete(t.store.categories, id)
	}
	for id, c := range t.stagedCats {
		t.store.categories[id] = c
	}
	for id, p := range t.stagedProds {
		t.store.products[id] = cloneProduct(p)
	}
	for id, s := range t.stagedSKUs {
		if _, hasOp := computedSKUs[id]; !hasOp && !duplicateRestockSKUs[id] {
			t.store.skus[id] = cloneSKU(s)
		}
	}
	for id, s := range computedSKUs {
		t.store.skus[id] = cloneSKU(s)
	}
	for _, r := range t.newRes {
		t.store.reservations[r.ID] = r
	}
	for _, tr := range t.resTransitions {
		r := t.store.reservations[tr.id]
		r.Status = tr.toStatus
		r.UpdatedAt = now
		t.store.reservations[tr.id] = r
	}
	t.store.history = append(t.store.history, filteredHistory...)
	return nil
}

func (t *memTx) CreateCategory(_ context.Context, cat Category) (Category, error) {
	now := time.Now().UTC()
	cat.CreatedAt = now
	cat.UpdatedAt = now
	t.store.mu.Lock()
	for _, c := range t.store.categories {
		if c.Slug == cat.Slug {
			t.store.mu.Unlock()
			return Category{}, ErrAlreadyExists
		}
	}
	t.store.mu.Unlock()
	t.stagedCats[cat.ID] = cat
	return cat, nil
}

func (t *memTx) GetCategoryByID(_ context.Context, id string) (Category, error) {
	if t.deletedCats[id] {
		return Category{}, ErrNotFound
	}
	if c, ok := t.stagedCats[id]; ok {
		return c, nil
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	c, ok := t.store.categories[id]
	if !ok {
		return Category{}, ErrNotFound
	}
	return c, nil
}

func (t *memTx) UpdateCategory(ctx context.Context, cat Category) (Category, error) {
	cur, err := t.GetCategoryByID(ctx, cat.ID)
	if err != nil {
		return Category{}, err
	}
	cat.CreatedAt = cur.CreatedAt
	cat.UpdatedAt = time.Now().UTC()
	t.stagedCats[cat.ID] = cat
	return cat, nil
}

func (t *memTx) CountProductsInCategory(ctx context.Context, categoryID string) (int64, error) {
	return t.store.CountProductsInCategory(ctx, categoryID)
}

func (t *memTx) CountChildCategories(ctx context.Context, parentID string) (int64, error) {
	return t.store.CountChildCategories(ctx, parentID)
}

func (t *memTx) DeleteCategory(ctx context.Context, id string) (bool, error) {
	if _, err := t.GetCategoryByID(ctx, id); err != nil {
		return false, nil
	}
	delete(t.stagedCats, id)
	t.deletedCats[id] = true
	return true, nil
}

func (t *memTx) ListCategories(ctx context.Context, parentID string) ([]Category, error) {
	return t.store.ListCategories(ctx, parentID)
}

func (t *memTx) CreateProduct(_ context.Context, p Product) (Product, error) {
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	t.stagedProds[p.ID] = cloneProduct(p)
	return cloneProduct(p), nil
}

func (t *memTx) GetProductByID(_ context.Context, id string) (Product, error) {
	if p, ok := t.stagedProds[id]; ok {
		if p.DeletedAt != nil {
			return Product{}, ErrNotFound
		}
		return cloneProduct(p), nil
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	p, ok := t.store.products[id]
	if !ok || p.DeletedAt != nil {
		return Product{}, ErrNotFound
	}
	return cloneProduct(p), nil
}

func (t *memTx) UpdateProduct(ctx context.Context, p Product) (Product, error) {
	cur, err := t.GetProductByID(ctx, p.ID)
	if err != nil {
		return Product{}, err
	}
	p.ShopID = cur.ShopID
	p.CreatedAt = cur.CreatedAt
	p.UpdatedAt = time.Now().UTC()
	t.stagedProds[p.ID] = cloneProduct(p)
	return cloneProduct(p), nil
}

func (t *memTx) UpdateProductReview(ctx context.Context, id, status, rejectionReason string) (Product, error) {
	cur, err := t.GetProductByID(ctx, id)
	if err != nil {
		return Product{}, err
	}
	cur.Status = status
	cur.RejectionReason = rejectionReason
	cur.UpdatedAt = time.Now().UTC()
	t.stagedProds[id] = cloneProduct(cur)
	return cloneProduct(cur), nil
}

func (t *memTx) UpdateProductStatus(ctx context.Context, id, status string) error {
	cur, err := t.GetProductByID(ctx, id)
	if err != nil {
		return err
	}
	cur.Status = status
	cur.UpdatedAt = time.Now().UTC()
	t.stagedProds[id] = cloneProduct(cur)
	return nil
}

func (t *memTx) SoftDeleteProduct(ctx context.Context, id string) (bool, error) {
	cur, err := t.GetProductByID(ctx, id)
	if err != nil {
		return false, nil
	}
	now := time.Now().UTC()
	cur.DeletedAt = &now
	cur.Status = ProductStatusHidden
	cur.UpdatedAt = now
	t.stagedProds[id] = cloneProduct(cur)
	return true, nil
}

func (t *memTx) ListProducts(ctx context.Context, filter ListProductsFilter) ([]Product, int64, error) {
	return t.store.ListProducts(ctx, filter)
}

func (t *memTx) CreateSKU(_ context.Context, s SKU) (SKU, error) {
	now := time.Now().UTC()
	s.ReservedStock = 0
	s.Version = 1
	s.CreatedAt = now
	s.UpdatedAt = now
	t.stagedSKUs[s.ID] = cloneSKU(s)
	return cloneSKU(s), nil
}

func (t *memTx) UpdateSKUDetails(ctx context.Context, s SKU) (SKU, error) {
	cur, err := t.GetSKUWithContext(ctx, s.ID)
	if err != nil {
		return SKU{}, err
	}
	cur.SKUCode = s.SKUCode
	cur.Name = s.Name
	cur.Attributes = cloneMap(s.Attributes)
	cur.PriceVND = s.PriceVND
	cur.Stock = s.Stock
	cur.WeightGrams = s.WeightGrams
	cur.ImageURL = s.ImageURL
	cur.Status = s.Status
	cur.Version++
	cur.UpdatedAt = time.Now().UTC()
	t.stagedSKUs[cur.ID] = cloneSKU(cur)
	return cloneSKU(cur), nil
}

func (t *memTx) DeactivateSKUsByProduct(_ context.Context, productID string) error {
	t.store.mu.Lock()
	for id, s := range t.store.skus {
		if s.ProductID == productID {
			cp := cloneSKU(s)
			cp.Status = SKUStatusInactive
			t.stagedSKUs[id] = cp
		}
	}
	t.store.mu.Unlock()
	for id, s := range t.stagedSKUs {
		if s.ProductID == productID {
			s.Status = SKUStatusInactive
			t.stagedSKUs[id] = s
		}
	}
	return nil
}

func (t *memTx) DeactivateSKU(ctx context.Context, skuID string) error {
	cur, err := t.GetSKUWithContext(ctx, skuID)
	if err != nil {
		return err
	}
	cur.Status = SKUStatusInactive
	t.stagedSKUs[skuID] = cloneSKU(cur)
	return nil
}

func (t *memTx) ListSKUsByProductIDs(_ context.Context, productIDs []string) ([]SKU, error) {
	want := make(map[string]bool, len(productIDs))
	for _, id := range productIDs {
		want[id] = true
	}
	merged := make(map[string]SKU)
	t.store.mu.Lock()
	for id, s := range t.store.skus {
		if want[s.ProductID] {
			merged[id] = t.store.enrichSKULocked(s)
		}
	}
	t.store.mu.Unlock()
	for id, s := range t.stagedSKUs {
		if want[s.ProductID] {
			merged[id] = t.enrichWithStaged(s)
		}
	}
	out := make([]SKU, 0, len(merged))
	for _, s := range merged {
		if s.Status != SKUStatusInactive {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (t *memTx) GetSKUsBatch(ctx context.Context, skuIDs []string) ([]SKU, error) {
	return t.store.GetSKUsBatch(ctx, skuIDs)
}

func (t *memTx) GetSKUWithContext(_ context.Context, skuID string) (SKU, error) {
	if s, ok := t.stagedSKUs[skuID]; ok {
		return t.enrichWithStaged(s), nil
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	s, ok := t.store.skus[skuID]
	if !ok {
		return SKU{}, ErrNotFound
	}
	return t.store.enrichSKULocked(s), nil
}

func (t *memTx) GetSKUWithContextForUpdate(ctx context.Context, skuID string) (SKU, error) {
	return t.GetSKUWithContext(ctx, skuID)
}

func (t *memTx) enrichWithStaged(s SKU) SKU {
	out := cloneSKU(s)
	if p, ok := t.stagedProds[s.ProductID]; ok {
		out.ShopID = p.ShopID
		out.ProductName = p.Name
		out.CategoryID = p.CategoryID
		out.ProductStatus = p.Status
		out.ProductDeleted = p.DeletedAt != nil
	} else {
		t.store.mu.Lock()
		if p, ok := t.store.products[s.ProductID]; ok {
			out.ShopID = p.ShopID
			out.ProductName = p.Name
			out.CategoryID = p.CategoryID
			out.ProductStatus = p.Status
			out.ProductDeleted = p.DeletedAt != nil
		}
		t.store.mu.Unlock()
	}
	if c, ok := t.stagedCats[out.CategoryID]; ok {
		out.CommissionRateBps = c.CommissionRateBps
	} else {
		t.store.mu.Lock()
		if c, ok := t.store.categories[out.CategoryID]; ok {
			out.CommissionRateBps = c.CommissionRateBps
		}
		t.store.mu.Unlock()
	}
	return out
}

func (t *memTx) CountActiveInStockSKUsByProduct(_ context.Context, productID string) (int64, error) {
	merged := make(map[string]SKU)
	t.store.mu.Lock()
	for id, s := range t.store.skus {
		if s.ProductID == productID {
			merged[id] = s
		}
	}
	t.store.mu.Unlock()
	for id, s := range t.stagedSKUs {
		if s.ProductID == productID {
			merged[id] = s
		}
	}
	var n int64
	for _, s := range merged {
		if s.Status != SKUStatusInactive && s.Stock > 0 {
			n++
		}
	}
	return n, nil
}

func (t *memTx) UpdateSKUStockOptimistic(ctx context.Context, skuID string, newStock int32, expectedVersion int64) (SKU, error) {
	cur, err := t.GetSKUWithContext(ctx, skuID)
	if err != nil {
		return SKU{}, err
	}
	if cur.Version != expectedVersion {
		return SKU{}, ErrVersionConflict
	}
	if newStock < cur.ReservedStock {
		return SKU{}, ErrInvalidStock
	}
	t.skuOps = append(t.skuOps, stagedSKUOp{
		kind:            skuOpUpdateStock,
		skuID:           skuID,
		amount:          newStock,
		expectedVersion: expectedVersion,
	})
	cur.Stock = newStock
	if cur.Status != SKUStatusInactive {
		if cur.Stock == 0 {
			cur.Status = SKUStatusOutOfStock
		} else {
			cur.Status = SKUStatusActive
		}
	}
	cur.Version++
	t.stagedSKUs[skuID] = cloneSKU(cur)
	return cur, nil
}

func (t *memTx) ReserveSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	if hook := t.store.BeforeReserveWrite; hook != nil {
		hook(skuID)
	}
	cur, err := t.GetSKUWithContext(ctx, skuID)
	if err != nil {
		return SKU{}, err
	}
	if cur.AvailableStock() < qty {
		return SKU{}, ErrInsufficientStock
	}
	if cur.Version != expectedVersion {
		return SKU{}, ErrVersionConflict
	}
	t.skuOps = append(t.skuOps, stagedSKUOp{
		kind:            skuOpReserve,
		skuID:           skuID,
		amount:          qty,
		expectedVersion: expectedVersion,
	})
	cur.ReservedStock += qty
	cur.Version++
	t.stagedSKUs[skuID] = cloneSKU(cur)
	return cur, nil
}

func (t *memTx) ReleaseSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	cur, err := t.GetSKUWithContext(ctx, skuID)
	if err != nil {
		return SKU{}, err
	}
	if cur.Version != expectedVersion {
		return SKU{}, ErrVersionConflict
	}
	t.skuOps = append(t.skuOps, stagedSKUOp{
		kind:            skuOpRelease,
		skuID:           skuID,
		amount:          qty,
		expectedVersion: expectedVersion,
	})
	cur.ReservedStock -= qty
	if cur.ReservedStock < 0 {
		cur.ReservedStock = 0
	}
	cur.Version++
	t.stagedSKUs[skuID] = cloneSKU(cur)
	return cur, nil
}

func (t *memTx) ConfirmSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	cur, err := t.GetSKUWithContext(ctx, skuID)
	if err != nil {
		return SKU{}, err
	}
	if cur.Version != expectedVersion {
		return SKU{}, ErrVersionConflict
	}
	t.skuOps = append(t.skuOps, stagedSKUOp{
		kind:            skuOpConfirm,
		skuID:           skuID,
		amount:          qty,
		expectedVersion: expectedVersion,
	})
	cur.Stock -= qty
	if cur.Stock < 0 {
		cur.Stock = 0
	}
	cur.ReservedStock -= qty
	if cur.ReservedStock < 0 {
		cur.ReservedStock = 0
	}
	if cur.Status != SKUStatusInactive && cur.Stock == 0 {
		cur.Status = SKUStatusOutOfStock
	}
	cur.Version++
	t.stagedSKUs[skuID] = cloneSKU(cur)
	return cur, nil
}

func (t *memTx) RestockSKUAtomic(ctx context.Context, skuID string, qty int32) (SKU, error) {
	cur, err := t.GetSKUWithContext(ctx, skuID)
	if err != nil {
		return SKU{}, err
	}
	t.skuOps = append(t.skuOps, stagedSKUOp{
		kind:   skuOpRestock,
		skuID:  skuID,
		amount: qty,
	})
	cur.Stock += qty
	if cur.Status != SKUStatusInactive && cur.Stock > 0 {
		cur.Status = SKUStatusActive
	}
	cur.Version++
	t.stagedSKUs[skuID] = cloneSKU(cur)
	return cur, nil
}

func (t *memTx) ListReservationsByOrderID(_ context.Context, orderID string, _ bool) ([]StockReservation, error) {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	out := make([]StockReservation, 0)
	for _, r := range t.store.reservations {
		if r.OrderID == orderID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKUID < out[j].SKUID })
	return out, nil
}

func (t *memTx) CreateStockReservation(_ context.Context, r StockReservation) (StockReservation, error) {
	now := time.Now().UTC()
	r.Status = ReservationStatusReserved
	r.CreatedAt = now
	r.UpdatedAt = now
	t.newRes = append(t.newRes, r)
	t.stagedRes[r.ID] = r
	return r, nil
}

func (t *memTx) TransitionStockReservation(_ context.Context, id, toStatus, fromStatus string) (bool, error) {
	t.store.mu.Lock()
	r, ok := t.store.reservations[id]
	t.store.mu.Unlock()
	if !ok || r.Status != fromStatus {
		return false, nil
	}
	t.resTransitions = append(t.resTransitions, stagedResTransition{
		id:         id,
		fromStatus: fromStatus,
		toStatus:   toStatus,
	})
	return true, nil
}

func (t *memTx) InsertStockHistory(_ context.Context, h StockHistoryEntry) error {
	h.CreatedAt = time.Now().UTC()
	t.stagedHistory = append(t.stagedHistory, h)
	return nil
}

func (t *memTx) TryInsertRestockHistory(_ context.Context, h StockHistoryEntry) (bool, error) {
	h.ChangeType = "RESTOCK"
	t.store.mu.Lock()
	for _, existing := range t.store.history {
		if existing.ChangeType == "RESTOCK" && existing.OrderID == h.OrderID && existing.SKUID == h.SKUID {
			t.store.mu.Unlock()
			return false, nil
		}
	}
	t.store.mu.Unlock()
	for _, staged := range t.stagedHistory {
		if staged.ChangeType == "RESTOCK" && staged.OrderID == h.OrderID && staged.SKUID == h.SKUID {
			return false, nil
		}
	}
	h.CreatedAt = time.Now().UTC()
	t.stagedHistory = append(t.stagedHistory, h)
	return true, nil
}

func cloneProduct(p Product) Product {
	if p.Images != nil {
		cp := make([]string, len(p.Images))
		copy(cp, p.Images)
		p.Images = cp
	}
	return p
}

func cloneSKU(s SKU) SKU {
	s.Attributes = cloneMap(s.Attributes)
	return s
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
