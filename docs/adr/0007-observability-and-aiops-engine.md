# ADR-0007: Hệ thống giám sát (Observability) và AIOps engine bằng Rust

- **Trạng thái**: Đề xuất
- **Ngày**: 2026-11-23

## Bối cảnh

- **Hiện trạng trong repo** (`docs/audit/plt.md` `PLT-B3`, `PLT-B4`, `PLT-B5`, `PLT-B7`; `infra/otel-collector-config.yaml`):
  - Cả 9 service backend đã có `/healthz`, `/readyz`, `/metrics` (riêng `user:8081/metrics` thiếu `http_requests_total`/`http_request_duration_seconds` — `PLT-B7`).
  - `otel-collector` (`otel/opentelemetry-collector-contrib:0.108.0`) đang nhận OTLP trace (`:4317/:4318`) nhưng chỉ xuất ra `debug`; **cổng Prometheus `:8889/metrics` hoàn toàn rỗng (0 byte, `PLT-B3`)** vì 9 service xuất Prometheus `/metrics` trực tiếp chứ không đẩy OTLP metrics, còn collector không cấu hình receiver `prometheus` để scrape.
  - 6/8 service domain chưa log access gRPC kèm `trace_id` và `payment` (Rust) chưa gắn W3C `traceparent` vào parent span OTel (`PLT-B4`); chưa có Prometheus, Grafana, Loki, Tempo, Alertmanager, Telegram, SLO-as-code, hay `services/aiops`.
- **Use case & tiêu chí đo trích từ `docs/spec.md`**:
  - `UC-OPS01` (Cao): dashboard Grafana theo service (RED: rate, errors, duration, CPU, RAM) và theo luồng nghiệp vụ; tra log Loki theo service + `trace_id`; mở trace Tempo xuyên gRPC/NATS; cảnh báo khi service mất dữ liệu (`no data`).
  - `UC-OPS02` (Cao): job AIOps đọc metric từ Prometheus theo chu kỳ, phát hiện bất thường bằng **z-score và EWMA** trên error rate, p95 latency, CPU, RAM; tạo incident, gộp cảnh báo chống alert storm, tạm tắt khi đang deploy/bảo trì; Alertmanager gửi **Telegram**.
  - `UC-OPS03` (Trung bình): gom log lỗi + trace chậm + metric + lịch sử deploy quanh sự cố, **che PII/secret**, gọi **LLM phân tích nguyên nhân gốc (RCA)** kèm bằng chứng + link log/trace; DevOps xác nhận đúng/sai.
  - `UC-OPS04` (Thấp — đặc tả kiến trúc sẵn): đối chiếu runbook, đề xuất/tự chạy `restart pod` hoặc `scale replica` qua K8s API trong **allowlist**, **giới hạn số lần/giờ**, kiểm tra lại sau 5 phút, ghi audit log.
  - `UC-OPS07` (Trung bình): định nghĩa **SLO** (vd 99,5% / 30 ngày) và rule cảnh báo tốc độ tiêu **error budget** lưu dưới dạng code trong repo.
  - `UC-OPS08` (Cao): tiêm lỗi có chủ đích trên staging (tắt pod, làm chậm Payment, đầy RAM, ngắt DB) kết hợp tải giả lập (`k6`) để chứng minh mục tiêu: **phát hiện < 2 phút** và **RCA đúng ≥ 4/5 kịch bản**.
  - **Ngoài phạm vi**: APM thương mại, lưu log dài hạn > 30 ngày, huấn luyện deep learning, tự rollback có thay đổi DB, dự báo dung lượng (`docs/spec.md:33-34`).

## Ứng viên

### Phần A — Stack Observability (Metric / Log / Trace / Alert / SLO)

