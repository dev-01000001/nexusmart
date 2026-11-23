# Báo cáo rà soát nghiệp vụ — Worker `plt` (Nền tảng xuyên suốt & mảng chưa có service)

- **Commit rà soát**: `0db2cddedf77ad3f326a3a4b0dbad459fd6ca843` (HEAD)
- **Stack kiểm chứng**: compose project `nexusmart-audit` (Gateway `:48080`, Web `:43000`, Mailpit `:48025`, NATS `:48222`, OTel Prom `:48889`, ops service `:48081..:48088`)
- **Phạm vi**: 28 UC (`UC-AI01..14`, `UC-B14`, `UC-S09`, `UC-S06`, `UC-A06`, `UC-OPS01..09`, phần gateway & bảo mật xuyên suốt của `UC-B02`), 9 nhóm đối chiếu Shopify/sàn, đối chiếu 11 microservice + hạ tầng/quan sát/DevOps/Docker/secret.

---

## 0. Đối chiếu 11 microservice theo đặc tả và nhu cầu ADR

Spec (`docs/spec.md:23-24,162-175`) định nghĩa **API Gateway + 11 microservice nghiệp vụ** và **1 AIOps engine nền (Rust)**:

| # | Thành phần | Ngôn ngữ (spec) | Trạng thái HEAD | Vị trí / Ghi chú | ADR cần chốt trước khi code (`AGENTS.md` luật 11, `docs/adr/README.md`) |
| --- | --- | --- | --- | --- | --- |
| 0 | `gateway` | Go | **ĐÃ CÓ** | `services/gateway` (`:8080`) | `ADR-0001` (đã đề xuất, chờ duyệt: giữ facade + thêm edge OSS) |
| 1 | `user` | Go | **ĐÃ CÓ** | `services/user` (`:50051/:8081`, `user_db` + Redis) | `ADR-0002` (đã đề xuất, chờ duyệt: giữ auth + lên EdDSA/JWKS) |
| 2 | `product` | Go | **ĐÃ CÓ** | `services/product` (`:50052/:8082`, `product_db`) | Cần thêm event `product.*` sang NATS (dùng lại NATS sẵn có) |
| 3 | `cart` | Go | **ĐÃ CÓ** | `services/cart` (`:50053/:8083`, Redis) | Không |
| 4 | `promotion` | Go | **ĐÃ CÓ** | `services/promotion` (`:50054/:8084`, `promotion_db` + Redis) | Không |
| 5 | `order` | Go | **ĐÃ CÓ** | `services/order` (`:50055/:8085`, `order_db`) | `ADR-0003` (đã đề xuất, chờ duyệt: giữ saga/outbox tự viết) |
| 6 | `payment` | Rust | **ĐÃ CÓ** | `services/payment` (`:50056/:8086`, `payment_db`) | Không |
| 7 | `shipping` | Go | **ĐÃ CÓ** | `services/shipping` (`:50057/:8087`, `shipping_db` + Redis) | Không |
| 8 | `notification` | Go | **ĐÃ CÓ** | `services/notification` (`:50058/:8088`, `notification_db`) | Không |
| 9 | **`search`** | Go | **THIẾU** | Chưa có `services/search`, `proto/nexusmart/search/`. Web `/tim-kiem` dùng client-side `web/src/lib/search.ts` trên ≤100 SP. | **Bắt buộc ADR (Nhóm B)**: chọn search engine (Meilisearch vs OpenSearch) + chiến lược hybrid vector |
| 10 | **`chat`** | Go | **THIẾU** | Chưa có `services/chat`, `proto/nexusmart/chat/`. Web chỉ có mock `BuyerChatPanel.tsx` & `/seller/chat`. | **Bắt buộc ADR (Nhóm B)**: Object storage cho ảnh chat (SeaweedFS/Garage) + Ingress hỗ trợ WebSocket (`ADR-0001`/nơi triển khai) |
| 11 | **`ai-assistant`** | Go | **THIẾU** | Chưa có `services/ai-assistant`, `proto/nexusmart/ai/`, `pgvector`. Web chỉ có mock `AiAssistantPanel.tsx`. | **Bắt buộc ADR**: Vector store (`pgvector` / đổi image `pgvector/pgvector:pg16` hay DB riêng) + LLM/Embedding API & guardrail ngân sách |
| + | **`aiops`** (engine nền) | Rust | **THIẾU** | Chưa có `services/aiops`, không có z-score/EWMA, runbook, Telegram alert, chaos test. | **Bắt buộc ADR (Nhóm B)**: Nơi triển khai (K8s tự host vs cloud) + Observability backend (Prometheus/Loki/Tempo/Grafana) + Alertmanager/Telegram |
| + | **Analytics** (thuộc `order`/`user`/`product`) | Go | **MỘT PHẦN** | Không phải service riêng theo spec; chưa có RPC báo cáo/bảng tổng hợp ngày trong `order.proto`. `/seller`, `/admin` tự cộng client-side trên 50–100 đơn. | Không cần hạ tầng mới nếu dùng bảng tổng hợp/materialized view trên `order_db` (Data warehouse nằm ngoài phạm vi `docs/spec.md:36`) |

> **ADR chặn đầu (`docs/adr/README.md:3`)**: phải chốt **ADR Nơi triển khai (Kubernetes tự host hay cloud)** trước khi chốt các ADR Nhóm B phụ thuộc (Ingress/Load Balancer + TLS, Helm/Argo CD, Observability backend Prometheus/Loki/Tempo/Grafana, Object storage cho media & backup WAL).

---

## 1. Bảng UC phụ trách (28 UC)

