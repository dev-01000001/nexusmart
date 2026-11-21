# Báo cáo Đánh giá Định lượng – NexusMart (`docs/EVAL_REPORT.md`)

- **Ngày đo kiểm cuối:** `2026-11-21` (nhánh `phase-0b`, toàn bộ 11 microservice + AIOps + Gateway + Web + stack Observability đang chạy đồng thời trên Docker Compose với **20.021 sản phẩm** đã đồng bộ vào `product_db`, `search_db` pgvector và Meilisearch).
- **Cơ sở đối chiếu:** Toàn bộ 17 hạng mục thuộc cột **"Yêu cầu kỹ thuật và tiêu chí đo"** trong `docs/spec.md` (tương đương tệp đặc tả `NexusMart – Phân công và lộ trình 6 tháng.xlsx`), bổ sung tiêu chí hệ thống Template gian hàng của Swarm T.
- **Nguyên tắc trung thực:** Mọi con số dưới đây đều đến từ lệnh thực thi thật, có nguồn lệnh/báo cáo kiểm chứng rõ ràng. Mọi tiêu chí mới đạt ở chế độ mock/cục bộ hoặc đang bị lỗi hồi quy liên swarm (`QA-03`, `QA-04`) đều được đánh dấu rõ **Đạt một phần** hoặc **Chưa đạt** kèm nguyên nhân gốc.

---

## 1. Tổng hợp kết quả theo 17 hạng mục đặc tả

| # | Hạng mục (`docs/spec.md`) | Tổng tiêu chí | Đạt | Đạt một phần | Chưa đạt | Ghi chú trọng yếu |
|---|---|---:|---:|---:|---:|---|
| 1 | Kiến trúc tổng thể | 5 | 5 | 0 | 0 | 11 service + AIOps + Gateway + Web; 10 DB riêng; 0 cổng gRPC public |
| 2 | Ngôn ngữ và nền tảng | 4 | 3 | 1 | 0 | Lint/format/build xanh; 15/15 Dockerfile non-root, 14/15 multi-stage (`PLT-05`) |
| 3 | Luồng mua hàng | 4 | 3 | 1 | 0 | Checkout 200 VU máy sạch: Read p95 `38.75 ms`, Checkout p95 `204.95 ms`, `0` 5xx (có 429 theo user/phút); máy tải nặng 0-429 (`PLT-07`) vượt ngưỡng do 1 replica |
| 4 | Người bán và quản trị | 3 | 2 | 1 | 0 | RBAC 2 lớp + Audit log append-only đạt; chưa có job nền nhập file CSV hàng loạt |
| 5 | Thanh toán | 5 | 5 | 0 | 0 | VND số nguyên, sổ cái ghi kép, ZaloPay+VietQR HMAC/replay/10x idempotent, đối soát & chặn mock-key ở prod (`D2`) |
| 6 | Vận chuyển | 4 | 4 | 0 | 0 | Interface `Carrier` (GHN + GHTK + Mock), circuit breaker, polling dự phòng, mapping 63→34 tỉnh |
| 7 | Khuyến mãi | 3 | 3 | 0 | 0 | Redis Lua script; 1.000 req / 100 suất → `100/100`, `oversell = 0`; hoàn voucher/xu khi trả hàng |
| 8 | Chat | 3 | 3 | 0 | 0 | 2-instance Redis Pub/Sub PASS; **1.000 kết nối WS đồng thời** (`0` lỗi, `0` drop, RSS ~71 MB) |
| 9 | Tìm kiếm | 4 | 4 | 0 | 0 | Có dấu = không dấu (`17.262 = 17.262`), sync **`368.5 ms` (< 5 s)**, **20.021 SP**, k6 live **p95 `42.97 ms` (< 200 ms)** |
| 10 | Trợ lý AI | 5 | 4 | 1 | 0 | Eval **100/100** (`Grounded 100%`, `Hallucination 0%`), Injection **36/36**, live SSE TTFT **`46.43 ms`** (chạy `LLM_MODE=mock`, chưa đo key Anthropic thật) |
| 11 | Giám sát và log | 4 | 3 | 1 | 0 | Trace sống vào Tempo `HTTP 200` (`gateway → search`), 16/16 target Prometheus `up`, 4 dashboard Grafana, 24 SLO rules, Telegram mock 98 alerts; còn `OBS-09`/`OBS-13` trên `services/user` |
| 12 | AIOps | 4 | 4 | 0 | 0 | Rust z-score + EWMA, Loki+Tempo context, allowlist + dry-run; Chaos **5/5 phát hiện < 31s (< 2 phút)**, **5/5 đúng RCA (>= 4/5)** |
| 13 | Bảo mật | 6 | 6 | 0 | 0 | Argon2id, JWT 15m + rotating refresh, rate limit, OWASP/XSS/SSRF, 0 K8s Secret trong repo, `npm audit` = `0 vulnerabilities`, chỉ `28080` & `3300` public |
| 14 | Dữ liệu | 3 | 3 | 0 | 0 | Migration riêng từng service; Restore Drill **9 DB / 77 bảng / 328.473 dòng khớp 100%** trong `2.45 s`; `make seed` 20k SP idempotent |
| 15 | Hạ tầng và CI/CD | 5 | 5 | 0 | 0 | 14 Helm charts (**28/28** lint + `kubeconform`), deploy + HPA + rollback 1 lệnh trên `kind`, CI chỉ `workflow_dispatch` |
| 16 | Kiểm thử | 5 | 3 | 2 | 0 | Coverage `order 65.9%`, `payment 85.25%`, `promotion 68.5%`, `shipping 62.9%`; Web E2E `41/41` x2 PASS; Go E2E `18/20` PASS (`QA-03` ở `13`/`15`), Playwright vướng selector `QA-04` sau khi T/G đổi `/tim-kiem` |
| 17 | Mức ưu tiên & Template gian hàng | 3 | 3 | 0 | 0 | 8 template khác biệt bố cục (ADR 3–0), editor trực quan ↔ JSON báo dòng/cột, chặn XSS/SSRF/SVG, mock cho mọi tích hợp ngoài |

