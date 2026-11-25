-- 000013_release_legacy_flash_sale_reservations.sql:
-- Release all legacy ACTIVE phase-0b flash sale pre-lock reservations and reset
-- skus.flash_sale_reserved_stock = 0 now that flash sale stock is reserved at checkout.

UPDATE flash_sale_stock_reservations
SET status = 'RELEASED',
    updated_at = NOW()
WHERE status = 'ACTIVE';

UPDATE skus
SET flash_sale_reserved_stock = 0,
    status = CASE
        WHEN status = 'OUT_OF_STOCK' AND stock > 0 THEN 'ACTIVE'
        ELSE status
    END,
    version = version + 1,
    updated_at = NOW()
WHERE flash_sale_reserved_stock <> 0;

UPDATE products p
SET status = 'APPROVED',
    updated_at = NOW()
WHERE p.status = 'OUT_OF_STOCK'
  AND p.deleted_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM skus s
      WHERE s.product_id = p.id
        AND s.status = 'ACTIVE'
        AND s.stock > 0
  );
