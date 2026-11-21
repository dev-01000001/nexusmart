# Swarm E – Báo cáo Observability và AIOps (`e-aiops`)

Ngày nghiệm thu: 2026-11-21
Nhánh làm việc: `phase-0b` (không commit, không đổi nhánh theo hợp đồng `m-all-contract.md`).

## 1. Phạm vi đã hoàn thành

### 1.1. Stack Observability (`deploy/observability/` + profile `obs` trong `docker-compose.yml`)
- **Prometheus** (`deploy/observability/prometheus/prometheus.yml`, cổng `127.0.0.1:9090`):
  - Chu kỳ scrape/eval `10s`; scrape toàn bộ 13 service (`gateway:8080`, `user:8081`, `product:8082`, `cart:8083`, `promotion:8084`, `order:8085`, `payment:8086`, `shipping:8087`, `notification:8088`, `search:8089`, `chat:8090`, `assistant:8091`, `aiops:8092`), `otel-collector:8889`, `prometheus:9090` và `cadvisor:8080` (lọc riêng project `nexus_ecom` và ánh xạ `container_label_com_docker_compose_service -> service`).
- **Quy tắc SLO & Error Budget dưới dạng code** (`deploy/observability/prometheus/rules/slo.yml`, 24 rules xác thực bằng `promtool`):
  - Recording rules RED cho HTTP và gRPC (`nexusmart:http_requests:rate5m`, `:http_errors_5xx:rate5m`, `:http_error_ratio:{5m,30m,1h,6h}`, `:http_latency_p95:5m`, `:grpc_requests:rate5m`, `:grpc_errors:rate5m`, `:grpc_error_ratio:{5m,30m,1h,6h}`, `:grpc_latency_p95:5m`, `:slo_error_budget_remaining_ratio` theo SLO khả dụng 99.5%/30 ngày). Dùng `http_requests_total{status=~"5.."}` để đồng nhất giữa Go và Rust.
  - Alerting rules đa cửa sổ: `NexusMartSLOFastBurn` (burn rate 14.4x, 5m+1h), `NexusMartSLOSlowBurn` (burn rate 6x, 30m+6h), `NexusMartServiceDown`, `NexusMartDependencyDown`, `NexusMartHighLatencyP95`, `NexusMartHighCPU`, `NexusMartHighMemory`.
- **Alertmanager + Cảnh báo Telegram** (`deploy/observability/alertmanager/alertmanager.yml`, `deploy/observability/telegram-relay/`):
  - Alertmanager (`127.0.0.1:9093`) gộp cảnh báo theo `[alertname, service]`, `group_wait: 15s`, `group_interval: 1m`, `repeat_interval: 1h`, inhibit rule khi service down, định tuyến đồng thời tới `telegram-relay:8099` và `aiops:8092/api/v1/alerts/webhook`.
  - `telegram-relay` (Node 22 stdlib không dependency ngoài, `USER node`, cổng host `127.0.0.1:18099` tránh xung đột cổng host `8099`): tự động chạy chế độ **mock receiver** mặc định khi `TELEGRAM_BOT_TOKEN`/`TELEGRAM_CHAT_ID` trống (`GET /healthz`, `GET /alerts`, `POST /webhook`), và gửi Telegram Bot API kèm timeout + retry backoff + che token khi cung cấp biến môi trường thật.
- **Loki + Promtail** (`deploy/observability/loki/loki-config.yaml`, `deploy/observability/promtail/promtail-config.yaml`, cổng `127.0.0.1:3100`):
  - Loki `3.1.1` TSDB v13, lưu giữ 30 ngày (`720h`) đúng đặc tả.
  - Promtail `3.1.1` thu log Docker lọc riêng project `nexus_ecom`, chuẩn hoá JSON của cả Go (`msg`, `service`, `trace_id`) và Rust (`message`, `span.trace_id`) với nhãn `service`, `level` và structured metadata `trace_id`/`span_id`.
- **Tempo** (`deploy/observability/tempo/tempo.yaml`, cổng `127.0.0.1:3200`):
  - Tempo `2.6.1` nhận OTLP gRPC `4317` / HTTP `4318`, bật API search trace.
  - Cung cấp kèm override tuỳ chọn `deploy/observability/otel-collector-obs.yaml` + `deploy/observability/compose.otel-tempo.yml` để nối `otel-collector -> tempo:4317` mà không sửa `infra/otel-collector-config.yaml` của stack chung.
