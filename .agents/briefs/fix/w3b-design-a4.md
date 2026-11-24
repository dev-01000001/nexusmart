# Thiết kế đợt 3b — Mảng A4 (UC-B10, UC-A15, B04 còn lại) & §10 (Consumer DLQ/Drain, CartItem.unavailable)

> Nền khảo sát: working tree đã có 3a (chưa commit). Số kế tiếp thực tế: migration `product 000008_`, `promotion 005_`, `payment 0009_`; e2e `41_`, e2e-web `09_`.

## 1. Hiện trạng

### Đã có và chạy thật (không viết lại)
- **Outbox `product`**: bảng `outbox_events` + lease/seq D9-bis (`services/product/migrations/000005_w2b_catalog_outbox_price_fee.sql:1-50`, `000007_w3a_outbox_lease_and_sku_index.sql:8-45`), worker out-of-tx (`services/product/internal/store/pg_store.go:147-260`, `services/product/internal/events/outbox.go:43-128`). Hiện mới phát `product.reviewed` (`services/product/internal/server/server.go:1131,1179`).
- **Xác minh đơn hàng**: `OrderService.GetOrder` (`services/order/internal/service/lifecycle.go:30-52`) đã kiểm tra quyền sở hữu buyer từ JWT và trả `status`, `buyer_id`, `shop_id`, `items[]` (`id`, `sku_id`, `product_id`, `sku_name`).
- **Chuẩn hoá tiếng Việt cơ bản**: `FoldVietnameseRune` + `NormalizeSearchText` (`services/product/internal/store/search.go:10-53`), gói `golang.org/x/text` đã có sẵn (`services/product/go.mod:45`).
- **Audit log admin tự động**: middleware tự ghi mọi `POST/PUT/PATCH/DELETE` 2xx dưới `/api/v1/admin/*` (`services/gateway/internal/middleware/audit.go:120-375`).
- **Kiểm duyệt SP thật**: `AdminReviewProduct` (`services/product/internal/server/server.go:1048-1190`) + tab `AdminProductModerationTab` (`web/src/features/admin/AdminProductModerationTab.tsx:34-542`). Giá gốc gạch ngang PDP đã chạy thật (`web/src/lib/catalog-mapper.ts:449-538`).
- **SKU bị ẩn đã trả bản rút gọn**: `GetSKUsBatch` (`services/product/internal/server/server.go:1377-1385,2285-2295`) đã trả `id, product_id, shop_id, product_name, name, image_url, is_active=false` với giá/tồn = 0.
- **Consumer mẫu ở `order`**: `stopping + wg`, không gọi `Drain()`, lỗi shutdown không vào DLQ, chỉ `Ack` khi DLQ ghi được ≥1 nơi bằng `context.WithoutCancel` (`services/order/internal/events/consumer.go:123-149,172-280,327-373`).
- **`notification` nuốt an toàn subject mới**: `buildNotificationPlan` có nhánh `default:` ack sạch không làm bẩn DLQ (`services/notification/internal/service/notification.go:1405-1408`).

