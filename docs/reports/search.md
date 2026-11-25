# Báo cáo Swarm A – Search Service

Ngày hoàn thành: 2026-11-21
Phạm vi: `.agents/briefs/m-a-search.md`, `.agents/briefs/m-all-contract.md`, spec hàng **"Tìm kiếm"**.

---

## 1. Đã hoàn thành

### 1.1 Protobuf & gRPC contract (`proto/nexusmart/search/`, `gen/go/nexusmart/search/`)
- Định nghĩa `proto/nexusmart/search/v1/search.proto` với `SearchService`:
  - `Search(SearchRequest) -> SearchResponse`: hỗ trợ từ khoá có dấu/không dấu, typo tolerance, lọc (`shop_id`, `category` theo slug hoặc id, `min_price_vnd`, `max_price_vnd`, `in_stock_only`), sắp xếp (`relevance`, `price_asc`, `price_desc`, `newest`), phân trang, facet (`categories`, `shops`, `price_ranges`) và chế độ `hybrid` / `keyword` / `vector`.
  - `Suggest(SuggestRequest) -> SuggestResponse`: gợi ý danh mục + sản phẩm không trùng lặp cho có dấu/không dấu và sai chính tả.
  - `SimilarProducts(SimilarProductsRequest) -> SimilarProductsResponse`: truy hồi ngữ nghĩa trên pgvector theo `product_id` hoặc `query` tự nhiên (có lọc `shop_id`, `category`, giá, tồn kho) phục vụ AI assistant RAG.
- Sinh mã Go vào `gen/go/nexusmart/search/v1/{search.pb.go,search_grpc.pb.go}` và đạt `buf lint`.

### 1.2 Product service transactional outbox (`services/product/`)
- Migration `services/product/migrations/000003_outbox_events.sql`: thêm sequence `product_event_version_seq` (version đơn điệu không khoá hàng) và bảng `outbox_events` kèm partial index `idx_product_outbox_unpublished`.
- Cập nhật `queries/product.sql` + sinh lại `internal/db/` (`sqlc generate`) và triển khai `NextProductEventVersion`, `InsertOutboxEvent`, `ProcessPendingOutbox` (`FOR UPDATE SKIP LOCKED`) trong `PgStore` và `MemoryStore`.
- Phát event trong **cùng transaction `WithTx`** kèm OTel `trace_id` và `traceparent` với payload chuẩn `{product_id, shop_id, version, occurred_at}`:
  - `product.created` (`CreateProduct`)
  - `product.updated` (`UpdateProduct`, `AdminReviewProduct`)
  - `product.deleted` (`DeleteProduct`)
  - `product.stock_changed` (`UpdateStock`, `ReserveStock`, `ReleaseStock`, `ConfirmStock`, `ReserveFlashSaleStock`, `releaseFlashSaleStock`)
- `services/product/internal/events/`: `EnsureStreams` tự tạo/cập nhật stream `NEXUSMART` bổ sung subject `product.>` và đảm bảo `NEXUSMART_DLQ`; `OutboxRelay` poll `100ms` kết hợp kênh đánh thức tức thì (`SetOutboxNotifier`), gắn header `Nats-Msg-Id` và `traceparent`.
- Gắn vào `services/product/cmd/product/main.go` khi `NATS_URL` được cấu hình.