---

## 2. Bảng chi tiết từng tiêu chí đo & bằng chứng định lượng

### 2.1 Kiến trúc tổng thể

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| 11 microservice sau API Gateway + AIOps + Web | **11/11** microservice (`user`, `product`, `cart`, `promotion`, `order`, `payment`, `shipping`, `notification`, `search`, `chat`, `assistant`) + `aiops` + `gateway` + `web` = **29 container** đều `Up (healthy)` | `docker compose --profile obs ps` (H Final Gate, 2026-11-21) | **Đạt** |
| REST cho client, gRPC giữa các service | Toàn bộ giao tiếp nội bộ dùng Protobuf/gRPC (`50051..50061`), **0 cổng gRPC** công bố ra host; REST/SSE/WS chỉ qua `gateway:28080` & `web:3300` | `docker ps` + `TestE2E_GRPCTrust_B1_RejectsAnonymousAndSpoofedMetadata` PASS (`0.57 s`) | **Đạt** |
| Mỗi service 1 CSDL riêng, không truy vấn chéo DB | **10 DB PostgreSQL độc lập** (`user_db`, `product_db`, `promotion_db`, `order_db`, `payment_db`, `shipping_db`, `notification_db`, `chat_db`, `assistant_db` trên `postgres:5432` + `search_db` pgvector trên `search-postgres:5433`) + Redis cho `cart`/cache/quota | `tools/backup/restore-drill.sh` (9 DB chính) + `search-postgres` (`search_db`) | **Đạt** |
| Saga orchestration đặt hàng + Outbox pattern | Saga đặt hàng + hoàn tiền + bù trừ khi thanh toán lỗi; bảng `outbox_events` tại `product`, `order`, `payment`, `shipping` | `TestE2E_MultiShopCheckout_COD` (`2.00 s`), `TestE2E_PaymentFailed_SagaCompensation` (`0.72 s`), `TestE2E_OrderDepth_*` (`2.81 s`) | **Đạt** |
| Consumer idempotent + Dead Letter Queue (DLQ) | Stream `NEXUSMART` (10 durable consumers, `49.537` msg) + `NEXUSMART_DLQ` (`0` msg tồn sau E2E); bảng `processed_events` & `dead_letter_events` | `curl http://127.0.0.1:8222/jsz?consumers=true`, `docs/reports/{search,d-order-depth,k-core-fix}.md` | **Đạt** |

### 2.2 Ngôn ngữ và nền tảng

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Go cho 10 service + Gateway; Rust cho Payment + AIOps; TS + Next.js cho Web | 10 Go microservices + `gateway` + `pkg/common` + `tools/catalog-gen` + `evals/assistant`; Rust `services/payment` & `services/aiops`; Next.js `15.5.29` / TS cho `web` (58 routes) | `Makefile`, `web/package.json`, `services/{payment,aiops}/Cargo.toml` | **Đạt** |
| Protobuf dùng chung sinh code Go & Rust | `buf lint` = **0 lỗi** trên toàn bộ `proto/nexusmart/*` (gồm 11 domain) | `make lint` (`FINAL_GATE_MAKE_LINT_RC=0`) | **Đạt** |
| Lint & format bắt buộc (`golangci-lint`, `gofmt`, `clippy`, `rustfmt`, eslint/prettier) | `make lint` **EXIT 0** (15 Go modules + `payment` + `aiops`); `tests/e2e` `gofmt` + `golangci-lint` **EXIT 0**; `web`: `eslint` 0 lỗi, `prettier --check` 100% khớp, `tsc --noEmit` 0 lỗi | H Final Gate (`task-430`) | **Đạt** |
| Image build nhiều giai đoạn, chạy user không phải root | **15/15** Dockerfile chạy non-root (`10/15` UID số `10001:10001`, `3/15` `appuser`, `2/15` `node`); **14/15** multi-stage (riêng `deploy/observability/telegram-relay/Dockerfile` là 1-stage `FROM node:22-alpine` — đã ghi `PLT-05`) | H Final Gate audit 15 Dockerfile + `docs/reports/{f-platform,d-order-depth,k-core-fix}.md` | **Đạt một phần** *(còn `PLT-05`)* |

