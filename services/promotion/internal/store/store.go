package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var (
	ErrNotFound                        = errors.New("not found")
	ErrDuplicateCode                   = errors.New("voucher code already exists")
	ErrOverlappingFlashSale            = errors.New("sku already registered in overlapping flash sale window")
	ErrOverlappingShopProgram          = errors.New("sku already belongs to an overlapping shop discount program")
	ErrFlashSaleEnded                  = errors.New("flash sale has already ended")
	ErrInvalidFlashTransition          = errors.New("invalid flash sale item status transition")
	ErrInsufficientCoins               = errors.New("insufficient coin balance")
	ErrReservationAlreadyReleased      = errors.New("promotion reservation already released")
	ErrVoucherNotEligibleForCommit     = errors.New("voucher inactive, expired, out of quota, or budget exhausted")
	ErrFlashNotEligibleForCommit       = errors.New("flash sale item not approved, outside active window, or sold out")
	ErrShopProgramNotEligibleForCommit = errors.New("shop discount program ended, outside active window, or item mismatch")
)

const voucherSelectColumns = `id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
	max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
	budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, status, category_ids, deleted_at,
	created_by, created_at, updated_at`

const flashItemSelectColumns = `fsi.id, fsi.flash_sale_id, fsi.shop_id, fsi.sku_id, fsi.original_price_vnd, fsi.flash_price_vnd,
	fsi.total_slots, fsi.remaining_slots, fsi.per_user_limit, fsi.status, fsi.rejection_reason,
	fsi.approved_by, fs.starts_at, fs.ends_at, fsi.created_at, fsi.updated_at`

// Store defines the persistence contract for the promotion service.
type Store interface {
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error

	// Vouchers
	CreateVoucher(ctx context.Context, v domain.Voucher) (*domain.Voucher, error)
	GetVoucherByID(ctx context.Context, id string) (*domain.Voucher, error)
	GetVoucherByIDOrCode(ctx context.Context, idOrCode string) (*domain.Voucher, error)
	UpdateVoucher(ctx context.Context, id string, mutate func(cur *domain.Voucher) error) (before *domain.Voucher, after *domain.Voucher, err error)
	SetVoucherStatus(ctx context.Context, id, newStatus string, check func(cur *domain.Voucher) error) (*domain.Voucher, error)
	DeleteVoucher(ctx context.Context, id string, check func(cur *domain.Voucher) error) (alreadyDeleted bool, err error)
	ListVouchers(ctx context.Context, shopID, scope string) ([]domain.Voucher, error)
	ListAllVouchers(ctx context.Context) ([]domain.Voucher, error)
	SaveUserVoucher(ctx context.Context, userID, voucherID string) (alreadySaved bool, err error)
	GetUserSavedVoucherIDs(ctx context.Context, userID string) (map[string]bool, error)
	ProcessExpiringSavedVouchers(ctx context.Context, now time.Time, horizon time.Duration, batchSize int, notifyFn func(ctx context.Context, item domain.ExpiringSavedVoucher) error) (sent int, scanned int, err error)

	// Flash sales
	CreateFlashSale(ctx context.Context, fs domain.FlashSale) (*domain.FlashSale, error)
	GetFlashSale(ctx context.Context, id string) (*domain.FlashSale, error)
	RegisterFlashSaleItem(ctx context.Context, item domain.FlashSaleItem, now time.Time) (*domain.FlashSaleItem, error)
	ApproveFlashSaleItem(ctx context.Context, itemID, adminID string, approve bool, reason string, originalPriceVND int64, now time.Time) (item *domain.FlashSaleItem, prevStatus string, err error)
	GetFlashSaleItem(ctx context.Context, itemID string) (*domain.FlashSaleItem, error)
	ListFlashSales(ctx context.Context, now time.Time, includeUpcoming bool) ([]domain.FlashSale, error)
	ListAllFlashSales(ctx context.Context, now time.Time, statusFilter string, page, pageSize int32) ([]domain.FlashSale, int64, error)
	ListFlashSaleItems(ctx context.Context, statusFilter, flashSaleID, shopID string, page, pageSize int32) ([]domain.FlashSaleItem, int64, error)
	ListApprovedFlashSaleItems(ctx context.Context) ([]domain.FlashSaleItem, error)
	HasOverlappingApprovedFlashSale(ctx context.Context, skuID string, startsAt, endsAt, now time.Time) (bool, error)

	// Shop discount programs (UC-S18)
	CreateShopDiscountProgram(ctx context.Context, prog domain.ShopDiscountProgram, now time.Time) (*domain.ShopDiscountProgram, error)
	GetShopDiscountProgram(ctx context.Context, programID string) (*domain.ShopDiscountProgram, error)
	UpdateShopDiscountProgram(ctx context.Context, programID string, now time.Time, mutate func(cur *domain.ShopDiscountProgram) (replaceItems bool, err error)) (*domain.ShopDiscountProgram, error)
	EndShopDiscountProgram(ctx context.Context, programID string, now time.Time, check func(cur *domain.ShopDiscountProgram) error) (*domain.ShopDiscountProgram, error)
	ListShopDiscountPrograms(ctx context.Context, shopID, statusFilter string, now time.Time, page, pageSize int32) ([]domain.ShopDiscountProgram, int64, error)
	GetShopDiscountProgramItem(ctx context.Context, itemID string) (*domain.ShopDiscountProgramItem, error)
	ListActiveShopDiscounts(ctx context.Context, skuIDs []string, shopID string, now time.Time) ([]domain.ShopDiscountProgramItem, error)

	// Coins & events
	GetCoinBalance(ctx context.Context, userID string, recentLimit int) (int64, []domain.CoinTransaction, error)
	CreditCoins(ctx context.Context, userID string, amount int64, reason, referenceID string) (balance int64, alreadyCredited bool, err error)
	ProcessOrderCompletedEvent(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (alreadyProcessed bool, err error)

	// Reservations
	GetReservation(ctx context.Context, orderID string) (*domain.PromotionReservation, error)
	CommitReservation(ctx context.Context, res domain.PromotionReservation, now time.Time) (existing *domain.PromotionReservation, alreadyReserved bool, err error)
	ReleaseReservation(ctx context.Context, orderID string) (alreadyReleased bool, err error)
	ReleaseReservationShop(ctx context.Context, orderID, shopID string) (*ShopReleaseResult, error)
}

// PostgresStore implements Store backed by PostgreSQL via pgxpool.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a PostgresStore using the given connection pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

// Migrate applies embedded SQL migrations in lexical order.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		sqlBytes, readErr := migrationsFS.ReadFile("migrations/" + name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		if _, execErr := s.pool.Exec(ctx, string(sqlBytes)); execErr != nil {
			return fmt.Errorf("exec migration %s: %w", name, execErr)
		}
	}
	return nil
}

