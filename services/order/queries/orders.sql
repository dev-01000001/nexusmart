-- name: CreateCheckoutGroup :one
INSERT INTO checkout_groups (
    id, buyer_id, idempotency_key, request_hash, status, payment_method, total_payable_vnd
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
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
SET payment_id = $2,
    payment_status = $3,
    qr_url = $4,
    qr_code = $5,
    payment_expires_at = $6,
    response_snapshot_json = NULL,
    retry_idempotency_key = $7,
    retry_from_payment_id = $8,
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
    shipping_address_json, payment_expires_at, confirmed_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15,
    $16, $17, $18,
    $19, $20, $21, $22, $23,
    $24, $25, $26
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
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit) OFFSET sqlc.arg(page_offset);

-- name: CountOrdersFiltered :one
SELECT COUNT(*)
FROM orders
WHERE (sqlc.arg(buyer_id)::text = '' OR buyer_id = sqlc.arg(buyer_id)::text)
  AND (sqlc.arg(shop_id)::text = '' OR shop_id = sqlc.arg(shop_id)::text)
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

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
    confirmed_at = CASE WHEN $2 = 'CONFIRMED' AND confirmed_at IS NULL THEN NOW() ELSE confirmed_at END,
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
    pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = NOW()
WHERE id = $1
RETURNING *;

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
    pending_shipment_cancel = FALSE,
    pending_shipment_cancel_at = NULL,
    pending_shipment_cancel_token = '',
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: UpdateOrderShippedFromWebhook :one
UPDATE orders
SET status = 'SHIPPING',
    tracking_code = CASE WHEN sqlc.arg(tracking_code)::text <> '' THEN sqlc.arg(tracking_code)::text ELSE tracking_code END,
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

-- name: UpdateOrderRefunded :exec
UPDATE orders
SET refunded_vnd = $2,
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

-- name: GetReturnRequestByOrderIDForUpdate :one
SELECT *
FROM return_requests
WHERE order_id = $1
FOR UPDATE;

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

-- ponytail: P2-1 — plain SELECT + ORDER BY random() (no locked_until lease column); prevents starvation behind 20 failing rows.
-- Multi-replica ceiling: concurrent sweeps are bounded by idempotent GetPayment/CancelPayment + per-row FOR UPDATE TX checks.
-- name: FindExpiredPendingOrders :many
SELECT id, checkout_group_id, payment_id
FROM orders
WHERE status = 'PENDING'
  AND payment_method = 'VIETQR'
  AND payment_expires_at IS NOT NULL
  AND payment_expires_at <= NOW()
ORDER BY random()
LIMIT $1;

-- ponytail: P2-1 — plain SELECT + ORDER BY random() without lease column; prevents starvation behind 20 failing rows.
-- Multi-replica ceiling: bounded by idempotent ReleasePromotion/ReleaseStock + FailCheckoutGroup.
-- name: FindStaleInProgressCheckoutGroups :many
SELECT g.id
FROM checkout_groups g
WHERE g.status = 'IN_PROGRESS'
  AND g.updated_at < NOW() - (sqlc.arg(stale_after_ms)::bigint * INTERVAL '1 millisecond')
  AND NOT EXISTS (
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
-- name: FindRefundingReturnRequests :many
SELECT id
FROM return_requests
WHERE status = 'REFUNDING'
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