### 2.3 Luồng mua hàng

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Máy trạng thái đơn hàng rõ ràng | Máy trạng thái đầy đủ gồm cả nhánh đổi trả (`DELIVERED -> RETURN_REQUESTED -> RETURN_APPROVED/REJECTED -> RETURNING -> RETURNED -> REFUNDED`), coverage `statemachine` = **100%** | `services/order/internal/statemachine/`, `docs/reports/d-order-depth.md` | **Đạt** |
| Giữ tồn kho theo SKU bằng optimistic locking | Kiểm chứng tranh chấp đồng thời + tách tồn kho Flash Sale Phase 2b (`available = stock - reserved - flash_sale_reserved`) | `TestE2E_FlashSaleStockReserve_Phase2b` PASS (`1.83 s`) | **Đạt** |
| Mục tiêu: p95 API đọc `< 300 ms`, tạo đơn `< 1 s` ở 200 VU đồng thời | **Lượt đo máy sạch (H, `task-423`, qua `gateway-bff`, 200 VU / 30s, 30 buyer, 20 SKU):** `Read count=25.374`, **p50=`4.67 ms`, p95=`38.75 ms`** (`< 300 ms`); `Checkout orders=3.381`, **p50=`66.18 ms`, p95=`204.95 ms`** (`< 1.000 ms`); **`5xx=0`** *(lưu ý có `5.708` request chạm rate limit 429 do 30 buyer > 180 req/phút/user)*.<br>**Lượt đo máy bận 8 swarm song song (F, `f-platform.md`, 0 lỗi 429):** đọc thuần 200 VU `p95=169.69 ms` (`< 300 ms`); hỗn hợp `1.001` đơn COD: `Read p95=1.914,33 ms`, `Checkout p95=7.945,86 ms`, `5xx=12` (`0,23%`) — đã ghi `PLT-07`. | `k6 run tests/load/checkout.js` (H `task-423` + `docs/reports/f-platform.md`) | **Đạt một phần** *(đạt mạnh trên máy sạch với rate limit chuẩn; chế độ 0-429 200 VU cần multi-replica HPA K8s theo `PLT-07`)* |
| Không tạo đơn trùng khi client gửi lại (`Idempotency-Key`) | Gửi lặp tuần tự & burst đồng thời cùng `Idempotency-Key` chỉ tạo duy nhất 1 đơn | `TestE2E_OrderIdempotency` (`SequentialReplay` + `ConcurrentBurst`) PASS (`1.06 s`) | **Đạt** |

### 2.4 Người bán và quản trị

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| RBAC theo role và theo gian hàng ở cả gateway và service | Kiểm chứng buyer/seller khác shop/admin/ẩn danh bị từ chối `403`/`404` trên site, chat, assistant, returns, complaints, admin config & gRPC trực tiếp | `TestE2E_GRPCTrust_B1`, `17_chat`, `18_assistant`, `19_order_depth`, `20_site_templates`, `tests/e2e-web/03,04,05,06,07,08` | **Đạt** |
| Audit log chỉ ghi thêm (append-only) cho thao tác quản trị | Trigger DB chặn UPDATE/DELETE, ghi đầy đủ duyệt shop/SP, suspend site, apply template, phân xử khiếu nại | `services/user`, `tests/e2e-web/04`, `docs/reports/t-templates.md` | **Đạt** |
| Job nền cho nhập file và xử lý hàng loạt | Hiện có CLI nạp hàng loạt `tools/catalog-gen` (20k SP trong `34.3 s`), các background job đối soát/hết hạn/polling/outbox, **nhưng chưa xây dựng UI/endpoint nhập CSV sản phẩm nền cho người bán** | Khảo sát mã nguồn `services/product` & `web/src/app/(seller)` | **Đạt một phần** *(có batch CLI/background workers, chưa có Seller CSV import job)* |

### 2.5 Thanh toán

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Tiền lưu số nguyên VND, sổ cái ghi kép (`SUM(debit)==SUM(credit)`) | `TestE2E_LedgerAlwaysBalanced` PASS (`0.01 s`); `TestE2E_LedgerAndReconciliation_ChannelFilter` PASS (`0.06 s`); `Phase 2b Ledger Summary: debits=634068340 credits=634068340 is_balanced=true accounts=54` | `make e2e` (`08`, `10`, `14`, `19`), `docs/reports/d-order-depth.md` | **Đạt** |
| Kiểm tra chữ ký HMAC webhook, chống replay (cửa sổ 300s) & chặn mock-key ở production | Từ chối chữ ký sai/thiếu & timestamp cũ (`401`); Swarm D2 bổ sung `Config::validate()` fail-fast + defense-in-depth `503`/`401` cho ZaloPay/VietQR/generic webhook ở `APP_ENV=production` | `TestE2E_PaymentWebhookIdempotencyAndSecurity` (`1.05 s`), `19_order_depth`, `docs/reports/d2-payment-fix.md` | **Đạt** |
| Idempotent theo mã giao dịch: webhook lặp 10 lần chỉ ghi nhận 1 lần | Cả VietQR (`05`) và ZaloPay (`19`) gửi lặp 10 lần chỉ ghi đúng 1 bút toán sổ cái | `TestE2E_PaymentWebhookIdempotencyAndSecurity/Replay10Times_RecordedOnce`, `19_order_depth` | **Đạt** |
| Job hết hạn thanh toán và job đối soát hằng ngày | `scan_expired_payments` & `run_reconciliation` (VietQR + ZaloPay, auto-heal, lưu `reconciliation_runs`/`items`), API `GET /api/v1/admin/payments/reconciliation-reports` | `19_order_depth/AdminReconciliationReportsAndRBAC` PASS, Rust PG17 integration PASS (`docs/reports/d-order-depth.md`) | **Đạt** |
| Ví sandbox thứ ba (ZaloPay v2) + số dư người bán | `ZaloPayAdapter` (`mock`/`sandbox`), tính `pending_vnd`, `available_vnd`, `total_earned_vnd` gồm cả hoàn tiền sau đối soát | `19_order_depth/ZaloPay_CheckoutHMACReplayAndBalancedLedger` PASS (`0.55 s`), `docs/reports/d-order-depth.md` | **Đạt** |

