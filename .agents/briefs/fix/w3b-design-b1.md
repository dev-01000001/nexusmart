# Khảo sát thiết kế Đợt 3b — Mảng B1 (Search: Meilisearch + pgvector) & mục §10 đi kèm

Nền khảo sát: cây làm việc có thay đổi 3a chưa commit (chỉ đọc; đối chiếu `ADR-0004`, `ADR-0006`, `ADR-0008`, `docs/spec.md`, `docs/contracts.md`, `w3-plan.md`). Lưu ý mốc đánh số đã tăng sau 3a: migration kế tiếp `product` là **`000008_`**, `tests/e2e/` từ **`41_`**, `tests/e2e-web/` từ **`09_`**.

## 1. Hiện trạng
- **Đã có và chạy thật (tái dùng, không viết lại)**:
  - `product` đã có cột `search_text` không dấu (`services/product/migrations/000004_product_search_text.sql:3-16`), hàm `FoldVietnameseRune` (`đ/Đ→d`) + `NormalizeSearchText` + `EscapeLikePattern` (`services/product/internal/store/search.go:10-74`), và `ListProducts` tìm theo `p.search_text LIKE ... ESCAPE '\'` (`services/product/queries/product.sql:255,285`, `internal/store/pg_store.go:641-649`).
  - `product` đã có `outbox_events` chuẩn D9/D9-bis lease (`aggregate_id`, `seq`, `claimed_until`, `claim_token`, partial indexes: `services/product/migrations/000005_w2b_catalog_outbox_price_fee.sql:3-17`, `000007_w3a_outbox_lease_and_sku_index.sql:8-45`) và `OutboxRelay` chạy sẵn (`services/product/cmd/product/main.go:120-130`, `internal/events/outbox.go:32-128`).
  - Stream `NEXUSMART` đã bật sẵn `product.>` và `user.>` ở cả Go (`pkg/common/jetstream/stream.go:33,37`) và Rust (`services/payment/src/workers.rs:40,44`); `notification` bỏ qua an toàn subject mới ở nhánh `default:` (`services/notification/internal/service/notification.go:1405-1408`).
  - `product` đã có CRUD biến thể nhiều SKU + khoảng giá `min_price_vnd`/`max_price_vnd` (`services/product/internal/server/server.go:560,746,2328-2356`), `GetCategoryDescendants` (`:402-431`), và outbox `product.reviewed` (`:1131,1179`).
  - `tools/loadgen/generate-products.mjs:1-1075` đã sinh 1–50.000 SP (mặc định 20.000) đa mức giá `int64` VND qua `COPY` (`:352,380,409`).
  - `web/src/components/ShopHeader.tsx:214-299` đã bỏ mảng `PRODUCTS` tĩnh, gọi `GET /api/gw/products?keyword=...` (debounce `220 ms`, `:297`) ghép giá flash/shop discount thật; BFF `web/src/app/api/gw/[...path]/route.ts:21,105` tự proxy `/api/gw/search*`.
- **Còn thiếu so với từng UC**:
  - Hạ tầng & service: chưa có `proto/nexusmart/search/`, `services/search/`, `search_db`, container `meilisearch`, extension `pgvector`, hay route `/api/v1/search*` (`services/gateway/internal/router/router.go:148-161`).
  - `UC-S02`/`S07`/`A02`/`A04` (đồng bộ index): `product` chỉ ghi `product.reviewed`, chưa phát `product.upserted`/`product.deleted`/`product.category_updated` tại `UpdateProduct` (`server.go:757`), `DeleteProduct` (`:1029`), `AdminReviewProduct` (`:1083`), `UpdateStock`/`ConfirmStock`/`RestockOrderItems` (`:1409,1725,1860` khi `syncProductStockStatus` đổi `APPROVED`↔`OUT_OF_STOCK`), và `Create/Update/DeleteCategory` (`:127,223,337`). `CreateProduct` (`:607`) luôn tạo `PENDING` nên chưa phát `upserted`. `UpdateProduct` (`:690,757`) & `DeleteProduct` (`:1018,1029`) chưa gọi `GetProductByIDForUpdate` trong TX; `CreateCategory` (`:128`) chỉ gọi `LockCategoryTree` khi có `parent_id`. `loadgen --mode copy` bỏ qua outbox nên bắt buộc có `ReindexAll`.
  - `UC-B03` & `UC-B20`: `Product.ListProducts` không có fuzzy/typo, synonym server, facet count, lọc `min_rating`, xếp hạng relevance, hay fallback 2 tầng; `web/src/lib/catalog.ts:409,420` tải trần `pageSize: 100` (và `tim-kiem/page.tsx:24-32`, `danh-muc/page.tsx:24-32` không truyền `serverTotal`), `CatalogExplorer.tsx:419-511,1543-1565` tự lọc/phân trang client.
  - `UC-B12`: chưa có API gợi ý (từ khoá + danh mục + gian hàng + trending Redis), chưa có lịch sử tìm kiếm lưu server và xoá được; `ShopHeader.tsx:297` debounce 220 ms (spec 200 ms, `docs/spec.md:85`).
  - `UC-AI02`: chưa có `pgvector`, bảng `product_embeddings`, `Embedder`, `HybridSearch` RRF, phân nhánh ưu tiên từ khoá cho query ngắn/mã SKU, và fallback khi embedder lỗi.
