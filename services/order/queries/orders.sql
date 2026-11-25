-- name: CreateCheckoutGroup :one
INSERT INTO checkout_groups (
    id, buyer_id, idempotency_key, request_hash, status, payment_method, total_payable_vnd,
    sales_channel, storefront_shop_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9
)
RETURNING *;

-- name: GetCheckoutGroupForIdempotency :one
SELECT g.*,
       EXISTS (SELECT 1 FROM orders o WHERE o.checkout_group_id = g.id) AS has_orders
FROM checkout_groups g
WHERE g.buyer_id = $1
  AND g.idempotency_key = $2
  AND (
      g.status <> 'FAILED'
      OR g.needs_cleanup = TRUE
      OR EXISTS (SELECT 1 FROM orders o WHERE o.checkout_group_id = g.id)
  )
ORDER BY (g.status <> 'FAILED') DESC,
         EXISTS (SELECT 1 FROM orders o WHERE o.checkout_group_id = g.id) DESC,
         g.needs_cleanup DESC,
         g.created_at DESC,
         g.id DESC
LIMIT 1;

-- name: GetCheckoutGroupByID :one
SELECT *
FROM checkout_groups
WHERE id = $1;

-- name: GetCheckoutGroupByIDForUpdate :one
SELECT *
FROM checkout_groups
WHERE id = $1
FOR UPDATE;

-- name: GetCheckoutGroupByPaymentID :one
SELECT *
FROM checkout_groups
WHERE payment_id = $1
  AND status <> 'FAILED'
ORDER BY created_at DESC
LIMIT 1;

-- name: CompleteCheckoutGroup :one
UPDATE checkout_groups
SET status = CASE
        WHEN status = 'IN_PROGRESS' THEN sqlc.arg(target_status)::text
        ELSE status
    END,
    payment_id = CASE
        WHEN payment_id = '' THEN $2
        ELSE payment_id
    END,
    payment_status = CASE
        WHEN payment_status IN ('', 'PENDING') THEN $3
        ELSE payment_status
    END,
    qr_url = CASE
        WHEN payment_status IN ('', 'PENDING') AND qr_url = '' THEN $4
        ELSE qr_url
    END,
    qr_code = CASE
        WHEN payment_status IN ('', 'PENDING') AND qr_code = '' THEN $5
        ELSE qr_code
    END,
    pay_url = CASE
        WHEN payment_status IN ('', 'PENDING') AND pay_url = '' THEN sqlc.arg(pay_url)::text
        ELSE pay_url
    END,
    payment_expires_at = CASE
        WHEN payment_status IN ('', 'PENDING') THEN COALESCE($6, payment_expires_at)
        ELSE payment_expires_at
    END,
    total_payable_vnd = $7,
    response_snapshot_json = CASE
        WHEN sqlc.arg(target_status)::text = 'FAILED' THEN NULL
        WHEN sqlc.narg(response_snapshot_json)::jsonb IS NOT NULL THEN sqlc.narg(response_snapshot_json)::jsonb
        ELSE response_snapshot_json
    END,
    pending_stock_confirm = CASE
        WHEN sqlc.arg(target_status)::text = 'FAILED' THEN FALSE
        ELSE pending_stock_confirm
    END,
    needs_cleanup = FALSE,
    updated_at = NOW()
WHERE id = $1
  AND status = 'IN_PROGRESS'
RETURNING *;

-- name: SetCheckoutGroupWarnings :exec
UPDATE checkout_groups
SET warnings_json = $2,
    updated_at = NOW()
WHERE id = $1
  AND status = 'IN_PROGRESS';

-- name: FailCheckoutGroup :exec
UPDATE checkout_groups
SET status = 'FAILED',
    payment_status = CASE WHEN payment_status IN ('', 'PENDING') THEN $2 ELSE payment_status END,
    needs_cleanup = FALSE,
    updated_at = NOW()
WHERE checkout_groups.id = $1
  AND checkout_groups.status = 'IN_PROGRESS'
  AND NOT EXISTS (
      SELECT 1 FROM orders o
      WHERE o.checkout_group_id = checkout_groups.id
        AND o.status <> 'CANCELLED'
  );

-- name: CheckoutGroupHasOrders :one
SELECT EXISTS (
    SELECT 1 FROM orders WHERE checkout_group_id = $1
);

-- name: ClaimCheckoutGroupForCleanup :exec
UPDATE checkout_groups
SET status = 'FAILED',
    payment_status = CASE WHEN payment_status IN ('', 'PENDING') THEN $2 ELSE payment_status END,
    needs_cleanup = TRUE,
    updated_at = NOW()
WHERE id = $1
  AND status = 'IN_PROGRESS';

-- name: ClearCheckoutGroupNeedsCleanup :exec
UPDATE checkout_groups
SET needs_cleanup = FALSE,
    updated_at = NOW()
WHERE id = $1;

-- name: MarkCheckoutGroupNeedsCleanup :exec
UPDATE checkout_groups
SET needs_cleanup = TRUE,
    payment_status = CASE WHEN sqlc.arg(reason)::text <> '' THEN sqlc.arg(reason)::text ELSE payment_status END,
    updated_at = NOW()
WHERE id = $1
  AND status = 'IN_PROGRESS';

-- name: UpdateCheckoutGroupPaymentStatus :exec
UPDATE checkout_groups
SET payment_status = $2,
    payment_id = CASE WHEN sqlc.arg(payment_id)::text <> '' THEN sqlc.arg(payment_id)::text ELSE payment_id END,
    updated_at = NOW()
WHERE id = $1;