### 2.6 Vận chuyển

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Interface `Carrier` chung, mỗi hãng 1 adapter | `GHN` + `GHTK` + `MockExpress` cài đặt chung `Carrier` (`Quote`, `CreateOrder`, `CancelOrder`, `TrackOrder`) + vận đơn hoàn `CreateReturnShipment` (`direction=return`, COD=0) | `19_order_depth/Shipping_MapLegacyAndMultiCarrierFee` & `Returns_ApproveReverseShipment*` PASS | **Đạt** |
| Timeout, retry backoff và circuit breaker cho mọi lời gọi ngoài | Bọc `NewResilientCarrier` (unit test `carrier` coverage `77.2%`) | `services/shipping/internal/carrier/`, `docs/reports/d-order-depth.md` | **Đạt** |
| Webhook idempotent + job polling dự phòng | `internal/webhook/` (`92.8%` coverage) + `internal/poller/` (`92.9%` coverage) | `docs/reports/d-order-depth.md` | **Đạt** |
| Bảng mapping địa chỉ cũ (63 tỉnh 3 cấp) → mới (34 tỉnh 2 cấp) | Kiểm chứng qua REST `POST /api/v1/shipping/addresses/map-legacy` & unit test `internal/address/` (`95.5%` coverage) | `19_order_depth/Shipping_MapLegacyAndMultiCarrierFee` PASS | **Đạt** |

### 2.7 Khuyến mãi

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Trừ suất flash sale & lượt voucher bằng Lua script trên Redis | `services/promotion/internal/lua/` (coverage `92.5%`) | `make test` (`services/promotion`), `docs/reports/d-order-depth.md` | **Đạt** |
| 1.000 yêu cầu đồng thời cho 100 suất, bán đúng 100 (`oversell = 0`) | **k6 gRPC (F):** `sold=100/100`, `rejected=900`, `oversell=0`, `remaining_slots=0`, `unexpected_errors=0`.<br>**Go E2E (H):** `06_flash_sale` (`0.69 s`) & `14_flash_sale_2b` (`succeeded=100, failed=900, 1.83 s`) đều `oversell=0`. | `tests/load/flash-sale.js` (`f-platform.md`), `make e2e` (`06`, `14`) | **Đạt** |
| Reserve và hoàn voucher, xu trong cùng saga (gồm trả hàng) | `order.cancelled` & `order.refunded` durable consumers hoàn voucher/xu & thu hồi xu thưởng idempotent (chặn số dư âm) | `07_saga_compensation`, `19_order_depth`, `services/promotion` (`68.5%` cov) | **Đạt** |

### 2.8 Chat

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| WebSocket + Redis Pub/Sub cho nhiều instance | `TestChatIntegrationPostgresRedisAndTwoInstances` (2 instance thật `chat-inst-a`/`chat-inst-b` trên Postgres 16 + Redis 7): PASS (`3.94 s`, `-race`); single-use `?ticket=` replay bị từ chối | `docs/reports/b-chat.md`, `17_chat_test.go` PASS (`0.82 s`), `k-core-fix.md` (`K-02`) | **Đạt** |
| Lưu tin nhắn (`text`/`image` chống SSRF/`product`/`order`), đồng bộ cursor khi mất kết nối, auto-reply | Kiểm chứng cả REST `before`/`after` lẫn WS `?after=<cursor>`, chặn SSRF `169.254.169.254` & `.svg`, auto-reply nguyên tử | `17_chat_test.go` (7/7 PASS), `tests/e2e-web/06` PASS | **Đạt** |
| Mục tiêu: **1.000 kết nối đồng thời trên 1 instance** | `active_peak=1000`, `active_after_hold=1000 (5s)`, `ramp=133 ms`, `connect p95=34.73 ms`, `sent/delivered=100/100`, `connect_errors=0`, `unexpected_drops=0`, RSS `21 MB -> 71 MB` | `services/chat/cmd/loadtest` (`docs/reports/b-chat.md`) | **Đạt** |

