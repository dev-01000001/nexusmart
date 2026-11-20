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
	ErrNotFound                   = errors.New("not found")
	ErrDuplicateCode              = errors.New("voucher code already exists")
	ErrOverlappingFlashSale       = errors.New("sku already registered in overlapping flash sale window")
	ErrFlashSaleEnded             = errors.New("flash sale has already ended")
	ErrInsufficientCoins          = errors.New("insufficient coin balance")
	ErrReservationAlreadyReleased = errors.New("promotion reservation already released")
)

// Store defines the persistence contract for the promotion service.
type Store interface {
	Migrate(ctx context.Context) error
	Ping(ctx context.Context) error

	// Vouchers
	CreateVoucher(ctx context.Context, v domain.Voucher) (*domain.Voucher, error)
	GetVoucherByIDOrCode(ctx context.Context, idOrCode string) (*domain.Voucher, error)
	ListVouchers(ctx context.Context, shopID, scope string) ([]domain.Voucher, error)
	ListAllActiveVouchers(ctx context.Context) ([]domain.Voucher, error)
	SaveUserVoucher(ctx context.Context, userID, voucherID string) (alreadySaved bool, err error)
	GetUserSavedVoucherIDs(ctx context.Context, userID string) (map[string]bool, error)

	// Flash sales
	CreateFlashSale(ctx context.Context, fs domain.FlashSale) (*domain.FlashSale, error)
	GetFlashSale(ctx context.Context, id string) (*domain.FlashSale, error)
	RegisterFlashSaleItem(ctx context.Context, item domain.FlashSaleItem, now time.Time) (*domain.FlashSaleItem, error)
	ApproveFlashSaleItem(ctx context.Context, itemID, adminID string, approve bool, reason string) (item *domain.FlashSaleItem, prevStatus string, err error)
	GetFlashSaleItem(ctx context.Context, itemID string) (*domain.FlashSaleItem, error)
	ListFlashSales(ctx context.Context, now time.Time, includeUpcoming bool) ([]domain.FlashSale, error)
	ListApprovedFlashSaleItems(ctx context.Context) ([]domain.FlashSaleItem, error)
	EndFlashSaleWindow(ctx context.Context, windowID string, now time.Time) (fs *domain.FlashSale, unreleasedApproved []domain.FlashSaleItem, err error)
	MarkFlashSaleItemStockReleased(ctx context.Context, itemID string, releasedAt time.Time) error
	ListExpiredUnreleasedFlashSaleItems(ctx context.Context, now time.Time) ([]domain.FlashSaleItem, error)

	// Coins & events
	GetCoinBalance(ctx context.Context, userID string, recentLimit int) (int64, []domain.CoinTransaction, error)
	CreditCoins(ctx context.Context, userID string, amount int64, reason, referenceID string) (balance int64, alreadyCredited bool, err error)
	ProcessOrderCompletedEvent(ctx context.Context, eventID, subject, orderID, buyerID string, coinsReward int64) (alreadyProcessed bool, err error)

	// Reservations
	GetReservation(ctx context.Context, orderID string) (*domain.PromotionReservation, error)
	CommitReservation(ctx context.Context, res domain.PromotionReservation) (existing *domain.PromotionReservation, alreadyReserved bool, err error)
	ReleaseReservation(ctx context.Context, orderID string) (alreadyReleased bool, err error)
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

	const q = `
		INSERT INTO vouchers (
			id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
			max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
			budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, created_by,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, 0, $12,
			$13, 0, $14, $15, $16, $17,
			$18, $19
		)
		RETURNING id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
			max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
			budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, created_by,
			created_at, updated_at
	`

	out, err := scanVoucher(s.pool.QueryRow(ctx, q,
		v.ID, v.Code, v.Name, v.Scope, v.ShopID, v.DiscountType, v.ValueVND, v.DiscountPercent,
		v.MaxDiscountVND, v.MinOrderVND, v.Quota, v.PerUserLimit,
		v.BudgetVND, v.StartsAt.UTC(), v.EndsAt.UTC(), v.IsActive, v.CreatedBy,
		v.CreatedAt, v.UpdatedAt,
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

func (s *PostgresStore) GetVoucherByIDOrCode(ctx context.Context, idOrCode string) (*domain.Voucher, error) {
	const q = `
		SELECT id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
			max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
			budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, created_by,
			created_at, updated_at
		FROM vouchers
		WHERE id = $1 OR code = $1
		LIMIT 1
	`
	v, err := scanVoucher(s.pool.QueryRow(ctx, q, idOrCode))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get voucher: %w", err)
	}
	return v, nil
}

func (s *PostgresStore) ListVouchers(ctx context.Context, shopID, scope string) ([]domain.Voucher, error) {
	const q = `
		SELECT id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
			max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
			budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, created_by,
			created_at, updated_at
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

func (s *PostgresStore) ListAllActiveVouchers(ctx context.Context) ([]domain.Voucher, error) {
	const q = `
		SELECT id, code, name, scope, shop_id, discount_type, value_vnd, discount_percent,
			max_discount_vnd, min_order_vnd, quota, used_count, per_user_limit,
			budget_vnd, used_budget_vnd, starts_at, ends_at, is_active, created_by,
			created_at, updated_at
		FROM vouchers
		WHERE is_active = TRUE
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list active vouchers: %w", err)
	}
	defer rows.Close()

	var result []domain.Voucher
	for rows.Next() {
		v, scanErr := scanVoucher(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan active voucher: %w", scanErr)
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

func (s *PostgresStore) CreateFlashSale(ctx context.Context, fs domain.FlashSale) (*domain.FlashSale, error) {
	if fs.ID == "" {
		fs.ID = uuid.NewString()
	}
	if fs.Status == "" {
		fs.Status = domain.FlashWindowStatusActive
	}
	now := time.Now().UTC()
	fs.CreatedAt = now
	fs.UpdatedAt = now

	const q = `
		INSERT INTO flash_sales (id, name, starts_at, ends_at, status, created_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, name, starts_at, ends_at, status, created_by, created_at, updated_at
	`
	var out domain.FlashSale
	err := s.pool.QueryRow(ctx, q,
		fs.ID, fs.Name, fs.StartsAt.UTC(), fs.EndsAt.UTC(), fs.Status, fs.CreatedBy, fs.CreatedAt, fs.UpdatedAt,
	).Scan(&out.ID, &out.Name, &out.StartsAt, &out.EndsAt, &out.Status, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert flash sale: %w", err)
	}
	return &out, nil
}

func (s *PostgresStore) GetFlashSale(ctx context.Context, id string) (*domain.FlashSale, error) {
	const q = `
		SELECT id, name, starts_at, ends_at, status, created_by, created_at, updated_at
		FROM flash_sales
		WHERE id = $1
	`
	var out domain.FlashSale
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&out.ID, &out.Name, &out.StartsAt, &out.EndsAt, &out.Status, &out.CreatedBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get flash sale: %w", err)
	}
	return &out, nil
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
	var windowStatus string
	err = tx.QueryRow(ctx, `SELECT starts_at, ends_at, status FROM flash_sales WHERE id = $1`, item.FlashSaleID).Scan(&startsAt, &endsAt, &windowStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query flash sale window: %w", err)
	}
	if windowStatus == domain.FlashWindowStatusEnded || !endsAt.After(now) {
		return nil, ErrFlashSaleEnded
	}

	var overlapCount int
	const overlapQ = `
		SELECT COUNT(1)
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.sku_id = $1
		  AND fsi.status IN ('PENDING', 'APPROVED')
		  AND fs.status <> 'ENDED'
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
			id, flash_sale_id, shop_id, sku_id, flash_price_vnd,
			total_slots, remaining_slots, per_user_limit, status, rejection_reason,
			approved_by, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $6, $7, 'PENDING', '',
			'', $8, $8
		)
		RETURNING id, flash_sale_id, shop_id, sku_id, flash_price_vnd,
			total_slots, remaining_slots, per_user_limit, status, rejection_reason,
			approved_by, created_at, updated_at
	`
	var out domain.FlashSaleItem
	err = tx.QueryRow(ctx, insertQ,
		item.ID, item.FlashSaleID, item.ShopID, item.SKUID, item.FlashPriceVND,
		item.TotalSlots, item.PerUserLimit, now.UTC(),
	).Scan(
		&out.ID, &out.FlashSaleID, &out.ShopID, &out.SKUID, &out.FlashPriceVND,
		&out.TotalSlots, &out.RemainingSlots, &out.PerUserLimit, &out.Status, &out.RejectionReason,
		&out.ApprovedBy, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert flash sale item: %w", err)
	}
	out.WindowStatus = windowStatus
	out.StartsAt = startsAt
	out.EndsAt = endsAt

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit register flash item: %w", err)
	}
	return &out, nil
}

func (s *PostgresStore) ApproveFlashSaleItem(ctx context.Context, itemID, adminID string, approve bool, reason string) (*domain.FlashSaleItem, string, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, "", fmt.Errorf("begin approve flash item tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var prevStatus, windowStatus string
	err = tx.QueryRow(ctx, `
		SELECT fsi.status, fs.status
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.id = $1
		FOR UPDATE OF fsi, fs
	`, itemID).Scan(&prevStatus, &windowStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("lock flash sale item: %w", err)
	}
	if approve && windowStatus == domain.FlashWindowStatusEnded {
		return nil, "", ErrFlashSaleEnded
	}

	newStatus := domain.FlashStatusRejected
	rejReason := reason
	if approve {
		newStatus = domain.FlashStatusApproved
		rejReason = ""
	}

	const updateQ = `
		UPDATE flash_sale_items fsi
		SET status = $2,
			rejection_reason = $3,
			approved_by = $4,
			stock_released_at = CASE WHEN $2 = 'APPROVED' THEN NULL ELSE fsi.stock_released_at END,
			updated_at = NOW()
		FROM flash_sales fs
		WHERE fsi.id = $1 AND fs.id = fsi.flash_sale_id
		RETURNING fsi.id, fsi.flash_sale_id, fsi.shop_id, fsi.sku_id, fsi.flash_price_vnd,
			fsi.total_slots, fsi.remaining_slots, fsi.per_user_limit, fsi.status, fsi.rejection_reason,
			fsi.approved_by, fs.status, fsi.stock_released_at, fs.starts_at, fs.ends_at, fsi.created_at, fsi.updated_at
	`
	var out domain.FlashSaleItem
	err = tx.QueryRow(ctx, updateQ, itemID, newStatus, rejReason, adminID).Scan(
		&out.ID, &out.FlashSaleID, &out.ShopID, &out.SKUID, &out.FlashPriceVND,
		&out.TotalSlots, &out.RemainingSlots, &out.PerUserLimit, &out.Status, &out.RejectionReason,
		&out.ApprovedBy, &out.WindowStatus, &out.StockReleasedAt, &out.StartsAt, &out.EndsAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, "", fmt.Errorf("update flash sale item approval: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit approve flash item: %w", err)
	}
	return &out, prevStatus, nil
}

func (s *PostgresStore) GetFlashSaleItem(ctx context.Context, itemID string) (*domain.FlashSaleItem, error) {
	const q = `
		SELECT fsi.id, fsi.flash_sale_id, fsi.shop_id, fsi.sku_id, fsi.flash_price_vnd,
			fsi.total_slots, fsi.remaining_slots, fsi.per_user_limit, fsi.status, fsi.rejection_reason,
			fsi.approved_by, fs.status, fsi.stock_released_at, fs.starts_at, fs.ends_at, fsi.created_at, fsi.updated_at
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.id = $1
	`
	var out domain.FlashSaleItem
	err := s.pool.QueryRow(ctx, q, itemID).Scan(
		&out.ID, &out.FlashSaleID, &out.ShopID, &out.SKUID, &out.FlashPriceVND,
		&out.TotalSlots, &out.RemainingSlots, &out.PerUserLimit, &out.Status, &out.RejectionReason,
		&out.ApprovedBy, &out.WindowStatus, &out.StockReleasedAt, &out.StartsAt, &out.EndsAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get flash sale item: %w", err)
	}
	return &out, nil
}

func (s *PostgresStore) ListFlashSales(ctx context.Context, now time.Time, includeUpcoming bool) ([]domain.FlashSale, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	var salesQ string
	if includeUpcoming {
		salesQ = `
			SELECT id, name, starts_at, ends_at, status, created_by, created_at, updated_at
			FROM flash_sales
			WHERE status <> 'ENDED' AND ends_at > $1
			ORDER BY starts_at ASC, id ASC
		`
	} else {
		salesQ = `
			SELECT id, name, starts_at, ends_at, status, created_by, created_at, updated_at
			FROM flash_sales
			WHERE status <> 'ENDED' AND starts_at <= $1 AND ends_at > $1
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
		var fs domain.FlashSale
		if scanErr := rows.Scan(&fs.ID, &fs.Name, &fs.StartsAt, &fs.EndsAt, &fs.Status, &fs.CreatedBy, &fs.CreatedAt, &fs.UpdatedAt); scanErr != nil {
			return nil, fmt.Errorf("scan flash sale: %w", scanErr)
		}
		saleIdx[fs.ID] = len(sales)
		saleIDs = append(saleIDs, fs.ID)
		sales = append(sales, fs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flash sales: %w", err)
	}
	if len(saleIDs) == 0 {
		return sales, nil
	}

	const itemsQ = `
		SELECT fsi.id, fsi.flash_sale_id, fsi.shop_id, fsi.sku_id, fsi.flash_price_vnd,
			fsi.total_slots, fsi.remaining_slots, fsi.per_user_limit, fsi.status, fsi.rejection_reason,
			fsi.approved_by, fs.status, fsi.stock_released_at, fs.starts_at, fs.ends_at, fsi.created_at, fsi.updated_at
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.flash_sale_id = ANY($1)
		  AND fsi.status = 'APPROVED'
		ORDER BY fsi.created_at ASC, fsi.id ASC
	`
	itemRows, err := s.pool.Query(ctx, itemsQ, saleIDs)
	if err != nil {
		return nil, fmt.Errorf("list flash sale items: %w", err)
	}
	defer itemRows.Close()

	for itemRows.Next() {
		var item domain.FlashSaleItem
		if scanErr := itemRows.Scan(
			&item.ID, &item.FlashSaleID, &item.ShopID, &item.SKUID, &item.FlashPriceVND,
			&item.TotalSlots, &item.RemainingSlots, &item.PerUserLimit, &item.Status, &item.RejectionReason,
			&item.ApprovedBy, &item.WindowStatus, &item.StockReleasedAt, &item.StartsAt, &item.EndsAt, &item.CreatedAt, &item.UpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan flash sale item: %w", scanErr)
		}
		if idx, ok := saleIdx[item.FlashSaleID]; ok {
			sales[idx].Items = append(sales[idx].Items, item)
		}
	}
	if err := itemRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flash sale items: %w", err)
	}
	return sales, nil
}

func (s *PostgresStore) ListApprovedFlashSaleItems(ctx context.Context) ([]domain.FlashSaleItem, error) {
	const q = `
		SELECT fsi.id, fsi.flash_sale_id, fsi.shop_id, fsi.sku_id, fsi.flash_price_vnd,
			fsi.total_slots, fsi.remaining_slots, fsi.per_user_limit, fsi.status, fsi.rejection_reason,
			fsi.approved_by, fs.status, fsi.stock_released_at, fs.starts_at, fs.ends_at, fsi.created_at, fsi.updated_at
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.status = 'APPROVED'
		  AND fs.status <> 'ENDED'
		  AND fs.ends_at > NOW()
	`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list approved flash items: %w", err)
	}
	defer rows.Close()

	var items []domain.FlashSaleItem
	for rows.Next() {
		var item domain.FlashSaleItem
		if scanErr := rows.Scan(
			&item.ID, &item.FlashSaleID, &item.ShopID, &item.SKUID, &item.FlashPriceVND,
			&item.TotalSlots, &item.RemainingSlots, &item.PerUserLimit, &item.Status, &item.RejectionReason,
			&item.ApprovedBy, &item.WindowStatus, &item.StockReleasedAt, &item.StartsAt, &item.EndsAt, &item.CreatedAt, &item.UpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan approved flash item: %w", scanErr)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) EndFlashSaleWindow(ctx context.Context, windowID string, now time.Time) (*domain.FlashSale, []domain.FlashSaleItem, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("begin end flash sale window tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var fs domain.FlashSale
	err = tx.QueryRow(ctx, `
		UPDATE flash_sales
		SET status = 'ENDED',
			ends_at = CASE WHEN $2 > starts_at THEN LEAST(ends_at, $2) ELSE ends_at END,
			updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, starts_at, ends_at, status, created_by, created_at, updated_at
	`, windowID, now.UTC()).Scan(
		&fs.ID, &fs.Name, &fs.StartsAt, &fs.EndsAt, &fs.Status, &fs.CreatedBy, &fs.CreatedAt, &fs.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("end flash sale window: %w", err)
	}

	rows, err := tx.Query(ctx, `
		SELECT fsi.id, fsi.flash_sale_id, fsi.shop_id, fsi.sku_id, fsi.flash_price_vnd,
			fsi.total_slots, fsi.remaining_slots, fsi.per_user_limit, fsi.status, fsi.rejection_reason,
			fsi.approved_by, fs.status, fsi.stock_released_at, fs.starts_at, fs.ends_at, fsi.created_at, fsi.updated_at
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.flash_sale_id = $1
		  AND fsi.status = 'APPROVED'
		ORDER BY fsi.created_at ASC, fsi.id ASC
	`, windowID)
	if err != nil {
		return nil, nil, fmt.Errorf("list window items for end: %w", err)
	}
	defer rows.Close()

	var unreleased []domain.FlashSaleItem
	for rows.Next() {
		var item domain.FlashSaleItem
		if scanErr := rows.Scan(
			&item.ID, &item.FlashSaleID, &item.ShopID, &item.SKUID, &item.FlashPriceVND,
			&item.TotalSlots, &item.RemainingSlots, &item.PerUserLimit, &item.Status, &item.RejectionReason,
			&item.ApprovedBy, &item.WindowStatus, &item.StockReleasedAt, &item.StartsAt, &item.EndsAt, &item.CreatedAt, &item.UpdatedAt,
		); scanErr != nil {
			return nil, nil, fmt.Errorf("scan window item: %w", scanErr)
		}
		fs.Items = append(fs.Items, item)
		if item.StockReleasedAt == nil {
			unreleased = append(unreleased, item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate window items: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("commit end flash sale window: %w", err)
	}
	return &fs, unreleased, nil
}

func (s *PostgresStore) MarkFlashSaleItemStockReleased(ctx context.Context, itemID string, releasedAt time.Time) error {
	if releasedAt.IsZero() {
		releasedAt = time.Now().UTC()
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE flash_sale_items
		SET stock_released_at = COALESCE(stock_released_at, $2),
			updated_at = NOW()
		WHERE id = $1
	`, itemID, releasedAt.UTC())
	if err != nil {
		return fmt.Errorf("mark flash sale item stock released: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListExpiredUnreleasedFlashSaleItems(ctx context.Context, now time.Time) ([]domain.FlashSaleItem, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE flash_sales
		SET status = 'ENDED', updated_at = NOW()
		WHERE status = 'ACTIVE' AND ends_at <= $1
	`, now.UTC()); err != nil {
		return nil, fmt.Errorf("mark expired flash sales ended: %w", err)
	}

	const q = `
		SELECT fsi.id, fsi.flash_sale_id, fsi.shop_id, fsi.sku_id, fsi.flash_price_vnd,
			fsi.total_slots, fsi.remaining_slots, fsi.per_user_limit, fsi.status, fsi.rejection_reason,
			fsi.approved_by, fs.status, fsi.stock_released_at, fs.starts_at, fs.ends_at, fsi.created_at, fsi.updated_at
		FROM flash_sale_items fsi
		JOIN flash_sales fs ON fs.id = fsi.flash_sale_id
		WHERE fsi.status = 'APPROVED'
		  AND fsi.stock_released_at IS NULL
		  AND (fs.status = 'ENDED' OR fs.ends_at <= $1)
		ORDER BY fs.ends_at ASC, fsi.id ASC
		LIMIT 100
	`
	rows, err := s.pool.Query(ctx, q, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("list expired unreleased flash sale items: %w", err)
	}
	defer rows.Close()

	var items []domain.FlashSaleItem
	for rows.Next() {
		var item domain.FlashSaleItem
		if scanErr := rows.Scan(
			&item.ID, &item.FlashSaleID, &item.ShopID, &item.SKUID, &item.FlashPriceVND,
			&item.TotalSlots, &item.RemainingSlots, &item.PerUserLimit, &item.Status, &item.RejectionReason,
			&item.ApprovedBy, &item.WindowStatus, &item.StockReleasedAt, &item.StartsAt, &item.EndsAt, &item.CreatedAt, &item.UpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan expired flash item: %w", scanErr)
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
			breakdown_json, created_at, released_at, updated_at
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

func (s *PostgresStore) CommitReservation(ctx context.Context, res domain.PromotionReservation) (*domain.PromotionReservation, bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, fmt.Errorf("begin commit reservation tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock existing reservation row if present
	existing, err := scanReservation(tx.QueryRow(ctx, `
		SELECT order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json,
			breakdown_json, created_at, released_at, updated_at
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

	// Deduct coins atomically inside the same Postgres transaction
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
		tag, err := tx.Exec(ctx, `
			INSERT INTO coin_transactions (id, user_id, delta, reason, reference_id)
			VALUES ($1, $2, $3, 'order_reserve', $4)
			ON CONFLICT (reference_id) DO NOTHING
		`, uuid.NewString(), res.UserID, -res.CoinsUsed, reserveRef)
		if err != nil {
			return nil, false, fmt.Errorf("insert coin reserve tx: %w", err)
		}
		if tag.RowsAffected() > 0 {
			if _, err := tx.Exec(ctx, `
				UPDATE coin_accounts
				SET balance = balance - $2, updated_at = NOW()
				WHERE user_id = $1
			`, res.UserID, res.CoinsUsed); err != nil {
				return nil, false, fmt.Errorf("debit coin account: %w", err)
			}
		}
	}

	vouchersJSON, err := json.Marshal(res.Vouchers)
	if err != nil {
		return nil, false, fmt.Errorf("marshal reserved vouchers: %w", err)
	}
	flashJSON, err := json.Marshal(res.FlashSaleItems)
	if err != nil {
		return nil, false, fmt.Errorf("marshal reserved flash items: %w", err)
	}
	breakdownJSON := res.BreakdownJSON
	if len(breakdownJSON) == 0 {
		breakdownJSON = []byte("{}")
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO promotion_reservations (
			order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json, breakdown_json
		) VALUES ($1, $2, 'RESERVED', $3, $4::jsonb, $5::jsonb, $6::jsonb)
		ON CONFLICT (order_id) DO NOTHING
	`, res.OrderID, res.UserID, res.CoinsUsed, string(vouchersJSON), string(flashJSON), string(breakdownJSON))
	if err != nil {
		return nil, false, fmt.Errorf("insert promotion reservation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Concurrent insert beat us; roll back our coin debit and return the committed row.
		_ = tx.Rollback(ctx)
		saved, getErr := s.GetReservation(ctx, res.OrderID)
		if getErr != nil {
			return nil, false, getErr
		}
		return saved, true, nil
	}

	// Keep Postgres counters synced in deterministic ID order to avoid deadlocks
	sortedVouchers := append([]domain.ReservedVoucher(nil), res.Vouchers...)
	sort.Slice(sortedVouchers, func(i, j int) bool { return sortedVouchers[i].VoucherID < sortedVouchers[j].VoucherID })
	for _, v := range sortedVouchers {
		if _, err := tx.Exec(ctx, `
			UPDATE vouchers
			SET used_count = used_count + 1,
				used_budget_vnd = used_budget_vnd + $2,
				updated_at = NOW()
			WHERE id = $1
		`, v.VoucherID, v.DiscountVND); err != nil {
			return nil, false, fmt.Errorf("sync voucher usage: %w", err)
		}
	}

	sortedFlash := append([]domain.ReservedFlashItem(nil), res.FlashSaleItems...)
	sort.Slice(sortedFlash, func(i, j int) bool { return sortedFlash[i].FlashSaleItemID < sortedFlash[j].FlashSaleItemID })
	for _, f := range sortedFlash {
		if _, err := tx.Exec(ctx, `
			UPDATE flash_sale_items
			SET remaining_slots = GREATEST(remaining_slots - $2, 0),
				updated_at = NOW()
			WHERE id = $1
		`, f.FlashSaleItemID, f.Quantity); err != nil {
			return nil, false, fmt.Errorf("sync flash remaining slots: %w", err)
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

	existing, err := scanReservation(tx.QueryRow(ctx, `
		SELECT order_id, user_id, status, coins_used, vouchers_json, flash_sale_items_json,
			breakdown_json, created_at, released_at, updated_at
		FROM promotion_reservations
		WHERE order_id = $1
		FOR UPDATE
	`, orderID))
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock reservation for release: %w", err)
	}
	if existing.Status == domain.ReservationStatusReleased {
		return true, nil
	}

	if existing.CoinsUsed > 0 {
		releaseRef := "release:" + orderID
		if _, _, err := creditCoinsInTx(ctx, tx, existing.UserID, existing.CoinsUsed, "order_release", releaseRef); err != nil {
			return false, fmt.Errorf("restore reserved coins: %w", err)
		}
	}

	sortedVouchers := append([]domain.ReservedVoucher(nil), existing.Vouchers...)
	sort.Slice(sortedVouchers, func(i, j int) bool { return sortedVouchers[i].VoucherID < sortedVouchers[j].VoucherID })
	for _, v := range sortedVouchers {
		if _, err := tx.Exec(ctx, `
			UPDATE vouchers
			SET used_count = GREATEST(used_count - 1, 0),
				used_budget_vnd = GREATEST(used_budget_vnd - $2, 0),
				updated_at = NOW()
			WHERE id = $1
		`, v.VoucherID, v.DiscountVND); err != nil {
			return false, fmt.Errorf("restore voucher usage: %w", err)
		}
	}

	sortedFlash := append([]domain.ReservedFlashItem(nil), existing.FlashSaleItems...)
	sort.Slice(sortedFlash, func(i, j int) bool { return sortedFlash[i].FlashSaleItemID < sortedFlash[j].FlashSaleItemID })
	for _, f := range sortedFlash {
		if _, err := tx.Exec(ctx, `
			UPDATE flash_sale_items
			SET remaining_slots = LEAST(remaining_slots + $2, total_slots),
				updated_at = NOW()
			WHERE id = $1
		`, f.FlashSaleItemID, f.Quantity); err != nil {
			return false, fmt.Errorf("restore flash remaining slots: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE promotion_reservations
		SET status = 'RELEASED', released_at = NOW(), updated_at = NOW()
		WHERE order_id = $1
	`, orderID); err != nil {
		return false, fmt.Errorf("mark reservation released: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit release reservation tx: %w", err)
	}
	return false, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVoucher(row rowScanner) (*domain.Voucher, error) {
	var v domain.Voucher
	err := row.Scan(
		&v.ID, &v.Code, &v.Name, &v.Scope, &v.ShopID, &v.DiscountType, &v.ValueVND, &v.DiscountPercent,
		&v.MaxDiscountVND, &v.MinOrderVND, &v.Quota, &v.UsedCount, &v.PerUserLimit,
		&v.BudgetVND, &v.UsedBudgetVND, &v.StartsAt, &v.EndsAt, &v.IsActive, &v.CreatedBy,
		&v.CreatedAt, &v.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func scanReservation(row rowScanner) (*domain.PromotionReservation, error) {
	var res domain.PromotionReservation
	var vouchersRaw, flashRaw, breakdownRaw []byte
	err := row.Scan(
		&res.OrderID, &res.UserID, &res.Status, &res.CoinsUsed,
		&vouchersRaw, &flashRaw, &breakdownRaw,
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
	res.BreakdownJSON = breakdownRaw
	return &res, nil
}
