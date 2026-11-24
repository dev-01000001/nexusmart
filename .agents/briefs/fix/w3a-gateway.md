# 3a – agent `gateway` (sở hữu `services/gateway/**`, `tools/seed/**`, `tests/e2e/39_*.go`)
Theo `w3a-plan.md` D1, D2, D6, D7, D8.
1. D1: bỏ `commission_rate_bps` khỏi JSON công khai (kiểm mọi DTO dùng chung, kể cả sản phẩm lồng trong shop/flash/cart/preview); thêm `GET /admin/categories`; `seed.mjs` đọc route admin, chạy seed test. Route `/seller/products*` giữ.
2. D2: preview, calculate-fee, đăng ký flash gặp SKU ẩn → 409 `SKU_INACTIVE` (đăng ký flash của chính chủ giữ mã cũ).
3. D6: truyền `bank_account_id` (thiếu → 400), `expected_owner_name`; lỗi → 409 đúng mã.
4. D7: middleware riêng, test Redis giả/miniredis: lần 6 trong phút → 429 có `Retry-After`; người khác không bị ảnh hưởng; Redis lỗi → theo hành vi hiện có của các giới hạn khác (ghi rõ).
5. D8: `GET /products?sku_ids=` (≤ 200, phần tử sai → 400).
E2E 39: JSON công khai không có trường hoa hồng; admin thấy; bank OTP 429; `sku_ids`.
