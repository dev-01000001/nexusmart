# Thiết kế đợt 3b — Mảng A7: Phân quyền nội bộ (UC-A11), Duyệt 2 người, Audit, A03, UC-A01

> Khảo sát read-only trên cây làm việc 3a. Mọi đường dẫn/dòng dựa trên mã nguồn hiện tại trên đĩa.

## 1. Hiện trạng

- **Đã có và chạy thật**:
  - **4 vai trò cơ bản**: `buyer`, `seller`, `admin`, `super_admin` (`proto/nexusmart/user/v1/user.proto:9-15`, `services/user/internal/service/helpers.go:31-35,113-115`). Bảng `users.role` là `TEXT` không có CHECK (`services/user/migrations/0001_init.sql:9`).
  - **Audit log append-only & lọc thời gian**: bảng `audit_logs` có trigger cấm UPDATE/DELETE (`services/user/migrations/0001_init.sql:75-109`); `ListAuditLogs` đã hỗ trợ `from_time`/`to_time` đủ 3 tầng: proto (`user.proto:378-386`), SQL (`services/user/queries/user.sql:384-400`), service (`services/user/internal/service/server.go:2034-2085`), gateway (`services/gateway/internal/router/handlers_user.go:1026-1067`) và web (`web/src/app/(admin)/admin/audit-log/page.tsx:38-39,64-69,194-213`). Gateway middleware `AdminAudit` tự ghi mọi mutation `/api/v1/admin/*` trừ 4 route self-audited của `user` (`services/gateway/internal/middleware/audit.go:30-35`).
  - **UC-A01 yêu cầu bổ sung hồ sơ (`REQUEST_INFO`)**: ĐÃ chạy thật end-to-end: `REVIEW_DECISION_REQUEST_INFO = 3` (`user.proto:36`), `AdminReviewShop` giữ `PENDING` + lưu `rejection_reason` (`user/server.go:1434-1439`), `RegisterShop` cho seller sửa & nộp lại (`user/server.go:1213-1222`), `notification` gửi email/in-app (`services/notification/internal/service/notification.go:637-644`), web đã có nút "Yêu cầu bổ sung" (`web/src/app/(admin)/admin/gian-hang/page.tsx:345,571-579`).
- **Thiếu so với từng UC**:
  - **UC-A11 (vai trò nội bộ)**: 47 route `/api/v1/admin/*` dùng chung 1 nhóm `RequireRoles("admin","super_admin")` (`services/gateway/internal/router/router.go:304,314-370`), và 6 service backend chỉ kiểm tra nhị phân `admin|super_admin` (`user/helpers.go:113`, `product/server.go:1948`, `promotion/promotion.go:2500`, `order/service.go:100`, `payment/src/auth.rs:40`, `shipping/server.go:400,408`). Chưa có 4 vai trò `moderator`, `cskh`, `accountant`, `ops`; chưa có RPC tạo tài khoản nhân viên; `AdminUpdateUserRole` đang cho cả `admin` thường đổi role thay vì khóa riêng `super_admin` (`user/server.go:1079`). `docs/contracts.md:156` chưa ghi tài liệu `from_time`/`to_time`.
  - **Duyệt 2 người (Maker-Checker)**: hoàn toàn chưa có cho hoàn tiền lớn và khóa gian hàng.
  - **UC-A03 (lịch sử hoạt động từng người dùng)**: `AdminUsersPanel.tsx:311-398` chỉ có sửa role & khóa/mở tài khoản; chưa có bảng/API ghi nhận sự kiện auth/hoạt động của người dùng (đăng nhập, đặt lại mật khẩu, hồ sơ, đơn gần đây, lịch sử từ chối COD).
  - **UC-A01 (khóa/mở gian hàng đang hoạt động)**: `SHOP_STATUS_SUSPENDED = 4` đã có ở enum & tab web (`user.proto:29`, `gian-hang/page.tsx:721`), nhưng `AdminReviewShop` chặn mọi shop khác `PENDING` (`user/server.go:1451-1453`). Đặc biệt: `effectiveSellerShopID` không kiểm tra trạng thái shop (`user/helpers.go:450-455`), `product` không biết shop bị khóa, và `order/checkout.go` không gọi `GetShop` kiểm tra `ACTIVE` trước khi tạo đơn!
