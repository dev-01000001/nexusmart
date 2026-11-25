# Swarm D – Order, Payment, Shipping, Promotion làm sâu
Hợp đồng: `m-all-contract.md`. Spec hàng "Luồng mua hàng", "Thanh toán", "Vận chuyển", "Kiểm thử".
- Trả hàng hoàn tiền: trạng thái mới trong máy trạng thái đơn (yêu cầu, duyệt, từ chối, đang hoàn hàng, đã hoàn tiền), saga hoàn tiền: payment ghi sổ cái kép, hoàn voucher/xu nếu áp dụng, shipping tạo vận đơn hoàn. Khiếu nại lên admin khi người bán từ chối.
- Payment: ví sandbox thứ ba (ZaloPay) dạng adapter có mock, IPN/callback HMAC chống replay idempotent; job đối soát hằng ngày ra báo cáo; số dư người bán (chờ đối soát, khả dụng).
- Shipping: adapter GHTK (mock khi không có key) bên cạnh GHN; bảng mapping địa chỉ cũ sang mới; job polling dự phòng webhook.
- Contract test gRPC giữa order và payment, shipping, promotion. Coverage ≥ 60% cho order, payment, promotion (ghi số thật từ `go test -cover` và `cargo llvm-cov` hoặc `cargo tarpaulin` nếu cài được user-local).