| UC | Ưu tiên | Trạng thái | Backend (`file:dòng`) | REST | Web | Bằng chứng | Ghi chú |
| --- | --- | --- | --- | --- | --- | --- | --- |
| UC-AI01 | Cao | THIẾU | Không có (`services/`, `proto/nexusmart/`) | Không có (`services/gateway/internal/router/router.go`) | Mock: `web/src/features/commerce/AiAssistantPanel.tsx:55-391` | `ls services/ proto/nexusmart/` chỉ có 8 domain; `rg -i 'pgvector\|embedding\|openai\|anthropic'` 0 kết quả trong `services/`; `AiAssistantPanel` giả lập streaming bằng `setInterval` (dòng 368) trên `PRODUCTS` tĩnh | Cần `ai-assistant` + `pgvector` + LLM API + SSE + ADR |
| UC-AI02 | Cao | THIẾU | Không có (`services/search` chưa tồn tại) | Chỉ có `GET /api/v1/products?keyword=` (`Product.ListProducts` `ILIKE`) | Mock/client: `web/src/lib/search.ts:1-230`, `web/src/app/(shop)/tim-kiem/` | `ls services/` không có `search`; `rg -i 'pgvector\|meilisearch\|elasticsearch\|opensearch'` 0 kết quả code; `web/src/lib/search.ts` lọc đồng nghĩa/bỏ dấu client-side trên ≤100 SP | Chưa có Search service, chưa có hybrid vector search |
| UC-AI03 | Trung bình | THIẾU | Không có | Không có | Mock: `web/src/features/commerce/AiAssistantPanel.tsx:64-97,203-238,353` | Không có RPC/route AI; `AiAssistantPanel.tsx:353` truyền `INITIAL_ORDERS` tĩnh cho mọi user đăng nhập, không gọi `OrderService` với JWT | Lệch hiển thị `PLT-D1` (hiện đơn mẫu của demo user) |
| UC-AI04 | Trung bình | THIẾU | Không có | Không có | Không có (`AiAssistantPanel.tsx` chỉ hiển thị link `/san-pham/[slug]`, không có tool `addToCart` & xác nhận) | Đọc `web/src/features/commerce/AiAssistantPanel.tsx:532-562`; `rg -n 'addToCart' web/src/features/commerce/AiAssistantPanel.tsx` 0 kết quả | Chưa có function calling thêm giỏ |
| UC-AI05 | Trung bình | THIẾU | Không có | Không có | Không có (PDP chỉ hiện `SAMPLE_REVIEWS` mock, không có bản tóm tắt AI) | `rg -i 'summary\|tóm tắt' web/src/features/product/ services/` không có bản tóm tắt đánh giá AI | Phụ thuộc thêm tính năng đánh giá (`UC-B10`) |
| UC-AI06 | Thấp | THIẾU | Không có | Không có | Không có (`web/src/features/seller/ProductFormView.tsx`) | `rg -i 'sinh mô tả\|generate.*desc' web/src/ services/` 0 kết quả | Mức Thấp (không bắt buộc cài đặt theo `docs/spec.md:39`) |
| UC-AI07 | Thấp | THIẾU | Không có | Không có | Mock rule tĩnh: `PdpRecommendationsSection` theo danh mục/chất liệu | Không có service embedding hành vi xem/mua | Mức Thấp |
| UC-AI08 | Thấp | THIẾU | Không có | Không có | Không có | `rg -i 'image.*embed\|tìm.*ảnh' web/src/ services/` 0 kết quả | Mức Thấp |
| UC-AI09 | Thấp | THIẾU | Không có | Không có | Không có (không có panel AI ở `(seller)`) | `ls web/src/app/(seller)/` không có route/panel AI | Mức Thấp |
| UC-AI10 | Trung bình | THIẾU | Không có | Không có | Mock tĩnh: `web/src/app/(seller)/seller/chat/page.tsx:21-34` (`QUICK_REPLIES` 3 mẫu cứng) | Đọc `seller/chat/page.tsx:21-34`: không gọi AI, không ghi nhận tỷ lệ dùng gợi ý | Chưa có Chat + AI gợi ý |
| UC-AI11 | Trung bình | THIẾU | Không có | Không có | Mock: `web/src/features/commerce/AiAssistantPanel.tsx:130-136` | Trả chuỗi cứng chính sách 45 ngày + link `/tro-giup`, không RAG embedding tài liệu | Chưa có RAG chính sách |
| UC-AI12 | Trung bình | THIẾU | Không có | Không có | Mock: `/tro-giup` lưu `SupportTicket` cục bộ trong `MockStoreContext.tsx:60-73` | Không có service ticket / chuyển tiếp nhân viên trong khung chat AI | Không chuyển được cho CSKH thật |
| UC-AI13 | Cao | THIẾU | Không có | Không có | Không có nút hữu ích/không hữu ích trong `AiAssistantPanel.tsx` | `rg -i 'eval\|hallucin\|hữu ích' services/ web/src/` không có eval set hay lưu hội thoại | Thiếu bộ eval 100 câu bắt buộc (Cao) |
| UC-AI14 | Cao | THIẾU | Không có | Không có | Không có | `rg -i 'prompt.?injection\|token.?budget\|guardrail' services/ web/src/` 0 kết quả | Thiếu guardrail & hạn mức token LLM (Cao) |
| UC-B14 | Trung bình | THIẾU | Không có (`services/chat` chưa tồn tại) | Không có WebSocket route ở `router.go` | Mock: `web/src/features/commerce/BuyerChatPanel.tsx:20-338`, `web/src/data/chat.ts` | `rg -i 'websocket\|pubsub\|coder/websocket' services/ web/src/` 0 kết quả; `BuyerChatPanel.tsx:150` dùng `setTimeout(850ms)` giả lập trả lời shop trong RAM | Không gửi ảnh/mã đơn thật, không cảnh báo SĐT/link ngoài sàn, không đồng bộ đa thiết bị (`PLT-D2`) |
| UC-S09 | Trung bình | THIẾU | Không có (`services/chat` chưa tồn tại) | Không có | Mock: `web/src/app/(seller)/seller/chat/page.tsx:51-363` | `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:43000/seller/chat` → `307 /dang-nhap?next=%2Fseller%2Fchat`; code đọc `useMockStore` + `sellerStore` cục bộ, không có cài đặt ngoài giờ/chặn spam/tỷ lệ phản hồi thật | Thiếu Chat service WebSocket + Redis pub/sub |
| UC-S06 | Trung bình | MỘT PHẦN | Không có RPC báo cáo (`proto/nexusmart/order/v1/order.proto:153-258`) | Tái dụng `GET /api/v1/seller/orders`, `GET /api/v1/seller/products`, `GET /api/v1/seller/balance` | `web/src/app/(seller)/seller/page.tsx:88-731` | Live: `GET /api/v1/seller/orders?page=1&page_size=5` (seller JWT) → `200 {"items":[],"page":1,"page_size":5,"total":0}`; `GET /api/v1/seller/balance` → `200 {"available_vnd":0,"pending_vnd":0,...}`; `rg -i 'csv\|xuất báo cáo' web/src/` → 0 kết quả | Tính client-side từ tối đa 100 đơn (`page_size=100`); thiếu chọn khoảng thời gian, tỷ lệ hủy, top SP bán chạy, xuất CSV (`PLT-B10`) |
| UC-A06 | Trung bình | MỘT PHẦN | Không có RPC thống kê / bảng tổng hợp ngày (`order.proto`, `user.proto`, `product.proto`) | Tái dụng `GET /api/v1/admin/{orders,ledger/summary,users,shops,products,audit-logs}` | `web/src/app/(admin)/admin/page.tsx:40-491` | Live: `GET /api/v1/admin/orders?page=1&page_size=5` → `200`; `GET /api/v1/admin/ledger/summary` → `200 {"accounts":[...]}`; `GET /api/v1/admin/audit-logs?page=1&page_size=5` → `200`; `rg -i 'csv\|xuất báo cáo' web/src/` → 0 kết quả | Tính client-side từ tối đa 50 đơn (`page_size=50`); thiếu chọn khoảng thời gian, bảng tổng hợp ngày, tổng gian hàng active, xuất báo cáo (`PLT-B10`) |
| UC-OPS01 | Cao | MỘT PHẦN | `pkg/common/{logger,telemetry,metrics}/`, `services/payment/src/{telemetry,metrics}.rs`, `infra/otel-collector-config.yaml` | `/healthz`, `/readyz`, `/metrics` (`:48080..:48088`), OTel Prom `:48889/metrics` | Không có (chưa có Grafana/Loki/Tempo) | Live: 9/9 `/healthz` & `/readyz` trả `200`; `otel-collector` nhận trace (`TracesExporter spans: 9`); NHƯNG `curl -i http://127.0.0.1:48889/metrics` trả `Content-Length: 0`; trace checkout `aa11bb22...` chỉ có log ở `gateway` & `payment` (0 dòng ở `order`, `product`, `promotion`, `shipping`, `notification`); `user:48081/metrics` thiếu `http_requests_total` | Chưa có Prometheus/Loki/Tempo/Grafana + dính 4 lỗi nền (`PLT-B3`, `PLT-B4`, `PLT-B5`, `PLT-B7`) |
| UC-OPS02 | Cao | THIẾU | Không có (`services/aiops` chưa tồn tại) | Không có | Không có | `find .` + `rg -i 'z-score\|ewma\|telegram\|alertmanager'` 0 kết quả trong code/infra | Thiếu Prometheus, Alertmanager, Telegram, AIOps engine Rust |
| UC-OPS03 | Trung bình | THIẾU | Không có (`services/aiops` chưa tồn tại) | Không có | Không có | `find .` không có thư mục/file `aiops`, `incident`, `root.?cause` | Thiếu AIOps phân tích nguyên nhân gốc bằng LLM |
| UC-OPS04 | Thấp | THIẾU | Không có | Không có | Không có | Không có `runbook` hay K8s API client | Mức Thấp |
| UC-OPS05 | Cao | MỘT PHẦN | `.github/workflows/ci.yml:1-142`, `services/*/Dockerfile`, `web/Dockerfile` | Không áp dụng | Không áp dụng | Đọc `.github/workflows/ci.yml`: chỉ `workflow_dispatch` (đúng AGENTS.md), có `proto-lint`, `go-lint-and-test`, `rust-payment`, `web`, `docker-build` (9 backend). `go test -coverpkg=./... ./...`: `order` **51.3%**, `promotion` **51.9%** (<60%); `payment` `cargo test` 35/35 pass (không có tool đo coverage); `web` `lint`/`format:check`/`check:data` pass | Thiếu K8s/Helm/HPA, deploy staging/prod, quét lỗ hổng dependency/image, docker-build `web`, k6/Playwright/contract test, coverage <60% (`PLT-B8`, `PLT-B9`) |
| UC-OPS06 | Trung bình | THIẾU | Không có | Không áp dụng | Không áp dụng | `find .` không có `k8s/`, `helm/`, `charts/`, script rollback | Chưa có hạ tầng triển khai để rollback |
| UC-OPS07 | Trung bình | THIẾU | Không có | Không áp dụng | Không áp dụng | `find .` & `rg -i 'error.?budget\|burn.?rate'` 0 kết quả trong `infra/`/`services/` | Chưa có SLO và cảnh báo error budget dạng code |
| UC-OPS08 | Cao | THIẾU | Không có | Không áp dụng | Không áp dụng | `find .` & `rg -i 'k6\|chaos\|fault.?inject'` 0 kết quả | Chưa có AIOps, kịch bản tiêm lỗi, và tải giả lập k6 |
| UC-OPS09 | Trung bình | THIẾU | Không có | Không áp dụng | Không áp dụng | `find .` không có script `pg_dump`, `wal`, `barman`, `pgbackrest`, backup/restore | Chưa có sao lưu hằng ngày, WAL/PITR, diễn tập khôi phục |
| UC-B02 *(phần gateway & bảo mật xuyên suốt)* | Cao | LỖI | `services/gateway/internal/middleware/{auth,ratelimit,logging}.go`, `services/gateway/internal/httpx/response.go`, `services/gateway/internal/router/router.go`, `services/payment/src/{auth,http}.rs` | Toàn bộ `/api/v1/*`, `/webhooks/*`, `payment:48086/{internal/*,payments/*/events}` | `web/src/middleware.ts`, `web/src/app/api/gw/[...path]/route.ts`, `web/next.config.ts` | Live pass: `rl:user:*` (180/phút) vẫn đếm khi đổi XFF (`179→178`); RBAC 401/403 chuẩn trên gateway (buyer→seller/admin `403`, seller→admin `403`, admin→seller `403`, unauth `401`) và `payment:48086/internal/ledger/summary` (unauth `401`, buyer/seller `403`, admin `200`); JTI thu hồi sau logout trả `401`. Live **LỖI**: đổi `X-Forwarded-For` (`198.51.100.11→.12`) vượt sạch IP rate limit (`297→299`); `httpx.MapGRPCError` lộ raw `codes.Internal`; thiếu security headers; body >2 MiB trả `400` thay `413`; `/webhooks/*` không rate limit | Xem `PLT-B1`, `PLT-B2`, `PLT-B6` |

