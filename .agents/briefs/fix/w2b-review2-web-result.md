# Báo cáo review độc lập — Wave 2b (`web/**`, `07_promotions.test.mjs`, `w2b-web.test.mjs`)

- **Phạm vi**: `web/**` (Next.js 15), [07_promotions.test.mjs](tests/e2e-web/07_promotions.test.mjs), [w2b-web.test.mjs](web/scripts/w2b-web.test.mjs) và đối chiếu cách gọi gateway với [docs/contracts.md](docs/contracts.md) + code backend thực tế.
- **Tình trạng lượt 1**: Đây là **lượt review độc lập đầu tiên cho `web`** (chưa có file `.agents/briefs/fix/w2b-review-*-result.md` cho phạm vi web, nên không có mục lượt 1 để gắn nhãn `ĐÃ SỬA / CHƯA SỬA / SỬA SAI`).
- **Kết quả kiểm tra tĩnh đã chạy (chỉ đọc, không sửa file, không chạy `npm run build`, không đụng container)**:
  - `npm run lint`: **PASS** (0 lỗi/cảnh báo)
  - `npx tsc --noEmit --incremental false`: **PASS** (0 lỗi)
  - `npm run format:check`: **PASS**
  - `npm run check:data`: **PASS** (15/15)
  - `node --test scripts/w2b-web.test.mjs`: **PASS** (16/16)

---

## 1. Phát hiện theo mức độ

### P0 — Lệch tiền thực tế / Rủi ro cộng tiền trùng

