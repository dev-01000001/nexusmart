# ADR-0004: Search service và tìm kiếm hybrid (từ khoá + vector)

- **Trạng thái**: Chấp nhận
- **Ngày**: 2026-11-23 (chấp nhận 2026-11-24 theo uỷ quyền)

## Bối cảnh

- **Hiện trạng trong repo** (`docs/audit/cat.md` `CAT-B1`, `CAT-B12`, `CAT-D2`; `docs/audit/plt.md` mục 0):
  - Chưa có `services/search` và `proto/nexusmart/search/`.
  - `services/product/queries/product.sql:121,142` tìm kiếm bằng `ILIKE '%' || keyword || '%'` nên không khớp tiếng Việt không dấu (`?keyword=ao%20thun` trả `0` kết quả trong khi `Áo thun` trả `5`), đồng thời lộ wildcard `%` (`?keyword=%25` trả toàn bộ catalog).
  - `web/src/lib/catalog.ts:341-360` và `CatalogExplorer.tsx:409-433` kéo trần `100` sản phẩm rồi tự bỏ dấu/lọc 13 facet ở client; `ShopHeader.tsx:173` đọc gợi ý từ mảng mock `PRODUCTS` tĩnh; `tools/seed/seed.mjs` mới có 18 sản phẩm (chưa có script sinh 20.000–50.000 sản phẩm).
  - `docker-compose.yml:116-117` đã truyền `NATS_URL`/`NATS_STREAM` cho `product`, nhưng `services/product` chưa kết nối NATS và stream `NEXUSMART` chưa có subject `product.>` (`CAT-B3`).
- **Use case & tiêu chí đo trích từ `docs/spec.md`**:
  - `UC-B03` (Cao): tìm theo từ khoá có dấu và không dấu; lọc danh mục, giá, đánh giá; sắp xếp; phân trang; fallback về `Product` khi `Search` lỗi.
  - `UC-B12` (Trung bình): gợi ý từ khoá debounce `200 ms`, chịu lỗi gõ sai chính tả/không dấu (fuzzy), gợi ý danh mục và gian hàng, lịch sử tìm kiếm (đồng bộ client + server, xoá được) và từ khoá hot.
  - `UC-AI02` (Cao): tìm kiếm ngữ nghĩa bằng vector trên `pgvector`, kết hợp kết quả từ khoá (hybrid search) và xếp hạng lại; truy vấn ngắn/mã SP ưu tiên từ khoá; dịch vụ embedding lỗi thì fallback về tìm kiếm từ khoá.
  - **Tiêu chí định lượng** (`docs/spec.md:31`): cập nhật index qua event từ `Product` độ trễ **< 5 giây**; bộ dữ liệu **20.000–50.000 sản phẩm**; mục tiêu **p95 truy vấn < 200 ms**.
  - **Ngoài phạm vi**: Learning to rank, cá nhân hoá thứ hạng tìm kiếm (`docs/spec.md:31`).
- **Stack đã chốt**: PostgreSQL 16 (`postgres:16-alpine`), Redis 7, NATS JetStream 2.10, Go 1.22 (`pgx/v5`, `grpc-go`). Lưu ý `pgvector` cũng là yêu cầu cứng của `services/ai-assistant` (`ADR-0006`).

## Ứng viên

