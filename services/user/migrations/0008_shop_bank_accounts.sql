-- 0008_shop_bank_accounts.sql: Seller shop bank accounts with OTP verification,
-- admin review fallback, single verified account invariant, payout lock (UC-S17),
-- and created_at indexes for platform user/shop statistics (UC-A06).

CREATE TABLE IF NOT EXISTS shop_bank_accounts (
    id UUID PRIMARY KEY,
    shop_id UUID NOT NULL REFERENCES shops(id) ON DELETE CASCADE,
    bank_code TEXT NOT NULL,
    bank_name TEXT NOT NULL,
    account_number TEXT NOT NULL,
    account_number_masked TEXT NOT NULL,
    account_holder_name TEXT NOT NULL,
    holder_name_matched BOOLEAN NOT NULL DEFAULT FALSE,
    status TEXT NOT NULL DEFAULT 'PENDING_OTP',
    review_note TEXT NOT NULL DEFAULT '',
    otp_hash TEXT NOT NULL DEFAULT '',
    otp_expires_at TIMESTAMPTZ,
    otp_attempts INTEGER NOT NULL DEFAULT 0,
    payout_locked_until TIMESTAMPTZ,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_shop_bank_accounts_status CHECK (
        status IN ('PENDING_OTP', 'PENDING_ADMIN_REVIEW', 'VERIFIED', 'REJECTED', 'SUPERSEDED')
    )
);

CREATE INDEX IF NOT EXISTS idx_shop_bank_accounts_shop_created
    ON shop_bank_accounts (shop_id, created_at DESC);

CREATE UNIQUE INDEX IF NOT EXISTS idx_shop_bank_accounts_one_verified
    ON shop_bank_accounts (shop_id)
    WHERE status = 'VERIFIED';

CREATE UNIQUE INDEX IF NOT EXISTS idx_shop_bank_accounts_one_pending_otp
    ON shop_bank_accounts (shop_id)
    WHERE status = 'PENDING_OTP';

CREATE INDEX IF NOT EXISTS idx_users_created_at ON users (created_at);
CREATE INDEX IF NOT EXISTS idx_shops_created_at ON shops (created_at);
