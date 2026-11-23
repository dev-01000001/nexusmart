# NexusMart Bulk Product Load Generator (`tools/loadgen`)

Script độc lập [`generate-products.mjs`](./generate-products.mjs) sinh từ **1 đến 50.000 sản phẩm** (mặc định **20.000**, kèm 2–3 SKU đa mức giá/sản phẩm, `sku_price_history`, `search_text` tiếng Việt không dấu) phục vụ kiểm thử tìm kiếm và kiểm thử tải. **Tách hoàn toàn khỏi `make seed`.**

## Tốc độ và hai chế độ ghi

| Chế độ | Cơ chế | Tốc độ ước tính | Phù hợp khi |
| --- | --- | --- | --- |
| `--mode copy` *(mặc định)* | PostgreSQL `COPY` vào bảng tạm rồi `INSERT ... ON CONFLICT DO NOTHING` trực tiếp vào `product_db` của project Docker Compose local | **~150–600 SP/giây** trên Docker local (kèm ~2,5 SKU + `sku_price_history` mỗi SP, tức ~400–1.500 dòng/giây; 20.000 SP ~35–110 giây tùy tải máy) | Nạp nhanh 20.000–50.000 SP ở môi trường dev local |
| `--mode api` | Gọi `POST /api/v1/seller/products` + `POST /api/v1/admin/products/{id}/review` qua Gateway REST | Chậm (chịu giới hạn `RATE_LIMIT_USER_PER_MIN=180` / `RATE_LIMIT_IP_PER_MIN=300` của Gateway) | Nạp lô nhỏ hoặc kiểm thử toàn bộ luồng REST + outbox `product.reviewed` |

## Hàng rào an toàn (chế độ `copy`)

Script **chỉ chạy trên DB dev local** và tự từ chối chạy ngay nếu phát hiện:
- `DATABASE_URL` có hostname **không phải** `localhost`, `127.0.0.1`, hoặc `::1`.
- Biến môi trường `NODE_ENV=production`, `NEXUSMART_ENV=production`, hoặc `APP_ENV=production`.
- Tên database trong `DATABASE_URL` không phải `product_db`.
- Khi máy host không có `psql` và dùng `docker compose exec -T postgres psql`, cổng của `docker compose port postgres 5432` phải khớp tuyệt đối với cổng trong `DATABASE_URL`.
- Mật khẩu trong `DATABASE_URL` luôn được che (`REDACTED`) trước khi in log.

## Cách dùng

Yêu cầu đã chạy `make seed` trước để có gian hàng và danh mục thật trên stack local.

```bash
# Sinh mặc định 20.000 sản phẩm (mode copy, có tiến độ, tiếp tục được)
node tools/loadgen/generate-products.mjs

# Sinh 50.000 sản phẩm (mức tối đa)
node tools/loadgen/generate-products.mjs --count 50000

# Tiếp tục sau khi dừng giữa chừng (tự bỏ qua các chỉ số đã tạo)
node tools/loadgen/generate-products.mjs --count 20000

# Dùng tag riêng cho một đợt kiểm thử tải
node tools/loadgen/generate-products.mjs --count 5000 --prefix k6run

# Dọn sạch toàn bộ sản phẩm/SKU/lịch sử giá đã sinh (không đụng 18 SP demo hay dữ liệu E2E)
node tools/loadgen/generate-products.mjs --cleanup
node tools/loadgen/generate-products.mjs --prefix k6run --cleanup

# Kiểm thử lô nhỏ đi qua API thật
node tools/loadgen/generate-products.mjs --mode api --count 20 --prefix apitest
node tools/loadgen/generate-products.mjs --mode api --prefix apitest --cleanup
```

## Chạy unit test

```bash
node --test tools/loadgen/generate-products.test.mjs
```
