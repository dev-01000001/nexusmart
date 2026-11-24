package store

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nexusmart/core/pkg/common/jetstream"
)

// MemoryStore is a concurrency-safe in-memory implementation of Store with real
// optimistic locking and atomic commit/rollback semantics for unit tests.
type MemoryStore struct {
	mu                   sync.Mutex
	categories           map[string]Category
	feeSchedules         []CategoryFeeSchedule
	nextFeeSeq           int64
	products             map[string]Product
	skus                 map[string]SKU
	priceHistory         []SKUPriceHistoryEntry
	nextPriceSeq         int64
	reservations         map[string]StockReservation
	releaseTombstones    map[string]time.Time
	history              []StockHistoryEntry
	outbox               []OutboxEvent
	nextOutboxSeq        int64
	outboxLease          time.Duration
	outboxPublishTimeout time.Duration
	outboxNowFn          func() time.Time
	logger               atomic.Pointer[slog.Logger]

	// BeforeReserveWrite is an optional test hook invoked right before an optimistic
	// reserve check inside a transaction to deterministically simulate concurrent updates.
	BeforeReserveWrite func(skuID string)
	// BeforeUpdateStockWrite is an optional test hook invoked right before an optimistic
	// stock update check inside a transaction to deterministically simulate concurrent updates.
	BeforeUpdateStockWrite func(skuID string)
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		categories:           make(map[string]Category),
		products:             make(map[string]Product),
		skus:                 make(map[string]SKU),
		reservations:         make(map[string]StockReservation),
		releaseTombstones:    make(map[string]time.Time),
		outboxLease:          jetstream.DefaultOutboxLease,
		outboxPublishTimeout: jetstream.DefaultOutboxPublishTimeout,
	}
}

// SetLogger attaches a structured logger used for outbox poison-event ERROR alerts.
func (m *MemoryStore) SetLogger(l *slog.Logger) {
	m.logger.Store(l)
}

func (m *MemoryStore) outboxLogger() *slog.Logger {
	if l := m.logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// SetOutboxLease overrides the outbox claim lease duration (useful in tests).
func (m *MemoryStore) SetOutboxLease(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d <= 0 {
		d = jetstream.DefaultOutboxLease
	}
	m.outboxLease = d
}

// SetOutboxPublishTimeout overrides the max per-event publish timeout budget (useful in tests).
func (m *MemoryStore) SetOutboxPublishTimeout(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d <= 0 {
		d = jetstream.DefaultOutboxPublishTimeout
	}
	m.outboxPublishTimeout = d
}

// SetOutboxClock overrides the clock used for outbox lease expiry checks (useful in tests).
func (m *MemoryStore) SetOutboxClock(fn func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outboxNowFn = fn
}

func (m *MemoryStore) outboxNowLocked() time.Time {
	if m.outboxNowFn == nil {
		return time.Now()
	}
	return m.outboxNowFn()
}

func (m *MemoryStore) outboxNow() time.Time {
	m.mu.Lock()
	fn := m.outboxNowFn
	m.mu.Unlock()
	if fn == nil {
		return time.Now()
	}
	return fn()
}

func (m *MemoryStore) outboxTimings() (time.Duration, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease := m.outboxLease
	if lease <= 0 {
		lease = jetstream.DefaultOutboxLease
	}
	pubTimeout := m.outboxPublishTimeout
	if pubTimeout <= 0 {
		pubTimeout = jetstream.DefaultOutboxPublishTimeout
	}
	return lease, pubTimeout
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

// PriceHistory returns a copy of all recorded SKU price history entries (for test assertions).
func (m *MemoryStore) PriceHistory() []SKUPriceHistoryEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SKUPriceHistoryEntry, len(m.priceHistory))
	copy(out, m.priceHistory)
	return out
}

// OutboxEvents returns a copy of all recorded outbox events (for test assertions).
func (m *MemoryStore) OutboxEvents() []OutboxEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboxEvent, len(m.outbox))
	for i, ev := range m.outbox {
		ev.PayloadJSON = append([]byte(nil), ev.PayloadJSON...)
		if ev.PublishedAt != nil {
			ts := *ev.PublishedAt
			ev.PublishedAt = &ts
		}
		if ev.ClaimedUntil != nil {
			ts := *ev.ClaimedUntil
			ev.ClaimedUntil = &ts
		}
		out[i] = ev
	}
	return out
}

func outboxEventBefore(a, b OutboxEvent) bool {
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	return a.ID < b.ID
}

// ClaimPendingOutbox claims up to limit unpublished outbox events whose lease/backoff is unset or expired,
// respecting strict per-aggregate seq order.
func (m *MemoryStore) ClaimPendingOutbox(_ context.Context, limit int32) ([]OutboxEvent, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.outboxNowLocked()
	lease := m.outboxLease
	if lease <= 0 {
		lease = jetstream.DefaultOutboxLease
	}
	leaseUntil := now.Add(lease)
	token := uuid.NewString()

	// Sort indices strictly by seq order.
	order := make([]int, len(m.outbox))
	for i := range m.outbox {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return outboxEventBefore(m.outbox[order[i]], m.outbox[order[j]])
	})

	// Track aggregates that already have an earlier in-flight leased or backed-off event.
	activeLeasedAgg := make(map[string]bool)
	claimed := make([]OutboxEvent, 0)
	for _, idx := range order {
		ev := &m.outbox[idx]
		if ev.PublishedAt != nil {
			continue
		}
		aggKey := OutboxAggregateKey(*ev)
		isLeased := ev.ClaimedUntil != nil && ev.ClaimedUntil.After(now)
		if isLeased {
			if aggKey != "" {
				activeLeasedAgg[aggKey] = true
			}
			continue
		}
		if aggKey != "" && activeLeasedAgg[aggKey] {
			continue
		}
		if limit > 0 && int32(len(claimed)) >= limit {
			break
		}
		untilCopy := leaseUntil
		ev.ClaimedUntil = &untilCopy
		ev.ClaimToken = token

		cp := *ev
		cp.PayloadJSON = append([]byte(nil), ev.PayloadJSON...)
		cpUntil := leaseUntil
		cp.ClaimedUntil = &cpUntil
		claimed = append(claimed, cp)
	}
	return claimed, token, nil
}