- **Dữ liệu mẫu ở web**:
  - `web/src/app/(admin)/layout.tsx:12` khóa cứng `["admin","super_admin"]`; `AdminShell.tsx:67-74` gọi cả 4 API badge không lọc theo vai trò (sẽ 403 với nhân viên nội bộ). `web/src/features/admin/auditStore.ts:1-199` giữ state giả in-memory cho `cau-hinh`, `kiem-duyet` tab nội dung, `noi-dung`, và `doi-soat` tab `phien_mau` (`ADMIN_RECONCILIATION`). Các trang `gian-hang`, `audit-log`, `AdminUsersPanel` đã chạy API thật.

## 2. Hợp đồng đề xuất

### 2.1 Ma trận vai trò × 47 route/RPC admin hiện có (+ route mới A6/A7)

> Ký hiệu: `R`=xem, `W`=ghi/duyệt, `M`=lập yêu cầu 2 bước, `C`=duyệt bước 2 (`checker ≠ maker`), `—`=từ chối `403`. `super_admin` có toàn quyền mọi dòng.

| Nhóm route REST (`router.go:314-370`) & RPC backend | `admin` | `moderator` | `cskh` | `accountant` | `ops` |
|---|---|---|---|---|---|
| `GET /admin/users` (`user:AdminListUsers`, `server.go:944`) + **MỚI** `/users/{id}/activities` | R | R | R | R | R |
| `PATCH /admin/users/{id}/status` (`user:AdminUpdateUserStatus`, `server.go:983`) | W | — | W* | — | W |
| `PATCH /admin/users/{id}/role` (`user:AdminUpdateUserRole`, `server.go:1078`) + **MỚI** `POST /admin/staff` | —** | — | — | — | — |
| `GET /admin/shops` (`user:AdminListShops`, `server.go:1364`), `POST /shops/{id}/review` (`server.go:1404`) | W | W | R | R | W |
| **MỚI** `POST /admin/shops/{id}/suspend` & `POST .../approvals/{id}/decide` (2 người) | M/C | M | — | — | M/C |
| **MỚI** `POST /admin/shops/{id}/unsuspend` (`user:AdminUnsuspendShop`) | W | — | — | — | W |
| `GET /shops/{id}/bank-account`, `POST .../bank-account/review` (`user/bank_account.go:568,626`) | W | — | — | W | R |
| `GET /admin/audit-logs` (`user:ListAuditLogs`, `server.go:2034`) | R | R† | R† | R† | R |
| `GET/POST/PUT/DELETE /admin/categories*` (`product:server.go:100,195,327,389`) | W | R | — | R | W |
| `GET/POST /admin/fees/categories*` (`product:server.go:433,496`) | W | — | — | W | R |
| `GET /admin/products*`, `POST /products/{id}/review` (`product:server.go:1048,1218,1253`) | W | W | R | — | R |
| `GET/POST/PUT/PATCH/DELETE /admin/vouchers*` (`promotion:promotion.go:375,513`, `vouchers_w2b.go:22,270,324`) | W | — | R | R | W |
| `GET/POST /admin/flash-sales*`, `GET /flash-sale-items`, `POST .../approve` (`promotion:737,864`, `flash_w2b.go:112,183`) | W | W‡ | — | — | W |
| `POST /admin/coins/credit` (`promotion:CreditCoins`, `promotion.go:1020`) | W | — | W | W | W |
| `GET /admin/orders` (`order:ListOrders`, `lifecycle.go:81`), `GET/POST /orders/needs-review*` (`order/reports.go:327,365`) | W | — | R | R | W |
| `GET /admin/reports`, `GET /admin/reports/export` (`order:GetPlatformReport`, `reports.go:280` + `user:GetPlatformUserStats`, `stats.go:96`) | R | — | — | R | R |
| `GET /admin/returns*`, `POST /returns/{id}/resolve` (`order/returns.go:234,258,633`) | W | — | W | R | W |
| `GET/POST /admin/fees/payment` (`payment:GetFeeConfig`/`SetFeeConfig`, `grpc.rs:781,838`) | W | — | — | W | R |
| `GET /admin/ledger/summary` (`payment/grpc.rs:401`), `POST /payments/reconcile` (`grpc.rs:509`), `GET/POST /reconciliation/queue*` (`grpc.rs:682,719`) | W | — | — | W | R |
| `GET /admin/refunds` (`payment/grpc.rs:553`), `POST /payments/{id}/refund` (`grpc.rs:361`), `POST /refunds/{id}/settle` (`grpc.rs:642`) | W/M/C | — | M§ | W/M/C | R |
| `GET/PUT/POST /admin/carriers*` (`shipping/grpcserver/server.go:340,355,382`) | W | — | — | — | W |
| **MỚI (A6)** `GET/PUT /admin/payment-methods*` (`payment`) | W | — | — | R | W |

