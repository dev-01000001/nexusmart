# Báo cáo Review Độc lập Lượt 2 — Đợt 2b (`services/promotion` & `services/product`)

> **Phương pháp & kiểm chứng thực thi:** Chỉ đọc code hiện tại và ranh giới gọi từ `services/order` / `services/gateway` (không sửa file, không chạy lệnh ghi, không đụng container, không đọc báo cáo coder). Đã chạy kiểm chứng chỉ đọc:
> - `go test -race -count=1 ./...` và `golangci-lint run ./...` trên cả `services/promotion` và `services/product`: **xanh (exit 0)**.
> - Lưu ý: cả [store_pg_test.go](services/promotion/internal/store/store_pg_test.go) và [pg_integration_test.go](services/product/internal/server/pg_integration_test.go) đều bị **`SKIP`** trong lần chạy mặc định do không đặt `TEST_PROMOTION_DB_URL` / `TEST_PRODUCT_DB_URL`.

---

## 1. Kiểm tra lại các phát hiện P0 / P1 / P2 của Lượt 1 (trong phạm vi)

Đối chiếu toàn bộ phát hiện P0/P1/P2 thuộc phạm vi từ [w2b-review-promotion-result.md](.agents/briefs/fix/w2b-review-promotion-result.md) và [w2b-review-edge-result.md](.agents/briefs/fix/w2b-review-edge-result.md):

