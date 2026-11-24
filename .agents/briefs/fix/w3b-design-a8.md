# Thiết kế đợt 3b — Mảng A8 (Người mua, Người bán & Gợi ý giá §10)

Nền khảo sát: cây làm việc hiện tại (sau 3a chưa commit). Mốc kế tiếp thực tế: migration `order 0015_`, `product 000008_`, `shipping 000006_`, `payment 0009_`; E2E `41_`, e2e-web `09_`.

## 1. Hiện trạng

- **UC-B26 (Sửa địa chỉ trước lấy hàng)**: `orders.shipping_address_json` đã lưu snapshot (`services/order/migrations/0001_init.sql:53`, `order.proto:92-100`); `CancelOrder` đã gọi `Shipping.CancelShipment` đồng bộ (`services/order/internal/service/lifecycle.go:281-289`). Thiếu: RPC sửa địa chỉ ở `order`, RPC `UpdateShipmentAddress` + method trên `carrier.Carrier` (`services/shipping/internal/carrier/carrier.go:129-140`), cột tên/SĐT/số nhà trên `shipments` (`services/shipping/migrations/000001_init.sql:36-55` chỉ có `to_province_code`/`to_ward_code`), cơ chế thu thêm phí ship khi `delta > 0` (chú ý `payments` có unique index 1 `succeeded`/order & group tại `services/payment/migrations/0001_init.sql:134` và `0002_payment_money_model.sql:35`), event báo seller, nút/form sửa ở `web/src/features/account/OrderDetailClient.tsx:2043-2059`.
- **UC-S13 (Nhập sản phẩm bằng file)**: `ProductService.CreateProduct` có sẵn validation SKU/slug/category (`services/product/internal/server/server.go:528-674`); gateway và BFF đều chặn body `2 MiB` (`services/gateway/internal/httpx/response.go:142`, `web/src/lib/server/proxy-security.ts:1`). Thiếu hoàn toàn: bảng job/row import, worker nền, tải file mẫu CSV, xuất file lỗi, RPC/REST và UI trên `web/src/app/(seller)/seller/san-pham/page.tsx`.
- **UC-S15 (Xử lý đơn & in nhãn gộp)**: `SellerShipOrder` (`lifecycle.go:606-753`) xử lý từng đơn; `LabelHandler` (`services/shipping/internal/webhook/label_handler.go:40-139`) chỉ in HTML 1 vận đơn qua `NewShippingLabelProxy` (`services/gateway/internal/proxy/proxy.go:145-194`). Web chỉ có nút lặp tuần tự `confirm` (`web/src/app/(seller)/seller/don-hang/page.tsx:240-262`). Thiếu: lọc theo hãng/hạn giao/từ khoá ở `ListOrdersFiltered` (`services/order/queries/orders.sql:243-258`), job nền tạo vận đơn hàng loạt, picking list theo SKU, gộp nhãn in PDF/HTML đa trang, checkbox & toolbar lô ở web.
- **UC-B08 (Tra cứu đơn khách thật)**: `orders.order_code` đã `UNIQUE` (`services/order/migrations/0001_init.sql:31`). Thiếu: query `GetOrderByCode`, RPC công khai, route `POST /api/v1/orders/lookup` + rate limit riêng. Web `/tra-cuu-don-hang` (`web/src/features/commerce/OrderLookupView.tsx:35-51,486-580`) đang **mock hoàn toàn** từ `web/src/mock/guestOrderLookup.ts:14-126` (4 mã giả `NM-261120-*`, huỷ/đổi địa chỉ giả trong React state).
- **S03 (Lịch sử tồn kho)**: bảng `stock_history` đã tồn tại (`services/product/migrations/000001_init.sql:73-89`) và đã ghi đủ 5 loại `MANUAL_UPDATE`, `RESERVE`, `RELEASE`, `CONFIRM`, `RESTOCK` (`services/product/internal/server/server.go:876,1549,1677,1800,1880,2176`). Thiếu: index `(sku_id, created_at DESC)`, câu `SELECT` trong `services/product/queries/product.sql`, RPC/REST và màn hình xem lịch sử ở `/seller/san-pham/page.tsx:504-562`.
- **S04 (Phân trang đơn seller)**: backend `GET /api/v1/seller/orders` (`services/gateway/internal/router/handlers_order.go:701-727`) & `ListOrders` (`lifecycle.go:82-164`) **đã phân trang thật** (`page`, `page_size <= 100`, `total`). Lỗi ở web: `web/src/app/(seller)/seller/don-hang/page.tsx:86` gọi cứng `page=1&page_size=100`, đếm `counts` và lọc phía client (`page.tsx:101-152`) → quá 100 đơn bị cắt mất; `seller/san-pham/page.tsx:77` cũng cắt ở 100 SP (ảnh hưởng kiểm nghiệm UC-S13 490 SP).
- **B15 (3 voucher hứa sai)**: `web/src/data/vouchers.ts:168-219` khai báo `EDU15` (`eduOnly`), `SINHNHAT50` (`minTierRequired: "bac"`), `NEXUSVIP12` (`minTierRequired: "vang"`); `tools/seed/seed.mjs:1402,1619-1665` seed thẳng cả 3 vào `promotion_db` dù `promotion` (`services/promotion/internal/store/migrations/001_init.sql:3-26`) không có kiểm tra đối tượng (`docs/spec.md:29` xếp hạng hội viên là **Ngoài phạm vi**). Còn nhắc ở `web/src/data/membership.ts:145,157,203`, `AccountMockTabs.tsx:31,127,511`, `MockStoreContext.tsx:507,590`, `hoi-vien/page.tsx:381`, `web/scripts/check-data.mjs:507,546`.
- **§10 `CheckoutSuggestionStrip` & Size finder trong `ShopDetail`**:
  - `CheckoutSuggestionStrip.tsx:84-87` lấy `product.min_price_vnd` thô, chưa trừ flash sale/shop discount; 3 nơi gọi (`gio-hang/page.tsx:696-700,787,818,1543`, `thanh-toan/page.tsx:168-182,312`, `cho-thanh-toan/[paymentId]/page.tsx:93-116`) chưa nạp discount cho SKU gợi ý.
  - `loadShopDetailData` (`web/src/lib/catalog.ts:736,756`) chỉ gọi `fetchActiveShopDiscounts({ shopId: shop.id })`, thiếu giảm giá gian hàng cho sản phẩm xưởng khác trong `allProducts` truyền vào `SizeFinder` (`ShopDetailClient.tsx:811-815`); `SizeFinder.tsx:420` chỉ in `product.price` chưa hiện giá gốc gạch ngang.

