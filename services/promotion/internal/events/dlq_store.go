package events

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const consumerMigrationAdvisoryLockKey int64 = 7042002

// DeadLetterRecord represents a dead-lettered JetStream message persisted to PostgreSQL.
type DeadLetterRecord struct {
	ID           string
	Subject      string
	EventID      string
	Payload      []byte
	ErrorMessage string
	Attempts     int
}

// DeadLetterStore persists dead-lettered messages locally alongside NATS DLQ stream publishing.
type DeadLetterStore interface {
	InsertDeadLetterEvent(ctx context.Context, rec DeadLetterRecord) error
}

// consumerMigrationLocker optionally serializes one-time legacy JetStream consumer upgrades
// across concurrent replicas when backed by PostgreSQL.
type consumerMigrationLocker interface {
	WithConsumerMigrationLock(ctx context.Context, fn func(context.Context) error) error
}

// PostgresDeadLetterStore persists dead-lettered events into promotion_db.dead_letter_events.
type PostgresDeadLetterStore struct {
	pool *pgxpool.Pool
}

// NewPostgresDeadLetterStore builds a PostgreSQL-backed DeadLetterStore.
func NewPostgresDeadLetterStore(pool *pgxpool.Pool) *PostgresDeadLetterStore {
	if pool == nil {
		return nil
	}
	return &PostgresDeadLetterStore{pool: pool}
}

// InsertDeadLetterEvent inserts a dead-lettered event row into dead_letter_events.
func (s *PostgresDeadLetterStore) InsertDeadLetterEvent(ctx context.Context, rec DeadLetterRecord) error {
	if s == nil || s.pool == nil {
		return errors.New("postgres dead letter store pool is nil")
	}
	id := strings.TrimSpace(rec.ID)
	if id == "" {
		id = uuid.NewString()
	}
	payload := rec.Payload
	if payload == nil {
		payload = []byte{}
	}
	const q = `
		INSERT INTO dead_letter_events (id, subject, event_id, payload, error_message, attempts)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	if _, err := s.pool.Exec(ctx, q, id, rec.Subject, rec.EventID, payload, rec.ErrorMessage, rec.Attempts); err != nil {
		return fmt.Errorf("insert dead_letter_events: %w", err)
	}
	return nil
}

// WithConsumerMigrationLock runs fn under a PostgreSQL transaction-scoped advisory lock so
// concurrent replicas upgrading a legacy non-queue durable consumer do not race on delete/recreate.
func (s *PostgresDeadLetterStore) WithConsumerMigrationLock(ctx context.Context, fn func(context.Context) error) error {
	if s == nil || s.pool == nil {
		return fn(ctx)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin consumer migration lock tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, consumerMigrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire consumer migration advisory lock: %w", err)
	}
	if err := fn(ctx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit consumer migration lock tx: %w", err)
	}
	return nil
}