1. **Prometheus + Alertmanager + Grafana + Loki + Tempo + Sloth (khớp spec)**
   - **Phiên bản & bảo trì** (tra `2026-11-23`):
     - `prometheus/prometheus` `v3.16.0` (2026-11-04) — **Apache-2.0** (CNCF).
     - `prometheus/alertmanager` `v0.35.0` (2026-11-09) — **Apache-2.0** (tích hợp sẵn `telegram_configs` không cần webhook trung gian).
     - `grafana/grafana` `v13.3.1` — **AGPL-3.0** (OSS).
     - `grafana/loki` `v3.7.10` — **AGPL-3.0**.
     - `grafana/tempo` `v3.1.1` — **AGPL-3.0**.
     - `slok/sloth` `v0.16.0` — **Apache-2.0** (CLI/controller sinh Prometheus multi-window multi-burn-rate SLO rules từ YAML).
   - **Vận hành & K8s**: Trên K8s cài qua Helm chart chuẩn `kube-prometheus-stack` + `loki` (chế độ `SingleBinary`) + `tempo` (chế độ monolithic); trên dev Compose chạy gọn trong profile `--profile obs` (~1.2–1.8 GB RAM tổng cho lưu giữ 7–14 ngày). Liên kết 1-click Metric ↔ Trace (`exemplars`/Tempo) ↔ Log (`trace_id` trên Loki). Khớp chuẩn PromQL HTTP API cho `services/aiops`.

2. **VictoriaMetrics + VictoriaLogs + Tempo + Alertmanager + Grafana**
   - **Phiên bản & bảo trì**: `VictoriaMetrics/VictoriaMetrics` `v1.156.0` (2026-11-09), `VictoriaMetrics/VictoriaLogs` `v1.52.x` LTS — cả hai đều **Apache-2.0**, viết bằng Go, rất tích cực.
   - **Vận hành & K8s**: Tiết kiệm 40–60% RAM (~0.6–1.0 GB tổng), tương thích PromQL query API cho `services/aiops`; tuy nhiên VictoriaLogs dùng ngôn ngữ truy vấn LogsQL riêng (cần plugin Grafana ngoài) và lệch công cụ Loki nêu trong `UC-OPS01`.

3. **SigNoz (All-in-one trên ClickHouse + OTel)**
   - **Phiên bản & bảo trì**: `SigNoz/signoz` `v0.150.0` (2026-11) — **MIT** (core) / Enterprise license cho thư mục `ee/`.
   - **Vận hành & K8s**: Một UI duy nhất nhận thẳng OTLP metric/log/trace từ `otel-collector`, nhưng **ClickHouse ngốn 2–4 GB RAM ngay cả khi nhàn rỗi** và không phơi PromQL/Prometheus API nguyên bản như `UC-OPS02`/`UC-OPS07` yêu cầu.

#### Bảng so sánh — Observability Stack

| Tiêu chí | Prometheus + Alertmanager + Grafana + Loki + Tempo | VictoriaMetrics + VictoriaLogs + Tempo + Grafana | SigNoz (ClickHouse) |
| --- | --- | --- | --- |
| Độ khớp tiêu chí (`UC-OPS01/02/07`, PromQL, Telegram, SLO) | **Xuất sắc** (khớp 100% tên & yêu cầu spec, Sloth sinh burn-rate chuẩn) | Rất tốt (tương thích PromQL; LogsQL khác Loki) | Trung bình (không có PromQL/Alertmanager native) |
| Độ phức tạp vận hành | Trung bình (4 thành phần ở chế độ single-binary + Helm chuẩn) | Thấp–trung bình (nhẹ RAM, thêm plugin VictoriaLogs) | Trung bình–cao (quản trị ClickHouse + ZooKeeper/Keeper) |
| Chi phí tài nguyên (demo 7–14 ngày) | ~1.2–1.8 GB RAM | **~0.6–1.0 GB RAM** | 2.5–4.5 GB RAM |
| Giấy phép | Prometheus/Alertmanager/Sloth **Apache-2.0**; Grafana/Loki/Tempo **AGPL-3.0** | VM/VL **Apache-2.0**; Grafana/Tempo **AGPL-3.0** | **MIT** (core) + Enterprise (`ee/`) |
| Mức độ trưởng thành | **Rất cao** (chuẩn ngành CNCF) | Cao | Trung bình–cao |

---

### Phần B — Xử lý lỗi `PLT-B3` (cổng `:8889` rỗng) và đường thu thập telemetry