## 2. Hợp đồng đề xuất

### 2.1 Protobuf mới (chỉ thêm trường/RPC, không phá tương thích)
- `proto/nexusmart/order/v1/order.proto`:
  - `Order` (sau `37`): `int64 pending_address_surcharge_vnd = 38;`, `string pending_address_payment_id = 39;` (chỉ hiện khi chờ thanh toán phụ thu VietQR).
  - `ListOrdersRequest` (sau `6`): `string carrier_code = 7;`, `string keyword = 8;`, `OrderPaymentMethod payment_method = 9;`, `google.protobuf.Timestamp due_before = 10;`, `repeated OrderStatus statuses = 11;`, `bool include_status_counts = 12;`.
  - `ListOrdersResponse` (sau `2`): `repeated OrderStatusCount status_counts = 3;`.
  - RPC mới trên `OrderService`:
    - `LookupOrder(LookupOrderRequest{order_code, phone})` → `LookupOrderResponse{PublicOrderSummary order}` (chỉ trường đã che PII, không trả `buyer_id`/`street_address`/`commission_vnd`/`label_url`).
    - `UpdateOrderAddress(UpdateOrderAddressRequest{order_id, address_id, OrderAddressSnapshot custom_address, bool accept_fee_increase, bool preview_only, string idempotency_key})` → `UpdateOrderAddressResponse{Order order, int64 old_fee_vnd, int64 new_fee_vnd, int64 fee_delta_vnd, bool requires_surcharge_payment, string surcharge_payment_id, string qr_url, string qr_code, Timestamp payment_expires_at, bool shipment_recreated}`.
    - `CreateBatchShipJob(CreateBatchShipJobRequest{repeated string order_ids, string carrier_code, string service_code})` → `CreateBatchShipJobResponse{BatchShipJob job}`.
    - `GetBatchShipJob(GetBatchShipJobRequest{job_id})` → `GetBatchShipJobResponse{BatchShipJob job}` (kèm `repeated BatchShipJobItem items`).
    - `GetPickingList(GetPickingListRequest{string job_id, repeated string order_ids})` → `GetPickingListResponse{repeated PickingListItem items, int32 order_count}`.