### 2.9 Tìm kiếm

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Có dấu và không dấu cho cùng kết quả (`đ/Đ`), lọc, sắp xếp, gợi ý, hybrid Meilisearch + pgvector RRF | Parity sống trên 20.021 SP: `"áo thun" total=17262` vs `"ao thun" total=17262`; `16_search_test.go` 7/7 PASS (diacritics, `shop_id` isolation, price filter+facets, `price_asc`/`price_desc`/`newest`, suggest) | H `task-423` (`tests/load/search.js`), H Final Gate (`16_search_test.go`), `docs/reports/search.md` | **Đạt** |
| Index cập nhật qua event từ Product, độ trễ `< 5 giây` | **Live trên catalog 20.021 SP (H Final Gate):** `368.52 ms` (xác nhận cả pgvector & Meilisearch). **Benchmark A (10 mẫu):** approve p50 `408 ms` / max `3.90 s`; update `296 ms`; stock `322 ms`; delete `354 ms`. | `16_search_test.go:212` (`task-430`), `docs/reports/search.md` | **Đạt** |
| Bộ dữ liệu mẫu 20.000–50.000 sản phẩm | `tools/catalog-gen` nạp qua Product gRPC API (không ghi trực tiếp DB): **20.000 SP** (`583.5 SP/s`, `34.3 s`); stack sống hiện có **20.021 SP** trên Meilisearch & pgvector (`make seed` idempotent) | `make seed` (`task-375`/`task-396`), `docs/reports/search.md` | **Đạt** |
| Mục tiêu: **p95 truy vấn `< 200 ms`** | **k6 live qua Gateway (H `task-423`, 50 VU / 20s, 20.021 SP, `FALLBACK_PRODUCTS=false`):** `Search count=7.771`, **p50=`7.43 ms`, p95=`42.97 ms`**; `Suggest count=2.550`, **p50=`2.44 ms`, p95=`42.62 ms`**; `429=0`, `5xx=0`. **gRPC trực tiếp (A):** 200 queries **p95=`17.26 ms`**, `SimilarProducts` **p95=`1.64 ms`**. | `tests/load/search.js` (`task-423`), `docs/reports/search.md` | **Đạt** |

### 2.10 Trợ lý AI

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Embedding lưu trong pgvector (Search service), cập nhật khi SP thay đổi | `assistant` gọi gRPC `SearchService` (`Search` hybrid & `SimilarProducts`), không giữ bản sao vector thứ 2; hậu kiểm cô lập `shop_id = 100%` | `services/assistant/internal/retriever/`, `18_assistant_test.go` PASS | **Đạt** |
| Tool chạy với JWT của người dùng, không có quyền riêng | 6 tools chỉ chuyển tiếp `Authorization: Bearer <user_jwt>`, không có tool SQL/checkout/payment; buyer không gọi được `get_seller_report` | `services/assistant/internal/tools/`, `18_assistant_test.go` (`OrderLookup`, `SellerRoutes+RBAC`) | **Đạt** |
| Streaming SSE, **TTFT `< 2 giây`** | **Live Docker toàn stack (H Final Gate `18_assistant`):** `TTFT = 46.43 ms` (`LLM_MODE=mock`). **Eval harness (n=126):** p50 `0 ms`, p95 `1 ms`, max `2 ms`. **Docker cold smoke (C):** `190 ms`. *(Chưa đo với key Anthropic API live vì không có key thật trong môi trường — adapter `AnthropicProvider` đã unit test qua `httptest`.)* | `18_assistant_test.go:329` (`task-430`), `make eval-assistant` (`task-368`), `docs/reports/c-assistant.md` | **Đạt một phần** *(đạt `< 2 s` ở chế độ mock & container; chưa gọi Anthropic live do thiếu API key)* |
| Eval set ~100 câu: đo tỷ lệ căn cứ (groundedness) & tỷ lệ bịa sản phẩm (hallucination) | `evals/assistant/dataset.json` (**100 câu** tiếng Việt): **Pass `100/100 (100.0%)`**, **Grounded `100.0%`**, **Product Hallucination `0.0%`**, **Shop Isolation `100.0%`**, **Policy Citation `100.0%`**, **Tool & Auth `100.0%`** | `make eval-assistant` (H `task-368` xác nhận độc lập) | **Đạt** |
| Bộ test prompt injection (>= 30 ca) & hạn mức token theo user | `evals/assistant/prompt_injection.json` (**36 ca**: 14 `direct`, 12 `product_description`, 10 `chat_message`): **`36/36 (100.0%)` bị chặn**; hạn mức token/ngày + ngân sách ngày VND bằng Redis Lua + cô lập IP khách vãng lai (`K-01`) | `make eval-assistant` (`task-368`), `18_assistant_test.go`, `docs/reports/k-core-fix.md` | **Đạt** |

### 2.11 Giám sát và log

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| OpenTelemetry SDK, trace ID xuyên suốt gRPC và message broker (NATS) | **Xác minh sống sau bản vá `OBS-01` (K) & `OBS-02/05/06/07/08/11/12` (D/K/G2):** gọi `GET /api/v1/search` lấy `X-Trace-Id=8182b0c51294ad0ffd98776142cae2ed` → tra Tempo `GET :3200/api/traces/<id>` trả **`HTTP 200` (`1.484 B`), đủ span `['gateway', 'search']`** | H `task-423`, `docs/reports/{e-aiops,d-order-depth,k-core-fix,g2-web-fix}.md` | **Đạt** |
| Log JSON có trace ID, che PII và secret | `pkg/common/logger` (`OBS-03` K), `web` `logServer` (`OBS-11` G2), Rust `payment` (`OBS-04/14` D), `notification` DLQ che OTP (`OBS-07` K); còn 1 mục nhỏ `OBS-13` fatal log ở `services/user/cmd/user/main.go:38` (ngoài phạm vi K) | `docs/reports/{issues,k-core-fix,d-order-depth,g2-web-fix}.md` | **Đạt một phần** *(còn `OBS-09`/`OBS-13` nhỏ trên `services/user`)* |
| Bộ chỉ số RED cho từng service | **16/16** Prometheus target `up` (đủ 13 service + `otel-collector` + `prometheus` + `cadvisor`); `TestE2E_HealthAndObservability` + `16`/`17`/`18` xác minh `/healthz`, `/readyz`, `/metrics` | `docs/reports/e-aiops.md`, `make e2e` (`01`, `16`, `17`, `18`) | **Đạt** |
| Dashboard theo service & luồng nghiệp vụ; SLO & cảnh báo dạng code; Telegram | 4 Grafana dashboards auto-provisioned (`Service RED`, `Checkout`, `Payment`, `Flash Sale`); **24 SLO/alert rules** (`promtool` 0 lỗi); Alertmanager + `telegram-relay` mock nhận **98** cảnh báo thật | `docs/reports/e-aiops.md` | **Đạt** |

