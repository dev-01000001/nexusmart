# Đợt 2b – agent `order` (sở hữu `services/order/**`, `tests/e2e/28_*.go`)
Theo `w2b-plan.md` §1, §2 (mua flash), §4, §8. Migration `0010_`. Skill `checkout-saga`.
1. Checkout: chương trình shop (tự tra ListActiveShopDiscounts + Reserve), freeship shop (GetShopShippingSettings bằng service token; `default_weight_grams` của shop khi SKU thiếu cân nặng), category_id/bps từng dòng, công thức R/P/D mới, `payment_fee_bps` từ payment.GetFeeConfig (lỗi → fail checkout rõ ràng, không đoán), `C_total ≤ R`; allocation gửi payment khớp `Σ(R + Ship − D) = amount`. Dữ liệu cũ mặc định 0.
2. Flash: chỉ suất đang chạy, hạn `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES`, từ chối COD khi có flash (thông điệp rõ); thanh toán lại giữ đúng hạn ngắn và không kéo dài giữ suất quá cửa sổ.
3. Báo cáo GetSellerReport/GetPlatformReport: theo ngày giờ VN, ≤ 366 ngày, định nghĩa (ghi vào comment + báo cáo): GMV = tổng P các đơn không huỷ; doanh thu ròng seller = R − hoàn − C của đơn COMPLETED theo ngày hoàn tất; tỷ lệ huỷ/trả; top sản phẩm; chỉ đơn của shop trong claims. Truy vấn có index, kiểm EXPLAIN.
4. ListOrdersNeedingReview/ResolveOrderReview (admin), `needs_manual_review` chỉ lộ cho admin.
5. Đơn huỷ/trả/hoàn một phần với chương trình shop và freeship shop: hoàn đúng, không vượt P; release đúng.
Test: unit bảng allocation đủ mọi loại giảm giá (gồm biên R âm, 0₫, nhiều shop, COD), flash hạn ngắn, báo cáo biên ngày VN; e2e 28.
