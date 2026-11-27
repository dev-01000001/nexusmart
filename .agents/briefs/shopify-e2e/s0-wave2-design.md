# S0a — Thiết kế hợp đồng đợt 2 (CHỈ ĐỌC, ra một tài liệu để manager duyệt)

Đọc trước: `.agents/briefs/shopify-e2e/common.md`, `AGENTS.md`, `docs/contracts.md`, `.runs/shopify-e2e/gap.md` (mục TOP 8: hạng 2, 3, 5),
`docs/adr/0001-site-template-format.md`, skill `add-rpc`, `add-migration`, `add-event`.

KHÔNG sửa file nào trong repo (swarm khác đang sửa proto payment/gateway/web). Chỉ ghi `.runs/shopify-e2e/wave2-contract.md`.

## Ba tính năng kiểu Shopify cần hợp đồng
1. **Khách hàng của shop** (Shopify "Customers"): người bán xem danh sách khách đã mua ở shop mình (tên, email/SĐT che bớt theo quy định PII
   hiện có, số đơn, tổng chi `int64` VND, đơn gần nhất), lọc/tìm/sắp xếp, trang chi tiết (lịch sử đơn của khách tại shop, địa chỉ giao gần nhất,
   ghi chú nội bộ + tag do người bán đặt). Dữ liệu tổng hợp ở service sở hữu đơn (`order`), cập nhật trong transaction nghiệp vụ hoặc qua
   event idempotent; backfill từ đơn cũ. Chỉ thấy khách của shop mình (`shop_id` từ JWT).
2. **Bộ sưu tập** (Shopify "Collections"): người bán tạo bộ sưu tập thủ công (chọn sản phẩm, sắp thứ tự) và tự động (luật: danh mục, khoảng giá,
   còn hàng, tag nếu có); slug, ảnh bìa, mô tả, ẩn/hiện. Site gian hàng có trang `/bo-suu-tap/[slug]` và khối Site Studio chọn bộ sưu tập thật
   (hiện `collection_slug` trong Site Studio là dữ liệu mẫu tĩnh — tìm chỗ đó). Tìm kiếm/lọc trên site theo bộ sưu tập nếu `search` cần biết.
3. **Trang nội dung + menu + SEO cho site** (Shopify "Online Store → Pages/Navigation/Preferences"): người bán tạo trang tuỳ chỉnh
   (`/trang/[slug]`: tiêu đề + nội dung khối an toàn — đoạn văn, tiêu đề, danh sách, ảnh, FAQ; KHÔNG HTML/JS thô theo ADR-0001), sửa nội dung
   `/gioi-thieu`, `/chinh-sach` (hiện cố định), menu đầu trang và chân trang (link tới trang, bộ sưu tập, sản phẩm, URL ngoài có kiểm), SEO
   (meta title/description, ảnh chia sẻ) cho trang chủ site; lưu theo phiên bản/xuất bản như Site Studio hiện có.

## Tài liệu cần ra (`.runs/shopify-e2e/wave2-contract.md`)
Cho từng tính năng:
- Service sở hữu + lý do; bảng/migration (tên file đúng kiểu đánh số của service), index.
- Proto: message + RPC chính xác (tên trường, kiểu, số thứ tự), theo phong cách file proto hiện có; RPC nội bộ nào cần vào allowlist.
- REST gateway: method, path, quyền, query/body, response, mã lỗi — đúng định dạng bảng `docs/contracts.md`.
- Event NATS (nếu có): subject, payload, idempotency, consumer.
- Web: trang mới/sửa (đường dẫn trong `web/src/app`), BFF route tường minh, mục menu seller (`web/src/features/seller/shell/sellerNav.ts`).
- Phân quyền theo đối tượng + PII: chỗ nào che email/SĐT.
- Kế hoạch test (unit/integration/e2e) và tiêu chí nghiệm thu đo được.
- Danh sách file mỗi swarm triển khai sẽ sửa, để chia ownership không giao nhau; file dùng chung (router gateway, contracts.md, sellerNav,
  gen/go) ghi rõ để một swarm hợp đồng làm trước.
- Rủi ro + phần nên cắt nếu thiếu thời gian (ghi `ponytail:` cho đơn giản hoá có trần).
Dẫn chứng `file:dòng` thật cho mọi khẳng định về code hiện có. Ngắn, bảng là chính.