- **Phương án 1 (đề xuất): Prometheus scrape trực tiếp `/metrics` của từng service** (`gateway:8080`, `user:8081`…), dùng `ServiceMonitor` trên K8s và `static_configs` trên Compose; **gỡ pipeline metrics rỗng `:8889` trong `infra/otel-collector-config.yaml`** (chỉ giữ `:8888` internal metrics của chính collector). `otel-collector` tập trung nhận **OTLP Traces → Tempo** và **Logs (OTLP hoặc thu stdout qua Alloy/Fluent Bit) → Loki**.
- **Phương án 2**: Thêm receiver `prometheus` vào `otel-collector` để scrape 9 service rồi phơi lại trên `:8889` cho Prometheus scrape tiếp. Cách này giữ nguyên cổng `:8889` nhưng thêm 1 bước nhảy dư thừa và dễ làm lệch label `instance`/`pod` khi chạy trên K8s.

---

### Phần C — Vị trí kiến trúc AIOps Engine Rust (`services/aiops`) và công cụ tiêm lỗi (`UC-OPS08`)

1. **Vị trí mã nguồn & triển khai `services/aiops` (Rust)**:
   - **Trong monorepo**: `services/aiops/` (workspace Rust tách biệt tương tự `services/payment/`, Dockerfile đa giai đoạn từ root repo, chạy non-root, dùng `tokio`, `reqwest`, `sqlx`, `kube` crate từ `kube-rs` Apache-2.0, `prometheus`, `tracing-subscriber`).
   - **Cơ sở dữ liệu riêng**: `aiops_db` trên PostgreSQL 16 (bảng `incidents`, `anomaly_samples`, `rca_reports`, `runbook_executions`, `deploy_events`, `maintenance_windows`).
   - **Vị trí trên Kubernetes**: chạy tại **namespace riêng `observability`** (tách khỏi `nexusmart-staging` và `nexusmart-prod` để sự cố ứng dụng không làm chết bộ phát hiện sự cố).
   - **An toàn runbook (`UC-OPS04`)**: ServiceAccount dùng **Role/RoleBinding tối thiểu theo allowlist** (chỉ `get/list` pods/deployments, `delete` pods, `patch` `deployments/scale` trong danh sách service stateless được duyệt; tuyệt đối không cấp quyền trên `postgres`, `redis`, `nats`, `Secret` hay `cluster-admin`); giới hạn cứng e.g. tối đa **2 lần/giờ/service**, cooldown 10 phút, và bắt buộc ghi `runbook_executions` (append-only).
   - **Kiến trúc luồng xử lý**:
     - `Detector loop` (mỗi 15–30s): truy vấn Prometheus HTTP API (`/api/v1/query_range`) cho `error_rate`, `p95_latency`, `pod_cpu`, `pod_memory`; tính **z-score** trên cửa sổ nền trượt (e.g. 60 phút hoặc cùng giờ ngày trước) + **EWMA** chống nhiễu gai đơn lẻ; chỉ mở incident khi vượt ngưỡng ≥ 2 chu kỳ liên tiếp (tổng phát hiện ~30–60 giây, đạt chặt mục tiêu **< 2 phút**).
     - `Alert suppression`: tự động kiểm bảng `deploy_events`/`maintenance_windows` để giảm cảnh báo giả lúc rolling update, gộp nhiều tín hiệu cùng service vào 1 incident duy nhất, bắn cảnh báo qua Alertmanager webhook (`/api/v2/alerts`) → **Telegram**.
     - `RCA worker`: gom log `level=error` từ Loki API, trace chậm > p95 từ Tempo API, metric bất thường và bản deploy gần nhất → chạy bộ **PII/secret redactor** → gọi LLM API (`ADR-0006`) trả JSON cấu trúc `{hypothesis, confidence, evidence_links, suggested_action}` → lưu vào `rca_reports` và gửi bổ sung vào luồng Telegram; nếu LLM lỗi, incident vẫn giữ nguyên dữ liệu thô (`UC-OPS03`).

2. **Công cụ tiêm lỗi có chủ đích (`UC-OPS08`, chỉ chạy trên `staging`)**:
   - **Script kịch bản có kiểm soát + `Grafana k6 v2.4.0` (AGPL-3.0)** *(đề xuất mặc định)*: 5 kịch bản tái lập 100% bằng script trong `tools/chaos/` (`kubectl delete pod`, bật env/header tiêm độ trễ trên `payment` chỉ khi `FAULT_INJECT_ENABLED=true` ở staging, `NetworkPolicy` tạm ngắt kết nối DB, pod gây áp lực RAM, kịch bản 5xx) kết hợp `k6` tạo lưu lượng nền → nhẹ, minh bạch, 0 privileged DaemonSet.
   - **Chaos Mesh `v2.8.7` (Apache-2.0)** *(tùy chọn nâng cao)*: CNCF Incubating, khai báo fault bằng CRD rất bài bản, nhưng yêu cầu DaemonSet quyền cao (`CAP_SYS_PTRACE`/`NET_ADMIN`) và tốn thêm tài nguyên trên cụm demo nhỏ.

