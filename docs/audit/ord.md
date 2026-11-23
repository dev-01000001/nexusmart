# Báo cáo rà soát nghiệp vụ – `ord` (Giỏ hàng, Checkout, Đơn hàng, Trả hàng, Khiếu nại)

- **Commit rà soát**: `0db2cdd` (`HEAD`) · **Stack**: `nexusmart-audit` (`:48080` gateway, `:43000` web, `:48025` Mailpit, `:48222` NATS) · **Ngày**: 2026-11-23
- **Tài khoản kiểm thử riêng**: `audit-ord-1@nexusmart.local`, `audit-ord-2@nexusmart.local`; sản phẩm & voucher tạo mới đều có tiền tố `AUDIT-ord`.
- **Tổng quan 14 UC**: `ĐỦ`: **1** (`UC-B05`) · `MỘT PHẦN`: **2** (`UC-B08`, `UC-S04`) · `LỖI`: **5** (`UC-B06`, `UC-B09`, `UC-B17`, `UC-PAY02`, `UC-PAY06`) · `THIẾU`: **6** (`UC-B26`, `UC-S10`, `UC-S15`, `UC-A05`, `UC-PAY08`, `UC-A16`; trong đó 2 UC mức Thấp) · `CHƯA KIỂM`: **0**.

---

## 1. Bảng UC