### 1.3 Search service (`services/search/`)
- **Chuẩn hoá tiếng Việt (`internal/normalize/`)**: `NormalizeVietnamese` gập `đ/Đ -> d`, tách Unicode NFD và bỏ toàn bộ dấu thanh/mũ, bảo đảm truy vấn có dấu và không dấu cho kết quả và điểm giống hệt nhau; kèm `IsSKUOrShortQuery` ưu tiên keyword cho mã SKU/truy vấn rất ngắn (UC-AI02).
- **Resilience (`internal/resilience/`)**: bọc mọi lời gọi ra ngoài (Meilisearch HTTP, Voyage API, Product gRPC) bằng timeout từng lần thử, retry backoff có jitter và circuit breaker (`gobreaker/v2`).
- **Embedding (`internal/embed/`)**: interface `Embedder` với `EMBED_MODE=mock|voyage` (mặc định `mock`: vector băm xác định 256 chiều chuẩn hoá L2 trên unigram/bigram/3-gram ký tự tiếng Việt đã chuẩn hoá; `voyage`: HTTP stdlib tới Voyage API, không chứa key thật).
- **Meilisearch client (`internal/meili/`)**: client HTTP stdlib gọn nhẹ (không kéo thêm SDK bên thứ ba), `EnsureIndex` cấu hình `searchableAttributes`, `filterableAttributes`, `sortableAttributes`, `faceting`, `pagination.maxTotalHits=100000`, `typoTolerance`, escape filter chống filter injection.
- **pgvector store (`migrations/000001_init.sql`, `internal/store/`)**: `CREATE EXTENSION IF NOT EXISTS vector`, bảng `product_documents` với cột `embedding vector(256)` + index HNSW (`vector_cosine_ops`, `m=16, ef_construction=64`), bảo vệ version khi upsert, batch upsert bằng `pgx.Batch`, tự phát hiện `hnsw.iterative_scan` trên pgvector 0.8+, cùng `processed_events` và `dead_letter_events`.
- **Hybrid Reciprocal Rank Fusion (`internal/rrf/`, `internal/server/`)**: chạy song song Meilisearch và pgvector rồi gộp hạng bằng RRF (`k=60`, trọng số keyword `1.0`, vector `0.8`); tự động degrade về backend còn lại nếu một backend lỗi.
- **Consumer & Indexer (`internal/consumer/`, `internal/indexer/`)**: consumer bền vững JetStream (`search-product-indexer-v1` / `search-workers`, subject `product.>`), xử lý idempotent theo `processed_events`, bảo vệ event cũ theo `version`, retry `NakWithDelay`, đẩy message lỗi vào `NEXUSMART_DLQ` (`dlq.<subject>`) + bảng `dead_letter_events`, kèm cache danh mục có TTL và tùy chọn đồng bộ khởi động `SEARCH_STARTUP_SYNC=true` qua `ProductService.ListProducts`.
- **Vận hành & đóng gói (`cmd/search/main.go`, `Dockerfile`)**: gRPC `:50059`, HTTP ops `:8089` (`/healthz`, `/readyz`, `/metrics`), log JSON kèm OTel trace ID, RED metrics; Docker multi-stage chạy bằng user không phải root `appuser` (uid `10001`).

### 1.4 Gateway REST & `tools/catalog-gen/` & tệp cấu hình dùng chung
- `services/gateway/`: thêm `handlers_search.go`, `router_search_test.go`, bổ sung cấu hình `SEARCH_GRPC_ADDR` (`config.go`, `clients.go`) và 1 dòng mount `h.mountSearchRoutes(opt)` trong `router.go`:
  - `GET /api/v1/search?q=&shop_id=&category=&min_price=&max_price=&sort=&page=&page_size=` → `{items, total, page, page_size, facets}` (có fallback sang `ProductService.ListProducts` khi Search `Unavailable`/`DeadlineExceeded` theo UC-B03).
  - `GET /api/v1/search/suggest?q=&shop_id=&limit=` → `{suggestions}`.
- `tools/catalog-gen/`: CLI Go sinh xác định 20.000 sản phẩm tiếng Việt phân bố đều cho 4 gian hàng seed & danh mục, nạp **hoàn toàn qua Product API** (`--mode=rest` qua Gateway có xử lý `429 Retry-After` và `--mode=grpc` qua `ProductService` với JWT RBAC, không ghi thẳng DB).
- Bổ sung cộng thêm (không sửa phần của swarm khác) vào `docker-compose.yml` (`search-postgres` dùng `pgvector/pgvector:pg16`, `meilisearch` dùng `getmeili/meilisearch:v1.12`, `search` cổng `127.0.0.1:8089` / gRPC `50059`), `Makefile` (`GO_MODULES` gồm `services/search` và `tools/catalog-gen`), `.env.example`.

---

## 2. Lệnh kiểm tra và đầu ra thật

