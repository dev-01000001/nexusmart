# 0b pha B1 – worker `checkout`: giỏ hàng, khuyến mãi, đặt hàng, thanh toán thật

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md`, `.agents/briefs/0b/b1-contracts.md` (bắt buộc), `docs/web-gap-audit.md` mục 1.5 và route 7–8, `docs/contracts.md` §4.3–4.7, skill `checkout-saga`, `web/CLAUDE.md`, skill `design-system`.

## Sở hữu
`web/src/context/CartContext.tsx`, trang `(shop)/gio-hang/**`, `(shop)/thanh-toan/**` (kể cả route mới `thanh-toan/cho-thanh-toan/[paymentId]`), `web/src/features/commerce/{RetryPaymentBox,VietQrPreview}.tsx`, `web/src/features/commerce/commerce.module.css` (chỉ thêm), file mới trong `web/src/features/checkout/**` nếu cần.

## Việc phải làm
1. **Giỏ hàng thật** trong `CartContext`: đọc `GET /cart`, thêm/sửa/xoá qua `POST /cart/items`, `PATCH /cart/items/{sku_id}`, `DELETE /cart/items/{sku_id}`, `DELETE /cart` (gọi `api()`). `addToCart` đúng hợp đồng 1. Chưa đăng nhập: giỏ rỗng, thao tác thêm trả `{ ok:false }` để UI chuyển trang đăng nhập (không giữ giỏ khách trong `localStorage`). Bỏ hoàn toàn việc lưu giỏ vào `localStorage` và mọi tham chiếu `useOrders()` trong file của bạn. Hiện đúng `clamped_to_stock`, `price_changed`, hết hàng.
2. **Chọn món**: backend không lưu "đã chọn"; giữ trạng thái chọn trong bộ nhớ (không persist) và khi đặt hàng một phần giỏ gửi `from_cart:false` + `items`. Toàn bộ giỏ → `from_cart:true`.
3. **Khuyến mãi và xu**: voucher shop/sàn/freeship từ `GET /vouchers` (theo `shop_id`, `scope`, `order_amount_vnd`), số dư xu `GET /coins/balance`, xem trước `POST /promotions/preview` mỗi khi giỏ/voucher/xu đổi (debounce). Voucher hết lượt/không đủ điều kiện hiện câu tiếng Việt từ lỗi thật.
4. **Thanh toán** `/thanh-toan`: địa chỉ từ `GET /users/me/addresses` (chọn, thêm mới bằng `AddressForm` theo hợp đồng 3); phí ship từng shop `POST /shipping/calculate-fee` → `shipping_choices`; phương thức COD và VietQR; chế độ "mua ngay" theo hợp đồng 2. `Idempotency-Key` sinh ở server (server component tạo UUID khi render trang, truyền xuống; bấm lại hoặc gửi lại sau lỗi mạng dùng CÙNG khoá; đặt thành công rồi thì không gửi lại). Đặt hàng `POST /checkout`: COD → chuyển `/don-hang/<id>` (hoặc danh sách nếu nhiều đơn); VietQR → `/thanh-toan/cho-thanh-toan/<payment_id>`.
5. **Trang chờ VietQR** `/thanh-toan/cho-thanh-toan/[paymentId]`: QR (`qr_url`/`qr_code`), số tiền, đếm ngược tới `payment_expires_at`, theo dõi trạng thái bằng SSE `GET /api/gw/payments/{id}/events` và dự phòng polling `GET /payments/{id}`; đã thanh toán → báo thành công + link đơn; hết hạn/thất bại → `POST /payments/{id}/retry` (chỉ VIETQR). Trang kiểm phiên ở server; payment không thuộc người dùng → không hiện dữ liệu.
6. **`RetryPaymentBox`, `VietQrPreview`** theo hợp đồng 4 (dùng lại trong trang chờ).
7. **Lỗi thật**: hết tồn khi đặt (`INSUFFICIENT_STOCK` …), voucher hết lượt, hết phiên (401 → đăng nhập lại rồi quay về), gateway không sẵn sàng (trạng thái lỗi có sẵn), COD vượt hạn mức/bị từ chối.
8. Phần không có backend trên hai trang này (khách vãng lai, thẻ quà tặng, quà ngưỡng 499k, VAT, thiệp quà, ví thứ 2): đọc từ `useMockStore()`/mock và ẩn khỏi luồng đặt hàng thật nếu nó làm sai số tiền (ghi lại từng mục đã ẩn/giữ).

## Tiêu chí nghiệm thu
- Với buyer demo (`tools/seed/README.md`), kiểm qua trình duyệt hoặc curl có cookie trên `next start` của bạn (`GATEWAY_URL=http://localhost:38080 COOKIE_SECURE=false`, cổng 3203), dán kết quả: thêm 2 SKU của 2 gian hàng → giỏ hiện 2 nhóm; áp voucher sàn → preview giảm đúng; đặt COD → thấy đơn ở `GET /api/gw/orders`; đặt VietQR → vào trang chờ, sau khi gửi webhook ký HMAC như `tests/e2e/03_vietqr_checkout_test.go` thì trang chuyển sang đã thanh toán (SSE hoặc polling); gửi lại cùng `Idempotency-Key` → `idempotent_replay:true`, không thêm đơn.
- `rg -n "localStorage" web/src/context/CartContext.tsx web/src/app/\(shop\)/{gio-hang,thanh-toan}` rỗng; `rg -n "useOrders" ` trong file của bạn rỗng.
- `npm run lint`, `npm run format:check`, `NEXT_DIST_DIR=.next-checkout npm run build` sạch. Kiểm trực quan giỏ, thanh toán, trang chờ ở 390px và 1440px trên dữ liệu thật, 0 FAIL mới.