## Quyết định

- **Đề xuất**:
  1. Chọn **Prometheus `v3.16.0` + Alertmanager `v0.35.0` (Telegram native) + Grafana `v13.3.1` + Loki `v3.7.10` + Tempo `v3.1.1` (chế độ single-binary, retention 14 ngày)** kết hợp **Sloth `v0.16.0`** sinh rule SLO/burn-rate dạng code trong `infra/monitoring/slo/`.
  2. Khắc phục `PLT-B3` bằng **Prometheus scrape trực tiếp `/metrics`** của tất cả service; `otel-collector` chuyển tiếp OTLP traces sang Tempo và logs sang Loki.
  3. Đặt AIOps engine tại **`services/aiops/` (Rust)**, triển khai ở **namespace `observability` tách biệt**, sở hữu DB `aiops_db`, tích hợp Alertmanager/Telegram và LLM API (`ADR-0006`), RBAC allowlist nghiêm ngặt cho runbook.
  4. Kiểm chứng `UC-OPS08` trên `staging` bằng **`k6` + bộ 5 kịch bản tiêm lỗi chuẩn hoá** trong `tools/chaos/` (chặn cứng không cho chạy trên `production`).
- **Điều kiện kích hoạt chuyển đổi**:
  - Chuyển từ Prometheus+Loki sang **VictoriaMetrics + VictoriaLogs** nếu máy demo cục bộ thiếu RAM (< 16 GB tổng) khi chạy đồng thời 12 service + observability stack.
  - Cài thêm **Chaos Mesh** nếu đồ án mở rộng thêm bài tiêm lỗi packet-loss/clock-skew tầng kernel.
- **Phụ thuộc**: `ADR-0006` (chọn LLM API và Secret cho RCA), `ADR-0008` (nơi triển khai K8s/Helm, namespace `staging`/`production`, đánh dấu deploy trên Grafana).
- **Chủ dự án quyết**: (trống)

## Hệ quả

- **Prometheus + Grafana + Loki + Tempo + Rust `aiops` (đề xuất)**:
  - *Tích cực*: bám sát 100% đặc tả `UC-OPS01..08`; hệ sinh thái dashboard/alert/Sloth phong phú; Rust `aiops` tiêu thụ rất ít RAM (~20–40 MB), không GC pause, tách biệt rõ phạm vi lỗi khỏi app.
  - *Tiêu cực*: Grafana/Loki/Tempo dùng **AGPL-3.0** (chấp nhận được vì dùng nội bộ cho vận hành, không chỉnh sửa phân phối lại thành dịch vụ cho khách); chạy đủ 4 thành phần tốn ~1.5 GB RAM; phải sửa dứt điểm `PLT-B4`/`PLT-B5`/`PLT-B7` trước thì dữ liệu đầu vào cho RCA mới đủ chính xác.

## Kế hoạch triển khai

