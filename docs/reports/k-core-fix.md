# Báo cáo Swarm K – Sửa lỗi Core, Chat, Assistant sau review (2026-11-21)

- **Brief**: `.agents/briefs/m-k-core-fix.md`, `.agents/briefs/m-all-contract.md`.
- **Phạm vi đã sửa (đúng quyền sở hữu)**: `pkg/common/logger/`, `infra/otel-collector-config.yaml`, `services/gateway/{Dockerfile,cmd/gateway/,internal/proxy/,internal/router/handlers_{chat,assistant}.go,internal/router/router_{chat,assistant}_test.go}`, `services/cart/{Dockerfile,cmd/cart/main.go}`, `services/notification/{Dockerfile,go.mod,internal/consumer/}`, `services/chat/{Dockerfile,internal/ws/}`, `services/assistant/{Dockerfile,internal/httpserver/server_test.go}`, `.dockerignore`, `docs/reports/issues.md`.
- **Không đụng phạm vi cấm**: không sửa `services/user/`, `services/{order,payment,shipping,promotion,search,product,aiops}/`, `deploy/`, `web/`, `tests/`, hay các handler gateway khác.

---

## 1. Phần đã hoàn thành

| ID | Nội dung đã sửa | Tệp chính & Test |
|---|---|---|
| **K-01** | Lưu `deps.TrustedProxies` trong `assistantHandler` (`handlers_assistant.go`), đặt `X-Forwarded-For` bằng IP client đã giải qua `h.trustedProxies.ClientIP(pr.In)` và xoá `X-Real-IP` trước khi gửi sang `assistant`. Mỗi khách vãng lai qua BFF tin cậy có bucket quota `ip:<client>` riêng; header `X-Forwarded-For`/`X-Real-IP` giả từ peer không tin cậy bị bỏ qua. | `services/gateway/internal/router/handlers_assistant.go`, `services/gateway/internal/router/router_assistant_test.go` (`TestGatewayAssistantSSETrustedProxyClientIP`), `services/assistant/internal/httpserver/server_test.go` (`TestHTTPServerAnonymousQuotaIsolatedByForwardedClientIP`) |
| **K-02** | Ưu tiên kiểm tra `?ticket=` trước `Cookie` ở cả gateway (`handlers_chat.go`) và `services/chat` (`internal/ws/handler.go`) để ticket tường minh từ site tên miền riêng không bị cookie host lấn át. Đường `?ticket=` và `Authorization: Bearer` bỏ kiểm tra Origin (`InsecureSkipVerify: true`) để site tên miền riêng mở WebSocket được; đường `Cookie` giữ kiểm tra `OriginPatterns` chặt (gateway gắn header nội bộ `X-Chat-Auth-Method: cookie` chỉ khi xác thực bằng cookie và xoá/ghi đè mọi giá trị client gửi lên). | `services/gateway/internal/router/handlers_chat.go`, `services/gateway/internal/router/router_chat_test.go` (`TestChatWebSocketAuthAnd101Tunnel`), `services/chat/internal/ws/handler.go`, `services/chat/internal/ws/handler_test.go` (`TestWebSocketOriginPolicyTicketVsCookie`) |
| **OBS-01** | Thêm exporter `otlp/tempo` (`endpoint: tempo:4317`, `tls.insecure: true`) và gắn `exporters: [otlp/tempo, debug]` vào pipeline `traces` trong `infra/otel-collector-config.yaml`. Đã xác thực bằng `otel/opentelemetry-collector-contrib:0.108.0 validate`. | `infra/otel-collector-config.yaml` |
| **OBS-03** | Bổ sung 8 key PII còn thiếu (`bank_account`, `account_number`, `full_name`, `recipient_name`, `address`, `id_card`, `cccd`, `tax_code`) vào `sensitiveKeySubstrings` và bổ sung regex che JWT thô (`eyJ...`), email (`MaskEmail`) và SĐT VN/quốc tế (`MaskPhone` có kiểm tra biên ký tự để không làm hỏng trace ID, UUID, timestamp, số tiền, IP:port) vào `ScrubSecrets`. | `pkg/common/logger/logger.go`, `pkg/common/logger/logger_test.go` (`TestScrubSecretsEmbeddedPIIJWTAndExtendedKeys`) |
| **OBS-07** | Mở OTel `SpanKindConsumer` span (`nats.consume <subject>`) trong `Consumer.HandleMessage`, ghi nhận lỗi lên span; cập nhật `DLQPublisher.PublishDLQ(ctx, ...)` và `newDLQMessage` để inject `traceparent`/`tracestate` vào `nats.Msg.Header`; bổ sung `sanitizeDLQPayload` + `logger.ScrubSecrets` để che `otp`, email, SĐT và key PII trước khi lưu vào `dead_letter_events` và phát lên `NEXUSMART_DLQ` (kể cả payload JSON lỗi). | `services/notification/internal/consumer/consumer.go`, `services/notification/internal/consumer/consumer_test.go` (`TestConsumerOpensConsumerSpanPropagatesDLQTraceAndRedactsOTP`), `services/notification/go.mod` |
| **OBS-08** | Xoá nhánh `if tp := pr.In.Header.Get("traceparent")` trong `NewWebhookProxy` để luôn gọi `otel.GetTextMapPropagator().Inject(pr.In.Context(), ...)` giống `NewSSEProxy`, giữ đúng quan hệ parent-child span của gateway và bảo toàn `tracestate`. | `services/gateway/internal/proxy/proxy.go`, `services/gateway/internal/proxy/proxy_test.go` (`TestWebhookProxyPropagatesChildSpanAndTracestate`) |
| **OBS-09** *(phần gateway & cart)* | Đăng ký `collectors.NewProcessCollector` và `collectors.NewGoCollector` trong `services/gateway/cmd/gateway/main.go` để `/metrics` xuất `process_cpu_seconds_total` và `process_resident_memory_bytes`. `services/cart/cmd/cart/main.go` đã dùng `prometheus.DefaultRegisterer`/`DefaultGatherer` sẵn có đủ process & Go collectors. | `services/gateway/cmd/gateway/main.go`, `services/gateway/cmd/gateway/main_test.go` (`TestGatewayMetricsRegistryExposesProcessAndGoCollectors`) |
| **OBS-13** *(phần gateway & cart)* | Thay `fmt.Fprintf(os.Stderr, ...)` khi thoát lỗi bằng `log.Error(...)` qua `logger.New` (JSON + che DSN/secret) ở cả `services/gateway/cmd/gateway/main.go` và `services/cart/cmd/cart/main.go`; đọc `OTEL_EXPORTER_OTLP_INSECURE` qua `env.EnvBool` thay vì hardcode `Insecure: true` trong `services/cart/cmd/cart/main.go`. | `services/gateway/cmd/gateway/main.go`, `services/cart/cmd/cart/main.go` |
| **PLT-04** *(phạm vi sở hữu)* | Cập nhật 5 Dockerfile trong phần sở hữu sang UID/GID số cố định `10001:10001` (gồm đổi `gateway` từ `addgroup -S` / `adduser -S` sang `addgroup -g 10001 -S appgroup && adduser -D -u 10001 -G appgroup appuser` + `USER 10001:10001`, và đổi `USER appuser` → `USER 10001:10001` ở `cart`, `notification`, `chat`, `assistant`). | `services/{gateway,cart,notification,chat,assistant}/Dockerfile` |
| **PLT-06** *(phần `.dockerignore`)* | Thêm `.dockerignore` ở thư mục gốc loại trừ `.git`, `web/node_modules`, `web/.next*`, `services/payment/target`, `services/aiops/target`, `.runs`. Context build Docker của backend giảm còn ~560 KB. | `.dockerignore` |

