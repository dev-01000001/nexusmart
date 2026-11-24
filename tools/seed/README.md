# NexusMart Seed (`make seed`)

Script [`seed.mjs`](./seed.mjs) nạp dữ liệu mẫu từ chính `web/src/data/*` (single source of truth) vào backend qua Gateway REST API và webhook ký HMAC-SHA256 bằng Node 24 (không thêm dependency, **không ghi trực tiếp DB**).

## Cách chạy

```bash
make seed
# hoặc chạy trực tiếp:
node tools/seed/seed.mjs
```

Biến môi trường (tự đọc mặc định từ `.env` ở gốc repo nếu không truyền):
- `SEED_GATEWAY_URL`: URL Gateway REST (mặc định `http://localhost:${GATEWAY_PORT:-8080}`).
- `MAILPIT_URL`: URL Mailpit API để lấy OTP đăng ký và OTP xác minh SĐT người mua mẫu (mặc định `http://localhost:${MAILPIT_UI_PORT:-8025}`).
- `ADMIN_BOOTSTRAP_EMAIL` / `ADMIN_BOOTSTRAP_PASSWORD`: tài khoản admin bootstrap.
- `PAYMENT_WEBHOOK_SECRET` / `SHIPPING_WEBHOOK_SECRET`: khóa ký HMAC-SHA256 dùng để chuyển trạng thái đơn hàng mẫu qua `/webhooks/payment` và `/webhooks/shipping/ghn`.

Script chạy lặp lại an toàn (idempotent): kiểm tra tồn tại trước khi tạo, dùng `Idempotency-Key` cố định cho đơn mẫu và chỉ tiến thêm các bước chưa hoàn tất; coi `409 Conflict` / `already_credited` là đã có, không tạo bản ghi trùng.

> Để sinh dữ liệu quy mô lớn 20.000–50.000 sản phẩm cho kiểm thử tìm kiếm/tải, dùng công cụ tách riêng tại [`tools/loadgen/README.md`](../loadgen/README.md) (không nằm trong `make seed`).

## Dữ liệu được nạp

- **4 gian hàng** từ `web/src/data/shops.ts` (mỗi gian hàng một tài khoản seller có địa chỉ lấy hàng hợp lệ theo `GET /api/v1/shipping/provinces`, đăng ký shop và được admin duyệt).
- **6 danh mục gốc + 15 danh mục con** từ `web/src/data/products.ts` (`CAT-D3`): cấu trúc `parent_id` đầy đủ và `commission_rate_bps` đồng bộ với tỷ lệ hoa hồng gian hàng (`550 bps` cho `ao-polo`, `quan-short`, `do-lot`, `polo-pique`, `short-linen`, `brief-tre`; `500 bps` cho các danh mục còn lại).
- **18 sản phẩm** từ `web/src/data/products.ts` (đầy đủ biến thể SKU, giá `*_vnd`, tồn kho, `weight_grams`, đường dẫn ảnh tương đối `/products/...`, `/models/...`), đã được admin duyệt.
- **11 voucher** từ `web/src/data/vouchers.ts` (5 platform + 2 freeship do admin tạo; 4 shop voucher do từng seller tạo).
- **3 khung Flash Sale** từ `web/src/data/flash-sale.ts` (`PRM-D1`): chọn SKU đang hoạt động có `available_stock` cao nhất, cắt chặt `total_slots <= available_stock`, `per_user_limit <= total_slots` và `flash_price_vnd < min_price_30d_vnd`.
- **4 chương trình giảm giá của shop** (UC-S18): 1 chương trình đang hoạt động cho mỗi gian hàng (kết hợp cả chế độ `discount_percent` và `discounted_price_vnd` số nguyên VND, tránh SKU đang chạy Flash Sale).
- **Lịch phí mẫu** (UC-A09): 3 lịch biểu phí hoa hồng danh mục tương lai (`ao-thun` 5,2%, `ao-polo` 5,8%, `tat-phu-kien` 5,5%) và 2 lịch phí thanh toán tương lai (`VIETQR` 1,5%, `COD` 1,0%) có `effective_from` ở tương lai (giữ nguyên phí thanh toán hiện hành 0 bps).
- **1 tài khoản người mua demo** từ `web/src/data/orders.ts` (xác minh SĐT thật qua `POST /api/v1/users/me/phone/otp` + Mailpit + `POST /api/v1/users/me/phone/verify` trước khi đặt đơn COD theo E19, địa chỉ mặc định hợp lệ theo tỉnh/phường backend + `42.000` xu khởi tạo idempotent).
- **6 đơn hàng mẫu ở nhiều trạng thái** (tạo 100% qua API thật & webhook ký HMAC, không ghi DB):
  - `PREPARING` (VietQR đã thanh toán, shop đã xác nhận)
  - `SHIPPING` (COD đã tạo vận đơn GHN và đang giao)
  - `COMPLETED` (VietQR đã giao & khách xác nhận nhận hàng — sinh sổ cái seller & xu thưởng)
  - `CANCELLED` (COD khách hủy)
  - `RETURN_REQUESTED` (VietQR đã giao & khách gửi yêu cầu trả hàng)
  - `RETURNED` (VietQR đã giao, khách yêu cầu trả hàng & shop chấp nhận hoàn tiền)

## Bảng tài khoản demo (chỉ dùng cho môi trường dev)

| Vai trò | Email | Mật khẩu dev | Ghi chú / Gian hàng |
| --- | --- | --- | --- |
| `admin` | `admin@nexusmart.local` | `AdminDevPassword123!` | Admin bootstrap (theo `.env`) |
| `buyer` | `minhquan.nguyen@nexusmart.vn` | `BuyerDevPassword123!` | Nguyễn Minh Quân · SĐT đã xác minh · địa chỉ mặc định TP.HCM · 42.000 xu · 6 đơn mẫu |
| `seller` | `seller.bachdang@nexusmart.local` | `SellerDevPassword123!` | Xưởng Dệt Bạch Đằng |
| `seller` | `seller.saigon@nexusmart.local` | `SellerDevPassword123!` | Kho Basic Sài Gòn |
| `seller` | `seller.danang@nexusmart.local` | `SellerDevPassword123!` | Sợi Tự Nhiên Đà Nẵng |
| `seller` | `seller.hanoi@nexusmart.local` | `SellerDevPassword123!` | Dệt Kim Hà Nội |
