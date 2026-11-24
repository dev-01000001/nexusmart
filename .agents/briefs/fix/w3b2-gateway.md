# 3b-2 — Agent `gateway` (A6 + A7)

- **Sở hữu**: `services/gateway/**`. Đọc `w2-rules.md`, `w3b-plan.md` §4 (F2, F5–F13), `w3b-design-a6.md`, `w3b-design-a7.md`, `docs/contracts.md` (§4.1, §4.5, §4.6, §4.9, §4.11, §7).
- **Cấm**: sửa `proto/`, `pkg/common/`, service khác, docker, `make up|seed|e2e|coverage`, git ghi.

## Phạm vi bắt buộc
1. **A6 — ZaloPay, `RetryPayment`, `payment-methods`**:
   - Proxy `POST /webhooks/zalopay` (công khai, không JWT, rate limit webhook, giữ nguyên raw body byte + `traceparent`) tới `payment:8086/webhooks/zalopay` (F2).
   - `GET /api/v1/payment-methods` (công khai / optional auth): trả `{"methods":[...]}` chỉ gồm trường công khai (`method`, `enabled`, `min_order_vnd`, `max_order_vnd`, `expiry_minutes`, `version`; bỏ `credentials_configured`, `credential_preview`, `updated_by`, `updated_at`, `note` — F6/F8).
   - `GET /api/v1/admin/payment-methods` (`payment_methods.read`) & `PUT /api/v1/admin/payment-methods/{method}` (`payment_methods.write`, truyền `expected_version` — F7).
   - `POST /api/v1/checkout`, `GET /api/v1/payments/{id}`, `POST /api/v1/payments/{id}/retry` (bắt buộc `Idempotency-Key`, mở rộng `method` ∈ `{VIETQR, ZALOPAY, COD}`, trả `pay_url`, `switched_to_cod`, `orders` — F5); `GET/POST /api/v1/admin/fees/payment` hỗ trợ `ZALOPAY`; `paymentDTO` có `pay_url`, `refundDTO` có `requested_by`, `approved_by`, `approved_at`, `approval_note`, `provider_m_refund_id`, `provider_status`, `status` hỗ trợ `PENDING_APPROVAL`/`REJECTED`/`MANUAL_REVIEW`.
2. **A7 — Ma trận quyền 6 vai trò theo `pkg/common/auth` (F9)**:
   - Bỏ nhóm cứng `RequireRoles("admin","super_admin")` cho toàn bộ `/api/v1/admin/*`; dùng `middleware.RequireRoles(auth.CapabilityRoles(cap)...)` (hoặc middleware `RequireCapability(cap)` dùng `auth.HasCapability`) khớp 100% `pkg/common/auth/capabilities.json` và `docs/contracts.md` §4.11 (gồm cả `/admin/banned-keywords*` và `/admin/reconciliation/cod/import`, `/admin/reconciliation/runs`).
   - Route chia sẻ (`GET /orders/{id}`, `/orders/{id}/timeline`, `/payments/{id}`, `/shipping/shipments/{order_id}`, `/shipping/labels/{tracking_code}`): mở nhánh admin cho vai trò có capability đọc tương ứng (`orders.read`, `refunds.read`/`ledger.read`, `carriers.read`).
3. **A7 — Route quản trị mới & Audit (F10–F14)**:
   - `POST /api/v1/admin/staff` (`staff.create`, không nhận field `password` — F14), `GET /api/v1/admin/users/{id}/activities` (`users.read`; gọi thêm `order.ListOrders(buyer_id=id, page=1, page_size=10)` bằng JWT người xem CHỈ khi có `orders.read`, ngược lại trả `recent_orders: []` — F13).
   - `POST /api/v1/admin/shops/{id}/suspend` (`shops.suspend.request`), `POST /api/v1/admin/shops/{id}/unsuspend` (`shops.unsuspend`), `POST /api/v1/admin/approvals/shop/{id}/decide` (`shops.suspend.decide`), `POST /api/v1/admin/approvals/refund/{id}/decide` (`refunds.approve`).
   - `GET /api/v1/admin/approvals` (`approvals.read`): gộp `user.AdminListShopApprovals` (chỉ gọi khi có `shops.approvals.read`) + `payment.ListRefunds(PENDING_APPROVAL)` (chỉ gọi khi có `refunds.read`).
   - Thêm các route tự ghi audit của `user` (`POST /api/v1/admin/staff`, `POST /api/v1/admin/shops/{id}/suspend`, `POST /api/v1/admin/shops/{id}/unsuspend`, `POST /api/v1/admin/approvals/shop/{id}/decide`) vào `userSelfAuditedRoutes` ở `internal/middleware/audit.go` để không ghi đúp.
4. **Mã lỗi chuẩn (`internal/httpx/response.go`)**: map `SHOP_SUSPENDED`, `PAYMENT_METHOD_DISABLED`, `ORDER_AMOUNT_OUT_OF_RANGE`, `VERSION_CONFLICT` → `409` đúng `error.code`; `MAKER_CHECKER_CONFLICT` → `403` đúng `error.code`.

## Tiêu chí nghiệm thu & Test bắt buộc
- **Bảng test RBAC bắt buộc (F9)**: MỌI route `/api/v1/admin/*` (cũ + 3b-1 + A6 + A7) × 6 vai trò nội bộ (`super_admin`, `admin`, `moderator`, `cskh`, `accountant`, `ops`) + `buyer` + `seller`, kiểm cả dương (không 403) và âm (`403`).
- Test proxy `/webhooks/zalopay` giữ nguyên raw body byte; test `/api/v1/payment-methods` công khai không lộ `credential_preview`/`updated_by`; test `/admin/users/{id}/activities` với `moderator` trả `200` và `recent_orders: []` (không gọi `order` gây 403); test `/admin/approvals` với từng vai trò; test mã lỗi `409`/`403` mới.
- Kiểm: `gofmt`, `golangci-lint`, `go build ./...`, `go test -race ./...` và test mới `-race -count=5`.
