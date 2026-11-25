# S – Cửa hàng (site theo tên miền phụ): trang chủ, danh sách, PDP, tìm kiếm, thông tin
Đọc `design.md`, `f-handoff.md`, `docs/adr/0001-site-template-format.md`.

## Sở hữu
`web/src/features/site/**`, `web/src/features/{search,chat,assistant}/**` (chỉ giao diện widget/ô tìm), `web/src/app/(site)/s/[host]/{page,layout,san-pham,tim-kiem,gioi-thieu,chinh-sach,flash-sale,voucher}/**` + CSS cạnh chúng. KHÔNG sửa `templateTypes.ts`, `templateValidator.ts` (schema) và test của chúng. `templateCatalog.ts`: chỉ rút gọn câu chữ, không đổi id/enum/màu/cấu trúc; phải khớp ràng buộc với catalog Go (đọc, không sửa Go).

## Việc
1. **8 giao diện phải khác nhau nhìn là biết** ngay ở màn đầu, thanh đầu trang, lưới, PDP, chân trang. Cùng Archivo, khác giọng và hình học:
   - `minimal-fashion` (normal): một ảnh 3:4 đứng giữa, tiêu đề tách hai nửa hai bên ảnh; lưới 3 cột so le độ cao.
   - `editorial` (tight): tiêu đề khổng lồ chồng lên hai ảnh lệch tầng; lưới bất đối xứng như tạp chí; đầu trang masthead.
   - `dense-grid` (normal 13–14px): không hero ảnh, vào thẳng ô tìm lớn + tường sản phẩm 5 cột, lọc dính.
   - `craft-atelier` (wide): ảnh chất vải tràn màn, tiêu đề góc dưới trái; cuộn ngang dải phim công đoạn.
   - `sport-active` (poster): tiêu đề nén cao 16–20vw, ảnh vận động cắt chéo `clip-path` mở theo cuộn; nền tối.
   - `luxury-maison` (wide, đậm 400): ảnh nhỏ đóng khung giữa khoảng lớn có chủ đích (một lần), mở chậm 1200ms; lưới 2 cột lớn.
   - `one-page-launch` (tight): một sản phẩm giữ chân giữa màn, 3 chặng cuộn đổi màu/góc; nút mua luôn thấy.
   - `multi-sku-catalog` (normal): mục lục chữ từng hàng, hover neo ảnh; bảng SKU đặt nhanh.
   - `classic`/v1: ảnh tràn màn, tiêu đề đáy.
   Mỗi biến thể section (`SECTION_VARIANTS_BY_TYPE`) vẫn phải dựng được và đẹp.
2. Lưới không mồ côi: 1 sản phẩm → thẻ đôi ngang; 2 → hai thẻ nửa màn; voucher 1 → dải ngang đầy hàng (mã lớn, nút Lưu). Mã voucher phải hiện trong HTML.
3. `SiteProductCard`: ảnh + tên 1 dòng + giá; hover ảnh thứ hai; nhãn giảm giá chữ nhỏ không nền hộp thô; chấm màu. Giữ `article:has(a[href^="/san-pham/"])`, liên kết tên là `a` cuối.
4. Đầu trang site: logo/tên, 3–4 link, icon tìm (mở ô tìm toàn màn clip-path), icon giỏ có số đếm nảy nhẹ khi thêm (scale 1→1.12→1, không elastic); giữ `header a[href="/tai-khoan"]`, `header a[href="/don-hang"]` khi đăng nhập, form search trong `main` ở `/tim-kiem`.
5. PDP: ảnh cuộn dọc lớn + cột mua dính; chọn màu đổi ảnh bằng crossfade; chọn size ô lớn; thanh mua dính đáy ở mobile; thông số, đổi trả, đánh giá vào `<details>`; thêm giỏ có drawer xác nhận trượt phải (vẫn có link "Mở giỏ hàng"). Giữ chuỗi test: `Kích cỡ:`, `Còn … sản phẩm sẵn giao`, `Thêm vào giỏ`.
6. Danh sách/tìm kiếm: bộ lọc dạng sheet, chip đang lọc, số kết quả; chuyển trang/ lọc có skeleton và giữ vị trí cuộn. Flash sale: đồng hồ lớn giọng `poster`, thanh còn suất. Voucher: vé lớn từng hàng.
7. Giới thiệu/chính sách: câu lớn + ảnh, chữ dài thu gọn sau "Xem thêm" (nội dung vẫn trong HTML).
8. Widget chat/trợ lý: gộp thành MỘT nút nổi mở bảng chọn (hiện hai nút đè nội dung), không che thanh mua mobile; bảng trượt lên, tin nhắn vào từng dòng.

## Nghiệm thu
Chụp trang chủ của cả 8 archetype (dùng trang preview/template gallery hoặc trang thử tạm không commit) ở 1440 và 390 xếp thành một tờ `.runs/shots/s/contact.png`; nhìn 5 giây phải phân biệt được. Bất biến mục 7. `templateValidator.test.mjs`, `site.test.mjs`, `siteEditorUtils.test.mjs` xanh.
