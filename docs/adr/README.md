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

Chạy skill `evaluate-oss` và mở ADR mới khi bắt đầu triển khai từng năng lực dưới đây. Danh sách ứng viên chỉ là điểm xuất phát, chưa tra web kiểm trạng thái trong đợt này:

- **Load balancer và ingress khi triển khai**: Gateway API với Envoy Gateway, Traefik; bare-metal thêm MetalLB hoặc Cilium.
- **Tìm kiếm sản phẩm**: Meilisearch, OpenSearch.
- **Object storage cho ảnh sản phẩm**: SeaweedFS, Garage (qua S3 API).
- **Backend log/metric/trace**: Prometheus, Loki, Tempo, Grafana (nối vào OpenTelemetry Collector có sẵn tại `infra/otel-collector-config.yaml`).
- **Phân quyền chi tiết**: OpenFGA.
- **Triển khai**: Helm, Argo CD.
- **Component tương tác phức tạp ở web (dialog, combobox, date picker)**: Radix, React Aria, Base UI (headless, dùng với CSS Modules).
