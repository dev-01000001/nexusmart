# 0b pha B1 – worker `seller-admin`: Kênh người bán và trang quản trị bằng dữ liệu thật

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md`, `.agents/briefs/0b/b1-contracts.md` (bắt buộc), `docs/web-gap-audit.md` mục 1.4, 1.6 và mục 2.2–2.3 (route 25–44), `docs/contracts.md` §4, `web/CLAUDE.md`, skill `design-system` (art direction seller "Achromatic ledger", admin "Framed sheet").

## Sở hữu
Mọi file trong `web/src/app/(seller)/**`, `web/src/app/(admin)/**` (kể cả `SellerShell.tsx`, `AdminShell.tsx`, layout đã có `requireRole` của `bff`: giữ nguyên phần kiểm phiên), `web/src/features/seller/**`, `web/src/features/admin/**` (`sellerStore.ts`, `auditStore.ts`), file mới trong `web/src/features/{seller,admin}/**`.

## Việc phải làm
Nối đúng các phần có cột "Cần 0b" trong `docs/web-gap-audit.md` mục 2.2–2.3; phần "Để sau" giữ mock, tách rõ (đọc từ `useMockStore()`/mock, không trộn kiểu dữ liệu thật).
1. **Seller** (`shop_id` luôn do gateway lấy từ JWT; web không gửi `shop_id`):
   - `/seller`: shop (`GET /seller/shop`), đơn cần xử lý (`GET /seller/orders`), cảnh báo tồn thấp từ `GET /seller/products`, số dư (`GET /seller/balance`); biểu đồ xu hướng tính từ đơn thật nếu đơn giản, không thì giữ mock và ghi lại.
   - `/seller/san-pham`: danh sách mọi trạng thái (`GET /seller/products`), sửa tồn nhanh `PATCH /seller/skus/{sku_id}/stock` với `expected_version` (xung đột 409 → báo "dữ liệu vừa thay đổi" và tải lại), xoá `DELETE /seller/products/{id}`.
   - `/seller/san-pham/moi` và `/seller/san-pham/[id]`: tạo/sửa (`POST`/`PUT /seller/products`, `GET /seller/products/{id}`, `GET /categories`), ma trận SKU (`sku_code`, `name`, `attributes`, `price_vnd`, `stock`, `weight_grams`, `image_url`), slug tuỳ chọn; sản phẩm mới ở trạng thái chờ duyệt.
   - `/seller/don-hang` và `/seller/don-hang/[id]`: danh sách/chi tiết (`GET /seller/orders`, `GET /seller/orders/{id}`, `GET /orders/{id}/timeline`, `GET /shipping/shipments/{order_id}`), xác nhận (`POST /seller/orders/{id}/confirm`), giao hàng (`POST /seller/orders/{id}/ship` chọn hãng/dịch vụ), huỷ (`POST /orders/{id}/cancel`); nút theo máy trạng thái (`docs/contracts.md` §6).
   - `/seller/khuyen-mai`: voucher shop (`GET`/`POST /seller/vouchers`), đăng ký SKU vào flash sale (`GET /flash-sales?include_upcoming=true`, `POST /seller/flash-sales/{id}/items`).
   - `/seller/tai-chinh` (`GET /seller/balance`), `/seller/ho-so` (`GET /seller/shop`, phần cập nhật hồ sơ chưa có RPC giữ mock).
   - `/seller/chat`, `/seller/tuong-tac`: giữ mock.
   - Người dùng có role seller nhưng chưa có shop / shop chưa duyệt: hiện trạng thái rõ ràng, không lỗi.
2. **Admin**:
   - `/admin`: số liệu thật từ `GET /admin/shops?status=PENDING`, `GET /admin/products?status=PENDING`, `GET /admin/ledger/summary`, `GET /admin/orders`, `GET /admin/users`, `GET /admin/audit-logs`.
   - `/admin/gian-hang`: lọc trạng thái, duyệt/từ chối/yêu cầu bổ sung kèm lý do (`POST /admin/shops/{id}/review`).
   - `/admin/kiem-duyet`: hàng đợi sản phẩm chờ duyệt (`GET /admin/products?status=PENDING`, `GET /admin/products/{id}`), duyệt/từ chối (`POST /admin/products/{id}/review`); phần đánh giá/hỏi đáp/media/từ cấm giữ mock.
   - `/admin/doi-soat`: sổ cái (`GET /admin/ledger/summary`), chạy đối soát theo ngày (`POST /admin/payments/reconcile`), hoàn tiền (`POST /admin/payments/{id}/refund` với `Idempotency-Key` sinh mỗi lần mở form, gửi lại dùng cùng khoá); lịch sử phiên cũ giữ mock.
   - `/admin/audit-log` (`GET /admin/audit-logs`, lọc + phân trang), `/admin/cau-hinh` (CRUD danh mục `GET /categories`, `POST/PUT/DELETE /admin/categories`; người dùng `GET /admin/users`, `PATCH /admin/users/{id}/status|role` kèm lý do), `/admin/thanh-vien` (voucher sàn/freeship `POST /admin/vouchers`, khung flash sale `POST /admin/flash-sales`, duyệt item `POST /admin/flash-sale-items/{item_id}/approve`, cộng xu `POST /admin/coins/credit`; hạng hội viên/thẻ quà/edu giữ mock).
   - `/admin/khieu-nai`, `/admin/noi-dung`: giữ mock.
3. Phần của `sellerStore`/`auditStore` đã được backend thay thế: ngừng dùng trong trang; xoá phần không còn ai dùng trong hai file này (bạn sở hữu chúng). Ngừng dùng `useOrders()` trong khu vực của bạn.

## Tiêu chí nghiệm thu
- Kiểm trên `next start` của bạn (cổng 3205, `GATEWAY_URL=http://localhost:38080 COOKIE_SECURE=false`) bằng tài khoản demo, dán kết quả: seller A thấy đúng sản phẩm/đơn/voucher của shop mình (không thấy của shop khác); sửa tồn một SKU → trang sản phẩm công khai thấy tồn mới; tạo sản phẩm mới → admin thấy trong `/admin/kiem-duyet` → duyệt → sản phẩm xuất hiện ở `GET /api/gw/products`; một đơn COD của buyer demo → seller xác nhận rồi giao → trạng thái đổi thật; admin duyệt một shop chờ duyệt → chủ shop vào được `/seller`; `/admin/doi-soat` hiện sổ cái `is_balanced`; seller vào `/admin` → 404.
- `npm run lint`, `npm run format:check`, `NEXT_DIST_DIR=.next-seller npm run build` sạch. Kiểm trực quan `/seller`, `/seller/san-pham`, `/seller/don-hang`, `/admin`, `/admin/gian-hang`, `/admin/kiem-duyet` ở 390px và 1440px trên dữ liệu thật, 0 FAIL mới.
