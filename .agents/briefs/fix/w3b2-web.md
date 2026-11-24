# 3b-2 — Agent `web` (A6 + A7)

- **Sở hữu**: `web/**` và `tests/e2e-web/11_*.test.mjs`, `tests/e2e-web/12_*.test.mjs`. Đọc `web/CLAUDE.md`, `design-system` skill, `w3b-plan.md` §4 (F5–F15), `w3b-design-a6.md`, `w3b-design-a7.md`, `docs/contracts.md`.
- **Cấm**: sửa `proto/`, `services/`, `pkg/common/`, docker, `make up|seed|e2e|e2e-web`, git ghi.

## Phạm vi bắt buộc
1. **A6 — Thanh toán ZaloPay & đổi phương thức (F5–F8)**:
   - `src/lib/gateway-types.ts`, `src/lib/format.ts`, `orderMeta.ts`: thêm `ZALOPAY`, `pay_url`, `PaymentMethodConfig`, trạng thái refund `PENDING_APPROVAL`/`REJECTED`/`MANUAL_REVIEW`, 4 vai trò nhân viên mới, `ShopSuspensionApproval`, `UserActivityEntry`.
   - `/thanh-toan` (`CheckoutClient.tsx`): đọc `GET /api/v1/payment-methods` thật (bỏ `DEFAULT_COD_MAX_VND` cứng); ẩn phương thức `enabled=false`; hiển thị lựa chọn `"Ví điện tử ZaloPay"` và hạn mức min/max thật; xử lý mã lỗi `SHOP_SUSPENDED`, `PAYMENT_METHOD_DISABLED`, `ORDER_AMOUNT_OUT_OF_RANGE`.
   - `/thanh-toan/cho-thanh-toan/[paymentId]` (`PaymentWaitingClient.tsx`) & `RetryPaymentBox.tsx`: khi `ZALOPAY`, hiện nút mở `pay_url` — **bắt buộc validate URL**: chỉ chấp nhận `https:` với host thuộc allowlist ZaloPay (`*.zalopay.vn`, `sb-openapi.zalopay.vn`, `qcgateway.zalopay.vn`, `sbgateway.zalopay.vn`) hoặc trang mô phỏng mock **cùng origin** (F8; từ chối `javascript:`, `http:` ngoài origin, host lạ). Mở rộng `RetryPaymentBox` cho đổi giữa `VIETQR`, `ZALOPAY`, `COD` (kèm `Idempotency-Key`; đổi sang `COD` chuyển sang chi tiết đơn đã xác nhận).
   - `/admin/cau-hinh` (Tab thanh toán): **gỡ hẳn** dữ liệu mẫu in-memory trong `auditStore.ts` và nhãn `"Ví NexusPay"` (`adminMeta.ts`); kết nối `GET/PUT /api/v1/admin/payment-methods` với `expected_version` (xử lý `409 VERSION_CONFLICT` báo tải lại — F7/F8); bổ sung phí `ZALOPAY` trong `AdminFeesPanel.tsx`.
2. **A7 — Vai trò nội bộ, tạo nhân viên, duyệt 2 người, hoạt động người dùng (F9–F15)**:
   - `src/app/(admin)/layout.tsx` & `AdminShell.tsx`: mở quyền vào `/admin` cho cả 6 vai trò nội bộ (`super_admin`, `admin`, `moderator`, `cskh`, `accountant`, `ops`); mirror bảng capability theo `pkg/common/auth/capabilities.json` để lọc menu sidebar, chỉ gọi API badge mà vai trò có quyền (0 request 403 trên sidebar!), và khi mở thẳng URL không đủ quyền hiển thị màn hình từ chối mà **không** gọi API sẽ 403 (F15).
   - `AdminUsersPanel.tsx`: hiện đủ 6 vai trò nội bộ; nút `"Tạo nhân viên"` chỉ hiện với `super_admin` gọi `POST /api/v1/admin/staff` (không có ô mật khẩu, báo đã gửi email đặt mật khẩu — F14); khoá/mở khoá chỉ hiện theo thứ bậc F10; ngăn kéo/tab `"Lịch sử hoạt động"` gọi `GET /api/v1/admin/users/{id}/activities` hiển thị log đăng nhập/hồ sơ/trạng thái + `cod_rejected_count` + `recent_orders` (F13).
   - `/admin/gian-hang`: thêm nút `"Yêu cầu khoá gian hàng"` (`POST /admin/shops/{id}/suspend`) cho shop `ACTIVE` và `"Mở khoá"` (`POST /admin/shops/{id}/unsuspend`) cho shop `SUSPENDED` theo đúng capability.
   - Hàng đợi `"Chờ duyệt 2 người"` (tab riêng hoặc mục trong `/admin/cau-hinh`/`/admin/doi-soat`): gọi `GET /api/v1/admin/approvals`, duyệt/từ chối khoá shop & hoàn tiền lớn; vô hiệu hoá nút duyệt khi `requested_by == currentUser.id` (maker ≠ checker). Cập nhật `/admin/doi-soat` hiển thị `PENDING_APPROVAL`/`REJECTED`/`MANUAL_REVIEW`. Gỡ dữ liệu mẫu mà A7 thay thế (F15).
3. **E2E web**: viết `tests/e2e-web/11_w3b2_zalopay_config.test.mjs` (A6) và `tests/e2e-web/12_w3b2_admin_governance.test.mjs` (A7) theo đúng kịch bản a6 §6 và a7 §6 (manager chạy `make e2e-web` sau).

## Tiêu chí nghiệm thu & Kiểm tra
- Không còn `"Ví NexusPay"` hay state giả tab `thanh_toan`; mở trực tiếp trang không đủ quyền bằng `moderator` không phát request 403 nào; `pay_url` host lạ bị chặn render link.
- Kiểm trong `web/`: `npm run lint`, `npm run format:check`, `npm run build` (và unit test nếu có, gồm test validate `pay_url` allowlist và capability helper UI).
