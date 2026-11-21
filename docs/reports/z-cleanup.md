# Báo cáo Swarm Z – Dọn việc nhỏ sau review (2026-11-21)

- **Brief**: `.agents/briefs/m-z-cleanup.md`, `.agents/briefs/m-all-contract.md`.
- **Phạm vi đã sửa (đúng quyền sở hữu)**:
  - `deploy/k8s/create-secrets.sh`, `deploy/k8s/README.md`
  - `services/user/cmd/user/main.go`, `services/user/cmd/user/main_test.go`
  - `web/src/features/assistant/assistant.test.mjs`, `web/src/features/seller/site/siteSummary.test.mjs`
  - `docs/reports/issues.md`, `docs/reports/z-cleanup.md`
- **Không đụng phạm vi cấm**: không sửa bất kỳ thư mục nào của các swarm song song (`A`, `E`, `H` hay các service/chart/file web khác).

---

## 1. Công việc đã hoàn thành

### 1.1 `deploy/k8s/create-secrets.sh` (+ cập nhật ví dụ trong `deploy/k8s/README.md`)
- Bổ sung kiểm tra production fail-fast khớp `services/payment/src/config.rs::validate()` và `deploy/helm/payment/values-production.yaml` (`APP_ENV: "production"`, `VIETQR_MODE: "payos"`, `ZALOPAY_MODE: "sandbox"`):
  - Trong `nexusmart-production`, bắt buộc phải truyền qua biến môi trường và không được là giá trị rỗng/placeholder/hằng dev-mock của repo (`is_placeholder_secret`): `POSTGRES_PASSWORD`, `JWT_SECRET`, `SERVICE_JWT_SECRET`, `ADMIN_BOOTSTRAP_PASSWORD`, `PAYMENT_WEBHOOK_SECRET`, `ZALOPAY_KEY1`, `ZALOPAY_KEY2`, `SHIPPING_WEBHOOK_SECRET`, `MEILI_MASTER_KEY`.
  - Theo `VIETQR_MODE` (mặc định `payos` trong `nexusmart-production` khớp `values-production.yaml`, mặc định `mock` ở staging):
    - `VIETQR_MODE=payos`: bắt buộc đủ `PAYOS_CLIENT_ID`, `PAYOS_API_KEY`, `PAYOS_CHECKSUM_KEY` (không rỗng/placeholder).
    - `VIETQR_MODE=sepay`: bắt buộc `SEPAY_API_TOKEN` (không rỗng/placeholder).
    - Giá trị khác (vd `mock`, `sandbox`) ở `nexusmart-production`: báo lỗi rõ ràng và thoát mã lỗi `1`.
  - Báo lỗi gom đủ mọi biến thiếu/placeholder ra `stderr` trước khi thoát mã `1`.
- Bổ sung nạp `--from-literal="ZALOPAY_KEY1=${ZALOPAY_KEY1}"` và `--from-literal="ZALOPAY_KEY2=${ZALOPAY_KEY2}"` vào `nexusmart-payment-secret` (cùng `PAYMENT_WEBHOOK_SECRET`, `PAYOS_*`, `SEPAY_API_TOKEN`). Staging/local giữ nguyên hành vi tự sinh ngẫu nhiên và để trống các khóa provider khi ở chế độ mock. Không có bất kỳ giá trị secret thật nào trong repo.

### 1.2 OBS-09 và OBS-13 phần `user` (`services/user/cmd/user/main.go`)
- Làm theo đúng mẫu Swarm K đã áp dụng ở `services/gateway/cmd/gateway/main.go`:
  - Khởi tạo `cfg := config.Load()` và `log := logger.New(...)` trong `main()`, ghi lỗi fatal bằng `log.Error("user service exited with error", slog.String("error", err.Error()))` (JSON có `"service":"user"` và che DSN/secret) thay cho `fmt.Fprintf(os.Stderr, ...)`.
  - Tách `newMetricsRegistry()` đăng ký `collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})` và `collectors.NewGoCollector()` để `/metrics` xuất `process_cpu_seconds_total`, `process_resident_memory_bytes`, `go_goroutines`.
  - Tách `newOpsHandler(...)` bọc `metrics.NewOpsHandler(...)` bằng `telemetry.HTTPMiddleware(serviceName)` và `red.HTTPMiddleware()` cho `opsSrv`.
  - Giữ nguyên lời gọi `service.BootstrapSiteTemplates(ctx, store, log)` của Swarm T.
