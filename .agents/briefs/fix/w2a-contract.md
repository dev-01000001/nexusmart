# Đợt 2a – pha hợp đồng – agent `contract`

Đọc `w2-plan.md` (toàn bộ), `w2-rules.md`, `docs/contracts.md`, skill `add-rpc`, `add-event`. Bạn chỉ làm HỢP ĐỒNG và khung biên dịch được; KHÔNG cài logic nghiệp vụ.

## Sở hữu
`proto/**`, `gen/go/**` (qua `make proto`), `pkg/common/auth/**`, `services/payment/src/auth.rs` (chỉ `SERVICE_METHODS`), `docs/contracts.md`, `docker-compose.yml`, `.env.example`. Ngoài ra được thêm stub tối thiểu để các module biên dịch: Rust `services/payment/src/grpc.rs` trả `Status::unimplemented` cho RPC mới (tonic bắt buộc cài đủ trait); Go dùng `Unimplemented*Server` nhúng sẵn, chỉ sửa fake/mock trong test nếu interface client thủ công cần.

## Proto (chỉ thêm trường/RPC, không đổi số trường cũ, không xoá)
1. `order.proto`
   - `enum ReturnStatus`: UNSPECIFIED, PENDING, AWAITING_SHIPMENT, IN_TRANSIT, RECEIVED, REFUNDING, REFUNDED, REJECTED, ESCALATED, EVIDENCE_REQUESTED, CLOSED.
   - `message ReturnEvent {id, from_status, to_status, actor_id, actor_role, note, created_at}`.
   - `message ReturnRequest {id, order_id, order_code, buyer_id, shop_id, status, reason, evidence_urls, seller_response, seller_evidence_urls, dispute_reason, admin_note, refund_amount_vnd, return_tracking_code, return_label_url, return_fee_vnd, return_fee_bearer, goods_received, deadline_at, created_at, updated_at, events}`.
   - RPC: `GetReturnRequest {return_request_id | order_id}`, `ListReturnRequests {role_scope: buyer|seller|admin, status, page, page_size}`, `RespondReturn {return_request_id, decision: ACCEPT_REFUND|ACCEPT_RETURN|REJECT (enum), note, evidence_urls}`, `EscalateReturn {return_request_id, reason, evidence_urls}`, `AddReturnEvidence {return_request_id, note, evidence_urls}`, `ConfirmReturnReceived {return_request_id}`, `DisputeReturnGoods {return_request_id, reason, evidence_urls}`, `AdminResolveReturn {return_request_id, decision: REFUND|RETURN_AND_REFUND|REJECT|REQUEST_EVIDENCE (enum), note, return_fee_bearer}`. Response đều trả `ReturnRequest` (list thêm `total`).
   - `Order`: thêm `delivered_at`, `return_request_id`, `return_status` (cho UI). Kiểm các trường đã có trước khi thêm.
2. `shipping.proto`
   - `enum ShipmentDirection {UNSPECIFIED, FORWARD, RETURN}`; `Shipment` thêm `direction`, `return_request_id`, `redelivery_count`; `CancelShipmentRequest`, `GetShipmentRequest` thêm `direction` (unspecified = FORWARD).
   - RPC: `CreateReturnShipment {order_id, return_request_id, shop_id, from_address (người mua), to_address (kho shop), weight_grams, carrier_code, service_code, items_summary}` → `{shipment, already_existed}`; `RequestRedelivery {order_id, preferred_date, note}` → `{shipment}`; `GetShopShippingSettings {shop_id}` / `UpdateShopShippingSettings {shop_id, enabled_carrier_codes, default_weight_grams, default_length_cm, default_width_cm, default_height_cm}` → `{settings}` (chính sách freeship của shop KHÔNG làm ở 2a); `AdminListCarriers` → carriers `{carrier_code, name, enabled, token_configured, updated_at}`; `AdminUpdateCarrier {carrier_code, enabled, api_token (write-only), fallback_rates[]}`; `AdminTestCarrier {carrier_code}` → `{ok, message}`. `fallback_rate {region: SAME_PROVINCE|SAME_REGION|CROSS_REGION, base_fee_vnd, base_weight_grams, extra_fee_vnd_per_500g}`.
