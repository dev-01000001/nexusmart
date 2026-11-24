-- 0007_shop_bank_account_encryption.sql: Encrypt shop bank account numbers at rest (D5).
-- New rows write AES-256-GCM ciphertext to account_number_enc and leave account_number = ''.
-- Legacy plaintext rows are backfilled idempotently at startup under a Postgres advisory lock.

ALTER TABLE shop_bank_accounts
    ADD COLUMN IF NOT EXISTS account_number_enc BYTEA;

ALTER TABLE shop_bank_accounts
    ALTER COLUMN account_number SET DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_shop_bank_accounts_unencrypted
    ON shop_bank_accounts (id)
    WHERE account_number <> '';
