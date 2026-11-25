# NexusMart Seed (`make seed`)

Script [`seed.mjs`](./seed.mjs) nạp dữ liệu mẫu từ chính `web/src/data/*` (single source of truth) vào backend qua Gateway REST API và webhook ký HMAC-SHA256 bằng Node 24 (không thêm dependency, **không ghi trực tiếp DB**).

## Cách chạy

```bash
make seed
# hoặc chạy trực tiếp:
node tools/seed/seed.mjs
```

Biến môi trường (tự đọc mặc định từ `.env` ở gốc repo nếu không truyền):
- `SEED_GATEWAY_URL` / `GATEWAY_URL`: URL Gateway REST (mặc định `http://localhost:${GATEWAY_PORT:-8080}`).
- `MAILPIT_URL`: URL Mailpit API để lấy OTP đăng ký và OTP xác minh SĐT người mua mẫu (mặc định `http://localhost:${MAILPIT_UI_PORT:-8025}`).
- `SITE_ROOT_DOMAIN`: tên miền gốc storefront (mặc định `nexusmart.localhost`).
- `SEED_CATALOG_COUNT`: số sản phẩm đại lý nạp qua `tools/catalog-gen` trên gian hàng thứ 5 (mặc định `20000`, đặt `0` để bỏ qua khi cần seed nhanh).
- `ADMIN_BOOTSTRAP_EMAIL` / `ADMIN_BOOTSTRAP_PASSWORD` (hoặc `ADMIN_EMAIL` / `ADMIN_PASSWORD`): tài khoản admin bootstrap.
- `SEED_BUYER_EMAIL` / `SEED_BUYER_PASSWORD`, `SEED_SELLER_EMAIL` / `SEED_SELLER_PASSWORD`: tùy chỉnh tài khoản demo.
- `PAYMENT_WEBHOOK_SECRET` / `SHIPPING_WEBHOOK_SECRET`: khóa ký HMAC-SHA256 dùng để chuyển trạng thái đơn hàng mẫu qua `/webhooks/payment` và `/webhooks/shipping/ghn`.

Script chạy lặp lại an toàn (idempotent): kiểm tra tồn tại trước khi tạo, dùng `Idempotency-Key` cố định cho đơn mẫu và chỉ tiến thêm các bước chưa hoàn tất; coi `409 Conflict` / `already_credited` là đã có, không tạo bản ghi trùng.

> Để nạp nhanh dữ liệu mẫu cốt lõi không kèm 20.000 sản phẩm đại lý, chạy `SEED_CATALOG_COUNT=0 make seed`. Ngoài ra vẫn có công cụ tải trực tiếp tại [`tools/loadgen/README.md`](../loadgen/README.md).

## Dữ liệu được nạp

- **4 gian hàng showcase** từ `web/src/data/shops.ts` + **1 gian hàng đại lý thứ 5** (`Kho Đại Lý NexusMart`, không xuất bản site để giữ gọn 4 site showcase).
- **6 danh mục gốc + 15 danh mục con** từ `web/src/data/products.ts` (`CAT-D3`): truy vấn qua `/api/v1/admin/categories` để đồng bộ `parent_id` và `commission_rate_bps`.
- **18 sản phẩm showcase** từ `web/src/data/products.ts` (đầy đủ biến thể SKU, giá `*_vnd`, tồn kho, `weight_grams`, đường dẫn ảnh tương đối), đã được admin duyệt.
- **11 voucher** từ `web/src/data/vouchers.ts` (5 platform + 2 freeship do admin tạo; 4 shop voucher do từng seller tạo; dọn voucher cũ đã ngừng theo B15).
- **3 khung Flash Sale** từ `web/src/data/flash-sale.ts` (`PRM-D1`): chọn SKU đang hoạt động có `available_stock` cao nhất, cắt chặt `total_slots <= available_stock`, `per_user_limit <= total_slots` và `flash_price_vnd < min_price_30d_vnd`.
- **4 chương trình giảm giá của shop** (UC-S18) và **lịch phí mẫu** (UC-A09).
- **4 site storefront đã xuất bản** (`bach-dang`, `sai-gon`, `da-nang`, `ha-noi`) với bảng màu/font riêng biệt, nâng cấp lên template `schema_version: 2` nếu có, kèm tên miền tùy chỉnh (`hanoi-knit.verified.test` ACTIVE, `danang.pending.test` PENDING).
- **Dữ liệu mẫu Chat**: cấu hình auto-reply và luồng 3 tin nhắn tiếng Việt trên `bach-dang` và `sai-gon` (tự bỏ qua nếu Chat chưa bật).
- **1 tài khoản người mua demo** (xác minh SĐT thật qua `POST /api/v1/users/me/phone/otp` + Mailpit + `POST /api/v1/users/me/phone/verify` trước khi đặt đơn COD theo E19, địa chỉ mặc định hợp lệ theo tỉnh/phường backend + `42.000` xu khởi tạo idempotent).
- **6 đơn hàng mẫu `STOREFRONT` ở nhiều trạng thái** (tạo 100% qua API thật với `sales_channel: "STOREFRONT"`, `storefront_shop_id` & webhook ký HMAC, dùng luồng đổi trả của `main`):
  - `PREPARING` (VietQR đã thanh toán, shop đã xác nhận)
  - `SHIPPING` (COD đã tạo vận đơn GHN và đang giao)
  - `COMPLETED` (VietQR đã giao & khách xác nhận nhận hàng — sinh sổ cái seller & xu thưởng)
  - `CANCELLED` (COD khách hủy)
  - `RETURN_REQUESTED` (VietQR đã giao & khách gửi yêu cầu trả hàng qua `POST /api/v1/orders/{id}/return`)
  - `RETURNED` (VietQR đã giao, khách yêu cầu trả hàng & shop chấp nhận hoàn tiền qua `POST /api/v1/seller/returns/{id}/respond`)
- **20.000 sản phẩm đại lý** qua `tools/catalog-gen` trên `seller-catalog@nexusmart.local` (khi `SEED_CATALOG_COUNT > 0`).

## Bảng tài khoản demo (chỉ dùng cho môi trường dev)

| Vai trò | Email | Mật khẩu dev | Ghi chú / Gian hàng |
| --- | --- | --- | --- |
| `admin` | `admin@nexusmart.local` | `AdminDevPassword123!` | Admin bootstrap (theo `.env`) |
| `buyer` | `buyer@nexusmart.local` | `BuyerDemo123!` | Nguyễn Minh Quân · SĐT đã xác minh · địa chỉ mặc định TP.HCM · 42.000 xu · 6 đơn mẫu |
| `seller` | `seller@nexusmart.local` | `SellerDemo123!` | Xưởng Dệt Bạch Đằng (`bach-dang`) |
| `seller` | `seller-saigon@nexusmart.local` | `SellerDemo123!` | Kho Basic Sài Gòn (`sai-gon`) |
| `seller` | `seller-danang@nexusmart.local` | `SellerDemo123!` | Sợi Tự Nhiên Đà Nẵng (`da-nang`, `danang.pending.test`) |
| `seller` | `seller-hanoi@nexusmart.local` | `SellerDemo123!` | Dệt Kim Hà Nội (`ha-noi`, `hanoi-knit.verified.test`) |
| `seller` | `seller-catalog@nexusmart.local` | `SellerDemo123!` | Kho Đại Lý NexusMart (không site; chứa danh mục 20k SP) |