- **Grafana** (`deploy/observability/grafana/`, cổng `127.0.0.1:3400`):
  - Auto-provision 3 datasource: Prometheus (mặc định), Loki (derived field `trace_id -> Tempo`), Tempo (`tracesToLogsV2 -> Loki`).
  - Auto-provision 4 dashboard JSON hợp lệ (thư mục `NexusMart`):
    1. `service-red-overview.json`: RED + SLO error budget + CPU/RAM + lỗi Loki theo từng service (`$service`).
    2. `flow-checkout.json`: Luồng nghiệp vụ Checkout & Order Saga (`gateway`, `cart`, `order`, `product`, `shipping`).
    3. `flow-payment.json`: Luồng nghiệp vụ Thanh toán (`payment` RED, `payment_webhooks_total`, `payment_ledger_transactions_total`).
    4. `flow-flash-sale.json`: Luồng nghiệp vụ Flash Sale & Khuyến mãi (`gateway`, `promotion`, `product`).

### 1.2. Audit log JSON, che PII và trace xuyên gRPC/NATS
- Kiểm tra trực tiếp mã nguồn toàn bộ 9 service lõi + `web` BFF + `pkg/common` + `infra/otel-collector-config.yaml`.
- Ghi đầy đủ **14 phát hiện có `file:line`, kịch bản tái hiện và swarm sở hữu** (`OBS-01` đến `OBS-14`) vào [docs/reports/issues.md](issues.md), bao gồm các lỗ hổng trọng yếu:
  - `OBS-01` (High, Core/Platform): `infra/otel-collector-config.yaml` chỉ export trace ra `debug`, chưa gửi tới `tempo:4317`.
  - `OBS-02` (High, Swarm D): Rust `payment` chưa tạo OTel span phân tán và chưa propagate W3C `traceparent` xuyên HTTP/gRPC/NATS.
  - `OBS-03` (High, Core/Platform): `pkg/common/logger.ScrubSecrets` chưa che email/SĐT/JWT nhúng trong chuỗi `msg`/`error` và thiếu một số key PII (`bank_account`, `full_name`, `address`, `cccd`, ...).
  - `OBS-04..OBS-14` (Medium/Low): thiếu `"service"` trong JSON log của `payment`, rơi `trace_id` ở nhánh lỗi không truyền `ctx` (`promotion`, `shipping`, `order`, `web`), consumer NATS chưa mở span (`shipping`, `notification`), thiếu `process_*` metrics ở 4 service, lệch định nghĩa `http_errors_total` ở `payment`, raw OTP trong DLQ `notification`.

### 1.3. AIOps Engine (`services/aiops/` – Rust, Tokio, reqwest, Axum)
- Chạy tại `127.0.0.1:8092` (image multi-stage `rust:1-slim-bookworm -> debian:bookworm-slim`, `USER 10001:10001`, mức tiêu thụ thực đo trên compose: **0.00% CPU, 20.83 MiB RAM**).
- **Phát hiện bất thường thống kê** (`src/detector.rs`):
  - Đọc Prometheus API (`/api/v1/query`) mỗi 10s cho error rate (HTTP 5xx + gRPC server error + HTTP 503), latency p95 (lọc nhiễu lưu lượng tối thiểu `> 0.35 req/s`), CPU (cAdvisor + fallback `process_cpu_seconds_total`), bộ nhớ (cAdvisor + fallback `process_resident_memory_bytes`), `up{job="nexusmart-services"}`, và probe trực tiếp NATS (`http://nats:8222/healthz` -> xuất metric `nexusmart_dependency_up{dependency="nats"}`).
  - Kết hợp cửa sổ trượt **Z-score** và **EWMA** với ngưỡng an toàn, khử trùng lặp incident, tự nâng cấp RCA khi xuất hiện tín hiệu mới nghiêm trọng hơn, tự đóng incident sau 2 chu kỳ khoẻ, và hỗ trợ cửa sổ bảo trì (`/api/v1/maintenance`).
- **Thu thập ngữ cảnh & che PII** (`src/context.rs`):
  - Gom log lỗi/cảnh báo từ Loki, trace chậm (`minDuration=200ms`) từ Tempo, lịch sử deploy (`/api/v1/deploys`) và trạng thái dependency; che email, SĐT, JWT, Bearer token, DSN, mật khẩu, API key trước khi lưu hoặc gửi LLM.
  - Mọi lời gọi HTTP ra ngoài dùng client chung (`src/http_client.rs`) có timeout, retry backoff và circuit breaker.