- **Dữ liệu mẫu/tĩnh còn ở web & mục §10 "Làm cùng B1"**:
  - `TRENDING_KEYWORDS` tĩnh (`web/src/data/content.ts:337`) dùng tại `ShopHeader.tsx:303,568` và `CatalogExplorer.tsx:1489`; `SEARCH_SYNONYMS` & `SPELLING_DICTIONARY` 16 từ tĩnh ở `web/src/lib/search.ts:20-66` (`ShopHeader.tsx:314`, `CatalogExplorer.tsx:728`).
  - 10 facet thuộc tính vải/phom biên tập lấy theo slug từ `web/src/mock/productEditorial.ts:74-117` (`catalog-mapper.ts:693-729`); `ProductCard.tsx:137-138` đã chặn bịa sao mẫu (đợi A4 cấp `rating_avg`).
  - **§10-1**: `web/src/app/(shop)/HomeClient.tsx:519` hiện `Xem tất cả {products.length} sản phẩm` (~100–126) vì `HomeCatalogData` (`web/src/lib/catalog.ts:246-253,318-325`) bỏ qua `productPage.total`.
  - **§10-2**: `tools/loadgen/generate-products.mjs:79` trỏ `"/products/do-lot-boxer-modal.png"` không tồn tại (ảnh thật ở `web/src/data/products.ts:2056` là `"/products/do-lot-boxer-modal-3.png"`).
  - **§10-3**: Tra tên gian từ 50–100 gian đầu khiến gian thứ 51+/101+ rơi về `"Gian hàng NexusMart"` ở **5 chỗ**: `web/src/lib/catalog.ts:91` (`page_size=100`), `web/src/app/(shop)/thanh-toan/page.tsx:118`, `web/src/app/(shop)/thanh-toan/cho-thanh-toan/[paymentId]/page.tsx:97`, `web/src/app/(shop)/gio-hang/page.tsx:134`, và `web/src/features/account/serverData.ts:19` (dùng bởi `don-hang/page.tsx:102`, `don-hang/[id]/page.tsx:147`, `tai-khoan/page.tsx:135`).

