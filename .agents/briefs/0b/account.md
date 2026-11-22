# 0b pha B1 – worker `account`: tài khoản, địa chỉ, đơn hàng thật

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md`, `.agents/briefs/0b/b1-contracts.md` (bắt buộc), `docs/web-gap-audit.md` route 9–11 và 14, `docs/contracts.md` §4.1, §4.5–4.8, `web/CLAUDE.md`, skill `design-system`.

## Sở hữu
Trang `(shop)/tai-khoan/**`, `(shop)/don-hang/**`, `(shop)/tra-cuu-don-hang/**`, `(shop)/tra-cuu-don/**`; `web/src/context/OrdersContext.tsx` (không xoá trong B1); `web/src/features/commerce/{AddressForm,OrderLookupView}.tsx` (+ css riêng); file mới trong `web/src/features/account/**` nếu cần.

## Việc phải làm
1. **Đơn hàng thật**: `/don-hang` (server component, `requireUser`) đọc `GET /orders` có lọc trạng thái và phân trang; `/don-hang/[id]` đọc `GET /orders/{id}`, `GET /orders/{id}/timeline`, `GET /shipping/shipments/{order_id}` (không có vận đơn thì ẩn phần đó). Đơn không thuộc người dùng (403/404 từ gateway) → `notFound()`. Thao tác qua `api()`: huỷ (`POST /orders/{id}/cancel`, lý do), đã nhận hàng (`/confirm-received`), yêu cầu trả hàng (`/return`, lý do), mua lại (thêm lại các SKU bằng `useCart().addToCart`), đơn chờ thanh toán VietQR → `RetryPaymentBox paymentId=…` (hợp đồng 4) và link tới `/thanh-toan/cho-thanh-toan/<payment_id>` (hợp đồng 5). Nút chỉ hiện ở trạng thái máy trạng thái cho phép (đối chiếu `docs/contracts.md` §6). Breakdown tiền (tạm tính, giảm giá, ship, xu, tổng) lấy từ đơn backend.
2. **Tài khoản** `/tai-khoan` (`requireUser`): hồ sơ (`GET/PATCH /users/me`), sổ địa chỉ CRUD (`GET/POST/PUT/DELETE /users/me/addresses`) bằng `AddressForm` (hợp đồng 3, chọn tỉnh/phường 2 cấp thật), số dư và lịch sử xu (`GET /coins/balance`), ví voucher đã lưu nếu backend trả được (không thì giữ mock và ghi lại), đơn gần đây (`GET /orders?page_size=…`), đăng ký mở gian hàng (`POST /shops/register`) và xem trạng thái (`GET /shops/me`; đã duyệt → link sang `/seller`), thông báo (`GET /notifications`, đánh dấu đã đọc `POST /notifications/read`) và tuỳ chọn thông báo (`GET/PUT /notifications/preferences`). Phần không có backend (hạng hội viên, số đo, yêu thích, đã xem, giới thiệu bạn bè) giữ mock từ `useMockStore()`, tách rõ.
3. **`AddressForm`** theo hợp đồng 3 (dùng chung với `checkout`). Lỗi trùng/không hợp lệ hiện câu tiếng Việt.
4. **Tra cứu đơn vãng lai** `/tra-cuu-don-hang`: backend chưa có (cần chống dò mã đơn, để sau). Giữ mock: chuyển dữ liệu/logic tra cứu mẫu sang một module trong `web/src/mock/`, chữ cho người mua giữ nguyên. Nếu `OrderLookupView` dùng `RetryPaymentBox` với đơn mẫu, thay bằng hiển thị tĩnh để không gọi API thật bằng dữ liệu mẫu.
5. Ngừng dùng `useOrders()` cho đơn và địa chỉ trong mọi file bạn sở hữu. Không xoá `OrdersContext.tsx` (pha C dọn).

## Tiêu chí nghiệm thu
- Với buyer demo, sau khi có ít nhất một đơn COD (tự tạo qua `POST /api/gw/checkout` có `Idempotency-Key` nếu trang thanh toán của `checkout` chưa xong), kiểm trên `next start` của bạn (cổng 3204, `GATEWAY_URL=http://localhost:38080 COOKIE_SECURE=false`), dán kết quả: `/don-hang` liệt kê đơn thật; `/don-hang/<id>` hiện timeline; huỷ đơn đang chờ xác nhận → trạng thái đổi thật; buyer khác mở `/don-hang/<id>` của buyer demo → 404; thêm/sửa/xoá địa chỉ thấy thay đổi ở `GET /api/gw/users/me/addresses`; đăng ký shop từ tài khoản buyer mới → `/tai-khoan` hiện "đang chờ duyệt".
- `rg -n "useOrders" ` trong file bạn sở hữu rỗng (trừ `OrdersContext.tsx`).
- `npm run lint`, `npm run format:check`, `NEXT_DIST_DIR=.next-account npm run build` sạch. Kiểm trực quan `/tai-khoan`, `/don-hang`, `/don-hang/[id]` ở 390px và 1440px trên dữ liệu thật, 0 FAIL mới.