*Ghi chú ma trận*: `*` `cskh` chỉ khóa/mở `buyer`/`seller` (không tác động nhân viên/admin). `**` `PATCH /users/{id}/role` & `POST /admin/staff` **chỉ `super_admin`**. `†` `moderator`/`cskh`/`accountant` chỉ xem audit log phạm vi đối tượng của mình hoặc của chính mình (`actor_id=me`). `‡` `moderator` duyệt `flash-sale-items/{item_id}/approve`, không tạo campaign. `§` `cskh` tạo refund thường (< ngưỡng) hoặc lập yêu cầu refund lớn (`M`), không tự duyệt (`C`) hay `settle`.

### 2.2 Proto, REST, Event, Migration, Env

- **Proto (`user.proto`, số kế tiếp thật)**:
  - `UserRole` (`user.proto:9-15`): thêm `USER_ROLE_MODERATOR = 5; USER_ROLE_CSKH = 6; USER_ROLE_ACCOUNTANT = 7; USER_ROLE_OPS = 8;`.
  - `Shop` (`user.proto:63-78`): `string suspended_reason = 14; Timestamp suspended_at = 15;`.
  - Thêm RPC trong `UserService` (`user.proto:521-555`):
    - `AdminCreateStaffUser` (chỉ `super_admin`: email, full_name, phone, role ∈ 5..8|3, temporary_password → `ACTIVE`, ghi audit + buộc đổi mật khẩu hoặc gửi reset).
    - `AdminListUserActivities` (`user_id`, `from_time`, `to_time`, `category`, `page`, `page_size` → trả `UserActivityEntry[]` + `cod_rejected_count`).
    - `AdminRequestShopSuspension`, `AdminListShopApprovals`, `AdminDecideShopSuspension` (`APPROVE|REJECT`, `checker != maker`), `AdminUnsuspendShop`.
- **Proto (`payment.proto`, số kế tiếp thật)**:
  - `RefundStatus` (`payment.proto:25-30`): thêm `REFUND_STATUS_PENDING_APPROVAL = 4; REFUND_STATUS_REJECTED = 5;`.
  - `Refund` (`payment.proto:79-97`): thêm `string requested_by = 17; string approved_by = 18; Timestamp approved_at = 19; string approval_note = 20;`.
  - Thêm RPC `DecideRefundApproval` (`refund_id`, `approve`, `note`) trên `PaymentService` (`payment.proto:353-370`).
- **REST mới** (ghi audit: thêm route `user` mới vào `userSelfAuditedRoutes` ở `gateway/middleware/audit.go:30-35`):
  - `POST /api/v1/admin/staff` (`super_admin`), `GET /api/v1/admin/users/{id}/activities` (mọi role nội bộ — gateway trả kèm 10 đơn gần nhất từ `order.ListOrders(buyer_id=id)`).
  - `POST /api/v1/admin/shops/{id}/suspend` (`admin`, `super_admin`, `ops`, `moderator` → tạo yêu cầu 2 bước), `POST /api/v1/admin/shops/{id}/unsuspend` (`admin`, `super_admin`, `ops`).
  - `GET /api/v1/admin/approvals` (gateway gộp `user.AdminListShopApprovals` + `payment.ListRefunds(status=pending_approval)`), `POST /api/v1/admin/approvals/shop/{id}/decide` (`admin`, `super_admin`, `ops`), `POST /api/v1/admin/approvals/refund/{id}/decide` (`admin`, `super_admin`, `accountant`).
