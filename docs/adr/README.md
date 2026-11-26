# Architecture Decision Records (ADR)

Nơi triển khai đã chốt **cục bộ trên `k3d`** (`$0`, không thuê VPS/cloud) theo [ADR-0008](0008-kubernetes-cicd-and-backup.md) (manager chốt ngày 2026-11-24 theo uỷ quyền của chủ dự án).

Mẫu ADR: [0000-template.md](0000-template.md).

## Chỉ mục ADR (Nhóm A: phần đã tự viết — giữ hay thay)

| Số | Tên | Trạng thái | Ngày |
| --- | --- | --- | --- |
| [0001](0001-gateway-keep-or-replace.md) | API Gateway tự viết bằng `chi` — giữ nguyên, thêm edge gateway hay thay hẳn | Đề xuất | 2026-11-22 |
| [0001-t](0001-site-template-format.md) | Định dạng Template cho Site Gian hàng (`Declarative Theme JSON v2`) | Chấp nhận | 2026-11-21 |
| [0002](0002-auth-keep-or-identity-provider.md) | Auth trong `services/user` — giữ tự viết hay chuyển sang Identity Provider | Đề xuất | 2026-11-22 |
| [0003](0003-saga-outbox-keep-or-temporal.md) | Saga và Outbox trong `services/order` — giữ tự viết hay chuyển sang Temporal | Đề xuất | 2026-11-22 |

## Nhóm B: năng lực chưa có

| Số | Tên | Trạng thái | Ngày |
| --- | --- | --- | --- |
| [0004](0004-search-service-hybrid-vector.md) | Search service và tìm kiếm hybrid (từ khoá + vector) | Chấp nhận | 2026-11-24 |
| [0005](0005-realtime-chat-and-object-storage.md) | Chat thời gian thực và Object Storage cho media | Chấp nhận | 2026-11-24 |
| [0006](0006-ai-assistant-llm-rag-guardrails.md) | Trợ lý AI — nhà cung cấp LLM, embedding tiếng Việt, RAG và guardrail | Chấp nhận | 2026-11-24 |
| [0007](0007-observability-and-aiops-engine.md) | Hệ thống giám sát (Observability) và AIOps engine bằng Rust | Chấp nhận | 2026-11-24 |
| [0008](0008-kubernetes-cicd-and-backup.md) | Nơi triển khai Kubernetes, CI/CD và sao lưu cơ sở dữ liệu | Chấp nhận | 2026-11-24 |
| [0009](0009-email-provider-resend.md) | Nhà cung cấp gửi email giao dịch cho `services/notification` (SMTP / Resend) | Đề xuất, chờ chủ dự án duyệt | 2026-11-26 |
| [0011](0011-shadcn-ui-tailwind-v4.md) | Hệ thống UI `shadcn/ui` + Tailwind CSS v4 cho `web/` | Đã duyệt (chủ dự án, 2026-11-26) | 2026-11-26 |

## Kiến trúc hợp nhất nhánh

| Số | Tên | Trạng thái | Ngày |
| --- | --- | --- | --- |
| [0010](0010-unify-main-and-phase-0b.md) | Hợp nhất nhánh `main` (kèm `3b-3`) và nhánh `phase-0b` | Đã duyệt (chủ dự án, 2026-11-26) | 2026-11-25 |

> ADR 0004–0008 chuyển sang **Chấp nhận** ngày `2026-11-24` theo uỷ quyền của chủ dự án trong `.agents/briefs/fix/w3-plan.md` §1.1 (chủ dự án không duyệt riêng từng ADR) sau khi tra lại phiên bản và bảo mật. Các cổng còn giữ (push, khoá/tiền/lần gọi thật LLM, sandbox thật, token Telegram, đo RAM B4, tra lại GHSA SeaweedFS trước B2) ghi chi tiết trong từng ADR.

Chạy skill `evaluate-oss` và mở ADR mới khi bắt đầu triển khai từng năng lực dưới đây. Danh sách ứng viên chỉ là điểm xuất phát, chưa tra web kiểm trạng thái trong đợt này:

- **Load balancer và ingress khi triển khai**: Gateway API với Envoy Gateway, Traefik; bare-metal thêm MetalLB hoặc Cilium.
- **Tìm kiếm sản phẩm**: Meilisearch, OpenSearch.
- **Object storage cho ảnh sản phẩm**: SeaweedFS, Garage (qua S3 API).
- **Backend log/metric/trace**: Prometheus, Loki, Tempo, Grafana (nối vào OpenTelemetry Collector có sẵn tại `infra/otel-collector-config.yaml`).
- **Phân quyền chi tiết**: OpenFGA.
- **Triển khai**: Helm, Argo CD.
- **Component tương tác phức tạp ở web (dialog, combobox, date picker)**: Radix, React Aria, Base UI (headless, dùng với CSS Modules).