- **Phân tích nguyên nhân gốc bằng LLM** (`src/llm.rs`):
  - Interface `LlmProvider` với `LLM_MODE=mock|anthropic` (mặc định `mock`, model live mặc định `claude-opus-5-5`, giới hạn số lệnh/ngân sách ngày và cache TTL theo chữ ký sự cố).
- **Tự khắc phục an toàn & Adapter** (`src/remediator.rs`, `src/adapter.rs`):
  - Chỉ cho phép hành động trong allowlist (`restart`, `scale` 1..10 trên danh sách service cho phép), giới hạn số lần/giờ (`AIOPS_MAX_ACTIONS_PER_HOUR=3`), mặc định **`AIOPS_DRY_RUN=true`**, ghi audit log đầy đủ (`/api/v1/audit`).
  - Hai adapter thật đã kiểm thử với mock API server: `DockerComposeAdapter` (Docker Engine HTTP API qua Unix socket / TCP) và `KubernetesAdapter` (patch `deployments` restart annotation và `/scale`).

### 1.4. Tiêm lỗi & đo kiểm (`tools/chaos/`)
- 5 kịch bản trong `tools/chaos/`: `slow-payment` (`tc netem` qua `tools/chaos/netem.Dockerfile` + fallback `docker pause/unpause`), `stop-order`, `promotion-db-error` (cô lập riêng `promotion_db`, không ảnh hưởng DB service khác), `nats-outage`, `search-high-cpu` (tự nhận diện container thật `nexus_ecom-search-1`, có fallback `e-test-search`).
- Phối hợp tài nguyên dùng chung qua file khoá nguyên tử `.runs/stack.lock` (`O_CREAT|O_EXCL`, chờ khi swarm khác như F/H đang giữ, luôn mở khoá và khôi phục sạch trong signal/finally trap).

### 1.5. Tích hợp file dùng chung (chỉ THÊM)
- `docker-compose.yml`: thêm 9 service dưới `profiles: ["obs"]` (mọi cổng chỉ bind `127.0.0.1`) và 4 volume dữ liệu obs.
- `Makefile`: thêm kiểm tra `services/aiops` vào `lint` (`cargo fmt`, `cargo clippy -D warnings`) và `test` (`cargo test`), cùng target `obs-up`, `obs-down`, `chaos`.
- `.env.example`: bổ sung các biến cấu hình Swarm E với giá trị mock/placeholder.

---

## 2. Lệnh kiểm tra và đầu ra thật

### 2.1. Validate cấu hình Prometheus, Alertmanager, OTel Collector, Docker Compose
```text
$ docker run --rm --entrypoint /bin/promtool -v "$PWD/deploy/observability/prometheus:/etc/prometheus:ro" prom/prometheus:v2.54.1 check config /etc/prometheus/prometheus.yml
Checking /etc/prometheus/prometheus.yml
  SUCCESS: 1 rule files found
 SUCCESS: /etc/prometheus/prometheus.yml is valid prometheus config file syntax

$ docker run --rm --entrypoint /bin/promtool -v "$PWD/deploy/observability/prometheus:/etc/prometheus:ro" prom/prometheus:v2.54.1 check rules /etc/prometheus/rules/slo.yml
Checking /etc/prometheus/rules/slo.yml
  SUCCESS: 24 rules found

$ docker run --rm --entrypoint /bin/amtool -v "$PWD/deploy/observability/alertmanager:/etc/alertmanager:ro" prom/alertmanager:v0.27.0 check-config /etc/alertmanager/alertmanager.yml
Checking '/etc/alertmanager/alertmanager.yml'  SUCCESS
Found:
 - global config
 - route
 - 1 inhibit rules
 - 2 receivers
 - 0 templates

$ docker compose --profile obs config --quiet && echo "COMPOSE_OBS_CONFIG_OK"
COMPOSE_OBS_CONFIG_OK
$ docker compose config --quiet && echo "COMPOSE_DEFAULT_CONFIG_OK"
COMPOSE_DEFAULT_CONFIG_OK
```

### 2.2. Unit & Integration Tests (`telegram-relay`, `tools/chaos`, `services/aiops`)
```text
$ node --test deploy/observability/telegram-relay/server.test.mjs
✔ telegram-relay unit tests (72.535472ms)
ℹ tests 6
ℹ suites 1
ℹ pass 6
ℹ fail 0

$ python3 -m unittest discover -s tools/chaos -v
Ran 15 tests in 0.442s
OK

$ (cd services/aiops && cargo fmt --all -- --check && cargo clippy --all-targets -- -D warnings && cargo test)
running 9 tests (src/lib.rs) -> test result: ok. 9 passed; 0 failed
running 6 tests (tests/integration_tests.rs) -> test result: ok. 6 passed; 0 failed
```