1. **PostgreSQL 16 (`unaccent` + `pg_trgm` + `tsvector`) + `pgvector` (`search_db` riêng)**
   - **Phiên bản & bảo trì**: PostgreSQL 16 (đang dùng); `pgvector` `v0.8.7` (2026-10-01), image Docker Compose `pgvector/pgvector:pg16` (riêng CloudNativePG trên K8s dùng custom image từ `ghcr.io/cloudnative-pg/postgresql:16.16` + `postgresql-16-pgvector`, xem `ADR-0008`); bảo trì rất tích cực.
   - **Giấy phép**: PostgreSQL License (tương đương MIT/BSD, OSI).
   - **Client Go / Rust**: Go `github.com/pgvector/pgvector-go` `v0.4.1` (MIT) + `pgx/v5` (sẵn có); Rust `pgvector` crate + `sqlx`.
   - **Vận hành & K8s**: Không thêm thành phần hạ tầng mới (chỉ đổi image Postgres Compose sang `pgvector/pgvector:pg16` — thử trên bản sao volume trước); chạy 1 container Compose; K8s đi chung CloudNativePG (`v1.30.2`, custom image PG 16.16 + `postgresql-16-pgvector`, `ADR-0008`).
   - **Giới hạn**: Ở 50.000 SP với nhiều facet động (`COUNT(*) GROUP BY`), sửa lỗi gõ sai tiếng Việt và gợi ý prefix debounce 200 ms, cần tinh chỉnh index GIN/GiST/HNSW rất kỹ; p95 dễ vượt 200 ms khi chạy kiểm thử tải đồng thời (`k6`) kết hợp lọc sâu + đếm facet.

2. **Meilisearch Community + `pgvector` (Hybrid RRF tại `services/search`)**
   - **Phiên bản & bảo trì**: `meilisearch/meilisearch` `v1.58.0` (2026-11-23); viết bằng Rust, bảo trì rất tích cực (chu kỳ release đều đặn hàng tháng).
   - **Giấy phép**: Dual-license — **Community Edition: MIT**; **Enterprise Edition: BUSL-1.1** (tính năng sharding ngang/Enterprise yêu cầu bản thương mại trên production).
   - **Client Go / Rust**: Go `github.com/meilisearch/meilisearch-go` (MIT, bảo trì tích cực); Rust `meilisearch-sdk`.
   - **Vận hành & K8s**: Rất nhẹ — 50.000 SP tốn ~200–400 MB RAM, 1 vCPU; 1 container trong `docker-compose.yml`; có Helm chart chính thức `meilisearch-kubernetes` (K8s). Bản MIT chỉ chạy single-node (HA bằng replica độc lập đọc chung stream NATS hoặc snapshot/dump).
   - **Lưu ý tiếng Việt**: Bộ tách từ `Charabia` bỏ dấu kết hợp (NFD) tốt nhưng **không tự quy đổi `đ`/`Đ` → `d`** (`U+0111` là ký tự độc lập). `services/search` phải bổ sung trường chuẩn hoá `name_folded`/`description_folded` (đổi `đ→d` + bỏ dấu) khi index và giữ trường gốc để hiển thị.

3. **Typesense**
   - **Phiên bản & bảo trì**: `typesense/typesense` `v30.2` (2026-04-19); viết bằng C++, bảo trì tích cực (v30 đổi cấu trúc API curation/synonym).
   - **Giấy phép**: Server **GPLv3**; official client (`typesense-go`) **Apache-2.0**.
   - **Client Go / Rust**: Go `github.com/typesense/typesense-go/v4` (Apache-2.0); Rust chỉ có crate cộng đồng.
   - **Vận hành & K8s**: Toàn bộ index nằm trên RAM (~2–3× dung lượng dữ liệu gốc, 50k SP ~300–600 MB RAM); 1 container Compose; HA built-in bằng Raft (tối thiểu 3 node, miễn phí OSS); có Helm chart/Operator cộng đồng nhưng hệ sinh thái nhỏ hơn OpenSearch/Meilisearch. Hỗ trợ sẵn hybrid search (keyword + vector) và chịu lỗi chính tả tốt; tiếng Việt vẫn cần chuẩn hoá `đ/d`.

