# Architecture Decision Records (ADR)

Nơi triển khai (Kubernetes tự host hay cloud) **chưa chốt** — đây là quyết định đầu tiên cần viết ADR trước khi chốt các thành phần hạ tầng phụ thuộc.

Mẫu ADR: [0000-template.md](0000-template.md).

## Chỉ mục ADR (Nhóm A: phần đã tự viết — giữ hay thay)

| Số | Tên | Trạng thái | Ngày |
| --- | --- | --- | --- |
| [0001](0001-gateway-keep-or-replace.md) | API Gateway tự viết bằng `chi` — giữ nguyên, thêm edge gateway hay thay hẳn | Đề xuất | 2026-11-22 |
| [0002](0002-auth-keep-or-identity-provider.md) | Auth trong `services/user` — giữ tự viết hay chuyển sang Identity Provider | Đề xuất | 2026-11-22 |
| [0003](0003-saga-outbox-keep-or-temporal.md) | Saga và Outbox trong `services/order` — giữ tự viết hay chuyển sang Temporal | Đề xuất | 2026-11-22 |

## Nhóm B: năng lực chưa có

| Số | Tên | Trạng thái | Ngày |
| --- | --- | --- | --- |
| [0004](0004-search-service-hybrid-vector.md) | Search service và tìm kiếm hybrid (từ khoá + vector) | Đề xuất | 2026-11-23 |
| [0005](0005-realtime-chat-and-object-storage.md) | Chat thời gian thực và Object Storage cho media | Đề xuất | 2026-11-23 |
| [0006](0006-ai-assistant-llm-rag-guardrails.md) | Trợ lý AI — nhà cung cấp LLM, embedding tiếng Việt, RAG và guardrail | Đề xuất | 2026-11-23 |
| [0007](0007-observability-and-aiops-engine.md) | Hệ thống giám sát (Observability) và AIOps engine bằng Rust | Đề xuất | 2026-11-23 |
| [0008](0008-kubernetes-cicd-and-backup.md) | Nơi triển khai Kubernetes, CI/CD và sao lưu cơ sở dữ liệu | Đề xuất | 2026-11-23 |

Chạy skill `evaluate-oss` và mở ADR mới khi bắt đầu triển khai từng năng lực dưới đây. Danh sách ứng viên chỉ là điểm xuất phát, chưa tra web kiểm trạng thái trong đợt này:

- **Load balancer và ingress khi triển khai**: Gateway API với Envoy Gateway, Traefik; bare-metal thêm MetalLB hoặc Cilium.
- **Tìm kiếm sản phẩm**: Meilisearch, OpenSearch.
- **Object storage cho ảnh sản phẩm**: SeaweedFS, Garage (qua S3 API).
- **Backend log/metric/trace**: Prometheus, Loki, Tempo, Grafana (nối vào OpenTelemetry Collector có sẵn tại `infra/otel-collector-config.yaml`).
- **Phân quyền chi tiết**: OpenFGA.
- **Triển khai**: Helm, Argo CD.
- **Component tương tác phức tạp ở web (dialog, combobox, date picker)**: Radix, React Aria, Base UI (headless, dùng với CSS Modules).
