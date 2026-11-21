-- name: CreateCheckoutGroup :one
INSERT INTO checkout_groups (
    id, buyer_id, idempotency_key, status, payment_method, total_payable_vnd,
    sales_channel, storefront_shop_id
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8
)
RETURNING *;

-- name: GetActiveCheckoutGroupByIdempotencyKey :one
SELECT *
FROM checkout_groups
WHERE buyer_id = $1
  AND idempotency_key = $2
  AND status <> 'FAILED'
LIMIT 1;

-- name: GetCheckoutGroupByID :one
SELECT *
FROM checkout_groups
WHERE id = $1;

-- name: CompleteCheckoutGroup :exec
UPDATE checkout_groups
SET status = 'COMPLETED',
    payment_id = $2,
    payment_status = $3,
    qr_url = $4,
    qr_code = $5,
    payment_expires_at = $6,
    total_payable_vnd = $7,
    response_snapshot_json = $8,
    updated_at = NOW()
WHERE id = $1;

-- name: FailCheckoutGroup :exec
UPDATE checkout_groups
SET status = 'FAILED',
    payment_status = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateCheckoutGroupPaymentStatus :exec
UPDATE checkout_groups
SET payment_status = $2,
    payment_id = CASE WHEN sqlc.arg(payment_id)::text <> '' THEN sqlc.arg(payment_id)::text ELSE payment_id END,
    updated_at = NOW()
WHERE id = $1;

-- name: InsertOrder :one
INSERT INTO orders (
    id, order_code, checkout_group_id, buyer_id, shop_id, status,
    payment_method, payment_id, payment_status,
    subtotal_vnd, flash_sale_discount_vnd, shop_voucher_discount_vnd,
    platform_voucher_discount_vnd, freeship_discount_vnd, coins_discount_vnd,
    shipping_fee_vnd, commission_vnd, total_payable_vnd,
    carrier_code, service_code, tracking_code, label_url, cancel_reason,
    shipping_address_json, payment_expires_at,
    sales_channel, storefront_shop_id
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18,
    $19, $20, $21, $22, $23,
    $24, $25,
    $26, $27
)
RETURNING *;

-- name: InsertOrderItem :one
INSERT INTO order_items (
    id, order_id, sku_id, product_id, product_name, sku_name,
    unit_price_vnd, effective_unit_price_vnd, quantity, line_total_vnd,
    weight_grams, flash_sale_item_id, commission_rate_bps
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13
)
RETURNING *;