4. **OpenSearch**
   - **Phiên bản & bảo trì**: `opensearch-project/OpenSearch` `3.10.0` (2026-11-18); bảo trì rất tích cực dưới OpenSearch Software Foundation (Linux Foundation).
   - **Giấy phép**: **Apache-2.0** hoàn toàn.
   - **Client Go / Rust**: Go `github.com/opensearch-project/opensearch-go/v5` (Apache-2.0); Rust `opensearch` chính thức.
   - **Vận hành & K8s**: JVM nặng — tối thiểu 1–2 GB RAM/node (dev) và 3 node (≥ 6 GB RAM) cho HA; có `opensearch-k8s-operator` và Helm chart rất chín muồi; có sẵn `icu_analyzer`/plugin phân tích tiếng Việt, k-NN vector, hybrid pipeline, Prometheus metrics. Nhưng quá nặng cho máy dev/demo cục bộ và quy mô 20k–50k SP.

### Bảng so sánh

| Tiêu chí | Postgres FTS + `pg_trgm` + `pgvector` | Meilisearch (MIT) + `pgvector` | Typesense | OpenSearch |
| --- | --- | --- | --- | --- |
| Độ khớp tiêu chí (50k SP, p95 < 200ms, tiếng Việt, gợi ý, hybrid) | Trung bình (facet động + typo + autocomplete ở 50k SP dễ chạm ngưỡng 200ms) | **Rất tốt** (keyword/typo/facet < 30ms; cộng `pgvector` RRF dễ đạt p95 < 120ms; cần fold `đ→d`) | Rất tốt (typo/facet/hybrid nhanh; cần fold `đ→d`) | Xuất sắc (ICU tiếng Việt chuẩn, full aggregation + k-NN) |
| Độ phức tạp vận hành | **Thấp nhất** (0 dịch vụ mới) | **Thấp** (1 binary Rust, ~300 MB RAM, không JVM) | Thấp–trung bình (quản lý RAM theo kích thước index; HA 3 node Raft) | **Cao** (JVM heap, shard/mapping, 3 node HA, >= 6 GB RAM) |
| Chi phí hạ tầng (50k SP) | $0 thêm | Rất thấp (~0.3–0.5 GB RAM) | Thấp (~0.5–1 GB RAM, tăng tuyến tính theo dữ liệu) | Trung bình–cao (2–6 GB RAM + đĩa) |
| Giấy phép | PostgreSQL License (rất mở) | **MIT** (Community); BUSL-1.1 chỉ cho Enterprise sharding | **GPLv3** (server), Apache-2.0 (client) | **Apache-2.0** |
| Mức độ trưởng thành | Rất cao (PostgreSQL core + `pgvector` 0.8.x) | Cao (phổ biến cho e-commerce SME, khớp `docs/spec.md:172`) | Khá cao (cộng đồng nhỏ hơn Meilisearch/OpenSearch) | Rất cao (chuẩn enterprise, kế thừa Lucene) |

## Quyết định

- **Đề xuất**: **Chọn Meilisearch Community (`v1.58.0`, MIT) cho tầng tìm kiếm từ khoá/fuzzy/facet/gợi ý, kết hợp `pgvector` (`v0.8.7`, client `pgvector-go v0.4.1`; Compose dùng `pgvector/pgvector:pg16`, K8s CNPG dùng custom image `ghcr.io/cloudnative-pg/postgresql:16.16` + `postgresql-16-pgvector`) cho tìm kiếm vector — hợp nhất bằng Reciprocal Rank Fusion (RRF) ngay trong `services/search` (Go)**.
  - **Lý do**:
    1. Khớp trực tiếp `docs/spec.md:31,172` (công cụ tìm kiếm nhẹ + `pgvector`) và mục tiêu p95 < 200 ms trên 50.000 SP với bộ nhớ gọn (~300 MB RAM, chạy mượt cả trên laptop dev lẫn cụm K8s nhỏ).
    2. Tách rõ trách nhiệm: Meilisearch xử lý phần mạnh nhất (prefix autocomplete 200 ms, typo tolerance, bộ lọc/facet động nhiều chiều, đồng nghĩa); `pgvector` (vốn đã bắt buộc cho `ADR-0006`) lưu vector embedding dùng chung vòng đời cập nhật event `product.>`; `services/search` ghép điểm RRF (`score = w_kw/(k+rank_kw) + w_vec/(k+rank_vec)`), tự bỏ qua nhánh vector nếu truy vấn ngắn (≤ 2 từ hoặc mã SKU) hoặc khi embedding API lỗi (`UC-AI02`).
    3. Đường fallback 2 tầng an toàn: Embedding lỗi → chỉ chạy Meilisearch; Meilisearch lỗi → `services/search` (hoặc `gateway`) fallback về `Product.ListProducts` (đã có `unaccent` + `foldVietnamese` từ bước đệm `CAT-F5`).
    4. Lưu lịch sử tìm kiếm và đếm từ khoá hot trong `search_db` (PostgreSQL) + Redis Sorted Set (`search:trending:7d`), giữ đúng nguyên tắc cách ly DB của từng service.
    5. Nếu chủ dự án muốn **tối giản tuyệt đối (0 container tìm kiếm mới)**, phương án dự phòng sạch là dùng **Postgres FTS + `pg_trgm` + `pgvector`** trong `search_db` rồi nâng lên Meilisearch sau (gRPC contract `search.proto` không đổi).
