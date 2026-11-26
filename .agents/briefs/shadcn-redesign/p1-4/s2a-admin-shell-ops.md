# S2a — Quản trị: shell + vận hành (phase 2, đợt 1)

Đọc trước: `common.md`, spec §8.1, §8.2, §9 A1/A2/A4, §10 (các dòng của trang dưới), §13, Figma `02`–`05`.
Mẫu mã: `web/src/app/(design)/design/app/DesignAppClient.tsx`.
**Không đổi** ma trận quyền 9 route và 6 vai trò (`web/src/lib/admin-rbac.ts`, default-deny, chỉ đọc).

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(admin)/layout.tsx`, `layout.module.css` (xoá khi hết người dùng), `admin/template.tsx`
- `web/src/app/(admin)/admin/page.tsx`, `page.module.css`
- `web/src/app/(admin)/admin/gian-hang/**`, `kiem-duyet/**`, `sites/**`, `khieu-nai/**`
- `web/src/features/admin/`:
  - `AdminShell.tsx`, `AdminStateGate.tsx`, `AdminOverviewClient.tsx`, `AdminShopsClient.tsx`;
  - `AdminBannedKeywordsTab.tsx`, `AdminReturnsClient.tsx`;
  - `sites/AdminSitesClient.tsx` + `.module.css`.
- File mới chỉ trong thư mục con mới `web/src/features/admin/shell/` và `web/src/features/admin/ops/`.
- `flash-sale`, `doi-soat`, `cau-hinh`, `audit-log` và các panel cấu hình là của S2b: không đụng.
- Chỉ đọc: `AdminUi.tsx`, `StatusPill.tsx`, `adminShared.module.css`, `AdminUserContext.tsx`, `AdminApprovalsPanel.tsx`.

## Bước 0: shell trước tiên (khoảng 45 phút, rồi viết tín hiệu)
1. `(admin)/layout.tsx`:
   - `import "@/styles/ui.css"`;
   - render `AppShell` với `area="admin"`, `areaLabel="Quản trị"`, `homeHref="/admin"`;
   - giữ `storageKey` thu gọn hiện có;
   - thêm `Toaster`;
   - quản trị **không** có `mobileNav`.
2. `navGroups`, `commandItems`, `account`:
   - lấy đúng mục và nhóm hiện có ở `AdminShell.tsx`, **lọc theo quyền** như hiện tại (vai trò nào thấy mục nào);
   - menu tài khoản có vai trò, về trang chủ NexusMart, đăng xuất;
   - đặt cấu hình nav trong `features/admin/shell/`.
3. Giữ `AdminStateGate` / `AdminUserContext`: chưa đăng nhập, không có quyền, trạng thái lỗi. Logic không đổi.
4. Tự kiểm:
   - mọi route `/admin/**` (kể cả của S2b) mở trong shell mới, menu sáng đúng một mục;
   - < 768 ☰ mở `Sheet`; 390 / 768 / 1440 không tràn; ⌘K chạy.
5. Ghi `.runs/shadcn-redesign/p1-4/signals/admin-shell.done`.

## Trang
| Route | Kiểu | Yêu cầu riêng (§10) |
|---|---|---|
| `/admin` | A4 | `MetricStrip` + `KpiChartCard` / biểu đồ từ `components/app/charts` thay `SvgChart` cũ. Giữ đủ chỉ số và việc cần làm hiện có |
| `/admin/gian-hang` | A1 + `Sheet` chi tiết + `AlertDialog` khi duyệt / từ chối | Phê duyệt 2 người giữ nguyên luồng |
| `/admin/kiem-duyet` | `Tabs` | Hàng đợi duyệt (A1 + `Sheet`) + từ khoá cấm (`AdminBannedKeywordsTab`, A1 + form thêm) |
| `/admin/sites` | A1 + `Sheet` + `AlertDialog` | |
| `/admin/khieu-nai` | A1 + `Sheet` chi tiết | Giữ các bước xử lý khiếu nại / hoàn tiền hiện có |

## Xong khi
- Mọi file trong phạm vi đã chuyển; `rg "module.css|styles\." <phạm vi>` rỗng, trừ phần chỉ đọc dùng chung (ghi rõ còn gì).
- Lệnh kiểm `common.md` §4 xanh trong phạm vi.
- Ảnh 390 / 768 / 1440 cho 5 route, mở thêm một `Sheet` chi tiết và một `AlertDialog`.
- Luồng đã đi bằng tài khoản quản trị:
  - lọc / tìm / phân trang ở gian hàng;
  - mở chi tiết → mở hộp xác nhận duyệt và **huỷ**. Chỉ duyệt thật trên đối tượng `QA-S2a-*` nếu tạo được;
  - kiểm duyệt: chuyển tab, thêm rồi xoá một từ khoá cấm `qa-s2a-…`;
  - sites: mở chi tiết;
  - khiếu nại: mở chi tiết;
  - ⌘K;
  - thu gọn / mở menu, F5 vẫn giữ.
- Nếu tạo / dùng được tài khoản vai trò hẹp hơn (ví dụ chỉ hỗ trợ), chụp menu đã lọc theo quyền. Không được thì kiểm bằng
  đọc mã và ghi lại.
