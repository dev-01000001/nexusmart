package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

// MemoryStore is a concurrency-safe in-memory implementation of Store for unit and race testing.
type MemoryStore struct {
	mu                sync.Mutex
	vouchers          map[string]domain.Voucher
	voucherByCode     map[string]string
	userVouchers      map[string]map[string]bool
	voucherNotifiedAt map[string]time.Time // key: userID + "|" + voucherID
	flashSales        map[string]domain.FlashSale
	flashItems        map[string]domain.FlashSaleItem
	shopPrograms      map[string]domain.ShopDiscountProgram
	shopProgramItems  map[string]domain.ShopDiscountProgramItem
	coinBalances      map[string]int64
	coinTxns          []domain.CoinTransaction
	coinRefIndex      map[string]bool
	reservations      map[string]domain.PromotionReservation
	userUsage         map[string]int32 // key: kind + "|" + targetID + "|" + userID
	processedEvents   map[string]bool
}

// NewMemoryStore initializes an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		vouchers:          make(map[string]domain.Voucher),
		voucherByCode:     make(map[string]string),
		userVouchers:      make(map[string]map[string]bool),
		voucherNotifiedAt: make(map[string]time.Time),
		flashSales:        make(map[string]domain.FlashSale),
		flashItems:        make(map[string]domain.FlashSaleItem),
		shopPrograms:      make(map[string]domain.ShopDiscountProgram),
		shopProgramItems:  make(map[string]domain.ShopDiscountProgramItem),
		coinBalances:      make(map[string]int64),
		coinRefIndex:      make(map[string]bool),
		reservations:      make(map[string]domain.PromotionReservation),
		userUsage:         make(map[string]int32),
		processedEvents:   make(map[string]bool),
	}
}

func (m *MemoryStore) Migrate(context.Context) error { return nil }
func (m *MemoryStore) Ping(context.Context) error    { return nil }

func (m *MemoryStore) CreateVoucher(_ context.Context, v domain.Voucher) (*domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.voucherByCode[v.Code]; exists {
		return nil, ErrDuplicateCode
	}
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	v.CreatedAt = now
	v.UpdatedAt = now
	v.UsedCount = 0
	v.UsedBudgetVND = 0
	v.RulesVersion = 1
	v.RedisSyncVersion = 1
	v.RedisSyncedVersion = 0
	if v.Status == "" {
		if v.IsActive {
			v.Status = domain.VoucherStatusActive
		} else {
			v.Status = domain.VoucherStatusPaused
		}
	}
	v.IsActive = v.Status == domain.VoucherStatusActive
	v.CategoryIDs = append([]string(nil), v.CategoryIDs...)

	m.vouchers[v.ID] = cloneVoucher(v)
	m.voucherByCode[v.Code] = v.ID
	cp := cloneVoucher(v)
	return &cp, nil
}

func (m *MemoryStore) GetVoucherByID(_ context.Context, id string) (*domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.vouchers[id]; ok {
		cp := cloneVoucher(v)
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) GetVoucherByIDOrCode(_ context.Context, idOrCode string) (*domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.vouchers[idOrCode]; ok {
		cp := cloneVoucher(v)
		return &cp, nil
	}
	if id, ok := m.voucherByCode[idOrCode]; ok {
		cp := cloneVoucher(m.vouchers[id])
		return &cp, nil
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) UpdateVoucher(
	_ context.Context,
	id string,
	mutate func(cur *domain.Voucher, hasActiveReservation bool) error,
) (*domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.vouchers[id]
	if !ok {
		return nil, ErrNotFound
	}
	hasActiveReservation := m.hasActiveVoucherReservationLocked(&existing)
	cur := cloneVoucher(existing)
	if err := mutate(&cur, hasActiveReservation); err != nil {
		return nil, err
	}
	cur.RedisSyncVersion++
	cur.UpdatedAt = time.Now().UTC()
	m.vouchers[id] = cloneVoucher(cur)
	after := cloneVoucher(cur)
	return &after, nil
}

func (m *MemoryStore) hasActiveVoucherReservationLocked(v *domain.Voucher) bool {
	for _, res := range m.reservations {
		if res.Status != domain.ReservationStatusReserved {
			continue
		}
		released := make(map[string]bool, len(res.ReleasedShops))
		for _, s := range res.ReleasedShops {
			released[s] = true
		}
		for _, rv := range res.Vouchers {
			if rv.VoucherID != v.ID {
				continue
			}
			if v.Scope == domain.VoucherScopeShop && v.ShopID != "" && released[v.ShopID] {
				continue
			}
			return true
		}
	}
	return false
}

func (m *MemoryStore) SetVoucherStatus(
	_ context.Context,
	id, newStatus string,
	check func(cur *domain.Voucher) error,
) (*domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.vouchers[id]
	if !ok {
		return nil, ErrNotFound
	}
	cur := cloneVoucher(existing)
	if check != nil {
		if err := check(&cur); err != nil {
			return nil, err
		}
	}
	cur.Status = newStatus
	cur.IsActive = newStatus == domain.VoucherStatusActive
	cur.RedisSyncVersion++
	cur.UpdatedAt = time.Now().UTC()
	m.vouchers[id] = cloneVoucher(cur)
	after := cloneVoucher(cur)
	return &after, nil
}

func (m *MemoryStore) DeleteVoucher(
	_ context.Context,
	id string,
	check func(cur *domain.Voucher) error,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.vouchers[id]
	if !ok {
		return false, ErrNotFound
	}
	cur := cloneVoucher(existing)
	if check != nil {
		if err := check(&cur); err != nil {
			return false, err
		}
	}
	if cur.EffectiveStatus() == domain.VoucherStatusDeleted {
		return true, nil
	}
	now := time.Now().UTC()
	cur.Status = domain.VoucherStatusDeleted
	cur.IsActive = false
	cur.DeletedAt = &now
	cur.RedisSyncVersion++
	cur.UpdatedAt = now
	m.vouchers[id] = cloneVoucher(cur)
	return false, nil
}

func (m *MemoryStore) SyncVoucherRedis(
	ctx context.Context,
	voucherID string,
	_ bool,
	syncFn func(ctx context.Context, v *domain.Voucher) error,
) (bool, error) {
	return m.syncVoucherRedisLocked(ctx, voucherID, false, syncFn)
}

func (m *MemoryStore) syncVoucherRedisLocked(
	ctx context.Context,
	voucherID string,
	onlyIfDirty bool,
	syncFn func(ctx context.Context, v *domain.Voucher) error,
) (bool, error) {
	if voucherID == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	cur, ok := m.vouchers[voucherID]
	if !ok {
		return false, nil
	}
	if onlyIfDirty && cur.RedisSyncedVersion >= cur.RedisSyncVersion {
		return false, nil
	}
	cp := cloneVoucher(cur)
	if syncFn != nil {
		if err := syncFn(ctx, &cp); err != nil {
			return false, err
		}
	}
	if cur.RedisSyncedVersion < cur.RedisSyncVersion {
		cur.RedisSyncedVersion = cur.RedisSyncVersion
		m.vouchers[cur.ID] = cur
	}
	return true, nil
}

func (m *MemoryStore) ReconcilePendingVouchers(
	ctx context.Context,
	limit int,
	syncFn func(ctx context.Context, v *domain.Voucher) error,
) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	m.mu.Lock()
	pending := make([]domain.Voucher, 0)
	for _, v := range m.vouchers {
		if v.RedisSyncedVersion < v.RedisSyncVersion {
			pending = append(pending, cloneVoucher(v))
		}
	}
	m.mu.Unlock()

	sort.Slice(pending, func(i, j int) bool {
		if !pending[i].UpdatedAt.Equal(pending[j].UpdatedAt) {
			return pending[i].UpdatedAt.Before(pending[j].UpdatedAt)
		}
		return pending[i].ID < pending[j].ID
	})
	if len(pending) > limit {
		pending = pending[:limit]
	}

	reconciled := 0
	var firstErr error
	for i := range pending {
		ok, err := m.syncVoucherRedisLocked(ctx, pending[i].ID, true, syncFn)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if IsRedisConnOrTimeoutErr(err) {
				break
			}
			continue
		}
		if ok {
			reconciled++
		}
	}
	return reconciled, firstErr
}