**Tổng hợp 28 UC**: `ĐỦ`: **0** · `MỘT PHẦN`: **4** (`UC-S06`, `UC-A06`, `UC-OPS01`, `UC-OPS05`) · `LỖI`: **1** (`UC-B02` phần gateway/bảo mật) · `THIẾU`: **23** (`UC-AI01..14`, `UC-B14`, `UC-S09`, `UC-OPS02..04`, `UC-OPS06..09`) · `CHƯA KIỂM`: **0**.

---

## 2. Bảng đối chiếu Shopify / sàn

| Nghiệp vụ | UC hoặc ngoài phạm vi | Trạng thái | Ghi chú |
| --- | --- | --- | --- |
| Kênh bán: online store, POS, app, mạng xã hội, Google, marketplace bên ngoài, nhập hàng của người bán khác | Online store: `UC-B01..B27`, `UC-S01..S18`. **App mobile native**: ngoài phạm vi đặc tả (`docs/spec.md:24`). **POS, mạng xã hội, Google, marketplace bên ngoài, nhập hàng của người bán khác**: ngoài phạm vi đặc tả (`docs/spec.md:39` — không có trong tab Use case). | MỘT PHẦN (chỉ online store) | Chỉ web Next.js online store thuộc phạm vi; mọi kênh bán còn lại ngoài đặc tả. |
| POS: bán tại quầy, thanh toán trực tiếp, quản lý ca và tiền mặt, trả hàng tại cửa hàng, mua online nhận tại quầy | **Ngoài phạm vi đặc tả** (`docs/spec.md:39` không có trong 100 UC; `docs/spec.md:27` loại trừ tiền thật). | Ngoài phạm vi | Không có code và không thuộc yêu cầu đồ án. |
| Bán quốc tế (Markets): giá và catalog theo thị trường, tên miền địa phương, thuế nhập khẩu, ngôn ngữ | **Ngoài phạm vi đặc tả** (`docs/spec.md:23` loại trừ multi-region, `docs/spec.md:28` loại trừ giao quốc tế, `docs/spec.md:39` không có trong tab Use case; `AGENTS.md` luật 6 chốt duy nhất VND `int64`). | Ngoài phạm vi | Thêm đa tiền tệ bắt buộc có ADR được duyệt. |
| B2B: hồ sơ công ty, bảng giá riêng, điều khoản thanh toán, số lượng tối thiểu, báo giá | **Ngoài phạm vi đặc tả** (`docs/spec.md:39` không có trong tab Use case; `docs/spec.md:27` loại trừ hoá đơn điện tử). | Ngoài phạm vi | Web chỉ có form VAT mẫu ở checkout/seller mock, không thuộc backend đặc tả. |
| Phân tích: dashboard, báo cáo bán hàng, khách hàng, tồn kho, hiệu quả marketing, xem trực tiếp | Dashboard & báo cáo cơ bản: `UC-S06` (TB), `UC-A06` (TB), `UC-S11` (Thấp), `UC-AI09` (Thấp). **BI nâng cao / xem trực tiếp / data warehouse**: ngoài phạm vi đặc tả (`docs/spec.md:26` *"công cụ BI nâng cao"*, `docs/spec.md:36` *"Data warehouse"*, `docs/spec.md:39`). | MỘT PHẦN | `/seller` và `/admin` mới cộng client-side trên 50–100 đơn mới nhất; thiếu RPC thống kê, chọn khoảng ngày, top SP, tỷ lệ hủy, xuất CSV (`PLT-B10`). |
| Tự động hoá và AI: workflow theo sự kiện, hẹn giờ phát hành hoặc A/B test, trợ lý cho người bán; Trợ lý AI làm kênh bán (checkout qua trợ lý) | Trợ lý AI (người mua & người bán): `UC-AI01..14` (`UC-AI04` thêm giỏ có xác nhận; `UC-AI06/09/10/11` cho người bán). **AI tự checkout/thanh toán**: ngoài phạm vi đặc tả (`docs/spec.md:32` *"AI tự thanh toán"*, `docs/spec.md:63` `UC-AI04` cấm AI tự checkout/thanh toán). **Workflow tự động hoá cho merchant, hẹn giờ phát hành, A/B test**: ngoài phạm vi đặc tả (`docs/spec.md:39`). | THIẾU (phần AI trong phạm vi) | Chưa có `ai-assistant` service, `pgvector`, LLM API; web chỉ có panel mock `AiAssistantPanel.tsx`. |
| Nền tảng cho nhà phát triển: app store, Admin API và Storefront API, webhook ra ngoài, functions, extension, theme store | **Ngoài phạm vi đặc tả** (`docs/spec.md:39` không có trong tab Use case). | Ngoài phạm vi | REST `/api/v1/*` là API nội bộ cho BFF web, không có public app/extension/outbound webhook platform. |
| Thông báo và tuân thủ (phần nền tảng): cookie và quyền riêng tư, chống bot, nhật ký hoạt động | Nhật ký hoạt động quản trị: `UC-A11` (Cao), `docs/spec.md:26`. Quyền dữ liệu cá nhân: `UC-B27` (Thấp). Chống lạm dụng API (rate limit): `docs/spec.md:35`, `UC-B02`, `UC-B16`. **WAF thương mại**: ngoài phạm vi đặc tả (`docs/spec.md:35`). **Cookie consent banner / chống bot CAPTCHA**: không có trong tab Use case (`docs/spec.md:39`). | MỘT PHẦN | Audit log append-only đã chạy cho `user`/`shop` nhưng thiếu cho `product`/`promotion`/`payment` (`PLT-B11`); `UC-B27` thiếu; hộp thoại cookie ở `ShopFooter.tsx:318-407` chỉ lưu `localStorage` mock; không có CAPTCHA/bot protection; rate limit IP bị bypass qua XFF (`PLT-B1`). |
| Chat người mua với người bán (sàn) | `UC-B14` (TB), `UC-S09` (TB). **Gọi thoại/video, chatbot riêng gian hàng**: ngoài phạm vi đặc tả (`docs/spec.md:30`). | THIẾU | Chưa có `services/chat` (WebSocket + Redis pub/sub); web chỉ có mock cục bộ `BuyerChatPanel.tsx` & `/seller/chat`. |

