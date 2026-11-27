# Swarm E — ADR lưu trữ ảnh do người bán tải lên (CHỈ TÀI LIỆU)

Đọc trước: `.agents/briefs/shopify-e2e/common.md`, `AGENTS.md` luật 11, các ADR trong `docs/adr/` (mẫu và chỉ mục `docs/adr/README.md`).

## Vì sao
Người bán không tải được ảnh sản phẩm của mình: form chỉ cho chọn trong ~12 ảnh áo mẫu (`web/src/features/seller/ProductFormView.tsx:73-86`).
Một xưởng gốm không bán được nếu không có ảnh thật. Cũng cần cho logo/banner site và ảnh bằng chứng đổi trả. Đây là năng lực mới ⇒ cần ADR được
chủ dự án duyệt trước khi code.

## Việc
1. Chạy quy trình skill `evaluate-oss` cho nhu cầu: lưu + phục vụ ảnh người dùng tải lên (JPEG/PNG/WebP, ≤ 5 MB/ảnh, ước lượng quy mô: 1.000
   shop × 200 SP × 5 ảnh), qua gateway/BFF hiện có, chạy được trên docker compose một máy (production hiện chạy trên 1 VM, sau Cloudflare Tunnel).
2. So ít nhất: (a) Postgres `bytea` trong DB của `product` (không thêm hạ tầng); (b) MinIO (S3-compatible, thêm 1 container + volume);
   (c) thư mục volume trên đĩa do một service phục vụ. Tiêu chí: vận hành/backup (`tools/backup`), bảo mật (kiểm magic bytes, chặn SVG/HTML,
   bỏ EXIF/GPS, kích thước), cache CDN (Cloudflare, header immutable), chi phí RAM/đĩa trên VM hiện tại, đường nâng cấp sau này, giấy phép.
3. Viết `docs/adr/0011-seller-media-storage.md` trạng thái **Đề xuất** (chờ duyệt), theo mẫu ADR hiện có: bối cảnh, phương án, quyết định đề xuất,
   hệ quả, hợp đồng phác thảo (REST upload/serve qua gateway + BFF, giới hạn, quyền theo shop từ JWT, xoá ảnh mồ côi), kế hoạch kiểm thử.
   Thêm dòng vào `docs/adr/README.md`.
4. Báo cáo cuối: 5 dòng tóm tắt khuyến nghị + 1 câu hỏi có/không cho chủ dự án.

## Được sửa
Chỉ `docs/adr/0011-seller-media-storage.md` (mới) và `docs/adr/README.md`. Không code, không chạy docker/make/build.
