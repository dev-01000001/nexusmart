---
name: checkout-saga
description: 'Luồng saga checkout đa gian hàng, idempotency, tách đơn con theo shop, bù trừ bằng service token, outbox/consumer NATS, watchdog hết hạn thanh toán, máy trạng thái đơn hàng và optimistic locking tồn kho SKU của NexusMart. Dùng khi sửa hoặc debug services/order, services/product (ReserveStock/ReleaseStock/ConfirmStock), hoặc tích hợp payment/promotion/shipping với luồng đơn hàng.'
---

# NexusMart Checkout Saga & Order Lifecycle

Nguồn sự thật: code trong `services/order/` (`internal/service/`, `internal/statemachine/`, `internal/events/`, `internal/clients/`, `internal/store/`, `migrations/0001_init.sql`) và `services/product/` (`internal/server/server.go`, `internal/store/pg_store.go`, `queries/product.sql`, `migrations/000001_init.sql`). Khi `docs/contracts.md` §6 lệch với code, **code là nguồn sự thật** (xem mục 7).

## 1. Bảng dữ liệu chính (`services/order/migrations/0001_init.sql`)

- `checkout_groups`: lưu phiên checkout theo `(buyer_id, idempotency_key)` với partial unique index `idx_checkout_groups_buyer_idem_active` (`WHERE status <> 'FAILED'`), trạng thái `IN_PROGRESS` | `COMPLETED` | `FAILED`, lưu `response_snapshot_json` để replay.
- `orders`: 1 đơn con/shop chung `checkout_group_id`, có index `idx_orders_pending_expiry` (`WHERE status = 'PENDING' AND payment_method = 'VIETQR'`). Tiền lưu `BIGINT` nguyên VND (`*_vnd`).
- `order_items`, `order_timeline_events`, `return_requests`, `outbox_events` (index `WHERE published_at IS NULL`), `processed_events(event_id PK)`, `dead_letter_events`.

## 2. Trình tự Saga `CreateCheckout` (`services/order/internal/service/checkout.go`)

Mỗi bước dưới đây bám đúng thứ tự thực thi trong `OrderService.CreateCheckout`:

1. **Validate & Idempotency**:
   - Kiểm tra `idempotency_key`, auth (`service.go:requireAuthenticatedCaller`), `address_id`, `payment_method` (`COD`|`VIETQR`), `coins_to_use >= 0`.
   - Fast path `checkout.go:tryLoadIdempotentReplay`: nếu `checkout_groups.status == "COMPLETED"`, trả `checkout.go:buildReplayResponse` (`idempotent_replay=true`, ưu tiên unmarshal `response_snapshot_json`).
   - Khóa nội bộ theo `buyerID + ":" + idemKey` (`service.go:keyedLocker.Lock`), rồi `checkout.go:acquireCheckoutGroup`: chèn `checkout_groups(status='IN_PROGRESS')`; nếu pod khác đang chạy cùng key thì poll mỗi `50ms` tối đa `15s`, hết hạn trả `Aborted`.
   - Nếu lỗi trước khi lưu đơn con: closure `failGroupBeforePersist` gọi `store.FailCheckoutGroup` (dưới `detachedCompensationContext`) đánh dấu `FAILED` để key thử lại được.
2. **Step A — Xác thực người mua & địa chỉ**: `User.ValidateUser` (yêu cầu `USER_STATUS_ACTIVE`) → `User.GetAddress` (kiểm chủ sở hữu địa chỉ).
3. **Step B — Gom item & tách kế hoạch theo shop**:
   - `checkout.go:resolveCheckoutInputs`: đọc `Cart.GetCart` nếu `from_cart=true` (lọc theo `selected` nếu có item được chọn, giữ `flash_sale_item_id` override từ request) hoặc dùng `req.Items`; gộp trùng `sku_id` và sort theo `sku_id`.
   - `Product.GetSKUsBatch` → `checkout.go:buildShopPlans`: kiểm `shop_id`, `is_active`, `price_vnd > 0`, `available_stock >= qty`, gom thành 1 `shopCheckoutPlan` cho mỗi `shop_id`, sort `plans` theo `shopID` và `stockItems` theo `SkuId`.