// MarkOutboxPublished marks an event published only if claimToken still owns the lease.
func (m *MemoryStore) MarkOutboxPublished(_ context.Context, id, claimToken string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.outboxNowLocked()
	for i := range m.outbox {
		if m.outbox[i].ID != id {
			continue
		}
		if m.outbox[i].PublishedAt != nil || m.outbox[i].ClaimToken != claimToken {
			return false, nil
		}
		ts := now
		m.outbox[i].PublishedAt = &ts
		m.outbox[i].Attempts++
		m.outbox[i].LastError = ""
		m.outbox[i].ClaimedUntil = nil
		m.outbox[i].ClaimToken = ""
		return true, nil
	}
	return false, nil
}

// StealOutboxClaimForTest overwrites the claim_token of an unpublished outbox row (used by unit tests
// to deterministically simulate mid-batch lease loss).
func (m *MemoryStore) StealOutboxClaimForTest(id, newToken string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.outbox {
		if m.outbox[i].ID == id && m.outbox[i].PublishedAt == nil {
			m.outbox[i].ClaimToken = newToken
			return
		}
	}
}

func (m *MemoryStore) markOutboxAttemptFailed(id, claimToken, errMsg string, backoff time.Duration) (int32, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.outbox {
		if m.outbox[i].ID != id {
			continue
		}
		if m.outbox[i].PublishedAt != nil || m.outbox[i].ClaimToken != claimToken {
			return 0, false
		}
		m.outbox[i].Attempts++
		m.outbox[i].LastError = errMsg
		if backoff > 0 {
			until := m.outboxNowLocked().Add(backoff)
			m.outbox[i].ClaimedUntil = &until
		} else {
			m.outbox[i].ClaimedUntil = nil
		}
		m.outbox[i].ClaimToken = ""
		return m.outbox[i].Attempts, true
	}
	return 0, false
}

func (m *MemoryStore) releaseOutboxClaim(id, claimToken string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.outbox {
		if m.outbox[i].ID != id {
			continue
		}
		if m.outbox[i].PublishedAt != nil || m.outbox[i].ClaimToken != claimToken {
			return
		}
		m.outbox[i].ClaimedUntil = nil
		m.outbox[i].ClaimToken = ""
		return
	}
}

func (m *MemoryStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev OutboxEvent) error) (int, error) {
	lease, pubTimeout := m.outboxTimings()
	leaseDeadline := m.outboxNow().Add(lease)

	claimed, claimToken, err := m.ClaimPendingOutbox(ctx, limit)
	if err != nil {
		return 0, err
	}
	if len(claimed) == 0 {
		return 0, nil
	}

	var toRelease []OutboxEvent
	defer func() {
		for _, r := range toRelease {
			m.releaseOutboxClaim(r.ID, claimToken)
		}
	}()

	published := 0
	failedAggs := make(map[string]bool)
	for i, ev := range claimed {
		if ctx.Err() != nil {
			toRelease = append(toRelease, claimed[i:]...)
			return published, ctx.Err()
		}
		if leaseDeadline.Sub(m.outboxNow()) < pubTimeout {
			toRelease = append(toRelease, claimed[i:]...)
			return published, nil
		}
		aggKey := OutboxAggregateKey(ev)
		if aggKey != "" && failedAggs[aggKey] {
			toRelease = append(toRelease, ev)
			continue
		}
		pubCtx, pubCancel := context.WithTimeout(ctx, pubTimeout)
		pubErr := publishFn(pubCtx, ev)
		pubCancel()
		if pubErr != nil {
			infraErr := jetstream.IsInfraError(pubErr)
			var backoff time.Duration
			if !infraErr {
				backoff = jetstream.OutboxRetryBackoff(ev.Attempts + 1)
			}
			newAttempts, updated := m.markOutboxAttemptFailed(ev.ID, claimToken, pubErr.Error(), backoff)
			if infraErr {
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, pubErr
			}
			if !updated {
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, nil
			}
			if newAttempts >= jetstream.OutboxPoisonLogAttempt {
				m.outboxLogger().ErrorContext(ctx, "outbox event repeatedly failed",
					slog.String("event_id", ev.ID),
					slog.String("event_type", ev.EventType),
					slog.String("aggregate_id", aggKey),
					slog.Int("attempts", int(newAttempts)),
					slog.String("error", pubErr.Error()),
				)
			}
			if aggKey != "" {
				failedAggs[aggKey] = true
			}
			continue
		}
		ok, markErr := m.MarkOutboxPublished(ctx, ev.ID, claimToken)
		if markErr != nil {
			toRelease = append(toRelease, claimed[i+1:]...)
			return published, markErr
		}
		if !ok {
			toRelease = append(toRelease, claimed[i+1:]...)
			return published, nil
		}
		published++
	}
	return published, nil
}

type skuOpKind int

const (
	skuOpUpdateStock skuOpKind = iota + 1
	skuOpReserve
	skuOpRelease
	skuOpConfirm
	skuOpRestock
	skuOpReactivate
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
		store:            m,
		stagedCats:       make(map[string]Category),
		deletedCats:      make(map[string]bool),
		stagedProds:      make(map[string]Product),
		stagedSKUs:       make(map[string]SKU),
		updatedSKUMeta:   make(map[string]bool),
		deactivatedSKUs:  make(map[string]bool),
		stagedRes:        make(map[string]StockReservation),
		stagedTombstones: make(map[string]time.Time),
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.commit()
}

// Non-tx Store methods delegate to single-operation WithTx or direct reads.

func (m *MemoryStore) LockCategoryTree(_ context.Context) error {
	return nil
}

func (m *MemoryStore) CreateCategory(ctx context.Context, cat Category) (Category, error) {
	var out Category
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.CreateCategory(ctx, cat)
		return e
	})
	return out, err
}

