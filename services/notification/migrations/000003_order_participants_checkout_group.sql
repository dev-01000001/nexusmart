ALTER TABLE order_participants
    ADD COLUMN IF NOT EXISTS checkout_group_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_order_participants_checkout_group
    ON order_participants(checkout_group_id)
    WHERE checkout_group_id <> '';
