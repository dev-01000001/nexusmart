Báo cáo review độc lập đợt 2b (chỉ đọc trực tiếp từ `git diff d88221d`, các file untracked và toàn bộ code liên quan).

---

## Tổng quan kết quả

- **P0:** 1 phát hiện (bỏ qua cơ chế kiểm duyệt admin cho tài khoản ngân hàng payout của shop).
- **P1:** 3 phát hiện (rò rỉ phí sàn cho buyer qua REST thanh toán + rò rỉ tài chính chéo shop qua gRPC; thiếu kiểm tra quyền admin ở `GetFeeConfig`; tin dữ liệu tài chính từ client trên gRPC `ReservePromotion`/`PreviewDiscounts`).
- **P2:** 7 phát hiện (lộ phí sàn ở tầng gRPC `OrderService` và REST public catalog; `GetSKUsBatch` bỏ qua trạng thái sản phẩm ẩn/xóa; CSV injection sót `\n`; mất trạng thái `PENDING_ADMIN_REVIEW` + đốt rate-limit khi lỗi publish OTP; trạng thái zombie `PENDING_OTP` & thiếu check `owner.Status == ACTIVE`; job `voucher_expiring` báo sai cho người đã dùng hết lượt/hết ngân sách; sai lệch contract giữa `docs/contracts.md`, gateway, shipping và web).
- **P3:** 3 phát hiện (biên `handlePreviewDiscounts`, redaction quá tay ở `audit.go` + `bank_review_pending`, thiếu `RateLimitAuth` ở gateway cho route OTP ngân hàng).

---

## Chi tiết phát hiện theo mức độ

### P0 — Bảo mật / Bỏ qua chốt kiểm soát tài chính

