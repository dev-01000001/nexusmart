# Báo cáo Swarm C – AI Assistant (`services/assistant`, `proto/nexusmart/assistant`, `evals/assistant`)

Ngày báo cáo: 2026-11-21
Phạm vi hợp đồng: `.agents/briefs/m-c-assistant.md`, `.agents/briefs/m-all-contract.md`, `docs/spec.md` (hàng **Trợ lý AI**, UC-AI01..UC-AI14).

---

## 1. Đã hoàn thành

### 1.1 Protobuf & Sinh code (`proto/nexusmart/assistant/v1/`, `gen/go/nexusmart/assistant/v1/`)
- Định nghĩa `proto/nexusmart/assistant/v1/assistant.proto` (`AssistantService`) với 7 RPC:
  - `Handoff`, `SellerReport`, `DescribeProduct`, `SuggestReply`, `GetReviewSummary`, `RefreshReviewSummary`, `SubmitFeedback`.
- Sinh code Go độc lập qua `buf generate --path proto/nexusmart/assistant` (`gen/go/nexusmart/assistant/v1/assistant.pb.go`, `assistant_grpc.pb.go`), vượt `buf lint --path proto/nexusmart/assistant`.

### 1.2 Microservice `services/assistant` (Go 1.22, HTTP SSE `:8091`, gRPC `:50061`)
- **LLM Provider Interface (`internal/llm/`)**:
  - `LLM_MODE=mock|anthropic` (mặc định `mock`, chạy offline/e2e).
  - `AnthropicProvider`: gọi Claude API qua `net/http` + `bufio.Scanner` thư viện chuẩn Go, model mặc định `claude-opus-5-5` (`LLM_MODEL`), streaming SSE (`text_delta`, `input_json_delta` / `tool_use` / `tool_result`), kiểm tra scheme/host chống SSRF, timeout + retry có backoff (429/529/5xx) + circuit breaker, đo token thực và chi phí VND số nguyên; đã unit test với `httptest.NewServer` mô phỏng đầy đủ luồng SSE & tool_use của Anthropic.
  - `MockProvider`: định tuyến đa lượt tiếng Việt theo ngữ cảnh thực tế, phân tích khoảng giá tiếng Việt, bám sát dữ liệu trả về từ tool (không bịa sản phẩm), xử lý câu hỏi ngoài phạm vi & từ chối tự checkout/thanh toán.
- **Guardrails & Chống Prompt Injection (`internal/guardrail/`)**:
  - Kiểm tra đầu vào trực tiếp (`CheckUserInput`): giới hạn 4.000 ký tự, lọc ký tự ẩn Unicode zero-width, phát hiện 6 nhóm tấn công tiếng Việt & tiếng Anh (`instruction_override`, `jailbreak`, `prompt_exfiltration`, `secret_exfiltration`, `privilege_escalation`, `delimiter_spoofing`) và giải mã Base64 lồng nhau.
  - Làm sạch dữ liệu không tin cậy (`SanitizeUntrustedData`): lọc và thay thế chỉ dẫn tiêm trong mô tả sản phẩm (`product_description`), đánh giá và tin nhắn chat của khách (`chat_message`) bằng `[nội dung không hợp lệ đã bị lọc]` trước khi đưa vào prompt nằm trong thẻ `<untrusted_*>`.
  - Lọc đầu ra (`FilterOutput`): che JWT (`eyJ...`), Bearer token, API key (`sk-ant-...`, `pa-...`), DSN (`postgres://...`, `redis://...`), marker system prompt nội bộ.
  - Kiểm tra căn cứ (`VerifyGrounding`): phát hiện mã sản phẩm/SKU bịa hoặc rò rỉ khác gian hàng.
- **RAG sản phẩm theo gian hàng (`internal/retriever/`)**:
  - `ChainedRetriever`: ưu tiên gọi gRPC `searchv1.SearchServiceClient` (`Search` chế độ `hybrid` trên pgvector + Meilisearch của Search service, và `SimilarProducts`), dự phòng Gateway REST `GET /api/v1/search` và `productv1.ProductServiceClient` (`ListProducts`/`GetProduct`). Toàn bộ lời gọi ra ngoài có timeout, retry có backoff và circuit breaker (`internal/resilience/`).
  - **Cô lập gian hàng chặt chẽ**: khi có `shop_id`, luôn truyền `shop_id` lên upstream **và** hậu kiểm lọc bỏ mọi sản phẩm không khớp `shop_id`, đồng thời làm sạch mô tả chống indirect prompt injection.
  - Không lưu bản sao vector sản phẩm thứ hai (đúng hợp đồng `m-all-contract.md`).
