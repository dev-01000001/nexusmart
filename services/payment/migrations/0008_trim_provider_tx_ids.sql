-- Wave 2a review round 2 fixes (P0-1, P1-1):
-- 1. Safely trim leading/trailing whitespace on legacy provider_tx_id values across
--    payments, webhook_events, reconciliation_queue, and reconciliation_items.
--    Any row whose trimmed value would be empty or violate a UNIQUE constraint is
--    skipped with RAISE WARNING so the migration never fails on dirty legacy data.
-- 2. Clear provider_tx_id on failed payments so failed webhooks never occupy
--    payments.provider_tx_id UNIQUE constraint (P1-1).

UPDATE payments
SET provider_tx_id = NULL,
    updated_at = NOW()
WHERE status = 'failed'
  AND provider_tx_id IS NOT NULL;

DO $$
DECLARE
    rec RECORD;
    v_trimmed TEXT;
BEGIN
    -- 1. payments.provider_tx_id (UNIQUE)
    FOR rec IN
        SELECT id, provider_tx_id
        FROM payments
        WHERE provider_tx_id IS NOT NULL
          AND provider_tx_id <> BTRIM(provider_tx_id)
    LOOP
        v_trimmed := BTRIM(rec.provider_tx_id);
        IF v_trimmed = '' THEN
            RAISE WARNING 'Skipping blank payments.provider_tx_id on payment %', rec.id;
            CONTINUE;
        END IF;
        BEGIN
            UPDATE payments
            SET provider_tx_id = v_trimmed,
                updated_at = NOW()
            WHERE id = rec.id;
        EXCEPTION WHEN unique_violation THEN
            RAISE WARNING 'Skipping trim of payments.provider_tx_id on payment % (duplicate after trim: %)', rec.id, v_trimmed;
        END;
    END LOOP;

    -- 2. webhook_events.provider_tx_id (UNIQUE)
    FOR rec IN
        SELECT id, provider_tx_id
        FROM webhook_events
        WHERE provider_tx_id <> BTRIM(provider_tx_id)
    LOOP
        v_trimmed := BTRIM(rec.provider_tx_id);
        IF v_trimmed = '' THEN
            RAISE WARNING 'Skipping blank webhook_events.provider_tx_id on event %', rec.id;
            CONTINUE;
        END IF;
        BEGIN
            UPDATE webhook_events
            SET provider_tx_id = v_trimmed
            WHERE id = rec.id;
        EXCEPTION WHEN unique_violation THEN
            RAISE WARNING 'Skipping trim of webhook_events.provider_tx_id on event % (duplicate after trim: %)', rec.id, v_trimmed;
        END;
    END LOOP;

    -- 3. reconciliation_queue.provider_tx_id (conditional UNIQUE index from 0004)
    FOR rec IN
        SELECT id, provider_tx_id
        FROM reconciliation_queue
        WHERE provider_tx_id <> BTRIM(provider_tx_id)
    LOOP
        v_trimmed := BTRIM(rec.provider_tx_id);
        IF v_trimmed = '' THEN
            RAISE WARNING 'Skipping blank reconciliation_queue.provider_tx_id on item %', rec.id;
            CONTINUE;
        END IF;
        BEGIN
            UPDATE reconciliation_queue
            SET provider_tx_id = v_trimmed
            WHERE id = rec.id;
        EXCEPTION WHEN unique_violation THEN
            RAISE WARNING 'Skipping trim of reconciliation_queue.provider_tx_id on item % (duplicate after trim: %)', rec.id, v_trimmed;
        END;
    END LOOP;
END
$$;

-- 4. reconciliation_items.provider_tx_id (non-unique)
UPDATE reconciliation_items
SET provider_tx_id = BTRIM(provider_tx_id)
WHERE provider_tx_id IS NOT NULL
  AND provider_tx_id <> BTRIM(provider_tx_id)
  AND BTRIM(provider_tx_id) <> '';