## 2. Hợp đồng đề xuất
- **Protobuf mới — `proto/nexusmart/search/v1/search.proto`** (`nexusmart.search.v1`, `SearchService`, cổng `:50059` gRPC nội bộ / `:8089` HTTP ops):
  - `SearchProductHit`: `id(1)`, `shop_id(2)`, `shop_name(3)`, `shop_slug(4)`, `category_id(5)`, `category_slug(6)`, `category_name(7)`, `name(8)`, `slug(9)`, `thumbnail_url(10)`, `min_price_vnd(11)` (`int64`), `max_price_vnd(12)` (`int64`), `status(13)`, `rating_avg(14)` (`double`), `review_count(15)` (`int32`), `in_stock(16)` (`bool`), `sku_count(17)` (`int32`), `score(18)` (`double`), `description_snippet(19)`, `sku_ids(20)` (`repeated string`), `default_sku_id(21)`, `colors(22)` (`repeated string`), `sizes(23)` (`repeated string`). Cấm `commission_rate_bps`.
  - `SearchProducts(q(1), category_id(2), category_slug(3), shop_id(4), shop_slug(5), min_price_vnd(6), max_price_vnd(7), min_rating(8), in_stock_only(9), sort_by(10), mode(11) [AUTO|KEYWORD|HYBRID], page(12), page_size(13), size(14), color(15))` → `items(1)`, `total(2)`, `page(3)`, `page_size(4)`, `category_facets(5)`, `shop_facets(6)`, `did_you_mean(7)`, `degraded_mode(8)` (`""`|`"keyword_only"`|`"product_fallback"`), `used_vector(9)`.
  - `HybridSearch(query(1), category_id(2), min_price_vnd(3), max_price_vnd(4), limit(5), include_out_of_stock(6))` → `hits(1)`, `used_vector(2)`, `degraded_mode(3)` — cho B3 `ai-assistant` RAG (`ADR-0004:104`).
  - `Suggest(query(1), limit(2))` → `keywords(1)`, `categories(2)` (`SuggestEntity`: `id(1)`, `name(2)`, `slug(3)`), `shops(3)`, `trending(4)`, `products(5)`.
  - `ListSearchHistory(limit(1))` → `items(1)` (`id(1)`, `query(2)`, `created_at(3)`); `DeleteSearchHistory(id(1), clear_all(2))` → `ok(1)`.
  - `ReindexAll(reindex_products(1), reindex_embeddings(2), reindex_categories(3), reindex_shops(4), batch_size(5))` → `job_id(1)`, `status(2)` (`RUNNING`|`COMPLETED`|`FAILED`); `GetReindexStatus(job_id(1))` → `job_id(1)`, `status(2)`, `products_indexed(3)`, `embeddings_updated(4)`, `error_message(5)`, `started_at(6)`, `finished_at(7)`. (Chạy nền async để không chạm timeout `GRPC_CALL_TIMEOUT=10s` ở `gateway/internal/config/config.go:64` và `WriteTimeout=30s` ở `gateway/cmd/gateway/main.go:137` khi reindex 50k SP!).
- **Protobuf mở rộng tối thiểu — `proto/nexusmart/product/v1/product.proto`** (giữ nguyên `Product` max `14` `:82`, `SKU` max `18` `:54`, `ListProductsRequest` max `10` `:200`; `user.proto` giữ nguyên max `13` `:77`):
  - Thêm RPC `ListProductsForIndex(after_id(1), page_size(2))` → `items(1)` (`IndexableProduct` 1..19: `id(1)`, `shop_id(2)`, `category_id(3)`, `category_ancestor_ids(4)`, `name(5)`, `slug(6)`, `description(7)`, `thumbnail_url(8)`, `min_price_vnd(9)`, `max_price_vnd(10)`, `status(11)`, `sku_codes(12)`, `sku_attributes_text(13)`, `total_stock(14)`, `sku_count(15)`, `updated_at(16)`, `sku_ids(17)`, `colors(18)`, `sizes(19)`), `next_cursor(2)`, `has_more(3)`. Keyset `WHERE id > $1` ≤ 500/lô, tránh `OFFSET` O(N²).
- **Phân loại gRPC Auth (`pkg/common/auth/auth.go:38-174`, bắt buộc bởi `TestAllGoServiceRPCsAreClassified` ở `grpc_trust_test.go:414`)**:
  - `publicRPCs`: `SearchService/{SearchProducts,Suggest}`.
  - `internalRPCs`: `SearchService/{ListSearchHistory,DeleteSearchHistory}: false`; `SearchService/{HybridSearch,ReindexAll,GetReindexStatus}: true`; `ProductService/ListProductsForIndex: true` (handler chỉ cho service token hoặc `admin`/`super_admin`).
- **Route REST (Gateway `:8080`, `services/gateway/internal/router/router.go`)**:
  - `GET /api/v1/search` — Optional auth (`opt` group; luôn chỉ trả SP công khai, chuyển tiếp JWT để lưu lịch sử). Nếu `SearchService` lỗi `Unavailable`/`DeadlineExceeded` → gateway tự gọi `ProductService.ListProducts` (`PublicOutgoingContext`) trả cùng envelope với `degraded_mode: "product_fallback"`.
  - `GET /api/v1/search/suggest` — Public (`api` group); lỗi `SearchService` → fallback `Product.ListProducts(page_size=5)`.
  - `GET /api/v1/search/history`, `DELETE /api/v1/search/history`, `DELETE /api/v1/search/history/{id}` — Authenticated (`authed`).
  - `POST /api/v1/admin/search/reindex` (`202 Accepted`), `GET /api/v1/admin/search/reindex/{job_id}` (`200`) — `admin`, `super_admin` (`admin` group, qua `AdminAudit`).
