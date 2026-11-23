# Review độc lập gateway/user/shipping/product/notification/web (agy, chỉ đọc) — 2026-11-23

Báo cáo review độc lập (chỉ đọc) cho đợt sửa lỗi Wave 1 trên nhánh `fix/business-audit` theo [.agents/briefs/fix/w1-review.md](file:///home/tai/Document/nexusmart/.agents/briefs/fix/w1-review.md).

## Kiểm chứng tự động đã chạy (chỉ đọc)
- `go vet ./...` và `go test -race ./...` trên 6 module trong phạm vi (`pkg/common/auth`, `services/gateway`, `services/user`, `services/shipping`, `services/product`, `services/notification`): **xanh**.
- `cd web && node --test scripts/audit-fixes.test.mjs`: **7/7 xanh**.
- Kiểm tra ánh xạ `translate(...)` trong [000004_product_search_text.sql](file:///home/tai/Document/nexusmart/services/product/migrations/000004_product_search_text.sql): khớp chính xác **134/134** ký tự, không lệch chỉ số.
- Truy vấn `SELECT` chỉ đọc trên Postgres của stack `nexusmart-audit`:
  - `product_db`: `18/18` sản phẩm có `search_text` đã backfill không dấu.
  - `user_db`: migration `0003_cod_rejections.sql` đã áp dụng, bảng `cod_rejections` tồn tại; `audit_logs` xác nhận trực tiếp lỗi P1 #4 bên dưới.
  - `notification_db`: cột `order_participants.checkout_group_id` tồn tại (`0` dòng hiện có).

---

## Phát hiện theo mức độ nghiêm trọng

### P0 — Sai tiền / leo thang quyền / mất dữ liệu
Không phát hiện lỗi P0 trong phạm vi được giao.

---

### P1 — Vỡ luồng chính / bỏ qua kiểm soát bảo mật chính

1. **[P1] `TRUSTED_PROXIES=web` kết hợp `extractClientIp` ở Next.js BFF vô hiệu hóa hoàn toàn R9/PLT-B1 (vượt rate-limit IP & giả mạo IP audit)**
   - **Vị trí**: [docker-compose.yml:384](file:///home/tai/Document/nexusmart/docker-compose.yml#L384), [session.ts:72-95](file:///home/tai/Document/nexusmart/web/src/lib/server/session.ts#L72-L95), [route.ts:97](file:///home/tai/Document/nexusmart/web/src/app/api/gw/%5B...path%5D/route.ts#L97)
   - **Kịch bản hỏng**: `gateway` tin cậy peer `web` (`TRUSTED_PROXIES=web`), còn `web` mở cổng trực tiếp ra client (`WEB_PORT`). Attacker gửi request tới `web` (`/api/gw/*` hoặc `/api/auth/*`) kèm header `X-Forwarded-For: 1.2.3.x` đổi mỗi request → `extractClientIp` lấy **phần tử trái nhất** `xff.split(",")[0]` rồi `applyClientIpHeader` forward nguyên sang `gateway` từ peer tin cậy `web` → `gateway` tin IP giả `1.2.3.x`, vượt hoàn toàn rate-limit theo IP (kể cả login/flash sale) và giả mạo `ip_address` trong `audit_logs`. Kể cả khi có edge reverse proxy trước `web`, việc cắt `xff.split(",")[0]` (do client kiểm soát) vẫn phá vỡ cơ chế quét phải-sang-trái của `gateway`.
   - **Đề xuất sửa (1 dòng)**: Tại `web`, chỉ tin `X-Forwarded-For` khi có cấu hình edge proxy tường minh (và lấy hop phải-sang-trái thay vì `split(",")[0]`), đồng thời để mặc định `TRUSTED_PROXIES` rỗng khi `web` nhận traffic trực tiếp từ client.

2. **[P1] UI đăng ký & nộp lại hồ sơ gian hàng trên `web` bị vỡ sau khi `services/user` siết `RegisterShop` (USR-B2)**
   - **Vị trí**: [AccountClient.tsx:362](file:///home/tai/Document/nexusmart/web/src/features/account/AccountClient.tsx#L362), [AccountClient.tsx:1289-1310](file:///home/tai/Document/nexusmart/web/src/features/account/AccountClient.tsx#L1289-L1310), [AccountClient.tsx:1370-1413](file:///home/tai/Document/nexusmart/web/src/features/account/AccountClient.tsx#L1370-L1413) (đối chiếu [server.go:867-898](file:///home/tai/Document/nexusmart/services/user/internal/service/server.go#L867-L898))
   - **Kịch bản hỏng**:
     - Khi admin chọn `REQUEST_INFO`, gian hàng giữ `status="PENDING"` và có `rejection_reason != ""`, nhưng dòng 1310 chỉ hiện form khi `(!myShop || myShop.status === "REJECTED")` → người bán **không có form để nộp lại hồ sơ** (và dòng 1291 gán sai nhãn `"Lý do từ chối:"`).
     - Form vẫn gắn nhãn `business_license_doc` là `"(không bắt buộc)"` (dòng 1371), mặc định `pickup_address_id=""` (`"Chọn sau"`, dòng 1395) và nhắn `"Bạn vẫn có thể gửi hồ sơ"` khi chưa có địa chỉ (dòng 1406) → bấm gửi bị `services/user` trả `400 INVALID_ARGUMENT`.
     - Regex SĐT client `/^0\d{9,10}$/` (dòng 362) cho qua số không hợp lệ như `0123456789`, sau đó bị `isValidVNPhone` ở backend từ chối `400`.
   - **Đề xuất sửa (1 dòng)**: Mở lại form nộp lại khi `myShop.status === "PENDING" && Boolean(myShop.rejection_reason)` (đổi nhãn `REQUEST_INFO`), bắt buộc chọn `pickup_address_id` + nhập `business_license_doc`, và đồng bộ regex SĐT VN với backend.

3. **[P1] `tools/seed/seed.mjs` mất idempotency sau khi gateway chặn auth trên `GET /api/v1/products` (CAT-B2)**
   - **Vị trí**: [seed.mjs:414-428](file:///home/tai/Document/nexusmart/tools/seed/seed.mjs#L414-L428) (đối chiếu [handlers_product.go:340](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_product.go#L340))
   - **Kịch bản hỏng**: `listAllAdminProducts(adminToken)` gọi `GET /api/v1/products?page=...` kèm admin JWT để liệt kê toàn bộ sản phẩm đã tồn tại. Do CAT-B2 đã dùng `PublicOutgoingContext` gỡ token trên route công khai này, mọi sản phẩm `PENDING`/`HIDDEN`/`REJECTED` bị ẩn khỏi kết quả → chạy lại `seed.mjs` (sau lần seed dang dở hoặc có sản phẩm bị ẩn) sẽ gọi lại `POST /api/v1/seller/products` trùng `slug` và lỗi `409 ALREADY_EXISTS`.
   - **Đề xuất sửa (1 dòng)**: Đổi endpoint trong `listAllAdminProducts` ở `tools/seed/seed.mjs:418` sang `/api/v1/admin/products?page=${page}&page_size=100`.

4. **[P1] 3 route admin self-audit ở `services/user` vẫn ghi IP container `gateway` thay vì `ClientIP` thật (R8 / USR-B4 — đã xác nhận trên `user_db`)**
   - **Vị trí**: [handlers_user.go:766](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_user.go#L766), [handlers_user.go:791](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_user.go#L791), [handlers_user.go:842](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_user.go#L842), [clients.go:124-137](file:///home/tai/Document/nexusmart/services/gateway/internal/clients/clients.go#L124-L137), [audit.go:28-32](file:///home/tai/Document/nexusmart/services/gateway/internal/middleware/audit.go#L28-L32), [helpers.go:46](file:///home/tai/Document/nexusmart/services/user/internal/service/helpers.go#L46), [helpers.go:163-177](file:///home/tai/Document/nexusmart/services/user/internal/service/helpers.go#L163-L177)
   - **Kịch bản hỏng**: Middleware `AdminAudit` bỏ qua `PATCH /api/v1/admin/users/{id}/status`, `PATCH /api/v1/admin/users/{id}/role`, và `POST /api/v1/admin/shops/{id}/review` để `services/user` tự ghi audit log. `services/user` đã đọc gRPC metadata `x-client-ip`, nhưng `services/gateway` **không hề gửi** metadata `x-client-ip`. Bằng chứng trực tiếp trong bảng `audit_logs` của `user_db` (`nexusmart-audit`): các action qua gateway middleware ghi `172.20.0.1`, trong khi `admin.shop.review` (4 dòng) ghi `172.20.0.15:58914` (IP + ephemeral port nội bộ của container `gateway`).
   - **Đề xuất sửa (1 dòng)**: Truyền thêm `"x-client-ip", h.trustedProxies.ClientIP(r)` qua `OutgoingContext` trong 3 handler admin của `handlers_user.go` (hoặc gắn tập trung cho mọi cuộc gọi gRPC từ HTTP request).

---

### P2 — Lệch đặc tả / edge case / race condition

5. **[P2] Race condition trong `ResubmitShop` có thể ghi đè shop vừa duyệt `ACTIVE` quay ngược về `PENDING`**
   - **Vị trí**: [user.sql:172-184](file:///home/tai/Document/nexusmart/services/user/queries/user.sql#L172-L184), [server.go:896-960](file:///home/tai/Document/nexusmart/services/user/internal/service/server.go#L896-L960)
   - **Kịch bản hỏng**: `RegisterShop` đọc `existing` ngoài transaction rồi gọi `ResubmitShop` chỉ có `WHERE id = $1` (khác `UpdateShopReview` có `AND status = 'PENDING'`). Khi shop ở trạng thái `REQUEST_INFO` (`PENDING` + `rejection_reason != ''`) và admin duyệt `APPROVE` (`status -> ACTIVE`, cấp `role='seller'` + `shop_id`) đồng thời với owner gọi `RegisterShop`, `ResubmitShop` chạy sau sẽ kéo `status` từ `ACTIVE` về lại `PENDING` trong khi user vẫn giữ role `seller` và `shop_id`.
   - **Đề xuất sửa (1 dòng)**: Thêm `AND (status = 'REJECTED' OR (status = 'PENDING' AND btrim(rejection_reason) <> ''))` vào `WHERE` của `ResubmitShop` và map `pgx.ErrNoRows` thành `codes.FailedPrecondition`.

6. **[P2] `resolveOrderShippingDefaults` dùng toán tử `??` với chuỗi rỗng `rawService`, chèn option rỗng vào dropdown dịch vụ giao hàng**
   - **Vị trí**: [types.ts:138-140](file:///home/tai/Document/nexusmart/web/src/features/seller/types.ts#L138-L140), [types.ts:158-172](file:///home/tai/Document/nexusmart/web/src/features/seller/types.ts#L158-L172)
   - **Kịch bản hỏng**: Với đơn hàng có `carrier_code: "ghn"` và `service_code: ""`, `matchedService` là `undefined`; biểu thức `matchedService?.serviceCode ?? rawService ?? matchedCarrier.services[0].serviceCode` trả về **`""`** vì `""` không nullish. `buildCarrierSelectOptions` sau đó chèn thêm `{ serviceCode: "", serviceLabel: "" }` vào `<select>` dịch vụ vận chuyển ở trang seller.
   - **Đề xuất sửa (1 dòng)**: Đổi dòng 139 thành `matchedService?.serviceCode || rawService || matchedCarrier.services[0].serviceCode`.

7. **[P2] `canRetryPayment("FAILED", true)` vẫn trả về `true` sau khi quá hạn thanh toán (`countdownExpired = true`)**
   - **Vị trí**: [helpers.ts:37-43](file:///home/tai/Document/nexusmart/web/src/features/checkout/helpers.ts#L37-L43), [RetryPaymentBox.tsx:301-302](file:///home/tai/Document/nexusmart/web/src/features/commerce/RetryPaymentBox.tsx#L301-L302), [RetryPaymentBox.tsx:379-417](file:///home/tai/Document/nexusmart/web/src/features/commerce/RetryPaymentBox.tsx#L379-L417), [orderMeta.ts:176-186](file:///home/tai/Document/nexusmart/web/src/features/account/orderMeta.ts#L176-L186)
   - **Kịch bản hỏng**: Nếu giao dịch VietQR ở trạng thái `FAILED` và đã quá `expires_at` (`countdownExpired === true`, trước khi watchdog hủy đơn), `canRetryPayment` vẫn trả `true` → `RetryPaymentBox` hiển thị đồng thời cả nút `"Thanh toán lại qua VietQR"` (bấm sẽ bị `OrderService.RetryPayment` trả 409 hết hạn) và nút `"Đặt lại từ giỏ hàng"`. Tương tự `needsVietQrPayment` không kiểm tra `payment_expires_at` đã quá hạn.
   - **Đề xuất sửa (1 dòng)**: Đổi `canRetryPayment` thành `return !countdownExpired && (status === "PENDING" || status === "FAILED");` (và bổ sung guard `payment_expires_at` trong `needsVietQrPayment`).

8. **[P2] `services/shipping` thiếu `USER_GRPC_ADDR` trong `docker-compose.yml`/`.env.example` và đặt default code là `"user:50051"`**
   - **Vị trí**: [config.go:54](file:///home/tai/Document/nexusmart/services/shipping/internal/config/config.go#L54), [docker-compose.yml:245-264](file:///home/tai/Document/nexusmart/docker-compose.yml#L245-L264), [.env.example](file:///home/tai/Document/nexusmart/.env.example)
   - **Kịch bản hỏng**: Các default khác trong `services/shipping/internal/config/config.go` đều dùng `localhost:*`, riêng `USER_GRPC_ADDR` default `"user:50051"` nên chạy local ngoài Docker sẽ lỗi DNS lookup `user:50051`; ngược lại `docker-compose.yml` không khai báo `USER_GRPC_ADDR` và `depends_on: user` cho `shipping`, phụ thuộc ngầm vào default này.
   - **Đề xuất sửa (1 dòng)**: Đổi default ở `config.go:54` thành `"localhost:50051"` và khai báo tường minh `USER_GRPC_ADDR: user:50051` + `depends_on: user` trong `docker-compose.yml` và `.env.example`.

9. **[P2] `MapGRPCError` ở gateway vẫn để lọt `st.Message()` thô cho `codes.Unavailable` (503) và `codes.DeadlineExceeded` (504)**
   - **Vị trí**: [response.go:98-102](file:///home/tai/Document/nexusmart/services/gateway/internal/httpx/response.go#L98-L102)
   - **Kịch bản hỏng**: Khi một microservice gRPC không kết nối được, `st.Message()` chứa chi tiết hạ tầng nội bộ (`dial tcp 172.20.0.x:50057: connect: connection refused`) và bị trả nguyên ra client ở HTTP 503 (dù lỗi 500 `Internal`/`Unknown`/`DataLoss` đã được che đúng).
   - **Đề xuất sửa (1 dòng)**: Trả message cố định cho `codes.Unavailable`/`codes.DeadlineExceeded` và log `err.Error()` server-side giống nhánh 500.

10. **[P2] Guard phòng thủ trên 3 handler sản phẩm công khai ở gateway fail-open với `PRODUCT_STATUS_UNSPECIFIED` và không đồng bộ `total`**
    - **Vị trí**: [handlers_product.go:406](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_product.go#L406), [handlers_product.go:431](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_product.go#L431), [handlers_product.go:450](file:///home/tai/Document/nexusmart/services/gateway/internal/router/handlers_product.go#L450)
    - **Kịch bản hỏng**: Điều kiện `p.GetStatus() != PRODUCT_STATUS_UNSPECIFIED && !isPublicProductStatus(p.GetStatus())` cho phép sản phẩm có status `UNSPECIFIED` (0) đi qua thay vì fail-closed; ngoài ra nếu vòng lọc loại bỏ phần tử ở `handleListProducts` thì `total` vẫn giữ `resp.GetTotal()`.
    - **Đề xuất sửa (1 dòng)**: Đổi điều kiện phòng thủ thành `!isPublicProductStatus(p.GetStatus())` trên cả 3 handler (sau khi bổ sung `Status: APPROVED` cho fixture test mock liên quan).

11. **[P2] Lệch tài liệu `docs/contracts.md` và code `RecordCODRejection` về quyền admin JWT**
    - **Vị trí**: [docs/contracts.md:287](file:///home/tai/Document/nexusmart/docs/contracts.md#L287) vs [server.go:1611-1613](file:///home/tai/Document/nexusmart/services/user/internal/service/server.go#L1611-L1613)
    - **Kịch bản hỏng**: `docs/contracts.md` mô tả `RecordCODRejection` chấp nhận `(service token / admin JWT)`, nhưng code thực tế yêu cầu bắt buộc `claims.Service != ""` (đúng theo `w1-user.md`) và trả `PERMISSION_DENIED` nếu gọi bằng admin JWT.
    - **Đề xuất sửa (1 dòng)**: Cập nhật `docs/contracts.md:287` thành `(service token)` cho khớp code và brief.

12. **[P2] E2E `18_product_rules_test.go:137` assert cứng `damRes.Total != 1` trên category dùng chung — dễ fail khi chạy lặp**
    - **Vị trí**: [18_product_rules_test.go:131-139](file:///home/tai/Document/nexusmart/tests/e2e/18_product_rules_test.go#L131-L139)
    - **Kịch bản hỏng**: Test tạo `"Đầm Linen Dáng Suông "+tag` trong `cat.ID` dùng chung nhưng search `keyword="dam"` (không kèm `tag`) và đòi `damRes.Total == 1`. Chạy E2E từ lần thứ 2 trở đi trên cùng DB không reset sẽ có `>= 2` sản phẩm `"Đầm..."` trong `cat.ID` và fail giả.
    - **Đề xuất sửa (1 dòng)**: Đổi điều kiện thành `damRes.Total >= 1 && prodFixHasProductID(damRes.Items, dress.ID)` (hoặc search `"dam " + tag` nếu muốn assert `== 1`).

13. **[P2 — Nghi ngờ] `isTrustedAuditIPCaller` ở `services/user` fail-open khi thiếu `peer` và `peerIPFromContext` giữ cả `:port`**
    - **Vị trí**: [helpers.go:138-161](file:///home/tai/Document/nexusmart/services/user/internal/service/helpers.go#L138-L161)
    - **Kịch bản hỏng (nghi ngờ)**: `isTrustedAuditIPCaller` trả `true` khi `!ok || p.Addr == nil` (dòng 152–154) và tin mọi peer có IP private (`10/8`, `172.16/12`, `192.168/16`) bất kể có service token hay không; khi fallback về `peerIPFromContext` (dòng 140) ghi chuỗi `host:port` (ví dụ `172.20.0.15:58914`) không đồng nhất với IP thuần của gateway.
    - **Cách kiểm chứng**: Rà soát mọi caller nội bộ trong mạng private gọi 3 RPC admin của `UserService`; chuẩn hóa `peerIPFromContext` tách host qua `net.SplitHostPort` và truyền mock `peer` trong unit test để fail-closed khi thiếu `peer`.
    - **Đề xuất sửa (1 dòng)**: Dùng `net.SplitHostPort` trong `peerIPFromContext` và đổi nhánh `!ok || p.Addr == nil` sang fail-closed `false` (chỉ tin `x-client-ip` từ peer/service đã cấu hình).

14. **[P2 — Nghi ngờ] Chênh lệch `cod_amount_vnd` trên đơn Flash Sale COD giữa `CheckoutClient` và `OrderService` khi bật `GHN_MODE=sandbox`**
    - **Vị trí**: [CheckoutClient.tsx:325](file:///home/tai/Document/nexusmart/web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L325) vs [checkout.go:850-852](file:///home/tai/Document/nexusmart/services/order/internal/service/checkout.go#L850-L852) và [ghn.go:188](file:///home/tai/Document/nexusmart/services/shipping/internal/carrier/ghn.go#L188)
    - **Kịch bản hỏng (nghi ngờ)**: Ở chế độ mock hiện tại, phí COD cố định (`> 0` là cộng `2000₫`/`1500₫`) nên chưa lệch; nhưng ở `GHN_MODE=sandbox` (`InsuranceValue: req.CODAmountVND`), `CheckoutClient` gửi `slice.subtotalVnd` (đã trừ flash sale) còn `OrderService` gửi `plan.subtotalVND` (chưa trừ flash sale) → phí bảo hiểm GHN và cache key quote có thể lệch nhau giữa màn checkout và lúc tạo đơn.
    - **Cách kiểm chứng**: Thử checkout Flash Sale COD với `GHN_MODE=sandbox` (hoặc test với 2 mức `InsuranceValue` vượt ngưỡng phí bảo hiểm GHN) để xác nhận chênh lệch phí.
    - **Đề xuất sửa (1 dòng)**: Thống nhất cùng công thức `cod_amount_vnd` (sau giảm flash sale hoặc giá gốc) giữa `CheckoutClient` và `OrderService.calculateShippingFees`.

---

### P3 — Nhỏ & Over-engineering (Ponytail Review)

15. **[P3 / `delete:`] `normalizedSKUInput.ExpectedVersion` và các nhánh check version trong `UpdateProduct` là dead code**
    - **Vị trí**: [server.go:473-475](file:///home/tai/Document/nexusmart/services/product/internal/server/server.go#L473-L475), [server.go:492-495](file:///home/tai/Document/nexusmart/services/product/internal/server/server.go#L492-L495), [server.go:1434](file:///home/tai/Document/nexusmart/services/product/internal/server/server.go#L1434) (đối chiếu [product.proto:55-64](file:///home/tai/Document/nexusmart/proto/nexusmart/product/v1/product.proto#L55-L64))
    - **Vấn đề**: Proto `SKUInput` không có trường `expected_version` và `validateSKUInputs` không bao giờ gán `ExpectedVersion`, nên mọi nhánh `in.ExpectedVersion > 0` không bao giờ chạy.
    - **Đề xuất sửa (1 dòng)**: Xoá `ExpectedVersion` khỏi `normalizedSKUInput` và các nhánh dead-code, truyền trực tiếp `cur.Version`/`old.Version`.

16. **[P3 / `shrink:`] `HandleWebhook` ở `services/shipping` lookup shipment & kiểm tra carrier 2 lần liên tiếp**
    - **Vị trí**: [shipping.go:409-425](file:///home/tai/Document/nexusmart/services/shipping/internal/service/shipping.go#L409-L425) trùng với [store.go:630-645](file:///home/tai/Document/nexusmart/services/shipping/internal/store/store.go#L630-L645)
    - **Vấn đề**: `HandleWebhook` query shipment + so sánh `carrierCode` ngoài transaction, rồi ngay sau đó `ApplyWebhookEvent` lặp lại đúng 2 bước đó (`FOR UPDATE`) trong transaction trước khi dedup `webhook_events` → tốn thêm 1–2 round-trip DB cho mọi webhook hợp lệ.
    - **Đề xuất sửa (1 dòng)**: Bỏ block pre-lookup ở `shipping.go:409-425`, để `ApplyWebhookEvent` kiểm tra tập trung trong transaction.

17. **[P3 / `shrink:`] Trùng lặp 2 closure retry `callListProvinces` và `callListWards` trong `validateProvinceWard`**
    - **Vị trí**: [server.go:1191-1260](file:///home/tai/Document/nexusmart/services/user/internal/service/server.go#L1191-L1260)
    - **Vấn đề**: Hai closure ~25 dòng lặp nguyên xi logic timeout 3s + 2 lần thử + backoff 50ms (~30 dòng thừa).
    - **Đề xuất sửa (1 dòng)**: Tách một helper generic nhỏ `retryShippingCall[T any](ctx, fn)` dùng chung cho cả hai RPC.

18. **[P3 / `delete:`] Dead code UI combo/bundle còn sót sau WEB-B2 & WEB-B3**
    - **Vị trí**: [PdpCrossSell.tsx:21-250](file:///home/tai/Document/nexusmart/web/src/features/product/PdpCrossSell.tsx#L21-L250) (`PdpBundleSection`), [productEditorial.ts:360-393](file:///home/tai/Document/nexusmart/web/src/mock/productEditorial.ts#L360-L393) (`getMockFlashExtras`), CSS `.combo*` ở [flash-sale/page.module.css:254-615](file:///home/tai/Document/nexusmart/web/src/app/(shop)/flash-sale/page.module.css#L254-L615) và [san-pham/[slug]/page.module.css:280-350](file:///home/tai/Document/nexusmart/web/src/app/(shop)/san-pham/%5Bslug%5D/page.module.css#L280-L350)
    - **Vấn đề**: `PdpBundleSection` và `getMockFlashExtras` không còn bất kỳ caller nào trong `web/src/`; các selector `.combo*` liên quan cũng không còn được gắn trên shop.
    - **Đề xuất sửa (1 dòng)**: Xoá `PdpBundleSection`, `getMockFlashExtras` và các class `.combo*` chết khỏi `web/src/`.

19. **[P3 / `shrink:`] Tham số variadic `trusted ...*TrustedProxies` và helper `firstTrustedProxies` không cần thiết**
    - **Vị trí**: [clientip.go:275-280](file:///home/tai/Document/nexusmart/services/gateway/internal/middleware/clientip.go#L275-L280), [logging.go:70](file:///home/tai/Document/nexusmart/services/gateway/internal/middleware/logging.go#L70), [ratelimit.go:102](file:///home/tai/Document/nexusmart/services/gateway/internal/middleware/ratelimit.go#L102), [ratelimit.go:123](file:///home/tai/Document/nexusmart/services/gateway/internal/middleware/ratelimit.go#L123)
    - **Vấn đề**: Dùng variadic chỉ để giữ tương thích cho vài lời gọi test nội bộ và âm thầm bỏ qua mọi phần tử từ vị trí thứ 2 trở đi.
    - **Đề xuất sửa (1 dòng)**: Đổi sang 1 tham số `tp *TrustedProxies` (nhận `nil`) và xoá `firstTrustedProxies`.

---

## Khu vực sạch (không phát hiện lỗi) và danh sách file đã đọc

- **`pkg/common/auth/**` (sạch)**: [auth.go](file:///home/tai/Document/nexusmart/pkg/common/auth/auth.go), [grpc_trust_test.go](file:///home/tai/Document/nexusmart/pkg/common/auth/grpc_trust_test.go).
- **`services/notification/**` (sạch — link thanh toán đa gian hàng `resolvePaymentOrderLink`, mẫu email/in-app COD & `order.returned`, định dạng VND/xu, escape HTML email đều khớp payload từ `order`/`payment`)**: [000002_order_participants_checkout_group.sql](file:///home/tai/Document/nexusmart/services/notification/migrations/000002_order_participants_checkout_group.sql), [notifications.sql](file:///home/tai/Document/nexusmart/services/notification/queries/notifications.sql), [models.go](file:///home/tai/Document/nexusmart/services/notification/internal/db/models.go), [notifications.sql.go](file:///home/tai/Document/nexusmart/services/notification/internal/db/notifications.sql.go), [store.go](file:///home/tai/Document/nexusmart/services/notification/internal/store/store.go), [mailer.go](file:///home/tai/Document/nexusmart/services/notification/internal/mailer/mailer.go), [mailer_test.go](file:///home/tai/Document/nexusmart/services/notification/internal/mailer/mailer_test.go), [notification.go](file:///home/tai/Document/nexusmart/services/notification/internal/service/notification.go), [notification_test.go](file:///home/tai/Document/nexusmart/services/notification/internal/service/notification_test.go), [consumer.go](file:///home/tai/Document/nexusmart/services/notification/internal/consumer/consumer.go), [server_test.go](file:///home/tai/Document/nexusmart/services/notification/internal/grpcserver/server_test.go).
- **E2E sạch (ngoài P2 #12 ở `18_product_rules_test.go`)**: [15_user_rules_test.go](file:///home/tai/Document/nexusmart/tests/e2e/15_user_rules_test.go), [16_gateway_rules_test.go](file:///home/tai/Document/nexusmart/tests/e2e/16_gateway_rules_test.go), [17_shipping_fee_test.go](file:///home/tai/Document/nexusmart/tests/e2e/17_shipping_fee_test.go), [05_audit_fixes.test.mjs](file:///home/tai/Document/nexusmart/tests/e2e-web/05_audit_fixes.test.mjs).
- **Các file khác đã đọc trong phạm vi**:
  - `services/gateway`: `cmd/gateway/main.go`, `internal/config/config.go`, `internal/clients/clients.go`, `internal/httpx/{response,response_test}.go`, `internal/middleware/{auth,audit,audit_test,clientip,clientip_logging_test,logging,middleware_test,ratelimit}.go`, `internal/router/{router,router_test,gateway_rules_test,handlers_payment,handlers_product,handlers_user}.go`.
  - `services/user`: `cmd/user/main.go`, `internal/config/config.go`, `migrations/0003_cod_rejections.sql`, `queries/user.sql`, `internal/db/{models,querier,user.sql}.go`, `internal/service/{bootstrap,helpers,server,server_test,rules_test}.go`.
  - `services/shipping`: `cmd/server/main.go`, `internal/config/config.go`, `internal/cache/{quote_cache,quote_cache_test}.go`, `internal/carrier/{fallback,ghn,mock_express,carrier_test}.go`, `internal/grpcserver/{server,server_test}.go`, `internal/service/{shipping,shipping_test}.go`, `internal/store/store.go`, `internal/userclient/{client,client_test}.go`, `internal/webhook/{handler,handler_test}.go`.
  - `services/product`: `proto/nexusmart/product/v1/product.proto`, `migrations/000004_product_search_text.sql`, `queries/product.sql`, `internal/db/{models,product.sql}.go`, `internal/store/{memory_store,pg_store,store,search,search_test}.go`, `internal/server/{server,rules_fix_test}.go`.
  - `web` & tài liệu đối chiếu: `web/src/app/(seller)/seller/don-hang/{page,[id]/page}.tsx`, `web/src/app/(shop)/{dang-nhap/page,flash-sale/FlashSaleClient,flash-sale/page,gio-hang/page,san-pham/[slug]/ProductDetailClient,san-pham/[slug]/page,thanh-toan/CheckoutClient,thanh-toan/page,thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient,don-hang/[id]/page}.tsx`, `web/src/app/api/gw/[...path]/route.ts`, `web/src/components/ProductCard.tsx`, `web/src/data/{membership,products}.ts`, `web/src/features/account/{AccountClient,OrderDetailClient,OrdersListClient,orderMeta}.tsx/.ts`, `web/src/features/checkout/{helpers,messages}.ts`, `web/src/features/commerce/RetryPaymentBox.tsx`, `web/src/features/product/PdpCrossSell.tsx`, `web/src/features/seller/{ProductFormView.tsx,types.ts}`, `web/src/lib/{api,catalog-mapper,redirect}.ts`, `web/src/lib/server/session.ts`, `web/scripts/audit-fixes.test.mjs`, `docker-compose.yml`, `.env.example`, `docs/contracts.md`, `tools/seed/seed.mjs`.