- `proto/nexusmart/shipping/v1/shipping.proto`:
  - `Shipment` (sau `15`): `ShipmentAddress to_address = 16;`.
  - RPC mới: `UpdateShipmentAddress(UpdateShipmentAddressRequest{order_id, ShipmentAddress to_address, int64 cod_amount_vnd, bool update_cod})` → `UpdateShipmentAddressResponse{Shipment shipment, bool recreated}`.
- `proto/nexusmart/payment/v1/payment.proto`:
  - `CreatePaymentRequest` (sau `9`): `string parent_order_id = 10;` (chỉ `order-service` khi tạo phụ thu đổi địa chỉ).
  - RPC mới: `AdjustCODShippingFee(AdjustCODShippingFeeRequest{order_id, int64 new_shipping_fee_vnd, string idempotency_key})` → `AdjustCODShippingFeeResponse{Payment payment}` (chỉ COD `awaiting_collection`).
- `proto/nexusmart/product/v1/product.proto`:
  - RPC mới: `ListStockHistory(ListStockHistoryRequest{shop_id, product_id, sku_id, change_type, Timestamp from_time, Timestamp to_time, int32 page, int32 page_size})` → `ListStockHistoryResponse{repeated StockHistoryItem items, int64 total}`.
  - `CreateProductImportJob(CreateProductImportJobRequest{filename, bytes csv_content, bool auto_confirm})` → `CreateProductImportJobResponse{ProductImportJob job}`; `GetProductImportJob`, `ListProductImportRows`, `ConfirmProductImportJob`.
- Allowlist auth:
  - `pkg/common/auth/auth.go`: thêm `OrderService/LookupOrder` vào `publicRPCs` (`auth.go:154`); đổi `ShippingService/CreateShipment` sang `true` (`auth.go:137`, handler chỉ nhận seller chủ shop hoặc `claims.Service == "order-service"`); thêm `ShippingService/UpdateShipmentAddress: true` (`order-service`/admin); các RPC mới khác đặt `false`.
  - `services/payment/src/auth.rs:21`: thêm `"AdjustCODShippingFee"` vào `SERVICE_METHODS`; nới `validate_service_create_payment` (`grpc.rs`) cho prefix `idempotency_key = "addr-adj:" + change_id` với `parent_order_id != ""`.

### 2.2 Route REST & Quyền (`services/gateway/internal/router/router.go`)
- `POST /api/v1/orders/lookup` — Public + `RateLimitOrderLookup` (IP, `10/min` → `429`), không dùng `GET` để SĐT không vào URL/log.
- `PATCH /api/v1/orders/{id}/address` — Buyer owner (JWT) + `Idempotency-Key` tuỳ chọn (bắt buộc khi `accept_fee_increase=true`).
- `GET /api/v1/seller/orders` — `seller` (mở rộng query: `status`/`statuses`, `carrier_code`, `payment_method`, `keyword`, `due_before`, `page`, `page_size`, trả thêm `status_counts`).
- `POST /api/v1/seller/orders/batch-ship` (`202`), `GET /api/v1/seller/orders/batch-ship/{job_id}`, `POST /api/v1/seller/orders/picking-list` — `seller` (`shop_id` từ JWT, tối đa 100 đơn/lô).
- `POST /api/v1/shipping/labels/batch` — `seller`/`admin`, proxy qua `ShippingLabelProxy` tới `shipping:8087` (`?format=pdf|html`, mặc định `pdf` chuẩn PDF 1.4 thuần Go + `html` đủ dấu tiếng Việt để in trình duyệt; tối đa 100 mã).
- `GET /api/v1/seller/stock-history` & `GET /api/v1/seller/skus/{sku_id}/stock-history` — `seller` (`shop_id` từ JWT).
- `GET /api/v1/seller/products/import/template` (CSV UTF-8 BOM, `?category_id=`), `POST /api/v1/seller/products/import` (`202`, ≤ 1 MiB, ≤ 1000 dòng), `GET /api/v1/seller/products/import/{job_id}`, `GET /api/v1/seller/products/import/{job_id}/errors.csv`, `POST /api/v1/seller/products/import/{job_id}/confirm` — `seller`.

