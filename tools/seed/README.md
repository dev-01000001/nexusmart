# NexusMart Seed (`make seed`)

Script [`seed.mjs`](./seed.mjs) nạp dữ liệu mẫu từ chính `web/src/data/*` (single source of truth) vào backend qua Gateway REST API bằng Node 24 (không thêm dependency).

## Cách chạy

```bash
make seed
```

Biến môi trường (tự đọc mặc định từ `.env` ở gốc repo nếu không truyền):
- `SEED_GATEWAY_URL`: URL Gateway REST (mặc định `http://localhost:${GATEWAY_PORT:-8080}`).
- `MAILPIT_URL`: URL Mailpit API để lấy OTP đăng ký (mặc định `http://localhost:${MAILPIT_UI_PORT:-8025}`).
- `ADMIN_BOOTSTRAP_EMAIL` / `ADMIN_BOOTSTRAP_PASSWORD`: tài khoản admin bootstrap.

Script chạy lặp lại an toàn (idempotent): kiểm tra tồn tại trước khi tạo và coi `409 Conflict` / `already_credited` là đã có, không tạo bản ghi trùng.

## Dữ liệu được nạp

- **4 gian hàng** từ `web/src/data/shops.ts` (mỗi gian hàng một tài khoản seller có địa chỉ lấy hàng hợp lệ theo `GET /api/v1/shipping/provinces`, đăng ký shop và được admin duyệt).
- **6 danh mục** và **18 sản phẩm** từ `web/src/data/products.ts` (đầy đủ biến thể SKU, giá `*_vnd`, tồn kho, `weight_grams`, đường dẫn ảnh tương đối `/products/...`, `/models/...`), đã được admin duyệt.
- **11 voucher** từ `web/src/data/vouchers.ts` (5 platform + 2 freeship do admin tạo; 4 shop voucher do từng seller tạo).
- **3 khung Flash Sale** từ `web/src/data/flash-sale.ts` (1 khung đang chạy tính theo thời điểm hiện tại và 2 khung sắp mở bán kế tiếp, 16 item được seller đăng ký và admin duyệt).
- **1 tài khoản người mua demo** từ `web/src/data/orders.ts` (địa chỉ mặc định hợp lệ theo tỉnh/phường backend + `42.000` xu khởi tạo idempotent).

## Bảng tài khoản demo (chỉ dùng cho môi trường dev)

| Vai trò | Email | Mật khẩu dev | Ghi chú / Gian hàng |
| --- | --- | --- | --- |
| `admin` | `admin@nexusmart.local` | `AdminDevPassword123!` | Admin bootstrap (theo `.env`) |
| `buyer` | `minhquan.nguyen@nexusmart.vn` | `BuyerDevPassword123!` | Nguyễn Minh Quân · địa chỉ mặc định TP.HCM · 42.000 xu |
| `seller` | `seller.bachdang@nexusmart.local` | `SellerDevPassword123!` | Xưởng Dệt Bạch Đằng |
| `seller` | `seller.saigon@nexusmart.local` | `SellerDevPassword123!` | Kho Basic Sài Gòn |
| `seller` | `seller.danang@nexusmart.local` | `SellerDevPassword123!` | Sợi Tự Nhiên Đà Nẵng |
| `seller` | `seller.hanoi@nexusmart.local` | `SellerDevPassword123!` | Dệt Kim Hà Nội |