- **Events NATS JetStream (`NEXUSMART`, tái dùng prefix `product.>` & `user.>`)**:
  - `product.upserted` (`product` outbox trong TX, `aggregate_id = product_id` → `search` durable `search-product-events`): payload giống `IndexableProduct` (mọi giá `*_vnd` là `int64`, không `commission_rate_bps`). Phát tại `AdminReviewProduct(APPROVE)`, `UpdateProduct` (trạng thái công khai), và `UpdateStock`/`ConfirmStock`/`RestockOrderItems` khi `status` hoặc `in_stock` thay đổi.
  - `product.deleted` (`product` outbox, `aggregate_id = product_id` → `search`): `{"product_id","shop_id","reason","updated_at"}`. Phát tại `DeleteProduct`, `AdminReviewProduct(REJECT)`, hoặc `UpdateProduct` chuyển sang `HIDDEN`/`PENDING`.
  - `product.category_updated` (`product` outbox sau `LockCategoryTree`, `aggregate_id = "cat:"+category_id` → `search`): `{"category_id","name","slug","parent_id","sort_order","action":"UPSERT"|"DELETE","updated_at"}`. Khi `parent_id` đổi, `search` cập nhật `search_categories` và reindex nhánh SP thuộc danh mục đó.
  - `user.shop_reviewed` (đã có `services/user/internal/events/publisher.go:45-51` → `search` durable `search-user-events`): `APPROVE` gọi `UserService.GetShop` upsert `search_shops`; `REJECT` xoá khỏi `search_shops`.
  - **Idempotency & Consumer hygiene (mẫu `order` r6, `services/order/internal/events/consumer.go:123-242`)**: `search_db.processed_events(event_id PK)` cùng TX với bảng state cục bộ + kiểm `source_updated_at` chống out-of-order; Meilisearch upsert/delete idempotent theo `id`; chỉ `Ack` sau khi ghi thành công hoặc đã lưu `dead_letter_events`/`NEXUSMART_DLQ`; lỗi shutdown không đẩy DLQ; không gọi `sub.Drain()`/`Unsubscribe()` trên durable consumer.
- **Migration & Database**:
  - `infra/postgres/init-dbs.sql`: thêm `CREATE DATABASE search_db;` (sau dòng 10).
  - `services/product/migrations/000008_product_index_cursor.sql`: `CREATE INDEX IF NOT EXISTS idx_products_public_id ON products (id ASC) WHERE deleted_at IS NULL AND status IN ('APPROVED','OUT_OF_STOCK');`.
  - `services/search/migrations/0001_init.sql` (runner có `pg_advisory_xact_lock` + `schema_migrations`): `CREATE EXTENSION IF NOT EXISTS vector;`, `processed_events`, `dead_letter_events`, `product_embeddings` (`product_id PK`, `shop_id`, `category_id`, `category_ancestor_ids TEXT[]`, `min_price_vnd BIGINT CHECK >= 0`, `max_price_vnd BIGINT CHECK >= 0`, `status`, `rating_avg REAL DEFAULT 0`, `review_count INT DEFAULT 0`, `in_stock BOOL`, `deleted BOOL DEFAULT FALSE`, `text_hash TEXT`, `embedding vector(1536)`, `source_updated_at TIMESTAMPTZ`, index HNSW `USING hnsw (embedding vector_cosine_ops) WITH (m=16, ef_construction=64)`, GIN `category_ancestor_ids`, B-tree `(status, min_price_vnd)`), `search_categories`, `search_shops`, `search_history` (`UNIQUE(user_id, query_folded)` + `(user_id, created_at DESC)`), `reindex_jobs`.
- **Kế hoạch đổi image Postgres sang `pgvector/pgvector:pg16` trên BẢN SAO volume**:
  1. `docker compose stop` để đóng sạch PGDATA; xuất `pg_dumpall > /tmp/nm-pg16-pre-b1.sql` và nhân bản raw volume `postgres_data` → `postgres_data_pgvector_test`.
  2. Chạy container thử nghiệm `pgvector/pgvector:pg16` trên `postgres_data_pgvector_test` (không đụng volume gốc): vì `postgres:16-alpine` (musl) khác Debian glibc (`pgvector/pgvector:pg16`), kiểm log collation; chạy `ALTER DATABASE ... REFRESH COLLATION VERSION` + `REINDEX DATABASE` cho 7 DB — nếu collation/binary báo lỗi thì nạp sạch `/tmp/nm-pg16-pre-b1.sql` vào volume mới của `pgvector/pgvector:pg16`.
  3. Tạo tay `search_db` (volume cũ không chạy lại `init-dbs.sql`), chạy `CREATE EXTENSION IF NOT EXISTS vector;` xác nhận `v0.8.7`, đối chiếu số dòng 7 DB gốc khớp 100%.
  4. Rollback: giữ nguyên volume `postgres_data` gốc tới sau nghiệm thu B1; lỗi thì đổi `docker-compose.yml:4` lại `postgres:16-alpine` + gắn lại volume gốc. Cập nhật `scripts/test-db.sh:8` sang `pgvector/pgvector:pg16`.
