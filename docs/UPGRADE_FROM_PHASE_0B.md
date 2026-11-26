# Hướng dẫn nâng cấp từ `phase-0b` (`a98ea3d`) lên bản gộp `main`

> Áp dụng cho hệ thống đang chạy nhánh `phase-0b` (`a98ea3d`) nâng cấp tại chỗ (giữ dữ liệu DB hiện có). Chi tiết quyết định kiến trúc xem [ADR-0010](adr/0010-unify-main-and-phase-0b.md).
> **Lưu ý triển khai:** Phải triển khai đồng bộ toàn bộ 13 service + `web` trong cùng một cửa sổ bảo trì (không chạy lẫn pod bản cũ và bản mới).

---

## Trước khi deploy

- **Thu hẹp `TRUSTED_PROXIES` của `gateway`:** Giá trị mặc định trong `deploy/helm/gateway/values.yaml` hiện để rộng (`10.0.0.0/8,172.16.0.0/12`). Trước khi deploy staging/production, bắt buộc ghi đè về đúng CIDR pod của `ingress-nginx` và `web` trên cụm đích để các pod khác không thể giả mạo `X-Forwarded-For` hoặc vào bucket `rl:bff`.
- **Thiết lập `NetworkPolicy` ở cấp cụm** *(không kèm manifest sẵn trong repo vì phụ thuộc CNI/namespace và nơi triển khai chưa chốt)*:
  - Chỉ cho phép pod `ingress-nginx` kết nối vào `web:3000` (chặn truy cập trực tiếp từ pod khác tới `web:3000` khi `TRUST_PROXY_HEADERS=true`).
  - Chỉ cho phép `web`, `ingress-nginx`, `assistant`, và `Prometheus` (scrape `/metrics`) kết nối vào `gateway:8080`.

---

## Bước 1 — Dừng ghi & Sao lưu toàn bộ cơ sở dữ liệu

1. Dừng lưu lượng ghi (hoặc dừng các container ứng dụng `web`, `gateway`, 12 microservice) trong khi giữ `postgres` và `search-postgres` chạy để dump sạch.
2. Sao lưu toàn bộ **10 DB PostgreSQL** (`user_db`, `product_db`, `promotion_db`, `order_db`, `payment_db`, `shipping_db`, `notification_db`, `chat_db`, `assistant_db` trên `postgres`, cộng `search_db` trên `search-postgres`) bằng `tools/backup/backup.sh` (hoặc `pg_dump -Fc` cho từng DB) và kiểm tra kích thước file dump > 0.
3. **Xuất danh sách khoản hoàn VietQR đã `completed` trước nâng cấp** để đối chiếu ở Bước 5:
   ```sql
   -- Chạy trên payment_db TRƯỚC khi khởi động payment bản mới
   SELECT id, payment_id, order_id, amount_vnd, status, provider_refund_id, created_at, updated_at
   FROM refunds
   WHERE status = 'completed' AND provider_refund_id IS NULL
   ORDER BY created_at ASC;
   ```

---

## Bước 2 — Cập nhật biến môi trường (so `.env.example` hiện tại với `a98ea3d:.env.example`)

### 2.1 Biến BẮT BUỘC đổi trước production (tuyệt đối không để mặc định dev/placeholder)

Kiểm tra đối chiếu với `deploy/k8s/create-secrets.sh` (khi triển khai `nexusmart-production`, script sẽ từ chối chạy nếu thiếu hoặc dùng giá trị placeholder):