// Ping verifies PostgreSQL connectivity.
func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PostgresStore) CreateVoucher(ctx context.Context, v domain.Voucher) (*domain.Voucher, error) {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	v.CreatedAt = now
	v.UpdatedAt = now
	if v.Status == "" {
		if v.IsActive {
			v.Status = domain.VoucherStatusActive
		} else {
			v.Status = domain.VoucherStatusPaused
		}
	}
	v.IsActive = v.Status == domain.VoucherStatusActive

	catJSON, err := marshalStringSliceJSON(v.CategoryIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal voucher category_ids: %w", err)
	}

	const q = `
		INSERT INTO vouchers (
			id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
			max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
			budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, status, category_ids,
			created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, 0, $12,
			$13, 0, $14, $15, $16, $17, $18::jsonb,
			$19, $20, $21
		)
		RETURNING ` + voucherSelectColumns

	out, err := scanVoucher(s.pool.QueryRow(ctx, q,
		v.ID, v.Code, v.Name, v.Scope, v.ShopID, v.DiscountType, v.ValueVND, v.DiscountPercent,
		v.MaxDiscountVND, v.MinOrderVND, v.Quota, v.PerUserLimit,
		v.BudgetVND, v.StartsAt.UTC(), v.EndsAt.UTC(), v.IsActive, v.Status, string(catJSON),
		v.CreatedBy, v.CreatedAt, v.UpdatedAt,
	))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrDuplicateCode
		}
		return nil, fmt.Errorf("insert voucher: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) GetVoucherByID(ctx context.Context, id string) (*domain.Voucher, error) {
	const q = `SELECT ` + voucherSelectColumns + ` FROM vouchers WHERE id = $1 LIMIT 1`
	v, err := scanVoucher(s.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get voucher by id: %w", err)
	}
	return v, nil
}

func (s *PostgresStore) GetVoucherByIDOrCode(ctx context.Context, idOrCode string) (*domain.Voucher, error) {
	const q = `SELECT ` + voucherSelectColumns + ` FROM vouchers WHERE id = $1 OR code = $1 LIMIT 1`
	v, err := scanVoucher(s.pool.QueryRow(ctx, q, idOrCode))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get voucher: %w", err)
	}
	return v, nil
}