### 2.1 Validate compose, proto, format, lint & unit test (`-race`)
Lệnh đã chạy:
```bash
docker compose config -q
buf lint --path proto/nexusmart/search
for mod in services/product services/search services/gateway tools/catalog-gen; do
  gofmt -l "$mod"
  (cd "$mod" && golangci-lint run --allow-parallel-runners -c "$(pwd)/../../.golangci.yml" ./...)
  (cd "$mod" && go test -race ./...)
done
```
Đầu ra thật:
- `docker compose config -q`: **VALID**
- `buf lint --path proto/nexusmart/search`: **OK** (0 lỗi)
- `gofmt -l`: trống trên cả 4 module
- `golangci-lint`: 0 cảnh báo trên cả 4 module
- `go test -race ./...`:
  - `services/product`: `ok internal/events (1.024s)`, `ok internal/server (1.333s)`
  - `services/search`: `ok internal/consumer`, `embed`, `indexer`, `integration`, `meili`, `normalize`, `resilience`, `rrf`, `server`, `store` (100% PASS)
  - `services/gateway`: `ok internal/httpx`, `middleware`, `proxy`, `router` (100% PASS, gồm 4 test mới `TestGatewaySearch*`)
  - `tools/catalog-gen`: `ok internal/generator (2.003s)`, `ok internal/loader (1.039s)`
  - `catalog-gen --dry-run=true --count=20000`: 20.000 sản phẩm, 40.421 SKU, 1.007 sản phẩm hết hàng, 5.000 SP/shop, 3.333–3.334 SP/danh mục trong `60 ms`.

### 2.2 Docker multi-stage build (`services/search/Dockerfile`)
Lệnh đã chạy:
```bash
docker build --progress=plain -t search-test-image -f services/search/Dockerfile .
docker inspect --format 'Image=search-test-image User={{.Config.User}} ExposedPorts={{.Config.ExposedPorts}} SizeBytes={{.Size}}' search-test-image
docker rmi -f search-test-image
```
Đầu ra thật:
```text
Image=search-test-image User=appuser ExposedPorts=map[50059/tcp:{} 8089/tcp:{}] SizeBytes=29984469
```
(Image non-root `appuser`, ~28.6 MB; đã xoá sau khi kiểm tra.)

### 2.3 Integration test với Meilisearch + pgvector Postgres + NATS JetStream tạm (`search-test-*`)
Lệnh đã chạy:
```bash
cd services/search && \
SEARCH_TEST_PG_URL="postgres://nexusmart:nexusmart_test_pw@127.0.0.1:15433/search_test_db?sslmode=disable" \
SEARCH_TEST_MEILI_URL="http://127.0.0.1:17700" \
SEARCH_TEST_MEILI_KEY="test-meili-master-key" \
SEARCH_TEST_NATS_URL="nats://127.0.0.1:14222" \
SEARCH_BENCH_20K="1" \
go test -v -count=1 -timeout 5m ./internal/integration/...
```
Đầu ra thật:
```text
=== RUN   TestSearchIntegrationLive
--- PASS: TestSearchIntegrationLive (25.35s)
=== RUN   TestSearchBenchmark20K
    search_integration_test.go:386: indexed 20000 products in 1m31.914858923s (217.6 docs/sec)
    search_integration_test.go:430: 20k search latency over 100 queries: p50=3.895034ms p95=18.077381ms p99=31.665159ms max=31.665159ms
--- PASS: TestSearchBenchmark20K (99.41s)
PASS
ok  	github.com/nexusmart/core/services/search/internal/integration	124.763s
```

### 2.4 Kiểm chứng live end-to-end (binary thật `product` + `search` + `catalog-gen` trên 20.000 sản phẩm)
Chạy đồng thời binary `services/product` (`product_test_db` + `search-test-nats`) và binary `services/search` (`search_test_db` pgvector + `search-test-meili` + `search-test-nats`):

1. **Độ trễ đồng bộ sự kiện Product Outbox → NATS JetStream → Search (Meilisearch + pgvector)**:
   ```text
   REALTIME_APPROVE_SYNC_LATENCY count=10 min=322.421294ms p50=408.000324ms p95=3.899440644s max=3.899440644s
   VIETNAMESE_DIACRITIC_PARITY: PASS (top=37fa6aeb-ce63-4a70-9df4-912419322559 total_acc=10 total_plain=10)
   REALTIME_UPDATE_SYNC_LATENCY: 295.882316ms (PASS)
   REALTIME_STOCK_SYNC_LATENCY: 322.047025ms (PASS)
   REALTIME_DELETE_SYNC_LATENCY: 354.471266ms (PASS)
   ```
   (Sản phẩm trạng thái `PENDING` được xác nhận không lộ trên kết quả tìm kiếm công khai.)

