# Làm lại UI các trang công khai của host gốc theo trang chủ

Chủ dự án (2026-11-27): "Sửa lại toàn bộ UI các màn khác trên trang chủ và các trang bên ngoài, lấy trang chủ làm chuẩn."

Trang chủ `/` (`web/src/features/platform/PlatformHome.tsx`) đã có ngôn ngữ hình riêng: nền tối gần đen và các dải teal/navy rất
tối, chữ trắng, chữ phụ `--home-ink-2`, accent teal `--home-accent`, nút bo tròn hình viên thuốc (nút chính nền trắng chữ tối, nút
phụ viền trắng), heading rất lớn Inter có `opsz`, dải nội dung bo góc lớn (`--home-radius-band`), thẻ tối có viền mảnh
(`--home-card`, `--home-border`), ảnh thật bo góc. Token nằm ở `web/src/features/platform/home/home-tokens.css`. Các trang còn lại
của host gốc vẫn là giao diện sáng kiểu ứng dụng, lệch hẳn trang chủ. Việc của bạn: đưa chúng về cùng ngôn ngữ hình với trang chủ.

Đọc trước: `web/CLAUDE.md`, skill `design-system`, skill `frontend-design`, `AGENTS.md`. Xem trang chủ trên trình duyệt (mục 4)
từ đầu đến cuối trước khi sửa, kể cả phần FAQ gần cuối trang chủ (dùng làm mẫu cho Trợ giúp).

## 1. Phạm vi (chỉ sửa các file này)

| Route / phần | File |
|---|---|
| Header khi **không** ở `/` (ở `/` phải giữ nguyên y hệt) | `web/src/features/platform/PlatformHeader.tsx` |
| Footer | `web/src/features/platform/PlatformFooter.tsx` |
| Layout công khai | `web/src/app/(shop)/(public)/layout.tsx`, `web/src/app/(shop)/layout.tsx`, `web/src/app/(shop)/template.tsx` |
| `/ve-nexusmart` | `web/src/app/(shop)/(public)/ve-nexusmart/page.tsx` |
| `/tro-giup` | `web/src/app/(shop)/(public)/tro-giup/page.tsx`, `.../tro-giup/layout.tsx` |
| `/dang-nhap`, `/dang-ky`, `/quen-mat-khau`, `/dat-lai-mat-khau` | `web/src/features/platform/auth/*`, `web/src/app/(shop)/{dang-nhap,dang-ky,quen-mat-khau,dat-lai-mat-khau}/page.tsx` |
| 404 host gốc | `web/src/features/platform/NotFoundView.tsx`, `web/src/app/not-found.tsx` |
| Token dùng chung | `web/src/features/platform/home/home-tokens.css` (chỉ **thêm** token; không đổi giá trị token cũ vì `/` đang dùng) |
| Test bám mã cũ của các file trên | `web/scripts/*.test.mjs`, `web/src/features/platform/platform.test.mjs` (xem mục 3) |
| Lỗi form (mục 2) | `web/src/features/account/AuthForms.tsx`, `web/src/features/admin/AdminCarriersPanel.tsx` |

Được tạo component mới trong `web/src/features/platform/` nếu ít nhất 2 trang dùng chung (ví dụ nút viên thuốc, dải tối). Phần
trang chủ đang tự viết mà bạn cần dùng lại: tách ra file mới rồi cho `PlatformHome.tsx` import lại **chỉ khi** hình `/` không đổi
một pixel; không chắc thì chép lớp Tailwind sang, đừng sửa `PlatformHome.tsx`.

**Cấm:** sửa `web/src/app/(site)`, `(seller)`, `(admin)`, `web/src/components/ui/*`, `web/src/styles/ui.css`, BFF `web/src/app/api`,
mọi thư mục ngoài `web/`, `web/tsconfig.json`, `web/next-env.d.ts`. Không thêm phụ thuộc npm. Không git ghi (add/commit/stash/checkout).