---

## 3. Lỗi phát hiện (`PLT-B<n>`)

### `PLT-B1` (P1) — Vượt giới hạn tốc độ theo IP tại Gateway bằng header `X-Forwarded-For` giả mạo
- **Ảnh hưởng**: `UC-B02` (Cao), `UC-B16`, `docs/spec.md:35`. Vô hiệu hoá hoàn toàn IP rate limit (300 req/phút) trên mọi endpoint public (kể cả `/auth/login`, `/auth/register`, `/auth/resend-otp`, `/flash-sales`), cho phép password spraying nhiều tài khoản hoặc spam OTP/đăng ký từ 1 IP thật.
- **Tái hiện**:
  ```bash
  for i in 1 2 3; do
    curl -sI -H "X-Forwarded-For: 198.51.100.11" http://127.0.0.1:48080/api/v1/categories | grep -i x-ratelimit
  done
  curl -sI -H "X-Forwarded-For: 198.51.100.12" http://127.0.0.1:48080/api/v1/categories | grep -i x-ratelimit
  ```
- **Mong đợi vs Thật**:
  - *Mong đợi*: khi không cấu hình trusted reverse proxy, Gateway lấy `r.RemoteAddr` (không tin `X-Forwarded-For` từ client lạ) nên 4 request cùng kết nối TCP phải giảm liên tục `299 → 298 → 297 → 296`.
  - *Thật*: 3 request đầu trả `X-RateLimit-Remaining: 299, 298, 297`; request thứ 4 đổi `X-Forwarded-For` nhảy lại về `X-RateLimit-Remaining: 299`. Kiểm tra Redis: `ZCARD rl:ip:198.51.100.11` = `3`, `ZCARD rl:ip:198.51.100.12` = `1`. (Riêng `RateLimitByUser` `rl:user:<uid>` 180/phút hoạt động đúng: 2 request đổi XFF vẫn giảm `179 → 178`).
- **Gốc nghi ngờ**: `services/gateway/internal/middleware/ratelimit.go:175-184` (`ClientIP` ưu tiên tuyệt đối `X-Forwarded-For` / `X-Real-IP` mà không kiểm danh sách CIDR proxy tin cậy) + `docker-compose.yml:398` (publish trực tiếp gateway `0.0.0.0:48080`).

### `PLT-B2` (P1) — Gateway lộ nguyên thông điệp lỗi `codes.Internal` (nhúng raw lỗi DB/Redis) từ backend gRPC
- **Ảnh hưởng**: `UC-B02`, `docs/spec.md:35` (OWASP Top 10: rò rỉ thông tin kỹ thuật nội bộ khi gọi trực tiếp Gateway `:48080`, vốn đang mở `0.0.0.0`).
- **Tái hiện** (kiểm chứng mã nguồn + unit test `response_test.go:33`):
  - `MapGRPCError(status.Error(codes.Internal, "load cart from redis: dial tcp 172.20.0.3:6379: connection refused"))` trả HTTP `500` với body `{"error":{"code":"INTERNAL_ERROR","message":"load cart from redis: dial tcp 172.20.0.3:6379: connection refused","trace_id":"..."}}`.
  - Lưu ý: khi đi qua BFF Web (`web/src/lib/server/gateway.ts:150-152`), lỗi `>=500` đã được che thành `503 UNAVAILABLE`, nhưng gọi trực tiếp Gateway không được che.
- **Mong đợi vs Thật**:
  - *Mong đợi*: mọi lỗi `codes.Internal` / `codes.Unknown` / `codes.DataLoss` trả thông điệp chung `"internal server error"` (log chi tiết nội bộ kèm `trace_id`).
  - *Thật*: `services/gateway/internal/httpx/response.go:83-88` trả nguyên `st.Message()` cho nhánh `default:`. Cùng lúc, 6 service nhúng trực tiếp `%v` của lỗi DB/Redis vào `codes.Internal`: `services/cart/internal/server/server.go:97,121,327`, `services/product/internal/server/server.go:68-1286`, `services/promotion/internal/service/promotion.go:201-1449`, `services/order/internal/service/service.go:143`, `services/shipping/internal/grpcserver/server.go:267`, `services/notification/internal/grpcserver/server.go:195`, `services/payment/src/error.rs:98,100`.
- **Gốc nghi ngờ**: `services/gateway/internal/httpx/response.go:83-88`.

### `PLT-B3` (P1) — Cổng Prometheus của OpenTelemetry Collector (`:48889/metrics`) hoàn toàn rỗng (0 byte)
- **Ảnh hưởng**: `UC-OPS01` (Cao). Không thể thu thập metric tập trung qua `otel-collector:8889` như thiết kế trong `docker-compose.yml:66` và `infra/otel-collector-config.yaml:17-29`.
- **Tái hiện**:
  ```bash
  curl -sS -i http://127.0.0.1:48889/metrics
  ```
