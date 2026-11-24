package db

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/pkg/common/jetstream"
)

// Store wraps sqlc Queries with transaction and healthcheck support.
type Store interface {
	Querier
	WithTx(ctx context.Context, fn func(q Querier) error) error
	Ping(ctx context.Context) error
}

// SQLStore is a PostgreSQL-backed Store using pgxpool.
type SQLStore struct {
	*Queries
	pool                 *pgxpool.Pool
	outboxLease          atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxLease
	outboxPublishTimeout atomic.Int64 // nanoseconds; defaults to jetstream.DefaultOutboxPublishTimeout
	clockMu              sync.RWMutex
	outboxNowFn          func() time.Time
	logger               *slog.Logger
}

// NewSQLStore constructs a SQLStore from a pgxpool.Pool.
func NewSQLStore(pool *pgxpool.Pool) *SQLStore {
	s := &SQLStore{
		Queries: New(pool),
		pool:    pool,
	}
	s.outboxLease.Store(int64(jetstream.DefaultOutboxLease))
	s.outboxPublishTimeout.Store(int64(jetstream.DefaultOutboxPublishTimeout))
	return s
}

// SetLogger configures the structured logger used for outbox poison-event alerts.
func (s *SQLStore) SetLogger(l *slog.Logger) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	s.logger = l
}

// SetOutboxLease overrides the outbox claim lease duration (defaults to jetstream.DefaultOutboxLease).
func (s *SQLStore) SetOutboxLease(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxLease
	}
	s.outboxLease.Store(int64(d))
}

// SetOutboxPublishTimeout overrides the max per-event publish timeout budget.
func (s *SQLStore) SetOutboxPublishTimeout(d time.Duration) {
	if d <= 0 {
		d = jetstream.DefaultOutboxPublishTimeout
	}
	s.outboxPublishTimeout.Store(int64(d))
}

// SetOutboxClock overrides the Go clock used for lease-budget checks.
func (s *SQLStore) SetOutboxClock(fn func() time.Time) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	s.outboxNowFn = fn
}

// WithTx executes fn inside a PostgreSQL transaction.
func (s *SQLStore) WithTx(ctx context.Context, fn func(q Querier) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Ping verifies database connectivity.
func (s *SQLStore) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
