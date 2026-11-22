# 0b pha B1 – hợp đồng chung giữa các worker (đọc trước brief riêng)

Pha B1 chạy song song 5 worker: `catalog`, `checkout`, `account`, `seller-admin`, `qa`. Bốn worker đầu sửa `web/src` cùng lúc, nên các điểm giao nhau dưới đây là hợp đồng cố định: bên cung cấp làm đúng chữ ký, bên dùng gọi đúng chữ ký, không ai tự đổi. Cần đổi: ghi vào "Nhờ worker khác" trong báo cáo, manager quyết.

## Nền đã có (pha A, B0) – dùng, không viết lại
- Server: `gatewayFetch` (`web/src/lib/server/gateway.ts`), `getServerSession`/`requireUser`/`requireRole` (`web/src/lib/server/auth.ts`). Server component đọc dữ liệu bằng các hàm này (gửi kèm cookie, xử lý lỗi an toàn).
- Client: `api(path, init)` (`web/src/lib/api.ts`) gọi `/api/gw/<path>` (proxy cùng origin, tự refresh khi 401, lỗi có `code` + câu tiếng Việt). Phiên phía client: `useSession()` (`web/src/context/SessionContext.tsx`).
- Kiểu dữ liệu gateway: `web/src/lib/gateway-types.ts`. Thiếu kiểu thì thêm vào file riêng trong khu vực của mình (ví dụ `web/src/features/<khu-vực>/types.ts`), không sửa file chung này trong B1.
- Hook sau B0: `useCart()` (`web/src/context/CartContext.tsx`), `useOrders()` (`web/src/context/OrdersContext.tsx`), `useMockStore()` (`web/src/mock/MockStoreContext.tsx`).
- Bảng route → endpoint: `docs/web-gap-audit.md` mục 2 (đã kiểm từng route; theo đúng cột "Phạm vi 0b"). Hợp đồng REST: `docs/contracts.md`.
- Dữ liệu thật trong stack worktree sau `make seed`: 4 gian hàng, 6 danh mục, 18 sản phẩm (slug khớp `web/src/data/products.ts`), 11 voucher, 3 khung flash sale. Tài khoản demo: `tools/seed/README.md`.

## Hợp đồng giao nhau
1. **`useCart()`** (cung cấp: `checkout`; dùng: `catalog`, header). Giữ tên trường `cartItems`, `cartCount`; `addToCart` đổi thành:
   ```ts
   addToCart(input: { skuId: string; quantity: number }): Promise<{ ok: boolean; clampedToStock?: boolean; message?: string }>
   ```
   `skuId` là `sku_id` thật từ backend. Kiểu phần tử `cartItems` do `checkout` định nghĩa (export từ `CartContext.tsx`), có ít nhất `skuId`, `productId`, `shopId`, `quantity`.
2. **Mua ngay / flash sale** (cung cấp: `checkout`; dùng: `catalog`). Giỏ hàng backend không lưu flash sale, nên "Mua ngay" đi thẳng tới `/thanh-toan?sku=<sku_id>&qty=<n>[&flash=<flash_sale_item_id>]`; trang thanh toán đặt hàng với `from_cart: false` và `items` tương ứng.
3. **`AddressForm`** (`web/src/features/commerce/AddressForm.tsx`; cung cấp: `account`; dùng: `checkout`). Props:
   ```ts
   { initial?: GatewayAddress; onSaved: (address: GatewayAddress) => void; onCancel?: () => void }
   ```
   Tự gọi `POST`/`PUT /users/me/addresses` qua `api()`, chọn tỉnh/phường 2 cấp từ `GET /shipping/provinces` và `/shipping/provinces/{code}/wards`.
4. **`RetryPaymentBox`** (`web/src/features/commerce/RetryPaymentBox.tsx`; cung cấp: `checkout`; dùng: `account`). Props:
   ```ts
   { paymentId: string; onPaid?: () => void }
   ```
   Tự đọc `GET /payments/{id}`, hiện QR (`VietQrPreview`, cũng thuộc `checkout`) và trạng thái, cho thanh toán lại qua `POST /payments/{id}/retry`.
5. **Trang chờ thanh toán VietQR**: route `/thanh-toan/cho-thanh-toan/[paymentId]` (cung cấp: `checkout`). `account` dẫn link tới đây cho đơn đang chờ thanh toán.
6. **`ProductCard`** (`web/src/components/ProductCard.tsx`; chủ: `catalog`). Các trang mock (bộ sưu tập, chiến dịch, hội viên, thẻ quà tặng, tạp chí, tài khoản, `ProductAddonStrip`) đang truyền sản phẩm mẫu: thay đổi props chỉ được THÊM (tương thích ngược), để trang của worker khác vẫn biên dịch.
7. **File dùng chung không ai xoá trong B1**: `OrdersContext.tsx`, `web/src/mock/MockStoreContext.tsx`, `web/src/data/*`. Mỗi worker chỉ ngừng dùng phần đã có backend thay thế trong khu vực của mình. Việc xoá phần thừa và gộp mock làm ở pha C (một worker duy nhất). Không sửa `web/src/lib/server/**`, `lib/api.ts`, `middleware.ts`, `app/api/auth/**`, `app/api/gw/**`, `app/layout.tsx`, `ShopHeader` (thuộc nền; cần đổi thì báo).

## Luật chạy chung của B1
- Máy 14 GB RAM, 4 worker web chạy cùng lúc: khi lặp dùng `npx tsc --noEmit -p .` và `npx eslint <file đã sửa>`; chỉ chạy `next build` khi gần xong (`NEXT_DIST_DIR=.next-<tên>`), tối đa một `next dev`/`next start` mỗi worker và tắt khi xong. Lỗi biên dịch nằm trong file của worker khác đang sửa dở: chờ vài phút rồi chạy lại, không sửa hộ.
- Mọi trang có dữ liệu thật phải có trạng thái đang tải, rỗng và lỗi (dùng `StateView` sẵn có), lỗi hiện câu tiếng Việt tự nhiên, không lộ mã lỗi/thuật ngữ nội bộ.
- Tiền: số nguyên VND từ backend (`*_vnd`), hiển thị bằng `web/src/lib/format.ts`; không tính tiền bằng số thực.
- Giữ nguyên thiết kế (skill `design-system`, `frontend-design`). Phần giữ mock trong trang có dữ liệu thật: tách rõ trong code (đọc từ `useMockStore()`/`web/src/data`/`web/src/mock`, không trộn vào kiểu dữ liệu thật).
- Kiểm trực quan trang mình sửa ở 390px và 1440px trên dữ liệu thật (Playwright/`inspect.mjs` nếu có), đăng nhập bằng tài khoản demo khi cần.