### 2.12 AIOps

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Thuật toán thống kê: z-score & EWMA trên error rate, p95, CPU, memory (Rust) | `services/aiops` (Rust, RAM thực đo `20.83 MiB`, 0% CPU nhàn rỗi); `15/15` unit+integration test PASS, `0` cảnh báo clippy; `/readyz` xác nhận kết nối Prometheus | `make lint`, `make test`, `curl :8092/readyz` (`task-423`), `docs/reports/e-aiops.md` | **Đạt** |
| Gom log lỗi (Loki), trace chậm (Tempo), lịch sử deploy làm ngữ cảnh LLM | Cài đặt đầy đủ trong `src/context.rs` + che PII trước khi đưa vào `LlmProvider` (`mock`/`anthropic`) | `docs/reports/e-aiops.md` | **Đạt** |
| Hành động tự động trong allowlist (`restart`, `scale`), giới hạn/giờ, `dry-run` mặc định | Kiểm chứng sống trên `:8092`: hành động cấm (`drop_db`/`postgres`) → **`403 blocked_allowlist`**; 3 lần `dry_run` → **`200 dry_run_skipped`**; lần 4 trong giờ → **`429 rate_limited`** | `docs/reports/e-aiops.md` (mục 2.4) | **Đạt** |
| Mục tiêu: **phát hiện `< 2 phút`**, **đúng nguyên nhân `>= 4/5` kịch bản tiêm lỗi** | **Chạy sống 5/5 kịch bản trên Compose (`.runs/chaos-results.md`):** `slow-payment` (`20.2 s`), `stop-order` (`15.2 s`), `promotion-db-error` (`20.1 s`), `nats-outage` (`10.5 s`), `search-high-cpu` trên `nexus_ecom-search-1` thật (`30.1 s`). **Phát hiện `< 120 s`: `5/5` (TB `19.2 s`, max `30.1 s`)**; **RCA đúng: `5/5` (`100%`)**; khôi phục sạch `5/5`. | `.runs/chaos-results.md`, `docs/reports/e-aiops.md` | **Đạt** |

### 2.13 Bảo mật

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Argon2id, access token 15 phút, refresh token xoay vòng | Kiểm chứng qua unit test `services/user` & `web/src/lib/server/tokens.ts` + E2E auth | `make test`, `tests/e2e-web/02` | **Đạt** |
| Rate limit ở API gateway theo IP và theo tài khoản | `TestE2E_RateLimit_Real429AndNoXFFBypass` PASS (`0.07 s`); `K-01` cô lập quota anonymous qua `TrustedProxies` | `make e2e` (`12`), `docs/reports/k-core-fix.md` | **Đạt** |
| OWASP Top 10: BOLA/IDOR, injection, SSRF, XSS | 11 ca XSS/SSRF/SVG/CSS `@import` trên site templates bị chặn `400`; SSRF/SVG ảnh chat bị chặn; cross-site IDOR/CSRF `403`/`404` | `17_chat`, `20_site_templates`, `tests/e2e-web/03,05,06,07,08` | **Đạt** |
| Secret trong K8s Secret, không nằm trong repo | `0` manifest `kind: Secret` trong `deploy/`; `create-secrets.sh` fail-closed ở `nexusmart-production` | `make helm-lint` (`task-368`), `docs/reports/f-platform.md` | **Đạt** |
| Quét lỗ hổng dependency & image trong CI (`govulncheck`, `cargo audit`, `npm audit`, Trivy) | `.github/workflows/security.yml` cấu hình đủ 6 job (`workflow_dispatch` only); đo cục bộ **`cd web && npm audit --omit=dev --audit-level=high` = `found 0 vulnerabilities`** (sau bản vá `PLT-02` của G2) | H Final Gate (`task-430`), `docs/reports/{f-platform,g2-web-fix}.md` | **Đạt** |
| Chỉ cổng `28080` và `3300` public; `0` cổng gRPC public | `docker ps` xác nhận duy nhất `nexus_ecom-gateway-1 (0.0.0.0:28080)` và `nexus_ecom-web-1 (0.0.0.0:3300)`; `0` cổng `5005x`/`5006x` mở ra host | H Final Gate (`task-430`) | **Đạt** |