### Thiếu so với UC & §10
- **UC-B10**: `product` chưa có bảng/RPC review, chưa kết nối `ORDER_GRPC_ADDR`/`USER_GRPC_ADDR` (`services/product/cmd/product/main.go:108-120`, `docker-compose.yml:119-148`), chưa có `"review.>"` trong `CanonicalPrimarySubjects` (`pkg/common/jetstream/stream.go:31-40`) và `CANONICAL_PRIMARY_SUBJECTS` (`services/payment/src/workers.rs:39-46`). `OrderDetailClient` chưa có nút/form đánh giá trên đơn `COMPLETED` (`web/src/features/account/OrderDetailClient.tsx:1749-1825`).
- **UC-A15**: chưa có bảng `banned_keywords`, chưa có cờ `categories.is_prohibited` (`services/product/migrations/000001_init.sql:3-12`). `CreateProduct` (`server.go:528`) và `UpdateProduct` (`server.go:676-740`, lỗ hổng: seller sửa SP `APPROVED` chèn từ cấm vẫn giữ `APPROVED`) chưa quét từ cấm. `search.go:10-53` chưa chống lách (zero-width, chấm/gạch/cách giữa chữ, leetspeak, homoglyph, lặp ký tự). Chưa có xem trước số SP bị ảnh hưởng và quét lại SP đang bán.
- **UC-B04 còn lại**: SP `HIDDEN`/xoá mềm trả `NotFound` → HTTP 404 → `notFound()` trần, không có thông báo ngừng bán + gợi ý tương tự (`services/product/internal/server/server.go:1241-1243`, `services/gateway/internal/router/handlers_product.go:649-652`, `web/src/lib/catalog.ts:520-523`, `web/src/app/(shop)/san-pham/[slug]/page.tsx:65-67`). `estimateDelivery` thuộc A5 (`ProductDetailClient.tsx:49,272`), tóm tắt AI thuộc B3 (`UC-AI05`).
- **§10(a) 4 consumer lỗi**:
  - `shipping` (`services/shipping/internal/events/consumer.go:71-162`): `Close()` gọi `sub.Drain()` (L74); không có `stopping/wg`/guard shutdown; `sendToDLQ` (L137-162) dùng `ctx` gốc, không kiểm lỗi DB/NATS, trả void, vẫn `Ack` (L100-101,107-108,126-127).
  - `notification` (`services/notification/internal/consumer/consumer.go:147-244`): `Close()` gọi `sub.Drain()` (L150); thiếu guard shutdown; `routeToDLQ` (L209-244) dùng `ctx` gốc, `PublishDLQ` không nhận `ctx` (L31,65-70), bỏ qua lỗi vẫn `Ack` (L179-180,193-194). Phải giữ `sanitizeDLQPayload` (L246-302).
  - `promotion` (`services/promotion/internal/events/consumer.go:107-255`): `Stop()` gọi `sub.Drain()` (L134); dùng `Subscribe` không phải `QueueSubscribe` (L107, lỗi khi >1 replica); `MaxDeliver(5)` thiếu headroom (L113); `PublishDLQ` bỏ qua `_ ctx` (L248) và lỗi, vẫn `Ack`; DB chưa có `dead_letter_events` (`migrations/001..004`).
  - `payment` (`services/payment/src/workers.rs:1055-1315`): đã là PullConsumer không `Drain()`, nhưng `js.publish(...)` ở L1199,1290 bỏ qua cả `Result` lẫn `PublishAckFuture` rồi vẫn `msg.ack()`! `max_deliver = 5` thiếu headroom (L1086); không kiểm `shutdown` trước DLQ; thiếu `order_id` ở L1230-1233 tự `ack()` bỏ mất event không vào DLQ; DB chưa có `dead_letter_events` (`migrations/0001..0008`).
- **§10(b) Giỏ ẩn SKU**: `cart.buildCart` (`services/cart/internal/server/server.go:378-380`) `continue` bỏ luôn SKU `!is_active`/`price<=0` trong khi key vẫn nằm Redis. `order.resolveCheckoutInputs` (`services/order/internal/service/checkout.go:1247-1262`) chưa biết trường `unavailable`; web `CartContext.tsx:219-299`, `gio-hang/page.tsx:343-360`, `CheckoutClient.tsx:333-349` chưa có nhánh hiển thị/lọc item `unavailable`.

### Dữ liệu mẫu ở web cần thay/đánh dấu
- `web/src/features/product/PdpReviews.tsx:37-128,163,209` + `MockStoreContext.tsx:210,404` + `data/membership.ts:356`: 100% `SAMPLE_REVIEWS`, cho khách vãng lai tự gửi review cục bộ.
- `web/src/components/ProductCard.tsx:89,137-152,427-437`: đọc sao từ `useMockStore().reviews` (`"Điểm đánh giá mẫu..."`), không đọc `GatewayProduct`.
- `web/src/app/(admin)/admin/kiem-duyet/page.tsx:48-54,220-252,950-1076` + `auditStore.ts:44,122` + `data/admin.ts:1801-1834`: bộ lọc từ cấm mock trong tab `qa` dưới banner dữ liệu mẫu.
- `web/src/features/product/PdpCrossSell.tsx:369`: fallback tĩnh `allProducts.length || 18`. (Ghi chú: `PdpQuestions` L1318, `PdpBackInStock` L786, `productEditorial.ts` L31 là phần ngoài B04/3b.)