4. **Step C — Tính phí vận chuyển từng shop**: `checkout.go:calculateShippingFees` → tra địa chỉ lấy hàng (`checkout.go:resolveShopPickupCodes` gọi `User.GetShop` + `User.GetAddress` bằng `clients.WithInternalAdminContext`) → `Shipping.CalculateFee` → chọn báo giá (`checkout.go:pickShippingQuote`: khớp `shipping_choices` hoặc rẻ nhất).
5. **Step D — Kiểm tra rủi ro COD sớm**: nếu `COD`, kiểm `cod_rejected_count >= ORDER_COD_MAX_REJECTED_COUNT` (`3`). Chỉ kiểm `preTotal > ORDER_COD_MAX_VND` (`5_000_000`) ở bước này khi `!hasPromotions`.
6. **Saga Step 1 — Giữ tồn kho**: `Product.ReserveStock(OrderId=checkoutGroupID, Items=allStockItems)` (mang JWT người mua, idempotent theo `checkoutGroupID`). Lỗi → `failGroupBeforePersist`.
7. **Saga Step 2 — Giữ khuyến mãi (chỉ khi `hasPromotions`)**:
   - `checkout.go:buildReservePromotionRequest` → `Promotion.ReservePromotion(OrderId=checkoutGroupID, ...)`.
   - **Bù trừ nếu lỗi**: `checkout.go:compensateReleaseStock` + `failGroupBeforePersist`.
   - Nếu có flash sale item: gọi `checkout.go:lookupFlashSalePrices` (`Promotion.ListActiveFlashSales`).
   - `checkout.go:applyDiscountsAndTotals`: áp breakdown theo shop (fallback `service.go:AllocateProportionalVND`), tính `totalPayableVND` (int64, clamp `>= 0`) và `commissionVND` theo `sku.commission_rate_bps` (fallback `ORDER_DEFAULT_COMMISSION_BPS=500`, cap ≤ `allocSubtotal`).
   - **Kiểm tra trần COD sau khuyến mãi**: nếu `COD && grandTotalPayableVND > CODMaxVND` → bù trừ `compensateReleasePromotion` + `compensateReleaseStock` + `failGroupBeforePersist`.
8. **Saga Step 3 — Lưu sub-orders + timeline + outbox trong 1 TX**:
   - Nếu `COD` hoặc `grandTotalPayableVND == 0`: khởi tạo `CONFIRMED` (`payment_status="COD_PENDING"` hoặc `"SUCCEEDED"` nếu 0 VND). Ngược lại (`VIETQR > 0`): `PENDING`, `payment_expires_at = now + ORDER_PAYMENT_EXPIRY_MINUTES` (`15m`).
   - `checkout.go:buildCheckoutBatch` dựng 1 đơn con/`shop_id` (mã `NM-YYYYMMDD-XXXXXXXX`), `order_items`, `order_timeline_events`, `outbox_events` (`order.created`; nếu `CONFIRMED` ghi thêm `order.confirmed`).
   - `store/postgres.go:PersistCheckoutBatch` commit toàn bộ trong 1 transaction. **Bù trừ nếu lỗi**: `compensateReleasePromotion` + `compensateReleaseStock` + `failGroupBeforePersist`.
9. **Saga Step 4 — Tạo thanh toán (chỉ khi `grandTotalPayableVND > 0`)**:
   - `checkout.go:buildCreatePaymentRequest` (`IdempotencyKey="checkout:"+checkoutGroupID`, `shop_allocations` theo từng đơn con) → `Payment.CreatePayment`.
   - **Bù trừ toàn phần nếu lỗi**: dưới `detachedCompensationContext`, gọi `store/postgres.go:CancelCheckoutGroupOrders` (khóa `FOR UPDATE`, chuyển mọi sub-order sang `CANCELLED`, ghi timeline + outbox `order.cancelled`, đặt group `FAILED`), sau đó `compensateReleasePromotion` → `compensateReleaseStock` → `notifyOutbox()`.
10. **Xác nhận tồn cho đơn khởi tạo `CONFIRMED`, lưu snapshot & dọn giỏ**:
    - Nếu `initialStatus == CONFIRMED` (COD hoặc 0 VND): gọi `checkout.go:confirmStockBestEffort` (`Product.ConfirmStock`).
    - `store/postgres.go:CompleteCheckoutWithPayment`: cập nhật `payment_id`/`payment_status`/`qr_url`/`qr_code`/`response_snapshot_json` và `checkout_groups.status='COMPLETED'` trong 1 TX.
    - **Saga Step 5**: best-effort `Cart.ClearItems(buyerID, skuIDs)` → `service.go:notifyOutbox`.