- **RAM dự kiến (máy 14,6 GB)**: nền 15 container ~1,25 GB (`w3-plan.md:39`) + `meilisearch v1.58.0` (~200–400 MB ở 20k–50k SP, ghim `--max-indexing-memory 256MB` và `mem_limit: 512m`) + `services/search` (~30–50 MB) + `pgvector` HNSW 50k×1536 (~250–400 MB working set) = **~1,8–2,1 GB / 14,6 GB** (~14% RAM). Đo lại bằng `docker stats --no-stream` sau loadgen 20k/50k SP.
- **Embedding mode `mock` tất định (`EMBEDDING_MODE=mock`, mặc định dev/CI/e2e)**:
  - Dùng `NormalizeSearchText` (`đ→d`, bỏ dấu) → tách unigram/bigram + ánh xạ nhóm đồng nghĩa từ `synonyms.json` (vd `chay bo`/`dryfit`/`the thao` → token gốc `syn:the_thao`) + char 3-gram → signed feature hashing FNV-1a vào `1536` chiều → chuẩn hoá L2.
  - 100% tất định, 0 mạng/0 API key, tạo cosine similarity thực sự cao giữa mô tả tiếng Việt cùng ý định để test RRF trên `pgvector` HNSW thật; `EMBEDDING_MODE=openai` (`text-embedding-3-small`, `gobreaker/v2`, timeout 800 ms) chỉ bật khi có key + chủ dự án duyệt.
- **Biến env & ops**: `.env.example` / `docker-compose.yml` / `docs/contracts.md` §7 thêm `MEILISEARCH_URL`, `MEILI_MASTER_KEY`, `MEILI_HTTP_PORT=7700` (127.0.0.1), `MEILI_NO_ANALYTICS=true`, `SEARCH_HTTP_PORT=8089`, `SEARCH_GRPC_ADDR=search:50059`, `EMBEDDING_MODE=mock`, `EMBEDDING_MODEL=text-embedding-3-small`, `EMBEDDING_DIM=1536`, `EMBEDDING_API_KEY=`, `SEARCH_RRF_K=60`, `SEARCH_MEILI_TIMEOUT_MS=1500`, `SEARCH_VECTOR_TIMEOUT_MS=800`. `/readyz` của `search` kiểm Postgres/Redis/NATS (hard deps) và **không** trả 503 khi chỉ tắt Meilisearch để nhánh fallback `Product.ListProducts` luôn sống trên K8s.
- **Thư viện/hạ tầng mới (đã duyệt tại ADR-0004)**: `getmeili/meilisearch:v1.58.0` (MIT), `pgvector/pgvector:pg16` (`v0.8.7`), `github.com/meilisearch/meilisearch-go` (MIT), `github.com/pgvector/pgvector-go v0.4.1` (MIT). Tái dùng `gobreaker/v2`, `go-redis/v9`, `pgx/v5`, `nats.go`.

