package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexusmart/core/pkg/common/jetstream"
	"github.com/nexusmart/core/services/product/internal/db"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// PgStore implements Store backed by PostgreSQL using pgx/v5 and sqlc queries.
type PgStore struct {
	pool                 *pgxpool.Pool
	outboxLease          atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxLease
	outboxPublishTimeout atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxPublishTimeout
	clockMu              sync.RWMutex
	outboxNowFn          func() time.Time
	logger               atomic.Pointer[slog.Logger]
	*pgQueries
}

// NewPgStore creates a PgStore wrapping the given pgxpool.Pool.
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	st := &PgStore{
		pool:      pool,
		pgQueries: &pgQueries{q: db.New(pool)},
	}
	st.outboxLease.Store(int64(jetstream.DefaultOutboxLease))
	st.outboxPublishTimeout.Store(int64(jetstream.DefaultOutboxPublishTimeout))
	return st
}

// SetLogger attaches a structured logger used for outbox poison-event ERROR alerts.
func (s *PgStore) SetLogger(l *slog.Logger) {
	s.logger.Store(l)
}

func (s *PgStore) outboxLogger() *slog.Logger {
	if l := s.logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// SetOutboxLease overrides the outbox claim lease duration (defaults to jetstream.DefaultOutboxLease).
func (s *PgStore) SetOutboxLease(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxLease
	}
	s.outboxLease.Store(int64(d))
}

// SetOutboxPublishTimeout overrides the max per-event publish timeout budget (defaults to jetstream.DefaultOutboxPublishTimeout).
func (s *PgStore) SetOutboxPublishTimeout(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxPublishTimeout
	}
	s.outboxPublishTimeout.Store(int64(d))
}

// SetOutboxClock overrides the Go clock used for lease-budget checks (defaults to time.Now with monotonic clock).
func (s *PgStore) SetOutboxClock(fn func() time.Time) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	s.outboxNowFn = fn
}

func (s *PgStore) outboxNow() time.Time {
	s.clockMu.RLock()
	fn := s.outboxNowFn
	s.clockMu.RUnlock()
	if fn == nil {
		return time.Now()
	}
	return fn()
}

func (s *PgStore) outboxLeaseDuration() time.Duration {
	d := time.Duration(s.outboxLease.Load())
	if d <= 0 {
		return jetstream.DefaultOutboxLease
	}
	return d
}

func (s *PgStore) outboxPublishTimeoutDuration() time.Duration {
	d := time.Duration(s.outboxPublishTimeout.Load())
	if d <= 0 {
		return jetstream.DefaultOutboxPublishTimeout
	}
	return d
}

func (s *PgStore) outboxLeaseMs() int64 {
	ms := s.outboxLeaseDuration().Milliseconds()
	if ms <= 0 {
		return 1
	}
	return ms
}