| UC | Ưu tiên | Trạng thái | Backend (`file:dòng`) | REST | Web | Bằng chứng | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| UC-B05 | Cao | **ĐỦ** | `services/cart/internal/server/server.go:59-258`; `services/cart/internal/store/redis_store.go:45-160` | `GET/DELETE /api/v1/cart`, `POST /api/v1/cart/items`, `PATCH/DELETE /api/v1/cart/items/{sku_id}` (`router.go:176-180`) | `/gio-hang` (`web/src/app/(shop)/gio-hang/page.tsx`) | Stack: SKU lạ→`404`; SKU `PENDING`→`409 SKU_NOT_ACTIVE`; hết hàng→`409 OUT_OF_STOCK`; thêm 10 vượt tồn 5→`200 clamped_to_stock:true, total_items:5`; đổi giá 100k→125k→`price_changed:true`; giỏ 2 shop→`2 shop_groups, 497.000₫`, giữ nguyên sau logout/login (`cart:<uid>` Redis TTL 30d). Unit test `services/cart` PASS. | Thiếu RPC `SetItemSelected` lưu cờ chọn vào Redis (mất chọn khi reload web); `GetCart` chưa tự kẹp lại số lượng đã lưu nếu tồn giảm sau lúc thêm. |
| UC-B06 | Cao | **LỖI** | `services/order/internal/service/checkout.go:63-520,1130-1170`; `services/order/internal/events/outbox.go` | `POST /api/v1/checkout` (`router.go:188`) | `/thanh-toan` (`CheckoutClient.tsx`) | Stack CO1 (2 shop COD + shopV 20k + platV 30k + 5k xu)→`201`, tách 2 đơn `NM-20261122-CBEA865F` (`174.770₫`, ship `27.500₫`) & `NM-20261122-2E672E43` (`330.230₫`, ship `35.500₫`), xoá giỏ, phát outbox `order.created`/`order.confirmed`. CO2 (2 shop VietQR)→`201`, `reserved_stock` tăng `0→1` ở cả 2 SKU. Replay cùng key+payload→`200 idempotent_replay:true`. | **P0 `ORD-B1`** (trừ voucher sàn & xu vào tiền trả seller, đơn 0₫ bỏ qua `CreatePayment`), **P1 `ORD-B4`** (cùng `Idempotency-Key` khác payload vẫn trả đơn cũ), **P2 `ORD-B10`**. |
| UC-B08 | Cao | **MỘT PHẦN** | `services/order/internal/service/lifecycle.go:34-150`; `services/notification/internal/service/notification.go:395-613` | `GET /api/v1/orders`, `GET /api/v1/orders/{id}`, `GET /api/v1/orders/{id}/timeline` (`router.go:189-191`) | `/don-hang`, `/don-hang/[id]`, `/tra-cuu-don-hang` | Stack: buyer1 đọc list/detail/timeline `200`, SSR `/don-hang` & `/don-hang/95c18110-...` khớp `order_db`; buyer2 gọi GET/timeline→`403`; thông báo đổi trạng thái nhận đủ ở `/api/v1/notifications`. | Dính **`ORD-B10`** (link thông báo `payment.succeeded` trỏ `checkout_group_id`→`404`), **`ORD-B12`** (timeline còn tiếng Anh thô, thiếu tab lọc `RETURNED`), **`ORD-D1`** (`/tra-cuu-don-hang` 100% mock). |
| UC-B09 | Trung bình | **LỖI** | `services/order/internal/service/lifecycle.go:155-350,667-760`; `services/product/internal/server/server.go` (`RestockOrderItems`) | `POST /api/v1/orders/{id}/cancel`, `POST /api/v1/orders/{id}/return` (`router.go:192,194`) | `/don-hang/[id]` (`OrderDetailClient.tsx:194-275`) | Stack: huỷ VietQR `PENDING` CO3→`200 CANCELLED`, hoàn tồn `32|1→32|0`, hoàn voucher `2→1`, hoàn xu `13k→15k`; huỷ đơn con VietQR đã trả (`bf90a979-...`)→`200 CANCELLED/REFUNDED`, hoàn tồn `29→30` (PR #2 đã sửa), tạo refund `193.000₫`; huỷ khi `SHIPPING`/`DELIVERED`→`409`; `RequestReturn` khi `DELIVERED`→`200 RETURN_REQUESTED` (lưu `return_requests`). | **P0 `ORD-B2`** (huỷ COD không hoàn sổ cái), **P0 `ORD-B3`** (huỷ 1 đơn con đa shop mất vĩnh viễn xu), **P1 `ORD-B7`** (`BuyerConfirmReceived` cho hoàn tất đơn `RETURN_REQUESTED` bỏ mồ côi `return_requests`, web gửi `evidence_urls:[]` không cho tải ảnh), **P2 `ORD-B8`** (huỷ VietQR `PENDING` giữ `payment_status=PENDING`). |
| UC-B17 | Cao | **LỖI** | `services/order/internal/service/lifecycle.go:585-664`; `services/order/internal/statemachine/statemachine.go:32-39` | `POST /api/v1/orders/{id}/confirm-received` (`router.go:193`) | `/don-hang/[id]` (`OrderDetailClient.tsx:222-240`) | Stack: `confirm-received` khi `CONFIRMED`→`409`; khi `DELIVERED` (`95c18110-...`)→`200 COMPLETED`, cộng `+1.790` xu (`1%` subtotal) qua `order.completed`, giải ngân seller. | **P0 `ORD-B1`** (seller nhận thiếu `11.730₫` do trừ voucher sàn & xu), **P1 `ORD-B7`** (cho phép `confirm-received` trên đơn đang `RETURN_REQUESTED`), **P2 `ORD-B9`** (giải ngân thẳng `available_vnd` bỏ qua `pending_vnd`, thiếu job tự `COMPLETED` sau 7 ngày). |
| UC-B26 | Trung bình | **THIẾU** | Đã tìm `rg "UpdateOrderAddress\|ChangeAddress\|shipping_address" proto/ services/order/ services/shipping/` → không có RPC sửa địa chỉ | Không có route trong `services/gateway/internal/router/router.go` | Không có trên `/don-hang/[id]`; chỉ có form mock ở `OrderLookupView.tsx` | Không có RPC, handler, hay luồng tính lại phí ship/cập nhật vận đơn sau khi đặt. | Chưa triển khai cả backend lẫn web thật. |
| UC-S04 | Cao | **MỘT PHẦN** | `services/order/internal/service/lifecycle.go:355-583`; `services/order/cmd/server/main.go:105-122` | `GET /api/v1/seller/orders`, `GET /api/v1/seller/orders/{id}`, `POST /confirm`, `POST /ship` (`router.go:233-236`); `POST /api/v1/orders/{id}/cancel` | `/seller/don-hang`, `/seller/don-hang/[id]` | Stack: seller1 `confirm` (`CONFIRMED→PREPARING`)→`200`; `ship`→`200 SHIPPING` + tạo vận đơn GHN `GHN-1E8BD14AE3`; `ship` thẳng từ `CONFIRMED`→`200`; seller2 gọi GET/confirm/ship/cancel đơn shop1→`403` (4/4). | Thiếu job tự huỷ đơn không xác nhận trong 48h + ghi vi phạm (`main.go` chỉ có outbox & expiry watchdog); `confirm` (`PREPARING`) không phát event thông báo cho buyer; hiển thị tiền thực nhận lệch DB (`ORD-D3`/`ORD-B1`); ảnh bìa đơn seller hardcode (`ORD-B12`). |
| UC-S10 | Trung bình | **THIẾU** | Chỉ có `INSERT INTO return_requests` (`services/order/queries/orders.sql:201`). Đã tìm `rg "ResolveReturn\|ApproveReturn\|RejectReturn\|ListReturn\|return_requests" proto/ services/` → 0 RPC đọc/xử lý | Không có route trong `router.go` | Không có giao diện duyệt/từ chối trả hàng ở `/seller/don-hang/[id]` | Đơn `747070cd-...` tạo được bản ghi `return_requests`, nhưng seller không có API xem hay duyệt/từ chối. | Thiếu toàn bộ vòng đời xử lý trả hàng, auto-accept 3 ngày và tạo vận đơn chiều ngược (`UC-SHIP08`). |
| UC-S15 | Trung bình | **THIẾU** | Đã tìm `rg "BatchShip\|BatchConfirm\|PickingList\|BulkLabel\|merge.*pdf" proto/ services/order/ services/shipping/` → không có | Không có route trong `router.go` | `/seller/don-hang/page.tsx:218-240` chỉ có nút "Xác nhận đơn chờ" gọi tuần tự `/confirm` từng đơn ở client | Không có job nền tạo vận đơn hàng loạt, không gộp nhãn PDF, không có picking list theo SKU, không chọn nhiều đơn để giao. | Chưa triển khai backend/job nền và UI chọn lô/in nhãn. |
| UC-A05 | Trung bình | **THIẾU** | Đã tìm `rg "Dispute\|disputes\|KhieuNai" proto/ services/order/ services/payment/` → không có bảng, proto, hay RPC | Không có route trong `router.go` | `/admin/khieu-nai/page.tsx` dùng 100% `useMockStore().disputes` | Đơn `RETURN_REQUESTED` thật (`NM-20261122-E885BBAC`) không xuất hiện ở `/admin/khieu-nai`; trang chỉ hiện case mock `KN-...`. | Xem thêm `ORD-D2`. |
| UC-PAY02 | Cao | **LỖI** | `services/order/internal/service/checkout.go:196-256`; `services/order/internal/service/handlers.go:223-249`; `services/payment/src/service.rs:189-196,380-405` | `POST /api/v1/checkout` (`payment_method:"COD"`) | `/thanh-toan` (`CheckoutClient.tsx`) | Stack: COD `5.500.000₫` không promo→`409 COD_LIMIT_EXCEEDED`; COD `5.500.000₫` trừ 30k promo (`5.470.000₫ > 5M`)→`409` kèm hoàn voucher `1→1` & hoàn tồn `5|0→5|0`. | **P1 `ORD-B6`** (`cod_rejected_count` không bao giờ được tăng nên chặn bom hàng vô hiệu), **P0 `ORD-B2`** (huỷ/hoàn COD không hoàn sổ cái), **P2 `ORD-B8`** (COD bị đánh dấu `SUCCEEDED` ngay lúc đặt), thiếu kiểm tra `phone_verified` (tiền điều kiện spec) và vùng hãng hỗ trợ COD. |
| UC-PAY06 | Cao | **LỖI** | `services/order/internal/service/handlers.go:254-409`; `services/order/internal/service/service_test.go:1275-1350,1763-1890`; `services/payment/src/service.rs:429-457` | Không có REST riêng (job nền trong `order`, chu kỳ `ORDER_EXPIRY_WATCHDOG_SEC=60s`) | `/thanh-toan/cho-thanh-toan/[paymentId]` (`PaymentWaitingClient.tsx`), `RetryPaymentBox.tsx` | Unit test `TestExpiryWatchdog_UC_PAY06` & `TestScanStaleCheckoutGroups_CleanupGuardsAndRetries` PASS; kiểm tra code xác nhận có hỏi `Payment.GetPayment` trước khi huỷ và có quét `IN_PROGRESS` quá 5 phút (commit `dbb29f9`). Stack: xác nhận sau `/payments/{id}/retry`, `orders.payment_id` giữ ID cũ đã `superseded`. | **P1 `ORD-B5`** (watchdog tra `GetPayment(PaymentId: old_superseded_id)` nên nhận `SUPERSEDED` và huỷ oan đơn dù giao dịch retry còn hạn hoặc đã trả), **P2 `ORD-B11`** (`RetryPaymentBox` cho retry `EXPIRED`). Chưa chờ 15 phút thời gian thực trên stack. |
| UC-PAY08 | Thấp | **THIẾU** | Đã tìm `rg "Receipt\|receipt\|BienNhan" proto/ services/order/ services/gateway/ web/src/` → không có | Không có route | Không có nút tải biên nhận PDF trên `/don-hang/[id]` hay `/seller/don-hang/[id]` | 0 file/RPC/UI liên quan. | Mức **Thấp** (không bắt buộc cài đặt theo `docs/spec.md:39`). |
| UC-A16 | Thấp | **THIẾU** | Đã tìm `rg "Fraud\|fraud\|RiskScore\|gian_lan" proto/ services/order/ services/promotion/ services/user/ web/src/` → không có | Không có route | Không có hàng đợi gian lận ở `/admin` | 0 job/rule/bảng/UI liên quan. | Mức **Thấp** (không bắt buộc cài đặt theo `docs/spec.md:39`). |

---

## 2. Bảng đối chiếu Shopify / sàn

| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |
| --- | --- | --- | --- |
| Giỏ hàng | `UC-B05` | **ĐỦ** | Hoạt động đủ trên Redis/API/Web; chỉ chưa lưu cờ `selected` xuống Redis. |
| Checkout | `UC-B06` | **LỖI** | Saga & tách đơn hoạt động, nhưng dính `ORD-B1` (sai tiền seller), `ORD-B4` (idempotency), `ORD-B10`. |
| Thanh toán nhanh (Express / Guest checkout) | Ngoài phạm vi đặc tả (`docs/spec.md:39` "Mọi tính năng không có trong tab Use case"; `docs/spec.md:79` `UC-B06` yêu cầu đã đăng nhập; `docs/spec.md:27` ngoài phạm vi "lưu thông tin thẻ") | — | Không có checkout vãng lai hay lưu thẻ 1-chạm. |
| Áp mã giảm giá (voucher sàn, shop, freeship, xu) | `UC-B15` (giao điểm tại `order`) | **LỖI** | Áp & trừ tổng tiền cho buyer đúng, nhưng phân bổ sang seller sai (`ORD-B1`) và huỷ 1 đơn con mất xu (`ORD-B3`). |
| Tính thuế (VAT / hoá đơn điện tử) | Ngoài phạm vi đặc tả (`docs/spec.md:27` ngoài phạm vi "hoá đơn điện tử"; `docs/spec.md:117` `UC-PAY08` ghi rõ thuế/hoá đơn điện tử ngoài phạm vi; `docs/spec.md:39`) | — | Không có tính thuế theo sản phẩm/khu vực. |
| Tính phí vận chuyển | `UC-SHIP02`, `UC-B06` | **ĐỦ** | `order` gọi `Shipping.CalculateFee` cho từng shop và lưu hãng/dịch vụ/phí (chi tiết hãng do worker `shp` rà). |
| Khôi phục giỏ bỏ dở (abandoned cart) | Ngoài phạm vi đặc tả (`docs/spec.md:39`, không có UC trong `docs/spec.md:43-143`) | — | Giỏ Redis lưu 30 ngày nhưng không có job nhắc/email giỏ bỏ dở. |
| Draft order (đơn nháp tạo hộ) | Ngoài phạm vi đặc tả (`docs/spec.md:39`, không có UC trong `docs/spec.md:43-143`) | — | Không có. |
| Upsell sau mua (post-purchase upsell) | Ngoài phạm vi đặc tả (`docs/spec.md:39`; `docs/spec.md:29` ngoài phạm vi "Combo, mua kèm") | — | Web có dải gợi ý tĩnh `ProductAddonStrip` ở `/don-hang` nhưng lấy mock (`ORD-D4`). |
| Vòng đời đơn (máy trạng thái, hoàn tất, tự huỷ) | `UC-B08`, `UC-B17`, `UC-S04`, `UC-PAY06` | **LỖI** | Máy trạng thái chặn tốt chuyển sai cơ bản, nhưng hở `RETURN_REQUESTED→COMPLETED` (`ORD-B7`), watchdog lỗi sau retry (`ORD-B5`), thiếu job 7 ngày & 48h (`ORD-B9`). |
| Sửa đơn (địa chỉ / hàng hoá sau đặt) | Sửa địa chỉ: `UC-B26`; sửa sản phẩm/số lượng sau đặt: ngoài phạm vi đặc tả (`docs/spec.md:39`) | **THIẾU** (`UC-B26`) | Chưa có API/UI thật sửa địa chỉ; `/tra-cuu-don-hang` chỉ là form mock (`ORD-D1`). |
| Huỷ đơn | `UC-B09`, `UC-S04`, `UC-PAY06` | **LỖI** | Hoàn tồn kho đã đúng (PR #2), nhưng huỷ COD treo sổ cái (`ORD-B2`), huỷ 1 đơn con mất xu (`ORD-B3`), huỷ VietQR `PENDING` giữ `payment_status=PENDING` (`ORD-B8`). |
| Trạng thái thanh toán và giao hàng | `UC-B08`, `UC-PAY02`, `UC-SHIP04` | **LỖI** | Webhook giao hàng cập nhật đơn đúng, nhưng `payment_status` COD & VietQR huỷ bị lệch (`ORD-B8`), thiếu tab `RETURNED` trên web (`ORD-B12`). |
| Định tuyến đơn theo kho (multi-warehouse routing) | Ngoài phạm vi đặc tả (`docs/spec.md:28` ngoài phạm vi "Tối ưu tuyến giao, quản lý kho"; `docs/spec.md:39`) | — | Mỗi shop cấu hình 1 kho lấy hàng (`UC-SHIP06`). |
| Trả hàng và đổi hàng | Trả hàng hoàn tiền: `UC-B09`, `UC-S10`, `UC-SHIP08`; Đổi hàng: chỉ là 1 nhánh quyết định ở `UC-A05` (`docs/spec.md:48`), không có UC tạo đơn đổi tự động (`docs/spec.md:39`) | **THIẾU** / **LỖI** | Mới có `POST /orders/{id}/return` phía buyer (dính `ORD-B7`); toàn bộ duyệt/từ chối seller, hoàn tiền, vận đơn chiều ngược, đổi hàng đều thiếu. |
| Store credit (ví hoàn tiền / tín dụng nội bộ) | Ngoài phạm vi đặc tả (`docs/spec.md:39`; chỉ có xu thưởng `UC-B18` và hoàn tiền phương thức gốc `UC-PAY04`) | — | Chưa có và không nằm trong spec (mock `/admin/khieu-nai` lại ghi "Ví NexusPay"). |
| Tách một lần checkout thành nhiều đơn con theo shop | `UC-B06` | **LỖI** | Tách 2 đơn con trên CO1/CO2 chạy đúng, nhưng phân bổ tiền cho seller sai (`ORD-B1`) và huỷ lẻ 1 đơn con mất xu (`ORD-B3`). |
| Phí vận chuyển theo từng shop | `UC-B06`, `UC-SHIP02` | **ĐỦ** | Kiểm chứng CO1: Shop 1 `27.500₫`, Shop 2 `35.500₫`, tính và cộng tách bạch theo từng đơn con. |
| Khiếu nại giữa người mua & người bán, sàn làm trọng tài, hoàn tiền | `UC-A05`, `UC-S10`, `UC-PAY04` | **THIẾU** | 0 bảng/RPC/REST khiếu nại; `/admin/khieu-nai` là dữ liệu giả (`ORD-D2`). |

---

## 3. Lỗi

### `ORD-B1` [P0] – Phân bổ tiền cho người bán trừ cả voucher sàn & xu vào doanh thu shop (và đơn 0₫ bỏ qua `CreatePayment`)
- **UC liên quan**: `UC-B06`, `UC-B17`, `UC-B15`, `UC-S04`
- **Tái hiện**:
  ```bash
  # 1. Lấy số dư trước của Seller 1 (Xưởng Dệt Bạch Đằng)
  curl -s http://localhost:48080/api/v1/seller/balance -H "Authorization: Bearer $SELLER1_TOKEN"

  # 2. Buyer checkout 2 shop có áp voucher shop (20k) + voucher sàn (30k) + 5.000 xu
  curl -s -X POST http://localhost:48080/api/v1/checkout \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -H "Idempotency-Key: audit-ord-b1-repro" \
    -d '{"address_id":"<ADDR_ID>","payment_method":"COD","shop_voucher_ids":["<SHOP_VOUCHER_ID>"],"platform_voucher_id":"<PLAT_VOUCHER_ID>","coins_to_use":5000}'

  # 3. Seller giao hàng -> webhook GHN delivered -> Buyer bấm confirm-received
  curl -s -X POST http://localhost:48080/api/v1/orders/<ORDER_ID>/confirm-received \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{}'

  # 4. Kiểm tra lại số dư Seller 1
  curl -s http://localhost:48080/api/v1/seller/balance -H "Authorization: Bearer $SELLER1_TOKEN"
  ```
- **Mong đợi**: Theo `UC-B15` (`docs/spec.md:88`), voucher sàn và xu do **sàn** chịu chi phí; tiền hàng thực nhận của seller phải là `subtotal - shop_voucher - commission = 179.000 - 20.000 - 8.950 = 150.050₫` (khớp công thức `computeOrderNetVnd` ở Seller Center). Nếu buyer dùng xu/voucher sàn giảm tổng thanh toán về `0₫`, seller vẫn phải được ghi nhận doanh thu từ nguồn trợ giá sàn.
- **Thực tế**: Trên đơn thật `NM-20261122-CBEA865F` (`95c18110-21ea-4dc6-8e52-54406b713b3d`, `subtotal=179.000`, `shop_voucher=20.000`, `platform_voucher=10.000`, `coins=1.730`, `shipping=27.500`, `commission=8.950`, `total_payable=174.770`), `payment_db.payment_shop_allocations.subtotal_vnd` bị ghi là `147.270` (`174.770 - 27.500`), nên khi hoàn tất, `seller_balances.available_vnd` của Shop 1 chỉ tăng **`138.320₫`** (`405.100 → 543.420₫`), **thiếu `11.730₫`** (đúng bằng `platform_voucher + coins`). Ngoài ra, đọc code xác nhận nếu `grandTotalPayableVND == 0`, `checkout.go:341` bỏ qua hoàn toàn `CreatePayment`, seller sẽ nhận `0₫`.
- **Gốc nghi ngờ**: `services/order/internal/service/checkout.go:341,1144-1154` (`allocSub := ord.TotalPayableVND - allocShip` lấy `TotalPayableVND` đã trừ trợ giá sàn & xu thay vì `SubtotalVND - FlashSaleDiscount - ShopVoucherDiscount`).

### `ORD-B2` [P0] – Huỷ hoặc hoàn hàng đơn COD không hoàn bút toán sổ cái, treo vĩnh viễn `cod_receivable` và `escrow_liability`
- **UC liên quan**: `UC-B09`, `UC-PAY02`, `UC-SHIP05`
- **Tái hiện**:
  ```bash
  # 1. Đọc tổng sổ cái trước
  curl -s http://localhost:48080/api/v1/admin/ledger/summary -H "Authorization: Bearer $ADMIN_TOKEN"

  # 2. Huỷ một đơn con COD đang CONFIRMED (ví dụ 5785be02-5467-4b17-9295-6a08e932a30b, 330.230₫)
  curl -s -X POST http://localhost:48080/api/v1/orders/<COD_ORDER_ID>/cancel \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"reason":"AUDIT-ord cancel COD"}'

  # 3. Đọc lại sổ cái và payment_shop_allocations (SELECT trên payment_db)
  curl -s http://localhost:48080/api/v1/admin/ledger/summary -H "Authorization: Bearer $ADMIN_TOKEN"
  ```
- **Mong đợi**: Khi đơn COD bị huỷ trước giao hoặc bị hoàn hàng (`RETURNED`) do khách từ chối nhận, bản ghi `payment_shop_allocations` của đơn đó phải chuyển trạng thái huỷ/hoàn và bút toán sổ cái `cod_receivable` / `escrow_liability` phải được hoàn ngược.
- **Thực tế**: Khi tạo đơn COD, `payment` ghi ngay `payments.status='succeeded'` và hạch toán `cod_receivable → escrow_liability`. Sau khi huỷ đơn con COD `5785be02-5467-4b17-9295-6a08e932a30b` (`330.230₫`) và sau khi hoàn đơn COD `8aff49aa-8f97-4b18-a913-f7ec7fbd213e` (`206.500₫`, `status=RETURNED`):
  - `GET /api/v1/admin/ledger/summary` giữ nguyên `total_debits = 5.652.240₫` không đổi sau khi huỷ `5785be02-...`.
  - `payment_db.payment_shop_allocations` của cả `5785be02-...` và `8aff49aa-...` vẫn ở `escrow_status = 'held'`, `payments.status = 'succeeded'`.
- **Gốc nghi ngờ**: `services/order/internal/service/lifecycle.go:300-325` (`wasPaid` là `false` với COD; nhánh `else` chỉ gọi `CancelPayment` khi `!hasActiveSibling` và bỏ qua lỗi `Conflict("cannot cancel payment in status 'succeeded'")`); `services/order/internal/service/handlers.go:232` (khi `RETURNED` chỉ hoàn tiền nếu `VIETQR`); `services/payment/src/service.rs:189-196,1889` (`process_order_cancelled_event` chỉ xử lý `"succeeded" if payment.method == "VIETQR"`).

### `ORD-B3` [P0] – Huỷ 1 đơn con trong checkout đa gian hàng làm mất vĩnh viễn phần Xu của đơn con đó
- **UC liên quan**: `UC-B09`, `UC-B06`, `UC-B15`
- **Tái hiện**:
  ```bash
  # 1. Checkout 2 shop cùng 5.000 xu (CO1: Shop 1 dùng 1.730 xu, Shop 2 dùng 3.270 xu)
  # 2. Hoàn tất đơn Shop 1, huỷ đơn Shop 2 đang CONFIRMED:
  curl -s -X POST http://localhost:48080/api/v1/orders/<SHOP2_ORDER_ID>/cancel \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"reason":"AUDIT-ord cancel sub-order 2"}'

  # 3. Kiểm tra lịch sử & số dư xu
  curl -s http://localhost:48080/api/v1/coins/balance -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi**: Theo `UC-B15` (`docs/spec.md:88`), khi một đơn con bị huỷ, phần xu đã phân bổ cho đơn con đó (`3.270` xu) phải được hoàn lại cho người mua.
- **Thực tế**: Trên nhóm checkout CO1 (`923b13c2-f3d9-46bf-904b-876be7acae77`), sau khi Shop 1 (`95c18110-...`, `coins_discount_vnd=1730`) hoàn tất và Shop 2 (`5785be02-...`, `coins_discount_vnd=3270`) bị huỷ, kiểm tra `promotion_db.coin_transactions` của `audit-ord-1`: có giao dịch trừ `-5000` khi reserve CO1, nhưng **0 xu được hoàn** khi huỷ `5785be02-...` → người mua mất vĩnh viễn **`3.270` xu**.
- **Gốc nghi ngờ**: `services/order/internal/service/lifecycle.go:283-298` — chỉ gọi `s.compensateReleasePromotion(ctx, groupID)` khi `!hasActiveSibling` (vì `promotion` hiện chỉ reserve/release nguyên khối theo `checkout_group_id`, không hỗ trợ hoàn xu theo `order_id`).

### `ORD-B4` [P1] – `Idempotency-Key` không kiểm tra payload khác, trả nhầm kết quả đơn cũ
- **UC liên quan**: `UC-B06`
- **Tái hiện**:
  ```bash
  # 1. Checkout 1 lần bằng VIETQR với Idempotency-Key cố định
  curl -s -X POST http://localhost:48080/api/v1/checkout \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -H "Idempotency-Key: audit-ord-idem-b4" \
    -d '{"address_id":"<ADDR_ID>","payment_method":"VIETQR","items":[{"sku_id":"<SKU1>","quantity":1}]}'

  # 2. Gửi lại CÙNG Idempotency-Key nhưng đổi hoàn toàn payload sang COD và quantity=2
  curl -i -s -X POST http://localhost:48080/api/v1/checkout \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -H "Idempotency-Key: audit-ord-idem-b4" \
    -d '{"address_id":"<ADDR_ID>","payment_method":"COD","items":[{"sku_id":"<SKU1>","quantity":2}]}'
  ```
- **Mong đợi**: Trả về `409 Conflict` hoặc `422 Unprocessable Entity` báo trùng `Idempotency-Key` với payload khác.
- **Thực tế**: Trả về **`HTTP 200`** với `idempotent_replay: true`, `checkout_group_id: "afb2a9ed-b3f0-4158-8bf3-b6a032e41590"`, `payment_method: "VIETQR"`, `total_payable_vnd: 398000` (dữ liệu đơn CO2 cũ, im lặng bỏ qua payload COD mới).
- **Gốc nghi ngờ**: `services/order/internal/service/checkout.go:92-94,451-512` và `services/order/migrations/0001_init.sql:3-18` không tính/lưu `request_hash` trên bảng `checkout_groups` để đối chiếu khi replay.

### `ORD-B5` [P1] – `orders.payment_id` và `orders.payment_expires_at` không cập nhật sau `POST /api/v1/payments/{id}/retry`, khiến watchdog `UC-PAY06` huỷ oan đơn
- **UC liên quan**: `UC-PAY06`, `UC-B08`, `UC-B25`
- **Tái hiện**:
  ```bash
  # 1. Tạo đơn VietQR (nhận payment_id cũ = P1, order_id = O1)
  # 2. Gọi retry thanh toán (tạo payment mới P2, đánh dấu P1 = superseded)
  curl -s -X POST http://localhost:48080/api/v1/payments/<P1>/retry \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{}'

  # 3. Đọc lại chi tiết đơn O1 ngay sau retry (trước khi thanh toán P2)
  curl -s http://localhost:48080/api/v1/orders/<O1> -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi**: Sau retry, `orders.payment_id` phải trỏ sang `P2` và `orders.payment_expires_at` cập nhật theo hạn của `P2` để: (a) người mua mở lại `/don-hang/{id}` thấy đúng mã QR còn hiệu lực; (b) watchdog `UC-PAY06` kiểm tra đúng trạng thái của `P2` tại hạn mới.
- **Thực tế**: Trên đơn thật `747070cd-e221-4c36-b335-1e3ae047a907` (CO2), sau khi retry tạo giao dịch mới `f62ac62b-4823-49a6-be24-4c3627463748` (làm `7309c836-...` thành `superseded`), `GET /api/v1/orders/747070cd-...` và `order_db.orders` **vẫn giữ `payment_id = 7309c836-2d6e-49dd-a880-2b3750034033`** và `payment_expires_at` cũ (chỉ được ghi đè sau khi webhook `payment.succeeded` về). Khi đến hạn cũ, `processExpiredGroup` gọi `Payment.GetPayment(PaymentId: old_superseded_id)` → `payment` ưu tiên tra nhánh `if let Some(pid)` và trả về `SUPERSEDED` (bỏ qua giao dịch retry `P2` đang `pending`/`succeeded`), khiến watchdog rơi thẳng vào nhánh `EXPIRED` và **huỷ oan đơn hàng**.
- **Gốc nghi ngờ**: `services/gateway/internal/router/handlers_payment.go:273-290` không báo sang `order`; `services/order/internal/service/handlers.go:278-316` truyền `cand.PaymentID` cũ vào `GetPayment`; `services/payment/src/service.rs:435-439` tra theo `payment_id` trước khi tra theo `order_group_id`.

### `ORD-B6` [P1] – Rule chặn bom hàng COD (`ORDER_COD_MAX_REJECTED_COUNT`) vô hiệu hoàn toàn vì không có code nào tăng `cod_rejected_count`
- **UC liên quan**: `UC-PAY02`, `UC-SHIP05`
- **Tái hiện**:
  ```bash
  # 1. Đặt đơn COD (CO5: 8aff49aa-8f97-4b18-a913-f7ec7fbd213e), seller ship lấy mã vận đơn GHN-9C85F2309E
  # 2. Gửi webhook vận chuyển trạng thái "returned" (khách từ chối nhận COD)
  TS=$(date +%s)
  BODY='{"event_id":"evt-cod-ret-1","tracking_code":"GHN-9C85F2309E","raw_status":"returned","note":"Khach tu choi nhan hang COD","occurred_at":"2026-11-22T18:24:00Z"}'
  SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SHIPPING_WEBHOOK_SECRET" -hex | awk '{print $NF}')
  curl -s -X POST http://localhost:48080/api/v1/webhooks/shipping/ghn \
    -H "Content-Type: application/json" -H "X-Timestamp: $TS" -H "X-Signature: sha256=$SIG" -d "$BODY"

  # 3. Kiểm tra hồ sơ buyer sau khi đơn đã thành RETURNED
  curl -s http://localhost:48080/api/v1/users/me -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi**: Theo `UC-PAY02` & `UC-SHIP05` (`docs/spec.md:111,140`), khi đơn COD bị từ chối nhận và hoàn hàng (`RETURNED`), hệ thống phải tăng `users.cod_rejected_count` để chặn COD khi đạt ngưỡng `ORDER_COD_MAX_REJECTED_COUNT=3`.
- **Thực tế**: Đơn CO5 `8aff49aa-...` đã chuyển `RETURNED` và hoàn tồn kho `30→31`, nhưng `user_db.users.cod_rejected_count` của `audit-ord-1` vẫn bằng **`0`**. Tìm toàn repo bằng `rg "cod_rejected_count"` cho thấy 0 chỗ thực hiện `UPDATE users SET cod_rejected_count = cod_rejected_count + 1` (chỉ có cột schema & lệnh đọc ở `ValidateUser`).
- **Gốc nghi ngờ**: `services/order/internal/service/handlers.go:223-249` (khi xử lý `shipment.updated` → `RETURNED` không gọi/phát lệnh tăng `cod_rejected_count` sang `user`); `services/user/` không có RPC hay consumer cập nhật bộ đếm này.

### `ORD-B7` [P1] – `BuyerConfirmReceived` cho phép hoàn tất đơn đang `RETURN_REQUESTED` bỏ mồ côi `return_requests`, và form trả hàng web không có tải ảnh bằng chứng
- **UC liên quan**: `UC-B17`, `UC-B09`
- **Tái hiện**:
  ```bash
  # 1. Trên đơn đã DELIVERED (ví dụ 747070cd-e221-4c36-b335-1e3ae047a907), buyer gửi yêu cầu trả hàng
  curl -s -X POST http://localhost:48080/api/v1/orders/<DELIVERED_ORDER_ID>/return \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"reason":"San pham loi duong may","evidence_urls":[]}'

  # 2. Đơn đang ở RETURN_REQUESTED — buyer gọi confirm-received
  curl -s -X POST http://localhost:48080/api/v1/orders/<DELIVERED_ORDER_ID>/confirm-received \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{}'
  ```
- **Mong đợi**: Đơn đang `RETURN_REQUESTED` phải chờ quy trình trả hàng/rút yêu cầu rõ ràng (đóng bản ghi `return_requests`), không được giải ngân escrow và cộng xu trong khi `return_requests` vẫn `PENDING`; UI trả hàng ở `/don-hang/[id]` phải cho đính kèm ảnh bằng chứng theo `UC-B09` bước 2.
- **Thực tế**: Bước 2 trả về **`HTTP 200`**, đơn `747070cd-...` chuyển thẳng thành `COMPLETED`, giải ngân cho seller và cộng `+1.790` xu cho buyer, trong khi `order_db.return_requests` (`f5c63a19-...`) vẫn nằm mồ côi ở `status = 'PENDING'`. Ngoài ra, `OrderDetailClient.tsx:263` gửi cứng `evidence_urls: []` và không có input tải ảnh/URL.
- **Gốc nghi ngờ**: `services/order/internal/statemachine/statemachine.go:36-39` cho phép `RETURN_REQUESTED → COMPLETED`; `services/order/internal/service/lifecycle.go:606-609` chỉ kiểm `ValidateTransition` mà không giới hạn `curStatus == DELIVERED` (hoặc không đóng `return_requests`); `web/src/features/account/OrderDetailClient.tsx:258-265`.

### `ORD-B8` [P2] – `orders.payment_status` lệch thực tế trên đơn COD mới đặt và đơn VietQR bị buyer huỷ
- **UC liên quan**: `UC-PAY02`, `UC-B09`, `UC-B08`
- **Tái hiện**:
  ```bash
  # A. Đặt đơn COD (chưa giao hàng) rồi xem chi tiết đơn và thông báo:
  curl -s http://localhost:48080/api/v1/orders/8aff49aa-8f97-4b18-a913-f7ec7fbd213e -H "Authorization: Bearer $TOKEN"
  curl -s "http://localhost:48080/api/v1/notifications?page=1&page_size=10" -H "Authorization: Bearer $TOKEN"

  # B. Tạo đơn VietQR (PENDING), huỷ ngay, rồi đọc lại đơn:
  curl -s http://localhost:48080/api/v1/orders/6d12467e-5ec9-46ff-b5dd-81568c080807 -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi**: (A) Đơn COD khi mới đặt phải có `payment_status = "COD_PENDING"`, chỉ chuyển đã thu sau khi shipper giao/đối soát, không gửi thông báo "Thanh toán thành công" lúc mới đặt; (B) Đơn VietQR `PENDING` đã bị huỷ phải có `payment_status = "CANCELLED"`.
- **Thực tế**: (A) Đơn COD `8aff49aa-...` (kể cả khi đã `RETURNED`) và `5785be02-...` (đã `CANCELLED`) đều trả `payment_status: "SUCCEEDED"`, đồng thời buyer nhận thông báo `"Thanh toán 206500 VND qua COD thành công"` ngay lúc đặt hàng; (B) Hai đơn VietQR đã huỷ `6d12467e-...` và `6fe2748d-...` có `status: "CANCELLED"` nhưng `payment_status: "PENDING"` mãi mãi (dù `payment_db.payments.status = 'cancelled'`).
- **Gốc nghi ngờ**: `services/order/internal/service/checkout.go:377,424` ghi đè `COD_PENDING` bằng `SUCCEEDED` từ `CreatePayment`; `services/order/internal/service/lifecycle.go:199-206` không truyền `PaymentStatus: "CANCELLED"` khi huỷ đơn `PENDING` và `services/order/internal/store/postgres.go:410` bỏ qua `payment.failed` nếu đơn không còn `PENDING`.

### `ORD-B9` [P2] – `BuyerConfirmReceived` giải ngân thẳng sang `available_vnd` (bỏ qua `pending_vnd`), thiếu job tự `COMPLETED` 7 ngày và tự huỷ 48h
- **UC liên quan**: `UC-B17`, `UC-S04`, `UC-PAY05`
- **Tái hiện**:
  ```bash
  # Sau khi buyer gọi POST /api/v1/orders/95c18110-21ea-4dc6-8e52-54406b713b3d/confirm-received:
  curl -s http://localhost:48080/api/v1/seller/balance -H "Authorization: Bearer $SELLER1_TOKEN"
  ```
- **Mong đợi**: Theo `UC-B17` (`docs/spec.md:90`), khi buyer xác nhận nhận hàng, tiền phải vào **số dư chờ (`pending_vnd`)** rồi mới sang `available_vnd` sau thời gian giữ tiền (`UC-PAY05`); có job tự `COMPLETED` sau 7 ngày giao; và theo `UC-S04` (`docs/spec.md:121`), đơn `CONFIRMED` quá 48h seller chưa xác nhận phải tự huỷ và ghi nhận vi phạm.
- **Thực tế**: Ngay sau `confirm-received`, `pending_vnd` giữ nguyên **`0`**, còn `available_vnd` nhảy thẳng `+138.320₫` (`405.100 → 543.420₫`). Kiểm tra `services/order/cmd/server/main.go:105-122` xác nhận `order` chỉ chạy đúng 2 vòng lặp nền (`outboxRelay` và `RunExpiryWatchdog`), không có job 7 ngày và 48h.
- **Gốc nghi ngờ**: `services/order/internal/service/lifecycle.go:645-658` gọi trực tiếp `Payment.ReleaseEscrow` (chuyển thẳng sang `available_vnd`); `services/order/cmd/server/main.go:112-121`.

### `ORD-B10` [P2] – Link thông báo `payment.succeeded` trỏ tới `checkout_group_id` gây lỗi HTTP 404
- **UC liên quan**: `UC-B08`, `UC-B06`
- **Tái hiện**:
  ```bash
  # 1. Đọc thông báo sau khi thanh toán / đặt COD thành công (link trả về "/orders/23867737-138e-4682-95f7-7e526b933582")
  curl -s "http://localhost:48080/api/v1/notifications?page=1&page_size=10" -H "Authorization: Bearer $TOKEN"

  # 2. Mở link tương ứng qua API & Web (orderMeta.ts đổi /orders/<id> -> /don-hang/<id>)
  curl -i -s http://localhost:48080/api/v1/orders/23867737-138e-4682-95f7-7e526b933582 -H "Authorization: Bearer $TOKEN"
  ```
- **Mong đợi**: Bấm vào thông báo "Thanh toán thành công" phải mở đúng trang chi tiết đơn hàng (`8aff49aa-8f97-4b18-a913-f7ec7fbd213e`) hoặc trang danh sách đơn (nếu checkout nhiều shop).
- **Thực tế**: Thông báo chứa `link: "/orders/23867737-138e-4682-95f7-7e526b933582"` (là `checkout_group_id`, không phải `order_id`). Cả `GET /api/v1/orders/23867737-...` lẫn `GET http://localhost:43000/don-hang/23867737-...` (đã đăng nhập BFF) đều trả **`HTTP 404`**.
- **Gốc nghi ngờ**: `services/order/internal/service/checkout.go:1159` gán `OrderId: checkoutGroupID` khi gọi `CreatePayment` (dù chỉ có 1 đơn con); `services/notification/internal/service/notification.go:605` tạo link `orderLink(p.OrderID)`.

### `ORD-B11` [P2] – `RetryPaymentBox` hiện nút "Thanh toán lại" cho giao dịch `EXPIRED` nhưng Gateway luôn chặn `409 Conflict`
- **UC liên quan**: `UC-PAY06`, `UC-B25`
- **Tái hiện**:
  ```bash
  # Khi có payment_id ở trạng thái EXPIRED:
  curl -i -s -X POST http://localhost:48080/api/v1/payments/<EXPIRED_PAYMENT_ID>/retry \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{}'
  ```
- **Mong đợi**: Theo `UC-B25` (`docs/spec.md:98`), đơn quá hạn phải ẩn nút thanh toán lại và gợi ý mua lại; UI và Gateway phải thống nhất trạng thái được phép retry.
- **Thực tế**: `RetryPaymentBox.tsx:300-303` bật `canRetry` cho cả `PAYMENT_STATUS_EXPIRED` nên nút "Thanh toán lại" vẫn hiện, nhưng `handlers_payment.go:252-256` từ chối mọi giao dịch `EXPIRED` bằng `409 Conflict`.
- **Gốc nghi ngờ**: `web/src/features/commerce/RetryPaymentBox.tsx:300-303` vs `services/gateway/internal/router/handlers_payment.go:252-256`.

### `ORD-B12` [P3] – Lỗi hiển thị web đơn hàng: tiếng Anh thô trong timeline, thiếu tab `RETURNED`, ảnh bìa seller hardcode
- **UC liên quan**: `UC-B08`, `UC-S04`
- **Tái hiện**:
  ```bash
  # 1. Lấy SSR HTML trang chi tiết đơn (có cookie đăng nhập Web BFF):
  curl -s -b cookies.txt http://localhost:43000/don-hang/95c18110-21ea-4dc6-8e52-54406b713b3d | grep -o "Shipped via ghn[^<]*"
  curl -s -b cookies.txt http://localhost:43000/don-hang/747070cd-e221-4c36-b335-1e3ae047a907 | grep -o "Order created awaiting payment"
  # 2. Kiểm tra tab lọc trên /don-hang:
  curl -s -b cookies.txt http://localhost:43000/don-hang | grep -c 'status=RETURNED'
  ```
- **Mong đợi**: Toàn bộ ghi chú timeline hiển thị tiếng Việt, có tab/lọc xem được đơn `RETURNED`, và trang `/seller/don-hang/[id]` hiển thị đúng ảnh sản phẩm của đơn.
- **Thực tế**: SSR HTML render nguyên chuỗi tiếng Anh `"Shipped via ghn (tracking: GHN-1E8BD14AE3)"` và `"Order created awaiting payment"`; `/don-hang` có `0` liên kết `status=RETURNED` (tab "Trả hàng" chỉ lọc `RETURN_REQUESTED`, làm đơn CO5 `RETURNED` bị khuất khỏi tab trả hàng); `/seller/don-hang/[id]` hardcode ảnh `/products/ao-thun-cotton-trang.png` cho mọi đơn.
- **Gốc nghi ngờ**: `web/src/features/account/orderMeta.ts:85-95,225-248`; `web/src/app/(seller)/seller/don-hang/[id]/page.tsx:273`.

---

## 4. Dữ liệu

- **`ORD-D1` (Mock vãng lai mâu thuẫn chính sách)**: Trang `/tra-cuu-don-hang` (`web/src/features/commerce/OrderLookupView.tsx`, `web/src/mock/guestOrderLookup.ts:14-35`) dùng 100% dữ liệu mẫu cố định (`NM-261120-109`, SĐT `0912 486 305`), không nối gateway nên không tra được đơn thật như `NM-20261122-CBEA865F`. Đồng thời nội dung ghi "đổi trả 45 ngày", mâu thuẫn với cửa sổ 7 ngày của backend (`services/order/internal/service/lifecycle.go:26`) và `/don-hang/[id]`.
- **`ORD-D2` (Mock khiếu nại admin)**: Trang `/admin/khieu-nai` (`web/src/app/(admin)/admin/khieu-nai/page.tsx`) đọc 100% `useMockStore().disputes` (case giả `KN-...`, nhắc "Ví NexusPay" ngoài đặc tả), hoàn toàn không phản ánh bảng `order_db.return_requests` thật.
- **`ORD-D3` (Lệch doanh thu thực nhận giữa Seller Web và `payment_db`)**: Trên đơn `NM-20261122-CBEA865F`, công thức hiển thị ở Seller Center (`web/src/features/seller/types.ts:107-114`, `computeOrderNetVnd`) tính ra **`150.050₫`**, nhưng số tiền thực cộng vào `payment_db.seller_balances` chỉ là **`138.320₫`** (lệch **`11.730₫`** do `ORD-B1`).
- **`ORD-D4` (Mock upsell & giới hạn tra ảnh đơn hàng)**: `/don-hang` và `/don-hang/[id]` (`OrdersListClient.tsx:77`, `OrderDetailClient.tsx:130`) render dải `ProductAddonStrip` từ mảng tĩnh `PRODUCTS.slice(0, 4)` thay vì API thật. Ngoài ra, `fetchOrderVisualLookup` (`web/src/features/account/serverData.ts:35-65`) chỉ gọi `page_size=100` sản phẩm công khai nên sản phẩm bị ẩn hoặc ngoài top 100 bị mất ảnh (rơi về icon).
- **`ORD-D5` (Cắt ngầm danh sách đơn seller ở 100 đơn)**: `web/src/app/(seller)/seller/don-hang/page.tsx:82` cố định `page=1&page_size=100` không có nút chuyển trang, nên shop có > 100 đơn sẽ không truy cập được các đơn cũ hơn trên UI.
- **Phần đã đối chiếu khớp sạch**: Trên `/don-hang` và `/don-hang/[id]` cho các đơn thật (`NM-20261122-CBEA865F`, `NM-20261122-E885BBAC`), `order_code`, `subtotal_vnd`, `shipping_fee_vnd`, các khoản giảm giá, `total_payable_vnd`, tên SKU (`Trắng tinh / M`), tên shop (`Xưởng Dệt Bạch Đằng`), tên & địa chỉ người nhận (`Audit Ord Buyer 1`, `123 Đường Audit Ord`), mã vận đơn (`GHN-1E8BD14AE3`) và ảnh sản phẩm (`/products/ao-thun-cotton-trang.png`, `HTTP 200`) đều khớp 100% với `order_db`. NATS `NEXUSMART_DLQ` = 0 message, `order_db.dead_letter_events` = 0, `order_db.outbox_events` chưa phát = 0.

---

## 5. Đề xuất sửa

| Mã | Giải quyết | Việc cụ thể | Cỡ | Proto | Migration | ADR |
| --- | --- | --- | --- | --- | --- | --- |
| **ORD-F1** | `ORD-B1`, `ORD-D3`, `UC-B06`, `UC-B17` | Sửa `buildCreatePaymentRequest` (`services/order/internal/service/checkout.go:1141-1155`) gửi `gross_seller_subtotal_vnd = subtotal - flash_sale - shop_voucher` và `platform_subsidy_vnd = platform_voucher + coins` (cùng `freeship_subsidy_vnd`); xử lý cả trường hợp `total_payable_vnd == 0` vẫn ghi nhận phân bổ cho seller. Ở `services/payment/src/service.rs`, hạch toán tài khoản sổ cái trợ giá sàn (`platform_promo_expense`) để đảm bảo seller nhận đủ `gross_seller_subtotal - commission` và sổ cái cân bằng. | **L** | Có (`payment.proto`) | Có (`payment_db`) | Không |
| **ORD-F2** | `ORD-B2`, `ORD-B8` (phần COD), `UC-PAY02` | Không đánh dấu đơn COD `SUCCEEDED` ngay lúc checkout: giữ `payment_status = "COD_PENDING"` ở `checkout.go:377,424` và `payments.status = 'cod_pending'` ở `payment` (không phát `payment.succeeded` lúc đặt); chỉ ghi nhận thu hộ sau khi giao/đối soát COD (`UC-PAY03`). Khi đơn COD bị `CANCELLED` hoặc `RETURNED`, huỷ `payment_shop_allocations` tương ứng và hoàn ngược bút toán sổ cái (nếu đã ghi nhận). | **M** | Có (`payment.proto`) | Có (`payment_db`) | Không |
| **ORD-F3** | `ORD-B3`, `UC-B09` | Mở rộng `PromotionService` để lưu phần xu (và voucher shop) phân bổ theo từng `order_id` trong `checkout_group_id`, thêm RPC/tham số `order_id` cho `ReleasePromotion` (đăng ký vào `internalRPCs` nếu tạo RPC mới) và gọi từ `services/order/internal/service/lifecycle.go:295-298` khi huỷ 1 đơn con dù còn sibling hoạt động. | **M** | Có (`promotion.proto`) | Có (`promotion_db`) | Không |
| **ORD-F4** | `ORD-B4`, `UC-B06` | Thêm cột `request_hash TEXT NOT NULL DEFAULT ''` vào `order_db.checkout_groups` (`0002_checkout_request_hash.sql`); tính SHA-256 trên payload chuẩn hoá (address, method, vouchers, coins, items đã sort) ở `services/order/internal/service/checkout.go:92-106,451-512`; nếu cùng `(buyer_id, idempotency_key)` nhưng khác `request_hash`, trả `InvalidArgument`/`FailedPrecondition` (gateway map sang `409`/`422`). | **S** | Không | Có (`order_db`) | Không |
| **ORD-F5** | `ORD-B5`, `ORD-B11`, `UC-PAY06` | Thêm RPC nội bộ `OrderService/SyncOrderPayment` (đăng ký `internalRPCs`) hoặc phát event `payment.created` để cập nhật `orders.payment_id` & `orders.payment_expires_at` ngay khi `/payments/{id}/retry` tạo payment mới (`handlers_payment.go:273-290`). Đồng thời sửa `processExpiredGroup` (`services/order/internal/service/handlers.go:278-281`) gọi `GetPayment` chỉ với `OrderGroupId` (bỏ `PaymentId` cũ) để `payment` luôn trả giao dịch mới nhất (`succeeded` > `pending` > khác). Ở web, bỏ `PAYMENT_STATUS_EXPIRED` khỏi `canRetry` trong `RetryPaymentBox.tsx:300-303`. | **S** | Có (`order.proto` nếu dùng RPC) | Không | Không |
| **ORD-F6** | `ORD-B6`, `UC-PAY02` | Khi `order` chuyển đơn COD sang `RETURNED` (`services/order/internal/service/handlers.go:223-249`), gọi RPC nội bộ `UserService/IncrementCodRejectedCount` (hoặc `user` lắng nghe `order.returned` có `payment_method=COD` idempotent theo `order_id`). Bổ sung kiểm tra `valResp.GetPhoneVerified()` và kiểm tra khu vực hỗ trợ COD từ `Shipping.CalculateFee` tại `checkout.go:203-220`. | **S** | Có (`user.proto`, `shipping.proto`) | Không | Không |
| **ORD-F7** | `ORD-B7` (phần state), `ORD-B8` (phần VietQR), `ORD-B10` | (1) Trong `lifecycle.go:606-609`, buộc `BuyerConfirmReceived` chỉ nhận `curStatus == DELIVERED` (chặn `RETURN_REQUESTED`). (2) Trong `lifecycle.go:199`, truyền `PaymentStatus: "CANCELLED"` khi huỷ đơn `PENDING`. (3) Ở `checkout.go:1159` & `notification.go:605`: truyền `order_id` thật khi checkout 1 đơn con, hoặc với checkout nhiều shop trỏ link tới `/orders` kèm ghi chú danh sách mã đơn để không 404. | **S** | Không | Không | Không |
| **ORD-F8** | `ORD-B9`, `UC-B17`, `UC-S04` | (1) Khi `BuyerConfirmReceived`, chuyển tiền vào `pending_vnd` (escrow hold), tách job chuyển `pending_vnd → available_vnd` sau hạn giữ tiền (`UC-PAY05`). (2) Thêm cột `delivered_at` và 2 job nền trong `services/order/cmd/server/main.go`: tự `COMPLETED` sau 7 ngày từ `DELIVERED` (`UC-B17`) và tự huỷ đơn `CONFIRMED` quá 48h + ghi vi phạm (`UC-S04`). (3) Phát event thông báo buyer khi seller chuyển `PREPARING`. | **M** | Có (`payment.proto` nếu tách release pending) | Có (`order_db`) | Không |
| **ORD-F9** | `UC-S10`, `UC-A05`, `ORD-B7` (upload ảnh), `ORD-D2` | Hoàn thiện luồng trả hàng & khiếu nại đầu-cuối: mở rộng `order.proto` & `order_db` (`ListReturnRequests`, `SellerResolveReturn`, job auto-accept 3 ngày, bảng `disputes` & `AdminResolveDispute`), nối `shipping` tạo vận đơn chiều ngược (`UC-SHIP08`), nối `payment` hoàn tiền (`CreateRefund`), thêm UI duyệt trả hàng ở `/seller/don-hang/[id]`, thay mock `/admin/khieu-nai` bằng API thật, thêm upload/đính kèm ảnh bằng chứng ở `OrderDetailClient.tsx:258-265`. | **L** | Có (`order.proto`, `shipping.proto`) | Có (`order_db`, `shipping_db`) | Chỉ cần nếu thêm object storage mới cho ảnh bằng chứng (`AGENTS.md` luật 11) |
| **ORD-F10** | `UC-B26`, `UC-S15`, `ORD-B12`, `ORD-D1`, `ORD-D4`, `ORD-D5` | (1) Thêm `UpdateOrderAddress` (`order.proto` + `shipping.proto` tính lại phí & cập nhật/tạo lại vận đơn) cho `UC-B26`. (2) Thêm `BatchShipOrders` + job nền gộp nhãn PDF + picking list theo SKU cho `UC-S15`. (3) Dọn web: nối `/tra-cuu-don-hang` vào endpoint tra cứu thật (hoặc gắn banner/bỏ mock 45 ngày), thêm bản dịch timeline + tab `RETURNED` trong `orderMeta.ts`, sửa ảnh bìa hardcode & thêm phân trang ở `/seller/don-hang`, thay `ProductAddonStrip` mock bằng API. | **L** | Có (`order.proto`, `shipping.proto`) | Có (`order_db` nếu lưu batch job) | Không (nếu sinh PDF bằng thư viện Go thuần; cần ADR nếu thêm service/headless browser mới) |

---

## 6. Đã kiểm gì, chưa kiểm gì

### Đã kiểm
- **Code & cấu trúc (`HEAD` `0db2cdd`)**: đọc trực tiếp toàn bộ luồng ở `proto/{cart,order}`, `services/cart`, `services/order`, `services/gateway` (cart/order/payment retry), `services/payment` (`CreatePayment`, `GetPayment`, `process_order_cancelled_event`), `services/product` (`RestockOrderItems`), `services/notification` (`payment.succeeded`), và các trang `web/` (`/gio-hang`, `/thanh-toan`, `/thanh-toan/cho-thanh-toan/[paymentId]`, `/don-hang`, `/don-hang/[id]`, `/tra-cuu-don-hang`, `/seller/don-hang`, `/seller/don-hang/[id]`, `/admin/khieu-nai`).
- **Unit test cục bộ**: `go test -count=1 ./...` ở `services/cart` và `services/order` — tất cả PASS (gồm `TestExpiryWatchdog_UC_PAY06` và `TestScanStaleCheckoutGroups_CleanupGuardsAndRetries`).
- **Chạy thật trên stack `nexusmart-audit`**:
  - Giỏ hàng: SKU lạ, SKU `PENDING`, hết hàng, vượt tồn kho (clamp), đổi giá (`price_changed`), xoá mục, giỏ 2 shop, bền vững qua logout/login.
  - Checkout 2 shop: CO1 (COD + shop voucher + platform voucher + 5.000 xu, tách `95c18110-...` & `5785be02-...`), CO2 (VietQR 2 shop, tách `747070cd-...` & `bf90a979-...`), CO3 & CO4 (VietQR + voucher/xu huỷ `PENDING`), CO5 (COD bị hoàn `RETURNED`), kiểm tra `ORDER_COD_MAX_VND` (có và không có promo).
  - Idempotency replay cùng key+payload và cùng key+khác payload; `/payments/{id}/retry` kiểm tra `orders.payment_id`; webhook VietQR & webhook GHN ký HMAC-SHA256 thật.
  - Máy trạng thái & bù trừ: chuyển trạng thái sai (409), seller confirm/ship, buyer confirm-received, hoàn tồn kho PR #2 (`RestockOrderItems`), hoàn tiền VietQR, hoàn voucher & xu, `RequestReturn` và lỗ hổng `confirm-received` trên `RETURN_REQUESTED`.
  - RBAC: 13/13 kiểm thử phân quyền chéo buyer/seller/admin đều PASS (`403`/`200`).
  - Đối chiếu DB/NATS/Redis/Web SSR: SELECT trực tiếp trên `order_db`, `payment_db`, `product_db`, `promotion_db`, `user_db`, kiểm tra NATS stream/DLQ, kiểm tra HTML SSR & link 404 qua Web BFF `:43000`.

### Chưa kiểm
- **Chờ hết hạn 15 phút thời gian thực trên stack (`UC-PAY06`)**: do không được sửa cấu hình/restart container để rút ngắn `ORDER_PAYMENT_EXPIRY_MINUTES=15`, watchdog được kiểm bằng đọc code `handlers.go:254-409`, chạy unit test `TestExpiryWatchdog_UC_PAY06` (PASS) và kiểm chứng thực tế lỗi `ORD-B5` trên `orders.payment_id` sau retry.
- **Checkout tổng thanh toán `0₫` (100% xu/voucher) trên stack**: kết luận ở `ORD-B1` dựa trên đọc code `services/order/internal/service/checkout.go:341` (`if grandTotalPayableVND > 0`).
- **Tương tác trình duyệt thực (Playwright click/DOM client-side)** cho các component chỉ render phía client (`/tra-cuu-don-hang`, `/admin/khieu-nai`, `/seller/don-hang/[id]`): đã kiểm tra bằng HTTP BFF/SSR và đọc trực tiếp mã nguồn TypeScript/React tương ứng.