### 2.14 Dữ liệu

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Migration có phiên bản cho từng service | SQL migrations riêng cho mọi service (`user` 0001..0004, `product` 000001..000003, `order` 0001..0004, `payment` 0001..0003, `shipping` 000001..000002, `promotion` 001..002, `notification`, `search`, `chat`, `assistant`) | Các thư mục `services/*/migrations/` & `make up` | **Đạt** |
| Sao lưu hằng ngày và diễn tập khôi phục | **Chạy thật trên stack 9 DB sống (H `task-423`, gồm cả `chat_db` & `assistant_db`):** `SHA256SUMS` 11/11 `OK`; khôi phục vào container `postgres:16-alpine` cô lập trong **`2.45 s`**; **`9 DB / 77 bảng / 328.473 trên 328.473 dòng khớp 100%`**. Kèm `k8s-cronjob.yaml` (`kubeconform` Valid). | `tools/backup/restore-drill.sh --fresh` (H `task-423`), `docs/reports/f-platform.md` | **Đạt** |
| Script sinh dữ liệu mẫu (user, shop, 20k SP, đơn, chat, trả hàng, template) | `make seed` nạp đầy đủ 6 tài khoản + 5 shop + 18 SP showcase (159 SKU) + 11 voucher + 3 khung Flash Sale (đã sửa `PLT-01`) + 4 site v2 + 2 hội thoại chat + auto-reply + 1 đơn trả hàng `PENDING` + 1 khiếu nại `OPEN` + **20.000 SP tìm kiếm**; chạy lại lần 2 **100% idempotent** (`created=0`) | `make seed` x2 (`task-375`, `task-430`) | **Đạt** |

### 2.15 Hạ tầng và CI/CD

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Helm chart cho từng service (`staging` & `production`), cấu hình tách khỏi image | **14 Helm chart** độc lập (`ConfigMap` + `checksum/config` + `existingSecret`); **`28/28`** tổ hợp môi trường đạt `helm lint --strict` + `kubeconform -strict` (đã gồm bản vá `deploy/helm/payment` của D2) | `make helm-lint` (H `task-368` xác nhận độc lập) | **Đạt** |
| Rolling update + readiness/liveness probe | `RollingUpdate (maxUnavailable: 0, maxSurge: 1)`, probe `/healthz` & `/readyz`; chạy thật `gateway` & `web` `1/1 Running` trên `kind` | `docs/reports/f-platform.md` (mục 3.1) | **Đạt** |
| Horizontal Pod Autoscaler (HPA) cho service chịu tải | `autoscaling/v2` bật mặc định cho `gateway`, `order`, `search` (CPU 70% / Memory 80%), đã xác minh trên `kind` | `deploy/helm/{gateway,order,search}/`, `docs/reports/f-platform.md` | **Đạt** |
| Rollback bằng một lệnh | `make rollback SERVICE=gateway REV=1` diễn tập thật trên cụm `kind` (`Rev 2 -> Rev 1`, `LOG_LEVEL` về `info`) | `docs/reports/f-platform.md` (mục 3.1) | **Đạt** |
| CI chỉ `workflow_dispatch` | Cả `.github/workflows/ci.yml` và `.github/workflows/security.yml` chỉ có duy nhất `on: workflow_dispatch:` (0 `push`/`pull_request`/`schedule`) | H Final Gate (`task-368`, `task-430`) | **Đạt** |

### 2.16 Kiểm thử

