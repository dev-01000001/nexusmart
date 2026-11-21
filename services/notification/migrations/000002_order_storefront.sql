-- Storefront (site) of an order, cached from order.* events so payment/shipment notifications
-- can link to the order page on the site where it was placed. Empty for MARKETPLACE orders.
ALTER TABLE order_participants ADD COLUMN IF NOT EXISTS storefront_shop_id TEXT NOT NULL DEFAULT '';