## 3. Bất biến tiền / quyền / đua & cách kiểm
- **Tiền**: `min_price_vnd`/`max_price_vnd` bắt buộc `int64`/`BIGINT >= 0`, cấm float; `Search` không lưu giá khuyến mãi hết hạn và không bao giờ làm nguồn tính giá checkout/preview. Test: kiểm schema + unit test giá > 2³¹ VND.
- **Không rò SP ẩn & hoa hồng (D1)**: SP `PENDING`/`REJECTED`/`HIDDEN`/deleted không xuất hiện trong `SearchProducts`/`HybridSearch`/`Suggest`; `commission_rate_bps` không có trong event/Meili/response. Test tấn công: tìm SP `PENDING`/bị ẩn kèm JWT admin hoặc spoof `x-user-role: admin` → 0 kết quả, JSON không có `commission_rate_bps`.
- **Chống Meilisearch filter injection**: escape/whitelist chặt `shop_id`/`category_id`/`size`/`color` trước khi ghép filter expression. Test tấn công: `shop_id="x" OR status="HIDDEN"` → `400` hoặc 0 kết quả.
- **Quyền lịch sử & Reindex**: `List/DeleteSearchHistory` chỉ dùng `claims.UserID()` (IDOR test: buyer A xoá `id` của buyer B → 0 dòng xoá); `POST /api/v1/admin/search/reindex` & `ListProductsForIndex` chặn buyer/seller (`403`) và anonymous (`401`).
- **Đua thứ tự outbox, deadlock khóa & Reindex vs Live Event**:
  - `UpdateProduct`/`DeleteProduct` khóa `GetProductByIDForUpdate` trước `InsertOutboxEvent` (D9-bis `docs/contracts.md:427-431`).
  - Thứ tự khóa nhất quán `products` → `skus` (hoặc lấy `product_id` theo thứ tự tăng dần trước khi khóa) ở `UpdateStock`/`ConfirmStock`/`RestockOrderItems` để tránh deadlock với `UpdateProduct`.
  - Consumer `search` lưu `deleted = TRUE` + `source_updated_at` trong `product_embeddings` và bỏ qua mọi upsert/reindex có `updated_at < source_updated_at` để `ReindexAll` song song không phục hồi SP vừa xoá. Test đua: `go test -race -count=5` chạy `UpdateProduct` + `DeleteProduct` song song và `ReindexAll` xen kẽ `product.deleted`.

## 4. Câu phải chốt
1. **Subject danh mục (`product.category_updated` vs `category.updated`)**:
   - *Đề xuất*: **`product.category_updated`** — dùng sẵn prefix `product.>` trên `NEXUSMART` (`pkg/common/jetstream/stream.go:37`, `services/payment/src/workers.rs:44`), không phải đụng Rust `payment` hay stream config.
2. **Đủ dữ liệu card trên đường đọc tìm kiếm mà không gọi N+1 `ProductService`**:
   - *Đề xuất*: `SearchProductHit` mang sẵn `sku_ids`, `default_sku_id`, `colors`, `sizes` (rút gọn); web chỉ gọi thêm `/shop-discounts/active?sku_ids=...` + `/flash-sales` (giống `ShopHeader.tsx:249-266`), giữ 0 lời gọi bổ sung tới `ProductService` và p95 < 200 ms.
3. **10 facet thuộc tính biên tập trên `CatalogExplorer` khi vượt 100 SP**:
   - *Đề xuất*: Đẩy 5 facet thật (`category`, `shop`, `price`, `rating`, `in_stock`) + `size`/`color` từ `skus.attributes` lên Meilisearch; ẩn số đếm giả của 10 facet biên tập (`productEditorial.ts`) khi `total > page_size` để không bao giờ đếm sai trên 20k–50k SP.
4. **Ngưỡng rẽ nhánh Keyword vs Hybrid RRF (`UC-AI02`)**:
   - *Đề xuất*: `mode=AUTO` chạy RRF (`k=60`, `w_kw=0.6`, `w_vec=0.4`) khi truy vấn sau chuẩn hoá ≥ 3 từ và không khớp regex mã SKU (`^[a-z0-9]+-[a-z0-9-]+$`); query ≤ 2 từ hoặc mã SKU ưu tiên Meilisearch; `mode=HYBRID` ép chạy RRF (B3 RAG & e2e).
5. **Điều phối `services/product` giữa B1 và A4**:
   - *Đề xuất*: Chạy **B1 T1 xong trước khi mở A4** trên `product` (`w3-plan.md:87`): B1 dùng `000008_product_index_cursor.sql` và không thêm trường vào message `Product`; `search` chừa sẵn `rating_avg`/`review_count` mặc định `0` để nối `review.created` sau A4.
6. **Tra tên gian §10-3 (`GET /shops/{id}` vs RPC batch mới)**:
   - *Đề xuất*: Đúng `w3-plan.md:141` — helper `web/src/lib/shop-lookup.ts` gọi `GET /shops/{id}` song song (dedup, concurrency ≤ 8) cho các `shop_id` thực sự hiển thị; không phải đổi `user.proto`/`services/user`.

