package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/nexusmart/core/services/promotion/internal/domain"
)

var shopDiscountItemNamespace = uuid.MustParse("7c8d3b20-5f9a-4b4e-8a6d-2e5f0d1b9c3a")

// stableShopDiscountItemID derives a deterministic UUID v5 per (program_id, sku_id)
// so removing and re-adding a SKU inside the same program preserves per_user_limit counters.
func stableShopDiscountItemID(programID, skuID string) string {
	return uuid.NewSHA1(shopDiscountItemNamespace, []byte(programID+":"+skuID)).String()
}

func (s *PostgresStore) CreateShopDiscountProgram(
	ctx context.Context,
	prog domain.ShopDiscountProgram,
	now time.Time,
) (*domain.ShopDiscountProgram, error) {
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

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin create shop discount program tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Acquire deterministic per-SKU advisory locks in sorted order to prevent deadlocks & overlap races.
	if err := lockShopDiscountSKUsInTx(ctx, tx, collectProgramSKUs(prog.Items)); err != nil {
		return nil, err
	}

	if err := checkShopProgramOverlapInTx(ctx, tx, "", collectProgramSKUs(prog.Items), prog.StartsAt, prog.EndsAt, now); err != nil {
		return nil, err
	}

	const insertProgQ = `
		INSERT INTO shop_discount_programs (
			id, shop_id, name, starts_at, ends_at, status, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, shop_id, name, starts_at, ends_at, status, ended_at, created_by, created_at, updated_at
	`
	out, err := scanShopDiscountProgram(tx.QueryRow(ctx, insertProgQ,
		prog.ID, prog.ShopID, prog.Name, prog.StartsAt, prog.EndsAt, prog.Status,
		prog.CreatedBy, prog.CreatedAt, prog.UpdatedAt,
	))
	if err != nil {
		return nil, fmt.Errorf("insert shop discount program: %w", err)
	}

	out.Items = make([]domain.ShopDiscountProgramItem, 0, len(prog.Items))
	for _, it := range prog.Items {
		if it.ID == "" {
			it.ID = stableShopDiscountItemID(out.ID, it.SKUID)
		}
		const insertItemQ = `
			INSERT INTO shop_discount_program_items (
				id, program_id, shop_id, sku_id, original_price_vnd, discounted_price_vnd,
				discount_percent, per_user_limit, price_inflation_flagged, warning, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
			RETURNING id, program_id, shop_id, sku_id, original_price_vnd, discounted_price_vnd,
				discount_percent, per_user_limit, price_inflation_flagged, warning, created_at, updated_at
		`
		saved, scanErr := scanShopDiscountItemBase(tx.QueryRow(ctx, insertItemQ,
			it.ID, out.ID, out.ShopID, it.SKUID, it.OriginalPriceVND, it.DiscountedPriceVND,
			it.DiscountPercent, it.PerUserLimit, it.PriceInflationFlagged, it.Warning, now.UTC(),
		))
		if scanErr != nil {
			return nil, fmt.Errorf("insert shop discount program item: %w", scanErr)
		}
		saved.ProgramStatus = out.Status
		saved.StartsAt = out.StartsAt
		saved.EndsAt = out.EndsAt
		out.Items = append(out.Items, *saved)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit shop discount program: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) GetShopDiscountProgram(ctx context.Context, programID string) (*domain.ShopDiscountProgram, error) {
	const q = `
		SELECT id, shop_id, name, starts_at, ends_at, status, ended_at, created_by, created_at, updated_at
		FROM shop_discount_programs
		WHERE id = $1
	`
	prog, err := scanShopDiscountProgram(s.pool.QueryRow(ctx, q, programID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get shop discount program: %w", err)
	}

	items, err := s.loadProgramItems(ctx, s.pool, []string{prog.ID})
	if err != nil {
		return nil, err
	}
	for i := range items[prog.ID] {
		items[prog.ID][i].ProgramStatus = prog.Status
		items[prog.ID][i].StartsAt = prog.StartsAt
		items[prog.ID][i].EndsAt = prog.EndsAt
	}
	prog.Items = items[prog.ID]
	return prog, nil
}

func (s *PostgresStore) UpdateShopDiscountProgram(
	ctx context.Context,
	programID string,
	now time.Time,
	mutate func(cur *domain.ShopDiscountProgram) (replaceItems bool, err error),
) (*domain.ShopDiscountProgram, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin update shop discount program tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	prog, err := scanShopDiscountProgram(tx.QueryRow(ctx, `
		SELECT id, shop_id, name, starts_at, ends_at, status, ended_at, created_by, created_at, updated_at
		FROM shop_discount_programs
		WHERE id = $1
		FOR UPDATE
	`, programID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock shop discount program: %w", err)
	}

	existingItemsMap, err := s.loadProgramItems(ctx, tx, []string{prog.ID})
	if err != nil {
		return nil, err
	}
	prog.Items = existingItemsMap[prog.ID]
	existingBySKU := make(map[string]domain.ShopDiscountProgramItem, len(prog.Items))
	for _, it := range prog.Items {
		existingBySKU[it.SKUID] = it
	}

	replaceItems, err := mutate(prog)
	if err != nil {
		return nil, err
	}

	prog.StartsAt = prog.StartsAt.UTC()
	prog.EndsAt = prog.EndsAt.UTC()
	prog.Status = prog.EffectiveStatus(now)

	skusToCheck := collectProgramSKUs(prog.Items)
	if err := lockShopDiscountSKUsInTx(ctx, tx, skusToCheck); err != nil {
		return nil, err
	}
	if err := checkShopProgramOverlapInTx(ctx, tx, prog.ID, skusToCheck, prog.StartsAt, prog.EndsAt, now); err != nil {
		return nil, err
	}

	out, err := scanShopDiscountProgram(tx.QueryRow(ctx, `
		UPDATE shop_discount_programs
		SET name = $2,
			starts_at = $3,
			ends_at = $4,
			status = $5,
			updated_at = NOW()
		WHERE id = $1
		RETURNING id, shop_id, name, starts_at, ends_at, status, ended_at, created_by, created_at, updated_at
	`, prog.ID, prog.Name, prog.StartsAt, prog.EndsAt, prog.Status))
	if err != nil {
		return nil, fmt.Errorf("update shop discount program: %w", err)
	}

	if replaceItems {
		keepSKUs := make([]string, 0, len(prog.Items))
		outItems := make([]domain.ShopDiscountProgramItem, 0, len(prog.Items))
		for _, it := range prog.Items {
			keepSKUs = append(keepSKUs, it.SKUID)
			if prev, exists := existingBySKU[it.SKUID]; exists {
				saved, updErr := scanShopDiscountItemBase(tx.QueryRow(ctx, `
					UPDATE shop_discount_program_items
					SET original_price_vnd = $2,
						discounted_price_vnd = $3,
						discount_percent = $4,
						per_user_limit = $5,
						price_inflation_flagged = $6,
						warning = $7,
						updated_at = NOW()
					WHERE id = $1
					RETURNING id, program_id, shop_id, sku_id, original_price_vnd, discounted_price_vnd,
						discount_percent, per_user_limit, price_inflation_flagged, warning, created_at, updated_at
				`, prev.ID, it.OriginalPriceVND, it.DiscountedPriceVND, it.DiscountPercent,
					it.PerUserLimit, it.PriceInflationFlagged, it.Warning))
				if updErr != nil {
					return nil, fmt.Errorf("update shop discount item: %w", updErr)
				}
				saved.ProgramStatus = out.Status
				saved.StartsAt = out.StartsAt
				saved.EndsAt = out.EndsAt
				outItems = append(outItems, *saved)
			} else {
				newID := it.ID
				if newID == "" {
					newID = stableShopDiscountItemID(out.ID, it.SKUID)
				}
				saved, insErr := scanShopDiscountItemBase(tx.QueryRow(ctx, `
					INSERT INTO shop_discount_program_items (
						id, program_id, shop_id, sku_id, original_price_vnd, discounted_price_vnd,
						discount_percent, per_user_limit, price_inflation_flagged, warning, created_at, updated_at
					) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
					RETURNING id, program_id, shop_id, sku_id, original_price_vnd, discounted_price_vnd,
						discount_percent, per_user_limit, price_inflation_flagged, warning, created_at, updated_at
				`, newID, out.ID, out.ShopID, it.SKUID, it.OriginalPriceVND, it.DiscountedPriceVND,
					it.DiscountPercent, it.PerUserLimit, it.PriceInflationFlagged, it.Warning, now.UTC()))
				if insErr != nil {
					return nil, fmt.Errorf("insert shop discount item on update: %w", insErr)
				}
				saved.ProgramStatus = out.Status
				saved.StartsAt = out.StartsAt
				saved.EndsAt = out.EndsAt
				outItems = append(outItems, *saved)
			}
		}
		if _, delErr := tx.Exec(ctx, `
			DELETE FROM shop_discount_program_items
			WHERE program_id = $1 AND NOT (sku_id = ANY($2))
		`, out.ID, keepSKUs); delErr != nil {
			return nil, fmt.Errorf("delete removed shop discount items: %w", delErr)
		}
		out.Items = outItems
	} else {
		for i := range prog.Items {
			if _, updErr := tx.Exec(ctx, `
				UPDATE shop_discount_program_items SET warning = $2, updated_at = NOW()
				WHERE id = $1 AND warning IS DISTINCT FROM $2
			`, prog.Items[i].ID, prog.Items[i].Warning); updErr != nil {
				return nil, fmt.Errorf("update shop discount item warning: %w", updErr)
			}
			prog.Items[i].ProgramStatus = out.Status
			prog.Items[i].StartsAt = out.StartsAt
			prog.Items[i].EndsAt = out.EndsAt
		}
		out.Items = prog.Items
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit update shop discount program: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) EndShopDiscountProgram(
	ctx context.Context,
	programID string,
	now time.Time,
	check func(cur *domain.ShopDiscountProgram) error,
) (*domain.ShopDiscountProgram, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin end shop discount program tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	prog, err := scanShopDiscountProgram(tx.QueryRow(ctx, `
		SELECT id, shop_id, name, starts_at, ends_at, status, ended_at, created_by, created_at, updated_at
		FROM shop_discount_programs
		WHERE id = $1
		FOR UPDATE
	`, programID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock shop discount program for end: %w", err)
	}
	if check != nil {
		if err := check(prog); err != nil {
			return nil, err
		}
	}

	out, err := scanShopDiscountProgram(tx.QueryRow(ctx, `
		UPDATE shop_discount_programs
		SET status = 'ENDED',
			ended_at = COALESCE(ended_at, $2),
			updated_at = NOW()
		WHERE id = $1
		RETURNING id, shop_id, name, starts_at, ends_at, status, ended_at, created_by, created_at, updated_at
	`, programID, now.UTC()))
	if err != nil {
		return nil, fmt.Errorf("end shop discount program: %w", err)
	}

	itemsMap, err := s.loadProgramItems(ctx, tx, []string{out.ID})
	if err != nil {
		return nil, err
	}
	out.Items = itemsMap[out.ID]
	for i := range out.Items {
		out.Items[i].ProgramStatus = domain.ShopProgramStatusEnded
		out.Items[i].StartsAt = out.StartsAt
		out.Items[i].EndsAt = out.EndsAt
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit end shop discount program: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) ListShopDiscountPrograms(
	ctx context.Context,
	shopID, statusFilter string,
	now time.Time,
	page, pageSize int32,
) ([]domain.ShopDiscountProgram, int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	page, pageSize = normalizePage(page, pageSize)
	offset := (page - 1) * pageSize

	where := `($1 = '' OR shop_id = $1)`
	args := []any{shopID}
	switch statusFilter {
	case domain.ShopProgramStatusScheduled:
		where += ` AND status <> 'ENDED' AND ended_at IS NULL AND starts_at > $2 AND ends_at > $2`
		args = append(args, now.UTC())
	case domain.ShopProgramStatusActive:
		where += ` AND status <> 'ENDED' AND ended_at IS NULL AND starts_at <= $2 AND ends_at > $2`
		args = append(args, now.UTC())
	case domain.ShopProgramStatusEnded:
		where += ` AND (status = 'ENDED' OR ended_at IS NOT NULL OR ends_at <= $2)`
		args = append(args, now.UTC())
	}

	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(1) FROM shop_discount_programs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count shop discount programs: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	limitIdx := len(args) + 1
	offsetIdx := len(args) + 2
	listQ := fmt.Sprintf(`
		SELECT id, shop_id, name, starts_at, ends_at, status, ended_at, created_by, created_at, updated_at
		FROM shop_discount_programs
		WHERE %s
		ORDER BY created_at DESC, id ASC
		LIMIT $%d OFFSET $%d
	`, where, limitIdx, offsetIdx)
	queryArgs := append(append([]any(nil), args...), pageSize, offset)

	rows, err := s.pool.Query(ctx, listQ, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list shop discount programs: %w", err)
	}
	defer rows.Close()

	programs := make([]domain.ShopDiscountProgram, 0)
	progIDs := make([]string, 0)
	for rows.Next() {
		p, scanErr := scanShopDiscountProgram(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan shop discount program: %w", scanErr)
		}
		p.Status = p.EffectiveStatus(now)
		programs = append(programs, *p)
		progIDs = append(progIDs, p.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate shop discount programs: %w", err)
	}

	if len(progIDs) > 0 {
		itemsMap, err := s.loadProgramItems(ctx, s.pool, progIDs)
		if err != nil {
			return nil, 0, err
		}
		for i := range programs {
			items := itemsMap[programs[i].ID]
			for j := range items {
				items[j].ProgramStatus = programs[i].Status
				items[j].StartsAt = programs[i].StartsAt
				items[j].EndsAt = programs[i].EndsAt
			}
			programs[i].Items = items
		}
	}
	return programs, total, nil
}

func (s *PostgresStore) GetShopDiscountProgramItem(ctx context.Context, itemID string) (*domain.ShopDiscountProgramItem, error) {
	const q = `
		SELECT sdi.id, sdi.program_id, sdi.shop_id, sdi.sku_id, sdi.original_price_vnd,
			sdi.discounted_price_vnd, sdi.discount_percent, sdi.per_user_limit,
			sdi.price_inflation_flagged, sdi.warning, sdp.status, sdp.starts_at, sdp.ends_at,
			sdi.created_at, sdi.updated_at
		FROM shop_discount_program_items sdi
		JOIN shop_discount_programs sdp ON sdp.id = sdi.program_id
		WHERE sdi.id = $1
	`
	var out domain.ShopDiscountProgramItem
	err := s.pool.QueryRow(ctx, q, itemID).Scan(
		&out.ID, &out.ProgramID, &out.ShopID, &out.SKUID, &out.OriginalPriceVND,
		&out.DiscountedPriceVND, &out.DiscountPercent, &out.PerUserLimit,
		&out.PriceInflationFlagged, &out.Warning, &out.ProgramStatus, &out.StartsAt, &out.EndsAt,
		&out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get shop discount program item: %w", err)
	}
	return &out, nil
}

const maxActiveShopDiscountRows = 500

func (s *PostgresStore) ListActiveShopDiscounts(
	ctx context.Context,
	skuIDs []string,
	shopID string,
	now time.Time,
) ([]domain.ShopDiscountProgramItem, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	const q = `
		SELECT sdi.id, sdi.program_id, sdi.shop_id, sdi.sku_id, sdi.original_price_vnd,
			sdi.discounted_price_vnd, sdi.discount_percent, sdi.per_user_limit,
			sdi.price_inflation_flagged, sdi.warning, sdp.status, sdp.starts_at, sdp.ends_at,
			sdi.created_at, sdi.updated_at
		FROM shop_discount_program_items sdi
		JOIN shop_discount_programs sdp ON sdp.id = sdi.program_id
		WHERE sdp.status <> 'ENDED'
		  AND sdp.ended_at IS NULL
		  AND sdp.starts_at <= $1
		  AND sdp.ends_at > $1
		  AND ($2 = '' OR sdi.shop_id = $2)
		  AND (cardinality($3::text[]) = 0 OR sdi.sku_id = ANY($3::text[]))
		  AND NOT EXISTS (
		      SELECT 1 FROM shop_suspension_state sss
		      WHERE sss.shop_id = sdi.shop_id AND sss.suspended = TRUE
		  )
		ORDER BY sdp.starts_at ASC, sdi.id ASC
		LIMIT 500
	`
	if skuIDs == nil {
		skuIDs = []string{}
	}
	rows, err := s.pool.Query(ctx, q, now.UTC(), shopID, skuIDs)
	if err != nil {
		return nil, fmt.Errorf("list active shop discounts: %w", err)
	}
	defer rows.Close()

	out := make([]domain.ShopDiscountProgramItem, 0)
	for rows.Next() {
		var it domain.ShopDiscountProgramItem
		if scanErr := rows.Scan(
			&it.ID, &it.ProgramID, &it.ShopID, &it.SKUID, &it.OriginalPriceVND,
			&it.DiscountedPriceVND, &it.DiscountPercent, &it.PerUserLimit,
			&it.PriceInflationFlagged, &it.Warning, &it.ProgramStatus, &it.StartsAt, &it.EndsAt,
			&it.CreatedAt, &it.UpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan active shop discount: %w", scanErr)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

type queryRower interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (s *PostgresStore) loadProgramItems(ctx context.Context, q queryRower, programIDs []string) (map[string][]domain.ShopDiscountProgramItem, error) {
	result := make(map[string][]domain.ShopDiscountProgramItem, len(programIDs))
	if len(programIDs) == 0 {
		return result, nil
	}
	const itemQ = `
		SELECT id, program_id, shop_id, sku_id, original_price_vnd, discounted_price_vnd,
			discount_percent, per_user_limit, price_inflation_flagged, warning, created_at, updated_at
		FROM shop_discount_program_items
		WHERE program_id = ANY($1)
		ORDER BY created_at ASC, id ASC
	`
	rows, err := q.Query(ctx, itemQ, programIDs)
	if err != nil {
		return nil, fmt.Errorf("load shop discount items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		it, scanErr := scanShopDiscountItemBase(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan shop discount item: %w", scanErr)
		}
		result[it.ProgramID] = append(result[it.ProgramID], *it)
	}
	return result, rows.Err()
}

func lockShopDiscountSKUsInTx(ctx context.Context, tx pgx.Tx, skuIDs []string) error {
	for _, skuID := range skuIDs {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "shop_disc_sku:"+skuID); err != nil {
			return fmt.Errorf("acquire shop discount sku lock: %w", err)
		}
	}
	return nil
}

func checkShopProgramOverlapInTx(
	ctx context.Context,
	tx pgx.Tx,
	excludeProgramID string,
	skuIDs []string,
	startsAt, endsAt, now time.Time,
) error {
	if len(skuIDs) == 0 {
		return nil
	}
	const overlapQ = `
		SELECT sdi.sku_id
		FROM shop_discount_program_items sdi
		JOIN shop_discount_programs sdp ON sdp.id = sdi.program_id
		WHERE sdi.sku_id = ANY($1)
		  AND ($2 = '' OR sdp.id <> $2)
		  AND sdp.status <> 'ENDED'
		  AND sdp.ended_at IS NULL
		  AND sdp.ends_at > $3
		  AND sdp.starts_at < $4
		  AND sdp.ends_at > $5
		LIMIT 1
	`
	var conflictSKU string
	err := tx.QueryRow(ctx, overlapQ, skuIDs, excludeProgramID, now.UTC(), endsAt.UTC(), startsAt.UTC()).Scan(&conflictSKU)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check overlapping shop discount program: %w", err)
	}
	return fmt.Errorf("%w: %s", ErrOverlappingShopProgram, conflictSKU)
}

func collectProgramSKUs(items []domain.ShopDiscountProgramItem) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.SKUID == "" {
			continue
		}
		if _, ok := seen[it.SKUID]; ok {
			continue
		}
		seen[it.SKUID] = struct{}{}
		out = append(out, it.SKUID)
	}
	sort.Strings(out)
	return out
}

func scanShopDiscountProgram(row rowScanner) (*domain.ShopDiscountProgram, error) {
	var p domain.ShopDiscountProgram
	err := row.Scan(
		&p.ID, &p.ShopID, &p.Name, &p.StartsAt, &p.EndsAt, &p.Status, &p.EndedAt,
		&p.CreatedBy, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func scanShopDiscountItemBase(row rowScanner) (*domain.ShopDiscountProgramItem, error) {
	var it domain.ShopDiscountProgramItem
	err := row.Scan(
		&it.ID, &it.ProgramID, &it.ShopID, &it.SKUID, &it.OriginalPriceVND,
		&it.DiscountedPriceVND, &it.DiscountPercent, &it.PerUserLimit,
		&it.PriceInflationFlagged, &it.Warning, &it.CreatedAt, &it.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &it, nil
}