## 2. Lỗi form lộ mật khẩu (làm trước)

Bấm "Tạo tài khoản" trước khi trang hydrate thì form gửi GET mặc định, email và mật khẩu lên URL (`/dang-ky?...&password=...`).
Working tree đã có sẵn bản sửa (Claude lỡ tự viết, chưa ai kiểm): thêm `method="post"` cho 5 form trong `AuthForms.tsx` và form có
ô mật khẩu trong `AdminCarriersPanel.tsx` (`git diff -- web/src/features`). Việc của bạn:
- rà diff đó (chỉ thêm thuộc tính + định dạng prettier, không đổi gì khác); tìm thêm mọi form trong `web/src` có ô
  `type="password"`, mã OTP hoặc token mà chưa có `method="post"`, thêm vào;
- kiểm thật: chặn JS (Playwright `javaScriptEnabled: false`) rồi submit `/dang-ky` và `/dang-nhap` trên cổng 43110 của host gốc
  **và** của site `bach-dang` (AuthForms dùng chung), URL sau submit không được chứa `password`, `email`, `otp`; ghi kết quả.

`AuthForms.tsx` dùng chung với site gian hàng: ngoài `method="post"` **không đổi gì** trong file đó. Muốn đổi hình form trên host
gốc thì làm ở khung bao (`features/platform/auth/*`), ví dụ đặt `data-site-theme-mode="dark"` cho vùng form để shadcn dùng token
tối có sẵn trong `ui.css` (custom-variant `dark` đọc thuộc tính này), rồi chỉnh màu viền/nút bằng biến trong vùng đó.

## 3. Tiêu chí nghiệm thu

1. Cả 8 route ở bảng trên (header/footer trên mọi route trừ `/`, 404 thử bằng `/khong-ton-tai`, `/dat-lai-mat-khau` thử cả không
   token và `?token=abc`) nhìn cùng một hệ với trang chủ: nền tối, chữ trắng, nút viên thuốc, heading lớn, cùng header và footer.
   - `/ve-nexusmart`: làm lại bố cục cho ra một trang câu chuyện thương hiệu có nhịp như trang chủ (hero ảnh tràn hoặc dải tối,
     các khối chữ + ảnh xen kẽ, kết bằng khối CTA). Hiện có khối tối trống to và chữ bị đẩy xuống góc: phải hết.
   - `/tro-giup`: giữ ô tìm, chip lọc chủ đề, accordion, khối liên hệ; nhìn như phần FAQ của trang chủ.
   - Auth: giữ chọn vai trò, gợi ý tên miền phụ, thanh độ mạnh mật khẩu, ảnh theo vai trò, mọi liên kết; khung tối theo trang chủ.
2. **Giữ nguyên chữ hiển thị** (được đổi bố cục, không đổi câu chữ, không bỏ nội dung). `tests/e2e-web/*.mjs` và
   `tests/ui-flows/platform.mjs` bám chữ, role, `href`: rg các route trên trong hai chỗ đó trước khi sửa, giữ mọi thứ chúng bám.
3. Không đổi hình: `/` (so cả trang bằng `scroll-shots.mjs`), `/dang-nhap` và `/dang-ky` của site `bach-dang`, `/seller` (seller),
   `/admin` (admin). So 43110 với mốc 43000, chỉ được loại (a)/(b) theo mục 5.
4. 390/768/1440: không tràn ngang, không lỗi console/hydrate (đọc `report.txt` của `ui-shots`), tương phản chữ ≥ 4.5:1 (chữ lớn
   ≥ 3:1), vòng focus thấy rõ trên nền tối, có `prefers-reduced-motion` cho chuyển động mới, ảnh `next/image` có `sizes`.
