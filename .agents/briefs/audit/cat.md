# Worker `cat` – catalog, tồn kho, storefront, tìm kiếm, đánh giá

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/cat.md`, mã `CAT-B<n>`, `CAT-D<n>`, `CAT-F<n>`.

## UC phụ trách
UC-B03, UC-B04, UC-B20, UC-B12, UC-B13, UC-B21, UC-B10, UC-S16, UC-S02, UC-S03, UC-S07, UC-S12, UC-S13, UC-A02, UC-A04, UC-A08, UC-A15.

## Đối chiếu Shopify/sàn
- Storefront và nội dung: theme và trình chỉnh sửa, trang và blog, menu, tìm kiếm và lọc, gợi ý sản phẩm, SEO, đa ngôn ngữ, headless qua Storefront API.
- Catalog: sản phẩm, biến thể và tuỳ chọn, collection thủ công và tự động, media, sản phẩm số, bundle, subscription, gift card, giá và giá so sánh, import/export.
- Tồn kho: nhiều kho, theo dõi số lượng, chuyển kho, đơn nhập hàng, điều chỉnh, kiểm kê, trạng thái sẵn có / đã giữ / đang về.
- Niềm tin (sàn): đánh giá và nhận xét, kiểm duyệt sản phẩm.

## Phải kiểm trên stack
- Tìm kiếm theo spec tab Phạm vi: có dấu và không dấu ("ao thun" phải ra "Áo thun"), lọc, sắp xếp, gợi ý từ khoá, lịch sử tìm kiếm. Tìm ở backend hay chỉ lọc phía client trên tối đa 100 sản phẩm (`web/src/lib/catalog.ts`, `CatalogExplorer.tsx`)? Có Search service / index qua event không? Phân trang `ListProducts` đúng không (limit, cursor/offset, tổng).
- Chi tiết sản phẩm theo slug và id, sản phẩm chưa duyệt/bị từ chối/ẩn không lộ ra storefront, giá min/max khớp SKU, `available_stock = stock - reserved_stock`.
- Seller: tạo sản phẩm có biến thể (SKU, thuộc tính, giá `*_vnd` int64, cân nặng), sửa, ẩn, xoá; cập nhật tồn kho có optimistic locking (gửi `version` cũ phải bị từ chối); seller A không sửa được sản phẩm/SKU của shop B; giá âm/0/số thực bị từ chối.
- Admin duyệt/từ chối sản phẩm kèm lý do (seller nhận thông báo?), quản lý danh mục (cây, slug), từ khoá cấm/hàng cấm, banner trang chủ.
- Đăng sản phẩm hàng loạt bằng file (job nền theo spec), đánh giá sản phẩm (chỉ người đã mua, 1 lần/đơn), seller trả lời đánh giá, yêu thích/theo dõi gian hàng, trang gian hàng.
- Dữ liệu: 18 sản phẩm và 6 danh mục trong DB/API so với `web/src/data/products.ts` (tên, slug, giá, giá gốc/giá so sánh, % giảm, tồn kho từng SKU, thuộc tính, ảnh). Ảnh `/products/...`, `/models/...` có trả 200 từ `http://localhost:43000` không. Có còn sản phẩm/danh mục rác không. Spec yêu cầu bộ mẫu 20.000–50.000 sản phẩm: hiện có gì.