| ID Lượt 1 | Tóm tắt phát hiện | Trạng thái | Bằng chứng `file:dòng` & nhận xét |
|---|---|---|---|
| **Promo P0 #1** | Race `ReservePromotion` vs `ReleasePromotion` (thiếu tombstone PG + thiếu check `RELEASED` ở `ON CONFLICT`) | **ĐÃ SỬA** *(còn kẽ hở phụ ở Redis — xem Lỗi mới #4)* | [store.go:1182-1236,1372-1398](services/promotion/internal/store/store.go#L1182-L1236), [memory_store.go:1041-1047,1140-1152](services/promotion/internal/store/memory_store.go#L1041-L1047), [promotion.go:970-983,1001-1007](services/promotion/internal/service/promotion.go#L970-L983): `ReleaseReservation` đã ghi tombstone `status='RELEASED'`, `CommitReservation` và nhánh `ON CONFLICT` đều chặn bằng `ErrReservationAlreadyReleased`. Tuy nhiên [release_atomic.lua:11-14](services/promotion/internal/lua/release_atomic.lua#L11-L14) vẫn chưa ghi tombstone `"RELEASED"` lên Redis khi key chưa tồn tại. |
| **Promo P0 #2** | (A) Redis mất dữ liệu/evict mất toàn bộ `per_user_limit` & lệch khi release; (B) TTL 30 ngày cho đơn hoàn muộn | **CHƯA SỬA** *(nhánh A)* / **ĐÃ SỬA** *(nhánh B)* | Nhánh B đã nâng `DefaultReservationTTLSeconds` lên 400 ngày ([scripts.go:18](services/promotion/internal/lua/scripts.go#L18)). Nhánh A **chưa sửa**: [promotion.go:71-75](services/promotion/internal/service/promotion.go#L71-L75) chỉ thêm ghi chú `ponytail:` dựa vào AOF ([docker-compose.yml:22](docker-compose.yml#L22)) và chốt cuối Postgres. **Lập luận này không che hết:** (1) `CommitReservation` ([store.go:1274-1350](services/promotion/internal/store/store.go#L1274-L1350)) **không hề kiểm tra `per_user_limit`** cho voucher, flash item hay shop discount program, nên Redis wipe/evict vẫn reset mọi giới hạn mỗi người về 0; (2) `SyncRedisFromStore` không nạp lại key `:user:*` và `promo:res:*`, còn `ReleasePromotion`/`ReleasePromotionShop` không có fallback hoàn Redis từ PG khi thiếu `promo:res:*`. |
| **Promo P0 #3** | (A) `adjust_voucher.lua` clamp âm về `0` gây oversell; (B) `UpdateVoucher` TOCTOU sửa luật khi đang checkout; (C) PG commit trước, Redis lỗi sau lệch vĩnh viễn | **CHƯA SỬA** *(mới sửa nhánh A; nhánh B & C còn nguyên)* | Nhánh A **đã sửa**: [adjust_voucher.lua:38-41,59-61](services/promotion/internal/lua/adjust_voucher.lua#L38-L41) bỏ clamp âm về `0`, kết hợp chốt cuối PG `used_count < quota` và `used_budget_vnd + $2 <= budget_vnd` ([store.go:1286-1287](services/promotion/internal/store/store.go#L1286-L1287)). Nhánh B **chưa sửa**: [vouchers_w2b.go:34-51](services/promotion/internal/service/vouchers_w2b.go#L34-L51) vẫn chụp `liveUsed` trước `FOR UPDATE`, nên `ReservePromotion` chạy Lua ngay sau dòng 36 vẫn để `UpdateVoucher` sửa `discount_type`/`value_vnd`/`per_user_limit`/`category_ids` hoặc tắt `budget_vnd` (`>0 -> 0` gọi `DEL budgetKey` trong khi `release_atomic.lua` sau đó `INCRBY` tạo key zombie). Nhánh C **chưa sửa**: [vouchers_w2b.go:238-249](services/promotion/internal/service/vouchers_w2b.go#L238-L249) vẫn sync Redis sau commit PG và [scripts.go:213-216](services/promotion/internal/lua/scripts.go#L213-L216) vẫn dùng `SetNX`, không tự sửa khi restart. |
| **Promo P0 #4** | Race `ReservePromotion` vs `EndShopDiscountProgram` / `SetVoucherStatus(PAUSED)` / `DeleteVoucher` | **ĐÃ SỬA** *(nhưng sinh deadlock mới — xem Lỗi mới #2)* | [store.go:1274-1350](services/promotion/internal/store/store.go#L1274-L1350), [memory_store.go:1055-1093](services/promotion/internal/store/memory_store.go#L1055-L1093), [promotion.go:1001-1017](services/promotion/internal/service/promotion.go#L1001-L1017): chốt cuối nguyên tử trong PG chặn voucher `PAUSED`/`DELETED`/hết hạn/hết suất và chương trình `ENDED`/hết hạn, kèm `compensateRedisRelease`. |
| **Promo P0 #5** & **Edge P1 #4** | `ReservePromotion` bỏ qua qty `ShopItems` khi trùng `FlashSaleItems` → oversell *(và nhận end-user JWT / tin giá gRPC nội bộ)* | **ĐÃ SỬA** *(nhánh lệch qty)* | [promotion.go:850-856,1189-1237](services/promotion/internal/service/promotion.go#L850-L856) (`resolveFlashFromShopItems` lấy qty duy nhất từ `ShopItems` và trả `InvalidArgument` khi `FlashSaleItems` lệch). *(Phần tin giá trong request gRPC nội bộ là tồn đợt 3 đã biết, không báo lại.)* |
| **Promo P1 #6** | `ApproveFlashSaleItem` cho chuyển `APPROVED ↔ REJECTED` + retry idempotent lỗi tồn kho | **ĐÃ SỬA** | [promotion.go:615-627,665-676](services/promotion/internal/service/promotion.go#L615-L627), [store.go:706-713](services/promotion/internal/store/store.go#L706-L713), [memory_store.go:429-435](services/promotion/internal/store/memory_store.go#L429-L435). *(Không báo lại lỗi đã biết khi `InitFlashItemSlots` lỗi rồi gọi lại không init Redis.)* |
| **Promo P1 #7** | `GET /api/v1/admin/vouchers` mặc định rơi vào chế độ storefront buyer | **ĐÃ SỬA** | [handlers_promotion.go:739-742](services/gateway/internal/router/handlers_promotion.go#L739-L742) mặc định `include_inactive=true` khi bỏ trống query param và `status != ACTIVE`. *(Lưu ý: `GET /api/v1/seller/vouchers` ở [handlers_promotion.go:600-603](services/gateway/internal/router/handlers_promotion.go#L600-L603) chỉ tự bật `include_inactive=true` khi lọc `PAUSED`/`DELETED`, gọi trực tiếp không param vẫn cần truyền `?include_inactive=true` để thấy voucher `PAUSED`.)* |
| **Promo P1 #8** | `PreviewDiscounts` không đọc `promo:flash:<id>:remaining` khi flash hết suất | **ĐÃ SỬA** *(nhưng sinh lệch mới khi chồng Shop Discount Program — xem Lỗi mới #3)* | [promotion.go:1515-1517,1540-1548](services/promotion/internal/service/promotion.go#L1515-L1517), [calculator.go:606-608](services/promotion/internal/calculator/calculator.go#L606-L608). |
| **Promo P1 #9** | `UpdateShopDiscountProgram` xóa rồi thêm lại SKU tạo UUID mới reset `per_user_limit` | **ĐÃ SỬA** | [store/shop_discounts.go:16-22,72-74,226-229](services/promotion/internal/store/shop_discounts.go#L16-L22), [memory_store.go:673-675,752-754](services/promotion/internal/store/memory_store.go#L673-L675) dùng UUIDv5 ổn định `stableShopDiscountItemID(programID, skuID)`. *(Lưu ý biên: item tạo từ trước bản sửa mang UUIDv4 cũ vẫn đổi ID 1 lần nếu bị xóa rồi thêm lại.)* |
| **Promo P1 #10** | Retry `SHOP_PROG_USER_LIMIT_EXCEEDED` giới hạn cứng 2 lần làm fail checkout nhiều shop | **ĐÃ SỬA** | [promotion.go:861-868](services/promotion/internal/service/promotion.go#L861-L868) đặt `maxAttempts = countDistinctShopProgramItems + 1` trong `[2, 20]`. |
| **Promo P2 #11** | Voucher `0₫` và shop discount program `0₫` vẫn đốt quota/`per_user_limit` | **ĐÃ SỬA** | [calculator.go:227-232,309-311,399-401,459-461](services/promotion/internal/calculator/calculator.go#L227-L232). Tương thích với `order` ([checkout.go:1413-1451](services/order/internal/service/checkout.go#L1413-L1451)). |
| **Promo P2 #12** | `buildCalcInput` tin `category_id` client khi SKU rỗng/không tồn tại | **ĐÃ SỬA** | [promotion.go:1446-1452](services/promotion/internal/service/promotion.go#L1446-L1452) đặt `catID := ""` và chỉ lấy từ `skuMeta[skuID]`. |
| **Promo P2 #13** | `CreateShopDiscountProgram`/`UpdateShopDiscountProgram` thiếu kiểm tra `sku.GetIsActive()` | **ĐÃ SỬA** | [service/shop_discounts.go:392-394](services/promotion/internal/service/shop_discounts.go#L392-L394). |
| **Promo P2 #14** | `UpdateShopDiscountProgram` (không `replace_items`) bỏ qua kiểm tra trùng flash nếu item đã có sẵn cảnh báo | **ĐÃ SỬA** *(nhưng warning mới không lưu xuống DB — xem Lỗi mới #8)* | [service/shop_discounts.go:154-170](services/promotion/internal/service/shop_discounts.go#L154-L170). |
| **Promo P2 #15** | `CreateVoucher`/`UpdateVoucher` thiếu validate chéo `value_vnd`/`discount_percent` | **ĐÃ SỬA** | [promotion.go:170-184](services/promotion/internal/service/promotion.go#L170-L184), [vouchers_w2b.go:164-191](services/promotion/internal/service/vouchers_w2b.go#L164-L191). Tương thích REST `PUT` đổi loại giảm giá vì dòng `164-174` tự xóa field đối diện về `0` khi voucher chưa dùng. |
| **Promo P2 #16** & **Edge P2 #10** | Job `promotion.voucher_expiring` không loại voucher hết `budget_vnd` và user đã dùng hết `per_user_limit` | **ĐÃ SỬA** *(`budget_vnd`)* / **CHƯA SỬA** *(`per_user_limit`)* | [store.go:468](services/promotion/internal/store/store.go#L468) và [memory_store.go:281-283](services/promotion/internal/store/memory_store.go#L281-L283) đã thêm điều kiện `budget_vnd`. **Chưa sửa `per_user_limit`**: cả `store.go:460-474`, `memory_store.go:265-294` và [voucher_expiry_job.go](services/promotion/internal/events/voucher_expiry_job.go) vẫn bắn thông báo nhắc sắp hết hạn cho user đã dùng hết `v.per_user_limit`. |
| **Promo P2 #17** | `ProductService.AdminReviewProduct` gửi trùng outbox `product.reviewed` và duyệt hàng tồn `0` thành `APPROVED` | **ĐÃ SỬA** | [server.go:1084-1195](services/product/internal/server/server.go#L1084-L1195), [product.sql:189-194](services/product/queries/product.sql#L189-L194) (`FOR UPDATE`, chặn `HIDDEN`, no-op idempotent không phát outbox trùng, gọi `syncProductStockStatus` về `OUT_OF_STOCK`). |
| **Promo P2 #18** | `POST /api/v1/admin/fees/categories/{id}` thiếu `commission_rate_bps` bị hiểu là `0%` | **ĐÃ SỬA** | [handlers_product.go:355-366](services/gateway/internal/router/handlers_product.go#L355-L366) dùng `*int32` và bắt buộc khác `nil`. |
| **Edge P2 #5** *(phần product)* | Công khai `commission_rate_bps` trên REST công khai `GET /api/v1/products*` | **CHƯA SỬA** | [handlers_product.go:69,142,517,550,569](services/gateway/internal/router/handlers_product.go#L69) vẫn dùng chung `mapProduct`/`mapSKU` trả `commission_rate_bps` cho khách vãng lai/buyer. |
| **Edge P2 #6** | `ProductService.GetSKUsBatch` công khai không lọc sản phẩm chưa duyệt/ẩn/xóa mềm/SKU `INACTIVE` | **CHƯA SỬA** | [auth.go:165](pkg/common/auth/auth.go#L165), [server.go:1323-1356](services/product/internal/server/server.go#L1323-L1356), [product.sql:460-502](services/product/queries/product.sql#L460-L502) vẫn trả mọi SKU không lọc `p.deleted_at IS NULL` / `p.status` / `s.status`, và [handlers_promotion.go:1476-1534](services/gateway/internal/router/handlers_promotion.go#L1476-L1534) không kiểm tra `sk.GetIsActive()`. |