#### 1. Bỏ qua hoàn toàn kiểm duyệt admin (`PENDING_ADMIN_REVIEW`) khi gắn tài khoản ngân hàng khác tên chủ shop (UC-S17)
- **Vị trí:** [bank_account.go:274](services/user/internal/service/bank_account.go#L274), [bank_account.go:447-465](services/user/internal/service/bank_account.go#L447-L465), [server.go:848-899](services/user/internal/service/server.go#L848-L899)
- **Kịch bản:** `AddShopBankAccount` và `VerifyShopBankAccountOTP` xác định `holder_name_matched` (để cho phép tự động `VERIFIED` mà không qua hàng chờ duyệt của admin) chỉ bằng cách so sánh `account_holder_name` với `users.full_name` (`owner.FullName`). Trong khi đó, `PATCH /api/v1/users/me` (`UpdateProfile`) cho phép chủ shop tự đổi `users.full_name` bất kỳ lúc nào mà không cần xác minh hay duyệt lại. Một seller muốn gắn tài khoản ngân hàng của bên thứ ba chỉ cần gọi `PATCH /api/v1/users/me` đổi `full_name` trùng tên chủ tài khoản bên thứ ba (thậm chí đổi sau khi đã gửi đơn, ngay khi đang `PENDING_OTP` vì dòng 455 đọc lại `owner.FullName`), rồi xác thực OTP → `holder_name_matched = true` → tài khoản nhận payout chuyển thẳng sang `VERIFIED`, vô hiệu hóa hoàn toàn bước `PENDING_ADMIN_REVIEW`.
- **Mức độ:** `P0`
- **Gợi ý sửa:** Lưu snapshot tên chủ pháp lý/chủ shop bất biến tại thời điểm admin duyệt shop (`shops.verified_owner_name`) để đối chiếu `holder_name_matched` thay vì so với cột `users.full_name` có thể tự sửa.

---

### P1 — Rò rỉ phí sàn / Dữ liệu chéo shop / Phân quyền RPC / Tin dữ liệu client

#### 2. Rò rỉ phí sàn (`commission_vnd`) cho buyer qua REST thanh toán và rò rỉ tài chính chéo gian hàng cho seller qua `PaymentService.GetPayment`
- **Vị trí:** [handlers_payment.go:143-175](services/gateway/internal/router/handlers_payment.go#L143-L175), [handlers_payment.go:310-332](services/gateway/internal/router/handlers_payment.go#L310-L332), [handlers_payment.go:377-386](services/gateway/internal/router/handlers_payment.go#L377-L386), [grpc.rs:287-291](services/payment/src/grpc.rs#L287-L291), [grpc.rs:1065-1076](services/payment/src/grpc.rs#L1065-L1076), [auth.rs:56-63](services/payment/src/auth.rs#L56-L63)
- **Kịch bản:**
  1. Dù gateway đã ẩn `commission_vnd`, `payment_fee_vnd`, `payment_fee_bps`, `commission_bps` cho buyer ở `mapOrderView` ([handlers_order.go:172-221](services/gateway/internal/router/handlers_order.go#L172-L221)), chính buyer đó gọi `GET /api/v1/payments/{id}` hoặc `POST /api/v1/payments/{id}/retry` vẫn nhận đầy đủ `shop_allocations[].commission_vnd` (tổng phí danh mục + phí thanh toán `C_cat + C_pay`), `subtotal_vnd` (`R`) và `platform_discount_vnd` (`D`).
  2. Trên gRPC `PaymentService.GetPayment`: với một checkout gộp 2 gian hàng (Shop A + Shop B chung `payment_id`), Seller A gọi `GetPayment` vượt qua `require_payment_read_access` nhờ `owns_shop_alloc == true`, nhưng `to_proto_payment(found)` trả toàn bộ `shop_allocations` gồm cả `order_id`, `shop_id`, `subtotal_vnd`, `commission_vnd`, `shipping_fee_vnd`, `platform_discount_vnd` của **Shop B**.
- **Mức độ:** `P1`
- **Gợi ý sửa:** Lọc `shop_allocations` theo caller ngay trong `to_proto_payment` (và `mapPayment`): ẩn/xóa `commission_vnd` (hoặc lược bỏ `shop_allocations`) với buyer, và chỉ trả allocation có `a.shop_id == caller.shop_id` với seller.

#### 3. Mọi buyer/seller gọi trực tiếp gRPC `PaymentService.GetFeeConfig` đều đọc được biểu phí thanh toán hiện tại, lịch phí tương lai và ghi chú admin
- **Vị trí:** [grpc.rs:761-811](services/payment/src/grpc.rs#L761-L811), [service.rs:299-316](services/payment/src/service.rs#L299-L316)
- **Kịch bản:** `get_fee_config` chỉ kiểm tra `caller.require_admin(...)` khi `req.include_history == true` (dòng 772–774). Khi gọi với `include_history: false`, bất kỳ JWT buyer/seller nào cũng đọc được biểu phí thanh toán và thậm chí truyền `at_time` trong tương lai để dò các lịch thay đổi phí chưa công bố cùng `created_by` (ID admin) và `note` nội bộ. Trong khi đó `order` gọi RPC này bằng service token (`is_admin() == true`) còn REST chỉ mở cho admin.
- **Mức độ:** `P1`
- **Gợi ý sửa:** Gọi `caller.require_admin("GetFeeConfig")?` vô điều kiện ở đầu `get_fee_config` (cho phép admin JWT và service token, chặn buyer/seller).

#### 4. `PromotionService.ReservePromotion` và `PreviewDiscounts` tin giá trị tài chính do client truyền trên gRPC bằng JWT buyer
- **Vị trí:** [auth.go:97-98](pkg/common/auth/auth.go#L97-L98), [promotion.go:1394-1470](services/promotion/internal/service/promotion.go#L1394-L1470)
- **Kịch bản:** `ReservePromotion` và `PreviewDiscounts` chấp nhận end-user JWT (`service_token = false`), và `buildCalcInput` sử dụng trực tiếp `ps.SubtotalVnd`, `ps.ShippingFeeVnd`, `ps.FreeShip*`, `pi.UnitPriceVnd` (và `pi.CategoryId` nếu không có platform voucher theo danh mục) từ request. Một buyer gọi thẳng gRPC `ReservePromotion` với `order_id` tùy chọn và `subtotal_vnd`/`unit_price_vnd` bơm khống sẽ vượt qua ngưỡng `min_order_vnd`, trừ thật quota/ngân sách voucher và slot flash sale trong Redis + Postgres mà không cần tạo đơn hàng. *(Qua REST gateway `/api/v1/promotions/preview`, gateway đã tự tra SKU/discount/freeship, chỉ còn tin `shipping_fee_vnd` cho bản xem trước.)*
- **Mức độ:** `P1`
- **Gợi ý sửa:** Chuyển `ReservePromotion` sang `service_token = true` và từ chối end-user JWT trong handler (vì chỉ `order` được giữ chỗ khuyến mãi), đồng thời tra giá SKU từ `productClient` trong `PreviewDiscounts`.

---

### P2 — Phòng thủ theo chiều sâu / Lỗi biên nghiệp vụ / Sai lệch đặc tả

#### 5. Lộ phí sàn cho buyer ở tầng gRPC `OrderService` và công khai `commission_rate_bps` trên REST `GET /api/v1/products*` & `GET /api/v1/categories`
- **Vị trí:** [service.go:311-377](services/order/internal/service/service.go#L311-L377), [handlers_product.go:101-145](services/gateway/internal/router/handlers_product.go#L101-L145), [server.go:382-392](services/product/internal/server/server.go#L382-L392)
- **Kịch bản:**
  1. `OrderService.aggregateToProtoForCaller` chỉ chặn `NeedsManualReview` theo `isAdmin`, vẫn trả nguyên `CommissionVnd`, `PaymentFeeVnd`, `PaymentFeeBps` và `Items[].CommissionBps` cho buyer qua gRPC (chỉ lớp gateway ẩn đi).
  2. Các endpoint công khai `GET /api/v1/products`, `GET /api/v1/products/{id}`, `GET /api/v1/products/slug/{slug}` (qua `mapSKU`) và `GET /api/v1/categories` (qua `mapCategory`) trả công khai `commission_rate_bps` của sàn cho khách vãng lai/buyer. *(Lưu ý: UI seller/admin đang cần `commission_rate_bps` ở `GET /api/v1/categories`, nhưng storefront `GET /api/v1/products*` không dùng cho buyer.)*
- **Mức độ:** `P2`
- **Gợi ý sửa:** Xóa về `0` các trường phí trong `aggregateToProtoForCaller` khi caller là buyer; ẩn `commission_rate_bps` trên các route `GET /api/v1/products*` công khai (chỉ trả trên route seller/admin).

#### 6. `ProductService.GetSKUsBatch` công khai không lọc sản phẩm chưa duyệt / bị ẩn / đã xóa mềm
- **Vị trí:** [auth.go:62](pkg/common/auth/auth.go#L62), [server.go:1223-1256](services/product/internal/server/server.go#L1223-L1256), [product.sql:453-495](services/product/queries/product.sql#L453-L495)
- **Kịch bản:** `GetSKUsBatch` là public RPC (không yêu cầu JWT), truy vấn `WHERE s.id = ANY($sku_ids)` không kiểm tra `p.deleted_at IS NULL`, `s.status <> 'INACTIVE'` hay `p.status IN ('APPROVED', 'OUT_OF_STOCK')`. Bất kỳ ai biết `sku_id` đều đọc được SKU của sản phẩm `PENDING`/`REJECTED`/`HIDDEN`/đã xóa mềm (kèm `stock`, `min_price_30d_vnd`, `commission_rate_bps`), và `POST /api/v1/promotions/preview` cũng tính giảm giá cho cả SKU không còn mở bán.
- **Mức độ:** `P2`
- **Gợi ý sửa:** Với caller không phải admin/chủ shop (hoặc tại `handlePreviewDiscounts`), chỉ trả/chấp nhận SKU active thuộc sản phẩm chưa xóa ở trạng thái `APPROVED`/`OUT_OF_STOCK`.

#### 7. Bảo vệ CSV injection ở `sanitizeCSVCell` bỏ sót ký tự xuống dòng `\n` (`0x0A`) và khoảng trắng đầu chuỗi
- **Vị trí:** [handlers_order.go:1344-1357](services/gateway/internal/router/handlers_order.go#L1344-L1357)
- **Kịch bản:** `sanitizeCSVCell` chỉ kiểm tra `v[0]` thuộc `'='`, `'+'`, `'-'`, `'@'`, `'\t'`, `'\r'`, bỏ sót `'\n'` (và trường hợp có khoảng trắng dẫn trước công thức). Nếu seller đặt tên sản phẩm bắt đầu bằng `\n=HYPERLINK(...)`, giá trị xuất vào cột `label` của dòng `top_product` trong `GET /api/v1/admin/reports/export` và `/api/v1/seller/reports/export` sẽ không được thêm dấu `'`.
- **Mức độ:** `P2`
- **Gợi ý sửa:** Kiểm tra thêm `'\n'` và kiểm tra ký tự đầu tiên sau `strings.TrimLeft(v, " \t\r\n")` trong `sanitizeCSVCell`.

#### 8. Lỗi publish `user.shop_bank_account_otp` sau khi commit DB làm mất bản ghi `PENDING_ADMIN_REVIEW` cũ và đốt lượt rate-limit
- **Vị trí:** [bank_account.go:256-346](services/user/internal/service/bank_account.go#L256-L346)
- **Kịch bản:** Shop đang có tài khoản ở `PENDING_ADMIN_REVIEW`. Seller gửi tài khoản mới đúng lúc NATS gián đoạn: transaction DB (dòng 267–311) đã commit việc `SupersedeNonVerifiedShopBankAccounts` bản ghi cũ và tạo bản ghi `PENDING_OTP` mới. Sau đó `PublishShopBankAccountOTP` (dòng 320) lỗi → nhánh bù trừ dòng 335 chỉ supersede bản ghi mới và trả `500`, không phục hồi bản ghi `PENDING_ADMIN_REVIEW` trước đó và vẫn tính 1 lượt vào giới hạn 5 lần/giờ (`CountShopBankAccountsCreatedSince`).
- **Mức độ:** `P2`
- **Gợi ý sửa:** Chuyển phát sự kiện sang outbox trong cùng transaction (xóa OTP ngay sau khi publish) hoặc tối thiểu không tính bản ghi bị hủy do lỗi publish vào `CountShopBankAccountsCreatedSince` và chỉ supersede `PENDING_ADMIN_REVIEW` khi OTP đã gửi/xác thực.

#### 9. Tài khoản ngân hàng kẹt trạng thái zombie `PENDING_OTP` sau khi OTP hết hạn/sai 5 lần, và `VerifyShopBankAccountOTP` thiếu kiểm tra `owner.Status == ACTIVE`
- **Vị trí:** [bank_account.go:402-454](services/user/internal/service/bank_account.go#L402-L454)
- **Kịch bản:**
  1. Khi OTP hết hạn (dòng 407–420) hoặc nhập sai đủ 5 lần (dòng 428–444), `RecordShopBankAccountOTPFailure` xóa `otp_hash = ""` và `otp_expires_at = NULL`, nhưng giữ `status = 'PENDING_OTP'`. `GET /api/v1/seller/bank-account` tiếp tục trả `status: "PENDING_OTP"` dù không thể xác thực được nữa.
  2. Khác với `AddShopBankAccount` (dòng 252), `VerifyShopBankAccountOTP` (dòng 447–454) không kiểm tra `owner.Status != StatusActive`, nên chủ shop vừa bị admin `LOCKED` vẫn xác thực xong tài khoản nhận tiền bằng access token còn hạn.
- **Mức độ:** `P2`
- **Gợi ý sửa:** Đổi trạng thái bản ghi sang `SUPERSEDED` (hoặc trạng thái hết hạn riêng) khi OTP hết hạn/vượt 5 lần sai, và thêm kiểm tra `owner.Status != StatusActive` ngay sau dòng 453.

#### 10. `ProcessExpiringSavedVouchers` vẫn phát `promotion.voucher_expiring` cho user đã dùng hết `per_user_limit` hoặc voucher đã cạn `budget_vnd`
- **Vị trí:** [store.go:480-493](services/promotion/internal/store/store.go#L480-L493)
- **Kịch bản:** Câu query lấy danh sách gửi nhắc sắp hết hạn chỉ kiểm tra `v.used_count < v.quota`, không kiểm tra ngân sách còn lại `(v.budget_vnd = 0 OR v.used_budget_vnd < v.budget_vnd)` và không kiểm tra số lần `uv.user_id` đã sử dụng voucher này so với `v.per_user_limit`. Người mua đã lưu và đã dùng xong voucher `per_user_limit = 1` vẫn nhận thông báo in-app + email nhắc dùng voucher trước 24h hết hạn.
- **Mức độ:** `P2`
- **Gợi ý sửa:** Bổ sung điều kiện kiểm tra ngân sách còn lại và số lượt sử dụng thực tế của `(uv.user_id, uv.voucher_id) < v.per_user_limit` vào `WHERE` của `ProcessExpiringSavedVouchers`.

#### 11. Sai lệch contract `docs/contracts.md` ↔ gateway ↔ shipping ↔ web trên `promotions/preview`, `checkout` và `seller/shipping-settings`
- **Vị trí:** [contracts.md:221](docs/contracts.md#L221), [contracts.md:226](docs/contracts.md#L226), [handlers_promotion.go:1311-1326](services/gateway/internal/router/handlers_promotion.go#L1311-L1326), [shipping.go:1134-1136](services/shipping/internal/service/shipping.go#L1134-L1136), [shipping.go:1169-1188](services/shipping/internal/service/shipping.go#L1169-L1188), [ho-so/page.tsx:250-253](web/src/app/(seller)/seller/ho-so/page.tsx#L250-L253)
- **Kịch bản:**
  1. `docs/contracts.md:221` mô tả request REST `POST /api/v1/promotions/preview` có `subtotal_vnd`, `free_ship_*`, `unit_price_vnd`, `category_id`, `shop_discount_program_item_id`, nhưng `handlePreviewDiscounts` hoàn toàn không đọc các trường này (chỉ giải mã `shop_id`, `shipping_fee_vnd`, `items[{sku_id, quantity, flash_sale_item_id}]` và tự tra cứu từ backend). Ngược lại, `docs/contracts.md:226` lại thiếu `shop_program_item_id` trong request REST `POST /api/v1/checkout` dù `handlers_order.go:481` và web có gửi.
  2. Trên web [ho-so/page.tsx:250-253](web/src/app/(seller)/seller/ho-so/page.tsx#L250-L253), client kiểm tra `freeShipMinOrderVnd <= 0` (chặn trường hợp freeship cho mọi đơn `min_order_vnd = 0` mà backend cho phép) nhưng lại không kiểm tra `freeShipMaxVnd <= 0` (backend [shipping.go:1134](services/shipping/internal/service/shipping.go#L1134) bắt buộc `> 0`, chỉ lỗi sau khi gửi request).
  3. Trong [shipping.go:1169-1179](services/shipping/internal/service/shipping.go#L1169-L1179), `UpdateShopShippingSettings` bảo toàn `DefaultWeightGrams` cũ khi đầu vào bằng `0`, nhưng không bảo toàn `DefaultLengthCM`/`DefaultWidthCM`/`DefaultHeightCM` → client gọi API bỏ qua 3 trường kích thước sẽ ghi đè thành `0`.
- **Mức độ:** `P2`
- **Gợi ý sửa:** Cập nhật [contracts.md:221,226](docs/contracts.md#L221) khớp đúng schema REST; đổi check ở `ho-so/page.tsx:250` sang `freeShipMaxVnd <= 0`; và bảo toàn `length/width/height` hiện tại (hoặc mặc định) khi input bằng `0` trong `shipping.go:1173`.

---

### P3 — Nhỏ / Biên

#### 12. Biên xử lý trong `handlePreviewDiscounts` (>200 SKU, tràn số `int64`, ghi đè map discount)
- **Vị trí:** [handlers_promotion.go:1368-1403](services/gateway/internal/router/handlers_promotion.go#L1368-L1403), [handlers_promotion.go:1443](services/gateway/internal/router/handlers_promotion.go#L1443)
- **Kịch bản:** Request gửi >200 SKU khác nhau bị `ListActiveShopDiscounts` từ chối 400 (giới hạn `maxActiveShopDiscountSKUs = 200`); phép nhân `unitPrice * int64(it.Quantity)` ở dòng 1443 không kiểm tra tràn số `int64` nếu `quantity` rất lớn; dòng 1401 ghi đè `activeDiscountBySKU[skuID]` theo phần tử cuối thay vì chọn giá thấp nhất nếu có nhiều bản ghi.
- **Mức độ:** `P3`
- **Gợi ý sửa:** Giới hạn số lượng SKU/item và `quantity` hợp lý ngay khi validate body trong `handlePreviewDiscounts`, kiểm tra tràn số `int64`, và chọn discount có `discounted_price_vnd` nhỏ nhất.

#### 13. `audit.go` ẩn nhầm `bank_account_name` trong audit log và `AdminReviewShop` luôn trả `bank_review_pending: false`
- **Vị trí:** [audit.go:394-417](services/gateway/internal/middleware/audit.go#L394-L417), [server.go:1507](services/user/internal/service/server.go#L1507), [handlers_user.go:1015](services/gateway/internal/router/handlers_user.go#L1015)
- **Kịch bản:** Chuỗi con `"bank_account"` trong `auditSensitiveSubstrings` khiến trường không nhạy cảm `"bank_account_name"` (tên người nhận hoàn tiền) bị thay bằng `[REDACTED]` trong audit log. Ngoài ra `AdminReviewShop` trả `toProtoShop(updated)` (luôn có `BankReviewPending: false`) thay vì `toAdminProtoShop`.
- **Mức độ:** `P3`
- **Gợi ý sửa:** Thêm `"bank_account_name"` vào whitelist tại `isSensitiveAuditKey` (`audit.go:408`) và đồng nhất mapper trả về của `AdminReviewShop`.

#### 14. Route OTP tài khoản ngân hàng shop chưa gắn `RateLimitAuth` tại gateway
- **Vị trí:** [router.go:248-250](services/gateway/internal/router/router.go#L248-L250)
- **Kịch bản:** Dù `services/user` có chặn 5 lần/giờ và 5 lần sai OTP trong Postgres, `POST /api/v1/seller/bank-account` và `POST /api/v1/seller/bank-account/verify-otp` ở gateway chưa đi qua `RateLimitAuth`, nên lưu lượng brute-force hoặc spam request vẫn đánh trực tiếp vào DB `user`.
- **Mức độ:** `P3`
- **Gợi ý sửa:** Gắn middleware `RateLimitAuth` cho 2 route `POST /seller/bank-account` và `POST /seller/bank-account/verify-otp`.

---

## Nghi ngờ (cần xác minh thêm)

1. **`putCachedShopOwner` trong RAM của `services/notification` có thể ghi đè `ShopName` bằng rỗng khi xử lý `user.shop_bank_account_otp`:**
   - Vị trí: [notification.go:375-377](services/notification/internal/service/notification.go#L375-L377), [notification.go:699-705](services/notification/internal/service/notification.go#L699-L705).
   - Trong Postgres, `UpsertShopOwner` ([notifications.sql:127](services/notification/queries/notifications.sql#L127)) giữ lại `shop_name` cũ khi `EXCLUDED.shop_name = ''`, nhưng lời gọi `s.putCachedShopOwner(*plan.cacheShop)` sau commit truyền `ShopName: ""`.
   - Cách kiểm tra: Đọc hàm `putCachedShopOwner` trong `services/notification/internal/service/notification.go` xem có giữ lại `ShopName` cũ trong map RAM khi giá trị mới rỗng hay không.

2. **`platform_fee_vnd` trong mô tả sổ cái `list_seller_ledger` (`services/payment`) phụ thuộc bất biến 1 `transaction_id` / 1 shop:**
   - Vị trí: [service.rs:450-456](services/payment/src/service.rs#L450-L456).
   - Truy vấn cộng `SUM(fe.credit_vnd)` trên `platform_fee_revenue` theo `pe.transaction_id`. Nếu có bất kỳ giao dịch kế toán nào gộp nhiều shop vào chung 1 `ledger_transaction` thì mô tả sẽ cộng lẫn phí sàn của shop khác.
   - Cách kiểm tra: Rà toàn bộ lời gọi tạo `ledger_transactions` có ghi `platform_fee_revenue` trong `services/payment/src/ledger.rs` để xác nhận mỗi giao dịch luôn chỉ thuộc đúng 1 sub-order/shop.

---

## Ponytail (code thừa / trùng lặp)

- [user.sql:475-489](services/user/queries/user.sql#L475-L489) + [stats.go:132-157](services/user/internal/service/stats.go#L132-L157): `GetUserPlatformCounts` và `GetShopPlatformCounts` tính `new_users`/`new_shops` theo khoảng thời gian trong SQL nhưng `stats.go` bỏ hoàn toàn 2 giá trị này và cộng lại từ `ListDailyNewUsersVN`/`ListDailyNewShopsVN` trong Go → cắt bỏ 2 tham số thời gian và cột tính thừa khỏi 2 query count tổng.
- [handlers_promotion.go:1463](services/gateway/internal/router/handlers_promotion.go#L1463) vs [shipping.go:1092-1120](services/shipping/internal/service/shipping.go#L1092-L1120): Nhánh bỏ qua `codes.NotFound` khi gọi `GetShopShippingSettings` ở `handlePreviewDiscounts` là dead branch vì `ShippingService.GetShopShippingSettings` luôn trả struct mặc định (`FreeShipEnabled: false`) thay vì `NotFound` → giản lược nhánh xử lý lỗi.
- [CheckoutClient.tsx:536-550](web/src/app/(shop)/thanh-toan/CheckoutClient.tsx#L536-L550) & [gio-hang/page.tsx:365-375](web/src/app/(shop)/gio-hang/page.tsx#L365-L375): Frontend tính và gửi `subtotal_vnd`, `unit_price_vnd`, `shop_discount_program_item_id` lên `POST /api/v1/promotions/preview` nhưng gateway bỏ qua hoàn toàn → xóa các trường không dùng khỏi payload client.

---

## Các file đã đọc và KHÔNG phát hiện lỗi

- **Hạ tầng chung & log:** [pkg/common/jetstream/stream.go](pkg/common/jetstream/stream.go) (union-merge đủ 6 subject chuẩn + retry race khởi động), [pkg/common/logger/logger.go](pkg/common/logger/logger.go), [pkg/common/logger/grpc.go](pkg/common/logger/grpc.go) (chỉ log metadata RPC, không log payload/OTP/số tài khoản).
- **`services/user`:** [0005_shop_bank_accounts.sql](services/user/migrations/0005_shop_bank_accounts.sql) (forward-only, partial unique index & `CHECK` đầy đủ), [helpers.go](services/user/internal/service/helpers.go) (mask 4 số cuối, hash OTP gắn `shop_id`/`account_id`, chuẩn hóa dấu tiếng Việt), [publisher.go](services/user/internal/events/publisher.go) (envelope chuẩn, không log OTP).
- **`services/shipping`:** [000004_shop_freeship_settings.sql](services/shipping/migrations/000004_shop_freeship_settings.sql) (idempotent khi chạy lại), [grpcserver/server.go](services/shipping/internal/grpcserver/server.go) (`GetShopShippingSettings` chặn anonymous, non-owner bắt buộc có `shop_id` và chỉ nhận 4 trường public freeship qua `toProtoPublicFreeShipSettings`; `UpdateShopShippingSettings` ép `shop_id` từ JWT seller).
- **`services/notification`:** [000004_notification_dedup_and_indexes.sql](services/notification/migrations/000004_notification_dedup_and_indexes.sql) (idempotent, tương thích dữ liệu cũ), [mailer.go](services/notification/internal/mailer/mailer.go) (không đưa OTP vào Subject, chặn CRLF header injection), [consumer.go](services/notification/internal/consumer/consumer.go) (`sanitizeDLQPayload` đệ quy che `otp`/`reset_token`/`account_number` và redact payload hỏng trên subject nhạy cảm trước khi lưu DLQ), [grpcserver/server.go](services/notification/internal/grpcserver/server.go), [store.go](services/notification/internal/store/store.go) (`InsertNotification` xử lý `ON CONFLICT (dedup_key) DO NOTHING` an toàn), [notifications.sql](services/notification/queries/notifications.sql) (email dedup + in-app dedup đúng).
- **`services/gateway`:** [clients.go](services/gateway/internal/clients/clients.go) (không giữ `SERVICE_JWT_SECRET`, forward JWT chuẩn), [response.go](services/gateway/internal/httpx/response.go) (che lỗi 5xx nội bộ), [handlers_shipping.go](services/gateway/internal/router/handlers_shipping.go) (ép `shop_id` từ JWT seller), [handlers_notification.go](services/gateway/internal/router/handlers_notification.go).
- **Chéo service đã đối chiếu quyền & migration:** [services/order/internal/service/reports.go](services/order/internal/service/reports.go), [0010_w2b_order.sql](services/order/migrations/0010_w2b_order.sql), [0007_payment_fee_configs.sql](services/payment/migrations/0007_payment_fee_configs.sql), [services/payment/src/workers.rs](services/payment/src/workers.rs), [000005_w2b_catalog_outbox_price_fee.sql](services/product/migrations/000005_w2b_catalog_outbox_price_fee.sql), [services/product/internal/events/outbox.go](services/product/internal/events/outbox.go), [003_w2b_promotions.sql](services/promotion/internal/store/migrations/003_w2b_promotions.sql), [vouchers_w2b.go](services/promotion/internal/service/vouchers_w2b.go), [flash_w2b.go](services/promotion/internal/service/flash_w2b.go), [shop_discounts.go](services/promotion/internal/service/shop_discounts.go), [voucher_expiry_job.go](services/promotion/internal/events/voucher_expiry_job.go), [web/src/app/(shop)/tai-khoan/page.tsx](web/src/app/(shop)/tai-khoan/page.tsx) (xác nhận route `/tai-khoan?tab=voucher` và `/seller/san-pham/[id]` từ link thông báo đều tồn tại và hoạt động).

