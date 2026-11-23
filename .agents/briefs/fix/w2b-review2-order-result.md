# Kết quả Review độc lập lượt 2 — Đợt 2b (`services/order/**` sau r4)

> **Kiểm chứng read-only đã chạy:**
> - `cd services/order && go test -race -count=1 ./...` → **PASS**
> - `cd services/order && golangci-lint run --allow-parallel-runners ./...` → **PASS (0 issues)**
> - `TEST_ORDER_DB_URL=... go test -v -race -count=1 ./internal/store/... -run TestPostgresR4` (DB tạm tự tạo và đã xoá) → **PASS**

---

## 1. Đối chiếu các phát hiện lượt 1 & yêu cầu r4

| Mục | Trạng thái | Vị trí hiện tại & nhận xét |
|---|---|---|
| **P0-1** (`CompleteCheckoutWithPayment`: downgrade, deadlock, disconnect, retry, recovery) | **SỬA SAI / CHƯA TRIỆT ĐỂ** | Đã sửa non-downgrade cơ bản, đổi thứ tự khóa `orders -> checkout_groups`, detached ctx + 3 retry, thêm `ScanIncompleteCheckoutGroups` & tự phục hồi ở `acquireCheckoutGroup` ([checkout.go](services/order/internal/service/checkout.go#L484-L531), [checkout.go](services/order/internal/service/checkout.go#L676-L680), [checkout.go](services/order/internal/service/checkout.go#L804-L829), [postgres.go](services/order/internal/store/postgres.go#L156-L248), [orders.sql](services/order/queries/orders.sql#L36-L73), [orders.sql](services/order/queries/orders.sql#L214-L231), [handlers.go](services/order/internal/service/handlers.go#L550-L773)). **Tuy nhiên bản sửa sinh lỗi mới nghiêm trọng**: N3, N4, N5, N7, N8, N10. |
| **P0-2** (`processExpiredGroup` bỏ qua lỗi `CancelPayment`) | **ĐÃ SỬA** | [handlers.go](services/order/internal/service/handlers.go#L380-L480): kiểm tra lỗi `CancelPayment`, xử lý `NotFound` / `AlreadyExists\|FailedPrecondition` → gọi lại `GetPayment` phân nhánh `SUCCEEDED`/`REFUNDED`/`PENDING`, trả lỗi với lỗi tạm thời để tick sau chạy lại, dùng `resolvedPaymentID`. *(Các nhánh `CancelPayment` khác vẫn hở: xem N2, N5).* |
| **Manager P1 / Mục C** (lỗi mơ hồ `ReserveStock`/`ReservePromotion` + `needs_cleanup`) | **SỬA CHƯA TRIỆT ĐỂ** | [0011_w2b_order_round4.sql](services/order/migrations/0011_w2b_order_round4.sql#L29-L34), [checkout.go](services/order/internal/service/checkout.go#L302-L322), [checkout.go](services/order/internal/service/checkout.go#L840-L894), [orders.sql](services/order/queries/orders.sql#L84-L90), [orders.sql](services/order/queries/orders.sql#L568-L580). Nhưng `PersistCheckoutBatch` lỗi mơ hồ vẫn gọi `compensateBeforePersistAndFail` (N1), `needs_cleanup` khoá `Idempotency-Key` 10–15s (N9), và `ReserveStock` lỗi mơ hồ gọi thừa `ReleasePromotion` (N12). |
| **P1-1** (Báo cáo khớp `compute_settlement_split`) | **ĐÃ SỬA** *(còn 1 race biên)* | [0011_w2b_order_round4.sql](services/order/migrations/0011_w2b_order_round4.sql#L5-L27), [orders.sql](services/order/queries/orders.sql#L347-L354), [orders.sql](services/order/queries/orders.sql#L762-L846), [memory.go](services/order/internal/store/memory.go#L1318), [memory.go](services/order/internal/store/memory.go#L2166-L2189). Đã kiểm tra đại số khớp 5 ca waterfall của [domain.rs](services/payment/src/domain.rs#L1069-L1130). Còn race bất đồng bộ `payment.refunded` tới sau `BuyerConfirmReceived` (N11). |
| **P2-1** (`CreateCheckoutResponse.warnings`) | **SỬA CHƯA TRIỆT ĐỂ** | [order.proto](proto/nexusmart/order/v1/order.proto#L279), [checkout.go](services/order/internal/service/checkout.go#L325-L327), [checkout.go](services/order/internal/service/checkout.go#L497-L526), gateway [handlers_order.go](services/gateway/internal/router/handlers_order.go#L547-L563). **Bị mất** khi phục hồi hoặc retry đồng thời (N4, N7). |
| **P2-2** (`ListActiveShopDiscounts` batch 200) | **ĐÃ SỬA** | [checkout.go](services/order/internal/service/checkout.go#L1369), [checkout.go](services/order/internal/service/checkout.go#L1397-L1405). |
| **P2-3** (`gmv_vnd = SUM(P + D)` loại `PENDING`/`CANCELLED`/`RETURNED`) | **ĐÃ SỬA** | [orders.sql](services/order/queries/orders.sql#L732-L737), [orders.sql](services/order/queries/orders.sql#L750-L755), [memory.go](services/order/internal/store/memory.go#L2150-L2153), [reports.go](services/order/internal/service/reports.go#L109-L110). |
| **P2-4** (Trùng `sku_id` xung đột `flash_sale_item_id` & `TrimSpace`) | **ĐÃ SỬA** | [checkout.go](services/order/internal/service/checkout.go#L910-L921), [checkout.go](services/order/internal/service/checkout.go#L962-L966). |
| **P2-5** (CSV injection) | **ĐÃ SỬA** | Ngoài `services/order`; đã xác nhận ở [handlers_order.go](services/gateway/internal/router/handlers_order.go#L1355-L1395). |
| **P3-1** (`order.completed` có `payment_fee_vnd`) | **ĐÃ SỬA** | [types.go](services/order/internal/events/types.go#L126), [service.go](services/order/internal/service/service.go#L224). |
| **P3-2** (Bỏ `ShopProgramItemId` khỏi hash & gateway) | **ĐÃ SỬA** | [checkout.go](services/order/internal/service/checkout.go#L553-L643), gateway [handlers_order.go](services/gateway/internal/router/handlers_order.go#L478-L506). |
| **P3-3** (Xoá fallback chia tỷ lệ chết) | **ĐÃ SỬA** *(sót 1 biên)* | [checkout.go](services/order/internal/service/checkout.go#L1478-L1497). Còn sót check `TotalShopProgramDiscountVnd > 0` khi `Shops` rỗng (N12) và để lại hàm chết `AllocateProportionalVND` (N13). |
| **P3-4** (`DailyReportPoint.platform_subsidy_vnd`) | **ĐÃ SỬA** | [order.proto](proto/nexusmart/order/v1/order.proto#L438), [reports.go](services/order/internal/service/reports.go#L170-L185), gateway [handlers_order.go](services/gateway/internal/router/handlers_order.go#L1180). |
| **P3-5** (`ComputeOrderRewardCoins` không trừ `S_shop`) | **ĐÃ SỬA** | [service.go](services/order/internal/service/service.go#L307-L318). *(Tài liệu [contracts.md](docs/contracts.md#L519) chưa cập nhật, xem N13).* |
| **P3-6** (`CodAmountVnd` pre-discount estimate) | **ĐÃ GHI NHẬN** | Đã ghi rõ tại [contracts.md](docs/contracts.md#L500). |
| **Mục H** (Ẩn phí seller với người mua trên mọi RPC) | **SỬA CHƯA TRIỆT ĐỂ** | [service.go](services/order/internal/service/service.go#L93-L98), [service.go](services/order/internal/service/service.go#L320-L388) che đúng trên mọi RPC đọc/sửa đơn mới; timeline và `ReturnRequest` không chứa phí. **Nhưng `buildReplayResponse` vẫn làm lộ phí nếu replay `response_snapshot_json` cũ trước r4 (N6).** |

---

## 2. Phát hiện MỚI (xếp theo mức nghiêm trọng thực tế)

### P0 — Mất/lệch tiền, mất hàng hoặc lệch dữ liệu tài chính/tồn kho không tự phục hồi

#### N1 [P0]: `PersistCheckoutBatch` lỗi commit mơ hồ → nhả tồn kho + khuyến mãi và đánh `checkout_groups = FAILED` trong khi `orders` COD/0đ đã sống (`CONFIRMED`)
- **Vị trí:** [checkout.go](services/order/internal/service/checkout.go#L401-L405), [postgres.go](services/order/internal/store/postgres.go#L94-L154)
- **Kịch bản cụ thể:**
  1. `PersistCheckoutBatch(ctx, batch)` chạy bằng `ctx` client (không detach). Client ngắt kết nối hoặc DB timeout ngay tại `tx.Commit(ctx)` ([postgres.go](services/order/internal/store/postgres.go#L150)) **sau khi** Postgres đã commit TX.
  2. Lúc này `orders` (với COD/0đ là `CONFIRMED`, `pending_stock_confirm = true`) + outbox (`order.created`, `order.confirmed`) **đã tồn tại**.
  3. Nhánh lỗi [checkout.go](services/order/internal/service/checkout.go#L402-L405) gọi `compensateBeforePersistAndFail` → gọi `ReleaseStock` (nhả kho + ghi tombstone → `ScanPendingStockConfirms` về sau thấy `Released=true` và xoá cờ mà **không trừ kho** → oversell), gọi `ReleasePromotion` (hoàn voucher/xu/flash slot cho khách), và gọi `FailCheckoutGroup` (`status = 'FAILED'`).
  4. **Hậu quả tài chính:** Seller nhận đơn `CONFIRMED`, xuất hàng giao thật, nhưng `Payment.CreatePayment` chưa từng chạy → không có row `payments`/`payment_shop_allocations` → khi giao xong, sổ cái `payment` không ghi nhận khoản thu/trợ giá → **seller mất trắng tiền hàng**; `ScanIncompleteCheckoutGroups` bỏ qua nhóm `FAILED`; khách retry cùng `Idempotency-Key` tạo luôn đơn trùng thứ 2 (`WHERE status <> 'FAILED'`).
  5. Nếu compensation lại lỗi, `MarkCheckoutGroupNeedsCleanup` đặt `needs_cleanup = TRUE` trên nhóm *đã có* `orders`, nên `FindStaleInProgressCheckoutGroups` (`NOT EXISTS orders`) bỏ qua còn `acquireCheckoutGroup` (`!existing.NeedsCleanup`) cũng bỏ qua.
- **Đề xuất sửa (1 dòng):** Dùng detached context cho `PersistCheckoutBatch` và khi trả lỗi mơ hồ phải kiểm tra `ListOrdersByCheckoutGroup` trước khi quyết định compensation (nếu orders đã ghi thì xử lý như lỗi post-persist, tuyệt đối không gọi `compensateBeforePersistAndFail`).

#### N2 [P0]: `CreatePayment` lỗi mơ hồ không kiểm tra/huỷ payment + vẫn nhả kho/khuyến mãi khi huỷ đơn thất bại → kẹt tiền khách trong `escrow_liability` hoặc huỷ đơn đã `SUCCEEDED`
- **Vị trí:** [checkout.go](services/order/internal/service/checkout.go#L419-L455), [postgres.go](services/order/internal/store/postgres.go#L338-L433), đối chiếu [service.rs](services/payment/src/service.rs#L3820-L3823)
- **Kịch bản cụ thể:**
  1. `CreatePayment(ctx, payReq)` dùng `ctx` client. Client disconnect/timeout sau khi `payment` đã commit `P1` (`status = 'pending'`).
  2. [checkout.go](services/order/internal/service/checkout.go#L424) gọi `CancelCheckoutGroupOrders` (`PaymentStatus: "FAILED"`, `WasPaid: false`) mà **không gọi `Payment.GetPayment`/`Payment.CancelPayment`**. Nếu sự kiện `order.cancelled` tới `payment` trước khi TX chậm của `CreatePayment` commit, `process_order_cancelled_event` ([service.rs](services/payment/src/service.rs#L3820-L3823)) thấy không có payment và đánh dấu đã xử lý (no-op) → `P1` tiếp tục `pending`; khách chuyển khoản VietQR → webhook ghi có `escrow_liability`, còn `order` bỏ qua `payment.succeeded` trên đơn `CANCELLED` ([postgres.go](services/order/internal/store/postgres.go#L667)) → **tiền khách kẹt vĩnh viễn trong escrow không tự hoàn**.
  3. Nếu webhook `payment.succeeded` đã vào trước `CancelCheckoutGroupOrders`: đơn đang `CONFIRMED / SUCCEEDED`, nhưng `CancelCheckoutGroupOrders` ([postgres.go](services/order/internal/store/postgres.go#L357-L398)) cho phép chuyển `CONFIRMED -> CANCELLED`, **ghi đè `payment_status = 'FAILED'`, phát `WasPaid: false` (không gọi `CreateRefund`)**, đánh `checkout_groups = 'FAILED'`, và chỉ gọi `releaseStockErr` bỏ qua `released=false` (rò tồn kho đã confirm).
  4. Nếu `CancelCheckoutGroupOrders` lỗi ([checkout.go](services/order/internal/service/checkout.go#L433-L436)), dòng 438–447 **vẫn tiếp tục gọi `releasePromotionErr` và `releaseStockErr`** trên các đơn chưa hề bị huỷ!
- **Đề xuất sửa (1 dòng):** Khi `CreatePayment` gặp lỗi mơ hồ, phải probe `GetPayment(OrderGroupId)` / `CancelPayment(OrderGroupId)` (nếu đã `SUCCEEDED` thì finalize chứ không huỷ; nếu không chắc thì để watchdog xử lý), và dừng ngay không nhả kho/khuyến mãi nếu `CancelCheckoutGroupOrders` trả lỗi.

#### N3 [P0]: `ScanIncompleteCheckoutGroups` trên đơn COD / 0đ bị crash trước `CreatePayment` → mất hàng (không huỷ vận đơn), nhả khuyến mãi trên đơn đã giao, và rò tồn kho 100%
- **Vị trí:** [handlers.go](services/order/internal/service/handlers.go#L573-L620), [postgres.go](services/order/internal/store/postgres.go#L338-L433), đối chiếu [server.go](services/product/internal/server/server.go#L1616-L1621)
- **Kịch bản cụ thể:**
  1. Đơn COD hoặc 0đ được tạo `status = CONFIRMED` + `pending_stock_confirm = true` ở `PersistCheckoutBatch`, rồi pod crash trước `CreatePayment` (`checkout_groups.status = 'IN_PROGRESS'`).
  2. **Rò tồn kho 100% (kể cả seller chưa làm gì):** Trong 10 giây, `ScanPendingStockConfirms` ([handlers.go](services/order/internal/service/handlers.go#L923-L955)) gọi `Product.ConfirmStock(groupID)` chuyển reservation `RESERVED -> CONFIRMED`. Sau 5 phút (`StaleCheckoutAfter`), `reconcileIncompleteCheckoutGroup` thấy `GetPayment == NotFound`, gọi `CancelCheckoutGroupOrders` rồi gọi `releaseStockErr` ([handlers.go](services/order/internal/service/handlers.go#L609)). `Product.ReleaseStock` trả `Released=false, err=nil` cho reservation đã `CONFIRMED` ([server.go](services/product/internal/server/server.go#L1616-L1621)); dòng 609 bỏ qua `released=false`, xoá `pending_cancel_compensation`, không bao giờ gọi `RestockOrderItems` → **tồn kho mất vĩnh viễn**.
  3. **Mất hàng nếu seller đã đóng gói (`PREPARING` + `tracking_code`):** Trong 5 phút đó seller có thể `SellerShipOrder`. `CancelCheckoutGroupOrders` không có `checkCancelShipmentGuard` và không gọi `Shipping.CancelShipment` → huỷ đơn nhưng vận đơn vẫn giao hàng cho khách.
  4. **Hoàn khuyến mãi/xu miễn phí trên đơn đã `SHIPPING`/`DELIVERED`:** Nếu đơn đã sang `SHIPPING`/`DELIVERED` trong 5 phút đó, `CancelCheckoutGroupOrders` bỏ qua đơn (`continue` tại [postgres.go](services/order/internal/store/postgres.go#L358)) nhưng **vẫn gọi `FailCheckoutGroup`** ([postgres.go](services/order/internal/store/postgres.go#L416)) và [handlers.go](services/order/internal/service/handlers.go#L605) **vẫn gọi `ReleasePromotion(groupID)`** → trả lại toàn bộ voucher/xu/flash slot cho khách trong khi hàng vẫn đang/đã giao!
- **Đề xuất sửa (1 dòng):** Trong `reconcileIncompleteCheckoutGroup`, nếu nhóm có đơn COD/0đ đã `CONFIRMED`+ (đặc biệt đang có tracking/`SHIPPING`/`DELIVERED`) mà `GetPayment == NotFound`, phải gọi lại `Payment.CreatePayment` idempotent (`"checkout:"+groupID`) để hoàn tất thay vì huỷ; còn nhánh huỷ chỉ được áp cho nhóm toàn đơn chưa có tracking và phải gọi `restockOrderItemsWithConfirmErr` khi `released == false`.

---

### P1 — Hỏng luồng chính / đua trạng thái nghiêm trọng

#### N4 [P1]: `CompleteCheckoutGroup` thiếu guard `status = 'IN_PROGRESS'` & `AttachCheckoutPaymentToOrders` thiếu guard `payment_id` → đua làm hỏng `RetryPayment`, ghi đè nhóm `FAILED`, xoá `warnings`
- **Vị trí:** [orders.sql](services/order/queries/orders.sql#L36-L73), [orders.sql](services/order/queries/orders.sql#L214-L231), [postgres.go](services/order/internal/store/postgres.go#L156-L248), [memory.go](services/order/internal/store/memory.go#L266-L351)
- **Kịch bản cụ thể:**
  1. Pod A nhận `P1` (`exp1 = T+5m`), `CompleteCheckoutWithPayment(P1)` lỗi lần 1 và đang chờ retry. Pod B phục hồi nhóm thành `COMPLETED` (`P1`), buyer lập tức gọi `RetryPayment(P1)` tạo `P2` (`exp2 = T+15m`, `UpdateCheckoutGroupPaymentOnRetry` đặt `response_snapshot_json = NULL`).
  2. Lần retry của Pod A chạy `CompleteCheckoutWithPayment(P1)`: vì `orders.payment_status` vẫn `'PENDING'`, `AttachCheckoutPaymentToOrders` ([orders.sql](services/order/queries/orders.sql#L224-L231)) **ghi đè `orders.payment_expires_at` từ `exp2` ngược về `exp1`**; `CompleteCheckoutGroup` ([orders.sql](services/order/queries/orders.sql#L58-L72)) thiếu `AND status = 'IN_PROGRESS'` nên **ghi đè `checkout_groups.payment_expires_at` về `exp1`** và ghi lại `response_snapshot_json` cũ lên trên `NULL` → watchdog huỷ sớm đơn `P2` của khách trước 10 phút.
  3. Tương tự: lời gọi `CompleteCheckoutWithPayment` trễ vẫn ghi `payment_id`/`response_snapshot_json` vào nhóm đã `FAILED`, hoặc ghi đè `warnings = []` lên snapshot đã có `warnings`.
- **Đề xuất sửa (1 dòng):** Kiểm tra `if grp.Status != "IN_PROGRESS" { return nil }` ngay sau `GetCheckoutGroupByIDForUpdate` trong `CompleteCheckoutWithPayment`, đồng thời thêm `AND status = 'IN_PROGRESS'` vào `CompleteCheckoutGroup` và `AND (payment_id = '' OR payment_id = $2)` vào `AttachCheckoutPaymentToOrders`.

#### N5 [P1]: `CancelOrder` chạy trước `CompleteCheckoutWithPayment` bỏ qua `Payment.CancelPayment` vì `prevOrd.PaymentID == ""` → để lọt payment VietQR `pending` sống
- **Vị trí:** [lifecycle.go](services/order/internal/service/lifecycle.go#L395-L414), [lifecycle.go](services/order/internal/service/lifecycle.go#L458), [checkout.go](services/order/internal/service/checkout.go#L484-L501), đối chiếu [service.rs](services/payment/src/service.rs#L524-L545) và [service.rs](services/payment/src/service.rs#L3820-L3823)
- **Kịch bản cụ thể:**
  1. Buyer thấy đơn `PENDING` (`payment_id = ''`) vừa persist và gọi `CancelOrder` trong lúc `CreatePayment` đang chạy hoặc vừa trả `P1` trước khi `CompleteCheckoutWithPayment` chạy.
  2. `cancelPaymentIgnoreTerminal` ([lifecycle.go](services/order/internal/service/lifecycle.go#L399)) và dòng 458 đều bỏ qua nếu `prevOrd.PaymentID == ""`, dù `PaymentService.CancelPayment` ([service.rs](services/payment/src/service.rs#L524-L545)) đã hỗ trợ huỷ bằng `order_group_id`.
  3. Nếu sự kiện `order.cancelled` đến `payment` trước khi `CreatePayment` commit (no-op tại [service.rs](services/payment/src/service.rs#L3820)), rồi `CompleteCheckoutWithPayment` chạy thấy đơn đã `CANCELLED` nhưng cũng không gọi `CancelPayment(P1)` → `P1` vẫn sống `pending` ở `payment` dù toàn bộ đơn đã `CANCELLED`.
- **Đề xuất sửa (1 dòng):** Cho phép `cancelPaymentIgnoreTerminal` gọi `CancelPayment(OrderGroupId: groupID)` cả khi `prevOrd.PaymentID == ""`, và nếu `CompleteCheckoutWithPayment` thấy tất cả đơn đã `CANCELLED` thì chủ động gọi `CancelPayment(P1)`.

---

### P2 — Vi phạm đặc tả / biên trạng thái / lộ dữ liệu biên

#### N6 [P2]: Rò phí hoa hồng và phí thanh toán cho người mua khi replay `response_snapshot_json` cũ (trước r4)
- **Vị trí:** [checkout.go](services/order/internal/service/checkout.go#L723-L738)
- **Kịch bản cụ thể:** Nhóm checkout tạo trước r4 lưu `response_snapshot_json` chứa `commission_vnd`, `payment_fee_vnd`, `payment_fee_bps`, `items[].commission_bps` khác 0. Khi buyer (`Role == "buyer"`, `ShopID == ""`) gọi lại `CreateCheckout` với cùng `Idempotency-Key`, `buildReplayResponse` bỏ qua nhánh dòng 728 và trả thẳng snapshot chưa che phí cho buyer. Ngoài ra, nếu `ListOrdersByCheckoutGroup` lỗi ở dòng 729 với admin/seller, code im lặng trả snapshot bị che phí (0đ) thay vì báo lỗi.
- **Đề xuất sửa (1 dòng):** Sau `protojson.Unmarshal` ở [checkout.go](services/order/internal/service/checkout.go#L726), luôn xoá về 0 các trường phí (và `NeedsManualReview` nếu `!caller.IsPrivileged`) cho mọi `ord` không đạt `canViewOrderFees(caller, ord.ShopId)`.

#### N7 [P2]: Mất `CreateCheckoutResponse.warnings` khi phục hồi hoặc khi 2 request cùng `Idempotency-Key` chạy song song trên 2 pod
- **Vị trí:** [checkout.go](services/order/internal/service/checkout.go#L676-L680), [handlers.go](services/order/internal/service/handlers.go#L766)
- **Kịch bản cụ thể:** `promoWarnings` không được lưu vào DB trước `CompleteCheckoutWithPayment`, nên mọi đường phục hồi qua `tryFinalizeInProgressGroupFromPayment` ([handlers.go](services/order/internal/service/handlers.go#L766)) đều tạo snapshot với `warnings = nil`. Hơn nữa `acquireCheckoutGroup` ([checkout.go](services/order/internal/service/checkout.go#L676)) gọi `tryFinalizeInProgressGroupFromPayment` **ngay lập tức** không chờ độ trễ staleness: request retry trên Pod B trong lúc Pod A vừa xong `CreatePayment` sẽ giành finalize trước (hoặc ghi đè sau do N4) và xoá sạch `warnings` ngay trong checkout bình thường.
- **Đề xuất sửa (1 dòng):** Lưu `warnings` vào `checkout_groups` khi persist batch, và trong `acquireCheckoutGroup` chỉ tự phục hồi nếu `time.Since(existing.UpdatedAt)` vượt quá ngưỡng grace (vd. 2–3s).

#### N8 [P2]: `resolveNonDowngradingGroupPaymentStatus` đánh sớm nhóm COD đa gian hàng thành `SUCCEEDED`/`REFUNDED` khi mới chỉ 1 đơn con thu tiền/hoàn tiền
- **Vị trí:** [store.go](services/order/internal/store/store.go#L354-L382)
- **Kịch bản cụ thể:** Nhóm COD 2 shop (`shop-1`, `shop-2`) bị trễ `CompleteCheckoutWithPayment`; `shop-1` giao trước nhận `payment.succeeded` (`SUCCEEDED`) trong khi `shop-2` vẫn `PENDING`. Khi `CompleteCheckoutWithPayment` chạy, vòng lặp dòng 367–372 trả ngay `"SUCCEEDED"` (hoặc `"REFUNDED"`) ngay khi gặp đơn con đầu tiên, bất chấp đơn anh em vẫn `PENDING` — trái với logic `allActivePaid` ở [postgres.go](services/order/internal/store/postgres.go#L774) và `shouldMarkGroupRefunded` ở [postgres.go](services/order/internal/store/postgres.go#L2192).
- **Đề xuất sửa (1 dòng):** Chỉ trả `"REFUNDED"` khi `shouldMarkGroupRefunded(orders, "", "", "")` đúng, và chỉ trả `"SUCCEEDED"` khi tất cả đơn active đều đã `SUCCEEDED`/`REFUNDED`.

#### N9 [P2]: `needs_cleanup = TRUE` khoá `Idempotency-Key` 10–15s rồi trả lỗi sai ngữ nghĩa `Aborted("concurrent checkout ... still in progress")`
- **Vị trí:** [checkout.go](services/order/internal/service/checkout.go#L676-L690), [checkout.go](services/order/internal/service/checkout.go#L883-L889)
- **Kịch bản cụ thể:** Checkout lỗi pre-persist và compensation lỗi tạm → đánh `needs_cleanup = TRUE`, giữ `status = 'IN_PROGRESS'`. Người mua bấm đặt lại ngay với cùng `Idempotency-Key`: `acquireCheckoutGroup` bỏ qua nhánh `NeedsCleanup` và ngủ vòng lặp 50ms chờ tick 10s của watchdog, dễ chạm timeout 15s và trả `Aborted("concurrent checkout ... still in progress")` dù không có checkout nào đang chạy.
- **Đề xuất sửa (1 dòng):** Khi `existing.NeedsCleanup == true` trong `acquireCheckoutGroup` (chưa có orders), thử gọi `cleanupStaleCheckoutGroup` trực tiếp trước khi ngủ chờ (hoặc trả `Unavailable` rõ ràng).

#### N10 [P2]: `RetryPayment` từ chối với `FailedPrecondition` nếu `CompleteCheckoutWithPayment` trước đó lỗi hết retry (`checkout_groups.payment_id == ""`)
- **Vị trí:** [lifecycle.go](services/order/internal/service/lifecycle.go#L954-L1010), [checkout.go](services/order/internal/service/checkout.go#L506-L531)
- **Kịch bản cụ thể:** `CreateCheckout` trả `payment_id = P1` cho buyer qua nhánh fallback [checkout.go](services/order/internal/service/checkout.go#L506-L531), nhưng DB vẫn `status = 'IN_PROGRESS', payment_id = ''`. Buyer gọi `RetryPayment(P1)`: dòng 963–984 đã tìm ra `group` qua `Payment.GetPayment(P1)`, nhưng dòng 1007 gặp `lockedGroup.PaymentID == ""` liền trả `FailedPrecondition` → buyer không thể retry trong 5 phút chờ watchdog (đơn flash sale 5 phút sẽ hết hạn hoàn toàn).
- **Đề xuất sửa (1 dòng):** Nếu `lockedGroup.Status == "IN_PROGRESS"` và `lockedGroup.PaymentID == ""`, gọi `tryFinalizeInProgressGroupFromPayment` rồi đọc lại `lockedGroup` trước khi kiểm tra dòng 1007.

#### N11 [P2]: Lệch báo cáo với sổ cái nếu `BuyerConfirmReceived` (hoặc auto-complete/return `CLOSED`) commit trước khi NATS giao `payment.refunded` của khoản hoàn tiền trước tất toán
- **Vị trí:** [orders.sql](services/order/queries/orders.sql#L347-L354), [postgres.go](services/order/internal/store/postgres.go#L2126-L2201)
- **Kịch bản cụ thể:** Admin hoàn một phần `96k` (`> raw_net = 92.4k`) khi đơn `DELIVERED` (`CreateRefund` đã commit ở `payment`), nhưng sự kiện `payment.refunded` đang trễ trên NATS. Buyer gọi `BuyerConfirmReceived` → `UpdateOrderCompleted` snapshot `settlement_refunded_vnd = 0` và phát `order.completed`. `payment` tất toán bằng `already_refunded = 96k` (`seller_net = 0, fees = 4k`), còn `order` khi nhận `payment.refunded` sau đó lại tính `96k` là post-settlement (`net_revenue = -3.6k, fees = 7.6k`, lệch 3.6k cả hai phía). *(Lưu ý thêm: backfill ở [0011_w2b_order_round4.sql](services/order/migrations/0011_w2b_order_round4.sql#L23-L27) gán `settlement_refunded_vnd = refunded_vnd` cho mọi đơn `COMPLETED` cũ sẽ tính lệch các đơn cũ có post-settlement refund `> raw_net`; đây là đánh đổi đã ghi chú do thiếu lịch sử tách pre/post).*
- **Đề xuất sửa (1 dòng):** Kèm cờ `is_post_settlement` trong event `payment.refunded` từ `payment` để `RecordPaymentRefunded` cộng thêm vào `settlement_refunded_vnd` nếu nhận khoản hoàn pre-settlement đến trễ.

---

### P3 — Biên nhẹ, code chết & over-engineering (`ponytail-review`)

#### N12 [P3]: `ReserveStock` lỗi mơ hồ gọi thừa `ReleasePromotion` khi chưa gọi `ReservePromotion`, và thiếu check `TotalShopProgramDiscountVnd` khi `breakdown.Shops` rỗng
- **Vị trí:** [checkout.go](services/order/internal/service/checkout.go#L302-L306), [checkout.go](services/order/internal/service/checkout.go#L1490-L1497)
- **Chi tiết:** (1) Ở dòng 305, `ReserveStock` mới là Step 1 (chưa gọi `ReservePromotion` và kể cả `!hasPromotions`) nhưng truyền `releasePromo = true`, tạo tombstone giả trong Redis+PG của `promotion` và làm hỏng cleanup (`needs_cleanup = TRUE`) nếu `promotion` đang down ở đơn không dùng khuyến mãi. (2) Ở dòng 1490–1496, kiểm tra `PROMOTION_BREAKDOWN_MISSING` bỏ sót `TotalShopProgramDiscountVnd > 0`: nếu `promotion` trả `Shops` rỗng nhưng đã trừ suất chương trình giảm giá shop, dòng 1551–1588 coi `appliedSet` rỗng (`computedShopProgramDiscount = 0 == p.shopProgramDiscountVND`) và lặng lẽ tính giá gốc cho khách mà không trả lỗi/nhả suất.
- **Đề xuất sửa (1 dòng):** Đổi tham số `releasePromo` ở dòng 305 thành `false`, và thêm `TotalFlashSaleDiscountVnd > 0 || TotalShopProgramDiscountVnd > 0` vào dòng 1491–1495.

#### N13 [P3]: Code chết & sai lệch nhỏ (`ponytail-review`)
- [service.go](services/order/internal/service/service.go#L263-L296): `AllocateProportionalVND` không còn caller production nào sau khi xoá fallback chia tỷ lệ (chỉ còn 1 test ở [service_test.go](services/order/internal/service/service_test.go#L953)) → nên xoá cả hàm lẫn test.
- [service.go](services/order/internal/service/service.go#L94): Nhánh `caller.IsService` trong `canViewOrderFees` là nhánh thừa vì toàn bộ RPC trả `Order` ở [auth.go](pkg/common/auth/auth.go#L107-L128) đều đặt `internalRPCs = false` và `canViewOrCancelOrder` không mở cho service token nếu không phải admin.
- [contracts.md](docs/contracts.md#L519): Vẫn ghi công thức xu thưởng cũ `max(0, R - V_plat - X)` thay vì công thức mới ở [service.go](services/order/internal/service/service.go#L307-L318) (không trừ `S_shop`).

---

## 3. Kiểm toán các test mới (liệu có đỏ thật khi bỏ bản sửa?)

### Các test **GIẢ / KHÔNG CHỨNG MINH ĐƯỢC** điều nó tuyên bố
1. **`TestW2B_R4_A_.../ClientDisconnectAfterCreatePayment_DetachedFinalizeAndClearCart` — phần `ConfirmStock`** ([w2b_round4_test.go](services/order/internal/service/w2b_round4_test.go#L229-L231)):
   - `mockProductClient.ConfirmStock` ([service_test.go](services/order/internal/service/service_test.go#L293-L301)) **bỏ qua hoàn toàn `ctx`** (`_ context.Context`), còn `clients.RetryIdempotent` ([clients.go](services/order/internal/clients/clients.go#L196)) gọi `fn(ctx)` trước khi kiểm tra `ctx.Done()`. Nếu bỏ `detachedCompensationContext` khỏi `confirmGroupStockAndClear`, assertion `len(h.product.confirmCalls) != 1` **vẫn xanh**. *(Hai assertion `wrapped.lastCtxErr` và `cartCtxErr` ở L223–228 thì đỏ thật).*
2. **`TestPostgresR4_A_.../ConcurrentCompleteAndPaymentResult_ZeroDeadlocks`** ([postgres_r4_test.go](services/order/internal/store/postgres_r4_test.go#L302-L375)):
   - Chỉ tạo **1 đơn con duy nhất** (`ordID := uuid.NewString()`). Trước r4, `UpdateOrderPaymentInfoByGroup` (khóa 1 hàng `orders`) → `CompleteCheckoutGroup` (khóa `checkout_groups`) đã cùng thứ tự khóa với `TransitionOrdersPaymentResult`; với 1 đơn con, deadlock AB-BA giữa các hàng `orders` là **không thể xảy ra**. Test chỉ đỏ nếu non-downgrade hỏng ở L368–373, **không chứng minh được chống deadlock đa gian hàng** (cần `>= 2` đơn con trong cùng nhóm).
3. **`TestW2B_R4_FGH_.../H_P3_...` — 2 nhánh kiểm quyền phí** ([w2b_round4_test.go](services/order/internal/service/w2b_round4_test.go#L1010-L1014), [w2b_round4_test.go](services/order/internal/service/w2b_round4_test.go#L1047-L1050)):
   - `svcCtx` đặt đồng thời `Role: "admin"` và `Service: "internal-svc"` nên không kiểm tra độc lập nhánh `caller.IsService`.
   - `seller2Ctx` (L1047) ghi comment *"Non-owning seller acting as buyer on another shop -> cannot see shop-1 fees either"*, nhưng `ordID` thuộc về `buyer-1` nên bị từ chối `PermissionDenied` trước khi vào `aggregateToProtoForCaller` — **không test ca seller mua hàng của shop khác**, và không test replay snapshot cũ (bỏ lọt N6).
4. **`TestPostgresR4_DE_.../Migration0011_BackfillsLegacyCompletedOrder`** ([postgres_r4_test.go](services/order/internal/store/postgres_r4_test.go#L666-L713)):
   - Comment L668 tuyên bố *"and preserves non-COMPLETED rows at 0"*, nhưng code **không chèn và không assert bất kỳ hàng non-COMPLETED nào** có `refunded_vnd > 0`.
5. **`TestW2B_R4_C_.../AmbiguousReserveStockError_CompensatesStockAndPromo`** ([w2b_round4_test.go](services/order/internal/service/w2b_round4_test.go#L511-L530)):
   - Đỏ thật khi gỡ `isAmbiguousReserveError`, nhưng lại assert cứng `len(h.promo.releaseCalls) != 1` cho đơn không có khuyến mãi và chưa tới bước `ReservePromotion` (khoá luôn lỗi thừa N12).

### Các test **CHỨNG MINH TỐT (đỏ thật khi bỏ bản sửa)**
- [w2b_round4_test.go](services/order/internal/service/w2b_round4_test.go): `RaceWithPaymentSucceeded_NoDowngradeAndFreshSnapshot`, `RaceWithCancelOrder_CancelledOrderUntouchedAndFreshSnapshot` *(phần không mutate đơn/snapshot)*, `CompleteCheckoutFailsAllRetries_IdempotentRetryRecoversImmediatelyWithoutHanging`, `WatchdogScanIncompleteCheckoutGroups_...` *(chỉ phủ VietQR)*, cả 3 subtest `TestW2B_R4_B_*`, `AmbiguousReservePromotionError_*`, `FailedCompensation_LeavesGroupForWatchdog*`, `TestW2B_R4_D_ReportSettlementWaterfallMatchesLedger`, `TestW2B_R4_E_GMVDefinitionAndDailySubsidy`, `F_WarningsForwardedAndReplayed`, `G_ListActiveShopDiscountsBatchesBy200`, `G_DuplicateSKUConflictingFlashIDRejected_AndTrimSpaceOverrides`, phần hash/reward coins/outbox `payment_fee_vnd`/buyer-vs-seller1-vs-admin masking của `H_P3_*`.
- [postgres_r4_test.go](services/order/internal/store/postgres_r4_test.go): `NoDowngradeWhenPaymentSucceededRacesAhead`, `CancelledOrderUntouchedAndSnapshotReflectsCancelled`, `WatchdogIncompleteAndNeedsCleanupQueries`, `SettlementWaterfallMatchesComputeSettlementSplit`, `GMVExcludesPendingCancelledReturnedAndIncludesPlatformSubsidy`.
- Các test cũ được chỉnh sửa (`audit_fix*_test.go`, `w2b_order_test.go`, `grpc_trust_test.go`, `service_test.go`): không bị làm yếu, chỉ cập nhật theo hợp đồng mới (bỏ `DefaultCommissionBPS`, thêm `GetFeeConfig`/`ListActiveShopDiscounts`, kiểm phí bằng `adminCtx`).

---

## 4. Các file đã đọc và không phát hiện vấn đề

- [0001_init.sql](services/order/migrations/0001_init.sql), [0010_w2b_order.sql](services/order/migrations/0010_w2b_order.sql)
- [migrate.go](services/order/internal/db/migrate.go)
- [main.go](services/order/cmd/server/main.go)
- [config.go](services/order/internal/config/config.go)
- [clients.go](services/order/internal/clients/clients.go)
- [server.go](services/order/internal/grpcserver/server.go)
- [types.go](services/order/internal/events/types.go), [outbox.go](services/order/internal/events/outbox.go), [consumer.go](services/order/internal/events/consumer.go)
- [reports.go](services/order/internal/service/reports.go), [returns.go](services/order/internal/service/returns.go)
- [postgres_returns.go](services/order/internal/store/postgres_returns.go), [memory_returns.go](services/order/internal/store/memory_returns.go)
- [order.proto](proto/nexusmart/order/v1/order.proto) (và kiểm khớp sqlc với `orders.sql.go`)
- Các file đối chiếu hợp đồng đã đọc: `services/product/internal/server/server.go`, `services/product/internal/store/{pg_store,memory_store}.go`, `services/promotion/internal/service/promotion.go`, `services/promotion/internal/store/store.go`, `services/promotion/internal/lua/release_atomic.lua`, `services/payment/src/{domain,service,grpc,error}.rs`, `services/gateway/internal/router/handlers_order.go`.