5. Lệnh kiểm (trong `web/`, bọc `flock` như dưới) xanh trên toàn app:
   ```bash
   flock /tmp/nm-web-ts.lock npx tsc --noEmit -p . 2>&1 | grep -v '^\.next' | grep error   # rỗng
   flock /tmp/nm-web-ts.lock npm run lint          # 0 problem
   npm run format:check                            # sạch
   flock /tmp/nm-web-ts.lock npm run test:unit     # 0 fail
   npm run check:data                              # xanh
   ```
   Test vỡ vì bám class/markup cũ của file trong phạm vi: viết lại để kiểm cùng bất biến trên mã mới, không xoá assertion, không
   `skip`; liệt kê từng test đã sửa và bất biến của nó.
6. `node tests/ui-flows/run.mjs --port 43110 --area platform` xanh (đọc `--help`/đầu file nếu cần tham số).

## 4. Server, tài khoản, công cụ

- Dev working tree (Claude chạy, tự khởi động lại): `http://nexusmart.localhost:43110`, site `http://bach-dang.nexusmart.localhost:43110`.
  Không tự chạy server khác, không kill server này. **Không chạy `next build`**, không `make e2e*`, `make up`, không đụng docker.
- Mốc đóng băng (bản production HEAD `b3b698d5`, cùng dữ liệu): `http://nexusmart.localhost:43000`.
- Tài khoản dev: seller `seller@nexusmart.local` / `SellerDemo123!`, admin `admin@nexusmart.local` / `AdminDevPassword123!`.
  Không đăng ký tài khoản mới (giới hạn tần suất và rác dữ liệu), trừ khi cần để kiểm mục 2, khi đó dùng email `qa-pub-<số>@nexusmart.local`.
- Chụp và so:
  ```bash
  cd /home/tai/Document/nexusmart; R=.runs/trang-cong-khai/agy
  for P in 43000 43110; do
    flock /tmp/nm-web-browser.lock node .agents/tools/ui-shots.mjs --port $P --login none \
      --routes-file tests/ui-flows/routes/platform.txt --widths 390,768,1440 --out $R/platform-$P
  done
  MASK="0:828:84:72;0:ih-72:84:72" .agents/tools/ui-diff.sh $R/platform-43000 $R/platform-43110 $R/platform-diff
  node .agents/tools/scroll-shots.mjs $R/home-43000 43000; node .agents/tools/scroll-shots.mjs $R/home-43110 43110
  MASK="0:828:84:72;0:ih-72:84:72" .agents/tools/ui-diff.sh $R/home-43000 $R/home-43110 $R/home-diff
  ```
  Trang chủ phải `SAME` (hoặc loại (a)/(b)); 7 trang còn lại đương nhiên `DIFF`, **tự mở ảnh 43110 ra xem** từng trang, từng bề
  rộng, so với ảnh trang chủ cùng bề rộng. Site/seller/admin: `--site bach-dang --routes /dang-nhap,/dang-ky`,
  `--login seller --routes /seller`, `--login admin --routes /admin`.
- Playwright: `/home/tai/.npm/_npx/9833c18b2d85bc59/node_modules/playwright/index.mjs`, kênh `chrome`, bọc `flock /tmp/nm-web-browser.lock`.

## 5. Xếp loại diff ở các trang phải giữ nguyên
(a) dữ liệu live đổi giữa hai lần chụp; (b) lệch ≤ 6/255 trên gradient/trong suốt (dev vs production). Còn lại là hồi quy: sửa.

## 6. Tiến độ và báo cáo
- Ghi `.runs/trang-cong-khai/agy/progress.md` sau mỗi trang (xong / đang làm / còn lại). Bị ngắt thì làm tiếp từ đó.
- Báo cáo cuối: (1) file đã đổi theo route; (2) kết quả mục 2 (URL sau submit khi tắt JS, 4 ca); (3) kết quả từng lệnh mục 3.5;
  (4) bảng diff `/`, site auth, seller, admin với loại từng dòng; (5) đường dẫn ảnh 43110 của 8 route × 3 bề rộng; (6) test đã viết
  lại; (7) phần chưa đạt, nói thẳng.