| Task | Phạm vi | Tiêu chí nghiệm thu đo được |
| --- | --- | --- |
| `T1` Vá nền telemetry (`PLT-B3/B4/B5/B7`) | Bọc `red.HTTPMiddleware()` cho `user`; thêm gRPC logging interceptor có `trace_id` trong `pkg/common`; gắn W3C `set_parent` ở `services/payment` Rust; sửa thứ tự `RequestLogger` lấy `user_id` ở `gateway`; cấu hình `otel-collector` xuất traces → Tempo | 1 giao dịch checkout có cùng `trace_id` hiện đủ trong log của mọi service tham gia và nối thành **1 cây trace liền mạch** (gồm cả `payment`) trên Tempo; 100% service xuất đủ 6 RED metrics |
| `T2` Stack Giám sát + Dashboard + Telegram (`UC-OPS01`, `UC-OPS07`) | Dựng Prometheus, Alertmanager (`telegram_configs`), Loki, Tempo, Grafana (Compose profile `obs` + Helm); viết 2 nhóm dashboard (theo từng service & theo luồng Checkout/Payment/Search) lưu JSON trong `infra/monitoring/grafana/`; khai báo SLO YAML + `Sloth` sinh burn-rate alerts | Dashboard hiển thị live RED + CPU/RAM; bấm 1 `trace_id` nhảy từ Loki sang Tempo; tắt 1 service bắn cảnh báo `no data` về nhóm Telegram **< 2 phút** |
| `T3` AIOps Detector Rust (`UC-OPS02`) | Dựng `services/aiops/` (Rust, `aiops_db`, `/healthz`, `/readyz`, `/metrics`), vòng quét Prometheus 15–30s cài `z-score` + `EWMA`, gom nhóm incident, suppression theo `deploy_events`, đẩy cảnh báo sang Alertmanager → Telegram | Unit test thuật toán `z-score`/`EWMA` trên chuỗi thời gian giả lập; tiêm tăng error rate/latency phát hiện và tạo incident **< 2 phút**, không phát alert storm (gộp thành 1 incident) |
| `T4` AIOps LLM RCA & Runbook khung (`UC-OPS03`, `UC-OPS04`) | Cài trình gom ngữ cảnh (Loki + Tempo + Prometheus + deploy), PII/secret redactor, gọi LLM API trả RCA có link log/trace, API đánh dấu đúng/sai; khung runbook allowlist (`restart`/`scale` qua `kube-rs`, trần 2 lần/giờ, audit log, mặc định chế độ yêu cầu duyệt tay) | Unit test redactor che 100% email/SĐT/JWT/API key trước khi gọi LLM; khi LLM lỗi, incident vẫn lưu đủ bằng chứng thô; hành động ngoài allowlist bị từ chối cứng |
| `T5` Kịch bản tiêm lỗi & kiểm chứng định lượng (`UC-OPS08`) | Viết tải nền `k6` (`tools/k6/`) và **5 kịch bản tiêm lỗi trên staging** (`tools/chaos/`: tắt pod `order`, làm chậm `payment` +1.5s, lỗi 500 giả lập, đầy RAM pod, ngắt kết nối DB), xuất bảng tổng hợp thời gian phát hiện + độ chính xác RCA | Chạy đủ 5 kịch bản trên staging: **5/5 phát hiện < 2 phút**, **≥ 4/5 RCA đề xuất đúng nguyên nhân gốc**, xuất bảng kết quả cho báo cáo đồ án |

## Câu hỏi chủ dự án cần trả lời

1. **Chấp nhận dùng bộ Grafana/Loki/Tempo (AGPL-3.0) chạy nội bộ** để khớp đúng đặc tả `UC-OPS01`, hay ưu tiên thay Prometheus+Loki bằng **VictoriaMetrics + VictoriaLogs** (Apache-2.0, nhẹ RAM hơn gần một nửa)?
2. **Với `UC-OPS04` (ưu tiên Thấp)**: có muốn bật chế độ **tự động thực thi** `restart pod`/`scale` trên staging để quay video demo, hay chỉ để chế độ **đề xuất + DevOps bấm xác nhận** (human-in-the-loop)?
3. **Kênh Telegram cảnh báo**: dùng **1 bot + 1 group riêng cho staging và production** (phân biệt bằng tag/topic) để tránh nhầm lẫn khi chạy tiêm lỗi `UC-OPS08` trên staging?

## Ngày kiểm tra trạng thái ứng viên

- **Ngày kiểm tra**: 2026-11-23
- **Nguồn**:
  - Prometheus & Alertmanager: https://github.com/prometheus/prometheus/releases, https://github.com/prometheus/alertmanager/releases, https://prometheus.io/download/
  - Grafana, Loki, Tempo: https://github.com/grafana/grafana, https://github.com/grafana/loki, https://github.com/grafana/tempo
  - VictoriaMetrics & VictoriaLogs: https://github.com/VictoriaMetrics/VictoriaMetrics/releases, https://github.com/VictoriaMetrics/VictoriaLogs
  - SigNoz: https://github.com/SigNoz/signoz
  - Sloth & Pyrra: https://github.com/slok/sloth, https://github.com/prometheus-community/pyrra
  - `kube-rs`, Grafana `k6`, Chaos Mesh: https://github.com/kube-rs/kube, https://github.com/grafana/k6, https://github.com/chaos-mesh/chaos-mesh