## 3. Bù trừ, Service Token & Allowlist gRPC

- `service.go:detachedCompensationContext(parent, timeout)`: bọc `context.WithoutCancel(parent)` + `clients.WithInternalAdminContext` + timeout `ORDER_GRPC_TIMEOUT` (`10s`), bảo đảm lời gọi bù trừ vẫn chạy xong kể cả khi client ngắt request.
- `services/order/internal/clients/clients.go:WithInternalAdminContext`: xóa raw JWT người mua và gắn claim `Service: "order-service"`, `Role: "admin"`.
- `clients.go:timeoutAndAuthClientInterceptor`: khi `claims.Service != ""` sẽ gọi `svc.SignServiceToken("order-service")` (ký bằng `SERVICE_JWT_SECRET`, TTL `1m` ở `pkg/common/auth/auth.go:SignServiceToken`); ngược lại chuyển tiếp JWT người mua.
- `clients.go:RetryIdempotent`: retry tối đa `3` lần, backoff `50ms` nhân đôi cho `Unavailable`, `DeadlineExceeded`, `ResourceExhausted`. Dùng trong `compensateReleaseStock`, `compensateReleasePromotion`, `confirmStockBestEffort`, `compensateCancelledOrder`, `HandlePaymentResult`, `BuyerConfirmReceived`.
- **Hủy đơn sau checkout** (`lifecycle.go:CancelOrder` → `lifecycle.go:compensateCancelledOrder`):
  - Nếu đơn đang `PENDING`: `store/postgres.go:TransitionOrderStatus` (`CancelGroupPend=true`) khóa mọi đơn cùng `checkout_group_id` theo thứ tự `shop_id ASC, created_at ASC, id ASC` (tránh AB-BA deadlock với `payment.succeeded`) và hủy cả nhóm `PENDING`, rồi gọi `ReleaseStock`, `ReleasePromotion`, `Payment.CancelPayment`.
  - Nếu đơn đang `CONFIRMED`/`PREPARING`: chỉ hủy đơn đó; nếu không còn sibling active thì gọi `ReleasePromotion` + `ReleaseStock` (nếu đã confirm thì fallback `lifecycle.go:restockOrderItems`), ngược lại chỉ `restockOrderItems`. Nếu đã trả VietQR thì gọi `Payment.CreateRefund` (`IdempotencyKey="order-cancel-refund:"+orderID`), nếu chưa trả và không còn sibling thì `Payment.CancelPayment`.
- **Allowlist nhận token service**:
  - Go `pkg/common/auth/auth.go:internalRPCs`: `UserService/{GetAddress,GetProfile}`, `ProductService/{ReleaseStock,ConfirmStock}`, `PromotionService/ReleasePromotion` (`true`). `ProductService/ReserveStock` và `PromotionService/ReservePromotion` đặt `false` (chỉ nhận JWT người mua).
  - Rust `services/payment/src/auth.rs:SERVICE_METHODS`: `GetPayment`, `CancelPayment`, `CreateRefund`, `ReleaseEscrow`.

## 4. Outbox, Consumer NATS & Payment Expiry Watchdog

- **Outbox** (`events/outbox.go:OutboxRelay`, `store/postgres.go:ProcessPendingOutbox`): poll mỗi `250ms` (`ORDER_OUTBOX_POLL_INTERVAL`) hoặc ngay khi `OutboxTrigger()` nhận tín hiệu; khóa batch `50` dòng bằng `queries/orders.sql:LockPendingOutboxEvents` (`FOR UPDATE SKIP LOCKED`), đóng gói `events.Envelope`, đặt header `Nats-Msg-Id = ev.ID` và `traceparent`, publish lên stream `NEXUSMART`.
  - Subject `order` phát ra (`events/types.go`): `order.created`, `order.confirmed`, `order.cancelled`, `order.shipped`, `order.delivered`, `order.completed`.