- Bổ sung unit test `TestUserOpsHandlerExposesCollectorsREDAndTraceMiddleware` tại `services/user/cmd/user/main_test.go` kiểm tra `X-Trace-Id` từ `traceparent`, `process_*` / `go_goroutines`, và `http_requests_total` / `http_request_duration_seconds_count` trên `/metrics`.
- Đã đánh dấu `OBS-09` và `OBS-13` phần `user` là "đã sửa bởi Z" trong `docs/reports/issues.md`.

### 1.3 Định dạng Prettier cho 2 file test web trong phạm vi sở hữu
- Chạy `npx prettier --write src/features/assistant/assistant.test.mjs src/features/seller/site/siteSummary.test.mjs` trong `web/`.

---

## 2. Lệnh kiểm tra Gate và đầu ra thật

### 2.1 `deploy/k8s/create-secrets.sh` (`bash -n`, `shellcheck`, và kịch bản production/staging với `kubectl` giả tạm thời)

Lệnh:
```bash
bash -n deploy/k8s/create-secrets.sh
command -v shellcheck || echo "shellcheck: not installed"
# + 7 kịch bản kiểm tra:
#   Case A: nexusmart-production thiếu PAYMENT_WEBHOOK_SECRET, ZALOPAY_KEY1/2, PAYOS_*
#   Case B: nexusmart-production dùng giá trị placeholder/hằng repo
#   Case C: nexusmart-production đặt VIETQR_MODE=mock
#   Case D: nexusmart-production đặt VIETQR_MODE=sepay nhưng thiếu SEPAY_API_TOKEN
#   Case E: nexusmart-production hợp lệ với VIETQR_MODE=payos (mặc định) -> xác minh nạp vào nexusmart-payment-secret
#   Case F: nexusmart-production hợp lệ với VIETQR_MODE=sepay -> xác minh nạp vào nexusmart-payment-secret
#   Case G: nexusmart-staging không đặt biến môi trường -> tạo secret thành công với ZALOPAY_KEY1/2 rỗng
```

Đầu ra thật (exit code 0):
```text
=== 1. bash -n & shellcheck ===
bash -n OK
shellcheck: not installed (skipped per brief)
=== 2. Functional tests for deploy/k8s/create-secrets.sh ===
Case A (missing payment keys) rc=1
ERROR: PAYMENT_WEBHOOK_SECRET must be explicitly provided via environment for nexusmart-production (required by payment Config::validate)
ERROR: ZALOPAY_KEY1 must be explicitly provided via environment for nexusmart-production (required by payment Config::validate)
ERROR: ZALOPAY_KEY2 must be explicitly provided via environment for nexusmart-production (required by payment Config::validate)
ERROR: PAYOS_CLIENT_ID must be explicitly provided via environment for nexusmart-production (required when VIETQR_MODE=payos)
ERROR: PAYOS_API_KEY must be explicitly provided via environment for nexusmart-production (required when VIETQR_MODE=payos)
ERROR: PAYOS_CHECKSUM_KEY must be explicitly provided via environment for nexusmart-production (required when VIETQR_MODE=payos)
Case B (placeholder secrets) rc=1
ERROR: PAYMENT_WEBHOOK_SECRET must not use a placeholder or dev/mock value for nexusmart-production (required by payment Config::validate)
ERROR: ZALOPAY_KEY1 must not use a placeholder or dev/mock value for nexusmart-production (required by payment Config::validate)
ERROR: ZALOPAY_KEY2 must not use a placeholder or dev/mock value for nexusmart-production (required by payment Config::validate)
ERROR: PAYOS_CHECKSUM_KEY must not use a placeholder or dev/mock value for nexusmart-production (required when VIETQR_MODE=payos)
Case C (VIETQR_MODE=mock in prod) rc=1
ERROR: VIETQR_MODE must be 'payos' or 'sepay' for nexusmart-production (got 'mock')
Case D (VIETQR_MODE=sepay missing token) rc=1
ERROR: SEPAY_API_TOKEN must be explicitly provided via environment for nexusmart-production (required when VIETQR_MODE=sepay)
Case E (valid prod payos) PASS
Case F (valid prod sepay) PASS
Case G (staging defaults) PASS
```