- **Mong đợi vs Thật**:
  - *Mong đợi*: xuất các chỉ số RED từ 9 service (hoặc ít nhất metric nhận qua OTLP/scrape).
  - *Thật*: trả `HTTP/1.1 200 OK`, `Content-Length: 0` (body rỗng hoàn toàn, dù đã phát sinh traffic checkout/cancel và `otel-collector` đang nhận đều `TracesExporter`).
- **Gốc nghi ngờ**: `pkg/common/telemetry/telemetry.go:35-76` và `services/payment/src/telemetry.rs:16-72` chỉ khởi tạo `TracerProvider` (không gửi OTLP metrics), còn `infra/otel-collector-config.yaml:1-29` không cấu hình receiver `prometheus` để scrape `gateway:8080/metrics`, `user:8081/metrics`, …, `notification:8088/metrics`.

### `PLT-B4` (P1) — 6/8 service domain không xuất log cho luồng nghiệp vụ bình thường và `payment` đứt chuỗi OTel span
- **Ảnh hưởng**: `UC-OPS01` (Cao), `docs/spec.md:33` ("trace ID xuyên suốt qua gRPC và message broker", "Log dạng JSON có trace ID"). DevOps không thể tra cùng `trace_id` trong log của `order`, `product`, `promotion`, `shipping`, `notification` cho một giao dịch checkout thành công, và trace trên Tempo/Collector bị đứt nhánh ở `payment`.
- **Tái hiện**:
  ```bash
  # Đặt 1 đơn VIETQR với traceparent cố định (buyer token)
  curl -sS -X POST http://127.0.0.1:48080/api/v1/checkout \
    -H "Authorization: Bearer $TOKEN" \
    -H "Idempotency-Key: audit-plt-trace-002" \
    -H "traceparent: 00-aa11bb22cc33dd44ee55ff6677889902-1122334455667788-01" \
    -H "Content-Type: application/json" \
    -d '{"address_id":"<ADDR_ID>","from_cart":false,"items":[{"sku_id":"<SKU_ID>","quantity":1}],"payment_method":"VIETQR"}'
  # Kiểm tra log toàn bộ 9 service theo trace_id:
  for s in gateway user product cart promotion order payment shipping notification; do
    echo "=== $s ==="; docker compose logs --since 5m "$s" | grep aa11bb22cc33dd44ee55ff6677889902
  done
  ```
- **Mong đợi vs Thật**:
  - *Mong đợi*: cùng `trace_id=aa11bb22cc33dd44ee55ff6677889902` xuất hiện trong log JSON và OTel span của các service tham gia (`gateway`, `order`, `user`, `product`, `shipping`, `promotion`, `payment`, `notification`).
  - *Thật*:
    - Trong `order_db.outbox_events` có lưu đúng `trace_id=aa11bb22cc33dd44ee55ff6677889902` và `traceparent=00-aa11bb22cc33dd44ee55ff6677889902-2f0fb04dcafec05c-01` (đã publish sang NATS và `notification` xử lý xong).
    - Tuy nhiên trên stdout log JSON, chỉ `gateway` (`http request`) và `payment` (`payment intent created`) có dòng log chứa `aa11bb22cc33dd44ee55ff6677889902`; **5 service `order`, `product`, `promotion`, `shipping`, `notification` (và `user` cho `ValidateUser`/`GetAddress`) có 0 dòng log**. Kết quả tương tự với lượt hủy đơn `bb22cc33dd44ee55ff6677889900aa22` (chỉ `gateway` và `payment` log).
    - Ở `services/payment`, `grpc.rs:140,482-484` và `http.rs:123-126` chỉ parse `trace_id` thành `String` để ghi trường log/DB, không gọi `tracing_opentelemetry::OpenTelemetrySpanExt::set_parent` nên không tạo child span OTel nối vào trace gốc.
- **Gốc nghi ngờ**: Không có gRPC request-logging interceptor trong `pkg/common` / các `services/*/cmd/*/main.go`; `services/payment/src/{grpc,http,telemetry}.rs` thiếu W3C `TraceContextPropagator::extract` gắn vào `tracing::Span`.

### `PLT-B5` (P2) — Gateway `RequestLogger` không bao giờ ghi `user_id` do thứ tự middleware
- **Ảnh hưởng**: `UC-OPS01`, `UC-B02`. Toàn bộ access log của Gateway thiếu danh tính `user_id` trên mọi request đã xác thực, giảm khả năng truy vết sự cố/an ninh theo tài khoản.
- **Tái hiện**:
  - Gọi `POST /api/v1/checkout` hoặc `POST /api/v1/orders/{id}/cancel` với `$TOKEN` hợp lệ, rồi xem log `docker compose logs --since 5m gateway | grep /api/v1/checkout`.
- **Mong đợi vs Thật**:
  - *Mong đợi*: dòng log JSON chứa `"user_id":"aaec5f9a-..."` đúng như ý đồ của `services/gateway/internal/middleware/logging.go:85-87`.
  - *Thật*: dòng log `{"msg":"http request","service":"gateway","trace_id":"aa11bb22...","method":"POST","path":"/api/v1/checkout","status":201,...}` **không có trường `user_id`**.
- **Gốc nghi ngờ**: `services/gateway/internal/router/router.go:107` gắn `main.Use(middleware.RequestLogger(deps.Logger))` TRƯỚC `authed.Use(middleware.Auth(...))` ở dòng 156; `Auth` tạo `r.WithContext(ctx)` mới chỉ truyền xuống handler bên trong nên `r.Context()` ở `RequestLogger` bên ngoài không bao giờ thấy `Claims`.

### `PLT-B6` (P2) — Thiếu HTTP security headers trên Gateway/Web, giới hạn body trả `400` thay vì `413`, `/webhooks/*` thiếu rate limit và BFF `/api/gw` không giới hạn kích thước body
- **Ảnh hưởng**: `UC-B02`, `docs/spec.md:35`. Tăng bề mặt clickjacking/MIME-sniffing, dễ bị cạn RAM ở BFF Node.js nếu gửi body lớn, và tuyến `/webhooks/*` không có rate limit tại Gateway.
- **Tái hiện**:
  ```bash
  # 1. Thiếu security headers trên Gateway và Web (web lộ thêm X-Powered-By: Next.js)
  curl -sI -H "Origin: https://evil.example" http://127.0.0.1:48080/api/v1/categories
  curl -sI http://127.0.0.1:43000/
  # 2. Gửi body 2 MiB + 1 KiB vào /api/v1/auth/login -> trả 400 thay vì 413
  python3 -c 'import urllib.request, urllib.error; r=urllib.request.Request("http://127.0.0.1:48080/api/v1/auth/login", data=(b"{\"email\":\"a@b.c\",\"password\":\"" + b"x"*(2*1024*1024+1024) + b"\"}"), headers={"Content-Type":"application/json"}); urllib.request.urlopen(r)'
  ```