- **Điều kiện kích hoạt chuyển đổi**:
  - Chuyển từ Meilisearch Community sang **OpenSearch** khi danh mục vượt > 2–5 triệu SP cần sharding phân tán OSS (tránh BUSL-1.1), cần phân tích hình thái tiếng Việt phức tạp/aggregation đa tầng, hoặc yêu cầu HA multi-node chịu ghi liên tục không phụ thuộc tái index từ NATS.
- **Phụ thuộc**:
  - `ADR-0006` (chốt mô hình Embedding tiếng Việt và số chiều vector cho `pgvector`).
  - `ADR-0008` (chốt `k3d` cục bộ và tách image Postgres: `pgvector/pgvector:pg16` cho Compose, custom image `ghcr.io/cloudnative-pg/postgresql:16.16` + `postgresql-16-pgvector` cho CloudNativePG).
  - Bước tiền đề `CAT-F4` (thêm transactional outbox + phát `product.>` từ `services/product` lên stream `NEXUSMART`) và `CAT-F5` (fallback không dấu ở `product`).
- **Chủ dự án quyết**: 2026-11-24: chủ dự án uỷ quyền cho manager chốt theo phương án đề xuất (nguyên văn: «mấy câu hỏi bạn từ đề xuất tự làm luôn đi»). Chủ dự án không duyệt riêng từng ADR.
  - **Chốt (`w3-plan.md` §1.1 câu 2)**: Meilisearch Community (`v1.58.0`, MIT) + `pgvector` (`v0.8.7`, `pgvector-go v0.4.1`; Compose dùng `pgvector/pgvector:pg16`, K8s CNPG dùng custom image PG `16.16` + `postgresql-16-pgvector`).
  - **Cổng còn giữ**: đổi image Postgres phải thử trên bản sao volume trước (có bước quay lại); tắt Meilisearch vẫn phải tìm được qua `Product.ListProducts`; đo RAM sau khi thêm; push vẫn phải hỏi chủ dự án từng lần; mọi lần gọi Embedding/LLM thật đầu tiên và ngân sách đều phải hỏi chủ dự án (`ADR-0006`).

## Hệ quả

- **Meilisearch + `pgvector` (đề xuất)**:
  - *Tích cực*: p95 dễ đạt < 100 ms ở 50.000 SP; triển khai 1 container nhẹ ở Compose và Helm; license MIT không vướng pháp lý; không bị khoá dữ liệu gốc vì Meilisearch là read-model dựng lại được từ `product_db`/NATS.
  - *Tiêu cực*: phải duy trì thêm 1 container Meilisearch và đồng bộ 2 nguồn index (Meilisearch + `pgvector`); phải tự chuẩn hoá `đ/Đ → d` ở `services/search`; bản MIT không có clustering nội tại (HA ở prod cần chạy 2 instance Meilisearch độc lập cùng subscribe NATS với consumer riêng).