## 5. Chia việc theo service, thứ tự & điểm xung đột
- **Thứ tự**: **(0) Thử đổi `pgvector/pgvector:pg16` trên bản sao volume** → **(1) Proto + `pkg/common/auth` + `docs/contracts.md`** (tuần tự) → **(2) `services/product` (T1)** → **(3) `services/search` (T2–T4) + Compose/OTel/CI** → **(4) `services/gateway` (T5a)** → **(5) `web/` + `tools/loadgen` (T5b + §10)** → **(6) `tests/e2e`, `tests/e2e-web`, `tests/k6`**.
- **Phân rã & ước lượng**:
  - `proto/` & `pkg/common` (~320 LOC): `proto/nexusmart/search/v1/search.proto`, thêm `ListProductsForIndex` vào `product.proto`, cập nhật `pkg/common/auth/{auth.go,grpc_trust_test.go}`, tách `pkg/common/textnorm/` dùng chung `FoldVietnameseRune`/`NormalizeSearchText`.
  - `services/product` (~320 LOC): `migrations/000008_product_index_cursor.sql`, `queries/product.sql`, `internal/events/types.go`, `internal/store/{store,pg_store,memory_store}.go`, `internal/server/server.go` + test PG/unit.
  - `services/search` (mới, ~1.900–2.200 LOC): `go.mod`, `Dockerfile`, `sqlc.yaml`, `migrations/{0001_init.sql,embed.go}`, `queries/search.sql`, `config/synonyms.json`, `internal/{config,db,embedder,meili,indexer,events,service,grpcserver}`, `cmd/search/main.go` + unit/race/PG test.
  - `services/gateway` (~380 LOC): `internal/config/config.go`, `internal/clients/clients.go`, `internal/router/{router.go,handlers_search.go,b1_search_routes_test.go}` (gồm fallback `Product.ListProducts`).
  - `infra/` & build (~120 LOC): `infra/postgres/init-dbs.sql`, `infra/otel-collector-config.yaml`, `docker-compose.yml`, `.env.example`, `Makefile`, `.github/workflows/ci.yml`, `scripts/test-db.sh`, `docs/contracts.md`.
  - `web/` & `tools/loadgen` (~650 LOC): sửa `tools/loadgen/generate-products.mjs:79`; thêm `web/src/lib/{search-client.ts,shop-lookup.ts}`; sửa `web/src/lib/{gateway-types.ts,catalog.ts}`, `web/src/components/ShopHeader.tsx`, `web/src/features/{catalog/CatalogExplorer.tsx,account/serverData.ts}`, `web/src/app/(shop)/{page.tsx,HomeClient.tsx,danh-muc/page.tsx,tim-kiem/page.tsx,gio-hang/page.tsx,thanh-toan/page.tsx,thanh-toan/cho-thanh-toan/[paymentId]/page.tsx}`.
  - Kiểm thử (~600 LOC): `tests/e2e/41_search_b1_test.go`, `tests/e2e-web/09_search_b1.test.mjs`, `tests/k6/search.js`.
- **Điểm 2 mảng cùng sửa 1 file**:
  - `services/product/` (`product.proto`, `server.go`, `queries/product.sql`, `store/*.go`, `migrations/000008_*`) — xung đột với **A4** (chạy B1 T1 trước A4).
  - `pkg/common/auth/{auth.go,grpc_trust_test.go}`, `services/gateway/internal/{config/config.go,clients/clients.go,router/router.go}`, `docs/contracts.md`, `docker-compose.yml`, `.env.example`, `Makefile`, `.github/workflows/ci.yml` — chạm chung A4–A8 (merge proto/wiring tuần tự).
  - `web/src/app/(shop)/{thanh-toan/page.tsx,gio-hang/page.tsx}`, `web/src/lib/{catalog.ts,gateway-types.ts}` — chạm nhẹ A5/A6/A8 (tách `shop-lookup.ts`/`search-client.ts` để giảm diff).