func (s *PgStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *PgStore) WithTx(ctx context.Context, fn func(q Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	txq := &pgQueries{q: db.New(tx)}
	if err := fn(txq); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// ClaimPendingOutbox claims a batch of unpublished outbox rows inside a short transaction
// (using claimed_until + FOR UPDATE SKIP LOCKED + claim_token) and commits before returning
// so callers can publish to NATS completely outside any DB transaction.
func (s *PgStore) ClaimPendingOutbox(ctx context.Context, limit int32) ([]OutboxEvent, string, error) {
	if limit <= 0 {
		limit = 50
	}
	claimToken := uuid.NewString()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("begin outbox claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)
	if err := q.LockOutboxClaim(ctx); err != nil {
		return nil, "", fmt.Errorf("lock outbox claim: %w", err)
	}
	rows, err := q.ClaimPendingOutboxEvents(ctx, db.ClaimPendingOutboxEventsParams{
		LeaseMs:    s.outboxLeaseMs(),
		ClaimToken: claimToken,
		BatchLimit: limit,
	})
	if err != nil {
		return nil, "", fmt.Errorf("claim pending outbox events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", fmt.Errorf("commit outbox claim tx: %w", err)
	}

	out := make([]OutboxEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDBClaimedOutboxEvent(row))
	}
	// UPDATE ... FROM ... RETURNING does not guarantee output order; restore creation order.
	sort.Slice(out, func(i, j int) bool {
		return outboxEventBefore(out[i], out[j])
	})
	return out, claimToken, nil
}

// MarkOutboxPublished marks an event published in a standalone query guarded by claim_token.
func (s *PgStore) MarkOutboxPublished(ctx context.Context, id, claimToken string) (bool, error) {
	n, err := s.q.MarkOutboxEventPublished(ctx, db.MarkOutboxEventPublishedParams{
		ID:         id,
		ClaimToken: claimToken,
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *PgStore) releaseRemainingOutboxClaims(ctx context.Context, remaining []OutboxEvent, claimToken string) {
	if len(remaining) == 0 {
		return
	}
	ids := make([]string, len(remaining))
	for i, rem := range remaining {
		ids[i] = rem.ID
	}
	relCtx, relCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer relCancel()
	_, _ = s.q.ReleaseOutboxEventClaims(relCtx, db.ReleaseOutboxEventClaimsParams{
		Ids:        ids,
		ClaimToken: claimToken,
	})
}

func (s *PgStore) ProcessPendingOutbox(ctx context.Context, limit int32, publishFn func(ctx context.Context, ev OutboxEvent) error) (int, error) {
	lease := s.outboxLeaseDuration()
	pubTimeout := s.outboxPublishTimeoutDuration()
	// Measure lease budget with Go clock captured BEFORE claiming so leaseDeadline <= DB claimed_until.
	leaseDeadline := s.outboxNow().Add(lease)

	claimed, claimToken, err := s.ClaimPendingOutbox(ctx, limit)
	if err != nil {
		return 0, err
	}
	if len(claimed) == 0 {
		return 0, nil
	}

	var toRelease []OutboxEvent
	defer func() {
		if len(toRelease) > 0 {
			s.releaseRemainingOutboxClaims(ctx, toRelease, claimToken)
		}
	}()

	published := 0
	failedAggs := make(map[string]bool)
	for i, ev := range claimed {
		if ctx.Err() != nil {
			toRelease = append(toRelease, claimed[i:]...)
			return published, ctx.Err()
		}
		if leaseDeadline.Sub(s.outboxNow()) < pubTimeout {
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
			var backoffMs int64
			if !infraErr {
				backoffMs = jetstream.OutboxRetryBackoff(ev.Attempts + 1).Milliseconds()
			}
			failCtx, failCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			newAttempts, failErr := s.q.MarkOutboxEventAttemptFailed(failCtx, db.MarkOutboxEventAttemptFailedParams{
				ID:         ev.ID,
				ClaimToken: claimToken,
				LastError:  pubErr.Error(),
				BackoffMs:  backoffMs,
			})
			failCancel()
			if infraErr {
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, pubErr
			}
			if errors.Is(failErr, pgx.ErrNoRows) {
				// Lost lease while publishing this event; release remaining claims and abort batch.
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, nil
			}
			if failErr != nil {
				toRelease = append(toRelease, claimed[i+1:]...)
				return published, fmt.Errorf("mark outbox event failed: %w", failErr)
			}
			if newAttempts >= jetstream.OutboxPoisonLogAttempt {
				s.outboxLogger().ErrorContext(ctx, "outbox event repeatedly failed",
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

		ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		marked, markErr := s.MarkOutboxPublished(ackCtx, ev.ID, claimToken)
		ackCancel()
		if markErr != nil {
			toRelease = append(toRelease, claimed[i+1:]...)
			return published, fmt.Errorf("mark outbox event published: %w", markErr)
		}
		if !marked {
			toRelease = append(toRelease, claimed[i+1:]...)
			return published, nil
		}
		published++
	}
	return published, nil
}

type pgQueries struct {
	q *db.Queries
}

func (p *pgQueries) LockCategoryTree(ctx context.Context) error {
	return mapPgError(p.q.LockCategoryTree(ctx))
}

func (p *pgQueries) CreateCategory(ctx context.Context, cat Category) (Category, error) {
	row, err := p.q.CreateCategory(ctx, db.CreateCategoryParams{
		ID:                cat.ID,
		Name:              cat.Name,
		Slug:              cat.Slug,
		ParentID:          toPgText(cat.ParentID),
		CommissionRateBps: cat.CommissionRateBps,
		SortOrder:         cat.SortOrder,
		IsProhibited:      cat.IsProhibited,
	})
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return fromCategoryFields(row.ID, row.Name, row.Slug, row.ParentID, row.CommissionRateBps, row.SortOrder, row.IsProhibited, row.CreatedAt, row.UpdatedAt), nil
}

func (p *pgQueries) GetCategoryByID(ctx context.Context, id string) (Category, error) {
	row, err := p.q.GetCategoryByID(ctx, db.GetCategoryByIDParams{
		AsOf: AsOfFromContext(ctx),
		ID:   id,
	})
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return fromCategoryFields(row.ID, row.Name, row.Slug, row.ParentID, row.CommissionRateBps, row.SortOrder, row.IsProhibited, row.CreatedAt, row.UpdatedAt), nil
}

func (p *pgQueries) GetCategoryBySlug(ctx context.Context, slug string) (Category, error) {
	row, err := p.q.GetCategoryBySlug(ctx, slug)
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return fromCategoryFields(row.ID, row.Name, row.Slug, row.ParentID, row.CommissionRateBps, row.SortOrder, row.IsProhibited, row.CreatedAt, row.UpdatedAt), nil
}

func (p *pgQueries) UpdateCategory(ctx context.Context, cat Category) (Category, error) {
	row, err := p.q.UpdateCategory(ctx, db.UpdateCategoryParams{
		ID:                cat.ID,
		Name:              cat.Name,
		Slug:              cat.Slug,
		ParentID:          toPgText(cat.ParentID),
		CommissionRateBps: cat.CommissionRateBps,
		SortOrder:         cat.SortOrder,
		IsProhibited:      cat.IsProhibited,
	})
	if err != nil {
		return Category{}, mapPgError(err)
	}
	return fromCategoryFields(row.ID, row.Name, row.Slug, row.ParentID, row.CommissionRateBps, row.SortOrder, row.IsProhibited, row.CreatedAt, row.UpdatedAt), nil
}

func (p *pgQueries) UpdateCategoryCommissionRate(ctx context.Context, id string, bps int32) error {
	return mapPgError(p.q.UpdateCategoryCommissionRate(ctx, db.UpdateCategoryCommissionRateParams{
		ID:                id,
		CommissionRateBps: bps,
	}))
}

func (p *pgQueries) CountProductsInCategory(ctx context.Context, categoryID string) (CategoryProductCounts, error) {
	row, err := p.q.CountProductsInCategory(ctx, categoryID)
	if err != nil {
		return CategoryProductCounts{}, mapPgError(err)
	}
	return CategoryProductCounts{
		Total:  row.TotalCnt,
		Active: row.ActiveCnt,
	}, nil
}

func (p *pgQueries) CountChildCategories(ctx context.Context, parentID string) (int64, error) {
	return p.q.CountChildCategories(ctx, toPgText(parentID))
}

func (p *pgQueries) DeleteCategory(ctx context.Context, id string) (bool, error) {
	n, err := p.q.DeleteCategory(ctx, id)
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) ListCategories(ctx context.Context, parentID string) ([]Category, error) {
	asOf := AsOfFromContext(ctx)
	switch parentID {
	case "":
		rows, err := p.q.ListAllCategories(ctx, asOf)
		if err != nil {
			return nil, mapPgError(err)
		}
		out := make([]Category, 0, len(rows))
		for _, r := range rows {
			out = append(out, fromCategoryFields(r.ID, r.Name, r.Slug, r.ParentID, r.CommissionRateBps, r.SortOrder, r.IsProhibited, r.CreatedAt, r.UpdatedAt))
		}
		return out, nil
	case "root":
		rows, err := p.q.ListRootCategories(ctx, asOf)
		if err != nil {
			return nil, mapPgError(err)
		}
		out := make([]Category, 0, len(rows))
		for _, r := range rows {
			out = append(out, fromCategoryFields(r.ID, r.Name, r.Slug, r.ParentID, r.CommissionRateBps, r.SortOrder, r.IsProhibited, r.CreatedAt, r.UpdatedAt))
		}
		return out, nil
	default:
		rows, err := p.q.ListCategoriesByParent(ctx, db.ListCategoriesByParentParams{
			AsOf:     asOf,
			ParentID: toPgText(parentID),
		})
		if err != nil {
			return nil, mapPgError(err)
		}
		out := make([]Category, 0, len(rows))
		for _, r := range rows {
			out = append(out, fromCategoryFields(r.ID, r.Name, r.Slug, r.ParentID, r.CommissionRateBps, r.SortOrder, r.IsProhibited, r.CreatedAt, r.UpdatedAt))
		}
		return out, nil
	}
}

func (p *pgQueries) CategoryHasAncestor(ctx context.Context, startID, targetAncestorID string) (bool, error) {
	has, err := p.q.CategoryHasAncestor(ctx, db.CategoryHasAncestorParams{
		TargetAncestorID: targetAncestorID,
		StartID:          startID,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return has, nil
}

func (p *pgQueries) IsCategoryProhibited(ctx context.Context, categoryID string) (bool, error) {
	prohibited, err := p.q.IsCategoryProhibited(ctx, categoryID)
	if err != nil {
		return false, mapPgError(err)
	}
	return prohibited, nil
}

func (p *pgQueries) ListProhibitedCategoryIDs(ctx context.Context) ([]string, error) {
	ids, err := p.q.ListProhibitedCategoryIDs(ctx)
	if err != nil {
		return nil, mapPgError(err)
	}
	return ids, nil
}

func (p *pgQueries) GetCategoryDescendants(ctx context.Context, categoryIDs []string) ([]string, error) {
	if len(categoryIDs) == 0 {
		return []string{}, nil
	}
	ids, err := p.q.GetCategoryDescendants(ctx, categoryIDs)
	if err != nil {
		return nil, mapPgError(err)
	}
	return ids, nil
}

func (p *pgQueries) InsertCategoryFeeSchedule(ctx context.Context, sched CategoryFeeSchedule) (CategoryFeeSchedule, error) {
	createdAt := sched.CreatedAt
	if createdAt.IsZero() {
		createdAt = AsOfFromContext(ctx)
	}
	row, err := p.q.InsertCategoryFeeSchedule(ctx, db.InsertCategoryFeeScheduleParams{
		ID:                sched.ID,
		CategoryID:        sched.CategoryID,
		CommissionRateBps: sched.CommissionRateBps,
		EffectiveFrom:     sched.EffectiveFrom.UTC(),
		CreatedBy:         sched.CreatedBy,
		Note:              sched.Note,
		CreatedAt:         createdAt.UTC(),
	})
	if err != nil {
		return CategoryFeeSchedule{}, mapPgError(err)
	}
	return fromDBCategoryFeeSchedule(row), nil
}

func (p *pgQueries) ListCategoryFeeSchedules(ctx context.Context, categoryID string, limit, offset int32) ([]CategoryFeeSchedule, int64, error) {
	total, err := p.q.CountCategoryFeeSchedules(ctx, categoryID)
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []CategoryFeeSchedule{}, 0, nil
	}
	rows, err := p.q.ListCategoryFeeSchedules(ctx, db.ListCategoryFeeSchedulesParams{
		CategoryID: categoryID,
		LimitVal:   limit,
		OffsetVal:  offset,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]CategoryFeeSchedule, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBCategoryFeeSchedule(r))
	}
	return out, total, nil
}

func (p *pgQueries) CreateProduct(ctx context.Context, prod Product) (Product, error) {
	imgBytes, err := marshalStringSlice(prod.Images)
	if err != nil {
		return Product{}, err
	}
	flaggedBytes, err := marshalStringSlice(prod.FlaggedKeywords)
	if err != nil {
		return Product{}, err
	}
	shopSuspended := prod.ShopSuspended
	if !shopSuspended {
		st, stErr := p.q.GetShopSuspensionState(ctx, prod.ShopID)
		if stErr == nil && st.Suspended {
			shopSuspended = true
		} else if stErr != nil && !errors.Is(stErr, pgx.ErrNoRows) {
			return Product{}, mapPgError(stErr)
		}
	}
	row, err := p.q.CreateProduct(ctx, db.CreateProductParams{
		ID:              prod.ID,
		ShopID:          prod.ShopID,
		CategoryID:      prod.CategoryID,
		Name:            prod.Name,
		Description:     prod.Description,
		Images:          imgBytes,
		Status:          prod.Status,
		RejectionReason: prod.RejectionReason,
		MinPriceVnd:     prod.MinPriceVND,
		MaxPriceVnd:     prod.MaxPriceVND,
		Slug:            prod.Slug,
		SearchText:      BuildProductSearchText(prod.Name, prod.Description),
		FlaggedKeywords: flaggedBytes,
		ShopSuspended:   shopSuspended,
	})
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetProductByID(ctx context.Context, id string) (Product, error) {
	row, err := p.q.GetProductByID(ctx, id)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetProductByIDForUpdate(ctx context.Context, id string) (Product, error) {
	row, err := p.q.GetProductByIDForUpdate(ctx, id)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetProductByIDForUpdateAny(ctx context.Context, id string) (Product, error) {
	row, err := p.q.GetProductByIDForUpdateAny(ctx, id)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetProductBySlug(ctx context.Context, slug string) (Product, error) {
	row, err := p.q.GetProductBySlug(ctx, slug)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) GetDeletedApprovedProductBySlug(ctx context.Context, slug string) (Product, error) {
	row, err := p.q.GetDeletedApprovedProductBySlug(ctx, slug)
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) UpdateProduct(ctx context.Context, prod Product) (Product, error) {
	imgBytes, err := marshalStringSlice(prod.Images)
	if err != nil {
		return Product{}, err
	}
	flaggedBytes, err := marshalStringSlice(prod.FlaggedKeywords)
	if err != nil {
		return Product{}, err
	}
	row, err := p.q.UpdateProduct(ctx, db.UpdateProductParams{
		ID:              prod.ID,
		CategoryID:      prod.CategoryID,
		Name:            prod.Name,
		Description:     prod.Description,
		Images:          imgBytes,
		Status:          prod.Status,
		RejectionReason: prod.RejectionReason,
		MinPriceVnd:     prod.MinPriceVND,
		MaxPriceVnd:     prod.MaxPriceVND,
		Slug:            prod.Slug,
		SearchText:      BuildProductSearchText(prod.Name, prod.Description),
		FlaggedKeywords: flaggedBytes,
	})
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) UpdateProductReview(ctx context.Context, id, status, rejectionReason string, flaggedKeywords []string) (Product, error) {
	flaggedBytes, err := marshalStringSlice(flaggedKeywords)
	if err != nil {
		return Product{}, err
	}
	row, err := p.q.UpdateProductReview(ctx, db.UpdateProductReviewParams{
		ID:              id,
		Status:          status,
		RejectionReason: rejectionReason,
		FlaggedKeywords: flaggedBytes,
	})
	if err != nil {
		return Product{}, mapPgError(err)
	}
	return fromDBProduct(row), nil
}

func (p *pgQueries) IncrementProductRating(ctx context.Context, id string, ratingDelta int64) (int64, int32, error) {
	row, err := p.q.IncrementProductRating(ctx, db.IncrementProductRatingParams{
		RatingDelta: ratingDelta,
		ID:          id,
	})
	if err != nil {
		return 0, 0, mapPgError(err)
	}
	return row.RatingSum, row.ReviewCount, nil
}

func (p *pgQueries) ListProductsForModerationScan(ctx context.Context, cursorID string, limit int32) ([]Product, error) {
	rows, err := p.q.ListProductsForModerationScan(ctx, db.ListProductsForModerationScanParams{
		CursorID: cursorID,
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

func (p *pgQueries) UpdateProductStatus(ctx context.Context, id, status string) error {
	return mapPgError(p.q.UpdateProductStatus(ctx, db.UpdateProductStatusParams{
		ID:     id,
		Status: status,
	}))
}

func (p *pgQueries) SoftDeleteProduct(ctx context.Context, id string) (bool, error) {
	n, err := p.q.SoftDeleteProduct(ctx, id)
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) ListProducts(ctx context.Context, filter ListProductsFilter) ([]Product, int64, error) {
	sqlKeyword := ""
	if strings.TrimSpace(filter.Keyword) != "" {
		norm := NormalizeSearchText(filter.Keyword)
		if norm == "" {
			return []Product{}, 0, nil
		}
		sqlKeyword = EscapeLikePattern(norm)
	}

	if len(filter.SKUIDs) > 0 {
		total, err := p.q.CountProductsBySKUIDs(ctx, db.CountProductsBySKUIDsParams{
			CategoryID:   filter.CategoryID,
			Keyword:      sqlKeyword,
			ShopID:       filter.ShopID,
			StatusFilter: filter.Status,
			MinPriceVnd:  filter.MinPriceVND,
			MaxPriceVnd:  filter.MaxPriceVND,
			SkuIds:       filter.SKUIDs,
		})
		if err != nil {
			return nil, 0, mapPgError(err)
		}
		if total == 0 {
			return []Product{}, 0, nil
		}

		rows, err := p.q.ListProductsBySKUIDs(ctx, db.ListProductsBySKUIDsParams{
			CategoryID:   filter.CategoryID,
			Keyword:      sqlKeyword,
			ShopID:       filter.ShopID,
			StatusFilter: filter.Status,
			MinPriceVnd:  filter.MinPriceVND,
			MaxPriceVnd:  filter.MaxPriceVND,
			SortBy:       filter.SortBy,
			LimitVal:     filter.Limit,
			OffsetVal:    filter.Offset,
			SkuIds:       filter.SKUIDs,
		})
		if err != nil {
			return nil, 0, mapPgError(err)
		}
		out := make([]Product, 0, len(rows))
		for _, r := range rows {
			out = append(out, fromDBProduct(r))
		}
		return out, total, nil
	}

	total, err := p.q.CountProductsFiltered(ctx, db.CountProductsFilteredParams{
		CategoryID:   filter.CategoryID,
		Keyword:      sqlKeyword,
		ShopID:       filter.ShopID,
		StatusFilter: filter.Status,
		PublicOnly:   filter.PublicOnly,
		MinPriceVnd:  filter.MinPriceVND,
		MaxPriceVnd:  filter.MaxPriceVND,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []Product{}, 0, nil
	}

	rows, err := p.q.ListProductsFiltered(ctx, db.ListProductsFilteredParams{
		CategoryID:   filter.CategoryID,
		Keyword:      sqlKeyword,
		ShopID:       filter.ShopID,
		StatusFilter: filter.Status,
		PublicOnly:   filter.PublicOnly,
		MinPriceVnd:  filter.MinPriceVND,
		MaxPriceVnd:  filter.MaxPriceVND,
		SortBy:       filter.SortBy,
		LimitVal:     filter.Limit,
		OffsetVal:    filter.Offset,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]Product, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromDBProduct(r))
	}
	return out, total, nil
}

func (p *pgQueries) CreateSKU(ctx context.Context, s SKU) (SKU, error) {
	attrBytes, err := marshalStringMap(s.Attributes)
	if err != nil {
		return SKU{}, err
	}
	row, err := p.q.CreateSKU(ctx, db.CreateSKUParams{
		ID:          s.ID,
		ProductID:   s.ProductID,
		SkuCode:     s.SKUCode,
		Name:        s.Name,
		Attributes:  attrBytes,
		PriceVnd:    s.PriceVND,
		Stock:       s.Stock,
		WeightGrams: s.WeightGrams,
		ImageURL:    s.ImageURL,
		Status:      s.Status,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	out := mergeDBSKU(row, s)
	if out.MinPrice30dVND <= 0 {
		out.MinPrice30dVND = out.PriceVND
	}
	return out, nil
}

func (p *pgQueries) InsertSKUPriceHistory(ctx context.Context, h SKUPriceHistoryEntry) error {
	recordedAt := h.RecordedAt
	if recordedAt.IsZero() {
		recordedAt = AsOfFromContext(ctx)
	}
	return mapPgError(p.q.InsertSKUPriceHistory(ctx, db.InsertSKUPriceHistoryParams{
		ID:         h.ID,
		SkuID:      h.SKUID,
		PriceVnd:   h.PriceVND,
		ActorID:    h.ActorID,
		Reason:     h.Reason,
		RecordedAt: recordedAt.UTC(),
	}))
}

func (p *pgQueries) UpdateSKUMetadata(ctx context.Context, s SKU) (SKU, error) {
	attrBytes, err := marshalStringMap(s.Attributes)
	if err != nil {
		return SKU{}, err
	}
	row, err := p.q.UpdateSKUMetadata(ctx, db.UpdateSKUMetadataParams{
		ID:          s.ID,
		SkuCode:     s.SKUCode,
		Name:        s.Name,
		Attributes:  attrBytes,
		PriceVnd:    s.PriceVND,
		WeightGrams: s.WeightGrams,
		ImageURL:    s.ImageURL,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, s), nil
}

func (p *pgQueries) ReactivateSKUOptimistic(ctx context.Context, s SKU, expectedVersion int64) (SKU, error) {
	attrBytes, err := marshalStringMap(s.Attributes)
	if err != nil {
		return SKU{}, err
	}
	row, err := p.q.ReactivateSKUOptimistic(ctx, db.ReactivateSKUOptimisticParams{
		ID:          s.ID,
		SkuCode:     s.SKUCode,
		Name:        s.Name,
		Attributes:  attrBytes,
		PriceVnd:    s.PriceVND,
		Stock:       s.Stock,
		WeightGrams: s.WeightGrams,
		ImageURL:    s.ImageURL,
		Version:     expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, getErr := p.GetSKUWithContext(ctx, s.ID)
			if getErr != nil {
				return SKU{}, getErr
			}
			if current.Version != expectedVersion {
				return SKU{}, ErrVersionConflict
			}
			if s.Stock < current.ReservedStock {
				return SKU{}, ErrInvalidStock
			}
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, s), nil
}

func (p *pgQueries) DeactivateSKUsByProduct(ctx context.Context, productID string) error {
	return mapPgError(p.q.DeactivateSKUsByProduct(ctx, productID))
}

func (p *pgQueries) DeactivateSKU(ctx context.Context, skuID string) error {
	return mapPgError(p.q.DeactivateSKU(ctx, skuID))
}

func (p *pgQueries) ListSKUsByProductIDs(ctx context.Context, productIDs []string) ([]SKU, error) {
	if len(productIDs) == 0 {
		return []SKU{}, nil
	}
	asOf := AsOfFromContext(ctx)
	rows, err := p.q.ListSKUsByProductIDs(ctx, db.ListSKUsByProductIDsParams{
		AsOf:       asOf,
		Cutoff30d:  asOf.Add(-PriceHistoryWindow),
		ProductIds: productIDs,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]SKU, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromSKUContextFields(
			r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
			r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
			r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
			r.SkuStatus, r.ProductStatus, r.ProductDeletedAt, r.ProductShopSuspended,
		))
	}
	return out, nil
}

func (p *pgQueries) ListAllSKUsByProductID(ctx context.Context, productID string) ([]SKU, error) {
	asOf := AsOfFromContext(ctx)
	rows, err := p.q.ListAllSKUsByProductID(ctx, db.ListAllSKUsByProductIDParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		ProductID: productID,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]SKU, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromSKUContextFields(
			r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
			r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
			r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
			r.SkuStatus, r.ProductStatus, r.ProductDeletedAt, r.ProductShopSuspended,
		))
	}
	return out, nil
}

func (p *pgQueries) GetSKUsBatch(ctx context.Context, skuIDs []string) ([]SKU, error) {
	if len(skuIDs) == 0 {
		return []SKU{}, nil
	}
	asOf := AsOfFromContext(ctx)
	rows, err := p.q.GetSKUsBatch(ctx, db.GetSKUsBatchParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		SkuIds:    skuIDs,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]SKU, 0, len(rows))
	for _, r := range rows {
		out = append(out, fromSKUContextFields(
			r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
			r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
			r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
			r.SkuStatus, r.ProductStatus, r.ProductDeletedAt, r.ProductShopSuspended,
		))
	}
	return out, nil
}

func (p *pgQueries) GetSKUWithContext(ctx context.Context, skuID string) (SKU, error) {
	asOf := AsOfFromContext(ctx)
	r, err := p.q.GetSKUWithContext(ctx, db.GetSKUWithContextParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		ID:        skuID,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return fromSKUContextFields(
		r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
		r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
		r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
		r.SkuStatus, r.ProductStatus, r.ProductDeletedAt, r.ProductShopSuspended,
	), nil
}

func (p *pgQueries) GetSKUWithContextForUpdate(ctx context.Context, skuID string) (SKU, error) {
	asOf := AsOfFromContext(ctx)
	r, err := p.q.GetSKUWithContextForUpdate(ctx, db.GetSKUWithContextForUpdateParams{
		AsOf:      asOf,
		Cutoff30d: asOf.Add(-PriceHistoryWindow),
		ID:        skuID,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return fromSKUContextFields(
		r.ID, r.ProductID, r.ShopID, r.ProductName, r.SkuCode, r.Name,
		r.Attributes, r.PriceVnd, r.MinPrice30dVnd, r.Stock, r.ReservedStock, r.Version,
		r.WeightGrams, r.ImageURL, r.CategoryID, r.CommissionRateBps,
		r.SkuStatus, r.ProductStatus, r.ProductDeletedAt, r.ProductShopSuspended,
	), nil
}

func (p *pgQueries) CountActiveInStockSKUsByProduct(ctx context.Context, productID string) (int64, error) {
	return p.q.CountActiveInStockSKUsByProduct(ctx, productID)
}

func (p *pgQueries) UpdateSKUStockOptimistic(ctx context.Context, skuID string, newStock int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.UpdateSKUStockOptimistic(ctx, db.UpdateSKUStockOptimisticParams{
		Stock:   newStock,
		ID:      skuID,
		Version: expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, getErr := p.GetSKUWithContext(ctx, skuID)
			if getErr != nil {
				return SKU{}, getErr
			}
			if current.Status == SKUStatusInactive {
				return SKU{}, ErrSKUInactive
			}
			if current.Version != expectedVersion {
				return SKU{}, ErrVersionConflict
			}
			if newStock < current.ReservedStock {
				return SKU{}, ErrInvalidStock
			}
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	ctxSKU, err := p.GetSKUWithContext(ctx, row.ID)
	if err != nil {
		return mergeDBSKU(row, SKU{}), nil
	}
	return ctxSKU, nil
}

func (p *pgQueries) ReserveSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.ReserveSKUOptimistic(ctx, db.ReserveSKUOptimisticParams{
		ReservedStock: qty,
		ID:            skuID,
		Version:       expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			current, getErr := p.GetSKUWithContext(ctx, skuID)
			if getErr != nil {
				return SKU{}, getErr
			}
			if current.AvailableStock() < qty {
				return SKU{}, ErrInsufficientStock
			}
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) ReleaseSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.ReleaseSKUOptimistic(ctx, db.ReleaseSKUOptimisticParams{
		ReservedStock: qty,
		ID:            skuID,
		Version:       expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) ConfirmSKUOptimistic(ctx context.Context, skuID string, qty int32, expectedVersion int64) (SKU, error) {
	row, err := p.q.ConfirmSKUOptimistic(ctx, db.ConfirmSKUOptimisticParams{
		Stock:   qty,
		ID:      skuID,
		Version: expectedVersion,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SKU{}, ErrVersionConflict
		}
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) RestockSKUAtomic(ctx context.Context, skuID string, qty int32) (SKU, error) {
	row, err := p.q.RestockSKUAtomic(ctx, db.RestockSKUAtomicParams{
		Stock: qty,
		ID:    skuID,
	})
	if err != nil {
		return SKU{}, mapPgError(err)
	}
	return mergeDBSKU(row, SKU{}), nil
}

func (p *pgQueries) LockStockOrder(ctx context.Context, orderID string) error {
	return mapPgError(p.q.LockStockOrder(ctx, orderID))
}

func (p *pgQueries) InsertStockReleaseTombstone(ctx context.Context, orderID string) (bool, error) {
	n, err := p.q.InsertStockReleaseTombstone(ctx, orderID)
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) HasStockReleaseTombstone(ctx context.Context, orderID string) (bool, error) {
	exists, err := p.q.HasStockReleaseTombstone(ctx, orderID)
	if err != nil {
		return false, mapPgError(err)
	}
	return exists, nil
}

func (p *pgQueries) ListReservationsByOrderID(ctx context.Context, orderID string, forUpdate bool) ([]StockReservation, error) {
	var (
		rows []db.StockReservation
		err  error
	)
	if forUpdate {
		rows, err = p.q.ListReservationsByOrderIDForUpdate(ctx, orderID)
	} else {
		rows, err = p.q.ListReservationsByOrderID(ctx, orderID)
	}
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]StockReservation, 0, len(rows))
	for _, r := range rows {
		out = append(out, StockReservation{
			ID:                 r.ID,
			ReservationGroupID: r.ReservationGroupID,
			OrderID:            r.OrderID,
			SKUID:              r.SkuID,
			Quantity:           r.Quantity,
			Status:             r.Status,
			CreatedAt:          r.CreatedAt,
			UpdatedAt:          r.UpdatedAt,
		})
	}
	return out, nil
}

func (p *pgQueries) CreateStockReservation(ctx context.Context, r StockReservation) (StockReservation, error) {
	row, err := p.q.CreateStockReservation(ctx, db.CreateStockReservationParams{
		ID:                 r.ID,
		ReservationGroupID: r.ReservationGroupID,
		OrderID:            r.OrderID,
		SkuID:              r.SKUID,
		Quantity:           r.Quantity,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return StockReservation{}, ErrDuplicateReservation
		}
		return StockReservation{}, mapPgError(err)
	}
	return StockReservation{
		ID:                 row.ID,
		ReservationGroupID: row.ReservationGroupID,
		OrderID:            row.OrderID,
		SKUID:              row.SkuID,
		Quantity:           row.Quantity,
		Status:             row.Status,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

func (p *pgQueries) TransitionStockReservation(ctx context.Context, id, toStatus, fromStatus string) (bool, error) {
	n, err := p.q.TransitionStockReservation(ctx, db.TransitionStockReservationParams{
		ID:       id,
		Status:   toStatus,
		Status_2: fromStatus,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) InsertStockHistory(ctx context.Context, h StockHistoryEntry) error {
	return mapPgError(p.q.InsertStockHistory(ctx, db.InsertStockHistoryParams{
		ID:             h.ID,
		SkuID:          h.SKUID,
		OrderID:        h.OrderID,
		ChangeType:     h.ChangeType,
		Delta:          h.Delta,
		StockBefore:    h.StockBefore,
		StockAfter:     h.StockAfter,
		ReservedBefore: h.ReservedBefore,
		ReservedAfter:  h.ReservedAfter,
		ActorID:        h.ActorID,
		Reason:         h.Reason,
	}))
}

func (p *pgQueries) TryInsertRestockHistory(ctx context.Context, h StockHistoryEntry) (bool, error) {
	n, err := p.q.TryInsertRestockHistory(ctx, db.TryInsertRestockHistoryParams{
		ID:             h.ID,
		SkuID:          h.SKUID,
		OrderID:        h.OrderID,
		Delta:          h.Delta,
		StockBefore:    h.StockBefore,
		StockAfter:     h.StockAfter,
		ReservedBefore: h.ReservedBefore,
		ReservedAfter:  h.ReservedAfter,
		ActorID:        h.ActorID,
		Reason:         h.Reason,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) InsertOutboxEvent(ctx context.Context, ev OutboxEvent) error {
	createdAt := ev.CreatedAt
	if createdAt.IsZero() {
		createdAt = AsOfFromContext(ctx)
	}
	return mapPgError(p.q.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
		ID:          ev.ID,
		EventType:   ev.EventType,
		AggregateID: OutboxAggregateKey(ev),
		TraceID:     ev.TraceID,
		Traceparent: ev.Traceparent,
		PayloadJson: ev.PayloadJSON,
		CreatedAt:   createdAt.UTC(),
	}))
}

func (p *pgQueries) CreateProductReview(ctx context.Context, r ProductReview) (ProductReview, error) {
	imgBytes, err := marshalStringSlice(r.ImageURLs)
	if err != nil {
		return ProductReview{}, err
	}
	st := r.Status
	if st == "" {
		st = ReviewStatusPublished
	}
	createdAt := r.CreatedAt
	if createdAt.IsZero() {
		createdAt = AsOfFromContext(ctx)
	}
	row, err := p.q.CreateProductReview(ctx, db.CreateProductReviewParams{
		ID:          r.ID,
		ProductID:   r.ProductID,
		ShopID:      r.ShopID,
		OrderID:     r.OrderID,
		OrderItemID: r.OrderItemID,
		SkuID:       r.SKUID,
		SkuName:     r.SKUName,
		BuyerID:     r.BuyerID,
		BuyerName:   r.BuyerName,
		Rating:      int16(r.Rating),
		Comment:     r.Comment,
		ImageUrls:   imgBytes,
		Status:      st,
		CreatedAt:   createdAt.UTC(),
	})
	if err != nil {
		return ProductReview{}, mapPgError(err)
	}
	return fromDBProductReview(row), nil
}

func (p *pgQueries) ListProductReviews(ctx context.Context, filter ListProductReviewsFilter) ([]ProductReview, int64, error) {
	total, err := p.q.CountProductReviews(ctx, db.CountProductReviewsParams{
		ProductID:    filter.ProductID,
		RatingFilter: filter.RatingFilter,
		HasImages:    filter.HasImages,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []ProductReview{}, 0, nil
	}
	rows, err := p.q.ListProductReviews(ctx, db.ListProductReviewsParams{
		ProductID:    filter.ProductID,
		RatingFilter: filter.RatingFilter,
		HasImages:    filter.HasImages,
		OffsetVal:    filter.Offset,
		LimitVal:     filter.Limit,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]ProductReview, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDBProductReview(row))
	}
	return out, total, nil
}

func (p *pgQueries) GetProductReviewRatingCounts(ctx context.Context, productID string) (map[int32]int32, error) {
	rows, err := p.q.GetProductReviewRatingCounts(ctx, productID)
	if err != nil {
		return nil, mapPgError(err)
	}
	counts := map[int32]int32{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}
	for _, r := range rows {
		counts[r.Rating] = r.Cnt
	}
	return counts, nil
}

func (p *pgQueries) ListReviewsByOrderAndBuyer(ctx context.Context, orderID, buyerID string) ([]ProductReview, error) {
	rows, err := p.q.ListReviewsByOrderAndBuyer(ctx, db.ListReviewsByOrderAndBuyerParams{
		OrderID: orderID,
		BuyerID: buyerID,
	})
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]ProductReview, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDBProductReview(row))
	}
	return out, nil
}

func (p *pgQueries) CreateBannedKeyword(ctx context.Context, kw BannedKeyword) (BannedKeyword, error) {
	createdAt := kw.CreatedAt
	if createdAt.IsZero() {
		createdAt = AsOfFromContext(ctx)
	}
	row, err := p.q.CreateBannedKeyword(ctx, db.CreateBannedKeywordParams{
		ID:             kw.ID,
		Term:           kw.Term,
		NormalizedTerm: kw.NormalizedTerm,
		Category:       kw.Category,
		Action:         kw.Action,
		IsActive:       kw.IsActive,
		CreatedBy:      kw.CreatedBy,
		CreatedAt:      createdAt.UTC(),
	})
	if err != nil {
		return BannedKeyword{}, mapPgError(err)
	}
	return fromDBBannedKeyword(row), nil
}

func (p *pgQueries) GetBannedKeywordByID(ctx context.Context, id string) (BannedKeyword, error) {
	row, err := p.q.GetBannedKeywordByID(ctx, id)
	if err != nil {
		return BannedKeyword{}, mapPgError(err)
	}
	return fromDBBannedKeyword(row), nil
}

func (p *pgQueries) UpdateBannedKeyword(ctx context.Context, kw BannedKeyword) (BannedKeyword, error) {
	row, err := p.q.UpdateBannedKeyword(ctx, db.UpdateBannedKeywordParams{
		ID:             kw.ID,
		Term:           kw.Term,
		NormalizedTerm: kw.NormalizedTerm,
		Category:       kw.Category,
		Action:         kw.Action,
		IsActive:       kw.IsActive,
	})
	if err != nil {
		return BannedKeyword{}, mapPgError(err)
	}
	return fromDBBannedKeyword(row), nil
}

func (p *pgQueries) DeleteBannedKeyword(ctx context.Context, id string) (bool, error) {
	n, err := p.q.DeleteBannedKeyword(ctx, id)
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) ListBannedKeywords(ctx context.Context, filter ListBannedKeywordsFilter) ([]BannedKeyword, int64, error) {
	sqlQuery := ""
	if trimmed := strings.ToLower(strings.TrimSpace(filter.Query)); trimmed != "" {
		sqlQuery = EscapeLikePattern(trimmed)
	}
	total, err := p.q.CountBannedKeywords(ctx, db.CountBannedKeywordsParams{
		IncludeInactive: filter.IncludeInactive,
		ActionFilter:    filter.Action,
		Query:           sqlQuery,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	if total == 0 {
		return []BannedKeyword{}, 0, nil
	}
	rows, err := p.q.ListBannedKeywords(ctx, db.ListBannedKeywordsParams{
		IncludeInactive: filter.IncludeInactive,
		ActionFilter:    filter.Action,
		Query:           sqlQuery,
		OffsetVal:       filter.Offset,
		LimitVal:        filter.Limit,
	})
	if err != nil {
		return nil, 0, mapPgError(err)
	}
	out := make([]BannedKeyword, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDBBannedKeyword(row))
	}
	return out, total, nil
}

func (p *pgQueries) ListActiveBannedKeywords(ctx context.Context) ([]BannedKeyword, error) {
	rows, err := p.q.ListActiveBannedKeywords(ctx)
	if err != nil {
		return nil, mapPgError(err)
	}
	out := make([]BannedKeyword, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDBBannedKeyword(row))
	}
	return out, nil
}

func (p *pgQueries) LockShopSuspension(ctx context.Context, shopID string) error {
	return mapPgError(p.q.LockShopSuspension(ctx, shopID))
}

func (p *pgQueries) GetShopSuspensionState(ctx context.Context, shopID string) (ShopSuspensionState, error) {
	row, err := p.q.GetShopSuspensionState(ctx, shopID)
	if err != nil {
		return ShopSuspensionState{}, mapPgError(err)
	}
	return ShopSuspensionState{
		ShopID:    row.ShopID,
		Suspended: row.Suspended,
		Version:   row.Version,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

func (p *pgQueries) IsShopSuspended(ctx context.Context, shopID string) (bool, error) {
	st, err := p.GetShopSuspensionState(ctx, shopID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return st.Suspended, nil
}

func (p *pgQueries) UpsertShopSuspensionState(ctx context.Context, shopID string, suspended bool, version int64) (ShopSuspensionState, bool, error) {
	row, err := p.q.UpsertShopSuspensionState(ctx, db.UpsertShopSuspensionStateParams{
		ShopID:    shopID,
		Suspended: suspended,
		Version:   version,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ShopSuspensionState{}, false, nil
		}
		return ShopSuspensionState{}, false, mapPgError(err)
	}
	return ShopSuspensionState{
		ShopID:    row.ShopID,
		Suspended: row.Suspended,
		Version:   row.Version,
		UpdatedAt: row.UpdatedAt,
	}, true, nil
}

func (p *pgQueries) SetProductsShopSuspendedByShop(ctx context.Context, shopID string, suspended bool) error {
	return mapPgError(p.q.SetProductsShopSuspendedByShop(ctx, db.SetProductsShopSuspendedByShopParams{
		ShopID:        shopID,
		ShopSuspended: suspended,
	}))
}

func (p *pgQueries) TryInsertProcessedEvent(ctx context.Context, eventID, eventType string) (bool, error) {
	n, err := p.q.TryInsertProcessedEvent(ctx, db.TryInsertProcessedEventParams{
		EventID:   eventID,
		EventType: eventType,
	})
	if err != nil {
		return false, mapPgError(err)
	}
	return n > 0, nil
}

func (p *pgQueries) InsertDeadLetterEvent(ctx context.Context, ev DeadLetterEvent) error {
	return mapPgError(p.q.InsertDeadLetterEvent(ctx, db.InsertDeadLetterEventParams{
		ID:           ev.ID,
		Subject:      ev.Subject,
		EventID:      ev.EventID,
		Payload:      ev.Payload,
		ErrorMessage: ev.ErrorMessage,
		Attempts:     ev.Attempts,
	}))
}

func mapPgError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation:
			return ErrAlreadyExists
		case pgForeignKeyViolation:
			return ErrForeignKeyViolation
		}
	}
	return err
}

func toPgText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{Valid: false}
	}
	return pgtype.Text{String: s, Valid: true}
}

func fromCategoryFields(
	id, name, slug string,
	parentID pgtype.Text,
	commissionRateBps, sortOrder int32,
	isProhibited bool,
	createdAt, updatedAt time.Time,
) Category {
	return Category{
		ID:                id,
		Name:              name,
		Slug:              slug,
		ParentID:          parentID.String,
		CommissionRateBps: commissionRateBps,
		SortOrder:         sortOrder,
		IsProhibited:      isProhibited,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}
}

func fromDBProductReview(r db.ProductReview) ProductReview {
	return ProductReview{
		ID:          r.ID,
		ProductID:   r.ProductID,
		ShopID:      r.ShopID,
		OrderID:     r.OrderID,
		OrderItemID: r.OrderItemID,
		SKUID:       r.SkuID,
		SKUName:     r.SkuName,
		BuyerID:     r.BuyerID,
		BuyerName:   r.BuyerName,
		Rating:      int32(r.Rating),
		Comment:     r.Comment,
		ImageURLs:   unmarshalStringSlice(r.ImageUrls),
		Status:      r.Status,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

func fromDBBannedKeyword(k db.BannedKeyword) BannedKeyword {
	return BannedKeyword{
		ID:             k.ID,
		Term:           k.Term,
		NormalizedTerm: k.NormalizedTerm,
		Category:       k.Category,
		Action:         k.Action,
		IsActive:       k.IsActive,
		CreatedBy:      k.CreatedBy,
		CreatedAt:      k.CreatedAt,
		UpdatedAt:      k.UpdatedAt,
	}
}

func fromDBCategoryFeeSchedule(s db.CategoryFeeSchedule) CategoryFeeSchedule {
	return CategoryFeeSchedule{
		ID:                s.ID,
		Seq:               s.Seq,
		CategoryID:        s.CategoryID,
		CommissionRateBps: s.CommissionRateBps,
		EffectiveFrom:     s.EffectiveFrom,
		CreatedBy:         s.CreatedBy,
		Note:              s.Note,
		CreatedAt:         s.CreatedAt,
	}
}

func fromDBClaimedOutboxEvent(e db.ClaimPendingOutboxEventsRow) OutboxEvent {
	return OutboxEvent{
		ID:           e.ID,
		EventType:    e.EventType,
		AggregateID:  e.AggregateID,
		TraceID:      e.TraceID,
		Traceparent:  e.Traceparent,
		PayloadJSON:  append([]byte(nil), e.PayloadJson...),
		PublishedAt:  e.PublishedAt,
		Attempts:     e.Attempts,
		LastError:    e.LastError,
		CreatedAt:    e.CreatedAt,
		Seq:          e.Seq,
		ClaimedUntil: e.ClaimedUntil,
		ClaimToken:   e.ClaimToken,
	}
}

func fromDBProduct(p db.Product) Product {
	return Product{
		ID:              p.ID,
		ShopID:          p.ShopID,
		CategoryID:      p.CategoryID,
		Name:            p.Name,
		Slug:            p.Slug,
		SearchText:      p.SearchText,
		Description:     p.Description,
		Images:          unmarshalStringSlice(p.Images),
		Status:          p.Status,
		RejectionReason: p.RejectionReason,
		MinPriceVND:     p.MinPriceVnd,
		MaxPriceVND:     p.MaxPriceVnd,
		RatingSum:       p.RatingSum,
		ReviewCount:     p.ReviewCount,
		FlaggedKeywords: unmarshalStringSlice(p.FlaggedKeywords),
		FirstApprovedAt: p.FirstApprovedAt,
		ShopSuspended:   p.ShopSuspended,
		DeletedAt:       p.DeletedAt,
		CreatedAt:       p.CreatedAt,
		UpdatedAt:       p.UpdatedAt,
	}
}

func mergeDBSKU(row db.Sku, base SKU) SKU {
	base.ID = row.ID
	base.ProductID = row.ProductID
	base.SKUCode = row.SkuCode
	base.Name = row.Name
	base.Attributes = unmarshalStringMap(row.Attributes)
	base.PriceVND = row.PriceVnd
	base.Stock = row.Stock
	base.ReservedStock = row.ReservedStock
	base.Version = row.Version
	base.WeightGrams = row.WeightGrams
	base.ImageURL = row.ImageURL
	base.Status = row.Status
	base.CreatedAt = row.CreatedAt
	base.UpdatedAt = row.UpdatedAt
	return base
}

func fromSKUContextFields(
	id, productID, shopID, productName, skuCode, name string,
	attrs []byte, priceVND, minPrice30dVND int64, stock, reservedStock int32, version int64,
	weightGrams int32, imageURL, categoryID string, commissionRateBps int32,
	skuStatus, productStatus string, productDeletedAt *time.Time, productShopSuspended bool,
) SKU {
	if minPrice30dVND <= 0 {
		minPrice30dVND = priceVND
	}
	return SKU{
		ID:                id,
		ProductID:         productID,
		ShopID:            shopID,
		ProductName:       productName,
		SKUCode:           skuCode,
		Name:              name,
		Attributes:        unmarshalStringMap(attrs),
		PriceVND:          priceVND,
		MinPrice30dVND:    minPrice30dVND,
		Stock:             stock,
		ReservedStock:     reservedStock,
		Version:           version,
		WeightGrams:       weightGrams,
		ImageURL:          imageURL,
		CategoryID:        categoryID,
		CommissionRateBps: commissionRateBps,
		Status:            skuStatus,
		ProductStatus:     productStatus,
		ProductDeleted:    productDeletedAt != nil,
		ShopSuspended:     productShopSuspended,
	}
}

func marshalStringSlice(in []string) ([]byte, error) {
	if in == nil {
		in = []string{}
	}
	return json.Marshal(in)
}

func unmarshalStringSlice(b []byte) []string {
	var out []string
	if len(b) == 0 {
		return []string{}
	}
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func marshalStringMap(in map[string]string) ([]byte, error) {
	if in == nil {
		in = map[string]string{}
	}
	return json.Marshal(in)
}

func unmarshalStringMap(b []byte) map[string]string {
	out := map[string]string{}
	if len(b) == 0 {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}