- **Thuần Postgres FTS + `pg_trgm` + `pgvector`**:
  - *Tích cực*: 0 hạ tầng mới, đơn giản hoá backup/HA.
  - *Tiêu cực*: khó đạt chất lượng typo-tolerant autocomplete + facet đa chiều mượt ở 50.000 SP; tải tìm kiếm cạnh tranh trực tiếp CPU/IOPS với Postgres chính.
- **Typesense / OpenSearch**:
  - *Typesense*: HA Raft tốt nhưng ăn RAM tuyến tính và server GPLv3.
  - *OpenSearch*: mạnh nhất về Lucene/tiếng Việt/HA nhưng nặng RAM/vận hành vượt nhu cầu đồ án.

## Kế hoạch triển khai

| Task | Phạm vi | Tiêu chí nghiệm thu đo được |
| --- | --- | --- |
| `T1` Event đồng bộ từ `product` (`CAT-F4`) | Mở rộng stream `NEXUSMART` thêm `product.>`; thêm bảng `outbox_events` + relay ở `services/product` phát `product.upserted`, `product.deleted`, `category.updated` | Tạo/sửa/duyệt/ẩn SP tạo bản ghi `outbox_events` trong cùng TX và xuất hiện trên NATS trong **< 1 giây**; có integration test |
| `T2` Hợp đồng `search.proto` & khung `services/search` | Tạo `proto/nexusmart/search/v1/search.proto` (`SearchProducts`, `Suggest`, `ListSearchHistory`, `DeleteSearchHistory`, `ReindexAll`), chạy `make proto`, cập nhật `docs/contracts.md`; dựng `services/search` (Go, `:50059/:8089`, `search_db`, `/healthz`, `/readyz`, `/metrics`) | `buf lint`, `make proto`, unit test pass; consumer idempotent (`processed_events` + DLQ `NEXUSMART_DLQ`) |
| `T3` Indexer Meilisearch + chuẩn hoá tiếng Việt | Tích hợp `meilisearch` vào Compose/Helm; cài chuẩn hoá `đ/Đ→d` + bỏ dấu (`name_folded`), mapping facet (`category_ids` gồm toàn bộ tổ tiên, `price_vnd`, `rating_avg`, `shop_id`, `status=APPROVED`); lệnh full-reindex batch | Sửa/duyệt SP trên `product` phản ánh trong `SearchProducts` **< 5 giây**; truy vấn `ao thun`, `duong`, `áo thun` trả kết quả khớp nhau |
| `T4` Gợi ý, lịch sử & hybrid `pgvector` | Cài `Suggest` (từ khoá + danh mục + gian hàng + trending Redis), lịch sử tìm kiếm (client + `search_db` có xoá); đổi Postgres Compose image sang `pgvector/pgvector:pg16` (thử trên bản sao volume trước; K8s CNPG dùng custom image PG 16.16 + `postgresql-16-pgvector`), thêm bảng `product_embeddings` + RRF hybrid + fallback khi embedding/Meilisearch lỗi | `Suggest` phản hồi < 80 ms; tắt embedding API vẫn trả kết quả từ khoá; tắt Meilisearch tự fallback `Product.ListProducts` |
| `T5` Nối Gateway, Web & kiểm thử tải 50.000 SP | Mở route `/api/v1/search`, `/api/v1/search/suggest`, `/api/v1/search/history` ở `gateway`; bỏ trần 100 SP ở `CatalogExplorer` và thay mock `ShopHeader` (debounce 200 ms); script sinh **20.000–50.000 SP** (`CAT-D2`) + kịch bản `k6` | Chạy `k6` trên ≥ 20.000 SP (mục tiêu 50.000 SP): **p95 < 200 ms**, error rate < 0.5%; Playwright E2E pass cho tìm không dấu + lọc + gợi ý |

## Câu hỏi chủ dự án cần trả lời

