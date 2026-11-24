package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strings"
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
	ErrVoucherUserLimitExceeded        = errors.New("voucher per-user limit exceeded")
	ErrFlashUserLimitExceeded          = errors.New("flash sale item per-user limit exceeded")
	ErrShopProgramUserLimitExceeded    = errors.New("shop discount program item per-user limit exceeded")
	ErrReservedCoinReferenceID         = errors.New("coin reference_id uses a reserved internal prefix")
	ErrCoinLedgerConflict              = errors.New("coin ledger reference conflict")
)

const voucherSelectColumns = `id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
	max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
	budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, status, category_ids, deleted_at,
	rules_version, redis_sync_version, redis_synced_version,
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
	UpdateVoucher(ctx context.Context, id string, mutate func(cur *domain.Voucher, hasActiveReservation bool) error) (*domain.Voucher, error)
	SetVoucherStatus(ctx context.Context, id, newStatus string, check func(cur *domain.Voucher) error) (*domain.Voucher, error)
	DeleteVoucher(ctx context.Context, id string, check func(cur *domain.Voucher) error) (alreadyDeleted bool, err error)
	SyncVoucherRedis(ctx context.Context, voucherID string, skipLocked bool, syncFn func(ctx context.Context, v *domain.Voucher) error) (synced bool, err error)
	ReconcilePendingVouchers(ctx context.Context, limit int, syncFn func(ctx context.Context, v *domain.Voucher) error) (reconciled int, err error)
	ListVouchers(ctx context.Context, shopID, scope string) ([]domain.Voucher, error)
	ListAllVouchers(ctx context.Context) ([]domain.Voucher, error)
	ListActiveVouchers(ctx context.Context, now time.Time) ([]domain.Voucher, error)
	GetUserUsageBatch(ctx context.Context, userID string, targets []UserUsageKey) (map[UserUsageKey]int32, error)
	SyncUserUsageRedis(ctx context.Context, kind, targetID, userID string, skipLocked bool, syncFn func(ctx context.Context, usedQty int32) error) (synced bool, err error)
	SaveUserVoucher(ctx context.Context, userID, voucherID string) (alreadySaved bool, err error)
	GetUserSavedVoucherIDs(ctx context.Context, userID string) (map[string]bool, error)
	ProcessExpiringSavedVouchers(ctx context.Context, now time.Time, horizon time.Duration, batchSize int, notifyFn func(ctx context.Context, item domain.ExpiringSavedVoucher) error) (sent int, scanned int, err error)

	// Flash sales
	CreateFlashSale(ctx context.Context, fs domain.FlashSale) (*domain.FlashSale, error)
	GetFlashSale(ctx context.Context, id string) (*domain.FlashSale, error)
	RegisterFlashSaleItem(ctx context.Context, item domain.FlashSaleItem, now time.Time) (*domain.FlashSaleItem, error)
	ApproveFlashSaleItem(ctx context.Context, itemID, adminID string, approve bool, reason string, originalPriceVND int64, now time.Time) (item *domain.FlashSaleItem, prevStatus string, err error)
	GetFlashSaleItem(ctx context.Context, itemID string) (*domain.FlashSaleItem, error)
	SyncFlashItemRedis(ctx context.Context, itemID string, skipLocked bool, syncFn func(ctx context.Context, item *domain.FlashSaleItem) error) (synced bool, err error)
	ListFlashSales(ctx context.Context, now time.Time, includeUpcoming bool) ([]domain.FlashSale, error)
	ListAllFlashSales(ctx context.Context, now time.Time, statusFilter string, page, pageSize int32) ([]domain.FlashSale, int64, error)
	ListFlashSaleItems(ctx context.Context, statusFilter, flashSaleID, shopID string, page, pageSize int32) ([]domain.FlashSaleItem, int64, error)
	ListApprovedFlashSaleItems(ctx context.Context) ([]domain.FlashSaleItem, error)
	ListActiveApprovedFlashSaleItems(ctx context.Context, now time.Time) ([]domain.FlashSaleItem, error)
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

	// Shop suspension state (F12)
	ProcessShopStatusChangedEvent(ctx context.Context, eventID, subject, shopID string, suspended bool, version int64) (alreadyProcessed bool, stale bool, err error)
	IsShopSuspended(ctx context.Context, shopID string) (bool, error)
	GetSuspendedShopIDs(ctx context.Context, shopIDs []string) (map[string]bool, error)

	// Reservations
	GetReservation(ctx context.Context, orderID string) (*domain.PromotionReservation, error)
	CommitReservation(ctx context.Context, res domain.PromotionReservation, now time.Time) (existing *domain.PromotionReservation, alreadyReserved bool, err error)
	ReleaseReservation(ctx context.Context, orderID string) (*FullReleaseResult, error)
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

// Migrate applies embedded SQL migrations in lexical order under a Postgres advisory lock,
// recording applied filenames in promotion_schema_migrations so concurrent startups or rolling
// restarts do not re-acquire ACCESS EXCLUSIVE table locks once a migration has already been applied.
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

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin migrate tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize concurrent Migrate() invocations across replicas / test packages.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7042001)`); err != nil {
		return fmt.Errorf("acquire migrate advisory lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS promotion_schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create promotion_schema_migrations: %w", err)
	}

	for _, name := range names {
		var alreadyApplied bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM promotion_schema_migrations WHERE name = $1)`, name).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if alreadyApplied {
			continue
		}
		sqlBytes, readErr := migrationsFS.ReadFile("migrations/" + name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		if _, execErr := tx.Exec(ctx, string(sqlBytes)); execErr != nil {
			return fmt.Errorf("exec migration %s: %w", name, execErr)
		}
		if _, insErr := tx.Exec(ctx, `INSERT INTO promotion_schema_migrations (name) VALUES ($1) ON CONFLICT DO NOTHING`, name); insErr != nil {
			return fmt.Errorf("record migration %s: %w", name, insErr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrate tx: %w", err)
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
			rules_version, redis_sync_version, redis_synced_version,
			created_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, 0, $12,
			$13, 0, $14, $15, $16, $17, $18::jsonb,
			1, 1, 0,
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
	mutate func(cur *domain.Voucher, hasActiveReservation bool) error,
) (*domain.Voucher, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin update voucher tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cur, err := scanVoucher(tx.QueryRow(ctx, `SELECT `+voucherSelectColumns+` FROM vouchers WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock voucher for update: %w", err)
	}

	shopFilter := ""
	if cur.Scope == domain.VoucherScopeShop {
		shopFilter = cur.ShopID
	}
	var hasActiveReservation bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM promotion_reservations
			WHERE status = 'RESERVED'
			  AND vouchers_json @> jsonb_build_array(jsonb_build_object('voucher_id', $1::text))
			  AND ($2 = '' OR NOT (released_shops_json ? $2))
		)
	`, cur.ID, shopFilter).Scan(&hasActiveReservation); err != nil {
		return nil, fmt.Errorf("check active reservations for voucher: %w", err)
	}

	if err := mutate(cur, hasActiveReservation); err != nil {
		return nil, err
	}

	catJSON, err := marshalStringSliceJSON(cur.CategoryIDs)
	if err != nil {
		return nil, fmt.Errorf("marshal voucher category_ids: %w", err)
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
			rules_version = $14,
			redis_sync_version = redis_sync_version + 1,
			updated_at = NOW()
		WHERE id = $1
		RETURNING ` + voucherSelectColumns

	after, err := scanVoucher(tx.QueryRow(ctx, updateQ,
		cur.ID, cur.Name, cur.DiscountType, cur.ValueVND, cur.DiscountPercent,
		cur.MaxDiscountVND, cur.MinOrderVND, cur.Quota, cur.PerUserLimit,
		cur.BudgetVND, cur.StartsAt.UTC(), cur.EndsAt.UTC(), string(catJSON), cur.RulesVersion,
	))
	if err != nil {
		return nil, fmt.Errorf("update voucher: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update voucher: %w", err)
	}
	return after, nil
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
			redis_sync_version = redis_sync_version + 1,
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
			redis_sync_version = redis_sync_version + 1,
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

