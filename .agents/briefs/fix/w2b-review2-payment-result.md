# Báo cáo Review độc lập Lượt 2 — Đợt 2b

> Phạm vi rà soát: `services/payment/**`, `pkg/common/**`, `services/cart/**`, `services/user/**`, `services/gateway/**`, cùng phần thay đổi từ `d88221d` của `services/shipping/**` và `services/notification/**`.  
> Chế độ: **chỉ đọc** (không sửa tệp, không đụng container), đã chạy `go test -race`, `golangci-lint`, `cargo fmt --check`, `cargo clippy -D warnings`, `cargo test`.

---

## 1. Đối soát các phát hiện P0 / P1 / P2 từ Lượt 1 (trong phạm vi)

| Nguồn Lượt 1 | Mức | Tóm tắt phát hiện | Trạng thái | Bằng chứng code hiện tại |
|---|---|---|---|---|
| Payment P0 #1 | P0 | `CartService` thiếu trong `internalRPCs` → IDOR vô danh | **ĐÃ SỬA** | [auth.go](pkg/common/auth/auth.go#L78-L83), [auth.go](pkg/common/auth/auth.go#L152-L172), [main.go](services/cart/cmd/cart/main.go#L116-L121), [server.go](services/cart/internal/server/server.go#L423-L451) |
| Edge P0 #1 | P0 | Vượt `PENDING_ADMIN_REVIEW` bằng `UpdateProfile` đổi `users.full_name` | **ĐÃ SỬA** | [0006_shop_verified_owner_name_and_otp_publish.sql](services/user/migrations/0006_shop_verified_owner_name_and_otp_publish.sql#L5-L13), [server.go](services/user/internal/service/server.go#L1460-L1474), [user.sql](services/user/queries/user.sql#L264-L276), [bank_account.go](services/user/internal/service/bank_account.go#L274), [bank_account.go](services/user/internal/service/bank_account.go#L478) |
| Edge P1 #4 / Promo P0 #5 | P0/P1 | `ReservePromotion` (và `PreviewDiscounts`) vẫn cho end-user JWT gọi gRPC trực tiếp và tin tiền tệ/số liệu từ client | **CHƯA SỬA** | [auth.go](pkg/common/auth/auth.go#L103-L104) vẫn đặt `ReservePromotion: false` (cho end-user JWT, cấm service token); [promotion.go](services/promotion/internal/service/promotion.go#L799-L820) và [promotion.go](services/promotion/internal/service/promotion.go#L1433-L1470) mới chặn `category_id` & khớp `qty` flash sale, vẫn tin `subtotal_vnd`, `unit_price_vnd`, `shipping_fee_vnd`, `free_ship_*` từ request gRPC khi gọi trực tiếp bằng JWT buyer |
| Edge P1 #2 | P1 | Lộ `commission_vnd` cho buyer và phân bổ chéo shop cho seller qua `GetPayment` / `retry` / SSE | **ĐÃ SỬA** | [auth.rs](services/payment/src/auth.rs#L48-L133), [grpc.rs](services/payment/src/grpc.rs#L1059-L1200), [http.rs](services/payment/src/http.rs#L286-L374), [handlers_payment.go](services/gateway/internal/router/handlers_payment.go#L212-L239) |
| Payment P2 #4 / Edge P1 #3 | P1 | `GetFeeConfig` cho buyer/seller gọi với `include_history=false` | **ĐÃ SỬA** | [grpc.rs](services/payment/src/grpc.rs#L789) gọi `require_admin_or_service` vô điều kiện; đã xác minh [checkout.go](services/order/internal/service/checkout.go#L1027) gọi bằng `WithInternalAdminContext` (service token) |
| Promo P1 #7 (gateway) | P1 | `GET /api/v1/admin/vouchers` mặc định `include_inactive=false` | **ĐÃ SỬA** | [handlers_promotion.go](services/gateway/internal/router/handlers_promotion.go#L739-L742) mặc định `true` khi bỏ trống (trừ khi `status=ACTIVE`) |
| Payment P2 #2 | P2 | `connect_jetstream` nuốt lỗi `ensure_stream_subjects` và đốt `retry_count` outbox khi NATS lỗi hạ tầng | **ĐÃ SỬA** | [workers.rs](services/payment/src/workers.rs#L186-L207), [workers.rs](services/payment/src/workers.rs#L218-L245), [workers.rs](services/payment/src/workers.rs#L421-L441) |
| Payment P2 #3 | P2 | `process_webhook_http` bỏ qua `trace_id` của middleware | **ĐÃ SỬA** | [http.rs](services/payment/src/http.rs#L66-L92), [http.rs](services/payment/src/http.rs#L194-L225) |
| Payment P2 #5 | P2 | `EnsureStream` race với `extraPrimarySubjects` khác nhau | **ĐÃ SỬA** | [stream.go](pkg/common/jetstream/stream.go#L79-L128) bỏ tham số variadic, đồng nhất `CanonicalPrimarySubjects()` cho toàn bộ 6 caller Go |
| Edge P2 #5 (gateway) | P2 | Public `GET /api/v1/categories` và `/api/v1/products*` vẫn lộ `commission_rate_bps` | **CHƯA SỬA** | [handlers_product.go](services/gateway/internal/router/handlers_product.go#L21-L44), [handlers_product.go](services/gateway/internal/router/handlers_product.go#L69-L142) vẫn serialize `commission_rate_bps` ra toàn bộ endpoint public |
| Edge P2 #6 (gateway) | P2 | `handlePreviewDiscounts` không lọc SKU ẩn / chưa duyệt / tắt / xóa mềm | **CHƯA SỬA** | [handlers_promotion.go](services/gateway/internal/router/handlers_promotion.go#L1476-L1532) không kiểm `sk.GetIsActive()` sau khi gọi `GetSKUsBatch` (và `product` `GetSKUsBatch` vẫn trả SKU không hoạt động) |
| Edge P2 #7 / Order P2-5 | P2 | `sanitizeCSVCell` bỏ sót `\n` và khoảng trắng đầu chuỗi trước ký tự công thức | **ĐÃ SỬA** | [handlers_order.go](services/gateway/internal/router/handlers_order.go#L1379-L1395) |
| Edge P2 #8 | P2 | Lỗi publish OTP làm mất `PENDING_ADMIN_REVIEW` và đốt rate-limit 5/h | **SỬA SAI** | [bank_account.go](services/user/internal/service/bank_account.go#L267), [bank_account.go](services/user/internal/service/bank_account.go#L335-L344), [bank_account.go](services/user/internal/service/bank_account.go#L594), [user.sql](services/user/queries/user.sql#L478-L489): giữ lại `PENDING_ADMIN_REVIEW` khi tạo `PENDING_OTP` nhưng **sinh lỗi hồi quy P1 #1 và P2 #1** bên dưới |
| Edge P2 #9 | P2 | Zombie `PENDING_OTP` sau hết hạn/5 lần sai + thiếu check `owner.Status == ACTIVE` trong `VerifyShopBankAccountOTP` | **SỬA SAI** *(chưa triệt để)* | Đã thêm check `owner.Status != StatusActive` ([bank_account.go](services/user/internal/service/bank_account.go#L401-L403)) và chuyển `SUPERSEDED` khi gọi `Verify` ([bank_account.go](services/user/internal/service/bank_account.go#L417-L451)), **nhưng** [user.sql](services/user/queries/user.sql#L478-L489) (`GetLatestShopBankAccountByShopID`) không lọc `otp_expires_at > NOW()`, nên nếu seller không gọi Verify sau khi OTP hết hạn thì bản ghi vẫn kẹt zombie `PENDING_OTP` vĩnh viễn ở `GET bank-account` (và khóa luôn P1 #1) |
| Edge P2 #11 (shipping/gateway/docs) | P2 | `UpdateShopShippingSettings` ghi đè 0 kích thước gói hàng; sai lệch DTO trong `docs/contracts.md` | **ĐÃ SỬA** | [shipping.go](services/shipping/internal/service/shipping.go#L1170-L1209) giữ nguyên kích thước cũ trong 1 lần đọc; [contracts.md](docs/contracts.md#L221-L226) đã cập nhật DTO (còn lệch nhỏ ở P3 #4) |
| Order P2-1 (gateway) | P2 | `POST /api/v1/checkout` không forward `warnings` | **ĐÃ SỬA** | [handlers_order.go](services/gateway/internal/router/handlers_order.go#L478-L482), [handlers_order.go](services/gateway/internal/router/handlers_order.go#L547-L563) |
| Promo P2 #18 (gateway) | P2 | Trường số bắt buộc bị bỏ trống tự decode thành `0` | **ĐÃ SỬA** | Đã chuyển sang `*int32`/`*int64` ở [handlers_product.go](services/gateway/internal/router/handlers_product.go#L355-L370), [handlers_payment.go](services/gateway/internal/router/handlers_payment.go#L504-L514), [handlers_payment.go](services/gateway/internal/router/handlers_payment.go#L856-L876), [handlers_shipping.go](services/gateway/internal/router/handlers_shipping.go#L455-L486), [handlers_promotion.go](services/gateway/internal/router/handlers_promotion.go#L807-L845) *(xem thêm P2 #4 về trường bool bắt buộc còn sót)* |
| Edge Nghi ngờ #1 | N/A | `putCachedShopOwner` ghi đè `ShopName` bằng `""` | **ĐÃ SỬA** | [notification.go](services/notification/internal/service/notification.go#L462-L466) |

*(Ngoài ra các P3 lượt 1 ở payment như `http_errors_total` chỉ đếm 5xx tại [metrics.rs](services/payment/src/metrics.rs#L207), `EventEnvelope.trace_id` tại [workers.rs](services/payment/src/workers.rs#L366), và `ORDER BY effective_from DESC, seq DESC` tại [service.rs](services/payment/src/service.rs#L2000-L2014) cũng đã được sửa.)*

---

## 2. Lỗi MỚI phát hiện (xếp theo mức độ trung thực)

> Không có lỗi **P0 mới** được chứng minh trong phạm vi lượt 2. *(Lưu ý: `ReservePromotion` ở bảng trên là lỗi lượt 1 **CHƯA SỬA**, mức rủi ro phụ thuộc mô hình tin cậy mạng gRPC nội bộ: nếu kẻ tấn công gọi được gRPC `PromotionService` bằng JWT buyer thì có thể đốt hết ngân sách voucher/suất flash sale bằng `subtotal_vnd` giả).*

### P1 — Hỏng luồng nghiệp vụ chính

#### P1 #1. [Hồi quy từ bản sửa Edge P2 #8] Bản ghi `PENDING_OTP` mới che khuất `PENDING_ADMIN_REVIEW` khiến Admin không thể xem/duyệt tài khoản ngân hàng (kẹt vĩnh viễn nếu OTP hết hạn)
- **Vị trí:** [bank_account.go](services/user/internal/service/bank_account.go#L267), [bank_account.go](services/user/internal/service/bank_account.go#L594), [user.sql](services/user/queries/user.sql#L478-L489) (ảnh hưởng trực tiếp [page.tsx](web/src/app/(admin)/admin/gian-hang/page.tsx#L149-L155) và [page.tsx](web/src/app/(admin)/admin/gian-hang/page.tsx#L473))
- **Kịch bản cụ thể:**
  1. Shop `S1` nộp tài khoản `A1` khác tên chủ sở hữu và xác thực OTP thành công → `A1` ở trạng thái `PENDING_ADMIN_REVIEW`. Danh sách quản trị `GET /api/v1/admin/shops?bank_review_pending=true` đánh dấu `S1` có `bank_review_pending = true`.
  2. Seller bấm thêm/sửa tài khoản ngân hàng lần nữa (`AddShopBankAccount`) → do bản sửa lượt 1 đổi sang `SupersedePendingOTPShopBankAccounts`, `A1` (`PENDING_ADMIN_REVIEW`) được giữ nguyên trong khi `A2` (`PENDING_OTP`, `created_at > A1.created_at`) được chèn mới.
  3. Admin mở chi tiết shop gọi `GET /api/v1/admin/shops/S1/bank-account` → `GetShopBankAccount` gọi `GetLatestShopBankAccountByShopID` (`ORDER BY created_at DESC, CASE status WHEN 'PENDING_OTP' THEN 0 ...`). Kết quả trả về **`A2` (`PENDING_OTP`)**, che hoàn toàn `A1`.
  4. Admin UI kiểm tra `bankAcc.status === "PENDING_ADMIN_REVIEW"` để hiển thị nút Duyệt/Từ chối và lấy `bankAcc.id`, nên không thể duyệt `A1` (gọi `/review` với `A2.id` cũng bị `409 FailedPrecondition`).
  5. Tệ hơn: nếu seller bỏ ngang không nhập OTP và `A2` hết hạn, do `GetLatestShopBankAccountByShopID` không lọc `otp_expires_at > NOW()`, **`A1` bị ẩn và shop kẹt `bank_review_pending = true` vĩnh viễn**; tương tự nếu shop đang có tài khoản `VERIFIED`, `GET /api/v1/seller/bank-account` cũng kẹt mãi ở bản ghi `PENDING_OTP` đã hết hạn.
- **Đề xuất sửa (1 dòng):** Trong `GetLatestShopBankAccountByShopID` loại bỏ `PENDING_OTP` đã hết hạn (`status <> 'PENDING_OTP' OR (otp_expires_at IS NOT NULL AND otp_expires_at > NOW())`), đồng thời khi caller là Admin trong `GetShopBankAccount` thì ưu tiên/truy vấn bản ghi `PENDING_ADMIN_REVIEW` trước `PENDING_OTP`.

---

### P2 — Lệch đặc tả / Trường hợp biên nghiêm trọng

#### P2 #1. [Lỗi trong bản sửa Edge P2 #8] `MarkShopBankAccountPublishFailed` dùng `ctx` gốc đã hết hạn/bị hủy
- **Vị trí:** [bank_account.go](services/user/internal/service/bank_account.go#L320-L345)
- **Kịch bản cụ thể:** Seller gọi `AddShopBankAccount`, transaction DB đã commit bản ghi `PENDING_OTP`, sau đó `PublishShopBankAccountOTP(ctx, ...)` thất bại do `context.DeadlineExceeded` hoặc client ngắt kết nối (`context.Canceled`). Ngay dòng 335, `s.store.MarkShopBankAccountPublishFailed(ctx, ...)` dùng lại đúng `ctx` đã hủy nên cũng thất bại ngay lập tức → bản ghi OTP chưa từng gửi đi vẫn nằm lại ở trạng thái `PENDING_OTP` với `otp_publish_failed = FALSE`, vừa đốt 1 lượt rate-limit 5/giờ, vừa kích hoạt lỗi che khuất **P1 #1**.
- **Đề xuất sửa (1 dòng):** Bọc `cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)` cho cuộc gọi `MarkShopBankAccountPublishFailed`.

#### P2 #2. Tràn số `int32` trong `CartService.AddItem` lưu số lượng âm vào Redis và trả tổng giỏ hàng âm
- **Vị trí:** [server.go](services/cart/internal/server/server.go#L100-L122)
- **Kịch bản cụ thể:**
  1. Buyer thêm SKU `sku-1` (tồn kho `10`) với `quantity: 1` → Redis lưu `Quantity: 1`.
  2. Buyer gọi `POST /api/v1/cart/items` với `sku_id: "sku-1"`, `quantity: 2147483647` (`math.MaxInt32`).
  3. `req.GetQuantity() <= 0` đi qua; phép cộng `int32` `desiredQty += existing.Quantity` tràn sang **`-2147483648`**.
  4. Kiểm tra `finalQty > sku.GetAvailableStock()` là `false`, nên Redis lưu `Quantity: -2147483648`. `GetCart` trả về `quantity`, `subtotal_vnd`, `total_items`, `total_vnd`, `total_weight_grams` đều âm. *(Đã kiểm [checkout.go](services/order/internal/service/checkout.go#L790): `OrderService` bỏ qua dòng `qty <= 0` nên không sinh đơn âm tiền — mức P2, không phải P0).*
- **Đề xuất sửa (1 dòng):** Cộng bằng `int64` (`desiredQty := int64(req.GetQuantity()) + int64(existing.Quantity)`) rồi clamp về `int64(sku.GetAvailableStock())` trước khi ép sang `int32`.

#### P2 #3. `handlePreviewDiscounts` ở Gateway chọn chương trình giảm giá shop khác thuật toán của `OrderService.CreateCheckout`
- **Vị trí:** [handlers_promotion.go](services/gateway/internal/router/handlers_promotion.go#L1461-L1466) đối chiếu [checkout.go](services/order/internal/service/checkout.go#L1268-L1293) và [shop_discounts.go](services/promotion/internal/store/shop_discounts.go#L467)
- **Kịch bản cụ thể:** Shop tạo 2 chương trình giảm giá đang chạy cùng lúc cho `sku-1` (giá gốc 100.000₫): Chương trình 1 (`starts_at` sớm hơn) giảm còn **70.000₫**, Chương trình 2 (`starts_at` muộn hơn) giảm còn **90.000₫**. `ListActiveShopDiscounts` trả về theo `starts_at ASC, id ASC`. `OrderService` chọn giá thấp nhất (**70.000₫**), đồng thời lọc `d.ShopId == shopID` và `DiscountedPriceVnd < sku.PriceVnd`. Ngược lại, `handlePreviewDiscounts` ghi đè mù `activeDiscountBySKU[d.GetSkuId()] = d.GetItemId()` nên luôn chọn bản ghi **cuối cùng** (**90.000₫**) và không kiểm tra `shop_id`/giá hợp lệ → màn hình xem trước báo sai tiền giảm so với lúc đặt hàng.
- **Đề xuất sửa (1 dòng):** Chọn chương trình có `DiscountedPriceVnd` nhỏ nhất (hòa chọn `ItemId` nhỏ hơn), khớp `ShopId == authShopID` và `DiscountedPriceVnd < unitPrice` giống `resolveActiveShopDiscounts`.

#### P2 #4. Trường boolean bắt buộc ở Gateway không dùng `*bool` → bỏ trống tự từ chối vĩnh viễn Flash Sale / tự tắt hãng vận chuyển toàn sàn
- **Vị trí:** [handlers_promotion.go](services/gateway/internal/router/handlers_promotion.go#L1270-L1287) và [handlers_shipping.go](services/gateway/internal/router/handlers_shipping.go#L529-L557)
- **Kịch bản cụ thể:**
  1. Admin gửi `POST /api/v1/admin/flash-sales/items/{item_id}/approve` với `{}` hoặc gõ nhầm `{"approved": true}`: `body.Approve` mặc định `false`, mà [promotion.go](services/promotion/internal/service/promotion.go#L602-L648) không bắt buộc `reason` khi từ chối và không cho chuyển ngược từ `REJECTED → APPROVED` → đăng ký flash sale bị **REJECT vĩnh viễn** ngoài ý muốn.
  2. Admin gửi `PUT /api/v1/admin/shipping/carriers/ghn` với `{"api_token": "new-key"}` hoặc cập nhật `fallback_rates` mà không gửi lại `"enabled": true`: `body.Enabled` mặc định `false` → **tắt hãng vận chuyển trên toàn sàn**.
- **Đề xuất sửa (1 dòng):** Dùng `*bool` cho `Approve` và `Enabled`, trả `400 INVALID_ARGUMENT` khi trường bắt buộc là `nil`.

---

### P3 — Lỗi nhỏ / Phòng thủ / Lệch tài liệu

#### P3 #1. `payment_events_sse` truyền cả `order_group_id` lẫn `order_id` làm nhánh join `payment_shop_allocations` thành dead code
- **Vị trí:** [http.rs](services/payment/src/http.rs#L280-L284) + [service.rs](services/payment/src/service.rs#L455-L492)
- **Kịch bản:** Khi gọi trực tiếp SSE với `id` không phải UUID (là `sub_order_id` của đơn đa shop), `http.rs` gọi `get_payment(None, Some(&id), Some(&id))`; nhánh `else if let Some(gid)` luôn khớp trước và chỉ tìm `payments.order_group_id` / `payments.order_id`, không bao giờ đi vào nhánh `else if let Some(oid)` có join `payment_shop_allocations.order_id` → trả 404.
- **Đề xuất sửa (1 dòng):** Truyền `get_payment(None, None, Some(&id))` khi `id` không phải UUID (vì nhánh `order_id` đã tìm cả `p.order_id`, `p.order_group_id`, và `a.order_id`).

#### P3 #2. Lớp phòng thủ gateway `canAccessPayment` fail-open khi `user_id` rỗng và `mapPaymentForCaller` chưa tự xóa trường nhạy cảm cho seller
- **Vị trí:** [handlers_payment.go](services/gateway/internal/router/handlers_payment.go#L160-L173), [handlers_payment.go](services/gateway/internal/router/handlers_payment.go#L227-L236)
- **Kịch bản:** `canAccessPayment` trả `true` nếu `p == nil || p.GetUserId() == ""`, và `mapPaymentForCaller` cho seller chỉ lọc `ShopAllocations` mà không xóa `QRURL`/`QRCode`/`ProviderRef` ở tầng gateway (hiện an toàn nhờ `services/payment` đã chặn và xóa sẵn, nhưng defense-in-depth ở gateway chưa khép kín).
- **Đề xuất sửa (1 dòng):** Đổi `canAccessPayment` sang fail-closed (`return false`) khi `p == nil || p.GetUserId() == ""` và xóa `QRURL`/`QRCode`/`ProviderRef` trong nhánh seller của `mapPaymentForCaller`.

#### P3 #3. Index `payment_fee_configs` lệch thứ tự sắp xếp sau khi sửa `ORDER BY`
- **Vị trí:** [0007_payment_fee_configs.sql](services/payment/migrations/0007_payment_fee_configs.sql#L19-L20) vs [service.rs](services/payment/src/service.rs#L2000-L2014)
- **Kịch bản:** Query dùng `ORDER BY payment_method ASC, effective_from DESC, seq DESC` nhưng index lại là `(payment_method, effective_from DESC, created_at DESC, seq DESC)`, phải sort lại khi trùng `effective_from`.
- **Đề xuất sửa (1 dòng):** Thêm migration forward-only tạo index `(payment_method, effective_from DESC, seq DESC)`.

#### P3 #4. `docs/contracts.md` cập nhật trễ 2 chi tiết nhỏ
- **Vị trí:** [contracts.md](docs/contracts.md#L248) (chưa liệt kê `\n` và khoảng trắng đầu chuỗi trong quy tắc CSV injection) và [contracts.md](docs/contracts.md#L271) (vẫn ghi SSE chỉ cho `buyer own or admin`, trong khi code đã mở cho seller tham gia đơn).
- **Đề xuất sửa (1 dòng):** Cập nhật 2 dòng mô tả trong `docs/contracts.md` cho khớp triển khai.

---

## 3. Kiểm định chất lượng các test mới (có ĐỎ khi bỏ bản sửa không?)

### A. Các test **KHÔNG chứng minh được bản sửa** hoặc có điểm mù lớn

1. **[workers.rs](services/payment/src/workers.rs#L1066-L1109) — `test_w2b_r3_publish_error_classification_protects_outbox_on_infra_failure` (KHÔNG chứng minh được luồng outbox):**
   - Chỉ gọi hàm thuần `classify_publish_error(kind)` trên 6 variant enum và assert lại chính mapping 6 dòng đó.
   - **Nếu bỏ hoàn toàn nhánh `ReconnectWithoutCountingRetry` trong `relay_outbox_batch` ([workers.rs](services/payment/src/workers.rs#L421-L441)) và quay lại gọi `mark_outbox_retry`, test này VẪN XANH.**
2. **[w2b_routes_test.go](services/gateway/internal/router/w2b_routes_test.go#L1886-L1894) & [w2b_routes_test.go](services/gateway/internal/router/w2b_routes_test.go#L973-L977) — nhánh SSE trong `TestW2BRound4ReviewFixes/PaymentPrivacyGetRetryAndSSE` (KHÔNG chứng minh được payload redaction):**
   - `PaymentSSEProxy` giả trả cứng `data: {"payment_id":"pay-multi-1"}`. Dòng kiểm tra `!strings.Contains(recSSE.Body.String(), "commission_vnd")` chỉ kiểm tra chuỗi cố định do chính test tạo ra — chỉ chứng minh được cổng 200/403 của `canAccessPayment`, **không** kiểm tra việc lọc payload SSE.
3. **[http.rs](services/payment/src/http.rs#L999-L1063) (`test_w2b_r4_sse_event_projection_redacts_seller_allocation_only`) và [grpc.rs](services/payment/src/grpc.rs#L2325-L2437) (`test_w2b_r4_payment_projection_filters_by_caller_role`) (BÁN PHẦN — chỉ test helper thuần):**
   - Cả hai chỉ gọi helper `project_sse_event_for_caller` / `to_proto_payment_for_caller`, không đi qua handler `payment_events_sse` (kể cả `live_stream`) hay `server.get_payment`. **Nếu `grpc.rs:get_payment` gọi nhầm `to_proto_payment` cũ hoặc `http.rs` quên `.project(evt)` trên live stream, 2 test này VẪN XANH.**
4. **[domain.rs](services/payment/src/domain.rs#L2716-L2832) (`test_w2b_fee_config_validation_and_time_boundaries`) — phần tie-break (ĐIỂM MÙ SQL):**
   - Test gọi hàm `#[cfg(test)] resolve_active_fee_configs` viết bằng Rust chứ không chạy SQL thật trong [service.rs](services/payment/src/service.rs#L1993-L2016). **Nếu đổi SQL quay lại `ORDER BY created_at DESC`, test này VẪN XANH.**
5. **[bank_and_stats_w2b_test.go](services/user/internal/service/bank_and_stats_w2b_test.go#L371-L580) (`TestW2B_Turn4_*`) (Điểm mù `memStore` + bỏ sót P1 #1 & P2 #1):**
   - Toàn bộ chạy trên `memStore` tự viết lại logic SQL trong [server_test.go](services/user/internal/service/server_test.go#L1092-L1290), không kiểm chứng SQL thật trong `queries/user.sql`.
   - `TestW2B_Turn4_PublishFailurePreservesPendingReviewAndDoesNotBurnRateLimit` chỉ gọi `GetShopBankAccount` **sau khi** publish thất bại (khi `A2` đã bị chuyển `SUPERSEDED`), không gọi khi `A2` đang ở `PENDING_OTP` (nên bỏ lọt **P1 #1**), và không kiểm tra `Publish` lỗi do `ctx` đã hủy (nên bỏ lọt **P2 #1**).

### B. Các test mới **ĐẠT YÊU CẦU (thật sự ĐỎ khi bỏ bản sửa)**

- [grpc_trust_test.go](pkg/common/auth/grpc_trust_test.go#L354-L416) (`TestAllGoServiceRPCsAreClassified`, `TestGRPCInterceptorContractAllowlist`): quét trực tiếp toàn bộ RPC sinh ra từ proto, chắc chắn đỏ nếu thiếu `CartService` hay RPC mới.
- [server_test.go](services/cart/internal/server/server_test.go#L254-L345) (`TestCartRemoveClearAndOwnership`): kiểm đầy đủ anonymous, service token và cross-user trên cả 5 RPC của `CartService`.
- [grpc.rs](services/payment/src/grpc.rs#L2230-L2323) (`test_w2b_r4_get_fee_config_requires_admin_or_service_unconditionally`): gọi trực tiếp `server.get_fee_config` với đầy đủ vai trò (buyer/seller/admin/service, `include_history` true/false).
- [http.rs](services/payment/src/http.rs#L842-L997) (`test_w2b_r3_webhook_without_traceparent_reuses_middleware_trace_id`, `test_w2b_r3_http_errors_total_counts_only_5xx_not_4xx`): đi qua axum router thật + bắt log & `/metrics` thật.
- [workers.rs](services/payment/src/workers.rs#L1032-L1064) (`test_w2b_r3_ensure_required_streams_returns_false_on_primary_or_dlq_failure`): phủ sát hàm wrapper 3 dòng của `connect_jetstream`.
- [stream_test.go](pkg/common/jetstream/stream_test.go), [telemetry_test.go](pkg/common/telemetry/telemetry_test.go), [grpc_test.go](pkg/common/logger/grpc_test.go): đỏ ngay khi bỏ logic canonical subject / OTLP / gRPC request log redaction.
- [w2b_routes_test.go](services/gateway/internal/router/w2b_routes_test.go) (các nhánh CSV `\n` + leading space, default `include_inactive=true` ở admin vouchers, `*int32`/`*int64` required fields, forward `warnings` ở checkout, `mapPaymentForCaller` trên GET/retry) và [w2b_audit_test.go](services/gateway/internal/middleware/w2b_audit_test.go).
- [shipping_test.go](services/shipping/internal/service/shipping_test.go#L2641-L2933), [server_test.go](services/shipping/internal/grpcserver/server_test.go#L850-L1024), [envelope_test.go](services/shipping/internal/events/envelope_test.go).
- [notification_w2b_test.go](services/notification/internal/service/notification_w2b_test.go), [consumer_test.go](services/notification/internal/consumer/consumer_test.go#L227-L287), [mailer_test.go](services/notification/internal/mailer/mailer_test.go#L209-L259).

---

## 4. Kết quả chạy kiểm tra (read-only)

- `go test -race -count=1 ./...` trên 6 module (`pkg/common`, `services/cart`, `services/user`, `services/gateway`, `services/shipping`, `services/notification`): **PASS 100%** (không race).
- `golangci-lint run ./...` trên cả 6 module Go: **PASS 100%** (0 cảnh báo).
- `cd services/payment && cargo fmt --all -- --check && cargo clippy --all-targets -- -D warnings && cargo test --lib --bins --tests`: **PASS 100%** (**86/86** unit test xanh, 0 cảnh báo clippy/fmt).

---

## 5. Ghi chú gọn & Danh sách tệp đã đọc không có lỗi mới

- **Ponytail (gọn):** `resolve_active_fee_configs` trong [domain.rs](services/payment/src/domain.rs#L491-L519) là code `#[cfg(test)]` viết lại thuật toán SQL bằng Rust — vừa dư vừa tạo cảm giác an toàn giả cho câu SQL thật ở `service.rs`; nên ưu tiên test SQL bằng integration test thay vì nhân đôi logic trong `domain.rs`.
- **Các tệp đã đọc đầy đủ/đối soát diff, KHÔNG phát hiện lỗi mới:**
  - `pkg/common`: [stream.go](pkg/common/jetstream/stream.go), [stream_test.go](pkg/common/jetstream/stream_test.go), [telemetry.go](pkg/common/telemetry/telemetry.go), [telemetry_test.go](pkg/common/telemetry/telemetry_test.go), [logger.go](pkg/common/logger/logger.go), [grpc.go](pkg/common/logger/grpc.go), [grpc_test.go](pkg/common/logger/grpc_test.go), [metrics.go](pkg/common/metrics/metrics.go), [grpc_trust_test.go](pkg/common/auth/grpc_trust_test.go)
  - `services/cart`: [main.go](services/cart/cmd/cart/main.go), [server_test.go](services/cart/internal/server/server_test.go)
  - `services/payment`: [build.rs](services/payment/build.rs), [auth.rs](services/payment/src/auth.rs), [metrics.rs](services/payment/src/metrics.rs), [telemetry.rs](services/payment/src/telemetry.rs), [ledger.rs](services/payment/src/ledger.rs) (phần mới đợt 2b), [grpc.rs](services/payment/src/grpc.rs) (ngoài điểm mù test nêu ở mục 3)
  - `services/user`: [stats.go](services/user/internal/service/stats.go), [migrate.go](services/user/internal/db/migrate.go), [0005_shop_bank_accounts.sql](services/user/migrations/0005_shop_bank_accounts.sql), [0006_shop_verified_owner_name_and_otp_publish.sql](services/user/migrations/0006_shop_verified_owner_name_and_otp_publish.sql), [helpers.go](services/user/internal/service/helpers.go), [publisher.go](services/user/internal/events/publisher.go), [main.go](services/user/cmd/user/main.go)
  - `services/gateway`: [router.go](services/gateway/internal/router/router.go), [clients.go](services/gateway/internal/clients/clients.go), [audit.go](services/gateway/internal/middleware/audit.go), [handlers_cart.go](services/gateway/internal/router/handlers_cart.go), [handlers_user.go](services/gateway/internal/router/handlers_user.go), [handlers_order.go](services/gateway/internal/router/handlers_order.go)
  - `services/shipping` (diff từ `d88221d`): [000004_shop_freeship_settings.sql](services/shipping/migrations/000004_shop_freeship_settings.sql), [store.go](services/shipping/internal/store/store.go), [shipping.go](services/shipping/internal/service/shipping.go), [server.go](services/shipping/internal/grpcserver/server.go), [envelope.go](services/shipping/internal/events/envelope.go)
  - `services/notification` (diff từ `d88221d`): [000004_notification_dedup_and_indexes.sql](services/notification/migrations/000004_notification_dedup_and_indexes.sql), [consumer.go](services/notification/internal/consumer/consumer.go), [mailer.go](services/notification/internal/mailer/mailer.go), [notification.go](services/notification/internal/service/notification.go), [store.go](services/notification/internal/store/store.go)