## 2. Hợp đồng đề xuất

### Proto (`proto/nexusmart/product/v1/product.proto`, `proto/nexusmart/cart/v1/cart.proto`)
- `product.proto` — mở rộng message hiện có (số trường kế tiếp thật):
  - `Category` (cuối `8` L32): `bool is_prohibited = 9;` (+ `CreateCategoryRequest` trường `6` sau L90; `UpdateCategoryRequest` trường `10: bool is_prohibited` + `11: bool has_is_prohibited` sau L107 theo mẫu CAT-B9 để update từng phần không reset cờ).
  - `Product` (cuối `14` L82): `int32 rating_avg_x100 = 15;` (0..500, số nguyên tránh sai số thực), `int32 review_count = 16;`, `repeated string flagged_keywords = 17;`.
  - `GetProductRequest` (cuối `2` L182): `bool include_discontinued_stub = 3;`.
  - `GetProductResponse` (cuối `1` L186): `bool discontinued = 2;`, `string discontinued_category_id = 3;`, `string discontinued_name = 4;`.
- `product.proto` — RPC mới trên `ProductService`:
  - Review: `CreateReview` (buyer JWT: `order_id, order_item_id, rating [1..5], comment [<=2000], image_urls [<=5]`), `ListProductReviews` (public: `product_id, rating_filter, has_images, page, page_size` → `reviews[], total, rating_avg_x100, review_count, map<int32,int32> rating_counts`), `ListOrderReviews` (buyer JWT: `order_id` → `reviews[]`).
  - Từ cấm: `ListBannedKeywords`, `CreateBannedKeyword`, `UpdateBannedKeyword`, `DeleteBannedKeyword`, `PreviewBannedKeyword` (`term` → `affected_count, samples[<=20]`), `RescanProductsForBannedContent` (`keyword_ids[], dry_run` → `scanned_count, blocked_count, flagged_count, samples[<=20]`) — tất cả `ADMIN`; thêm `CheckBannedContent` (`text` → `blocked, flagged, matched_terms[]`) vào `internalRPCs` (`pkg/common/auth/auth.go:38`) cho `chat` B2 tái dùng.
- `cart.proto` — `CartItem` (cuối `13` L20): `bool unavailable = 14;`, `string unavailable_reason = 15;` (`SKU_INACTIVE` | `SKU_NOT_FOUND`). `Cart.total_items` và `total_vnd`/`subtotal_vnd` chỉ cộng item khả dụng.

### REST Gateway & quyền (`services/gateway/internal/router/router.go:148-151,324-332`)
- Public: `GET /api/v1/products/{id}/reviews`, `GET /api/v1/products/slug/{slug}` (bổ sung trả `200 {discontinued: true, category_id, name}` khi SP ngừng bán, vẫn `404` với slug không tồn tại/nháp `PENDING`).
- Buyer JWT (`RequireAuth`): `POST /api/v1/orders/{order_id}/reviews`, `GET /api/v1/orders/{order_id}/reviews`.
- Admin JWT (`RequireRole("ADMIN")`, tự động qua `middleware/audit.go`): `GET/POST /api/v1/admin/banned-keywords`, `PUT/DELETE /api/v1/admin/banned-keywords/{id}`, `POST /api/v1/admin/banned-keywords/preview`, `POST /api/v1/admin/banned-keywords/rescan`; thêm `is_prohibited` vào `POST/PUT /api/v1/admin/categories`. Cập nhật `auditTargetType` + `extractTargetIDFromJSON` (`middleware/audit.go:294,367`).
- Cart DTO (`handlers_cart.go:12-66`): ánh xạ `unavailable`, `unavailable_reason`.