- **Mong đợi vs Thật**:
  - *Mong đợi*: có `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy`, ẩn `X-Powered-By`; body vượt ngưỡng trả `413 Payload Too Large` (dùng `http.MaxBytesReader` ở Gateway & kiểm kích thước ở BFF `/api/gw`); `/webhooks/*` có rate limit IP/riêng và giới hạn body ngay tại Gateway.
  - *Thật*:
    - Gateway không có CORS (trả `405` cho `OPTIONS`, không có `Access-Control-Allow-Origin` — đúng với kiến trúc BFF cùng origin), nhưng thiếu hoàn toàn security headers; Web (`:43000`) cũng không đặt security headers và trả `X-Powered-By: Next.js`.
    - Gateway dùng `io.LimitReader(r.Body, 2<<20)` (`services/gateway/internal/httpx/response.go:102,114`) nên payload 2 MiB + 1 KiB trả `400 INVALID_JSON` thay vì `413`.
    - `/webhooks/*` (`router.go:110-116`, `proxy/proxy.go:20-66`) nằm ngoài nhóm `/api/v1` nên không có rate limit và không giới hạn body tại Gateway (khi bắn 2.1 MiB vào `/webhooks/payment`, `payment` Axum mới chặn trả `413`; còn `services/shipping/internal/webhook/handler.go:89` dùng `io.LimitReader(..., 1<<20)` cắt cụt body rồi báo `401` sai chữ ký).
    - BFF `web/src/app/api/gw/[...path]/route.ts:107` gọi `await req.arrayBuffer()` không kiểm `Content-Length`/giới hạn byte trước khi đọc toàn bộ body vào bộ nhớ.
- **Gốc nghi ngờ**: `services/gateway/internal/router/router.go:101-117`, `services/gateway/internal/httpx/response.go:102,114`, `web/next.config.ts:3-10`, `web/src/app/api/gw/[...path]/route.ts:107`.

### `PLT-B7` (P2) — Service `user` không xuất HTTP RED metrics trên `:48081/metrics`
- **Ảnh hưởng**: `UC-OPS01`, lệch hợp đồng `docs/contracts.md:37-43` (mọi service phải xuất đủ 6 chỉ số RED).
- **Tái hiện**:
  ```bash
  curl -sS http://127.0.0.1:48081/healthz >/dev/null
  curl -sS http://127.0.0.1:48081/metrics | grep -E '^(http|grpc)_'
  ```
- **Mong đợi vs Thật**:
  - *Mong đợi*: xuất cả `http_requests_total`, `http_request_duration_seconds` và `grpc_*` (như `product`, `cart`, `promotion`, `order`, `shipping`, `notification`, `payment`).
  - *Thật*: chỉ có `grpc_requests_total`, `grpc_errors_total`, `grpc_request_duration_seconds`; hoàn toàn thiếu `http_requests_total` và `http_request_duration_seconds`.
- **Gốc nghi ngờ**: `services/user/cmd/user/main.go:154` gắn trực tiếp `metrics.NewOpsHandler(cfg.ServiceName, readyFn, reg)` mà không bọc `telemetry.HTTPMiddleware` và `red.HTTPMiddleware()`.

### `PLT-B8` (P2) — Coverage unit test của `order` (51.3%) và `promotion` (51.9%) dưới mục tiêu 60%, thiếu đo coverage cho `payment`
- **Ảnh hưởng**: `UC-OPS05`, lệch tiêu chí kiểm thử `docs/spec.md:38` ("Mục tiêu coverage 60% cho Order, Payment, Promotion").
- **Tái hiện**:
  ```bash
  (cd services/order && go test -coverpkg=./... -coverprofile=/tmp/o.cov ./... && go tool cover -func=/tmp/o.cov | tail -n 1)
  (cd services/promotion && go test -coverpkg=./... -coverprofile=/tmp/p.cov ./... && go tool cover -func=/tmp/p.cov | tail -n 1)
  ```
- **Mong đợi vs Thật**:
  - *Mong đợi*: ≥ 60% cho cả 3 service `order`, `promotion`, `payment` và có kiểm tra ngưỡng trong CI.
  - *Thật*:
    - `services/order`: **51.3%** toàn module (`internal/service` 54.8%, `internal/statemachine` 88.4%, `internal/clients` 28.7%, `internal/events` 16.4%, `internal/store` 0.0%, `internal/grpcserver` 0.0%, `internal/db` 0.0%); kể cả loại trừ `internal/db` generated và `cmd/server` vẫn chỉ đạt **58.6%**.
    - `services/promotion`: **51.9%** toàn module (`internal/calculator` 77.2%, `internal/lua` 63.2%, `internal/service` 51.6%, `internal/events` 8.9%, `internal/store` 0.0%).
    - `services/payment`: `cargo test` xanh 35/35 test nhưng máy không cài `cargo-llvm-cov`/`cargo-tarpaulin`/`grcov` và CI (`.github/workflows/ci.yml:85-87`) chỉ chạy `cargo test` không đo coverage.
- **Gốc nghi ngờ**: Thiếu unit/integration test cho tầng `internal/store`, `internal/events`, `internal/grpcserver`; `.github/workflows/ci.yml:62-88` không đặt bước kiểm ngưỡng coverage.

### `PLT-B9` (P2) — CI thiếu build Docker image cho `web` & thiếu quét lỗ hổng; `otel-collector` thiếu healthcheck; `product` khai báo phụ thuộc NATS thừa
- **Ảnh hưởng**: `UC-OPS05`, `docs/spec.md:31,35,37`.
- **Tái hiện**: đọc trực tiếp `.github/workflows/ci.yml:115-142`, `docker-compose.yml:58-67,116-129`, và chạy `rg -i 'nats' services/product/` (0 kết quả).
- **Mong đợi vs Thật**:
  - *Mong đợi*: CI build đủ cả `web/Dockerfile`, quét lỗ hổng dependency (`govulncheck`, `cargo audit`, `npm audit`) & image (`trivy`), có integration/contract test; `otel-collector` có healthcheck; `product` phát event thay đổi sản phẩm lên NATS cho Search/AI.
  - *Thật*: `docker-build` bỏ sót `web`; không có bước security/vulnerability scan nào trong CI; `otel-collector` không có `healthcheck` (`docker compose ps` hiện `Up` thay vì `healthy`); `product` khai báo `NATS_URL`/`depends_on: nats` nhưng không có code NATS.
- **Gốc nghi ngờ**: `.github/workflows/ci.yml:115-142`, `docker-compose.yml:58-67,116-129`, `services/product/`.

### `PLT-B10` (P2) — Báo cáo `UC-S06` (`/seller`) và `UC-A06` (`/admin`) tổng hợp client-side từ lát cắt 50–100 đơn mới nhất, thiếu lọc ngày và xuất báo cáo
- **Ảnh hưởng**: `UC-S06`, `UC-A06`. Số liệu GMV, doanh thu thuần, hoa hồng và biểu đồ 7 ngày bị sai lệch âm thầm ngay khi gian hàng có >100 đơn hoặc toàn sàn có >50 đơn; không có bộ lọc khoảng thời gian, tỷ lệ hủy, top sản phẩm bán chạy, bảng tổng hợp theo ngày, hay xuất CSV.
- **Tái hiện**: đọc `web/src/app/(seller)/seller/page.tsx:107,161-226` (`page_size=100`), `web/src/app/(admin)/admin/page.tsx:64,89-124` (`page_size=50`), và `proto/nexusmart/order/v1/order.proto:153-258` (không có RPC báo cáo hay tham số `from_date`/`to_date`).
- **Mong đợi vs Thật**:
  - *Mong đợi*: backend cung cấp RPC thống kê theo khoảng thời gian (có bảng tổng hợp theo ngày cho truy vấn dài theo `UC-A06`), trả đủ GMV, doanh thu, số đơn, tỷ lệ hủy, top sản phẩm, tổng shop/user và cho xuất CSV.
  - *Thật*: client cộng tay trên trang 1 (`50` hoặc `100` đơn), cố định cửa sổ 7 ngày gần nhất, không có xuất báo cáo.
- **Gốc nghi ngờ**: `proto/nexusmart/order/v1/order.proto:153-258`, `web/src/app/(seller)/seller/page.tsx:107`, `web/src/app/(admin)/admin/page.tsx:64`.

