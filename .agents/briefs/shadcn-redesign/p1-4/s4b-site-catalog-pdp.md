# S4b — Site gian hàng: danh mục + trang sản phẩm + tìm kiếm (phase 4, đợt 2)

Đọc trước: `common.md`, spec §11.1, §11.3 (dòng Đầu trang / ô tìm, Thẻ sản phẩm, Danh mục / tìm kiếm, Chi tiết sản
phẩm, Thêm vào giỏ), §12, §13, §15 mục 6–7.
Mẫu mã: `DesignSiteClient.tsx`.
Shell site do S4a làm: chỉ chụp ảnh nghiệm thu khi đã có `signals/site-shell.done`.

## Phạm vi (chỉ những file này; file khác là cấm)
- `web/src/app/(site)/s/[host]/san-pham/**` (`page.tsx`, `[slug]/page.tsx`), `tim-kiem/**`
- `web/src/features/site/`: `SitePdpView.tsx`, `SiteCatalogView.tsx`, `SiteSearchView.tsx`, `SiteProductCard.tsx`,
  `MultiSkuDirectory.tsx`, `CollapsibleProse.tsx`.
- `web/src/features/search/**`: `SiteSearchBar.*`, `SiteSearchResults.*`, `index.ts` (S4b là chủ barrel này).
- `web/src/features/assistant/ProductReviewSummary.tsx` + `.module.css`.
- File mới chỉ trong thư mục con mới `web/src/features/site/catalog/` và `web/src/features/search/` (cả cây là của S4b).
- **Chỉ đọc:** `site.module.css`, `siteTemplates.module.css`, `templateCatalog.ts`, `templateTypes.ts`, `context/**`,
  `features/commerce/**`.

## Export dùng chéo (giữ nguyên)
- **`SiteProductCard`:** tên, props, kiểu. `SiteRenderer` của S4a dùng nó.
- **`SiteSearchBar`:** tên, props. Đầu trang của S4a render nó.

## Việc
- **Thẻ sản phẩm:**
  - ảnh tỉ lệ cố định, bo theo `radius`;
  - tên 2 dòng; giá dùng `Money` (giá gốc gạch nếu có);
  - nhãn giảm giá / flash sale là `Badge`;
  - `card_style` đổi khung / viền / chữ.
- **Ô tìm (`SiteSearchBar`):** mở `CommandDialog` (nạp bằng `next/dynamic`), gợi ý giữ logic hiện có.
- **Danh mục / tìm kiếm**, theo `catalog_layout`:
  - bộ lọc là cột trái từ 1024 trở lên; trên mobile là `Sheet`, thay drawer tự viết; nạp bằng `next/dynamic`;
  - sắp xếp bằng `Select`; phân trang bằng `Pagination`;
  - trống / lỗi / đang tải theo A1;
  - giữ query string hiện có.
- **Chi tiết sản phẩm**, theo `pdp_layout`:
  - biến thể là chip `ToggleGroup`; SKU hết hàng mờ + gạch;
  - số lượng có nút −/+;
  - [Thêm vào giỏ] primary + [Mua ngay] outline; mobile có thanh mua dính đáy;
  - đánh giá + tóm tắt AI (`ProductReviewSummary`) trong thẻ;
  - khối dài dùng `Accordion` hoặc giữ `CollapsibleProse` theo `pdp_layout`.
- **Thêm vào giỏ:**
  - giữ drawer xác nhận `cartConfirmDrawer`, dựng lại bằng `Sheet` phải: sản phẩm vừa thêm, tạm tính,
    [Xem giỏ hàng] [Thanh toán];
  - thêm từ thẻ ở danh mục (nếu có) thì hiện toast `sonner` (`Toaster` đã ở layout site).
- **Ngân sách JS:** `CommandDialog`, `Sheet` nạp khi cần. Claude đo First Load JS của trang sản phẩm trên bản build: không
  được vượt 296 kB (+15% so mốc 257 kB).

## Xong khi
- `rg "module.css|styles\." <phạm vi>` rỗng, trừ phần chỉ đọc dùng chung.
- Lệnh kiểm `common.md` §4 xanh trong phạm vi.
- Ảnh 390 / 768 / 1440 trên 4 site seed:
  - `/san-pham`, một trang sản phẩm có nhiều SKU, `/tim-kiem?q=ao`, kết quả trống;
  - mở `Sheet` lọc trên mobile, `CommandDialog` tìm, `Sheet` xác nhận giỏ;
  - một mẫu tối.
- Luồng đã đi bằng tài khoản người mua, trên 4 site:
  - lọc / sắp xếp / phân trang;
  - tìm qua ô tìm đầu trang;
  - chọn biến thể (thử SKU hết hàng);
  - −/+ số lượng;
  - thêm vào giỏ → `Sheet` → [Xem giỏ hàng] tới `/gio-hang`;
  - [Mua ngay];
  - mở / đóng khối thông tin.