### Event NATS JetStream
- Bổ sung `"review.>"` vào CẢ HAI nơi: `pkg/common/jetstream/stream.go:31-40` và `services/payment/src/workers.rs:39-46`.
- **`review.created`**: phát bởi `services/product` qua `outbox_events` (aggregate lock `products FOR UPDATE` trước insert). Nhận: hiện chưa có consumer trong A4 (B3 `ai-assistant` sẽ đăng ký cho `UC-AI05`; `notification` tự ack an toàn ở `notification.go:1405-1408`). Payload: `{review_id, product_id, shop_id, order_id, order_item_id, buyer_id, rating, review_count, rating_avg_x100, occurred_at}`. Idempotent consumer bằng `event_id` (`processed_events`) + dedup phụ `review_id`.
- **`product.reviewed`** (đã có): tái dùng khi `RescanProductsForBannedContent` chuyển SP vi phạm `BLOCK`/hàng cấm sang `REJECTED` để `notification` báo seller ngay.

### Migration (forward-only, không thêm dependency/hạ tầng mới)
- **`services/product/migrations/000008_w3b_reviews_and_moderation.sql`**:
  - `ALTER TABLE categories ADD COLUMN is_prohibited BOOLEAN NOT NULL DEFAULT FALSE;`
  - `ALTER TABLE products ADD COLUMN rating_sum BIGINT NOT NULL DEFAULT 0 CHECK (rating_sum >= 0), ADD COLUMN review_count INT NOT NULL DEFAULT 0 CHECK (review_count >= 0), ADD COLUMN flagged_keywords JSONB NOT NULL DEFAULT '[]'::jsonb;`
  - `CREATE TABLE product_reviews (id TEXT PK, product_id TEXT FK products, shop_id TEXT, order_id TEXT, order_item_id TEXT NOT NULL UNIQUE, sku_id TEXT, sku_name TEXT, buyer_id TEXT, buyer_name TEXT, rating SMALLINT CHECK (1..5), comment TEXT, image_urls JSONB DEFAULT '[]', status TEXT DEFAULT 'PUBLISHED' CHECK (IN ('PUBLISHED','HIDDEN')), created_at, updated_at);`
    - Index: `idx_product_reviews_product_created (product_id, created_at DESC) WHERE status='PUBLISHED'`, `idx_product_reviews_product_rating (product_id, rating) WHERE status='PUBLISHED'`, `idx_product_reviews_order_buyer (order_id, buyer_id)`.
  - `CREATE TABLE banned_keywords (id TEXT PK, term TEXT NOT NULL, normalized_term TEXT NOT NULL UNIQUE, category TEXT NOT NULL DEFAULT 'GENERAL', action TEXT NOT NULL CHECK (action IN ('BLOCK','REVIEW')), is_active BOOLEAN NOT NULL DEFAULT TRUE, created_by TEXT DEFAULT '', created_at, updated_at);`
    - Index: `idx_banned_keywords_active (action) WHERE is_active = TRUE`.
- **`services/promotion/internal/store/migrations/005_dead_letter_events.sql`** & **`services/payment/migrations/0009_dead_letter_events.sql`**: tạo bảng `dead_letter_events (id, event_id, subject, payload_json, error_message, attempts, created_at)` theo chuẩn `shipping`/`notification` + `AGENTS.md` luật 4 (bảo đảm DLQ vẫn sống khi NATS tắt).

### Biến môi trường mới (`docker-compose.yml`, `.env.example`)
- `services/product`: `ORDER_GRPC_ADDR=order:50055`, `USER_GRPC_ADDR=user:50051`. **Không** thêm `depends_on: order` trong `docker-compose.yml` (vì `order` đã phụ thuộc `product` ở L387 — dial lười bằng `grpc.NewClient` + circuit breaker/timeout 3s).

## 3. Bất biến tiền/quyền/đua & bài test kiểm chứng