## 6. Bài nghiệm thu
- **E2E tự động (`make e2e` — `41_search_b1_test.go`, `make e2e-web` — `09_search_b1.test.mjs`)**:
  - Đồng bộ < 5s: tạo SP biến thể 2 mức giá → chưa duyệt không hiện trên `/api/v1/search`; admin `APPROVE` → hiện < 5s kèm `min_price_vnd..max_price_vnd`; sửa tên/giá → phản ánh < 5s; ẩn (`HIDDEN`) hoặc `DELETE`/`REJECT` → biến mất < 5s.
  - Tiếng Việt & đồng nghĩa: `do lot`, `ao phong`, `ao thn` (gõ sai), `đ` vs `d` trả đúng SP; admin sửa danh mục → bộ lọc cập nhật.
  - Hybrid RRF (`UC-AI02`): query mô tả ≥ 3 từ (`mode=auto`/`hybrid`) có `used_vector=true`, SP gần nghĩa đứng top; query mã SKU ưu tiên từ khoá; lỗi Embedder → `200` với `degraded_mode="keyword_only"`.
  - Fallback Meilisearch: dừng container `meilisearch` → `GET /api/v1/search?q=ao+thun` vẫn trả `200`, `degraded_mode="product_fallback"`; bật lại phục hồi bình thường.
  - Gợi ý & lịch sử (`UC-B12`): `/api/v1/search/suggest?q=ao` phản hồi **< 80 ms**, đủ `keywords`/`categories`/`shops`/`trending`; user đăng nhập tìm kiếm → `/api/v1/search/history` hiện lịch sử → xoá 1 mục và xoá hết; user khác bị chặn đọc/xoá.
  - Quy mô & tải (`k6`): nạp 20.000 SP (`node tools/loadgen/generate-products.mjs --count 20000`), gọi `POST /api/v1/admin/search/reindex` và chờ `COMPLETED`, chạy `tests/k6/search.js` đạt **p95 < 200 ms**, error rate < 0,5%; ghi `docker stats` RAM.
- **Thao tác trình duyệt thật (Playwright / tay trên `http://localhost:3000`, 1440px & 390px, với ≥ 20.000 SP + > 100 gian)**:
  1. Mở `/` → thấy `"Xem tất cả 20.xxx sản phẩm"` (tổng thật, không phải `100`/`126`); ảnh `do-lot` load `200` (không 404).
  2. Bấm ô tìm kiếm trên header, gõ `"ao th"` chờ 200 ms → thấy từ khoá gợi ý, danh mục, gian hàng, SP khớp, lịch sử tìm kiếm (khi đăng nhập) + nút xoá từng mục; bấm xoá → biến mất và reload vẫn mất.
  3. Vào `/danh-muc` & `/tim-kiem?q=ao+thun` → thấy tổng số thật (> 100), bấm `"Xem thêm"`/phân trang tải trang 2–3 từ server; lọc danh mục + giá + sắp xếp giá tăng dần → thứ tự khớp; tìm `"khongcotu999"` → hiện trạng thái trống + từ khoá phổ biến.
  4. Tạo giỏ/đơn với SP thuộc gian hàng thứ 101+ → mở `/gio-hang`, `/thanh-toan`, `/don-hang`, `/thanh-toan/cho-thanh-toan/{id}` → hiện đúng tên gian thật, không hiện `"Gian hàng NexusMart"`.
  5. Dừng container `meilisearch`, reload `/tim-kiem?q=ao+thun` → vẫn hiện sản phẩm thật (không trắng trang, không 500).

## 7. Rủi ro và thứ nên cắt nếu quá lớn
- **Rủi ro chính**:
  - *Collation mismatch `postgres:16-alpine` (musl) → `pgvector/pgvector:pg16` (Debian glibc)*: phải thử trên bản sao volume và sẵn đường `pg_dumpall`/restore sạch thay vì gắn thẳng PGDATA.
  - *RAM spike khi reindex 50.000 SP trên máy 14,6 GB*: ghim `--max-indexing-memory 256MB` + `mem_limit: 512m` cho Meilisearch, reindex lô 500 SP, bỏ qua tính lại embedding nếu `text_hash` không đổi.
  - *Timeout reindex & rate limit khi sau này bật OpenAI thật*: thiết kế `ReindexAll` chạy nền async có `GetReindexStatus` ngay từ đầu; batch embedding 100–500 SP/lần.
  - *10 facet biên tập tĩnh ở `CatalogExplorer`*: đưa hết lên backend sẽ phình schema `product` ngoài phạm vi.
- **Thứ nên cắt trước nếu quá tải**:
  1. Chỉ đẩy 5 facet thật (`category`, `shop`, `price`, `rating`, `in_stock`) + `size`/`color` từ `skus.attributes` lên Meilisearch; không đụng 10 facet biên tập tĩnh.
  2. Chạy `k6` bắt buộc ở mốc **20.000 SP** trước (đạt tiêu chí `w3-plan.md:76`), mốc 50.000 SP để ở bước đo báo cáo 3e.
  3. Quản lý đồng nghĩa qua `synonyms.json` trong repo + API reindex admin, không làm UI quản trị đồng nghĩa (`ADR-0004:107`).