### 2.3. Kiểm tra Stack Observability sống trên Docker Compose
- **Prometheus targets (16/16 `up`)**: `cadvisor`, `otel-collector`, `prometheus`, và đủ 13 `nexusmart-services` (`aiops`, `assistant`, `cart`, `chat`, `gateway`, `notification`, `order`, `payment`, `product`, `promotion`, `search`, `shipping`, `user`).
- **Grafana (`127.0.0.1:3400`)**: `"database": "ok"`, `version: "11.2.0"`; 3 datasource (`Prometheus`, `Loki`, `Tempo`); 4 dashboard (`nexusmart-service-red`, `nexusmart-flow-checkout`, `nexusmart-flow-payment`, `nexusmart-flow-flash-sale`).
- **Loki (`127.0.0.1:3100`) & Tempo (`127.0.0.1:3200`) & Alertmanager (`127.0.0.1:9093`)**: tất cả `healthy`/`ready`.
- **Telegram Relay mock receiver (`127.0.0.1:18099`)**: `{"status":"ok","service":"telegram-relay","mode":"mock"}`, đã nhận và lưu **98** cảnh báo thật từ Alertmanager và AIOps (`NexusMartSLOSlowBurn`, `AIOps_cpu_saturation`, `AIOps_message_broker_outage`, ...).
- **Non-root Docker images & cổng public**:
  - `nexus_ecom-aiops`: `USER=10001:10001`
  - `nexus_ecom-telegram-relay`: `USER=node`
  - Chỉ `gateway` (`0.0.0.0:28080`) và `web` (`0.0.0.0:3300`) mở public; mọi cổng của Swarm E chỉ bind `127.0.0.1`.

### 2.4. Kiểm tra chính sách an toàn Remediation sống trên AIOps (`127.0.0.1:8092`)
```text
1. Disallowed action (drop_db): HTTP 403 {"error":{"code":"blocked_allowlist",...}}
2. Disallowed service (postgres): HTTP 403 {"error":{"code":"blocked_allowlist",...}}
3.1..3.3 Allowed dry-run restart (search): HTTP 200 {"mode":"dry_run","outcome":"dry_run_skipped"} (x3)
3.4 Allowed dry-run restart attempt 4: HTTP 429 {"error":{"code":"rate_limited","message":"service 'search' reached hourly remediation limit (3/3)"}}
4. Feedback POST: HTTP 200
5. Audit summary: {"total":18,"outcomes":{"blocked_allowlist":2,"blocked_rate_limit":1,"confirmed_correct":1,"dry_run_skipped":3,"recorded_no_open_incident":11}}
```

### 2.5. Kết quả chạy thật 5 kịch bản Chaos trên Compose (`.runs/chaos-results.json`, `.runs/chaos-results.md`)
Lệnh: `./tools/chaos/run.sh --output .runs/chaos-results.json --cooldown 35 --detect-timeout 120`
(Đã chờ Swarm H giải phóng `.runs/stack.lock`, khoá `owner=E`, chạy 5 kịch bản thật tuần tự và mở khoá sạch khi xong.)

| Kịch bản | Mục tiêu thực tế | Thời gian phát hiện (s) | < 120s? | Nguyên nhân AIOps dự đoán | Nguyên nhân thật | Đúng RCA? | Thời gian khôi phục (s) |
|---|---|---:|:---:|---|---|:---:|---:|
| `slow-payment` | `nexus_ecom-payment-1` (`tc netem 650ms`) | **20.2** | PASS | `payment` (`latency_degradation`) | `payment` (`latency_degradation`) | **PASS** | 1.1 |
| `stop-order` | `nexus_ecom-order-1` (`docker stop`) | **15.2** | PASS | `order` (`service_unavailable`) | `order` (`service_unavailable`) | **PASS** | 6.2 |
| `promotion-db-error` | `nexus_ecom-promotion-1` (`promotion_db` isolated) | **20.1** | PASS | `promotion` (`database_error`) | `promotion` (`database_error`) | **PASS** | 0.1 |
| `nats-outage` | `nexus_ecom-nats-1` (`docker stop`) | **10.5** | PASS | `nats` (`message_broker_outage`) | `nats` (`message_broker_outage`) | **PASS** | 6.6 |
| `search-high-cpu` | `nexus_ecom-search-1` (container thật của Swarm A/H) | **30.1** | PASS | `search` (`cpu_saturation`) | `search` (`cpu_saturation`) | **PASS** | 0.1 |

