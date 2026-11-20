# Nghiên cứu: mỗi gian hàng (provider) cấu hình thành một site riêng, kiểu Shopify

Bối cảnh: NexusMart là sàn đa gian hàng đồ basic (Go microservices + Rust payment + Next.js `web/`). Đọc `AGENTS.md`, `docs/CORE_REPORT.md`, `proto/nexusmart/user/v1/user.proto`, `web/DESIGN_REPORT.md` để biết hiện trạng.

Đề xuất sơ bộ của manager (cần bạn kiểm chứng, phản biện, không mặc định là đúng):
1. Một app Next.js đa tenant, `middleware` theo `Host`, rewrite sang `(site)/[shop]/...`.
2. Service Go mới `storefront` giữ cấu hình site (theme, trang, menu, tên miền, nháp/xuất bản).
3. Theme là dữ liệu: token + danh sách section JSON có schema; không cho người bán viết code theme.
4. Checkout dùng lõi `order`/`payment` hiện có; tiền qua NexusMart rồi đối soát.
5. Một tài khoản người mua dùng chung; giữ sàn chung song song site riêng.
6. Tên miền riêng: xác minh DNS, TLS tự động (Caddy on-demand TLS).

Quy tắc:
- CHỈ nghiên cứu. Không sửa code sản phẩm. Chỉ ghi đúng một file báo cáo được giao trong `docs/research/sites/`.
- Dùng web search và đọc tài liệu chính thức. Mỗi nhận định quan trọng kèm URL nguồn. Ghi rõ `đã kiểm chứng` (đọc nguồn chính thức) hay `suy luận`.
- Không đăng nhập, không tạo tài khoản, không nhập dữ liệu cá nhân ở đâu.
- Viết tiếng Việt, 1.200–2.000 từ, có bảng. Kết thúc bằng: khuyến nghị cụ thể cho NexusMart, rủi ro, câu hỏi còn mở.
