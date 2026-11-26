# S2b — Quản trị: cấu hình + tiền (phase 2, đợt 2)

Đọc trước: `common.md`, spec §9 A1/A3/A5, §10 (`flash-sale`, `doi-soat`, `cau-hinh`, `audit-log`), §13.
Mẫu mã: `DesignAppClient.tsx`.
Shell do S2a làm: chỉ chụp ảnh nghiệm thu khi đã có `signals/admin-shell.done`.
**Không đổi** ma trận quyền (`src/lib/admin-rbac.ts`) hay luồng phê duyệt 2 người.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(admin)/admin/flash-sale/**`, `doi-soat/**`, `cau-hinh/**`, `audit-log/**`
- `web/src/features/admin/`:
  - `AdminFlashSaleClient` (nằm trong `app/(admin)/admin/flash-sale/`), `AdminReconciliationClient.tsx`;
  - `AdminConfigClient.tsx`, `AdminApprovalsPanel.tsx`, `AdminCarriersPanel.tsx`, `AdminCategoriesPanel.tsx`;
  - `AdminFeesPanel.tsx`, `AdminPaymentMethodsPanel.tsx`, `AdminUsersPanel.tsx`, `AdminSearchReindexBlock.tsx`,
    `AdminAuditLogClient.tsx`.
- File mới chỉ trong thư mục con mới `web/src/features/admin/config/` và `web/src/features/admin/finance/`.
- **Chỉ đọc:**
  - `AdminUi.tsx`, `StatusPill.tsx`, `adminShared.module.css`, `AdminUserContext.tsx`;
  - mọi `*Helpers.ts`, `*Validators.ts`, `bannedKeywordsHelper.ts`, `codReconciliationHelper.ts`, `adminMeta.ts`,
    `adminServer.ts`, `adminApiTypes.ts`, `adminComplaintsTypes.ts`, `types.ts`.

## Trang
| Route | Kiểu | Yêu cầu riêng (§10) |
|---|---|---|
| `/admin/flash-sale` | A1 + A3 | Danh sách + form tạo / sửa (A3, `SaveBar`, lỗi dưới ô) |
| `/admin/doi-soat` | A1 trong `Tabs` + `Dialog` tải CSV | Đối soát VietQR / COD: giữ mọi bước, cột, thao tác đánh dấu, tải CSV |
| `/admin/cau-hinh` | `Tabs`, 7 tab | Cổng thanh toán, Phê duyệt 2 người, Biểu phí, Hãng vận chuyển, Danh mục, Người dùng (A1 + `Sheet`), Voucher sàn & Xu (**chỉ đọc**), cùng khối `AdminSearchReindexBlock`. Tab danh sách theo A1, tab form theo A5. Giữ ẩn / hiện tab theo quyền như hiện tại |
| `/admin/audit-log` | A1 chỉ đọc | Giữ đủ bộ lọc, phân trang |

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng, trừ phần chỉ đọc dùng chung (ghi rõ).
- Lệnh kiểm `common.md` §4 xanh trong phạm vi; test helper / validator không đổi.
- Ảnh 390 / 768 / 1440 cho 4 route và đủ 7 tab cấu hình, kèm `Dialog` CSV, `Sheet` người dùng, form flash sale có lỗi.
- Luồng đã đi bằng tài khoản quản trị:
  - flash sale: mở form tạo, nhập sai để thấy lỗi, huỷ (chỉ tạo thật với tên `QA-S2b-…` và xoá / tắt lại nếu được);
  - đối soát: đổi tab, mở `Dialog` tải CSV (không tải dữ liệu thật);
  - cấu hình: đi qua 7 tab; ở tab Danh mục thêm rồi xoá `QA-S2b-…` nếu có nút xoá; mở `Sheet` người dùng;
  - audit log: lọc + phân trang.