-- name: SetCheckoutGroupPendingStockConfirm :exec
UPDATE checkout_groups
SET pending_stock_confirm = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateCheckoutGroupPaymentOnRetry :exec
UPDATE checkout_groups
SET payment_method = $2,
    payment_id = $3,
    payment_status = $4,
    qr_url = $5,
    qr_code = $6,
    pay_url = $7,
    payment_expires_at = $8,
    pending_stock_confirm = CASE WHEN sqlc.arg(set_pending_stock_confirm)::boolean THEN TRUE ELSE pending_stock_confirm END,
    response_snapshot_json = NULL,
    retry_idempotency_key = $9,
    retry_from_payment_id = $10,
    updated_at = NOW()
WHERE id = $1;

-- name: InsertOrder :one
INSERT INTO orders (
    id, order_code, checkout_group_id, buyer_id, shop_id, status,
    payment_method, payment_id, payment_status,
    subtotal_vnd, flash_sale_discount_vnd, shop_program_discount_vnd,
    shop_voucher_discount_vnd, shop_freeship_discount_vnd,
    platform_voucher_discount_vnd, freeship_discount_vnd, coins_discount_vnd,
    shipping_fee_vnd, commission_vnd, payment_fee_vnd, payment_fee_bps, total_payable_vnd,
    carrier_code, service_code, tracking_code, label_url, cancel_reason,
    shipping_address_json, payment_expires_at, confirmed_at,
    sales_channel, storefront_shop_id
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12,
    $13, $14,
    $15, $16, $17,
    $18, $19, $20, $21, $22,
    $23, $24, $25, $26, $27,
    $28, $29, $30,
    $31, $32
)
RETURNING *;

-- name: InsertOrderItem :one
INSERT INTO order_items (
    id, order_id, sku_id, product_id, product_name, sku_name,
    unit_price_vnd, effective_unit_price_vnd, quantity, line_total_vnd,
    weight_grams, flash_sale_item_id, commission_rate_bps,
    category_id, shop_program_item_id
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13,
    $14, $15
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

-- name: GetOrderByCode :one
SELECT *
FROM orders
WHERE order_code = $1;

-- name: ListOrdersByIDs :many
SELECT *
FROM orders
WHERE id = ANY(sqlc.arg(ids)::text[]);

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
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(carrier_code)::text = '' OR carrier_code = sqlc.arg(carrier_code)::text)
  AND (sqlc.arg(payment_method)::text = '' OR payment_method = sqlc.arg(payment_method)::text)
  AND (
      sqlc.arg(keyword_pattern)::text = ''
      OR order_code ILIKE sqlc.arg(keyword_pattern)::text ESCAPE '\'
      OR COALESCE(shipping_address_json->>'recipient_name', '') ILIKE sqlc.arg(keyword_pattern)::text ESCAPE '\'
  )
  AND (
      sqlc.narg(due_cutoff)::timestamptz IS NULL
      OR COALESCE(confirmed_at, created_at) <= sqlc.narg(due_cutoff)::timestamptz
  )
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
  AND (cardinality(sqlc.arg(statuses)::text[]) = 0 OR status = ANY(sqlc.arg(statuses)::text[]))
  AND (sqlc.arg(carrier_code)::text = '' OR carrier_code = sqlc.arg(carrier_code)::text)
  AND (sqlc.arg(payment_method)::text = '' OR payment_method = sqlc.arg(payment_method)::text)
  AND (
      sqlc.arg(keyword_pattern)::text = ''
      OR order_code ILIKE sqlc.arg(keyword_pattern)::text ESCAPE '\'
      OR COALESCE(shipping_address_json->>'recipient_name', '') ILIKE sqlc.arg(keyword_pattern)::text ESCAPE '\'
  )
  AND (
      sqlc.narg(due_cutoff)::timestamptz IS NULL
      OR COALESCE(confirmed_at, created_at) <= sqlc.narg(due_cutoff)::timestamptz
  )
  AND (sqlc.arg(sales_channel)::text = '' OR sales_channel = sqlc.arg(sales_channel)::text)
  AND (sqlc.arg(storefront_shop_id)::text = '' OR storefront_shop_id = sqlc.arg(storefront_shop_id)::text);

-- name: CountOrdersByStatus :many
SELECT status, COUNT(*)::bigint AS count
FROM orders
WHERE (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(sales_channel)::text = '' OR sales_channel = sqlc.arg(sales_channel)::text)
  AND (sqlc.arg(storefront_shop_id)::text = '' OR storefront_shop_id = sqlc.arg(storefront_shop_id)::text)
GROUP BY status
ORDER BY status ASC;

-- name: GetPickingListByOrderIDs :many
SELECT oi.sku_id,
       MAX(oi.product_id)::text AS product_id,
       MAX(oi.product_name)::text AS product_name,
       MAX(oi.sku_name)::text AS sku_name,
       COALESCE(SUM(oi.quantity), 0)::int AS total_quantity,
       COUNT(DISTINCT oi.order_id)::int AS order_count,
       ARRAY_AGG(DISTINCT o.order_code ORDER BY o.order_code)::text[] AS order_codes
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
WHERE o.shop_id = $1
  AND o.id = ANY(sqlc.arg(order_ids)::text[])
GROUP BY oi.sku_id
ORDER BY oi.sku_id ASC;

-- name: AttachCheckoutPaymentToOrders :exec
UPDATE orders
SET payment_id = CASE
        WHEN payment_id = '' THEN $2
        ELSE payment_id
    END,
    payment_status = CASE
        WHEN payment_status IN ('', 'PENDING') THEN $3
        ELSE payment_status
    END,
    payment_expires_at = CASE
        WHEN payment_status IN ('', 'PENDING') THEN COALESCE($4, payment_expires_at)
        ELSE payment_expires_at
    END,
    updated_at = NOW()
WHERE checkout_group_id = $1
  AND status <> 'CANCELLED'
  AND (payment_id = '' OR payment_id = $2)
  AND (payment_status IN ('', 'PENDING') OR payment_id = '');

