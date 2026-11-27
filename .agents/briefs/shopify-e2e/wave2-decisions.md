# Đợt 2 — quyết định của manager trên `.runs/shopify-e2e/wave2-contract.md` (S0a, 27/11)

Hợp đồng S0a được duyệt với các sửa đổi sau (đơn giản hoá có trần, ghi `ponytail:` trong code tại chỗ):

1. Khách hàng (W2-A) — KHÔNG làm bảng tổng hợp `shop_customers` cập nhật trong mọi TX đơn hàng.
   - Danh sách/chi tiết tính khi đọc: truy vấn gộp `orders` theo `(shop_id, buyer_id)` (đếm đơn, tổng chi theo công thức §1.1, đơn gần nhất,
     tên + SĐT che từ `shipping_address_json` của đơn gần nhất), sắp xếp + phân trang trong SQL. Thêm index `(shop_id, buyer_id, created_at DESC)`.
   - Chỉ một bảng mới `shop_customer_notes(shop_id, buyer_id, note, tags TEXT[], updated_at, PK(shop_id,buyer_id))` cho ghi chú/tag.
   - Không gọi `UserService.GetProfile` (không email) — `ponytail:` gộp khi đọc; materialize khi một shop > ~100k đơn.
   - REST giữ nguyên §1.3 (bỏ `available_tags` nếu tốn thêm truy vấn nặng; giữ nếu một `SELECT DISTINCT unnest(tags)` theo shop).
2. Bộ sưu tập (W2-B) — bỏ hẳn phần `search` (không migration search, không event `product.collection_updated`, không lọc tìm kiếm theo bộ sưu
   tập) và bỏ `products.tags`. Luật tự động chỉ: danh mục (kèm con), khoảng giá, còn hàng; đánh giá bằng SQL trong `product`. Trang site
   `/bo-suu-tap/[slug]` gọi `product` qua gateway. Còn lại theo §2 (MANUAL ≤ 200 SP, Site Studio chọn bộ sưu tập thật, sitemap).
3. Trang/Menu/SEO (W2-C) — menu + SEO trong `ConfigV2` như §3.1.1 (hưởng nháp/xuất bản/rollback sẵn có). Trang nội dung: một bảng `site_pages`
   có cột nháp + cột đã xuất bản (title/meta/blocks), trạng thái DRAFT/PUBLISHED/HIDDEN; KHÔNG làm `site_page_versions`, rollback, versions
   endpoint (`ponytail:` lịch sử phiên bản trang khi có nhu cầu). Bỏ các route `/rollback`, `/versions`.
4. Thứ tự: W2-0 (hợp đồng: proto, gen/go, allowlist, router gateway + mã lỗi, docs/contracts.md, sellerNav, host.ts prefix) chạy SAU KHI đợt 1
   (W, P, M) xong và được nghiệm thu; rồi W2-A/B/C song song (≤ 3 agy).
