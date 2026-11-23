# Worker `pay` – thanh toán, sổ cái, hoàn tiền, đối soát, tiền của sàn

Luật chung: `.agents/briefs/audit/rules.md`. Báo cáo: `docs/audit/pay.md`, mã `PAY-B<n>`, `PAY-D<n>`, `PAY-F<n>`. Đọc thêm `services/payment/CLAUDE.md`, skill `integrate-provider`.

## UC phụ trách
UC-B07, UC-B25, UC-PAY01, UC-PAY03, UC-PAY04, UC-PAY05, UC-PAY07, UC-S17, UC-A09, UC-A13, UC-A14.

## Đối chiếu Shopify/sàn
- Thanh toán: cổng chính, cổng bên thứ ba, thanh toán thủ công (COD, chuyển khoản), trả góp, đa tiền tệ, hoàn tiền, khiếu nại chargeback, phân tích gian lận, đối soát và payout.
- Tài chính: tài khoản số dư (Balance), vay vốn, thuế, báo cáo tài chính.
- Tiền của sàn: hoa hồng, phí dịch vụ, giữ tiền chờ giao thành công (escrow), đối soát và chi trả cho người bán.

## Phải kiểm trên stack (buyer riêng của mình)
- VietQR mock: tạo đơn → payment PENDING → QR/nội dung chuyển khoản → webhook ký đúng → PAID → đơn chuyển trạng thái; SSE trạng thái thanh toán (cần JWT, chỉ chủ đơn).
- Webhook: chữ ký sai → 401; `X-Timestamp` lệch > 300 giây → từ chối; cùng `provider_tx_id` gửi 10 lần → ghi nhận 1 lần (đếm `webhook_events`, ledger); số tiền webhook khác số tiền đơn → xử lý thế nào (UC-A14 giao dịch không khớp).
- Sổ cái ghi kép: SELECT bảng ledger/journal trong `payment_db`, mỗi giao dịch tổng nợ = tổng có, không có số thực; số dư seller = tổng bút toán; hoa hồng `ORDER_DEFAULT_COMMISSION_BPS=500` có ghi bút toán không; tiền có bị giữ (escrow) tới khi giao thành công/xác nhận nhận hàng không.
- Thanh toán lại đơn chờ thanh toán (UC-B25), hết hạn thanh toán (`PAYMENT_EXPIRY_MINUTES`), hoàn tiền toàn phần/một phần (ai kích hoạt, ledger đảo ngược), đối soát online hằng ngày và đối soát COD (job, bảng `reconciliation_*`, admin xem lịch sử), payout/rút tiền seller, khai báo tài khoản ngân hàng seller, cấu hình phí sàn, cấu hình phương thức thanh toán.
- Route HTTP nội bộ `/internal/*` của payment (cổng 48086) phải đòi JWT admin (PR #2); SSE phải đòi JWT và quyền sở hữu.
- Ví điện tử sandbox thứ ba theo spec ("Một ví điện tử ở môi trường sandbox"): có chưa.
- Dữ liệu: số tiền payment = số tiền đơn (order_db) = số hiển thị ở web; trạng thái payment và order nhất quán; seller xem số dư ở `/seller/tai-chinh` khớp ledger.