- **Event NATS JetStream**:
  - **MỚI `user.shop_status_changed`**: publisher `user` (`services/user/internal/events/publisher.go:67`, thêm `outbox_events` nhẹ hoặc publish sau tx — khuyến nghị outbox giống `order`/`payment`); payload `{shop_id, owner_user_id, shop_name, from_status, to_status, reason, actor_id, approver_id}`.
    - Consumer `product`: idempotent qua `processed_events(event_id)` — set `products.shop_suspended = (to_status == 'SUSPENDED')` (ẩn khỏi `ListProducts`/`GetProduct` công khai mà giữ nguyên `status` gốc `APPROVED`/`HIDDEN` để khi `ACTIVE` lại mở đúng trạng thái cũ!).
    - Consumer `notification`: gửi email + in-app cho chủ shop.
- **Migration (số file kế tiếp thật sau 3a)**:
  - `services/user/migrations/0008_w3b_governance_approvals_activity.sql`: thêm `shops.suspended_at`, `shops.suspended_reason`; bảng `shop_suspension_approvals (id UUID PK, shop_id UUID NOT NULL, status TEXT CHECK IN ('PENDING','APPROVED','REJECTED'), reason TEXT NOT NULL, requested_by UUID NOT NULL, requested_role TEXT NOT NULL, decided_by UUID, decided_note TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ, decided_at TIMESTAMPTZ, CHECK (decided_by IS NULL OR decided_by <> requested_by))` + unique partial index `WHERE status = 'PENDING'` trên `shop_id`; bảng append-only `user_activity_logs (id UUID PK, user_id UUID NOT NULL, event_type TEXT NOT NULL, ip_address TEXT NOT NULL DEFAULT '', metadata_json TEXT NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())` + index `(user_id, created_at DESC)`.
  - `services/payment/migrations/0010_w3b_refund_two_person_approval.sql` (nối sau `0009_` của A6): mở rộng CHECK `refunds.status IN ('pending_approval','pending','completed','failed','manual_review','rejected')`; thêm `requested_by TEXT`, `approved_by TEXT`, `approved_at TIMESTAMPTZ`, `approval_note TEXT`, ràng buộc `CHECK (approved_by IS NULL OR requested_by IS NULL OR approved_by <> requested_by)` và index `WHERE status = 'pending_approval'`.
  - `services/product/migrations/000008_shop_suspended_flag.sql`: thêm `products.shop_suspended BOOLEAN NOT NULL DEFAULT FALSE`, index `(shop_id) WHERE shop_suspended = TRUE`, thêm `processed_events` nếu `product` chưa có consumer NATS.
- **Env mới**: `PAYMENT_LARGE_REFUND_APPROVAL_VND` (mặc định `5000000`, `services/payment/src/config.rs`). Không thêm dependency/hạ tầng mới.

## 3. Bất biến tiền/quyền/đua

