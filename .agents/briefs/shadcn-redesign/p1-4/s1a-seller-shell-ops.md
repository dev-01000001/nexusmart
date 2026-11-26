# S1a — Người bán: shell + vận hành (phase 1, đợt 1)

Đọc trước: `common.md` (cùng thư mục), spec §8.1, §8.2, §9 A1/A2/A4/A6/A9, §10 (các dòng của trang dưới), §13, Figma `02`–`06`.
Mẫu mã: `web/src/app/(design)/design/app/DesignAppClient.tsx` (A1, A2, A4, A6, A9 và cách dựng `AppShell` + `mobileNav`).

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(seller)/layout.tsx`, `layout.module.css` (xoá khi hết người dùng), `template.tsx`
- `web/src/app/(seller)/seller/page.tsx`, `page.module.css`
- `web/src/app/(seller)/seller/don-hang/**`, `tra-hang/**`, `chat/**`, `tuong-tac/**`
- `web/src/features/seller/`:
  - `SellerShell.tsx`, `SellerStateGuard.tsx`, `SellerShopOnboarding.tsx`, `SellerSiteStatus.tsx`;
  - `SellerOverviewClient.tsx`, `SellerTrendChart.tsx` + `.module.css`;
  - `SellerOrdersClient.tsx`, `SellerOrderDetailClient.tsx`, `SellerOrderQuickDrawer.tsx`, `SellerReturnsClient.tsx`.
- File mới chỉ trong thư mục con mới `web/src/features/seller/shell/` và `web/src/features/seller/ops/`.
- Thư mục khác của `(seller)` (`san-pham`, `khuyen-mai`, `tai-chinh`, `bao-cao-ai`, `ho-so`, `site`) là của S1b/S1c: không đụng.

## Bước 0: shell trước tiên (làm xong trong khoảng 45 phút rồi viết tín hiệu)
1. `(seller)/layout.tsx`:
   - `import "@/styles/ui.css"`;
   - render `AppShell` (`@/components/app`) với `area="seller"`, `areaLabel="Người bán"`, `homeHref="/seller"`;
   - giữ `storageKey` lưu trạng thái thu gọn hiện có;
   - thêm `Toaster` (`@/components/ui/sonner`).
2. Nav và command:
   - Dựng `navGroups` / `commandItems` / `account` từ **đúng** dữ liệu `SellerShell.tsx` đang có: nhóm, mục, số đếm,
     "Thao tác nhanh", trạng thái chưa có shop / chờ duyệt, xem site, đăng xuất.
   - Mục được xếp theo sơ đồ §8.1.
   - Đặt cấu hình nav trong `features/seller/shell/`.
3. `mobileNav`:
   - Thanh đáy 5 nút như `SellerShell` hiện có, dựng bằng Tailwind.
   - Mục đang chọn tính bằng `resolveActiveNav` (`@/components/app`) như sidebar. Không viết matcher riêng.
4. Giữ `SellerStateGuard`:
   - chưa có shop → onboarding;
   - shop chờ duyệt / bị khoá → trạng thái tương ứng.
   Logic phiên / quyền không đổi.
5. Tự kiểm:
   - mọi route `/seller/**` (kể cả của S1b/S1c) mở được trong shell mới, menu sáng đúng một mục;
   - 390 / 768 / 1440 không tràn;
   - ⌘K mở được;
   - đăng xuất chạy.
6. Ghi `.runs/shadcn-redesign/p1-4/signals/seller-shell.done`.

## Trang (sau bước 0, theo thứ tự)
| Route | Kiểu | Yêu cầu riêng (§10) |
|---|---|---|
| `/seller` | A9 + A4 | Chưa có shop / chờ duyệt: onboarding (A5, `SellerShopOnboarding`). Shop `ACTIVE`: `SetupGuide` (3 bước bắt buộc + 1 tuỳ chọn, chỉ đánh dấu theo dữ liệu thật; dùng `setupProgress`) + báo cáo theo kỳ (lọc ngày, xuất CSV, biểu đồ xu hướng bằng `components/app/charts`) + việc cần làm + xem nhanh đơn (`Sheet`) |
| `/seller/don-hang` | A1 | 6 nhóm lọc `SellerOrderFilterKey` (tab hoặc chip theo A1), xem nhanh bằng `Sheet`, chọn nhiều + lên đơn lô, in nhãn PDF, trạng thái job vận đơn. `IndexTable` + `StatusBadge` (`statusTone`) |
| `/seller/don-hang/[id]` | A2 | Đúng Figma `03`: `PageHeader` (←, mã đơn, nhãn trạng thái, dòng phụ, nút) + `DetailLayout` 2/3 + 1/3 + `Timeline` |
| `/seller/tra-hang` | A1 + `Sheet` chi tiết | Giữ các bước xử lý trả hàng hiện có |
| `/seller/chat` | A6 | Danh sách hội thoại + khung tin nhắn. Mobile: hai màn chuyển qua lại. Giữ WebSocket / logic gửi |
| `/seller/tuong-tac` | `Tabs` | "Đánh giá" (danh sách tóm tắt đánh giá AI theo sản phẩm, chỉ đọc) + "Thông báo" (danh sách, đánh dấu đã đọc). Không thêm hỏi đáp |

## Xong khi
- Mọi file trong phạm vi đã chuyển; `rg "module.css|styles\." <phạm vi>` rỗng, trừ `SellerCommon` / `SellerUiPrimitives`
  nếu còn dùng (ghi rõ).
- Lệnh kiểm `common.md` §4 xanh trong phạm vi.
- Ảnh 390 / 768 / 1440 cho 6 route + một đơn cụ thể + trạng thái onboarding:
  - Onboarding cần một tài khoản người bán chưa có shop. Tạo bằng đăng ký `QA-S1a-…@example.com`, mã OTP lấy ở
    Mailpit `http://127.0.0.1:48025`. Không tạo được thì ghi lý do.
- Luồng đã đi:
  - lọc đơn theo 6 nhóm;
  - xem nhanh;
  - chọn 2 đơn → lên đơn lô (hoặc tới bước xác nhận nếu dữ liệu không cho);
  - mở chi tiết đơn → chuyển trạng thái hợp lệ;
  - trả hàng: mở `Sheet`;
  - chat: gửi 1 tin;
  - tương tác: đánh dấu đã đọc;
  - ⌘K;
  - thu gọn / mở menu và F5 thấy giữ trạng thái;
  - mobile: ☰ và thanh đáy.