- **RAG tài liệu chính sách trong repo (`policies/`, `internal/policy/`)**:
  - 6 tài liệu chính sách tiếng Việt nhúng (`//go:embed`, hỗ trợ `Reload` từ thư mục cấu hình `ASSISTANT_POLICIES_DIR`): `returns.md`, `shipping.md`, `payment.md`, `seller-fees.md`, `promotions.md`, `privacy-security.md` bám sát `docs/spec.md`.
  - Truy hồi hybrid không phân biệt dấu tiếng Việt (`NormalizeVietnamese`, BM25 + hash vector 256 chiều xác định + RRF), trả trích dẫn chuẩn `/policies/<slug>#<anchor>`.
- **Function Calling với JWT của người dùng (`internal/tools/`)**:
  - 6 tool: `search_products`, `search_policies`, `get_order_status`, `add_to_cart` (có bước xác nhận trước khi gọi `POST /api/v1/cart/items`), `get_seller_report` (chỉ có trong allowlist khi `role == "seller"` và có `SellerShopID` từ JWT), `handoff_to_agent` (gọi `POST /api/v1/chat/conversations` với `shop_id` & `initial_message`).
  - **Không có quyền riêng**: `HTTPGatewayClient` chỉ chuyển tiếp `Authorization: Bearer <user_jwt>` nguyên trạng của người dùng cuối; không sử dụng service token, không mở tool checkout/thanh toán hay sinh SQL.
- **Tính năng cho người bán & tóm tắt đánh giá (`internal/service/`, `internal/consumer/`)**:
  - Tóm tắt đánh giá sản phẩm (`GetReviewSummary`, `RefreshReviewSummary` + consumer NATS JetStream idempotent có DLQ cho `review.created` / `product.review_created`): ngưỡng tối thiểu 10 đánh giá (cấu hình `ASSISTANT_MIN_REVIEWS_SUMMARY`), cache Redis + Postgres, giữ bản cũ nếu LLM lỗi.
  - Sinh mô tả sản phẩm (`DescribeProduct`): từ chối đầu vào quá ít thông tin (UC-AI06), cache câu trả lời lặp lại.
  - Gợi ý trả lời chat cho người bán (`SuggestReply`): lọc injection từ tin khách & lịch sử chat (gắn `injection_detected`), kiểm tra sản phẩm thuộc đúng shop người bán, không tự gửi tin, đề xuất hỏi lại khi thiếu thông tin (`needs_more_info`).
  - Báo cáo kinh doanh người bán (`SellerReport`): tổng hợp số liệu VND số nguyên trực tiếp từ API đơn hàng & số dư của gian hàng.
- **Hạn mức token, giới hạn chi phí ngày, cache & chống lạm dụng (`internal/quota/`)**:
  - Redis Lua script nguyên tử cho hạn mức token theo người dùng mỗi ngày (`ASSISTANT_DAILY_USER_TOKEN_LIMIT`, múi giờ `Asia/Ho_Chi_Minh`), giới hạn chi phí toàn hệ thống theo ngày tính bằng VND (`ASSISTANT_DAILY_COST_LIMIT_VND`, chuyển sang `LLM_FALLBACK_MODEL` hoặc trả 429 `BUDGET_EXCEEDED`), khoá tạm chat khi phát hiện tấn công prompt injection lặp lại (`ASSISTANT_ABUSE_STRIKE_LIMIT`), và cache câu trả lời lặp lại cho truy vấn không chứa dữ liệu riêng tư (`assistant:cache:*`).
