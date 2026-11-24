package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexusmart/core/pkg/common/secretbox"
)

const (
	// bankAccountBackfillLockID is a fixed Postgres session advisory lock key ("NMUSBANK")
	// serializing startup bank-account encryption backfill across concurrent service replicas.
	bankAccountBackfillLockID int64 = 0x4e4d555342414e4b
	defaultBackfillBatchSize  int   = 100
)

// BackfillShopBankAccountEncryption verifies that box can decrypt all existing encrypted
// shop_bank_accounts rows and idempotently encrypts any legacy plaintext rows in bounded
// transactional batches under a PostgreSQL session advisory lock.
//
// If any existing encrypted row cannot be decrypted with box (e.g. wrong BANK_ACCOUNT_KEY),
// it fails immediately before modifying any row so the service refuses to start without
// overwriting data. Errors never include key material, plaintext account numbers, or ciphertext.
func BackfillShopBankAccountEncryption(ctx context.Context, pool *pgxpool.Pool, box *secretbox.Box, batchSize int) (int, error) {
	if pool == nil {
		return 0, errors.New("postgres pool is nil")
	}
	if box == nil {
		return 0, errors.New("BANK_ACCOUNT_KEY secretbox is not initialized")
	}
	if batchSize <= 0 {
		batchSize = defaultBackfillBatchSize
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire connection for bank account backfill: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, bankAccountBackfillLockID); err != nil {
		return 0, fmt.Errorf("acquire bank account backfill advisory lock: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, unlockErr := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, bankAccountBackfillLockID); unlockErr != nil {
			// Ensure a connection still holding a session advisory lock is never returned to the pool.
			_ = conn.Conn().Close(unlockCtx)
		}
	}()

	// Phase 1: Verify key compatibility against every already-encrypted row before touching any row.
	if err := verifyExistingEncryptedBankAccounts(ctx, conn, box, batchSize); err != nil {
		return 0, err
	}

	// Phase 2: Encrypt remaining legacy plaintext rows in bounded transactional batches.
	totalBackfilled := 0
	for {
		batchCount, err := backfillBankAccountBatch(ctx, conn, box, batchSize)
		if err != nil {
			return totalBackfilled, err
		}
		if batchCount == 0 {
			break
		}
		totalBackfilled += batchCount
	}

	return totalBackfilled, nil
}

func verifyExistingEncryptedBankAccounts(ctx context.Context, conn *pgxpool.Conn, box *secretbox.Box, batchSize int) error {
	lastID := ""
	for {
		var (
			rows pgx.Rows
			err  error
		)
		if lastID == "" {
			rows, err = conn.Query(ctx, `
				SELECT id::text, account_number, account_number_enc
				FROM shop_bank_accounts
				WHERE account_number_enc IS NOT NULL
				ORDER BY id ASC
				LIMIT $1
			`, batchSize)
		} else {
			rows, err = conn.Query(ctx, `
				SELECT id::text, account_number, account_number_enc
				FROM shop_bank_accounts
				WHERE account_number_enc IS NOT NULL
				  AND id > $1::uuid
				ORDER BY id ASC
				LIMIT $2
			`, lastID, batchSize)
		}
		if err != nil {
			return fmt.Errorf("query existing encrypted bank accounts: %w", err)
		}

		count := 0
		for rows.Next() {
			var (
				id    string
				plain string
				enc   []byte
			)
			if scanErr := rows.Scan(&id, &plain, &enc); scanErr != nil {
				rows.Close()
				return fmt.Errorf("scan existing encrypted bank account: %w", scanErr)
			}
			decrypted, decErr := box.Decrypt(enc)
			if decErr != nil {
				rows.Close()
				return fmt.Errorf("verify BANK_ACCOUNT_KEY on bank account %s: %w", id, decErr)
			}
			if plain != "" && decrypted != plain {
				rows.Close()
				return fmt.Errorf("bank account %s has inconsistent plaintext and encrypted values", id)
			}
			lastID = id
			count++
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return fmt.Errorf("iterate existing encrypted bank accounts: %w", rowsErr)
		}
		rows.Close()

		if count < batchSize {
			return nil
		}
	}
}

func backfillBankAccountBatch(ctx context.Context, conn *pgxpool.Conn, box *secretbox.Box, batchSize int) (int, error) {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin bank account backfill batch: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	type pendingRow struct {
		id        string
		plaintext string
		enc       []byte
	}

	rows, err := tx.Query(ctx, `
		SELECT id::text, account_number, account_number_enc
		FROM shop_bank_accounts
		WHERE account_number <> ''
		ORDER BY id ASC
		LIMIT $1
		FOR UPDATE
	`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("select unencrypted bank accounts: %w", err)
	}

	batch := make([]pendingRow, 0, batchSize)
	for rows.Next() {
		var r pendingRow
		if scanErr := rows.Scan(&r.id, &r.plaintext, &r.enc); scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("scan unencrypted bank account: %w", scanErr)
		}
		batch = append(batch, r)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate unencrypted bank accounts: %w", rowsErr)
	}
	rows.Close()

	if len(batch) == 0 {
		return 0, nil
	}

	for _, r := range batch {
		if r.enc != nil {
			decrypted, decErr := box.Decrypt(r.enc)
			if decErr != nil {
				return 0, fmt.Errorf("verify BANK_ACCOUNT_KEY on bank account %s: %w", r.id, decErr)
			}
			if decrypted != r.plaintext {
				return 0, fmt.Errorf("bank account %s has inconsistent plaintext and encrypted values", r.id)
			}
			tag, execErr := tx.Exec(ctx, `
				UPDATE shop_bank_accounts
				SET account_number = ''
				WHERE id = $1::uuid
				  AND account_number <> ''
			`, r.id)
			if execErr != nil {
				return 0, fmt.Errorf("clear plaintext bank account %s: %w", r.id, execErr)
			}
			if tag.RowsAffected() != 1 {
				return 0, fmt.Errorf("unexpected row count clearing plaintext bank account %s", r.id)
			}
			continue
		}

		ct, encErr := box.Encrypt(r.plaintext)
		if encErr != nil {
			return 0, fmt.Errorf("encrypt bank account %s: %w", r.id, encErr)
		}
		tag, execErr := tx.Exec(ctx, `
			UPDATE shop_bank_accounts
			SET account_number_enc = $2,
			    account_number = ''
			WHERE id = $1::uuid
			  AND account_number <> ''
			  AND account_number_enc IS NULL
		`, r.id, ct)
		if execErr != nil {
			return 0, fmt.Errorf("update encrypted bank account %s: %w", r.id, execErr)
		}
		if tag.RowsAffected() != 1 {
			return 0, fmt.Errorf("unexpected row count encrypting bank account %s", r.id)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit bank account backfill batch: %w", err)
	}
	return len(batch), nil
}