| Biến | Yêu cầu production |
| --- | --- |
| `BANK_ACCOUNT_KEY` *(mới)* | Khóa AES-256-GCM 32-byte base64 mã hóa STK shop trong `user_db` (`openssl rand -base64 32`). |
| `SHIPPING_CONFIG_KEY` *(mới)* | Khóa AES-256-GCM 32-byte base64 mã hóa token hãng vận chuyển trong `shipping_db` (`openssl rand -base64 32`). |
| `ZALOPAY_APP_ID`, `ZALOPAY_KEY1`, `ZALOPAY_KEY2` *(đổi mặc định về rỗng)* | Bắt buộc điền khóa thật từ env khi `ZALOPAY_MODE=sandbox` (`payment` từ chối khởi động nếu rỗng ở chế độ sandbox; chưa bật live mode ở đợt này). |
| `JWT_SECRET`, `SERVICE_JWT_SECRET` | Hai chuỗi ngẫu nhiên mạnh và **phải khác nhau**. |
| `POSTGRES_PASSWORD`, `ADMIN_BOOTSTRAP_PASSWORD`, `MEILI_MASTER_KEY` | Đổi khỏi giá trị dev mặc định. |
| `PAYMENT_WEBHOOK_SECRET`, `SHIPPING_WEBHOOK_SECRET` | Khóa HMAC-SHA256 thật cho webhook thanh toán & vận chuyển. |
| `PAYOS_CLIENT_ID`, `PAYOS_API_KEY`, `PAYOS_CHECKSUM_KEY` (hoặc `SEPAY_API_TOKEN`) | Bắt buộc tương ứng với `VIETQR_MODE=payos` hoặc `VIETQR_MODE=sepay`. |
| `COOKIE_SECURE`, `SHIPPING_TEST_FAULT_INJECTION`, `APP_ORIGIN` | Production phải đặt `COOKIE_SECURE=true`, `SHIPPING_TEST_FAULT_INJECTION=false`; `TRUSTED_PROXIES` chỉ gồm CIDR reverse proxy/mạng `bff` tin cậy. |
| `TRUST_PROXY_HEADERS` | **Bắt buộc `true` ở production** (chỉ bật khi `web` đứng sau reverse proxy tin cậy); proxy phía trước **phải ghi đè** (không nối thêm) `X-Forwarded-Proto` và `X-Forwarded-For` (hoặc `X-Real-IP`), đồng thời CIDR của proxy/`web` phải nằm trong `TRUSTED_PROXIES` của `gateway` để quét `X-Forwarded-For` phải→trái. `web` **không bao giờ tin `X-Forwarded-Host`** (luôn lấy host/tenant từ header `Host`, khớp mặc định giữ nguyên `Host` của `ingress-nginx`). Khi `TRUST_PROXY_HEADERS=false`, `web` không chuyển tiếp IP khách nên mọi lưu lượng trình duyệt qua `web` dùng chung bucket theo IP container `web` (mọi yêu cầu ghi 300/phút qua `RATE_LIMIT_IP_PER_MIN`, quên/đặt lại mật khẩu 10/phút qua `RATE_LIMIT_AUTH_PER_MIN`, tra cứu đơn 10/phút qua `RATE_LIMIT_ORDER_LOOKUP_PER_MIN`), dễ gây lỗi `429` hàng loạt — do đó production bắt buộc đặt `true`. |
| `PUBLIC_GATEWAY_URL`, `CHAT_WS_URL` *(mới, để trống ở dev)* | Bắt buộc đặt URL `https://` / `wss://` công khai ở production (không dùng host nội bộ như `gateway-bff`) để chat realtime chạy qua HTTPS và khớp CSP `connect-src`. |
| `ORDER_COD_REQUIRE_PHONE_VERIFIED` *(mới, mặc định `true`)* | Lưu ý: OTP SĐT hiện đang mock qua email/Mailpit — cần tích hợp nhà cung cấp SMS thật trước khi bật xác minh SĐT bắt buộc ở production. |

### 2.2 Biến thêm mới (65 biến so với `a98ea3d:.env.example`)