*(Bổ sung kiểm tra nhanh P3 lượt 1 trong phạm vi: `ErrForeignKeyViolation` ở `SetCategoryFeeSchedule`/`CreateProduct` **đã sửa** tại [server.go:486,679](services/product/internal/server/server.go#L486) và [pg_store.go:138-140,773-778](services/product/internal/store/pg_store.go#L138-L140); graceful shutdown `bgWG.Wait()` của `expiryJob` trong `promotion` **đã sửa** tại [main.go:127-131,213](services/promotion/cmd/server/main.go#L127-L131); nhánh `ReservePromotion` bare với voucher/shop program **đã chặn** tại [promotion.go:944-947](services/promotion/internal/service/promotion.go#L944-L947).)*

---

## 2. Phát hiện lỗi MỚI (ưu tiên lỗi do bản sửa sinh ra)

### P0 — Mất/lệch tiền, vượt quyền hoặc mất dữ liệu từ ngoài hệ thống
- **Không phát hiện lỗi P0 mới** từ ngoài hệ thống *(lưu ý các nhánh chưa sửa của Promo P0 #2 và P0 #3 đã thống kê ở mục 1)*.

---

### P1 — Lỗi tương thích chéo service, deadlock DB, sai số tiền giữa Preview và thực tế

#### 1. Bản sửa tombstone `Product.ReleaseStock` trả `Released: false` nhưng `ConfirmStock` trả `FailedPrecondition` làm vỡ tương thích với `order` (kẹt vòng lặp xác nhận/nhả kho)
- **Vị trí:**
  - [services/product/internal/server/server.go:1587-1598](services/product/internal/server/server.go#L1587-L1598) (`ReleaseStock`: trả `Released: false, AlreadyReleased: !inserted` trên nhánh tombstone, ngược với dòng [1612-1615](services/product/internal/server/server.go#L1612-L1615) trả `Released: true, AlreadyReleased: true` khi đã có dòng `RELEASED`)
  - [services/product/internal/server/server.go:1695-1704](services/product/internal/server/server.go#L1695-L1704) (`ConfirmStock`: đổi từ `codes.NotFound` sang `codes.FailedPrecondition` khi có tombstone)
  - Điểm gọi bị ảnh hưởng ở `order`: [checkout.go:1760-1777,1842-1857](services/order/internal/service/checkout.go#L1760-L1777) (`confirmGroupStockAndClear`) và [lifecycle.go:494-518](services/order/internal/service/lifecycle.go#L494-L518) (`restockOrderItemsWithConfirmErr`).
- **Mức độ:** `P1` (lỗi tương thích chéo service sinh ra trực tiếp từ bản sửa tombstone `000006_stock_release_tombstones.sql`).
- **Kịch bản cụ thể:**
  1. `ReleaseStock(G1)` chạy khi `G1` chưa có dòng nào trong `stock_reservations` (ví dụ: race hủy sớm/stale-checkout sweep trước khi `ReserveStock` kịp tạo dòng, hoặc đơn legacy/seeded từng qua luồng hủy). `product` ghi bản ghi vào `stock_release_tombstones` và trả `{Released: false, AlreadyReleased: false}`; mọi lần gọi `ReleaseStock(G1)` tiếp theo trả `{Released: false, AlreadyReleased: true}` (vẫn **`Released: false`**).
  2. Sau đó `ConfirmStock(G1)` được gọi (từ `confirmGroupStockAndClear` hoặc `restockOrderItemsWithConfirmErr`). Thay vì trả `codes.NotFound` như trước bản sửa, `product` thấy tombstone nên trả **`codes.FailedPrecondition`**.
  3. Phía caller `order` bắt `FailedPrecondition` → gọi lại `ReleaseStock(G1)` để xác nhận đơn đã xả kho rồi hay chưa và **chỉ clear cờ / bỏ qua restock khi `resp.GetReleased() == true`** ([checkout.go:1845](services/order/internal/service/checkout.go#L1845), [lifecycle.go:505](services/order/internal/service/lifecycle.go#L505)).
  4. Vì `product` trả `Released: false` dù `AlreadyReleased: true`, nhánh kiểm tra luôn thất bại → `confirmGroupStockAndClear` / `restockOrderItemsWithConfirmErr` trả lỗi `FailedPrecondition` lặp vô hạn (watchdog `ScanPendingStockConfirms` / consumer / quy trình hoàn hàng kẹt vĩnh viễn trên nhóm đơn đó).
- **Đề xuất sửa (1 dòng):** Khi `ReleaseStock` gặp tombstone đã tồn tại sẵn (`!inserted`), trả `Released: true, AlreadyReleased: true` nhất quán với nhánh `allReleased` ở [server.go:1613-1614](services/product/internal/server/server.go#L1613-L1614) (giữ nguyên lần đầu chưa có gì trả `Released: false, AlreadyReleased: false`).

#### 2. Deadlock AB-BA trên Postgres giữa `CommitReservation` (`FOR SHARE OF sdi, sdp`) và `UpdateShopDiscountProgram` (`sdp` trước → `sdi` sau)
- **Vị trí:**
  - [services/promotion/internal/store/store.go:1324-1342](services/promotion/internal/store/store.go#L1324-L1342) (`CommitReservation`)
  - [services/promotion/internal/store/shop_discounts.go:144-149,185-253](services/promotion/internal/store/shop_discounts.go#L144-L149) (`UpdateShopDiscountProgram`)
- **Mức độ:** `P1` (lỗi đua DB sinh ra trực tiếp từ bản sửa chốt cuối Postgres của lượt 1; gây `500 Internal` khi checkout và khóa cứng `promo:res:<order_id> = "RELEASED"`).
- **Kịch bản cụ thể:**
  1. `UpdateShopDiscountProgram(P1, replace_items=true)` lấy khóa `FOR UPDATE` trên bảng cha **`shop_discount_programs`** (`id = P1`, [shop_discounts.go:144-149](services/promotion/internal/store/shop_discounts.go#L144-L149)), rồi thực thi `UPDATE`/`DELETE` lấy khóa ghi trên bảng con **`shop_discount_program_items`** ([shop_discounts.go:205,249](services/promotion/internal/store/shop_discounts.go#L205)).
  2. Bản sửa lượt 1 trong `CommitReservation` chạy `SELECT sdi.id FROM shop_discount_program_items sdi JOIN shop_discount_programs sdp ON sdp.id = sdi.program_id WHERE sdi.id = $1 ... FOR SHARE OF sdi, sdp` ([store.go:1330-1342](services/promotion/internal/store/store.go#L1330-L1342)): PostgreSQL khóa hàng trên **`sdi` trước**, sau đó mới khóa **`sdp`** (ngược thứ tự khóa bảng của `UpdateShopDiscountProgram`, và cũng không sort theo `program_id` như ghi chú ở [store.go:1181](services/promotion/internal/store/store.go#L1181)).
  3. Khi buyer đặt hàng đúng lúc seller sửa chương trình giảm giá đang chạy (`replace_items=true`): Tx1 giữ `sdp(P1)` chờ `sdi(I1)`, Tx2 giữ `sdi(I1)` chờ `sdp(P1)` → Postgres hủy một giao dịch bằng `SQLSTATE 40P01` (deadlock). Nếu `CommitReservation` bị hủy, `ReservePromotion` trả `500 Internal` và chạy `compensateRedisRelease` đặt `promo:res:<order_id> = "RELEASED"`.
- **Đề xuất sửa (1 dòng):** Trong `CommitReservation`, khóa `shop_discount_programs` (`FOR SHARE`, sort theo `program_id ASC`) **trước** rồi mới khóa `shop_discount_program_items` (`FOR SHARE`, sort theo `item_id ASC`) để trùng hoàn toàn thứ tự khóa cha → con của `UpdateShopDiscountProgram` và `EndShopDiscountProgram`.

#### 3. Lệch số tiền giữa REST Preview, gRPC Preview và Reserve/Checkout khi SKU Flash Sale đã hết suất đồng thời có Shop Discount Program đang chạy
- **Vị trí:**
  - [services/gateway/internal/router/handlers_promotion.go:1449-1467,1525-1532](services/gateway/internal/router/handlers_promotion.go#L1525-L1532)
  - [services/promotion/internal/service/promotion.go:1401,1455](services/promotion/internal/service/promotion.go#L1401)
  - [services/promotion/internal/calculator/calculator.go:191-215](services/promotion/internal/calculator/calculator.go#L191-L215)
  - Đối chiếu điểm gọi `order`: [services/order/internal/service/checkout.go:1244,1367-1369](services/order/internal/service/checkout.go#L1244).
- **Mức độ:** `P1` (sai số tiền giữa các đường Preview và thực tế Checkout, bộc lộ trực tiếp từ bản sửa `CheckRemaining` trong `PreviewDiscounts`).
- **Kịch bản cụ thể:**
  - SKU `S1` giá gốc `200,000₫` đang đồng thời có Flash Sale `F1` (`APPROVED`, giá `100,000₫`, **`remaining_slots = 0`**) và Shop Discount Program `P1` (`ACTIVE`, giá `150,000₫`) — hệ thống cho phép chồng lấn (chỉ cảnh báo khi tạo).
  - Người mua gửi `{sku_id: "S1", quantity: 1, flash_sale_item_id: "F1"}`:
    1. **Qua REST `POST /api/v1/promotions/preview`**: Gateway tra `ListActiveShopDiscounts` cho mọi SKU và gắn **cả `FlashSaleItemId: "F1"` lẫn `ShopDiscountProgramItemId: "P1"`** lên cùng dòng ([handlers_promotion.go:1525-1532](services/gateway/internal/router/handlers_promotion.go#L1525-L1532)). Vì bản sửa lượt 1 bật `CheckRemaining=true`, `calculator.go:191-212` bỏ qua `F1` (`flashApplied = false`, ghi cảnh báo hết suất), rồi dòng `214` (`if !flashApplied && item.ShopDiscountProgramItemID != ""`) **rơi xuống áp `P1`** → REST Preview trả **`150,000₫`** (`shop_program_discount_vnd = 50,000₫`).
    2. **Qua gRPC `PreviewDiscounts` trực tiếp** (không truyền sẵn `ShopDiscountProgramItemId`): [promotion.go:1401,1455](services/promotion/internal/service/promotion.go#L1401) chỉ auto-lookup shop program khi `fiID == ""`, nên bỏ qua `P1` → trả giá thường **`200,000₫`** (lệch `50,000₫` so với REST Preview).
    3. **Khi đặt hàng (`ReservePromotion` / `CreateCheckout`)**: `order` bỏ qua `ListActiveShopDiscounts` với dòng có `flash_sale_item_id` ([checkout.go:1244](services/order/internal/service/checkout.go#L1244)) và `ReservePromotion` (`strict=true`) trả lỗi `FLASH_SOLD_OUT` chứ không áp `150,000₫`. Ngược lại nếu client xóa `flash_sale_item_id` để đặt lại thì checkout tính `150,000₫`, khớp REST Preview nhưng lệch gRPC Preview (`200,000₫`).
- **Đề xuất sửa (1 dòng):** Thống nhất một quy tắc duy nhất giữa `promotion.go`, `calculator.go` và `handlers_promotion.go`: nếu `FlashSaleItemId` được chỉ định nhưng hết suất trong Preview thì hoặc không rơi ngầm sang `ShopDiscountProgramItemId` (`calculator.go:214` kiểm tra `item.FlashSaleItemID == ""` và gateway không gắn `ShopDiscountProgramItemId` khi đã có `FlashSaleItemId`), hoặc `promotion.go:1401,1455` auto-lookup shop program cho cả SKU có `FlashSaleItemId` để gRPC Preview khớp REST Preview.

---

### P2 — Ranh giới tin cậy gRPC, race rò rỉ Redis khi crash, lỗi biên đa replica

#### 4. `release_atomic.lua` không ghi tombstone `"RELEASED"` lên Redis khi `promo:res:<order_id>` chưa tồn tại → rò rỉ counter Redis nếu crash/lỗi Redis lúc bù trừ
- **Vị trí:** [services/promotion/internal/lua/release_atomic.lua:11-14](services/promotion/internal/lua/release_atomic.lua#L11-L14), [services/promotion/internal/lua/scripts.go:142-158](services/promotion/internal/lua/scripts.go#L142-L158), [services/promotion/internal/service/promotion.go:1002,1054-1062,1154-1163](services/promotion/internal/service/promotion.go#L1002)
- **Mức độ:** `P2` (chốt PG đã ngăn oversell/đơn lậu, nhưng để hở cửa sổ rò rỉ suất/quota/budget/per-user trên Redis không tự hồi phục).
- **Kịch bản cụ thể:**
  1. `ReleasePromotion(G1)` đến **trước** khi `ReservePromotion(G1)` chạy `reserve_atomic.lua`: PG ghi tombstone `status='RELEASED'`, nhưng `release_atomic.lua` thấy `GET promo:res:G1 == nil` nên trả ngay `ALREADY_RELEASED` **mà không ghi `SET promo:res:G1 "RELEASED" EX <ttl>`**. `ReleasePromotion` trả thành công và sẽ không được gọi lại.
  2. `ReservePromotion(G1)` chạy trễ: gọi `reserve_atomic.lua` thành công (vì Redis trống) → trừ suất flash, quota/budget voucher và tăng bộ đếm `per_user_limit`; sau đó bị PG `CommitReservation` từ chối (`ErrReservationAlreadyReleased`) và chỉ còn trông chờ `compensateRedisRelease(G1)` (best-effort, nuốt lỗi chỉ ghi log).
  3. Nếu pod `promotion` bị kill/OOM hoặc Redis lỗi ngắn đúng 5 giây đó, mọi counter vừa trừ trên Redis bị **rò rỉ vĩnh viễn**.
- **Đề xuất sửa (1 dòng):** Truyền `DefaultReservationTTLSeconds` vào `release_atomic.lua` và khi `raw == false` (key chưa tồn tại) ghi `redis.call('SET', resKey, 'RELEASED', 'EX', ttl)` trước khi trả `ALREADY_RELEASED` để `reserve_atomic.lua` chặn ngay từ tầng Redis.

#### 5. `ProductService.ReleaseStock` và `ConfirmStock` thiếu kiểm tra `requireServiceOrAdmin(ctx)` — bất kỳ JWT `buyer`/`seller` nào gọi gRPC nội bộ đều xả/xác nhận được kho của đơn bất kỳ
- **Vị trí:**
  - [services/product/internal/server/server.go:1565-1569](services/product/internal/server/server.go#L1565-L1569) (`ReleaseStock`)
  - [services/product/internal/server/server.go:1673-1677](services/product/internal/server/server.go#L1673-L1677) (`ConfirmStock`)
  - Đối chiếu [pkg/common/auth/auth.go:63-65,421-434](pkg/common/auth/auth.go#L63-L65) và [server.go:1802-1806](services/product/internal/server/server.go#L1802-L1806) (`RestockOrderItems` có gọi `requireServiceOrAdmin`), [promotion.go:1037,1073](services/promotion/internal/service/promotion.go#L1037) (`ReleasePromotion`/`ReleasePromotionShop` có gọi `requireAdminOrService`).
- **Mức độ:** `P2` (cổng gRPC `product:50052` chỉ mở nội bộ Docker, không public qua gateway → lỗi ranh giới tin cậy gRPC).
- **Kịch bản cụ thể:**
  - `auth.go:63-64` khai báo `ReleaseStock: true, ConfirmStock: true` trong `internalRPCs`. `GRPCUnaryServerInterceptor` ([auth.go:421-425](pkg/common/auth/auth.go#L421-L425)) chấp nhận **mọi end-user JWT hợp lệ** (kể cả `role=buyer`) và chuyển vào handler.
  - Phía `order` luôn gọi `ReleaseStock`/`ConfirmStock` bằng service token (`clients.WithInternalAdminContext`, được kiểm chứng tại [services/order/internal/service/grpc_trust_test.go:197-208](services/order/internal/service/grpc_trust_test.go#L197-L208)).
  - Vì `ReleaseStock` và `ConfirmStock` không gọi `requireServiceOrAdmin(ctx)`, bất kỳ caller nào trên mạng nội bộ cầm JWT `buyer`/`seller` thường đều có thể xả kho + cắm tombstone `stock_release_tombstones` vào `order_id` của người khác, hoặc gọi `ConfirmStock` trừ tồn thật của đơn chưa thanh toán.
- **Đề xuất sửa (1 dòng):** Thêm `if _, err := requireServiceOrAdmin(ctx); err != nil { return nil, err }` ngay đầu `ReleaseStock` và `ConfirmStock` (đồng bộ với `RestockOrderItems`), và bổ sung test từ chối buyer/seller JWT trong [grpc_trust_test.go](services/product/internal/server/grpc_trust_test.go).

#### 6. IDOR đọc `DiscountBreakdown` chéo người dùng qua các nhánh replay idempotent của `PromotionService.ReservePromotion`
- **Vị trí:** [services/promotion/internal/service/promotion.go:823-831,970-980,1019-1025](services/promotion/internal/service/promotion.go#L823-L831), [services/promotion/internal/store/store.go:1189-1194,1226-1235](services/promotion/internal/store/store.go#L1189-L1194)
- **Mức độ:** `P2` (ranh giới tin cậy gRPC nội bộ, cổng `50054` không public qua gateway).
- **Kịch bản cụ thể:**
  - `ReservePromotion` chấp nhận end-user JWT (`auth.go:104`). Cả 3 nhánh idempotency (`GetReservation` dòng 823, `luaOutcome.AlreadyReserved` dòng 970, `alreadyCommitted` dòng 1019) đều trả thẳng `existing.BreakdownJSON` mà **không kiểm tra `existing.UserID == userID`**.
  - Caller mang JWT của `buyer-B` gọi `ReservePromotion(order_id = <checkout_group_id của buyer-A>)` nhận về đầy đủ `DiscountBreakdown` (shop, voucher, mức giảm, xu, tổng tiền) của `buyer-A`.
- **Đề xuất sửa (1 dòng):** Kiểm tra `if existing.UserID != "" && existing.UserID != userID` → trả `codes.PermissionDenied` ở cả 3 nhánh replay (trừ admin/service).

#### 7. Phá hòa `category_fee_schedules` bằng `created_at` từ đồng hồ ứng dụng Go đứng trước `seq` DB → lệch đồng hồ giữa các replica khiến lịch phí cũ ghi đè lịch phí mới
- **Vị trí:**
  - [services/product/internal/server/server.go:446,471](services/product/internal/server/server.go#L446) (`CreatedAt: now` lấy từ đồng hồ Go của từng instance)
  - [services/product/queries/product.sql:20,76,94,113,168,382,427,471,515,558](services/product/queries/product.sql#L20) (cả 10 truy vấn đều dùng `ORDER BY effective_from DESC, created_at DESC, seq DESC`)
  - [services/product/migrations/000005_w2b_catalog_outbox_price_fee.sql:33](services/product/migrations/000005_w2b_catalog_outbox_price_fee.sql#L33) (`seq BIGSERIAL NOT NULL`)
- **Mức độ:** `P2` (lệch phí hoa hồng khi đa replica có sai lệch đồng hồ NTP hoặc NTP step lùi).
- **Kịch bản cụ thể:**
  - Admin tạo lịch phí `800 bps` cho `effective_from = T0` vào Replica A (đồng hồ nhanh hơn 200ms → lưu `created_at = T + 200ms, seq = 10`).
  - 100ms sau admin sửa thành `500 bps` cho cùng `effective_from = T0` vào Replica B (đồng hồ chuẩn → lưu `created_at = T + 100ms, seq = 11`).
  - Vì mọi câu SQL đặt `created_at DESC` đứng trước `seq DESC`, lịch phí cũ `800 bps` (`seq = 10`) luôn thắng lịch phí mới `500 bps` (`seq = 11`).
- **Đề xuất sửa (1 dòng):** Đổi tie-break trong `product.sql` thành `ORDER BY effective_from DESC, seq DESC` (và sinh lại `sqlc`), hoặc lấy `created_at` từ `NOW()` của Postgres nhưng vẫn ưu tiên `seq DESC`.

---

### P3 — Nhỏ / Đồng nhất dữ liệu hiển thị

#### 8. `UpdateShopDiscountProgram` không `replace_items` không ghi ngược cảnh báo chồng lấn Flash Sale vào cột `warning` trong DB
- **Vị trí:** [services/promotion/internal/service/shop_discounts.go:154-170](services/promotion/internal/service/shop_discounts.go#L154-L170), [services/promotion/internal/store/shop_discounts.go:255-261](services/promotion/internal/store/shop_discounts.go#L255-L261)
- **Mức độ:** `P3`.
- **Kịch bản cụ thể:** Seller gia hạn `ends_at` mà không bật `replace_items` khiến chương trình chạm khung Flash Sale đã duyệt. Response `UpdateShopDiscountProgram` trả đúng `warnings`, nhưng `store/shop_discounts.go:255-261` không cập nhật cột `warning` trong bảng `shop_discount_program_items`, nên lần gọi `ListShopDiscountPrograms` sau đó item bị mất cảnh báo chồng lấn Flash Sale.
- **Đề xuất sửa (1 dòng):** Khi `!replaceItems` mà phát hiện cảnh báo mới trên item, cập nhật cột `warning` của các item bị ảnh hưởng trong cùng transaction `UpdateShopDiscountProgram`.

---

## 3. Kiểm định chất lượng các test mới (có thật sự đỏ khi bỏ bản sửa không?)

### A. Các test **KHÔNG chứng minh được** (hoặc chỉ chứng minh một nửa — vẫn XANH nếu gỡ bản sửa thật trên Postgres)

1. **[w2b_round2_test.go:213-319](services/product/internal/server/w2b_round2_test.go#L213-L319) — `TestW2B_Round2_ReleaseBeforeReserve_TombstonePreventsLeakedStock` (phần 2 — concurrent race):**
   - **Không chứng minh được `LockStockOrder`:** Chạy trên `MemoryStore`, nơi `LockStockOrder` là **no-op (`return nil`)** ([memory_store.go:1928](services/product/internal/store/memory_store.go#L1928)) và mọi `memTx` đã tuần tự hóa bằng một mutex toàn cục. Nếu xóa `q.LockStockOrder(ctx, orderID)` khỏi [server.go:1448,1580,1688](services/product/internal/server/server.go#L1448), test này **vẫn XANH 100%**.
   - *(Phần 1 tuần tự **có đỏ thật** nếu bỏ `HasStockReleaseTombstone`/`InsertStockReleaseTombstone` trong `server.go`, nhưng không test gọi lại `ReleaseStock` sau `ConfirmStock -> FailedPrecondition`, nên bỏ lọt Lỗi mới #1.)*
2. **[w2b_product_test.go:156-375](services/product/internal/server/w2b_product_test.go#L156-L375) — `TestW2B_CategoryTree_CATB9_CycleRecursiveSlugPresenceFlags` (phần §3b — concurrent reparent):**
   - **Không chứng minh được `LockCategoryTree`:** Chạy trên `MemoryStore`, nơi `LockCategoryTree` cũng là **no-op (`return nil`)** ([memory_store.go:1280](services/product/internal/store/memory_store.go#L1280)). Xóa lời gọi `LockCategoryTree` khỏi `server.go` thì test này **vẫn XANH 100%**.
3. **[w2b_round2_test.go:321-380](services/product/internal/server/w2b_round2_test.go#L321-L380) — `TestW2B_Round2_FKViolationMappedToNotFound_NeverInternal`:**
   - **Không chứng minh được mapping lỗi SQLSTATE `23503` của `pg_store.go`:** Dùng wrapper `fkErrorStore` tự tiêm sẵn `store.ErrForeignKeyViolation`. Nếu xóa hẳn nhánh bắt `pgconn.PgError` code `"23503"` trong [pg_store.go:138-140,773-778](services/product/internal/store/pg_store.go#L138-L140), test này **vẫn XANH** (chỉ đỏ khi xóa `case errors.Is(err, store.ErrForeignKeyViolation)` trong `server.go`).
4. **[promotion_w2b_test.go:1628-1708](services/promotion/internal/service/promotion_w2b_test.go#L1628-L1708) — `TestService_R3_AuditFixes_.../A_CommitGateFailureCompensatesRedis`:**
   - **Chỉ chứng minh `MemoryStore` + `compensateRedisRelease`, không chứng minh Postgres:** Nếu xóa sạch các chốt cuối SQL (`status='ACTIVE'`, `used_count < quota`, `remaining_slots >= $2`, `FOR SHARE OF sdi, sdp`, tombstone `INSERT ... ON CONFLICT`) trong [store.go:1182-1398](services/promotion/internal/store/store.go#L1182-L1398), test này và toàn bộ `go test ./...` mặc định **vẫn XANH 100%**. Ngoài ra không chạy song song `CommitReservation` với `UpdateShopDiscountProgram`, nên bỏ lọt deadlock AB-BA (Lỗi mới #2).
5. **[voucher_expiry_job_test.go:315-380](services/promotion/internal/events/voucher_expiry_job_test.go#L315-L380) — `TestVoucherExpiryJob_R3_SkipsBudgetExhaustedVoucher`:**
   - **Không chứng minh được SQL ở `PostgresStore`:** Chạy trên `MemoryStore` nên xóa điều kiện `budget_vnd` tại [store.go:468](services/promotion/internal/store/store.go#L468) vẫn để `go test ./...` mặc định **XANH**.
6. **[store_pg_test.go](services/promotion/internal/store/store_pg_test.go) và [pg_integration_test.go](services/product/internal/server/pg_integration_test.go):**
   - Logic assertion bên trong các test Postgres này **tốt và sẽ đỏ thật** khi gỡ bản sửa SQL/lock/tombstone/FK mapping trên Postgres thật.
   - **Điểm yếu lớn:** Cả 2 file đều tự `t.Skip` khi `TEST_PROMOTION_DB_URL` / `TEST_PRODUCT_DB_URL` chưa đặt. Trong `go test ./...` thường ngày, **0% SQL, advisory lock, `FOR SHARE`/`FOR UPDATE` và trigger/FK mapping của `PostgresStore`/`pg_store`** được chạy. *(Ngoài ra `store_pg_test.go` thiếu bài test đua đồng thời `CommitReservation` + `UpdateShopDiscountProgram(replace_items=true)` nên không phát hiện Lỗi mới #2.)*

### B. Các test **ĐỎ THẬT** khi bỏ bản sửa (kiểm chứng logic đạt)

- **[lua_w2b_test.go](services/promotion/internal/lua/lua_w2b_test.go):** Tất cả 5 test (`L33`, `L90`, `L118`, `L214`, `L285`) chạy trên `miniredis` thật với script Lua thật — **đỏ thật** nếu gỡ check `statusKey`, khôi phục clamp âm về `0` trong `adjust_voucher.lua`, giảm TTL về 30 ngày, hoặc hỏng per-user limit.
- **[calculator_w2b_test.go](services/promotion/internal/calculator/calculator_w2b_test.go):** Tất cả 4 test (`L94`, `L439`, `L474`, `L636`) test trực tiếp `calculator.Calculate` — **đỏ thật** nếu phá công thức tiền, bỏ `CheckRemaining`, hoặc cho voucher/program `0₫` lọt vào `Applied*`. *(Thiếu case kết hợp flash sold-out + shop discount program → bỏ lọt Lỗi mới #3.)*
- **[promotion_w2b_test.go:1711-2124](services/promotion/internal/service/promotion_w2b_test.go#L1711-L2124) — Subtest `D`, `E`, `F`, `G`, `H`, `I_J_K_L`:** Đều **đỏ thật** ở tầng service/miniredis nếu gỡ `resolveFlashFromShopItems` (`D`), state machine flash (`E`, trên `MemoryStore`), đọc `FlashRemainingKey` + missing key = 0 (`F`), `stableShopDiscountItemID` (`G`, trên `MemoryStore`), `maxAttempts` hội tụ 3-shop (`H`), 0₫ voucher / spoofed `category_id` / inactive SKU / non-replace flash overlap / cross-field voucher / bare reserve reject (`I_J_K_L`).
- **[w2b_round2_test.go:17-211](services/product/internal/server/w2b_round2_test.go#L17-L211) — `TestW2B_Round2_AdminReviewProduct_...`:** **Đỏ thật** nếu bỏ idempotency không phát outbox trùng, bỏ `syncProductStockStatus` cho sản phẩm tồn 0, hoặc bỏ chặn duyệt sản phẩm `HIDDEN`.
- **[outbox_test.go](services/product/internal/events/outbox_test.go)** (`L70`, `L147`), **[client_test.go](services/promotion/internal/productclient/client_test.go)** (`L53`), **[config_test.go](services/promotion/internal/config/config_test.go)** (`L9`): Đều **đỏ thật** khi gỡ hành vi tương ứng.
- **Diff trên các test cũ** ([server_test.go](services/product/internal/server/server_test.go), [promotion_test.go](services/promotion/internal/service/promotion_test.go), [promotion_fix_test.go](services/promotion/internal/service/promotion_fix_test.go)): Chỉ cập nhật theo đặc tả/luật mới (CAT-B8, bắt buộc `ShopItems` khi reserve voucher, state machine flash chặt); **không phát hiện trường hợp hạ thấp assertion để che lỗi**.

---

## 4. Các file đã đọc và không phát hiện lỗi mới

- **`services/promotion`:** [003_w2b_promotions.sql](services/promotion/internal/store/migrations/003_w2b_promotions.sql), [002_partial_release.sql](services/promotion/internal/store/migrations/002_partial_release.sql), [config.go](services/promotion/internal/config/config.go), [models.go](services/promotion/internal/domain/models.go), [release_plan.go](services/promotion/internal/store/release_plan.go), [consumer.go](services/promotion/internal/events/consumer.go), [productclient/client.go](services/promotion/internal/productclient/client.go), [flash_w2b.go](services/promotion/internal/service/flash_w2b.go), [release_partial_atomic.lua](services/promotion/internal/lua/release_partial_atomic.lua), [reserve_atomic.lua](services/promotion/internal/lua/reserve_atomic.lua), [cmd/server/main.go](services/promotion/cmd/server/main.go).
- **`services/product`:** [000001_init.sql](services/product/migrations/000001_init.sql), [000005_w2b_catalog_outbox_price_fee.sql](services/product/migrations/000005_w2b_catalog_outbox_price_fee.sql), [000006_stock_release_tombstones.sql](services/product/migrations/000006_stock_release_tombstones.sql), [migrations/embed.go](services/product/migrations/embed.go), [internal/events/outbox.go](services/product/internal/events/outbox.go), [internal/events/types.go](services/product/internal/events/types.go), [cmd/product/main.go](services/product/cmd/product/main.go), [internal/store/store.go](services/product/internal/store/store.go).
- **Proto & hợp đồng:** [promotion.proto](proto/nexusmart/promotion/v1/promotion.proto), [product.proto](proto/nexusmart/product/v1/product.proto).
