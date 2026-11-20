package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	pool *pgxpool.Pool
}

// NewSQLStore constructs a SQLStore from a pgxpool.Pool.
func NewSQLStore(pool *pgxpool.Pool) *SQLStore {
	return &SQLStore{
		Queries: New(pool),
		pool:    pool,
	}
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