- **Lưu trữ & Observability (`migrations/`, `internal/store/`, `internal/httpserver/`, `internal/grpcserver/`, `cmd/server/`, `Dockerfile`)**:
  - Migration có phiên bản cho `assistant_db` (8 bảng), tự động che PII (email, SĐT Việt Nam, JWT/API key) trước khi lưu hội thoại/phản hồi (`RedactPII`).
  - Log JSON có `trace_id`/`span_id` (OpenTelemetry) và che secret; metric RED + metric riêng (`assistant_ttft_seconds`, `assistant_tokens_total`, `assistant_cost_vnd_total`, `assistant_cache_requests_total`, `assistant_guardrail_blocks_total`, `assistant_tool_calls_total`).
  - `Dockerfile` build nhiều giai đoạn (`golang:1.22-alpine` -> `alpine:3.20`), chạy bằng user non-root `appuser` (uid `10001`).

### 1.3 Tích hợp Gateway & Cấu hình dùng chung (chỉ thêm phần sở hữu)
- `services/gateway/internal/router/handlers_assistant.go` & `router_assistant_test.go`:
  - `POST /api/v1/assistant/chat` (SSE proxy với `FlusherTraceMiddleware`, `FlushInterval: -1`, `X-Accel-Buffering: no`, `Auth(required=false)` + rate limit IP/User, xoá header giả mạo `X-User-*`, chỉ chuyển tiếp Bearer JWT).
  - `POST /api/v1/assistant/handoff`, `POST /api/v1/assistant/feedback` (JWT bắt buộc).
  - `POST /api/v1/seller/assistant/report`, `POST /api/v1/seller/assistant/describe-product`, `POST /api/v1/seller/assistant/suggest-reply` (`RequireRoles("seller")` + `RequireSellerShop()`).
  - `GET /api/v1/products/{id}/review-summary` (công khai/optional-auth, đã kiểm tra không che tuyến `GET /api/v1/products/{id}`).
- `services/gateway/internal/router/router.go`: thêm đúng 1 dòng `mountAssistantRoutes(r, deps)`.
- `docker-compose.yml`: thêm block service `assistant` (bind `127.0.0.1:${ASSISTANT_HTTP_PORT:-8091}:8091`, gRPC `50061` nội bộ) và biến `ASSISTANT_GRPC_ADDR`/`ASSISTANT_HTTP_URL` cho `gateway` & `e2e`.
- `Makefile`: bổ sung `services/assistant` và `evals/assistant` vào `GO_MODULES`, thêm target `eval-assistant`.
- `.env.example`: bổ sung biến môi trường cho Swarm C với giá trị giả/mock (không có key thật).

### 1.4 Bộ đánh giá định lượng & Prompt Injection (`evals/assistant/`)
- `evals/assistant/dataset.json`: **100 câu tiếng Việt** (`EVAL-001`..`EVAL-100`): 30 `product_rag`, 20 `anti_hallucination` (10 bẫy cross-shop + 10 sản phẩm không tồn tại), 20 `policy_qa`, 20 `tool_calling` (tra đơn, thêm giỏ, báo cáo người bán, chuyển nhân viên + phân quyền), 10 `out_of_scope`.
- `evals/assistant/prompt_injection.json`: **36 ca prompt injection** (`INJ-001`..`INJ-036`): 14 ca `direct`, 12 ca gián tiếp trong `product_description`, 10 ca gián tiếp trong `chat_message` (gợi ý trả lời cho người bán).
- `evals/assistant/cmd/evalrunner/main.go`, `runner.go`, `runner_test.go`, `README.md` (dùng facade công khai `services/assistant/pkg/evalharness` chạy stack SSE + `SuggestReply` thật trong tiến trình, hỗ trợ `--mode=mock` và `--mode=anthropic`).

---

## 2. Lệnh kiểm tra và đầu ra thật

### 2.1 Proto lint, gofmt, golangci-lint, unit test `-race`, và `docker compose config`
Lệnh đã chạy:
```bash
buf lint --path proto/nexusmart/assistant
gofmt -l services/assistant evals/assistant services/gateway/internal/router/handlers_assistant.go services/gateway/internal/router/router_assistant_test.go services/gateway/internal/router/router.go
(cd services/assistant && golangci-lint run --allow-parallel-runners -c ../../.golangci.yml ./...)
(cd evals/assistant && golangci-lint run --allow-parallel-runners -c ../../.golangci.yml ./...)
(cd services/gateway && golangci-lint run --allow-parallel-runners -c ../../.golangci.yml ./internal/router/...)
(cd services/assistant && go test -race -count=1 ./...)
(cd evals/assistant && go test -race -count=1 ./...)
(cd services/gateway && go test -race -count=1 ./internal/router/...)
docker compose config --quiet
```

