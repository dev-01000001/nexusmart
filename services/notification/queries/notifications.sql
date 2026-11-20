-- name: InsertProcessedEvent :execrows
INSERT INTO processed_events (event_id, subject)
VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING;

-- name: IsEventProcessed :one
SELECT EXISTS(SELECT 1 FROM processed_events WHERE event_id = $1);

-- name: DeleteProcessedEvent :exec
DELETE FROM processed_events WHERE event_id = $1;

-- name: HasNotificationForEvent :one
SELECT EXISTS(SELECT 1 FROM notifications WHERE event_id = $1 AND user_id = $2);

-- name: InsertDeadLetterEvent :exec
INSERT INTO dead_letter_events (id, event_id, subject, payload, error_message, attempts)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: InsertNotification :one
INSERT INTO notifications (id, user_id, category, title, body, link, event_id, is_read, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, FALSE, $8)
RETURNING id, user_id, category, title, body, link, event_id, is_read, created_at;

-- name: ListNotifications :many
SELECT id, user_id, category, title, body, link, event_id, is_read, created_at
FROM notifications
WHERE user_id = $1
  AND ($2::text = '' OR category = $2)
  AND (NOT $3::boolean OR is_read = FALSE)
ORDER BY created_at DESC
LIMIT $4 OFFSET $5;

-- name: CountNotifications :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1
  AND ($2::text = '' OR category = $2)
  AND (NOT $3::boolean OR is_read = FALSE);

-- name: CountUnreadNotifications :one
SELECT COUNT(*)
FROM notifications
WHERE user_id = $1 AND is_read = FALSE;

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications
SET is_read = TRUE
WHERE user_id = $1 AND is_read = FALSE;

-- name: MarkSelectedNotificationsRead :execrows
UPDATE notifications
SET is_read = TRUE
WHERE user_id = $1
  AND id = ANY($2::uuid[])
  AND is_read = FALSE;

-- name: GetPreferences :one
SELECT user_id, order_in_app, order_email, order_push,
       promotion_in_app, promotion_email, promotion_push,
       system_in_app, system_email, system_push, updated_at
FROM notification_preferences
WHERE user_id = $1;

-- name: UpsertPreferences :one
INSERT INTO notification_preferences (
    user_id,
    order_in_app, order_email, order_push,
    promotion_in_app, promotion_email, promotion_push,
    system_in_app, system_email, system_push,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
ON CONFLICT (user_id) DO UPDATE SET
    order_in_app = EXCLUDED.order_in_app,
    order_email = EXCLUDED.order_email,
    order_push = EXCLUDED.order_push,
    promotion_in_app = EXCLUDED.promotion_in_app,
    promotion_email = EXCLUDED.promotion_email,
    promotion_push = EXCLUDED.promotion_push,
    system_in_app = EXCLUDED.system_in_app,
    system_email = EXCLUDED.system_email,
    system_push = EXCLUDED.system_push,
    updated_at = NOW()
RETURNING user_id, order_in_app, order_email, order_push,
          promotion_in_app, promotion_email, promotion_push,
          system_in_app, system_email, system_push, updated_at;

-- name: UpsertUserContact :exec
INSERT INTO user_contacts (user_id, email, full_name, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (user_id) DO UPDATE SET
    email = EXCLUDED.email,
    full_name = CASE WHEN EXCLUDED.full_name <> '' THEN EXCLUDED.full_name ELSE user_contacts.full_name END,
    updated_at = NOW();

-- name: GetUserContact :one
SELECT user_id, email, full_name, updated_at
FROM user_contacts
WHERE user_id = $1;

-- name: UpsertOrderParticipant :exec
INSERT INTO order_participants (order_id, buyer_id, shop_id, order_code, updated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (order_id) DO UPDATE SET
    buyer_id = CASE WHEN EXCLUDED.buyer_id <> '' THEN EXCLUDED.buyer_id ELSE order_participants.buyer_id END,
    shop_id = CASE WHEN EXCLUDED.shop_id <> '' THEN EXCLUDED.shop_id ELSE order_participants.shop_id END,
    order_code = CASE WHEN EXCLUDED.order_code <> '' THEN EXCLUDED.order_code ELSE order_participants.order_code END,
    updated_at = NOW();

-- name: GetOrderParticipant :one
SELECT order_id, buyer_id, shop_id, order_code, updated_at
FROM order_participants
WHERE order_id = $1;