- **Phát hiện < 2 phút**: **5/5 (100%)** — trung bình **19.2s**, chậm nhất **30.1s** (mục tiêu: `< 120s`).
- **Độ chính xác RCA**: **5/5 (100%)** (mục tiêu: `>= 4/5`).
- **Khôi phục & dọn dẹp**: **5/5 (100%)**, không để lại lỗi hay `.runs/stack.lock`.

---

## 3. Đối chiếu tiêu chí đo trong đặc tả (`docs/spec.md`)

| Tiêu chí đo | Yêu cầu | Kết quả thực tế | Trạng thái |
|---|---|---|---|
| Stack giám sát (Prometheus, Loki, Tempo, Grafana) qua profile `obs` | Đủ metric, log, trace, dashboard theo service & 3 luồng nghiệp vụ | 16/16 target Prometheus `up`, Loki thu log JSON, Tempo `ready`, Grafana có 3 datasource & 4 dashboard | **Đạt** |
| SLO và cảnh báo theo error budget dưới dạng code | Lưu dưới dạng code trong repo, cảnh báo Telegram (mock khi không có token) | 24 rules trong `deploy/observability/prometheus/rules/slo.yml` (`promtool` sạch), Alertmanager + `telegram-relay` mock nhận 98 alerts | **Đạt** |
| Kiểm tra log JSON có `trace_id`, che PII, trace xuyên gRPC & NATS | Phát hiện và báo lỗi cho swarm sở hữu | Đã audit đủ 9 service + `web` + `pkg/common` + `infra`, ghi 14 lỗi (`OBS-01..OBS-14`) vào `docs/reports/issues.md` | **Đạt** |
| AIOps Engine (Rust, Tokio, reqwest): Z-score + EWMA + Context + LLM + Remediation | Allowlist (`restart`, `scale`), giới hạn/giờ, `dry-run` mặc định, adapter Compose & K8s | Đầy đủ, 15/15 test xanh, 0 cảnh báo clippy, smoke live đạt 403/200/429, RAM ~21 MiB | **Đạt** |
| Chaos 5 kịch bản đo thật trên Compose | Phát hiện < 2 phút, đúng nguyên nhân >= 4/5, phối hợp `.runs/stack.lock` | **5/5 phát hiện < 31s**, **5/5 đúng nguyên nhân**, phối hợp `.runs/stack.lock` thực tế với Swarm F & H | **Đạt** |

## 4. Phần chưa làm / Giới hạn trung thực

1. **Trace thật từ các service vào Tempo khi chạy stack mặc định**: Vì `infra/otel-collector-config.yaml` nằm ngoài quyền sở hữu của Swarm E và hiện chỉ xuất trace sang `debug` (`OBS-01` trong `docs/reports/issues.md`), Tempo ở cấu hình mặc định chưa nhận span trực tiếp từ các service trừ khi bật kèm override `docker compose -f docker-compose.yml -f deploy/observability/compose.otel-tempo.yml --profile obs up -d` (hoặc Core cập nhật `infra/otel-collector-config.yaml`).
2. **Chế độ LLM live (`LLM_MODE=anthropic`) và Telegram live**: Đã cài đặt đầy đủ HTTP client (timeout, retry backoff, circuit breaker, giới hạn ngân sách ngày, cache TTL) và kiểm thử bằng local mock HTTP server trong unit/integration test; chưa gọi API Anthropic / Telegram thật do không có key thật trong repo (đúng hợp đồng `m-all-contract.md`).
3. **Thao tác `scale` thực thi trên Docker Compose đơn lẻ**: `KubernetesAdapter` hỗ trợ đầy đủ cả `restart` và `scale` qua K8s REST API; `DockerComposeAdapter` hỗ trợ `restart` qua Docker Engine API và `scale` khi bật `DOCKER_SWARM_MODE=true` (trả mã trạng thái rõ ràng nếu chạy Compose đơn node không bật Swarm).
4. **Dọn dẹp ổ đĩa**: Đã xoá `services/aiops/target` (2.1 GB), `tools/chaos/__pycache__` và image tạm `e-test-netem:local` sau khi kiểm thử xong (giữ lại `tools/chaos/netem.Dockerfile` để tái tạo nhanh khi chạy lại `slow-payment` bằng `tc netem`; vẫn có fallback `docker pause/unpause` tự động khi không build image này). Các container profile `obs` đang chạy khoẻ để hỗ trợ Swarm H lập báo cáo tổng hợp và demo.
