# Thiết kế đợt 3b — Mảng A6: ZaloPay, UC-B25, UC-A13, UC-B06/B07

> Khảo sát read-only trên cây làm việc 3a. Tài liệu ZaloPay chính thức: [docs.zalopay.vn/v2/general/overview.html](https://docs.zalopay.vn/v2/general/overview.html) (tạo đơn, callback, truy vấn, hoàn tiền, truy vấn hoàn tiền). Không gọi sandbox, không ghi khóa.

## 1. Hiện trạng

- **Đã có và chạy thật**:
  - `payment` (Rust): mô hình tiền `int64` VND, sổ cái ghi kép cân bằng (`services/payment/src/ledger.rs:1-400`), outbox có lease (`services/payment/migrations/0008_outbox_lease_and_fee_config_index.sql:13-44`), `VietQrAdapter` mock/sandbox có timeout/retry/CB (`services/payment/src/vietqr.rs:1-400`).
  - **Chống trùng & giao dịch cũ thành công muộn (UC-B25 phần lõi)**: `supersede_previous` (`services/payment/src/service.rs:309-340`), `decide_webhook_action` (`services/payment/src/webhook_security.rs:278-295`), `record_late_payment_refund_tx` tạo `refunds(is_late_payment=TRUE, affects_escrow=FALSE)` với key `late-webhook:<provider_tx_id>` không phát `payment.succeeded` (`services/payment/src/service.rs:996-1045,4849-4929`), unique index `idx_payments_one_succeeded_per_order` (`0001_init.sql:134`) & `idx_payments_one_succeeded_per_group` (`0002_payment_money_model.sql:35`).
  - Phí thanh toán theo lịch (`payment_fee_configs`, `GetFeeConfig`/`SetFeeConfig`, `services/payment/src/grpc.rs:781-886`) và trang chờ thanh toán SSE/polling + countdown (`web/src/features/commerce/RetryPaymentBox.tsx:160-328`, `web/src/app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient.tsx:182-192`).
- **Thiếu so với từng UC**:
  - **ZaloPay adapter + mock**: chỉ có `COD`/`VIETQR` ở proto (`payment.proto:9-13`, `order.proto:22-26`), Rust domain/gRPC (`services/payment/src/domain.rs:12-34`, `src/grpc.rs:851-859,1061-1066,1120-1124`), CHECK SQL (`services/payment/migrations/0001_init.sql:117`, `0007_payment_fee_configs.sql:9`), Go statemachine/checkout (`services/order/internal/statemachine/statemachine.go:120-141`, `checkout.go:96-99,220,276-301,348-356`), gateway (`handlers_payment.go:133-142,825-827,866-868`, `handlers_order.go:457-466`), notification (`services/notification/internal/service/notification.go:1961-1970`). Chưa có route `/webhooks/zalopay` (`services/payment/src/http.rs:46-47`, `services/gateway/internal/router/router.go:120-121`), chưa có `ZaloPayAdapter`, chưa gọi `/v2/query` ở expiry/reconcile (`service.rs:2882,3224,3241`), chưa auto-refund qua `/v2/refund` + `/v2/query_refund` (`service.rs:1629-1710,4060`).
  - **UC-B25 (đổi phương thức khi trả lại)**: `RetryPayment` đang chặn `COD` và mọi phương thức khác `VIETQR` (`services/gateway/internal/router/handlers_payment.go:410-413`, `services/order/internal/service/lifecycle.go:963-969,1204-1206`, `services/order/internal/store/postgres.go:607`); SQL retry không cập nhật `payment_method`/`payment_fee_vnd`/`status` (`services/order/queries/orders.sql:150-161,279-286`); `order` hardcode `== VIETQR` cho online payment ở 6 chỗ (`lifecycle.go:312,388`, `handlers.go:353`, `store/postgres.go:999,1297`, `queries/orders.sql:650-658`, `0001_init.sql:65-67`, `checkout.go:1490-1493,2176-2179`).
  - **UC-A13 (cấu hình phương thức)**: chưa có bảng/RPC; `ORDER_COD_MAX_VND` (5.000.000) và `ORDER_PAYMENT_EXPIRY_MINUTES` (15) cố định bằng env (`services/order/internal/config/config.go:68-71`, `services/payment/src/config.rs:123-126`); chưa có kiểm tra bật/tắt & `min_order_vnd` lúc checkout/retry.
  - **UC-B06/B07 phần ví**: `CheckoutClient.tsx:1454-1531` chỉ có VietQR và COD, dùng `DEFAULT_COD_MAX_VND` cứng (`web/src/features/checkout/messages.ts:7`); `RetryPaymentBox.tsx:358,458` chỉ gửi `{ method: "VIETQR" }` và chỉ render QR, chưa có `pay_url` ZaloPay hay chọn đổi phương thức.
- **Dữ liệu mẫu ở web**:
  - `web/src/app/(admin)/admin/cau-hinh/page.tsx:101-125,249-265,958-1133` (Tab `thanh_toan`): bật/tắt COD/VietQR/"Ví NexusPay", thời gian giữ đơn đều là state giả in-memory trong `web/src/features/admin/auditStore.ts:41,144-198` (mất khi F5, không gọi API). `web/src/features/admin/adminMeta.ts:54-58` dùng nhãn `"Ví NexusPay"`.

## 2. Hợp đồng đề xuất

- **Giao thức ZaloPay v2 (chính thức `docs.zalopay.vn`, HMAC-SHA256 hex thường)**:
  - **Tạo đơn** (`POST {base}/v2/create`): `app_trans_id = yyMMdd_<unique>` (GMT+7), `app_time` (Unix ms), MAC (`key1`) = `app_id|app_trans_id|app_user|amount|app_time|embed_data|item`; lưu `order_url` → `pay_url`, `app_trans_id` → `provider_order_code`.
  - **IPN/Callback** (`POST /webhooks/zalopay` → proxy `gateway` → `payment/src/http.rs`): JSON `{"data":"<raw_json>","mac":"<hex>","type":1}`; verify constant-time `HMAC-SHA256(key2, raw_data_str) == mac`; chống replay: `|now_sec - data.server_time/1000| <= 300` (và chấp nhận `X-Timestamp` cho chế độ mock/nội bộ); `provider_tx_id = data.zp_trans_id` (`webhook_events.provider_tx_id UNIQUE`); trả `{"return_code":1,"return_message":"success"}` (`2` nếu trùng, `0` khi lỗi tạm để ZaloPay retry).
  - **Truy vấn** (`POST {base}/v2/query`): MAC (`key1`) = `app_id|app_trans_id|key1`; `return_code`: `1`=thành công (confirm payment thay vì hủy khi scanner chạy), `2`=thất bại, `3`=đang xử lý.
  - **Hoàn tiền** (`POST {base}/v2/refund` & `/v2/query_refund`): `m_refund_id = yyMMdd_<app_id>_<unique>`; MAC refund (`key1`) = `app_id|zp_trans_id|amount|description|timestamp`; MAC query_refund (`key1`) = `app_id|m_refund_id|timestamp`. Nếu `return_code=3`, worker định kỳ poll `query_refund` đến khi `1` (`completed` + `build_refund_settle_tx`) hoặc `2` (`failed` + chuyển đối soát tay).
- **Proto (số trường kế tiếp thật)**:
  - `payment.proto`: `PaymentMethod`: `PAYMENT_METHOD_ZALOPAY = 3` (`payment.proto:13`). `Payment`: `string pay_url = 16;` (`payment.proto:76`). `CreatePaymentRequest`: không cần đổi nếu dùng `method=ZALOPAY`. Thêm `PaymentMethodConfig` (`PaymentMethod method=1; bool enabled=2; int64 min_order_vnd=3; int64 max_order_vnd=4; int32 expiry_minutes=5; bool credentials_configured=6; string credential_preview=7; string updated_by=8; Timestamp updated_at=9; string note=10;`). Thêm 2 RPC trong `PaymentService`: `GetPaymentMethodConfigs`, `UpdatePaymentMethodConfig`.
  - `order.proto`: `OrderPaymentMethod`: `ORDER_PAYMENT_METHOD_ZALOPAY = 3` (`order.proto:26`). `CreateCheckoutResponse`: `string pay_url = 12;` (`order.proto:218`). `RetryPaymentResponse`: `OrderPaymentMethod payment_method = 4; bool switched_to_cod = 5; string pay_url = 6;` (`order.proto:335`).
- **REST & Quyền**:
  - `GET /api/v1/payment-methods` (public/authed, không trả `credential_preview`/`updated_by`) → `payment.GetPaymentMethodConfigs` (phục vụ `/thanh-toan` & `RetryPaymentBox`).
  - `GET /api/v1/admin/payment-methods` & `PUT /api/v1/admin/payment-methods/{method}` (quyền `admin`, `super_admin`, `ops`; ghi audit qua `AdminAudit`) → `payment.GetPaymentMethodConfigs` / `UpdatePaymentMethodConfig`.
  - `POST /api/v1/payments/{id}/retry` (buyer sở hữu, `Idempotency-Key` bắt buộc) mở rộng `method` ∈ `{VIETQR, ZALOPAY, COD}`; trả `{ payment, switched_to_cod, orders }`.
  - `POST /webhooks/zalopay` (public, không JWT, proxy raw body tới `payment:8086/webhooks/zalopay` qua `services/gateway/internal/proxy/proxy.go`, giữ nguyên byte body để verify `mac`).
  - Nội bộ: thêm `"GetPaymentMethodConfigs"` vào `SERVICE_METHODS` (`services/payment/src/auth.rs:21-27`) để `order` gọi bằng service token.
- **Event NATS JetStream**:
  - Tái dùng toàn bộ event hiện có (`payment.succeeded`, `payment.failed`, `payment.expired`, `payment.refunded`, `payment.refund_settled`, `order.confirmed`, `order.cancelled`) — không cần subject mới; đảm bảo `payment.method = "ZALOPAY"` truyền đúng qua payload tới `order` & `notification`.
  - Idempotent: `processed_events(event_id)` (`order`/`notification`), `processed_nats_events(event_id)` (`payment`). Đồng thời vá consumer shutdown/DLQ của `payment` theo `w3-plan.md §10` (Ack chỉ sau khi ghi `NEXUSMART_DLQ`).
- **Migration (số file kế tiếp thật sau 3a)**:
  - `services/payment/migrations/0009_zalopay_and_method_configs.sql`:
    - Cập nhật CHECK (`DROP CONSTRAINT IF EXISTS payments_method_check` / `payment_fee_configs_payment_method_check` rồi `ADD CONSTRAINT` cho `IN ('COD','VIETQR','ZALOPAY')`), seed `payment_fee_configs` cho `ZALOPAY` (0 bps).
    - Thêm cột `payments.pay_url TEXT`, `refunds.provider_m_refund_id TEXT UNIQUE`, `refunds.provider_status TEXT` (`pending|processing|succeeded|failed`).
    - Tạo lại index `idx_payments_pending_expiry ON payments (expires_at) WHERE status = 'pending' AND method IN ('VIETQR','ZALOPAY')`.
    - Tạo bảng `payment_method_configs (method TEXT PRIMARY KEY CHECK (...), enabled BOOLEAN NOT NULL DEFAULT TRUE, min_order_vnd BIGINT NOT NULL DEFAULT 0 CHECK (>=0), max_order_vnd BIGINT NOT NULL DEFAULT 0 CHECK (>=0), expiry_minutes INT NOT NULL DEFAULT 15 CHECK (expiry_minutes >= 0 AND expiry_minutes <= 1440), note TEXT NOT NULL DEFAULT '', updated_by TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())` và seed `COD` (max 5.000.000, expiry 0), `VIETQR` (max 0=không giới hạn, expiry 15), `ZALOPAY` (max 0, expiry 15).
  - `services/order/migrations/0015_zalopay_and_retry_switch.sql`:
    - Thêm `checkout_groups.pay_url TEXT NOT NULL DEFAULT ''`.
    - Tạo lại `idx_orders_pending_expiry ON orders (payment_expires_at) WHERE status = 'PENDING' AND payment_method IN ('VIETQR','ZALOPAY')` (thay index cũ `0001_init.sql:65-67`).
- **Biến env mới** (`.env.example`, không chứa key thật): `ZALOPAY_MODE` (`mock`|`sandbox`, mặc định `mock`), `ZALOPAY_BASE_URL` (`https://sb-openapi.zalopay.vn`), `ZALOPAY_APP_ID`, `ZALOPAY_KEY1`, `ZALOPAY_KEY2`, `ZALOPAY_CALLBACK_URL`, `ZALOPAY_REDIRECT_URL`, `ZALOPAY_HTTP_TIMEOUT_SECS` (10). Không thêm crate Rust/Go hay hạ tầng mới (dùng `hmac`, `sha2`, `hex`, `reqwest` có sẵn trong `services/payment/Cargo.toml`).

## 3. Bất biến tiền/quyền/đua

- **Chống cộng tiền 2 lần / xác nhận đè sau đổi phương thức**: khóa nhóm `lock_payment_group_tx` (`service.rs:270`) + unique index `idx_payments_one_succeeded_per_group` (`0002_payment_money_model.sql:35`). *Test đua*: gửi song song IPN ZaloPay của giao dịch cũ (`superseded`) và IPN VietQR của giao dịch mới (hoặc sau khi đã đổi sang COD) → chỉ 1 giao dịch `succeeded`; giao dịch cũ tạo đúng 1 `refunds(is_late_payment=TRUE)` với bút toán `DR gateway_clearing / CR refund_payable`, sổ cái tổng `debit == credit`.
- **Chống replay & giả mạo IPN ZaloPay**: constant-time HMAC với `key2` trên chuỗi `data` nguyên bản, bác bỏ nếu `|now - server_time/1000| > 300s`, unique `webhook_events.provider_tx_id`. *Test tấn công*: đổi 1 ký tự trong `data` (401/return_code=-1), đổi `amount` khác `payments.amount_vnd` (vào `reconciliation_queue`, không confirm), phát lại cùng `zp_trans_id` (idempotent, không ghi sổ lần 2), gửi `server_time` cũ 301 giây (từ chối).
- **Chống chiếm giữ tồn kho vô hạn qua retry**: hạn thanh toán khi retry không được vượt quá trần `min(original_expires_at, now + method_expiry)` (hoặc 1 lần mở rộng có trần cứng; đề xuất giữ nguyên `original_expires_at` của checkout group cho cả online↔online). *Test đua*: gọi `RetryPayment` đồng thời với scanner hết hạn → advisory lock `order_retry:<group_id>` (`store/postgres.go:577`) + `FOR UPDATE` đảm bảo hoặc hủy sạch hoặc thay thế sạch, không tạo đơn `CANCELLED` mà payment `pending`.
- **Chống lách hạn mức COD / Flash Sale / phương thức đang tắt qua `RetryPayment`**: đổi Online→COD phải chạy lại đủ 4 bước kiểm tra: không có Flash Sale item, COD đang bật (`enabled=true`), `min_order_vnd <= total_payable_vnd <= max_order_vnd`, `cod_rejected_count < threshold` (UC-PAY02). *Test tấn công*: đặt Flash Sale bằng VietQR rồi gọi `POST /payments/{id}/retry` với `method=COD` → `409 FAILED_PRECONDITION`; tắt `ZALOPAY` trong UC-A13 rồi gọi checkout/retry `ZALOPAY` → `409 FAILED_PRECONDITION`, nhưng IPN cho giao dịch ZaloPay đã tạo trước đó vẫn `200` thành công.
- **Bảo vệ secret**: không log `ZALOPAY_KEY1`/`ZALOPAY_KEY2` (dùng `mask_secret`), không nhận/trả secret dạng plaintext qua REST API cấu hình.

## 4. Câu phải chốt

1. **Tính lại phí thanh toán (`payment_fee_bps`) khi đổi phương thức lúc `RetryPayment`**:
   - *Đề xuất*: tính lại `payment_fee_bps`/`payment_fee_vnd` theo phương thức mới (phí thuộc người bán chịu, `C_total <= R`, `total_payable_vnd` của người mua không đổi) và cập nhật `ShopAllocation.commission_vnd` ở payment mới. *Vì sao*: khớp số tiền đối soát thực tế của kênh thanh toán mới và không bắt buyer trả lại khác giá đã xác nhận.
2. **Thời hạn thanh toán khi đổi phương thức lúc `RetryPayment` (Online ↔ Online)**:
   - *Đề xuất*: giữ nguyên `payment_expires_at` gốc của `checkout_group` (không cộng thêm thời gian). *Vì sao*: ngăn buyer bấm đổi VietQR↔ZaloPay liên tục để giữ tồn kho Flash Sale/SKU vô thời hạn; khớp sẵn logic `store/postgres.go:622-624`.
3. **Chính sách hoàn tiền cho giao dịch ZaloPay**:
   - *Đề xuất*: hoàn tự động về ví ZaloPay gốc qua `/v2/refund` + worker poll `/v2/query_refund` (không bắt buyer nộp STK ngân hàng như VietQR/COD); chỉ khi `/v2/refund` trả lỗi vĩnh viễn mới chuyển `manual_review` để kế toán xử lý tay. *Vì sao*: đúng đặc tả UC-B07/UC-PAY04 và tận dụng tối đa adapter ZaloPay.
4. **Cấu hình key/secret cổng thanh toán ở UC-A13**:
   - *Đề xuất*: key/secret (`ZALOPAY_KEY1/2`, `VIETQR_API_KEY`) tiếp tục nạp từ env/secret manager; API `GET /admin/payment-methods` chỉ trả `credentials_configured` + chuỗi đã che (`***1234`), không lưu secret vào `payment_method_configs`. *Vì sao*: tuân thủ `AGENTS.md` (không cần thêm crate mã hóa mới/ADR) và tránh lộ khóa qua DB backup.
5. **Tắt phương thức cuối cùng**:
   - *Đề xuất*: `UpdatePaymentMethodConfig` từ chối tắt nếu là phương thức duy nhất còn `enabled=true` (`InvalidArgument`). *Vì sao*: tránh vô tình khóa toàn bộ checkout sàn.

## 5. Chia việc theo service

- **Thứ tự**: (1) `proto/` + `make proto` + `docs/contracts.md` → (2) `services/payment` (migration + adapter + configs) → (3) `services/order` + `services/notification` → (4) `services/gateway` → (5) `web/` → (6) E2E.
- **`proto/` & `docs/`** (~90 dòng): `proto/nexusmart/payment/v1/payment.proto`, `proto/nexusmart/order/v1/order.proto`, `docs/contracts.md`, `.env.example`. *(Điểm giao chung với A7 ở `payment.proto` nếu A7 thêm approval hoàn tiền lớn — nên chốt proto 1 lượt đầu sprint.)*
- **`services/payment/`** (~950 dòng): `migrations/0009_zalopay_and_method_configs.sql`, `src/zalopay.rs` (mới, ~380 dòng theo chuẩn `vietqr.rs`), `src/config.rs`, `src/domain.rs`, `src/auth.rs`, `src/http.rs` (`/webhooks/zalopay`), `src/grpc.rs`, `src/service.rs`, `src/workers.rs` (query trước expiry + poll `query_refund` + DLQ hardening). *(Điểm giao chung với A7 ở `src/grpc.rs`, `src/auth.rs`, `src/service.rs`, `migrations/` — chốt số migration `0009_` cho A6, `0010_` cho A7.)*
- **`services/order/`** (~420 dòng): `migrations/0015_zalopay_and_retry_switch.sql`, `queries/orders.sql` + `sqlc generate`, `internal/statemachine/statemachine.go`, `internal/service/checkout.go`, `internal/service/lifecycle.go`, `internal/service/handlers.go`, `internal/store/postgres.go`.
- **`services/notification/`** (~30 dòng): `internal/service/notification.go:1961-1970` (thêm nhãn `"ví ZaloPay"`).
- **`services/gateway/`** (~280 dòng): `internal/router/router.go` (`/webhooks/zalopay`, `/payment-methods`, `/admin/payment-methods`), `internal/router/handlers_payment.go`, `internal/router/handlers_order.go`, `internal/proxy/proxy.go`. *(Điểm giao chung với A7 ở `router.go`.)*
- **`web/`** (~450 dòng): `src/lib/gateway-types.ts`, `src/lib/format.ts`, `src/app/(shop)/thanh-toan/page.tsx` & `CheckoutClient.tsx`, `src/features/commerce/RetryPaymentBox.tsx`, `src/app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient.tsx`, `src/features/account/orderMeta.ts`, `src/features/admin/AdminFeesPanel.tsx`, `src/app/(admin)/admin/cau-hinh/page.tsx` (thay Tab 3 mock bằng API thật).

## 6. Bài nghiệm thu

- **E2E backend (`tests/e2e/41_zalopay_payment_w3b_test.go`)**:
  1. Đặt đơn `ZALOPAY` (mock) → trả `pay_url`, đơn `PENDING`; gửi IPN chữ ký `key2` đúng → đơn `CONFIRMED`, sổ cái cân bằng; gửi lại cùng `zp_trans_id` → idempotent; gửi sai `mac` hoặc `server_time` > 300s → từ chối.
  2. UC-B25 đổi `ZALOPAY → VIETQR` và `VIETQR → COD` (thành công trên đơn thường, thất bại trên đơn Flash Sale / vượt hạn mức COD); gửi IPN muộn cho giao dịch ZaloPay cũ đã `superseded` → sinh `refunds(is_late_payment=TRUE)`, không đổi trạng thái đơn, sổ cái cân bằng.
  3. Hủy đơn ZaloPay đã thanh toán → tự động gọi mock `/v2/refund` + `/v2/query_refund` → `refunds.status = completed`, bút toán `refund_payable` về 0.
  4. UC-A13: Admin tắt `ZALOPAY`, hạ COD max xuống 500.000đ, đặt min order VietQR 50.000đ → checkout vi phạm bị chặn `409`, đơn ZaloPay tạo trước đó vẫn nhận IPN thành công; thử tắt nốt phương thức cuối → bị chặn `400`.
- **Thao tác trình duyệt thật (`tests/e2e-web/09_w3b_zalopay_config.test.mjs`)**:
  1. Vào `/admin/cau-hinh` → Tab "Cổng thanh toán" → thấy 3 phương thức thật (`COD`, `VIETQR`, `ZALOPAY`, không còn "Ví NexusPay") → sửa hạn mức COD = 2.000.000đ, bấm Lưu → F5 tải lại trang → giá trị 2.000.000đ vẫn giữ nguyên và có bản ghi trong `/admin/audit-log`.
  2. Đăng nhập buyer → `/thanh-toan` → thấy lựa chọn "Ví điện tử ZaloPay" và hạn mức COD mới 2.000.000đ → chọn ZaloPay → bấm "Đặt hàng" → chuyển `/thanh-toan/cho-thanh-toan/<id>` thấy nút thanh toán ZaloPay + đếm ngược.
  3. Trên trang chờ thanh toán bấm "Đổi phương thức thanh toán" → chọn `VIETQR` (hiện mã QR mới) hoặc chọn `COD` (đơn chuyển thẳng sang chi tiết đơn hàng trạng thái "Đã xác nhận").

## 7. Rủi ro

- **Phạm vi lớn ở `services/payment/src/service.rs`** (5.458 dòng): dễ phát sinh lỗi hồi quy trên các nhánh hardcode `"VIETQR"` (expiry, reconcile, cancel-refund, assign queue). *Giảm thiểu*: tách helper `is_online_method()` dùng chung ở cả Rust & Go, quét `rg "VIETQR"` sau khi sửa.
- **Độ trễ gọi `GetPaymentMethodConfigs` từ `order` lúc checkout**: mỗi checkout thêm 1 RPC tới `payment`. *Giảm thiểu*: cache TTL ngắn 15–30 giây trong `order` (hoặc cache có fallback gọi trực tiếp khi miss), tương tự pattern nhẹ trong service Go; chấp nhận chậm nhất 30s khi admin đổi cấu hình (hoặc đọc trực tiếp nếu tải thấp, ~2ms nội bộ).
- **Trạng thái hoàn tiền ZaloPay bất đồng bộ (`return_code=3`)**: nếu mất worker hoặc timeout mạng có thể treo ở `processing`. *Giảm thiểu*: lưu `provider_m_refund_id` trước khi gọi `/v2/refund` để retry idempotent và có job poll `query_refund` định kỳ.
- **Thứ nên cắt nếu quá tải**:
  1. Cắt đối soát batch sao kê tự động ZaloPay (vì ZaloPay v2 không có API list-all-transactions theo ngày công khai chuẩn như VietQR; giữ đối soát qua `/v2/query` theo giao dịch chờ & queue không khớp).
  2. Cắt `min_order_vnd` riêng cho từng shop (chỉ giữ cấp toàn sàn đúng UC-A13).
  3. Không làm lưu/xoay vòng secret qua UI trong đợt 3b (chỉ hiển thị trạng thái cấu hình masked từ env).