- **Consumer** (`events/consumer.go:Consumer.Start`, `ProcessRawMessage`): queue-subscribe `payment.>` (`order-payment-consumer`) và `shipment.>` (`order-shipment-consumer`), `AckWait=30s`, `defaultMaxDeliverAttempts=5`; quá 5 lần hoặc JSON lỗi gọi `sendToDLQ` (ghi bảng `dead_letter_events` + publish `dlq.<subject>`) rồi `Ack`.
  - **Kết quả thanh toán** (`handlers.go:HandlePaymentSucceeded`, `HandlePaymentFailed`, `HandlePaymentExpired` → `handlers.go:HandlePaymentResult`; cộng thêm RPC nội bộ `lifecycle.go:MarkPaymentResult`):
    1. Kiểm `IsEventProcessed(event_id)`.
    2. Nếu nhóm còn đơn `PENDING`, chạy gRPC downstream **trước** khi commit `processed_events` để lỗi tạm thời kích hoạt NATS redelivery: `SUCCEEDED` → `Product.ConfirmStock`; `FAILED|EXPIRED` → `Product.ReleaseStock` + `Promotion.ReleasePromotion` (bỏ qua `NotFound`).
    3. `store/postgres.go:TransitionOrdersPaymentResult` chèn `processed_events`, chuyển đơn `PENDING → CONFIRMED|CANCELLED`, ghi timeline + outbox (`order.confirmed` / `order.cancelled`) trong 1 TX.
  - **Vận chuyển & hoàn tiền**: `handlers.go:HandlePaymentRefunded` → `store/postgres.go:RecordPaymentRefunded`; `handlers.go:HandleShipmentUpdated` → `store/postgres.go:ApplyShipmentUpdate` (`delivered` → `DELIVERED` + outbox `order.delivered`; `returned` → `RETURNED`, rồi `restockOrderItems` + `Payment.CreateRefund` với `IdempotencyKey="order-return-refund:"+orderID` nếu đã trả VietQR).
- **Expiry Watchdog** (`handlers.go:RunExpiryWatchdog`, `ScanExpiredPendingOrders`, `processExpiredGroup`): quét mỗi `10s` (`ORDER_EXPIRY_WATCHDOG_INTERVAL`, timeout `15s`) tối đa `20` đơn qua `queries/orders.sql:LockExpiredPendingOrders` (`FOR UPDATE SKIP LOCKED`). Trước khi hủy (UC-PAY06), luôn gọi `Payment.GetPayment`:
  - Nếu Payment đã `SUCCEEDED` → xác nhận đơn thay vì hủy.
  - Nếu gọi Payment lỗi (khác `NotFound`) → giữ nguyên để tick sau thử lại.
  - Nếu Payment vẫn `PENDING` → gọi `Payment.CancelPayment`, rồi `HandlePaymentResult(..., EXPIRED)`.

## 5. Máy trạng thái đơn hàng (`services/order/internal/statemachine/statemachine.go`)

`ValidateTransition` kiểm tra bảng `allowedTransitions`:

| Từ trạng thái | Sang trạng thái hợp lệ | Tác nhân / Hàm kích hoạt |
| --- | --- | --- |
| `PENDING` | `CONFIRMED`, `CANCELLED` | `HandlePaymentResult`, `processExpiredGroup`, `CancelOrder` |
| `CONFIRMED` | `PREPARING`, `SHIPPING`, `CANCELLED` | `SellerConfirmOrder`, `SellerShipOrder`, `CancelOrder` |
| `PREPARING` | `SHIPPING`, `CANCELLED` | `SellerShipOrder`, `CancelOrder` |
| `SHIPPING` | `DELIVERED`, `RETURNED` | `HandleShipmentUpdated` (`delivered` / `returned`) |
| `DELIVERED` | `COMPLETED`, `RETURN_REQUESTED` | `BuyerConfirmReceived`, `RequestReturn` (cửa sổ `7d` = `returnWindowDuration`) |
| `RETURN_REQUESTED` | `RETURNED`, `COMPLETED` | `HandleShipmentUpdated` (`returned`); nhánh `COMPLETED` mới có ở state machine |
| `COMPLETED` / `CANCELLED` / `RETURNED` | *(trạng thái kết thúc)* | — |

## 6. Optimistic Locking tồn kho SKU (`services/product`)