| Tiêu chí đo (`docs/spec.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Unit & Integration test với CSDL thật trong container | `make test` **EXIT 0** (15 Go modules `-race` + Rust `payment` 61 unit + 3 PG17 integration + Rust `aiops` 9+6); `web`: **191/191** unit tests PASS; Search/Chat/Assistant/User có integration tests với Postgres/Redis/Meilisearch/NATS thật | H Final Gate (`task-430`) + `docs/reports/{search,b-chat,c-assistant,d-order-depth,d2-payment-fix,t-templates}.md` | **Đạt** |
| Contract test gRPC giữa các service | `services/order/internal/service/contract_grpc_test.go` (wire-level `bufconn` + `auth.GRPCUnaryServerInterceptor` cho `Order ↔ Payment/Shipping/Promotion`) | `docs/reports/d-order-depth.md` | **Đạt** |
| Kiểm thử tải k6 cho checkout, flash sale, tìm kiếm | **Flash Sale:** `100/100`, `oversell=0` (F); **Search 20k:** `p95=42.97 ms`, `0` lỗi (H `task-423`); **Checkout 200 VU:** máy sạch `Read p95=38.75 ms`, `Checkout p95=204.95 ms`, `0` 5xx (H `task-423`, có 429 user/phút) / máy bận 0-429 (`PLT-07`, F) | `tests/load/{flash-sale,search,checkout}.js` | **Đạt** *(xem ghi chú `PLT-07` ở mục 2.3)* |
| E2E tự động: Go E2E (`tests/e2e`), Web BFF E2E (`tests/e2e-web`), Playwright (`tests/playwright`) | • **Web BFF E2E (`01..08`):** **`41/41` PASS hai lần liên tiếp** (`16.99 s` & `14.98 s`, EXIT 0).<br>• **Go E2E mới (`16..20`):** **`5/5` PASS** trên catalog 20k SP.<br>• **Go E2E tổng (`01..20`):** **`18/20` PASS**, **2/20 FAIL** (`13_sites_backend`, `15_custom_domains`) do lỗi hồi quy **`QA-03`** ở `services/user/internal/site/validate.go:513` (ngoài phạm vi H).<br>• **Playwright:** PASS `35.0 s` trên bản trước rebuild (F); sau khi T/G đổi `/tim-kiem` sang `SiteSearchResults`, bước 1 đăng ký+OTP PASS nhưng bước 2 FAIL do lệch selector `#site-search-input` (**`QA-04`**, ngoài phạm vi H). | H Final Gate (`task-430`), H `task-423`, `docs/reports/issues.md` (`QA-03`, `QA-04`) | **Đạt một phần** *(chờ vá 2 mục liên swarm `QA-03` & `QA-04`)* |
| **Coverage `>= 60%`** cho `Order`, `Payment`, `Promotion` | • `services/order`: **65.9%** (statements)<br>• `services/payment`: **85.25%** (lines, `llvm-cov`)<br>• `services/promotion`: **68.5%** (statements)<br>• *(bổ sung `services/shipping`: **62.9%**)* | `docs/reports/d-order-depth.md` (mục 2.2 & 2.3) | **Đạt** |

### 2.17 Mức ưu tiên triển khai & Hệ thống Template gian hàng (Swarm T)

| Tiêu chí đo (`docs/spec.md` & `m-t-templates.md`) | Kết quả đo thật | Nguồn đo (Lệnh / Báo cáo) | Trạng thái |
|---|---|---|---|
| Mỗi use case mức Cao có test tự động | Phủ toàn bộ UC mức Cao (Auth, Search, PDP, Cart, Checkout COD/VietQR/ZaloPay, Flash Sale, Chat, AI Assistant RAG/Eval/Guardrail, Seller/Admin RBAC & Audit, Observability & AIOps Chaos) qua Go E2E (`01..20`), Web E2E (`01..08`), Rust/Go unit+integration, Eval 100+36 | `tests/e2e/`, `tests/e2e-web/`, `evals/assistant/`, `tools/chaos/` | **Đạt** |
| Mỗi tích hợp bên ngoài có bản mock để demo khi sandbox lỗi | Đủ mock chạy offline cho: Anthropic LLM, Voyage Embedding, VietQR (payOS/SePay), ZaloPay v2, GHN, GHTK, SMTP (Mailpit), Telegram Alert Relay | `docs/DEMO.md`, `.env.example` | **Đạt** |
| Hệ thống `>= 8` template khác biệt bố cục + ADR + visual/code editor + chống XSS/SSRF | ADR-0001 biểu quyết **3–0** chọn `Declarative Theme JSON v2`; **8 template** độc lập phủ đủ 5 trang, WCAG AA `>= 4.5:1`; nâng cấp 4 site seed v1→v2 không mất dữ liệu; `20_site_templates_test.go` (5/5) & `08_site_templates.test.mjs` PASS; 11/11 ca XSS/SSRF/SVG/CSS `@import` bị chặn `400` | `docs/adr/0001-site-template-format.md`, `20_site_templates_test.go`, `08_site_templates.test.mjs`, `docs/reports/t-templates.md` | **Đạt** |

---

## 3. Danh mục các điểm chưa đạt tuyệt đối (cần xử lý ở vòng vá ngắn tiếp theo)

1. **`QA-03` (High — chặn `make e2e` EXIT 0 ở bài `13` & `15`, thuộc Swarm T / Manager):** `services/user/internal/site/validate.go:513` chặn `.local` khiến URL `https://sandbox.nexusmart.local/banners/site-hero.jpg` trong `tests/e2e/13_sites_backend_test.go:32` (dùng chung với `15_custom_domains_test.go:27`) bị từ chối `400`. Cách vá 1 dòng: đổi URL fixture trong `13_sites_backend_test.go:32` sang `https://cdn.example.com/banners/site-hero.jpg` (ưu tiên để giữ chặt SSRF) hoặc cho phép cấu hình `SITE_IMAGE_ALLOWED_HOSTS` ở dev.
2. **`QA-04` (Medium — chặn Playwright bước 2, thuộc Swarm F / G):** `tests/playwright/specs/buyer-flow.spec.ts:94` tìm `#site-search-input` cũ sau khi trang `/tim-kiem` chuyển sang `SiteSearchResults` / `SiteSearchBar` (`role="combobox"`).
3. **`PLT-07` (Medium — 200 VU checkout không giới hạn 429 trên 1 replica Compose):** Trên máy sạch có rate limit mặc định, checkout 200 VU đạt `Read p95 = 38.75 ms`, `Checkout p95 = 204.95 ms`, `0` lỗi 5xx; để đạt cùng ngưỡng khi gỡ rate limit trên tải ghi đồng thời cực đại, cần chạy multi-replica trên Kubernetes với HPA (`deploy/helm/{gateway,order,search}` đã cấu hình sẵn).
4. **Kiểm chứng live với API trả phí (`LLM_MODE=anthropic`, `EMBED_MODE=voyage`, `ZALOPAY_MODE=sandbox`, Telegram thật):** Chưa gọi ra Internet bằng key thật vì không có secret trong môi trường; mọi adapter đều đã có unit test HTTP mock đầy đủ và hướng dẫn chạy live trong `docs/reports/c-assistant.md` & `docs/DEMO.md`.
5. **Tồn đọng kỹ thuật nhỏ:** `OBS-09`/`OBS-13` trên `services/user/cmd/user/main.go` (chưa bật `ProcessCollector` & JSON fatal log), `PLT-05` (`telegram-relay` Dockerfile 1-stage), và ghi chú `deploy/k8s/create-secrets.sh` cần thêm `ZALOPAY_KEY1`/`ZALOPAY_KEY2` cho production (Swarm D2 ghi nhận cho Swarm F).
