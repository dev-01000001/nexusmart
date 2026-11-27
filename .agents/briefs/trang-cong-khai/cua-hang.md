# Trang danh bạ cửa hàng `/cua-hang` theo trang chủ

Chủ dự án (2026-11-27): "làm tiếp trang danh bạ cửa hàng theo trang chủ". Tiếp nối đợt trước (brief `brief.md` cùng thư mục,
commit `844181a0`): header/footer, `/ve-nexusmart`, `/tro-giup`, auth, 404 đã theo ngôn ngữ hình trang chủ, dùng chung
`web/src/features/platform/platformStyles.ts` và `platformFont.ts`. Đọc lại `brief.md` mục 2–6: luật, server, công cụ chụp/so, báo cáo
áp dụng y nguyên, chỉ khác phạm vi dưới đây. Thư mục chạy: `.runs/trang-cong-khai/cua-hang/`.

Hiện **không có trang danh bạ riêng**: danh bạ chỉ là mục S2 của trang chủ (`SiteDirectorySection.tsx`, neo `#danh-ba-gian-hang`,
24 site/trang qua `?trang=`, dữ liệu `loadSiteDirectory` → `GET /api/v1/sites`, backend chỉ có `page`/`page_size`). Header "Cửa hàng",
footer "Các cửa hàng", nút "Xem danh bạ" ở `/ve-nexusmart`, liên kết "Danh bạ gian hàng" và `buyerHref` ở khung auth đều trỏ
`/#danh-ba-gian-hang`.

## Việc
1. Trang mới `web/src/app/(shop)/(public)/cua-hang/page.tsx` (server component, `metadata` title "Cửa hàng"):
   - đầu trang như các trang đã làm (heading lớn kiểu trang chủ, một câu dẫn, số gian hàng đang mở);
   - lưới **toàn bộ** site đã xuất bản, dùng lại thẻ site của `SiteDirectorySection` (ảnh, logo, tên, tên miền, mũi tên), phân trang
     `?trang=` (`parseDirectoryPage`, `loadSiteDirectory`), nút trang trỏ `/cua-hang?trang=N`;
   - trạng thái rỗng (0 site: lời mời mở gian hàng) và lỗi gateway (`DirectoryErrorState` có sẵn) giống trang chủ;
   - kết bằng khối CTA "Mở gian hàng" như `/ve-nexusmart`.
2. Dùng lại, đừng chép: tách thẻ site / lưới / phân trang của `SiteDirectorySection.tsx` thành phần dùng chung (ví dụ tham số
   `pageHref`), để trang chủ và `/cua-hang` cùng dùng. **Trang chủ phải giữ y hệt từng pixel** (kể cả link phân trang của trang chủ vẫn
   là `/?trang=N#danh-ba-gian-hang`).
3. Trỏ về `/cua-hang`: header "Cửa hàng" (có trạng thái đang chọn khi ở `/cua-hang`), footer "Các cửa hàng", "Xem danh bạ" ở
   `/ve-nexusmart` (và `redirect` khi `?tab=stores`), "Danh bạ gian hàng" + `buyerHref` ở `features/platform/auth/*` và
   `app/(shop)/dang-nhap/page.tsx`, menu người dùng trong `PlatformHeader` ("Các cửa hàng" nếu có). Kiểm `buyerHref` mới vẫn đi qua
   `resolvePostLoginPath`/`sanitizeNextPath` đúng. **Giữ** nút "Xem các cửa hàng" trên hero trang chủ cuộn trong trang (`#danh-ba-gian-hang`).
4. Thêm `/cua-hang` và `/cua-hang?trang=2` vào `tests/ui-flows/routes/platform.txt`. Rg `danh-ba-gian-hang` trong `tests/` và
   `web/src/**/*.test.mjs`: test nào bám href cũ của các link ở mục 3 thì viết lại theo href mới, giữ bất biến.

## Phạm vi file
Được sửa: `web/src/app/(shop)/(public)/cua-hang/**` (mới), `web/src/features/platform/{SiteDirectorySection,SiteDirectoryDoors,
PlatformHeader,PlatformFooter,platformStyles}.tsx|ts`, file mới trong `web/src/features/platform/`, `web/src/features/platform/auth/*`,
`web/src/app/(shop)/dang-nhap/page.tsx`, `web/src/app/(shop)/(public)/ve-nexusmart/page.tsx` (chỉ href/redirect),
`tests/ui-flows/routes/platform.txt`, test bám href. Cấm như `brief.md` mục 1; không sửa `PlatformHome.tsx` ngoài dòng import/dùng
phần tách ra.

## Nghiệm thu
- `/cua-hang` ở 390/768/1440 cùng hệ với trang chủ, không tràn ngang, 0 lỗi console/hydrate, có thẻ đủ ảnh; `?trang=2` (nếu dữ liệu
  có ≤ 24 site thì kiểm `?trang=2` vẫn hiện trang hợp lệ hoặc về trang cuối như trang chủ đang làm); thử lỗi gateway nếu làm được
  không cần tắt stack (ví dụ unit test `normalizeDirectoryResponse`), không đụng docker.
- `/` so 43000 bằng `scroll-shots`: SAME (hoặc (a)/(b)).
- Gate `brief.md` mục 3.5 xanh toàn app; `node tests/ui-flows/run.mjs --port 43110 --area platform` xanh.
- Báo cáo như `brief.md` mục 6, thêm danh sách mọi href đã đổi (file:dòng cũ → mới).