func (s *PostgresStore) SyncVoucherRedis(
	ctx context.Context,
	voucherID string,
	skipLocked bool,
	syncFn func(ctx context.Context, v *domain.Voucher) error,
) (bool, error) {
	return s.syncVoucherRedisLocked(ctx, voucherID, false, skipLocked, syncFn)
}

func (s *PostgresStore) ReconcilePendingVouchers(
	ctx context.Context,
	limit int,
	syncFn func(ctx context.Context, v *domain.Voucher) error,
) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id
		FROM vouchers
		WHERE redis_synced_version < redis_sync_version
		ORDER BY updated_at ASC, id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return 0, fmt.Errorf("list pending redis sync vouchers: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("scan pending voucher id: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate pending voucher ids: %w", err)
	}
	rows.Close()

	reconciled := 0
	var firstErr error
	for _, id := range ids {
		ok, rErr := s.syncVoucherRedisLocked(ctx, id, true, true, syncFn)
		if rErr != nil {
			if firstErr == nil {
				firstErr = rErr
			}
			if IsRedisConnOrTimeoutErr(rErr) {
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

// IsRedisConnOrTimeoutErr reports whether err indicates a Redis timeout, cancellation, or connection drop.
func IsRedisConnOrTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "client is closed") ||
		strings.Contains(msg, "eof")
}

func (s *PostgresStore) syncVoucherRedisLocked(
	ctx context.Context,
	voucherID string,
	onlyIfDirty bool,
	skipLocked bool,
	syncFn func(ctx context.Context, v *domain.Voucher) error,
) (bool, error) {
	if voucherID == "" {
		return false, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin sync voucher redis tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := `SELECT ` + voucherSelectColumns + ` FROM vouchers WHERE id = $1`
	if onlyIfDirty {
		q += ` AND redis_synced_version < redis_sync_version`
	}
	q += ` FOR UPDATE`
	if skipLocked {
		q += ` SKIP LOCKED`
	}

	cur, err := scanVoucher(tx.QueryRow(ctx, q, voucherID))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock voucher %s for redis sync: %w", voucherID, err)
	}
	if syncFn != nil {
		if err := syncFn(ctx, cur); err != nil {
			return false, err
		}
	}
	if cur.RedisSyncedVersion < cur.RedisSyncVersion {
		if _, err := tx.Exec(ctx, `
			UPDATE vouchers
			SET redis_synced_version = redis_sync_version
			WHERE id = $1
		`, cur.ID); err != nil {
			return false, fmt.Errorf("mark voucher %s redis synced: %w", cur.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit sync voucher %s redis: %w", cur.ID, err)
	}
	return true, nil
}

func (s *PostgresStore) queryVouchers(ctx context.Context, q string, args ...any) ([]domain.Voucher, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query vouchers: %w", err)
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

func (s *PostgresStore) ListVouchers(ctx context.Context, shopID, scope string) ([]domain.Voucher, error) {
	const q = `
		SELECT ` + voucherSelectColumns + `
		FROM vouchers
		WHERE ($1 = '' OR shop_id = $1)
		  AND ($2 = '' OR scope = $2)
		ORDER BY created_at DESC, id ASC
	`
	return s.queryVouchers(ctx, q, shopID, scope)
}

func (s *PostgresStore) ListAllVouchers(ctx context.Context) ([]domain.Voucher, error) {
	return s.queryVouchers(ctx, `SELECT `+voucherSelectColumns+` FROM vouchers`)
}

func (s *PostgresStore) ListActiveVouchers(ctx context.Context, now time.Time) ([]domain.Voucher, error) {
	const q = `
		SELECT ` + voucherSelectColumns + `
		FROM vouchers
		WHERE status = 'ACTIVE' AND is_active = TRUE AND deleted_at IS NULL
		  AND starts_at <= $1 AND ends_at >= $1
	`
	return s.queryVouchers(ctx, q, now.UTC())
}

func (s *PostgresStore) GetUserUsageBatch(ctx context.Context, userID string, targets []UserUsageKey) (map[UserUsageKey]int32, error) {
	out := make(map[UserUsageKey]int32, len(targets))
	if userID == "" || len(targets) == 0 {
		return out, nil
	}
	seen := make(map[UserUsageKey]struct{}, len(targets))
	kinds := make([]string, 0, len(targets))
	targetIDs := make([]string, 0, len(targets))
	for _, t := range targets {
		if t.Kind == "" || t.TargetID == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		kinds = append(kinds, t.Kind)
		targetIDs = append(targetIDs, t.TargetID)
	}
	if len(kinds) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT puu.kind, puu.target_id, puu.used_qty
		FROM promotion_user_usage puu
		JOIN UNNEST($2::text[], $3::text[]) AS t(kind, target_id)
		  ON puu.kind = t.kind AND puu.target_id = t.target_id
		WHERE puu.user_id = $1
	`, userID, kinds, targetIDs)
	if err != nil {
		return nil, fmt.Errorf("batch get user usage: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k UserUsageKey
		var qty int32
		if scanErr := rows.Scan(&k.Kind, &k.TargetID, &qty); scanErr != nil {
			return nil, fmt.Errorf("scan batch user usage: %w", scanErr)
		}
		out[k] = qty
	}
	return out, rows.Err()
}

func (s *PostgresStore) SyncUserUsageRedis(
	ctx context.Context,
	kind, targetID, userID string,
	skipLocked bool,
	syncFn func(ctx context.Context, usedQty int32) error,
) (bool, error) {
	if kind == "" || targetID == "" || userID == "" {
		return false, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin sync user usage tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := `SELECT used_qty FROM promotion_user_usage WHERE kind = $1 AND target_id = $2 AND user_id = $3 FOR UPDATE`
	if skipLocked {
		q += ` SKIP LOCKED`
	}
	var qty int32
	err = tx.QueryRow(ctx, q, kind, targetID, userID).Scan(&qty)
	if errors.Is(err, pgx.ErrNoRows) {
		if skipLocked {
			var exists bool
			if exErr := tx.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					FROM promotion_user_usage
					WHERE kind = $1 AND target_id = $2 AND user_id = $3
				)
			`, kind, targetID, userID).Scan(&exists); exErr != nil {
				return false, fmt.Errorf("check user usage exists (%s %s): %w", kind, targetID, exErr)
			}
			if exists {
				// Row exists and is currently locked by another transaction; do not overwrite Redis.
				return false, nil
			}
		}
		// ponytail: P1-4 accepted — deleting an absent :user: row without a PG row lock can race a concurrent commit;
		// buildCalcInput takes max(Redis, PG), PG CommitReservation enforces per_user_limit, and :user: keys have 120s TTL.
		qty = 0
	} else if err != nil {
		return false, fmt.Errorf("lock user usage (%s %s): %w", kind, targetID, err)
	}
	if syncFn != nil {
		if err := syncFn(ctx, qty); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit sync user usage (%s %s): %w", kind, targetID, err)
	}
	return true, nil
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
		LEFT JOIN promotion_user_usage puu
		       ON puu.kind = 'VOUCHER'
		      AND puu.target_id = uv.voucher_id
		      AND puu.user_id = uv.user_id
		WHERE uv.expiry_notified_at IS NULL
		  AND v.status = 'ACTIVE'
		  AND v.is_active = TRUE
		  AND v.used_count < v.quota
		  AND (v.budget_vnd = 0 OR v.used_budget_vnd < v.budget_vnd)
		  AND (v.per_user_limit <= 0 OR COALESCE(puu.used_qty, 0) < v.per_user_limit)
		  AND NOT (
		      v.scope = 'SHOP' AND EXISTS (
		          SELECT 1 FROM shop_suspension_state sss
		          WHERE sss.shop_id = v.shop_id AND sss.suspended = TRUE
		      )
		  )
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

	// ponytail: P2-10 deferred — holding FOR UPDATE OF uv SKIP LOCKED across NATS publish is kept
	// for now (bounded by nats.Context(ctx) and VoucherExpiryJob's 30s batch timeout); refactor to outbox/claim-then-publish later.
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

func (s *PostgresStore) SyncFlashItemRedis(
	ctx context.Context,
	itemID string,
	skipLocked bool,
	syncFn func(ctx context.Context, item *domain.FlashSaleItem) error,
) (bool, error) {
	if itemID == "" {
		return false, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin sync flash item redis tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := `SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.id = $1
		FOR UPDATE OF fsi`
	if skipLocked {
		q += ` SKIP LOCKED`
	}

	cur, err := scanFlashSaleItem(tx.QueryRow(ctx, q, itemID))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock flash item %s for redis sync: %w", itemID, err)
	}
	if syncFn != nil {
		if err := syncFn(ctx, cur); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit sync flash item %s redis: %w", itemID, err)
	}
	return true, nil
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

	if err := s.attachApprovedFlashItems(ctx, sales, saleIdx, saleIDs, true); err != nil {
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
		if err := s.attachApprovedFlashItems(ctx, sales, saleIdx, saleIDs, false); err != nil {
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
	excludeSuspended bool,
) error {
	itemsQ := `
		SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.flash_sale_id = ANY($1)
		  AND fsi.status = 'APPROVED'`
	if excludeSuspended {
		itemsQ += `
		  AND NOT EXISTS (
		      SELECT 1 FROM shop_suspension_state sss
		      WHERE sss.shop_id = fsi.shop_id AND sss.suspended = TRUE
		  )`
	}
	itemsQ += `
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

func (s *PostgresStore) queryFlashSaleItems(ctx context.Context, q string, args ...any) ([]domain.FlashSaleItem, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query flash sale items: %w", err)
	}
	defer rows.Close()

	var items []domain.FlashSaleItem
	for rows.Next() {
		item, scanErr := scanFlashSaleItem(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan flash sale item: %w", scanErr)
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListApprovedFlashSaleItems(ctx context.Context) ([]domain.FlashSaleItem, error) {
	const q = `
		SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.status = 'APPROVED'
	`
	return s.queryFlashSaleItems(ctx, q)
}

func (s *PostgresStore) ListActiveApprovedFlashSaleItems(ctx context.Context, now time.Time) ([]domain.FlashSaleItem, error) {
	const q = `
		SELECT ` + flashItemSelectColumns + `
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.status = 'APPROVED' AND fs.ends_at > $1
	`
	return s.queryFlashSaleItems(ctx, q, now.UTC())
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

func hasReservedStoreCoinRefPrefix(refID string) bool {
	lower := strings.ToLower(strings.TrimSpace(refID))
	return strings.HasPrefix(lower, "reserve:") || strings.HasPrefix(lower, "release:")
}

func (s *PostgresStore) CreditCoins(ctx context.Context, userID string, amount int64, reason, referenceID string) (int64, bool, error) {
	if hasReservedStoreCoinRefPrefix(referenceID) {
		return 0, false, fmt.Errorf("%w: %s", ErrReservedCoinReferenceID, referenceID)
	}
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

func (s *PostgresStore) ProcessShopStatusChangedEvent(
	ctx context.Context,
	eventID, subject, shopID string,
	suspended bool,
	version int64,
) (bool, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, false, fmt.Errorf("begin shop status tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Serialize concurrent updates for the same shop_id across replicas.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "shop_suspension:"+shopID); err != nil {
		return false, false, fmt.Errorf("lock shop suspension %s: %w", shopID, err)
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO processed_events (event_id, subject) VALUES ($1, $2) ON CONFLICT (event_id) DO NOTHING`,
		eventID, subject,
	)
	if err != nil {
		return false, false, fmt.Errorf("insert processed event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return true, false, nil
	}

	upTag, err := tx.Exec(ctx, `
		INSERT INTO shop_suspension_state (shop_id, suspended, version, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (shop_id) DO UPDATE
			SET suspended = EXCLUDED.suspended,
				version = EXCLUDED.version,
				updated_at = NOW()
			WHERE shop_suspension_state.version < EXCLUDED.version
	`, shopID, suspended, version)
	if err != nil {
		return false, false, fmt.Errorf("upsert shop_suspension_state: %w", err)
	}
	stale := upTag.RowsAffected() == 0

	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("commit shop status tx: %w", err)
	}
	return false, stale, nil
}

func (s *PostgresStore) IsShopSuspended(ctx context.Context, shopID string) (bool, error) {
	shopID = strings.TrimSpace(shopID)
	if shopID == "" {
		return false, nil
	}
	var suspended bool
	err := s.pool.QueryRow(ctx, `SELECT suspended FROM shop_suspension_state WHERE shop_id = $1`, shopID).Scan(&suspended)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query shop_suspension_state: %w", err)
	}
	return suspended, nil
}

func (s *PostgresStore) GetSuspendedShopIDs(ctx context.Context, shopIDs []string) (map[string]bool, error) {
	out := make(map[string]bool)
	if len(shopIDs) == 0 {
		return out, nil
	}
	seen := make(map[string]struct{}, len(shopIDs))
	uniq := make([]string, 0, len(shopIDs))
	for _, id := range shopIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT shop_id
		FROM shop_suspension_state
		WHERE shop_id = ANY($1::text[]) AND suspended = TRUE
	`, uniq)
	if err != nil {
		return nil, fmt.Errorf("batch query shop_suspension_state: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return nil, fmt.Errorf("scan suspended shop_id: %w", scanErr)
		}
		out[id] = true
	}
	return out, rows.Err()
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
		if coinTag.RowsAffected() == 0 {
			return nil, false, fmt.Errorf("%w: duplicate coin reserve reference %s", ErrCoinLedgerConflict, reserveRef)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE coin_accounts
			SET balance = balance - $2, updated_at = NOW()
			WHERE user_id = $1
		`, res.UserID, res.CoinsUsed); err != nil {
			return nil, false, fmt.Errorf("debit coin account: %w", err)
		}
	}

	// Authoritative Postgres final gate for vouchers (sorted by ID to match release lock order).
	usageChecks := make([]usageCheck, 0, len(res.Vouchers)+len(res.FlashSaleItems)+len(res.ShopProgramItems))
	sortedVouchers := append([]domain.ReservedVoucher(nil), res.Vouchers...)
	sort.Slice(sortedVouchers, func(i, j int) bool { return sortedVouchers[i].VoucherID < sortedVouchers[j].VoucherID })
	for _, v := range sortedVouchers {
		var perUserLimit int32
		err := tx.QueryRow(ctx, `
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
			  AND ($4 = 0 OR rules_version = $4)
			RETURNING per_user_limit
		`, v.VoucherID, v.DiscountVND, now, v.RulesVersion).Scan(&perUserLimit)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("%w: voucher %s", ErrVoucherNotEligibleForCommit, v.VoucherID)
		}
		if err != nil {
			return nil, false, fmt.Errorf("sync voucher usage: %w", err)
		}
		usageChecks = append(usageChecks, usageCheck{
			kind:     domain.UsageKindVoucher,
			targetID: v.VoucherID,
			qty:      1,
			limit:    perUserLimit,
		})
	}

	// Authoritative Postgres final gate for flash sale slots (sorted by ID; no GREATEST clamp).
	sortedFlash := append([]domain.ReservedFlashItem(nil), res.FlashSaleItems...)
	sort.Slice(sortedFlash, func(i, j int) bool { return sortedFlash[i].FlashSaleItemID < sortedFlash[j].FlashSaleItemID })
	for _, f := range sortedFlash {
		var perUserLimit int32
		err := tx.QueryRow(ctx, `
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
			RETURNING fsi.per_user_limit
		`, f.FlashSaleItemID, f.Quantity, now).Scan(&perUserLimit)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("%w: flash item %s", ErrFlashNotEligibleForCommit, f.FlashSaleItemID)
		}
		if err != nil {
			return nil, false, fmt.Errorf("sync flash remaining slots: %w", err)
		}
		usageChecks = append(usageChecks, usageCheck{
			kind:     domain.UsageKindFlash,
			targetID: f.FlashSaleItemID,
			qty:      f.Quantity,
			limit:    perUserLimit,
		})
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
			var perUserLimit int32
			err := tx.QueryRow(ctx, `
				SELECT sdi.id, sdi.per_user_limit
				FROM shop_discount_program_items sdi
				JOIN shop_discount_programs sdp ON sdp.id = sdi.program_id
				WHERE sdi.id = $1
				  AND ($2 = '' OR sdi.shop_id = $2)
				  AND ($3 = '' OR sdi.sku_id = $3)
				  AND sdp.status <> 'ENDED'
				  AND sdp.ended_at IS NULL
				  AND sdp.starts_at <= $4
				  AND sdp.ends_at > $4
			`, p.ShopDiscountProgramItemID, p.ShopID, p.SKUID, now).Scan(&verifiedID, &perUserLimit)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, false, fmt.Errorf("%w: shop discount program item %s", ErrShopProgramNotEligibleForCommit, p.ShopDiscountProgramItemID)
			}
			if err != nil {
				return nil, false, fmt.Errorf("verify shop discount program item: %w", err)
			}
			usageChecks = append(usageChecks, usageCheck{
				kind:     domain.UsageKindShopProgram,
				targetID: p.ShopDiscountProgramItemID,
				qty:      p.Quantity,
				limit:    perUserLimit,
			})
		}
	}

	// Authoritative Postgres per_user_limit gate across vouchers, flash items, and shop discount programs
	// (survives Redis loss/eviction and serializes concurrent checkouts by the same user via unique index lock).
	if res.UserID != "" && len(usageChecks) > 0 {
		if err := enforceUserUsageLimitsInTx(ctx, tx, res.UserID, usageChecks, now); err != nil {
			return nil, false, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit reservation tx: %w", err)
	}
	res.Status = domain.ReservationStatusReserved
	return &res, false, nil
}