- **User & Bảo mật (5):** `BANK_ACCOUNT_KEY`, `USER_BANK_PAYOUT_LOCK_HOURS` (`24`), `USER_PHONE_OTP_TTL_SECONDS` (`300`), `USER_PHONE_OTP_COOLDOWN_SECONDS` (`60`), `USER_PHONE_OTP_MAX_PER_HOUR` (`5`).
- **Gateway & Proxy Trust (9):** `TRUSTED_PROXIES` (`172.31.250.0/28`), `SEARCH_FALLBACK_TIMEOUT_MS` (`1500`), `RATE_LIMIT_AUTH_PER_MIN` (`10`), `RATE_LIMIT_BANK_OTP_PER_MIN` (`5`), `RATE_LIMIT_PHONE_OTP_PER_MIN` (`5`), `RATE_LIMIT_SHIPPING_FEE_PER_MIN` (`30`), `RATE_LIMIT_ORDER_LOOKUP_PER_MIN` (`10`), `RATE_LIMIT_ORDER_LOOKUP_CODE_PER_10MIN` (`5`), `RATE_LIMIT_WEBHOOK_PER_MIN` (`600`).
- **Payment & ZaloPay (6):** `PAYMENT_SELLER_HOLD_HOURS` (`72`), `PAYMENT_LARGE_REFUND_APPROVAL_VND` (`5000000`), `ZALOPAY_REDIRECT_URL` (`http://localhost:3300/thanh-toan/cho`), `ZALOPAY_HTTP_TIMEOUT_SECS` (`10`), `ZALOPAY_LATE_SWEEP_WINDOW_HOURS` (`24`), `ZALOPAY_LATE_SWEEP_BATCH_LIMIT` (`25`).
- **Shipping (12):** `SHIPPING_CONFIG_KEY`, `SHIPPING_LABEL_BASE_URL` (`http://localhost:3300/api/shipping/labels`), `SHIPPING_MAX_REDELIVERY` (`3`), `SHIPPING_FEE_CACHE_TTL_SECONDS` (`60`), `SHIPPING_CREATE_RETRY_MAX` (`8`), `SHIPPING_CREATE_RETRY_BASE_MS` (`2000`), `SHIPPING_CREATE_RETRY_MAX_BACKOFF_MS` (`300000`), `SHIPPING_CREATE_WORKER_INTERVAL_MS` (`2000`), `CARRIER_MAX_RETRIES` (`3`), `CARRIER_CB_MAX_FAILURES` (`3`), `CARRIER_CB_OPEN_TIMEOUT` (`30s`), `SHIPPING_TEST_FAULT_INJECTION` (`false`).
- **Order (11):** `ORDER_COD_REQUIRE_PHONE_VERIFIED` (`true`), `ORDER_COD_MIN_ORDERS_FOR_RATE` (`3`), `ORDER_COD_MAX_REJECT_RATE_BPS` (`3000`), `ORDER_FLASH_PAYMENT_EXPIRY_MINUTES` (`5`), `ORDER_AUTO_COMPLETE_DAYS` (`7`), `ORDER_SELLER_CONFIRM_HOURS` (`48`), `ORDER_RETURN_SELLER_RESPONSE_DAYS` (`3`), `ORDER_RETURN_SHIP_DAYS` (`5`), `ORDER_RETURN_INSPECT_DAYS` (`3`), `ORDER_BATCH_SHIP_MAX_ITEMS` (`50`), `ORDER_GRPC_ADDR` (`localhost:50055`).
- **Product & Promotion (3):** `PRODUCT_IMPORT_MAX_ROWS` (`1000`), `PRODUCT_IMPORT_MAX_BYTES` (`1048576`), `PROMOTION_VOUCHER_EXPIRY_NOTICE_HOURS` (`24`).
- **Search, Meilisearch & Embeddings (13):** `SEARCH_GRPC_ADDR` (`localhost:50059`), `MEILISEARCH_URL` (`http://meilisearch:7700`), `MEILI_ENV` (`production`), `MEILI_NO_ANALYTICS` (`true`), `MEILI_MAX_INDEXING_MEMORY` (`256Mb`), `EMBEDDING_MODE` (`mock`), `EMBEDDING_MODEL` (`text-embedding-3-small`), `EMBEDDING_DIM` (`1536`), `EMBEDDING_API_KEY` (`""`), `SEARCH_RRF_K` (`60`), `SEARCH_MEILI_TIMEOUT_MS` (`1500`), `SEARCH_VECTOR_TIMEOUT_MS` (`800`), `SEARCH_VECTOR_MIN_SIMILARITY` (`0.25`).
- **Web & Chat realtime (2):** `PUBLIC_GATEWAY_URL` (`""`), `CHAT_WS_URL` (`""`) — bắt buộc đặt URL `https://` / `wss://` công khai ở production để chat realtime chạy qua HTTPS.
- **Khác (4):** `WEB_BASE_URL` (`http://localhost:3300`), `USER_GRPC_ADDR` (`localhost:50051`), `OTEL_HEALTH_PORT` (`13133`), `SEED_CATALOG_COUNT` (`20000`).

