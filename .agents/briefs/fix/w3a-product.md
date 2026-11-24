# 3a – agent `product` (sở hữu `services/product/**`, `services/cart/**`, `tests/e2e/35_*.go`)
Theo `w3a-plan.md` D2, D8, D9.
1. D2: che SKU ẩn trong `GetSKUsBatch`; chủ shop, admin thấy đủ. Test bảng (ẩn danh, buyer, seller khác, seller chủ, admin) × (SKU tắt, sản phẩm chờ duyệt/từ chối/ẩn, đã xoá, đang bán). `commission_rate_bps` của SKU đang bán KHÔNG đổi (D1).
2. Cart: rà Add/Update/buildCart/checkout với SKU đã che, không lỗi, không hiện giá 0.
3. D8: `ListProducts.sku_ids` (≤ 200, quá → InvalidArgument; chỉ sản phẩm công khai; kết hợp được với phân trang; truy vấn có index).
4. D9 outbox (migration `000007_` nếu cần).
5. Sửa `pg_integration_test.go` đọc nhầm `TEST_SHIPPING_DB_URL`.
E2E 35: D2 qua gRPC product (mẫu `27_`), D8 qua REST.
