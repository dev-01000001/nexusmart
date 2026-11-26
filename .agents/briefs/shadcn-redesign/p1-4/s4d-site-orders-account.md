# S4d — Site gian hàng: đơn hàng + tài khoản + đăng nhập site + widget (phase 4, đợt 2)

Đọc trước: `common.md`, spec §8.3 (phần "Site gian hàng"), §9 A2, §11.1, §11.3 (Đơn hàng / chi tiết / tra cứu, Tài
khoản, Đăng nhập), §12, §13.
Shell site do S4a làm: chỉ chụp ảnh nghiệm thu khi đã có `signals/site-shell.done`.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(site)/s/[host]/don-hang/**`, `tra-cuu-don-hang/**`, `tra-cuu-don/**`, `tai-khoan/**`, `dang-nhap/**`,
  `dang-ky/**`, `quen-mat-khau/**`, `dat-lai-mat-khau/**`
- `web/src/features/account/`: `AccountClientView.tsx` + `.module.css`, `AccountAddressForm.tsx`,
  `OrderDetailClientView.tsx` + `.module.css`, `OrdersClientView.tsx` + `.module.css`.
- `web/src/features/commerce/OrderLookupView.tsx` + `.module.css`.
- `web/src/features/chat/SiteChatWidget.tsx` + `.module.css`, `features/chat/index.ts`.
- `web/src/features/assistant/SiteAssistantWidget.tsx` + `.module.css`, `features/assistant/index.ts`.
- File mới chỉ trong thư mục con mới `web/src/features/account/ui/`.
- **Chỉ đọc:**
  - `AuthForms.tsx` + `AuthForms.module.css` (S3; dùng `LoginForm` / `RegisterForm` / … như hiện tại, không sửa);
  - `orderMeta.ts`, `orderView.ts`, `orderReturnSync.ts`, `accountTypes.ts`, `authRedirect.ts`, `*.server.ts`;
  - `chatClient.ts`, `sseParser.ts`, `assistantMarkdown.ts`;
  - `features/commerce/**` khác (S4c), `context/**`.

## Export dùng chéo (giữ nguyên)
- `SiteChatWidget`, `SiteAssistantWidget`: tên, props. `SiteSupportDock` của S4a mở chúng.

## Việc
- **`/don-hang`:**
  - danh sách thẻ đơn (mã, ngày, `StatusBadge`, tổng), lọc theo nhóm trạng thái đang có;
  - phân trang / trống / lỗi theo A1.
- **`/don-hang/[id]`:**
  - A2 rút gọn: sản phẩm, tóm tắt tiền, địa chỉ (đổi địa chỉ nếu đang có), vận chuyển, `Timeline`;
  - thanh toán lại dùng `RetryPaymentBox` (S4c);
  - **đổi trả 10 bước** dùng `Sheet` + chỉ báo bước; giữ đủ bước, ảnh, lý do;
  - giữ giao lại / hoàn tiền như hiện có.
- **Tra cứu đơn** (`tra-cuu-don-hang`, `tra-cuu-don` redirect): form + kết quả theo cùng kiểu thẻ.
- **`/tai-khoan`:**
  - `Tabs` giữ đúng 4 tab và query `?tab=`: "Hồ sơ" (gồm xác minh SĐT OTP với neo `#xac-minh-sdt` mà trang thanh toán trỏ
    tới, và cài đặt thông báo), "Sổ địa chỉ", "Mã giảm giá", "Yêu thích";
  - mỗi tab theo A5; không thêm tab "Bảo mật".
- **4 trang auth của site:**
  - bọc bằng `AuthSplit` **trong** `SiteShell` (§8.3), dạng thẻ trong vùng nội dung, nhận màu site;
  - cột ảnh dùng `banner_url` của mẫu, logo / tên là của shop;
  - form vẫn là `AuthForms` của S3;
  - bỏ import `AuthForms.module.css` ở trang site.
- **Widget chat và trợ lý AI:**
  - giao diện bằng Tailwind + `Sheet` / thẻ nổi, nhận màu site;
  - giữ logic WebSocket / SSE / markdown, giới hạn và thông báo lỗi.

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng, trừ phần chỉ đọc dùng chung.
- Lệnh kiểm `common.md` §4 xanh trong phạm vi; test `orderView` / `orderReturnSync` / `accountTypes` / `authRedirect`
  không đổi.
- Ảnh 390 / 768 / 1440 trên 4 site seed (đăng nhập người mua trừ trang auth):
  - `/don-hang`, một đơn có `Timeline`, `Sheet` đổi trả;
  - `/tra-cuu-don-hang` (form + kết quả);
  - `/tai-khoan` 4 tab;
  - 4 trang auth;
  - widget chat và trợ lý đang mở; một mẫu tối.
- Luồng đã đi:
  - mở đơn → mở `Sheet` đổi trả, đi tới bước cuối rồi huỷ (chỉ gửi thật trên đơn tạo bởi luồng QA nếu có);
  - tra cứu đơn đúng / sai;
  - tài khoản đổi 4 tab qua URL `?tab=`;
  - thêm rồi xoá một địa chỉ `QA-S4d`;
  - mở neo `#xac-minh-sdt`;
  - đăng nhập / đăng xuất trên site, quay về đúng trang `next`;
  - gửi 1 tin chat;
  - hỏi trợ lý 1 câu.