### 2.3 Biến đổi giá trị mặc định (5 biến)

- `GATEWAY_URL`: `http://localhost:28080` → `http://localhost:8080` (compose tự ghi đè `web` thành `http://gateway-bff:8080`).
- `ZALOPAY_CALLBACK_URL`: `http://localhost:28080/webhooks/zalopay` → `http://localhost:8080/webhooks/zalopay`.
- `ZALOPAY_APP_ID`: `2554` → `""`.
- `ZALOPAY_KEY1`: `sandbox-zalopay-key1-placeholder` → `""`.
- `ZALOPAY_KEY2`: `sandbox-zalopay-key2-placeholder` → `""`.

### 2.4 Biến bị bỏ (13 biến)

- **Chuyển sang cấu hình DB quản trị (6):** `GHN_ENABLED`, `GHTK_ENABLED`, `MOCK_CARRIER_ENABLED` (bảng carrier admin `/api/v1/admin/carriers`), `ORDER_COD_MAX_VND` và `ORDER_PAYMENT_EXPIRY_MINUTES` (bảng `payment_method_configs`, `/api/v1/admin/payment-methods`), `ORDER_DEFAULT_COMMISSION_BPS` (lịch phí theo danh mục `/api/v1/admin/fees/categories`).
- **Không còn công bố ra host / bỏ cấu hình cũ của `search` (4):** `MEILI_PORT`, `SEARCH_POSTGRES_PORT` (chỉ chạy nội bộ mạng compose), `MEILI_INDEX`, `SEARCH_STARTUP_SYNC` (thay bằng reindex admin).
- **Đổi tên chuẩn (3, vẫn có fallback trong `docker-compose.yml`):** `TRUSTED_PROXY_CIDRS` → `TRUSTED_PROXIES`, `WEB_APP_ORIGIN` → `APP_ORIGIN`, `WEB_COOKIE_SECURE` → `COOKIE_SECURE`.

---

## Bước 3 — Thay đổi image & volume cho `search-postgres` và `meilisearch`

| Thành phần | Bản `phase-0b` (`a98ea3d`) | Bản gộp hiện tại (`docker-compose.yml`) | Tác động |
| --- | --- | --- | --- |
| `search-postgres` | `pgvector/pgvector:pg16` (mở cổng `5433`) | `pgvector/pgvector:0.8.7-pg16` (chỉ nội bộ, `mem_limit: 384m`) | Ghim `pgvector >= 0.8` cho `hnsw.iterative_scan`, giữ nguyên volume `search_postgres_data`; `postgres` chính giữ nguyên `postgres:16-alpine`. |
| `meilisearch` | `getmeili/meilisearch:v1.12` (mở cổng `7700`, volume `meilisearch_data`) | `getmeili/meilisearch:v1.58.0` (chỉ nội bộ, `mem_limit: 512m`, volume `meilisearch_v1_58_data`) | Meilisearch từ chối mở trực tiếp dữ liệu minor cũ → bắt buộc dùng volume mới `meilisearch_v1_58_data` và chạy reindex ở Bước 6. Chỉ xóa volume cũ `meilisearch_data` sau khi xác nhận nâng cấp ổn định. |
| `web` / `e2e-web` | Node 22 (`web/Dockerfile` context `./web`) | Node 24 (`web/Dockerfile` context `.`), nối mạng nội bộ `bff` (`172.31.250.0/28`) | Build luôn dùng context gốc repo. |

> **Lưu ý multi-replica `web`:** Khi chạy `web` nhiều replica (`replicaCount > 1`), cần bật sticky session hoặc dùng Redis cho cơ chế single-flight refresh token (`web/src/lib/server/tokens.ts`, hiện giữ trạng thái trong bộ nhớ tiến trình).

---

## Bước 4 — Khởi động stack & Migration tự chạy

Khởi động toàn bộ cụm dịch vụ bản mới (mọi service tự chạy migration nhúng khi khởi động, **forward-only**, không sửa các file `phase-0b` đã áp dụng):