Đầu ra thật (exit code 0, 0 cảnh báo lint):
```text
?   	github.com/nexusmart/core/services/assistant/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/assistant/internal/config	[no test files]
ok  	github.com/nexusmart/core/services/assistant/internal/consumer	1.020s
ok  	github.com/nexusmart/core/services/assistant/internal/grpcserver	1.017s
ok  	github.com/nexusmart/core/services/assistant/internal/guardrail	1.040s
ok  	github.com/nexusmart/core/services/assistant/internal/httpserver	1.022s
ok  	github.com/nexusmart/core/services/assistant/internal/llm	1.039s
ok  	github.com/nexusmart/core/services/assistant/internal/policy	1.033s
?   	github.com/nexusmart/core/services/assistant/migrations	[no test files]
?   	github.com/nexusmart/core/services/assistant/pkg/evalharness	[no test files]
?   	github.com/nexusmart/core/services/assistant/policies	[no test files]
ok  	github.com/nexusmart/core/services/assistant/internal/quota	1.023s
ok  	github.com/nexusmart/core/services/assistant/internal/resilience	1.228s
ok  	github.com/nexusmart/core/services/assistant/internal/retriever	1.179s
ok  	github.com/nexusmart/core/services/assistant/internal/service	1.020s
ok  	github.com/nexusmart/core/services/assistant/internal/store	1.011s
ok  	github.com/nexusmart/core/services/assistant/internal/tools	1.119s
?   	github.com/nexusmart/core/evals/assistant/cmd/evalrunner	[no test files]
ok  	github.com/nexusmart/core/evals/assistant	3.359s
ok  	github.com/nexusmart/core/services/gateway/internal/router	1.126s
```

### 2.2 Bộ đánh giá định lượng 100 câu tiếng Việt + 36 ca Prompt Injection + SSE TTFT (mock)
Lệnh đã chạy:
```bash
cd evals/assistant && go run ./cmd/evalrunner --mode=mock
```

Đầu ra thật (exit code 0):
```text
# NexusMart AI Assistant Evaluation Report (mock / mock)

| Metric | Value | Target | Status |
| --- | ---: | ---: | :---: |
| Dataset Pass Rate | 100/100 (100.0%) | >= 95.0% | PASS |
| Grounded Rate | 100.0% | >= 95.0% | PASS |
| Product Hallucination Rate | 0.0% | 0.0% | PASS |
| Shop Isolation Rate | 100.0% | 100.0% | PASS |
| Policy Citation Rate | 100.0% | >= 95.0% | PASS |
| Tool & Auth Rate | 100.0% | >= 95.0% | PASS |
| Prompt Injection Defense (All) | 36/36 (100.0%) | 100.0% | PASS |
| - Injection: `direct` | 14/14 (100.0%) | 100.0% | PASS |
| - Injection: `product_description` | 12/12 (100.0%) | 100.0% | PASS |
| - Injection: `chat_message` | 10/10 (100.0%) | 100.0% | PASS |
| SSE TTFT (min / p50 / p95 / max) | 0 / 0 / 1 / 2 ms (n=126) | p95 < 2000 ms | PASS |

**ALL QUALITY GATES PASSED.**
```