### `PLT-B11` (P2) — Nhật ký kiểm toán (Audit Log) chưa ghi cho thao tác quản trị ngoài `user`/`shop`
- **Ảnh hưởng**: `UC-A11` (Cao), `docs/spec.md:26` ("Audit log chỉ ghi thêm cho mọi thao tác quản trị").
- **Tái hiện**: `rg -n 'WriteAuditLog|InsertAuditLog' services/` chỉ có 3 điểm ghi trong `services/user/internal/service/server.go:730,809,1077` (khóa/mở user, đổi role, duyệt shop).
- **Mong đợi vs Thật**:
  - *Mong đợi*: mọi thao tác quản trị (`AdminReviewProduct`, CRUD danh mục, `AdminCreateVoucher`, `AdminCreateFlashSale`, `ApproveFlashSaleItem`, `CreditCoins`, `CreateRefund`, `RunReconciliation`) đều được ghi vào `audit_logs`.
  - *Thật*: `UserService.WriteAuditLog` (`services/user/internal/service/server.go:1326`) đã tạo sẵn nhưng không có service hay gateway handler nào gọi.
- **Gốc nghi ngờ**: `services/gateway/internal/router/handlers_{product,promotion,payment}.go` và các service tương ứng chưa gọi `WriteAuditLog` (hoặc chưa phát sự kiện audit).

---

## 4. Dữ liệu & nhất quán hiển thị (`PLT-D<n>`)

- **`PLT-D1` — Panel Trợ lý AI hiển thị đơn mẫu của người khác cho mọi tài khoản đăng nhập**: `web/src/features/commerce/AiAssistantPanel.tsx:353,441-442,508` ghi rõ *"chỉ đọc số đo và đơn hàng thuộc tài khoản đang đăng nhập"*, nhưng thực tế đọc hằng `INITIAL_ORDERS` (đơn mẫu của `Nguyễn Minh Quân`) cho bất kỳ buyer mới đăng ký nào, kèm nút `Mở chi tiết đơn` dẫn tới `/don-hang/<mock-id>` gây 404 trên backend thật.
- **`PLT-D2` — Giao diện Chat 1-1 giả lập không có nhãn "dữ liệu mẫu" và tự khẳng định đang đồng bộ thật**: `web/src/app/(seller)/seller/chat/page.tsx:253` hiển thị `"Đồng bộ trực tiếp với khung chat người mua"` và `BuyerChatPanel.tsx:150` giả lập phản hồi sau 850ms bằng state RAM, không lưu DB và không truyền qua mạng giữa buyer/seller khác trình duyệt.
- **`PLT-D3` — Lệch số liệu giữa KPI tổng và KPI tính từ lát cắt đơn trên `/admin` và `/seller`**: trên `/admin` (`page.tsx:64,218-224`), nhãn hiển thị tổng `ordersTotal` (từ `res.total`) bên cạnh `totalGmvVnd` và `totalCommissionVnd` chỉ cộng từ 50 đơn trang 1; tương tự `/seller` (`page.tsx:107`) chỉ cộng từ 100 đơn trang 1.
- **`PLT-D4` — Lệch cấu hình & tài liệu hợp đồng**:
  - `docs/contracts.md:137` quy định `POST /api/v1/shops/register` cho `buyer, seller`, nhưng `services/gateway/internal/router/router.go:171` cho phép cả `admin, super_admin`.
  - `docker-compose.yml:116-117,128-129` truyền `NATS_URL`/`NATS_STREAM`/`depends_on: nats` cho `product` nhưng `services/product` không dùng NATS.
  - `docker-compose.yml:66` mở cổng `OTEL_PROM_PORT=48889` của `otel-collector`, nhưng không có metric nào được đẩy/scrape vào nên luôn trả 0 byte.
- **Kiểm tra dữ liệu nền (SẠCH)**:
  - Outbox & DLQ trên stack `nexusmart-audit`: `order_db.outbox_events` 7 bản ghi (`0` kẹt `published_at IS NULL`); `payment_db.outbox_events` 3 bản ghi (100% `status='sent'`); `shipping_db.outbox_events` 0 kẹt; `dead_letter_events` = `0` ở cả `order_db`, `shipping_db`, `notification_db`; NATS stream `NEXUSMART` 25 message / 6 durable consumer (`ack_pending=0`, `pending=0`, `redelivered=0`); `NEXUSMART_DLQ` = `0` message.
  - Docker & Secret: 10/10 container ứng dụng (`gateway`, 8 domain service, `web`) đều build nhiều giai đoạn từ root repo và chạy non-root (`appuser` uid 100/10001, `nextjs` uid 10001); `.env` không nằm trong `git ls-files` (chỉ có `.env.example` giá trị dev); quét `rg` toàn repo và toàn bộ log container không phát hiện secret thật hay PII (`email`, `phone`, mật khẩu, JWT, `DATABASE_URL`) bị lộ (email đăng ký `audit-plt-1@nexusmart.local` được che thành `au***@nexusmart.local`, `payment` che toàn bộ secret trong `Config::fmt`).

---

## 5. Đề xuất sửa (`PLT-F<n>`)

