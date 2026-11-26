# S3 — Nền tảng + đăng nhập (phase 3, đợt 1: chưa làm cây trang chủ)

Đọc trước: `common.md`, spec §8.3, §8.4, §10 (các dòng `/`, `/ve-nexusmart`, `/tro-giup`, auth), §12, §13, §16, Figma `01`.
Mẫu mã: `web/src/app/(design)/design/auth/` (`AuthSplit` 2 biến thể).

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(shop)/**`:
  - gồm `layout.tsx`, `layout.module.css`, `template.tsx`, 4 trang auth, `tro-giup/**`, `ve-nexusmart/**`, `page.tsx`;
  - `gian-hang/[slug]/route.ts` và `san-pham/[slug]/route.ts` là logic, **chỉ được dời** khi tách route group, nội dung giữ nguyên.
- `web/src/features/platform/`: `PlatformHeader.*`, `PlatformFooter.*`, `PlatformAuthShell.*`, `NotFoundView.*`,
  `platform.test.mjs` (xem `common.md` về test cạnh mã).
- `web/src/features/account/AuthForms.tsx`.
  - `AuthForms.module.css` **không được xoá**: trang auth của site (S4d) còn import nó.
- File mới chỉ trong `web/src/app/(shop)/**` và thư mục con mới `web/src/features/platform/auth/`.
- **Chỉ đọc ở đợt này** (cây trang chủ, chuyển ở đợt sau): `PlatformHome.*`, `PlatformGlobe.*`, `globeData.ts`,
  `globeRenderer.ts`, `SiteDirectory*`, `StorefrontStageMini.*`, `SubdomainPreview.*`, `showcaseTemplates.ts`,
  `VietQrSvg.tsx`, `directory*`, `legacy*`, `subdomain.ts`.

## Ràng buộc riêng
- **Trang chủ `/` phải giữ y hình.**
  - Header bản trang chủ (nhánh `data-home="true"` của `PlatformHeader`) chuyển sang Tailwind nhưng không được đổi hình.
  - Kiểm bằng ảnh viewport (`--viewport`) của `/` ở 390 / 768 / 1440 trên cổng 43110 so với 43101 (bản cũ đóng băng),
    hai vị trí: đầu trang và sau khi cuộn tới footer.
  - So bằng `ffmpeg -i a.png -i b.png -filter_complex "blend=all_mode=difference,format=gray,lut=y='if(gt(val,24),255,0)',signalstats,metadata=print:key=lavfi.signalstats.YAVG:file=-" -f null -`
    phải ra `YAVG=0`. Nếu không bằng 0, xem vùng khác: chỉ chấp nhận khi đó là dữ liệu đổi, và phải ghi rõ.
  - Không dùng ảnh fullPage để so: viewport bị kéo cao làm CSS theo viewport đổi.
- **Dọn ở `(shop)/layout.*`:**
  - bỏ khai Inter riêng (layout gốc đã gắn `--font-inter` lên `<html>`);
  - bỏ dòng `--home-font` trong `.shell` nếu chuyển xong mà trang chủ vẫn y hình.
- **Route group** (§8.3, §16):
  - tách để `/`, `/ve-nexusmart`, `/tro-giup` và 2 redirect 308 có header / footer;
  - 4 trang auth đứng riêng trong `AuthSplit` toàn màn, không header / footer;
  - URL không đổi, redirect 308 vẫn chạy (kiểm bằng `curl -sI`);
  - layout của khu vực `import "@/styles/ui.css"`.
- **`AuthForms.tsx`:**
  - chuyển sang Tailwind + `components/ui`;
  - **giữ nguyên tên export và props** của `LoginForm`, `RegisterForm`, `ForgotPasswordForm`, `ResetPasswordForm`. Site
    gian hàng dùng chung.
  - Giữ đủ:
    - field, bước OTP 6 ô, lỗi, chuyển hướng `next`;
    - nút chuyển vai trò "Người bán / Khách mua" (`ToggleGroup`);
    - nhãn gợi ý tên miền `?role=seller&ten=…` (`Badge`).
  - Thêm thanh độ mạnh mật khẩu, chỉ phía client, không chặn gửi (§8.3).
- `PlatformAuthShell` được `AuthSplit` thay; xoá file khi `rg` không còn ai import.
- **`NotFoundView`:**
  - được `src/app/not-found.tsx` gốc dùng, nằm ngoài mọi layout khu vực;
  - nên **tự** `import "@/styles/ui.css"` và tự bọc `UiScope`.
- `/tro-giup`: khổ 720px, câu hỏi thường gặp bằng `Accordion`.
- `/ve-nexusmart`: giữ bố cục 4 màn ảnh toàn chiều rộng, chỉ đổi công nghệ và font.
- Font: phần ứng dụng dùng Inter (spec §17.2).

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng, trừ cây trang chủ (chưa làm) và `AuthForms.module.css` (giữ cho site).
- Lệnh kiểm `common.md` §4 xanh trong phạm vi.
- Ảnh 390 / 768 / 1440:
  - `/ve-nexusmart`, `/tro-giup`, `/dang-nhap`, `/dang-ky`, `/quen-mat-khau`, `/dat-lai-mat-khau?token=x`;
  - `/dang-ky?role=seller&ten=qa-shop`;
  - một đường dẫn 404 ở host gốc.
- Trang chủ so viewport = 0 khác, cả 3 khổ, cả 2 vị trí.
- Luồng đã đi:
  - đăng nhập người bán và quản trị: đúng chuyển hướng `next`;
  - đăng nhập sai mật khẩu: hiện lỗi;
  - đăng ký tới bước OTP bằng email `QA-S3-…@example.com`, mã ở Mailpit `http://127.0.0.1:48025`;
  - quên mật khẩu: gửi được;
  - nút chuyển vai trò;
  - 2 redirect 308.
- Sau khi có `signals/site-shell.done`: mở `/dang-nhap` của một site để chắc `LoginForm` vẫn hiện được ở đó. Phần khung
  site do S4d làm, chỉ ghi nhận xét.