### 2.3 Event NATS JetStream (`NEXUSMART`)
- `order.address_updated` (mới, tái dùng prefix `order.>` đã có):
  - Phát: `order` qua transactional `outbox_events` trong cùng TX cập nhật `orders` (`aggregate_id = order_id`, sau khoá `orders FOR UPDATE`).
  - Payload: `{"order_id","order_code","buyer_id","shop_id","old_fee_vnd","new_fee_vnd","tracking_code","shipment_recreated","province_name","ward_name"}` (không đưa `street_address`/`phone` thô vào log/DLQ).
  - Nhận: `notification` (idempotent bằng `processed_events(event_id)` trong `ProcessEventTx`) → gửi in-app + email báo **người bán** (đặc biệt nhắc in lại nhãn nếu `shipment_recreated=true`) và in-app xác nhận cho người mua.
- Phụ thu VietQR UC-B26 tái dùng `payment.succeeded` / `payment.expired` / `payment.failed` hiện có với `order_group_id = "addr-adj:" + change_id`, `order_id = "addr-adj:" + change_id` (khớp `processed_events(event_id)` ở `order`).

### 2.4 Migration & Biến môi trường
- `services/order/migrations/0015_w3b_a8_address_and_batch_ship.sql`:
  - `order_address_changes(id PK, order_id FK, new_address_json JSONB, old_fee_vnd BIGINT, new_fee_vnd BIGINT, delta_fee_vnd BIGINT CHECK >= 0, surcharge_payment_id TEXT, status CHECK IN ('PENDING_PAYMENT','APPLIED','CANCELLED','EXPIRED'), expires_at TIMESTAMPTZ, created_at, updated_at)` + partial unique index `(order_id) WHERE status = 'PENDING_PAYMENT'`.
  - `seller_batch_jobs(...)`, `seller_batch_job_items(..., UNIQUE(job_id, order_id))` + index `idx_seller_batch_jobs_queued` (`WHERE status = 'QUEUED'`), index `orders (shop_id, carrier_code, created_at DESC)`.
- `services/product/migrations/000008_w3b_a8_import_jobs_and_stock_idx.sql`:
  - `CREATE INDEX IF NOT EXISTS idx_stock_history_sku_created ON stock_history (sku_id, created_at DESC, id DESC);`
  - `product_import_jobs(...)` (`raw_csv BYTEA`, `status`, counters, lease `claimed_until`), `product_import_rows(..., UNIQUE(job_id, row_number))` + partial index `WHERE status IN ('QUEUED','CONFIRMING')`.
- `services/shipping/migrations/000006_w3b_a8_shipment_recipient.sql` (idempotent `IF NOT EXISTS`):
  - `ALTER TABLE shipments ADD COLUMN IF NOT EXISTS to_name/to_phone/to_street_address/from_name/from_phone/from_street_address TEXT NOT NULL DEFAULT '';`
- `services/payment/migrations/0009_w3b_a8_parent_order_surcharge.sql`:
  - `ALTER TABLE payments ADD COLUMN IF NOT EXISTS parent_order_id TEXT NOT NULL DEFAULT '';` + partial index `WHERE parent_order_id <> ''` (để `order.completed` / `order.cancelled` settle hoặc refund kèm phụ thu VietQR đã trả mà không phá `idx_payments_one_succeeded_per_order`).
- Biến env mới (`.env.example`, `docker-compose.yml`, `docs/contracts.md` §7):
  - `RATE_LIMIT_ORDER_LOOKUP_PER_MIN=10` (gateway), `PRODUCT_IMPORT_MAX_ROWS=1000`, `PRODUCT_IMPORT_MAX_BYTES=1048576` (product/gateway), `ORDER_BATCH_SHIP_MAX_ITEMS=100` (order). Không thêm dependency hay container mới.

## 3. Bất biến tiền / quyền / đua & bài kiểm tấn công

- **Bất biến tiền UC-B26 (Double-entry & trần COD)**:
  - Tiền nguyên `int64` VND; `sum(R + Ship - D) == payments.amount_vnd` luôn giữ.
  - COD tăng phí: `new_total <= ORDER_COD_MAX_VND` (5.000.000₫) và `AdjustCODShippingFee` chỉ sửa khi `escrow_status = 'awaiting_collection'` (chưa có bút toán sổ cái).
  - VietQR đã trả tăng phí: phụ thu tạo payment riêng (`order_id = "addr-adj:" + change_id`, `parent_order_id = order.id`, `subtotal_vnd = 0`, `shipping_fee_vnd = delta`); nếu đơn đã chuyển `SHIPPING`/`CANCELLED` trước khi webhook `payment.succeeded` về → `order` không đổi địa chỉ mà gọi `Payment.CreateRefund` hoàn trả `delta` (idempotent `addr-adj-refund:<change_id>`). Khi `order.completed`, `payment` settle luôn `shipping_fee_payable` cho cả payment gốc và payment phụ thu; tổng `Debit == Credit`.
  - *Bài test đua*: (1) Buyer đổi địa chỉ tăng phí đồng thời với webhook `shipment.updated=delivering` → hoặc địa chỉ đổi xong trước pickup, hoặc webhook thắng và đổi địa chỉ trả `409` / auto-refund phụ thu; không bao giờ lệch địa chỉ giữa `orders` và `shipments`. (2) Gửi 2 request `PATCH /orders/{id}/address` đồng thời → partial unique `PENDING_PAYMENT` + khoá `orders FOR UPDATE` chỉ cho 1 thay đổi.