- **Quyền sở hữu review (IDOR/ID spoof)**: `product` gọi `OrderService.GetOrder` chuyển tiếp JWT buyer, tự kiểm `ord.BuyerId == claims.UserID()` (chặn cả admin/seller giả danh buyer) và lấy `product_id, shop_id, sku_id` từ `ord.Items` (bỏ qua input client). *Test tấn công*: Buyer B dùng `order_id/order_item_id` của Buyer A → `403 PermissionDenied`; Admin dùng `order_id` của Buyer A → `403`; Buyer A gửi đơn `DELIVERED`/`CANCELLED` → `400 FailedPrecondition`.
- **Mỗi dòng hàng chỉ 1 review + nhất quán `rating_sum`/`review_count`**: `UNIQUE(order_item_id)` + `SELECT ... FOR UPDATE` dòng `products` trước khi `INSERT product_reviews` + `UPDATE products` + `INSERT outbox_events`. *Test đua*: 10 goroutine gọi `CreateReview` cùng `order_item_id` → đúng 1 `201`, 9 `409 AlreadyExists`, `review_count` tăng đúng 1, đúng 1 dòng `outbox_events`.
- **Chống lách từ cấm & lỗ hổng sửa SP đã duyệt**: chuẩn hoá NFKC → bỏ ký tự zero-width (`unicode.Cf`) → gấp Cyrillic confusable → bỏ dấu Việt + `đ` → gấp leetspeak (`0/1/3/4/5/7/@/$`) → khớp cả dạng tách từ (`spaced`, gộp ký tự lặp ≥3) và dạng dính liền (`compact` cho từ ≥4 ký tự). Áp trên `CreateProduct`, `UpdateProduct` (SP `APPROVED` dính `REVIEW` bị hạ về `PENDING`, dính `BLOCK` bị từ chối), `CreateReview`, `AdminReviewProduct` (chặn duyệt SP thuộc danh mục cấm/từ `BLOCK`). *Test tấn công*: đăng `"m.a t.u.y"`, `"f@k3"`, `"h\u200bang g\u200bia"`, `"HÀNG   GIIIẢ"` → đều bị chặn; seller sửa SP `APPROVED` chèn từ `REVIEW` → bị kéo về `PENDING` và biến mất khỏi storefront.
- **Không rò rỉ nháp/hàng cấm ở B04 discontinued**: chỉ trả stub ngừng bán (`category_id`, `name` nếu `HIDDEN`; bỏ `name` nếu `REJECTED`/xoá mềm; không trả SKU/giá/mô tả) cho SP từng bán/bị ẩn; slug không tồn tại hoặc `PENDING` chưa từng duyệt vẫn `404`. *Test tấn công*: gọi `/products/slug/{pending-slug}` không JWT → `404`, không lộ tên/giá nháp.
- **Tiền giỏ hàng không lệch với `CartItem.unavailable`**: `price_vnd=0`, `selected=false`, không cộng `subtotal_vnd`/`total_vnd`/`total_items`; `order.resolveCheckoutInputs` (`from_cart=true`) loại bỏ item `unavailable` và trả `400 CART_NO_AVAILABLE_ITEMS` nếu không còn món hợp lệ; `items[]` gửi SKU ẩn vẫn bị `buildShopPlans` chặn `SKU_INACTIVE` (`checkout.go:1328`). *Test tấn công*: ẩn SKU đang nằm giỏ → `GET /cart` trả dòng `unavailable=true, price_vnd=0`, `total_vnd` chỉ gồm SKU còn bán; `POST /orders {from_cart:true}` khi giỏ chỉ còn SKU ẩn → `400`, không tạo đơn 0đ.
- **Không mất event / không xoá durable consumer (§10a)**: *Test đua/sự cố*: (1) huỷ `ctx` khi handler đang lỗi → `dead_letter_events` = 0, không gọi `Ack`/`Nak`; (2) giả lập cả DB + NATS DLQ lỗi ở lần thử 5 → consumer gọi `Nak` (không `Ack`), không nuốt event; (3) gọi `Stop()`/`Close()` kiểm `js.ConsumerInfo` vẫn tồn tại durable consumer trên NATS server (không bị `Drain()` xoá).

## 4. Câu phải chốt (đề xuất cho manager duyệt)