func (m *MemoryStore) GetCategoryByID(ctx context.Context, id string) (Category, error) {
	asOf := AsOfFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.categories[id]
	if !ok {
		return Category{}, ErrNotFound
	}
	c.CommissionRateBps = m.effectiveCommissionBpsLocked(c.ID, c.CommissionRateBps, asOf, nil)
	return c, nil
}

func (m *MemoryStore) GetCategoryBySlug(_ context.Context, slug string) (Category, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.categories {
		if c.Slug == slug {
			return c, nil
		}
	}
	return Category{}, ErrNotFound
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

func (m *MemoryStore) UpdateCategoryCommissionRate(ctx context.Context, id string, bps int32) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.UpdateCategoryCommissionRate(ctx, id, bps)
	})
}

func (m *MemoryStore) CountProductsInCategory(_ context.Context, categoryID string) (CategoryProductCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var counts CategoryProductCounts
	for _, p := range m.products {
		if p.CategoryID == categoryID {
			counts.Total++
			if p.DeletedAt == nil {
				counts.Active++
			}
		}
	}
	return counts, nil
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

func (m *MemoryStore) ListCategories(ctx context.Context, parentID string) ([]Category, error) {
	asOf := AsOfFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Category, 0, len(m.categories))
	for _, c := range m.categories {
		switch parentID {
		case "":
			c.CommissionRateBps = m.effectiveCommissionBpsLocked(c.ID, c.CommissionRateBps, asOf, nil)
			out = append(out, c)
		case "root":
			if c.ParentID == "" {
				c.CommissionRateBps = m.effectiveCommissionBpsLocked(c.ID, c.CommissionRateBps, asOf, nil)
				out = append(out, c)
			}
		default:
			if c.ParentID == parentID {
				c.CommissionRateBps = m.effectiveCommissionBpsLocked(c.ID, c.CommissionRateBps, asOf, nil)
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

func (m *MemoryStore) CategoryHasAncestor(_ context.Context, startID, targetAncestorID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return categoryHasAncestorInMap(m.categories, startID, targetAncestorID), nil
}

func (m *MemoryStore) GetCategoryDescendants(_ context.Context, categoryIDs []string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return collectCategoryDescendants(m.categories, categoryIDs), nil
}

func (m *MemoryStore) InsertCategoryFeeSchedule(ctx context.Context, sched CategoryFeeSchedule) (CategoryFeeSchedule, error) {
	var out CategoryFeeSchedule
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.InsertCategoryFeeSchedule(ctx, sched)
		return e
	})
	return out, err
}

func (m *MemoryStore) ListCategoryFeeSchedules(_ context.Context, categoryID string, limit, offset int32) ([]CategoryFeeSchedule, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	matched := make([]CategoryFeeSchedule, 0, len(m.feeSchedules))
	for _, s := range m.feeSchedules {
		if categoryID != "" && s.CategoryID != categoryID {
			continue
		}
		matched = append(matched, s)
	}
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].EffectiveFrom.Equal(matched[j].EffectiveFrom) {
			return matched[i].EffectiveFrom.After(matched[j].EffectiveFrom)
		}
		return matched[i].Seq > matched[j].Seq
	})
	total := int64(len(matched))
	start := int(offset)
	if start >= len(matched) {
		return []CategoryFeeSchedule{}, total, nil
	}
	end := start + int(limit)
	if limit <= 0 || end > len(matched) {
		end = len(matched)
	}
	out := make([]CategoryFeeSchedule, end-start)
	copy(out, matched[start:end])
	return out, total, nil
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

func (m *MemoryStore) GetProductByIDForUpdate(ctx context.Context, id string) (Product, error) {
	return m.GetProductByID(ctx, id)
}

func (m *MemoryStore) GetProductBySlug(_ context.Context, slug string) (Product, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.products {
		if p.DeletedAt == nil && p.Slug != "" && p.Slug == slug {
			return cloneProduct(p), nil
		}
	}
	return Product{}, ErrNotFound
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
	allowedCats := map[string]bool{}
	if filter.CategoryID != "" {
		if _, exists := m.categories[filter.CategoryID]; exists {
			for _, id := range collectCategoryDescendants(m.categories, []string{filter.CategoryID}) {
				allowedCats[id] = true
			}
		}
	}
	kw := ""
	if strings.TrimSpace(filter.Keyword) != "" {
		kw = NormalizeSearchText(filter.Keyword)
		if kw == "" {
			return []Product{}
		}
	}
	var matchedBySKUs map[string]bool
	if len(filter.SKUIDs) > 0 {
		wantSKUs := make(map[string]bool, len(filter.SKUIDs))
		for _, id := range filter.SKUIDs {
			wantSKUs[id] = true
		}
		matchedBySKUs = make(map[string]bool)
		for _, s := range m.skus {
			if wantSKUs[s.ID] && s.Status != SKUStatusInactive {
				matchedBySKUs[s.ProductID] = true
			}
		}
		if len(matchedBySKUs) == 0 {
			return []Product{}
		}
	}
	matched := make([]Product, 0)
	for _, p := range m.products {
		if p.DeletedAt != nil {
			continue
		}
		if matchedBySKUs != nil {
			if !matchedBySKUs[p.ID] {
				continue
			}
			if p.Status != ProductStatusApproved && p.Status != ProductStatusOutOfStock {
				continue
			}
		}
		if filter.CategoryID != "" && !allowedCats[p.CategoryID] {
			continue
		}
		if kw != "" {
			st := p.SearchText
			if st == "" {
				st = BuildProductSearchText(p.Name, p.Description)
			}
			if !strings.Contains(st, kw) {
				continue
			}
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

func (m *MemoryStore) InsertSKUPriceHistory(ctx context.Context, h SKUPriceHistoryEntry) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.InsertSKUPriceHistory(ctx, h)
	})
}

func (m *MemoryStore) UpdateSKUMetadata(ctx context.Context, s SKU) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.UpdateSKUMetadata(ctx, s)
		return e
	})
	return out, err
}