- **Bất biến quyền & chống lộ PII**:
  - `LookupOrder`: so SĐT toàn chuỗi sau chuẩn hoá bằng `subtle.ConstantTimeCompare`; sai mã hoặc sai SĐT trả cùng `404`; vượt `10/min/IP` trả `429` + `Retry-After`; response không chứa `street_address`, `buyer_id`, `commission_vnd`, `label_url`.
  - *Bài test tấn công*: (1) Dò 15 cặp mã đơn/SĐT từ 1 IP → từ lần 11 nhận `429`. (2) Gửi đúng `order_code` + sai SĐT vs sai `order_code` → status code & thông báo giống hệt. (3) Buyer B gọi `PATCH /orders/{orderA}/address` → `403`. (4) Seller B gọi `GET /seller/skus/{skuA}/stock-history`, `GET /seller/products/import/{jobA}`, `POST /seller/orders/batch-ship` chứa 1 đơn của shop A, hoặc `POST /shipping/labels/batch` chứa 1 mã vận đơn của shop A → tất cả trả `403`. (5) CSV injection (`=CMD|...`) trong tên sản phẩm/lỗi xuất `errors.csv` bị thêm tiền tố `'`.
- **Bất biến đua UC-S15 & UC-S13**:
  - Batch ship chạy song song với buyer `CancelOrder`: worker khoá `orders FOR UPDATE` và dùng cơ chế `pending_shipment_cancel` sẵn có (`lifecycle.go:668-746`); đơn đã `CANCELLED` ghi `SKIPPED_CANCELLED`, không để lọt vận đơn mồ côi.
  - Import confirm bấm 2 lần đồng thời: chuyển trạng thái nguyên tử `WHERE status = 'READY'` → `CONFIRMING` dưới `FOR UPDATE`; lần 2 trả nguyên job (idempotent), không nhân đôi 490 sản phẩm.

## 4. Câu phải chốt (đề xuất để manager duyệt)

1. **UC-B26 phí ship mới CAO hơn trên đơn VietQR đã thanh toán (`CONFIRMED`/`PREPARING`)**:
   - *Đề xuất*: Hỗ trợ đầy đủ cả **COD** (xác nhận tăng COD tại lúc giao) lẫn **VietQR đã thanh toán** (tạo QR phụ thu `parent_order_id = order.id`, trả xong mới áp địa chỉ & cập nhật vận đơn); riêng **VietQR `PENDING` chưa trả** mà tăng phí → trả `409` yêu cầu huỷ đặt lại để tránh race supersede nhóm đa gian hàng. *Lý do*: đạt đúng tiêu chí "phí mới cao hơn thì trả thêm hoặc giữ địa chỉ cũ" mà giữ nguyên sổ cái ghi kép và 2 unique index hiện có.
2. **UC-B26 phí ship mới THẤP hơn (`delta < 0`)**:
   - *Đề xuất*: Cho đổi địa chỉ ngay, **giữ nguyên `shipping_fee_vnd` / `total_payable_vnd`** (không hoàn chênh lệch nhỏ). *Lý do*: tránh phát sinh quy trình hoàn tiền thủ công cho vài nghìn đồng và không phá công thức phân bổ `P + D = R + Ship`.
3. **UC-S13 định dạng file nhập sản phẩm**:
   - *Đề xuất*: Chỉ nhận **CSV UTF-8 (có BOM)** (tối đa 1000 dòng / 1 MiB), chưa nhận `.xlsx`. *Lý do*: Go stdlib `encoding/csv` đủ dùng, không thêm dependency mới, Excel mở/lưu CSV UTF-8 BOM chuẩn tiếng Việt, khớp với `seller/reports/export` hiện tại.
