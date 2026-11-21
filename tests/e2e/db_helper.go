package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// SetDomainCreatedAtOffset adjusts domains.created_at in user_db relative to database NOW()
// so E2E tests can deterministically exercise time-window rules (e.g. 48-hour PENDING hold).
func SetDomainCreatedAtOffset(ctx context.Context, databaseURL, domainID string, offset time.Duration) error {
	dsn := strings.TrimSpace(databaseURL)
	if dsn == "" {
		return fmt.Errorf("user database URL is empty")
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect user_db: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	tag, err := conn.Exec(
		ctx,
		`UPDATE domains SET created_at = NOW() + ($1 * INTERVAL '1 second') WHERE id = $2`,
		int64(offset.Seconds()),
		domainID,
	)
	if err != nil {
		return fmt.Errorf("update domain %s created_at: %w", domainID, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update domain %s created_at: expected 1 row affected, got %d", domainID, tag.RowsAffected())
	}
	return nil
}