| Mã | Giải quyết | Việc cụ thể (file, RPC, migration, route) | Cỡ | Đổi proto / migration / ADR |
| --- | --- | --- | --- | --- |
| `PLT-F1` | `PLT-B1`, `UC-B02` | Sửa `ClientIP` trong `services/gateway/internal/middleware/ratelimit.go` chỉ tin `X-Forwarded-For` khi `RemoteAddr` thuộc danh sách `TRUSTED_PROXY_CIDRS` cấu hình qua env; thêm rate limit riêng cho `/api/v1/auth/*` và `/webhooks/*` trong `router.go`. | S | Không đổi proto/migration. **Chốt `ADR-0001`** (edge gateway OSS ghi đè XFF khi deploy). |
| `PLT-F2` | `PLT-B2`, `UC-B02` | Sửa `services/gateway/internal/httpx/response.go` (`MapGRPCError` nhánh `default:`): luôn trả `"internal server error"` cho `codes.Internal`/`Unknown`/`DataLoss` và log `st.Message()` kèm `trace_id` phía server; bổ sung unit test ở `response_test.go`. | S | Không |
| `PLT-F3` | `PLT-B6`, `UC-B02` | Thêm middleware security headers (`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`) ở `services/gateway/internal/router/router.go` và `headers()` + `poweredByHeader: false` ở `web/next.config.ts`; chuyển `DecodeJSON`/`DecodeOptionalJSON` sang `http.MaxBytesReader` trả `413`; giới hạn body trên webhook proxy & `web/src/app/api/gw/[...path]/route.ts`. | S | Không |
| `PLT-F4` | `PLT-B3`, `PLT-B4`, `PLT-B5`, `PLT-B7`, `UC-OPS01` | (1) Thêm receiver `prometheus` scrape 9 service vào `infra/otel-collector-config.yaml` (hoặc xuất OTLP metrics từ `pkg/common/metrics`); (2) thêm gRPC request-logging interceptor trong `pkg/common/logger` gắn vào 7 service Go; (3) nối W3C `traceparent` vào `tracing::Span` bằng `OpenTelemetrySpanExt::set_parent` trong `services/payment/src/{grpc,http,telemetry}.rs`; (4) đọc `user_id` trong `gateway` bằng context pointer/hook sau `Auth`; (5) bọc `red.HTTPMiddleware()` cho `opsSrv` tại `services/user/cmd/user/main.go:154`. | M | Không đổi proto/migration. |
| `PLT-F5` | `PLT-B8`, `PLT-B9`, `UC-OPS05` | Bổ sung unit/integration test cho `services/order/internal/{store,events,grpcserver}` và `services/promotion/internal/{store,events}` nâng coverage ≥60%; thêm job đo coverage Rust (`cargo-llvm-cov`), build `web/Dockerfile`, và quét lỗ hổng (`govulncheck`, `cargo audit`, `npm audit`, Trivy) vào `.github/workflows/ci.yml` (giữ `workflow_dispatch`); thêm healthcheck cho `otel-collector` trong `docker-compose.yml`; thêm bộ contract test gRPC, k6 & Playwright. | L | Không đổi proto/migration. |
| `PLT-F6` | `PLT-B10`, `PLT-D3`, `UC-S06`, `UC-A06` | Thêm RPC `GetSellerAnalytics` & `GetPlatformAnalytics` (nhận `from_date`, `to_date`, trả GMV, doanh thu thuần, số đơn, tỷ lệ hủy, chuỗi theo ngày, top sản phẩm) trong `proto/nexusmart/order/v1/order.proto`, bảng tổng hợp ngày `daily_order_stats` (migration `0003_*` ở `order_db`), endpoint `GET /api/v1/{seller,admin}/analytics` + xuất CSV ở gateway và nối lại `/seller`, `/admin`. | M | **Đổi proto** (`order.proto`) + **migration** (`order_db`). Không cần ADR hạ tầng. |
| `PLT-F7` | `PLT-B11`, `UC-A11` | Ghi audit log cho toàn bộ thao tác quản trị còn lại (gọi `UserService.WriteAuditLog` qua service token — bổ sung vào `internalRPCs` `pkg/common/auth/auth.go` — hoặc phát sự kiện `audit.*` lên NATS) từ `gateway`/`product`/`promotion`/`payment`; đồng bộ RBAC `POST /api/v1/shops/register` với `docs/contracts.md:137`. | M | Không đổi proto (đã có `WriteAuditLog`), không migration. |
| `PLT-F8` | `PLT-D1`, `PLT-D2` | Gắn nhãn rõ "Chế độ mô phỏng (chưa nối dịch vụ)" và tắt việc hiển thị `INITIAL_ORDERS` cho user đã đăng nhập trong `AiAssistantPanel.tsx`, sửa dòng `"Đồng bộ trực tiếp..."` trong `seller/chat/page.tsx` cho đến khi có `ai-assistant` và `chat`. | S | Không |
| `PLT-F9` | `UC-OPS01..09` (Hạ tầng vận hành & AIOps) | (1) Viết **ADR Nơi triển khai** trước tiên; (2) viết các ADR Nhóm B cho Observability stack (Prometheus + Loki + Tempo + Grafana + Alertmanager/Telegram + SLO code), Helm/K8s/HPA + rollback, Backup WAL/PITR lên Object Storage; (3) sau khi ADR được duyệt, dựng `services/aiops` (Rust: z-score/EWMA, LLM RCA, runbook) và kịch bản tiêm lỗi `UC-OPS08`. | L | **Bắt buộc ADR** (Nơi triển khai, Observability, Triển khai K8s/Helm, Object storage). |
| `PLT-F10` | `UC-AI01..14`, `UC-B14`, `UC-S09`, `UC-B03/B12` (3 service còn thiếu) | Lần lượt mở ADR và dựng 3 microservice còn thiếu: (1) `services/search` (Go + engine theo ADR + phát event `product.*` từ `services/product`); (2) `services/chat` (Go WebSocket + Redis pub/sub + `chat_db` + object storage theo ADR); (3) `services/ai-assistant` (Go + `pgvector` theo ADR + RAG + function calling bằng JWT user + SSE + guardrail + eval set 100 câu). | L | **Bắt buộc ADR** (Search engine, Vector store `pgvector` & LLM provider, Object storage) + **proto mới** (`search`, `chat`, `ai`) + **migration mới** (`search_db`, `chat_db`, `ai_db`). |

---

## 6. Đã kiểm gì, chưa kiểm gì

### Đã kiểm
- **Kiến trúc & mã nguồn HEAD (`0db2cdd`)**: đối chiếu toàn bộ 11 microservice đặc tả, `proto/nexusmart/`, `services/gateway/`, `pkg/common/`, `services/payment/src/`, `services/*/cmd/*/main.go`, `services/shipping/internal/webhook/`, `web/src/{middleware,app/api/gw,features/commerce,app/(seller)/seller,app/(admin)/admin,components/ShopFooter,lib/search,mock}`, `docker-compose.yml`, `infra/`, `.github/workflows/ci.yml`, `docs/adr/`.
- **Kiểm chứng live trên stack `nexusmart-audit`**:
  - 9/9 `/healthz`, `/readyz`, `/metrics` (`:48080..:48088`) và OTel Collector `:48889/metrics`.
  - Rate limit IP + tái hiện bypass qua `X-Forwarded-For` + kiểm tra Redis `rl:ip:*`, `rl:user:*` (rate limit theo user giữ nguyên đếm khi đổi XFF).
  - Ma trận RBAC đầy đủ qua Gateway (unauth/buyer/seller/admin) và trực tiếp trên `payment:48086/internal/ledger/summary`, `/payments/*/events`; JTI revocation sau logout (`401`).
  - CORS, HTTP security headers (Gateway & Web), giới hạn body >2 MiB trên `/api/v1/auth/login` (`400`) và `/webhooks/payment` (`413`).
  - Tạo buyer riêng `audit-plt-1@nexusmart.local` (lấy OTP từ Mailpit `:48025`), đặt 1 đơn `VIETQR` (`NM-20261122-66F9FC9B`) và hủy lại ngay để hoàn tồn kho, kiểm tra lan truyền `trace_id`/`traceparent` trên log cả 9 service, `order_db`/`payment_db`/`shipping_db` `outbox_events`, `processed_events`, `dead_letter_events`, và NATS JetStream (`:48222/jsz`).
  - Quét log container 9 service tìm rò rỉ PII/secret; kiểm tra `docker compose exec <svc> id` cho 10/10 container app; quét `git ls-files` và `rg` secret thật trong repo.
  - Chạy kiểm tra chỉ đọc trên host: `go test -coverpkg=./...` cho `services/order` và `services/promotion`; `cargo test` cho `services/payment`; `npm run lint`, `npm run format:check`, `TZ=UTC npm run check:data` cho `web/`.

### Chưa kiểm (kèm lý do)
- **Độ phủ dòng code (coverage %) của `services/payment` (Rust)**: máy không cài `cargo-llvm-cov`, `cargo-tarpaulin` hay `grcov` và luật rà soát cấm cài công cụ mới (chỉ xác nhận `cargo test` pass 35/35 test).
- **Kích hoạt HTTP `429` thật trên rate limit Gateway**: không bắn >300 req/phút để tuân thủ luật dùng chung stack `nexusmart-audit` cho 8 worker (đã kiểm chứng bằng header `X-RateLimit-Limit`/`X-RateLimit-Remaining` giảm dần và key `rl:ip:*`/`rl:user:*` trên Redis).
- **Chạy `make e2e` / `make e2e-web` toàn bộ**: cấm chạy theo luật cứng (dùng chung stack); chỉ kiểm tra mã nguồn các file trong `tests/e2e/` và `tests/e2e-web/`.
- **Gọi thực tế vào LLM API / nhà cung cấp ngoài**: chưa có service AI và luật cấm gọi dịch vụ ngoài thật.