-- name: InsertTimelineEvent :one
INSERT INTO order_timeline_events (
    id, order_id, from_status, to_status, actor_id, actor_role, note
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetOrderByID :one
SELECT *
FROM orders
WHERE id = $1;

-- name: GetOrderByIDForUpdate :one
SELECT *
FROM orders
WHERE id = $1
FOR UPDATE;

-- name: ListOrdersByCheckoutGroup :many
SELECT *
FROM orders
WHERE checkout_group_id = $1
ORDER BY shop_id ASC, created_at ASC;

-- name: ListOrdersByCheckoutGroupForUpdate :many
SELECT *
FROM orders
WHERE checkout_group_id = $1
ORDER BY shop_id ASC, created_at ASC, id ASC
FOR UPDATE;

-- name: ListOrderItemsByOrderID :many
SELECT *
FROM order_items
WHERE order_id = $1
ORDER BY sku_id ASC;

-- name: ListOrderTimelineByOrderID :many
SELECT *
FROM order_timeline_events
WHERE order_id = $1
ORDER BY created_at ASC, id ASC;

-- name: ListOrdersFiltered :many
SELECT *
FROM orders
WHERE (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(sales_channel)::text = '' OR sales_channel = sqlc.arg(sales_channel)::text)
  AND (sqlc.arg(storefront_shop_id)::text = '' OR storefront_shop_id = sqlc.arg(storefront_shop_id)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountOrdersFiltered :one
SELECT COUNT(*)
FROM orders
WHERE (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(sales_channel)::text = '' OR sales_channel = sqlc.arg(sales_channel)::text)
  AND (sqlc.arg(storefront_shop_id)::text = '' OR storefront_shop_id = sqlc.arg(storefront_shop_id)::text);

-- name: UpdateOrderPaymentInfoByGroup :exec
UPDATE orders
SET payment_id = $2,
    payment_status = $3,
    payment_expires_at = COALESCE($4, payment_expires_at),
    updated_at = NOW()
WHERE checkout_group_id = $1;

-- name: UpdateOrderStatus :one
UPDATE orders
SET status = $2,
    cancel_reason = CASE WHEN sqlc.arg(cancel_reason)::text <> '' THEN sqlc.arg(cancel_reason)::text ELSE cancel_reason END,
    payment_status = CASE WHEN sqlc.arg(payment_status)::text <> '' THEN sqlc.arg(payment_status)::text ELSE payment_status END,
    payment_id = CASE WHEN sqlc.arg(payment_id)::text <> '' THEN sqlc.arg(payment_id)::text ELSE payment_id END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderShipped :one
UPDATE orders
SET status = 'SHIPPING',
    carrier_code = $2,
    service_code = $3,
    tracking_code = $4,
    label_url = $5,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderDelivered :one
UPDATE orders
SET status = 'DELIVERED',
    tracking_code = CASE WHEN sqlc.arg(tracking_code)::text <> '' THEN sqlc.arg(tracking_code)::text ELSE tracking_code END,
    delivered_at = NOW(),
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderCompleted :one
UPDATE orders
SET status = 'COMPLETED',
    completed_at = NOW(),
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderTrackingCode :exec
UPDATE orders
SET tracking_code = CASE WHEN sqlc.arg(tracking_code)::text <> '' THEN sqlc.arg(tracking_code)::text ELSE tracking_code END,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateOrderPaymentStatus :exec
UPDATE orders
SET payment_status = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: InsertReturnRequest :one
INSERT INTO return_requests (
    id, order_id, buyer_id, shop_id, items_json, reason, evidence_urls_json, status,
    seller_note, reverse_tracking_code, reverse_label_url, refund_amount_vnd, refund_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    $9, $10, $11, $12, $13
)
RETURNING *;

-- name: GetReturnRequestByID :one
SELECT *
FROM return_requests
WHERE id = $1;

-- name: GetReturnRequestByIDForUpdate :one
SELECT *
FROM return_requests
WHERE id = $1
FOR UPDATE;

-- name: GetLatestReturnRequestByOrderID :one
SELECT *
FROM return_requests
WHERE order_id = $1
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ListReturnRequestsFiltered :many
SELECT *
FROM return_requests
WHERE (sqlc.arg(order_id)::text = '' OR order_id = sqlc.arg(order_id)::text)
  AND (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountReturnRequestsFiltered :one
SELECT COUNT(*)
FROM return_requests
WHERE (sqlc.arg(order_id)::text = '' OR order_id = sqlc.arg(order_id)::text)
  AND (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

-- name: UpdateReturnRequest :one
UPDATE return_requests
SET status = CASE WHEN sqlc.arg(status)::text <> '' THEN sqlc.arg(status)::text ELSE status END,
    seller_note = CASE WHEN sqlc.arg(seller_note)::text <> '' THEN sqlc.arg(seller_note)::text ELSE seller_note END,
    reverse_tracking_code = CASE WHEN sqlc.arg(reverse_tracking_code)::text <> '' THEN sqlc.arg(reverse_tracking_code)::text ELSE reverse_tracking_code END,
    reverse_label_url = CASE WHEN sqlc.arg(reverse_label_url)::text <> '' THEN sqlc.arg(reverse_label_url)::text ELSE reverse_label_url END,
    refund_amount_vnd = CASE WHEN sqlc.arg(refund_amount_vnd)::bigint > 0 THEN sqlc.arg(refund_amount_vnd)::bigint ELSE refund_amount_vnd END,
    refund_id = CASE WHEN sqlc.arg(refund_id)::text <> '' THEN sqlc.arg(refund_id)::text ELSE refund_id END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: InsertComplaint :one
INSERT INTO complaints (
    id, return_request_id, order_id, buyer_id, shop_id, reason, evidence_urls_json, status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;

-- name: GetComplaintByID :one
SELECT *
FROM complaints
WHERE id = $1;

-- name: GetComplaintByIDForUpdate :one
SELECT *
FROM complaints
WHERE id = $1
FOR UPDATE;

-- name: GetComplaintByReturnRequestID :one
SELECT *
FROM complaints
WHERE return_request_id = $1;

-- name: UpdateComplaintDetails :one
UPDATE complaints
SET reason = CASE WHEN sqlc.arg(reason)::text <> '' THEN sqlc.arg(reason)::text ELSE reason END,
    evidence_urls_json = CASE WHEN sqlc.arg(update_evidence)::boolean THEN sqlc.arg(evidence_urls_json)::jsonb ELSE evidence_urls_json END
WHERE id = $1
RETURNING *;

-- name: ResolveComplaintRow :one
UPDATE complaints
SET status = $2,
    resolution = $3,
    admin_note = $4,
    resolved_by = $5,
    resolved_at = NOW()
WHERE id = $1
RETURNING *;

-- name: ListComplaintsFiltered :many
SELECT *
FROM complaints
WHERE (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(order_id)::text = '' OR order_id = sqlc.arg(order_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountComplaintsFiltered :one
SELECT COUNT(*)
FROM complaints
WHERE (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(order_id)::text = '' OR order_id = sqlc.arg(order_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text);

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (
    id, event_type, trace_id, traceparent, payload_json
) VALUES (
    $1, $2, $3, $4, $5
);

-- name: LockPendingOutboxEvents :many
SELECT *
FROM outbox_events
WHERE published_at IS NULL
ORDER BY created_at ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: MarkOutboxEventPublished :exec
UPDATE outbox_events
SET published_at = NOW(),
    attempts = attempts + 1,
    last_error = ''
WHERE id = $1;

-- name: MarkOutboxEventAttemptFailed :exec
UPDATE outbox_events
SET attempts = attempts + 1,
    last_error = $2
WHERE id = $1;

-- name: TryMarkEventProcessed :execrows
INSERT INTO processed_events (event_id, event_type)
VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING;

-- name: IsEventProcessed :one
SELECT EXISTS (
    SELECT 1 FROM processed_events WHERE event_id = $1
);

-- name: InsertDeadLetterEvent :exec
INSERT INTO dead_letter_events (
    id, subject, event_id, payload, error_message, attempts
) VALUES (
    $1, $2, $3, $4, $5, $6
);

-- name: LockExpiredPendingOrders :many
SELECT id, checkout_group_id, payment_id
FROM orders
WHERE status = 'PENDING'
  AND payment_method IN ('VIETQR', 'ZALOPAY')
  AND payment_expires_at IS NOT NULL
  AND payment_expires_at <= NOW()
ORDER BY payment_expires_at ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;
