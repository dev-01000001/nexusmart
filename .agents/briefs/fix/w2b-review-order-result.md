# Báo cáo review độc lập đợt 2b – `services/order` và các điểm tích hợp

> **Phạm vi & phương pháp**: Chỉ đọc tĩnh (không sửa file, không chạy lệnh ghi/git/container/test, không đọc báo cáo coder). Đối chiếu diff từ commit `d88221d` trên `services/order` cùng các điểm gọi chéo tới `promotion`, `payment`, `shipping`, `product`, `notification`, `gateway`, `proto/` và `pkg/common`.

---

## P0 – Tiền / Mất dữ liệu / Race condition nghiêm trọng

### P0-1. `CompleteCheckoutWithPayment` ghi đè `payment_status` khi `payment.succeeded` (hoặc `CancelOrder`) đến đồng thời + dùng `ctx` gốc và nuốt lỗi DB
- **Vị trí**:
  - [checkout.go:412-537](services/order/internal/service/checkout.go#L412-L537) (đặc biệt dòng `434`, `526-537`)
  - [postgres.go:156-187](services/order/internal/store/postgres.go#L156-L187), [memory.go:265-293](services/order/internal/store/memory.go#L265-L293)
  - [orders.sql:36-47](services/order/queries/orders.sql#L36-L47) (`CompleteCheckoutGroup`), [orders.sql:179-185](services/order/queries/orders.sql#L179-L185) (`UpdateOrderPaymentInfoByGroup`)
- **Mức**: **P0** (mất tiền kẹt vĩnh viễn trong `escrow_liability` / hỏng trạng thái thanh toán & kẹt idempotency)
- **Kịch bản cụ thể**:
  1. **Race mất tiền với `payment.succeeded`**:
     - `PersistCheckoutBatch` (`checkout.go:412`) commit đơn `PENDING` (`payment_id = ''`, `payment_status = 'PENDING'`).
     - `Payment.CreatePayment` (`checkout.go:434`) tạo thanh toán `P1` (`PENDING`).
     - Trước khi `CompleteCheckoutWithPayment` (`checkout.go:526`) commit, webhook VietQR xác nhận `P1` → sự kiện `payment.succeeded` tới `order` → `TransitionOrdersPaymentResult` thấy `payment_id == ""` nên chấp nhận `P1`, chuyển `orders.status = 'CONFIRMED'`, `orders.payment_status = 'SUCCEEDED'`, `orders.payment_id = P1`, `checkout_groups.payment_status = 'SUCCEEDED'`.
     - `CompleteCheckoutWithPayment` chạy sau đó gọi `UpdateOrderPaymentInfoByGroup` và `CompleteCheckoutGroup` **không có điều kiện chặn trên `payment_status` hiện tại**, ghi đè `orders.payment_status` và `checkout_groups.payment_status` từ `'SUCCEEDED'` **ngược về `'PENDING'`**.
     - Hệ quả: đơn đang `CONFIRMED` nhưng `payment_status = 'PENDING'`. Nếu giao thất bại (`SHIPPING → RETURNED`), `compensateReturnedOrder` ([handlers.go:340-344](services/order/internal/service/handlers.go#L340-L344)) kiểm tra `strings.EqualFold(ord.PaymentStatus, "SUCCEEDED")` → `false` nên bỏ qua `CreateRefund`; trong khi consumer `order.returned` ở `payment` ([domain.rs:1262-1291](services/payment/src/domain.rs#L1262-L1291)) cố tình nhường hoàn tiền đơn giao thất bại cho `order` → **tiền người mua kẹt vĩnh viễn trong `escrow_liability`**. Tương tự, `CancelOrder` đồng thời bị ghi đè `payment_status = 'CANCELLED'` về `'PENDING'`.
  2. **Nuốt lỗi DB / hủy `ctx` sau `CreatePayment`**:
     - Client ngắt kết nối hoặc gateway hết timeout ngay sau khi `CreatePayment` xong → `ctx` bị hủy → `CompleteCheckoutWithPayment(ctx, ...)` (`checkout.go:526`) lỗi `context.Canceled` nhưng dòng `535-537` chỉ log rồi bỏ qua.
     - Kết quả: `orders.payment_id = ''`, `checkout_groups.status = 'IN_PROGRESS'`; mọi retry idempotent bị treo 15s rồi trả `Aborted`, `RetryPayment` fail vì không có `payment_id`, còn `ScanStaleCheckoutGroups` bỏ qua vì đã có bản ghi trong `orders`.
- **Đề xuất sửa (1 dòng)**: Chỉ cập nhật `payment_status`/`payment_id` trong `UpdateOrderPaymentInfoByGroup` và `CompleteCheckoutGroup` khi hiện tại là `''`/`'PENDING'` (không hạ cấp từ `SUCCEEDED`/`REFUNDED`/`CANCELLED`), đồng thời chạy `CompleteCheckoutWithPayment` với `context.WithoutCancel(ctx)` có retry/watchdog bù nếu thất bại.

---

### P0-2. Watchdog hết hạn Flash/VietQR (`processExpiredGroup`) bỏ qua lỗi `Payment.CancelPayment` → hủy đơn dù webhook vừa thanh toán thành công
- **Vị trí**:
  - [handlers.go:380-437](services/order/internal/service/handlers.go#L380-L437) (đặc biệt dòng `420` và `431`)
- **Mức**: **P0** (race trực tiếp giữa watchdog hết hạn 5 phút flash sale và webhook thanh toán)
- **Kịch bản cụ thể**:
  1. **Trùng thời điểm webhook xác nhận tiền về**:
     - Đơn flash sale chạm `payment_expires_at` (5 phút). Watchdog gọi `GetPayment(P1)` (`handlers.go:385`) nhận `PENDING`.
     - Ngay lúc đó webhook VietQR xác nhận `P1` trong `payment` ([service.rs:964-990](services/payment/src/service.rs#L964-L990) → `status = 'succeeded'`, ghi sổ cái, đẩy outbox `payment.succeeded`).
     - Watchdog gọi `Payment.CancelPayment(P1)` tại `handlers.go:420` → `payment` từ chối bằng `AlreadyExists` (`"cannot cancel payment in status 'succeeded'"`, [service.rs:562-566](services/payment/src/service.rs#L562-L566)).
     - Dòng `420` bỏ qua lỗi (`_, _ = ...`) và chạy thẳng `HandlePaymentResult(..., EXPIRED)` ở dòng `429`. Nếu watchdog khóa `ListOrdersByCheckoutGroupForUpdate` trước khi NATS kịp giao `payment.succeeded`, đơn đã thanh toán đúng hạn bị chuyển `CANCELLED` (`payment_status = 'EXPIRED'`), nhả tồn kho và **mất suất flash sale** (event `payment.succeeded` tới sau bị bỏ qua vì đơn đã `CANCELLED`).
  2. **Lỗi tạm thời khi hủy payment**:
     - `CancelPayment` trả `Unavailable`/`DeadlineExceeded` nhưng bị nuốt → `order` hủy đơn & nhả kho/suất flash trong khi `P1` vẫn `PENDING` ở `payment`, cho phép người mua quét QR thanh toán sau cho đơn đã hủy và đã bán lại tồn kho.
  3. **Mất `payment_id` khi `cand.PaymentID == ""`**:
     - Nếu `orders.payment_id` đang rỗng (do crash trước `CompleteCheckoutWithPayment`), dòng `385` đã tìm ra `pay.GetPaymentId()` qua `OrderGroupId`, nhưng dòng `431` vẫn truyền `cand.PaymentID` rỗng vào `HandlePaymentResult`.
- **Đề xuất sửa (1 dòng)**: Kiểm tra lỗi `CancelPayment` tại `handlers.go:420` — nếu `AlreadyExists`/`FailedPrecondition` thì gọi lại `GetPayment` để rẽ nhánh theo `SUCCEEDED`/`REFUNDED`, nếu lỗi mạng/tạm thời thì `return err` để chu kỳ sau thử lại, và truyền `pay.GetPaymentId()` tại dòng `431`.

---

## P1 – Sai lệch tài chính trọng yếu giữa báo cáo và sổ cái

### P1-1. Báo cáo `net_revenue_vnd`, `commission_vnd`, `payment_fee_vnd` không khớp sổ cái `payment` khi có hoàn tiền một phần trước lúc đơn hoàn tất (`refunded_vnd > R - C_total`)
- **Vị trí**:
  - [orders.sql:682-730](services/order/queries/orders.sql#L682-L730) (`ReportSellerCompletedDaily`, `ReportPlatformCompletedDaily`)
  - [memory.go:2055-2074](services/order/internal/store/memory.go#L2055-L2074)
  - [reports.go:114-117](services/order/internal/service/reports.go#L114-L117)
- **Mức**: **P1** (báo cáo seller & platform lệch trực tiếp sổ cái kế toán)
- **Kịch bản cụ thể**:
  - Đơn `O1`: `G = R = 100,000₫`, `Ship = 30,000₫`, `D = 0`, `P = 130,000₫`, `C_cat = 5,000₫`, `C_pay = 2,600₫` (`C_total = 7,600₫`, `raw_seller_net = 92,400₫`).
  - Khi đơn ở trạng thái `DELIVERED` (escrow vẫn `held`), admin hoàn một phần `110,000₫` (ví dụ hoàn tiền hàng + một phần phí ship), sau đó đơn chuyển `DELIVERED → COMPLETED`.
  - Trong sổ cái `payment` (`compute_settlement_split` theo thứ tự thác nước `seller_net → commission → shipping`, [domain.rs:1069-1130](services/payment/src/domain.rs#L1069-L1130)): `seller_pending` nhận **`0₫`**, `platform_fee_revenue` nhận **`0₫`**, `shipping_fee_payable` nhận **`20,000₫`**.
  - Trong báo cáo `order` (`orders.sql:685-694`):
    - `net_revenue_vnd = R - refunded_vnd - C_total = 100,000 - 110,000 - 7,600 =` **`-17,600₫`** (lệch `-17,600₫` so với `0₫` trên sổ cái).
    - `commission_vnd = 5,000₫`, `payment_fee_vnd = 2,600₫` (lệch `+7,600₫` so với `platform_fee_revenue = 0₫` trên sổ cái).
  - Nguyên nhân gốc: bảng `orders` chỉ lưu một cột `refunded_vnd` gộp chung hoàn trước settlement (ăn theo thác nước escrow) và hoàn sau settlement (trừ trực tiếp số dư shop, không giảm phí sàn), nên công thức trừ thẳng `refunded_vnd` sai với trường hợp hoàn trước settlement vượt `raw_seller_net`.
- **Đề xuất sửa (1 dòng)**: Tách cột `pre_settlement_refunded_vnd` (hoặc ghi nhận số thực chi/phí thực thu khi `COMPLETED`) và áp dụng đúng công thức thác nước `compute_settlement_split` trong `ReportSellerCompletedDaily` / `ReportPlatformCompletedDaily` cho phần hoàn trước settlement.

---

## P2 – Lệch đặc tả / Trường hợp biên / Bảo mật mức vừa

### P2-1. `CreateCheckout` bỏ rơi `breakdown.warnings` khi chương trình giảm giá shop vượt `per_user_limit`
- **Vị trí**:
  - [checkout.go:318](services/order/internal/service/checkout.go#L318), [checkout.go:505-515](services/order/internal/service/checkout.go#L505-L515)
  - [order.proto:206-217](proto/nexusmart/order/v1/order.proto#L206-L217)
  - [handlers_order.go:548-559](services/gateway/internal/router/handlers_order.go#L548-L559)
- **Mức**: **P2** (lệch đặc tả `w2b-plan.md` §2)
- **Kịch bản cụ thể**:
  - Người mua đặt SKU đang có chương trình giảm giá shop nhưng vượt `per_user_limit`. `Promotion.ReservePromotion` ([calculator.go:215-222](services/promotion/internal/calculator/calculator.go#L215-L222)) tính dòng đó về giá thường và trả cảnh báo trong `breakdown.Warnings`.
  - `CreateCheckout` không đọc hay trả `breakdown.GetWarnings()` trong `CreateCheckoutResponse` và gateway `POST /api/v1/checkout`, trái yêu cầu `w2b-plan.md` §2 (*"Quá giới hạn mỗi người: cả dòng hàng đó tính giá thường và có cảnh báo (preview và đặt hàng giống nhau)"*).
- **Đề xuất sửa (1 dòng)**: Thêm `repeated string warnings` vào `CreateCheckoutResponse` (và JSON response ở gateway) để chuyển tiếp `breakdown.GetWarnings()`.

### P2-2. `resolveActiveShopDiscounts` không chia batch 200 SKU khi gọi `Promotion.ListActiveShopDiscounts`
- **Vị trí**:
  - [checkout.go:1247-1252](services/order/internal/service/checkout.go#L1247-L1252) (đối chiếu [shop_discounts.go:297-305](services/promotion/internal/service/shop_discounts.go#L297-L305))
- **Mức**: **P2** (lỗi biên > 200 SKU làm hỏng checkout)
- **Kịch bản cụ thể**:
  - Giỏ/đơn hàng có `> 200` SKU không phải flash sale → `order` gửi toàn bộ `nonFlashSKUs` trong 1 RPC `ListActiveShopDiscounts` → `promotion` từ chối với `InvalidArgument("sku_ids exceeds maximum limit of 200")` → toàn bộ checkout thất bại với `SHOP_DISCOUNT_LOOKUP_FAILED`.
- **Đề xuất sửa (1 dòng)**: Chia `nonFlashSKUs` thành từng lô tối đa 200 phần tử khi gọi `ListActiveShopDiscounts` và gộp kết quả.

### P2-3. Định nghĩa `gmv_vnd` trong báo cáo cộng cả đơn `PENDING` chưa thanh toán và `RETURNED` đã hoàn tiền, đồng thời ra `0₫` cho đơn trợ giá 100% (`0₫`)
- **Vị trí**:
  - [orders.sql:662](services/order/queries/orders.sql#L662), [orders.sql:675](services/order/queries/orders.sql#L675)
  - [memory.go:2035-2039](services/order/internal/store/memory.go#L2035-L2039), [reports.go:109](services/order/internal/service/reports.go#L109)
- **Mức**: **P2** (lệch ngữ nghĩa số liệu báo cáo)
- **Kịch bản cụ thể**:
  - (a) Đơn hoàn tất có `G = R = 200,000₫`, `Ship = 30,000₫`, voucher sàn + xu `D = 230,000₫` → `P = 0₫`: báo cáo ghi `net_revenue_vnd = 190,000₫`, `platform_subsidy_vnd = 230,000₫`, nhưng `gmv_vnd = 0₫` (`GMV < net_revenue`).
  - (b) Đơn VietQR tạo ra ở trạng thái `PENDING` (chưa trả tiền) và đơn `RETURNED` (đã hoàn tiền 100%) đều thỏa `status <> 'CANCELLED'` nên vẫn cộng vào `gmv_vnd`.
- **Đề xuất sửa (1 dòng)**: Chốt lại công thức GMV theo đặc tả (loại `PENDING`/`RETURNED` hoặc tính trên `R + Ship` / `G - F - P_shop`) và cập nhật đồng bộ `orders.sql`, `memory.go`, `docs/contracts.md`.

### P2-4. `resolveCheckoutInputs` tự gộp các dòng trùng `sku_id` nhưng xung đột `flash_sale_item_id`, và thiếu `TrimSpace` cho `flashOverrides`
- **Vị trí**:
  - [checkout.go:771-776](services/order/internal/service/checkout.go#L771-L776), [checkout.go:817-822](services/order/internal/service/checkout.go#L817-L822)
- **Mức**: **P2** (xử lý biên đầu vào sai)
- **Kịch bản cụ thể**:
  - (a) Với `from_cart = false`, client gửi 2 dòng `[{sku_id:"S1", quantity:1, flash_sale_item_id:"FS1"}, {sku_id:"S1", quantity:5, flash_sale_item_id:""}]` (hoặc 2 `flash_sale_item_id` khác nhau): dòng `817-821` âm thầm cộng thành `{sku_id:"S1", quantity:6, flash_sale_item_id:"FS1"}`, áp giá flash cho cả 6 sản phẩm (hoặc fail giới hạn mỗi người) thay vì báo lỗi xung đột.
  - (b) Với `from_cart = true`, dòng `773-774` không `strings.TrimSpace` `sku_id`/`flash_sale_item_id` trong khi dòng `796` có `TrimSpace`, khiến khóa `flashOverrides` có khoảng trắng sẽ âm thầm bị bỏ qua.
- **Đề xuất sửa (1 dòng)**: Trả `InvalidArgument` khi phát hiện hai dòng trùng `sku_id` nhưng khác `flash_sale_item_id`, và dùng `strings.TrimSpace` cho cả key lẫn value của `flashOverrides`.

### P2-5. Guard chống CSV injection ở gateway bỏ sót `\n` và khoảng trắng đầu chuỗi trước ký tự công thức
- **Vị trí**:
  - [handlers_order.go:1347-1357](services/gateway/internal/router/handlers_order.go#L1347-L1357) (`sanitizeCSVCell`, áp dụng cho `top_product` tại dòng `1472` và `1576`)
- **Mức**: **P2** (bảo mật CSV injection)
- **Kịch bản cụ thể**:
  - Tên sản phẩm bắt đầu bằng `"\n=HYPERLINK(...)"` hoặc `" =1+1"` có `v[0]` là `'\n'` hoặc `' '`, đi lọt `switch v[0]` tại `handlers_order.go:1351-1352` và được xuất thẳng vào file CSV báo cáo seller/admin mà không có dấu `'` đứng trước.
- **Đề xuất sửa (1 dòng)**: Thêm `'\n'` vào `switch v[0]` và kiểm tra ký tự khác khoảng trắng đầu tiên (`strings.TrimLeft(v, " \t\r\n")`) nếu thuộc `=`, `+`, `-`, `@` thì thêm tiền tố `'`.

---

## P3 – Nhỏ / Đồng nhất hợp đồng / `ponytail-review`

### P3-1. Sự kiện `order.completed` chỉ gửi `commission_vnd = C_cat` (thiếu `payment_fee_vnd` / `C_pay`)
- **Vị trí**: [service.go:205-216](services/order/internal/service/service.go#L205-L216), [types.go:119-128](services/order/internal/events/types.go#L119-L128)
- **Mức**: **P3** (hiện chưa có consumer nào hỏng vì `payment` đọc `payment_shop_allocations.commission_vnd = C_cat + C_pay`, còn `promotion`/`notification` chỉ đọc `coins_reward`, nhưng ngữ nghĩa trường `commission_vnd` giữa event và allocation bị lệch)
- **Kịch bản**: Đơn VietQR có `C_cat = 5,000₫`, `C_pay = 2,600₫` → `payment_shop_allocations.commission_vnd = 7,600₫`, nhưng payload NATS `order.completed` ghi `commission_vnd = 5,000` và không có `payment_fee_vnd`.
- **Đề xuất sửa (1 dòng)**: Thêm trường `payment_fee_vnd` vào `OrderCompletedPayload` (hoặc ghi rõ trong `docs/contracts.md` rằng `order.completed.commission_vnd` là `C_cat`).

### P3-2. `computeCheckoutRequestHash` băm `ShopProgramItemId` dù server bỏ qua trường này (`ponytail-review`)
- **Vị trí**: [checkout.go:561,590,600](services/order/internal/service/checkout.go#L561-L602), [handlers_order.go:481,505](services/gateway/internal/router/handlers_order.go#L481-L506)
- **Mức**: **P3** (có thể gây 409 giả khi retry idempotent + code thừa)
- **Kịch bản**: Client retry cùng `Idempotency-Key` nhưng bỏ trống/thay đổi `shop_program_item_id` (trường mà server luôn tự tra từ `ListActiveShopDiscounts` và bỏ qua đầu vào client) → bị từ chối `409 IDEMPOTENCY_KEY_CONFLICT`.
- **Đề xuất sửa (1 dòng)**: Bỏ `ShopProgramItemID` khỏi `computeCheckoutRequestHash` và không chuyển tiếp `shop_program_item_id` từ client ở gateway.

### P3-3. Nhánh fallback phân bổ tỷ lệ cho `shopProgramDiscountVND` trong `applyDiscountsAndTotals` là dead code tự mâu thuẫn (`ponytail-review`)
- **Vị trí**: [checkout.go:1336-1360](services/order/internal/service/checkout.go#L1336-L1360) đối chiếu [checkout.go:1413-1451](services/order/internal/service/checkout.go#L1413-L1451)
- **Mức**: **P3** (code thừa không thể chạy đúng)
- **Kịch bản**: Nếu `breakdown.Shops` rỗng trong khi `TotalShopProgramDiscountVnd > 0`, dòng `1353` gán `p.shopProgramDiscountVND > 0`, nhưng dòng `1415` không tìm thấy `shopBreakdowns[p.shopID]` nên `computedShopProgramDiscount = 0` → dòng `1443` luôn trả lỗi `SHOP_PROGRAM_PRICE_CHANGED`.
- **Đề xuất sửa (1 dòng)**: Yêu cầu `breakdown.Shops` không rỗng khi `promoReserved == true` và xóa nhánh fallback phân bổ tỷ lệ cho giảm giá cấp dòng (`flash`/`shopProgram`).

### P3-4. Cột `platform_subsidy_vnd` bị bỏ trống trên mọi dòng `daily` của CSV báo cáo admin dù SQL đã tính theo ngày
- **Vị trí**: [orders.sql:717-723](services/order/queries/orders.sql#L717-L723), [reports.go:168-178](services/order/internal/service/reports.go#L168-L178), [handlers_order.go:1570](services/gateway/internal/router/handlers_order.go#L1570)
- **Mức**: **P3** (thiếu nhất quán giữa summary và daily trên CSV admin)
- **Kịch bản**: `ReportPlatformCompletedDaily` trả `platform_subsidy_vnd` từng ngày vào `CompletedDailyStat`, nhưng `DailyReportPoint` không mang trường này, nên dòng `daily` trong CSV admin luôn xuất `""` ở cột `platform_subsidy_vnd`.
- **Đề xuất sửa (1 dòng)**: Bổ sung `platform_subsidy_vnd` vào `DailyReportPoint` (hoặc truyền vào dòng `daily` của CSV admin).

### P3-5. `ComputeOrderRewardCoins` trừ cả `S_shop` (freeship do shop chịu) khỏi tiền hàng tính xu thưởng
- **Vị trí**: [service.go:288-305](services/order/internal/service/service.go#L288-L305)
- **Mức**: **P3** (sai nhẹ tiền xu thưởng khi shop bật freeship nội shop)
- **Kịch bản**: Đơn `G = 100,000₫`, `Ship = 30,000₫`, `S_shop = 30,000₫` (shop miễn phí ship), `V_plat = 0`, `X = 0`, `P = 100,000₫` (người mua trả đủ `100,000₫` tiền hàng và `0₫` phí ship). Vì `computeSellerRevenue` trừ `S_shop` (`R = 70,000₫`), người mua chỉ nhận `700` xu thay vì `1,000` xu (và nếu thêm `V_plat = 70,000₫`, người mua trả `30,000₫` tiền hàng nhưng nhận `0` xu).
- **Đề xuất sửa (1 dòng)**: Tính phần tiền hàng người mua thực trả là `min(P, max(0, (G - F - P_shop - V_shop) - V_plat - X))` hoặc ghi rõ trong spec rằng xu thưởng tính trên `R`.

### P3-6. Ước lượng `CodAmountVnd = plan.subtotalVND` trước giảm giá khi gọi `Shipping.CalculateFee`
- **Vị trí**: [checkout.go:989-1002](services/order/internal/service/checkout.go#L989-L1002) (đối chiếu [lifecycle.go:664-666](services/order/internal/service/lifecycle.go#L664-L666))
- **Mức**: **P3** (biên đơn COD giảm 100% về `0₫`)
- **Kịch bản**: Đơn COD được voucher/xu giảm còn `P = 0₫` vẫn bị tính phụ phí thu hộ COD lúc checkout vì `codEstimate = plan.subtotalVND > 0`, trong khi `CreateShipment` sau đó truyền `CodAmountVnd = 0`.
- **Đề xuất sửa (1 dòng)**: Chấp nhận do phụ thuộc vòng giữa `Ship` và `S_free`, nhưng nên ghi rõ trong `docs/contracts.md` rằng phí thu hộ COD lúc báo giá ước tính theo tạm tính trước giảm giá (hoặc tính lại khi `P == 0`).

---

## Đối chiếu các bất biến đã kiểm tra đạt (không có lỗi)

- **Công thức tiền `w2b-plan.md` §1**: Đã kiểm từng dòng qua [checkout.go:1307-1533,1694-1707](services/order/internal/service/checkout.go#L1307-L1533), [calculator.go:122-550](services/promotion/internal/calculator/calculator.go#L122-L550), [domain.rs:979-1130](services/payment/src/domain.rs#L979-L1130):
  - `P + D == R + Ship`, `R = G - F - P_shop - V_shop - S_shop >= 0` (tự cắt `S_shop` để `R >= 0`), `P_shop` và `S_shop` không bị tính hai lần hay lẫn vào `D`.
  - Flash sale và chương trình giảm giá shop loại trừ hoàn toàn trên cùng 1 SKU (`checkout.go:1280-1283, 1367-1372`, `calculator.go`).
  - `S_free` áp trên `Ship - S_shop`; `C_cat` tính trên giá sau giảm cấp dòng (`effectivePrice * qty * bps / 10000`, chặn `<= R`); `C_pay = P * payment_fee_bps / 10000` chặn để `C_cat + C_pay <= R`; `ShopAllocation` gửi đúng `(R, C_cat + C_pay, Ship, D)`.
  - Đơn `0₫`: vẫn tạo payment `amount_vnd = 0` ở trạng thái `succeeded` / escrow `held` để quyết toán trợ giá sàn `D` cho shop.
  - Đổi cấu hình phí không ảnh hưởng đơn cũ: `commission_rate_bps`, `category_id`, `payment_fee_bps`, `commission_vnd`, `payment_fee_vnd` đều được snapshot theo đơn/dòng hàng; `effective_from` được lọc đúng ở `product` ([product.sql:458-468](services/product/queries/product.sql#L458-L468)) và `payment` ([service.rs:1989-2007](services/payment/src/service.rs#L1989-L2007)).
  - Không tin `shop_id`, giá, `category_id`, `free_ship_*`, `shop_program_item_id` từ client: tất cả lấy lại từ `product` / `shipping` / `promotion` / `payment` và claims JWT.
- **Flash sale & `RetryPayment`**: Chặn flash chưa bắt đầu/đã kết thúc, cấm `COD` khi có flash, khóa `payment_expires_at <= min(now + 5m, earliestFlashEndsAt)`; `RetryPayment` ([lifecycle.go:936-1191](services/order/internal/service/lifecycle.go#L936-L1191)) dùng PG advisory lock liên replica (`WithCheckoutGroupRetryLock`), cấm chuyển sang COD, không gia hạn quá hạn flash gốc, xóa `response_snapshot_json`.
- **Huỷ một đơn con / Trả hàng / Hoàn kho**: `Product.ReleaseStock` ([server.go:1478-1495](services/product/internal/server/server.go#L1478-L1495)) trả `Released=false, err=nil` khi đặt chỗ đã `CONFIRMED`, giúp nhánh hủy đơn con còn lại trong [lifecycle.go:440-446](services/order/internal/service/lifecycle.go#L440-L446) rơi chuẩn sang `ConfirmStock + RestockOrderItems` (idempotent theo unique partial index `stock_history`); Lua `release_atomic` / `release_partial_atomic` xử lý nguyên tử cả `F|`, `V|`, `P|`; khóa idempotency hoàn tiền `order-cancel-refund:` và `order-return-refund:` tách bạch đúng.
- **RBAC, `internalRPCs` & `SERVICE_METHODS`**: Các RPC mới của `order` (`GetSellerReport`, `GetPlatformReport`, `ListOrdersNeedingReview`, `ResolveOrderReview`) đều đăng ký `false` trong [auth.go:119-122](pkg/common/auth/auth.go#L119-L122) (chặn ẩn danh & service token); `GetShopShippingSettings` (`true` tại [auth.go:127](pkg/common/auth/auth.go#L127)) và `GetFeeConfig` ([auth.rs:24](services/payment/src/auth.rs#L24)) cho phép service token chuẩn; báo cáo seller lọc theo `shop_id` từ claims đã xác minh; route admin qua `RequireRoles("admin","super_admin")` + `AdminAudit`; trường `needs_manual_review` bị ẩn khỏi buyer/seller.
- **Migration `0010` & NATS JetStream**: [0010_w2b_order.sql](services/order/migrations/0010_w2b_order.sql) là forward-only, idempotent (`IF NOT EXISTS`), có `DEFAULT` hợp lệ với `CHECK` trên dữ liệu cũ, backfill `completed_at` an toàn; [stream.go](pkg/common/jetstream/stream.go) luôn union-merge danh sách subject thay vì ghi đè.

---

## Các file đã đọc và không phát hiện lỗi

- [services/order/migrations/0010_w2b_order.sql](services/order/migrations/0010_w2b_order.sql)
- [services/order/cmd/server/main.go](services/order/cmd/server/main.go)
- [services/order/internal/config/config.go](services/order/internal/config/config.go)
- [services/order/internal/clients/clients.go](services/order/internal/clients/clients.go)
- [services/order/internal/grpcserver/server.go](services/order/internal/grpcserver/server.go)
- [services/order/internal/events/outbox.go](services/order/internal/events/outbox.go)
- [services/order/internal/events/consumer.go](services/order/internal/events/consumer.go)
- [services/order/internal/statemachine/statemachine.go](services/order/internal/statemachine/statemachine.go)
- [services/order/internal/statemachine/return_statemachine.go](services/order/internal/statemachine/return_statemachine.go)
- [services/order/internal/store/store.go](services/order/internal/store/store.go)
- [services/order/internal/store/postgres_returns.go](services/order/internal/store/postgres_returns.go)
- [services/order/internal/store/memory_returns.go](services/order/internal/store/memory_returns.go)
- [services/order/internal/service/lifecycle.go](services/order/internal/service/lifecycle.go)
- [services/order/internal/service/returns.go](services/order/internal/service/returns.go)
- [services/order/internal/db/migrate.go](services/order/internal/db/migrate.go)
- [services/order/sqlc.yaml](services/order/sqlc.yaml)
- [pkg/common/auth/auth.go](pkg/common/auth/auth.go)
- [pkg/common/jetstream/stream.go](pkg/common/jetstream/stream.go)
- [services/gateway/internal/router/router.go](services/gateway/internal/router/router.go)
- [services/gateway/internal/router/handlers_payment.go](services/gateway/internal/router/handlers_payment.go)
- [services/payment/src/auth.rs](services/payment/src/auth.rs), [services/payment/src/ledger.rs](services/payment/src/ledger.rs), [services/payment/src/webhook_security.rs](services/payment/src/webhook_security.rs), [services/payment/src/domain.rs](services/payment/src/domain.rs) (phần đối chiếu tích hợp với `order`), [services/payment/src/service.rs](services/payment/src/service.rs) (phần tích hợp với `order`)
- [services/promotion/internal/calculator/calculator.go](services/promotion/internal/calculator/calculator.go), [services/promotion/internal/store/release_plan.go](services/promotion/internal/store/release_plan.go), [services/promotion/internal/lua/reserve_atomic.lua](services/promotion/internal/lua/reserve_atomic.lua), [services/promotion/internal/lua/release_atomic.lua](services/promotion/internal/lua/release_atomic.lua), [services/promotion/internal/lua/release_partial_atomic.lua](services/promotion/internal/lua/release_partial_atomic.lua)
- [services/product/internal/server/server.go](services/product/internal/server/server.go) (các RPC tồn kho/SKU), [services/product/queries/product.sql](services/product/queries/product.sql)
- [services/shipping/internal/service/shipping.go](services/shipping/internal/service/shipping.go), [services/shipping/internal/grpcserver/server.go](services/shipping/internal/grpcserver/server.go)
- [services/notification/internal/service/notification.go](services/notification/internal/service/notification.go) (consumer `order.cancelled`, `order.completed`)

