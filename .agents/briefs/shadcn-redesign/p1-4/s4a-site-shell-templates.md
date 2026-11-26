# S4a — Site gian hàng: shell + mẫu + trang chủ (phase 4, đợt 1)

Đọc trước: `common.md`, spec §5.3 (font), §11 (cả 11.1–11.3), §12, §13, §15 mục 6–7.
Mẫu mã: `web/src/app/(design)/design/site/DesignSiteClient.tsx` (khung site: `UiScope` + `buildSiteUiVars` +
`data-site-theme-mode`). `web/src/lib/site/theme.ts` đã có `buildSiteUiVars`, `resolveSiteUiFontStack`, `siteRadiusToCss`.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(site)/layout.tsx`, `error.tsx`, `error.module.css`
- `web/src/app/(site)/s/[host]/layout.tsx`, `page.tsx`, `template.tsx`, `not-found.tsx`, `gioi-thieu/**`, `chinh-sach/**`
- `web/src/features/site/`:
  - `SiteShell.tsx`, `SiteChrome.tsx`, `SiteRenderer.tsx`, `DenseGridHeroSearch.tsx`, `OnePageLaunchHero.tsx`;
  - `SafeActionLink.tsx`, `SiteInfoView.tsx`, `SiteSupportDock.tsx` + `.module.css`;
  - `index.ts` (barrel; S4a là chủ duy nhất).
- `web/src/lib/site/theme.ts`: **chỉ** đổi giá trị `SITE_FONT_LABELS` và `resolveSiteFontStack` sang 5 font thật (§5.3,
  §14 phase 4). Không đổi tên / chữ ký export nào.
- `web/src/lib/site/site.test.mjs`: chỉ sửa assertion font (đang khẳng định luôn là "Archivo").
- File mới chỉ trong thư mục con mới `web/src/features/site/shell/` và `web/src/features/site/sections/`.
- **Không đụng:**
  - `SitePdpView`, `SiteCatalogView`, `SiteSearchView`, `SiteProductCard`, `MultiSkuDirectory`, `CollapsibleProse` (S4b);
  - `features/search/**` (S4b);
  - widget chat / trợ lý (S4d); `features/commerce/**`, `features/account/**`, `context/**`;
  - các route site khác.
- **Chỉ đọc:** `site.module.css`, `siteTemplates.module.css` (dùng chung, không xoá), `templateCatalog.ts`,
  `templateValidator.ts`, `templateTypes.ts`, `siteMetrics.ts`, `siteNav.ts`.

## Bước 0: shell trước tiên (khoảng 60 phút, rồi viết tín hiệu)
1. `s/[host]/layout.tsx`:
   - `import "@/styles/ui.css"`;
   - `Toaster` nạp bằng `next/dynamic` (§15.7).
2. `SiteShell`:
   - phần tử gốc thành `UiScope`, nhận thêm `buildSiteUiVars(theme)` cạnh các biến `--site-*` đang có;
   - mở rộng tham số theme theo §11.1;
   - **giữ** `data-site-root` và `data-site-theme-mode="dark|light"` (E2E kiểm hai thuộc tính này);
   - giữ cô lập theo host, SEO, `StoreContext`.
3. Tự kiểm:
   - mọi route site của 4 site seed vẫn mở được (200, không lỗi console / hydration);
   - biến `--ui-*` có trên phần tử gốc, mẫu tối có `data-site-theme-mode="dark"`.
4. Ghi `.runs/shadcn-redesign/p1-4/signals/site-shell.done`.

## Việc (sau bước 0)
- **Đầu trang (§11.3)**, theo `header_style`:
  - icon giỏ có số lượng;
  - mobile: ☰ mở `Sheet` trái chứa nav + tài khoản, nạp bằng `next/dynamic`;
  - ô tìm vẫn render `SiteSearchBar` (S4b) như hiện tại, với cùng props.
- **Cuối trang** theo `footer_style`.
- **Trang chủ:** `SiteRenderer` dựng mọi loại section và biến thể bằng Tailwind + primitive.
  - **Giữ đúng khác biệt cấu trúc** giữa 8 archetype mà `templateCatalog.ts` mô tả: `header_style`, `footer_style`,
    `container_width`, `card_style`, `sections`, biến thể khối.
  - `density` chỉ đổi khoảng cách section / container (§11.1).
  - Site phẳng, không bóng tĩnh; nút primary dùng màu `accent` của mẫu.
  - Thẻ sản phẩm: dùng `SiteProductCard` của S4b, **giữ props đang truyền**.
- **Giới thiệu, chính sách** (`SiteInfoView`): theo `info_layout`, chữ khổ 720px.
- **Lỗi và 404** của site: trong `SiteShell`, dùng `EmptyState` / `ErrorState`.
- **`SiteSupportDock`:** chuyển giao diện, giữ cách mở widget chat / trợ lý (widget là của S4d).
- **Font (§5.3):** `SITE_FONT_LABELS` / `resolveSiteFontStack` dùng 5 font thật đã nạp ở `src/lib/fonts.ts`. Mỗi mẫu
  hiện đúng font của mình.

## Xem đủ 8 mẫu mà không đổi site đã xuất bản
- 4 site seed (`bach-dang`, `sai-gon`, `da-nang`, `ha-noi`): dữ liệu thật. **Không đổi mẫu đã xuất bản.**
- Thư viện mẫu trong Site Studio (`/seller/site`, đăng nhập người bán): khung xem trước dùng `SiteRenderer` thật với dữ
  liệu mẫu (`previewData.ts`), không lưu gì. Chụp 8 mẫu ở đây.
- Chỉ khi cần, mới đổi **bản nháp** (không xuất bản) của shop người bán để xem `/seller/site/xem-truoc`. Phải ghi lại bản
  nháp gốc trước và trả về đúng như cũ khi xong.

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng, trừ file chỉ đọc dùng chung (ghi rõ còn gì).
- Lệnh kiểm `common.md` §4 xanh trong phạm vi, kể cả `site.test.mjs`.
- Ảnh 390 / 768 / 1440:
  - trang chủ 4 site seed, giới thiệu, chính sách, 404;
  - 8 mẫu trong thư viện / xem trước, gồm 2 mẫu tối;
  - menu ☰ mở trên mobile.
- `report.txt` 0 lỗi console / hydration.
- Luồng đã đi:
  - điều hướng đầu / cuối trang;
  - ☰ mobile mở / đóng bằng Esc;
  - icon giỏ hiện số khi giỏ có hàng (thêm hàng bằng tài khoản người mua trên trang sản phẩm cũ);
  - mở widget hỗ trợ;
  - trang lỗi.
- Ghi số First Load JS bạn thấy trong log dev nếu có. Claude đo chính thức trên bản build (ngân sách +15% so mốc
  phase 0: trang chủ site 257 kB, trang sản phẩm 257 kB).
