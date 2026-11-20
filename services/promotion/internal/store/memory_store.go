package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

// MemoryStore is a concurrency-safe in-memory implementation of Store for unit and race testing.
type MemoryStore struct {
	mu              sync.Mutex
	vouchers        map[string]domain.Voucher
	voucherByCode   map[string]string
	userVouchers    map[string]map[string]bool
	flashSales      map[string]domain.FlashSale
	flashItems      map[string]domain.FlashSaleItem
	coinBalances    map[string]int64
	coinTxns        []domain.CoinTransaction
	coinRefIndex    map[string]bool
	reservations    map[string]domain.PromotionReservation
	processedEvents map[string]bool
}

// NewMemoryStore initializes an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		vouchers:        make(map[string]domain.Voucher),
		voucherByCode:   make(map[string]string),
		userVouchers:    make(map[string]map[string]bool),
		flashSales:      make(map[string]domain.FlashSale),
		flashItems:      make(map[string]domain.FlashSaleItem),
		coinBalances:    make(map[string]int64),
		coinRefIndex:    make(map[string]bool),
		reservations:    make(map[string]domain.PromotionReservation),
		processedEvents: make(map[string]bool),
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

	m.vouchers[v.ID] = v
	m.voucherByCode[v.Code] = v.ID
	cp := v
	return &cp, nil
}

func (m *MemoryStore) GetVoucherByIDOrCode(_ context.Context, idOrCode string) (*domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.vouchers[idOrCode]; ok {
		cp := v
		return &cp, nil
	}
	if id, ok := m.voucherByCode[idOrCode]; ok {
		cp := m.vouchers[id]
		return &cp, nil
	}
	return nil, ErrNotFound
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
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *MemoryStore) ListAllActiveVouchers(_ context.Context) ([]domain.Voucher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []domain.Voucher
	for _, v := range m.vouchers {
		if v.IsActive {
			out = append(out, v)
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

func (m *MemoryStore) CreateFlashSale(_ context.Context, fs domain.FlashSale) (*domain.FlashSale, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if fs.ID == "" {
		fs.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	fs.CreatedAt = now
	fs.UpdatedAt = now
	m.flashSales[fs.ID] = fs
	cp := fs
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

func (m *MemoryStore) ApproveFlashSaleItem(_ context.Context, itemID, adminID string, approve bool, reason string) (*domain.FlashSaleItem, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, ok := m.flashItems[itemID]
	if !ok {
		return nil, "", ErrNotFound
	}
	prevStatus := item.Status
	if approve {
		item.Status = domain.FlashStatusApproved
		item.RejectionReason = ""
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
		cp := fs
		cp.Items = nil
		for _, item := range m.flashItems {
			if item.FlashSaleID == fs.ID && item.Status == domain.FlashStatusApproved {
				item.StartsAt = fs.StartsAt
				item.EndsAt = fs.EndsAt
				cp.Items = append(cp.Items, item)
			}
		}
		sort.Slice(cp.Items, func(i, j int) bool { return cp.Items[i].ID < cp.Items[j].ID })
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

func (m *MemoryStore) CommitReservation(_ context.Context, res domain.PromotionReservation) (*domain.PromotionReservation, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.reservations[res.OrderID]; ok {
		if existing.Status == domain.ReservationStatusReleased {
			return nil, false, ErrReservationAlreadyReleased
		}
		cp := cloneReservation(existing)
		return &cp, true, nil
	}

	if res.CoinsUsed > 0 {
		cur := m.coinBalances[res.UserID]
		if cur < res.CoinsUsed {
			return nil, false, ErrInsufficientCoins
		}
		reserveRef := "reserve:" + res.OrderID
		if !m.coinRefIndex[reserveRef] {
			m.coinRefIndex[reserveRef] = true
			m.coinBalances[res.UserID] = cur - res.CoinsUsed
			m.coinTxns = append(m.coinTxns, domain.CoinTransaction{
				ID:          uuid.NewString(),
				UserID:      res.UserID,
				Delta:       -res.CoinsUsed,
				Reason:      "order_reserve",
				ReferenceID: reserveRef,
				CreatedAt:   time.Now().UTC(),
			})
		}
	}

	now := time.Now().UTC()
	res.Status = domain.ReservationStatusReserved
	res.CreatedAt = now
	res.UpdatedAt = now
	m.reservations[res.OrderID] = cloneReservation(res)

	for _, rv := range res.Vouchers {
		if v, ok := m.vouchers[rv.VoucherID]; ok {
			v.UsedCount++
			v.UsedBudgetVND += rv.DiscountVND
			v.UpdatedAt = now
			m.vouchers[rv.VoucherID] = v
		}
	}
	for _, rf := range res.FlashSaleItems {
		if fi, ok := m.flashItems[rf.FlashSaleItemID]; ok {
			fi.RemainingSlots -= rf.Quantity
			if fi.RemainingSlots < 0 {
				fi.RemainingSlots = 0
			}
			fi.UpdatedAt = now
			m.flashItems[rf.FlashSaleItemID] = fi
		}
	}

	cp := cloneReservation(res)
	return &cp, false, nil
}

func (m *MemoryStore) ReleaseReservation(_ context.Context, orderID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.reservations[orderID]
	if !ok || existing.Status == domain.ReservationStatusReleased {
		return true, nil
	}

	if existing.CoinsUsed > 0 {
		releaseRef := "release:" + orderID
		m.creditCoinsLocked(existing.UserID, existing.CoinsUsed, "order_release", releaseRef)
	}

	now := time.Now().UTC()
	for _, rv := range existing.Vouchers {
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
	for _, rf := range existing.FlashSaleItems {
		if fi, exists := m.flashItems[rf.FlashSaleItemID]; exists {
			fi.RemainingSlots += rf.Quantity
			if fi.RemainingSlots > fi.TotalSlots {
				fi.RemainingSlots = fi.TotalSlots
			}
			fi.UpdatedAt = now
			m.flashItems[rf.FlashSaleItemID] = fi
		}
	}

	existing.Status = domain.ReservationStatusReleased
	existing.ReleasedAt = &now
	existing.UpdatedAt = now
	m.reservations[orderID] = existing
	return false, nil
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

func cloneReservation(r domain.PromotionReservation) domain.PromotionReservation {
	cp := r
	cp.Vouchers = append([]domain.ReservedVoucher(nil), r.Vouchers...)
	cp.FlashSaleItems = append([]domain.ReservedFlashItem(nil), r.FlashSaleItems...)
	cp.BreakdownJSON = append([]byte(nil), r.BreakdownJSON...)
	if r.ReleasedAt != nil {
		t := *r.ReleasedAt
		cp.ReleasedAt = &t
	}
	return cp
}