### 2.2 `services/user`: `gofmt`, `golangci-lint`, `go test -race -count=1`, và xác minh JSON fatal log (OBS-13)

Lệnh:
```bash
ROOT="$(pwd)"
gofmt -l services/user
(cd "$ROOT/services/user" && golangci-lint run --allow-parallel-runners -c "$ROOT/.golangci.yml" ./...)
(cd "$ROOT/services/user" && go test -race -count=1 ./...)
# + build binary tạm (đã xoá qua trap) chạy với DATABASE_URL sai mật khẩu để xác minh JSON log & redaction
```

Đầu ra thật (exit code 0):
```text
=== 3. services/user: gofmt, golangci-lint, go test -race ===
gofmt -l services/user: OK (empty)
golangci-lint services/user: OK
?   	github.com/nexusmart/core/services/user/internal/config	[no test files]
?   	github.com/nexusmart/core/services/user/internal/events	[no test files]
?   	github.com/nexusmart/core/services/user/migrations	[no test files]
ok  	github.com/nexusmart/core/services/user/cmd/user	1.018s
ok  	github.com/nexusmart/core/services/user/internal/cloudflare	1.038s
ok  	github.com/nexusmart/core/services/user/internal/db	1.009s
ok  	github.com/nexusmart/core/services/user/internal/password	1.185s
ok  	github.com/nexusmart/core/services/user/internal/security	1.007s
ok  	github.com/nexusmart/core/services/user/internal/service	2.438s
ok  	github.com/nexusmart/core/services/user/internal/site	1.302s
=== 3b. Verify OBS-13 JSON fatal log scrubbing on services/user ===
user fatal rc=1
{"time":"2026-11-21T16:21:55.386367166+07:00","level":"ERROR","msg":"user service exited with error","service":"user","error":"ping postgres: failed to connect to `user=u database=user_db`:\n\t127.0.0.1:1 (127.0.0.1): dial error: dial tcp 127.0.0.1:1: connect: connection refused\n\t127.0.0.1:1 (127.0.0.1): dial error: dial tcp 127.0.0.1:1: connect: connection refused"}
OBS-13 JSON fatal log verified PASS
```

### 2.3 `web`: Prettier và unit test cho 2 file test web

Lệnh:
```bash
cd web && npx prettier --write src/features/assistant/assistant.test.mjs src/features/seller/site/siteSummary.test.mjs
cd web && npx prettier --check src
node --test web/src/features/assistant/assistant.test.mjs web/src/features/seller/site/siteSummary.test.mjs
```

Đầu ra thật (exit code 0):
```text
src/features/assistant/assistant.test.mjs 57ms
src/features/seller/site/siteSummary.test.mjs 4ms

Checking formatting...
All matched files use Prettier code style!

✔ createSseParser parses token, tool_call, tool_result, done, and error events
✔ createSseParser handles arbitrary chunk splits, CRLF split across chunks, and multi-line data
✔ createSseParser falls back gracefully for plain-text token data without explicit event header
✔ extractToolCardData sanitizes unsafe URLs and builds safe storefront links
✔ extractToolCardData pulls pending add-to-cart confirmation, orders, and safe policy citations
✔ normalizeReviewSummary & shouldDisplayReviewSummary enforce >= 10 reviews and non-empty content
✔ normalizeSellerReport maps proto metrics and generic rows into clean KPIs and tables
✔ normalizeDescribeProduct & normalizeSuggestReply handle both object and string payloads
✔ validators reject malformed inputs and sanitizeAssistantError hides technical jargon
✔ buildPublicSiteUrl keeps the site host and borrows only the request port
✔ buildPublicSiteUrl rejects empty or unsafe hosts
✔ summarizeSellerSite maps backend site status to the seller-facing state
ℹ tests 12
ℹ suites 0
ℹ pass 12
ℹ fail 0
```

---

## 3. Ghi chú / Phần không thuộc phạm vi
- `shellcheck` chưa được cài đặt trên máy (`command -v shellcheck` rỗng); brief chỉ yêu cầu "`shellcheck` nếu có". Script đã qua `bash -n` và kiểm thử chức năng 7 kịch bản.
- Không commit, không đổi nhánh, không rebuild stack, không chạy `make e2e`/`make e2e-web` theo `m-all-contract.md`.
