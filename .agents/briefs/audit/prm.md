# Worker `prm` – khuyến mãi, xu, flash sale, thông báo

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/prm.md`, mã `PRM-B<n>`, `PRM-D<n>`, `PRM-F<n>`.

## UC phụ trách
UC-B15, UC-B16, UC-B18, UC-B22, UC-B23, UC-S05, UC-S08, UC-S18, UC-A07; và email/thông báo của mọi luồng (đăng ký, duyệt shop UC-A01, đặt hàng, thanh toán, giao hàng, hoàn tiền).

## Đối chiếu Shopify/sàn
- Khuyến mãi và marketing: mã giảm giá và giảm tự động, mua X tặng Y, miễn phí vận chuyển, kết hợp khuyến mãi, email marketing, automation, form thu lead, chat Inbox có nhân viên bán hàng AI, hợp tác influencer.
- Thông báo và tuân thủ (phần thông báo): mẫu email và SMS giao dịch, chính sách cửa hàng.
- Chương trình toàn sàn (sàn): flash sale, voucher sàn và voucher shop, đăng ký sản phẩm vào chiến dịch.

## Phải kiểm trên stack (buyer riêng của mình; voucher/flash sale thử hết suất thì tự tạo bản AUDIT)
- Voucher sàn, voucher shop, mã freeship: điều kiện đơn tối thiểu, giảm tối đa, hạn dùng, giới hạn lượt toàn cục và mỗi người, phạm vi shop/danh mục; kết hợp voucher sàn + shop + freeship + xu trong một checkout (quy tắc kết hợp có định nghĩa không, tổng giảm có vượt giá trị đơn không).
- Trừ lượt voucher và suất flash sale bằng Lua nguyên tử trên Redis (đọc script, đọc test `tests/e2e/06_flash_sale_test.go`; không bắn tải qua gateway), reserve và hoàn voucher/xu khi huỷ đơn hoặc saga bù trừ (kiểm số lượt/xu trước và sau).
- Flash sale theo khung giờ: giá flash áp ở giỏ/checkout, hết suất, ngoài khung giờ thì về giá thường; seller đăng ký sản phẩm vào flash sale → admin duyệt (admin có liệt kê item PENDING không, hay phải nhập tay `item_id`).
- Xu: tích khi hoàn tất đơn, dùng khi checkout, số dư xu, lịch sử.
- Ví voucher (lưu voucher), seller bật/tắt/xoá voucher shop, chương trình giảm giá của gian hàng (UC-S18), `GET /api/v1/seller/vouchers` có trả voucher hết hạn/chưa bắt đầu không.
- Thông báo: email Mailpit cho từng sự kiện (nội dung tiếng Việt, đúng số tiền/mã đơn, không lộ PII thừa), trung tâm thông báo trong app và cài đặt bật/tắt (UC-B23), consumer NATS idempotent theo `event_id`, `dead_letter_events`.
- Dữ liệu: 11 voucher và 3 khung flash sale (16 item) trong DB/API khớp `web/src/data/vouchers.ts`, `flash-sale.ts` (giá trị, điều kiện, thời gian, giá flash, suất); hiển thị ở web khớp backend.