1. **Tên hiển thị người đánh giá (`buyer_name`)**: *Đề xuất*: `product` gọi `UserService.GetProfile` (chuyển tiếp JWT buyer, `USER_GRPC_ADDR=user:50051`, không vòng lặp `depends_on`, RPC đã có trong `internalRPCs` ở `pkg/common/auth/auth.go:41`), lưu snapshot `full_name` (che/rút gọn nếu là email, fallback `"Người mua đã xác thực"` khi `user` tạm lỗi). *Vì sao*: JWT chỉ chứa `sub/role/shop_id` (`pkg/common/auth/auth.go:183-188`), không sửa token hay `user` service.
2. **Đánh giá vi phạm từ cấm mức `REVIEW`**: *Đề xuất*: trên `CreateReview`, cả `BLOCK` và `REVIEW` đều trả `400 BANNED_CONTENT` yêu cầu buyer sửa (chỉ `CreateProduct`/`UpdateProduct` phân biệt `BLOCK` vs `REVIEW → PENDING`). *Vì sao*: khớp nguyên văn UC-B10 (*"Nội dung vi phạm (từ ngữ cấm): từ chối và yêu cầu sửa"*) và không phải làm thêm hàng đợi admin duyệt review ngoài phạm vi A4.
3. **Rescan SP hàng loạt (`RescanProductsForBannedContent`)**: *Đề xuất*: chạy đồng bộ theo lô trong request (tối đa 2.000 SP/lần với cursor hoặc `limit`), mỗi SP vi phạm chạy tx ngắn `FOR UPDATE` + outbox, trả ngay thống kê. *Vì sao*: danh mục hiện tại nhỏ, không cần thêm bảng job/worker async phức tạp; vượt 2.000 trả `has_more + next_cursor` để gọi tiếp.
4. **Bảng `dead_letter_events` cho `promotion` và `payment`**: *Đề xuất*: thêm migration `promotion 005_` + `payment 0009_` để đủ 2 sink (DB + `NEXUSMART_DLQ`). *Vì sao*: tuân đúng `AGENTS.md` luật 4, thống nhất 5/5 consumer, và cho phép `Ack` an toàn ngay cả khi NATS DLQ tạm lỗi nhưng DB sống.
5. **Hành vi `order` (`from_cart=true`) khi giỏ trộn SKU khả dụng + `unavailable`**: *Đề xuất*: bỏ qua các item `unavailable` (vì luôn `selected=false`), chỉ tính tiền & tạo đơn cho item khả dụng được chọn; nếu sau khi lọc còn 0 item → `400 CART_NO_AVAILABLE_ITEMS`. *Vì sao*: không chặn buyer thanh toán các món hợp lệ còn lại trong giỏ, khớp UX web chọn từng món.
6. **Ảnh đính kèm trong review (chưa có B2 upload)**: *Đề xuất*: nhận tối đa 5 URL `https://` hoặc `/products/...`, kiểm tra chặt scheme (cấm `javascript:`, `data:`, IP nội bộ/localhost khi không phải dev) giống `parseSafeEvidenceUrl` ở `OrderDetailClient.tsx:1275`. *Vì sao*: khớp `w3-plan.md:64` ("ảnh bằng URL, tải tệp chờ B2") và chống XSS/SSRF.

## 5. Chia việc theo service, thứ tự & điểm đụng file