-- name: UpdateOrderPaymentOnRetry :one
UPDATE orders
SET payment_method = $2,
    payment_id = $3,
    payment_status = $4,
    payment_fee_vnd = $5,
    payment_fee_bps = $6,
    payment_expires_at = $7,
    status = CASE WHEN sqlc.arg(confirm_order)::boolean THEN 'CONFIRMED' ELSE status END,
    confirmed_at = CASE WHEN sqlc.arg(confirm_order)::boolean AND confirmed_at IS NULL THEN NOW() ELSE confirmed_at END,
    updated_at = NOW()
WHERE id = $1
  AND status = 'PENDING'
RETURNING *;

-- name: UpdateOrderStatus :one
UPDATE orders
SET status = $2,
    cancel_reason = CASE WHEN sqlc.arg(cancel_reason)::text <> '' THEN sqlc.arg(cancel_reason)::text ELSE cancel_reason END,
    payment_status = CASE WHEN sqlc.arg(payment_status)::text <> '' THEN sqlc.arg(payment_status)::text ELSE payment_status END,
    payment_id = CASE WHEN sqlc.arg(payment_id)::text <> '' THEN sqlc.arg(payment_id)::text ELSE payment_id END,
    confirmed_at = CASE WHEN $2 = 'CONFIRMED' AND confirmed_at IS NULL THEN NOW() ELSE confirmed_at END,
    shipment_job_status = CASE WHEN $2 IN ('CANCELLED', 'RETURNED', 'SHIPPING', 'DELIVERED', 'COMPLETED') THEN '' ELSE shipment_job_status END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderPreparingWithShipment :one
UPDATE orders
SET status = 'PREPARING',
    carrier_code = $2,
    service_code = $3,
    tracking_code = $4,
    label_url = $5,
    shipment_job_status = '',
    pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: SetOrderShipmentQueued :one
UPDATE orders
SET shipment_job_status = 'QUEUED',
    pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: SetOrderShipmentJobFailed :one
UPDATE orders
SET shipment_job_status = 'FAILED',
    updated_at = NOW()
WHERE id = $1
  AND tracking_code = ''
  AND status IN ('CONFIRMED', 'PREPARING')
RETURNING *;

-- name: MarkOrderShipmentJobFailedIfQueued :execrows
UPDATE orders
SET shipment_job_status = 'FAILED',
    updated_at = NOW()
WHERE id = $1
  AND status IN ('CONFIRMED', 'PREPARING')
  AND shipment_job_status = 'QUEUED'
  AND tracking_code = ''
  AND pending_shipment_cancel = FALSE;

-- name: ClearOrderShipmentJobStatus :exec
UPDATE orders
SET shipment_job_status = '',
    updated_at = NOW()
WHERE id = $1;

-- name: SetOrderPendingShipmentCancel :exec
UPDATE orders
SET pending_shipment_cancel = $2,
    pending_shipment_cancel_at = CASE WHEN $2 THEN sqlc.arg(marked_at)::timestamptz ELSE NULL END,
    pending_shipment_cancel_token = CASE WHEN $2 THEN sqlc.arg(token)::text ELSE '' END,
    updated_at = NOW()
WHERE id = $1;

-- name: ClearOrderPendingShipmentCancelByToken :execrows
UPDATE orders
SET pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = NOW()
WHERE id = $1
  AND pending_shipment_cancel = TRUE
  AND pending_shipment_cancel_token = $2;

-- name: ClearStaleOrderPendingShipmentCancel :execrows
UPDATE orders
SET pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = NOW()
WHERE id = $1
  AND status IN ('CONFIRMED', 'PREPARING')
  AND pending_shipment_cancel = TRUE
  AND pending_shipment_cancel_token = $2
  AND COALESCE(pending_shipment_cancel_at, updated_at) < sqlc.arg(cutoff)::timestamptz;

-- name: SetOrderPendingCancelCompensation :exec
UPDATE orders
SET pending_cancel_compensation = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: SetOrderPendingReturnCompensation :exec
UPDATE orders
SET pending_return_compensation = $2,
    updated_at = NOW()
WHERE id = $1;

-- name: MarkOrderOrphanShipmentManualReview :exec
UPDATE orders
SET pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    orphan_shipment_needs_manual_review = TRUE,
    updated_at = NOW()
WHERE id = $1;

-- name: UpdateOrderShippingLabel :one
UPDATE orders
SET carrier_code = CASE WHEN carrier_code = '' OR LOWER(carrier_code) = 'fallback' THEN $2 ELSE carrier_code END,
    service_code = CASE WHEN service_code = '' OR LOWER(carrier_code) = 'fallback' THEN $3 ELSE service_code END,
    label_url = CASE WHEN sqlc.arg(label_url)::text <> '' THEN sqlc.arg(label_url)::text ELSE label_url END,
    shipment_job_status = '',
    pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderShippedFromWebhook :one
UPDATE orders
SET status = 'SHIPPING',
    carrier_code = CASE WHEN (carrier_code = '' OR LOWER(carrier_code) = 'fallback') AND sqlc.arg(carrier_code)::text <> '' THEN sqlc.arg(carrier_code)::text ELSE carrier_code END,
    service_code = CASE WHEN (service_code = '' OR LOWER(carrier_code) = 'fallback') AND sqlc.arg(service_code)::text <> '' THEN sqlc.arg(service_code)::text ELSE service_code END,
    tracking_code = CASE WHEN sqlc.arg(tracking_code)::text <> '' THEN sqlc.arg(tracking_code)::text ELSE tracking_code END,
    shipment_job_status = '',
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderDelivered :one
UPDATE orders
SET status = 'DELIVERED',
    carrier_code = CASE WHEN (carrier_code = '' OR LOWER(carrier_code) = 'fallback') AND sqlc.arg(carrier_code)::text <> '' THEN sqlc.arg(carrier_code)::text ELSE carrier_code END,
    tracking_code = CASE WHEN sqlc.arg(tracking_code)::text <> '' THEN sqlc.arg(tracking_code)::text ELSE tracking_code END,
    shipment_job_status = '',
    delivered_at = NOW(),
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderCompleted :one
UPDATE orders
SET status = 'COMPLETED',
    completed_at = NOW(),
    settlement_refunded_vnd = refunded_vnd,
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

