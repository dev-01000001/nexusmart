# Đợt 2b – agent `payment` (sở hữu `services/payment/**`, `tests/e2e/29_*.go`)
Theo `w2b-plan.md` §1, §4, §8. Migration `0007_`.
1. GetFeeConfig/SetFeeConfig: theo phương thức, `effective_from` ≥ hiện tại (cho phép ngay), bps 0–2000, lịch sử bất biến; tra hiệu lực tại `at_time`; GetFeeConfig cho service token và người dùng đăng nhập, SetFeeConfig chỉ admin.
2. ListSellerLedger: bút toán `seller_pending/available:<shop>` của chính shop (seller) hoặc mọi shop (admin), phân trang, mô tả tiếng Việt theo loại (tiền đơn hàng, phí sàn, hoàn tiền, giải ngân, rút tiền), không lộ shop khác.
3. Kiểm mọi đường quyết toán/hoàn/giải ngân vẫn đúng khi R gồm P_shop, S_shop và C_total gồm phí thanh toán (không đổi sổ cái, chỉ thêm test); stream NATS theo §8.
Test: unit hiệu lực theo thời gian (biên), quyền; e2e 29: đổi phí → đơn mới khác đơn cũ, sổ giao dịch khớp số dư (tổng bút toán = số dư).