Schema `services/product/migrations/000001_init.sql`: `skus(stock, reserved_stock, version)` với `CHECK (reserved_stock <= stock)`; `stock_reservations(order_id, sku_id UNIQUE, status IN ('RESERVED','CONFIRMED','RELEASED'))`; `stock_history`.

Cơ chế (`services/product/internal/server/server.go`, `queries/product.sql`, `store/pg_store.go`):
- Sort SKU tăng dần (`normalizeAndSortReservationItems`) tránh deadlock; vòng lặp retry tối đa `maxOptimisticRetries = 8` (`retryBaseDelay = 5ms * attempt`) khi đụng `ErrVersionConflict` hoặc `ErrDuplicateReservation`, vượt trần trả `Aborted` (`STOCK_VERSION_CONFLICT`).
- `Server.ReserveStock`: idempotent theo `order_id` (`AlreadyReserved=true` nếu đã giữ/confirm; lỗi nếu đã `RELEASED`) → `ReserveSKUOptimistic` (`SET reserved_stock = reserved_stock + $1, version = version + 1 WHERE id = $2 AND version = $3 AND (stock - reserved_stock) >= $1`) + `CreateStockReservation` + `InsertStockHistory("RESERVE")`.
- `Server.ReleaseStock`: khóa `ListReservationsByOrderIDForUpdate` → chuyển `RESERVED → RELEASED` (`TransitionStockReservation`) → `ReleaseSKUOptimistic` (trừ `reserved_stock`, tăng `version` theo `WHERE version = $3`) + `InsertStockHistory("RELEASE")`.
- `Server.ConfirmStock`: khóa `ListReservationsByOrderIDForUpdate` → chuyển `RESERVED → CONFIRMED` → `ConfirmSKUOptimistic` (trừ cả `stock` và `reserved_stock`, tăng `version`, chuyển SKU `OUT_OF_STOCK` nếu `stock=0`) + `syncProductStockStatus` + `InsertStockHistory("CONFIRM")`.
- `Server.UpdateStock` (seller chỉnh tồn): `UpdateSKUStockOptimistic` (`WHERE id = $2 AND version = $3 AND $1 >= reserved_stock`), không tự retry để caller tải lại version mới.

## 7. Chỗ `docs/contracts.md` §6 lệch với code

1. **Kiểm tra COD**: `contracts.md` ghi kiểm `CODMaxVND` trước `ReserveStock`; code chỉ kiểm sớm khi `!hasPromotions`, còn khi có khuyến mãi thì kiểm lại sau `ReservePromotion` + `applyDiscountsAndTotals` rồi bù trừ nếu vượt (`checkout.go:213-228,263-276`).
2. **Đơn 0 VND & xác nhận tồn COD**: code cho cả `COD` lẫn `grandTotalPayableVND == 0` khởi tạo `CONFIRMED`, ghi sẵn `order.confirmed` vào outbox, bỏ qua `CreatePayment` khi 0 VND và gọi ngay `confirmStockBestEffort` (`checkout.go:283-289,341,388-390`).
3. **Cộng xu khi hoàn tất**: `contracts.md` ghi gọi `PromotionService.CreditCoins`; thực tế `lifecycle.go:BuyerConfirmReceived` chỉ ghi `order.completed` (mang `coins_reward`) vào outbox và gọi trực tiếp `Payment.ReleaseEscrow` (best-effort), còn `promotion` tự cộng xu khi tiêu thụ `order.completed` (`services/promotion/internal/events/consumer.go:ProcessRawMessage`).
4. **Auto-complete 7 ngày & đóng return**: `contracts.md` nêu 7d auto-complete (`DELIVERED → COMPLETED`) và từ chối trả hàng (`RETURN_REQUESTED → COMPLETED`), nhưng `services/order` chưa có job auto-complete 7 ngày và chưa có RPC từ chối trả hàng.
5. **Bù trừ `restockOrderItems` với `internalRPCs`**: `lifecycle.go:restockOrderItems` gọi `Product.GetSKUsBatch` và `Product.UpdateStock` bằng service token (`detachedCompensationContext`), nhưng 2 RPC này chưa nằm trong `pkg/common/auth/auth.go:internalRPCs` nên interceptor sẽ trả `PermissionDenied` cho token service (rủi ro hiện có trong code, chưa sửa trong đợt tài liệu này).
