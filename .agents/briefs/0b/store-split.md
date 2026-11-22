# 0b pha B0 – worker `store-split`: tách `StoreContext` theo chủ sở hữu (cơ học, không đổi hành vi)

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md`, `.agents/briefs/sites-0b-web-gateway.md` mục 6, `web/CLAUDE.md`. Bạn là worker DUY NHẤT sửa `web/src` trong lúc chạy (pha A của `platform`/`gateway` có thể còn chạy nhưng không đụng `web/src`). Pha A `bff` đã thay đăng nhập mock bằng phiên thật; không đụng phần phiên đó (`web/src/lib/server/**`, `lib/api.ts`, `SessionContext`, `middleware.ts`, `app/api/**`).

## Vì sao
`web/src/context/StoreContext.tsx` (~1.850 dòng) gom giỏ, đơn, địa chỉ cùng mọi tính năng mock (hội viên, thẻ quà tặng, chat, trợ lý AI, đánh giá, hỏi đáp, yêu cầu hỗ trợ, số đo, báo có hàng, đã xem, yêu thích, cookie, toast, dữ liệu mock seller/admin). Ở pha B1 bốn worker song song sẽ thay từng phần bằng dữ liệu thật; mỗi phần cần đúng một file một chủ.

## Việc phải làm (thuần cơ học, hành vi và UI không đổi)
Tách `StoreContext` thành ba provider/hook, cập nhật mọi chỗ import trong `web/src`, rồi xoá `StoreContext.tsx`:
1. `web/src/context/CartContext.tsx` → `useCart()`: giỏ (`cartItems`, `cartCount`, `cartSummary`, thêm/sửa/xoá/chọn), voucher và xu đang chọn cho đơn sắp đặt, `savedVoucherCodes`/`saveVoucherCode`, cách giao hàng, `guestCheckout`, `placeOrder`. (Chủ pha B1: `checkout`.)
2. `web/src/context/OrdersContext.tsx` → `useOrders()`: `orders` và các thao tác sau mua (`cancelOrder`, `requestReturnOrder`, `updateOrderStatus`, `lookupOrder`, `retryOrderPayment`, `requestEditOrderAddress`, `requestExchangeOrder`, `requestVatInvoice`), `addresses` và thao tác địa chỉ, `selectedAddress*`. (Chủ pha B1: `account`.) `placeOrder` ở `CartContext` vẫn ghi đơn mock sang đây như hiện tại (gọi hàm của `OrdersContext`).
3. `web/src/mock/MockStoreContext.tsx` → `useMockStore()`: mọi thứ còn lại không có backend (hội viên, xu hiển thị, thẻ quà tặng, edu, chat, AI, đánh giá, hỏi đáp, hỗ trợ, số đo, báo có hàng, đã xem, yêu thích, cookie, toast, và dữ liệu mock seller/admin như `shopApprovals`, `disputes`, `reconciliationRows`, `platformConfig`, `activeSellerShopId`). Đầu file ghi một dòng chú thích: đây là dữ liệu mẫu cho tính năng chưa có backend.
- Giữ nguyên key `localStorage` hiện tại cho phần mock trong lượt này (pha B1 xử lý giỏ/đơn); mỗi provider chỉ lưu phần của mình. Nếu thứ tự provider quan trọng (Cart gọi Orders), lồng đúng thứ tự trong `layout.tsx` (chỉ sửa đúng chỗ provider, không đụng phần phiên của `bff`).
- Type dùng chung đang khai trong `StoreContext.tsx` chuyển theo phần của nó; type dùng ở nhiều phần đặt ở `web/src/context/types.ts`.
- Không đổi tên trường, không đổi logic, không đổi chữ hiển thị. Không dọn dẹp ngoài phạm vi.

## Tiêu chí nghiệm thu
- `rg -n "StoreContext|useStore\b" web/src` rỗng; ba hook mới là đường duy nhất tới dữ liệu cũ.
- `cd web && npm run lint && npm run format:check && npm run check:data && npm run build` sạch (dùng `NEXT_DIST_DIR=.next-split` cho build).
- Chạy `NEXT_DIST_DIR=.next-split GATEWAY_URL=http://localhost:38080 COOKIE_SECURE=false npx next start -p 3206` sau build và mở (Playwright nếu có, không thì curl) trang chủ, một trang sản phẩm, giỏ, thanh toán, tài khoản, `/seller`, `/admin` (đăng nhập admin bootstrap trong `.env` nếu cần): không lỗi runtime, hành vi mock như trước (thêm giỏ → badge tăng; đặt hàng mock → thấy trong danh sách đơn).
- Báo cáo liệt kê trường nào vào hook nào (bảng), để manager giao chủ pha B1.