func (m *MemoryStore) ReactivateSKUOptimistic(ctx context.Context, s SKU, expectedVersion int64) (SKU, error) {
	var out SKU
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		out, e = q.ReactivateSKUOptimistic(ctx, s, expectedVersion)
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

func (m *MemoryStore) ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]SKU, error) {
	asOf := AsOfFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[string]bool, len(productIDs))
	for _, id := range productIDs {
		want[id] = true
	}
	out := make([]SKU, 0)
	for _, s := range m.skus {
		if want[s.ProductID] && s.Status != SKUStatusInactive {
			out = append(out, m.enrichSKULocked(s, asOf))
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

func (m *MemoryStore) ListAllSKUsByProductID(ctx context.Context, productID string) ([]SKU, error) {
	asOf := AsOfFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SKU, 0)
	for _, s := range m.skus {
		if s.ProductID == productID {
			out = append(out, m.enrichSKULocked(s, asOf))
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

func (m *MemoryStore) GetSKUsBatch(ctx context.Context, skuIDs []string) ([]SKU, error) {
	asOf := AsOfFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[string]bool, len(skuIDs))
	for _, id := range skuIDs {
		want[id] = true
	}
	out := make([]SKU, 0, len(skuIDs))
	for _, s := range m.skus {
		if want[s.ID] {
			out = append(out, m.enrichSKULocked(s, asOf))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemoryStore) GetSKUWithContext(ctx context.Context, skuID string) (SKU, error) {
	asOf := AsOfFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.skus[skuID]
	if !ok {
		return SKU{}, ErrNotFound
	}
	return m.enrichSKULocked(s, asOf), nil
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

func (m *MemoryStore) LockStockOrder(_ context.Context, _ string) error {
	return nil
}

func (m *MemoryStore) InsertStockReleaseTombstone(ctx context.Context, orderID string) (bool, error) {
	var inserted bool
	err := m.WithTx(ctx, func(q Queries) error {
		var e error
		inserted, e = q.InsertStockReleaseTombstone(ctx, orderID)
		return e
	})
	return inserted, err
}

func (m *MemoryStore) HasStockReleaseTombstone(_ context.Context, orderID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.releaseTombstones[orderID]
	return ok, nil
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

func (m *MemoryStore) InsertOutboxEvent(ctx context.Context, ev OutboxEvent) error {
	return m.WithTx(ctx, func(q Queries) error {
		return q.InsertOutboxEvent(ctx, ev)
	})
}

func (m *MemoryStore) effectiveCommissionBpsLocked(categoryID string, fallbackBps int32, asOf time.Time, staged []CategoryFeeSchedule) int32 {
	found := false
	var best CategoryFeeSchedule
	consider := func(s CategoryFeeSchedule) {
		if s.CategoryID != categoryID || s.EffectiveFrom.After(asOf) {
			return
		}
		if !found ||
			s.EffectiveFrom.After(best.EffectiveFrom) ||
			(s.EffectiveFrom.Equal(best.EffectiveFrom) && s.Seq > best.Seq) {
			best = s
			found = true
		}
	}
	for _, s := range m.feeSchedules {
		consider(s)
	}
	for _, s := range staged {
		consider(s)
	}
	if found {
		return best.CommissionRateBps
	}
	return fallbackBps
}

func (m *MemoryStore) minPrice30dLocked(skuID string, currentPrice int64, asOf time.Time, staged []SKUPriceHistoryEntry) int64 {
	cutoff := asOf.Add(-PriceHistoryWindow)
	minPrice := currentPrice
	hasPrior := false
	var priorTime time.Time
	var priorSeq int64
	var priorPrice int64

	consider := func(h SKUPriceHistoryEntry) {
		if h.SKUID != skuID || h.PriceVND <= 0 {
			return
		}
		if !h.RecordedAt.After(cutoff) {
			if !hasPrior || h.RecordedAt.After(priorTime) || (h.RecordedAt.Equal(priorTime) && h.Seq > priorSeq) {
				priorTime = h.RecordedAt
				priorSeq = h.Seq
				priorPrice = h.PriceVND
				hasPrior = true
			}
		} else if !h.RecordedAt.After(asOf) {
			if minPrice <= 0 || h.PriceVND < minPrice {
				minPrice = h.PriceVND
			}
		}
	}
	for _, h := range m.priceHistory {
		consider(h)
	}
	for _, h := range staged {
		consider(h)
	}
	if hasPrior && (minPrice <= 0 || priorPrice < minPrice) {
		minPrice = priorPrice
	}
	if minPrice <= 0 {
		return currentPrice
	}
	return minPrice
}

func (m *MemoryStore) enrichSKULocked(s SKU, asOf time.Time) SKU {
	out := cloneSKU(s)
	if p, ok := m.products[s.ProductID]; ok {
		out.ShopID = p.ShopID
		out.ProductName = p.Name
		out.CategoryID = p.CategoryID
		out.ProductStatus = p.Status
		out.ProductDeleted = p.DeletedAt != nil
		if c, ok := m.categories[p.CategoryID]; ok {
			out.CommissionRateBps = m.effectiveCommissionBpsLocked(c.ID, c.CommissionRateBps, asOf, nil)
		}
	}
	out.MinPrice30dVND = m.minPrice30dLocked(out.ID, out.PriceVND, asOf, nil)
	return out
}

func categoryHasAncestorInMap(cats map[string]Category, startID, targetAncestorID string) bool {
	visited := make(map[string]bool)
	cur := startID
	for cur != "" {
		if cur == targetAncestorID {
			return true
		}
		if visited[cur] {
			return false
		}
		visited[cur] = true
		c, ok := cats[cur]
		if !ok {
			return false
		}
		cur = c.ParentID
	}
	return false
}

func collectCategoryDescendants(cats map[string]Category, rootIDs []string) []string {
	seen := make(map[string]bool, len(rootIDs))
	queue := make([]string, 0, len(rootIDs))
	for _, id := range rootIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, id)
	}
	childrenByParent := make(map[string][]string)
	for _, c := range cats {
		if c.ParentID != "" {
			childrenByParent[c.ParentID] = append(childrenByParent[c.ParentID], c.ID)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, childID := range childrenByParent[cur] {
			if !seen[childID] {
				seen[childID] = true
				queue = append(queue, childID)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// memTx buffers writes and validates optimistic locks atomically on commit.
type memTx struct {
	store              *MemoryStore
	stagedCats         map[string]Category
	deletedCats        map[string]bool
	stagedFeeSchedules []CategoryFeeSchedule
	stagedProds        map[string]Product
	stagedSKUs         map[string]SKU
	stagedPriceHistory []SKUPriceHistoryEntry
	updatedSKUMeta     map[string]bool
	deactivatedSKUs    map[string]bool
	stagedRes          map[string]StockReservation
	stagedTombstones   map[string]time.Time
	skuOps             []stagedSKUOp
	resTransitions     []stagedResTransition
	newRes             []StockReservation
	stagedHistory      []StockHistoryEntry
	stagedOutbox       []OutboxEvent
}

func (t *memTx) commit() error {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()

	now := time.Now().UTC()

	// 1. Validate category slug uniqueness, hierarchy cycles, and FK references on delete.
	effCats := make(map[string]Category, len(t.store.categories)+len(t.stagedCats))
	for id, c := range t.store.categories {
		if !t.deletedCats[id] {
			effCats[id] = c
		}
	}
	for id, c := range t.stagedCats {
		effCats[id] = c
	}

	for id, cat := range t.stagedCats {
		for existingID, existing := range effCats {
			if existingID != id && existing.Slug == cat.Slug {
				return ErrAlreadyExists
			}
		}
		if cat.ParentID != "" {
			if cat.ParentID == id {
				return ErrCategoryCycle
			}
			if _, parentExists := effCats[cat.ParentID]; !parentExists {
				return ErrNotFound
			}
			if categoryHasAncestorInMap(effCats, cat.ParentID, id) {
				return ErrCategoryCycle
			}
		}
	}

	for delID := range t.deletedCats {
		for _, c := range effCats {
			if c.ParentID == delID {
				return ErrForeignKeyViolation
			}
		}
		for prodID, p := range t.store.products {
			effProd := p
			if sp, ok := t.stagedProds[prodID]; ok {
				effProd = sp
			}
			if effProd.CategoryID == delID {
				return ErrForeignKeyViolation
			}
		}
		for _, sp := range t.stagedProds {
			if sp.CategoryID == delID {
				return ErrForeignKeyViolation
			}
		}
	}

	// 1a. Validate FK references on staged fee schedules and products.
	for _, f := range t.stagedFeeSchedules {
		if _, ok := effCats[f.CategoryID]; !ok {
			return ErrForeignKeyViolation
		}
	}
	for _, prod := range t.stagedProds {
		if _, ok := effCats[prod.CategoryID]; !ok {
			return ErrForeignKeyViolation
		}
	}

	// 1b. Validate active product slug uniqueness
	for id, prod := range t.stagedProds {
		if prod.DeletedAt != nil || prod.Slug == "" {
			continue
		}
		for existingID, existing := range t.store.products {
			if existingID != id && existing.DeletedAt == nil && existing.Slug == prod.Slug {
				if staged, hasStaged := t.stagedProds[existingID]; hasStaged && (staged.DeletedAt != nil || staged.Slug != prod.Slug) {
					continue
				}
				return ErrAlreadyExists
			}
		}
	}

	// 1c. Validate SKU (product_id, sku_code) uniqueness across all rows including INACTIVE.
	for id, sku := range t.stagedSKUs {
		for existingID, existing := range t.store.skus {
			if existingID == id {
				continue
			}
			eff := existing
			if staged, hasStaged := t.stagedSKUs[existingID]; hasStaged {
				eff = staged
			}
			if eff.ProductID == sku.ProductID && eff.SKUCode == sku.SKUCode {
				return ErrAlreadyExists
			}
		}
		for otherID, other := range t.stagedSKUs {
			if otherID != id && other.ProductID == sku.ProductID && other.SKUCode == sku.SKUCode {
				return ErrAlreadyExists
			}
		}
	}

	// 2. Validate new stock reservations unique (order_id, sku_id) and not release-tombstoned.
	for _, r := range t.newRes {
		if _, tomb := t.store.releaseTombstones[r.OrderID]; tomb {
			return ErrOrderAlreadyReleased
		}
		for _, existing := range t.store.reservations {
			if existing.OrderID == r.OrderID && existing.SKUID == r.SKUID {
				return ErrDuplicateReservation
			}
		}
	}

	// 2b. Validate staged release tombstones did not race with a newly committed RESERVED row.
	for ordID := range t.stagedTombstones {
		for _, existing := range t.store.reservations {
			if existing.OrderID == ordID && existing.Status == ReservationStatusReserved {
				return ErrVersionConflict
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
			if cur.Status == SKUStatusInactive || t.deactivatedSKUs[op.skuID] {
				return ErrSKUInactive
			}
			if cur.Version != op.expectedVersion {
				return ErrVersionConflict
			}
			if op.amount < cur.ReservedStock {
				return ErrInvalidStock
			}
			cur.Stock = op.amount
			if cur.Stock == 0 {
				cur.Status = SKUStatusOutOfStock
			} else {
				cur.Status = SKUStatusActive
			}
			cur.Version++
			cur.UpdatedAt = now
		case skuOpReactivate:
			if cur.Status != SKUStatusInactive && !t.deactivatedSKUs[op.skuID] {
				return ErrVersionConflict
			}
			if cur.Version != op.expectedVersion {
				return ErrVersionConflict
			}
			if op.amount < cur.ReservedStock {
				return ErrInvalidStock
			}
			cur.Stock = op.amount
			if cur.Stock == 0 {
				cur.Status = SKUStatusOutOfStock
			} else {
				cur.Status = SKUStatusActive
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
		filteredFees := t.store.feeSchedules[:0]
		for _, f := range t.store.feeSchedules {
			if f.CategoryID != id {
				filteredFees = append(filteredFees, f)
			}
		}
		t.store.feeSchedules = filteredFees
	}
	for id, c := range t.stagedCats {
		t.store.categories[id] = c
	}
	for _, f := range t.stagedFeeSchedules {
		t.store.nextFeeSeq++
		f.Seq = t.store.nextFeeSeq
		t.store.feeSchedules = append(t.store.feeSchedules, f)
	}
	for id, p := range t.stagedProds {
		t.store.products[id] = cloneProduct(p)
	}
	for id, s := range t.stagedSKUs {
		if _, hasOp := computedSKUs[id]; !hasOp && !duplicateRestockSKUs[id] {
			if t.updatedSKUMeta[id] && !t.deactivatedSKUs[id] {
				if existing, exists := t.store.skus[id]; exists {
					existing.SKUCode = s.SKUCode
					existing.Name = s.Name
					existing.Attributes = cloneMap(s.Attributes)
					existing.PriceVND = s.PriceVND
					existing.WeightGrams = s.WeightGrams
					existing.ImageURL = s.ImageURL
					existing.UpdatedAt = now
					t.store.skus[id] = cloneSKU(existing)
					continue
				}
			}
			t.store.skus[id] = cloneSKU(s)
		}
	}
	for id, s := range computedSKUs {
		if t.updatedSKUMeta[id] {
			if staged, ok := t.stagedSKUs[id]; ok {
				s.SKUCode = staged.SKUCode
				s.Name = staged.Name
				s.Attributes = cloneMap(staged.Attributes)
				s.PriceVND = staged.PriceVND
				s.WeightGrams = staged.WeightGrams
				s.ImageURL = staged.ImageURL
			}
		}
		t.store.skus[id] = cloneSKU(s)
	}
	for _, ph := range t.stagedPriceHistory {
		t.store.nextPriceSeq++
		ph.Seq = t.store.nextPriceSeq
		t.store.priceHistory = append(t.store.priceHistory, ph)
	}
	for _, r := range t.newRes {
		t.store.reservations[r.ID] = r
	}
	for ordID, ts := range t.stagedTombstones {
		if _, exists := t.store.releaseTombstones[ordID]; !exists {
			t.store.releaseTombstones[ordID] = ts
		}
	}
	for _, tr := range t.resTransitions {
		r := t.store.reservations[tr.id]
		r.Status = tr.toStatus
		r.UpdatedAt = now
		t.store.reservations[tr.id] = r
	}
	t.store.history = append(t.store.history, filteredHistory...)
	for _, ev := range t.stagedOutbox {
		ev.PayloadJSON = append([]byte(nil), ev.PayloadJSON...)
		ev.AggregateID = OutboxAggregateKey(ev)
		t.store.nextOutboxSeq++
		ev.Seq = t.store.nextOutboxSeq
		t.store.outbox = append(t.store.outbox, ev)
	}
	return nil
}

func (t *memTx) LockCategoryTree(_ context.Context) error {
	return nil
}

func (t *memTx) CreateCategory(ctx context.Context, cat Category) (Category, error) {
	now := AsOfFromContext(ctx)
	cat.CreatedAt = now
	cat.UpdatedAt = now
	t.store.mu.Lock()
	for id, c := range t.store.categories {
		if !t.deletedCats[id] && c.Slug == cat.Slug {
			t.store.mu.Unlock()
			return Category{}, ErrAlreadyExists
		}
	}
	t.store.mu.Unlock()
	for _, c := range t.stagedCats {
		if c.Slug == cat.Slug {
			return Category{}, ErrAlreadyExists
		}
	}
	t.stagedCats[cat.ID] = cat
	return cat, nil
}

func (t *memTx) GetCategoryByID(ctx context.Context, id string) (Category, error) {
	if t.deletedCats[id] {
		return Category{}, ErrNotFound
	}
	asOf := AsOfFromContext(ctx)
	if c, ok := t.stagedCats[id]; ok {
		t.store.mu.Lock()
		c.CommissionRateBps = t.store.effectiveCommissionBpsLocked(c.ID, c.CommissionRateBps, asOf, t.stagedFeeSchedules)
		t.store.mu.Unlock()
		return c, nil
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	c, ok := t.store.categories[id]
	if !ok {
		return Category{}, ErrNotFound
	}
	c.CommissionRateBps = t.store.effectiveCommissionBpsLocked(c.ID, c.CommissionRateBps, asOf, t.stagedFeeSchedules)
	return c, nil
}

func (t *memTx) GetCategoryBySlug(_ context.Context, slug string) (Category, error) {
	for _, c := range t.stagedCats {
		if c.Slug == slug {
			return c, nil
		}
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	for id, c := range t.store.categories {
		if t.deletedCats[id] {
			continue
		}
		if staged, ok := t.stagedCats[id]; ok {
			if staged.Slug == slug {
				return staged, nil
			}
			continue
		}
		if c.Slug == slug {
			return c, nil
		}
	}
	return Category{}, ErrNotFound
}

func (t *memTx) UpdateCategory(ctx context.Context, cat Category) (Category, error) {
	cur, err := t.GetCategoryByID(ctx, cat.ID)
	if err != nil {
		return Category{}, err
	}
	cat.CreatedAt = cur.CreatedAt
	cat.UpdatedAt = AsOfFromContext(ctx)
	t.stagedCats[cat.ID] = cat
	return cat, nil
}

func (t *memTx) UpdateCategoryCommissionRate(ctx context.Context, id string, bps int32) error {
	cur, err := t.GetCategoryByID(ctx, id)
	if err != nil {
		return err
	}
	cur.CommissionRateBps = bps
	cur.UpdatedAt = AsOfFromContext(ctx)
	t.stagedCats[id] = cur
	return nil
}

func (t *memTx) CountProductsInCategory(ctx context.Context, categoryID string) (CategoryProductCounts, error) {
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

func (t *memTx) CategoryHasAncestor(_ context.Context, startID, targetAncestorID string) (bool, error) {
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
	return categoryHasAncestorInMap(merged, startID, targetAncestorID), nil
}

func (t *memTx) GetCategoryDescendants(_ context.Context, categoryIDs []string) ([]string, error) {
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
	return collectCategoryDescendants(merged, categoryIDs), nil
}

func (t *memTx) InsertCategoryFeeSchedule(ctx context.Context, sched CategoryFeeSchedule) (CategoryFeeSchedule, error) {
	if sched.CreatedAt.IsZero() {
		sched.CreatedAt = AsOfFromContext(ctx)
	}
	sched.EffectiveFrom = sched.EffectiveFrom.UTC()
	sched.CreatedAt = sched.CreatedAt.UTC()
	t.store.mu.Lock()
	baseSeq := t.store.nextFeeSeq
	t.store.mu.Unlock()
	sched.Seq = baseSeq + int64(len(t.stagedFeeSchedules)) + 1
	t.stagedFeeSchedules = append(t.stagedFeeSchedules, sched)
	return sched, nil
}

func (t *memTx) ListCategoryFeeSchedules(ctx context.Context, categoryID string, limit, offset int32) ([]CategoryFeeSchedule, int64, error) {
	return t.store.ListCategoryFeeSchedules(ctx, categoryID, limit, offset)
}

func (t *memTx) CreateProduct(ctx context.Context, p Product) (Product, error) {
	now := AsOfFromContext(ctx)
	p.SearchText = BuildProductSearchText(p.Name, p.Description)
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

func (t *memTx) GetProductByIDForUpdate(ctx context.Context, id string) (Product, error) {
	return t.GetProductByID(ctx, id)
}

func (t *memTx) GetProductBySlug(_ context.Context, slug string) (Product, error) {
	for _, p := range t.stagedProds {
		if p.DeletedAt == nil && p.Slug != "" && p.Slug == slug {
			return cloneProduct(p), nil
		}
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	for id, p := range t.store.products {
		if staged, ok := t.stagedProds[id]; ok {
			if staged.DeletedAt == nil && staged.Slug != "" && staged.Slug == slug {
				return cloneProduct(staged), nil
			}
			continue
		}
		if p.DeletedAt == nil && p.Slug != "" && p.Slug == slug {
			return cloneProduct(p), nil
		}
	}
	return Product{}, ErrNotFound
}

func (t *memTx) UpdateProduct(ctx context.Context, p Product) (Product, error) {
	cur, err := t.GetProductByID(ctx, p.ID)
	if err != nil {
		return Product{}, err
	}
	p.ShopID = cur.ShopID
	p.SearchText = BuildProductSearchText(p.Name, p.Description)
	p.CreatedAt = cur.CreatedAt
	p.UpdatedAt = AsOfFromContext(ctx)
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
	cur.UpdatedAt = AsOfFromContext(ctx)
	t.stagedProds[id] = cloneProduct(cur)
	return cloneProduct(cur), nil
}

func (t *memTx) UpdateProductStatus(ctx context.Context, id, status string) error {
	cur, err := t.GetProductByID(ctx, id)
	if err != nil {
		return err
	}
	cur.Status = status
	cur.UpdatedAt = AsOfFromContext(ctx)
	t.stagedProds[id] = cloneProduct(cur)
	return nil
}

func (t *memTx) SoftDeleteProduct(ctx context.Context, id string) (bool, error) {
	cur, err := t.GetProductByID(ctx, id)
	if err != nil {
		return false, nil
	}
	now := AsOfFromContext(ctx)
	cur.DeletedAt = &now
	cur.Status = ProductStatusHidden
	cur.UpdatedAt = now
	t.stagedProds[id] = cloneProduct(cur)
	return true, nil
}

func (t *memTx) ListProducts(ctx context.Context, filter ListProductsFilter) ([]Product, int64, error) {
	return t.store.ListProducts(ctx, filter)
}

func (t *memTx) CreateSKU(ctx context.Context, s SKU) (SKU, error) {
	now := AsOfFromContext(ctx).Add(time.Duration(len(t.stagedSKUs)) * time.Nanosecond)
	s.ReservedStock = 0
	s.Version = 1
	s.CreatedAt = now
	s.UpdatedAt = now
	if s.MinPrice30dVND <= 0 {
		s.MinPrice30dVND = s.PriceVND
	}
	t.stagedSKUs[s.ID] = cloneSKU(s)
	return cloneSKU(s), nil
}

func (t *memTx) InsertSKUPriceHistory(ctx context.Context, h SKUPriceHistoryEntry) error {
	if h.RecordedAt.IsZero() {
		h.RecordedAt = AsOfFromContext(ctx)
	}
	h.RecordedAt = h.RecordedAt.UTC()
	h.Seq = int64(len(t.stagedPriceHistory)) + 1_000_000
	t.stagedPriceHistory = append(t.stagedPriceHistory, h)
	return nil
}

func (t *memTx) UpdateSKUMetadata(ctx context.Context, s SKU) (SKU, error) {
	cur, err := t.GetSKUWithContext(ctx, s.ID)
	if err != nil {
		return SKU{}, err
	}
	cur.SKUCode = s.SKUCode
	cur.Name = s.Name
	cur.Attributes = cloneMap(s.Attributes)
	cur.PriceVND = s.PriceVND
	cur.WeightGrams = s.WeightGrams
	cur.ImageURL = s.ImageURL
	cur.UpdatedAt = AsOfFromContext(ctx)
	t.stagedSKUs[cur.ID] = cloneSKU(cur)
	t.updatedSKUMeta[cur.ID] = true
	return cloneSKU(cur), nil
}

func (t *memTx) ReactivateSKUOptimistic(ctx context.Context, s SKU, expectedVersion int64) (SKU, error) {
	if hook := t.store.BeforeUpdateStockWrite; hook != nil {
		hook(s.ID)
	}
	cur, err := t.GetSKUWithContext(ctx, s.ID)
	if err != nil {
		return SKU{}, err
	}
	if cur.Status != SKUStatusInactive {
		return SKU{}, ErrVersionConflict
	}
	if cur.Version != expectedVersion {
		return SKU{}, ErrVersionConflict
	}
	if s.Stock < cur.ReservedStock {
		return SKU{}, ErrInvalidStock
	}
	t.skuOps = append(t.skuOps, stagedSKUOp{
		kind:            skuOpReactivate,
		skuID:           s.ID,
		amount:          s.Stock,
		expectedVersion: expectedVersion,
	})
	cur.SKUCode = s.SKUCode
	cur.Name = s.Name
	cur.Attributes = cloneMap(s.Attributes)
	cur.PriceVND = s.PriceVND
	cur.Stock = s.Stock
	cur.WeightGrams = s.WeightGrams
	cur.ImageURL = s.ImageURL
	if cur.Stock == 0 {
		cur.Status = SKUStatusOutOfStock
	} else {
		cur.Status = SKUStatusActive
	}
	cur.Version++
	cur.UpdatedAt = AsOfFromContext(ctx)
	t.stagedSKUs[cur.ID] = cloneSKU(cur)
	t.updatedSKUMeta[cur.ID] = true
	return cloneSKU(cur), nil
}

func (t *memTx) DeactivateSKUsByProduct(_ context.Context, productID string) error {
	t.store.mu.Lock()
	for id, s := range t.store.skus {
		if s.ProductID == productID {
			cp := cloneSKU(s)
			cp.Status = SKUStatusInactive
			t.stagedSKUs[id] = cp
			t.deactivatedSKUs[id] = true
		}
	}
	t.store.mu.Unlock()
	for id, s := range t.stagedSKUs {
		if s.ProductID == productID {
			s.Status = SKUStatusInactive
			t.stagedSKUs[id] = s
			t.deactivatedSKUs[id] = true
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
	t.deactivatedSKUs[skuID] = true
	return nil
}

func (t *memTx) ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]SKU, error) {
	asOf := AsOfFromContext(ctx)
	want := make(map[string]bool, len(productIDs))
	for _, id := range productIDs {
		want[id] = true
	}
	merged := make(map[string]SKU)
	t.store.mu.Lock()
	for id, s := range t.store.skus {
		if want[s.ProductID] {
			merged[id] = t.store.enrichSKULocked(s, asOf)
		}
	}
	t.store.mu.Unlock()
	for id, s := range t.stagedSKUs {
		if want[s.ProductID] {
			merged[id] = t.enrichWithStaged(s, asOf)
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

func (t *memTx) ListAllSKUsByProductID(ctx context.Context, productID string) ([]SKU, error) {
	asOf := AsOfFromContext(ctx)
	merged := make(map[string]SKU)
	t.store.mu.Lock()
	for id, s := range t.store.skus {
		if s.ProductID == productID {
			merged[id] = t.store.enrichSKULocked(s, asOf)
		}
	}
	t.store.mu.Unlock()
	for id, s := range t.stagedSKUs {
		if s.ProductID == productID {
			merged[id] = t.enrichWithStaged(s, asOf)
		}
	}
	out := make([]SKU, 0, len(merged))
	for _, s := range merged {
		out = append(out, s)
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

func (t *memTx) GetSKUWithContext(ctx context.Context, skuID string) (SKU, error) {
	asOf := AsOfFromContext(ctx)
	if s, ok := t.stagedSKUs[skuID]; ok {
		return t.enrichWithStaged(s, asOf), nil
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	s, ok := t.store.skus[skuID]
	if !ok {
		return SKU{}, ErrNotFound
	}
	return t.store.enrichSKULocked(s, asOf), nil
}

func (t *memTx) GetSKUWithContextForUpdate(ctx context.Context, skuID string) (SKU, error) {
	return t.GetSKUWithContext(ctx, skuID)
}

func (t *memTx) enrichWithStaged(s SKU, asOf time.Time) SKU {
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
	t.store.mu.Lock()
	var fallbackBps int32
	if c, ok := t.stagedCats[out.CategoryID]; ok {
		fallbackBps = c.CommissionRateBps
	} else if c, ok := t.store.categories[out.CategoryID]; ok {
		fallbackBps = c.CommissionRateBps
	}
	out.CommissionRateBps = t.store.effectiveCommissionBpsLocked(out.CategoryID, fallbackBps, asOf, t.stagedFeeSchedules)
	out.MinPrice30dVND = t.store.minPrice30dLocked(out.ID, out.PriceVND, asOf, t.stagedPriceHistory)
	t.store.mu.Unlock()
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
	if hook := t.store.BeforeUpdateStockWrite; hook != nil {
		hook(skuID)
	}
	cur, err := t.GetSKUWithContext(ctx, skuID)
	if err != nil {
		return SKU{}, err
	}
	if cur.Status == SKUStatusInactive {
		return SKU{}, ErrSKUInactive
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
	if cur.Stock == 0 {
		cur.Status = SKUStatusOutOfStock
	} else {
		cur.Status = SKUStatusActive
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

func (t *memTx) LockStockOrder(_ context.Context, _ string) error {
	return nil
}

func (t *memTx) InsertStockReleaseTombstone(ctx context.Context, orderID string) (bool, error) {
	if _, ok := t.stagedTombstones[orderID]; ok {
		return false, nil
	}
	t.store.mu.Lock()
	_, exists := t.store.releaseTombstones[orderID]
	t.store.mu.Unlock()
	if exists {
		return false, nil
	}
	t.stagedTombstones[orderID] = AsOfFromContext(ctx)
	return true, nil
}

func (t *memTx) HasStockReleaseTombstone(_ context.Context, orderID string) (bool, error) {
	if _, ok := t.stagedTombstones[orderID]; ok {
		return true, nil
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	_, ok := t.store.releaseTombstones[orderID]
	return ok, nil
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

func (t *memTx) InsertOutboxEvent(ctx context.Context, ev OutboxEvent) error {
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = AsOfFromContext(ctx)
	}
	ev.CreatedAt = ev.CreatedAt.UTC()
	ev.PayloadJSON = append([]byte(nil), ev.PayloadJSON...)
	t.stagedOutbox = append(t.stagedOutbox, ev)
	return nil
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
