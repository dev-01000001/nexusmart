-- Allow recreating a RETURN shipment after a previous RETURN shipment was cancelled or returned,
-- while keeping FORWARD active uniqueness unchanged (status <> 'cancelled').
-- Idempotent and safe on repeated startups: inspects pg_indexes and skips DROP/CREATE once applied.
DO $$
DECLARE
    idx_def TEXT;
BEGIN
    SELECT indexdef
      INTO idx_def
      FROM pg_indexes
     WHERE schemaname = current_schema()
       AND tablename = 'shipments'
       AND indexname = 'uq_shipments_order_direction_active';

    IF idx_def IS NULL OR idx_def NOT LIKE '%returned%' THEN
        DROP INDEX IF EXISTS uq_shipments_order_direction_active;
        CREATE UNIQUE INDEX IF NOT EXISTS uq_shipments_order_direction_active
            ON shipments (order_id, direction)
            WHERE status <> 'cancelled' AND NOT (direction = 'RETURN' AND status = 'returned');
    END IF;
END
$$;