2. **Nạp 20.000 sản phẩm tiếng Việt qua `tools/catalog-gen` → `ProductService` gRPC API (không ghi DB)**:
   ```json
   {
     "mode": "grpc",
     "dry_run": false,
     "seed": 20261121,
     "generated": 20000,
     "created": 20000,
     "approved": 20000,
     "failed": 0,
     "elapsed_ms": 34274,
     "rate_per_sec": 583.52,
     "shops": 4,
     "categories": 6
   }
   ```

3. **Benchmark trực tiếp qua gRPC trên 20.000 sản phẩm đã đồng bộ vào `services/search`**:
   ```text
   INDEX_READY total_docs=20000 wait=2m9.372410944s
   BENCH20K_ACCENTED_UNACCENTED_EXACT_MATCH: PASS (top10 identical, total=6672)
   BENCH20K_GRPC_SEARCH runs=200 p50=7.255802ms p95=17.259455ms p99=68.516657ms max=162.459713ms
   BENCH20K_GRPC_SUGGEST runs=50 p50=2.609626ms p95=4.890169ms max=86.588071ms
   BENCH20K_GRPC_SIMILAR runs=50 p50=1.109594ms p95=1.644916ms max=64.233787ms
   ```

*(Toàn bộ container tạm `search-test-pgvector`, `search-test-meili`, `search-test-nats`, binary tạm và `search-test-image` đã được xoá sạch sau khi chạy.)*

---

## 3. Đối chiếu tiêu chí đo trong đặc tả

| Tiêu chí (Spec "Tìm kiếm" & `m-a-search.md`) | Kết quả đo thật | Trạng thái |
|---|---|---|
| Tìm từ khoá có dấu và không dấu cho cùng kết quả (kể cả `đ/Đ`) | Top-10 giống hệt nhau trên 20.000 SP (`total=6672`) | **Đạt** |
| Typo tolerance, lọc (`shop_id`, `category`, giá, tồn kho), sắp xếp, facet, gợi ý (`Suggest`) | Verified qua unit test + integration live + benchmark 20k | **Đạt** |
| Hybrid search (Meilisearch + pgvector 256-dim, RRF) + `SimilarProducts` cho AI assistant | Verified qua unit test + live integration + `SimilarProducts` p95 `1.64 ms` | **Đạt** |
| Product phát `product.*` qua outbox; Search consume & cập nhật index `< 5 giây` | Approve: p50 `408 ms`, max `3.90 s`; Update `296 ms`; Stock `322 ms`; Delete `354 ms` | **Đạt** |
| `tools/catalog-gen` sinh 20.000 SP tiếng Việt, nạp qua API Product (không ghi thẳng DB) | 20.000/20.000 tạo & duyệt thành công (`0` lỗi, `583.5 SP/s`) | **Đạt** |
| Benchmark `p95` truy vấn `< 200 ms` trên 20.000 sản phẩm | `TestSearchBenchmark20K`: p95 `18.08 ms`; live gRPC 200 queries: p95 `17.26 ms` | **Đạt** |
| Lint sạch, unit test `-race` xanh, Docker image multi-stage non-root | `gofmt`, `golangci-lint`, `go test -race`, `User=appuser` (~28.6 MB) | **Đạt** |

---

## 4. Phần chưa làm / phạm vi giao cho swarm khác
- **Không rebuild stack compose đang chạy và không chạy `make e2e` / `make seed`**: theo quy tắc hợp đồng chung (`m-all-contract.md`), việc dựng lại stack và chạy E2E toàn cục thuộc quyền sở hữu của Swarm H (qa-docs).
- **Chưa kiểm thử live với Voyage API thật (`EMBED_MODE=voyage`)**: không có API key thật trong môi trường và không commit key vào repo; adapter `VoyageEmbedder` đã được kiểm thử unit qua HTTP mock (`httptest.Server`) bao gồm retry và lỗi 4xx/5xx.
- **Lưu ý vận hành khi nạp bulk 20.000 SP qua REST Gateway**: gateway mặc định giới hạn `RATE_LIMIT_IP_PER_MIN=300`, `RATE_LIMIT_USER_PER_MIN=180`; `tools/catalog-gen --mode=rest` tự tôn trọng `Retry-After`, còn nạp hàng loạt 20.000 sản phẩm trong môi trường staging/benchmark nên dùng `--mode=grpc` hoặc nới rate limit cục bộ.