### 2.3 Integration test với Postgres & Redis tạm + Build & Smoke test Docker image non-root
Chạy với container riêng `c-assistant-test-pg` (`postgres:16-alpine`), `c-assistant-test-redis` (`redis:7-alpine`), image `c-assistant-test-img`, và container ứng dụng `c-assistant-test-app` trên mạng tạm `c-assistant-test-net` (toàn bộ đã được xoá sạch sau khi chạy):
```text
=== 1. Start temporary Postgres (tmpfs) & Redis containers ===
Postgres & Redis ready after 1s
=== 2. Real Postgres & Redis integration tests ===
=== RUN   TestPostgresStoreIntegration
--- PASS: TestPostgresStoreIntegration (0.05s)
=== RUN   TestRealRedisQuotaIntegration
--- PASS: TestRealRedisQuotaIntegration (0.08s)
PASS
ok  	github.com/nexusmart/core/services/assistant/internal/store	1.136s
=== 4. Verify non-root user in image ===
uid=10001(appuser) gid=10001(appuser) groups=10001(appuser)
=== 5. Run assistant container & smoke test healthz/readyz/metrics/SSE ===
--- GET /healthz ---
{"service":"assistant","status":"ok"}
--- GET /readyz ---
{"service":"assistant","status":"ready"}
--- POST /assistant/chat (live SSE smoke test over Docker) ---
event: tool_call
data: {"id":"toolu_mock_1","name":"search_policies","arguments":{"query":"Chính sách đổi trả hàng trên NexusMart là bao nhiêu ngày?"}}
...
event: done
data: {"conversation_id":"6a53a293-8162-482e-8639-b4f566cd114e","message_id":"f3c8a5ae-ca1c-4657-ade6-852d70525f00","model":"mock","usage":{"input_tokens":2013,"output_tokens":695,"total_tokens":2708,"cost_vnd":2087},"ttft_ms":190,"latency_ms":192,"cache_hit":false,"citations":["/policies/returns#van-don-chieu-nguoc-va-thoi-han-gui-tra-hang","/policies/returns#thoi-han-va-dieu-kien-doi-tra","/policies/returns#quy-trinh-phan-hoi-cua-nguoi-ban","/policies/returns#chinh-sach-huy-don-hang"]}
--- GET /metrics (sample after SSE chat) ---
assistant_cache_requests_total{endpoint="chat",result="miss"} 1
assistant_cost_vnd_total{endpoint="chat",model="mock"} 2087
assistant_tokens_total{direction="input",endpoint="chat",model="mock"} 2013
assistant_tokens_total{direction="output",endpoint="chat",model="mock"} 695
assistant_tool_calls_total{status="ok",tool="search_policies"} 1
assistant_ttft_seconds_bucket{cache_hit="false",endpoint="chat",model="mock",le="0.25"} 1
DOCKER_INTEGRATION_AND_IMAGE_PASSED
```
Xác nhận dọn dẹp: `docker ps -a --filter name=c-assistant-test` và `docker images c-assistant-test-img` đều rỗng.

---

## 3. Cách đo live với Claude API (`LLM_MODE=anthropic`)

Khi có `ANTHROPIC_API_KEY` hợp lệ (không commit key vào repo):

1. **Chạy bộ eval 100 câu + 36 ca prompt injection live và đo TTFT p50/p95/max**:
   ```bash
   cd evals/assistant
   ANTHROPIC_API_KEY="<your-key>" LLM_MODEL="claude-opus-5-5" \
     go run ./cmd/evalrunner --mode=anthropic --out=/tmp/assistant-eval-live.json
   # Hoặc smoke nhanh 10 câu:
   ANTHROPIC_API_KEY="<your-key>" LLM_MODEL="claude-opus-5-5" \
     go run ./cmd/evalrunner --mode=anthropic --limit=10
   ```
2. **Đo TTFT trực tiếp qua SSE endpoint của service/gateway**:
   ```bash
   # Khởi động assistant với LLM_MODE=anthropic ANTHROPIC_API_KEY=... LLM_MODEL=claude-opus-5-5
   curl -N -s -w "\ntime_starttransfer=%{time_starttransfer}s total=%{time_total}s\n" \
     -X POST http://127.0.0.1:28080/api/v1/assistant/chat \
     -H "Content-Type: application/json" \
     -H "Authorization: Bearer <USER_JWT>" \
     -d '{"shop_id":"<SHOP_ID>","messages":[{"role":"user","content":"Tìm áo thun cotton dưới 250k"}]}'
   # Trường `ttft_ms` trong event `done` và histogram Prometheus `assistant_ttft_seconds` ghi lại chính xác thời điểm token đầu tiên được flush.
   ```

---

## 4. Đối chiếu tiêu chí đo trong Spec & Brief

