-- Backfill missing double-entry `refund_settle` ledger transactions for phase-0b ZaloPay refunds.
--
-- Background:
-- On phase-0b, ZaloPay refunds were disbursed synchronously via `/v2/refund` and stored with
-- `status = 'completed'` and `provider_refund_id IS NOT NULL`, while posting only the 1-step
-- `refund` accrual ledger transaction (Credit `refund_payable`) and no `refund_settle` transaction
-- (Debit `refund_payable` / Credit `gateway_clearing`).
-- Migration `0005_refund_settlement_and_reconciliation.sql` preserved those rows as `completed`
-- (to avoid double disbursement) without backfilling `refund_settle`, leaving `refund_payable`
-- and `gateway_clearing` permanently inflated by the disbursed ZaloPay refund amount.
--
-- This migration is idempotent and a no-op on fresh databases or databases upgraded from `main`:
-- - Selects only completed ZaloPay refunds (`payments.method = 'ZALOPAY'` and ZaloPay provider)
--   with non-empty `provider_refund_id`, an existing `refund` ledger tx, and no `refund_settle` tx.
-- - Leaves VietQR (`provider_refund_id IS NULL`, downgraded to `pending` by 0005) and COD / other
--   providers untouched.
-- - Uses the exact same `idempotency_key` (`'refund-settle:' || r.id::TEXT`), `reference_type`,
--   entries (Debit `refund_payable`, Credit `gateway_clearing`), and channel/metadata format as
--   `ledger::build_refund_settle_tx` + `ledger::post_transaction`.

DO $$
DECLARE
    rec RECORD;
    v_tx_id UUID;
    v_metadata JSONB;
BEGIN
    FOR rec IN
        SELECT
            r.id AS refund_id,
            r.amount_vnd,
            p.sales_channel,
            p.storefront_shop_id
        FROM refunds r
        JOIN payments p ON p.id = r.payment_id
        WHERE r.status = 'completed'
          AND r.provider_refund_id IS NOT NULL
          AND BTRIM(r.provider_refund_id) <> ''
          AND r.amount_vnd > 0
          AND p.method = 'ZALOPAY'
          AND LOWER(BTRIM(p.provider)) IN ('zalopay', 'zalopay_mock', 'zalopay_sandbox')
          AND EXISTS (
              SELECT 1
              FROM ledger_transactions lt_ref
              WHERE lt_ref.reference_type = 'refund'
                AND lt_ref.reference_id = r.id::TEXT
          )
          AND NOT EXISTS (
              SELECT 1
              FROM ledger_transactions lt_settle
              WHERE lt_settle.reference_type = 'refund_settle'
                AND lt_settle.reference_id = r.id::TEXT
          )
        ORDER BY r.created_at ASC, r.id ASC
    LOOP
        v_tx_id := NULL;
        v_metadata := jsonb_build_object(
            'sales_channel', rec.sales_channel,
            'storefront_shop_id', rec.storefront_shop_id
        );

        INSERT INTO ledger_transactions (
            idempotency_key,
            reference_type,
            reference_id,
            description,
            sales_channel,
            storefront_shop_id,
            metadata
        )
        VALUES (
            'refund-settle:' || rec.refund_id::TEXT,
            'refund_settle',
            rec.refund_id::TEXT,
            'Refund ' || rec.refund_id::TEXT || ' disbursed to buyer',
            rec.sales_channel,
            rec.storefront_shop_id,
            v_metadata
        )
        ON CONFLICT (idempotency_key) DO NOTHING
        RETURNING id INTO v_tx_id;

        IF v_tx_id IS NOT NULL THEN
            INSERT INTO ledger_entries (
                transaction_id,
                account_id,
                debit_vnd,
                credit_vnd,
                sales_channel,
                storefront_shop_id,
                metadata
            )
            VALUES
                (
                    v_tx_id,
                    'refund_payable',
                    rec.amount_vnd,
                    0,
                    rec.sales_channel,
                    rec.storefront_shop_id,
                    v_metadata
                ),
                (
                    v_tx_id,
                    'gateway_clearing',
                    0,
                    rec.amount_vnd,
                    rec.sales_channel,
                    rec.storefront_shop_id,
                    v_metadata
                );
        END IF;
    END LOOP;
END
$$;