1. **Có duyệt thêm container Meilisearch (MIT) bên cạnh `pgvector`**, hay muốn ưu tiên tối giản **0 container mới** (chỉ dùng Postgres `search_db` với `unaccent` + `pg_trgm` + `pgvector` cho giai đoạn đầu)?
   - **Trả lời (`w3-plan.md` §1.1 câu 2)**: Chốt thêm Meilisearch (`v1.58.0`, MIT) + `pgvector` (`v0.8.7`). Đổi image Postgres thử trên bản sao volume trước; tắt Meilisearch vẫn tìm được.
2. **Dữ liệu vector `product_embeddings` đặt ở `search_db` hay `ai_db`** (hoặc `ai-assistant` gọi gRPC `Search.HybridSearch` để tránh tạo và lưu trùng vector ở 2 DB)? *(Khuyến nghị: `search_db` sở hữu `product_embeddings` và `Search.HybridSearch` phục vụ cả `UC-AI02` lẫn RAG sản phẩm của `UC-AI01`, còn `ai_db` chỉ lưu embedding tài liệu chính sách `UC-AI11`.)*
   - **Trả lời**: Chưa chốt, mặc định `search_db` sở hữu `product_embeddings` và `ai-assistant` gọi `Search.HybridSearch` (`ai_db` chỉ lưu embedding chính sách `UC-AI11`), đổi được sau.
3. **Danh mục đồng nghĩa (synonyms) tiếng Việt** có cần giao diện Admin quản lý hay quản lý dạng file JSON/YAML trong repo là đủ cho phạm vi đồ án?
   - **Trả lời**: Chưa chốt, mặc định quản lý dạng file JSON/YAML trong repo cho phạm vi đồ án, đổi được sau.

## Tra lại 2026-11-24

- **Meilisearch**: nâng tham chiếu `v1.57.0` → **`v1.58.0`** (`2026-11-23`), giữ giấy phép MIT (CE) / BUSL-1.1 (EE), không có CVE nghiêm trọng mới.
- **`pgvector` & `pgvector-go`**: giữ `pgvector v0.8.7` (PostgreSQL License, đã vá `CVE-2026-103484`), nâng client Go `pgvector-go` `v0.4.0` → **`v0.4.1`** (MIT).
- **Tách chiến lược image Postgres (liên đới `ADR-0008`)**: `pgvector/pgvector:pg16` chỉ dùng cho Docker Compose; CloudNativePG (`v1.30.2`) không chạy trực tiếp image này (`UID 26`/operand layout) và Image Volume Extensions yêu cầu PostgreSQL 18+, nên K8s dùng custom image từ `ghcr.io/cloudnative-pg/postgresql:16.16` cài `postgresql-16-pgvector`.
- **Nguồn**:
  - https://github.com/meilisearch/meilisearch/releases/tag/v1.58.0
  - https://github.com/meilisearch/meilisearch/blob/main/LICENSE
  - https://github.com/pgvector/pgvector/releases/tag/v0.8.7
  - https://github.com/pgvector/pgvector/blob/master/LICENSE
  - https://hub.docker.com/r/pgvector/pgvector/tags
  - https://cloudnative-pg.io/documentation/current/container_image_requirements/

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-23
- **Nguồn**:
  - Meilisearch: https://github.com/meilisearch/meilisearch, https://github.com/meilisearch/meilisearch/releases, https://github.com/meilisearch/meilisearch/blob/main/LICENSE, https://github.com/meilisearch/meilisearch-go
  - pgvector & pgvector-go: https://github.com/pgvector/pgvector, https://github.com/pgvector/pgvector/releases, https://github.com/pgvector/pgvector-go
  - Typesense: https://github.com/typesense/typesense, https://github.com/typesense/typesense/releases, https://github.com/typesense/typesense-go
  - OpenSearch: https://github.com/opensearch-project/OpenSearch, https://github.com/opensearch-project/OpenSearch/releases, https://github.com/opensearch-project/opensearch-go