-- name: UpdateOrderRefunded :exec
UPDATE orders
SET refunded_vnd = $2,
    settlement_refunded_vnd = $3,
    payment_status = CASE WHEN sqlc.arg(payment_status)::text <> '' THEN sqlc.arg(payment_status)::text ELSE payment_status END,
    updated_at = NOW()
WHERE id = $1;

-- name: InsertReturnRequest :one
INSERT INTO return_requests (
    id, order_id, order_code, buyer_id, shop_id, reason, evidence_urls_json,
    status, refund_amount_vnd, deadline_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10
)
RETURNING *;

-- name: InsertReturnEvent :one
INSERT INTO return_events (
    id, return_request_id, from_status, to_status, actor_id, actor_role, note
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
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

-- name: GetReturnRequestByOrderID :one
SELECT *
FROM return_requests
WHERE order_id = $1;

-- name: HasReturnRequestForOrder :one
SELECT EXISTS (
    SELECT 1 FROM return_requests WHERE order_id = $1
);

-- name: HasActiveReturnRequestForOrder :one
SELECT EXISTS (
    SELECT 1 FROM return_requests WHERE order_id = $1 AND status <> 'CLOSED'
);

-- name: ListReturnSummariesByOrderIDs :many
SELECT id, order_id, status
FROM return_requests
WHERE order_id = ANY(sqlc.arg(order_ids)::text[]);

-- name: ListReturnEventsByRequestID :many
SELECT *
FROM return_events
WHERE return_request_id = $1
ORDER BY created_at ASC, id ASC;

-- name: ListReturnRequestsFiltered :many
SELECT *
FROM return_requests
WHERE (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountReturnRequestsFiltered :one
SELECT COUNT(*)
FROM return_requests
WHERE (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

-- name: UpdateReturnRequestState :one
UPDATE return_requests
SET status = $2,
    evidence_urls_json = $3,
    seller_response = $4,
    seller_evidence_urls_json = $5,
    dispute_reason = $6,
    admin_note = $7,
    refund_amount_vnd = $8,
    return_tracking_code = $9,
    return_label_url = $10,
    return_fee_vnd = $11,
    return_fee_bearer = $12,
    goods_received = $13,
    should_restock = $14,
    deadline_at = $15,
    return_shipment_terminal = $16,
    previous_return_trackings_json = $17,
    deadline_extension_count = $18,
    escalated_from_transit_timeout = $19,
    escalated_from_cancel_failure = $20,
    require_approval_if_large = $21,
    refund_requested_by = $22,
    refund_id = $23,
    refund_approval_status = $24,
    refund_attempt = $25,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: ExtendReturnRequestDeadline :one
UPDATE return_requests
SET deadline_at = $2,
    deadline_extension_count = deadline_extension_count + 1,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateReturnRequestSideEffects :one
UPDATE return_requests
SET refund_done = $2,
    promo_released = $3,
    restock_done = $4,
    refund_amount_vnd = CASE WHEN sqlc.arg(update_refund_amount)::bool THEN sqlc.arg(refund_amount_vnd)::bigint ELSE refund_amount_vnd END,
    refund_id = CASE WHEN sqlc.arg(update_refund_id)::bool THEN sqlc.arg(refund_id)::text ELSE refund_id END,
    refund_approval_status = CASE WHEN sqlc.arg(update_refund_approval_status)::bool THEN sqlc.arg(refund_approval_status)::text ELSE refund_approval_status END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateReturnShipmentInfo :one
UPDATE return_requests
SET return_tracking_code = $2,
    return_label_url = $3,
    return_fee_vnd = $4,
    deadline_at = $5,
    return_shipment_terminal = FALSE,
    deadline_extension_count = 0,
    escalated_from_transit_timeout = FALSE,
    escalated_from_cancel_failure = FALSE,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (
    id, event_type, trace_id, traceparent, payload_json
) VALUES (
    $1, $2, $3, $4, $5
);

-- name: ClaimPendingOutboxEvents :many
WITH locked AS (
    SELECT e.id,
           COALESCE(NULLIF(e.payload_json->>'order_id', ''), e.id) AS agg_id,
           e.seq
    FROM outbox_events e
    WHERE e.published_at IS NULL
      AND (e.claimed_until IS NULL OR e.claimed_until <= NOW())
      AND NOT EXISTS (
          SELECT 1
          FROM outbox_events prev
          WHERE prev.published_at IS NULL
            AND COALESCE(NULLIF(prev.payload_json->>'order_id', ''), prev.id) = COALESCE(NULLIF(e.payload_json->>'order_id', ''), e.id)
            AND prev.seq < e.seq
            AND prev.claimed_until IS NOT NULL
            AND prev.claimed_until > NOW()
      )
    ORDER BY e.seq ASC
    LIMIT sqlc.arg(batch_limit)::int
    FOR UPDATE OF e SKIP LOCKED
),
eligible AS (
    SELECT l.id
    FROM locked l
    WHERE NOT EXISTS (
        SELECT 1
        FROM outbox_events prev
        WHERE prev.published_at IS NULL
          AND COALESCE(NULLIF(prev.payload_json->>'order_id', ''), prev.id) = l.agg_id
          AND prev.seq < l.seq
          AND NOT EXISTS (SELECT 1 FROM locked l2 WHERE l2.id = prev.id)
    )
)
UPDATE outbox_events o
SET claimed_until = NOW() + (sqlc.arg(lease_ms)::bigint * INTERVAL '1 millisecond'),
    claim_token = sqlc.arg(claim_token)::text
FROM eligible
WHERE o.id = eligible.id
RETURNING o.*;

-- name: MarkOutboxEventPublished :execrows
UPDATE outbox_events
SET published_at = NOW(),
    attempts = attempts + 1,
    last_error = '',
    claimed_until = NULL,
    claim_token = ''
WHERE id = $1
  AND published_at IS NULL
  AND claim_token = $2;

-- name: MarkOutboxEventAttemptFailed :one
UPDATE outbox_events
SET attempts = attempts + 1,
    last_error = $2,
    claimed_until = CASE
        WHEN sqlc.arg(apply_backoff)::boolean THEN
            NOW() + (LEAST(POWER(2, LEAST(attempts + 1, 6)), 60)::bigint * INTERVAL '1 second')
        ELSE NULL
    END,
    claim_token = ''
WHERE id = $1
  AND published_at IS NULL
  AND claim_token = $3
RETURNING attempts;

-- name: ReleaseOutboxEventClaims :execrows
UPDATE outbox_events
SET claimed_until = NULL,
    claim_token = ''
WHERE id = ANY(sqlc.arg(ids)::text[])
  AND published_at IS NULL
  AND claim_token = sqlc.arg(claim_token)::text;

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

-- ponytail: P2-1 — plain SELECT + ORDER BY random() (no locked_until lease column); prevents starvation behind 20 failing rows.
-- Multi-replica ceiling: concurrent sweeps are bounded by idempotent GetPayment/CancelPayment + per-row FOR UPDATE TX checks.
-- name: FindExpiredPendingOrders :many
SELECT id, checkout_group_id, payment_id
FROM orders
WHERE status = 'PENDING'
  AND payment_method IN ('VIETQR', 'ZALOPAY')
  AND payment_expires_at IS NOT NULL
  AND payment_expires_at <= NOW()
ORDER BY random()
LIMIT $1;

-- ponytail: P2-1 — plain SELECT + ORDER BY random() without lease column; prevents starvation behind 20 failing rows.
-- Multi-replica ceiling: bounded by idempotent ReleasePromotion/ReleaseStock + FailCheckoutGroup.
-- name: FindStaleInProgressCheckoutGroups :many
SELECT g.id
FROM checkout_groups g
WHERE (
    (g.status = 'IN_PROGRESS' AND (
        g.needs_cleanup = TRUE
        OR g.updated_at < NOW() - (sqlc.arg(stale_after_ms)::bigint * INTERVAL '1 millisecond')
    ))
    OR (g.status = 'FAILED' AND g.needs_cleanup = TRUE)
)
  AND NOT EXISTS (
      SELECT 1 FROM orders o WHERE o.checkout_group_id = g.id
  )
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- name: FindIncompleteCheckoutGroupsWithOrders :many
SELECT g.id
FROM checkout_groups g
WHERE g.status = 'IN_PROGRESS'
  AND g.updated_at < NOW() - (sqlc.arg(stale_after_ms)::bigint * INTERVAL '1 millisecond')
  AND EXISTS (
      SELECT 1 FROM orders o WHERE o.checkout_group_id = g.id
  )
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random() prevents starvation behind permanently failing rows without adding next_retry_at/lease columns.
-- name: FindConfirmedOrdersPastTimeout :many
SELECT id
FROM orders
WHERE status = 'CONFIRMED'
  AND COALESCE(confirmed_at, updated_at) <= sqlc.arg(cutoff)::timestamptz
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random() prevents starvation; P2-5 ignores CLOSED legacy return rows.
-- name: FindDeliveredOrdersForAutoComplete :many
SELECT o.id
FROM orders o
WHERE o.status = 'DELIVERED'
  AND o.delivered_at IS NOT NULL
  AND o.delivered_at <= sqlc.arg(cutoff)::timestamptz
  AND NOT EXISTS (
      SELECT 1 FROM return_requests r WHERE r.order_id = o.id AND r.status <> 'CLOSED'
  )
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random() prevents starvation without next_retry_at/lease columns.
-- name: FindExpiredReturnRequests :many
SELECT id
FROM return_requests
WHERE status IN ('PENDING', 'AWAITING_SHIPMENT', 'IN_TRANSIT', 'REJECTED', 'RECEIVED', 'EVIDENCE_REQUESTED')
  AND deadline_at IS NOT NULL
  AND deadline_at <= sqlc.arg(now)::timestamptz
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random(); deterministic client_order_code in shipping prevents duplicate carrier labels across replicas.
-- name: FindAwaitingShipmentReturnsWithoutTracking :many
SELECT id
FROM return_requests
WHERE status = 'AWAITING_SHIPMENT'
  AND return_tracking_code = ''
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random(); downstream CreateRefund/ReleasePromotionShop/RestockOrderItems are idempotent.
-- F11: Skip rows currently awaiting two-person checker approval so watchdog does not re-call CreateRefund every tick.
-- name: FindRefundingReturnRequests :many
SELECT id
FROM return_requests
WHERE status = 'REFUNDING'
  AND refund_approval_status <> 'PENDING_APPROVAL'
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random(); CancelShipment is idempotent.
-- name: FindCancelledOrdersPendingShipmentCancel :many
SELECT id
FROM orders
WHERE status = 'CANCELLED'
  AND pending_shipment_cancel = TRUE
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random(); reconciles active orders whose pending_shipment_cancel flag exceeded PendingShipmentCancelTTL.
-- name: FindActiveOrdersWithStalePendingShipmentCancel :many
SELECT id
FROM orders
WHERE status IN ('CONFIRMED', 'PREPARING')
  AND pending_shipment_cancel = TRUE
  AND COALESCE(pending_shipment_cancel_at, updated_at) < sqlc.arg(cutoff)::timestamptz
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random(); ConfirmStock is idempotent by checkout_group_id.
-- name: FindCheckoutGroupsPendingStockConfirm :many
SELECT id
FROM checkout_groups
WHERE pending_stock_confirm = TRUE
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random(); all cancel compensations (ReleaseStock/ConfirmStock/Restock/ReleasePromotion/CreateRefund) are idempotent.
-- name: FindCancelledOrdersPendingCompensation :many
SELECT id
FROM orders
WHERE status = 'CANCELLED'
  AND pending_cancel_compensation = TRUE
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- ponytail: P2-1 + ORDER BY random(); all SHIPPING -> RETURNED compensations (ConfirmStock/Restock/ReleasePromotionShop/RecordCODRejection/CreateRefund) are idempotent.
-- name: FindReturnedOrdersPendingCompensation :many
SELECT id
FROM orders
WHERE status = 'RETURNED'
  AND pending_return_compensation = TRUE
ORDER BY random()
LIMIT sqlc.arg(batch_limit)::int;

-- Wave 2b: Admin orphan-shipment manual review queue (uses idx_orders_orphan_manual_review).
-- name: ListOrdersNeedingManualReview :many
SELECT *
FROM orders
WHERE orphan_shipment_needs_manual_review = TRUE
ORDER BY updated_at ASC, id ASC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountOrdersNeedingManualReview :one
SELECT COUNT(*)
FROM orders
WHERE orphan_shipment_needs_manual_review = TRUE;

-- name: ClearOrderOrphanShipmentManualReview :one
UPDATE orders
SET orphan_shipment_needs_manual_review = FALSE,
    updated_at = NOW()
WHERE id = $1
  AND orphan_shipment_needs_manual_review = TRUE
RETURNING *;

-- Wave 2b: Seller and Platform reporting queries (Asia/Ho_Chi_Minh date boundaries passed as TIMESTAMPTZ
-- so PostgreSQL can use B-tree indexes directly: idx_orders_shop_id, idx_orders_created_at,
-- idx_orders_shop_completed_at, idx_orders_completed_at, idx_order_items_order_id).

-- name: ReportSellerStatusCounts :many
SELECT status, COUNT(*)::bigint AS count
FROM orders
WHERE shop_id = $1
  AND created_at >= sqlc.arg(start_ts)::timestamptz
  AND created_at < sqlc.arg(end_ts)::timestamptz
GROUP BY status;

-- name: ReportPlatformStatusCounts :many
SELECT status, COUNT(*)::bigint AS count
FROM orders
WHERE created_at >= sqlc.arg(start_ts)::timestamptz
  AND created_at < sqlc.arg(end_ts)::timestamptz
GROUP BY status;

-- name: ReportSellerCreatedDaily :many
SELECT to_char(created_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM-DD')::text AS report_date,
       COUNT(*)::bigint AS order_count,
       COUNT(*) FILTER (WHERE status = 'CANCELLED')::bigint AS cancelled_count,
       COUNT(*) FILTER (WHERE status = 'RETURNED')::bigint AS returned_count,
       COALESCE(SUM(
           total_payable_vnd
           + GREATEST(0, platform_voucher_discount_vnd)
           + GREATEST(0, freeship_discount_vnd)
           + GREATEST(0, coins_discount_vnd)
       ) FILTER (WHERE status NOT IN ('PENDING', 'CANCELLED', 'RETURNED')), 0)::bigint AS gmv_vnd
FROM orders
WHERE shop_id = $1
  AND created_at >= sqlc.arg(start_ts)::timestamptz
  AND created_at < sqlc.arg(end_ts)::timestamptz
GROUP BY 1
ORDER BY 1 ASC;

-- name: ReportPlatformCreatedDaily :many
SELECT to_char(created_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM-DD')::text AS report_date,
       COUNT(*)::bigint AS order_count,
       COUNT(*) FILTER (WHERE status = 'CANCELLED')::bigint AS cancelled_count,
       COUNT(*) FILTER (WHERE status = 'RETURNED')::bigint AS returned_count,
       COALESCE(SUM(
           total_payable_vnd
           + GREATEST(0, platform_voucher_discount_vnd)
           + GREATEST(0, freeship_discount_vnd)
           + GREATEST(0, coins_discount_vnd)
       ) FILTER (WHERE status NOT IN ('PENDING', 'CANCELLED', 'RETURNED')), 0)::bigint AS gmv_vnd
FROM orders
WHERE created_at >= sqlc.arg(start_ts)::timestamptz
  AND created_at < sqlc.arg(end_ts)::timestamptz
GROUP BY 1
ORDER BY 1 ASC;

-- name: ReportSellerCompletedDaily :many
WITH base AS (
    SELECT to_char(completed_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM-DD')::text AS report_date,
           GREATEST(0, subtotal_vnd - flash_sale_discount_vnd - shop_program_discount_vnd - shop_voucher_discount_vnd - shop_freeship_discount_vnd) AS r_vnd,
           GREATEST(0, commission_vnd) AS raw_c_cat,
           GREATEST(0, payment_fee_vnd) AS raw_c_pay,
           GREATEST(0, refunded_vnd) AS refunded,
           LEAST(GREATEST(0, settlement_refunded_vnd), GREATEST(0, refunded_vnd)) AS pre_refund
    FROM orders
    WHERE shop_id = $1
      AND status = 'COMPLETED'
      AND completed_at IS NOT NULL
      AND completed_at >= sqlc.arg(start_ts)::timestamptz
      AND completed_at < sqlc.arg(end_ts)::timestamptz
),
capped AS (
    SELECT report_date,
           r_vnd,
           LEAST(raw_c_cat, r_vnd) AS c_cat,
           LEAST(raw_c_pay, GREATEST(0, r_vnd - LEAST(raw_c_cat, r_vnd))) AS c_pay,
           refunded,
           pre_refund
    FROM base
),
wf AS (
    SELECT report_date,
           GREATEST(0, (r_vnd - c_cat - c_pay) - pre_refund) - (refunded - pre_refund) AS seller_net,
           GREATEST(0, pre_refund - (r_vnd - c_cat - c_pay)) AS rem_after_seller,
           c_cat,
           c_pay
    FROM capped
)
SELECT report_date::text AS report_date,
       COUNT(*)::bigint AS completed_count,
       COALESCE(SUM(seller_net), 0)::bigint AS net_revenue_vnd,
       COALESCE(SUM(c_cat - LEAST(GREATEST(0, rem_after_seller - LEAST(rem_after_seller, c_pay)), c_cat)), 0)::bigint AS commission_vnd,
       COALESCE(SUM(c_pay - LEAST(rem_after_seller, c_pay)), 0)::bigint AS payment_fee_vnd
FROM wf
GROUP BY 1
ORDER BY 1 ASC;

-- name: ReportPlatformCompletedDaily :many
WITH base AS (
    SELECT to_char(completed_at AT TIME ZONE 'Asia/Ho_Chi_Minh', 'YYYY-MM-DD')::text AS report_date,
           GREATEST(0, subtotal_vnd - flash_sale_discount_vnd - shop_program_discount_vnd - shop_voucher_discount_vnd - shop_freeship_discount_vnd) AS r_vnd,
           GREATEST(0, shipping_fee_vnd) AS ship_vnd,
           total_payable_vnd,
           GREATEST(0, commission_vnd) AS raw_c_cat,
           GREATEST(0, payment_fee_vnd) AS raw_c_pay,
           GREATEST(0, refunded_vnd) AS refunded,
           LEAST(GREATEST(0, settlement_refunded_vnd), GREATEST(0, refunded_vnd)) AS pre_refund
    FROM orders
    WHERE status = 'COMPLETED'
      AND completed_at IS NOT NULL
      AND completed_at >= sqlc.arg(start_ts)::timestamptz
      AND completed_at < sqlc.arg(end_ts)::timestamptz
),
capped AS (
    SELECT report_date,
           r_vnd,
           GREATEST(0, r_vnd + ship_vnd - total_payable_vnd) AS platform_subsidy,
           LEAST(raw_c_cat, r_vnd) AS c_cat,
           LEAST(raw_c_pay, GREATEST(0, r_vnd - LEAST(raw_c_cat, r_vnd))) AS c_pay,
           refunded,
           pre_refund
    FROM base
),
wf AS (
    SELECT report_date,
           platform_subsidy,
           GREATEST(0, (r_vnd - c_cat - c_pay) - pre_refund) - (refunded - pre_refund) AS seller_net,
           GREATEST(0, pre_refund - (r_vnd - c_cat - c_pay)) AS rem_after_seller,
           c_cat,
           c_pay
    FROM capped
)
SELECT report_date::text AS report_date,
       COUNT(*)::bigint AS completed_count,
       COALESCE(SUM(seller_net), 0)::bigint AS net_revenue_vnd,
       COALESCE(SUM(c_cat - LEAST(GREATEST(0, rem_after_seller - LEAST(rem_after_seller, c_pay)), c_cat)), 0)::bigint AS commission_vnd,
       COALESCE(SUM(c_pay - LEAST(rem_after_seller, c_pay)), 0)::bigint AS payment_fee_vnd,
       COALESCE(SUM(platform_subsidy), 0)::bigint AS platform_subsidy_vnd
FROM wf
GROUP BY 1
ORDER BY 1 ASC;

-- name: ReportSellerTopProducts :many
SELECT oi.product_id,
       MAX(oi.product_name)::text AS product_name,
       COALESCE(SUM(oi.quantity), 0)::bigint AS units_sold,
       COALESCE(SUM(oi.line_total_vnd), 0)::bigint AS revenue_vnd,
       COUNT(DISTINCT oi.order_id)::bigint AS order_count
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
WHERE o.shop_id = $1
  AND o.status = 'COMPLETED'
  AND o.completed_at IS NOT NULL
  AND o.completed_at >= sqlc.arg(start_ts)::timestamptz
  AND o.completed_at < sqlc.arg(end_ts)::timestamptz
GROUP BY oi.product_id
ORDER BY revenue_vnd DESC, units_sold DESC, oi.product_id ASC
LIMIT sqlc.arg(top_limit)::int;

-- name: ReportPlatformTopProducts :many
SELECT oi.product_id,
       MAX(oi.product_name)::text AS product_name,
       COALESCE(SUM(oi.quantity), 0)::bigint AS units_sold,
       COALESCE(SUM(oi.line_total_vnd), 0)::bigint AS revenue_vnd,
       COUNT(DISTINCT oi.order_id)::bigint AS order_count
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
WHERE o.status = 'COMPLETED'
  AND o.completed_at IS NOT NULL
  AND o.completed_at >= sqlc.arg(start_ts)::timestamptz
  AND o.completed_at < sqlc.arg(end_ts)::timestamptz
GROUP BY oi.product_id
ORDER BY revenue_vnd DESC, units_sold DESC, oi.product_id ASC
LIMIT sqlc.arg(top_limit)::int;

-- Wave 3b-1 (E19): Single indexed query counting a buyer's terminal COD orders and delivery refusals.
-- Uses idx_orders_buyer_cod_status ON orders (buyer_id, status) INCLUDE (delivered_at) WHERE payment_method = 'COD'.
-- Excludes non-terminal orders and pre-delivery CANCELLED orders; counts only SHIPPING -> RETURNED (delivered_at IS NULL)
-- as COD rejections while treating post-delivery returns (delivered_at IS NOT NULL) as non-rejected terminal outcomes.
-- name: GetBuyerTerminalCODStats :one
SELECT
    COUNT(*)::bigint AS total_terminal,
    COUNT(*) FILTER (WHERE status = 'RETURNED' AND delivered_at IS NULL)::bigint AS rejected_count
FROM orders
WHERE buyer_id = $1
  AND payment_method = 'COD'
  AND status IN ('COMPLETED', 'RETURNED');

-- Wave 3b-3 (A8: G1-G3): Durable order_address_changes intent queries.

-- name: InsertOrderAddressChange :one
INSERT INTO order_address_changes (
    id, order_id, buyer_id, idempotency_key, request_hash, new_address_json,
    carrier_code, service_code, old_carrier_code, old_service_code, old_tracking_code,
    had_shipment, shipment_cancelled,
    old_shipping_fee_vnd, new_shipping_fee_vnd, applied_shipping_fee_vnd,
    old_total_payable_vnd, new_total_payable_vnd,
    fee_adjusted, needs_cod_adjust, cod_adjusted,
    state, next_attempt_at, claimed_until, claim_token,
    trace_id, traceparent, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13,
    $14, $15, $16,
    $17, $18,
    $19, $20, $21,
    $22, $23, $24, $25,
    $26, $27, $28, $29
)
RETURNING *;

-- name: GetOrderAddressChangeByID :one
SELECT *
FROM order_address_changes
WHERE id = $1;

-- name: GetOrderAddressChangeByIDForUpdate :one
SELECT *
FROM order_address_changes
WHERE id = $1
FOR UPDATE;

-- name: GetOpenOrderAddressChangeByOrderID :one
SELECT *
FROM order_address_changes
WHERE order_id = $1
  AND state NOT IN ('COMPLETED', 'ABORTED')
LIMIT 1;

-- name: GetOrderAddressChangeByOrderAndIdemKey :one
SELECT *
FROM order_address_changes
WHERE order_id = $1
  AND idempotency_key = $2
  AND idempotency_key <> ''
LIMIT 1;

-- name: HasOpenOrderAddressChange :one
SELECT EXISTS (
    SELECT 1
    FROM order_address_changes
    WHERE order_id = $1
      AND state NOT IN ('COMPLETED', 'ABORTED')
);

-- name: ListOpenOrderAddressChangesByOrderIDs :many
SELECT *
FROM order_address_changes
WHERE order_id = ANY(sqlc.arg(order_ids)::text[])
  AND state NOT IN ('COMPLETED', 'ABORTED');

-- name: IsCancelledAddressChangeTracking :one
SELECT EXISTS (
    SELECT 1
    FROM order_address_changes
    WHERE order_id = $1
      AND shipment_cancelled = TRUE
      AND old_tracking_code <> ''
      AND LOWER(old_tracking_code) = LOWER(sqlc.arg(tracking_code)::text)
);

-- name: UpdateOrderAddressChangeState :one
UPDATE order_address_changes
SET state = $2,
    shipment_cancelled = $3,
    cod_adjusted = $4,
    shipment_recreated = $5,
    shipment_queued = $6,
    shipment_job_id = $7,
    new_tracking_code = $8,
    outbox_emitted = $9,
    abort_code = $10,
    last_error = $11,
    claimed_until = $12,
    claim_token = $13,
    updated_at = $14
WHERE id = $1
  AND claim_token = sqlc.arg(expected_claim_token)::text
RETURNING *;

-- name: ReleaseOrderAddressChangeWithBackoff :execrows
UPDATE order_address_changes
SET attempts = attempts + 1,
    next_attempt_at = $2,
    claimed_until = NULL,
    claim_token = '',
    last_error = $3,
    updated_at = $4
WHERE id = $1
  AND claim_token = sqlc.arg(expected_claim_token)::text
  AND state NOT IN ('COMPLETED', 'ABORTED');

-- name: RenewOrderAddressChangeLease :one
UPDATE order_address_changes
SET claimed_until = $2,
    updated_at = $3
WHERE id = $1
  AND claim_token = sqlc.arg(expected_claim_token)::text
  AND state NOT IN ('COMPLETED', 'ABORTED')
RETURNING *;

-- name: ClaimOrderAddressChangeByID :one
UPDATE order_address_changes
SET claimed_until = $2,
    claim_token = $3,
    updated_at = $4
WHERE id = $1
  AND state NOT IN ('COMPLETED', 'ABORTED')
  AND (claimed_until IS NULL OR claimed_until <= $4)
RETURNING *;

-- name: ClaimDueOrderAddressChanges :many
WITH due AS (
    SELECT id
    FROM order_address_changes
    WHERE state NOT IN ('COMPLETED', 'ABORTED')
      AND next_attempt_at <= sqlc.arg(now)::timestamptz
      AND (claimed_until IS NULL OR claimed_until <= sqlc.arg(now)::timestamptz)
      AND (cardinality(sqlc.arg(exclude_ids)::text[]) = 0 OR NOT (id = ANY(sqlc.arg(exclude_ids)::text[])))
    ORDER BY next_attempt_at ASC, created_at ASC, id ASC
    LIMIT sqlc.arg(batch_limit)::int
    FOR UPDATE SKIP LOCKED
)
UPDATE order_address_changes c
SET claimed_until = sqlc.arg(now)::timestamptz + (sqlc.arg(lease_ms)::bigint * INTERVAL '1 millisecond'),
    claim_token = sqlc.arg(claim_token)::text,
    updated_at = sqlc.arg(now)::timestamptz
FROM due
WHERE c.id = due.id
RETURNING c.*;

-- name: UpdateOrderAddressAndFees :one
UPDATE orders
SET shipping_address_json = $2,
    shipping_fee_vnd = $3,
    total_payable_vnd = $4,
    carrier_code = $5,
    service_code = $6,
    tracking_code = '',
    label_url = '',
    shipment_job_status = '',
    pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = $7
WHERE id = $1
RETURNING *;

-- name: AdjustCheckoutGroupTotalPayable :exec
UPDATE checkout_groups
SET total_payable_vnd = GREATEST(0, total_payable_vnd + sqlc.arg(delta_vnd)::bigint),
    updated_at = NOW()
WHERE id = $1;