| Gói | Service / File chính | Ước lượng |
|---|---|---|
| **T0 (§10a)** | `shipping/internal/events/consumer.go` + test; `notification/internal/consumer/consumer.go` + test; `promotion/internal/events/consumer.go` + `migrations/005_*.sql` + test; `payment/src/workers.rs` + `migrations/0009_*.sql` + test | ~650 dòng |
| **T1 (§10b)** | `proto/cart.proto` + `make proto`; `cart/internal/server/server.go` + test; `order/internal/service/checkout.go` + test; `gateway/internal/router/handlers_cart.go`; `web` (`gateway-types.ts`, `CartContext.tsx`, `gio-hang/page.tsx`, `CheckoutClient.tsx`) | ~420 dòng |
| **T2 (Proto+Common)** | `proto/product.proto` + `make proto`; `pkg/common/jetstream/stream.go`, `pkg/common/auth/auth.go`; `payment/src/workers.rs` (`CANONICAL_PRIMARY_SUBJECTS`); `docs/contracts.md`; `docker-compose.yml`, `.env.example` | ~260 dòng |
| **T3 (`product`)** | `migrations/000008_*.sql`, `queries/product.sql` + `sqlc generate`, `internal/moderation/normalize.go` (+test), `internal/store/{store,pg_store,memory_store}.go`, `internal/server/{server,reviews,moderation}.go` (+test), `cmd/product/main.go` | ~1.500 dòng |
| **T4 (`gateway`)** | `internal/router/{router,handlers_product,handlers_reviews,handlers_moderation}.go`, `internal/middleware/audit.go` (+test) | ~450 dòng |
| **T5 (`web`)** | `lib/{gateway-types,catalog,catalog-mapper}.ts`, `components/ProductCard.tsx`, `features/product/{PdpReviews,PdpCrossSell}.tsx`, `app/(shop)/san-pham/[slug]/{page,ProductDetailClient}.tsx`, `features/account/OrderDetailClient.tsx`, `app/(admin)/admin/kiem-duyet/page.tsx`, `features/admin/{AdminBannedKeywordsTab,AdminProductModerationTab}.tsx`, `features/seller/ProductFormView.tsx` | ~1.100 dòng |

- **Thứ tự bắt buộc**: `T0` trước (đúng chỉ đạo `w3-plan.md:138`) → `T1` song song `T2` → `T3` → `T4` → `T5` → e2e (`41_`, `09_`).
- **Điểm 2 luồng cùng sửa một file (cần điều phối merge)**:
  - **A4 vs B1 T1** (`w3-plan.md:87`): cùng đụng `proto/product.proto`, `services/product/queries/product.sql`, `services/product/internal/server/server.go`, `pkg/common/jetstream/stream.go`, `services/payment/src/workers.rs` → **không chạy song song** trên `product`; chốt A4 xong T2–T3 hoặc gộp proto/stream một lần.
  - **A4 vs A5**: cùng đụng `web/src/app/(shop)/san-pham/[slug]/ProductDetailClient.tsx` (A4 sửa reviews/cross-sell, A5 thay `estimateDelivery`) và `services/shipping/internal/events/consumer.go` (A4 làm T0 trước để A5 kế thừa).
  - **T0 vs T2 trong nội bộ A4**: cùng đụng `services/payment/src/workers.rs` → làm T0 trước rồi thêm `"review.>"` ở T2.

## 6. Bài nghiệm thu

### E2E backend (`tests/e2e/41_reviews_moderation_w3b_test.go`)
- **B10**: hoàn tất đơn (`COMPLETED`) → `POST /orders/{id}/reviews` (5 sao + URL ảnh) trả `201`; gọi lại lần 2 → `409`; Buyer B gửi cùng `order_item_id` → `403`; đơn `DELIVERED` → `400`; `GET /products/{id}` có `rating_avg_x100=500, review_count=1`; `GET /products/{id}/reviews` trả review vừa tạo; kiểm NATS `NEXUSMART` nhận được message subject `review.created`.
- **A15**: Admin tạo từ cấm `"hàng giả"` (`BLOCK`) và `"cam kết 100%"` (`REVIEW`) → `POST /admin/banned-keywords/preview` trả đúng `affected_count`; Seller tạo SP chứa `"h.à.n.g g.i.ả"` → `400`; Buyer review chứa `"h@ng gi4"` → `400`; Seller sửa SP `APPROVED` thêm `"cam kết 100%"` → chuyển về `PENDING`; Admin gọi `POST /admin/banned-keywords/rescan` → SP đang bán chứa từ `BLOCK` chuyển `REJECTED` + phát `product.reviewed`.
- **B04 discontinued**: Seller ẩn SP (`HIDDEN`) → `GET /products/slug/{slug}` trả `200 {discontinued: true, category_id}` không có `skus`; slug `PENDING` trả `404`.
- **§10b Cart unavailable**: Buyer thêm 2 SKU (1 ẩn sau đó) → `GET /cart` trả đủ 2 dòng, dòng ẩn `unavailable=true, price_vnd=0, selected=false`, `total_vnd` chỉ tính SKU còn bán; `POST /orders {from_cart:true}` khi chỉ còn SKU ẩn → `400`; `DELETE /cart/items/{sku_id}` xoá sạch.