- **Chống tự duyệt (Maker ≠ Checker) ở 2 tầng**: kiểm tra ở ứng dụng (`caller.user_id != requested_by`) VÀ DB CHECK constraint (`decided_by <> requested_by`, `approved_by <> requested_by`). *Test tấn công*: `admin A` lập yêu cầu khóa shop hoặc hoàn tiền 6.000.000đ rồi tự gọi `Decide` bằng JWT của `A` → `403 PERMISSION_DENIED`.
- **Chống giữ chỗ/ghi sổ sớm trước khi đủ 2 chữ ký**: hoàn tiền lớn ở trạng thái `pending_approval` **CHƯA** post sổ cái `build_refund_tx` và **CHƯA** phát `payment.refunded`; chỉ khi checker duyệt trong 1 SQL tx mới kiểm tra lại hạn mức hoàn còn lại (`FOR UPDATE`), chuyển `pending`, ghi sổ cái và outbox. *Test đua*: 2 checker duyệt đồng thời 1 yêu cầu → `SELECT ... FOR UPDATE` + `status = 'pending_approval'` đảm bảo chỉ 1 thành công, 1 nhận `409 FAILED_PRECONDITION`, không hoàn tiền 2 lần.
- **Defense-in-depth phân quyền vai trò nội bộ**: `pkg/common/auth/auth.go` (Go) và `services/payment/src/auth.rs` (Rust) kiểm tra đúng tập vai trò cho từng RPC, không tin mỗi gateway. *Test tấn công*: dùng JWT `moderator` gọi thẳng gRPC `payment.SettleRefund`, `user.AdminUpdateUserRole`, `order.AdminResolveReturn` → đều `PermissionDenied`; `cskh` thử khóa tài khoản `admin` → `PermissionDenied`.
- **Khóa gian hàng chặn đơn mới & ẩn sản phẩm nhưng giữ đơn đang chạy**: khi `SHOP_STATUS_SUSPENDED`, `product` chặn `CreateProduct`/`UpdateProduct`/`UpdateStock` & ẩn khỏi storefront; `order.CreateCheckout` gọi `User.GetShop` kiểm tra `status == ACTIVE` (từ chối đơn mới `409 SHOP_SUSPENDED`), trong khi đơn đã tạo trước đó vẫn cho seller xử lý (`SellerShipOrder`, đối soát) và **không** xóa hay sửa dữ liệu sổ cái. *Test đua*: buyer bấm checkout đúng lúc checker duyệt khóa shop → hoặc trước khi khóa (đơn đi tiếp bình thường) hoặc sau khi khóa (checkout bị chặn).
- **Bảo vệ `super_admin` cuối cùng & tính toàn vẹn audit**: giữ quy tắc không khóa/hạ cấp `super_admin` cuối (`user/server.go:1029,1123`); không mở bất kỳ quyền sửa/xóa `audit_logs` hay `user_activity_logs`.

## 4. Câu phải chốt

1. **Đặt bảng duyệt 2 người theo từng domain (`user_db` + `payment_db`) hay tập trung 1 bảng ở `user_db`?**
   - *Đề xuất*: đặt theo domain (`shop_suspension_approvals` ở `user_db`, `refunds.status='pending_approval'` ở `payment_db`), gateway gộp API `GET /api/v1/admin/approvals`. *Vì sao*: giữ nguyên tử 1-tx (duyệt xong ghi sổ/khóa shop ngay, không lỗi nửa vời 2-phase), không tạo kết nối gRPC mới `user ↔ payment`, và đặt được DB CHECK `checker <> maker` chống bypass trực tiếp trên từng service.
2. **Ngưỡng "hoàn tiền lớn" áp dụng cho luồng nào?**
   - *Đề xuất*: áp dụng cho hoàn tiền thủ công admin (`POST /api/v1/admin/payments/{id}/refund` khi `amount_vnd >= PAYMENT_LARGE_REFUND_APPROVAL_VND`, mặc định 5.000.000đ) và `AdminResolveReturn(REFUND)` có `refund_amount_vnd >= ngưỡng` (không chặn auto-refund do hủy đơn/late-payment từ saga service token). *Vì sao*: không làm kẹt luồng saga tự động, vẫn khóa chặt mọi khoản chi lớn do con người quyết định.
3. **Khi shop bị `SUSPENDED`, quyền seller JWT xử lý thế nào?**
   - *Đề xuất*: thu hồi refresh token & `revoked_before` ngay khi khóa để ép lấy token mới; vẫn cấp `shop_id` cho đơn đang chạy trong `order` (đúng UC-A01), nhưng `product` (cập nhật từ `user.shop_status_changed`) chặn tạo/sửa sản phẩm và `order.CreateCheckout` chặn đơn mới bằng `GetShop`. *Vì sao*: đáp ứng đúng UC-A01 ("chủ shop xử lý nốt đơn đang chạy, không tạo SP mới, không nhận đơn mới") mà không đụng nhiều vào mô hình JWT hiện có.
4. **Hành vi `admin` thường khi đã có 4 vai trò chuyên biệt**:
   - *Đề xuất*: giữ `admin` như vai trò quản trị vận hành tổng hợp (tương thích ngược toàn bộ test E2E 1–40 hiện có), chỉ tách riêng việc tạo nhân viên & đổi vai trò cho `super_admin`. *Vì sao*: tránh vỡ hàng loạt test đang dùng tài khoản `admin` seed sẵn.