func (s *PostgresStore) ReleaseReservation(ctx context.Context, orderID string) (*FullReleaseResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin release reservation tx: %w", err)
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
			return nil, fmt.Errorf("insert release tombstone: %w", insErr)
		}
		if tag.RowsAffected() > 0 {
			if err := tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("commit release tombstone: %w", err)
			}
			return &FullReleaseResult{AlreadyReleased: true}, nil
		}
		// A concurrent CommitReservation just inserted the row — lock and release it now.
		existing, err = scanReservation(tx.QueryRow(ctx, selectForUpdateQ, orderID))
		if err != nil {
			return nil, fmt.Errorf("re-lock reservation after tombstone conflict: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("lock reservation for release: %w", err)
	}

	result := &FullReleaseResult{
		UserID:         existing.UserID,
		Vouchers:       existing.Vouchers,
		FlashSaleItems: existing.FlashSaleItems,
		ShopPrograms:   existing.ShopProgramItems,
	}

	plan, err := planRemainingRelease(existing, lookupFlashShopInTx(ctx, tx), lookupShopProgShopInTx(ctx, tx))
	if err != nil {
		return nil, fmt.Errorf("plan remaining release: %w", err)
	}
	if plan.alreadyReleased {
		result.AlreadyReleased = true
		return result, nil
	}

	if plan.coinsToReturn > 0 {
		releaseRef := "release:" + orderID
		_, alreadyCredited, err := creditCoinsInTx(ctx, tx, existing.UserID, plan.coinsToReturn, "order_release", releaseRef)
		if err != nil {
			return nil, fmt.Errorf("restore reserved coins: %w", err)
		}
		if !alreadyCredited {
			result.CoinsReturned = plan.coinsToReturn
		}
	}

	if err := restoreVouchersAndFlashInTx(ctx, tx, plan.vouchersToRelease, plan.flashToRelease); err != nil {
		return nil, err
	}
	if err := restoreUserUsageInTx(ctx, tx, existing.UserID, plan.vouchersToRelease, plan.flashToRelease, plan.shopProgramsToRelease); err != nil {
		return nil, err
	}

	releasedShopsJSON, err := json.Marshal(plan.newReleasedShops)
	if err != nil {
		return nil, fmt.Errorf("marshal released shops: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE promotion_reservations
		SET status = 'RELEASED',
			released_shops_json = $2::jsonb,
			released_at = NOW(),
			updated_at = NOW()
		WHERE order_id = $1
	`, orderID, string(releasedShopsJSON)); err != nil {
		return nil, fmt.Errorf("mark reservation released: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit release reservation tx: %w", err)
	}
	return result, nil
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
		_, alreadyCredited, err := creditCoinsInTx(ctx, tx, existing.UserID, plan.coinsToReturn, "order_release", releaseRef)
		if err != nil {
			return nil, fmt.Errorf("restore shop reserved coins: %w", err)
		}
		if alreadyCredited {
			result.CoinsReturned = 0
		}
	}

	if err := restoreVouchersAndFlashInTx(ctx, tx, plan.vouchersToRelease, plan.flashToRelease); err != nil {
		return nil, err
	}
	if err := restoreUserUsageInTx(ctx, tx, existing.UserID, plan.vouchersToRelease, plan.flashToRelease, plan.shopProgramsToRelease); err != nil {
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

type usageCheck struct {
	kind     string
	targetID string
	qty      int32
	limit    int32
}

func mergeUsageChecks(in []usageCheck) []usageCheck {
	byKey := make(map[string]usageCheck, len(in))
	for _, u := range in {
		if u.targetID == "" || u.qty <= 0 {
			continue
		}
		k := u.kind + "|" + u.targetID
		if existing, ok := byKey[k]; ok {
			existing.qty += u.qty
			if u.limit > 0 && (existing.limit <= 0 || u.limit < existing.limit) {
				existing.limit = u.limit
			}
			byKey[k] = existing
		} else {
			byKey[k] = u
		}
	}
	out := make([]usageCheck, 0, len(byKey))
	for _, u := range byKey {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].kind != out[j].kind {
			return out[i].kind < out[j].kind
		}
		return out[i].targetID < out[j].targetID
	})
	return out
}

func userLimitError(kind, targetID string) error {
	switch kind {
	case domain.UsageKindVoucher:
		return fmt.Errorf("%w: voucher %s", ErrVoucherUserLimitExceeded, targetID)
	case domain.UsageKindFlash:
		return fmt.Errorf("%w: flash item %s", ErrFlashUserLimitExceeded, targetID)
	case domain.UsageKindShopProgram:
		return fmt.Errorf("%w: shop discount program item %s", ErrShopProgramUserLimitExceeded, targetID)
	default:
		return fmt.Errorf("per-user limit exceeded for %s %s", kind, targetID)
	}
}

func enforceUserUsageLimitsInTx(ctx context.Context, tx pgx.Tx, userID string, checks []usageCheck, now time.Time) error {
	merged := mergeUsageChecks(checks)
	for _, uc := range merged {
		if uc.limit > 0 && uc.qty > uc.limit {
			return userLimitError(uc.kind, uc.targetID)
		}
		var newUsed int32
		err := tx.QueryRow(ctx, `
			INSERT INTO promotion_user_usage (kind, target_id, user_id, used_qty, updated_at)
			SELECT $1, $2, $3, $4, $5
			WHERE ($6 <= 0 OR $4 <= $6)
			ON CONFLICT (kind, target_id, user_id) DO UPDATE
				SET used_qty = promotion_user_usage.used_qty + EXCLUDED.used_qty,
					updated_at = EXCLUDED.updated_at
				WHERE ($6 <= 0 OR promotion_user_usage.used_qty + EXCLUDED.used_qty <= $6)
			RETURNING used_qty
		`, uc.kind, uc.targetID, userID, uc.qty, now, uc.limit).Scan(&newUsed)
		if errors.Is(err, pgx.ErrNoRows) {
			return userLimitError(uc.kind, uc.targetID)
		}
		if err != nil {
			return fmt.Errorf("enforce per-user usage limit (%s %s): %w", uc.kind, uc.targetID, err)
		}
	}
	return nil
}

func restoreUserUsageInTx(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	vouchers []domain.ReservedVoucher,
	flashItems []domain.ReservedFlashItem,
	shopProgs []domain.ReservedShopProgramItem,
) error {
	if userID == "" {
		return nil
	}
	checks := make([]usageCheck, 0, len(vouchers)+len(flashItems)+len(shopProgs))
	for _, v := range vouchers {
		checks = append(checks, usageCheck{kind: domain.UsageKindVoucher, targetID: v.VoucherID, qty: 1})
	}
	for _, f := range flashItems {
		checks = append(checks, usageCheck{kind: domain.UsageKindFlash, targetID: f.FlashSaleItemID, qty: f.Quantity})
	}
	for _, p := range shopProgs {
		checks = append(checks, usageCheck{kind: domain.UsageKindShopProgram, targetID: p.ShopDiscountProgramItemID, qty: p.Quantity})
	}
	for _, uc := range mergeUsageChecks(checks) {
		if _, err := tx.Exec(ctx, `
			UPDATE promotion_user_usage
			SET used_qty = GREATEST(used_qty - $4, 0),
				updated_at = NOW()
			WHERE kind = $1 AND target_id = $2 AND user_id = $3
		`, uc.kind, uc.targetID, userID, uc.qty); err != nil {
			return fmt.Errorf("restore per-user usage (%s %s): %w", uc.kind, uc.targetID, err)
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
		&v.RulesVersion, &v.RedisSyncVersion, &v.RedisSyncedVersion,
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