#### P0-1. Tổng tiền & phí ship hiển thị trên `/thanh-toan` có thể **thấp hơn** số tiền server thực thu khi đặt hàng (`weight_grams` mặc định lệch + `cod_amount_vnd` lệch)
- **Vị trí**: [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L322-L357), [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L483-L492), [thanh-toan/page.tsx](web/src/app/(shop)/thanh-toan/page.tsx#L308), [CartContext.tsx](web/src/context/CartContext.tsx#L226), đối chiếu [checkout.go](services/order/internal/service/checkout.go#L1094-L1148)
- **Kịch bản cụ thể**:
  1. Shop giữ mặc định `default_weight_grams = 500` (hoặc cấu hình `1000g` trong `/seller/ho-so`), có SKU để `weight_grams = 0` (hoặc `< 100g`, ví dụ phụ kiện `50g`). Người mua đặt `qty = 3`.
  2. Trên web, `CartContext` / `CheckoutClient` / Buy Now tính khối lượng `Math.max(100, weightGrams || 300) * 3 = 900g`, gọi `POST /shipping/calculate-fee` nhận phí mức `<=1000g` (ví dụ `22.000 ₫`) và hiển thị nút **"Đặt hàng · X + 22.000 ₫"**.
  3. Người mua bấm đặt -> `POST /checkout` trên server dùng `shop.default_weight_grams` (`500g` × 3 = `1500g`, hoặc `3000g` nếu shop đặt `1000g`), vượt ngưỡng 1000g của carrier (`mock_express`/`fallback`/`ghn`) nên tính phí cao hơn (`+4.000 ₫` đến `+16.000 ₫`) và **tạo đơn/mã VietQR thu số tiền cao hơn mức người mua vừa xác nhận** mà không cảnh báo hay hỏi lại.
  4. Tương tự với đơn COD có giảm giá: [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L490) gửi `cod_amount_vnd = slice.subtotalVnd` (đã trừ khuyến mãi), trong khi [checkout.go](services/order/internal/service/checkout.go#L1137) gửi `plan.subtotalVND` (trước giảm giá), gây lệch phí bảo hiểm trên GHN sandbox khi vượt bậc.
- **Nguyên nhân**: Frontend tự đoán fallback `300g` và ép sàn `100g` từng SKU thay vì dùng `shop.default_weight_grams` (mặc định server `500g`) và gửi `cod_amount_vnd` khác công thức server.
- **Đề xuất sửa (1 dòng)**: Trả `default_weight_grams` (hoặc khối lượng đã quy đổi theo shop) từ cart/shipping quote API để web gửi đúng `weight_grams` và cùng công thức `cod_amount_vnd` như `services/order/internal/service/checkout.go`.

#### P0-2. Cộng Xu thủ công ở Admin sinh `reference_id` mới theo `Date.now()` ở mỗi lần bấm -> bấm thử lại sau timeout có thể **cộng Xu gấp đôi**
- **Vị trí**: [AdminPromoCoinsPanel.tsx](web/src/features/admin/AdminPromoCoinsPanel.tsx#L439-L451)
- **Kịch bản cụ thể**: Admin để trống ô `"Mã tham chiếu (tuỳ chọn)"`, nhập cộng `500.000 Xu` (= 500.000 ₫ dùng được khi thanh toán) và bấm **"Cộng Xu"**. Nếu BFF proxy quá hạn 15 giây ([gw route.ts](web/src/app/api/gw/[...path]/route.ts#L20)) sau khi `promotion` đã ghi nhận giao dịch, UI hiện `"Hệ thống đang tạm bận, vui lòng thử lại"`. Admin bấm **"Cộng Xu"** lần 2 -> `admin-credit-${Date.now()}` tạo `reference_id` hoàn toàn mới -> server **cộng thêm 500.000 Xu lần nữa** (vô hiệu hóa cơ chế idempotent `already_credited`).
- **Nguyên nhân**: Sinh `reference_id` ngay trong handler `onSubmit` bằng `Date.now()` thay vì giữ nguyên khóa idempotency cho cả phiên submit đến khi thành công hoặc đổi dữ liệu đầu vào (khác với cách đã làm đúng ở `/admin/doi-soat`).
- **Đề xuất sửa (1 dòng)**: Lưu `pendingCreditRefId` trong state/ref (tạo 1 lần cho bộ `{userId, amount, reason}`), giữ nguyên khi retry lỗi và chỉ reset sau khi thành công hoặc khi admin đổi input.

---

### P1 — Hỏng luồng cốt lõi / Đường kẹt / Sai hiển thị tiền / Rò dữ liệu giữa phiên

#### P1-1. Kẹt vĩnh viễn ở `/thanh-toan` khi gặp `IDEMPOTENCY_KEY_CONFLICT` và sai hoàn toàn thông báo lỗi hết hàng (`INSUFFICIENT_STOCK`)
- **Vị trí**: [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L979-L993), [messages.ts](web/src/features/checkout/messages.ts#L127-L162), đối chiếu [response.go](services/gateway/internal/httpx/response.go#L94-L95) & [checkout.go](services/order/internal/service/checkout.go#L550-L667)
- **Kịch bản cụ thể**:
  1. Người mua bấm **"Đặt hàng"** với khóa `K1` -> gặp lỗi mạng/503 sau khi server đã tạo `checkout_group`, rồi sửa địa chỉ/voucher/phương thức và bấm đặt lại.
  2. `order` trả gRPC `codes.FailedPrecondition` (`"IDEMPOTENCY_KEY_CONFLICT: ..."`); gateway ánh xạ thành HTTP 409 với **`error.code = "FAILED_PRECONDITION"`** (gateway **không bao giờ** phát mã `"IDEMPOTENCY_KEY_CONFLICT"` hay `"INSUFFICIENT_STOCK"`).
  3. [error-map.ts](web/src/lib/server/error-map.ts#L266-L274) dịch `message` thành `"Yêu cầu đặt hàng trùng mã xử lý..."` (hoặc `"Sản phẩm không đủ số lượng tồn kho."`) nhưng vẫn giữ `code: "FAILED_PRECONDITION"`.
  4. [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L979) kiểm `err.code === "IDEMPOTENCY_KEY_CONFLICT"` -> **luôn `false`**, nên `checkoutIdempotencyKey` **không bao giờ được xoay**, kẹt người mua cho đến khi F5 toàn trang.
  5. Tiếp theo `translateCheckoutError` ([messages.ts](web/src/features/checkout/messages.ts#L127-L162)) cũng chỉ kiểm `err.code === "INSUFFICIENT_STOCK"` / `"IDEMPOTENCY_KEY_CONFLICT"` -> rơi xuống nhánh `FAILED_PRECONDITION` và **ghi đè** câu tiếng Việt đúng của `error-map.ts` bằng câu sai ngữ cảnh (ví dụ báo *"Suất Flash sale đã kết thúc"* khi thực tế là hết hàng hoặc trùng khóa!).
- **Nguyên nhân**: Frontend giả định gateway trả `code: "IDEMPOTENCY_KEY_CONFLICT"` / `"INSUFFICIENT_STOCK"`, lệch với `MapGRPCError` của gateway.
- **Đề xuất sửa (1 dòng)**: Chuẩn hóa `code` ngay trong `sanitizeGatewayError` (hoặc kiểm tra cả `err.message` đã dịch trong `CheckoutClient.tsx:979` và `translateCheckoutError`) trước nhánh `FAILED_PRECONDITION`.

#### P1-2. Vòng lặp ngắt/nối lại SSE vô hạn trong `RetryPaymentBox` làm vô hiệu polling dự phòng và spam `/api/auth/refresh`
- **Vị trí**: [RetryPaymentBox.tsx](web/src/features/commerce/RetryPaymentBox.tsx#L132-L208)
- **Kịch bản cụ thể**: Đang ở trang chờ thanh toán hoặc chi tiết đơn hàng VietQR `PENDING`, kết nối SSE bị ngắt (`source.onerror`) -> gọi `setSseDisconnected(true)` (`L181`/`L186`/`L189`). Vì `sseDisconnected` nằm trong dependency array của chính `useEffect` (`L208`), React lập tức đóng `EventSource`, xóa `pollInterval`, chạy lại effect, gọi ngay `setSseDisconnected(false)` (`L137`) -> lại chạy effect lần nữa và mở lại `EventSource` liên tục không ngừng, liên tục gọi `refreshSessionOnce()` và reset bộ đếm `setInterval` trước khi kịp poll.
- **Nguyên nhân**: Đưa `sseDisconnected` vào deps của `useEffect` vừa tạo `EventSource` vừa ghi `setSseDisconnected(false)`.
- **Đề xuất sửa (1 dòng)**: Tách `useEffect` mở SSE (chỉ phụ thuộc `activePaymentId`, `isPending`, `fetchPayment`) khỏi `useEffect` quản lý `pollInterval` (phụ thuộc `sseDisconnected`), hoặc dùng `ref` / backoff cho SSE reconnect.

#### P1-3. Bảng chi tiết tiền trên 3 trang đơn hàng/thanh toán không cộng khớp về tổng cuối
- **Vị trí**:
  - Người mua chi tiết đơn: [OrderDetailClient.tsx](web/src/features/account/OrderDetailClient.tsx#L2068-L2126)
  - Người bán chi tiết đơn: [seller/don-hang/[id]/page.tsx](web/src/app/(seller)/seller/don-hang/[id]/page.tsx#L562-L578), [seller/don-hang/[id]/page.tsx](web/src/app/(seller)/seller/don-hang/[id]/page.tsx#L641-L670)
  - Trang chờ thanh toán (nhánh fallback): [PaymentWaitingClient.tsx](web/src/app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient.tsx#L255)
- **Kịch bản cụ thể**:
  1. Đơn hàng có giảm giá chương trình gian hàng (`shop_program_discount_vnd = 30.000 ₫`) hoặc freeship gian hàng (`shop_freeship_discount_vnd = 15.000 ₫`): trang `/don-hang/[id]` của người mua bỏ qua 2 dòng này, khiến tổng các dòng hiển thị cao hơn `total_payable_vnd` đúng `45.000 ₫`.
  2. Trên `/seller/don-hang/[id]`, `computeOrderNetVnd` trừ đủ 6 khoản nhưng bảng đối soát chỉ liệt kê `subtotal_vnd`, `flash_sale_discount_vnd`, `shop_voucher_discount_vnd`, `commission_vnd` — bỏ sót `shop_program_discount_vnd`, `shop_freeship_discount_vnd` và `payment_fee_vnd` (xuất hiện trên mọi đơn VietQR!), khiến các dòng không cộng khớp về `"Thực nhận dự kiến"`.
  3. Trên `/thanh-toan/cho-thanh-toan/[paymentId]`, khi `initialOrders.length === 0` (chỉ có `payment.shop_allocations`), mỗi đơn hiển thị `alloc.subtotal_vnd + alloc.shipping_fee_vnd` (bỏ sót `- (alloc.platform_discount_vnd ?? 0)` theo [contracts.md](docs/contracts.md#L293)), khiến tổng các đơn lớn hơn `payment.amount_vnd`.
- **Nguyên nhân**: Giao diện chưa cập nhật đầy đủ các trường khấu trừ mới của Wave 2b và bỏ sót `platform_discount_vnd` ở nhánh fallback.
- **Đề xuất sửa (1 dòng)**: Bổ sung các dòng `shop_program_discount_vnd`, `shop_freeship_discount_vnd` (và `payment_fee_vnd` cho trang seller) vào 2 trang chi tiết đơn, và trừ `alloc.platform_discount_vnd ?? 0` tại `PaymentWaitingClient.tsx:255`.

#### P1-4. PDP và "Mua ngay" khóa/cắt số lượng theo `per_user_limit` của chương trình giảm giá shop (chặn mua giá thường), và hỏng giới hạn khi Flash `perUserLimit === 0`
- **Vị trí**: [ProductDetailClient.tsx](web/src/app/(shop)/san-pham/[slug]/ProductDetailClient.tsx#L235-L241), [thanh-toan/page.tsx](web/src/app/(shop)/thanh-toan/page.tsx#L280-L285)
- **Kịch bản cụ thể**:
  1. Theo đặc tả Wave 2b, vượt `per_user_limit` của chương trình giảm giá gian hàng **không được chặn mua** mà vẫn cho đặt với giá thường kèm cảnh báo. Tuy nhiên PDP kẹp `maxQty = Math.min(activeShopDiscount.per_user_limit, skuStock)` (không cho bấm `+` quá giới hạn), và `/thanh-toan?mode=buy_now&qty=3` âm thầm cắt `clampedQty` xuống `matchedDisc.per_user_limit` (ví dụ `1`).
  2. Ngoài ra tại [ProductDetailClient.tsx](web/src/app/(shop)/san-pham/[slug]/ProductDetailClient.tsx#L235-L240), nếu `activeFlashItem.perUserLimit === 0` (không giới hạn mỗi người, chỉ giới hạn `remainingSlots = 2`), `Math.min(0, 2, 20)` ra `0`, rồi `orderCap || skuStock` bật về `20` (bỏ qua luôn `remainingSlots`).
- **Nguyên nhân**: Áp nhầm quy tắc giới hạn số lượng của Flash Sale lên chương trình giảm giá shop và xử lý `perUserLimit = 0` sai bằng `Math.min` rồi `||`.
- **Đề xuất sửa (1 dòng)**: Bỏ kẹp `maxQty`/`maxBuyNowQty` theo `activeShopDiscount.per_user_limit` (chỉ hiển thị lưu ý vượt giới hạn sẽ tính giá thường) và chỉ đưa `perUserLimit` vào `Math.min` khi `> 0`.

#### P1-5. Kiểm tra số âm của Freeship gian hàng là dead code -> nhập số âm tự lưu thành `0 ₫` (miễn ship mọi đơn hàng)
- **Vị trí**: [seller/ho-so/page.tsx](web/src/app/(seller)/seller/ho-so/page.tsx#L244-L253)
- **Kịch bản cụ thể**: Seller bật Freeship gian hàng và nhập `-50000` vào ô "Đơn tối thiểu áp dụng Freeship". Dòng `244` chạy `Math.max(0, Math.trunc(Number(...) || 0))` biến nó thành `0`, khiến nhánh kiểm tra `if (freeShipEnabled && freeShipMinOrderVnd < 0)` ở dòng `250` **không bao giờ thực thi** và API lưu thẳng `free_ship_min_order_vnd: 0` ("Freeship cho mọi đơn hàng") -> shop bị trừ tiền hỗ trợ ship cho mọi đơn giá trị thấp.
- **Nguyên nhân**: Kẹp `Math.max(0, ...)` trước khi validate `< 0`.
- **Đề xuất sửa (1 dòng)**: Bỏ `Math.max(0, ...)` ở dòng `244` (parse thẳng `Number(freeShipMinOrderStr)` và kiểm tra `!Number.isFinite(...) || freeShipMinOrderVnd < 0` trước khi `Math.trunc`).

#### P1-6. Mobile 390px ở Admin ẩn trắng bảng dữ liệu trên 3 màn hình quản trị thật và hỏng duyệt tài khoản ngân hàng gian hàng
- **Vị trí**: [adminShared.module.css](web/src/features/admin/adminShared.module.css#L859-L861), [AdminProductModerationTab.tsx](web/src/features/admin/AdminProductModerationTab.tsx#L265), [AdminProductModerationTab.tsx](web/src/features/admin/AdminProductModerationTab.tsx#L400), [AdminUsersPanel.tsx](web/src/features/admin/AdminUsersPanel.tsx#L225), [AdminCategoriesPanel.tsx](web/src/features/admin/AdminCategoriesPanel.tsx#L277), [admin/gian-hang/page.tsx](web/src/app/(admin)/admin/gian-hang/page.tsx#L134-L141), [admin/gian-hang/page.tsx](web/src/app/(admin)/admin/gian-hang/page.tsx#L168-L174)
- **Kịch bản cụ thể**:
  1. Ở viewport `<=720px` (390px), `.tableWrap { display: none; }` ẩn toàn bộ bảng nhưng 3 màn hình **Kiểm duyệt sản phẩm** (cả danh sách sản phẩm lẫn bảng SKU), **Quản lý người dùng** và **Danh mục** không hề có `.mobileList` -> admin không xem/chọn được danh sách trên điện thoại.
  2. Trên `/admin/gian-hang` ở 390px, `activeDesktopShop` luôn fallback sang `filtered[0]`, nên `inspectedShopId = activeDesktopShop?.id ?? mobileOpenId` **không bao giờ lấy `mobileOpenId`**. Khi admin mở thẻ gian hàng thứ 2 trở đi (kể cả trong tab `"Chờ duyệt tài khoản ngân hàng"`), `loadShopBankAccount(shop.id)` không bao giờ được gọi và UI hiển thị sai *"Gian hàng chưa đăng ký tài khoản ngân hàng nhận giải ngân."*
- **Nguyên nhân**: CSS ẩn `.tableWrap` toàn cục mà thiếu `.mobileList` thay thế ở 3 panel; logic `inspectedShopId` ưu tiên `activeDesktopShop` luôn khác `null`.
- **Đề xuất sửa (1 dòng)**: Thêm `.mobileList` (hoặc chỉ ẩn `.tableWrap` khi có `.mobileList`) cho 3 panel trên, và đổi `admin/gian-hang` dùng `mobileOpenId ?? activeDesktopShop?.id` (hoặc đồng bộ `selectedId` khi mở thẻ mobile).

#### P1-7. Rò rỉ PII và trạng thái giỏ/cảnh báo giữa các phiên đăng nhập khác nhau trên cùng trình duyệt
- **Vị trí**: [MockStoreContext.tsx](web/src/mock/MockStoreContext.tsx#L34), [MockStoreContext.tsx](web/src/mock/MockStoreContext.tsx#L220-L267), [CartContext.tsx](web/src/context/CartContext.tsx#L176-L193), [SessionContext.tsx](web/src/context/SessionContext.tsx#L143-L150)
- **Kịch bản cụ thể**:
  1. Tài khoản A lưu thông báo có hàng (chứa SĐT/email), thông số cơ thể, thẻ quà tặng (chứa `recipientEmail`, lời nhắn), `eduEmail`, ngày sinh, danh sách yêu thích vào khóa `localStorage` dùng chung `"nexusmart_store_v2"`. Sau khi A đăng xuất và B đăng nhập trên cùng trình duyệt, toàn bộ dữ liệu cá nhân của A hiển thị trong phiên của B.
  2. `CartContext` không xóa `savedVoucherCodes` khi đăng xuất, và không reset voucher/xu/opt-out Flash khi đổi trực tiếp từ tài khoản A sang B (chỉ reset khi `user === null`).
  3. `SessionContext.logout()` không gọi `clearCheckoutWarnings()`, giữ nguyên `nm_checkout_warnings_v1` trong `sessionStorage` sang phiên kế tiếp.
- **Nguyên nhân**: Khóa storage không gắn theo `user.id` và luồng đăng xuất/đổi user chưa dọn sạch state client.
- **Đề xuất sửa (1 dòng)**: Xóa `nexusmart_store_v2`, `nm_checkout_warnings_v1`, `savedVoucherCodes` khi `logout()` và tách/reset state theo `user?.id` khi người dùng thay đổi.

#### P1-8. Checkout gửi `coins_to_use` cũ khi đang debounce preview; đơn giá dòng Flash không khớp tổng tiền khi hết lượt & cảnh báo nhầm sang SKU Flash khác cùng shop
- **Vị trí**: [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L906-L908), [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L1459-L1466), [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L1832), [gio-hang/page.tsx](web/src/app/(shop)/gio-hang/page.tsx#L909-L919), [helpers.ts](web/src/features/checkout/helpers.ts#L388-L401)
- **Kịch bản cụ thể**:
  1. Người mua bật dùng Xu, bỏ 1 voucher làm tăng số Xu có thể dùng rồi bấm **"Đặt hàng"** trong cửa sổ debounce 220ms (nút chỉ disable theo `shippingLoading`, không disable theo `previewLoading`). `effectiveCoins` lấy `preview.total_coins_discount_vnd` cũ (thấp hơn mức mới) gửi lên `POST /checkout`.
  2. Khi người mua đã hết `per_user_limit` Flash (`flashEval.previewShortfall === true`), tổng đơn ở cột phải tính giá thường theo `preview`, nhưng từng dòng sản phẩm vẫn hiển thị đơn giá Flash.
  3. Trong [helpers.ts](web/src/features/checkout/helpers.ts#L388-L401), `shopShortfall` so sánh tổng giảm Flash toàn shop nên nếu 1 shop có 2 SKU Flash và chỉ SKU #1 hết giới hạn, SKU #2 hợp lệ vẫn bị gán `previewShortfall = true` và hiện cảnh báo sai yêu cầu bỏ Flash.
- **Nguyên nhân**: Nút Đặt hàng không khóa theo `previewLoading` / dùng `preview.total_coins_discount_vnd` cũ; `evaluateShopFlashLines` tổng hợp theo shop thay vì theo từng `flash_sale_item_id`.
- **Đề xuất sửa (1 dòng)**: Disable nút Đặt hàng khi `previewLoading` (hoặc gửi `coinBalance` để server tự cap), đồng thời ẩn đơn giá Flash khi `previewShortfall` và đối chiếu `previewWarnings` theo đúng `flashSaleItemId` trước khi lan shortfall cho SKU khác.

#### P1-9. Tạo lại mã VietQR từ `/don-hang` hoặc `/don-hang/[id]` không cập nhật `payment_id` mới ở component cha
- **Vị trí**: [OrderDetailClient.tsx](web/src/features/account/OrderDetailClient.tsx#L880), [OrderDetailClient.tsx](web/src/features/account/OrderDetailClient.tsx#L940-L946), [OrdersListClient.tsx](web/src/features/account/OrdersListClient.tsx#L519-L526), [OrdersListClient.tsx](web/src/features/account/OrdersListClient.tsx#L557)
- **Kịch bản cụ thể**: Người mua tạo lại giao dịch thanh toán VietQR (retry) thành công trong `RetryPaymentBox` -> sinh `newPaymentId`, nhưng cha không truyền callback `onPaymentReplaced`, nên nút `"Mở trang chờ VietQR"` / `"Thanh toán VietQR"` trên thẻ đơn hàng vẫn trỏ về `/thanh-toan/cho-thanh-toan/<oldPaymentId>` (giao dịch đã bị hủy).
- **Nguyên nhân**: Thiếu prop `onPaymentReplaced` khi mount `RetryPaymentBox` trong danh sách và chi tiết đơn.
- **Đề xuất sửa (1 dòng)**: Truyền `onPaymentReplaced={(newId) => ...}` để cập nhật `payment_id` của đơn hiện tại trong state (hoặc reload đơn).

#### P1-10. Trang Voucher Admin cho phép Sửa/Tạm dừng/Xóa voucher `SHOP` (luôn lỗi 403) và hiển thị nhầm `SHOP` thành `PLATFORM` khi sửa
- **Vị trí**: [AdminPromoCoinsPanel.tsx](web/src/features/admin/AdminPromoCoinsPanel.tsx#L115), [AdminPromoCoinsPanel.tsx](web/src/features/admin/AdminPromoCoinsPanel.tsx#L214), [AdminPromoCoinsPanel.tsx](web/src/features/admin/AdminPromoCoinsPanel.tsx#L700-L760), đối chiếu [vouchers_w2b.go](services/promotion/internal/service/vouchers_w2b.go#L374-L377)
- **Kịch bản cụ thể**: `GET /admin/vouchers` trả về cả voucher scope `SHOP`. Admin bấm Sửa trên 1 voucher `SHOP` -> form ép `vScope` thành `"PLATFORM"`, khi lưu/tạm dừng/xóa đều thất bại HTTP 403 (`admins can only manage PLATFORM or FREESHIP vouchers`).
- **Nguyên nhân**: UI không kiểm tra `scope === "SHOP"` để ẩn thao tác sửa/xóa hoặc để chế độ chỉ xem.
- **Đề xuất sửa (1 dòng)**: Ẩn các nút Sửa/Tạm dừng/Xóa cho dòng có `v.scope === "SHOP"` và gắn nhãn "Voucher gian hàng (chỉ xem)".

#### P1-11. Xuất báo cáo CSV ở Seller và Admin lỗi 401 sau khi access token hết hạn 15 phút
- **Vị trí**: [seller/page.tsx](web/src/app/(seller)/seller/page.tsx#L178), [admin/page.tsx](web/src/app/(admin)/admin/page.tsx#L154)
- **Kịch bản cụ thể**: Seller hoặc Admin mở trang báo cáo quá 15 phút rồi bấm **"Xuất CSV"**. Hai hàm gọi `fetch("/api/gw/.../reports/export")` trực tiếp, bỏ qua cơ chế tự gọi `/api/auth/refresh` một lần rồi retry của `api()` ([lib/api.ts](web/src/lib/api.ts)), dẫn đến lỗi 401 dù refresh token vẫn còn hạn và các nút khác trên cùng trang hoạt động bình thường.
- **Nguyên nhân**: Dùng raw `fetch` thay vì helper hỗ trợ refresh 401 khi tải blob.
- **Đề xuất sửa (1 dòng)**: Dùng helper tải file qua `api`/refresh-retry khi gặp 401 giống cơ chế `refreshSessionOnce()` trong `web/src/lib/api.ts`.

---

### P2 — Sai đặc tả / Edge case / UX & Dữ liệu mẫu gây hiểu nhầm

#### P2-1. Đóng form sửa chương trình giảm giá gian hàng không xóa `editingId` -> mở "Tạo mới" lại ghi đè chương trình cũ
- **Vị trí**: [seller/khuyen-mai/page.tsx](web/src/app/(seller)/seller/khuyen-mai/page.tsx#L572), [PromotionsDiscountProgramsTab.tsx](web/src/features/seller/PromotionsDiscountProgramsTab.tsx#L62)
- **Kịch bản cụ thể**: Seller bấm "Sửa" chương trình A -> bấm nút tiêu đề `"Đóng biểu mẫu"` (`showProgramForm = false`, nhưng `editingId` vẫn là ID của A) -> bấm `"Tạo chương trình giảm giá"` -> form mở lại ở chế độ sửa A và gửi `PUT`, âm thầm ghi đè chương trình A thay vì `POST` tạo mới.
- **Đề xuất sửa (1 dòng)**: Reset `editingId = null` (hoặc gọi `resetForm()`) khi `showForm` chuyển từ `true` sang `false` hoặc mở bằng nút tạo mới.

#### P2-2. Đăng ký Flash Sale ở Seller chọn mặc định SKU chưa duyệt/ẩn/hết hàng và kiểm tra % giảm sai gốc `min_price_30d_vnd`
- **Vị trí**: [seller/khuyen-mai/page.tsx](web/src/app/(seller)/seller/khuyen-mai/page.tsx#L168), [seller/khuyen-mai/page.tsx](web/src/app/(seller)/seller/khuyen-mai/page.tsx#L471-L486), đối chiếu [flash_w2b.go](services/promotion/internal/service/flash_w2b.go#L58-L78)
- **Kịch bản cụ thể**: `fSkuId` khởi tạo từ `products` chưa lọc (có thể chọn nhầm SKU thuộc sản phẩm `HIDDEN`/`REJECTED` hoặc hết tồn kho), và client kiểm tra `min_discount_percent` theo `price_vnd` hiện tại thay vì `min_price_30d_vnd` của server (báo hợp lệ ở client rồi bị server từ chối).
- **Đề xuất sửa (1 dòng)**: Khởi tạo `fSkuId` từ danh sách `approvedProducts` có `available_stock > 0`, và ghi chú mức giảm tối thiểu tính trên giá thấp nhất 30 ngày.

#### P2-3. Kiểm tra điều kiện Voucher & ô nhập mã thủ công ở Giỏ hàng / Thanh toán chưa khớp server
- **Vị trí**: [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L446), [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L785), [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L824-L850), [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L1546-L1549), [gio-hang/page.tsx](web/src/app/(shop)/gio-hang/page.tsx#L257-L263), [gio-hang/page.tsx](web/src/app/(shop)/gio-hang/page.tsx#L588-L615)
- **Kịch bản cụ thể**:
  1. Nút chọn voucher gian hàng trên `/thanh-toan` chỉ so `slice.subtotalVnd >= v.min_order_vnd`, bỏ qua `v.eligible`, trạng thái `PAUSED`, chưa đến `starts_at`, hết `per_user_limit`/`budget_vnd`.
  2. `/gio-hang` gọi `GET /vouchers?order_amount_vnd` bằng tổng giá gốc trước giảm (`selectedSubtotalVnd`), đánh dấu đủ điều kiện cho mã mà tổng thực tế sau Flash/CT shop không đạt `min_order_vnd`.
  3. Nhập tay một mã không nằm sẵn trong danh sách (ví dụ mã freeship ẩn hoặc mã shop riêng): client chỉ thử gửi vào `platform_voucher_id`, không thử `freeship_voucher_id` hay `shop_voucher_ids` -> luôn báo lỗi không hợp lệ.
- **Đề xuất sửa (1 dòng)**: Dùng trạng thái `eligible`/`ineligible_reason` từ API cho nút voucher shop, truyền subtotal sau giảm vào `GET /vouchers`, và kiểm tra scope (hoặc hỗ trợ tra mã) khi áp mã nhập tay.

#### P2-4. `isFlashLimitOrSlotError` nhận nhầm lỗi 429 rate-limit hoặc `FAILED_PRECONDITION` không liên quan thành lỗi hết suất Flash Sale
- **Vị trí**: [helpers.ts](web/src/features/checkout/helpers.ts#L472-L473)
- **Kịch bản cụ thể**: Khi giỏ hàng có 1 sản phẩm Flash Sale, **bất kỳ** lỗi 429 (kể cả rate-limit IP của gateway) hoặc bất kỳ `FAILED_PRECONDITION` nào khác (hết lượt voucher, không có hãng vận chuyển, địa chỉ lỗi...) đều trả `true` -> UI bật `flashCheckoutBlocked` và xúi người mua bấm *"Bỏ suất Flash sale"* dù nguyên nhân không phải Flash Sale.
- **Đề xuất sửa (1 dòng)**: Chỉ trả `true` khi `msg` thực sự nhắc tới Flash Sale / suất mua / giới hạn mua (từ `error-map.ts:285`), không bắt tràn toàn bộ `429` và `FAILED_PRECONDITION`.

#### P2-5. `VietQrPreview` gọi `decodeURIComponent` 2 lần làm rơi về tài khoản ACB giả cứng, và fallback SVG vẽ nhiễu không quét được
- **Vị trí**: [VietQrPreview.tsx](web/src/features/commerce/VietQrPreview.tsx#L63-L75), [VietQrPreview.tsx](web/src/features/commerce/VietQrPreview.tsx#L90-L132)
- **Kịch bản cụ thể**: `URL.searchParams.get("accountName")` đã tự giải mã URL; gọi thêm `decodeURIComponent(...)` sẽ ném `URIError` nếu tên tài khoản chứa ký tự `%` -> khối `catch` rơi về thông tin giả cứng `"ACB · Ngân hàng Á Châu"` / `"2026 8841 9001"`. Nếu ảnh `qrUrl` lỗi mạng, fallback vẽ ma trận pixel ngẫu nhiên từ hash FNV-1a trông như QR thật nhưng **không thể quét bằng app ngân hàng**.
- **Đề xuất sửa (1 dòng)**: Bỏ `decodeURIComponent` thừa, không hiển thị số tài khoản giả khi parse lỗi, và thay fallback SVG giả bằng trạng thái lỗi tải ảnh QR + nút tải lại cùng thông tin chuyển khoản thật từ API.

#### P2-6. Tính năng Yêu thích (Wishlist) hỏng hoàn toàn với mọi sản phẩm thật từ backend
- **Vị trí**: [ProductCard.tsx](web/src/components/ProductCard.tsx#L295), [AccountMockTabs.tsx](web/src/features/account/AccountMockTabs.tsx#L327-L336)
- **Kịch bản cụ thể**: Người mua bấm tim trên sản phẩm thật -> lưu UUID backend vào wishlist, badge trên header tăng lên `1`, nhưng vào `/tai-khoan?tab=yeu-thich` thì `getProductById(entry.productId)` chỉ tìm trong mảng mock tĩnh `PRODUCTS` -> hiển thị trống `0 sản phẩm`.
- **Đề xuất sửa (1 dòng)**: Gắn nhãn rõ tab mẫu hoặc lưu kèm snapshot sản phẩm tối thiểu / fetch theo danh sách ID thật khi mở tab yêu thích.

#### P2-7. Khu vực dữ liệu mẫu thiếu nhãn `"Bản xem trước, dữ liệu mẫu"`, gắn vào luồng thật gây lỗi và còn chính sách giả "Đổi trả 45 ngày"
- **Vị trí**: [BuyerChatPanel.tsx](web/src/features/commerce/BuyerChatPanel.tsx#L17-L44), [AccountMockTabs.tsx](web/src/features/account/AccountMockTabs.tsx#L440-L623), [the-qua-tang/page.tsx](web/src/app/(shop)/the-qua-tang/page.tsx#L503), [ProductDetailClient.tsx](web/src/app/(shop)/san-pham/[slug]/ProductDetailClient.tsx#L965-L976), [AiAssistantPanel.tsx](web/src/features/commerce/AiAssistantPanel.tsx#L306), [seller/ho-so/page.tsx](web/src/app/(seller)/seller/ho-so/page.tsx#L1288-L1501)
- **Kịch bản cụ thể**:
  1. `BuyerChatPanel`, `AccountMockGiftCardsPanel`, `AccountMockReferralTab` không có banner `"Bản xem trước, dữ liệu mẫu"` (nhắn tin cho shop giả lập trả lời như thật).
  2. `/the-qua-tang` bấm dùng mã sinh viên ghi mã giả `EDU15` vào `CartContext` thật rồi chuyển sang `/gio-hang` (bị server từ chối); nút `"Áp vào giỏ"` trên thẻ quà tặng mẫu chuyển sang `/gio-hang` nhưng giỏ hàng không đọc `appliedGiftCardCode`.
  3. PDP, BuyerChat, AI Assistant và khối mẫu trên `/seller/ho-so` vẫn ghi *"Đổi trả miễn phí 45 ngày"*, mâu thuẫn với chính sách thật **7 ngày** ở trang chủ, chân trang và backend `services/order`.
  4. `/seller/ho-so` giữ nguyên khối mẫu *"Cấu hình hiển thị vận hành & cam kết (mẫu)"* có cả ô *"Ngưỡng freeship riêng của shop"* nằm ngay dưới form freeship thật.
- **Đề xuất sửa (1 dòng)**: Thêm banner `"Bản xem trước, dữ liệu mẫu"` cho 3 panel mẫu, ngừng ghi mã giả vào `CartContext`, sửa toàn bộ copy "45 ngày" -> "7 ngày", và gỡ khối cấu hình freeship mẫu trùng lặp trên `/seller/ho-so`.

#### P2-8. Cảnh báo chương trình giảm giá ở Seller hiển thị nguyên văn tiếng Anh + UUID thô từ backend
- **Vị trí**: [PromotionsDiscountProgramsTab.tsx](web/src/features/seller/PromotionsDiscountProgramsTab.tsx#L396-L400) (từ [shop_discounts.go](services/promotion/internal/service/shop_discounts.go#L444-L454))
- **Kịch bản cụ thể**: Khi seller tạo/sửa chương trình có SKU trùng lịch Flash Sale hoặc vượt giá gốc, UI hiển thị trực tiếp chuỗi tiếng Anh chứa UUID thô (`"SKU <uuid> overlaps active flash sale..."`).
- **Đề xuất sửa (1 dòng)**: Map `warnings` qua hàm dịch sang tiếng Việt kèm tên SKU/mã SKU thay vì render nguyên văn chuỗi backend.

#### P2-9. Thiếu chống race condition (request sequence / AbortController) ở các bộ lọc tải dữ liệu bất đồng bộ
- **Vị trí**: [seller/page.tsx](web/src/app/(seller)/seller/page.tsx#L124-L160), [admin/page.tsx](web/src/app/(admin)/admin/page.tsx#L106-L138), [seller/tai-chinh/page.tsx](web/src/app/(seller)/seller/tai-chinh/page.tsx#L118-L150), [CheckoutClient.tsx](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L470-L543)
- **Kịch bản cụ thể**: Đổi địa chỉ giao hàng hoặc chuyển COD/VietQR liên tiếp trên `/thanh-toan` (hoặc đổi khoảng ngày báo cáo/sổ cái): request trước trả về chậm hơn sẽ ghi đè kết quả mới nhất (`quotesByShop` sai địa chỉ/phương thức, báo cáo sai kỳ chọn).
- **Đề xuất sửa (1 dòng)**: Dùng `reqSeqRef` (như đã áp dụng cho `previewSeqRef` ở `CheckoutClient.tsx:186`) hoặc `AbortController` để bỏ qua response cũ.

#### P2-10. Nút Xóa/Kết thúc trên mobile ở Seller Khuyến mãi thực thi ngay lập tức không qua xác nhận như desktop
- **Vị trí**: [seller/khuyen-mai/page.tsx](web/src/app/(seller)/seller/khuyen-mai/page.tsx#L1155), [PromotionsDiscountProgramsTab.tsx](web/src/features/seller/PromotionsDiscountProgramsTab.tsx#L913)
- **Kịch bản cụ thể**: Ở màn hình desktop, bấm "Xóa"/"Kết thúc" mở hộp xác nhận 2 bước; ở màn hình 390px, chạm nhầm 1 lần lập tức gọi API xóa voucher / kết thúc chương trình giảm giá.
- **Đề xuất sửa (1 dòng)**: Tái sử dụng cùng trạng thái xác nhận inline `confirmDeleteId` / `confirmEndId` cho nút trên mobile card.

---

### P3 — Nhỏ (UI / A11y)

- **P3-1** ([ShopHeader.tsx](web/src/components/ShopHeader.tsx#L535-L536)): Gợi ý tìm kiếm nhanh trên header chỉ hiển thị `min_price_vnd`/`max_price_vnd` gốc, không phản ánh giá Flash Sale / chương trình giảm giá shop đang chạy.
- **P3-2** ([catalog.ts](web/src/lib/catalog.ts#L564-L601)): Trên PDP, `fetchActiveShopDiscounts({ shopId: rawProduct.shop_id })` chỉ tải ưu đãi của shop hiện tại nên danh sách `similarProducts` thuộc shop khác bị thiếu giá giảm của shop họ.
- **P3-3** (A11y & chi tiết mobile):
  - [AdminCarriersPanel.tsx](web/src/features/admin/AdminCarriersPanel.tsx#L439-L467): 3 `<label>` trong mobile card thiếu cặp `htmlFor`/`id`.
  - [seller/tra-hang/page.tsx](web/src/app/(seller)/seller/tra-hang/page.tsx#L436) và [admin/audit-log/page.tsx](web/src/app/(admin)/admin/audit-log/page.tsx#L276): `<div onClick>` / `<tr onClick>` không thao tác được bằng bàn phím (`tabIndex`, `role`, `onKeyDown`).
  - [admin/audit-log/page.tsx](web/src/app/(admin)/admin/audit-log/page.tsx#L338-L357): Thẻ mobile của Nhật ký kiểm toán không có nút mở rộng xem `before_json`/`after_json` như bảng desktop.

---

## 2. Nghi ngờ (chưa thể chứng minh tuyệt đối do không được chạy build/container/Playwright)

1. **Tràn ngang thực tế tại viewport 390px**: Đọc CSS tĩnh cho thấy các trang chính đã có breakpoint `<=720px`, nhưng ngoài lỗi ẩn mất bảng đã chứng minh ở **P1-6**, các chuỗi không tự ngắt (UUID, mã vận đơn dài, cụm nút hành động) trên `/thanh-toan`, `/gio-hang`, `/admin/doi-soat`, `/admin/khieu-nai`, `AdminFeesPanel` cần kiểm chứng thực tế bằng Playwright ở `390x844` với assertion `document.documentElement.scrollWidth <= window.innerWidth`.
2. **Biên dịch production Next.js 15 (`npm run build`)**: Chưa thể xác nhận không có lỗi bundler/Server-Client boundary hoặc dynamic route rendering khi build thật (cần chạy trong bước verify có quyền build).

---

## 3. Đánh giá chất lượng các test mới (test nào không chứng minh được gì)

### A. [web/scripts/w2b-web.test.mjs](web/scripts/w2b-web.test.mjs) (16 test)

- **Test không chứng minh được gì / dương tính giả (vẫn XANH dù lỗi còn nguyên)**:
  - **Test 14 (`L1244-1259`)** — phần kiểm tra freeship `min_order_vnd`: Chỉ regex mã nguồn tìm `freeShipEnabled && freeShipMinOrderVnd < 0` trong `seller/ho-so/page.tsx`, bỏ qua dòng `L244` đã kẹp `Math.max(0, ...)` khiến nhánh đó là **dead code** (**P1-5**). Test xanh hoàn toàn trong khi nhập `-50000` vẫn lưu thành `0 ₫`.
  - **Test 16 (`L1381-1458`)**: Test `isFlashLimitOrSlotError` và `sanitizeGatewayError` bằng dữ liệu tổng hợp `{ code: "INSUFFICIENT_STOCK" }` và `{ code: "IDEMPOTENCY_KEY_CONFLICT" }` mà **gateway không bao giờ trả về** (`MapGRPCError` luôn trả `FAILED_PRECONDITION`). Không test `translateCheckoutError` cũng không test nhánh xoay key ở `CheckoutClient.tsx:979` -> **bỏ lọt hoàn toàn P1-1**.
  - **Test 4 (`L431-435`), Test 5 (`L467-597`), Test 6 (`L599-669`), Test 7 (`L671-723`), Test 10 (`L908-992`), Test 12 (`L1098-1147`)**: Phần kiểm tra file bằng `readFileSync` + `.includes()` / regex chỉ xác nhận sự tồn tại chuỗi ký tự (có thể xanh cả khi code bị comment, gọi sai tham số hoặc hỏng runtime):
    - Test 7 (`L707-711`) chỉ đếm `shared.mobileList >= 4` trong `admin/doi-soat`, bỏ sót hoàn toàn 3 panel admin thiếu `mobileList` và lỗi `admin/gian-hang` (**P1-6**).
    - Test 5 (`L568-596`) và Test 6 (`L599-669`) không bắt được vòng lặp SSE vô hạn (**P1-2**), không bắt được lỗi 401 khi export CSV (**P1-11**), không bắt được thiếu 3 dòng đối soát trên chi tiết đơn (**P1-3**).
    - Test 10 (`L975-991`) kiểm nhầm wrapper SSR 20 dòng `app/(shop)/don-hang/[id]/page.tsx` thay vì `OrderDetailClient.tsx`; Test 16 (`L1464`) bổ sung `OrderDetailClient.tsx` nhưng chỉ kiểm `commission_vnd`, bỏ sót `payment_fee_vnd`.
- **Test có giá trị thực (thật sự ĐỎ khi bỏ logic helper)**:
  - Logic thuần trong Test 1 (`L110-184` `mapGatewayProduct`), Test 2 (`L200-366`), Test 3 (`L368-428`), Test 4 (`L437-464` `sanitizeGatewayError`), Test 8 (`L725-777`), Test 9 (`L796-882`), Test 11 (`L994-1072`), Test 13 (`L1149-1215`), Test 14 (`L1262-1305` `isTerminalBankOtpError`), Test 15 (`L1326-1364`). Tuy nhiên toàn bộ là unit test hàm tách rời, không kiểm chứng component React có dùng đúng kết quả hàm không (ví dụ `computeOrderNetVnd` đúng ở Test 3 nhưng UI `seller/don-hang/[id]` vẫn hiển thị thiếu 3 dòng).

### B. [tests/e2e-web/07_promotions.test.mjs](tests/e2e-web/07_promotions.test.mjs) (7 test)

- **Lỗ hổng bao phủ lớn nhất**: Ngoại trừ Test 1 (`L184-210`, tải trang 404/favicon/banner SSR) và 10 dòng check HTML PDP ở Test 2 (`L296-306`), **toàn bộ Tests 2–7 không chạy qua bất kỳ trang React UI nào** mà chỉ gọi thẳng HTTP `/api/gw/*` (tương đương E2E gateway + BFF proxy).
- **Những gì bộ test này KHÔNG chứng minh được về frontend**:
  - Test 2 (`L315`), Test 3 (`L484`), Test 6 (`L792`), Test 7 (`L969`) tự tạo SKU có `weight_grams: 250` (`L120`) và gửi thẳng `weightGrams` -> **không bao giờ chạm P0-1**.
  - Test 6 (`L801-909`) gọi thẳng `POST /api/gw/checkout` thay vì mua từ PDP / Buy Now -> **không bao giờ phát hiện P1-4** (UI chặn tăng số lượng / cắt `qty` theo `per_user_limit` của chương trình shop).
  - Test 7 (`L918-1155`) gọi thẳng API bỏ `flash_sale_item_id`, không test nút `"Mua giá thường"` hay đường xử lý lỗi của `CheckoutClient` -> **bỏ lọt P1-1, P1-8, P2-4**.
  - Test 4 (`L567-607`) test `Content-Disposition` ngay lúc vừa login -> **bỏ lọt P1-11** (lỗi 401 sau 15 phút).
  - Test 5 (`L610-746`) chỉ gọi API duyệt ngân hàng, không mở UI -> **bỏ lọt P1-6** (mobile `/admin/gian-hang` không tải được tài khoản ngân hàng của shop #2+).
  - Test 2 (`L299-306`) kiểm tra giá gạch ngang bằng `pdpRes.text.includes(formatVndDots(regularPrice))` trên toàn bộ HTML (kể cả JSON hydration / sản phẩm gợi ý khác) nên dễ dương tính giả nếu thẻ giá gạch ngang bị vỡ.

---

## 4. Ponytail Review (code chết & thừa cần cắt)

- [PromotionsBundleTab.tsx](web/src/features/seller/PromotionsBundleTab.tsx#L1-L823): `dead-code` toàn bộ file 823 dòng không còn bất kỳ import nào sau khi tab Combo mẫu được thay bằng `PromotionsDiscountProgramsTab` -> xóa file.
- [seller/ho-so/page.tsx](web/src/app/(seller)/seller/ho-so/page.tsx#L1288-L1501): `duplicate-feature` khối "Cấu hình hiển thị vận hành & cam kết (mẫu)" lặp lại cài đặt freeship + chính sách 45 ngày giả ngay dưới form freeship thật -> xóa toàn bộ khối mẫu và state giả đi kèm.
- [sellerStore.ts](web/src/features/seller/sellerStore.ts#L90-L122): `dead-code` `BANK_ACCOUNTS` và `getShopBankAccount` không còn nơi gọi sau khi `/seller/tai-chinh` dùng API thật -> xóa.
- [search.ts](web/src/lib/search.ts#L212-L240): `dead-code` `InstantSearchResult` và `getInstantSearchSuggestions` không còn nơi gọi sau khi `ShopHeader` chuyển sang tìm kiếm thật `/products?keyword=` -> xóa.
- [MockStoreContext.tsx](web/src/mock/MockStoreContext.tsx#L535-L554): `write-only-state` `appliedGiftCardCode` (và [membership.ts](web/src/data/membership.ts#L73)) chỉ ghi ở trang mẫu, không bao giờ được giỏ hàng/thanh toán đọc -> xóa.
- [AdminFeesPanel.tsx](web/src/features/admin/AdminFeesPanel.tsx#L69-L88): `speculative-fallback` đọc khóa ngoài hợp đồng (`items?`, `configs?`, `schedules?` ở [gateway-types.ts](web/src/lib/gateway-types.ts#L982-L983)) trong khi gateway luôn trả cố định `categories` và `payment_fees` -> đọc đúng khóa hợp đồng.
- [seller/ho-so/page.tsx](web/src/app/(seller)/seller/ho-so/page.tsx#L250-L253): `dead-branch` nhánh kiểm tra `freeShipMinOrderVnd < 0` bị vô hiệu bởi `Math.max(0, ...)` ở `L244` -> bỏ `Math.max(0, ...)` để nhánh này chạy thật (sửa **P1-5**).

`net: -1137 lines possible.`

---

## 5. Các phần đã kiểm tra và đạt (không phát hiện lỗi)

- **Bảo mật BFF proxy** ([gw route.ts](web/src/app/api/gw/[...path]/route.ts), [proxy-security.ts](web/src/lib/server/proxy-security.ts), [csrf.ts](web/src/lib/server/csrf.ts), [session.ts](web/src/lib/server/session.ts)): Chặn root `webhooks`/`auth`, chỉ forward allowlist header, đính `Authorization` duy nhất từ HttpOnly cookie, không lộ token ra response, giới hạn body 2 MiB, ép `X-Content-Type-Options: nosniff` + fallback sandbox CSP cho HTML, `redirect: "manual"`, che chi tiết lỗi 5xx, forward `Content-Disposition` đúng cho tải CSV.
- **Phân quyền RBAC phía server** ([middleware.ts](web/src/middleware.ts), [auth.ts](web/src/lib/server/auth.ts), [(seller)/layout.tsx](web/src/app/(seller)/layout.tsx), [(admin)/layout.tsx](web/src/app/(admin)/layout.tsx)): Xác minh `role` từ `GET /users/me` trên gateway (không tin raw JWT), trả 404 khi sai vai trò, không xoay refresh token trên request prefetch.
- **Không lộ phí sàn cho người mua**: Quét toàn bộ `(shop)`, `account`, `checkout`, `commerce`, `catalog`, `components` — không tham chiếu hay hiển thị `commission_*` hay `payment_fee_*`.
- **Chống XSS / CSV Injection**: Không có `dangerouslySetInnerHTML`, `innerHTML`, `window.open`, `javascript:` hay sinh CSV phía trình duyệt (CSV do gateway tạo kèm `sanitizeCSVCell` và BOM); link bằng chứng được kiểm `http:`/`https:` qua `parseSafeEvidenceUrl`.
- **404 thật & Favicon** ([not-found.tsx](web/src/app/not-found.tsx), `icon.svg`, `favicon.ico`, [bo-suu-tap/[slug]/page.tsx](web/src/app/(shop)/bo-suu-tap/[slug]/page.tsx), [tap-chi/[slug]/page.tsx](web/src/app/(shop)/tap-chi/[slug]/page.tsx), [chien-dich/[slug]/page.tsx](web/src/app/(shop)/chien-dich/[slug]/page.tsx)): Gọi đúng `notFound()` khi slug không tồn tại.
- **Đối soát & Hoàn tiền Admin** ([admin/doi-soat/page.tsx](web/src/app/(admin)/admin/doi-soat/page.tsx)): Xử lý `Idempotency-Key` đúng (giữ khóa khi retry lỗi, chỉ xoay khi mở mới hoặc sau thành công), đầy đủ thẻ mobile `.mobileList` cho bảng sổ cái và hàng đợi.