## 5. Chia việc theo service

- **Thứ tự**: (1) `pkg/common/auth` + `proto/` + `docs/contracts.md` → (2) `services/user` → (3) `services/payment` + `services/product` + `services/order` + `services/promotion` + `services/shipping` + `services/notification` → (4) `services/gateway` → (5) `web/` → (6) E2E.
- **`pkg/common/auth/auth.go`** (~60 dòng): hằng số 4 vai trò mới + helper nhóm quyền (`IsStaffRole`, `CanModerate`, `CanSupport`, `CanAccount`, `CanOps`).
- **`proto/` & `docs/`** (~120 dòng): `user.proto`, `payment.proto`, `docs/contracts.md` (cập nhật ma trận + `from_time`/`to_time` audit). *(Giao chung với A6 ở `payment.proto`.)*
- **`services/user/`** (~650 dòng): `migrations/0008_w3b_governance_approvals_activity.sql`, `queries/user.sql` + `sqlc generate`, `internal/service/helpers.go`, `internal/service/server.go`, `internal/service/approvals.go` (mới), `internal/events/publisher.go`.
- **`services/payment/`** (~320 dòng): `migrations/0010_w3b_refund_two_person_approval.sql`, `src/auth.rs` (4 vai trò nội bộ + helper theo nhóm), `src/config.rs`, `src/grpc.rs`, `src/service.rs`. *(Giao chung với A6 ở `src/auth.rs`, `src/grpc.rs`, `src/service.rs`.)*
- **`services/product/`** (~220 dòng): `migrations/000008_shop_suspended_flag.sql`, `internal/server/server.go` (phân quyền `moderator`/`ops`/`accountant` + lọc `shop_suspended = FALSE` ở public + chặn seller mutation khi shop suspended), consumer `user.shop_status_changed`.
- **`services/order/`, `promotion/`, `shipping/`, `notification/`** (~260 dòng tổng): cập nhật hàm kiểm tra vai trò theo ma trận ở `order/service.go`, `order/returns.go`, `order/reports.go`, `order/checkout.go` (gọi `GetShop` kiểm `ACTIVE`), `promotion/promotion.go`, `shipping/grpcserver/server.go`, và `notification.go` (nhận `user.shop_status_changed`).
- **`services/gateway/`** (~360 dòng): `internal/router/router.go` (tách nhóm `RequireRoles` theo ma trận), `internal/middleware/audit.go`, `internal/router/handlers_user.go`, `handlers_payment.go`. *(Giao chung với A6 ở `router.go`.)*
- **`web/`** (~550 dòng): `src/lib/gateway-types.ts`, `src/app/(admin)/layout.tsx`, `AdminShell.tsx` (lọc menu/badge theo role), `src/features/admin/types.ts`, `AdminUsersPanel.tsx` (tạo nhân viên, 4 role mới, ngăn kéo lịch sử hoạt động A03), `src/app/(admin)/admin/gian-hang/page.tsx` (nút Yêu cầu khóa / Mở khóa shop), `src/app/(admin)/admin/doi-soat/page.tsx` (trạng thái chờ duyệt 2 người), thêm tab/mục "Chờ duyệt 2 người" trong `/admin/cau-hinh` hoặc `/admin/gian-hang`.

## 6. Bài nghiệm thu

