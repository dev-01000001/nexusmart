# Swarm brief – NexusMart UI (toàn bộ giao diện web)

Bạn là coordinator của swarm. Claude Code là manager và sẽ review kết quả bằng skill design-taste-frontend. Đọc `AGENTS.md` trước.

## Mục tiêu
Dựng toàn bộ UI web NexusMart trong `web/`: Next.js (App Router, TypeScript), dữ liệu mock trong code, chưa có backend. Mọi chữ trên UI bằng tiếng Việt. Giá là số nguyên VND, hiển thị dạng `199.000₫`.

## Cảm hứng thị giác
Phong cách cửa hàng đồ basic Việt Nam kiểu Coolmate: sạch, ảnh sản phẩm là chính, thông tin size và chất liệu rõ, mua nhanh.
Đây là thương hiệu gốc NexusMart. KHÔNG dùng tên, logo, ảnh, câu chữ hay bố cục y hệt của Coolmate hoặc sàn nào khác.

## Art direction (bắt buộc, đã chọn sẵn, không tự đổi)
- Storefront: `.taste-skill/storefront-sheet.md` (direction contact-sheet, Work Sans, palette Frost and slate).
- Seller center: `.taste-skill/seller-sheet.md` (app style Achromatic ledger).
- Admin: `.taste-skill/admin-sheet.md` (app style Framed sheet).
- Cả ba dùng chung một palette và một typeface.
- Đọc hết `/home/dev/.claude/skills/design-taste-frontend/SKILL.md` (house rules, laws). Cho seller và admin, đọc thêm `references/app-screens.md`. Tuân thủ house rules: không eyebrow, không gradient, không shadow lúc nghỉ, không glass, không icon card, không monospace, không chữ hoa nhỏ giãn cách, heading tối đa 2 dòng.

## Phạm vi màn hình
Người mua `(shop)`:
- Trang chủ theo direction sheet. Trang danh mục và kết quả tìm kiếm có bộ lọc (size, màu, giá, gian hàng) và sắp xếp.
- Trang sản phẩm: chọn màu và size theo SKU, bảng size, chất liệu, tồn kho, voucher của gian hàng, link chat với người bán.
- Trang flash sale theo khung giờ. Trang gian hàng.
- Giỏ hàng gom theo gian hàng, voucher sàn, voucher gian hàng, mã freeship, xu.
- Checkout: sổ địa chỉ 2 cấp theo 34 tỉnh thành, phí ship, thanh toán COD, VietQR, ví sandbox (mock, không form thẻ).
- Đơn hàng: danh sách, chi tiết, theo dõi trạng thái, yêu cầu trả hàng. Tài khoản. Đăng nhập và đăng ký.
- Khung chat người mua với người bán. Panel trợ lý AI (UI mock, có trạng thái streaming).

Người bán `(seller)`: tổng quan, sản phẩm có biến thể (danh sách, tạo/sửa), đơn hàng, khuyến mãi (voucher, flash sale), tài chính (số dư, đối soát), chat.

Quản trị `(admin)`: hàng đợi duyệt gian hàng, cấu hình, khiếu nại, đối soát thanh toán, audit log.

Mọi màn có trạng thái loading, empty, error. Bố cục điện thoại là một bố cục riêng, không chỉ thu nhỏ.

## Ảnh
- Dùng công cụ tạo ảnh của bạn để sinh ảnh sản phẩm gốc của NexusMart: áo thun, polo, quần short, quần jogger, đồ lót, tất. Nền phẳng, ánh sáng nhất quán, không logo thương hiệu thật. Lưu vào `web/public/products/`.
- Nếu không tạo được ảnh, vẽ sản phẩm bằng SVG phẳng theo palette. Ảnh không khí lấy theo mục Imagery trong storefront sheet, có credit.
- Không bịa đánh giá, số liệu kinh doanh, logo đối tác, báo chí. Nội dung mẫu (tên sản phẩm, giá, gian hàng) được phép, liệt kê trong báo cáo.

## Ponytail
- Chỉ dùng Next.js và CSS thuần (CSS modules + một file token). Không UI kit, không Tailwind, không thư viện state, không thư viện icon. Giỏ hàng dùng React context.
- Một component dùng chung cho mỗi khái niệm (ProductCard, Price, Button). Không trừu tượng hoá thừa.

## Chia việc gợi ý
1. Một worker dựng nền: scaffold `web/`, tokens, font, layout ba khu vực, mock data, component dùng chung. Các worker khác chờ bước này.
2. Song song: storefront trang chủ + danh mục + sản phẩm; giỏ + checkout + đơn hàng + tài khoản; seller center; admin.
3. Một worker QA chạy gate liên tục và gửi lỗi về đúng worker.

## Gate trước khi báo xong
- `cd web && npm run build` và `npm run lint` sạch.
- `node /home/dev/.claude/skills/design-taste-frontend/scripts/lint.mjs web/src` sạch.
- Chạy `npm run start`, rồi `node <skill>/scripts/inspect.mjs http://localhost:3000/<route>` cho trang chủ, danh mục, sản phẩm, giỏ, checkout; thêm `--app` cho một màn seller và một màn admin. 0 FAIL ở 1440, 768, 390. Mở ảnh chụp và tự sửa lỗi nhìn thấy.
- Viết `web/DESIGN_REPORT.md`: danh sách route, kết quả inspector, ảnh nào được tạo, ảnh nào lấy ngoài và credit, nội dung mẫu cần thay, phần chưa xong.