- `user`: chạy nối tiếp `0005` → `0013` (giữ nguyên `0001..0004`).
- `product`: chạy nối tiếp `000004` → `000013` (`000013` giải phóng `flash_sale_stock_reservations` cũ và đặt `flash_sale_reserved_stock = 0`).
- `promotion`: chạy nối tiếp `003` → `007` (bảng `promotion_schema_migrations`).
- `order`: chạy nối tiếp `0005` → `0021` (`0008_returns_and_lifecycle.sql` ánh xạ dữ liệu `return_requests`, `complaints` và `orders.status` từ `phase-0b`; giữ bảng `complaints` chỉ-đọc).
- `payment`: chạy nối tiếp `0004` → `0018` (`sqlx::migrate!` kiểm checksum các file `0001..0003` nguyên vẹn; `0018_backfill_phase0b_zalopay_refund_settle.sql` tự động bù idempotent bút toán `refund_settle` cho các khoản hoàn ZaloPay đã chi ở `phase-0b`).
- `shipping`: bảng `schema_migrations` tự bootstrap nhận diện DB `phase-0b` qua cột `shipments.direction` (không chạy lại `000002_return_shipments.sql`), sau đó chạy `000003` → `000008`.
- `notification`: chạy `000003` → `000005` (SQL idempotent).
- `search`: chạy `000002_w3b3_search_schema.sql` trên `search_db`.

Kiểm tra `/healthz` và `/readyz` trên toàn bộ service (`8080..8091`, và `8092` nếu bật profile `obs`).

---

## Bước 5 — Rà tay các khoản hoàn tiền VietQR chuyển `completed → pending` ở `payment 0005`

> **CẢNH BÁO — KHÔNG CHUYỂN TIỀN LẦN HAI:**
> Migration `services/payment/migrations/0005_refund_settlement_and_reconciliation.sql` đưa các khoản hoàn tiền cũ có `status = 'completed'`, `provider_refund_id IS NULL` và chưa có bút toán `refund_settle` về trạng thái `pending` để hoàn tất quy trình sổ cái 2 bước (riêng hoàn ZaloPay đã chi có `provider_refund_id IS NOT NULL` được giữ nguyên `completed`).
> Migration `services/payment/migrations/0018_backfill_phase0b_zalopay_refund_settle.sql` tự động bù idempotent giao dịch sổ cái `refund_settle` (`Debit refund_payable` / `Credit gateway_clearing`) cho các khoản hoàn ZaloPay `phase-0b` đã giữ `completed` này (không đụng VietQR `provider_refund_id IS NULL`, COD hoặc DB đi lên từ `main`), nên vận hành **chỉ cần rà tay các khoản hoàn VietQR** bên dưới.

1. Đối chiếu danh sách đã xuất ở Bước 1 với danh sách `GET /api/v1/admin/refunds?status=PENDING` (hoặc truy vấn `payment_db`):
   ```sql
   SELECT id, payment_id, order_id, amount_vnd, status, provider_refund_id, reason, created_at
   FROM refunds
   WHERE status = 'pending'
   ORDER BY created_at ASC;
   ```
2. Với **từng khoản hoàn VietQR mà bộ phận vận hành/kế toán ĐÃ chuyển khoản tay cho khách ở giai đoạn `phase-0b`**:
   - **Tuyệt đối KHÔNG tạo lệnh chuyển tiền ngân hàng mới.**
   - Ghi nhận đã chi trên sổ cái bằng `POST /api/v1/admin/refunds/{id}/settle` (kèm `Idempotency-Key` duy nhất, điền mã giao dịch ngân hàng cũ vào `provider_refund_id` và ghi chú `"Đã chuyển tay trước nâng cấp phase-0b"`).
3. Chỉ chuyển tiền thật đối với các khoản xác nhận **chưa từng chi** trên sao kê ngân hàng.

---

## Bước 6 — Chạy reindex tìm kiếm một lần (`POST /api/v1/admin/search/reindex`)

Do `meilisearch` dùng volume mới `meilisearch_v1_58_data` và `services/search` dùng schema `3b-3`, chỉ mục tìm kiếm sẽ **rỗng** sau nâng cấp cho đến khi reindex:

1. Gọi một lần bằng JWT staff có quyền `search.reindex` (`super_admin`, `admin`, `ops`):
   ```bash
   curl -fsS -X POST "http://localhost:${GATEWAY_PORT:-8080}/api/v1/admin/search/reindex" \
     -H "Authorization: Bearer ${ADMIN_JWT}" \
     -H "Content-Type: application/json" \
     -d '{"reindex_products":true,"reindex_embeddings":true,"reindex_categories":true,"reindex_shops":true}'
   ```
2. Lấy `job_id` trả về (`202 Accepted`) và kiểm tra tới khi `status = "COMPLETED"`:
   ```bash
   curl -fsS "http://localhost:${GATEWAY_PORT:-8080}/api/v1/admin/search/reindex/${JOB_ID}" \
     -H "Authorization: Bearer ${ADMIN_JWT}"
   ```
3. Kiểm tra nhanh `GET /api/v1/search?q=...` và `GET /api/v1/search/suggest?q=...&shop_id=...` đã trả kết quả thật.

---

## Bước 7 — Dọn consumer NATS JetStream cũ `search-product-indexer-v1`

- Bản `search` mới dùng 2 durable consumer: `search-product-events` (subject `product.>`) và `search-user-events` (subject `user.>`).
- Consumer cũ `search-product-indexer-v1` của `phase-0b` trên stream `NEXUSMART` không còn dịch vụ nào tiêu thụ; nếu giữ lại sẽ tích lũy pending message trên JetStream.
- Sau khi Bước 4–6 ổn định, xóa consumer cũ:
  ```bash
  nats --server "${NATS_URL:-nats://127.0.0.1:4222}" consumer rm NEXUSMART search-product-indexer-v1 -f
  ```
  *(Hoặc kiểm tra trước bằng `nats consumer ls NEXUSMART` / endpoint monitor `http://127.0.0.1:8222/jsz?consumers=true`.)*

---

## Bước 8 — Kiểm tra sau nâng cấp & Lưu ý API đã bỏ

- Kiểm tra 8 API `phase-0b` đã bỏ không còn caller bên ngoài gọi: `POST/GET /api/v1/orders/{id}/returns`, `POST /api/v1/orders/{id}/complaints`, `POST /api/v1/seller/returns/{id}/approve|reject`, `GET /api/v1/admin/complaints`, `POST /api/v1/admin/complaints/{id}/resolve`, `GET /api/v1/admin/payments/reconciliation-reports` (đã thay bằng `/api/v1/orders/{id}/return`, `/api/v1/returns/*`, `/api/v1/seller/returns/{id}/respond`, `/api/v1/admin/returns/*`, `/api/v1/admin/reconciliation/runs|queue`).
- Kiểm tra `GET /api/v1/admin/ledger/summary` trả `"is_balanced": true`.

---

## Cách quay lui (Rollback)

Toàn bộ migration của NexusMart là **forward-only** (không có file `down.sql`). Nếu cần quay về `phase-0b` (`a98ea3d`):

1. **Dừng toàn bộ service ứng dụng** (`web`, `gateway`, 12 microservice) để ngắt mọi ghi DB.
2. **Khôi phục toàn bộ 10 dump DB** đã sao lưu ở Bước 1 (`pg_restore --clean --if-exists` vào 9 DB trên `postgres` và `search_db` trên `search-postgres`; tham khảo quy trình trong `tools/backup/restore-drill.sh`). Không chạy bản `phase-0b` trên DB đã chạy migration bản gộp.
3. **Đưa mã nguồn, `.env` và `docker-compose.yml` về commit `a98ea3d`**: dùng lại image `getmeili/meilisearch:v1.12` (gắn volume `meilisearch_data` cũ nếu chưa xóa) và `pgvector/pgvector:pg16`, cùng bộ biến môi trường cũ (bao gồm `ORDER_COD_MAX_VND`, `ORDER_PAYMENT_EXPIRY_MINUTES`, `GHN_ENABLED`, `GHTK_ENABLED`, `MOCK_CARRIER_ENABLED`).
4. **Khởi động lại cụm `phase-0b`** và xác nhận `/healthz`, `/readyz`, tìm kiếm và số liệu sổ cái khớp trạng thái trước nâng cấp.