func (m *MemoryStore) GetUserUsageBatch(_ context.Context, userID string, targets []UserUsageKey) (map[UserUsageKey]int32, error) {
	out := make(map[UserUsageKey]int32, len(targets))
	if userID == "" || len(targets) == 0 {
		return out, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range targets {
		if t.Kind == "" || t.TargetID == "" {
			continue
		}
		if qty, ok := m.userUsage[memUsageKey(t.Kind, t.TargetID, userID)]; ok {
			out[t] = qty
		}
	}
	return out, nil
}

func (m *MemoryStore) SyncUserUsageRedis(
	ctx context.Context,
	kind, targetID, userID string,
	_ bool,
	syncFn func(ctx context.Context, usedQty int32) error,
) (bool, error) {
	if kind == "" || targetID == "" || userID == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	qty := m.userUsage[memUsageKey(kind, targetID, userID)]
	if syncFn != nil {
		if err := syncFn(ctx, qty); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (m *MemoryStore) ListVouchers(_ context.Context, shopID, scope string) ([]domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []domain.Voucher
	for _, v := range m.vouchers {
		if shopID != "" && v.ShopID != shopID {
			continue
		}
		if scope != "" && v.Scope != scope {
			continue
		}
		out = append(out, cloneVoucher(v))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryStore) ListAllVouchers(_ context.Context) ([]domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]domain.Voucher, 0, len(m.vouchers))
	for _, v := range m.vouchers {
		out = append(out, cloneVoucher(v))
	}
	return out, nil
}

func (m *MemoryStore) ListActiveVouchers(_ context.Context, now time.Time) ([]domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []domain.Voucher
	for _, v := range m.vouchers {
		if v.EffectiveStatus() == domain.VoucherStatusActive && v.IsActive && v.DeletedAt == nil && !now.Before(v.StartsAt) && !now.After(v.EndsAt) {
			out = append(out, cloneVoucher(v))
		}
	}
	return out, nil
}

func (m *MemoryStore) SaveUserVoucher(_ context.Context, userID, voucherID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.userVouchers[userID] == nil {
		m.userVouchers[userID] = make(map[string]bool)
	}
	if m.userVouchers[userID][voucherID] {
		return true, nil
	}
	m.userVouchers[userID][voucherID] = true
	return false, nil
}

func (m *MemoryStore) GetUserSavedVoucherIDs(_ context.Context, userID string) (map[string]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make(map[string]bool)
	for vid := range m.userVouchers[userID] {
		out[vid] = true
	}
	return out, nil
}

func (m *MemoryStore) ProcessExpiringSavedVouchers(
	ctx context.Context,
	now time.Time,
	horizon time.Duration,
	batchSize int,
	notifyFn func(ctx context.Context, item domain.ExpiringSavedVoucher) error,
) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if batchSize <= 0 {
		batchSize = 100
	}
	cutoff := now.UTC().Add(horizon)

	candidates := make([]domain.ExpiringSavedVoucher, 0)
	for userID, saved := range m.userVouchers {
		for vid := range saved {
			notifKey := userID + "|" + vid
			if _, already := m.voucherNotifiedAt[notifKey]; already {
				continue
			}
			v, ok := m.vouchers[vid]
			if !ok {
				continue
			}
			if v.EffectiveStatus() != domain.VoucherStatusActive || !v.IsActive {
				continue
			}
			if v.UsedCount >= v.Quota {
				continue
			}
			if v.BudgetVND > 0 && v.UsedBudgetVND >= v.BudgetVND {
				continue
			}
			if v.PerUserLimit > 0 && m.userUsage[memUsageKey(domain.UsageKindVoucher, vid, userID)] >= v.PerUserLimit {
				continue
			}
			if !v.EndsAt.After(now.UTC()) || v.EndsAt.After(cutoff) {
				continue
			}
			candidates = append(candidates, domain.ExpiringSavedVoucher{
				UserID:    userID,
				VoucherID: vid,
				Code:      v.Code,
				EndsAt:    v.EndsAt,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].EndsAt.Equal(candidates[j].EndsAt) {
			return candidates[i].EndsAt.Before(candidates[j].EndsAt)
		}
		if candidates[i].UserID != candidates[j].UserID {
			return candidates[i].UserID < candidates[j].UserID
		}
		return candidates[i].VoucherID < candidates[j].VoucherID
	})
	if len(candidates) > batchSize {
		candidates = candidates[:batchSize]
	}

	sent := 0
	var firstErr error
	for _, it := range candidates {
		if notifyFn != nil {
			if pubErr := notifyFn(ctx, it); pubErr != nil {
				if firstErr == nil {
					firstErr = pubErr
				}
				continue
			}
		}
		m.voucherNotifiedAt[it.UserID+"|"+it.VoucherID] = now.UTC()
		sent++
	}
	return sent, len(candidates), firstErr
}

func (m *MemoryStore) CreateFlashSale(_ context.Context, fs domain.FlashSale) (*domain.FlashSale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if fs.ID == "" {
		fs.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	fs.CreatedAt = now
	fs.UpdatedAt = now
	fs.CategoryIDs = append([]string(nil), fs.CategoryIDs...)
	m.flashSales[fs.ID] = fs
	cp := fs
	cp.CategoryIDs = append([]string(nil), fs.CategoryIDs...)
	return &cp, nil
}

func (m *MemoryStore) GetFlashSale(_ context.Context, id string) (*domain.FlashSale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fs, ok := m.flashSales[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := fs
	cp.CategoryIDs = append([]string(nil), fs.CategoryIDs...)
	return &cp, nil
}

func (m *MemoryStore) RegisterFlashSaleItem(_ context.Context, item domain.FlashSaleItem, now time.Time) (*domain.FlashSaleItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	fs, ok := m.flashSales[item.FlashSaleID]
	if !ok {
		return nil, ErrNotFound
	}
	if !fs.EndsAt.After(now) {
		return nil, ErrFlashSaleEnded
	}

	for _, existing := range m.flashItems {
		if existing.SKUID != item.SKUID {
			continue
		}
		if existing.Status != domain.FlashStatusPending && existing.Status != domain.FlashStatusApproved {
			continue
		}
		otherFS := m.flashSales[existing.FlashSaleID]
		if otherFS.StartsAt.Before(fs.EndsAt) && otherFS.EndsAt.After(fs.StartsAt) {
			return nil, ErrOverlappingFlashSale
		}
	}

	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	item.RemainingSlots = item.TotalSlots
	item.Status = domain.FlashStatusPending
	item.RejectionReason = ""
	item.StartsAt = fs.StartsAt
	item.EndsAt = fs.EndsAt
	item.CreatedAt = now.UTC()
	item.UpdatedAt = now.UTC()

	m.flashItems[item.ID] = item
	cp := item
	return &cp, nil
}

func (m *MemoryStore) ApproveFlashSaleItem(
	_ context.Context,
	itemID, adminID string,
	approve bool,
	reason string,
	originalPriceVND int64,
	now time.Time,
) (*domain.FlashSaleItem, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	item, ok := m.flashItems[itemID]
	if !ok {
		return nil, "", ErrNotFound
	}
	fs, fsOk := m.flashSales[item.FlashSaleID]
	if fsOk {
		item.StartsAt = fs.StartsAt
		item.EndsAt = fs.EndsAt
	}

	prevStatus := item.Status
	newStatus := domain.FlashStatusRejected
	if approve {
		newStatus = domain.FlashStatusApproved
	}
	if prevStatus == newStatus {
		cp := item
		return &cp, prevStatus, nil
	}
	if prevStatus != domain.FlashStatusPending {
		return nil, prevStatus, fmt.Errorf("%w: cannot transition flash sale item from %s to %s", ErrInvalidFlashTransition, prevStatus, newStatus)
	}

	if approve {
		if fsOk && !fs.EndsAt.After(now) {
			return nil, "", ErrFlashSaleEnded
		}
		for _, other := range m.flashItems {
			if other.ID == item.ID || other.SKUID != item.SKUID {
				continue
			}
			if other.Status != domain.FlashStatusPending && other.Status != domain.FlashStatusApproved {
				continue
			}
			otherFS := m.flashSales[other.FlashSaleID]
			if otherFS.StartsAt.Before(item.EndsAt) && otherFS.EndsAt.After(item.StartsAt) {
				return nil, "", ErrOverlappingFlashSale
			}
		}
	}

	if approve {
		item.Status = domain.FlashStatusApproved
		item.RejectionReason = ""
		if originalPriceVND > 0 {
			item.OriginalPriceVND = originalPriceVND
		}
	} else {
		item.Status = domain.FlashStatusRejected
		item.RejectionReason = reason
	}
	item.ApprovedBy = adminID
	item.UpdatedAt = time.Now().UTC()
	m.flashItems[itemID] = item
	cp := item
	return &cp, prevStatus, nil
}

func (m *MemoryStore) GetFlashSaleItem(_ context.Context, itemID string) (*domain.FlashSaleItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.flashItems[itemID]
	if !ok {
		return nil, ErrNotFound
	}
	if fs, ok := m.flashSales[item.FlashSaleID]; ok {
		item.StartsAt = fs.StartsAt
		item.EndsAt = fs.EndsAt
	}
	cp := item
	return &cp, nil
}

func (m *MemoryStore) SyncFlashItemRedis(
	ctx context.Context,
	itemID string,
	_ bool,
	syncFn func(ctx context.Context, item *domain.FlashSaleItem) error,
) (bool, error) {
	if itemID == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.flashItems[itemID]
	if !ok {
		return false, nil
	}
	if fs, fsOk := m.flashSales[item.FlashSaleID]; fsOk {
		item.StartsAt = fs.StartsAt
		item.EndsAt = fs.EndsAt
	}
	cp := item
	if syncFn != nil {
		if err := syncFn(ctx, &cp); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (m *MemoryStore) ListFlashSales(_ context.Context, now time.Time, includeUpcoming bool) ([]domain.FlashSale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	var out []domain.FlashSale
	for _, fs := range m.flashSales {
		if includeUpcoming {
			if !fs.EndsAt.After(now) {
				continue
			}
		} else {
			if fs.StartsAt.After(now) || !fs.EndsAt.After(now) {
				continue
			}
		}
		cp := m.attachApprovedFlashItemsLocked(fs)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartsAt.Equal(out[j].StartsAt) {
			return out[i].StartsAt.Before(out[j].StartsAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryStore) ListAllFlashSales(_ context.Context, now time.Time, statusFilter string, page, pageSize int32) ([]domain.FlashSale, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	page, pageSize = normalizePage(page, pageSize)

	filtered := make([]domain.FlashSale, 0, len(m.flashSales))
	for _, fs := range m.flashSales {
		switch statusFilter {
		case "UPCOMING":
			if !fs.StartsAt.After(now) {
				continue
			}
		case "ACTIVE":
			if fs.StartsAt.After(now) || !fs.EndsAt.After(now) {
				continue
			}
		case "ENDED":
			if fs.EndsAt.After(now) {
				continue
			}
		}
		filtered = append(filtered, m.attachApprovedFlashItemsLocked(fs))
	}
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].StartsAt.Equal(filtered[j].StartsAt) {
			return filtered[i].StartsAt.After(filtered[j].StartsAt)
		}
		return filtered[i].ID < filtered[j].ID
	})

	total := int64(len(filtered))
	start := int((page - 1) * pageSize)
	if start >= len(filtered) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, nil
}

func (m *MemoryStore) attachApprovedFlashItemsLocked(fs domain.FlashSale) domain.FlashSale {
	cp := fs
	cp.CategoryIDs = append([]string(nil), fs.CategoryIDs...)
	cp.Items = nil
	for _, item := range m.flashItems {
		if item.FlashSaleID == fs.ID && item.Status == domain.FlashStatusApproved {
			item.StartsAt = fs.StartsAt
			item.EndsAt = fs.EndsAt
			cp.Items = append(cp.Items, item)
		}
	}
	sort.Slice(cp.Items, func(i, j int) bool { return cp.Items[i].ID < cp.Items[j].ID })
	return cp
}

func (m *MemoryStore) ListFlashSaleItems(_ context.Context, statusFilter, flashSaleID, shopID string, page, pageSize int32) ([]domain.FlashSaleItem, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	page, pageSize = normalizePage(page, pageSize)
	filtered := make([]domain.FlashSaleItem, 0, len(m.flashItems))
	for _, it := range m.flashItems {
		if statusFilter != "" && it.Status != statusFilter {
			continue
		}
		if flashSaleID != "" && it.FlashSaleID != flashSaleID {
			continue
		}
		if shopID != "" && it.ShopID != shopID {
			continue
		}
		if fs, ok := m.flashSales[it.FlashSaleID]; ok {
			it.StartsAt = fs.StartsAt
			it.EndsAt = fs.EndsAt
		}
		filtered = append(filtered, it)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
		}
		return filtered[i].ID < filtered[j].ID
	})

	total := int64(len(filtered))
	start := int((page - 1) * pageSize)
	if start >= len(filtered) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, nil
}

func (m *MemoryStore) ListApprovedFlashSaleItems(_ context.Context) ([]domain.FlashSaleItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []domain.FlashSaleItem
	for _, item := range m.flashItems {
		if item.Status == domain.FlashStatusApproved {
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *MemoryStore) ListActiveApprovedFlashSaleItems(_ context.Context, now time.Time) ([]domain.FlashSaleItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []domain.FlashSaleItem
	for _, item := range m.flashItems {
		if item.Status != domain.FlashStatusApproved {
			continue
		}
		if fs, ok := m.flashSales[item.FlashSaleID]; ok && fs.EndsAt.After(now) {
			item.StartsAt = fs.StartsAt
			item.EndsAt = fs.EndsAt
			out = append(out, item)
		}
	}
	return out, nil
}

func (m *MemoryStore) HasOverlappingApprovedFlashSale(_ context.Context, skuID string, startsAt, endsAt, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, it := range m.flashItems {
		if it.SKUID != skuID || it.Status != domain.FlashStatusApproved {
			continue
		}
		fs := m.flashSales[it.FlashSaleID]
		if fs.EndsAt.After(now) && fs.StartsAt.Before(endsAt) && fs.EndsAt.After(startsAt) {
			return true, nil
		}
	}
	return false, nil
}

// --- Shop Discount Programs (UC-S18) ---

func (m *MemoryStore) CreateShopDiscountProgram(_ context.Context, prog domain.ShopDiscountProgram, now time.Time) (*domain.ShopDiscountProgram, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	if prog.ID == "" {
		prog.ID = uuid.NewString()
	}
	prog.StartsAt = prog.StartsAt.UTC()
	prog.EndsAt = prog.EndsAt.UTC()
	prog.Status = prog.EffectiveStatus(now)
	prog.CreatedAt = now.UTC()
	prog.UpdatedAt = now.UTC()

	if err := m.checkShopProgramOverlapLocked("", collectProgramSKUs(prog.Items), prog.StartsAt, prog.EndsAt, now); err != nil {
		return nil, err
	}

	savedItems := make([]domain.ShopDiscountProgramItem, 0, len(prog.Items))
	for _, it := range prog.Items {
		if it.ID == "" {
			it.ID = stableShopDiscountItemID(prog.ID, it.SKUID)
		}
		it.ProgramID = prog.ID
		it.ShopID = prog.ShopID
		it.ProgramStatus = prog.Status
		it.StartsAt = prog.StartsAt
		it.EndsAt = prog.EndsAt
		it.CreatedAt = now.UTC()
		it.UpdatedAt = now.UTC()
		m.shopProgramItems[it.ID] = it
		savedItems = append(savedItems, it)
	}
	prog.Items = savedItems
	m.shopPrograms[prog.ID] = cloneShopProgram(prog)
	cp := cloneShopProgram(prog)
	return &cp, nil
}

func (m *MemoryStore) GetShopDiscountProgram(_ context.Context, programID string) (*domain.ShopDiscountProgram, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.shopPrograms[programID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := cloneShopProgram(p)
	return &cp, nil
}

func (m *MemoryStore) UpdateShopDiscountProgram(
	_ context.Context,
	programID string,
	now time.Time,
	mutate func(cur *domain.ShopDiscountProgram) (replaceItems bool, err error),
) (*domain.ShopDiscountProgram, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	existing, ok := m.shopPrograms[programID]
	if !ok {
		return nil, ErrNotFound
	}
	cur := cloneShopProgram(existing)
	existingBySKU := make(map[string]domain.ShopDiscountProgramItem, len(cur.Items))
	for _, it := range cur.Items {
		existingBySKU[it.SKUID] = it
	}

	replaceItems, err := mutate(&cur)
	if err != nil {
		return nil, err
	}
	cur.StartsAt = cur.StartsAt.UTC()
	cur.EndsAt = cur.EndsAt.UTC()
	cur.Status = cur.EffectiveStatus(now)
	cur.UpdatedAt = now.UTC()

	if err := m.checkShopProgramOverlapLocked(cur.ID, collectProgramSKUs(cur.Items), cur.StartsAt, cur.EndsAt, now); err != nil {
		return nil, err
	}

	if replaceItems {
		// Remove old items for this program from index, preserving IDs for unchanged SKUs
		for id, it := range m.shopProgramItems {
			if it.ProgramID == cur.ID {
				delete(m.shopProgramItems, id)
			}
		}
		nextItems := make([]domain.ShopDiscountProgramItem, 0, len(cur.Items))
		for _, it := range cur.Items {
			if prev, exists := existingBySKU[it.SKUID]; exists {
				it.ID = prev.ID
				it.CreatedAt = prev.CreatedAt
			} else {
				if it.ID == "" {
					it.ID = stableShopDiscountItemID(cur.ID, it.SKUID)
				}
				it.CreatedAt = now.UTC()
			}
			it.ProgramID = cur.ID
			it.ShopID = cur.ShopID
			it.ProgramStatus = cur.Status
			it.StartsAt = cur.StartsAt
			it.EndsAt = cur.EndsAt
			it.UpdatedAt = now.UTC()
			m.shopProgramItems[it.ID] = it
			nextItems = append(nextItems, it)
		}
		cur.Items = nextItems
	} else {
		for i := range cur.Items {
			cur.Items[i].ProgramStatus = cur.Status
			cur.Items[i].StartsAt = cur.StartsAt
			cur.Items[i].EndsAt = cur.EndsAt
			cur.Items[i].UpdatedAt = now.UTC()
			m.shopProgramItems[cur.Items[i].ID] = cur.Items[i]
		}
	}

	m.shopPrograms[cur.ID] = cloneShopProgram(cur)
	cp := cloneShopProgram(cur)
	return &cp, nil
}

func (m *MemoryStore) EndShopDiscountProgram(
	_ context.Context,
	programID string,
	now time.Time,
	check func(cur *domain.ShopDiscountProgram) error,
) (*domain.ShopDiscountProgram, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	existing, ok := m.shopPrograms[programID]
	if !ok {
		return nil, ErrNotFound
	}
	cur := cloneShopProgram(existing)
	if check != nil {
		if err := check(&cur); err != nil {
			return nil, err
		}
	}
	cur.Status = domain.ShopProgramStatusEnded
	if cur.EndedAt == nil {
		t := now.UTC()
		cur.EndedAt = &t
	}
	cur.UpdatedAt = now.UTC()
	for i := range cur.Items {
		cur.Items[i].ProgramStatus = domain.ShopProgramStatusEnded
		m.shopProgramItems[cur.Items[i].ID] = cur.Items[i]
	}
	m.shopPrograms[cur.ID] = cloneShopProgram(cur)
	cp := cloneShopProgram(cur)
	return &cp, nil
}

func (m *MemoryStore) ListShopDiscountPrograms(
	_ context.Context,
	shopID, statusFilter string,
	now time.Time,
	page, pageSize int32,
) ([]domain.ShopDiscountProgram, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	page, pageSize = normalizePage(page, pageSize)

	filtered := make([]domain.ShopDiscountProgram, 0, len(m.shopPrograms))
	for _, p := range m.shopPrograms {
		if shopID != "" && p.ShopID != shopID {
			continue
		}
		eff := p.EffectiveStatus(now)
		if statusFilter != "" && eff != statusFilter {
			continue
		}
		cp := cloneShopProgram(p)
		cp.Status = eff
		for i := range cp.Items {
			cp.Items[i].ProgramStatus = eff
		}
		filtered = append(filtered, cp)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if !filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
		}
		return filtered[i].ID < filtered[j].ID
	})

	total := int64(len(filtered))
	start := int((page - 1) * pageSize)
	if start >= len(filtered) {
		return nil, total, nil
	}
	end := start + int(pageSize)
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], total, nil
}

func (m *MemoryStore) GetShopDiscountProgramItem(_ context.Context, itemID string) (*domain.ShopDiscountProgramItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	it, ok := m.shopProgramItems[itemID]
	if !ok {
		return nil, ErrNotFound
	}
	if p, pOk := m.shopPrograms[it.ProgramID]; pOk {
		it.ProgramStatus = p.Status
		it.StartsAt = p.StartsAt
		it.EndsAt = p.EndsAt
	}
	cp := it
	return &cp, nil
}

func (m *MemoryStore) ListActiveShopDiscounts(_ context.Context, skuIDs []string, shopID string, now time.Time) ([]domain.ShopDiscountProgramItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	skuSet := make(map[string]bool, len(skuIDs))
	for _, id := range skuIDs {
		if id != "" {
			skuSet[id] = true
		}
	}

	out := make([]domain.ShopDiscountProgramItem, 0)
	for _, p := range m.shopPrograms {
		if p.EffectiveStatus(now) != domain.ShopProgramStatusActive {
			continue
		}
		if shopID != "" && p.ShopID != shopID {
			continue
		}
		for _, it := range p.Items {
			if len(skuSet) > 0 && !skuSet[it.SKUID] {
				continue
			}
			it.ProgramStatus = domain.ShopProgramStatusActive
			it.StartsAt = p.StartsAt
			it.EndsAt = p.EndsAt
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartsAt.Equal(out[j].StartsAt) {
			return out[i].StartsAt.Before(out[j].StartsAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > maxActiveShopDiscountRows {
		out = out[:maxActiveShopDiscountRows]
	}
	return out, nil
}

func (m *MemoryStore) checkShopProgramOverlapLocked(excludeID string, skuIDs []string, startsAt, endsAt, now time.Time) error {
	skuSet := make(map[string]bool, len(skuIDs))
	for _, id := range skuIDs {
		skuSet[id] = true
	}
	for _, other := range m.shopPrograms {
		if excludeID != "" && other.ID == excludeID {
			continue
		}
		if other.Status == domain.ShopProgramStatusEnded || other.EndedAt != nil || !other.EndsAt.After(now) {
			continue
		}
		if !other.StartsAt.Before(endsAt) || !other.EndsAt.After(startsAt) {
			continue
		}
		for _, it := range other.Items {
			if skuSet[it.SKUID] {
				return fmt.Errorf("%w: %s", ErrOverlappingShopProgram, it.SKUID)
			}
		}
	}
	return nil
}

// --- Coins & Reservations ---

func (m *MemoryStore) GetCoinBalance(_ context.Context, userID string, recentLimit int) (int64, []domain.CoinTransaction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if recentLimit <= 0 {
		recentLimit = 50
	}
	var sum int64
	txns := make([]domain.CoinTransaction, 0)
	for i := len(m.coinTxns) - 1; i >= 0; i-- {
		t := m.coinTxns[i]
		if t.UserID == userID {
			sum += t.Delta
			if len(txns) < recentLimit {
				txns = append(txns, t)
			}
		}
	}
	return sum, txns, nil
}

func (m *MemoryStore) CreditCoins(_ context.Context, userID string, amount int64, reason, referenceID string) (int64, bool, error) {
	if hasReservedStoreCoinRefPrefix(referenceID) {
		return 0, false, fmt.Errorf("%w: %s", ErrReservedCoinReferenceID, referenceID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	bal, already := m.creditCoinsLocked(userID, amount, reason, referenceID)
	return bal, already, nil
}

func (m *MemoryStore) creditCoinsLocked(userID string, amount int64, reason, referenceID string) (int64, bool) {
	if m.coinRefIndex[referenceID] {
		return m.coinBalances[userID], true
	}
	m.coinRefIndex[referenceID] = true
	m.coinBalances[userID] += amount
	m.coinTxns = append(m.coinTxns, domain.CoinTransaction{
		ID:          uuid.NewString(),
		UserID:      userID,
		Delta:       amount,
		Reason:      reason,
		ReferenceID: referenceID,
		CreatedAt:   time.Now().UTC(),
	})
	return m.coinBalances[userID], false
}

func (m *MemoryStore) ProcessOrderCompletedEvent(_ context.Context, eventID, _, orderID, buyerID string, coinsReward int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.processedEvents[eventID] {
		return true, nil
	}
	m.processedEvents[eventID] = true

	if coinsReward > 0 && buyerID != "" {
		refID := orderID
		if refID == "" {
			refID = eventID
		}
		m.creditCoinsLocked(buyerID, coinsReward, "order_completed_reward", refID)
	}
	return false, nil
}

func (m *MemoryStore) GetReservation(_ context.Context, orderID string) (*domain.PromotionReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, ok := m.reservations[orderID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := cloneReservation(res)
	return &cp, nil
}

func (m *MemoryStore) CommitReservation(_ context.Context, res domain.PromotionReservation, now time.Time) (*domain.PromotionReservation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	if existing, ok := m.reservations[res.OrderID]; ok {
		if existing.Status == domain.ReservationStatusReleased {
			return nil, false, ErrReservationAlreadyReleased
		}
		cp := cloneReservation(existing)
		return &cp, true, nil
	}

	// Validate ALL conditions atomically BEFORE mutating any state (matches Postgres rollback).
	if res.CoinsUsed > 0 {
		if m.coinBalances[res.UserID] < res.CoinsUsed {
			return nil, false, ErrInsufficientCoins
		}
		if m.coinRefIndex["reserve:"+res.OrderID] {
			return nil, false, fmt.Errorf("%w: duplicate coin reserve reference reserve:%s", ErrCoinLedgerConflict, res.OrderID)
		}
	}
	usageChecks := make([]usageCheck, 0, len(res.Vouchers)+len(res.FlashSaleItems)+len(res.ShopProgramItems))
	for _, rv := range res.Vouchers {
		v, ok := m.vouchers[rv.VoucherID]
		if !ok ||
			v.EffectiveStatus() != domain.VoucherStatusActive ||
			!v.IsActive ||
			(rv.RulesVersion > 0 && v.RulesVersion != rv.RulesVersion) ||
			v.UsedCount >= v.Quota ||
			(v.BudgetVND > 0 && v.UsedBudgetVND+rv.DiscountVND > v.BudgetVND) ||
			now.Before(v.StartsAt) ||
			now.After(v.EndsAt) {
			return nil, false, fmt.Errorf("%w: voucher %s", ErrVoucherNotEligibleForCommit, rv.VoucherID)
		}
		usageChecks = append(usageChecks, usageCheck{
			kind:     domain.UsageKindVoucher,
			targetID: rv.VoucherID,
			qty:      1,
			limit:    v.PerUserLimit,
		})
	}
	for _, rf := range res.FlashSaleItems {
		fi, ok := m.flashItems[rf.FlashSaleItemID]
		if !ok || fi.Status != domain.FlashStatusApproved || fi.RemainingSlots < rf.Quantity {
			return nil, false, fmt.Errorf("%w: flash item %s", ErrFlashNotEligibleForCommit, rf.FlashSaleItemID)
		}
		if fs, fsOk := m.flashSales[fi.FlashSaleID]; fsOk {
			if now.Before(fs.StartsAt) || !now.Before(fs.EndsAt) {
				return nil, false, fmt.Errorf("%w: flash item %s outside window", ErrFlashNotEligibleForCommit, rf.FlashSaleItemID)
			}
		}
		usageChecks = append(usageChecks, usageCheck{
			kind:     domain.UsageKindFlash,
			targetID: rf.FlashSaleItemID,
			qty:      rf.Quantity,
			limit:    fi.PerUserLimit,
		})
	}
	for _, rp := range res.ShopProgramItems {
		spi, ok := m.shopProgramItems[rp.ShopDiscountProgramItemID]
		if !ok {
			return nil, false, fmt.Errorf("%w: shop discount program item %s", ErrShopProgramNotEligibleForCommit, rp.ShopDiscountProgramItemID)
		}
		if rp.ShopID != "" && spi.ShopID != rp.ShopID {
			return nil, false, fmt.Errorf("%w: shop discount program item %s shop mismatch", ErrShopProgramNotEligibleForCommit, rp.ShopDiscountProgramItemID)
		}
		if rp.SKUID != "" && spi.SKUID != rp.SKUID {
			return nil, false, fmt.Errorf("%w: shop discount program item %s sku mismatch", ErrShopProgramNotEligibleForCommit, rp.ShopDiscountProgramItemID)
		}
		p, pOk := m.shopPrograms[spi.ProgramID]
		if !pOk || p.EffectiveStatus(now) != domain.ShopProgramStatusActive {
			return nil, false, fmt.Errorf("%w: shop discount program item %s inactive", ErrShopProgramNotEligibleForCommit, rp.ShopDiscountProgramItemID)
		}
		usageChecks = append(usageChecks, usageCheck{
			kind:     domain.UsageKindShopProgram,
			targetID: rp.ShopDiscountProgramItemID,
			qty:      rp.Quantity,
			limit:    spi.PerUserLimit,
		})
	}

	mergedUsage := mergeUsageChecks(usageChecks)
	if res.UserID != "" {
		for _, uc := range mergedUsage {
			curUsed := m.userUsage[memUsageKey(uc.kind, uc.targetID, res.UserID)]
			if uc.limit > 0 && curUsed+uc.qty > uc.limit {
				return nil, false, userLimitError(uc.kind, uc.targetID)
			}
		}
	}

	// All final gates passed — apply mutations atomically.
	if res.CoinsUsed > 0 {
		cur := m.coinBalances[res.UserID]
		reserveRef := "reserve:" + res.OrderID
		m.coinRefIndex[reserveRef] = true
		m.coinBalances[res.UserID] = cur - res.CoinsUsed
		m.coinTxns = append(m.coinTxns, domain.CoinTransaction{
			ID:          uuid.NewString(),
			UserID:      res.UserID,
			Delta:       -res.CoinsUsed,
			Reason:      "order_reserve",
			ReferenceID: reserveRef,
			CreatedAt:   now,
		})
	}

	res.Status = domain.ReservationStatusReserved
	res.CreatedAt = now
	res.UpdatedAt = now
	m.reservations[res.OrderID] = cloneReservation(res)

	for _, rv := range res.Vouchers {
		v := m.vouchers[rv.VoucherID]
		v.UsedCount++
		v.UsedBudgetVND += rv.DiscountVND
		v.UpdatedAt = now
		m.vouchers[rv.VoucherID] = v
	}
	for _, rf := range res.FlashSaleItems {
		fi := m.flashItems[rf.FlashSaleItemID]
		fi.RemainingSlots -= rf.Quantity
		fi.UpdatedAt = now
		m.flashItems[rf.FlashSaleItemID] = fi
	}
	if res.UserID != "" {
		for _, uc := range mergedUsage {
			k := memUsageKey(uc.kind, uc.targetID, res.UserID)
			m.userUsage[k] += uc.qty
		}
	}

	cp := cloneReservation(res)
	return &cp, false, nil
}

func (m *MemoryStore) ReleaseReservation(_ context.Context, orderID string) (*FullReleaseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.reservations[orderID]
	if !ok {
		// Write RELEASED tombstone so any late CommitReservation fails with ErrReservationAlreadyReleased.
		now := time.Now().UTC()
		m.reservations[orderID] = domain.PromotionReservation{
			OrderID:    orderID,
			Status:     domain.ReservationStatusReleased,
			ReleasedAt: &now,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		return &FullReleaseResult{AlreadyReleased: true}, nil
	}

	result := &FullReleaseResult{
		UserID:         existing.UserID,
		Vouchers:       append([]domain.ReservedVoucher(nil), existing.Vouchers...),
		FlashSaleItems: append([]domain.ReservedFlashItem(nil), existing.FlashSaleItems...),
		ShopPrograms:   append([]domain.ReservedShopProgramItem(nil), existing.ShopProgramItems...),
	}

	plan, err := planRemainingRelease(&existing, m.lookupFlashShopLocked, m.lookupShopProgShopLocked)
	if err != nil {
		return nil, err
	}
	if plan.alreadyReleased {
		result.AlreadyReleased = true
		return result, nil
	}

	if plan.coinsToReturn > 0 {
		releaseRef := "release:" + orderID
		if _, alreadyCredited := m.creditCoinsLocked(existing.UserID, plan.coinsToReturn, "order_release", releaseRef); !alreadyCredited {
			result.CoinsReturned = plan.coinsToReturn
		}
	}

	now := time.Now().UTC()
	m.restoreVouchersAndFlashLocked(plan.vouchersToRelease, plan.flashToRelease, now)
	m.restoreUserUsageLocked(existing.UserID, plan.vouchersToRelease, plan.flashToRelease, plan.shopProgramsToRelease)

	existing.Status = domain.ReservationStatusReleased
	existing.ReleasedShops = plan.newReleasedShops
	existing.ReleasedAt = &now
	existing.UpdatedAt = now
	m.reservations[orderID] = existing
	return result, nil
}

func (m *MemoryStore) ReleaseReservationShop(_ context.Context, orderID, shopID string) (*ShopReleaseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.reservations[orderID]
	if !ok {
		return &ShopReleaseResult{AlreadyReleased: true}, nil
	}

	plan, err := planShopRelease(&existing, shopID, m.lookupFlashShopLocked, m.lookupShopProgShopLocked)
	if err != nil {
		return nil, err
	}

	result := &ShopReleaseResult{
		AlreadyReleased:             plan.alreadyReleased,
		AllReleased:                 plan.allReleased,
		UserID:                      existing.UserID,
		CoinsReturned:               plan.coinsToReturn,
		ShopVoucherReleased:         plan.shopVoucherReleased,
		FlashQuantityReleased:       plan.flashQuantityReleased,
		ShopProgramQuantityReleased: plan.shopProgramQuantityReleased,
		PlatformVoucherReleased:     plan.platformVoucherReleased,
		TargetVouchers:              append([]domain.ReservedVoucher(nil), plan.targetVouchersForRedis...),
		TargetFlash:                 append([]domain.ReservedFlashItem(nil), plan.targetFlashForRedis...),
		TargetShopPrograms:          append([]domain.ReservedShopProgramItem(nil), plan.targetShopProgramsForRedis...),
		AllVouchers:                 append([]domain.ReservedVoucher(nil), existing.Vouchers...),
		AllFlash:                    append([]domain.ReservedFlashItem(nil), existing.FlashSaleItems...),
		AllShopPrograms:             append([]domain.ReservedShopProgramItem(nil), existing.ShopProgramItems...),
	}
	if plan.alreadyReleased {
		return result, nil
	}

	if plan.coinsToReturn > 0 {
		releaseRef := "release:" + orderID + ":shop:" + shopID
		if _, alreadyCredited := m.creditCoinsLocked(existing.UserID, plan.coinsToReturn, "order_release", releaseRef); alreadyCredited {
			result.CoinsReturned = 0
		}
	}

	now := time.Now().UTC()
	m.restoreVouchersAndFlashLocked(plan.vouchersToRelease, plan.flashToRelease, now)
	m.restoreUserUsageLocked(existing.UserID, plan.vouchersToRelease, plan.flashToRelease, plan.shopProgramsToRelease)

	existing.ReleasedShops = plan.newReleasedShops
	existing.UpdatedAt = now
	if plan.allReleased {
		existing.Status = domain.ReservationStatusReleased
		existing.ReleasedAt = &now
	}
	m.reservations[orderID] = existing
	return result, nil
}

func (m *MemoryStore) lookupFlashShopLocked(itemID string) (string, error) {
	if fi, ok := m.flashItems[itemID]; ok {
		return fi.ShopID, nil
	}
	return "", nil
}

func (m *MemoryStore) lookupShopProgShopLocked(itemID string) (string, error) {
	if it, ok := m.shopProgramItems[itemID]; ok {
		return it.ShopID, nil
	}
	return "", nil
}

func (m *MemoryStore) restoreVouchersAndFlashLocked(vouchers []domain.ReservedVoucher, flashItems []domain.ReservedFlashItem, now time.Time) {
	for _, rv := range vouchers {
		if v, exists := m.vouchers[rv.VoucherID]; exists {
			v.UsedCount--
			if v.UsedCount < 0 {
				v.UsedCount = 0
			}
			v.UsedBudgetVND -= rv.DiscountVND
			if v.UsedBudgetVND < 0 {
				v.UsedBudgetVND = 0
			}
			v.UpdatedAt = now
			m.vouchers[rv.VoucherID] = v
		}
	}
	for _, rf := range flashItems {
		if fi, exists := m.flashItems[rf.FlashSaleItemID]; exists {
			fi.RemainingSlots += rf.Quantity
			if fi.RemainingSlots > fi.TotalSlots {
				fi.RemainingSlots = fi.TotalSlots
			}
			fi.UpdatedAt = now
			m.flashItems[rf.FlashSaleItemID] = fi
		}
	}
}

func (m *MemoryStore) restoreUserUsageLocked(
	userID string,
	vouchers []domain.ReservedVoucher,
	flashItems []domain.ReservedFlashItem,
	shopProgs []domain.ReservedShopProgramItem,
) {
	if userID == "" {
		return
	}
	checks := make([]usageCheck, 0, len(vouchers)+len(flashItems)+len(shopProgs))
	for _, rv := range vouchers {
		checks = append(checks, usageCheck{kind: domain.UsageKindVoucher, targetID: rv.VoucherID, qty: 1})
	}
	for _, rf := range flashItems {
		checks = append(checks, usageCheck{kind: domain.UsageKindFlash, targetID: rf.FlashSaleItemID, qty: rf.Quantity})
	}
	for _, rp := range shopProgs {
		checks = append(checks, usageCheck{kind: domain.UsageKindShopProgram, targetID: rp.ShopDiscountProgramItemID, qty: rp.Quantity})
	}
	for _, uc := range mergeUsageChecks(checks) {
		k := memUsageKey(uc.kind, uc.targetID, userID)
		next := m.userUsage[k] - uc.qty
		if next < 0 {
			next = 0
		}
		m.userUsage[k] = next
	}
}

func memUsageKey(kind, targetID, userID string) string {
	return kind + "|" + targetID + "|" + userID
}

// AccountBalance returns the stored coin_accounts balance for invariant verification in tests.
func (m *MemoryStore) AccountBalance(userID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.coinBalances[userID]
}

// LedgerSum returns the sum of all coin_transactions.delta for userID.
func (m *MemoryStore) LedgerSum(userID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, t := range m.coinTxns {
		if t.UserID == userID {
			sum += t.Delta
		}
	}
	return sum
}

func cloneShopProgram(p domain.ShopDiscountProgram) domain.ShopDiscountProgram {
	cp := p
	cp.Items = append([]domain.ShopDiscountProgramItem(nil), p.Items...)
	if p.EndedAt != nil {
		t := *p.EndedAt
		cp.EndedAt = &t
	}
	return cp
}

func cloneReservation(r domain.PromotionReservation) domain.PromotionReservation {
	cp := r
	cp.Vouchers = append([]domain.ReservedVoucher(nil), r.Vouchers...)
	cp.FlashSaleItems = append([]domain.ReservedFlashItem(nil), r.FlashSaleItems...)
	cp.ShopProgramItems = append([]domain.ReservedShopProgramItem(nil), r.ShopProgramItems...)
	cp.BreakdownJSON = append([]byte(nil), r.BreakdownJSON...)
	cp.ReleasedShops = append([]string(nil), r.ReleasedShops...)
	if r.ReleasedAt != nil {
		t := *r.ReleasedAt
		cp.ReleasedAt = &t
	}
	return cp
}