---

## 2. Lệnh kiểm tra Gate và đầu ra thật

### Lệnh đã chạy
```bash
ROOT="$(pwd)"
MODS="pkg/common services/gateway services/cart services/notification services/chat services/assistant"

gofmt -l $MODS
for m in $MODS; do (cd "$ROOT/$m" && golangci-lint run --allow-parallel-runners -c "$ROOT/.golangci.yml" ./...); done
for m in $MODS; do (cd "$ROOT/$m" && go test -race -count=1 ./...); done
docker compose config -q
docker run --rm -v "$ROOT/infra/otel-collector-config.yaml:/etc/otel-collector-config.yaml:ro" \
  otel/opentelemetry-collector-contrib:0.108.0 validate --config=/etc/otel-collector-config.yaml
for svc in gateway chat assistant; do
  docker build -f "services/$svc/Dockerfile" -t "k-test-$svc" .
  docker inspect --format '{{.Config.User}}' "k-test-$svc"
done
docker rmi -f k-test-gateway k-test-chat k-test-assistant
docker images --format '{{.Repository}}:{{.Tag}}' 'k-test-*'
```

### Đầu ra thật (exit code 0)
```text
=== 1. gofmt -l ===
gofmt OK (empty)
=== 2. golangci-lint ===
--- lint: pkg/common ---
--- lint: services/gateway ---
--- lint: services/cart ---
--- lint: services/notification ---
--- lint: services/chat ---
--- lint: services/assistant ---
golangci-lint OK
=== 3. go test -race -count=1 ===
--- test: pkg/common ---
ok  	github.com/nexusmart/core/pkg/common/auth	1.041s
ok  	github.com/nexusmart/core/pkg/common/env	1.008s
ok  	github.com/nexusmart/core/pkg/common/logger	1.012s
ok  	github.com/nexusmart/core/pkg/common/metrics	1.012s
ok  	github.com/nexusmart/core/pkg/common/telemetry	1.012s
--- test: services/gateway ---
?   	github.com/nexusmart/core/services/gateway/internal/clients	[no test files]
?   	github.com/nexusmart/core/services/gateway/internal/config	[no test files]
ok  	github.com/nexusmart/core/services/gateway/cmd/gateway	1.020s
ok  	github.com/nexusmart/core/services/gateway/internal/httpx	1.012s
ok  	github.com/nexusmart/core/services/gateway/internal/middleware	1.019s
ok  	github.com/nexusmart/core/services/gateway/internal/proxy	1.017s
ok  	github.com/nexusmart/core/services/gateway/internal/router	1.184s
--- test: services/cart ---
?   	github.com/nexusmart/core/services/cart/cmd/cart	[no test files]
?   	github.com/nexusmart/core/services/cart/internal/store	[no test files]
ok  	github.com/nexusmart/core/services/cart/internal/resilience	1.119s
ok  	github.com/nexusmart/core/services/cart/internal/server	1.206s
--- test: services/notification ---
?   	github.com/nexusmart/core/services/notification/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/config	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/db	[no test files]
?   	github.com/nexusmart/core/services/notification/internal/store	[no test files]
?   	github.com/nexusmart/core/services/notification/migrations	[no test files]
ok  	github.com/nexusmart/core/services/notification/internal/consumer	1.017s
ok  	github.com/nexusmart/core/services/notification/internal/grpcserver	1.023s
ok  	github.com/nexusmart/core/services/notification/internal/mailer	1.009s
ok  	github.com/nexusmart/core/services/notification/internal/service	1.034s
ok  	github.com/nexusmart/core/services/notification/internal/userclient	1.017s
--- test: services/chat ---
?   	github.com/nexusmart/core/services/chat/cmd/loadtest	[no test files]
?   	github.com/nexusmart/core/services/chat/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/chat/internal/config	[no test files]
?   	github.com/nexusmart/core/services/chat/internal/metrics	[no test files]
?   	github.com/nexusmart/core/services/chat/internal/userclient	[no test files]
?   	github.com/nexusmart/core/services/chat/migrations	[no test files]
ok  	github.com/nexusmart/core/services/chat/internal/db	1.010s
ok  	github.com/nexusmart/core/services/chat/internal/grpcserver	1.014s
ok  	github.com/nexusmart/core/services/chat/internal/hub	1.163s
ok  	github.com/nexusmart/core/services/chat/internal/integration	1.015s
ok  	github.com/nexusmart/core/services/chat/internal/service	1.017s
ok  	github.com/nexusmart/core/services/chat/internal/store	1.008s
ok  	github.com/nexusmart/core/services/chat/internal/ticket	1.013s
ok  	github.com/nexusmart/core/services/chat/internal/validator	1.015s
ok  	github.com/nexusmart/core/services/chat/internal/ws	1.031s
--- test: services/assistant ---
?   	github.com/nexusmart/core/services/assistant/cmd/server	[no test files]
?   	github.com/nexusmart/core/services/assistant/internal/config	[no test files]
?   	github.com/nexusmart/core/services/assistant/migrations	[no test files]
?   	github.com/nexusmart/core/services/assistant/pkg/evalharness	[no test files]
?   	github.com/nexusmart/core/services/assistant/policies	[no test files]
ok  	github.com/nexusmart/core/services/assistant/internal/consumer	1.015s
ok  	github.com/nexusmart/core/services/assistant/internal/grpcserver	1.014s
ok  	github.com/nexusmart/core/services/assistant/internal/guardrail	1.039s
ok  	github.com/nexusmart/core/services/assistant/internal/httpserver	1.028s
ok  	github.com/nexusmart/core/services/assistant/internal/llm	1.045s
ok  	github.com/nexusmart/core/services/assistant/internal/policy	1.036s
ok  	github.com/nexusmart/core/services/assistant/internal/quota	1.026s
ok  	github.com/nexusmart/core/services/assistant/internal/resilience	1.228s
ok  	github.com/nexusmart/core/services/assistant/internal/retriever	1.181s
ok  	github.com/nexusmart/core/services/assistant/internal/service	1.023s
ok  	github.com/nexusmart/core/services/assistant/internal/store	1.014s
ok  	github.com/nexusmart/core/services/assistant/internal/tools	1.120s
=== 4. docker compose config -q ===
docker compose config OK
=== 4b. otel-collector config validate ===
otel-collector validate OK
=== 5. Docker build & verify USER 10001:10001 (gateway, chat, assistant) ===
--- build k-test-gateway ---
k-test-gateway: user=10001:10001 size_bytes=28923631
--- build k-test-chat ---
k-test-chat: user=10001:10001 size_bytes=32335573
--- build k-test-assistant ---
k-test-assistant: user=10001:10001 size_bytes=33837378
=== 6. Confirm k-test-* images removed ===
ALL_GATES_PASSED
```

---

## 3. Phần chưa làm / Để lại cho bước sau (đúng theo brief)

1. **`OBS-09` & `OBS-13` phần `services/user/`**: không chỉnh `services/user/cmd/user/main.go` (và `services/user/Dockerfile` cho `PLT-04`) vì Swarm T đang sở hữu `services/user/` theo brief. Cần thực hiện sau khi Swarm T hoàn tất:
   - `services/user/cmd/user/main.go`: đăng ký `ProcessCollector`/`GoCollector` (hoặc dùng `DefaultRegisterer`), gắn HTTP RED/trace middleware cho `opsSrv`, và thay `fmt.Fprintf(os.Stderr, ...)` bằng `log.Error(...)` qua `logger.New`.
   - `services/user/Dockerfile`: chuyển sang `adduser -D -u 10001` và `USER 10001:10001`.
2. **`PLT-06` nâng Go toolchain (`golang:1.22-alpine`)**: không nâng đợt này theo đúng yêu cầu trong brief.
3. **Không rebuild stack, không chạy `make e2e`**: tuân thủ luật dùng chung (`m-all-contract.md` và brief K); chỉ Swarm H chạy E2E toàn stack sau khi gộp.