4. **UC-S15 định dạng in nhãn gộp PDF**:
   - *Đề xuất*: Endpoint `POST /api/v1/shipping/labels/batch` hỗ trợ `?format=pdf` (sinh PDF 1.4 chuẩn nhiều trang A6 thuần Go không thêm thư viện, tiếng Việt không dấu chuẩn tem nhiệt để test `%PDF-1.4` thật) **và** `?format=html` (HTML gộp `@page { size: A6 }` đủ dấu tiếng Việt + `Ctrl+P`, mở từ web). *Lý do*: thỏa mãn cả chữ "1 file PDF" của UC-S15 lẫn luật không thêm dependency/font ngoài.
5. **B15 xử lý 3 voucher `EDU15`, `SINHNHAT50`, `NEXUSVIP12`**:
   - *Đề xuất*: **Gỡ hẳn** khỏi `tools/seed/seed.mjs` (kèm dọn `DELETE /api/v1/admin/vouchers/{id}` trên volume cũ nếu đã tồn tại), bỏ khỏi `VOUCHERS` thật và cập nhật copy/test ở `web/src/data/*` + `web/scripts/check-data.mjs` (nhãn rõ tính năng Hội viên là minh họa, chưa phát hành mã thanh toán). *Lý do*: chương trình thành viên theo hạng nằm ở cột "Ngoài phạm vi" (`docs/spec.md:29`); giữ mã tên `EDU15`/`NEXUSVIP12` với luật công khai vẫn gây hiểu nhầm.

## 5. Chia việc theo service, thứ tự & điểm giao nhau

- **Bước 0 — Hợp đồng & nền chung (tuần tự, làm trước mọi service)**:
  - `proto/nexusmart/{order,shipping,product,payment}/v1/*.proto` + `make proto` + `docs/contracts.md` (~280 dòng).
  - `pkg/common/auth/auth.go` + `grpc_trust_test.go` (~35 dòng).
  - *Điểm xung đột với các mảng 3b khác*: cùng sửa `product.proto` (A4/B1), `shipping.proto` (A5), `payment.proto` (A5/A6), `auth.go`, và đánh số migration (`product 000008_`, `order 0015_`, `shipping 000006_`, `payment 0009_`) → phải chốt số migration + merge Bước 0 trước khi rẽ nhánh.
- **Bước 1 — Các service độc lập (chạy song song sau Bước 0)**:
  - `services/shipping` (~420 dòng): `migrations/000006_*`, `internal/carrier/{carrier,ghn,mock_express}.go` (`UpdateShipmentAddress`), `internal/service/shipping.go`, `internal/store/store.go`, `internal/grpcserver/server.go`, `internal/webhook/{label_handler,batch_label}.go` (PDF + HTML gộp), `cmd/server/main.go`.
  - `services/payment` (~260 dòng): `migrations/0009_*`, `src/{auth,grpc,service,domain}.rs` (`AdjustCODShippingFee`, `parent_order_id` surcharge settle/refund).
  - `services/product` (~750 dòng): `migrations/000008_*`, `queries/product.sql` + `sqlc generate`, `internal/store/{store,pg_store,memory_store}.go`, `internal/server/{server,stock_history,import_job}.go`, worker nền trong `cmd/server/main.go`. *(Chú ý: tránh chạy song song với A4/B1 T1 trên `services/product`)*
  - `services/notification` (~80 dòng): `internal/service/notification.go` thêm nhánh `case "order.address_updated":`.
  - `tools/seed/seed.mjs` (~40 dòng): lọc & dọn 3 mã B15.
- **Bước 2 — `services/order` & `services/gateway` (sau Bước 1)**:
  - `services/order` (~900 dòng): `migrations/0015_*`, `queries/orders.sql` + `sqlc generate`, `internal/store/postgres.go`, `internal/events/types.go`, `internal/service/{address_update,batch_ship,lookup,lifecycle,handlers}.go` (kèm watchdog hết hạn `order_address_changes`).
  - `services/gateway` (~550 dòng): `internal/config/config.go`, `internal/middleware/ratelimit.go`, `internal/router/{router,handlers_order,handlers_product,handlers_shipping}.go`, `internal/proxy/proxy.go` + `.env.example`, `docker-compose.yml`. *(Điểm giao nhau: `router.go` dùng chung với A4–A7)*.
