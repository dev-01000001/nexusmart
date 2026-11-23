# Báo cáo Review Độc lập — Đợt 2b (`services/promotion` & `services/product`)

Phạm vi soát: toàn bộ luồng Wave 2b của `services/promotion` và `services/product` (kèm kiểm tra chéo contract/caller với `gateway`, `order`, `notification`, `pkg/common`, `proto`). Không sửa file, không chạy lệnh ghi.

---

## P0 — Sai tiền / Oversell / Vượt giới hạn mỗi người / Lệch Redis–DB

### 1. Race `ReservePromotion` vs `ReleasePromotion` gây oversell flash/voucher và lệch Redis–DB vĩnh viễn
- **Vị trí:** [promotion.go:879-980](services/promotion/internal/service/promotion.go#L879-L980), [promotion.go:1017-1025](services/promotion/internal/service/promotion.go#L1017-L1025), [store.go:1186-1306](services/promotion/internal/store/store.go#L1186-L1306), [store.go:1309-1326](services/promotion/internal/store/store.go#L1309-L1326)
- **Kịch bản:**
  1. `T1` (`ReservePromotion(G1)`) chạy `reserve_atomic.lua` trừ suất flash/voucher/per-user trên Redis và ghi `promo:res:G1 = "RESERVED\n..."`, sau đó bị chậm trước `CommitReservation`.
  2. `T2` (`ReleasePromotion(G1)` từ compensation/watchdog) chạy `store.ReleaseReservation(G1)` → chưa có dòng PG nên trả `pgAlreadyReleased=true` mà **không ghi tombstone**; tiếp đó `T2` chạy `engine.Release(G1)` → `release_atomic.lua` hoàn toàn bộ suất/quota/per-user về Redis và đặt `promo:res:G1 = "RELEASED"`.
  3. `T1` tiếp tục chạy `store.CommitReservation(G1)` → không thấy dòng PG → trừ xu, insert `status='RESERVED'`, giảm `remaining_slots`/tăng `used_count` trong PG và trả `{Reserved: true}` cho `order`.
  4. Ngoài ra, ở nhánh `ON CONFLICT (order_id) DO NOTHING` ([store.go:1266-1273](services/promotion/internal/store/store.go#L1266-L1273)), code trả `(saved, true, nil)` mà không kiểm tra `saved.Status == RELEASED`.
  - **Kết quả sai:** Đơn hàng tiếp tục hợp lệ nhưng Redis đã trả lại hết suất/quota và bộ đếm per-user → người mua khác (hoặc chính user đó) tiếp tục giữ suất → **oversell flash/voucher, vượt `per_user_limit`, Redis lệch DB vĩnh viễn**.
- **Đề xuất sửa:** Khi `ReleaseReservation` không thấy dòng PG, ghi dòng tombstone `status='RELEASED'` trong `promotion_reservations` (hoặc kiểm tra lại `promo:res:<order_id>` còn `RESERVED` sau khi giữ lock PG), và kiểm tra `saved.Status == RELEASED` ở nhánh `ON CONFLICT` ([store.go:1268-1272](services/promotion/internal/store/store.go#L1268-L1272)).

### 2. Redis restart/eviction hoặc trả hàng sau 30 ngày làm mất toàn bộ bộ đếm `per_user_limit` và làm rò rỉ suất khi hủy/hoàn
- **Vị trí:** [promotion.go:70-99](services/promotion/internal/service/promotion.go#L70-L99), [scripts.go:17](services/promotion/internal/lua/scripts.go#L17), [release_atomic.lua:11-14](services/promotion/internal/lua/release_atomic.lua#L11-L14), [release_partial_atomic.lua:17-20](services/promotion/internal/lua/release_partial_atomic.lua#L17-L20)
- **Kịch bản:**
  - **Nhánh A (Redis restart/eviction):** `SyncRedisFromStore` chỉ nạp lại `promo:voucher:<id>:{status,remaining,budget_remaining}` và `promo:flash:<id>:remaining` (qua `SetNX`). Hàm **không bao giờ nạp lại** `promo:voucher:<id>:user:<uid>`, `promo:flash:<id>:user:<uid>`, `promo:shop_prog:<id>:user:<uid>` và `promo:res:<order_id>` từ các dòng `promotion_reservations WHERE status = 'RESERVED'`. Sau khi Redis restart/evict: (1) mọi `per_user_limit` về `0` → **mọi user vượt giới hạn mỗi người**; (2) khi hủy đơn cũ, `GET promo:res:<id>` trả `nil` → Lua trả `ALREADY_RELEASED`, không hoàn suất/ngân sách trên Redis trong khi PG vẫn hoàn → lệch Redis–DB và kẹt suất vĩnh viễn.
  - **Nhánh B (Đơn hoàn sau 30 ngày):** `promo:res:<order_id>` có TTL 30 ngày (`DefaultReservationTTLSeconds`), còn các key `:user:*` không có TTL; trả hàng/tranh chấp quá 30 ngày gọi `ReleasePromotionShop` sẽ gặp `nil` ở Redis, rò rỉ quota/budget/per-user trên Redis trong khi PG đã hoàn.
- **Đề xuất sửa:** Mở rộng `SyncRedisFromStore` để dựng lại bộ đếm `:user:` và `promo:res:<order_id>` từ `promotion_reservations WHERE status='RESERVED'` (trừ các shop trong `released_shops_json`), và khi PG vừa release từ `RESERVED` mà Redis không có `promo:res:<order_id>` thì fallback bù trực tiếp delta từ PG sang Redis.

### 3. Race `UpdateVoucher` (TOCTOU) + `adjust_voucher.lua` clamp về `0` gây oversell voucher, vượt ngân sách và lệch Redis–DB không tự phục hồi
- **Vị trí:** [vouchers_w2b.go:34-51](services/promotion/internal/service/vouchers_w2b.go#L34-L51), [vouchers_w2b.go:232-243](services/promotion/internal/service/vouchers_w2b.go#L232-L243), [adjust_voucher.lua:37-42](services/promotion/internal/lua/adjust_voucher.lua#L37-L42), [adjust_voucher.lua:59-64](services/promotion/internal/lua/adjust_voucher.lua#L59-L64), [promotion.go:70-88](services/promotion/internal/service/promotion.go#L70-L88)
- **Kịch bản:**
  - **Nhánh A (Oversell do clamp âm về 0):** Voucher `V1` có `quota=10, used_count=0`. `UpdateVoucher` đọc `liveUsed=0` trước khi lock PG. Đồng thời, 5 `ReservePromotion` chạy `reserve_atomic.lua` (`rem=5` ở Redis, PG chưa commit `used_count`). `UpdateVoucher` thấy `hasUsed=false`, hạ `quota` xuống `3` (`quotaDelta=-7`), commit PG, rồi gọi `adjust_voucher.lua`: `INCRBY remKey -7` thành `-2` rồi **clamp thành `0`**, xóa sổ khoản thâm hụt `-2`. Khi 1 trong 5 đơn hủy, `release_atomic.lua` chạy `INCR remKey` → `1` → đơn thứ 6 vẫn reserve được (5 đơn hoạt động > `quota=3`). Lỗi clamp tương tự ở [adjust_voucher.lua:60-63](services/promotion/internal/lua/adjust_voucher.lua#L60-L63) làm vượt `budget_vnd` sau khi hủy đơn.
  - **Nhánh B (Đổi luật sau khi đã giữ suất):** Trong cùng cửa sổ TOCTOU, `UpdateVoucher` có thể đổi `discount_type`, `value_vnd`, `per_user_limit`, `category_ids` hoặc tắt `budget_vnd` (`>0 -> 0`, `DEL budgetKey`; sau đó `release_atomic.lua` gọi `INCRBY` tạo key budget zombie).
  - **Nhánh C (Lỗi Redis sau commit PG):** `CreateVoucher`/`UpdateVoucher`/`SetVoucherStatus`/`DeleteVoucher` đều commit PG trước rồi mới cập nhật Redis. Nếu Redis lỗi/pod crash sau commit PG, vì `SyncRedisFromStore` dùng `SetNX` cho `remaining`/`budget_remaining`, **ngay cả restart service cũng không tự đồng bộ lại quota/budget**.
- **Đề xuất sửa:** Không clamp âm trong `adjust_voucher.lua` (từ chối giảm quota/budget nếu `cur + delta < 0` hoặc đọc `used` nguyên tử trong Lua trước khi cho phép sửa trường của voucher chưa dùng), và khi khởi động/reconcile ghi đè hoặc đối soát counter Redis theo PG khi không có tx in-flight.

### 4. Race `ReservePromotion` vs `EndShopDiscountProgram` / `SetVoucherStatus(PAUSED)` / `DeleteVoucher` cho phép áp khuyến mãi đã dừng/xóa
- **Vị trí:** [shop_discounts.go:207-241](services/promotion/internal/service/shop_discounts.go#L207-L241), [promotion.go:825-885](services/promotion/internal/service/promotion.go#L825-L885), [promotion.go:960-980](services/promotion/internal/service/promotion.go#L960-L980), [reserve_atomic.lua:129-146](services/promotion/internal/lua/reserve_atomic.lua#L129-L146), [store.go:1278-1287](services/promotion/internal/store/store.go#L1278-L1287)
- **Kịch bản:**
  - **Chương trình giảm giá shop:** `EndShopDiscountProgram` chỉ cập nhật PG (`status='ENDED'`), không cập nhật Redis. `T1` (`ReservePromotion`) đọc item active trong `buildCalcInput`; `T2` gọi `EndShopDiscountProgram` commit `ENDED` và trả 200 OK; `T1` chạy `reserve_atomic.lua` (không kiểm tra key trạng thái chương trình) rồi `CommitReservation` (không kiểm tra lại trạng thái chương trình trong PG) → đơn hàng vẫn được giảm giá theo chương trình đã kết thúc. Race tương tự khi `UpdateShopDiscountProgram` đổi giá/xóa SKU đúng lúc checkout.
  - **Voucher:** `T1` đọc voucher `ACTIVE`; `T2` (`SetVoucherStatus(PAUSED)` / `DeleteVoucher`) commit PG xong nhưng chưa kịp ghi `statusKey` lên Redis; `T1` qua `reserve_atomic.lua` rồi chạy `UPDATE vouchers SET used_count = used_count + 1 WHERE id = $1` **không có điều kiện `AND status = 'ACTIVE'`** → voucher đã tạm dừng/xóa vẫn bị trừ suất và áp vào đơn.
- **Đề xuất sửa:** Kiểm tra lại `vouchers.status = 'ACTIVE'` và `shop_discount_programs.status <> 'ENDED' AND ends_at > NOW()` ngay trong transaction `CommitReservation` (nếu lệch thì rollback PG + bù trừ Redis), đồng thời đồng bộ `promo:shop_prog:<program_id>:status` sang Redis cho `reserve_atomic.lua`.

### 5. `ReservePromotion` nhận end-user JWT và bỏ qua số lượng trong `ShopItems` khi trùng `FlashSaleItems` → oversell flash & vượt `per_user_limit`
- **Vị trí:** [promotion.go:804](services/promotion/internal/service/promotion.go#L804), [promotion.go:1188-1217](services/promotion/internal/service/promotion.go#L1188-L1217), [auth.go:98](pkg/common/auth/auth.go#L98)
- **Kịch bản:** `ReservePromotion` không nằm trong `internalRPCs` (cho phép gọi bằng JWT người dùng thường). Caller gửi đồng thời `FlashSaleItems: [{flash_sale_item_id: F1, quantity: 1}]` và `ShopItems: [{..., items: [{flash_sale_item_id: F1, quantity: 10}]}]`. Ở [promotion.go:1207-1212](services/promotion/internal/service/promotion.go#L1207-L1212), `resolveFlashReservations` thấy `explicitSet[F1] == true` nên **bỏ qua** số lượng `10` trong `ShopItems`. Kết quả: `calculator.Calculate` tính giảm giá cho **10 sản phẩm**, còn `reserve_atomic.lua` chỉ trừ **1 suất** flash và cộng **1** vào `per_user_limit` → **oversell 9 suất và vượt giới hạn mỗi người**.
- **Đề xuất sửa:** Khi `len(req.GetShopItems()) > 0`, luôn suy ra `flashSpecs` từ `ShopItems` (hoặc từ chối bằng `InvalidArgument` nếu số lượng giữa `FlashSaleItems` và `ShopItems` không khớp).

---

## P1 — Hỏng luồng chính / Vi phạm state machine / Preview lệch Checkout

### 6. `ApproveFlashSaleItem` cho phép chuyển trạng thái trái phép (`APPROVED -> REJECTED`, `REJECTED -> APPROVED`), làm hỏng suất Redis và làm hỏng retry idempotent
- **Vị trí:** [store.go:703-768](services/promotion/internal/store/store.go#L703-L768), [promotion.go:589-643](services/promotion/internal/service/promotion.go#L589-L643)
- **Kịch bản:**
  - **Nhánh A:** `store.ApproveFlashSaleItem` không kiểm tra `prevStatus`. Admin có thể reject một item đang `APPROVED` (thậm chí đang chạy và đã bán một phần) → `DeleteFlashItemSlots` xóa key Redis; sau đó khi một đơn cũ hủy, `release_atomic.lua` chạy `INCRBY remKey qty` dựng lại key rác. Nếu admin gọi approve lại (`REJECTED -> APPROVED`), `prevStatus != APPROVED` đặt `overwrite = true`, ghi đè Redis bằng `remaining_slots` cũ trong DB.
  - **Nhánh B:** Gọi lại `ApproveFlashSaleItem(approve=true)` để retry idempotent trên item đã `APPROVED` và đang bán bị chạy lại `validateFlashSaleSKU` ([promotion.go:607](services/promotion/internal/service/promotion.go#L607)) kiểm tra `total_slots <= available_stock`; khi tồn kho đã giảm do bán hàng, lời gọi idempotent bị lỗi `FailedPrecondition`.
- **Đề xuất sửa:** Chỉ cho phép chuyển từ `PENDING -> APPROVED|REJECTED` trong `store.ApproveFlashSaleItem`; trả idempotent ngay khi trạng thái đích trùng `prevStatus` (không kiểm tra lại `total_slots <= available_stock`) và trả `FailedPrecondition` cho `APPROVED -> REJECTED` / `REJECTED -> APPROVED`.

### 7. `GET /api/v1/admin/vouchers` mặc định rơi vào chế độ buyer storefront, ẩn hết voucher `PAUSED`/sắp chạy/hết hạn/hết suất
- **Vị trí:** [promotion.go:262-280](services/promotion/internal/service/promotion.go#L262-L280), [promotion.go:350-375](services/promotion/internal/service/promotion.go#L350-L375), [handlers_promotion.go:705-725](services/gateway/internal/router/handlers_promotion.go#L705-L725)
- **Kịch bản:** Admin gọi `GET /api/v1/admin/vouchers` (không truyền `include_inactive=true` hay `status`, hoặc chỉ lọc `?scope=PLATFORM`). Gateway truyền `IncludeInactive: false, Status: UNSPECIFIED`. Trong `PromotionService.ListAvailableVouchers`, `adminManageView` là `false` ([promotion.go:263-265](services/promotion/internal/service/promotion.go#L263-L265)), nên `manageView = false` và hệ thống lọc như người mua: ẩn toàn bộ voucher `PAUSED`, voucher lịch tương lai (`now < starts_at`), đã hết hạn, hết quota/ngân sách, hoặc admin đã dùng hết lượt (ngược với `GET /api/v1/seller/vouchers` luôn vào `sellerManageView`).
- **Đề xuất sửa:** Mặc định `includeInactive = true` trong `handleAdminListVouchers` khi query param bị bỏ trống (hoặc đặt `adminManageView = hasClaims && isAdmin(claims) && reqUserID == "" && req.GetOrderAmountVnd() == 0`).

### 8. `PreviewDiscounts` không kiểm tra `promo:flash:<id>:remaining`, báo giá flash ảo khi đã hết suất
- **Vị trí:** [promotion.go:1506-1536](services/promotion/internal/service/promotion.go#L1506-L1536), [calculator.go:577-606](services/promotion/internal/calculator/calculator.go#L577-L606)
- **Kịch bản:** Flash item `F1` đang active nhưng `promo:flash:F1:remaining == 0` (đã hết suất). Người mua gọi `PreviewDiscounts` với `flash_sale_item_id: F1`. `buildCalcInput` chỉ đọc `FlashUserKey`, không đọc `FlashRemainingKey`, và `validateFlashCandidate` không kiểm tra `RemainingSlots < item.Quantity`. Preview trả giá flash không có cảnh báo — trái yêu cầu `w2b-plan.md §2` (*"hết suất thì báo rõ và cho mua giá thường"*) — rồi đến checkout mới thất bại `FLASH_SOLD_OUT`.
- **Đề xuất sửa:** Đọc thêm `lua.FlashRemainingKey(id)` trong `buildCalcInput`, gán vào `FlashItemCandidate.RemainingSlots`, và từ chối/cảnh báo fallback giá thường trong `validateFlashCandidate` khi `alreadyInOrder + item.Quantity > cand.RemainingSlots`.

### 9. `UpdateShopDiscountProgram` trên chương trình `ACTIVE` hard-delete item khi bỏ SKU → thêm lại SKU sẽ reset `per_user_limit`
- **Vị trí:** [store/shop_discounts.go:190-246](services/promotion/internal/store/shop_discounts.go#L190-L246), [service/shop_discounts.go:113-186](services/promotion/internal/service/shop_discounts.go#L113-L186)
- **Kịch bản:** Chương trình `P1` đang `ACTIVE` có SKU `S1` (`item_id = I1`, `per_user_limit = 1`). Buyer A mua 1 cái (`promo:shop_prog:I1:user:A = 1`). Seller gọi `UpdateShopDiscountProgram` (`replace_items=true`) bỏ `S1` → PG `DELETE` hẳn dòng `I1`. Seller cập nhật lại `P1` thêm `S1` → sinh UUID mới `I2` → `promo:shop_prog:I2:user:A = 0` → Buyer A mua vượt `per_user_limit` trong cùng chương trình `P1`.
- **Đề xuất sửa:** Cấm xóa/thay thế item (hoặc giảm `per_user_limit`) khi chương trình đã `ACTIVE`, hoặc đặt khóa Redis per-user theo `(program_id, sku_id)` thay vì `item_id`.

### 10. Retry cạnh tranh `SHOP_PROG_USER_LIMIT_EXCEEDED` giới hạn cứng `2` lần có thể làm fail checkout nhiều shop thay vì fallback giá thường
- **Vị trí:** [promotion.go:823-894](services/promotion/internal/service/promotion.go#L823-L894)
- **Kịch bản:** Đơn checkout nhiều shop có >= 2 item thuộc chương trình giảm giá shop (`I1` của Shop 1, `I2` của Shop 2) cùng race với các phiên checkout khác của cùng user: lần 0 fail `I1` → lần 1 bỏ `I1` về giá thường nhưng race trúng `I2` → hết `maxShopProgRaceAttempts = 2`, `ReservePromotion` trả lỗi `FailedPrecondition` làm hỏng checkout thay vì fallback `I2` về giá thường theo spec.
- **Đề xuất sửa:** Tăng số lần thử lên `len(shopProgSpecs)+1` (có trần trên hợp lý) hoặc loại trừ đúng `item_id` từ `resErr.Key` trong vòng lặp để luôn hội tụ về giá thường.

---

## P2 — Lệch spec / Edge-case / Trust boundary / Mapping lỗi

### 11. Voucher giảm `0₫` và item chương trình giảm giá shop giảm `0₫` vẫn bị trừ quota và `per_user_limit`
- **Vị trí:** [calculator.go:223-249](services/promotion/internal/calculator/calculator.go#L223-L249), [calculator.go:296-313](services/promotion/internal/calculator/calculator.go#L296-L313), [calculator.go:382-400](services/promotion/internal/calculator/calculator.go#L382-L400), [calculator.go:452-470](services/promotion/internal/calculator/calculator.go#L452-L470), [calculator.go:683-705](services/promotion/internal/calculator/calculator.go#L683-L705)
- **Kịch bản:**
  - **Voucher:** Đơn có `G = 100.000₫`, `V_shop = 100.000₫` → `afterShop = 0₫`. Người mua áp thêm voucher sàn `FIXED 20.000₫, min_order = 0`. `evaluateVoucher` tính `disc = 0`, trả `(0, "")`, và vẫn đưa vào `AppliedVouchers` → `ReservePromotion` trừ quota và `per_user_limit` cho mức giảm `0₫` (tương tự với voucher `1%` trên cơ sở `< 100₫`).
  - **Chương trình giảm giá shop:** Giá gốc SKU hạ xuống `<= discounted_price_vnd` sau khi tạo chương trình → `lineProgDisc = 0` nhưng vẫn thêm vào `AppliedShopPrograms`, đốt lượt `per_user_limit` của người mua cho `0₫` giảm giá.
- **Đề xuất sửa:** Trong `evaluateVoucher`, yêu cầu `disc > 0` mới áp dụng (nếu `0` thì cảnh báo/từ chối), và ở dòng chương trình shop chỉ đưa vào `AppliedShopPrograms` khi `item.UnitPriceVND > spi.DiscountedPriceVND`.

### 12. `buildCalcInput` tin `category_id` do client gửi khi `sku_id` rỗng hoặc không tồn tại ở `ProductService`
- **Vị trí:** [promotion.go:1447-1452](services/promotion/internal/service/promotion.go#L1447-L1452)
- **Kịch bản:** Caller gọi gRPC `PreviewDiscounts`/`ReservePromotion` với voucher sàn giới hạn danh mục, gửi `sku_id` giả/rỗng kèm `category_id` giả khớp danh mục voucher. Vì code chỉ ghi đè `catID` khi `skuMeta[skuID]` tìm thấy (`if sk, ok := skuMeta[skuID]; ok`), giá trị client gửi vẫn được giữ nguyên để qua bước kiểm tra danh mục.
- **Đề xuất sửa:** Khi `needCategorySKUs` là `true` và `s.productClient != nil`, xóa `catID = ""` trước khi tra `skuMeta[skuID]` để chỉ tin danh mục từ `ProductService`.

### 13. `CreateShopDiscountProgram` / `UpdateShopDiscountProgram` không kiểm tra `sku.GetIsActive()`
- **Vị trí:** [service/shop_discounts.go:382-395](services/promotion/internal/service/shop_discounts.go#L382-L395)
- **Kịch bản:** Seller tạo/cập nhật chương trình giảm giá shop với SKU đang `INACTIVE` hoặc thuộc sản phẩm `PENDING`/`REJECTED`/`HIDDEN`/đã xóa mềm. Khác với `validateFlashSaleSKU` ([flash_w2b.go:50](services/promotion/internal/service/flash_w2b.go#L50)), hàm không kiểm tra `sku.GetIsActive()`, khiến SKU không mở bán vẫn vào chương trình và lộ trên `ListActiveShopDiscounts`.
- **Đề xuất sửa:** Thêm kiểm tra `if !sku.GetIsActive() { return ..., status.Errorf(codes.FailedPrecondition, "sku %s is not active", skuID) }` trong `validateShopDiscountProgramItems`.

### 14. `UpdateShopDiscountProgram` (không `replace_items`) bỏ qua kiểm tra trùng flash sale nếu item đã có sẵn cảnh báo
- **Vị trí:** [service/shop_discounts.go:155-170](services/promotion/internal/service/shop_discounts.go#L155-L170)
- **Kịch bản:** Item có sẵn cảnh báo nâng giá ảo (`it.Warning != ""`). Seller gia hạn `ends_at` để trùng khung flash sale đã duyệt mà không gửi `replace_items`. Nhánh `if it.Warning != "" { ... } else { HasOverlappingApprovedFlashSale(...) }` bỏ qua hoàn toàn bước kiểm tra trùng flash sale.
- **Đề xuất sửa:** Luôn gọi `HasOverlappingApprovedFlashSale` cho mọi item khi cập nhật khung thời gian và tránh lặp nội dung cảnh báo.

### 15. `CreateVoucher` / `UpdateVoucher` thiếu validate trường chéo loại giảm giá → trả `500 Internal` do vi phạm DB `CHECK`
- **Vị trí:** [promotion.go:164-172](services/promotion/internal/service/promotion.go#L164-L172), [vouchers_w2b.go:177-185](services/promotion/internal/service/vouchers_w2b.go#L177-L185), [001_init.sql:10-11](services/promotion/internal/store/migrations/001_init.sql#L10-L11)
- **Kịch bản:** Gửi `discount_type=FIXED, value_vnd=10000, discount_percent=150` (hoặc `-1`), hoặc `discount_type=PERCENT, discount_percent=10, value_vnd=-5` qua được tầng service nhưng đụng `CHECK (value_vnd >= 0)` / `CHECK (discount_percent BETWEEN 0 AND 100)` của Postgres, trả về `500 Internal` thay vì `400 InvalidArgument`.
- **Đề xuất sửa:** Validate `value_vnd >= 0` và `0 <= discount_percent <= 100` (hoặc ép trường không dùng về `0`) trong cả `CreateVoucher` và `UpdateVoucher`.

### 16. Job `promotion.voucher_expiring` không kiểm tra hết ngân sách (`budget_vnd`)
- **Vị trí:** [store.go:480-493](services/promotion/internal/store/store.go#L480-L493)
- **Kịch bản:** Voucher `ACTIVE` vẫn còn `used_count < quota` nhưng `budget_vnd > 0` và `used_budget_vnd >= budget_vnd` (đã cạn ngân sách, không dùng được nữa). `ProcessExpiringSavedVouchers` vẫn query và gửi thông báo sắp hết hạn tới người mua.
- **Đề xuất sửa:** Thêm điều kiện `AND (v.budget_vnd = 0 OR v.used_budget_vnd < v.budget_vnd)` vào truy vấn `ProcessExpiringSavedVouchers`.

### 17. `ProductService.AdminReviewProduct`: thiếu chặn duyệt lại (gửi trùng event `product.reviewed`) và duyệt sản phẩm hết hàng bị đặt nhầm `APPROVED`
- **Vị trí:** [server.go:1033-1113](services/product/internal/server/server.go#L1033-L1113)
- **Kịch bản:**
  - **Trùng event:** Gọi `AdminReviewProduct(APPROVE)` hai lần (hoặc trên sản phẩm đã `APPROVED`/`HIDDEN`) đều thành công và chèn thêm dòng `product.reviewed` mới vào `outbox_events` với UUID mới → seller nhận thông báo in-app + email lặp lại.
  - **Sai trạng thái tồn kho:** Duyệt `APPROVE` sản phẩm `PENDING` mà mọi SKU đều có `stock == 0` sẽ đặt `status = APPROVED` thay vì `OUT_OF_STOCK` (khác với `UpdateProduct` gọi `syncProductStockStatus`).
- **Đề xuất sửa:** Khóa dòng sản phẩm (`FOR UPDATE`), chỉ cho duyệt từ `PENDING` (hoặc bỏ qua ghi outbox nếu đã ở trạng thái đích), và gọi `syncProductStockStatus` sau khi `APPROVE`.

### 18. `POST /api/v1/admin/fees/categories/{id}` âm thầm đặt phí hoa hồng về `0%` nếu thiếu trường `commission_rate_bps`
- **Vị trí:** [handlers_product.go:353-378](services/gateway/internal/router/handlers_product.go#L353-L378) (ranh giới gateway → `SetCategoryFeeSchedule`)
- **Kịch bản:** Admin gửi `POST /api/v1/admin/fees/categories/{id}` với body `{"note": "test"}` quên trường `commission_rate_bps`. Go decode `int32` mặc định thành `0` (hợp lệ trong `[0, 10000]`), âm thầm hạ phí hoa hồng danh mục xuống `0%`.
- **Đề xuất sửa:** Đổi `CommissionRateBps` trong payload JSON sang `*int32` và trả `400 Bad Request` khi `nil`.

---

## P3 — Nhỏ / Vận hành / Dọn code thừa (`ponytail-review`)

### 19. Giữ PG transaction (kèm row lock) xuyên suốt network I/O tới NATS; outbox `product` retry 250ms không backoff
- **Vị trí:** [promotion/internal/store/store.go:474-541](services/promotion/internal/store/store.go#L474-L541), [product/internal/store/pg_store.go:61-97](services/product/internal/store/pg_store.go#L61-L97), [product/internal/events/outbox.go:108-115](services/product/internal/events/outbox.go#L108-L115)
- **Kịch bản:** Cả hai worker giữ transaction PG + `FOR UPDATE SKIP LOCKED` trong lúc publish tối đa 50–100 message sang NATS. Nếu NATS chậm/treo, connection pool PG bị kẹt; ngoài ra outbox `product` không có backoff/retry limit nên dòng lỗi sẽ bị quét lại mỗi 250ms.
- **Đề xuất sửa:** Đặt timeout ngắn theo từng publish NATS, tách nhịp lease/ack hoặc thêm backoff/giới hạn retry khi `ProcessPendingOutbox` lỗi liên tiếp.

### 20. Không map `ErrForeignKeyViolation` trong `SetCategoryFeeSchedule` và `CreateProduct`
- **Vị trí:** [server.go:478-483](services/product/internal/server/server.go#L478-L483), [server.go:648-656](services/product/internal/server/server.go#L648-L656)
- **Kịch bản:** Khi danh mục bị xóa song song bởi `DeleteCategory` ngay sau bước `GetCategoryByID`, insert bảng con vi phạm FK trả về `500 Internal` thay vì `404 NotFound`.
- **Đề xuất sửa:** Map `errors.Is(err, store.ErrForeignKeyViolation)` sang `codes.NotFound` (hoặc `codes.FailedPrecondition`).

### 21. Graceful shutdown của `services/promotion` không đợi `expiryJob` dừng trước khi đóng NATS
- **Vị trí:** [services/promotion/cmd/server/main.go:127](services/promotion/cmd/server/main.go#L127)
- **Kịch bản:** `go expiryJob.Start(ctx, ...)` chạy không qua `sync.WaitGroup`, nên khi nhận `SIGTERM`, `defer nc.Close()` có thể đóng kết nối NATS trong lúc `RunOnce` đang publish (khác với `services/product/cmd/product/main.go` có `bgWG.Wait()`).
- **Đề xuất sửa:** Bọc `expiryJob.Start` trong `sync.WaitGroup` và `wg.Wait()` sau `cancel()` trước khi đóng NATS/DB.

### 22. Code & cấu hình thừa (`ponytail-review`)
- [store.go:56](services/promotion/internal/store/store.go#L56), [store.go:405-426](services/promotion/internal/store/store.go#L405-L426), [memory_store.go:224](services/promotion/internal/store/memory_store.go#L224): `ListAllActiveVouchers` hoàn toàn không được gọi ở đâu — xóa.
- [scripts.go:207-225](services/promotion/internal/lua/scripts.go#L207-L225): `SyncVoucherKeysIfMissing` không dùng trong production (chỉ còn trong test, đã thay bằng `SyncVoucherKeysWithStatus`) — xóa.
- [service/shop_discounts.go:454-546](services/promotion/internal/service/shop_discounts.go#L454-L546): tham số `shopItems` và nhánh lặp dòng `524-545` là dead code (caller duy nhất ở [promotion.go:896](services/promotion/internal/service/promotion.go#L896) luôn truyền `nil`) — rút gọn; đồng thời kiểm tra nhánh `resolveBareVoucherReservations` ([promotion.go:1339-1342](services/promotion/internal/service/promotion.go#L1339-L1342)) đang để `disc = 0` với voucher `PERCENT` có `budget_vnd > 0`.
- [docker-compose.yml:191](docker-compose.yml#L191), [docker-compose.yml:209](docker-compose.yml#L209): biến `SHIPPING_GRPC_ADDR` và `depends_on: shipping` trên container `promotion` không được `services/promotion` đọc hay sử dụng — bỏ để giảm phụ thuộc khởi động.

---

## File đã đọc và không thấy lỗi

**`services/promotion`**
- [003_w2b_promotions.sql](services/promotion/internal/store/migrations/003_w2b_promotions.sql) (idempotent khi chạy lại trên DB có sẵn dữ liệu)
- [002_partial_release.sql](services/promotion/internal/store/migrations/002_partial_release.sql)
- [config.go](services/promotion/internal/config/config.go)
- [models.go](services/promotion/internal/domain/models.go)
- [client.go](services/promotion/internal/productclient/client.go)
- [consumer.go](services/promotion/internal/events/consumer.go) (dùng `jetstream.EnsureStreams` union-merge chuẩn)
- [voucher_expiry_job.go](services/promotion/internal/events/voucher_expiry_job.go) (envelope & payload khớp `docs/contracts.md` và `notification`)
- [flash_w2b.go](services/promotion/internal/service/flash_w2b.go)
- [release_plan.go](services/promotion/internal/store/release_plan.go)
- [Dockerfile](services/promotion/Dockerfile)

**`services/product`**
- [000005_w2b_catalog_outbox_price_fee.sql](services/product/migrations/000005_w2b_catalog_outbox_price_fee.sql) (backfill `sku_price_history` & `category_fee_schedules` an toàn trên dữ liệu có sẵn)
- [000001_init.sql](services/product/migrations/000001_init.sql), [000002_sku_optimistic_locking.sql](services/product/migrations/000002_sku_optimistic_locking.sql), [000003_product_slug_search_and_category_sort.sql](services/product/migrations/000003_product_slug_search_and_category_sort.sql), [000004_product_audit_fixes.sql](services/product/migrations/000004_product_audit_fixes.sql), [embed.go](services/product/migrations/embed.go)
- [product.sql](services/product/queries/product.sql) (`min_price_30d_vnd` xử lý đúng giá carry-over ở mốc 30 ngày, CTE đệ quy danh mục, tie-break `effective_from, created_at, seq` của `category_fee_schedules`)
- [main.go](services/product/cmd/product/main.go) (graceful shutdown có `bgWG.Wait()`)
- [types.go](services/product/internal/events/types.go)
- [store.go](services/product/internal/store/store.go)
- [memory_store.go](services/product/internal/store/memory_store.go)
- [search.go](services/product/internal/store/search.go)
- [db.go](services/product/internal/db/db.go)
- [Dockerfile](services/product/Dockerfile)

**Dùng chung / hợp đồng**
- [stream.go](pkg/common/jetstream/stream.go) (union-merge subject & retry xung đột đúng chuẩn)
- [promotion.proto](proto/nexusmart/promotion/v1/promotion.proto), [product.proto](proto/nexusmart/product/v1/product.proto)
- Hệ thức tiền trong [calculator.go](services/promotion/internal/calculator/calculator.go): đã kiểm chứng đại số `P + D = R + Ship`, `R = G - F - P_shop - V_shop - S_shop >= 0`, `D = V_plat + S_free + X`, ưu tiên flash > chương trình shop, `allocateProportional` dùng `math/big` largest-remainder không sai số làm tròn (chỉ tồn tại edge-case giảm `0₫` ở mục #11 và thiếu check `RemainingSlots` ở mục #8).