- **E2E backend (`tests/e2e/42_admin_rbac_approvals_w3b_test.go`)**:
  1. `super_admin` tạo 4 nhân viên (`moderator`, `cskh`, `accountant`, `ops`); kiểm tra đủ ma trận dương & âm (vd: `moderator` duyệt SP/shop `200`, gọi `settle refund`/`credit coins`/`update carrier` → `403`; `accountant` xem báo cáo/đối soát/settle `200`, duyệt SP → `403`; `cskh` phân xử trả hàng `200`, đổi fee → `403`; `admin` thường gọi `POST /admin/staff` hoặc đổi role → `403`).
  2. Duyệt 2 người khóa shop: `ops1` gửi `POST /admin/shops/{id}/suspend` → shop vẫn `ACTIVE`; `ops1` tự duyệt → `403`; `ops2` duyệt → shop thành `SUSPENDED`, sản phẩm ẩn khỏi `GET /api/v1/products`, checkout mới vào shop bị `409`, đơn cũ vẫn chuyển trạng thái được; `ops2` mở khóa `unsuspend` → sản phẩm `APPROVED` hiện lại, sản phẩm seller tự ẩn vẫn `HIDDEN`.
  3. Duyệt 2 người hoàn tiền lớn: `cskh` tạo refund 6.000.000đ → `pending_approval`, sổ cái chưa đổi; `cskh` tự duyệt → `403`; `accountant` duyệt → `pending`, sổ cái ghi `DR escrow/CR refund_payable`, xuất hiện trên `GET /admin/audit-logs?from_time=...&to_time=...`.
  4. UC-A03: buyer đăng nhập, đặt đơn, bị khóa/mở khóa → `GET /admin/users/{id}/activities` trả đủ timeline login, thay đổi trạng thái và đơn gần đây.
- **Thao tác trình duyệt thật (`tests/e2e-web/10_w3b_admin_governance.test.mjs`)**:
  1. Đăng nhập `moderator` → vào `/admin` → sidebar chỉ hiện Tổng quan/Duyệt gian hàng/Kiểm duyệt, không báo lỗi 403 badge; mở trực tiếp `/admin/doi-soat` thấy màn hình không đủ quyền.
  2. Đăng nhập `super_admin` → `/admin/cau-hinh` → Tab "Người dùng" → bấm "Tạo nhân viên" vai trò Kế toán, mở chi tiết 1 buyer → xem tab "Lịch sử hoạt động" thấy log đăng nhập + đơn hàng.
  3. Đăng nhập `ops1` → `/admin/gian-hang` → chọn shop `ACTIVE` → bấm "Yêu cầu khóa gian hàng" + nhập lý do → đổi phiên `ops2` → vào hàng đợi "Chờ duyệt 2 người" → bấm "Chấp thuận" → shop chuyển badge "Tạm ngưng"; vào `/admin/audit-log` lọc "Từ ngày / Đến ngày" → thấy đúng cặp bản ghi yêu cầu & phê duyệt.

## 7. Rủi ro

- **Phạm vi chạm 7 service cho ma trận RBAC**: dễ sót 1 RPC kiểm tra `isAdmin()` cũ gây từ chối sai (`403` cho vai trò được phép) hoặc hở quyền. *Giảm thiểu*: định nghĩa bảng capability tập trung trong `pkg/common/auth` (Go) + mirror trong `payment/src/auth.rs`, viết table-driven test cho đủ 6 vai trò × mọi admin RPC.
- **Độ trễ bất đồng bộ `user.shop_status_changed` tới `product`**: trong vài trăm ms sau khi khóa shop, sản phẩm có thể vẫn thấy trên list trước khi consumer xử lý xong. *Giảm thiểu*: `order.CreateCheckout` kiểm tra đồng bộ `User.GetShop` nên **tuyệt đối không lọt đơn mới** ngay cả trong cửa sổ ms đó.
- **Xung đột hợp nhất với A6**: cả hai cùng sửa `payment.proto`, `services/payment/src/{auth,grpc,service}.rs`, `services/gateway/internal/router/router.go`. *Giảm thiểu*: chốt hợp đồng proto & thứ tự migration (`payment 0009_` A6, `0010_` A7) trước khi chia nhánh.
- **Thứ nên cắt nếu quá tải**:
  1. Cắt tính năng buộc đổi mật khẩu lần đầu cho nhân viên mới (chỉ cần tạo mật khẩu ban đầu + dùng sẵn luồng quên/đặt lại mật khẩu hiện có).
  2. Cắt bộ lọc `action` nâng cao và xuất CSV audit log (tập trung giữ đúng `from_time`/`to_time` + quyền xem theo vai trò).
  3. Cắt thay toàn bộ tab mock khác trong `cau-hinh`/`noi-dung`/`kiem-duyet` (ngoài phạm vi A7).