- **Bước 3 — `web/` (~1.100 dòng) & Test E2E**:
  - `web/src/lib/{gateway-types,catalog}.ts`, `web/src/features/checkout/CheckoutSuggestionStrip.tsx`, `web/src/features/catalog/SizeFinder.tsx`, `web/src/app/(shop)/{gio-hang/page,thanh-toan/page,thanh-toan/CheckoutClient,thanh-toan/cho-thanh-toan/[paymentId]/page,thanh-toan/cho-thanh-toan/[paymentId]/PaymentWaitingClient}.tsx` (§10 giá).
  - `web/src/features/commerce/OrderLookupView.tsx` + xoá dùng `mock/guestOrderLookup.ts` (UC-B08).
  - `web/src/features/account/OrderDetailClient.tsx` (UC-B26 form đổi địa chỉ + xác nhận phí thêm / QR phụ thu).
  - `web/src/app/(seller)/seller/don-hang/page.tsx` (S04 phân trang server + bộ lọc hãng/hạn + UC-S15 chọn lô, tiến độ job, in nhãn gộp, picking list).
  - `web/src/app/(seller)/seller/san-pham/page.tsx` + component modal/drawer (S03 lịch sử tồn kho, UC-S13 nhập CSV 2 bước + tải lỗi, phân trang SP).
  - `web/src/data/{vouchers,membership}.ts`, `AccountMockTabs.tsx`, `MockStoreContext.tsx`, `hoi-vien/page.tsx`, `web/scripts/check-data.mjs` (B15).
  - `tests/e2e/41_a8_buyer_seller_test.go`, `tests/e2e-web/09_a8_buyer_seller.test.mjs`.

## 6. Bài nghiệm thu (E2E + trình duyệt thật)

- **E2E API (`tests/e2e/41_a8_buyer_seller_test.go`)**:
  - UC-B26: đổi địa chỉ cùng tỉnh trên đơn `PREPARING` đã có vận đơn `mock_express` → `shipment_recreated=true`, mã vận đơn mới cập nhật trên đơn, seller nhận thông báo `order.address_updated`; đổi sang tỉnh phí cao hơn với `accept_fee_increase=false` → `409` giữ địa chỉ cũ; với `accept_fee_increase=true` trên COD → COD & `total_payable_vnd` tăng đúng `delta`; trên VietQR đã trả → tạo QR phụ thu, bắn webhook thanh toán → đơn cập nhật địa chỉ + `ledger/summary` cân bằng; đơn đã `SHIPPING` → từ chối `409`.
  - UC-S13: upload CSV 500 dòng (490 hợp lệ + 10 dòng lỗi giá âm/sai danh mục/trùng SKU) → job vào `READY` báo `490/10`, tải `errors.csv` đủ 10 dòng có lý do (và guard `=`), bấm `confirm` → tạo đúng 490 sản phẩm `PENDING`; gửi file sai header hoặc 1001 dòng → `400`.
  - UC-S15: tạo 5 đơn `CONFIRMED` (huỷ 1 đơn, 4 đơn còn lại chạy `batch-ship`) → job kết thúc 4 `SUCCEEDED`, 1 `SKIPPED_CANCELLED`; gọi `picking-list` khớp tổng SL từng SKU; gọi `POST /shipping/labels/batch?format=pdf` trả `%PDF-1.4` hợp lệ; lẫn 1 mã shop khác → `403`.
  - UC-B08: tra đúng `(order_code, phone)` → `200`, không lộ `street_address`/`buyer_id`/`commission_vnd`; sai SĐT hoặc sai mã → cùng `404`; bắn 11 request/phút cùng IP → lần 11 trả `429` + `Retry-After`.
  - S03 & S04 & B15: sửa tồn SKU + đặt/huỷ đơn → `GET /seller/skus/{id}/stock-history` đủ các dòng `MANUAL_UPDATE`/`RESERVE`/`RELEASE`, shop khác gọi trả `403`; tạo 25 đơn seller → `page=1&page_size=20` trả 20, `page=2` trả 5, `total=25`; `GET /vouchers` không còn `EDU15`/`SINHNHAT50`/`NEXUSVIP12`.
