-- Persist the last ZaloPay /v2/query observation (`return_code`, `is_processing`) on `payments`
-- so post-expiry late-payment sweeps durably distinguish unpaid/abandoned orders
-- (`return_code = 3, is_processing = false`) from still-processing orders (`is_processing = true`)
-- or transport/unknown errors across restarts and multiple replicas.
ALTER TABLE payments
    ADD COLUMN IF NOT EXISTS provider_last_query_return_code INTEGER NULL,
    ADD COLUMN IF NOT EXISTS provider_last_query_is_processing BOOLEAN NULL;