### Trình duyệt thật (`tests/e2e-web/09_w3b_reviews_moderation.test.mjs`, 390/768/1440px)
1. **Đánh giá đơn hoàn tất**: đăng nhập buyer → mở `/don-hang/{id}` (`COMPLETED`) → bấm **"Viết đánh giá"** ở dòng SP → chọn 5 sao, gõ nhận xét, dán URL ảnh → bấm **"Gửi đánh giá"** → thấy badge **"Đã đánh giá"** → mở `/san-pham/{slug}` thấy điểm trung bình thật + nhận xét mới, không còn banner `"Đánh giá mẫu"`, thẻ SP ở `/san-pham` hiện sao thật.
2. **Chặn từ cấm & rescan**: đăng nhập admin → mở `/admin/kiem-duyet` → tab **"Từ khoá cấm"** → nhập từ cấm, bấm **"Xem trước ảnh hưởng"** thấy số SP khớp → bấm **"Lưu quy tắc"** → bấm **"Quét lại SP đang bán"** thấy tóm tắt số SP bị gỡ/chờ duyệt; chuyển sang seller `/seller/san-pham/them-moi` gõ từ cấm viết lách (`m.a t.u.y`) → bấm lưu thấy hộp lỗi từ chối rõ ràng.
3. **SP ngừng bán (B04)**: seller ẩn SP → trình duyệt mở `/san-pham/{slug}` → thấy thông báo **"Sản phẩm đã ngừng kinh doanh"** + lưới **Sản phẩm tương tự** bấm vào được (không phải trang 404 trắng).
4. **Giỏ hàng có SKU bị ẩn (§10b)**: buyer mở `/gio-hang` có 1 SKU còn bán + 1 SKU đã bị seller ẩn → thấy dòng SKU ẩn gắn nhãn **"Không còn bán"**, checkbox/số lượng bị khoá, tổng tiền không cộng món ẩn → bấm **"Xóa khỏi giỏ"** → dòng ẩn biến mất, có thể bấm đặt hàng món còn lại bình thường.

## 7. Rủi ro và thứ nên cắt nếu quá lớn

- **Rủi ro kỹ thuật**:
  - `product` gọi gRPC sang `order` tạo phụ thuộc hai chiều (`order` → `product` khi checkout, `product` → `order` khi review): bắt buộc dial lười + timeout 3s + circuit breaker, tuyệt đối không tạo chu trình `depends_on` ở Compose.
  - Chuẩn hoá `compact` có thể chặn nhầm từ ngắn (vd từ 2–3 ký tự ghép qua ranh giới hai từ bình thường): bắt buộc chỉ dùng `compact` cho từ ≥4 ký tự và yêu cầu `term` ≥2 ký tự, bắt buộc hiển thị **Preview affected count** trước khi lưu.
  - `payment` Rust `update_consumer` (`workers.rs:1150`) có thể từ chối đổi `max_deliver` trên JetStream consumer cũ → đã có fallback recreate consumer ở L1156, cần test kỹ không replay mất idempotency.
  - `web/src/app/(admin)/admin/kiem-duyet/page.tsx` đang trộn tab thật (`san_pham`) với 3 tab mock (`reviews`, `qa`, `media`) → tách riêng tab thật **"Từ khoá cấm"** thay vì sửa lẫn vào tab `qa` mock.
- **Thứ nên cắt nếu quá tải thời lượng 3b**:
  - Cắt **UI quản trị danh mục cấm riêng** (tái dùng checkbox `Danh mục hàng cấm` ngay trong màn hình sửa danh mục sẵn có thay vì thêm tab mới).
  - Cắt **duyệt review thủ công & phản hồi seller dưới review** (không nằm trong UC-B10, để sau).
  - Không đụng `PdpQuestions`, `PdpBackInStock`, `productEditorial.ts` (không nằm trong tiêu chí B04/A4).
