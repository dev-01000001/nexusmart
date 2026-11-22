# 0b pha B1 – worker `catalog`: duyệt sản phẩm bằng dữ liệu thật

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md`, `.agents/briefs/0b/b1-contracts.md` (bắt buộc), `docs/web-gap-audit.md` mục 1.1–1.3 và route 1–6 ở mục 2.1, `web/CLAUDE.md`, skill `design-system`.

## Sở hữu
Trang `(shop)/page.tsx` (trang chủ), `(shop)/danh-muc/**`, `(shop)/tim-kiem/**`, `(shop)/san-pham/**`, `(shop)/gian-hang/**`, `(shop)/flash-sale/**`; `web/src/features/catalog/**`, `web/src/features/product/**`, `web/src/components/ProductCard.*`, `web/src/features/commerce/ProductAddonStrip.tsx`; hàm đọc dữ liệu mới `web/src/lib/catalog*.ts` (server) và mapper. Các trang mock có dùng `ProductCard` (`bo-suu-tap`, `chien-dich`, `hoi-vien`, `the-qua-tang`, `tap-chi`): chỉ sửa nếu cần để giữ biên dịch.

## Việc phải làm
1. **Đọc dữ liệu thật ở server component** qua `gatewayFetch`: danh mục (`GET /categories`), sản phẩm (`GET /products` với `category_slug`, `shop_slug`, `keyword`, `min_price_vnd`/`max_price_vnd`, `sort_by`, `page`, `page_size`), chi tiết theo slug (`GET /products/slug/{slug}`, có SKU và tồn kho thật), gian hàng (`GET /shops`, `GET /shops/slug/{slug}`), voucher (`GET /vouchers`, `?shop_id=`), flash sale (`GET /flash-sales?include_upcoming=true`, có suất còn lại). Chọn cách render phù hợp (dynamic hoặc `revalidate` ngắn); trang sản phẩm/gian hàng không còn `generateStaticParams` từ dữ liệu mẫu; slug không tồn tại → `notFound()`.
2. **Mapper** từ kiểu gateway sang view model mà UI hiện có cần (màu/size lấy từ `skus[].attributes`, giá từ SKU, ảnh từ `images`/`image_url`). Nội dung biên tập không có ở backend (câu chuyện chất liệu, thông số người mẫu, hướng dẫn bảo quản, đánh giá, hỏi đáp, bảng size…) lấy từ dữ liệu mẫu theo `slug` qua một module rõ tên trong `web/src/mock/` (ví dụ `productEditorial.ts`), và trang vẫn hiển thị ổn khi sản phẩm không có nội dung biên tập (sản phẩm người bán mới tạo).
3. **Lọc/sắp xếp**: lọc nào backend có thì gửi lên gateway (danh mục, gian hàng, giá, từ khoá, sắp xếp mới nhất/giá/tên); lọc backend chưa có (13 nhóm thuộc tính, bán chạy, tìm bỏ dấu/đồng nghĩa/gợi ý chính tả) giữ chạy phía client trên tập sản phẩm thật đã tải, ghi chú `ponytail:` trần (khi có Search service thì chuyển lên server). URL lọc hiện có giữ nguyên.
4. **Thêm giỏ / mua ngay** theo hợp đồng 1 và 2: chọn biến thể → `useCart().addToCart({ skuId, quantity })` với `sku_id` thật; hết hàng/không đủ tồn hiện đúng trạng thái; chưa đăng nhập → chuyển `/dang-nhap?next=<trang hiện tại>`. Flash sale: "Mua ngay" dẫn `/thanh-toan?sku=…&qty=1&flash=<flash_sale_item_id>`; hiện suất còn lại thật, khung đang chạy/sắp tới theo thời gian thật.
5. **Lưu voucher** trên trang sản phẩm/gian hàng: `POST /vouchers/{id}/save` qua `api()` (cần đăng nhập).
6. Thông tin gian hàng trên trang sản phẩm từ `GET /shops/{id}` (công khai), không dùng dữ liệu mẫu cho phần đã có backend.

## Tiêu chí nghiệm thu
- Trang chủ, `/danh-muc`, `/danh-muc?cat=ao-thun`, `/tim-kiem?q=ao`, `/san-pham/<slug thật>`, `/gian-hang/<slug thật>`, `/flash-sale` hiển thị dữ liệu từ backend (kiểm: đổi giá/tồn một SKU qua REST seller rồi tải lại trang thấy thay đổi; dán lệnh và kết quả). `/san-pham/khong-ton-tai` → 404.
- Thêm giỏ từ trang sản phẩm khi đã đăng nhập buyer demo → `GET /api/gw/cart` có SKU đó (phụ thuộc `checkout` hoàn tất `addToCart`; nếu chưa xong khi bạn kiểm, ghi rõ).
- `rg -n "from \"@/data/products\"|PRODUCTS\b" web/src/app/\(shop\)/{page.tsx,danh-muc,tim-kiem,san-pham,gian-hang,flash-sale} web/src/features/{catalog,product}` chỉ còn chỗ đọc nội dung biên tập qua module mock (liệt kê từng chỗ và lý do).
- `npm run lint`, `npm run format:check`, `npm run check:data`, `NEXT_DIST_DIR=.next-catalog npm run build` sạch. Kiểm trực quan 390px và 1440px các trang trên (`node /home/tai/.claude/plugins/cache/taste-skill/taste-skill/2.1.4/skills/taste-skill/scripts/inspect.mjs <url> --widths 1440,390` nếu chạy được), 0 FAIL mới.