| Tiêu chí (`docs/spec.md` & `.agents/briefs/m-c-assistant.md`) | Kết quả | Bằng chứng |
|---|---|---|
| RAG sản phẩm đúng gian hàng qua gRPC `search` (không giữ bản vector thứ hai) | **Đạt** | `internal/retriever/grpc_search.go` gọi `searchv1.SearchServiceClient` (`Search` hybrid, `SimilarProducts`), hậu kiểm `shop_id`, dự phòng Gateway/Product; `Shop Isolation Rate = 100.0%`. |
| Function calling: tra đơn, thêm giỏ, báo cáo người bán, hỏi đáp chính sách trong repo, chuyển nhân viên | **Đạt** | `internal/tools/registry.go`, 6 tài liệu trong `services/assistant/policies/`, tích hợp `POST /api/v1/chat/conversations` (`initial_message`), unit test + 20 ca tool trong eval đạt `100.0%`. |
| Tool chạy bằng JWT của người dùng, không có quyền riêng; không tự checkout/SQL | **Đạt** | `internal/tools/gateway_client.go` chỉ chuyển `Authorization: Bearer` từ người dùng; `AllowedTools(role)` ẩn `get_seller_report` khỏi buyer/anonymous; không có tool checkout/payment/SQL. |
| Tóm tắt đánh giá, sinh mô tả sản phẩm, gợi ý trả lời chat cho người bán | **Đạt** | `internal/service/{reviews,seller}.go`, consumer NATS idempotent + DLQ (`internal/consumer/nats.go`), các endpoint REST/gRPC qua gateway đã test xanh. |
| Hạn mức token/ngày theo user (Redis), giới hạn chi phí ngày, cache câu trả lời lặp lại | **Đạt** | `internal/quota/redis_quota.go` dùng Lua script nguyên tử; đã test bằng `miniredis` và container `redis:7-alpine` thật (`TestRealRedisQuotaIntegration`). |
| SSE streaming, token đầu tiên < 2 giây | **Đạt (mock & container)** | Eval SSE (n=126): p50 = `0 ms`, p95 = `1 ms`, max = `2 ms`; container Docker nối Postgres+Redis thật: `ttft_ms = 190 ms` (< 2000 ms). Đã ghi rõ cách đo live ở mục 3. |
| Eval set ~100 câu tiếng Việt đo tỷ lệ căn cứ & tỷ lệ bịa sản phẩm | **Đạt** | `evals/assistant/dataset.json` (100 câu): Grounded `100.0%`, Product Hallucination `0.0%`, Pass `100/100`. |
| Bộ test prompt injection ≥ 30 ca (gồm mô tả sản phẩm & tin chat) | **Đạt** | `evals/assistant/prompt_injection.json` (36 ca: 14 direct, 12 product_description, 10 chat_message): `36/36 (100.0%)`. |
| Lint/format sạch, Docker image nhiều giai đoạn non-root, OTel JSON log & RED metrics | **Đạt** | `buf lint`, `gofmt`, `golangci-lint` = 0 lỗi; image chạy `uid=10001(appuser)`; `/healthz`, `/readyz`, `/metrics` xác nhận trên container thật. |

---

## 5. Phần chưa làm / Chuyển giao cho bước sau (nêu rõ lý do)

1. **Đo live với Anthropic Claude API (`--mode=anthropic`)**: chưa thực thi gọi mạng tới `api.anthropic.com` do không có `ANTHROPIC_API_KEY` thật trong môi trường (không commit secret). Mã `AnthropicProvider` (streaming SSE, `tool_use`, retry/backoff/circuit breaker, tính token & chi phí) đã được unit test đầy đủ qua `httptest.NewServer`; chỉ cần truyền `ANTHROPIC_API_KEY` và chạy lệnh ở mục 3 để lấy số live.
2. **Kiểm thử E2E toàn stack (`make e2e`, `make e2e-web`, rebuild stack compose)**: không chạy theo luật phân công trong `.agents/briefs/m-all-contract.md` (chỉ Swarm H chạy `make e2e` và rebuild stack khi gộp toàn bộ 9 swarm).
3. **Database `assistant_db` khi nâng cấp từ volume Postgres sẵn có**: `services/assistant/cmd/server/main.go` tự động kiểm tra/tạo `assistant_db` nếu chưa tồn tại rồi chạy migration idempotent, nên không cần chỉnh file ngoài phạm vi `infra/postgres/init-dbs.sql`.
