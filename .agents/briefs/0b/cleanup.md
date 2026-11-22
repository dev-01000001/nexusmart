# 0b pha C – worker `cleanup`: dọn phần thừa sau B1, một nguồn sự thật

Đọc trước: `.agents/briefs/sites-0b-swarm-rules.md`, `.agents/briefs/0b/b1-contracts.md`, `.agents/briefs/sites-0b-web-gateway.md` mục 6 và mục Gate, `web/CLAUDE.md`. Pha B1 đã xong; song song với bạn chỉ có worker `bff` sửa `web/src/middleware.ts`, `web/src/lib/server/**`, `web/src/app/api/**`, `web/src/components/ShopHeader.*`, `web/src/components/ShopFooter.*` (không đụng các file đó). Mọi file khác trong `web/src`, cùng các file dưới đây, bạn được sửa.

## Việc phải làm (ponytail: xoá nhiều hơn thêm, không đổi hành vi và UI)
1. **Một nguồn sự thật cho giỏ và đơn**:
   - `web/src/context/OrdersContext.tsx`: giỏ/đơn/địa chỉ đã lấy từ backend. Tìm mọi chỗ còn dùng `useOrders()` (ví dụ `AiAssistantPanel`, `tro-giup`, `PdpReviews`, …). Phần chỉ là tính năng mock (trợ lý AI, hỗ trợ, đánh giá "đã mua") đọc dữ liệu mẫu từ `web/src/mock/` hoặc `web/src/data`, không qua context đơn hàng. Sau đó xoá `OrdersContext.tsx` và provider của nó trong `web/src/app/layout.tsx` (chỉ sửa đúng dòng provider).
   - `CartContext.tsx`: bỏ `LegacyMockAddToCartInput` và nhánh mock; `AiAssistantPanel` (mock) không thêm giỏ bằng dữ liệu mẫu nữa: hoặc tra `sku_id` thật như `ProductCard` đang làm, hoặc bỏ nút thêm giỏ trong panel. Chọn cách ít code hơn.
   - Gỡ các cầu nối chỉ phục vụ luồng mock cũ (`registerCartBridge`, `applyCheckoutRewards`, `prependOrders`, …) nếu không còn ai dùng thật; bỏ trường không còn ai dùng khỏi `web/src/mock/MockStoreContext.tsx`.
   - `rg -n "localStorage" web/src` chỉ còn tiện ích theo người xem (mock store: số đo, yêu thích, đã xem, báo có hàng, cookie, hội viên mẫu), mỗi chỗ có một dòng chú thích lý do.
2. **Gộp kiểu DTO gateway**: các worker B1 tự khai kiểu riêng (ví dụ `web/src/features/checkout/types.ts`, `web/src/lib/catalog-types.ts`, kiểu trong `features/account`, `features/admin`, `features/seller`). Gộp các kiểu mô tả response/request của gateway về `web/src/lib/gateway-types.ts` (theo `docs/contracts.md`), xoá bản trùng; view model riêng của UI thì giữ tại chỗ. Gộp hàm trùng nhau nếu có (ví dụ nhãn trạng thái đơn tiếng Việt, định dạng tiền) về một chỗ.
3. **Đăng xuất**: mọi chỗ đăng xuất ngoài `ShopHeader` (ví dụ `/tai-khoan`) điều hướng bằng `window.location.assign(...)` như trang đăng nhập (lý do: `router.refresh` chen ngang điều hướng, Client Router Cache giữ phiên cũ).
4. **`web/tsconfig.json`**: `include` chỉ còn `**/*.ts`, `**/*.tsx`, `.next/types/**/*.ts`, `next-env.d.ts` (bỏ các `.next-<tên>/types` do build song song tự chèn). Ghi một dòng vào `web/CLAUDE.md`: build với `NEXT_DIST_DIR` sẽ tự chèn dòng vào `tsconfig.json`, hoàn nguyên trước khi commit. Xoá thư mục build cũ `web/.next-*`.
5. **`Makefile`**: `e2e-web` chạy `node --test 'tests/e2e-web/*.test.mjs'`; xoá `tests/e2e-web/index.js`. Gỡ `skip` ở ca `4h`, `4i` trong `tests/e2e-web/04_security_rbac_pages.test.mjs` (trang sản phẩm và đơn hàng đã là SSR thật).
6. **Seed** (`tools/seed/seed.mjs`): tên khung flash sale phải khớp giờ thật mà seed đặt (giờ Việt Nam `Asia/Ho_Chi_Minh`), thay vì tên cố định "12:00–18:00" trong khi khung tính theo "bây giờ". Chạy lại `make seed` hai lần vẫn không tạo trùng (khung cũ còn hiệu lực thì giữ).
7. **`web/CLAUDE.md`** mục "Hiện trạng dữ liệu và Hạ tầng" và "Bố cục `src/`": cập nhật theo thực tế mới (BFF, phiên cookie, `web/src/mock`, Dockerfile/compose/CI, `make seed`, `make e2e-web`). Ngắn, đúng sự thật.

## Tiêu chí nghiệm thu
- `rg -n "useOrders|OrdersContext|LegacyMockAddToCartInput|registerCartBridge" web/src` rỗng (hoặc giải thích từng chỗ còn lại).
- `cd web && npm run lint && npm run format:check && npm run check:data && NEXT_DIST_DIR=.next-cleanup npm run build` sạch, rồi hoàn nguyên `tsconfig.json` như mục 4 và xoá `.next-cleanup`.
- `NEXT_DIST_DIR=.next-cleanup GATEWAY_URL=http://localhost:38080 COOKIE_SECURE=false npx next start -p 3207` (build lại nếu cần): mở trang chủ, sản phẩm, giỏ, thanh toán, đơn hàng, tài khoản, `/seller`, `/admin` bằng tài khoản demo, không lỗi runtime/console; mở trợ lý AI và trang trợ giúp không lỗi. Tắt server khi xong.
- `make seed` chạy hai lần: lần hai 0 tạo mới; `curl -s localhost:38080/api/v1/flash-sales?include_upcoming=true` tên khung khớp giờ.
- Báo cáo: bảng file đã xoá/gộp và số dòng giảm (`git diff --stat` cho phần bạn làm).