func (s *PostgresStore) UpdateVoucher(
	ctx context.Context,
	id string,
	mutate func(cur *domain.Voucher) error,
) (*domain.Voucher, *domain.Voucher, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("begin update voucher tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cur, err := scanVoucher(tx.QueryRow(ctx, `SELECT `+voucherSelectColumns+` FROM vouchers WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("lock voucher for update: %w", err)
	}

	before := cloneVoucher(*cur)
	if err := mutate(cur); err != nil {
		return nil, nil, err
	}

	catJSON, err := marshalStringSliceJSON(cur.CategoryIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal voucher category_ids: %w", err)
	}

	const updateQ = `
		UPDATE vouchers
		SET name = $2,
			discount_type = $3,
			value_vnd = $4,
			discount_percent = $5,
			max_discount_vnd = $6,
			min_order_vnd = $7,
			quota = $8,
			per_user_limit = $9,
			budget_vnd = $10,
			starts_at = $11,
			ends_at = $12,
			category_ids = $13::jsonb,
			updated_at = NOW()
		WHERE id = $1
		RETURNING ` + voucherSelectColumns

	after, err := scanVoucher(tx.QueryRow(ctx, updateQ,
		cur.ID, cur.Name, cur.DiscountType, cur.ValueVND, cur.DiscountPercent,
		cur.MaxDiscountVND, cur.MinOrderVND, cur.Quota, cur.PerUserLimit,
		cur.BudgetVND, cur.StartsAt.UTC(), cur.EndsAt.UTC(), string(catJSON),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("update voucher: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit update voucher: %w", err)
	}
	return &before, after, nil
}

func (s *PostgresStore) SetVoucherStatus(
	ctx context.Context,
	id, newStatus string,
	check func(cur *domain.Voucher) error,
) (*domain.Voucher, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin set voucher status tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cur, err := scanVoucher(tx.QueryRow(ctx, `SELECT `+voucherSelectColumns+` FROM vouchers WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock voucher for status update: %w", err)
	}
	if check != nil {
		if err := check(cur); err != nil {
			return nil, err
		}
	}

	isActive := newStatus == domain.VoucherStatusActive
	const updateQ = `
		UPDATE vouchers
		SET status = $2,
			is_active = $3,
			updated_at = NOW()
		WHERE id = $1
		RETURNING ` + voucherSelectColumns

	after, err := scanVoucher(tx.QueryRow(ctx, updateQ, id, newStatus, isActive))
	if err != nil {
		return nil, fmt.Errorf("update voucher status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit voucher status: %w", err)
	}
	return after, nil
}

func (s *PostgresStore) DeleteVoucher(
	ctx context.Context,
	id string,
	check func(cur *domain.Voucher) error,
) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin delete voucher tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cur, err := scanVoucher(tx.QueryRow(ctx, `SELECT `+voucherSelectColumns+` FROM vouchers WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("lock voucher for delete: %w", err)
	}
	if check != nil {
		if err := check(cur); err != nil {
			return false, err
		}
	}
	if cur.EffectiveStatus() == domain.VoucherStatusDeleted {
		return true, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE vouchers
		SET status = 'DELETED',
			is_active = FALSE,
			deleted_at = COALESCE(deleted_at, NOW()),
			updated_at = NOW()
		WHERE id = $1
	`, id); err != nil {
		return false, fmt.Errorf("soft-delete voucher: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit soft-delete voucher: %w", err)
	}
	return false, nil
}

func (s *PostgresStore) ListVouchers(ctx context.Context, shopID, scope string) ([]domain.Voucher, error) {
	const q = `
		SELECT ` + voucherSelectColumns + `
		FROM vouchers
		WHERE ($1 = '' OR shop_id = $1)
		  AND ($2 = '' OR scope = $2)
		ORDER BY created_at DESC, id ASC
	`
	rows, err := s.pool.Query(ctx, q, shopID, scope)
	if err != nil {
		return nil, fmt.Errorf("list vouchers: %w", err)
	}
	defer rows.Close()

	var result []domain.Voucher
	for rows.Next() {
		v, scanErr := scanVoucher(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan voucher: %w", scanErr)
		}
		result = append(result, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vouchers: %w", err)
	}
	return result, nil
}

func (s *PostgresStore) ListAllVouchers(ctx context.Context) ([]domain.Voucher, error) {
	const q = `SELECT ` + voucherSelectColumns + ` FROM vouchers`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list all vouchers: %w", err)
	}
	defer rows.Close()

	var result []domain.Voucher
	for rows.Next() {
		v, scanErr := scanVoucher(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan voucher: %w", scanErr)
		}
		result = append(result, *v)
	}
	return result, rows.Err()
}

func (s *PostgresStore) SaveUserVoucher(ctx context.Context, userID, voucherID string) (bool, error) {
	const q = `
		INSERT INTO user_vouchers (user_id, voucher_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, voucher_id) DO NOTHING
	`
	tag, err := s.pool.Exec(ctx, q, userID, voucherID)
	if err != nil {
		return false, fmt.Errorf("save user voucher: %w", err)
	}
	return tag.RowsAffected() == 0, nil
}

func (s *PostgresStore) GetUserSavedVoucherIDs(ctx context.Context, userID string) (map[string]bool, error) {
	saved := make(map[string]bool)
	if userID == "" {
		return saved, nil
	}
	const q = `SELECT voucher_id FROM user_vouchers WHERE user_id = $1`
	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list saved vouchers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan saved voucher id: %w", err)
		}
		saved[id] = true
	}
	return saved, rows.Err()
}

func (s *PostgresStore) ProcessExpiringSavedVouchers(
	ctx context.Context,
	now time.Time,
	horizon time.Duration,
	batchSize int,
	notifyFn func(ctx context.Context, item domain.ExpiringSavedVoucher) error,
) (int, int, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	cutoff := now.UTC().Add(horizon)

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("begin expiring vouchers tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
		SELECT uv.user_id, uv.voucher_id, v.code, v.ends_at
		FROM user_vouchers uv
		JOIN vouchers v ON v.id = uv.voucher_id
		WHERE uv.expiry_notified_at IS NULL
		  AND v.status = 'ACTIVE'
		  AND v.is_active = TRUE
		  AND v.used_count < v.quota
		  AND (v.budget_vnd = 0 OR v.used_budget_vnd < v.budget_vnd)
		  AND v.ends_at > $1
		  AND v.ends_at <= $2
		ORDER BY v.ends_at ASC, uv.user_id ASC, uv.voucher_id ASC
		FOR UPDATE OF uv SKIP LOCKED
		LIMIT $3
	`
	rows, err := tx.Query(ctx, q, now.UTC(), cutoff, batchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("query expiring saved vouchers: %w", err)
	}
	items := make([]domain.ExpiringSavedVoucher, 0)
	for rows.Next() {
		var it domain.ExpiringSavedVoucher
		if scanErr := rows.Scan(&it.UserID, &it.VoucherID, &it.Code, &it.EndsAt); scanErr != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan expiring saved voucher: %w", scanErr)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, fmt.Errorf("iterate expiring saved vouchers: %w", err)
	}
	rows.Close()

	if len(items) == 0 {
		return 0, 0, nil
	}

	sent := 0
	var firstErr error
	for _, it := range items {
		if notifyFn != nil {
			if pubErr := notifyFn(ctx, it); pubErr != nil {
				if firstErr == nil {
					firstErr = pubErr
				}
				continue
			}
		}
		if _, updErr := tx.Exec(ctx, `
			UPDATE user_vouchers
			SET expiry_notified_at = $3
			WHERE user_id = $1 AND voucher_id = $2
		`, it.UserID, it.VoucherID, now.UTC()); updErr != nil {
			return sent, len(items), fmt.Errorf("mark voucher expiry notified: %w", updErr)
		}
		sent++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, len(items), fmt.Errorf("commit expiring vouchers tx: %w", err)
	}
	return sent, len(items), firstErr
}

func (s *PostgresStore) CreateFlashSale(ctx context.Context, fs domain.FlashSale) (*domain.FlashSale, error) {
	if fs.ID == "" {
		fs.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	fs.CreatedAt = now
	fs.UpdatedAt = now

	catJSON, err := marshalStringSliceJSON(fs.CategoryIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal flash sale category_ids: %w", err)
	}

	const q = `
		INSERT INTO flash_sales (id, name, starts_at, ends_at, min_discount_percent, category_ids, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
		RETURNING id, name, starts_at, ends_at, min_discount_percent, category_ids, created_by, created_at, updated_at
	`
	out, err := scanFlashSale(s.pool.QueryRow(ctx, q,
		fs.ID, fs.Name, fs.StartsAt.UTC(), fs.EndsAt.UTC(), fs.MinDiscountPercent, string(catJSON),
		fs.CreatedBy, fs.CreatedAt, fs.UpdatedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("insert flash sale: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) GetFlashSale(ctx context.Context, id string) (*domain.FlashSale, error) {
	const q = `
		SELECT id, name, starts_at, ends_at, min_discount_percent, category_ids, created_by, created_at, updated_at
		FROM flash_sales
		WHERE id = $1
	`
	out, err := scanFlashSale(s.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get flash sale: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) RegisterFlashSaleItem(ctx context.Context, item domain.FlashSaleItem, now time.Time) (*domain.FlashSaleItem, error) {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin register flash item tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize per-SKU registration using an advisory transaction lock to prevent overlapping window races.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "flash_sku:"+item.SKUID); err != nil {
		return nil, fmt.Errorf("acquire sku lock: %w", err)
	}

	var startsAt, endsAt time.Time
	err = tx.QueryRow(ctx, `SELECT starts_at, ends_at FROM flash_sales WHERE id = $1`, item.FlashSaleID).Scan(&startsAt, &endsAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query flash sale window: %w", err)
	}
	if !endsAt.After(now) {
		return nil, ErrFlashSaleEnded
	}

	var overlapCount int
	const overlapQ = `
		SELECT COUNT(1)
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.sku_id = $1
		  AND fsi.status IN ('PENDING', 'APPROVED')
		  AND fs.starts_at < $2
		  AND fs.ends_at > $3
	`
	if err := tx.QueryRow(ctx, overlapQ, item.SKUID, endsAt, startsAt).Scan(&overlapCount); err != nil {
		return nil, fmt.Errorf("check overlapping flash sale: %w", err)
	}
	if overlapCount > 0 {
		return nil, ErrOverlappingFlashSale
	}

	const insertQ = `
		INSERT INTO flash_sale_items (
			id, flash_sale_id, shop_id, sku_id, original_price_vnd, flash_price_vnd,
			total_slots, remaining_slots, per_user_limit, status, rejection_reason,
			approved_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $7, $8, 'PENDING', '',
			'', $9, $9
		)
		RETURNING id, flash_sale_id, shop_id, sku_id, original_price_vnd, flash_price_vnd,
			total_slots, remaining_slots, per_user_limit, status, rejection_reason,
			approved_by, created_at, updated_at
	`
	var out domain.FlashSaleItem
	err = tx.QueryRow(ctx, insertQ,
		item.ID, item.FlashSaleID, item.ShopID, item.SKUID, item.OriginalPriceVND, item.FlashPriceVND,
		item.TotalSlots, item.PerUserLimit, now.UTC(),
	).Scan(
		&out.ID, &out.FlashSaleID, &out.ShopID, &out.SKUID, &out.OriginalPriceVND, &out.FlashPriceVND,
		&out.TotalSlots, &out.RemainingSlots, &out.PerUserLimit, &out.Status, &out.RejectionReason,
		&out.ApprovedBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert flash sale item: %w", err)
	}
	out.StartsAt = startsAt
	out.EndsAt = endsAt

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit register flash item: %w", err)
	}
	return &out, nil
}

func (s *PostgresStore) ApproveFlashSaleItem(
	ctx context.Context,
	itemID, adminID string,
	approve bool,
	reason string,
	originalPriceVND int64,
	now time.Time,
) (*domain.FlashSaleItem, string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("begin approve flash item tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Read immutable sku_id first so advisory lock order matches RegisterFlashSaleItem (advisory -> row lock).
	var skuID string
	err = tx.QueryRow(ctx, `SELECT sku_id FROM flash_sale_items WHERE id = $1`, itemID).Scan(&skuID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("lookup flash item sku_id: %w", err)
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "flash_sku:"+skuID); err != nil {
		return nil, "", fmt.Errorf("acquire sku lock: %w", err)
	}

	cur, err := scanFlashSaleItem(tx.QueryRow(ctx, `
		SELECT `+flashItemSelectColumns+`
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.id = $1
		FOR UPDATE OF fsi
	`, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("lock flash sale item: %w", err)
	}

	prevStatus := cur.Status
	newStatus := domain.FlashStatusRejected
	rejReason := reason
	if approve {
		newStatus = domain.FlashStatusApproved
		rejReason = ""
	}

	// Idempotent replay on matching status: do NOT re-validate stock/overlap or mutate row/Redis.
	if prevStatus == newStatus {
		return cur, prevStatus, nil
	}
	// Strict state machine: only PENDING -> APPROVED|REJECTED transitions are permitted.
	if prevStatus != domain.FlashStatusPending {
		return nil, prevStatus, fmt.Errorf("%w: cannot transition flash sale item from %s to %s", ErrInvalidFlashTransition, prevStatus, newStatus)
	}

	if approve {
		if !cur.EndsAt.After(now) {
			return nil, "", ErrFlashSaleEnded
		}
		var overlapCount int
		const overlapQ = `
			SELECT COUNT(1)
			FROM flash_sale_items fsi
			JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
			WHERE fsi.id <> $1
			  AND fsi.sku_id = $2
			  AND fsi.status IN ('PENDING', 'APPROVED')
			  AND fs.starts_at < $3
			  AND fs.ends_at > $4
		`
		if err := tx.QueryRow(ctx, overlapQ, itemID, skuID, cur.EndsAt, cur.StartsAt).Scan(&overlapCount); err != nil {
			return nil, "", fmt.Errorf("check overlapping flash sale on approve: %w", err)
		}
		if overlapCount > 0 {
			return nil, "", ErrOverlappingFlashSale
		}
	}

	const updateQ = `
		UPDATE flash_sale_items fsi
		SET status = $2,
			rejection_reason = $3,
			approved_by = $4,
			original_price_vnd = CASE WHEN $5 > 0 THEN $5 ELSE fsi.original_price_vnd END,
			updated_at = NOW()
		FROM flash_sales fs
		WHERE fsi.id = $1 AND fs.id = fsi.flash_sale_id
		RETURNING ` + flashItemSelectColumns

	out, err := scanFlashSaleItem(tx.QueryRow(ctx, updateQ, itemID, newStatus, rejReason, adminID, originalPriceVND))
	if err != nil {
		return nil, "", fmt.Errorf("update flash sale item approval: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit approve flash item: %w", err)
	}
	return out, prevStatus, nil
}

func (s *PostgresStore) GetFlashSaleItem(ctx context.Context, itemID string) (*domain.FlashSaleItem, error) {
	const q = `
		SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.id = $1
	`
	out, err := scanFlashSaleItem(s.pool.QueryRow(ctx, q, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get flash sale item: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) ListFlashSales(ctx context.Context, now time.Time, includeUpcoming bool) ([]domain.FlashSale, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var salesQ string
	if includeUpcoming {
		salesQ = `
			SELECT id, name, starts_at, ends_at, min_discount_percent, category_ids, created_by, created_at, updated_at
			FROM flash_sales
			WHERE ends_at > $1
			ORDER BY starts_at ASC, id ASC
		`
	} else {
		salesQ = `
			SELECT id, name, starts_at, ends_at, min_discount_percent, category_ids, created_by, created_at, updated_at
			FROM flash_sales
			WHERE starts_at <= $1 AND ends_at > $1
			ORDER BY starts_at ASC, id ASC
		`
	}

	rows, err := s.pool.Query(ctx, salesQ, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("list flash sales: %w", err)
	}
	defer rows.Close()

	sales := make([]domain.FlashSale, 0)
	saleIdx := make(map[string]int)
	saleIDs := make([]string, 0)
	for rows.Next() {
		fs, scanErr := scanFlashSale(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan flash sale: %w", scanErr)
		}
		saleIdx[fs.ID] = len(sales)
		saleIDs = append(saleIDs, fs.ID)
		sales = append(sales, *fs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flash sales: %w", err)
	}
	if len(saleIDs) == 0 {
		return sales, nil
	}

	if err := s.attachApprovedFlashItems(ctx, sales, saleIdx, saleIDs); err != nil {
		return nil, err
	}
	return sales, nil
}

func (s *PostgresStore) ListAllFlashSales(ctx context.Context, now time.Time, statusFilter string, page, pageSize int32) ([]domain.FlashSale, int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	page, pageSize = normalizePage(page, pageSize)
	offset := (page - 1) * pageSize

	whereClause := "TRUE"
	args := []any{}
	switch statusFilter {
	case "UPCOMING":
		whereClause = "starts_at > $1"
		args = append(args, now.UTC())
	case "ACTIVE":
		whereClause = "starts_at <= $1 AND ends_at > $1"
		args = append(args, now.UTC())
	case "ENDED":
		whereClause = "ends_at <= $1"
		args = append(args, now.UTC())
	}

	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(1) FROM flash_sales WHERE `+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count all flash sales: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	limitIdx := len(args) + 1
	offsetIdx := len(args) + 2
	listQ := fmt.Sprintf(`
		SELECT id, name, starts_at, ends_at, min_discount_percent, category_ids, created_by, created_at, updated_at
		FROM flash_sales
		WHERE %s
		ORDER BY starts_at DESC, id ASC
		LIMIT $%d OFFSET $%d
	`, whereClause, limitIdx, offsetIdx)
	queryArgs := append(append([]any(nil), args...), pageSize, offset)

	rows, err := s.pool.Query(ctx, listQ, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list all flash sales: %w", err)
	}
	defer rows.Close()

	sales := make([]domain.FlashSale, 0)
	saleIdx := make(map[string]int)
	saleIDs := make([]string, 0)
	for rows.Next() {
		fs, scanErr := scanFlashSale(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan flash sale: %w", scanErr)
		}
		saleIdx[fs.ID] = len(sales)
		saleIDs = append(saleIDs, fs.ID)
		sales = append(sales, *fs)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate all flash sales: %w", err)
	}
	if len(saleIDs) > 0 {
		if err := s.attachApprovedFlashItems(ctx, sales, saleIdx, saleIDs); err != nil {
			return nil, 0, err
		}
	}
	return sales, total, nil
}

func (s *PostgresStore) attachApprovedFlashItems(
	ctx context.Context,
	sales []domain.FlashSale,
	saleIdx map[string]int,
	saleIDs []string,
) error {
	const itemsQ = `
		SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.flash_sale_id = ANY($1)
		  AND fsi.status = 'APPROVED'
		ORDER BY fsi.created_at ASC, fsi.id ASC
	`
	itemRows, err := s.pool.Query(ctx, itemsQ, saleIDs)
	if err != nil {
		return fmt.Errorf("list flash sale items: %w", err)
	}
	defer itemRows.Close()

	for itemRows.Next() {
		item, scanErr := scanFlashSaleItem(itemRows)
		if scanErr != nil {
			return fmt.Errorf("scan flash sale item: %w", scanErr)
		}
		if idx, ok := saleIdx[item.FlashSaleID]; ok {
			sales[idx].Items = append(sales[idx].Items, *item)
		}
	}
	return itemRows.Err()
}

func (s *PostgresStore) ListFlashSaleItems(
	ctx context.Context,
	statusFilter, flashSaleID, shopID string,
	page, pageSize int32,
) ([]domain.FlashSaleItem, int64, error) {
	page, pageSize = normalizePage(page, pageSize)
	offset := (page - 1) * pageSize

	const countQ = `
		SELECT COUNT(1)
		FROM flash_sale_items fsi
		WHERE ($1 = '' OR fsi.status = $1)
		  AND ($2 = '' OR fsi.flash_sale_id = $2)
		  AND ($3 = '' OR fsi.shop_id = $3)
	`
	var total int64
	if err := s.pool.QueryRow(ctx, countQ, statusFilter, flashSaleID, shopID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count flash sale items: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	const q = `
		SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE ($1 = '' OR fsi.status = $1)
		  AND ($2 = '' OR fsi.flash_sale_id = $2)
		  AND ($3 = '' OR fsi.shop_id = $3)
		ORDER BY fsi.created_at DESC, fsi.id ASC
		LIMIT $4 OFFSET $5
	`
	rows, err := s.pool.Query(ctx, q, statusFilter, flashSaleID, shopID, pageSize, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list flash sale items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.FlashSaleItem, 0)
	for rows.Next() {
		it, scanErr := scanFlashSaleItem(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan flash sale item: %w", scanErr)
		}
		items = append(items, *it)
	}
	return items, total, rows.Err()
}

func (s *PostgresStore) ListApprovedFlashSaleItems(ctx context.Context) ([]domain.FlashSaleItem, error) {
	const q = `
		SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.status = 'APPROVED'
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list approved flash items: %w", err)
	}
	defer rows.Close()

	var items []domain.FlashSaleItem
	for rows.Next() {
		item, scanErr := scanFlashSaleItem(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan approved flash item: %w", scanErr)
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) HasOverlappingApprovedFlashSale(ctx context.Context, skuID string, startsAt, endsAt, now time.Time) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1
			FROM flash_sale_items fsi
			JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
			WHERE fsi.sku_id = $1
			  AND fsi.status = 'APPROVED'
			  AND fs.ends_at > $2
			  AND fs.starts_at < $3
			  AND fs.ends_at > $4
		)
	`
	var exists bool
	if err := s.pool.QueryRow(ctx, q, skuID, now.UTC(), endsAt.UTC(), startsAt.UTC()).Scan(&exists); err != nil {
		return false, fmt.Errorf("check overlapping approved flash sale: %w", err)
	}
	return exists, nil
}

func (s *PostgresStore) GetCoinBalance(ctx context.Context, userID string, recentLimit int) (int64, []domain.CoinTransaction, error) {
	if recentLimit <= 0 {
		recentLimit = 50
	}

	// Authoritative ledger invariant: balance ALWAYS equals SUM(coin_transactions.delta).
	var balance int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(delta), 0) FROM coin_transactions WHERE user_id = $1`, userID).Scan(&balance); err != nil {
		return 0, nil, fmt.Errorf("sum coin transactions: %w", err)
	}

	const q = `
		SELECT id, user_id, delta, reason, reference_id, created_at
		FROM coin_transactions
		WHERE user_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2
	`
	rows, err := s.pool.Query(ctx, q, userID, recentLimit)
	if err != nil {
		return 0, nil, fmt.Errorf("list coin transactions: %w", err)
	}
	defer rows.Close()

	txns := make([]domain.CoinTransaction, 0)
	for rows.Next() {
		var t domain.CoinTransaction
		if scanErr := rows.Scan(&t.ID, &t.UserID, &t.Delta, &t.Reason, &t.ReferenceID, &t.CreatedAt); scanErr != nil {
			return 0, nil, fmt.Errorf("scan coin transaction: %w", scanErr)
		}
		txns = append(txns, t)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, fmt.Errorf("iterate coin transactions: %w", err)
	}
	return balance, txns, nil
}

func (s *PostgresStore) CreditCoins(ctx context.Context, userID string, amount int64, reason, referenceID string) (int64, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, false, fmt.Errorf("begin credit coins tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	bal, already, err := creditCoinsInTx(ctx, tx, userID, amount, reason, referenceID)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("commit credit coins: %w", err)
	}
	return bal, already, nil
}

func (s *PostgresStore) ProcessOrderCompletedEvent(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin order completed tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_events (event_id, subject) VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`,
		eventID, subject,
	)
	if err != nil {
		return false, fmt.Errorf("insert processed event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return true, nil
	}

	if coinsReward > 0 && buyerID != "" {
		refID := orderID
		if refID == "" {
			refID = eventID
		}
		if _, _, err := creditCoinsInTx(ctx, tx, buyerID, coinsReward, "order_completed_reward", refID); err != nil {
			return false, fmt.Errorf("credit reward coins: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit order completed tx: %w", err)
	}
	return false, nil
}

func creditCoinsInTx(ctx context.Context, tx pgx.Tx, userID string, amount int64, reason, referenceID string) (int64, bool, error) {
	if _, err := tx.Exec(ctx,
		`INSERT INTO coin_accounts (user_id, balance) VALUES ($1, 0) ON CONFLICT (user_id) DO NOTHING`,
		userID,
	); err != nil {
		return 0, false, fmt.Errorf("ensure coin account: %w", err)
	}

	var curBalance int64
	if err := tx.QueryRow(ctx, `SELECT balance FROM coin_accounts WHERE user_id = $1 FOR UPDATE`, userID).Scan(&curBalance); err != nil {
		return 0, false, fmt.Errorf("lock coin account: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO coin_transactions (id, user_id, delta, reason, reference_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (reference_id) DO NOTHING
	`, uuid.NewString(), userID, amount, reason, referenceID)
	if err != nil {
		return 0, false, fmt.Errorf("insert coin transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return curBalance, true, nil
	}

	var newBalance int64
	if err := tx.QueryRow(ctx, `
		UPDATE coin_accounts
		SET balance = balance + $2, updated_at = NOW()
		WHERE user_id = $1
		RETURNING balance
	`, userID, amount).Scan(&newBalance); err != nil {
		return 0, false, fmt.Errorf("update coin account balance: %w", err)
	}
	return newBalance, false, nil
}

func (s *PostgresStore) GetReservation(ctx context.Context, orderID string) (*domain.PromotionReservation, error) {
	const q = `
		SELECT order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json,
			shop_program_items_json, breakdown_json, released_shops_json, created_at, released_at, updated_at
		FROM promotion_reservations
		WHERE order_id = $1
	`
	res, err := scanReservation(s.pool.QueryRow(ctx, q, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get reservation: %w", err)
	}
	return res, nil
}

func (s *PostgresStore) CommitReservation(ctx context.Context, res domain.PromotionReservation, now time.Time) (*domain.PromotionReservation, bool, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin commit reservation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock order matches ReleaseReservation/ReleaseReservationShop on shared tables, and locks parent
	// shop_discount_programs FOR SHARE (id ASC, never locking shop_discount_program_items) to match
	// UpdateShopDiscountProgram/EndShopDiscountProgram (sdp FOR UPDATE -> sdi):
	// promotion_reservations -> coin_accounts -> vouchers (id ASC) -> flash_sale_items (id ASC) -> shop_discount_programs (id ASC FOR SHARE).
	existing, err := scanReservation(tx.QueryRow(ctx, `
		SELECT order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json,
			shop_program_items_json, breakdown_json, released_shops_json, created_at, released_at, updated_at
		FROM promotion_reservations
		WHERE order_id = $1
		FOR UPDATE
	`, res.OrderID))
	if err == nil {
		if existing.Status == domain.ReservationStatusReleased {
			return nil, false, ErrReservationAlreadyReleased
		}
		return existing, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, fmt.Errorf("query existing reservation: %w", err)
	}

	vouchersJSON, err := json.Marshal(nonNilSlice(res.Vouchers))
	if err != nil {
		return nil, false, fmt.Errorf("marshal reserved vouchers: %w", err)
	}
	flashJSON, err := json.Marshal(nonNilSlice(res.FlashSaleItems))
	if err != nil {
		return nil, false, fmt.Errorf("marshal reserved flash items: %w", err)
	}
	shopProgJSON, err := json.Marshal(nonNilSlice(res.ShopProgramItems))
	if err != nil {
		return nil, false, fmt.Errorf("marshal reserved shop program items: %w", err)
	}
	breakdownJSON := res.BreakdownJSON
	if len(breakdownJSON) == 0 {
		breakdownJSON = []byte("{}")
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO promotion_reservations (
			order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json,
			shop_program_items_json, breakdown_json
		) VALUES ($1, $2, 'RESERVED', $3, $4::jsonb, $5::jsonb, $6::jsonb, $7::jsonb)
		ON CONFLICT (order_id) DO NOTHING
	`, res.OrderID, res.UserID, res.CoinsUsed, string(vouchersJSON), string(flashJSON), string(shopProgJSON), string(breakdownJSON))
	if err != nil {
		return nil, false, fmt.Errorf("insert promotion reservation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		_ = tx.Rollback(ctx)
		saved, getErr := s.GetReservation(ctx, res.OrderID)
		if getErr != nil {
			return nil, false, getErr
		}
		if saved.Status == domain.ReservationStatusReleased {
			return nil, false, ErrReservationAlreadyReleased
		}
		return saved, true, nil
	}

	if res.CoinsUsed > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO coin_accounts (user_id, balance) VALUES ($1, 0) ON CONFLICT (user_id) DO NOTHING`,
			res.UserID,
		); err != nil {
			return nil, false, fmt.Errorf("ensure coin account: %w", err)
		}

		var balance int64
		if err := tx.QueryRow(ctx, `SELECT balance FROM coin_accounts WHERE user_id = $1 FOR UPDATE`, res.UserID).Scan(&balance); err != nil {
			return nil, false, fmt.Errorf("lock coin account: %w", err)
		}
		if balance < res.CoinsUsed {
			return nil, false, ErrInsufficientCoins
		}

		reserveRef := "reserve:" + res.OrderID
		coinTag, err := tx.Exec(ctx, `
			INSERT INTO coin_transactions (id, user_id, delta, reason, reference_id)
			VALUES ($1, $2, $3, 'order_reserve', $4)
			ON CONFLICT (reference_id) DO NOTHING
		`, uuid.NewString(), res.UserID, -res.CoinsUsed, reserveRef)
		if err != nil {
			return nil, false, fmt.Errorf("insert coin reserve tx: %w", err)
		}
		if coinTag.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE coin_accounts
				SET balance = balance - $2, updated_at = NOW()
				WHERE user_id = $1
			`, res.UserID, res.CoinsUsed); err != nil {
				return nil, false, fmt.Errorf("debit coin account: %w", err)
			}
		}
	}

	// Authoritative Postgres final gate for vouchers (sorted by ID to match release lock order).
	sortedVouchers := append([]domain.ReservedVoucher(nil), res.Vouchers...)
	sort.Slice(sortedVouchers, func(i, j int) bool { return sortedVouchers[i].VoucherID < sortedVouchers[j].VoucherID })
	for _, v := range sortedVouchers {
		vTag, err := tx.Exec(ctx, `
			UPDATE vouchers
			SET used_count = used_count + 1,
				used_budget_vnd = used_budget_vnd + $2,
				updated_at = $3
			WHERE id = $1
			  AND status = 'ACTIVE'
			  AND is_active = TRUE
			  AND used_count < quota
			  AND (budget_vnd = 0 OR used_budget_vnd + $2 <= budget_vnd)
			  AND starts_at <= $3
			  AND ends_at >= $3
		`, v.VoucherID, v.DiscountVND, now)
		if err != nil {
			return nil, false, fmt.Errorf("sync voucher usage: %w", err)
		}
		if vTag.RowsAffected() == 0 {
			return nil, false, fmt.Errorf("%w: voucher %s", ErrVoucherNotEligibleForCommit, v.VoucherID)
		}
	}

	// Authoritative Postgres final gate for flash sale slots (sorted by ID; no GREATEST clamp).
	sortedFlash := append([]domain.ReservedFlashItem(nil), res.FlashSaleItems...)
	sort.Slice(sortedFlash, func(i, j int) bool { return sortedFlash[i].FlashSaleItemID < sortedFlash[j].FlashSaleItemID })
	for _, f := range sortedFlash {
		fTag, err := tx.Exec(ctx, `
			UPDATE flash_sale_items fsi
			SET remaining_slots = fsi.remaining_slots - $2,
				updated_at = $3
			FROM flash_sales fs
			WHERE fsi.id = $1
			  AND fs.id = fsi.flash_sale_id
			  AND fsi.status = 'APPROVED'
			  AND fsi.remaining_slots >= $2
			  AND fs.starts_at <= $3
			  AND fs.ends_at > $3
		`, f.FlashSaleItemID, f.Quantity, now)
		if err != nil {
			return nil, false, fmt.Errorf("sync flash remaining slots: %w", err)
		}
		if fTag.RowsAffected() == 0 {
			return nil, false, fmt.Errorf("%w: flash item %s", ErrFlashNotEligibleForCommit, f.FlashSaleItemID)
		}
	}

	// Authoritative Postgres final gate for shop discount program items:
	// 1. Lock ONLY parent shop_discount_programs in id ASC order (FOR SHARE OF sdp, never locking sdi)
	//    to match UpdateShopDiscountProgram/EndShopDiscountProgram (sdp FOR UPDATE -> sdi) and eliminate AB-BA deadlocks.
	// 2. Verify items and active window in a separate statement (fresh READ COMMITTED snapshot after lock acquisition).
	if len(res.ShopProgramItems) > 0 {
		sortedProgs := append([]domain.ReservedShopProgramItem(nil), res.ShopProgramItems...)
		sort.Slice(sortedProgs, func(i, j int) bool {
			return sortedProgs[i].ShopDiscountProgramItemID < sortedProgs[j].ShopDiscountProgramItemID
		})
		itemIDs := make([]string, 0, len(sortedProgs))
		for _, p := range sortedProgs {
			itemIDs = append(itemIDs, p.ShopDiscountProgramItemID)
		}
		lockRows, err := tx.Query(ctx, `
			SELECT sdp.id
			FROM shop_discount_programs sdp
			WHERE sdp.id IN (
				SELECT DISTINCT program_id
				FROM shop_discount_program_items
				WHERE id = ANY($1)
			)
			ORDER BY sdp.id ASC
			FOR SHARE OF sdp
		`, itemIDs)
		if err != nil {
			return nil, false, fmt.Errorf("lock shop discount programs: %w", err)
		}
		for lockRows.Next() {
			var lockedProgID string
			if scanErr := lockRows.Scan(&lockedProgID); scanErr != nil {
				lockRows.Close()
				return nil, false, fmt.Errorf("scan locked shop discount program: %w", scanErr)
			}
		}
		if err := lockRows.Err(); err != nil {
			lockRows.Close()
			return nil, false, fmt.Errorf("iterate locked shop discount programs: %w", err)
		}
		lockRows.Close()

		for _, p := range sortedProgs {
			var verifiedID string
			err := tx.QueryRow(ctx, `
				SELECT sdi.id
				FROM shop_discount_program_items sdi
				JOIN shop_discount_programs sdp ON sdp.id = sdi.program_id
				WHERE sdi.id = $1
				  AND ($2 = '' OR sdi.shop_id = $2)
				  AND ($3 = '' OR sdi.sku_id = $3)
				  AND sdp.status <> 'ENDED'
				  AND sdp.ended_at IS NULL
				  AND sdp.starts_at <= $4
				  AND sdp.ends_at > $4
			`, p.ShopDiscountProgramItemID, p.ShopID, p.SKUID, now).Scan(&verifiedID)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, false, fmt.Errorf("%w: shop discount program item %s", ErrShopProgramNotEligibleForCommit, p.ShopDiscountProgramItemID)
			}
			if err != nil {
				return nil, false, fmt.Errorf("verify shop discount program item: %w", err)
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit reservation tx: %w", err)
	}
	res.Status = domain.ReservationStatusReserved
	return &res, false, nil
}

func (s *PostgresStore) ReleaseReservation(ctx context.Context, orderID string) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin release reservation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const selectForUpdateQ = `
		SELECT order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json,
			shop_program_items_json, breakdown_json, released_shops_json, created_at, released_at, updated_at
		FROM promotion_reservations
		WHERE order_id = $1
		FOR UPDATE
	`
	existing, err := scanReservation(tx.QueryRow(ctx, selectForUpdateQ, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		// Write a RELEASED tombstone so any late-arriving CommitReservation sees ErrReservationAlreadyReleased
		// and compensates Redis cleanly.
		tag, insErr := tx.Exec(ctx, `
			INSERT INTO promotion_reservations (
				order_id, user_id, status, coins_used,
				vouchers_json, flash_sale_items_json, shop_program_items_json,
				breakdown_json, released_shops_json, released_at
			) VALUES ($1, '', 'RELEASED', 0, '[]'::jsonb, '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, '[]'::jsonb, NOW())
			ON CONFLICT (order_id) DO NOTHING
		`, orderID)
		if insErr != nil {
			return false, fmt.Errorf("insert release tombstone: %w", insErr)
		}
		if tag.RowsAffected() > 0 {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit release tombstone: %w", err)
			}
			return true, nil
		}
		// A concurrent CommitReservation just inserted the row — lock and release it now.
		existing, err = scanReservation(tx.QueryRow(ctx, selectForUpdateQ, orderID))
		if err != nil {
			return false, fmt.Errorf("re-lock reservation after tombstone conflict: %w", err)
		}
	} else if err != nil {
		return false, fmt.Errorf("lock reservation for release: %w", err)
	}

	plan, err := planRemainingRelease(existing, lookupFlashShopInTx(ctx, tx), lookupShopProgShopInTx(ctx, tx))
	if err != nil {
		return false, fmt.Errorf("plan remaining release: %w", err)
	}
	if plan.alreadyReleased {
		return true, nil
	}

	if plan.coinsToReturn > 0 {
		releaseRef := "release:" + orderID
		if _, _, err := creditCoinsInTx(ctx, tx, existing.UserID, plan.coinsToReturn, "order_release", releaseRef); err != nil {
			return false, fmt.Errorf("restore reserved coins: %w", err)
		}
	}

	if err := restoreVouchersAndFlashInTx(ctx, tx, plan.vouchersToRelease, plan.flashToRelease); err != nil {
		return false, err
	}

	releasedShopsJSON, err := json.Marshal(plan.newReleasedShops)
	if err != nil {
		return false, fmt.Errorf("marshal released shops: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE promotion_reservations
		SET status = 'RELEASED',
			released_shops_json = $2::jsonb,
			released_at = NOW(),
			updated_at = NOW()
		WHERE order_id = $1
	`, orderID, string(releasedShopsJSON)); err != nil {
		return false, fmt.Errorf("mark reservation released: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit release reservation tx: %w", err)
	}
	return false, nil
}

func (s *PostgresStore) ReleaseReservationShop(ctx context.Context, orderID, shopID string) (*ShopReleaseResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin release shop reservation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	existing, err := scanReservation(tx.QueryRow(ctx, `
		SELECT order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json,
			shop_program_items_json, breakdown_json, released_shops_json, created_at, released_at, updated_at
		FROM promotion_reservations
		WHERE order_id = $1
		FOR UPDATE
	`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return &ShopReleaseResult{AlreadyReleased: true}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock reservation for shop release: %w", err)
	}

	plan, err := planShopRelease(existing, shopID, lookupFlashShopInTx(ctx, tx), lookupShopProgShopInTx(ctx, tx))
	if err != nil {
		return nil, fmt.Errorf("plan shop release: %w", err)
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
		TargetVouchers:              plan.targetVouchersForRedis,
		TargetFlash:                 plan.targetFlashForRedis,
		TargetShopPrograms:          plan.targetShopProgramsForRedis,
		AllVouchers:                 existing.Vouchers,
		AllFlash:                    existing.FlashSaleItems,
		AllShopPrograms:             existing.ShopProgramItems,
	}
	if plan.alreadyReleased {
		return result, nil
	}

	if plan.coinsToReturn > 0 {
		releaseRef := "release:" + orderID + ":shop:" + shopID
		if _, _, err := creditCoinsInTx(ctx, tx, existing.UserID, plan.coinsToReturn, "order_release", releaseRef); err != nil {
			return nil, fmt.Errorf("restore shop reserved coins: %w", err)
		}
	}

	if err := restoreVouchersAndFlashInTx(ctx, tx, plan.vouchersToRelease, plan.flashToRelease); err != nil {
		return nil, err
	}

	releasedShopsJSON, err := json.Marshal(plan.newReleasedShops)
	if err != nil {
		return nil, fmt.Errorf("marshal released shops: %w", err)
	}

	if plan.allReleased {
		if _, err := tx.Exec(ctx, `
			UPDATE promotion_reservations
			SET status = 'RELEASED',
				released_shops_json = $2::jsonb,
				released_at = NOW(),
				updated_at = NOW()
			WHERE order_id = $1
		`, orderID, string(releasedShopsJSON)); err != nil {
			return nil, fmt.Errorf("mark reservation released after last shop: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE promotion_reservations
			SET released_shops_json = $2::jsonb,
				updated_at = NOW()
			WHERE order_id = $1
		`, orderID, string(releasedShopsJSON)); err != nil {
			return nil, fmt.Errorf("record released shop on reservation: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit release shop reservation tx: %w", err)
	}
	return result, nil
}

func lookupFlashShopInTx(ctx context.Context, tx pgx.Tx) func(itemID string) (string, error) {
	return func(itemID string) (string, error) {
		var shopID string
		err := tx.QueryRow(ctx, `SELECT shop_id FROM flash_sale_items WHERE id = $1`, itemID).Scan(&shopID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("lookup flash item %s shop_id: %w", itemID, err)
		}
		return shopID, nil
	}
}

func lookupShopProgShopInTx(ctx context.Context, tx pgx.Tx) func(itemID string) (string, error) {
	return func(itemID string) (string, error) {
		var shopID string
		err := tx.QueryRow(ctx, `SELECT shop_id FROM shop_discount_program_items WHERE id = $1`, itemID).Scan(&shopID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("lookup shop discount item %s shop_id: %w", itemID, err)
		}
		return shopID, nil
	}
}

func restoreVouchersAndFlashInTx(ctx context.Context, tx pgx.Tx, vouchers []domain.ReservedVoucher, flashItems []domain.ReservedFlashItem) error {
	sortedVouchers := append([]domain.ReservedVoucher(nil), vouchers...)
	sort.Slice(sortedVouchers, func(i, j int) bool { return sortedVouchers[i].VoucherID < sortedVouchers[j].VoucherID })
	for _, v := range sortedVouchers {
		if _, err := tx.Exec(ctx, `
			UPDATE vouchers
			SET used_count = GREATEST(used_count - 1, 0),
				used_budget_vnd = GREATEST(used_budget_vnd - $2, 0),
				updated_at = NOW()
			WHERE id = $1
		`, v.VoucherID, v.DiscountVND); err != nil {
			return fmt.Errorf("restore voucher usage: %w", err)
		}
	}

	sortedFlash := append([]domain.ReservedFlashItem(nil), flashItems...)
	sort.Slice(sortedFlash, func(i, j int) bool { return sortedFlash[i].FlashSaleItemID < sortedFlash[j].FlashSaleItemID })
	for _, f := range sortedFlash {
		if _, err := tx.Exec(ctx, `
			UPDATE flash_sale_items
			SET remaining_slots = LEAST(remaining_slots + $2, total_slots),
				updated_at = NOW()
			WHERE id = $1
		`, f.FlashSaleItemID, f.Quantity); err != nil {
			return fmt.Errorf("restore flash remaining slots: %w", err)
		}
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVoucher(row rowScanner) (*domain.Voucher, error) {
	var v domain.Voucher
	var catRaw []byte
	err := row.Scan(
		&v.ID, &v.Code, &v.Name, &v.Scope, &v.ShopID, &v.DiscountType, &v.ValueVND, &v.DiscountPercent,
		&v.MaxDiscountVND, &v.MinOrderVND, &v.Quota, &v.UsedCount, &v.PerUserLimit,
		&v.BudgetVND, &v.UsedBudgetVND, &v.StartsAt, &v.EndsAt, &v.IsActive, &v.Status, &catRaw, &v.DeletedAt,
		&v.CreatedBy, &v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(catRaw) > 0 {
		_ = json.Unmarshal(catRaw, &v.CategoryIDs)
	}
	v.Status = v.EffectiveStatus()
	return &v, nil
}

func scanFlashSale(row rowScanner) (*domain.FlashSale, error) {
	var fs domain.FlashSale
	var catRaw []byte
	err := row.Scan(
		&fs.ID, &fs.Name, &fs.StartsAt, &fs.EndsAt, &fs.MinDiscountPercent, &catRaw,
		&fs.CreatedBy, &fs.CreatedAt, &fs.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(catRaw) > 0 {
		_ = json.Unmarshal(catRaw, &fs.CategoryIDs)
	}
	return &fs, nil
}

func scanFlashSaleItem(row rowScanner) (*domain.FlashSaleItem, error) {
	var item domain.FlashSaleItem
	err := row.Scan(
		&item.ID, &item.FlashSaleID, &item.ShopID, &item.SKUID, &item.OriginalPriceVND, &item.FlashPriceVND,
		&item.TotalSlots, &item.RemainingSlots, &item.PerUserLimit, &item.Status, &item.RejectionReason,
		&item.ApprovedBy, &item.StartsAt, &item.EndsAt, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func scanReservation(row rowScanner) (*domain.PromotionReservation, error) {
	var res domain.PromotionReservation
	var vouchersRaw, flashRaw, shopProgRaw, breakdownRaw, releasedShopsRaw []byte
	err := row.Scan(
		&res.OrderID, &res.UserID, &res.Status, &res.CoinsUsed,
		&vouchersRaw, &flashRaw, &shopProgRaw, &breakdownRaw, &releasedShopsRaw,
		&res.CreatedAt, &res.ReleasedAt, &res.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if len(vouchersRaw) > 0 {
		_ = json.Unmarshal(vouchersRaw, &res.Vouchers)
	}
	if len(flashRaw) > 0 {
		_ = json.Unmarshal(flashRaw, &res.FlashSaleItems)
	}
	if len(shopProgRaw) > 0 {
		_ = json.Unmarshal(shopProgRaw, &res.ShopProgramItems)
	}
	if len(releasedShopsRaw) > 0 {
		_ = json.Unmarshal(releasedShopsRaw, &res.ReleasedShops)
	}
	res.BreakdownJSON = breakdownRaw
	return &res, nil
}

func marshalStringSliceJSON(s []string) ([]byte, error) {
	if s == nil {
		s = []string{}
	}
	return json.Marshal(s)
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func normalizePage(page, pageSize int32) (int32, int32) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

func cloneVoucher(v domain.Voucher) domain.Voucher {
	cp := v
	cp.CategoryIDs = append([]string(nil), v.CategoryIDs...)
	if v.DeletedAt != nil {
		t := *v.DeletedAt
		cp.DeletedAt = &t
	}
	return cp
}