3. `payment.proto`
   - `message Refund` (dùng message sẵn có nếu có): thêm `status`, `bank_code`, `bank_account_masked`, `bank_account_name`, `provider_refund_id`, `is_late_payment`, `created_at`, `settled_at` khi thiếu.
   - RPC: `ListRefunds {status, order_id, page, page_size}` (admin mọi refund; buyer chỉ refund của mình), `SubmitRefundBankAccount {refund_id, bank_code, bank_account_number, bank_account_name}` (buyer chủ payment), `SettleRefund {refund_id, provider_refund_id, note, idempotency_key}` (admin), `ListReconciliationQueue {status, page, page_size}` (admin), `ResolveReconciliationItem {item_id, action: ASSIGN|REFUND|SUSPEND (enum), payment_id, note, idempotency_key}` (admin). Với admin, `Refund` có thêm `bank_account_number` đầy đủ; với buyer chỉ masked.
4. `user.proto`: `ForgotPassword {email}` → `{sent}`; `ResetPassword {token, new_password}` → `{ok}`. Thêm trường lý do khoá vào `User` nếu chưa có (`lock_reason`).

## Allowlist (`internalRPCs`, `SERVICE_METHODS`)
- Go: `OrderService` mọi RPC return mới = `false` (bắt buộc JWT người dùng); `ShippingService/CancelShipment` = `true`, `CreateReturnShipment` = `true`, `CreateShipment` giữ như hiện tại nếu không phá luồng, `RequestRedelivery` = `false`, `UpdateShopShippingSettings` = `false`, `Admin*Carrier*` = `false`. `GetShopShippingSettings` = `true`.
- Rust: bỏ `ReleaseEscrow` khỏi `SERVICE_METHODS`.
- Cập nhật `pkg/common/auth/grpc_trust_test.go` cho khớp.

## `docs/contracts.md`
- REST mới (bảng đúng mục): `GET /orders/{id}/return`, `GET /returns/{id}`, `GET /returns` (buyer), `POST /returns/{id}/escalate`, `POST /returns/{id}/evidence`, `POST /orders/{id}/redelivery`, `GET /orders/{id}/refunds`, `POST /refunds/{id}/bank-account`; `GET /seller/returns`, `GET /seller/returns/{id}`, `POST /seller/returns/{id}/respond`, `POST /seller/returns/{id}/confirm-received`, `POST /seller/returns/{id}/dispute`, `GET|PUT /seller/shipping-settings`; `GET /admin/returns`, `GET /admin/returns/{id}`, `POST /admin/returns/{id}/resolve`, `GET /admin/refunds`, `POST /admin/refunds/{id}/settle`, `GET /admin/reconciliation/queue`, `POST /admin/reconciliation/queue/{id}/resolve`, `GET /admin/carriers`, `PUT /admin/carriers/{code}`, `POST /admin/carriers/{code}/test`; `POST /auth/forgot-password`, `POST /auth/reset-password`. Tất cả dưới `/api/v1`. Thao tác ghi tiền của admin yêu cầu `Idempotency-Key`.
- Event mới: `order.return_updated` `{return_request_id, order_id, order_code, checkout_group_id, buyer_id, shop_id, status, previous_status, reason, return_tracking_code, deadline_at, actor_role}` (order → notification); `payment.refund_settled` `{refund_id, payment_id, order_id, user_id, amount_vnd}` (payment → notification); `user.password_reset_requested` `{user_id, email, full_name, reset_token, expires_at}` (user → notification; ghi rõ token không được log). `shipment.updated` thêm `direction`, `return_request_id`, `redelivery_count` (thiếu `direction` = `FORWARD`).
- Cập nhật §6 (máy trạng thái đơn, saga) theo `w2-plan.md` §1–2, thêm bảng trạng thái yêu cầu trả hàng; bỏ các câu "NOT YET IMPLEMENTED" tương ứng.
- Khoá Redis `user:revoked_before:<user_id>`.

## Env (compose + `.env.example`)
`ORDER_AUTO_COMPLETE_DAYS=7`, `ORDER_SELLER_CONFIRM_HOURS=48`, `ORDER_RETURN_SELLER_RESPONSE_DAYS=3`, `ORDER_RETURN_SHIP_DAYS=5`, `ORDER_RETURN_INSPECT_DAYS=3`, `PAYMENT_SELLER_HOLD_HOURS=72`, `SHIPPING_MAX_REDELIVERY=3`, `SHIPPING_CONFIG_KEY` (khoá mã hoá token hãng, 32 byte base64; default dev ghi rõ dev-only), `WEB_BASE_URL` cho link đặt lại mật khẩu ở notification (dùng biến có sẵn nếu đã có), `USER_GRPC_ADDR` cho notification (lấy chủ shop).

## Kiểm
`make proto` sạch (buf lint, không diff thừa), mọi module Go `go build ./... && go vet ./...`, `cargo build` + `cargo test` ở payment, `go test ./...` trong `pkg/common`. `docker compose config -q` ok. Không rebuild container.
