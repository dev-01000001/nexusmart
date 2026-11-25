ALTER TABLE shipments ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'forward' CHECK (direction IN ('forward', 'return'));
ALTER TABLE shipments ADD COLUMN IF NOT EXISTS return_request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE shipments DROP CONSTRAINT IF EXISTS shipments_order_id_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_shipments_order_direction ON shipments (order_id, direction);
CREATE INDEX IF NOT EXISTS idx_shipments_return_request_id ON shipments (return_request_id) WHERE return_request_id <> '';