- **Trình duyệt thật (Playwright 390/768/1440, `tests/e2e-web/09_a8_buyer_seller.test.mjs`)**:
  1. Mở `/tra-cuu-don-hang` (chưa đăng nhập) → không còn nút đơn mẫu giả; nhập mã đơn thật + SĐT người nhận → thấy trạng thái/timeline thật, địa chỉ chỉ hiện phường/tỉnh (không lộ số nhà).
  2. Đăng nhập buyer, mở `/don-hang/{id}` (đơn COD `CONFIRMED`) → bấm "Sửa địa chỉ", chọn tỉnh khác phí cao hơn → thấy cảnh báo chênh lệch phí `+X₫`, bấm "Giữ địa chỉ cũ" → không đổi; lặp lại bấm "Xác nhận trả thêm" → địa chỉ và tổng COD cập nhật ngay.
  3. Đăng nhập seller, mở `/seller/don-hang` → thấy thanh phân trang `1–20 / tổng N`, chuyển trang 2 tải đúng đơn cũ; lọc `CONFIRMED` + tick 3 đơn → bấm "Tạo vận đơn hàng loạt" → thấy tiến độ hoàn tất, bấm "In nhãn gộp" mở trang in 3 nhãn và bấm "In danh sách nhặt hàng" thấy bảng gom theo SKU.
  4. Mở `/seller/san-pham` → mở 1 SP → bấm "Sửa tồn" lưu số mới → bấm "Lịch sử tồn" thấy dòng vừa thay đổi (`before → after`, `delta`); bấm "Nhập file CSV" tải mẫu, upload file có dòng lỗi → thấy bảng lỗi theo dòng + nút tải CSV lỗi → bấm "Xác nhận nhập dòng hợp lệ" → sản phẩm mới hiện ở tab "Chờ duyệt".
  5. Mở `/gio-hang`, `/thanh-toan`, `/gian-hang/{slug}` (tab "Bảng size của xưởng") với 1 SKU đang có Flash Sale và 1 SKU đang có Shop Discount → cả `CheckoutSuggestionStrip` và `SizeFinder` hiển thị đúng giá đã giảm kèm giá gốc gạch ngang.

## 7. Rủi ro & thứ nên cắt nếu quá lớn

- **Rủi ro chính**:
  - **Độ phức tạp phụ thu VietQR của UC-B26**: chạm cả `payment` (Rust), `order` (Go) và watchdog hết hạn phụ thu — là phần rủi ro tiền & thời gian lớn nhất trong A8.
  - **Xung đột `services/product` với A4/B1**: A4 (đánh giá + từ cấm), B1 T1 (outbox) và A8 (S03 + UC-S13) cùng sửa `product.proto`, `queries/product.sql`, `migrations/000008_*`.
  - **Tải 500 dòng CSV qua BFF/gRPC**: thời gian insert 490 SP nếu chạy đồng bộ sẽ vượt timeout `15s` của BFF (`web/src/app/api/gw/[...path]/route.ts:20`), bắt buộc chạy worker nền `202` + polling (đã thiết kế ở §2).
  - **Phân trang `/seller/san-pham`**: hiện cũng cắt ở 100 (`seller/san-pham/page.tsx:77`); nếu không làm luôn phân trang server cùng UC-S13 thì seller không xem được sản phẩm thứ 101–490.
- **Thứ tự cắt nếu 3b quá tải (giữ các mục Cao và lỗi sai sự thật trước)**:
  1. **Giữ bắt buộc**: B15 (gỡ 3 voucher hứa sai), S04 (phân trang đơn seller), S03 (API + màn lịch sử tồn), UC-B08 (tra cứu đơn thật + rate limit), §10 (2 khối gợi ý giá).
  2. **Cắt/thu gọn trước nếu thiếu công**:
     - Nhánh **phụ thu VietQR đã trả** của UC-B26: nếu quá lớn, chốt bản 3b chỉ cho trả thêm trên **COD** (không chạm Rust `payment` trừ `AdjustCODShippingFee`), còn VietQR đã trả có `delta > 0` trả `409` yêu cầu giữ địa chỉ cũ hoặc huỷ đặt lại (đẩy phụ thu VietQR sang 3c).
     - Nhánh **PDF binary** của UC-S15: cắt bộ sinh `%PDF-1.4`, giữ bản HTML gộp `@page { size: A6 }` + nút `window.print()` (khớp thiết kế `label_handler.go` hiện có).
     - `UC-S13` + `UC-S15` đều là mức **Trung bình** (`docs/spec.md:130,132`); theo `w3-plan.md:92`, có thể đẩy toàn bộ UC-S13/UC-S15 sang cuối 3b sau khi B1 và các mục Cao xong.
